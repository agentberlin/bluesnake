package ratelimit

import (
	"testing"
	"time"
)

func TestAllowBurstThenDeny(t *testing.T) {
	l := New(1, 3) // 3 burst, 1/sec
	for i := 0; i < 3; i++ {
		if !l.Allow("ip") {
			t.Fatalf("request %d within burst should be allowed", i)
		}
	}
	if l.Allow("ip") {
		t.Error("4th request should be denied after burst exhausted")
	}
}

func TestRefill(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 1)
	l.now = func() time.Time { return now }

	if !l.Allow("ip") {
		t.Fatal("first allowed")
	}
	if l.Allow("ip") {
		t.Fatal("second denied immediately")
	}
	now = now.Add(time.Second) // one token refilled
	if !l.Allow("ip") {
		t.Error("should be allowed after 1s refill")
	}
}

func TestPerKeyIsolation(t *testing.T) {
	l := New(1, 1)
	if !l.Allow("a") {
		t.Fatal("a first")
	}
	if !l.Allow("b") {
		t.Error("b should have its own bucket")
	}
	if l.Allow("a") {
		t.Error("a second should be denied")
	}
}

func TestIPKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"203.0.113.7", "203.0.113.7"},                // IPv4 unchanged
		{"::ffff:203.0.113.7", "203.0.113.7"},         // 4-in-6 unmapped to IPv4
		{"2001:db8:1:2::aaaa", "2001:db8:1:2::/64"},   // IPv6 → /64 prefix
		{"2001:db8:1:2:ffff::1", "2001:db8:1:2::/64"}, // same /64 → same key
		{"2001:db8:1:3::1", "2001:db8:1:3::/64"},      // different /64 → different key
		{"not-an-ip", "not-an-ip"},                    // pass through
		{"", ""},
	}
	for _, c := range cases {
		if got := IPKey(c.in); got != c.want {
			t.Errorf("IPKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGCDropsIdleBuckets(t *testing.T) {
	now := time.Unix(0, 0)
	l := New(1, 1)
	l.now = func() time.Time { return now }
	l.Allow("a")
	if l.Len() != 1 {
		t.Fatalf("len = %d, want 1", l.Len())
	}
	now = now.Add(10 * time.Minute) // long idle + GC interval passed
	l.Allow("b")                    // triggers gc
	if l.Len() != 1 {
		t.Errorf("idle bucket 'a' should be collected; len = %d", l.Len())
	}
}
