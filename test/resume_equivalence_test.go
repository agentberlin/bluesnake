package acceptance

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/store"
)

// A resumed crawl must finalise to a result that is byte-for-byte equivalent to
// an uninterrupted crawl of the same site (DESIGN §1 goal #4, §5.2). This pins
// that contract over the full set of per-page aggregates the finalize path owns
// — counts, inlinks, discovered_from, depth and the analysis outputs — so the
// "the number matches only for this session" class of resume bug can't return.
//
// equivRoutes is deterministic and built so /hub is linked by /, /a, /b and /c:
// its inlink count (4) is only correct when computed over the FULL two-session
// graph. With one worker the crawl order is fixed and an interrupt after three
// pages straddles the resume boundary — /, /a, /b land in session 1 and /c,
// /hub, /a1, /b1 in session 2 — so any session-scoped aggregate diverges.
var equivRoutes = map[string]string{
	"/":    `<a href="/a">a</a> <a href="/b">b</a> <a href="/c">c</a> <a href="/hub">hub</a>`,
	"/a":   `<a href="/hub">h</a> <a href="/a1">a1</a>`,
	"/b":   `<a href="/hub">h</a> <a href="/b1">b1</a>`,
	"/c":   `<a href="/hub">h</a>`,
	"/hub": `<a href="/">home</a>`,
	"/a1":  `alpha unique leaf content for the a-one page body`,
	"/b1":  `beta unique leaf content for the b-one page body`,
}

func equivServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, body := range equivRoutes {
		p, b := path, body
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != p { // "/" pattern catches every unmatched path
				http.NotFound(w, r)
				return
			}
			fmt.Fprintf(w, "<!doctype html><html><head><title>%s</title></head><body>%s</body></html>", p, b)
		})
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func equivCfg() *config.Config {
	cfg := config.Default()
	cfg.Speed.MaxThreads = 1 // deterministic crawl order → deterministic interrupt point
	return cfg
}

type pageAgg struct {
	Scope, State, DiscoveredFrom                  string
	Depth, Inlinks, UniqueInlinks, UniqueOutlinks int
	Indexable                                     bool
	LinkScore                                     float64
}

// snapshot reads the finalised, persisted result of a crawl: every per-page
// aggregate plus the registry's crawled/total counts and the issue tallies.
func snapshot(t *testing.T, dir, id, base string) (map[string]pageAgg, map[string]int, int, int) {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("open %s: %v", id, err)
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatalf("load pages: %v", err)
	}
	rel := func(u string) string {
		if u == "" {
			return ""
		}
		if len(u) >= len(base) && u[:len(base)] == base {
			return u[len(base):]
		}
		return u
	}
	out := make(map[string]pageAgg, len(pages))
	for url, rec := range pages {
		out[rel(url)] = pageAgg{
			Scope: rec.Scope, State: rec.State, DiscoveredFrom: rel(rec.DiscoveredFrom),
			Depth: rec.Depth, Inlinks: rec.Inlinks, UniqueInlinks: rec.UniqueInlinks,
			UniqueOutlinks: rec.UniqueOutlinks, Indexable: rec.Indexable, LinkScore: rec.LinkScore,
		}
	}
	issues, err := st.IssueCounts()
	if err != nil {
		t.Fatalf("issue counts: %v", err)
	}
	crawled, total := registryCounts(t, dir, id)
	return out, issues, crawled, total
}

func registryCounts(t *testing.T, dir, id string) (int, int) {
	t.Helper()
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatalf("list crawls: %v", err)
	}
	for _, in := range infos {
		if in.ID == id {
			return in.Crawled, in.Total
		}
	}
	t.Fatalf("crawl %s not in registry", id)
	return 0, 0
}

// straightCrawl runs one uninterrupted crawl to completion and finalises it.
func straightCrawl(t *testing.T, dir, seed string) string {
	t.Helper()
	cfg := equivCfg()
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := st.ID
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		st.Close()
		t.Fatalf("crawler: %v", err)
	}
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		st.Close()
		t.Fatalf("run: %v", err)
	}
	if _, err := finalize.Crawl(c, st, res, finalize.Params{
		StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Resumed: false, Completed: true,
	}); err != nil {
		st.Close()
		t.Fatalf("finalize: %v", err)
	}
	c.Close()
	st.Close()
	return id
}

// interruptResumeCrawl interrupts a crawl after `after` pages, finalises it as
// interrupted (pause), then resumes it to completion and finalises again — the
// exact two-session lifecycle the desktop/MCP/CLL pause+resume drives.
func interruptResumeCrawl(t *testing.T, dir, seed string, after int) string {
	t.Helper()
	cfg := equivCfg()
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := st.ID

	ctx, cancel := context.WithCancel(context.Background())
	sink := &interruptSink{Crawl: st, limit: after, cancel: cancel}
	c1, err := crawler.New(cfg, crawler.WithSink(sink))
	if err != nil {
		st.Close()
		t.Fatalf("crawler: %v", err)
	}
	res1, err := c1.Run(ctx, seed)
	if err != nil {
		st.Close()
		t.Fatalf("run1: %v", err)
	}
	if !res1.Interrupted {
		st.Close()
		t.Fatalf("session 1 was not interrupted (crawled %d)", res1.Crawled)
	}
	if _, err := finalize.Crawl(c1, st, res1, finalize.Params{
		StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Resumed: false, Completed: false,
	}); err != nil {
		st.Close()
		t.Fatalf("finalize interrupted: %v", err)
	}
	c1.Close()
	st.Close()

	// resume from the stored frontier and drain to completion
	st2, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	processed, err := st2.ProcessedURLs()
	if err != nil {
		st2.Close()
		t.Fatalf("processed: %v", err)
	}
	pending, err := st2.PendingFrontier()
	if err != nil {
		st2.Close()
		t.Fatalf("pending: %v", err)
	}
	if len(processed) == 0 || len(pending) == 0 {
		st2.Close()
		t.Fatalf("bad interrupt split: processed=%d pending=%d", len(processed), len(pending))
	}
	c2, err := crawler.New(cfg, crawler.WithSink(st2), crawler.WithResume(processed, pending))
	if err != nil {
		st2.Close()
		t.Fatalf("crawler2: %v", err)
	}
	res2, err := c2.Run(context.Background(), seed)
	if err != nil {
		st2.Close()
		t.Fatalf("run2: %v", err)
	}
	if _, err := finalize.Crawl(c2, st2, res2, finalize.Params{
		StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Resumed: true, Completed: true,
	}); err != nil {
		st2.Close()
		t.Fatalf("finalize resumed: %v", err)
	}
	c2.Close()
	st2.Close()
	return id
}

func TestResumeEquivalence(t *testing.T) {
	srv := equivServer(t)
	seed := srv.URL + "/"
	dir := t.TempDir()

	straightID := straightCrawl(t, dir, seed)
	resumeID := interruptResumeCrawl(t, dir, seed, 3)

	sPages, sIssues, sCrawled, sTotal := snapshot(t, dir, straightID, srv.URL)
	rPages, rIssues, rCrawled, rTotal := snapshot(t, dir, resumeID, srv.URL)

	if sCrawled != rCrawled || sTotal != rTotal {
		t.Errorf("registry counts differ:\n  straight: crawled=%d total=%d\n  resumed:  crawled=%d total=%d",
			sCrawled, sTotal, rCrawled, rTotal)
	}
	if len(sPages) != len(rPages) {
		t.Errorf("page-set size differs: straight=%d resumed=%d", len(sPages), len(rPages))
	}
	for url, sp := range sPages {
		rp, ok := rPages[url]
		if !ok {
			t.Errorf("%s present in straight crawl but missing from resumed crawl", url)
			continue
		}
		if sp.Inlinks != rp.Inlinks {
			t.Errorf("%s inlinks differ: straight=%d resumed=%d", url, sp.Inlinks, rp.Inlinks)
		}
		if sp.DiscoveredFrom != rp.DiscoveredFrom {
			t.Errorf("%s discovered_from differs: straight=%q resumed=%q", url, sp.DiscoveredFrom, rp.DiscoveredFrom)
		}
		if sp.UniqueInlinks != rp.UniqueInlinks {
			t.Errorf("%s unique_inlinks differ: straight=%d resumed=%d", url, sp.UniqueInlinks, rp.UniqueInlinks)
		}
		if sp.UniqueOutlinks != rp.UniqueOutlinks {
			t.Errorf("%s unique_outlinks differ: straight=%d resumed=%d", url, sp.UniqueOutlinks, rp.UniqueOutlinks)
		}
		if sp.Depth != rp.Depth {
			t.Errorf("%s depth differs: straight=%d resumed=%d", url, sp.Depth, rp.Depth)
		}
		if sp.State != rp.State || sp.Scope != rp.Scope || sp.Indexable != rp.Indexable {
			t.Errorf("%s state/scope/indexable differ: straight=%+v resumed=%+v", url, sp, rp)
		}
		if math.Abs(sp.LinkScore-rp.LinkScore) > 0.01 {
			t.Errorf("%s link_score differs: straight=%.4f resumed=%.4f", url, sp.LinkScore, rp.LinkScore)
		}
	}
	if len(sIssues) != len(rIssues) {
		t.Errorf("issue-check set differs: straight=%v resumed=%v", sIssues, rIssues)
	}
	for id, n := range sIssues {
		if rIssues[id] != n {
			t.Errorf("issue %q count differs: straight=%d resumed=%d", id, n, rIssues[id])
		}
	}
}
