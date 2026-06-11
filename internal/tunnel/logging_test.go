package tunnel

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// syncBuffer is a mutex-guarded log sink so a test goroutine can read what a
// handler goroutine has written so far (the smoking-gun case) under -race.
type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// bufLoggers builds a Loggers writing JSON to an in-memory buffer.
func bufLoggers(debug bool) (*Loggers, *syncBuffer) {
	buf := &syncBuffer{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	l := slog.New(h)
	return &Loggers{tunnel: l, access: l, debug: debug}, buf
}

// parseLines returns the JSON log records in the buffer, one per line.
func parseLines(t *testing.T, s string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("non-JSON log line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func lineByMsg(lines []map[string]any, msg string) map[string]any {
	for _, m := range lines {
		if m["msg"] == msg {
			return m
		}
	}
	return nil
}

func TestResolveLevel(t *testing.T) {
	cases := map[string]struct {
		want  slog.Level
		debug bool
	}{
		"":      {slog.LevelInfo, false},
		"info":  {slog.LevelInfo, false},
		"DEBUG": {slog.LevelDebug, true},
		"warn":  {slog.LevelWarn, false},
		"error": {slog.LevelError, false},
		"bogus": {slog.LevelInfo, false},
	}
	for in, want := range cases {
		t.Setenv(envLogLevel, in)
		got, debug := resolveLevel()
		if got != want.want || debug != want.debug {
			t.Errorf("resolveLevel(%q) = (%v,%v), want (%v,%v)", in, got, debug, want.want, want.debug)
		}
	}
}

func TestOpenLoggersWritesFiles(t *testing.T) {
	dir := t.TempDir()
	lg := openLoggers(dir)
	lg.Tunnel().Info("hello", "k", "v")
	lg.access.Info("request.start", "id", "abc")
	lg.Close()

	for _, name := range []string{logTunnelFile, logAccessFile} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

func TestOpenLoggersEmptyDirDiscards(t *testing.T) {
	lg := openLoggers("") // discard
	// Must not panic or create files; nothing to assert beyond survival.
	lg.Tunnel().Info("x")
	lg.access.Info("y")
	lg.Close()
}

func TestNilLoggersSafe(t *testing.T) {
	var lg *Loggers
	if lg.Tunnel() == nil {
		t.Fatal("nil Loggers.Tunnel() returned nil logger")
	}
	lg.Close() // no panic
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if rec.Code != http.StatusTeapot {
		t.Errorf("nil-Loggers passthrough broke: status %d", rec.Code)
	}
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer supersecret")
	h.Set("Cookie", "session=abc")
	h.Set("Set-Cookie", "session=abc")
	h.Set("Proxy-Authorization", "Basic x")
	h.Set("User-Agent", "claude")
	h.Set("Content-Type", "application/json")

	got := redactHeaders(h)
	for _, k := range []string{"Authorization", "Cookie", "Set-Cookie", "Proxy-Authorization"} {
		if got[k] != "[redacted]" {
			t.Errorf("%s = %q, want [redacted]", k, got[k])
		}
	}
	if got["User-Agent"] != "claude" {
		t.Errorf("User-Agent = %q, want passthrough", got["User-Agent"])
	}
	// Belt and suspenders: no secret value leaked anywhere in the flattened map.
	for k, v := range got {
		if strings.Contains(v, "supersecret") {
			t.Errorf("secret leaked under %s: %q", k, v)
		}
	}
}

func TestAccessHandlerStartAndEnd(t *testing.T) {
	lg, buf := bufLoggers(false)
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body) // so bytes_in is counted
		http.Error(w, "nope", http.StatusNotFound)
	}))

	// Query string present in the URL must NOT appear in the path-only log.
	req := httptest.NewRequest(http.MethodPost, "/.well-known/oauth-authorization-server?connect_secret=leak", strings.NewReader("hello"))
	req.Header.Set("User-Agent", "claude-connector/1.0")
	h.ServeHTTP(httptest.NewRecorder(), req)

	raw := buf.String()
	if strings.Contains(raw, "connect_secret") || strings.Contains(raw, "leak") {
		t.Fatalf("query string leaked into access log:\n%s", raw)
	}
	lines := parseLines(t, raw)
	start := lineByMsg(lines, "request.start")
	end := lineByMsg(lines, "request.end")
	if start == nil || end == nil {
		t.Fatalf("want both request.start and request.end, got:\n%s", raw)
	}
	if start["id"] != end["id"] {
		t.Errorf("start/end ids differ: %v vs %v", start["id"], end["id"])
	}
	if start["path"] != "/.well-known/oauth-authorization-server" {
		t.Errorf("path = %v, want path-only", start["path"])
	}
	if end["status"].(float64) != http.StatusNotFound {
		t.Errorf("status = %v, want 404", end["status"])
	}
	if end["bytes_in"].(float64) != 5 {
		t.Errorf("bytes_in = %v, want 5", end["bytes_in"])
	}
	if end["bytes_out"].(float64) <= 0 {
		t.Errorf("bytes_out = %v, want > 0", end["bytes_out"])
	}
	if end["ua"] != "claude-connector/1.0" {
		t.Errorf("ua = %v", end["ua"])
	}
	if end["sse"] != false {
		t.Errorf("sse = %v, want false for a JSON 404", end["sse"])
	}
	// At info level, no headers are logged.
	if _, ok := end["resp_headers"]; ok {
		t.Errorf("resp_headers present at info level: %v", end["resp_headers"])
	}
}

func TestAccessHandlerDebugLogsRedactedHeaders(t *testing.T) {
	lg, buf := bufLoggers(true) // debug
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Set-Cookie", "session=secretvalue")
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer topsecret")
	h.ServeHTTP(httptest.NewRecorder(), req)

	raw := buf.String()
	if strings.Contains(raw, "topsecret") || strings.Contains(raw, "secretvalue") {
		t.Fatalf("credential leaked at debug level:\n%s", raw)
	}
	lines := parseLines(t, raw)
	start := lineByMsg(lines, "request.start")
	if _, ok := start["req_headers"]; !ok {
		t.Errorf("debug level should log req_headers, got:\n%s", raw)
	}
}

func TestAccessHandlerSSE(t *testing.T) {
	lg, buf := bufLoggers(false)
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "data: hi\n\n")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))

	end := lineByMsg(parseLines(t, buf.String()), "request.end")
	if end["sse"] != true {
		t.Errorf("sse = %v, want true", end["sse"])
	}
	if end["stream_close"] != "upstream-eof" {
		t.Errorf("stream_close = %v, want upstream-eof", end["stream_close"])
	}
}

// TestAccessHandlerSmokingGun verifies the headline requirement: the start line
// is flushed before the request is served, so a request that never completes
// leaves a start with no matching end.
func TestAccessHandlerSmokingGun(t *testing.T) {
	lg, buf := bufLoggers(false)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release // hang until the test lets go
		w.WriteHeader(http.StatusOK)
	}))
	go func() {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
		close(done)
	}()

	<-started // handler is now blocked mid-request
	mid := buf.String()
	if !strings.Contains(mid, "request.start") {
		t.Fatalf("start line not flushed before serving:\n%s", mid)
	}
	if strings.Contains(mid, "request.end") {
		t.Fatalf("end line present while request still hanging:\n%s", mid)
	}

	close(release)
	<-done
	if !strings.Contains(buf.String(), "request.end") {
		t.Errorf("end line missing after completion:\n%s", buf.String())
	}
}

func TestStreamCloseReason(t *testing.T) {
	if got := streamCloseReason(context.Background()); got != "upstream-eof" {
		t.Errorf("live ctx = %q, want upstream-eof", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := streamCloseReason(ctx); got != "client-disconnect" {
		t.Errorf("cancelled ctx = %q, want client-disconnect", got)
	}
}

func TestNewReqIDUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		id := newReqID()
		if id == "" {
			t.Fatal("empty id")
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

func TestYamuxLogWriterRoutesByLevel(t *testing.T) {
	buf := &syncBuffer{}
	w := yamuxLogWriter{log: slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))}
	io.WriteString(w, "[ERR] yamux: keepalive failed: i/o timeout\n")
	io.WriteString(w, "[DEBUG] yamux: stream opened\n")

	lines := parseLines(t, buf.String())
	if len(lines) != 2 {
		t.Fatalf("want 2 lines, got %d", len(lines))
	}
	if lines[0]["level"] != "WARN" {
		t.Errorf("[ERR] keepalive routed to %v, want WARN", lines[0]["level"])
	}
	if lines[1]["level"] != "DEBUG" {
		t.Errorf("[DEBUG] line routed to %v, want DEBUG", lines[1]["level"])
	}
}

// TestAccessHandlerPreservesStreaming guards that the recorder still satisfies
// http.Flusher (Unwrap + Flush), so SSE bodies the proxy flushes get through.
func TestAccessHandlerPreservesStreaming(t *testing.T) {
	lg, _ := bufLoggers(false)
	var flushed bool
	h := lg.accessHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("response writer lost http.Flusher through the access recorder")
			return
		}
		io.WriteString(w, "chunk")
		f.Flush()
		flushed = true
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/mcp", nil))
	if !flushed {
		t.Error("handler never flushed")
	}
}

// TestClientProxyWritesAccessLog verifies the full wiring: a Client built with
// a LogDir forwards a request through proxyHandler and lands start/end lines in
// mcp-access.log on disk.
func TestClientProxyWritesAccessLog(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	dir := t.TempDir()
	c := New(Config{
		Identity:  sampleIdentity(),
		LocalAddr: strings.TrimPrefix(backend.URL, "http://"),
		LogDir:    dir,
	})
	defer c.lg.Close()

	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}"))
	req.RequestURI = ""
	c.proxyHandler().ServeHTTP(httptest.NewRecorder(), req)

	data, err := os.ReadFile(filepath.Join(dir, logAccessFile))
	if err != nil {
		t.Fatalf("reading access log: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, "request.start") || !strings.Contains(s, "request.end") {
		t.Errorf("access log missing start/end lines:\n%s", s)
	}
}
