// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// `lenny-test secretscan` runs the static tier's secret scan on its own,
// over the committed tree at HEAD, so a merger can check a records-only
// commit in seconds without a merge-gate run (.claude/rules/git-workflow.md,
// "Merge only on a green gate verdict"; change 0158, decision 10). It runs
// runSecretScan, the function the static tier's check runs, and reports
// the same three outcomes. It implements the merge gate rather than a
// specification behavior, so it carries no spec annotation.

// The exit codes of `lenny-test secretscan`. Only exitSecretScanPass counts
// as passing; an unverified scan does not.
const (
	exitSecretScanPass       = 0
	exitSecretScanFail       = 1
	exitSecretScanUsage      = 2
	exitSecretScanUnverified = 3
)

// runSecretScanCommand parses the subcommand's flags, runs the secret scan
// over the committed tree at HEAD of the repository, prints the check's
// output and a verdict line naming the commit, and returns the exit code.
func runSecretScanCommand(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("secretscan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "", "repository root (default: the checkout holding the current directory)")
	if err := fs.Parse(args); err != nil {
		return exitSecretScanUsage
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "lenny-test secretscan: unexpected argument %q\n", fs.Arg(0))
		return exitSecretScanUsage
	}
	dir := *root
	if dir == "" {
		dir = repoRoot()
	}
	// The scan reads the commit HEAD resolved to here, so the commit the
	// verdict line names is the one the scan read.
	commit := headCommit(dir)
	out, err := runSecretScanAt(dir, commit)
	if out != "" {
		fmt.Fprintln(stdout, strings.TrimRight(out, "\n"))
	}
	if err != nil {
		fmt.Fprintf(stdout, "lenny-test secretscan: %s on %s: %v\n", verdictstatus.VerdictFail, commit, err)
		return exitSecretScanFail
	}
	if status, reason := classifyUnverified(out); status == verdictstatus.Unverified {
		fmt.Fprintf(stdout, "lenny-test secretscan: %s on %s: %s\n", verdictstatus.VerdictUnverified, commit, reason)
		return exitSecretScanUnverified
	}
	fmt.Fprintf(stdout, "lenny-test secretscan: %s on %s\n", verdictstatus.VerdictPass, commit)
	return exitSecretScanPass
}

// headCommit returns the commit ID of HEAD under dir, or "HEAD" when git
// cannot resolve it; the scan itself then fails and says why.
func headCommit(dir string) string {
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(out))
}

// runSecretScanSubcommand is the entry point main dispatches to.
func runSecretScanSubcommand(args []string) int {
	return runSecretScanCommand(args, os.Stdout, os.Stderr)
}
