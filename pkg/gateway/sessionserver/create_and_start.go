// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"math"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// CreateAndStartRequest is the §15.1 POST /v1/sessions/start body —
// the convenience surface that bundles create + finalize + start.
type CreateAndStartRequest struct {
	// Inherits the same shape as CreateSessionRequest.
	RuntimeRef       string            `json:"runtimeRef"`
	UserID           string            `json:"userId,omitempty"`
	WorkspacePlan    json.RawMessage   `json:"workspacePlan,omitempty"`
	IsolationProfile isolation.Profile `json:"isolationProfile,omitempty"`
	Environment      string            `json:"environment,omitempty"`

	// Pool is the §14.1 client-requested target pool selector.
	// The combined create-and-start body is the same CreateSessionRequest
	// envelope (§14.1), so the pool pin is honored or rejected identically
	// to the two-step create path: a pin that does not exist, is not backed
	// by the runtime, or is isolation-inconsistent is rejected, and a pin
	// the tenant is not granted is forbidden. spec: §7.1 / §14.1. F-CS2.
	Pool string `json:"pool,omitempty"`

	// CallbackURL is the §15.1 optional completion-notification
	// webhook. It is validated against the §14 SSRF mitigations at
	// admission and rejected with 400 INVALID_CALLBACK_URL on failure.
	// spec: §15.1; §14. F-15.1.11.
	CallbackURL string `json:"callbackUrl,omitempty"`
	// CallbackSecret is the §14 write-only HMAC signing secret for the
	// callback. spec: §14. F-15.1.11.
	CallbackSecret string `json:"callbackSecret,omitempty"`
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
	tenantID := s.resolveTenant(r)

	// spec: §11.1; §10.6; §4.8; §15.2.1 rule 1 — decode the request first so
	// the §11.1 concurrency and admission-rate gates and the §10.6
	// environment-admission gate can read the requested runtimeRef, isolation
	// profile, pool, and environment. The full gate set, including the §4.8
	// PostAuth policy chain, then runs BEFORE buildCreateAndStartRow runs the
	// §4.8 PreRoute/PostRoute interceptor chains and the §10.7 experiment
	// router. This holds both the §11.1 concurrency-and-rate-before-policy
	// ordering and the §4.8 PostAuth-before-PreRoute phase order that the
	// two-step create path holds, and always runs before mintClaimStartPersist
	// claims a pod.
	req, ok := s.decodeCreateAndStartRequest(w, r)
	if !ok {
		return
	}

	if !s.createAndStartGates(w, r, tenantID, req) {
		return
	}

	row, build, ok := s.buildCreateAndStartRow(w, r, tenantID, req)
	if !ok {
		return
	}

	level, uploadToken, ok := s.mintClaimStartPersist(w, r, &row, build)
	if !ok {
		return
	}

	// spec: §7.2 — the create-and-start path lands the session
	// directly in running, so emit status_change(running) for SSE
	// subscribers (e.g. a parent watching a delegated child) on parity
	// with the explicit POST /start transition.
	s.emitStatusChange(row.TenantID, row.ID, row.State)
	s.writeCreateSessionResponse(w, row, level, uploadToken, build.planWarnings)
}

// createAndStartGates runs the §15.1 admission gates on the combined
// create-and-start path over the decoded request: the active-user gate, the
// §12.8 tenant-state gate, the §12.9 tenant data-classification gate, the §11
// session-quota gate, the §11.1 concurrency and admission-rate gates, the §4.8
// PostAuth policy chain, and the §10.6 environment-admission gate. The §11.1
// concurrency and admission-rate gates run before the policy chain and the
// §10.6 environment-admission gate runs after it, in the same relative order
// the two-step create path applies them (createAdmissionGates in create.go),
// so an over-limit create reserves no rate or token budget. The gates read the
// requested runtimeRef, isolation profile, pool, and environment straight from
// the decoded request, so this whole set runs BEFORE buildCreateAndStartRow
// runs the §4.8 PreRoute/PostRoute interceptor chains and the §10.7 experiment
// router, preserving the §4.8 PostAuth-before-PreRoute phase order (spec: §4.8
// PreAuth → PostAuth → PreRoute → PostRoute). Each gate writes its own §15.1
// error envelope and returns false; true means the request may proceed to the
// row build and pod claim.
// spec: §15.1, §12.8, §12.9, §11, §11.1, §10.6, §4.8, §15.2.1 rule 1.
func (s *Server) createAndStartGates(w http.ResponseWriter, r *http.Request, tenantID string, req CreateAndStartRequest) bool {
	if !s.requireActiveUser(w, r) {
		return false
	}
	// spec: §12.8 — a tenant that has left the `active`
	// TenantState (disabling/deleting/deleted) rejects new session
	// creation before any other admission work.
	if !s.requireTenantState(w, r, tenantID) {
		return false
	}
	// spec: §12.9 — the gateway policy engine validates tenant
	// data classification before any pool/credential work, so a
	// misconfigured workspaceTier fails the create up front.
	if !s.requireTenantClassification(w, r, tenantID) {
		return false
	}
	if !s.requireSessionQuota(w, r, tenantID) {
		return false
	}
	// spec: §11.1 — global, per-user, and per-runtime concurrent-session
	// admission caps. Enforced before the admission-rate and policy gates so an
	// over-limit create consumes no rate budget and reserves no token budget,
	// matching the two-step create path. The caller's subject is the per-user
	// scope key; an unauthenticated principal leaves the per-user scope inert.
	concUser := ""
	if p, ok := getPrincipal(r); ok {
		concUser = p.Subject
	}
	if !s.requireConcurrencyLimits(w, r, tenantID, concUser, req.RuntimeRef) {
		return false
	}
	// spec: §11.1 — per-runtime and per-pool requests-per-minute
	// admission limits, enforced before the §4.8 policy chain so an over-limit
	// create never reserves token budget. The requested isolation profile
	// (defaulted when omitted) resolves the pool, and the client-pinned pool
	// keys the per-pool scope, matching the two-step create path.
	rlProfile := req.IsolationProfile
	if rlProfile == "" {
		rlProfile = s.defaultIsoProf
	}
	if !s.requireAdmissionRateLimit(w, r, tenantID, req.RuntimeRef, rlProfile, req.Pool) {
		return false
	}
	if !s.requirePolicyChain(w, r, tenantID) {
		return false
	}
	// spec: §11.1 / §10.6 — a create-and-start that names no
	// environment is admitted only when the caller belongs to at least one
	// environment or the tenant's noEnvironmentPolicy resolves to allow-all;
	// the platform default deny-all rejects with 403, closing the fail-open
	// the create-and-start path had before it ran this gate.
	return s.requireEnvironmentAdmission(w, r, req.Environment, req.RuntimeRef)
}

// createAndStartBuild carries the per-create resolution
// buildCreateAndStartRow produced that mintClaimStartPersist and the
// response render consume: the pool-derived isolation level, the parsed
// workspace plan the same-call claim/start materialize, and the §14
// plan-parse warnings the response echoes and the parse-warning publish
// emits.
type createAndStartBuild struct {
	level        SessionIsolationLevel
	parsedPlan   workspaceplan.Plan
	planWarnings []workspaceplan.Warning
}

// decodeCreateAndStartRequest decodes the §15.1 POST /v1/sessions/start body
// and enforces the runtimeRef required-field check, resolving the request
// fields the §11.1 concurrency/admission-rate and §10.6 environment-admission
// gates read (runtimeRef, isolation profile, pool, environment) before those
// gates run. It writes the §15.1 error envelope and returns ok=false on a
// malformed body or a missing runtimeRef. Splitting the decode from the row
// build lets createAndStartGates run the §4.8 PostAuth policy chain before
// buildCreateAndStartRow runs the §4.8 PreRoute/PostRoute chains, preserving
// the §4.8 phase order. spec: §15.1, §11.1, §10.6, §4.8.
func (s *Server) decodeCreateAndStartRequest(w http.ResponseWriter, r *http.Request) (CreateAndStartRequest, bool) {
	var req CreateAndStartRequest
	body := jsonReader(w, r)
	defer body.Close()
	if err := json.NewDecoder(body).Decode(&req); err != nil {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "request body is not valid JSON", nil)
		return CreateAndStartRequest{}, false
	}
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required",
			map[string]any{"field": "runtimeRef"})
		return CreateAndStartRequest{}, false
	}
	return req, true
}

// buildCreateAndStartRow runs the §27.4 playground-visibility, §5.3
// isolation-profile, §7.1/§14.1 pool-selectable, §14 workspace-plan, §7.5
// setup-command, and §4.8 PreRoute/PostRoute validation over the decoded
// request, resolving the §7.1 isolation level and assembling the StateRunning
// session row (the client-pinned pool, the §15.1 callback, retention/resume
// deadlines, §27.6 playground caps, the §10.7 experiment variant, and the
// runtime-hint route rewrites). The §4.8 PreRoute/PostRoute interceptor chains
// and the §10.7 experiment router run here, after createAndStartGates has run
// the §4.8 PostAuth policy chain, so the §4.8 PostAuth-before-PreRoute phase
// order holds. It returns the built row and the createAndStartBuild the persist
// and response stages consume, writing the §15.1 error envelope and returning
// ok=false on any rejection. spec: §27.4, §5.3, §7.1, §14, §7.5, §4.8, §10.7.
func (s *Server) buildCreateAndStartRow(w http.ResponseWriter, r *http.Request, tenantID string, req CreateAndStartRequest) (sessionstore.Session, createAndStartBuild, bool) {
	// spec: §27.5 / §27.9 — parity with handleCreate: an
	// origin=playground caller may only create against a playground-visible
	// runtime, so the create-and-start ingress enforces the same §27.4
	// allowedRuntimes boundary. F-27.4.1.
	if !s.requirePlaygroundRuntimeVisible(w, r, req.RuntimeRef) {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}

	isoProf := req.IsolationProfile
	if isoProf == "" {
		isoProf = s.defaultIsoProf
	}
	if !isolation.IsValid(isoProf) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			"isolationProfile is not a recognised §5.3 profile",
			map[string]any{"fields": []map[string]string{{"field": "isolationProfile"}}})
		return sessionstore.Session{}, createAndStartBuild{}, false
	}

	// spec: §7.1 step 1, §7.1 step 4, §14.1 — honor or reject the
	// client-pinned pool on the combined create-and-start path on parity
	// with the two-step create path's validateRequestEnvelope gate. Runs
	// before the claim so an unsatisfiable, unauthorized, or
	// isolation-inconsistent pin fails fast before any pod is claimed.
	// F-CS2 (0018).
	//
	// spec: §7.1 — pass the client's raw request profile
	// (empty when omitted), not the defaulted isoProf, so a pinned pool's own
	// profile governs the session's isolation and only an explicitly-requested
	// inconsistent profile is rejected. See validateRequestEnvelope for the
	// matching two-step-path rationale. F-CS2 (0018).
	if !s.requirePoolSelectable(w, r, tenantID, req.RuntimeRef, string(req.IsolationProfile), req.Pool) {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}

	parsedPlan, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}
	// spec: §7.5 / §5.1 — runtime setupCommandPolicy.maxCommands
	// cap, enforced at the create-and-start ingress for parity with the
	// two-step create path. F-7.5.5.
	if !s.enforceSetupCommandPolicy(w, r, req.RuntimeRef, parsedPlan) {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}

	// §4.8 PreRoute (below the ExperimentRouter pivot): run the PreRoute
	// interceptors with priority < 300 over the TaskSpec after
	// authentication and before runtime selection. A REJECT blocks the
	// create; a MODIFY may rewrite runtime hints (the requested runtime)
	// but not the authenticated identity, which the chain enforces. This
	// segment runs before routeExperiment so a priority 101–299 MODIFY of
	// the runtime hint affects which variant the ExperimentRouter assigns
	// (spec: §4.8).
	preRoute, ok := s.runRouteChainRange(w, r, interceptor.PhasePreRoute, routeTaskSpec{
		TenantID:         tenantID,
		UserID:           req.UserID,
		RequestedRuntime: req.RuntimeRef,
	}, math.MinInt32, ExperimentRouterPriority)
	if !ok {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}
	runtimeRef := req.RuntimeRef
	if preRoute.RequestedRuntime != "" {
		runtimeRef = preRoute.RequestedRuntime
	}

	// spec: §7.1 — resolve the pool-derived isolation level
	// once so executionMode + scrubPolicy can be persisted on the row
	// (same path as the two-step create flow). GET / List read the
	// persisted values via toResponse so the rich envelope survives a
	// coordinator handoff.
	//
	// spec: §7.1 — when the client pins a pool and omits
	// isolationProfile, the named pool's own profile governs, so resolve the
	// level against the pool (effective requested profile empty) and persist
	// the pool-derived profile on the row so the same-call claim
	// (claimAtCreate re-resolves from row.IsolationProfile) is consistent
	// with the pin. F-CS2 (0018).
	effProf := effectiveRequestedProfile(req.IsolationProfile, isoProf, req.Pool)
	level := s.resolveIsolationLevel(r.Context(), runtimeRef, effProf, req.Pool)
	row := sessionstore.Session{
		ID:               s.idFn(),
		TenantID:         tenantID,
		UserID:           req.UserID,
		RuntimeRef:       runtimeRef,
		Environment:      req.Environment,
		State:            session.StateRunning, // skip directly to running per §15.1
		IsolationProfile: persistedRowProfile(level, isoProf),
		// spec: §14.1 — persist the client-pinned pool so the
		// same-call claim constrains resolution to it and a client that lost
		// the response can recover its requested pool. F-CS2 (0018).
		Pool:                   req.Pool,
		ExecutionMode:          level.ExecutionMode,
		ScrubPolicy:            level.ScrubPolicy,
		ConversationContinuity: level.ConversationContinuity,
		WorkspacePlan:          planJSON,
		CreatedAt:              s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// spec: §15.1 / §14 — validate the optional
	// completion-notification callbackUrl against the SSRF mitigations and
	// seal the callbackSecret before any pod side effects. F-15.1.11.
	if !s.validateCallback(w, r, req.CallbackURL, req.CallbackSecret, tenantID, &row) {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}
	// spec: §7.1 / §12.9 — stamp the tier-keyed default
	// artifact-retention deadline at create (mirrors the plain create path)
	// so the GC can reclaim this session's artifacts; the terminal
	// transition rolls it forward.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.retentionForTier(r.Context(), tenantID, req.Environment))
	// spec: §4.2 — stamp the resume-eligibility deadline so the
	// row reaching `running` carries the same per-session resume window
	// as the two-step `POST /v1/sessions` + `POST /start` path.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
	// spec: §27.3 / §27.6 — apply the playground idle /
	// duration caps + origin=playground label for a /playground/*-originated
	// create-and-start, on parity with the two-step create path. F-27.3.3 /
	// F-27.6.1 / F-27.6.2 / F-27.6.8.
	s.applyPlaygroundCaps(r.Context(), runtimeRef, &row)
	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}
	// §4.8 PreRoute (at or above the ExperimentRouter pivot): run the
	// PreRoute interceptors with priority ≥ 300 after experiment routing,
	// so an external interceptor at priority ≥ 300 orders after the
	// ExperimentRouter built-in per the §4.8 line-12 ascending-priority
	// rule. It sees the experiment-assigned runtime as the requested
	// runtime; a runtime-hint MODIFY here gets the final say on the
	// runtime before runtime selection completes, with the isolation
	// level re-resolved so executionMode/scrubPolicy stay consistent.
	afterRoute, ok := s.runRouteChainRange(w, r, interceptor.PhasePreRoute, routeTaskSpec{
		TenantID:         tenantID,
		UserID:           req.UserID,
		RequestedRuntime: row.RuntimeRef,
	}, ExperimentRouterPriority, math.MaxInt32)
	if !ok {
		return sessionstore.Session{}, createAndStartBuild{}, false
	}
	if afterRoute.RequestedRuntime != "" && afterRoute.RequestedRuntime != row.RuntimeRef {
		row.RuntimeRef = afterRoute.RequestedRuntime
		// spec: §7.1 — re-resolve against the effective
		// requested profile so a pinned pool's own profile still governs after
		// a runtime-hint MODIFY. F-CS2 (0018).
		afterLevel := s.resolveIsolationLevel(r.Context(), row.RuntimeRef, effProf, req.Pool)
		row.ExecutionMode = afterLevel.ExecutionMode
		row.ScrubPolicy = afterLevel.ScrubPolicy
		row.ConversationContinuity = afterLevel.ConversationContinuity
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
		return sessionstore.Session{}, createAndStartBuild{}, false
	}

	return row, createAndStartBuild{level: level, parsedPlan: parsedPlan, planWarnings: planWarnings}, true
}

// mintClaimStartPersist runs the §7.1 atomic create-and-start
// unit: the §7.1 step 8 uploadToken mint, the same-call §7.1
// Claim → Prepare → Launch sequence on the pod binder, the store INSERT,
// and the post-persist registration (lease tree, binding, parse-warning
// publish). A mint, claim, prepare, launch, or persist failure leaves no
// row behind and reclaims the pod and any credential lease, and it returns
// the resolved isolation level and minted uploadToken the response echoes.
// spec: §7.1, §6.2, §8.6, §14.
func (s *Server) mintClaimStartPersist(w http.ResponseWriter, r *http.Request, row *sessionstore.Session, build createAndStartBuild) (SessionIsolationLevel, string, bool) {
	level := build.level

	// spec: §7.1 — atomicity. Mint the §7.1 step 8 uploadToken
	// and run the pod claim BEFORE the row is persisted: a failure in any
	// step of the create-and-start atomic unit (mint, pre-claim,
	// claim, bind) returns `SESSION_CREATION_FAILED` to the client with
	// no session row left behind, matching the §7.1 "does NOT persist
	// the session row" contract. On store.Create failure the bound pod
	// is released to the §6.2 reclaim path so no pod or credential
	// lease leaks past the failure.
	// spec: §7.1 — TTL = maxCreatedStateTimeoutSeconds. F-7.4.7.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return level, "", false
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	// When the gateway is wired with a pod binder, the §15.1
	// create-and-start path runs the §7.1 Claim → Prepare → Launch sequence
	// in one call, reusing the same phases the decomposed create → finalize
	// → start lifecycle drives (§4.7 of the proposal). claimAtCreate runs
	// the credential pre-check and claims the pod (persisting the §4.6
	// binding on the row); startOnPod then sees that binding and runs the
	// prepare barrier plus the launch against it without re-claiming. A
	// claim, prepare, or launch failure leaves no row behind per the §7.1 atomicity contract; the binder reclaims the pod (and any
	// lease) on the prepare/launch failure path. A Token Service outage
	// during credential assignment surfaces as TOKEN_SERVICE_UNAVAILABLE
	// with Retry-After per §4.3.
	var (
		bound       *podsession.BindResult
		createClaim *podsession.ClaimResult
	)
	if s.podBinder != nil {
		outcome, err := s.claimAtCreate(r.Context(), *row, build.parsedPlan, claimRouteStart)
		if err != nil {
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return level, "", false
		}
		level = outcome.Level
		row.ExecutionMode = level.ExecutionMode
		row.ScrubPolicy = level.ScrubPolicy
		row.ConversationContinuity = level.ConversationContinuity
		// spec: §4.1 / §5 (proposal) — carry the create-time credential
		// resolution and pod_claim duration into the same-call startOnPod so
		// the combined path runs the §7.1-step-3 pre-check exactly once before
		// the step-4 claim (no re-resolve at the prepare dispatch) and the
		// single end-to-end startup observation spans pod claim through ready.
		startCtx := &startContext{
			CredPools:         outcome.CredPools,
			PoolDeliveryModes: outcome.PoolDeliveryModes,
			UserCredProviders: outcome.UserCredProviders,
		}
		if outcome.Claim != nil {
			createClaim = outcome.Claim
			row.PodAssignment = createClaim.SandboxName
			row.PoolRef = createClaim.Pool
			startCtx.PodClaim = createClaim.PodClaim
		}
		result, err := s.startOnPod(r.Context(), *row, build.parsedPlan, startCtx)
		if err != nil {
			// spec: §7.1 — a create-step failure rolls back without
			// persisting the row, releasing the create-time claim. The
			// combined path claims at /create and then runs prepare/launch
			// against that claim in the same call, so on a startOnPod error
			// the create-time claim is released here unless the binder already
			// owns its release (see createClaimNeedsRollback).
			if createClaimNeedsRollback(createClaim, err) {
				s.rollbackClaim(r.Context(), createClaim, row.ID)
			}
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return level, "", false
		}
		bound = result
		if bound != nil {
			row.PodAssignment = bound.SandboxName
			row.SetupOutput = setupOutputsFromBind(bound.SetupOutputs)
		}
	}

	if bound != nil {
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the single-call /start commits
		// the row to `running` with pod_assignment through store.Create
		// below, before registerBinding runs, so acquire the coordination
		// lease here, ahead of that commit. Left to registerBinding, a peer
		// sweep firing in the commit-to-registerBinding window would read the
		// committed running-pod row with an unheld lease as adoptable and
		// steal it. ErrHeld is unreachable for a genuinely fresh single-call
		// session whose lease is unheld, but on a raced peer it fails closed:
		// release the bound pod and abort before the commit.
		if err := s.acquireCoordinationLease(r.Context(), row.TenantID, row.ID); err != nil {
			s.rollbackBinding(r.Context(), bound)
			s.writeSessionCreationFailed(w, "coordination_lease_held", err.Error())
			return level, "", false
		}
	}

	if err := s.store.Create(r.Context(), *row); err != nil {
		// spec: §7.1 — persistence failure after the bind must
		// roll back the claimed pod so the gateway does not leak a pod
		// or its credential lease past a "no session_id returned"
		// failure. spec: §4.6.1, §10.1 — the coordination lease was
		// acquired above ahead of this commit, so release it too, or the
		// failed commit strands a held lease with no binding and decouples
		// the lease holder from the binding holder co-location unifies.
		if bound != nil {
			s.releaseCoordinationLease(r.Context(), row.TenantID, row.ID)
		}
		s.rollbackBinding(r.Context(), bound)
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return level, "", false
	}
	s.recordSessionCreated(r.Context(), *row)
	// §8.6: register the root tree's lease-extension budget so a later
	// in-process budget-exhaustion extension (the gateway LLM Proxy's
	// ExtendForBudget trigger) resolves it instead of ErrSessionNotFound.
	// F-15.3.5.
	s.registerLeaseTree(*row)
	// The coordination lease was already acquired above, ahead of the
	// running-commit, so the binding publishes unconditionally here rather
	// than routing back through registerBinding's self-renew acquire. Gating
	// the publish on a second acquire would let a transient leaseStore error
	// skip the podRegistry.Put while the row is already committed to running
	// and this replica holds the lease, stranding a committed running session
	// with a held lease but no binding, the lease-without-binding decoupling
	// co-location removes. spec: §4.6.1, §10.1.
	stampBindingGeneration(bound, createdRowGeneration(row.CoordinationGeneration))
	s.publishBinding(r.Context(), bound)
	// spec: §14 — publish parse-time
	// `workspace_plan_unknown_source_type` / `workspace_plan_path_collision`
	// warnings on the per-session SSE bus so Ops/audit subscribers see
	// them asynchronously, parity with the two-step create path.
	// F-14.1.17.
	s.publishParsePlanWarnings(row.TenantID, row.ID, build.planWarnings)
	return level, tok, true
}
