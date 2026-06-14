// Package selfupdate checks GitHub Releases for a newer bluesnake and downloads
// the right artifact for the running OS/arch, verified against the release's
// SHA256SUMS manifest. It is the shared, OS-agnostic core; the actual install
// (swapping a macOS .app bundle, running the Windows installer) lives in the
// desktop package, which is necessarily platform-specific. Releases are unsigned
// (see docs/PACKAGING.md), so the checksum is the only integrity guarantee — a
// missing or mismatched checksum is a hard failure, never a silent install.
package selfupdate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Overridable for tests; production points at the real repo's latest release.
var (
	apiURL         = "https://api.github.com/repos/agentberlin/bluesnake/releases/latest"
	checkClient    = &http.Client{Timeout: 15 * time.Second}
	downloadClient = &http.Client{Timeout: 10 * time.Minute}
)

const userAgent = "bluesnake-updater"

// ErrDevBuild is returned by Check for an unstamped local build (0.0.0-dev),
// which has no meaningful version to compare against.
var ErrDevBuild = errors.New("development build — updates are disabled")

// Asset is the single downloadable artifact for this platform.
type Asset struct {
	Name string
	URL  string
	Size int64
}

// Release is the latest published release, with the asset chosen for this
// platform (if any) and the URL of its checksum manifest.
type Release struct {
	Version           string // "0.2.0" (leading "v" stripped)
	Notes             string // release body (markdown)
	HTMLURL           string // human release page
	Asset             Asset  // artifact for runtime.GOOS/GOARCH; zero if unsupported
	SumsURL           string // SHA256SUMS asset URL
	PlatformSupported bool   // a desktop artifact exists for this OS/arch
}

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Body    string        `json:"body"`
	HTMLURL string        `json:"html_url"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

// IsDevBuild reports whether a version string is an unstamped local build, for
// which updates are disabled.
func IsDevBuild(v string) bool {
	_, _, ok := parseSemver(v)
	return !ok || strings.Contains(strings.ToLower(v), "dev")
}

// Check fetches the latest release and reports whether it is newer than current.
// Returns ErrDevBuild for an unstamped build. A nil error with isNewer=false
// means "up to date". rel is always returned on success (even when not newer),
// so callers can show the current-vs-latest comparison.
func Check(ctx context.Context, current string) (rel *Release, isNewer bool, err error) {
	if IsDevBuild(current) {
		return nil, false, ErrDevBuild
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", userAgent) // GitHub rejects requests without a UA
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := checkClient.Do(req)
	if err != nil {
		return nil, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
		return nil, false, errors.New("GitHub rate limit reached — try again in a little while")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("update check failed (GitHub returned HTTP %d)", resp.StatusCode)
	}
	var gr githubRelease
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&gr); err != nil {
		return nil, false, fmt.Errorf("couldn't parse the release feed: %w", err)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(gr.TagName), "v")
	if latest == "" {
		return nil, false, errors.New("latest release has no version tag")
	}
	rel = &Release{Version: latest, Notes: strings.TrimSpace(gr.Body), HTMLURL: gr.HTMLURL}
	for _, a := range gr.Assets {
		if a.Name == "SHA256SUMS" {
			rel.SumsURL = a.URL
		}
	}
	if asset, ok := assetFor(runtime.GOOS, runtime.GOARCH, gr.Assets); ok {
		rel.Asset = asset
		rel.PlatformSupported = true
	}
	return rel, compareVersions(latest, current) > 0, nil
}

// assetFor picks the desktop artifact for the platform. Windows arm64 runs the
// amd64 build under emulation, so all Windows maps to the amd64 installer. Linux
// ships no desktop app, so it has no artifact.
func assetFor(goos, goarch string, assets []githubAsset) (Asset, bool) {
	var match func(name string) bool
	switch goos {
	case "darwin":
		// universal .app zip (not the .dmg — the zip extracts in place)
		match = func(n string) bool { return strings.HasSuffix(n, "-darwin-universal-app.zip") }
	case "windows":
		// NSIS installer (not the bare portable .exe) — it handles the in-use swap + elevation
		match = func(n string) bool { return strings.HasSuffix(n, "-windows-amd64-installer.exe") }
	default:
		return Asset{}, false
	}
	for _, a := range assets {
		if match(a.Name) {
			return Asset{Name: a.Name, URL: a.URL, Size: a.Size}, true
		}
	}
	return Asset{}, false
}

// Download streams the release asset into destDir and verifies it against the
// release's SHA256SUMS. Returns the downloaded file path. The partial file is
// removed on any failure (corrupt download, checksum mismatch). progress may be
// nil; when set it is called with cumulative/total bytes (total may be 0 if the
// server sends no length).
func Download(ctx context.Context, rel *Release, destDir string, progress func(done, total int64)) (string, error) {
	if rel == nil || rel.Asset.URL == "" {
		return "", errors.New("no downloadable update for this platform")
	}
	want, err := fetchSums(ctx, rel.SumsURL)
	if err != nil {
		return "", err
	}
	sum, ok := want[rel.Asset.Name]
	if !ok {
		return "", fmt.Errorf("no published checksum for %s — refusing to install unverified", rel.Asset.Name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.Asset.URL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := downloadClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed (HTTP %d)", resp.StatusCode)
	}
	total := resp.ContentLength
	if total <= 0 {
		total = rel.Asset.Size
	}

	out := filepath.Join(destDir, rel.Asset.Name)
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	pr := &progressReader{r: resp.Body, total: total, cb: progress}
	if _, err := io.Copy(io.MultiWriter(f, h), pr); err != nil {
		f.Close()
		os.Remove(out)
		return "", fmt.Errorf("download interrupted: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(out)
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		os.Remove(out)
		return "", fmt.Errorf("checksum mismatch for %s — the download may be corrupt", rel.Asset.Name)
	}
	return out, nil
}

// fetchSums downloads and parses a "<sha256>␠␠<filename>" manifest into a
// name->sha map (the filename may carry a leading '*' binary-mode marker).
func fetchSums(ctx context.Context, url string) (map[string]string, error) {
	if url == "" {
		return nil, errors.New("release has no SHA256SUMS — refusing to install unverified")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := checkClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't fetch checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("couldn't fetch checksums (HTTP %d)", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[len(fields)-1], "*")
		m[name] = fields[0]
	}
	return m, nil
}

type progressReader struct {
	r     io.Reader
	total int64
	done  int64
	cb    func(done, total int64)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	p.done += int64(n)
	if p.cb != nil {
		p.cb(p.done, p.total)
	}
	return n, err
}

// parseSemver splits "X.Y.Z[-pre][+build]" (optional leading "v"). ok is false
// for anything that isn't three numeric components.
func parseSemver(v string) (nums [3]int, pre string, ok bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i] // drop build metadata
	}
	core := v
	if i := strings.IndexByte(v, '-'); i >= 0 {
		core, pre = v[:i], v[i+1:]
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return nums, pre, false
	}
	for i := 0; i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		if err != nil || n < 0 {
			return nums, pre, false
		}
		nums[i] = n
	}
	return nums, pre, true
}

// compareVersions returns -1, 0, or +1. A release outranks a pre-release of the
// same core version. Unparseable versions sort below parseable ones.
func compareVersions(a, b string) int {
	na, pa, oka := parseSemver(a)
	nb, pb, okb := parseSemver(b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return -1
	case !okb:
		return 1
	}
	for i := 0; i < 3; i++ {
		if na[i] != nb[i] {
			if na[i] < nb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa == "" && pb == "":
		return 0
	case pa == "": // a is a release, b is a pre-release
		return 1
	case pb == "":
		return -1
	case pa < pb:
		return -1
	case pa > pb:
		return 1
	}
	return 0
}
