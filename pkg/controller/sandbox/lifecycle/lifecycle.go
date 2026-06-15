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
	// SDKConnectElapsed is how long the SDK-connect work on the current
	// edge has been running, used as the §6.1 sdk_connecting watchdog
	// clock. The reconciler re-anchors it per edge: on the warm-fill edge
	// (warming → sdk_connecting) it is the pod's running time (pre-connect
	// begins when the pod starts running); on the recycle re-warm edge it
	// is measured from the claim's rewarmStartedAt stamp so neither the
	// prior occupancy episode nor the whole-pod scrub counts against the
	// re-warm budget. Zero disables the watchdog (no observed start time
	// yet). spec: §6.1 (watchdog clock anchored at the entry into the
	// SDK-connect work on the edge being measured), §3.3.
	SDKConnectElapsed time.Duration
	// SDKConnectTimeout is the §6.1 line 69 watchdog budget
	// (sdkConnectTimeoutSeconds). Zero disables the watchdog.
	SDKConnectTimeout time.Duration
	// Recycle marks the §6.2 recycle re-warm edge: a recycling claim
	// carrying SandboxClaim.status.rewarmStartedAt projects sdk_connecting
	// while the preConnect SDK re-warms. On this edge the success terminus
	// is reserved (the claim projection writes it when the gateway patches
	// the claim recycling → reserved), so the warm-fill arm takes no action
	// on a ready pod and only runs the re-warm watchdog. On the warm-fill
	// edge (Recycle false) the success terminus is idle and this arm writes
	// it. spec: §6.1 (reserved terminus), §6.2 (recycle edges), §3.3.
	Recycle bool
}

// TimedOut reports whether the decision DecideSDKWarm returned is the
// §6.1 watchdog firing (sdk_connecting → failed because the SDK hung past
// SDKConnectTimeout), so the reconciler increments
// lenny_warmpool_sdk_connect_timeout_total only for that transition and
// not for a genuine pod failure. It covers both the warm-fill edge and
// the recycle re-warm edge: the reconciler re-anchors SDKConnectElapsed
// per edge, so the watchdog measures only the re-warm leg on a recycling
// claim. spec: §6.1, §3.3.
func (in SDKWarmInputs) TimedOut() bool {
	return in.Phase == state.SDKConnecting &&
		(in.Pod == PodPending || in.Pod == PodNotReady) &&
		in.SDKConnectTimeout > 0 && in.SDKConnectElapsed > in.SDKConnectTimeout
}

// DecideSDKWarm maps an SDK-warm Sandbox's phase and observed Pod to the
// next warm-path action (§6.1 lines 30-69, §6.2 lines 89-123). It routes
// the warm sequence through sdk_connecting: the pod warms to Running
// (warming → sdk_connecting), pre-connects its SDK while the readiness
// gate holds Pod.Ready False (sdk_connecting), and becomes claimable when
// the gate flips on container readiness. The sdk_connecting phase has two
// non-failure termini: on the warm-fill edge the readiness flip projects
// idle (this arm writes it); on the recycle re-warm edge (a recycling
// claim carrying rewarmStartedAt, in.Recycle true) the success terminus is
// reserved and the claim projection writes it, so this arm takes no action
// on a ready pod and only runs the re-warm watchdog. A pod that hangs in
// sdk_connecting past the watchdog budget is retired to failed on either
// edge. Phases past idle reuse the pod-warm Decide so SDK-warm and
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
			// §6.2: sdk_connecting → failed on a dead pod, on both the
			// warm-fill and the recycle re-warm edge.
			return setPhase(state.Failed)
		case PodReady:
			if in.Recycle {
				// §6.2 sdk_connecting → reserved (recycle re-warm edge):
				// the success terminus is reserved and the claim projection
				// (OccupancyReconciler) writes it when the gateway patches
				// the claim recycling → reserved. The warm-fill arm leaves
				// the ready pod untouched so the two arms do not fight over
				// the phase; this is a clean exit, not idle.
				return Decision{}
			}
			// §6.2 sdk_connecting → idle (warm-fill edge): the readiness
			// gate flipped (containers, including the pre-connected SDK, are
			// ready) so the pod is idle and claimable.
			return setPhase(state.Idle)
		default: // PodPending, PodNotReady — the SDK is still connecting.
			if in.TimedOut() {
				// §6.1 line 69 watchdog: the SDK hung in sdk_connecting
				// past sdkConnectTimeoutSeconds, measured from pod start on
				// the warm-fill edge and from rewarmStartedAt on the recycle
				// re-warm edge. Retire the pod to failed on either edge.
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
