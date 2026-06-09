// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §16.7 lines 672, 676, 677, 682 — migration 0163 creates the
// single-row deployment_config_state baseline the deployment-transition
// audit emitter diffs each Helm render against. The scope CHECK enforces
// the singleton invariant, and lenny_app holds the read + upsert DML the
// reconciliation endpoint needs (the table is mutable platform-operational
// state, not the append-only audit chain). F-8.2.5, F-9.2.10, F-17.2.8.
func TestDeploymentConfigStateMigration_spec_16_7(t *testing.T) {
	b, err := FS.ReadFile("0163_deployment_config_state.up.sql")
	if err != nil {
		t.Fatalf("read migration 0163: %v", err)
	}
	sql := string(b)
	for _, want := range []string{
		"CREATE TABLE deployment_config_state",
		"CHECK (scope = 'platform')",
		"cycle_detection_mode",
		"allow_self_recursion",
		"default_max_depth",
		"elicitation_floor",
		"last_revision",
		"GRANT SELECT, INSERT, UPDATE ON deployment_config_state TO lenny_app",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("migration 0163 missing %q", want)
		}
	}
	// The baseline must not grant DELETE: there is exactly one persistent
	// row and the reconciliation only upserts it.
	if strings.Contains(sql, "DELETE ON deployment_config_state") {
		t.Error("migration 0163 must not grant DELETE on the singleton baseline")
	}

	down, err := FS.ReadFile("0163_deployment_config_state.down.sql")
	if err != nil {
		t.Fatalf("read migration 0163 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS deployment_config_state") {
		t.Error("migration 0163 down must drop deployment_config_state")
	}
}
