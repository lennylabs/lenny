// SPDX-License-Identifier: MIT

package sessionserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel/trace"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/observability/tracing"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// createSession runs the §15.1 session-creation flow over an
// already-decoded request. It is decomposed (proposal 0020 R12) into the
// admission gates, the row validation and build, the §7.1 atomic
// claim/mint/persist unit, and the CreateSessionResponse render, so the
// top-level method reads as that pipeline. The CreateSessionRequest is
// decoded by the caller (handleCreate / handleEnvironmentSessions).
func (s *Server) createSession(w http.ResponseWriter, r *http.Request, req CreateSessionRequest) {
	// spec: §16.3 — open the gateway-side `session.create` span on
	// the request context so the create flow (quota/admission gates, the
	// store INSERT, the §7.1 uploadToken mint) rides one trace. The tracer
	// resolves the process-global OTel provider; constructing it here keeps
	// the span site self-contained. Downstream store calls inherit the span
	// through the rebound request context. Correlation attributes are
	// projected from the context by Start.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanSessionCreate)
	defer span.End()
	r = r.WithContext(ctx)

	// spec: §10.2 (Session ownership) rules 1 and 3 — the owner is the
	// principal's subject, resolved before any gate, store write, or claim.
	// It is the one value the §12.8 creation fence, the §11.1 per-user
	// concurrency key, and the stored row use.
	owner, ok := s.requireOwner(w, r)
	if !ok {
		return
	}
	tenantID := s.resolveTenant(r)
	if !s.createAdmissionGates(w, r, req, tenantID, owner) {
		return
	}

	row, build, ok := s.validateAndBuildCreateRow(w, r, req, tenantID, owner, span)
	if !ok {
		return
	}

	level, uploadToken, ok := s.claimMintPersist(w, r, &row, build, span)
	if !ok {
		return
	}

	s.writeCreateSessionResponse(w, row, level, uploadToken, build.allWarnings)
}

// createBuild carries the per-create resolution validateAndBuildCreateRow
// produced that claimMintPersist and the response render consume: the
// pool-derived isolation level, the parsed workspace plan claimAtCreate
// materializes, and the aggregated §14 plan/envelope warnings the response
// echoes and the parse-warning publish emits.
type createBuild struct {
	level       SessionIsolationLevel
	parsedPlan  workspaceplan.Plan
	allWarnings []workspaceplan.Warning
}

// createAdmissionGates runs the §15.1 admission gates that precede any row
// construction: the §10.1 dual-store gate, the active-user and §11.1
// quota/concurrency/rate gates, the §4.8 policy chain, the runtimeRef
// required-field check, the §10.6 environment-admission gate, and the
// §27.4 playground-runtime-visibility boundary. Each writes its own §15.1
// error envelope and returns false; true means the request may proceed to
// row validation. owner is the session owner createSession resolved from
// the principal. spec: §15.1, §11.1, §10.1, §10.6, §27.4, §10.2.
func (s *Server) createAdmissionGates(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, tenantID, owner string) bool {
	// spec: §10.1 item 2 — while both Postgres and Redis are unreachable
	// a new session.create cannot complete its Postgres INSERT, so reject
	// it with 503 + Retry-After: 10 before consuming any quota, rate, or
	// token budget. In-progress sessions are unaffected (they continue on
	// cached coordination state); only creation is suspended. F-10.1.3.
	if s.dualStore != nil && s.dualStore.Unavailable() {
		w.Header().Set("Retry-After", "10")
		s.writeError(w, http.StatusServiceUnavailable, "PLATFORM_DEGRADED",
			"session creation is suspended: the platform's coordination stores are temporarily unavailable",
			map[string]any{"reason": "dual_store_unavailable", "retryAfter": 10})
		return false
	}
	if !s.requireActiveUser(w, r) {
		return false
	}
	// spec: §12.8 (Lifecycle fence and drain) — the early creation gate
	// over the tenant row and the new row's owner (the principal's
	// subject), before the quota, concurrency, rate-limit, and policy
	// gates, so a fenced create consumes no rate budget and claims no pod.
	if !s.requireCreationAllowed(w, r, tenantID, owner) {
		return false
	}
	if !s.requireSessionQuota(w, r, tenantID) {
		return false
	}
	// spec: §11.1 — global, per-user, and per-runtime
	// concurrent-session admission caps. Enforced before the rate-limit
	// and policy gates so an over-limit create consumes no rate budget
	// and reserves no token budget. The owner is the per-user scope key.
	// F-11.1.3.
	if !s.requireConcurrencyLimits(w, r, tenantID, owner, req.RuntimeRef) {
		return false
	}
	// spec: §11.1 — per-runtime and per-pool requests-per-minute
	// admission limits. Enforced before the §4.8 policy chain (so an
	// over-limit create never reserves token budget) using the requested
	// isolation profile to resolve the pool. An empty RuntimeRef is left
	// to the required-field check below; an invalid profile resolves to
	// no pool and the per-pool scope is skipped. F-11.1.2.
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
	if req.RuntimeRef == "" {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", "runtimeRef is required", map[string]any{"field": "runtimeRef"})
		return false
	}

	// §11.1 / §10.6 — a session create that names no
	// environment is admitted only when the caller is a member of at
	// least one environment (transparent filter applies) or when the
	// tenant's noEnvironmentPolicy resolves to allow-all. The platform
	// default deny-all rejects with 403 so an empty Environment field
	// no longer bypasses the §10.6 access-path default.
	if !s.requireEnvironmentAdmission(w, r, req.Environment, req.RuntimeRef) {
		return false
	}

	// spec: §27.5 / §27.9 — an origin=playground caller may
	// only create a session against a runtime its playground.allowedRuntimes
	// list exposes. This closes the §27.4 "see and select" gap so the
	// allowedRuntimes filter is an authorization boundary, not just a picker
	// display filter. A non-playground caller is unaffected. F-27.4.1.
	return s.requirePlaygroundRuntimeVisible(w, r, req.RuntimeRef)
}

// validateAndBuildCreateRow runs the §5.3 isolation-profile, §15.1
// pool-draining, §14 workspace-plan, §7.5 setup-command, and §7.3
// retry-policy validation, resolves the §7.1 isolation level, and
// assembles the persisted session row (retention/resume deadlines, §14
// request-envelope fields, §27.6 playground caps, and the §10.7 experiment
// variant). It returns the built row and the createBuild the persist and
// response stages consume, writing the §15.1 error envelope and returning
// ok=false on any rejection. spec: §5.3, §15.1, §14, §7.5, §7.3, §7.1, §10.7.
func (s *Server) validateAndBuildCreateRow(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, tenantID, owner string, span trace.Span) (sessionstore.Session, createBuild, bool) {
	// §5.3 isolation profile: explicit override > §5.3 default. The
	// minimal gateway does not yet resolve pools, so any explicit
	// value is taken at face value (production validates against the
	// resolved pool's profile).
	isoProf := req.IsolationProfile
	if isoProf == "" {
		isoProf = s.defaultIsoProf
	}
	if !isolation.IsValid(isoProf) {
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR",
			fmt.Sprintf("isolationProfile %q is not a recognised §5.3 profile", isoProf),
			map[string]any{"fields": []map[string]string{{"field": "isolationProfile"}}})
		return sessionstore.Session{}, createBuild{}, false
	}

	// spec: §7.1 — when a pool is pinned and the client
	// omits isolationProfile, the named pool's own profile governs, so every
	// pool resolution on this create defers to the pool (effective requested
	// profile empty) rather than the deployment default. F-CS2 (0018).
	effProf := effectiveRequestedProfile(req.IsolationProfile, isoProf, req.Pool)

	// spec: §15.1 — reject a create that would select a pool in
	// the `draining` phase with 503 POOL_DRAINING + Retry-After before any
	// pod claim. The gate resolves the same pool the session would bind
	// to; it is inert in the Postgres-only posture (no pool binding). F-15.1.8.
	if !s.requirePoolNotDraining(w, r, req.RuntimeRef, effProf, req.Pool) {
		return sessionstore.Session{}, createBuild{}, false
	}

	// §14 workspace plan: parse + validate when present. Absent plan
	// is admitted (the session starts with an empty workspace, the
	// minimal gateway uses this for tests that exercise pure
	// state-machine paths). The validated plan is stored on the row so
	// the start handler can materialize it onto the claimed pod and
	// GET /v1/sessions/{id} can return it per §15.1.
	parsedPlan, planJSON, planWarnings, planOK := s.resolveSubmittedPlan(w, r, req.WorkspacePlan, tenantID, "")
	if !planOK {
		return sessionstore.Session{}, createBuild{}, false
	}
	// spec: §7.5 / §5.1 — runtime setupCommandPolicy.maxCommands
	// is a per-session cap the gateway enforces before pod claim so a
	// buggy or malicious client cannot DoS the setup phase. F-7.5.5.
	if !s.enforceSetupCommandPolicy(w, r, req.RuntimeRef, parsedPlan) {
		return sessionstore.Session{}, createBuild{}, false
	}

	// spec: §7.3 — validate the client-supplied retry
	// policy before any side effect, then clamp against the deployer
	// caps so the persisted value is the effective upper bound. A nil
	// input stays nil; a non-nil input always lands on the row with
	// the deployer cap as the floor for unset fields. F-7.3.1.
	if err := session.ValidateRetryPolicy(req.RetryPolicy); err != nil {
		// spec: §16.3 — a malformed retryPolicy is a caller error
		// (PERMANENT: the same request will not validate on retry).
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
		var rpErr *session.RetryPolicyValidationError
		if errors.As(err, &rpErr) {
			s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(),
				map[string]any{"field": "retryPolicy." + rpErr.Field, "reason": rpErr.Reason})
			return sessionstore.Session{}, createBuild{}, false
		}
		s.writeError(w, http.StatusBadRequest, "VALIDATION_ERROR", err.Error(), nil)
		return sessionstore.Session{}, createBuild{}, false
	}
	var effectiveRetry *session.RetryPolicy
	if req.RetryPolicy != nil {
		clamped := session.ClampRetryPolicy(req.RetryPolicy, s.retryPolicyCaps)
		effectiveRetry = &clamped
	}

	// spec: §7.1 — resolve the pool-derived isolation level
	// once at create time so the executionMode / scrubPolicy halves can
	// be persisted on the row alongside isolationProfile. GET / List
	// return the same envelope across the session's lifetime
	// (persistedIsolationLevel in toResponse), so a client that lost
	// the create response or hits a different replica still sees the
	// rich level the pool resolved to.
	// spec: §7.1 — resolve the level against effProf so a
	// pinned pool's own profile governs, and persist the pool-derived profile
	// on the row. The later claim re-resolves from row.IsolationProfile, so
	// persisting the pool's profile keeps the claim consistent with the pin.
	// F-CS2 (0018).
	level := s.resolveIsolationLevel(r.Context(), req.RuntimeRef, effProf, req.Pool)
	row := sessionstore.Session{
		ID:                     s.idFn(),
		TenantID:               tenantID,
		UserID:                 owner, // spec: §10.2 (Session ownership) rule 1
		RuntimeRef:             req.RuntimeRef,
		Environment:            req.Environment,
		State:                  session.StateCreated,
		IsolationProfile:       persistedRowProfile(level, isoProf),
		ExecutionMode:          level.ExecutionMode,
		ScrubPolicy:            level.ScrubPolicy,
		ConversationContinuity: level.ConversationContinuity,
		WorkspacePlan:          planJSON,
		Metadata:               cloneMetadata(req.Metadata),
		RetryPolicy:            effectiveRetry,
		CreatedAt:              s.clock(),
	}
	row.UpdatedAt = row.CreatedAt
	// spec: §4.2 — stamp the resume-eligibility deadline
	// onto the row at create time. The watchdog can then expire a
	// session whose resume window has passed without consulting the
	// global watchdog budget; the per-session window also lets
	// individual sessions override the platform default by adjusting
	// this field on Update.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
	// spec: §7.1 / §12.9 — stamp the tier-keyed default
	// artifact-retention deadline at create so a session that never reaches
	// a terminal state is still eligible for GC (the retention GC treats a
	// zero deadline as ineligible, which would otherwise let the row live
	// forever). The deadline is the §12.9 per-tier default (T4 24h, T2 90d)
	// or the deployer-configured window for T3. The terminal transition
	// rolls this forward to terminal_time + the same window.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.retentionForTier(r.Context(), tenantID, req.Environment))

	// spec: §14 — validate the §14 request-envelope
	// fields (env blocklist, pool, timeouts cap, credentialPolicy
	// restrict-only, delegationLease bounds, runtimeOptions schema) and
	// copy the accepted values onto the row. Rejection writes the §15.1
	// error envelope (400 ENV_VAR_BLOCKLISTED / RUNTIME_OPTIONS_INVALID /
	// VALIDATION_ERROR) and returns. F-14.1.12 / F-14.1.14 / F-14.1.15.
	envWarnings, ok := s.validateRequestEnvelope(w, r, req, tenantID, &row)
	if !ok {
		return sessionstore.Session{}, createBuild{}, false
	}

	// spec: §27.3 / §27.6 — when the caller's session
	// bearer carries the origin=playground claim, stamp the §27.6 idle and
	// duration caps (min-wins over any §14 timeout the client requested) and
	// the origin=playground audit label onto the row before persist. Reads
	// the §14 timeouts validateRequestEnvelope copied above so a tighter
	// client value is preserved. F-27.3.3 / F-27.6.1 / F-27.6.2 / F-27.6.8.
	s.applyPlaygroundCaps(r.Context(), req.RuntimeRef, &row)

	// §10.7: the ExperimentRouter may enroll the session in a variant,
	// rewriting its runtime/pool before the row is persisted. It fails
	// the creation closed when the variant pool is less isolated than
	// the session's profile.
	if !s.routeExperiment(w, r, &row) {
		return sessionstore.Session{}, createBuild{}, false
	}
	// spec: §14.1 WorkspacePlan Schema Versioning (Runtime workspace
	// defaults) — a plan that binds here carries the routed runtime's
	// workspaceDefaults ahead of its own entries. The setupCommandPolicy
	// check above saw only the client's commands (§7.5 Command policy). A
	// create without a plan binds nothing, so its defaults compose at
	// finalize.
	if len(planJSON) > 0 {
		parsedPlan, row.WorkspacePlan, ok = s.composeRuntimeDefaults(w, r, row.RuntimeRef, parsedPlan, planJSON)
		if !ok {
			return sessionstore.Session{}, createBuild{}, false
		}
	}

	// spec: §14 — the §14 plan-parse warnings and the
	// §14 RuntimeOptionsUnschematized envelope warning ride the
	// same per-session SSE bus; aggregate them so the persist stage can
	// publish all three async and the response echoes them. F-14.1.17 /
	// F-14.1.15.
	allWarnings := append(append([]workspaceplan.Warning(nil), planWarnings...), envWarnings...)
	return row, createBuild{level: level, parsedPlan: parsedPlan, allWarnings: allWarnings}, true
}

// claimMintPersist runs the §7.1 atomic create unit: the optional
// synchronous pod claim (steps 3-5), the §7.1 step 8 uploadToken mint
// before persist, the store INSERT, and the post-persist registration
// (lease tree, parse-warning publish). It rolls the create-time claim back
// on a mint or persist failure so no pod leaks past a "no session_id"
// return, and returns the resolved isolation level and the minted
// uploadToken the response echoes.
// spec: §7.1, §8.6, §14.
func (s *Server) claimMintPersist(w http.ResponseWriter, r *http.Request, row *sessionstore.Session, build createBuild, span trace.Span) (SessionIsolationLevel, string, bool) {
	level, createClaim, ok := s.claimForNewRow(w, r, row, build.parsedPlan, build.level, span)
	if !ok {
		return level, "", false
	}

	tok, ok := s.mintUploadToken(w, r, row, createClaim, span)
	if !ok {
		return level, "", false
	}

	if err := s.store.Create(r.Context(), *row); err != nil {
		// spec: §7.1 — persistence failure leaves no row behind;
		// the minted upload token's digest is never referenced because
		// the finalize/upload paths look up the digest off the
		// (non-existent) row. Roll back the create-time pod claim so no pod
		// leaks past the failure, then return SESSION_CREATION_FAILED so the
		// client retries.
		// spec: §16.3 — a store INSERT failure is retryable
		// (TRANSIENT: the client receives a 503 + Retry-After).
		s.rollbackClaim(r.Context(), createClaim, row.ID)
		// spec: §12.8 (Lifecycle fence and drain) — a fence refusal in
		// the insert transaction answers 403 with its code rather than
		// the retryable 503.
		var ref *sessionstore.CreationRefusal
		if errors.As(err, &ref) {
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
			s.writeCreationRefusal(w, ref)
			return level, "", false
		}
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return level, "", false
	}
	s.recordSessionCreated(r.Context(), *row)
	// spec: §14 — each plan warning is an "event"
	// the gateway emits, not just an echo-in-response. Publish the
	// parse-time `workspace_plan_unknown_source_type` and
	// `workspace_plan_path_collision` warnings on the same per-session
	// SSE bus that the materializer's `workspace_plan_strip_components_skip`
	// warnings ride, so Ops/audit consumers see all three async.
	// F-14.1.17. The §14 RuntimeOptionsUnschematized warning the
	// envelope validation raised rides the same plane. F-14.1.15.
	s.publishParsePlanWarnings(row.TenantID, row.ID, build.allWarnings)
	return level, tok, true
}

// claimForNewRow runs the §7.1 step-3 credential availability pre-check and
// the step-4 warm-pod claim for a top-level session row that is not yet
// persisted, and stamps the claimed binding (PodAssignment and PoolRef) and
// the resolved pool's isolation level on the row. Every handler that inserts
// a top-level session in `created` calls it before the insert:
// `POST /v1/sessions`, `POST /v1/sessions/{id}/derive`, and
// `POST /v1/sessions/{id}/replay`. §7.1 derive rule 2 inserts the derived row
// "in the same initial state as a fresh `CreateSession`", and the §15.1
// `created` state row states that a top-level session in `created` holds a
// claimed warm pod. Without the claim, an exclusive pool prepares nothing at
// finalize and the launch-only `/start` reconnects to an empty binding.
//
// The claim surfaces pool exhaustion before the client uploads, and the
// persisted binding lets a later /finalize and /start reconnect to the pod
// across a coordinator handoff (§4.6). A service-mode pool is claimless and
// returns a nil claim. A concurrent-workspace pool reserves a per-session
// slot. A gateway with no pod binder (the Postgres-only posture) claims
// nothing and returns level unchanged.
//
// It returns the level the response reports and the claim the caller
// releases through rollbackClaim when a later step of its atomic unit fails.
// On a pre-check or claim failure it writes the claim-failure envelope
// (SESSION_CREATION_FAILED, CREDENTIAL_POOL_EXHAUSTED,
// USER_CREDENTIAL_NOT_FOUND, or the pool-warming RUNTIME_UNAVAILABLE) and
// returns ok=false. A failed claim holds no pod: the pre-check is claimless,
// and the exclusive Claim and the concurrent ClaimSlot reclaim their own pod
// or slot on failure.
//
// spec: §7.1 (Atomicity of session creation (steps 2–8)); §7.1 (Derive
// session semantics) derive rule 2; §15.1 (REST API) the `created` state
// row; §4.6.
func (s *Server) claimForNewRow(w http.ResponseWriter, r *http.Request, row *sessionstore.Session, plan workspaceplan.Plan, level SessionIsolationLevel, span trace.Span) (SessionIsolationLevel, *podsession.ClaimResult, bool) {
	if s.podBinder == nil {
		return level, nil, true
	}
	outcome, err := s.claimAtCreate(r.Context(), *row, plan, claimRouteCreate)
	if err != nil {
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
			"could not place the session on a warm pod")
		return level, nil, false
	}
	// spec: §7.1 — the returned level reflects the actual resolved pool's
	// profile, tightening the create-response accuracy guarantee.
	level = outcome.Level
	row.ExecutionMode = level.ExecutionMode
	row.ScrubPolicy = level.ScrubPolicy
	row.ConversationContinuity = level.ConversationContinuity
	if outcome.Claim != nil {
		// spec: §4.6 — PodAssignment + PoolRef plus the session id
		// reconstruct the deterministic claim and lease key.
		row.PodAssignment = outcome.Claim.SandboxName
		row.PoolRef = outcome.Claim.Pool
	}
	return level, outcome.Claim, true
}

// mintUploadToken mints the §7.1 step-8 uploadToken for a row that is not
// yet persisted and stamps its digest and expiry on the row, so the insert
// that follows stores them. Every handler that inserts a top-level session in
// `created` calls it after its claim: `POST /v1/sessions`, derive, and replay,
// whose §15.1 response is "identical to `POST /v1/sessions`" and whose new
// session goes through the standard upload → finalize → start lifecycle.
// Minting before the insert keeps §7.1 atomicity: on failure no row exists,
// so the client receives no session_id. The TTL is
// maxCreatedStateTimeoutSeconds, threaded through s.uploadTokenTTL, so the
// token deadline matches the watchdog's created-state deadline (F-7.4.7).
//
// On failure it releases claim (the create-time pod claim of the same atomic
// unit, nil when none was taken), writes 503 SESSION_CREATION_FAILED with
// reason upload_token_issuance_failed, and returns ok=false.
//
// spec: §7.1 (Normal Flow) step 8 and Atomicity of session creation (steps
// 2–8); §7.4 (Upload Safety) Upload authorization; §15.1 (Session Replay
// Semantics).
func (s *Server) mintUploadToken(w http.ResponseWriter, r *http.Request, row *sessionstore.Session, claim *podsession.ClaimResult, span trace.Span) (string, bool) {
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		s.rollbackClaim(r.Context(), claim, row.ID)
		// spec: §16.3 — PERMANENT: a bad session id does not become valid
		// on retry.
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return "", false
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry
	return tok, true
}

// writeCreateSessionResponse renders the §15.1 201 CreateSessionResponse:
// the toResponse envelope with the create-time-resolved isolation level,
// the minted uploadToken, and the aggregated §14 plan/envelope warnings.
// spec: §7.1, §15.1.
func (s *Server) writeCreateSessionResponse(w http.ResponseWriter, row sessionstore.Session, level SessionIsolationLevel, uploadToken string, warnings []workspaceplan.Warning) {
	base := toResponse(row)
	// spec: §7.1 — the pool-resolved level is now persisted on
	// the row, so toResponse returns it on every read. The local
	// resolved-at-admission value still wins over the persisted column
	// for the create response itself (covers a future code path that
	// might write executionMode after the row insert).
	base.SessionIsolationLevel = level
	resp := CreateSessionResponse{
		SessionResponse:       base,
		UploadToken:           uploadToken,
		WorkspacePlanWarnings: warnings,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(resp)
}
