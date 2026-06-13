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
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/cucumber/godog"
)

func (w *world) registerStoreSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a stored crawl of a (\d+)-page fixture site interrupted after (\d+) pages$`, w.storedInterruptedCrawl)
	sc.Step(`^the crawl is resumed from the store$`, w.resumeFromStore)
	sc.Step(`^all (\d+) pages are processed in the store$`, w.storeProcessedCount)
	sc.Step(`^the stored frontier is empty$`, w.storeFrontierEmpty)
	sc.Step(`^no fixture page was fetched twice$`, w.noDoubleFetch)
	sc.Step(`^the output does not contain "([^"]*)"$`, w.outputNotContains)
	sc.Step(`^the output does not contain literal "([^"]*)"$`, w.outputNotContainsLiteral)
	sc.Step(`^the file "([^"]*)" in the store dir contains "([^"]*)"$`, w.storeFileContains)
	sc.Step(`^the file "([^"]*)" in the store dir does not contain "([^"]*)"$`, w.storeFileNotContains)
	sc.Step(`^a URL list file containing "([^"]*)" and "([^"]*)"$`, w.urlListFile)
	sc.Step(`^the site page "([^"]*)" changes to body "([^"]*)"$`, w.sitePageChanges)
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
	cfg.Speed.MaxThreads = 2

	st, err := store.CreateCrawl(w.storeDirPath(), "resumetest", srv.URL+"/", "spider", cfg)
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
	seed, err := st.Meta("seed")
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
	_, err = c.Run(context.Background(), seed)
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
		if path != "/robots.txt" && n > 1 {
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
