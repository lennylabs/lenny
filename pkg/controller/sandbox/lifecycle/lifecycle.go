// SPDX-License-Identifier: MIT

// Package lifecycle holds the pure warm-path planner for the Sandbox
// pod lifecycle (§6.2). Given a Sandbox's current phase and the
// observed state of its backing Pod, Decide returns the single action
// the controller should take: create the Pod, delete it, advance the
// Sandbox phase, or do nothing.
//
// The planner covers only the warm path the WarmPoolController owns:
// the pod-warm sequence warming → idle, the retirement of an idle pod
// whose backing Pod has died, and the draining → terminated teardown
// (§6.2). The claim and session phases (idle → claimed → ... →
// attached) are driven by the gateway, and the SDK-warm path
// (sdk_connecting) is layered on with the adapter RPCs later; the
// planner leaves both untouched.
//
// The planner holds no Kubernetes client so the state machine can be
// exhaustively unit-tested; the controller-runtime reconciler observes
// the Pod, calls Decide, and applies the result.
package lifecycle

import "github.com/lennylabs/lenny/pkg/sandbox/state"

// PodObservation is the reconciler's reduced view of the Pod backing a
// Sandbox.
type PodObservation int

const (
	// PodAbsent: no Pod exists for the Sandbox.
	PodAbsent PodObservation = iota
	// PodPending: the Pod exists but is not yet Running.
	PodPending
	// PodReady: the Pod is Running and every container is Ready.
	PodReady
	// PodNotReady: the Pod is Running but not all containers are Ready.
	PodNotReady
	// PodFailed: the Pod terminated in a failure state.
	PodFailed
	// PodSucceeded: the Pod terminated successfully. A warm agent pod
	// is not expected to exit 0, so the planner treats this as a defect.
	PodSucceeded
)

// Action is the single operation the reconciler performs for one
// Sandbox in a reconcile pass.
type Action int

const (
	// ActionNone: leave the Sandbox and its Pod unchanged.
	ActionNone Action = iota
	// ActionCreatePod: create the backing Pod from the pool template.
	ActionCreatePod
	// ActionDeletePod: delete the backing Pod.
	ActionDeletePod
	// ActionSetPhase: advance Sandbox.status.phase to Decision.NextPhase.
	ActionSetPhase
)

// Decision is the planner's output for one Sandbox.
type Decision struct {
	// Action is the operation to perform.
	Action Action
	// NextPhase is the phase to write; meaningful only when Action is
	// ActionSetPhase.
	NextPhase state.State
}

// Decide maps a Sandbox's current phase and its observed Pod to the
// next warm-path action (§6.2). Phases outside the warm path the
// WarmPoolController owns yield ActionNone.
func Decide(phase state.State, pod PodObservation) Decision {
	switch phase {
	case "", state.Warming:
		switch pod {
		case PodAbsent:
			return Decision{Action: ActionCreatePod}
		case PodReady:
			return setPhase(state.Idle)
		case PodFailed, PodSucceeded:
			return setPhase(state.Failed)
		default: // PodPending, PodNotReady — the pod is still warming.
			return Decision{}
		}
	case state.Idle:
		switch pod {
		case PodAbsent, PodFailed, PodSucceeded:
			// The warm pod's backing Pod is gone. §6.2 has no
			// idle → failed edge; retire the Sandbox through
			// idle → draining → terminated so the warm-pool sizing
			// reconciler provisions a fresh replacement.
			return setPhase(state.Draining)
		default: // PodReady, PodPending, PodNotReady — leave it warm.
			return Decision{}
		}
	case state.Draining:
		if pod == PodAbsent {
			return setPhase(state.Terminated)
		}
		return Decision{Action: ActionDeletePod}
	default:
		// Terminal phases and the gateway-owned claim/session phases:
		// the WarmPoolController takes no pod-lifecycle action.
		return Decision{}
	}
}

func setPhase(p state.State) Decision {
	return Decision{Action: ActionSetPhase, NextPhase: p}
}
