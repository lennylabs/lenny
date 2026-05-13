package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// runList prints the available groups, tiers, or subsets without executing.
//
// Usage:
//
//	lenny-test list                    # both groups and tiers (default)
//	lenny-test list --groups           # only groups
//	lenny-test list --tiers            # only tiers
//	lenny-test list --subsets          # only subsets
//	lenny-test list --specs            # spec sections from spec-map.json
//	lenny-test list --json             # machine-readable
func runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	groupsOnly := fs.Bool("groups", false, "list groups only")
	tiersOnly := fs.Bool("tiers", false, "list tiers only")
	subsetsOnly := fs.Bool("subsets", false, "list subsets only")
	specsOnly := fs.Bool("specs", false, "list spec sections from spec-map.json")
	jsonOut := fs.Bool("json", false, "machine-readable output")
	if err := fs.Parse(args); err != nil {
		return 2
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
		out["subsets"] = []string{"(reads tests/groups.subsets.yaml in Phase 1)"}
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
	if specs, ok := out["specs"].([]string); ok {
		fmt.Println("Spec sections:")
		for _, s := range specs {
			fmt.Printf("  %s\n", s)
		}
		fmt.Println()
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
