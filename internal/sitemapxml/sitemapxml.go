// Package sitemapxml decodes sitemap XML the way search-engine crawlers read
// it: a file whose URLs can be read is read, whatever its XML declaration
// says. encoding/xml rejects any version other than 1.0 and any encoding other
// than UTF-8 — a sitemap that declares XML 1.1, "utf8" or ISO-8859-1 but is
// otherwise a valid urlset would be thrown away whole. Unmarshal settles the
// declaration itself (it is dropped before encoding/xml sees it, the body
// transcoded to UTF-8 when it names a known legacy encoding) and reports what
// was odd about it as a note; only markup that really can't be read is an
// error. Every sitemap reader shares it: spider-crawl discovery, list mode's
// --sitemap source and the sitemap site check.
package sitemapxml

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	"golang.org/x/text/encoding/htmlindex"
	"golang.org/x/text/transform"
)

var (
	utf8BOM    = []byte("\xef\xbb\xbf")
	versionRe  = pseudoAttr("version")
	encodingRe = pseudoAttr("encoding")
)

// pseudoAttr matches one pseudo-attribute of an XML declaration, in either
// quote style and with the spaces XML allows around the "=".
func pseudoAttr(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?:^|\s)` + name + `\s*=\s*(?:"([^"]*)"|'([^']*)')`)
}

// Unmarshal is xml.Unmarshal for a sitemap body. note says what was odd about
// the XML declaration ("" for none); err is non-nil only when the markup itself
// can't be decoded.
func Unmarshal(body []byte, v any) (note string, err error) {
	r, note := reader(body)
	return note, xml.NewDecoder(r).Decode(v)
}

// reader returns body with its XML declaration (if any) settled: removed, the
// newlines inside it kept so syntax-error line numbers still match the file,
// and the rest transcoded to UTF-8 when the declaration names a known legacy
// encoding. Nothing is copied: the parts are streamed.
func reader(body []byte) (io.Reader, string) {
	bom := bytes.HasPrefix(body, utf8BOM)
	start := 0
	if bom {
		start = len(utf8BOM)
	}
	for start < len(body) && isSpace(body[start]) {
		start++
	}
	rest := body[start:]
	// "<?xml" then whitespace or "?>": a declaration, not <?xml-stylesheet?>
	if len(rest) < 6 || !bytes.HasPrefix(rest, []byte("<?xml")) || !(isSpace(rest[5]) || rest[5] == '?') {
		return bytes.NewReader(body), ""
	}
	end := bytes.Index(rest, []byte("?>"))
	if end < 0 {
		return bytes.NewReader(body), "" // unterminated: the decoder reports it
	}
	decl := string(rest[len("<?xml"):end])
	var after io.Reader = bytes.NewReader(rest[end+len("?>"):])

	var notes []string
	if ver := attr(versionRe, decl); ver != "" && ver != "1.0" {
		notes = append(notes, fmt.Sprintf("declares XML version %q; read as 1.0", ver))
	}
	switch label := attr(encodingRe, decl); {
	case label == "" || strings.EqualFold(label, "utf-8"):
	case bom: // the byte-order mark outranks the declaration
		notes = append(notes, fmt.Sprintf("declares encoding %q but starts with a UTF-8 byte-order mark; read as UTF-8", label))
	default:
		enc, err := htmlindex.Get(label)
		name := ""
		if err == nil {
			name, _ = htmlindex.Name(enc)
		}
		switch name {
		case "utf-8": // a misspelling such as "utf8"
			notes = append(notes, fmt.Sprintf("declares encoding %q; read as UTF-8", label))
		case "utf-16le", "utf-16be":
			// the declaration itself was read one byte per character
			notes = append(notes, fmt.Sprintf("declares encoding %q but is not UTF-16; read as UTF-8", label))
		case "", "replacement":
			notes = append(notes, fmt.Sprintf("declares unknown encoding %q; read as UTF-8", label))
		default:
			after = transform.NewReader(after, enc.NewDecoder())
			notes = append(notes, fmt.Sprintf("declares encoding %q; the sitemaps protocol requires UTF-8 (transcoded)", label))
		}
	}
	newlines := strings.Repeat("\n", strings.Count(decl, "\n"))
	return io.MultiReader(bytes.NewReader(body[:start]), strings.NewReader(newlines), after), strings.Join(notes, "; ")
}

func attr(re *regexp.Regexp, decl string) string {
	m := re.FindStringSubmatch(decl)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1] + m[2])
}

// isSpace is XML's S production.
func isSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
