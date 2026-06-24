package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withAPIURL points the package's GitHub endpoint at a test server for the
// duration of fn, restoring it afterward.
func withAPIURL(t *testing.T, url string, fn func()) {
	t.Helper()
	old := apiURL
	apiURL = url
	defer func() { apiURL = old }()
	fn()
}

// TestParseSemverTable exercises every branch of parseSemver directly: valid
// cores, leading v, prerelease + build metadata, and the malformed cases (wrong
// component count, non-numeric, negative).
func TestParseSemverTable(t *testing.T) {
	cases := []struct {
		in      string
		wantNum [3]int
		wantPre string
		ok      bool
	}{
		{"1.2.3", [3]int{1, 2, 3}, "", true},
		{"v0.10.0", [3]int{0, 10, 0}, "", true},
		{"  0.1.0  ", [3]int{0, 1, 0}, "", true},
		{"1.2.3-rc1", [3]int{1, 2, 3}, "rc1", true},
		{"1.2.3+build9", [3]int{1, 2, 3}, "", true},
		{"1.2.3-rc1+build9", [3]int{1, 2, 3}, "rc1", true},
		{"1.2", [3]int{}, "", false},          // too few components
		{"1.2.3.4", [3]int{}, "", false},      // too many components
		{"1.x.3", [3]int{}, "", false},        // non-numeric
		{"-1.2.3", [3]int{}, "", false},       // negative component
		{"", [3]int{}, "", false},             // empty
		{"abc", [3]int{}, "", false},          // garbage
		{"1.2.3-", [3]int{1, 2, 3}, "", true}, // trailing dash, empty prerelease
	}
	for _, c := range cases {
		nums, pre, ok := parseSemver(c.in)
		if ok != c.ok {
			t.Errorf("parseSemver(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if !ok {
			continue
		}
		if nums != c.wantNum || pre != c.wantPre {
			t.Errorf("parseSemver(%q) = (%v, %q), want (%v, %q)", c.in, nums, pre, c.wantNum, c.wantPre)
		}
	}
}

// TestComparePrereleaseOrdering covers compareVersions's prerelease tie-break
// arms (pa<pb, pa>pb, equal pre) that the existing test only partially reaches.
func TestComparePrereleaseOrdering(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0-alpha", "1.0.0-beta", -1}, // alpha < beta lexically
		{"1.0.0-beta", "1.0.0-alpha", 1},
		{"1.0.0-rc1", "1.0.0-rc1", 0}, // identical prerelease
		{"garbage", "alsobad", 0},     // both unparseable -> 0
	}
	for _, c := range cases {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q,%q)=%d, want %d", c.a, c.b, got, c.want)
		}
	}
}

// TestAssetForUnknownArch confirms assetFor on darwin with no matching artifact
// (only a .dmg present) reports unsupported — the "loop finds nothing" arm.
func TestAssetForNoMatchingArtifact(t *testing.T) {
	assets := []githubAsset{
		{Name: "bluesnake-0.2.0-darwin-universal.dmg", URL: "u-dmg"},
		{Name: "SHA256SUMS", URL: "u-sums"},
	}
	if a, ok := assetFor("darwin", "arm64", assets); ok {
		t.Errorf("assetFor with only a .dmg should be unsupported, got %q", a.URL)
	}
}

// TestCheckRateLimited covers Check's GitHub rate-limit branch (403 + remaining 0).
func TestCheckRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	withAPIURL(t, srv.URL, func() {
		_, _, err := Check(context.Background(), "0.1.0")
		if err == nil || !strings.Contains(err.Error(), "rate limit") {
			t.Errorf("Check rate-limited err=%v, want a rate-limit error", err)
		}
	})
}

// TestCheckNon200 covers Check's generic non-OK status branch (e.g. 500).
func TestCheckNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	withAPIURL(t, srv.URL, func() {
		if _, _, err := Check(context.Background(), "0.1.0"); err == nil {
			t.Error("Check on HTTP 500 should error")
		}
	})
}

// TestCheckBadJSON covers Check's JSON-decode error branch.
func TestCheckBadJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("not json at all"))
	}))
	defer srv.Close()
	withAPIURL(t, srv.URL, func() {
		_, _, err := Check(context.Background(), "0.1.0")
		if err == nil || !strings.Contains(err.Error(), "release feed") {
			t.Errorf("Check on bad JSON err=%v, want a parse error", err)
		}
	})
}

// TestCheckEmptyTag covers Check's "no version tag" branch.
func TestCheckEmptyTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name": "", "assets": []}`))
	}))
	defer srv.Close()
	withAPIURL(t, srv.URL, func() {
		_, _, err := Check(context.Background(), "0.1.0")
		if err == nil || !strings.Contains(err.Error(), "no version tag") {
			t.Errorf("Check empty tag err=%v, want a no-tag error", err)
		}
	})
}

// TestCheckRequestBuildError covers the http.NewRequestWithContext error arm by
// pointing apiURL at a control character the URL parser rejects.
func TestCheckRequestBuildError(t *testing.T) {
	withAPIURL(t, "http://\x7f/bad", func() {
		if _, _, err := Check(context.Background(), "0.1.0"); err == nil {
			t.Error("Check with an unparseable apiURL should error")
		}
	})
}

// TestDownloadNoAsset covers Download's nil-rel / empty-asset guard.
func TestDownloadNoAsset(t *testing.T) {
	if _, err := Download(context.Background(), nil, t.TempDir(), nil); err == nil {
		t.Error("Download(nil) should error")
	}
	if _, err := Download(context.Background(), &Release{}, t.TempDir(), nil); err == nil {
		t.Error("Download with an empty asset should error")
	}
}

// TestDownloadNoSumsURL covers fetchSums's empty-URL guard surfaced through Download.
func TestDownloadNoSumsURL(t *testing.T) {
	rel := &Release{Asset: Asset{Name: "x", URL: "http://example.test/x"}, SumsURL: ""}
	_, err := Download(context.Background(), rel, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "SHA256SUMS") {
		t.Errorf("Download with no SumsURL err=%v, want a SHA256SUMS error", err)
	}
}

// TestDownloadSumsHTTPError covers fetchSums's non-OK status branch.
func TestDownloadSumsHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	rel := &Release{Asset: Asset{Name: "x", URL: srv.URL + "/x"}, SumsURL: srv.URL + "/SHA256SUMS"}
	_, err := Download(context.Background(), rel, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "checksums") {
		t.Errorf("Download with 404 checksums err=%v, want a checksum-fetch error", err)
	}
}

// TestDownloadMissingChecksum covers Download's "asset not in the manifest" arm:
// the sums file is fetched fine but lists a different filename.
func TestDownloadMissingChecksum(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc123  some-other-file\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rel := &Release{
		Asset:   Asset{Name: "bluesnake-app.zip", URL: srv.URL + "/app.zip"},
		SumsURL: srv.URL + "/SHA256SUMS",
	}
	_, err := Download(context.Background(), rel, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "no published checksum") {
		t.Errorf("Download with a missing checksum err=%v, want a no-checksum error", err)
	}
}

// TestDownloadAssetHTTPError covers Download's non-OK asset-download branch (the
// checksum is present, but the artifact GET 404s).
func TestDownloadAssetHTTPError(t *testing.T) {
	const name = "bluesnake-app.zip"
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		// a real sha so the lookup succeeds and we reach the download
		w.Write([]byte("0000000000000000000000000000000000000000000000000000000000000000  " + name + "\n"))
	})
	mux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	rel := &Release{
		Asset:   Asset{Name: name, URL: srv.URL + "/app.zip"},
		SumsURL: srv.URL + "/SHA256SUMS",
	}
	_, err := Download(context.Background(), rel, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "download failed") {
		t.Errorf("Download with a 503 asset err=%v, want a download-failed error", err)
	}
	if entries, _ := os.ReadDir(t.TempDir()); len(entries) != 0 {
		t.Errorf("nothing should have been written, got %v", entries)
	}
}

// goodSums writes a SHA256SUMS handler for `name` with the given hex sum.
func sumsHandler(hexsum, name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(hexsum + "  " + name + "\n"))
	}
}

// TestCheckNetworkError covers Check's checkClient.Do error arm by pointing at a
// server that's already closed (connection refused).
func TestCheckNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing listening now
	withAPIURL(t, url, func() {
		if _, _, err := Check(context.Background(), "0.1.0"); err == nil {
			t.Error("Check against a dead server should error")
		}
	})
}

// TestDownloadAssetNetworkError covers Download's downloadClient.Do error arm:
// the sums fetch succeeds but the asset host is dead.
func TestDownloadAssetNetworkError(t *testing.T) {
	const name = "bluesnake-app.zip"
	asset := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	assetURL := asset.URL + "/app.zip"
	asset.Close()

	sums := httptest.NewServer(sumsHandler("00", name))
	defer sums.Close()

	rel := &Release{Asset: Asset{Name: name, URL: assetURL}, SumsURL: sums.URL + "/SHA256SUMS"}
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err == nil {
		t.Error("Download against a dead asset host should error")
	}
}

// TestFetchSumsNetworkError covers fetchSums's checkClient.Do error arm (dead
// sums host).
func TestFetchSumsNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	sumsURL := srv.URL + "/SHA256SUMS"
	srv.Close()
	rel := &Release{Asset: Asset{Name: "x", URL: "http://example.test/x"}, SumsURL: sumsURL}
	_, err := Download(context.Background(), rel, t.TempDir(), nil)
	if err == nil || !strings.Contains(err.Error(), "fetch checksums") {
		t.Errorf("Download with a dead sums host err=%v, want a fetch-checksums error", err)
	}
}

// TestDownloadAssetRequestBuildError covers Download's NewRequestWithContext
// error arm for the asset URL: the sums fetch succeeds (the checksum is present)
// but the asset URL has a control char the request builder rejects.
func TestDownloadAssetRequestBuildError(t *testing.T) {
	const name = "app.zip"
	srv := httptest.NewServer(sumsHandler("00", name))
	defer srv.Close()
	rel := &Release{
		Asset:   Asset{Name: name, URL: "http://example.test/\x7f"},
		SumsURL: srv.URL + "/SHA256SUMS",
	}
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err == nil {
		t.Error("Download with an unparseable asset URL should error at request build")
	}
}

// TestFetchSumsRequestBuildError covers fetchSums's NewRequestWithContext error
// arm via a SumsURL containing a control char.
func TestFetchSumsRequestBuildError(t *testing.T) {
	rel := &Release{
		Asset:   Asset{Name: "x", URL: "http://example.test/x"},
		SumsURL: "http://example.test/\x7f",
	}
	if _, err := Download(context.Background(), rel, t.TempDir(), nil); err == nil {
		t.Error("Download with an unparseable SumsURL should error at request build")
	}
}

// TestDownloadCreateError covers Download's os.Create error arm by handing it a
// destDir that doesn't exist (Create on a path under a missing parent fails).
func TestDownloadCreateError(t *testing.T) {
	payload := []byte("artifact")
	sum := sha256.Sum256(payload)
	const name = "app.zip"
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", sumsHandler(hex.EncodeToString(sum[:]), name))
	mux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rel := &Release{Asset: Asset{Name: name, URL: srv.URL + "/app.zip"}, SumsURL: srv.URL + "/SHA256SUMS"}
	missingDir := filepath.Join(t.TempDir(), "does", "not", "exist")
	if _, err := Download(context.Background(), rel, missingDir, nil); err == nil {
		t.Error("Download into a missing destDir should error at os.Create")
	}
}

// TestDownloadInterrupted covers Download's io.Copy error arm: the server sends a
// large Content-Length then closes the connection mid-body, so the copy fails and
// the partial file is removed.
func TestDownloadInterrupted(t *testing.T) {
	const name = "app.zip"
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", sumsHandler("00", name))
	mux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000") // promise far more than we send
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("short"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// returning here closes the connection before the promised length
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	rel := &Release{Asset: Asset{Name: name, URL: srv.URL + "/app.zip"}, SumsURL: srv.URL + "/SHA256SUMS"}
	if _, err := Download(context.Background(), rel, dir, nil); err == nil {
		t.Error("a truncated download should error")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("partial download not cleaned up: %v", entries)
	}
}

// TestDownloadNoContentLengthFallsBackToAssetSize covers the total<=0 fallback:
// a chunked response has no Content-Length, so Download uses rel.Asset.Size as
// the progress total, and still completes + verifies.
func TestDownloadNoContentLengthFallsBackToAssetSize(t *testing.T) {
	payload := []byte("the real artifact bytes")
	sum := sha256.Sum256(payload)
	const name = "app.zip"
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", sumsHandler(hex.EncodeToString(sum[:]), name))
	mux.HandleFunc("/app.zip", func(w http.ResponseWriter, r *http.Request) {
		// no Content-Length: write via a flusher so the response is chunked
		f, _ := w.(http.Flusher)
		w.Write(payload)
		if f != nil {
			f.Flush()
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	dir := t.TempDir()
	rel := &Release{
		Asset:   Asset{Name: name, URL: srv.URL + "/app.zip", Size: int64(len(payload))},
		SumsURL: srv.URL + "/SHA256SUMS",
	}
	var sawTotal int64
	path, err := Download(context.Background(), rel, dir, func(done, total int64) { sawTotal = total })
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if filepath.Base(path) != name {
		t.Errorf("downloaded to %q, want basename %q", path, name)
	}
	if sawTotal != int64(len(payload)) {
		t.Errorf("progress total = %d, want fallback to Asset.Size %d", sawTotal, len(payload))
	}
}
