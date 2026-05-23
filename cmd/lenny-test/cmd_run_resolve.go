// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// resolveChangedPlan walks `git diff --name-only` against the
// working tree. Equivalent to resolveChangedPlanFor("").
func resolveChangedPlan() []tierPlan {
	return resolveChangedPlanFor("")
}

// resolveChangedPlanFor diffs against the given git ref. An empty
// ref reads the working tree (staged + unstaged + untracked); a
// ref like "main" or "HEAD~5" diffs from that ref to HEAD.
//
// Maps each changed path through tests/change-graph.json to a set
// of tiers; the static tier is always included.
func resolveChangedPlanFor(ref string) []tierPlan {
	paths := changedPathsFor(ref)
	if len(paths) == 0 {
		return nil
	}
	tiers := map[string]bool{"static": true}
	for _, p := range paths {
		for _, t := range tiersForChangedPath(p) {
			tiers[t] = true
		}
	}
	return planFromTierSet(tiers)
}

// resolveSpecsPlan resolves a list of spec section ids through
// spec-map.json. Each id contributes the tier of every test it
// references (tier inferred from the test path).
func resolveSpecsPlan(specs []string) []tierPlan {
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
	tiers := map[string]bool{}
	for _, id := range specs {
		sec, ok := doc.Sections[id]
		if !ok {
			continue
		}
		for _, t := range sec.Tests {
			if tier := inferTierFromPath(t); tier != "" {
				tiers[tier] = true
			}
		}
	}
	if len(tiers) == 0 {
		// Default to unit when the spec hits the package layer only.
		return []tierPlan{{name: "unit", notes: "no test paths resolved; running unit for safety"}}
	}
	return planFromTierSet(tiers)
}

// resolvePkgsPlan resolves package globs through the change graph.
func resolvePkgsPlan(pkgs []string) []tierPlan {
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
	tiers := map[string]bool{}
	for _, pkg := range pkgs {
		for key, perTier := range doc.Globs {
			if key == pkg || strings.HasPrefix(key, pkg) || strings.HasPrefix(pkg, key) {
				for t := range perTier {
					tiers[t] = true
				}
			}
		}
	}
	if len(tiers) == 0 {
		return nil
	}
	return planFromTierSet(tiers)
}

// changedPathsFor returns the changed paths between `ref` and HEAD.
// When ref is empty, falls back to the working-tree diff (staged +
// unstaged + untracked).
func changedPathsFor(ref string) []string {
	if _, err := exec.LookPath("git"); err != nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	var sets [][]string
	if ref == "" {
		sets = [][]string{
			{"diff", "--name-only"},
			{"diff", "--name-only", "--staged"},
			{"ls-files", "--others", "--exclude-standard"},
		}
	} else {
		sets = [][]string{
			{"diff", "--name-only", ref + "..HEAD"},
		}
	}
	for _, args := range sets {
		cmd := exec.Command("git", args...)
		cmd.Dir = repoRoot()
		body, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(body), "\n") {
			line = strings.TrimSpace(line)
			if line != "" && !seen[line] {
				seen[line] = true
				out = append(out, line)
			}
		}
	}
	return out
}

// tiersForChangedPath looks the path up in the change graph and
// returns the set of tier names it implies.
func tiersForChangedPath(path string) []string {
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
	tiers := []string{}
	for key, perTier := range doc.Globs {
		if strings.HasPrefix(path, key) {
			for t := range perTier {
				tiers = append(tiers, t)
			}
		}
	}
	return tiers
}

// inferTierFromPath maps a test path back to a tier name.
func inferTierFromPath(path string) string {
	switch {
	case strings.HasPrefix(path, "tests/tier0_static"):
		return "static"
	case strings.HasPrefix(path, "tests/tier2_component"):
		return "component"
	case strings.HasPrefix(path, "tests/tier3_contract"):
		return "contract"
	case strings.HasPrefix(path, "tests/tier4_integration"):
		return "integration"
	case strings.HasPrefix(path, "tests/tier5_e2e_kind"):
		return "e2e_kind"
	case strings.HasPrefix(path, "tests/tier6_e2e_cloud"):
		return "e2e_cloud"
	case strings.HasPrefix(path, "tests/tier7a_load_local"):
		return "load_local"
	case strings.HasPrefix(path, "tests/tier7b_load_kind"):
		return "load_kind"
	case strings.HasPrefix(path, "tests/tier8_chaos"):
		return "chaos"
	case strings.HasPrefix(path, "tests/tier9_security"):
		return "security"
	case strings.HasPrefix(path, "tests/tier10_conformance"):
		return "conformance"
	case strings.HasPrefix(path, "tests/tier11_docs"):
		return "docs"
	case strings.HasPrefix(path, "tests/tier12_load_cloud"):
		return "load_cloud"
	case strings.HasPrefix(path, "pkg/") || strings.HasSuffix(path, "_test.go"):
		// A pkg/ unit test.
		return "unit"
	}
	return ""
}

// planFromTierSet emits tierPlans in canonical tier order (static
// first, docs last).
func planFromTierSet(tiers map[string]bool) []tierPlan {
	canonical := allTiers()
	plan := []tierPlan{}
	for _, t := range canonical {
		if tiers[t] {
			plan = append(plan, tierPlan{name: t})
		}
	}
	// Any custom tiers (theoretical future additions) tacked on
	// alphabetically.
	custom := []string{}
	for t := range tiers {
		known := false
		for _, c := range canonical {
			if c == t {
				known = true
				break
			}
		}
		if !known {
			custom = append(custom, t)
		}
	}
	sort.Strings(custom)
	for _, t := range custom {
		plan = append(plan, tierPlan{name: t})
	}
	return plan
}
