// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// TestTokenRuleFixMigrations checks that migration 0201 creates the
// tenant-scoped impersonation_starts table with tenant_id leading its
// primary key, its impersonator index, the tenant guard trigger, and
// row-level security, that migration 0202 adds the partial index the full
// revoke's delegation seed reads, that migration 0203 adds the depth
// column with a default so a rolling deploy's older replicas can insert,
// and that each down migration reverses its up migration.
//
// spec: §12.2 (Storage Roles), §13.3 (Credential Flow), §11.4 (User Invalidation)
func TestTokenRuleFixMigrations(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{"0201_impersonation_starts.up.sql", []string{
			"CREATE TABLE impersonation_starts (",
			"PRIMARY KEY (tenant_id, bearer_jti)",
			"ON impersonation_starts (tenant_id, impersonator_jti);",
			"BEFORE INSERT OR UPDATE OR DELETE ON impersonation_starts",
			"ALTER TABLE impersonation_starts FORCE ROW LEVEL SECURITY;",
			"USING (tenant_id = current_setting('app.current_tenant', false));",
		}},
		{"0201_impersonation_starts.down.sql", []string{"DROP TABLE IF EXISTS impersonation_starts;"}},
		{"0202_issued_tokens_act_sub_index.up.sql", []string{
			"CREATE INDEX idx_issued_tokens_act_sub_child ON issued_tokens (tenant_id, act_sub) " +
				"WHERE act_sub IS NOT NULL AND parent_jti IS NOT NULL;",
		}},
		{"0202_issued_tokens_act_sub_index.down.sql", []string{"DROP INDEX IF EXISTS idx_issued_tokens_act_sub_child;"}},
		{"0203_issued_tokens_depth.up.sql", []string{"ALTER TABLE issued_tokens ADD COLUMN depth INTEGER NOT NULL DEFAULT 0;"}},
		{"0203_issued_tokens_depth.down.sql", []string{"ALTER TABLE issued_tokens DROP COLUMN IF EXISTS depth;"}},
	}
	for _, tc := range cases {
		b, err := FS.ReadFile(tc.file)
		if err != nil {
			t.Fatalf("read %s: %v", tc.file, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(string(b), want) {
				t.Errorf("%s missing %q", tc.file, want)
			}
		}
	}
}
