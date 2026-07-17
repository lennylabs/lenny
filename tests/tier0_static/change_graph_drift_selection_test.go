// SPDX-License-Identifier: MIT

package tier0_static

import "testing"

// spec: 25.10 (Configuration Drift Detection); §5 "Every package under
// `pkg/` appears in the change graph (or under an explicit `pkg/**`
// glob)."
// diagnosis: pkg/drift implements the §25.10 field-by-field desired-vs-
//
//	actual diff and severity classification consumed by
//	pkg/ops/driftservice (see reconcile.go and driftservice.go, both of
//	which import github.com/lennylabs/lenny/pkg/drift). pkg/drift owns
//	its own in-package unit suite (pkg/drift/drift_test.go) and, through
//	driftservice, feeds the tier-3 drift_against_target_test.go wire
//	contract. Without a tests/change-graph.json glob entry for
//	pkg/drift, a change under pkg/drift/*.go resolves to an empty tier
//	set (static only) and `lenny-test --changed`/`--since` never
//	re-selects pkg/drift/drift_test.go or the contract suite that
//	exercises it transitively. Add a "pkg/drift" glob entry mapping to
//	at least the unit tier, mirroring the sibling
//	pkg/ops/driftservice entry.
func TestChangeGraphDriftPackageSelectsUnitTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "pkg/drift/drift.go")

	if len(tiers) == 0 {
		t.Fatal("a change to pkg/drift resolved to an empty tier set (static only); it owns pkg/drift/drift_test.go, so tests/change-graph.json must map \"pkg/drift\" to at least the unit tier")
	}
	if !tiers["unit"] {
		t.Errorf("a change to pkg/drift resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
			sortedKeys(tiers), "unit")
	}
}
