// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// runList prints the available groups, tiers, subsets, or — when
// the discovery flags are supplied — the test set that would run
// for that filter.
//
// Usage:
//
//	lenny-test list                          # groups + tiers (default)
//	lenny-test list --groups | --tiers | --subsets | --specs
//	lenny-test list --tier <name>            # tests in that tier
//	lenny-test list --spec 4.2,12.3          # tests for those sections
//	lenny-test list --pkg pkg/quota          # tests covering those pkgs
//	lenny-test list --changed                # tests for changed files
//	lenny-test list --json                   # machine-readable
func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	groupsOnly := fs.Bool("groups", false, "list groups only")
	tiersOnly := fs.Bool("tiers", false, "list tiers only")
	subsetsOnly := fs.Bool("subsets", false, "list subsets only")
	specsOnly := fs.Bool("specs", false, "list spec sections from spec-map.json")
	tierFilter := fs.String("tier", "", "list tests in the named tier")
	specFilter := fs.String("spec", "", "comma-separated spec sections; resolves through spec-map")
	pkgFilter := fs.String("pkg", "", "comma-separated package globs; resolves through change-graph")
	changed := fs.Bool("changed", false, "list tests affected by uncommitted git changes")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Discovery flags (--tier, --spec, --pkg, --changed) take
	// precedence over the catalog listings.
	if *tierFilter != "" || *specFilter != "" || *pkgFilter != "" || *changed {
		return listDiscovered(*tierFilter, *specFilter, *pkgFilter, *changed, *jsonOut)
	}

	all := !*groupsOnly && !*tiersOnly && !*subsetsOnly && !*specsOnly

	out := map[string]any{}

	if all || *groupsOnly {
		out["groups"] = knownGroups()
	}
	if all || *tiersOnly {
		out["tiers"] = allTiers()
	}
	if *subsetsOnly {
		out["subsets"] = listSubsets()
	}
	if *specsOnly {
		out["specs"] = specSections()
	}

	if *jsonOut {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Println(string(b))
		return 0
	}

	if groups, ok := out["groups"].([]string); ok {
		fmt.Println("Groups:")
		for _, g := range groups {
			fmt.Printf("  %s\n", g)
		}
		fmt.Println()
	}
	if tiers, ok := out["tiers"].([]string); ok {
		fmt.Println("Tiers (in execution order):")
		for _, t := range tiers {
			fmt.Printf("  %s\n", t)
		}
		fmt.Println()
	}
	if subsets, ok := out["subsets"].([]string); ok {
		fmt.Println("Subsets (groups.subsets.yaml):")
		for _, s := range subsets {
			fmt.Printf("  %s\n", s)
		}
		fmt.Println()
	}
	if specs, ok := out["specs"].([]string); ok {
		fmt.Println("Spec sections:")
		for _, s := range specs {
			fmt.Printf("  %s\n", s)
		}
		fmt.Println()
	}

	return 0
}

// listDiscovered resolves a selector into a concrete test set and
// prints it.
func listDiscovered(tier, spec, pkg string, changed, jsonOut bool) int {
	tests := []string{}
	if tier != "" {
		tests = append(tests, testsForTier(tier)...)
	}
	if spec != "" {
		tests = append(tests, testsForSpec(spec)...)
	}
	if pkg != "" {
		tests = append(tests, testsForPkg(pkg)...)
	}
	if changed {
		tests = append(tests, testsForChanged()...)
	}
	// Dedupe.
	seen := map[string]bool{}
	uniq := []string{}
	for _, t := range tests {
		if seen[t] {
			continue
		}
		seen[t] = true
		uniq = append(uniq, t)
	}
	sort.Strings(uniq)
	if jsonOut {
		b, _ := json.MarshalIndent(map[string]any{
			"tier":    tier,
			"spec":    spec,
			"pkg":     pkg,
			"changed": changed,
			"tests":   uniq,
			"count":   len(uniq),
		}, "", "  ")
		fmt.Println(string(b))
		return 0
	}
	fmt.Printf("Resolved %d test path(s) for selector:", len(uniq))
	if tier != "" {
		fmt.Printf(" tier=%s", tier)
	}
	if spec != "" {
		fmt.Printf(" spec=%s", spec)
	}
	if pkg != "" {
		fmt.Printf(" pkg=%s", pkg)
	}
	if changed {
		fmt.Print(" changed=true")
	}
	fmt.Println()
	for _, t := range uniq {
		fmt.Printf("  %s\n", t)
	}
	return 0
}

func knownGroups() []string {
	return []string{
		"pr",
		"pr-fast",
		"nightly",
		"weekly",
		"pre-release",
		"phase-0-gate",
		"phase-1-gate",
		"phase-1.5-gate",
		"phase-2-gate",
		"phase-2.5-gate",
		"phase-2.8-gate",
		"phase-3-gate",
		"phase-3.5-gate",
		"phase-4-gate",
		"phase-4.5-gate",
		"phase-5-gate",
		"phase-5.4-gate",
		"phase-5.5-gate",
		"phase-5.75-gate",
		"phase-5.8-gate",
		"phase-6-gate",
		"phase-6.5-gate",
		"phase-7-gate",
		"phase-8-gate",
		"phase-9-gate",
		"phase-9.5-gate",
		"phase-10-gate",
		"phase-11-gate",
		"phase-11.5-gate",
		"phase-12a-gate",
		"phase-12b-gate",
		"phase-12c-gate",
		"phase-13-gate",
		"phase-13.5-gate",
		"phase-14-gate",
		"phase-14.5-gate",
		"phase-15-gate",
		"phase-16-gate",
		"phase-16.5-gate",
		"phase-17a-gate",
		"phase-17b-gate",
	}
}

func specSections() []string {
	path := filepath.Join(repoRoot(), "tests", "spec-map.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("(could not read spec-map.json: %v)", err)}
	}
	var doc struct {
		Sections map[string]any `json:"sections"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return []string{fmt.Sprintf("(could not parse spec-map.json: %v)", err)}
	}
	out := make([]string, 0, len(doc.Sections))
	for k := range doc.Sections {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
