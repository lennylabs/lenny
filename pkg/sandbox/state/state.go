// SPDX-License-Identifier: MIT

// Package state defines the Sandbox CRD state machine, per spec §6.2.
//
// The state enum and ValidTransitions() form the authoritative contract.
// IsValid returns nil for an edge present in ValidTransitions() and an
// InvalidTransitionError otherwise. The host-schedulable label check that
// distinguishes task_cleanup → sdk_connecting/idle (schedulable) from
// task_cleanup → draining (not schedulable) is enforced separately by the
// admission webhook lenny-sandboxclaim-guard; this function reports both
// edges as valid because they are legal at the state-machine layer.
package state

import "fmt"

// State is the Sandbox phase, written to Sandbox.status.phase.
type State string

// LabelState is the pod label the Sandbox-to-Pod reconciler keeps in
// sync with the Sandbox's §6.2 phase. The §4.6.1 per-pool
// PodDisruptionBudget selects warm pods by this label
// (lenny.dev/state: idle) so disruption protection targets only
// unclaimed pods. spec: §4.6.1 "Disruption protection for agent pods".
//
// The label carries only the coarse operational value set (see
// CoarseState), not the full §6.2 phase: spec §6.2 lines 305-313 reserve
// the full phase for Sandbox.status.phase and restrict the pod label to
// idle/active/draining for selectors, monitoring, and NetworkPolicies.
const LabelState = "lenny.dev/state"

// LabelRuntime is the pod label carrying the runtime name (the Sandbox's
// RuntimeRef). spec: §6.2 line 311 — operators select pods by runtime
// type through this coarse label. It is immutable for a pod's lifetime,
// stamped once at pod creation.
const LabelRuntime = "lenny.dev/runtime"

// The coarse pod-state label values. spec: §6.2 line 309 — the
// lenny.dev/state pod label carries only these three operational values
// for kubectl, monitoring, and NetworkPolicy selectors. CoarseIdle and
// CoarseDraining deliberately equal the Idle and Draining phase strings
// so the §4.6.1 PDB idle selector (which matches string(Idle)) keeps
// working unchanged.
const (
	CoarseIdle     = "idle"
	CoarseActive   = "active"
	CoarseDraining = "draining"
)

// CoarseState maps a §6.2 phase to its coarse lenny.dev/state pod-label
// value (spec §6.2 line 309: idle, active, or draining) and reports
// whether the phase has a coarse operational value at all. A pod is idle
// when warm and claimable, active once it is claimed and serving a
// session/slot/task, and draining when retiring. The pre-ready phases
// (warming, sdk_connecting) and the terminal phases (completed, failed,
// cancelled, expired, terminated) have no coarse value — the pod is
// either not yet claimable or gone — so the second return is false and
// the reconciler removes the label rather than emitting a fourth value
// the spec does not define. spec: §6.2 lines 305-313.
func CoarseState(s State) (string, bool) {
	switch s {
	case Idle:
		return CoarseIdle, true
	case Draining:
		return CoarseDraining, true
	case Claimed, ReceivingUploads, FinalizingWorkspace, RunningSetup,
		StartingSession, Attached, TaskCleanup, SlotActive, Resuming,
		Suspended, ResumePending, AwaitingClientAction:
		return CoarseActive, true
	default:
		return "", false
	}
}

const (
	Warming              State = "warming"
	SDKConnecting        State = "sdk_connecting"
	Idle                 State = "idle"
	Claimed              State = "claimed"
	SlotActive           State = "slot_active"
	ReceivingUploads     State = "receiving_uploads"
	FinalizingWorkspace  State = "finalizing_workspace"
	RunningSetup         State = "running_setup"
	StartingSession      State = "starting_session"
	Attached             State = "attached"
	TaskCleanup          State = "task_cleanup"
	Resuming             State = "resuming"
	Suspended            State = "suspended"
	ResumePending        State = "resume_pending"
	AwaitingClientAction State = "awaiting_client_action"
	Completed            State = "completed"
	Failed               State = "failed"
	Cancelled            State = "cancelled"
	Expired              State = "expired"
	Draining             State = "draining"
	Terminated           State = "terminated"
)

func All() []State {
	return []State{
		Warming, SDKConnecting, Idle, Claimed, SlotActive, ReceivingUploads,
		FinalizingWorkspace, RunningSetup, StartingSession, Attached, TaskCleanup,
		Resuming, Suspended, ResumePending, AwaitingClientAction,
		Completed, Failed, Cancelled, Expired, Draining, Terminated,
	}
}

// Terminal returns the §6.2 session-terminal phases (spec: §6.2 line 269 —
// completed, failed, cancelled, expired) plus the pod-lifecycle terminal
// terminated (spec: §6.2 line 84 SDK-warm sdk_connecting → terminated,
// line 195 draining → terminated). These are the phases at which the
// SESSION has stopped: the §6.2 line 269 maxSessionAge timer is no longer
// evaluated and no session-level edge leaves them. completed is terminal:
// §6.2 lines 96-99 list attached → completed and suspended → completed as
// terminal session edges with no path back out, so IsTerminal(Completed)
// reports true (a resumable-state helper that treated completed as
// non-terminal would contradict the spec and the CoarseState mapping, which
// classes completed among the terminal phases that carry no coarse label
// value).
//
// The four session-terminal phases additionally carry a pod-reclamation
// drain edge (completed/failed/cancelled/expired → draining; see
// ValidTransitions): a session-mode pod is exclusive, so once its session
// settles the gateway records the disposition on Sandbox.status.phase and
// drains the pod for replacement. That drain edge is a POD-lifecycle
// transition, distinct from the SESSION-terminality this helper reports — a
// phase can be session-terminal and still have an outgoing pod-cleanup edge.
// Only terminated has no outgoing edge of any kind.
func Terminal() []State {
	return []State{Completed, Failed, Cancelled, Expired, Terminated}
}

func IsTerminal(s State) bool {
	for _, t := range Terminal() {
		if s == t {
			return true
		}
	}
	return false
}

type Transition struct {
	From State
	To   State
}

// ValidTransitions per spec §6.2. The list captures the pod-warm path,
// the SDK-warm path, the claim/setup chain, the attached/session edges,
// the suspension/recovery loop, the task-cleanup edges, and the
// concurrent-mode slot edges. The host-schedulable gate
// (lenny.dev/host-schedulable: "true") chooses between
// task_cleanup → sdk_connecting/idle (schedulable) and
// task_cleanup → draining (not schedulable); both edges appear here.
func ValidTransitions() []Transition {
	return []Transition{
		// Pod-warm path.
		{Warming, Idle},
		{Warming, Failed},
		// SDK-warm path.
		{Warming, SDKConnecting},
		{SDKConnecting, Idle},
		{SDKConnecting, Failed},
		{SDKConnecting, Terminated},
		// Claim and setup.
		{Idle, Claimed},
		{Claimed, ReceivingUploads},
		{ReceivingUploads, FinalizingWorkspace},
		{ReceivingUploads, Failed},
		{FinalizingWorkspace, RunningSetup},
		{FinalizingWorkspace, Failed},
		{RunningSetup, StartingSession},
		{RunningSetup, Failed},
		// starting_session is the §6.2 line 87 phase between running_setup
		// and attached (the agent runtime is starting on a live pod). Its
		// pre-attached failure edges (spec: §6.2 lines 102-103):
		// starting_session → failed when retries are exhausted, and
		// starting_session → resume_pending when the pod crashed after the
		// client observed `starting` and retries remain (the sole
		// post-attached visibility path per §6.2 line 303).
		{StartingSession, Attached},
		{StartingSession, Failed},
		{StartingSession, ResumePending},
		// Session-level edges from attached.
		{Attached, Completed},
		{Attached, Failed},
		{Attached, Cancelled},
		{Attached, Suspended},
		{Attached, ResumePending},
		// Suspension/recovery loop.
		{Suspended, Attached},
		{Suspended, ResumePending},
		{Suspended, Completed},
		{Suspended, Cancelled},
		{Suspended, Expired},
		{Suspended, Failed},
		{ResumePending, Resuming},
		{ResumePending, AwaitingClientAction},
		{ResumePending, Cancelled},
		{ResumePending, Completed},
		{Resuming, Attached},
		{Resuming, AwaitingClientAction},
		{Resuming, Cancelled},
		{Resuming, Completed},
		{AwaitingClientAction, ResumePending},
		{AwaitingClientAction, Completed},
		{AwaitingClientAction, Cancelled},
		{AwaitingClientAction, Expired},
		// Task-cleanup paths.
		{Attached, TaskCleanup},
		{TaskCleanup, SDKConnecting}, // host-schedulable, preConnect
		{TaskCleanup, Idle},          // host-schedulable, non-preConnect
		{TaskCleanup, Draining},      // not host-schedulable, or maxTasksPerPod reached
		{TaskCleanup, Failed},
		// Concurrent-mode slot edges (§5.2 / §6.2). A concurrent-mode pod
		// hosts up to maxConcurrent slots simultaneously: idle → slot_active
		// on the first slot, slot_active → slot_active for each further
		// slot assignment and for a slot completing while siblings remain,
		// slot_active → idle when the last slot drains.
		{Idle, SlotActive},
		{SlotActive, SlotActive},
		{SlotActive, Idle},
		{SlotActive, Draining}, // unhealthy slot threshold or maxPodUptimeSeconds
		{SlotActive, Failed},
		// Drain and terminate. claimed → draining is the claim-time abort
		// path: a pod claimed but torn down before workspace start (gateway
		// crash between the SSA claim and the setup chain) drains and
		// terminates rather than stranding. It is a deliberate extension of
		// the §6.2 idle → draining → terminated cleanup model to the
		// pre-attached claimed phase; §6.2 enumerates the cleanup edge but
		// not this specific source, so it is documented here.
		{Idle, Draining},
		{Claimed, Draining},
		{Attached, Draining},
		{Draining, Terminated},
		// Session-terminal pod reclamation. A session-mode pod is exclusive
		// to one session (§6.2 line 194); once the session reaches a terminal
		// phase the gateway records the disposition on Sandbox.status.phase
		// (attached → completed/failed/cancelled, §6.2 lines 105-117) and then
		// drains the pod so the warm-pool sizer provisions a replacement.
		// §6.2 enumerates the terminal session edges and the
		// draining → terminated cleanup but not the per-terminal drain source;
		// like the claimed → draining abort edge above, these are documented
		// extensions of the §6.2 cleanup model so an exclusive pod is reclaimed
		// rather than stranded at its terminal phase. The drain is a
		// pod-lifecycle transition and does not contradict the session
		// terminality reported by Terminal()/IsTerminal.
		{Completed, Draining},
		{Failed, Draining},
		{Cancelled, Draining},
		{Expired, Draining},
	}
}

// InvalidTransitionError is returned by IsValid for any edge not present
// in ValidTransitions(). Callers can errors.As to retrieve the typed
// value and read From/To for structured logging.
type InvalidTransitionError struct {
	From State
	To   State
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("sandbox: %q → %q is not a valid transition per spec §6.2", e.From, e.To)
}

var validSet = func() map[Transition]struct{} {
	m := make(map[Transition]struct{}, len(ValidTransitions()))
	for _, t := range ValidTransitions() {
		m[t] = struct{}{}
	}
	return m
}()

// IsValid reports whether the transition from → to is legal per the
// canonical list in ValidTransitions(). Returns nil on a legal edge and
// an *InvalidTransitionError on an illegal one.
func IsValid(from, to State) error {
	if _, ok := validSet[Transition{From: from, To: to}]; ok {
		return nil
	}
	return &InvalidTransitionError{From: from, To: to}
}
