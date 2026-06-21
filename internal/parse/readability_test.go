package parse

import (
	"math"
	"testing"
)

// TestBlockSentences pins the per-block sentence model directly (no HTML):
// greedy 80-char packing plus terminator splits, with grid/terminator dedup.
func TestBlockSentences(t *testing.T) {
	words := func(n int, w string) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = w
		}
		return out
	}
	cases := []struct {
		name  string
		words []string
		want  int
	}{
		{"empty", nil, 0},
		{"two words one sentence", []string{"hello", "world"}, 1},
		{"40 one-char words fit one line", words(40, "x"), 1}, // 79 chars
		{"41 one-char words overflow", words(41, "x"), 2},     // 81 chars
		{"40 five-char words pack to four", words(40, "lorem"), 4},
		{"17 four-char words pack to two", words(17, "abcd"), 2},
		{"word-final terminators split", []string{"One", "fish.", "Two", "fish!", "and", "more"}, 3},
		{"trailing terminator does not add", []string{"hello", "world."}, 1},
		{"abbreviations split", []string{"Dr.", "Smith", "went", "home.", "done"}, 3},
		{"decimal splits mid-word", []string{"cost", "3.14", "dollars"}, 2},
		{"standalone ellipsis splits", []string{"alpha", "...", "beta"}, 2},
		// 16 "abcd" words fill an 80-char run; a word-final terminator at that
		// run's last word is the run boundary itself, so it is not counted twice.
		// (SF reports one more here from an off-by-one exactly at the char limit;
		// we deliberately do not reproduce that boundary artifact — one word
		// earlier, a mid-run terminator, both agree. See the parity test.)
		{"terminator on the run boundary is the boundary",
			append(append(words(15, "abcd"), "abcd."), words(16, "abcd")...), 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := blockSentences(tc.words); got != tc.want {
				t.Errorf("blockSentences(%v) = %d, want %d", tc.words, got, tc.want)
			}
		})
	}
}

func TestMidWordTerminators(t *testing.T) {
	cases := []struct {
		word string
		want int
	}{
		{"3.14", 1},
		{"lorem.", 0}, // word-final, not mid-word
		{"v1.2.3", 2},
		{"...", 0},  // nothing follows the run
		{"U.S.", 1}, // "." after U (S. follows); trailing "." is final
		{"hello", 0},
		{"3.14.15", 2},
		{"", 0},
	}
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			if got := midWordTerminators(tc.word); got != tc.want {
				t.Errorf("midWordTerminators(%q) = %d, want %d", tc.word, got, tc.want)
			}
		})
	}
}

func TestComputeStats(t *testing.T) {
	cases := []struct {
		name      string
		blocks    []string
		words     int
		sentences int
	}{
		{"empty", nil, 0, 0},
		{"blank blocks", []string{"", "   "}, 0, 0},
		{"single line", []string{"alpha beta gamma delta epsilon"}, 5, 1},
		{"blocks are independent sentences", []string{"Hello world", "Goodbye now"}, 4, 2},
		{"sum of within-block splits", []string{"One fish. Two fish! more", "next line"}, 7, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := computeStats(tc.blocks)
			if s.words != tc.words || s.sentences != tc.sentences {
				t.Errorf("computeStats = {words:%d sentences:%d}, want {words:%d sentences:%d}",
					s.words, s.sentences, tc.words, tc.sentences)
			}
			if tc.words > 0 {
				wantAWPS := float64(tc.words) / float64(tc.sentences)
				if math.Abs(s.avgWordsPerSentence-wantAWPS) > 1e-9 {
					t.Errorf("avgWordsPerSentence = %v, want %v", s.avgWordsPerSentence, wantAWPS)
				}
			}
		})
	}
}

// TestSyllableEstimate pins the vowel-group syllable heuristic against
// Screaming Frog v24.1. Every value below is a real SF measurement, recovered
// by crawling controlled probe pages (one test word amplified 5x and diluted
// by 30 one-syllable "cat" fillers, so SF's reported Word/Sentence/Flesch invert
// cleanly to a per-word syllable count without the score clamping). The rules
// SF turned out to use — and that the old naive heuristic missed — are:
//
//   - "-es" is NEVER subtracted as a silent ending: SF counts box·es, hous·es,
//     servic·es, machin·es (a plural/3rd-person "-es" stays a vowel group). Only
//     a single trailing "-e" or "-ed" is silent (cake, baked), and "-le/-les"
//     keep their sounded e (table, little). This is the dominant gap — "-es"
//     words are everywhere in real text — and the old code under-counted them all.
//   - The vowel pair "ia" splits into two syllables (so·ci·al, me·di·a, mar·tian,
//     vari·a·tion) — the vowel-group pass merges it into one, so add one back per
//     occurrence. The sole exception is the word-final "-tial" (par·tial,
//     es·sen·tial), which SF leaves merged; being end-anchored, a trailing period
//     re-enables the split ("partial." => 3). No other vowel pair (io, ie, iou,
//     ea, eo, ua, oa, ai, oe) ever splits.
func TestSyllableEstimate(t *testing.T) {
	cases := []struct {
		word string
		want int
	}{
		// existing pins (unchanged by the new rules)
		{"cake", 1}, {"table", 2}, {"machine", 2}, {"happy", 2},
		{"communication", 5}, {"strength", 1}, {"baked", 1}, {"little", 2},
		{"the", 1}, {"$49", 0}, {"a", 1}, {"les", 1},
		// -es (never subtract)
		{"names", 2}, {"machines", 3}, {"boxes", 2}, {"buses", 2}, {"dishes", 2}, {"classes", 2},
		{"services", 3}, {"devices", 3}, {"images", 3}, {"sentences", 3}, {"pages", 2},
		{"faces", 2}, {"uses", 2}, {"values", 2}, {"features", 3}, {"cases", 2}, {"sizes", 2},
		{"places", 2},
		// ia split
		{"media", 3}, {"social", 3}, {"special", 3}, {"material", 4}, {"trivia", 3},
		{"india", 3}, {"piano", 3}, {"diary", 3}, {"giant", 2}, {"asian", 3}, {"russian", 3},
		{"brilliant", 3}, {"familiar", 4}, {"australia", 4}, {"financial", 4}, {"crucial", 3},
		{"racial", 3}, {"guardian", 3}, {"comedian", 4}, {"radiant", 3}, {"variation", 4},
		{"association", 5}, {"appreciate", 4}, {"initiate", 4}, {"martian", 3}, {"maniac", 3},
		{"croatian", 3},
		// tial exception (no split) — end-anchored, so a trailing period re-splits
		{"partial", 2}, {"initial", 3}, {"essential", 3}, {"potential", 3}, {"presidential", 4},
		{"partial.", 3}, {"boxes.", 2},
		// non-splitting vowel pairs (regression guards — already matched SF)
		{"radio", 2}, {"audio", 2}, {"video", 2}, {"various", 2}, {"serious", 2}, {"obvious", 2},
		{"society", 3}, {"variety", 3}, {"area", 2}, {"idea", 2}, {"opinion", 3}, {"million", 2},
		{"nation", 2}, {"question", 2}, {"museum", 2}, {"mosaic", 2}, {"theory", 2},
		{"people", 2}, {"actual", 2}, {"usual", 2},
	}
	for _, tc := range cases {
		t.Run(tc.word, func(t *testing.T) {
			if got := syllableEstimate(tc.word); got != tc.want {
				t.Errorf("syllableEstimate(%q) = %d, want %d", tc.word, got, tc.want)
			}
		})
	}
}
