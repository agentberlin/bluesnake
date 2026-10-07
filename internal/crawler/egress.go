package crawler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/proxypool"
)

// Egress modes reported by EgressStatus. "" means the crawl has no
// direct→proxy switch (http.proxy_on_block is off).
const (
	EgressDirect   = "direct"
	EgressDraining = "draining"
	EgressProxy    = "proxy"
)

// EgressEvent is the persisted record of a crawl's switch to the proxy. It is
// written the moment the trip happens — before the drain finishes — so a crawl
// paused mid-drain still resumes on the proxy.
type EgressEvent struct {
	State string    `json:"state"`          // always EgressProxy
	After int64     `json:"switched_after"` // pages recorded when the switch tripped
	At    time.Time `json:"at"`
}

// EgressSink is the optional sink extension that persists the switch (the
// store keeps it in crawl meta, which resume reads back).
type EgressSink interface {
	EgressSwitched(ev EgressEvent) error
}

// EgressStatus is the switch's live state for progress surfaces.
type EgressStatus struct {
	Mode          string // "" | direct | draining | proxy
	SwitchedAfter int64  // pages recorded when the switch tripped (meaningful once Mode is draining/proxy)
	Refetched     int64  // blocked URLs re-fetched through the proxy
	StillBlocked  int64  // in-scope block responses received through the proxy
}

type egressState int

const (
	stDirect egressState = iota
	stDraining
	stProxied
)

// egressCtl is a crawl's http.proxy_on_block controller: it watches in-scope
// page responses for rate-limit and block signals, and on a trip holds new
// fetches (the gate), lets the in-flight ones finish (the drain), flips the
// shared Escalation — which moves the fetch client and Chrome to the proxy —
// and puts the URLs it parked back on the queue.
//
// A URL that gets a block before the switch is PARKED, not recorded: its
// frontier row stays claimed (a pause leaves it pending for resume), its
// MaxURLs slot is refunded, and it waits in a small in-RAM set. Recording first
// would write phantom 429 rows into the audit. A parked URL comes back either
// at the switch (re-fetched through the proxy) or, if the window moves on
// without tripping, for one last direct try whose answer is recorded — so a
// genuine 403 page can never loop.
type egressCtl struct {
	c   *Crawler
	esc *proxypool.Escalation
	win proxypool.Window

	mu      sync.Mutex
	cond    *sync.Cond
	state   egressState
	active  int // gated fetches and renders currently on the wire
	parked  map[string]parkedURL
	lastTry map[string]bool // aged-out URLs whose next attempt is recorded
	reruns  []func(context.Context) []frontier.Item

	requeue   func(frontier.Item) // set by Run: publish the item as claimable work
	maxParked int

	switchedAfter atomic.Int64
	refetched     atomic.Int64
	stillBlocked  atomic.Int64
}

type parkedURL struct {
	item frontier.Item
	seq  uint64 // window observation count when parked
}

func newEgressCtl(c *Crawler, esc *proxypool.Escalation, threads int) *egressCtl {
	e := &egressCtl{
		c:       c,
		esc:     esc,
		parked:  map[string]parkedURL{},
		lastTry: map[string]bool{},
		// Parked URLs are bounded by the trip rule: fewer than WindowBlocks
		// blocks sit in an untripped window, plus whatever was in flight when
		// it tripped. The cap keeps that bound explicit (bounded-RAM contract).
		maxParked: proxypool.WindowBlocks + 2*threads,
	}
	e.cond = sync.NewCond(&e.mu)
	if esc.Escalated() {
		e.state = stProxied
	}
	return e
}

// watch wakes gate waiters when ctx ends, so a pause never hangs on the gate.
func (e *egressCtl) watch(ctx context.Context, stop <-chan struct{}) {
	if e == nil {
		return
	}
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		e.mu.Lock()
		e.cond.Broadcast()
		e.mu.Unlock()
	}()
}

// enter is the single gate every crawl fetch and render passes: it blocks while
// the crawl is draining for the switch and counts the caller as on the wire.
// false = ctx ended while waiting (nothing entered; do not exit).
func (e *egressCtl) enter(ctx context.Context) bool {
	if e == nil {
		return true
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for e.state == stDraining {
		if ctx.Err() != nil {
			return false
		}
		e.cond.Wait()
	}
	if ctx.Err() != nil {
		return false
	}
	e.active++
	return true
}

// exit ends a gated fetch or render; the last one out of a drain flips the switch.
func (e *egressCtl) exit() {
	if e == nil {
		return
	}
	e.mu.Lock()
	e.active--
	e.maybeSwitchLocked()
	e.mu.Unlock()
}

// preSwitch reports whether the crawl is still on the direct route.
func (e *egressCtl) preSwitch() bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.state != stProxied
}

// observe feeds one in-scope, direct-route page response into the trip window.
func (e *egressCtl) observe(s proxypool.Strength) {
	tripped := e.win.Observe(s)
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ageOutLocked()
	if tripped {
		e.tripLocked()
	}
}

// tripLocked starts the drain and persists the switch at once, so a pause
// mid-drain still resumes on the proxy.
func (e *egressCtl) tripLocked() {
	if e.state != stDirect {
		return
	}
	e.state = stDraining
	after := e.c.totalCount.Load()
	e.switchedAfter.Store(after)
	if es, ok := e.c.sink.(EgressSink); ok && e.c.sink != nil {
		e.c.noteSinkErr(es.EgressSwitched(EgressEvent{State: EgressProxy, After: after, At: time.Now().UTC()}))
	}
	e.maybeSwitchLocked()
}

// maybeSwitchLocked completes a drain: once nothing is on the wire it flips the
// shared switch (fetch client + Chrome move to the proxy), requeues every
// parked URL and opens the gate.
func (e *egressCtl) maybeSwitchLocked() {
	if e.state != stDraining || e.active != 0 {
		return
	}
	e.esc.Escalate()
	e.state = stProxied
	for url, p := range e.parked {
		delete(e.parked, url)
		e.refetched.Add(1)
		e.requeue(p.item)
	}
	e.cond.Broadcast()
}

// parkOutcome tells crawlOne what to do with a blocked response.
type parkOutcome int

const (
	parkHeld    parkOutcome = iota // parked: abandon the item unrecorded
	parkRefetch                    // the switch already happened: fetch again now
)

// park holds a URL whose direct fetch came back blocked. Its MaxURLs slot is
// refunded either way: the blocked attempt never becomes a page.
func (e *egressCtl) park(it frontier.Item) parkOutcome {
	e.c.fetched.Add(-1)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == stProxied {
		e.refetched.Add(1)
		return parkRefetch
	}
	e.parked[it.URL] = parkedURL{item: it, seq: e.win.Seen()}
	if len(e.parked) > e.maxParked {
		e.releaseOldestLocked()
	}
	return parkHeld
}

// takeLastTry reports (and consumes) whether this URL's next attempt is its
// final direct try, to be recorded whatever it returns.
func (e *egressCtl) takeLastTry(url string) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lastTry[url] {
		delete(e.lastTry, url)
		return true
	}
	return false
}

// ageOutLocked releases parked URLs the window has moved past without a trip.
func (e *egressCtl) ageOutLocked() {
	if e.state != stDirect {
		return
	}
	now := e.win.Seen()
	for url, p := range e.parked {
		if now-p.seq >= proxypool.WindowSize {
			e.releaseLocked(url, p)
		}
	}
}

func (e *egressCtl) releaseOldestLocked() {
	var oldest string
	var seq uint64
	for url, p := range e.parked {
		if oldest == "" || p.seq < seq {
			oldest, seq = url, p.seq
		}
	}
	if oldest != "" {
		e.releaseLocked(oldest, e.parked[oldest])
	}
}

func (e *egressCtl) releaseLocked(url string, p parkedURL) {
	delete(e.parked, url)
	e.lastTry[url] = true
	e.requeue(p.item)
}

// addRerun schedules a crawl-start fetch (sitemaps, llms.txt) that came back
// blocked to run again once the crawl is on the proxy.
func (e *egressCtl) addRerun(fn func(context.Context) []frontier.Item) {
	e.mu.Lock()
	e.reruns = append(e.reruns, fn)
	e.mu.Unlock()
}

// takeReruns hands out the scheduled reruns once the switch has happened.
func (e *egressCtl) takeReruns() []func(context.Context) []frontier.Item {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state != stProxied || len(e.reruns) == 0 {
		return nil
	}
	r := e.reruns
	e.reruns = nil
	return r
}

// idle is the feeder's last word before it ends the crawl: nothing is queued
// or in flight. Parked URLs must not be stranded, so they come back for their
// last direct try; a drain still waiting on an out-of-band fetch (the site-
// check pass) is waited out; reruns nobody picked up run here. Returns true
// when it put work back on the queue.
func (e *egressCtl) idle(ctx context.Context, enqueue func(frontier.Item)) bool {
	if e == nil {
		return false
	}
	e.mu.Lock()
	requeuedBefore := e.refetched.Load()
	// Idle rule: nothing is left to fetch but parked URLs, and every answer
	// the site gave was a block — a seed refused from the first request
	// discovers nothing else to count, so waiting for the window to fill
	// would end the crawl on a 429. Switch instead.
	if e.state == stDirect && len(e.parked) > 0 && e.win.AllBlocked() && e.win.Trip() {
		e.tripLocked()
	}
	for e.state == stDraining && ctx.Err() == nil {
		e.cond.Wait()
	}
	// A switch completed during this call requeued the parked URLs.
	more := e.refetched.Load() > requeuedBefore
	if e.state == stDirect && len(e.parked) > 0 {
		for url, p := range e.parked {
			e.releaseLocked(url, p)
		}
		more = true
	}
	e.mu.Unlock()
	for _, fn := range e.takeReruns() {
		for _, it := range fn(ctx) {
			enqueue(it)
			more = true
		}
	}
	return more
}

// status is the live reading for progress surfaces.
func (e *egressCtl) status() EgressStatus {
	if e == nil {
		return EgressStatus{}
	}
	e.mu.Lock()
	st := e.state
	e.mu.Unlock()
	mode := EgressDirect
	switch st {
	case stDraining:
		mode = EgressDraining
	case stProxied:
		mode = EgressProxy
	}
	return EgressStatus{
		Mode:          mode,
		SwitchedAfter: e.switchedAfter.Load(),
		Refetched:     e.refetched.Load(),
		StillBlocked:  e.stillBlocked.Load(),
	}
}

// classify is the ban policy over a fetch result.
func classify(res *fetch.Result) proxypool.Strength {
	return proxypool.Classify(res.StatusCode, res.Headers, res.FetchError)
}

// gatedFetcher is the crawler's fetch client behind the egress gate, for the
// fetches that do not go through fetchCapped: robots.txt and the site-check
// pass. They hold the drain like page fetches, so no connection opened before
// the switch is still in use after it.
type gatedFetcher struct{ c *Crawler }

func (g gatedFetcher) Fetch(ctx context.Context, rawURL string) *fetch.Result {
	return g.FetchWith(ctx, rawURL, fetch.Override{})
}

func (g gatedFetcher) FetchWith(ctx context.Context, rawURL string, o fetch.Override) *fetch.Result {
	if !g.c.egress.enter(ctx) {
		return &fetch.Result{URL: rawURL, FetchError: "crawl cancelled"}
	}
	defer g.c.egress.exit()
	return g.c.client.FetchWith(ctx, rawURL, o)
}
