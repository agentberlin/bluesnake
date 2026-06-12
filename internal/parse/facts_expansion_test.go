package parse

import "testing"

// The 2026-06 catalogue tranche needs three new parse-level facts: the
// alt-attribute-present/empty distinction on image links (SF splits Missing
// Alt Text from Missing Alt Attribute), h1 text sourced from an image alt
// (SF's "Alt Text in h1" — SF shows the alt as the h1), and canonical link
// elements carrying attributes that are invalid in a canonical annotation.

func TestImageNoAltAttr(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<img src="/no-attr.png">
		<img src="/empty-alt.png" alt="">
		<img src="/with-alt.png" alt="described">
	</body></html>`, nil, nil)

	tests := []struct {
		url       string
		noAltAttr bool
		alt       string
	}{
		{"https://ex.com/no-attr.png", true, ""},
		{"https://ex.com/empty-alt.png", false, ""},
		{"https://ex.com/with-alt.png", false, "described"},
	}
	for _, tt := range tests {
		l := findLink(f, Image, tt.url)
		if l == nil {
			t.Errorf("no image link for %s", tt.url)
			continue
		}
		if l.NoAltAttr != tt.noAltAttr || l.Alt != tt.alt {
			t.Errorf("%s: NoAltAttr=%v Alt=%q, want NoAltAttr=%v Alt=%q",
				tt.url, l.NoAltAttr, l.Alt, tt.noAltAttr, tt.alt)
		}
	}
}

func TestH1AltTextFallback(t *testing.T) {
	// image-only h1: the alt text becomes the h1 (Screaming Frog behaviour)
	// and the page is marked as having an alt-text h1
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<h1><img src="/logo.png" alt="Company Logo"></h1>
	</body></html>`, nil, nil)
	if len(f.H1s) != 1 || f.H1s[0] != "Company Logo" {
		t.Errorf("H1s = %v, want the image alt text extracted as the h1", f.H1s)
	}
	if !f.H1AltText {
		t.Error("H1AltText not set for an image-only h1")
	}

	// real text wins: the image alt must not replace it
	f = parseHTML(t, "https://ex.com/p", `<html><body>
		<h1>Real heading <img src="/i.png" alt="decoration"></h1>
	</body></html>`, nil, nil)
	if len(f.H1s) != 1 || f.H1s[0] != "Real heading" {
		t.Errorf("H1s = %v, want the element text untouched", f.H1s)
	}
	if f.H1AltText {
		t.Error("H1AltText set although the h1 has its own text")
	}

	// empty alt on an image-only h1: stays a missing h1, not an alt-text h1
	f = parseHTML(t, "https://ex.com/p", `<html><body>
		<h1><img src="/i.png" alt=""></h1>
	</body></html>`, nil, nil)
	if len(f.H1s) != 1 || f.H1s[0] != "" {
		t.Errorf("H1s = %v, want one empty h1", f.H1s)
	}
	if f.H1AltText {
		t.Error("H1AltText set although the image alt is empty")
	}

	// the fallback is h1-only: an image-only h2 stays empty
	f = parseHTML(t, "https://ex.com/p", `<html><body>
		<h1>Fine</h1><h2><img src="/i.png" alt="not a heading"></h2>
	</body></html>`, nil, nil)
	if len(f.H2s) != 1 || f.H2s[0] != "" {
		t.Errorf("H2s = %v, want one empty h2 (no alt fallback)", f.H2s)
	}
	if f.H1AltText {
		t.Error("H1AltText set by an h2")
	}
}

func TestCanonicalInvalidAttrs(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><head>
		<link rel="canonical" href="/canon" hreflang="en">
	</head></html>`, nil, nil)
	if len(f.CanonicalInvalidAttrs) != 1 || f.CanonicalInvalidAttrs[0] != "hreflang" {
		t.Errorf("CanonicalInvalidAttrs = %v, want [hreflang]", f.CanonicalInvalidAttrs)
	}

	f = parseHTML(t, "https://ex.com/p", `<html><head>
		<link rel="canonical" href="/canon" media="screen" type="text/html" lang="en">
	</head></html>`, nil, nil)
	if len(f.CanonicalInvalidAttrs) != 3 {
		t.Errorf("CanonicalInvalidAttrs = %v, want hreflang/lang/media/type carriers collected", f.CanonicalInvalidAttrs)
	}

	f = parseHTML(t, "https://ex.com/p", `<html><head>
		<link rel="canonical" href="/canon">
		<link rel="alternate" hreflang="de" href="/de">
		<link rel="stylesheet" type="text/css" href="/s.css">
	</head></html>`, nil, nil)
	if len(f.CanonicalInvalidAttrs) != 0 {
		t.Errorf("CanonicalInvalidAttrs = %v, want none for a clean canonical (other rels exempt)", f.CanonicalInvalidAttrs)
	}
}
