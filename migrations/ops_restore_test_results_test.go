// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.11 lines 4098, 4128-4133, 4254-4256 — migration 0136 creates the
// ops_restore_test_results record the lenny-restore-test CronJob writes one
// row to per run and the leader lenny-ops replica reads to publish the
// restore-test gauges (lenny_restore_test_success / _duration_seconds /
// _artifact_success_rate / _artifact_missing_total). F-17.3.6.
func TestOpsRestoreTestResultsMigration_spec_25_11(t *testing.T) {
	b, err := FS.ReadFile("0136_ops_restore_test_results.up.sql")
	if err != nil {
		t.Fatalf("read migration 0136: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE ops_restore_test_results",
		"id                     TEXT PRIMARY KEY",
		"backup_id              TEXT NOT NULL DEFAULT ''",
		"started_at             TIMESTAMPTZ NOT NULL",
		"completed_at           TIMESTAMPTZ NOT NULL",
		"success                BOOLEAN NOT NULL",
		"duration_ms            BIGINT NOT NULL DEFAULT 0",
		"artifact_checked       BOOLEAN NOT NULL DEFAULT false",
		"artifact_sampled       INT NOT NULL DEFAULT 0",
		"artifact_present       INT NOT NULL DEFAULT 0",
		"artifact_missing       INT NOT NULL DEFAULT 0",
		"artifact_success_rate  DOUBLE PRECISION NOT NULL DEFAULT 0",
		"ops_restore_test_results_completed_at_idx",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0136 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0136_ops_restore_test_results.down.sql")
	if err != nil {
		t.Fatalf("read migration 0136 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS ops_restore_test_results") {
		t.Error("migration 0136 down must drop the ops_restore_test_results table")
	}
}
