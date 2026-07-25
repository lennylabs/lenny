// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/storage/leasestore"
)

// fakeLeaseStore is an in-memory leasestore.LeaseStore for the at-bind
// acquire tests. It honours the Acquire holder fencing (a held lease is
// re-acquirable only by its current holder; a different holder gets
// ErrHeld) so a test can preset a live foreign holder and assert the
// bind funnel fails closed. acquires counts the Acquire calls so a test
// can assert the idempotent self-renew still hit the store.
type fakeLeaseStore struct {
	holders  map[string]string // "tenant/session" -> holder
	acquires int
}

func newFakeLeaseStore() *fakeLeaseStore {
	return &fakeLeaseStore{holders: map[string]string{}}
}

func leaseK(tenantID, sessionID string) string { return tenantID + "/" + sessionID }

func (f *fakeLeaseStore) Acquire(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	f.acquires++
	k := leaseK(tenantID, sessionID)
	if cur, ok := f.holders[k]; ok && cur != holder {
		return leasestore.Lease{}, leasestore.ErrHeld
	}
	f.holders[k] = holder
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *fakeLeaseStore) Renew(_ context.Context, tenantID, sessionID, holder string, _ time.Duration) (leasestore.Lease, error) {
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: holder}, nil
}

func (f *fakeLeaseStore) Release(_ context.Context, tenantID, sessionID, holder string) error {
	k := leaseK(tenantID, sessionID)
	if cur, ok := f.holders[k]; ok && cur == holder {
		delete(f.holders, k)
	}
	return nil
}

func (f *fakeLeaseStore) Get(_ context.Context, tenantID, sessionID string) (leasestore.Lease, error) {
	k := leaseK(tenantID, sessionID)
	h, ok := f.holders[k]
	if !ok {
		return leasestore.Lease{}, leasestore.ErrNotFound
	}
	return leasestore.Lease{TenantID: tenantID, SessionID: sessionID, Holder: h}, nil
}

func (f *fakeLeaseStore) DeleteByUser(context.Context, string, string) (int, error) { return 0, nil }

func (f *fakeLeaseStore) DeleteByTenant(context.Context, string) (int, error) { return 0, nil }

// leaseTestServer builds a Server wired only with the pieces the at-bind
// acquire and registerBinding funnel exercise: a session store, a pod
// registry, and the injected leaseStore/replicaID.
func leaseTestServer(t *testing.T, leases leasestore.LeaseStore, replicaID string) (*Server, *podsession.Registry, sessionstore.Store) {
	t.Helper()
	store := memstore.New()
	registry := podsession.NewRegistry()
	srv := New(store, Options{
		PodRegistry:            registry,
		CoordinationLeaseStore: leases,
		ReplicaID:              replicaID,
	})
	return srv, registry, store
}

func seedRunningRow(t *testing.T, store sessionstore.Store, tenantID, id string) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:         id,
		TenantID:   tenantID,
		State:      session.StateRunning,
		RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease; fail-closed on a live foreign holder)
//
// A nil leaseStore or an empty replicaID is the in-memory / dev posture
// with no Redis leasestore: the at-bind acquire is a no-op so the bind
// still publishes as before.
func TestAcquireCoordinationLease_DisabledIsNoOp(t *testing.T) {
	t.Run("nil store", func(t *testing.T) {
		srv, _, _ := leaseTestServer(t, nil, "replica-a")
		if err := srv.acquireCoordinationLease(context.Background(), "acme", "sess-1"); err != nil {
			t.Fatalf("nil leaseStore should no-op, got %v", err)
		}
	})
	t.Run("empty replica", func(t *testing.T) {
		leases := newFakeLeaseStore()
		srv, _, _ := leaseTestServer(t, leases, "")
		if err := srv.acquireCoordinationLease(context.Background(), "acme", "sess-1"); err != nil {
			t.Fatalf("empty replicaID should no-op, got %v", err)
		}
		if leases.acquires != 0 {
			t.Fatalf("empty replicaID must not touch the leaseStore, acquires=%d", leases.acquires)
		}
	})
}

// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease)
//
// The at-bind acquire holds the lease for the binding replica and, being
// idempotent for the same holder, a later acquire on a hoisted path
// self-renews without an ErrHeld.
func TestAcquireCoordinationLease_HoldsAndSelfRenews(t *testing.T) {
	leases := newFakeLeaseStore()
	srv, _, _ := leaseTestServer(t, leases, "replica-a")
	ctx := context.Background()

	if err := srv.acquireCoordinationLease(ctx, "acme", "sess-1"); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	got, err := leases.Get(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatalf("lease not held after acquire: %v", err)
	}
	if got.Holder != "replica-a" {
		t.Fatalf("lease holder = %q, want replica-a", got.Holder)
	}
	// The hoisted paths acquire ahead of the running-commit and then run
	// registerBinding, which acquires again; the same-holder self-renew
	// must not fail.
	if err := srv.acquireCoordinationLease(ctx, "acme", "sess-1"); err != nil {
		t.Fatalf("self-renew acquire: %v", err)
	}
	if leases.acquires != 2 {
		t.Fatalf("acquires = %d, want 2 (both hit the store)", leases.acquires)
	}
}

// spec: 10.1 (per-session coordination lease; fail-closed on a live
// foreign holder)
//
// A live foreign holder makes the acquire return leasestore.ErrHeld
// unwrapped so a caller can branch on errors.Is and fail the bind closed.
func TestAcquireCoordinationLease_ForeignHolderErrHeld(t *testing.T) {
	leases := newFakeLeaseStore()
	// A live foreign replica already coordinates the session.
	if _, err := leases.Acquire(context.Background(), "acme", "sess-1", "replica-b", time.Minute); err != nil {
		t.Fatalf("preset foreign holder: %v", err)
	}
	srv, _, _ := leaseTestServer(t, leases, "replica-a")

	err := srv.acquireCoordinationLease(context.Background(), "acme", "sess-1")
	if !errors.Is(err, leasestore.ErrHeld) {
		t.Fatalf("acquire against a foreign holder = %v, want ErrHeld", err)
	}
}

// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease; fail-closed on a live foreign holder)
//
// Regression for the co-location fix: registerBinding acquires the lease
// before it publishes the binding, so a bind that meets a live foreign
// holder publishes NOTHING and returns the error. Against the pre-fix
// void registerBinding (which always Put the binding into the shared
// registry regardless of the lease) this test fails, because the executor
// would then serve the session on a replica a foreign holder still
// coordinates.
func TestRegisterBinding_ForeignHolderPublishesNothing(t *testing.T) {
	leases := newFakeLeaseStore()
	if _, err := leases.Acquire(context.Background(), "acme", "sess-held", "replica-b", time.Minute); err != nil {
		t.Fatalf("preset foreign holder: %v", err)
	}
	srv, registry, _ := leaseTestServer(t, leases, "replica-a")

	err := srv.registerBinding(context.Background(), &podsession.BindResult{
		SessionID:   "sess-held",
		TenantID:    "acme",
		SandboxName: "sbx-1",
	})
	if !errors.Is(err, leasestore.ErrHeld) {
		t.Fatalf("registerBinding against a foreign holder = %v, want ErrHeld", err)
	}
	if _, ok := registry.Get("sess-held"); ok {
		t.Fatal("registerBinding published a binding for a session a foreign replica holds (fail-open double-bind)")
	}
}

// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease)
//
// On an unheld lease registerBinding acquires it for this replica and then
// publishes the binding, so the lease holder is the binding holder from
// bind time.
func TestRegisterBinding_AcquiresThenPublishes(t *testing.T) {
	leases := newFakeLeaseStore()
	srv, registry, store := leaseTestServer(t, leases, "replica-a")
	seedRunningRow(t, store, "acme", "sess-fresh")

	err := srv.registerBinding(context.Background(), &podsession.BindResult{
		SessionID:   "sess-fresh",
		TenantID:    "acme",
		SandboxName: "sbx-1",
	})
	if err != nil {
		t.Fatalf("registerBinding on an unheld lease: %v", err)
	}
	if _, ok := registry.Get("sess-fresh"); !ok {
		t.Fatal("registerBinding did not publish the binding after acquiring the lease")
	}
	got, gerr := leases.Get(context.Background(), "acme", "sess-fresh")
	if gerr != nil {
		t.Fatalf("lease not held after registerBinding: %v", gerr)
	}
	if got.Holder != "replica-a" {
		t.Fatalf("lease holder = %q, want replica-a", got.Holder)
	}
}

// spec: 4.6.1 (coordinating replica holds the lease), 10.1 (per-session
// coordination lease)
//
// In the dev / in-memory posture (no leaseStore wired) registerBinding
// publishes the binding as before, so the executor still serves.
func TestRegisterBinding_NoLeaseStorePublishes(t *testing.T) {
	srv, registry, store := leaseTestServer(t, nil, "replica-a")
	seedRunningRow(t, store, "acme", "sess-dev")

	err := srv.registerBinding(context.Background(), &podsession.BindResult{
		SessionID:   "sess-dev",
		TenantID:    "acme",
		SandboxName: "sbx-1",
	})
	if err != nil {
		t.Fatalf("registerBinding with no leaseStore: %v", err)
	}
	if _, ok := registry.Get("sess-dev"); !ok {
		t.Fatal("registerBinding must publish the binding in the no-leaseStore posture")
	}
}
