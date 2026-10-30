// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lennylabs/lenny/pkg/auth"
	"github.com/lennylabs/lenny/pkg/gateway/credentials/impersonation"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/externalapi/pagination"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/storage/issuedtokenstore"
)

// ImpersonationService is the §13.3 platform-admin impersonation issuer
// the admin router drives. *impersonation.Service satisfies it. A nil
// service leaves the /v1/admin/impersonation routes unregistered.
//
// spec: §13.3 (Platform-admin impersonation); §16.7.
type ImpersonationService interface {
	Issue(ctx context.Context, req impersonation.IssueRequest) (impersonation.Ticket, string, error)
	End(ctx context.Context, id, endedBy string) (impersonation.EndResult, error)
	ListActive(ctx context.Context, callerSub string) ([]impersonation.Session, error)
}

// WithImpersonation wires the §13.3 impersonation issuer onto the Router.
// A nil interface value leaves the routes unregistered: the posture of a
// gateway without a Postgres pool, a signer, the revocation propagator, or
// the durable audit store. Callers must never pass a typed-nil service,
// which passes the nil check and registers routes that panic on first use.
func (r *Router) WithImpersonation(s ImpersonationService) *Router {
	r.impersonation = s
	return r
}

// StartImpersonationRequest is the POST /v1/admin/impersonation body.
type StartImpersonationRequest struct {
	// TargetTenantID / TargetUserID identify the impersonated user.
	TargetTenantID string `json:"targetTenantId"`
	TargetUserID   string `json:"targetUserId"`
	// Reason is the §16.7 impersonation_reason (required — a cross-tenant
	// admin action must carry a recorded reason).
	Reason string `json:"reason"`
	// TicketRef is the §16.7 ticket_id external justification reference
	// (e.g. a support ticket id). Required.
	TicketRef string `json:"ticketId"`
	// DurationSeconds is the §16.7 impersonation_duration_seconds.
	DurationSeconds int64 `json:"durationSeconds"`
}

// startImpersonationResponse is the POST /v1/admin/impersonation success
// body. The bearer is the minted target-user token; the session id backs
// the DELETE and the audit-pair join.
type startImpersonationResponse struct {
	ImpersonationSessionID string `json:"impersonationSessionId"`
	BearerToken            string `json:"bearerToken"`
	TokenType              string `json:"tokenType"`
	TargetTenantID         string `json:"targetTenantId"`
	TargetUserID           string `json:"targetUserId"`
	ExpiresAt              string `json:"expiresAt"`
}

// handleStartImpersonation implements POST /v1/admin/impersonation, the
// §13.3 platform-admin impersonation flow. It is platform-admin only: a
// tenant-admin cannot impersonate a user (cross-tenant by design). The
// caller's own bearer identity is passed so the service refuses an
// impersonation or revoked caller bearer with 403 FORBIDDEN before any
// audit write. The admin.impersonation_started audit row is written
// before the bearer is minted, and a CMP-058 unresolvable target region
// fails the issuance closed with PLATFORM_AUDIT_REGION_UNRESOLVABLE (no
// session, no bearer).
//
// spec: §13.3 (Platform-admin impersonation, rule 1); §15.1; §16.7; §11.7.
func (r *Router) handleStartImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := r.requireImpersonationAdmin(w, req)
	if !ok {
		return
	}
	var body StartImpersonationRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.TargetTenantID == "" || body.TargetUserID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"targetTenantId and targetUserId are required",
			map[string]any{"fields": []string{"targetTenantId", "targetUserId"}})
		return
	}
	if body.Reason == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"reason is required", map[string]any{"field": "reason"})
		return
	}
	if body.TicketRef == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"ticketId (external justification reference) is required",
			map[string]any{"field": "ticketId"})
		return
	}
	if body.DurationSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"durationSeconds must be positive", map[string]any{"field": "durationSeconds"})
		return
	}

	// Resolve the target tenant and user. A missing target is 404 — the
	// impersonation never reaches the audit write.
	if _, err := r.tenants.Get(req.Context(), body.TargetTenantID); err != nil {
		if errors.Is(err, tenantstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "target tenant not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	var targetRoles []auth.Role
	if r.users != nil {
		u, err := r.users.Get(req.Context(), body.TargetTenantID, body.TargetUserID)
		if err != nil {
			if errors.Is(err, userstore.ErrNotFound) {
				writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "target user not found", nil)
				return
			}
			writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		targetRoles = u.Roles
	}

	ticket, bearer, err := r.impersonation.Issue(req.Context(), impersonation.IssueRequest{
		AdminSub:       principal.Subject,
		TargetTenantID: body.TargetTenantID,
		TargetUserID:   body.TargetUserID,
		Reason:         body.Reason,
		TicketRef:      body.TicketRef,
		Duration:       time.Duration(body.DurationSeconds) * time.Second,
		TargetRoles:    targetRoles,
		CallerTenantID: principal.TenantID,
		CallerJTI:      principal.JTI,
	})
	if errors.Is(err, impersonation.ErrIssuanceRefused) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"the impersonation request is refused for the calling bearer or an invalidated user", nil)
		return
	}
	if err != nil {
		r.writeImpersonationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, startImpersonationResponse{
		ImpersonationSessionID: ticket.ID,
		BearerToken:            bearer,
		TokenType:              "Bearer",
		TargetTenantID:         ticket.TargetTenantID,
		TargetUserID:           ticket.TargetUserID,
		ExpiresAt:              ticket.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

// handleEndImpersonation implements DELETE /v1/admin/impersonation/{id}:
// it ends the session by revoking its bearer from any replica. It is
// platform-admin only, so no other caller reaches the __all__ lookup. The
// not-found and not-active outcomes are checked before the default
// branch, so a joined cross_tenant_read emit error still yields 404 or
// 409. An ended-event failure after the revoke reaches the default branch
// and its error is returned while the bearer stays revoked.
//
// spec: §13.3 (Platform-admin impersonation, rules 3 and 7); §15.1.
func (r *Router) handleEndImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := r.requireImpersonationAdmin(w, req)
	if !ok {
		return
	}
	res, err := r.impersonation.End(req.Context(), req.PathValue("id"), principal.Subject)
	if err != nil {
		writeEndImpersonationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, endImpersonationResponse{
		ImpersonationSessionID: res.SessionID,
		EndedAt:                res.EndedAt.UTC().Format(time.RFC3339),
	})
}

// endImpersonationResponse is the DELETE /v1/admin/impersonation/{id}
// success body.
type endImpersonationResponse struct {
	ImpersonationSessionID string `json:"impersonationSessionId"`
	EndedAt                string `json:"endedAt"`
}

// writeEndImpersonationError maps an End error: 409 INVALID_STATE_TRANSITION
// for a revoked or expired bearer, 404 for an unknown id, and the shared
// impersonation mapping otherwise.
//
// spec: §15.1 (DELETE /v1/admin/impersonation/{id}).
func writeEndImpersonationError(w http.ResponseWriter, r *Router, err error) {
	var notActive *issuedtokenstore.NotActiveError
	switch {
	case errors.Is(err, impersonation.ErrAlreadyEnded) && errors.As(err, &notActive):
		writeError(w, http.StatusConflict, "INVALID_STATE_TRANSITION",
			"the impersonation session's bearer is "+notActive.State,
			map[string]any{"currentState": notActive.State, "allowedStates": []string{"active"}})
	case errors.Is(err, impersonation.ErrNotFound):
		writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "impersonation session not found", nil)
	default:
		r.writeImpersonationError(w, err)
	}
}

// impersonationEntry is one item of the GET /v1/admin/impersonation
// listing: the fields the issued_tokens row holds. The minted bearer is
// never echoed back.
type impersonationEntry struct {
	ImpersonationSessionID string `json:"impersonationSessionId"`
	AdminSub               string `json:"adminSub"`
	TargetTenantID         string `json:"targetTenantId"`
	TargetUserID           string `json:"targetUserId"`
	IssuedAt               string `json:"issuedAt"`
	ExpiresAt              string `json:"expiresAt"`
}

// handleListImpersonation implements GET /v1/admin/impersonation, the
// active-session listing across every tenant. It is platform-admin only,
// and the service emits cross_tenant_read before it reads any row. The
// items go out in the §15.1 cursor-paginated envelope, sortable on
// created_at (the bearer's issuedAt) with the session id as tiebreaker.
//
// spec: §13.3 (Platform-admin impersonation, rules 2 and 7); §15.1.
func (r *Router) handleListImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := r.requireImpersonationAdmin(w, req)
	if !ok {
		return
	}
	sessions, err := r.impersonation.ListActive(req.Context(), principal.Subject)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	entries := make([]impersonationEntry, 0, len(sessions))
	for _, s := range sessions {
		entries = append(entries, impersonationEntry{
			ImpersonationSessionID: s.ID,
			AdminSub:               s.AdminSub,
			TargetTenantID:         s.TargetTenantID,
			TargetUserID:           s.TargetUserID,
			IssuedAt:               s.IssuedAt.UTC().Format(time.RFC3339),
			ExpiresAt:              s.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writePaginatedList(w, req, r.clock(), entries, []string{"created_at"}, adminListDefaultSort,
		func(e impersonationEntry, _ pagination.Sort) (string, string) {
			return e.IssuedAt, e.ImpersonationSessionID
		})
}

// requireImpersonationAdmin resolves the authenticated principal and
// refuses a caller without platform-admin with 403 FORBIDDEN, before any
// cross-tenant read.
//
// spec: §13.3 (Platform-admin impersonation, rule 7).
func (r *Router) requireImpersonationAdmin(w http.ResponseWriter, req *http.Request) (authmw.Principal, bool) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return authmw.Principal{}, false
	}
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"impersonation requires the platform-admin role", nil)
		return authmw.Principal{}, false
	}
	return principal, true
}

// auditRegionUnresolvable is the §11.7 CMP-058 fail-closed error surface
// the auditstore returns when a platform-tenant write referencing a
// non-platform target_tenant_id cannot resolve the target's regional
// platform-Postgres. The admin layer maps it without importing auditstore.
type auditRegionUnresolvable interface {
	Code() string
	HTTPStatus() int
}

// writeImpersonationError maps an issuer error to the canonical admin
// envelope. A bearer the KMS could not sign surfaces as
// KMS_SIGNING_UNAVAILABLE (HTTP 503 with Retry-After, §10.2); a CMP-058
// fail-closed as PLATFORM_AUDIT_REGION_UNRESOLVABLE (HTTP 422); a
// duration error as 400; everything else as 500.
func (r *Router) writeImpersonationError(w http.ResponseWriter, err error) {
	if writeSigningUnavailable(w, err) {
		return
	}
	var unresolvable auditRegionUnresolvable
	switch {
	case errors.As(err, &unresolvable):
		writeError(w, unresolvable.HTTPStatus(), unresolvable.Code(),
			"the impersonation audit record could not be written to the target tenant's regional platform-Postgres", nil)
	case errors.Is(err, impersonation.ErrInvalidDuration):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
			map[string]any{"field": "durationSeconds"})
	case errors.Is(err, impersonation.ErrMissingField):
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
	default:
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
