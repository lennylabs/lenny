// SPDX-License-Identifier: MIT

package tier0_static

import "testing"

// spec: TESTING.md §5 "Every package under `pkg/` appears in the change
// graph (or under an explicit `pkg/**` glob)."
// diagnosis: pkg/embedded/stack owns a large in-package unit suite (among
//
//	others, stack_test.go, catalog_test.go, apply_test.go, bringup_test.go,
//	cluster_test.go, lifecycle_test.go, logs_test.go, restart_test.go,
//	runtimeapply_test.go, runtimepods_test.go, runtimes_test.go,
//	state_test.go, status_test.go, and tlsproxy_test.go). Before this
//	change, tests/change-graph.json had no glob entry matching
//	"pkg/embedded/stack" (or any "pkg/embedded" prefix), so a change under
//	pkg/embedded/stack resolved to an empty tier set (static only) and
//	`lenny-test run --changed`/`--since`/`--pkg pkg/embedded/stack` never
//	re-selected this unit suite. Add a "pkg/embedded/stack" glob entry
//	mapping to at least the unit tier.
func TestChangeGraphEmbeddedStackPackageSelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/embedded/stack/stack.go")

	if len(tiers) == 0 {
		t.Fatal(`a change to pkg/embedded/stack resolved to an empty tier set (static only); it owns an in-package unit suite (stack_test.go, catalog_test.go, apply_test.go, and others), so tests/change-graph.json must map "pkg/embedded/stack" to at least the unit tier`)
	}
	if !tiers["unit"] {
		t.Errorf("a change to pkg/embedded/stack resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}
