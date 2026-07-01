// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// eventSessionForceTerminated is the §24.11 force-terminate audit event
// type. It mirrors observability/audit.EventSessionForceTerminated; the
// admin emit path serializes event-type strings (see r.emit), so the
// constant is duplicated here as a string to avoid importing the catalog
// package into the router. spec: §24.11 line 136 — F-24.11.3.
const eventSessionForceTerminated = "session.force_terminated"

// SessionAdmin backs the §24.11 platform-admin session-investigation
// surface. The operator supplies only the session id (a globally-unique
// UUID), so both methods resolve across tenants; the router gates them
// behind requireAdmin (platform-admin). spec: §24.11 lines 135-136.
type SessionAdmin interface {
	// GetByID returns the session row whose id equals id, across every
	// tenant. Returns sessionstore.ErrNotFound when no row exists.
	GetByID(ctx context.Context, id string) (sessionstore.Session, error)

	// ForceTerminate transitions a stuck or unresponsive session to
	// `failed` and releases its assigned pod to the pool, bypassing the
	// interactive-state guard the normal lifecycle enforces. It returns
	// the resulting row, the state the session held immediately before the
	// force (empty when the session was already terminal), and a bool that
	// is true only when the call actually transitioned the row — a repeat
	// call against an already-terminal session is a no-op returning
	// transitioned=false, so the endpoint is idempotent. Returns
	// sessionstore.ErrNotFound when no row exists. spec: §24.11 line 136.
	ForceTerminate(ctx context.Context, id string) (sess sessionstore.Session, previousState session.State, transitioned bool, err error)
}

// WithSessionAdmin wires the §24.11 session-investigation seam onto the
// Router. A nil seam leaves both `GET /v1/admin/sessions/{id}` and
// `POST /v1/admin/sessions/{id}/force-terminate` unregistered.
func (r *Router) WithSessionAdmin(s SessionAdmin) *Router {
	r.sessionAdmin = s
	return r
}

// adminSessionPayload is the §24.11 line 135 investigation view: session
// state, the failure cause when terminal, scheduling metadata, and the
// assigned pod. It is a read-only projection of the persisted row.
type adminSessionPayload struct {
	ID               string `json:"id"`
	TenantID         string `json:"tenantId"`
	UserID           string `json:"userId"`
	State            string `json:"state"`
	FailureClass     string `json:"failureClass,omitempty"`
	FailureReason    string `json:"failureReason,omitempty"`
	RuntimeRef       string `json:"runtimeRef,omitempty"`
	PoolRef          string `json:"poolRef,omitempty"`
	Environment      string `json:"environment,omitempty"`
	IsolationProfile string `json:"isolationProfile,omitempty"`
	ExecutionMode    string `json:"executionMode,omitempty"`
	AssignedPod      string `json:"assignedPod,omitempty"`
	CreatedAt        string `json:"createdAt,omitempty"`
	UpdatedAt        string `json:"updatedAt,omitempty"`
}

func newAdminSessionPayload(s sessionstore.Session) adminSessionPayload {
	return adminSessionPayload{
		ID:               s.ID,
		TenantID:         s.TenantID,
		UserID:           s.UserID,
		State:            string(s.State),
		FailureClass:     string(s.FailureClass),
		FailureReason:    s.FailureReason,
		RuntimeRef:       s.RuntimeRef,
		PoolRef:          s.PoolRef,
		Environment:      s.Environment,
		IsolationProfile: string(s.IsolationProfile),
		ExecutionMode:    s.ExecutionMode,
		AssignedPod:      s.PodAssignment,
		CreatedAt:        rfc3339Nano(s.CreatedAt),
		UpdatedAt:        rfc3339Nano(s.UpdatedAt),
	}
}

// handleGetSession implements GET /v1/admin/sessions/{id} — the §24.11
// platform-admin read-through used to investigate a stuck, orphaned, or
// unexpectedly terminated session. spec: §24.11 line 135.
func (r *Router) handleGetSession(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimSpace(req.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "session id is required", nil)
		return
	}
	sess, err := r.sessionAdmin.GetByID(req.Context(), id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, newAdminSessionPayload(sess))
}

// handleForceTerminateSession implements POST
// /v1/admin/sessions/{id}/force-terminate — the §24.11 line 136
// operator-driven forced terminal transition. The session transitions
// immediately to `failed` and the assigned pod is released to the pool.
// An optional `reason` in the request body is recorded in the
// session.force_terminated audit event. The endpoint is idempotent: a
// force against an already-terminal session returns 200 with the current
// state and emits no audit event. spec: §24.11 line 136.
func (r *Router) handleForceTerminateSession(w http.ResponseWriter, req *http.Request) {
	id := strings.TrimSpace(req.PathValue("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "session id is required", nil)
		return
	}
	// The reason is optional; an empty or absent body is valid. Only a
	// malformed (non-empty, non-JSON) body is rejected.
	var body struct {
		Reason string `json:"reason"`
	}
	if req.Body != nil {
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
			return
		}
	}

	sess, previousState, transitioned, err := r.sessionAdmin.ForceTerminate(req.Context(), id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if transitioned {
		// spec: §24.11 line 136 / §16.7 — record operator identity, the
		// session, and the pre-force state so an auditor can reconstruct
		// the forced transition. The justification (reason) is optional.
		p, _ := authmw.FromContext(req.Context())
		detail := map[string]any{
			"session_id":     sess.ID,
			"previous_state": string(previousState),
		}
		if strings.TrimSpace(body.Reason) != "" {
			detail["reason"] = strings.TrimSpace(body.Reason)
		}
		r.emit(req.Context(), p, eventSessionForceTerminated, sess.ID, detail)
	}
	writeJSON(w, http.StatusOK, newAdminSessionPayload(sess))
}
