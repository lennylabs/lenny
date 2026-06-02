// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.5 lines 2613-2664. Migration 0118 brings
// ops_event_subscriptions to the full §25.5 column set (secret hash +
// fingerprint at rest, the tenant-isolation columns, the generation
// counter, the severity filter) and adds the ops_event_deliveries
// delivery-tracking table with its two indexes. Both tables are
// platform-scoped (no RLS, no tenant guard).
func TestOpsEventSubscriptionsV25_5Migration_spec_25_5(t *testing.T) {
	b, err := FS.ReadFile("0118_ops_event_subscriptions_v25_5.up.sql")
	if err != nil {
		t.Fatalf("read migration 0118: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE ops_event_subscriptions",
		"DROP COLUMN secret",
		"DROP COLUMN types",
		"ADD COLUMN types                       TEXT[] NOT NULL DEFAULT '{}'",
		"ADD COLUMN severity                    TEXT[]",
		"ADD COLUMN secret_hash                 TEXT NOT NULL",
		"ADD COLUMN secret_fingerprint          TEXT NOT NULL",
		"ADD COLUMN previous_secret_fingerprint TEXT",
		"ADD COLUMN secret_rotated_at           TIMESTAMPTZ",
		"ADD COLUMN created_by                  TEXT NOT NULL",
		"ADD COLUMN created_by_tenant_id        TEXT",
		"ADD COLUMN tenant_filter               TEXT NOT NULL DEFAULT '*'",
		"ADD COLUMN generation                  BIGINT NOT NULL DEFAULT 0",
		"ADD COLUMN active                      BOOLEAN NOT NULL DEFAULT true",
		"CREATE TABLE ops_event_deliveries",
		"subscription_id TEXT NOT NULL REFERENCES ops_event_subscriptions(id) ON DELETE CASCADE",
		"CREATE INDEX ops_event_deliveries_expires_at",
		"CREATE INDEX ops_event_deliveries_subscription_status",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0118 up missing %q", want)
		}
	}
	// Platform-scoped: no RLS / tenant-guard apparatus on either table.
	for _, forbidden := range []string{"ROW LEVEL SECURITY", "lenny_tenant_guard"} {
		if strings.Contains(up, forbidden) {
			t.Errorf("migration 0118 up should be platform-scoped but contains %q", forbidden)
		}
	}

	b, err = FS.ReadFile("0118_ops_event_subscriptions_v25_5.down.sql")
	if err != nil {
		t.Fatalf("read migration 0118 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP TABLE IF EXISTS ops_event_deliveries",
		"DROP COLUMN IF EXISTS secret_hash",
		"ADD COLUMN types  JSONB NOT NULL DEFAULT '[]'::jsonb",
		"ADD COLUMN secret TEXT  NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0118 down missing %q", want)
		}
	}
}
