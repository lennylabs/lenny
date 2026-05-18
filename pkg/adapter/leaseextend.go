// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
)

// LeaseExtender is the §8.6 lease-extension seam: the adapter calls it
// when its LLM proxy rejects a request for budget exhaustion, and acts
// on the returned status. *gatewaycontrol.Client satisfies it by
// dialing the gateway's GatewayControl service.
//
// The interface is the boundary between the adapter's LLM-proxy
// integration and the gateway round-trip, so the budget-exhaustion
// handler is unit-testable without a live gateway.
type LeaseExtender interface {
	// ExtendLease requests additional token budget for sessionID. See
	// gatewaycontrol.Client.ExtendLease for the §8.6 semantics.
	ExtendLease(ctx context.Context, sessionID string, requestedTokens int64) (gatewaycontrol.ExtensionResult, error)
}

// BudgetExhaustionDecision is what the adapter does after a §8.6
// ExtendLease round-trip following an LLM-proxy budget rejection.
type BudgetExhaustionDecision int

const (
	// RetryLLMCall — the gateway granted budget (GRANTED or
	// PARTIALLY_GRANTED). The adapter transparently retries the LLM
	// call the proxy rejected; the runtime sees a slightly slow
	// response, not a failure.
	RetryLLMCall BudgetExhaustionDecision = iota
	// PropagateBudgetExhausted — the outcome is terminal
	// (CEILING_REACHED or REJECTED). The adapter MUST NOT retry the
	// extension and propagates BUDGET_EXHAUSTED to the runtime as a
	// terminal error.
	PropagateBudgetExhausted
)

// ErrLeaseExtenderUnset — HandleBudgetExhaustion was called without a
// LeaseExtender wired. The §8.6 trigger path cannot reach the gateway,
// so the caller propagates BUDGET_EXHAUSTED.
var ErrLeaseExtenderUnset = errors.New("adapter: no lease extender configured for §8.6 extension")

// HandleBudgetExhaustion is the §8.6 trigger entry point. The adapter's
// LLM-proxy integration calls it when the proxy rejects a call for
// budget exhaustion; it issues the gateway ExtendLease request and
// returns the decision the proxy path acts on.
//
// SEAM: the LLM-proxy rejection detector that calls this is a
// follow-on. The adapter does not yet host the §4.9 LLM-proxy client
// path, so no production caller is wired here. Once that path lands it
// invokes HandleBudgetExhaustion on a budget-exhaustion rejection,
// retries the LLM call on RetryLLMCall, and surfaces BUDGET_EXHAUSTED
// to the runtime on PropagateBudgetExhausted.
func (s *Server) HandleBudgetExhaustion(ctx context.Context, requestedTokens int64) (BudgetExhaustionDecision, gatewaycontrol.ExtensionResult, error) {
	if s.LeaseExtender == nil {
		return PropagateBudgetExhausted, gatewaycontrol.ExtensionResult{}, ErrLeaseExtenderUnset
	}
	sessionID := s.currentSessionID()
	if sessionID == "" {
		return PropagateBudgetExhausted, gatewaycontrol.ExtensionResult{},
			errors.New("adapter: no session assigned; cannot request a §8.6 lease extension")
	}

	res, err := s.LeaseExtender.ExtendLease(ctx, sessionID, requestedTokens)
	if err != nil {
		// A transport or gateway error is not a grant. The adapter
		// cannot retry the LLM call, so it propagates BUDGET_EXHAUSTED.
		return PropagateBudgetExhausted, gatewaycontrol.ExtensionResult{}, err
	}
	if res.Status.ShouldRetryLLMCall() {
		return RetryLLMCall, res, nil
	}
	// CEILING_REACHED or REJECTED: terminal, no retry (§8.6).
	return PropagateBudgetExhausted, res, nil
}

// currentSessionID returns the session currently assigned to the pod,
// or empty when the pod is idle.
func (s *Server) currentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}
