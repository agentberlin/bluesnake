package finalize

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// crawlInto runs a small two-page crawl into a fresh store and returns the open
// crawl, the crawler, the result and the store dir.
func crawlInto(t *testing.T) (*store.Crawl, *crawler.Crawler, *crawler.Result, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, `<a href="/a">a</a>`)
		default:
			fmt.Fprint(w, "<p>x</p>") // no title/h1/meta -> issues fire
		}
	}))
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	cfg := config.Default()
	st, err := store.CreateCrawl(dir, "proj", srv.URL+"/", "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), srv.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	return st, c, res, dir
}

func TestCrawlCompletedRunsAnalysis(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	cfg := config.Default()

	out, err := Crawl(c, st, res, Params{
		StoreDir: dir, Cfg: cfg, Completed: true, // Resumed:false -> Seeds unused
	})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if out.Status != store.StatusCompleted {
		t.Errorf("status = %q, want completed", out.Status)
	}
	if !out.Analyzed {
		t.Error("expected Analyzed=true on a completed crawl with analysis.auto")
	}
	if out.IssueTotal == 0 {
		t.Error("expected issues to be evaluated and counted")
	}
	// issues are persisted
	counts, err := st.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) == 0 {
		t.Error("issues table empty after finalize")
	}
	// registry status reflects completion
	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 || infos[0].Status != store.StatusCompleted {
		t.Errorf("registry status = %+v, want one completed crawl", infos)
	}
}

func TestCrawlInterruptedSkipsAnalysis(t *testing.T) {
	st, c, res, dir := crawlInto(t)
	cfg := config.Default()

	out, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Completed: false})
	if err != nil {
		t.Fatalf("Crawl: %v", err)
	}
	if out.Status != store.StatusInterrupted {
		t.Errorf("status = %q, want interrupted", out.Status)
	}
	if out.Analyzed {
		t.Error("interrupted crawl must not run analysis")
	}
	counts, err := st.IssueCounts()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Errorf("interrupted crawl persisted %d issue checks, want 0", len(counts))
	}
}

func TestAnalyzeStandalone(t *testing.T) {
	st, _, _, _ := crawlInto(t)
	cfg := config.Default()

	out, err := Analyze(st, cfg)
	if err != nil {
		t.Fatalf("Analyze: %v", err)
	}
	if !out.Analyzed || out.IssueTotal == 0 {
		t.Errorf("Analyze outcome = %+v, want analyzed with issues", out)
	}
}
