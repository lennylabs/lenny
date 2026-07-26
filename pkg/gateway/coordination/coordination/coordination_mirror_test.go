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

// fakeTenants is a static TenantLister.
type fakeTenants struct{ ids []string }

func (f fakeTenants) ListTenants(context.Context) ([]string, error) { return f.ids, nil }

// fakeLeases is an in-memory leasestore.LeaseStore for the sweeper test:
// it tracks the holder per (tenant, session) and honours the Acquire
// fencing (a held lease can be re-acquired only by its current holder
// when stealAllowed is false). The test drives it via preset to model a
// prior holder for the handoff case.
type fakeLeases struct {
	holders map[string]string // "tenant/session" -> holder
}

func newFakeLeases() *fakeLeases { return &fakeLeases{holders: map[string]string{}} }

func lk(t, s string) string { return t + "/" + s }

func (f *fakeLeases) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	k := lk(tenantID, sessionID)
	if cur, ok := f.holders[k]; ok && cur != holder {
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	f.holders[k] = holder
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *fakeLeases) Renew(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *fakeLeases) Release(_ context.Context, tenantID, sessionID, holder string) error {
	delete(f.holders, lk(tenantID, sessionID))
	return nil
}

func (f *fakeLeases) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	k := lk(tenantID, sessionID)
	h, ok := f.holders[k]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (f *fakeLeases) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }
func (f *fakeLeases) DeleteByTenant(context.Context, string) (int, error)       { return 0, nil }

func mustCreate(t *testing.T, store sessionstore.Store, s sessionstore.Session) {
	t.Helper()
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Unix(1, 0).UTC()
	}
	if err := store.Create(context.Background(), s); err != nil {
		t.Fatalf("create session %s: %v", s.ID, err)
	}
}

// spec: §10.1 line 165 — the sweep mirrors every lease this replica holds
// into the coordination_lease barrier-target table, and a terminal
// session's row is marked released so the barrier-target query stops
// returning it.
func TestSweepMirrorsHeldLeasesAndReleasesTerminal_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	// The running sessions carry a persisted pod assignment so the sweep
	// adopts their lapsed lease as a still-running-pod orphan and mirrors it.
	mustCreate(t, sessions, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-s1", CoordinationGeneration: 2})
	mustCreate(t, sessions, sessionstore.Session{ID: "s2", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-s2"})
	mustCreate(t, sessions, sessionstore.Session{ID: "done", TenantID: "acme", State: session.StateCompleted})

	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, newFakeLeases(), Options{
		ReplicaID: "rep-1",
		Mirror:    mirror,
	})

	held, err := sw.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if held != 2 {
		t.Fatalf("held = %d, want 2 (terminal session not counted)", held)
	}

	rows, err := mirror.ListHeldByReplica(ctx, "rep-1")
	if err != nil {
		t.Fatalf("ListHeldByReplica: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("mirror rows = %d, want 2 (terminal session released)", len(rows))
	}
	var sawS1 bool
	for _, r := range rows {
		if r.SessionID == "done" {
			t.Errorf("terminal session 'done' present in barrier-target set")
		}
		if r.SessionID == "s1" {
			sawS1 = true
			if r.CoordinationGeneration != 2 {
				t.Errorf("s1 mirrored generation = %d, want 2", r.CoordinationGeneration)
			}
		}
	}
	if !sawS1 {
		t.Errorf("s1 missing from mirror")
	}
}

// spec: §4.6.1 — the sweep records this replica's dialable inter-replica
// address as the mirror row's coordinator_address alongside its identity,
// so the eviction-forward hop resolves a routable target for the current
// coordinator. Co-location makes the coordinator the binding holder, so the
// sweep mirrors the session this replica binds rather than an arbitrary
// non-terminal session it happens to acquire.
func TestSweepRecordsInterReplicaAddressInMirror_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning})

	bindings := newFakeBindings()
	bindings.bound["s1"] = true
	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, newFakeLeases(), Options{
		ReplicaID:           "rep-1",
		InterReplicaAddress: "10.0.0.1:50054",
		Mirror:              mirror,
		Bindings:            bindings,
	})
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	got, found, err := mirror.GetBySession(ctx, "acme", "s1")
	if err != nil || !found {
		t.Fatalf("GetBySession: found=%v err=%v, want found, nil", found, err)
	}
	if got.CoordinatorReplica != "rep-1" {
		t.Errorf("coordinator_replica = %q, want rep-1", got.CoordinatorReplica)
	}
	if got.CoordinatorAddress != "10.0.0.1:50054" {
		t.Errorf("coordinator_address = %q, want 10.0.0.1:50054", got.CoordinatorAddress)
	}
}

// spec: §4.6.1 — a cross-replica coordinator handoff overwrites both the
// coordinator identity and its dialable address in the mirror, so the
// eviction-forward hop dials the new holder rather than a stale predecessor.
// Under co-location the handoff is the binding moving: the lease travels
// with the binding, so the replica that binds the session next is the
// coordinator the mirror must name.
func TestSweepHandoffOverwritesMirrorAddress_spec_4_6_1(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning})

	mirror := coordlease.NewMemoryStore(nil)
	leases := newFakeLeases()

	// rep-1 binds s1, so its sweep renews the co-located lease and records
	// its own identity and address.
	rep1Bindings := newFakeBindings()
	rep1Bindings.bound["s1"] = true
	sw1 := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID: "rep-1", InterReplicaAddress: "10.0.0.1:50054",
		Mirror: mirror, Bindings: rep1Bindings,
	})
	if _, err := sw1.Sweep(ctx); err != nil {
		t.Fatalf("rep-1 Sweep: %v", err)
	}

	// The binding moves to rep-2 and rep-1 relinquishes the co-located
	// lease, so rep-2's sweep overwrites the mirror with its identity and
	// address.
	if err := leases.Release(ctx, "acme", "s1", "rep-1"); err != nil {
		t.Fatalf("release rep-1 lease: %v", err)
	}
	rep2Bindings := newFakeBindings()
	rep2Bindings.bound["s1"] = true
	sw2 := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{
		ReplicaID: "rep-2", InterReplicaAddress: "10.0.0.2:50054",
		Mirror: mirror, Bindings: rep2Bindings,
	})
	if _, err := sw2.Sweep(ctx); err != nil {
		t.Fatalf("rep-2 Sweep: %v", err)
	}

	got, found, err := mirror.GetBySession(ctx, "acme", "s1")
	if err != nil || !found {
		t.Fatalf("GetBySession: found=%v err=%v, want found, nil", found, err)
	}
	if got.CoordinatorReplica != "rep-2" || got.CoordinatorAddress != "10.0.0.2:50054" {
		t.Errorf("mirror after handoff = (%q, %q), want (rep-2, 10.0.0.2:50054)",
			got.CoordinatorReplica, got.CoordinatorAddress)
	}
}

// spec: §10.1 line 165 — a session this replica cannot acquire (held by
// another replica) is not mirrored under this replica's id.
func TestSweepSkipsForeignLeaseInMirror_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "mine", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-mine"})
	mustCreate(t, sessions, sessionstore.Session{ID: "theirs", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-theirs"})

	leases := newFakeLeases()
	// "theirs" is already held by rep-2.
	leases.holders[lk("acme", "theirs")] = "rep-2"

	mirror := coordlease.NewMemoryStore(nil)
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, leases, Options{ReplicaID: "rep-1", Mirror: mirror})
	if _, err := sw.Sweep(ctx); err != nil {
		t.Fatalf("Sweep: %v", err)
	}

	rows, _ := mirror.ListHeldByReplica(ctx, "rep-1")
	if len(rows) != 1 || rows[0].SessionID != "mine" {
		t.Fatalf("rep-1 mirror = %+v, want only 'mine'", rows)
	}
}

// spec: §10.1 line 165 — a nil Mirror disables mirroring without
// affecting the sweep (the barrier then falls back to the in-memory
// lease cache).
func TestSweepNilMirrorIsNoop_spec_10_1_165(t *testing.T) {
	ctx := context.Background()
	sessions := memstore.New()
	mustCreate(t, sessions, sessionstore.Session{ID: "s1", TenantID: "acme", State: session.StateRunning, PodAssignment: "pod-s1"})
	sw := NewSweeper(fakeTenants{ids: []string{"acme"}}, sessions, newFakeLeases(), Options{ReplicaID: "rep-1"})
	if held, err := sw.Sweep(ctx); err != nil || held != 1 {
		t.Fatalf("Sweep with nil mirror: held=%d err=%v, want 1, nil", held, err)
	}
}
