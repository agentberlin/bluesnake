package main

// #74 N10: a crawl that FAILED TO START reaches OnDone with an empty status
// and an error. The old empty-status default made the desktop UI render it as
// a successful "completed" crawl with an empty crawl id.

import (
	"errors"
	"testing"

	"github.com/agentberlin/bluesnake/internal/runner"
	"github.com/agentberlin/bluesnake/internal/store"
)

func TestDoneStatusFailedStartIsError(t *testing.T) {
	cases := []struct {
		name string
		out  runner.Outcome
		want string
	}{
		{"failed start", runner.Outcome{Err: errors.New("bad seed")}, "error"},
		{"completed", runner.Outcome{Status: store.StatusCompleted}, store.StatusCompleted},
		{"interrupted", runner.Outcome{Status: store.StatusInterrupted}, store.StatusInterrupted},
		// a finalize error on an otherwise-terminal crawl keeps its real status
		{"completed with error", runner.Outcome{Status: store.StatusCompleted, Err: errors.New("finalize")}, store.StatusCompleted},
		{"legacy empty", runner.Outcome{}, store.StatusCompleted},
	}
	for _, c := range cases {
		if got := doneStatus(c.out); got != c.want {
			t.Errorf("%s: doneStatus = %q, want %q", c.name, got, c.want)
		}
	}
}
