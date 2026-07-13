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

// opsPackagesWithUnitSuite enumerates every pkg/ops/<pkg> directory that
// carries an in-package *_test.go file. Such a package owns a unit suite
// that pins its §25 behavior, so a change under it must select at least
// the unit tier. It returns the package directory (relative to the repo
// root) and a representative non-test source path inside it.
func opsPackagesWithUnitSuite(t *testing.T) map[string]string {
	t.Helper()
	root := schematest.RepoRoot(t)
	opsDir := filepath.Join(root, "pkg", "ops")
	entries, err := os.ReadDir(opsDir)
	if err != nil {
		t.Fatalf("read pkg/ops: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgDir := filepath.Join(opsDir, e.Name())
		files, err := os.ReadDir(pkgDir)
		if err != nil {
			t.Fatalf("read %s: %v", pkgDir, err)
		}
		hasUnitTest := false
		representative := ""
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".go") {
				continue
			}
			if strings.HasSuffix(f.Name(), "_test.go") {
				hasUnitTest = true
				continue
			}
			if representative == "" {
				representative = f.Name()
			}
		}
		if !hasUnitTest {
			continue
		}
		if representative == "" {
			// A package with only test files still owns a unit suite;
			// use a synthetic source path (prefix resolution only needs
			// the change-graph key to be a prefix of the changed path).
			representative = "doc.go"
		}
		relKey := filepath.ToSlash(filepath.Join("pkg", "ops", e.Name()))
		out[relKey] = relKey + "/" + representative
	}
	return out
}

// spec: 25.4 (the lenny-ops service; agent-operability package surface)
// diagnosis: A pkg/ops/<pkg> package that owns an in-package unit suite
//
//	resolves to an empty tier set (static only) through
//	tests/change-graph.json, so a change to it selects no unit/contract/
//	integration tests under `lenny-test --changed`/`--since` and its
//	suite silently stops running on the diff that touched it. Every
//	pkg/ops/* package that ships _test.go files must carry a
//	change-graph glob whose tiers include "unit". Add the missing glob
//	mapping in tests/change-graph.json for the package named in the
//	failure.
func TestChangeGraphOpsPackagesWithSuiteSelectUnitTier(t *testing.T) {
	t.Parallel()

	pkgs := opsPackagesWithUnitSuite(t)
	if len(pkgs) == 0 {
		t.Fatal("found no pkg/ops/* package with an in-package unit suite; enumeration is broken")
	}
	for key, changedPath := range pkgs {
		tiers := resolveChangeGraphTiers(t, changedPath)
		if len(tiers) == 0 {
			t.Errorf("a change to %s resolved to an empty tier set (static only); it owns a unit suite, so tests/change-graph.json must map %q to at least the unit tier",
				changedPath, key)
			continue
		}
		if !tiers["unit"] {
			t.Errorf("a change to %s resolved to tiers %v; it owns an in-package unit suite, so the resolution must include %q",
				changedPath, sortedKeys(tiers), "unit")
		}
	}
}

// spec: 25.4 (the lenny-ops service; backup, upgrade, and drift surfaces)
// diagnosis: pkg/ops/backup, pkg/ops/upgradeservice, and
//
//	pkg/ops/driftservice each own a dedicated tier-3 contract suite under
//	tests/tier3_contract/ops_endpoints (backup_test.go, upgrade_test.go,
//	drift_against_target_test.go), and backup and upgradeservice also own
//	tier-4 integration suites (backup_restore_test.go /
//	backup_degradation_test.go, release_channel_fetch_test.go). When
//	tests/change-graph.json omits the contract/integration tier for one
//	of these packages, `lenny-test --changed`/`--since` under-selects and
//	a wire-contract or multi-service regression in that package ships
//	untested. Add the missing tier mapping in tests/change-graph.json.
func TestChangeGraphOpsDedicatedSuitesSelectHigherTiers(t *testing.T) {
	t.Parallel()

	cases := []struct {
		pkg   string
		tiers []string
	}{
		{"pkg/ops/backup", []string{"unit", "contract", "integration"}},
		{"pkg/ops/upgradeservice", []string{"unit", "contract", "integration"}},
		{"pkg/ops/driftservice", []string{"unit", "contract"}},
	}
	for _, tc := range cases {
		tiers := resolveChangeGraphTiers(t, tc.pkg+"/change.go")
		for _, want := range tc.tiers {
			if !tiers[want] {
				t.Errorf("a change to %s resolved to tiers %v; it owns a dedicated %s suite, so the resolution must include %q",
					tc.pkg, sortedKeys(tiers), want, want)
			}
		}
	}
}
