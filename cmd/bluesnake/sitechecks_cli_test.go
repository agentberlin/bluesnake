package main

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

// checksOffProfile disables the whole site-check family and edits the bot
// roster, so each choice has to set what it means rather than inherit it, and
// is seen leaving the roster alone.
const checksOffProfile = `site_checks:
  enabled: never
  robots: false
  sitemap: false
  render_diff: false
  ai_bots:
    check: false
    live_probe: false
    skip: [Bytespider]
`

// frozenSiteChecks reads the site_checks block a stored crawl froze.
func frozenSiteChecks(t *testing.T, dir, id string) config.SiteChecksConfig {
	t.Helper()
	st, err := store.OpenCrawl(dir, id)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	y, err := st.Meta("config")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.SiteChecks
}

// `crawl --site-checks` and the desktop's setup card map each value the same
// way: over the same profile, a CLI crawl freezes exactly the site_checks a
// desktop job carrying that choice freezes (its overrides in the job spec,
// resolved at enqueue by runner.FreezeSpec). The shorthand wins over the
// profile, like --threads.
func TestCrawlSiteChecksFlagMatchesTheDesktop(t *testing.T) {
	// Every path 404s: the seed fetch fails before the render diff would
	// launch Chrome, and the checks still run and freeze their config.
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	for _, choice := range []string{runner.SiteChecksAuto, runner.SiteChecksAll, runner.SiteChecksOff} {
		t.Run(choice, func(t *testing.T) {
			dir := t.TempDir()
			writeTestProfile(t, dir, "checks-off", "Checks off", checksOffProfile)
			if _, errOut, err := runCLI(t, "crawl", srv.URL+"/", "--store-dir", dir, "-q",
				"--profile", "Checks off", "--site-checks", choice); err != nil {
				t.Fatalf("crawl: %v (%s)", err, errOut)
			}
			infos, err := store.ListCrawls(dir)
			if err != nil || len(infos) != 1 {
				t.Fatalf("crawls = %v, %v", infos, err)
			}
			got := frozenSiteChecks(t, dir, infos[0].ID)

			overrides, err := runner.SiteChecksOverrides(choice)
			if err != nil {
				t.Fatal(err)
			}
			frozen, err := runner.FreezeSpec(dir, queue.JobSpec{
				Mode: "spider", URL: srv.URL + "/", Profile: "Checks off", Config: overrides,
			})
			if err != nil {
				t.Fatal(err)
			}
			desktop, err := config.Load([]byte(frozen.ConfigYAML))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, desktop.SiteChecks) {
				t.Errorf("CLI froze %+v, the desktop resolves %+v", got, desktop.SiteChecks)
			}
			if wantEnabled := map[string]string{"auto": "auto", "all": "always", "off": "never"}[choice]; got.Enabled != wantEnabled {
				t.Errorf("enabled = %q, want %q", got.Enabled, wantEnabled)
			}
		})
	}
}

// --site-checks all turns on every check, not just the gate, whatever the
// base had off — and leaves the base's bot roster edits alone.
func TestCrawlSiteChecksAllSetsEveryCheck(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	writeTestProfile(t, dir, "checks-off", "Checks off", checksOffProfile)
	if _, errOut, err := runCLI(t, "crawl", srv.URL+"/blog/", "--store-dir", dir, "-q",
		"--profile", "Checks off", "--site-checks", "all"); err != nil {
		t.Fatalf("crawl: %v (%s)", err, errOut)
	}
	infos, _ := store.ListCrawls(dir)
	sc := frozenSiteChecks(t, dir, infos[0].ID)
	if sc.Enabled != "always" || !sc.Robots || !sc.Sitemap || !sc.RenderDiff || !sc.AIBots.Check || !sc.AIBots.LiveProbe {
		t.Errorf("site_checks = %+v, want every check on", sc)
	}
	if !reflect.DeepEqual(sc.AIBots.Skip, []string{"Bytespider"}) {
		t.Errorf("ai_bots.skip = %v, want the profile's roster edit kept", sc.AIBots.Skip)
	}
	// Forced on a path crawl, the pass ran and stored its reports.
	st, err := store.OpenCrawl(dir, infos[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	checks, err := st.SiteChecks()
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) == 0 {
		t.Error("--site-checks all on a path crawl stored no site-check reports")
	}
}

// A value outside the three is a config error (exit 2) naming them, before
// any crawl is registered.
func TestCrawlSiteChecksFlagRejectsUnknownValues(t *testing.T) {
	dir := t.TempDir()
	out, code := runCmd(t, "crawl", "https://e.com/", "--store-dir", dir, "--site-checks", "always")
	if code != 2 || !strings.Contains(out, "auto, all or off") {
		t.Errorf("exit %d, output %q; want exit 2 naming the choices", code, out)
	}
	if infos, _ := store.ListCrawls(dir); len(infos) != 0 {
		t.Errorf("a rejected flag still registered crawls: %+v", infos)
	}
}
