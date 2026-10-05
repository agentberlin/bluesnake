package main

import (
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bundleLines runs `bluesnake bundle` and splits the JSONL output.
func bundleLines(t *testing.T, args ...string) (map[string]any, []map[string]any) {
	t.Helper()
	out, code := runCmd(t, args...)
	if code != 0 {
		t.Fatalf("bundle: exit %d, output:\n%s", code, out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("line 1 is not JSON: %v\n%s", err, lines[0])
	}
	var pages []map[string]any
	for _, l := range lines[1:] {
		var p map[string]any
		if err := json.Unmarshal([]byte(l), &p); err != nil {
			t.Fatalf("page line is not JSON: %v\n%s", err, l)
		}
		pages = append(pages, p)
	}
	return header, pages
}

func TestBundleCmd_HeaderCountAndPageText(t *testing.T) {
	dir, id := completedCrawl(t)
	header, pages := bundleLines(t, "bundle", id, "--store-dir", dir)

	if header["format"] != "bluesnake.pages/2" {
		t.Errorf("format = %v", header["format"])
	}
	if header["crawl_id"] != id {
		t.Errorf("crawl_id = %v, want %q", header["crawl_id"], id)
	}
	if header["bluesnake_version"] == "" || header["config_digest"] == "" {
		t.Errorf("header = %v, want version and config digest", header)
	}
	if n, ok := header["pages"].(float64); !ok || int(n) != len(pages) {
		t.Errorf("header pages = %v, but %d page lines follow", header["pages"], len(pages))
	}
	var withText int
	for _, p := range pages {
		if s, _ := p["content_text"].(string); strings.Contains(s, "leaf body") {
			withText++
		}
	}
	if withText == 0 {
		t.Errorf("no page carries its body text:\n%v", pages)
	}
}

func TestBundleCmd_OutputFileAndGzip(t *testing.T) {
	dir, id := completedCrawl(t)

	plainPath := filepath.Join(t.TempDir(), "crawl.jsonl")
	if out, code := runCmd(t, "bundle", id, "--store-dir", dir, "-o", plainPath); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	plain, err := os.ReadFile(plainPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) == 0 {
		t.Fatal("empty bundle file")
	}

	// A .gz output implies --gzip without the flag.
	gzPath := filepath.Join(t.TempDir(), "crawl.jsonl.gz")
	if out, code := runCmd(t, "bundle", id, "--store-dir", dir, "-o", gzPath); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	f, err := os.Open(gzPath)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("-o *.gz did not write a gzip stream: %v", err)
	}
	defer gr.Close()
	got, err := io.ReadAll(gr)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plain) {
		t.Error("the gzipped file decompresses to something other than the plain bundle")
	}

	// An explicit --gzip=false overrides the extension.
	rawGz := filepath.Join(t.TempDir(), "raw.jsonl.gz")
	if out, code := runCmd(t, "bundle", id, "--store-dir", dir, "-o", rawGz, "--gzip=false"); code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	raw, err := os.ReadFile(rawGz)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != string(plain) {
		t.Error("--gzip=false must beat the .gz extension")
	}
}

// Bundling twice produces identical bytes — the property that lets a consumer
// diff two bundles and a conversion be compared against a committed fixture.
func TestBundleCmd_Deterministic(t *testing.T) {
	dir, id := completedCrawl(t)
	first, code := runCmd(t, "bundle", id, "--store-dir", dir, "--scope", "all", "--link-types", "all")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, first)
	}
	second, code := runCmd(t, "bundle", id, "--store-dir", dir, "--scope", "all", "--link-types", "all")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, second)
	}
	if first != second {
		t.Error("two bundles of one unchanged crawl differ")
	}
}

func TestBundleCmd_Errors(t *testing.T) {
	dir, id := completedCrawl(t)
	cases := []struct {
		name string
		args []string
	}{
		{"unknown crawl", []string{"bundle", "no-such-crawl", "--store-dir", dir}},
		{"bad scope", []string{"bundle", id, "--store-dir", dir, "--scope", "iternal"}},
		{"bad link type", []string{"bundle", id, "--store-dir", dir, "--link-types", "hyperlinks"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, code := runCmd(t, tc.args...)
			if code != 2 {
				t.Errorf("exit %d, want 2 (config error), output:\n%s", code, out)
			}
		})
	}
	if out, code := runCmd(t, "bundle", "--store-dir", dir); code == 0 {
		t.Errorf("a missing crawl id must fail:\n%s", out)
	}
}

func TestBundleCmd_Help(t *testing.T) {
	out, code := runCmd(t, "bundle", "--help")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	for _, want := range []string{"--scope", "--link-types", "--gzip", "--output", "--full"} {
		if !strings.Contains(out, want) {
			t.Errorf("help missing %q:\n%s", want, out)
		}
	}
}
