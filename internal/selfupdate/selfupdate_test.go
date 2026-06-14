package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.1.1", "0.1.0", 1},
		{"0.1.0", "0.1.1", -1},
		{"0.1.0", "0.1.0", 0},
		{"1.0.0", "0.9.9", 1},
		{"0.2.0", "0.10.0", -1}, // numeric, not lexical
		{"v0.1.1", "0.1.1", 0},  // leading v tolerated
		{"0.1.0", "0.1.0-rc1", 1},
		{"0.1.0-rc1", "0.1.0", -1},
		{"0.1.0+build5", "0.1.0", 0}, // build metadata ignored
		{"garbage", "0.1.0", -1},     // unparseable sorts low
		{"0.1.0", "garbage", 1},
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestIsDevBuild(t *testing.T) {
	for _, v := range []string{"0.0.0-dev", "0.0.0-dev+abc", "", "not-a-version"} {
		if !IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"0.1.0", "1.2.3", "v0.1.1"} {
		if IsDevBuild(v) {
			t.Errorf("IsDevBuild(%q) = true, want false", v)
		}
	}
}

func TestAssetFor(t *testing.T) {
	assets := []githubAsset{
		{Name: "bluesnake-0.2.0-darwin-universal.dmg", URL: "u-dmg"},
		{Name: "bluesnake-0.2.0-darwin-universal-app.zip", URL: "u-zip", Size: 99},
		{Name: "bluesnake-0.2.0-windows-amd64.exe", URL: "u-portable"},
		{Name: "bluesnake-0.2.0-windows-amd64-installer.exe", URL: "u-installer"},
		{Name: "bluesnake-0.2.0-linux-amd64.tar.gz", URL: "u-linux"},
		{Name: "SHA256SUMS", URL: "u-sums"},
	}
	cases := []struct {
		goos, goarch string
		wantURL      string // "" => unsupported
	}{
		{"darwin", "arm64", "u-zip"},
		{"darwin", "amd64", "u-zip"},
		{"windows", "amd64", "u-installer"}, // installer, not the portable exe
		{"windows", "arm64", "u-installer"}, // emulation
		{"linux", "amd64", ""},
	}
	for _, c := range cases {
		a, ok := assetFor(c.goos, c.goarch, assets)
		if c.wantURL == "" {
			if ok {
				t.Errorf("assetFor(%s/%s) = %q, want unsupported", c.goos, c.goarch, a.URL)
			}
			continue
		}
		if !ok || a.URL != c.wantURL {
			t.Errorf("assetFor(%s/%s) = (%q,%v), want %q", c.goos, c.goarch, a.URL, ok, c.wantURL)
		}
	}
}

func TestCheck(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ua := r.Header.Get("User-Agent"); ua == "" {
			t.Errorf("request missing User-Agent header")
		}
		fmt.Fprint(w, `{
			"tag_name": "v0.2.0",
			"body": "release notes here",
			"html_url": "https://example.test/releases/v0.2.0",
			"assets": [
				{"name": "bluesnake-0.2.0-darwin-universal-app.zip", "browser_download_url": "https://example.test/app.zip", "size": 1234},
				{"name": "bluesnake-0.2.0-windows-amd64-installer.exe", "browser_download_url": "https://example.test/installer.exe", "size": 5678},
				{"name": "SHA256SUMS", "browser_download_url": "https://example.test/SHA256SUMS"}
			]
		}`)
	}))
	defer srv.Close()
	old := apiURL
	apiURL = srv.URL
	defer func() { apiURL = old }()

	rel, isNewer, err := Check(context.Background(), "0.1.0")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !isNewer {
		t.Errorf("isNewer=false, want true (0.2.0 > 0.1.0)")
	}
	if rel.Version != "0.2.0" || rel.Notes != "release notes here" {
		t.Errorf("unexpected release: %+v", rel)
	}
	if rel.SumsURL != "https://example.test/SHA256SUMS" {
		t.Errorf("SumsURL=%q", rel.SumsURL)
	}

	// not newer when current == latest
	if _, isNewer, err := Check(context.Background(), "0.2.0"); err != nil || isNewer {
		t.Errorf("Check(current=latest): isNewer=%v err=%v, want false/nil", isNewer, err)
	}

	// dev build short-circuits before any network call
	if _, _, err := Check(context.Background(), "0.0.0-dev"); err != ErrDevBuild {
		t.Errorf("Check(dev) err=%v, want ErrDevBuild", err)
	}
}

func TestDownloadVerify(t *testing.T) {
	payload := []byte("this is the update artifact")
	sum := sha256.Sum256(payload)
	hexsum := hex.EncodeToString(sum[:])
	const name = "bluesnake-0.2.0-darwin-universal-app.zip"

	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		// realistic manifest: two spaces, plus an unrelated line
		fmt.Fprintf(w, "%s  %s\n%s  some-other-file\n", hexsum, name, hexsum)
	})
	mux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{
		Version: "0.2.0",
		Asset:   Asset{Name: name, URL: srv.URL + "/app.zip", Size: int64(len(payload))},
		SumsURL: srv.URL + "/SHA256SUMS",
	}
	dir := t.TempDir()

	var lastDone int64
	path, err := Download(context.Background(), rel, dir, func(done, total int64) { lastDone = done })
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Base(path) != name {
		t.Errorf("downloaded to %q, want basename %q", path, name)
	}
	if lastDone != int64(len(payload)) {
		t.Errorf("progress final done=%d, want %d", lastDone, len(payload))
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(payload) {
		t.Errorf("downloaded content mismatch")
	}

	// checksum mismatch => error + file removed
	rel.SumsURL = srv.URL + "/SHA256SUMS"
	badmux := http.NewServeMux()
	badmux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", "deadbeef", name)
	})
	badmux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	badsrv := httptest.NewServer(badmux)
	defer badsrv.Close()
	rel.Asset.URL = badsrv.URL + "/app.zip"
	rel.SumsURL = badsrv.URL + "/SHA256SUMS"
	dir2 := t.TempDir()
	if _, err := Download(context.Background(), rel, dir2, nil); err == nil {
		t.Errorf("expected checksum mismatch error, got nil")
	}
	if entries, _ := os.ReadDir(dir2); len(entries) != 0 {
		t.Errorf("partial download not cleaned up: %v", entries)
	}
}
