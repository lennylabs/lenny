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
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
)

// ImpersonationService is the §13.3 platform-admin impersonation issuer
// the admin router drives. *impersonation.Service satisfies it. A nil
// service leaves the /v1/admin/impersonation routes unregistered.
//
// spec: §13.3 line 585; §16.7 line 680.
type ImpersonationService interface {
	Issue(ctx context.Context, req impersonation.IssueRequest) (impersonation.Ticket, string, error)
	End(ctx context.Context, id, endedBy string) (impersonation.Ticket, error)
	ListActive(ctx context.Context) ([]impersonation.Ticket, error)
}

// WithImpersonation wires the §13.3 impersonation issuer onto the Router.
// A nil service leaves the routes unregistered (the cold-start posture for
// a gateway without a JWT signer).
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

// handleStartImpersonation implements POST /v1/admin/impersonation — the
// §13.3 platform-admin impersonation flow. It is platform-admin only: a
// tenant-admin cannot impersonate a user (cross-tenant by design). The
// admin.impersonation_started audit row is written before the bearer is
// minted, and a CMP-058 unresolvable target region fails the issuance
// closed with PLATFORM_AUDIT_REGION_UNRESOLVABLE (no session, no bearer).
//
// spec: §13.3 line 585; §16.7 line 680; §11.7 lines 430-433.
func (r *Router) handleStartImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"impersonation requires the platform-admin role", nil)
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
	})
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

// handleEndImpersonation implements DELETE /v1/admin/impersonation/{id} —
// an operator-initiated end. It is platform-admin only and emits
// admin.impersonation_ended (reason=explicit).
func (r *Router) handleEndImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"impersonation requires the platform-admin role", nil)
		return
	}
	id := req.PathValue("id")
	ticket, err := r.impersonation.End(req.Context(), id, principal.Subject)
	if err != nil {
		switch {
		case errors.Is(err, impersonation.ErrNotFound):
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "impersonation session not found", nil)
		case errors.Is(err, impersonation.ErrAlreadyEnded):
			writeError(w, http.StatusConflict, "CONFLICT", "impersonation session already ended", nil)
		default:
			r.writeImpersonationError(w, err)
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"impersonationSessionId": ticket.ID,
		"endedAt":                ticket.EndedAt.UTC().Format(time.RFC3339),
		"endReason":              string(ticket.EndReason),
	})
}

// impersonationEntry is one row of the GET /v1/admin/impersonation active
// listing. The minted bearer is never echoed back.
type impersonationEntry struct {
	ImpersonationSessionID string `json:"impersonationSessionId"`
	AdminSub               string `json:"adminSub"`
	TargetTenantID         string `json:"targetTenantId"`
	TargetUserID           string `json:"targetUserId"`
	Reason                 string `json:"reason"`
	TicketID               string `json:"ticketId"`
	IssuedAt               string `json:"issuedAt"`
	ExpiresAt              string `json:"expiresAt"`
}

// handleListImpersonation implements GET /v1/admin/impersonation — the
// active-session listing. platform-admin only.
func (r *Router) handleListImpersonation(w http.ResponseWriter, req *http.Request) {
	principal, ok := authmw.FromContext(req.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"admin handler reached without authenticated principal", nil)
		return
	}
	if !principal.HasRole(auth.RolePlatformAdmin) {
		writeError(w, http.StatusForbidden, "FORBIDDEN",
			"impersonation requires the platform-admin role", nil)
		return
	}
	tickets, err := r.impersonation.ListActive(req.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	entries := make([]impersonationEntry, 0, len(tickets))
	for _, t := range tickets {
		entries = append(entries, impersonationEntry{
			ImpersonationSessionID: t.ID,
			AdminSub:               t.AdminSub,
			TargetTenantID:         t.TargetTenantID,
			TargetUserID:           t.TargetUserID,
			Reason:                 t.Reason,
			TicketID:               t.TicketRef,
			IssuedAt:               t.IssuedAt.UTC().Format(time.RFC3339),
			ExpiresAt:              t.ExpiresAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": entries})
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
// envelope. A CMP-058 fail-closed surfaces as PLATFORM_AUDIT_REGION_UNRESOLVABLE
// (HTTP 422); a duration error as 400; everything else as 500.
func (r *Router) writeImpersonationError(w http.ResponseWriter, err error) {
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
