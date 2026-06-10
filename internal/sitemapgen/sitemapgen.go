// Package sitemapgen generates sitemaps.org-conformant XML sitemaps from a
// crawl: indexable 2xx internal HTML pages, auto-split at the 49,999-URL
// limit with a generated sitemap index.
package sitemapgen

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/hhsecond/acrawler/internal/crawler"
)

// Options controls generation.
type Options struct {
	MaxPerFile     int    // default 49999
	IncludeLastmod bool   // from the Last-Modified response header
	BaseName       string // default "sitemap"
	IndexBaseURL   string // URL prefix for split-file <loc> entries in the index
	IncludeNonHTML bool   // include PDFs etc. (default: HTML only)
}

// File is one generated output file.
type File struct {
	Name string
	Data []byte
}

type urlEntry struct {
	XMLName xml.Name `xml:"url"`
	Loc     string   `xml:"loc"`
	Lastmod string   `xml:"lastmod,omitempty"`
}

type urlset struct {
	XMLName xml.Name   `xml:"urlset"`
	Xmlns   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

type indexEntry struct {
	XMLName xml.Name `xml:"sitemap"`
	Loc     string   `xml:"loc"`
}

type sitemapIndex struct {
	XMLName  xml.Name     `xml:"sitemapindex"`
	Xmlns    string       `xml:"xmlns,attr"`
	Sitemaps []indexEntry `xml:"sitemap"`
}

const xmlns = "http://www.sitemaps.org/schemas/sitemap/0.9"

// Generate builds the sitemap file set from crawl results.
func Generate(pages map[string]*crawler.PageRecord, opts Options) ([]File, error) {
	if opts.MaxPerFile <= 0 {
		opts.MaxPerFile = 49999
	}
	if opts.BaseName == "" {
		opts.BaseName = "sitemap"
	}

	var entries []urlEntry
	urls := make([]string, 0, len(pages))
	for u := range pages {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	for _, u := range urls {
		rec := pages[u]
		if rec.Scope != "internal" || rec.State != crawler.StateCrawled ||
			rec.StatusCode < 200 || rec.StatusCode >= 300 || !rec.Indexable {
			continue
		}
		isHTML := strings.Contains(rec.ContentType, "text/html") ||
			strings.Contains(rec.ContentType, "application/xhtml")
		if !isHTML && !opts.IncludeNonHTML {
			continue
		}
		entry := urlEntry{Loc: u}
		if opts.IncludeLastmod && rec.Headers != nil {
			if lm := rec.Headers["Last-Modified"]; lm != "" {
				if t, err := http.ParseTime(lm); err == nil {
					entry.Lastmod = t.Format(time.DateOnly)
				}
			}
		}
		entries = append(entries, entry)
	}

	var files []File
	chunks := chunk(entries, opts.MaxPerFile)
	for i, c := range chunks {
		name := opts.BaseName + ".xml"
		if len(chunks) > 1 {
			name = fmt.Sprintf("%s-%d.xml", opts.BaseName, i+1)
		}
		data, err := marshalXML(urlset{Xmlns: xmlns, URLs: c})
		if err != nil {
			return nil, err
		}
		files = append(files, File{Name: name, Data: data})
	}
	if len(chunks) > 1 {
		idx := sitemapIndex{Xmlns: xmlns}
		for _, f := range files {
			idx.Sitemaps = append(idx.Sitemaps, indexEntry{Loc: strings.TrimSuffix(opts.IndexBaseURL, "/") + "/" + f.Name})
		}
		data, err := marshalXML(idx)
		if err != nil {
			return nil, err
		}
		files = append(files, File{Name: opts.BaseName + "-index.xml", Data: data})
	}
	return files, nil
}

func chunk(entries []urlEntry, size int) [][]urlEntry {
	var chunks [][]urlEntry
	for len(entries) > size {
		chunks = append(chunks, entries[:size])
		entries = entries[size:]
	}
	return append(chunks, entries)
}

func marshalXML(v any) ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteString(xml.Header)
	enc := xml.NewEncoder(&buf)
	enc.Indent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	buf.WriteString("\n")
	return buf.Bytes(), nil
}
