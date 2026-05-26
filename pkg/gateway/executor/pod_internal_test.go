// SPDX-License-Identifier: MIT

package executor

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// TestDispositionPhase_spec_6_2 covers the disposition → §6.2 Sandbox phase
// mapping the pod executor records at release time (attached →
// completed/failed/cancelled/expired). An unknown or empty disposition maps to
// the empty phase, which Release treats as "no terminal phase to record" so a
// bogus phase is never written.
func TestDispositionPhase_spec_6_2(t *testing.T) {
	cases := []struct {
		in   Disposition
		want state.State
	}{
		{DispositionCompleted, state.Completed},
		{DispositionFailed, state.Failed},
		{DispositionCancelled, state.Cancelled},
		{DispositionExpired, state.Expired},
		{"", ""},
		{Disposition("bogus"), ""},
	}
	for _, c := range cases {
		if got := dispositionPhase(c.in); got != c.want {
			t.Errorf("dispositionPhase(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
