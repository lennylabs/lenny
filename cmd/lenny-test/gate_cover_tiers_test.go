// SPDX-License-Identifier: MIT

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The check that coverage --diff --require-gate makes of the tiers of the
// gate run behind the profile is contributor tooling (change 0172,
// decision 25), so these cases carry no specification annotation.

// writeTieredCoverRun records gate run id on commit as the only gate run
// with --cover in the history, with a passing verdict file on a clean tree
// that records hash and, when tiers is non-nil, the restriction --tier
// gave the run.
func writeTieredCoverRun(t *testing.T, root, id, commit, hash string, tiers []string) {
	t.Helper()
	writeCoverHistory(t, root, []historyLine{{RunID: id, Verdict: "PASS", TriggerMode: "gate", GitRevision: commit, Cover: true, CoverProfileSHA256: hash}}, map[string]string{id: "-"})
	gate := map[string]any{"cover": true, "cover_profile_sha256": hash}
	if tiers != nil {
		gate["restricted_to"] = tiers
	}
	body, err := json.Marshal(map[string]any{"verdict": "PASS", "gate": gate})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, gateResultsDir, "gate-"+id+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCoverageDiffRequireGateRefusesARunThatLeavesOutAMergeCheckTier
// checks that under --require-gate the profile of a passing gate run on
// HEAD is refused when --tier restricted the run to tiers that leave out
// any of static, unit, component, and contract, with the run's tiers and
// the missing ones named, and that without the flag the same profile is
// measured with a note naming the run's tiers.
func TestCoverageDiffRequireGateRefusesARunThatLeavesOutAMergeCheckTier(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	hash := profileHash(written)

	cases := []struct {
		tiers   []string
		missing string
	}{
		{[]string{"static", "unit"}, "leaves out component, contract"},
		{[]string{"unit", "component", "contract"}, "leaves out static"},
		{[]string{"static", "unit", "component", "integration"}, "leaves out contract"},
		{[]string{"static"}, "leaves out unit, component, contract"},
		{[]string{}, "leaves out static, unit, component, contract"},
	}
	for _, tc := range cases {
		writeTieredCoverRun(t, r.root, "t1", head, hash, tc.tiers)
		ran := "the tiers " + strings.Join(tc.tiers, ", ")
		_, err := checkProfileAgainstGate(r.root, profile, written, true)
		if err == nil || !strings.Contains(err.Error(), "--require-gate: gate run t1") || !strings.Contains(err.Error(), "ran "+ran+",") || !strings.Contains(err.Error(), tc.missing) || !strings.Contains(err.Error(), "--cover") {
			t.Errorf("restricted to %v, --require-gate: %v; want a refusal naming the run, %q, %q, and the gate command", tc.tiers, err, ran, tc.missing)
		}
		if len(tc.tiers) == 0 {
			continue
		}
		note, err := checkProfileAgainstGate(r.root, profile, written, false)
		if err != nil || !strings.Contains(note, "gate run t1") || !strings.Contains(note, "which ran "+ran) {
			t.Errorf("restricted to %v, without the flag: %q, %v; want the profile measured with a note naming %q", tc.tiers, note, err, ran)
		}
	}
}

// TestCoverageDiffRequireGateAcceptsARunOfEveryMergeCheckTier checks that
// under --require-gate the profile of a passing gate run on HEAD is
// accepted when --tier restricted the run to the four tiers of the check
// before a merge or to more, in any order, and when --tier did not
// restrict it, and that the note names the run's tiers.
func TestCoverageDiffRequireGateAcceptsARunOfEveryMergeCheckTier(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	hash := profileHash(written)

	cases := []struct {
		tiers []string
		ran   string
	}{
		{[]string{"static", "unit", "component", "contract"}, "the tiers static, unit, component, contract"},
		{[]string{"contract", "component", "unit", "static"}, "the tiers contract, component, unit, static"},
		{[]string{"static", "unit", "component", "contract", "integration", "docs"}, "the tiers static, unit, component, contract, integration, docs"},
		{nil, "every tier of the merge gate"},
	}
	for _, tc := range cases {
		writeTieredCoverRun(t, r.root, "t2", head, hash, tc.tiers)
		note, err := checkProfileAgainstGate(r.root, profile, written, true)
		if err != nil || !strings.Contains(note, "gate run t2") || !strings.Contains(note, "which ran "+tc.ran) {
			t.Errorf("restricted to %v, --require-gate: %q, %v; want it accepted with a note naming %q", tc.tiers, note, err, tc.ran)
		}
	}
}

// TestCoverageDiffRequireGateRefusesARestrictedRunOnADirtyTree checks that
// the tier check also holds when the tree has uncommitted changes, which
// do not skip the check under --require-gate.
func TestCoverageDiffRequireGateRefusesARestrictedRunOnADirtyTree(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	writeTieredCoverRun(t, r.root, "t3", head, profileHash(written), []string{"static", "unit"})
	r.write("a.go", "package m\n\nvar edited = 1\n")
	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "leaves out component, contract") {
		t.Errorf("a dirty tree and a run restricted to static and unit: %v; want a refusal", err)
	}
}

// TestCoverageDiffRequireGateRefusesARestrictedRunEndToEnd checks the
// wiring through reportDiffCoverage: with --require-gate a branch whose
// latest gate run with --cover ran only static and unit exits 1, and a run
// of the four tiers lets it through.
func TestCoverageDiffRequireGateRefusesARestrictedRunEndToEnd(t *testing.T) {
	r := newLintRepo(t, "main")
	r.git("checkout", "-q", "-b", "feature")
	head := r.commit("notes.txt", "a note\n", "feature")
	t.Setenv("LENNY_COVER_PROFILE", "")
	t.Chdir(r.root)
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\n")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, written, 0o644); err != nil {
		t.Fatal(err)
	}
	writeTieredCoverRun(t, r.root, "e1", head, profileHash(written), []string{"static", "unit"})
	if code := reportDiffCoverage("main", 0.8, false, true); code != 1 {
		t.Errorf("a run restricted to static and unit, --require-gate: exit %d; want 1", code)
	}
	if code := reportDiffCoverage("main", 0.8, false, false); code != 0 {
		t.Errorf("the same without the flag: exit %d; want 0", code)
	}
	writeTieredCoverRun(t, r.root, "e2", head, profileHash(written), []string{"static", "unit", "component", "contract"})
	if code := reportDiffCoverage("main", 0.8, false, true); code != 0 {
		t.Errorf("a run of the four tiers, --require-gate: exit %d; want 0", code)
	}
}
