//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// chunkEncodingCheckPriorVersion is the schema version immediately below
// migration 0194, the point the rollback half returns to.
const chunkEncodingCheckPriorVersion = 193

// spec: 10.1 (Chunked-object storage model), 4.4 (MinIO object key logging
// for manual recovery)
// diagnosis: migration 0194 did not restore the closed-enum CHECK on
//
//	checkpoint_manifest.chunk_encoding, or its .down.sql did not drop it.
//	The chunk object key suffix and the resume decoder both derive from
//	this column, so a database that admits 'zip' holds a manifest whose
//	chunks no reader can name or decode. A failure on the 'tar' or 'tar.gz'
//	rows means the constraint is narrower than the §10.1 enum.
func TestCheckpointManifestChunkEncodingCheckMigration_spec_10_1(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant %s: %v", tenant, err)
	}
	// Each row names its own session: partial_manifest_active_uniq holds
	// one active partial manifest per session.
	const insertManifest = `INSERT INTO checkpoint_manifest
		(tenant_id, checkpoint_id, session_id, chunk_object_key_prefix, chunk_size_bytes,
		 chunk_encoding, checkpoint_started_at, checkpoint_timeout_at)
		VALUES ($1, $2, $4, '/checkpoints/', 1048576, $3, now(), now() + interval '1 hour')`
	const (
		tarID     = "6f1c2a4e-0000-4000-8000-000000019401"
		tarGzID   = "6f1c2a4e-0000-4000-8000-000000019402"
		zipID     = "6f1c2a4e-0000-4000-8000-000000019403"
		refusedID = "6f1c2a4e-0000-4000-8000-000000019404"
	)

	// Both §10.1 enum values are admitted.
	for id, enc := range map[string]string{tarID: "tar", tarGzID: "tar.gz"} {
		if err := execTenant(ctx, pg, tenant, insertManifest, tenant, id, enc, "sess-"+id); err != nil {
			t.Fatalf("insert manifest row with chunk_encoding %q: %v", enc, err)
		}
	}

	// Out-of-enum values are refused on INSERT, including the empty value
	// and spellings that differ from a legal value only in case or
	// whitespace. The Go stores map an empty encoding to tar.gz before the
	// write, so the database never needs to admit it.
	for _, enc := range []string{"zip", "", "TAR.GZ", " tar", "tar.zst"} {
		assertCheckViolation(t, "insert chunk_encoding "+enc,
			execTenant(ctx, pg, tenant, insertManifest, tenant, refusedID, enc, "sess-"+refusedID))
	}
	// An UPDATE cannot move a legal row out of the enum.
	assertCheckViolation(t, "update chunk_encoding to zip",
		execTenant(ctx, pg, tenant,
			`UPDATE checkpoint_manifest SET chunk_encoding = 'zip' WHERE checkpoint_id = $1`, tarID))

	// Rolling 0194 back removes the constraint and keeps the legal rows.
	pg.MigrateTo(t, dir, chunkEncodingCheckPriorVersion)
	if err := execTenant(ctx, pg, tenant, insertManifest, tenant, zipID, "zip", "sess-"+zipID); err != nil {
		t.Fatalf("after rolling 0194 back the column is unconstrained; insert 'zip': %v", err)
	}
	if err := execTenant(ctx, pg, tenant,
		`DELETE FROM checkpoint_manifest WHERE checkpoint_id = $1`, zipID); err != nil {
		t.Fatalf("remove the out-of-enum row before re-applying 0194: %v", err)
	}

	// Re-applying 0194 validates the remaining rows and restores the refusal.
	pg.MigrateTo(t, dir, chunkEncodingCheckPriorVersion+1)
	assertCheckViolation(t, "insert chunk_encoding zip after re-applying 0194",
		execTenant(ctx, pg, tenant, insertManifest, tenant, zipID, "zip", "sess-"+zipID))
	var rows int
	if err := pg.Pool.QueryRow(ctx,
		`SELECT count(*) FROM checkpoint_manifest WHERE checkpoint_id IN ($1, $2)`, tarID, tarGzID).
		Scan(&rows); err != nil {
		t.Fatalf("count legal rows: %v", err)
	}
	if rows != 2 {
		t.Errorf("legal rows after the 0194 round trip = %d, want 2", rows)
	}
}

// assertCheckViolation fails the test unless err is a Postgres
// check_violation (SQLSTATE 23514).
func assertCheckViolation(t *testing.T, what string, err error) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Errorf("%s: err = %v, want a check_violation (23514)", what, err)
	}
}
