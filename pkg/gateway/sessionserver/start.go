// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// tokenServiceUnavailableRetryAfterSeconds is the §4.3 Retry-After
// header emitted with TOKEN_SERVICE_UNAVAILABLE: 5 seconds is the
// circuit-breaker open-state cool-down in pkg/gateway/subsystem.
// spec: §4.3 line 214.
const tokenServiceUnavailableRetryAfterSeconds = 5

// writeTokenServiceUnavailable writes the §4.3 retryable-503 envelope
// when the Token Service circuit-breaker is open or the Token Service
// is otherwise unavailable. The Retry-After header lets a client back
// off with a deterministic budget rather than parsing the body.
// spec: §4.3 line 214.
func (s *Server) writeTokenServiceUnavailable(w http.ResponseWriter, cause error) {
	w.Header().Set("Retry-After", "5")
	msg := "Token Service is unavailable; retry in a few seconds"
	if cause != nil {
		msg = msg + ": " + cause.Error()
	}
	s.writeError(w, http.StatusServiceUnavailable, "TOKEN_SERVICE_UNAVAILABLE", msg, nil)
}

// CreateAndStartRequest is the §15.1 POST /v1/sessions/start body —
// the convenience surface that bundles create + finalize + start.
type CreateAndStartRequest struct {
	// Inherits the same shape as CreateSessionRequest.
	RuntimeRef       string            `json:"runtimeRef"`
	UserID           string            `json:"userId,omitempty"`
	WorkspacePlan    json.RawMessage   `json:"workspacePlan,omitempty"`
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`
	Environment      string            `json:"environment,omitempty"`
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
	if !s.requirePolicyChain(w, r, tenantID) {
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

	parsedPlan, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return
	}

	// §4.8 PreRoute: run the interceptor chain over the TaskSpec after
	// authentication and before runtime selection. A REJECT blocks the
	// create; a MODIFY may rewrite runtime hints (the requested runtime)
	// but not the authenticated identity, which the chain enforces.
	preRoute, ok := s.runRouteChain(w, r, interceptor.PhasePreRoute, routeTaskSpec{
		TenantID:         tenantID,
		UserID:           req.UserID,
		RequestedRuntime: req.RuntimeRef,
	})
	if !ok {
		return
	}
	runtimeRef := req.RuntimeRef
	if preRoute.RequestedRuntime != "" {
		runtimeRef = preRoute.RequestedRuntime
	}

	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       runtimeRef,
		Environment:      req.Environment,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: isoProf,
		WorkspacePlan:    planJSON,
		CreatedAt:        s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return
	}
	// §4.8 PostRoute: run the interceptor chain after runtime selection
	// with the resolved runtime metadata. A REJECT blocks the create; a
	// MODIFY may rewrite runtime-specific parameters but not the resolved
	// runtime or credential assignment, which the chain enforces.
	if _, ok := s.runRouteChain(w, r, interceptor.PhasePostRoute, routeTaskSpec{
		TenantID:            tenantID,
		UserID:              req.UserID,
		ResolvedRuntimeName: row.RuntimeRef,
	}); !ok {
		return
	}
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
	// no pod behind it. A Token Service outage during credential
	// assignment surfaces as TOKEN_SERVICE_UNAVAILABLE with Retry-After
	// per §4.3 line 214.
	if s.podBinder != nil {
		if err := s.startOnPod(r.Context(), row, parsedPlan); err != nil {
			s.failSession(r.Context(), tenantID, row.ID)
			if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
				s.writeTokenServiceUnavailable(w, err)
				return
			}
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
			if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
				s.writeTokenServiceUnavailable(w, err)
				return
			}
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
// ParseStored is used because the stored plan carries the
// gateway-written resolvedCommitSha that Parse rejects as client input.
func storedWorkspacePlan(row sessionstore.Session) (workspaceplan.Plan, error) {
	if len(row.WorkspacePlan) == 0 || isJSONNull(row.WorkspacePlan) {
		return workspaceplan.Plan{}, nil
	}
	plan, _, err := workspaceplan.ParseStored(row.WorkspacePlan)
	return plan, err
}

// resolvePlanForCreate parses a client-submitted §14 WorkspacePlan and,
// when a RefResolver is wired and the plan has a gitClone source, pins
// each gitClone ref to an immutable commit SHA per §14. It returns the
// parsed plan, the canonical JSON to persist on the session row (the
// pinned form when pinning occurred, the submitted bytes otherwise),
// and the consumer-advisory warnings. On a validation or
// ref-resolution failure it writes the §15.1 error response and
// returns ok=false; the caller must abort.
func (s *Server) resolvePlanForCreate(w http.ResponseWriter, r *http.Request, rawPlan json.RawMessage) (
	plan workspaceplan.Plan, storedJSON json.RawMessage, warnings []workspaceplan.Warning, ok bool,
) {
	if len(rawPlan) == 0 || isJSONNull(rawPlan) {
		return workspaceplan.Plan{}, nil, nil, true
	}
	parsed, warns, err := workspaceplan.Parse(rawPlan)
	if err != nil {
		s.writeWorkspacePlanError(w, err)
		return workspaceplan.Plan{}, nil, nil, false
	}
	storedJSON = rawPlan
	if !s.checkGitCloneAuthBindings(w, r, parsed) {
		return workspaceplan.Plan{}, nil, nil, false
	}
	if s.refResolver != nil && hasGitClone(parsed) {
		if err := workspaceplan.PinCommitSHAs(r.Context(), &parsed, s.refResolver); err != nil {
			s.writeRefResolveError(w, err)
			return workspaceplan.Plan{}, nil, nil, false
		}
		pinned, err := workspaceplan.Marshal(parsed)
		if err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"could not serialize the resolved workspace plan: "+err.Error(), nil)
			return workspaceplan.Plan{}, nil, nil, false
		}
		storedJSON = pinned
	}
	return parsed, storedJSON, warns, true
}

// hasGitClone reports whether the plan has at least one gitClone
// source — the only source type whose ref the gateway must pin.
func hasGitClone(plan workspaceplan.Plan) bool {
	for _, src := range plan.Sources {
		if src.Type == workspaceplan.TypeGitClone {
			return true
		}
	}
	return false
}

// checkGitCloneAuthBindings runs the §14 gitClone auth host-to-pool
// check for every gitClone source carrying an auth block. When a
// CredentialPools store is wired, each such source's URL host must
// bind to exactly one of the tenant's VCS credential pools whose
// provider matches the leaseScope; a binding failure writes the §15.1
// GIT_CLONE_AUTH_UNSUPPORTED_HOST or GIT_CLONE_AUTH_HOST_AMBIGUOUS
// response and returns false. With no store wired the check is
// skipped, so a gateway without one is unchanged.
func (s *Server) checkGitCloneAuthBindings(w http.ResponseWriter, r *http.Request, plan workspaceplan.Plan) bool {
	if s.credPools == nil {
		return true
	}
	var pools []credentialpoolstore.CredentialPool
	loaded := false
	for i, src := range plan.Sources {
		gc, ok := src.Variant.(workspaceplan.GitClone)
		if !ok || gc.Auth == nil {
			continue
		}
		host, hostOK := workspaceplan.GitCloneHost(gc)
		provider, _, scopeOK := workspaceplan.ParseLeaseScope(gc.Auth.LeaseScope)
		if !hostOK || !scopeOK {
			// validateGitClone already guaranteed a parseable HTTPS URL
			// and a well-formed leaseScope; nothing to bind otherwise.
			continue
		}
		if !loaded {
			ps, err := s.credPools.List(r.Context(), s.resolveTenant(r), credentialpoolstore.ListFilter{})
			if err != nil {
				s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
					"could not load credential pools: "+err.Error(), nil)
				return false
			}
			pools, loaded = ps, true
		}
		if _, err := credentialpoolstore.ResolveVCSPool(pools, provider, host); err != nil {
			s.writeVCSResolveError(w, err, i)
			return false
		}
	}
	return true
}

// writeVCSResolveError maps a §14 VCS-pool binding failure to its
// §15.1 response: GIT_CLONE_AUTH_UNSUPPORTED_HOST or
// GIT_CLONE_AUTH_HOST_AMBIGUOUS, both HTTP 422.
func (s *Server) writeVCSResolveError(w http.ResponseWriter, err error, sourceIndex int) {
	var ve *credentialpoolstore.VCSResolveError
	if !errors.As(err, &ve) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{"host": ve.Host, "sourceIndex": sourceIndex}
	if ve.Reason == credentialpoolstore.VCSHostAmbiguous {
		details["matchingPools"] = ve.MatchingPools
		s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_HOST_AMBIGUOUS",
			"the gitClone URL host matches multiple VCS credential pools", details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_AUTH_UNSUPPORTED_HOST",
		"the gitClone URL host matches no VCS credential pool", details)
}

// writeRefResolveError maps a §14 gitClone ref-resolution failure to
// its §15.1 response: a transient failure is GIT_CLONE_REF_RESOLVE_TRANSIENT
// (503, retryable), a permanent one is GIT_CLONE_REF_UNRESOLVABLE (422).
func (s *Server) writeRefResolveError(w http.ResponseWriter, err error) {
	var re *workspaceplan.ResolveError
	if !errors.As(err, &re) {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	details := map[string]any{
		"url":         re.URL,
		"ref":         re.Ref,
		"sourceIndex": re.SourceIndex,
		"reason":      string(re.Reason),
	}
	if re.Reason.Transient() {
		s.writeError(w, http.StatusServiceUnavailable, "GIT_CLONE_REF_RESOLVE_TRANSIENT",
			"could not resolve a gitClone ref: "+re.Err.Error(), details)
		return
	}
	s.writeError(w, http.StatusUnprocessableEntity, "GIT_CLONE_REF_UNRESOLVABLE",
		"could not resolve a gitClone ref: "+re.Err.Error(), details)
}

// experimentContextToProto converts a session's stored §10.7
// experimentContext into the adapter-protocol message delivered in the
// StartSession manifest. It returns nil for an unenrolled session.
func experimentContextToProto(ec *sessionstore.ExperimentContext) *adapterv1.ExperimentContext {
	if ec == nil {
		return nil
	}
	return &adapterv1.ExperimentContext{
		ExperimentId: ec.ExperimentID,
		VariantId:    ec.VariantID,
		Inherited:    ec.Inherited,
	}
}

// runtimeSetupPolicy resolves the effective §5.1 setupPolicy of the
// named runtime into the adapter-protocol message the adapter uses to
// bound the setup phase. It returns nil when no runtime store is
// wired, the runtime is unresolvable, or the runtime declares no
// setupPolicy.
func (s *Server) runtimeSetupPolicy(ctx context.Context, runtimeName string) *adapterv1.SetupPolicy {
	if s.runtimes == nil {
		return nil
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil || rt.SetupPolicy == nil {
		return nil
	}
	return &adapterv1.SetupPolicy{
		TimeoutSeconds: int32(rt.SetupPolicy.TimeoutSeconds),
		OnTimeout:      string(rt.SetupPolicy.OnTimeout),
	}
}

// startOnPod places a started session on a Kubernetes warm pod. It
// resolves the warm pool serving the session's runtime and §5.3
// isolation profile, then dispatches by the pool's executionMode:
// session and task modes claim an idle pod through podBinder.Bind,
// concurrent mode reserves a slot on a shared pod through
// podBinder.BindSlot (§5.2). The pod's §4.7 adapter runs the
// per-mode assignment sequence and the binding is recorded so the
// message and teardown paths can reach the pod.
//
// On success the bound pod's SandboxName is persisted to
// sessions.pod_assignment so a fresh gateway replica can recover the
// binding after a coordinator handoff without losing the assignment.
// spec: §4.2 line 160 — "Pod-to-session binding".
func (s *Server) startOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) error {
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return err
	}
	if match.ExecutionMode == string(runtimestore.ExecutionModeConcurrent) {
		result, err := s.podBinder.BindSlot(ctx, podsession.SlotBindRequest{
			Pool:              match.Pool,
			SessionID:         row.ID,
			TenantID:          row.TenantID,
			Runtime:           row.RuntimeRef,
			Style:             podclaim.ConcurrencyStyle(match.ConcurrencyStyle),
			MaxConcurrent:     match.MaxConcurrent,
			Plan:              podsession.WorkspacePlanToProto(plan),
			ExperimentContext: experimentContextToProto(row.ExperimentContext),
			TracingContext:    row.TracingContext,
			SetupPolicy:       s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		})
		if err != nil {
			return err
		}
		s.podRegistry.Put(result)
		s.persistPodAssignment(ctx, row.TenantID, row.ID, result.SandboxName)
		return nil
	}
	result, err := s.podBinder.Bind(ctx, podsession.BindRequest{
		Pool:              match.Pool,
		SessionID:         row.ID,
		TenantID:          row.TenantID,
		Runtime:           row.RuntimeRef,
		Plan:              podsession.WorkspacePlanToProto(plan),
		ExperimentContext: experimentContextToProto(row.ExperimentContext),
		TracingContext:    row.TracingContext,
		SetupPolicy:       s.runtimeSetupPolicy(ctx, row.RuntimeRef),
	})
	if err != nil {
		return err
	}
	s.podRegistry.Put(result)
	s.persistPodAssignment(ctx, row.TenantID, row.ID, result.SandboxName)
	return nil
}

// persistPodAssignment writes the bound pod's SandboxName back to the
// session row so a fresh gateway replica can pick up the binding from
// Postgres after a coordinator handoff. Best-effort: an update failure
// is logged via the configured error handler but does not fail the
// claim — the in-memory Registry remains authoritative for this
// replica, and the next coordination sweep will re-publish the
// assignment. spec: §4.2 line 160 — "Pod-to-session binding".
func (s *Server) persistPodAssignment(ctx context.Context, tenantID, sessionID, podAssignment string) {
	if podAssignment == "" {
		return
	}
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.PodAssignment = podAssignment
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: persist pod_assignment for session %s: %v", sessionID, err)
	}
}

// failSession marks a session row failed after a start-path error. The
// update is best-effort: the start handler has already chosen the HTTP
// error it returns to the client, so a store failure here cannot change
// the reply. A failed child session is archived to the §8.10
// session_tree_archive so a resumed parent can replay the outcome.
func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateFailed
		return nil
	})
	if err == nil {
		s.archiveSettledChild(ctx, updated)
	}
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
			if errors.Is(err, credassign.ErrTokenServiceUnavailable) {
				s.writeTokenServiceUnavailable(w, err)
				return
			}
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
	// spec: §4.4 line 236 — partial-manifest cleanup runs on every
	// resume regardless of whether the underlying reassembly
	// succeeded. The cleaner deletes the chunk objects and
	// soft-deletes the manifest row under the `deleted_at IS NULL`
	// guard; failures leave the row active for the §12.5 backstop
	// sweep to retry.
	if s.partialManifestCleaner != nil {
		if cerr := s.partialManifestCleaner.CleanupAfterResume(r.Context(), tenantID, id); cerr != nil {
			// Cleanup failure is non-fatal: the resume already
			// completed and the row stays active for the
			// backstop sweep. Surface the error in logs only.
			log.Printf("sessionserver: partial-manifest cleanup for session %s failed: %v", id, cerr)
		}
	}
	// spec: §7.2 line 138 — `session.resumed` precedes
	// `children_reattached`. The event fires from the resume handler
	// (rather than from resumeOnPod) so dev-mode / unit-test
	// deployments without a pod binder still emit the event.
	s.emitResumedEvent(r.Context(), updated, s.classifyResume(r.Context(), updated))
	s.emitChildrenReattached(r.Context(), tenantID, id)
	s.writeSession(w, http.StatusOK, updated)
}

// reattachedChild is the §7.1 ReattachedChild schema carried in the
// children_reattached event.
type reattachedChild struct {
	SessionID         string          `json:"session_id"`
	State             string          `json:"state"`
	PendingRequestID  string          `json:"pending_request_id,omitempty"`
	Result            json.RawMessage `json:"result,omitempty"`
	DelegationLeaseID string          `json:"delegation_lease_id"`
}

// emitChildrenReattached publishes the §7.1 / §8.10 children_reattached
// event on a resumed parent's event stream. Per §7.1 the event fires
// only when the parent has one or more active (non-terminal) children;
// it is a no-op when the parent has no children or every child has
// already settled. The children array carries every child — a settled
// child includes its §8.8 result. Best-effort: a failure to enumerate
// or publish never fails the resume.
func (s *Server) emitChildrenReattached(ctx context.Context, tenantID, parentID string) {
	if s.events == nil {
		return
	}
	all, err := s.store.List(ctx, tenantID, sessionstore.ListFilter{})
	if err != nil {
		return
	}
	children := make([]reattachedChild, 0)
	anyActive := false
	for _, row := range all {
		if row.ParentSessionID != parentID {
			continue
		}
		child := reattachedChild{
			SessionID: row.ID,
			State:     string(row.State),
			// v1 has no separate delegation-lease id; the child session
			// id is the parent's correlation handle.
			DelegationLeaseID: row.ID,
		}
		if session.IsTerminal(row.State) {
			child.Result, _ = json.Marshal(archivedTaskResult(row))
		} else {
			anyActive = true
		}
		children = append(children, child)
	}
	if !anyActive {
		return
	}
	data, _ := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	s.events.Publish(parentID, "children_reattached", string(data), s.clock())
}

// resumeOnPod restores a session onto a fresh §5 warm pod. When the
// session carries a §7.1 WorkspaceSnapshot it is restored from that
// checkpoint via the adapter Resume RPC. A session that never
// checkpointed has no snapshot to restore; it is rebuilt from the §14
// WorkspacePlan recorded at create by reusing the start path.
//
// On a successful resume the gateway publishes a §7.2 / §4.4
// `session.resumed` event with the derived `resumeMode` (full,
// partial_workspace, or conversation_only) and `workspaceLost`
// (derived from the resume mode) so clients can detect a degraded
// resume.
//
// spec: §4.4 line 263, §7.2 line 138, §10.1 partial-manifest path.
func (s *Server) resumeOnPod(ctx context.Context, row sessionstore.Session) error {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			return err
		}
		return s.startOnPod(ctx, row, plan)
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return err
	}
	result, err := s.podBinder.Resume(ctx, podsession.ResumeRequest{
		Pool:              match.Pool,
		SessionID:         row.ID,
		TenantID:          row.TenantID,
		Runtime:           row.RuntimeRef,
		CheckpointID:      row.WorkspaceSnapshot.Ref,
		ExperimentContext: experimentContextToProto(row.ExperimentContext),
		TracingContext:    row.TracingContext,
	})
	if err != nil {
		return err
	}
	s.podRegistry.Put(result)
	// spec: §4.2 line 156 — recovery_generation is incremented on each
	// pod recovery. Persist the new pod assignment in the same update
	// so a fresh replica picks up the recovered binding without
	// re-running resume.
	s.bumpRecoveryGeneration(ctx, row.TenantID, row.ID, result.SandboxName)
	return nil
}

// classifyResume picks the §4.4 / §7.2 ResumeMode for a resume of the
// given session by combining (a) the workspace snapshot source, (b)
// the eviction-state-store lookup (conversation-only fallback), and
// (c) the partial-manifest lookup (partial-workspace reassembly). The
// precedence is:
//
//   - eviction-state record present → ResumeConversationOnly (the
//     workspace bytes are gone; the §4.4 fallback writer recorded
//     conversation cursor + last-message context only).
//   - active partial manifest present → ResumePartialWorkspace (the
//     §10.1 reassembly path applies; recovery fraction is carried on
//     the event when the manifest had a baseline full checkpoint
//     size).
//   - workspace snapshot present, no eviction / partial state →
//     ResumeFull.
//
// A nil lookup (production without the store wired, or dev mode)
// degrades to ResumeFull. A lookup error degrades to ResumeFull as
// well — the resume itself succeeded, so a transient store outage
// must not block the session from coming back online; the operator
// observes the degraded classification only by inspecting the gauge
// rather than by the event.
//
// spec: §4.4 line 263; §7.2 line 138; §10.1 partial-manifest path.
func (s *Server) classifyResume(ctx context.Context, row sessionstore.Session) checkpoint.ResumeMode {
	if s.evictionStateLookup != nil {
		has, err := s.evictionStateLookup.HasEvictionState(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumeConversationOnly
		}
	}
	if s.partialManifestLookup != nil {
		has, err := s.partialManifestLookup.HasActivePartialManifest(ctx, row.TenantID, row.ID)
		if err == nil && has {
			return checkpoint.ResumePartialWorkspace
		}
	}
	return checkpoint.ResumeFull
}

// resumedEventPayload is the §7.2 line 138 `session.resumed` event
// schema: `resumeMode`, `workspaceLost`, and an optional
// `workspaceRecoveryFraction` (populated by the §10.1 partial-manifest
// path; omitted on full and conversation-only resumes per the
// optional-fraction rule).
type resumedEventPayload struct {
	ResumeMode                 string   `json:"resumeMode"`
	WorkspaceLost              bool     `json:"workspaceLost"`
	WorkspaceRecoveryFraction  *float64 `json:"workspaceRecoveryFraction,omitempty"`
}

// emitResumedEvent publishes the §7.2 line 138 `session.resumed` event
// onto the session's event stream. Best-effort: when the gateway is
// not wired with an event bus (dev mode / unit tests without one) the
// emission is a no-op.
//
// spec: §7.2 line 138, §4.4 line 263, §10.1 partial-manifest path.
func (s *Server) emitResumedEvent(_ context.Context, row sessionstore.Session, mode checkpoint.ResumeMode) {
	if s.events == nil {
		return
	}
	payload := resumedEventPayload{
		ResumeMode:    string(mode),
		WorkspaceLost: mode.WorkspaceLost(),
	}
	s.publishEvent(row.ID, "session.resumed", payload)
}

// bumpRecoveryGeneration increments the §4.2 recovery_generation
// counter and persists the recovered pod assignment in the same
// transaction. The store's monotonicity floor ensures the counter
// only advances; the in-memory Registry already holds the new BindResult
// on success.
//
// A pod recovery is also a §4.2 line 158 retry — the Session Manager
// is responsible for both counters, and a recovery onto a fresh pod
// is the v1 retry path. retry_count is bumped in the same
// transaction; the store enforces monotonicity on both columns.
// spec: §4.2 line 156 — "incremented on each pod recovery".
// spec: §4.2 line 158 — "Retry counters and policy enforcement".
func (s *Server) bumpRecoveryGeneration(ctx context.Context, tenantID, sessionID, podAssignment string) {
	_, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.RecoveryGeneration++
		row.RetryCount++
		if podAssignment != "" {
			row.PodAssignment = podAssignment
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump recovery_generation for session %s: %v", sessionID, err)
	}
}
