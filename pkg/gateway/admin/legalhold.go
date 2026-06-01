// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
)

// WithSessions wires the SessionStore onto the Router. It backs the
// §12.8 legal-hold endpoint and the GDPR-erasure legal-hold preflight.
func (r *Router) WithSessions(s sessionstore.Store) *Router {
	r.sessions = s
	return r
}

// LegalHoldRequest is the POST /v1/admin/legal-hold body. It names the
// session to hold or release and the desired hold state.
type LegalHoldRequest struct {
	TenantID  string `json:"tenantId"`
	SessionID string `json:"sessionId"`
	Hold      bool   `json:"hold"`
}

// handleSetLegalHold implements POST /v1/admin/legal-hold — the §12.8
// legal-hold control. Setting a hold suspends the artifact retention
// GC for the session and blocks a GDPR erasure of the session owner;
// clearing it restores normal retention. The operation is
// platform-admin only: a legal hold is a spoliation control and a
// tenant-admin must not set or clear one.
func (r *Router) handleSetLegalHold(w http.ResponseWriter, req *http.Request) {
	var body LegalHoldRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return
	}
	if body.SessionID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "sessionId is required",
			map[string]any{"field": "sessionId"})
		return
	}
	if body.TenantID == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "tenantId is required",
			map[string]any{"field": "tenantId"})
		return
	}
	updated, err := r.sessions.Update(req.Context(), body.TenantID, body.SessionID,
		func(s *sessionstore.Session) error {
			s.LegalHold = body.Hold
			return nil
		})
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	principal, _ := authmw.FromContext(req.Context())
	event := "legal_hold.cleared"
	if body.Hold {
		event = "legal_hold.set"
	}
	r.emit(req.Context(), principal, event, body.SessionID, map[string]any{
		"tenantId":  body.TenantID,
		"sessionId": body.SessionID,
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"sessionId": body.SessionID,
		"tenantId":  body.TenantID,
		"legalHold": updated.LegalHold,
	})
}

// heldSessions returns the ids of the user's sessions that carry a
// §12.8 legal hold. An empty result means the §12.8 erasure
// legal-hold preflight passes and the user may be erased. When no
// SessionStore is wired the preflight cannot run and the result is
// empty.
func (r *Router) heldSessions(ctx context.Context, tenantID, userID string) ([]string, error) {
	if r.sessions == nil {
		return nil, nil
	}
	rows, err := r.sessions.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return nil, err
	}
	var held []string
	for _, s := range rows {
		if s.UserID == userID && s.LegalHold {
			held = append(held, s.ID)
		}
	}
	return held, nil
}
