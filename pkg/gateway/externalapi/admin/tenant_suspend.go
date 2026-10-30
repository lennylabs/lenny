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

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// eventTenantSuspended / eventTenantResumed are the §15.1 tenant
// suspend/resume operator-action audit event types. They mirror
// observability/audit.EventTenantSuspended / EventTenantResumed; the
// admin emit path serializes event-type strings (see r.emit), so the
// constants are duplicated here to avoid importing the catalog package
// into the router. spec: §15.1.
const (
	eventTenantSuspended = "tenant.suspended"
	eventTenantResumed   = "tenant.resumed"
)

// tenantSuspendPayload is the §15.1 suspend response: the resulting
// suspension marker, the operator identity and reason recorded with it,
// the count of sessions this request's drain terminated, and the IDs of
// the sessions the drain did not bring to a terminal state. A non-empty
// FailedSessions means the drain is incomplete; the field encodes as an
// empty array, never null, when the drain succeeded.
type tenantSuspendPayload struct {
	TenantID        string   `json:"tenantId"`
	Suspended       bool     `json:"suspended"`
	Reason          string   `json:"reason,omitempty"`
	SuspendedAt     string   `json:"suspendedAt,omitempty"`
	SuspendedBy     string   `json:"suspendedBy,omitempty"`
	DrainedSessions int      `json:"drainedSessions"`
	FailedSessions  []string `json:"failedSessions"`
}

// tenantResumePayload is the §15.1 resume response.
type tenantResumePayload struct {
	TenantID  string `json:"tenantId"`
	Suspended bool   `json:"suspended"`
}

// errAlreadySuspended is the sentinel the suspend mutate closure returns
// when the locked row already carries the marker. Returning an error
// from the closure makes the store write nothing, so a repeat suspend
// never rewrites the reason, timestamp, or operator of the first one.
var errAlreadySuspended = errors.New("tenant already suspended")

// handleSuspendTenant implements POST /v1/admin/tenants/{id}/suspend,
// the §15.1 operator suspension. The handler commits the suspension
// marker under the tenant row lock, writes one tenant.suspended audit
// row, and then drains the tenant's live sessions through the shared
// lifecycle drain. With the marker committed, the creation fence refuses
// new sessions and message injection is rejected with TENANT_SUSPENDED.
//
// Newness is decided under the row lock rather than from the pre-read,
// so two concurrent first-time suspends write the marker once and audit
// exactly one already_suspended=false row between them. A repeat
// suspend leaves the marker unchanged, writes its own audit row with
// already_suspended=true (§11.8 audits every admin operation, and the
// repeat's drain terminates sessions), and runs the drain again, which
// is the retry path for an incomplete drain. The audit emit and the
// drain run on a context detached from request cancellation, so a
// client disconnect cannot skip the audit row or stop the drain inside
// a session's terminal pipeline.
//
// An incomplete drain answers 200 with the unfinished IDs in
// failedSessions. A drain that cannot list the tenant's sessions
// answers 500 INTERNAL_ERROR with the marker left set. Platform-admin
// only.
//
// spec: §15.1 (REST API), §12.8 (Lifecycle fence and drain), §11.8
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
		writeTenantLookupError(w, err)
		return
	}
	// §12.8 tombstone: a deleted tenant's row is retained with mutable
	// fields nulled. It cannot be suspended.
	if existing.State == tenantstore.TenantStateDeleted {
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"tenant is deleted and cannot be suspended", nil)
		return
	}

	p, _ := authmw.FromContext(req.Context())
	row, already, err := r.commitSuspension(req.Context(), id, reason, p.Subject)
	if err != nil {
		writeTenantLookupError(w, err)
		return
	}

	dctx := context.WithoutCancel(req.Context())
	r.emit(dctx, p, eventTenantSuspended, id, map[string]any{
		"tenant_id":         id,
		"reason":            reason,
		"already_suspended": already,
	})

	res, err := r.drainTenant(dctx, id)
	if err != nil && !errors.Is(err, ErrDrainIncomplete) {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"tenant suspended; listing the tenant's sessions for the drain failed: "+err.Error(),
			map[string]any{"drainedSessions": res.Terminated})
		return
	}
	writeJSON(w, http.StatusOK, newTenantSuspendPayload(row, res))
}

// commitSuspension sets the suspension marker under the tenant row lock.
// It returns the locked row and already=true without writing when the
// marker was already set, and the updated row and already=false when
// this call set it. spec: §12.8 (Lifecycle fence and drain).
func (r *Router) commitSuspension(ctx context.Context, id, reason, subject string) (tenantstore.Tenant, bool, error) {
	var locked tenantstore.Tenant
	updated, err := r.tenants.Update(ctx, id, func(t *tenantstore.Tenant) error {
		if t.Suspended {
			locked = *t
			return errAlreadySuspended
		}
		t.Suspended = true
		t.SuspendedReason = reason
		t.SuspendedAt = r.clock()
		t.SuspendedBy = subject
		return nil
	})
	if errors.Is(err, errAlreadySuspended) {
		return locked, true, nil
	}
	if err != nil {
		return tenantstore.Tenant{}, false, err
	}
	return updated, false, nil
}

// writeTenantLookupError answers a tenant store error: 404 for a missing
// row and 500 for anything else.
func writeTenantLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, tenantstore.ErrNotFound) {
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "tenant not found", nil)
		return
	}
	writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
}

// handleResumeTenant implements POST /v1/admin/tenants/{id}/resume — the
// §15.1 operator resumption. Clearing the suspension marker
// restores normal tenant operation. Sessions terminated by the
// suspension stay terminated; resumption is not a pause. The endpoint is
// idempotent: resuming a tenant that is not suspended returns 200 with
// the current marker and emits no event. Platform-admin only. spec:
// §15.1.
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

// drainTenant runs the shared lifecycle drain over the whole tenant
// when the session seams are wired, publishing the transitioned sessions
// through the router's binding-release fan-out, and is a no-op returning
// a zero result and nil otherwise.
// spec: §15.1; §12.8 (Lifecycle fence and drain); §28.5.7 (Gateway-to-store).
func (r *Router) drainTenant(ctx context.Context, tenantID string) (DrainResult, error) {
	if r.sessions == nil || r.sessionAdmin == nil {
		return DrainResult{}, nil
	}
	return DrainSessions(ctx, r.sessions, r.sessionAdmin, r.userPods, tenantID, "")
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

func newTenantSuspendPayload(t tenantstore.Tenant, res DrainResult) tenantSuspendPayload {
	failed := make([]string, 0, len(res.Failed)+len(res.Remaining))
	failed = append(failed, res.Failed...)
	failed = append(failed, res.Remaining...)
	return tenantSuspendPayload{
		TenantID:        t.ID,
		Suspended:       t.Suspended,
		Reason:          t.SuspendedReason,
		SuspendedAt:     rfc3339Nano(t.SuspendedAt),
		SuspendedBy:     t.SuspendedBy,
		DrainedSessions: res.Terminated,
		FailedSessions:  failed,
	}
}
