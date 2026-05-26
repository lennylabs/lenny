// SPDX-License-Identifier: MIT

package policy_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
)

// fakeRetryLookup is a test RetryStateLookup keyed by session id.
type fakeRetryLookup struct {
	states map[string]policy.RetryState
	err    error
}

func (f *fakeRetryLookup) LookupRetryState(_ context.Context, _ /*tenantID*/, sessionID string) (policy.RetryState, bool, error) {
	if f.err != nil {
		return policy.RetryState{}, false, f.err
	}
	st, ok := f.states[sessionID]
	return st, ok, nil
}

// fakeMaxRetriesResolver returns a fixed maxRetries for every session.
type fakeMaxRetriesResolver struct {
	max int
	ok  bool
}

func (f fakeMaxRetriesResolver) ResolveMaxRetries(_ context.Context, _, _ string) (int, bool) {
	return f.max, f.ok
}

// spec: §4.8 line 977 — RetryPolicyEvaluator is the PostRoute built-in at
// the reserved priority 600.
func TestRetryPolicyEvaluatorIdentity_spec_4_8(t *testing.T) {
	e := policy.NewRetryPolicyEvaluator(nil, nil, 0)
	if e.Name() != policy.RetryPolicyEvaluatorName {
		t.Errorf("Name() = %q, want %q", e.Name(), policy.RetryPolicyEvaluatorName)
	}
	if e.Priority() != 600 {
		t.Errorf("Priority() = %d, want 600", e.Priority())
	}
	if !e.Builtin() {
		t.Error("RetryPolicyEvaluator must be a built-in")
	}
	if e.FailPolicy() != interceptor.FailClosed {
		t.Errorf("FailPolicy() = %q, want fail-closed", e.FailPolicy())
	}
}

// spec: §7.3 — a session whose RetryCount has reached the effective
// maxRetries has exhausted automatic recovery; a further automatic
// routing attempt is rejected. A session within budget, a session with
// no retry state, and a request with no session context are admitted.
func TestRetryPolicyEvaluatorBudget_spec_7_3(t *testing.T) {
	lookup := &fakeRetryLookup{states: map[string]policy.RetryState{
		"sess_fresh":     {RetryCount: 0},
		"sess_one_retry": {RetryCount: 1},
		"sess_exhausted": {RetryCount: 2},
		"sess_over":      {RetryCount: 5},
	}}
	// Default maxRetries (2), no resolver.
	e := policy.NewRetryPolicyEvaluator(lookup, nil, policy.DefaultMaxRetries)

	cases := []struct {
		name       string
		sessionID  string
		wantAction interceptor.Action
	}{
		{"no_session_context", "", interceptor.ActionAllow},
		{"unknown_session", "sess_missing", interceptor.ActionAllow},
		{"fresh_session", "sess_fresh", interceptor.ActionAllow},
		{"within_budget", "sess_one_retry", interceptor.ActionAllow},
		{"at_budget", "sess_exhausted", interceptor.ActionReject},
		{"over_budget", "sess_over", interceptor.ActionReject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Intercept(context.Background(), interceptor.Request{
				Phase:     interceptor.PhasePostRoute,
				SessionID: tc.sessionID,
				TenantID:  "acme",
			})
			if err != nil {
				t.Fatalf("Intercept returned error: %v", err)
			}
			if res.Action != tc.wantAction {
				t.Errorf("Action = %v, want %v (reason %q)", res.Action, tc.wantAction, res.Reason)
			}
			if tc.wantAction == interceptor.ActionReject && res.Reason == "" {
				t.Error("a REJECT must carry a human-readable reason")
			}
		})
	}
}

// spec: §4.8 line 977 — the effective retryPolicy.maxRetries comes from
// the resolver (the per-session / per-DelegationPolicy source). A
// resolver-supplied cap overrides the evaluator default.
func TestRetryPolicyEvaluatorResolverOverridesDefault_spec_4_8(t *testing.T) {
	lookup := &fakeRetryLookup{states: map[string]policy.RetryState{
		"sess": {RetryCount: 3},
	}}
	// Default would admit (3 < 5), but the resolver tightens the cap to 3,
	// so the session is at budget and rejected.
	e := policy.NewRetryPolicyEvaluator(lookup, fakeMaxRetriesResolver{max: 3, ok: true}, 5)
	res, err := e.Intercept(context.Background(), interceptor.Request{SessionID: "sess", TenantID: "acme"})
	if err != nil {
		t.Fatalf("Intercept error: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Errorf("Action = %v, want REJECT (resolver cap 3, retryCount 3)", res.Action)
	}

	// A resolver that widens the cap to 10 admits the same session.
	e2 := policy.NewRetryPolicyEvaluator(lookup, fakeMaxRetriesResolver{max: 10, ok: true}, 2)
	res2, err := e2.Intercept(context.Background(), interceptor.Request{SessionID: "sess", TenantID: "acme"})
	if err != nil {
		t.Fatalf("Intercept error: %v", err)
	}
	if res2.Action != interceptor.ActionAllow {
		t.Errorf("Action = %v, want ALLOW (resolver cap 10, retryCount 3)", res2.Action)
	}
}

// spec: §4.8 — RetryPolicyEvaluator is fail-closed: a retry-state lookup
// fault surfaces as an error so the chain rejects rather than admitting
// an unverifiable retry.
func TestRetryPolicyEvaluatorFailsClosedOnLookupError_spec_4_8(t *testing.T) {
	lookup := &fakeRetryLookup{err: errors.New("postgres unreachable")}
	e := policy.NewRetryPolicyEvaluator(lookup, nil, 2)
	_, err := e.Intercept(context.Background(), interceptor.Request{SessionID: "sess", TenantID: "acme"})
	if err == nil {
		t.Fatal("Intercept must surface the lookup error so the fail-closed chain rejects")
	}
}

// spec: §4.8 — a nil lookup leaves the priority-600 slot registered but
// dormant: every request is admitted.
func TestRetryPolicyEvaluatorNilLookupAdmits_spec_4_8(t *testing.T) {
	e := policy.NewRetryPolicyEvaluator(nil, nil, 2)
	res, err := e.Intercept(context.Background(), interceptor.Request{SessionID: "sess", TenantID: "acme"})
	if err != nil {
		t.Fatalf("Intercept error: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Errorf("Action = %v, want ALLOW (nil lookup)", res.Action)
	}
}

// spec: §4.8 — when registered on the PostRoute chain, an exhausted
// session short-circuits the chain with the evaluator named as the
// rejecter, and a session within budget passes through.
func TestRetryPolicyEvaluatorOnChain_spec_4_8(t *testing.T) {
	lookup := &fakeRetryLookup{states: map[string]policy.RetryState{
		"sess_exhausted": {RetryCount: 2},
		"sess_ok":        {RetryCount: 0},
	}}
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePostRoute, policy.NewRetryPolicyEvaluator(lookup, nil, 2)); err != nil {
		t.Fatalf("register RetryPolicyEvaluator: %v", err)
	}

	rejected := chain.Run(context.Background(), interceptor.Request{
		Phase:     interceptor.PhasePostRoute,
		SessionID: "sess_exhausted",
		TenantID:  "acme",
	})
	if rejected.Action != interceptor.ActionReject {
		t.Fatalf("exhausted session: Action = %v, want REJECT", rejected.Action)
	}
	if rejected.RejectedBy != policy.RetryPolicyEvaluatorName {
		t.Errorf("RejectedBy = %q, want %q", rejected.RejectedBy, policy.RetryPolicyEvaluatorName)
	}

	admitted := chain.Run(context.Background(), interceptor.Request{
		Phase:     interceptor.PhasePostRoute,
		SessionID: "sess_ok",
		TenantID:  "acme",
	})
	if admitted.Action != interceptor.ActionAllow {
		t.Errorf("within-budget session: Action = %v, want ALLOW", admitted.Action)
	}
}
