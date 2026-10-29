//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/environment/tenantstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
)

// seedParentIn stores a depth-zero parent row owned by alice in state st.
func (e *fenceEnv) seedParentIn(t *testing.T, st session.State) sessionstore.Session {
	t.Helper()
	p := e.row("alice")
	p.State = st
	if err := e.sessions.Create(context.Background(), p); err != nil {
		t.Fatalf("seed parent in %s: %v", st, err)
	}
	return p
}

// childOf is a depth-one delegated child row of parentID owned by alice.
func (e *fenceEnv) childOf(parentID string) sessionstore.Session {
	c := e.row("alice")
	c.ParentSessionID, c.RootSessionID, c.DelegationDepth = parentID, parentID, 1
	return c
}

// TestSessionStoreCreationFenceParentRow_spec_12_8 pins the parent rule of
// the creation fence inside pgstore.Create against real Postgres: a terminal
// parent, an absent parent, and an empty parent ID each refuse a delegated
// child with DELEGATION_PARENT_REVOKED and write no row, a parent in a live state other
// than running admits its child, a reservation reads no parent row, and a
// parent-row read error fails the insert closed.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.2 (Parent freshness)
// diagnosis: a failure means pgstore.Create does not read the delegated
// child's parent row inside its insert transaction, compares the parent
// against running instead of the terminal set, lets the parent_session_id
// foreign key answer an absent parent before the fence, skips the parent
// read for a delegated child with an empty parent ID, applies the parent
// rule to a reservation, or admits the insert when the parent read fails.
func TestSessionStoreCreationFenceParentRow_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)

	for _, st := range session.TerminalStates() {
		t.Run("terminal parent "+string(st)+" refuses", func(t *testing.T) {
			e.addTenant(t)
			parent := e.seedParentIn(t, st)
			child := e.childOf(parent.ID)
			ref := pgRefusalOf(t, e.sessions.Create(ctx, child), sessionstore.RefusalDelegationParentRevoked)
			if ref.Details["parentSessionId"] != parent.ID {
				t.Fatalf("details = %v, want parentSessionId %s", ref.Details, parent.ID)
			}
			if n := e.rowCount(t, child.ID); n != 0 {
				t.Fatalf("refused child left %d rows", n)
			}
		})
	}

	t.Run("absent parent refuses before the foreign key", func(t *testing.T) {
		e.addTenant(t)
		child := e.childOf(newUUID(t))
		requirePGRefusal(t, e.sessions.Create(ctx, child), sessionstore.RefusalDelegationParentRevoked)
		if n := e.rowCount(t, child.ID); n != 0 {
			t.Fatalf("refused child left %d rows", n)
		}
	})

	t.Run("delegated child with no parent ID refuses", func(t *testing.T) {
		e.addTenant(t)
		child := e.childOf("")
		child.RootSessionID = ""
		ref := pgRefusalOf(t, e.sessions.Create(ctx, child), sessionstore.RefusalDelegationParentRevoked)
		if ref.Details["parentSessionId"] != "" {
			t.Fatalf("details = %v, want an empty parentSessionId", ref.Details)
		}
		if n := e.rowCount(t, child.ID); n != 0 {
			t.Fatalf("refused child left %d rows", n)
		}

		reserved := e.childOf("")
		reserved.RootSessionID = ""
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", reserved.ID, true); err != nil {
			t.Fatalf("Reserve for a delegated child: %v, want admit (no parent rule)", err)
		}
		requirePGRefusal(t, e.sessions.CreateReserved(ctx, reserved), sessionstore.RefusalDelegationParentRevoked)
		if n := e.rowCount(t, reserved.ID); n != 0 {
			t.Fatalf("refused reserved child left %d rows", n)
		}
	})

	for _, st := range []session.State{session.StateSuspended, session.StateAwaitingClientAction, session.StateResumePending, session.StateCreated} {
		t.Run("live parent "+string(st)+" admits", func(t *testing.T) {
			e.addTenant(t)
			parent := e.seedParentIn(t, st)
			if err := e.sessions.Create(ctx, e.childOf(parent.ID)); err != nil {
				t.Fatalf("child of a %s parent: %v, want admit", st, err)
			}
		})
	}

	t.Run("reservation reads no parent row and CreateReserved refuses", func(t *testing.T) {
		e.addTenant(t)
		parent := e.seedParentIn(t, session.StateCompleted)
		child := e.childOf(parent.ID)
		if err := e.sessions.Reserve(ctx, e.tenant, "alice", child.ID, true); err != nil {
			t.Fatalf("Reserve under a terminal parent: %v, want admit", err)
		}
		requirePGRefusal(t, e.sessions.CreateReserved(ctx, child), sessionstore.RefusalDelegationParentRevoked)
		if n := e.rowCount(t, child.ID); n != 0 {
			t.Fatalf("refused child left %d rows", n)
		}
	})

	t.Run("a tenant marker wins over a terminal parent", func(t *testing.T) {
		e.addTenant(t)
		parent := e.seedParentIn(t, session.StateFailed)
		e.setTenant(t, func(tn *tenantstore.Tenant) { tn.Suspended = true })
		requirePGRefusal(t, e.sessions.Create(ctx, e.childOf(parent.ID)), sessionstore.RefusalTenantSuspended)
	})

	t.Run("parent read error fails the insert closed", func(t *testing.T) {
		e.addTenant(t)
		parent := e.seedParentIn(t, session.StateRunning)
		// A pool whose lock_timeout is short turns the parent read's wait
		// on a held FOR UPDATE lock into a read error.
		cfg, err := pgxpool.ParseConfig(e.pg.DSN)
		if err != nil {
			t.Fatalf("parse DSN: %v", err)
		}
		cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
			_, err := c.Exec(ctx, "SET lock_timeout = '300ms'")
			return err
		}
		pool, err := pgxpool.NewWithConfig(ctx, cfg)
		if err != nil {
			t.Fatalf("lock-timeout pool: %v", err)
		}
		t.Cleanup(pool.Close)
		timed := pgstore.New(pool)

		holder, err := e.pg.Pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin holder: %v", err)
		}
		t.Cleanup(func() { _ = holder.Rollback(ctx) })
		if _, err := holder.Exec(ctx, `SELECT 1 FROM sessions WHERE id = $1::uuid FOR UPDATE`, parent.ID); err != nil {
			t.Fatalf("hold the parent row: %v", err)
		}

		child := e.childOf(parent.ID)
		err = timed.Create(ctx, child)
		var ref *sessionstore.CreationRefusal
		if err == nil || errors.As(err, &ref) {
			t.Fatalf("Create with an unreadable parent row = %v, want a read error", err)
		}
		if n := e.rowCount(t, child.ID); n != 0 {
			t.Fatalf("insert after a failed parent read left %d rows", n)
		}
		if err := timed.Create(ctx, e.row("alice")); err != nil {
			t.Fatalf("depth-zero Create on the same pool: %v, want admit (only the parent read fails)", err)
		}
	})
}

// TestSessionStoreCreationFenceSerializesWithParentTerminalWrite_spec_12_8
// pins the lock conflict of the parent read. A parent's terminal Update,
// holding FOR UPDATE, blocks a delegated child's Create until it commits,
// and the create then observes the terminal parent and refuses. A child
// Create holding its FOR SHARE lock on the parent blocks the parent's
// terminal Update, and the committed child is on the primary listing of the
// parent's children once the Update returns.
//
// spec: 12.8 (Compliance Interfaces: Lifecycle fence and drain), 8.10 (Delegation Tree Recovery)
// diagnosis: a failure means the parent read runs outside the insert
// transaction or without a lock that conflicts with pgstore.Update's FOR
// UPDATE, so a child can commit after its parent's terminal write and escape
// the cascade's listing.
func TestSessionStoreCreationFenceSerializesWithParentTerminalWrite_spec_12_8(t *testing.T) {
	ctx := context.Background()
	var gate *heldCopier
	e := newFenceEnv(t, pgstore.WithTranscriptCopier(func(context.Context, pgx.Tx, string, string, string) error {
		return gate.wait()
	}))

	t.Run("a terminal Update blocks the child Create, which then refuses", func(t *testing.T) {
		e.addTenant(t)
		parent := e.seedParentIn(t, session.StateRunning)
		hold, entered := make(chan struct{}), make(chan struct{})
		writeDone := make(chan error, 1)
		go func() {
			_, err := e.sessions.Update(ctx, e.tenant, parent.ID, func(r *sessionstore.Session) error {
				close(entered)
				<-hold
				r.State = session.StateFailed
				return nil
			})
			writeDone <- err
		}()
		<-entered
		child := e.childOf(parent.ID)
		createDone := make(chan error, 1)
		go func() { createDone <- e.sessions.Create(ctx, child) }()
		awaitLockWaiter(t, ctx, e.pg, createDone)
		close(hold)
		if err := awaitUpdateResult(t, "terminal Update", writeDone); err != nil {
			t.Fatalf("terminal write: %v", err)
		}
		requirePGRefusal(t, awaitUpdateResult(t, "Create", createDone), sessionstore.RefusalDelegationParentRevoked)
		if n := e.rowCount(t, child.ID); n != 0 {
			t.Fatalf("refused child left %d rows", n)
		}
	})

	t.Run("an in-flight child Create blocks the terminal Update", func(t *testing.T) {
		e.addTenant(t)
		parent := e.seedParentIn(t, session.StateRunning)
		gate = newHeldCopier()
		t.Cleanup(gate.release)
		child := e.childOf(parent.ID)
		// CreateWithTranscript consumes a reservation, so the ID is
		// reserved first, as the replay handler reserves it.
		if err := e.sessions.Reserve(ctx, e.tenant, child.UserID, child.ID, true); err != nil {
			t.Fatalf("reserve child ID: %v", err)
		}
		createDone := make(chan error, 1)
		go func() { createDone <- e.sessions.CreateWithTranscript(ctx, child, parent.ID) }()
		<-gate.entered // the fence holds its FOR SHARE lock on the parent row
		writeDone := make(chan error, 1)
		go func() {
			_, err := e.sessions.Update(ctx, e.tenant, parent.ID, func(r *sessionstore.Session) error {
				r.State = session.StateCancelled
				return nil
			})
			writeDone <- err
		}()
		awaitLockWaiter(t, ctx, e.pg, writeDone)
		gate.release()
		if err := awaitUpdateResult(t, "Create", createDone); err != nil {
			t.Fatalf("in-flight child Create: %v", err)
		}
		if err := awaitUpdateResult(t, "terminal Update", writeDone); err != nil {
			t.Fatalf("terminal write: %v", err)
		}
		byParent, err := sessionstore.ChildrenByParent(ctx, e.sessions, e.tenant)
		if err != nil {
			t.Fatalf("ChildrenByParent: %v", err)
		}
		if kids := byParent[parent.ID]; len(kids) != 1 || kids[0].ID != child.ID {
			t.Fatalf("primary children of the ended parent = %v, want [%s]", kids, child.ID)
		}
	})
}
