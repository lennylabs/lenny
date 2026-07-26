// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fakeBindings is an in-memory BindingRegistry for the acquire-scoping
// tests. Bound reports the per-session presence a live pod binding would
// have; ConnAlive defaults to live and is set false to model a dead
// gateway-to-pod channel; EvictBinding records the eviction and drops the
// binding, mirroring the production seam that removes the podRegistry
// entry and the executor's cached Attach stream in one call.
type fakeBindings struct {
	bound   map[string]bool
	alive   map[string]bool
	evicted map[string]bool
}

func newFakeBindings() *fakeBindings {
	return &fakeBindings{bound: map[string]bool{}, alive: map[string]bool{}, evicted: map[string]bool{}}
}

func (f *fakeBindings) Bound(sessionID string) bool { return f.bound[sessionID] }

func (f *fakeBindings) ConnAlive(sessionID string) bool {
	v, ok := f.alive[sessionID]
	if !ok {
		return true
	}
	return v
}

func (f *fakeBindings) EvictBinding(sessionID string) {
	f.evicted[sessionID] = true
	delete(f.bound, sessionID)
}

func (f *fakeLeases) held(tenantID, sessionID string) (string, bool) {
	h, ok := f.holders[lk(tenantID, sessionID)]
	return h, ok
}

// spec: §4.6.1 (coordinating replica holds the lease), §10.1 (per-session
// coordination lease). A committed-but-never-bound session — a created,
// ready, finalizing, or suspended row, or a running row with no persisted
// pod assignment — is not adopted by a peer sweep, so the lease is not
// landed on a replica that holds no binding for the session. Regression:
// the prior lazy sweep acquired every non-terminal session, which let a
// peer steal a freshly committed session's lease before its owning
// replica's at-bind acquire ran.
func TestSweepDoesNotAdoptNeverBoundSession_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "created", TenantID: "acme", State: session.StateCreated})
	mustCreate(t, sessions, sessionstore.Session{ID: "ready", TenantID: "acme", State: session.StateReady})
	mustCreate(t, sessions, sessionstore.Session{ID: "suspended", TenantID: "acme", State: session.StateSuspended})
	// A running row with no persisted pod assignment is not yet an
	// adoptable still-running-pod session.
	mustCreate(t, sessions, sessionstore.Session{ID: "run-unbound", TenantID: "acme", State: session.StateRunning})

	leases := newFakeLeases()
	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1", Mirror: mirror})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("held = %d, want 0 (no never-bound session adopted)", held)
	}
	for _, id := range []string{"created", "ready", "suspended", "run-unbound"} {
		if h, ok := leases.held("acme", id); ok {
			t.Errorf("session %s lease acquired by %q, want unheld", id, h)
		}
	}
	rows, _ := mirror.ListHeldByReplica(ctx, "rep-1")
	if len(rows) != 0 {
		t.Errorf("mirror rows = %d, want 0", len(rows))
	}
}

// spec: §10.1 (coordinator handoff re-adopts the still-running pod). A
// running (or input_required) session with a persisted pod assignment and
// a lapsed lease this replica holds no binding for is adopted as a
// crash-takeover orphan.
func TestSweepAdoptsRunningPodOrphan_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "run", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-run"})
	mustCreate(t, sessions, sessionstore.Session{ID: "input", TenantID: "acme", State: session.StateInputRequired, PodAssignment: "pod-input"})

	leases := newFakeLeases()
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1"})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 2 {
		t.Fatalf("held = %d, want 2 (both running-pod orphans adopted)", held)
	}
	for _, id := range []string{"run", "input"} {
		if h, ok := leases.held("acme", id); !ok || h != "rep-1" {
			t.Errorf("session %s holder = %q ok=%v, want rep-1 held", id, h, ok)
		}
	}
}

// spec: §10.1 (per-session coordination lease). A lease this replica
// already holds after a takeover, whose binding it has not yet published,
// is renewed on the priorHolder == replica term even though the session is
// not otherwise adoptable (no local binding, no persisted pod assignment).
// Regression: without the renew term the gate skips such a session, the
// taken-over lease lapses on its TTL, and the session is re-orphaned.
func TestSweepRenewsSelfHeldLeaseWithoutBinding_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	// running with no persisted pod assignment: not adoptable, not bound.
	mustCreate(t, sessions, sessionstore.Session{ID: "taken", TenantID: "acme", State: session.StateRunning})

	leases := newFakeLeases()
	// This replica already holds the lease (a prior takeover), but no
	// binding is published yet.
	leases.holders[lk("acme", "taken")] = "rep-1"

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1"})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("held = %d, want 1 (self-held lease renewed)", held)
	}
	if h, ok := leases.held("acme", "taken"); !ok || h != "rep-1" {
		t.Errorf("holder = %q ok=%v, want rep-1 still held", h, ok)
	}
}

// spec: §4.6.1 (coordinating replica holds the lease). A session this
// replica binds is renewed regardless of whether it carries a persisted
// pod assignment, because the live local binding is the co-location
// signal.
func TestSweepRenewsBoundSession_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "mine", TenantID: "acme", State: session.StateRunning})

	leases := newFakeLeases()
	bindings := newFakeBindings()
	bindings.bound["mine"] = true // live binding, channel alive by default.

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1", Bindings: bindings})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 1 {
		t.Fatalf("held = %d, want 1 (bound session renewed)", held)
	}
	if h, ok := leases.held("acme", "mine"); !ok || h != "rep-1" {
		t.Errorf("holder = %q ok=%v, want rep-1", h, ok)
	}
	if bindings.evicted["mine"] {
		t.Errorf("live-channel binding was evicted, want retained")
	}
}

// spec: §10.1 (hold state on connection loss; TTL-lapse recovery). A bound
// session whose held gateway-to-pod channel has died is evicted (binding
// and cached Attach stream) and its lease released instead of renewed, so
// the session reverts to a lapsed-lease orphan a subsequent sweep
// re-adopts before the pod's hold-state self-termination. Regression: a
// dead-connection binding would otherwise keep boundHere true and renew
// the lease forever, leaving no lapsed lease for any peer to adopt.
func TestSweepEvictsDeadConnectionBinding_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "dead", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-dead"})

	leases := newFakeLeases()
	// The replica holds the lease with a live-looking binding.
	leases.holders[lk("acme", "dead")] = "rep-1"
	bindings := newFakeBindings()
	bindings.bound["dead"] = true
	bindings.alive["dead"] = false // held channel has died.

	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1"})
	sw.bindings = bindings

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("held = %d, want 0 (dead-connection lease released, not renewed)", held)
	}
	if !bindings.evicted["dead"] {
		t.Errorf("dead-connection binding was not evicted")
	}
	if h, ok := leases.held("acme", "dead"); ok {
		t.Errorf("lease still held by %q after dead-connection eviction, want released", h)
	}
}
