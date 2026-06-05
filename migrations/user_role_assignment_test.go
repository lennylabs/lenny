// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.1 lines 826-828 — the platform-managed role assignment for a
// user within a tenant. Migration 0146 adds the role_assigned presence
// flag (controlling the §10.2 OIDC override) plus the role_assigned_by /
// role_assigned_at provenance columns surfaced by GET
// /v1/admin/tenants/{id}/users. F-15.1.3.
func TestUserRoleAssignmentMigration_spec_15_1_826(t *testing.T) {
	b, err := FS.ReadFile("0146_user_role_assignment.up.sql")
	if err != nil {
		t.Fatalf("read migration 0146: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"role_assigned    BOOLEAN     NOT NULL DEFAULT TRUE",
		"role_assigned_by TEXT        NOT NULL DEFAULT ''",
		"role_assigned_at TIMESTAMPTZ",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0146 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0146_user_role_assignment.down.sql")
	if err != nil {
		t.Fatalf("read migration 0146 down: %v", err)
	}
	for _, want := range []string{
		"DROP COLUMN IF EXISTS role_assigned",
		"DROP COLUMN IF EXISTS role_assigned_by",
		"DROP COLUMN IF EXISTS role_assigned_at",
	} {
		if !strings.Contains(string(down), want) {
			t.Errorf("migration 0146 down missing %q", want)
		}
	}
}
