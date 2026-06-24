package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadFileRoundTrip writes a real YAML file to disk, loads it, and asserts
// the merged values — covering LoadFile's success path (the bulk of its body)
// which the missing-file test never reaches.
func TestLoadFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bluesnake.yaml")
	yaml := "# My Profile\nspeed:\n  max_threads: 8\nrobots:\n  mode: ignore\nlimits:\n  max_depth: 4\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	for k, want := range map[string]string{
		"speed.max_threads": "8",
		"robots.mode":       "ignore",
		"limits.max_depth":  "4",
		"limits.max_urls":   "5000000", // untouched default survives the file merge
	} {
		if got, _ := c.Get(k); got != want {
			t.Errorf("Get(%q) = %q, want %q", k, got, want)
		}
	}
}

// TestLoadFileInvalidContent covers LoadFile's second error path: the file reads
// fine but its contents fail Load (the error is wrapped with the path).
func TestLoadFileInvalidContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("robots:\n  mode: obey\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadFile(path)
	if err == nil {
		t.Fatal("invalid config file must error")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("LoadFile error must name the path %q, got %v", path, err)
	}
	if !strings.Contains(err.Error(), "robots.mode") {
		t.Errorf("LoadFile error must carry the validation detail, got %v", err)
	}
}

// TestLoadFileMalformedYAML covers LoadFile wrapping a syntactic YAML parse error.
func TestLoadFileMalformedYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("speed:\n  max_threads: : :\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFile(path); err == nil {
		t.Fatal("malformed YAML must error")
	}
}

// TestGetSectionIsNotAValue covers Get's "asked for a section, not a leaf" error.
func TestGetSectionIsNotAValue(t *testing.T) {
	c := Default()
	for _, key := range []string{"speed", "robots", "thresholds.title"} {
		_, err := c.Get(key)
		if err == nil || !strings.Contains(err.Error(), "section") {
			t.Errorf("Get(%q) on a section: err=%v, want a 'section' error", key, err)
		}
	}
}

// TestGetDescendIntoNonStruct covers the "not addressable below" branch where a
// path tries to descend past a scalar leaf.
func TestGetDescendIntoNonStruct(t *testing.T) {
	c := Default()
	_, err := c.Get("robots.mode.extra")
	if err == nil || !strings.Contains(err.Error(), "not addressable") {
		t.Errorf("descending past a scalar: err=%v, want 'not addressable'", err)
	}
}

// TestValidateRegexAndStructBranches drives the per-slice validation branches of
// Validate that the existing YAML-driven tests don't all reach (description
// threshold pair, http.version, retry, limits.by_path, custom_js,
// custom_extraction regex, custom_search/link-position name checks), each as a
// single bad config asserting its key.
func TestValidateRegexAndStructBranches(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(c *Config)
		errLike string
	}{
		{"description min over max", func(c *Config) {
			c.Thresholds.Description.MinChars = 200
			c.Thresholds.Description.MaxChars = 100
		}, "thresholds.description"},
		{"bad http version", func(c *Config) { c.HTTP.Version = "3" }, "http.version"},
		{"negative retry", func(c *Config) { c.Advanced.Retry5xx = -1 }, "retry_5xx"},
		{"by_path needs pattern+max", func(c *Config) {
			c.Limits.ByPath = []PathLimit{{Pattern: "", Max: 0}}
		}, "by_path"},
		{"custom_js needs type", func(c *Config) {
			c.CustomJS = []CustomJS{{Name: "x", File: "f.js", Type: "bogus"}}
		}, "custom_js"},
		{"custom_js needs name+file", func(c *Config) {
			c.CustomJS = []CustomJS{{Name: "", File: "", Type: "action"}}
		}, "custom_js"},
		{"custom_extraction regex bad", func(c *Config) {
			c.CustomExtraction = []CustomExtraction{{Name: "e", Type: "regex", Expression: "[bad"}}
		}, "custom_extraction"},
		{"custom_extraction needs name", func(c *Config) {
			c.CustomExtraction = []CustomExtraction{{Name: "", Type: "css", Expression: "div"}}
		}, "custom_extraction"},
		{"custom_search needs name", func(c *Config) {
			c.CustomSearch = []CustomSearch{{Name: "", Mode: "contains", Pattern: "x"}}
		}, "custom_search"},
		{"link position needs match", func(c *Config) {
			c.LinkPositions = []LinkPosition{{Name: "x", Match: ""}}
		}, "link_positions"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := Default()
			tc.mutate(c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errLike)
			}
			if !strings.Contains(err.Error(), tc.errLike) {
				t.Fatalf("error %q must contain %q", err.Error(), tc.errLike)
			}
		})
	}
}

// TestValidateValidExtractionAndJS confirms the non-error arms of the
// custom_extraction (xpath/css) and custom_js (valid type) switches pass.
func TestValidateValidExtractionAndJS(t *testing.T) {
	c := Default()
	c.CustomExtraction = []CustomExtraction{
		{Name: "a", Type: "xpath", Expression: "//h1"},
		{Name: "b", Type: "css", Expression: "h1"},
		{Name: "c", Type: "regex", Expression: "h1"},
	}
	c.CustomJS = []CustomJS{
		{Name: "j", File: "j.js", Type: "extraction"},
		{Name: "k", File: "k.js", Type: "action"},
	}
	c.CustomSearch = []CustomSearch{{Name: "s", Mode: "not_contains", Pattern: "x"}}
	if err := c.Validate(); err != nil {
		t.Fatalf("valid extraction/js config must validate: %v", err)
	}
}

// TestFieldByYAMLTagUnknownLeaf exercises fieldByYAMLTag's not-found branch (an
// unknown nested leaf) and its found branch (a valid deep path) through Get.
func TestFieldByYAMLTagUnknownLeaf(t *testing.T) {
	c := Default()
	if _, err := c.Get("thresholds.title.no_such_field"); err == nil {
		t.Error("unknown nested leaf must error")
	}
	if got, _ := c.Get("thresholds.title.max_px"); got != "561" {
		t.Errorf("thresholds.title.max_px = %q, want 561", got)
	}
}

// TestFieldByYAMLTagSkipsUnexported forces fieldByYAMLTag to scan past
// ScopeConfig's unexported includeRE/excludeRE fields (the !IsExported continue):
// an unknown scope key visits every field of the struct, exported and not.
func TestFieldByYAMLTagSkipsUnexported(t *testing.T) {
	c := Default()
	if _, err := c.Get("scope.no_such_key"); err == nil {
		t.Error("unknown scope key must error after scanning all fields")
	}
	// a real exported scope leaf still resolves
	if got, _ := c.Get("scope.crawl_all_subdomains"); got != "false" {
		t.Errorf("scope.crawl_all_subdomains = %q, want false", got)
	}
}
