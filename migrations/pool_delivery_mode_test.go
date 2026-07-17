// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §4.9 (cross-tenant credential-delivery combinations) — migration
// 0175 adds the delivery_mode, spiffe_binding,
// allow_direct_mode_standard_isolation, and
// allow_proxy_mode_spiffe_binding_disabled columns on sandbox_warm_pools so
// one warm-pool admin resource carries the whole credential-delivery
// combination the pool-registration and admission layers inspect. The two
// text fields default to ” (inherit) and the two opt-in booleans default to
// false (no acknowledgment). The down migration drops all four.
func TestPoolDeliveryModeMigration_spec_4_9(t *testing.T) {
	b, err := FS.ReadFile("0175_sandbox_warm_pools_delivery_mode.up.sql")
	if err != nil {
		t.Fatalf("read migration 0175: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE sandbox_warm_pools",
		"ADD COLUMN delivery_mode TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN spiffe_binding TEXT NOT NULL DEFAULT ''",
		"ADD COLUMN allow_direct_mode_standard_isolation BOOLEAN NOT NULL DEFAULT false",
		"ADD COLUMN allow_proxy_mode_spiffe_binding_disabled BOOLEAN NOT NULL DEFAULT false",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0175 up missing %q", want)
		}
	}
	// The opt-in acknowledgments must default to false (fail closed): a true
	// default would silently acknowledge a forbidden combination the deployer
	// never opted into.
	if strings.Contains(up, "DEFAULT true") {
		t.Error("migration 0175 opt-in columns must default to false, not true")
	}

	d, err := FS.ReadFile("0175_sandbox_warm_pools_delivery_mode.down.sql")
	if err != nil {
		t.Fatalf("read migration 0175 down: %v", err)
	}
	down := string(d)
	for _, want := range []string{
		"DROP COLUMN IF EXISTS delivery_mode",
		"DROP COLUMN IF EXISTS spiffe_binding",
		"DROP COLUMN IF EXISTS allow_direct_mode_standard_isolation",
		"DROP COLUMN IF EXISTS allow_proxy_mode_spiffe_binding_disabled",
	} {
		if !strings.Contains(down, want) {
			t.Errorf("migration 0175 down missing %q", want)
		}
	}
}
