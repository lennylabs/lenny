// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// The selector is contributor tooling: it chooses which tests a diff runs
// (change 0116, CS-4 to CS-6), and no requirement of the specification
// states it, so these cases carry no specification annotation.

// fixtureGraph is a small module graph laid out like the real one:
//
//	pkg/leaf        <- pkg/mid (import), pkg/side (test import),
//	                   tests/static (import), tests/component/leafc
//	                   (test import)
//	pkg/mid         <- pkg/top, tests/contract/midc (test import)
//	pkg/top         <- cmd/app, tests/integration (test import)
//	pkg/side        <- pkg/sidedep (import), reached from pkg/leaf only
//	                   through a test import, so pkg/sidedep is not
//	                   selected by a change to pkg/leaf
//	cmd/lenny-ops   <- tests/component tagged tests in-package
//	pkg/assets      embeds pkg/assets/sub/x.tmpl, where pkg/assets/sub
//	                   is itself a package
//	pkg/helper      <- tests/chaos (test import)
//	pkg/orphan         (no importers)
func fixtureGraph() *pkgGraph {
	g := newPkgGraph()
	m := moduleImportPath + "/"
	g.addPackage(m+"pkg/leaf", "pkg/leaf", []string{"fmt"}, nil, nil)
	g.addPackage(m+"pkg/mid", "pkg/mid", []string{m + "pkg/leaf", "strings"}, nil, nil)
	g.addPackage(m+"pkg/side", "pkg/side", nil, []string{"testing", m + "pkg/leaf", m + "pkg/side"}, nil)
	g.addPackage(m+"pkg/sidedep", "pkg/sidedep", []string{m + "pkg/side"}, nil, nil)
	g.addPackage(m+"pkg/top", "pkg/top", []string{m + "pkg/mid"}, []string{m + "pkg/mid"}, nil)
	g.addPackage(m+"cmd/app", "cmd/app", []string{m + "pkg/top", m + "pkg/top"}, nil, nil)
	g.addPackage(m+"cmd/lenny-ops", "cmd/lenny-ops", []string{m + "pkg/orphan"}, nil, nil)
	g.addPackage(m+"tests/static", "tests/static", []string{m + "pkg/leaf"}, nil, nil)
	g.addPackage(m+"tests/component/leafc", "tests/component/leafc", nil, []string{m + "pkg/leaf"}, nil)
	g.addPackage(m+"tests/contract/midc", "tests/contract/midc", nil, []string{m + "pkg/mid"}, nil)
	g.addPackage(m+"tests/integration", "tests/integration", nil, []string{m + "pkg/top"}, nil)
	g.addPackage(m+"tests/chaos", "tests/chaos", nil, []string{m + "pkg/helper"}, nil)
	g.addPackage(m+"pkg/helper", "pkg/helper", nil, nil, nil)
	g.addPackage(m+"pkg/orphan", "pkg/orphan", nil, nil, nil)
	g.addPackage(m+"pkg/assets", "pkg/assets", nil, nil, []string{"pkg/assets/sub/x.tmpl"})
	g.addPackage(m+"pkg/assets/sub", "pkg/assets/sub", nil, nil, nil)
	return g
}

// fixtureInputs is a non-Go input list with a tier row, a package row,
// and a helper row whose package's importers read the input.
func fixtureInputs() *nonGoInputs {
	return &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: []nonGoInput{
		{Paths: []string{"charts/lenny/**"}, Runs: []string{tierE2EKind, "tests/component/leafc"}, Reason: "deploys it"},
		{Paths: []string{"compose/**"}, Runs: []string{"pkg/helper"}, Reason: "the helper runs it"},
		{Paths: []string{"docs/**"}, Runs: []string{tierDocs}, Reason: "checks it"},
	}}
}

// planOf resolves a selection and returns its plan by tier name.
func planOf(sel *changeSelection) map[string]tierPlan {
	out := map[string]tierPlan{}
	for _, p := range sel.plan() {
		out[p.name] = p
	}
	return out
}

func tierNamesOf(plan map[string]tierPlan) []string {
	var out []string
	for _, t := range allTiers() {
		if _, ok := plan[t]; ok {
			out = append(out, t)
		}
	}
	return out
}

// TestSelectionWalksTheFullClosureOfDependents pins CS-4 over a changed
// leaf package: every package whose build depends on it, at any depth, and
// every package whose tests import one of those, each in the tier that runs
// it. The static tier's own packages are never selected again, and a
// single-package tier runs whole.
func TestSelectionWalksTheFullClosureOfDependents(t *testing.T) {
	plan := planOf(selectChanged([]string{"pkg/leaf/a.go"}, fixtureGraph(), nil, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if got, want := tierNamesOf(plan), []string{tierStatic, tierUnit, tierComponent, tierContract, tierIntegration}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v", got, want)
	}
	if got, want := plan[tierUnit].pkgs, []string{"./cmd/app", "./pkg/leaf", "./pkg/mid", "./pkg/side", "./pkg/top"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unit scope = %v, want %v", got, want)
	}
	if got := plan[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./tests/component/leafc"}) {
		t.Errorf("component scope = %v", got)
	}
	if got := plan[tierContract].pkgs; !reflect.DeepEqual(got, []string{"./tests/contract/midc"}) {
		t.Errorf("contract scope = %v", got)
	}
	if plan[tierIntegration].pkgs != nil {
		t.Errorf("the integration tier is one package and runs whole; got scope %v", plan[tierIntegration].pkgs)
	}
	if plan[tierStatic].pkgs != nil {
		t.Errorf("the static tier runs whole; got %v", plan[tierStatic].pkgs)
	}
}

// TestATestImportEndsTheWalk pins that a package whose tests import a
// changed package is selected while its own importers are not: nothing
// imports a package's tests.
func TestATestImportEndsTheWalk(t *testing.T) {
	g := fixtureGraph()
	deps := g.dependents([]string{moduleImportPath + "/pkg/leaf"}, -1)
	if !contains(deps, moduleImportPath+"/pkg/side") {
		t.Errorf("dependents = %v, want pkg/side, whose tests import pkg/leaf", deps)
	}
	if contains(deps, moduleImportPath+"/pkg/sidedep") {
		t.Errorf("dependents = %v include pkg/sidedep, which imports only pkg/side's build", deps)
	}

	// A package reached through a test edge and a build edge is walked on
	// through the build edge: x's tests import a, and x's build imports b,
	// which imports a, so y, which imports x, depends on a.
	m := moduleImportPath + "/"
	both := newPkgGraph()
	both.addPackage(m+"a", "a", nil, nil, nil)
	both.addPackage(m+"b", "b", []string{m + "a"}, nil, nil)
	both.addPackage(m+"x", "x", []string{m + "b"}, []string{m + "a"}, nil)
	both.addPackage(m+"y", "y", []string{m + "x"}, nil, nil)
	if got := both.dependents([]string{m + "a"}, -1); !reflect.DeepEqual(got, []string{m + "b", m + "x", m + "y"}) {
		t.Errorf("dependents through a test edge and a build edge = %v, want b, x, and y", got)
	}
}

// TestRdepsDepthNarrowsTheWalk pins the operator flag: zero selects the
// changed package alone, one adds its direct dependents, and a negative
// depth is the full closure. Cycles end the walk.
func TestRdepsDepthNarrowsTheWalk(t *testing.T) {
	g := fixtureGraph()
	m := moduleImportPath + "/"
	if got := g.dependents([]string{m + "pkg/leaf"}, 0); len(got) != 0 {
		t.Errorf("depth 0 = %v", got)
	}
	want1 := []string{m + "pkg/mid", m + "pkg/side", m + "tests/component/leafc", m + "tests/static"}
	if got := g.dependents([]string{m + "pkg/leaf"}, 1); !reflect.DeepEqual(got, want1) {
		t.Errorf("depth 1 = %v, want %v", got, want1)
	}
	if got := g.dependents([]string{m + "pkg/leaf"}, -1); !contains(got, m+"cmd/app") || !contains(got, m+"tests/integration") {
		t.Errorf("full closure = %v", got)
	}
	cyc := newPkgGraph()
	cyc.addPackage(m+"pkg/ca", "pkg/ca", []string{m + "pkg/cb"}, nil, nil)
	cyc.addPackage(m+"pkg/cb", "pkg/cb", []string{m + "pkg/ca"}, nil, nil)
	if got := cyc.dependents([]string{m + "pkg/ca"}, -1); !reflect.DeepEqual(got, []string{m + "pkg/cb"}) {
		t.Errorf("a cycle ends the walk; got %v", got)
	}
	sel := planOf(selectChanged([]string{"pkg/leaf/a.go"}, g, nil, specEdits{}, selectOpts{rdepsDepth: 0}))
	if got := sel[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/leaf"}) {
		t.Errorf("--rdeps-depth 0 unit scope = %v", got)
	}
}

// TestEmbeddedFilesTestdataAndBrokenFilesSelectTheirOwners pins the owning
// package: the nearest package directory holds a testdata fixture and a
// file that does not compile, and a package that embeds a file from a
// subdirectory package owns that file as well.
func TestEmbeddedFilesTestdataAndBrokenFilesSelectTheirOwners(t *testing.T) {
	g := fixtureGraph()
	m := moduleImportPath + "/"
	if got := g.ownersOf("pkg/leaf/testdata/case/input.json"); !reflect.DeepEqual(got, []string{m + "pkg/leaf"}) {
		t.Errorf("testdata owner = %v", got)
	}
	if got := g.ownersOf("pkg/leaf/broken.go"); !reflect.DeepEqual(got, []string{m + "pkg/leaf"}) {
		t.Errorf("broken file owner = %v", got)
	}
	if got := g.ownersOf("pkg/assets/sub/x.tmpl"); !reflect.DeepEqual(got, []string{m + "pkg/assets/sub", m + "pkg/assets"}) {
		t.Errorf("embedded file owners = %v", got)
	}
	if got := g.ownersOf("README.md"); got != nil {
		t.Errorf("a root-level file has no owner; got %v", got)
	}
}

// TestModuleWideFilesAreReportedAndAllRunsEveryPackage pins go.mod, go.sum,
// and go.work: no package scope describes them, so the selection notes them
// and only --all runs the whole unit tier.
func TestModuleWideFilesAreReportedAndAllRunsEveryPackage(t *testing.T) {
	opts := selectOpts{rdepsDepth: fullRdepsDepth}
	sel := selectChanged([]string{"go.mod", "go.sum"}, fixtureGraph(), nil, specEdits{}, opts)
	if got := tierNamesOf(planOf(sel)); !reflect.DeepEqual(got, []string{tierStatic}) {
		t.Errorf("tiers = %v", got)
	}
	notes := strings.Join(sel.notes(opts), "\n")
	if !strings.Contains(notes, "module-wide build file(s) changed (go.mod, go.sum)") {
		t.Errorf("notes = %q", notes)
	}
	opts.all = true
	all := selectChanged([]string{"go.mod"}, fixtureGraph(), nil, specEdits{}, opts)
	if p := planOf(all)[tierUnit]; p.name != tierUnit || p.pkgs != nil {
		t.Errorf("--all unit plan = %+v, want the whole tier", p)
	}
	if strings.Contains(strings.Join(all.notes(opts), "\n"), "module-wide") {
		t.Errorf("--all already runs every package, so the module-wide note is noise")
	}
}

// TestNonGoInputRowsSelectTiersAndPackages pins CS-6: a tier target runs
// the whole tier, a test package target runs that package, and a helper
// package target selects the tests that import it.
func TestNonGoInputRowsSelectTiersAndPackages(t *testing.T) {
	opts := selectOpts{rdepsDepth: fullRdepsDepth}
	plan := planOf(selectChanged([]string{"charts/lenny/templates/x.yaml"}, fixtureGraph(), fixtureInputs(), specEdits{}, opts))
	if got := tierNamesOf(plan); !reflect.DeepEqual(got, []string{tierStatic, tierComponent, tierE2EKind}) {
		t.Fatalf("tiers = %v", got)
	}
	if got := plan[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./tests/component/leafc"}) {
		t.Errorf("component scope = %v", got)
	}
	helper := planOf(selectChanged([]string{"compose/default.yml"}, fixtureGraph(), fixtureInputs(), specEdits{}, opts))
	if _, ok := helper[tierChaos]; !ok {
		t.Errorf("a change the helper reads must select the tests that import it; tiers = %v", tierNamesOf(helper))
	}
	if got := helper[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/helper"}) {
		t.Errorf("helper unit scope = %v", got)
	}
	noGraph := planOf(selectChanged([]string{"compose/default.yml"}, nil, fixtureInputs(), specEdits{}, opts))
	if got := noGraph[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/helper"}) {
		t.Errorf("without a graph a package target runs alone; unit scope = %v", got)
	}
}

// TestBaseRowsJoinTheBranchRowsAndDeadTargetsSelectNothing pins that a
// branch is selected by the union of its rows and the base commit's, so a
// branch that deletes a row still runs what it named, and that a target
// the graph no longer holds selects nothing.
func TestBaseRowsJoinTheBranchRowsAndDeadTargetsSelectNothing(t *testing.T) {
	branch := &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion}
	base := fixtureInputs()
	base.Inputs = append(base.Inputs, nonGoInput{Paths: []string{"compose/**"}, Runs: []string{"pkg/gone"}, Reason: "removed since"})
	merged := branch.union(base)
	plan := planOf(selectChanged([]string{"charts/lenny/values.yaml", "compose/default.yml"}, fixtureGraph(), merged, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if _, ok := plan[tierE2EKind]; !ok {
		t.Errorf("a row the branch deleted must still select; tiers = %v", tierNamesOf(plan))
	}
	if contains(plan[tierUnit].pkgs, "./pkg/gone") {
		t.Errorf("a target the graph does not hold must select nothing; unit = %v", plan[tierUnit].pkgs)
	}
	var none *nonGoInputs
	if none.union(nil) != nil || none.union(base) != base || base.union(nil) != base {
		t.Errorf("a union with a nil list must return the other list")
	}
}

// TestUnreadableSourcesFailSafeToTheMergeGateTiers pins that a source the
// selection cannot read widens it: unreadable specification edits and an
// unreadable diff each run the unit, component, contract, and integration
// tiers whole.
func TestUnreadableSourcesFailSafeToTheMergeGateTiers(t *testing.T) {
	plan := planOf(selectChanged([]string{"spec/05_pools.md"}, fixtureGraph(), fixtureInputs(), specEdits{failed: true}, selectOpts{rdepsDepth: fullRdepsDepth}))
	for _, tier := range failSafeTiers {
		if p, ok := plan[tier]; !ok || p.pkgs != nil {
			t.Errorf("unreadable edits must run %s whole; got %+v", tier, p)
		}
	}
	if _, err := changedPathsFor("no-such-ref-0116"); err == nil {
		t.Errorf("an unknown ref must fail the diff rather than read as empty")
	}
	diffPlan, notes := resolveChangedPlanFor("no-such-ref-0116", selectOpts{rdepsDepth: fullRdepsDepth})
	got := map[string]tierPlan{}
	for _, p := range diffPlan {
		got[p.name] = p
	}
	for _, tier := range failSafeTiers {
		if p, ok := got[tier]; !ok || p.pkgs != nil {
			t.Errorf("an unreadable diff must run %s whole; got %+v", tier, p)
		}
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "the diff is unreadable") {
		t.Errorf("notes = %v", notes)
	}
}

// TestNoGoFileNegatesATierTag pins the assumption loadPkgGraph rests on:
// the graph is loaded with every tier's tag set at once, so a file whose
// build constraint negates a tier tag would drop out of the graph with the
// imports it carries.
func TestNoGoFileNegatesATierTag(t *testing.T) {
	files, err := trackedFiles(context.Background(), repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	negated := regexp.MustCompile(`!\s*(` + strings.Join(tierBuildTags(), "|") + `)\b`)
	for _, f := range files {
		if !strings.HasSuffix(f, ".go") || strings.Contains(f, "/testdata/") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(repoRoot(), f))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(body), "\n") {
			if strings.HasPrefix(line, "package ") {
				break
			}
			if strings.HasPrefix(line, "//go:build") && negated.MatchString(line) {
				t.Errorf("%s: %q negates a tier tag, so the selector's graph leaves the file out", f, line)
			}
		}
	}
}

// TestUnownedPathsAreReportedAndSelectOnlyTheStaticTier pins the note for a
// path no package owns and no row matches, and that the records no test
// reads select nothing without a note.
func TestUnownedPathsAreReportedAndSelectOnlyTheStaticTier(t *testing.T) {
	opts := selectOpts{rdepsDepth: fullRdepsDepth}
	paths := []string{".github/workflows/ci.yml", "changes/0001-x/change.md", "proposals/0001.md", "issues/X/X-I1.md", "CHANGELOG.md"}
	sel := selectChanged(paths, fixtureGraph(), fixtureInputs(), specEdits{}, opts)
	if got := tierNamesOf(planOf(sel)); !reflect.DeepEqual(got, []string{tierStatic}) {
		t.Errorf("tiers = %v", got)
	}
	if !reflect.DeepEqual(sel.unowned, []string{".github/workflows/ci.yml"}) {
		t.Errorf("unowned = %v", sel.unowned)
	}
	if notes := strings.Join(sel.notes(opts), "\n"); !strings.Contains(notes, "select the static tier only: .github/workflows/ci.yml") {
		t.Errorf("notes = %q", notes)
	}
}

// TestSpecificationEditsRunFunctionsByName pins how CS-5's result joins the
// selection: the cited tests run by name in their tiers, a test in a tier
// that cannot run by name runs its tier whole, the packages whose code
// cites a changed identifier are changed, and a package selected whole
// drops the names it would run.
func TestSpecificationEditsRunFunctionsByName(t *testing.T) {
	edits := specEdits{
		tests: []annotation.TestFunc{
			{Dir: "pkg/orphan", Name: "TestOrphan"},
			{Dir: "pkg/leaf", Name: "TestLeafCited"},
			{Dir: "tests/component/other", Name: "TestComponentCited"},
			{Dir: "tests/integration", Name: "TestIntegrationCited"},
			{Dir: "tests/chaos", Name: "TestChaosCited"},
			{Dir: "tests/static", Name: "TestStaticCited"},
		},
		changedDirs: []string{"pkg/mid"},
		unitDirs:    []string{"pkg/helper"},
		notes:       []string{"a note"},
	}
	opts := selectOpts{rdepsDepth: fullRdepsDepth}
	sel := selectChanged(nil, fixtureGraph(), nil, edits, opts)
	plan := planOf(sel)
	if got := plan[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./cmd/app", "./pkg/helper", "./pkg/mid", "./pkg/top"}) {
		t.Errorf("unit scope = %v", got)
	}
	if got := plan[tierUnit].funcs; !reflect.DeepEqual(got, map[string][]string{"./pkg/leaf": {"TestLeafCited"}, "./pkg/orphan": {"TestOrphan"}}) {
		t.Errorf("unit by-name = %v", got)
	}
	if got := plan[tierComponent].funcs; !reflect.DeepEqual(got, map[string][]string{"./tests/component/other": {"TestComponentCited"}}) {
		t.Errorf("component by-name = %v", got)
	}
	if p := plan[tierIntegration]; p.pkgs != nil {
		t.Errorf("pkg/mid's closure reaches the integration package, so it runs whole; got %+v", p)
	}
	if p, ok := plan[tierChaos]; !ok || p.pkgs != nil {
		t.Errorf("the chaos tier cannot run a function by name, so it runs whole; got %+v", p)
	}
	if !contains(sel.notes(opts), "a note") {
		t.Errorf("notes = %v", sel.notes(opts))
	}

	byName := planOf(selectChanged(nil, fixtureGraph(), nil, specEdits{tests: []annotation.TestFunc{{Dir: "tests/integration", Name: "TestIntegrationCited"}}}, opts))
	if p := byName[tierIntegration]; p.pkgs == nil || !reflect.DeepEqual(p.funcs, map[string][]string{"./tests/integration": {"TestIntegrationCited"}}) {
		t.Errorf("an integration test cited alone runs by name; got %+v", p)
	}
}

// TestSelectPackagesResolvesThroughTheGraph pins `--pkg`: the packages at
// or under each named directory are changed, and an unknown name is
// returned.
func TestSelectPackagesResolvesThroughTheGraph(t *testing.T) {
	sel, unknown := selectPackages([]string{"./pkg/mid/...", "pkg/nowhere"}, fixtureGraph(), selectOpts{rdepsDepth: fullRdepsDepth})
	plan := planOf(sel)
	if got := plan[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./cmd/app", "./pkg/mid", "./pkg/top"}) {
		t.Errorf("unit scope = %v", got)
	}
	if _, ok := plan[tierIntegration]; !ok {
		t.Errorf("tiers = %v, want the integration tier, whose tests import pkg/top", tierNamesOf(plan))
	}
	if !reflect.DeepEqual(unknown, []string{"pkg/nowhere"}) {
		t.Errorf("unknown = %v", unknown)
	}
	tagged, _ := selectPackages([]string{"cmd/lenny-ops"}, fixtureGraph(), selectOpts{rdepsDepth: fullRdepsDepth, all: true})
	tp := planOf(tagged)
	if got := tp[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./cmd/lenny-ops"}) {
		t.Errorf("a package with in-package component tests runs in the component tier too; got %v", got)
	}
	if tp[tierUnit].pkgs != nil {
		t.Errorf("--all runs the whole unit tier; got %v", tp[tierUnit].pkgs)
	}
}

// TestCollapsePatternsDropsCoveredPackages pins the scope's printed form.
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

// TestDiffRange pins how a --since value becomes a git range.
func TestDiffRange(t *testing.T) {
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

// TestMergeDiscoveryPlansNeverNarrows pins the union of --changed, --spec,
// and --pkg: scopes and by-name functions union per tier, and an unscoped
// contributor or --all leaves a tier unscoped.
func TestMergeDiscoveryPlansNeverNarrows(t *testing.T) {
	changed := []tierPlan{
		{name: tierStatic},
		{name: tierUnit, pkgs: []string{"./pkg/a"}, funcs: map[string][]string{"./pkg/x": {"TestX"}}},
		{name: tierComponent, pkgs: []string{"./tests/component/a"}},
	}
	pkg := []tierPlan{
		{name: tierUnit, pkgs: []string{"./pkg/b/...", "./pkg/x"}},
		{name: tierContract},
		{name: tierComponent, pkgs: []string{}, funcs: map[string][]string{"./tests/component/b": {"TestB"}}},
	}
	spec := []tierPlan{{name: tierUnit}, {name: tierComponent}}

	merged := mergeDiscoveryPlans([][]tierPlan{changed, pkg}, false)
	if names := planNames(merged); !reflect.DeepEqual(names, []string{"static", "unit", "component", "contract"}) {
		t.Errorf("tiers = %v", names)
	}
	if got := merged[1].pkgs; !reflect.DeepEqual(got, []string{"./pkg/a", "./pkg/b/...", "./pkg/x"}) {
		t.Errorf("merged unit scope = %v", got)
	}
	if merged[1].funcs != nil {
		t.Errorf("./pkg/x runs whole, so its by-name functions drop; got %v", merged[1].funcs)
	}
	if got := merged[2].funcs; !reflect.DeepEqual(got, map[string][]string{"./tests/component/b": {"TestB"}}) {
		t.Errorf("merged component by-name = %v", got)
	}
	if merged[3].pkgs != nil {
		t.Errorf("an unscoped contract tier stays unscoped; got %v", merged[3].pkgs)
	}
	withSpec := mergeDiscoveryPlans([][]tierPlan{changed, spec}, false)
	if withSpec[1].pkgs != nil || withSpec[2].pkgs != nil {
		t.Errorf("an unscoped contributor must leave the tier unscoped; got %+v", withSpec)
	}
	if got := mergeDiscoveryPlans([][]tierPlan{changed}, true)[1].pkgs; got != nil {
		t.Errorf("--all must leave the unit tier unscoped; got %v", got)
	}
}

// TestDryRunListsEachTierScopeAndNotes pins the dry-run report, which is
// how an operator sees what --changed will run before running it.
func TestDryRunListsEachTierScopeAndNotes(t *testing.T) {
	var buf bytes.Buffer
	writeDryRun(&buf, selector{changed: true, rdepsDepth: -1}, resolvedSelector{
		tiers: []tierPlan{
			{name: tierStatic},
			{name: tierUnit, pkgs: []string{"./pkg/a"}, funcs: map[string][]string{"./pkg/b": {"TestB1", "TestB2"}}},
			{name: tierComponent, pkgs: []string{"./tests/component/a"}},
			{name: tierIntegration},
		},
		notes: []string{"1 changed path(s) that no Go package owns: X"},
	})
	out := buf.String()
	for _, want := range []string{"rdeps-depth=-1", "- unit", "pkg:    ./pkg/a", "run:    ./pkg/b: TestB1, TestB2", "pkg:    ./tests/component/a", "note: 1 changed path(s)"} {
		if !strings.Contains(out, want) {
			t.Errorf("dry run lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "every package") != 0 {
		t.Errorf("only an unscoped unit tier prints every package:\n%s", out)
	}
	buf.Reset()
	writeDryRun(&buf, selector{tiers: []string{tierUnit}}, resolvedSelector{tiers: []tierPlan{{name: tierUnit}}})
	if !strings.Contains(buf.String(), "./... (every package)") {
		t.Errorf("unscoped dry run does not say it runs every package:\n%s", buf.String())
	}
}

// TestParsePkgGraphSeparatesBuildAndTestImports pins the reading of
// `go list -json`: test and external test imports are test edges, embedded
// files are joined to the package directory, and packages outside the
// module or at its root are skipped.
func TestParsePkgGraphSeparatesBuildAndTestImports(t *testing.T) {
	m := moduleImportPath + "/"
	out := `{"ImportPath":"` + m + `pkg/a","Dir":"/r/pkg/a","Imports":["` + m + `pkg/b"],"TestImports":["` + m + `pkg/c"],"XTestImports":["` + m + `pkg/a","` + m + `pkg/d"],"EmbedFiles":["t/x.tmpl"],"TestEmbedFiles":["testdata/y.json"]}
{"ImportPath":"` + moduleImportPath + `","Dir":"/r"}
{"ImportPath":"example.com/other","Dir":"/elsewhere/other"}`
	g, err := parsePkgGraph([]byte(out), "/r")
	if err != nil {
		t.Fatalf("parsePkgGraph: %v", err)
	}
	if !reflect.DeepEqual(g.importers[m+"pkg/b"], []string{m + "pkg/a"}) {
		t.Errorf("build importers of pkg/b = %v", g.importers[m+"pkg/b"])
	}
	for _, dep := range []string{"pkg/c", "pkg/d"} {
		if !reflect.DeepEqual(g.testImporters[m+dep], []string{m + "pkg/a"}) {
			t.Errorf("test importers of %s = %v", dep, g.testImporters[m+dep])
		}
	}
	if len(g.testImporters[m+"pkg/a"]) != 0 {
		t.Errorf("an external test's import of its own package is no edge")
	}
	if got := g.embedOwners["pkg/a/t/x.tmpl"]; !reflect.DeepEqual(got, []string{m + "pkg/a"}) {
		t.Errorf("embed owners = %v", got)
	}
	if got := g.embedOwners["pkg/a/testdata/y.json"]; !reflect.DeepEqual(got, []string{m + "pkg/a"}) {
		t.Errorf("test embed owners = %v", got)
	}
	if len(g.dirToPkg) != 1 {
		t.Errorf("dirToPkg = %v, want only pkg/a", g.dirToPkg)
	}
	if _, err := parsePkgGraph([]byte("{not json"), "/r"); err == nil {
		t.Errorf("malformed go list output must fail")
	}
}

// TestLoadPkgGraphReadsAFixtureModuleWithEveryTierTag loads a fixture module
// through `go list`: a package carrying only a tier's build tag is in the
// graph with its imports, a package with a file that does not compile keeps
// its place, and an embedded file selects its owner.
func TestLoadPkgGraphReadsAFixtureModuleWithEveryTierTag(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("blocked: go is not on PATH")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":                           "module " + moduleImportPath + "\n\ngo 1.22\n",
		"pkg/leaf/leaf.go":                 "package leaf\n\nimport _ \"embed\"\n\n//go:embed sub/data.txt\nvar Data string\n",
		"pkg/leaf/sub/data.txt":            "data\n",
		"pkg/leaf/sub/sub.go":              "package sub\n",
		"pkg/broken/broken.go":             "package broken\n\nfunc {\n",
		"tests/component/c/c_test.go":      "//go:build component\n\npackage c\n\nimport (\n\t\"testing\"\n\n\t_ \"" + moduleImportPath + "/pkg/leaf\"\n)\n\nfunc TestC(t *testing.T) {}\n",
		"tests/integration/i_test.go":      "//go:build integration\n\npackage integration\n\nimport (\n\t\"testing\"\n\n\t_ \"" + moduleImportPath + "/pkg/leaf\"\n)\n\nfunc TestI(t *testing.T) {}\n",
		"pkg/mid/mid.go":                   "package mid\n\nimport _ \"" + moduleImportPath + "/pkg/leaf\"\n",
		"pkg/untagged/untagged_test.go":    "package untagged\n\nimport \"testing\"\n\nfunc TestU(t *testing.T) {}\n",
		"pkg/untagged/component_x_test.go": "//go:build component\n\npackage untagged\n\nimport _ \"" + moduleImportPath + "/pkg/mid\"\n",
	}
	for p, body := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	g, err := loadPkgGraph(context.Background(), root)
	if err != nil {
		t.Fatalf("loadPkgGraph: %v", err)
	}
	m := moduleImportPath + "/"
	if _, ok := g.dirToPkg["pkg/broken"]; !ok {
		t.Errorf("a package with a file that does not compile must stay in the graph; dirs = %v", g.dirToPkg)
	}
	deps := g.dependents([]string{m + "pkg/leaf"}, -1)
	for _, want := range []string{"tests/component/c", "tests/integration", "pkg/mid", "pkg/untagged"} {
		if !contains(deps, m+want) {
			t.Errorf("dependents of pkg/leaf = %v, want %s", deps, want)
		}
	}
	if got := g.ownersOf("pkg/leaf/sub/data.txt"); !contains(got, m+"pkg/leaf") || !contains(got, m+"pkg/leaf/sub") {
		t.Errorf("owners of the embedded file = %v", got)
	}
}

// TestLoadPkgGraphReadsTheModule loads the real module graph and checks two
// edges that are stable by construction: this command imports the
// annotation index, and the integration package, which only builds under
// its tag, is in the graph.
func TestLoadPkgGraphReadsTheModule(t *testing.T) {
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
	idx := moduleImportPath + "/scripts/specshift/annotation"
	if !contains(g.importers[idx], self) {
		t.Errorf("importers of the annotation index = %v; want %s", g.importers[idx], self)
	}
	if _, ok := g.dirToPkg["tests/integration"]; !ok {
		t.Errorf("the integration package is missing; the graph was loaded without the tier tags")
	}
}

func planNames(plan []tierPlan) []string {
	out := make([]string, 0, len(plan))
	for _, p := range plan {
		out = append(out, p.name)
	}
	return out
}

// TestATestsTargetRunsThePackageWithoutItsDependents pins the `tests` form
// of a non-Go input row (change 0172, GS-1): the package's own tests run
// in the tier that runs them, and none of its dependents is selected,
// while the same package under `runs` still selects its dependents. A
// `tests` target the graph does not hold selects nothing and still counts
// as a match, and without a graph the package is selected alone.
func TestATestsTargetRunsThePackageWithoutItsDependents(t *testing.T) {
	opts := selectOpts{rdepsDepth: fullRdepsDepth}
	row := func(runs, tests []string) *nonGoInputs {
		return &nonGoInputs{Kind: nonGoInputsKind, Version: nonGoInputsVersion, Inputs: []nonGoInput{
			{Paths: []string{"compose/**"}, Runs: runs, Tests: tests, Reason: "reads it"},
		}}
	}

	own := planOf(selectChanged([]string{"compose/a.yml"}, fixtureGraph(), row(nil, []string{"pkg/leaf", "tests/component/leafc"}), specEdits{}, opts))
	if got, want := tierNamesOf(own), []string{tierStatic, tierUnit, tierComponent}; !reflect.DeepEqual(got, want) {
		t.Fatalf("tiers = %v, want %v", got, want)
	}
	if got := own[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/leaf"}) {
		t.Errorf("unit scope = %v; want pkg/leaf alone", got)
	}
	if got := own[tierComponent].pkgs; !reflect.DeepEqual(got, []string{"./tests/component/leafc"}) {
		t.Errorf("component scope = %v", got)
	}

	walked := planOf(selectChanged([]string{"compose/a.yml"}, fixtureGraph(), row([]string{"pkg/leaf"}, nil), specEdits{}, opts))
	if got, want := walked[tierUnit].pkgs, []string{"./cmd/app", "./pkg/leaf", "./pkg/mid", "./pkg/side", "./pkg/top"}; !reflect.DeepEqual(got, want) {
		t.Errorf("a runs target's unit scope = %v, want its dependents %v", got, want)
	}
	if _, ok := walked[tierIntegration]; !ok {
		t.Errorf("a runs target no longer walks its dependents into the integration tier: %v", tierNamesOf(walked))
	}

	gone := selectChanged([]string{"compose/a.yml"}, fixtureGraph(), row(nil, []string{"pkg/gone"}), specEdits{}, opts)
	if got := tierNamesOf(planOf(gone)); !reflect.DeepEqual(got, []string{tierStatic}) {
		t.Errorf("a tests target outside the graph selected %v", got)
	}
	if len(gone.unowned) != 0 {
		t.Errorf("a path a row matches is reported as unowned: %v", gone.unowned)
	}

	noGraph := planOf(selectChanged([]string{"compose/a.yml"}, nil, row(nil, []string{"pkg/leaf"}), specEdits{}, opts))
	if got := noGraph[tierUnit].pkgs; !reflect.DeepEqual(got, []string{"./pkg/leaf"}) {
		t.Errorf("without a graph, unit scope = %v; want pkg/leaf", got)
	}
}

// TestCommittedSpecRowSelectsNoDependentOfItsPackages pins the committed
// spec/** row: a specification edit runs the tests of the packages whose
// tests read the specification and none of their dependents, so the
// tracing package's importers across the module are not selected.
func TestCommittedSpecRowSelectsNoDependentOfItsPackages(t *testing.T) {
	inputs, err := readNonGoInputs(repoRoot())
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range inputs.Inputs {
		if contains(in.Paths, "spec/**") {
			for _, target := range in.Runs {
				if strings.Contains(target, "/") {
					t.Errorf("the spec/** row walks the dependents of %s; list it under tests", target)
				}
			}
		}
	}
	m := moduleImportPath + "/"
	g := newPkgGraph()
	g.addPackage(m+"pkg/observability/tracing", "pkg/observability/tracing", nil, nil, nil)
	g.addPackage(m+"pkg/api/openapi", "pkg/api/openapi", nil, nil, nil)
	g.addPackage(m+"pkg/gateway/core", "pkg/gateway/core", []string{m + "pkg/observability/tracing", m + "pkg/api/openapi"}, nil, nil)
	g.addPackage(m+"tests/contract/rest_sessions", "tests/contract/rest_sessions", nil, []string{m + "pkg/gateway/core"}, nil)
	g.addPackage(m+"tests/integration", "tests/integration", nil, []string{m + "pkg/observability/tracing"}, nil)
	g.addPackage(m+"tests/conformance", "tests/conformance", nil, nil, nil)

	plan := planOf(selectChanged([]string{"spec/05_pools.md"}, g, inputs, specEdits{}, selectOpts{rdepsDepth: fullRdepsDepth}))
	if got, want := plan[tierUnit].pkgs, []string{"./pkg/api/openapi", "./pkg/observability/tracing"}; !reflect.DeepEqual(got, want) {
		t.Errorf("unit scope = %v, want %v", got, want)
	}
	for _, tier := range []string{tierContract, tierIntegration} {
		if _, ok := plan[tier]; ok {
			t.Errorf("a specification edit selected the %s tier through a dependent of the spec/** row's packages", tier)
		}
	}
	if _, ok := plan[tierConformance]; !ok {
		t.Errorf("the conformance tests that read the specification are not selected: %v", tierNamesOf(plan))
	}
}
