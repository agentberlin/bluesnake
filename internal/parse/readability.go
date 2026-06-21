package parse

import (
	"math"
	"strings"
	"unicode/utf8"
)

// maxSentenceChars is the character budget Screaming Frog packs a single
// sentence into when segmenting a block for readability. Measured against
// controlled probe pages (a 40-word, 79-char line stays one sentence; a 41-word,
// 81-char line becomes two), it is an absolute grid over the block's
// single-space text — not a per-word or per-clause limit.
const maxSentenceChars = 80

// contentStats are the readability metrics derived from extracted page text.
type contentStats struct {
	words               int
	sentences           int
	avgWordsPerSentence float64
	flesch              float64
}

// computeStats derives word, sentence and Flesch metrics from content already
// segmented into blocks (logical lines: paragraphs, table rows, list items,
// headings). Each block is whitespace-collapsed visible text. Words are
// whitespace-separated tokens; sentences are counted per block (see
// blockSentences) and summed; Flesch uses the total words and sentences.
func computeStats(blocks []string) contentStats {
	var all []string
	sentences := 0
	for _, b := range blocks {
		words := strings.Fields(b)
		if len(words) == 0 {
			continue
		}
		all = append(all, words...)
		sentences += blockSentences(words)
	}
	if len(all) == 0 {
		return contentStats{}
	}
	if sentences < 1 {
		sentences = 1
	}
	return contentStats{
		words:               len(all),
		sentences:           sentences,
		avgWordsPerSentence: float64(len(all)) / float64(sentences),
		flesch:              fleschScore(all, sentences),
	}
}

// blockSentences counts the sentences in one block (one logical line), matching
// Screaming Frog. SF greedy-packs the block's words into runs of at most
// maxSentenceChars characters over the single-space text, then additionally
// splits at every terminator (. ! ?) — so "One fish. Two fish! ..." is five
// sentences even though it is one short line, and "cost 3.14 dollars" is two
// ("3.14" splits mid-word), while a long unpunctuated paragraph is split purely
// by the character budget. The character grid is absolute: a terminator adds a
// split without resetting it (verified against pages with a period mid-
// paragraph). Terminator splits and grid breaks are deduplicated where they
// coincide (a word-final terminator at a run boundary is the run boundary). A
// block that contains any word is at least one sentence.
func blockSentences(words []string) int {
	if len(words) == 0 {
		return 0
	}
	runStart, lineLen, count := 0, 0, 0
	// closeRun finalises the completed run words[runStart..end] (inclusive):
	// one sentence for the run plus one for each word-final terminator before
	// the run's last word (the line reads on past it; the run's last word is the
	// run boundary itself, already counted).
	closeRun := func(end int) {
		count++
		for i := runStart; i < end; i++ {
			if endsWithTerminator(words[i]) {
				count++
			}
		}
	}
	for i, w := range words {
		// Terminators inside a word (e.g. the "." in "3.14") always split — they
		// never sit at a between-word grid boundary, so no dedup is needed.
		count += midWordTerminators(w)
		wlen := utf8.RuneCountInString(w)
		switch {
		case i == runStart:
			lineLen = wlen
		case lineLen+1+wlen > maxSentenceChars:
			closeRun(i - 1)
			runStart, lineLen = i, wlen
		default:
			lineLen += 1 + wlen
		}
	}
	closeRun(len(words) - 1)
	return count
}

func endsWithTerminator(w string) bool {
	r, _ := utf8.DecodeLastRuneInString(w)
	return r == '.' || r == '!' || r == '?'
}

// midWordTerminators counts terminator runs (. ! ?) inside a word that are
// followed by more characters in the same word — each one starts a new sentence
// the way splitting on [.!?]+ would ("3.14" -> "3" | "14"). A terminator run at
// the end of the word is word-final, not mid-word, and is handled by the caller
// against the character grid.
func midWordTerminators(w string) int {
	rs := []rune(w)
	n := 0
	for i := 0; i < len(rs); i++ {
		if !isTerminator(rs[i]) {
			continue
		}
		j := i
		for j < len(rs) && isTerminator(rs[j]) {
			j++
		}
		if j < len(rs) { // characters remain after the terminator run
			n++
		}
		i = j - 1
	}
	return n
}

func isTerminator(r rune) bool { return r == '.' || r == '!' || r == '?' }

// fleschScore computes the Flesch Reading Ease score with a vowel-group
// syllable approximation, clamped to [0, 100] (Screaming Frog parity).
func fleschScore(words []string, sentences int) float64 {
	syllables := 0
	for _, w := range words {
		syllables += syllableEstimate(w)
	}
	wordCount := float64(len(words))
	score := 206.835 - 1.015*(wordCount/float64(sentences)) - 84.6*(float64(syllables)/wordCount)
	return math.Min(100, math.Max(0, score))
}

// syllableEstimate counts vowel groups (y included) with two corrections
// reverse-engineered from Screaming Frog v24.1 (the readability score is defined
// as SF parity, so its syllable heuristic is the reference). Measured by crawling
// controlled probe pages — one test word amplified and diluted by one-syllable
// fillers, then inverting SF's reported Flesch — over ~170 words spanning every
// systematic class; the two rules below took agreement from 58% to 100%.
//
//   - "ia" is a hiatus: SF counts the i and the a as separate syllables
//     (so·ci·al, me·di·a, mar·tian, vari·a·tion). The vowel-group pass merges the
//     pair into one, so add one back per "ia". No other vowel pair splits (io, ie,
//     iou, ea, eo, ua, oa, ai, oe all stay one syllable). The sole exception is a
//     word-final "-tial" (par·tial, es·sen·tial), which SF leaves merged; it is
//     end-anchored, so a trailing period re-enables the split ("partial." => 3).
//   - A trailing "-es" is NOT silent: SF counts box·es, hous·es, servic·es,
//     machin·es. Only a lone "-e" or "-ed" is dropped (cake, baked), and "-le/-les"
//     keep their sounded e (table, little). "-es" plurals and 3rd-person verbs are
//     pervasive in real text, so dropping that subtraction is the dominant gain.
//
// Tokens keep whatever punctuation they were written with, and vowel-less tokens
// ("$49") count zero syllables.
func syllableEstimate(word string) int {
	w := strings.ToLower(word)
	count := 0
	prevVowel := false
	for _, r := range w {
		isVowel := strings.ContainsRune("aeiouy", r)
		if isVowel && !prevVowel {
			count++
		}
		prevVowel = isVowel
	}
	count += strings.Count(w, "ia")
	if strings.HasSuffix(w, "tial") {
		count--
	}
	if count > 1 {
		switch {
		case strings.HasSuffix(w, "le") || strings.HasSuffix(w, "les"):
			// sounded e
		case strings.HasSuffix(w, "e") || strings.HasSuffix(w, "ed"):
			count--
		}
	}
	return count
}
