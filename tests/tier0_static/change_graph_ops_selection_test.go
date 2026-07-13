// SPDX-License-Identifier: MIT

package tier0_static

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// resolveChangeGraphTiers mirrors the prefix-match semantics that
// lenny-test uses to turn a changed path into a tier set (see
// tiersForChangedPath in cmd/lenny-test): a glob key applies to a
// changed path when the key is a prefix of the path. Every matching
// key contributes the tiers it lists. The guard tests below assert on
// the tier set this resolution produces, so they catch a change-graph
// map that under-selects for a given path without depending on the
// unexported resolver in package main.
func resolveChangeGraphTiers(t *testing.T, changedPath string) map[string]bool {
	t.Helper()
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
	tiers := map[string]bool{}
	for key, perTier := range doc.Globs {
		if strings.HasPrefix(changedPath, key) {
			for tier := range perTier {
				tiers[tier] = true
			}
		}
	}
	return tiers
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// spec: 25.4 (the lenny-ops service; opsserver HTTP endpoint surface)
// diagnosis: A change under pkg/ops/opsserver no longer selects its
//
//	tier-3 contract suite. The tier3_contract/ops_endpoints suite drives
//	the pkg/ops/opsserver HTTP handlers directly (it imports opsserver
//	and calls opsserver.New), so an endpoint or wire-contract change to
//	opsserver must run those contract tests. When the change graph omits
//	the contract tier for pkg/ops/opsserver, `lenny-test --changed` and
//	`--since` under-select and a broken opsserver endpoint contract ships
//	untested. Re-add the "contract" mapping in tests/change-graph.json.
func TestChangeGraphOpsserverSelectsContractTier(t *testing.T) {
	t.Parallel()

	// A representative opsserver source change (any file under the
	// package). §25.4's endpoints are served from pkg/ops/opsserver and
	// pinned by the tier-3 ops_endpoints contract suite.
	tiers := resolveChangeGraphTiers(t, "pkg/ops/opsserver/mcp.go")

	for _, want := range []string{"unit", "contract"} {
		if !tiers[want] {
			t.Errorf("change to pkg/ops/opsserver resolved to tiers %v; expected it to include %q so the tier-3 ops_endpoints contract suite is selected",
				sortedKeys(tiers), want)
		}
	}
}

// spec: 25.4 (the lenny-ops service; opsserver HTTP endpoint surface)
// diagnosis: A change to the tier-3 ops_endpoints contract suite itself
//
//	does not select the contract tier, so editing or extending an
//	opsserver contract test does not re-run it under `lenny-test
//	--changed`/`--since`. tests/tier3_contract/ops_endpoints/** must map
//	to the contract tier in tests/change-graph.json.
func TestChangeGraphOpsEndpointsSuiteSelectsContractTier(t *testing.T) {
	t.Parallel()

	tiers := resolveChangeGraphTiers(t, "tests/tier3_contract/ops_endpoints/mcp_test.go")

	if !tiers["contract"] {
		t.Errorf("change to tests/tier3_contract/ops_endpoints resolved to tiers %v; expected it to include %q",
			sortedKeys(tiers), "contract")
	}
}
