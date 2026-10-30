// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
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

// TestSessionLookupHoldsThePostTerminalWindow_spec_4_6_1 pins the orphan
// GC's session lookup to the §7.1 pod-holder predicate under the
// controller's cap: a pod whose only session is terminal, unreleased, and
// inside claimOrphanTimeout has an active session, and it has none once the
// release is recorded or claimOrphanTimeout passes. The store the controller
// builds carries the orphan timeout the GC loop applies as its cap.
//
// spec: §4.6.1 (Orphaned SandboxClaim detection), §7.1 (Post-terminal window)
func TestSessionLookupHoldsThePostTerminalWindow_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	const orphan = 15 * time.Minute
	store := memstore.NewWithClock(func() time.Time { return now }, memstore.WithPostTerminalCap(orphan))
	for _, r := range []sessionstore.Session{
		{ID: "sealing", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-sealing", TerminatedAt: now.Add(-time.Minute)},
		{ID: "released", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-released", TerminatedAt: now.Add(-time.Minute), PodReleasedAt: now},
		{ID: "old", TenantID: "acme", State: session.StateFailed, PodAssignment: "pod-old", TerminatedAt: now.Add(-orphan)},
	} {
		if err := store.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	lookup := &sessionActiveLookup{store: store}
	for pod, want := range map[string]bool{"pod-sealing": true, "pod-released": false, "pod-old": false} {
		if got, err := lookup.PodHasActiveSession(ctx, pod); err != nil || got != want {
			t.Errorf("PodHasActiveSession(%s) = (%v, %v), want %v", pod, got, err, want)
		}
	}
}

// TestNewSessionLookupCapsTheWindowAtTheOrphanTimeout_spec_4_6_1 pins the
// controller's store construction: its post-terminal cap is the orphan
// timeout the GC loop applies, the flag value when positive and the §4.6.1
// default otherwise.
//
// spec: §4.6.1 (Orphaned SandboxClaim detection), §7.1 (Post-terminal window)
func TestNewSessionLookupCapsTheWindowAtTheOrphanTimeout_spec_4_6_1(t *testing.T) {
	for flagValue, want := range map[time.Duration]time.Duration{
		20 * time.Minute: 20 * time.Minute,
		0:                15 * time.Minute,
		-time.Second:     15 * time.Minute,
	} {
		l := newSessionLookup(nil, flagValue)
		pg, ok := l.store.(*sessionstorepg.Store)
		if !ok {
			t.Fatalf("lookup store is %T, want the Postgres store", l.store)
		}
		if got := pg.PostTerminalCap(); got != want {
			t.Errorf("newSessionLookup(%v) cap = %v, want %v", flagValue, got, want)
		}
	}
}
