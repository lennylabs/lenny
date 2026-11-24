// SPDX-License-Identifier: MIT

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The coverage flags of the unit tier are contributor tooling (change 0172,
// GS-4), so these cases carry no specification annotation.

// unitPlanOf returns the unit tier's plan of a resolved selection.
func unitPlanOf(t *testing.T, r resolvedSelector) tierPlan {
	t.Helper()
	for _, p := range r.tiers {
		if p.name == tierUnit {
			return p
		}
	}
	t.Fatalf("the plan holds no unit tier: %+v", r.tiers)
	return tierPlan{}
}

// assertNoCoverage fails when an invocation carries a coverage flag or
// lacks the race detector and -count=1.
func assertNoCoverage(t *testing.T, call []string) {
	t.Helper()
	args := strings.Join(call, " ")
	if strings.Contains(args, "-coverprofile") || strings.Contains(args, "-covermode") {
		t.Errorf("the gate's unit invocation carries a coverage flag: %s", args)
	}
	if !strings.Contains(args, "-race") || !strings.Contains(args, "-count=1") {
		t.Errorf("the gate's unit invocation lacks -race or -count=1: %s", args)
	}
}

// TestGateUnitTierRunsWithRaceAndWithoutCoverage checks that the unit tier
// of the gate's plan, whether the --changed selection or --all, keeps
// -race and -count=1 and writes no coverage profile, even with
// LENNY_COVER_PROFILE naming a path, and that a scoped plan's whole-package
// invocation, the one that writes the profile elsewhere, writes none.
func TestGateUnitTierRunsWithRaceAndWithoutCoverage(t *testing.T) {
	t.Setenv("LENNY_COVER_PROFILE", filepath.Join(t.TempDir(), "cover.out"))

	all, err := gateChangedPlan("no-such-ref", defaultRdepsDepth, true)
	if err != nil {
		t.Fatal(err)
	}
	full := unitPlanOf(t, all)
	if !full.noCoverage {
		t.Errorf("the --all unit plan writes a coverage profile: %+v", full)
	}

	useChangedPlan(t, changedFixturePlan(), nil)
	changed, err := gateChangedPlan("main", defaultRdepsDepth, false)
	if err != nil {
		t.Fatal(err)
	}
	scoped := unitPlanOf(t, changed)
	if !scoped.noCoverage || scoped.pkgs == nil {
		t.Errorf("the --changed unit plan = %+v; want a scoped plan without coverage", scoped)
	}

	calls := recordGoTest(t)
	if st, _, _ := runUnitTier(full); st != "pass" {
		t.Fatalf("the --all unit tier = %q", st)
	}
	scoped.funcs = map[string][]string{"./pkg/c": {"TestC"}}
	if st, _, _ := runUnitTier(scoped); st != "pass" {
		t.Fatalf("the scoped unit tier = %q", st)
	}
	if len(*calls) != 3 {
		t.Fatalf("invocations = %v; want ./... then the whole packages and the by-name run", *calls)
	}
	if last := (*calls)[0][len((*calls)[0])-1]; last != "./..." {
		t.Errorf("the --all invocation runs %q; want ./...", last)
	}
	for _, call := range *calls {
		assertNoCoverage(t, call)
	}
}

// TestRunChangedAndTierUnitStillWriteTheCoverageProfile checks that the
// unit tier of `run --changed` and of `--tier unit` keeps writing the
// coverage profile with -covermode=atomic, because `lenny-test coverage
// --diff` reads it.
func TestRunChangedAndTierUnitStillWriteTheCoverageProfile(t *testing.T) {
	profile := filepath.Join(t.TempDir(), "results", "cover.out")
	t.Setenv("LENNY_COVER_PROFILE", profile)
	useChangedPlan(t, changedFixturePlan(), nil)

	for _, s := range []selector{{changed: true}, {tiers: []string{tierUnit}}} {
		p := unitPlanOf(t, mustResolve(t, s))
		if p.noCoverage {
			t.Errorf("%s: the unit plan drops the coverage profile", describe(s))
		}
		calls := recordGoTest(t)
		if st, _, _ := runUnitTier(p); st != "pass" {
			t.Fatalf("%s: unit tier = %q", describe(s), st)
		}
		if len(*calls) == 0 {
			t.Fatalf("%s: the unit tier ran nothing", describe(s))
		}
		args := strings.Join((*calls)[0], " ")
		if !strings.Contains(args, "-coverprofile="+profile) || !strings.Contains(args, "-covermode=atomic") || !strings.Contains(args, "-race") {
			t.Errorf("%s: unit invocation = %s; want -race and the coverage profile at %s", describe(s), args, profile)
		}
	}
}
