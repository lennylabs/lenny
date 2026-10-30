// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy/policy"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// requireSessionQuota enforces the §11.2 per-tenant concurrent-session
// quota on the session-creation path. When the tenant already holds
// MaxConcurrentSessions sessions that count toward the caps (every
// non-terminal session other than a suspended one), the create is
// rejected with 429 QUOTA_EXCEEDED. The same tenant check gates every
// resume of a suspended session through admitResume.
//
// A zero limit, an unknown tenant, or an unwired tenant registry means
// the tenant has no concurrent-session limit. requireSessionQuota
// returns true when the create may proceed; when it returns false it
// has already written the response.
//
// spec: §11.1 (Concurrent-session count), §11.2 (Budgets and Quotas)
func (s *Server) requireSessionQuota(w http.ResponseWriter, r *http.Request, tenantID string) bool {
	return s.answerCapCheck(w, s.checkTenantCap(r.Context(), tenantID))
}

// requireTenantClassification implements the §12.9 requirement
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
// already written the response. spec: §12.9; §15.1.
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

// requireTenantNotSuspended rejects a §15.1 message injection against a
// suspended tenant with 403 TENANT_SUSPENDED. It returns true when the
// call may proceed; when it returns false it has already written the
// response. An unwired registry or an unknown tenant means there is no
// suspension to consult, so the call proceeds. spec: §15.1.
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

// requireConcurrencyLimits enforces the §11.1 concurrent-session
// admission caps for the global, per-user, and per-runtime scopes (the
// per-tenant scope is enforced by requireSessionQuota against the tenant
// record, and the per-team scope is a §14 user-defined session label
// with no admin-settable limit surface in the spec). Each scope is
// checked only when its cap is configured (> 0). The scopes are
// evaluated narrowest-first (per-user, per-runtime, global) so the
// rejection names the tightest binding limit. Each count covers the
// non-terminal sessions other than suspended ones, and the same check
// gates every resume of a suspended session through admitResume.
//
// A count at or above a scope's cap rejects the create with 429
// QUOTA_EXCEEDED, the same code and status the per-tenant cap returns,
// carrying the breached scope, its limit, and the count. A counter
// error fails the request closed with 500 (an unenforceable admission
// cap must not silently admit).
//
// requireConcurrencyLimits returns true when the create may proceed;
// when it returns false it has already written the response.
//
// spec: §11.1 (Concurrent-session count). F-11.1.3.
func (s *Server) requireConcurrencyLimits(w http.ResponseWriter, r *http.Request, tenantID, userID, runtimeRef string) bool {
	return s.answerCapCheck(w, s.checkScopeCaps(r.Context(), tenantID, userID, runtimeRef))
}

// answerCapCheck writes the REST answer of a failed cap check, on session
// creation or on a message's resume admission: a *CapRefusal as 429
// QUOTA_EXCEEDED and any other error, a lookup or count error, as 500
// INTERNAL_ERROR, so an unenforceable cap fails closed. It returns true
// when err is nil.
func (s *Server) answerCapCheck(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	var refusal *CapRefusal
	if errors.As(err, &refusal) {
		s.writeCapRefusal(w, refusal)
		return false
	}
	s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	return false
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
	// spec: §4.8, §15.1 — a fail-closed interceptor
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

// Concurrent-session cap scopes a CapRefusal names.
const (
	CapScopeTenant  = "tenant"
	CapScopeUser    = "user"
	CapScopeRuntime = "runtime"
	CapScopeGlobal  = "global"
)

// CapRefusal is the refusal of a concurrent-session admission check: the
// count of the named scope is at or above its limit. Session creation and
// every resume of a suspended session answer it with 429 QUOTA_EXCEEDED,
// and lenny/send_message with the QUOTA_EXCEEDED tool error, each carrying
// Message and Details, so a refused resume carries the details a refused
// creation carries.
//
// spec: §11.1 (Concurrent-session count), §11.1 (Resume admission)
type CapRefusal struct {
	// Scope is CapScopeTenant, CapScopeUser, CapScopeRuntime, or
	// CapScopeGlobal.
	Scope string
	// Limit is the scope's configured limit.
	Limit int
	// Active is the count the check read.
	Active int
}

// Error implements error.
func (r *CapRefusal) Error() string {
	return "concurrent-session " + r.Scope + " limit " + strconv.Itoa(r.Limit) +
		" reached (" + strconv.Itoa(r.Active) + " counted)"
}

// Message is the human-readable message of the refusal's error envelope.
func (r *CapRefusal) Message() string {
	if r.Scope == CapScopeTenant {
		return "the tenant has reached its concurrent-session limit"
	}
	return "the " + r.Scope + " concurrent-session limit was reached"
}

// Details is the refusal's error-envelope details. The tenant scope
// carries limit and active; the other scopes also carry scope.
func (r *CapRefusal) Details() map[string]any {
	if r.Scope == CapScopeTenant {
		return map[string]any{"limit": r.Limit, "active": r.Active}
	}
	return map[string]any{"scope": r.Scope, "limit": r.Limit, "active": r.Active}
}

// writeCapRefusal writes a cap refusal as 429 QUOTA_EXCEEDED.
// spec: §15.1 (REST API), §11.1 (Concurrent-session count)
func (s *Server) writeCapRefusal(w http.ResponseWriter, r *CapRefusal) {
	s.writeError(w, http.StatusTooManyRequests, "QUOTA_EXCEEDED", r.Message(), r.Details())
}

// checkTenantCap runs the §11.2 per-tenant concurrent-session quota check.
// It returns nil when the tenant has no enabled limit (no registry, an
// unknown tenant, or a non-positive MaxConcurrentSessions) or its count is
// below the limit, a *CapRefusal at or above it, and a wrapped error when
// the tenant lookup or the count fails, so the caller fails closed.
//
// spec: §11.1 (Concurrent-session count), §11.2 (Budgets and Quotas)
func (s *Server) checkTenantCap(ctx context.Context, tenantID string) error {
	if s.tenants == nil {
		return nil
	}
	tenant, err := s.tenants.Get(ctx, tenantID)
	if errors.Is(err, tenantstore.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("concurrent-session quota check failed: %w", err)
	}
	if tenant.MaxConcurrentSessions <= 0 {
		return nil
	}
	active, err := s.store.CountActiveSessions(ctx, tenantID)
	if err != nil {
		return fmt.Errorf("concurrent-session quota check failed: %w", err)
	}
	if active >= tenant.MaxConcurrentSessions {
		return &CapRefusal{Scope: CapScopeTenant, Limit: tenant.MaxConcurrentSessions, Active: active}
	}
	return nil
}

// checkScopeCaps runs the §11.1 per-user, per-runtime, and global
// concurrent-session limit checks, in that order, each only when its limit
// is enabled and its key is non-empty. It returns nil, the first
// *CapRefusal, or a wrapped count error.
//
// spec: §11.1 (Concurrent-session count)
func (s *Server) checkScopeCaps(ctx context.Context, tenantID, userID, runtimeRef string) error {
	if s.maxConcSessPerUser > 0 && userID != "" {
		active, err := s.store.CountActiveSessionsByUser(ctx, tenantID, userID)
		if err := scopeOutcome(CapScopeUser, s.maxConcSessPerUser, active, err); err != nil {
			return err
		}
	}
	if s.maxConcSessPerRuntime > 0 && runtimeRef != "" {
		active, err := s.store.CountActiveSessionsByRuntime(ctx, tenantID, runtimeRef)
		if err := scopeOutcome(CapScopeRuntime, s.maxConcSessPerRuntime, active, err); err != nil {
			return err
		}
	}
	if s.maxConcSessGlobal > 0 {
		active, err := s.store.CountActiveSessionsGlobal(ctx)
		if err := scopeOutcome(CapScopeGlobal, s.maxConcSessGlobal, active, err); err != nil {
			return err
		}
	}
	return nil
}

// scopeOutcome turns one scope's count into the check's outcome: a wrapped
// count error, a *CapRefusal at or above the limit, or nil.
func scopeOutcome(scope string, limit, active int, err error) error {
	if err != nil {
		return fmt.Errorf("concurrent-session limit check failed: %w", err)
	}
	if active >= limit {
		return &CapRefusal{Scope: scope, Limit: limit, Active: active}
	}
	return nil
}

// admitResume is the resume admission check: the check session creation
// runs, evaluated over the session's tenant, owning user, and runtime. Every
// write out of suspended toward running calls it before the write and makes
// no write on an error. It returns nil, a *CapRefusal, or a wrapped lookup or
// count error. With every limit disabled it issues no count query. The check
// and the write are not atomic: admissions in flight at once can each pass
// against the same count, as session creation can.
//
// spec: §11.1 (Resume admission), §11.1 (Concurrent admissions)
func (s *Server) admitResume(ctx context.Context, row sessionstore.Session) error {
	if err := s.checkTenantCap(ctx, row.TenantID); err != nil {
		return err
	}
	return s.checkScopeCaps(ctx, row.TenantID, row.UserID, row.RuntimeRef)
}

// AdmitResume exposes admitResume to the lenny/send_message tool, which
// checks a message to a suspended session before holding it.
// spec: §11.1 (Resume admission), §7.2 (Message delivery routing — seven
// paths, path 6)
func (s *Server) AdmitResume(ctx context.Context, row sessionstore.Session) error {
	return s.admitResume(ctx, row)
}

// logCapRefusal logs a refusal of a resume that follows an admitted entry
// check, or of a backlog resume, at INFO with the refusing scope. Any other
// error is logged at INFO without one.
func logCapRefusal(msg, sessionID string, err error) {
	var refusal *CapRefusal
	if errors.As(err, &refusal) {
		slog.Info(msg, "session_id", sessionID, "scope", refusal.Scope,
			"limit", refusal.Limit, "active", refusal.Active)
		return
	}
	slog.Info(msg, "session_id", sessionID, "error", err)
}
