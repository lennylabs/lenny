// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §10.5 lines 466-540. Migration 0147 creates the runtime_upgrade
// table that durably records the per-pool RuntimeUpgrade phase so an
// operator-driven runtime image rollout survives a gateway restart and
// resumes from the recorded phase. One upgrade targets one pool, so the
// table is keyed by pool name; the version column serializes concurrent
// phase transitions across gateway replicas, and previous_pool_spec
// preserves the old pool configuration for rollback (line 507).
func TestRuntimeUpgradeMigration_spec_10_5(t *testing.T) {
	up := mustReadMigration(t, "0147_runtime_upgrade.up.sql")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS runtime_upgrade",
		"pool                          TEXT        PRIMARY KEY",
		"CHECK (phase IN ('pending', 'expanding', 'draining',",
		"new_image                     TEXT        NOT NULL",
		"previous_pool_spec            JSONB",
		"schema_version                TEXT        NOT NULL DEFAULT ''",
		"drain_first                   BOOLEAN     NOT NULL DEFAULT FALSE",
		"CHECK (canary_percent >= 0 AND canary_percent <= 100)",
		"auto_advance                  BOOLEAN     NOT NULL DEFAULT FALSE",
		"version                       BIGINT      NOT NULL DEFAULT 1",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON runtime_upgrade TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0147 up missing %q", want)
		}
	}
	// The upgrade is platform-operational state (one cluster runtime
	// catalog), not tenant-isolated — same posture as ca_rotation.
	assertPlatformScoped(t, "0147", up)

	down := mustReadMigration(t, "0147_runtime_upgrade.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS runtime_upgrade") {
		t.Errorf("migration 0147 down missing DROP TABLE")
	}
}
