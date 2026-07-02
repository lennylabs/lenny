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
	// spec: §16.3 line 336 — open the gateway-side `session.create` span on
	// the request context so the create flow (quota/admission gates, the
	// store INSERT, the §7.1 uploadToken mint) rides one trace. The tracer
	// resolves the process-global OTel provider; constructing it here keeps
	// the span site self-contained. Downstream store calls inherit the span
	// through the rebound request context. Correlation attributes are
	// projected from the context by Start.
	ctx, span := tracing.NewTracer(nil).Start(r.Context(), tracing.SpanSessionCreate)
	defer span.End()
	r = r.WithContext(ctx)

	tenantID := s.resolveTenant(r)
	if !s.createAdmissionGates(w, r, req, tenantID) {
		return
	}

	row, build, ok := s.validateAndBuildCreateRow(w, r, req, tenantID, span)
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
// row validation. spec: §15.1, §11.1, §10.1, §10.6, §27.4.
func (s *Server) createAdmissionGates(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, tenantID string) bool {
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
	if !s.requireSessionQuota(w, r, tenantID) {
		return false
	}
	// spec: §11.1 line 8 — global, per-user, and per-runtime
	// concurrent-session admission caps. Enforced before the rate-limit
	// and policy gates so an over-limit create consumes no rate budget
	// and reserves no token budget. The caller's subject is the per-user
	// scope key; an unauthenticated principal leaves the per-user scope
	// inert. F-11.1.3.
	concUser := ""
	if p, ok := getPrincipal(r); ok {
		concUser = p.Subject
	}
	if !s.requireConcurrencyLimits(w, r, tenantID, concUser, req.RuntimeRef) {
		return false
	}
	// spec: §11.1 line 7 — per-runtime and per-pool requests-per-minute
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

	// §11.1 line 13 / §10.6 — a session create that names no
	// environment is admitted only when the caller is a member of at
	// least one environment (transparent filter applies) or when the
	// tenant's noEnvironmentPolicy resolves to allow-all. The platform
	// default deny-all rejects with 403 so an empty Environment field
	// no longer bypasses the §10.6 access-path default.
	if !s.requireEnvironmentAdmission(w, r, req.Environment, req.RuntimeRef) {
		return false
	}

	// spec: §27.5 line 190 / §27.9 line 250 — an origin=playground caller may
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
func (s *Server) validateAndBuildCreateRow(w http.ResponseWriter, r *http.Request, req CreateSessionRequest, tenantID string, span trace.Span) (sessionstore.Session, createBuild, bool) {
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

	// spec: §7.1 line 18 / line 75 — when a pool is pinned and the client
	// omits isolationProfile, the named pool's own profile governs, so every
	// pool resolution on this create defers to the pool (effective requested
	// profile empty) rather than the deployment default. F-CS2 (0018).
	effProf := effectiveRequestedProfile(req.IsolationProfile, isoProf, req.Pool)

	// spec: §15.1 line 797 — reject a create that would select a pool in
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
	parsedPlan, planJSON, planWarnings, planOK := s.resolvePlanForCreate(w, r, req.WorkspacePlan)
	if !planOK {
		return sessionstore.Session{}, createBuild{}, false
	}
	// spec: §7.5 line 477 / §5.1 line 76 — runtime setupCommandPolicy.maxCommands
	// is a per-session cap the gateway enforces before pod claim so a
	// buggy or malicious client cannot DoS the setup phase. F-7.5.5.
	if !s.enforceSetupCommandPolicy(w, r, req.RuntimeRef, parsedPlan) {
		return sessionstore.Session{}, createBuild{}, false
	}

	// spec: §7.3 lines 377-393 — validate the client-supplied retry
	// policy before any side effect, then clamp against the deployer
	// caps so the persisted value is the effective upper bound. A nil
	// input stays nil; a non-nil input always lands on the row with
	// the deployer cap as the floor for unset fields. F-7.3.1.
	if err := session.ValidateRetryPolicy(req.RetryPolicy); err != nil {
		// spec: §16.3 line 336 — a malformed retryPolicy is a caller error
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

	// spec: §7.1 line 75 — resolve the pool-derived isolation level
	// once at create time so the executionMode / scrubPolicy halves can
	// be persisted on the row alongside isolationProfile. GET / List
	// return the same envelope across the session's lifetime
	// (persistedIsolationLevel in toResponse), so a client that lost
	// the create response or hits a different replica still sees the
	// rich level the pool resolved to.
	// spec: §7.1 line 18 / line 75 — resolve the level against effProf so a
	// pinned pool's own profile governs, and persist the pool-derived profile
	// on the row. The later claim re-resolves from row.IsolationProfile, so
	// persisting the pool's profile keeps the claim consistent with the pin.
	// F-CS2 (0018).
	level := s.resolveIsolationLevel(r.Context(), req.RuntimeRef, effProf, req.Pool)
	row := sessionstore.Session{
		ID:                     s.idFn(),
		TenantID:               tenantID,
		UserID:                 req.UserID,
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
	// spec: §4.2 line 159 — stamp the resume-eligibility deadline
	// onto the row at create time. The watchdog can then expire a
	// session whose resume window has passed without consulting the
	// global watchdog budget; the per-session window also lets
	// individual sessions override the platform default by adjusting
	// this field on Update.
	row.ResumeEligibleUntil = row.CreatedAt.Add(s.resumeWindow)
	// spec: §7.1 line 77 / §12.9 line 1043 — stamp the tier-keyed default
	// artifact-retention deadline at create so a session that never reaches
	// a terminal state is still eligible for GC (the retention GC treats a
	// zero deadline as ineligible, which would otherwise let the row live
	// forever). The deadline is the §12.9 per-tier default (T4 24h, T2 90d)
	// or the deployer-configured window for T3. The terminal transition
	// rolls this forward to terminal_time + the same window.
	row.RetentionExpiresAt = row.CreatedAt.Add(s.retentionForTier(r.Context(), tenantID, req.Environment))

	// spec: §14 lines 47-79, 154-155 — validate the §14 request-envelope
	// fields (env blocklist, pool, timeouts cap, credentialPolicy
	// restrict-only, delegationLease bounds, runtimeOptions schema) and
	// copy the accepted values onto the row. Rejection writes the §15.1
	// error envelope (400 ENV_VAR_BLOCKLISTED / RUNTIME_OPTIONS_INVALID /
	// VALIDATION_ERROR) and returns. F-14.1.12 / F-14.1.14 / F-14.1.15.
	envWarnings, ok := s.validateRequestEnvelope(w, r, req, tenantID, &row)
	if !ok {
		return sessionstore.Session{}, createBuild{}, false
	}

	// spec: §27.3 line 63 / §27.6 lines 200-203 — when the caller's session
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

	// spec: §14 lines 100, 334, 338 — the §14 plan-parse warnings and the
	// §14 line 155 RuntimeOptionsUnschematized envelope warning ride the
	// same per-session SSE bus; aggregate them so the persist stage can
	// publish all three async and the response echoes them. F-14.1.17 /
	// F-14.1.15.
	allWarnings := append(append([]workspaceplan.Warning(nil), planWarnings...), envWarnings...)
	return row, createBuild{level: level, parsedPlan: parsedPlan, allWarnings: allWarnings}, true
}

// claimMintPersist runs the §7.1 line 28 atomic create unit: the optional
// synchronous pod claim (steps 3-5), the §7.1 step 8 uploadToken mint
// before persist, the store INSERT, and the post-persist registration
// (lease tree, parse-warning publish). It rolls the create-time claim back
// on a mint or persist failure so no pod leaks past a "no session_id"
// return, and returns the resolved isolation level and the minted
// uploadToken the response echoes.
// spec: §7.1 (atomicity, lines 28/58/75), §8.6, §14.
func (s *Server) claimMintPersist(w http.ResponseWriter, r *http.Request, row *sessionstore.Session, build createBuild, span trace.Span) (SessionIsolationLevel, string, bool) {
	level := build.level

	// spec: §7.1 steps 3-5 — when the gateway is wired with a pod binder,
	// the create atomic unit runs the credential availability pre-check
	// (step 3) and claims an idle warm pod (step 4) synchronously, before
	// the row persist (step 5). The claim surfaces pool exhaustion
	// immediately so the client learns of it before uploading, and the
	// claimed pod's binding (PodAssignment + PoolRef) is persisted on the
	// row so a later /finalize and /start reconnect to it (§4.6). A claim
	// failure leaves no row behind per the §7.1 line 28 atomicity contract.
	// A service-mode pool is claimless (a nil claim); a concurrent-workspace
	// pool claims a per-session slot at create like every non-service-mode
	// pool (claimAtCreate returns the reserved slot's binding), so the §15.1
	// created-state pod-claim invariant holds uniformly.
	var createClaim *podsession.ClaimResult
	if s.podBinder != nil {
		outcome, err := s.claimAtCreate(r.Context(), *row, build.parsedPlan)
		if err != nil {
			// spec: §7.1 line 28 — the pre-check or claim failed before any
			// row was persisted; surface SESSION_CREATION_FAILED (or the
			// credential / pool-warming envelope) with no session_id. No pod
			// is held: the pre-check is claimless, and the exclusive Claim and
			// the concurrent ClaimSlot reclaim their own pod/slot on failure.
			tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
			s.writePodClaimError(w, err, "SESSION_CREATION_FAILED",
				"could not place the session on a warm pod")
			return level, "", false
		}
		// spec: §7.1 line 75 — the returned level reflects the actual resolved
		// pool's profile, tightening the create-response accuracy guarantee.
		level = outcome.Level
		row.ExecutionMode = level.ExecutionMode
		row.ScrubPolicy = level.ScrubPolicy
		row.ConversationContinuity = level.ConversationContinuity
		if outcome.Claim != nil {
			createClaim = outcome.Claim
			// spec: §4.6 (proposal) — persist the durable binding so the claim
			// survives a coordinator handoff during the create → finalize →
			// start window; PodAssignment + PoolRef plus the session id
			// reconstruct the deterministic claim and lease key.
			row.PodAssignment = createClaim.SandboxName
			row.PoolRef = createClaim.Pool
		}
	}

	// spec: §7.1 line 28 — atomicity. Mint the §7.1 step 8 uploadToken
	// BEFORE the row is persisted: on failure no session row exists, so
	// the client receives no session_id (matching the "does NOT persist
	// the session row" rule). The token's digest + expiry are stamped
	// directly on the row that will be persisted, replacing the legacy
	// "Create then Update with digest" sequence that left an orphan
	// `created`-state row when the mint failed.
	// spec: §7.1 line 58 — TTL = maxCreatedStateTimeoutSeconds; the
	// gateway threads the configured value through s.uploadTokenTTL so
	// the token deadline matches the watchdog's MaxCreatedSeconds and the
	// createdsweeper's Timeout. F-7.4.7.
	tok, parsed, err := s.uploadIssuer.IssueDetailed(row.ID, s.uploadTokenTTL)
	if err != nil {
		// spec: §7.1 line 28 — the mint failed before the row persist, so roll
		// back the create-time pod claim rather than leak it past a "no
		// session_id returned" failure.
		s.rollbackClaim(r.Context(), createClaim, row.ID)
		// spec: §16.3 line 336 — the uploadToken mint failed before any row
		// was persisted; record it on the create span (PERMANENT: a bad
		// session id does not become valid on retry).
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryPermanent))
		s.writeSessionCreationFailed(w, "upload_token_issuance_failed",
			"upload token issuance failed: "+err.Error())
		return level, "", false
	}
	row.UploadTokenDigest = parsed.Digest
	row.UploadTokenExpiry = parsed.Expiry

	if err := s.store.Create(r.Context(), *row); err != nil {
		// spec: §7.1 line 28 — persistence failure leaves no row behind;
		// the minted upload token's digest is never referenced because
		// the finalize/upload paths look up the digest off the
		// (non-existent) row. Roll back the create-time pod claim so no pod
		// leaks past the failure, then return SESSION_CREATION_FAILED so the
		// client retries.
		// spec: §16.3 line 336 — a store INSERT failure is retryable
		// (TRANSIENT: the client receives a 503 + Retry-After).
		s.rollbackClaim(r.Context(), createClaim, row.ID)
		tracing.RecordError(span, tracing.CategorizeError(err, tracing.CategoryTransient))
		s.writeSessionCreationFailed(w, "row_persistence_failed", err.Error())
		return level, "", false
	}
	s.recordSessionCreated(r.Context(), *row)
	// §8.6: register the root tree's lease-extension budget so a later
	// in-process budget-exhaustion extension (the gateway LLM Proxy's
	// ExtendForBudget trigger) resolves it instead of ErrSessionNotFound.
	// F-15.3.5.
	s.registerLeaseTree(*row)
	// spec: §14 lines 100, 334, 338 — each plan warning is an "event"
	// the gateway emits, not just an echo-in-response. Publish the
	// parse-time `workspace_plan_unknown_source_type` and
	// `workspace_plan_path_collision` warnings on the same per-session
	// SSE bus that the materializer's `workspace_plan_strip_components_skip`
	// warnings ride, so Ops/audit consumers see all three async.
	// F-14.1.17. The §14 line 155 RuntimeOptionsUnschematized warning the
	// envelope validation raised rides the same plane. F-14.1.15.
	s.publishParsePlanWarnings(row.TenantID, row.ID, build.allWarnings)
	return level, tok, true
}

// writeCreateSessionResponse renders the §15.1 201 CreateSessionResponse:
// the toResponse envelope with the create-time-resolved isolation level,
// the minted uploadToken, and the aggregated §14 plan/envelope warnings.
// spec: §7.1 line 75, §15.1.
func (s *Server) writeCreateSessionResponse(w http.ResponseWriter, row sessionstore.Session, level SessionIsolationLevel, uploadToken string, warnings []workspaceplan.Warning) {
	base := toResponse(row)
	// spec: §7.1 line 75 — the pool-resolved level is now persisted on
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
