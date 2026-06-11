package config

import (
	"strings"
	"testing"
)

func TestWaitStrategyDefault(t *testing.T) {
	c := Default()
	if c.Rendering.WaitStrategy != "adaptive" {
		t.Errorf("Default().Rendering.WaitStrategy = %q, want %q", c.Rendering.WaitStrategy, "adaptive")
	}
	got, err := c.Get("rendering.wait_strategy")
	if err != nil {
		t.Fatalf("Get(rendering.wait_strategy): %v", err)
	}
	if got != "adaptive" {
		t.Errorf("Get(rendering.wait_strategy) = %q, want %q", got, "adaptive")
	}
}

func TestWaitStrategyLoadFixed(t *testing.T) {
	c, err := Load([]byte("rendering:\n  wait_strategy: fixed\n"))
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{
		"rendering.wait_strategy":    "fixed",
		"rendering.ajax_timeout_sec": "5", // untouched default in the same block
	} {
		if got, _ := c.Get(k); got != want {
			t.Errorf("Get(%q) = %q, want %q", k, got, want)
		}
	}
}

func TestWaitStrategyInvalidValueRejected(t *testing.T) {
	_, err := Load([]byte("rendering:\n  wait_strategy: eager\n"))
	if err == nil {
		t.Fatal("wait_strategy=eager must fail validation, got nil")
	}
	if !strings.Contains(err.Error(), "rendering.wait_strategy") {
		t.Fatalf("error %q must mention rendering.wait_strategy", err.Error())
	}
}

func TestWaitStrategySetRoundTrip(t *testing.T) {
	c := Default()
	if err := c.Set("rendering.wait_strategy=fixed"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate after Set: %v", err)
	}
	got, err := c.Get("rendering.wait_strategy")
	if err != nil {
		t.Fatal(err)
	}
	if got != "fixed" {
		t.Errorf("Get(rendering.wait_strategy) = %q, want %q", got, "fixed")
	}
}

func TestWaitStrategyUnknownKeyRejectedBySet(t *testing.T) {
	c := Default()
	if err := c.Set("rendering.waitstrategy=fixed"); err == nil {
		t.Error("Set(rendering.waitstrategy=fixed) must fail: unknown key")
	}
}
