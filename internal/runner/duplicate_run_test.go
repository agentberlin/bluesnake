package runner

// #74 N7: Executor.cur is keyed by crawl id. Two resume jobs racing for the
// same crawl used to overwrite each other's map entry, making the first run
// unaddressable — Pause/Stop/Cancel signalled the wrong session and the
// loser's cleanup deleted the winner's registration. The executor must refuse
// the duplicate instead.

import (
	"context"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/internal/queue"
)

func TestExecutorRefusesDuplicateCrawlID(t *testing.T) {
	srv := chainServer(t, 4)
	dir := t.TempDir()
	obs := &recObs{pauseAfter: 2}
	e := New(dir, obs)
	obs.exec = e
	if _, err := e.Run(context.Background(),
		queue.JobSpec{URL: srv.URL + "/", Config: single(1)}, nil); err != nil {
		t.Fatal(err)
	}
	id := obs.startID

	// Simulate the same crawl already being in flight on this executor (the
	// first of two racing resume jobs).
	e.mu.Lock()
	e.cur[id] = &run{}
	e.mu.Unlock()

	_, err := e.Run(context.Background(), queue.JobSpec{ResumeID: id}, nil)
	if err == nil {
		t.Fatal("a second run of an in-flight crawl id was accepted (N7)")
	}
	if !strings.Contains(err.Error(), "already running") {
		t.Errorf("error = %q, want it to name the duplicate session", err)
	}
	// The first session's registration must be untouched (still addressable).
	e.mu.Lock()
	_, ok := e.cur[id]
	e.mu.Unlock()
	if !ok {
		t.Error("the refused duplicate deregistered the in-flight session")
	}
}
