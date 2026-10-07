package proxypool

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func probeTarget(t *testing.T) string {
	t.Helper()
	origin := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(origin.Close)
	return strings.TrimPrefix(origin.URL, "http://")
}

func TestProbeAcceptsGoodCredentials(t *testing.T) {
	up := newFakeUpstream(t, "u", "right")
	p := mustPool(t, []Entry{{URL: up.URL("u", "right")}}, RoundRobin).Select("h")
	if err := Probe(context.Background(), p, probeTarget(t)); err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if up.connects.Load() != 1 {
		t.Fatalf("probe opened %d tunnels, want 1", up.connects.Load())
	}
}

// A wrong password must be named as such, never mistaken for a block.
func TestProbeReportsBadCredentials(t *testing.T) {
	up := newFakeUpstream(t, "u", "right")
	p := mustPool(t, []Entry{{URL: up.URL("u", "wrong")}}, RoundRobin).Select("h")
	err := Probe(context.Background(), p, probeTarget(t))
	if !errors.Is(err, ErrProxyAuth) {
		t.Fatalf("Probe err = %v, want ErrProxyAuth", err)
	}
	if strings.Contains(err.Error(), "wrong") {
		t.Fatalf("error leaks the password: %v", err)
	}
}

func TestProbeReportsDeadProxy(t *testing.T) {
	up := newFakeUpstream(t, "", "")
	addr := up.URL("", "")
	up.srv.Close()
	p := mustPool(t, []Entry{{URL: addr}}, RoundRobin).Select("h")
	if err := Probe(context.Background(), p, probeTarget(t)); err == nil {
		t.Fatal("a dead proxy must fail the probe")
	}
}

func TestProbeDirectAlwaysPasses(t *testing.T) {
	p := mustPool(t, nil, RoundRobin).Select("h")
	if err := Probe(context.Background(), p, "unused:1"); err != nil {
		t.Fatal(err)
	}
}

// The switchable forwarder carries Chrome direct until Escalate, then through
// the upstream — and the tunnel opened before the switch must not survive it.
func TestSwitchableForwarderMovesToUpstream(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok") //nolint:errcheck
	}))
	t.Cleanup(origin.Close)
	up := newFakeUpstream(t, "u", "p")
	p := mustPool(t, []Entry{{URL: up.URL("u", "p")}}, RoundRobin).Select("h")
	f, err := StartSwitchableForwarder(p, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	client := clientVia(t, f)

	get := func() {
		t.Helper()
		resp, err := client.Get(origin.URL)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body) //nolint:errcheck
		resp.Body.Close()
	}
	get()
	if up.connects.Load() != 0 {
		t.Fatalf("before the switch the upstream saw %d tunnels, want 0", up.connects.Load())
	}
	f.Escalate()
	get() // the pre-switch tunnel was cut, so the client must dial a new one
	if up.connects.Load() != 1 {
		t.Fatalf("after the switch the upstream saw %d tunnels, want 1", up.connects.Load())
	}
	if got := up.seenAuth.Load().(string); got != up.wantAuth {
		t.Fatalf("upstream credential = %q, want the configured one", got)
	}
}

// A forwarder started already escalated (a resumed crawl) never goes direct.
func TestSwitchableForwarderStartsEscalated(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(origin.Close)
	up := newFakeUpstream(t, "", "")
	p := mustPool(t, []Entry{{URL: up.URL("", "")}}, RoundRobin).Select("h")
	f, err := StartSwitchableForwarder(p, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	resp, err := clientVia(t, f).Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if up.plain.Load() != 1 {
		t.Fatalf("upstream carried %d plain requests, want 1", up.plain.Load())
	}
}
