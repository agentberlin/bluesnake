package runner

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
)

// allChecksOff is a base with the whole site-check family disabled, so a
// choice that sets a check proves it sets it rather than inheriting a default.
func allChecksOff() *config.Config {
	cfg := config.Default()
	cfg.SiteChecks = config.SiteChecksConfig{Enabled: "auto"}
	return cfg
}

// The per-crawl site-checks choice the desktop's setup card and `crawl
// --site-checks` share. "all" sets every check rather than only the gate, so
// it means the same thing over any base config; auto and off move only the
// gate and leave which checks run to the base.
func TestSiteChecksChoiceOverBaseConfig(t *testing.T) {
	everything := config.SiteChecksConfig{
		Enabled: "always", Robots: true, Sitemap: true, RenderDiff: true,
		AIBots: config.AIBotsChecksConfig{Check: true, LiveProbe: true},
	}
	for _, tt := range []struct {
		choice string
		base   func() *config.Config
		want   config.SiteChecksConfig
	}{
		{SiteChecksAll, allChecksOff, everything},
		{SiteChecksAll, config.Default, everything},
		{SiteChecksAuto, allChecksOff, config.SiteChecksConfig{Enabled: "auto"}},
		{SiteChecksOff, config.Default, func() config.SiteChecksConfig {
			sc := config.Default().SiteChecks
			sc.Enabled = "never"
			return sc
		}()},
	} {
		overrides, err := SiteChecksOverrides(tt.choice)
		if err != nil {
			t.Fatalf("%s: %v", tt.choice, err)
		}
		cfg := tt.base()
		if err := ApplyOverrides(cfg, overrides); err != nil {
			t.Fatalf("%s: %v", tt.choice, err)
		}
		if !equalSiteChecks(cfg.SiteChecks, tt.want) {
			t.Errorf("%s: site_checks = %+v, want %+v", tt.choice, cfg.SiteChecks, tt.want)
		}
	}
	// The registry edits a base carries (extra bots, skips) are not the
	// choice's to touch: "all" turns checks on, it does not reset the roster.
	cfg := config.Default()
	cfg.SiteChecks.AIBots.Skip = []string{"Bytespider"}
	overrides, _ := SiteChecksOverrides(SiteChecksAll)
	if err := ApplyOverrides(cfg, overrides); err != nil {
		t.Fatal(err)
	}
	if len(cfg.SiteChecks.AIBots.Skip) != 1 {
		t.Errorf("all reset the base's ai_bots.skip: %v", cfg.SiteChecks.AIBots.Skip)
	}
}

// A choice outside the three is an error naming them, not a raw value passed
// on to config validation.
func TestSiteChecksChoiceRejectsUnknownValues(t *testing.T) {
	for _, bad := range []string{"", "always", "never", "All", "nonsense"} {
		if _, err := SiteChecksOverrides(bad); err == nil {
			t.Errorf("SiteChecksOverrides(%q) accepted", bad)
		}
	}
}

// BuildConfig applies a spec's overrides through the same ApplyOverrides, so
// a desktop job carrying the choice resolves to the config the CLI builds.
func TestBuildConfigAppliesSiteChecksOverrides(t *testing.T) {
	overrides, err := SiteChecksOverrides(SiteChecksAll)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := BuildConfig(t.TempDir(), queue.JobSpec{Mode: "spider", URL: "https://ex.com/", Config: overrides})
	if err != nil {
		t.Fatal(err)
	}
	if sc := cfg.SiteChecks; sc.Enabled != "always" || !sc.RenderDiff || !sc.AIBots.LiveProbe {
		t.Errorf("site_checks = %+v, want every check on", sc)
	}
}

func equalSiteChecks(a, b config.SiteChecksConfig) bool {
	return a.Enabled == b.Enabled && a.Robots == b.Robots && a.Sitemap == b.Sitemap &&
		a.RenderDiff == b.RenderDiff && a.AIBots.Check == b.AIBots.Check &&
		a.AIBots.LiveProbe == b.AIBots.LiveProbe &&
		len(a.AIBots.Bots) == len(b.AIBots.Bots) && len(a.AIBots.Skip) == len(b.AIBots.Skip)
}
