// SPDX-License-Identifier: MIT

package adapter_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/adapter"
	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// fakeExtender is a stub LeaseExtender for the §8.6 trigger seam. It
// returns a canned result or error and records the request.
type fakeExtender struct {
	result gatewaycontrol.ExtensionResult
	err    error

	gotSessionID string
	gotExt       gatewaycontrol.Extension
	calls        int
}

func (f *fakeExtender) ExtendLease(_ context.Context, sessionID string, ext gatewaycontrol.Extension) (gatewaycontrol.ExtensionResult, error) {
	f.calls++
	f.gotSessionID = sessionID
	f.gotExt = ext
	if f.err != nil {
		return gatewaycontrol.ExtensionResult{}, f.err
	}
	return f.result, nil
}

// extenderServer builds an adapter Server with a session assigned via
// StartSession, ready for the §8.6 trigger seam.
func extenderServer(t *testing.T, ext adapter.LeaseExtender) *adapter.Server {
	t.Helper()
	s, _, _ := sessionServer(t)
	s.LeaseExtender = ext
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return s
}

// TestHandleBudgetExhaustionGrantedRetries: a GRANTED extension tells
// the adapter to retry the LLM call the proxy rejected.
func TestHandleBudgetExhaustionGrantedRetries(t *testing.T) {
	ext := &fakeExtender{result: gatewaycontrol.ExtensionResult{
		Status:        gatewaycontrol.StatusGranted,
		GrantedTokens: 200_000,
	}}
	s := extenderServer(t, ext)

	decision, res, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err != nil {
		t.Fatalf("HandleBudgetExhaustion: %v", err)
	}
	if decision != adapter.RetryLLMCall {
		t.Errorf("decision = %v, want RetryLLMCall", decision)
	}
	if res.GrantedTokens != 200_000 {
		t.Errorf("granted = %d, want 200000", res.GrantedTokens)
	}
	// The request carried the assigned session and the asked amount.
	if ext.gotSessionID != "sess-1" {
		t.Errorf("extender session = %q, want sess-1", ext.gotSessionID)
	}
	if ext.gotExt.AdditionalTokens != 200_000 {
		t.Errorf("extender tokens = %d, want 200000", ext.gotExt.AdditionalTokens)
	}
	// spec: §8.6 — the budget-exhaustion trigger extends only the token
	// dimension; the other extendable fields stay zero.
	if ext.gotExt.AdditionalChildren != 0 || ext.gotExt.AdditionalTreeSize != 0 {
		t.Errorf("budget-exhaustion trigger set non-token dimensions: %+v", ext.gotExt)
	}
}

// TestHandleBudgetExhaustionPartiallyGrantedRetries: a
// PARTIALLY_GRANTED extension also tells the adapter to retry.
func TestHandleBudgetExhaustionPartiallyGrantedRetries(t *testing.T) {
	ext := &fakeExtender{result: gatewaycontrol.ExtensionResult{
		Status:        gatewaycontrol.StatusPartiallyGranted,
		GrantedTokens: 50_000,
	}}
	s := extenderServer(t, ext)

	decision, _, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err != nil {
		t.Fatalf("HandleBudgetExhaustion: %v", err)
	}
	if decision != adapter.RetryLLMCall {
		t.Errorf("decision = %v, want RetryLLMCall", decision)
	}
}

// TestHandleBudgetExhaustionCeilingReachedPropagates: a CEILING_REACHED
// extension is terminal — the adapter propagates BUDGET_EXHAUSTED and
// does not retry.
func TestHandleBudgetExhaustionCeilingReachedPropagates(t *testing.T) {
	ext := &fakeExtender{result: gatewaycontrol.ExtensionResult{
		Status: gatewaycontrol.StatusCeilingReached,
	}}
	s := extenderServer(t, ext)

	decision, _, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err != nil {
		t.Fatalf("HandleBudgetExhaustion: %v", err)
	}
	if decision != adapter.PropagateBudgetExhausted {
		t.Errorf("decision = %v, want PropagateBudgetExhausted", decision)
	}
}

// TestHandleBudgetExhaustionRejectedPropagates: a REJECTED extension is
// terminal — the adapter propagates BUDGET_EXHAUSTED.
func TestHandleBudgetExhaustionRejectedPropagates(t *testing.T) {
	ext := &fakeExtender{result: gatewaycontrol.ExtensionResult{
		Status:              gatewaycontrol.StatusRejected,
		CoolOffExpiryUnixMs: 1_799_999_999_000,
	}}
	s := extenderServer(t, ext)

	decision, res, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err != nil {
		t.Fatalf("HandleBudgetExhaustion: %v", err)
	}
	if decision != adapter.PropagateBudgetExhausted {
		t.Errorf("decision = %v, want PropagateBudgetExhausted", decision)
	}
	if res.CoolOffExpiryUnixMs != 1_799_999_999_000 {
		t.Errorf("cool-off expiry = %d, want 1799999999000", res.CoolOffExpiryUnixMs)
	}
}

// TestHandleBudgetExhaustionGatewayErrorPropagates: a transport error
// from the gateway is not a grant — the adapter propagates
// BUDGET_EXHAUSTED and surfaces the error.
func TestHandleBudgetExhaustionGatewayErrorPropagates(t *testing.T) {
	wantErr := errors.New("gateway unreachable")
	ext := &fakeExtender{err: wantErr}
	s := extenderServer(t, ext)

	decision, _, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err == nil {
		t.Fatal("HandleBudgetExhaustion should surface the gateway error")
	}
	if decision != adapter.PropagateBudgetExhausted {
		t.Errorf("decision = %v, want PropagateBudgetExhausted on a gateway error", decision)
	}
}

// TestHandleBudgetExhaustionNoExtender: with no LeaseExtender wired the
// trigger path returns ErrLeaseExtenderUnset and propagates.
func TestHandleBudgetExhaustionNoExtender(t *testing.T) {
	s, _, _ := sessionServer(t)
	if _, err := s.StartSession(context.Background(), startReq("sess-1")); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	decision, _, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if !errors.Is(err, adapter.ErrLeaseExtenderUnset) {
		t.Errorf("error = %v, want ErrLeaseExtenderUnset", err)
	}
	if decision != adapter.PropagateBudgetExhausted {
		t.Errorf("decision = %v, want PropagateBudgetExhausted", decision)
	}
}

// TestBudgetExhaustedEnvelopeForCeilingReached_spec_8_6_line_629: a
// CEILING_REACHED outcome maps to a §15.1 BUDGET_EXHAUSTED envelope
// with POLICY category and retryable=false.
func TestBudgetExhaustedEnvelopeForCeilingReached_spec_8_6_line_629(t *testing.T) {
	env := adapter.BudgetExhaustedEnvelopeFor(gatewaycontrol.StatusCeilingReached)
	if env == nil {
		t.Fatal("BudgetExhaustedEnvelopeFor(CEILING_REACHED) = nil, want envelope")
	}
	if env.Code != adapterv1.Error_ERROR_CODE_BUDGET_EXHAUSTED {
		t.Errorf("Code = %v, want ERROR_CODE_BUDGET_EXHAUSTED", env.Code)
	}
	if env.Category != adapterv1.Error_CATEGORY_POLICY {
		t.Errorf("Category = %v, want CATEGORY_POLICY", env.Category)
	}
	if env.Retryable {
		t.Error("Retryable = true; the adapter MUST NOT retry the extension")
	}
	if env.Message == "" {
		t.Error("Message is empty; want human-readable terminal-budget text")
	}
}

// TestBudgetExhaustedEnvelopeForRejected_spec_8_6_line_629: a REJECTED
// outcome maps to a §15.1 BUDGET_EXHAUSTED envelope.
func TestBudgetExhaustedEnvelopeForRejected_spec_8_6_line_629(t *testing.T) {
	env := adapter.BudgetExhaustedEnvelopeFor(gatewaycontrol.StatusRejected)
	if env == nil {
		t.Fatal("BudgetExhaustedEnvelopeFor(REJECTED) = nil, want envelope")
	}
	if env.Code != adapterv1.Error_ERROR_CODE_BUDGET_EXHAUSTED {
		t.Errorf("Code = %v, want ERROR_CODE_BUDGET_EXHAUSTED", env.Code)
	}
	if env.Message != adapter.BudgetExhaustedMessageRejected {
		t.Errorf("Message = %q, want %q", env.Message, adapter.BudgetExhaustedMessageRejected)
	}
}

// TestBudgetExhaustedEnvelopeForGrantedReturnsNil_spec_8_6_line_629:
// non-terminal outcomes do not produce a propagation envelope; the
// caller retries the LLM call.
func TestBudgetExhaustedEnvelopeForGrantedReturnsNil_spec_8_6_line_629(t *testing.T) {
	cases := []gatewaycontrol.ExtensionStatus{
		gatewaycontrol.StatusGranted,
		gatewaycontrol.StatusPartiallyGranted,
		gatewaycontrol.StatusUnspecified,
	}
	for _, status := range cases {
		t.Run(status.String(), func(t *testing.T) {
			if env := adapter.BudgetExhaustedEnvelopeFor(status); env != nil {
				t.Errorf("envelope for %v = %+v, want nil", status, env)
			}
		})
	}
}

// TestBudgetExhaustedEnvelopeDefaultMessage_spec_15_1_line_1080: an
// empty message falls back to the rejection-default text so the
// envelope always carries a human-readable message.
func TestBudgetExhaustedEnvelopeDefaultMessage_spec_15_1_line_1080(t *testing.T) {
	env := adapter.BudgetExhaustedEnvelope("")
	if env.Message != adapter.BudgetExhaustedMessageRejected {
		t.Errorf("default message = %q, want %q", env.Message, adapter.BudgetExhaustedMessageRejected)
	}
}

// TestHandleBudgetExhaustionNoSession: with no session assigned the
// trigger path errors — there is no lease to extend.
func TestHandleBudgetExhaustionNoSession(t *testing.T) {
	ext := &fakeExtender{result: gatewaycontrol.ExtensionResult{Status: gatewaycontrol.StatusGranted}}
	s, _, _ := sessionServer(t)
	s.LeaseExtender = ext

	decision, _, err := s.HandleBudgetExhaustion(context.Background(), 200_000)
	if err == nil {
		t.Fatal("HandleBudgetExhaustion with no session should error")
	}
	if decision != adapter.PropagateBudgetExhausted {
		t.Errorf("decision = %v, want PropagateBudgetExhausted", decision)
	}
	if ext.calls != 0 {
		t.Errorf("extender called %d times, want 0 — no session means no gateway round-trip", ext.calls)
	}
}
