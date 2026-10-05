package parse

import (
	"slices"
	"testing"
)

// Headings is the one heading record: every h1–h6 in document order, each with
// its own text — h3–h6 included, which no per-level list used to keep.
func TestHeadingsRecordEveryLevelInDocumentOrder(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<h2>Intro</h2>
		<h1>  Main
			heading </h1>
		<h3>Detail</h3><h4>Finer</h4><h5>Finest</h5><h6>Smallest</h6>
		<h2></h2>
		<h1>Second</h1>
	</body></html>`, nil, nil)
	want := []Heading{
		{Level: 2, Text: "Intro"},
		{Level: 1, Text: "Main heading"},
		{Level: 3, Text: "Detail"},
		{Level: 4, Text: "Finer"},
		{Level: 5, Text: "Finest"},
		{Level: 6, Text: "Smallest"},
		{Level: 2},
		{Level: 1, Text: "Second"},
	}
	if !slices.Equal(f.Headings, want) {
		t.Fatalf("headings =\n  %+v\nwant\n  %+v", f.Headings, want)
	}
	// The per-level views the checks read are derived from it.
	if got := f.HeadingTexts(1); !slices.Equal(got, []string{"Main heading", "Second"}) {
		t.Errorf("h1 texts = %q", got)
	}
	if got := f.HeadingTexts(2); !slices.Equal(got, []string{"Intro", ""}) {
		t.Errorf("h2 texts = %q, want the empty h2 kept in place", got)
	}
	if got := f.HeadingLevels(); !slices.Equal(got, []int{2, 1, 3, 4, 5, 6, 2, 1}) {
		t.Errorf("heading levels = %v", got)
	}
}

// A page without headings has no record, and its views are empty.
func TestHeadingsAbsent(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body><p>No headings.</p></body></html>`, nil, nil)
	if f.Headings != nil || len(f.HeadingTexts(1)) != 0 || len(f.HeadingLevels()) != 0 {
		t.Errorf("headings = %+v, levels = %v", f.Headings, f.HeadingLevels())
	}
}
