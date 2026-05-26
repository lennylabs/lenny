// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// RetryPolicyEvaluatorPriority is the §4.8 built-in priority for
// RetryPolicyEvaluator. The §4.8 built-in interceptor table fixes it at
// 600, above GuardrailsInterceptor (400) and every other built-in: at
// PostRoute an external interceptor at priority > 600 is the only one
// guaranteed to run after all built-ins. spec: §4.8 line 977.
const RetryPolicyEvaluatorPriority int32 = 600

// RetryPolicyEvaluatorName identifies RetryPolicyEvaluator in audit rows
// and chain errors.
const RetryPolicyEvaluatorName = "RetryPolicyEvaluator"

// DefaultMaxRetries is the §7.3 retryPolicy.maxRetries fallback applied
// when no per-session policy is resolved. It matches the §7.3 worked
// example (`"maxRetries": 2`). spec: §7.3 retryPolicy example.
const DefaultMaxRetries = 2

// RetryState is the §7.3 session retry state RetryPolicyEvaluator reads
// at PostRoute. RetryCount is the §4.2 line 158 monotonic retry counter
// the coordinator bumps on each automatic recovery attempt.
type RetryState struct {
	// RetryCount is how many times the watchdog/coordinator has retried
	// this logical session (resume on a fresh pod, coordinator-handoff
	// retry). It starts at zero on a fresh session.
	RetryCount int64
}

// RetryStateLookup resolves a session's §7.3 retry state by id. A lookup
// that finds no persisted session returns ok == false so the evaluator
// admits the request: a routing request that carries no resolvable
// session (a fresh create whose row is not yet persisted, or a request
// with no session context) is not a continuation and is never gated. A
// backing-store fault returns a non-nil error so the fail-closed
// evaluator rejects rather than admitting an unverifiable retry.
type RetryStateLookup interface {
	LookupRetryState(ctx context.Context, tenantID, sessionID string) (state RetryState, ok bool, err error)
}

// RetryPolicyResolver resolves the effective §7.3 retryPolicy.maxRetries
// for a session. A resolver that finds no policy returns ok == false so
// the evaluator falls back to its configured default. The per-session /
// per-DelegationPolicy RetryPolicy source layers on here as that field
// set is modeled (v1 models only the retry counter, so the default
// applies); this mirrors the MaxInputSizeResolver seam on
// DelegationPolicyEvaluator. spec: §4.8 line 977 ("RetryPolicy from the
// effective DelegationPolicy"), §7.3 retryPolicy.maxRetries.
type RetryPolicyResolver interface {
	ResolveMaxRetries(ctx context.Context, tenantID, sessionID string) (maxRetries int, ok bool)
}

// RetryPolicyEvaluator is the §4.8 built-in at PostRoute (priority 600).
// It enforces the §7.3 retry-eligibility budget on the routing path: a
// session whose RetryCount has reached the effective
// retryPolicy.maxRetries has exhausted automatic recovery (§7.3: it
// transitions to awaiting_client_action and only an explicit client
// resume may proceed), so a further automatic routing attempt for it is
// rejected before a warm pod is claimed.
//
// The complementary §7.3 resume-window timer (maxResumeWindowSeconds /
// resumeEligibleUntil) is governed canonically by the gateway watchdog
// (resume_pending timeout → awaiting_client_action), not here: a
// session's resume window is unrelated to whether a normal continuation
// may route, and gating PostRoute on it would falsely reject
// continuations of long-lived running sessions. This evaluator realizes
// the §4.8 priority-600 chain slot and the retry-budget half that no
// other code path enforced, the same division DelegationPolicyEvaluator
// uses for the §8.3 maxInputSize cap.
//
// RetryPolicyEvaluator is built-in (Builtin() == true), registers at the
// reserved priority 600, fires at PostRoute only, and is fail-closed: a
// retry-state lookup fault rejects the request rather than admitting an
// unverifiable retry. A request that carries no resolvable session
// (empty session id, or a fresh create whose row is not yet persisted)
// is admitted with ActionAllow.
type RetryPolicyEvaluator struct {
	lookup            RetryStateLookup
	resolver          RetryPolicyResolver
	defaultMaxRetries int
}

// NewRetryPolicyEvaluator returns a RetryPolicyEvaluator backed by
// lookup. resolver supplies the per-session effective maxRetries; pass
// nil to apply defaultMaxRetries uniformly. A non-positive
// defaultMaxRetries selects DefaultMaxRetries. A nil lookup yields an
// evaluator that admits every request (the slot is registered but
// dormant until a retry-state source is wired).
func NewRetryPolicyEvaluator(lookup RetryStateLookup, resolver RetryPolicyResolver, defaultMaxRetries int) *RetryPolicyEvaluator {
	if defaultMaxRetries <= 0 {
		defaultMaxRetries = DefaultMaxRetries
	}
	return &RetryPolicyEvaluator{lookup: lookup, resolver: resolver, defaultMaxRetries: defaultMaxRetries}
}

// Name implements interceptor.Interceptor.
func (e *RetryPolicyEvaluator) Name() string { return RetryPolicyEvaluatorName }

// Priority implements interceptor.Interceptor. RetryPolicyEvaluator is a
// built-in and registers at the reserved priority 600.
func (e *RetryPolicyEvaluator) Priority() int32 { return RetryPolicyEvaluatorPriority }

// Builtin implements interceptor.Interceptor.
func (e *RetryPolicyEvaluator) Builtin() bool { return true }

// FailPolicy implements interceptor.Interceptor. The evaluator is
// fail-closed: a retry-state lookup fault is resolved as a REJECT so a
// backing-store outage cannot silently admit a retry past its budget.
func (e *RetryPolicyEvaluator) FailPolicy() interceptor.FailPolicy {
	return interceptor.FailClosed
}

// Timeout implements interceptor.Interceptor. A non-positive value
// selects the phase default.
func (e *RetryPolicyEvaluator) Timeout() time.Duration { return 0 }

// Intercept implements interceptor.Interceptor. It resolves the routed
// session's retry state and rejects a request whose RetryCount has
// reached the effective retryPolicy.maxRetries with the §7.3
// retry-exhaustion reason; every other request (no session context,
// no persisted session, or a session within its retry budget) is
// admitted with ActionAllow. The evaluator never issues MODIFY.
func (e *RetryPolicyEvaluator) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	if e.lookup == nil || req.SessionID == "" {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	tenantID := req.Metadata[MetadataTenantID]
	if tenantID == "" {
		tenantID = req.TenantID
	}
	st, ok, err := e.lookup.LookupRetryState(ctx, tenantID, req.SessionID)
	if err != nil {
		// Fail closed: surface the fault so the chain rejects rather than
		// admitting a retry whose budget could not be verified.
		return interceptor.Result{}, fmt.Errorf("retry-state lookup for session %q: %w", req.SessionID, err)
	}
	if !ok {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	maxRetries := e.defaultMaxRetries
	if e.resolver != nil {
		if resolved, rok := e.resolver.ResolveMaxRetries(ctx, tenantID, req.SessionID); rok && resolved >= 0 {
			maxRetries = resolved
		}
	}
	if st.RetryCount >= int64(maxRetries) {
		return interceptor.Result{
			Action: interceptor.ActionReject,
			Reason: fmt.Sprintf("session %q has exhausted its automatic retry budget (%d of %d retries used); "+
				"the session is in awaiting_client_action and requires an explicit client resume",
				req.SessionID, st.RetryCount, maxRetries),
		}, nil
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// Ensure RetryPolicyEvaluator satisfies the interceptor contract at
// compile time.
var _ interceptor.Interceptor = (*RetryPolicyEvaluator)(nil)
