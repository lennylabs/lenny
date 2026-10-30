// SPDX-License-Identifier: MIT

package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"
)

// This file holds the package-level half of `--changed` selection: which Go
// packages the unit tier tests for a given diff. The tier-level half (which
// tiers a changed path reaches) stays in cmd_run_resolve.go and reads the
// change graph.
//
// Before the package-level half existed, `--changed` chose tiers only, and
// the unit tier always ran `go test -race ./...` over the whole module. A diff
// touching one leaf package therefore ran every package's tests, which took
// over an hour on the full tree. The unit tier now runs the packages the diff
// touches, the unit targets the change graph lists for the changed paths, and
// the in-module packages that import a touched package, up to a bounded depth.

// moduleImportPath is the import-path prefix of this repository's module.
const moduleImportPath = "github.com/lennylabs/lenny"

// defaultRdepsDepth is how many hops of in-module importers `--changed` adds
// to the touched packages. One hop runs the direct consumers of a changed
// package, which is where a changed contract surfaces first; the static tier
// type-checks every package in the module (`go vet ./...`), so a signature
// break further out still fails tier 0. Operators widen it with
// `--rdeps-depth` (-1 for the full transitive closure) or run everything with
// `--all`.
const defaultRdepsDepth = 1

// goListTimeout bounds the `go list` call that loads the package graph.
const goListTimeout = 2 * time.Minute

// globalChangePaths are repo-relative files whose change can alter the build
// of every package, so no package-level scope describes their blast radius.
// A diff that touches one of them is reported, and `--all` runs every
// package; without `--all` the unit scope stays at what the rest of the diff
// selects, and tier 0's module-wide `go vet ./...` compiles every package
// against the new module graph.
var globalChangePaths = map[string]bool{
	"go.mod":  true,
	"go.sum":  true,
	"go.work": true,
}

// pkgGraph is the in-module Go package graph the selector walks: where each
// package lives and which in-module packages import it, counting test
// imports, because a package whose tests import a changed package is a
// consumer whose tests can break.
type pkgGraph struct {
	// dirToPkg maps a repo-relative package directory to its import path.
	dirToPkg map[string]string
	// pkgToDir maps an import path back to its repo-relative directory.
	pkgToDir map[string]string
	// importers maps an import path to the in-module packages importing it.
	importers map[string][]string
}

// newPkgGraph returns an empty graph ready for addPackage.
func newPkgGraph() *pkgGraph {
	return &pkgGraph{
		dirToPkg:  map[string]string{},
		pkgToDir:  map[string]string{},
		importers: map[string][]string{},
	}
}

// addPackage records one package, its repo-relative directory, and the
// import paths it (or its tests) import. Imports outside the module are
// ignored.
func (g *pkgGraph) addPackage(importPath, dir string, imports []string) {
	g.dirToPkg[dir] = importPath
	g.pkgToDir[importPath] = dir
	seen := map[string]bool{}
	for _, imp := range imports {
		if imp == importPath || seen[imp] || !inModule(imp) {
			continue
		}
		seen[imp] = true
		g.importers[imp] = append(g.importers[imp], importPath)
	}
}

func inModule(importPath string) bool {
	return importPath == moduleImportPath || strings.HasPrefix(importPath, moduleImportPath+"/")
}

// packageForPath returns the import path of the package that owns a changed
// repo-relative path: the package in the file's directory, or the nearest
// ancestor directory that is a package, so a changed testdata fixture or
// embedded asset selects the package that reads it. The module root itself
// is never returned, because a root-level file (a README, a Makefile) is not
// owned by a root package. It returns "" when no package owns the path.
func (g *pkgGraph) packageForPath(p string) string {
	for dir := path.Dir(p); dir != "." && dir != "/" && dir != ""; dir = path.Dir(dir) {
		if pkg, ok := g.dirToPkg[dir]; ok {
			return pkg
		}
	}
	return ""
}

// reverseDeps returns the in-module packages that import any of roots,
// walking at most depth hops; a negative depth walks the full transitive
// closure. The roots themselves are not in the result.
func (g *pkgGraph) reverseDeps(roots []string, depth int) []string {
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
				if seen[imp] {
					continue
				}
				seen[imp] = true
				out = append(out, imp)
				next = append(next, imp)
			}
		}
		frontier = next
	}
	sort.Strings(out)
	return out
}

// loadPkgGraph builds the package graph for the module rooted at root with
// one `go list` call. -e keeps a package that fails to load (a broken file
// in the diff, or a directory whose files are all build-tagged) from
// aborting the load; such a package is simply absent from the graph.
func loadPkgGraph(ctx context.Context, root string) (*pkgGraph, error) {
	ctx, cancel := context.WithTimeout(ctx, goListTimeout)
	defer cancel()
	const sep = "\x1f"
	format := "{{.ImportPath}}" + sep + "{{.Dir}}" + sep +
		"{{join .Imports \",\"}},{{join .TestImports \",\"}},{{join .XTestImports \",\"}}"
	cmd := exec.CommandContext(ctx, "go", "list", "-e", "-f", format, "./...")
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list ./...: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	g := newPkgGraph()
	prefix := strings.TrimSuffix(root, "/") + "/"
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Split(line, sep)
		if len(fields) != 3 || !inModule(fields[0]) {
			continue
		}
		dir := strings.TrimPrefix(fields[1], prefix)
		if dir == fields[1] {
			// The module root package, or a directory outside root.
			continue
		}
		g.addPackage(fields[0], dir, strings.Split(fields[2], ","))
	}
	return g, nil
}

// selectOpts carries the operator-tunable knobs of package-level selection.
type selectOpts struct {
	// rdepsDepth is the importer depth; see defaultRdepsDepth.
	rdepsDepth int
	// all runs every package in the unit tier regardless of the diff.
	all bool
}

// changeSelection is the full result of resolving a diff: the tiers it
// reaches, the unit tier's package scope, and what the operator needs to
// know about paths the selection could not place.
type changeSelection struct {
	// tiers is the set of tier names the diff reaches; static is always in it.
	tiers map[string]bool
	// unitPkgs is the unit tier's go-test patterns (`./pkg/foo/...` or
	// `./pkg/foo`), sorted. Nil means every package (`./...`), which
	// happens only under --all.
	unitPkgs []string
	// unmapped lists changed paths no change-graph key covers.
	unmapped []string
	// global lists changed paths in globalChangePaths.
	global []string
}

// notes renders the selection's operator-facing warnings.
func (c changeSelection) notes(opts selectOpts) []string {
	var out []string
	if len(c.unmapped) > 0 {
		out = append(out, fmt.Sprintf(
			"%d changed path(s) have no tests/change-graph.json key; their owning packages run in the unit tier, and a path no package owns runs tier 0 only: %s",
			len(c.unmapped), strings.Join(c.unmapped, ", "),
		))
	}
	if len(c.global) > 0 && !opts.all {
		out = append(out, fmt.Sprintf(
			"module-wide build file(s) changed (%s); tier 0 type-checks every package, and `--all` runs every package's unit tests",
			strings.Join(c.global, ", "),
		))
	}
	return out
}

// selectChanged resolves changed paths into tiers and a unit-tier package
// scope. It is pure over its inputs so the selection rules are testable
// without git or the go tool.
//
// For each changed path:
//   - A path a change-graph key covers reaches the key's tiers. When those
//     include unit, the key's unit targets, the path's owning package, and
//     that package's importers (to opts.rdepsDepth hops) join the unit scope.
//   - A path no key covers is reported as unmapped. When a package owns it,
//     the unit tier is selected for that package and its importers, so an
//     unmapped change still runs the tests nearest to it rather than nothing.
//   - A module-wide build file is reported as global.
func selectChanged(paths []string, globs map[string]map[string][]string, g *pkgGraph, opts selectOpts) changeSelection {
	sel := changeSelection{tiers: map[string]bool{tierStatic: true}}
	patterns := map[string]bool{}
	var touched []string
	for _, p := range paths {
		if globalChangePaths[p] {
			sel.global = append(sel.global, p)
		}
		tiers := tiersForChangedPathIn(globs, p)
		for _, t := range tiers {
			sel.tiers[t] = true
		}
		owner := ""
		if g != nil {
			owner = g.packageForPath(p)
		}
		mapped := len(tiers) > 0
		if !mapped && !globalChangePaths[p] {
			sel.unmapped = append(sel.unmapped, p)
		}
		wantsUnit := contains(tiers, tierUnit) || (!mapped && owner != "")
		if !wantsUnit {
			continue
		}
		sel.tiers[tierUnit] = true
		for _, target := range unitTargetsFor(globs, p) {
			patterns[goTestPattern(target)] = true
		}
		if owner != "" {
			touched = append(touched, owner)
		}
	}
	if g != nil {
		for _, pkg := range append(touched, g.reverseDeps(touched, opts.rdepsDepth)...) {
			if dir, ok := g.pkgToDir[pkg]; ok && !runsInStaticTier(dir) {
				patterns["./"+dir] = true
			}
		}
	}
	if opts.all {
		sel.tiers[tierUnit] = true
		return sel
	}
	if sel.tiers[tierUnit] {
		sel.unitPkgs = collapsePatterns(patterns)
	}
	return sel
}

// staticTierTree is the package tree the static tier runs with `go test`
// on every invocation. Its packages import many others, so they show up as
// importers of a touched package; adding them to the unit scope would run
// the same tests twice in one invocation.
const staticTierTree = "tests/tier0_static"

func runsInStaticTier(dir string) bool {
	return dir == staticTierTree || strings.HasPrefix(dir, staticTierTree+"/")
}

// unitTargetsFor returns the unit-tier targets of every change-graph key
// covering p.
func unitTargetsFor(globs map[string]map[string][]string, p string) []string {
	var out []string
	for key, perTier := range globs {
		if strings.HasPrefix(p, changeGraphGlobPrefix(key)) {
			out = append(out, perTier[tierUnit]...)
		}
	}
	return out
}

// goTestPattern turns a change-graph target (`pkg/foo/...`, `pkg/foo/x_test.go`,
// `pkg/foo`) into a `go test` package pattern rooted at the module.
func goTestPattern(target string) string {
	target = strings.TrimPrefix(target, "./")
	if strings.HasSuffix(target, ".go") {
		target = path.Dir(target)
	}
	return "./" + target
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
