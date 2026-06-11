package robots

import (
	"strings"
	"testing"
)

const sample = `User-agent: *
Disallow: /private/
Allow: /private/public-bit
Disallow: /*.pdf$

User-agent: bluesnake
Disallow: /only-for-others/

Sitemap: https://ex.com/sitemap.xml
`

func TestGroupSelection(t *testing.T) {
	f := Parse([]byte(sample))
	tests := []struct {
		ua      string
		url     string
		allowed bool
	}{
		// our specific token gets its own group, not *
		{"bluesnake", "https://ex.com/private/x", true},
		{"bluesnake", "https://ex.com/only-for-others/x", false},
		// generic agents fall back to *
		{"somebot", "https://ex.com/private/x", false},
		{"somebot", "https://ex.com/only-for-others/x", true},
		// token match is case-insensitive and prefix-based (Googlebot-Images
		// matches a "googlebot" group)
		{"Bluesnake-Images", "https://ex.com/only-for-others/x", false},
		{"BluesnakeFoo", "https://ex.com/only-for-others/x", false},
	}
	for _, tt := range tests {
		v := f.Verdict(tt.ua, tt.url)
		if v.Allowed != tt.allowed {
			t.Errorf("Verdict(%q, %q).Allowed = %v, want %v", tt.ua, tt.url, v.Allowed, tt.allowed)
		}
	}
}

func TestLongestMatchAllowWinsTies(t *testing.T) {
	f := Parse([]byte(sample))
	tests := []struct {
		url     string
		allowed bool
	}{
		{"https://ex.com/private/public-bit", true},   // allow rule is longer
		{"https://ex.com/private/public-bit/y", true}, // still longer
		{"https://ex.com/private/other", false},       // only disallow matches
		{"https://ex.com/other", true},                // nothing matches
	}
	for _, tt := range tests {
		if v := f.Verdict("somebot", tt.url); v.Allowed != tt.allowed {
			t.Errorf("Verdict(somebot, %q).Allowed = %v, want %v", tt.url, v.Allowed, tt.allowed)
		}
	}
}

func TestWildcards(t *testing.T) {
	f := Parse([]byte(sample))
	tests := []struct {
		url     string
		allowed bool
	}{
		{"https://ex.com/doc.pdf", false},        // /*.pdf$
		{"https://ex.com/a/deep/doc.pdf", false}, // * spans path segments
		{"https://ex.com/doc.pdfx", true},        // $ anchors the end
		{"https://ex.com/doc.pdf?x=1", true},     // query breaks the end anchor
	}
	for _, tt := range tests {
		if v := f.Verdict("somebot", tt.url); v.Allowed != tt.allowed {
			t.Errorf("Verdict(somebot, %q).Allowed = %v, want %v", tt.url, v.Allowed, tt.allowed)
		}
	}
}

func TestVerdictCarriesMatchedRule(t *testing.T) {
	f := Parse([]byte(sample))
	v := f.Verdict("somebot", "https://ex.com/private/x")
	if v.Allowed {
		t.Fatal("must be blocked")
	}
	if v.Rule == nil {
		t.Fatal("blocked verdict must carry the matched rule")
	}
	if v.Rule.Line != 2 {
		t.Errorf("matched line = %d, want 2", v.Rule.Line)
	}
	if v.Rule.Raw != "Disallow: /private/" {
		t.Errorf("raw rule = %q", v.Rule.Raw)
	}
}

func TestSitemaps(t *testing.T) {
	f := Parse([]byte(sample))
	if len(f.Sitemaps) != 1 || f.Sitemaps[0] != "https://ex.com/sitemap.xml" {
		t.Errorf("sitemaps = %v", f.Sitemaps)
	}
}

func TestEmptyAndMissing(t *testing.T) {
	for _, data := range []string{"", "\n\n", "# only comments\n"} {
		f := Parse([]byte(data))
		if v := f.Verdict("somebot", "https://ex.com/anything"); !v.Allowed {
			t.Errorf("empty robots (%q) must allow everything", data)
		}
	}
}

func TestParsingEdgeCases(t *testing.T) {
	t.Run("rules before any group are ignored", func(t *testing.T) {
		f := Parse([]byte("Disallow: /x\nUser-agent: *\nDisallow: /y\n"))
		if v := f.Verdict("bot", "https://ex.com/x"); !v.Allowed {
			t.Error("/x must be allowed (rule had no group)")
		}
		if v := f.Verdict("bot", "https://ex.com/y"); v.Allowed {
			t.Error("/y must be blocked")
		}
	})

	t.Run("consecutive user-agent lines share rules", func(t *testing.T) {
		f := Parse([]byte("User-agent: a\nUser-agent: b\nDisallow: /x\n"))
		for _, ua := range []string{"a", "b"} {
			if v := f.Verdict(ua, "https://ex.com/x"); v.Allowed {
				t.Errorf("%s must be blocked", ua)
			}
		}
	})

	t.Run("multiple groups for the same agent merge", func(t *testing.T) {
		f := Parse([]byte("User-agent: a\nDisallow: /x\n\nUser-agent: a\nDisallow: /y\n"))
		for _, p := range []string{"/x", "/y"} {
			if v := f.Verdict("a", "https://ex.com"+p); v.Allowed {
				t.Errorf("%s must be blocked", p)
			}
		}
	})

	t.Run("comments and unknown directives", func(t *testing.T) {
		f := Parse([]byte("User-agent: * # everyone\nCrawl-delay: 10\nDisallow: /x # tail comment\n"))
		if v := f.Verdict("bot", "https://ex.com/x"); v.Allowed {
			t.Error("/x must be blocked despite comments and crawl-delay")
		}
	})

	t.Run("keys are case-insensitive, CRLF tolerated", func(t *testing.T) {
		f := Parse([]byte("USER-AGENT: *\r\nDISALLOW: /x\r\n"))
		if v := f.Verdict("bot", "https://ex.com/x"); v.Allowed {
			t.Error("/x must be blocked")
		}
	})

	t.Run("empty disallow allows everything", func(t *testing.T) {
		f := Parse([]byte("User-agent: *\nDisallow:\n"))
		if v := f.Verdict("bot", "https://ex.com/x"); !v.Allowed {
			t.Error("empty disallow must not block")
		}
	})
}

func TestQueryMatching(t *testing.T) {
	f := Parse([]byte("User-agent: *\nDisallow: /*?sessionid=\n"))
	if v := f.Verdict("bot", "https://ex.com/p?sessionid=1"); v.Allowed {
		t.Error("query rule must match")
	}
	if v := f.Verdict("bot", "https://ex.com/p"); !v.Allowed {
		t.Error("no query, must be allowed")
	}
}

func TestGoogleDocExamples(t *testing.T) {
	// from Google's robots.txt documentation examples
	tests := []struct {
		rules   string
		url     string
		allowed bool
	}{
		{"Allow: /p\nDisallow: /", "https://ex.com/page", true},
		{"Allow: /folder\nDisallow: /folder", "https://ex.com/folder/page", true}, // tie: allow wins
		{"Allow: /page\nDisallow: /*.htm", "https://ex.com/page.htm", false},      // longest match
		{"Allow: /$\nDisallow: /", "https://ex.com/", true},
		{"Allow: /$\nDisallow: /", "https://ex.com/page.htm", false},
		{"Disallow: /fish*", "https://ex.com/fishheads/yummy.html", false},
		{"Disallow: /fish/", "https://ex.com/fish", true},
		{"Disallow: /fish/", "https://ex.com/fish/salmon.htm", false},
	}
	for _, tt := range tests {
		f := Parse([]byte("User-agent: *\n" + tt.rules + "\n"))
		if v := f.Verdict("bot", tt.url); v.Allowed != tt.allowed {
			t.Errorf("rules %q url %q: allowed = %v, want %v",
				strings.ReplaceAll(tt.rules, "\n", "; "), tt.url, v.Allowed, tt.allowed)
		}
	}
}
