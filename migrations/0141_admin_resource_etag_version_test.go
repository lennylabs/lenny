// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency. Migration
// 0141 extends the integer `version` column (the strong entity tag) to the
// tenants resource, which also backs the §10.6 rbac-config and §9.2
// elicitation-content-integrity sub-resources; the down migration drops it.
// F-15.1.2.
func TestAdminResourceEtagVersion4Migration_spec_15_1_1207(t *testing.T) {
	b, err := FS.ReadFile("0141_admin_resource_etag_version_4.up.sql")
	if err != nil {
		t.Fatalf("read migration 0141 up: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE tenants",
		"ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0141 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0141_admin_resource_etag_version_4.down.sql")
	if err != nil {
		t.Fatalf("read migration 0141 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS version",
		"tenants",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0141 down missing %q", want)
		}
	}
}
