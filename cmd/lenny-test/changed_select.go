// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// This file holds `--changed` and `--pkg` selection (change 0116, CS-4 and
// CS-6): which test packages, in which tiers, a diff reaches. Three sources
// feed it:
//
//   - The Go import graph. A changed file selects its owning package and
//     every package whose build, test builds included, depends on that
//     package directly or transitively. The graph is loaded with every
//     tier's build tag, so a tagged tier package is in it.
//   - The specification edits of the diff (spec_edit_select.go, CS-5),
//     which select test functions by name and treat the packages whose code
//     cites a changed identifier as changed.
//   - tests/non-go-inputs.yaml (non_go_inputs.go, CS-6), for the inputs no
//     Go package owns, such as the chart and the compose stack.
//
// The selection replaced tests/change-graph.json, a hand-kept map from path
// prefixes to tier targets; no selector reads that file any more. The
// harness is contributor tooling, so this file carries no specification
// annotation.

// moduleImportPath is the import-path prefix of this repository's module.
const moduleImportPath = "github.com/lennylabs/lenny"

// defaultRdepsDepth is how many hops of in-module dependents `--changed`
// and `--pkg` add to the changed packages. A negative depth walks the full
// closure, which is the "changed packages and their dependents" the merge
// gate runs (change 0154): one hop left out dependents whose build still
// breaks. Operators narrow the walk in an inner loop with `--rdeps-depth`.
const defaultRdepsDepth = -1

// goListTimeout bounds the `go list` call that loads the package graph.
const goListTimeout = 2 * time.Minute

// globalChangePaths are repo-relative files whose change can alter the build
// of every package, so no package-level scope describes their blast radius.
// A diff that touches one of them is reported, and `--all` runs every
// package; without `--all` the unit scope stays at what the rest of the diff
// selects, and the static tier's module-wide `go vet ./...` compiles every
// package against the new module graph.
var globalChangePaths = map[string]bool{
	"go.mod":  true,
	"go.sum":  true,
	"go.work": true,
}

// pkgGraph is the in-module Go package graph the selector walks.
//
// The graph keeps build edges and test edges apart. A package's build
// depends on another when it imports it, and that dependency carries on to
// every package that imports the first one. A package's test build depends
// on the packages its tests import, but nothing imports a package's tests,
// so a test edge ends the walk: the package whose tests import a changed
// package is selected, and its own importers are not, unless they reach
// the changed package some other way.
type pkgGraph struct {
	// dirToPkg maps a repo-relative package directory to its import path.
	dirToPkg map[string]string
	// pkgToDir maps an import path back to its repo-relative directory.
	pkgToDir map[string]string
	// importers maps an import path to the in-module packages whose build
	// imports it.
	importers map[string][]string
	// testImporters maps an import path to the in-module packages whose
	// tests, and not whose build, import it.
	testImporters map[string][]string
	// embedOwners maps a repo-relative file to the packages that embed it
	// with //go:embed, including a test embed.
	embedOwners map[string][]string
}

// newPkgGraph returns an empty graph ready for addPackage.
func newPkgGraph() *pkgGraph {
	return &pkgGraph{
		dirToPkg:      map[string]string{},
		pkgToDir:      map[string]string{},
		importers:     map[string][]string{},
		testImporters: map[string][]string{},
		embedOwners:   map[string][]string{},
	}
}

// addPackage records one package: its repo-relative directory, the import
// paths its build imports, the import paths only its tests import (internal
// and external test packages alike), and the repo-relative files it embeds.
// Imports outside the module and a package's import of itself, which an
// external test package makes, are ignored.
func (g *pkgGraph) addPackage(importPath, dir string, imports, testImports, embeds []string) {
	g.dirToPkg[dir] = importPath
	g.pkgToDir[importPath] = dir
	build := map[string]bool{}
	for _, imp := range imports {
		if imp == importPath || build[imp] || !inModule(imp) {
			continue
		}
		build[imp] = true
		g.importers[imp] = append(g.importers[imp], importPath)
	}
	seen := map[string]bool{}
	for _, imp := range testImports {
		if imp == importPath || build[imp] || seen[imp] || !inModule(imp) {
			continue
		}
		seen[imp] = true
		g.testImporters[imp] = append(g.testImporters[imp], importPath)
	}
	for _, f := range embeds {
		if !contains(g.embedOwners[f], importPath) {
			g.embedOwners[f] = append(g.embedOwners[f], importPath)
		}
	}
}

func inModule(importPath string) bool {
	return importPath == moduleImportPath || strings.HasPrefix(importPath, moduleImportPath+"/")
}

// packageForPath returns the import path of the package whose directory,
// or nearest ancestor directory, holds a changed repo-relative path, so a
// changed testdata fixture or asset selects the package that reads it. The
// module root itself is never returned, because a root-level file (a
// README, a Makefile) is not owned by a root package. It returns "" when no
// package directory holds the path.
func (g *pkgGraph) packageForPath(p string) string {
	for dir := path.Dir(p); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if pkg, ok := g.dirToPkg[dir]; ok {
			return pkg
		}
	}
	return ""
}

// ownersOf returns the owning packages of a changed path: the package
// packageForPath names, and every package that embeds the file. A package
// can embed a file from a subdirectory that is itself a package, so the
// directory walk alone would miss the embedding package. A file that does
// not load (a syntax error in the diff) keeps its owner, because the walk
// reads directories and `go list -e` still reports the package.
func (g *pkgGraph) ownersOf(p string) []string {
	var out []string
	if pkg := g.packageForPath(p); pkg != "" {
		out = append(out, pkg)
	}
	for _, pkg := range g.embedOwners[p] {
		if !contains(out, pkg) {
			out = append(out, pkg)
		}
	}
	return out
}

// dependents returns the in-module packages whose build or test build
// depends on any of roots, walking at most depth hops; a negative depth
// walks the full closure. A build importer is walked on, and a test
// importer is selected and not walked on (see pkgGraph). The roots
// themselves are not in the result.
func (g *pkgGraph) dependents(roots []string, depth int) []string {
	seen := map[string]bool{}
	for _, r := range roots {
		seen[r] = true
	}
	frontier := append([]string(nil), roots...)
	out := []string{}
	for hop := 0; len(frontier) > 0 && (depth < 0 || hop < depth); hop++ {
		next := []string{}
		for _, p := range frontier {
			for _, imp := range g.importers[p] {
				if !seen[imp] {
					seen[imp] = true
					out = append(out, imp)
					next = append(next, imp)
				}
			}
			for _, imp := range g.testImporters[p] {
				if !seen[imp] {
					seen[imp] = true
					out = append(out, imp)
				}
			}
		}
		frontier = next
	}
	sort.Strings(out)
	return out
}

// goListPackage is the part of `go list -json` output the graph reads.
type goListPackage struct {
	ImportPath      string
	Dir             string
	Imports         []string
	TestImports     []string
	XTestImports    []string
	EmbedFiles      []string
	TestEmbedFiles  []string
	XTestEmbedFiles []string
}

// goListFields names the fields loadPkgGraph asks `go list -json` for.
const goListFields = "ImportPath,Dir,Imports,TestImports,XTestImports,EmbedFiles,TestEmbedFiles,XTestEmbedFiles"

// loadPkgGraph builds the package graph for the module rooted at root with
// one `go list` call carrying every tier's build tag (tierBuildTags), so
// the tagged tier packages and the tagged test files of untagged packages
// are in the graph. -e keeps a package that fails to load, such as one with
// a broken file in the diff, in the output rather than aborting the load.
func loadPkgGraph(ctx context.Context, root string) (*pkgGraph, error) {
	ctx, cancel := context.WithTimeout(ctx, goListTimeout)
	defer cancel()
	args := []string{"list", "-e", "-json=" + goListFields}
	if tags := tierBuildTags(); len(tags) > 0 {
		args = append(args, "-tags="+strings.Join(tags, ","))
	}
	cmd := exec.CommandContext(ctx, "go", append(args, "./...")...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./...: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parsePkgGraph(out, root)
}

// parsePkgGraph reads the JSON stream `go list -json` writes for the module
// rooted at root.
func parsePkgGraph(out []byte, root string) (*pkgGraph, error) {
	g := newPkgGraph()
	prefix := strings.TrimSuffix(root, "/") + "/"
	dec := json.NewDecoder(bytes.NewReader(out))
	for {
		var p goListPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("parse go list output: %w", err)
		}
		dir := strings.TrimPrefix(p.Dir, prefix)
		if !inModule(p.ImportPath) || dir == p.Dir {
			// A package outside the module, the module root package, or
			// a directory outside root.
			continue
		}
		var embeds []string
		for _, f := range append(append(append([]string{}, p.EmbedFiles...), p.TestEmbedFiles...), p.XTestEmbedFiles...) {
			embeds = append(embeds, path.Join(dir, f))
		}
		testImports := append(append([]string{}, p.TestImports...), p.XTestImports...)
		g.addPackage(p.ImportPath, dir, p.Imports, testImports, embeds)
	}
	return g, nil
}

// selectOpts carries the operator-tunable knobs of package-level selection.
type selectOpts struct {
	// rdepsDepth is the dependent depth; see defaultRdepsDepth.
	rdepsDepth int
	// all runs every package in the unit tier regardless of the diff.
	all bool
}

// packageScopedTiers are the tiers whose test packages a selection names
// one by one. Every other tier is one Go package, or a few, and runs whole
// when anything selects it, as decision 3 of change 0116 settles for the
// integration and chaos tiers until they split by area.
var packageScopedTiers = map[string]bool{tierUnit: true, tierComponent: true, tierContract: true}

// byNameTiers are the tiers whose executors run a selection's test
// functions by name (`go test -run`). A test function a specification edit
// selects in any other tier runs its whole tier.
var byNameTiers = map[string]bool{tierUnit: true, tierComponent: true, tierContract: true, tierIntegration: true}

// tierScope is what a selection runs in one tier: the whole tier, or the
// package directories it runs whole and the test functions it runs by name
// in other directories.
type tierScope struct {
	whole bool
	pkgs  map[string]bool
	funcs map[string]map[string]bool
}

// changeSelection is the full result of resolving a diff: the scope of
// each tier it reaches, and what the operator needs to know about paths the
// selection could not place.
type changeSelection struct {
	// scopes maps each tier the diff reaches to its scope; static is
	// always in it.
	scopes map[string]*tierScope
	// unowned lists changed paths that no Go package owns and no row of
	// the non-Go input list matches.
	unowned []string
	// global lists changed paths in globalChangePaths.
	global []string
	// extra carries notes from the specification-edit selection and the
	// non-Go input list.
	extra []string
}

// newChangeSelection returns a selection holding the static tier, which
// runs whole on every invocation.
func newChangeSelection() *changeSelection {
	return &changeSelection{scopes: map[string]*tierScope{tierStatic: {whole: true}}}
}

func (c *changeSelection) scope(tier string) *tierScope {
	s, ok := c.scopes[tier]
	if !ok {
		s = &tierScope{pkgs: map[string]bool{}, funcs: map[string]map[string]bool{}}
		c.scopes[tier] = s
	}
	return s
}

// addWhole selects a whole tier.
func (c *changeSelection) addWhole(tier string) { c.scope(tier).whole = true }

// addPkg selects one test package of a tier, or the whole tier when the
// tier is not package-scoped.
func (c *changeSelection) addPkg(tier, dir string) {
	if !packageScopedTiers[tier] {
		c.addWhole(tier)
		return
	}
	c.scope(tier).pkgs[dir] = true
}

// addFunc selects one test function by name, or its whole tier when the
// tier's executor cannot run a function by name.
func (c *changeSelection) addFunc(tier, dir, name string) {
	if !byNameTiers[tier] {
		c.addWhole(tier)
		return
	}
	s := c.scope(tier)
	if s.funcs[dir] == nil {
		s.funcs[dir] = map[string]bool{}
	}
	s.funcs[dir][name] = true
}

// addPackageDir selects the tests of a package directory in the tier that
// runs them: the tier whose directory holds it, or the unit tier for a
// package beside its code. A package outside the tier directories that
// also carries a tier's tagged tests (tierExtraTrees) is selected in that
// tier too. The static tier's packages run on every invocation, so they
// are never added.
func (c *changeSelection) addPackageDir(dir string) {
	if runsInStaticTier(dir) {
		return
	}
	if t := tierOfDir(dir); t != "" {
		c.addPkg(t, dir)
		return
	}
	c.addPkg(tierUnit, dir)
	for _, t := range allTiers() {
		for _, tree := range tierExtraTrees(t) {
			if dir == tree || strings.HasPrefix(dir, tree+"/") {
				c.addPkg(t, dir)
			}
		}
	}
}

// addTarget selects one target of a non-Go input row. A tier name selects
// the whole tier. A package directory is treated as changed, so it returns
// the package's import path for the caller to walk with the other changed
// packages: a test package has no dependents, and a helper package that
// reads the input on behalf of its importers, such as the envtest helper
// that loads the chart's CRDs, selects the tests that import it. Without a
// graph, or for a directory the graph does not hold, the package is
// selected alone.
func (c *changeSelection) addTarget(g *pkgGraph, target string) string {
	if contains(allTiers(), target) {
		c.addWhole(target)
		return ""
	}
	dir := strings.TrimSuffix(strings.TrimPrefix(target, "./"), "/...")
	if g != nil {
		if pkg, ok := g.dirToPkg[dir]; ok {
			return pkg
		}
	}
	c.addPackageDir(dir)
	return ""
}

// notes renders the selection's operator-facing warnings.
func (c *changeSelection) notes(opts selectOpts) []string {
	out := append([]string{}, c.extra...)
	if len(c.unowned) > 0 {
		out = append(out, fmt.Sprintf(
			"%d changed path(s) that no Go package owns and no row of %s matches select the static tier only: %s",
			len(c.unowned), nonGoInputsFile, strings.Join(c.unowned, ", "),
		))
	}
	if len(c.global) > 0 && !opts.all {
		out = append(out, fmt.Sprintf(
			"module-wide build file(s) changed (%s); the static tier type-checks every package, and `--all` runs every package's unit tests",
			strings.Join(c.global, ", "),
		))
	}
	return out
}

// specEdits is what the specification edits of a diff select (CS-5).
type specEdits struct {
	// tests are the test functions that run by name.
	tests []annotation.TestFunc
	// changedDirs are the package directories whose code cites a changed
	// identifier; the selection treats them as changed.
	changedDirs []string
	// unitDirs are the package directories whose doc.go names a page the
	// diff edits outside its identifiers; their unit tests run whole.
	unitDirs []string
	// notes are the operator-facing notes the edits raised.
	notes []string
}

// selectChanged resolves changed paths into a per-tier scope. It is pure
// over its inputs so the selection rules are testable without git or the
// go tool.
//
// For each changed path, the owning packages (pkgGraph.ownersOf) are
// changed, and every row of the non-Go input list whose pattern matches the
// path adds its targets: a tier name selects the whole tier, and a package
// directory is changed too (see addTarget). The packages whose code cites an identifier a
// specification edit changes are changed too. The changed packages and
// their dependents to opts.rdepsDepth hops are then selected, each in the
// tier that runs it, and the test functions the specification edits name
// run by name. A path that no package owns and no row matches is reported,
// except the records under changes/, proposals/, and issues/ and the
// root-level documents, which select nothing further by design.
func selectChanged(paths []string, g *pkgGraph, inputs *nonGoInputs, spec specEdits, opts selectOpts) *changeSelection {
	sel := newChangeSelection()
	sel.extra = append(sel.extra, spec.notes...)
	var roots []string
	for _, p := range paths {
		if globalChangePaths[p] {
			sel.global = append(sel.global, p)
			continue
		}
		var owners []string
		if g != nil {
			owners = g.ownersOf(p)
		}
		targets := inputs.targetsFor(p)
		for _, t := range targets {
			if pkg := sel.addTarget(g, t); pkg != "" {
				roots = append(roots, pkg)
			}
		}
		roots = append(roots, owners...)
		if len(owners) == 0 && len(targets) == 0 && !selectsNothingByDesign(p) {
			sel.unowned = append(sel.unowned, p)
		}
	}
	if g != nil {
		for _, dir := range spec.changedDirs {
			if pkg, ok := g.dirToPkg[dir]; ok {
				roots = append(roots, pkg)
			}
		}
		for _, pkg := range append(roots, g.dependents(roots, opts.rdepsDepth)...) {
			if dir, ok := g.pkgToDir[pkg]; ok {
				sel.addPackageDir(dir)
			}
		}
	}
	for _, dir := range spec.unitDirs {
		if !runsInStaticTier(dir) {
			sel.addPkg(tierUnit, dir)
		}
	}
	for _, t := range spec.tests {
		if !runsInStaticTier(t.Dir) {
			sel.addFunc(testTier(t), t.Dir, t.Name)
		}
	}
	if opts.all {
		sel.addWhole(tierUnit)
	}
	return sel
}

// selectsNothingByDesign reports whether a changed path is a record that
// no test reads: the change records, the proposals, the issues, and the
// root-level documents. A record a test does read has a row in the non-Go
// input list, which is matched before this is asked.
func selectsNothingByDesign(p string) bool {
	for _, dir := range []string{"changes/", "proposals/", "issues/"} {
		if strings.HasPrefix(p, dir) {
			return true
		}
	}
	return !strings.Contains(p, "/") && strings.HasSuffix(p, ".md")
}

// selectPackages resolves `--pkg` through the import graph: every package
// at or under each named directory is changed, and the selection is that of
// a diff changing those packages. It returns the selection and the names
// that matched no package.
func selectPackages(names []string, g *pkgGraph, opts selectOpts) (*changeSelection, []string) {
	sel := newChangeSelection()
	var roots, unknown []string
	for _, name := range names {
		dir := strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(name, "./"), "/..."), "/")
		matched := false
		for d, pkg := range g.dirToPkg {
			if d == dir || strings.HasPrefix(d, dir+"/") {
				roots = append(roots, pkg)
				matched = true
			}
		}
		if !matched {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(roots)
	for _, pkg := range append(roots, g.dependents(roots, opts.rdepsDepth)...) {
		sel.addPackageDir(g.pkgToDir[pkg])
	}
	if opts.all {
		sel.addWhole(tierUnit)
	}
	return sel, unknown
}

// plan renders the selection as tier plans in canonical order. A whole
// tier has nil pkgs. A scoped tier has non-nil pkgs, the `./dir` patterns
// it runs whole, and funcs, the test names it runs by name in each other
// directory.
func (c *changeSelection) plan() []tierPlan {
	tiers := map[string]bool{}
	for t := range c.scopes {
		tiers[t] = true
	}
	plan := planFromTierSet(tiers)
	for i := range plan {
		s := c.scopes[plan[i].name]
		if s.whole {
			continue
		}
		plan[i].pkgs = []string{}
		for dir := range s.pkgs {
			plan[i].pkgs = append(plan[i].pkgs, "./"+dir)
		}
		plan[i].pkgs = collapsePatterns(stringSet(plan[i].pkgs))
		for dir, names := range s.funcs {
			if s.pkgs[dir] {
				continue
			}
			if plan[i].funcs == nil {
				plan[i].funcs = map[string][]string{}
			}
			plan[i].funcs["./"+dir] = sortedKeys(names)
		}
	}
	return plan
}

// stringSet returns the set of values.
func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

// sortedKeys returns the keys of a set in order.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// staticTierTree is the package tree the static tier runs with `go test`
// on every invocation. Its packages import many others, so they show up as
// dependents of a changed package; selecting them again would run the same
// tests twice in one invocation.
const staticTierTree = "tests/static"

func runsInStaticTier(dir string) bool {
	return dir == staticTierTree || strings.HasPrefix(dir, staticTierTree+"/")
}

// collapsePatterns sorts the patterns and drops any pattern a recursive
// `/...` pattern in the set already covers, so the printed scope is short and
// each package appears once.
func collapsePatterns(set map[string]bool) []string {
	var recursive []string
	for p := range set {
		if strings.HasSuffix(p, "/...") {
			recursive = append(recursive, strings.TrimSuffix(p, "/..."))
		}
	}
	out := []string{}
	for p := range set {
		base := strings.TrimSuffix(p, "/...")
		covered := false
		for _, r := range recursive {
			if (base == r && p != r+"/...") || strings.HasPrefix(base, r+"/") {
				covered = true
				break
			}
		}
		if !covered {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}
