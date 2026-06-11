package store

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
)

// warcRecord is one parsed record: header map plus the raw block.
type warcRecord struct {
	headers map[string]string
	block   []byte
}

// parseWARC walks the decompressed archive record by record (version line,
// headers, blank line, Content-Length block, CRLFCRLF terminator).
func parseWARC(t *testing.T, data []byte) []warcRecord {
	t.Helper()
	var recs []warcRecord
	for len(data) > 0 {
		idx := bytes.Index(data, []byte("\r\n\r\n"))
		if idx < 0 {
			t.Fatalf("no header terminator in record data: %q", data)
		}
		lines := strings.Split(string(data[:idx]), "\r\n")
		if lines[0] != "WARC/1.1" {
			t.Fatalf("record version line = %q, want WARC/1.1", lines[0])
		}
		rec := warcRecord{headers: map[string]string{}}
		for _, line := range lines[1:] {
			name, value, ok := strings.Cut(line, ":")
			if !ok {
				t.Fatalf("malformed WARC header line: %q", line)
			}
			rec.headers[name] = strings.TrimSpace(value)
		}
		n, err := strconv.Atoi(rec.headers["Content-Length"])
		if err != nil {
			t.Fatalf("Content-Length %q: %v", rec.headers["Content-Length"], err)
		}
		start := idx + 4
		if len(data) < start+n+4 || string(data[start+n:start+n+4]) != "\r\n\r\n" {
			t.Fatalf("block of %d bytes not terminated by CRLFCRLF", n)
		}
		rec.block = data[start : start+n]
		recs = append(recs, rec)
		data = data[start+n+4:]
	}
	return recs
}

func TestArchiveWARC(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got := c.ArchivePath(); got != "" {
		t.Errorf("ArchivePath before any Archive call = %q, want empty", got)
	}

	if err := c.Archive("https://ex.com/", &fetch.Result{
		URL:         "https://ex.com/",
		StatusCode:  200,
		Status:      "OK",
		HTTPVersion: "HTTP/1.1",
		ContentType: "text/html",
		Headers:     http.Header{"Content-Type": []string{"text/html"}},
		Body:        []byte("<html><body>home</body></html>"),
	}); err != nil {
		t.Fatalf("Archive 200: %v", err)
	}
	if err := c.Archive("https://ex.com/missing", &fetch.Result{
		URL:         "https://ex.com/missing",
		StatusCode:  404,
		Status:      "Not Found",
		HTTPVersion: "HTTP/1.1",
	}); err != nil {
		t.Fatalf("Archive 404: %v", err)
	}

	path := c.ArchivePath()
	want := filepath.Join(dir, "crawls", c.ID+".assets", "archive.warc.gz")
	if path != want {
		t.Fatalf("ArchivePath = %q, want %q", path, want)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("archive file: %v", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	all, err := io.ReadAll(gz) // multistream: all members in sequence
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	recs := parseWARC(t, all)
	if len(recs) != 3 {
		t.Fatalf("archive has %d records, want 3 (warcinfo + 2 responses)", len(recs))
	}
	if got := recs[0].headers["WARC-Type"]; got != "warcinfo" {
		t.Errorf("first record WARC-Type = %q, want warcinfo", got)
	}
	if got := recs[0].headers["Content-Type"]; got != "application/warc-fields" {
		t.Errorf("warcinfo Content-Type = %q", got)
	}
	for i, wantURI := range map[int]string{1: "https://ex.com/", 2: "https://ex.com/missing"} {
		if got := recs[i].headers["WARC-Type"]; got != "response" {
			t.Errorf("record %d WARC-Type = %q, want response", i, got)
		}
		if got := recs[i].headers["WARC-Target-URI"]; got != wantURI {
			t.Errorf("record %d WARC-Target-URI = %q, want %q", i, got, wantURI)
		}
	}
	if block := string(recs[1].block); !strings.HasPrefix(block, "HTTP/1.1 200 ") ||
		!strings.Contains(block, "<html><body>home</body></html>") {
		t.Errorf("200 response block = %q", block)
	}
	if block := string(recs[2].block); !strings.HasPrefix(block, "HTTP/1.1 404 ") {
		t.Errorf("404 response block = %q", block)
	}
}

func TestArchivePathEmptyWithoutArchive(t *testing.T) {
	dir := t.TempDir()
	c, err := CreateCrawl(dir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if got := c.ArchivePath(); got != "" {
		t.Errorf("ArchivePath = %q, want empty (Archive never called)", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "crawls", c.ID+".assets", "archive.warc.gz")); !os.IsNotExist(err) {
		t.Errorf("archive file must not exist, stat err = %v", err)
	}
}
