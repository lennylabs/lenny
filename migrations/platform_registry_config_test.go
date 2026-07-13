// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §25.8 lines 3300-3301, 3360-3362 — migration 0135 creates the
// platform_registry_config singleton that backs the runtime registry API
// (GET/PUT /v1/admin/platform/registry). The PUT persists url, overrides,
// pull_secret_name, and require_digest so the change survives a restart and
// takes effect on the next image resolution. The pull-secret value is never
// stored; only the Secret name.
func TestPlatformRegistryConfigMigration_spec_25_8(t *testing.T) {
	b, err := FS.ReadFile("0135_platform_registry_config.up.sql")
	if err != nil {
		t.Fatalf("read migration 0135: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE platform_registry_config",
		"id               TEXT PRIMARY KEY DEFAULT 'singleton'",
		"url              TEXT NOT NULL DEFAULT ''",
		"overrides        JSONB NOT NULL DEFAULT '{}'",
		"pull_secret_name TEXT NOT NULL DEFAULT ''",
		"require_digest   BOOLEAN NOT NULL DEFAULT false",
		"updated_at       TIMESTAMPTZ NOT NULL DEFAULT now()",
		"updated_by       TEXT NOT NULL DEFAULT ''",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0135 missing %q", want)
		}
	}

	down, err := FS.ReadFile("0135_platform_registry_config.down.sql")
	if err != nil {
		t.Fatalf("read migration 0135 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS platform_registry_config") {
		t.Error("migration 0135 down must drop the platform_registry_config table")
	}
}
