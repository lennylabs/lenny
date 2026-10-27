// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §12.2; §13.3. Migration 0190 creates the issued_tokens partial
// index on act_sub restricted to impersonation rows, whose predicate is
// exactly the §13.3 "Platform-admin impersonation" rule 2 predicate
// (act_sub IS NOT NULL AND parent_jti IS NULL), so the impersonation
// listing and the platform-admin full-revoke cascade are index-backed.
// The down migration drops the index.
func TestIssuedTokensImpersonationIndexMigration_spec_12_2(t *testing.T) {
	b, err := FS.ReadFile("0190_issued_tokens_impersonation_index.up.sql")
	if err != nil {
		t.Fatalf("read migration 0190: %v", err)
	}
	const wantUp = "CREATE INDEX idx_issued_tokens_impersonation ON issued_tokens (act_sub) " +
		"WHERE act_sub IS NOT NULL AND parent_jti IS NULL;"
	if up := string(b); !strings.Contains(up, wantUp) {
		t.Errorf("migration 0190 up missing %q", wantUp)
	}

	b, err = FS.ReadFile("0190_issued_tokens_impersonation_index.down.sql")
	if err != nil {
		t.Fatalf("read migration 0190 down: %v", err)
	}
	const wantDown = "DROP INDEX IF EXISTS idx_issued_tokens_impersonation;"
	if down := string(b); !strings.Contains(down, wantDown) {
		t.Errorf("migration 0190 down missing %q", wantDown)
	}
}
