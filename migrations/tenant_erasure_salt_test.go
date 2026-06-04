// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.8 lines 843-850 — the per-tenant billing-pseudonymization
// `erasure_salt` is stored in the tenant configuration record in
// Postgres, KMS-envelope-encrypted at rest and never in plaintext.
// Migration 0133 adds the BYTEA column that holds the §4 envelope blob;
// a NULL value is the §12.8 line 850 destroyed state. F-12.8.5.
func TestTenantErasureSaltMigration_spec_12_8_845(t *testing.T) {
	b, err := FS.ReadFile("0133_tenant_erasure_salt.up.sql")
	if err != nil {
		t.Fatalf("read migration 0133: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"ALTER TABLE tenants",
		"ADD COLUMN erasure_salt BYTEA",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0133 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0133_tenant_erasure_salt.down.sql")
	if err != nil {
		t.Fatalf("read migration 0133 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS erasure_salt") {
		t.Error("migration 0133 down must drop the erasure_salt column")
	}
}
