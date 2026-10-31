// SPDX-License-Identifier: MIT

//go:build integration

// Tier-4 integration test: the §12.8 user erasure deletes the erased user's
// delegation budget state through the real cmd/lenny-gateway binary, over a
// live Postgres and Redis. It seeds a delegation_tree_budget row and the
// tree-wide {root}:dlg:* keys for one root session of alice and one of bob,
// erases alice through the admin API, and asserts that alice's row and keys
// are gone while bob's remain.
package tier4_integration_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget"
	delegationbudgetpg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationbudget/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// dlgTreeKeys returns tree-wide and per-parent delegation budget keys of root.
func dlgTreeKeys(root string) []string {
	tag := "{" + root + "}:dlg:"
	return []string{tag + "tokens", tag + "tree_size", tag + "tree_memory", tag + "parallel_children:" + root}
}

// spec: 12.8 (Compliance Interfaces)
// diagnosis: the user erasure left the erased user's delegation_tree_budget
// row or tree-wide delegation budget keys behind, or deleted another user's.
// A surviving row keeps the erased user's tree counters, denial, and grants
// in Postgres after the session rows are gone and no later step can find
// them; a deleted bob row resets an unrelated tree's budget.
func TestUserErasureDeletesTheRootsDelegationBudgetRowAndKeys_spec_12_8(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)

	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0", "--tenant-deletion-interval=1s")
	base := gw.BaseURL()

	const alice, bob = "alice@acme.com", "bob@acme.com"
	code, _ := erasureAdminDo(t, base, http.MethodPost, "/v1/admin/bootstrap", "platform-admin", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
		"runtimes": []map[string]any{{
			"name": "echo", "image": "lenny/echo@sha256:abc", "labels": map[string]string{"tier": "test"},
		}},
		"users": []map[string]any{
			{"subject": alice, "tenantId": "acme", "email": alice, "roles": []string{"tenant-admin"}},
			{"subject": bob, "tenantId": "acme", "email": bob, "roles": []string{"tenant-admin"}},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d", code)
	}
	start := func(user string) string {
		t.Helper()
		code, created := erasureDoAs(t, base, http.MethodPost, "/v1/sessions/start", user, "", map[string]any{
			"runtimeRef": "echo",
		})
		sid, _ := created["id"].(string)
		if code != http.StatusCreated || sid == "" {
			t.Fatalf("create session for %s: %d (%v)", user, code, created)
		}
		return sid
	}
	aliceRoot, bobRoot := start(alice), start(bob)

	ctx := t.Context()
	rows := delegationbudgetpg.New(pg.Pool)
	if err := rows.Write(ctx, []delegationbudget.Checkpoint{
		{TenantID: "acme", RootSessionID: aliceRoot, TreeSize: 2, TokenBudgetConsumed: 100},
		{TenantID: "acme", RootSessionID: bobRoot, TreeSize: 3, TokenBudgetConsumed: 200},
	}); err != nil {
		t.Fatalf("seed delegation_tree_budget rows: %v", err)
	}
	for _, root := range []string{aliceRoot, bobRoot} {
		for _, k := range dlgTreeKeys(root) {
			if err := rd.Client.Set(ctx, k, "1", 0).Err(); err != nil {
				t.Fatalf("seed %s: %v", k, err)
			}
		}
	}

	jobID := eraseUser(t, base, alice)
	awaitErasureCompleted(t, base, jobID, 10*time.Second)

	countRow := func(root string) int {
		return dbCountErasure(t, pg, `SELECT COUNT(*) FROM delegation_tree_budget WHERE tenant_id = 'acme' AND root_session_id = $1`, root)
	}
	if n := countRow(aliceRoot); n != 0 {
		t.Errorf("the erased user's delegation_tree_budget row survived the erasure: count %d, want 0", n)
	}
	if n := countRow(bobRoot); n != 1 {
		t.Errorf("another user's delegation_tree_budget row count = %d, want 1", n)
	}
	if n := rd.Client.Exists(ctx, dlgTreeKeys(aliceRoot)...).Val(); n != 0 {
		t.Errorf("%d of the erased user's delegation budget keys survived the erasure, want 0", n)
	}
	if n := rd.Client.Exists(ctx, dlgTreeKeys(bobRoot)...).Val(); n != int64(len(dlgTreeKeys(bobRoot))) {
		t.Errorf("%d of another user's delegation budget keys remain, want %d", n, len(dlgTreeKeys(bobRoot)))
	}
}
