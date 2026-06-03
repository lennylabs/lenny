// SPDX-License-Identifier: MIT

package opsserver

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/observability/correlation"
)

// withCorrelation extracts the §25.2 correlation headers
// (X-Lenny-Operation-ID, X-Lenny-Agent-Name, traceparent, X-Lenny-Tenant-ID,
// X-Lenny-Session-ID) from the inbound request and stamps them onto the
// request context as a correlation.Fields value. The §25.4 structured
// logging primitive (pkg/observability/logging) reads these via the
// context-aware slog handler and projects them as top-level structured log
// fields (operation_id, agent_name, trace_id, etc.).
//
// spec: §25.4 lines 2499-2509 / lines 2512-2526 — every lenny-ops log line
// MUST carry the correlation fields when present on the originating
// request. Component is stamped as "lenny-ops" (the §16.4 binary label).
func withCorrelation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		incoming := correlation.FromHTTPHeader(r.Header)
		// Stamp component=lenny-ops so the structured logger emits the
		// §16.4 binary label even when callers do not supply one.
		incoming.Component = "lenny-ops"
		ctx := correlation.With(r.Context(), correlation.From(r.Context()).Merge(incoming))
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withAccessLog emits a single structured access-log line per request
// after the handler returns. The slog handler chain (configured by main)
// projects the correlation.Fields onto the line, so a single grep of
// lenny-ops container logs for an operation_id surfaces the full request
// trail.
//
// spec: §25.4 lines 2499-2509 — operators index logs by operation_id /
// agent_name to correlate a restore failure across request, worker, and
// audit trail.
func withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The Kubernetes probes and the §16.9 Prometheus scrape endpoint
		// fire every few seconds; logging each one floods the access log
		// without adding correlation value. Pass them through unlogged,
		// matching the gateway's /metrics + /healthz access-log exclusion.
		if isHighFrequencyScrape(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.LogAttrs(r.Context(), slog.LevelInfo, "lenny-ops request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rw.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
	})
}

// isHighFrequencyScrape reports whether p is a path the kubelet or
// Prometheus polls on a short interval (the liveness/readiness probes and
// the §16.9 /metrics scrape). withAccessLog skips these so steady-state
// scrape traffic does not drown the operation-correlated request log.
func isHighFrequencyScrape(p string) bool {
	return p == "/healthz" || p == "/readyz" || p == "/metrics"
}

// statusRecorder captures the response status so withAccessLog can record
// it without reparsing the response. ResponseWriter does not expose the
// status code by default; recorders are the standard idiom.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(status int) {
	s.status = status
	s.ResponseWriter.WriteHeader(status)
}

// ContextWithComponent stamps component=lenny-ops on ctx for log lines
// emitted outside the HTTP request path (background loops, leader-elected
// reconcilers). spec: §16.4 every log line carries the binary label.
func ContextWithComponent(ctx context.Context) context.Context {
	return correlation.WithComponent(ctx, "lenny-ops")
}
