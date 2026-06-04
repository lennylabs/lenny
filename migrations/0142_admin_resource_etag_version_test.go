// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §15.1 lines 1207-1224 — ETag-based optimistic concurrency. Migration
// 0142 extends the integer `version` column (the strong entity tag) to the
// runtime_definitions resource, completing the admin-resource rollout; the
// down migration drops it. F-15.1.2.
func TestAdminResourceEtagVersion5Migration_spec_15_1_1207(t *testing.T) {
	b, err := FS.ReadFile("0142_admin_resource_etag_version_5.up.sql")
	if err != nil {
		t.Fatalf("read migration 0142 up: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE runtime_definitions",
		"ADD COLUMN IF NOT EXISTS version INTEGER NOT NULL DEFAULT 1",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0142 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0142_admin_resource_etag_version_5.down.sql")
	if err != nil {
		t.Fatalf("read migration 0142 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS version",
		"runtime_definitions",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0142 down missing %q", want)
		}
	}
}
