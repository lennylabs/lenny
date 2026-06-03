// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.2 lines 393-394. Migration 0128 creates the
// ops_operation_baselines table backing the canonical Progress
// Envelope's historical_p50 ETA method. It is platform-scoped (no RLS,
// no tenant column); the down migration drops it.
func TestOpsOperationBaselinesMigration_spec_25_2(t *testing.T) {
	up := mustReadMigration(t, "0128_ops_operation_baselines.up.sql")
	for _, want := range []string{
		"CREATE TABLE ops_operation_baselines",
		"kind            TEXT PRIMARY KEY",
		"p50_duration_ms BIGINT NOT NULL",
		"p90_duration_ms BIGINT NOT NULL",
		"sample_size     BIGINT NOT NULL",
		"last_updated    TIMESTAMPTZ NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0128 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0128", up)

	down := mustReadMigration(t, "0128_ops_operation_baselines.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS ops_operation_baselines") {
		t.Errorf("migration 0128 down missing drop")
	}
}
