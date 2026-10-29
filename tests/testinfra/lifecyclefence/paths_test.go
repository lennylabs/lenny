// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// suspendDirectly sets the tenant's suspension marker without the admin
// router, so a reserved path's insert is refused by the fence.
func suspendDirectly(t *testing.T, w *World) func() {
	return func() {
		if _, err := w.S.Tenants.Update(context.Background(), w.Tenant, func(tn *tenantstore.Tenant) error {
			tn.Suspended = true
			return nil
		}); err != nil {
			t.Errorf("suspend tenant: %v", err)
		}
	}
}

// reservedFor reports whether id is reserved for user.
func reservedFor(t *testing.T, w *World, user, id string) bool {
	t.Helper()
	ids, err := w.S.Sessions.ReservedSessionIDs(context.Background(), w.Tenant, user)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range ids {
		if got == id {
			return true
		}
	}
	return false
}

// TestRefusedReservedPathUndoKeepsReservationWhenDeletionFails_spec_12_8
// refuses the insert of a production derive and a production delegation
// after their pre-insert writes, with and without a failing object deletion
// in the undo. A successful undo deletes the writes and releases the
// reservation; an undo whose deletion fails keeps the reservation, so the
// writes stay under an ID the owner's erasure lists.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestRefusedReservedPathUndoKeepsReservationWhenDeletionFails_spec_12_8(t *testing.T) {
	cases := []struct {
		name         string
		deleteFaults int
		wantKept     bool
	}{
		{"undo deletes everything and releases", 0, false},
		{"undo deletion fails and keeps the reservation", 1, true},
	}
	for _, path := range []Path{PathDerive, PathDelegate} {
		for _, tc := range cases {
			t.Run(string(path)+"/"+tc.name, func(t *testing.T) {
				w := NewWorld(t, MemoryStores(), 0)
				w.Objects.FailDeletes(tc.deleteFaults)
				w.Faults.BeforeReservedInsert(suspendDirectly(t, w))
				o := w.Create(context.Background(), path, "alice")
				if !o.Refused() || !o.Reserved {
					t.Fatalf("%s outcome = %+v, want a refusal after the reservation", path, o)
				}
				if o.ReservationKept != tc.wantKept {
					t.Errorf("ReservationKept = %v, want %v", o.ReservationKept, tc.wantKept)
				}
				if got := reservedFor(t, w, "alice", o.ID); got != tc.wantKept {
					t.Errorf("reservation present = %v, want %v", got, tc.wantKept)
				}
				if got := w.Objects.Count(w.Tenant, o.ID) > 0; got != tc.wantKept {
					t.Errorf("written objects present = %v, want %v", got, tc.wantKept)
				}
				w.Check(t)
			})
		}
	}
}

// TestReservedPathsRunTheProductionCreatePaths_spec_12_8 pins that the derive
// and delegation paths run the production gateway code rather than a
// reproduction of its store calls: an admitted derive's row is a new root
// with no delegation parent, records its source's snapshot as its parent
// workspace, and carries the derived checkpoint's snapshot reference, and the
// handler copied the source's chunk and wrote the derived manifest row under
// the derived ID; an admitted delegation's row is a depth-1 child of its
// parent with the export written under the child ID.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 15.1 (REST API), 12.6 (StoreRouter), 4.5 (Workspace lineage)
func TestReservedPathsRunTheProductionCreatePaths_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 0)

	d := w.Create(ctx, PathDerive, "alice")
	if d.Err != nil {
		t.Fatalf("derive: %v", d.Err)
	}
	row, err := w.S.Sessions.Get(ctx, w.Tenant, d.ID)
	if err != nil {
		t.Fatalf("read derived row: %v", err)
	}
	if row.ParentSessionID != "" || row.RootSessionID != d.ID {
		t.Errorf("derived row parent = %q root = %q, want no parent and its own id %q as root",
			row.ParentSessionID, row.RootSessionID, d.ID)
	}
	if src, err := w.S.Sessions.Get(ctx, w.Tenant, w.sources["alice"]); err != nil {
		t.Fatalf("read source row: %v", err)
	} else if src.WorkspaceSnapshot == nil || row.ParentWorkspaceRef != src.WorkspaceSnapshot.Ref {
		t.Errorf("derived row parent workspace = %q, want the source snapshot %+v", row.ParentWorkspaceRef, src.WorkspaceSnapshot)
	}
	if row.WorkspaceSnapshot == nil || !strings.Contains(row.WorkspaceSnapshot.Ref, d.ID) {
		t.Errorf("derived row snapshot = %+v, want a reference under the derived ID", row.WorkspaceSnapshot)
	}
	if w.Objects.Count(w.Tenant, d.ID) != 1 || w.Manifests.Count(w.Tenant, d.ID) != 1 {
		t.Errorf("derive wrote %d objects and %d manifest rows, want one of each",
			w.Objects.Count(w.Tenant, d.ID), w.Manifests.Count(w.Tenant, d.ID))
	}

	c := w.Create(ctx, PathDelegate, "bob")
	if c.Err != nil {
		t.Fatalf("delegate: %v", c.Err)
	}
	child, err := w.S.Sessions.Get(ctx, w.Tenant, c.ID)
	if err != nil {
		t.Fatalf("read child row: %v", err)
	}
	if child.DelegationDepth != 1 || child.ParentSessionID == "" || child.UserID != "bob" {
		t.Errorf("child row = depth %d parent %q owner %q, want a depth-1 child of bob's parent",
			child.DelegationDepth, child.ParentSessionID, child.UserID)
	}
	if w.Objects.Count(w.Tenant, c.ID) != 1 {
		t.Errorf("delegation wrote %d export objects under the child ID, want 1", w.Objects.Count(w.Tenant, c.ID))
	}
	if reservedFor(t, w, "alice", d.ID) || reservedFor(t, w, "bob", c.ID) {
		t.Error("a reservation outlived its committed insert")
	}
	w.Check(t)
}

// TestReleaseBeforeDeletionIsAViolation_spec_12_8 pins the release-order
// invariant the World asserts on every production undo: a reservation
// released while an object or a manifest row remains under its ID is
// recorded as a violation, and a release after both are gone is not.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestReleaseBeforeDeletionIsAViolation_spec_12_8(t *testing.T) {
	for _, remaining := range []bool{true, false} {
		t.Run(fmt.Sprintf("objects remain %v", remaining), func(t *testing.T) {
			ctx := context.Background()
			w := NewWorld(t, MemoryStores(), 0)
			o := CreateOutcome{ID: uuid.NewString(), Path: PathDerive, Owner: "alice"}
			p := w.newPathStore(&o, false)
			if err := p.Reserve(ctx, w.Tenant, "alice", o.ID, false); err != nil {
				t.Fatalf("reserve: %v", err)
			}
			if remaining {
				u := blobstore.URI{TenantID: w.Tenant, ObjectType: blobstore.ObjectTypeWorkspace, SessionID: o.ID, PartID: "copy", TTL: time.Hour}
				if _, err := w.Objects.Put(u, "application/x-tar", strings.NewReader("x")); err != nil {
					t.Fatalf("write object: %v", err)
				}
			}
			if err := p.ReleaseReservation(ctx, w.Tenant, o.ID); err != nil {
				t.Fatalf("release: %v", err)
			}
			w.mu.Lock()
			got := len(w.violations)
			w.mu.Unlock()
			if (got > 0) != remaining {
				t.Errorf("violations = %d, want a violation %v", got, remaining)
			}
		})
	}
}

// TestKeptReservationIsReachedByOwnerErasure_spec_12_8 refuses a derive
// because its owner's erasure starts between the copy and the insert, with
// an undo whose object deletion fails. The kept reservation lists the ID to
// the erasure, which deletes the copy and the reservation.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.8 (Processing restriction during erasure (GDPR Article 18))
func TestKeptReservationIsReachedByOwnerErasure_spec_12_8(t *testing.T) {
	ctx := context.Background()
	w := NewWorld(t, MemoryStores(), 1)
	e := w.NewErasure()
	var jobID string
	w.Objects.FailDeletes(1)
	w.Faults.BeforeReservedInsert(func() {
		j, err := e.Start(ctx, "alice")
		if err != nil {
			t.Errorf("start erasure: %v", err)
		}
		jobID = j.ID
	})
	o := w.Create(ctx, PathDerive, "alice")
	if !o.Refused() || !o.ReservationKept {
		t.Fatalf("derive outcome = %+v, want a refusal whose undo kept the reservation", o)
	}
	if _, err := e.RunToCompletion(ctx, jobID, 4); err != nil {
		t.Fatal(err)
	}
	if n := w.Objects.Count(w.Tenant, o.ID); n != 0 {
		t.Errorf("%d copied objects outlived the owner's erasure", n)
	}
	w.Check(t)
}

// TestDeriveFailureRowAdoptsKeptReservation_spec_12_8 fails a derive's copy
// with an undo whose deletion fails. The derive-failure row's insert deletes
// the kept reservation whose ID and owner match, and the row keeps the
// copied objects reachable through its own ID.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeriveFailureRowAdoptsKeptReservation_spec_12_8(t *testing.T) {
	w := NewWorld(t, MemoryStores(), 0)
	w.Objects.FailDeletes(1)
	o := w.Create(context.Background(), PathDeriveFailure, "alice")
	if o.Err != nil || !o.ReservationKept {
		t.Fatalf("derive-failure outcome = %+v, want an admitted row after a kept reservation", o)
	}
	if reservedFor(t, w, "alice", o.ID) {
		t.Error("the derive-failure row's insert left the matching reservation in place")
	}
	if w.Objects.Count(w.Tenant, o.ID) == 0 {
		t.Error("the failed undo deleted the copied objects")
	}
	w.Check(t)
}

// TestDeriveFailureRowFencedBySourceIsSourceGone_spec_7_1 removes or ends a
// failing derive's source between the copy failure and the derive-failure
// row's generation-fenced insert. The insert persists no row, and the
// World records the derive as source-gone rather than as an unexpected
// error, whichever way the source left. Deleting the source row by hand
// stands in for the owner's erasure without its receipt, so that case
// asserts the World recorded no violation instead of running the whole
// checker, whose bystander check expects the seeded source to stand.
//
// spec: 7.1 (Derive session semantics), 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestDeriveFailureRowFencedBySourceIsSourceGone_spec_7_1(t *testing.T) {
	type leaveFunc func(ctx context.Context, w *World, src string) error
	cases := map[string]struct {
		leave     leaveFunc
		fullCheck bool
	}{
		"source row deleted": {leave: func(ctx context.Context, w *World, src string) error {
			return w.S.Sessions.Delete(ctx, w.Tenant, src)
		}},
		"source generation advanced": {fullCheck: true, leave: func(ctx context.Context, w *World, src string) error {
			_, err := w.S.Sessions.Update(ctx, w.Tenant, src, func(s *sessionstore.Session) error {
				s.CoordinationGeneration++
				return nil
			})
			return err
		}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			w := NewWorld(t, MemoryStores(), 0)
			src := w.sources["alice"]
			w.Faults.BeforeDeriveFailureRow(func() {
				if err := tc.leave(ctx, w, src); err != nil {
					t.Error(err)
				}
			})
			o := w.Create(ctx, PathDeriveFailure, "alice")
			if o.Path != PathDeriveFailure || !errors.Is(o.Err, ErrSourceGone) {
				t.Fatalf("derive-failure outcome = %+v, want a source-gone derive failure", o)
			}
			if _, err := w.S.Sessions.Get(ctx, w.Tenant, o.ID); !errors.Is(err, sessionstore.ErrNotFound) {
				t.Errorf("derive-failure row read = %v, want no row", err)
			}
			if tc.fullCheck {
				w.Check(t)
				return
			}
			w.mu.Lock()
			defer w.mu.Unlock()
			for _, v := range w.violations {
				t.Error(v)
			}
		})
	}
}

// TestFailureRowOutcomeKeepsOtherErrors_spec_12_8 pins that only the
// generation fence's source-gone answers become ErrSourceGone: a fence
// refusal and any other store error keep their own identity.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestFailureRowOutcomeKeepsOtherErrors_spec_12_8(t *testing.T) {
	refusal := &sessionstore.CreationRefusal{}
	if err := failureRowOutcome(nil); err != nil {
		t.Errorf("failureRowOutcome(nil) = %v, want nil", err)
	}
	if err := failureRowOutcome(refusal); errors.Is(err, ErrSourceGone) || !errors.As(err, &refusal) {
		t.Errorf("failureRowOutcome(refusal) = %v, want the refusal unchanged", err)
	}
	if err := failureRowOutcome(ErrInjected); errors.Is(err, ErrSourceGone) || !errors.Is(err, ErrInjected) {
		t.Errorf("failureRowOutcome(injected) = %v, want the injected error unchanged", err)
	}
}
