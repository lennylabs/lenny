//go:build component

// SPDX-License-Identifier: MIT

package stores_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/orphancleanup"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/tests/testinfra/orphanfixture"
)

// TestSessionStoreRootRowIsItsOwnRoot_spec_12_8 pins the root column of a
// session inserted with no delegation parent, as derive and replay insert
// their sessions: pgstore writes root_session_id as the row's own id.
//
// spec: §12.8 step 16, §12.6 (StoreRouter)
// diagnosis: a failure means pgstore.Create no longer falls back to the row's
// own id when no parent is written, so a derived or replayed session joins
// another session's delegation tree and the tree-scoped readers (orphan
// cleanup, the cascade, tree recovery) treat it as a member.
func TestSessionStoreRootRowIsItsOwnRoot_spec_12_8(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)
	row := e.row("alice")
	row.State = session.StateRunning
	if err := e.sessions.Create(ctx, row); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := e.sessions.Get(ctx, e.tenant, row.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RootSessionID != row.ID || got.ParentSessionID != "" || got.DelegationDepth != 0 {
		t.Fatalf("root=%q parent=%q depth=%d, want root %q, no parent, depth 0",
			got.RootSessionID, got.ParentSessionID, got.DelegationDepth, row.ID)
	}
	var stored string
	if err := e.pg.Pool.QueryRow(ctx,
		`SELECT root_session_id::text FROM sessions WHERE id = $1::uuid`, row.ID).Scan(&stored); err != nil {
		t.Fatalf("read root_session_id: %v", err)
	}
	if stored != row.ID {
		t.Fatalf("stored root_session_id = %q, want %q", stored, row.ID)
	}
}

// TestDeletedParentLeavesDelegatedChildSelectedAsOrphan_spec_8_10 deletes the
// parent row of a live delegated child in Postgres. The parent foreign key's
// ON DELETE SET NULL clears the child's parent_session_id and leaves its
// delegation_depth, and orphan cleanup's predicate selects the child as an
// orphan whose parent is absent.
//
// spec: §8.10 (Orphan cleanup), §12.8 step 16
// diagnosis: a failure means the parent foreign key no longer clears the
// child's parent link, the depth column changed with it, or the orphan
// predicate reads an empty parent link as "not delegated", so a child whose
// parent row was deleted is never ended.
func TestDeletedParentLeavesDelegatedChildSelectedAsOrphan_spec_8_10(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)
	parent := e.seedParentIn(t, session.StateRunning)
	child := e.childOf(parent.ID)
	child.State = session.StateRunning
	if err := e.sessions.Create(ctx, child); err != nil {
		t.Fatalf("create child: %v", err)
	}
	if _, err := e.pg.Pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1::uuid`, parent.ID); err != nil {
		t.Fatalf("delete parent: %v", err)
	}
	got, err := e.sessions.Get(ctx, e.tenant, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.ParentSessionID != "" || got.DelegationDepth != 1 {
		t.Fatalf("child parent=%q depth=%d, want empty parent and depth 1", got.ParentSessionID, got.DelegationDepth)
	}
	rows, err := e.sessions.List(ctx, e.tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	o, ok := orphancleanup.Orphaned(got, orphancleanup.IndexByID(rows))
	if !ok || o.ParentPresent {
		t.Fatalf("Orphaned = %+v, %v; want selected with the parent absent", o, ok)
	}
}

// TestTwoOrphanSweepersOverPostgresEndEachOrphanOnce_spec_8_10 runs two
// orphan-cleanup sweepers concurrently over one Postgres session store after
// a cancel_all parent was cancelled with its depth-2 subtree still live. The
// locked-row guard in each sweeper's write admits one of them per row, so
// every descendant receives exactly one terminal write and one post-terminal
// hook, and no live session is left under a terminal cancel_all parent.
//
// spec: §8.10 (Orphan cleanup), §7.2 (Interactive Session Model)
// diagnosis: a failure means the sweeper's terminal write is not guarded on
// the locked row in pgstore.Update, so two replicas sweeping one tenant both
// end a session and run its terminal pipeline twice, or a pass does not
// cancel the children of a terminal cancel_all parent.
func TestTwoOrphanSweepersOverPostgresEndEachOrphanOnce_spec_8_10(t *testing.T) {
	ctx := context.Background()
	e := newFenceEnv(t)
	writes := orphanfixture.NewWriteCounter(e.sessions)
	nodes := orphanfixture.Chain(uuid.NewString, 2, 3)
	orphanfixture.Seed(t, writes, sessionstore.Session{TenantID: e.tenant, UserID: "alice", RuntimeRef: "echo"}, nodes)
	if _, _, err := orphanfixture.Cancel(ctx, writes, e.tenant, nodes[0].ID, time.Now()); err != nil {
		t.Fatal(err)
	}

	hooks := orphanfixture.NewHookCounter()
	tenants := orphancleanup.StaticTenants{e.tenant}
	sweepers := []*orphancleanup.Sweeper{
		orphancleanup.New(writes, tenants, orphancleanup.Options{Terminal: hooks}),
		orphancleanup.New(writes, tenants, orphancleanup.Options{Terminal: hooks}),
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, sw := range sweepers {
		wg.Add(1)
		go func(sw *orphancleanup.Sweeper) {
			defer wg.Done()
			<-start
			if _, err := sw.Tick(ctx, time.Now()); err != nil {
				t.Errorf("Tick: %v", err)
			}
		}(sw)
	}
	close(start)
	wg.Wait()
	hooks.Wait(t, 10*time.Second)

	rows, err := e.sessions.List(ctx, e.tenant, sessionstore.ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if live := orphanfixture.LiveUnderTerminalCancelAll(rows); len(live) != 0 {
		t.Fatalf("live sessions under a terminal cancel_all parent: %v", live)
	}
	reach := orphanfixture.CancelAllReach(nodes)
	orphanfixture.CheckOnce(t, nodes, writes, hooks.Hooks, reach)
	for id := range reach {
		if h := hooks.Hooks(id); h != 1 {
			t.Errorf("session %s ran %d sweeper hooks, want 1", id, h)
		}
	}
}
