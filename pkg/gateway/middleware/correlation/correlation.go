// SPDX-License-Identifier: MIT

// Package correlation hosts the gateway's HTTP correlation middleware.
//
// §16.4 line 372 requires every log line to carry the agent-operability
// correlation fields when present on the originating request: operation_id
// (from the X-Lenny-Operation-ID header) and agent_name (from
// X-Lenny-Agent-Name), alongside the §16.4 line 371 session_id, tenant_id,
// trace_id, and span_id. The shared logging handler
// (pkg/observability/logging) projects those values onto every record from a
// correlation.Fields value stored on the record's context — but nothing in
// the request path ever populated that context from the inbound headers.
//
// This middleware closes that gap: it reads the spec-defined correlation
// headers off each request, merges them onto any Fields already on the
// request context, and re-attaches the merged value so every downstream
// handler and log line inherits them. It then emits one structured
// request-completion log line per request, which carries the projected
// correlation fields — the concrete log surface §16.4 line 372 promises can
// be joined across binaries via operation_id.
//
// spec: §16.4 lines 371-372; §25.4 (X-Lenny-Operation-ID / X-Lenny-Agent-Name).
// F-16.4.2, F-16.4.3.
package correlation

import (
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

// Options configures the middleware.
type Options struct {
	// Logger is the structured logger the request-completion line is emitted
	// to. nil resolves to slog.Default(), which the binary's logging.Setup
	// installs as the §16.4 JSON handler.
	Logger *slog.Logger

	// SkipPaths suppresses the request-completion log line for the listed
	// exact paths. Liveness, readiness, and metrics scrape endpoints are the
	// usual entries so probe traffic does not flood the access log. The
	// correlation context is still attached for skipped paths; only the log
	// line is suppressed.
	SkipPaths map[string]bool
}

// Wrap returns a handler that attaches the inbound correlation headers to
// the request context and emits a structured request-completion log line.
// It is intended to be the outermost middleware so the context is populated
// before any inner handler logs, and so the completion line observes the
// final response status.
//
// spec: §16.4 lines 371-372. F-16.4.2, F-16.4.3.
func Wrap(next http.Handler, opts Options) http.Handler {
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// spec: §16.4 lines 371-372 / §25.4 — read X-Lenny-Operation-ID,
		// X-Lenny-Agent-Name, traceparent, X-Lenny-Session-ID, and
		// X-Lenny-Tenant-ID, then merge them onto whatever scope the context
		// already carried. Merge lets an inner-set value survive an absent
		// header rather than being clobbered with the empty string.
		incoming := corr.FromHTTPHeader(r.Header)
		merged := corr.From(r.Context()).Merge(incoming)
		ctx := corr.With(r.Context(), merged)

		// spec: §16.3 lines 320, 326 ("Client → Gateway (HTTP headers)") —
		// extract the inbound W3C traceparent through the process-wide OTel
		// propagator so a span opened by a downstream §16.3 handler continues
		// the client's trace instead of starting a detached root. corr.From
		// captures trace_id/span_id as strings for the §16.4 log line; this
		// step establishes the real OTel remote SpanContext that tracer.Start
		// parents under. F-16.3.3.
		ctx = otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(r.Header))
		r = r.WithContext(ctx)

		if opts.SkipPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		cw := &captureRW{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(cw, r)

		// The logging handler projects the correlation fields from ctx onto
		// this record, so operation_id / agent_name / session_id / tenant_id /
		// trace_id appear on the line when the request carried them.
		logger.LogAttrs(ctx, slog.LevelInfo, "http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", cw.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
			slog.Int("bytes", cw.bytes),
		)
	})
}

// captureRW records the response status and byte count for the completion
// log line. It forwards Flush so the outermost wrapper does not hide SSE
// streaming, and exposes the underlying writer via Unwrap so net/http's
// ResponseController can traverse to capabilities (Hijack, deadlines) the
// inner handlers rely on.
type captureRW struct {
	http.ResponseWriter
	status      int
	bytes       int
	wroteHeader bool
}

func (c *captureRW) WriteHeader(code int) {
	if !c.wroteHeader {
		c.status = code
		c.wroteHeader = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *captureRW) Write(b []byte) (int, error) {
	if !c.wroteHeader {
		c.wroteHeader = true
	}
	n, err := c.ResponseWriter.Write(b)
	c.bytes += n
	return n, err
}

// Flush forwards to the underlying http.Flusher when present. The §15.1 SSE
// event stream and §4.9 LLM-proxy streaming translators rely on http.Flusher
// to push partial bytes; without this forwarder this outermost wrapper would
// hide it and break streaming.
func (c *captureRW) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		if !c.wroteHeader {
			c.wroteHeader = true
		}
		f.Flush()
	}
}

// Unwrap exposes the wrapped writer to net/http's ResponseController.
func (c *captureRW) Unwrap() http.ResponseWriter { return c.ResponseWriter }
