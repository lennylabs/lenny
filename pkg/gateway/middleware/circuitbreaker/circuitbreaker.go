// SPDX-License-Identifier: MIT

// Package circuitbreaker wraps any http.Handler with the §11.6
// operator-managed circuit-breaker admission gate. The middleware
// runs after authentication and before any interceptor chain (per
// §11.6 "pre-chain gate"), so requests carry a resolved caller
// identity for the audit row but are rejected before quota / policy
// evaluation.
//
// Production reads the breaker set from Redis (with the in-process
// cache the spec mandates); this package provides an in-memory
// Registry that satisfies the same shape for the minimal gateway and
// the tier-3 contract tests.
package circuitbreaker

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/circuitbreaker"
)

// Registry returns the current set of breakers the middleware
// evaluates on every request. Production wires this to the Redis
// cb:{name} cache; tests use MemoryRegistry below.
type Registry interface {
	Snapshot(ctx context.Context) ([]circuitbreaker.Breaker, error)
}

// MemoryRegistry is an in-memory Registry. Set replaces the current
// snapshot atomically; readers see a consistent set.
type MemoryRegistry struct {
	mu       sync.RWMutex
	breakers []circuitbreaker.Breaker
}

// NewMemoryRegistry returns an empty Registry.
func NewMemoryRegistry() *MemoryRegistry {
	return &MemoryRegistry{breakers: []circuitbreaker.Breaker{}}
}

// Set replaces the registry contents with the supplied breakers.
func (r *MemoryRegistry) Set(breakers []circuitbreaker.Breaker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.breakers = append(r.breakers[:0], breakers...)
}

// Snapshot returns a copy of the current breakers.
func (r *MemoryRegistry) Snapshot(_ context.Context) ([]circuitbreaker.Breaker, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]circuitbreaker.Breaker, len(r.breakers))
	copy(out, r.breakers)
	return out, nil
}

// RequestExtractor returns the §11.6 admission Request the middleware
// uses to match against open breakers. The minimal gateway extracts
// from request body (POST /v1/sessions carries runtimeRef); production
// wires this to the authenticated principal + the resolved runtime /
// pool / connector / operation_type.
type RequestExtractor func(*http.Request) circuitbreaker.Request

// SnapshotExtractor returns the §16.7 admission-time request snapshot
// recorded on the `admission.circuit_breaker_rejected` audit row. It
// resolves the authenticated caller identity (attached by the auth
// middleware, which runs before this pre-chain gate) and the requested
// runtime/pool. A nil extractor records only what the breaker carries.
type SnapshotExtractor func(*http.Request) RejectionSnapshot

// Options configures a Middleware.
type Options struct {
	Extract RequestExtractor
	// Audit, when set, emits the §16.7 `admission.circuit_breaker_rejected`
	// audit event on a breaker match, applying the §11.6 per-replica
	// sampling discipline. When nil the middleware still rejects but
	// writes no audit row.
	Audit *AuditReporter
	// Snapshot resolves the audit row's request snapshot from the
	// request. Ignored when Audit is nil.
	Snapshot SnapshotExtractor
	// CacheAge reports how long ago the breaker registry's in-process
	// cache last refreshed from Redis. When set and the age exceeds the
	// §11.7 5-second staleness budget, every admission decision is
	// recorded as a §16.7 `admission.circuit_breaker_cache_stale` serve
	// (sampled) via Audit. Nil (the MemoryRegistry / non-cached path)
	// disables stale-serve detection. The production wiring passes the
	// cachingstore's LastRefresh as `time.Since(LastRefresh())`.
	CacheAge func() time.Duration
}

// Wrap returns an http.Handler that consults the Registry on every
// request. When an open breaker matches the request per
// §11.6 FirstMatch, the middleware returns 503 CIRCUIT_BREAKER_OPEN
// without invoking the inner handler.
func Wrap(inner http.Handler, reg Registry, opts Options) http.Handler {
	if opts.Extract == nil {
		opts.Extract = defaultExtract
	}
	return &middleware{inner: inner, reg: reg, opts: opts}
}

type middleware struct {
	inner http.Handler
	reg   Registry
	opts  Options
}

func (m *middleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	breakers, err := m.reg.Snapshot(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
		return
	}
	match := circuitbreaker.FirstMatch(breakers, m.opts.Extract(r))
	// spec: §16.7 line 679 — a decision served against a cache that has
	// not refreshed within the §11.7 5-second budget is recorded (sampled)
	// before the request is admitted or rejected, so the audit trail shows
	// which decisions were made blind during a Redis outage.
	m.reportCacheStale(r, match != nil)
	if match != nil {
		// spec: §11.6 line 327 — the gate runs after AuthEvaluator, so
		// the audit row carries the authenticated caller identity even
		// though the breaker match criteria do not reference the tenant.
		if m.opts.Audit != nil {
			var snap RejectionSnapshot
			if m.opts.Snapshot != nil {
				snap = m.opts.Snapshot(r)
			}
			m.opts.Audit.Report(r.Context(), *match, snap)
		}
		writeBreakerRejection(w, *match)
		return
	}
	m.inner.ServeHTTP(w, r)
}

// reportCacheStale records the §16.7 `admission.circuit_breaker_cache_stale`
// serve when the breaker cache age exceeds the staleness budget. It is a
// no-op when stale-serve detection is disabled (no CacheAge source or no
// Audit reporter) or the cache is fresh.
func (m *middleware) reportCacheStale(r *http.Request, rejected bool) {
	if m.opts.Audit == nil || m.opts.CacheAge == nil {
		return
	}
	age := m.opts.CacheAge()
	if age <= CacheStaleAfter {
		return
	}
	outcome := "admitted"
	if rejected {
		outcome = "rejected"
	}
	var snap RejectionSnapshot
	if m.opts.Snapshot != nil {
		snap = m.opts.Snapshot(r)
	}
	m.opts.Audit.ReportCacheStale(r.Context(), outcome, age.Seconds(), snap)
}

func writeBreakerRejection(w http.ResponseWriter, b circuitbreaker.Breaker) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "0")
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    "CIRCUIT_BREAKER_OPEN",
			"message": "circuit breaker " + b.Name + " is open: " + b.Reason,
			"details": map[string]any{
				"circuitName": b.Name,
				"reason":      b.Reason,
				"limitTier":   string(b.LimitTier),
				"openedAt":    b.OpenedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
			},
		},
	})
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{"code": code, "message": message},
	})
}

// defaultExtract reads the request fields the minimal gateway can
// observe at admission time. The body's runtimeRef / pool fields are
// extracted by the dedicated handler; this default falls back to the
// path-derived operation_type so unauthenticated probes still match
// the operation_type tier.
func defaultExtract(r *http.Request) circuitbreaker.Request {
	req := circuitbreaker.Request{}
	switch r.Method + " " + pathHead(r.URL.Path) {
	case "POST /v1/sessions":
		req.OperationType = circuitbreaker.OpSessionCreation
	}
	return req
}

func pathHead(p string) string {
	// Trim trailing path segments so we match the canonical
	// "POST /v1/sessions" without the `{id}` suffix.
	depth := 0
	for i := 0; i < len(p); i++ {
		if p[i] == '/' {
			depth++
			if depth > 2 {
				return p[:i]
			}
		}
	}
	return p
}
