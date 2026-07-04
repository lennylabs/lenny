// SPDX-License-Identifier: MIT

package pgstore_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lennylabs/lenny/migrations"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioncheckpointmeta/pgstore"
	embpostgres "github.com/lennylabs/lenny/tests/testinfra/embpg"
)

// TestSessionCheckpointMetaPgStore_spec_10_1 brings up an embedded
// Postgres, applies the tenant/RLS prerequisites plus migration 0148,
// and exercises the Postgres-backed session_checkpoint_meta store. It
// pins the reconciled schema after proposal 0026 removed the
// resume-dedup columns: the store persists barrier_id, checkpoint_ref,
// and workspace_recovery_fraction and no longer binds
// last_tool_call_id/last_tool_call_sequence. The round-trip runs
// against the migration-0148 table, so a store that still referenced
// the dropped columns would fail to insert or scan. It downloads the
// PostgreSQL bundle, so it is skipped under -short.
//
// spec: §10.1 lines 165-166, 393.
func TestSessionCheckpointMetaPgStore_spec_10_1(t *testing.T) {
	if testing.Short() {
		t.Skip("downloads the PostgreSQL bundle; skipped under -short")
	}
	pg := embpostgres.New(embpostgres.Config{
		DataDir:      t.TempDir(),
		Port:         15547,
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

	// The tenant anchor + RLS roles + lenny_tenant_guard() (0001, 0002)
	// and the session_checkpoint_meta table itself (0148). 0148 references
	// only tenants(id), so no later migration is needed.
	applyMigrations(
		t, ctx, pool,
		"0001_initial_schema.up.sql",
		"0002_rls_immutability_roles.up.sql",
		"0148_session_checkpoint_meta.up.sql",
	)
	insertTenant(t, ctx, pool, "acme")
	insertTenant(t, ctx, pool, "globex")

	s := pgstore.New(pool, nil)
	var _ sessioncheckpointmeta.Store = s

	// Upsert then Get round-trips the retained fields against the
	// migration-0148 schema. A store that still bound
	// last_tool_call_id/last_tool_call_sequence would reference columns
	// migration 0148 no longer creates and fail here.
	frac := 0.75
	rec := sessioncheckpointmeta.Record{
		TenantID:                  "acme",
		SessionID:                 "sess-1",
		CoordinationGeneration:    3,
		BarrierID:                 "1",
		CheckpointRef:             "ckpt-abc",
		WorkspaceRecoveryFraction: &frac,
	}
	if err := s.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	got, err := s.Get(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CoordinationGeneration != 3 || got.BarrierID != "1" || got.CheckpointRef != "ckpt-abc" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	if got.WorkspaceRecoveryFraction == nil || *got.WorkspaceRecoveryFraction != 0.75 {
		t.Fatalf("workspace_recovery_fraction not round-tripped: %v", got.WorkspaceRecoveryFraction)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("updated_at not stamped")
	}

	// A second barrier overwrites the prior generation's row.
	rec2 := sessioncheckpointmeta.Record{
		TenantID:               "acme",
		SessionID:              "sess-1",
		CoordinationGeneration: 4,
		BarrierID:              "2",
		CheckpointRef:          "ckpt-def",
	}
	if err := s.Upsert(ctx, rec2); err != nil {
		t.Fatalf("second Upsert: %v", err)
	}
	got, err = s.Get(ctx, "acme", "sess-1")
	if err != nil {
		t.Fatalf("Get after overwrite: %v", err)
	}
	if got.CoordinationGeneration != 4 || got.BarrierID != "2" || got.CheckpointRef != "ckpt-def" {
		t.Fatalf("overwrite did not take: %+v", got)
	}
	if got.WorkspaceRecoveryFraction != nil {
		t.Fatalf("overwrite should clear the fraction, got %v", *got.WorkspaceRecoveryFraction)
	}

	// A missing row returns ErrNotFound.
	if _, err := s.Get(ctx, "acme", "missing"); err != sessioncheckpointmeta.ErrNotFound {
		t.Fatalf("missing Get = %v, want ErrNotFound", err)
	}

	// Tenant isolation: globex's row is independent.
	if err := s.Upsert(ctx, sessioncheckpointmeta.Record{
		TenantID: "globex", SessionID: "sess-1", BarrierID: "g1", CheckpointRef: "g-ck",
	}); err != nil {
		t.Fatalf("globex Upsert: %v", err)
	}
	g, err := s.Get(ctx, "globex", "sess-1")
	if err != nil {
		t.Fatalf("globex Get: %v", err)
	}
	if g.BarrierID != "g1" || g.CheckpointRef != "g-ck" {
		t.Fatalf("globex row = %+v, want independent", g)
	}
}

func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool, names ...string) {
	t.Helper()
	for _, name := range names {
		sql, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

func insertTenant(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	nonce := make([]byte, 32)
	if _, err := pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, $2)
		 ON CONFLICT (id) DO NOTHING`, id, nonce); err != nil {
		t.Fatalf("insert tenant %q: %v", id, err)
	}
}
