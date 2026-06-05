// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §10.3 lines 344-350. Migration 0143 creates the ca_rotation
// singleton table that durably records the CA-rotation stage so an
// operator who interrupts the procedure resumes from the recorded stage.
// The row is platform-global (one cluster CA), pinned by a constant id;
// the version column serializes concurrent stage transitions.
func TestCARotationMigration_spec_10_3(t *testing.T) {
	up := mustReadMigration(t, "0143_ca_rotation.up.sql")
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS ca_rotation",
		"id                  TEXT        PRIMARY KEY DEFAULT 'singleton'",
		"CHECK (id = 'singleton')",
		"CHECK (stage IN ('idle', 'new_ca_deployed', 'promoted', 'old_ca_retired'))",
		"current_ca_id       TEXT        NOT NULL",
		"overlap_started_at  TIMESTAMPTZ",
		"overlap_window_secs BIGINT      NOT NULL DEFAULT 0",
		"version             BIGINT      NOT NULL DEFAULT 1",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ca_rotation TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0143 up missing %q", want)
		}
	}
	assertPlatformScoped(t, "0143", up)

	down := mustReadMigration(t, "0143_ca_rotation.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS ca_rotation") {
		t.Errorf("migration 0143 down missing DROP TABLE")
	}
}
