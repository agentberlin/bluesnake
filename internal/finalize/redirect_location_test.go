package finalize

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// threePageSite serves, under every prefix, a three-page site: home links to a,
// a links to b. The start URL of each copy (the bare prefix) answers 301 with
// the Location the caller chose for it.
func threePageSite(t *testing.T, locations func(path string, origin string) (string, bool)) *httptest.Server {
	t.Helper()
	next := map[string]string{"home": "a", "a": "b", "b": ""}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if loc, ok := locations(r.URL.Path, srv.URL); ok {
			w.Header().Set("Location", loc)
			w.WriteHeader(http.StatusMovedPermanently)
			return
		}
		i := strings.LastIndex(r.URL.Path, "/")
		prefix, page := r.URL.Path[:i+1], r.URL.Path[i+1:]
		if page == "" {
			page = "home"
		}
		link, ok := next[page]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		body := ""
		if link != "" {
			body = fmt.Sprintf(`<a href="%s%s">next</a>`, prefix, link)
		}
		fmt.Fprintf(w, "<html><head><title>%s</title></head><body><p>The %s page.</p>%s</body></html>", page, page, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawlDepths runs a crawl through the store and the production finalize path
// and returns the persisted page records (depth is what the assertions read).
func crawlDepths(t *testing.T, seed string) map[string]*crawler.PageRecord {
	t.Helper()
	dir := t.TempDir()
	cfg := config.Default()
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c, err := crawler.New(cfg, crawler.WithSink(st))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), seed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Crawl(c, st, res, Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true}); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	return pages
}

func assertDepths(t *testing.T, got map[string]*crawler.PageRecord, want map[string]int) {
	t.Helper()
	for u, d := range want {
		if g, ok := got[u]; !ok {
			var crawled []string
			for gu, gp := range got {
				crawled = append(crawled, fmt.Sprintf("%s status=%d err=%q redirect=%q", gu, gp.StatusCode, gp.FetchError, gp.RedirectURL))
			}
			t.Errorf("%s was not crawled at all; crawled: %s", u, strings.Join(crawled, "; "))
		} else if g.Depth != d {
			t.Errorf("depth(%s) = %d, want %d", u, g.Depth, d)
		}
	}
}

// TestDepthFollowsRedirectLocationSpellings: the crawl follows a redirect by its
// target's normal form, and finalize's depth step must take the very same hop.
// Each copy of the site sits behind one 301 from its start URL; only the
// spelling of the Location differs, and every spelling that urlutil.Normalize
// would rewrite must yield the depths the normal-form control does (0, 1, 2, 3).
// Before the fix every page past such a redirect had no depth at all.
func TestDepthFollowsRedirectLocationSpellings(t *testing.T) {
	srv := threePageSite(t, func(path, origin string) (string, bool) {
		u, _ := url.Parse(origin)
		switch path {
		case "/c/": // control: already in normal form
			return "/c/home", true
		case "/f/": // a fragment, which normal form strips
			return "/f/home#top", true
		case "/u/": // an upper-case host, which normal form lowercases (the seed
			// for this copy is spelled with "localhost", below: an IP has no case)
			return "http://LOCALHOST:" + u.Port() + "/u/home", true
		}
		return "", false
	})
	ipOrigin := srv.URL
	// The upper-case-host copy needs a host NAME: IP literals have no case.
	hostOrigin := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)

	cases := []struct {
		name, origin, prefix string
	}{
		{"normal form (control)", ipOrigin, "/c/"},
		{"fragment", ipOrigin, "/f/"},
		{"upper-case host", hostOrigin, "/u/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seed := tc.origin + tc.prefix
			got := crawlDepths(t, seed)
			assertDepths(t, got, map[string]int{
				seed:                           0,
				tc.origin + tc.prefix + "home": 1,
				tc.origin + tc.prefix + "a":    2,
				tc.origin + tc.prefix + "b":    3,
			})
		})
	}

	// The apex-to-www shape: a Location that is a bare origin with no path.
	// Normal form gives it the "/" the home page is stored under.
	t.Run("origin without a path", func(t *testing.T) {
		srv := threePageSite(t, func(path, origin string) (string, bool) {
			if path == "/start" {
				return origin, true
			}
			return "", false
		})
		seed := srv.URL + "/start"
		got := crawlDepths(t, seed)
		assertDepths(t, got, map[string]int{
			seed:           0,
			srv.URL + "/":  1,
			srv.URL + "/a": 2,
			srv.URL + "/b": 3,
		})
	})
}
