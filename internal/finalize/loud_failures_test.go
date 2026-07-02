package finalize

// #74 R5: finalize's EC-05 ordering sealed `completed` only after depth and
// analysis were durable — but a SaveInlinksFromEdges failure was merely noted
// and completion proceeded, leaving a `completed` crawl with stale inlinks (a
// gap in the guarantee for the one aggregate finalize itself computes). And if
// the final seal write itself failed, the returned Outcome still claimed
// StatusCompleted while the registry disagreed (R5-sub).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestFinalize_InlinksError_DoesNotSealCompleted: a failed inlinks/
// discovered_from write must abort completion like a depth or analysis
// failure — the crawl stays interrupted so a resume repairs it.
func TestFinalize_InlinksError_DoesNotSealCompleted(t *testing.T) {
	st, c, res, dir, seed := crawlGraphNoFinalize(t)

	// Fault-inject the one write R5 is about: SaveInlinksFromEdges reads the
	// edges table inside its UPDATE; dropping the table fails exactly that
	// transaction while depth (links) and analysis remain healthy.
	if _, err := st.DB().Exec(`DROP TABLE edges`); err != nil {
		t.Fatalf("drop edges: %v", err)
	}

	out, ferr := Crawl(c, st, res, Params{
		StoreDir: dir, Cfg: config.Default(), Seeds: []string{seed}, Completed: true,
	})
	if ferr == nil {
		t.Fatal("expected finalize to fail with the edges table dropped, got nil")
	}
	if out.Status == store.StatusCompleted {
		t.Errorf("Outcome.Status = completed despite the inlinks write failing")
	}
	if got := registryStatus(t, dir, st.ID); got == store.StatusCompleted {
		t.Errorf("crawl sealed %q despite the inlinks write failing — stale inlinks are now sealed as final", got)
	}
}

// TestFinalizeOutcomeStatusMatchesRegistry: when the status write itself fails
// (read-only registry), the returned Outcome must not claim completed — the
// caller-visible status and the registry must never disagree.
func TestFinalizeOutcomeStatusMatchesRegistry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: file permissions are not enforced")
	}
	st, c, res, dir, seed := crawlGraphNoFinalize(t)

	regPath := filepath.Join(dir, "registry.db")
	if err := os.Chmod(regPath, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(regPath, 0o644) })

	out, ferr := Crawl(c, st, res, Params{
		StoreDir: dir, Cfg: config.Default(), Seeds: []string{seed}, Completed: true,
	})
	if ferr == nil {
		t.Fatal("expected finalize to fail with a read-only registry, got nil")
	}
	if out.Status == store.StatusCompleted {
		t.Error("Outcome.Status = completed while the registry write failed — caller-visible status disagrees with the registry")
	}
	if err := os.Chmod(regPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := registryStatus(t, dir, st.ID); got == store.StatusCompleted {
		t.Errorf("registry status = %q, want not-completed after failed seal", got)
	}
}
