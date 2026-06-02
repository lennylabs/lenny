// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §11.2 lines 29, 48; §12.4 lines 193, 218; §8.6 lines 730-733.
// Migration 0115 creates the delegation_tree_budget table — the durable
// Postgres checkpoint of the §8.2 Redis dlg:* tree-budget counters that
// the §11.2 line 48 two-source reconstruction restores via the MAX rule
// on Redis recovery. The table carries the standard §12.3 tenant-guard
// trigger and RLS policy; the down migration drops the table (and with
// it the trigger, policy, and index).
func TestDelegationTreeBudgetMigration_spec_11_2(t *testing.T) {
	b, err := FS.ReadFile("0115_delegation_tree_budget.up.sql")
	if err != nil {
		t.Fatalf("read migration 0115: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"CREATE TABLE delegation_tree_budget",
		"tenant_id             TEXT        NOT NULL REFERENCES tenants(id)",
		"root_session_id       TEXT        NOT NULL",
		"tree_size             BIGINT      NOT NULL DEFAULT 0",
		"token_budget_consumed BIGINT      NOT NULL DEFAULT 0",
		"tree_memory_bytes     BIGINT      NOT NULL DEFAULT 0",
		"extension_denied      BOOLEAN     NOT NULL DEFAULT FALSE",
		"cool_off_expiry       TIMESTAMPTZ",
		"checkpoint_at         TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()",
		"PRIMARY KEY (tenant_id, root_session_id)",
		"idx_delegation_tree_budget_checkpoint",
		"CREATE TRIGGER lenny_tenant_guard",
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"CREATE POLICY lenny_tenant_isolation ON delegation_tree_budget",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON delegation_tree_budget TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0115 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0115_delegation_tree_budget.down.sql")
	if err != nil {
		t.Fatalf("read migration 0115 down: %v", err)
	}
	if down := string(b); !strings.Contains(down, "DROP TABLE IF EXISTS delegation_tree_budget") {
		t.Errorf("migration 0115 down missing DROP TABLE delegation_tree_budget")
	}
}
