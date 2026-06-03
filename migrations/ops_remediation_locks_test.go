// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.4 lines 2160-2253. Migration 0121 creates the remediation
// lock coordination trio — ops_remediation_locks (Tier 1 acquire table
// with the unique(scope) compare-and-set primitive), ops_lock_epoch
// (the dual-store outage-epoch singleton), and ops_lock_conflicts (the
// split-brain audit log). All three are platform-scoped (no RLS, no
// tenant column); the down migration drops all three.
func TestOpsRemediationLocksMigration_spec_25_4(t *testing.T) {
	up := mustReadMigration(t, "0121_ops_remediation_locks.up.sql")
	for _, want := range []string{
		"CREATE TABLE ops_remediation_locks",
		"scope        TEXT NOT NULL",
		"epoch        BIGINT NOT NULL",
		"revision     BIGINT NOT NULL DEFAULT 0",
		"CONSTRAINT unique_active_scope UNIQUE (scope)",
		"CREATE TABLE ops_lock_epoch",
		"current   BIGINT NOT NULL DEFAULT 0",
		"CREATE TABLE ops_lock_conflicts",
		"pre_outage_lock    JSONB NOT NULL",
		"post_outage_lock   JSONB NOT NULL",
		"winner             TEXT NOT NULL",
		"loser_was_active   BOOLEAN NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0121 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0121", up)

	down := mustReadMigration(t, "0121_ops_remediation_locks.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS ops_remediation_locks",
		"DROP TABLE IF EXISTS ops_lock_epoch",
		"DROP TABLE IF EXISTS ops_lock_conflicts",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0121 down missing %q", want)
		}
	}
}
