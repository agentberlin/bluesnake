package sitecheck

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const smNS = "http://www.sitemaps.org/schemas/sitemap/0.9"

func urlset(entries ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns=%q>`, smNS)
	for _, e := range entries {
		b.WriteString(e)
	}
	b.WriteString(`</urlset>`)
	return b.String()
}

func entry(loc string) string { return "<url><loc>" + loc + "</loc></url>" }

func entryLastmod(loc, lastmod string) string {
	return "<url><loc>" + loc + "</loc><lastmod>" + lastmod + "</lastmod></url>"
}

// sitemapSite serves a robots.txt (optional) and a set of sitemap bodies.
type sitemapSite struct {
	robots string            // "" = 404
	bodies map[string]string // path -> body
	gzipd  map[string]bool   // path -> serve gzip-compressed
	server *httptest.Server
}

func newSitemapSite(t *testing.T, s *sitemapSite) *sitemapSite {
	t.Helper()
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			if s.robots == "" {
				w.WriteHeader(404)
				return
			}
			fmt.Fprint(w, strings.ReplaceAll(s.robots, "BASE", s.server.URL))
			return
		}
		body, ok := s.bodies[r.URL.Path]
		if !ok {
			w.WriteHeader(404)
			return
		}
		body = strings.ReplaceAll(body, "BASE", s.server.URL)
		if s.gzipd[r.URL.Path] {
			var buf bytes.Buffer
			zw := gzip.NewWriter(&buf)
			zw.Write([]byte(body))
			zw.Close()
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(buf.Bytes())
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(s.server.Close)
	return s
}

func TestSitemapDiscoveryViaRobots(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		robots: "User-agent: *\nDisallow:\nSitemap: BASE/found.xml\n",
		bodies: map[string]string{"/found.xml": urlset(entry("BASE/a"), entry("BASE/b"))},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Missing || len(rep.Files) != 1 {
		t.Fatalf("report = %+v", rep)
	}
	f := rep.Files[0]
	if f.Source != "robots" || f.Kind != "urlset" || f.Entries != 2 || !f.DeclaredInRobots {
		t.Errorf("file = %+v", f)
	}
	if ids := findingIDs(rep); ids != nil {
		t.Errorf("healthy sitemap has findings %v", ids)
	}
}

func TestSitemapConventionProbe(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sitemap.xml": urlset(entry("BASE/a"))},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Missing || len(rep.Files) != 1 || rep.Files[0].Source != "convention" {
		t.Fatalf("report = %+v", rep)
	}
	if hasFinding(rep, "sitemap_fetch_error") {
		t.Error("a missed convention probe must not be a fetch error")
	}
}

func TestSitemapMissing(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Missing || !hasFinding(rep, "sitemap_missing") {
		t.Fatalf("Missing=%v findings=%v", rep.Missing, findingIDs(rep))
	}
}

func TestSitemapDeclaredButBroken(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		robots: "Sitemap: BASE/gone.xml\nSitemap: BASE/bad.xml\nSitemap: BASE/empty.xml\n",
		bodies: map[string]string{
			"/bad.xml":   "this is not xml at all <",
			"/empty.xml": urlset(),
		},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"sitemap_fetch_error", "sitemap_invalid_xml", "sitemap_empty"} {
		if !hasFinding(rep, id) {
			t.Errorf("findings = %v, want %s", findingIDs(rep), id)
		}
	}
}

func TestSitemapWrongRootElement(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "sitemap_invalid_xml") {
		t.Errorf("findings = %v, want sitemap_invalid_xml for an RSS root", findingIDs(rep))
	}
}

func TestSitemapMissingNamespace(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": `<?xml version="1.0"?><urlset><url><loc>https://ex.com/a</loc></url></urlset>`},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "sitemap_invalid_xml") {
		t.Errorf("findings = %v, want sitemap_invalid_xml for a missing xmlns", findingIDs(rep))
	}
}

func TestSitemapEntryValidation(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": urlset(
			entryLastmod("BASE/ok", "2026-01-15"),
			entryLastmod("BASE/ok2", "2026-01-15T10:30:00+01:00"),
			entryLastmod("BASE/bad-date", "15/01/2026"),
			entry("https://other-host.example/cross"),
			entry("BASE/dup"),
			entry("BASE/dup"),
		)},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f := rep.Files[0]
	if f.Entries != 6 || f.CrossHost != 1 || f.InvalidLastmod != 1 || f.Duplicates != 1 {
		t.Fatalf("file = %+v", f)
	}
	for _, id := range []string{"sitemap_cross_host_urls", "sitemap_invalid_lastmod"} {
		if !hasFinding(rep, id) {
			t.Errorf("findings = %v, want %s", findingIDs(rep), id)
		}
	}
}

// Cross-host entries in a robots.txt-declared sitemap are a legitimate
// cross-submission (sitemaps.org), so they are recorded but not a finding.
func TestSitemapCrossHostExemptWhenDeclaredInRobots(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		robots: "Sitemap: BASE/sm.xml\n",
		bodies: map[string]string{"/sm.xml": urlset(entry("https://other-host.example/x"))},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files[0].CrossHost != 1 {
		t.Fatalf("file = %+v, want the cross-host entry recorded", rep.Files[0])
	}
	if hasFinding(rep, "sitemap_cross_host_urls") {
		t.Error("robots-declared sitemap must be exempt from the cross-host finding")
	}
}

func TestSitemapOver50k(t *testing.T) {
	var entries strings.Builder
	for i := 0; i <= maxSitemapURLs; i++ { // one over the limit
		fmt.Fprintf(&entries, "<url><loc>https://ex.com/p/%d</loc></url>", i)
	}
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": urlset(entries.String())},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "sitemap_over_50k") {
		t.Errorf("findings = %v, want sitemap_over_50k", findingIDs(rep))
	}
}

func TestSitemapGzipAndIndexRecursion(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		robots: "Sitemap: BASE/index.xml\n",
		bodies: map[string]string{
			"/index.xml": fmt.Sprintf(`<?xml version="1.0"?><sitemapindex xmlns=%q>`+
				`<sitemap><loc>BASE/child1.xml.gz</loc></sitemap>`+
				`<sitemap><loc>BASE/child-missing.xml</loc></sitemap>`+
				`</sitemapindex>`, smNS),
			"/child1.xml.gz": urlset(entry("BASE/a")),
		},
		gzipd: map[string]bool{"/child1.xml.gz": true},
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL, SitemapOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 3 {
		t.Fatalf("files = %d, want index + 2 children: %+v", len(rep.Files), rep.Files)
	}
	index := rep.Files[0]
	if index.Kind != "sitemapindex" || len(index.Children) != 2 {
		t.Errorf("index = %+v", index)
	}
	var gz *SitemapFile
	for i := range rep.Files {
		if strings.HasSuffix(rep.Files[i].URL, "child1.xml.gz") {
			gz = &rep.Files[i]
		}
	}
	if gz == nil || !gz.Gzip || gz.Entries != 1 || gz.Source != "index" || !gz.DeclaredInRobots {
		t.Fatalf("gzip child = %+v", gz)
	}
	if !hasFinding(rep, "sitemap_fetch_error") { // the missing child
		t.Errorf("findings = %v, want sitemap_fetch_error for the missing child", findingIDs(rep))
	}
}

func TestSitemapKnownSkipsDiscovery(t *testing.T) {
	robotsFetched := false
	var s *httptest.Server
	s = serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			robotsFetched = true
			w.WriteHeader(404)
		case "/known.xml":
			w.Write([]byte(urlset(entry(s.URL + "/a"))))
		default:
			w.WriteHeader(404)
		}
	})
	rep, err := newChecker(t).Sitemaps(context.Background(), s.URL, SitemapOptions{
		Declared:      []string{s.URL + "/known.xml"},
		RobotsChecked: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if robotsFetched {
		t.Error("robots.txt fetched although RobotsChecked was set")
	}
	if len(rep.Files) != 1 || rep.Files[0].Source != "declared" {
		t.Fatalf("report = %+v", rep)
	}
}

func TestSitemapCheckEntries(t *testing.T) {
	s := newSitemapSite(t, &sitemapSite{
		bodies: map[string]string{"/sm.xml": urlset(entry("BASE/hit-a"), entry("BASE/hit-b"), entry("BASE/hit-c"))},
	})
	s.bodies["/hit-a"] = "<html></html>" // 200
	// /hit-b and /hit-c 404
	rep, err := newChecker(t).Sitemaps(context.Background(), s.server.URL+"/sm.xml", SitemapOptions{CheckEntries: 2})
	if err != nil {
		t.Fatal(err)
	}
	checks := rep.Files[0].EntryChecks
	if len(checks) != 2 || checks[0].Status != 200 || checks[1].Status != 404 {
		t.Fatalf("entry checks = %+v", checks)
	}
}
