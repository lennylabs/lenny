//go:build integration

// SPDX-License-Identifier: MIT

// Tier-4 integration test for the §25.11 backup-and-restore round trip.
// It drives the production lenny-backup runner surfaces — the ExecDumper
// (pg_dump per shard), the TarGzArchiver (client-side AES-256-GCM
// encryption), the MinIOUploader (SSE-S3 upload), the TarGzOpener
// (download and decrypt), and the ExecScratchRestorer (pg_restore into a
// scratch database) — against a real Postgres container and a real MinIO
// container. It seeds a row, runs a full backup that writes a real
// encrypted archive to MinIO, restores that archive into a fresh
// database, and asserts the seeded row reappears.
//
// This exercises the §25.11 "Full backup flow inside the Job" (pg_dump,
// encrypt client-side, upload to MinIO) and the restore "Runs pg_restore
// against each shard" as a genuine data round trip, which the tier-5
// chart-render test cannot: the tier-5 test only proves the templates
// render and the image loads.
package tier4_integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/url"
	"os/exec"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/minio/minio-go/v7"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
	"github.com/lennylabs/lenny/tests/testinfra/containers"
)

// captureReporter records the runner's terminal transition so the test
// can assert on the §25.11 step-8 completion the run reports.
type captureReporter struct {
	result   runner.Result
	failed   string
	failedID string
}

func (r *captureReporter) BackupCompleted(_ context.Context, res runner.Result) error {
	r.result = res
	return nil
}

func (r *captureReporter) BackupFailed(_ context.Context, id, msg string) error {
	r.failedID, r.failed = id, msg
	return nil
}

// spec: §25.11 (Backup and Restore API) — "Full backup flow inside the
// Job: 1. Runs pg_dump against each Postgres shard ... 5. Encrypts the
// archive client-side with AES-256-GCM ... 7. Uploads to MinIO at
// backups/{type}/{id}/{timestamp}.tar.gz.enc"; Restore Execution —
// "Runs pg_restore against each shard."
//
// diagnosis: the §25.11 backup-and-restore round trip is broken. The
// runner produced a real encrypted archive from a live Postgres, wrote
// it to MinIO, and read it back, but a restore of that archive into a
// fresh database did not reproduce the seeded row. A failure means one
// of the production runner surfaces (ExecDumper pg_dump, TarGzArchiver
// AES-256-GCM, MinIOUploader SSE-S3 upload, TarGzOpener decrypt,
// ExecScratchRestorer pg_restore) does not round-trip tenant data.
func TestBackupRestoreRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("starts Postgres and MinIO containers and downloads pg client tools; skipped under -short")
	}
	// The runner shells out to pg_dump / pg_restore / psql. They run in
	// the lenny-backup image in production; a tier-4 host run needs them
	// on PATH. Skip with a precise diagnosis when the tooling is absent,
	// matching the infra-gated skip convention used elsewhere in tier 4.
	pgDump := lookTool(t, "pg_dump")
	pgRestore := lookTool(t, "pg_restore")
	psql := lookTool(t, "psql")

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Pin Postgres 15 so the host pg_dump (>= 15) can dump the server;
	// pg_dump refuses a server newer than itself.
	pg := containers.StartPostgres(t, containers.PostgresOptions{
		Image:    "postgres:15",
		Database: "lenny_src",
		User:     "lenny",
		Password: "lenny",
	})

	// Seed a known row in the source database. The backup must carry it
	// through the archive and the restore must reproduce it.
	const wantID, wantNote = 4242, "acme-backup-probe"
	mustExec(t, pg.Pool, ctx,
		`CREATE TABLE backup_probe (id integer PRIMARY KEY, note text NOT NULL)`)
	mustExec(t, pg.Pool, ctx,
		`INSERT INTO backup_probe (id, note) VALUES ($1, $2)`, wantID, wantNote)

	// A fresh scratch database is the restore target; it starts empty, so
	// a reappearing row proves the restore, not a residual table.
	mustExec(t, pg.Pool, ctx, `CREATE DATABASE lenny_scratch`)
	scratchDSN := swapDatabase(t, pg.DSN, "lenny_scratch")

	// Real MinIO with the built-in KMS enabled so the §25.11 / §12.9
	// SSE-S3 upload the MinIOUploader always applies is accepted.
	m := containers.StartMinIO(t, containers.MinIOOptions{
		Bucket: "lenny-backups",
		Env: map[string]string{
			"MINIO_KMS_SECRET_KEY": "lenny-test-key:AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=",
		},
	})

	// The §25.11 client-side AES-256-GCM data key. The archiver encrypts
	// under it; the opener decrypts under the same key.
	dataKey := make([]byte, 32)
	for i := range dataKey {
		dataKey[i] = byte(i + 1)
	}

	uploader, err := runner.NewMinIOUploader(runner.MinIOUploaderConfig{
		Client: m.Client,
		Bucket: m.Bucket,
	})
	if err != nil {
		t.Fatalf("build MinIOUploader: %v", err)
	}

	rep := &captureReporter{}
	// Run a full §25.11 backup: pg_dump the shard, tar+gzip, encrypt
	// client-side, checksum, upload to MinIO.
	res, err := runner.Run(ctx, runner.Config{
		BackupID: "bkp-t4-roundtrip",
		Mode:     runner.ModeFull,
		Dumper: &runner.ExecDumper{
			PgDumpPath: pgDump,
			ShardDSNs:  []string{pg.DSN},
		},
		Archiver: &runner.TarGzArchiver{DataKey: dataKey},
		Uploader: uploader,
		Reporter: rep,
	})
	if err != nil {
		t.Fatalf("§25.11 backup run failed: %v (reporter failure: %q)", err, rep.failed)
	}

	// The run reports the §25.11 step-8 completion: an encrypted archive
	// at the canonical path with a non-empty checksum and a postgres
	// component.
	if !res.Encrypted {
		t.Errorf("§25.11 step-5 violation: the archive was not encrypted client-side despite a data key")
	}
	if res.Checksum == "" {
		t.Errorf("§25.11 step-6 violation: the run reported no archive checksum")
	}
	if !bytes.HasPrefix([]byte(res.StoragePath), []byte("backups/full/bkp-t4-roundtrip/")) {
		t.Errorf("§25.11 step-7 violation: archive stored at %q, want the backups/full/{id}/ path", res.StoragePath)
	}
	if !hasComponent(res, "postgres") {
		t.Errorf("§25.11 step-8 violation: the completion records no postgres component: %+v", res.Components)
	}
	if rep.result.BackupID != "bkp-t4-roundtrip" {
		t.Errorf("§25.11 step-8 violation: reporter did not receive the completion; got %+v", rep.result)
	}

	// Download the archive from MinIO. SSE-S3 decrypts server-side, so the
	// bytes on the wire are the client-side ciphertext the run uploaded.
	// Its SHA-256 must equal the recorded checksum (upload integrity).
	downloaded := getObject(t, ctx, m.Client, m.Bucket, res.StoragePath)
	if got := sha256Hex(downloaded); got != res.Checksum {
		t.Fatalf("§25.11 step-6/7 violation: downloaded archive hashes to %s, recorded checksum is %s",
			got, res.Checksum)
	}

	// A wrong key must fail to open the archive: client-side encryption is
	// load-bearing, not decorative.
	if _, err := (&runner.TarGzOpener{DataKey: bytes.Repeat([]byte{0x9}, 32)}).
		ExtractPostgresDumps(ctx, downloaded); err == nil {
		t.Errorf("§25.11 step-5 violation: the archive opened under the wrong data key; encryption is not enforced")
	}

	// Open the archive under the correct key and recover the shard dumps.
	dumps, err := (&runner.TarGzOpener{DataKey: dataKey}).ExtractPostgresDumps(ctx, downloaded)
	if err != nil {
		t.Fatalf("§25.11 restore: extract postgres dumps from the archive: %v", err)
	}
	if len(dumps) != 1 {
		t.Fatalf("§25.11 restore: expected 1 shard dump, got %d", len(dumps))
	}

	// Restore the shard dump into the fresh scratch database with the
	// production ScratchRestorer (pg_restore), the §25.11 restore step.
	restorer := &runner.ExecScratchRestorer{
		PgRestorePath: pgRestore,
		PsqlPath:      psql,
		ScratchDSN:    scratchDSN,
	}
	if err := restorer.RestoreAndSmoke(ctx, dumps); err != nil {
		t.Fatalf("§25.11 restore: pg_restore into scratch failed: %v", err)
	}

	// The seeded row must reappear in the restored database.
	scratch, err := pgxpool.New(ctx, scratchDSN)
	if err != nil {
		t.Fatalf("connect to restored scratch database: %v", err)
	}
	defer scratch.Close()
	var gotNote string
	if err := scratch.QueryRow(ctx,
		`SELECT note FROM backup_probe WHERE id = $1`, wantID).Scan(&gotNote); err != nil {
		t.Fatalf("§25.11 round trip violation: seeded row absent after restore: %v", err)
	}
	if gotNote != wantNote {
		t.Errorf("§25.11 round trip violation: restored note = %q, want %q", gotNote, wantNote)
	}
}

// lookTool resolves a required CLI tool or skips the test with a precise
// diagnosis, matching the tier-4 infra-gated skip convention.
func lookTool(t *testing.T, name string) string {
	t.Helper()
	p, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("precondition not met: %s is not on PATH (%v); the §25.11 runner shells out to it, "+
			"so the round trip cannot run without it", name, err)
	}
	return p
}

// swapDatabase returns dsn with its database (path) replaced by name.
func swapDatabase(t *testing.T, dsn, name string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn %q: %v", dsn, err)
	}
	u.Path = "/" + name
	return u.String()
}

// mustExec runs a statement against the pool or fails the test.
func mustExec(t *testing.T, pool *pgxpool.Pool, ctx context.Context, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(ctx, sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// getObject reads an object from MinIO fully or fails the test.
func getObject(t *testing.T, ctx context.Context, c *minio.Client, bucket, key string) []byte {
	t.Helper()
	obj, err := c.GetObject(ctx, bucket, key, minio.GetObjectOptions{})
	if err != nil {
		t.Fatalf("GetObject %s/%s: %v", bucket, key, err)
	}
	defer obj.Close()
	data, err := io.ReadAll(obj)
	if err != nil {
		t.Fatalf("read object %s/%s: %v", bucket, key, err)
	}
	return data
}

// hasComponent reports whether the result records a component by name.
func hasComponent(res runner.Result, name string) bool {
	for _, c := range res.Components {
		if c.Name == name {
			return true
		}
	}
	return false
}

// sha256Hex returns the lowercase hex SHA-256 of data.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
