// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy/interceptor"
)

// GuardrailsInterceptorPriority is the §4.8 built-in priority for
// GuardrailsInterceptor. The §4.8 built-in interceptor table fixes it
// at 400: external interceptors at priority 101–399 run before
// guardrails, those at 401–599 run after (spec: §4.8 line 1070).
const GuardrailsInterceptorPriority int32 = 400

// GuardrailsInterceptorName identifies GuardrailsInterceptor in audit
// rows and chain errors.
const GuardrailsInterceptorName = "GuardrailsInterceptor"

// GuardrailsPhases are the phases GuardrailsInterceptor is active at
// when enabled: the delegation input, the LLM proxy request and
// response, and the agent output (spec: §4.8 line 1070). A phase fires
// only when its chain is invoked; an unwired phase chain keeps the
// registration dormant until that phase runs.
func GuardrailsPhases() []interceptor.Phase {
	return []interceptor.Phase{
		interceptor.PhasePreDelegation,
		interceptor.PhasePreLLMRequest,
		interceptor.PhasePostLLMResponse,
		interceptor.PhasePostAgentOutput,
	}
}

// GuardrailsInterceptor is the §4.8 built-in hook point for an external
// content classifier (AWS Bedrock Guardrails, Azure Content Safety,
// Lakera Guard, or a custom gRPC service). It is disabled by default:
// a GuardrailsInterceptor with a nil classifier passes every request
// through with ActionAllow. When a deployer wires a classifier, every
// Intercept call delegates to it, so the classifier's ALLOW/REJECT/
// MODIFY decision and its FailPolicy and Timeout govern the request.
//
// GuardrailsInterceptor is a built-in (Builtin() == true) registered at
// the reserved priority 400 across the phases GuardrailsPhases reports
// (spec: §4.8 line 1070). It is the gateway-side stable identity for
// the classifier: external interceptor priority ordering (101–399
// before, 401–599 after) is defined relative to this fixed priority
// regardless of the backing classifier's own registration.
type GuardrailsInterceptor struct {
	classifier interceptor.Interceptor
}

// NewGuardrailsInterceptor returns a GuardrailsInterceptor backed by
// classifier. A nil classifier yields a disabled (no-op) interceptor
// that admits every request, matching the §4.8 "disabled by default"
// posture.
func NewGuardrailsInterceptor(classifier interceptor.Interceptor) *GuardrailsInterceptor {
	return &GuardrailsInterceptor{classifier: classifier}
}

// Enabled reports whether a backing classifier is configured.
func (g *GuardrailsInterceptor) Enabled() bool { return g.classifier != nil }

// Name implements interceptor.Interceptor.
func (g *GuardrailsInterceptor) Name() string { return GuardrailsInterceptorName }

// Priority implements interceptor.Interceptor. GuardrailsInterceptor is
// a built-in at the reserved priority 400.
func (g *GuardrailsInterceptor) Priority() int32 { return GuardrailsInterceptorPriority }

// Builtin implements interceptor.Interceptor. GuardrailsInterceptor is
// the built-in identity for a deployer-wired classifier, so it may
// register at a priority within the reserved ceiling and at any phase.
func (g *GuardrailsInterceptor) Builtin() bool { return true }

// FailPolicy implements interceptor.Interceptor. When a classifier is
// configured the guardrail inherits the classifier's fail policy; a
// disabled guardrail reports FailClosed (it never errors, so the value
// is informational).
func (g *GuardrailsInterceptor) FailPolicy() interceptor.FailPolicy {
	if g.classifier != nil {
		return g.classifier.FailPolicy()
	}
	return interceptor.FailClosed
}

// Timeout implements interceptor.Interceptor. It inherits the
// classifier's per-call deadline; a disabled guardrail returns 0 so the
// chain's phase default applies.
func (g *GuardrailsInterceptor) Timeout() time.Duration {
	if g.classifier != nil {
		return g.classifier.Timeout()
	}
	return 0
}

// Intercept implements interceptor.Interceptor. A disabled guardrail
// (nil classifier) returns ActionAllow. An enabled guardrail delegates
// the decision to the configured classifier, which sees the content as
// it stands after any upstream MODIFY (spec: §4.8 line 1070).
func (g *GuardrailsInterceptor) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	if g.classifier == nil {
		return interceptor.Result{Action: interceptor.ActionAllow}, nil
	}
	return g.classifier.Intercept(ctx, req)
}

// RegisterGuardrails registers g on every phase GuardrailsPhases
// reports. A registration error (an unknown phase) is returned for the
// caller to fail fast.
func RegisterGuardrails(chain *interceptor.Chain, g *GuardrailsInterceptor) error {
	for _, phase := range GuardrailsPhases() {
		if err := chain.Register(phase, g); err != nil {
			return err
		}
	}
	return nil
}
