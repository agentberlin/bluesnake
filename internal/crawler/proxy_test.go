package crawler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// recordingProxy is a forward proxy that marks what passes through it, so a
// crawl's recorded egress can be checked against the one that really carried
// the page rather than merely being non-empty.
type recordingProxy struct {
	srv  *httptest.Server
	name string
	mu   sync.Mutex
	hits int
}

func newRecordingProxy(t *testing.T, name string) *recordingProxy {
	t.Helper()
	p := &recordingProxy{name: name}
	p.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits++
		p.mu.Unlock()
		out, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		resp, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for name, values := range resp.Header {
			for _, v := range values {
				w.Header().Add(name, v)
			}
		}
		w.Header().Set("X-Crawl-Proxy", p.name)
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	}))
	t.Cleanup(p.srv.Close)
	return p
}

func (p *recordingProxy) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hits
}

// The egress that fetched a page must reach the page's RECORD, not just the
// fetch result. This is the first link of the attribution chain (D7/REQ-S7) —
// without it a partially-blocked crawl cannot be diagnosed after the fact, and
// every layer downstream (store, export, UI) is fed an empty value.
func TestPageRecordCarriesTheServingProxy(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":  link("/a") + link("/b"),
		"/a": "<p>a</p>",
		"/b": "<p>b</p>",
	})
	px := newRecordingProxy(t, "P1")

	res := crawl(t, s, func(cfg *config.Config) {
		cfg.HTTP.Proxies = []config.ProxyEntry{{URL: px.srv.URL}}
	})

	if px.count() == 0 {
		t.Fatal("no request reached the proxy — the crawl bypassed it")
	}
	for _, path := range []string{"/", "/a", "/b"} {
		rec := s.page(res, path)
		if rec == nil {
			t.Fatalf("%s not recorded", path)
		}
		if rec.Proxy != px.srv.URL {
			t.Errorf("%s recorded proxy = %q, want %q", path, rec.Proxy, px.srv.URL)
		}
		if rec.Headers["X-Crawl-Proxy"] != "P1" {
			t.Errorf("%s did not come back through the proxy (header = %q)",
				path, rec.Headers["X-Crawl-Proxy"])
		}
	}
}

// An unproxied crawl still records its egress: "direct" is a fact about how the
// page was fetched, and an empty column would be indistinguishable from a page
// recorded before proxy support existed.
func TestPageRecordSaysDirectWithoutAProxy(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<p>hi</p>"})
	res := crawl(t, s, nil)

	rec := s.page(res, "/")
	if rec == nil {
		t.Fatal("/ not recorded")
	}
	if rec.Proxy != "direct" {
		t.Errorf("recorded proxy = %q, want \"direct\"", rec.Proxy)
	}
}

// Credentials must not survive into anything the crawl persists.
func TestPageRecordProxyIsRedacted(t *testing.T) {
	s := newSite(t, map[string]string{"/": "<p>hi</p>"})
	px := newRecordingProxy(t, "P1")
	withCreds := strings.Replace(px.srv.URL, "http://", "http://user:sekrit@", 1)

	res := crawl(t, s, func(cfg *config.Config) {
		cfg.HTTP.Proxy = withCreds
	})

	rec := s.page(res, "/")
	if rec == nil {
		t.Fatal("/ not recorded")
	}
	for _, secret := range []string{"sekrit", "user", "@"} {
		if strings.Contains(rec.Proxy, secret) {
			t.Fatalf("recorded proxy %q leaks %q", rec.Proxy, secret)
		}
	}
	if rec.Proxy != px.srv.URL {
		t.Errorf("recorded proxy = %q, want the redacted %q", rec.Proxy, px.srv.URL)
	}
}

// Pages fetched through different egresses must be attributed to the right one
// each — a single pool-wide label would make the column useless for working out
// which IP a WAF objected to.
func TestPageRecordsAttributePerEgress(t *testing.T) {
	pages := map[string]string{"/": ""}
	var links []string
	for i := range 6 {
		p := fmt.Sprintf("/p%d", i)
		links = append(links, link(p))
		pages[p] = "<p>leaf</p>"
	}
	pages["/"] = strings.Join(links, "")
	s := newSite(t, pages)

	a, b := newRecordingProxy(t, "A"), newRecordingProxy(t, "B")
	res := crawl(t, s, func(cfg *config.Config) {
		cfg.Speed.MaxThreads = 1 // deterministic round-robin order
		cfg.HTTP.Proxies = []config.ProxyEntry{{URL: a.srv.URL}, {URL: b.srv.URL}}
	})

	byLabel := map[string]int{}
	for path := range pages {
		rec := s.page(res, path)
		if rec == nil {
			t.Fatalf("%s not recorded", path)
		}
		byLabel[rec.Proxy]++
		// The label must name the egress that actually served the page.
		want := map[string]string{a.srv.URL: "A", b.srv.URL: "B"}[rec.Proxy]
		if got := rec.Headers["X-Crawl-Proxy"]; got != want {
			t.Errorf("%s served by %q but recorded as %q", path, got, rec.Proxy)
		}
	}
	if len(byLabel) != 2 {
		t.Errorf("pages attributed to %d egresses, want both: %v", len(byLabel), byLabel)
	}
	if a.count() == 0 || b.count() == 0 {
		t.Errorf("traffic split A:%d B:%d — one egress carried nothing", a.count(), b.count())
	}
}
