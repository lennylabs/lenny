// SPDX-License-Identifier: MIT

// Package adminaudit holds the admin audit event and the sink that
// commits each event to the actor's tenant audit chain. The API front's
// admin handlers emit these events, and so do the other gateway parts and
// the composition root's auditors for the platform events they record,
// so the sink is a domain value any part may hold.
//
// spec: §11.7 (Audit Logging)
package adminaudit

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
	"github.com/lennylabs/lenny/pkg/auth/authmw"
	corr "github.com/lennylabs/lenny/pkg/observability/correlation"
)

// Sink receives §11.7 admin audit events. The admin router emits one
// event per successful mutation (create / update / soft-delete);
// reads do not emit. Implementations must be non-blocking — the
// admin handler does not wait for delivery.
type Sink interface {
	EmitAdminEvent(ctx context.Context, event Event)
}

// Event is the §11.7 admin-event payload. Fields match the
// canonical OCSF mapping used by the §11.7 hash-chain audit
// pipeline.
type Event struct {
	// Type is the §11.7 event type (e.g., `admin.tenant.created`).
	Type string

	// ActorSubject is the JWT `sub` of the calling user.
	ActorSubject string

	// ActorTenantID is the JWT `tenant_id` of the calling user
	// (usually `platform` for platform-admin calls).
	ActorTenantID string

	// TargetResource is the resource the operation affects (e.g.,
	// the tenant id).
	TargetResource string

	// OperationID is the §11.7 optional correlation
	// token, sourced from the X-Lenny-Operation-ID request header (via
	// the correlation context). The OCSF translator projects it onto
	// metadata.correlation_uid. Empty when the request carried no
	// header. spec: §11.7.
	OperationID string

	// CallerKind is the §11.7 optional caller class,
	// sourced from the OIDC caller_type claim ("human" / "service" /
	// "agent"). The OCSF translator projects it onto actor.user.type /
	// type_id, which the §25.9 human-vs-agent reporting reads. Empty
	// when the token carried no claim. spec: §11.7.
	CallerKind string

	// AgentName is the §15.1 human-readable agent instance
	// identifier, sourced from the X-Lenny-Agent-Name request header
	// (via the correlation context). §15.1 requires it be propagated to
	// audit records so the §25.9 audit-query API can attribute a
	// remediation to the operator agent that issued it. Empty when the
	// request carried no header. spec: §15.1. F-15.1.10.
	AgentName string

	// TargetTenantID names the tenant an event concerns when the event
	// is written to a different tenant's chain, such as a platform-chain
	// row about a refused tenant. ChainSink writes it as the
	// payload's top-level `target_tenant_id`, which the §11.7
	// cross-tenant residency gate reads; a value under Detail is never
	// read by that gate. Empty for events written to the target
	// tenant's own chain. spec: §11.7.
	TargetTenantID string

	// Detail carries event-specific fields the auditor records
	// verbatim in the hash-chain entry.
	Detail map[string]any

	// At is the gateway clock instant the audit event fired.
	At time.Time
}

// ChainSink is a Sink that commits every admin mutation
// to a §11.7 per-tenant hash chain. Each event is appended to the
// actor's tenant chain so the chain is independently verifiable per
// tenant.
type ChainSink struct {
	log   audit.Appender
	clock func() time.Time
}

// NewChainSink returns a sink backed by an in-memory ChainSet.
// Pass nil for clock to default to time.Now.
func NewChainSink(chains *audit.ChainSet, clock func() time.Time) *ChainSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainSink{log: audit.NewChainSetAppender(chains, clock), clock: clock}
}

// NewLogSink returns a sink backed by any audit appender, used to wire
// the Postgres-backed audit chain so the trail survives a gateway
// restart.
func NewLogSink(appender audit.Appender, clock func() time.Time) *ChainSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainSink{log: appender, clock: clock}
}

// EmitAdminEvent implements Sink. The event is committed to the
// actor's tenant chain. Platform-admin actions (ActorTenantID
// typically "platform") land on the platform chain; tenant-admin
// actions land on the tenant's chain. An append failure is logged —
// the Sink contract is fire-and-forget.
func (s *ChainSink) EmitAdminEvent(ctx context.Context, event Event) {
	tenant := event.ActorTenantID
	if tenant == "" {
		tenant = "platform"
	}
	// spec: §11.7 — operation_id and caller_kind are
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
	// spec: §15.1 — X-Lenny-Agent-Name is propagated to audit
	// records. Prefer the value the emitter set; fall back to the
	// request context so direct EmitAdminEvent callers still carry it.
	// F-15.1.10.
	agentName := event.AgentName
	if agentName == "" {
		agentName = corr.From(ctx).AgentName
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
	if agentName != "" {
		fields["agent_name"] = agentName
	}
	if callerKind != "" {
		fields["caller_kind"] = callerKind
	}
	// spec: §11.7 — a row about another tenant carries that tenant as
	// the top-level target_tenant_id, the only key the cross-tenant
	// residency gate reads.
	if event.TargetTenantID != "" {
		fields["target_tenant_id"] = event.TargetTenantID
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
