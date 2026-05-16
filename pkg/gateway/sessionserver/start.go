// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// CreateAndStartRequest is the §15.1 POST /v1/sessions/start body —
// the convenience surface that bundles create + finalize + start.
type CreateAndStartRequest struct {
	// Inherits the same shape as CreateSessionRequest.
	RuntimeRef       string             `json:"runtimeRef"`
	UserID           string             `json:"userId,omitempty"`
	WorkspacePlan    json.RawMessage    `json:"workspacePlan,omitempty"`
	IsolationProfile isolation.Profile  `json:"isolationProfile,omitempty"`
}

// CreateAndStartResponse is the convenience reply. Mirrors the
// CreateSessionResponse plus an explicit running-state confirmation.
type CreateAndStartResponse = CreateSessionResponse

// handleCreateAndStart implements POST /v1/sessions/start per §15.1:
// the gateway runs the create → finalize → start chain in one call.
// The response is the regular CreateSessionResponse with State =
// "running" so callers receive the uploadToken + sessionIsolationLevel
// in the same envelope they would from POST /v1/sessions.
//
// Workspace plan validation, isolation profile resolution, upload
// token minting, and the role check all run as in handleCreate; the
// extra work here is just to advance the row through the §15.1
// precondition table to running before returning.
func (s *Server) handleCreateAndStart(w http.ResponseWriter, r *http.Request) {
	if !s.requireActiveUser(w, r) {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.requireSessionQuota(w, r, tenantID) {
		return
	}

	var req CreateAndStartRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "request body is not valid JSON", nil)
		return
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required",
			map[string]any{"field": "runtimeRef"})
		return
	}

	isoProf := req.IsolationProfile
	if isoProf == "" {
		isoProf = s.defaultIsoProf
	}
	if !isolation.IsValid(isoProf) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile",
			map[string]any{"fields": []map[string]string{{"field": "isolationProfile"}}})
		return
	}

	var planWarnings []workspaceplan.Warning
	var parsedPlan workspaceplan.Plan
	if len(req.WorkspacePlan) > 0 && !isJSONNull(req.WorkspacePlan) {
		plan, warnings, err := workspaceplan.Parse(req.WorkspacePlan)
		if err != nil {
			s.writeWorkspacePlanError(w, err)
			return
		}
		parsedPlan = plan
		planWarnings = warnings
	}

	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       req.RuntimeRef,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: isoProf,
		CreatedAt:        s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	if err := s.store.Create(r.Context(), row); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.recordSessionCreated(r.Context(), row)

	// §7.1 step 8: mint the uploadToken — useful even for the
	// /sessions/start path because clients may follow up with
	// mid-session uploads when the runtime supports them.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, 0)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"upload token issuance failed", nil)
		return
	}
	if _, err := s.store.Update(r.Context(), tenantID, row.ID, func(row *sessionstore.Session) error {
		row.UploadTokenDigest = parsed.Digest
		row.UploadTokenExpiry = parsed.Expiry
		return nil
	}); err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	// When the gateway is wired with a pod binder, the §15.1 start path
	// places the session on a Kubernetes warm pod before reporting it
	// running. A claim failure marks the row failed and surfaces a
	// retryable 503 rather than leaving a session stuck in running with
	// no pod behind it.
	if s.podBinder != nil {
		if err := s.startOnPod(r.Context(), row, parsedPlan); err != nil {
			s.failSession(r.Context(), tenantID, row.ID)
			s.writeError(w, http.StatusServiceUnavailable, "POD_CLAIM_FAILED",
				"could not place the session on a warm pod: "+err.Error(), nil)
			return
		}
	}

	resp := CreateSessionResponse{
		SessionResponse:       toResponse(row),
		UploadToken:           tok,
		SessionIsolationLevel: defaultIsolationLevel(isoProf),
		WorkspacePlanWarnings: planWarnings,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}

// startOnPod places a started session on a Kubernetes warm pod. It
// resolves the warm pool serving the session's runtime and §5.3
// isolation profile, claims an idle pod from it, starts the session on
// the pod's §4.7 adapter, and records the binding so the message and
// teardown paths can reach the pod.
func (s *Server) startOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) error {
	pool, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return err
	}
	result, err := s.podBinder.Bind(ctx, podsession.BindRequest{
		Pool:      pool,
		SessionID: row.ID,
		TenantID:  row.TenantID,
		Runtime:   row.RuntimeRef,
		Plan:      podsession.WorkspacePlanToProto(plan),
	})
	if err != nil {
		return err
	}
	s.podRegistry.Put(result)
	return nil
}

// failSession marks a session row failed after a start-path error. The
// update is best-effort: the start handler has already chosen the HTTP
// error it returns to the client, so a store failure here cannot change
// the reply.
func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	_, _ = s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateFailed
		return nil
	})
}
