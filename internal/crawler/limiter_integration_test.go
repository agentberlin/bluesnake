package crawler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/limiter"
)

// TestGlobalLimiterBoundsFetchesAcrossCrawls (GL-07/GL-08) pins the process-wide
// fetch cap: two crawls, each with its own worker pool, sharing ONE limiter must
// never have more than G page fetches in flight at once — the cap is global, not
// per-crawl. The fixture server samples its own concurrent in-flight page
// requests (robots.txt excluded, since it bypasses the limiter).
func TestGlobalLimiterBoundsFetchesAcrossCrawls(t *testing.T) {
	const G = 2
	var cur, max int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404) // bypasses the limiter; keep it out of the gauge
			return
		}
		c := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&max)
			if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond) // hold the slot long enough to overlap
		atomic.AddInt64(&cur, -1)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < 20; i++ {
				fmt.Fprint(w, link(fmt.Sprintf("/p%d", i)))
			}
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	defer srv.Close()

	lim := limiter.New(G, 1, 0)
	run := func() {
		cfg := config.Default()
		cfg.Speed.MaxThreads = 5 // 2 crawls × 5 = up to 10 would-be concurrent
		sink := newCapSink()
		c, err := New(cfg, WithSink(sink), WithLimiter(lim))
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
			t.Error(err)
		}
	}

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); run() }()
	}
	wg.Wait()

	got := atomic.LoadInt64(&max)
	if got > G {
		t.Errorf("peak concurrent page fetches across 2 crawls = %d, want <= %d (global cap)", got, G)
	}
	if got < G {
		t.Errorf("peak concurrency = %d; the global cap never bound (with 10 workers it should reach %d)", got, G)
	}
}

// TestNoLimiterUnboundedSingleCrawl is the control: without a limiter a single
// crawl's concurrency is governed solely by MaxThreads (the default behaviour is
// unchanged), so it can exceed the small global cap used above.
func TestNoLimiterUnboundedSingleCrawl(t *testing.T) {
	var cur, max int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		c := atomic.AddInt64(&cur, 1)
		for {
			m := atomic.LoadInt64(&max)
			if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		atomic.AddInt64(&cur, -1)
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < 20; i++ {
				fmt.Fprint(w, link(fmt.Sprintf("/p%d", i)))
			}
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	defer srv.Close()

	cfg := config.Default()
	cfg.Speed.MaxThreads = 5
	sink := newCapSink()
	c, err := New(cfg, WithSink(sink)) // no limiter
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), srv.URL+"/"); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&max); got < 2 {
		t.Errorf("unbounded single crawl peaked at %d concurrent fetches, want it to use multiple threads", got)
	}
}

// TestOutOfBandFetchesRespectGlobalCap (GL-08 completeness): the out-of-band
// crawl-start fetches — /llms.txt, /llms-full.txt, and the sitemap enumeration
// walk — must take a global fetch slot like every worker fetch. Crawl A
// saturates a G=1 cap with a backlog of slow pages; crawl B then starts and
// does its out-of-band fetches. If those bypass the limiter, the gauge reads 2
// while A's worker holds the only slot (exactly the H1 breach the MCP-surface
// GL-08 test surfaced with parallel crawls).
func TestOutOfBandFetchesRespectGlobalCap(t *testing.T) {
	const G = 1
	var cur, max int64
	gaugeHandler := func(pages func(w http.ResponseWriter, r *http.Request)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				w.WriteHeader(404) // robots keeps its documented limiter bypass; exclude it
				return
			}
			c := atomic.AddInt64(&cur, 1)
			for {
				m := atomic.LoadInt64(&max)
				if c <= m || atomic.CompareAndSwapInt64(&max, m, c) {
					break
				}
			}
			time.Sleep(15 * time.Millisecond)
			atomic.AddInt64(&cur, -1)
			pages(w, r)
		}
	}
	srvA := httptest.NewServer(gaugeHandler(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.URL.Path == "/" {
			for i := 0; i < 20; i++ {
				fmt.Fprint(w, link(fmt.Sprintf("/p%d", i)))
			}
			return
		}
		fmt.Fprint(w, "<p>leaf</p>")
	}))
	defer srvA.Close()
	var srvBURL string
	srvB := httptest.NewServer(gaugeHandler(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/llms.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "# Site B\n\n> Summary.\n")
		case "/llms-full.txt":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "# Site B full\n")
		case "/sitemap.xml":
			w.Header().Set("Content-Type", "application/xml")
			fmt.Fprintf(w, `<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>%s/s1</loc></url></urlset>`, srvBURL)
		default:
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<p>b</p>")
		}
	}))
	defer srvB.Close()
	srvBURL = srvB.URL

	lim := limiter.New(G, 1, 0)
	run := func(cfg *config.Config, seed string) {
		sink := newCapSink()
		c, err := New(cfg, WithSink(sink), WithLimiter(lim))
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := c.Run(context.Background(), seed); err != nil {
			t.Error(err)
		}
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // crawl A: saturates the single global slot with a 20-page backlog
		defer wg.Done()
		cfg := config.Default()
		cfg.Speed.MaxThreads = 4
		cfg.LlmsTxt.Check = false // A is pure worker traffic
		run(cfg, srvA.URL+"/")
	}()
	go func() { // crawl B: starts mid-A and does the out-of-band fetches
		defer wg.Done()
		time.Sleep(60 * time.Millisecond) // A's workers are saturated by now
		cfg := config.Default()
		cfg.Speed.MaxThreads = 1
		cfg.Sitemaps.URLs = []string{srvB.URL + "/sitemap.xml"}
		run(cfg, srvB.URL+"/")
	}()
	wg.Wait()

	if got := atomic.LoadInt64(&max); got > G {
		t.Errorf("peak concurrent fetches = %d, want <= %d — an out-of-band fetch (llms.txt/sitemap) bypassed the global cap", got, G)
	}
}
