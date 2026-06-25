package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestDefaults(t *testing.T) {
	c := Default()
	tests := []struct {
		key  string
		want string
	}{
		{"mode", "spider"},
		{"speed.max_threads", "5"},
		{"speed.max_urls_per_sec", "0"},
		{"limits.max_urls", "5000000"},
		{"limits.max_depth", "-1"},
		{"limits.max_redirects", "5"},
		{"limits.max_url_length", "10000"},
		{"limits.max_links_per_page", "10000"},
		{"limits.max_page_size_kb", "51200"},
		{"robots.mode", "respect"},
		{"robots.show_blocked_internal", "true"},
		{"robots.show_blocked_external", "false"},
		{"rendering.mode", "text"},
		{"rendering.ajax_timeout_sec", "5"},
		{"advanced.cookie_storage", "session"},
		{"advanced.respect_hsts", "true"},
		{"advanced.respect_self_referencing_meta_refresh", "true"},
		{"advanced.response_timeout_sec", "20"},
		{"advanced.retry_5xx", "0"},
		{"advanced.percent_encoding", "upper"},
		{"advanced.crawl_fragments", "false"},
		{"thresholds.title.min_chars", "30"},
		{"thresholds.title.max_chars", "60"},
		{"thresholds.title.min_px", "200"},
		{"thresholds.title.max_px", "561"},
		{"thresholds.description.min_chars", "70"},
		{"thresholds.description.max_chars", "155"},
		{"thresholds.url_max_chars", "115"},
		{"thresholds.h1_max_chars", "70"},
		{"thresholds.h2_max_chars", "70"},
		{"thresholds.image_alt_max_chars", "100"},
		{"thresholds.image_max_kb", "100"},
		{"thresholds.low_content_words", "200"},
		{"thresholds.high_crawl_depth", "4"},
		{"thresholds.high_internal_outlinks", "1000"},
		{"thresholds.high_external_outlinks", "100"},
		{"content.near_duplicates.enabled", "false"},
		{"content.near_duplicates.threshold", "90"},
		{"content.near_duplicates.indexable_only", "true"},
		{"resources.images.store", "false"},
		{"resources.images.crawl", "false"},
		{"resources.javascript.crawl", "false"},
		{"links.internal.store", "true"},
		{"links.internal.crawl", "true"},
		{"links.external.crawl", "false"},
		{"links.canonicals.store", "true"},
		{"links.canonicals.crawl", "false"},
		{"links.pagination.store", "false"},
		{"links.pagination.crawl", "false"},
		{"links.hreflang.store", "true"},
		{"links.hreflang.crawl", "false"},
		{"links.amp.store", "false"},
		{"links.mobile_alternate.store", "false"},
		{"scope.crawl_all_subdomains", "false"},
		{"scope.check_links_outside_start_folder", "true"},
		{"extraction.page_details.titles", "true"},
		{"extraction.structured_data.jsonld", "false"},
		{"extraction.store_html", "false"},
		{"sitemaps.crawl_linked", "true"},
		{"sitemaps.auto_discover_via_robots", "true"},
		{"analysis.auto", "true"},
		{"analysis.link_score", "true"},
		{"list_mode.respect_robots", "false"},
		{"list_mode.crawl_depth", "0"},
		{"compare.content_change_threshold", "10"},
		{"store_link_paths", "true"},
	}
	for _, tt := range tests {
		got, err := c.Get(tt.key)
		if err != nil {
			t.Errorf("Get(%q): %v", tt.key, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Get(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestDefaultsValid(t *testing.T) {
	if err := Default().Validate(); err != nil {
		t.Fatalf("default config must validate: %v", err)
	}
}

func TestDefaultContentAreaExcludes(t *testing.T) {
	c := Default()
	got := c.Content.Area.ExcludeElements
	if len(got) != 2 || got[0] != "nav" || got[1] != "footer" {
		t.Errorf("default content area excludes = %v, want [nav footer]", got)
	}
}

func TestDefaultLinkPositions(t *testing.T) {
	c := Default()
	if len(c.LinkPositions) == 0 {
		t.Fatal("default link positions must not be empty")
	}
	last := c.LinkPositions[len(c.LinkPositions)-1]
	if last.Name != "content" || last.Match != "/" {
		t.Errorf("last link position must be the content catch-all, got %+v", last)
	}
}

func TestLoadEmpty(t *testing.T) {
	c, err := Load(nil)
	if err != nil {
		t.Fatalf("empty config must load: %v", err)
	}
	if got, _ := c.Get("speed.max_threads"); got != "5" {
		t.Errorf("empty config must equal defaults, max_threads = %s", got)
	}
}

func TestLoadMergesOverDefaults(t *testing.T) {
	c, err := Load([]byte("speed:\n  max_threads: 12\nlimits:\n  max_depth: 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"speed.max_threads": "12",
		"limits.max_depth":  "3",
		"limits.max_urls":   "5000000", // untouched default
		"robots.mode":       "respect",
	} {
		if got, _ := c.Get(k); got != want {
			t.Errorf("Get(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestLoadPartialNestedMerge(t *testing.T) {
	// setting only `store` must keep the default for `crawl` in the same block
	c, err := Load([]byte("links:\n  pagination:\n    store: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Get("links.pagination.store"); got != "true" {
		t.Errorf("store = %s, want true", got)
	}
	if got, _ := c.Get("links.pagination.crawl"); got != "false" {
		t.Errorf("crawl = %s, want false (default preserved)", got)
	}
}

func TestLoadUnknownKey(t *testing.T) {
	_, err := Load([]byte("sped:\n  max_threads: 12\n"))
	if err == nil || !strings.Contains(err.Error(), "sped") {
		t.Fatalf("unknown key must error mentioning the key, got: %v", err)
	}
}

func TestLoadUnknownNestedKey(t *testing.T) {
	_, err := Load([]byte("speed:\n  threads: 12\n"))
	if err == nil || !strings.Contains(err.Error(), "threads") {
		t.Fatalf("unknown nested key must error, got: %v", err)
	}
}

func TestValidateErrors(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		errLike string
	}{
		{"bad robots mode", "robots:\n  mode: obey\n", "robots.mode"},
		{"bad rendering mode", "rendering:\n  mode: chrome\n", "rendering.mode"},
		{"bad cookie storage", "advanced:\n  cookie_storage: forever\n", "cookie_storage"},
		{"bad percent encoding", "advanced:\n  percent_encoding: mixed\n", "percent_encoding"},
		{"bad mode", "mode: serp\n", "mode"},
		{"zero threads", "speed:\n  max_threads: 0\n", "max_threads"},
		{"negative threads", "speed:\n  max_threads: -2\n", "max_threads"},
		{"negative rate", "speed:\n  max_urls_per_sec: -1\n", "max_urls_per_sec"},
		{"threshold over 100", "content:\n  near_duplicates:\n    threshold: 150\n", "threshold"},
		{"threshold negative", "content:\n  near_duplicates:\n    threshold: -1\n", "threshold"},
		{"bad include regex", "scope:\n  include: [\"[bad\"]\n", "include"},
		{"bad exclude regex", "scope:\n  exclude: [\"[unclosed\"]\n", "exclude"},
		{"bad rewrite regex", "url_rewriting:\n  regex_replace:\n    - {pattern: \"[x\", replace: \"y\"}\n", "regex_replace"},
		{"bad url mapping regex", "compare:\n  url_mapping:\n    - {pattern: \"(\", replace: \"x\"}\n", "url_mapping"},
		{"title min over max", "thresholds:\n  title: {min_chars: 70, max_chars: 60}\n", "title"},
		{"max_urls zero", "limits:\n  max_urls: 0\n", "max_urls"},
		{"bad timeout", "advanced:\n  response_timeout_sec: 0\n", "response_timeout_sec"},
		{"zero ajax timeout", "rendering:\n  ajax_timeout_sec: 0\n", "ajax_timeout_sec"},
		{"empty link position name", "link_positions:\n  - {name: \"\", match: \"/\"}\n", "link_positions"},
		{"bad custom search regex", "custom_search:\n  - {name: s, mode: contains, pattern: \"[a\", regex: true}\n", "custom_search"},
		{"bad custom search mode", "custom_search:\n  - {name: s, mode: never, pattern: \"x\"}\n", "custom_search"},
		{"bad custom extraction type", "custom_extraction:\n  - {name: e, type: jsonpath, expression: \"x\"}\n", "custom_extraction"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Load([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.errLike)
			}
			if !strings.Contains(err.Error(), tt.errLike) {
				t.Fatalf("error %q must contain %q", err.Error(), tt.errLike)
			}
		})
	}
}

func TestSetOverrides(t *testing.T) {
	tests := []struct {
		set  string
		key  string
		want string
	}{
		{"speed.max_threads=3", "speed.max_threads", "3"},
		{"scope.crawl_all_subdomains=true", "scope.crawl_all_subdomains", "true"},
		{"robots.mode=ignore", "robots.mode", "ignore"},
		{"limits.max_depth=7", "limits.max_depth", "7"},
		{"http.user_agent=mybot/2.0", "http.user_agent", "mybot/2.0"},
		{"links.pagination.crawl=true", "links.pagination.crawl", "true"},
		{"speed.max_urls_per_sec=2.5", "speed.max_urls_per_sec", "2.5"},
		{`scope.exclude=["/private/"]`, "scope.exclude", "[/private/]"},
	}
	for _, tt := range tests {
		t.Run(tt.set, func(t *testing.T) {
			c := Default()
			if err := c.Set(tt.set); err != nil {
				t.Fatalf("Set(%q): %v", tt.set, err)
			}
			got, err := c.Get(tt.key)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("after Set(%q), Get(%q) = %q, want %q", tt.set, tt.key, got, tt.want)
			}
		})
	}
}

func TestSetErrors(t *testing.T) {
	tests := []string{
		"speed.threads=3",      // unknown key
		"nonsense",             // no '='
		"speed.max_threads=lo", // type mismatch
		"=5",                   // empty key
	}
	for _, s := range tests {
		c := Default()
		if err := c.Set(s); err == nil {
			t.Errorf("Set(%q) must fail", s)
		}
	}
}

func TestSetThenValidate(t *testing.T) {
	c := Default()
	if err := c.Set("speed.max_threads=0"); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(); err == nil {
		t.Error("validate must catch invalid value introduced by Set")
	}
}

func TestGetErrors(t *testing.T) {
	c := Default()
	for _, key := range []string{"nope", "speed.nope", "speed.max_threads.deeper", ""} {
		if _, err := c.Get(key); err == nil {
			t.Errorf("Get(%q) must fail", key)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	// marshalling the default config and loading it back must be a no-op
	data, err := yaml.Marshal(Default())
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(data)
	if err != nil {
		t.Fatalf("marshalled default config must load: %v\n---\n%s", err, data)
	}
	for _, key := range []string{"speed.max_threads", "robots.mode", "thresholds.title.max_px", "links.hreflang.crawl"} {
		want, _ := Default().Get(key)
		got, _ := c.Get(key)
		if got != want {
			t.Errorf("round-trip changed %s: %q -> %q", key, want, got)
		}
	}
}

func TestLoadFileMissing(t *testing.T) {
	if _, err := LoadFile("/nonexistent/bluesnake.yaml"); err == nil {
		t.Error("missing file must error")
	}
}

func TestCompiledPatterns(t *testing.T) {
	c, err := Load([]byte("scope:\n  include: [\"/blog/\"]\n  exclude: [\"\\\\?page=\"]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Scope.IncludeRE()) != 1 || len(c.Scope.ExcludeRE()) != 1 {
		t.Fatal("compiled patterns must be available after load")
	}
	if !c.Scope.ExcludeRE()[0].MatchString("https://ex.com/list?page=2") {
		t.Error("compiled exclude must match")
	}
}
