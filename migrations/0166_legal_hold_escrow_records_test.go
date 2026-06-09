// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 lines 884-885 — migration 0166 creates the durable escrow
// ledger record store that marks an escrowed resource for Phase 4's
// DeleteByTenant skip logic and indexes it for the escrow-GC release path.
// The table is platform-operational (it must survive the tenant tombstone),
// so it carries no RLS policy and no lenny_tenant_guard.
func TestLegalHoldEscrowRecordsMigration_spec_12_8_884(t *testing.T) {
	up := mustReadMigration(t, "0166_legal_hold_escrow_records.up.sql")
	for _, want := range []string{
		"CREATE TABLE legal_hold_escrow_records",
		"escrow_object_key    TEXT        NOT NULL",
		"session_id           TEXT        NOT NULL DEFAULT ''",
		"artifact_uri         TEXT        NOT NULL DEFAULT ''",
		"released_at          TIMESTAMPTZ",
		"PRIMARY KEY (tenant_id, escrow_object_key)",
		"legal_hold_escrow_records_active_session",
		"legal_hold_escrow_records_active_artifact",
		"WHERE released_at IS NULL",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON legal_hold_escrow_records TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0166 up missing %q", want)
		}
	}
	// Platform-operational state survives the tenant tombstone: no RLS, no
	// tenant guard (mirrors coordination_lease / the escrow audit events).
	for _, forbidden := range []string{"ENABLE ROW LEVEL SECURITY", "CREATE POLICY", "CREATE TRIGGER"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0166 up must not contain %q (platform-scoped table)", forbidden)
		}
	}

	down := mustReadMigration(t, "0166_legal_hold_escrow_records.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS legal_hold_escrow_records") {
		t.Errorf("migration 0166 down must drop legal_hold_escrow_records")
	}
}
