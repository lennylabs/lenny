// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// The dependent depth of the check before a merge is contributor tooling
// (change 0172, decision 26), so these cases carry no specification
// annotation.

// TestThePremergeDepthSelectsTheChangedPackagesAndTheirDirectImporters
// pins the selection of the check before a merge over the fixture graph:
// the changed package, the packages whose build imports it, and the
// packages whose tests import it, and none of the packages that reach it
// through another package.
func TestThePremergeDepthSelectsTheChangedPackagesAndTheirDirectImporters(t *testing.T) {
	plan := planOf(selectChanged([]string{"pkg/leaf/a.go"}, fixtureGraph(), nil, specEdits{}, selectOpts{rdepsDepth: premergeRdepsDepth}))
	if got, want := tierNamesOf(plan), []string{tierStatic, tierUnit, tierComponent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v: the contract and integration tests reach pkg/leaf only through pkg/mid and pkg/top", got, want)
	}
	if got, want := plan[tierUnit].pkgs, []string{"./pkg/leaf", "./pkg/mid", "./pkg/side"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unit scope = %v, want %v", got, want)
	}
	if got := plan[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./tests/component/leafc"}) {
		t.Errorf("component scope = %v, want the package whose tests import pkg/leaf", got)
	}
	full := planOf(selectChanged([]string{"pkg/leaf/a.go"}, fixtureGraph(), nil, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if !contains(full[tierUnit].pkgs, "./cmd/app") {
		t.Errorf("the full closure lost cmd/app: %v", full[tierUnit].pkgs)
	}
}

// TestThePremergeDepthWalksARowTargetOneHop pins that a package a non-Go
// input row names under `runs` is a changed package at the premerge depth
// too: its direct importers run and theirs do not.
func TestThePremergeDepthWalksARowTargetOneHop(t *testing.T) {
	inputs := &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: []nonGoInput{
		{Paths: []string{"compose/**"}, Runs: []string{"pkg/leaf"}, Reason: "the helper runs it"},
	}}
	plan := planOf(selectChanged([]string{"compose/default.yml"}, fixtureGraph(), inputs, specEdits{}, selectOpts{rdepsDepth: premergeRdepsDepth}))
	if got, want := plan[tierUnit].pkgs, []string{"./pkg/leaf", "./pkg/mid", "./pkg/side"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unit scope = %v, want %v", got, want)
	}
}

// TestTheGateDepthFollowsTheTierRestriction pins the default depth of a
// gate run: the direct importers for a run that --tier restricts, the full
// closure for a run of every tier and for a full run, and the depth the
// command line names whenever it names one.
func TestTheGateDepthFollowsTheTierRestriction(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    gateFlagValues
		want int
	}{
		{"a run that --tier restricts", gateFlagValues{tiers: "static,unit,component,contract"}, premergeRdepsDepth},
		{"a run of every tier", gateFlagValues{}, fullRdepsDepth},
		{"a full run that --tier restricts", gateFlagValues{tiers: "static,unit", all: true}, fullRdepsDepth},
		{"a restricted run with --rdeps-depth -1", gateFlagValues{tiers: "unit", rdeps: -1, rdepsSet: true}, -1},
		{"a run of every tier with --rdeps-depth 2", gateFlagValues{rdeps: 2, rdepsSet: true}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.f.base, tc.f.layout = "main", defaultGateLayout
			o, err := gateRunOptions(tc.f)
			if err != nil {
				t.Fatalf("gateRunOptions: %v", err)
			}
			if o.rdepsDepth != tc.want {
				t.Errorf("depth = %d, want %d", o.rdepsDepth, tc.want)
			}
			args := strings.Join(gatePassArgs(o), " ")
			if !strings.Contains(args, "--rdeps-depth "+strconv.Itoa(tc.want)) {
				t.Errorf("the container's arguments %q do not carry the depth %d", args, tc.want)
			}
		})
	}
}

// TestARestrictedRunAtThePremergeDepthCanPass pins the verdict of each
// depth: a run that --tier restricts passes at the premerge depth and
// above, and is UNVERIFIED below it; a run of every tier is UNVERIFIED at
// any depth at or above zero; and the verdict and its summary record the
// depth.
func TestARestrictedRunAtThePremergeDepthCanPass(t *testing.T) {
	tiers := []string{tierStatic, tierUnit, tierComponent, tierContract}
	for _, tc := range []struct {
		name  string
		tiers []string
		depth int
		want  string
	}{
		{"restricted, direct importers", tiers, premergeRdepsDepth, verdictstatus.VerdictPass},
		{"restricted, two hops", tiers, 2, verdictstatus.VerdictPass},
		{"restricted, full closure", tiers, fullRdepsDepth, verdictstatus.VerdictPass},
		{"restricted, changed packages only", tiers, 0, verdictstatus.VerdictUnverified},
		{"every tier, direct importers", nil, premergeRdepsDepth, verdictstatus.VerdictUnverified},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := stubGateRunner().run(gateOptions{root: ".", base: "main", rdepsDepth: tc.depth, tiers: tc.tiers, environment: "lenny-gate:test"})
			if v.Verdict != tc.want {
				t.Fatalf("verdict %s, want %s (unverified %v)", v.Verdict, tc.want, v.Gate.Unverified)
			}
			if v.Gate.RdepsDepth != tc.depth {
				t.Errorf("gate.rdeps_depth = %d, want %d", v.Gate.RdepsDepth, tc.depth)
			}
			var out bytes.Buffer
			printGateSummary(&out, v, "tests/results/gate-x.json")
			if !strings.Contains(out.String(), depthPhrase(tc.depth)) {
				t.Errorf("the summary does not name the depth:\n%s", out.String())
			}
		})
	}
}

// TestDepthPhraseNamesEachDepth pins the summary's wording of each depth.
func TestDepthPhraseNamesEachDepth(t *testing.T) {
	for depth, want := range map[int]string{
		-1: "every package that depends on them",
		0:  "the changed packages only",
		1:  "import them directly",
		3:  "to 3 hops",
	} {
		if got := depthPhrase(depth); !strings.Contains(got, want) {
			t.Errorf("depthPhrase(%d) = %q, want it to contain %q", depth, got, want)
		}
	}
}

// TestTheDeveloperLoopDefaultsToThePremergeDepth pins that `run --changed`
// and the lists that follow it resolve at the depth of the check before a
// merge, so the developer loop selects what that check runs.
func TestTheDeveloperLoopDefaultsToThePremergeDepth(t *testing.T) {
	stub := useChangedPlan(t, nil, nil)
	resolveChangedPlan()
	if stub.calls != 1 || stub.opts.rdepsDepth != premergeRdepsDepth {
		t.Errorf("resolveChangedPlan resolved with %+v (%d calls), want depth %d", stub.opts, stub.calls, premergeRdepsDepth)
	}
}

// TestRunDefaultsToThePremergeDepth pins the default of `lenny-test run
// --rdeps-depth`: a `--pkg` or `--changed` selection without the flag
// resolves at the premerge depth, and the flag still names another depth.
func TestRunDefaultsToThePremergeDepth(t *testing.T) {
	var got []int
	prev := resolvePkgsPlanFn
	resolvePkgsPlanFn = func(_ []string, opts selectOpts) ([]tierPlan, []string) {
		got = append(got, opts.rdepsDepth)
		return []tierPlan{{name: tierStatic}}, nil
	}
	t.Cleanup(func() { resolvePkgsPlanFn = prev })
	if code := runRun([]string{"--pkg", "pkg/leaf", "--dry-run"}); code != 0 {
		t.Fatalf("run --pkg --dry-run exited %d", code)
	}
	if code := runRun([]string{"--pkg", "pkg/leaf", "--rdeps-depth", "-1", "--dry-run"}); code != 0 {
		t.Fatalf("run --pkg --rdeps-depth -1 --dry-run exited %d", code)
	}
	if want := []int{premergeRdepsDepth, fullRdepsDepth}; !reflect.DeepEqual(got, want) {
		t.Errorf("run resolved --pkg at depths %v, want %v", got, want)
	}
}
