// SPDX-License-Identifier: MIT

package playground

import (
	"context"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
)

// ChainAuditEmitter bridges the playground's audit events into the
// durable §11.7 audit chain through an admin.AuditSink. §27.3.1 step 6
// states that playground.bearer_minted and playground.bearer_revoked
// share the taxonomy and redaction rules of the other auth events in
// §11.7, so they are committed to the principal's per-tenant hash chain
// as well as logged. A nil Sink degrades to log-only, so a deployment
// without a durable sink still serves the playground.
//
// The emitter lives beside the handler rather than in the gateway
// binary so the chain placement of each event, which the §12.8 tenant
// deletion lifecycle depends on, is testable against a real audit store
// from the security tier. spec: §27.3.1, §10.2, §11.7, §12.8.
type ChainAuditEmitter struct {
	Sink admin.AuditSink
}

// EmitPlaygroundEvent commits a bearer_minted or bearer_revoked event to
// the principal's tenant chain; ActorSubject is the playground user.
func (e ChainAuditEmitter) EmitPlaygroundEvent(ctx context.Context, ev AuditEvent) {
	slog.InfoContext(ctx, "playground audit event", "type", ev.Type, "tenant", ev.TenantID, "user", ev.UserID, "jti", ev.BearerJTI)
	if e.Sink == nil {
		return
	}
	detail := map[string]any{
		"session_cookie_id": ev.SessionCookieID,
		"bearer_jti":        ev.BearerJTI,
		"origin":            ev.Origin,
	}
	if ev.BearerTTLSeconds > 0 {
		detail["bearer_ttl_seconds"] = ev.BearerTTLSeconds
	}
	for k, v := range ev.Labels {
		detail["label_"+k] = v
	}
	e.Sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:           ev.Type,
		ActorSubject:   ev.UserID,
		ActorTenantID:  ev.TenantID,
		TargetResource: ev.SessionCookieID,
		Detail:         detail,
		At:             ev.At,
	})
}

// EmitMintRejected commits a §10.2 playground.bearer_mint_rejected event.
//
// A tenant_not_found rejection is written to the platform chain with the
// refused tenant as the top-level target_tenant_id, following the §11.7
// cross-tenant pattern the auth middleware's TENANT_NOT_FOUND row uses.
// The refused tenant is missing or in deletion, and after the §12.8
// Phase 4 audit entry its own chain holds only gdpr.* rows, so a refused
// mint must never append to it. A tenant_claim_invalid_format or
// subject_typ_invalid rejection stays on the claimed tenant's chain, and
// a rejection that fires before tenant extraction carries an empty
// tenant, which the sink commits to the platform chain.
func (e ChainAuditEmitter) EmitMintRejected(ctx context.Context, ev MintRejectedEvent) {
	slog.InfoContext(ctx, "playground.bearer_mint_rejected", "tenant", ev.TenantID, "subject_jti", ev.SubjectJTI,
		"subject_typ", ev.SubjectTyp, "invariant", ev.InvariantViolated, "ingress", ev.IngressPath)
	if e.Sink == nil {
		return
	}
	event := admin.AuditEvent{
		Type:          "playground.bearer_mint_rejected",
		ActorTenantID: ev.TenantID,
		Detail: map[string]any{
			"subject_jti":        ev.SubjectJTI,
			"subject_typ":        ev.SubjectTyp,
			"invariant_violated": ev.InvariantViolated,
			"ingress_path":       ev.IngressPath,
		},
		At: ev.At,
	}
	if ev.InvariantViolated == errTenantNotFound {
		event.ActorTenantID = ""
		event.TargetTenantID = ev.TenantID
	}
	e.Sink.EmitAdminEvent(ctx, event)
}
