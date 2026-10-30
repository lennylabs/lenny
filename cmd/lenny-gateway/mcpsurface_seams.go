// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lennylabs/lenny/pkg/clockinject"
	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingfanout"
	"github.com/lennylabs/lenny/pkg/gateway/billing/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/admin"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/mcptools"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
)

// mcpDelegationAuditor adapts the gateway audit sink to the
// mcptools.DelegationAuditor interface, drawing the §11.7 actor fields
// from the request principal on the context. It also tees the §11.2.1
// billing-stream events (delegation.spawned, delegation.isolation_violation)
// into the per-tenant billing ledger so cost-attribution consumers see
// them alongside the audit chain. spec: §11.2.1. F-11.2.1.
type mcpDelegationAuditor struct {
	sink    admin.AuditSink
	billing *billingfanout.Emitter
}

func (a mcpDelegationAuditor) EmitDelegationEvent(ctx context.Context, eventType string, detail map[string]any) {
	tenantID, subject := "", ""
	if p, ok := authmw.FromContext(ctx); ok {
		tenantID, subject = p.TenantID, p.Subject
	}
	if a.sink != nil {
		ev := admin.AuditEvent{Type: eventType, Detail: detail, At: clockinject.Now().UTC()}
		ev.ActorSubject = subject
		ev.ActorTenantID = tenantID
		a.sink.EmitAdminEvent(ctx, ev)
	}
	// spec: §11.2.1 — tee the cost-attribution / compliance subset into the
	// billing stream. The tenant is the delegating caller's (the parent
	// session's) tenant; the user is the parent session owner.
	switch billingstore.EventType(eventType) {
	case billingstore.EventDelegationSpawned:
		if ev, ok := billingfanout.DelegationSpawned(tenantID, subject, detail); ok {
			a.billing.Emit(ctx, ev)
		}
	case billingstore.EventDelegationIsolationViolation:
		if ev, ok := billingfanout.DelegationIsolationViolation(tenantID, subject, detail); ok {
			a.billing.Emit(ctx, ev)
		}
	}
}

// mcpVCSLeaseAuditor writes the §4.9.2 `credential.leased` audit row each
// time lenny/vcs_token mints a VCS token for a pod's git-credential
// helper, binding the lease to the originating session id per the §26.2
// audit-traceability requirement. It appends directly to the §11.7
// per-tenant hash chain (the §4.9.2 event-type catalog is distinct from
// the admin-audit catalog the EmitAdminEvent path validates against).
// The token is never recorded. spec: §26.2; §4.9.2. F-26.2.5.
type mcpVCSLeaseAuditor struct {
	appender policy.AuditAppender
	// billing tees the §11.2.1 credential.leased event into the per-tenant
	// billing stream alongside the §4.9.2 audit row. Nil disables the tee.
	// F-11.2.1.
	billing *billingfanout.Emitter
}

func (a mcpVCSLeaseAuditor) RecordVCSLease(ctx context.Context, lease mcptools.VCSLeaseRecord) {
	// spec: §11.2.1 — the credential lease is a billing-stream
	// cost-attribution event bound to the leasing session. A VCS token mint
	// is not pool-backed, so credential_pool_id is empty; the provider is
	// the credential attribution and the access mode is the delivery mode.
	a.billing.Emit(ctx, billingfanout.CredentialLeased(
		lease.TenantID, lease.SessionID, "", lease.Provider, lease.Mode,
	))
	if a.appender == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"session_id": lease.SessionID,
		"provider":   lease.Provider,
		"host":       lease.Host,
		"mode":       lease.Mode,
		"scope":      fmt.Sprintf("vcs.%s.%s", lease.Provider, lease.Mode),
	})
	if err != nil {
		return
	}
	_, _ = a.appender.Append(ctx, lease.TenantID, string(credential.AuditCredentialLeased),
		json.RawMessage(payload), clockinject.Now().UTC())
}
