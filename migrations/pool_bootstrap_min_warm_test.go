// SPDX-License-Identifier: MIT

package migrations

import (
	"strings"
	"testing"
)

// spec: §17.8.2 "Cold-start bootstrap procedure" — migration 0137 adds
// the bootstrap_min_warm override column on sandbox_warm_pools so the
// PoolScalingController can pin the pool to a static warm-pod floor until
// the convergence criteria are met; the down migration drops it.
func TestPoolBootstrapMinWarmMigration_spec_17_8_2(t *testing.T) {
	b, err := FS.ReadFile("0137_pool_bootstrap_min_warm.up.sql")
	if err != nil {
		t.Fatalf("read migration 0137: %v", err)
	}
	up := string(b)
	for _, want := range []string{
		"ALTER TABLE sandbox_warm_pools",
		"ADD COLUMN IF NOT EXISTS bootstrap_min_warm INTEGER",
		"bootstrap_min_warm >= 0",
	} {
		if !strings.Contains(up, want) {
			t.Errorf("migration 0137 up missing %q", want)
		}
	}

	down, err := FS.ReadFile("0137_pool_bootstrap_min_warm.down.sql")
	if err != nil {
		t.Fatalf("read migration 0137 down: %v", err)
	}
	if !strings.Contains(string(down), "DROP COLUMN IF EXISTS bootstrap_min_warm") {
		t.Error("migration 0137 down must drop bootstrap_min_warm")
	}
}
