// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/tenantstore"
)

// requireSessionQuota enforces the §11.2 per-tenant concurrent-session
// quota on the session-creation path. When the tenant already holds
// MaxConcurrentSessions non-terminal sessions, the create is rejected
// with 429 QUOTA_EXCEEDED.
//
// A zero limit, an unknown tenant, or an unwired tenant registry means
// the tenant has no concurrent-session limit. requireSessionQuota
// returns true when the create may proceed; when it returns false it
// has already written the response.
func (s *Server) requireSessionQuota(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.tenants == nil {
		return true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session quota check failed: "+err.Error(), nil)
		return false
	}
	if tenant.MaxConcurrentSessions <= 0 {
		return true
	}
	// §11.2: count the tenant's live sessions with an indexed COUNT
	// rather than materializing every historical row in Go.
	active, err := s.store.CountActiveSessions(r.Context(), tenantID)
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session quota check failed: "+err.Error(), nil)
		return false
	}
	if active >= tenant.MaxConcurrentSessions {
		s.writeError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED",
			"the tenant has reached its concurrent-session limit",
			map[string]any{
				"limit":  tenant.MaxConcurrentSessions,
				"active": active,
			})
		return false
	}
	return true
}

// requireTenantClassification implements the §12.9 line 1048 requirement
// that the gateway policy engine validate a tenant's data-classification
// configuration at session creation. A tenant whose workspaceTier is not
// a recognized §12.9 tier (a stale value left over from a direct database
// write or a pre-validation bootstrap) rejects the create with 422
// CLASSIFICATION_CONTROL_VIOLATION rather than admitting a session that
// would defer the violation to a runtime write the happy path may never
// reach.
//
// An unwired tenant registry or an unknown tenant means there is no
// classification to validate, so the create proceeds (the §10.2
// tenant-claim path governs unknown tenants). requireTenantClassification
// returns true when the create may proceed; when it returns false it has
// already written the response. spec: §12.9 line 1048; §15.1 line 1078.
func (s *Server) requireTenantClassification(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.tenants == nil {
		return true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"tenant classification check failed: "+err.Error(), nil)
		return false
	}
	if cerr := policy.ValidateTenantClassification(tenant); cerr != nil {
		var ce *policy.ClassificationError
		errors.As(cerr, &ce)
		s.writeError(w, http.StatusUnprocessableEntity, "CLASSIFICATION_CONTROL_VIOLATION",
			"the tenant's workspaceTier is not a recognized §12.9 data-classification tier; "+
				"session creation is blocked until the classification configuration is corrected",
			map[string]any{"tenantId": ce.TenantID, "tier": ce.Tier, "reason": ce.Reason})
		return false
	}
	return true
}

// requireTenantState implements the §12.8 phase-table requirement that
// the gateway reject new session creation once a tenant leaves the
// `active` TenantState. A tenant in `disabling`, `deleting`, or
// `deleted` state rejects the create with 403 TENANT_NOT_ACTIVE; the
// deletion controller advances the state, so the gate is dormant until
// a tenant deletion is in flight. An unwired registry or an unknown
// tenant means there is no state to consult, so the create proceeds (the
// §10.2 tenant-claim path governs unknown tenants); a soft-deleted
// tenant is already rejected upstream as TENANT_NOT_FOUND.
// requireTenantState returns true when the create may proceed; when it
// returns false it has already written the response. spec: §12.8 lines
// 865-873.
func (s *Server) requireTenantState(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.tenants == nil {
		return true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"tenant state check failed: "+err.Error(), nil)
		return false
	}
	// spec: §15.1 line 818 — a suspended tenant rejects new session
	// creation with TENANT_SUSPENDED. The check precedes the §12.8
	// deletion-lifecycle gate so an operator suspension is reported as
	// suspension rather than as the deletion-lifecycle TENANT_NOT_ACTIVE.
	if tenant.IsSuspended() {
		s.writeError(w, http.StatusForbidden, "TENANT_SUSPENDED",
			"the tenant is suspended and is not accepting new sessions",
			map[string]any{"tenantId": tenantID})
		return false
	}
	if !tenant.AcceptsNewWork() {
		s.writeError(w, http.StatusForbidden, "TENANT_NOT_ACTIVE",
			"the tenant is being disabled or deleted and is not accepting new sessions",
			map[string]any{"tenantId": tenantID, "state": tenant.State})
		return false
	}
	return true
}

// requireTenantNotSuspended rejects a §15.1 message injection against a
// suspended tenant with 403 TENANT_SUSPENDED. It returns true when the
// call may proceed; when it returns false it has already written the
// response. An unwired registry or an unknown tenant means there is no
// suspension to consult, so the call proceeds. spec: §15.1 line 818.
func (s *Server) requireTenantNotSuspended(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.tenants == nil {
		return true
	}
	tenant, err := s.tenants.Get(r.Context(), tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return true
	}
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"tenant state check failed: "+err.Error(), nil)
		return false
	}
	if tenant.IsSuspended() {
		s.writeError(w, http.StatusForbidden, "TENANT_SUSPENDED",
			"the tenant is suspended; message injection is rejected",
			map[string]any{"tenantId": tenantID})
		return false
	}
	return true
}

// requireConcurrencyLimits enforces the §11.1 line 8 concurrent-session
// admission caps for the global, per-user, and per-runtime scopes (the
// per-tenant scope is enforced by requireSessionQuota against the tenant
// record, and the per-team scope is a §14 user-defined session label
// with no admin-settable limit surface in the spec). Each scope is
// checked only when its cap is configured (> 0). The scopes are
// evaluated narrowest-first (per-user, per-runtime, global) so the
// rejection names the tightest binding limit.
//
// A non-terminal session at or above a scope's cap rejects the create
// with 429 QUOTA_EXCEEDED — the same code and status the per-tenant
// concurrent-session cap returns — carrying the breached scope, its
// limit, and the live count. A counter error fails the request closed
// with 500 (an unenforceable admission cap must not silently admit).
//
// requireConcurrencyLimits returns true when the create may proceed;
// when it returns false it has already written the response.
//
// spec: §11.1 line 8 (Concurrency limits — global, per-user,
// per-runtime). F-11.1.3.
func (s *Server) requireConcurrencyLimits(w http.ResponseWriter, r *http.Request, tenantID, userID, runtimeRef string) bool {
	if s.maxConcSessPerUser > 0 && userID != "" {
		active, err := s.store.CountActiveSessionsByUser(r.Context(), tenantID, userID)
		if !s.admitConcurrencyScope(w, "user", s.maxConcSessPerUser, active, err) {
			return false
		}
	}
	if s.maxConcSessPerRuntime > 0 && runtimeRef != "" {
		active, err := s.store.CountActiveSessionsByRuntime(r.Context(), tenantID, runtimeRef)
		if !s.admitConcurrencyScope(w, "runtime", s.maxConcSessPerRuntime, active, err) {
			return false
		}
	}
	if s.maxConcSessGlobal > 0 {
		active, err := s.store.CountActiveSessionsGlobal(r.Context())
		if !s.admitConcurrencyScope(w, "global", s.maxConcSessGlobal, active, err) {
			return false
		}
	}
	return true
}

// admitConcurrencyScope applies one §11.1 concurrent-session scope
// decision. A counter error fails closed with 500; an active count at
// or above the cap rejects with 429 QUOTA_EXCEEDED. It returns true when
// the scope admits the create. spec: §11.1 line 8. F-11.1.3.
func (s *Server) admitConcurrencyScope(w http.ResponseWriter, scope string, limit, active int, err error) bool {
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session limit check failed: "+err.Error(), nil)
		return false
	}
	if active >= limit {
		s.writeError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED",
			"the "+scope+" concurrent-session limit was reached",
			map[string]any{"scope": scope, "limit": limit, "active": active})
		return false
	}
	return true
}

// requirePolicyChain runs the §4.8 PostAuth interceptor chain on the
// session-creation path. The built-in QuotaEvaluator (priority 200)
// enforces the §11.2 hierarchical token budget here; any registered
// external interceptor runs in the same chain.
//
// When the chain REJECTs, requirePolicyChain emits the §16.7
// `interceptor.rejected` audit row (synchronously, per §11.7) and
// writes a 429 QUOTA_EXCEEDED response. It returns true when the chain
// admits the request (ActionAllow or ActionModify, or no chain wired);
// when it returns false it has already written the response.
//
// A chain that fails closed (interceptor error or timeout) carries
// interceptor.CodeInterceptorTimeout; the rejection is still surfaced
// as a 429 because a session create blocked by policy enforcement is a
// policy outcome from the caller's perspective.
func (s *Server) requirePolicyChain(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	if s.interceptors == nil {
		return true
	}
	callerSub := ""
	if p, ok := getPrincipal(r); ok {
		callerSub = p.Subject
	}
	req := interceptor.Request{
		Phase:    interceptor.PhasePostAuth,
		TenantID: tenantID,
		Metadata: map[string]string{
			policy.MetadataTenantID: tenantID,
			policy.MetadataUserID:   callerSub,
		},
	}
	res := s.interceptors.Run(r.Context(), req)
	if res.Action != interceptor.ActionReject {
		return true
	}
	// §11.7: the chain-rejection audit row is gateway-originated and is
	// written synchronously before the response is returned. An audit
	// append failure fails the request closed with 500 — a policy
	// rejection that cannot be recorded must not be silently dropped.
	if s.policyAuditSink != nil {
		if err := s.policyAuditSink.RecordRejection(r.Context(), policy.RejectionContext{
			TenantID:  tenantID,
			CallerSub: callerSub,
			Phase:     interceptor.PhasePostAuth,
		}, res); err != nil {
			s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
				"policy rejection could not be recorded to the audit chain: "+err.Error(), nil)
			return false
		}
	}
	// spec: §4.8 line 1032, §15.1 line 1008 — a fail-closed interceptor
	// timeout/error is surfaced as 503 INTERCEPTOR_TIMEOUT (TRANSIENT,
	// retryable) so the caller distinguishes "the policy service is
	// degraded" from a deliberate policy REJECT. The details carry
	// interceptor_ref, phase, and timeout_ms per the error catalog.
	if res.Code == interceptor.CodeInterceptorTimeout {
		s.writeError(w, http.StatusServiceUnavailable, interceptor.CodeInterceptorTimeout, res.Reason,
			map[string]any{
				"interceptor_ref": res.RejectedBy,
				"phase":           string(interceptor.PhasePostAuth),
				"timeout_ms":      res.TimeoutMs,
			})
		return false
	}
	details := map[string]any{"reason": res.Reason}
	if res.Code != "" {
		details["interceptorCode"] = res.Code
	}
	s.writeError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED", res.Reason, details)
	return false
}
