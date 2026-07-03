package main

import (
	"testing"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/project"
	"github.com/agentberlin/bluesnake/internal/store"
)

// TestProjectCrawlAllEnqueues pins that "crawl all" enqueues one job per project
// member into the core queue (source=project), driving crawls through the same
// dispatcher as hand-started ones — without the core App depending on the
// project layer. It also pins that the shared setup (profile knobs) reaches
// every member job and that each job's config is frozen at enqueue.
func TestProjectCrawlAllEnqueues(t *testing.T) {
	a := testApp(t)
	a.ensureQueue() // build the queue over the temp store dir (no drain loop in tests)
	pa := NewProjectApp(a)

	s, err := project.Open(a.storeDir)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.CreateProject("", "https://main.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(p.ID, "rival.com", project.RoleCompetitor); err != nil {
		t.Fatal(err)
	}
	s.Close()

	n, err := pa.CrawlAll(p.ID, StartRequest{Threads: 2, Rate: 1, MaxDepth: -1, Rendering: "text"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("CrawlAll enqueued %d jobs, want 2 (main + competitor)", n)
	}

	// every member job carries the shared setup, frozen at enqueue
	raw, err := a.disp.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range raw {
		if j.Spec.ConfigYAML == "" {
			t.Errorf("job %s (%s) was enqueued unfrozen", j.ID, j.Label)
			continue
		}
		cfg, err := config.Load([]byte(j.Spec.ConfigYAML))
		if err != nil {
			t.Fatalf("job %s frozen config: %v", j.ID, err)
		}
		if cfg.Speed.MaxThreads != 2 {
			t.Errorf("job %s max_threads = %d, want the shared setup's 2", j.ID, cfg.Speed.MaxThreads)
		}
	}

	jobs, err := a.ListQueue()
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("queue has %d jobs, want 2", len(jobs))
	}
	domains := map[string]bool{}
	for _, j := range jobs {
		if j.Source != "project" {
			t.Errorf("job %s source = %q, want project", j.ID, j.Source)
		}
		if j.Status != store.JobQueued {
			t.Errorf("job %s status = %q, want queued", j.ID, j.Status)
		}
		domains[j.Label] = true
	}
	if !domains["main.com"] || !domains["rival.com"] {
		t.Errorf("queued member domains = %v, want main.com + rival.com", domains)
	}
}
