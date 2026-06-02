// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §9.3 line 136 / §5.1 — connector tool-capability metadata
// derived from MCP ToolAnnotations. Migration 0114 adds the
// capability_inference_mode, capabilities, tool_capabilities, and
// capabilities_refreshed_at columns; the down migration drops them.
// F-9.3.8.
func TestConnectorsCapabilitiesMigration_spec_9_3_136(t *testing.T) {
	b, err := FS.ReadFile("0114_connectors_capabilities.up.sql")
	if err != nil {
		t.Fatalf("read migration 0114: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE connectors",
		"ADD COLUMN capability_inference_mode TEXT",
		"DEFAULT 'strict'",
		"ADD COLUMN capabilities",
		"ADD COLUMN tool_capabilities",
		"ADD COLUMN capabilities_refreshed_at TIMESTAMPTZ",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0114 up missing %q", want)
		}
	}

	b, err = FS.ReadFile("0114_connectors_capabilities.down.sql")
	if err != nil {
		t.Fatalf("read migration 0114 down: %v", err)
	}
	down := string(b)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS capability_inference_mode",
		"DROP COLUMN IF EXISTS capabilities",
		"DROP COLUMN IF EXISTS tool_capabilities",
		"DROP COLUMN IF EXISTS capabilities_refreshed_at",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0114 down missing %q", want)
		}
	}
}
