package export

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/store"
)

// pathStore seeds a crawl whose discovered_from edges form a straight chain
// (/ -> /a -> /b), a two-page cycle, and a page whose recorded parent was
// never stored — the shapes the crawl_paths walker must survive.
func pathStore(t *testing.T) *store.Crawl {
	t.Helper()
	st, err := store.CreateCrawl(t.TempDir(), "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	recs := []*crawler.PageRecord{
		{URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: ""},
		{URL: "https://ex.com/a", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: "https://ex.com/"},
		{URL: "https://ex.com/b", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: "https://ex.com/a"},
		{URL: "https://ex.com/loop", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: "https://ex.com/loop2"},
		{URL: "https://ex.com/loop2", Scope: "internal", State: crawler.StateCrawled,
			StatusCode: 200, DiscoveredFrom: "https://ex.com/loop"},
		// external scope and error state: crawl_paths covers every stored page
		{URL: "https://ex.com/orphanly", Scope: "external", State: crawler.StateError,
			DiscoveredFrom: "https://ex.com/ghost"},
	}
	for _, r := range recs {
		if err := st.Page(r); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func pathRowOf(t *testing.T, d *Dataset, url string) []string {
	t.Helper()
	for _, row := range d.Rows {
		if len(row) > 0 && row[0] == url {
			if len(row) != 3 {
				t.Fatalf("row for %s has %d columns, want 3: %v", url, len(row), row)
			}
			return row
		}
	}
	t.Fatalf("no crawl_paths row for %s in %+v", url, d.Rows)
	return nil
}

func TestCrawlPathsListed(t *testing.T) {
	if !slices.Contains(Reports(), "crawl_paths") {
		t.Errorf("Reports() = %v, missing crawl_paths", Reports())
	}
}

func TestCrawlPathsReport(t *testing.T) {
	st := pathStore(t)

	d, err := BuildReport(st, "crawl_paths")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "crawl_paths" {
		t.Errorf("name = %q, want crawl_paths", d.Name)
	}
	if want := []string{"url", "hops", "path"}; !slices.Equal(d.Header, want) {
		t.Errorf("header = %v, want %v", d.Header, want)
	}
	if len(d.Rows) != 6 {
		t.Errorf("rows = %d, want 6 (one per stored page, internal and external, any state)", len(d.Rows))
	}
	urls := make([]string, 0, len(d.Rows))
	for _, row := range d.Rows {
		urls = append(urls, row[0])
	}
	if !slices.IsSorted(urls) {
		t.Errorf("rows not sorted by url: %v", urls)
	}

	seed := pathRowOf(t, d, "https://ex.com/")
	if seed[1] != "0" || seed[2] != "https://ex.com/" {
		t.Errorf("seed row = %v, want hops 0 and path = its own url", seed)
	}

	b := pathRowOf(t, d, "https://ex.com/b")
	if b[1] != "2" {
		t.Errorf("/b hops = %q, want 2", b[1])
	}
	if want := "https://ex.com/ -> https://ex.com/a -> https://ex.com/b"; b[2] != want {
		t.Errorf("/b path = %q, want %q", b[2], want)
	}

	// the loop pages reference each other; BuildReport returning at all proves
	// the walk terminated — hops just has to stay within the 25-hop guard
	for _, u := range []string{"https://ex.com/loop", "https://ex.com/loop2"} {
		row := pathRowOf(t, d, u)
		hops, err := strconv.Atoi(row[1])
		if err != nil || hops < 0 || hops > 25 {
			t.Errorf("%s hops = %q, want a finite count in 0..25", u, row[1])
		}
		if !strings.HasSuffix(row[2], u) {
			t.Errorf("%s path = %q, must end with the url itself", u, row[2])
		}
	}

	orphan := pathRowOf(t, d, "https://ex.com/orphanly")
	if orphan[1] != "1" {
		t.Errorf("orphanly hops = %q, want 1 (one known edge to an unstored parent)", orphan[1])
	}
	parts := strings.Split(orphan[2], " -> ")
	if len(parts) != 2 || parts[0] != "https://ex.com/ghost" || parts[1] != "https://ex.com/orphanly" {
		t.Errorf("orphanly path = %q, want the known parent then the url", orphan[2])
	}
}
