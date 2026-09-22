// Package bundle writes a stored crawl as one self-describing, streamable file:
// gzipped JSON Lines, a header record followed by one record per page, each
// page carrying its own text, structured data and nested link edges.
//
// It exists because the tab exports cannot carry a page. They are Screaming
// Frog-tab-shaped — flat `Dataset{Header []string, Rows [][]string}` — and
// deliberately so: they are what a human opens in a spreadsheet. A page record
// is not flat. Its headings, robots directives, schema.org types, raw JSON-LD
// blocks and link edges are all natural multiples that CSV forces into H1-1,
// H1-2, … or drops entirely, and its body text and JSON-LD have no column at
// all. Reconstructing one from the internal/links/response_codes tabs means
// joining three CSVs and still coming up short.
//
// Three properties are load-bearing, and each has a test that fails if it is
// lost:
//
//   - It STREAMS. One sql.Rows scan over pages: decode a row, write a line, let
//     it go. LoadPages materialises every PageRecord including ContentText into
//     a map, which is the shape MEMORY-SCALING.md §4/Phase 2 exists to keep off
//     the finalize peak; a bundle of a multi-million-page crawl must not
//     reintroduce it. Peak RAM is one page record regardless of crawl size,
//     pinned by TestBundleRAMFlatOnPageCount.
//   - It is DETERMINISTIC. Two bundles of one unchanged crawl are byte-
//     identical: pages ordered by URL, links left in Facts.Links (document)
//     order, and no wall-clock value anywhere in a page record. That is what
//     lets a consumer diff two bundles and a test compare against a fixture.
//   - It is VERSIONED and COUNTED. A consumer must be able to refuse a format
//     it does not understand rather than silently misread it (the failure mode
//     of every CSV column rename), and must be able to tell a truncated
//     transfer from a small crawl. The header's `format` and `pages` are those
//     two checks.
package bundle

import (
	"bufio"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
	"github.com/agentberlin/bluesnake/internal/structured"
	"github.com/agentberlin/bluesnake/internal/version"
)

// Format is the stream's self-description: <name>/<major>. The major is bumped
// for any change that is NOT purely additive — a removed or retyped field, a
// changed meaning — because that is what consumers pin on. Adding a field does
// not bump it, so a reader that ignores unknown keys keeps working.
const Format = "bluesnake.pages/1"

// Scope values for Options.Scope.
const (
	ScopeInternal = "internal"
	ScopeExternal = "external"
	ScopeAll      = "all"
)

// LinkTypeAll selects every link type instead of a named subset.
const LinkTypeAll = "all"

// Options configures one bundle.
type Options struct {
	// Scope selects which pages are emitted: internal (the default and the
	// analogue of SF's internal_all.csv — what a site's own corpus means),
	// external, or all. External pages carry no Facts at all, so including them
	// adds rows that are status codes and nothing else.
	Scope string
	// LinkTypes are the link types nested under each page; empty means the
	// default (hyperlink), and a single "all" entry means every type. A bundle's
	// links are the link GRAPH: image/css/js/xhr/canonical rows are assets and
	// references and are the majority of the table by volume, and a consumer
	// that wants them has the `links` tab export.
	LinkTypes []string
	// GZIP compresses the stream. Go's gzip writer emits no mtime and no OS
	// byte, so a gzipped bundle stays byte-reproducible like the plain one.
	GZIP bool
}

// Header is the bundle's first line: everything needed to know what the stream
// is, that it arrived whole, and what the values in it mean.
type Header struct {
	Format           string   `json:"format"`
	BluesnakeVersion string   `json:"bluesnake_version"`
	CrawlID          string   `json:"crawl_id"`
	Mode             string   `json:"mode"`
	Seeds            []string `json:"seeds"`
	// Status, StartedAt, FinishedAt, Crawled and Total come from the REGISTRY
	// row, not the crawl DB: a bundle of an interrupted crawl is legitimate and
	// should say so here rather than be refused.
	Status     string `json:"status"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Scope      string `json:"scope"`
	// LinkTypes echoes the filter that produced this stream, so an absent link
	// type is distinguishable from a page that had none.
	LinkTypes []string `json:"link_types"`
	// Pages is the EXACT number of page lines that follow, counted before the
	// stream opens. A consumer that reads fewer has a truncated file.
	Pages   int `json:"pages"`
	Crawled int `json:"crawled"`
	Total   int `json:"total"`
	// ConfigDigest hashes the crawl's frozen config. The link-position rules are
	// configurable, so `position` is only interpretable against the config that
	// produced it; the digest is how a consumer notices a corpus built under two
	// different rule sets.
	ConfigDigest string `json:"config_digest"`
}

// Page is one page record. Every field is an existing stored value — nothing
// here is computed for the first time.
type Page struct {
	URL   string `json:"url"`
	Scope string `json:"scope"`
	State string `json:"state"`
	// Depth is null when no followed-link path reaches the URL (crawler.NoDepth),
	// matching the blank the tab exports render.
	Depth              *int   `json:"depth"`
	StatusCode         int    `json:"status_code"`
	Status             string `json:"status"`
	ContentType        string `json:"content_type"`
	HTTPVersion        string `json:"http_version"`
	ResponseTimeMs     int64  `json:"response_time_ms"`
	Size               int    `json:"size"`
	FetchError         string `json:"fetch_error"`
	RedirectURL        string `json:"redirect_url"`
	RedirectType       string `json:"redirect_type"`
	Indexable          bool   `json:"indexable"`
	IndexabilityStatus string `json:"indexability_status"`
	LastModified       string `json:"last_modified"`
	Title              string `json:"title"`
	MetaDescription    string `json:"meta_description"`
	// H1, MetaRobots and XRobotsTag stay ARRAYS. They are natural multiples that
	// CSV forced into H1-1, H1-2, …; the single-value flattening in the tab
	// exports is a presentation choice a machine format should not inherit.
	H1 []string `json:"h1"`
	// Canonical follows the `canonicals` tab's rule (HTML, falling back to the
	// HTTP Link header), not the `internal` tab's HTML-only one.
	Canonical   string               `json:"canonical"`
	MetaRobots  []string             `json:"meta_robots"`
	XRobotsTag  []string             `json:"x_robots_tag"`
	WordCount   int                  `json:"word_count"`
	ContentText string               `json:"content_text"`
	Structured  *structured.PageData `json:"structured,omitempty"`
	Links       []Link               `json:"links"`
}

// Link is one edge nested under its source page. It carries the EVIDENCE a
// consumer classifies from — position, both DOM paths, anchor — and no verdict
// beyond the one bluesnake's configured rules already produced: whether a link
// is content or boilerplate is a tuned judgement over a corpus, and a verdict
// baked into an export is a verdict that needs a re-crawl to change.
type Link struct {
	URL      string `json:"url"`
	Anchor   string `json:"anchor"`
	Rel      string `json:"rel"`
	Nofollow bool   `json:"nofollow"`
	Type     string `json:"type"`
	Position string `json:"position"`
	// ElemPath is the pure-positional SF link path; PositionPath is the
	// id/class-annotated chain the position rules matched. Both are empty when
	// the crawl ran with link-path storage off, and PositionPath is also empty
	// on crawls made before it was retained.
	ElemPath     string `json:"elem_path"`
	PositionPath string `json:"position_path"`
	// Origin is the JS-rendering provenance (html | rendered | xhr); absent on a
	// crawl that did not render.
	Origin string `json:"origin,omitempty"`
}

// pageColumns is the single row shape the count and the stream agree on.
const pageColumns = `url, scope, state, COALESCE(depth, ?), status_code, status,
	content_type, COALESCE(http_version, ''), response_time_ms, size, fetch_error,
	redirect_url, redirect_type, indexable, indexability_status,
	headers, structured, facts`

// Validate reports whether the scope and link types are ones this bundle can
// emit. Callers that distinguish a bad request from a failed one (the CLI's
// exit-code contract) call it first; Write validates again regardless, so a
// library caller cannot skip it.
func (o Options) Validate() error {
	if _, err := normalizeScope(o.Scope); err != nil {
		return err
	}
	_, err := normalizeLinkTypes(o.LinkTypes)
	return err
}

// Write streams the crawl to w. info is the crawl's registry row (store.CrawlInfo).
func Write(st *store.Crawl, info store.Info, opts Options, w io.Writer) error {
	scope, err := normalizeScope(opts.Scope)
	if err != nil {
		return err
	}
	linkTypes, err := normalizeLinkTypes(opts.LinkTypes)
	if err != nil {
		return err
	}
	seeds, err := st.Seeds()
	if err != nil {
		return err
	}
	mode, err := st.Meta("mode")
	if err != nil {
		return err
	}
	digest, err := configDigest(st)
	if err != nil {
		return err
	}

	// The count and the stream run in ONE transaction so the header's `pages` is
	// the exact number of lines that follow — the consumer's truncation check is
	// only worth having if it cannot race a concurrent write.
	tx, err := st.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	where, args := scopeFilter(scope)
	var pages int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pages`+where, args...).Scan(&pages); err != nil {
		return err
	}

	out := w
	var gz *gzip.Writer
	if opts.GZIP {
		gz = gzip.NewWriter(w)
		out = gz
	}
	bw := bufio.NewWriterSize(out, 64<<10)
	enc := json.NewEncoder(bw)
	// Page text and JSON-LD are full of <, > and &. Go escapes those to the
	// six-byte \u003c form by default, which bloats the stream for no benefit;
	// the result is valid JSON either way.
	enc.SetEscapeHTML(false)

	if err := enc.Encode(Header{
		Format:           Format,
		BluesnakeVersion: version.Version,
		CrawlID:          info.ID,
		Mode:             mode,
		Seeds:            nonNil(seeds),
		Status:           info.Status,
		StartedAt:        rfc3339(info.Started),
		FinishedAt:       rfc3339(info.Finished),
		Scope:            scope,
		LinkTypes:        linkTypes,
		Pages:            pages,
		Crawled:          info.Crawled,
		Total:            info.Total,
		ConfigDigest:     digest,
	}); err != nil {
		return err
	}

	if err := streamPages(tx, scope, linkTypes, func(p *Page) error {
		return enc.Encode(p)
	}); err != nil {
		return err
	}
	if err := bw.Flush(); err != nil {
		return err
	}
	if gz != nil {
		if err := gz.Close(); err != nil {
			return err
		}
	}
	return nil // the deferred Rollback closes the read-only transaction
}

// streamPages scans the pages table one row at a time — decode a row, hand it
// over, let it go. This is the shape store.StreamContentText already uses, and
// the reason the bundle's peak RAM is one page record rather than the whole
// crawl. Do not replace it with a LoadPages map.
func streamPages(tx *sql.Tx, scope string, linkTypes []string, fn func(*Page) error) error {
	where, args := scopeFilter(scope)
	// One placeholder list serves the whole statement, in SQL order: the `?` in
	// pageColumns' COALESCE(depth, ?) comes before the scope filter's, so
	// NoDepth is bound first.
	q := `SELECT ` + pageColumns + ` FROM pages` + where + ` ORDER BY url`
	rows, err := tx.Query(q, append([]any{crawler.NoDepth}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()

	want := linkTypeSet(linkTypes)
	for rows.Next() {
		var p Page
		var depth, indexable int
		var headersJSON, structuredJSON, factsJSON []byte
		if err := rows.Scan(&p.URL, &p.Scope, &p.State, &depth, &p.StatusCode, &p.Status,
			&p.ContentType, &p.HTTPVersion, &p.ResponseTimeMs, &p.Size, &p.FetchError,
			&p.RedirectURL, &p.RedirectType, &indexable, &p.IndexabilityStatus,
			&headersJSON, &structuredJSON, &factsJSON); err != nil {
			return err
		}
		p.Indexable = indexable == 1
		if depth != crawler.NoDepth {
			d := depth
			p.Depth = &d
		}
		if len(headersJSON) > 0 {
			var headers map[string]string
			if err := json.Unmarshal(headersJSON, &headers); err != nil {
				return fmt.Errorf("%s: headers: %w", p.URL, err)
			}
			p.LastModified = headerValue(headers, "Last-Modified")
		}
		if len(structuredJSON) > 0 {
			p.Structured = &structured.PageData{}
			if err := json.Unmarshal(structuredJSON, p.Structured); err != nil {
				return fmt.Errorf("%s: structured: %w", p.URL, err)
			}
		}
		// Links come from this row's own facts rather than a per-page query
		// against the links table: same values, no second round trip, and
		// Facts.Links is in document order where a links-table scan would need an
		// ordering column it does not have.
		if len(factsJSON) > 0 {
			var f parse.Facts
			if err := json.Unmarshal(factsJSON, &f); err != nil {
				return fmt.Errorf("%s: facts: %w", p.URL, err)
			}
			fillFromFacts(&p, &f, want)
		} else {
			// Non-HTML (PDFs, images) and every external page have no Facts. These
			// rows still matter to a consumer — a link to a crawled PDF is a link
			// to something the crawl found — so they are emitted with the
			// Facts-derived fields empty rather than filtered out.
			p.H1, p.MetaRobots, p.XRobotsTag, p.Links = []string{}, []string{}, []string{}, []Link{}
		}
		if err := fn(&p); err != nil {
			return err
		}
	}
	return rows.Err()
}

func fillFromFacts(p *Page, f *parse.Facts, want map[string]bool) {
	p.Title = first(f.Titles)
	p.MetaDescription = first(f.Descriptions)
	p.H1 = nonNil(f.H1s)
	p.Canonical = first(f.CanonicalHTML)
	if p.Canonical == "" {
		p.Canonical = first(f.CanonicalHTTP)
	}
	p.MetaRobots = nonNil(f.MetaRobots)
	p.XRobotsTag = nonNil(f.XRobotsTag)
	p.WordCount = f.WordCount
	p.ContentText = f.ContentText

	p.Links = []Link{}
	for _, l := range f.Links {
		if want != nil && !want[string(l.Type)] {
			continue
		}
		p.Links = append(p.Links, Link{
			URL: l.URL, Anchor: l.Anchor, Rel: l.Rel, Nofollow: l.Nofollow,
			Type: string(l.Type), Position: l.Position,
			ElemPath: l.ElemPath, PositionPath: l.PositionPath, Origin: l.Origin,
		})
	}
}

// scopeFilter renders the page predicate for a scope. "all" filters nothing.
func scopeFilter(scope string) (string, []any) {
	if scope == ScopeAll {
		return "", nil
	}
	return " WHERE scope = ?", []any{scope}
}

func linkTypeSet(types []string) map[string]bool {
	if len(types) == 1 && types[0] == LinkTypeAll {
		return nil // no filter
	}
	set := make(map[string]bool, len(types))
	for _, t := range types {
		set[t] = true
	}
	return set
}

func normalizeScope(scope string) (string, error) {
	switch scope {
	case "":
		return ScopeInternal, nil
	case ScopeInternal, ScopeExternal, ScopeAll:
		return scope, nil
	}
	return "", fmt.Errorf("unknown scope %q (%s, %s, %s)", scope, ScopeInternal, ScopeExternal, ScopeAll)
}

// normalizeLinkTypes validates the requested types against the parser's own
// list, so a typo produces an error rather than a silently empty links array on
// every page.
func normalizeLinkTypes(types []string) ([]string, error) {
	if len(types) == 0 {
		return []string{string(parse.Hyperlink)}, nil
	}
	known := make([]string, 0, len(parse.LinkTypes()))
	for _, t := range parse.LinkTypes() {
		known = append(known, string(t))
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if t == LinkTypeAll {
			return []string{LinkTypeAll}, nil
		}
		if !slices.Contains(known, t) {
			return nil, fmt.Errorf("unknown link type %q (%s, or %s)",
				t, strings.Join(known, ", "), LinkTypeAll)
		}
		if !slices.Contains(out, t) {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return []string{string(parse.Hyperlink)}, nil
	}
	return out, nil
}

// configDigest hashes the crawl's frozen config YAML. The exact bytes are
// frozen at crawl start and never rewritten, so the digest is stable for the
// life of the crawl and comparable across crawls.
func configDigest(st *store.Crawl) (string, error) {
	cfg, err := st.Meta("config")
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(cfg))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// headerValue looks a response header up case-insensitively: the stored keys
// come from net/http's canonical form today, but a rendered response or a
// future transport need not agree, and the lookup costs nothing.
func headerValue(headers map[string]string, name string) string {
	if v, ok := headers[http.CanonicalHeaderKey(name)]; ok {
		return v
	}
	for k, v := range headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func rfc3339(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// nonNil keeps every array in the stream an array: a consumer sees [] rather
// than null for "this page had none", so the record shape never varies.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func first(values []string) string {
	if len(values) > 0 {
		return values[0]
	}
	return ""
}
