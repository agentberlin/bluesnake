package finalize

// The lite finalize path — a page map without link lists or bodies, the
// per-page link and content checks streamed in one page at a time — must
// evaluate the catalogue exactly as a whole-map issues.Evaluate does, under
// every setting of the two flags that gate the duplicate checks:
// ignore_non_indexable_for_issues and ignore_paginated_for_duplicates. The
// duplicate checks run over the lite map, so this also proves the lite map
// keeps every fact they read (titles, the rel=prev arrays, indexability).

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/store"
)

// dupGateGraph: four pages share the title "Dup" — two plain ones, one
// noindex (dropped by ignoreNonIndexable), one paginated via rel=prev (dropped
// by ignorePaginated) — so each flag visibly changes the duplicate set.
func dupGateGraph(t *testing.T) *store.Crawl {
	t.Helper()
	bodies := map[string]string{
		"/":   `<html><head><title>Home</title></head><body><a href="/x1">1</a><a href="/x2">2</a><a href="/nx">3</a><a href="/pg">4</a></body></html>`,
		"/x1": `<html><head><title>Dup</title></head><body><p>one</p></body></html>`,
		"/x2": `<html><head><title>Dup</title></head><body><p>two</p></body></html>`,
		"/nx": `<html><head><title>Dup</title><meta name="robots" content="noindex"></head><body><p>three</p></body></html>`,
		"/pg": `<html><head><title>Dup</title><link rel="prev" href="/x1"></head><body><p>four</p></body></html>`,
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

	seed := srv.URL + "/"
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
	return st
}

func TestLitePathIssueParity_GateMatrix(t *testing.T) {
	st := dupGateGraph(t)
	full, err := st.LoadPages()
	if err != nil {
		t.Fatal(err)
	}
	lite, err := st.LoadPagesLite()
	if err != nil {
		t.Fatal(err)
	}
	dupIDs := map[string]bool{
		"content_exact_duplicate": true, "title_duplicate": true, "description_duplicate": true,
		"h1_duplicate": true, "h2_duplicate": true,
	}

	distinct := map[string]bool{} // duplicate-set fingerprints across combos (vacuity guard)
	for _, ignoreNonIdx := range []bool{false, true} {
		for _, ignorePag := range []bool{false, true} {
			name := fmt.Sprintf("nonIndexable=%v/paginated=%v", ignoreNonIdx, ignorePag)
			cfg := config.Default()
			cfg.Advanced.IgnoreNonIndexableForIssues = ignoreNonIdx
			cfg.Advanced.IgnorePaginatedForDuplicates = ignorePag

			want := occurrenceSet(issues.Evaluate(full, cfg))
			got, err := evaluateIssues(st, lite, cfg)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			gotSet := occurrenceSet(got)
			dups := 0
			for k := range want {
				if _, ok := gotSet[k]; !ok {
					t.Errorf("%s: lite path missing %q (present in the whole-map evaluation)", name, k)
				}
			}
			for k, id := range gotSet {
				if _, ok := want[k]; !ok {
					t.Errorf("%s: lite path has extra %q (absent in the whole-map evaluation)", name, k)
				}
				if dupIDs[id] {
					dups++
				}
			}
			if dups == 0 {
				t.Fatalf("%s: fixture produced no duplicate occurrences — parity is vacuous", name)
			}
			distinct[fmt.Sprint(dups)] = true
		}
	}
	// Vacuity guard: the flags must actually bind on this fixture — at least
	// the all-off and all-on combos differ in occurrence count (4 vs 2 pages
	// carry the shared title).
	if len(distinct) < 2 {
		t.Errorf("every flag combination produced the same duplicate count — the gate clauses were never exercised: %v", distinct)
	}
}

// occurrenceSet keys occurrences by url|issue|detail, mapping to the issue id.
func occurrenceSet(occs []issues.Occurrence) map[string]string {
	set := make(map[string]string, len(occs))
	for _, o := range occs {
		set[o.URL+"|"+o.IssueID+"|"+o.Detail] = o.IssueID
	}
	return set
}
