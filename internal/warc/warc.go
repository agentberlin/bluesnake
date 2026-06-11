// Package warc implements a minimal WARC/1.1 writer (ISO 28500) for crawl
// archiving — warcinfo and response records only, which is all a crawler
// needs to preserve what it fetched. Each record is written as its own gzip
// member (the standard .warc.gz layout), so archives stream and standard
// tooling (and plain gzip multistream readers) can seek record by record.
package warc

import (
	"compress/gzip"
	"crypto/rand"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Writer emits WARC/1.1 records, one gzip member per record.
type Writer struct {
	w io.Writer
}

// NewWriter wraps w. The caller owns w; Close flushes the last member but
// does not close w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// recordID returns a fresh "<urn:uuid:...>" record id (RFC 9562 v4).
func recordID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("<urn:uuid:%x-%x-%x-%x-%x>", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// writeRecord emits one full record as its own gzip member.
func (w *Writer) writeRecord(headers [][2]string, block []byte) error {
	var rec strings.Builder
	rec.WriteString("WARC/1.1\r\n")
	for _, h := range headers {
		rec.WriteString(h[0])
		rec.WriteString(": ")
		rec.WriteString(h[1])
		rec.WriteString("\r\n")
	}
	fmt.Fprintf(&rec, "Content-Length: %d\r\n", len(block))
	rec.WriteString("\r\n")

	gz := gzip.NewWriter(w.w)
	if _, err := io.WriteString(gz, rec.String()); err != nil {
		return err
	}
	if _, err := gz.Write(block); err != nil {
		return err
	}
	if _, err := io.WriteString(gz, "\r\n\r\n"); err != nil {
		return err
	}
	return gz.Close()
}

func warcDate() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// WriteWarcinfo writes the archive-level warcinfo record. Fields are emitted
// sorted by name (application/warc-fields "name: value" lines).
func (w *Writer) WriteWarcinfo(fields map[string]string) error {
	names := make([]string, 0, len(fields))
	for n := range fields {
		names = append(names, n)
	}
	sort.Strings(names)
	var block strings.Builder
	for _, n := range names {
		block.WriteString(n)
		block.WriteString(": ")
		block.WriteString(fields[n])
		block.WriteString("\r\n")
	}
	return w.writeRecord([][2]string{
		{"WARC-Type", "warcinfo"},
		{"WARC-Record-ID", recordID()},
		{"WARC-Date", warcDate()},
		{"Content-Type", "application/warc-fields"},
	}, []byte(block.String()))
}

// WriteResponse writes one response record: the block is the reconstructed
// HTTP response (status line, canonical header lines, blank line, body).
func (w *Writer) WriteResponse(targetURI string, status int, proto string, header http.Header, body []byte) error {
	var block strings.Builder
	fmt.Fprintf(&block, "%s %d %s\r\n", proto, status, http.StatusText(status))
	names := make([]string, 0, len(header))
	for n := range header {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		for _, v := range header[n] {
			block.WriteString(http.CanonicalHeaderKey(n))
			block.WriteString(": ")
			block.WriteString(v)
			block.WriteString("\r\n")
		}
	}
	block.WriteString("\r\n")
	block.Write(body)
	return w.writeRecord([][2]string{
		{"WARC-Type", "response"},
		{"WARC-Record-ID", recordID()},
		{"WARC-Date", warcDate()},
		{"WARC-Target-URI", targetURI},
		{"Content-Type", "application/http; msgtype=response"},
	}, []byte(block.String()))
}

// Close finalizes the archive. Each record already closed its own gzip
// member, so there is nothing buffered — Close exists so callers can treat
// the writer like any other resource.
func (w *Writer) Close() error { return nil }
