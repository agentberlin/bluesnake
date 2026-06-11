package store

import (
	"context"
	"regexp"
	"testing"
)

func TestNewIDFormat(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9]{12}$`)
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id, err := NewID()
		if err != nil {
			t.Fatal(err)
		}
		if !re.MatchString(id) {
			t.Fatalf("id %q does not match DNS-label format", id)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q within 1000 draws", id)
		}
		seen[id] = true
	}
}

func TestNewSecretAndToken(t *testing.T) {
	s1, _ := NewSecret()
	s2, _ := NewSecret()
	if s1 == s2 {
		t.Error("two secrets collided")
	}
	if len(s1) < 40 {
		t.Errorf("secret too short: %d chars", len(s1))
	}
}

func TestHashDeterministicAndDistinct(t *testing.T) {
	if string(Hash("abc")) != string(Hash("abc")) {
		t.Error("hash not deterministic")
	}
	if string(Hash("abc")) == string(Hash("abd")) {
		t.Error("hash collision on distinct inputs")
	}
	if len(Hash("x")) != 32 {
		t.Errorf("hash length = %d, want 32", len(Hash("x")))
	}
}

func TestHashPepper(t *testing.T) {
	plain := Hash("secret")
	SetPepper([]byte("pep-1"))
	defer SetPepper(nil)

	peppered := Hash("secret")
	if string(peppered) != string(Hash("secret")) {
		t.Error("peppered hash not deterministic")
	}
	if string(peppered) == string(plain) {
		t.Error("pepper did not change the hash")
	}
	if len(peppered) != 32 {
		t.Errorf("peppered hash length = %d, want 32", len(peppered))
	}
	// A different pepper yields a different hash.
	SetPepper([]byte("pep-2"))
	if string(Hash("secret")) == string(peppered) {
		t.Error("different pepper produced the same hash")
	}
}

func TestMemStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	m := NewMem()
	tn := &Tunnel{ID: "abc123def456", ConnectSecretHash: Hash("cs")}

	if err := m.Create(ctx, tn); err != nil {
		t.Fatal(err)
	}
	if err := m.Create(ctx, tn); err != ErrConflict {
		t.Errorf("duplicate create = %v, want ErrConflict", err)
	}

	got, err := m.GetByID(ctx, tn.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.ConnectSecretHash) != string(Hash("cs")) {
		t.Error("connect secret hash mismatch")
	}

	if _, err := m.GetByID(ctx, "nope"); err != ErrNotFound {
		t.Errorf("missing get = %v, want ErrNotFound", err)
	}

	if err := m.MarkConnected(ctx, tn.ID); err != nil {
		t.Fatal(err)
	}
	if m.ConnectCount(tn.ID) != 1 {
		t.Errorf("connect count = %d, want 1", m.ConnectCount(tn.ID))
	}
}

func TestMemStoreGetReturnsCopy(t *testing.T) {
	ctx := context.Background()
	m := NewMem()
	tn := &Tunnel{ID: "abc123def456", ConnectSecretHash: Hash("cs")}
	_ = m.Create(ctx, tn)
	got, _ := m.GetByID(ctx, tn.ID)
	got.ConnectSecretHash[0] ^= 0xff // mutate caller's copy
	again, _ := m.GetByID(ctx, tn.ID)
	if string(again.ConnectSecretHash) != string(Hash("cs")) {
		t.Error("mutating returned tunnel corrupted store state")
	}
}
