// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lennylabs/lenny/pkg/audit"
)

// ChainAuditSink is an AuditSink that commits every admin mutation
// to a §11.7 per-tenant hash chain. It is the production-grade
// substrate for the admin audit trail: each event is appended to
// the actor's tenant chain so the chain is independently verifiable
// per tenant.
//
// The §11.7 audit pipeline (OCSF translation, SIEM streaming, the
// retranscribe worker) consumes the same ChainSet; this sink is the
// write head.
type ChainAuditSink struct {
	chains *audit.ChainSet
	clock  func() time.Time
}

// NewChainAuditSink returns a sink backed by the supplied ChainSet.
// Pass nil for clock to default to time.Now.
func NewChainAuditSink(chains *audit.ChainSet, clock func() time.Time) *ChainAuditSink {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &ChainAuditSink{chains: chains, clock: clock}
}

// EmitAdminEvent implements AuditSink. The event is committed to the
// actor's tenant chain. Platform-admin actions (ActorTenantID
// typically "platform") land on the platform chain; tenant-admin
// actions land on the tenant's chain.
func (s *ChainAuditSink) EmitAdminEvent(_ context.Context, event AuditEvent) {
	tenant := event.ActorTenantID
	if tenant == "" {
		tenant = "platform"
	}
	payload, _ := json.Marshal(map[string]any{
		"actor_subject":   event.ActorSubject,
		"actor_tenant_id": event.ActorTenantID,
		"target_resource": event.TargetResource,
		"detail":          event.Detail,
	})
	at := event.At
	if at.IsZero() {
		at = s.clock()
	}
	s.chains.Append(tenant, event.Type, payload, at)
}

// ChainSet exposes the underlying ChainSet so the §11.7 verifier and
// the audit-query API can read the committed rows.
func (s *ChainAuditSink) ChainSet() *audit.ChainSet { return s.chains }
