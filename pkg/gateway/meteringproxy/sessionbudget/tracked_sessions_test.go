// SPDX-License-Identifier: MIT

package sessionbudget

import (
	"context"
	"sort"
	"testing"
)

// spec: §11.2 (budget enforcement), §10.1.1 (replica-local state)
//
// TrackedSessions lists every session the enforcer holds a counter for, so
// the per-replica budget state sweep can resolve them, and stops listing a
// session once it is forgotten. A nil enforcer lists nothing.
func TestEnforcerTrackedSessionsListsCounters_spec_11_2(t *testing.T) {
	e := New(&recordingTerminator{}, nil, nil)
	ctx := context.Background()
	for _, id := range []string{"s_b", "s_a"} {
		e.Record(ctx, ctx, "acme", id, 1000, 1)
	}
	got := e.TrackedSessions()
	sort.Strings(got)
	if len(got) != 2 || got[0] != "s_a" || got[1] != "s_b" {
		t.Fatalf("TrackedSessions = %v, want [s_a s_b]", got)
	}
	e.Forget("s_a")
	if got := e.TrackedSessions(); len(got) != 1 || got[0] != "s_b" {
		t.Fatalf("TrackedSessions after Forget = %v, want [s_b]", got)
	}
	var nilEnforcer *Enforcer
	if got := nilEnforcer.TrackedSessions(); got != nil {
		t.Fatalf("nil enforcer TrackedSessions = %v, want nil", got)
	}
}
