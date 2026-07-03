package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/issues"
	"github.com/agentberlin/bluesnake/internal/sitecheck"
	"github.com/spf13/cobra"
)

// newToolsCmd is the standalone-testers command group (DESIGN.md §5.10):
// one namespace, driven by the sitecheck registry, so the top-level command
// space stays flat no matter how many tools are added. Tool runs are
// throwaway — nothing is persisted.
func newToolsCmd() *cobra.Command {
	toolsCmd := &cobra.Command{
		Use:   "tools",
		Short: "Standalone SEO testers (no crawl required)",
		Long: "Interactive site testers that run against live URLs without a crawl.\n" +
			"`bluesnake tools list` enumerates them. The same checks run automatically\n" +
			"during full-domain crawls (site_checks config) and surface as issues.",
	}
	toolsCmd.AddCommand(newToolsListCmd(), newToolsRobotsCmd(), newToolsSitemapCmd(),
		newToolsAIBotsCmd(), newToolsRenderCmd(), newToolsLlmsCmd(),
		newToolsStructuredCmd(), newToolsSerpCmd())
	return toolsCmd
}

func newToolsStructuredCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "structured <url>",
		Short: "Validate a page's structured data against Google rich-result requirements",
		Long: "Fetches the page and validates its schema.org markup (JSON-LD, Microdata,\n" +
			"RDFa): required properties missing per rich-result feature are errors,\n" +
			"recommended ones warnings — the same engine a crawl runs per page.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.Structured(cmd.Context(), args[0])
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			switch {
			case rep.FetchError != "":
				fmt.Fprintf(out, "fetch failed: %s\n", rep.FetchError)
			case rep.FetchStatus < 200 || rep.FetchStatus >= 300:
				fmt.Fprintf(out, "fetch status %d — nothing to validate\n", rep.FetchStatus)
			case len(rep.Formats) == 0:
				fmt.Fprintln(out, "no structured data found")
			default:
				fmt.Fprintf(out, "formats  %s\n", strings.Join(rep.Formats, ", "))
				fmt.Fprintf(out, "types    %s\n", strings.Join(rep.Types, ", "))
			}
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func newToolsSerpCmd() *cobra.Command {
	var title, description, pageURL string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "serp [--title ...] [--description ...] [--url ...]",
		Short: "Preview a title/description pair as Google's results page renders it",
		Long: "Measures rendered pixel widths against the thresholds.* limits and shows\n" +
			"the truncation Google would apply. --url fetches a live page's actual\n" +
			"title and meta description; --title/--description override for editing.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.Serp(cmd.Context(), sitecheck.SerpOptions{Title: title, Description: description, URL: pageURL})
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			if rep.FetchError != "" || (rep.URL != "" && !rep.Fetched) {
				fmt.Fprintf(out, "fetch failed: %s (status %d)\n", rep.FetchError, rep.FetchStatus)
				printFindings(out, rep.Findings())
				return nil
			}
			printSerpField(out, "title", rep.Title)
			printSerpField(out, "description", rep.Description)
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().StringVar(&title, "title", "", "title text to measure")
	cmd.Flags().StringVar(&description, "description", "", "meta description text to measure")
	cmd.Flags().StringVar(&pageURL, "url", "", "page URL to fetch the actual title/description from")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func printSerpField(w io.Writer, name string, f *sitecheck.SerpField) {
	if f == nil {
		return
	}
	if f.Text == "" {
		fmt.Fprintf(w, "%-12s (none)\n", name)
		return
	}
	fmt.Fprintf(w, "%-12s %q\n", name, f.Text)
	fmt.Fprintf(w, "             %d chars (%d-%d), %dpx (%d-%dpx)\n",
		f.Chars, f.MinChars, f.MaxChars, f.Pixels, f.MinPx, f.MaxPx)
	if f.Truncated != "" {
		fmt.Fprintf(w, "             displays as %q\n", f.Truncated)
	}
}

func newToolsRenderCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "render <url>",
		Short: "Diff a page raw vs JavaScript-rendered (needs Chrome)",
		Long: "Fetches the page, renders it in headless Chrome, and diffs the two:\n" +
			"what non-rendering consumers (text crawlers, most AI bots) miss.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.RenderDiff(cmd.Context(), args[0])
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			switch {
			case rep.FetchError != "":
				fmt.Fprintf(out, "fetch failed: %s\n", rep.FetchError)
			case !rep.Rendered && rep.RenderError != "":
				fmt.Fprintf(out, "render failed: %s\n", rep.RenderError)
			case !rep.Rendered:
				fmt.Fprintf(out, "fetch status %d — nothing to diff\n", rep.FetchStatus)
			default:
				fmt.Fprintf(out, "word count   raw %d  rendered %d\n", rep.RawWordCount, rep.RenderedWordCount)
				fmt.Fprintf(out, "title        %s\n", diffCol(rep.TitleChanged, rep.RawTitle, rep.RenderedTitle))
				fmt.Fprintf(out, "canonical    %s\n", diffCol(rep.CanonicalChanged, rep.RawCanonical, rep.RenderedCanonical))
				fmt.Fprintf(out, "description  changed: %v\n", rep.DescriptionChanged)
				fmt.Fprintf(out, "h1           changed: %v\n", rep.H1Changed)
				if rep.NoindexOnlyRaw {
					fmt.Fprintln(out, "noindex      present in raw HTML only (removed by JavaScript)")
				}
				if rep.RenderedOnlyLinks > 0 {
					fmt.Fprintf(out, "js links     %d hyperlinks only in the rendered DOM\n", rep.RenderedOnlyLinks)
				}
				for _, e := range rep.ConsoleErrors {
					fmt.Fprintf(out, "console      %s\n", e)
				}
			}
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func diffCol(changed bool, raw, rendered string) string {
	if !changed {
		return "unchanged"
	}
	return fmt.Sprintf("%q -> %q", raw, rendered)
}

func newToolsLlmsCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "llms <site>",
		Short: "Validate a site's /llms.txt and /llms-full.txt (llmstxt.org)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.LlmsTxt(cmd.Context(), args[0])
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			for _, f := range rep.Files {
				if !f.Found {
					fmt.Fprintf(out, "MISSING  %s  (status %d)\n", f.URL, f.Status)
					continue
				}
				fmt.Fprintf(out, "OK       %s  (title %q)\n", f.URL, f.Title)
			}
			for _, l := range rep.Links {
				fmt.Fprintf(out, "  link   %s  (%s)\n", l.URL, l.Section)
			}
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func newToolsAIBotsCmd() *cobra.Command {
	var live bool
	var skip []string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "aibots <site>",
		Short: "Test which AI crawlers can access a site",
		Long: "Evaluates robots.txt for every AI crawler in the registry (GPTBot,\n" +
			"ClaudeBot, PerplexityBot, ...) and, with --live, probes the site root\n" +
			"once per fetcher bot with that bot's User-Agent against a control fetch —\n" +
			"catching WAF/CDN-level blocks robots.txt testing cannot see.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.AIBots(cmd.Context(), args[0], sitecheck.AIBotOptions{Live: live, Skip: skip})
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			if live {
				fmt.Fprintf(out, "control fetch  %s  (status %d)\n", rep.URL, rep.ControlStatus)
			}
			for _, b := range rep.Bots {
				robotsCol := "allowed"
				if !b.RobotsAllowed {
					robotsCol = fmt.Sprintf("BLOCKED (line %d: %s)", b.RobotsLine, b.RobotsRule)
				}
				liveCol := "-"
				switch {
				case b.TokenOnly():
					liveCol = "control token (never fetches)"
				case b.Probed && b.LiveError != "":
					liveCol = b.LiveError
				case b.Probed && b.BlockedLive:
					liveCol = fmt.Sprintf("BLOCKED (status %d)", b.LiveStatus)
				case b.Probed:
					liveCol = fmt.Sprintf("status %d", b.LiveStatus)
				}
				note := ""
				if !b.RespectsRobots {
					note = "  [does not honour robots.txt]"
				}
				fmt.Fprintf(out, "%-22s %-12s robots: %-40s live: %s%s\n",
					b.Name, b.Operator, robotsCol, liveCol, note)
			}
			fmt.Fprintln(out, "note: "+rep.Caveat)
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().BoolVar(&live, "live", true, "probe the site root with each fetcher bot's User-Agent")
	cmd.Flags().StringSliceVar(&skip, "skip", nil, "registry bot names to exclude")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func newToolsListCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the available tools",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(sitecheck.Tools())
			}
			for _, tool := range sitecheck.Tools() {
				fmt.Fprintf(cmd.OutOrStdout(), "%-10s %s\n", tool.Name, tool.Summary)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the registry as JSON")
	return cmd
}

func newToolsRobotsCmd() *cobra.Command {
	var site, robotsFile, userAgent string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "robots [url...]",
		Short: "Audit a robots.txt and test URLs against it (Google REP semantics)",
		Long: "Fetches and audits a site's live robots.txt (or a local file via\n" +
			"--robots-file) and reports a verdict for every given URL.",
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			opts := sitecheck.RobotsOptions{TestURLs: args, TestUserAgent: userAgent}
			var rep *sitecheck.RobotsReport
			switch {
			case robotsFile != "":
				data, err := os.ReadFile(robotsFile)
				if err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
				rep = chk.EvaluateRobotsFile(data, opts)
			default:
				target := site
				if target == "" {
					if len(args) == 0 {
						err := errors.New("provide --site, --robots-file, or at least one URL")
						fmt.Fprintln(cmd.ErrOrStderr(), err)
						return exitErr{2, err}
					}
					target = args[0]
				}
				if rep, err = chk.Robots(cmd.Context(), target, opts); err != nil {
					fmt.Fprintln(cmd.ErrOrStderr(), err)
					return exitErr{2, err}
				}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			switch {
			case rep.FetchError != "":
				fmt.Fprintf(out, "robots.txt  %s  (unreachable: %s)\n", rep.URL, rep.FetchError)
			case !rep.Found:
				fmt.Fprintf(out, "robots.txt  %s  (status %d)\n", rep.URL, rep.Status)
			default:
				fmt.Fprintf(out, "robots.txt  %s  (status %d, %d bytes, %d groups, %d rules)\n",
					displayURL(rep), rep.Status, rep.SizeBytes, rep.Groups, rep.Rules)
				for _, sm := range rep.Sitemaps {
					fmt.Fprintf(out, "sitemap     %s\n", sm)
				}
			}
			for _, v := range rep.Verdicts {
				if v.Allowed {
					fmt.Fprintf(out, "ALLOWED  %s\n", v.URL)
				} else {
					fmt.Fprintf(out, "BLOCKED  %s  (line %d: %s)\n", v.URL, v.Line, v.Rule)
				}
			}
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().StringVar(&site, "site", "", "site root to fetch robots.txt from (default: the first URL's host)")
	cmd.Flags().StringVar(&robotsFile, "robots-file", "", "local robots.txt file to test against instead of fetching")
	cmd.Flags().StringVar(&userAgent, "robots-user-agent", "bluesnake", "robots user-agent token")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

func newToolsSitemapCmd() *cobra.Command {
	var checkEntries int
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "sitemap <site|sitemap-url>",
		Short: "Discover and validate a site's XML sitemaps",
		Long: "Given a site root, discovers sitemaps via robots.txt Sitemap: directives\n" +
			"(falling back to /sitemap.xml conventions); given a sitemap URL, validates\n" +
			"that file. Index files are expanded recursively.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			chk, err := newToolChecker()
			if err != nil {
				return err
			}
			rep, err := chk.Sitemaps(cmd.Context(), args[0], sitecheck.SitemapOptions{CheckEntries: checkEntries})
			if err != nil {
				fmt.Fprintln(cmd.ErrOrStderr(), err)
				return exitErr{2, err}
			}
			if asJSON {
				return emitToolJSON(cmd.OutOrStdout(), rep)
			}
			out := cmd.OutOrStdout()
			if rep.Missing {
				fmt.Fprintf(out, "no sitemap found for %s (robots.txt directives and conventional paths)\n", rep.Site)
			}
			for _, f := range rep.Files {
				switch {
				case f.FetchError != "":
					fmt.Fprintf(out, "ERROR    %s  (%s)\n", f.URL, f.FetchError)
				case f.Status < 200 || f.Status >= 300:
					fmt.Fprintf(out, "ERROR    %s  (status %d)\n", f.URL, f.Status)
				case f.XMLError != "":
					fmt.Fprintf(out, "INVALID  %s  (%s)\n", f.URL, f.XMLError)
				default:
					fmt.Fprintf(out, "OK       %s  (%s, %d entries, %d bytes, via %s)\n",
						f.URL, f.Kind, f.Entries, f.SizeBytes, f.Source)
				}
				for _, ec := range f.EntryChecks {
					if ec.Error != "" {
						fmt.Fprintf(out, "  entry %s  (%s)\n", ec.URL, ec.Error)
					} else {
						fmt.Fprintf(out, "  entry %s  (status %d)\n", ec.URL, ec.Status)
					}
				}
			}
			for _, skipped := range rep.Skipped {
				fmt.Fprintf(out, "SKIPPED  %s  (beyond the per-run expansion bound)\n", skipped)
			}
			printFindings(out, rep.Findings())
			return nil
		},
	}
	cmd.Flags().IntVar(&checkEntries, "check-entries", 0, "live-check up to N listed URLs")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit the full report as JSON")
	return cmd
}

// newToolChecker builds a checker over the default config — tools are
// stateless one-shots in their own process. No limiter: nothing runs beside
// a one-shot, so there is no process-wide ceiling to share (the executor's
// single-crawl P17 fallback, applied to a no-crawl process). In-process
// surfaces (desktop Tools hub, MCP run_tool) inject theirs via WithLimiter.
func newToolChecker() (*sitecheck.Checker, error) {
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		return nil, err
	}
	return sitecheck.New(cfg, client), nil
}

// displayURL prefers the final URL when robots.txt was served via redirects.
func displayURL(rep *sitecheck.RobotsReport) string {
	if rep.FinalURL != "" && rep.FinalURL != rep.URL {
		return rep.URL + " -> " + rep.FinalURL
	}
	return rep.URL
}

// printFindings renders the derived issue occurrences with their catalogue
// severity — the same findings a crawl would store.
func printFindings(w io.Writer, fs []sitecheck.Finding) {
	if len(fs) == 0 {
		fmt.Fprintln(w, "findings: none")
		return
	}
	fmt.Fprintf(w, "findings (%d):\n", len(fs))
	for _, f := range fs {
		sev, name := "?", f.IssueID
		if def, ok := issues.Lookup(f.IssueID); ok {
			sev, name = string(def.Severity), def.Name
		}
		line := fmt.Sprintf("  [%s] %s", sev, name)
		if f.Detail != "" {
			line += " — " + f.Detail
		}
		fmt.Fprintln(w, line)
	}
}

// emitToolJSON wraps a report with its findings — the same JSON shape the MCP
// run_tool function returns.
func emitToolJSON(w io.Writer, rep sitecheck.Reporter) error {
	fs := rep.Findings()
	if fs == nil {
		fs = []sitecheck.Finding{}
	}
	return json.NewEncoder(w).Encode(struct {
		Report   any                 `json:"report"`
		Findings []sitecheck.Finding `json:"findings"`
	}{rep, fs})
}
