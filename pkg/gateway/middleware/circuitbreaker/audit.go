// SPDX-License-Identifier: MIT

package circuitbreaker

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/circuitbreaker"
)

// EventAdmissionCircuitBreakerRejected is the §16.7 audit event type
// written when the §11.6 pre-chain AdmissionController gate rejects a
// request because an open breaker matched. §4.8 contrasts it explicitly
// with `interceptor.rejected`: the circuit-breaker gate is not an
// interceptor, so a breaker REJECT produces this distinct event type.
const EventAdmissionCircuitBreakerRejected = "admission.circuit_breaker_rejected"

// EventAdmissionCircuitBreakerCacheStale is the §16.7 line 679 audit
// event written (sampled) when the §11.6 admission gate serves a decision
// against a breaker cache that has not refreshed within the 5-second poll
// interval. The security-salient case is outcome="admitted": a breaker
// whose state the admission path could not verify did not block the
// request.
const EventAdmissionCircuitBreakerCacheStale = "admission.circuit_breaker_cache_stale"

// CacheStaleAfter is the §11.7 staleness budget: an admission cache older
// than this when a decision is served is "stale" for the §16.7
// cache-stale audit event and the stale-serve counter.
const CacheStaleAfter = 5 * time.Second

// auditSamplingWindow is the §11.6 line 331 rolling window: the first
// rejection per (tenant_id, circuit_name, caller_sub) within any
// 10-second window is written as a full audit row; subsequent
// rejections in the same window are suppressed.
const auditSamplingWindow = 10 * time.Second

// AuditAppender commits an `admission.circuit_breaker_rejected` row to a
// tenant's §11.7 hash chain. The signature matches the policy package's
// audit appender so the gateway passes the same backend (durable
// Postgres or in-memory) without an adapter.
type AuditAppender interface {
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// RejectionMetrics records the §11.6 line 333 rejection counters. The
// gateway metrics object satisfies it.
type RejectionMetrics interface {
	// RecordCircuitBreakerRejection counts every breaker-caused
	// rejection, including those whose audit row is sampled away.
	RecordCircuitBreakerRejection(tenantID, circuitName, limitTier string)
	// RecordCircuitBreakerRejectionSuppressed counts only the
	// rejections whose audit row was elided by sampling.
	RecordCircuitBreakerRejectionSuppressed(tenantID, circuitName, limitTier string)
	// RecordCircuitBreakerCacheStaleServe counts every admission decision
	// served against a stale breaker cache, labelled by outcome
	// (rejected | admitted). spec: §16.1 line 218.
	RecordCircuitBreakerCacheStaleServe(outcome string)
}

// AuditReporter emits the §16.7 `admission.circuit_breaker_rejected`
// audit event on a breaker match, applying the §11.6 per-replica
// sampling discipline and the §11.6 line 333 rejection counters. The
// sampling window is held in this replica's memory, keyed by
// (tenant_id, circuit_name, caller_sub) — explicitly per-replica with
// no Redis or EventBus coordination (spec: §11.6 line 331 "Sampling
// window locality").
type AuditReporter struct {
	appender  AuditAppender
	metrics   RejectionMetrics
	replicaID string
	clock     func() time.Time
	mu        sync.Mutex
	lastWrite map[string]time.Time
}

// NewAuditReporter returns an AuditReporter backed by appender. metrics
// may be nil. replicaID populates the audit row's
// replica_service_instance_id. clock overrides time.Now; pass nil in
// production.
func NewAuditReporter(appender AuditAppender, metrics RejectionMetrics, replicaID string, clock func() time.Time) *AuditReporter {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &AuditReporter{
		appender:  appender,
		metrics:   metrics,
		replicaID: replicaID,
		clock:     clock,
		lastWrite: make(map[string]time.Time),
	}
}

// RejectionSnapshot is the admission-time request snapshot the §16.7
// audit row records alongside the matched breaker.
type RejectionSnapshot struct {
	// CallerSub and CallerTenantID are the authenticated caller
	// identity resolved by AuthEvaluator before the pre-chain gate runs
	// (spec: §11.6 line 327).
	CallerSub      string
	CallerTenantID string
	// Runtime and Pool are the requested admission targets, when
	// resolvable at admission time.
	Runtime string
	Pool    string
	// SessionID is set when admitting a continuation; ParentSessionID
	// and DelegationDepth are set when admitting a delegation child.
	SessionID       string
	ParentSessionID string
	DelegationDepth int
}

// Report emits the audit event for a breaker match. It always
// increments lenny_circuit_breaker_rejections_total; it writes a full
// audit row only for the first rejection per (tenant_id, circuit_name,
// caller_sub) within the 10-second window and increments
// lenny_circuit_breaker_rejections_suppressed_total otherwise. A nil
// appender makes Report a metrics-only no-op for the audit write.
func (a *AuditReporter) Report(ctx context.Context, b circuitbreaker.Breaker, snap RejectionSnapshot) {
	tier := string(b.LimitTier)
	tenantID := snap.CallerTenantID
	if a.metrics != nil {
		a.metrics.RecordCircuitBreakerRejection(tenantID, b.Name, tier)
	}
	if a.appender == nil {
		return
	}
	if a.suppressed(tenantID, b.Name, snap.CallerSub) {
		if a.metrics != nil {
			a.metrics.RecordCircuitBreakerRejectionSuppressed(tenantID, b.Name, tier)
		}
		return
	}
	row := map[string]any{
		"circuit_name":                b.Name,
		"reason":                      b.Reason,
		"opened_at":                   b.OpenedAt.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"),
		"limit_tier":                  tier,
		"replica_service_instance_id": a.replicaID,
		"caller_sub":                  snap.CallerSub,
		"caller_tenant_id":            snap.CallerTenantID,
	}
	if snap.Runtime != "" {
		row["runtime"] = snap.Runtime
	}
	if snap.Pool != "" {
		row["pool"] = snap.Pool
	}
	if snap.SessionID != "" {
		row["session_id"] = snap.SessionID
	}
	if snap.ParentSessionID != "" {
		row["parent_session_id"] = snap.ParentSessionID
		row["delegation_depth"] = snap.DelegationDepth
	}
	payload, _ := json.Marshal(row)
	// The audit write is best-effort on the request hot path: a breaker
	// rejection already returns 503, so a transient audit-backend fault
	// must not change the caller-visible outcome. The hash-chain
	// integrity check (§11.7) surfaces any persistent write failure.
	_, _ = a.appender.Append(ctx, tenantID, EventAdmissionCircuitBreakerRejected, payload, a.clock())
}

// ReportCacheStale emits the §16.7 `admission.circuit_breaker_cache_stale`
// audit event for one admission decision served against a stale breaker
// cache. It always increments lenny_circuit_breaker_cache_stale_serves_total
// (labelled by outcome); it writes a full audit row only for the first
// stale serve per (replica, outcome) within the 10-second sampling
// window so a Redis outage's stale-serve storm cannot flood the audit
// log. A nil appender makes this a metrics-only no-op for the audit
// write. outcome is "rejected" when an open breaker matched the request
// and "admitted" otherwise (the security-salient case). ageSeconds is the
// cache age at decision time.
func (a *AuditReporter) ReportCacheStale(ctx context.Context, outcome string, ageSeconds float64, snap RejectionSnapshot) {
	if a.metrics != nil {
		a.metrics.RecordCircuitBreakerCacheStaleServe(outcome)
	}
	if a.appender == nil {
		return
	}
	// Sample per (replica, outcome): the stale window is a property of this
	// replica's cache, so the dedup key is the replica + outcome rather than
	// the per-(tenant, circuit, caller) key the rejection path uses.
	if a.suppressed("\x00cachestale", a.replicaID, outcome) {
		return
	}
	row := map[string]any{
		"outcome":                     outcome,
		"cache_age_seconds":           ageSeconds,
		"replica_service_instance_id": a.replicaID,
	}
	if snap.CallerSub != "" {
		row["caller_sub"] = snap.CallerSub
	}
	if snap.CallerTenantID != "" {
		row["caller_tenant_id"] = snap.CallerTenantID
	}
	payload, _ := json.Marshal(row)
	// The cache-stale event is written under the caller's tenant when
	// known; a probe with no resolved tenant falls back to the empty
	// tenant the appender routes to the platform tenant. Best-effort on the
	// hot path, like the rejection write.
	_, _ = a.appender.Append(ctx, snap.CallerTenantID, EventAdmissionCircuitBreakerCacheStale, payload, a.clock())
}

// suppressed reports whether a rejection for the (tenant, circuit,
// caller) tuple falls inside an open 10-second window. The first call
// in a window returns false and opens the window; later calls return
// true until the window elapses.
func (a *AuditReporter) suppressed(tenantID, circuitName, callerSub string) bool {
	key := tenantID + "\x00" + circuitName + "\x00" + callerSub
	now := a.clock()
	a.mu.Lock()
	defer a.mu.Unlock()
	if last, ok := a.lastWrite[key]; ok && now.Sub(last) < auditSamplingWindow {
		return true
	}
	a.lastWrite[key] = now
	return false
}
