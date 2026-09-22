package structured

// PageData.JSONLD retention: the verbatim blocks behind the verdict. Types and
// Formats say WHAT a page declared; these tests pin that the bytes it declared
// it WITH survive extraction — in document order, pre-recovery, and including
// the blocks that failed to parse (the ones an owner most needs to see).

import (
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// Two blocks round-trip in document order, each trimmed but otherwise verbatim.
func TestJSONLDRawBlocksInDocumentOrder(t *testing.T) {
	first := `{"@context":"https://schema.org","@type":"Organization","name":"First","logo":"l.png","url":"https://ex.com"}`
	second := `{"@context":"https://schema.org","@type":"WebPage","name":"Second"}`
	body := "<html><head>\n<script type=\"application/ld+json\">\n  " + first + "\n</script>\n" +
		"<script type=\"application/ld+json\">" + second + "</script>\n</head><body></body></html>"

	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.JSONLD) != 2 {
		t.Fatalf("JSONLD = %d blocks (%v), want 2", len(d.JSONLD), d.JSONLD)
	}
	if d.JSONLD[0] != first {
		t.Errorf("JSONLD[0] = %q, want the first block verbatim %q", d.JSONLD[0], first)
	}
	if d.JSONLD[1] != second {
		t.Errorf("JSONLD[1] = %q, want the second block verbatim %q", d.JSONLD[1], second)
	}
}

// The lenient control-char retry salvages the DATA, but JSONLD must carry the
// block as the page served it — the broken source is what the owner has to fix,
// and Recovered already reports that a salvage happened.
func TestJSONLDRawIsPreRecovery(t *testing.T) {
	raw := "{\"@context\":\"https://schema.org\",\"@type\":\"VeterinaryCare\"," +
		"\"name\":\"Clinic\",\"address\":\"123 Main St\nSuite 4\"}"
	d := extract(t, "<html><head><script type=\"application/ld+json\">"+raw+"</script></head><body></body></html>",
		func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.Recovered) == 0 {
		t.Fatal("Recovered = empty: this block must still be reported as leniently salvaged")
	}
	if len(d.JSONLD) != 1 {
		t.Fatalf("JSONLD = %v, want the one block", d.JSONLD)
	}
	if d.JSONLD[0] != raw {
		t.Errorf("JSONLD[0] = %q, want the ORIGINAL bytes %q — not the escaped form", d.JSONLD[0], raw)
	}
	if strings.Contains(d.JSONLD[0], `\n`) {
		t.Errorf("JSONLD[0] carries the escaped form %q — escapeJSONControlChars' output must not be stored", d.JSONLD[0])
	}
}

// A syntactically broken block is stored AND reported: ParseErrors says the
// block is wrong, JSONLD is the only thing that can show which block.
func TestJSONLDRawStoresUnparseableBlock(t *testing.T) {
	broken := `{"@type":"Article", "headline": }`
	d := extract(t, `<html><head><script type="application/ld+json">`+broken+`</script></head><body></body></html>`,
		func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.ParseErrors) == 0 {
		t.Error("ParseErrors = empty, want the syntax error reported")
	}
	if len(d.JSONLD) != 1 || d.JSONLD[0] != broken {
		t.Errorf("JSONLD = %v, want the broken block %q stored verbatim", d.JSONLD, broken)
	}
}

// Microdata and RDFa have no verbatim block to keep: a page carrying only
// microdata contributes to Formats/Types and leaves JSONLD nil.
func TestJSONLDRawEmptyForMicrodataOnlyPage(t *testing.T) {
	body := `<html><body><div itemscope itemtype="https://schema.org/Person">
		<span itemprop="name">Ada</span></div></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.Microdata = true; s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if d.JSONLD != nil {
		t.Errorf("JSONLD = %v, want nil on a microdata-only page", d.JSONLD)
	}
	if len(d.Types) == 0 {
		t.Errorf("types = %v, want the microdata type still extracted", d.Types)
	}
}

// Templated JSON-LD is not the page's structured data (walk skips <template>),
// so it must not leak into the retained blocks either.
func TestJSONLDRawSkipsTemplatedBlocks(t *testing.T) {
	body := `<html><head><script type="application/ld+json">{"@type":"WebPage","name":"Real"}</script></head>
		<body><template><script type="application/ld+json">{"@type":"Product","name":"Templated"}</script></template></body></html>`
	d := extract(t, body, func(s *config.StructuredDataConfig) { s.JSONLD = true })
	if d == nil {
		t.Fatal("nil data")
	}
	if len(d.JSONLD) != 1 || strings.Contains(d.JSONLD[0], "Templated") {
		t.Errorf("JSONLD = %v, want only the real block", d.JSONLD)
	}
}
