// SPDX-License-Identifier: MIT

package sessionserver

import (
	"errors"
	"net/http"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
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
	rows, err := s.store.List(r.Context(), tenantID, sessionstore.ListFilter{})
	if err != nil {
		s.writeError(w, http.StatusInternalServerError, "INTERNAL_ERROR",
			"concurrent-session quota check failed: "+err.Error(), nil)
		return false
	}
	active := 0
	for _, row := range rows {
		if !session.IsTerminal(row.State) {
			active++
		}
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
