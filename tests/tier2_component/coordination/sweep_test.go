//go:build component

// SPDX-License-Identifier: MIT

// Component test for the §10.1 lease coordination sweeper
// (pkg/gateway/coordination), exercised against a real Redis lease
// store with in-memory session and tenant stores. It confirms a
// sweep adopts the coordination lease only for a still-running-pod
// session (running or input_required with a persisted pod assignment)
// this replica holds no binding for, leaves never-bound, terminal, and
// other replicas' sessions alone, and is idempotent across passes.
package coordination_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/coordination/coordination"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// fakeBindingReg is a coordination.BindingRegistry over an in-memory set,
// standing in for the per-replica podsession registry. A takeover publish
// flips a session to bound, modeling the production podRegistry.Put that
// holds the re-established connection open as the serving binding.
type fakeBindingReg struct {
	mu    sync.Mutex
	bound map[string]bool
}

func newFakeBindingReg() *fakeBindingReg { return &fakeBindingReg{bound: map[string]bool{}} }

func (f *fakeBindingReg) Bound(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bound[id]
}

func (f *fakeBindingReg) ConnAlive(string) bool { return true }

func (f *fakeBindingReg) EvictBinding(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.bound, id)
}

func (f *fakeBindingReg) publish(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.bound[id] = true
}

// fakeReadopter is a coordination.Readopter for the crash-takeover
// component tests. On a non-failing session it returns a publish callback
// that flips the fake binding to bound; on a failing session it models the
// coordfence relinquish by releasing the real coordination lease and
// returning an error, so the Sweeper records an adoption backoff.
type fakeReadopter struct {
	fail      map[string]bool
	leases    leasestore.LeaseStore
	replicaID string
	bindings  *fakeBindingReg

	mu    sync.Mutex
	calls int
	gens  []int64
}

func (r *fakeReadopter) ReadoptAndFence(ctx context.Context, tenantID, sessionID string, generation int64) (func(), error) {
	r.mu.Lock()
	r.calls++
	r.gens = append(r.gens, generation)
	r.mu.Unlock()
	if r.fail[sessionID] {
		_ = r.leases.Release(ctx, tenantID, sessionID, r.replicaID)
		return nil, errors.New("coordfence: relinquished")
	}
	return func() { r.bindings.publish(sessionID) }, nil
}

func (r *fakeReadopter) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

// failingUpdateStore wraps a session store and fails its first failUpdates
// Update calls, so a component test can model a transient Postgres error
// during the crash-takeover generation bump against the real Redis lease
// store. Every other method delegates to the wrapped store, and Update falls
// through once the failure budget is spent.
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

// staticLister is a coordination.TenantLister over a fixed slice.
type staticLister []string

func (s staticLister) ListTenants(context.Context) ([]string, error) {
	return []string(s), nil
}

func uniq(t *testing.T) string {
	t.Helper()
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return hex.EncodeToString(b[:])
}

func seedSession(t *testing.T, store *memstore.Store, tenant, id string, state session.State) {
	t.Helper()
	row := sessionstore.Session{ID: id, TenantID: tenant, State: state, RuntimeRef: "echo"}
	// A running-pod session carries a persisted pod assignment; that is the
	// signal that makes its lapsed lease adoptable by the coordination
	// sweep, which no longer acquires the lease of a never-bound session.
	if state == session.StateRunning || state == session.StateInputRequired {
		row.PodAssignment = "pod-" + id
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

// spec: 10.1
// diagnosis: the lease coordination sweeper in
// pkg/gateway/coordination did not behave as specified. A sweep must
// adopt the coordination lease only for a still-running-pod session
// (running or input_required with a persisted pod assignment) it holds no
// binding for, leave never-bound, terminal, and other replicas' sessions
// alone, and be idempotent across passes.
func TestSweeperContract(t *testing.T) {
	t.Parallel()
	rd := containers.StartRedis(t, containers.RedisOptions{})
	leases := leasestore.New(rd.Client)
	ctx := context.Background()

	newSweeper := func(sessions *memstore.Store, tenants []string, replica string) *coordination.Sweeper {
		return coordination.NewSweeper(staticLister(tenants), sessions, leases, coordination.Options{
			ReplicaID: replica,
			TTL:       30 * time.Second,
			Interval:  time.Hour,
		})
	}

	t.Run("sweep adopts running-pod sessions and leaves never-bound and terminal alone", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		// Adoptable still-running-pod orphans: running / input_required with
		// a persisted pod assignment and no local binding.
		adopt := []string{run + "-run", run + "-input"}
		seedSession(t, sessions, "acme", adopt[0], session.StateRunning)
		seedSession(t, sessions, "acme", adopt[1], session.StateInputRequired)
		// Never-bound non-terminal sessions: the sweep must not adopt these,
		// because a peer must not land the lease on a replica that holds no
		// binding before the owning replica's at-bind acquire runs.
		neverBound := []string{run + "-created", run + "-ready", run + "-suspended", run + "-awaiting"}
		seedSession(t, sessions, "acme", neverBound[0], session.StateCreated)
		seedSession(t, sessions, "acme", neverBound[1], session.StateReady)
		seedSession(t, sessions, "acme", neverBound[2], session.StateSuspended)
		seedSession(t, sessions, "acme", neverBound[3], session.StateAwaitingClientAction)
		done := run + "-done"
		seedSession(t, sessions, "acme", done, session.StateCompleted)
		seedSession(t, sessions, "acme", run+"-failed", session.StateFailed)

		held, err := newSweeper(sessions, []string{"acme"}, "replica-1").Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if held != 2 {
			t.Errorf("held = %d, want 2 (the running-pod orphans only)", held)
		}
		for _, id := range adopt {
			lease, err := leases.Get(ctx, "acme", id)
			if err != nil {
				t.Errorf("running-pod session %s has no lease: %v", id, err)
				continue
			}
			if lease.Holder != "replica-1" {
				t.Errorf("session %s lease holder = %q, want replica-1", id, lease.Holder)
			}
		}
		for _, id := range append(neverBound, done, run+"-failed") {
			if _, err := leases.Get(ctx, "acme", id); err == nil {
				t.Errorf("session %s must not hold a coordination lease (never-bound or terminal)", id)
			}
		}
	})

	t.Run("sweep is idempotent across passes", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		seedSession(t, sessions, "acme", run+"-x", session.StateRunning)
		sw := newSweeper(sessions, []string{"acme"}, "replica-1")
		first, err := sw.Sweep(ctx)
		if err != nil || first != 1 {
			t.Fatalf("first Sweep: held=%d err=%v", first, err)
		}
		second, err := sw.Sweep(ctx)
		if err != nil || second != 1 {
			t.Fatalf("second Sweep: held=%d err=%v", second, err)
		}
		if lease, err := leases.Get(ctx, "acme", run+"-x"); err != nil || lease.Holder != "replica-1" {
			t.Errorf("lease after re-sweep: %+v err=%v", lease, err)
		}
	})

	t.Run("sweep leaves another replica's sessions alone", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		mine := run + "-mine"
		theirs := run + "-theirs"
		seedSession(t, sessions, "acme", mine, session.StateRunning)
		seedSession(t, sessions, "acme", theirs, session.StateRunning)
		// Another replica already holds the lease for `theirs`.
		if _, err := leases.Acquire(ctx, "acme", theirs, "replica-other", 30*time.Second); err != nil {
			t.Fatalf("pre-acquire: %v", err)
		}
		held, err := newSweeper(sessions, []string{"acme"}, "replica-1").Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep: %v", err)
		}
		if held != 1 {
			t.Errorf("held = %d, want 1 (only the session this replica owns)", held)
		}
		if lease, _ := leases.Get(ctx, "acme", theirs); lease.Holder != "replica-other" {
			t.Errorf("other replica's lease holder = %q, want replica-other", lease.Holder)
		}
		if lease, _ := leases.Get(ctx, "acme", mine); lease.Holder != "replica-1" {
			t.Errorf("own session lease holder = %q, want replica-1", lease.Holder)
		}
	})

	t.Run("sweep with no sessions holds nothing", func(t *testing.T) {
		held, err := newSweeper(memstore.New(), []string{"acme", "globex"}, "replica-1").Sweep(ctx)
		if err != nil || held != 0 {
			t.Errorf("empty Sweep: held=%d err=%v, want 0/nil", held, err)
		}
	})

	// spec: §4.2 line 156 — coordination_generation is incremented on
	// coordinator handoff across gateway replicas. The sweeper observes
	// the prior holder before its Acquire; when the prior holder is a
	// different replica, the sweeper records the handoff. The no-bump
	// cases are: lease unheld (priorHolder == ""), self-renew
	// (priorHolder == replicaID), and held-by-other (Acquire returns
	// ErrHeld so no handoff occurred).
	t.Run("RecordHandoff increments coordination_generation monotonically", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-handoff"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		// Confirm baseline counter is zero.
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Fatalf("baseline CoordinationGeneration = %d, want 0", got.CoordinationGeneration)
		}

		sw := newSweeper(sessions, []string{"acme"}, "replica-B")
		// One observed handoff bumps the counter by exactly one.
		sw.RecordHandoff(ctx, "acme", sessID)
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("after one handoff, CoordinationGeneration = %d, want 1",
				got.CoordinationGeneration)
		}

		// Successive handoffs continue to advance.
		sw.RecordHandoff(ctx, "acme", sessID)
		sw.RecordHandoff(ctx, "acme", sessID)
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 3 {
			t.Errorf("after three handoffs, CoordinationGeneration = %d, want 3",
				got.CoordinationGeneration)
		}
	})

	// spec: §10.1 (a terminal session is no longer coordinated by anyone; a
	// session that goes terminal during takeover is not resurrected), §4.2
	// line 156 — a session that races to terminal between the sweep's List
	// snapshot and the atomic handoff bump must not have its
	// coordination_generation advanced. RecordHandoff re-reads the row under
	// the same atomic Update and refuses the bump on a terminal state, so the
	// takeover is abandoned rather than resurrecting the session. Pre-guard
	// code bumped unconditionally and would report generation 1 here.
	t.Run("RecordHandoff refuses to bump a session that went terminal", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-terminal-handoff"
		// Seed running (the state the sweep's List would have observed), then
		// transition it terminal before the handoff bump runs, modeling the
		// concurrent terminal transition during a takeover.
		seedSession(t, sessions, "acme", sessID, session.StateRunning)
		if _, err := sessions.Update(ctx, "acme", sessID, func(row *sessionstore.Session) error {
			row.State = session.StateCompleted
			return nil
		}); err != nil {
			t.Fatalf("transition session terminal: %v", err)
		}

		sw := newSweeper(sessions, []string{"acme"}, "replica-B")
		if gen := sw.RecordHandoff(ctx, "acme", sessID); gen != 0 {
			t.Errorf("RecordHandoff on a terminal session returned generation %d, want 0 (bump refused)", gen)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Errorf("terminal session CoordinationGeneration = %d, want 0 (never bumped by a raced takeover)",
				got.CoordinationGeneration)
		}
	})

	// spec: §4.2 line 156, §10.1 (coordinator handoff re-adopts the
	// still-running pod) — a fresh acquisition of a running-pod orphan by a
	// replica that holds no binding for it is a crash takeover (the prior
	// coordinator crashed and its lease lapsed), so the sweeper bumps
	// coordination_generation exactly once. The self-renew that follows
	// (the lease is now self-held) must NOT bump again, so the generation
	// advances once per handoff rather than once per sweep.
	t.Run("Sweep takeover bumps once then self-renew does not bump", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-self-renew"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)
		sw := newSweeper(sessions, []string{"acme"}, "replica-1")

		// First sweep: lease unheld, running-pod orphan adopted by replica-1.
		// This is a crash takeover, so the generation bumps to 1.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("after takeover Sweep, CoordinationGeneration = %d, want 1",
				got.CoordinationGeneration)
		}

		// Second sweep: lease held by replica-1, renewed by replica-1.
		// No handoff (priorHolder == replicaID), so no further bump.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("second Sweep (renew): %v", err)
		}
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("after self-renew Sweep, CoordinationGeneration = %d, want 1 "+
				"(handoff bumps once, not once per sweep)",
				got.CoordinationGeneration)
		}
	})

	// spec: §4.2 line 156 — when the lease is held by another replica
	// the sweeper's Acquire fails with ErrHeld; no handoff occurred so
	// the counter must not bump.
	t.Run("Sweep held-by-other does not bump counter", func(t *testing.T) {
		run := uniq(t)
		sessions := memstore.New()
		sessID := run + "-held-by-other"
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		// Replica-A owns the lease.
		if _, err := leases.Acquire(ctx, "acme", sessID, "replica-A", 30*time.Second); err != nil {
			t.Fatalf("acquire replica-A: %v", err)
		}

		// Replica-B's sweep: Get sees replica-A as prior holder, but
		// Acquire returns ErrHeld (replica-A still owns). No handoff.
		swB := newSweeper(sessions, []string{"acme"}, "replica-B")
		if _, err := swB.Sweep(ctx); err != nil {
			t.Fatalf("held-by-other Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Errorf("after held-by-other Sweep, CoordinationGeneration = %d, want 0 "+
				"(Acquire returned ErrHeld; no handoff observed)",
				got.CoordinationGeneration)
		}
	})

	// spec: §10.1 (coordinator handoff re-adopts the still-running pod;
	// CoordinatorFence precondition; no operational RPC before the fence
	// acknowledges), §4.2 line 156. On the crash-takeover edge the sweeper
	// adopts the lapsed lease, bumps coordination_generation, drives the
	// re-adopt fence with the post-bump generation over the real Redis lease
	// store, and publishes the binding only after the fence acknowledges —
	// once per handoff. The next sweep observes the published binding and
	// renews without re-fencing.
	t.Run("crash takeover fences and publishes once per handoff", func(t *testing.T) {
		run := uniq(t)
		sessID := run + "-takeover"
		sessions := memstore.New()
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		bindings := newFakeBindingReg()
		readopter := &fakeReadopter{leases: leases, replicaID: "replica-1", bindings: bindings}
		sw := coordination.NewSweeper(staticLister([]string{"acme"}), sessions, leases, coordination.Options{
			ReplicaID: "replica-1",
			TTL:       30 * time.Second,
			Interval:  time.Hour,
			Bindings:  bindings,
			Readopter: readopter,
		})

		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Fatalf("generation = %d, want 1 (takeover bumped once)", got.CoordinationGeneration)
		}
		if readopter.callCount() != 1 {
			t.Fatalf("ReadoptAndFence calls = %d, want 1", readopter.callCount())
		}
		if readopter.gens[0] != 1 {
			t.Errorf("fenced generation = %d, want 1", readopter.gens[0])
		}
		if !bindings.Bound(sessID) {
			t.Errorf("binding not published after the fence acknowledged")
		}
		if lease, err := leases.Get(ctx, "acme", sessID); err != nil || lease.Holder != "replica-1" {
			t.Errorf("lease after takeover: %+v err=%v, want held by replica-1", lease, err)
		}

		// The published binding makes the next sweep a renew.
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("second Sweep: %v", err)
		}
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("generation = %d, want 1 (no re-bump on the renew sweep)", got.CoordinationGeneration)
		}
		if readopter.callCount() != 1 {
			t.Errorf("ReadoptAndFence calls = %d, want 1 (fence fires once per handoff)", readopter.callCount())
		}
	})

	// spec: §10.1 line 35 (relinquish-and-backoff). A terminal fence failure
	// relinquishes the real Redis lease (coordfence release) and the sweeper
	// records a per-session adoption backoff, so the fixed sweep does not
	// re-adopt inside the window and re-bump the generation every sweep. The
	// generation increment stays in the store; after the window elapses the
	// session is re-adopted.
	// spec: §10.1 (coordinator handoff fences the pod to the post-handoff
	// generation; a failed generation bump restarts the handoff from lease
	// acquisition), §4.2 line 156. When the RecordHandoff store write fails
	// transiently the sweeper must not drive the re-adopt fence at the
	// baseline generation 0: it releases the just-acquired real Redis lease
	// and skips the re-adopt, so the next sweep re-observes the unheld lapsed
	// lease and re-runs the full bump-then-fence takeover once the store
	// recovers, fencing to the real post-handoff generation.
	//
	// Regression: the pre-fix sweeper proceeded to ReadoptAndFence with the 0
	// RecordHandoff returns on a store error, driving the fence at generation
	// 0 and self-holding the lease so the takeover never re-ran and the bump
	// was lost. This asserts no fence on the failed bump, the lease released
	// rather than self-held, and the eventual fence carrying the post-bump
	// generation.
	t.Run("failed generation bump skips fence and releases lease", func(t *testing.T) {
		run := uniq(t)
		sessID := run + "-gen-bump-fail"
		base := memstore.New()
		seedSession(t, base, "acme", sessID, session.StateRunning)
		sessions := &failingUpdateStore{Store: base, failUpdates: 1}

		bindings := newFakeBindingReg()
		readopter := &fakeReadopter{leases: leases, replicaID: "replica-1", bindings: bindings}
		sw := coordination.NewSweeper(staticLister([]string{"acme"}), sessions, leases, coordination.Options{
			ReplicaID: "replica-1",
			TTL:       30 * time.Second,
			Interval:  time.Hour,
			Bindings:  bindings,
			Readopter: readopter,
		})

		// First sweep: the generation bump fails, so no fence is driven and
		// the just-acquired lease is released rather than self-held.
		held, err := sw.Sweep(ctx)
		if err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		if held != 0 {
			t.Fatalf("held = %d, want 0 (lease released after the failed bump)", held)
		}
		if readopter.callCount() != 0 {
			t.Fatalf("ReadoptAndFence calls = %d, want 0 (no fence at a baseline generation)", readopter.callCount())
		}
		if _, err := leases.Get(ctx, "acme", sessID); err == nil {
			t.Fatalf("lease still held after a failed generation bump, want released")
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 0 {
			t.Fatalf("generation = %d, want 0 (the failed bump did not land)", got.CoordinationGeneration)
		}

		// Second sweep: the store has recovered, so the takeover re-runs from
		// a fresh handoff observation, bumps to generation 1, and fences to it.
		held, err = sw.Sweep(ctx)
		if err != nil {
			t.Fatalf("second Sweep: %v", err)
		}
		if held != 1 {
			t.Fatalf("held = %d, want 1 (orphan re-adopted after the store recovered)", held)
		}
		if readopter.callCount() != 1 {
			t.Fatalf("ReadoptAndFence calls = %d, want 1 (fence fires on the successful bump)", readopter.callCount())
		}
		if readopter.gens[0] != 1 {
			t.Errorf("fenced generation = %d, want 1 (the post-bump generation, never 0)", readopter.gens[0])
		}
		if !bindings.Bound(sessID) {
			t.Errorf("binding not published after the recovered takeover")
		}
	})

	t.Run("terminal fence failure relinquishes lease and backs off re-adoption", func(t *testing.T) {
		run := uniq(t)
		sessID := run + "-relinquish"
		sessions := memstore.New()
		seedSession(t, sessions, "acme", sessID, session.StateRunning)

		readopter := &fakeReadopter{fail: map[string]bool{sessID: true}, leases: leases, replicaID: "replica-1"}
		now := time.Unix(2000, 0).UTC()
		var clockMu sync.Mutex
		clock := func() time.Time {
			clockMu.Lock()
			defer clockMu.Unlock()
			return now
		}
		advance := func(d time.Duration) {
			clockMu.Lock()
			defer clockMu.Unlock()
			now = now.Add(d)
		}
		sw := coordination.NewSweeper(staticLister([]string{"acme"}), sessions, leases, coordination.Options{
			ReplicaID:       "replica-1",
			TTL:             30 * time.Second,
			Interval:        time.Hour,
			Readopter:       readopter,
			AdoptionBackoff: 10 * time.Second,
			Clock:           clock,
		})

		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("first Sweep: %v", err)
		}
		got, _ := sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Fatalf("generation = %d, want 1 (bump stays after relinquish)", got.CoordinationGeneration)
		}
		if _, err := leases.Get(ctx, "acme", sessID); err == nil {
			t.Fatalf("lease still held after terminal fence relinquish, want released")
		}

		// Inside the backoff window: no re-adopt, no re-bump.
		advance(5 * time.Second)
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("second Sweep: %v", err)
		}
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 1 {
			t.Errorf("generation = %d, want 1 (no re-adopt inside the backoff window)", got.CoordinationGeneration)
		}
		if readopter.callCount() != 1 {
			t.Errorf("ReadoptAndFence calls = %d, want 1 (no re-fence inside backoff)", readopter.callCount())
		}
		if _, err := leases.Get(ctx, "acme", sessID); err == nil {
			t.Errorf("lease re-acquired inside the backoff window, want unheld")
		}

		// After the window elapses: re-adopted, generation climbs once more.
		advance(10 * time.Second)
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("third Sweep: %v", err)
		}
		got, _ = sessions.Get(ctx, "acme", sessID)
		if got.CoordinationGeneration != 2 {
			t.Errorf("generation = %d, want 2 (re-adopted after the backoff elapsed)", got.CoordinationGeneration)
		}
		if readopter.callCount() != 2 {
			t.Errorf("ReadoptAndFence calls = %d, want 2 (re-fenced after the backoff elapsed)", readopter.callCount())
		}
	})
}
