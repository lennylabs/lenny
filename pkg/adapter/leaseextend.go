// SPDX-License-Identifier: MIT

package adapter

import (
	"context"
	"errors"

	"github.com/lennylabs/lenny/pkg/adapter/gatewaycontrol"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
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

// BudgetExhaustedMessageGatewayUnreachable is the human-readable
// message embedded in the BUDGET_EXHAUSTED envelope when the adapter
// cannot reach the gateway to request an extension. The runtime sees a
// terminal-budget error rather than a transport error so the §8.6 line
// 629 contract holds even when the §8.6 round-trip itself fails.
const BudgetExhaustedMessageGatewayUnreachable = "lease extension unavailable; the budget for this session is exhausted"

// BudgetExhaustedMessageCeilingReached is embedded when the gateway
// returned CEILING_REACHED — the deployment/tenant/runtime ceiling is
// at zero headroom for the tree.
const BudgetExhaustedMessageCeilingReached = "lease-extension ceiling reached; the budget for this session is exhausted"

// BudgetExhaustedMessageRejected is embedded when the gateway returned
// REJECTED — the user (or the in-flight §8.6 line 732 atomic re-check)
// denied the extension and the subtree is in a rejection cool-off
// window.
const BudgetExhaustedMessageRejected = "lease extension denied; the budget for this session is exhausted"

// BudgetExhaustedEnvelope renders a §15.1 line 1080 error envelope the
// adapter sends to the runtime when it propagates BUDGET_EXHAUSTED per
// §8.6 line 629. Category is POLICY and retryable is false because the
// adapter MUST NOT retry the extension after a terminal outcome; the
// runtime is expected to surface the terminal error rather than loop.
//
// The helper is consumed by the LLM-proxy trigger that lands with the
// §8.6 F-8.6.6 wiring. It centralises the envelope shape so the
// production trigger and any other future propagation site emit the
// same wire shape.
// spec: §8.6 line 629; §15.1 line 1080
func BudgetExhaustedEnvelope(message string) *adapterv1.Error {
	if message == "" {
		message = BudgetExhaustedMessageRejected
	}
	return &adapterv1.Error{
		Code:      adapterv1.Error_ERROR_CODE_BUDGET_EXHAUSTED,
		Category:  adapterv1.Error_CATEGORY_POLICY,
		Message:   message,
		Retryable: false,
	}
}

// BudgetExhaustedEnvelopeFor maps a §8.6 ExtendLease outcome to the
// BUDGET_EXHAUSTED envelope text the adapter propagates. It returns nil
// for non-terminal outcomes (GRANTED, PARTIALLY_GRANTED) so the caller
// retries the LLM call instead of propagating an error.
// spec: §8.6 line 629
func BudgetExhaustedEnvelopeFor(status gatewaycontrol.ExtensionStatus) *adapterv1.Error {
	switch status {
	case gatewaycontrol.StatusCeilingReached:
		return BudgetExhaustedEnvelope(BudgetExhaustedMessageCeilingReached)
	case gatewaycontrol.StatusRejected:
		return BudgetExhaustedEnvelope(BudgetExhaustedMessageRejected)
	default:
		return nil
	}
}
