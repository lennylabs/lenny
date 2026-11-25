// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// TestIssuedTokensParentIndexMigration checks that migration 0199 creates
// the issued_tokens partial index on (tenant_id, parent_jti) for rows that
// record a parent, which keys each step of the recursive revocation, and
// that the down migration drops it.
//
// spec: §12.2 (Storage Roles), §13.3 (Credential Flow)
func TestIssuedTokensParentIndexMigration(t *testing.T) {
	b, err := FS.ReadFile("0199_issued_tokens_parent_index.up.sql")
	if err != nil {
		t.Fatalf("read migration 0199: %v", err)
	}
	const wantUp = "CREATE INDEX idx_issued_tokens_parent ON issued_tokens (tenant_id, parent_jti) " +
		"WHERE parent_jti IS NOT NULL;"
	if up := string(b); !strings.Contains(up, wantUp) {
		t.Errorf("migration 0199 up missing %q", wantUp)
	}

	b, err = FS.ReadFile("0199_issued_tokens_parent_index.down.sql")
	if err != nil {
		t.Fatalf("read migration 0199 down: %v", err)
	}
	const wantDown = "DROP INDEX IF EXISTS idx_issued_tokens_parent;"
	if down := string(b); !strings.Contains(down, wantDown) {
		t.Errorf("migration 0199 down missing %q", wantDown)
	}
}
