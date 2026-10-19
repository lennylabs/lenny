// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestPodHasActiveSessionUsesSlotHolders_spec_4_6_1 pins the orphan GC's
// session lookup to the slot-holder predicate: a pod a non-terminal row names
// has an active session; a pod only a terminal row or no row names has none,
// and a recovery row whose pod_assignment the failure funnel cleared does not
// keep its earlier pod active.
//
// spec: §4.6.1 (Orphaned SandboxClaim detection), §5.2 (Post-recovery
// rehydration atomicity)
func TestPodHasActiveSessionUsesSlotHolders_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	for _, r := range []sessionstore.Session{
		{ID: "live", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-live"},
		{ID: "ended", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-ended"},
		{ID: "recovering", TenantID: "acme", State: session.StateResumePending, PodAssignment: ""},
	} {
		if err := store.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	lookup := &sessionActiveLookup{store: store}
	for pod, want := range map[string]bool{"pod-live": true, "pod-ended": false, "pod-recovered-from": false, "pod-none": false} {
		got, err := lookup.PodHasActiveSession(ctx, pod)
		if err != nil || got != want {
			t.Errorf("PodHasActiveSession(%s) = (%v, %v), want %v", pod, got, err, want)
		}
	}
}
