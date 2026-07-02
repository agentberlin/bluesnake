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

// GlobalRenderCap resolves rendering.max_global_renders into the process-wide
// render-slot cap (REN-01/#76): explicit positive values pass through; 0 (the
// default) means "auto" — the same cores-scaled tab ceiling that already bounds
// a single crawl's renderer (min(threads, 2/4/8) tabs), so one crawl never
// notices the global cap while M parallel rendered crawls stay bounded out of
// the box. Chrome-tab sizing is this package's knowledge; the limiter stays a
// generic slot pool and receives the resolved value.
func GlobalRenderCap(cfg *config.Config) int {
	if n := cfg.Rendering.MaxGlobalRenders; n > 0 {
		return n
	}
	return maxTabs()
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

// deferredTimerShim wraps window.setTimeout/clearTimeout so the adaptive settle
// loop can tell when JavaScript still has DOM work scheduled on a timer with no
// accompanying network request (R9a). Network-idle is the wrong sole settle
// signal for SPAs that inject content via setTimeout: the wire goes quiet ~500ms
// after DOMContentLoaded, long before a setTimeout(…, 1500) fires. The shim
// maintains window.__bsPendingTimers, a live count of *in-window* one-shot
// timers, which wait() reads each tick and treats like an in-flight request.
//
// Deliberately bounded so the latency cost lands only where waiting is correct.
// Only a page's *own initial* deferred work is counted; a timer is NOT counted
// when:
//   - it is a setInterval (analytics/polling chatter would never end — the shim
//     simply doesn't wrap setInterval);
//   - its delay exceeds the AJAX cap (a "show popup in 30s" timer can't fire
//     in-window, so it must not hold the snapshot);
//   - it is re-armed from inside another timer's callback (depth > 0). A
//     self-rescheduling animation/poll loop (carousel, countdown, rAF-via-
//     setTimeout) would otherwise pin the count above zero forever and dwell to
//     the cap; only the first, top-level schedule of such a chain is counted, so
//     once it fires the page settles on its DOM-stability/network signals.
//
// The AJAX cap still bounds everything, so even a pathological case never hangs.
//
// KNOWN RESIDUALS (deferred DOM work the shim does not wait for — bounded, rare,
// and never a hang): string-code timers (setTimeout("…", d) — legacy, eval-gated
// by CSP); and DOM injected via requestAnimationFrame / queueMicrotask / a
// promise chain rather than setTimeout. These mirror Screaming Frog only when it
// happens to land inside SF's fixed dwell; wait_strategy=fixed is the escape
// hatch if a site needs the full dwell.
//
// The wrapper is transparent: every function callback runs inside it (so re-arm
// depth is tracked), it forwards arguments and `this`, returns the native id,
// and clearTimeout still cancels (decrementing the count, floored at zero). %d
// is the cap in milliseconds.
const deferredTimerShim = `(function(){
  if (window.__bsTimerShim) return;
  var orig = window.setTimeout, origClear = window.clearTimeout;
  if (typeof orig !== 'function') return;
  window.__bsTimerShim = true;
  window.__bsPendingTimers = 0;
  var CAP = %d;
  var tracked = Object.create(null);
  var depth = 0; // > 0 while executing inside a timer callback (re-arms don't count)
  window.setTimeout = function(fn, delay){
    if (typeof fn !== 'function') {
      return orig.apply(window, arguments); // string-code etc: run, don't track
    }
    var d = +delay || 0;
    var count = depth === 0 && d <= CAP;
    var extra = Array.prototype.slice.call(arguments, 2);
    var id = orig.call(window, function(){
      if (tracked[id]) { delete tracked[id]; if (window.__bsPendingTimers > 0) window.__bsPendingTimers--; }
      depth++;
      try { return fn.apply(this, extra); }
      finally { depth--; }
    }, d);
    if (count) { tracked[id] = true; window.__bsPendingTimers++; }
    return id;
  };
  window.clearTimeout = function(id){
    if (tracked[id]) { delete tracked[id]; if (window.__bsPendingTimers > 0) window.__bsPendingTimers--; }
    return origClear.apply(window, arguments);
  };
})();`

// timerShimSource renders deferredTimerShim with the AJAX-timeout cap baked in.
func timerShimSource(ajaxTimeoutSec int) string {
	return fmt.Sprintf(deferredTimerShim, ajaxTimeoutSec*1000)
}

// shadowClosedShim records *closed* shadow roots as they are attached (R9b /
// rendering.flatten_shadow_dom). chromedp.OuterHTML never serializes shadow DOM,
// so links/headings/structured-data inside a shadow tree were invisible to the
// parser — yet Screaming Frog pierces both open AND closed roots. Open roots are
// reachable at snapshot time via element.shadowRoot; CLOSED roots are not, by
// design, so we stash a reference (keyed by host) when attachShadow is called.
// Only the closed case is intercepted, and the requested mode is left untouched,
// so page encapsulation semantics are unchanged — the stash merely gives the
// snapshot pass read access it would otherwise lack. Declarative shadow DOM
// created with mode:"closed" by the parser (no attachShadow call) is the one
// unreachable residual; open declarative roots are caught by the snapshot scan.
const shadowClosedShim = `(function(){
  if (window.__bsShadowShim) return;
  var orig = Element.prototype.attachShadow;
  if (typeof orig !== 'function') return;
  window.__bsShadowShim = true;
  window.__bsClosedRoots = new WeakMap();
  Element.prototype.attachShadow = function(init){
    var root = orig.apply(this, arguments);
    try { if (init && init.mode === 'closed') window.__bsClosedRoots.set(this, root); } catch(e){}
    return root;
  };
})();`

// flattenShadowDOM is evaluated synchronously right before the HTML snapshot
// when rendering.flatten_shadow_dom is on. In ONE traversal it pierces every
// shadow root (open via element.shadowRoot, closed via the shim's WeakMap) using
// an explicit stack — O(N) over the composed tree, not a re-scan per host — and
// collects each host plus any *filled* <slot>. It then (a) drops the fallback
// children of filled slots (a filled slot renders its assigned light content,
// not its fallback, so serializing the fallback would over-count links/words),
// and (b) MOVES each shadow tree's children up into its host as ordinary
// light-DOM children. Moving (not cloning) preserves nested shadow roots, which
// were already collected in the same walk, so order doesn't matter. The whole
// pass is synchronous, so the page's own MutationObservers can't run before
// outerHTML is read, and the tab is discarded immediately after — so mutating
// the live DOM here is safe. parse then sees the shadow links as normal rendered
// content (Origin: rendered), as Screaming Frog reports them; static-mode
// raw-HTML parsing is untouched. The whole body is wrapped so any exception
// (e.g. a hostile shadowRoot getter) still returns the un-flattened snapshot
// rather than failing the render. Residuals: shadow DOM inside iframes and
// closed *declarative* shadow roots are not reached; slotted content keeps its
// host-order position (link/heading extraction is order-independent).
const flattenShadowDOM = `(function(){
  try {
    var closed = window.__bsClosedRoots;
    function rootOf(el){
      try { if (el.shadowRoot) return el.shadowRoot; } catch(e){}
      if (closed && closed.has(el)) return closed.get(el);
      return null;
    }
    var hosts = [], filledSlots = [], stack = [document.documentElement], guard = 0;
    while (stack.length && guard++ < 2000000) {
      var el = stack.pop();
      if (!el || el.nodeType !== 1) continue;
      if (el.tagName === 'SLOT') {
        try { if (el.assignedNodes && el.assignedNodes().length > 0) filledSlots.push(el); } catch(e){}
      }
      var root = rootOf(el);
      if (root) {
        hosts.push({host: el, root: root});
        var rc = root.children;
        for (var i = 0; i < rc.length; i++) stack.push(rc[i]);
      }
      var lc = el.children;
      for (var j = 0; j < lc.length; j++) stack.push(lc[j]);
    }
    for (var s = 0; s < filledSlots.length; s++) {
      var sl = filledSlots[s];
      while (sl.firstChild) sl.removeChild(sl.firstChild);
    }
    for (var m = 0; m < hosts.length; m++) {
      var h = hosts[m];
      while (h.root.firstChild) h.host.appendChild(h.root.firstChild);
    }
  } catch(e) { /* fall through — never lose the snapshot over flattening */ }
  return document.documentElement.outerHTML;
})();`

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
	var pendingTimers int // last known count of in-window deferred timers (shim)
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
			// The AJAX cap is the hard bound and always wins, even if deferred
			// work is still pending (a re-arming setTimeout chain mustn't hang).
			if now.After(settleDeadline) {
				return nil
			}
			// A page isn't settled while scripts are still scheduled to mutate
			// the DOM on an in-window timer (R9a). The shim maintains this count;
			// a transient eval error keeps the previously read value instead of
			// resetting (only matters mid-settle — by the first post-DCL tick the
			// shim has long been installed, so the initial 0 is real, not a gap).
			if n, err := evalInt(ctx, "window.__bsPendingTimers|0"); err == nil {
				pendingTimers = n
			}
			if pendingTimers > 0 {
				continue
			}
			quiet := now.Sub(last)
			switch {
			case inflight == 0 && quiet >= networkIdleWindow:
				return nil
			case quiet >= stuckIdleWindow:
				return nil // only permanently-open requests remain
			}
			// DOM-stability probe every 500ms: two consecutive identical
			// node counts with no DOM-affecting request pending = settled.
			if now.Sub(lastPoll) >= 500*time.Millisecond {
				lastPoll = now
				if n, err := evalInt(ctx, "document.getElementsByTagName('*').length"); err == nil && n > 0 {
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

// evalInt evaluates a JS expression that yields a number in the page's top
// frame and returns it as an int.
func evalInt(ctx context.Context, expr string) (int, error) {
	var n int
	err := chromedp.Evaluate(expr, &n).Do(ctx)
	return n, err
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
	}
	// Adaptive settling watches for DOM work scheduled on in-window timers; the
	// shim that exposes that count must be installed before navigation so it
	// runs at document-start, ahead of any page script (R9a). Fixed mode dwells
	// the full AJAX timeout regardless, so it needs no instrumentation.
	if r.cfg.Rendering.WaitStrategy != "fixed" {
		shim := timerShimSource(r.cfg.Rendering.AjaxTimeoutSec)
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(shim).Do(ctx)
			return err
		}))
	}
	// Shadow-DOM flattening (R9b) needs closed roots stashed as they attach,
	// regardless of wait strategy — also installed before navigation.
	if r.cfg.Rendering.FlattenShadowDOM {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(shadowClosedShim).Do(ctx)
			return err
		}))
	}
	actions = append(actions,
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
	)
	// Screenshot is taken from the settled DOM BEFORE shadow flattening, which
	// mutates the live tree (moves shadow content into light DOM) purely to
	// serialize it — the pixels must reflect the real page, not the flattened one.
	if r.cfg.Rendering.Screenshots {
		actions = append(actions, chromedp.FullScreenshot(&res.Screenshot, 80))
	}
	if r.cfg.Rendering.FlattenShadowDOM {
		actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
			// Best-effort: if the flatten eval errors or yields nothing, fall back
			// to the plain serialization so shadow-DOM support can never make a
			// page worse than rendering without it.
			if err := chromedp.Evaluate(flattenShadowDOM, &res.HTML).Do(ctx); err != nil || res.HTML == "" {
				return chromedp.OuterHTML("html", &res.HTML).Do(ctx)
			}
			return nil
		}))
	} else {
		actions = append(actions, chromedp.OuterHTML("html", &res.HTML))
	}
	if err := chromedp.Run(tabCtx, actions...); err != nil {
		return nil, err
	}
	return res, nil
}
