package main

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/agentberlin/bluesnake/internal/store"
)

// BrandLogo returns a data: URL for the favicon of the given site (a seed URL
// or a bare host). The favicon is fetched from Google's favicon service the
// first time a brand is seen and cached in the registry, so later calls — and
// later app launches — serve it straight from disk. Returns "" when no logo
// could be obtained; the UI falls back to the domain's initial.
func (a *App) BrandLogo(site string) string {
	host := brandHost(site)
	if host == "" {
		return ""
	}
	if data, ct, err := store.GetBrandLogo(a.storeDir, host); err == nil && len(data) > 0 {
		return dataURL(ct, data)
	}
	data, ct := fetchFavicon(host)
	if len(data) == 0 {
		return ""
	}
	// Best-effort cache: a failed write just means we refetch next time.
	_ = store.SaveBrandLogo(a.storeDir, host, ct, data)
	return dataURL(ct, data)
}

// brandHost extracts the host from a seed URL or bare hostname, dropping a
// leading "www." so every crawl of the same site maps to one brand.
func brandHost(site string) string {
	site = strings.TrimSpace(site)
	if site == "" {
		return ""
	}
	if !strings.Contains(site, "://") {
		site = "//" + site // let url.Parse read a bare host as the authority
	}
	u, err := url.Parse(site)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

// fetchFavicon downloads host's favicon from Google's favicon service,
// returning the image bytes and content type (or nil, "" on any failure).
func fetchFavicon(host string) ([]byte, string) {
	endpoint := "https://www.google.com/s2/favicons?sz=64&domain=" + url.QueryEscape(host)
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return nil, ""
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ""
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil || len(data) == 0 {
		return nil, ""
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "image/png"
	}
	return data, ct
}

func dataURL(contentType string, data []byte) string {
	if contentType == "" {
		contentType = "image/png"
	}
	return "data:" + contentType + ";base64," + base64.StdEncoding.EncodeToString(data)
}
