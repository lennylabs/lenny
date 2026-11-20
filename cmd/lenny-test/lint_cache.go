// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// The golangci-lint cache fill of the gate container. Inside the
// container, golangci-lint's cache lies on the build cache volume, which
// every later gate run of the same worktree reads. The container script
// (gateContainerScript) runs `lenny-test lint-cache-fill` before the gate,
// with the cache writable, then mounts the cache read-only and runs the
// gate, so the static tier's golangci-lint runs read the cache the fill
// wrote and no test tier can write it (owner's answer to GT-11,
// 2026-11-20). The fill runs the whole-tree run with the arguments and the
// environment of the static tier's check and discards its report; the
// changed-line run reads the same cache entries. The command is
// contributor tooling, so this file carries no specification annotation.

// runLintCacheFill runs the fill for the checkout at the repository root.
func runLintCacheFill(args []string) int {
	return lintCacheFillCommand(args, newGolangciLint(repoRoot(), ""), os.Stderr)
}

// lintCacheFillCommand runs the fill with the check l and reports to
// stderr. It returns 0 when golangci-lint ran to the end, with or without
// findings, 1 when it did not, and 2 on a usage error. A run without
// golangci-lint installed fills nothing and returns 1.
func lintCacheFillCommand(args []string, l golangciLint, stderr io.Writer) int {
	if len(args) > 0 {
		fmt.Fprintln(stderr, "usage: lenny-test lint-cache-fill (takes no arguments)")
		return 2
	}
	if err := l.fillCache(context.Background()); err != nil {
		fmt.Fprintf(stderr, "lenny-test lint-cache-fill: %v\n", err)
		return 1
	}
	return 0
}

// fillCache runs the whole-tree golangci-lint run of the check, which
// writes the analysis of every package to the checkout's cache directory
// (golangciLintEnv), and discards the report. golangci-lint's exit status
// for findings counts as a finished run.
func (l golangciLint) fillCache(ctx context.Context) error {
	if l.bin == "" {
		return errors.New("golangci-lint not found, so the cache was not filled")
	}
	report, err := os.CreateTemp("", "lenny-lint-fill-*.json")
	if err != nil {
		return fmt.Errorf("create the report file: %w", err)
	}
	name := report.Name()
	_ = report.Close()
	defer func() { _ = os.Remove(name) }()
	out, err := l.exec(ctx, l.bin, l.root, "", wholeTreeLintArgs(l.root, name)...)
	var exitErr *exec.ExitError
	if err != nil && (!errors.As(err, &exitErr) || exitErr.ExitCode() != golangciLintExitIssues) {
		return fmt.Errorf("golangci-lint did not finish its run on the whole tree (%w):\n%s", err, out)
	}
	return nil
}
