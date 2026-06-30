// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/lennylabs/lenny/pkg/auth"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// ForceDeleteTenantRequest is the POST /v1/admin/tenants/{id}/force-delete
// body. acknowledgeHoldOverride proceeds despite active legal holds (the
// §12.8 Phase 3.5 escrow segregation authorizes, never skips, the hold
// handling); justification is the required free-text override reason.
//
// spec: §12.8 lines 880-889; §24.10 row 4.
type ForceDeleteTenantRequest struct {
	AcknowledgeHoldOverride bool   `json:"acknowledgeHoldOverride,omitempty"`
	Justification           string `json:"justification,omitempty"`
}

// handleForceDeleteTenant implements POST /v1/admin/tenants/{id}/force-delete
// (§24.10 row 4): the platform-admin force-delete that, with
// acknowledgeHoldOverride, segregates held evidence into the region-scoped
// legal-hold escrow and proceeds with the §12.8 deletion lifecycle.
//
// The endpoint is platform-admin only — a legal-hold override is a
// spoliation-adjacent control and a tenant-admin must not self-override.
// Without acknowledgeHoldOverride, the request is rejected with
// TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD when active holds exist (it does not
// silently assume the override); with it, the justification must be
// non-empty. The override intent is stamped durably on the tenant row so
// the §12.8 controller (which reconstructs its deletion job from the
// persisted state after a restart) escrows held evidence at Phase 3.5
// rather than re-blocking.
//
// spec: §12.8 lines 880-889; §24.10 row 4. F-12.8.2, F-24.10.2.
func (r *Router) handleForceDeleteTenant(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	// §12.8 line 880: tenant-admins cannot self-override; force-delete is
	// platform-admin only.
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"tenant force-delete requires the platform-admin role", nil)
		return
	}
	var body ForceDeleteTenantRequest
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}

	row, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tenantStateOrActive(row.State) == tenantstore.TenantStateDeleted || !row.DeletedAt.IsZero() {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
		return
	}
	// spec: §15.1 line 1213 — honour If-Match when present.
	if !enforceIfMatchIfPresent(w, req, row.Version) {
		return
	}

	// §12.8 line 880: enumerate active tenant-scoped legal holds. The
	// override authorizes the Phase 3.5 escrow; absent the override, an
	// active hold blocks fail-closed.
	holds, err := r.activeTenantHolds(req.Context(), id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"legal-hold preflight failed: "+err.Error(), nil)
		return
	}
	holdTuples := make([]map[string]any, 0, len(holds))
	for _, h := range holds {
		holdTuples = append(holdTuples, map[string]any{"resourceType": h.ResourceType, "resourceId": h.ResourceID})
	}

	if !body.AcknowledgeHoldOverride {
		// §12.8 line 889: force-delete without acknowledgeHoldOverride while
		// active holds exist is rejected — the endpoint never silently
		// assumes the override.
		if len(holds) > 0 {
			r.emit(req.Context(), principal, "admin.tenant.deletion_blocked", id, map[string]any{
				"tenantId":  id,
				"holdCount": len(holds),
				"holds":     holdTuples,
			})
			writeError(w, http.StatusConflict, "TENANT_DELETE_BLOCKED_BY_LEGAL_HOLD",
				"the tenant has one or more sessions or artifacts under a legal hold; force-delete requires acknowledgeHoldOverride with a justification",
				map[string]any{"holds": holdTuples})
			return
		}
		// No holds: force-delete on an unheld tenant is an ordinary
		// deletion, identical to DELETE.
		updated, err := r.transitionToDisabling(req.Context(), id, nil)
		if err != nil {
			r.writeTenantUpdateError(w, err)
			return
		}
		r.emit(req.Context(), principal, "admin.tenant.deletion_initiated", id,
			map[string]any{"state": tenantStateOrActive(updated.State)})
		writeJSON(w, http.StatusAccepted, map[string]any{
			"id":    id,
			"state": tenantStateOrActive(updated.State),
		})
		return
	}

	// Override path. §12.8: justification is required and non-empty.
	if body.Justification == "" {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST",
			"acknowledgeHoldOverride requires a non-empty justification", nil)
		return
	}
	overrideAt := r.clock()
	updated, err := r.transitionToDisabling(req.Context(), id, func(t *tenantstore.Tenant) {
		// Stamp the durable override so the §12.8 controller segregates held
		// evidence at Phase 3.5 even after a gateway restart that rebuilds
		// the job from the tenant row.
		t.ForceDeleteHoldOverride = true
		t.ForceDeleteJustification = body.Justification
		t.ForceDeleteBy = principal.Subject
		t.ForceDeleteAt = overrideAt
	})
	if err != nil {
		r.writeTenantUpdateError(w, err)
		return
	}
	// admin.tenant.force_delete_initiated records the authorization; the
	// controller emits the gdpr.legal_hold_overridden_tenant critical event
	// once Phase 3.5's escrow sub-steps complete (it carries the escrow
	// object keys the handler cannot yet know).
	r.emit(req.Context(), principal, "admin.tenant.force_delete_initiated", id, map[string]any{
		"tenantId":                id,
		"state":                   tenantStateOrActive(updated.State),
		"acknowledgeHoldOverride": true,
		"justification":           body.Justification,
		"holdCount":               len(holds),
		"holds":                   holdTuples,
	})
	writeJSON(w, http.StatusAccepted, map[string]any{
		"id":                      id,
		"state":                   tenantStateOrActive(updated.State),
		"acknowledgeHoldOverride": true,
		"heldResources":           holdTuples,
	})
}

// transitionToDisabling moves an active tenant into the §12.8 deletion
// lifecycle (active → disabling), applying the optional stamp mutation
// (e.g. the force-delete override fields) in the same write. A tenant
// already mid-lifecycle keeps its phase so a repeated request does not
// rewind it; the stamp still applies.
func (r *Router) transitionToDisabling(ctx context.Context, id string, stamp func(*tenantstore.Tenant)) (tenantstore.Tenant, error) {
	return r.tenants.Update(ctx, id, func(t *tenantstore.Tenant) error {
		if stamp != nil {
			stamp(t)
		}
		if t.State == "" || t.State == tenantstore.TenantStateActive {
			t.State = tenantstore.TenantStateDisabling
		}
		t.UpdatedAt = r.clock()
		return nil
	})
}

// writeTenantUpdateError maps a tenant Update failure to the canonical
// admin error envelope.
func (r *Router) writeTenantUpdateError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenantstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// activeTenantHolds enumerates the §12.8 line 878 active tenant-scoped
// legal holds: session-level holds (sessions.legal_hold) and
// artifact-level holds on any artifact under one of the tenant's
// sessions. It mirrors the controller-side enumerator so the force-delete
// preflight and the Phase 3.5 enumeration agree on what is held. When no
// SessionStore is wired the preflight cannot run and the result is empty.
func (r *Router) activeTenantHolds(ctx context.Context, tenantID string) ([]heldResource, error) {
	if r.sessions == nil {
		return nil, nil
	}
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var holds []heldResource
	for _, s := range rows {
		if s.LegalHold {
			holds = append(holds, heldResource{ResourceType: "session", ResourceID: s.ID})
		}
		if r.artifactHolds != nil {
			held, herr := r.artifactHolds.IsLegalHeldAt(ctx, tenantID, s.ID)
			if herr != nil {
				return nil, herr
			}
			if held {
				holds = append(holds, heldResource{ResourceType: "artifact", ResourceID: s.ID})
			}
		}
	}
	return holds, nil
}
