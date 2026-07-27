//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §6.2 graceful pod release during extended
// suspension (`maxSuspendedPodHoldSeconds`) against the Postgres-backed
// SessionStore. Once a session has been `suspended` longer than the
// configured hold window, the gateway checkpoints the workspace, releases
// the pod back to the pool, and clears the session's pod binding while
// leaving the session in `suspended` with no state change. The released
// session then sits podless until the client acts, and the hold timer does
// not fire again.
package watchdog_component_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: 6.2 (graceful pod release during extended suspension —
// maxSuspendedPodHoldSeconds), 11.3 line 233 (max suspended pod hold
// operator knob)
//
// diagnosis: the §6.2 suspended pod-hold sweep did not behave as specified
// against the Postgres-backed SessionStore. A session held `suspended` past
// `maxSuspendedPodHoldSeconds` must have its pod released and its pod
// binding cleared while the session itself stays `suspended` (podless), and
// a session still inside the window must keep its pod. A failure means
// either a long-suspended session pins a warm pod indefinitely (pool
// starvation), or the sweep took a state transition the spec forbids —
// §6.2 is explicit that the session remains in `suspended` with no state
// change — which would strand the client's resume path.
//
// §6.2 makes the release conditional on a successful checkpoint ("If
// checkpoint fails: the gateway does NOT release the pod"). This test drives
// the checkpoint-succeeds branch, so an implementation that adds a checkpoint
// hook to the watchdog must wire a succeeding one here; the checkpoint-fails
// branch (pod held, warning event, retry on the next interval) needs its own
// case.
func TestWatchdogReleasesPodAfterSuspendedHoldWindow_spec_6_2(t *testing.T) {
	t.Skip("the §6.2 pod-release-during-suspension sweep is unbuilt: the watchdog has no suspended pod-hold pass, so a podless-suspended session row is unreachable")
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	tick := time.Now().UTC().Truncate(time.Microsecond)

	// heldID entered `suspended` 1000s before the tick, past the 900s hold
	// window, so the sweep must checkpoint and release its pod. freshID
	// entered `suspended` 60s before the tick and is well inside the window,
	// so it must keep its binding: §6.2 releases the pod only after the
	// session "has been in `suspended` state for longer than
	// `maxSuspendedPodHoldSeconds`".
	overdueID := newUUID(t)
	freshID := newUUID(t)
	born := tick.Add(-2 * time.Hour)
	overdueSince := tick.Add(-1000 * time.Second)
	freshSince := tick.Add(-60 * time.Second)

	if err := store.Create(ctx, sessionstore.Session{
		ID: overdueID, TenantID: tenant, State: session.StateSuspended,
		RuntimeRef: "echo", PoolRef: "pool-a", PodAssignment: "pod-overdue",
		CreatedAt: born, UpdatedAt: overdueSince,
	}); err != nil {
		t.Fatalf("create overdue row: %v", err)
	}
	if err := store.Create(ctx, sessionstore.Session{
		ID: freshID, TenantID: tenant, State: session.StateSuspended,
		RuntimeRef: "echo", PoolRef: "pool-a", PodAssignment: "pod-fresh",
		CreatedAt: born, UpdatedAt: freshSince,
	}); err != nil {
		t.Fatalf("create fresh row: %v", err)
	}

	// Only the suspended pod-hold window is armed. Every other platform clock
	// is set far out of reach so no other sweep can claim either row: §6.2
	// pauses `maxSessionAge` and `maxClientIdleSeconds` during `suspended`, and
	// this test asserts the pod-hold edge alone.
	huge := watchdog.DefaultMaxSessionAgeSeconds * 100
	w := watchdog.New(store, watchdog.StaticTenants{tenant}, watchdog.Config{
		MaxSuspendedPodHoldSeconds:     900,
		MaxSessionAgeSeconds:           huge,
		MaxIdleSeconds:                 huge,
		MaxAwaitingClientActionSeconds: huge,
		MaxResumePendingSeconds:        huge,
		MaxResumingSeconds:             huge,
		MaxFinalizingSeconds:           huge,
	}, nil)

	if _, err := w.Tick(ctx, tick); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// §6.2: on a successful checkpoint the gateway "releases the pod back to
	// the pool, clears the session's pod binding ... The session remains in
	// `suspended` — no state change."
	overdue, err := store.Get(ctx, tenant, overdueID)
	if err != nil {
		t.Fatalf("get overdue row: %v", err)
	}
	if overdue.State != session.StateSuspended {
		t.Errorf("overdue row state = %q, want suspended (§6.2: no state change on pod release)", overdue.State)
	}
	if overdue.PodAssignment != "" {
		t.Errorf("overdue row pod assignment = %q, want cleared after the hold window", overdue.PodAssignment)
	}

	fresh, err := store.Get(ctx, tenant, freshID)
	if err != nil {
		t.Fatalf("get fresh row: %v", err)
	}
	if fresh.State != session.StateSuspended {
		t.Errorf("fresh row state = %q, want suspended", fresh.State)
	}
	if fresh.PodAssignment != "pod-fresh" {
		t.Errorf("fresh row pod assignment = %q, want pod-fresh retained inside the hold window", fresh.PodAssignment)
	}

	// §6.2: "The `maxSuspendedPodHoldSeconds` timer stops (it has served its
	// purpose)" and the released session "stays in `suspended` (podless) until
	// the client acts". A later tick must therefore leave the released row
	// exactly as it is rather than re-firing the release or expiring it.
	if _, err := w.Tick(ctx, tick.Add(time.Hour)); err != nil {
		t.Fatalf("second Tick: %v", err)
	}
	after, err := store.Get(ctx, tenant, overdueID)
	if err != nil {
		t.Fatalf("re-get overdue row: %v", err)
	}
	if after.State != session.StateSuspended || after.PodAssignment != "" {
		t.Errorf("released row after a later tick = state %q pod %q, want suspended podless", after.State, after.PodAssignment)
	}
}
