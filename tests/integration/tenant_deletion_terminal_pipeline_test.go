// SPDX-License-Identifier: MIT

//go:build integration

// Integration test: §12.8 tenant deletion holds Phase 2 until the terminal
// pipeline of every session its drain ended has recorded its end, driven
// through the real multi-tenant cmd/lenny-gateway binary over a migrated
// Postgres and a Redis. A trigger records each session row's pipeline mark at
// the moment Phase 4 deletes the row, because the deletion leaves no other
// witness of the state Phase 2 left it in.
package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/gateway"
)

// installPipelineMarkProbe records, for every sessions row a later statement
// deletes, its tenant, ID, terminated_at, terminal_pipeline_done_at, and the
// deletion time.
func installPipelineMarkProbe(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE test_pipeline_mark_probe (deleted_tenant TEXT, session_id UUID, terminated_at TIMESTAMPTZ, pipeline_done_at TIMESTAMPTZ, deleted_at TIMESTAMPTZ)`,
		`CREATE FUNCTION test_record_pipeline_mark() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER AS $$
		 BEGIN
		   INSERT INTO test_pipeline_mark_probe VALUES (OLD.tenant_id, OLD.id, OLD.terminated_at, OLD.terminal_pipeline_done_at, clock_timestamp());
		   RETURN OLD;
		 END $$`,
		`CREATE TRIGGER test_pipeline_mark_probe BEFORE DELETE ON sessions
		 FOR EACH ROW EXECUTE FUNCTION test_record_pipeline_mark()`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("install pipeline mark probe: %v", err)
		}
	}
}

// spec: §12.8 (Lifecycle fence and drain), §12.8 (Tenant deletion lifecycle),
// §7.1 (Pipeline-pending session)
// diagnosis: a failure means tenant deletion left Phase 2 while a session its
// drain ended had not recorded the end of its terminal pipeline and its budget
// had not elapsed, so Phase 4 deleted the session row while the pipeline could
// still write the session's billing event, quota checkpoint, or session log:
// the tenant-deletion terminator (cmd/lenny-gateway/tenantdeletion.go) does
// not wait on admin.TerminalPipelinesPending, or the gateway writes the
// pipeline mark before the pipeline's last step.
func TestTenantDeletionDeletesNoDrainedSessionBeforeItsTerminalPipelineEnds(t *testing.T) {
	t.Parallel()
	gateway.SkipUnlessAvailable(t)
	pg := sharedDatabase(t)
	rd := containers.StartRedis(t, containers.RedisOptions{})
	gw := gateway.StartWith(t,
		"--no-environment-policy", "allow-all", "--dev-mode",
		"--postgres-dsn="+pg.DSN, "--postgres-billing-audit-ddl-dsn="+pg.DSN,
		"--redis-url=redis://"+rd.Addr+"/0",
		"--tenancy-mode=multi", "--multi-tenant=true",
		"--bearer-trust-hmac-key-file="+writeTenancyTrustKey(t),
		"--tenant-deletion-interval=300ms",
		"--workspace-seal-max-duration-seconds=8", "--stream-failure-report-timeout=2s")
	ctx := context.Background()

	code, raw := deletionAdminDo(t, gw.BaseURL(), http.MethodPost, "/v1/admin/bootstrap", map[string]any{
		"tenants": []map[string]any{{"id": "acme", "displayName": "Acme Corp"}},
	})
	if code != http.StatusOK {
		t.Fatalf("bootstrap: status %d: %s", code, raw)
	}
	live := seedLiveSessions(t, ctx, pg.Pool, "acme", liveSessionsPerTenant)
	installPipelineMarkProbe(t, ctx, pg.Pool)

	if code, raw := deletionAdminDo(t, gw.BaseURL(), http.MethodDelete, "/v1/admin/tenants/acme", nil); code != http.StatusAccepted {
		t.Fatalf("DELETE /v1/admin/tenants/acme: status %d: %s", code, raw)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		var left int
		if err := pg.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM sessions WHERE tenant_id = 'acme'`).Scan(&left); err != nil {
			t.Fatalf("count acme sessions: %v", err)
		}
		if left == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d acme sessions remain 90s after the DELETE request; Phase 4 never deleted them", left)
		}
		time.Sleep(100 * time.Millisecond)
	}

	rows, err := pg.Pool.Query(ctx,
		`SELECT session_id::text, terminated_at, pipeline_done_at, deleted_at FROM test_pipeline_mark_probe WHERE deleted_tenant = 'acme'`)
	if err != nil {
		t.Fatalf("read pipeline mark probe: %v", err)
	}
	defer rows.Close()
	seen := map[string]bool{}
	for rows.Next() {
		var id string
		var terminatedAt, doneAt *time.Time
		var deletedAt time.Time
		if err := rows.Scan(&id, &terminatedAt, &doneAt, &deletedAt); err != nil {
			t.Fatalf("scan pipeline mark probe: %v", err)
		}
		seen[id] = true
		switch {
		case terminatedAt == nil:
			t.Errorf("Phase 4 deleted session %s with no terminated_at; Phase 2 did not drain it", id)
		case doneAt == nil && deletedAt.Before(terminatedAt.Add(pipelineTestBudget)):
			t.Errorf("Phase 4 deleted session %s %v after it ended, with no pipeline mark and inside the %v budget",
				id, deletedAt.Sub(*terminatedAt), pipelineTestBudget)
		}
	}
	for _, id := range live {
		if !seen[id] {
			t.Errorf("live session %s was not deleted through the probe", id)
		}
	}
}
