//go:build component

// SPDX-License-Identifier: MIT

// Contract test for the §10.7 lenny_eval_aggregates materialized view
// (migration 0156). Covers the DDL existence and ownership, the
// BYPASSRLS cross-tenant REFRESH, the tenant-scoped read view isolation
// under the non-superuser lenny_app role, the pgstore aggregate read
// path matching the on-read aggregation, and the down rollback. This
// lives in its own package so it compiles independently of any drift in
// the broader stores component suite.
package evalaggregates_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore"
	evalpg "github.com/lennylabs/lenny/pkg/gateway/experiment/evalstore/pgstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	sessionpg "github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/pgstore"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

func startPG(t *testing.T) *containers.Postgres {
	t.Helper()
	return containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: schematest.RepoRoot(t) + "/migrations",
	})
}

func newUUID(t *testing.T) string {
	t.Helper()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

func freshTenant(t *testing.T, ctx context.Context, pg *containers.Postgres) string {
	t.Helper()
	id := "t-" + newUUID(t)[:8]
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)`, id, []byte{0x01}); err != nil {
		t.Fatalf("seed tenant %q: %v", id, err)
	}
	return id
}

func seedSession(t *testing.T, ctx context.Context, ss sessionstore.Store, tenant string) string {
	t.Helper()
	id := newUUID(t)
	if err := ss.Create(ctx, sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning, RuntimeRef: "echo",
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	return id
}

func evalScore(v float64) *float64 { return &v }

// spec: §10.7 line 1088 — the materialized view, its BYPASSRLS owner
// role, the SECURITY DEFINER refresh function, and the tenant-scoped
// read view are all defined in the migration system. F-10.7.12.
// diagnosis: a failure means the eval-aggregates matview DDL, its
// BYPASSRLS owner role, the SECURITY DEFINER refresh function, or the
// tenant-scoped read view is missing or misdefined in the migrations.
func TestEvalAggregatesMatviewDDL_spec_10_7_1088(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()

	var n int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_matviews WHERE matviewname = 'lenny_eval_aggregates'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lenny_eval_aggregates matview missing (n=%d err=%v)", n, err)
	}
	var bypassRLS bool
	if err := pg.Pool.QueryRow(ctx,
		`SELECT rolbypassrls FROM pg_roles WHERE rolname = 'lenny_eval_aggregator'`).Scan(&bypassRLS); err != nil {
		t.Fatalf("lenny_eval_aggregator role missing: %v", err)
	}
	if !bypassRLS {
		t.Error("lenny_eval_aggregator must hold BYPASSRLS")
	}
	var secDef, ownerIsAggregator bool
	if err := pg.Pool.QueryRow(ctx, `
		SELECT p.prosecdef, r.rolname = 'lenny_eval_aggregator'
		FROM pg_proc p JOIN pg_roles r ON r.oid = p.proowner
		WHERE p.proname = 'refresh_lenny_eval_aggregates'`).Scan(&secDef, &ownerIsAggregator); err != nil {
		t.Fatalf("refresh function missing: %v", err)
	}
	if !secDef {
		t.Error("refresh_lenny_eval_aggregates must be SECURITY DEFINER")
	}
	if !ownerIsAggregator {
		t.Error("refresh_lenny_eval_aggregates must be owned by lenny_eval_aggregator")
	}
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM pg_views WHERE viewname = 'lenny_eval_aggregates_tenant'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("lenny_eval_aggregates_tenant view missing (n=%d err=%v)", n, err)
	}
}

// spec: §10.7 lines 954, 1088 — a cross-tenant REFRESH populates every
// tenant's aggregates (BYPASSRLS), the tenant-scoped view isolates each
// tenant under the non-superuser lenny_app role, lenny_app cannot read
// the matview directly, and the pgstore aggregate read reproduces the
// on-read aggregation. F-10.7.12.
// diagnosis: a failure means the matview refresh leaks across tenants,
// the lenny_app role can read the matview directly, or the pgstore
// aggregate read diverges from the on-read aggregation.
func TestEvalAggregatesRefreshIsolationAndAggregation_spec_10_7_1088(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	sessionStore := sessionpg.New(pg.Pool)
	store := evalpg.New(pg.Pool)
	ctx := context.Background()

	acme := freshTenant(t, ctx, pg)
	globex := freshTenant(t, ctx, pg)

	sA := seedSession(t, ctx, sessionStore, acme)
	sB := seedSession(t, ctx, sessionStore, acme)
	sC := seedSession(t, ctx, sessionStore, acme)
	put := func(tenant, sess, exp, variant, scorer string, score *float64, dims map[string]float64) {
		if _, err := store.Put(ctx, evalstore.EvalResult{
			TenantID: tenant, SessionID: sess, ExperimentID: exp,
			VariantID: variant, Scorer: scorer, Score: score, Scores: dims,
		}); err != nil {
			t.Fatalf("seed eval: %v", err)
		}
	}
	// judge: 0.8 (sA, coherence 0.7), 0.9 (sB, coherence 0.9 + relevance 0.5).
	// safety: no top-level score (sC) with only a bias dimension.
	// one unattributed row (experiment_id="") must be excluded.
	put(acme, sA, "exp_eval", "treatment", "judge", evalScore(0.8), map[string]float64{"coherence": 0.7})
	put(acme, sB, "exp_eval", "treatment", "judge", evalScore(0.9), map[string]float64{"coherence": 0.9, "relevance": 0.5})
	put(acme, sC, "exp_eval", "treatment", "safety", nil, map[string]float64{"bias": 0.2})
	put(acme, sA, "", "treatment", "judge", evalScore(0.1), nil)

	gS := seedSession(t, ctx, sessionStore, globex)
	put(globex, gS, "exp_eval", "treatment", "judge", evalScore(0.3), nil)

	if err := store.RefreshAggregates(ctx); err != nil {
		t.Fatalf("RefreshAggregates: %v", err)
	}
	// Refresh is idempotent (CONCURRENTLY needs a populated view); a second
	// call must also succeed.
	if err := store.RefreshAggregates(ctx); err != nil {
		t.Fatalf("RefreshAggregates (second): %v", err)
	}

	var tenantsInView int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(DISTINCT tenant_id) FROM lenny_eval_aggregates WHERE tenant_id IN ($1,$2)`,
		acme, globex).Scan(&tenantsInView); err != nil {
		t.Fatalf("matview count: %v", err)
	}
	if tenantsInView != 2 {
		t.Errorf("matview holds %d of 2 tenants after cross-tenant refresh", tenantsInView)
	}

	aggs, err := store.AggregatesByExperiment(ctx, acme, "exp_eval")
	if err != nil {
		t.Fatalf("AggregatesByExperiment: %v", err)
	}
	tr, ok := aggs["treatment"]
	if !ok {
		t.Fatalf("treatment aggregate missing: %+v", aggs)
	}
	if tr.SampleCount != 3 {
		t.Errorf("sampleCount = %d, want 3 distinct sessions", tr.SampleCount)
	}
	judge := tr.Scorers["judge"]
	if judge.Count != 2 || !approx(judge.Mean, 0.85) || !approx(judge.P50, 0.8) || !approx(judge.P95, 0.9) {
		t.Errorf("judge = %+v, want count 2 mean 0.85 p50 0.8 p95 0.9", judge)
	}
	if c := judge.Dimensions["coherence"]; c.Count != 2 || !approx(c.Mean, 0.8) || !approx(c.P50, 0.7) || !approx(c.P95, 0.9) {
		t.Errorf("coherence = %+v, want count 2 mean 0.8 p50 0.7 p95 0.9", c)
	}
	if r := judge.Dimensions["relevance"]; r.Count != 1 || !approx(r.Mean, 0.5) {
		t.Errorf("relevance = %+v, want count 1 mean 0.5", r)
	}
	safety := tr.Scorers["safety"]
	if safety.Count != 0 || safety.Mean != 0 {
		t.Errorf("safety top-level = %+v, want zero (dimension-only scorer)", safety)
	}
	if b := safety.Dimensions["bias"]; b.Count != 1 || !approx(b.Mean, 0.2) {
		t.Errorf("bias = %+v, want count 1 mean 0.2", b)
	}

	if acmeRows := tenantViewCountAsApp(t, ctx, pg, acme); acmeRows == 0 {
		t.Error("lenny_app/acme sees no aggregate rows through the tenant view")
	}
	if crossRows := tenantViewCrossCountAsApp(t, ctx, pg, acme, globex); crossRows != 0 {
		t.Errorf("lenny_app/acme leaked %d globex rows through the tenant view", crossRows)
	}
	if directMatviewReadableAsApp(t, ctx, pg, acme) {
		t.Error("lenny_app could read lenny_eval_aggregates directly, bypassing the tenant filter")
	}

	if m, err := store.AggregatesByExperiment(ctx, acme, ""); err != nil || len(m) != 0 {
		t.Errorf("empty experiment id: map=%v err=%v", m, err)
	}
	if m, err := store.AggregatesByExperiment(ctx, acme, "absent"); err != nil || len(m) != 0 {
		t.Errorf("unknown experiment id: map=%v err=%v", m, err)
	}
}

// spec: §10.7 line 1088 — migration 0156 rolls back cleanly.
// diagnosis: a failure means migration 0156 does not roll back cleanly,
// leaving eval-aggregates objects behind or erroring on down-migration.
func TestEvalAggregatesMigrationRollback_spec_10_7_1088(t *testing.T) {
	t.Parallel()
	pg := startPG(t)
	ctx := context.Background()
	dir := schematest.RepoRoot(t) + "/migrations"

	pg.MigrateTo(t, dir, 155)

	for _, q := range []struct {
		label string
		sql   string
	}{
		{"matview", `SELECT count(*) FROM pg_matviews WHERE matviewname = 'lenny_eval_aggregates'`},
		{"view", `SELECT count(*) FROM pg_views WHERE viewname = 'lenny_eval_aggregates_tenant'`},
		{"function", `SELECT count(*) FROM pg_proc WHERE proname = 'refresh_lenny_eval_aggregates'`},
		{"role", `SELECT count(*) FROM pg_roles WHERE rolname = 'lenny_eval_aggregator'`},
	} {
		var n int
		if err := pg.Pool.QueryRow(ctx, q.sql).Scan(&n); err != nil {
			t.Fatalf("post-rollback %s query: %v", q.label, err)
		}
		if n != 0 {
			t.Errorf("post-rollback %s still present (n=%d)", q.label, n)
		}
	}
}

func approx(a, b float64) bool { return a-b < 1e-9 && b-a < 1e-9 }

func tenantViewCountAsApp(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) int {
	t.Helper()
	tx := beginAsApp(t, ctx, pg, tenant)
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM lenny_eval_aggregates_tenant`).Scan(&n); err != nil {
		t.Fatalf("tenant view count: %v", err)
	}
	return n
}

func tenantViewCrossCountAsApp(t *testing.T, ctx context.Context, pg *containers.Postgres, scope, other string) int {
	t.Helper()
	tx := beginAsApp(t, ctx, pg, scope)
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM lenny_eval_aggregates_tenant WHERE tenant_id = $1`, other).Scan(&n); err != nil {
		t.Fatalf("cross count: %v", err)
	}
	return n
}

func directMatviewReadableAsApp(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) bool {
	t.Helper()
	tx := beginAsApp(t, ctx, pg, tenant)
	defer func() { _ = tx.Rollback(ctx) }()
	var n int
	return tx.QueryRow(ctx, `SELECT count(*) FROM lenny_eval_aggregates`).Scan(&n) == nil
}

// beginAsApp opens a tx, sets the RLS tenant context, and drops to the
// non-superuser lenny_app role (a superuser bypasses RLS, so the read
// must run as the gateway's production role).
func beginAsApp(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant string) pgx.Tx {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	return tx
}
