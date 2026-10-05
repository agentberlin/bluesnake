package parse

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

func withSrcset(c *config.Config) { c.Advanced.ExtractSrcset = true }

// A <picture>'s <source> elements name their images in srcset — src is not
// valid there — so with srcset extraction on, every candidate is an image link.
// It stays behind advanced.extract_srcset like <img srcset>, as Screaming Frog
// gates picture alternatives behind the same option: a picture's sources are
// alternates of its <img>, which is always read.
func TestPictureSourceSrcsetCandidates(t *testing.T) {
	const page = `<html><body><picture>
		<source type="image/avif" srcset="/cat.avif 1x, /cat@2x.avif 2x">
		<source type="image/webp" srcset="/cat.webp">
		<img src="/cat.jpg" alt="A cat">
	</picture></body></html>`
	if f := parseHTML(t, "https://ex.com/p", page, nil, nil); countLinks(f, Image) != 1 {
		t.Errorf("srcset off: image links = %+v, want only the <img>", f.Links)
	}
	f := parseHTML(t, "https://ex.com/p", page, nil, withSrcset)
	for _, u := range []string{"https://ex.com/cat.avif", "https://ex.com/cat@2x.avif", "https://ex.com/cat.webp", "https://ex.com/cat.jpg"} {
		if findLink(f, Image, u) == nil {
			t.Errorf("srcset on: no image link to %s in %+v", u, f.Links)
		}
	}
	if n := countLinks(f, Image); n != 4 {
		t.Errorf("srcset on: %d image links, want 4", n)
	}
}

// A <source> has no alt of its own: the picture's alternative text is its
// <img>'s, so each source link carries that alt and whether it was present —
// otherwise every picture would read as an image missing its alt.
func TestPictureSourcesTakeTheirImgAlt(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<picture><source srcset="/described.webp"><img src="/described.jpg" alt="A cat"></picture>
		<picture><source srcset="/decorative.webp"><img src="/decorative.jpg" alt=""></picture>
		<picture><source srcset="/bare.webp"><img src="/bare.jpg"></picture>
		<picture><source src="/legacy.webp"><img src="/legacy.jpg" alt="Legacy"></picture>
	</body></html>`, nil, withSrcset)
	for _, tt := range []struct {
		url       string
		alt       string
		noAltAttr bool
	}{
		{"https://ex.com/described.webp", "A cat", false},
		{"https://ex.com/decorative.webp", "", false},
		{"https://ex.com/bare.webp", "", true},
		{"https://ex.com/legacy.webp", "Legacy", false},
	} {
		l := findLink(f, Image, tt.url)
		if l == nil {
			t.Errorf("no image link to %s", tt.url)
			continue
		}
		if l.Alt != tt.alt || l.NoAltAttr != tt.noAltAttr {
			t.Errorf("%s: alt=%q noAltAttr=%v, want alt=%q noAltAttr=%v", tt.url, l.Alt, l.NoAltAttr, tt.alt, tt.noAltAttr)
		}
	}
}

// A <source> outside a <picture> is media, and its srcset is not read.
func TestMediaSourceIgnoresSrcset(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<video><source src="/clip.mp4" srcset="/not-an-image.webp"></video>`, nil, withSrcset)
	if findLink(f, Media, "https://ex.com/clip.mp4") == nil || countLinks(f, Image) != 0 {
		t.Errorf("links = %+v, want the media source only", f.Links)
	}
}

// Consent managers defer an embed by leaving src empty or about:blank and
// parking the real URL in data-src; the iframe link is that URL, as written.
// A real src always wins, and an iframe with no deferred source is unchanged.
func TestIframeDeferredSource(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<iframe src="" data-src="https://www.youtube.com/embed/abc"></iframe>
		<iframe src=" about:blank " data-src="/player/2"></iframe>
		<iframe data-src="/player/3"></iframe>
		<iframe src="/player/real" data-src="/player/deferred"></iframe>
	</body></html>`, nil, nil)
	for _, u := range []string{"https://www.youtube.com/embed/abc", "https://ex.com/player/2", "https://ex.com/player/3", "https://ex.com/player/real"} {
		if findLink(f, IFrame, u) == nil {
			t.Errorf("no iframe link to %s in %+v", u, f.Links)
		}
	}
	if l := findLink(f, IFrame, "https://www.youtube.com/embed/abc"); l != nil && l.Raw != "https://www.youtube.com/embed/abc" {
		t.Errorf("raw = %q, want data-src as written", l.Raw)
	}
	if n := countLinks(f, IFrame); n != 4 {
		t.Errorf("%d iframe links, want 4 (the deferred one never shadows a real src)", n)
	}
	// No data-src: about:blank stays what it was before deferred sources were read.
	f = parseHTML(t, "https://ex.com/p", `<iframe src="about:blank"></iframe>`, nil, nil)
	if len(f.Links) != 1 || f.Links[0].Type != IFrame || f.Links[0].Raw != "about:blank" {
		t.Errorf("links = %+v, want the about:blank iframe kept as written", f.Links)
	}
}

// An iframe's title names what it embeds (usually the video); it is carried on
// the iframe link, collapsed, and on no other link type.
func TestIframeTitle(t *testing.T) {
	f := parseHTML(t, "https://ex.com/p", `<html><body>
		<iframe src="/embed/1" title="  How to   crawl a site "></iframe>
		<iframe src="/embed/2"></iframe>
		<a href="/x" title="tooltip">x</a>
		<img src="/i.png" title="tooltip">
	</body></html>`, nil, nil)
	if l := findLink(f, IFrame, "https://ex.com/embed/1"); l == nil || l.Title != "How to crawl a site" {
		t.Errorf("titled iframe = %+v", l)
	}
	if l := findLink(f, IFrame, "https://ex.com/embed/2"); l == nil || l.Title != "" {
		t.Errorf("untitled iframe = %+v", l)
	}
	for _, l := range f.Links {
		if l.Type != IFrame && l.Title != "" {
			t.Errorf("%s link carries title %q", l.Type, l.Title)
		}
	}
}
