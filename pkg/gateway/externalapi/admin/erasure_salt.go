// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/erasurejob"
)

// ErasureSaltRotator rotates a tenant's §12.8 billing-pseudonymization
// erasure_salt: it generates a fresh per-tenant 256-bit salt and persists
// it under the §12.8 line 856 advisory lock, deleting the prior salt in
// the same store write. *erasurejob.BillingEraser satisfies it. F-12.8.5.
type ErasureSaltRotator interface {
	RotateErasureSalt(ctx context.Context, tenantID string) error
}

// WithErasureSaltRotation wires
// POST /v1/admin/tenants/{id}/rotate-erasure-salt (§12.8 line 857). A nil
// rotator leaves the route unregistered. F-12.8.5.
func (r *Router) WithErasureSaltRotation(rotator ErasureSaltRotator) *Router {
	r.saltRotator = rotator
	return r
}

// handleRotateErasureSalt implements
// POST /v1/admin/tenants/{id}/rotate-erasure-salt. Per §12.8 line 857 a
// platform admin rotates a compromised salt: the old salt is deleted
// immediately (not archived) and a security audit event is emitted. The
// rotation runs under the §12.8 line 856 `erasure_salt_migration:{tenant_id}`
// advisory lock so it never races a concurrent erasure pseudonymization.
// spec: §12.8 lines 856-857. F-12.8.5.
func (r *Router) handleRotateErasureSalt(w http.ResponseWriter, req *http.Request) {
	id := req.PathValue("id")
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", "admin handler reached without authenticated principal", nil)
		return
	}
	// Reject an unknown tenant or a §12.8 deleted tombstone before rotating.
	t, err := r.tenants.Get(req.Context(), id)
	if errors.Is(err, tenantstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !t.IsActive() {
		writeError(w, http.StatusConflict, "TENANT_DELETED", "tenant is deleted; its erasure_salt cannot be rotated", nil)
		return
	}

	if err := r.saltRotator.RotateErasureSalt(req.Context(), id); err != nil {
		if errors.Is(err, erasurejob.ErrBillingErasureExempt) {
			// §12.8 line 855: an exempt tenant uses no salt, so there is
			// nothing to rotate.
			writeError(w, http.StatusConflict, "BILLING_ERASURE_EXEMPT",
				"tenant is exempt from billing erasure; no erasure_salt to rotate", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// §12.8 line 857: emit the security audit event recording the rotation.
	r.emit(req.Context(), principal, "tenant.erasure_salt_rotated", id, map[string]any{
		"reason": "admin_initiated_rotation",
	})

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"tenantId": id, "rotated": true})
}
