// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 line 865 — each tenant carries a TenantState enum
// (active/disabling/deleting/deleted) persisted in Postgres. Migration
// 0105 must add the state column with the four-value CHECK constraint
// and an 'active' default, and reconcile any pre-existing soft-deleted
// row to the 'deleted' tombstone state. F-12.8.12.
func TestTenantStateMigration_spec_12_8_865(t *testing.T) {
	b, err := FS.ReadFile("0105_tenant_state.up.sql")
	if err != nil {
		t.Fatalf("read migration 0105: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"ADD COLUMN state TEXT NOT NULL DEFAULT 'active'",
		"CHECK (state IN ('active', 'disabling', 'deleting', 'deleted'))",
		"UPDATE tenants SET state = 'deleted' WHERE deleted_at IS NOT NULL",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0105 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0105_tenant_state.down.sql")
	if err != nil {
		t.Fatalf("read migration 0105 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS state") {
		t.Error("migration 0105 down must drop the state column")
	}
}
