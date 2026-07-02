package serve

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/store"
)

// dataset mirrors the JSON wire shape of an exported Dataset.
type dataset struct {
	Name   string     `json:"name"`
	Header []string   `json:"header"`
	Rows   [][]string `json:"rows"`
}

// crawlEntry mirrors one /api/crawls registry entry.
type crawlEntry struct {
	ID      string `json:"id"`
	Seed    string `json:"seed"`
	Mode    string `json:"mode"`
	Status  string `json:"status"`
	Crawled int    `json:"crawled"`
}

// issueEntry mirrors one /api/crawls/{id}/issues entry.
type issueEntry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Priority string `json:"priority"`
	Tab      string `json:"tab"`
	Count    int    `json:"count"`
}

func servedStore(t *testing.T) (dir, id string) {
	t.Helper()
	dir = t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	recs := []*crawler.PageRecord{
		{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, Status: "OK", ContentType: "text/html", Indexable: true,
			Facts: &parse.Facts{Titles: []string{"Home"}, Descriptions: []string{"d"},
				H1s: []string{"H"}, WordCount: 100}},
		{URL: "https://ex.com/a", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 404, Status: "Not Found", ContentType: "text/html"},
	}
	for _, r := range recs {
		if err := st.Page(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SaveIssues(nil, []issues.Occurrence{
		{URL: "https://ex.com/a", IssueID: "internal_client_error"},
		{URL: "https://ex.com/", IssueID: "security_missing_hsts"},
	}); err != nil {
		t.Fatal(err)
	}
	id = st.ID
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.SetStatus(dir, id, store.StatusCompleted, 2, 2); err != nil {
		t.Fatal(err)
	}
	return dir, id
}

func apiServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	dir, id := servedStore(t)
	srv := httptest.NewServer(Handler(dir))
	t.Cleanup(srv.Close)
	return srv, id
}

// get performs a GET, asserts the JSON content type, optionally decodes the
// body, and returns the status code.
func get(t *testing.T, srv *httptest.Server, path string, into any) int {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("GET %s Content-Type = %q, want application/json", path, ct)
	}
	if into != nil {
		if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
			t.Fatalf("GET %s: decoding body: %v", path, err)
		}
	}
	return resp.StatusCode
}

func findRow(d *dataset, col int, value string) bool {
	for _, row := range d.Rows {
		if col < len(row) && row[col] == value {
			return true
		}
	}
	return false
}

func TestListCrawls(t *testing.T) {
	srv, id := apiServer(t)
	var entries []crawlEntry
	if code := get(t, srv, "/api/crawls", &entries); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want exactly 1", entries)
	}
	e := entries[0]
	if e.ID != id || e.Seed != "https://ex.com/" ||
		e.Mode != "spider" || e.Status != store.StatusCompleted || e.Crawled != 2 {
		t.Errorf("entry = %+v", e)
	}
}

func TestDatasetsList(t *testing.T) {
	srv, id := apiServer(t)
	var names []string
	if code := get(t, srv, "/api/crawls/"+id+"/datasets", &names); code != 200 {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{"internal", "issues"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("datasets %v missing %q", names, want)
		}
	}
}

func TestDatasetEndpoint(t *testing.T) {
	srv, id := apiServer(t)

	var d dataset
	if code := get(t, srv, "/api/crawls/"+id+"/datasets/internal", &d); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if d.Name != "internal" || len(d.Header) == 0 {
		t.Errorf("dataset = %q header %v", d.Name, d.Header)
	}
	if !findRow(&d, 0, "https://ex.com/") || !findRow(&d, 0, "https://ex.com/a") {
		t.Errorf("internal rows = %+v", d.Rows)
	}

	// ?issue= narrows the dataset to affected URLs.
	var filtered dataset
	if code := get(t, srv, "/api/crawls/"+id+"/datasets/internal?issue=internal_client_error", &filtered); code != 200 {
		t.Fatalf("filtered status = %d", code)
	}
	if len(filtered.Rows) != 1 || filtered.Rows[0][0] != "https://ex.com/a" {
		t.Errorf("filtered rows = %+v", filtered.Rows)
	}
}

func TestReportsEndpoints(t *testing.T) {
	srv, id := apiServer(t)

	var names []string
	if code := get(t, srv, "/api/crawls/"+id+"/reports", &names); code != 200 {
		t.Fatalf("status = %d", code)
	}
	found := false
	for _, n := range names {
		if n == "crawl_overview" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reports %v missing crawl_overview", names)
	}

	var report dataset
	if code := get(t, srv, "/api/crawls/"+id+"/reports/crawl_overview", &report); code != 200 {
		t.Fatalf("report status = %d", code)
	}
	if !findRow(&report, 0, "total") {
		t.Errorf("crawl_overview rows = %+v", report.Rows)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	srv, id := apiServer(t)
	var d dataset
	if code := get(t, srv, "/api/crawls/"+id+"/overview", &d); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if !findRow(&d, 0, "total") {
		t.Errorf("overview rows = %+v", d.Rows)
	}
}

func TestIssuesEndpoint(t *testing.T) {
	srv, id := apiServer(t)
	var entries []issueEntry
	if code := get(t, srv, "/api/crawls/"+id+"/issues", &entries); code != 200 {
		t.Fatalf("status = %d", code)
	}
	// Only issues with count > 0, sorted by id.
	if len(entries) != 2 ||
		entries[0].ID != "internal_client_error" || entries[1].ID != "security_missing_hsts" {
		t.Fatalf("entries = %+v", entries)
	}
	def, ok := issues.Lookup("internal_client_error")
	if !ok {
		t.Fatal("catalogue lost internal_client_error")
	}
	got := entries[0]
	if got.Name != def.Name || got.Severity != string(def.Severity) ||
		got.Priority != string(def.Priority) || got.Tab != def.Tab || got.Count != 1 {
		t.Errorf("entry = %+v, want catalogue def %+v with count 1", got, def)
	}
}

func TestPageEndpoint(t *testing.T) {
	srv, id := apiServer(t)
	var rec crawler.PageRecord
	path := "/api/crawls/" + id + "/page?url=" + url.QueryEscape("https://ex.com/")
	if code := get(t, srv, path, &rec); code != 200 {
		t.Fatalf("status = %d", code)
	}
	if rec.URL != "https://ex.com/" || rec.StatusCode != 200 || !rec.Indexable {
		t.Errorf("record = %+v", rec)
	}
	if rec.Facts == nil || len(rec.Facts.Titles) != 1 || rec.Facts.Titles[0] != "Home" {
		t.Errorf("facts = %+v", rec.Facts)
	}
}

func TestErrors(t *testing.T) {
	srv, id := apiServer(t)
	tests := []struct {
		name string
		path string
		want int
	}{
		{"unknown crawl id", "/api/crawls/doesnotexist/overview", 404},
		{"unknown dataset", "/api/crawls/" + id + "/datasets/bogus", 404},
		{"unknown report", "/api/crawls/" + id + "/reports/bogus", 404},
		{"unknown page url", "/api/crawls/" + id + "/page?url=" + url.QueryEscape("https://ex.com/missing"), 404},
		{"missing url param", "/api/crawls/" + id + "/page", 400},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var body map[string]string
			if code := get(t, srv, tc.path, &body); code != tc.want {
				t.Fatalf("status = %d, want %d", code, tc.want)
			}
			if body["error"] == "" {
				t.Errorf(`body %v lacks an "error" message`, body)
			}
		})
	}
}

// A traversing crawl id must never reach a file outside the store: the mux
// path-cleans literal "../", and store.OpenCrawl rejects %2f-encoded ones.
// Either way the request must not succeed and must not leak the store path.
func TestServeNoPathTraversal(t *testing.T) {
	srv, _ := apiServer(t)
	for _, raw := range []string{
		"/api/crawls/..%2f..%2fetc/overview",
		"/api/crawls/%2e%2e%2foutside/overview",
		"/api/crawls/..%2f..%2f..%2f..%2fetc%2fpasswd/page?url=x",
	} {
		resp, err := http.Get(srv.URL + raw)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			t.Errorf("traversing request %q succeeded (200) — must be rejected", raw)
		}
		if bytes.Contains(body, []byte("/etc/passwd")) {
			t.Errorf("response for %q reflects a traversal target: %s", raw, body)
		}
	}
}

// Error bodies must not disclose the store directory (the API can be bound
// off-localhost via --addr).
func TestServeErrorsDoNotLeakStorePath(t *testing.T) {
	srv, _ := apiServer(t)
	var nf map[string]string
	get(t, srv, "/api/crawls/nope/overview", &nf)
	if strings.ContainsAny(nf["error"], "/\\") {
		t.Errorf("404 body leaks a filesystem path: %q", nf["error"])
	}
}

func TestMethodNotAllowed(t *testing.T) {
	srv, _ := apiServer(t)
	resp, err := http.Post(srv.URL+"/api/crawls", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/crawls = %d, want 405", resp.StatusCode)
	}
}
