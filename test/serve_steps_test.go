package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/cucumber/godog"
	"github.com/hhsecond/acrawler/internal/serve"
)

func (w *world) registerServeSteps(sc *godog.ScenarioContext) {
	sc.Step(`^serving the stored crawl, GET "([^"]*)" responds (\d+) and contains "([^"]*)"$`, w.serveGetRespondsContains)
}

// serveGetRespondsContains spins up a fresh read-only API server over the
// scenario store dir for each assertion. <crawlid> and <serverurl> are
// substituted in both the request path and the expected substring.
func (w *world) serveGetRespondsContains(path string, wantStatus int, wantContains string) error {
	srv := httptest.NewServer(serve.Handler(w.storeDirPath()))
	defer srv.Close()

	sub := func(s string) string {
		s = strings.ReplaceAll(s, "<crawlid>", w.storedCrawlID)
		if w.server != nil {
			s = strings.ReplaceAll(s, "<serverurl>", w.server.URL)
		}
		return s
	}
	path = sub(path)
	wantContains = sub(wantContains)

	resp, err := http.Get(srv.URL + path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("GET %s = %d, want %d\nbody: %s", path, resp.StatusCode, wantStatus, body)
	}
	if !strings.Contains(string(body), wantContains) {
		return fmt.Errorf("GET %s body does not contain %q:\n%s", path, wantContains, body)
	}
	return nil
}
