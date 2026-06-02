// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.10 lines 3811-3820. Migration 0117 creates the
// bootstrap_seed_snapshot table — the §25.10 durable desired-state store
// the configuration-drift detector compares running state against. The
// id column is CHECK-constrained to 'live'/'target' so no third
// desired-state row can exist (F-25.10.13); desired_state is JSONB. It
// is platform-scoped (no tenant column, no RLS); the down migration
// drops the table.
func TestBootstrapSeedSnapshotMigration_spec_25_10(t *testing.T) {
	b, err := FS.ReadFile("0117_bootstrap_seed_snapshot.up.sql")
	if err != nil {
		t.Fatalf("read migration 0117: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"CREATE TABLE bootstrap_seed_snapshot",
		"id            TEXT        PRIMARY KEY DEFAULT 'live'",
		"CHECK (id IN ('live', 'target'))",
		"desired_state JSONB       NOT NULL",
		"source        TEXT        NOT NULL",
		"upgrade_id    TEXT",
		"written_at    TIMESTAMPTZ NOT NULL DEFAULT now()",
		"written_by    TEXT        NOT NULL",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0117 up missing %q", want)
		}
	}
	// Platform-scoped: no RLS / tenant-guard apparatus.
	for _, forbidden := range []string{"ROW LEVEL SECURITY", "lenny_tenant_guard", "tenant_id"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0117 up should be platform-scoped but contains %q", forbidden)
		}
	}

	b, err = FS.ReadFile("0117_bootstrap_seed_snapshot.down.sql")
	if err != nil {
		t.Fatalf("read migration 0117 down: %v", err)
	}
	if down := string(b); !strings.Contains(down, "DROP TABLE IF EXISTS bootstrap_seed_snapshot") {
		t.Errorf("migration 0117 down missing DROP TABLE bootstrap_seed_snapshot")
	}
}
