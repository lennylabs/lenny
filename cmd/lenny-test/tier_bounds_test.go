// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"strings"
	"testing"
)

// The tests in this file pin the tier bounds of change 0116 (CS-7). They
// test contributor tooling, which implements the testing model rather
// than a specification behavior, so they carry no spec annotation.

// wideChangedPlan is a selection that reaches tiers inside and outside
// every gate: a scoped unit tier, the component and integration tiers,
// the Kind tier, and the docs tier.
func wideChangedPlan() []tierPlan {
	return []tierPlan{
		{name: tierStatic},
		{name: tierUnit, pkgs: []string{"./pkg/a"}},
		{name: tierComponent, pkgs: []string{"./tests/component/a"}},
		{name: tierIntegration},
		{name: tierE2EKind},
		{name: tierDocs},
	}
}

// TestGateBoundsTheChangedSelection pins that --gate keeps the tiers of
// the named gate, with their package scope, and drops the others.
func TestGateBoundsTheChangedSelection(t *testing.T) {
	cases := []struct {
		gate string
		want []string
	}{
		{gateMerge, []string{tierStatic, tierUnit, tierComponent, tierIntegration, tierDocs}},
		{gateArea, []string{tierE2EKind, tierDocs}},
		{gateRelease, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.gate, func(t *testing.T) {
			useChangedPlan(t, wideChangedPlan(), nil)
			r := mustResolve(t, selector{changed: true, since: "main", gate: tc.gate})
			if got := planNames(r.tiers); strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("--gate %s kept %v; want %v", tc.gate, got, tc.want)
			}
			for _, p := range r.tiers {
				if p.name == tierUnit && !reflect.DeepEqual(p.pkgs, []string{"./pkg/a"}) {
					t.Errorf("--gate %s lost the unit scope: %v", tc.gate, p.pkgs)
				}
			}
			if len(r.notes) != 0 {
				t.Errorf("a gate bound raised notes: %v", r.notes)
			}
		})
	}
}

// TestGateAloneRunsTheGateTiersWhole pins --gate without a discovery
// selector: each tier of the gate runs whole, and no diff is read.
func TestGateAloneRunsTheGateTiersWhole(t *testing.T) {
	stub := useChangedPlan(t, wideChangedPlan(), nil)
	for _, gate := range gateNames() {
		want, _ := gateTiers(gate)
		r := mustResolve(t, selector{gate: gate})
		if got := planNames(r.tiers); !reflect.DeepEqual(got, want) {
			t.Errorf("--gate %s = %v; want %v", gate, got, want)
		}
		for _, p := range r.tiers {
			if p.pkgs != nil {
				t.Errorf("--gate %s scoped the %s tier to %v", gate, p.name, p.pkgs)
			}
		}
	}
	if stub.calls != 0 {
		t.Errorf("--gate alone read the diff")
	}
}

// TestGateTiersFollowTheGatesTable pins the gate table to the "Gates"
// table of the test coverage rule: the merge gate runs no tier that
// needs a cluster, the journeys tier is in no gate, and every gate tier
// is a tier.
func TestGateTiersFollowTheGatesTable(t *testing.T) {
	merge, _ := gateTiers(gateMerge)
	for _, tier := range []string{tierE2EKind, tierE2ECloud, tierLoadKind, tierLoadCloud, tierChaos, tierSecurity} {
		if contains(merge, tier) {
			t.Errorf("the merge gate runs %s", tier)
		}
	}
	for _, gate := range gateNames() {
		tiers, ok := gateTiers(gate)
		if !ok || len(tiers) == 0 {
			t.Fatalf("gate %s has no tiers", gate)
		}
		if !reflect.DeepEqual(tiers, sortByTierOrder(tiers)) {
			t.Errorf("gate %s lists its tiers out of execution order: %v", gate, tiers)
		}
		for _, tier := range tiers {
			if !contains(allTiers(), tier) {
				t.Errorf("gate %s names %q, which is not a tier", gate, tier)
			}
		}
	}
	if _, ok := gateTiers("nightly"); ok {
		t.Errorf("nightly is not a gate")
	}
	if _, ok := gateTiers(""); ok {
		t.Errorf("the empty name is not a gate")
	}
}

// TestUnboundedDiscoveryRunsOnlyInfrastructureFreeTiers pins that a
// discovery selection with neither --tier nor --gate keeps the static,
// unit, component, and contract tiers, and names the tiers it left out.
func TestUnboundedDiscoveryRunsOnlyInfrastructureFreeTiers(t *testing.T) {
	useChangedPlan(t, wideChangedPlan(), []string{"selection note"})
	r := mustResolve(t, selector{changed: true, since: "main"})
	if got, want := planNames(r.tiers), []string{tierStatic, tierUnit, tierComponent}; !reflect.DeepEqual(got, want) {
		t.Errorf("unbounded --changed = %v; want %v", got, want)
	}
	if len(r.notes) != 2 || r.notes[0] != "selection note" {
		t.Fatalf("notes = %v; want the selection's note and the bound's note", r.notes)
	}
	for _, tier := range []string{tierIntegration, tierE2EKind, tierDocs} {
		if !strings.Contains(r.notes[1], tier) {
			t.Errorf("the bound's note %q does not name the %s tier it left out", r.notes[1], tier)
		}
	}

	// A selection inside the bound adds no note.
	useChangedPlan(t, []tierPlan{{name: tierStatic}, {name: tierContract}}, nil)
	r = mustResolve(t, selector{changed: true})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierStatic, tierContract}) || len(r.notes) != 0 {
		t.Errorf("a selection inside the bound = %v, notes %v", got, r.notes)
	}

	// An empty selection falls back to the static tier.
	useChangedPlan(t, nil, nil)
	r = mustResolve(t, selector{changed: true})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierStatic}) {
		t.Errorf("an empty selection = %v; want the static tier", got)
	}

	// --pkg is a discovery selector and takes the same bound.
	prev := resolvePkgsPlanFn
	resolvePkgsPlanFn = func([]string, selectOpts) ([]tierPlan, []string) { return wideChangedPlan(), nil }
	t.Cleanup(func() { resolvePkgsPlanFn = prev })
	r = mustResolve(t, selector{pkgs: []string{"pkg/a"}})
	if planHasTier(r.tiers, tierIntegration) || !planHasTier(r.tiers, tierUnit) {
		t.Errorf("unbounded --pkg = %v", planNames(r.tiers))
	}
}

// TestTierListRestrictsAndRunsInOrder pins a list-valued --tier: alone it
// runs each named tier whole in execution order, and beside a discovery
// selector it keeps the named tiers the selection reaches, notes each one
// it does not, and lifts the infrastructure-free bound for the tiers it
// names.
func TestTierListRestrictsAndRunsInOrder(t *testing.T) {
	stub := useChangedPlan(t, wideChangedPlan(), nil)
	tiers, err := parseTierList("docs, unit,unit")
	if err != nil {
		t.Fatalf("parseTierList: %v", err)
	}
	if !reflect.DeepEqual(tiers, []string{tierUnit, tierDocs}) {
		t.Fatalf("parseTierList = %v; want unit then docs, once each", tiers)
	}
	r := mustResolve(t, selector{tiers: tiers})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierUnit, tierDocs}) || r.tiers[0].pkgs != nil {
		t.Errorf("--tier unit,docs alone = %+v", r.tiers)
	}
	if stub.calls != 0 {
		t.Errorf("--tier alone read the diff")
	}

	r = mustResolve(t, selector{tiers: []string{tierUnit, tierIntegration, tierContract}, changed: true})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierUnit, tierIntegration}) {
		t.Errorf("--tier unit,integration,contract --changed = %v", got)
	}
	if len(r.notes) != 1 || !strings.Contains(r.notes[0], "contract") {
		t.Errorf("notes = %v; want one naming the unreached contract tier", r.notes)
	}

	// --tier and --gate intersect.
	r = mustResolve(t, selector{tiers: []string{tierUnit, tierE2EKind}, gate: gateMerge, changed: true})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierUnit}) {
		t.Errorf("--tier unit,e2e_kind --gate merge = %v", got)
	}
}

// TestTierListAndGateRefusals pins the values that cannot resolve: an
// unknown tier, the journeys tier in a list, an empty list, an unknown
// gate, and a --subset beside more than one tier.
func TestTierListAndGateRefusals(t *testing.T) {
	for _, value := range []string{"unit,bogus", "journeys", "unit,journeys", " , "} {
		if _, err := parseTierList(value); err == nil {
			t.Errorf("parseTierList(%q) accepted", value)
		}
	}
	if tiers, err := parseTierList(""); err != nil || tiers != nil {
		t.Errorf("parseTierList(\"\") = %v, %v; want no tiers", tiers, err)
	}
	refused := []selector{
		{gate: "nightly"},
		{tiers: []string{"bogus"}},
		{tiers: []string{tierUnit, tierComponent}, subsets: []string{"stores"}},
		{subsets: []string{"stores"}, changed: true},
	}
	for _, s := range refused {
		if _, err := s.resolve(); err == nil {
			t.Errorf("resolve(%s, subsets=%v) accepted", describe(s), s.subsets)
		}
	}
}

// TestRunRefusesTheRemovedMaxTierFlag pins that --max-tier is gone: run
// reads it as an unknown flag and returns the usage exit code, as it does
// for an unknown tier or gate.
func TestRunRefusesTheRemovedMaxTierFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--max-tier", "unit", "--dry-run"},
		{"--tier", "unit,bogus", "--dry-run"},
		{"--tier", "unit,journeys", "--dry-run"},
		{"--gate", "nightly", "--dry-run"},
	} {
		if code := runRun(args); code != 2 {
			t.Errorf("runRun(%q) = %d; want 2", args, code)
		}
	}
	if code := runRun([]string{"-h"}); code != 0 {
		t.Errorf("runRun(-h) = %d; want 0", code)
	}
}

// TestGroupsBoundTheirSelection pins the groups of change 0116: pr-fast
// keeps the static, unit, and component tiers of the working tree's
// selection, and pr runs the merge gate's tiers whole.
func TestGroupsBoundTheirSelection(t *testing.T) {
	useChangedPlan(t, append(wideChangedPlan(), tierPlan{name: tierContract}), nil)
	if got := planNames(tiersForGroup("pr-fast")); !reflect.DeepEqual(got, []string{tierStatic, tierUnit, tierComponent}) {
		t.Errorf("pr-fast = %v", got)
	}
	want, _ := gateTiers(gateMerge)
	if got := planNames(tiersForGroup("pr")); !reflect.DeepEqual(got, want) {
		t.Errorf("pr = %v; want the merge gate's tiers %v", got, want)
	}
	r := mustResolve(t, selector{group: "pr", gate: gateArea})
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierDocs}) {
		t.Errorf("--group pr --gate area = %v; want the docs tier the two share", got)
	}
}

// TestGateChangedPlanKeepsTheTiersTheGateRuns pins the merge gate's plan:
// `run --changed --gate merge` with the docs tier left to the gate's
// general documentation gates.
func TestGateChangedPlanKeepsTheTiersTheGateRuns(t *testing.T) {
	stub := useChangedPlan(t, wideChangedPlan(), nil)
	r, err := gateChangedPlan("main", -1, false)
	if err != nil {
		t.Fatalf("gateChangedPlan: %v", err)
	}
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierStatic, tierUnit, tierComponent, tierIntegration}) {
		t.Errorf("gate plan = %v", got)
	}
	if stub.ref != "main" || stub.opts.rdepsDepth != -1 {
		t.Errorf("gate plan resolved ref %q opts %+v", stub.ref, stub.opts)
	}
	r, err = gateChangedPlan("main", -1, true)
	if err != nil {
		t.Fatalf("gateChangedPlan --all: %v", err)
	}
	if got := planNames(r.tiers); !reflect.DeepEqual(got, []string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration}) {
		t.Errorf("gate --all plan = %v", got)
	}
}
