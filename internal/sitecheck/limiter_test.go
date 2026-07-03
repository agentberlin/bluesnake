package sitecheck

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/agentberlin/bluesnake/internal/fetch"
	"github.com/agentberlin/bluesnake/internal/limiter"
)

func newCappedChecker(t *testing.T, lim *limiter.Limiter) *Checker {
	t.Helper()
	cfg := config.Default()
	client, err := fetch.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return New(cfg, client, WithLimiter(lim))
}

// A cap-1 fetch pool across a check that fetches many times (robots + control
// + one probe per bot): a single leaked slot would deadlock the second fetch,
// so completing under the deadline proves every fetch releases its slot.
func TestWithLimiterBracketsEveryFetch(t *testing.T) {
	var hits atomic.Int64
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/robots.txt" {
			w.Write([]byte("User-agent: *\nAllow: /\nSitemap: /s.xml\n"))
			return
		}
		w.Write([]byte("<html></html>"))
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	chk := newCappedChecker(t, limiter.New(1, 1, 0))
	rep, err := chk.AIBots(ctx, srv.URL, AIBotOptions{Live: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Bots) == 0 || hits.Load() < 3 {
		t.Fatalf("bots = %d, hits = %d — the probes did not run", len(rep.Bots), hits.Load())
	}
}

// With the fetch pool saturated and the context cancelled, a check degrades to
// an error report without ever reaching the network — report, never fail.
func TestWithLimiterFetchDegradesOnCancel(t *testing.T) {
	var hits atomic.Int64
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Write([]byte("<html><head><title>t</title></head></html>"))
	})
	lim := limiter.New(1, 1, 0)
	if !lim.AcquireFetch(context.Background()) {
		t.Fatal("could not saturate the fetch pool")
	}
	defer lim.ReleaseFetch()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	rep, err := newCappedChecker(t, lim).Serp(ctx, SerpOptions{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rep.FetchError, "fetch slot") || hits.Load() != 0 {
		t.Errorf("fetch error = %q, hits = %d — want a slot-wait error and no request", rep.FetchError, hits.Load())
	}
}

// With the render pool saturated, the render diff keeps its raw fetch (fetch
// and render slots are never held together) and degrades the render itself.
func TestWithLimiterRenderDegradesOnCancel(t *testing.T) {
	srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body><p>raw words here</p></body></html>"))
	})
	lim := limiter.New(0, 1, 1)
	if !lim.AcquireRender(context.Background()) {
		t.Fatal("could not saturate the render pool")
	}
	defer lim.ReleaseRender()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	rep, err := newCappedChecker(t, lim).RenderDiff(ctx, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if rep.FetchStatus != 200 {
		t.Fatalf("fetch status = %d, want the raw fetch to succeed", rep.FetchStatus)
	}
	if rep.Rendered || !strings.Contains(rep.RenderError, "render slot") {
		t.Errorf("rendered = %v, render error = %q — want a slot-wait degrade", rep.Rendered, rep.RenderError)
	}
}
