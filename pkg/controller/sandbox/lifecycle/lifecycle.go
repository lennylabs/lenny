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

import (
	"time"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

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

// SDKWarmInputs is the reconciler's reduced view for the §6.1 SDK-warm
// warm path. It extends the pod-warm Decide with the signals the
// sdk_connecting phase needs.
type SDKWarmInputs struct {
	// Phase is the Sandbox's current status.phase.
	Phase state.State
	// Pod is the reduced view of the backing Pod (same as Decide).
	Pod PodObservation
	// SDKConnectElapsed is how long the backing Pod has been Running,
	// used as the §6.1 line 69 sdk_connecting watchdog clock (the SDK
	// pre-connect begins when the pod starts running). Zero disables the
	// watchdog (no observed start time yet).
	SDKConnectElapsed time.Duration
	// SDKConnectTimeout is the §6.1 line 69 watchdog budget
	// (sdkConnectTimeoutSeconds). Zero disables the watchdog.
	SDKConnectTimeout time.Duration
}

// TimedOut reports whether the decision DecideSDKWarm returned is the
// §6.1 line 69 watchdog firing (sdk_connecting → failed because the SDK
// hung past SDKConnectTimeout), so the reconciler increments
// lenny_warmpool_sdk_connect_timeout_total only for that transition and
// not for a genuine pod failure.
func (in SDKWarmInputs) TimedOut() bool {
	return in.Phase == state.SDKConnecting &&
		(in.Pod == PodPending || in.Pod == PodNotReady) &&
		in.SDKConnectTimeout > 0 && in.SDKConnectElapsed > in.SDKConnectTimeout
}

// DecideSDKWarm maps an SDK-warm Sandbox's phase and observed Pod to the
// next warm-path action (§6.1 lines 30-69, §6.2 lines 89-98). It routes
// the warm sequence through sdk_connecting: the pod warms to Running
// (warming → sdk_connecting), pre-connects its SDK while the readiness
// gate holds Pod.Ready False (sdk_connecting), and becomes claimable when
// the gate flips on container readiness (sdk_connecting → idle). A pod
// that hangs in sdk_connecting past the watchdog budget is retired to
// failed. Phases past idle reuse the pod-warm Decide so SDK-warm and
// pod-warm share the retirement and teardown logic.
//
// The caller selects DecideSDKWarm only when the pool's runtime declares
// capabilities.preConnect: true and the §6.1 circuit breaker has not set
// spec.sdkWarmDisabled; otherwise it calls Decide and the pod warms
// straight to pod-warm idle.
func DecideSDKWarm(in SDKWarmInputs) Decision {
	switch in.Phase {
	case "", state.Warming:
		switch in.Pod {
		case PodAbsent:
			return Decision{Action: ActionCreatePod}
		case PodFailed, PodSucceeded:
			return setPhase(state.Failed)
		case PodPending:
			// The pod is still scheduling; SDK pre-connect has not begun.
			return Decision{}
		default: // PodNotReady, PodReady — the pod is Running.
			// §6.1 line 30 / §6.2 line 89: the container is up and the
			// adapter pre-connects its SDK. Enter sdk_connecting; the
			// readiness gate keeps the pod un-claimable until the SDK is
			// connected and the gate flips.
			return setPhase(state.SDKConnecting)
		}
	case state.SDKConnecting:
		switch in.Pod {
		case PodAbsent, PodFailed, PodSucceeded:
			// §6.2 line 97: sdk_connecting → failed on a dead pod.
			return setPhase(state.Failed)
		case PodReady:
			// §6.2 line 90: the readiness gate flipped (containers,
			// including the pre-connected SDK, are ready) so the pod is
			// idle and claimable.
			return setPhase(state.Idle)
		default: // PodPending, PodNotReady — the SDK is still connecting.
			if in.TimedOut() {
				// §6.1 line 69 watchdog: the SDK hung in sdk_connecting
				// past sdkConnectTimeoutSeconds. Retire the pod to failed.
				return setPhase(state.Failed)
			}
			return Decision{}
		}
	default:
		// idle, draining, terminal, and the gateway-owned claim/session
		// phases share the pod-warm planner.
		return Decide(in.Phase, in.Pod)
	}
}
