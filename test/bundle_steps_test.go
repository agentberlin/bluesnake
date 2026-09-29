package acceptance

// Steps for features/bundle.feature. The bundle is read back exactly as a
// consumer would: parse line 1 as the header, every later line as a page, and
// assert against the decoded records rather than the raw text — so a scenario
// fails on a changed MEANING, not on a changed byte.

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentberlin/bluesnake/internal/bundle"
	"github.com/cucumber/godog"
)

const bundleFile = "crawl.jsonl"

func (w *world) registerBundleSteps(sc *godog.ScenarioContext) {
	sc.Step(`^the bundle header has "([^"]*)" equal to "([^"]*)"$`, w.bundleHeaderEquals)
	sc.Step(`^the bundle header carries "([^"]*)"$`, w.bundleHeaderCarries)
	sc.Step(`^the bundle page count matches the header$`, w.bundlePageCountMatches)
	sc.Step(`^the bundle header counts (\d+) pages? as "([^"]*)"$`, w.bundleHeaderStatusCount)
	sc.Step(`^the bundle header status counts add up to its pages$`, w.bundleHeaderStatusCountsSum)
	sc.Step(`^the bundle page "([^"]*)" has "([^"]*)" equal to "([^"]*)"$`, w.bundlePageFieldEquals)
	sc.Step(`^the bundle page "([^"]*)" has "([^"]*)" containing "([^"]*)"$`, w.bundlePageFieldContains)
	sc.Step(`^the bundle page "([^"]*)" has structured jsonld containing "([^"]*)"$`, w.bundlePageJSONLDContains)
	sc.Step(`^the bundle page "([^"]*)" has a link to "([^"]*)" with "([^"]*)" equal to "([^"]*)"$`, w.bundleLinkFieldEquals)
	sc.Step(`^the bundle page "([^"]*)" has a link to "([^"]*)" with "([^"]*)" containing "([^"]*)"$`, w.bundleLinkFieldContains)
	sc.Step(`^the bundle contains (an|no) external page$`, w.bundleExternalPages)
	sc.Step(`^the bundle contains (a|no) link of type "([^"]*)"$`, w.bundleLinksOfType)
	sc.Step(`^the file "([^"]*)" in the store dir is a gzip stream containing "([^"]*)"$`, w.storeFileGzipContains)
	sc.Step(`^the files "([^"]*)" and "([^"]*)" in the store dir are identical$`, w.storeFilesIdentical)
}

// readBundle decodes the bundle written to the store dir.
func (w *world) readBundle() (bundle.Header, []bundle.Page, error) {
	var h bundle.Header
	data, err := os.ReadFile(filepath.Join(w.storeDirPath(), bundleFile))
	if err != nil {
		return h, nil, err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return h, nil, fmt.Errorf("bundle is empty")
	}
	if err := json.Unmarshal([]byte(lines[0]), &h); err != nil {
		return h, nil, fmt.Errorf("header line is not JSON: %w\n%s", err, lines[0])
	}
	pages := make([]bundle.Page, 0, len(lines)-1)
	for _, line := range lines[1:] {
		var p bundle.Page
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return h, nil, fmt.Errorf("page line is not JSON: %w\n%s", err, line)
		}
		pages = append(pages, p)
	}
	return h, pages, nil
}

// bundlePage finds the record for a fixture path on the scenario's test server.
func (w *world) bundlePage(path string) (*bundle.Page, error) {
	_, pages, err := w.readBundle()
	if err != nil {
		return nil, err
	}
	want := w.ensureServer().URL + path
	for i := range pages {
		if pages[i].URL == want {
			return &pages[i], nil
		}
	}
	urls := make([]string, len(pages))
	for i, p := range pages {
		urls[i] = p.URL
	}
	return nil, fmt.Errorf("no bundle page for %s; got %v", want, urls)
}

func (w *world) bundleHeaderEquals(field, want string) error {
	h, _, err := w.readBundle()
	if err != nil {
		return err
	}
	got, err := headerField(h, field)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("header %s = %q, want %q", field, got, want)
	}
	return nil
}

func (w *world) bundleHeaderCarries(field string) error {
	h, _, err := w.readBundle()
	if err != nil {
		return err
	}
	got, err := headerField(h, field)
	if err != nil {
		return err
	}
	if got == "" {
		return fmt.Errorf("header %s is empty", field)
	}
	return nil
}

// headerField reads one header value through the marshalled JSON, so the step
// names the field exactly as a consumer sees it on the wire.
func headerField(h bundle.Header, field string) (string, error) {
	raw, err := json.Marshal(h)
	if err != nil {
		return "", err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return "", err
	}
	v, ok := m[field]
	if !ok {
		return "", fmt.Errorf("header has no field %q", field)
	}
	return fmt.Sprint(v), nil
}

// The header's page count is the consumer's truncation check: it is only worth
// having if it is exact.
func (w *world) bundlePageCountMatches() error {
	h, pages, err := w.readBundle()
	if err != nil {
		return err
	}
	if h.Pages != len(pages) {
		return fmt.Errorf("header pages = %d, but %d page lines follow", h.Pages, len(pages))
	}
	if h.Pages == 0 {
		return fmt.Errorf("bundle carries no pages")
	}
	return nil
}

// statusCountKeys are the header's status_counts keys — the progress feed's.
var statusCountKeys = []string{"status_2xx", "status_3xx", "status_4xx", "status_5xx", "blocked_by_robots", "no_response"}

// bundleStatusCounts reads status_counts off the raw header line, as a
// consumer sees it on the wire: a missing key is a failure, not a zero.
func (w *world) bundleStatusCounts() (map[string]int, int, error) {
	data, err := os.ReadFile(filepath.Join(w.storeDirPath(), bundleFile))
	if err != nil {
		return nil, 0, err
	}
	line, _, _ := strings.Cut(string(data), "\n")
	var h struct {
		Pages        int             `json:"pages"`
		StatusCounts *map[string]int `json:"status_counts"`
	}
	if err := json.Unmarshal([]byte(line), &h); err != nil {
		return nil, 0, fmt.Errorf("header line is not JSON: %w\n%s", err, line)
	}
	if h.StatusCounts == nil {
		return nil, 0, fmt.Errorf("header has no status_counts:\n%s", line)
	}
	for _, k := range statusCountKeys {
		if _, ok := (*h.StatusCounts)[k]; !ok {
			return nil, 0, fmt.Errorf("status_counts has no %q key: %v", k, *h.StatusCounts)
		}
	}
	return *h.StatusCounts, h.Pages, nil
}

func (w *world) bundleHeaderStatusCount(want int, key string) error {
	counts, _, err := w.bundleStatusCounts()
	if err != nil {
		return err
	}
	if got, ok := counts[key]; !ok || got != want {
		return fmt.Errorf("status_counts %s = %d (present: %v), want %d: %v", key, got, ok, want, counts)
	}
	return nil
}

// The six are a partition of the page lines: they sum to `pages`, which is
// the number of lines that follow.
func (w *world) bundleHeaderStatusCountsSum() error {
	counts, pages, err := w.bundleStatusCounts()
	if err != nil {
		return err
	}
	sum := 0
	for _, k := range statusCountKeys {
		sum += counts[k]
	}
	if sum != pages {
		return fmt.Errorf("status_counts sum to %d, header pages = %d: %v", sum, pages, counts)
	}
	return w.bundlePageCountMatches()
}

func (w *world) bundlePageFieldEquals(path, field, want string) error {
	got, err := w.bundlePageField(path, field)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("page %s: %s = %q, want %q", path, field, got, want)
	}
	return nil
}

func (w *world) bundlePageFieldContains(path, field, want string) error {
	got, err := w.bundlePageField(path, field)
	if err != nil {
		return err
	}
	if !strings.Contains(got, want) {
		return fmt.Errorf("page %s: %s = %q, want it to contain %q", path, field, got, want)
	}
	return nil
}

func (w *world) bundlePageField(path, field string) (string, error) {
	p, err := w.bundlePage(path)
	if err != nil {
		return "", err
	}
	switch field {
	case "content_text":
		return p.ContentText, nil
	case "title":
		return p.Title, nil
	case "meta_description":
		return p.MetaDescription, nil
	case "state":
		return p.State, nil
	case "content_type":
		return p.ContentType, nil
	}
	return "", fmt.Errorf("unsupported bundle page field %q", field)
}

func (w *world) bundlePageJSONLDContains(path, want string) error {
	p, err := w.bundlePage(path)
	if err != nil {
		return err
	}
	if p.Structured == nil || len(p.Structured.JSONLD) == 0 {
		return fmt.Errorf("page %s carries no structured.jsonld (%+v)", path, p.Structured)
	}
	for _, block := range p.Structured.JSONLD {
		if strings.Contains(block, want) {
			return nil
		}
	}
	return fmt.Errorf("page %s: no jsonld block contains %q; got %v", path, want, p.Structured.JSONLD)
}

func (w *world) bundleLinkFieldEquals(path, target, field, want string) error {
	got, err := w.bundleLinkField(path, target, field)
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("page %s link %s: %s = %q, want %q", path, target, field, got, want)
	}
	return nil
}

func (w *world) bundleLinkFieldContains(path, target, field, want string) error {
	got, err := w.bundleLinkField(path, target, field)
	if err != nil {
		return err
	}
	if !strings.Contains(got, want) {
		return fmt.Errorf("page %s link %s: %s = %q, want it to contain %q", path, target, field, got, want)
	}
	return nil
}

func (w *world) bundleLinkField(path, target, field string) (string, error) {
	p, err := w.bundlePage(path)
	if err != nil {
		return "", err
	}
	want := w.ensureServer().URL + target
	for _, l := range p.Links {
		if l.URL != want {
			continue
		}
		switch field {
		case "position":
			return l.Position, nil
		case "position_path":
			return l.PositionPath, nil
		case "elem_path":
			return l.ElemPath, nil
		case "anchor":
			return l.Anchor, nil
		case "type":
			return l.Type, nil
		}
		return "", fmt.Errorf("unsupported bundle link field %q", field)
	}
	return "", fmt.Errorf("page %s has no link to %s (%d links)", path, want, len(p.Links))
}

func (w *world) bundleExternalPages(qualifier string) error {
	_, pages, err := w.readBundle()
	if err != nil {
		return err
	}
	var external int
	for _, p := range pages {
		if p.Scope == bundle.ScopeExternal {
			external++
		}
	}
	if qualifier == "no" && external > 0 {
		return fmt.Errorf("bundle carries %d external pages", external)
	}
	if qualifier == "an" && external == 0 {
		return fmt.Errorf("bundle carries no external page (%d pages total)", len(pages))
	}
	return nil
}

func (w *world) bundleLinksOfType(qualifier, typ string) error {
	_, pages, err := w.readBundle()
	if err != nil {
		return err
	}
	var n int
	for _, p := range pages {
		for _, l := range p.Links {
			if l.Type == typ {
				n++
			}
		}
	}
	if qualifier == "no" && n > 0 {
		return fmt.Errorf("bundle carries %d %q links", n, typ)
	}
	if qualifier == "a" && n == 0 {
		return fmt.Errorf("bundle carries no %q link", typ)
	}
	return nil
}

func (w *world) storeFileGzipContains(name, substr string) error {
	f, err := os.Open(filepath.Join(w.storeDirPath(), name))
	if err != nil {
		return err
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("%s is not a gzip stream: %w", name, err)
	}
	defer gr.Close()
	data, err := io.ReadAll(gr)
	if err != nil {
		return fmt.Errorf("reading %s: %w", name, err)
	}
	if !strings.Contains(string(data), substr) {
		return fmt.Errorf("%s does not contain %q", name, substr)
	}
	return nil
}

func (w *world) storeFilesIdentical(a, b string) error {
	da, err := os.ReadFile(filepath.Join(w.storeDirPath(), a))
	if err != nil {
		return err
	}
	db, err := os.ReadFile(filepath.Join(w.storeDirPath(), b))
	if err != nil {
		return err
	}
	if !bytes.Equal(da, db) {
		return fmt.Errorf("%s (%d bytes) and %s (%d bytes) differ", a, len(da), b, len(db))
	}
	return nil
}
