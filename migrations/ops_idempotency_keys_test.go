// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.4 lines 2011-2130. Migration 0116 creates the
// ops_idempotency_keys table — the §25.4 control-plane idempotency-key
// registry keyed by (key, caller_id), with the expires_at retention
// index. It is platform-scoped (no tenant column, no RLS); the down
// migration drops the table and its index.
func TestOpsIdempotencyKeysMigration_spec_25_4(t *testing.T) {
	b, err := FS.ReadFile("0116_ops_idempotency_keys.up.sql")
	if err != nil {
		t.Fatalf("read migration 0116: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"CREATE TABLE ops_idempotency_keys",
		"key         TEXT        NOT NULL",
		"caller_id   TEXT        NOT NULL",
		"endpoint    TEXT        NOT NULL",
		"status      TEXT        NOT NULL DEFAULT 'in_progress'",
		"response    JSONB",
		"expires_at  TIMESTAMPTZ NOT NULL",
		"PRIMARY KEY (key, caller_id)",
		"CREATE INDEX ops_idempotency_keys_expires_at ON ops_idempotency_keys (expires_at)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0116 up missing %q", want)
		}
	}
	// Platform-scoped: no RLS / tenant-guard apparatus.
	for _, forbidden := range []string{"ROW LEVEL SECURITY", "lenny_tenant_guard", "tenant_id"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0116 up should be platform-scoped but contains %q", forbidden)
		}
	}

	b, err = FS.ReadFile("0116_ops_idempotency_keys.down.sql")
	if err != nil {
		t.Fatalf("read migration 0116 down: %v", err)
	}
	if down := string(b); !strings.Contains(down, "DROP TABLE IF EXISTS ops_idempotency_keys") {
		t.Errorf("migration 0116 down missing DROP TABLE ops_idempotency_keys")
	}
}
