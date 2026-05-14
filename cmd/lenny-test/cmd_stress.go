// SPDX-License-Identifier: MIT

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// runStress runs a single test repeatedly to detect flakes. Per
// TESTING.md §17.10 the flake budget is 50 consecutive runs; any
// failure quarantines the test.
//
// Usage:
//
//	lenny-test stress --test TestSandboxClaimSkipLocked --runs 50
//	lenny-test stress --test TestSandboxClaim --runs 50 --pkg ./pkg/...
//	lenny-test stress --test TestX --runs 25 --tag integration
//
// The command shells out to `go test -run <name> -count=1` once per
// iteration. Anything beyond a single iteration result is recorded
// but the exit code reports the first failing run.
func runStress(args []string) int {
	fs := flag.NewFlagSet("stress", flag.ExitOnError)
	testName := fs.String("test", "", "exact test name to run (regex anchored with ^…$)")
	pattern := fs.String("pattern", "", "regex of test names; multiple matching tests are stressed in the same run")
	runs := fs.Int("runs", 50, "number of consecutive runs to attempt")
	target := fs.String("pkg", "./...", "package selector")
	tag := fs.String("tag", "", "optional build tag (e.g. component, contract, integration)")
	timeoutSec := fs.Int("timeout-seconds", 600, "per-run timeout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *testName == "" && *pattern == "" {
		fmt.Fprintln(os.Stderr, "stress: one of --test or --pattern is required")
		return 2
	}
	if *testName != "" && *pattern != "" {
		fmt.Fprintln(os.Stderr, "stress: --test and --pattern are mutually exclusive")
		return 2
	}
	if *runs <= 0 {
		fmt.Fprintln(os.Stderr, "stress: --runs must be positive")
		return 2
	}

	// Build the `-run` argument: --test anchors with ^…$, --pattern
	// passes the regex through unanchored so multiple names match.
	runArg := ""
	label := ""
	if *testName != "" {
		runArg = "^" + *testName + "$"
		label = *testName
	} else {
		runArg = *pattern
		label = "pattern=" + *pattern
	}

	fmt.Printf("lenny-test stress: %d consecutive runs of %s against %s", *runs, label, *target)
	if *tag != "" {
		fmt.Printf(" (tag=%s)", *tag)
	}
	fmt.Println()

	start := time.Now()
	pass := 0
	fail := 0
	for i := 1; i <= *runs; i++ {
		cmd := buildGoTestStressCmd(runArg, *target, *tag, *timeoutSec)
		out, err := cmd.CombinedOutput()
		if err != nil {
			fail++
			fmt.Printf("  run %3d: FAIL (%v)\n", i, err)
			// Print a trimmed view of the output to surface the
			// failure without flooding the terminal.
			lines := strings.Split(string(out), "\n")
			for _, line := range trimLines(lines, 40) {
				fmt.Printf("    %s\n", line)
			}
			// Stop at the first failing run — the §17.10 budget
			// is zero tolerance.
			fmt.Printf("\nstress: failed at run %d/%d after %s\n", i, *runs, time.Since(start))
			return 1
		}
		pass++
		if i%10 == 0 || i == *runs {
			fmt.Printf("  run %3d: pass (cumulative %d/%d, %s elapsed)\n",
				i, pass, *runs, time.Since(start).Round(time.Second))
		}
	}
	fmt.Printf("\nstress: %d/%d runs passed (%s elapsed)\n", pass, *runs, time.Since(start).Round(time.Second))
	return 0
}

// buildGoTestStressCmd assembles `go test -count=1 -run <runArg>`
// for a single stress iteration. runArg is already the anchored
// regex (or unanchored pattern); the caller does not pass a bare
// test name.
func buildGoTestStressCmd(runArg, target, tag string, timeoutSec int) *exec.Cmd {
	args := []string{"test", "-count=1", "-run", runArg}
	if timeoutSec > 0 {
		args = append(args, fmt.Sprintf("-timeout=%ds", timeoutSec))
	}
	if tag != "" {
		args = append(args, "-tags="+tag)
	}
	args = append(args, target)
	return exec.Command("go", args...)
}

func trimLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	keep := lines[len(lines)-max:]
	return append([]string{fmt.Sprintf("... (%d earlier lines omitted)", len(lines)-max)}, keep...)
}
