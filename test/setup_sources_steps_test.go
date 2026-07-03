package acceptance

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/cucumber/godog"
)

func (w *world) registerSetupSourceSteps(sc *godog.ScenarioContext) {
	sc.Step(`^a site was crawled with max depth (\d+)$`, w.siteCrawledWithDepth)
	sc.Step(`^the app settings set max depth (\d+)$`, w.appSettingsDepth)
	sc.Step(`^a profile "([^"]*)" with max depth (\d+)$`, w.profileWithDepth)
	sc.Step(`^a crawl of the same site is enqueued with the last-used setup$`, w.enqueueSameSiteLast)
	sc.Step(`^a crawl of an uncrawled site is enqueued with the last-used setup$`, w.enqueueUncrawledLast)
	sc.Step(`^a crawl of the same site is enqueued with profile "([^"]*)"$`, w.enqueueSameSiteProfile)
	sc.Step(`^a crawl of the www subdomain of the site is enqueued with the last-used setup$`, w.enqueueWWWLast)
	sc.Step(`^the enqueued job freezes max depth (\d+)$`, w.frozenDepthIs)
	sc.Step(`^the enqueued job freezes the default max depth$`, w.frozenDepthDefault)
}

// siteCrawledWithDepth runs a real single-page crawl through the queue with
// the given depth override, leaving behind exactly what any crawl leaves: a
// registry row and a crawl database with the effective config frozen in — the
// two facts the "last-used setup" source is derived from.
func (w *world) siteCrawledWithDepth(depth int) error {
	srv := w.ensureServer()
	r := w.route("/")
	r.status, r.body = 200, `<html><head><title>p</title></head><body>x</body></html>`

	spec, err := runner.FreezeSpec(w.storeDirPath(), queue.JobSpec{
		URL:    srv.URL + "/",
		Config: map[string]any{"limits.max_depth": depth, "speed.max_threads": 1},
	})
	if err != nil {
		return err
	}
	obs := &queueObserver{total: 1, done: make(chan struct{})}
	disp := queue.New(queue.NewMemStore(), runner.New(w.storeDirPath(), obs))
	if err := disp.Start(context.Background()); err != nil {
		return err
	}
	if _, err := disp.Enqueue(spec, "manual", "", srv.URL); err != nil {
		return err
	}
	<-obs.done
	disp.Shutdown()
	w.setupSeed = srv.URL + "/"
	return nil
}

func (w *world) appSettingsDepth(depth int) error {
	return w.writeSetupProfile(runner.DefaultProfileName, depth)
}

func (w *world) profileWithDepth(name string, depth int) error {
	return w.writeSetupProfile(name, depth)
}

// writeSetupProfile saves a named profile the way the desktop app does
// ("# Name" header + YAML body, slug filename).
func (w *world) writeSetupProfile(name string, depth int) error {
	slug := strings.Trim(strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, strings.ToLower(name)), "-")
	dir := filepath.Join(w.storeDirPath(), "profiles")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("# %s\nlimits:\n  max_depth: %d\n", name, depth)
	return os.WriteFile(filepath.Join(dir, slug+".yaml"), []byte(body), 0o644)
}

// freezeSetup enqueues-in-miniature: it resolves and freezes the spec exactly
// the way every surface's enqueue path does (runner.FreezeSpec), keeping the
// frozen job for the Then assertions.
func (w *world) freezeSetup(spec queue.JobSpec) error {
	frozen, err := runner.FreezeSpec(w.storeDirPath(), spec)
	if err != nil {
		return err
	}
	w.frozenSetup = frozen
	return nil
}

func (w *world) enqueueSameSiteLast() error {
	return w.freezeSetup(queue.JobSpec{URL: w.setupSeed, ConfigSource: "last"})
}

func (w *world) enqueueUncrawledLast() error {
	return w.freezeSetup(queue.JobSpec{URL: "https://uncrawled.example/", ConfigSource: "last"})
}

func (w *world) enqueueSameSiteProfile(name string) error {
	return w.freezeSetup(queue.JobSpec{URL: w.setupSeed, Profile: name})
}

func (w *world) enqueueWWWLast() error {
	u, err := url.Parse(w.setupSeed)
	if err != nil {
		return err
	}
	return w.freezeSetup(queue.JobSpec{URL: "https://www." + u.Host + "/", ConfigSource: "last"})
}

func (w *world) frozenDepthIs(depth int) error {
	cfg, err := config.Load([]byte(w.frozenSetup.ConfigYAML))
	if err != nil {
		return err
	}
	if cfg.Limits.MaxDepth != depth {
		return fmt.Errorf("frozen max_depth = %d, want %d", cfg.Limits.MaxDepth, depth)
	}
	return nil
}

func (w *world) frozenDepthDefault() error {
	return w.frozenDepthIs(config.Default().Limits.MaxDepth)
}
