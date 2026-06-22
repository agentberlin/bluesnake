package acceptance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/cucumber/godog"
)

func (w *world) registerStoreSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a stored crawl of a (\d+)-page fixture site interrupted after (\d+) pages$`, w.storedInterruptedCrawl)
	sc.Step(`^the crawl is resumed from the store$`, w.resumeFromStore)
	sc.Step(`^a stored cross-linked crawl interrupted before the shortcut page$`, w.storedCrossLinkedCrawl)
	sc.Step(`^a stored list-mode crawl with one seed pending$`, w.storedListModeCrawl)
	sc.Step(`^all (\d+) pages are processed in the store$`, w.storeProcessedCount)
	sc.Step(`^the stored crawl has issues recorded$`, w.storeHasIssues)
	sc.Step(`^the registry reports (\d+) crawled and (\d+) total for the resumed crawl$`, w.registryReportsCounts)
	sc.Step(`^the stored crawl page "([^"]*)" has depth (\d+)$`, w.storedPageDepth)
	sc.Step(`^the stored frontier is empty$`, w.storeFrontierEmpty)
	sc.Step(`^no fixture page was fetched twice$`, w.noDoubleFetch)
	sc.Step(`^the output does not contain "([^"]*)"$`, w.outputNotContains)
	sc.Step(`^the output does not contain literal "([^"]*)"$`, w.outputNotContainsLiteral)
	sc.Step(`^the file "([^"]*)" in the store dir contains "([^"]*)"$`, w.storeFileContains)
	sc.Step(`^the file "([^"]*)" in the store dir does not contain "([^"]*)"$`, w.storeFileNotContains)
	sc.Step(`^a URL list file containing "([^"]*)" and "([^"]*)"$`, w.urlListFile)
	sc.Step(`^the site page "([^"]*)" changes to body "([^"]*)"$`, w.sitePageChanges)
	sc.Step(`^the site page "([^"]*)" changes to body:$`, w.sitePageChangesDoc)
}

func (w *world) urlListFile(a, b string) error {
	srv := w.ensureServer()
	a = strings.ReplaceAll(a, "<serverurl>", srv.URL)
	b = strings.ReplaceAll(b, "<serverurl>", srv.URL)
	w.listFilePath = filepath.Join(w.tmpDir, "urls.txt")
	return os.WriteFile(w.listFilePath, []byte(a+"\n"+b+"\n"), 0o644)
}

func (w *world) sitePageChanges(path, body string) error {
	r := w.route(path)
	r.body = body
	// remember the crawl that ran before the mutation
	w.firstCrawlID = w.latestCrawlID()
	return nil
}

func (w *world) sitePageChangesDoc(path string, doc *godog.DocString) error {
	return w.sitePageChanges(path, doc.Content)
}

func (w *world) outputNotContainsLiteral(substr string) error {
	if strings.Contains(w.out, substr) {
		return fmt.Errorf("output contains %q:\n%s", substr, w.out)
	}
	return nil
}

func (w *world) storeFile(name string) (string, error) {
	data, err := os.ReadFile(filepath.Join(w.storeDirPath(), name))
	if err != nil {
		return "", err
	}
	return strings.ReplaceAll(string(data), w.server.URL, "<serverurl>"), nil
}

func (w *world) storeFileContains(name, substr string) error {
	content, err := w.storeFile(name)
	if err != nil {
		return err
	}
	if !strings.Contains(content, substr) {
		return fmt.Errorf("%s does not contain %q:\n%s", name, substr, content)
	}
	return nil
}

func (w *world) storeFileNotContains(name, substr string) error {
	content, err := w.storeFile(name)
	if err != nil {
		return err
	}
	if strings.Contains(content, substr) {
		return fmt.Errorf("%s contains %q", name, substr)
	}
	return nil
}

func (w *world) storeDirPath() string {
	return filepath.Join(w.tmpDir, "store")
}

// interruptSink cancels a context after N stored pages.
type interruptSink struct {
	*store.Crawl
	mu     sync.Mutex
	count  int
	limit  int
	cancel context.CancelFunc
}

func (s *interruptSink) Page(rec *crawler.PageRecord) error {
	err := s.Crawl.Page(rec)
	s.mu.Lock()
	s.count++
	if s.count == s.limit {
		s.cancel()
	}
	s.mu.Unlock()
	return err
}

func (w *world) storedInterruptedCrawl(pages, interruptAfter int) error {
	if err := w.sitePageGenerated("/", pages); err != nil {
		return err
	}
	srv := w.ensureServer()
	cfg := config.Default()
	// One worker makes the interrupt point deterministic (same rationale as
	// TestResumeEquivalence's equivCfg). interruptSink cancels after the Nth
	// *committed* page; with concurrent workers a second worker can have already
	// *fetched* another page whose result is not yet committed when cancel fires.
	// That page is never persisted, so it stays in the resumed frontier and is
	// fetched again on resume — which is correct (nothing was committed to resume
	// for it), but trips the "no fixture page was fetched twice" assertion
	// non-deterministically. Single-threaded, no fetch is ever abandoned
	// mid-flight: every fetched page is committed, so resume re-fetches nothing.
	cfg.Speed.MaxThreads = 1

	st, err := store.CreateCrawl(w.storeDirPath(), "resumetest", []string{srv.URL + "/"}, "spider", cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	w.storedCrawlID = st.ID

	ctx, cancel := context.WithCancel(context.Background())
	sink := &interruptSink{Crawl: st, limit: interruptAfter, cancel: cancel}
	c, err := crawler.New(cfg, crawler.WithSink(sink))
	if err != nil {
		return err
	}
	res, err := c.Run(ctx, srv.URL+"/")
	if err != nil {
		return err
	}
	if !res.Interrupted {
		return fmt.Errorf("crawl was not interrupted")
	}
	return store.SetStatus(w.storeDirPath(), st.ID, store.StatusInterrupted, res.Crawled, res.Total)
}

// storedCrossLinkedCrawl deterministically constructs an interrupted crawl
// where /deep is reachable two ways: a long path / -> /a -> /b -> /deep that was
// fully crawled in the first session (so /deep is recorded at its admit-time
// depth 3), and a short path / -> /shortcut -> /deep whose middle hop /shortcut
// was still in the frontier when the crawl stopped. A correct resume crawls
// /shortcut and recomputes depth over the full two-session graph, dropping /deep
// to 2 (Screaming Frog parity); the old behaviour leaves it at the stale 3.
// The state is written directly (rather than racing a real interrupt) so the
// setup is deterministic.
func (w *world) storedCrossLinkedCrawl() error {
	srv := w.ensureServer()
	abs := func(p string) string { return srv.URL + p }
	// resume only re-fetches the pending /shortcut; it must serve a link to /deep
	r := w.route("/shortcut")
	r.status, r.body = 200, `<a href="/deep">d</a>`

	st, err := store.CreateCrawl(w.storeDirPath(), "depthtest", []string{abs("/")}, "spider", config.Default())
	if err != nil {
		return err
	}
	defer st.Close()
	w.storedCrawlID = st.ID

	hl := func(target string) parse.Link {
		return parse.Link{Type: parse.Hyperlink, URL: abs(target)}
	}
	// session-1 pages, each at its admit-time depth (/deep wrongly at 3)
	sess1 := []struct {
		path  string
		depth int
		links []parse.Link
	}{
		{"/", 0, []parse.Link{hl("/a"), hl("/shortcut")}},
		{"/a", 1, []parse.Link{hl("/b")}},
		{"/b", 2, []parse.Link{hl("/deep")}},
		{"/deep", 3, nil},
	}
	for _, p := range sess1 {
		rec := &crawler.PageRecord{
			URL: abs(p.path), Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Depth: p.depth, Facts: &parse.Facts{Links: p.links},
		}
		if err := st.Page(rec); err != nil {
			return err
		}
	}
	// /shortcut was discovered (depth 1) but not yet crawled — left in the frontier
	if err := st.FrontierAdd(frontier.Item{URL: abs("/shortcut"), Depth: 1, Source: abs("/")}); err != nil {
		return err
	}
	return store.SetStatus(w.storeDirPath(), st.ID, store.StatusInterrupted, len(sess1), len(sess1))
}

// storedListModeCrawl builds an interrupted list-mode crawl with two uploaded
// seeds (/ and /b, neither linking the other) where / was crawled and /b is
// still pending. Both are depth-0 list seeds. CreateCrawl freezes the full seed
// set, so resume re-roots the depth BFS from every seed and both keep depth 0 —
// rather than rerooting from a single seed and NULLing /b.
func (w *world) storedListModeCrawl() error {
	srv := w.ensureServer()
	abs := func(p string) string { return srv.URL + p }
	r := w.route("/b")
	r.status, r.body = 200, `<p>second seed</p>`

	cfg := config.Default()
	cfg.Mode = "list"
	cfg.Limits.MaxDepth = 0 // list default: don't follow links

	st, err := store.CreateCrawl(w.storeDirPath(), "listtest", []string{abs("/"), abs("/b")}, "list", cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	w.storedCrawlID = st.ID

	// / already crawled as a depth-0 seed
	if err := st.Page(&crawler.PageRecord{
		URL: abs("/"), Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Depth: 0, Facts: &parse.Facts{},
	}); err != nil {
		return err
	}
	// /b is the other uploaded seed, still pending at depth 0
	if err := st.FrontierAdd(frontier.Item{URL: abs("/b"), Depth: 0}); err != nil {
		return err
	}
	return store.SetStatus(w.storeDirPath(), st.ID, store.StatusInterrupted, 1, 1)
}

// storedPageDepth asserts the persisted (post-resume) crawl depth of a page.
func (w *world) storedPageDepth(path string, want int) error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	rec := pages[w.server.URL+path]
	if rec == nil {
		return fmt.Errorf("%s not in store", path)
	}
	if rec.Depth != want {
		return fmt.Errorf("%s stored depth = %d, want %d", path, rec.Depth, want)
	}
	return nil
}

func (w *world) resumeFromStore() error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	seeds, err := st.Seeds()
	if err != nil {
		return err
	}
	processed, err := st.ProcessedURLs()
	if err != nil {
		return err
	}
	pending, err := st.PendingFrontier()
	if err != nil {
		return err
	}
	c, err := crawler.New(cfg, crawler.WithSink(st), crawler.WithResume(processed, pending))
	if err != nil {
		return err
	}
	_, err = c.Run(context.Background(), seeds...)
	return err
}

func (w *world) storeProcessedCount(want int) error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	processed, err := st.ProcessedURLs()
	if err != nil {
		return err
	}
	if len(processed) != want {
		return fmt.Errorf("%d pages processed, want %d", len(processed), want)
	}
	return nil
}

// storeHasIssues asserts post-crawl analysis ran and persisted occurrences —
// a resumed crawl that drains to completion must finalise like a fresh one,
// not leave the issues table empty (which would read as a clean site).
func (w *world) storeHasIssues() error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	counts, err := st.IssueCounts()
	if err != nil {
		return err
	}
	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		return fmt.Errorf("no issues recorded after resume — analysis did not run")
	}
	return nil
}

// registryReportsCounts asserts the registry's crawled/total for the resumed
// crawl — the authoritative full-graph counts finalize persists, which must
// equal a straight crawl's (the resume-count-equivalence contract).
func (w *world) registryReportsCounts(crawled, total int) error {
	infos, err := store.ListCrawls(w.storeDirPath())
	if err != nil {
		return err
	}
	for _, in := range infos {
		if in.ID == w.storedCrawlID {
			if in.Crawled != crawled || in.Total != total {
				return fmt.Errorf("registry crawled=%d total=%d, want %d/%d", in.Crawled, in.Total, crawled, total)
			}
			return nil
		}
	}
	return fmt.Errorf("crawl %s not in registry", w.storedCrawlID)
}

func (w *world) storeFrontierEmpty() error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	pending, err := st.PendingFrontier()
	if err != nil {
		return err
	}
	if len(pending) != 0 {
		return fmt.Errorf("frontier has %d leftover items", len(pending))
	}
	return nil
}

func (w *world) noDoubleFetch() error {
	w.hitsMu.Lock()
	defer w.hitsMu.Unlock()
	for path, n := range w.hits {
		if !wellKnownSiteFile(path) && n > 1 {
			return fmt.Errorf("%s fetched %d times", path, n)
		}
	}
	return nil
}

func (w *world) outputNotContains(substr string) error {
	if substr == "<crawlid>" {
		substr = w.latestCrawlID()
	}
	if substr == "" {
		return fmt.Errorf("nothing to check: empty substring")
	}
	if strings.Contains(w.out, substr) {
		return fmt.Errorf("output contains %q:\n%s", substr, w.out)
	}
	return nil
}

// latestCrawlID resolves the most recent crawl in the scenario store dir.
func (w *world) latestCrawlID() string {
	if w.storedCrawlID != "" {
		return w.storedCrawlID
	}
	infos, err := store.ListCrawls(w.storeDirPath())
	if err != nil || len(infos) == 0 {
		return ""
	}
	return infos[len(infos)-1].ID
}
