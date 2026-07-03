package sitecheck

import (
	"context"
	"fmt"

	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/robots"
)

// RobotsOptions customizes a robots.txt check.
type RobotsOptions struct {
	// TestURLs are evaluated against the file (tool mode) with TestUserAgent,
	// which defaults to the configured http.robots_user_agent token.
	TestURLs      []string
	TestUserAgent string
}

// IgnoredLine mirrors robots.IgnoredLine with JSON tags for report storage.
type IgnoredLine struct {
	Line int    `json:"line"`
	Raw  string `json:"raw"`
}

// RobotsVerdict is one tested URL's outcome (tool mode).
type RobotsVerdict struct {
	URL       string `json:"url"`
	UserAgent string `json:"user_agent"`
	Allowed   bool   `json:"allowed"`
	Line      int    `json:"line,omitempty"`
	Rule      string `json:"rule,omitempty"`
}

// RobotsReport is the robots.txt file-level audit.
type RobotsReport struct {
	Site          string          `json:"site,omitempty"`
	URL           string          `json:"url"`
	FinalURL      string          `json:"final_url,omitempty"`
	RedirectChain []string        `json:"redirect_chain,omitempty"`
	Status        int             `json:"status"`
	FetchError    string          `json:"fetch_error,omitempty"`
	Found         bool            `json:"found"`
	SizeBytes     int             `json:"size_bytes"`
	Groups        int             `json:"groups"`
	Rules         int             `json:"rules"`
	Sitemaps      []string        `json:"sitemaps,omitempty"`
	IgnoredLines  []IgnoredLine   `json:"ignored_lines,omitempty"`
	BlocksAll     bool            `json:"blocks_all"`
	BlocksAllRule string          `json:"blocks_all_rule,omitempty"`
	Verdicts      []RobotsVerdict `json:"verdicts,omitempty"`
	// Body carries the audited file (capped at the 500 KiB Google reads) so a
	// report is self-contained — the desktop tester edits it in place.
	Body string `json:"body,omitempty"`
}

// RobotsFetch is the raw retrieval outcome for a robots.txt: the terminal
// response after Google-REP hop following, however obtained.
type RobotsFetch struct {
	URL        string // canonical robots.txt URL for the site
	FinalURL   string
	Chain      []string
	Status     int
	FetchError string
	Body       []byte
}

// Found reports whether the retrieval landed on a 2xx file.
func (rf *RobotsFetch) Found() bool {
	return rf.FetchError == "" && rf.Status >= 200 && rf.Status < 300
}

// FetchRobots retrieves a site root's robots.txt with Google fetch semantics
// (up to five redirect hops — RFC 9309 / Google REP — with the terminal
// response deciding). It is shared with the crawler's robots manager so one
// crawl never fetches the same host's file twice.
func FetchRobots(ctx context.Context, client *fetch.Client, root string) *RobotsFetch {
	rf := &RobotsFetch{URL: root + "/robots.txt"}
	target := rf.URL
	var res *fetch.Result
	for hop := 0; hop <= robotsRedirectHops; hop++ {
		res = client.Fetch(ctx, target)
		if res.FetchError == "" && res.StatusCode >= 300 && res.StatusCode < 400 && res.RedirectURL != "" {
			rf.Chain = append(rf.Chain, res.RedirectURL)
			target = res.RedirectURL
			continue
		}
		break
	}
	rf.FinalURL = target
	rf.Status = res.StatusCode
	rf.FetchError = res.FetchError
	if rf.Found() {
		rf.Body = res.Body
	}
	return rf
}

// Robots fetches and audits a site's live robots.txt: file-level health and
// optional URL verdicts.
func (c *Checker) Robots(ctx context.Context, site string, opts RobotsOptions) (*RobotsReport, error) {
	root, err := siteRoot(site)
	if err != nil {
		return nil, err
	}
	return c.EvaluateRobots(root, FetchRobots(ctx, c.client, root), opts), nil
}

// EvaluateRobots builds the audit report over an already-retrieved
// robots.txt (the crawl pass reuses the robots manager's single fetch).
func (c *Checker) EvaluateRobots(root string, rf *RobotsFetch, opts RobotsOptions) *RobotsReport {
	rep := &RobotsReport{
		Site: root, URL: rf.URL, FinalURL: rf.FinalURL,
		RedirectChain: rf.Chain, Status: rf.Status, FetchError: rf.FetchError,
	}
	if rf.Found() {
		c.evalRobotsBody(rep, rf.Body, opts)
	}
	return rep
}

// EvaluateRobotsFile audits an in-hand robots.txt body — the --robots-file /
// pasted-textarea tester path. No network is touched.
func (c *Checker) EvaluateRobotsFile(body []byte, opts RobotsOptions) *RobotsReport {
	rep := &RobotsReport{URL: "robots.txt"}
	c.evalRobotsBody(rep, body, opts)
	return rep
}

func (c *Checker) evalRobotsBody(rep *RobotsReport, body []byte, opts RobotsOptions) {
	rep.Found = true
	rep.SizeBytes = len(body)
	rep.Body = string(body[:min(len(body), maxRobotsBytes)])
	f := robots.Parse(body)
	rep.Groups = len(f.Groups)
	for _, g := range f.Groups {
		rep.Rules += len(g.Rules)
	}
	rep.Sitemaps = f.Sitemaps
	for _, ig := range f.Ignored {
		rep.IgnoredLines = append(rep.IgnoredLines, IgnoredLine{Line: ig.Line, Raw: ig.Raw})
	}

	// blocks-all = the wildcard group's verdict for the root path. The literal
	// token "*" prefix-matches no named group, so it cleanly selects the
	// fallback rules — the ones that apply to any agent without its own group.
	site := rep.Site
	if site == "" {
		site = "https://example.invalid"
	}
	if v := f.Verdict("*", site+"/"); !v.Allowed {
		rep.BlocksAll = true
		rep.BlocksAllRule = fmt.Sprintf("line %d: %s", v.Rule.Line, v.Rule.Raw)
	}

	ua := opts.TestUserAgent
	if ua == "" {
		ua = c.cfg.HTTP.RobotsUserAgent
	}
	for _, u := range opts.TestURLs {
		v := f.Verdict(ua, u)
		rv := RobotsVerdict{URL: u, UserAgent: ua, Allowed: v.Allowed}
		if v.Rule != nil {
			rv.Line, rv.Rule = v.Rule.Line, v.Rule.Raw
		}
		rep.Verdicts = append(rep.Verdicts, rv)
	}
}

// Findings derives the issue occurrences (DESIGN.md §5.10). All
// attach to the robots.txt URL.
func (r *RobotsReport) Findings() []Finding {
	var out []Finding
	add := func(id, detail string) {
		out = append(out, Finding{IssueID: id, URL: r.URL, Detail: detail})
	}
	switch {
	case r.FetchError != "":
		// Unreachable robots.txt: Google initially treats the whole site as
		// disallowed — a whole-site crawl-visibility risk.
		add("robots_txt_server_error", "unreachable: "+r.FetchError)
	case r.Status >= 500:
		add("robots_txt_server_error", fmt.Sprintf("status %d", r.Status))
	case !r.Found && r.Status >= 300 && r.Status < 400:
		// The redirect budget was exhausted — Google then treats it as 404.
		add("robots_txt_missing", fmt.Sprintf("redirect chain exceeds %d hops", robotsRedirectHops))
	case !r.Found:
		add("robots_txt_missing", fmt.Sprintf("status %d", r.Status))
	default:
		if r.BlocksAll {
			add("robots_txt_blocks_all", r.BlocksAllRule)
		}
		for _, ig := range r.IgnoredLines {
			add("robots_txt_invalid_lines", fmt.Sprintf("line %d: %s", ig.Line, ig.Raw))
		}
		if r.SizeBytes > maxRobotsBytes {
			add("robots_txt_too_large", fmt.Sprintf("%d bytes (Google processes only the first %d)", r.SizeBytes, maxRobotsBytes))
		}
		if len(r.Sitemaps) == 0 {
			add("robots_txt_no_sitemap", "")
		}
	}
	return out
}
