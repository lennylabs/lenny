// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
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
	var planJSON json.RawMessage
	if len(req.WorkspacePlan) > 0 && !isJSONNull(req.WorkspacePlan) {
		plan, warnings, err := workspaceplan.Parse(req.WorkspacePlan)
		if err != nil {
			s.writeWorkspacePlanError(w, err)
			return
		}
		parsedPlan = plan
		planJSON = req.WorkspacePlan
		planWarnings = warnings
	}

	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       req.RuntimeRef,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: isoProf,
		WorkspacePlan:    planJSON,
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

// handleStart implements POST /v1/sessions/{id}/start per §15.1: the
// explicit start transition of the two-step create → finalize → start
// lifecycle. It transitions a ready session to running and, when the
// gateway is wired with a pod binder, places the session on a §5 warm
// pod using the §14 WorkspacePlan stored at create.
//
// handleStart is a dedicated handler rather than a generic
// handleTransition because the start transition carries the extra
// pod-placement step — the same reason handleFinalize is dedicated for
// the finalize transition. The pod claim runs before the row
// transitions: a claim failure leaves the row ready so the client can
// retry POST /start.
func (s *Server) handleStart(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointStart,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	if s.podBinder != nil {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"stored workspace plan could not be parsed: "+err.Error(), nil)
			return
		}
		if err := s.startOnPod(r.Context(), row, plan); err != nil {
			s.writeError(w, http.StatusServiceUnavailable, "POD_CLAIM_FAILED",
				"could not place the session on a warm pod: "+err.Error(), nil)
			return
		}
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionStart(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// storedWorkspacePlan re-parses the §14 WorkspacePlan recorded on the
// session row at create. It returns the zero Plan when the session was
// created without a plan. The plan was validated at create, so a parse
// failure here indicates gateway-version skew against the stored plan.
func storedWorkspacePlan(row sessionstore.Session) (workspaceplan.Plan, error) {
	if len(row.WorkspacePlan) == 0 || isJSONNull(row.WorkspacePlan) {
		return workspaceplan.Plan{}, nil
	}
	plan, _, err := workspaceplan.Parse(row.WorkspacePlan)
	return plan, err
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

// handleResume implements POST /v1/sessions/{id}/resume per §15.1 and
// §7.1. The endpoint is valid only from `awaiting_client_action` — the
// state a session reaches after automatic resume retries are exhausted
// or the resume window elapses (§7.2). A session in that state has no
// live pod, so the handler restores the session onto a fresh §5 warm
// pod from its latest §7.1 WorkspaceSnapshot before the row
// transitions to running. The API-reported transition is
// `resume_pending` → `running`; the `resume_pending` and `resuming`
// states between are internal transients.
//
// handleResume is a dedicated handler rather than a generic
// handleTransition because the resume carries the extra pod-claim and
// workspace-restore step.
func (s *Server) handleResume(w http.ResponseWriter, r *http.Request) {
	tenantID := s.resolveTenant(r)
	id := r.PathValue("id")
	row, err := s.store.Get(r.Context(), tenantID, id)
	if err != nil {
		if errors.Is(err, sessionstore.ErrNotFound) {
			s.writeError(w, http.StatusNotFound, "RESOURCE_NOT_FOUND", "session not found", nil)
			return
		}
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if err := session.Validate(session.PreconditionRequest{
		Endpoint:     session.EndpointResume,
		CurrentState: row.State,
	}); err != nil {
		s.writePreconditionError(w, err)
		return
	}

	// A session in `awaiting_client_action` has no live pod: it reached
	// that state after its pod failed and automatic recovery was
	// abandoned. When the gateway is wired with a pod binder, restore
	// the session onto a fresh pod before the row transitions to
	// running. A claim failure marks the row failed and surfaces a
	// retryable 503.
	if s.podBinder != nil {
		if err := s.resumeOnPod(r.Context(), row); err != nil {
			s.failSession(r.Context(), tenantID, id)
			s.writeError(w, http.StatusServiceUnavailable, "POD_CLAIM_FAILED",
				"could not resume the session on a warm pod: "+err.Error(), nil)
			return
		}
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionResume(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	s.writeSession(w, http.StatusOK, updated)
}

// resumeOnPod restores a session onto a fresh §5 warm pod. When the
// session carries a §7.1 WorkspaceSnapshot it is restored from that
// checkpoint via the adapter Resume RPC. A session that never
// checkpointed has no snapshot to restore; it is rebuilt from the §14
// WorkspacePlan recorded at create by reusing the start path.
func (s *Server) resumeOnPod(ctx context.Context, row sessionstore.Session) error {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			return err
		}
		return s.startOnPod(ctx, row, plan)
	}
	pool, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return err
	}
	result, err := s.podBinder.Resume(ctx, podsession.ResumeRequest{
		Pool:         pool,
		SessionID:    row.ID,
		TenantID:     row.TenantID,
		Runtime:      row.RuntimeRef,
		CheckpointID: row.WorkspaceSnapshot.Ref,
	})
	if err != nil {
		return err
	}
	s.podRegistry.Put(result)
	return nil
}
