// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

// AuditLog is the §11.7 audit-chain surface the admin subsystem
// writes to and reads from. The in-memory audit.ChainSet (via the
// chainSetAuditLog adapter) and the Postgres-backed
// pkg/gateway/auditstore both satisfy it, so the gateway swaps the
// audit backend without changing the admin handlers.
type AuditLog interface {
	// Append commits an admin event to the tenant's chain.
	Append(ctx context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error)
	// Rows returns the tenant's audit rows in sequence order.
	Rows(ctx context.Context, tenantID string) ([]audit.Row, error)
	// Verify reports the tenant chain's §11.7 integrity state.
	Verify(ctx context.Context, tenantID string) (audit.VerifyResult, error)
}

// chainSetAuditLog adapts an in-memory audit.ChainSet to AuditLog.
type chainSetAuditLog struct {
	chains *audit.ChainSet
	clock  func() time.Time
}

func (a chainSetAuditLog) Append(_ context.Context, tenantID, eventType string, payload json.RawMessage, at time.Time) (audit.Row, error) {
	if at.IsZero() {
		if a.clock != nil {
			at = a.clock()
		} else {
			at = time.Now().UTC()
		}
	}
	return a.chains.Append(tenantID, eventType, payload, at), nil
}

func (a chainSetAuditLog) Rows(_ context.Context, tenantID string) ([]audit.Row, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return nil, nil
	}
	return c.Rows(), nil
}

func (a chainSetAuditLog) Verify(_ context.Context, tenantID string) (audit.VerifyResult, error) {
	c := a.chains.Chain(tenantID)
	if c == nil {
		return audit.VerifyResult{Integrity: audit.ChainVerified}, nil
	}
	return c.Verify(), nil
}

// ChainAuditSink is an AuditSink that commits every admin mutation
// to a §11.7 per-tenant hash chain. Each event is appended to the
// actor's tenant chain so the chain is independently verifiable per
// tenant.
type ChainAuditSink struct {
	log   AuditLog
	clock func() time.Time
}

// NewChainAuditSink returns a sink backed by an in-memory ChainSet.
// Pass nil for clock to default to time.Now.
func NewChainAuditSink(chains *audit.ChainSet, clock func() time.Time) *ChainAuditSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainAuditSink{log: chainSetAuditLog{chains: chains, clock: clock}, clock: clock}
}

// NewAuditLogSink returns a sink backed by any AuditLog — used to
// wire the Postgres-backed audit chain so the trail survives a
// gateway restart.
func NewAuditLogSink(auditLog AuditLog, clock func() time.Time) *ChainAuditSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainAuditSink{log: auditLog, clock: clock}
}

// EmitAdminEvent implements AuditSink. The event is committed to the
// actor's tenant chain. Platform-admin actions (ActorTenantID
// typically "platform") land on the platform chain; tenant-admin
// actions land on the tenant's chain. An append failure is logged —
// the AuditSink contract is fire-and-forget.
func (s *ChainAuditSink) EmitAdminEvent(ctx context.Context, event AuditEvent) {
	tenant := event.ActorTenantID
	if tenant == "" {
		tenant = "platform"
	}
	// spec: §11.7 lines 347-348 — operation_id and caller_kind are
	// optional correlation fields the OCSF translator projects onto
	// metadata.correlation_uid and actor.user.type. Prefer the values
	// the emitter set explicitly; fall back to the request context so
	// events constructed by direct EmitAdminEvent callers (the
	// credential, delegation, playground, and auth-failure auditors)
	// still carry them when the inbound request supplied them.
	operationID := event.OperationID
	if operationID == "" {
		operationID = corr.From(ctx).OperationID
	}
	callerKind := event.CallerKind
	if callerKind == "" {
		if p, ok := authmw.FromContext(ctx); ok {
			callerKind = p.CallerType
		}
	}
	fields := map[string]any{
		"actor_subject":   event.ActorSubject,
		"actor_tenant_id": event.ActorTenantID,
		"target_resource": event.TargetResource,
		"detail":          event.Detail,
	}
	if operationID != "" {
		fields["operation_id"] = operationID
	}
	if callerKind != "" {
		fields["caller_kind"] = callerKind
	}
	payload, _ := json.Marshal(fields)
	at := event.At
	if at.IsZero() {
		at = s.clock()
	}
	if _, err := s.log.Append(ctx, tenant, event.Type, payload, at); err != nil {
		log.Printf("admin: audit append failed for tenant %q event %q: %v", tenant, event.Type, err)
	}
}
