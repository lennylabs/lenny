// SPDX-License-Identifier: MIT

// Package slotstate defines the §6.2 concurrent-workspace per-slot
// sub-state machine and a registry that tracks each slotId's sub-state.
//
// A concurrent-workspace pod has a two-level state model (spec §6.2 line
// 194): the pod-level phase (pkg/sandbox/state) tracks the pod's overall
// availability (idle / slot_active / draining), while these per-slot
// sub-states track each individual slot's progress through workspace
// materialization, execution, and cleanup. The sub-states are tracked per
// slotId, not as a pod-level phase (spec §6.2 line 170).
//
// SubState and ValidTransitions() form the authoritative contract for the
// per-slot edges in spec §6.2 lines 171-176. IsValid returns nil for an
// edge present in ValidTransitions() and an InvalidTransitionError
// otherwise.
package slotstate

import "fmt"

// SubState is a slot's per-slotId sub-state, distinct from the pod-level
// Sandbox phase. spec: §6.2 lines 170-176.
type SubState string

const (
	// SlotAssigned is the initial sub-state once a slotId is allocated
	// (the pod-level idle → slot_active assignment has succeeded).
	SlotAssigned SubState = "slot_assigned"
	// ReceivingUploads is the workspace-materialization sub-state for one
	// slot. spec: §6.2 line 171.
	ReceivingUploads SubState = "receiving_uploads"
	// Running is the dispatched sub-state: the slot's workspace is ready
	// and the task has been dispatched to the runtime with the slotId.
	// spec: §6.2 line 172.
	Running SubState = "running"
	// SlotCleanup is the post-execution cleanup sub-state (task completed
	// or failed, per-slot cleanup runs). spec: §6.2 line 173.
	SlotCleanup SubState = "slot_cleanup"
	// Released is the terminal sub-state for a slot whose workspace was
	// removed, processes killed, and slotId released. spec: §6.2 line 175.
	Released SubState = "released"
	// Leaked is the terminal sub-state for a slot whose cleanup timed out:
	// the slot is not reclaimed until pod termination and remains counted
	// in active_slots. spec: §6.2 line 176, line 179.
	Leaked SubState = "leaked"
	// Failed is the terminal sub-state for a slot that hit a non-retryable
	// error (OOM, workspace validation, policy rejection). spec: §6.2
	// line 174.
	Failed SubState = "failed"
)

// All returns every per-slot sub-state in spec narrative order.
func All() []SubState {
	return []SubState{
		SlotAssigned, ReceivingUploads, Running, SlotCleanup,
		Released, Leaked, Failed,
	}
}

// IsValidState reports whether s is one of the §6.2 per-slot sub-states.
func IsValidState(s SubState) bool {
	switch s {
	case SlotAssigned, ReceivingUploads, Running, SlotCleanup, Released, Leaked, Failed:
		return true
	}
	return false
}

// Terminal reports whether s is a sub-state with no outgoing edge:
// released (reclaimed), leaked (awaiting pod termination), or failed.
func Terminal(s SubState) bool {
	switch s {
	case Released, Leaked, Failed:
		return true
	}
	return false
}

// OccupiesSlot reports whether a slot in sub-state s still counts toward
// the pod's active_slots. Every sub-state except Released occupies a
// slot; in particular a Leaked slot "remains counted in active_slots
// (preventing the gateway from over-assigning new slots that would
// conflict with the leaked slot's unreleased resources)". spec: §6.2
// line 179. A Failed slot's pod is replaced as a whole, but until the pod
// terminates the slot is still allocated, so it occupies one too.
func OccupiesSlot(s SubState) bool {
	return s != Released
}

// Transition is one directed per-slot sub-state edge.
type Transition struct {
	From SubState
	To   SubState
}

// ValidTransitions is the canonical per-slot sub-state edge list, spec
// §6.2 lines 171-176:
//
//	slot_assigned     → receiving_uploads   (workspace materialization begins)
//	receiving_uploads → running             (workspace ready, task dispatched)
//	running           → slot_cleanup        (task completes or fails)
//	running           → failed              (non-retryable error)
//	slot_cleanup      → released            (slot reclaimed)
//	slot_cleanup      → leaked              (cleanup timeout exceeded)
func ValidTransitions() []Transition {
	return []Transition{
		{SlotAssigned, ReceivingUploads},
		{ReceivingUploads, Running},
		{Running, SlotCleanup},
		{Running, Failed},
		{SlotCleanup, Released},
		{SlotCleanup, Leaked},
	}
}

// InvalidTransitionError is returned by IsValid for any per-slot edge not
// present in ValidTransitions().
type InvalidTransitionError struct {
	From SubState
	To   SubState
}

func (e *InvalidTransitionError) Error() string {
	return fmt.Sprintf("slotstate: %q → %q is not a valid per-slot transition per spec §6.2", e.From, e.To)
}

var validSet = func() map[Transition]struct{} {
	m := make(map[Transition]struct{}, len(ValidTransitions()))
	for _, t := range ValidTransitions() {
		m[t] = struct{}{}
	}
	return m
}()

// IsValid reports whether the per-slot transition from → to is legal per
// ValidTransitions(). Returns nil on a legal edge and an
// *InvalidTransitionError otherwise.
func IsValid(from, to SubState) error {
	if _, ok := validSet[Transition{From: from, To: to}]; ok {
		return nil
	}
	return &InvalidTransitionError{From: from, To: to}
}
