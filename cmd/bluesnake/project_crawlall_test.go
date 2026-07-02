package main

// #74 N3: `projects crawl-all` used to block solely on the group observer's
// done channel. A Ctrl-C with members still queued cancelled the drain loops,
// so those members' OnDone never fired, the counter never reached the total,
// and the command hung forever — with the deferred signal-stop also swallowing
// the second Ctrl-C.

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"testing"
	"time"
)

// runCmdCtx executes the root command with a caller context, so a test can
// cancel it mid-run (the in-process equivalent of Ctrl-C: signal.NotifyContext
// wraps this context, and cancelling the parent cancels the crawl).
func runCmdCtx(ctx context.Context, args ...string) (string, int) {
	root := newRootCmd()
	var buf bytes.Buffer
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.ExecuteContext(ctx)
	if err == nil {
		return buf.String(), 0
	}
	code := 1
	var ee exitErr
	if errors.As(err, &ee) {
		code = ee.code
	}
	return buf.String(), code
}

// hangListener accepts TCP connections and never responds, so an https fetch
// against it parks in the TLS handshake until its context is cancelled —
// holding the first member crawl deterministically in flight.
func hangListener(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			defer conn.Close() // hold it open, say nothing
		}
	}()
	return ln.Addr().String()
}

func TestProjectCrawlAll_InterruptDoesNotHang(t *testing.T) {
	dir := t.TempDir()
	main := hangListener(t)

	out, code := runCmd(t, "projects", "create", main, "--store-dir", dir)
	if code != 0 {
		t.Fatalf("create: exit %d, output:\n%s", code, out)
	}
	m := regexp.MustCompile(`created project (\S+)`).FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no project id in output:\n%s", out)
	}
	projectID := m[1]
	// Two more members that stay QUEUED behind the hanging member 1
	// (parallel defaults to 1) — the exact shape that used to hang forever.
	for _, d := range []string{"192.0.2.10", "192.0.2.11"} {
		if out, code := runCmd(t, "projects", "add", projectID, d, "--store-dir", dir); code != 0 {
			t.Fatalf("add %s: exit %d, output:\n%s", d, code, out)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(500 * time.Millisecond) // member 1 is parked in its TLS handshake
		cancel()
	}()

	type res struct {
		out  string
		code int
	}
	done := make(chan res, 1)
	go func() {
		out, code := runCmdCtx(ctx, "projects", "crawl-all", projectID, "--store-dir", dir)
		done <- res{out, code}
	}()

	select {
	case r := <-done:
		if r.code != 3 {
			t.Errorf("interrupted crawl-all exit = %d, want 3, output:\n%s", r.code, r.out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("crawl-all hung after interrupt with members still queued (N3)")
	}
}
