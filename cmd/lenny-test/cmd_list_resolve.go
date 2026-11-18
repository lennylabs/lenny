// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// enumerateTier returns the concrete test function names for the
// tier by shelling out to `go test -list`. Falls back to the
// directory glob from testsForTier when `go test` is unavailable
// or the listing fails (e.g., compilation error).
func enumerateTier(tier string) []string {
	glob := testsForTier(tier)
	if len(glob) == 0 {
		return nil
	}
	// Some tiers need a build tag (component, contract, integration,
	// e2e_kind, e2e_cloud, load, chaos, security, conformance).
	args := []string{"test", "-list", ".*"}
	if tag := tierBuildTag(tier); tag != "" {
		args = append(args, "-tags="+tag)
	}
	args = append(args, glob...)
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot()
	out, err := cmd.Output()
	if err != nil {
		// Fall back to the directory glob.
		return glob
	}
	names := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		// `go test -list` interleaves "ok pkg" lines with test
		// names; only Test/Fuzz/Example/Benchmark names are useful.
		if strings.HasPrefix(line, "Test") ||
			strings.HasPrefix(line, "Fuzz") ||
			strings.HasPrefix(line, "Example") ||
			strings.HasPrefix(line, "Benchmark") {
			names = append(names, line)
		}
	}
	if len(names) == 0 {
		return glob
	}
	return names
}

// tierBuildTag returns the build tag a tier needs, or "" when the
// tier compiles untagged.
func tierBuildTag(tier string) string {
	switch tier {
	case "component":
		return "component"
	case "contract":
		return "contract"
	case "integration":
		return "integration"
	case "e2e_kind":
		return "e2e_kind"
	case "e2e_cloud":
		return "e2e_cloud"
	case "load_local":
		return "load_local"
	case "load_kind":
		return "load_kind"
	case "chaos":
		return "chaos"
	case "security":
		return "security"
	case "conformance":
		return "conformance"
	case "load_cloud":
		return "load_cloud"
	}
	return ""
}

// testsForTier returns the canonical test paths for the given tier
// name. Returns an empty slice when the tier is unknown.
func testsForTier(tier string) []string {
	switch tier {
	case "static":
		return []string{"./tests/static/..."}
	case "unit":
		return []string{"./..."}
	case "component":
		return []string{"./tests/component/..."}
	case "contract":
		return []string{"./tests/contract/..."}
	case "integration":
		return []string{"./tests/integration/..."}
	case "e2e_kind":
		return []string{"./tests/e2e_kind/..."}
	case "e2e_cloud":
		return []string{"./tests/e2e_cloud/..."}
	case "load_local":
		return []string{"./tests/load_local/..."}
	case "load_kind":
		return []string{"./tests/load_kind/..."}
	case "chaos":
		return []string{"./tests/chaos/..."}
	case "security":
		return []string{"./tests/security/..."}
	case "conformance":
		return []string{"./tests/conformance/..."}
	case "docs":
		return []string{"./tests/docs/..."}
	case "load_cloud":
		return []string{"./tests/load_cloud/..."}
	}
	return nil
}

// testsForSpec resolves identifiers, page codes, and numbered sections
// to the tests the annotation index and the spec map name for them (see
// specModel.selectFor). A query that neither knows is an error.
func testsForSpec(specs string) ([]string, error) {
	m, err := loadSpecModel(context.Background(), repoRoot())
	if err != nil {
		return nil, err
	}
	return testsForSpecIn(m, splitNonEmpty(specs))
}

// testsForSpecIn is testsForSpec over a loaded model: the index's tests
// as `path::Name`, then the spec map's entries as the map writes them.
func testsForSpecIn(m *specModel, queries []string) ([]string, error) {
	out := []string{}
	var unknown []string
	for _, q := range queries {
		sel, ok := m.selectFor(q)
		if !ok {
			unknown = append(unknown, q)
			continue
		}
		for _, t := range sel.tests {
			out = append(out, t.String())
		}
		out = append(out, sel.mapped...)
	}
	if len(unknown) > 0 {
		return nil, unknownSpecQueries(unknown)
	}
	return out, nil
}

// testsForPkg resolves package directories through the import graph, as
// `run --pkg` does, and lists what the selection runs (see planEntries).
func testsForPkg(pkgs string) []string {
	plan, _ := resolvePkgsPlan(splitNonEmpty(pkgs), selectOpts{rdepsDepth: defaultRdepsDepth})
	return planEntries(plan)
}

// testsForChanged resolves the working-tree diff as `run --changed` does
// and lists what the selection runs (see planEntries).
func testsForChanged() []string {
	return planEntries(resolveChangedPlan())
}

// planEntries lists what a plan runs: a whole tier as its test
// directories (`./tests/integration/...`), a scoped tier's packages as
// their `./dir` patterns, and a test function run by name as `dir::Name`.
// The static tier runs on every invocation and is left out.
func planEntries(plan []tierPlan) []string {
	out := []string{}
	for _, t := range plan {
		if t.name == tierStatic {
			continue
		}
		if t.pkgs == nil {
			out = append(out, testsForTier(t.name)...)
			continue
		}
		out = append(out, t.pkgs...)
		for _, dir := range sortedFuncDirs(t.funcs) {
			for _, name := range t.funcs[dir] {
				out = append(out, strings.TrimPrefix(dir, "./")+"::"+name)
			}
		}
	}
	return out
}

// listSubsets returns the subset names from groups.subsets.yaml.
// A minimal YAML reader (key:-prefix lines at depth 2) is used so
// we don't depend on yaml.v3 in cmd_list.go. Missing or malformed
// files fail loudly rather than returning a synthesized
// "(could not read ...)" row.
func listSubsets() ([]string, error) {
	path := filepath.Join(repoRoot(), groupsSubsetsFile)
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", groupsSubsetsFile, err)
	}
	out := []string{}
	for _, line := range strings.Split(string(body), "\n") {
		// Subset names are 2-space-indented keys ending in `:`.
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "   ") {
			trimmed := strings.TrimSpace(line)
			if strings.HasSuffix(trimmed, ":") {
				name := strings.TrimSuffix(trimmed, ":")
				if name != "" && !strings.HasPrefix(name, "#") {
					out = append(out, name)
				}
			}
		}
	}
	sort.Strings(out)
	return out, nil
}
