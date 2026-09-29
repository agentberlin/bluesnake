package render

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

// chromePID is the process id of the renderer's running Chrome.
func chromePID(t *testing.T, r *Renderer) int {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browserCtx == nil {
		t.Fatal("no Chrome running")
	}
	return chromedp.FromContext(r.browserCtx).Browser.Process().Pid
}

// killChrome kills the renderer's Chrome the way a crash or the OOM killer
// would, and waits until the renderer has noticed.
func killChrome(t *testing.T, r *Renderer) {
	t.Helper()
	r.mu.Lock()
	browser := r.browserCtx
	r.mu.Unlock()
	if err := chromedp.FromContext(browser).Browser.Process().Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-browser.Done():
	case <-time.After(10 * time.Second):
		t.Fatal("renderer did not notice Chrome dying")
	}
}

func htmlServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// Launching Chrome per page cost a cold start and an empty cache on every
// render; one Chrome now serves the whole crawl, a tab per page.
func TestRenderReusesOneChrome(t *testing.T) {
	cfg := requireChrome(t)
	srv := htmlServer(t, jsPage)
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if _, err := r.Render(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	pid := chromePID(t, r)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Go(func() {
			res, err := r.Render(context.Background(), srv.URL)
			if err == nil && !strings.Contains(res.HTML, "content injected by javascript") {
				err = fmt.Errorf("rendered DOM missing JS content")
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := chromePID(t, r); got != pid {
		t.Errorf("Chrome was relaunched (pid %d -> %d); want one Chrome for every render", pid, got)
	}
}

// A dead Chrome must not take the rest of the crawl's renders with it: the
// next render starts a new one.
func TestRenderStartsNewChromeAfterItDies(t *testing.T) {
	cfg := requireChrome(t)
	srv := htmlServer(t, jsPage)
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	if _, err := r.Render(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	pid := chromePID(t, r)
	killChrome(t, r)

	res, err := r.Render(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("render after Chrome died: %v", err)
	}
	if !strings.Contains(res.HTML, "content injected by javascript") {
		t.Error("rendered DOM missing JS content")
	}
	if chromePID(t, r) == pid {
		t.Error("render reported success on the dead Chrome")
	}
}

// A page whose render was cut short by Chrome dying is rendered again on the
// new Chrome rather than failing (and degrading to raw HTML).
func TestRenderRetriesPageWhenChromeDiesMidRender(t *testing.T) {
	cfg := requireChrome(t)
	arrived, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/" {
			http.NotFound(w, req)
			return
		}
		if calls.Add(1) == 1 { // hold the first request until Chrome is dead
			close(arrived)
			<-release
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, jsPage)
	}))
	defer srv.Close()
	defer close(release)

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	type outcome struct {
		res *Result
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := r.Render(context.Background(), srv.URL+"/")
		done <- outcome{res, err}
	}()
	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("page request never arrived")
	}
	killChrome(t, r)

	select {
	case o := <-done:
		if o.err != nil {
			t.Fatalf("render failed instead of retrying on a new Chrome: %v", o.err)
		}
		if !strings.Contains(o.res.HTML, "content injected by javascript") {
			t.Error("retried render missing JS content")
		}
	case <-time.After(60 * time.Second):
		t.Fatal("render did not finish")
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("server saw %d page requests, want 2 (the killed render and its retry)", got)
	}
}

// A closed renderer must not quietly start another Chrome.
func TestRenderAfterCloseFails(t *testing.T) {
	cfg := requireChrome(t)
	srv := htmlServer(t, jsPage)
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Render(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	r.Close()
	if _, err := r.Render(context.Background(), srv.URL); err == nil {
		t.Error("render after Close succeeded")
	}
}

// Pages must not see each other's state: with the default
// advanced.cookie_storage=session the raw fetcher keeps no cookie jar, so a
// render must not carry cookies (HTTP or document.cookie) or web storage from
// one page into the next either.
func TestRenderIsolatesPagesFromEachOther(t *testing.T) {
	cfg := requireChrome(t)
	var (
		mu            sync.Mutex
		cookieHeaders []string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		mu.Lock()
		cookieHeaders = append(cookieHeaders, r.Header.Get("Cookie"))
		mu.Unlock()
		w.Header().Set("Set-Cookie", "srv=1; Path=/")
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><script>
var prev = 'cookie=' + document.cookie + ';local=' + localStorage.getItem('k') + ';session=' + sessionStorage.getItem('k');
document.cookie = 'js=1; path=/';
localStorage.setItem('k', '1');
sessionStorage.setItem('k', '1');
document.addEventListener('DOMContentLoaded', function() {
  var p = document.createElement('p'); p.id = 'prev'; p.textContent = prev; document.body.appendChild(p);
});
</script></head><body></body></html>`)
	}))
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range 2 {
		res, err := r.Render(context.Background(), srv.URL+"/")
		if err != nil {
			t.Fatalf("render %d: %v", i+1, err)
		}
		// document.cookie reads back the cookies the response set, so the
		// fresh-page value is the response's own cookie and no JS state
		if want := "cookie=srv=1;local=null;session=null"; !strings.Contains(res.HTML, want) {
			t.Errorf("render %d saw state from an earlier page: want %q in\n%s", i+1, want, res.HTML)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(cookieHeaders) != 2 {
		t.Fatalf("server saw %d page requests, want 2", len(cookieHeaders))
	}
	for i, h := range cookieHeaders {
		if h != "" {
			t.Errorf("request %d carried Cookie %q from an earlier page", i+1, h)
		}
	}
}

// The viewport is what responsive pages lay out against, so every render must
// get the configured window size, not a browser default.
func TestRenderUsesConfiguredWindowSize(t *testing.T) {
	cfg := requireChrome(t)
	cfg.Rendering.WindowWidth, cfg.Rendering.WindowHeight = 1280, 900
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><script>
document.addEventListener('DOMContentLoaded', function() {
  var p = document.createElement('p'); p.id = 'vp';
  p.textContent = 'viewport=' + window.innerWidth + 'x' + window.innerHeight;
  document.body.appendChild(p);
});
</script></body></html>`)
	}))
	defer srv.Close()

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range 2 { // the second render runs in a tab opened after the first
		res, err := r.Render(context.Background(), srv.URL)
		if err != nil {
			t.Fatalf("render %d: %v", i+1, err)
		}
		if !strings.Contains(res.HTML, "viewport=1280x") {
			t.Errorf("render %d: want a 1280px-wide viewport in\n%s", i+1, res.HTML)
		}
	}
}

// Renders must leave through the configured proxy (D6), not direct. The proxy
// here also plays the origin: Chrome sends it the absolute-URI request for a
// .test host it never resolves itself.
func TestRenderRoutesThroughConfiguredProxy(t *testing.T) {
	cfg := requireChrome(t)
	var proxied atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "bluesnake-render.test" || r.URL.Path != "/" {
			http.NotFound(w, r) // including Chrome's /favicon.ico
			return
		}
		proxied.Add(1)
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><p>served via the proxy</p></body></html>`)
	}))
	defer proxy.Close()
	cfg.HTTP.Proxy = proxy.URL

	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for i := range 2 {
		res, err := r.Render(context.Background(), "http://bluesnake-render.test/")
		if err != nil {
			t.Fatalf("render %d: %v", i+1, err)
		}
		if !strings.Contains(res.HTML, "served via the proxy") {
			t.Errorf("render %d did not come through the proxy:\n%s", i+1, res.HTML)
		}
	}
	if got := proxied.Load(); got != 2 {
		t.Errorf("proxy saw %d page requests, want 2", got)
	}
}
