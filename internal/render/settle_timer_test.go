package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// R9a: adaptive settle must not snapshot while the page still has in-window
// setTimeout callbacks pending that mutate the DOM. A bare network-idle window
// (the old sole trigger) fires ~500ms after DOMContentLoaded — long before a
// setTimeout(…, 1500) injects its link — so adaptive used to miss links that
// Screaming Frog (which dwells its full AJAX timeout) catches.
//
// The injected hrefs are assembled by string concatenation ('/r'+'-late') so
// the literal path "/r-late" appears in res.HTML ONLY once the node is actually
// serialized into the rendered DOM — never from the inline <script> source.
//
// timerPage injects:
//   - a static control link (always present)
//   - a link at 1500ms via setTimeout (delay <= cap: must be caught)
const timerPage = `<html><head><title>timer</title></head><body>
<a href="/r-control">control</a>
<script>
  setTimeout(function(){
    var a=document.createElement('a'); a.href='/r'+'-late'; a.textContent='x';
    document.body.appendChild(a);
  }, 1500);
</script></body></html>`

func htmlOnly(body string) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	})
	return httptest.NewServer(mux)
}

func TestRenderAdaptiveCatchesDeferredTimerDOM(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "adaptive"
	cfg.Rendering.AjaxTimeoutSec = 10
	srv := htmlOnly(timerPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)

	if !strings.Contains(res.HTML, "/r-control") {
		t.Error("snapshot missing the static control link")
	}
	// The core R9a assertion: this link only exists in the DOM if we waited
	// past the 1500ms timer, so its presence alone proves the deferred wait.
	if !strings.Contains(res.HTML, "/r-late") {
		t.Error("snapshot missing the 1500ms setTimeout-injected link — adaptive settled before the timer fired (R9a)")
	}
	// Loose ceiling (Chrome tab startup alone is ~1-1.5s, so margins must be
	// generous): once the 1500ms work is done the page is quiet, so adaptive
	// must settle promptly — nowhere near the 10s cap.
	if elapsed >= 6*time.Second {
		t.Errorf("render took %s — should settle shortly after the 1500ms timer, not dwell toward the 10s cap", elapsed)
	}
}

// Guard the design boundaries that contain the latency downside: setInterval
// (analytics/polling) and far-future setTimeout (delay > cap, e.g. a cookie
// banner) are NOT countable deferred work, so a page carrying both must still
// settle quickly instead of dwelling to the cap.
const uncountedTimerPage = `<html><head><title>uncounted</title></head><body><h1>x</h1>
<script>
  window.addEventListener('DOMContentLoaded', function(){
    var p=document.createElement('p'); p.textContent='settled'+'-content'; document.body.appendChild(p);
    setInterval(function(){ fetch('/ping'); }, 300);               // chatter: never counted
    setTimeout(function(){                                          // far-future: delay > cap, never counted
      var a=document.createElement('a'); a.href='/r'+'-never'; a.textContent='x';
      document.body.appendChild(a);
    }, 12000);
  });
</script></body></html>`

func TestRenderAdaptiveSettlesEarlyDespiteUncountedTimers(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "adaptive"
	cfg.Rendering.AjaxTimeoutSec = 10
	srv := htmlOnly(uncountedTimerPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// With a 10s cap, counting setInterval or the 12000ms far-future timeout as
	// pending work would dwell to ~10s; correct behaviour settles in ~1-2s.
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("render took %s — setInterval / far-future setTimeout must not be counted as pending work (would dwell to the cap)", elapsed)
	}
	if !strings.Contains(res.HTML, "settled-content") {
		t.Error("snapshot missing the DCL-injected content")
	}
	if strings.Contains(res.HTML, "/r-never") {
		t.Error("snapshot wrongly contains the far-future (12000ms > 10s cap) link")
	}
}

// A self-rescheduling setTimeout loop over a static DOM (carousel/countdown/
// poll tick) must NOT pin the page open until the AJAX cap. Only the first,
// top-level schedule is counted; the re-arm fires from inside the callback
// (depth > 0) and is ignored, so the page settles on its normal signals.
const rearmingTimerPage = `<html><head><title>rearm</title></head><body><h1>x</h1>
<script>
  window.addEventListener('DOMContentLoaded', function(){
    var p=document.createElement('p'); p.textContent='settled'+'-content'; document.body.appendChild(p);
    var n=0;
    function tick(){ n++; setTimeout(tick, 100); } // re-arms forever, never grows DOM
    setTimeout(tick, 100);                          // first schedule: top-level, counted once
  });
</script></body></html>`

func TestRenderAdaptiveSettlesEarlyOnRearmingTimerLoop(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WaitStrategy = "adaptive"
	cfg.Rendering.AjaxTimeoutSec = 10
	srv := htmlOnly(rearmingTimerPage)
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	start := time.Now()
	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	// Counting the re-arm would dwell to the full 10s cap; only the first
	// schedule is counted, so the page settles in ~1-2s once it fires.
	if elapsed := time.Since(start); elapsed >= 5*time.Second {
		t.Errorf("render took %s — a re-arming setTimeout loop must not dwell to the AJAX cap", elapsed)
	}
	if !strings.Contains(res.HTML, "settled-content") {
		t.Error("snapshot missing the DCL-injected content")
	}
}
