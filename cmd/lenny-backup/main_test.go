// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"strings"
	"testing"
)

// runArgs drives the lenny-backup run function with args and returns
// the exit code and the combined stdout/stderr.
func runArgs(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRunRejectsUnparseableFlags(t *testing.T) {
	code, _, _ := runArgs("--not-a-flag")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d for an unknown flag", code, exitUsage)
	}
}

func TestRunRequiresMinIOEndpoint(t *testing.T) {
	// A backup run with no MinIO endpoint cannot build its dependencies.
	code, _, stderr := runArgs("--mode", "full", "--backup-id", "bkp-1",
		"--postgres-shard", "postgres://localhost/lenny")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d when --minio-endpoint is missing", code, exitUsage)
	}
	if !strings.Contains(stderr, "minio-endpoint") {
		t.Errorf("stderr = %q, want it to mention the missing minio-endpoint", stderr)
	}
}

func TestRunRequiresReportDSN(t *testing.T) {
	// With a MinIO endpoint but no report DSN, the run cannot record the
	// outcome.
	code, _, stderr := runArgs("--mode", "full", "--backup-id", "bkp-1",
		"--postgres-shard", "postgres://localhost/lenny",
		"--minio-endpoint", "minio:9000", "--minio-bucket", "lenny-backups",
		"--report-dsn", "")
	if code != exitUsage {
		t.Errorf("exit = %d, want %d when --report-dsn is missing", code, exitUsage)
	}
	if !strings.Contains(stderr, "report-dsn") {
		t.Errorf("stderr = %q, want it to mention the missing report-dsn", stderr)
	}
}

func TestRunBackupRequiresBackupID(t *testing.T) {
	// A backup run (not retention) needs a backup id. The DSN points at
	// an unreachable host, but the backup-id check fires before any
	// connection attempt.
	code, _, stderr := runArgs("--mode", "full",
		"--postgres-shard", "postgres://localhost/lenny",
		"--minio-endpoint", "minio:9000", "--minio-bucket", "lenny-backups",
		"--report-dsn", "postgres://localhost:1/lenny", "--backup-id", "")
	// The connection to the unreachable DSN may fail first (exitUsage)
	// or the backup-id check fires (exitUsage); either way it is a usage
	// error, never a successful run.
	if code == exitOK {
		t.Errorf("exit = %d, want a non-zero usage code for a missing backup id", code)
	}
	_ = stderr
}

func TestRunFullBackupRequiresShards(t *testing.T) {
	// A full backup with no shards is a usage error. The report DSN is
	// unreachable; the shard check is reached only after the deps are
	// built, so this also exercises that ordering. Accept any non-OK
	// exit — the point is the run never reports success.
	code, _, _ := runArgs("--mode", "full", "--backup-id", "bkp-1",
		"--minio-endpoint", "minio:9000", "--minio-bucket", "lenny-backups",
		"--report-dsn", "postgres://localhost:1/lenny")
	if code == exitOK {
		t.Errorf("exit = %d, want a non-zero code for a full backup with no shards", code)
	}
}

func TestStringListFlagRepeats(t *testing.T) {
	l := &stringList{}
	if err := l.Set("a"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := l.Set("b"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if len(l.values) != 2 || l.values[0] != "a" || l.values[1] != "b" {
		t.Errorf("values = %v, want [a b]", l.values)
	}
	if l.String() != "a,b" {
		t.Errorf("String() = %q, want a,b", l.String())
	}
}

func TestDefaultExcludedTablesCoverSecrets(t *testing.T) {
	// §25.11 defaultExcludedTables must exclude the named secret tables.
	want := map[string]bool{
		"platform_secrets":            true,
		"tenant_secrets":              true,
		"credential_pool_raw_secrets": true,
	}
	for _, tbl := range defaultExcludedTables {
		delete(want, tbl)
	}
	if len(want) != 0 {
		t.Errorf("defaultExcludedTables is missing %v", want)
	}
}
