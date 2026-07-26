// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// errFenceRelinquished models the coordfence terminal-failure return the
// production Readopter surfaces after its retry budget is exhausted.
var errFenceRelinquished = errors.New("coordfence: relinquished")

// readoptCall records one ReadoptAndFence invocation so a test can assert
// the generation the pod was fenced to and whether the binding was
// published (the publish callback ran only after the fence acknowledged).
type readoptCall struct {
	sessionID  string
	generation int64
	published  bool
}

// fakeReadopter is an in-memory Readopter for the crash-takeover tests. On a
// non-failing session it returns a publish callback that flips the fake
// binding to bound (modeling the production podRegistry.Put that holds the
// re-established connection open). On a failing session it models the
// coordfence relinquish by releasing the lease and returning an error, so
// the Sweeper records an adoption backoff and publishes no binding.
type fakeReadopter struct {
	fail      map[string]bool
	leases    *fakeLeases
	tenantID  string
	replicaID string
	bindings  *fakeBindings
	calls     []*readoptCall
}

func (r *fakeReadopter) ReadoptAndFence(_ context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	call := &readoptCall{sessionID: sessionID, generation: generation}
	r.calls = append(r.calls, call)
	if r.fail[sessionID] {
		// Terminal fence failure: coordfence relinquishes the lease.
		_ = r.leases.Release(context.Background(), tenantID, sessionID, r.replicaID)
		return nil, errFenceRelinquished
	}
	return func() {
		call.published = true
		if r.bindings != nil {
			r.bindings.bound[sessionID] = true
			r.bindings.alive[sessionID] = true
		}
	}, nil
}

// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
// CoordinatorFence precondition; no operational RPC before the fence
// acknowledges), §4.2 line 156 (generation bump on handoff). On the
// crash-takeover edge the Sweeper bumps coordination_generation, drives the
// re-adopt fence with the post-bump generation, and publishes the binding
// only after the fence acknowledges — exactly once per handoff. The next
// sweep observes the published binding and renews without re-bumping or
// re-fencing. Regression: anchoring the bump on the orphan-adoption edge
// (priorHolder empty) is required, because a TTL-lapse takeover leaves the
// prior holder unobserved; the pre-fix predicate never fired here.
func TestSweepCrashTakeoverFencesPublishesOncePerHandoff_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "orphan", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-orphan"})

	leases := newFakeLeases()
	bindings := newFakeBindings()
	readopter := &fakeReadopter{leases: leases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID: "rep-1",
		Bindings:  bindings,
		Readopter: readopter,
	})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("held = %d, want 1 (orphan adopted)", held)
	}
	got, _ := sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Fatalf("generation = %d, want 1 (takeover bumped once)", got.CoordinationGeneration)
	}
	if len(readopter.calls) != 1 {
		t.Fatalf("ReadoptAndFence calls = %d, want 1", len(readopter.calls))
	}
	if readopter.calls[0].generation != 1 {
		t.Errorf("fenced generation = %d, want 1 (the post-bump generation)", readopter.calls[0].generation)
	}
	if !readopter.calls[0].published {
		t.Errorf("binding was not published after the fence acknowledged")
	}
	if !bindings.bound["orphan"] {
		t.Errorf("binding not present after takeover publish")
	}

	// The next sweep observes the published binding and renews the lease
	// without re-driving the handoff bump or the fence.
	held, err = sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("held = %d, want 1 (bound session renewed)", held)
	}
	got, _ = sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Errorf("generation = %d, want 1 (no re-bump on the renew sweep)", got.CoordinationGeneration)
	}
	if len(readopter.calls) != 1 {
		t.Errorf("ReadoptAndFence calls = %d, want 1 (fence fires once per handoff)", len(readopter.calls))
	}
}

// spec: §10.1 line 35 (relinquish-and-backoff), §4.2 line 156. On a terminal
// fence failure the coordfence driver relinquishes the lease; the Sweeper
// publishes no binding and records a per-session adoption backoff so the
// fixed sweep does not re-adopt inside the jittered window and re-drive
// RecordHandoff and the fence every sweep. The generation increment stays in
// the store. After the backoff elapses the session is re-adopted.
//
// Regression: without the backoff the immediately following sweep re-adopts
// the relinquished lapsed lease and re-bumps the generation on every sweep;
// this asserts the generation does not climb on the next sweep and does
// climb once the window elapses.
func TestSweepCrashTakeoverTerminalFenceRelinquishesAndBacksOff_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "orphan", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-orphan"})

	leases := newFakeLeases()
	readopter := &fakeReadopter{fail: map[string]bool{"orphan": true}, leases: leases, tenantID: "acme", replicaID: "rep-1"}

	now := time.Unix(1000, 0).UTC()
	clock := func() time.Time { return now }

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID:       "rep-1",
		Readopter:       readopter,
		AdoptionBackoff: 10 * time.Second,
		Clock:           clock,
	})

	// First sweep: takeover, fence fails, lease relinquished, backoff set.
	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("held = %d, want 0 (lease relinquished on terminal fence failure)", held)
	}
	if h, ok := leases.held("acme", "orphan"); ok {
		t.Fatalf("lease still held by %q after relinquish, want released", h)
	}
	got, _ := sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Fatalf("generation = %d, want 1 (bump stays after relinquish)", got.CoordinationGeneration)
	}
	if len(readopter.calls) != 1 {
		t.Fatalf("ReadoptAndFence calls = %d, want 1", len(readopter.calls))
	}

	// Second sweep inside the backoff window: not re-adopted, no re-bump.
	now = now.Add(5 * time.Second)
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	got, _ = sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Errorf("generation = %d, want 1 (no re-adopt inside the backoff window)", got.CoordinationGeneration)
	}
	if len(readopter.calls) != 1 {
		t.Errorf("ReadoptAndFence calls = %d, want 1 (no re-fence inside the backoff window)", len(readopter.calls))
	}
	if h, ok := leases.held("acme", "orphan"); ok {
		t.Errorf("lease re-acquired by %q inside the backoff window, want unheld", h)
	}

	// Third sweep after the backoff elapses: re-adopted, generation climbs.
	now = now.Add(10 * time.Second)
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("third Sweep: %v", err)
	}
	got, _ = sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 2 {
		t.Errorf("generation = %d, want 2 (re-adopted after the backoff elapsed)", got.CoordinationGeneration)
	}
	if len(readopter.calls) != 2 {
		t.Errorf("ReadoptAndFence calls = %d, want 2 (re-fenced after the backoff elapsed)", len(readopter.calls))
	}
}

// failingUpdateStore wraps a session store and fails its first failUpdates
// Update calls, so a test can model a transient store error during the
// crash-takeover generation bump. Every other method delegates to the
// wrapped store, and Update falls through once the failure budget is spent.
type failingUpdateStore struct {
	sessionstore.Store
	failUpdates int
}

func (s *failingUpdateStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.failUpdates > 0 {
		s.failUpdates--
		return sessionstore.Session{}, errors.New("transient store error during generation bump")
	}
	return s.Store.Update(ctx, tenantID, id, mutate)
}

// spec: §10.1 (coordinator handoff fences the pod to the post-handoff
// generation; a failed generation bump restarts the handoff from lease
// acquisition). When the RecordHandoff store write fails transiently the
// Sweeper must not drive CoordinatorFence at the baseline generation 0: it
// releases the just-acquired lease and skips the re-adopt, so a subsequent
// sweep re-observes the unheld lapsed lease and re-runs the full
// bump-then-fence takeover once the store recovers, fencing the pod to the
// real post-handoff generation.
//
// Regression: the pre-fix code proceeded to ReadoptAndFence with the 0 that
// RecordHandoff returns on a store error, driving CoordinatorFence(session,
// 0) and self-holding the lease so the takeover predicate never fired again
// and the bump was lost forever. This asserts no fence is driven on the
// failed bump, the lease is released rather than self-held, and the eventual
// fence carries the post-bump generation and never 0.
func TestSweepCrashTakeoverSkipsFenceWhenGenerationBumpFails_spec_10_1(t *testing.T) {
	ctx := context.Background()
	base := memstore.New()
	mustCreate(t, base, sessionstore.Session{ID: "orphan", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-orphan"})
	sessions := &failingUpdateStore{Store: base, failUpdates: 1}

	leases := newFakeLeases()
	bindings := newFakeBindings()
	readopter := &fakeReadopter{leases: leases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID: "rep-1",
		Bindings:  bindings,
		Readopter: readopter,
	})

	// First sweep: the generation bump fails, so no fence is driven and the
	// just-acquired lease is released rather than self-held.
	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("held = %d, want 0 (lease released after the failed bump)", held)
	}
	if len(readopter.calls) != 0 {
		t.Fatalf("ReadoptAndFence calls = %d, want 0 (no fence at a baseline generation)", len(readopter.calls))
	}
	if h, ok := leases.held("acme", "orphan"); ok {
		t.Fatalf("lease still held by %q after a failed bump, want released", h)
	}
	got, _ := sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 0 {
		t.Fatalf("generation = %d, want 0 (the failed bump did not land)", got.CoordinationGeneration)
	}

	// Second sweep: the store has recovered, so the takeover re-runs from a
	// fresh handoff observation, bumps to generation 1, and fences to it.
	held, err = sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("held = %d, want 1 (orphan re-adopted after the store recovered)", held)
	}
	if len(readopter.calls) != 1 {
		t.Fatalf("ReadoptAndFence calls = %d, want 1 (fence fires on the successful bump)", len(readopter.calls))
	}
	if readopter.calls[0].generation != 1 {
		t.Errorf("fenced generation = %d, want 1 (the post-bump generation, never 0)", readopter.calls[0].generation)
	}
	if !bindings.bound["orphan"] {
		t.Errorf("binding not present after the recovered takeover published")
	}
}

// spec: §10.1 (coordinator handoff re-adopts the still-running pod), §4.2
// line 156. A nil Readopter disables the re-adopt: the generation bump and
// the lease acquire still stand on the takeover edge, and the self-held
// lease keeps the bump to once per handoff on the following sweep. Models a
// deployment or a peer without the seam wired.
func TestSweepCrashTakeoverWithoutReadopterStillBumpsOnce_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "orphan", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-orphan"})

	leases := newFakeLeases()
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1"})

	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	got, _ := sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Fatalf("generation = %d, want 1 (takeover bumps even without a re-adopt seam)", got.CoordinationGeneration)
	}
	if h, ok := leases.held("acme", "orphan"); !ok || h != "rep-1" {
		t.Fatalf("holder = %q ok=%v, want rep-1 held", h, ok)
	}

	// Second sweep: self-held lease renewed, no re-bump.
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	got, _ = sessions.Get(ctx, "acme", "orphan")
	if got.CoordinationGeneration != 1 {
		t.Errorf("generation = %d, want 1 (self-renew does not re-bump)", got.CoordinationGeneration)
	}
}
