package issues

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/serpwidth"
)

func occDetail(occs []Occurrence, url, id string) string {
	for _, o := range occs {
		if o.URL == url && o.IssueID == id {
			return o.Detail
		}
	}
	return ""
}

func titledPage(url, title string) *crawler.PageRecord {
	return htmlPage(url, &parse.Facts{
		Titles: []string{title},
		H1s:    []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
	})
}

func describedPage(url, desc string) *crawler.PageRecord {
	return htmlPage(url, &parse.Facts{
		Titles:       []string{"a reasonable length page title here"},
		Descriptions: []string{desc},
		H1s:          []string{"h"}, H2s: []string{"x"}, HeadingLevels: []int{1},
	})
}

func TestTitlePixelIssues(t *testing.T) {
	// 55 W's: 55 chars (within the 30..60 char window) but
	// 55 * 944 units * 20px / 1000 = 1038px, far over the 561px default.
	wide := strings.Repeat("W", 55)
	// 40 l's: 40 chars (within the char window) but
	// 40 * 222 * 20 / 1000 = 177.6 -> 178px, under the 200px default.
	narrow := strings.Repeat("l", 40)
	occs := eval(
		titledPage("https://ex.com/wide", wide),
		titledPage("https://ex.com/narrow", narrow),
		titledPage("https://ex.com/normal", "The quick brown fox jumps over the lazy dog once mor"),
		htmlPage("https://ex.com/none", &parse.Facts{H2s: []string{"x"}}),
		titledPage("https://ex.com/empty", ""),
	)

	if !has(occs, "https://ex.com/wide", "title_over_pixels") {
		t.Error("missing title_over_pixels on wide title")
	}
	if has(occs, "https://ex.com/wide", "title_over_chars") {
		t.Error("55-char title must not trip the character check")
	}
	if got, want := occDetail(occs, "https://ex.com/wide", "title_over_pixels"),
		fmt.Sprintf("%dpx", serpwidth.Title(wide)); got != want {
		t.Errorf("title_over_pixels detail = %q, want %q", got, want)
	}
	if got := occDetail(occs, "https://ex.com/wide", "title_over_pixels"); got != "1038px" {
		t.Errorf("title_over_pixels detail = %q, want \"1038px\"", got)
	}

	if !has(occs, "https://ex.com/narrow", "title_below_pixels") {
		t.Error("missing title_below_pixels on narrow title")
	}
	if has(occs, "https://ex.com/narrow", "title_below_chars") {
		t.Error("40-char title must not trip the character check")
	}
	if got := occDetail(occs, "https://ex.com/narrow", "title_below_pixels"); got != "178px" {
		t.Errorf("title_below_pixels detail = %q, want \"178px\"", got)
	}

	if has(occs, "https://ex.com/wide", "title_below_pixels") ||
		has(occs, "https://ex.com/narrow", "title_over_pixels") {
		t.Error("over and below pixel checks are mutually exclusive")
	}
	for _, url := range []string{"https://ex.com/normal", "https://ex.com/none", "https://ex.com/empty"} {
		for _, id := range []string{"title_over_pixels", "title_below_pixels"} {
			if has(occs, url, id) {
				t.Errorf("unexpected %s on %s", id, url)
			}
		}
	}
}

func TestDescriptionPixelIssues(t *testing.T) {
	// 100 W's: 100 chars (within the 70..155 char window) but
	// 100 * 944 * 13.9 / 1000 = 1312.16 -> 1312px, over the 985px default.
	wide := strings.Repeat("W", 100)
	// 125 l's: 125 chars (within the char window) but
	// 125 * 222 * 13.9 / 1000 = 385.725 -> 386px, under the 400px default.
	narrow := strings.Repeat("l", 125)
	normal := "The quick brown fox jumps over the lazy dog while the lazy dog " +
		"watches the quick brown fox jump over it again and again all day"
	occs := eval(
		describedPage("https://ex.com/wide", wide),
		describedPage("https://ex.com/narrow", narrow),
		describedPage("https://ex.com/normal", normal),
		titledPage("https://ex.com/none", "a reasonable length page title here"),
		describedPage("https://ex.com/empty", ""),
	)

	if !has(occs, "https://ex.com/wide", "description_over_pixels") {
		t.Error("missing description_over_pixels on wide description")
	}
	if has(occs, "https://ex.com/wide", "description_over_chars") {
		t.Error("100-char description must not trip the character check")
	}
	if got, want := occDetail(occs, "https://ex.com/wide", "description_over_pixels"),
		fmt.Sprintf("%dpx", serpwidth.Description(wide)); got != want {
		t.Errorf("description_over_pixels detail = %q, want %q", got, want)
	}
	if got := occDetail(occs, "https://ex.com/wide", "description_over_pixels"); got != "1312px" {
		t.Errorf("description_over_pixels detail = %q, want \"1312px\"", got)
	}

	if !has(occs, "https://ex.com/narrow", "description_below_pixels") {
		t.Error("missing description_below_pixels on narrow description")
	}
	if has(occs, "https://ex.com/narrow", "description_below_chars") {
		t.Error("125-char description must not trip the character check")
	}
	if got := occDetail(occs, "https://ex.com/narrow", "description_below_pixels"); got != "386px" {
		t.Errorf("description_below_pixels detail = %q, want \"386px\"", got)
	}

	for _, url := range []string{"https://ex.com/normal", "https://ex.com/none", "https://ex.com/empty"} {
		for _, id := range []string{"description_over_pixels", "description_below_pixels"} {
			if has(occs, url, id) {
				t.Errorf("unexpected %s on %s", id, url)
			}
		}
	}
}

func TestPixelChecksDisabledAtZero(t *testing.T) {
	cfg := config.Default()
	cfg.Thresholds.Title.MaxPx = 0
	cfg.Thresholds.Title.MinPx = 0
	cfg.Thresholds.Description.MaxPx = 0
	cfg.Thresholds.Description.MinPx = 0

	pages := map[string]*crawler.PageRecord{}
	for _, p := range []*crawler.PageRecord{
		titledPage("https://ex.com/wide-title", strings.Repeat("W", 55)),
		titledPage("https://ex.com/narrow-title", strings.Repeat("l", 40)),
		describedPage("https://ex.com/wide-desc", strings.Repeat("W", 100)),
		describedPage("https://ex.com/narrow-desc", strings.Repeat("l", 130)),
	} {
		pages[p.URL] = p
	}
	occs := Evaluate(pages, cfg)
	for url := range pages {
		for _, id := range []string{"title_over_pixels", "title_below_pixels",
			"description_over_pixels", "description_below_pixels"} {
			if has(occs, url, id) {
				t.Errorf("%s fired on %s although pixel thresholds are zero", id, url)
			}
		}
	}
}

func TestPixelIssueCatalogue(t *testing.T) {
	want := []struct {
		id, tab, name string
		sev           Severity
		pri           Priority
	}{
		{"title_over_pixels", "page_titles", "Over X Pixels", Opportunity, Medium},
		{"title_below_pixels", "page_titles", "Below X Pixels", Opportunity, Medium},
		{"description_over_pixels", "meta_description", "Over X Pixels", Opportunity, Low},
		{"description_below_pixels", "meta_description", "Below X Pixels", Opportunity, Low},
	}
	for _, w := range want {
		d, ok := Lookup(w.id)
		if !ok {
			t.Errorf("%s missing from catalogue", w.id)
			continue
		}
		if d.Tab != w.tab || d.Name != w.name || d.Severity != w.sev || d.Priority != w.pri {
			t.Errorf("%s = %+v, want tab=%s name=%q severity=%s priority=%s",
				w.id, d, w.tab, w.name, w.sev, w.pri)
		}
	}
}
