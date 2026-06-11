package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agentberlin/bluesnake/tunnelserver/internal/store"
)

func newAPI(t *testing.T) (*API, *store.Mem) {
	t.Helper()
	st := store.NewMem()
	a := New(Config{
		Store:       st,
		BaseDomain:  "t.snake.blue",
		ConnectAddr: "connect.t.snake.blue:443",
		PerIPPerMin: 1000, PerIPBurst: 1000, // effectively unlimited for most tests
		GlobalPerMin: 100000, GlobalBurst: 100000,
	})
	return a, st
}

func do(t *testing.T, h http.Handler, method, target, body, ip string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if ip != "" {
		req.RemoteAddr = ip + ":12345"
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRegisterReturnsCredentialsAndPersistsHashes(t *testing.T) {
	a, st := newAPI(t)
	rec := do(t, a.Handler(), http.MethodPost, "http://api/v1/register", "", "1.2.3.4")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var resp registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.TunnelID == "" || resp.ConnectSecret == "" {
		t.Fatalf("incomplete response: %+v", resp)
	}
	if resp.PublicHost != resp.TunnelID+".t.snake.blue" {
		t.Errorf("public_host = %q", resp.PublicHost)
	}
	if resp.ConnectAddr != "connect.t.snake.blue:443" {
		t.Errorf("connect_addr = %q", resp.ConnectAddr)
	}

	// The store must hold only a hash that verifies against the plaintext.
	tn, err := st.GetByID(context.Background(), resp.TunnelID)
	if err != nil {
		t.Fatal(err)
	}
	if string(tn.ConnectSecretHash) != string(store.Hash(resp.ConnectSecret)) {
		t.Error("stored connect secret hash does not verify")
	}
}

func TestPerIPRateLimit(t *testing.T) {
	st := store.NewMem()
	a := New(Config{
		Store: st, BaseDomain: "t.snake.blue",
		PerIPPerMin: 1, PerIPBurst: 2,
		GlobalPerMin: 100000, GlobalBurst: 100000,
	})
	h := a.Handler()
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, do(t, h, http.MethodPost, "http://api/v1/register", "", "9.9.9.9").Code)
	}
	// First two within burst succeed; the rest are limited.
	if codes[0] != 200 || codes[1] != 200 {
		t.Errorf("burst requests should succeed: %v", codes)
	}
	if codes[2] != http.StatusTooManyRequests || codes[3] != http.StatusTooManyRequests {
		t.Errorf("over-burst requests should be 429: %v", codes)
	}
	// A different IP is unaffected.
	if c := do(t, h, http.MethodPost, "http://api/v1/register", "", "8.8.8.8").Code; c != 200 {
		t.Errorf("other IP got %d, want 200", c)
	}
}

func TestTrustProxyHeaderFromTrustedPeer(t *testing.T) {
	st := store.NewMem()
	a := New(Config{
		Store: st, BaseDomain: "t.snake.blue",
		TrustProxyHeader:  true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"}, // the test peer is "trusted"
		PerIPPerMin:       1, PerIPBurst: 1,
		GlobalPerMin: 100000, GlobalBurst: 100000,
	})
	h := a.Handler()
	// Same trusted TCP peer, different CF-Connecting-IP → independent buckets.
	req1 := httptest.NewRequest(http.MethodPost, "http://api/v1/register", nil)
	req1.RemoteAddr = "10.0.0.1:1"
	req1.Header.Set("CF-Connecting-IP", "1.1.1.1")
	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "http://api/v1/register", nil)
	req2.RemoteAddr = "10.0.0.1:2"
	req2.Header.Set("CF-Connecting-IP", "2.2.2.2")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req2)

	if rec1.Code != 200 || rec2.Code != 200 {
		t.Errorf("distinct forwarded IPs from a trusted peer should each pass: %d %d", rec1.Code, rec2.Code)
	}
}

// TestSpoofedProxyHeaderFromUntrustedPeer is the security-critical case: a
// direct-to-origin attacker (peer NOT in the trusted ranges) must not be able to
// mint fresh buckets by varying CF-Connecting-IP — the header is ignored and
// rate limiting falls back to the real peer IP.
func TestSpoofedProxyHeaderFromUntrustedPeer(t *testing.T) {
	st := store.NewMem()
	a := New(Config{
		Store: st, BaseDomain: "t.snake.blue",
		TrustProxyHeader:  true,
		TrustedProxyCIDRs: []string{"10.0.0.0/8"}, // attacker is NOT in here
		PerIPPerMin:       1, PerIPBurst: 1,
		GlobalPerMin: 100000, GlobalBurst: 100000,
	})
	h := a.Handler()
	codes := []int{}
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "http://api/v1/register", nil)
		req.RemoteAddr = "203.0.113.7:5555"                  // same untrusted peer each time
		req.Header.Set("CF-Connecting-IP", "9.9.9."+itoa(i)) // spoof a new IP each time
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
	}
	if codes[0] != 200 {
		t.Fatalf("first request should pass: %v", codes)
	}
	if codes[1] != http.StatusTooManyRequests || codes[2] != http.StatusTooManyRequests {
		t.Errorf("spoofed header from untrusted peer must NOT bypass per-IP limit: %v", codes)
	}
}

func itoa(i int) string { return string(rune('0' + i)) }

func TestHealth(t *testing.T) {
	a, _ := newAPI(t)
	rec := do(t, a.Handler(), http.MethodGet, "http://api/v1/health", "", "1.2.3.4")
	if rec.Code != http.StatusOK {
		t.Errorf("health status = %d", rec.Code)
	}
}

func TestWrongMethod(t *testing.T) {
	a, _ := newAPI(t)
	rec := do(t, a.Handler(), http.MethodGet, "http://api/v1/register", "", "1.2.3.4")
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET register status = %d, want 405", rec.Code)
	}
}
