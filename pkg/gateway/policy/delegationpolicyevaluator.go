// SPDX-License-Identifier: MIT

package policy

import (
	"context"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/delegationpolicystore"
	"github.com/lennylabs/lenny/pkg/gateway/interceptor"
)

// DelegationPolicyEvaluatorPriority is the §4.8 built-in priority for
// DelegationPolicyEvaluator. The §4.8 built-in interceptor table fixes
// it at 250 (above QuotaEvaluator at 200, below ExperimentRouter at
// 300). spec: §4.8 line 974.
const DelegationPolicyEvaluatorPriority int32 = 250

// DelegationPolicyEvaluatorName identifies DelegationPolicyEvaluator in
// audit rows and chain errors.
const DelegationPolicyEvaluatorName = "DelegationPolicyEvaluator"

// CodeInputTooLarge is the §15.1 error code a delegation carries when
// its TaskSpec.input exceeds the effective contentPolicy.maxInputSize.
// The gateway maps it to HTTP 413. spec: §15.1 line 1067.
const CodeInputTooLarge = "INPUT_TOO_LARGE"

// MaxInputSizeResolver resolves the effective §8.3
// contentPolicy.maxInputSize (in bytes) for a delegation issued by a
// parent session in a tenant. A resolver that finds no policy returns
// ok == false so the evaluator falls back to its configured default.
//
// The effective limit is the inherited, monotonically-tightened cap
// from the parent lease's DelegationPolicy. v1 resolves it from the
// tenant-scoped delegationpolicystore; the per-lease inheritance chain
// layers on as that wiring lands.
type MaxInputSizeResolver interface {
	ResolveMaxInputSize(ctx context.Context, tenantID, parentSessionID string) (limitBytes int, ok bool)
}

// DelegationPolicyEvaluator is the §4.8 built-in at PreDelegation
// (priority 250). It enforces the §8.3 contentPolicy.maxInputSize hard
// byte cap on TaskSpec.input: a delegation whose input exceeds the
// effective limit is rejected with INPUT_TOO_LARGE before pod
// allocation (§8.3 line 157). The PreDelegation chain payload is the
// serialized TaskSpec.input, so the byte length the evaluator measures
// is len(req.Content).
//
// The §8.3 depth ceiling, fan-out cap, cycle detection, and
// tag-matched allow/deny rules are enforced canonically inside
// delegation.Service (pkg/gateway/delegation, pkg/delegation/cycle,
// pkg/delegation/lease), which holds the parent lease context the
// PreDelegation chain Request does not carry. This evaluator realizes
// the §4.8 priority-250 chain slot and the maxInputSize cap that no
// other code path enforced.
//
// DelegationPolicyEvaluator is built-in (Builtin() == true), registers
// at the reserved priority 250, and is fail-closed.
type DelegationPolicyEvaluator struct {
	resolver       MaxInputSizeResolver
	defaultMaxSize int
}

// NewDelegationPolicyEvaluator returns a DelegationPolicyEvaluator. The
// resolver supplies the per-delegation effective maxInputSize; pass nil
// to apply defaultMaxSize uniformly. A non-positive defaultMaxSize
// selects the §8.3 default (128 KiB).
func NewDelegationPolicyEvaluator(resolver MaxInputSizeResolver, defaultMaxSize int) *DelegationPolicyEvaluator {
	if defaultMaxSize <= 0 {
		defaultMaxSize = delegationpolicystore.DefaultMaxInputSize
	}
	return &DelegationPolicyEvaluator{resolver: resolver, defaultMaxSize: defaultMaxSize}
}

// Name implements interceptor.Interceptor.
func (e *DelegationPolicyEvaluator) Name() string { return DelegationPolicyEvaluatorName }

// Priority implements interceptor.Interceptor. DelegationPolicyEvaluator
// is a built-in security-critical interceptor and registers at the
// reserved priority 250.
func (e *DelegationPolicyEvaluator) Priority() int32 { return DelegationPolicyEvaluatorPriority }

// Builtin implements interceptor.Interceptor.
func (e *DelegationPolicyEvaluator) Builtin() bool { return true }

// FailPolicy implements interceptor.Interceptor. The evaluator is
// fail-closed: when it returns an error (a resolver fault) the chain
// rejects the delegation rather than admitting it unmetered.
func (e *DelegationPolicyEvaluator) FailPolicy() interceptor.FailPolicy {
	return interceptor.FailClosed
}

// Timeout implements interceptor.Interceptor. A non-positive value
// selects the phase default.
func (e *DelegationPolicyEvaluator) Timeout() time.Duration { return 0 }

// Intercept implements interceptor.Interceptor. It measures the
// PreDelegation payload (the serialized TaskSpec.input) against the
// effective contentPolicy.maxInputSize and rejects an oversize
// delegation with INPUT_TOO_LARGE. An input within the limit is
// admitted with ActionAllow; the evaluator never issues MODIFY.
func (e *DelegationPolicyEvaluator) Intercept(ctx context.Context, req interceptor.Request) (interceptor.Result, error) {
	limit := e.defaultMaxSize
	if e.resolver != nil {
		if resolved, ok := e.resolver.ResolveMaxInputSize(ctx, req.TenantID, req.SessionID); ok && resolved > 0 {
			limit = resolved
		}
	}
	size := len(req.Content)
	if size > limit {
		return interceptor.Result{
			Action: interceptor.ActionReject,
			Code:   CodeInputTooLarge,
			Reason: fmt.Sprintf("delegation input is %d bytes, exceeding the contentPolicy.maxInputSize limit of %d bytes", size, limit),
		}, nil
	}
	return interceptor.Result{Action: interceptor.ActionAllow}, nil
}

// Ensure DelegationPolicyEvaluator satisfies the interceptor contract
// at compile time.
var _ interceptor.Interceptor = (*DelegationPolicyEvaluator)(nil)
