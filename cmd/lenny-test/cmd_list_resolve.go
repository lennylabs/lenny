// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
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
		return []string{"./tests/tier0_static/..."}
	case "unit":
		return []string{"./..."}
	case "component":
		return []string{"./tests/tier2_component/..."}
	case "contract":
		return []string{"./tests/tier3_contract/..."}
	case "integration":
		return []string{"./tests/tier4_integration/..."}
	case "e2e_kind":
		return []string{"./tests/tier5_e2e_kind/..."}
	case "e2e_cloud":
		return []string{"./tests/tier6_e2e_cloud/..."}
	case "load_local":
		return []string{"./tests/tier7a_load_local/..."}
	case "load_kind":
		return []string{"./tests/tier7b_load_kind/..."}
	case "chaos":
		return []string{"./tests/tier8_chaos/..."}
	case "security":
		return []string{"./tests/tier9_security/..."}
	case "conformance":
		return []string{"./tests/tier10_conformance/..."}
	case "docs":
		return []string{"./tests/tier11_docs/..."}
	case "load_cloud":
		return []string{"./tests/tier12_load_cloud/..."}
	}
	return nil
}

// testsForSpec resolves spec sections to test paths via spec-map.json.
func testsForSpec(specs string) []string {
	body, err := os.ReadFile(filepath.Join(repoRoot(), specMapFile))
	if err != nil {
		return nil
	}
	var doc struct {
		Sections map[string]struct {
			Tests []string `json:"tests"`
		} `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := []string{}
	for _, id := range splitNonEmpty(specs) {
		sec, ok := doc.Sections[id]
		if !ok {
			continue
		}
		out = append(out, sec.Tests...)
	}
	return out
}

// testsForPkg resolves package globs to test paths via change-graph.json.
func testsForPkg(pkgs string) []string {
	body, err := os.ReadFile(filepath.Join(repoRoot(), changeGraphFile))
	if err != nil {
		return nil
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	out := []string{}
	for _, pkg := range splitNonEmpty(pkgs) {
		// Try exact match first, then prefix match.
		if tiers, ok := doc.Globs[pkg]; ok {
			out = append(out, flattenTiers(tiers)...)
			continue
		}
		for key, tiers := range doc.Globs {
			if strings.HasPrefix(key, pkg) || strings.HasPrefix(pkg, key) {
				out = append(out, flattenTiers(tiers)...)
			}
		}
	}
	return out
}

// testsForChanged walks `git diff --name-only` and resolves each
// path through the change graph.
func testsForChanged() []string {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	// Staged + unstaged + untracked.
	out := []string{}
	for _, args := range [][]string{
		{"diff", "--name-only"},
		{"diff", "--name-only", "--staged"},
		{"ls-files", "--others", "--exclude-standard"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot()
		body, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				out = append(out, line)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	// Resolve each changed path through the change graph.
	body, err := os.ReadFile(filepath.Join(repoRoot(), changeGraphFile))
	if err != nil {
		return nil
	}
	var doc struct {
		Globs map[string]map[string][]string `json:"globs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil
	}
	resolved := []string{}
	for _, path := range out {
		for key, tiers := range doc.Globs {
			if strings.HasPrefix(path, key) {
				resolved = append(resolved, flattenTiers(tiers)...)
			}
		}
	}
	return resolved
}

func flattenTiers(tiers map[string][]string) []string {
	out := []string{}
	for _, paths := range tiers {
		out = append(out, paths...)
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
