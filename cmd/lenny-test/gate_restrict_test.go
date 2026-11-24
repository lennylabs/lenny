// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The gate's tier and coverage options are contributor tooling (change
// 0172, decision 9), so these cases carry no specification annotation.

// TestGateTierRestrictsTheRunToTheNamedTiers checks that a run with
// --tier runs only the named tiers, keeps the diff selection of each
// within them, records the others as not selected, and records the
// restriction in the verdict, the command, and the summary.
func TestGateTierRestrictsTheRunToTheNamedTiers(t *testing.T) {
	l := &laneLog{}
	g := laneRunner(l, nil, nil)
	tiers := []string{tierStatic, tierUnit, tierComponent, tierContract}
	v := g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, tiers: tiers, environment: "lenny-gate:test"})
	v.finalize()
	for _, tier := range tiers {
		if l.index("end "+tier) < 0 {
			t.Errorf("the restricted run did not run the %s tier: %v", tier, l.events)
		}
	}
	for _, tier := range []string{tierIntegration, tierDocs} {
		if l.index("start "+tier) >= 0 {
			t.Errorf("the restricted run ran the %s tier: %v", tier, l.events)
		}
		if got := v.Tiers[tier].Status; got != verdictstatus.NotSelected {
			t.Errorf("tier %s status %q; want not-selected", tier, got)
		}
	}
	if got := l.plans[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/a"}) {
		t.Errorf("the restricted unit tier ran %v; want the diff selection ./pkg/a", got)
	}
	if _, ok := v.Gate.Selection[tierIntegration]; ok {
		t.Errorf("the selection records a tier the run was restricted from: %+v", v.Gate.Selection)
	}
	if v.Verdict != verdictstatus.VerdictPass || !reflect.DeepEqual(v.Gate.RestrictedTo, tiers) {
		t.Errorf("verdict %s restricted to %v", v.Verdict, v.Gate.RestrictedTo)
	}
	if !strings.Contains(v.Command, "--tier static,unit,component,contract") {
		t.Errorf("command %q lacks the restriction", v.Command)
	}
	var b strings.Builder
	printGateSummary(&b, v, "x")
	if !strings.Contains(b.String(), "restricted to static, unit, component, contract") {
		t.Errorf("summary lacks the restriction:\n%s", b.String())
	}
}

// TestParseGateTiersRefusesTiersOutsideTheMergeGate checks the parsing of
// the gate's --tier: merge-gate tiers in tier order, and a refusal for an
// unknown tier and for a tier the merge gate does not run.
func TestParseGateTiersRefusesTiersOutsideTheMergeGate(t *testing.T) {
	got, err := parseGateTiers("contract,static,unit")
	if err != nil || !reflect.DeepEqual(got, []string{tierStatic, tierUnit, tierContract}) {
		t.Errorf("parseGateTiers = %v, %v", got, err)
	}
	if got, err := parseGateTiers(""); err != nil || got != nil {
		t.Errorf("an empty --tier = %v, %v; want no restriction", got, err)
	}
	for _, bad := range []string{"security", "load_local", "e2e_kind", "nightly", ",", "journeys"} {
		if _, err := parseGateTiers(bad); err == nil {
			t.Errorf("parseGateTiers(%q) succeeded", bad)
		}
	}
	if _, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, layout: defaultGateLayout, tiers: "security"}); err == nil {
		t.Error("gateRunOptions accepted a tier outside the merge gate")
	}
	o, err := gateRunOptions(gateFlagValues{base: "main", rdeps: -1, layout: defaultGateLayout, tiers: "unit,static", cover: true})
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(gatePassArgs(o), " ")
	if !strings.Contains(args, "--tier static,unit") || !strings.Contains(args, "--cover") {
		t.Errorf("pass args %q lack the restriction or --cover", args)
	}
}

// TestGateCoverWritesTheProfileCoverageDiffReads checks --cover: the unit
// tier's plan runs with coverage on, an earlier profile is removed first,
// the verdict records the profile, and the profile path is the one
// `lenny-test coverage --diff` reads.
func TestGateCoverWritesTheProfileCoverageDiffReads(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "tests", "results", "cover.out")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &laneLog{}
	g := laneRunner(l, nil, nil)
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{tiers: []tierPlan{{name: tierUnit, pkgs: []string{"./pkg/a"}, noCoverage: true}}}, nil
	}
	unit := g.exec[tierUnit]
	g.exec[tierUnit] = func(p tierPlan) (string, string, *tierResult) {
		if _, err := os.Stat(profile); err == nil {
			t.Error("the earlier profile was still there when the unit tier started")
		}
		if err := os.WriteFile(profile, []byte("mode: atomic\n"), 0o644); err != nil {
			t.Error(err)
		}
		return unit(p)
	}
	v := g.run(gateOptions{root: root, base: "main", rdepsDepth: defaultRdepsDepth, cover: true, coverProfile: profile, environment: "lenny-gate:test"})
	if l.plans[tierUnit].noCoverage {
		t.Error("the unit tier of a --cover run writes no profile")
	}
	if !v.Gate.Cover || v.Gate.CoverProfile != "tests/results/cover.out" {
		t.Errorf("gate cover %v profile %q", v.Gate.Cover, v.Gate.CoverProfile)
	}
	var b strings.Builder
	printGateSummary(&b, v, "x")
	if !strings.Contains(b.String(), "coverage:    tests/results/cover.out") {
		t.Errorf("summary lacks the profile:\n%s", b.String())
	}

	t.Setenv("LENNY_COVER_PROFILE", profile)
	if got := coverProfilePath(); got != profile {
		t.Errorf("the gate writes %s and coverage --diff reads LENNY_COVER_PROFILE %s", got, profile)
	}
	plan := l.plans[tierUnit]
	calls := recordGoTest(t)
	runUnitTier(plan)
	if args := strings.Join((*calls)[0], " "); !strings.Contains(args, "-coverprofile="+profile) {
		t.Errorf("the unit tier of a --cover run = %s; want the profile at %s", args, profile)
	}
}

// TestGateCoverWithoutAUnitTierWritesAnEmptyProfile checks that a --cover
// run whose selection holds no unit tier leaves a profile that covers
// nothing in place of an earlier one, and that a run without --cover
// leaves an earlier profile as it is.
func TestGateCoverWithoutAUnitTierWritesAnEmptyProfile(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "cover.out")
	if err := os.WriteFile(profile, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	g := laneRunner(&laneLog{}, nil, nil)
	g.plan = func(string, int, bool) (resolvedSelector, error) { return resolvedSelector{}, nil }

	g.run(gateOptions{root: root, base: "main", rdepsDepth: defaultRdepsDepth, coverProfile: profile, environment: "lenny-gate:test"})
	if body, _ := os.ReadFile(profile); string(body) != "stale" {
		t.Errorf("a run without --cover changed the profile to %q", body)
	}
	v := g.run(gateOptions{root: root, base: "main", rdepsDepth: defaultRdepsDepth, cover: true, coverProfile: profile, environment: "lenny-gate:test"})
	if body, _ := os.ReadFile(profile); string(body) != "mode: atomic\n" {
		t.Errorf("profile = %q; want one that covers nothing", body)
	}
	if v.Gate.CoverProfile != "cover.out" {
		t.Errorf("gate cover profile %q", v.Gate.CoverProfile)
	}
}

// TestGateCoverFailsWhenTheProfileCannotBeReadied checks that a --cover
// run whose earlier profile cannot be removed fails rather than leaving
// coverage --diff to read it.
func TestGateCoverFailsWhenTheProfileCannotBeReadied(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cover.out")
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0o755); err != nil {
		t.Fatal(err)
	}
	g := laneRunner(&laneLog{}, nil, nil)
	v := g.run(gateOptions{root: ".", base: "main", rdepsDepth: defaultRdepsDepth, cover: true, coverProfile: dir, environment: "lenny-gate:test"})
	if v.Verdict != verdictstatus.VerdictFail || !strings.Contains(v.Tiers[tierUnit].Detail+v.Tiers[tierUnit].Reason, "remove the earlier coverage profile") {
		t.Errorf("verdict %s unit %+v", v.Verdict, v.Tiers[tierUnit])
	}
}

// TestDiffCoverageBelowPassesADiffWithNothingToCover checks the threshold
// of coverage --diff: a share under the threshold fails, a share at or
// above it passes, and a diff with no coverable changed lines passes.
func TestDiffCoverageBelowPassesADiffWithNothingToCover(t *testing.T) {
	cases := []struct {
		threshold        float64
		covered, changed int
		want             bool
	}{
		{0.80, 7, 10, true},
		{0.80, 8, 10, false},
		{0.80, 0, 0, false},
		{0, 0, 10, false},
	}
	for _, tc := range cases {
		if got := diffCoverageBelow(tc.threshold, tc.covered, tc.changed); got != tc.want {
			t.Errorf("diffCoverageBelow(%v, %d, %d) = %v; want %v", tc.threshold, tc.covered, tc.changed, got, tc.want)
		}
	}
}
