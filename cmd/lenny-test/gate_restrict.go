// SPDX-License-Identifier: MIT

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// This file holds the gate's tier and coverage options (change 0172,
// decision 9). `--tier` restricts the gate to some of the merge gate's
// tiers, keeping the diff selection within them, and `--cover` writes the
// unit run's coverage profile, so a code branch can run the check before
// a merge that decision 8 states: `lenny-test gate --tier
// static,unit,component,contract --cover`, then `lenny-test coverage
// --diff main --threshold 0.80` on that profile. The gate on `main` uses
// neither option. The harness is contributor tooling, so this file
// carries no specification annotation.

// parseGateTiers parses the gate's `--tier`: a comma-separated list of
// the merge gate's tiers. An empty value restricts nothing. A name that is
// not a tier, and a tier outside the merge gate, are refused, so a
// restricted run cannot reach a tier the gate does not run.
func parseGateTiers(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	names, err := parseTierList(value)
	if err != nil {
		return nil, err
	}
	merge, _ := gateTiers(gateMerge)
	for _, n := range names {
		if !contains(merge, n) {
			return nil, fmt.Errorf("--tier %s: the merge gate runs %s; run the %s tier with `lenny-test --tier %s`", n, strings.Join(merge, ", "), n, n)
		}
	}
	return names, nil
}

// gateRuns reports whether a run restricted to tiers runs the named
// tier. A nil restriction runs every tier.
func gateRuns(tiers []string, name string) bool {
	return tiers == nil || contains(tiers, name)
}

// prepareCoverProfile readies the coverage profile of a run with
// `--cover`. It removes a profile an earlier run left at path, so `lenny-test
// coverage --diff` after this run reads only what this run wrote, and it
// turns the unit plan's coverage back on, so the unit tier writes the
// profile. It returns why the earlier profile could not be removed, or nil.
func prepareCoverProfile(path string, plans map[string]tierPlan) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the earlier coverage profile %s: %w", path, err)
	}
	if p, ok := plans[tierUnit]; ok {
		p.noCoverage = false
		plans[tierUnit] = p
	}
	return nil
}

// finishCoverProfile writes a profile that covers nothing when the run
// wrote none: its selection held no unit tier, or the unit tier ran only
// test functions by name, which writes no profile. `lenny-test coverage
// --diff` then reads this run's result rather than failing on a missing
// file, and counts every changed code line of a production file the
// profile does not hold as uncovered (unmeasuredStatementLines).
func finishCoverProfile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create the directory of the coverage profile %s: %w", path, err)
	}
	if err := os.WriteFile(path, []byte("mode: atomic\n"), 0o644); err != nil {
		return fmt.Errorf("write an empty coverage profile %s: %w", path, err)
	}
	return nil
}

// gateCoverProfileError returns why `--cover` cannot write the profile
// that `lenny-test coverage --diff` reads, or nil. LENNY_COVER_PROFILE set
// to the empty value turns the profile off, and the gate container does
// not receive the host's environment, so on the host half a
// LENNY_COVER_PROFILE other than the default path would leave coverage
// --diff reading a file the gate never wrote.
func gateCoverProfileError(here bool, root string, lookup func(string) (string, bool)) error {
	v, ok := lookup("LENNY_COVER_PROFILE")
	if !ok {
		return nil
	}
	if v == "" {
		return fmt.Errorf("--cover with LENNY_COVER_PROFILE set empty writes no profile; unset LENNY_COVER_PROFILE")
	}
	if !here && filepath.Clean(v) != filepath.Join(root, coverProfileFile) {
		return fmt.Errorf("--cover in the gate container writes %s, and LENNY_COVER_PROFILE names %s, which coverage --diff would read instead; unset LENNY_COVER_PROFILE", coverProfileFile, v)
	}
	return nil
}

// coverProfileRecord returns the path the verdict records for the
// profile a `--cover` run wrote: relative to root when the profile lies
// under it, or "" when no profile exists at path.
func coverProfileRecord(root, path string) string {
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return path
}
