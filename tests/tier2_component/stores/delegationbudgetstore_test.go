//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §11.2 line 29 delegation tree budget checkpoint
// store, exercising the Postgres-backed
// pkg/gateway/delegationbudget/pgstore against a real container with the
// production migrations applied. Covers the Write/ListActive
// round-trip, the upsert that updates the counters and checkpoint_at
// while preserving the §8.6 extension_denied column, the cross-tenant
// ListActive read, and DeleteByTenant.
package stores_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/gateway/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// setExtensionDenied flips the §8.6 extension_denied column on a tree
// row under the tenant's RLS context, so the test can prove the §11.2
// counter checkpoint upsert does not clobber it.
func setExtensionDenied(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, root string) {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`UPDATE delegation_tree_budget SET extension_denied = TRUE WHERE tenant_id = $1 AND root_session_id = $2`,
		tenant, root); err != nil {
		t.Fatalf("set extension_denied: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

func extensionDenied(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, root string) bool {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL app.current_tenant = '"+tenant+"'"); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	var denied bool
	if err := tx.QueryRow(ctx,
		`SELECT extension_denied FROM delegation_tree_budget WHERE tenant_id = $1 AND root_session_id = $2`,
		tenant, root).Scan(&denied); err != nil {
		if err == pgx.ErrNoRows {
			t.Fatalf("row %q/%q missing", tenant, root)
		}
		t.Fatalf("read extension_denied: %v", err)
	}
	return denied
}

func findCheckpoint(rows []delegationbudget.Checkpoint, tenant, root string) (delegationbudget.Checkpoint, bool) {
	for _, r := range rows {
		if r.TenantID == tenant && r.RootSessionID == root {
			return r, true
		}
	}
	return delegationbudget.Checkpoint{}, false
}

// spec: §11.2 lines 29, 44, 48; §12.4 line 218.
// diagnosis: a failure means the delegation-budget store mis-accounts a
// tree's token/depth budget or mishandles the §12.4 erasure scope, so
// delegation budget enforcement would admit or reject the wrong calls.
func TestDelegationBudgetStoreContract(t *testing.T) {
	t.Parallel()
	_, pg := startStore(t)
	store := delegationbudgetpg.New(pg.Pool)
	ctx := context.Background()
	tenant := freshTenant(t, ctx, pg)

	// Write then ListActive round-trip.
	if err := store.Write(ctx, []delegationbudget.Checkpoint{{
		TenantID: tenant, RootSessionID: "root1", TreeSize: 3, TokenBudgetConsumed: 1000, TreeMemoryBytes: 36864,
	}}); err != nil {
		t.Fatalf("write: %v", err)
	}
	rows, err := store.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active: %v", err)
	}
	got, ok := findCheckpoint(rows, tenant, "root1")
	if !ok {
		t.Fatalf("root1 not found in ListActive")
	}
	if got.TreeSize != 3 || got.TokenBudgetConsumed != 1000 || got.TreeMemoryBytes != 36864 {
		t.Fatalf("round-trip = %+v, want {3,1000,36864}", got)
	}
	if got.CheckpointAt.IsZero() {
		t.Fatalf("checkpoint_at not populated on read")
	}

	// Upsert updates the counters and checkpoint_at but must preserve the
	// §8.6 extension_denied column (the checkpoint never owns it).
	setExtensionDenied(t, ctx, pg, tenant, "root1")
	if err := store.Write(ctx, []delegationbudget.Checkpoint{{
		TenantID: tenant, RootSessionID: "root1", TreeSize: 7, TokenBudgetConsumed: 2000, TreeMemoryBytes: 73728,
	}}); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	rows, err = store.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active after upsert: %v", err)
	}
	got, _ = findCheckpoint(rows, tenant, "root1")
	if got.TreeSize != 7 || got.TokenBudgetConsumed != 2000 || got.TreeMemoryBytes != 73728 {
		t.Fatalf("upserted counters = %+v, want {7,2000,73728}", got)
	}
	if !extensionDenied(t, ctx, pg, tenant, "root1") {
		t.Fatalf("upsert clobbered extension_denied; it must be preserved")
	}

	// Cross-tenant ListActive sees a second tenant's row.
	tenant2 := freshTenant(t, ctx, pg)
	if err := store.Write(ctx, []delegationbudget.Checkpoint{{
		TenantID: tenant2, RootSessionID: "root2", TreeSize: 1, TokenBudgetConsumed: 50, TreeMemoryBytes: 12288,
	}}); err != nil {
		t.Fatalf("write tenant2: %v", err)
	}
	rows, err = store.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active cross-tenant: %v", err)
	}
	if _, ok := findCheckpoint(rows, tenant, "root1"); !ok {
		t.Fatalf("tenant1 row missing from cross-tenant ListActive")
	}
	if _, ok := findCheckpoint(rows, tenant2, "root2"); !ok {
		t.Fatalf("tenant2 row missing from cross-tenant ListActive")
	}

	// DeleteByTenant removes only the named tenant's rows.
	n, err := store.DeleteByTenant(ctx, tenant)
	if err != nil {
		t.Fatalf("delete by tenant: %v", err)
	}
	if n != 1 {
		t.Fatalf("deleted %d rows, want 1", n)
	}
	rows, err = store.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active after delete: %v", err)
	}
	if _, ok := findCheckpoint(rows, tenant, "root1"); ok {
		t.Fatalf("tenant1 row survived DeleteByTenant")
	}
	if _, ok := findCheckpoint(rows, tenant2, "root2"); !ok {
		t.Fatalf("tenant2 row wrongly removed by tenant1 DeleteByTenant")
	}
}
