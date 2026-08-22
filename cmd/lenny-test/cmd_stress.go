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
// The flake budget is 50 consecutive runs; any
// failure quarantines the test.
//
// Usage:
//
//	lenny-test stress --test TestSandboxClaimSkipLocked --runs 50
//	lenny-test stress --test TestSandboxClaim --runs 50 --pkg ./pkg/...
//	lenny-test stress --test TestX --runs 25 --tag integration
//
// The command shells out to `go test -run <name> -count=1` once per
// iteration, with the race detector on when the budget is being spent
// on a concurrency tier (see stressRaceDefault) or when --race says so.
// Anything beyond a single iteration result is recorded but the exit
// code reports the first failing run.
func runStress(args []string) int {
	fs := flag.NewFlagSet("stress", flag.ExitOnError)
	testName := fs.String("test", "", "exact test name to run (regex anchored with ^…$)")
	pattern := fs.String("pattern", "", "regex of test names; multiple matching tests are stressed in the same run")
	runs := fs.Int("runs", 50, "number of consecutive runs to attempt")
	target := fs.String("pkg", "./...", "package selector")
	tag := fs.String("tag", "", "optional build tag (e.g. component, contract, integration)")
	timeoutSec := fs.Int("timeout-seconds", 600, "per-run timeout")
	race := fs.String("race", "auto", "run each iteration under the race detector: auto (on for the concurrency tiers), on, off")
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
	useRace, err := resolveStressRace(*race, *tag, *target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "stress: %v\n", err)
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
	matched, err := listMatchingTests(runArg, *target, *tag, useRace)
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
	if useRace {
		fmt.Printf(" [race detector on]")
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
		cmd := buildGoTestStressCmd(runArg, *target, *tag, *timeoutSec, useRace)
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
			// Stop at the first failing run — the flake budget
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

// stressRaceDefault reports whether a budget against this build tag and
// package selector runs under the race detector by default. The
// concurrency tiers assert ordering and atomicity properties whose
// violations are data races on shared state, so a budget spent there is
// worth little without the detector. Every other budget keeps a plain
// argv: the detector needs cgo, and it distorts the wall-clock timing a
// latency scenario measures against its target. The cloud load and SLO
// tier is a wall-clock tier for that reason and is excluded here, which
// keeps this default and the tier runner one statement (TESTING.md §17.4
// determinism, §17.10 flake budget).
func stressRaceDefault(tag, target string) bool {
	if tag == "load_local" {
		return true
	}
	return strings.Contains(target, "tier7a_load_local")
}

// resolveStressRace turns the --race tri-state into the detector
// decision for this budget. "auto" defers to stressRaceDefault.
func resolveStressRace(mode, tag, target string) (bool, error) {
	switch mode {
	case "auto":
		return stressRaceDefault(tag, target), nil
	case "on", "true":
		return true, nil
	case "off", "false":
		return false, nil
	}
	return false, fmt.Errorf("--race must be one of auto, on, off (got %q)", mode)
}

// buildGoTestStressCmd assembles `go test -count=1 -run <runArg>` for a
// single stress iteration, with `-race` when race is set. runArg is
// already the anchored regex (or unanchored pattern); the caller does
// not pass a bare test name. The caller decides the detector from the
// tier being stressed rather than this function forcing it, so a budget
// on a non-concurrency test keeps a plain argv that builds without cgo.
func buildGoTestStressCmd(runArg, target, tag string, timeoutSec int, race bool) *exec.Cmd {
	args := []string{"test", "-count=1", "-run", runArg}
	if race {
		args = append(args, "-race")
	}
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
// package; we filter the footers out and return the names. race mirrors
// the iteration argv so discovery builds the package the same way the
// iterations do.
func listMatchingTests(runArg, target, tag string, race bool) ([]string, error) {
	args := []string{"test", "-list", runArg}
	if race {
		args = append(args, "-race")
	}
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
