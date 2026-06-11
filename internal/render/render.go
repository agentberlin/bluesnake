// Package render drives headless Chrome (via the DevTools protocol) for
// JavaScript rendering mode: it loads a page, waits for the network to go
// idle (capped by the configured AJAX timeout), and returns the rendered
// DOM, console errors and an optional screenshot. The crawler parses raw
// and rendered HTML separately and diffs them (the JavaScript tab data).
package render

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// networkIdleWindow is how long the wire must stay quiet (no in-flight
// requests) after page load before the DOM is considered settled.
const networkIdleWindow = 500 * time.Millisecond

// Result is one rendered page.
type Result struct {
	HTML          string
	ConsoleErrors []string
	Screenshot    []byte
	JSResults     []JSResult // custom JS extraction snippet values
	XHRURLs       []string   // GET XHR/fetch request URLs observed while rendering
}

// JSResult is one custom JS extraction snippet's value for a page: JS strings
// verbatim, anything else compact JSON, "error: ..." when the snippet threw.
type JSResult struct {
	Name  string
	Value string
}

// snippet is one custom_js entry with its source loaded from disk.
type snippet struct {
	cfg    config.CustomJS
	source string
}

// Renderer owns a headless Chrome allocator; each Render call runs in its
// own tab. Safe for concurrent use (bounded internally).
type Renderer struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	cfg         *config.Config
	sem         chan struct{}
	snippets    []snippet
}

// ChromePath locates a Chrome/Chromium binary (config override first).
func ChromePath(cfg *config.Config) string {
	if cfg.Rendering.ChromePath != "" {
		return cfg.Rendering.ChromePath
	}
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser",
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	for _, name := range []string{"google-chrome", "chromium", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// New starts the Chrome allocator. Errors when no Chrome can be found.
func New(cfg *config.Config) (*Renderer, error) {
	path := ChromePath(cfg)
	if path == "" {
		return nil, fmt.Errorf("rendering.mode=javascript requires Chrome/Chromium (set rendering.chrome_path)")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(path),
		chromedp.Flag("headless", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.IgnoreCertErrors,
		chromedp.UserAgent(cfg.HTTP.UserAgent),
	)
	if w, h := cfg.Rendering.WindowWidth, cfg.Rendering.WindowHeight; w > 0 && h > 0 {
		opts = append(opts, chromedp.WindowSize(w, h))
	} else {
		opts = append(opts, chromedp.WindowSize(1024, 768)) // googlebot-desktop preset
	}
	// custom JS snippets load once, at construction: a missing file is a
	// config error, not a per-page one
	var snippets []snippet
	for _, cj := range cfg.CustomJS {
		src, err := os.ReadFile(cj.File)
		if err != nil {
			return nil, fmt.Errorf("custom_js %q: %w", cj.Name, err)
		}
		snippets = append(snippets, snippet{cfg: cj, source: string(src)})
	}
	allocCtx, cancel := chromedp.NewExecAllocator(context.Background(), opts...)
	concurrency := min(cfg.Speed.MaxThreads, maxTabs())
	return &Renderer{
		allocCtx:    allocCtx,
		allocCancel: cancel,
		cfg:         cfg,
		sem:         make(chan struct{}, concurrency),
		snippets:    snippets,
	}, nil
}

func maxTabs() int {
	switch cores := runtime.NumCPU(); {
	case cores < 4:
		return 2
	case cores < 8:
		return 4
	default:
		return 8 // each tab costs ~100-300MB; plenty for modern desktops
	}
}

func (r *Renderer) Close() { r.allocCancel() }

// settleTracker watches page lifecycle + network events on a tab so Render
// can snapshot as soon as the DOM settles instead of sleeping the full AJAX
// timeout. "Settled" means DOMContentLoaded has fired and either (a) no
// countable request is in flight for networkIdleWindow, or (b) nothing has
// happened on the wire for stuckIdleWindow — which absorbs background video
// streams, third-party widget iframes and other long-lived requests that
// never finish.
type settleTracker struct {
	mu       sync.Mutex
	inflight map[network.RequestID]network.ResourceType
	last     time.Time // last lifecycle or network state change
	dcl      bool      // DOMContentLoaded observed
	loaded   bool      // load event observed (fixed wait strategy)
}

// stuckIdleWindow is how long the wire must produce no events at all before
// permanently-open requests (streams, widgets) are written off as settled.
const stuckIdleWindow = 1500 * time.Millisecond

// ignorableRequest filters request kinds that routinely stay open forever
// and say nothing about whether the DOM is still changing.
func ignorableRequest(t network.ResourceType, url string) bool {
	switch t {
	case network.ResourceTypeMedia, network.ResourceTypeWebSocket,
		network.ResourceTypeEventSource, network.ResourceTypePing,
		network.ResourceTypePrefetch:
		return true
	}
	return strings.HasPrefix(url, "blob:") || strings.HasPrefix(url, "data:")
}

func trackSettle(tabCtx context.Context) *settleTracker {
	tr := &settleTracker{inflight: map[network.RequestID]network.ResourceType{}, last: time.Now()}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		switch e := ev.(type) {
		case *page.EventDomContentEventFired:
			tr.dcl = true
		case *page.EventLoadEventFired:
			tr.loaded = true
		case *network.EventRequestWillBeSent:
			if ignorableRequest(e.Type, e.Request.URL) {
				return
			}
			tr.inflight[e.RequestID] = e.Type
		case *network.EventLoadingFinished:
			delete(tr.inflight, e.RequestID)
		case *network.EventLoadingFailed:
			delete(tr.inflight, e.RequestID)
		case *network.EventRequestServedFromCache:
			delete(tr.inflight, e.RequestID)
		default:
			return
		}
		tr.last = time.Now()
	})
	return tr
}

// pendingDOMWork reports whether any in-flight request could still mutate
// the DOM (scripts, stylesheets, XHR/fetch). Stuck widget iframes, beacons
// and streams don't count.
func (tr *settleTracker) pendingDOMWork() bool {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	for _, t := range tr.inflight {
		switch t {
		case network.ResourceTypeXHR, network.ResourceTypeFetch,
			network.ResourceTypeScript, network.ResourceTypeStylesheet:
			return true
		}
	}
	return false
}

// wait blocks until the page settles. dclWait caps the wait for
// DOMContentLoaded; settleWait (the AJAX timeout) caps the quiet-down phase
// after it. Settled means any of: the network goes fully idle, the DOM stops
// growing while nothing DOM-affecting is in flight (third-party widgets and
// analytics can chatter forever), or the wire goes completely silent. All
// paths degrade to snapshotting whatever has rendered at the cap.
func (tr *settleTracker) wait(ctx context.Context, dclWait, settleWait time.Duration) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	dclDeadline := time.Now().Add(dclWait)
	var settleDeadline time.Time
	var lastPoll time.Time
	var nodeCount, stablePolls int
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			tr.mu.Lock()
			dcl, inflight, last := tr.dcl, len(tr.inflight), tr.last
			tr.mu.Unlock()
			now := time.Now()
			if !dcl {
				if now.After(dclDeadline) {
					return nil
				}
				continue
			}
			if settleDeadline.IsZero() {
				settleDeadline = now.Add(settleWait)
			}
			quiet := now.Sub(last)
			switch {
			case inflight == 0 && quiet >= networkIdleWindow:
				return nil
			case quiet >= stuckIdleWindow:
				return nil // only permanently-open requests remain
			case now.After(settleDeadline):
				return nil // cap reached — snapshot whatever has rendered
			}
			// DOM-stability probe every 500ms: two consecutive identical
			// node counts with no DOM-affecting request pending = settled.
			if now.Sub(lastPoll) >= 500*time.Millisecond {
				lastPoll = now
				var n int
				if err := chromedp.Evaluate("document.getElementsByTagName('*').length", &n).Do(ctx); err == nil && n > 0 {
					if n == nodeCount {
						stablePolls++
					} else {
						nodeCount, stablePolls = n, 0
					}
					if stablePolls >= 2 && !tr.pendingDOMWork() {
						return nil
					}
				}
			}
		}
	}
}

// waitFixed implements rendering.wait_strategy=fixed (DESIGN.md §8): wait
// for the browser load event (capped by loadWait), then sleep the FULL AJAX
// timeout before snapshotting. Slower than adaptive settling but the
// snapshot moment is deterministic, which keeps `compare` runs stable on
// pages with flaky widgets.
func (tr *settleTracker) waitFixed(ctx context.Context, loadWait, sleep time.Duration) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	deadline := time.Now().Add(loadWait)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
		tr.mu.Lock()
		loaded := tr.loaded
		tr.mu.Unlock()
		if loaded || time.Now().After(deadline) {
			break
		}
	}
	select {
	case <-time.After(sleep):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runCustomJS executes the loaded custom_js snippets after the page settled:
// action snippets first (results discarded — they exist to mutate the page),
// then extraction snippets, each bounded by its own timeout.
func (r *Renderer) runCustomJS(ctx context.Context, res *Result) {
	run := func(s snippet) (json.RawMessage, error) {
		timeout := time.Duration(s.cfg.TimeoutSec) * time.Second
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		sctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var raw json.RawMessage
		err := chromedp.Evaluate(s.source, &raw).Do(sctx)
		return raw, err
	}
	for _, s := range r.snippets {
		if s.cfg.Type == "action" {
			run(s) // best-effort: a broken action must not kill the render
		}
	}
	for _, s := range r.snippets {
		if s.cfg.Type != "extraction" {
			continue
		}
		raw, err := run(s)
		value := ""
		switch {
		case err != nil:
			value = "error: " + err.Error()
		case len(raw) > 0 && raw[0] == '"': // JS strings verbatim, not JSON-quoted
			var str string
			if json.Unmarshal(raw, &str) == nil {
				value = str
			} else {
				value = string(raw)
			}
		default:
			value = string(raw)
		}
		res.JSResults = append(res.JSResults, JSResult{Name: s.cfg.Name, Value: value})
	}
}

// Render loads the URL, waits for the network to settle (at most the AJAX
// timeout), and snapshots the DOM.
func (r *Renderer) Render(ctx context.Context, url string) (*Result, error) {
	r.sem <- struct{}{}
	defer func() { <-r.sem }()

	tabCtx, cancel := chromedp.NewContext(r.allocCtx)
	defer cancel()
	// the tab budget must cover the wait phase AND every custom JS snippet's
	// own timeout, or a slow snippet (within its documented timeout_sec) would
	// blow the deadline mid-snippet and abort the whole render — losing the
	// rendered HTML, the JS diff and the other snippets' results
	budget := r.cfg.Advanced.ResponseTimeoutSec + r.cfg.Rendering.AjaxTimeoutSec + 10
	for _, s := range r.snippets {
		if s.cfg.TimeoutSec > 0 {
			budget += s.cfg.TimeoutSec
		} else {
			budget += 5 // matches runCustomJS's default
		}
	}
	timeout := time.Duration(budget) * time.Second
	tabCtx, cancelTimeout := context.WithTimeout(tabCtx, timeout)
	defer cancelTimeout()
	go func() { // propagate caller cancellation
		select {
		case <-ctx.Done():
			cancel()
		case <-tabCtx.Done():
		}
	}()

	res := &Result{}
	if r.cfg.Rendering.JSErrorReporting {
		var mu sync.Mutex
		chromedp.ListenTarget(tabCtx, func(ev any) {
			if e, ok := ev.(*cdpruntime.EventConsoleAPICalled); ok && e.Type == "error" {
				mu.Lock()
				for _, arg := range e.Args {
					res.ConsoleErrors = append(res.ConsoleErrors, string(arg.Value))
				}
				mu.Unlock()
			}
		})
	}

	// XHR/fetch GETs the page makes while rendering are discovered URLs
	// (Screaming Frog parity); POSTs and data:/blob: schemes are not.
	var xhrMu sync.Mutex
	xhrSeen := map[string]bool{}
	chromedp.ListenTarget(tabCtx, func(ev any) {
		e, ok := ev.(*network.EventRequestWillBeSent)
		if !ok || e.Request.Method != "GET" {
			return
		}
		if e.Type != network.ResourceTypeXHR && e.Type != network.ResourceTypeFetch {
			return
		}
		u := e.Request.URL
		if strings.HasPrefix(u, "blob:") || strings.HasPrefix(u, "data:") {
			return
		}
		xhrMu.Lock()
		if !xhrSeen[u] {
			xhrSeen[u] = true
			res.XHRURLs = append(res.XHRURLs, u)
		}
		xhrMu.Unlock()
	})

	settle := trackSettle(tabCtx)
	actions := []chromedp.Action{
		network.Enable(),
		// Navigate without waiting for the browser load event — heavy
		// media can hold it open for many seconds after the DOM is done.
		// settle.wait keys off DOMContentLoaded + network quiet instead.
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, _, errText, _, err := page.Navigate(url).Do(ctx)
			if err != nil {
				return err
			}
			if errText != "" {
				return fmt.Errorf("navigate %s: %s", url, errText)
			}
			return nil
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			loadWait := time.Duration(r.cfg.Advanced.ResponseTimeoutSec) * time.Second
			settleWait := time.Duration(r.cfg.Rendering.AjaxTimeoutSec) * time.Second
			if r.cfg.Rendering.WaitStrategy == "fixed" {
				return settle.waitFixed(ctx, loadWait, settleWait)
			}
			return settle.wait(ctx, loadWait, settleWait)
		}),
		chromedp.ActionFunc(func(ctx context.Context) error {
			r.runCustomJS(ctx, res)
			return nil
		}),
		chromedp.OuterHTML("html", &res.HTML),
	}
	if r.cfg.Rendering.Screenshots {
		actions = append(actions, chromedp.FullScreenshot(&res.Screenshot, 80))
	}
	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return nil, err
	}
	return res, nil
}
