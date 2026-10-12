package proxypool

import (
	"net/http"
	"testing"
)

func TestClassify(t *testing.T) {
	hdr := func(kv ...string) http.Header {
		h := http.Header{}
		for i := 0; i+1 < len(kv); i += 2 {
			h.Set(kv[i], kv[i+1])
		}
		return h
	}
	cases := []struct {
		name   string
		status int
		h      http.Header
		err    string
		want   Strength
	}{
		{"ok", 200, nil, "", NotBlock},
		{"redirect", 301, nil, "", NotBlock},
		{"not found is a finding", 404, nil, "", NotBlock},
		{"429 always", 429, nil, "", Hard},
		{"bare 403 is soft", 403, nil, "", Soft},
		{"cloudflare challenge 403", 403, hdr("Cf-Mitigated", "challenge"), "", Hard},
		{"aws waf 403", 403, hdr("X-Amzn-Waf-Action", "captcha"), "", Hard},
		{"plain 503 is maintenance", 503, nil, "", NotBlock},
		{"503 retry-after is throttling", 503, hdr("Retry-After", "30"), "", Soft},
		{"firewall 503", 503, hdr("Cf-Mitigated", "challenge"), "", Hard},
		{"marker never upgrades a 200", 200, hdr("Cf-Mitigated", "challenge"), "", NotBlock},
		{"linkedin 999", 999, nil, "", Soft},
		{"500 is a finding", 500, nil, "", NotBlock},
		{"connection reset", 0, nil, `Get "x": read tcp: connection reset by peer`, Soft},
		{"connection refused", 0, nil, "dial tcp 1.2.3.4:443: connect: connection refused", Soft},
		{"timeout is not a block", 0, nil, "context deadline exceeded (Client.Timeout exceeded)", NotBlock},
	}
	for _, c := range cases {
		if got := Classify(c.status, c.h, c.err); got != c.want {
			t.Errorf("%s: Classify(%d) = %v, want %v", c.name, c.status, got, c.want)
		}
	}
}

func TestWindowTripsOnThreeHardInARow(t *testing.T) {
	var w Window
	for i, s := range []Strength{Hard, Hard} {
		if w.Observe(s) {
			t.Fatalf("tripped after %d hard signals", i+1)
		}
	}
	if !w.Observe(Hard) {
		t.Fatal("3 hard signals in a row must trip")
	}
	if w.Observe(Hard) {
		t.Fatal("a window trips once")
	}
}

func TestWindowHardRunBrokenByOtherAnswers(t *testing.T) {
	var w Window
	// 4 blocks (under the burst threshold), the hard run broken by a 200.
	for _, s := range []Strength{Hard, Hard, NotBlock, Hard, Hard} {
		if w.Observe(s) {
			t.Fatal("an interrupted hard run of 4 blocks must not trip")
		}
	}
}

func TestWindowTripsOnFiveBlocksInTwenty(t *testing.T) {
	var w Window
	tripped := -1
	for i := range 20 {
		s := NotBlock
		if i%4 == 0 {
			s = Soft // 0,4,8,12,16 → the 5th block arrives at i=16
		}
		if w.Observe(s) {
			tripped = i
			break
		}
	}
	if tripped != 16 {
		t.Fatalf("tripped at %d, want 16 (the fifth block within 20)", tripped)
	}
}

func TestWindowBlocksAgeOut(t *testing.T) {
	var w Window
	// 4 blocks, then 20 clean answers push them all out of the window.
	for range 4 {
		w.Observe(Soft)
	}
	for range WindowSize {
		w.Observe(NotBlock)
	}
	for range 4 {
		if w.Observe(Soft) {
			t.Fatal("blocks that aged out of the window must not count")
		}
	}
	if !w.Observe(Soft) {
		t.Fatal("5 blocks inside the window must trip")
	}
	if w.Seen() != 4+WindowSize+5 {
		t.Fatalf("Seen = %d", w.Seen())
	}
}

func TestEscalationRunsHooksOnce(t *testing.T) {
	e := NewEscalation(false)
	var calls []string
	e.OnEscalate(func() { calls = append(calls, "a") })
	e.OnEscalate(func() { calls = append(calls, "b") })
	if e.Escalated() {
		t.Fatal("new switch must start direct")
	}
	if !e.Escalate() || e.Escalate() {
		t.Fatal("only the first Escalate reports true")
	}
	select {
	case <-e.Done():
	default:
		t.Fatal("Done must be closed after the switch")
	}
	e.OnEscalate(func() { calls = append(calls, "late") })
	if got := len(calls); got != 3 || calls[0] != "a" || calls[1] != "b" || calls[2] != "late" {
		t.Fatalf("hooks = %v, want [a b late]", calls)
	}
}

func TestEscalationNilAndPreEscalated(t *testing.T) {
	var nilE *Escalation
	if nilE.Escalated() || nilE.Escalate() {
		t.Fatal("a nil switch never escalates")
	}
	nilE.OnEscalate(func() { t.Fatal("nil switch must not run hooks") })
	e := NewEscalation(true)
	ran := false
	e.OnEscalate(func() { ran = true })
	if !e.Escalated() || !ran {
		t.Fatal("a pre-escalated switch runs late hooks immediately")
	}
}
