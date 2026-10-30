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

// countingLeases wraps fakeLeases and records each Acquire and Release by
// session, so a test can assert the sweep wrote no lease for a session.
type countingLeases struct {
	*fakeLeases
	acquired map[string]int
	released map[string]int
}

func newCountingLeases() *countingLeases {
	return &countingLeases{fakeLeases: newFakeLeases(), acquired: map[string]int{}, released: map[string]int{}}
}

func (c *countingLeases) Acquire(ctx context.Context, tenantID, sessionID, holder string, ttl time.Duration) (leasestore.Lease, error) {
	c.acquired[sessionID]++
	return c.fakeLeases.Acquire(ctx, tenantID, sessionID, holder, ttl)
}

func (c *countingLeases) Release(ctx context.Context, tenantID, sessionID, holder string) error {
	c.released[sessionID]++
	return c.fakeLeases.Release(ctx, tenantID, sessionID, holder)
}

// raceStore wraps a session store and runs a per-session hook once, right
// before the next Update of that session, so a test can land a concurrent
// write between the sweep's List and the handoff compare-and-swap.
type raceStore struct {
	sessionstore.Store
	beforeUpdate map[string]func()
}

func (r *raceStore) Update(ctx context.Context, tenantID, id string, mutate func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if hook, ok := r.beforeUpdate[id]; ok {
		delete(r.beforeUpdate, id)
		hook()
	}
	return r.Store.Update(ctx, tenantID, id, mutate)
}

// heldDelivery records one HeldDeliverer call and whether the session's
// binding was already published when it ran.
type heldDelivery struct {
	tenantID, sessionID string
	generation          int64
	boundAtCall         bool
}

type recordingDeliverer struct {
	bindings *fakeBindings
	calls    []heldDelivery
}

func (d *recordingDeliverer) DeliverHeldAfterTakeover(tenantID, sessionID string, generation int64) {
	bound := d.bindings != nil && d.bindings.bound[sessionID]
	d.calls = append(d.calls, heldDelivery{tenantID, sessionID, generation, bound})
}

type recordingReleaser struct{ rows []sessionstore.Session }

func (r *recordingReleaser) ReleaseTerminalBinding(row sessionstore.Session) {
	r.rows = append(r.rows, row)
}

// updateRow applies mutate to a stored row, failing the test on error.
func updateRow(t *testing.T, store sessionstore.Store, id string, mutate func(*sessionstore.Session)) {
	t.Helper()
	if _, err := store.Update(context.Background(), "acme", id, func(row *sessionstore.Session) error {
		mutate(row)
		return nil
	}); err != nil {
		t.Fatalf("update %s: %v", id, err)
	}
}

// heldMirrorGen returns the generation of the session's active mirror row
// held by replica, and whether one exists.
func heldMirrorGen(t *testing.T, mirror coordlease.Store, replica, sessionID string) (int64, bool) {
	t.Helper()
	rows, err := mirror.ListHeldByReplica(context.Background(), replica)
	if err != nil {
		t.Fatalf("ListHeldByReplica: %v", err)
	}
	for _, r := range rows {
		if r.SessionID == sessionID {
			return r.CoordinationGeneration, true
		}
	}
	return 0, false
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination)
// diagnosis: the Sweeper adopted a lapsed lease only for running and
// input_required rows with a pod, so a session whose coordinator died in
// resume_pending, awaiting_client_action, or a held-pod suspended state had
// no coordinating replica. A failure here means the adoption predicate admits
// a state the spec does not list, misses one it lists, readopts the failed
// pod of a recovering row, or bumps more than once per handoff.
func TestSweepAdoptsSpecAdoptedStatesOnly_spec_10_1_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	type rowSpec struct {
		id    string
		state session.State
		pod   string
	}
	adopted := []rowSpec{
		{"running", session.StateRunning, "pod-running"},
		{"input", session.StateInputRequired, "pod-input"},
		{"suspended-held", session.StateSuspended, "pod-suspended"},
		{"pending-failedpod", session.StateResumePending, "pod-failed"},
		{"pending", session.StateResumePending, ""},
		{"awaiting-failedpod", session.StateAwaitingClientAction, "pod-failed2"},
		{"awaiting", session.StateAwaitingClientAction, ""},
	}
	refused := []rowSpec{
		{"created", session.StateCreated, ""},
		{"finalizing", session.StateFinalizing, "pod-f"},
		{"ready", session.StateReady, "pod-r"},
		{"starting", session.StateStarting, "pod-s"},
		{"resuming", session.StateResuming, "pod-res"},
		{"running-nopod", session.StateRunning, ""},
		{"input-nopod", session.StateInputRequired, ""},
		{"suspended-podless", session.StateSuspended, ""},
	}
	for _, r := range append(append([]rowSpec{}, adopted...), refused...) {
		mustCreate(t, sessions, sessionstore.Session{ID: r.id, TenantID: "acme", State: r.state, PodAssignment: r.pod})
	}
	leases := newCountingLeases()
	bindings := newFakeBindings()
	readopter := &fakeReadopter{leases: leases.fakeLeases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}
	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Mirror: mirror, Bindings: bindings, Readopter: readopter})

	for pass := 1; pass <= 2; pass++ {
		held, err := sw.Sweep(ctx)
		if err != nil {
			t.Fatalf("Sweep %d: %v", pass, err)
		}
		if held != len(adopted) {
			t.Fatalf("Sweep %d held = %d, want %d (each adopted state)", pass, held, len(adopted))
		}
	}
	for _, r := range adopted {
		if got := generationOf(t, sessions, r.id); got != 2 {
			t.Errorf("%s generation = %d, want 2 (one bump across two sweeps)", r.id, got)
		}
		if h, ok := leases.held("acme", r.id); !ok || h != "rep-1" {
			t.Errorf("%s lease holder = %q ok=%v, want rep-1", r.id, h, ok)
		}
		if gen, ok := heldMirrorGen(t, mirror, "rep-1", r.id); !ok || gen != 2 {
			t.Errorf("%s mirror row = (%d, %v), want generation 2", r.id, gen, ok)
		}
	}
	for _, r := range refused {
		if leases.acquired[r.id] != 0 {
			t.Errorf("%s: %d Acquire calls, want none", r.id, leases.acquired[r.id])
		}
		if got := generationOf(t, sessions, r.id); got != 1 {
			t.Errorf("%s generation = %d, want 1 (never adopted)", r.id, got)
		}
	}
	readopted := map[string]int64{}
	for _, c := range readopter.calls {
		readopted[c.sessionID] = c.generation
	}
	want := map[string]int64{"running": 2, "input": 2, "suspended-held": 2}
	if len(readopted) != len(want) || len(readopter.calls) != len(want) {
		t.Fatalf("readopt calls = %v, want one each for %v", readopted, want)
	}
	for id, gen := range want {
		if readopted[id] != gen {
			t.Errorf("%s readopt generation = %d, want %d", id, readopted[id], gen)
		}
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1.8
// (Rolling Updates and the CheckpointBarrier Protocol)
// diagnosis: a suspended session whose pod was released must hold no
// coordination lease and have no coordinating replica. A failure means the
// Sweeper renewed, released, or adopted that lease, left this replica's
// mirror row naming a session with no pod, or failed to adopt the session
// once a write moved it to resume_pending.
func TestSweepPodlessSuspendedHoldsNoLease_spec_10_1_1(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name  string
		alive bool
	}{{"live connection", true}, {"dead connection", false}} {
		t.Run(tc.name, func(t *testing.T) {
			sessions := memstore.New()
			mustCreate(t, sessions, sessionstore.Session{ID: "ps", TenantID: "acme", State: session.StateSuspended})
			leases := newCountingLeases()
			leases.holders[lk("acme", "ps")] = "rep-1"
			bindings := newFakeBindings()
			bindings.bind("ps", 1)
			bindings.alive["ps"] = tc.alive
			mirror := coordlease.NewMemoryStore(nil)
			if err := mirror.Upsert(ctx, coordlease.Lease{TenantID: "acme", SessionID: "ps", CoordinatorReplica: "rep-1", CoordinationGeneration: 1}); err != nil {
				t.Fatalf("seed mirror: %v", err)
			}
			readopter := &fakeReadopter{leases: leases.fakeLeases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}
			sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
				Options{ReplicaID: "rep-1", Mirror: mirror, Bindings: bindings, Readopter: readopter})

			held, err := sw.Sweep(ctx)
			if err != nil || held != 0 {
				t.Fatalf("Sweep: held=%d err=%v, want 0 and nil", held, err)
			}
			if leases.acquired["ps"] != 0 || leases.released["ps"] != 0 {
				t.Fatalf("lease writes = %d acquires, %d releases, want none", leases.acquired["ps"], leases.released["ps"])
			}
			if _, ok := heldMirrorGen(t, mirror, "rep-1", "ps"); ok {
				t.Fatal("mirror row still names the podless suspended session")
			}
			if bindings.evicted["ps"] {
				t.Fatal("the Sweeper evicted the binding the suspension release owns")
			}

			// The lease lapses on its TTL: still nothing is written or adopted.
			delete(leases.holders, lk("acme", "ps"))
			if held, err := sw.Sweep(ctx); err != nil || held != 0 {
				t.Fatalf("post-lapse Sweep: held=%d err=%v", held, err)
			}
			if leases.acquired["ps"] != 0 || generationOf(t, sessions, "ps") != 1 {
				t.Fatal("a lapsed podless suspended lease was adopted")
			}
			if _, ok := heldMirrorGen(t, mirror, "rep-1", "ps"); ok {
				t.Fatal("a mirror row was written for a podless suspended session")
			}

			// A write moves the session to resume_pending: it is adopted with
			// one bump and no readopt.
			delete(bindings.bound, "ps")
			updateRow(t, sessions, "ps", func(r *sessionstore.Session) { r.State = session.StateResumePending })
			if held, err := sw.Sweep(ctx); err != nil || held != 1 {
				t.Fatalf("resume_pending Sweep: held=%d err=%v, want 1", held, err)
			}
			if got := generationOf(t, sessions, "ps"); got != 2 {
				t.Fatalf("generation = %d, want 2 (one bump on adoption)", got)
			}
			if len(readopter.calls) != 0 {
				t.Fatalf("readopt calls = %d, want 0 for a resume_pending adoption", len(readopter.calls))
			}
		})
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 10.1 (hold
// state on connection loss)
// A bound resume_pending session whose held channel died is evicted and its
// lease released, and a later sweep adopts it with one bump and no readopt.
func TestSweepDeadConnectionResumePendingIsReadopted_spec_10_1_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "rp", TenantID: "acme", State: session.StateResumePending, PodAssignment: "pod-failed"})
	leases := newCountingLeases()
	leases.holders[lk("acme", "rp")] = "rep-1"
	bindings := newFakeBindings()
	bindings.bind("rp", 1)
	bindings.alive["rp"] = false
	readopter := &fakeReadopter{leases: leases.fakeLeases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Bindings: bindings, Readopter: readopter})

	if held, err := sw.Sweep(ctx); err != nil || held != 0 {
		t.Fatalf("dead-connection Sweep: held=%d err=%v", held, err)
	}
	if !bindings.evicted["rp"] || leases.released["rp"] != 1 {
		t.Fatalf("dead binding evicted=%v releases=%d, want evicted and released once", bindings.evicted["rp"], leases.released["rp"])
	}
	if held, err := sw.Sweep(ctx); err != nil || held != 1 {
		t.Fatalf("adoption Sweep: held=%d err=%v, want 1", held, err)
	}
	if got := generationOf(t, sessions, "rp"); got != 2 {
		t.Fatalf("generation = %d, want 2", got)
	}
	if len(readopter.calls) != 0 {
		t.Fatalf("readopt calls = %d, want 0 (the resume_pending pod failed)", len(readopter.calls))
	}
}

// spec: 10.1.2 (Coordinator Handoff Protocol)
// diagnosis: the takeover bump is the step 1 compare-and-swap on the
// generation the sweep listed. A failure means a takeover bumped a row whose
// generation another writer had already advanced, readopted a pod the locked
// row no longer holds, adopted a row that left the adopted states, or wrote a
// mirror row at a generation other than the one the handoff wrote.
func TestRecordHandoffComparesListedRow_spec_10_1_2(t *testing.T) {
	ctx := context.Background()
	newRace := func(t *testing.T, mutate func(*sessionstore.Session)) (*raceStore, *countingLeases, *fakeReadopter, *coordlease.MemoryStore, *Sweeper) {
		t.Helper()
		base := memstore.New()
		mustCreate(t, base, sessionstore.Session{ID: "s", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-s"})
		store := &raceStore{Store: base, beforeUpdate: map[string]func(){
			"s": func() { updateRow(t, base, "s", mutate) },
		}}
		leases := newCountingLeases()
		bindings := newFakeBindings()
		readopter := &fakeReadopter{leases: leases.fakeLeases, tenantID: "acme", replicaID: "rep-1", bindings: bindings}
		mirror := coordlease.NewMemoryStore(nil)
		sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, store, leases,
			Options{ReplicaID: "rep-1", Mirror: mirror, Bindings: bindings, Readopter: readopter})
		return store, leases, readopter, mirror, sw
	}

	t.Run("generation advanced past the listing", func(t *testing.T) {
		store, leases, readopter, mirror, sw := newRace(t, func(r *sessionstore.Session) { r.CoordinationGeneration = 5 })
		if held, err := sw.Sweep(ctx); err != nil || held != 0 {
			t.Fatalf("Sweep: held=%d err=%v, want 0", held, err)
		}
		if got := generationOf(t, store, "s"); got != 5 {
			t.Fatalf("generation = %d, want 5 (left as the other writer set it)", got)
		}
		if leases.released["s"] != 1 {
			t.Fatalf("lease releases = %d, want 1 (claim discarded)", leases.released["s"])
		}
		if len(readopter.calls) != 0 {
			t.Fatal("a pod was readopted after the compare-and-swap missed")
		}
		if _, ok := heldMirrorGen(t, mirror, "rep-1", "s"); ok {
			t.Fatal("a mirror row was written after the compare-and-swap missed")
		}
	})

	t.Run("stored row moved to resume_pending at the listed generation", func(t *testing.T) {
		store, leases, readopter, mirror, sw := newRace(t, func(r *sessionstore.Session) { r.State = session.StateResumePending })
		if held, err := sw.Sweep(ctx); err != nil || held != 1 {
			t.Fatalf("Sweep: held=%d err=%v, want 1", held, err)
		}
		if got := generationOf(t, store, "s"); got != 2 {
			t.Fatalf("generation = %d, want 2", got)
		}
		if len(readopter.calls) != 0 {
			t.Fatal("the failed pod of a resume_pending row was readopted")
		}
		if h, ok := leases.held("acme", "s"); !ok || h != "rep-1" {
			t.Fatalf("lease holder = %q ok=%v, want rep-1", h, ok)
		}
		if gen, ok := heldMirrorGen(t, mirror, "rep-1", "s"); !ok || gen != 2 {
			t.Fatalf("mirror row = (%d, %v), want the handoff generation 2", gen, ok)
		}
	})

	t.Run("listed held-pod row is now podless", func(t *testing.T) {
		store, leases, readopter, _, sw := newRace(t, func(r *sessionstore.Session) {
			r.State = session.StateSuspended
			r.PodAssignment = ""
		})
		if held, err := sw.Sweep(ctx); err != nil || held != 0 {
			t.Fatalf("Sweep: held=%d err=%v, want 0", held, err)
		}
		if got := generationOf(t, store, "s"); got != 1 {
			t.Fatalf("generation = %d, want 1 (no bump)", got)
		}
		if leases.released["s"] != 1 || len(readopter.calls) != 0 {
			t.Fatalf("releases=%d readopts=%d, want the claim discarded and no readopt", leases.released["s"], len(readopter.calls))
		}
	})

	t.Run("direct compare-and-swap", func(t *testing.T) {
		sessions := memstore.New()
		mustCreate(t, sessions, sessionstore.Session{ID: "held", TenantID: "acme", State: session.StateSuspended, PodAssignment: "pod-h", CoordinationGeneration: 3})
		mustCreate(t, sessions, sessionstore.Session{ID: "done", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-d"})
		mustCreate(t, sessions, sessionstore.Session{ID: "resuming", TenantID: "acme", State: session.StateResuming, PodAssignment: "pod-r"})
		sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, newFakeLeases(), Options{ReplicaID: "rep-1"})
		if gen, holds := sw.RecordHandoff(ctx, "acme", "held", 2); gen != 0 || holds {
			t.Errorf("stale expected generation: (%d, %v), want (0, false)", gen, holds)
		}
		if gen, holds := sw.RecordHandoff(ctx, "acme", "held", 3); gen != 4 || !holds {
			t.Errorf("matching expected generation: (%d, %v), want (4, true)", gen, holds)
		}
		for _, id := range []string{"done", "resuming"} {
			if gen, holds := sw.RecordHandoff(ctx, "acme", id, 1); gen != 0 || holds {
				t.Errorf("%s: (%d, %v), want (0, false)", id, gen, holds)
			}
			if got := generationOf(t, sessions, id); got != 1 {
				t.Errorf("%s generation = %d, want 1", id, got)
			}
		}
	})
}

// spec: 10.1.2 (Coordinator Handoff Protocol), 7.2 (Message delivery routing —
// seven paths, path 6)
// diagnosis: the takeover delivery hook must run exactly once per takeover
// that published a binding, after the publish, with the handoff generation. A
// failure means held messages are delivered before the pod is fenced, twice,
// on a takeover that bound nothing, or not at all.
func TestSweepCallsHeldDelivererAfterTakeoverPublish_spec_10_1_2(t *testing.T) {
	ctx := context.Background()
	base := memstore.New()
	mustCreate(t, base, sessionstore.Session{ID: "ok", TenantID: "acme", State: session.StateSuspended, PodAssignment: "pod-ok"})
	mustCreate(t, base, sessionstore.Session{ID: "fencefail", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-ff"})
	mustCreate(t, base, sessionstore.Session{ID: "bumpfail", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-bf"})
	mustCreate(t, base, sessionstore.Session{ID: "recovering", TenantID: "acme", State: session.StateResumePending})
	mustCreate(t, base, sessionstore.Session{ID: "renew", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-rn"})
	// A concurrent writer advances bumpfail's generation before every handoff
	// compare-and-swap, so each takeover of it misses.
	store := &raceStore{Store: base, beforeUpdate: map[string]func(){}}
	var advance func()
	advance = func() {
		updateRow(t, base, "bumpfail", func(r *sessionstore.Session) { r.CoordinationGeneration++ })
		store.beforeUpdate["bumpfail"] = advance
	}
	store.beforeUpdate["bumpfail"] = advance
	leases := newFakeLeases()
	bindings := newFakeBindings()
	bindings.bind("renew", 1)
	readopter := &fakeReadopter{leases: leases, tenantID: "acme", replicaID: "rep-1", bindings: bindings, fail: map[string]bool{"fencefail": true}}
	deliverer := &recordingDeliverer{bindings: bindings}
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, store, leases, Options{
		ReplicaID: "rep-1", Bindings: bindings, Readopter: readopter, HeldDeliverer: deliverer,
		AdoptionBackoff: time.Hour,
	})

	for pass := 1; pass <= 2; pass++ {
		if _, err := sw.Sweep(ctx); err != nil {
			t.Fatalf("Sweep %d: %v", pass, err)
		}
	}
	if len(deliverer.calls) != 1 {
		t.Fatalf("deliverer calls = %+v, want exactly one (the published takeover)", deliverer.calls)
	}
	got := deliverer.calls[0]
	if got.tenantID != "acme" || got.sessionID != "ok" || got.generation != 2 {
		t.Fatalf("deliverer call = %+v, want acme/ok at the handoff generation 2", got)
	}
	if !got.boundAtCall {
		t.Fatal("the deliverer ran before the takeover published the binding")
	}

	// A nil hook is skipped.
	mustCreate(t, base, sessionstore.Session{ID: "nilhook", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-nh"})
	nilSweeper := NewSweeper(fakeTenants{ids: []string{"acme"}}, base, newFakeLeases(), Options{ReplicaID: "rep-2", Readopter: &fakeReadopter{bindings: newFakeBindings()}})
	if _, err := nilSweeper.Sweep(ctx); err != nil {
		t.Fatalf("nil-hook Sweep: %v", err)
	}
}

// spec: 10.1.1 (Stateless Replicas and Per-Session Coordination), 7.2
// (Interactive Session Model)
// diagnosis: a binding this replica holds for a session another replica ended
// must reach the terminal releaser on every listing. A failure means a bound
// terminal row was not handed over, an unbound or live row was, the terminal
// branch wrote a lease or evicted the binding itself, or later rows stopped
// being renewed.
func TestSweepHandsBoundTerminalRowsToReleaser_spec_10_1_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "t-bound", TenantID: "acme", State: session.StateCompleted, PodAssignment: "pod-1"})
	mustCreate(t, sessions, sessionstore.Session{ID: "t-cancelled", TenantID: "acme", State: session.StateCancelled, PodAssignment: "pod-2"})
	mustCreate(t, sessions, sessionstore.Session{ID: "t-unbound", TenantID: "acme", State: session.StateFailed})
	mustCreate(t, sessions, sessionstore.Session{ID: "live", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-3"})
	leases := newCountingLeases()
	bindings := newFakeBindings()
	bindings.bind("t-bound", 1)
	bindings.bind("t-cancelled", 1)
	bindings.bind("live", 1)
	releaser := &recordingReleaser{}
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
		Options{ReplicaID: "rep-1", Bindings: bindings, TerminalReleaser: releaser})

	held, err := sw.Sweep(ctx)
	if err != nil || held != 1 {
		t.Fatalf("Sweep: held=%d err=%v, want the live session renewed", held, err)
	}
	got := map[string]session.State{}
	for _, r := range releaser.rows {
		got[r.ID] = r.State
	}
	if len(releaser.rows) != 2 || got["t-bound"] != session.StateCompleted || got["t-cancelled"] != session.StateCancelled {
		t.Fatalf("releaser rows = %v, want t-bound and t-cancelled once each", got)
	}
	for _, id := range []string{"t-bound", "t-cancelled", "t-unbound"} {
		if leases.acquired[id] != 0 || leases.released[id] != 0 || bindings.evicted[id] {
			t.Errorf("%s: acquires=%d releases=%d evicted=%v, want no lease write and no eviction", id, leases.acquired[id], leases.released[id], bindings.evicted[id])
		}
	}
	if h, ok := leases.held("acme", "live"); !ok || h != "rep-1" {
		t.Errorf("live lease holder = %q ok=%v, want rep-1", h, ok)
	}

	// A nil releaser is skipped and the sweep still renews.
	nilSweeper := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, newFakeLeases(), Options{ReplicaID: "rep-1", Bindings: bindings})
	if held, err := nilSweeper.Sweep(ctx); err != nil || held != 1 {
		t.Fatalf("nil-releaser Sweep: held=%d err=%v", held, err)
	}
}

// spec: 10.1.2 (Coordinator Handoff Protocol), 7.2 (Interactive Session Model)
// diagnosis: a readopt that published a binding for a row a terminal write
// ended during the dial and fence leaves a binding no post-terminal hook saw.
// A failure means the next sweep did not hand that binding to the terminal
// releaser, so the pod stays bound to an ended session.
func TestSweepReapsBindingPublishedAfterTerminalWrite_spec_10_1_2(t *testing.T) {
	ctx := context.Background()
	for _, state := range []session.State{session.StateRunning, session.StateSuspended} {
		t.Run(string(state), func(t *testing.T) {
			sessions := memstore.New()
			mustCreate(t, sessions, sessionstore.Session{ID: "s", TenantID: "acme", State: state, PodAssignment: "pod-s"})
			leases := newFakeLeases()
			bindings := newFakeBindings()
			readopter := &fakeReadopter{
				leases: leases, tenantID: "acme", replicaID: "rep-1", bindings: bindings,
				onReadopt: func(id string) {
					updateRow(t, sessions, id, func(r *sessionstore.Session) { r.State = session.StateCancelled })
				},
			}
			releaser := &recordingReleaser{}
			sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases,
				Options{ReplicaID: "rep-1", Bindings: bindings, Readopter: readopter, TerminalReleaser: releaser})

			if _, err := sw.Sweep(ctx); err != nil {
				t.Fatalf("takeover Sweep: %v", err)
			}
			if !bindings.bound["s"] || len(releaser.rows) != 0 {
				t.Fatalf("after takeover bound=%v releaser=%d, want a published binding and no release yet", bindings.bound["s"], len(releaser.rows))
			}
			if _, err := sw.Sweep(ctx); err != nil {
				t.Fatalf("reap Sweep: %v", err)
			}
			if len(releaser.rows) != 1 || releaser.rows[0].ID != "s" || releaser.rows[0].State != session.StateCancelled {
				t.Fatalf("releaser rows = %+v, want the cancelled row once", releaser.rows)
			}
			if releaser.rows[0].CoordinationGeneration != 2 {
				t.Errorf("released row generation = %d, want the handoff generation 2", releaser.rows[0].CoordinationGeneration)
			}
		})
	}
}
