// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.1 line 797 — POST /v1/admin/pools/{name}/drain transitions a
// pool into the `draining` phase. Migration 0120 adds the draining_since
// timestamp column on sandbox_warm_pools so the phase persists; the down
// migration drops it.
func TestPoolDrainingSinceMigration_spec_15_1_797(t *testing.T) {
	b, err := FS.ReadFile("0120_pool_draining_since.up.sql")
	if err != nil {
		t.Fatalf("read migration 0120: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE sandbox_warm_pools",
		"ADD COLUMN IF NOT EXISTS draining_since TIMESTAMPTZ",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0120 up missing %q", want)
		}
	}

	down, err := FS.ReadFile("0120_pool_draining_since.down.sql")
	if err != nil {
		t.Fatalf("read migration 0120 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS draining_since") {
		t.Error("migration 0120 down must drop draining_since")
	}
}
