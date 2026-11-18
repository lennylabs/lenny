// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// This file runs a scoped tier plan: the package patterns a selection runs
// whole, and the test functions a specification edit selects by name
// (change 0116, CS-4 and CS-5). The harness is contributor tooling, so this
// file carries no specification annotation.

// maxRunPatternBytes bounds the `-run` pattern of a by-name run. A
// specification edit to a broad section can name thousands of test
// functions, and a pattern past this size runs their packages whole
// instead, which runs a superset of the selection and keeps the command
// line well under the argument limit of every host the harness runs on.
const maxRunPatternBytes = 32 * 1024

// runPattern returns the `-run` pattern that matches exactly the named
// top-level test functions.
func runPattern(names []string) string {
	quoted := make([]string, 0, len(names))
	for _, n := range names {
		quoted = append(quoted, regexp.QuoteMeta(n))
	}
	return "^(" + strings.Join(quoted, "|") + ")$"
}

// scopedRuns returns the argument tails of the `go test` invocations a
// scoped plan needs: one that runs pkgs whole, and one that runs the
// functions of funcs by name in their packages. `-run` applies to every
// package of an invocation, so a name that exists in two of the by-name
// packages runs in both, which is a superset of the selection. When the
// pattern would pass maxRunPatternBytes, the by-name packages run whole.
func scopedRuns(pkgs []string, funcs map[string][]string) [][]string {
	whole := append([]string{}, pkgs...)
	dirs := sortedFuncDirs(funcs)
	seen := map[string]bool{}
	var names []string
	for _, dir := range dirs {
		for _, n := range funcs[dir] {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
			}
		}
	}
	sort.Strings(names)
	pattern := runPattern(names)
	if len(dirs) > 0 && len(pattern) > maxRunPatternBytes {
		whole = collapsePatterns(stringSet(append(whole, dirs...)))
		dirs = nil
	}
	var tails [][]string
	if len(whole) > 0 {
		tails = append(tails, whole)
	}
	if len(dirs) > 0 {
		tails = append(tails, append([]string{"-run", pattern}, dirs...))
	}
	return tails
}

// runGoTestJSONFn runs one `go test -json` invocation. Tests replace it to
// record the arguments a tier hands `go test` without running it.
var runGoTestJSONFn = runGoTestJSON

// runGoTestTails runs one `go test` invocation per tail, each with the
// flags argsFor returns for it, and merges the results. The error is the
// first invocation's error.
func runGoTestTails(tails [][]string, argsFor func(i int, tail []string) []string) (*tierResult, error) {
	merged := &tierResult{}
	var firstErr error
	for i, tail := range tails {
		r, err := runGoTestJSONFn(append(argsFor(i, tail), tail...)...)
		mergeTierResult(merged, r)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return merged, firstErr
}

// mergeTierResult adds the counts, failures, and output of one invocation
// to a tier's result.
func mergeTierResult(into, r *tierResult) {
	if r == nil {
		return
	}
	into.Total += r.Total
	into.Passed += r.Passed
	into.Failed += r.Failed
	into.Skipped += r.Skipped
	into.Failures = append(into.Failures, r.Failures...)
	if r.RawOut != "" {
		if into.RawOut != "" {
			into.RawOut += "\n"
		}
		into.RawOut += r.RawOut
	}
}

// tierOutcome maps a tier's merged result to its status and detail.
func tierOutcome(label string, result *tierResult, runErr error) (string, string, *tierResult) {
	if runErr != nil && result.Failed == 0 {
		return "fail", fmt.Sprintf("%s suite failed: %v\n%s", label, runErr, result.RawOut), result
	}
	if result.Failed > 0 {
		return "fail", summarizeFailures(result), result
	}
	return "pass", "", result
}

// runScopedTier runs a scoped plan of a tagged tier with the tier's flags.
// A scope that names nothing runs nothing, because `go test` with no
// package argument would test the working directory instead.
func runScopedTier(label string, flags []string, p tierPlan) (string, string, *tierResult) {
	tails := scopedRuns(p.pkgs, p.funcs)
	if len(tails) == 0 {
		return "pass", "no packages in the selected scope", &tierResult{}
	}
	result, runErr := runGoTestTails(tails, func(int, []string) []string { return append([]string{}, flags...) })
	return tierOutcome(label, result, runErr)
}
