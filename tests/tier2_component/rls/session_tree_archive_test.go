//go:build component

// SPDX-License-Identifier: MIT

// Component coverage for the §8.10 session_tree_archive table
// (migration 0100) and its Postgres-backed store. Exercises the
// tenant-isolation machinery and the pgstore round trip:
//
//   - the lenny_tenant_guard trigger rejects a write whose row
//     tenant_id does not match app.current_tenant,
//   - the session_tree_archive_tenant_isolation RLS policy filters
//     reads to the calling tenant under lenny_app,
//   - the (root_session_id, node_session_id) PK makes a re-archive an
//     idempotent upsert rather than a duplicate row,
//   - pgstore Replay returns a tree's nodes in original-settlement
//     order, and GetByNode resolves a settled child by its node id.
//
// spec: §8.10 lines 129, 1062; §7.1 lines 426-433; §12.7 line 783.
package rls_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/treearchive/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// seedArchiveSession inserts one session under tenant and returns its id. The
// archive's root_session_id FK requires the row to exist.
func seedArchiveSession(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) string {
	t.Helper()
	var id string
	if err := pgtenant.InTx(ctx, pg.Pool, tenant, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`INSERT INTO sessions (id, tenant_id, state, runtime_ref, root_session_id)
			 VALUES (gen_random_uuid(), $1, 'completed', 'echo', gen_random_uuid())
			 RETURNING id::text`, tenant).Scan(&id)
	}); err != nil {
		t.Fatalf("seed session for %s: %v", tenant, err)
	}
	return id
}

// spec: §8.10 line 129 — the lenny_tenant_guard trigger rejects an
// insert whose row tenant_id does not match app.current_tenant.
func TestSessionTreeArchiveTriggerRejectsMismatchedTenant(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: migrationsDir(t)})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "alice")
	seedTenant(t, ctx, pg, "bob")
	aliceRoot := seedArchiveSession(t, ctx, pg, "alice")

	// Under bob's tenant context, name alice's tenant_id: the trigger
	// must reject it.
	err := pgtenant.InTx(ctx, pg.Pool, "bob", func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx,
			`INSERT INTO session_tree_archive
			   (tenant_id, root_session_id, node_session_id, state, settled_at)
			 VALUES ('alice', $1::uuid, gen_random_uuid(), 'completed', now())`,
			aliceRoot)
		return err
	})
	if err == nil {
		t.Errorf("mismatched-tenant archive insert succeeded; trigger must reject (§8.10 line 129)")
	}
}

// spec: §8.10 line 129 — RLS isolates archive rows by tenant under the
// lenny_app role; alice never sees bob's tree.
func TestSessionTreeArchiveRLSIsolatesPerTenant(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: migrationsDir(t)})
	ctx := context.Background()
	store := treearchivepg.New(pg.Pool, nil)

	seedTenant(t, ctx, pg, "alice")
	seedTenant(t, ctx, pg, "bob")
	aliceRoot := seedArchiveSession(t, ctx, pg, "alice")
	bobRoot := seedArchiveSession(t, ctx, pg, "bob")

	mustArchive(t, ctx, store, treearchive.ArchivedNode{
		TenantID: "alice", RootSessionID: aliceRoot, NodeSessionID: aliceRoot,
		State: "completed", Result: `{"taskId":"a"}`, SettledAt: time.Unix(100, 0).UTC(),
	})
	mustArchive(t, ctx, store, treearchive.ArchivedNode{
		TenantID: "bob", RootSessionID: bobRoot, NodeSessionID: bobRoot,
		State: "completed", Result: `{"taskId":"b"}`, SettledAt: time.Unix(100, 0).UTC(),
	})

	for _, tc := range []struct {
		tenant string
		want   int
	}{{"alice", 1}, {"bob", 1}} {
		var got int
		err := pgtenant.InTx(ctx, pg.Pool, tc.tenant, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
				return err
			}
			return tx.QueryRow(ctx, `SELECT COUNT(*) FROM session_tree_archive`).Scan(&got)
		})
		if err != nil {
			t.Fatalf("count under %s: %v", tc.tenant, err)
		}
		if got != tc.want {
			t.Errorf("%s sees %d archive rows, want %d (RLS isolation)", tc.tenant, got, tc.want)
		}
	}
}

// spec: §8.10 line 129 — re-archiving the same (root, node) is an
// idempotent upsert: a node settles once, and a cascade re-archive
// overwrites rather than duplicating.
func TestSessionTreeArchiveUpsertIsIdempotent(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: migrationsDir(t)})
	ctx := context.Background()
	store := treearchivepg.New(pg.Pool, nil)

	seedTenant(t, ctx, pg, "alice")
	root := seedArchiveSession(t, ctx, pg, "alice")
	node := seedArchiveSession(t, ctx, pg, "alice")

	mustArchive(t, ctx, store, treearchive.ArchivedNode{
		TenantID: "alice", RootSessionID: root, NodeSessionID: node,
		State: "completed", Result: `{"v":1}`, SettledAt: time.Unix(100, 0).UTC(),
	})
	// Re-archive the same node with a later result.
	mustArchive(t, ctx, store, treearchive.ArchivedNode{
		TenantID: "alice", RootSessionID: root, NodeSessionID: node,
		State: "failed", Result: `{"v":2}`, SettledAt: time.Unix(200, 0).UTC(),
	})

	got, err := store.Get(ctx, "alice", root, node)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.State != "failed" || got.Result != `{"v":2}` {
		t.Errorf("upsert did not overwrite: state=%q result=%q", got.State, got.Result)
	}
	nodes, err := store.Replay(ctx, "alice", root)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(nodes) != 1 {
		t.Errorf("replay returned %d rows, want 1 (re-archive must not duplicate)", len(nodes))
	}
}

// spec: §8.10 lines 1062-1063 — Replay returns a tree's settled nodes
// in original-settlement order; GetByNode resolves a child by its
// globally-unique node id without the tree root.
func TestSessionTreeArchiveReplayOrderAndGetByNode(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: migrationsDir(t)})
	ctx := context.Background()
	store := treearchivepg.New(pg.Pool, nil)

	seedTenant(t, ctx, pg, "alice")
	root := seedArchiveSession(t, ctx, pg, "alice")
	c1 := seedArchiveSession(t, ctx, pg, "alice")
	c2 := seedArchiveSession(t, ctx, pg, "alice")
	c3 := seedArchiveSession(t, ctx, pg, "alice")

	// Archive out of settlement order; Replay must reorder.
	mustArchive(t, ctx, store, treearchive.ArchivedNode{TenantID: "alice", RootSessionID: root, NodeSessionID: c2, ParentSessionID: root, State: "completed", SettledAt: time.Unix(200, 0).UTC()})
	mustArchive(t, ctx, store, treearchive.ArchivedNode{TenantID: "alice", RootSessionID: root, NodeSessionID: c1, ParentSessionID: root, State: "completed", SettledAt: time.Unix(100, 0).UTC()})
	mustArchive(t, ctx, store, treearchive.ArchivedNode{TenantID: "alice", RootSessionID: root, NodeSessionID: c3, ParentSessionID: root, State: "completed", SettledAt: time.Unix(300, 0).UTC()})

	nodes, err := store.Replay(ctx, "alice", root)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	gotOrder := []string{nodes[0].NodeSessionID, nodes[1].NodeSessionID, nodes[2].NodeSessionID}
	wantOrder := []string{c1, c2, c3}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Errorf("replay order[%d] = %s, want %s (settlement order)", i, gotOrder[i], wantOrder[i])
		}
	}

	got, err := store.GetByNode(ctx, "alice", c2)
	if err != nil {
		t.Fatalf("getbynode: %v", err)
	}
	if got.NodeSessionID != c2 || got.ParentSessionID != root {
		t.Errorf("getbynode returned node=%s parent=%s, want node=%s parent=%s", got.NodeSessionID, got.ParentSessionID, c2, root)
	}

	// A node id absent from the archive is ErrNotFound.
	if _, err := store.GetByNode(ctx, "alice", root); err == nil {
		t.Errorf("getbynode for an unarchived node id returned no error; want ErrNotFound")
	}
}

func mustArchive(t *testing.T, ctx context.Context, store *treearchivepg.Store, n treearchive.ArchivedNode) {
	t.Helper()
	if err := store.Archive(ctx, n); err != nil {
		t.Fatalf("archive node %s: %v", n.NodeSessionID, err)
	}
}
