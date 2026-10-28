//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// spec: §6.2 (Pod State Machine)
// diagnosis: the Postgres session store did not own state_entered_at the
//
//	way §6.2 requires. Either an insert through Create or CreateIfGeneration
//	kept a caller-supplied value instead of the creation instant, a
//	same-state Update moved it, a state-changing Update did not set it to the
//	write's updated_at, or a mutator's own value was persisted, or the column
//	did not round-trip through Get, GetByID, and List. Every per-state
//	deadline the watchdog measures from it then fires early or never.
func TestSessionStoreStampsStateEntry_spec_6_2(t *testing.T) {
	t.Parallel()
	store, pg := startStore(t)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)
	forged := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Microsecond)

	t.Run("Create sets the creation instant", func(t *testing.T) {
		id := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateCreated, RuntimeRef: "echo",
			StateEnteredAt: forged,
		}); err != nil {
			t.Fatalf("Create: %v", err)
		}
		assertStateEntryEverywhere(t, ctx, store, tenant, id, func(r sessionstore.Session) time.Time { return r.CreatedAt })
	})

	t.Run("CreateIfGeneration sets the creation instant", func(t *testing.T) {
		src := newUUID(t)
		if err := store.Create(ctx, sessionstore.Session{
			ID: src, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
		}); err != nil {
			t.Fatalf("seed source: %v", err)
		}
		id := newUUID(t)
		created := time.Now().UTC().Add(-time.Minute).Truncate(time.Microsecond)
		if err := store.CreateIfGeneration(ctx, sessionstore.Session{
			ID: id, TenantID: tenant, State: session.StateCreated, RuntimeRef: "echo",
			CreatedAt: created, StateEnteredAt: forged,
		}, tenant, src, 1); err != nil {
			t.Fatalf("CreateIfGeneration: %v", err)
		}
		assertStateEntryEverywhere(t, ctx, store, tenant, id, func(sessionstore.Session) time.Time { return created })
	})

	cases := []struct {
		name    string
		next    session.State
		forge   bool
		changed bool
	}{
		{name: "same-state write", next: session.StateRunning},
		{name: "same-state write with a forged value", next: session.StateRunning, forge: true},
		{name: "state-changing write", next: session.StateSuspended, changed: true},
		{name: "state-changing write with a forged value", next: session.StateSuspended, forge: true, changed: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id := newUUID(t)
			if err := store.Create(ctx, sessionstore.Session{
				ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
			}); err != nil {
				t.Fatalf("Create: %v", err)
			}
			before, err := store.Get(ctx, tenant, id)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			next, forge := c.next, c.forge
			updated, err := store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
				r.State = next
				if forge {
					r.StateEnteredAt = forged
				}
				return nil
			})
			if err != nil {
				t.Fatalf("Update: %v", err)
			}
			want := before.StateEnteredAt
			if c.changed {
				want = updated.UpdatedAt
			}
			if !updated.StateEnteredAt.Equal(want) {
				t.Fatalf("returned StateEnteredAt = %v, want %v", updated.StateEnteredAt, want)
			}
			assertStateEntryEverywhere(t, ctx, store, tenant, id, func(sessionstore.Session) time.Time { return want })
		})
	}
}

// assertStateEntryEverywhere reads one row through Get, GetByID, and List
// and checks that each read carries the StateEnteredAt want derives from
// the row Get returned.
func assertStateEntryEverywhere(t *testing.T, ctx context.Context, store *pgstore.Store, tenant, id string, want func(sessionstore.Session) time.Time) {
	t.Helper()
	got, err := store.Get(ctx, tenant, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	expect := want(got)
	if !got.StateEnteredAt.Equal(expect) {
		t.Fatalf("Get StateEnteredAt = %v, want %v", got.StateEnteredAt, expect)
	}
	byID, err := store.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if !byID.StateEnteredAt.Equal(expect) {
		t.Fatalf("GetByID StateEnteredAt = %v, want %v", byID.StateEnteredAt, expect)
	}
	listed, err := store.List(ctx, tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, r := range listed {
		if r.ID == id {
			if !r.StateEnteredAt.Equal(expect) {
				t.Fatalf("List StateEnteredAt = %v, want %v", r.StateEnteredAt, expect)
			}
			return
		}
	}
	t.Fatalf("List did not return %s", id)
}
