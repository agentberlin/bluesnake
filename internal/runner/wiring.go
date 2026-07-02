package runner

import (
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/render"
)

// ProcessWiring resolves the process-level parallel-crawl wiring for a
// dispatcher-owning surface (the desktop app, the standalone MCP server) from
// the saved default profile, read once at dispatcher construction — restart
// the surface to apply a changed knob. It returns how many crawls the
// dispatcher may run at once (speed.max_concurrent_crawls, min 1) and — when
// parallel — the ONE shared process-wide limiter every crawl must run under:
// the global fetch cap is the user's speed.max_global_threads (NOT
// parallel × per-crawl threads, which could never bind — H1), one finalize
// pass at a time (§5.6/H2), and the process-wide Chrome render pool (#76).
// The CLI's `projects crawl-all` resolves the same knob itself (flag > config
// > 1) and builds the identical limiter, so semantics match across surfaces.
//
// With concurrency 1 the limiter is nil: the executor's per-crawl fallback —
// built from each crawl's own config — IS the process-wide cap when only one
// crawl ever runs (the P17 invariant), keeping single-crawl behaviour
// byte-identical to the pre-parallel surfaces.
//
// An unreadable default profile fails safe to (1, nil): the same profile is
// what start jobs load, so the real error surfaces on the first start rather
// than being swallowed here.
func ProcessWiring(storeDir string) (concurrency int, lim *limiter.Limiter, err error) {
	cfg, err := LoadProfile(storeDir, "")
	if err != nil {
		return 1, nil, err
	}
	w := cfg.Speed.MaxConcurrentCrawls
	if w < 1 {
		w = 1
	}
	if w == 1 {
		return 1, nil, nil
	}
	return w, limiter.New(cfg.Speed.MaxGlobalThreads, 1, render.GlobalRenderCap(cfg)), nil
}
