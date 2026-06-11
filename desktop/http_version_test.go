package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hhsecond/acrawler/internal/config"
	"github.com/hhsecond/acrawler/internal/crawler"
	"github.com/hhsecond/acrawler/internal/store"
)

func TestPageDetailHTTPVersion(t *testing.T) {
	a := testApp(t)
	st, err := store.CreateCrawl(a.storeDir, "", "https://ex.com/", "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Page(&crawler.PageRecord{
		URL: "https://ex.com/", Scope: "internal", State: crawler.StateCrawled,
		StatusCode: 200, Status: "OK", ContentType: "text/html",
		HTTPVersion: "HTTP/2.0",
	}); err != nil {
		t.Fatal(err)
	}
	id := st.ID
	st.Close()

	d, err := a.PageDetail(id, "https://ex.com/")
	if err != nil {
		t.Fatal(err)
	}
	if d.HTTPVersion != "HTTP/2.0" {
		t.Errorf("HTTPVersion = %q, want %q", d.HTTPVersion, "HTTP/2.0")
	}
	// the bridge serializes via the json tag: the frontend reads httpVersion
	js, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(js), `"httpVersion":"HTTP/2.0"`) {
		t.Errorf("PageDetail JSON missing httpVersion: %s", js)
	}
}
