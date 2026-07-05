package crawler

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/internal/config"
)

// TestCloseReleasesConnectionGoroutines pins that a finished crawl releases
// every goroutine its HTTP client spawned. A completed crawl's last
// keep-alive connection sits idle in the transport pool, and the transport
// has no IdleConnTimeout — so without an explicit close, the connection's
// readLoop/writeLoop goroutines (plus the server-side handler) linger until
// the PEER closes it: for a keep-alive-forever peer, indefinitely. That is a
// per-crawl goroutine/socket leak in long-lived multi-crawl processes (the
// desktop app, the MCP server) and was the intermittent winddown failure in
// runner's TestShutdownPausesAllInFlightCrawls — a pause landing BETWEEN
// requests left an idle connection alive past the 90s budget. Close must
// drop the idle pool.
func TestCloseReleasesConnectionGoroutines(t *testing.T) {
	s := newSite(t, map[string]string{"/": link("/a"), "/a": "<p>leaf</p>"})
	baseline := runtime.NumGoroutine() // server already up: its listener is in the baseline

	cfg := config.Default()
	cfg.SiteChecks.Enabled = "never" // one client, page fetches only — the lean repro
	c, err := New(cfg, WithSink(newCapSink()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(context.Background(), s.server.URL+"/"); err != nil {
		t.Fatal(err)
	}
	c.Close()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+1 { // +1: unrelated runtime churn, never enough to hide a conn pair
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("connection goroutines still alive 10s after Close: baseline %d, now %d — the fetch client's idle pool was not closed",
		baseline, runtime.NumGoroutine())
}
