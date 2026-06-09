// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 lines 880-889 — the force-delete legal-hold override must
// be durable on the tenant row so the tenant-deletion controller (which
// reconstructs its job from the persisted state after a restart) still
// segregates held evidence on the override path. Migration 0162 adds the
// four override columns. F-12.8.2, F-24.10.2.
func TestTenantForceDeleteOverrideMigration_spec_12_8_880(t *testing.T) {
	b, err := FS.ReadFile("0162_tenant_force_delete_override.up.sql")
	if err != nil {
		t.Fatalf("read migration 0162: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"ADD COLUMN force_delete_hold_override BOOLEAN NOT NULL DEFAULT false",
		"ADD COLUMN force_delete_justification TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN force_delete_by TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN force_delete_at TIMESTAMPTZ",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0162 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0162_tenant_force_delete_override.down.sql")
	if err != nil {
		t.Fatalf("read migration 0162 down: %v", err)
	}
	for _, want := range []string{
		"DROP COLUMN IF EXISTS force_delete_hold_override",
		"DROP COLUMN IF EXISTS force_delete_at",
	} {
		if !strings.Contains(string(down), want) {
			t.Errorf("migration 0162 down missing %q", want)
		}
	}
}
