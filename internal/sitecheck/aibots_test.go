package sitecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
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
	// Token-only entries are never probed — no fetcher sends their name.
	for _, name := range []string{"Google-Extended", "Applebot-Extended"} {
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
			RobotsLine: 2, RobotsRule: "Disallow: /",
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
	for _, b := range DefaultBots() {
		if b.Name == "" || b.Operator == "" || b.RobotsToken == "" {
			t.Errorf("bot %+v needs name, operator, robots token", b)
		}
		switch b.Purpose {
		case "training", "search", "user_action":
		default:
			t.Errorf("bot %s has invalid purpose %q", b.Name, b.Purpose)
		}
		if b.TokenOnly() && !b.RespectsRobots {
			t.Errorf("token-only entry %s makes no sense as robots-ignoring", b.Name)
		}
	}
}
