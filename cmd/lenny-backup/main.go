// SPDX-License-Identifier: MIT

// Command lenny-backup performs one §25.11 backup run. lenny-ops does
// not run backups in-process; it creates a Kubernetes Job from the
// lenny-backup image, and that Job invokes this binary. A run dumps the
// Postgres shards, exports the platform configuration and CRDs,
// packages and encrypts a tar archive, uploads it to the MinIO backup
// bucket, applies the retention policy, and exits.
//
// The same image is used for the §25.11 retention-enforcement cron Job
// and the restore-test verification Job; the --mode flag selects the
// run.
//
// Usage:
//
//	lenny-backup --mode full --backup-id bkp-abc \
//	  --postgres-shard $DSN1 --postgres-shard $DSN2 \
//	  --minio-endpoint minio:9000 --minio-bucket lenny-backups \
//	  --report-dsn $LENNY_OPS_POSTGRES_DSN
//
//	lenny-backup --mode retention --report-dsn $LENNY_OPS_POSTGRES_DSN \
//	  --minio-endpoint minio:9000 --minio-bucket lenny-backups
//
// The Postgres connection strings, the MinIO credentials, and the KMS
// data key come from the Job's mounted Secrets (lenny-backup-postgres,
// lenny-backup-minio). lenny-backup does not resolve KMS itself: the
// --data-key-file flag points at the unwrapped AES-256 data key the Job
// init container (or an envelope-encryption sidecar) materialized.
//
// Exit codes:
//
//	0   the run succeeded
//	1   the run failed (a backup step failed; the ops_backups row is
//	    marked failed)
//	2   invalid invocation (a flag is missing or malformed)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/lennylabs/lenny/pkg/ops/backup/runner"
)

const (
	exitOK      = 0
	exitFailure = 1
	exitUsage   = 2
)

// defaultExcludedTables is the §25.11 contentPolicy.defaultExcludedTables
// list: tables excluded from pg_dump unless includeSensitiveTables is
// set. The pattern-matched %_secret% / %_token% tables are expanded by
// the operator into --exclude-tables; this list carries the named ones.
var defaultExcludedTables = []string{
	"platform_secrets",
	"tenant_secrets",
	"credential_pool_raw_secrets",
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one lenny-backup invocation and returns a process exit
// code. It is split out from main so a test drives it directly.
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("lenny-backup", flag.ContinueOnError)
	fs.SetOutput(stderr)

	mode := fs.String("mode", "full",
		"run mode: full, postgres, config, retention, verify, or restore-test")
	backupID := fs.String("backup-id", os.Getenv("LENNY_BACKUP_ID"),
		"the ops_backups row id this run writes to (required for a backup or verify run)")
	shardList := newStringList(fs, "postgres-shard",
		"a Postgres shard connection string; repeat once per shard")
	excludeList := newStringList(fs, "exclude-table",
		"a table excluded from pg_dump beyond the §25.11 defaults; repeat per table")
	redactList := newStringList(fs, "redact-column",
		"a §25.11 contentPolicy.redactColumns entry (bare column or table.column); "+
			"its data is rewritten to [REDACTED] in a plain-format dump; repeat per column")
	includeSensitive := fs.Bool("include-sensitive-tables", false,
		"include the §25.11 defaultExcludedTables in the dump (not recommended)")
	minioEndpoint := fs.String("minio-endpoint", os.Getenv("LENNY_BACKUP_MINIO_ENDPOINT"),
		"the MinIO endpoint for the backup bucket (host:port)")
	minioBucket := fs.String("minio-bucket", os.Getenv("LENNY_BACKUP_MINIO_BUCKET"),
		"the §25.11 backup bucket")
	minioAccessKey := fs.String("minio-access-key", os.Getenv("LENNY_BACKUP_MINIO_ACCESS_KEY"),
		"the lenny-backup MinIO access key")
	minioSecretKey := fs.String("minio-secret-key", os.Getenv("LENNY_BACKUP_MINIO_SECRET_KEY"),
		"the lenny-backup MinIO secret key")
	minioTLS := fs.Bool("minio-tls", true, "connect to MinIO over TLS")
	kmsKeyID := fs.String("kms-key-id", os.Getenv("LENNY_BACKUP_KMS_KEY_ID"),
		"the §12.9 SSE-KMS key for the MinIO upload; empty selects SSE-S3")
	dataKeyFile := fs.String("data-key-file", os.Getenv("LENNY_BACKUP_DATA_KEY_FILE"),
		"path to the unwrapped 32-byte AES-256 data key for §25.11 client-side encryption; "+
			"empty disables client-side encryption and relies on SSE")
	reportDSN := fs.String("report-dsn", os.Getenv("LENNY_OPS_POSTGRES_DSN"),
		"the lenny-ops Postgres DSN for the §25.11 step-8 ops_backups update")
	pgDumpPath := fs.String("pg-dump-path", "pg_dump", "the pg_dump executable")
	pgRestorePath := fs.String("pg-restore-path", "pg_restore",
		"the pg_restore executable used by the verify and restore-test modes")
	psqlPath := fs.String("psql-path", "psql",
		"the psql executable used to restore §25.11 redacted (plain-format) dumps in restore-test mode")
	preRestoreRetainDays := fs.Int("pre-restore-retain-days", 7,
		"the §25.11 backups.retention.preRestoreRetainDays window")
	timeout := fs.Duration("timeout", 2*time.Hour,
		"hard deadline for the run, matching the Job activeDeadlineSeconds")

	// §25.11 verify and restore-test flags.
	namespace := fs.String("namespace", os.Getenv("LENNY_NAMESPACE"),
		"the release namespace the restore-test Job runs in (used for the scratch namespace name)")
	backupSelector := fs.String("backup-selector", os.Getenv("LENNY_BACKUP_SELECTOR"),
		"the §25.11 restore-test backup type selector; the latest matching backup is restored (empty = any type)")
	artifactSampleSize := fs.Int("artifact-sample-size", 100,
		"the §25.11 number of ArtifactStore object keys the restore-test sampled-HEAD check probes")
	scratchDSN := fs.String("scratch-dsn", os.Getenv("LENNY_BACKUP_SCRATCH_DSN"),
		"the scratch Postgres DSN the restore-test restores into; empty verifies readability only")
	jobName := fs.String("job-name", os.Getenv("HOSTNAME"),
		"the restore-test result row id (defaults to the pod HOSTNAME)")
	replicationEndpoint := fs.String("replication-endpoint", os.Getenv("LENNY_BACKUP_REPLICATION_ENDPOINT"),
		"the off-cluster ArtifactStore replication-target MinIO endpoint for the §25.11 sampled-HEAD check; empty skips it")
	replicationBucket := fs.String("replication-bucket", os.Getenv("LENNY_BACKUP_REPLICATION_BUCKET"),
		"the replication-target bucket the sampled-HEAD check probes")
	replicationAccessKey := fs.String("replication-access-key", os.Getenv("LENNY_BACKUP_REPLICATION_ACCESS_KEY"),
		"the replication-target MinIO access key")
	replicationSecretKey := fs.String("replication-secret-key", os.Getenv("LENNY_BACKUP_REPLICATION_SECRET_KEY"),
		"the replication-target MinIO secret key")
	replicationTLS := fs.Bool("replication-tls", true, "connect to the replication target over TLS")

	fs.Usage = func() {
		fmt.Fprintln(stderr,
			"usage: lenny-backup --mode <full|postgres|config|retention|verify|restore-test> [flags]")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}

	// The run is bounded by --timeout and cancelled on SIGTERM so a
	// Job-level deletion drains the run cleanly.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	deps, err := resolveDeps(ctx, depsInput{
		minioEndpoint:  *minioEndpoint,
		minioBucket:    *minioBucket,
		minioAccessKey: *minioAccessKey,
		minioSecretKey: *minioSecretKey,
		minioTLS:       *minioTLS,
		kmsKeyID:       *kmsKeyID,
		dataKeyFile:    *dataKeyFile,
		reportDSN:      *reportDSN,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: %v\n", err)
		return exitUsage
	}
	defer deps.close()

	// Retention-only mode: the §25.11 daily 03:30 UTC cron Job runs
	// retention enforcement without a backup.
	if *mode == "retention" {
		pruned, err := deps.reporter.RunRetention(ctx, deps.uploader, *preRestoreRetainDays)
		if err != nil {
			fmt.Fprintf(stderr, "lenny-backup: retention enforcement: %v\n", err)
			return exitFailure
		}
		fmt.Fprintf(stdout, "lenny-backup: retention enforcement pruned %d backups: %s\n",
			len(pruned), strings.Join(pruned, ", "))
		return exitOK
	}

	// §25.11 Backup Verification: download, checksum, pg_restore --list.
	if *mode == "verify" {
		return runVerify(ctx, deps, *backupID, *pgRestorePath, stdout, stderr)
	}

	// §25.11 Test Restore: the monthly lenny-restore-test CronJob. It
	// selects the latest backup matching the selector, verifies it,
	// restores it into the scratch Postgres, samples the ArtifactStore
	// replication target, and records the outcome for the lenny-ops
	// restore-test gauges.
	if *mode == "restore-test" {
		_ = *namespace // the scratch namespace is provisioned by the Job; the binary only logs it
		if *namespace != "" {
			fmt.Fprintf(stdout, "lenny-backup: restore-test for namespace %s\n", *namespace)
		}
		return runRestoreTest(ctx, deps, restoreTestParams{
			jobName:       *jobName,
			selector:      *backupSelector,
			sampleSize:    *artifactSampleSize,
			scratchDSN:    *scratchDSN,
			pgRestorePath: *pgRestorePath,
			psqlPath:      *psqlPath,
			replication: replicationConfig{
				endpoint:  *replicationEndpoint,
				bucket:    *replicationBucket,
				accessKey: *replicationAccessKey,
				secretKey: *replicationSecretKey,
				useTLS:    *replicationTLS,
			},
		}, stdout, stderr)
	}

	// A backup run.
	runMode := runner.Mode(*mode)
	if *backupID == "" {
		fmt.Fprintln(stderr, "lenny-backup: --backup-id is required for a backup run")
		return exitUsage
	}
	if runMode == runner.ModeFull || runMode == runner.ModePostgres {
		if len(shardList.values) == 0 {
			fmt.Fprintln(stderr, "lenny-backup: --postgres-shard is required for a full or postgres backup")
			return exitUsage
		}
	}

	excluded := append([]string(nil), excludeList.values...)
	if !*includeSensitive {
		excluded = append(excluded, defaultExcludedTables...)
	}

	dumper := &runner.ExecDumper{
		PgDumpPath:    *pgDumpPath,
		ShardDSNs:     shardList.values,
		ExcludeTables: excluded,
		RedactColumns: redactList.values,
		ConfigExport:  deps.configExport,
		CRDExport:     deps.crdExport,
	}
	archiver := &runner.TarGzArchiver{
		DataKey:         deps.dataKey,
		ExcludedTables:  excluded,
		RedactedColumns: redactList.values,
	}

	result, err := runner.Run(ctx, runner.Config{
		BackupID:             *backupID,
		Mode:                 runMode,
		Dumper:               dumper,
		Archiver:             archiver,
		Uploader:             deps.uploader,
		Pruner:               deps.uploader,
		Reporter:             deps.reporter,
		RetentionStore:       deps.reporter,
		PreRestoreRetainDays: *preRestoreRetainDays,
	})
	if err != nil {
		fmt.Fprintf(stderr, "lenny-backup: backup %s failed: %v\n", *backupID, err)
		return exitFailure
	}
	fmt.Fprintf(stdout,
		"lenny-backup: backup %s completed: %d bytes, checksum %s, stored at %s (encrypted=%t)\n",
		result.BackupID, result.SizeBytes, result.Checksum, result.StoragePath, result.Encrypted)
	if len(result.Pruned) > 0 {
		fmt.Fprintf(stdout, "lenny-backup: retention enforcement pruned %d backups\n", len(result.Pruned))
	}
	return exitOK
}

// minioClient builds a MinIO client for the backup bucket from the
// resolved credentials.
func minioClient(endpoint, accessKey, secretKey string, useTLS bool) (*minio.Client, error) {
	if endpoint == "" {
		return nil, errors.New("--minio-endpoint is required")
	}
	return minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useTLS,
	})
}

// stringList is a repeatable string flag: each occurrence appends a
// value. It backs --postgres-shard and --exclude-table.
type stringList struct {
	values []string
}

// newStringList registers a repeatable string flag on fs.
func newStringList(fs *flag.FlagSet, name, usage string) *stringList {
	l := &stringList{}
	fs.Var(l, name, usage)
	return l
}

// String implements flag.Value.
func (l *stringList) String() string { return strings.Join(l.values, ",") }

// Set implements flag.Value: it appends one value.
func (l *stringList) Set(v string) error {
	l.values = append(l.values, v)
	return nil
}
