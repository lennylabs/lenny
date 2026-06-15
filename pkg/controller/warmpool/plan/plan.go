// SPDX-License-Identifier: MIT

// Package plan holds the pure warm-pool reconcile planner. Given a
// pool's minWarm and maxWarm bounds and the observed phase of every
// Sandbox in the pool, Compute decides how many Sandboxes to create
// and which idle Sandboxes to drain so the pool converges on its
// target warm count.
//
// This is the decision core of the §4.6.1 WarmPoolController. It is
// kept free of any Kubernetes client so the convergence logic can be
// exhaustively unit-tested; the controller-runtime Reconcile method
// wraps it, supplying the observed pods and applying the returned
// Plan via the API server.
//
// Warm-pod accounting follows the §6.2 state machine:
//
//   - A pod counts as warm inventory while it is unclaimed
//     (warming, sdk_connecting, idle).
//   - Only an idle pod is ready to serve a fresh §4.6 claim.
//     ReadyCount tracks only idle pods.
//   - Claimed, reserved, draining, and terminal pods are not warm
//     inventory and the planner ignores them for the warm/ready
//     counts. With one session mode a pod serving any number of
//     concurrent sessions projects the coarse claimed phase (spec §6.2
//     collapses the former slot-active value into claimed), so a
//     concurrently-occupied pod falls under the claimed-is-not-warm
//     rule with no special case. The claim path (the gateway flipping
//     idle → claimed) reduces both warm and ready by one until the pod
//     returns to idle or terminates.
//   - A reserved pod is one whose §4.6 claim sits in the §6.2 reserved
//     hold window: scrubbed and held for its pinned tenant until the
//     claim-hold TTL expires. The §4.6.2 rule "reserved pods count as
//     occupied" excludes it from claimable idle inventory (it is not
//     counted in WarmCount or ReadyCount) and counts it occupied for
//     scaling, so a long gateway.claimHoldTTLSeconds depresses apparent
//     idle inventory. The planner surfaces the count in ReservedCount so
//     the controller can publish the §16.1 lenny_warmpool_reserved_pods
//     gauge from the same observation.
//   - Drain candidates are limited to idle pods, so the planner never
//     drains a pod whose claim carries live sessions or sits in the
//     reserved hold window.
package plan

import (
	"sort"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// Pod is the observed state of one Sandbox in the pool, reduced to the
// fields the planner needs.
type Pod struct {
	// Name is the Sandbox resource name.
	Name string
	// Phase is the Sandbox's observed §6.2 lifecycle phase.
	Phase state.State
}

// Inputs is the planner's full input for one pool reconcile pass.
type Inputs struct {
	// MinWarm is the warm-pod floor the controller maintains. A
	// negative value is clamped to zero.
	MinWarm int
	// MaxWarm is the warm-pod ceiling. A negative value is clamped to
	// zero. When MaxWarm is below MinWarm the planner targets MaxWarm,
	// since the ceiling is the hard bound.
	MaxWarm int
	// Pods is every Sandbox currently associated with the pool,
	// regardless of phase.
	Pods []Pod
}

// Plan is the planner's decision for one reconcile pass.
type Plan struct {
	// Create is the number of new Sandboxes the controller should
	// create from the pool's SandboxTemplate.
	Create int
	// Drain lists, in stable order, the idle Sandbox names to
	// transition to draining to shed warm pods above the target.
	Drain []string
	// WarmCount is the count of unclaimed pods that are warming or
	// idle. It is written to SandboxWarmPool.status.warmCount.
	WarmCount int
	// ReadyCount is the count of idle (claimable) pods. It is written
	// to SandboxWarmPool.status.readyCount.
	ReadyCount int
	// ReservedCount is the count of pods whose claim is in the §6.2
	// reserved hold window. Per §4.6.2 these pods are occupied: they are
	// excluded from WarmCount and ReadyCount and are never drain
	// candidates. The controller publishes this count to the §16.1
	// lenny_warmpool_reserved_pods gauge.
	ReservedCount int
}

// isWarming reports whether a phase is an unclaimed pod still being
// prepared toward idle.
func isWarming(s state.State) bool {
	return s == state.Warming || s == state.SDKConnecting
}

// Compute applies the §4.6.1 warm-pool convergence rules and returns
// the create/drain plan plus the warm and ready counts for the status
// subresource. It is pure: equal Inputs always yield an equal Plan.
func Compute(in Inputs) Plan {
	target := warmTarget(in.MinWarm, in.MaxWarm)

	var warmCount, readyCount, reservedCount int
	var idleNames []string
	for _, p := range in.Pods {
		switch {
		case p.Phase == state.Idle:
			warmCount++
			readyCount++
			idleNames = append(idleNames, p.Name)
		case isWarming(p.Phase):
			warmCount++
		case p.Phase == state.Reserved:
			// spec: §4.6.2 "reserved pods count as occupied" — a reserved pod
			// is excluded from claimable idle inventory (not warm, not ready)
			// and is counted occupied. It is surfaced separately so the
			// controller can emit the §16.1 reserved-pods gauge.
			reservedCount++
		}
	}

	plan := Plan{WarmCount: warmCount, ReadyCount: readyCount, ReservedCount: reservedCount}

	switch {
	case warmCount < target:
		plan.Create = target - warmCount
	case warmCount > target:
		// Shed the excess by draining idle pods. A pod still warming is
		// left alone; it becomes idle (warming → idle) and is drained on
		// a later pass if the pool is still over target. This keeps the
		// planner convergent without draining half-warmed capacity.
		excess := warmCount - target
		if excess > len(idleNames) {
			excess = len(idleNames)
		}
		sort.Strings(idleNames)
		plan.Drain = idleNames[:excess]
	}
	return plan
}

// warmTarget resolves the warm-pod target from the pool bounds. Both
// bounds are clamped to zero; the ceiling wins when it is below the
// floor, because MaxWarm is the hard bound the planner must not cross.
func warmTarget(minWarm, maxWarm int) int {
	if minWarm < 0 {
		minWarm = 0
	}
	if maxWarm < 0 {
		maxWarm = 0
	}
	if minWarm > maxWarm {
		return maxWarm
	}
	return minWarm
}
