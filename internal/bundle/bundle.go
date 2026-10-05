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
// A page record carries EVERYTHING the crawl stored about the page: every
// column of its pages row, every parsed fact, its custom search/extraction
// values, and — on request (Options.Full), when the crawl was configured to
// keep them — its raw and rendered HTML. The bundle is the one export a
// consumer should never have to go back to the store for, so an omission here
// is a bug, not a trim. Two things are deliberately NOT in the default stream:
// issues, which are verdicts (see Link) the consumer's own catalogue run can
// reproduce, and the page sources, which are opt-in because they are the
// whole file by volume — measured at 7–13× the gzipped bundle on crawls that
// kept both raw and rendered HTML — and most consumers want the text, not the
// markup.
//
// Three properties are load-bearing, and each has a test that fails if it is
// lost:
//
//   - It STREAMS. One sql.Rows scan over pages: decode a row, write a line, let
//     it go. LoadPages materialises every PageRecord including ContentText into
//     a map, which is the shape MEMORY-SCALING.md §4/Phase 2 exists to keep off
//     the finalize peak; a bundle of a multi-million-page crawl must not
//     reintroduce it. Peak RAM is one page record regardless of crawl size,
//     pinned by TestBundleRAMFlatOnPageCount. Stored HTML follows the same
//     rule: one page's file is read, written and dropped before the next.
//   - It is DETERMINISTIC. Two bundles of one unchanged crawl are byte-
//     identical: pages ordered by URL, links left in Facts.Links (document)
//     order, custom results sorted, map keys sorted by the encoder, and no
//     wall-clock value anywhere in a page record. That is what lets a consumer
//     diff two bundles and a test compare against a fixture.
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
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
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
	// Full also carries each page's stored sources — html and rendered_html —
	// when the crawl kept them (Header.Stored). Off by default: the sources are
	// the whole file by volume (7–13× the gzipped bundle on crawls that stored
	// both), and the metadata-only bundle is what an index or a diff wants.
	Full bool
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
	Pages int `json:"pages"`
	// StatusCounts breaks those same lines down by outcome, so a consumer can
	// describe a crawl without reading its pages. It is counted in the statement
	// that counts Pages and sums to it. Added within bluesnake.pages/1: a bundle
	// written before it has no such key, which means "no breakdown", not zeros.
	StatusCounts StatusCounts `json:"status_counts"`
	// Stored says which page sources the crawl was configured to keep on disk,
	// read from its frozen config rather than from the files. Full says whether
	// this stream carries them (Options.Full): a page line has `html` /
	// `rendered_html` exactly when Full is true AND Stored says the crawl kept
	// that kind. A consumer holding a bundle with stored.html true and full
	// false knows a re-bundle with --full yields the sources without a re-crawl.
	// Both added within bluesnake.pages/1: absent on an older bundle, which
	// carries neither.
	Stored  Stored `json:"stored"`
	Full    bool   `json:"full"`
	Crawled int    `json:"crawled"`
	Total   int    `json:"total"`
	// ConfigDigest hashes the crawl's frozen config. The link-position rules are
	// configurable, so `position` is only interpretable against the config that
	// produced it; the digest is how a consumer notices a corpus built under two
	// different rule sets.
	ConfigDigest string `json:"config_digest"`
	// SiteChecks and LlmsTxt are the crawl-level stored data: the site-check
	// pass's reports (DESIGN.md §5.10) and the llms.txt audit's files. They
	// follow the data, not the config — a row exists exactly when a check ran
	// or a file was fetched, so [] means nothing ran, and the frozen config
	// (ConfigDigest) still records what was asked for. Reports only: their
	// findings are issues, which the bundle leaves out as verdicts. Both come
	// last because they are the header's bulk (the robots.txt body, the
	// llms.txt files). Added within bluesnake.pages/1: absent on an older
	// bundle, which means "not carried", not "none ran".
	SiteChecks []SiteCheck `json:"site_checks"`
	LlmsTxt    []LlmsTxt   `json:"llms_txt"`
}

// SiteCheck is one stored site-check report: the check kind (robots | sitemap
// | ai_bots | render_diff), the URL it audited, and the report exactly as the
// pass stored it — the robots report carries the robots.txt body the crawl
// obeyed (capped at the 500 KiB Google reads), the ai_bots report each bot's
// robots verdict and, when probed, its live fetch of the site root beside a
// control fetch. Sorted by (kind, subject). The stored checked_at is left out:
// it is a wall-clock value the report does not need.
type SiteCheck struct {
	Kind    string          `json:"kind"`
	Subject string          `json:"subject"`
	Report  json.RawMessage `json:"report"`
}

// LlmsTxt is one fetched /llms.txt or /llms-full.txt with its structural
// validation and its raw body — stored on a miss too, where it is whatever the
// server returned — and the curated links it listed. Sorted by URL, links by
// URL; Links is always an array (only llms.txt carries a link index).
type LlmsTxt struct {
	URL       string        `json:"url"`
	Kind      string        `json:"kind"` // llms_txt | llms_full_txt
	Status    int           `json:"status"`
	Found     bool          `json:"found"`
	Title     string        `json:"title"`
	Summary   string        `json:"summary"`
	Malformed bool          `json:"malformed"`
	Content   string        `json:"content"`
	Links     []LlmsTxtLink `json:"links"`
}

// LlmsTxtLink is one curated link an llms.txt listed: the resolved target, the
// section heading it sat under and its link text.
type LlmsTxtLink struct {
	URL     string `json:"url"`
	Section string `json:"section"`
	Anchor  string `json:"anchor"`
}

// StatusCounts is the header's per-outcome breakdown of the page lines, keyed
// exactly as the `--progress json` feed keys its live counters and classified
// by the same rule (store.PageBreakdown mirrors the runner's): state
// blocked_robots is blocked_by_robots and state error is no_response, then
// status >= 500 / 400 / 300 / 200 by class, and a page with no class (status
// below 200) is no_response too — so every page counts exactly once and the six
// sum to Pages. All six keys are always present, zeros included.
type StatusCounts struct {
	Status2xx       int `json:"status_2xx"`
	Status3xx       int `json:"status_3xx"`
	Status4xx       int `json:"status_4xx"`
	Status5xx       int `json:"status_5xx"`
	BlockedByRobots int `json:"blocked_by_robots"`
	NoResponse      int `json:"no_response"`
}

// Stored is the header's record of which page sources the crawl kept on disk.
// HTML is extraction.store_html (the raw response body); RenderedHTML is
// extraction.store_rendered_html AND a JavaScript-rendering crawl, since the
// flag alone writes nothing without a renderer. In a Full bundle each true here
// means every page line carries the matching field — as a string, empty where
// that page had no stored file (a non-HTML response, an error, an external
// page). Otherwise it is a statement about the crawl alone.
type Stored struct {
	HTML         bool `json:"html"`
	RenderedHTML bool `json:"rendered_html"`
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
	// MatchedRobotsLine is the robots.txt line that decided a blocked page (0
	// when none did). Proxy is the redacted egress label that fetched the page —
	// what turns "why did these 200 URLs 403?" into a query — and never carries
	// credentials.
	MatchedRobotsLine int    `json:"matched_robots_line"`
	Proxy             string `json:"proxy"`
	// The link graph as finalize derived it: inlink counts over the gated
	// discovery edges, the first page that discovered this one, and the
	// PageRank-style link score. Zero/empty on a crawl that has not finalised.
	Inlinks            int     `json:"inlinks"`
	UniqueInlinks      int     `json:"unique_inlinks"`
	UniqueOutlinks     int     `json:"unique_outlinks"`
	LinkScore          float64 `json:"link_score"`
	DiscoveredFrom     string  `json:"discovered_from"`
	OutsideStartFolder bool    `json:"outside_start_folder"`
	// Duplicates: DuplicateOf names the page whose raw body this one was
	// byte-identical to (the R8 short-circuit); ClosestSimilarity and
	// NearDupCount are the near-duplicate analysis over content text.
	DuplicateOf       string  `json:"duplicate_of"`
	ClosestSimilarity float64 `json:"closest_similarity"`
	NearDupCount      int     `json:"near_dup_count"`
	// Headers are the response headers as stored (first value each). The map
	// is always present — {} where nothing was recorded — and LastModified stays
	// as the one header the first bundles already pulled out.
	Headers      map[string]string `json:"headers"`
	LastModified string            `json:"last_modified"`
	// Sitemaps are the sitemap entries that list this page, sorted by sitemap,
	// each with the <lastmod> it gave the page exactly as written ("" when it
	// gave none, or on a crawl stored before lastmod was kept). [] when no
	// sitemap lists the page. A lastmod belongs to an entry, not a URL — two
	// sitemaps can date one page differently — which is why it travels with
	// membership rather than as one value.
	Sitemaps        []SitemapEntry `json:"sitemaps"`
	Title           string         `json:"title"`
	MetaDescription string         `json:"meta_description"`
	MetaKeywords    []string       `json:"meta_keywords"`
	// Authors is the evidence of who wrote the page, in document order, each
	// entry naming its source (meta | article | rel | microdata | byline) —
	// what was found, not a verdict on who the author is. JSON-LD authors are
	// in Structured verbatim. A parsed fact, so a crawl made before it was
	// retained carries [].
	Authors []Author `json:"authors"`
	// H1, H2, MetaRobots and XRobotsTag stay ARRAYS. They are natural multiples
	// that CSV forced into H1-1, H1-2, …; the single-value flattening in the tab
	// exports is a presentation choice a machine format should not inherit.
	// HeadingLevels is the document order of every h1..h6 level, the evidence
	// behind any heading-structure verdict.
	H1            []string `json:"h1"`
	H2            []string `json:"h2"`
	HeadingLevels []int    `json:"heading_levels"`
	// Canonical, RelNext and RelPrev follow the `canonicals` tab's rule (HTML,
	// falling back to the HTTP Link header), not the `internal` tab's HTML-only
	// one.
	Canonical  string   `json:"canonical"`
	RelNext    string   `json:"rel_next"`
	RelPrev    string   `json:"rel_prev"`
	MetaRobots []string `json:"meta_robots"`
	XRobotsTag []string `json:"x_robots_tag"`
	// MetaRobotsAgents are the robots meta tags addressed to a single crawler
	// (googlebot, bingbot, …), which meta_robots — the generic tag — does not
	// carry; the agent name is lowercased. DataNoSnippet is the text of every
	// element carrying a data-nosnippet attribute, in document order. Both are
	// parsed facts, so a crawl made before they were retained carries [].
	MetaRobotsAgents []AgentDirective `json:"meta_robots_agents"`
	DataNoSnippet    []string         `json:"data_nosnippet"`
	// MetaRefresh is the raw content attribute; MetaRefreshURL its resolved
	// target (the page itself for a bare delay, "" when there is none).
	MetaRefresh    string `json:"meta_refresh"`
	MetaRefreshURL string `json:"meta_refresh_url"`
	Lang           string `json:"lang"`
	IsAMP          bool   `json:"is_amp"`
	// Hreflang keeps both sources apart (html | http) where Canonical merges
	// them: a page legitimately declares alternates in both places at once.
	Hreflang         []Hreflang   `json:"hreflang"`
	AMPLinks         []string     `json:"amp_links"`
	MobileAlternates []string     `json:"mobile_alternates"`
	Head             HeadValidity `json:"head"`
	// Readability: WordCount over the content area, TextRatio the share of page
	// bytes that are body text, and the Flesch reading-ease inputs and score.
	WordCount           int     `json:"word_count"`
	TextRatio           float64 `json:"text_ratio"`
	AvgWordsPerSentence float64 `json:"avg_words_per_sentence"`
	Flesch              float64 `json:"flesch"`
	// ContentHash is the MD5 of the raw response body — the key the identical-
	// content short-circuit matched on, and a consumer's cheapest change check.
	ContentHash string               `json:"content_hash"`
	ContentText string               `json:"content_text"`
	Structured  *structured.PageData `json:"structured,omitempty"`
	// JSDiff is the raw-vs-rendered comparison a JavaScript-rendering crawl
	// stores; absent on a crawl that did not render, like Structured on a page
	// with none.
	JSDiff *crawler.JSDiff `json:"jsdiff,omitempty"`
	// CustomResults are the values the crawl's custom_search / custom_extraction
	// / custom_js config asked for, by name. Always an array: a crawl with no
	// custom config has none, and [] says exactly that.
	CustomResults []CustomResult `json:"custom_results"`
	Links         []Link         `json:"links"`
	// HTML and RenderedHTML are the stored page sources, present on every line
	// when the bundle is Full and the header's Stored says the crawl kept them,
	// and absent otherwise — the one place a key's presence varies, because ""
	// cannot distinguish "not stored for this page" from "not carried". They
	// come last so the rest of a record is readable before the wall of markup.
	HTML         *string `json:"html,omitempty"`
	RenderedHTML *string `json:"rendered_html,omitempty"`
}

// Author is one piece of author evidence: where it was found and what it said.
// Name or URL is "" when that source carried none.
type Author struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	URL    string `json:"url"`
}

// SitemapEntry is one sitemap listing a page, with the lastmod it gave it.
type SitemapEntry struct {
	Sitemap string `json:"sitemap"`
	Lastmod string `json:"lastmod"`
}

// AgentDirective is one robots meta tag scoped to a named crawler.
type AgentDirective struct {
	Agent   string `json:"agent"`
	Content string `json:"content"`
}

// Hreflang is one hreflang annotation with its source (html | http).
type Hreflang struct {
	Lang   string `json:"lang"`
	URL    string `json:"url"`
	Source string `json:"source"`
}

// HeadValidity is the Google-parseability check over <head>: the elements
// found in it that do not belong there, and whether it was missing or
// duplicated.
type HeadValidity struct {
	InvalidElements []string `json:"invalid_elements"`
	Missing         bool     `json:"missing"`
	Multiple        bool     `json:"multiple"`
}

// CustomResult is one custom search / extraction / JS value, keyed by the
// configured name. Kind is search | extraction | js. Sorted by (kind, name).
type CustomResult struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Link is one edge nested under its source page. It carries the EVIDENCE a
// consumer classifies from — position, both DOM paths, anchor — and no verdict
// beyond the one bluesnake's configured rules already produced: whether a link
// is content or boilerplate is a tuned judgement over a corpus, and a verdict
// baked into an export is a verdict that needs a re-crawl to change.
type Link struct {
	URL string `json:"url"`
	// Raw is the href exactly as written, before resolution and normalisation.
	Raw    string `json:"raw"`
	Anchor string `json:"anchor"`
	// Alt, Width and Height belong to image links, Title to iframe links and
	// Lang to hreflang links; like Origin they are omitted where the link type
	// cannot carry them.
	Alt string `json:"alt,omitempty"`
	// NoAltAttr says an image link's <img> had no alt attribute at all, which
	// Alt cannot: it is omitted when empty, so a missing alt and a decorative
	// alt="" would look the same. Present on every image link, false included,
	// and absent on every other type.
	NoAltAttr *bool `json:"no_alt_attr,omitempty"`
	// Title is an iframe's title attribute, which usually names the video it
	// embeds.
	Title    string `json:"title,omitempty"`
	Rel      string `json:"rel"`
	Target   string `json:"target"`
	Nofollow bool   `json:"nofollow"`
	Type     string `json:"type"`
	PathType string `json:"path_type"`
	Position string `json:"position"`
	// ElemPath is the pure-positional SF link path; PositionPath is the
	// id/class-annotated chain the position rules matched. Both are empty when
	// the crawl ran with link-path storage off, and PositionPath is also empty
	// on crawls made before it was retained.
	ElemPath     string `json:"elem_path"`
	PositionPath string `json:"position_path"`
	Lang         string `json:"lang,omitempty"`
	Width        string `json:"width,omitempty"`
	Height       string `json:"height,omitempty"`
	// Origin is the JS-rendering provenance (html | rendered | xhr); absent on a
	// crawl that did not render.
	Origin string `json:"origin,omitempty"`
}

// Blob kinds as the crawler's BlobSink names them.
const (
	blobHTML         = "html"
	blobRenderedHTML = "rendered_html"
)

// pageColumns is the single row shape the stream decodes. The custom results,
// the sitemap entries and the two blob paths are correlated subqueries rather
// than per-page round trips, so the whole crawl is still ONE cursor:
// custom_results and sitemap_entries are aggregated into JSON arrays (sorted on
// decode — SQLite does not promise an aggregate's order) through an index on
// url, and each blobs lookup is a primary-key probe.
const pageColumns = `url, scope, state, COALESCE(depth, ?), status_code, status,
	content_type, COALESCE(http_version, ''), response_time_ms, size, fetch_error,
	redirect_url, redirect_type, indexable, indexability_status,
	matched_robots_line, COALESCE(proxy, ''),
	inlinks, unique_inlinks, unique_outlinks, link_score, COALESCE(discovered_from, ''), outside_start_folder,
	COALESCE(duplicate_of, ''), closest_similarity, near_dup_count,
	headers, structured, jsdiff, facts,
	COALESCE((SELECT json_group_array(json_object('kind', kind, 'name', name, 'value', value))
		FROM custom_results WHERE custom_results.url = pages.url), '[]'),
	COALESCE((SELECT json_group_array(json_object('sitemap', sitemap, 'lastmod', COALESCE(lastmod, '')))
		FROM sitemap_entries WHERE sitemap_entries.url = pages.url), '[]'),
	COALESCE((SELECT path FROM blobs WHERE blobs.url = pages.url AND blobs.kind = '` + blobHTML + `'), ''),
	COALESCE((SELECT path FROM blobs WHERE blobs.url = pages.url AND blobs.kind = '` + blobRenderedHTML + `'), '')`

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
	cfgYAML, err := st.Meta("config")
	if err != nil {
		return err
	}
	stored, err := storedAssets(cfgYAML)
	if err != nil {
		return err
	}

	// The count and the stream run in ONE transaction so the header's `pages` is
	// the exact number of lines that follow — the consumer's truncation check is
	// only worth having if it cannot race a concurrent write. The status
	// breakdown is counted in the same statement, over the same scope filter, so
	// it describes exactly those lines too.
	tx, err := st.DB().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	where, args := scopeFilter(scope)
	pages, sc, err := store.PageBreakdown(tx, where, args...)
	if err != nil {
		return err
	}
	counts := StatusCounts{
		Status2xx: sc.S2xx, Status3xx: sc.S3xx, Status4xx: sc.S4xx, Status5xx: sc.S5xx,
		BlockedByRobots: sc.Blocked, NoResponse: sc.NoResponse,
	}
	// The crawl-level rows are read in the same transaction too, so the header
	// describes one snapshot of the crawl.
	checks, err := siteChecks(tx)
	if err != nil {
		return err
	}
	llms, err := llmsTxt(tx)
	if err != nil {
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
	// Page text, HTML and JSON-LD are full of <, > and &. Go escapes those to
	// the six-byte < form by default, which bloats the stream for no
	// benefit; the result is valid JSON either way.
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
		StatusCounts:     counts,
		Stored:           stored,
		Full:             opts.Full,
		Crawled:          info.Crawled,
		Total:            info.Total,
		ConfigDigest:     configDigest(cfgYAML),
		SiteChecks:       checks,
		LlmsTxt:          llms,
	}); err != nil {
		return err
	}

	s := &stream{tx: tx, scope: scope, want: linkTypeSet(linkTypes), full: opts.Full, stored: stored, assetsDir: st.AssetsDir()}
	if err := s.pages(func(p *Page) error {
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

// stream is one pass over the pages table.
type stream struct {
	tx        *sql.Tx
	scope     string
	want      map[string]bool // link types to keep; nil keeps every type
	full      bool            // carry the stored sources Stored says exist
	stored    Stored
	assetsDir string
}

// pages scans the pages table one row at a time — decode a row, hand it over,
// let it go. This is the shape store.StreamContentText already uses, and the
// reason the bundle's peak RAM is one page record rather than the whole crawl.
// Do not replace it with a LoadPages map.
func (s *stream) pages(fn func(*Page) error) error {
	where, args := scopeFilter(s.scope)
	// One placeholder list serves the whole statement, in SQL order: the `?` in
	// pageColumns' COALESCE(depth, ?) comes before the scope filter's, so
	// NoDepth is bound first.
	q := `SELECT ` + pageColumns + ` FROM pages` + where + ` ORDER BY url`
	rows, err := s.tx.Query(q, append([]any{crawler.NoDepth}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var p Page
		var depth, indexable, outside int
		var headersJSON, structuredJSON, jsdiffJSON, factsJSON, customJSON, sitemapsJSON []byte
		var htmlPath, renderedPath string
		if err := rows.Scan(&p.URL, &p.Scope, &p.State, &depth, &p.StatusCode, &p.Status,
			&p.ContentType, &p.HTTPVersion, &p.ResponseTimeMs, &p.Size, &p.FetchError,
			&p.RedirectURL, &p.RedirectType, &indexable, &p.IndexabilityStatus,
			&p.MatchedRobotsLine, &p.Proxy,
			&p.Inlinks, &p.UniqueInlinks, &p.UniqueOutlinks, &p.LinkScore, &p.DiscoveredFrom, &outside,
			&p.DuplicateOf, &p.ClosestSimilarity, &p.NearDupCount,
			&headersJSON, &structuredJSON, &jsdiffJSON, &factsJSON, &customJSON, &sitemapsJSON,
			&htmlPath, &renderedPath); err != nil {
			return err
		}
		p.Indexable = indexable == 1
		p.OutsideStartFolder = outside == 1
		if depth != crawler.NoDepth {
			d := depth
			p.Depth = &d
		}
		p.Headers = map[string]string{}
		if len(headersJSON) > 0 {
			if err := json.Unmarshal(headersJSON, &p.Headers); err != nil {
				return fmt.Errorf("%s: headers: %w", p.URL, err)
			}
			p.LastModified = headerValue(p.Headers, "Last-Modified")
		}
		if len(structuredJSON) > 0 {
			p.Structured = &structured.PageData{}
			if err := json.Unmarshal(structuredJSON, p.Structured); err != nil {
				return fmt.Errorf("%s: structured: %w", p.URL, err)
			}
		}
		if len(jsdiffJSON) > 0 {
			p.JSDiff = &crawler.JSDiff{}
			if err := json.Unmarshal(jsdiffJSON, p.JSDiff); err != nil {
				return fmt.Errorf("%s: jsdiff: %w", p.URL, err)
			}
		}
		if err := json.Unmarshal(customJSON, &p.CustomResults); err != nil {
			return fmt.Errorf("%s: custom_results: %w", p.URL, err)
		}
		if p.CustomResults == nil {
			p.CustomResults = []CustomResult{}
		}
		slices.SortFunc(p.CustomResults, func(a, b CustomResult) int {
			if c := strings.Compare(a.Kind, b.Kind); c != 0 {
				return c
			}
			return strings.Compare(a.Name, b.Name)
		})
		if err := json.Unmarshal(sitemapsJSON, &p.Sitemaps); err != nil {
			return fmt.Errorf("%s: sitemaps: %w", p.URL, err)
		}
		if p.Sitemaps == nil {
			p.Sitemaps = []SitemapEntry{}
		}
		// (sitemap, url) is the table's key, so the sitemap alone orders them.
		slices.SortFunc(p.Sitemaps, func(a, b SitemapEntry) int { return strings.Compare(a.Sitemap, b.Sitemap) })
		// Links come from this row's own facts rather than a per-page query
		// against the links table: same values, no second round trip, and
		// Facts.Links is in document order where a links-table scan would need an
		// ordering column it does not have.
		var f parse.Facts
		if len(factsJSON) > 0 {
			if err := json.Unmarshal(factsJSON, &f); err != nil {
				return fmt.Errorf("%s: facts: %w", p.URL, err)
			}
		}
		// Non-HTML (PDFs, images) and every external page have no Facts. These
		// rows still matter to a consumer — a link to a crawled PDF is a link to
		// something the crawl found — so they are emitted with the Facts-derived
		// fields empty rather than filtered out; fillFromFacts over the zero
		// Facts keeps every array an array.
		fillFromFacts(&p, &f, s.want)
		if s.full && s.stored.HTML {
			if p.HTML, err = s.readBlob(p.URL, blobHTML, htmlPath); err != nil {
				return err
			}
		}
		if s.full && s.stored.RenderedHTML {
			if p.RenderedHTML, err = s.readBlob(p.URL, blobRenderedHTML, renderedPath); err != nil {
				return err
			}
		}
		if err := fn(&p); err != nil {
			return err
		}
	}
	return rows.Err()
}

// readBlob returns one stored page source: "" (never nil) when the page has no
// blobs row, the file's contents otherwise. The blobs table records the path as
// it was at crawl time; a store that has moved since still has the file under
// its own assets dir by name, so that is tried before a missing file is an
// error — a recorded blob whose file is gone is corruption, not an empty page.
func (s *stream) readBlob(url, kind, path string) (*string, error) {
	if path == "" {
		return new(string), nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = os.ReadFile(filepath.Join(s.assetsDir, filepath.Base(path)))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: stored %s: %w", url, kind, err)
	}
	str := string(data)
	return &str, nil
}

func fillFromFacts(p *Page, f *parse.Facts, want map[string]bool) {
	p.Title = first(f.Titles)
	p.MetaDescription = first(f.Descriptions)
	p.MetaKeywords = nonNil(f.Keywords)
	p.Authors = make([]Author, 0, len(f.Authors))
	for _, a := range f.Authors {
		p.Authors = append(p.Authors, Author{Source: a.Source, Name: a.Name, URL: a.URL})
	}
	p.H1 = nonNil(f.H1s)
	p.H2 = nonNil(f.H2s)
	p.HeadingLevels = f.HeadingLevels
	if p.HeadingLevels == nil {
		p.HeadingLevels = []int{}
	}
	p.Canonical = firstOf(f.CanonicalHTML, f.CanonicalHTTP)
	p.RelNext = firstOf(f.NextHTML, f.NextHTTP)
	p.RelPrev = firstOf(f.PrevHTML, f.PrevHTTP)
	p.MetaRobots = nonNil(f.MetaRobots)
	p.XRobotsTag = nonNil(f.XRobotsTag)
	p.MetaRobotsAgents = make([]AgentDirective, 0, len(f.MetaRobotsAgents))
	for _, d := range f.MetaRobotsAgents {
		p.MetaRobotsAgents = append(p.MetaRobotsAgents, AgentDirective{Agent: d.Agent, Content: d.Content})
	}
	p.DataNoSnippet = nonNil(f.NoSnippet)
	p.MetaRefresh = f.MetaRefresh
	p.MetaRefreshURL = f.MetaRefreshURL
	p.Lang = f.Lang
	p.IsAMP = f.IsAMP
	p.Hreflang = make([]Hreflang, 0, len(f.HreflangHTML)+len(f.HreflangHTTP))
	for _, h := range f.HreflangHTML {
		p.Hreflang = append(p.Hreflang, Hreflang{Lang: h.Lang, URL: h.URL, Source: "html"})
	}
	for _, h := range f.HreflangHTTP {
		p.Hreflang = append(p.Hreflang, Hreflang{Lang: h.Lang, URL: h.URL, Source: "http"})
	}
	p.AMPLinks = nonNil(f.AMPLinks)
	p.MobileAlternates = nonNil(f.MobileAlternates)
	p.Head = HeadValidity{
		InvalidElements: nonNil(f.Head.InvalidElementsInHead),
		Missing:         f.Head.MissingHead,
		Multiple:        f.Head.MultipleHead,
	}
	p.WordCount = f.WordCount
	p.TextRatio = f.TextRatio
	p.AvgWordsPerSentence = f.AvgWordsPerSentence
	p.Flesch = f.Flesch
	p.ContentHash = f.Hash
	p.ContentText = f.ContentText

	p.Links = []Link{}
	for _, l := range f.Links {
		if want != nil && !want[string(l.Type)] {
			continue
		}
		link := Link{
			URL: l.URL, Raw: l.Raw, Anchor: l.Anchor, Alt: l.Alt, Title: l.Title, Rel: l.Rel, Target: l.Target,
			Nofollow: l.Nofollow, Type: string(l.Type), PathType: l.PathType, Position: l.Position,
			ElemPath: l.ElemPath, PositionPath: l.PositionPath,
			Lang: l.Lang, Width: l.Width, Height: l.Height, Origin: l.Origin,
		}
		if l.Type == parse.Image {
			link.NoAltAttr = &l.NoAltAttr // l is this iteration's own copy
		}
		p.Links = append(p.Links, link)
	}
}

// siteChecks reads the stored site-check reports, sorted by (kind, subject):
// SQLite's binary collation is Go's string order, so the header is as
// deterministic as the page lines. [] when the pass never ran.
func siteChecks(tx *sql.Tx) ([]SiteCheck, error) {
	rows, err := tx.Query(`SELECT kind, subject, report FROM site_checks ORDER BY kind, subject`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	checks := []SiteCheck{}
	for rows.Next() {
		var sc SiteCheck
		var report string
		if err := rows.Scan(&sc.Kind, &sc.Subject, &report); err != nil {
			return nil, err
		}
		// Verbatim, so validated rather than decoded: a corrupt row must fail
		// here, naming itself, not deep inside the encoder.
		if !json.Valid([]byte(report)) {
			return nil, fmt.Errorf("site check %s %s: stored report is not JSON", sc.Kind, sc.Subject)
		}
		sc.Report = json.RawMessage(report)
		checks = append(checks, sc)
	}
	return checks, rows.Err()
}

// llmsTxt reads the stored llms.txt files, sorted by URL, each with the
// curated links it listed, sorted by URL. [] when no file was fetched.
func llmsTxt(tx *sql.Tx) ([]LlmsTxt, error) {
	rows, err := tx.Query(`SELECT url, kind, status, found, title, summary, malformed, COALESCE(content, '')
		FROM llmstxt ORDER BY url`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []LlmsTxt{}
	index := map[string]int{}
	for rows.Next() {
		f := LlmsTxt{Links: []LlmsTxtLink{}}
		var found, malformed int
		if err := rows.Scan(&f.URL, &f.Kind, &f.Status, &found, &f.Title, &f.Summary, &malformed, &f.Content); err != nil {
			return nil, err
		}
		f.Found, f.Malformed = found == 1, malformed == 1
		index[f.URL] = len(files)
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	lrows, err := tx.Query(`SELECT src, url, section, anchor FROM llmstxt_links ORDER BY src, url`)
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		var src string
		var l LlmsTxtLink
		if err := lrows.Scan(&src, &l.URL, &l.Section, &l.Anchor); err != nil {
			return nil, err
		}
		// A link is recorded only after its file, so its src always has a row.
		if i, ok := index[src]; ok {
			files[i].Links = append(files[i].Links, l)
		}
	}
	return files, lrows.Err()
}

// storedAssets reads which page assets the crawl kept from its frozen config —
// the same bytes the digest hashes, decoded the way resume decodes them. The
// config decides, not the files: see Header.Stored.
func storedAssets(cfgYAML string) (Stored, error) {
	cfg, err := config.Load([]byte(cfgYAML))
	if err != nil {
		return Stored{}, fmt.Errorf("frozen config: %w", err)
	}
	return Stored{
		HTML:         cfg.Extraction.StoreHTML,
		RenderedHTML: cfg.Extraction.StoreRenderedHTML && cfg.Rendering.Mode == "javascript",
	}, nil
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
func configDigest(cfgYAML string) string {
	sum := sha256.Sum256([]byte(cfgYAML))
	return "sha256:" + hex.EncodeToString(sum[:])
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

// firstOf is the canonicals-tab rule: the HTML value, else the HTTP header's.
func firstOf(fromHTML, fromHTTP []string) string {
	if v := first(fromHTML); v != "" {
		return v
	}
	return first(fromHTTP)
}
