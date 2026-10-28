//go:build component

// SPDX-License-Identifier: MIT

package migrations_test

import (
	"context"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// chunkEncodingDefaultPriorVersion is the schema version immediately below
// migration 0193, the point the rollback half returns to.
const chunkEncodingDefaultPriorVersion = 192

// spec: 10.1 (Chunked-object storage model), 4.4 (MinIO object key logging
// for manual recovery)
// diagnosis: migration 0193 did not move the checkpoint_manifest.chunk_encoding
//
//	default from 'tar' to 'tar.gz', or its .down.sql did not restore 'tar'.
//	The only chunk producer writes gzip-compressed tar chunks, so a row that
//	took a 'tar' default would tell an operator following the §4.4 manual
//	recovery procedure to run a plain tar extract against gzip bytes. A failure
//	on the INSERT half means the default exists in the catalog but does not
//	reach a row that omits the column.
func TestCheckpointManifestChunkEncodingDefaultMigration_spec_10_1(t *testing.T) {
	t.Parallel()
	dir := prodMigrations(t)
	pg := containers.StartPostgres(t, containers.PostgresOptions{MigrationsDir: dir})
	ctx := context.Background()

	if got := columnDefault(t, ctx, pg, "checkpoint_manifest", "chunk_encoding"); !strings.HasPrefix(got, "'tar.gz'") {
		t.Errorf("checkpoint_manifest.chunk_encoding default after 0193 = %q, want 'tar.gz'", got)
	}
	mustBeNotNull(t, ctx, pg, "checkpoint_manifest", "chunk_encoding")

	// A row that omits the column records the producer's encoding.
	const tenant = "acme"
	if _, err := pg.Pool.Exec(ctx,
		`INSERT INTO tenants (id, genesis_nonce) VALUES ($1, '\x00')`, tenant); err != nil {
		t.Fatalf("seed tenant %s: %v", tenant, err)
	}
	const checkpointID = "6f1c2a4e-0000-4000-8000-000000000193"
	const insertManifest = `INSERT INTO checkpoint_manifest
		(tenant_id, checkpoint_id, session_id, chunk_object_key_prefix, chunk_size_bytes,
		 checkpoint_started_at, checkpoint_timeout_at)
		VALUES ($1, $2, 'sess-alice', '/checkpoints/', 1048576, now(), now() + interval '1 hour')`
	if err := execTenant(ctx, pg, tenant, insertManifest, tenant, checkpointID); err != nil {
		t.Fatalf("insert manifest row omitting chunk_encoding: %v", err)
	}
	var encoding string
	if err := pg.Pool.QueryRow(ctx,
		`SELECT chunk_encoding FROM checkpoint_manifest WHERE checkpoint_id = $1`, checkpointID).
		Scan(&encoding); err != nil {
		t.Fatalf("read chunk_encoding: %v", err)
	}
	if encoding != "tar.gz" {
		t.Errorf("chunk_encoding of a row omitting the column = %q, want tar.gz", encoding)
	}

	// Rolling 0193 back restores the 0178 default and leaves the row as written.
	pg.MigrateTo(t, dir, chunkEncodingDefaultPriorVersion)
	if got := columnDefault(t, ctx, pg, "checkpoint_manifest", "chunk_encoding"); !strings.HasPrefix(got, "'tar'") {
		t.Errorf("checkpoint_manifest.chunk_encoding default after rolling 0193 back = %q, want 'tar'", got)
	}
	if err := pg.Pool.QueryRow(ctx,
		`SELECT chunk_encoding FROM checkpoint_manifest WHERE checkpoint_id = $1`, checkpointID).
		Scan(&encoding); err != nil {
		t.Fatalf("read chunk_encoding after rollback: %v", err)
	}
	if encoding != "tar.gz" {
		t.Errorf("rollback rewrote chunk_encoding to %q; the down must change only the default", encoding)
	}
}
