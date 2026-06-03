// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.8 lines 3579-3605. Migration 0124 creates the
// platform_upgrade_state orchestration singleton and the
// platform_upgrade_check_cache release-channel cache. Platform-scoped
// (no RLS, no tenant column); the down migration drops both.
func TestPlatformUpgradeMigration_spec_25_8(t *testing.T) {
	up := mustReadMigration(t, "0124_platform_upgrade.up.sql")
	for _, want := range []string{
		"CREATE TABLE platform_upgrade_state",
		"target_images         JSONB NOT NULL",
		"previous_phases       TEXT[] NOT NULL DEFAULT '{}'",
		"pre_upgrade_backup_id TEXT",
		"metadata              JSONB NOT NULL DEFAULT '{}'",
		"CREATE TABLE platform_upgrade_check_cache",
		"ttl_seconds     INT NOT NULL DEFAULT 21600",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0124 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0124", up)

	down := mustReadMigration(t, "0124_platform_upgrade.down.sql")
	for _, want := range []string{
		"DROP TABLE IF EXISTS platform_upgrade_state",
		"DROP TABLE IF EXISTS platform_upgrade_check_cache",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0124 down missing %q", want)
		}
	}
}
