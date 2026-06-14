// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	embpostgres "github.com/lennylabs/lenny/pkg/embedded/postgres"
	"github.com/lennylabs/lenny/pkg/gateway/pgtenant"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint"
	"github.com/lennylabs/lenny/pkg/gateway/quotacheckpoint/pgstore"
)

// TestDeleteByUserAndTenant_ErasesCheckpoint brings up an embedded Postgres
// and proves the §12.8 step-6 Postgres half of the QuotaStore erasure:
// DeleteByUser removes a single user's checkpoint rows while preserving
// other users and the per-tenant rollup, and DeleteByTenant removes the
// whole tenant. The token_usage_checkpoint table is the §11.2 line-48
// durable budget source, so leaving these rows behind would let a recovery
// reconcile resurrect an erased user's usage.
//
// spec: §12.8 step 6 (Redis + Postgres); §12.1 line 5.
func TestDeleteByUserAndTenant_ErasesCheckpoint_spec_12_8_step6(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15534,
		Database:     "lenny",
		Username:     "lenny",
		Password:     "lenny",
		StartTimeout: 3 * time.Minute,
	})
	if err := pg.Start(); err != nil {
		t.Fatalf("embedded postgres Start: %v", err)
	}
	defer func() { _ = pg.Stop() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, pg.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0119_token_usage_checkpoint.up.sql",
	)
	insertTenant(t, ctx, pool, "acme")
	insertTenant(t, ctx, pool, "globex")

	s := pgstore.New(pool)
	const (
		period = "hourly"
		label  = "hourly-2026060814"
	)
	rows := []quotacheckpoint.Row{
		{TenantID: "acme", Scope: "user", SubjectID: "alice", Period: period, WindowLabel: label, TokenTotal: 100},
		{TenantID: "acme", Scope: "user", SubjectID: "bob", Period: period, WindowLabel: label, TokenTotal: 50},
		{TenantID: "acme", Scope: "tenant", SubjectID: "", Period: period, WindowLabel: label, TokenTotal: 150},
		{TenantID: "globex", Scope: "user", SubjectID: "alice", Period: period, WindowLabel: label, TokenTotal: 70},
	}
	if err := s.Write(ctx, rows); err != nil {
		t.Fatalf("seed Write: %v", err)
	}

	// DeleteByUser(acme, alice): only acme/alice's user row is removed.
	n, err := s.DeleteByUser(ctx, "acme", "alice")
	if err != nil {
		t.Fatalf("DeleteByUser: %v", err)
	}
	if n != 1 {
		t.Errorf("DeleteByUser removed %d, want 1", n)
	}
	if c := countRows(t, ctx, pool, "acme", "user", "alice"); c != 0 {
		t.Errorf("acme/alice rows after erase = %d, want 0", c)
	}
	if c := countRows(t, ctx, pool, "acme", "user", "bob"); c != 1 {
		t.Errorf("acme/bob rows after alice erase = %d, want 1 (collateral erase)", c)
	}
	if c := countRows(t, ctx, pool, "acme", "tenant", ""); c != 1 {
		t.Errorf("acme tenant rollup after user erase = %d, want 1 (rollup must survive)", c)
	}
	if c := countRows(t, ctx, pool, "globex", "user", "alice"); c != 1 {
		t.Errorf("globex/alice after acme erase = %d, want 1 (cross-tenant leak)", c)
	}

	// A subject with no rows is an idempotent no-op.
	if n, err := s.DeleteByUser(ctx, "acme", "alice"); err != nil || n != 0 {
		t.Errorf("repeat DeleteByUser = (%d,%v), want (0,nil)", n, err)
	}

	// DeleteByTenant(acme): every remaining acme row goes; globex survives.
	n, err = s.DeleteByTenant(ctx, "acme")
	if err != nil {
		t.Fatalf("DeleteByTenant: %v", err)
	}
	if n != 2 { // bob user row + tenant rollup
		t.Errorf("DeleteByTenant removed %d, want 2", n)
	}
	if c := countRowsAllScopes(t, ctx, pool, "acme"); c != 0 {
		t.Errorf("acme rows after tenant erase = %d, want 0", c)
	}
	if c := countRowsAllScopes(t, ctx, pool, "globex"); c != 1 {
		t.Errorf("globex rows after acme tenant erase = %d, want 1", c)
	}
}

func countRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID, scope, subject string) int {
	t.Helper()
	var c int
	if err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM token_usage_checkpoint
			 WHERE tenant_id = $1 AND scope = $2 AND subject_id = $3`,
			tenantID, scope, subject).Scan(&c)
	}); err != nil {
		t.Fatalf("countRows: %v", err)
	}
	return c
}

func countRowsAllScopes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, tenantID string) int {
	t.Helper()
	var c int
	if err := pgtenant.InTx(ctx, pool, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx,
			`SELECT count(*) FROM token_usage_checkpoint WHERE tenant_id = $1`, tenantID).Scan(&c)
	}); err != nil {
		t.Fatalf("countRowsAllScopes: %v", err)
	}
	return c
}
