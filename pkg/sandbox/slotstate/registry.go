// SPDX-License-Identifier: MIT

package slotstate

import "sync"

// Registry is a concurrency-safe tracker of per-slot sub-states keyed by
// slotId, scoped to the slots' owning pods. It is the in-process record of
// each slot's §6.2 sub-state so the gateway can report the per-pod
// leaked-slot count (the lenny_adapter_leaked_slots gauge, spec §6.2 line
// 179) and the active-slot occupancy that gates further assignment.
//
// The registry enforces the per-slot state machine on Transition: an edge
// absent from ValidTransitions() is rejected. MarkLeaked and MarkReleased
// record the terminal cleanup outcomes the gateway observes directly when
// a slot's cleanup completes or times out, seeding the slot if it was not
// already tracked through its full lifecycle (the intermediate
// materialization and dispatch transitions run in the in-pod adapter).
type Registry struct {
	mu    sync.Mutex
	slots map[string]slotRecord
}

type slotRecord struct {
	pod   string
	pool  string
	state SubState
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{slots: make(map[string]slotRecord)}
}

// Assign records a newly-allocated slot in the SlotAssigned sub-state. It
// is idempotent for an already-tracked slot (the pod/pool are not
// rewritten); a caller re-driving the lifecycle starts a fresh slot under
// a fresh slotId.
func (r *Registry) Assign(slotID, pod, pool string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.slots[slotID]; ok {
		return
	}
	r.slots[slotID] = slotRecord{pod: pod, state: SlotAssigned, pool: pool}
}

// Transition advances a tracked slot to sub-state to, enforcing the §6.2
// per-slot edge list. It returns an *InvalidTransitionError on an illegal
// edge and errUnknownSlot when the slotId is not tracked. On reaching
// Released the slot is dropped (it is reclaimed and no longer occupies a
// slot); Leaked and Failed slots are retained until the pod terminates so
// the leaked/active counts stay accurate.
func (r *Registry) Transition(slotID string, to SubState) error {
	if r == nil {
		return errUnknownSlot
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.slots[slotID]
	if !ok {
		return errUnknownSlot
	}
	if err := IsValid(rec.state, to); err != nil {
		return err
	}
	if to == Released {
		delete(r.slots, slotID)
		return nil
	}
	rec.state = to
	r.slots[slotID] = rec
	return nil
}

// MarkReleased records that a slot's cleanup reclaimed it, dropping it
// from the registry. It is a no-op for an untracked slot (the gateway does
// not record every healthy slot's full lifecycle; an untracked release is
// simply a clean teardown of a slot the registry never saw leak).
func (r *Registry) MarkReleased(slotID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.slots, slotID)
}

// MarkLeaked records a slot whose cleanup timed out, so it remains counted
// in the pod's active_slots and leaked_slots until the pod terminates
// (spec §6.2 line 176, line 179). It seeds the slot at Leaked if the
// registry was not already tracking it. MarkLeaked returns the pod's
// resulting leaked-slot count so the caller can publish the
// lenny_adapter_leaked_slots gauge.
func (r *Registry) MarkLeaked(slotID, pod, pool string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec := r.slots[slotID]
	rec.state = Leaked
	if rec.pod == "" {
		rec.pod = pod
	}
	if rec.pool == "" {
		rec.pool = pool
	}
	r.slots[slotID] = rec
	return r.leakedCountLocked(rec.pod)
}

// State returns the tracked sub-state of slotID and whether it is tracked.
func (r *Registry) State(slotID string) (SubState, bool) {
	if r == nil {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rec, ok := r.slots[slotID]
	return rec.state, ok
}

// LeakedCount returns the number of slots in the Leaked sub-state for pod.
func (r *Registry) LeakedCount(pod string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.leakedCountLocked(pod)
}

func (r *Registry) leakedCountLocked(pod string) int {
	n := 0
	for _, rec := range r.slots {
		if rec.pod == pod && rec.state == Leaked {
			n++
		}
	}
	return n
}

// ActiveCount returns the number of slots that occupy capacity on pod: per
// §6.2 line 179 every tracked slot except a Released one (which is dropped)
// counts, so leaked slots are included and gate further assignment.
func (r *Registry) ActiveCount(pod string) int {
	if r == nil {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, rec := range r.slots {
		if rec.pod == pod && OccupiesSlot(rec.state) {
			n++
		}
	}
	return n
}

// ForgetPod drops every slot tracked for pod. The gateway calls it when a
// pod terminates: a terminated pod's leaked slots are reclaimed with the
// pod, so they no longer count toward active_slots or leaked_slots.
func (r *Registry) ForgetPod(pod string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, rec := range r.slots {
		if rec.pod == pod {
			delete(r.slots, id)
		}
	}
}

// errUnknownSlot is returned by Transition for a slotId the registry is
// not tracking.
var errUnknownSlot = &unknownSlotError{}

type unknownSlotError struct{}

func (*unknownSlotError) Error() string { return "slotstate: slot is not tracked" }
