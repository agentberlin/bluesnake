package registry

import (
	"net"
	"testing"
	"time"

	"github.com/hashicorp/yamux"
)

// pairSession returns a server-side yamux session connected to a client-side
// one over an in-memory pipe. Both are closed via t.Cleanup.
func pairSession(t *testing.T) *yamux.Session {
	t.Helper()
	c1, c2 := net.Pipe()
	server, err := yamux.Server(c1, nil)
	if err != nil {
		t.Fatal(err)
	}
	client, err := yamux.Client(c2, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close(); client.Close() })
	return server
}

func TestAddEvictsPrior(t *testing.T) {
	r := New()
	s1 := NewSession("id", "h", pairSession(t), time.Now())
	s2 := NewSession("id", "h", pairSession(t), time.Now())

	if ev := r.Add(s1); ev != nil {
		t.Errorf("first add evicted %v, want nil", ev)
	}
	ev := r.Add(s2)
	if ev != s1 {
		t.Error("second add should evict the first session")
	}
	if r.Get("id") != s2 {
		t.Error("registry should hold the newest session")
	}
	if r.Count() != 1 {
		t.Errorf("count = %d, want 1", r.Count())
	}
}

func TestRemoveOnlyCurrent(t *testing.T) {
	r := New()
	s1 := NewSession("id", "h", pairSession(t), time.Now())
	s2 := NewSession("id", "h", pairSession(t), time.Now())
	r.Add(s1)
	r.Add(s2) // s2 is now current

	// A late cleanup of the evicted s1 must not remove s2.
	r.Remove(s1)
	if r.Get("id") != s2 {
		t.Error("removing stale session clobbered current one")
	}
	r.Remove(s2)
	if r.Get("id") != nil {
		t.Error("removing current session should clear it")
	}
}

func TestGetMissing(t *testing.T) {
	if New().Get("nope") != nil {
		t.Error("missing tunnel should return nil")
	}
}
