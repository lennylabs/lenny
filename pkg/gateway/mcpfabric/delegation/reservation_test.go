// SPDX-License-Identifier: MIT

package delegation_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegation/export"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

// reserveStore wraps a session store so a test can inject the error each
// reservation or insert method returns and observe which ran.
type reserveStore struct {
	sessionstore.Store

	mu                sync.Mutex
	reserveErr        error
	createErr         error
	createReservedErr error
	calls             []string
	// beforeChildInsert runs before the child row's insert reaches the
	// wrapped store, after every admission gate passed.
	beforeChildInsert func()
}

func (r *reserveStore) note(name string) {
	r.mu.Lock()
	r.calls = append(r.calls, name)
	r.mu.Unlock()
}

func (r *reserveStore) count(name string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if c == name {
			n++
		}
	}
	return n
}

func (r *reserveStore) Reserve(ctx context.Context, tenantID, owner, id string, child bool) error {
	r.note("Reserve")
	if r.reserveErr != nil {
		return r.reserveErr
	}
	return r.Store.Reserve(ctx, tenantID, owner, id, child)
}

func (r *reserveStore) Create(ctx context.Context, s sessionstore.Session) error {
	r.note("Create")
	if s.ID == "sess_child" && r.beforeChildInsert != nil {
		r.beforeChildInsert()
	}
	if r.createErr != nil && s.ID == "sess_child" {
		return r.createErr
	}
	return r.Store.Create(ctx, s)
}

func (r *reserveStore) CreateReserved(ctx context.Context, s sessionstore.Session) error {
	r.note("CreateReserved")
	if r.beforeChildInsert != nil {
		r.beforeChildInsert()
	}
	if r.createReservedErr != nil {
		return r.createReservedErr
	}
	return r.Store.CreateReserved(ctx, s)
}

// recordingObjects is a session-object deleter that records each deletion
// and can fail it.
type recordingObjects struct {
	mu      sync.Mutex
	deleted []string
	ctxErr  []error
	err     error
}

func (o *recordingObjects) DeleteBySession(ctx context.Context, _, sessionID string) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.deleted = append(o.deleted, sessionID)
	o.ctxErr = append(o.ctxErr, ctx.Err())
	return 1, o.err
}

type delegationFixture struct {
	mem     *memstore.Store
	store   *reserveStore
	users   *userstore.Memory
	tenants *tenantstore.Memory
	mat     *fakeMaterializer
	objects *recordingObjects
}

// newDelegationFixture builds the service over a reservation-observing store
// with a running parent. Each mutate function adjusts the service options
// before the service is built.
func newDelegationFixture(t *testing.T, wireObjects bool, mutate ...func(*delegation.Options)) (*delegationFixture, *delegation.Service) {
	t.Helper()
	f := &delegationFixture{
		users:   userstore.NewMemory(),
		tenants: tenantstore.NewMemory(),
		mat:     &fakeMaterializer{},
		objects: &recordingObjects{},
	}
	if err := f.tenants.Create(context.Background(), tenantstore.Tenant{ID: "acme"}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	if err := f.users.Create(context.Background(), userstore.User{Subject: "user_alice", TenantID: "acme"}); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	f.mem = memstore.New(memstore.WithCreationMarkers(sessionstore.StoreMarkers(f.tenants, f.users)))
	f.store = &reserveStore{Store: f.mem}
	seedParent(t, f.mem, "sess_parent", "", "gemini", "pool-b", isolation.ProfileSandboxed)
	opts := delegation.Options{
		Clock:              func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) },
		IDFunc:             func() string { return "sess_child" },
		ExportMaterializer: f.mat,
	}
	if wireObjects {
		opts.SessionObjects = f.objects
	}
	for _, m := range mutate {
		m(&opts)
	}
	return f, delegation.NewService(f.store, opts)
}

func (f *delegationFixture) reserved(t *testing.T) []string {
	t.Helper()
	ids, err := f.mem.ReservedSessionIDs(context.Background(), "acme", "user_alice")
	if err != nil {
		t.Fatalf("ReservedSessionIDs: %v", err)
	}
	return ids
}

func delegateExport(ctx context.Context, svc *delegation.Service) (delegation.Result, error) {
	return svc.Delegate(ctx, "acme", delegation.Request{
		ParentSessionID:  "sess_parent",
		RuntimeRef:       "claude",
		PoolRef:          "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
		FileExport:       []export.Spec{{Source: "docs/*.txt"}},
	})
}

// TestDelegateExportReservesAndInsertsReserved_spec_12_8 pins the
// reservation of a delegated child that exports files: the child ID is
// reserved for the parent's owner before the export runs, the child row
// inserts through CreateReserved, and the reservation ends with the
// insert. A child without exports takes no reservation.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateExportReservesAndInsertsReserved_spec_12_8(t *testing.T) {
	f, svc := newDelegationFixture(t, true)
	if _, err := delegateExport(context.Background(), svc); err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	if f.store.count("Reserve") != 1 || f.store.count("CreateReserved") != 1 {
		t.Fatalf("calls = %v, want one Reserve and one CreateReserved", f.store.calls)
	}
	if ids := f.reserved(t); len(ids) != 0 {
		t.Errorf("reservation outlived the insert: %v", ids)
	}

	g, svc2 := newDelegationFixture(t, true)
	if _, err := svc2.Delegate(context.Background(), "acme", delegation.Request{
		ParentSessionID: "sess_parent", RuntimeRef: "claude", PoolRef: "pool-a",
		IsolationProfile: isolation.ProfileSandboxed,
	}); err != nil {
		t.Fatalf("Delegate without exports: %v", err)
	}
	if g.store.count("Reserve") != 0 || g.store.count("Create") < 1 {
		t.Errorf("a child without exports reserved or skipped Create: %v", g.store.calls)
	}
}

// TestDelegateReserveRefusalExportsNothing_spec_12_8 pins a reservation
// the fence refuses: the *CreationRefusal reaches the caller unwrapped,
// and neither the export nor the insert runs. A non-refusal Reserve
// error is returned the same way.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateReserveRefusalExportsNothing_spec_12_8(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
	}{
		{"refusal", &sessionstore.CreationRefusal{Code: "TENANT_SUSPENDED", Message: "m", Details: map[string]any{"tenantId": "acme"}}},
		{"error", errors.New("reservation insert failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, svc := newDelegationFixture(t, true)
			f.store.reserveErr = tc.err
			_, err := delegateExport(context.Background(), svc)
			if !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			var ref *sessionstore.CreationRefusal
			if tc.name == "refusal" && (!errors.As(err, &ref) || ref.Code != "TENANT_SUSPENDED") {
				t.Errorf("refusal not returned as *CreationRefusal: %v", err)
			}
			if f.mat.called {
				t.Errorf("the export ran after the reservation failed")
			}
			if f.store.count("Create")+f.store.count("CreateReserved") != 0 {
				t.Errorf("the child was inserted after the reservation failed")
			}
		})
	}
}

// TestDelegateExportUndo_spec_12_8 pins the undo of a delegated child's
// export: an export failure and an insert refusal each delete the objects
// under the child ID and release the reservation, including when the
// request context is cancelled; a non-refusal insert error, a deletion
// error, and an unwired deleter each keep the reservation.
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateExportUndo_spec_12_8(t *testing.T) {
	refusal := sessionstore.ReservationEndedRefusal("user_alice")
	cases := []struct {
		name         string
		wire         bool
		exportErr    error
		insertErr    error
		deleteErr    error
		cancel       bool
		wantDeleted  bool
		wantReserved int
	}{
		{name: "export failure", wire: true, exportErr: errors.New("scan rejected"), wantDeleted: true},
		{name: "export failure, cancelled context", wire: true, exportErr: errors.New("scan rejected"), cancel: true, wantDeleted: true},
		{name: "insert refusal", wire: true, insertErr: refusal, wantDeleted: true},
		{name: "non-refusal insert error", wire: true, insertErr: errors.New("commit outcome unknown"), wantReserved: 1},
		{name: "deletion error", wire: true, insertErr: refusal, deleteErr: errors.New("object store down"), wantDeleted: true, wantReserved: 1},
		{name: "unwired deleter", wire: false, insertErr: refusal, wantReserved: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, svc := newDelegationFixture(t, tc.wire)
			f.mat.err = tc.exportErr
			f.store.createReservedErr = tc.insertErr
			f.objects.err = tc.deleteErr
			ctx := context.Background()
			if tc.cancel {
				// The export observes the cancellation; the undo must run
				// on a context detached from it.
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			}
			_, err := delegateExport(ctx, svc)
			if err == nil {
				t.Fatal("Delegate succeeded, want the injected failure")
			}
			if tc.insertErr == refusal {
				var ref *sessionstore.CreationRefusal
				if !errors.As(err, &ref) || ref.Code != "ERASURE_IN_PROGRESS" {
					t.Errorf("err = %v, want the refusal unwrapped", err)
				}
			}
			if got := len(f.objects.deleted) > 0; got != tc.wantDeleted {
				t.Errorf("objects deleted = %v, want %v", f.objects.deleted, tc.wantDeleted)
			}
			for _, e := range f.objects.ctxErr {
				if e != nil {
					t.Errorf("the undo ran on a cancelled context: %v", e)
				}
			}
			if ids := f.reserved(t); len(ids) != tc.wantReserved {
				t.Errorf("reservations = %v, want %d", ids, tc.wantReserved)
			}
		})
	}
}

// TestDelegateSoftDisabledOwnerKeepsDelegating_spec_11_4 pins the
// delegated-child classification at the store fence: a soft-disabled
// owner's in-flight work keeps delegating, while a restricted owner is
// refused with the fence's code and no child row.
// spec: 11.4 (User Invalidation), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDelegateSoftDisabledOwnerKeepsDelegating_spec_11_4(t *testing.T) {
	f, svc := newDelegationFixture(t, true)
	if _, err := f.users.Update(context.Background(), "acme", "user_alice", func(u *userstore.User) error {
		u.Disabled = true
		return nil
	}); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := delegateExport(context.Background(), svc); err != nil {
		t.Fatalf("a soft-disabled owner's delegation was refused: %v", err)
	}

	g, svc2 := newDelegationFixture(t, true)
	if _, err := g.users.Update(context.Background(), "acme", "user_alice", func(u *userstore.User) error {
		u.ProcessingRestricted, u.ErasureJobID = true, "job-1"
		return nil
	}); err != nil {
		t.Fatalf("restrict: %v", err)
	}
	_, err := delegateExport(context.Background(), svc2)
	var ref *sessionstore.CreationRefusal
	if !errors.As(err, &ref) || ref.Code != "ERASURE_IN_PROGRESS" {
		t.Fatalf("err = %v, want ERASURE_IN_PROGRESS", err)
	}
	if _, gerr := g.mem.Get(context.Background(), "acme", "sess_child"); !errors.Is(gerr, sessionstore.ErrNotFound) {
		t.Errorf("a refused delegation committed a child row")
	}
}
