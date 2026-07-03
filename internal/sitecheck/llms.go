package sitecheck

import (
	"context"
	"net/url"

	"github.com/agentberlin/bluesnake/internal/llmstxt"
)

// LlmsFile is one fetched /llms.txt (or /llms-full.txt) with its structural
// validation outcome.
type LlmsFile struct {
	URL       string `json:"url"`
	Kind      string `json:"kind"` // llms_txt | llms_full_txt
	Status    int    `json:"status"`
	Found     bool   `json:"found"`
	Title     string `json:"title,omitempty"`
	Summary   string `json:"summary,omitempty"`
	Malformed bool   `json:"malformed,omitempty"`
}

// LlmsLink is one curated link (tool display; the crawl's link cross-checks
// need the crawl graph and stay in the analyze phase).
type LlmsLink struct {
	Section string `json:"section,omitempty"`
	Name    string `json:"name,omitempty"`
	URL     string `json:"url"`
}

// LlmsReport is the /llms.txt structural audit (llmstxt.org).
type LlmsReport struct {
	Site  string     `json:"site"`
	Files []LlmsFile `json:"files"`
	Links []LlmsLink `json:"links,omitempty"`
}

// LlmsTxt fetches and structurally validates a host's /llms.txt (and, per
// llms_txt.fetch_full, /llms-full.txt) — the standalone half of the audit
// that already runs during crawls (DESIGN.md §9, 2026-06-17).
func (c *Checker) LlmsTxt(ctx context.Context, site string) (*LlmsReport, error) {
	root, err := siteRoot(site)
	if err != nil {
		return nil, err
	}
	rep := &LlmsReport{Site: root}
	kinds := []struct{ path, kind string }{{"/llms.txt", "llms_txt"}}
	if c.cfg.LlmsTxt.FetchFull {
		kinds = append(kinds, struct{ path, kind string }{"/llms-full.txt", "llms_full_txt"})
	}
	for _, k := range kinds {
		target := root + k.path
		res := c.client.Fetch(ctx, target)
		f := LlmsFile{URL: target, Kind: k.kind, Status: res.StatusCode}
		f.Found = res.FetchError == "" && res.StatusCode == 200
		if f.Found {
			file := llmstxt.Parse(res.Body)
			f.Title, f.Summary, f.Malformed = file.Title, file.Summary, file.Malformed
			if k.kind == "llms_txt" {
				for _, l := range file.Links {
					rep.Links = append(rep.Links, LlmsLink{Section: l.Section, Name: l.Name, URL: l.URL})
				}
			}
		}
		rep.Files = append(rep.Files, f)
	}
	return rep, nil
}

// Findings derives the file-level checks — the SINGLE implementation of these
// rules: the analyze phase delegates here so the tool and the crawl can never
// disagree. Curated-link checks are not derivable from the files alone and
// stay in analyze.
func (r *LlmsReport) Findings() []Finding {
	type group struct{ primary, full *LlmsFile }
	hostOf := func(u string) string {
		if p, err := url.Parse(u); err == nil {
			return p.Host
		}
		return u
	}
	byHost := map[string]*group{}
	for i := range r.Files {
		f := &r.Files[i]
		g := byHost[hostOf(f.URL)]
		if g == nil {
			g = &group{}
			byHost[hostOf(f.URL)] = g
		}
		switch f.Kind {
		case "llms_txt":
			g.primary = f
		case "llms_full_txt":
			g.full = f
		}
	}
	var out []Finding
	add := func(url, id, detail string) {
		out = append(out, Finding{IssueID: id, URL: url, Detail: detail})
	}
	for _, g := range byHost {
		if g.primary == nil {
			continue
		}
		if !g.primary.Found {
			add(g.primary.URL, "llms_txt_missing", "")
			continue // nothing else to validate when the file is absent
		}
		if g.primary.Title == "" {
			add(g.primary.URL, "llms_txt_invalid_format", "missing H1 title")
		}
		if g.primary.Summary == "" {
			add(g.primary.URL, "llms_txt_missing_summary", "")
		}
		if g.primary.Malformed {
			add(g.primary.URL, "llms_txt_malformed_link_list", "")
		}
		if g.full != nil && !g.full.Found {
			add(g.full.URL, "llms_full_txt_missing", "")
		}
	}
	return out
}
