package bundle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/finalize"
	"github.com/agentberlin/bluesnake/internal/store"
)

// nearCopySite serves /a and /b, 200-word pages one word apart; /c, unrelated;
// /noindex, a copy of /a that is noindex; and a short home page linking them.
func nearCopySite(t *testing.T) *httptest.Server {
	t.Helper()
	words := func(prefix string, n int) []string {
		w := make([]string, n)
		for i := range w {
			w[i] = fmt.Sprintf("%s%d", prefix, i)
		}
		return w
	}
	a := words("alpha", 200)
	b := append([]string(nil), a...)
	b[100] = "swapped"
	page := func(head string, ws []string) string {
		return "<html><head><title>t</title>" + head + "</head><body><p>" + strings.Join(ws, " ") + "</p></body></html>"
	}
	bodies := map[string]string{
		"/":        `<html><body><a href="/a">a</a><a href="/b">b</a><a href="/c">c</a><a href="/noindex">n</a></body></html>`,
		"/a":       page("", a),
		"/b":       page("", b),
		"/c":       page("", words("zeta", 200)),
		"/noindex": page(`<meta name="robots" content="noindex">`, a),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// crawlNearCopies crawls nearCopySite the way Berlin does for a brand's own
// site — analysis off, near copies on at 80% for pages of 50 words or more —
// and finalizes it.
func crawlNearCopies(t *testing.T, enabled bool) (*store.Crawl, store.Info) {
	t.Helper()
	srv := nearCopySite(t)
	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Analysis.Auto = false
	cfg.Content.NearDuplicates.Enabled = enabled
	cfg.Content.NearDuplicates.Threshold = 80
	cfg.Content.NearDuplicates.MinWords = 50
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
	if _, err := finalize.Crawl(c, st, res, finalize.Params{StoreDir: dir, Cfg: cfg, Seeds: []string{seed}, Completed: true}); err != nil {
		t.Fatal(err)
	}
	info, err := store.CrawlInfo(dir, st.ID)
	if err != nil {
		t.Fatal(err)
	}
	return st, info
}

// pageLine is a page's raw line, for the keys whose null-or-[] the decoded
// struct cannot show apart.
func pageLine(t *testing.T, raw []byte, url string) string {
	t.Helper()
	for _, line := range pageLines(raw) {
		if strings.HasPrefix(line, `{"url":"`+url+`"`) {
			return line
		}
	}
	t.Fatalf("no line for %s", url)
	return ""
}

// TestBundleCarriesNearCopies: each compared page carries its near copies and
// their count; a compared page with none carries [] and 0; a page that was not
// compared carries null for both; the header records the settings.
func TestBundleCarriesNearCopies(t *testing.T) {
	st, info := crawlNearCopies(t, true)
	h, pages, raw := bundleOf(t, st, info, Options{})
	if h.NearCopySettings == nil || *h.NearCopySettings != (NearCopySettings{Threshold: 80, MinWords: 50, IndexableOnly: true}) {
		t.Errorf("header near_copy_settings = %+v", h.NearCopySettings)
	}
	a, b := pageByPath(pages, "/a"), pageByPath(pages, "/b")
	if a == nil || b == nil {
		t.Fatal("/a or /b missing")
	}
	// 196 shingles each, 191 shared: 191/201.
	const share = 0.9502
	if len(a.NearCopies) != 1 || a.NearCopies[0] != (NearCopy{URL: b.URL, Share: share}) ||
		a.NearCopyCount == nil || *a.NearCopyCount != 1 {
		t.Errorf("/a near copies = %+v count %v", a.NearCopies, a.NearCopyCount)
	}
	if len(b.NearCopies) != 1 || b.NearCopies[0] != (NearCopy{URL: a.URL, Share: share}) {
		t.Errorf("/b near copies = %+v, want /a at the same share", b.NearCopies)
	}
	if a.ClosestSimilarity != 95.02 || a.NearDupCount != 1 {
		t.Errorf("/a closest_similarity %v near_dup_count %d, want the list's 95.02 and 1", a.ClosestSimilarity, a.NearDupCount)
	}
	c := pageByPath(pages, "/c")
	if line := pageLine(t, raw, c.URL); !strings.Contains(line, `"near_copies":[],"near_copy_count":0,`) {
		t.Errorf("/c was compared and has none — want [] and 0:\n%.400s", line)
	}
	for _, path := range []string{"/noindex", "/"} { // noindex; under 50 words
		p := pageByPath(pages, path)
		if line := pageLine(t, raw, p.URL); !strings.Contains(line, `"near_copies":null,"near_copy_count":null,`) {
			t.Errorf("%s was not compared — want null:\n%.400s", path, line)
		}
	}
	if !strings.Contains(string(raw), `"share":0.9502}`) {
		t.Error("share not written to 4 places")
	}
}

// TestBundleNearCopiesNullWhenTheStepDidNotRun: with the check off — or on a
// crawl stored before lists were kept — every page reads not compared, and the
// header's settings are null: never an empty list.
func TestBundleNearCopiesNullWhenTheStepDidNotRun(t *testing.T) {
	st, info := crawlNearCopies(t, false)
	_, pages, raw := bundleOf(t, st, info, Options{})
	if !strings.Contains(strings.SplitN(string(raw), "\n", 2)[0], `"near_copy_settings":null`) {
		t.Error("header near_copy_settings is not null")
	}
	for _, p := range pages {
		if line := pageLine(t, raw, p.URL); !strings.Contains(line, `"near_copies":null,"near_copy_count":null,`) {
			t.Errorf("%s: want null near copies:\n%.300s", p.URL, line)
		}
	}
}
