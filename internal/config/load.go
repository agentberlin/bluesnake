package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load parses YAML strictly (unknown keys are errors), merges it over the
// defaults, and validates the result.
func Load(data []byte) (*Config, error) {
	c := Default()
	if err := decodeStrict(data, c); err != nil {
		return nil, err
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadFile is Load over a file path.
func LoadFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	c, err := Load(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func decodeStrict(data []byte, into *Config) error {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Set applies a dotted-path override of the form "a.b.c=value". The value is
// parsed as YAML, so it follows the same typing rules as the config file
// (true/false, numbers, [lists]). Unknown keys and type mismatches are errors.
// Callers must run Validate after applying all overrides.
func (c *Config) Set(override string) error {
	key, value, ok := strings.Cut(override, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("config: override %q must have the form key.path=value", override)
	}
	// Build a YAML document for just this key and strict-decode it into c,
	// reusing the schema's typing and unknown-key detection.
	var doc strings.Builder
	segs := strings.Split(key, ".")
	for i, seg := range segs {
		doc.WriteString(strings.Repeat("  ", i))
		doc.WriteString(seg)
		doc.WriteString(":")
		if i < len(segs)-1 {
			doc.WriteString("\n")
		}
	}
	doc.WriteString(" ")
	doc.WriteString(value)
	doc.WriteString("\n")
	if err := decodeStrict([]byte(doc.String()), c); err != nil {
		return fmt.Errorf("override %q: %w", override, err)
	}
	return nil
}

// Get returns the effective value at a dotted yaml-tag path, formatted as a
// string. Used by `config show`, tests, and the acceptance steps.
func (c *Config) Get(key string) (string, error) {
	if key == "" {
		return "", errors.New("config: empty key")
	}
	v := reflect.ValueOf(c).Elem()
	segs := strings.Split(key, ".")
	for i, seg := range segs {
		if v.Kind() != reflect.Struct {
			return "", fmt.Errorf("config: %s is not addressable below %s", key, strings.Join(segs[:i], "."))
		}
		f, ok := fieldByYAMLTag(v, seg)
		if !ok {
			return "", fmt.Errorf("config: unknown key %q (at %q)", key, seg)
		}
		v = f
	}
	if v.Kind() == reflect.Struct {
		return "", fmt.Errorf("config: %s is a section, not a value", key)
	}
	return fmt.Sprintf("%v", v.Interface()), nil
}

func fieldByYAMLTag(v reflect.Value, tag string) (reflect.Value, bool) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(ft.Tag.Get("yaml"), ",")
		if name == tag {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

// Validate checks enums, ranges, and compiles every regex in the config.
// Error messages carry the dotted key path of the offending value.
func (c *Config) Validate() error {
	var errs []error
	bad := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}
	oneOf := func(key, val string, allowed ...string) {
		if !slices.Contains(allowed, val) {
			bad("%s: invalid value %q (want %s)", key, val, strings.Join(allowed, "|"))
		}
	}

	oneOf("mode", c.Mode, "spider", "list")
	oneOf("robots.mode", c.Robots.Mode, "respect", "ignore", "ignore-report")
	oneOf("rendering.mode", c.Rendering.Mode, "text", "javascript")
	oneOf("rendering.wait_strategy", c.Rendering.WaitStrategy, "adaptive", "fixed")
	oneOf("advanced.cookie_storage", c.Advanced.CookieStorage, "session", "persistent", "none")
	oneOf("advanced.percent_encoding", c.Advanced.PercentEncoding, "upper", "lower")
	oneOf("http.version", c.HTTP.Version, "", "1.1", "2")

	if c.Speed.MaxThreads < 1 {
		bad("speed.max_threads: must be >= 1, got %d", c.Speed.MaxThreads)
	}
	if c.Speed.MaxURLsPerSec < 0 {
		bad("speed.max_urls_per_sec: must be >= 0, got %v", c.Speed.MaxURLsPerSec)
	}
	if c.Speed.MaxGlobalThreads < 0 {
		bad("speed.max_global_threads: must be >= 0 (0 = unlimited), got %d", c.Speed.MaxGlobalThreads)
	}
	if c.Speed.MaxConcurrentCrawls < 0 {
		bad("speed.max_concurrent_crawls: must be >= 0 (0/1 = one crawl at a time), got %d", c.Speed.MaxConcurrentCrawls)
	}
	if c.Limits.MaxURLs < 1 {
		bad("limits.max_urls: must be >= 1, got %d", c.Limits.MaxURLs)
	}
	if c.Advanced.ResponseTimeoutSec < 1 {
		bad("advanced.response_timeout_sec: must be >= 1, got %d", c.Advanced.ResponseTimeoutSec)
	}
	if c.Rendering.AjaxTimeoutSec < 1 {
		bad("rendering.ajax_timeout_sec: must be >= 1, got %d", c.Rendering.AjaxTimeoutSec)
	}
	if c.Rendering.MaxGlobalRenders < 0 {
		bad("rendering.max_global_renders: must be >= 0 (0 = auto, cores-scaled), got %d", c.Rendering.MaxGlobalRenders)
	}
	if c.Advanced.Retry5xx < 0 {
		bad("advanced.retry_5xx: must be >= 0, got %d", c.Advanced.Retry5xx)
	}
	if t := c.Content.NearDuplicates.Threshold; t < 0 || t > 100 {
		bad("content.near_duplicates.threshold: must be between 0 and 100, got %d", t)
	}
	if t := c.Thresholds.Title; t.MinChars > t.MaxChars || t.MinPx > t.MaxPx {
		bad("thresholds.title: min must not exceed max")
	}
	if d := c.Thresholds.Description; d.MinChars > d.MaxChars || d.MinPx > d.MaxPx {
		bad("thresholds.description: min must not exceed max")
	}

	c.Scope.includeRE = compileAll("scope.include", c.Scope.Include, bad)
	c.Scope.excludeRE = compileAll("scope.exclude", c.Scope.Exclude, bad)
	for i, rr := range c.URLRewriting.RegexReplace {
		if _, err := regexp.Compile(rr.Pattern); err != nil {
			bad("url_rewriting.regex_replace[%d]: %v", i, err)
		}
	}
	for i, m := range c.Compare.URLMapping {
		if _, err := regexp.Compile(m.Pattern); err != nil {
			bad("compare.url_mapping[%d]: %v", i, err)
		}
	}
	for i, p := range c.Limits.ByPath {
		if p.Pattern == "" || p.Max < 1 {
			bad("limits.by_path[%d]: needs a pattern and max >= 1", i)
		}
	}
	for i, lp := range c.LinkPositions {
		if lp.Name == "" || lp.Match == "" {
			bad("link_positions[%d]: name and match are required", i)
		}
	}
	for i, cs := range c.CustomSearch {
		if cs.Name == "" {
			bad("custom_search[%d]: name is required", i)
		}
		if cs.Mode != "contains" && cs.Mode != "not_contains" {
			bad("custom_search[%d].mode: invalid value %q (want contains|not_contains)", i, cs.Mode)
		}
		if cs.Regex {
			if _, err := regexp.Compile(cs.Pattern); err != nil {
				bad("custom_search[%d].pattern: %v", i, err)
			}
		}
	}
	for i, ce := range c.CustomExtraction {
		if ce.Name == "" {
			bad("custom_extraction[%d]: name is required", i)
		}
		switch ce.Type {
		case "xpath", "css":
		case "regex":
			if _, err := regexp.Compile(ce.Expression); err != nil {
				bad("custom_extraction[%d].expression: %v", i, err)
			}
		default:
			bad("custom_extraction[%d].type: invalid value %q (want xpath|css|regex)", i, ce.Type)
		}
	}
	for i, cj := range c.CustomJS {
		if cj.Name == "" || cj.File == "" {
			bad("custom_js[%d]: name and file are required", i)
		}
		if cj.Type != "extraction" && cj.Type != "action" {
			bad("custom_js[%d].type: invalid value %q (want extraction|action)", i, cj.Type)
		}
	}

	return errors.Join(errs...)
}

func compileAll(key string, patterns []string, bad func(string, ...any)) []*regexp.Regexp {
	res := make([]*regexp.Regexp, 0, len(patterns))
	for i, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			bad("%s[%d]: %v", key, i, err)
			continue
		}
		res = append(res, re)
	}
	return res
}
