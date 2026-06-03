// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §11.2 lines 42-48; §12.4. Migration 0119 creates the
// token_usage_checkpoint table — the durable Postgres checkpoint of the
// §11.2 Redis token-usage counters that the §11.2 line 48 two-source
// reconstruction restores via the MAX rule on Redis recovery and the
// §24.6 operator reconcile re-aggregates into Redis. The table carries
// the standard §12.3 tenant-guard trigger and RLS policy; the down
// migration drops the table (and with it the trigger, policy, and index).
func TestTokenUsageCheckpointMigration_spec_11_2(t *testing.T) {
	b, err := FS.ReadFile("0119_token_usage_checkpoint.up.sql")
	if err != nil {
		t.Fatalf("read migration 0119: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"CREATE TABLE token_usage_checkpoint",
		"tenant_id     TEXT        NOT NULL REFERENCES tenants(id)",
		"scope         TEXT        NOT NULL CHECK (scope IN ('user', 'tenant'))",
		"subject_id    TEXT        NOT NULL DEFAULT ''",
		"period        TEXT        NOT NULL",
		"window_label  TEXT        NOT NULL",
		"token_total   BIGINT      NOT NULL DEFAULT 0",
		"checkpoint_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()",
		"PRIMARY KEY (tenant_id, scope, subject_id, period, window_label)",
		"idx_token_usage_checkpoint_checkpoint",
		"CREATE TRIGGER lenny_tenant_guard",
		"ENABLE ROW LEVEL SECURITY",
		"FORCE ROW LEVEL SECURITY",
		"CREATE POLICY lenny_tenant_isolation ON token_usage_checkpoint",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON token_usage_checkpoint TO lenny_app",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0119 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0119_token_usage_checkpoint.down.sql")
	if err != nil {
		t.Fatalf("read migration 0119 down: %v", err)
	}
	if down := string(b); !strings.Contains(down, "DROP TABLE IF EXISTS token_usage_checkpoint") {
		t.Errorf("migration 0119 down missing DROP TABLE token_usage_checkpoint")
	}
}
