// SPDX-License-Identifier: MIT

package slotstate

import (
	"errors"
	"testing"
)

// spec: §6.2 lines 171-176 — the per-slot sub-state edge list is exactly
// the six edges the spec enumerates, and no others are legal.
func TestValidTransitions_spec_6_2(t *testing.T) {
	want := map[Transition]bool{
		{SlotAssigned, ReceivingUploads}: true,
		{ReceivingUploads, Running}:      true,
		{Running, SlotCleanup}:           true,
		{Running, Failed}:                true,
		{SlotCleanup, Released}:          true,
		{SlotCleanup, Leaked}:            true,
	}
	got := ValidTransitions()
	if len(got) != len(want) {
		t.Fatalf("ValidTransitions() has %d edges, want %d", len(got), len(want))
	}
	for _, e := range got {
		if !want[e] {
			t.Errorf("unexpected per-slot edge %q → %q", e.From, e.To)
		}
	}
	// Every enumerated edge passes IsValid; a representative illegal edge
	// (skipping materialization) fails.
	for e := range want {
		if err := IsValid(e.From, e.To); err != nil {
			t.Errorf("IsValid(%q,%q) = %v, want nil", e.From, e.To, err)
		}
	}
	if err := IsValid(SlotAssigned, Running); err == nil {
		t.Error("slot_assigned → running must be illegal (must pass through receiving_uploads)")
	}
	var ite *InvalidTransitionError
	if err := IsValid(Leaked, Released); !errors.As(err, &ite) {
		t.Errorf("IsValid on a terminal source must return *InvalidTransitionError, got %T", err)
	}
}

// spec: §6.2 lines 175-176 — released/leaked/failed are terminal; spec
// §6.2 line 179 — a leaked slot still occupies a slot, a released one does
// not.
func TestTerminalAndOccupancy_spec_6_2(t *testing.T) {
	for _, s := range []SubState{Released, Leaked, Failed} {
		if !Terminal(s) {
			t.Errorf("Terminal(%q) = false, want true", s)
		}
	}
	for _, s := range []SubState{SlotAssigned, ReceivingUploads, Running, SlotCleanup} {
		if Terminal(s) {
			t.Errorf("Terminal(%q) = true, want false", s)
		}
	}
	if OccupiesSlot(Released) {
		t.Error("a released slot must not count toward active_slots")
	}
	for _, s := range []SubState{SlotAssigned, ReceivingUploads, Running, SlotCleanup, Leaked, Failed} {
		if !OccupiesSlot(s) {
			t.Errorf("OccupiesSlot(%q) = false, want true (§6.2 line 179 leaked still counts)", s)
		}
	}
}

// spec: §6.2 lines 170-176 — IsValidState covers exactly the seven
// sub-states and All() enumerates them.
func TestStateEnum_spec_6_2(t *testing.T) {
	if got := len(All()); got != 7 {
		t.Fatalf("All() = %d sub-states, want 7", got)
	}
	for _, s := range All() {
		if !IsValidState(s) {
			t.Errorf("IsValidState(%q) = false", s)
		}
	}
	if IsValidState("nonsense") {
		t.Error("IsValidState must reject an unknown value")
	}
}
