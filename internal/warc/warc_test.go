package warc

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// record is one parsed WARC record: version line, header map, raw block.
type record struct {
	version string
	headers map[string]string
	block   []byte
}

// parseOneRecord consumes exactly one record from data and returns the rest.
// Layout per WARC/1.1: version line CRLF, header lines, CRLF, block, CRLFCRLF.
func parseOneRecord(t *testing.T, data []byte) (record, []byte) {
	t.Helper()
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		t.Fatalf("no header terminator in record data: %q", data)
	}
	lines := strings.Split(string(data[:idx]), "\r\n")
	rec := record{version: lines[0], headers: map[string]string{}}
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
	blockStart := idx + 4
	if len(data) < blockStart+n+4 {
		t.Fatalf("record truncated: have %d bytes, Content-Length %d", len(data)-blockStart, n)
	}
	rec.block = data[blockStart : blockStart+n]
	if string(data[blockStart+n:blockStart+n+4]) != "\r\n\r\n" {
		t.Fatalf("block of %d bytes not followed by CRLFCRLF (Content-Length wrong?)", n)
	}
	return rec, data[blockStart+n+4:]
}

func parseRecords(t *testing.T, data []byte) []record {
	t.Helper()
	var recs []record
	for len(data) > 0 {
		var rec record
		rec, data = parseOneRecord(t, data)
		recs = append(recs, rec)
	}
	return recs
}

// binBody is the binary response fixture: bytes 0x00 through 0x0f.
func binBody() []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(i)
	}
	return b
}

// archiveBytes writes the shared fixture archive: one warcinfo record and two
// response records (binary body, empty body).
func archiveBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := NewWriter(&buf)
	if err := w.WriteWarcinfo(map[string]string{"software": "bluesnake-test"}); err != nil {
		t.Fatalf("WriteWarcinfo: %v", err)
	}
	hdr := http.Header{}
	hdr.Set("Content-Type", "application/octet-stream")
	if err := w.WriteResponse("http://example.com/bin", 200, "HTTP/1.1", hdr, binBody()); err != nil {
		t.Fatalf("WriteResponse bin: %v", err)
	}
	if err := w.WriteResponse("http://example.com/empty", 404, "HTTP/1.1", http.Header{}, nil); err != nil {
		t.Fatalf("WriteResponse empty: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return buf.Bytes()
}

func TestWriterRoundTrip(t *testing.T) {
	gz, err := gzip.NewReader(bytes.NewReader(archiveBytes(t)))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	all, err := io.ReadAll(gz) // multistream mode: every member, one stream
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	recs := parseRecords(t, all)
	if len(recs) != 3 {
		t.Fatalf("parsed %d records, want 3", len(recs))
	}

	wantTypes := []string{"warcinfo", "response", "response"}
	seenIDs := map[string]bool{}
	for i, rec := range recs {
		if rec.version != "WARC/1.1" {
			t.Errorf("record %d version = %q, want WARC/1.1", i, rec.version)
		}
		if got := rec.headers["WARC-Type"]; got != wantTypes[i] {
			t.Errorf("record %d WARC-Type = %q, want %q", i, got, wantTypes[i])
		}
		id := rec.headers["WARC-Record-ID"]
		if !strings.HasPrefix(id, "<urn:uuid:") || !strings.HasSuffix(id, ">") {
			t.Errorf("record %d WARC-Record-ID = %q, want <urn:uuid:...>", i, id)
		}
		if seenIDs[id] {
			t.Errorf("duplicate WARC-Record-ID %q", id)
		}
		seenIDs[id] = true

		date := rec.headers["WARC-Date"]
		// WARC/1.1 dates are the UTC "Z" profile of RFC 3339.
		if !strings.HasSuffix(date, "Z") {
			t.Errorf("record %d WARC-Date = %q, want UTC (Z suffix)", i, date)
		}
		ts, err := time.Parse(time.RFC3339, date)
		if err != nil {
			t.Errorf("record %d WARC-Date %q does not parse as RFC3339: %v", i, date, err)
		} else if d := time.Since(ts); d < -time.Hour || d > time.Hour {
			t.Errorf("record %d WARC-Date %q not near now", i, date)
		}
	}

	// warcinfo record
	info := recs[0]
	if got := info.headers["Content-Type"]; got != "application/warc-fields" {
		t.Errorf("warcinfo Content-Type = %q", got)
	}
	if _, ok := info.headers["WARC-Target-URI"]; ok {
		t.Error("warcinfo must not carry WARC-Target-URI")
	}
	if !strings.Contains(string(info.block), "software: bluesnake-test") {
		t.Errorf("warcinfo block missing field, got %q", info.block)
	}

	// binary-body response record
	bin := recs[1]
	if got := bin.headers["WARC-Target-URI"]; got != "http://example.com/bin" {
		t.Errorf("bin WARC-Target-URI = %q", got)
	}
	if got := bin.headers["Content-Type"]; got != "application/http; msgtype=response" {
		t.Errorf("bin Content-Type = %q", got)
	}
	wantBin := "HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\n\r\n" + string(binBody())
	if string(bin.block) != wantBin {
		t.Errorf("bin block = %q, want %q", bin.block, wantBin)
	}
	if got := bin.headers["Content-Length"]; got != strconv.Itoa(len(wantBin)) {
		t.Errorf("bin Content-Length = %q, want %d", got, len(wantBin))
	}

	// empty-body response record
	empty := recs[2]
	if got := empty.headers["WARC-Target-URI"]; got != "http://example.com/empty" {
		t.Errorf("empty WARC-Target-URI = %q", got)
	}
	if got := empty.headers["Content-Type"]; got != "application/http; msgtype=response" {
		t.Errorf("empty Content-Type = %q", got)
	}
	wantEmpty := "HTTP/1.1 404 Not Found\r\n\r\n"
	if string(empty.block) != wantEmpty {
		t.Errorf("empty block = %q, want %q", empty.block, wantEmpty)
	}
}

func TestEachRecordIsOwnGzipMember(t *testing.T) {
	br := bufio.NewReader(bytes.NewReader(archiveBytes(t)))
	gz, err := gzip.NewReader(br)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()

	var members [][]byte
	for {
		gz.Multistream(false)
		member, err := io.ReadAll(gz)
		if err != nil {
			t.Fatalf("member %d: %v", len(members), err)
		}
		members = append(members, member)
		if err := gz.Reset(br); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("reset after member %d: %v", len(members), err)
		}
	}
	if len(members) != 3 {
		t.Fatalf("archive has %d gzip members, want 3 (one per record)", len(members))
	}
	for i, m := range members {
		if recs := parseRecords(t, m); len(recs) != 1 {
			t.Errorf("gzip member %d holds %d records, want exactly 1", i, len(recs))
		}
	}
}
