// Package serpwidth measures the approximate rendered pixel width of text
// the way Google's desktop results page displays it (Arial), using a bundled
// font-metrics table — no font files or rendering engine involved. Titles
// render at 20px and descriptions at 13px; the thresholds.title/description
// min_px/max_px config compares against these measurements (DESIGN.md §9).
package serpwidth

import "math"

// Google desktop SERP font sizes (CSS pixels).
const (
	TitleFontPx       = 20.0
	DescriptionFontPx = 13.0
)

// asciiWidths holds advance widths in 1/1000 em for runes 32..126, from the
// Helvetica AFM tables (Arial is metric-compatible with Helvetica by design).
var asciiWidths = [95]int{
	278, 278, 355, 556, 556, 889, 667, 191, 333, 333, // space ! " # $ % & ' ( )
	389, 584, 278, 333, 278, 278, // * + , - . /
	556, 556, 556, 556, 556, 556, 556, 556, 556, 556, // 0-9
	278, 278, 584, 584, 584, 556, 1015, // : ; < = > ? @
	667, 667, 722, 722, 667, 611, 778, 722, 278, 500, // A-J
	667, 556, 833, 722, 778, 667, 778, 722, 667, 611, // K-T
	722, 667, 944, 667, 667, 611, // U-Z
	278, 278, 278, 469, 556, 333, // [ \ ] ^ _ `
	556, 556, 500, 556, 556, 278, 556, 556, 222, 222, // a-j
	500, 222, 833, 556, 556, 556, 556, 333, 500, 278, // k-t
	556, 500, 722, 500, 500, 500, // u-z
	334, 260, 334, 584, // { | } ~
}

const (
	defaultWidth = 556  // average lowercase advance, used for unmapped runes
	cjkWidth     = 1000 // CJK and other full-width scripts occupy a full em
)

// Width returns the approximate rendered width in pixels of s at the given
// font size. Widths accumulate in font units and round once at the end.
func Width(s string, fontSizePx float64) int {
	total := 0
	for _, r := range s {
		switch {
		case r >= 32 && r <= 126:
			total += asciiWidths[r-32]
		case r >= 0x2E80:
			total += cjkWidth
		default:
			total += defaultWidth
		}
	}
	return int(math.Round(float64(total) * fontSizePx / 1000))
}

// Title measures s at the SERP title font size.
func Title(s string) int { return Width(s, TitleFontPx) }

// Description measures s at the SERP description font size.
func Description(s string) int { return Width(s, DescriptionFontPx) }
