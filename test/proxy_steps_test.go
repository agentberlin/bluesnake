package acceptance

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"

	"github.com/agentberlin/bluesnake/internal/config"
	"github.com/cucumber/godog"
)

// acceptProxy is a recording forward proxy standing in for a provider gateway.
// Targets in these scenarios are the world's plain-HTTP test server, which a
// client reaches through a proxy with absolute-form requests.
type acceptProxy struct {
	srv    *httptest.Server
	name   string
	mu     sync.Mutex
	hits   int
	status int // when non-zero, answered instead of forwarding
}

func (w *world) registerProxySteps(sc *godog.ScenarioContext) {
	sc.Step(`^(\d+) test proxies$`, w.startProxies)
	sc.Step(`^test proxy (\d+) fails every request with (\d+)$`, w.proxyFails)
	sc.Step(`^the fetch went through test proxy (\d+)$`, w.fetchWentThrough)
	sc.Step(`^the fetch records a proxy$`, w.fetchRecordsProxy)
	sc.Step(`^the recorded proxy contains no credentials$`, w.proxyNoCredentials)
	sc.Step(`^test proxy (\d+) served (\d+) requests?$`, w.proxyServed)
	sc.Step(`^the crawl reports proxy traffic$`, w.proxyTrafficRecorded)
}

func (w *world) startProxies(n int) error {
	for range n {
		p := &acceptProxy{name: fmt.Sprintf("P%d", len(w.proxies)+1)}

		p.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			p.mu.Lock()
			p.hits++
			status := p.status
			p.mu.Unlock()
			if status != 0 {
				rw.WriteHeader(status)
				return
			}
			out, err := http.NewRequest(r.Method, r.RequestURI, r.Body)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadGateway)
				return
			}
			resp, err := http.DefaultTransport.RoundTrip(out)
			if err != nil {
				http.Error(rw, err.Error(), http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			rw.Header().Set("X-Acceptance-Proxy", p.name)
			rw.WriteHeader(resp.StatusCode)
			io.Copy(rw, resp.Body) //nolint:errcheck
		}))
		w.proxies = append(w.proxies, p)
		// Credentials on every entry: redaction is not an edge case to bolt on
		// later, it is the behaviour every scenario should be exercising.
		creds := strings.Replace(p.srv.URL, "http://", "http://user:sekrit@", 1)
		w.proxyEntries = append(w.proxyEntries, config.ProxyEntry{URL: creds})
	}
	return nil
}

func (w *world) proxyFails(idx, status int) error {
	p, err := w.proxyAt(idx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.status = status
	p.mu.Unlock()
	return nil
}

func (w *world) proxyAt(idx int) (*acceptProxy, error) {
	if idx < 1 || idx > len(w.proxies) {
		return nil, fmt.Errorf("test proxy %d does not exist (%d configured)", idx, len(w.proxies))
	}
	return w.proxies[idx-1], nil
}

func (w *world) fetchWentThrough(idx int) error {
	p, err := w.proxyAt(idx)
	if err != nil {
		return err
	}
	if w.fetchRes == nil {
		return fmt.Errorf("no fetch performed")
	}
	if got := w.fetchRes.Headers.Get("X-Acceptance-Proxy"); got != p.name {
		return fmt.Errorf("response came back via %q, want %q", got, p.name)
	}
	// The recorded egress must name the proxy that really carried it —
	// attribution that can drift from reality is worse than none.
	if !strings.Contains(p.srv.URL, strings.TrimPrefix(w.fetchRes.Proxy, "http://")) {
		return fmt.Errorf("recorded proxy %q does not match the serving proxy %q",
			w.fetchRes.Proxy, p.srv.URL)
	}
	return nil
}

func (w *world) fetchRecordsProxy() error {
	if w.fetchRes == nil {
		return fmt.Errorf("no fetch performed")
	}
	if w.fetchRes.Proxy == "" {
		return fmt.Errorf("no proxy recorded on the result")
	}
	return nil
}

func (w *world) proxyNoCredentials() error {
	if w.fetchRes == nil {
		return fmt.Errorf("no fetch performed")
	}
	for _, secret := range []string{"sekrit", "user:", "@"} {
		if strings.Contains(w.fetchRes.Proxy, secret) {
			return fmt.Errorf("recorded proxy %q leaks %q", w.fetchRes.Proxy, secret)
		}
	}
	return nil
}

func (w *world) proxyServed(idx, want int) error {
	p, err := w.proxyAt(idx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	got := p.hits
	p.mu.Unlock()
	if got != want {
		return fmt.Errorf("test proxy %d served %d requests, want %d", idx, got, want)
	}
	return nil
}

func (w *world) proxyTrafficRecorded() error {
	c, err := w.client()
	if err != nil {
		return err
	}
	if c.Traffic().Total() == 0 {
		return fmt.Errorf("no wire bytes metered")
	}
	per := c.TrafficByEgress()
	if len(per) == 0 {
		return fmt.Errorf("no per-egress traffic recorded")
	}
	for _, t := range per {
		if strings.Contains(t.Proxy, "sekrit") {
			return fmt.Errorf("traffic report leaks credentials: %q", t.Proxy)
		}
	}
	return nil
}

func (w *world) closeProxies() {
	for _, p := range w.proxies {
		p.srv.Close()
	}
	w.proxies = nil
	w.proxyEntries = nil
}
