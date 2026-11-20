// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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
	mainsOut, err := goList(ctx, root, append([]string{tags, "-f", `{{if eq .Name "main"}}{{.ImportPath}}{{end}}`, lintProductMains}, hidden...)...)
	if err != nil {
		return nil, fmt.Errorf("list the main packages under %s: %w", lintProductMains, err)
	}
	mains := strings.Fields(mainsOut)
	if len(mains) == 0 {
		return nil, fmt.Errorf("no main package under %s", lintProductMains)
	}
	depsOut, err := goList(ctx, root, append([]string{tags, "-deps", "-f", "{{with .Module}}{{.Path}}{{end}} {{.ImportPath}}"}, mains...)...)
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
	for _, line := range strings.Split(depsOut, "\n") {
		mod, pkg, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || mod != module || covered[pkg] {
			continue
		}
		outside = append(outside, pkg)
	}
	sort.Strings(outside)
	return outside, nil
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
// build targets: each job's matrix, and the inputs of each step.
type releaseWorkflow struct {
	Jobs map[string]struct {
		Strategy struct {
			Matrix yaml.Node `yaml:"matrix"`
		} `yaml:"strategy"`
		Steps []struct {
			With map[string]yaml.Node `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// releaseTargets returns, sorted and without duplicates, the build targets
// the release workflow body names in either of the two forms it uses:
//
//   - A job matrix whose include entries carry goos and goarch, or whose
//     top level carries goos and goarch lists, which give every pair. The
//     cross-compiled CLI archives use this form.
//   - A step input named platforms that lists os/arch pairs separated by
//     commas or new lines, as docker/build-push-action reads it. A variant
//     after a second slash is dropped, because the go command's listing
//     does not depend on it. The images use this form.
//
// A matrix that is not a mapping, such as an expression the workflow
// evaluates, an entry that names only one of goos and goarch, a platform
// that is not an os/arch pair, and a workflow that names no target are
// errors, so a change of form fails the check rather than dropping a
// target from it.
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
		for i, step := range job.Steps {
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

// matrixTargets returns the targets a job matrix names: one per include
// entry that carries goos and goarch, and every pair of the top-level goos
// and goarch lists. A job with no matrix names none.
func matrixTargets(m yaml.Node) ([]lintTarget, error) {
	if m.Kind == 0 {
		return nil, nil
	}
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("the matrix is not a mapping, so its build targets cannot be read")
	}
	var matrix map[string]any
	if err := m.Decode(&matrix); err != nil {
		return nil, fmt.Errorf("read the matrix: %w", err)
	}
	var out []lintTarget
	if include, ok := matrix["include"]; ok {
		entries, ok := include.([]any)
		if !ok {
			return nil, errors.New("the matrix include is not a list")
		}
		for i, e := range entries {
			entry, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("matrix include entry %d is not a mapping", i+1)
			}
			goos, hasOS := entry["goos"]
			goarch, hasArch := entry["goarch"]
			if !hasOS && !hasArch {
				continue
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
	oses, err := stringList(goosList)
	if err != nil || !hasArch {
		return nil, errors.New("the matrix carries goos and goarch only as two lists of strings")
	}
	arches, err := stringList(goarchList)
	if err != nil {
		return nil, errors.New("the matrix carries goos and goarch only as two lists of strings")
	}
	for _, goos := range oses {
		for _, goarch := range arches {
			t, err := targetOf(goos, goarch)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// stringList returns v as a list of strings, accepting a single string.
func stringList(v any) ([]string, error) {
	switch v := v.(type) {
	case string:
		return []string{v}, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			s, ok := e.(string)
			if !ok {
				return nil, fmt.Errorf("%v is not a string", e)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%v is not a list of strings", v)
}

// targetOf returns the target of a goos and a goarch value, which must
// both be strings of the form the go command takes.
func targetOf(goos, goarch any) (lintTarget, error) {
	osName, okOS := goos.(string)
	archName, okArch := goarch.(string)
	if !okOS || !okArch || !lintTargetName.MatchString(osName) || !lintTargetName.MatchString(archName) {
		return lintTarget{}, fmt.Errorf("goos %v and goarch %v are not both names of the form the go command takes", goos, goarch)
	}
	return lintTarget{goos: osName, goarch: archName}, nil
}

// lintTargetName is the form of a GOOS or GOARCH value.
var lintTargetName = regexp.MustCompile(`^[a-z0-9]+$`)

// parsePlatform reads an os/arch platform, dropping a variant after a
// second slash.
func parsePlatform(p string) (lintTarget, error) {
	parts := strings.Split(p, "/")
	if len(parts) < 2 || len(parts) > 3 {
		return lintTarget{}, fmt.Errorf("platform %q is not os/arch", p)
	}
	t, err := targetOf(parts[0], parts[1])
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
