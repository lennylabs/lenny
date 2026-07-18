// SPDX-License-Identifier: MIT

package opsserver

import (
	"net/http"
	"time"

	auditcat "github.com/lennylabs/lenny/pkg/observability/audit"
	"github.com/lennylabs/lenny/pkg/observability/correlation"
	"github.com/lennylabs/lenny/pkg/ops/auditrate"
)

// The §16.7 / §25.6 diagnostic audit event types, pinned to the audit
// catalog so a rename of a catalog constant fails to compile here rather
// than silently emitting a stale type string.
var (
	eventSessionDiagnosed        = auditcat.EventDiagnosticsSessionDiagnosed.String()
	eventPoolDiagnosed           = auditcat.EventDiagnosticsPoolDiagnosed.String()
	eventCredentialPoolDiagnosed = auditcat.EventDiagnosticsCredentialPoolDiagnosed.String()
	eventConnectivityChecked     = auditcat.EventDiagnosticsConnectivityChecked.String()
)

// DiagnosticsAuditConfig enables the §25.9 diagnostics-audit emission
// with per-resource coalescing and a per-service-account rate cap. When
// the §25.6 diagnostic endpoints serve a successful diagnosis, the
// server records the access through the auditrate limiter: repeated
// calls for the same {resourceType, resourceId} within a 60s window
// coalesce into a single audit event with an incremented invocationCount,
// and excess distinct events per service account are dropped with the
// lenny_audit_rate_limited_total counter incremented.
//
// spec: §25.9 lines 3695-3703 (Diagnostics Audit Rate Limiting).
type DiagnosticsAuditConfig struct {
	// Emit is the terminal audit sink each coalesced diagnostic audit
	// event is handed to when its window closes. lenny-ops wires it to
	// opsaudit.Recorder, which durably commits the event to the §11.7
	// hash chain (the same durable-append path the §25.11 backup
	// AuditSink uses). A nil Emit accounts windows without emitting.
	Emit func(auditrate.Event)

	// RatePerMinute is ops.audit.diagnosticsRatePerMinute (default 60):
	// the per-service-account cap on distinct diagnostic audit events.
	RatePerMinute int

	// RateLimited, when non-nil, is invoked for each dropped diagnostic
	// audit event so the §25.9 lenny_audit_rate_limited_total counter can
	// be incremented (labeled by event type and service account).
	RateLimited func(eventType, serviceAccount string)

	// Now overrides the clock for the coalescing and rate windows
	// (tests). A nil value uses time.Now.
	Now func() time.Time
}

// diagNow returns the diagnostics-audit clock instant.
func (s *Server) diagNow() time.Time {
	if s.diagAuditCfg != nil && s.diagAuditCfg.Now != nil {
		return s.diagAuditCfg.Now()
	}
	return time.Now()
}

// recordDiagnosticAudit applies the §25.9 diagnostics-audit rate limiting
// to one successful diagnosis. resourceType/resourceID identify the
// coalescing window; eventType is the §16.7 diagnostic audit event type.
// A dropped event increments the rate-limited counter. The §25.9 line
// 3703 X-Lenny-Operation-ID correlation is stamped onto the emitted
// event so a query with ?operationId= groups the diagnostic events.
func (s *Server) recordDiagnosticAudit(r *http.Request, eventType, resourceType, resourceID string) {
	if s.diagAudit == nil {
		return
	}
	sa := diagnosticServiceAccount(r)
	d := s.diagAudit.Record(auditrate.Call{
		ServiceAccount: sa,
		ResourceType:   resourceType,
		ResourceID:     resourceID,
		EventType:      eventType,
		OperationID:    correlation.From(r.Context()).OperationID,
	}, s.diagNow())
	if d == auditrate.Drop && s.diagAuditCfg != nil && s.diagAuditCfg.RateLimited != nil {
		s.diagAuditCfg.RateLimited(eventType, sa)
	}
}

// diagnosticServiceAccount resolves the §25.9 service account a
// diagnostic call is rate-limited under: the authenticated principal
// subject when present, otherwise the X-Lenny-Agent-Name correlation
// header, otherwise "anonymous".
func diagnosticServiceAccount(r *http.Request) string {
	if p, ok := callerPrincipal(r); ok && p.Subject != "" {
		return p.Subject
	}
	if name := correlation.From(r.Context()).AgentName; name != "" {
		return name
	}
	return "anonymous"
}

// SweepDiagnosticsAudit flushes §25.9 coalescing windows whose 60s have
// elapsed and prunes stale rate state, so a closed window emits even
// during an idle period. cmd/lenny-ops runs it on a ticker.
func (s *Server) SweepDiagnosticsAudit(now time.Time) {
	if s.diagAudit != nil {
		s.diagAudit.Sweep(now)
	}
}

// FlushDiagnosticsAudit drains every open §25.9 coalescing window,
// emitting each window's accumulated event. cmd/lenny-ops calls it on
// shutdown so no in-flight coalesced diagnostic event is lost.
func (s *Server) FlushDiagnosticsAudit() {
	if s.diagAudit != nil {
		s.diagAudit.Flush()
	}
}
