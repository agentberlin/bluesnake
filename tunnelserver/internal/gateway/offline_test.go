package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/agentberlin/bluesnake/tunnelserver/internal/registry"
	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

// tunnelID is a well-formed 12-char id for offline tests (labels that don't
// match the id shape skip the store entirely).
const tunnelID = "abc123def456"

// postMCP runs one POST against the public handler for the given subdomain.
func postMCP(g *Gateway, id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://"+id+".t.snake.blue/mcp", strings.NewReader(body))
	req.Host = id + ".t.snake.blue"
	rec := httptest.NewRecorder()
	g.PublicHandler().ServeHTTP(rec, req)
	return rec
}

func TestOfflineStubForRegisteredTunnel(t *testing.T) {
	g, st := newGateway(t)
	snap := `{"serverInfo":{"name":"bluesnake","version":"7.7.7"},"instructions":"from snapshot","toolsResult":{"tools":[{"name":"start_crawl"}]}}`
	_ = st.Create(context.Background(), &store.Tunnel{
		ID: tunnelID, ConnectSecretHash: store.Hash("s"), MCPSnapshot: []byte(snap),
	})

	// initialize succeeds and carries the snapshot's serverInfo plus the
	// offline note.
	rec := postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "7.7.7") || !strings.Contains(rec.Body.String(), "OFFLINE") {
		t.Errorf("initialize body = %s", rec.Body.String())
	}

	// tools/list serves the snapshot.
	rec = postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if !strings.Contains(rec.Body.String(), "start_crawl") {
		t.Errorf("tools/list body = %s", rec.Body.String())
	}

	// tools/call comes back 200 with an isError result.
	rec = postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"start_crawl"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("tools/call status = %d", rec.Code)
	}
	var resp struct {
		Result struct {
			IsError bool `json:"isError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.Result.IsError {
		t.Errorf("tools/call body = %s, want isError result", rec.Body.String())
	}
}

func TestOfflineUnknownAndRevokedStay502(t *testing.T) {
	g, st := newGateway(t)
	_ = st.Create(context.Background(), &store.Tunnel{
		ID: "dead123dead1", ConnectSecretHash: store.Hash("s"), Revoked: true,
	})

	cases := []struct {
		name string
		id   string
	}{
		{"unknown well-formed id", tunnelID},
		{"revoked tunnel", "dead123dead1"},
		{"label not an id shape", "notanid"},
	}
	for _, c := range cases {
		rec := postMCP(g, c.id, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
		if rec.Code != http.StatusBadGateway {
			t.Errorf("%s: status = %d, want 502", c.name, rec.Code)
		}
	}
}

func TestOfflineNonMCPPathStays502(t *testing.T) {
	g, st := newGateway(t)
	_ = st.Create(context.Background(), &store.Tunnel{ID: tunnelID, ConnectSecretHash: store.Hash("s")})
	req := httptest.NewRequest(http.MethodPost, "http://"+tunnelID+".t.snake.blue/other", nil)
	req.Host = tunnelID + ".t.snake.blue"
	rec := httptest.NewRecorder()
	g.PublicHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("non-/mcp offline status = %d, want 502", rec.Code)
	}
}

// countingStore wraps a Store and counts GetByID calls.
type countingStore struct {
	store.Store
	gets atomic.Int64
}

func (c *countingStore) GetByID(ctx context.Context, id string) (*store.Tunnel, error) {
	c.gets.Add(1)
	return c.Store.GetByID(ctx, id)
}

func TestOfflineVerdictIsCached(t *testing.T) {
	mem := store.NewMem()
	_ = mem.Create(context.Background(), &store.Tunnel{ID: tunnelID, ConnectSecretHash: store.Hash("s")})
	cs := &countingStore{Store: mem}
	g := New(registry.New(), cs, "t.snake.blue", nil)

	for i := 0; i < 5; i++ {
		if rec := postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":1,"method":"ping"}`); rec.Code != http.StatusOK {
			t.Fatalf("ping %d status = %d", i, rec.Code)
		}
	}
	if got := cs.gets.Load(); got != 1 {
		t.Errorf("store lookups = %d, want 1 (verdict cached)", got)
	}

	// Negative verdicts are cached too.
	for i := 0; i < 5; i++ {
		postMCP(g, "unknown12345", `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	}
	if got := cs.gets.Load(); got != 2 {
		t.Errorf("store lookups = %d, want 2 (negative verdict cached)", got)
	}
}

func TestOfflineStoreOutage502(t *testing.T) {
	g := New(registry.New(), failingStore{}, "t.snake.blue", nil)
	rec := postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":1,"method":"ping"}`)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("store outage status = %d, want 502", rec.Code)
	}
}

func TestOfflineGateTTLAndEviction(t *testing.T) {
	now := time.Now()
	o := newOfflineGate(func() time.Time { return now })
	o.put("a", gateEntry{allowed: true})
	if e, ok := o.get("a"); !ok || !e.allowed {
		t.Fatal("fresh entry should be returned")
	}
	now = now.Add(offlineCacheTTL + time.Second)
	if _, ok := o.get("a"); ok {
		t.Error("expired entry should miss")
	}
	// Eviction prefers expired entries and never grows past the cap.
	for i := 0; i < offlineCacheMax+10; i++ {
		o.put(string(rune('a'+i%26))+strings.Repeat("x", 8)+itoa(i), gateEntry{})
	}
	if len(o.entries) > offlineCacheMax {
		t.Errorf("cache size = %d, want <= %d", len(o.entries), offlineCacheMax)
	}
}

// itoa avoids importing strconv for one test helper.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestConnectPrimesSnapshotAndOfflineFollows drives the full lifecycle at the
// gateway level: a fake app connects (HandleConn), the probe snapshots its
// tools, the app drops, and the offline stub then serves those tools.
func TestConnectPrimesSnapshotAndOfflineFollows(t *testing.T) {
	g, st := newGateway(t)
	_ = st.Create(context.Background(), &store.Tunnel{ID: tunnelID, ConnectSecretHash: store.Hash("s")})

	// Fake local MCP server behind the tunnel, answering the probe.
	backend := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var result string
		switch req.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"bluesnake","version":"5.5.5"},"instructions":"live"}`
		case "tools/list":
			result = `{"tools":[{"name":"probe_saw_me","inputSchema":{}}]}`
		default:
			result = `{}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":` + string(req.ID) + `,"result":` + result + `}`))
	})
	gwSess := fakeApp(t, backend)

	sess := registry.NewSession(tunnelID, tunnelID+".t.snake.blue", gwSess, time.Now())
	sess.Handler = g.proxyFor(sess)
	g.reg.Add(sess)
	g.snapshotSession(sess) // synchronously, as HandleConn does in a goroutine

	// Snapshot persisted?
	tn, err := st.GetByID(context.Background(), tunnelID)
	if err != nil || len(tn.MCPSnapshot) == 0 {
		t.Fatalf("snapshot not persisted: %v, %q", err, tn.MCPSnapshot)
	}

	// App drops; offline stub must now serve the snapshot's tools.
	g.reg.Remove(sess)
	rec := postMCP(g, tunnelID, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "probe_saw_me") {
		t.Errorf("offline tools/list = %d %s", rec.Code, rec.Body.String())
	}
}
