package sitecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
)

func TestAIBotsRobotsVerdicts(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: GPTBot\nDisallow: /\n\nUser-agent: *\nAllow: /\n")
			return
		}
		fmt.Fprint(w, "<html></html>")
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.RobotsFound || rep.Caveat == "" {
		t.Fatalf("report = %+v, want robots found + caveat", rep)
	}
	byName := map[string]AIBotResult{}
	for _, b := range rep.Bots {
		byName[b.Name] = b
	}
	if byName["GPTBot"].RobotsAllowed {
		t.Error("GPTBot should be blocked by its group")
	}
	if got := byName["GPTBot"]; got.RobotsLine != 2 || got.RobotsRule != "Disallow: /" {
		t.Errorf("GPTBot matched rule = %+v", got)
	}
	if !byName["ClaudeBot"].RobotsAllowed {
		t.Error("ClaudeBot should fall back to the wildcard allow")
	}
	// Without Live, nothing is probed.
	for _, b := range rep.Bots {
		if b.Probed {
			t.Errorf("%s probed without Live", b.Name)
		}
	}
	found := map[string]bool{}
	for _, f := range rep.Findings() {
		found[f.IssueID] = true
	}
	if !found["ai_bot_blocked_robots"] || found["ai_bots_all_blocked_robots"] || found["ai_bot_blocked_live"] {
		t.Errorf("findings = %v", found)
	}
}

func TestAIBotsAllBlocked(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "ai_bots_all_blocked_robots") {
		t.Errorf("findings = %v, want ai_bots_all_blocked_robots", findingIDs(rep))
	}
}

func TestAIBotsLiveProbes(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		switch {
		case r.URL.Path == "/robots.txt":
			w.WriteHeader(404) // no robots: every bot allowed there
		case strings.Contains(ua, "ClaudeBot"):
			w.WriteHeader(403) // the "WAF" blocks ClaudeBot at the edge
		default:
			fmt.Fprint(w, "<html></html>")
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if rep.ControlStatus != 200 {
		t.Fatalf("control status = %d", rep.ControlStatus)
	}
	byName := map[string]AIBotResult{}
	for _, b := range rep.Bots {
		byName[b.Name] = b
	}
	claude := byName["ClaudeBot"]
	if !claude.Probed || claude.LiveStatus != 403 || !claude.BlockedLive {
		t.Errorf("ClaudeBot = %+v, want an edge block", claude)
	}
	if !claude.RobotsAllowed {
		t.Error("ClaudeBot robots verdict should be allowed (no robots.txt)")
	}
	gpt := byName["GPTBot"]
	if !gpt.Probed || gpt.LiveStatus != 200 || gpt.BlockedLive {
		t.Errorf("GPTBot = %+v, want a clean probe", gpt)
	}
	// Token-only entries are never probed: no fetcher sends a control token,
	// and a search engine's crawler is verified by reverse DNS, so a probe
	// with its UA from our IP would meet an impostor block the real one never
	// does.
	for _, name := range []string{"Google-Extended", "Applebot-Extended", "Googlebot", "Bingbot", "Applebot"} {
		if b := byName[name]; b.Probed || !b.TokenOnly() {
			t.Errorf("%s = %+v, want token-only and unprobed", name, b)
		}
	}
	ids := findingIDs(rep)
	if !hasFinding(rep, "ai_bot_blocked_live") {
		t.Errorf("findings = %v, want ai_bot_blocked_live", ids)
	}
	if hasFinding(rep, "ai_bot_blocked_robots") {
		t.Errorf("findings = %v — no robots blocks exist", ids)
	}
}

// When the control fetch itself fails, nothing is attributable to
// bot-blocking: no live findings.
func TestAIBotsNoAttributionWithoutHealthyControl(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		w.WriteHeader(503) // down for everyone
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(rep, "ai_bot_blocked_live") {
		t.Errorf("findings = %v — a site down for everyone is not a bot block", findingIDs(rep))
	}
}

func TestAIBotsSkipAndExtra(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: MyBot\nDisallow: /\n")
			return
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{
		Skip:  []string{"Bytespider"},
		Extra: []Bot{{Name: "MyBot", Operator: "Me", Purpose: "training", RobotsToken: "MyBot", RespectsRobots: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, b := range rep.Bots {
		names[b.Name] = true
	}
	if names["Bytespider"] {
		t.Error("skipped bot still present")
	}
	if !names["MyBot"] {
		t.Error("extra bot missing")
	}
	var blocked bool
	for _, f := range rep.Findings() {
		if f.IssueID == "ai_bot_blocked_robots" && strings.Contains(f.Detail, "MyBot") {
			blocked = true
		}
	}
	if !blocked {
		t.Error("extra bot's robots block not reported")
	}
}

// A robots-ignoring bot's block carries the ineffectiveness note.
func TestAIBotsRobotsIgnoringNote(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: Bytespider\nDisallow: /\n\nUser-agent: *\nAllow: /\n")
			return
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var note bool
	for _, f := range rep.Findings() {
		if f.IssueID == "ai_bot_blocked_robots" && strings.Contains(f.Detail, "does not honour robots.txt") {
			note = true
		}
	}
	if !note {
		t.Error("Bytespider block should carry the robots-ineffective note")
	}
}

func TestDecodeFindingsAIBots(t *testing.T) {
	rep := &AIBotsReport{
		Site: "https://ex.com", URL: "https://ex.com/",
		Bots: []AIBotResult{{
			Bot:        Bot{Name: "GPTBot", Operator: "OpenAI", Purpose: "training", RespectsRobots: true},
			BotVerdict: BotVerdict{RobotsLine: 2, RobotsRule: "Disallow: /"},
		}},
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	got := DecodeFindings(KindAIBots, data)
	ids := map[string]bool{}
	for _, f := range got {
		ids[f.IssueID] = true
	}
	if !ids["ai_bot_blocked_robots"] || !ids["ai_bots_all_blocked_robots"] {
		t.Fatalf("decoded findings = %+v", got)
	}
}

func TestRegistryInvariants(t *testing.T) {
	names := map[string]bool{}
	for _, b := range DefaultBots() {
		if b.Name == "" || b.Operator == "" {
			t.Errorf("bot %+v needs name and operator", b)
		}
		if names[b.Name] {
			t.Errorf("bot %s listed twice", b.Name)
		}
		names[b.Name] = true
		if b.RobotsToken == "" && b.UserAgent == "" {
			t.Errorf("bot %s has neither a robots token nor a user agent: nothing to check", b.Name)
		}
		switch b.Purpose {
		case "training", "search", "user_action":
		default:
			t.Errorf("bot %s has invalid purpose %q", b.Name, b.Purpose)
		}
		if b.TokenOnly() && !b.RespectsRobots {
			t.Errorf("token-only entry %s makes no sense as robots-ignoring", b.Name)
		}
		// robots.txt cannot address a bot with no token, so the bot cannot
		// honour it, and there is no group of its own to fall back from.
		if b.RobotsToken == "" && (b.RespectsRobots || len(b.RobotsFallback) > 0) {
			t.Errorf("tokenless entry %s must not respect robots.txt or carry a fallback", b.Name)
		}
		if strings.Contains(b.UserAgent, "W.X.Y.Z") || strings.Contains(b.UserAgent, "X.X.X.X") {
			t.Errorf("bot %s carries the operator's version placeholder: %q", b.Name, b.UserAgent)
		}
	}
}

// The registry's documented fallback groups: a site that blocks only
// Googlebot blocks Applebot too (Apple follows Googlebot's group when none
// names Applebot), and Amzn-SearchBot follows the search engines' groups.
// Both change the site check's verdicts.
func TestAIBotsFallbackGroups(t *testing.T) {
	for _, tt := range []struct {
		name, robotsTxt    string
		applebot, amzn     bool // allowed
		applebotVia, amznV string
	}{
		{"googlebot group only", "User-agent: Googlebot\nDisallow: /\n\nUser-agent: *\nAllow: /\n",
			false, false, "Googlebot", "Googlebot"},
		{"bingbot group only", "User-agent: bingbot\nDisallow: /\n",
			true, false, "", "bingbot"},
		{"own groups win", "User-agent: Googlebot\nDisallow: /\n\nUser-agent: Applebot\nUser-agent: Amzn-SearchBot\nAllow: /\n",
			true, true, "", ""},
		{"no named groups: *", "User-agent: *\nDisallow: /\n",
			false, false, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := serve(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/robots.txt" {
					fmt.Fprint(w, tt.robotsTxt)
				}
			})
			rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
			if err != nil {
				t.Fatal(err)
			}
			byName := map[string]AIBotResult{}
			for _, b := range rep.Bots {
				byName[b.Name] = b
			}
			if a := byName["Applebot"]; a.RobotsAllowed != tt.applebot || a.RobotsVia != tt.applebotVia {
				t.Errorf("Applebot = allowed %v via %q, want %v via %q", a.RobotsAllowed, a.RobotsVia, tt.applebot, tt.applebotVia)
			}
			if a := byName["Amzn-SearchBot"]; a.RobotsAllowed != tt.amzn || a.RobotsVia != tt.amznV {
				t.Errorf("Amzn-SearchBot = allowed %v via %q, want %v via %q", a.RobotsAllowed, a.RobotsVia, tt.amzn, tt.amznV)
			}
		})
	}
	// The finding says whose group decided.
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: Googlebot\nDisallow: /\n")
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var detail string
	for _, f := range rep.Findings() {
		if f.IssueID == "ai_bot_blocked_robots" && strings.HasPrefix(f.Detail, "Applebot (") {
			detail = f.Detail
		}
	}
	if !strings.Contains(detail, "follows Googlebot's") {
		t.Errorf("Applebot finding = %q, want it to name the Googlebot group", detail)
	}
}

// A bot with a user agent and no robots token (Google-Agent) is probed live
// and never gets a robots verdict: robots.txt cannot address it.
func TestAIBotsTokenlessBots(t *testing.T) {
	var probed atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: *\nDisallow: /\n")
			return
		}
		if ua := r.Header.Get("User-Agent"); strings.Contains(ua, "Google-Agent") || strings.Contains(ua, "Google-GeminiNotebook") {
			probed.Add(1)
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range rep.Bots {
		if b.Name != "Google-Agent" && b.Name != "Google-GeminiNotebook" {
			continue
		}
		if b.RobotsToken != "" || !b.RobotsAllowed || b.RobotsRule != "" || !b.Probed || b.LiveStatus != 200 {
			t.Errorf("%s = %+v, want no robots verdict and a live probe", b.Name, b)
		}
		for _, f := range rep.Findings() {
			if strings.HasPrefix(f.Detail, b.Name+" (") {
				t.Errorf("tokenless %s produced finding %+v", b.Name, f)
			}
		}
	}
	if n := probed.Load(); n != 2 {
		t.Errorf("probes with the tokenless bots' user agents = %d, want 2", n)
	}
}

// onlyBots skips every registry bot but the named ones, so a test's fetch
// count stays small.
func onlyBots(keep ...string) []string {
	var skip []string
	for _, b := range DefaultBots() {
		if !slices.Contains(keep, b.Name) {
			skip = append(skip, b.Name)
		}
	}
	return skip
}

func TestAIBotsURLsVerdicts(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		ua := r.Header.Get("User-Agent")
		switch {
		case r.URL.Path == "/robots.txt":
			fmt.Fprint(w, "User-agent: GPTBot\nDisallow: /pricing\n\nUser-agent: Googlebot\nDisallow: /search-hidden\n\nUser-agent: *\nAllow: /\n")
		case r.URL.Path == "/down":
			w.WriteHeader(403) // turned away for everyone, the control included
		case strings.Contains(ua, "ClaudeBot"):
			w.WriteHeader(403) // the "WAF" blocks ClaudeBot at the edge
		default:
			fmt.Fprint(w, "<html></html>")
		}
	})
	urls := []string{s.URL + "/pricing", s.URL + "/search-hidden", s.URL + "/down", s.URL + "/pricing"}
	rep, err := newChecker(t).AIBotsURLs(context.Background(), s.URL, urls, AIBotOptions{
		Live: true, Skip: onlyBots("GPTBot", "ClaudeBot", "Applebot", "Googlebot"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Bots) != 4 || rep.Caveat == "" || !rep.Robots.Found || rep.Robots.Status != 200 ||
		!strings.Contains(rep.Robots.Body, "Disallow: /pricing") || rep.Robots.URL != s.URL+"/robots.txt" {
		t.Fatalf("report header = bots %d, robots %+v, caveat %q", len(rep.Bots), rep.Robots, rep.Caveat)
	}
	if len(rep.URLs) != 3 {
		t.Fatalf("urls = %d, want the repeat dropped", len(rep.URLs))
	}
	verdicts := func(i int) map[string]NamedBotVerdict {
		if rep.URLs[i].URL != urls[i] {
			t.Fatalf("urls[%d] = %s, want input order", i, rep.URLs[i].URL)
		}
		m := map[string]NamedBotVerdict{}
		for _, v := range rep.URLs[i].Bots {
			m[v.Name] = v
		}
		return m
	}

	// /pricing is disallowed for GPTBot only: its verdict names the rule.
	pricing := verdicts(0)
	if g := pricing["GPTBot"]; g.RobotsAllowed || g.RobotsLine != 2 || g.RobotsRule != "Disallow: /pricing" {
		t.Errorf("GPTBot on /pricing = %+v, want blocked by line 2", g)
	}
	for _, name := range []string{"ClaudeBot", "Applebot", "Googlebot"} {
		if !pricing[name].RobotsAllowed {
			t.Errorf("%s on /pricing = %+v, want allowed", name, pricing[name])
		}
	}
	// Control 200, ClaudeBot 403: an edge block; GPTBot's 200 is not.
	if rep.URLs[0].ControlStatus != 200 {
		t.Errorf("control on /pricing = %d", rep.URLs[0].ControlStatus)
	}
	if c := pricing["ClaudeBot"]; !c.Probed || c.LiveStatus != 403 || !c.BlockedLive {
		t.Errorf("ClaudeBot on /pricing = %+v, want blocked live", c)
	}
	if g := pricing["GPTBot"]; !g.Probed || g.BlockedLive {
		t.Errorf("GPTBot on /pricing = %+v, want probed, not blocked live", g)
	}
	if a := pricing["Applebot"]; a.Probed {
		t.Errorf("token-only Applebot probed: %+v", a)
	}

	// No Applebot group: Applebot follows Googlebot's.
	if a := verdicts(1)["Applebot"]; a.RobotsAllowed || a.RobotsVia != "Googlebot" || a.RobotsRule != "Disallow: /search-hidden" {
		t.Errorf("Applebot on /search-hidden = %+v, want blocked via Googlebot's group", a)
	}

	// Control 403: no bot is marked blocked.
	down := verdicts(2)
	if rep.URLs[2].ControlStatus != 403 {
		t.Errorf("control on /down = %d", rep.URLs[2].ControlStatus)
	}
	for name, v := range down {
		if v.BlockedLive {
			t.Errorf("%s on /down marked blocked live with an unhealthy control", name)
		}
	}

	// Findings attach to the page they are about.
	byURL := map[string][]string{}
	for _, f := range rep.Findings() {
		byURL[f.URL] = append(byURL[f.URL], f.IssueID+" "+f.Detail)
	}
	if got := strings.Join(byURL[urls[0]], "\n"); !strings.Contains(got, "ai_bot_blocked_robots GPTBot") ||
		!strings.Contains(got, "ai_bot_blocked_live ClaudeBot") {
		t.Errorf("/pricing findings = %q", got)
	}
	if got := strings.Join(byURL[urls[1]], "\n"); !strings.Contains(got, "ai_bot_blocked_robots Applebot") {
		t.Errorf("/search-hidden findings = %q", got)
	}
	if len(byURL[urls[2]]) != 0 {
		t.Errorf("/down findings = %q, want none", byURL[urls[2]])
	}
}

func TestAIBotsURLsRejectsOtherHosts(t *testing.T) {
	chk := newChecker(t)
	for _, urls := range [][]string{
		{"https://ex.com/a", "https://other.com/b"},
		{"https://ex.com/a", "https://sub.ex.com/b"},
		{"https://ex.com:8443/a"},
		{"ftp://ex.com/a"},
		{},
	} {
		if _, err := chk.AIBotsURLs(context.Background(), "ex.com", urls, AIBotOptions{}); err == nil {
			t.Errorf("AIBotsURLs(%v) = nil error, want a rejection", urls)
		}
	}
	// Same host: default ports, case and a bare host are all the site's.
	got, err := sameHostURLs("https://ex.com", []string{"https://EX.com/a", "https://ex.com:443/b", "ex.com/c", "http://ex.com/d"})
	if err != nil || len(got) != 4 {
		t.Errorf("sameHostURLs = %v, %v, want all four accepted", got, err)
	}
}

// The pages × bots fetches stay within the speed settings: at most
// speed.max_threads in flight, and starts spaced by speed.max_urls_per_sec.
func TestAIBotsURLsSpeed(t *testing.T) {
	var inFlight, peak atomic.Int32
	var fetches atomic.Int32
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		time.Sleep(10 * time.Millisecond)
	})
	var urls []string
	for i := range 8 {
		urls = append(urls, fmt.Sprintf("%s/p%d", s.URL, i))
	}
	opts := AIBotOptions{Live: true, Skip: onlyBots("GPTBot")}

	cfg := config.Default()
	cfg.Speed.MaxThreads = 2
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, client).AIBotsURLs(context.Background(), s.URL, urls, opts); err != nil {
		t.Fatal(err)
	}
	if p := peak.Load(); p > 2 {
		t.Errorf("peak in-flight fetches = %d, want <= speed.max_threads (2)", p)
	}

	cfg.Speed.MaxURLsPerSec = 50 // one start every 20ms
	fetches.Store(0)
	start := time.Now()
	if _, err := New(cfg, client).AIBotsURLs(context.Background(), s.URL, urls, opts); err != nil {
		t.Fatal(err)
	}
	n := fetches.Load() // robots.txt + 8 × (control + GPTBot)
	if want := time.Duration(n-1) * 20 * time.Millisecond; time.Since(start) < want {
		t.Errorf("%d fetches took %v, want >= %v at 50/s", n, time.Since(start), want)
	}
}

// A run cancelled while waiting on the rate limit degrades to fetch errors —
// a check reports, never fails.
func TestAIBotsURLsCancelledWhilePaced(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {})
	cfg := config.Default()
	cfg.Speed.MaxURLsPerSec = 0.01 // one start per 100s: only the first goes
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rep, err := New(cfg, client).AIBotsURLs(ctx, s.URL, []string{s.URL + "/a"}, AIBotOptions{Live: true, Skip: onlyBots("GPTBot")})
	if err != nil {
		t.Fatal(err)
	}
	page := rep.URLs[0]
	if !strings.Contains(page.ControlError, "rate limit") || !strings.Contains(page.Bots[0].LiveError, "rate limit") {
		t.Errorf("page = %+v, want the control and probe cancelled at the rate limit", page)
	}
}

func TestRateGate(t *testing.T) {
	var nilGate *rateGate
	if !nilGate.wait(context.Background()) {
		t.Error("a nil gate must never wait")
	}
	g := &rateGate{every: 10 * time.Millisecond}
	start := time.Now()
	for range 4 {
		if !g.wait(context.Background()) {
			t.Fatal("wait failed")
		}
	}
	if el := time.Since(start); el < 30*time.Millisecond {
		t.Errorf("4 starts took %v, want >= 30ms", el)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	slow := &rateGate{every: time.Hour}
	slow.wait(context.Background()) // the first start goes at once
	if slow.wait(ctx) {
		t.Error("a cancelled wait must report false")
	}
}

// The search engines' crawlers sit beside their training tokens, so a report
// can tell a site that opts out of training (Google-Extended,
// Applebot-Extended) from one that blocks being found — the crawlers AI
// Overviews, Copilot and Siri answer from.
func TestAIBotsSearchCrawlersBesideTrainingTokens(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			fmt.Fprint(w, "User-agent: Google-Extended\nUser-agent: Applebot-Extended\nDisallow: /\n\n"+
				"User-agent: bingbot\nDisallow: /\n\nUser-agent: *\nAllow: /\n")
			return
		}
	})
	rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]AIBotResult{}
	for _, b := range rep.Bots {
		byName[b.Name] = b
	}
	for _, tt := range []struct {
		name, operator string
		allowed        bool
	}{
		{"Googlebot", "Google", true},
		{"Google-Extended", "Google", false},
		{"Applebot", "Apple", true},
		{"Applebot-Extended", "Apple", false},
		{"Bingbot", "Microsoft", false}, // matched by the lowercase token Bing documents
	} {
		b, ok := byName[tt.name]
		if !ok {
			t.Errorf("%s missing from the report", tt.name)
			continue
		}
		if b.Operator != tt.operator || b.RobotsAllowed != tt.allowed {
			t.Errorf("%s = operator %q allowed %v, want %q %v", tt.name, b.Operator, b.RobotsAllowed, tt.operator, tt.allowed)
		}
	}
	for _, name := range []string{"Googlebot", "Bingbot", "Applebot"} {
		if b := byName[name]; b.Purpose != "search" || !b.TokenOnly() || b.DocURL == "" {
			t.Errorf("%s = %+v, want a token-only search entry with its doc URL", name, b.Bot)
		}
	}
}

// The all-blocked headline counts every training and search crawler, the
// search engines' included: a site that names every AI bot but still lets
// Googlebot, Bingbot and Applebot in is not invisible to AI answers, since
// those engines answer from what their crawlers index.
func TestAIBotsAllBlockedCountsSearchEngineCrawlers(t *testing.T) {
	engines := map[string]bool{"Googlebot": true, "Bingbot": true, "Applebot": true}
	robotsFor := func(blockEngines bool) string {
		var b strings.Builder
		for _, bot := range DefaultBots() {
			if (engines[bot.Name] && !blockEngines) || bot.RobotsToken == "" {
				continue
			}
			fmt.Fprintf(&b, "User-agent: %s\n", bot.RobotsToken)
		}
		b.WriteString("Disallow: /\n\nUser-agent: *\nAllow: /\n")
		return b.String()
	}
	for _, tt := range []struct {
		blockEngines, headline bool
	}{{false, false}, {true, true}} {
		body := robotsFor(tt.blockEngines)
		s := serve(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				fmt.Fprint(w, body)
			}
		})
		rep, err := newChecker(t).AIBots(context.Background(), s.URL, AIBotOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if got := hasFinding(rep, "ai_bots_all_blocked_robots"); got != tt.headline {
			t.Errorf("engines blocked=%v: all-blocked headline = %v, want %v (findings %v)",
				tt.blockEngines, got, tt.headline, findingIDs(rep))
		}
	}
}
