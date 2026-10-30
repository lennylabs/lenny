// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §4.7 (runtime adapter, nonce-only activation), §5.1 (runtime
// definition) — migration 0169 adds the require_so_peercred column on
// runtime_definitions so the RuntimeReconciler can mirror
// Runtime.spec.requireSoPeercred into the gateway runtime registry, where
// the §5.3 pool admission gate evaluates it. The column defaults to true
// (full SO_PEERCRED enforcement); false activates §4.7 nonce-only mode.
// The down migration drops it. The default must be true so a runtime that
// never sets the field, including one registered only through the admin
// API, keeps the enforced posture (§4.7 fail-closed).
func TestRuntimeRequireSoPeercredMigration_spec_4_7_5_1(t *testing.T) {
	b, err := FS.ReadFile("0169_runtime_definitions_require_so_peercred.up.sql")
	if err != nil {
		t.Fatalf("read migration 0169: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE runtime_definitions",
		"ADD COLUMN require_so_peercred BOOLEAN NOT NULL DEFAULT true",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0169 up missing %q", want)
		}
	}
	// The default must be true (fail closed): a false default would put
	// every runtime that omits the field into nonce-only mode and silently
	// weaken the §4.7 adapter-agent boundary.
	if strings.Contains(up, "DEFAULT false") {
		t.Error("migration 0169 require_so_peercred must default to true, not false")
	}

	down, err := FS.ReadFile("0169_runtime_definitions_require_so_peercred.down.sql")
	if err != nil {
		t.Fatalf("read migration 0169 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS require_so_peercred") {
		t.Error("migration 0169 down must drop require_so_peercred")
	}
}
