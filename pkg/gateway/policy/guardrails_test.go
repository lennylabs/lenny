// SPDX-License-Identifier: MIT

package policy_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
	"github.com/lennylabs/lenny/pkg/gateway/policy"
)

// spec: §4.8 line 1070 — GuardrailsInterceptor is the built-in hook for
// a deployer-wired external content classifier at the fixed priority
// 400, disabled by default, active at PreDelegation, PreLLMRequest,
// PostLLMResponse, and PostAgentOutput.

// stubClassifier is a deployer classifier returning a fixed decision.
type stubClassifier struct {
	res  interceptor.Result
	fail interceptor.FailPolicy
	to   time.Duration
	seen *interceptor.Request
}

func (s *stubClassifier) Name() string                       { return "stub-classifier" }
func (s *stubClassifier) Priority() int32                    { return 999 }
func (s *stubClassifier) Builtin() bool                      { return false }
func (s *stubClassifier) FailPolicy() interceptor.FailPolicy { return s.fail }
func (s *stubClassifier) Timeout() time.Duration             { return s.to }
func (s *stubClassifier) Intercept(_ context.Context, req interceptor.Request) (interceptor.Result, error) {
	r := req
	s.seen = &r
	return s.res, nil
}

func TestGuardrailsDisabledAdmitsEveryRequest(t *testing.T) {
	g := policy.NewGuardrailsInterceptor(nil)
	if g.Enabled() {
		t.Fatal("a nil-classifier guardrail must report disabled")
	}
	res, err := g.Intercept(context.Background(), interceptor.Request{Content: []byte("anything")})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionAllow {
		t.Errorf("action = %v, want ALLOW", res.Action)
	}
}

func TestGuardrailsBuiltinPriorityIs400(t *testing.T) {
	g := policy.NewGuardrailsInterceptor(nil)
	if !g.Builtin() {
		t.Error("GuardrailsInterceptor must be a built-in")
	}
	if g.Priority() != policy.GuardrailsInterceptorPriority || g.Priority() != 400 {
		t.Errorf("priority = %d, want 400", g.Priority())
	}
}

func TestGuardrailsDelegatesToClassifier(t *testing.T) {
	stub := &stubClassifier{
		res:  interceptor.Result{Action: interceptor.ActionReject, Reason: "blocked"},
		fail: interceptor.FailOpen,
		to:   250 * time.Millisecond,
	}
	g := policy.NewGuardrailsInterceptor(stub)
	if !g.Enabled() {
		t.Fatal("a configured guardrail must report enabled")
	}
	// The guardrail inherits the classifier's fail policy and timeout.
	if g.FailPolicy() != interceptor.FailOpen {
		t.Errorf("fail policy = %q, want fail-open (inherited)", g.FailPolicy())
	}
	if g.Timeout() != 250*time.Millisecond {
		t.Errorf("timeout = %v, want 250ms (inherited)", g.Timeout())
	}
	res, err := g.Intercept(context.Background(), interceptor.Request{Content: []byte("scan me")})
	if err != nil {
		t.Fatalf("Intercept: %v", err)
	}
	if res.Action != interceptor.ActionReject {
		t.Errorf("action = %v, want REJECT (delegated)", res.Action)
	}
	if stub.seen == nil || string(stub.seen.Content) != "scan me" {
		t.Error("the classifier did not see the request content")
	}
}

func TestRegisterGuardrailsCoversTheSpecPhases(t *testing.T) {
	chain := interceptor.NewChain()
	if err := policy.RegisterGuardrails(chain, policy.NewGuardrailsInterceptor(nil)); err != nil {
		t.Fatalf("RegisterGuardrails: %v", err)
	}
	want := []interceptor.Phase{
		interceptor.PhasePreDelegation,
		interceptor.PhasePreLLMRequest,
		interceptor.PhasePostLLMResponse,
		interceptor.PhasePostAgentOutput,
	}
	for _, phase := range want {
		if chain.Len(phase) != 1 {
			t.Errorf("phase %s has %d interceptors, want 1", phase, chain.Len(phase))
		}
	}
	// Phases outside the guardrails set must stay empty.
	if chain.Len(interceptor.PhasePostAuth) != 0 {
		t.Error("guardrails registered on a phase outside its spec set")
	}
}
