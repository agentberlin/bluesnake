package acceptance

import (
	"context"
	"fmt"

	"strings"

	"github.com/agentberlin/bluesnake/internal/analyze"
	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/cucumber/godog"
)

func (w *world) registerIssuesSteps(sc *godog.ScenarioContext) {
	sc.Step(`^analysis is run$`, w.runAnalysisStep)
	sc.Step(`^the crawl page "([^"]*)" has custom result "([^"]*)" with value "([^"]*)"$`, w.crawlPageCustomResult)
	sc.Step(`^I crawl the site into a store$`, w.crawlIntoStore)
	sc.Step(`^issues are evaluated$`, w.evaluateIssues)
	sc.Step(`^the page "([^"]*)" has issue "([^"]*)"$`, w.pageHasIssue)
	sc.Step(`^the page "([^"]*)" has issue "([^"]*)" with detail "([^"]*)"$`, w.pageHasIssueDetail)
	sc.Step(`^the page "([^"]*)" does not have issue "([^"]*)"$`, w.pageHasNoIssue)
}

func (w *world) crawlIntoStore() error {
	srv := w.ensureServer()
	cfg := config.Default()
	for _, o := range w.crawlOverride {
		o = strings.ReplaceAll(o, "<serverurl>", srv.URL)
		if err := cfg.Set(o); err != nil {
			return err
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	st, err := store.CreateCrawl(w.storeDirPath(), []string{srv.URL + "/"}, "spider", cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	w.storedCrawlID = st.ID
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		return err
	}
	seed := srv.URL + "/"
	if w.crawlResult, err = c.Run(context.Background(), seed); err != nil {
		return err
	}
	return w.finalizeCrawlPages(c, st, w.crawlResult, seed)
}

func (w *world) runAnalysisStep() error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	occs := issues.Evaluate(pages, cfg)
	sitemaps, err := st.SitemapIndex()
	if err != nil {
		return err
	}
	llmstxt, err := st.LlmsTxt()
	if err != nil {
		return err
	}
	results := analyze.Run(pages, sitemaps, llmstxt, cfg)
	w.issueOccs = append(occs, results.Occurrences...)
	if err := st.SaveIssues(occs); err != nil {
		return err
	}
	return st.SaveAnalysis(results)
}

func (w *world) evaluateIssues() error {
	st, err := store.OpenCrawl(w.storeDirPath(), w.storedCrawlID)
	if err != nil {
		return err
	}
	defer st.Close()
	pages, err := st.LoadPages()
	if err != nil {
		return err
	}
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return err
	}
	occs := issues.Evaluate(pages, cfg)
	if err := st.SaveIssues(occs); err != nil {
		return err
	}
	w.issueOccs = occs
	return nil
}

func (w *world) crawlPageCustomResult(path, name, want string) error {
	rec := w.crawlPages[w.server.URL+path]
	if rec == nil {
		return fmt.Errorf("no record for %s", path)
	}
	for _, cr := range rec.CustomResults {
		if cr.Name == name {
			if cr.Value != want {
				return fmt.Errorf("%s = %q, want %q", name, cr.Value, want)
			}
			return nil
		}
	}
	return fmt.Errorf("no custom result %q on %s (have %+v)", name, path, rec.CustomResults)
}

func (w *world) findIssue(path, id string) bool {
	url := w.server.URL + path
	for _, o := range w.issueOccs {
		if o.URL == url && o.IssueID == id {
			return true
		}
	}
	return false
}

func (w *world) pageHasIssue(path, id string) error {
	if !w.findIssue(path, id) {
		return fmt.Errorf("%s does not have issue %s", path, id)
	}
	return nil
}

// pageHasIssueDetail asserts the issue fired with an exact detail string. Used
// to pin measured values (e.g. SERP pixel widths) that a plain has-issue check
// cannot distinguish — a regression that changes the measurement still fires
// the issue but with a different detail.
func (w *world) pageHasIssueDetail(path, id, detail string) error {
	url := w.server.URL + path
	var seen []string
	for _, o := range w.issueOccs {
		if o.URL == url && o.IssueID == id {
			if o.Detail == detail {
				return nil
			}
			seen = append(seen, o.Detail)
		}
	}
	return fmt.Errorf("%s issue %s detail = %v, want %q", path, id, seen, detail)
}

func (w *world) pageHasNoIssue(path, id string) error {
	if w.findIssue(path, id) {
		return fmt.Errorf("%s unexpectedly has issue %s", path, id)
	}
	return nil
}
