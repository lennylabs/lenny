// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// EventTypeInterceptorRejected is the §11.7-path audit event type
// written when a §4.8 interceptor chain REJECTs a request. §11.7 line
// 331 lists it in the per-tenant audit-write traffic; §4.8 / §16.7
// mention it only to contrast it with `admission.circuit_breaker_rejected`
// (the pre-chain circuit-breaker gate, which is not an interceptor). It
// is a core §11.7 event rather than a §16.7 / §25 addition, so the
// pkg/observability/audit catalog recognizes it via auxKnownEventTypes
// (IsKnownEventType) without listing it in Catalog(). spec: §11.7 line
// 331. F-11.7.18.
const EventTypeInterceptorRejected = "interceptor.rejected"

// AuditAppender is the §11.7 per-tenant audit hash-chain surface the
// policy audit sink writes to. Both the Postgres-backed
// pkg/gateway/auditstore.Store and an adapter over the in-memory
// audit.ChainSet satisfy it, so the gateway swaps the audit backend
// without changing the sink.
type AuditAppender interface {
	// Append commits an audit event to the tenant's chain and returns
	// the sealed row.
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
}

// ChainSetAppender adapts an in-memory audit.ChainSet to
// AuditAppender. It backs the minimal gateway (no Postgres); the
// chain is lost on a process restart, which §11.7 permits only for
// the non-durable in-memory mode.
type ChainSetAppender struct {
	chains *audit.ChainSet
	clock  func() time.Time
}

// NewChainSetAppender returns an AuditAppender backed by chains. clock
// overrides time.Now; pass nil in production.
func NewChainSetAppender(chains *audit.ChainSet, clock func() time.Time) *ChainSetAppender {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainSetAppender{chains: chains, clock: clock}
}

// Append implements AuditAppender.
func (a *ChainSetAppender) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		at = a.clock()
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

// Ensure ChainSetAppender satisfies AuditAppender at compile time.
var _ AuditAppender = (*ChainSetAppender)(nil)

// AuditSink emits the §16.7 `interceptor.rejected` audit row to a
// tenant's §11.7 hash chain when a §4.8 interceptor chain REJECTs a
// request. The append is gateway-originated, so per §11.7 it is
// synchronous: RecordRejection blocks until the row is committed (or
// the append fails), and the caller holds the response until then.
type AuditSink struct {
	appender AuditAppender
	clock    func() time.Time
}

// NewAuditSink returns an AuditSink backed by appender. clock overrides
// time.Now; pass nil in production.
func NewAuditSink(appender AuditAppender, clock func() time.Time) *AuditSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &AuditSink{appender: appender, clock: clock}
}

// RejectionContext carries the request identity the audit payload
// records alongside the chain's REJECT decision.
type RejectionContext struct {
	// TenantID scopes the audit row to a tenant's chain.
	TenantID string
	// CallerSub is the authenticated caller's OIDC subject.
	CallerSub string
	// SessionID is the session the rejected request targeted, when
	// the request carried one.
	SessionID string
	// Phase is the §4.8 interception phase whose chain REJECTed.
	Phase interceptor.Phase
}

// RecordRejection commits an `interceptor.rejected` audit row for a
// chain REJECT. The payload records the rejecting interceptor's
// identity (the chain's Result.Reason and Code), the §4.8 phase, the
// authenticated caller, and the targeted session. The append is
// synchronous per §11.7; RecordRejection returns the append error so
// the caller can fail closed when the durable audit write fails.
func (s *AuditSink) RecordRejection(ctx context.Context, rc RejectionContext, res interceptor.Result) error {
	// spec: §4.8 line 981 — the audit row records the interceptor that
	// actually rejected. The chain stamps Result.RejectedBy with the
	// rejecting interceptor's Name(); only when the chain leaves it
	// empty (no interceptor identified) does the built-in QuotaEvaluator
	// name stand in.
	interceptorName := res.RejectedBy
	if interceptorName == "" {
		interceptorName = QuotaEvaluatorName
	}
	row := map[string]any{
		"phase":            string(rc.Phase),
		"interceptor_name": interceptorName,
		"interceptor_ref":  interceptorName,
		"reason":           res.Reason,
		"error_code":       res.Code,
		"caller_sub":       rc.CallerSub,
		"caller_tenant_id": rc.TenantID,
		"session_id":       rc.SessionID,
	}
	// spec: §4.8 line 1032, §15.1 INTERCEPTOR_TIMEOUT — a fail-closed
	// timeout/error carries the elapsed deadline so operators can
	// distinguish a degraded interceptor from a deliberate REJECT.
	if res.Code == interceptor.CodeInterceptorTimeout {
		row["timeout_ms"] = res.TimeoutMs
	}
	payload, _ := json.Marshal(row)
	_, err := s.appender.Append(ctx, rc.TenantID, EventTypeInterceptorRejected, payload, s.clock())
	return err
}
