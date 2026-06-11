package store

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/fetch"
)

// TestArchiveConcurrent pins that Archive is safe to call from many crawl
// worker goroutines at once: the lazy init must happen exactly once and the
// resulting archive must be a readable gzip stream with one record per call
// (plus the single warcinfo). Run with -race to catch the data race.
func TestArchiveConcurrent(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}

	const n = 24
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			url := fmt.Sprintf("https://ex.com/p%d", i)
			err := c.Archive(url, &fetch.Result{
				URL: url, StatusCode: 200, Status: "OK", HTTPVersion: "HTTP/1.1",
				Headers: http.Header{"Content-Type": []string{"text/html"}},
				Body:    []byte(fmt.Sprintf("<html><body>page %d</body></html>", i)),
			})
			if err != nil {
				t.Errorf("Archive: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(c.ArchivePath())
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	all, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("archive not a readable gzip stream (corruption): %v", err)
	}
	// warcinfo + one response per goroutine, no more (single lazy init)
	if got := bytes.Count(all, []byte("WARC-Type: response")); got != n {
		t.Errorf("response records = %d, want %d", got, n)
	}
	if got := bytes.Count(all, []byte("WARC-Type: warcinfo")); got != 1 {
		t.Errorf("warcinfo records = %d, want exactly 1 (lazy init must run once)", got)
	}
}

// TestArchiveAppendsAcrossReopen pins that reopening a crawl (the resume path
// uses a fresh *Crawl over the same id) appends to the existing archive
// instead of truncating it.
func TestArchiveAppendsAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	if err := c.Archive("https://ex.com/first", &fetch.Result{
		URL: "https://ex.com/first", StatusCode: 200, HTTPVersion: "HTTP/1.1",
		Body: []byte("first"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	c2, err := OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := c2.Archive("https://ex.com/second", &fetch.Result{
		URL: "https://ex.com/second", StatusCode: 200, HTTPVersion: "HTTP/1.1",
		Body: []byte("second"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(c2.ArchivePath())
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	all, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	for _, want := range []string{"https://ex.com/first", "https://ex.com/second"} {
		if !bytes.Contains(all, []byte("WARC-Target-URI: "+want)) {
			t.Errorf("archive missing record for %s after reopen (truncated?)", want)
		}
	}
}
