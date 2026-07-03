package robots

import "testing"

// Parse must surface the lines it skips — the tester/site-check audit needs
// them — without changing what is parsed: malformed lines still have no
// effect on verdicts.
func TestParseReportsIgnoredLines(t *testing.T) {
	data := []byte(`# a comment, not ignored-reported
Disallow: /before-any-group

User-agent: *
Disallow /missing-colon
Disallow: /fine
Allow:
Crawl-delay: 10

not a directive at all
`)
	f := Parse(data)

	want := []IgnoredLine{
		{Line: 2, Raw: "Disallow: /before-any-group"},
		{Line: 5, Raw: "Disallow /missing-colon"},
		{Line: 10, Raw: "not a directive at all"},
	}
	if len(f.Ignored) != len(want) {
		t.Fatalf("Ignored = %+v, want %+v", f.Ignored, want)
	}
	for i, w := range want {
		if f.Ignored[i] != w {
			t.Errorf("Ignored[%d] = %+v, want %+v", i, f.Ignored[i], w)
		}
	}

	// The ignored lines must not have leaked into the rules: only /fine counts.
	if n := len(f.Groups); n != 1 {
		t.Fatalf("groups = %d, want 1", n)
	}
	if n := len(f.Groups[0].Rules); n != 1 || f.Groups[0].Rules[0].Path != "/fine" {
		t.Fatalf("rules = %+v, want the single /fine rule", f.Groups[0].Rules)
	}
}

// Spec-valid constructs must never be reported as ignored: comments, blank
// lines, empty Disallow (means "no restriction"), and unknown-but-well-formed
// directives (RFC 9309: crawlers MUST ignore unrecognized directives).
func TestParseIgnoredLinesCleanFile(t *testing.T) {
	data := []byte(`# full comment
User-agent: *
Disallow:
Allow: /x
Crawl-delay: 5

Sitemap: https://ex.com/sitemap.xml
`)
	if f := Parse(data); len(f.Ignored) != 0 {
		t.Errorf("Ignored = %+v, want none", f.Ignored)
	}
}
