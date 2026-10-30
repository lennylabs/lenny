// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// TestSessionCheckpointsSlotIDMigration asserts the frozen SQL of migration
// 0112 as it landed: it added the slot_id column to session_checkpoints and
// re-pointed the rotation index onto the (session_id, slot_id) pair, because
// the rule of the day applied the "latest 2" checkpoint cap independently per
// slot; the down migration restored the per-session index and dropped the
// column. That pair is history. Migration 0180 dropped the column and re-keyed
// the rotation index on session_id, which is the key the retention rule states
// today. Migration 0112's own text is frozen, and the drop leaves it unchanged,
// so this test asserts it as written.
//
// spec: §12.5 (checkpoint retention and rotation, keyed on session_id since
// migration 0180 retired the slot column)
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
