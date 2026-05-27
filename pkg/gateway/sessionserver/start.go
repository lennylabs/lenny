// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/checkpoint"
	"github.com/lennylabs/lenny/pkg/gateway/credassign"
	"github.com/lennylabs/lenny/pkg/gateway/credentialpoolstore"
	"github.com/lennylabs/lenny/pkg/gateway/credrouter"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// tokenServiceUnavailableRetryAfterSeconds is the §4.3 Retry-After
// header emitted with TOKEN_SERVICE_UNAVAILABLE: 5 seconds is the
// circuit-breaker open-state cool-down in pkg/gateway/subsystem.
// spec: §4.3 line 214.
const tokenServiceUnavailableRetryAfterSeconds = 5

// DefaultWarmupEstimateSeconds is the §5.2 line 625 fallback estimate
// for a pool's remaining warm-up time, used for the PoolWarmingUp 503's
// estimatedReadyIn detail and Retry-After header when no historical
// lenny_warmpool_pod_startup_duration_seconds p50 is available. The
// Retry-After header is max(30, estimate). Operators tune it through
// the gateway's WarmupEstimateSeconds option.
// spec: §5.2 line 625.
const DefaultWarmupEstimateSeconds = 120

// minWarmupRetryAfterSeconds is the §5.2 line 625 floor on the
// PoolWarmingUp Retry-After header: max(30, estimatedWarmupSeconds).
const minWarmupRetryAfterSeconds = 30

// writePodClaimError maps a startOnPod / resumeOnPod failure to its
// §15.1 error envelope. The §5.2 pool-warming and pod/slot exhaustion
// conditions take their spec-defined codes; a Token Service outage
// surfaces as TOKEN_SERVICE_UNAVAILABLE (§4.3). Any other failure
// falls back to a retryable 503 carrying fallbackCode + fallbackMsg
// plus a §15.1 line 1138 `Retry-After` header. Per §7.1 line 28 the
// atomic-creation paths (`POST /v1/sessions`, `POST /v1/sessions/start`)
// pass `SESSION_CREATION_FAILED` so a generic claim failure surfaces
// the spec-named code instead of the legacy `POD_CLAIM_FAILED`; the
// `POST /v1/sessions/{id}/start` two-step path passes `STARTING_FAILED`
// per §6.2 line 303.
// spec: §5.2 line 519 (WARM_POOL_EXHAUSTED), §5.2 lines 602-625
// (RUNTIME_UNAVAILABLE), §7.1 line 28 (SESSION_CREATION_FAILED),
// §15.1 line 1138 (Retry-After).
func (s *Server) writePodClaimError(w http.ResponseWriter, err error, fallbackCode, fallbackMsg string) {
	var warming *podsession.PoolWarmingError
	var credAssign *podsession.CredentialAssignmentError
	switch {
	case errors.Is(err, credassign.ErrTokenServiceUnavailable):
		s.writeTokenServiceUnavailable(w, err)
	case errors.As(err, &warming):
		s.writePoolWarming(w, warming)
	case errors.Is(err, credrouter.ErrUserCredentialNotFound):
		// spec: §4.9 lines 1364, §15.1 line 993 — a user-only policy with
		// no pre-registered credential for the user and provider.
		s.writeError(w, http.StatusNotFound, "USER_CREDENTIAL_NOT_FOUND",
			"no pre-registered credential found for the user and provider; "+
				"register one via POST /v1/credentials or configure pool fallback", nil)
	case errors.Is(err, credrouter.ErrNoCredentialAvailable):
		// spec: §4.9 line 1218 — no provider in the intersection had an
		// assignable credential at the pre-claim check; no pod was claimed.
		s.writeCredentialPoolExhausted(w, "pre_claim")
	case errors.As(err, &credAssign):
		// spec: §4.9 line 1220 — the pre-claim check passed but the lease
		// assignment failed (a credential became unavailable in the race
		// window). Record the mismatch so operators can tune pool sizing.
		if s.preclaimMismatch != nil {
			s.preclaimMismatch(credAssign.Pool, credAssign.Provider)
		}
		s.writeCredentialPoolExhausted(w, "assignment_race")
	case errors.Is(err, podclaim.ErrNoIdlePod):
		s.writeWarmPoolExhausted(w, "no_idle_pods")
	case errors.Is(err, podclaim.ErrNoConcurrentSlot), errors.Is(err, podclaim.ErrTenantMismatch):
		s.writeWarmPoolExhausted(w, "concurrent_slots_exhausted")
	default:
		// spec: §7.1 line 28 / §15.1 line 1138 — the atomic-unit fallback
		// is always retryable; include Retry-After so a client backs off
		// with a deterministic budget rather than parsing the body.
		w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
		s.writeError(w, http.StatusServiceUnavailable, fallbackCode,
			fallbackMsg+": "+err.Error(), nil)
	}
}

// sessionCreationFailedRetryAfterSeconds is the default Retry-After
// budget written on §7.1 atomic-unit failures (SESSION_CREATION_FAILED,
// STARTING_FAILED). Five seconds matches the §4.3 TOKEN_SERVICE_UNAVAILABLE
// floor and is short enough that a client retry sees a freshly idle
// warm pod under the typical §5.2 fill cadence.
// spec: §7.1 line 28; §15.1 line 1138.
const sessionCreationFailedRetryAfterSeconds = 5

// writeSessionCreationFailed writes the §7.1 line 28 SESSION_CREATION_FAILED
// 503 envelope used by the create paths (POST /v1/sessions, POST
// /v1/sessions/start) when the atomic unit (steps 2-8) fails outside
// the classified errors (CREDENTIAL_POOL_EXHAUSTED, WARM_POOL_EXHAUSTED,
// RUNTIME_UNAVAILABLE, TOKEN_SERVICE_UNAVAILABLE). reason is echoed
// under details.reason so an operator can distinguish
// upload_token_issuance_failed from row_persistence_failed without
// parsing the human message. The §15.1 line 1138 Retry-After header is
// always included so clients back off with a deterministic budget.
// spec: §7.1 line 28; §15.1 line 1138.
func (s *Server) writeSessionCreationFailed(w http.ResponseWriter, reason, message string) {
	w.Header().Set("Retry-After", strconv.Itoa(sessionCreationFailedRetryAfterSeconds))
	s.writeError(w, http.StatusServiceUnavailable, "SESSION_CREATION_FAILED",
		message, map[string]any{"reason": reason})
}

// writeCredentialPoolExhausted writes the §4.9 CREDENTIAL_POOL_EXHAUSTED
// envelope (category POLICY, HTTP 503). details.reason distinguishes
// the pre-claim miss ("pre_claim") from the assignment-race miss
// ("assignment_race"). spec: §4.9 lines 1218, 1220; §15.1 line 990.
func (s *Server) writeCredentialPoolExhausted(w http.ResponseWriter, reason string) {
	s.writeError(w, http.StatusServiceUnavailable, "CREDENTIAL_POOL_EXHAUSTED",
		"no provider has an assignable credential; retry once the pool frees up",
		map[string]any{"reason": reason})
}

// writeWarmPoolExhausted writes the §5.2 line 519 WARM_POOL_EXHAUSTED
// envelope. details.reason distinguishes "no_idle_pods" (the pool holds
// no pods) from "concurrent_slots_exhausted" (pods exist but every slot
// is full). The code is the same one session-mode pod exhaustion uses.
// spec: §5.2 line 519, §15.2.1 line 1017.
func (s *Server) writeWarmPoolExhausted(w http.ResponseWriter, reason string) {
	s.writeError(w, http.StatusServiceUnavailable, "WARM_POOL_EXHAUSTED",
		"no warm pod or concurrent slot is available; retry with backoff",
		map[string]any{"reason": reason})
}

// writePoolWarming writes the §5.2 lines 602-625 "Pool Not Ready"
// response: 503 RUNTIME_UNAVAILABLE with Retry-After max(30,
// estimatedWarmupSeconds) and a details block carrying the pool name,
// the PoolWarmingUp condition, the warm-up estimate, and the count of
// pods still warming.
// spec: §5.2 lines 602-625.
func (s *Server) writePoolWarming(w http.ResponseWriter, warming *podsession.PoolWarmingError) {
	estimate := s.warmupEstimateSeconds
	if estimate <= 0 {
		estimate = DefaultWarmupEstimateSeconds
	}
	retryAfter := estimate
	if retryAfter < minWarmupRetryAfterSeconds {
		retryAfter = minWarmupRetryAfterSeconds
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	s.writeError(w, http.StatusServiceUnavailable, "RUNTIME_UNAVAILABLE",
		fmt.Sprintf("Pool '%s' is warming up — no idle pods are available yet. "+
			"Retry after the indicated interval.", warming.Pool),
		map[string]any{
			"poolName":         warming.Pool,
			"poolCondition":    "PoolWarmingUp",
			"estimatedReadyIn": estimate,
			"podsWarming":      int(warming.PodsWarming),
		})
}

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

	// spec: §7.1 line 75 — resolve the pool-derived isolation level
	// once so executionMode + scrubPolicy can be persisted on the row
	// (same path as the two-step create flow). GET / List read the
	// persisted values via toResponse so the rich envelope survives a
	// coordinator handoff.
	level := s.resolveIsolationLevel(r.Context(), runtimeRef, isoProf)
	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       runtimeRef,
		Environment:      req.Environment,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: isoProf,
		ExecutionMode:    level.ExecutionMode,
		ScrubPolicy:      level.ScrubPolicy,
		WorkspacePlan:    planJSON,
		CreatedAt:        s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// spec: §7.1 line 77 — stamp the default artifact-retention deadline
	// at create (mirrors the plain create path) so the GC can reclaim
	// this session's artifacts; the terminal transition rolls it forward.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.defaultRetention)
	// spec: §4.2 line 159 — stamp the resume-eligibility deadline so the
	// row reaching `running` carries the same per-session resume window
	// as the two-step `POST /v1/sessions` + `POST /start` path.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
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

	// spec: §7.1 line 28 — atomicity. Mint the §7.1 step 8 uploadToken
	// and run the pod claim BEFORE the row is persisted: a failure in any
	// step of the create-and-start atomic unit (mint, pre-claim,
	// claim, bind) returns `SESSION_CREATION_FAILED` to the client with
	// no session row left behind, matching the §7.1 "does NOT persist
	// the session row" contract. On store.Create failure the bound pod
	// is released to the §6.2 reclaim path so no pod or credential
	// lease leaks past the failure.
	// spec: §7.1 line 58 — TTL = maxCreatedStateTimeoutSeconds. F-7.4.7.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	// When the gateway is wired with a pod binder, the §15.1 start path
	// places the session on a Kubernetes warm pod before the row is
	// persisted. A claim failure leaves no row behind per the atomicity
	// contract. A Token Service outage during credential assignment
	// surfaces as TOKEN_SERVICE_UNAVAILABLE with Retry-After per §4.3
	// line 214.
	var bound *podsession.BindResult
	if s.podBinder != nil {
		result, err := s.startOnPod(r.Context(), row, parsedPlan)
		if err != nil {
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return
		}
		bound = result
		if bound != nil {
			row.PodAssignment = bound.SandboxName
		}
	}

	if err := s.store.Create(r.Context(), row); err != nil {
		// spec: §7.1 line 28 — persistence failure after the bind must
		// roll back the claimed pod so the gateway does not leak a pod
		// or its credential lease past a "no session_id returned"
		// failure.
		s.rollbackBinding(r.Context(), bound)
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return
	}
	s.recordSessionCreated(r.Context(), row)
	s.registerBinding(r.Context(), bound)

	// spec: §7.2 line 137 — the create-and-start path lands the session
	// directly in running, so emit status_change(running) for SSE
	// subscribers (e.g. a parent watching a delegated child) on parity
	// with the explicit POST /start transition.
	s.emitStatusChange(row.TenantID, row.ID, row.State)

	base := toResponse(row)
	// spec: §7.1 line 75 — the pool-resolved level was stamped onto the
	// row before persist; persistedIsolationLevel inside toResponse
	// already returns it. Override with the local copy for parity with
	// the two-step create path (covers the no-pool-resolved fallback).
	base.SessionIsolationLevel = level
	resp := CreateSessionResponse{
		SessionResponse:       base,
		UploadToken:           tok,
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
		result, err := s.startOnPod(r.Context(), row, plan)
		if err != nil {
			// spec: §7.1 line 28 / §6.2 line 303 — `POST /v1/sessions/{id}/start`
			// returns `STARTING_FAILED` when the §7.1 atomic-creation unit
			// fails on the explicit start half. The row stays `ready` so the
			// client can retry; no pod is left allocated (pre-claim or
			// claim-attempt errors release before this point).
			s.writePodClaimError(w, err, "STARTING_FAILED", "could not place the session on a warm pod")
			return
		}
		s.registerBinding(r.Context(), result)
	}

	updated, err := s.store.Update(r.Context(), tenantID, id, func(row *sessionstore.Session) error {
		transitionStart(row)
		return nil
	})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	// spec: §7.2 line 137 — surface the ready → running transition.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
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
// runtimeManifestFields resolves the runtime definition and returns the
// §15.4 adapter-manifest fields sourced from it (§4.7): the agentInterface
// descriptor JSON-encoded (nil when the runtime declares none, so the
// manifest field is null) and minPlatformVersion. A resolve failure yields
// zero values so a missing descriptor never blocks session start — the
// gateway already enforces minPlatformVersion at registration, and
// agentInterface is informational.
func (s *Server) runtimeManifestFields(ctx context.Context, runtimeName string) (agentInterface []byte, minPlatformVersion string) {
	if s.runtimes == nil {
		return nil, ""
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, runtimeName)
	if err != nil {
		return nil, ""
	}
	if rt.AgentInterface != nil {
		if b, err := json.Marshal(rt.AgentInterface); err == nil {
			agentInterface = b
		}
	}
	return agentInterface, rt.MinPlatformVersion
}

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

// resolveCredentialPools runs the §4.9 pre-claim credential
// availability check for a session and returns the provider→pool map
// the binder mints leases from. It computes the §4.9 intersection of
// the session runtime's supportedProviders and the tenant's
// credentialPolicy.providerPools, builds a pool descriptor per provider
// from the credential-pool registry, and asks the CredentialRouter to
// resolve a source for each provider. The check passes when at least
// one provider has an assignable credential; on miss it returns the
// router's typed error (credrouter.ErrNoCredentialAvailable →
// CREDENTIAL_POOL_EXHAUSTED, credrouter.ErrUserCredentialNotFound →
// USER_CREDENTIAL_NOT_FOUND), surfaced by writePodClaimError before any
// pod is claimed.
//
// When the tenant configures no credentialPolicy, or the tenant /
// runtime / credential-pool registries are not all wired, the
// intersection is empty and the session assigns no upstream LLM
// credentials — preserving the pre-§4.9 behavior for deployments
// without credential pools.
//
// spec: §4.9 lines 1216-1218 (Pre-Claim check), 1326 (intersection).
func (s *Server) resolveCredentialPools(ctx context.Context, row sessionstore.Session) (map[string]string, error) {
	if s.tenants == nil || s.runtimes == nil || s.credPools == nil {
		return nil, nil
	}
	tenant, err := s.tenants.Get(ctx, row.TenantID)
	if err != nil {
		// The §10.2 tenant-claim extractor already gated the request; an
		// unresolvable tenant row here means no credentialPolicy applies.
		return nil, nil
	}
	policy := tenant.CredentialPolicy
	if !policy.Configured() {
		return nil, nil
	}
	rt, err := runtimestore.Resolve(ctx, s.runtimes, row.RuntimeRef)
	if err != nil {
		// Runtime-resolution failure is surfaced by the pool-resolution
		// path; the §4.9 layer contributes no credentials.
		return nil, nil
	}
	intersection := credrouter.Intersection(rt.SupportedProviders, policy)

	allPools, err := s.credPools.List(ctx, row.TenantID, credentialpoolstore.ListFilter{})
	if err != nil {
		return nil, fmt.Errorf("sessionserver: load credential pools for pre-claim: %w", err)
	}
	byName := make(map[string]credentialpoolstore.CredentialPool, len(allPools))
	for _, p := range allPools {
		byName[p.Name] = p
	}

	in := credrouter.PreClaimInput{
		TenantID:        row.TenantID,
		UserID:          row.UserID,
		PreferredSource: policy.PreferredSource,
	}
	for _, provider := range intersection {
		var descs []credrouter.PoolDescriptor
		for _, poolName := range policy.PoolOrderFor(provider) {
			if p, ok := byName[poolName]; ok {
				descs = append(descs, poolDescriptor(p))
			}
		}
		userAvail := s.userCredChecker != nil &&
			s.userCredChecker(ctx, row.TenantID, row.UserID, provider)
		in.Providers = append(in.Providers, credrouter.ProviderInput{
			Provider:                provider,
			AllowedPools:            descs,
			UserCredentialAvailable: userAvail,
		})
	}

	res, err := credrouter.PreClaim(ctx, s.credRouter, in)
	if err != nil {
		return nil, err
	}
	return res.PoolAssignments, nil
}

// poolDescriptor maps a §4.9 credential pool to the router's pool
// descriptor. A pool is assignable when it holds at least one
// non-revoked credential; cooldown is rotation-time state and is false
// at session creation. The live active-leases-versus-maxConcurrent
// refinement (spec §4.9 line 1218) tightens HasCapacity once a lease-
// utilization reader is wired; until then a pool with a usable
// credential is treated as having capacity.
func poolDescriptor(p credentialpoolstore.CredentialPool) credrouter.PoolDescriptor {
	assignable := false
	for _, c := range p.Credentials {
		if !c.IsRevoked() {
			assignable = true
			break
		}
	}
	return credrouter.PoolDescriptor{
		PoolID:      p.Name,
		Healthy:     assignable,
		HasCapacity: assignable,
	}
}

// startOnPod places a started session on a Kubernetes warm pod. It
// resolves the warm pool serving the session's runtime and §5.3
// isolation profile, then dispatches by the pool's executionMode:
// session and task modes claim an idle pod through podBinder.Bind,
// concurrent mode reserves a slot on a shared pod through
// podBinder.BindSlot (§5.2). The pod's §4.7 adapter runs the
// per-mode assignment sequence; the BindResult is returned for the
// caller to register and persist after its own atomicity gates pass.
//
// startOnPod no longer registers the binding or persists the
// SandboxName field itself: the §7.1 line 28 atomicity contract
// requires the gateway to skip the persist write entirely when an
// earlier step in steps 2-8 fails, so the caller decides when to
// publish the binding. The session-mode and concurrent-slot paths
// both surface their result the same way; the caller distinguishes
// them by inspecting BindResult.SlotID when it has to roll back.
// spec: §4.2 line 160 — "Pod-to-session binding"; §7.1 line 28 —
// atomic-creation rollback.
func (s *Server) startOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (*podsession.BindResult, error) {
	// spec: §4.9 lines 1216-1218 — run the pre-claim credential
	// availability check and resolve the per-provider pool map BEFORE a
	// pod is claimed, so a session that would fail at credential
	// assignment is rejected without wasting a warm pod.
	credPools, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		return nil, err
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return nil, err
	}
	// spec: §5.2 lines 602-625 — a session targeting a pool in the
	// PoolWarmingUp bootstrap state returns 503 RUNTIME_UNAVAILABLE
	// before a claim is attempted, so the client receives a retry hint
	// rather than burning a claim attempt that would surface as a less
	// informative SESSION_CREATION_FAILED.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	if match.ExecutionMode == string(runtimestore.ExecutionModeConcurrent) {
		result, err := s.podBinder.BindSlot(ctx, podsession.SlotBindRequest{
			Pool:               match.Pool,
			SessionID:          row.ID,
			TenantID:           row.TenantID,
			Runtime:            row.RuntimeRef,
			Style:              podclaim.ConcurrencyStyle(match.ConcurrencyStyle),
			MaxConcurrent:      match.MaxConcurrent,
			Plan:               podsession.WorkspacePlanToProto(plan),
			ExperimentContext:  experimentContextToProto(row.ExperimentContext),
			TracingContext:     row.TracingContext,
			SetupPolicy:        s.runtimeSetupPolicy(ctx, row.RuntimeRef),
			CredentialPools:    credPools,
			AgentInterface:     agentInterface,
			MinPlatformVersion: minPlatformVersion,
		})
		if err != nil {
			return nil, err
		}
		return result, nil
	}
	result, err := s.podBinder.Bind(ctx, podsession.BindRequest{
		Pool:               match.Pool,
		SessionID:          row.ID,
		TenantID:           row.TenantID,
		Runtime:            row.RuntimeRef,
		Plan:               podsession.WorkspacePlanToProto(plan),
		ExperimentContext:  experimentContextToProto(row.ExperimentContext),
		TracingContext:     row.TracingContext,
		SetupPolicy:        s.runtimeSetupPolicy(ctx, row.RuntimeRef),
		CredentialPools:    credPools,
		AgentInterface:     agentInterface,
		MinPlatformVersion: minPlatformVersion,
	})
	if err != nil {
		return nil, err
	}
	s.recordStartupMetrics(match, result.Timings)
	return result, nil
}

// registerBinding publishes a successful startOnPod / resumeOnPod
// result so the message and teardown paths can reach the pod, and
// persists the bound pod's SandboxName to sessions.pod_assignment so
// a fresh gateway replica can recover the binding after a coordinator
// handoff. The persist is best-effort: a failure leaves the in-memory
// Registry authoritative and the next coordination sweep re-publishes
// the assignment. spec: §4.2 line 160 — "Pod-to-session binding".
func (s *Server) registerBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil {
		return
	}
	s.podRegistry.Put(result)
	s.persistPodAssignment(ctx, result.TenantID, result.SessionID, result.SandboxName)
	// F-7.4.15: republish any §14 advisory warnings the adapter raised
	// during FinalizeWorkspace materialization. The
	// `workspace_plan_strip_components_skip` warning per §7.4 line 459
	// is the only producer in v1; SSE subscribers see one event per
	// skipped archive entry so a client can audit the strip-components
	// rule.
	s.publishWorkspaceWarnings(result)
}

// publishWorkspaceWarnings emits one SSE `workspace_plan_warning`
// frame per §14 advisory the adapter returned from FinalizeWorkspace.
// The handler is a no-op when the bind produced no warnings.
//
// spec: §7.4 line 459; §14 WarningCode. F-7.4.15.
func (s *Server) publishWorkspaceWarnings(result *podsession.BindResult) {
	if result == nil || len(result.WorkspacePlanWarnings) == 0 {
		return
	}
	for _, w := range result.WorkspacePlanWarnings {
		if w == nil {
			continue
		}
		s.publishEvent(result.TenantID, result.SessionID, "workspace_plan_warning", map[string]any{
			"code":        w.GetCode(),
			"sourceIndex": w.GetSourceIndex(),
			"entry":       w.GetEntry(),
			"message":     w.GetMessage(),
		})
	}
}

// rollbackBinding releases a successful startOnPod result whose
// caller has decided to abort the §7.1 line 28 atomic-creation flow
// before the session row was persisted. Session-mode and task-mode
// bindings drop through Binder.Release with a `failed` disposition;
// concurrent-slot bindings drop through ReleaseSlot. Best-effort:
// the §6.2 reclaim path runs regardless, so a release error here is
// logged and swallowed. spec: §7.1 line 28 atomic-creation rollback.
func (s *Server) rollbackBinding(ctx context.Context, result *podsession.BindResult) {
	if result == nil || s.podBinder == nil {
		return
	}
	var err error
	if result.SlotID != "" {
		err = s.podBinder.ReleaseSlot(ctx, result)
	} else {
		err = s.podBinder.Release(ctx, result, state.Failed)
	}
	if err != nil {
		log.Printf("sessionserver: rollback binding for session %s: %v", result.SessionID, err)
	}
}

// recordStartupMetrics observes the §6.3 startup-latency histograms for
// a successful session-mode start. It records each instrumented
// hot-path phase on lenny_session_startup_phase_duration_seconds
// (§6.3 line 372) and the end-to-end pod-warm envelope on
// lenny_session_startup_duration_seconds (§6.3 line 348). Per §6.3 line
// 348 the end-to-end metric is pod claim through agent session ready
// excluding workspace materialization; setup commands are also excluded
// because they are deployer-controlled (§6.3 line 363) and the 2s runc /
// 5s gVisor SLO budgets only the platform phases (claim ≤100ms +
// credential ≤100ms + agent start ≤1.5s/4.5s). The end-to-end total is
// therefore PodClaim + CredentialAssignment + AgentSessionStart.
// The first-prompt/TTFT phase is tracked separately (F-6.3.3,
// lenny_session_time_to_first_token_seconds) because it needs runtime
// streaming feedback the start path does not see.
// spec: §6.3 lines 348, 358, 372.
func (s *Server) recordStartupMetrics(match podsession.PoolMatch, t podsession.BindTimings) {
	runtimeClass, ok := isolation.RuntimeClassName(isolation.Profile(match.IsolationProfile))
	if !ok {
		// An unrecognized profile would mislabel the series; skip rather
		// than emit an empty runtime_class.
		return
	}
	if s.observeStartupPhase != nil {
		s.observeStartupPhase("pod_claim", runtimeClass, t.PodClaim.Seconds())
		s.observeStartupPhase("workspace_materialization", runtimeClass, t.WorkspaceMaterialization.Seconds())
		s.observeStartupPhase("setup_commands", runtimeClass, t.SetupCommands.Seconds())
		s.observeStartupPhase("credential_assignment", runtimeClass, t.CredentialAssignment.Seconds())
		s.observeStartupPhase("agent_session_start", runtimeClass, t.AgentSessionStart.Seconds())
	}
	if s.observeStartupDuration != nil {
		total := t.PodClaim + t.CredentialAssignment + t.AgentSessionStart
		s.observeStartupDuration(match.Pool, runtimeClass, match.IsolationProfile, total.Seconds())
	}
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
		// spec: §7.2 lines 137, 141 / §11.7 / §7.1 line 77 — the
		// start-path failure is a terminal transition, so it emits the
		// same status_change/session_complete SSE events, the
		// session.failed audit event, and the retention-window roll as
		// any other terminal path. The heavier seal/executor-close
		// teardown stays in recordSessionCompleted: a start-path failure
		// never bound a workspace to seal.
		s.emitTerminalLifecycle(ctx, updated)
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
			// spec: §16.1 catalog — record the failed resume attempt
			// before unwinding so the {pool, outcome="failure"} counter
			// advances even when the row transitions straight to failed.
			// F-7.3.10.
			if s.incSessionResumeAttempt != nil {
				s.incSessionResumeAttempt(row.PoolRef, "failure")
			}
			s.failSession(r.Context(), tenantID, id)
			// spec: §7.3 — a resume claim failure surfaces as a retryable
			// 503; the row was already persisted (resume requires it), so
			// `RESUME_FAILED` is the analogous spec-named fallback to
			// `SESSION_CREATION_FAILED` and `STARTING_FAILED`.
			s.writePodClaimError(w, err, "RESUME_FAILED", "could not resume the session on a warm pod")
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
	// spec: §16.1 catalog — every resume call increments the
	// `lenny_session_resume_attempts_total{pool, outcome="success"}`
	// counter; the matching {outcome="failure"} branch fires above on
	// the resumeOnPod error path. F-7.3.10.
	if s.incSessionResumeAttempt != nil {
		s.incSessionResumeAttempt(updated.PoolRef, "success")
	}
	// spec: §11.7 / §16.7 — the session.resumed audit row is appended
	// after every successful resume so SIEM dashboards can filter on
	// the §7.3 recovery surface. The matching SSE `session.resumed`
	// event below is the client-facing signal. F-7.3.18.
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(r.Context(), SessionLifecycleEvent{
			EventType:  auditSessionResumed,
			TenantID:   updated.TenantID,
			SessionID:  updated.ID,
			UserID:     updated.UserID,
			RuntimeRef: updated.RuntimeRef,
			State:      string(updated.State),
			At:         s.clock(),
		})
	}
	// spec: §7.2 line 137 — surface the resume transition (the
	// resume_pending → resuming → running chain collapses to the
	// resolved state) before the richer session.resumed event below.
	s.emitStatusChange(updated.TenantID, updated.ID, updated.State)
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
//
// A non-terminal child carries `pending_request_id` when an outstanding
// `lenny/request_input` or §6/§9.2 pending interaction exists for the
// child — the parent needs the id to answer via `lenny/send_message`
// (inReplyTo) or the §15.1 interaction endpoints. spec: §7.2 line 153
// (ReattachedChild.pending_request_id). F-7.2.16.
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
			// spec: §7.2 line 153 — populate the pending_request_id when
			// the child has an outstanding request directed at the
			// parent. lenny/request_input wins over the
			// interaction-store entries because it carries a structured
			// reply contract; an interaction (tool-use / elicitation)
			// is the fallback when the child raised an approval.
			// F-7.2.16.
			child.PendingRequestID = s.lookupPendingRequest(ctx, tenantID, row.ID)
		}
		children = append(children, child)
	}
	if !anyActive {
		return
	}
	data, _ := json.Marshal(struct {
		Children []reattachedChild `json:"children"`
	}{Children: children})
	s.events.PublishForTenant(tenantID, parentID, "children_reattached", string(data), s.clock())
}

// lookupPendingRequest returns the pending request id for a child
// session in `input_required`-equivalent state, or "" when none is
// outstanding. It prefers `lenny/request_input` registrations (the
// §8.5 structured-reply contract) and falls back to a §6/§9.2 pending
// interaction (tool-use approval / elicitation). When both sources are
// unwired the function is a no-op. spec: §7.2 line 153 (ReattachedChild
// schema). F-7.2.16.
func (s *Server) lookupPendingRequest(ctx context.Context, tenantID, sessionID string) string {
	if s.inputWaits != nil {
		if ids := s.inputWaits.PendingForSession(sessionID); len(ids) > 0 {
			sort.Strings(ids)
			return ids[0]
		}
	}
	if s.interactions != nil {
		pending, err := s.interactions.ListPending(ctx, tenantID, sessionID)
		if err == nil && len(pending) > 0 {
			return pending[0].ID
		}
	}
	return ""
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
		result, err := s.startOnPod(ctx, row, plan)
		if err != nil {
			return err
		}
		s.registerBinding(ctx, result)
		return nil
	}
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile))
	if err != nil {
		return err
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	result, err := s.podBinder.Resume(ctx, podsession.ResumeRequest{
		Pool:               match.Pool,
		SessionID:          row.ID,
		TenantID:           row.TenantID,
		Runtime:            row.RuntimeRef,
		CheckpointID:       row.WorkspaceSnapshot.Ref,
		ExperimentContext:  experimentContextToProto(row.ExperimentContext),
		TracingContext:     row.TracingContext,
		AgentInterface:     agentInterface,
		MinPlatformVersion: minPlatformVersion,
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
	ResumeMode                string   `json:"resumeMode"`
	WorkspaceLost             bool     `json:"workspaceLost"`
	WorkspaceRecoveryFraction *float64 `json:"workspaceRecoveryFraction,omitempty"`
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
	s.publishEvent(row.TenantID, row.ID, "session.resumed", payload)
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
//
// On a successful bump, the §16.1 lenny_session_retry_total counter is
// incremented with the row's FailureClass as the label (or "unknown"
// when no class is recorded), and the §11.7 / §16.7
// session.retry_attempted audit row is appended. Both side effects are
// best-effort and gated on their respective hooks being wired.
//
// spec: §4.2 line 156 — "incremented on each pod recovery".
// spec: §4.2 line 158 — "Retry counters and policy enforcement".
// spec: §16.1 catalog — lenny_session_retry_total. F-7.3.10.
// spec: §11.7 / §16.7 — session.retry_attempted audit. F-7.3.18.
func (s *Server) bumpRecoveryGeneration(ctx context.Context, tenantID, sessionID, podAssignment string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.RecoveryGeneration++
		row.RetryCount++
		if podAssignment != "" {
			row.PodAssignment = podAssignment
		}
		return nil
	})
	if err != nil {
		log.Printf("sessionserver: bump recovery_generation for session %s: %v", sessionID, err)
		return
	}
	s.recordSessionRetry(ctx, updated)
}

// recordSessionRetry fires the §16.1 lenny_session_retry_total metric
// and the §11.7 / §16.7 session.retry_attempted audit row for one
// retry attempt. Best-effort: a nil hook degrades to a no-op without
// rolling back the row update that triggered it.
//
// spec: §16.1 catalog (F-7.3.10); §11.7 / §16.7 (F-7.3.18).
func (s *Server) recordSessionRetry(ctx context.Context, row sessionstore.Session) {
	failureClass := string(row.FailureClass)
	if failureClass == "" {
		failureClass = "unknown"
	}
	if s.incSessionRetry != nil {
		s.incSessionRetry(failureClass)
	}
	if s.lifecycleAudit != nil {
		s.lifecycleAudit.EmitSessionLifecycle(ctx, SessionLifecycleEvent{
			EventType:    auditSessionRetryAttempted,
			TenantID:     row.TenantID,
			SessionID:    row.ID,
			UserID:       row.UserID,
			RuntimeRef:   row.RuntimeRef,
			State:        string(row.State),
			FailureClass: failureClass,
			At:           s.clock(),
		})
	}
}
