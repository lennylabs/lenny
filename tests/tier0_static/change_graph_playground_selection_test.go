// SPDX-License-Identifier: MIT

package tier0_static

import "testing"

// spec: 27.8 (Web Playground — Metrics); TESTING.md §5 "Every package under
// `pkg/` appears in the change graph (or under an explicit `pkg/**` glob)."
// diagnosis: pkg/gateway/mcpfabric/playground implements the §27 web
//
//	playground and owns an in-package unit suite (playground_test.go,
//	metrics_catalog_test.go, metrics_increment_test.go, auth.go/oidc.go
//	tests, and others). Its sibling packages under pkg/gateway/mcpfabric
//	(mcp, mcpruntimes) each have a tests/change-graph.json glob entry
//	mapping to at least the unit tier. Without a
//	"pkg/gateway/mcpfabric/playground" entry, a change under this
//	package resolves to an empty tier set (static only) and
//	`lenny-test --changed`/`--since` never re-selects the package's own
//	unit suite. Add a "pkg/gateway/mcpfabric/playground" glob entry
//	mapping to at least the unit tier, mirroring the sibling
//	pkg/gateway/mcpfabric/mcp and pkg/gateway/mcpfabric/mcpruntimes
//	entries.
func TestChangeGraphPlaygroundPackageSelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/gateway/mcpfabric/playground/playground.go")

	if len(tiers) == 0 {
		t.Fatal("a change to pkg/gateway/mcpfabric/playground resolved to an empty tier set (static only); it owns an in-package unit suite, so tests/change-graph.json must map \"pkg/gateway/mcpfabric/playground\" to at least the unit tier")
	}
	if !tiers["unit"] {
		t.Errorf("a change to pkg/gateway/mcpfabric/playground resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}
