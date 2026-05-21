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
//     (warming, sdk_connecting, idle) OR while it is serving §5.2
//     concurrent slots (slot_active). Slot-active pods stay alive
//     across slot releases and remain part of the pool's effective
//     capacity, so the planner counts them as warm to avoid
//     oscillating between "pool below target → create more" and
//     "pool above target → drain idle" each time a slot releases.
//   - Only an idle pod is ready to serve a fresh §4.6 claim
//     (session-mode claims a whole pod; concurrent-mode slot
//     reservation onto a slot-active pod does not consume an idle
//     pod). ReadyCount tracks only idle pods.
//   - Claimed, draining, and terminal pods are not warm inventory
//     and the planner ignores them. The session-mode claim path
//     (the gateway flipping idle → claimed) reduces both warm and
//     ready by one until the pod returns to idle or terminates;
//     this is the established contract and is unchanged.
//   - Drain candidates are still limited to idle pods. The planner
//     never drains a slot-active pod — its slots carry live
//     sessions whose termination is the gateway's responsibility,
//     not the planner's.
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

	var warmCount, readyCount int
	var idleNames []string
	for _, p := range in.Pods {
		switch {
		case p.Phase == state.Idle:
			warmCount++
			readyCount++
			idleNames = append(idleNames, p.Name)
		case isWarming(p.Phase):
			warmCount++
		case p.Phase == state.SlotActive:
			// §5.2 concurrent modes: a slot-active pod is still part
			// of pool capacity. It is not eligible for fresh §4.6
			// claims (idle is) and not eligible for drain (its slots
			// host live sessions), but counting it as warm keeps the
			// planner from creating a replacement every time a pod
			// flips idle → slot_active under sustained slot churn.
			warmCount++
		}
	}

	plan := Plan{WarmCount: warmCount, ReadyCount: readyCount}

	switch {
	case warmCount < target:
		plan.Create = target - warmCount
	case warmCount > target:
		// Shed the excess by draining idle pods. A pod still warming
		// or hosting concurrent slots is left alone; it becomes idle
		// (warming → idle, or slot_active → idle on last release) and
		// is drained on a later pass if the pool is still over target.
		// This keeps the planner convergent without draining half-
		// warmed capacity or evicting live concurrent sessions.
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
