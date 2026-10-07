// SPDX-License-Identifier: MIT

package coordination

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordlease"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
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

// acquireCountingLeases wraps fakeLeases and records each Acquire's session.
type acquireCountingLeases struct {
	*fakeLeases
	acquired []string
}

func (a *acquireCountingLeases) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	a.acquired = append(a.acquired, sessionID)
	return a.fakeLeases.Acquire(ctx, tenantID, sessionID, holder, ttl)
}

// spec: §10.1 (Horizontal Scaling), §10.1.1 (Stateless Replicas and
// Per-Session Coordination), §7.3 (Retry and Resume)
// diagnosis: the failure funnel releases a failed session's pod binding and
// leaves its coordination lease with the coordinating replica. The Sweeper's
// self-held renew term keeps that lease in resume_pending and
// awaiting_client_action with no binding and no pod assignment. A failure
// means the lease of a recovering session lapses on its TTL, so the replica
// the §7.2 inbox rows and the §29.6 resume preconditions assume coordinates
// the session no longer holds it.
func TestSweepRenewsSelfHeldLeaseInRecoveringStates_spec_10_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "pending", TenantID: "acme", State: session.StateResumePending})
	mustCreate(t, sessions, sessionstore.Session{ID: "awaiting", TenantID: "acme", State: session.StateAwaitingClientAction})

	leases := &acquireCountingLeases{fakeLeases: newFakeLeases()}
	leases.holders[lk("acme", "pending")] = "rep-1"
	leases.holders[lk("acme", "awaiting")] = "rep-1"
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Bindings: newFakeBindings()})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 2 {
		t.Fatalf("held = %d, want 2 (both self-held recovering leases renewed)", held)
	}
	got := map[string]int{}
	for _, id := range leases.acquired {
		got[id]++
	}
	if got["pending"] != 1 || got["awaiting"] != 1 {
		t.Errorf("Acquire calls = %v, want one for each recovering session", leases.acquired)
	}
	for _, id := range []string{"pending", "awaiting"} {
		if h, ok := leases.held("acme", id); !ok || h != "rep-1" {
			t.Errorf("session %s holder = %q ok=%v, want rep-1", id, h, ok)
		}
	}
}

// setGeneration moves a session row's coordination_generation, modeling the
// bump a peer replica's takeover writes through RecordHandoff.
func setGeneration(t *testing.T, store sessionstore.Store, sessionID string, generation int64) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", sessionID, func(row *sessionstore.Session) error {
		row.CoordinationGeneration = generation
		return nil
	}); err != nil {
		t.Fatalf("set generation of %s: %v", sessionID, err)
	}
}

// generationOf reads a session row's coordination_generation.
func generationOf(t *testing.T, store sessionstore.Store, sessionID string) int64 {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", sessionID)
	if err != nil {
		t.Fatalf("get %s: %v", sessionID, err)
	}
	return row.CoordinationGeneration
}

// mirrorSnapshot returns the mirror rows each replica holds, keyed by
// replica then session, so a test can assert a sweep wrote no mirror row.
func mirrorSnapshot(t *testing.T, mirror coordlease.Store, replicas ...string) map[string]map[string]int64 {
	t.Helper()
	out := map[string]map[string]int64{}
	for _, r := range replicas {
		rows, err := mirror.ListHeldByReplica(context.Background(), r)
		if err != nil {
			t.Fatalf("ListHeldByReplica(%s): %v", r, err)
		}
		out[r] = map[string]int64{}
		for _, row := range rows {
			out[r][row.SessionID] = row.CoordinationGeneration
		}
	}
	return out
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior). A bound session whose lease a peer replica now
// holds, and whose coordination_generation has advanced past the generation
// this replica last renewed it at, has been taken over: the Sweeper evicts
// its binding and cached Attach stream and writes no lease, mirror, or
// session row. A foreign lease at an unchanged generation, which
// leasestore.Failover reports for a stale Redis holder after a Redis
// outage, is not evicted, and neither is a binding this replica never
// renewed. Regression: the pre-fix Sweep skipped every ErrHeld, so a stale
// coordinator kept its binding and stream indefinitely.
func TestSweepEvictsBindingWhenPeerHoldsLease_spec_10_1_5(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	for _, id := range []string{"taken", "stale", "unbound"} {
		mustCreate(t, sessions, sessionstore.Session{ID: id, TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-" + id, CoordinationGeneration: 1})
	}
	leases := newFakeLeases()
	leases.holders[lk("acme", "taken")] = "rep-1"
	leases.holders[lk("acme", "stale")] = "rep-1"
	leases.holders[lk("acme", "unbound")] = "rep-2"
	bindings := newFakeBindings()
	bindings.bound["taken"] = true
	bindings.bound["stale"] = true
	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Mirror: mirror, Bindings: bindings})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("first Sweep: %v", err)
	}
	if held != 2 {
		t.Fatalf("first Sweep held = %d, want 2 (both bound sessions renewed)", held)
	}

	// rep-2 now holds every lease. Only "taken" advanced its generation, as
	// a peer's takeover does; "stale" models a stale foreign Redis holder.
	for _, id := range []string{"taken", "stale", "unbound"} {
		leases.holders[lk("acme", id)] = "rep-2"
	}
	setGeneration(t, sessions, "taken", 2)
	before := mirrorSnapshot(t, mirror, "rep-1", "rep-2")

	held, err = sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("second Sweep held = %d, want 0 (no lease held here)", held)
	}
	if !bindings.evicted["taken"] {
		t.Errorf("taken-over session was not evicted")
	}
	if bindings.evicted["stale"] {
		t.Errorf("session held elsewhere at an unchanged generation was evicted")
	}
	if bindings.evicted["unbound"] {
		t.Errorf("unbound session was evicted")
	}
	// fakeLeases.Release ignores the holder, so a stray Release removes the
	// peer's lease and fails here.
	for _, id := range []string{"taken", "stale", "unbound"} {
		if h, ok := leases.held("acme", id); !ok || h != "rep-2" {
			t.Errorf("session %s holder = %q ok=%v, want rep-2", id, h, ok)
		}
	}
	after := mirrorSnapshot(t, mirror, "rep-1", "rep-2")
	for r := range before {
		if len(before[r]) != len(after[r]) {
			t.Errorf("mirror rows for %s = %v, want unchanged %v", r, after[r], before[r])
			continue
		}
		for id, gen := range before[r] {
			if after[r][id] != gen {
				t.Errorf("mirror row %s/%s generation = %d, want unchanged %d", r, id, after[r][id], gen)
			}
		}
	}
	for id, want := range map[string]int64{"taken": 2, "stale": 1, "unbound": 1} {
		if got := generationOf(t, sessions, id); got != want {
			t.Errorf("session %s generation = %d, want %d (no session-row write)", id, got, want)
		}
	}

	// An unbound sweep forgets the renewed generation, so a rebind at a
	// generation advanced while unbound is not mistaken for a takeover.
	delete(bindings.bound, "stale")
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("third Sweep: %v", err)
	}
	bindings.bound["stale"] = true
	setGeneration(t, sessions, "stale", 2)
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("fourth Sweep: %v", err)
	}
	if bindings.evicted["stale"] {
		t.Errorf("rebound session with no renewed generation was evicted")
	}
	if !bindings.bound["stale"] {
		t.Errorf("rebound session lost its binding")
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.5
// (Stale Replica Behavior). The crash-takeover edge records the generation
// RecordHandoff returned, so a foreign lease observed on the next sweep at
// that same generation does not evict the published binding. Recording the
// pre-bump row value would make the takeover's own bump read as a peer's.
func TestSweepRecordsHandoffGenerationOnTakeover_spec_10_1_5(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "orphan", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-orphan", CoordinationGeneration: 1})

	leases := newFakeLeases()
	bindings := newFakeBindings()
	readopter := &fakeReadopter{leases: leases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Bindings: bindings, Readopter: readopter})

	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("takeover Sweep: %v", err)
	}
	if got := generationOf(t, sessions, "orphan"); got != 2 {
		t.Fatalf("generation after takeover = %d, want 2", got)
	}
	if !bindings.bound["orphan"] {
		t.Fatalf("binding not published by the takeover")
	}

	// No renew sweep runs before the foreign lease appears, so the entry the
	// takeover edge recorded is the one compared.
	leases.holders[lk("acme", "orphan")] = "rep-2"
	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("second Sweep: %v", err)
	}
	if held != 0 {
		t.Fatalf("held = %d, want 0", held)
	}
	if bindings.evicted["orphan"] {
		t.Errorf("binding evicted at the generation this replica took over at")
	}
	if !bindings.bound["orphan"] {
		t.Errorf("binding lost after a foreign lease at an unchanged generation")
	}
}
