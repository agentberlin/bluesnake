package analyze

import (
	"fmt"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/nearcopy"
)

// pageSource is a nearcopy.Source over a page map holding each page's text —
// what store.NearCopyPages streams from the database.
func pageSource(pages map[string]*crawler.PageRecord) nearcopy.Source {
	return func(fn func(*nearcopy.Page) error) error {
		for url, rec := range pages {
			if rec.Facts == nil {
				continue
			}
			if err := fn(&nearcopy.Page{
				URL: url, Internal: rec.Scope == "internal", Crawled: rec.State == crawler.StateCrawled,
				Indexable: rec.Indexable, Paginated: rec.Facts.IsPaginated(), WordCount: rec.Facts.WordCount,
				Hash: rec.Facts.Hash, Signature: rec.Minhash, Text: rec.Facts.ContentText,
			}); err != nil {
				return err
			}
		}
		return nil
	}
}

// nearCopiesOf runs the near-copy step over a page map under cfg's settings,
// as finalize runs it over the store; nil when cfg does not ask for it.
func nearCopiesOf(t *testing.T, pages map[string]*crawler.PageRecord, cfg *config.Config) *nearcopy.Result {
	t.Helper()
	if !NearCopiesOn(cfg) {
		return nil
	}
	res, err := nearcopy.Run(pageSource(pages), NearCopySettings(cfg), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func nearCopyText(prefix string, n int) []string {
	var w []string
	for i := range n {
		w = append(w, fmt.Sprintf("%s%dx", prefix, i))
	}
	return w
}

func textPage(url, text, hash string) *crawler.PageRecord {
	p := page(url)
	p.Facts.ContentText = text
	p.Facts.WordCount = len(strings.Fields(text))
	p.Facts.Hash = hash
	return p
}

func TestNearDuplicates(t *testing.T) {
	// non-repetitive text: 200 distinct words, one word changed in `similar`
	w := nearCopyText("word", 200)
	long := strings.Join(w, " ")
	w[100] = "changed"
	similar := strings.Join(w, " ")
	different := strings.Join(nearCopyText("other", 200), " ")

	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	pages := toMap(
		textPage("https://ex.com/a", long, "h1"),
		textPage("https://ex.com/b", similar, "h2"),
		textPage("https://ex.com/c", different, "h3"),
	)
	r := Run(pages, nil, nil, nil, cfg, WithNearCopies(nearCopiesOf(t, pages, cfg)))
	if r.NearCopies == nil {
		t.Fatal("Results carry no near copies")
	}
	got := map[string]nearcopy.PageResult{}
	for _, p := range r.NearCopies.Pages {
		got[p.URL] = p
	}
	a := got["https://ex.com/a"]
	if a.Count != 1 || a.Copies[0].URL != "https://ex.com/b" || a.Copies[0].Share < 0.9 {
		t.Fatalf("near copies of /a = %+v", a)
	}
	if c := got["https://ex.com/c"]; c.Count != 0 {
		t.Error("different page has near copies")
	}
	if !hasOcc(r, "https://ex.com/a", "content_near_duplicate") || !hasOcc(r, "https://ex.com/b", "content_near_duplicate") {
		t.Error("missing content_near_duplicate occurrence")
	}
	if hasOcc(r, "https://ex.com/c", "content_near_duplicate") {
		t.Error("different page has content_near_duplicate")
	}
	// The detail names the closest copy and its exact share.
	for _, o := range r.Occurrences {
		if o.URL == "https://ex.com/a" && o.IssueID == "content_near_duplicate" &&
			o.Detail != fmt.Sprintf("%.0f%% similar to https://ex.com/b", a.Copies[0].Share*100) {
			t.Errorf("detail = %q", o.Detail)
		}
	}
}

// TestNearDuplicateIssueLeavesOutByteIdenticalTwins: byte-identical pages are
// listed as near copies at 100%, but the issue — Screaming Frog's Near
// Duplicates — leaves them to the exact-duplicate check. A page with the same
// text and other HTML is still a near duplicate.
func TestNearDuplicateIssueLeavesOutByteIdenticalTwins(t *testing.T) {
	text := strings.Join(nearCopyText("word", 200), " ")
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true
	twins := toMap(textPage("https://ex.com/a", text, "same"), textPage("https://ex.com/b", text, "same"))
	r := Run(twins, nil, nil, nil, cfg, WithNearCopies(nearCopiesOf(t, twins, cfg)))
	for _, p := range r.NearCopies.Pages {
		if p.Count != 1 || p.Copies[0].Share != 1 {
			t.Errorf("twin %s = %+v, want its twin at 100%%", p.URL, p)
		}
	}
	if hasOcc(r, "https://ex.com/a", "content_near_duplicate") || hasOcc(r, "https://ex.com/b", "content_near_duplicate") {
		t.Error("byte-identical twins raised content_near_duplicate")
	}
	retext := toMap(textPage("https://ex.com/a", text, "h1"), textPage("https://ex.com/b", text, "h2"))
	r = Run(retext, nil, nil, nil, cfg, WithNearCopies(nearCopiesOf(t, retext, cfg)))
	if !hasOcc(r, "https://ex.com/a", "content_near_duplicate") {
		t.Error("same text under other HTML did not raise content_near_duplicate")
	}
}

// TestIgnorePaginatedForNearDuplicates pins the near-duplicate half of
// advanced.ignore_paginated_for_duplicates: a URL declaring rel="prev" is
// dropped from near-duplicate detection entirely (neither flagged nor a match
// target), while page 1 (rel="next" only) keeps participating.
func TestIgnorePaginatedForNearDuplicates(t *testing.T) {
	w := nearCopyText("word", 200)
	base := strings.Join(w, " ")
	w[100] = "changed"
	near1 := strings.Join(w, " ")
	w[100] = "word100x"
	w[50] = "altered"
	near2 := strings.Join(w, " ")

	mk := func(url, text, hash string, next, prev []string) *crawler.PageRecord {
		p := textPage(url, text, hash)
		p.Facts.NextHTML = next
		p.Facts.PrevHTML = prev
		return p
	}
	build := func() map[string]*crawler.PageRecord {
		return toMap(
			mk("https://ex.com/a", base, "h1", []string{"https://ex.com/b"}, nil),
			mk("https://ex.com/b", near1, "h2", nil, []string{"https://ex.com/a"}), // paginated
			mk("https://ex.com/plain", near2, "h3", nil, nil),
		)
	}
	listed := func(r *nearcopy.Result) map[string]int {
		m := map[string]int{}
		for _, p := range r.Pages {
			m[p.URL] = p.Count
		}
		return m
	}
	cfg := config.Default()
	cfg.Content.NearDuplicates.Enabled = true

	// Flag off: the paginated page participates in near-dup detection.
	pages := build()
	off := listed(nearCopiesOf(t, pages, cfg))
	if off["https://ex.com/b"] == 0 {
		t.Fatal("flag off: paginated /b should be a near-duplicate candidate")
	}

	// Flag on: /b drops out; /a still matches the non-paginated /plain.
	cfg.Advanced.IgnorePaginatedForDuplicates = true
	pages = build()
	res := nearCopiesOf(t, pages, cfg)
	on := listed(res)
	if _, ok := on["https://ex.com/b"]; ok {
		t.Error("flag on: paginated /b must be excluded from near-duplicate detection")
	}
	if on["https://ex.com/a"] != 1 {
		t.Error("flag on: non-paginated /a must still be detected as a near-duplicate of /plain")
	}
	r := Run(pages, nil, nil, nil, cfg, WithNearCopies(res))
	if hasOcc(r, "https://ex.com/b", "content_near_duplicate") {
		t.Error("flag on: paginated /b must not get a content_near_duplicate occurrence")
	}
}

// TestNearCopySettingsFromConfig: the step reads its four settings from the
// config, and runs whenever the content check and its analysis toggle are on —
// analysis.auto has no say.
func TestNearCopySettingsFromConfig(t *testing.T) {
	cfg := config.Default()
	if NearCopiesOn(cfg) {
		t.Error("near copies on by default")
	}
	cfg.Content.NearDuplicates.Enabled = true
	cfg.Analysis.Auto = false
	if !NearCopiesOn(cfg) {
		t.Error("near copies off with the check on and analysis.auto off")
	}
	cfg.Analysis.NearDuplicates = false
	if NearCopiesOn(cfg) {
		t.Error("near copies on with analysis.near_duplicates off")
	}
	cfg.Content.NearDuplicates.Threshold = 80
	cfg.Content.NearDuplicates.MinWords = 50
	cfg.Content.NearDuplicates.IndexableOnly = true
	cfg.Advanced.IgnorePaginatedForDuplicates = true
	if s := NearCopySettings(cfg); s != (nearcopy.Settings{Threshold: 80, MinWords: 50, IndexableOnly: true, IgnorePaginated: true}) {
		t.Errorf("settings = %+v", s)
	}
}
