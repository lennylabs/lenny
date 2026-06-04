// SPDX-License-Identifier: MIT

package slotstate

import (
	"errors"
	"sync"
	"testing"
)

// spec: §6.2 lines 171-175 — the registry drives a slot through the legal
// lifecycle and drops it on Released (reclaimed, no longer occupies a
// slot).
func TestRegistryLifecycleToReleased_spec_6_2(t *testing.T) {
	r := NewRegistry()
	r.Assign("slot-1", "pod-a", "default-gvisor")
	if s, ok := r.State("slot-1"); !ok || s != SlotAssigned {
		t.Fatalf("after Assign: state=%q ok=%v, want slot_assigned/true", s, ok)
	}
	for _, to := range []SubState{ReceivingUploads, Running, SlotCleanup, Released} {
		if err := r.Transition("slot-1", to); err != nil {
			t.Fatalf("Transition(slot-1, %q) = %v", to, err)
		}
	}
	if _, ok := r.State("slot-1"); ok {
		t.Error("a released slot must be dropped from the registry")
	}
	if r.ActiveCount("pod-a") != 0 || r.LeakedCount("pod-a") != 0 {
		t.Errorf("after release: active=%d leaked=%d, want 0/0",
			r.ActiveCount("pod-a"), r.LeakedCount("pod-a"))
	}
}

// spec: §6.2 line 176 — an illegal edge is rejected and an untracked slot
// transition errors.
func TestRegistryRejectsIllegalAndUnknown_spec_6_2(t *testing.T) {
	r := NewRegistry()
	r.Assign("slot-1", "pod-a", "p")
	var ite *InvalidTransitionError
	if err := r.Transition("slot-1", Running); !errors.As(err, &ite) {
		t.Errorf("slot_assigned → running must be *InvalidTransitionError, got %v", err)
	}
	if err := r.Transition("ghost", Released); err == nil {
		t.Error("transitioning an untracked slot must error")
	}
}

// spec: §6.2 line 176, line 179 — a slot whose cleanup times out is leaked,
// stays counted in active_slots, and feeds the per-pod leaked count; a
// tracked slot reaching slot_cleanup can then be marked leaked.
func TestRegistryLeak_spec_6_2(t *testing.T) {
	r := NewRegistry()
	// Slot the gateway tracked through its lifecycle into cleanup.
	r.Assign("slot-1", "pod-a", "p")
	_ = r.Transition("slot-1", ReceivingUploads)
	_ = r.Transition("slot-1", Running)
	_ = r.Transition("slot-1", SlotCleanup)
	if n := r.MarkLeaked("slot-1", "pod-a", "p"); n != 1 {
		t.Fatalf("MarkLeaked returned %d, want 1", n)
	}
	if s, ok := r.State("slot-1"); !ok || s != Leaked {
		t.Fatalf("slot-1 state=%q ok=%v, want leaked/true", s, ok)
	}
	// A leaked slot still occupies capacity (§6.2 line 179).
	if r.ActiveCount("pod-a") != 1 {
		t.Errorf("leaked slot must remain in active_slots, active=%d", r.ActiveCount("pod-a"))
	}
	// Slot the gateway never tracked: MarkLeaked seeds it directly.
	if n := r.MarkLeaked("slot-2", "pod-a", "p"); n != 2 {
		t.Fatalf("seeding leak returned %d, want 2", n)
	}
	if r.LeakedCount("pod-a") != 2 {
		t.Errorf("LeakedCount = %d, want 2", r.LeakedCount("pod-a"))
	}
	// Leaks are scoped per pod.
	if r.LeakedCount("pod-b") != 0 {
		t.Errorf("pod-b leaked count = %d, want 0", r.LeakedCount("pod-b"))
	}
}

// spec: §6.2 line 179 — leaked slots are reclaimed when the pod terminates,
// so ForgetPod clears the pod's leaked/active counts.
func TestRegistryForgetPod_spec_6_2(t *testing.T) {
	r := NewRegistry()
	r.MarkLeaked("slot-1", "pod-a", "p")
	r.MarkLeaked("slot-2", "pod-a", "p")
	r.MarkLeaked("slot-3", "pod-b", "p")
	r.ForgetPod("pod-a")
	if r.LeakedCount("pod-a") != 0 {
		t.Errorf("after ForgetPod(pod-a): leaked=%d, want 0", r.LeakedCount("pod-a"))
	}
	if r.LeakedCount("pod-b") != 1 {
		t.Errorf("pod-b leaked count must be untouched, got %d", r.LeakedCount("pod-b"))
	}
}

// MarkReleased drops a tracked leaked slot (e.g. a late reclaim) and is a
// no-op for an untracked slot.
func TestRegistryMarkReleased(t *testing.T) {
	r := NewRegistry()
	r.MarkLeaked("slot-1", "pod-a", "p")
	r.MarkReleased("slot-1")
	if r.LeakedCount("pod-a") != 0 {
		t.Errorf("MarkReleased must drop the slot, leaked=%d", r.LeakedCount("pod-a"))
	}
	r.MarkReleased("never-tracked") // must not panic
}

// The registry tolerates concurrent slot updates without racing (run under
// -race).
func TestRegistryConcurrent(t *testing.T) {
	r := NewRegistry()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := string(rune('a'+i%26)) + string(rune('0'+i/26))
			r.Assign(id, "pod-a", "p")
			_ = r.Transition(id, ReceivingUploads)
			_ = r.Transition(id, Running)
			_ = r.Transition(id, SlotCleanup)
			if i%2 == 0 {
				_ = r.Transition(id, Released)
			} else {
				r.MarkLeaked(id, "pod-a", "p")
			}
		}(i)
	}
	wg.Wait()
	if got := r.LeakedCount("pod-a"); got != 25 {
		t.Errorf("LeakedCount = %d, want 25", got)
	}
}
