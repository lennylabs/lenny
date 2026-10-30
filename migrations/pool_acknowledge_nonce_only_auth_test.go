// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §4.7 (runtime adapter, nonce-only acknowledgment gate), §5.3
// (isolation profiles) — migration 0170 adds the
// acknowledge_nonce_only_auth column on sandbox_warm_pools, the §5.3
// pool-level opt-in of the same class as allow_standard_isolation
// (migration 0033). The gateway pool admission path rejects a pool that
// references a require_so_peercred = false runtime unless this flag is
// true. The column defaults to false (no acknowledgment), so a pool that
// never opts in keeps full SO_PEERCRED enforcement. The down migration
// drops it.
func TestPoolAcknowledgeNonceOnlyAuthMigration_spec_4_7_5_3(t *testing.T) {
	b, err := FS.ReadFile("0170_sandbox_warm_pools_acknowledge_nonce_only_auth.up.sql")
	if err != nil {
		t.Fatalf("read migration 0170: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE sandbox_warm_pools",
		"ADD COLUMN acknowledge_nonce_only_auth BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0170 up missing %q", want)
		}
	}
	// The default must be false (fail closed): a true default would
	// admit a nonce-only runtime into any pool without the deployer
	// opt-in the §4.7 acknowledgment gate requires.
	if strings.Contains(up, "DEFAULT true") {
		t.Error("migration 0170 acknowledge_nonce_only_auth must default to false, not true")
	}

	down, err := FS.ReadFile("0170_sandbox_warm_pools_acknowledge_nonce_only_auth.down.sql")
	if err != nil {
		t.Fatalf("read migration 0170 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS acknowledge_nonce_only_auth") {
		t.Error("migration 0170 down must drop acknowledge_nonce_only_auth")
	}
}
