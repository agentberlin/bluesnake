package tunnel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func sampleIdentity() *Identity {
	return &Identity{
		TunnelID:      "k3x9qzpw04ab",
		ConnectSecret: "connect-secret",
		PublicHost:    "k3x9qzpw04ab.t.snake.blue",
		ConnectAddr:   "connect.t.snake.blue:443",
		APIBase:       "https://api.snake.blue",
	}
}

func TestIdentityURLs(t *testing.T) {
	id := sampleIdentity()
	if got, want := id.PublicURL(), "https://k3x9qzpw04ab.t.snake.blue"; got != want {
		t.Errorf("PublicURL = %q, want %q", got, want)
	}
	if got, want := id.MCPURL(), "https://k3x9qzpw04ab.t.snake.blue/mcp"; got != want {
		t.Errorf("MCPURL = %q, want %q", got, want)
	}
}

func TestLoadIdentityMissing(t *testing.T) {
	id, err := LoadIdentity(t.TempDir())
	if err != nil || id != nil {
		t.Errorf("got (%v, %v), want (nil, nil) for missing file", id, err)
	}
}

func TestSaveLoadRoundTripAndPerms(t *testing.T) {
	dir := t.TempDir()
	want := sampleIdentity()
	if err := saveIdentity(dir, want); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(IdentityPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("identity file perm = %o, want 600", perm)
	}
	got, err := LoadIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *want {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestLoadIdentityCorrupt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(IdentityPath(dir), []byte(`{"tunnel_id":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadIdentity(dir); err == nil {
		t.Error("want error for incomplete identity")
	}
}

func TestRegister(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/register" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"tunnel_id":      "abc123def456",
			"connect_secret": "cs",
			"public_host":    "abc123def456.t.snake.blue",
			"connect_addr":   "connect.t.snake.blue:443",
		})
	}))
	defer srv.Close()

	id, err := Register(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if id.TunnelID != "abc123def456" || id.ConnectSecret != "cs" || id.APIBase != srv.URL {
		t.Errorf("unexpected identity %+v", id)
	}
}

func TestRegisterServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limited"})
	}))
	defer srv.Close()

	_, err := Register(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("want error")
	}
	if got := err.Error(); got == "" || !contains(got, "rate limited") {
		t.Errorf("error %q should surface server message", got)
	}
}

func TestEnsureIdentityReusesExisting(t *testing.T) {
	dir := t.TempDir()
	want := sampleIdentity()
	if err := saveIdentity(dir, want); err != nil {
		t.Fatal(err)
	}
	// No server; if it tried to register this would fail.
	got, err := EnsureIdentity(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.TunnelID != want.TunnelID {
		t.Errorf("got %s, want %s", got.TunnelID, want.TunnelID)
	}
}

func TestAPIBaseEnvOverride(t *testing.T) {
	t.Setenv(envAPIBase, "http://localhost:9999")
	if got := APIBase(); got != "http://localhost:9999" {
		t.Errorf("APIBase = %q, want override", got)
	}
}

func TestIdentityPath(t *testing.T) {
	if got, want := IdentityPath("/x"), filepath.Join("/x", "tunnel.json"); got != want {
		t.Errorf("IdentityPath = %q, want %q", got, want)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
