package main

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestListQueueExcludesTerminalJobs pins Option A: the desktop queue view shows
// only outstanding work (queued/running). A finished job stays in the registry
// (for resumability and crawl linkage) but drops out of ListQueue rather than
// lingering as history.
func TestListQueueExcludesTerminalJobs(t *testing.T) {
	a := testApp(t)
	a.ensureQueue() // no drain loop in tests, so enqueued jobs stay queued

	done, err := a.disp.Enqueue(queue.JobSpec{URL: "https://done.com/"}, "manual", "", "done.com")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.disp.Enqueue(queue.JobSpec{URL: "https://pending.com/"}, "manual", "", "pending.com"); err != nil {
		t.Fatal(err)
	}
	// Take the first job terminal directly in the registry.
	if err := store.FinishJob(a.storeDir, done.ID, store.JobDone, ""); err != nil {
		t.Fatal(err)
	}

	items, err := a.ListQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("ListQueue = %d items, want 1 (terminal job excluded): %+v", len(items), items)
	}
	if items[0].Label != "pending.com" || items[0].Status != store.JobQueued {
		t.Errorf("surfaced job = %+v, want the queued pending.com job", items[0])
	}

	// The finished job is retained in the registry, just not surfaced.
	all, err := store.ListJobs(a.storeDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("registry has %d jobs, want 2 (terminal row retained)", len(all))
	}
}
