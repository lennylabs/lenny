// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §10.1 lines 165-166, 393. Migration 0148 creates the
// session_checkpoint_meta table the gateway writes after receiving a
// CheckpointBarrierAck during graceful drain. The row persists the
// barrier_id, the checkpoint_ref the barrier flush produced, and the
// workspace_recovery_fraction the coordinator-handoff session.resumed
// event reports. One row per session, upserted on every barrier, so
// the PK is (tenant_id, session_id). The table is tenant-scoped and
// carries the standard RLS posture.
func TestSessionCheckpointMetaMigration_spec_10_1(t *testing.T) {
	up := mustReadMigration(t, "0148_session_checkpoint_meta.up.sql")
	for _, want := range []string{
		"CREATE TABLE session_checkpoint_meta",
		"tenant_id                   TEXT        NOT NULL REFERENCES tenants(id)",
		"session_id                  TEXT        NOT NULL",
		"coordination_generation     BIGINT      NOT NULL DEFAULT 0",
		"barrier_id                  TEXT        NOT NULL DEFAULT ''",
		"checkpoint_ref              TEXT        NOT NULL DEFAULT ''",
		"workspace_recovery_fraction DOUBLE PRECISION",
		"PRIMARY KEY (tenant_id, session_id)",
		// Tenant-scoped RLS apparatus.
		"CREATE TRIGGER lenny_tenant_guard",
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"current_setting('app.current_tenant', false)",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON session_checkpoint_meta TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0148 up missing %q", want)
		}
	}

	down := mustReadMigration(t, "0148_session_checkpoint_meta.down.sql")
	for _, want := range []string{
		"DROP POLICY IF EXISTS lenny_tenant_isolation ON session_checkpoint_meta",
		"DROP TRIGGER IF EXISTS lenny_tenant_guard ON session_checkpoint_meta",
		"DROP TABLE IF EXISTS session_checkpoint_meta",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0148 down missing %q", want)
		}
	}
}
