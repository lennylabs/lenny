// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/playground"
)

// playgroundTenantRegistry adapts a tenantstore.Store into the
// playground.TenantRegistry the §27.2 layer-4 Ready-gate consults. It
// reports a tenant as registered when the store returns a row that is
// not soft-deleted; the built-in "default" tenant is always
// registered so a dev-mode playground against the Embedded-Mode
// default tenant resolves without a Postgres row.
type playgroundTenantRegistry struct {
	store tenantstore.Store
}

func (r playgroundTenantRegistry) IsRegistered(tenantID string) (bool, error) {
	if tenantID == "default" {
		return true, nil
	}
	row, err := r.store.Get(context.Background(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return row.IsActive(), nil
}

// playgroundAuditEmitter bridges the playground's §27.3.1 / §10.2
// audit events into the durable §11.7 audit chain. spec: §27.3.1 step 6 — playground.bearer_minted and playground.bearer_revoked
// "share the taxonomy and redaction rules of other auth events in
// §11.7", so they are committed to the principal's per-tenant hash
// chain, not just logged. It keeps the lightweight log line so a mint
// and revoke remain observable in the gateway log stream, and falls
// back to log-only when no durable sink is wired. F-27.3.5.
type playgroundAuditEmitter struct {
	sink admin.AuditSink
}

func (e playgroundAuditEmitter) EmitPlaygroundEvent(ctx context.Context, ev playground.AuditEvent) {
	log.Printf("lenny-gateway: §27 audit %s tenant=%s user=%s jti=%s", ev.Type, ev.TenantID, ev.UserID, ev.BearerJTI)
	if e.sink == nil {
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
	// The event lands on the principal's tenant chain (§11.7 is
	// tenant-scoped); ActorSubject is the playground user.
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:           ev.Type,
		ActorSubject:   ev.UserID,
		ActorTenantID:  ev.TenantID,
		TargetResource: ev.SessionCookieID,
		Detail:         detail,
		At:             ev.At,
	})
}

// EmitMintRejected routes the §10.2
// playground.bearer_mint_rejected event to the durable §11.7 sink and
// logs it alongside the metric increment. A rejection that fires before
// tenant extraction carries an empty tenant; the sink commits it to the
// platform chain. F-27.3.5.
func (e playgroundAuditEmitter) EmitMintRejected(ctx context.Context, ev playground.MintRejectedEvent) {
	log.Printf("lenny-gateway: §10.2 audit playground.bearer_mint_rejected tenant=%s subject_jti=%s subject_typ=%s invariant=%s ingress=%s",
		ev.TenantID, ev.SubjectJTI, ev.SubjectTyp, ev.InvariantViolated, ev.IngressPath)
	if e.sink == nil {
		return
	}
	e.sink.EmitAdminEvent(ctx, admin.AuditEvent{
		Type:          "playground.bearer_mint_rejected",
		ActorTenantID: ev.TenantID,
		Detail: map[string]any{
			"subject_jti":        ev.SubjectJTI,
			"subject_typ":        ev.SubjectTyp,
			"invariant_violated": ev.InvariantViolated,
			"ingress_path":       ev.IngressPath,
		},
		At: ev.At,
	})
}
