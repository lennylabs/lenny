// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.11 lines 4073-4098. Migration 0126 creates the
// ops_artifact_replication_state table the §25.11 ArtifactStore
// replication Controller persists RegionState rows to. Platform-scoped
// (no RLS, no tenant column); the down migration drops the table.
func TestOpsArtifactReplicationStateMigration_spec_25_11(t *testing.T) {
	up := mustReadMigration(t, "0126_ops_artifact_replication_state.up.sql")
	for _, want := range []string{
		"CREATE TABLE ops_artifact_replication_state",
		"region                       TEXT PRIMARY KEY",
		"status                       TEXT NOT NULL",
		"suspended_since",
		"replication_lag_seconds      INT NOT NULL DEFAULT 0",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0126 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0126", up)

	down := mustReadMigration(t, "0126_ops_artifact_replication_state.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS ops_artifact_replication_state") {
		t.Errorf("migration 0126 down missing the drop")
	}
}

// spec: §25.11 lines 4145-4149. Migration 0127 adds the restore Job
// correlation column and the legal-hold ledger confirmation watermark to
// ops_restore_state (added to the RestoreState row after 0123 shipped).
func TestOpsRestoreStateCompletionMigration_spec_25_11(t *testing.T) {
	up := mustReadMigration(t, "0127_ops_restore_state_completion.up.sql")
	for _, want := range []string{
		"ALTER TABLE ops_restore_state",
		"ADD COLUMN job_id",
		"ADD COLUMN ledger_confirmed_at",
		"ADD COLUMN ledger_confirmed_by",
		"ADD COLUMN ledger_confirmed_justification",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0127 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0127", up)

	down := mustReadMigration(t, "0127_ops_restore_state_completion.down.sql")
	for _, want := range []string{
		"DROP COLUMN IF EXISTS job_id",
		"DROP COLUMN IF EXISTS ledger_confirmed_justification",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0127 down missing %q", want)
		}
	}
}
