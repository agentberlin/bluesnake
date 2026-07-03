package main

import (
	"os"
	"regexp"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
)

// The shared config schema (config-schema.js, rendered by both Settings and
// Crawl Setup) promises "every key is a verified dotted yaml path" — this
// test IS that verification. Each tg/num/ch/txt/lst helper call names a
// config key; every one must resolve through the config schema, so a renamed
// or mistyped key fails here instead of rendering a dead field.
func TestSettingsFieldKeysResolve(t *testing.T) {
	src, err := os.ReadFile("frontend/src/views/config-schema.js")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`\b(?:tg|num|ch|txt|lst)\("([a-z0-9_.]+)"`)
	matches := re.FindAllStringSubmatch(string(src), -1)
	if len(matches) < 40 { // sanity: the schema regex must keep matching the file
		t.Fatalf("extracted only %d keys — did the field helpers change shape?", len(matches))
	}
	cfg := config.Default()
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if seen[key] {
			t.Errorf("key %q listed twice", key)
		}
		seen[key] = true
		if _, err := cfg.Get(key); err != nil {
			t.Errorf("settings.jsx field %q does not resolve: %v", key, err)
		}
	}
}
