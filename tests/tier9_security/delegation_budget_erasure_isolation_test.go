// SPDX-License-Identifier: MIT

//go:build security

// Tier-9 isolation of the §12.8 user erasure's delegation budget step over
// the production stores: the treebudget Reserver on a real Redis and the
// delegationbudget pgstore on a real Postgres with the production migrations.
// The step purges a root's {root}:dlg:* keys and then deletes its
// delegation_tree_budget row under the erased user's tenant. The test pins
// that erasing alice's root leaves bob's root and a globex tree sharing
// alice's root id untouched, and that a row delete under the wrong tenant
// deletes nothing.
package tier9_security_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treebudget"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// budgetRowCount counts the delegation_tree_budget rows of one tree.
func budgetRowCount(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, root string) int {
	t.Helper()
	var n int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM delegation_tree_budget WHERE tenant_id = $1 AND root_session_id = $2`,
		tenant, root).Scan(&n); err != nil {
		t.Fatalf("count %s/%s: %v", tenant, root, err)
	}
	return n
}

// spec: 12.8 (Compliance Interfaces)
// diagnosis: the user erasure's delegation budget step deleted another
// user's or another tenant's delegation_tree_budget row, or a row delete
// under the wrong tenant removed a row it does not own. Either resets an
// unrelated tree's counters, denial, and grants, which crosses the §12.8
// erasure boundary and the tenant boundary.
func TestUserErasureDelegationBudgetStepStaysInsideTheUsersTrees_spec_12_8(t *testing.T) {
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: schematest.RepoRoot(t) + "/migrations"})
	rd := containers.StartRedis(t, containers.RedisOptions{})
	ctx := context.Background()
	for _, id := range []string{"acme", "globex"} {
		seedIsolationTenant(t, ctx, pg.Pool, id)
	}
	keys := treebudget.New(rd.Client, time.Hour)
	rows := delegationbudgetpg.New(pg.Pool)

	const aliceRoot, bobRoot = "root-alice", "root-bob"
	isoMust(t, "seed rows", rows.Write(ctx, []delegationbudget.Checkpoint{
		{TenantID: "acme", RootSessionID: aliceRoot, TreeSize: 2, TokenBudgetConsumed: 100},
		{TenantID: "acme", RootSessionID: bobRoot, TreeSize: 3, TokenBudgetConsumed: 200},
		{TenantID: "globex", RootSessionID: aliceRoot, TreeSize: 4, TokenBudgetConsumed: 300},
	}))
	treeKeys := func(root string) []string {
		return []string{"{" + root + "}:dlg:tokens", "{" + root + "}:dlg:tree_size", "{" + root + "}:dlg:children_total:" + root}
	}
	for _, root := range []string{aliceRoot, bobRoot} {
		for _, k := range treeKeys(root) {
			isoMust(t, "seed "+k, rd.Client.Set(ctx, k, "1", 0).Err())
		}
	}

	t.Run("a row delete under the wrong tenant deletes nothing", func(t *testing.T) {
		n, err := rows.DeleteByRoot(ctx, "initech", bobRoot)
		if err != nil || n != 0 {
			t.Fatalf("DeleteByRoot(initech, %s) = (%d, %v), want (0, nil)", bobRoot, n, err)
		}
		if got := budgetRowCount(t, ctx, pg, "acme", bobRoot); got != 1 {
			t.Fatalf("acme %s rows after a wrong-tenant delete = %d, want 1", bobRoot, got)
		}
	})

	t.Run("erasing alice's root leaves bob's and globex's trees", func(t *testing.T) {
		if _, err := keys.PurgeRoot(ctx, aliceRoot); err != nil {
			t.Fatalf("PurgeRoot: %v", err)
		}
		n, err := rows.DeleteByRoot(ctx, "acme", aliceRoot)
		if err != nil || n != 1 {
			t.Fatalf("DeleteByRoot(acme, %s) = (%d, %v), want (1, nil)", aliceRoot, n, err)
		}
		if got := budgetRowCount(t, ctx, pg, "acme", aliceRoot); got != 0 {
			t.Errorf("alice's row survived: %d", got)
		}
		if got := budgetRowCount(t, ctx, pg, "acme", bobRoot); got != 1 {
			t.Errorf("bob's row count = %d, want 1", got)
		}
		if got := budgetRowCount(t, ctx, pg, "globex", aliceRoot); got != 1 {
			t.Errorf("globex's row under the same root id count = %d, want 1", got)
		}
		if n := rd.Client.Exists(ctx, treeKeys(aliceRoot)...).Val(); n != 0 {
			t.Errorf("%d of alice's tree keys survived, want 0", n)
		}
		if n := rd.Client.Exists(ctx, treeKeys(bobRoot)...).Val(); n != int64(len(treeKeys(bobRoot))) {
			t.Errorf("%d of bob's tree keys remain, want %d", n, len(treeKeys(bobRoot)))
		}
	})

	t.Run("a repeated erasure is a no-op", func(t *testing.T) {
		n, err := rows.DeleteByRoot(ctx, "acme", aliceRoot)
		if err != nil || n != 0 {
			t.Fatalf("second DeleteByRoot = (%d, %v), want (0, nil)", n, err)
		}
	})
}
