// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

const (
	fenceTenant = "acme"
	fenceOwner  = "alice"
)

// fenceFixture is a memstore fenced over in-memory tenant and user stores,
// wired as cmd/lenny-gateway wires the memory posture.
type fenceFixture struct {
	tenants  *tenantstore.Memory
	users    *userstore.Memory
	sessions *memstore.Store
}

func newFenceFixture(t *testing.T, opts ...memstore.Option) *fenceFixture {
	t.Helper()
	ctx := context.Background()
	f := &fenceFixture{tenants: tenantstore.NewMemory(), users: userstore.NewMemory()}
	if err := f.tenants.Create(ctx, tenantstore.Tenant{ID: fenceTenant}); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
	for _, sub := range []string{fenceOwner, "bob"} {
		if err := f.users.Create(ctx, userstore.User{TenantID: fenceTenant, Subject: sub}); err != nil {
			t.Fatalf("seed user %s: %v", sub, err)
		}
	}
	all := append([]memstore.Option{memstore.WithCreationMarkers(sessionstore.StoreMarkers(f.tenants, f.users))}, opts...)
	f.sessions = memstore.New(all...)
	return f
}

// markerCase sets one lifecycle marker and names the code the fence answers.
type markerCase struct {
	name string
	set  func(t *testing.T, f *fenceFixture)
	code string
}

func setUser(mut func(*userstore.User)) func(*testing.T, *fenceFixture) {
	return func(t *testing.T, f *fenceFixture) {
		t.Helper()
		if _, err := f.users.Update(context.Background(), fenceTenant, fenceOwner, func(u *userstore.User) error {
			mut(u)
			return nil
		}); err != nil {
			t.Fatalf("set user marker: %v", err)
		}
	}
}

func setTenant(mut func(*tenantstore.Tenant)) func(*testing.T, *fenceFixture) {
	return func(t *testing.T, f *fenceFixture) {
		t.Helper()
		if _, err := f.tenants.Update(context.Background(), fenceTenant, func(tn *tenantstore.Tenant) error {
			mut(tn)
			return nil
		}); err != nil {
			t.Fatalf("set tenant marker: %v", err)
		}
	}
}

func markerCases() []markerCase {
	return []markerCase{
		{"soft-disabled owner", setUser(func(u *userstore.User) { u.Disabled = true }), sessionstore.RefusalUserInvalidated},
		{"hard-disabled owner", setUser(func(u *userstore.User) { u.DeletedAt = time.Now() }), sessionstore.RefusalUserInvalidated},
		{"restricted owner", setUser(func(u *userstore.User) { u.ProcessingRestricted, u.ErasureJobID = true, "job-1" }), sessionstore.RefusalErasureInProgress},
		{"suspended tenant", setTenant(func(tn *tenantstore.Tenant) { tn.Suspended = true }), sessionstore.RefusalTenantSuspended},
		{"disabling tenant", setTenant(func(tn *tenantstore.Tenant) { tn.State = tenantstore.TenantStateDisabling }), sessionstore.RefusalTenantNotActive},
		{"deleting tenant", setTenant(func(tn *tenantstore.Tenant) { tn.State = tenantstore.TenantStateDeleting }), sessionstore.RefusalTenantNotActive},
	}
}

func liveRow(owner string) sessionstore.Session {
	return sessionstore.Session{
		ID: uuid.NewString(), TenantID: fenceTenant, UserID: owner,
		RuntimeRef: "echo", State: session.StateCreated,
	}
}

// requireRefusal fails the test unless err is a *CreationRefusal with code.
func requireRefusal(t *testing.T, err error, code string) {
	t.Helper()
	_ = refusalOf(t, err, code)
}

// refusalOf is requireRefusal returning the refusal, for cases that check its details.
func refusalOf(t *testing.T, err error, code string) *sessionstore.CreationRefusal {
	t.Helper()
	var ref *sessionstore.CreationRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("err = %v, want *CreationRefusal %s", err, code)
	}
	if ref.Code != code {
		t.Fatalf("refusal code = %s, want %s", ref.Code, code)
	}
	return ref
}

func requireNoRow(t *testing.T, s *memstore.Store, id string) {
	t.Helper()
	if _, err := s.Get(context.Background(), fenceTenant, id); !errors.Is(err, sessionstore.ErrNotFound) {
		t.Fatalf("Get(%s) = %v, want ErrNotFound (nothing inserted)", id, err)
	}
}

func reserved(t *testing.T, s *memstore.Store, owner string) []string {
	t.Helper()
	ids, err := s.ReservedSessionIDs(context.Background(), fenceTenant, owner)
	if err != nil {
		t.Fatalf("ReservedSessionIDs: %v", err)
	}
	return ids
}

// TestMemstoreCreateFenceRefusesEachMarker_spec_12_8 asserts that Create
// with WithCreationMarkers refuses with *CreationRefusal for each marker
// rule, inserts nothing on refusal, and admits a row with no owner under
// user markers.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
func TestMemstoreCreateFenceRefusesEachMarker_spec_12_8(t *testing.T) {
	ctx := context.Background()
	for _, mc := range markerCases() {
		t.Run(mc.name, func(t *testing.T) {
			f := newFenceFixture(t)
			mc.set(t, f)
			row := liveRow(fenceOwner)
			ref := refusalOf(t, f.sessions.Create(ctx, row), mc.code)
			if mc.code == sessionstore.RefusalErasureInProgress {
				want := map[string]any{"userId": fenceOwner, "jobId": "job-1"}
				if !reflect.DeepEqual(ref.Details, want) {
					t.Fatalf("details = %v, want %v", ref.Details, want)
				}
			}
			requireNoRow(t, f.sessions, row.ID)
		})
	}
	t.Run("row with no owner is admitted under user markers", func(t *testing.T) {
		f := newFenceFixture(t)
		setUser(func(u *userstore.User) { u.ProcessingRestricted, u.Disabled = true, true })(t, f)
		if err := f.sessions.Create(ctx, liveRow("")); err != nil {
			t.Fatalf("Create ownerless: %v", err)
		}
	})
	t.Run("owner with no registry row is admitted", func(t *testing.T) {
		f := newFenceFixture(t)
		if err := f.sessions.Create(ctx, liveRow("carol")); err != nil {
			t.Fatalf("Create unregistered owner: %v", err)
		}
	})
	t.Run("derive-failure row created terminal is refused", func(t *testing.T) {
		f := newFenceFixture(t)
		setUser(func(u *userstore.User) { u.ProcessingRestricted = true })(t, f)
		row := liveRow(fenceOwner)
		row.State, row.FailureClass = session.StateFailed, session.FailureClassDeriveFailure
		requireRefusal(t, f.sessions.Create(ctx, row), sessionstore.RefusalErasureInProgress)
		requireNoRow(t, f.sessions, row.ID)
	})
}

// TestMemstoreCreateFenceClassifiesByDelegationDepth_spec_12_8 asserts that
// a soft-disabled owner's row with ParentSessionID set and DelegationDepth 0
// (a derived or replayed session) is refused, and a row with
// DelegationDepth 1 (a delegated child) is admitted.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 11.4 (User Invalidation)
func TestMemstoreCreateFenceClassifiesByDelegationDepth_spec_12_8(t *testing.T) {
	ctx := context.Background()
	f := newFenceFixture(t)
	parent := liveRow(fenceOwner)
	if err := f.sessions.Create(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}
	setUser(func(u *userstore.User) { u.Disabled = true })(t, f)

	derived := liveRow(fenceOwner)
	derived.ParentSessionID = parent.ID
	requireRefusal(t, f.sessions.Create(ctx, derived), sessionstore.RefusalUserInvalidated)
	requireNoRow(t, f.sessions, derived.ID)

	child := liveRow(fenceOwner)
	child.ParentSessionID, child.DelegationDepth = parent.ID, 1
	if err := f.sessions.Create(ctx, child); err != nil {
		t.Fatalf("delegated child of a soft-disabled owner: %v, want admit", err)
	}
}

// TestMemstoreCreateFenceFailsClosedOnReadError_spec_12_8 asserts that a
// marker-reader error is returned and nothing is inserted or reserved.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestMemstoreCreateFenceFailsClosedOnReadError_spec_12_8(t *testing.T) {
	ctx := context.Background()
	boom := errors.New("marker read failed")
	s := memstore.New(memstore.WithCreationMarkers(func(context.Context, string, string) (sessionstore.CreationMarkers, error) {
		return sessionstore.CreationMarkers{}, boom
	}))
	row := liveRow(fenceOwner)
	if err := s.Create(ctx, row); !errors.Is(err, boom) {
		t.Fatalf("Create = %v, want the reader error", err)
	}
	requireNoRow(t, s, row.ID)
	if err := s.Reserve(ctx, fenceTenant, fenceOwner, row.ID, false); !errors.Is(err, boom) {
		t.Fatalf("Reserve = %v, want the reader error", err)
	}
	if ids := reserved(t, s, fenceOwner); len(ids) != 0 {
		t.Fatalf("reservations after a failed read = %v, want none", ids)
	}
}

// TestMemstoreUnfencedStoreAdmits_spec_12_8 asserts that a store built
// without WithCreationMarkers admits a root insert, which the fence's
// TenantFound=false case relies on in the in-memory posture.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestMemstoreUnfencedStoreAdmits_spec_12_8(t *testing.T) {
	if err := memstore.New().Create(context.Background(), liveRow(fenceOwner)); err != nil {
		t.Fatalf("unfenced Create: %v", err)
	}
}

// TestMemstoreReservations_spec_12_8 runs the session-reservation cases
// against the in-memory store: CreateReserved after the owner's erasure,
// Reserve under each marker, the soft-disabled delegated-child admission,
// the reservation deletes of Create, DeleteByUser, and DeleteByTenant, the
// empty-owner case, and the serialization of Reserve with a marker write.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestMemstoreReservations_spec_12_8(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateReserved after DeleteByUser refuses and inserts nothing", func(t *testing.T) {
		f := newFenceFixture(t)
		row := liveRow(fenceOwner)
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if _, err := f.sessions.DeleteByUser(ctx, fenceTenant, fenceOwner); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		ref := refusalOf(t, f.sessions.CreateReserved(ctx, row), sessionstore.RefusalErasureInProgress)
		if !reflect.DeepEqual(ref.Details, map[string]any{"userId": fenceOwner}) {
			t.Fatalf("details = %v, want userId only", ref.Details)
		}
		requireNoRow(t, f.sessions, row.ID)
	})

	t.Run("CreateReserved inserts and removes the reservation", func(t *testing.T) {
		f := newFenceFixture(t)
		row := liveRow(fenceOwner)
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if ids := reserved(t, f.sessions, fenceOwner); !reflect.DeepEqual(ids, []string{row.ID}) {
			t.Fatalf("reserved = %v, want [%s]", ids, row.ID)
		}
		if err := f.sessions.CreateReserved(ctx, row); err != nil {
			t.Fatalf("CreateReserved: %v", err)
		}
		if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 0 {
			t.Fatalf("reserved after insert = %v, want none", ids)
		}
	})

	t.Run("CreateReserved refuses under a marker set after Reserve", func(t *testing.T) {
		f := newFenceFixture(t)
		row := liveRow(fenceOwner)
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		setTenant(func(tn *tenantstore.Tenant) { tn.Suspended = true })(t, f)
		requireRefusal(t, f.sessions.CreateReserved(ctx, row), sessionstore.RefusalTenantSuspended)
		requireNoRow(t, f.sessions, row.ID)
		if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 1 {
			t.Fatalf("a refused insert removed the reservation: %v", ids)
		}
	})

	for _, mc := range markerCases() {
		t.Run("Reserve refuses: "+mc.name, func(t *testing.T) {
			f := newFenceFixture(t)
			mc.set(t, f)
			requireRefusal(t, f.sessions.Reserve(ctx, fenceTenant, fenceOwner, uuid.NewString(), false), mc.code)
			if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 0 {
				t.Fatalf("refused Reserve recorded %v", ids)
			}
		})
	}

	t.Run("Reserve admits a soft-disabled owner only for a delegated child", func(t *testing.T) {
		f := newFenceFixture(t)
		setUser(func(u *userstore.User) { u.Disabled = true })(t, f)
		requireRefusal(t, f.sessions.Reserve(ctx, fenceTenant, fenceOwner, uuid.NewString(), false), sessionstore.RefusalUserInvalidated)
		id := uuid.NewString()
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, id, true); err != nil {
			t.Fatalf("Reserve delegated child: %v", err)
		}
		if ids := reserved(t, f.sessions, fenceOwner); !reflect.DeepEqual(ids, []string{id}) {
			t.Fatalf("reserved = %v, want [%s]", ids, id)
		}
	})

	t.Run("Create deletes a matching reservation and keeps another owner's", func(t *testing.T) {
		f := newFenceFixture(t)
		mine, theirs := liveRow(fenceOwner), liveRow(fenceOwner)
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, mine.ID, false); err != nil {
			t.Fatalf("Reserve mine: %v", err)
		}
		if err := f.sessions.Reserve(ctx, fenceTenant, "bob", theirs.ID, false); err != nil {
			t.Fatalf("Reserve theirs: %v", err)
		}
		if err := f.sessions.Create(ctx, mine); err != nil {
			t.Fatalf("Create mine: %v", err)
		}
		if err := f.sessions.Create(ctx, theirs); err != nil {
			t.Fatalf("Create theirs: %v", err)
		}
		if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 0 {
			t.Fatalf("matching reservation kept: %v", ids)
		}
		if ids := reserved(t, f.sessions, "bob"); !reflect.DeepEqual(ids, []string{theirs.ID}) {
			t.Fatalf("other owner's reservation = %v, want kept", ids)
		}
	})

	t.Run("DeleteByUser and DeleteByTenant delete reservations", func(t *testing.T) {
		f := newFenceFixture(t)
		for _, owner := range []string{fenceOwner, "bob"} {
			if err := f.sessions.Reserve(ctx, fenceTenant, owner, uuid.NewString(), false); err != nil {
				t.Fatalf("Reserve %s: %v", owner, err)
			}
		}
		if _, err := f.sessions.DeleteByUser(ctx, fenceTenant, fenceOwner); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 0 {
			t.Fatalf("DeleteByUser kept %v", ids)
		}
		if ids := reserved(t, f.sessions, "bob"); len(ids) != 1 {
			t.Fatalf("DeleteByUser removed another user's reservation: %v", ids)
		}
		if _, err := f.sessions.DeleteByTenant(ctx, fenceTenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		if ids := reserved(t, f.sessions, "bob"); len(ids) != 0 {
			t.Fatalf("DeleteByTenant kept %v", ids)
		}
	})

	t.Run("empty owner records nothing and CreateReserved inserts the ownerless row", func(t *testing.T) {
		f := newFenceFixture(t)
		row := liveRow("")
		if err := f.sessions.Reserve(ctx, fenceTenant, "", row.ID, false); err != nil {
			t.Fatalf("Reserve ownerless: %v", err)
		}
		if ids := reserved(t, f.sessions, ""); len(ids) != 0 {
			t.Fatalf("empty owner recorded %v", ids)
		}
		if err := f.sessions.CreateReserved(ctx, row); err != nil {
			t.Fatalf("CreateReserved ownerless: %v", err)
		}
	})

	t.Run("ReleaseReservation is idempotent", func(t *testing.T) {
		f := newFenceFixture(t)
		id := uuid.NewString()
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, id, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		for i := 0; i < 2; i++ {
			if err := f.sessions.ReleaseReservation(ctx, fenceTenant, id); err != nil {
				t.Fatalf("ReleaseReservation #%d: %v", i, err)
			}
		}
		if ids := reserved(t, f.sessions, fenceOwner); len(ids) != 0 {
			t.Fatalf("released reservation kept: %v", ids)
		}
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, id, false); err != nil {
			t.Fatalf("re-Reserve: %v", err)
		}
		if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, id, false); !errors.Is(err, sessionstore.ErrAlreadyExists) {
			t.Fatalf("duplicate Reserve = %v, want ErrAlreadyExists", err)
		}
	})

	for _, w := range []struct {
		name  string
		write func(f *fenceFixture, entered chan<- struct{}, hold <-chan struct{}) error
		code  string
	}{
		{"user Update", func(f *fenceFixture, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := f.users.Update(ctx, fenceTenant, fenceOwner, func(u *userstore.User) error {
				close(entered)
				<-hold
				u.ProcessingRestricted = true
				return nil
			})
			return err
		}, sessionstore.RefusalErasureInProgress},
		{"tenant Update", func(f *fenceFixture, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := f.tenants.Update(ctx, fenceTenant, func(tn *tenantstore.Tenant) error {
				close(entered)
				<-hold
				tn.Suspended = true
				return nil
			})
			return err
		}, sessionstore.RefusalTenantSuspended},
	} {
		t.Run("Reserve serializes with a "+w.name, func(t *testing.T) {
			f := newFenceFixture(t)
			hold, entered := make(chan struct{}), make(chan struct{})
			writeDone := make(chan error, 1)
			go func() {
				writeDone <- w.write(f, entered, hold)
			}()
			<-entered
			reserveDone := make(chan error, 1)
			go func() { reserveDone <- f.sessions.Reserve(ctx, fenceTenant, fenceOwner, uuid.NewString(), false) }()
			select {
			case err := <-reserveDone:
				t.Fatalf("Reserve returned %v while the marker write held its lock", err)
			case <-time.After(50 * time.Millisecond):
			}
			close(hold)
			if err := <-writeDone; err != nil {
				t.Fatalf("marker write: %v", err)
			}
			requireRefusal(t, <-reserveDone, w.code)
		})
	}
}

// TestMemstoreSnapshotCarriesReservations_spec_12_8 asserts that a
// reservation recorded before ExportState is returned by ReservedSessionIDs
// after ImportState into a fresh store, beside the session rows.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 17.4 (Local Development Mode)
func TestMemstoreSnapshotCarriesReservations_spec_12_8(t *testing.T) {
	ctx := context.Background()
	f := newFenceFixture(t)
	id := uuid.NewString()
	if err := f.sessions.Reserve(ctx, fenceTenant, fenceOwner, id, false); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	row := liveRow(fenceOwner)
	if err := f.sessions.Create(ctx, row); err != nil {
		t.Fatalf("Create: %v", err)
	}
	snap, err := f.sessions.ExportState()
	if err != nil {
		t.Fatalf("ExportState: %v", err)
	}
	restored := memstore.New()
	if err := restored.ImportState(snap); err != nil {
		t.Fatalf("ImportState: %v", err)
	}
	if ids := reserved(t, restored, fenceOwner); !reflect.DeepEqual(ids, []string{id}) {
		t.Fatalf("restored reservations = %v, want [%s]", ids, id)
	}
	if _, err := restored.Get(ctx, fenceTenant, row.ID); err != nil {
		t.Fatalf("restored session: %v", err)
	}
	if err := restored.CreateReserved(ctx, liveRowWithID(id, fenceOwner)); err != nil {
		t.Fatalf("CreateReserved on the restored reservation: %v", err)
	}
	if err := restored.ImportState(nil); err != nil {
		t.Fatalf("ImportState(nil): %v", err)
	}
	if ids := reserved(t, restored, fenceOwner); len(ids) != 0 {
		t.Fatalf("empty snapshot kept reservations %v", ids)
	}
}

func liveRowWithID(id, owner string) sessionstore.Session {
	r := liveRow(owner)
	r.ID = id
	return r
}
