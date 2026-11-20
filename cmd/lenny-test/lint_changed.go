// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
	"github.com/lennylabs/lenny/scripts/specshift/annotation"
)

// The golangci-lint check of the static tier. It runs the linter twice
// over the same configuration. The first run reports only the findings on
// the lines the branch adds or changes relative to its merge base with
// the integration branch, and any such finding fails the tier. The second
// run reports the findings of the whole tree, which include the findings
// that predate the check, as a non-fatal warning. The whole-tree run
// becomes fatal when the pre-existing findings that CONV-I1 lists reach
// zero. The check is contributor tooling, so this file carries no
// specification annotation.

// lintDebtIssue tracks the pre-existing golangci-lint findings that keep
// the whole-tree run a warning.
const lintDebtIssue = "CONV-I1"

// golangciLintTimeout bounds each golangci-lint run. A run that exceeds
// it exits non-zero, which fails the changed-line run rather than passing
// it. The gate container starts with an empty golangci-lint cache, so the
// first run analyzes the whole module.
const golangciLintTimeout = "10m"

// lintBase is the commit the changed-line run compares the working tree
// with.
type lintBase struct {
	// commit is the merge base of HEAD with ref.
	commit string
	// ref names the integration ref the merge base was taken against.
	ref string
	// atHead reports that the merge base is HEAD itself, as on the
	// integration branch, so only uncommitted and untracked changes count
	// as changed lines.
	atHead bool
}

// resolveLintBase returns the merge base the changed-line run compares
// with. With base empty, it takes the first integration ref the
// repository carries, main and then origin/main (annotation.MergeBase),
// which is the resolution the static tier's other changed-line lints use.
// With base set, as the merge gate's --base sets it, it takes the merge
// base of base with HEAD, or the base side of a range, as --since reads
// it (resolveDiffSides). Any failure, including a repository that carries
// none of the refs, is returned as an error.
func resolveLintBase(ctx context.Context, root, base string) (lintBase, error) {
	var b lintBase
	if base == "" {
		commit, ref, err := annotation.MergeBase(ctx, root)
		if err != nil {
			return lintBase{}, err
		}
		b = lintBase{commit: commit, ref: ref}
	} else {
		sides, err := resolveDiffSides(ctx, root, base)
		if err != nil {
			return lintBase{}, fmt.Errorf("find the merge base with %s: %w", base, err)
		}
		b = lintBase{commit: sides.base, ref: base}
	}
	head, err := commitOf(ctx, root, "HEAD")
	if err != nil {
		return lintBase{}, fmt.Errorf("resolve HEAD: %w", err)
	}
	b.atHead = b.commit == head
	return b, nil
}

// golangciLint is the static tier's golangci-lint check. Its fields are
// injected so a test runs the check's control flow without the linter or
// a repository.
type golangciLint struct {
	// root is the checkout the linter runs in.
	root string
	// base is the ref the branch merges into, or empty for the default
	// integration refs (see resolveLintBase).
	base string
	// bin is the golangci-lint binary, or empty when none is installed.
	bin string
	// resolve finds the merge base.
	resolve func(ctx context.Context, root, base string) (lintBase, error)
	// exec runs bin with args in root and returns the combined output.
	exec func(ctx context.Context, bin, root string, args ...string) (string, error)
}

// newGolangciLint returns the check wired to the installed linter, git,
// and the process runner.
//
// The linter runs in root with its symbolic links resolved. golangci-lint
// matches each finding to the diff by the finding's path relative to its
// working directory. When the working directory is reached through a
// symbolic link (on macOS, /var is a link to /private/var), the
// relative path it computes climbs out to the root and names no file in
// the diff, so every finding reads as old and the changed-line run passes
// whatever it finds.
func newGolangciLint(root, base string) golangciLint {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return golangciLint{
		root:    root,
		base:    base,
		bin:     resolveGoBin("golangci-lint"),
		resolve: resolveLintBase,
		exec:    execGolangciLint,
	}
}

// execGolangciLint runs golangci-lint in root.
func execGolangciLint(ctx context.Context, bin, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// run runs the check. A finding on a changed line, or a changed-line run
// that fails for any other reason, returns an error, which fails the
// tier. When the merge base cannot be resolved the changed-line run does
// not happen, and the output carries the unverified marker, so the
// tier reports that the check proved nothing rather than passing. The
// whole-tree run happens in every case except a missing linter, and its
// findings never fail the tier.
func (l golangciLint) run() (string, error) {
	if l.bin == "" {
		return "golangci-lint not found; skipping (run scripts/setup-dev.sh)", nil
	}
	ctx := context.Background()
	var sections []string
	var failure error
	b, err := l.resolve(ctx, l.root, l.base)
	if err != nil {
		sections = append(sections, fmt.Sprintf("%s golangci-lint did not run on the changed lines: the merge base could not be resolved (%v); fetch the integration branch or pass --base", verdictstatus.UnverifiedMarker, err))
	} else {
		section, runErr := l.changedLines(ctx, b)
		sections = append(sections, section)
		failure = runErr
	}
	sections = append(sections, l.wholeTree(ctx))
	return strings.Join(sections, "\n"), failure
}

// changedLines runs golangci-lint with --new-from-rev at the merge base.
// The linter then reports a finding only on a line `git diff <base>`
// adds or changes in the working tree, or anywhere in an untracked file,
// and it reports a type-checking error wherever it occurs.
func (l golangciLint) changedLines(ctx context.Context, b lintBase) (string, error) {
	scope := fmt.Sprintf("the lines changed since the merge base %s with %s", shortCommit(b.commit), b.ref)
	if b.atHead {
		scope = fmt.Sprintf("the uncommitted and untracked lines, because HEAD is the merge base %s with %s", shortCommit(b.commit), b.ref)
	}
	out, err := l.exec(ctx, l.bin, l.root, golangciLintArgs("--new-from-rev="+b.commit)...)
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return fmt.Sprintf("golangci-lint could not run on %s: %v\n%s", scope, err, out), fmt.Errorf("golangci-lint on the changed lines: %w", err)
		}
		return fmt.Sprintf("golangci-lint reports findings on %s:\n%s", scope, out), fmt.Errorf("golangci-lint on the changed lines: %w", err)
	}
	return fmt.Sprintf("golangci-lint: no finding on %s", scope), nil
}

// wholeTree runs golangci-lint over the whole tree and reports its
// findings as a warning.
func (l golangciLint) wholeTree(ctx context.Context) string {
	out, err := l.exec(ctx, l.bin, l.root, golangciLintArgs()...)
	if err != nil {
		return fmt.Sprintf("WARNING (non-fatal until the pre-existing findings %s lists reach zero; see .golangci.yml):\n%s", lintDebtIssue, out)
	}
	return "golangci-lint: no finding in the whole tree"
}

// golangciLintArgs returns the arguments of one golangci-lint run.
// golangci-lint holds a lock file in the temporary directory and, by
// default, exits with an error when another instance holds it, which
// would fail the changed-line run whenever a lint in another worktree on
// the same host overlaps it. --allow-serial-runners makes it wait for the
// lock instead.
func golangciLintArgs(extra ...string) []string {
	return append([]string{"run", "--timeout=" + golangciLintTimeout, "--allow-serial-runners"}, extra...)
}

// shortCommit abbreviates a commit hash for a message.
func shortCommit(c string) string {
	if len(c) > 12 {
		return c[:12]
	}
	return c
}
