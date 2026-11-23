// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// TestIssuedTokensImpersonatorMigration checks that migration 0200 adds the
// issued_tokens columns that record the platform-admin bearer an
// impersonation started from, and the partial index on them that the
// cross-tenant revocation lookup reads, and that the down migration drops
// both.
//
// spec: §12.2 (Storage Roles), §13.3 (Credential Flow)
func TestIssuedTokensImpersonatorMigration(t *testing.T) {
	b, err := FS.ReadFile("0200_issued_tokens_impersonator.up.sql")
	if err != nil {
		t.Fatalf("read migration 0200: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE issued_tokens ADD COLUMN impersonator_tenant_id TEXT NULL;",
		"ALTER TABLE issued_tokens ADD COLUMN impersonator_jti TEXT NULL;",
		"CREATE INDEX idx_issued_tokens_impersonator ON issued_tokens (impersonator_tenant_id, impersonator_jti) " +
			"WHERE impersonator_jti IS NOT NULL;",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0200 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0200_issued_tokens_impersonator.down.sql")
	if err != nil {
		t.Fatalf("read migration 0200 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP INDEX IF EXISTS idx_issued_tokens_impersonator;",
		"ALTER TABLE issued_tokens DROP COLUMN IF EXISTS impersonator_jti;",
		"ALTER TABLE issued_tokens DROP COLUMN IF EXISTS impersonator_tenant_id;",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0200 down missing %q", want)
		}
	}
}
