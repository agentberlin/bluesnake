package tunnel

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestProxyHandlerRewritesHostAndStripsOrigin verifies the two hygiene
// rewrites the local MCP server depends on: a localhost Host (so its
// DNS-rebinding guard accepts the request) and no Origin header.
func TestProxyHandlerRewritesHostAndStripsOrigin(t *testing.T) {
	var gotHost, gotOrigin string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		gotOrigin = r.Header.Get("Origin")
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	local := strings.TrimPrefix(backend.URL, "http://")
	c := New(Config{Identity: sampleIdentity(), LocalAddr: local})

	req := httptest.NewRequest(http.MethodPost, "https://k3x9qzpw04ab.t.snake.blue/mcp", strings.NewReader("{}"))
	req.Host = "k3x9qzpw04ab.t.snake.blue"
	req.Header.Set("Origin", "https://evil.example.com")
	req.RequestURI = "" // required for ReverseProxy via ServeHTTP
	req.URL, _ = url.Parse("/mcp")

	rec := httptest.NewRecorder()
	c.proxyHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rec.Code, rec.Body.String())
	}
	if gotHost != local {
		t.Errorf("backend saw Host %q, want %q", gotHost, local)
	}
	if gotOrigin != "" {
		t.Errorf("backend saw Origin %q, want it stripped", gotOrigin)
	}
}

func TestProxyHandlerLocalDown(t *testing.T) {
	// Nothing listening on this address → 502 with the friendly message.
	c := New(Config{Identity: sampleIdentity(), LocalAddr: "127.0.0.1:1"})
	req := httptest.NewRequest(http.MethodPost, "https://x/mcp", strings.NewReader("{}"))
	req.RequestURI = ""
	req.URL, _ = url.Parse("/mcp")
	rec := httptest.NewRecorder()
	c.proxyHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rec.Code)
	}
}

func TestNewInitialStatus(t *testing.T) {
	c := New(Config{Identity: sampleIdentity(), LocalAddr: "127.0.0.1:8473"})
	st := c.Status()
	if st.State != StateConnecting {
		t.Errorf("initial state = %q, want connecting", st.State)
	}
	if st.MCPURL == "" || st.PublicURL == "" {
		t.Errorf("status URLs not populated: %+v", st)
	}
}
