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
