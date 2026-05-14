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

	// Preflight: ask `go test -list <runArg>` which tests match. A
	// silent "0 tests" would otherwise look like a clean 50/50 pass
	// when in fact nothing ran. Print the names so the operator can
	// see what's about to be hammered.
	matched, err := listMatchingTests(runArg, *target, *tag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stress: discovery failed: %v\n", err)
		return 2
	}
	if len(matched) == 0 {
		fmt.Fprintf(os.Stderr, "stress: no tests match %s in %s", label, *target)
		if *tag != "" {
			fmt.Fprintf(os.Stderr, " (tag=%s)", *tag)
		}
		fmt.Fprintln(os.Stderr, "\nVerify the test name, the package selector, and the build tag.")
		return 2
	}

	fmt.Printf("lenny-test stress: %d consecutive runs of %s against %s", *runs, label, *target)
	if *tag != "" {
		fmt.Printf(" (tag=%s)", *tag)
	}
	fmt.Println()
	fmt.Printf("stress: %d test(s) matched discovery:\n", len(matched))
	for _, n := range matched {
		fmt.Printf("  - %s\n", n)
	}

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

// listMatchingTests asks `go test -list <regex>` which tests under
// the target package(s) match the regex. The output is a flat list
// of test names, one per line, interleaved with "ok" footers per
// package; we filter the footers out and return the names.
func listMatchingTests(runArg, target, tag string) ([]string, error) {
	args := []string{"test", "-list", runArg}
	if tag != "" {
		args = append(args, "-tags="+tag)
	}
	args = append(args, target)
	out, err := exec.Command("go", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("go test -list: %w\n%s", err, out)
	}
	var matched []string
	for _, line := range strings.Split(string(out), "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		// `go test -list` prints package footer lines starting with
		// "ok " or "?". Filter those out.
		if strings.HasPrefix(s, "ok ") || strings.HasPrefix(s, "?") {
			continue
		}
		// `[no tests to run]` and `FAIL ` are also footer-like noise.
		if strings.HasPrefix(s, "FAIL") || strings.HasPrefix(s, "[no") {
			continue
		}
		matched = append(matched, s)
	}
	return matched, nil
}

func trimLines(lines []string, max int) []string {
	if len(lines) <= max {
		return lines
	}
	keep := lines[len(lines)-max:]
	return append([]string{fmt.Sprintf("... (%d earlier lines omitted)", len(lines)-max)}, keep...)
}
