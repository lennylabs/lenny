// SPDX-License-Identifier: MIT

package session

import "testing"

// TestCountsTowardConcurrencyCapsExcludesOnlySuspendedAndTerminal pins the
// concurrent-session count predicate: every non-terminal state counts,
// including the recovery states and the internal resuming state, except
// suspended. A predicate that also counted suspended would keep an idle
// session holding a cap slot for up to the suspended-session lifetime.
//
// spec: §11.1 (Concurrent-session count)
func TestCountsTowardConcurrencyCapsExcludesOnlySuspendedAndTerminal(t *testing.T) {
	want := map[State]bool{
		StateCreated:              true,
		StateFinalizing:           true,
		StateReady:                true,
		StateStarting:             true,
		StateRunning:              true,
		StateInputRequired:        true,
		StateResumePending:        true,
		StateResuming:             true,
		StateAwaitingClientAction: true,
		StateSuspended:            false,
		StateCompleted:            false,
		StateFailed:               false,
		StateCancelled:            false,
		StateExpired:              false,
	}
	for _, s := range append(AllStates(), StateResuming) {
		w, ok := want[s]
		if !ok {
			t.Errorf("state %q has no expected count outcome; add it to this table", s)
			continue
		}
		if got := CountsTowardConcurrencyCaps(s); got != w {
			t.Errorf("CountsTowardConcurrencyCaps(%q) = %v, want %v", s, got, w)
		}
	}
	// Every recovery state counts, so the §16.5 ratio's numerator is a
	// subset of its denominator.
	for _, s := range RecoveryStates() {
		if !CountsTowardConcurrencyCaps(s) {
			t.Errorf("recovery state %q is not counted", s)
		}
	}
}
