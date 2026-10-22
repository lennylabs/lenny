// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// fixtureGraph is a small module graph shaped like the real one:
//
//	pkg/leaf        <- pkg/mid (import), pkg/side (test import),
//	                   tests/tier0_static (import)
//	pkg/mid         <- pkg/top
//	pkg/top         <- cmd/app
//	pkg/orphan         (no importers)
//	pkg/fix            (owns pkg/fix/testdata/...)
func fixtureGraph() *pkgGraph {
	g := newPkgGraph()
	m := moduleImportPath + "/"
	g.addPackage(m+"pkg/leaf", "pkg/leaf", []string{"fmt"})
	g.addPackage(m+"pkg/mid", "pkg/mid", []string{m + "pkg/leaf", "strings"})
	g.addPackage(m+"pkg/side", "pkg/side", []string{"testing", m + "pkg/leaf"})
	g.addPackage(m+"pkg/top", "pkg/top", []string{m + "pkg/mid"})
	g.addPackage(m+"cmd/app", "cmd/app", []string{m + "pkg/top", m + "pkg/top"})
	g.addPackage(m+"tests/tier0_static", "tests/tier0_static", []string{m + "pkg/leaf"})
	g.addPackage(m+"pkg/orphan", "pkg/orphan", nil)
	g.addPackage(m+"pkg/fix", "pkg/fix", []string{"", "os"})
	return g
}

// fixtureGlobs mirrors the change graph's layout: per-key tier maps whose
// unit entries are recursive package patterns.
func fixtureGlobs() map[string]map[string][]string {
	return map[string]map[string][]string{
		"pkg/leaf": {
			"static": {"tests/tier0_static/leaf_test.go"},
			"unit":   {"pkg/leaf/...", "pkg/consumer/..."},
			"e2e":    {"tests/tier5_e2e_kind/leaf_test.go"},
		},
		"tests/tier0_static": {"static": {"tests/tier0_static/..."}},
		"docs/":              {"docs": {"tests/tier11_docs/..."}},
		"pkg/mid/...":        {"unit": {"pkg/mid/..."}, "contract": {"tests/tier3_contract/mid/..."}},
	}
}

// TestSelectChangedScopesTheUnitTierToTheDiff drives the --changed selector
// over representative diffs and pins, for each, the tiers it reaches, the
// unit tier's package scope, and what it reports. The selector replaced a
// unit tier that always ran `go test ./...`, so every case also asserts the
// scope is never the whole module unless --all asked for it.
//
// spec: the test infrastructure §6 (selection algorithm: `--changed` walks
// `git diff --name-only` through the change graph), §5 (change graph);
// .claude/rules/test-coverage.md (How to run: `--changed --max-tier`
// runs every tier up to the bound for the packages the diff touches)
func TestSelectChangedScopesTheUnitTierToTheDiff(t *testing.T) {
	cases := []struct {
		name         string
		paths        []string
		opts         selectOpts
		wantTiers    []string
		wantPkgs     []string // nil: unit tier unscoped or absent
		wantUnmapped []string
		wantGlobal   []string
	}{
		{
			name:      "leaf package change runs graph targets, the package, and direct importers",
			paths:     []string{"pkg/leaf/leaf.go"},
			opts:      selectOpts{rdepsDepth: 1},
			wantTiers: []string{"e2e", "static", "unit"},
			// tests/tier0_static imports pkg/leaf but runs in the static tier.
			wantPkgs: []string{"./pkg/consumer/...", "./pkg/leaf/...", "./pkg/mid", "./pkg/side"},
		},
		{
			name:      "depth zero runs the touched package and graph targets only",
			paths:     []string{"pkg/leaf/leaf.go"},
			opts:      selectOpts{rdepsDepth: 0},
			wantTiers: []string{"e2e", "static", "unit"},
			wantPkgs:  []string{"./pkg/consumer/...", "./pkg/leaf/..."},
		},
		{
			name:      "negative depth walks the full transitive closure",
			paths:     []string{"pkg/leaf/leaf.go"},
			opts:      selectOpts{rdepsDepth: -1},
			wantTiers: []string{"e2e", "static", "unit"},
			wantPkgs:  []string{"./cmd/app", "./pkg/consumer/...", "./pkg/leaf/...", "./pkg/mid", "./pkg/side", "./pkg/top"},
		},
		{
			name:         "documentation-only diff runs tier 0 and reports the unmapped path",
			paths:        []string{"BUILD-GAPS.md"},
			opts:         selectOpts{rdepsDepth: 1},
			wantTiers:    []string{"static"},
			wantUnmapped: []string{"BUILD-GAPS.md"},
		},
		{
			name:      "mapped non-Go path reaches its tiers without a unit scope",
			paths:     []string{"docs/guide.md"},
			opts:      selectOpts{rdepsDepth: 1},
			wantTiers: []string{"docs", "static"},
		},
		{
			name:         "unmapped Go file selects its own package and importers",
			paths:        []string{"pkg/top/top.go"},
			opts:         selectOpts{rdepsDepth: 1},
			wantTiers:    []string{"static", "unit"},
			wantPkgs:     []string{"./cmd/app", "./pkg/top"},
			wantUnmapped: []string{"pkg/top/top.go"},
		},
		{
			name:         "unmapped fixture selects the nearest owning package",
			paths:        []string{"pkg/fix/testdata/golden/out.json"},
			opts:         selectOpts{rdepsDepth: 1},
			wantTiers:    []string{"static", "unit"},
			wantPkgs:     []string{"./pkg/fix"},
			wantUnmapped: []string{"pkg/fix/testdata/golden/out.json"},
		},
		{
			name:      "a key spelled with /... selects like a bare key",
			paths:     []string{"pkg/mid/mid.go"},
			opts:      selectOpts{rdepsDepth: 1},
			wantTiers: []string{"contract", "static", "unit"},
			wantPkgs:  []string{"./pkg/mid/...", "./pkg/top"},
		},
		{
			name:      "tier-0 test change stays in the static tier",
			paths:     []string{"tests/tier0_static/leaf_test.go"},
			opts:      selectOpts{rdepsDepth: 1},
			wantTiers: []string{"static"},
		},
		{
			name:       "go.mod alone is reported as global and does not widen the unit tier",
			paths:      []string{"go.mod", "go.sum"},
			opts:       selectOpts{rdepsDepth: 1},
			wantTiers:  []string{"static"},
			wantGlobal: []string{"go.mod", "go.sum"},
		},
		{
			name:         "go.mod with a package change keeps the package scope",
			paths:        []string{"go.mod", "pkg/orphan/o.go"},
			opts:         selectOpts{rdepsDepth: 1},
			wantTiers:    []string{"static", "unit"},
			wantPkgs:     []string{"./pkg/orphan"},
			wantGlobal:   []string{"go.mod"},
			wantUnmapped: []string{"pkg/orphan/o.go"},
		},
		{
			name:       "--all runs every package",
			paths:      []string{"go.mod"},
			opts:       selectOpts{rdepsDepth: 1, all: true},
			wantTiers:  []string{"static", "unit"},
			wantGlobal: []string{"go.mod"},
		},
		{
			name:         "a root-level file is never owned by a module-root package",
			paths:        []string{"Makefile"},
			opts:         selectOpts{rdepsDepth: 1},
			wantTiers:    []string{"static"},
			wantUnmapped: []string{"Makefile"},
		},
		{
			name:      "empty diff runs tier 0",
			opts:      selectOpts{rdepsDepth: 1},
			wantTiers: []string{"static"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel := selectChanged(tc.paths, fixtureGlobs(), fixtureGraph(), tc.opts)
			if got := sortedKeys(sel.tiers); !reflect.DeepEqual(got, tc.wantTiers) {
				t.Errorf("tiers = %v, want %v", got, tc.wantTiers)
			}
			if !reflect.DeepEqual(sel.unitPkgs, tc.wantPkgs) {
				t.Errorf("unit scope = %#v, want %#v", sel.unitPkgs, tc.wantPkgs)
			}
			if !reflect.DeepEqual(sel.unmapped, tc.wantUnmapped) {
				t.Errorf("unmapped = %v, want %v", sel.unmapped, tc.wantUnmapped)
			}
			if !reflect.DeepEqual(sel.global, tc.wantGlobal) {
				t.Errorf("global = %v, want %v", sel.global, tc.wantGlobal)
			}
			// The regression this selector fixes: a unit tier reached by
			// a diff is scoped unless --all asked for every package.
			if sel.tiers[tierUnit] && sel.unitPkgs == nil && !tc.opts.all {
				t.Errorf("unit tier selected with no scope; it would run ./...")
			}
			for _, p := range sel.unitPkgs {
				if p == "./..." || strings.HasPrefix(p, "./tests/tier0_static") {
					t.Errorf("unit scope carries %q", p)
				}
			}
		})
	}
}

// TestSelectChangedWithoutPackageGraphKeepsGraphTargets pins the degraded
// path: when the package graph cannot load, the change graph's own unit
// targets still scope a mapped change, and an unmapped Go file is reported.
// resolveChangedPlanFor widens such a plan to every package; this test
// covers the pure half.
//
// spec: the test infrastructure §6 (selection algorithm), §5 (change graph)
func TestSelectChangedWithoutPackageGraphKeepsGraphTargets(t *testing.T) {
	sel := selectChanged([]string{"pkg/leaf/leaf.go", "pkg/top/top.go"}, fixtureGlobs(), nil, selectOpts{rdepsDepth: 1})
	want := []string{"./pkg/consumer/...", "./pkg/leaf/..."}
	if !reflect.DeepEqual(sel.unitPkgs, want) {
		t.Errorf("unit scope = %v, want %v", sel.unitPkgs, want)
	}
	if !reflect.DeepEqual(sel.unmapped, []string{"pkg/top/top.go"}) {
		t.Errorf("unmapped = %v", sel.unmapped)
	}
}

// TestChangeSelectionNotesNameWhatTheSelectionCouldNotPlace pins the
// operator-facing report: an unmapped path and a module-wide build file are
// both named, and --all silences the build-file advice it already acts on.
//
// spec: the test infrastructure §6 (selection algorithm)
func TestChangeSelectionNotesNameWhatTheSelectionCouldNotPlace(t *testing.T) {
	sel := changeSelection{unmapped: []string{"BUILD-GAPS.md"}, global: []string{"go.mod"}}
	notes := strings.Join(sel.notes(selectOpts{}), "\n")
	for _, want := range []string{"BUILD-GAPS.md", "go.mod", "--all"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes %q do not name %q", notes, want)
		}
	}
	if got := sel.notes(selectOpts{all: true}); len(got) != 1 {
		t.Errorf("with --all, notes = %v; want the unmapped note alone", got)
	}
	if got := (changeSelection{}).notes(selectOpts{}); got != nil {
		t.Errorf("a clean selection raised notes: %v", got)
	}
}

// TestReverseDepsBoundsAndCycles pins the importer walk: depth bounds the
// hops, roots are excluded, and an import cycle through test imports does
// not loop.
//
// spec: the test infrastructure §6 (selection algorithm)
func TestReverseDepsBoundsAndCycles(t *testing.T) {
	g := newPkgGraph()
	g.addPackage("github.com/lennylabs/lenny/a", "a", []string{"github.com/lennylabs/lenny/b"})
	g.addPackage("github.com/lennylabs/lenny/b", "b", []string{"github.com/lennylabs/lenny/a"})
	g.addPackage("github.com/lennylabs/lenny/c", "c", []string{"github.com/lennylabs/lenny/b", "example.com/ext"})
	if got := g.reverseDeps([]string{"github.com/lennylabs/lenny/a"}, -1); !reflect.DeepEqual(got, []string{"github.com/lennylabs/lenny/b", "github.com/lennylabs/lenny/c"}) {
		t.Errorf("transitive = %v", got)
	}
	if got := g.reverseDeps([]string{"github.com/lennylabs/lenny/a"}, 1); !reflect.DeepEqual(got, []string{"github.com/lennylabs/lenny/b"}) {
		t.Errorf("depth 1 = %v", got)
	}
	if got := g.reverseDeps(nil, -1); len(got) != 0 {
		t.Errorf("no roots = %v", got)
	}
	if _, ok := g.importers["example.com/ext"]; ok {
		t.Errorf("an out-of-module import was recorded")
	}
}

// TestCollapsePatternsDropsCoveredPackages pins the scope's printed form.
//
// spec: the test infrastructure §6 (selection algorithm: within a tier,
// group by package)
func TestCollapsePatternsDropsCoveredPackages(t *testing.T) {
	got := collapsePatterns(map[string]bool{
		"./pkg/a/...": true, "./pkg/a": true, "./pkg/a/b": true, "./pkg/a/b/...": true,
		"./pkg/ab": true, "./pkg/c": true,
	})
	want := []string{"./pkg/a/...", "./pkg/ab", "./pkg/c"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collapse = %v, want %v", got, want)
	}
	if got := collapsePatterns(nil); got == nil || len(got) != 0 {
		t.Errorf("an empty set must collapse to an empty, non-nil scope; got %#v", got)
	}
}

// TestGoTestPatternAndDiffRange pins the two small conversions the selector
// relies on.
//
// spec: the test infrastructure §6 (selection algorithm)
func TestGoTestPatternAndDiffRange(t *testing.T) {
	for in, want := range map[string]string{
		"pkg/a/...":             "./pkg/a/...",
		"./pkg/a/...":           "./pkg/a/...",
		"pkg/a/x_test.go":       "./pkg/a",
		"tests/tier1_unit/life": "./tests/tier1_unit/life",
	} {
		if got := goTestPattern(in); got != want {
			t.Errorf("goTestPattern(%q) = %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{
		"main":       "main...HEAD",
		"HEAD~5":     "HEAD~5...HEAD",
		"a..b":       "a..b",
		"base...tip": "base...tip",
	} {
		if got := diffRange(in); got != want {
			t.Errorf("diffRange(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestUnitTierArgsRunsTheScopeAndNeverTheModuleWhenScoped pins the argv
// the unit tier hands `go test`: a scoped plan names its packages, and only
// an unscoped plan names ./... .
//
// spec: the test infrastructure §6 (selection algorithm); §17.4 race
// detection on by default
func TestUnitTierArgsRunsTheScopeAndNeverTheModuleWhenScoped(t *testing.T) {
	scoped := unitTierArgs([]string{"./pkg/a", "./pkg/b/..."}, "")
	if !contains(scoped, "-race") || contains(scoped, "./...") {
		t.Errorf("scoped argv = %v", scoped)
	}
	if tail := scoped[len(scoped)-2:]; !reflect.DeepEqual(tail, []string{"./pkg/a", "./pkg/b/..."}) {
		t.Errorf("scoped argv tail = %v", tail)
	}
	full := unitTierArgs(nil, "")
	if full[len(full)-1] != "./..." {
		t.Errorf("unscoped argv = %v", full)
	}
	profile := t.TempDir() + "/cover.out"
	withCover := unitTierArgs([]string{"./pkg/a"}, profile)
	if !contains(withCover, "-coverprofile="+profile) {
		t.Errorf("coverage argv = %v", withCover)
	}
}

// TestRunUnitTierWithAnEmptyScopeRunsNothing pins the guard against
// `go test` with no package argument, which would test the working
// directory rather than nothing.
//
// spec: the test infrastructure §6 (selection algorithm)
func TestRunUnitTierWithAnEmptyScopeRunsNothing(t *testing.T) {
	st, detail, res := runUnitTier([]string{})
	if st != "pass" || res == nil || !strings.Contains(detail, "no packages") {
		t.Errorf("runUnitTier([]) = %q, %q, %v", st, detail, res)
	}
}

// TestMergeDiscoveryPlansNeverNarrows pins the union of --changed, --spec,
// and --pkg: scopes union, and an unscoped contributor or --all leaves the
// unit tier unscoped.
//
// spec: the test infrastructure §6 (selection algorithm: selectors compose)
func TestMergeDiscoveryPlansNeverNarrows(t *testing.T) {
	changed := []tierPlan{{name: tierStatic}, {name: tierUnit, pkgs: []string{"./pkg/a"}}}
	pkg := []tierPlan{{name: tierUnit, pkgs: []string{"./pkg/b/..."}}, {name: tierContract}}
	spec := []tierPlan{{name: tierUnit}}

	merged := mergeDiscoveryPlans([][]tierPlan{changed, pkg}, false)
	if names := planNames(merged); !reflect.DeepEqual(names, []string{"static", "unit", "contract"}) {
		t.Errorf("tiers = %v", names)
	}
	if got := merged[1].pkgs; !reflect.DeepEqual(got, []string{"./pkg/a", "./pkg/b/..."}) {
		t.Errorf("merged scope = %v", got)
	}
	if got := mergeDiscoveryPlans([][]tierPlan{changed, spec}, false)[1].pkgs; got != nil {
		t.Errorf("an unscoped contributor must leave the unit tier unscoped; got %v", got)
	}
	if got := mergeDiscoveryPlans([][]tierPlan{changed}, true)[1].pkgs; got != nil {
		t.Errorf("--all must leave the unit tier unscoped; got %v", got)
	}
}

// TestPkgsPlanScopesTheUnitTierToTheNamedPackage pins --pkg: the named
// package and the graph's unit targets for it, rather than ./... .
//
// spec: the test infrastructure §6 (selection algorithm: `--pkg` resolves
// through the change graph)
func TestPkgsPlanScopesTheUnitTierToTheNamedPackage(t *testing.T) {
	plan := pkgsPlanIn(fixtureGlobs(), []string{"./pkg/leaf/..."})
	var unit *tierPlan
	for i := range plan {
		if plan[i].name == tierUnit {
			unit = &plan[i]
		}
	}
	if unit == nil {
		t.Fatalf("plan %v has no unit tier", planNames(plan))
	}
	if want := []string{"./pkg/consumer/...", "./pkg/leaf/..."}; !reflect.DeepEqual(unit.pkgs, want) {
		t.Errorf("scope = %v, want %v", unit.pkgs, want)
	}
	if plan := pkgsPlanIn(fixtureGlobs(), []string{"pkg/unknown"}); plan != nil {
		t.Errorf("an unknown package resolved to %v", planNames(plan))
	}
}

// TestDryRunListsTheUnitScopeAndNotes pins the dry-run report, which is how
// an operator sees what --changed will run before running it.
//
// spec: the test infrastructure §6 (command surface: `--dry-run --changed`
// shows what would run)
func TestDryRunListsTheUnitScopeAndNotes(t *testing.T) {
	var buf bytes.Buffer
	writeDryRun(&buf, selector{changed: true, rdepsDepth: 1}, resolvedSelector{
		tiers: []tierPlan{{name: tierStatic}, {name: tierUnit, pkgs: []string{"./pkg/a"}}},
		notes: []string{"1 changed path(s) have no tests/change-graph.json key: X.md"},
	})
	out := buf.String()
	for _, want := range []string{"rdeps-depth=1", "- unit", "pkg:    ./pkg/a", "note: 1 changed path(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	buf.Reset()
	writeDryRun(&buf, selector{tier: tierUnit}, resolvedSelector{tiers: []tierPlan{{name: tierUnit}}})
	if !strings.Contains(buf.String(), "./... (every package)") {
		t.Errorf("unscoped dry run does not say it runs every package:\n%s", buf.String())
	}
}

// TestLoadPkgGraphReadsTheModule loads the real module graph through
// `go list` and checks one edge that is stable by construction: this
// command imports its own changegraph package.
//
// spec: the test infrastructure §6 (selection algorithm)
func TestLoadPkgGraphReadsTheModule(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the module graph with go list")
	}
	g, err := loadPkgGraph(context.Background(), repoRoot())
	if err != nil {
		t.Fatalf("loadPkgGraph: %v", err)
	}
	self := moduleImportPath + "/cmd/lenny-test"
	if dir := g.pkgToDir[self]; dir != "cmd/lenny-test" {
		t.Fatalf("cmd/lenny-test dir = %q", dir)
	}
	if got := g.packageForPath("cmd/lenny-test/testdata/x.json"); got != self {
		t.Errorf("packageForPath(testdata) = %q", got)
	}
	if !contains(g.importers[self+"/changegraph"], self) {
		t.Errorf("importers of changegraph = %v; want %s", g.importers[self+"/changegraph"], self)
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func planNames(plan []tierPlan) []string {
	out := make([]string, 0, len(plan))
	for _, p := range plan {
		out = append(out, p.name)
	}
	return out
}
