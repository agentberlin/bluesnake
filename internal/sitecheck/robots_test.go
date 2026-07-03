package sitecheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
)

func newChecker(t *testing.T) *Checker {
	t.Helper()
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, client)
}

func serve(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(handler)
	t.Cleanup(s.Close)
	return s
}

func findingIDs(r Reporter) []string {
	var ids []string
	for _, f := range r.Findings() {
		ids = append(ids, f.IssueID)
	}
	return ids
}

func hasFinding(r Reporter, id string) bool {
	for _, f := range r.Findings() {
		if f.IssueID == id {
			return true
		}
	}
	return false
}

func TestRobotsHealthy(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/robots.txt" {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, "User-agent: *\nDisallow: /private/\nSitemap: https://ex.com/sitemap.xml\n")
	})
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{
		TestURLs: []string{s.URL + "/private/x", s.URL + "/ok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.Status != 200 {
		t.Fatalf("Found=%v Status=%d, want found 200", rep.Found, rep.Status)
	}
	if rep.Groups != 1 || rep.Rules != 1 {
		t.Errorf("Groups=%d Rules=%d, want 1/1", rep.Groups, rep.Rules)
	}
	if len(rep.Sitemaps) != 1 {
		t.Errorf("Sitemaps = %v, want one", rep.Sitemaps)
	}
	// The report carries the audited body — the desktop tester loads it into
	// its editor, so a live report must be self-contained.
	if !strings.Contains(rep.Body, "Disallow: /private/") {
		t.Errorf("Body = %q, want the fetched file", rep.Body)
	}
	if ids := findingIDs(rep); ids != nil {
		t.Errorf("healthy robots.txt has findings %v, want none", ids)
	}
	if len(rep.Verdicts) != 2 || rep.Verdicts[0].Allowed || !rep.Verdicts[1].Allowed {
		t.Errorf("Verdicts = %+v, want blocked then allowed", rep.Verdicts)
	}
	if rep.Verdicts[0].Line != 2 || rep.Verdicts[0].Rule != "Disallow: /private/" {
		t.Errorf("blocked verdict = %+v, want matched line 2", rep.Verdicts[0])
	}
}

func TestRobotsMissing(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found {
		t.Fatal("Found = true for a 404")
	}
	if !hasFinding(rep, "robots_txt_missing") {
		t.Errorf("findings = %v, want robots_txt_missing", findingIDs(rep))
	}
	// A missing file must not also complain about a missing Sitemap directive.
	if hasFinding(rep, "robots_txt_no_sitemap") {
		t.Error("missing robots.txt also reported robots_txt_no_sitemap")
	}
}

func TestRobotsServerError(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(rep, "robots_txt_server_error") {
		t.Errorf("findings = %v, want robots_txt_server_error", findingIDs(rep))
	}
}

func TestRobotsUnreachable(t *testing.T) {
	rep, err := newChecker(t).Robots(context.Background(), "http://127.0.0.1:1", RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.FetchError == "" || !hasFinding(rep, "robots_txt_server_error") {
		t.Errorf("FetchError=%q findings=%v, want unreachable → server_error", rep.FetchError, findingIDs(rep))
	}
}

func TestRobotsUnhealthyFile(t *testing.T) {
	big := strings.Repeat("# padding padding padding padding padding padding\n", 11000) // >500KiB
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "Disallow: /early\nUser-agent: *\nDisallow: /\nDisallow /broken\n"+big)
	})
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{
		"robots_txt_blocks_all", "robots_txt_invalid_lines",
		"robots_txt_too_large", "robots_txt_no_sitemap",
	} {
		if !hasFinding(rep, id) {
			t.Errorf("findings = %v, want %s", findingIDs(rep), id)
		}
	}
	if !rep.BlocksAll || rep.BlocksAllRule == "" {
		t.Errorf("BlocksAll=%v rule=%q", rep.BlocksAll, rep.BlocksAllRule)
	}
	if len(rep.IgnoredLines) != 2 { // the before-group rule and the missing colon
		t.Errorf("IgnoredLines = %+v, want 2", rep.IgnoredLines)
	}
	// The carried body is capped at what Google reads; the full size is still
	// reported (the desktop editor loads rep.Body, the finding cites SizeBytes).
	if len(rep.Body) != maxRobotsBytes || rep.SizeBytes <= maxRobotsBytes {
		t.Errorf("body carried = %d bytes (want cap %d), size = %d", len(rep.Body), maxRobotsBytes, rep.SizeBytes)
	}
}

// A specific Allow that overrides a wildcard Disallow: / for some paths still
// means the root is blocked for everyone — blocks_all stays, matching the
// wildcard verdict for the root path.
func TestRobotsBlocksAllIsWildcardRootVerdict(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "User-agent: googlebot\nDisallow: /\n\nUser-agent: *\nAllow: /\nSitemap: /s.xml\n")
	})
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// Only googlebot's group blocks; the wildcard group allows → not blocks-all.
	if rep.BlocksAll {
		t.Error("BlocksAll = true although the wildcard group allows the root")
	}
}

func TestRobotsRedirectFollowed(t *testing.T) {
	var s *httptest.Server
	s = serve(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			http.Redirect(w, r, s.URL+"/real-robots.txt", http.StatusMovedPermanently)
		case "/real-robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /x\nSitemap: /s.xml\n")
		default:
			w.WriteHeader(404)
		}
	})
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Found || rep.Rules != 1 {
		t.Fatalf("redirected robots not parsed: %+v", rep)
	}
	if rep.FinalURL != s.URL+"/real-robots.txt" || len(rep.RedirectChain) != 1 {
		t.Errorf("FinalURL=%q chain=%v", rep.FinalURL, rep.RedirectChain)
	}
}

// A redirect loop exhausts the hop budget and lands on a 3xx terminal —
// Google treats that as no robots.txt (allow all), so it reports missing.
func TestRobotsRedirectLoop(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/robots.txt", http.StatusFound)
	})
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Found || !hasFinding(rep, "robots_txt_missing") {
		t.Errorf("Found=%v findings=%v, want missing", rep.Found, findingIDs(rep))
	}
}

func TestEvaluateRobotsFile(t *testing.T) {
	body := []byte("User-agent: *\nDisallow: /private/\n")
	rep := newChecker(t).EvaluateRobotsFile(body, RobotsOptions{
		TestURLs:      []string{"https://ex.com/private/x"},
		TestUserAgent: "somebot",
	})
	if !rep.Found || len(rep.Verdicts) != 1 || rep.Verdicts[0].Allowed {
		t.Fatalf("report = %+v", rep)
	}
	if rep.Verdicts[0].UserAgent != "somebot" {
		t.Errorf("verdict UA = %q", rep.Verdicts[0].UserAgent)
	}
}

func TestDecodeFindingsRoundTrip(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	rep, err := newChecker(t).Robots(context.Background(), s.URL, RobotsOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	got := DecodeFindings(KindRobots, data)
	if len(got) != len(rep.Findings()) || got[0].IssueID != "robots_txt_missing" {
		t.Fatalf("decoded findings = %+v", got)
	}
	if DecodeFindings("no-such-kind", data) != nil {
		t.Error("unknown kind should yield nil findings")
	}
	if DecodeFindings(KindRobots, []byte("{broken")) != nil {
		t.Error("undecodable report should yield nil findings")
	}
}
