// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §24.13 line 150 — the expand-contract phase-tracking table that
// backs `GET /v1/admin/schema/migrations/status`. Migration 0134 creates
// schema_migration_phase with the spec-named columns (`version`, `phase`,
// `appliedAt`, `gateCheckResult`, `migrationJobName`); the migration Job
// UPSERTs a row per applied version so the status surface reports the
// real applied-at timestamp, Job name, and Phase 3 gate result. F-24.13.4.
func TestSchemaMigrationPhaseMigration_spec_24_13_150(t *testing.T) {
	b, err := FS.ReadFile("0134_schema_migration_phase.up.sql")
	if err != nil {
		t.Fatalf("read migration 0134: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE schema_migration_phase",
		"version            BIGINT      PRIMARY KEY",
		"phase              TEXT        NOT NULL",
		"applied_at         TIMESTAMPTZ NOT NULL DEFAULT now()",
		"gate_check_result  TEXT        NOT NULL DEFAULT 'not_run'",
		"migration_job_name TEXT",
		"updated_at         TIMESTAMPTZ NOT NULL DEFAULT now()",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0134 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0134_schema_migration_phase.down.sql")
	if err != nil {
		t.Fatalf("read migration 0134 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS schema_migration_phase") {
		t.Error("migration 0134 down must drop the schema_migration_phase table")
	}
}
