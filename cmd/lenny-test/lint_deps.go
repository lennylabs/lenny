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
	"sort"
	"strings"
)

// The coverage check of the static tier's golangci-lint check. Both
// golangci-lint runs read the packages the pattern ./... matches, and the
// go command leaves out of that pattern every directory whose name starts
// with `_` or `.` and every directory named testdata, although a package
// there still compiles into a binary that imports it. The check lists the
// module's packages that the main packages under cmd/ import, directly or
// not, for the build the lint reads, and fails on any of them that ./...
// does not match. The main packages include those in a directory under
// cmd/ that starts with `_` or `.`, which ./cmd/... skips but a build that
// names the directory compiles (owner's answer to GT-10, 2026-11-20). The check is
// contributor tooling, so this file carries no specification annotation.

// lintProductMains is the pattern of the product binaries' packages.
const lintProductMains = "./cmd/..."

// productPackagesOutsideLint returns, sorted, the import paths of the
// module's packages that a main package under cmd/ imports, directly or
// not, and that ./... does not match. goList runs `go list` in root with
// the given arguments and returns its standard output. Every listing sets
// the lint's build tags, and goList runs in the lint's build target
// (lintTargetEnv), so the packages are the ones the lint's build compiles.
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
	cmd := exec.CommandContext(ctx, "go", append([]string{"list"}, args...)...)
	cmd.Dir = root
	cmd.Env = lintTargetEnv(os.Environ())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("go list %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// depsSection runs the coverage check and fails the golangci-lint check
// on a product package outside ./..., or when the listing fails.
func (l golangciLint) depsSection(ctx context.Context) (string, error) {
	outside, err := l.deps(ctx, l.root)
	if err != nil {
		return fmt.Sprintf("the check that the lint reads every package of the product binaries did not finish: %v", err), fmt.Errorf("product package listing: %w", err)
	}
	if len(outside) > 0 {
		return fmt.Sprintf("packages the binaries under %s import that ./... does not match, so neither golangci-lint run reads them (move each out of a directory named testdata or starting with _ or .):\n  %s", lintProductMains, strings.Join(outside, "\n  ")),
			fmt.Errorf("product packages outside the lint: %d", len(outside))
	}
	return fmt.Sprintf("every package of this module that the binaries under %s import is one ./... matches", lintProductMains), nil
}
