// SPDX-License-Identifier: MIT

package tokenanomaly_test

import (
	"sort"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/session/tokenanomaly"
)

// spec: §11.2 (direct-mode integrity control), §10.1.1 (replica-local state)
//
// TrackedSessions lists every session the detector holds an accumulator
// for, so the per-replica budget state sweep can resolve them, and stops
// listing a session once it is forgotten. A nil detector lists nothing.
func TestDetectorTrackedSessionsListsAccumulators_spec_11_2(t *testing.T) {
	d, _ := newDetector(t, tokenanomaly.Config{})
	d.Observe("acme", "s_b", 1, 1)
	d.Observe("acme", "s_a", 0, 0)
	got := d.TrackedSessions()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "s_a" || got[1] != "s_b" {
		t.Fatalf("TrackedSessions = %v, want [s_a s_b]", got)
	}
	d.Forget("s_b")
	if got := d.TrackedSessions(); len(got) != 1 || got[0] != "s_a" {
		t.Fatalf("TrackedSessions after Forget = %v, want [s_a]", got)
	}
	var nilDetector *tokenanomaly.Detector
	if got := nilDetector.TrackedSessions(); got != nil {
		t.Fatalf("nil detector TrackedSessions = %v, want nil", got)
	}
}
