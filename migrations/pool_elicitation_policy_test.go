// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §9.2 lines 86, 90-98 — the per-pool elicitation policy
// (elicitationDepthPolicy and urlModeElicitation) is stored on the
// sandbox_warm_pools row so the gateway resolves it at
// lenny/request_elicitation dispatch time. Migration 0110 adds the
// elicitation_policy JSONB column; the down migration drops it.
func TestPoolElicitationPolicyMigration_spec_9_2(t *testing.T) {
	b, err := FS.ReadFile("0110_pool_elicitation_policy.up.sql")
	if err != nil {
		t.Fatalf("read migration 0110: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE sandbox_warm_pools",
		"ADD COLUMN IF NOT EXISTS elicitation_policy JSONB",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0110 up missing %q", want)
		}
	}

	down, err := FS.ReadFile("0110_pool_elicitation_policy.down.sql")
	if err != nil {
		t.Fatalf("read migration 0110 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS elicitation_policy") {
		t.Error("migration 0110 down must drop elicitation_policy")
	}
}
