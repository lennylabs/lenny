// SPDX-License-Identifier: MIT

package main

import (
	"reflect"
	"strings"
	"testing"
)

// stubChangedPlan replaces the --changed and --since resolution with a
// fixed plan for the test's lifetime and counts the calls, recording
// the ref and options each call received.
type stubChangedPlan struct {
	calls int
	ref   string
	opts  selectOpts
}

func useChangedPlan(t *testing.T, plan []tierPlan, notes []string) *stubChangedPlan {
	t.Helper()
	stub := &stubChangedPlan{}
	prev := resolveChangedPlanFn
	resolveChangedPlanFn = func(ref string, opts selectOpts) ([]tierPlan, []string) {
		stub.calls++
		stub.ref = ref
		stub.opts = opts
		// Hand out a copy so a resolve that edits the plan cannot
		// leak into the next case.
		out := make([]tierPlan, len(plan))
		for i, p := range plan {
			p.pkgs = append([]string(nil), p.pkgs...)
			if plan[i].pkgs == nil {
				p.pkgs = nil
			}
			out[i] = p
		}
		return out, notes
	}
	t.Cleanup(func() { resolveChangedPlanFn = prev })
	return stub
}

// changedFixturePlan is what a diff touching two Go packages and a
// documentation page resolves to: the static tier, a scoped unit tier,
// and the docs tier.
func changedFixturePlan() []tierPlan {
	return []tierPlan{
		{name: tierStatic},
		{name: tierUnit, pkgs: []string{"./pkg/a", "./pkg/b/..."}},
		{name: tierDocs},
	}
}

func mustResolve(t *testing.T, s selector) resolvedSelector {
	t.Helper()
	r, err := s.resolve()
	if err != nil {
		t.Fatalf("resolve(%s): %v", describe(s), err)
	}
	return r
}

// TestTierWithSinceKeepsTheChangedUnitScope pins T-17.4.20: --tier unit
// --since <ref> runs the packages the diff reaches rather than ./... .
// The selector sets changed whenever --since is given, as runRun does.
//
// spec: the test infrastructure §6 (selection algorithm: the unit tier of
// `--changed` and `--since` is scoped to the packages the diff reaches, and
// selectors compose by intersection)
func TestTierWithSinceKeepsTheChangedUnitScope(t *testing.T) {
	stub := useChangedPlan(t, changedFixturePlan(), []string{"1 changed path(s) have no tests/change-graph.json key: X.md"})

	r := mustResolve(t, selector{tier: tierUnit, changed: true, since: "HEAD~3", rdepsDepth: 1})
	if stub.calls != 1 {
		t.Fatalf("the change selection ran %d time(s), want 1", stub.calls)
	}
	if stub.ref != "HEAD~3" || stub.opts.rdepsDepth != 1 || stub.opts.all {
		t.Errorf("change selection got ref=%q opts=%+v", stub.ref, stub.opts)
	}
	if names := planNames(r.tiers); !reflect.DeepEqual(names, []string{tierUnit}) {
		t.Fatalf("tiers = %v, want only unit", names)
	}
	if got, want := r.tiers[0].pkgs, []string{"./pkg/a", "./pkg/b/..."}; !reflect.DeepEqual(got, want) {
		t.Errorf("unit scope = %v, want %v (nil means ./...)", got, want)
	}
	if len(r.notes) != 1 || !strings.Contains(r.notes[0], "X.md") {
		t.Errorf("the change selection's notes were dropped: %v", r.notes)
	}

	// --changed without --since resolves the working tree the same way.
	stub.calls = 0
	r = mustResolve(t, selector{tier: tierUnit, changed: true})
	if stub.calls != 1 || stub.ref != "" {
		t.Errorf("--tier unit --changed: calls=%d ref=%q", stub.calls, stub.ref)
	}
	if r.tiers[0].pkgs == nil {
		t.Errorf("--tier unit --changed runs every package")
	}
}

// TestTierAloneStillNamesTheWholeTier pins that --tier without a discovery
// selector is unchanged: the unit tier runs every package and no diff is
// read.
//
// spec: the test infrastructure §6 (command surface: `--tier` runs one tier)
func TestTierAloneStillNamesTheWholeTier(t *testing.T) {
	stub := useChangedPlan(t, changedFixturePlan(), nil)

	r := mustResolve(t, selector{tier: tierUnit})
	if stub.calls != 0 {
		t.Errorf("--tier unit alone read the diff")
	}
	if len(r.tiers) != 1 || r.tiers[0].name != tierUnit || r.tiers[0].pkgs != nil {
		t.Errorf("--tier unit alone = %+v, want one unscoped unit tier", r.tiers)
	}

	r = mustResolve(t, selector{tier: tierComponent, subsets: []string{"stores"}})
	if len(r.tiers) != 1 || !reflect.DeepEqual(r.tiers[0].subsets, []string{"stores"}) {
		t.Errorf("--tier component --subset stores = %+v", r.tiers)
	}
	if len(r.notes) != 0 {
		t.Errorf("--tier alone raised notes: %v", r.notes)
	}
}

// TestTierWithSinceForOtherTiers pins the non-unit tiers: a tier the diff
// selects keeps the change plan's entry for it, and a tier the diff does
// not select runs nothing and says so.
//
// spec: the test infrastructure §6 (selection algorithm: selectors compose
// by intersection)
func TestTierWithSinceForOtherTiers(t *testing.T) {
	cases := []struct {
		name     string
		plan     []tierPlan
		tier     string
		subsets  []string
		want     []tierPlan
		wantNote string
	}{
		{
			name: "static is always selected",
			plan: changedFixturePlan(),
			tier: tierStatic,
			want: []tierPlan{{name: tierStatic}},
		},
		{
			name: "docs selected by a documentation change",
			plan: changedFixturePlan(),
			tier: tierDocs,
			want: []tierPlan{{name: tierDocs}},
		},
		{
			name:     "docs not selected by a Go-only change",
			plan:     []tierPlan{{name: tierStatic}, {name: tierUnit, pkgs: []string{"./pkg/a"}}},
			tier:     tierDocs,
			want:     []tierPlan{},
			wantNote: "does not reach the docs tier",
		},
		{
			name:     "unit not selected by a documentation-only change",
			plan:     []tierPlan{{name: tierStatic}, {name: tierDocs}},
			tier:     tierUnit,
			want:     []tierPlan{},
			wantNote: "does not reach the unit tier",
		},
		{
			name:     "an empty diff selects nothing",
			plan:     nil,
			tier:     tierStatic,
			want:     []tierPlan{},
			wantNote: "does not reach the static tier",
		},
		{
			name:    "subsets apply to the selected tier",
			plan:    []tierPlan{{name: tierStatic}, {name: tierComponent}},
			tier:    tierComponent,
			subsets: []string{"stores"},
			want:    []tierPlan{{name: tierComponent, subsets: []string{"stores"}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useChangedPlan(t, tc.plan, nil)
			r := mustResolve(t, selector{tier: tc.tier, subsets: tc.subsets, changed: true, since: "main"})
			if !reflect.DeepEqual(r.tiers, tc.want) {
				t.Errorf("tiers = %+v, want %+v", r.tiers, tc.want)
			}
			if tc.wantNote == "" {
				if len(r.notes) != 0 {
					t.Errorf("unexpected notes: %v", r.notes)
				}
				return
			}
			if len(r.notes) != 1 || !strings.Contains(r.notes[0], tc.wantNote) {
				t.Errorf("notes = %v, want one containing %q", r.notes, tc.wantNote)
			}
		})
	}
}

// TestTierUnitWithSinceAndAllRunsEveryPackage pins --all: it widens the
// unit tier of the change selection to ./... , also under --tier unit.
//
// spec: the test infrastructure §6 (selection algorithm: `--all` runs the
// unit tier over every package)
func TestTierUnitWithSinceAndAllRunsEveryPackage(t *testing.T) {
	stub := useChangedPlan(t, changedFixturePlan(), nil)

	r := mustResolve(t, selector{tier: tierUnit, changed: true, since: "HEAD~3", all: true})
	if !stub.opts.all {
		t.Errorf("--all was not passed to the change selection")
	}
	if len(r.tiers) != 1 || r.tiers[0].name != tierUnit || r.tiers[0].pkgs != nil {
		t.Errorf("--tier unit --since --all = %+v, want one unscoped unit tier", r.tiers)
	}
}

// TestTierComposesWithPkgAndMaxTier pins the remaining compositions: --pkg
// under --tier unit keeps its package scope, and --max-tier still bounds a
// --tier --since plan.
//
// spec: the test infrastructure §6 (selection algorithm: selectors compose
// by intersection)
func TestTierComposesWithPkgAndMaxTier(t *testing.T) {
	useChangedPlan(t, changedFixturePlan(), nil)

	r := mustResolve(t, selector{tier: tierUnit, pkgs: []string{"./cmd/lenny-test/..."}})
	if len(r.tiers) != 1 || r.tiers[0].name != tierUnit {
		t.Fatalf("--tier unit --pkg = %v", planNames(r.tiers))
	}
	if r.tiers[0].pkgs == nil || !containsString(r.tiers[0].pkgs, "./cmd/lenny-test/...") {
		t.Errorf("--tier unit --pkg scope = %v, want it to name ./cmd/lenny-test/...", r.tiers[0].pkgs)
	}

	r = mustResolve(t, selector{tier: tierDocs, maxTier: tierUnit, changed: true})
	if len(r.tiers) != 0 {
		t.Errorf("--max-tier unit kept %v", planNames(r.tiers))
	}
}

// TestGroupStaysExclusiveOfTheChangeSelection pins that --group names a
// complete plan: a --since beside it reads no diff.
//
// spec: the test infrastructure §6 (command surface: `--group` runs a named
// group)
func TestGroupStaysExclusiveOfTheChangeSelection(t *testing.T) {
	stub := useChangedPlan(t, changedFixturePlan(), nil)
	r, err := selector{group: "pr-fast", tier: tierUnit, changed: true, since: "main"}.resolve()
	if err != nil {
		t.Fatalf("resolve --group pr-fast: %v", err)
	}
	if stub.calls != 0 {
		t.Errorf("--group read the diff")
	}
	if len(r.tiers) == 0 {
		t.Errorf("--group pr-fast resolved no tiers")
	}
}
