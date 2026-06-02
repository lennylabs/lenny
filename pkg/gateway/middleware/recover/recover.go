// SPDX-License-Identifier: MIT

// Package recover hosts the gateway's HTTP panic-recovery middleware.
// A request goroutine panic on the gateway request path defaults — via
// net/http — to a recover that silently truncates the response. The
// active streaming session on that goroutine then sees an abrupt EOF
// with no diagnostic. §10.4 line 377 sets the contract that a gateway
// pod failure must be observable to the client (broken stream and
// reconnect) and must never lose session state. A request-scoped
// recover that converts a panic into an explicit 500 response and a
// structured server log keeps a single-handler crash from masquerading
// as a client-side disconnect.
//
// spec: §10.4 line 377. F-10.4.9.
package recover

import (
	"bufio"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"runtime/debug"
)

// Logger is the minimal surface the middleware uses to record a
// recovered panic. Implementations MUST NOT panic themselves (the
// recover guard does not re-enter).
type Logger interface {
	Printf(format string, args ...any)
}

// stdLogger wraps log.Printf so callers can drop in the standard logger
// without wrapping it themselves.
type stdLogger struct{}

func (stdLogger) Printf(format string, args ...any) {
	log.Printf(format, args...)
}

// Middleware wraps next so a handler-goroutine panic is converted to a
// 500 response and a structured server log entry rather than the Go
// runtime's silent recover-and-truncate default.
//
// The middleware writes the response header only if the inner handler
// has not already started a body (the net/http ResponseWriter tracks
// this via wroteHeader internally; the wrapper introspects the value
// it just emitted). When the inner handler has already started the
// body, no header is rewritten — the response is necessarily already
// truncated, but the client sees the established framing. The log
// line carries the request method, URL, recovered value, and a
// goroutine stack trace so the panic is forensically reconstructable.
//
// spec: §10.4 line 377. F-10.4.9.
func Middleware(next http.Handler) http.Handler {
	return MiddlewareWithLogger(next, stdLogger{})
}

// MiddlewareWithLogger is the test seam: it accepts an injectable
// Logger so unit tests can assert on the recovered-panic log line
// without relying on the global standard logger.
func MiddlewareWithLogger(next http.Handler, logger Logger) http.Handler {
	if logger == nil {
		logger = stdLogger{}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &recordingResponseWriter{ResponseWriter: w}
		defer func() {
			if rec := recover(); rec != nil {
				// http.ErrAbortHandler is the standard library's
				// documented way for a handler to abort silently
				// without logging — for instance a streaming handler
				// that detects the client disconnected. Honor it by
				// re-panicking so net/http closes the connection.
				if rec == http.ErrAbortHandler {
					panic(rec)
				}
				logger.Printf("lenny-gateway: §10.4 recovered handler panic on %s %s: %v\n%s",
					r.Method, r.URL.String(), rec, debug.Stack())
				if !rw.wroteHeader {
					rw.Header().Set("Content-Type", "application/json")
					rw.WriteHeader(http.StatusInternalServerError)
					_, _ = fmt.Fprint(rw, `{"error":{"code":"INTERNAL_ERROR","message":"gateway recovered from an internal handler panic"}}`)
				}
			}
		}()
		next.ServeHTTP(rw, r)
	})
}

// recordingResponseWriter tracks whether the inner handler wrote a
// status header before panicking. The middleware only writes its own
// 500 status when no header was emitted; otherwise the response is
// already on the wire and overriding it would surface as an HTTP/1.1
// protocol violation.
type recordingResponseWriter struct {
	http.ResponseWriter
	wroteHeader bool
}

func (w *recordingResponseWriter) WriteHeader(status int) {
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(status)
}

func (w *recordingResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
	}
	return w.ResponseWriter.Write(b)
}

// Flush forwards to the underlying writer when it implements
// http.Flusher. Required so the recovery wrapper does not break SSE
// streaming on the request path.
func (w *recordingResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack forwards to the underlying http.Hijacker so a WebSocket
// upgrade survives this wrapper. The §15.2 / §27.5 MCP WebSocket
// transport at /mcp/v1/ws upgrades through the full middleware chain;
// nhooyr.io/websocket performs a direct http.Hijacker type assertion on
// the ResponseWriter it is handed, so every wrapper between net/http and
// the upgrade handler must re-expose Hijack or the upgrade fails. spec:
// §27.5 / §27.3.1 line 142.
func (w *recordingResponseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, errors.New("recover: underlying ResponseWriter does not support hijacking")
}
