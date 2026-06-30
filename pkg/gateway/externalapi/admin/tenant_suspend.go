// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// eventTenantSuspended / eventTenantResumed are the §15.1 tenant
// suspend/resume operator-action audit event types. They mirror
// observability/audit.EventTenantSuspended / EventTenantResumed; the
// admin emit path serializes event-type strings (see r.emit), so the
// constants are duplicated here to avoid importing the catalog package
// into the router. spec: §15.1 line 818.
const (
	eventTenantSuspended = "tenant.suspended"
	eventTenantResumed   = "tenant.resumed"
)

// tenantSuspendPayload is the §15.1 suspend response: the resulting
// suspension marker, the operator identity and reason recorded with it,
// and the count of active sessions drained by the suspension.
type tenantSuspendPayload struct {
	TenantID        string `json:"tenantId"`
	Suspended       bool   `json:"suspended"`
	Reason          string `json:"reason,omitempty"`
	SuspendedAt     string `json:"suspendedAt,omitempty"`
	SuspendedBy     string `json:"suspendedBy,omitempty"`
	DrainedSessions int    `json:"drainedSessions"`
}

// tenantResumePayload is the §15.1 resume response.
type tenantResumePayload struct {
	TenantID  string `json:"tenantId"`
	Suspended bool   `json:"suspended"`
}

// handleSuspendTenant implements POST /v1/admin/tenants/{id}/suspend —
// the §15.1 line 818 operator suspension. Setting the suspension marker
// makes the gateway reject new session creation and message injection
// with TENANT_SUSPENDED; all of the tenant's active sessions are then
// drained. The optional `reason` body and the operator identity are
// recorded on the row and in the tenant.suspended audit event. The
// endpoint is idempotent: suspending an already-suspended tenant returns
// 200 with the current marker and neither re-drains nor re-emits.
// Platform-admin only. spec: §15.1 line 818.
func (r *Router) handleSuspendTenant(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimSpace(req.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenant id is required", nil)
		return
	}
	reason, ok := decodeSuspendReason(w, req)
	if !ok {
		return
	}

	existing, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// §12.8 tombstone: a deleted tenant's row is retained with mutable
	// fields nulled. It cannot be suspended.
	if existing.State == tenantstore.TenantStateDeleted {
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"tenant is deleted and cannot be suspended", nil)
		return
	}
	if existing.IsSuspended() {
		// Idempotent no-op: already suspended.
		writeJSON(w, http.StatusOK, newTenantSuspendPayload(existing, 0))
		return
	}

	p, _ := authmw.FromContext(req.Context())
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		t.Suspended = true
		t.SuspendedReason = reason
		t.SuspendedAt = r.clock()
		t.SuspendedBy = p.Subject
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// spec: §15.1 line 818 — all active sessions in the tenant are
	// drained. The marker set above is the authoritative rejection; the
	// drain is a best-effort consequence performed when the session
	// seams are wired. A drain error does not unwind the suspension.
	drained, _ := r.drainTenantSessions(req.Context(), id)

	detail := map[string]any{"tenant_id": id, "drained_sessions": drained}
	if reason != "" {
		detail["reason"] = reason
	}
	r.emit(req.Context(), p, eventTenantSuspended, id, detail)

	writeJSON(w, http.StatusOK, newTenantSuspendPayload(updated, drained))
}

// handleResumeTenant implements POST /v1/admin/tenants/{id}/resume — the
// §15.1 line 819 operator resumption. Clearing the suspension marker
// restores normal tenant operation. Sessions terminated by the
// suspension stay terminated; resumption is not a pause. The endpoint is
// idempotent: resuming a tenant that is not suspended returns 200 with
// the current marker and emits no event. Platform-admin only. spec:
// §15.1 line 819.
func (r *Router) handleResumeTenant(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimSpace(req.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenant id is required", nil)
		return
	}

	existing, err := r.tenants.Get(req.Context(), id)
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if !existing.IsSuspended() {
		// Idempotent no-op: not suspended.
		writeJSON(w, http.StatusOK, tenantResumePayload{TenantID: id, Suspended: false})
		return
	}

	p, _ := authmw.FromContext(req.Context())
	updated, err := r.tenants.Update(req.Context(), id, func(t *tenantstore.Tenant) error {
		t.Suspended = false
		t.SuspendedReason = ""
		t.SuspendedAt = time.Time{}
		t.SuspendedBy = ""
		return nil
	})
	if err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	r.emit(req.Context(), p, eventTenantResumed, id, map[string]any{"tenant_id": id})
	writeJSON(w, http.StatusOK, tenantResumePayload{TenantID: updated.ID, Suspended: updated.Suspended})
}

// drainTenantSessions force-terminates every non-terminal session in the
// tenant per the §15.1 suspend contract and returns the count actually
// transitioned. It is a no-op returning (0, nil) when the session store
// or the force-terminate seam is not wired. ForceTerminate is idempotent,
// so a session that reaches a terminal state concurrently is skipped.
// spec: §15.1 line 818.
func (r *Router) drainTenantSessions(ctx context.Context, tenantID string) (int, error) {
	if r.sessions == nil || r.sessionAdmin == nil {
		return 0, nil
	}
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return 0, err
	}
	drained := 0
	for _, s := range rows {
		if session.IsTerminal(s.State) {
			continue
		}
		_, _, transitioned, terr := r.sessionAdmin.ForceTerminate(ctx, s.ID)
		if terr != nil {
			if errors.Is(terr, sessionstore.ErrNotFound) {
				continue
			}
			return drained, terr
		}
		if transitioned {
			drained++
		}
	}
	return drained, nil
}

// decodeSuspendReason reads the optional {"reason": "..."} suspend body.
// An empty or absent body is valid; only a non-empty, non-JSON body is
// rejected. It returns the trimmed reason and false when it has already
// written an error response.
func decodeSuspendReason(w http.ResponseWriter, req *http.Request) (string, bool) {
	var body struct {
		Reason string `json:"reason"`
	}
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return "", false
		}
	}
	return strings.TrimSpace(body.Reason), true
}

func newTenantSuspendPayload(t tenantstore.Tenant, drained int) tenantSuspendPayload {
	return tenantSuspendPayload{
		TenantID:        t.ID,
		Suspended:       t.Suspended,
		Reason:          t.SuspendedReason,
		SuspendedAt:     rfc3339Nano(t.SuspendedAt),
		SuspendedBy:     t.SuspendedBy,
		DrainedSessions: drained,
	}
}
