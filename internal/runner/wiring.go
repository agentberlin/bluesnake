package runner

import (
	"github.com/agentberlin/bluesnake/internal/limiter"
	"github.com/agentberlin/bluesnake/internal/render"
)

// ProcessWiring resolves the process-level parallel-crawl wiring for a
// dispatcher-owning surface (the desktop app, the standalone MCP server) from
// the saved default profile, read once at dispatcher construction. It returns
// how many crawls the dispatcher may run at once (speed.max_concurrent_crawls;
// 0 = unlimited, the default) and the ONE shared process-wide limiter every
// crawl must run under:
// the global fetch cap is the user's speed.max_global_threads (NOT
// parallel × per-crawl threads, which could never bind — H1), one finalize
// pass at a time (§5.6/H2), and the process-wide Chrome render pool (#76).
//
// The limiter is returned even when the width resolves to 1: the width is live
// (queue.Dispatcher.SetConcurrency can raise it at any time, no restart), so
// the executor's per-crawl fallback — sound only while one crawl ever runs
// (P17) — cannot be relied on by these surfaces. The limiter's caps are all
// width-independent, so one limiter built here stays valid across every
// retarget; changing max_global_threads or the render cap still needs a
// restart. Only the fixed single-crawl surfaces (the CLI's one-shot `crawl` /
// list commands) run without an injected limiter.
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
	if w < 0 {
		w = 0 // <= 0 = unlimited, the limiter convention
	}
	return w, limiter.New(cfg.Speed.MaxGlobalThreads, 1, render.GlobalRenderCap(cfg)), nil
}
