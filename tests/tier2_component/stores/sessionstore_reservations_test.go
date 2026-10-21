//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/environment/userstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// TestSessionStoreReservations_spec_12_8 pins the session-ID reservation of
// the creation fence against real Postgres (migration 0187): CreateReserved
// after the owner's erasure, Reserve under each marker and its
// delegated-child admission, Reserve's serialization with the marker
// writers, the reservation deletes of Create, DeleteByUser, and
// DeleteByTenant, the empty-owner case, and a CreateReserved that waits on
// the reservation's row lock held by the owner's erasure.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain)
// diagnosis: a failure means a write keyed to a reserved session ID can
// outlive its owner's erasure: either Reserve records an ID the fence should
// refuse, the erasure does not end the user's reservations, or
// CreateReserved inserts a row whose reservation the erasure already ended.
func TestSessionStoreReservations_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)

	t.Run("CreateReserved after DeleteByUser refuses and inserts nothing", func(t *testing.T) {
		e.addTenant(t)
		row := e.row("alice")
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if _, err := e.sessions.DeleteByUser(ctx, e.tenant, "alice"); err != nil {
			t.Fatalf("DeleteByUser: %v", err)
		}
		ref := pgRefusalOf(t, e.sessions.CreateReserved(ctx, row), sessionstore.RefusalErasureInProgress)
		if !reflect.DeepEqual(ref.Details, map[string]any{"userId": "alice"}) {
			t.Fatalf("details = %v, want userId only", ref.Details)
		}
		if n := e.rowCount(t, row.ID); n != 0 {
			t.Fatalf("refused CreateReserved left %d rows", n)
		}
	})

	t.Run("CreateReserved inserts and removes the reservation", func(t *testing.T) {
		e.addTenant(t)
		row := e.row("alice")
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		if n := e.reservationCount(t, "alice"); n != 1 {
			t.Fatalf("reservations = %d, want 1", n)
		}
		if err := e.sessions.CreateReserved(ctx, row); err != nil {
			t.Fatalf("CreateReserved: %v", err)
		}
		if n := e.rowCount(t, row.ID); n != 1 {
			t.Fatalf("rows = %d, want 1", n)
		}
		if n := e.reservationCount(t, "alice"); n != 0 {
			t.Fatalf("reservations after insert = %d, want 0", n)
		}
	})

	for _, mc := range pgMarkerCases() {
		t.Run("Reserve refuses and records nothing: "+mc.name, func(t *testing.T) {
			e.addTenant(t)
			mc.set(t, e)
			requirePGRefusal(t, e.sessions.Reserve(ctx, e.tenant, "alice", uuid.NewString(), false), mc.code)
			if n := e.reservationCount(t, "alice"); n != 0 {
				t.Fatalf("refused Reserve recorded %d rows", n)
			}
		})
	}

	t.Run("Reserve admits a soft-disabled owner only for a delegated child", func(t *testing.T) {
		e.addTenant(t)
		e.setUser(t, "alice", func(u *userstore.User) { u.Disabled = true })
		requirePGRefusal(t, e.sessions.Reserve(ctx, e.tenant, "alice", uuid.NewString(), false), sessionstore.RefusalUserInvalidated)
		id := uuid.NewString()
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", id, true); err != nil {
			t.Fatalf("Reserve delegated child: %v", err)
		}
		ids, err := e.sessions.ReservedSessionIDs(ctx, e.tenant, "alice")
		if err != nil || !reflect.DeepEqual(ids, []string{id}) {
			t.Fatalf("ReservedSessionIDs = %v, %v; want [%s]", ids, err, id)
		}
	})

	for _, w := range []struct {
		name  string
		write func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error
		code  string
	}{
		{"tenant Update", func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := e.tenants.Update(ctx, e.tenant, func(tn *tenantstore.Tenant) error {
				close(entered)
				<-hold
				tn.State = tenantstore.TenantStateDisabling
				return nil
			})
			return err
		}, sessionstore.RefusalTenantNotActive},
		{"user Update", func(e *fenceEnv, entered chan<- struct{}, hold <-chan struct{}) error {
			_, err := e.users.Update(ctx, e.tenant, "alice", func(u *userstore.User) error {
				close(entered)
				<-hold
				u.ProcessingRestricted = true
				return nil
			})
			return err
		}, sessionstore.RefusalErasureInProgress},
	} {
		t.Run("Reserve serializes with a "+w.name, func(t *testing.T) {
			e.addTenant(t)
			hold, entered := make(chan struct{}), make(chan struct{})
			writeDone := make(chan error, 1)
			go func() { writeDone <- w.write(e, entered, hold) }()
			<-entered
			reserveDone := make(chan error, 1)
			go func() { reserveDone <- e.sessions.Reserve(ctx, e.tenant, "alice", uuid.NewString(), false) }()
			awaitLockWaiter(t, ctx, e.pg, reserveDone)
			close(hold)
			if err := awaitUpdateResult(t, w.name, writeDone); err != nil {
				t.Fatalf("marker write: %v", err)
			}
			requirePGRefusal(t, awaitUpdateResult(t, "Reserve", reserveDone), w.code)
			if n := e.reservationCount(t, "alice"); n != 0 {
				t.Fatalf("refused Reserve recorded %d rows", n)
			}
		})
	}

	t.Run("Create deletes the matching reservation and keeps another owner's", func(t *testing.T) {
		e.addTenant(t)
		mine, theirs := e.row("alice"), e.row("alice")
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", mine.ID, false); err != nil {
			t.Fatalf("Reserve mine: %v", err)
		}
		if err := e.sessions.Reserve(ctx, e.tenant, "bob", theirs.ID, false); err != nil {
			t.Fatalf("Reserve theirs: %v", err)
		}
		for _, r := range []sessionstore.Session{mine, theirs} {
			if err := e.sessions.Create(ctx, r); err != nil {
				t.Fatalf("Create %s: %v", r.ID, err)
			}
		}
		if n := e.reservationCount(t, "alice"); n != 0 {
			t.Fatalf("matching reservation kept (%d)", n)
		}
		if n := e.reservationCount(t, "bob"); n != 1 {
			t.Fatalf("another owner's reservation = %d, want kept", n)
		}
	})

	t.Run("DeleteByUser and DeleteByTenant delete reservations with the rows", func(t *testing.T) {
		e.addTenant(t)
		for _, owner := range []string{"alice", "bob"} {
			if err := e.sessions.Create(ctx, e.row(owner)); err != nil {
				t.Fatalf("Create %s: %v", owner, err)
			}
			if err := e.sessions.Reserve(ctx, e.tenant, owner, uuid.NewString(), false); err != nil {
				t.Fatalf("Reserve %s: %v", owner, err)
			}
		}
		n, err := e.sessions.DeleteByUser(ctx, e.tenant, "alice")
		if err != nil || n != 1 {
			t.Fatalf("DeleteByUser = %d, %v; want 1 session row", n, err)
		}
		if c := e.reservationCount(t, "alice"); c != 0 {
			t.Fatalf("DeleteByUser kept %d reservations", c)
		}
		if c := e.reservationCount(t, "bob"); c != 1 {
			t.Fatalf("DeleteByUser touched another user's reservation (%d)", c)
		}
		if _, err := e.sessions.DeleteByTenant(ctx, e.tenant); err != nil {
			t.Fatalf("DeleteByTenant: %v", err)
		}
		if c := e.reservationCount(t, "bob"); c != 0 {
			t.Fatalf("DeleteByTenant kept %d reservations", c)
		}
	})

	t.Run("empty owner records nothing and CreateReserved inserts the ownerless row", func(t *testing.T) {
		e.addTenant(t)
		row := e.row("")
		if err := e.sessions.Reserve(ctx, e.tenant, "", row.ID, false); err != nil {
			t.Fatalf("Reserve ownerless: %v", err)
		}
		var n int
		if err := e.pg.Pool.QueryRow(ctx, `SELECT count(*) FROM session_reservations WHERE tenant_id = $1`, e.tenant).Scan(&n); err != nil || n != 0 {
			t.Fatalf("ownerless Reserve recorded %d rows (%v)", n, err)
		}
		if err := e.sessions.CreateReserved(ctx, row); err != nil {
			t.Fatalf("CreateReserved ownerless: %v", err)
		}
		if c := e.rowCount(t, row.ID); c != 1 {
			t.Fatalf("ownerless row count = %d, want 1", c)
		}
	})

	t.Run("ReleaseReservation is idempotent", func(t *testing.T) {
		e.addTenant(t)
		id := uuid.NewString()
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", id, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		for i := 0; i < 2; i++ {
			if err := e.sessions.ReleaseReservation(ctx, e.tenant, id); err != nil {
				t.Fatalf("ReleaseReservation #%d: %v", i, err)
			}
		}
		if c := e.reservationCount(t, "alice"); c != 0 {
			t.Fatalf("released reservation kept (%d)", c)
		}
	})

	t.Run("CreateReserved waiting on the erasure's reservation lock refuses", func(t *testing.T) {
		e.addTenant(t)
		row := e.row("alice")
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", row.ID, false); err != nil {
			t.Fatalf("Reserve: %v", err)
		}
		// The erasure's transaction, holding the reservation's row lock: the
		// statements DeleteByUser issues, left uncommitted.
		tx, err := e.pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		for _, q := range []string{
			`SELECT set_config('app.current_tenant', $1, true)`,
			`DELETE FROM sessions WHERE tenant_id = $1 AND user_id = 'alice'`,
			`DELETE FROM session_reservations WHERE tenant_id = $1 AND user_id = 'alice'`,
		} {
			if _, err := tx.Exec(ctx, q, e.tenant); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		done := make(chan error, 1)
		go func() { done <- e.sessions.CreateReserved(ctx, row) }()
		awaitLockWaiter(t, ctx, e.pg, done)
		if err := tx.Commit(ctx); err != nil {
			t.Fatalf("commit erasure: %v", err)
		}
		requirePGRefusal(t, awaitUpdateResult(t, "CreateReserved", done), sessionstore.RefusalErasureInProgress)
		if c := e.rowCount(t, row.ID); c != 0 {
			t.Fatalf("CreateReserved after the erasure left %d rows", c)
		}
	})
}

// TestSessionStoreReservedSessionIDsReadsPrimary_spec_12_8 builds the store
// over a primary and a second migrated database standing in for a read
// replica that has not replicated the reservation. ReservedSessionIDs
// returns the reservation, so the erasure's lister reaches data written
// under a reserved ID the replica has not seen.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 12.3 (Postgres HA Requirements: Read replicas)
// diagnosis: a failure means ReservedSessionIDs reads the read pool, so an
// erasure that lists right after a Reserve can miss the reserved ID and leave
// the derive copy or the delegated child's export behind.
func TestSessionStoreReservedSessionIDsReadsPrimary_spec_12_8(t *testing.T) {
	ctx := context.Background()
	primary := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	replica := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	store := pgstore.New(primary.Pool, pgstore.WithReadPool(replica.Pool))
	tenant := freshTenant(t, ctx, primary)
	ids := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range ids {
		if err := store.Reserve(ctx, tenant, "alice", id, false); err != nil {
			t.Fatalf("Reserve %s: %v", id, err)
		}
	}
	got, err := store.ReservedSessionIDs(ctx, tenant, "alice")
	if err != nil {
		t.Fatalf("ReservedSessionIDs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ReservedSessionIDs = %v, want both reservations from the primary", got)
	}
	none, err := store.ReservedSessionIDs(ctx, tenant, "bob")
	if err != nil || len(none) != 0 {
		t.Fatalf("another user's ReservedSessionIDs = %v, %v; want none", none, err)
	}
}
