package sitecheck

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/render"
)

func parseDoc(t *testing.T, html string) *parse.Facts {
	t.Helper()
	return parse.Parse("https://ex.com/", []byte(html), nil, config.Default())
}

// The diff core is pure over two parses — Chrome-free.
func TestDiffFactsAndFindings(t *testing.T) {
	raw := parseDoc(t, `<html><head><title>Shell</title>
		<meta name="robots" content="noindex">
		<link rel="canonical" href="https://ex.com/raw">
		</head><body><p>tiny</p></body></html>`)
	rendered := parseDoc(t, `<html><head><title>Hydrated App</title>
		<meta name="description" content="injected description">
		<link rel="canonical" href="https://ex.com/rendered">
		</head><body><h1>App</h1>
		<a href="https://ex.com/a">a</a><a href="https://ex.com/b">b</a>
		<p>`+longText(200)+`</p></body></html>`)

	rep := &RenderDiffReport{URL: "https://ex.com/", Rendered: true}
	diffFacts(rep, raw, rendered, []string{"boom"})

	if !rep.TitleChanged || !rep.CanonicalChanged || !rep.DescriptionChanged || !rep.H1Changed {
		t.Errorf("element diffs = %+v", rep)
	}
	if !rep.NoindexOnlyRaw {
		t.Error("noindex removed by JS not detected")
	}
	if rep.RenderedOnlyLinks != 2 {
		t.Errorf("rendered-only links = %d, want 2", rep.RenderedOnlyLinks)
	}
	if rep.RawWordCount >= rep.RenderedWordCount {
		t.Errorf("word counts raw=%d rendered=%d", rep.RawWordCount, rep.RenderedWordCount)
	}

	ids := map[string]bool{}
	for _, f := range rep.Findings() {
		ids[f.IssueID] = true
	}
	for _, id := range []string{
		"js_dependent_content", "js_contains_links", "js_title_updated",
		"js_description_updated", "js_h1_updated", "js_canonical_mismatch",
		"js_noindex_only_raw", "js_console_errors",
	} {
		if !ids[id] {
			t.Errorf("findings %v missing %s", ids, id)
		}
	}
}

func TestDiffFactsIdenticalPageIsClean(t *testing.T) {
	doc := `<html><head><title>Same</title></head><body><h1>Same</h1><p>` + longText(100) + `</p>
		<a href="https://ex.com/x">x</a></body></html>`
	rep := &RenderDiffReport{URL: "https://ex.com/", Rendered: true}
	diffFacts(rep, parseDoc(t, doc), parseDoc(t, doc), nil)
	if got := rep.Findings(); got != nil {
		t.Errorf("identical raw/rendered produced findings %+v", got)
	}
}

// An unrendered report (fetch failed, Chrome missing mid-run) yields nothing.
func TestRenderDiffUnrenderedNoFindings(t *testing.T) {
	rep := &RenderDiffReport{URL: "https://ex.com/", RenderedWordCount: 500}
	if got := rep.Findings(); got != nil {
		t.Errorf("unrendered report produced findings %+v", got)
	}
}

func longText(words int) string {
	s := ""
	for i := 0; i < words; i++ {
		s += fmt.Sprintf("word%d ", i)
	}
	return s
}

// End-to-end against real Chrome; skips itself when none is installed
// (matching the render package's convention).
func TestRenderDiffEndToEnd(t *testing.T) {
	cfg := config.Default()
	if render.ChromePath(cfg) == "" {
		t.Skip("no Chrome/Chromium found")
	}
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>shell</title></head><body>
			<div id="app"></div>
			<script>document.title = "hydrated";
			document.getElementById("app").innerHTML = '<a href="/js-only">j</a><p>`+longText(120)+`</p>';
			</script></body></html>`)
	})
	rep, err := newChecker(t).RenderDiff(context.Background(), s.URL+"/")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Rendered {
		t.Fatalf("render failed: %s", rep.RenderError)
	}
	if !rep.TitleChanged || rep.RenderedTitle != "hydrated" {
		t.Errorf("title diff = %+v", rep)
	}
	if !hasFinding(rep, "js_dependent_content") || !hasFinding(rep, "js_contains_links") {
		t.Errorf("findings = %v", findingIDs(rep))
	}
}

func TestLlmsTool(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/llms.txt":
			fmt.Fprint(w, "# My Project\n\n> A tidy summary.\n\n## Docs\n- [Guide](/guide): the guide\n")
		default:
			w.WriteHeader(404)
		}
	})
	rep, err := newChecker(t).LlmsTxt(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Files) != 2 { // llms.txt + llms-full.txt (fetch_full default on)
		t.Fatalf("files = %+v", rep.Files)
	}
	if !rep.Files[0].Found || rep.Files[0].Title != "My Project" || len(rep.Links) != 1 {
		t.Errorf("primary = %+v links = %+v", rep.Files[0], rep.Links)
	}
	if !hasFinding(rep, "llms_full_txt_missing") {
		t.Errorf("findings = %v, want llms_full_txt_missing", findingIDs(rep))
	}
	if hasFinding(rep, "llms_txt_missing") || hasFinding(rep, "llms_txt_missing_summary") {
		t.Errorf("unexpected findings %v", findingIDs(rep))
	}
}

func TestLlmsToolMissing(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	rep, err := newChecker(t).LlmsTxt(context.Background(), s.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "llms_txt_missing") {
		t.Errorf("findings = %v, want llms_txt_missing", findingIDs(rep))
	}
	// The absent-primary short-circuit must suppress the full-file finding.
	if hasFinding(rep, "llms_full_txt_missing") {
		t.Errorf("findings = %v — absent primary short-circuits the rest", findingIDs(rep))
	}
}
