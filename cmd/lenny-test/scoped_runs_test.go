// SPDX-License-Identifier: MIT

package main

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// Running a scoped tier plan is contributor tooling (change 0116, CS-4 and
// CS-5), so these cases carry no specification annotation.

// recordGoTest replaces the `go test` runner with one that records each
// invocation's arguments and returns the given results in order.
func recordGoTest(t *testing.T, results ...*tierResult) *[][]string {
	t.Helper()
	var calls [][]string
	prev := runGoTestJSONFn
	runGoTestJSONFn = func(args ...string) (*tierResult, error) {
		calls = append(calls, args)
		if len(calls) <= len(results) {
			r := results[len(calls)-1]
			if r.Failed > 0 {
				return r, errors.New("exit status 1")
			}
			return r, nil
		}
		return &tierResult{}, nil
	}
	t.Cleanup(func() { runGoTestJSONFn = prev })
	return &calls
}

// TestScopedRunsRunPackagesWholeAndFunctionsByName pins the invocations a
// scoped plan needs: one for the whole packages, one with a `-run` pattern
// for the by-name packages, and the by-name packages run whole when the
// pattern would pass its bound.
func TestScopedRunsRunPackagesWholeAndFunctionsByName(t *testing.T) {
	got := scopedRuns([]string{"./pkg/a"}, map[string][]string{"./pkg/c": {"TestC"}, "./pkg/b": {"TestB.x", "TestC"}})
	want := [][]string{{"./pkg/a"}, {"-run", `^(TestB\.x|TestC)$`, "./pkg/b", "./pkg/c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tails = %v, want %v", got, want)
	}
	if got := scopedRuns([]string{}, nil); got != nil {
		t.Errorf("an empty scope needs no invocation; got %v", got)
	}
	if got := scopedRuns(nil, map[string][]string{"./pkg/b": {"TestB"}}); !reflect.DeepEqual(got, [][]string{{"-run", "^(TestB)$", "./pkg/b"}}) {
		t.Errorf("by-name only = %v", got)
	}
	many := []string{}
	for i := 0; len(runPattern(many)) <= maxRunPatternBytes; i++ {
		many = append(many, fmt.Sprintf("TestGenerated%06d", i))
	}
	got = scopedRuns([]string{"./pkg/a"}, map[string][]string{"./pkg/b": many})
	if !reflect.DeepEqual(got, [][]string{{"./pkg/a", "./pkg/b"}}) {
		t.Errorf("an oversized pattern must run its packages whole; got %d tail(s), first %v", len(got), got[0])
	}
}

// TestUnitTierRunsEachInvocationAndWritesCoverageOnce pins the unit tier
// over a scoped plan: the whole-package invocation writes the coverage
// profile, the by-name invocation does not, and the results merge.
func TestUnitTierRunsEachInvocationAndWritesCoverageOnce(t *testing.T) {
	t.Setenv("LENNY_COVER_PROFILE", t.TempDir()+"/cover.out")
	calls := recordGoTest(
		t,
		&tierResult{Total: 3, Passed: 3},
		&tierResult{Total: 2, Passed: 1, Failed: 1, Failures: []failureEntry{{Package: "p", Test: "TestB"}}, RawOut: "tail"},
	)
	st, detail, res := runUnitTier(tierPlan{name: tierUnit, pkgs: []string{"./pkg/a"}, funcs: map[string][]string{"./pkg/b": {"TestB"}}})
	if st != "fail" || !strings.Contains(detail, "p::TestB") || res.Total != 5 || res.Failed != 1 || res.Passed != 4 {
		t.Errorf("runUnitTier = %q, %q, %+v", st, detail, res)
	}
	if len(*calls) != 2 {
		t.Fatalf("invocations = %v", *calls)
	}
	first, second := strings.Join((*calls)[0], " "), strings.Join((*calls)[1], " ")
	if !strings.Contains(first, "-coverprofile=") || !strings.HasSuffix(first, "./pkg/a") || !strings.Contains(first, "-race") {
		t.Errorf("whole-package invocation = %s", first)
	}
	if strings.Contains(second, "-coverprofile=") || !strings.Contains(second, "-run ^(TestB)$ ./pkg/b") {
		t.Errorf("by-name invocation = %s", second)
	}
}

// TestUnitTierWithAnEmptyScopeRunsNothing pins the guard against `go test`
// with no package argument, which would test the working directory rather
// than nothing, and that an unscoped plan runs the module.
func TestUnitTierWithAnEmptyScopeRunsNothing(t *testing.T) {
	calls := recordGoTest(t)
	st, detail, res := runUnitTier(tierPlan{name: tierUnit, pkgs: []string{}})
	if st != "pass" || res == nil || !strings.Contains(detail, "no packages") || len(*calls) != 0 {
		t.Errorf("runUnitTier(empty) = %q, %q, %v, calls %v", st, detail, res, *calls)
	}
	if st, _, _ := runUnitTier(tierPlan{name: tierUnit}); st != "pass" || len(*calls) != 1 || (*calls)[0][len((*calls)[0])-1] != "./..." {
		t.Errorf("an unscoped unit tier must run ./...; status %q, calls %v", st, *calls)
	}
}

// TestUnitTierArgsRunsTheScopeAndNeverTheModuleWhenScoped pins the argv
// the unit tier hands `go test`: a scoped plan names its packages, and only
// an unscoped plan names ./... .
func TestUnitTierArgsRunsTheScopeAndNeverTheModuleWhenScoped(t *testing.T) {
	scoped := unitTierArgs([]string{"./pkg/a", "./pkg/b/..."}, "")
	if !contains(scoped, "-race") || contains(scoped, "./...") {
		t.Errorf("scoped argv = %v", scoped)
	}
	if full := unitTierArgs(nil, ""); full[len(full)-1] != "./..." {
		t.Errorf("unscoped argv = %v", full)
	}
}

// TestTaggedTiersRunTheirScope pins the component, contract, and
// integration executors over a scoped plan: each runs its tag and the
// selected packages or functions, and an empty scope runs nothing.
func TestTaggedTiersRunTheirScope(t *testing.T) {
	calls := recordGoTest(t)
	scope := tierPlan{pkgs: []string{"./tests/contract/a"}, funcs: map[string][]string{"./tests/contract/b": {"TestB"}}}
	if st, detail, _ := runContractTier(scope); st != "pass" {
		t.Fatalf("contract = %q %q", st, detail)
	}
	if got := strings.Join((*calls)[0], " "); got != "-count=1 -tags=contract ./tests/contract/a" {
		t.Errorf("contract whole invocation = %q", got)
	}
	if got := strings.Join((*calls)[1], " "); got != "-count=1 -tags=contract -run ^(TestB)$ ./tests/contract/b" {
		t.Errorf("contract by-name invocation = %q", got)
	}
	*calls = nil
	integ := tierPlan{pkgs: []string{}, funcs: map[string][]string{"./tests/integration": {"TestI"}}}
	if st, detail, _ := runIntegrationTier(integ); st != "pass" {
		t.Fatalf("integration = %q %q", st, detail)
	}
	if got := strings.Join((*calls)[0], " "); !strings.Contains(got, "-tags=integration -run ^(TestI)$ ./tests/integration") {
		t.Errorf("integration by-name invocation = %q", got)
	}
	*calls = nil
	if st, detail, _ := runComponentTier(tierPlan{pkgs: []string{}}); st != "pass" || !strings.Contains(detail, "no packages") || len(*calls) != 0 {
		t.Errorf("an empty component scope = %q %q, calls %v", st, detail, *calls)
	}
	if st, _, _ := runContractTier(tierPlan{pkgs: []string{}}); st != "pass" || len(*calls) != 0 {
		t.Errorf("an empty contract scope ran %v", *calls)
	}
}

// TestTierOutcomeSeparatesBuildFailuresFromTestFailures pins the status a
// merged result maps to.
func TestTierOutcomeSeparatesBuildFailuresFromTestFailures(t *testing.T) {
	if st, detail, _ := tierOutcome("unit", &tierResult{RawOut: "build failed"}, errors.New("exit status 2")); st != "fail" || !strings.Contains(detail, "unit suite failed") || !strings.Contains(detail, "build failed") {
		t.Errorf("build failure = %q %q", st, detail)
	}
	if st, _, _ := tierOutcome("unit", &tierResult{Total: 1, Passed: 1}, nil); st != "pass" {
		t.Errorf("a passing result = %q", st)
	}
	merged := &tierResult{}
	mergeTierResult(merged, nil)
	mergeTierResult(merged, &tierResult{Total: 1, Skipped: 1, RawOut: "a"})
	mergeTierResult(merged, &tierResult{Total: 1, Passed: 1, RawOut: "b"})
	if merged.Total != 2 || merged.Skipped != 1 || merged.Passed != 1 || merged.RawOut != "a\nb" {
		t.Errorf("merged = %+v", merged)
	}
}
