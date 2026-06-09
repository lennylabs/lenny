// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §4.8 lines 1034-1040 / §8.3 lines 205-224 (SEC-013) — migration
// 0161 creates the platform-scoped interceptors registry table with the
// closed fail-policy enum, the reserved-priority (>100) CHECK, and the
// server-minted transition columns the admin write path never sets from a
// request body. F-4.8.17.
func TestInterceptorsMigration_spec_4_8_1034(t *testing.T) {
	b, err := FS.ReadFile("0161_interceptors.up.sql")
	if err != nil {
		t.Fatalf("read migration 0161: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS interceptors",
		"name                            TEXT        PRIMARY KEY",
		"fail_open_transition_at         TIMESTAMPTZ",
		"cooldown_seconds_at_transition  INTEGER",
		"CHECK (fail_policy IN ('fail-closed', 'fail-open'))",
		"CHECK (priority > 100)",
		"GRANT SELECT, INSERT, UPDATE, DELETE ON interceptors TO lenny_app",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0161 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0161_interceptors.down.sql")
	if err != nil {
		t.Fatalf("read migration 0161 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS interceptors") {
		t.Error("migration 0161 down must drop the interceptors table")
	}
}
