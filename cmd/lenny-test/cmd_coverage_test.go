// SPDX-License-Identifier: MIT

package main

import "testing"

// spec: TESTING.md §22.1 (80% floor on changed code).
//
// The changed-line coverage gate measures covered/coverable over the
// lines a diff touched. parseCoverProfileRanges must separate the lines
// the Go coverage model can instrument (any statement block) from the
// lines a test actually executed (a block with a non-zero hit count) so
// the gate's denominator excludes uncoverable lines.
func TestParseCoverProfileRangesSeparatesCoverableFromCovered(t *testing.T) {
	// Two blocks for one file: lines 10-11 executed (hit count 3),
	// lines 20-21 never executed (hit count 0). The module prefix is
	// stripped to the repo-relative path.
	profile := "mode: atomic\n" +
		"github.com/lennylabs/lenny/pkg/foo/foo.go:10.2,11.3 2 3\n" +
		"github.com/lennylabs/lenny/pkg/foo/foo.go:20.2,21.3 2 0\n"

	prof := parseCoverProfileRanges(profile)
	fp, ok := prof["pkg/foo/foo.go"]
	if !ok {
		t.Fatalf("expected pkg/foo/foo.go in profile, got keys %v", keysOf(prof))
	}

	// Both blocks are coverable; only the executed block is covered.
	for _, ln := range []int{10, 11, 20, 21} {
		if !fp.coverable[ln] {
			t.Errorf("line %d should be coverable", ln)
		}
	}
	for _, ln := range []int{10, 11} {
		if !fp.covered[ln] {
			t.Errorf("line %d should be covered", ln)
		}
	}
	for _, ln := range []int{20, 21} {
		if fp.covered[ln] {
			t.Errorf("line %d should not be covered", ln)
		}
	}
	// A line outside every block (e.g. a comment at line 5) is neither
	// coverable nor covered, so the gate never counts it.
	if fp.coverable[5] || fp.covered[5] {
		t.Errorf("line 5 is outside every statement block; want uncoverable")
	}
}

// spec: TESTING.md §22.1 (80% floor on changed code).
//
// A merged profile concatenates the blocks of several test runs under a
// single `mode:` header. A line stays covered once any run records a hit
// over it, so a block hit by the envtest tier lifts a line the unit tier
// left at zero.
func TestParseCoverProfileRangesMergesAcrossRuns(t *testing.T) {
	profile := "mode: atomic\n" +
		"github.com/lennylabs/lenny/pkg/foo/foo.go:10.2,11.3 2 0\n" +
		"github.com/lennylabs/lenny/pkg/foo/foo.go:10.2,11.3 2 5\n"

	fp := parseCoverProfileRanges(profile)["pkg/foo/foo.go"]
	if fp == nil {
		t.Fatal("expected pkg/foo/foo.go in profile")
	}
	for _, ln := range []int{10, 11} {
		if !fp.covered[ln] {
			t.Errorf("line %d covered by the second run should be covered", ln)
		}
	}
}

// spec: TESTING.md §22.1 (80% floor on changed code).
//
// countChanged is the per-file denominator the gate applies: a changed
// line counts only when the profile marks it coverable, and counts as
// covered only when the profile marks it executed. A file with no
// instrumentable statements (all declarations) contributes nothing.
func TestCountChangedExcludesUncoverableLines(t *testing.T) {
	fp := &fileProfile{
		coverable: map[int]bool{10: true, 11: true, 20: true, 21: true},
		covered:   map[int]bool{10: true, 11: true},
	}
	// Changed range 5-21 spans a comment (5-9), the covered block
	// (10-11), more comments (12-19), and the uncovered block (20-21).
	total, covered := countChanged(fp, []lineRange{{start: 5, end: 21}})
	if total != 4 {
		t.Errorf("total: want 4 coverable changed lines, got %d", total)
	}
	if covered != 2 {
		t.Errorf("covered: want 2, got %d", covered)
	}

	// A pure-declaration file has no profile entry; nothing counts.
	total, covered = countChanged(nil, []lineRange{{start: 1, end: 30}})
	if total != 0 || covered != 0 {
		t.Errorf("nil profile: want 0/0, got %d/%d", covered, total)
	}
}

func keysOf(m map[string]*fileProfile) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
