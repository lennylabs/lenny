// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/usagestore"
)

// handleUsage implements GET /v1/usage per §15.1 — the aggregated
// usage report. The §15.1 contract: this is a single aggregated
// object, not a paginated list.
//
// A platform-admin caller may scope the report to one tenant via
// ?tenantId=; every other caller is scoped to their own tenant.
func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	if s.usage == nil {
		// Metering disabled — return an empty report rather than an
		// error so dashboards do not break.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(usagestore.Report{
			ByTenant:  []usagestore.TenantUsage{},
			ByRuntime: []usagestore.RuntimeUsage{},
		})
		return
	}
	// The caller's resolved tenant scopes the report; a non-empty
	// ?tenantId= is honoured only when it matches the caller's
	// tenant (cross-tenant usage reads require platform-admin, which
	// the minimal gateway does not yet distinguish — scope to the
	// caller's tenant unconditionally).
	tenant := s.resolveTenant(r)
	report, err := s.usage.Aggregate(r.Context(), tenant)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

// recordSessionCreated records the §15.1 usage event and the §11.2.1
// `session.created` billing event for a new session. Both writes are
// best-effort: a metering or billing failure is never allowed to fail
// the session create.
func (s *Server) recordSessionCreated(ctx context.Context, sess sessionstore.Session) {
	if s.usage != nil {
		_ = s.usage.Record(ctx, usagestore.Record{
			TenantID: sess.TenantID,
			Runtime:  sess.RuntimeRef,
			Sessions: 1,
		})
	}
	if s.billing != nil {
		_, _ = s.billing.Append(ctx, billingstore.Event{
			TenantID:  sess.TenantID,
			UserID:    sess.UserID,
			SessionID: sess.ID,
			EventType: billingstore.EventSessionCreated,
		})
	}
}

// recordSessionCompleted emits the §11.2.1 `session.completed` billing
// event for a session that has reached a terminal state. Best-effort:
// a billing failure never fails the transition that triggered it.
func (s *Server) recordSessionCompleted(ctx context.Context, sess sessionstore.Session) {
	if s.billing == nil {
		return
	}
	_, _ = s.billing.Append(ctx, billingstore.Event{
		TenantID:  sess.TenantID,
		UserID:    sess.UserID,
		SessionID: sess.ID,
		EventType: billingstore.EventSessionCompleted,
	})
}
