// SPDX-License-Identifier: MIT

package migrations_test

import (
	"strings"
	"testing"
)

// TestCheckpointManifestMigrationSQL asserts the static SQL surface of
// migration 0175: the up drops the migration 0062
// session_partial_checkpoint_manifest table and the migration 0150
// partial unique index, then creates checkpoint_manifest with the full
// §10.1 lines 141-151 column set, the partial_manifest_active_uniq index
// scoped to (session_id, slot_id), the lenny_tenant_guard trigger,
// ENABLE/FORCE ROW LEVEL SECURITY with the lenny_tenant_isolation
// policy, and the lenny_app grants; the down recreates the 0062 table
// and the 0150 index.
//
// spec: §10.1 lines 141-151 (manifest column set and the
// (session_id, slot_id) partial unique index), §12.3 (RLS apparatus).
func TestCheckpointManifestMigrationSQL_spec_10_1_12_3(t *testing.T) {
	up := readMigration0173(t, "0175_checkpoint_manifest.up.sql")

	// The up must drop the superseded 0062 table and the 0150 index.
	for _, want := range []string{
		"DROP TABLE IF EXISTS session_partial_checkpoint_manifest",
		"DROP INDEX IF EXISTS partial_manifest_active_uniq",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0175 up missing supersede statement %q", want)
		}
	}

	if !strings.Contains(up, "CREATE TABLE checkpoint_manifest") {
		t.Fatalf("migration 0175 up must CREATE TABLE checkpoint_manifest")
	}

	// The full §10.1 lines 141-151 column set.
	for _, col := range []string{
		"checkpoint_id",
		"tenant_id",
		"session_id",
		"slot_id",
		"coordination_generation",
		"recovery_generation",
		"partial",
		"manifest_reason",
		"chunk_object_key_prefix",
		"chunk_size_bytes",
		"chunk_encoding",
		"chunk_count",
		"workspace_bytes_uploaded",
		"reserved_bytes",
		"reservation_released_at",
		"baseline_full_checkpoint_bytes",
		"checkpoint_started_at",
		"checkpoint_timeout_at",
		"created_at",
		"deleted_at",
	} {
		if !strings.Contains(up, col) {
			t.Errorf("migration 0175 up missing checkpoint_manifest column %q", col)
		}
	}

	// manifest_reason is NOT NULL and defaults to the in_progress
	// intent-row disposition (§10.1 line 141).
	if !strings.Contains(up, "manifest_reason") ||
		!strings.Contains(up, "NOT NULL DEFAULT 'in_progress'") {
		t.Errorf("migration 0175 up must declare manifest_reason NOT NULL DEFAULT 'in_progress'")
	}

	// baseline_full_checkpoint_bytes is BIGINT NULL with no DEFAULT so the
	// §10.1 line 155 IS NULL branch stays reachable and the §7.2 resume
	// path never divides by zero.
	if !strings.Contains(up, "baseline_full_checkpoint_bytes BIGINT      NULL") {
		t.Errorf("migration 0175 up must declare baseline_full_checkpoint_bytes BIGINT NULL")
	}

	// The primary key leads with tenant_id per §12.3 R-01.
	if !strings.Contains(up, "PRIMARY KEY (tenant_id, checkpoint_id)") {
		t.Errorf("migration 0175 up must key checkpoint_manifest on (tenant_id, checkpoint_id)")
	}

	// The partial unique index is scoped to (session_id, slot_id) over
	// active partial rows (§10.1 lines 143-151).
	normUp := strings.Join(strings.Fields(up), " ")
	if !strings.Contains(normUp,
		"CREATE UNIQUE INDEX partial_manifest_active_uniq ON checkpoint_manifest (session_id, slot_id) WHERE partial = TRUE AND deleted_at IS NULL") {
		t.Errorf("migration 0175 up must scope partial_manifest_active_uniq to (session_id, slot_id) WHERE partial = TRUE AND deleted_at IS NULL")
	}

	// The §12.3 tenant-isolation apparatus mirrors every tenant-scoped
	// table: the guard trigger, FORCE RLS, the policy, and lenny_app
	// grants.
	for _, want := range []string{
		"CREATE TRIGGER lenny_tenant_guard",
		"ALTER TABLE checkpoint_manifest ENABLE ROW LEVEL SECURITY",
		"ALTER TABLE checkpoint_manifest FORCE ROW LEVEL SECURITY",
		"CREATE POLICY lenny_tenant_isolation ON checkpoint_manifest",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON checkpoint_manifest TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0175 up missing §12.3 apparatus %q", want)
		}
	}

	down := readMigration0173(t, "0175_checkpoint_manifest.down.sql")
	// The down drops the new table and recreates the 0062 table and the
	// 0150 index it superseded.
	for _, want := range []string{
		"DROP TABLE IF EXISTS checkpoint_manifest",
		"CREATE TABLE session_partial_checkpoint_manifest",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0175 down missing %q", want)
		}
	}
	normDown := strings.Join(strings.Fields(down), " ")
	if !strings.Contains(normDown,
		"CREATE UNIQUE INDEX partial_manifest_active_uniq ON session_partial_checkpoint_manifest (tenant_id, session_id) WHERE deleted_at IS NULL") {
		t.Errorf("migration 0175 down must recreate the 0150 index scoped to (tenant_id, session_id)")
	}
}
