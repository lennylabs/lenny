//go:build component

// SPDX-License-Identifier: MIT

// Component tests for the §10.1 checkpoint_manifest table (migration
// 0175): the §12.3 tenant-isolation apparatus (tenant-guard trigger,
// FORCE ROW LEVEL SECURITY, cross-tenant read denial), the §10.1 lines
// 143-151 partial_manifest_active_uniq at-most-one-active-partial
// invariant scoped to (session_id, slot_id), and the §10.1 line 141
// intent-row defaults (manifest_reason = 'in_progress',
// baseline_full_checkpoint_bytes NULL) the §7.2 resume path relies on.
package rls_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// insertManifest writes one checkpoint_manifest intent row for tenant /
// session / slot under that tenant's app.current_tenant context as the
// lenny_app role, mirroring the §10.1 intent-row-first INSERT. baseline
// and manifest_reason are left to their column defaults so callers can
// assert those defaults. Returns any INSERT error (e.g. the partial
// unique violation) without failing the test.
func insertManifest(t *testing.T, ctx context.Context, pg *containers.Postgres, tenant, session, slot, checkpointID string) error {
	t.Helper()
	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("insert manifest begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
		t.Fatalf("set tenant context %q: %v", tenant, err)
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO checkpoint_manifest (
			tenant_id, checkpoint_id, session_id, slot_id,
			chunk_object_key_prefix, chunk_size_bytes,
			checkpoint_started_at, checkpoint_timeout_at)
		 VALUES ($1, $2::uuid, $3, $4,
			'/'||$1||'/checkpoints/'||$3||'/'||$2::text||'/', 16777216,
			now(), now() + interval '30 seconds')`,
		tenant, checkpointID, session, slot)
	if err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}

// spec: 12.2.2, 12.3
// diagnosis: the checkpoint_manifest table (migration 0175) shipped
//
//	without the tenant-guard trigger, so a write issued with no
//	app.current_tenant set was admitted. Every §12.3 tenant-scoped
//	table's guard must reject a bare write regardless of role.
func TestCheckpointManifestRequiresTenantContext(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	_, err := pg.Pool.Exec(ctx,
		`INSERT INTO checkpoint_manifest (
			tenant_id, checkpoint_id, session_id,
			chunk_object_key_prefix, chunk_size_bytes,
			checkpoint_started_at, checkpoint_timeout_at)
		 VALUES ('acme', gen_random_uuid(), 'sess-1',
			'/acme/checkpoints/sess-1/', 16777216, now(), now())`)
	if err == nil {
		t.Fatal("checkpoint_manifest insert with no app.current_tenant must be rejected")
	}
	if !strings.Contains(err.Error(), "app.current_tenant is not set") {
		t.Errorf("unexpected rejection (want app.current_tenant guard): %v", err)
	}
}

// spec: 12.2.2, 12.3
// diagnosis: FORCE ROW LEVEL SECURITY on checkpoint_manifest leaked
//
//	cross-tenant rows. Connected as the non-superuser lenny_app role,
//	a query in tenant-a context must never observe a tenant-b manifest.
func TestCheckpointManifestPreventsCrossTenantRead(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()

	seedTenant(t, ctx, pg, "tenant-a")
	seedTenant(t, ctx, pg, "tenant-b")
	if err := insertManifest(t, ctx, pg, "tenant-a", "sess-a", "default", "11111111-1111-1111-1111-111111111111"); err != nil {
		t.Fatalf("seed tenant-a manifest: %v", err)
	}
	if err := insertManifest(t, ctx, pg, "tenant-b", "sess-b", "default", "22222222-2222-2222-2222-222222222222"); err != nil {
		t.Fatalf("seed tenant-b manifest: %v", err)
	}

	conn, err := pg.Pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire conn: %v", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SET ROLE lenny_app"); err != nil {
		t.Fatalf("set role lenny_app: %v", err)
	}

	for _, tc := range []struct{ tenant, foreign string }{
		{"tenant-a", "tenant-b"},
		{"tenant-b", "tenant-a"},
	} {
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := tx.Exec(ctx,
			"SELECT set_config('app.current_tenant', $1, true)", tc.tenant); err != nil {
			t.Fatalf("set context %q: %v", tc.tenant, err)
		}
		var own, foreign int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM checkpoint_manifest`).Scan(&own); err != nil {
			t.Fatalf("count own for %q: %v", tc.tenant, err)
		}
		if err := tx.QueryRow(ctx,
			`SELECT COUNT(*) FROM checkpoint_manifest WHERE tenant_id = $1`, tc.foreign).Scan(&foreign); err != nil {
			t.Fatalf("count foreign for %q: %v", tc.tenant, err)
		}
		_ = tx.Rollback(ctx)
		if own != 1 {
			t.Errorf("tenant %q sees %d of its own manifests, want 1", tc.tenant, own)
		}
		if foreign != 0 {
			t.Errorf("tenant %q sees %d cross-tenant manifests, want 0 (RLS leak)", tc.tenant, foreign)
		}
	}
}

// spec: 10.1
// diagnosis: the partial_manifest_active_uniq index did not enforce
//
//	at-most-one active partial manifest per (session_id, slot_id).
//	The migration 0150 index scoped on (tenant_id, session_id) admitted
//	a second active partial row for a distinct slot's key; the §10.1
//	line 147 index scopes on (session_id, slot_id) over active partial
//	rows so a second active partial row for the same slot is rejected.
func TestCheckpointManifestActivePartialIsUnique(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	if err := insertManifest(t, ctx, pg, "acme", "sess-1", "default", "33333333-3333-3333-3333-333333333333"); err != nil {
		t.Fatalf("first active partial manifest must insert: %v", err)
	}

	// A second active partial row for the same (session_id, slot_id) — a
	// fresh checkpoint_id, so the primary key does not collide — must be
	// rejected by the partial unique index.
	err := insertManifest(t, ctx, pg, "acme", "sess-1", "default", "44444444-4444-4444-4444-444444444444")
	if err == nil {
		t.Fatal("second active partial manifest for the same (session_id, slot_id) must be rejected")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" || pgErr.ConstraintName != "partial_manifest_active_uniq" {
		t.Errorf("want unique violation on partial_manifest_active_uniq, got: %v", err)
	}

	// A different slot for the same session is a distinct active key and
	// must be admitted.
	if err := insertManifest(t, ctx, pg, "acme", "sess-1", "slot-2", "55555555-5555-5555-5555-555555555555"); err != nil {
		t.Errorf("a distinct slot's active partial manifest must insert: %v", err)
	}
}

// spec: 10.1, 7.2
// diagnosis: the intent row wrote a non-'in_progress' manifest_reason or
//
//	a non-NULL baseline_full_checkpoint_bytes for a session with no
//	prior full checkpoint. §10.1 line 141 requires the intent row to
//	carry manifest_reason = 'in_progress' until a terminal arm
//	overwrites it, and baseline_full_checkpoint_bytes NULL so the §10.1
//	line 155 IS NULL branch and the §7.2 optional-fraction rule keep the
//	resume path from dividing by zero.
func TestCheckpointManifestIntentRowDefaults(t *testing.T) {
	t.Parallel()
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		MigrationsDir: migrationsDir(t),
	})
	ctx := context.Background()
	seedTenant(t, ctx, pg, "acme")

	if err := insertManifest(t, ctx, pg, "acme", "sess-1", "default", "66666666-6666-6666-6666-666666666666"); err != nil {
		t.Fatalf("insert intent row: %v", err)
	}

	tx, err := pg.Pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx,
		"SELECT set_config('app.current_tenant', 'acme', true)"); err != nil {
		t.Fatalf("set context: %v", err)
	}
	var reason string
	var baseline *int64
	var partial bool
	if err := tx.QueryRow(ctx,
		`SELECT manifest_reason, baseline_full_checkpoint_bytes, partial
		   FROM checkpoint_manifest WHERE tenant_id = 'acme' AND session_id = 'sess-1'`).
		Scan(&reason, &baseline, &partial); err != nil {
		t.Fatalf("read intent row: %v", err)
	}
	if reason != "in_progress" {
		t.Errorf("intent-row manifest_reason = %q, want in_progress", reason)
	}
	if baseline != nil {
		t.Errorf("intent-row baseline_full_checkpoint_bytes = %d, want NULL", *baseline)
	}
	if !partial {
		t.Errorf("intent-row partial = %v, want true", partial)
	}
}

