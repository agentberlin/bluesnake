package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	lumberjack "gopkg.in/natefinch/lumberjack.v2"
)

// This file is the tunnel client's observability: two JSON logs written under
// <storeDir>/logs and rotated by lumberjack — tunnel.log for connection
// lifecycle (dial/handshake/disconnect/reconnect) and mcp-access.log for one
// start line and one end line per request forwarded through the tunnel. The
// access log exists to answer "which request from a remote MCP client never
// completes": the start line is flushed before the request is served, so a
// start with no matching end is the smoking gun. See docs/TUNNEL.md.

const (
	logTunnelFile = "tunnel.log"
	logAccessFile = "mcp-access.log"
	envLogLevel   = "BLUESNAKE_LOG_LEVEL"

	// Rotation footprint per file: ~10MB live + 3 compressed backups, dropped
	// after 14 days — ~40MB worst case across both files.
	logMaxSizeMB  = 10
	logMaxBackups = 3
	logMaxAgeDays = 14
)

// Loggers holds the tunnel client's two file-backed loggers. A nil *Loggers is
// usable and discards everything, so the non-public path and most tests never
// touch the filesystem.
type Loggers struct {
	tunnel  *slog.Logger
	access  *slog.Logger
	debug   bool // BLUESNAKE_LOG_LEVEL=debug — adds redacted headers
	closers []io.Closer
}

// resolveLevel maps BLUESNAKE_LOG_LEVEL to a slog level. Default is info
// (lifecycle + access lines); debug additionally logs redacted headers.
func resolveLevel() (slog.Level, bool) {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(envLogLevel))) {
	case "debug":
		return slog.LevelDebug, true
	case "warn", "warning":
		return slog.LevelWarn, false
	case "error":
		return slog.LevelError, false
	default:
		return slog.LevelInfo, false
	}
}

// openLoggers builds file-backed loggers under logDir. An empty logDir (the
// tunnel running without a configured log directory) yields discard loggers.
// lumberjack creates logDir lazily on first write, so this never fails.
func openLoggers(logDir string) *Loggers {
	level, debug := resolveLevel()
	if logDir == "" {
		d := slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: level}))
		return &Loggers{tunnel: d, access: d, debug: debug}
	}
	mk := func(name string) (*slog.Logger, io.Closer) {
		w := &lumberjack.Logger{
			Filename:   filepath.Join(logDir, name),
			MaxSize:    logMaxSizeMB,
			MaxBackups: logMaxBackups,
			MaxAge:     logMaxAgeDays,
			Compress:   true,
		}
		return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level})), w
	}
	tl, tc := mk(logTunnelFile)
	al, ac := mk(logAccessFile)
	return &Loggers{tunnel: tl, access: al, debug: debug, closers: []io.Closer{tc, ac}}
}

// Tunnel returns the lifecycle logger (always non-nil).
func (l *Loggers) Tunnel() *slog.Logger {
	if l == nil {
		return slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return l.tunnel
}

// Close flushes and closes the underlying rotating files.
func (l *Loggers) Close() {
	if l == nil {
		return
	}
	for _, c := range l.closers {
		_ = c.Close()
	}
}

// accessHandler wraps next with the per-request access log. It emits one
// request.start line (flushed before the request is served, so a hang shows up
// as a start with no end) and one request.end line on completion.
func (l *Loggers) accessHandler(next http.Handler) http.Handler {
	if l == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newReqID()
		start := time.Now()

		// Count request-body bytes accurately even when Content-Length is
		// unknown (chunked uploads report -1).
		cr := &countingReader{rc: r.Body}
		r.Body = cr

		startAttrs := []any{
			"id", id,
			"method", r.Method,
			"path", r.URL.Path, // path only — never RawQuery or the full URL
			"ua", r.UserAgent(),
		}
		if l.debug {
			startAttrs = append(startAttrs, "req_headers", redactHeaders(r.Header))
		}
		l.access.Info("request.start", startAttrs...)

		rec := &accessRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		sse := strings.HasPrefix(strings.ToLower(rec.contentType), "text/event-stream")
		endAttrs := []any{
			"id", id,
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes_in", cr.n,
			"bytes_out", rec.written,
			"ua", r.UserAgent(),
			"sse", sse,
		}
		if sse {
			// For an SSE response ServeHTTP blocks until the stream ends, so
			// duration_ms above is the stream duration; record why it closed.
			endAttrs = append(endAttrs, "stream_close", streamCloseReason(r.Context()))
		}
		if l.debug {
			endAttrs = append(endAttrs, "resp_headers", redactHeaders(rec.Header()))
		}
		l.access.Info("request.end", endAttrs...)
	})
}

// streamCloseReason distinguishes the remote MCP client hanging up from the
// local server closing the stream, inferred from the request context.
func streamCloseReason(ctx context.Context) string {
	if ctx.Err() != nil {
		return "client-disconnect"
	}
	return "upstream-eof"
}

// countingReader tallies bytes read from the request body.
type countingReader struct {
	rc io.ReadCloser
	n  int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) Close() error { return c.rc.Close() }

// accessRecorder captures the response status, byte count, and Content-Type
// for the access log. It implements Unwrap and Flush so http.ResponseController
// and the reverse proxy's per-write flushing (FlushInterval -1) keep streaming
// SSE bodies immediately.
type accessRecorder struct {
	http.ResponseWriter
	status      int
	written     int64
	contentType string
	wroteHeader bool
}

func (r *accessRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.contentType = r.ResponseWriter.Header().Get("Content-Type")
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *accessRecorder) Write(p []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	n, err := r.ResponseWriter.Write(p)
	r.written += int64(n)
	return n, err
}

// Unwrap exposes the underlying writer to http.ResponseController.
func (r *accessRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

// Flush forwards to the underlying writer so streamed responses flush through.
func (r *accessRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// sensitiveHeaders are never written, even at debug level. The connect secret
// and pepper never travel as HTTP headers on this path, but Authorization and
// Cookie can carry credentials, so they are redacted.
var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
}

// redactHeaders flattens a header map for debug logging, masking credential
// headers and never including request/response bodies.
func redactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			out[k] = "[redacted]"
			continue
		}
		out[k] = strings.Join(v, ", ")
	}
	return out
}

// reqSeq backs the fallback request id if crypto/rand is unavailable.
var reqSeq uint64

// newReqID returns a short id correlating a request's start and end lines.
func newReqID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatUint(atomic.AddUint64(&reqSeq, 1), 16)
	}
	return hex.EncodeToString(b[:])
}

// yamuxLogWriter routes yamux's internal logging (keepalive/heartbeat failures,
// stream errors) into the tunnel lifecycle log instead of discarding it.
type yamuxLogWriter struct{ log *slog.Logger }

func (w yamuxLogWriter) Write(p []byte) (int, error) {
	line := strings.TrimRight(string(p), "\n")
	switch {
	case strings.Contains(line, "[ERR]"), strings.Contains(line, "[WARN]"):
		w.log.Warn("yamux", "msg", line)
	default:
		w.log.Debug("yamux", "msg", line)
	}
	return len(p), nil
}
