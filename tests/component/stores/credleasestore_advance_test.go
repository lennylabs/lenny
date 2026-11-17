//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"testing"
	"time"
)

// spec: §4.9 (Proactive Lease Renewal)
// diagnosis: a failure means the renewal's lease lookup cannot tell a
// released lease from an unreadable one, an extension recreated a lease a
// release removed, or an extension did not move the plain expires_at
// projection, so the expired-lease sweep deletes a lease the renewal kept
// alive.
func TestCredLeaseStoreLookupAndAdvance_spec_4_9(t *testing.T) {
	t.Parallel()
	store, pg := newCredLeaseStore(t)
	ctx := context.Background()

	t.Run("lookup of an absent lease is not found", func(t *testing.T) {
		if _, ok, err := store.Lookup("cl-absent-" + newUUID(t)); ok || err != nil {
			t.Errorf("Lookup(absent) = ok %v err %v, want not found and no error", ok, err)
		}
	})

	t.Run("lookup of an unreadable blob is an error", func(t *testing.T) {
		l := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		if err := store.Put(l); err != nil {
			t.Fatalf("Put: %v", err)
		}
		if _, err := pg.Pool.Exec(ctx,
			`UPDATE credential_leases SET lease = $2 WHERE lease_id = $1`, l.LeaseID, []byte("not a sealed lease")); err != nil {
			t.Fatalf("corrupt the lease blob: %v", err)
		}
		if _, ok, err := store.Lookup(l.LeaseID); err == nil {
			t.Errorf("Lookup(unreadable) = ok %v and no error, want an error", ok)
		}
	})

	t.Run("advance of a removed lease writes nothing", func(t *testing.T) {
		l := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		if err := store.Put(l); err != nil {
			t.Fatalf("Put: %v", err)
		}
		store.Remove(l.LeaseID)
		l.ExpiresAt = l.ExpiresAt.Add(time.Hour)
		ok, err := store.Advance(l)
		if err != nil || ok {
			t.Fatalf("Advance(removed) = %v, %v; want false and no error", ok, err)
		}
		if _, found, _ := store.Lookup(l.LeaseID); found {
			t.Error("Advance recreated a removed lease")
		}
	})

	t.Run("advance moves the expiry past the sweep cutoff", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Microsecond)
		l := credleaseExpiring("cl-"+newUUID(t), "lt-"+newUUID(t), now.Add(time.Minute))
		l.CoordinationGeneration = 7
		if err := store.Put(l); err != nil {
			t.Fatalf("Put: %v", err)
		}
		advanced := l
		advanced.ExpiresAt = now.Add(time.Hour)
		advanced.RenewBefore = now.Add(50 * time.Minute)
		ok, err := store.Advance(advanced)
		if err != nil || !ok {
			t.Fatalf("Advance = %v, %v; want true", ok, err)
		}
		// A cutoff between the two expiries keeps the advanced row.
		if _, err := store.DeleteExpired(ctx, now.Add(10*time.Minute)); err != nil {
			t.Fatalf("DeleteExpired: %v", err)
		}
		got, found, err := store.Lookup(l.LeaseID)
		if err != nil || !found {
			t.Fatalf("Lookup after the sweep = found %v err %v, want the advanced lease kept", found, err)
		}
		if !got.ExpiresAt.Equal(advanced.ExpiresAt) || !got.RenewBefore.Equal(advanced.RenewBefore) {
			t.Errorf("advanced lease = expires %v renew %v, want %v / %v",
				got.ExpiresAt, got.RenewBefore, advanced.ExpiresAt, advanced.RenewBefore)
		}
		if got.CoordinationGeneration != 7 {
			t.Errorf("CoordinationGeneration = %d, want 7 kept through the advance", got.CoordinationGeneration)
		}
	})
}

// spec: §7.3 (Credential leases on the replacement pod), §4.9 (Credential
// Leasing Service)
// diagnosis: a failure means the Postgres lease row drops the coordination
// generation a lease was minted at, so a generation-scoped release through
// ReleaseThrough cannot tell an earlier incarnation's lease from a later
// one's and returns the wrong set.
func TestCredLeaseStoreRoundTripsCoordinationGeneration_spec_7_3(t *testing.T) {
	t.Parallel()
	store, _ := newCredLeaseStore(t)
	for _, gen := range []int64{0, 1, 42} {
		l := credleaseProxy("cl-"+newUUID(t), "lt-"+newUUID(t))
		l.CoordinationGeneration = gen
		if err := store.Put(l); err != nil {
			t.Fatalf("Put: %v", err)
		}
		got, ok := store.GetByID(l.LeaseID)
		if !ok || got.CoordinationGeneration != gen {
			t.Errorf("GetByID = %+v ok %v, want generation %d", got, ok, gen)
		}
		bySession := store.LeasesBySession([]string{l.SessionID})
		if len(bySession) != 1 || bySession[0].CoordinationGeneration != gen {
			t.Errorf("LeasesBySession = %+v, want one lease at generation %d", bySession, gen)
		}
	}
}
