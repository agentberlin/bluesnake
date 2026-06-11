package serpwidth

import "testing"

func TestWidthKnownGlyphs(t *testing.T) {
	cases := []struct {
		s    string
		font float64
		want int
	}{
		{"", TitleFontPx, 0},
		{"a", 20, 11},  // 556 units * 20px / 1000
		{"W", 20, 19},  // 944 * 0.02 = 18.88
		{"l", 20, 4},   // 222 * 0.02 = 4.44
		{" ", 20, 6},   // 278 * 0.02 = 5.56
		{"a", 13, 7},   // 556 * 0.013 = 7.23
		{"ab", 20, 22}, // widths accumulate before rounding
		{"@", 20, 20},  // 1015 * 0.02 = 20.3
	}
	for _, c := range cases {
		if got := Width(c.s, c.font); got != c.want {
			t.Errorf("Width(%q, %v) = %d, want %d", c.s, c.font, got, c.want)
		}
	}
}

func TestWidthRelativeOrdering(t *testing.T) {
	if Width("WWW", 20) <= Width("lll", 20) {
		t.Error("wide glyphs must measure wider than narrow glyphs")
	}
	if Width("hello world", 20) <= Width("hello", 20) {
		t.Error("longer strings must measure wider")
	}
	if Width("hello", 20) <= Width("hello", 13) {
		t.Error("larger font sizes must measure wider")
	}
}

func TestWidthFallbacks(t *testing.T) {
	// unknown Latin-ish rune falls back to the average glyph width
	if got, want := Width("é", 20), Width("a", 20); got != want {
		t.Errorf("unknown rune width = %d, want average %d", got, want)
	}
	// CJK glyphs are full-width (1000 units = the font size)
	if got := Width("中", 20); got != 20 {
		t.Errorf("CJK rune width = %d, want 20", got)
	}
	if got := Width("中文", 13); got != 26 {
		t.Errorf("CJK string width = %d, want 26", got)
	}
}

func TestTitleAndDescription(t *testing.T) {
	// the convenience wrappers fix the Google desktop SERP font sizes
	if got, want := Title("acrawler"), Width("acrawler", TitleFontPx); got != want {
		t.Errorf("Title = %d, want %d", got, want)
	}
	if got, want := Description("acrawler"), Width("acrawler", DescriptionFontPx); got != want {
		t.Errorf("Description = %d, want %d", got, want)
	}
	if Title("acrawler") <= Description("acrawler") {
		t.Error("title font is larger than description font")
	}
}

func TestDefaultThresholdSanity(t *testing.T) {
	// the shipped defaults (title max 561px / desc max 985px) should roughly
	// agree with the character defaults (60 / 155 chars) for average text
	title := "The quick brown fox jumps over the lazy dog once mor" // 53 chars
	if px := Title(title); px < 200 || px > 561 {
		t.Errorf("typical title measures %dpx, expected within 200..561", px)
	}
	desc := "The quick brown fox jumps over the lazy dog while the lazy dog " +
		"watches the quick brown fox jump over it again and again all day" // 129 chars
	if px := Description(desc); px < 400 || px > 985 {
		t.Errorf("typical description measures %dpx, expected within 400..985", px)
	}
}
