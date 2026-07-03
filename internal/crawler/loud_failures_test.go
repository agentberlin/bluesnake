package crawler

// #74 R6 / D4 ("errors are loud"): a store/dedup error mid-crawl must fail the
// run — never read as "duplicate, drop silently". Silent incompleteness is the
// wrong default for an audit product: the crawl would report success while
// missing pages. This is the doc-named guard (MEMORY-SCALING.md §13's
// TestWorkerPool_StoreErrorMidCrawl_NoSilentLoss) that was never written.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/frontier"
)

var errDedupBoom = errors.New("dedup authority failed")

// errDedupSink is a capturing sink that is also the crawl's dedup authority
// (like the production store sink), whose Admit fails for URLs containing
// "fail" — an injected mid-crawl store error.
type errDedupSink struct {
	*capSink
	mem map[string]bool
}

func newErrDedupSink() *errDedupSink {
	return &errDedupSink{capSink: newCapSink(), mem: map[string]bool{}}
}

func (s *errDedupSink) Admit(it frontier.Item) (bool, error) {
	if strings.Contains(it.URL, "fail") {
		return false, errDedupBoom
	}
	if s.mem[it.URL] {
		return false, nil
	}
	s.mem[it.URL] = true
	return true, nil
}
func (s *errDedupSink) Remove(url string) error { delete(s.mem, url); return nil }
func (s *errDedupSink) Seen(url string) (bool, error) {
	return s.mem[url], nil
}
func (s *errDedupSink) MarkSeen(urls []string) error {
	for _, u := range urls {
		s.mem[u] = true
	}
	return nil
}
func (s *errDedupSink) Count() (int, error) { return len(s.mem), nil }

// TestWorkerPool_StoreErrorMidCrawl_NoSilentLoss: an Admit error means the URL
// was neither crawled nor recorded — the run must return the error so the
// caller knows the result is incomplete, and the rest of the crawl still
// drains (the error declines one URL, it does not wedge the pool).
func TestWorkerPool_StoreErrorMidCrawl_NoSilentLoss(t *testing.T) {
	s := newSite(t, map[string]string{
		"/":     `<a href="/ok">ok</a> <a href="/fail-page">f</a>`,
		"/ok":   `<p>fine</p>`,
		"/fail": `<p>never admitted</p>`,
	})
	sink := newErrDedupSink()
	c, err := New(config.Default(), WithSink(sink))
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.Run(context.Background(), s.server.URL+"/")
	if err == nil {
		t.Error("Run returned nil after a dedup-authority error — the crawl silently dropped a URL and reported success")
	} else if !errors.Is(err, errDedupBoom) {
		t.Errorf("Run error = %v, want the injected dedup error", err)
	}
	// The healthy part of the crawl still drained.
	if res == nil {
		t.Fatal("Run returned no result")
	}
	pages := sink.snapshot()
	if pages[s.server.URL+"/"] == nil || pages[s.server.URL+"/ok"] == nil {
		t.Errorf("healthy pages not crawled after a single dedup error: %v", len(pages))
	}
}
