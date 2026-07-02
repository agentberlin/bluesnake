package finalize

// #75 bug 1 (pipeline half): the `issues` command re-evaluates only the
// catalogue checks, so it must leave the analysis-phase occurrences —
// redirect_chain, content_near_duplicate, hreflang_*, sitemap_*, llms_txt_* —
// byte-identical in the store. Before the ownership-partitioned SaveIssues it
// deleted ALL rows and re-inserted only the catalogue's, silently degrading a
// completed crawl's stored findings until the next full Analyze.

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestIssuesCmdPreservesAnalysisIssues crawls a fixture that fires analysis-phase
// checks from three different analyzers (a 2-hop redirect chain, a near-duplicate
// pair, an hreflang cluster with a missing return link), finalizes it (full
// analyze), then runs the catalogue-only refresh and asserts the entire issues
// table survives byte-identically.
func TestIssuesCmdPreservesAnalysisIssues(t *testing.T) {
	words := make([]string, 400)
	for i := range words {
		words[i] = fmt.Sprintf("word%d", i)
	}
	nd1 := strings.Join(words, " ")
	words[200] = "changed"
	nd2 := strings.Join(words, " ")

	page := func(head, body string) string {
		return "<html><head><title>A Sensible Fixture Page Title Here</title>" + head + "</head><body>" + body + "</body></html>"
	}
	links := `<a href="/r1">r</a><a href="/nd1">n1</a><a href="/nd2">n2</a><a href="/hl-en">en</a><a href="/hl-de">de</a>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			fmt.Fprint(w, page("", links))
		case "/r1":
			http.Redirect(w, r, "/r2", http.StatusMovedPermanently)
		case "/r2":
			http.Redirect(w, r, "/ok", http.StatusMovedPermanently)
		case "/ok":
			fmt.Fprint(w, page("", "the chain target page"))
		case "/nd1":
			fmt.Fprint(w, page("", nd1))
		case "/nd2":
			fmt.Fprint(w, page("", nd2))
		case "/hl-en":
			fmt.Fprint(w, page(`<link rel="alternate" hreflang="de" href="/hl-de">`, "english page"))
		case "/hl-de":
			fmt.Fprint(w, page(`<link rel="alternate" hreflang="de" href="/hl-de">`, "german page"))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()

	seed := srv.URL + "/"
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	st, err := store.CreateCrawl(dir, []string{seed}, "spider", cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
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

	dump := func() []string {
		rows, err := st.DB().Query(`SELECT url, issue, detail FROM issues ORDER BY url, issue, detail`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var url, issue, detail string
			if err := rows.Scan(&url, &issue, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, url+"|"+issue+"|"+detail)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	before := dump()

	// Vacuity guard: the fixture must actually have fired one analysis-phase
	// check per analyzer family, or the survival assertion below proves nothing.
	for _, want := range []string{"|redirect_chain|", "|content_near_duplicate|", "|hreflang_missing_return|"} {
		found := false
		for _, row := range before {
			if strings.Contains(row, want) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("fixture never fired %s — issues table:\n%s", strings.Trim(want, "|"), strings.Join(before, "\n"))
		}
	}

	// The cheap catalogue-only refresh the `issues` command runs.
	if err := Issues(st, cfg); err != nil {
		t.Fatalf("Issues: %v", err)
	}

	after := dump()
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("issues refresh changed the stored issue set\nbefore (%d rows):\n%s\nafter (%d rows):\n%s",
			len(before), strings.Join(before, "\n"), len(after), strings.Join(after, "\n"))
	}
}
