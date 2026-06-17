package llmstxt

import (
	"reflect"
	"testing"
)

func TestParseValidDocument(t *testing.T) {
	doc := `# Example Project

> A concise summary of the project for LLMs.

Some free prose that should not be treated as a link list.

## Docs

- [Quickstart](https://ex.com/quickstart): get going fast
- [Reference](/reference)

## Optional

- [Changelog](https://ex.com/changelog)
`
	f := Parse([]byte(doc))
	if !f.Valid() {
		t.Fatalf("expected valid file")
	}
	if f.Title != "Example Project" {
		t.Errorf("title = %q", f.Title)
	}
	if f.Summary != "A concise summary of the project for LLMs." {
		t.Errorf("summary = %q", f.Summary)
	}
	if f.Malformed {
		t.Errorf("unexpected malformed flag")
	}
	want := []Link{
		{Section: "Docs", Name: "Quickstart", URL: "https://ex.com/quickstart", Desc: "get going fast"},
		{Section: "Docs", Name: "Reference", URL: "/reference"},
		{Section: "Optional", Name: "Changelog", URL: "https://ex.com/changelog"},
	}
	if !reflect.DeepEqual(f.Links, want) {
		t.Errorf("links = %#v, want %#v", f.Links, want)
	}
}

func TestParseStructuralValidation(t *testing.T) {
	tests := []struct {
		name      string
		doc       string
		title     string
		summary   string
		malformed bool
		links     int
	}{
		{
			name:  "no title",
			doc:   "> just a summary\n\n## S\n- [a](https://x.ex/a)\n",
			title: "", summary: "just a summary", links: 1,
		},
		{
			name:  "title only",
			doc:   "# Only A Title\n",
			title: "Only A Title",
		},
		{
			name:  "missing summary",
			doc:   "# Title\n\n## S\n- [a](https://x.ex/a)\n",
			title: "Title", summary: "", links: 1,
		},
		{
			name:  "malformed bullet in section",
			doc:   "# Title\n\n> sum\n\n## S\n- not a link at all\n- [ok](https://x.ex/ok)\n",
			title: "Title", summary: "sum", malformed: true, links: 1,
		},
		{
			name:  "bullets before any section are prose, not malformed",
			doc:   "# Title\n\n- a free bullet\n- another\n",
			title: "Title", malformed: false, links: 0,
		},
		{
			name:    "multi-line blockquote summary joins",
			doc:     "# Title\n> line one\n> line two\n\n## S\n- [a](https://x.ex/a)\n",
			title:   "Title",
			summary: "line one line two", links: 1,
		},
		{
			name: "empty document",
			doc:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := Parse([]byte(tt.doc))
			if f.Title != tt.title {
				t.Errorf("title = %q, want %q", f.Title, tt.title)
			}
			if f.Summary != tt.summary {
				t.Errorf("summary = %q, want %q", f.Summary, tt.summary)
			}
			if f.Malformed != tt.malformed {
				t.Errorf("malformed = %v, want %v", f.Malformed, tt.malformed)
			}
			if len(f.Links) != tt.links {
				t.Errorf("links = %d, want %d", len(f.Links), tt.links)
			}
			if (f.Title != "") != f.Valid() {
				t.Errorf("Valid() inconsistent with title presence")
			}
		})
	}
}

func TestParseAsteriskBullets(t *testing.T) {
	f := Parse([]byte("# T\n\n## S\n* [a](https://x.ex/a): d\n"))
	if len(f.Links) != 1 || f.Links[0].URL != "https://x.ex/a" || f.Links[0].Desc != "d" {
		t.Fatalf("asterisk bullet not parsed: %#v", f.Links)
	}
}
