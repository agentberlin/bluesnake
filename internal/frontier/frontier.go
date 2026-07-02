// Package frontier is the crawl's admission control: URL dedup and every
// pre-fetch limit (depth, per-depth, folder depth, query strings, URL length,
// per-subdomain, per-path caps). Dedup is delegated to a Dedup authority — an
// exact in-memory set by default, or a store-backed set (frontier ∪ pages) that
// bounds RAM by keeping the visited set on disk (MEMORY-SCALING.md §5.1). The
// dedup authority is consulted OUTSIDE the cap mutex, so a store-backed authority
// can do its (microsecond) DB work without serialising the in-memory cap logic.
package frontier

import (
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/urlutil"
)

// Item is one admitted crawl candidate.
type Item struct {
	URL          string // normalized URL-encoded address
	Depth        int    // link hops from the seed; redirects count as a hop
	RedirectHops int    // consecutive redirect hops leading here
	Source       string // first discovering page ("" for seeds)
}

// Dedup is the admission authority. Admit must be ATOMIC and exactly-once: when
// two callers race the same URL, exactly one gets firstSeen=true. Remove undoes
// a just-admitted URL (the cap-overflow rollback). A nil/empty implementation is
// invalid; the frontier supplies an exact in-memory one by default.
type Dedup interface {
	Admit(it Item) (firstSeen bool, err error)
	Remove(url string) error
	Seen(url string) (bool, error)
	MarkSeen(urls []string) error
	Count() (int, error)
}

// Queue is the crawl's work-queue authority (issue #77, MEMORY-SCALING.md
// §5.2/§5.3): the store between an item's admission and its hand-off to a
// worker. The crawler's in-RAM ready-buffer is a bounded window over it,
// refilled by a single feeder goroutine, so per-crawl RAM does not scale with
// the discovered frontier when the authority is durable (the SQLite store).
// The engine falls back to an in-memory queue (frontier-linear by design) for
// library/test sinks with no persistence.
type Queue interface {
	// Enqueue publishes an item that passed dedup AND every cap check as
	// claimable work. The durable authority flips the born-claimed row Admit
	// wrote to claimable — the two-step publish is what keeps a row invisible
	// to the feeder until its admission is complete, so a cap-overflow
	// rollback (Remove) can never race the feeder into crawling the URL.
	Enqueue(it Item) error
	// ClaimBatch atomically claims up to n published items in (depth, seq)
	// order — deterministic BFS pull order — marking them in-flight so no
	// item is ever handed out twice (WP-08).
	ClaimBatch(n int) ([]Item, error)
	// Recover prepares the queue at Run start: the durable authority resets
	// orphaned in-flight claims (a crash's or pause's claimed rows, EC-01) so
	// they become claimable again; the in-memory authority — whose rows died
	// with the process — re-enqueues the caller-supplied pending items.
	Recover(pending []Item) error
}

// memDedup is the default exact in-memory dedup: a set of admitted URL strings.
type memDedup struct {
	mu   sync.Mutex
	seen map[string]bool
}

func newMemDedup() *memDedup { return &memDedup{seen: map[string]bool{}} }

func (m *memDedup) Admit(it Item) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.seen[it.URL] {
		return false, nil
	}
	m.seen[it.URL] = true
	return true, nil
}

func (m *memDedup) Remove(url string) error {
	m.mu.Lock()
	delete(m.seen, url)
	m.mu.Unlock()
	return nil
}

func (m *memDedup) Seen(url string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.seen[url], nil
}

func (m *memDedup) MarkSeen(urls []string) error {
	m.mu.Lock()
	for _, u := range urls {
		m.seen[u] = true
	}
	m.mu.Unlock()
	return nil
}

func (m *memDedup) Count() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.seen), nil
}

type Frontier struct {
	cfg   *config.Config
	dedup Dedup
	onErr func(error) // receives every dedup-authority error; never nil

	mu       sync.Mutex
	perDepth map[int]int
	perSub   map[string]int
	perPath  []int // counters parallel to cfg.Limits.ByPath
}

// Option configures a Frontier.
type Option func(*Frontier)

// WithDedup supplies a store-backed dedup authority (e.g. the SQLite frontier ∪
// pages set). A nil dedup keeps the default exact in-memory set.
func WithDedup(d Dedup) Option {
	return func(f *Frontier) {
		if d != nil {
			f.dedup = d
		}
	}
}

// WithErrorSink routes dedup-authority errors to the caller (the crawler wires
// its sink-error latch here, so a store failure fails the run). Admission
// decisions stay conservative regardless — an erroring URL is declined — but
// the error is never swallowed: silently dropping a URL on a transient store
// error would report an incomplete crawl as success (#74 R6/D4). A nil fn
// keeps errors ignored (bare library/test use).
func WithErrorSink(fn func(error)) Option {
	return func(f *Frontier) {
		if fn != nil {
			f.onErr = fn
		}
	}
}

func New(cfg *config.Config, opts ...Option) *Frontier {
	f := &Frontier{
		cfg:      cfg,
		dedup:    newMemDedup(),
		onErr:    func(error) {},
		perDepth: make(map[int]int),
		perSub:   make(map[string]int),
		perPath:  make([]int, len(cfg.Limits.ByPath)),
	}
	for _, o := range opts {
		o(f)
	}
	return f
}

// noteErr reports a dedup-authority error (nil-safe convenience).
func (f *Frontier) noteErr(err error) {
	if err != nil {
		f.onErr(err)
	}
}

// Admit reports whether the item passes dedup and all configured limits, and
// records it. An admitted item is the caller's to crawl; rejected items are
// silently dropped (matching Screaming Frog: over-limit URLs are not reported).
func (f *Frontier) Admit(it Item) bool {
	lim := &f.cfg.Limits

	// Cheap lock-free pre-gates (no dedup state touched, so a rejection here
	// leaves nothing to roll back).
	if lim.MaxURLLength > 0 && len(it.URL) > lim.MaxURLLength {
		return false
	}
	if lim.MaxDepth >= 0 && it.Depth > lim.MaxDepth {
		return false
	}
	if lim.MaxQueryStrings >= 0 && urlutil.QueryParamCount(it.URL) > lim.MaxQueryStrings {
		return false
	}
	if lim.MaxFolderDepth >= 0 && urlutil.FolderDepth(it.URL) > lim.MaxFolderDepth {
		return false
	}

	// Dedup via the authority, OUTSIDE the cap mutex. A store-backed authority
	// does its DB work here without blocking the in-memory cap accounting; a
	// duplicate (the common high-fan-out case) returns immediately. On a dedup
	// error we conservatively decline (never double-admit) AND surface the
	// error — the URL is lost to this crawl, which must not read as success.
	first, err := f.dedup.Admit(it)
	if err != nil {
		f.noteErr(err)
		return false
	}
	if !first {
		return false
	}

	// Per-bucket caps + counter bumps, serialized so two workers can't both pass
	// a cap at its boundary. A novel URL that overflows a cap is rolled back out
	// of the dedup set (so it can be admitted later if room opens) — caps are
	// monotonic, so in practice it stays rejected, but the set never leaks it.
	// A FAILED rollback is surfaced too: it leaks a durable frontier row that a
	// later resume would crawl cap-free (via Readmit).
	f.mu.Lock()
	if lim.MaxURLsPerDepth >= 0 && f.perDepth[it.Depth] >= lim.MaxURLsPerDepth {
		f.mu.Unlock()
		f.noteErr(f.dedup.Remove(it.URL))
		return false
	}
	host := urlutil.Host(it.URL)
	if lim.MaxPerSubdomain >= 0 && f.perSub[host] >= lim.MaxPerSubdomain {
		f.mu.Unlock()
		f.noteErr(f.dedup.Remove(it.URL))
		return false
	}
	pathIdx := -1
	for i, pl := range lim.ByPath {
		if strings.Contains(it.URL, pl.Pattern) {
			if f.perPath[i] >= pl.Max {
				f.mu.Unlock()
				f.noteErr(f.dedup.Remove(it.URL))
				return false
			}
			pathIdx = i
			break
		}
	}
	f.perDepth[it.Depth]++
	f.perSub[host]++
	if pathIdx >= 0 {
		f.perPath[pathIdx]++
	}
	f.mu.Unlock()
	return true
}

// Readmit re-records an already-admitted item (a resume's pending frontier row)
// without consuming any limit budget — it was counted in the session that first
// admitted it. It always reports true; the caller re-queues the item for crawling.
func (f *Frontier) Readmit(it Item) bool {
	// ensure the authority knows it (no-op if already present); an error means
	// the durable frontier row may be gone — surface it
	_, err := f.dedup.Admit(it)
	f.noteErr(err)
	return true
}

// RehydrateCounters replays already-admitted items through the per-bucket
// counters (perDepth, perSub, perPath) without touching dedup or applying caps,
// so a resumed crawl carries the running bucket totals from the session(s) that
// first admitted them. Without it the counters restart at zero on resume and a
// crawl with MaxURLsPerDepth / MaxPerSubdomain / a ByPath cap could admit up to a
// full extra bucket past what a straight crawl allowed (FR-08). It mirrors Admit's
// increment block exactly (perDepth + perSub always; the first matching ByPath).
func (f *Frontier) RehydrateCounters(items []Item) {
	lim := &f.cfg.Limits
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range items {
		f.perDepth[it.Depth]++
		f.perSub[urlutil.Host(it.URL)]++
		for i, pl := range lim.ByPath {
			if strings.Contains(it.URL, pl.Pattern) {
				f.perPath[i]++
				break
			}
		}
	}
}

// MarkSeen records URLs as already processed (resume preseeding) without
// counting them against any limit.
func (f *Frontier) MarkSeen(urls []string) { f.noteErr(f.dedup.MarkSeen(urls)) }

// Seen reports whether a URL was already admitted. An authority error reads as
// "not seen" (conservative for the read paths that use this) but is surfaced.
func (f *Frontier) Seen(url string) bool {
	s, err := f.dedup.Seen(url)
	f.noteErr(err)
	return s
}

// Admitted returns the number of admitted URLs.
func (f *Frontier) Admitted() int {
	n, _ := f.dedup.Count()
	return n
}
