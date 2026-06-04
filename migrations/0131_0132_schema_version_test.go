// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.5 item 7 and §15.4.1 line 1694. Migration 0131 adds the
// query-filterable schema_version column the spec mandates on every
// MessageEnvelope persisted to session_messages; the down migration drops
// it.
func TestSessionMessagesSchemaVersionMigration_spec_15_4_1_1694(t *testing.T) {
	up := mustReadMigration(t, "0131_session_messages_schema_version.up.sql")
	for _, want := range []string{
		"ALTER TABLE session_messages",
		"ADD COLUMN schema_version INT NOT NULL DEFAULT 1",
		"CHECK (schema_version >= 1)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0131 up missing %q", want)
		}
	}

	down := mustReadMigration(t, "0131_session_messages_schema_version.down.sql")
	if !strings.Contains(down, "DROP COLUMN IF EXISTS schema_version") {
		t.Errorf("migration 0131 down missing schema_version DROP")
	}
}

// spec: §15.5 item 7 (checkpoint metadata). Migration 0132 adds the
// schema_version column to the session_checkpoints checkpoint-metadata
// catalog; the down migration drops it.
func TestSessionCheckpointsSchemaVersionMigration_spec_15_5_item7(t *testing.T) {
	up := mustReadMigration(t, "0132_session_checkpoints_schema_version.up.sql")
	for _, want := range []string{
		"ALTER TABLE session_checkpoints",
		"ADD COLUMN schema_version INT NOT NULL DEFAULT 1",
		"CHECK (schema_version >= 1)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0132 up missing %q", want)
		}
	}

	down := mustReadMigration(t, "0132_session_checkpoints_schema_version.down.sql")
	if !strings.Contains(down, "DROP COLUMN IF EXISTS schema_version") {
		t.Errorf("migration 0132 down missing schema_version DROP")
	}
}
