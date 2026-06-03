// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.9 lines 3684-3691. Migration 0125 creates the
// audit_log_deferred_writes table that tracks audit events generated
// during a Postgres outage for original-timestamp-order reconciliation.
// Platform-scoped (no RLS, no tenant column); the down migration drops it.
func TestAuditLogDeferredWritesMigration_spec_25_9(t *testing.T) {
	up := mustReadMigration(t, "0125_audit_log_deferred_writes.up.sql")
	for _, want := range []string{
		"CREATE TABLE audit_log_deferred_writes",
		"id             BIGSERIAL PRIMARY KEY",
		"event_payload  JSONB NOT NULL",
		"applied_at     TIMESTAMPTZ",
		"replica_id     TEXT NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0125 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0125", up)

	down := mustReadMigration(t, "0125_audit_log_deferred_writes.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS audit_log_deferred_writes") {
		t.Error("migration 0125 down missing DROP TABLE audit_log_deferred_writes")
	}
}
