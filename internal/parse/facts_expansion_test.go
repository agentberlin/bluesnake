package parse

import (
	"slices"
	"testing"
)

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
	for _, tt := range []struct {
		name, body string
		want       []Heading
	}{
		// image-only h1: the alt text becomes the h1 (Screaming Frog
		// behaviour), and that heading is marked as taken from an alt
		{"image-only h1", `<h1><img src="/logo.png" alt="Company Logo"></h1>`,
			[]Heading{{Level: 1, Text: "Company Logo", FromAlt: true}}},
		// real text wins: the image alt must not replace it
		{"h1 with text", `<h1>Real heading <img src="/i.png" alt="decoration"></h1>`,
			[]Heading{{Level: 1, Text: "Real heading"}}},
		// empty alt on an image-only h1: stays a missing h1, not an alt-text h1
		{"empty alt", `<h1><img src="/i.png" alt=""></h1>`,
			[]Heading{{Level: 1}}},
		// the fallback is h1-only: an image-only h2 stays empty
		{"image-only h2", `<h1>Fine</h1><h2><img src="/i.png" alt="not a heading"></h2>`,
			[]Heading{{Level: 1, Text: "Fine"}, {Level: 2}}},
		// the mark is on the heading that fell back, not on the page's first h1
		{"second h1", `<h1>Fine</h1><h1><img src="/logo.png" alt="Logo"></h1>`,
			[]Heading{{Level: 1, Text: "Fine"}, {Level: 1, Text: "Logo", FromAlt: true}}},
	} {
		f := parseHTML(t, "https://ex.com/p", `<html><body>`+tt.body+`</body></html>`, nil, nil)
		if !slices.Equal(f.Headings, tt.want) {
			t.Errorf("%s: headings = %+v, want %+v", tt.name, f.Headings, tt.want)
		}
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
