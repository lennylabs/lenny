// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.5 lines 313, 326 — in concurrent-workspace mode the
// "latest 2" checkpoint cap applies independently per slot, so the
// retention catalog keys rotation on (session_id, slot_id) pairs.
// Migration 0112 adds the slot_id column and re-points the rotation
// index to include it; the down migration restores the per-session
// index and drops the column.
func TestSessionCheckpointsSlotIDMigration_spec_12_5(t *testing.T) {
	b, err := FS.ReadFile("0112_session_checkpoints_slot_id.up.sql")
	if err != nil {
		t.Fatalf("read migration 0112: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE session_checkpoints",
		"ADD COLUMN slot_id TEXT NOT NULL DEFAULT ''",
		"DROP INDEX IF EXISTS idx_session_checkpoints_session_age",
		"idx_session_checkpoints_slot_age",
		"(tenant_id, session_id, slot_id, created_at DESC)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0112 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0112_session_checkpoints_slot_id.down.sql")
	if err != nil {
		t.Fatalf("read migration 0112 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_session_checkpoints_slot_age",
		"idx_session_checkpoints_session_age",
		"DROP COLUMN IF EXISTS slot_id",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0112 down missing %q", want)
		}
	}
}
