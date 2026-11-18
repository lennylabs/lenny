// SPDX-License-Identifier: MIT

package annotation

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/lennylabs/lenny/scripts/specshift/scope"
)

// BranchReport is what one run of the lint over a branch's diff found.
type BranchReport struct {
	// Base is the merge-base commit the branch was compared with, and
	// BaseRef is the integration ref it was taken from.
	Base, BaseRef string
	// Files is the number of changed Go files the lint read.
	Files int
	// Findings is every refusal, ordered by file and line.
	Findings []Finding
	// Skipped names each check the run could not apply, with the reason.
	Skipped []string
}

// LintBranch lints the Go lines the working tree at root adds or changes
// relative to its merge base with the integration branch. It returns
// ErrNoBase when there is no merge base, so the caller skips with that
// reason.
//
// The domain is the shared read domain of scripts/specshift/scope, which
// leaves out every testdata directory, the change records, and the
// staged proposals; generated files are left out as well, because their
// comments are written by a generator.
func LintBranch(ctx context.Context, root string) (BranchReport, error) {
	var rep BranchReport
	base, ref, err := MergeBase(ctx, root)
	if err != nil {
		return rep, err
	}
	rep.Base, rep.BaseRef = base, ref
	changed, err := ChangedGoLines(ctx, root, base)
	if err != nil {
		return rep, err
	}
	read := scope.DirReader(root)
	list := scope.GitLister(root)
	lint, skipped, err := NewLint(ctx, list, read)
	if err != nil {
		return rep, err
	}
	rep.Skipped = skipped

	paths := make([]string, 0, len(changed))
	for p := range changed {
		if scope.Readable(p) {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	for _, p := range paths {
		src, err := read(p)
		if err != nil {
			return rep, fmt.Errorf("read %s: %w", p, err)
		}
		if IsGenerated(src) {
			continue
		}
		rep.Files++
		found, err := lint.Check(ChangedFile{Path: p, Src: src, Lines: changed[p]})
		if err != nil {
			return rep, err
		}
		rep.Findings = append(rep.Findings, found...)
	}
	return rep, nil
}

// NewLint loads what the lint compares with: the identifier registry
// with the issue records, and the specification's text. A registry the
// tree does not carry yet skips the identifier check, and the returned
// list names that skip.
func NewLint(ctx context.Context, list scope.Lister, read scope.FileReader) (Lint, []string, error) {
	var l Lint
	var skipped []string
	reg, err := LoadRegistry(read, RegistryPath)
	switch {
	case errors.Is(err, ErrNoRegistry):
		skipped = append(skipped, fmt.Sprintf("identifier resolution: %v, so no identifier citation can be resolved", err))
	case err != nil:
		return l, nil, err
	default:
		all, err := list(ctx)
		if err != nil {
			return l, nil, fmt.Errorf("list the tracked tree: %w", err)
		}
		reg.AddIssuePaths(all)
		l.Registry = reg
	}
	spec, err := LoadSpecText(ctx, list, read)
	if err != nil {
		return l, nil, err
	}
	l.Spec = spec
	return l, skipped, nil
}
