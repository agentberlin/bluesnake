package runner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/crawler"
	"github.com/agentberlin/bluesnake/internal/frontier"
	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// --- config.go ---

// TestLoadProfileDefaultFile covers LoadProfile's empty-name branch when a saved
// default profile file exists (it must be loaded, not fall back to built-ins).
func TestLoadProfileDefaultFile(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// the default profile's slug is profileSlug(DefaultProfileName)
	slug := profileSlug(DefaultProfileName)
	if err := os.WriteFile(filepath.Join(pdir, slug+".yaml"),
		[]byte("# Default audit\nspeed:\n  max_threads: 11\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadProfile(dir, "")
	if err != nil {
		t.Fatalf("LoadProfile(default file): %v", err)
	}
	if cfg.Speed.MaxThreads != 11 {
		t.Errorf("default profile not loaded from disk: threads=%d, want 11", cfg.Speed.MaxThreads)
	}
}

// TestListProfileNamesMissingDir covers ListProfileNames's ReadDir-error branch
// (no profiles dir => nil).
func TestListProfileNamesMissingDir(t *testing.T) {
	if names := ListProfileNames(t.TempDir()); names != nil {
		t.Errorf("ListProfileNames with no profiles dir = %v, want nil", names)
	}
}

// TestListProfileNamesSkipsAndSorts covers the skip arms (a subdir and a
// non-.yaml file are ignored) and the default-first sort ordering.
func TestListProfileNamesSkipsAndSorts(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(filepath.Join(pdir, "a-subdir"), 0o755); err != nil { // ignored (dir)
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(pdir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("notes.txt", "ignored")                                    // ignored (not .yaml)
	write("zebra.yaml", "speed:\n  max_threads: 1\n")                // no header -> slug name "zebra"
	write("alpha.yaml", "# Alpha Audit\nspeed:\n  max_threads: 1\n") // header name
	write(profileSlug(DefaultProfileName)+".yaml", "# "+DefaultProfileName+"\nspeed:\n  max_threads: 1\n")

	names := ListProfileNames(dir)
	if len(names) != 3 {
		t.Fatalf("ListProfileNames = %v, want 3 entries (txt + subdir skipped)", names)
	}
	if names[0] != DefaultProfileName {
		t.Errorf("default profile must sort first, got %v", names)
	}
	// the remaining two are alphabetical: "Alpha Audit" before "zebra"
	if names[1] != "Alpha Audit" || names[2] != "zebra" {
		t.Errorf("non-default names mis-sorted: %v", names)
	}
}

// TestListProfileNamesDefaultStaysFirst exercises the sort comparator's
// "names[j] is default" arm (return false): the default's filename sorts first
// on disk, so insertion-sort compares a later non-default (i) against it (j) and
// must keep the default ahead.
func TestListProfileNamesDefaultStaysFirst(t *testing.T) {
	dir := t.TempDir()
	pdir := filepath.Join(dir, "profiles")
	if err := os.MkdirAll(pdir, 0o755); err != nil {
		t.Fatal(err)
	}
	// "default-audit.yaml" sorts before "mmm.yaml"/"zzz.yaml" on disk, so the raw
	// order is [Default audit, mmm, zzz] and the comparator hits names[j]==default.
	os.WriteFile(filepath.Join(pdir, profileSlug(DefaultProfileName)+".yaml"),
		[]byte("# "+DefaultProfileName+"\nspeed:\n  max_threads: 1\n"), 0o644)
	os.WriteFile(filepath.Join(pdir, "mmm.yaml"), []byte("speed:\n  max_threads: 1\n"), 0o644)
	os.WriteFile(filepath.Join(pdir, "zzz.yaml"), []byte("speed:\n  max_threads: 1\n"), 0o644)
	names := ListProfileNames(dir)
	if len(names) != 3 || names[0] != DefaultProfileName {
		t.Errorf("default must stay first, got %v", names)
	}
	if names[1] != "mmm" || names[2] != "zzz" {
		t.Errorf("non-default names mis-sorted: %v", names)
	}
}

// TestBuildConfigProfileNotFound covers BuildConfig's LoadProfile-error arm.
func TestBuildConfigProfileNotFound(t *testing.T) {
	if _, err := BuildConfig(t.TempDir(), queue.JobSpec{Profile: "ghost"}); err == nil {
		t.Error("BuildConfig with a missing profile should error")
	}
}

// TestBuildConfigBadOverride covers BuildConfig's cfg.Set-error arm (unknown key).
func TestBuildConfigBadOverride(t *testing.T) {
	_, err := BuildConfig(t.TempDir(), queue.JobSpec{Config: map[string]any{"speed.bogus": 1}})
	if err == nil {
		t.Error("BuildConfig with an unknown override key should error")
	}
}

// TestBuildConfigUnmarshalableValue covers BuildConfig's json.Marshal-error arm:
// a channel value can't be JSON-encoded, so the per-override encode fails.
func TestBuildConfigUnmarshalableValue(t *testing.T) {
	_, err := BuildConfig(t.TempDir(), queue.JobSpec{
		Config: map[string]any{"speed.max_threads": make(chan int)},
	})
	if err == nil || !strings.Contains(err.Error(), "speed.max_threads") {
		t.Errorf("BuildConfig with an unmarshalable value err=%v, want a config[key] error", err)
	}
}

// TestBuildConfigListModeIgnoreRobots covers the list-mode branch that flips
// robots to ignore when the profile's list_mode.respect_robots is false (the
// default), and that overrides win over the list-mode depth adjustment.
func TestBuildConfigListModeIgnoreRobots(t *testing.T) {
	dir := t.TempDir()
	cfg, err := BuildConfig(dir, queue.JobSpec{
		Mode:   "list",
		Config: map[string]any{"limits.max_depth": 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != "list" {
		t.Errorf("mode = %q, want list", cfg.Mode)
	}
	if cfg.Robots.Mode != "ignore" {
		t.Errorf("robots.mode = %q, want ignore (list_mode.respect_robots is false by default)", cfg.Robots.Mode)
	}
	if cfg.Limits.MaxDepth != 5 {
		t.Errorf("override must win over the list-mode depth: max_depth=%d, want 5", cfg.Limits.MaxDepth)
	}
}

// TestValidateSpecConfigYAMLInvalid covers ValidateSpec's ConfigYAML
// validation-error arm (parses but fails Validate).
func TestValidateSpecConfigYAMLInvalid(t *testing.T) {
	err := ValidateSpec(t.TempDir(), queue.JobSpec{
		URL:        "https://e.com/",
		ConfigYAML: "speed:\n  max_threads: 0\n", // parses, fails Validate
	})
	if err == nil || !strings.Contains(err.Error(), "max_threads") {
		t.Errorf("ValidateSpec with an invalid ConfigYAML err=%v, want a max_threads error", err)
	}
}

// TestResolveSeedsBadMode covers ResolveSeeds's default (unknown mode) arm.
func TestResolveSeedsBadMode(t *testing.T) {
	_, _, err := ResolveSeeds(context.Background(), config.Default(), queue.JobSpec{Mode: "weird"})
	if err == nil || !strings.Contains(err.Error(), "mode must be") {
		t.Errorf("ResolveSeeds(bad mode) err=%v, want a mode error", err)
	}
}

// TestResolveSeedsBadSpiderURL covers ResolveSeeds's spider URL guard.
func TestResolveSeedsBadSpiderURL(t *testing.T) {
	_, _, err := ResolveSeeds(context.Background(), config.Default(), queue.JobSpec{URL: "ftp://nope"})
	if err == nil {
		t.Error("ResolveSeeds with a non-http URL should error")
	}
}

// TestResolveSeedsSitemapFetchError covers ResolveSeeds's sitemap-fetch error arm
// by pointing at a server that 500s the sitemap.
func TestResolveSeedsSitemapFetchError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	_, _, err := ResolveSeeds(context.Background(), config.Default(),
		queue.JobSpec{Mode: "list", SitemapURL: srv.URL + "/sitemap.xml"})
	if err == nil || !strings.Contains(err.Error(), "sitemap fetch") {
		t.Errorf("ResolveSeeds sitemap 500 err=%v, want a sitemap-fetch error", err)
	}
}

// --- runner.go ---

// TestErrNoSeedMessage covers errNoSeed.Error().
func TestErrNoSeedMessage(t *testing.T) {
	if got := errNoSeed("abc123").Error(); got != "crawl abc123 has no stored seed" {
		t.Errorf("errNoSeed.Error() = %q", got)
	}
}

// TestSignalIdleNoop covers signal's r==nil (idle) guard via the public Pause/Stop.
func TestSignalIdleNoop(t *testing.T) {
	e := New(t.TempDir(), nil)
	e.Pause() // must not panic when idle
	e.Stop()
	if _, ok := e.Snapshot(); ok {
		t.Error("Snapshot ok=true on an idle executor")
	}
}

// TestOpenForResumeMissingSeed covers openForResume's empty-seed arm (errNoSeed):
// a crawl whose stored seeds were cleared.
func TestOpenForResumeMissingSeed(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"https://e.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.SetMeta("seeds", ""); err != nil { // clear the stored seeds
		t.Fatal(err)
	}
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusInterrupted, 0, 0); err != nil {
		t.Fatal(err)
	}
	_, err = New(dir, nil).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil)
	if err == nil || !strings.Contains(err.Error(), "no stored seed") {
		t.Errorf("resume of a seedless crawl err=%v, want a no-seed error", err)
	}
}

// TestOpenForResumeBadSeedsJSON covers openForResume's st.Seeds() error arm: the
// stored "seeds" meta holds malformed JSON, so the unmarshal inside Seeds fails
// (distinct from the empty-seeds errNoSeed path).
func TestOpenForResumeBadSeedsJSON(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"https://e.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	if err := st.SetMeta("seeds", "{not valid json"); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusInterrupted, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, nil).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil); err == nil {
		t.Error("resume with malformed seeds JSON should error")
	}
}

// TestOpenForResumeMissingConfigMeta covers openForResume's Meta("config") error
// arm: a crawl created then stripped of its frozen config meta.
func TestOpenForResumeMissingConfigMeta(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"https://e.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	id := st.ID
	// blank the config meta so Load([]byte("")) yields defaults but the seeds path
	// still runs; to force the *config-load* error path instead, store garbage:
	if err := st.SetMeta("config", "speed:\n  : :\n"); err != nil { // unparseable YAML
		t.Fatal(err)
	}
	st.Close()
	if err := store.SetStatus(dir, id, store.StatusInterrupted, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := New(dir, nil).Run(context.Background(), queue.JobSpec{ResumeID: id}, nil); err == nil {
		t.Error("resume with an unparseable frozen config should error")
	}
}

// TestOpenSpiderConfigYAML covers open's ConfigYAML branch (a spider crawl whose
// config comes from a frozen YAML blob rather than a profile) by running it
// end-to-end against a fixture server.
func TestOpenSpiderConfigYAML(t *testing.T) {
	srv := fixtureServer(t)
	dir := t.TempDir()
	status, err := New(dir, nil).Run(context.Background(), queue.JobSpec{
		URL:        srv.URL + "/",
		ConfigYAML: "speed:\n  max_threads: 1\n",
	}, nil)
	if err != nil {
		t.Fatalf("Run with ConfigYAML: %v", err)
	}
	if status != store.StatusCompleted {
		t.Errorf("status = %q, want completed", status)
	}
}

// TestOpenSpiderConfigYAMLInvalid covers open's ConfigYAML error arm (Load fails),
// and confirms the open-failure terminal OnDone fires.
func TestOpenSpiderConfigYAMLInvalid(t *testing.T) {
	dir := t.TempDir()
	obs := &recObs{}
	e := New(dir, obs)
	_, err := e.Run(context.Background(), queue.JobSpec{
		URL:        "https://e.com/",
		ConfigYAML: "speed:\n  max_threads: 0\n", // fails Validate inside Load
	}, nil)
	if err == nil {
		t.Fatal("Run with an invalid ConfigYAML should error")
	}
	obs.mu.Lock()
	defer obs.mu.Unlock()
	if obs.dones != 1 {
		t.Errorf("OnDone fired %d times on open failure, want 1", obs.dones)
	}
}

// TestRunCrawlerNewError covers Run's crawler.New-error arm: a malformed
// http.proxy passes config validation (proxy isn't validated) but fails when the
// fetch client is built inside crawler.New, after open() has already succeeded.
func TestRunCrawlerNewError(t *testing.T) {
	dir := t.TempDir()
	obs := &recObs{}
	e := New(dir, obs)
	_, err := e.Run(context.Background(), queue.JobSpec{
		URL:        "https://e.com/",
		ConfigYAML: "http:\n  proxy: \"://%zz\"\n", // unparseable proxy URL
	}, nil)
	if err == nil {
		t.Fatal("Run with a malformed proxy should error at crawler.New")
	}
	// crawler.New failing happens after open(), so the registry got a crawl row;
	// the important pin is that Run surfaces the error rather than panicking.
}

// --- sink / counters ---

// TestSinkPageErrorPropagates covers the sink's Page inner-error arm: when the
// store can't persist (its DB is closed) the tee returns the error and does not
// bump live counters.
func TestSinkPageErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := &run{st: st}
	s := &sink{Crawl: st, r: r}
	st.Close() // make the inner store fail

	rec := &crawler.PageRecord{URL: "http://ex.com/p", StatusCode: 200, State: crawler.StateCrawled}
	if err := s.Page(rec); err == nil {
		t.Error("sink.Page should propagate the inner store error")
	}
	if r.total != 0 {
		t.Errorf("live counter advanced despite a persist failure: total=%d", r.total)
	}
}

// TestSinkAdmitErrorPropagates covers the sink's Admit (dedup authority)
// inner-error arm: when the store can't persist, Admit returns the error and the
// Discovered counter does not advance.
func TestSinkAdmitErrorPropagates(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	r := &run{st: st}
	s := &sink{Crawl: st, r: r}
	st.Close()

	if _, err := s.Admit(frontier.Item{URL: "http://ex.com/q"}); err == nil {
		t.Error("sink.Admit should propagate the inner store error")
	}
	if r.discovered != 0 {
		t.Errorf("discovered counter advanced despite a persist failure: %d", r.discovered)
	}
}

// TestSinkPageAndFrontierHappy covers the sink's success arms (counters bump,
// observer hook fires) without going through a full crawl.
func TestSinkPageAndFrontierHappy(t *testing.T) {
	dir := t.TempDir()
	st, err := store.CreateCrawl(dir, []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	obs := &recObs{}
	r := &run{st: st}
	s := &sink{Crawl: st, r: r, obs: obs}

	if err := s.Page(&crawler.PageRecord{URL: "http://ex.com/a", StatusCode: 200, State: crawler.StateCrawled, Indexable: true}); err != nil {
		t.Fatal(err)
	}
	if first, err := s.Admit(frontier.Item{URL: "http://ex.com/b"}); err != nil || !first {
		t.Fatalf("Admit(novel) = (%v, %v), want (true, nil)", first, err)
	}
	if r.total != 1 || r.discovered != 1 || r.s2 != 1 || r.indexable != 1 {
		t.Errorf("counters after one page+frontier: %+v", r)
	}
	obs.mu.Lock()
	if obs.pages != 1 {
		t.Errorf("observer OnPage fired %d times, want 1", obs.pages)
	}
	obs.mu.Unlock()

	// The remaining Dedup forwarders: Seen and Count read through to the store,
	// and Remove (the cap-overflow rollback) rolls back the Discovered bump.
	if seen, err := s.Seen("http://ex.com/b"); err != nil || !seen {
		t.Errorf("Seen(admitted) = (%v,%v), want (true,nil)", seen, err)
	}
	if err := s.MarkSeen([]string{"http://ex.com/a"}); err != nil {
		t.Errorf("MarkSeen: %v", err)
	}
	if n, err := s.Count(); err != nil || n < 1 {
		t.Errorf("Count = (%d,%v), want (>=1,nil)", n, err)
	}
	if err := s.Remove("http://ex.com/b"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if r.discovered != 0 {
		t.Errorf("discovered after Remove = %d, want 0 (rollback of the Admit bump)", r.discovered)
	}
}

// TestOnPageStateBuckets covers onPage's blocked/error/status-bucket arms
// directly (StateBlockedRobots -> blocked, StateError -> noresp, and 5xx).
func TestOnPageStateBuckets(t *testing.T) {
	r := &run{}
	r.onPage(&crawler.PageRecord{State: crawler.StateBlockedRobots})
	r.onPage(&crawler.PageRecord{State: crawler.StateError})
	r.onPage(&crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 503})
	r.onPage(&crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 404})
	r.onPage(&crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 301})
	r.onPage(&crawler.PageRecord{State: crawler.StateCrawled, StatusCode: 200, Indexable: true})

	if r.blocked != 1 {
		t.Errorf("blocked = %d, want 1", r.blocked)
	}
	if r.noresp != 1 {
		t.Errorf("noresp = %d, want 1", r.noresp)
	}
	if r.s5 != 1 || r.s4 != 1 || r.s3 != 1 || r.s2 != 1 {
		t.Errorf("status buckets s2=%d s3=%d s4=%d s5=%d, want 1 each", r.s2, r.s3, r.s4, r.s5)
	}
	if r.indexable != 1 {
		t.Errorf("indexable = %d, want 1", r.indexable)
	}
	if r.total != 6 {
		t.Errorf("total = %d, want 6", r.total)
	}
}

// TestSnapshotRateWindowAndClamp covers snapshot's stale-sample eviction (the
// 4s window drops old timestamps) and the negative-queue clamp.
func TestSnapshotRateWindowAndClamp(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &run{
		st:         st,
		seeds:      []string{"http://ex.com/"},
		started:    time.Now().Add(-2 * time.Second),
		total:      10, // more processed than discovered -> queue would go negative
		discovered: 3,
		recent: []time.Time{
			time.Now().Add(-10 * time.Second), // stale: evicted
			time.Now().Add(-5 * time.Second),  // stale: evicted
			time.Now().Add(-1 * time.Second),  // fresh: counted
			time.Now(),                        // fresh: counted
		},
	}
	snap := r.snapshot()
	if snap.Queue != 0 {
		t.Errorf("Queue = %d, want 0 (clamped)", snap.Queue)
	}
	if len(r.recent) != 2 {
		t.Errorf("stale samples not evicted: recent has %d, want 2", len(r.recent))
	}
	if snap.RatePerSec != float64(2)/4.0 {
		t.Errorf("RatePerSec = %v, want %v (2 fresh / 4s)", snap.RatePerSec, float64(2)/4.0)
	}
	if snap.Seed != "http://ex.com/" {
		t.Errorf("Seed = %q", snap.Seed)
	}
	if snap.ElapsedSec < 2 {
		t.Errorf("ElapsedSec = %d, want >= 2", snap.ElapsedSec)
	}
}

// TestSnapshotNoSeed covers snapshot's empty-seeds arm (seed stays "").
func TestSnapshotNoSeed(t *testing.T) {
	st, err := store.CreateCrawl(t.TempDir(), []string{"http://ex.com/"}, "spider", config.Default())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	r := &run{st: st, started: time.Now()}
	if snap := r.snapshot(); snap.Seed != "" {
		t.Errorf("Seed = %q, want empty when no seeds", snap.Seed)
	}
}
