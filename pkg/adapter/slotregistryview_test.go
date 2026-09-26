// SPDX-License-Identifier: MIT

package adapter

import (
	"testing"
)

// InspectSlotRegistry reports each registry state the §4.7.1 rules read for
// one identifier: no entry, an entry stamped at creation, a started entry,
// an untokened entry, and an identifier under the §5.2 reclaim hold. The
// in-process conformance battery names the state behind a failure through
// it, so a view that misreported any field would misdirect that diagnosis.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func TestInspectSlotRegistryReportsEachRegistryState_spec_4_7_1(t *testing.T) {
	s, _ := orderingPod(t)

	if got := s.InspectSlotRegistry("alice"); got != (SlotRegistryView{}) {
		t.Fatalf("view of an unknown identifier = %+v, want the zero view", got)
	}

	if err := assign(s, "alice", attempt1); err != nil {
		t.Fatalf("assign: %v", err)
	}
	want := SlotRegistryView{Entry: true, BindAttempt: attempt1}
	if got := s.InspectSlotRegistry("alice"); got != want {
		t.Fatalf("view of a bound, unstarted entry = %+v, want %+v", got, want)
	}

	if err := start(s, "alice"); err != nil {
		t.Fatalf("start: %v", err)
	}
	want.Started = true
	if got := s.InspectSlotRegistry("alice"); got != want {
		t.Fatalf("view of a started entry = %+v, want %+v", got, want)
	}

	if err := start(s, "bob"); err != nil {
		t.Fatalf("start bob: %v", err)
	}
	if got, want := s.InspectSlotRegistry("bob"), (SlotRegistryView{Entry: true, Started: true}); got != want {
		t.Fatalf("view of an untokened started entry = %+v, want %+v", got, want)
	}

	s.mu.Lock()
	_, removed, _, release := s.reclaimSlotLocked("alice")
	s.mu.Unlock()
	if !removed {
		t.Fatal("reclaimSlotLocked removed nothing")
	}
	if got, want := s.InspectSlotRegistry("alice"), (SlotRegistryView{ReclaimHeld: true}); got != want {
		t.Fatalf("view during the reclaim hold = %+v, want %+v", got, want)
	}
	release()
	if got := s.InspectSlotRegistry("alice"); got != (SlotRegistryView{}) {
		t.Fatalf("view after the hold ended = %+v, want the zero view", got)
	}
}
