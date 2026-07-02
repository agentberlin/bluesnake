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

// TestDepthAndInlinkGateDivergenceOracle is the H4 + M6 guard: the depth CSR and
// the inlink SQL must re-apply the followsForDepth / hyperlink gate over the raw
// `links` superset, and we prove it against a HAND-DERIVED oracle (known input →
// known expected output) on a fixture where the gate actually DIVERGES from the
// raw graph — not against values the same finalize path produced (which would be
// circular, M6), and not on an all-hyperlink fixture where the gate is a no-op
// (H4). The fixture gives /b a stored-but-not-followed shortcut (a rel=nofollow
// hyperlink from "/" and an image edge from "/a"), so reading `links` raw would
// place /b at depth 1 or 2; only the gated, followed path /→/a→/c→/b yields its
// true depth 3. Gutting followsForDepthRow to `return true` makes this fail.
func TestDepthAndInlinkGateDivergenceOracle(t *testing.T) {
	bodies := map[string]string{
		// "/" links to /a (followed), /b via rel=nofollow (stored, NOT followed),
		// and /r (an HTTP redirect to /d — the redirect-as-hop depth edge, #74 R8).
		"/": `<a href="/a">a</a> <a href="/b" rel="nofollow">b-shortcut</a> <a href="/r">r</a>`,
		// "/a" links to /c (followed) and references /b as an image (stored, NOT
		// followed — images aren't crawled).
		"/a": `<a href="/c">c</a> <img src="/b">`,
		// "/c" links to /b with a real hyperlink (the only followed path to /b).
		"/c": `<a href="/b">b</a>`,
		"/b": `<p>leaf</p>`,
		"/d": `<p>redirect target — reachable only through /r</p>`,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/r" {
			// /d is reachable ONLY through this hop: if the depth CSR drops
			// redirect edges, /d has no followed path at all (NoDepth).
			http.Redirect(w, r, "/d", http.StatusFound)
			return
		}
		body, ok := bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, "<html><head><title>t</title></head><body>%s</body></html>", body)
	}))
	t.Cleanup(srv.Close)

	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
	// Pin the gate-relevant flags so the fixture diverges regardless of defaults:
	// images are not crawled and internal nofollow links are not followed.
	cfg.Resources.Images.Crawl = false
	cfg.Scope.FollowInternalNofollow = false

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

	// Depth: re-derive from the raw links superset via the production CSR, and
	// assert the hand-derived gated depths (NOT rec.Depth — independent oracle).
	links, err := st.LinkRows()
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the raw links superset really does carry the gate-excluded edges, so
	// the test would be vacuous (gate never exercised) if they were missing.
	sawNofollow, sawImage := false, false
	for _, l := range links {
		if l.Nofollow {
			sawNofollow = true
		}
		if l.Type == "image" {
			sawImage = true
		}
	}
	if !sawNofollow || !sawImage {
		t.Fatalf("fixture did not store the gate-excluded edges (nofollow=%v image=%v) — test is vacuous", sawNofollow, sawImage)
	}
	redirects, _ := st.Redirects()
	// Vacuity guard for the redirect-hop assertion below (#74 R8): the fixture
	// must really have stored the /r -> /d redirect edge.
	if len(redirects) == 0 {
		t.Fatal("fixture stored no redirect edges — the redirect-as-hop assertion is vacuous")
	}
	pages, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	urls := make([]string, 0, len(pages))
	for u := range pages {
		urls = append(urls, u)
	}
	csr := c.RecomputeDepthsFromLinks(links, redirects, urls, []string{seed})
	// /d's depth pins redirect-as-hop (#74 R8): its ONLY path from the seed is
	// / -> /r -(HTTP redirect)-> /d, so depth(/d) = depth(/r) + 1. Dropping the
	// redirect edges from the depth CSR leaves /d with no followed path at all.
	wantDepth := map[string]int{
		seed: 0, seed + "a": 1, seed + "c": 2, seed + "b": 3,
		seed + "r": 1, seed + "d": 2,
	}
	for u, d := range wantDepth {
		if csr[u] != d {
			t.Errorf("depth(%s) = %d, want %d (the followed path; the nofollow/image shortcut to /b must be gated out, the redirect hop to /d must count)", u, csr[u], d)
		}
	}
	// The persisted depth (what finalize.Crawl wrote) must match the same oracle.
	for u, d := range wantDepth {
		if pages[u].Depth != d {
			t.Errorf("persisted depth(%s) = %d, want %d", u, pages[u].Depth, d)
		}
	}

	// Inlinks: hyperlink-only, gate-applied. /b is linked by /c (followed
	// hyperlink) — its nofollow link from "/" and image edge from "/a" must NOT
	// count. The redirect edge into /d is not a hyperlink, so /d has 0 inlinks
	// even though it has a discovery path. Hand-derived oracle, asserted on the
	// finalized column.
	wantInlinks := map[string]int{
		seed: 0, seed + "a": 1, seed + "c": 1, seed + "b": 1,
		seed + "r": 1, seed + "d": 0,
	}
	for u, n := range wantInlinks {
		if pages[u].Inlinks != n {
			t.Errorf("inlinks(%s) = %d, want %d (nofollow/image/redirect edges must not count)", u, pages[u].Inlinks, n)
		}
	}
}
