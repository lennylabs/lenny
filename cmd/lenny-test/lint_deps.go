// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// The coverage check of the static tier's golangci-lint check. Both
// golangci-lint runs read the packages the pattern ./... matches, and the
// go command leaves out of that pattern every directory whose name starts
// with `_` or `.` and every directory named testdata, although a package
// there still compiles into a binary that imports it. The check lists the
// module's packages that the main packages under cmd/ import, directly or
// not, and fails on any of them that ./... does not match. The main
// packages include those in a directory under cmd/ that starts with `_`
// or `.`, which ./cmd/... skips but a build that names the directory
// compiles (owner's answer to GT-10, 2026-11-20). A file behind an
// operating system or architecture constraint imports its packages only in
// the build it names, so the check lists the imports once for the lint's
// own build (lintBuildTarget) and once for every other build target the
// release definition builds (owner's answer to GT-17, 2026-11-20). The
// lint itself still reads only its own build. The check is contributor
// tooling, so this file carries no specification annotation.

// releaseDefinition is the workflow that builds the release's binaries and
// images. The check reads its build targets from it, so a target the
// release adds is checked without an edit here.
const releaseDefinition = ".github/workflows/release.yml"

// lintDepsResult is the outcome of the coverage check: the build targets
// it listed, in the order it listed them, and each package outside ./...
// with the targets whose build imports it.
type lintDepsResult struct {
	targets []lintTarget
	outside []string
}

// lintProductMains is the pattern of the product binaries' packages.
const lintProductMains = "./cmd/..."

// productPackagesOutsideLint returns, sorted, the import paths of the
// module's packages that a main package under cmd/ imports, directly or
// not, and that ./... does not match. goList runs `go list` in root with
// the given arguments, in one build target, and returns its standard
// output. Every listing sets the lint's build tags, so the packages are
// the ones that target's build compiles with those tags.
// Every target lists every main package under cmd/, although the release
// builds some of them only for Linux, so the listings of the main packages
// and of what they import run with -e: a package whose files the target's
// build constraints all exclude is left out rather than failing the
// listing, because that build does not compile it (owner's answer to
// GT-19, 2026-11-20). Any other error a listed package carries, such as an
// import that no module provides or a file whose package clause or import
// block does not parse, fails the check (lintDepsList).
// A go list failure, and a tree with no main package under cmd/, are
// errors, so the check never passes on an empty listing.
func productPackagesOutsideLint(ctx context.Context, root string, goList func(ctx context.Context, root string, args ...string) (string, error)) ([]string, error) {
	tags := "-tags=" + strings.Join(lintBuildTags(), ",")
	module, err := goList(ctx, root, "-m", "-f", "{{.Path}}")
	if err != nil {
		return nil, fmt.Errorf("read the module path: %w", err)
	}
	module = strings.TrimSpace(module)
	if module == "" || strings.Contains(module, "\n") {
		return nil, fmt.Errorf("go list -m names %q rather than one main module", module)
	}
	hidden, err := hiddenCmdDirs(root)
	if err != nil {
		return nil, err
	}
	listed, err := lintDepsList(ctx, root, goList, append([]string{tags, lintProductMains}, hidden...)...)
	if err != nil {
		return nil, fmt.Errorf("list the main packages under %s: %w", lintProductMains, err)
	}
	var mains []string
	for _, p := range listed {
		if p.Name == "main" {
			mains = append(mains, p.ImportPath)
		}
	}
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main package under %s", lintProductMains)
	}
	deps, err := lintDepsList(ctx, root, goList, append([]string{tags, "-deps"}, mains...)...)
	if err != nil {
		return nil, fmt.Errorf("list the packages the binaries under %s import: %w", lintProductMains, err)
	}
	linted, err := goList(ctx, root, "-e", tags, "-f", "{{.ImportPath}}", "./...")
	if err != nil {
		return nil, fmt.Errorf("list the packages ./... matches: %w", err)
	}
	covered := map[string]bool{}
	for _, p := range strings.Fields(linted) {
		covered[p] = true
	}
	var outside []string
	for _, p := range deps {
		if p.Module == nil || p.Module.Path != module || covered[p.ImportPath] {
			continue
		}
		outside = append(outside, p.ImportPath)
	}
	sort.Strings(outside)
	return outside, nil
}

// lintDepsPackage is the part of a `go list -json` record the coverage
// check reads.
type lintDepsPackage struct {
	ImportPath string
	Name       string
	Module     *struct{ Path string }
	Error      *lintDepsError
	DepsErrors []*lintDepsError
}

// lintDepsError is the part of a go list package error the check reads.
type lintDepsError struct {
	ImportStack []string
	Err         string
}

// goListExcluded opens the error go list records for a package whose files
// the build constraints of the listing's build all exclude.
const goListExcluded = "build constraints exclude all Go files in "

// excludedByConstraints reports whether e is the error of a package whose
// files the build constraints of the listing's build all exclude.
func excludedByConstraints(e *lintDepsError) bool {
	return e != nil && strings.HasPrefix(e.Err, goListExcluded) && !strings.Contains(e.Err, "\n")
}

// lintDepsList runs `go list -e -json` with args through goList and
// returns the packages it lists, leaving out each package whose files the
// build constraints all exclude. With -e the go command lists a package it
// cannot load, with the error in the package's Error field and in each
// importer's DepsErrors, rather than failing, so every error in either
// field other than a build-constraint exclusion fails the listing. A
// package outside ./... therefore cannot drop out of the listing behind
// -e: it is listed, or the listing fails, unless the build does not
// compile it.
func lintDepsList(ctx context.Context, root string, goList func(ctx context.Context, root string, args ...string) (string, error), args ...string) ([]lintDepsPackage, error) {
	out, err := goList(ctx, root, append([]string{"-e", "-json=ImportPath,Name,Module,Error,DepsErrors"}, args...)...)
	if err != nil {
		return nil, err
	}
	var pkgs []lintDepsPackage
	var problems []string
	seen := map[string]bool{}
	note := func(pkg string, e *lintDepsError) {
		if e == nil || excludedByConstraints(e) {
			return
		}
		msg := fmt.Sprintf("%s: %s", pkg, strings.ReplaceAll(e.Err, "\n", " "))
		if len(e.ImportStack) > 0 && !slices.Equal(e.ImportStack, []string{pkg}) {
			msg += " (import stack " + strings.Join(e.ImportStack, " -> ") + ")"
		}
		if !seen[msg] {
			seen[msg] = true
			problems = append(problems, msg)
		}
	}
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p lintDepsPackage
		if err := dec.Decode(&p); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("read the go list output: %w", err)
		}
		if p.ImportPath == "" {
			return nil, errors.New("read the go list output: a package record names no import path")
		}
		note(p.ImportPath, p.Error)
		for _, e := range p.DepsErrors {
			note(p.ImportPath, e)
		}
		if excludedByConstraints(p.Error) {
			continue
		}
		pkgs = append(pkgs, p)
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("go list reports packages it cannot load:\n  %s", strings.Join(problems, "\n  "))
	}
	return pkgs, nil
}

// hiddenCmdDirs returns, as ./-relative patterns, each directory under
// cmd/ that holds a Go file and that lintProductMains does not match
// because a path element starts with `_` or `.`. A main package there
// compiles when a build names its directory, so the listing names it too.
// Directories named testdata hold fixtures and are left out. A tree with
// no cmd/ directory returns none.
func hiddenCmdDirs(root string) ([]string, error) {
	var out []string
	base := filepath.Join(root, "cmd")
	err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == base && errors.Is(err, fs.ErrNotExist) {
				return filepath.SkipDir
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(p))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		for _, el := range strings.Split(rel, "/") {
			if strings.HasPrefix(el, "_") || strings.HasPrefix(el, ".") {
				if pattern := "./" + rel; len(out) == 0 || out[len(out)-1] != pattern {
					out = append(out, pattern)
				}
				break
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", base, err)
	}
	return out, nil
}

// execGoList runs `go list` in root, in the lint's build target, and
// returns its standard output; the error carries its standard error.
func execGoList(ctx context.Context, root string, args ...string) (string, error) {
	return execGoListFor(ctx, root, lintBuildTarget, args...)
}

// execGoListFor runs `go list` in root, in the build target t with the
// settings every build the lint reads shares (lintTargetEnvVars), and
// returns its standard output; the error carries its standard error.
func execGoListFor(ctx context.Context, root string, t lintTarget, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "go", append([]string{"list"}, args...)...)
	cmd.Dir = root
	cmd.Env = targetEnv(os.Environ(), t)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list %s (%s): %w: %s", strings.Join(args, " "), t, err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// releasePackagesOutsideLint runs the coverage check for the checkout at
// root in every target lintDepsTargets returns, with the go command.
func releasePackagesOutsideLint(ctx context.Context, root string) (lintDepsResult, error) {
	targets, err := lintDepsTargets(root)
	if err != nil {
		return lintDepsResult{}, err
	}
	return packagesOutsideLintPerTarget(ctx, root, targets, execGoListFor)
}

// packagesOutsideLintPerTarget runs productPackagesOutsideLint once per
// target, with goList bound to that target, and returns each package
// outside ./... once, sorted, with the targets whose build imports it. A
// listing that fails in any target is an error naming the target.
func packagesOutsideLintPerTarget(ctx context.Context, root string, targets []lintTarget, goList func(ctx context.Context, root string, t lintTarget, args ...string) (string, error)) (lintDepsResult, error) {
	res := lintDepsResult{targets: targets}
	byPkg := map[string][]string{}
	for _, t := range targets {
		outside, err := productPackagesOutsideLint(ctx, root, func(ctx context.Context, root string, args ...string) (string, error) {
			return goList(ctx, root, t, args...)
		})
		if err != nil {
			return res, fmt.Errorf("in the build %s: %w", t, err)
		}
		for _, p := range outside {
			byPkg[p] = append(byPkg[p], t.String())
		}
	}
	for p, ts := range byPkg {
		res.outside = append(res.outside, fmt.Sprintf("%s (built for %s)", p, strings.Join(ts, ", ")))
	}
	sort.Strings(res.outside)
	return res, nil
}

// lintDepsTargets returns the build targets the coverage check lists: the
// lint's own build first, then every other target the release definition
// builds, sorted. A release definition the check cannot read, or one that
// names no target, is an error, so the check never passes on fewer
// targets than the release builds.
func lintDepsTargets(root string) ([]lintTarget, error) {
	body, err := os.ReadFile(filepath.Join(root, releaseDefinition))
	if err != nil {
		return nil, fmt.Errorf("read the release build targets: %w", err)
	}
	release, err := releaseTargets(body)
	if err != nil {
		return nil, fmt.Errorf("read the release build targets from %s: %w", releaseDefinition, err)
	}
	out := []lintTarget{lintBuildTarget}
	for _, t := range release {
		if t != lintBuildTarget {
			out = append(out, t)
		}
	}
	return out, nil
}

// releaseWorkflow is the part of a GitHub Actions workflow that names
// build targets: each job's matrix, environment, and steps.
type releaseWorkflow struct {
	Jobs map[string]struct {
		Strategy struct {
			Matrix yaml.Node `yaml:"matrix"`
		} `yaml:"strategy"`
		Env   map[string]yaml.Node `yaml:"env"`
		Steps []struct {
			With map[string]yaml.Node `yaml:"with"`
			Env  map[string]yaml.Node `yaml:"env"`
			Run  string               `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// releaseTargetEnv is the only value the release workflow may give GOOS
// and GOARCH in an env block: the matrix value the reading takes the
// target from.
var releaseTargetEnv = map[string]string{
	"GOOS":   "${{ matrix.goos }}",
	"GOARCH": "${{ matrix.goarch }}",
}

// releaseRunTarget finds a GOOS or GOARCH assignment in a step's script.
var releaseRunTarget = regexp.MustCompile(`\bGO(OS|ARCH)=`)

// releaseTargets returns, sorted and without duplicates, the build targets
// the release workflow body names in either of the two forms it uses:
//
//   - A job matrix whose include entries carry goos and goarch, or whose
//     top level carries goos and goarch lists, which give every pair. The
//     cross-compiled CLI archives use this form.
//   - A step input named platforms that lists os/arch pairs separated by
//     commas or new lines, as docker/build-push-action reads it. The images
//     use this form. A variant after a second slash, such as v7 in
//     linux/arm/v7, is ignored, so the listing reads the go command's
//     default for that architecture's GOARM, GOAMD64, or GOARM64 setting.
//
// The reading refuses the forms that would set a target it does not read:
// a matrix that is not a mapping, such as an expression the workflow
// evaluates; a matrix include entry that names only one of goos and
// goarch; a goos or goarch value that is not a plain name; a platform that
// is not an os/arch pair; a job or step env block that sets GOOS or GOARCH
// to anything other than the matrix's goos or goarch; and a step script
// that assigns GOOS or GOARCH. A workflow that names no target is an
// error too. A form the reading does not look at, such as a matrix entry
// whose keys have other names that a build step reads through another
// variable, or a reusable workflow a job calls, can still name a target
// the reading misses.
func releaseTargets(body []byte) ([]lintTarget, error) {
	var wf releaseWorkflow
	if err := yaml.Unmarshal(body, &wf); err != nil {
		return nil, fmt.Errorf("parse the workflow: %w", err)
	}
	seen := map[lintTarget]bool{}
	names := make([]string, 0, len(wf.Jobs))
	for name := range wf.Jobs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		job := wf.Jobs[name]
		targets, err := matrixTargets(job.Strategy.Matrix)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", name, err)
		}
		for _, t := range targets {
			seen[t] = true
		}
		if err := checkTargetEnv(job.Env); err != nil {
			return nil, fmt.Errorf("job %s: %w", name, err)
		}
		for i, step := range job.Steps {
			if err := checkTargetEnv(step.Env); err != nil {
				return nil, fmt.Errorf("job %s, step %d: %w", name, i+1, err)
			}
			if releaseRunTarget.MatchString(step.Run) {
				return nil, fmt.Errorf("job %s, step %d: the script assigns GOOS or GOARCH, which the reading of build targets does not follow; set them in the step's env from the matrix", name, i+1)
			}
			node, ok := step.With["platforms"]
			if !ok {
				continue
			}
			if node.Kind != yaml.ScalarNode {
				return nil, fmt.Errorf("job %s, step %d: platforms is not a string", name, i+1)
			}
			for _, p := range strings.FieldsFunc(node.Value, func(r rune) bool { return r == ',' || r == '\n' }) {
				t, err := parsePlatform(strings.TrimSpace(p))
				if err != nil {
					return nil, fmt.Errorf("job %s, step %d: %w", name, i+1, err)
				}
				seen[t] = true
			}
		}
	}
	if len(seen) == 0 {
		return nil, errors.New("the workflow names no build target (a matrix with goos and goarch, or a platforms input)")
	}
	out := make([]lintTarget, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// checkTargetEnv refuses an env block that sets GOOS or GOARCH to anything
// other than the matrix value releaseTargetEnv names.
func checkTargetEnv(env map[string]yaml.Node) error {
	for name, want := range releaseTargetEnv {
		v, ok := env[name]
		if !ok {
			continue
		}
		if v.Kind != yaml.ScalarNode || strings.TrimSpace(v.Value) != want {
			return fmt.Errorf("env sets %s to %q, and the reading of build targets follows only %s", name, v.Value, want)
		}
	}
	return nil
}

// matrixTargets returns the targets a job matrix names: one per include
// entry that carries goos and goarch, and every pair of the top-level goos
// and goarch lists. A job with no matrix names none. Each value is read as
// the scalar text the workflow holds, so an unquoted number such as 386 is
// a name like any other.
func matrixTargets(m yaml.Node) ([]lintTarget, error) {
	if m.Kind == 0 {
		return nil, nil
	}
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("the matrix is not a mapping, so its build targets cannot be read")
	}
	var matrix map[string]yaml.Node
	if err := m.Decode(&matrix); err != nil {
		return nil, fmt.Errorf("read the matrix: %w", err)
	}
	var out []lintTarget
	if include, ok := matrix["include"]; ok {
		if include.Kind != yaml.SequenceNode {
			return nil, errors.New("the matrix include is not a list")
		}
		for i, e := range include.Content {
			if e.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("matrix include entry %d is not a mapping", i+1)
			}
			var entry map[string]yaml.Node
			if err := e.Decode(&entry); err != nil {
				return nil, fmt.Errorf("matrix include entry %d: %w", i+1, err)
			}
			goos, hasOS := entry["goos"]
			goarch, hasArch := entry["goarch"]
			if !hasOS && !hasArch {
				continue
			}
			if !hasOS || !hasArch {
				return nil, fmt.Errorf("matrix include entry %d names only one of goos and goarch", i+1)
			}
			t, err := targetOf(goos, goarch)
			if err != nil {
				return nil, fmt.Errorf("matrix include entry %d: %w", i+1, err)
			}
			out = append(out, t)
		}
	}
	goosList, hasOS := matrix["goos"]
	goarchList, hasArch := matrix["goarch"]
	if !hasOS && !hasArch {
		return out, nil
	}
	if !hasOS || !hasArch {
		return nil, errors.New("the matrix carries only one of the goos and goarch lists")
	}
	for _, goos := range scalarList(goosList) {
		for _, goarch := range scalarList(goarchList) {
			t, err := targetOf(goos, goarch)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// scalarList returns the items of a list node, or the node itself when it
// is not a list, so that targetOf refuses any item that is not a scalar.
func scalarList(n yaml.Node) []yaml.Node {
	if n.Kind != yaml.SequenceNode {
		return []yaml.Node{n}
	}
	out := make([]yaml.Node, 0, len(n.Content))
	for _, c := range n.Content {
		out = append(out, *c)
	}
	return out
}

// targetOf returns the target of a goos and a goarch node, which must both
// be scalars whose text has the form the go command takes.
func targetOf(goos, goarch yaml.Node) (lintTarget, error) {
	if goos.Kind != yaml.ScalarNode || goarch.Kind != yaml.ScalarNode {
		return lintTarget{}, errors.New("goos and goarch are not both plain values")
	}
	return targetOfNames(goos.Value, goarch.Value)
}

// targetOfNames returns the target of a GOOS and a GOARCH name, which
// must both have the form the go command takes.
func targetOfNames(goos, goarch string) (lintTarget, error) {
	if !lintTargetName.MatchString(goos) || !lintTargetName.MatchString(goarch) {
		return lintTarget{}, fmt.Errorf("goos %q and goarch %q are not both names of the form the go command takes", goos, goarch)
	}
	return lintTarget{goos: goos, goarch: goarch}, nil
}

// lintTargetName is the form of a GOOS or GOARCH value.
var lintTargetName = regexp.MustCompile(`^[a-z0-9]+$`)

// parsePlatform reads an os/arch platform, ignoring a variant after a
// second slash.
func parsePlatform(p string) (lintTarget, error) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return lintTarget{}, fmt.Errorf("platform %q is not os/arch", p)
	}
	t, err := targetOfNames(parts[0], parts[1])
	if err != nil {
		return lintTarget{}, fmt.Errorf("platform %q: %w", p, err)
	}
	return t, nil
}

// depsSection runs the coverage check and fails the golangci-lint check
// on a product package outside ./... in any build target it lists, or
// when the listing fails.
func (l golangciLint) depsSection(ctx context.Context) (string, error) {
	res, err := l.deps(ctx, l.root)
	if err != nil {
		return fmt.Sprintf("the check that the lint reads every package of the product binaries did not finish: %v", err), fmt.Errorf("product package listing: %w", err)
	}
	targets := make([]string, 0, len(res.targets))
	for _, t := range res.targets {
		targets = append(targets, t.String())
	}
	builds := strings.Join(targets, ", ")
	if len(res.targets) == 0 {
		return "the check that the lint reads every package of the product binaries listed no build", errors.New("product package listing: no build target")
	}
	if len(res.outside) > 0 {
		return fmt.Sprintf("packages the binaries under %s import in the builds %s that ./... does not match, so neither golangci-lint run reads them (move each out of a directory named testdata or starting with _ or .):\n  %s", lintProductMains, builds, strings.Join(res.outside, "\n  ")),
			fmt.Errorf("product packages outside the lint: %d", len(res.outside))
	}
	return fmt.Sprintf("every package of this module that the binaries under %s import in the builds %s is one ./... matches", lintProductMains, builds), nil
}
