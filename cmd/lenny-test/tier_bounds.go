// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"sort"
	"strings"
)

// Gate names. A gate states which tiers run and what must be green
// before a merge, a milestone exit, or a release
// (.claude/rules/test-coverage.md, "Gates"). `--gate <name>` bounds a
// selection at the tiers of that gate (change 0116, CS-7), in place of
// the tier order that --max-tier read.
const (
	gateMerge   = "merge"
	gateArea    = "area"
	gateRelease = "release"
)

// gateNames returns the gate names in the order the rule lists them.
func gateNames() []string {
	return []string{gateMerge, gateArea, gateRelease}
}

// gateTiers returns the tiers of the named gate in execution order, and
// false for a name that is not a gate. The table follows the "Gates"
// table of .claude/rules/test-coverage.md. The merge gate's docs entry
// stands for its general documentation gates, which `lenny-test gate`
// runs from gateDocsFiles; a `run --gate merge` that selects the docs
// tier runs the whole tier. The area gate's chaos entry stands for the
// chaos core, and the journeys tier, which is not a go test tier, runs
// through `lenny-test --tier journeys` at the area and release gates.
func gateTiers(name string) ([]string, bool) {
	switch name {
	case gateMerge:
		return []string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration, tierDocs}, true
	case gateArea:
		return []string{tierE2EKind, tierChaos, tierSecurity, tierConformance, tierDocs}, true
	case gateRelease:
		return []string{tierE2ECloud, tierLoadLocal, tierLoadKind, tierChaos, tierLoadCloud}, true
	}
	return nil, false
}

// infraFreeTiers are the tiers a discovery selection (--changed, --since,
// --spec, or --pkg) runs when neither --tier nor --gate bounds it: the
// tiers that start no compose stack and no cluster (change 0116, CS-7).
// The component tier needs a Docker daemon and the envtest binaries,
// which the developer's host already carries, and starts neither. A tier
// outside this list runs only when a gate or --tier names it, so the
// inner loop does not start infrastructure by surprise.
var infraFreeTiers = []string{tierStatic, tierUnit, tierComponent, tierContract}

// prFastTiers bound the pr-fast group's change selection: the developer
// inner loop, which runs no contract tier (tests/groups.yaml).
var prFastTiers = []string{tierStatic, tierUnit, tierComponent}

// parseTierList splits a --tier value into tier names and refuses a name
// that is not a tier. The journeys tier runs through its own command and
// is refused here, so a list cannot reach it.
func parseTierList(value string) ([]string, error) {
	names := splitNonEmpty(value)
	if strings.TrimSpace(value) != "" && len(names) == 0 {
		return nil, fmt.Errorf("--tier %q names no tier", value)
	}
	known := allTiers()
	seen := map[string]bool{}
	var out []string
	for _, n := range names {
		if n == tierJourneys {
			return nil, fmt.Errorf("--tier %s takes its own flags and runs alone: run `lenny-test --tier %s`", tierJourneys, tierJourneys)
		}
		if !contains(known, n) {
			return nil, fmt.Errorf("unknown tier %q. Valid tiers: %s", n, strings.Join(known, ", "))
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return sortByTierOrder(out), nil
}

// sortByTierOrder returns the tier names in execution order.
func sortByTierOrder(names []string) []string {
	out := append([]string(nil), names...)
	order := allTiers()
	sort.SliceStable(out, func(i, j int) bool { return indexOf(order, out[i]) < indexOf(order, out[j]) })
	return out
}

// wholeTierPlans returns a plan that runs each named tier whole.
func wholeTierPlans(names []string, subsets []string) []tierPlan {
	plan := make([]tierPlan, 0, len(names))
	for _, n := range names {
		plan = append(plan, tierPlan{name: n, subsets: subsets})
	}
	return plan
}

// keepTiers returns the entries of plan whose tier is in keep, in plan
// order, and the names of the tiers it dropped.
func keepTiers(plan []tierPlan, keep []string) ([]tierPlan, []string) {
	kept := []tierPlan{}
	var dropped []string
	for _, p := range plan {
		if contains(keep, p.name) {
			kept = append(kept, p)
			continue
		}
		dropped = append(dropped, p.name)
	}
	return kept, dropped
}

// restrictToTiers keeps the entries of plan that --tier names, with their
// tests and package scope, and applies the --subset filter. Each named
// tier the plan does not select runs nothing and carries a note.
func restrictToTiers(plan []tierPlan, tiers []string, subsets []string) ([]tierPlan, []string) {
	kept, _ := keepTiers(plan, tiers)
	if len(subsets) > 0 {
		for i := range kept {
			kept[i].subsets = subsets
		}
	}
	var notes []string
	for _, t := range tiers {
		if !planHasTier(kept, t) {
			notes = append(notes, fmt.Sprintf("the selection does not reach the %s tier, so --tier %s runs nothing", t, t))
		}
	}
	return kept, notes
}

// planHasTier reports whether plan holds an entry for tier.
func planHasTier(plan []tierPlan, tier string) bool {
	for _, p := range plan {
		if p.name == tier {
			return true
		}
	}
	return false
}

// boundByGate keeps the entries of plan that the named gate runs. It
// returns no note: naming the gate states the bound.
func boundByGate(plan []tierPlan, gate string) []tierPlan {
	tiers, _ := gateTiers(gate)
	kept, _ := keepTiers(plan, tiers)
	return kept
}

// boundToInfraFree keeps the entries of an unbounded discovery plan that
// need no infrastructure, and returns a note naming the tiers the
// selection reached and the bound left out, so the developer knows what
// a gate or --tier would add.
func boundToInfraFree(plan []tierPlan) ([]tierPlan, string) {
	kept, dropped := keepTiers(plan, infraFreeTiers)
	if len(dropped) == 0 {
		return kept, ""
	}
	return kept, fmt.Sprintf("the selection also reaches the %s tier(s), which run only when --gate or --tier names them (`lenny-test gate` runs the merge gate)", strings.Join(dropped, ", "))
}
