// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §8.6 lines 730-733; §8.6 line 643.
// Migration 0130 adds the §8.6 lease-extension grant counters to
// delegation_tree_budget so the Postgres-backed leasecontrol.DenialStore
// has a durable budget counter to increment inside the same row-lock
// transaction that re-checks the extension-denied flag (the §8.6 line 732
// in-flight atomic check). The down migration drops the columns. F-8.6.5.
func TestDelegationTreeBudgetExtensionDeltasMigration_spec_8_6(t *testing.T) {
	b, err := FS.ReadFile("0130_delegation_tree_budget_extension_deltas.up.sql")
	if err != nil {
		t.Fatalf("read migration 0130: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE delegation_tree_budget",
		"ADD COLUMN ext_tokens            BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_seconds           BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_children          BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_parallel_children BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_tree_size         BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_file_export_files BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN ext_file_export_bytes BIGINT      NOT NULL DEFAULT 0",
		"ADD COLUMN updated_at            TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0130 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0130_delegation_tree_budget_extension_deltas.down.sql")
	if err != nil {
		t.Fatalf("read migration 0130 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS ext_tokens",
		"DROP COLUMN IF EXISTS ext_file_export_bytes",
		"DROP COLUMN IF EXISTS updated_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0130 down missing %q", want)
		}
	}
}
