// SPDX-License-Identifier: MIT

// Package leaseextension implements the §8.6 lease-extension budget
// computations: resolving the effective extension ceiling from the
// layered deployment/tenant/runtime configuration, and computing the
// grant for a requested extension against that ceiling.
//
// These are the pure decision functions. The stateful surface of §8.6
// — elicitation, cool-off windows, the auto-mode rate limit, the
// extension-denied flag and its Postgres durability — lives in the
// gateway-side ExtendLease handler, which uses these functions as its
// computational kernel.
package leaseextension

// Outcome is the result of computing a §8.6 extension grant for one
// budget dimension.
type Outcome int

const (
	// Granted means the full requested increase fits under the ceiling.
	Granted Outcome = iota
	// PartiallyGranted means the request was capped to the remaining
	// headroom, which is non-zero.
	PartiallyGranted
	// CeilingReached means no headroom remains; the grant is zero. §8.6
	// requires the adapter to treat this as terminal and not retry.
	CeilingReached
)

func (o Outcome) String() string {
	switch o {
	case Granted:
		return "GRANTED"
	case PartiallyGranted:
		return "PARTIALLY_GRANTED"
	case CeilingReached:
		return "CEILING_REACHED"
	default:
		return "UNKNOWN"
	}
}

// Grant computes the §8.6 extension grant for one budget dimension.
// current is the dimension's present limit, requested is the increase
// asked for, and ceiling is the effective maximum the dimension may
// reach. It returns the amount granted and the outcome: the full
// request when it fits, the remaining headroom when it does not, and
// zero when the ceiling is already reached. A non-positive request
// grants nothing and is reported as Granted.
func Grant(current, requested, ceiling int64) (granted int64, outcome Outcome) {
	if requested <= 0 {
		return 0, Granted
	}
	headroom := ceiling - current
	if headroom <= 0 {
		return 0, CeilingReached
	}
	if requested <= headroom {
		return requested, Granted
	}
	return headroom, PartiallyGranted
}

// ResolveEffectiveMax resolves the §8.6 effective maxExtendableBudget
// from the layered configuration. A zero value means "not set" at that
// level. The base value is the deployment default, overridden by the
// tenant base and then the runtime base; the result is capped by the
// tenant ceiling (when set) and then by the deployment ceiling (when
// set), which §8.6 designates as the absolute maximum.
func ResolveEffectiveMax(deploymentDefault, deploymentMax, tenantBase, tenantMax, runtimeBase int64) int64 {
	v := deploymentDefault
	if tenantBase > 0 {
		v = tenantBase
	}
	if runtimeBase > 0 {
		v = runtimeBase
	}
	if tenantMax > 0 && v > tenantMax {
		v = tenantMax
	}
	if deploymentMax > 0 && v > deploymentMax {
		v = deploymentMax
	}
	return v
}
