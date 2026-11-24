// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// The gate's selection record is contributor tooling (change 0172, GS-1),
// so these cases carry no specification annotation.

// TestGateRecordsTheSelectionOfEachTierOfASelectedRun checks that a
// selected run records, for each test tier its plan selected, the whole
// tier or the packages it runs whole and the functions it runs by name,
// leaves out the static tier and the tiers the plan did not select, and
// prints the same in its summary.
func TestGateRecordsTheSelectionOfEachTierOfASelectedRun(t *testing.T) {
	g := stubGateRunner()
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{tiers: []tierPlan{
			{name: tierStatic},
			{name: tierUnit, pkgs: []string{"./pkg/a", "./pkg/b"}, funcs: map[string][]string{"./pkg/c": {"TestX", "TestY"}}},
			{name: tierComponent},
			{name: tierIntegration, pkgs: []string{}, funcs: map[string][]string{"./tests/integration": {"TestZ"}}},
		}}, nil
	}
	v := runStubGate(g)
	want := map[string]gateTierSelection{
		tierUnit:        {Packages: 2, Functions: 2},
		tierComponent:   {Whole: true},
		tierIntegration: {Functions: 1},
	}
	if v.Gate.All || !reflect.DeepEqual(v.Gate.Selection, want) {
		t.Fatalf("gate all %v selection %+v; want a selected run with %+v", v.Gate.All, v.Gate.Selection, want)
	}

	v.finalize()
	payload, err := json.Marshal(v.Gate)
	if err != nil {
		t.Fatal(err)
	}
	for _, frag := range []string{`"unit":{"whole":false,"packages":2,"functions":2}`, `"component":{"whole":true}`, `"integration":{"whole":false,"functions":1}`} {
		if !strings.Contains(string(payload), frag) {
			t.Errorf("the verdict's gate section lacks %s: %s", frag, payload)
		}
	}

	var b strings.Builder
	printGateSummary(&b, v, "x")
	out := b.String()
	for _, frag := range []string{"selected run", "[2 package(s), 2 function(s) by name]", "[whole tier]", "[0 package(s), 1 function(s) by name]"} {
		if !strings.Contains(out, frag) {
			t.Errorf("summary lacks %q:\n%s", frag, out)
		}
	}
	if strings.Contains(out, "full run") {
		t.Errorf("a selected run's summary names a full run:\n%s", out)
	}
}

// TestGateRecordsAFullRunAsWholeForEveryTier checks that a run with --all
// records every test tier of the merge gate as whole, with gate.all set,
// from the plan the gate resolves for --all.
func TestGateRecordsAFullRunAsWholeForEveryTier(t *testing.T) {
	g := stubGateRunner()
	g.plan = gateChangedPlan
	v := g.run(gateOptions{root: ".", base: "no-such-ref", rdepsDepth: defaultRdepsDepth, all: true, environment: "lenny-gate:test"})
	want := map[string]gateTierSelection{}
	for _, name := range gateChangedTiers {
		want[name] = gateTierSelection{Whole: true}
	}
	if !v.Gate.All || !reflect.DeepEqual(v.Gate.Selection, want) {
		t.Fatalf("gate all %v selection %+v; want a full run with %+v", v.Gate.All, v.Gate.Selection, want)
	}
	var b strings.Builder
	printGateSummary(&b, v, "x")
	if !strings.Contains(b.String(), "full run (--all)") {
		t.Errorf("summary does not name the full run:\n%s", b.String())
	}
}

// TestGateRecordsNoSelectionWhenThePlanFails checks that a plan that
// cannot be resolved leaves the selection empty rather than recording a
// tier the gate never planned.
func TestGateRecordsNoSelectionWhenThePlanFails(t *testing.T) {
	g := stubGateRunner()
	g.plan = func(string, int, bool) (resolvedSelector, error) {
		return resolvedSelector{}, errors.New("no merge base")
	}
	if v := runStubGate(g); v.Gate.Selection != nil {
		t.Errorf("selection %+v after a failed plan", v.Gate.Selection)
	}
}

// TestTierSelectionOfCountsNamedTestsAndSubsets checks that a plan
// narrowed by named tests or subsets is not recorded as whole.
func TestTierSelectionOfCountsNamedTestsAndSubsets(t *testing.T) {
	if got := tierSelectionOf(tierPlan{name: tierUnit, tests: []string{"TestA", "TestB"}}); got != (gateTierSelection{Functions: 2}) {
		t.Errorf("named tests = %+v", got)
	}
	if got := tierSelectionOf(tierPlan{name: tierComponent, subsets: []string{"stores"}}); got.Whole {
		t.Errorf("a subset plan is recorded as whole: %+v", got)
	}
	if got := tierSelectionOf(tierPlan{name: tierUnit, pkgs: []string{}}); got != (gateTierSelection{}) {
		t.Errorf("an empty scope = %+v; want no packages and no functions", got)
	}
}
