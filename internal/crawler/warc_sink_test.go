package crawler

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
)

// archiveRecorder is a Sink that also implements the optional ArchiveSink
// extension, recording every Archive call.
type archiveRecorder struct {
	mu    sync.Mutex
	pages map[string]*PageRecord
	calls map[string][]*fetch.Result
}

var (
	_ Sink        = (*archiveRecorder)(nil)
	_ ArchiveSink = (*archiveRecorder)(nil)
)

func (a *archiveRecorder) Page(rec *PageRecord) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.pages == nil {
		a.pages = map[string]*PageRecord{}
	}
	cp := *rec
	a.pages[rec.URL] = &cp
	return nil
}
func (a *archiveRecorder) snapshot() map[string]*PageRecord {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make(map[string]*PageRecord, len(a.pages))
	for k, v := range a.pages {
		out[k] = v
	}
	return out
}
func (a *archiveRecorder) FrontierDone(string) error { return nil }

func (a *archiveRecorder) Archive(url string, res *fetch.Result) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.calls == nil {
		a.calls = map[string][]*fetch.Result{}
	}
	a.calls[url] = append(a.calls[url], res)
	return nil
}

func TestArchiveSinkReceivesFetchedResponses(t *testing.T) {
	pages := map[string]string{
		"/":           link("/missing") + link("/old") + link("/private/x"),
		"/new":        "<p>target</p>",
		"/private/x":  "<p>secret</p>",
		"/robots.txt": "User-agent: *\nDisallow: /private/\n",
	}
	s := newSite(t, pages)
	// wrap the handler so /old answers with a real 301 (same pattern as
	// TestRedirectDiscovery); /missing stays absent and 404s
	s.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits[r.URL.Path]++
		s.mu.Unlock()
		if r.URL.Path == "/old" {
			http.Redirect(w, r, "/new", http.StatusMovedPermanently)
			return
		}
		body, ok := s.pages[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	})

	sink := &archiveRecorder{}
	cfg := config.Default()
	cfg.Extraction.StoreWARC = true
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res := runCap(t, c, sink, s.server.URL+"/")

	if rec := s.page(res, "/private/x"); rec == nil || rec.State != StateBlockedRobots {
		t.Fatalf("/private/x = %+v, want blocked by robots", rec)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	for path, wantStatus := range map[string]int{"/": 200, "/missing": 404, "/old": 301, "/new": 200} {
		calls := sink.calls[s.server.URL+path]
		if len(calls) != 1 {
			t.Errorf("%s archived %d times, want exactly 1", path, len(calls))
			continue
		}
		if calls[0].StatusCode != wantStatus {
			t.Errorf("%s archived with status %d, want %d", path, calls[0].StatusCode, wantStatus)
		}
	}
	if calls := sink.calls[s.server.URL+"/"]; len(calls) == 1 {
		if got := string(calls[0].Body); got != pages["/"] {
			t.Errorf("/ archived body = %q, want %q", got, pages["/"])
		}
	}
	if calls := sink.calls[s.server.URL+"/new"]; len(calls) == 1 {
		if got := string(calls[0].Body); got != pages["/new"] {
			t.Errorf("/new archived body = %q, want %q", got, pages["/new"])
		}
	}
	if calls := sink.calls[s.server.URL+"/missing"]; len(calls) == 1 {
		if len(calls[0].Body) != 0 {
			t.Errorf("/missing archived body = %q, want empty", calls[0].Body)
		}
	}
	if calls := sink.calls[s.server.URL+"/private/x"]; len(calls) != 0 {
		t.Errorf("robots-blocked URL archived %d times, want 0", len(calls))
	}
}

func TestArchiveSinkNotCalledWhenDisabled(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a"),
		"/a": "<p>x</p>",
	})
	sink := &archiveRecorder{}
	cfg := config.Default() // extraction.store_warc defaults to false
	c, err := New(cfg, WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.calls) != 0 {
		t.Errorf("Archive called for %d URLs with store_warc disabled, want 0: %v", len(sink.calls), sink.calls)
	}
}
