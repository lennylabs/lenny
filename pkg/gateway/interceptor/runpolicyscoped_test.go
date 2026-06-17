// SPDX-License-Identifier: MIT

package interceptor_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// TestRunPolicyScoped_builtin_always_runs_spec_4_8_1036 confirms a
// built-in interceptor runs at PreDelegation regardless of the
// contentPolicy.interceptorRef — the §4.8 line-250 DelegationPolicyEvaluator
// (maxInputSize) is never gated by the policy ref. F-8.2.9.
func TestRunPolicyScoped_builtin_always_runs_spec_4_8_1036(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{name: "builtin-deleg", priority: 250, builtin: true, calls: &calls})

	for _, ref := range []string{"", "scanner-a"} {
		calls = nil
		// A non-empty ref that names no registered external interceptor
		// fails closed (covered below); register the scanner so the
		// builtin-only assertion isolates the builtin behavior.
		if ref != "" {
			mustRegister(t, c, phase, &fakeInterceptor{name: ref, priority: 500, builtin: false, calls: &calls})
		}
		res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase}, ref)
		if res.Action != interceptor.ActionAllow {
			t.Fatalf("ref=%q action = %v, want ALLOW", ref, res.Action)
		}
		found := false
		for _, n := range calls {
			if n == "builtin-deleg" {
				found = true
			}
		}
		if !found {
			t.Errorf("ref=%q builtin did not run; calls=%v", ref, calls)
		}
	}
}

// TestRunPolicyScoped_external_scoped_to_ref_spec_8_3_157 confirms only
// the policy-named external interceptor runs and the unnamed external
// interceptor is skipped — a registered scanner fires only for the
// delegations whose policy selects it. F-8.2.9 / F-13.5.2.
func TestRunPolicyScoped_external_scoped_to_ref_spec_8_3_157(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{name: "builtin-deleg", priority: 250, builtin: true, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "scanner-a", priority: 500, builtin: false, calls: &calls})
	mustRegister(t, c, phase, &fakeInterceptor{name: "scanner-b", priority: 600, builtin: false, calls: &calls})

	res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase}, "scanner-a")
	if res.Action != interceptor.ActionAllow {
		t.Fatalf("action = %v, want ALLOW", res.Action)
	}
	if !equal(calls, []string{"builtin-deleg", "scanner-a"}) {
		t.Errorf("calls = %v, want [builtin-deleg scanner-a] (scanner-b must not run)", calls)
	}
}

// TestRunPolicyScoped_empty_ref_runs_no_external_spec_8_3_157 confirms a
// policy with interceptorRef: null runs no external content scanner — the
// pre-fix bug was that every globally registered scanner fired regardless
// of policy. F-13.5.2.
func TestRunPolicyScoped_empty_ref_runs_no_external_spec_8_3_157(t *testing.T) {
	c := interceptor.NewChain()
	var calls []string
	mustRegister(t, c, phase, &fakeInterceptor{name: "scanner-a", priority: 500, builtin: false, calls: &calls})

	res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase}, "")
	if res.Action != interceptor.ActionAllow {
		t.Fatalf("action = %v, want ALLOW", res.Action)
	}
	if len(calls) != 0 {
		t.Errorf("calls = %v, want none (empty ref runs no external scan)", calls)
	}
}

// TestRunPolicyScoped_unresolvable_ref_fails_closed_spec_4_8_1032
// confirms a configured contentPolicy.interceptorRef that names no
// registered external interceptor fails closed with INTERCEPTOR_TIMEOUT
// rather than silently bypassing the content scan. F-8.2.9 / F-13.5.2.
func TestRunPolicyScoped_unresolvable_ref_fails_closed_spec_4_8_1032(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{name: "builtin-deleg", priority: 250, builtin: true})

	res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase, Content: []byte("hi")}, "missing-scanner")
	if res.Action != interceptor.ActionReject {
		t.Fatalf("action = %v, want REJECT", res.Action)
	}
	if res.Code != interceptor.CodeInterceptorTimeout {
		t.Errorf("code = %q, want %q", res.Code, interceptor.CodeInterceptorTimeout)
	}
	if res.RejectedBy != "missing-scanner" {
		t.Errorf("rejectedBy = %q, want missing-scanner", res.RejectedBy)
	}
}

// TestRunPolicyScoped_named_scanner_can_reject_and_modify confirms the
// named external interceptor's REJECT short-circuits and its MODIFY
// rewrites the payload for the rest of the chain. F-8.2.9.
func TestRunPolicyScoped_named_scanner_can_reject_and_modify(t *testing.T) {
	t.Run("reject", func(t *testing.T) {
		c := interceptor.NewChain()
		mustRegister(t, c, phase, &fakeInterceptor{
			name: "scanner", priority: 500, builtin: false,
			fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
				return interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked"}, nil
			},
		})
		res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase, Content: []byte("x")}, "scanner")
		if res.Action != interceptor.ActionReject || res.RejectedBy != "scanner" {
			t.Fatalf("res = %+v, want REJECT by scanner", res)
		}
	})
	t.Run("modify", func(t *testing.T) {
		c := interceptor.NewChain()
		mustRegister(t, c, phase, &fakeInterceptor{
			name: "scanner", priority: 500, builtin: false,
			fn: func(_ context.Context, _ interceptor.Request) (interceptor.Result, error) {
				return interceptor.Result{Action: interceptor.ActionModify, ModifiedContent: []byte("redacted")}, nil
			},
		})
		res := c.RunPolicyScoped(context.Background(), interceptor.Request{Phase: phase, Content: []byte("secret")}, "scanner")
		if res.Action != interceptor.ActionModify || string(res.ModifiedContent) != "redacted" {
			t.Fatalf("res = %+v, want MODIFY redacted", res)
		}
	})
}

// TestHasExternalNamed confirms the lookup distinguishes external from
// built-in and ignores an empty ref. F-8.2.9.
func TestHasExternalNamed(t *testing.T) {
	c := interceptor.NewChain()
	mustRegister(t, c, phase, &fakeInterceptor{name: "builtin-deleg", priority: 250, builtin: true})
	mustRegister(t, c, phase, &fakeInterceptor{name: "scanner", priority: 500, builtin: false})

	if !c.HasExternalNamed(phase, "scanner") {
		t.Error("HasExternalNamed(scanner) = false, want true")
	}
	if c.HasExternalNamed(phase, "builtin-deleg") {
		t.Error("HasExternalNamed(builtin-deleg) = true, want false (built-ins are not policy refs)")
	}
	if c.HasExternalNamed(phase, "") {
		t.Error("HasExternalNamed(\"\") = true, want false")
	}
	if c.HasExternalNamed(interceptor.PhasePreMessageDelivery, "scanner") {
		t.Error("HasExternalNamed on a phase with no registration = true, want false")
	}
}
