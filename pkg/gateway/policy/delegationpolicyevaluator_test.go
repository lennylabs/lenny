// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// fakeMaxInputResolver is a MaxInputSizeResolver test double.
type fakeMaxInputResolver struct {
	limit int
	ok    bool
}

func (f fakeMaxInputResolver) ResolveMaxInputSize(_ context.Context, _, _ string) (int, bool) {
	return f.limit, f.ok
}

// spec: §4.8 line 974, §8.3 line 157 — DelegationPolicyEvaluator
// admits a TaskSpec.input within the effective maxInputSize and rejects
// an oversize input with INPUT_TOO_LARGE.
func TestDelegationPolicyEvaluator_MaxInputSize_spec_4_8_974(t *testing.T) {
	t.Parallel()
	const limit = 32
	e := NewDelegationPolicyEvaluator(nil, limit)

	cases := []struct {
		name       string
		size       int
		wantAction interceptor.Action
	}{
		{"under limit", limit - 1, interceptor.ActionAllow},
		{"at limit", limit, interceptor.ActionAllow},
		{"over limit", limit + 1, interceptor.ActionReject},
		{"empty input", 0, interceptor.ActionAllow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Intercept(context.Background(), interceptor.Request{
				Phase:   interceptor.PhasePreDelegation,
				Content: []byte(strings.Repeat("x", tc.size)),
			})
			if err != nil {
				t.Fatalf("Intercept: %v", err)
			}
			if res.Action != tc.wantAction {
				t.Fatalf("action = %v, want %v", res.Action, tc.wantAction)
			}
			if tc.wantAction == interceptor.ActionReject && res.Code != CodeInputTooLarge {
				t.Fatalf("code = %q, want %q", res.Code, CodeInputTooLarge)
			}
		})
	}
}

// spec: §8.3 line 157 — when a resolver supplies a tighter effective
// maxInputSize than the configured default, the evaluator enforces the
// resolved value.
func TestDelegationPolicyEvaluator_ResolverOverride_spec_8_3(t *testing.T) {
	t.Parallel()
	// Default is generous (1 KiB), resolver tightens to 8 bytes.
	e := NewDelegationPolicyEvaluator(fakeMaxInputResolver{limit: 8, ok: true}, 1024)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:    interceptor.PhasePreDelegation,
		TenantID: "acme",
		Content:  []byte(strings.Repeat("x", 9)),
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject || res.Code != CodeInputTooLarge {
		t.Fatalf("action=%v code=%q, want REJECT/%s", res.Action, res.Code, CodeInputTooLarge)
	}
}

// spec: §8.3 line 116 — a resolver miss (ok == false) falls back to the
// configured default cap rather than rejecting.
func TestDelegationPolicyEvaluator_ResolverMissUsesDefault_spec_8_3(t *testing.T) {
	t.Parallel()
	e := NewDelegationPolicyEvaluator(fakeMaxInputResolver{ok: false}, 16)
	res, err := e.Intercept(context.Background(), interceptor.Request{
		Phase:   interceptor.PhasePreDelegation,
		Content: []byte(strings.Repeat("x", 16)),
	})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Fatalf("action = %v, want ALLOW", res.Action)
	}
}

// spec: §8.3 line 116 — a non-positive configured default selects the
// §8.3 128 KiB default.
func TestDelegationPolicyEvaluator_DefaultsTo128KiB_spec_8_3(t *testing.T) {
	t.Parallel()
	e := NewDelegationPolicyEvaluator(nil, 0)
	if e.defaultMaxSize != delegationpolicystore.DefaultMaxInputSize {
		t.Fatalf("defaultMaxSize = %d, want %d", e.defaultMaxSize, delegationpolicystore.DefaultMaxInputSize)
	}
}

// spec: §4.8 line 974 — the built-in registers at the reserved priority
// 250 on the PreDelegation phase and is fail-closed.
func TestDelegationPolicyEvaluator_Contract_spec_4_8_974(t *testing.T) {
	t.Parallel()
	e := NewDelegationPolicyEvaluator(nil, 1024)
	if e.Priority() != DelegationPolicyEvaluatorPriority || e.Priority() != 250 {
		t.Fatalf("priority = %d, want 250", e.Priority())
	}
	if !e.Builtin() {
		t.Fatal("Builtin() = false, want true")
	}
	if e.FailPolicy() != interceptor.FailClosed {
		t.Fatalf("FailPolicy = %q, want fail-closed", e.FailPolicy())
	}
	if e.Name() != DelegationPolicyEvaluatorName {
		t.Fatalf("Name = %q", e.Name())
	}
	// A built-in at priority 250 must register on PreDelegation without
	// tripping the reserved-priority ceiling check.
	chain := interceptor.NewChain()
	if err := chain.Register(interceptor.PhasePreDelegation, e); err != nil {
		t.Fatalf("Register: %v", err)
	}
}
