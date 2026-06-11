package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStoreWARCDefault(t *testing.T) {
	c := Default()
	if c.Extraction.StoreWARC {
		t.Error("Extraction.StoreWARC must default to false")
	}
	got, err := c.Get("extraction.store_warc")
	if err != nil {
		t.Fatalf("Get(extraction.store_warc): %v", err)
	}
	if got != "false" {
		t.Errorf("extraction.store_warc default = %q, want false", got)
	}
}

func TestStoreWARCYAMLLoad(t *testing.T) {
	c, err := Load([]byte("extraction:\n  store_warc: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Extraction.StoreWARC {
		t.Error("Extraction.StoreWARC not set from yaml")
	}
	if got, _ := c.Get("extraction.store_warc"); got != "true" {
		t.Errorf("extraction.store_warc = %q, want true", got)
	}
	// sibling defaults in the same block must survive the partial merge
	if got, _ := c.Get("extraction.store_html"); got != "false" {
		t.Errorf("extraction.store_html = %q, want false (default preserved)", got)
	}
}

func TestStoreWARCSetOverride(t *testing.T) {
	c := Default()
	if err := c.Set("extraction.store_warc=true"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if !c.Extraction.StoreWARC {
		t.Error("Extraction.StoreWARC not set by override")
	}
	if got, _ := c.Get("extraction.store_warc"); got != "true" {
		t.Errorf("after Set, Get = %q, want true", got)
	}
}

func TestStoreWARCRoundTrip(t *testing.T) {
	c := Default()
	if err := c.Set("extraction.store_warc=true"); err != nil {
		t.Fatal(err)
	}
	data, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Load(data)
	if err != nil {
		t.Fatalf("marshalled config must load: %v\n---\n%s", err, data)
	}
	if got, _ := c2.Get("extraction.store_warc"); got != "true" {
		t.Errorf("round-trip changed extraction.store_warc: %q", got)
	}
}
