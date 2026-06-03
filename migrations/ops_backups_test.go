// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.11 lines 4165-4295. Migration 0123 creates the backup,
// schedule, retention, and restore-state durable schema. Platform-scoped
// (no RLS, no tenant column); the down migration drops all four tables.
func TestOpsBackupsMigration_spec_25_11(t *testing.T) {
	up := mustReadMigration(t, "0123_ops_backups.up.sql")
	for _, want := range []string{
		"CREATE TABLE ops_backups",
		"type            TEXT NOT NULL",
		"job_id          TEXT NOT NULL",
		"platform_version TEXT NOT NULL",
		"schema_version  INT NOT NULL",
		"CREATE TABLE ops_backup_schedule",
		"full_cron   TEXT NOT NULL DEFAULT '0 2 * * *'",
		"pg_cron     TEXT NOT NULL DEFAULT '0 */6 * * *'",
		"CREATE TABLE ops_retention_policy",
		"retain_min_full  INT NOT NULL DEFAULT 3",
		"CREATE TABLE ops_restore_state",
		"shard_states      JSONB NOT NULL",
		"pre_restore_backup_id TEXT NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0123 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0123", up)

	down := mustReadMigration(t, "0123_ops_backups.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS ops_backups",
		"DROP TABLE IF EXISTS ops_backup_schedule",
		"DROP TABLE IF EXISTS ops_retention_policy",
		"DROP TABLE IF EXISTS ops_restore_state",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0123 down missing %q", want)
		}
	}
}
