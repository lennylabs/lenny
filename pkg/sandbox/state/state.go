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

const (
	Warming              State = "warming"
	SDKConnecting        State = "sdk_connecting"
	Idle                 State = "idle"
	Claimed              State = "claimed"
	SlotActive           State = "slot_active"
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
		Warming, SDKConnecting, Idle, Claimed, SlotActive, ReceivingUploads,
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
		// Drain and terminate.
		{Idle, Draining},
		{Claimed, Draining},
		{Attached, Draining},
		{Draining, Terminated},
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
