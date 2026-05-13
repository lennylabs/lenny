// Package state defines the Sandbox CRD state machine, per spec §6.2.
//
// Phase 1 ships the state enum and the canonical transition list.
// IsValid is a Phase 2 deliverable; in Phase 1 it returns
// ErrNotImplemented.
package state

import "errors"

// State is the Sandbox phase, written to Sandbox.status.phase.
type State string

const (
	Warming              State = "warming"
	SDKConnecting        State = "sdk_connecting"
	Idle                 State = "idle"
	Claimed              State = "claimed"
	ReceivingUploads     State = "receiving_uploads"
	FinalizingWorkspace  State = "finalizing_workspace"
	RunningSetup         State = "running_setup"
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
		Warming, SDKConnecting, Idle, Claimed, ReceivingUploads,
		FinalizingWorkspace, RunningSetup, Attached, TaskCleanup,
		Resuming, Suspended, ResumePending, AwaitingClientAction,
		Completed, Failed, Cancelled, Expired, Draining, Terminated,
	}
}

func Terminal() []State {
	return []State{Failed, Cancelled, Expired, Terminated}
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
// the suspension/recovery loop, and the task-cleanup edges. The
// host-schedulable gate (lenny.dev/host-schedulable: "true") chooses
// between task_cleanup → sdk_connecting/idle (schedulable) and
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
		{RunningSetup, Attached},
		{RunningSetup, Failed},
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
		// Drain and terminate.
		{Idle, Draining},
		{Claimed, Draining},
		{Attached, Draining},
		{Draining, Terminated},
	}
}

var ErrNotImplemented = errors.New("sandbox-state IsValid: not implemented in Phase 1 (see TESTING.md §13.1)")

// IsValid reports whether the transition from → to is legal.
//
// Phase 1 stub. Phase 2 implements; Phase 3.5 adds the host-schedulable
// label check via a separate guard function.
func IsValid(from, to State) error {
	_ = from
	_ = to
	return ErrNotImplemented
}
