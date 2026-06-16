package export

import (
	"reflect"
	"strconv"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/parse"
	"github.com/agentberlin/bluesnake/internal/serpwidth"
	"github.com/agentberlin/bluesnake/internal/store"
)

func serpStore(t *testing.T, title, desc string) *store.Crawl {
	t.Helper()
	st, err := store.CreateCrawl(t.TempDir(), "", []string{"https://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.Page(&crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html", Indexable: true,
		IndexabilityStatus: "Indexable",
		Facts:              &parse.Facts{Titles: []string{title}, Descriptions: []string{desc}},
	}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestTitlesTabPixelWidth(t *testing.T) {
	title := "Acme Widgets and Other Fine Goods"
	st := serpStore(t, title, "a meta description for the home page")

	d, err := Build(st, "titles", "")
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"url", "title", "length", "pixel_width", "count", "indexability_status"}
	if !reflect.DeepEqual(d.Header, wantHeader) {
		t.Errorf("titles header = %v, want %v", d.Header, wantHeader)
	}
	if len(d.Rows) != 1 {
		t.Fatalf("titles rows = %d, want 1", len(d.Rows))
	}
	row := d.Rows[0]
	if row[0] != "https://ex.com/" || row[1] != title {
		t.Errorf("titles row = %v", row)
	}
	if want := strconv.Itoa(serpwidth.Title(title)); row[3] != want {
		t.Errorf("titles pixel_width cell = %q, want %q", row[3], want)
	}
}

func TestDescriptionsTabPixelWidth(t *testing.T) {
	desc := "A meta description that the exporter must measure in SERP pixels."
	st := serpStore(t, "Acme Widgets and Other Fine Goods", desc)

	d, err := Build(st, "descriptions", "")
	if err != nil {
		t.Fatal(err)
	}
	wantHeader := []string{"url", "description", "length", "pixel_width", "count", "indexability_status"}
	if !reflect.DeepEqual(d.Header, wantHeader) {
		t.Errorf("descriptions header = %v, want %v", d.Header, wantHeader)
	}
	if len(d.Rows) != 1 {
		t.Fatalf("descriptions rows = %d, want 1", len(d.Rows))
	}
	row := d.Rows[0]
	if row[0] != "https://ex.com/" || row[1] != desc {
		t.Errorf("descriptions row = %v", row)
	}
	if want := strconv.Itoa(serpwidth.Description(desc)); row[3] != want {
		t.Errorf("descriptions pixel_width cell = %q, want %q", row[3], want)
	}
}
