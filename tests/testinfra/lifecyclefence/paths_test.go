// SPDX-License-Identifier: MIT

package lifecyclefence

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
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

// TestRefusedDeriveUndoKeepsReservationWhenDeletionFails_spec_12_8 refuses a
// derive's insert after its snapshot copy, with and without a failing object
// deletion in the undo. A successful undo deletes the copy and releases the
// reservation; an undo whose deletion fails keeps the reservation, so the
// copy stays under an ID the owner's erasure lists.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
func TestRefusedDeriveUndoKeepsReservationWhenDeletionFails_spec_12_8(t *testing.T) {
	cases := []struct {
		name         string
		deleteFaults int
		wantKept     bool
	}{
		{"undo deletes everything and releases", 0, false},
		{"undo deletion fails and keeps the reservation", 1, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorld(t, MemoryStores(), 0)
			w.Objects.FailDeletes(tc.deleteFaults)
			w.Faults.BeforeReservedInsert(suspendDirectly(t, w))
			o := w.Create(context.Background(), PathDerive, "alice")
			if !o.Refused() || !o.Reserved {
				t.Fatalf("derive outcome = %+v, want a refusal after the reservation", o)
			}
			if o.ReservationKept != tc.wantKept {
				t.Errorf("ReservationKept = %v, want %v", o.ReservationKept, tc.wantKept)
			}
			if got := reservedFor(t, w, "alice", o.ID); got != tc.wantKept {
				t.Errorf("reservation present = %v, want %v", got, tc.wantKept)
			}
			if got := w.Objects.Count(w.Tenant, o.ID) > 0; got != tc.wantKept {
				t.Errorf("copied objects present = %v, want %v", got, tc.wantKept)
			}
			w.Check(t)
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
