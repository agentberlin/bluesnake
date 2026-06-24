package runner

import (
	"context"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestStartupErrorMarksCrawlTerminal pins the fix for an orphaned-status bug: when
// crawler.New fails *after* open() has already registered the crawl (here via a
// malformed http.proxy that passes config validation but breaks the fetch client),
// finalize never runs. The executor must still record a terminal status, otherwise
// the registry row claims to be "running" forever even though the dispatcher has
// gone idle.
func TestStartupErrorMarksCrawlTerminal(t *testing.T) {
	dir := t.TempDir()
	e := New(dir, nil)

	_, err := e.Run(context.Background(), queue.JobSpec{
		URL:        "https://e.com/",
		ConfigYAML: "http:\n  proxy: \"://%zz\"\n", // unparseable proxy → crawler.New fails
	}, nil)
	if err == nil {
		t.Fatal("Run with a malformed proxy should error at crawler.New")
	}

	infos, err := store.ListCrawls(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected the failed crawl to be registered once, got %d rows", len(infos))
	}
	if infos[0].Status == store.StatusRunning {
		t.Errorf("crawl left orphaned at %q; a startup failure must record a terminal status", infos[0].Status)
	}
	if infos[0].Status != store.StatusInterrupted {
		t.Errorf("status = %q, want %q", infos[0].Status, store.StatusInterrupted)
	}
}
