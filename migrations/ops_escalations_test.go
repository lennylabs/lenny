// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.4 lines 2433-2455. Migration 0122 creates the Tier 1
// (Postgres) ops_escalations durable store with the two §25.4 list
// indexes. created_at is deliberately NOT defaulted so a buffer flush
// preserves the authoring timestamp. Platform-scoped (no RLS, no tenant
// column); the down migration drops the table and both indexes.
func TestOpsEscalationsMigration_spec_25_4(t *testing.T) {
	up := mustReadMigration(t, "0122_ops_escalations.up.sql")
	for _, want := range []string{
		"CREATE TABLE ops_escalations",
		"severity        TEXT NOT NULL",
		"failed_actions  JSONB NOT NULL DEFAULT '[]'",
		"status          TEXT NOT NULL DEFAULT 'open'",
		"persistence     TEXT NOT NULL",
		"emitted         BOOLEAN NOT NULL DEFAULT false",
		"created_at      TIMESTAMPTZ NOT NULL",
		"CREATE INDEX ops_escalations_status_severity ON ops_escalations (status, severity)",
		"CREATE INDEX ops_escalations_created_at ON ops_escalations (created_at DESC)",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0122 up missing %q", want)
		}
	}
	// created_at must NOT carry a now() default (spec: line 2447).
	if strings.Contains(up, "created_at      TIMESTAMPTZ NOT NULL DEFAULT now()") {
		t.Error("migration 0122: created_at must not default to now() so flushed escalations preserve authoring time")
	}
	assertPlatformScoped(t, "0122", up)

	down := mustReadMigration(t, "0122_ops_escalations.down.sql")
	if !strings.Contains(down, "DROP TABLE IF EXISTS ops_escalations") {
		t.Error("migration 0122 down missing DROP TABLE ops_escalations")
	}
}
