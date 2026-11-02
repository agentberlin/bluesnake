package sitemapxml

import (
	"encoding/xml"
	"strings"
	"testing"
)

type set struct {
	XMLName xml.Name
	URLs    []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

const body = `
<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
  <url><loc>https://ex.com/a</loc></url>
  <url><loc>https://ex.com/b</loc></url>
</urlset>`

// TestUnmarshalReadsAnyDeclaration: a urlset is read whatever its first line
// declares — encoding/xml alone rejects every case with a note here (the
// miniorange.com sitemap declares XML 1.1).
func TestUnmarshalReadsAnyDeclaration(t *testing.T) {
	for _, tc := range []struct {
		name, prefix, note string
	}{
		{"no declaration", "", ""},
		{"1.0", `<?xml version="1.0" encoding="UTF-8"?>`, ""},
		{"lower-case utf-8", `<?xml version="1.0" encoding="utf-8"?>`, ""},
		{"no encoding", `<?xml version="1.0"?>`, ""},
		{"UTF-8 BOM", "\xef\xbb\xbf" + `<?xml version="1.0" encoding="UTF-8"?>`, ""},
		{"blank line first", "\n  " + `<?xml version="1.0" encoding="UTF-8"?>`, ""},
		{"1.1", `<?xml version="1.1" encoding="UTF-8"?>`, `declares XML version "1.1"; read as 1.0`},
		{"1.1 single quotes", `<?xml version='1.1' encoding='UTF-8'?>`, `declares XML version "1.1"; read as 1.0`},
		{"2.0", `<?xml version="2.0" encoding="UTF-8"?>`, `declares XML version "2.0"; read as 1.0`},
		{"1.1 after BOM and blank line", "\xef\xbb\xbf\n" + `<?xml version="1.1"?>`, `declares XML version "1.1"; read as 1.0`},
		{"spaces around =", `<?xml version = "1.1" encoding = "UTF-8" ?>`, `declares XML version "1.1"; read as 1.0`},
		{"utf8 misspelling", `<?xml version="1.0" encoding="utf8"?>`, `declares encoding "utf8"; read as UTF-8`},
		{"ISO-8859-1", `<?xml version="1.0" encoding="ISO-8859-1"?>`, `declares encoding "ISO-8859-1"; the sitemaps protocol requires UTF-8 (transcoded)`},
		{"windows-1252", `<?xml version="1.0" encoding="windows-1252"?>`, `declares encoding "windows-1252"; the sitemaps protocol requires UTF-8 (transcoded)`},
		{"us-ascii", `<?xml version="1.0" encoding="us-ascii"?>`, `declares encoding "us-ascii"; the sitemaps protocol requires UTF-8 (transcoded)`},
		{"unknown encoding", `<?xml version="1.0" encoding="klingon"?>`, `declares unknown encoding "klingon"; read as UTF-8`},
		{"UTF-16 label on a single-byte file", `<?xml version="1.0" encoding="UTF-16"?>`, `declares encoding "UTF-16" but is not UTF-16; read as UTF-8`},
		{"BOM outranks the encoding", "\xef\xbb\xbf" + `<?xml version="1.0" encoding="ISO-8859-1"?>`, `declares encoding "ISO-8859-1" but starts with a UTF-8 byte-order mark; read as UTF-8`},
		{"both odd", `<?xml version="1.1" encoding="latin1"?>`, `declares XML version "1.1"; read as 1.0; declares encoding "latin1"; the sitemaps protocol requires UTF-8 (transcoded)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var s set
			note, err := Unmarshal([]byte(tc.prefix+body), &s)
			if err != nil {
				t.Fatalf("Unmarshal: %v", err)
			}
			if s.XMLName.Local != "urlset" || len(s.URLs) != 2 || s.URLs[1].Loc != "https://ex.com/b" {
				t.Errorf("decoded %+v, want the two-URL urlset", s)
			}
			if note != tc.note {
				t.Errorf("note = %q, want %q", note, tc.note)
			}
		})
	}
}

// TestUnmarshalTranscodes: a legacy single-byte body comes out as UTF-8 — the
// é in the URL is the one byte 0xE9 in ISO-8859-1, which UTF-8 rejects.
func TestUnmarshalTranscodes(t *testing.T) {
	raw := `<?xml version="1.0" encoding="ISO-8859-1"?><urlset><url><loc>https://ex.com/caf` + "\xe9" + `</loc></url></urlset>`
	var s set
	if _, err := Unmarshal([]byte(raw), &s); err != nil {
		t.Fatal(err)
	}
	if len(s.URLs) != 1 || s.URLs[0].Loc != "https://ex.com/café" {
		t.Errorf("decoded %+v, want https://ex.com/café", s.URLs)
	}
}

// TestUnmarshalStillRejectsBrokenMarkup: settling the declaration never makes
// unreadable markup readable.
func TestUnmarshalStillRejectsBrokenMarkup(t *testing.T) {
	for name, raw := range map[string]string{
		"not xml":                  "this is not xml at all <",
		"unclosed root":            `<?xml version="1.1"?><urlset><url><loc>https://ex.com/a</loc></url>`,
		"unterminated declaration": `<?xml version="1.0" <urlset></urlset>`,
		"invalid UTF-8":            `<?xml version="1.0" encoding="UTF-8"?><urlset><url><loc>https://ex.com/caf` + "\xe9" + `</loc></url></urlset>`,
	} {
		t.Run(name, func(t *testing.T) {
			var s set
			if _, err := Unmarshal([]byte(raw), &s); err == nil {
				t.Errorf("Unmarshal(%q) = nil error, want one", raw)
			}
		})
	}
}

// TestUnmarshalKeepsLineNumbers: a declaration spread over lines is dropped
// without shifting the line a later syntax error is reported on.
func TestUnmarshalKeepsLineNumbers(t *testing.T) {
	raw := "<?xml version=\"1.1\"\n  encoding=\"UTF-8\"?>\n<urlset>\n<url></loc></url>\n</urlset>"
	var s set
	_, err := Unmarshal([]byte(raw), &s)
	if err == nil || !strings.Contains(err.Error(), "line 4") {
		t.Errorf("err = %v, want a syntax error on line 4", err)
	}
}

// TestUnmarshalLeavesOtherProcessingInstructions: <?xml-stylesheet?> is not a
// declaration and passes through untouched.
func TestUnmarshalLeavesOtherProcessingInstructions(t *testing.T) {
	raw := `<?xml-stylesheet type="text/xsl" href="/sitemap.xsl"?>` + body
	var s set
	note, err := Unmarshal([]byte(raw), &s)
	if err != nil || note != "" || len(s.URLs) != 2 {
		t.Errorf("Unmarshal = (%q, %v) with %d URLs, want (\"\", nil) with 2", note, err, len(s.URLs))
	}
}
