//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test: §12.8 tenant deletion against the real
// cmd/lenny-gateway binary and a migrated Postgres. It seeds a tenant
// whose sessions hold a delegated tree with archived child results,
// checkpoints, and eval results, deletes the tenant through the admin
// API, and asserts that Phase 4 completes and leaves none of the
// tenant's rows behind while a second tenant's rows survive.
package tier4_integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention"
	checkpointretentionpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/checkpointretention/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore"
	partialmanifestpg "github.com/lennylabs/lenny/pkg/gateway/checkpoint/partialmanifeststore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	evalstorepg "github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive"
	treearchivepg "github.com/lennylabs/lenny/pkg/gateway/mcpfabric/delegationtree/treearchive/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
	sessioncheckpointmetapg "github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionstorepg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// phase4Tables are the session-keyed tables the seed populates. Phase 4
// must leave none of the deleted tenant's rows in any of them.
var phase4Tables = []string{
	"sessions",
	"session_tree_archive",
	"eval_results",
	"checkpoint_manifest",
	"session_checkpoints",
	"session_checkpoint_meta",
}

// seededTenant records the identifiers seeded for one tenant.
type seededTenant struct {
	root, child, grandchild string
}

// seedTenantTree writes a three-level delegation tree for tenant: a root
// session, a delegated child, and a delegated grandchild, with the
// settled child and grandchild archived under the root, a checkpoint
// manifest, a retained checkpoint, checkpoint metadata, and an eval
// result per session.
func seedTenantTree(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) seededTenant {
	t.Helper()
	ids := seededTenant{root: uuid.NewString(), child: uuid.NewString(), grandchild: uuid.NewString()}
	now := time.Now().UTC()
	sessions := sessionstorepg.New(pool)
	for _, s := range []sessionstore.Session{
		{ID: ids.root, TenantID: tenant, UserID: "alice@" + tenant + ".com", State: session.StateRunning, CreatedAt: now, UpdatedAt: now},
		{ID: ids.child, TenantID: tenant, UserID: "alice@" + tenant + ".com", ParentSessionID: ids.root, RootSessionID: ids.root, State: session.StateCompleted, CreatedAt: now, UpdatedAt: now},
		{ID: ids.grandchild, TenantID: tenant, UserID: "alice@" + tenant + ".com", ParentSessionID: ids.child, RootSessionID: ids.root, State: session.StateFailed, FailureReason: string(session.FailureDeadlockTimeout), CreatedAt: now, UpdatedAt: now},
	} {
		if err := sessions.Create(ctx, s); err != nil {
			t.Fatalf("seed session %s/%s: %v", tenant, s.ID, err)
		}
	}

	archive := treearchivepg.New(pool, nil)
	for _, n := range []treearchive.ArchivedNode{
		{TenantID: tenant, RootSessionID: ids.root, NodeSessionID: ids.child, ParentSessionID: ids.root, State: "completed", Result: `{"taskId":"` + ids.child + `"}`, SettledAt: now},
		{TenantID: tenant, RootSessionID: ids.root, NodeSessionID: ids.grandchild, ParentSessionID: ids.child, State: "failed", Result: `{"taskId":"` + ids.grandchild + `"}`, SettledAt: now},
	} {
		if err := archive.Archive(ctx, n); err != nil {
			t.Fatalf("seed archive %s/%s: %v", tenant, n.NodeSessionID, err)
		}
	}

	manifests := partialmanifestpg.New(pool, nil)
	retention := checkpointretentionpg.New(pool, nil)
	meta := sessioncheckpointmetapg.New(pool, nil)
	evals := evalstorepg.New(pool)
	for _, sid := range []string{ids.root, ids.child} {
		cp := uuid.NewString()
		if err := manifests.Put(ctx, partialmanifeststore.Record{
			TenantID: tenant, CheckpointID: cp, SessionID: sid,
			ChunkObjectKeyPrefix: fmt.Sprintf("/%s/checkpoints/%s/%s/", tenant, sid, cp),
			CheckpointStartedAt:  now, CheckpointTimeoutAt: now.Add(time.Minute),
		}); err != nil {
			t.Fatalf("seed checkpoint manifest %s/%s: %v", tenant, sid, err)
		}
		if err := retention.Insert(ctx, checkpointretention.Record{
			TenantID: tenant, SessionID: sid, Ref: "checkpoints/" + cp, Retained: true,
		}); err != nil {
			t.Fatalf("seed retained checkpoint %s/%s: %v", tenant, sid, err)
		}
		if err := meta.Upsert(ctx, sessioncheckpointmeta.Record{
			TenantID: tenant, SessionID: sid, BarrierID: "b-1", CheckpointRef: "checkpoints/" + cp,
		}); err != nil {
			t.Fatalf("seed checkpoint meta %s/%s: %v", tenant, sid, err)
		}
		score := 0.5
		if _, err := evals.Put(ctx, evalstore.EvalResult{
			TenantID: tenant, SessionID: sid, Scorer: "llm-judge", Score: &score,
		}); err != nil {
			t.Fatalf("seed eval result %s/%s: %v", tenant, sid, err)
		}
	}
	return ids
}

// tenantRowCounts returns the row count of each phase4Tables table for
// tenant.
func tenantRowCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) map[string]int {
	t.Helper()
	out := make(map[string]int, len(phase4Tables))
	for _, table := range phase4Tables {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
			t.Fatalf("count %s for %s: %v", table, tenant, err)
		}
		out[table] = n
	}
	return out
}

// leftoverTenantTables reports every table with a tenant_id column that
// still holds rows for tenant, excluding the tombstoned tenants row and
// the audit_log, whose gdpr.* receipts §12.8 retains.
func leftoverTenantTables(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenant string) map[string]int {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT c.table_name
		  FROM information_schema.columns c
		  JOIN information_schema.tables tb
		    ON tb.table_schema = c.table_schema AND tb.table_name = c.table_name
		 WHERE c.table_schema = 'public' AND c.column_name = 'tenant_id'
		   AND tb.table_type = 'BASE TABLE'`)
	if err != nil {
		t.Fatalf("list tenant tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	out := map[string]int{}
	for _, table := range tables {
		if table == "tenants" || table == "audit_log" {
			continue
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM "`+table+`" WHERE tenant_id::text = $1`, tenant).Scan(&n); err != nil {
			continue // a partitioned parent or a view-like table; the per-table assertions cover the seed
		}
		if n > 0 {
			out[table] = n
		}
	}
	return out
}

// tenantDeletionLogLines returns the gateway log lines that mention the
// tenant-deletion lifecycle, capped so a failure message stays readable.
func tenantDeletionLogLines(logs string) string {
	var out []string
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, "tenant-deletion") || strings.Contains(line, "tenant erase") {
			out = append(out, line)
		}
	}
	if len(out) > 40 {
		out = out[len(out)-40:]
	}
	return strings.Join(out, "\n")
}

// diagnosis: a failure means §12.8 tenant deletion did not finish Phase 4
// for a tenant with archived delegation trees and checkpoints, or left
// its rows behind. A tenant stuck in `deleting` with sessions still
// present usually means a session-keyed eraser (session_tree_archive
// first among them, because its foreign key into sessions has no ON
// DELETE action) is missing from the gateway's tenant eraser sequence or
// runs after SessionStore. Surviving rows for the second tenant mean a
// DeleteByTenant ignores its tenant scope.
//
// spec: §12.8 (Compliance Interfaces, tenant deletion Phase 4), §8.10
// (Delegation Tree Recovery).
func TestTenantDeletionPhase4ClearsArchivedTreesAndCheckpoints_spec_12_8(t *testing.T) {
	gateway.SkipUnlessAvailable(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: filepath.Join(schematest.RepoRoot(t), "migrations"),
	})
	gw := gateway.StartWith(t, "--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN,
		// The DDL login provisions each tenant's audit sequence at
		// bootstrap; the Phase 6 erasure receipt appends to it.
		"--postgres-billing-audit-ddl-dsn="+pg.DSN,
		"--tenant-deletion-interval=300ms")
	ctx := context.Background()

	adminDo := func(method, path string, body any) (int, []byte) {
		t.Helper()
		var reader io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			reader = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, gw.BaseURL()+path, reader)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Lenny-Tenant-ID", "acme")
		req.Header.Set("X-Lenny-User-ID", "alice@acme.com")
		req.Header.Set("X-Lenny-Roles", "platform-admin")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, raw
	}

	code, raw := adminDo(http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{
			{"id": "acme", "displayName": "Acme Corp"},
			{"id": "globex", "displayName": "Globex"},
		},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}

	seedTenantTree(t, ctx, pg.Pool, "acme")
	seedTenantTree(t, ctx, pg.Pool, "globex")
	globexBefore := tenantRowCounts(t, ctx, pg.Pool, "globex")
	for table, n := range tenantRowCounts(t, ctx, pg.Pool, "acme") {
		if n == 0 {
			t.Fatalf("seed wrote no %s rows for acme; the test would not exercise Phase 4", table)
		}
	}

	if code, raw := adminDo(http.MethodDelete, "/v1/admin/tenants/acme", nil); code != http.StatusAccepted {
		t.Fatalf("DELETE /v1/admin/tenants/acme: status %d: %s", code, raw)
	}

	// The runner advances one phase per tick; the tombstone (`deleted`)
	// is written only after Phase 4 and the Phase 6 receipt succeed.
	deadline := time.Now().Add(90 * time.Second)
	var state string
	for time.Now().Before(deadline) {
		if err := pg.Pool.QueryRow(ctx, `SELECT coalesce(state, '') FROM tenants WHERE id = 'acme'`).Scan(&state); err != nil {
			t.Fatalf("read acme tenant state: %v", err)
		}
		if state == "deleted" {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if state != "deleted" {
		t.Fatalf("acme tenant state = %q after 90s, want deleted; remaining rows: %v\ngateway tenant-deletion log:\n%s",
			state, tenantRowCounts(t, ctx, pg.Pool, "acme"), tenantDeletionLogLines(gw.Logs()))
	}

	for table, n := range tenantRowCounts(t, ctx, pg.Pool, "acme") {
		if n != 0 {
			t.Errorf("%s still holds %d acme rows after tenant deletion", table, n)
		}
	}
	globexAfter := tenantRowCounts(t, ctx, pg.Pool, "globex")
	for table, before := range globexBefore {
		if globexAfter[table] != before {
			t.Errorf("%s globex rows = %d after deleting acme, want %d", table, globexAfter[table], before)
		}
	}
	// Report, without failing, any other tenant-scoped table Phase 4 does
	// not yet erase, so the gap stays visible.
	if left := leftoverTenantTables(t, ctx, pg.Pool, "acme"); len(left) > 0 {
		t.Logf("tenant-scoped tables still holding acme rows outside the seeded set: %v", left)
	}
}
