// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

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

// spec: 25.8 (Platform Lifecycle Management, `GET /v1/admin/platform/config/diff`);
// TESTING.md §5 "`tests/change-graph.json` maps source packages ... to the
// tests that exercise them."
// diagnosis: pkg/ops/configservice implements the §25.8 config-diff endpoint
//
//	and imports github.com/lennylabs/lenny/pkg/drift directly
//	(configservice.go:25), calling drift.Diff, drift.Change, and
//	drift.Modified from toChanges() and warningsFor() to build the diff
//	response. pkg/ops/configservice is a second, independent consumer of
//	pkg/drift alongside pkg/ops/driftservice, and it owns its own unit
//	suite (pkg/ops/configservice/configservice_test.go) that exercises
//	that dependency. If the "pkg/drift" change-graph entry lists only
//	tests reachable through pkg/ops/driftservice, a pkg/drift change
//	that breaks configservice's use of drift.Diff/Change/Modified never
//	re-selects configservice's unit suite under `lenny-test --changed`.
//	The "pkg/drift" entry's unit tier must also cover
//	pkg/ops/configservice, mirroring configservice's own change-graph
//	entry.
func TestChangeGraphDriftEntryCoversConfigServiceConsumer(t *testing.T) {
	t.Parallel()

	root := schematest.RepoRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "tests", "change-graph.json"))
	if err != nil {
		t.Fatalf("read change-graph.json: %v", err)
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("parse change-graph.json: %v", err)
	}

	unit, ok := doc.Globs["pkg/drift"]["unit"]
	if !ok {
		t.Fatal(`"pkg/drift" has no "unit" tier entry in tests/change-graph.json`)
	}
	for _, g := range unit {
		if strings.HasPrefix(g, "pkg/ops/configservice") {
			return
		}
	}
	t.Errorf(`"pkg/drift"."unit" is %v; it must include a glob covering pkg/ops/configservice, which imports pkg/drift directly (configservice.go:25) and owns a unit suite that a pkg/drift change can break`, unit)
}
