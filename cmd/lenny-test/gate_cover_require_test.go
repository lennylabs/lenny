// SPDX-License-Identifier: MIT

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The check of coverage --diff against a recorded, passing gate run is
// contributor tooling (change 0172, decisions 23 and 24), so these cases
// carry no specification annotation.

// writeCoverVerdict writes the verdict file of gate run id, a run on a
// clean tree, under root with the given verdict and profile hash.
func writeCoverVerdict(t *testing.T, root, id, verdict, hash string) {
	t.Helper()
	body := `{"verdict":"` + verdict + `","gate":{"cover":true,"cover_profile_sha256":"` + hash + `"}}`
	if err := os.WriteFile(filepath.Join(root, gateResultsDir, "gate-"+id+".json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCoverageDiffRequireGateRefusesWithoutARecordedCoverRun checks that
// with --require-gate the check refuses when no gate run with --cover on
// HEAD is recorded, whether the history is absent or holds only runs on
// other commits, and passes the profile of a passing run on HEAD, while
// without the flag the same states measure the profile with a note.
func TestCoverageDiffRequireGateRefusesWithoutARecordedCoverRun(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")

	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "--require-gate") || !strings.Contains(err.Error(), "--cover") {
		t.Errorf("no history: %v; want a refusal naming --require-gate and the gate command", err)
	}
	if note, err := checkProfileAgainstGate(r.root, profile, written, false); err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("no history without the flag: %q, %v; want a note", note, err)
	}

	writeCoverHistory(t, r.root, []historyLine{
		{RunID: "o1", TriggerMode: "gate", GitRevision: "0000000", Cover: true},
		{RunID: "n1", TriggerMode: "gate", GitRevision: head},
		{RunID: "u1", TriggerMode: "run", GitRevision: head, Cover: true},
	}, map[string]string{"o1": "-"})
	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "no gate run with --cover is recorded for "+shortCommit(head)) {
		t.Errorf("only a run on another commit, a gate run without --cover, and a `run` line: %v; want a refusal", err)
	}
	if note, err := checkProfileAgainstGate(r.root, profile, written, false); err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("the same history without the flag: %q, %v; want a note", note, err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "p1", Verdict: "PASS", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"p1": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, written, true); err != nil || !strings.Contains(note, "gate run p1") {
		t.Errorf("a passing run on HEAD that wrote the profile: %q, %v; want it accepted", note, err)
	}
	rewritten := append(append([]byte{}, written...), "example.com/m/a.go:3.1,4.2 1 0\n"...)
	if _, err := checkProfileAgainstGate(r.root, profile, rewritten, true); err == nil || !strings.Contains(err.Error(), "gate run p1") {
		t.Errorf("a rewritten profile with the flag: %v; want a refusal naming gate run p1", err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "o2", Verdict: "PASS", TriggerMode: "gate", GitRevision: "0000000", Cover: true, CoverProfileSHA256: profileHash(written)}}, nil)
	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "a gate run on 0000000") {
		t.Errorf("with the flag, the profile of a run on another commit: %v; want a refusal naming that commit", err)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "m1", Verdict: "PASS", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"m1": "-"})
	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "verdict of gate run m1") {
		t.Errorf("with the flag, a run on HEAD whose verdict file is gone: %v; want a refusal", err)
	}
}

// TestCoverageDiffRequireGateChecksTheProfileOnADirtyTree checks that
// uncommitted changes do not skip the check under --require-gate: the
// profile must still be the one the latest gate run with --cover on HEAD
// wrote, and with no such run the check refuses.
func TestCoverageDiffRequireGateChecksTheProfileOnADirtyTree(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	rewritten := append(append([]byte{}, written...), "example.com/m/a.go:3.1,4.2 1 0\n"...)
	r.write("a.go", "package m\n\nvar edited = 1\n")

	if _, err := checkProfileAgainstGate(r.root, profile, written, true); err == nil || !strings.Contains(err.Error(), "--require-gate") {
		t.Errorf("a dirty tree with no run: %v; want a refusal", err)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "d1", Verdict: "PASS", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"d1": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, written, true); err != nil || !strings.Contains(note, "gate run d1") {
		t.Errorf("a dirty tree whose profile the passing run on HEAD wrote: %q, %v; want it accepted", note, err)
	}
	if _, err := checkProfileAgainstGate(r.root, profile, rewritten, true); err == nil || !strings.Contains(err.Error(), "gate run d1") {
		t.Errorf("a dirty tree whose profile a later run rewrote: %v; want a refusal", err)
	}
	if note, err := checkProfileAgainstGate(r.root, profile, rewritten, false); err != nil || !strings.Contains(note, "uncommitted changes") {
		t.Errorf("the same without the flag: %q, %v; want the check skipped with a note", note, err)
	}
}

// TestCoverageDiffRefusesTheProfileOfAGateRunThatDidNotPass checks that
// the profile of the latest gate run with --cover on HEAD is refused when
// that run's verdict is FAIL, UNVERIFIED, or absent, with and without
// --require-gate, and that on a dirty tree without the flag a profile
// whose hash a gate run that did not pass recorded is refused, while a
// profile no run recorded, or a passing run recorded, is measured.
func TestCoverageDiffRefusesTheProfileOfAGateRunThatDidNotPass(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")
	hash := profileHash(written)

	remedies := map[string]string{"FAIL": "fix the failure", "UNVERIFIED": "gate.unverified", "": "fix the failure"}
	for _, verdict := range []string{"FAIL", "UNVERIFIED", ""} {
		writeCoverHistory(t, r.root, []historyLine{{RunID: "f1", Verdict: verdict, TriggerMode: "gate", GitRevision: head, Cover: true, CoverProfileSHA256: hash}}, nil)
		writeCoverVerdict(t, r.root, "f1", verdict, hash)
		want := verdict
		if want == "" {
			want = "missing"
		}
		for _, require := range []bool{false, true} {
			_, err := checkProfileAgainstGate(r.root, profile, written, require)
			if err == nil || !strings.Contains(err.Error(), "gate run f1") || !strings.Contains(err.Error(), "verdict is "+want+" rather than PASS") || !strings.Contains(err.Error(), remedies[verdict]) {
				t.Errorf("verdict %q, --require-gate %v: %v; want a refusal naming the run, its verdict, and %q", verdict, require, err, remedies[verdict])
			}
		}
	}

	r.write("a.go", "package m\n\nvar edited = 1\n")
	writeCoverHistory(t, r.root, []historyLine{
		{RunID: "x1", Verdict: "PASS", TriggerMode: "gate", GitRevision: "0000000", Cover: true, CoverProfileSHA256: hash},
		{RunID: "x2", Verdict: "FAIL", TriggerMode: "gate", GitRevision: head, Cover: true, CoverProfileSHA256: hash},
	}, nil)
	if _, err := checkProfileAgainstGate(r.root, profile, written, false); err == nil || !strings.Contains(err.Error(), "gate run x2") {
		t.Errorf("a dirty tree whose profile a failing run wrote last: %v; want a refusal naming gate run x2", err)
	}
	other := []byte("mode: atomic\n")
	if note, err := checkProfileAgainstGate(r.root, profile, other, false); err != nil || !strings.Contains(note, "uncommitted changes") {
		t.Errorf("a dirty tree whose profile no gate run wrote: %q, %v; want a note", note, err)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "x3", Verdict: "PASS", TriggerMode: "gate", GitRevision: head, Cover: true, CoverProfileSHA256: hash}}, nil)
	if note, err := checkProfileAgainstGate(r.root, profile, written, false); err != nil || !strings.Contains(note, "uncommitted changes") {
		t.Errorf("a dirty tree whose profile a passing run wrote: %q, %v; want a note", note, err)
	}
}

// TestCoverageRequireGateAppliesOnlyToDiff checks that --require-gate
// without --diff is a usage error.
func TestCoverageRequireGateAppliesOnlyToDiff(t *testing.T) {
	if code := runCoverage([]string{"--go", "--require-gate"}); code != 2 {
		t.Errorf("--go --require-gate: exit %d; want 2", code)
	}
}

// TestCoverageDiffRequireGateHoldsWhenNoGoFileChanged checks the wiring of
// --require-gate into coverage --diff: the check runs before the command
// looks for changed .go files, so a branch that changes none still needs
// a recorded gate run, and a recorded passing run lets it through.
func TestCoverageDiffRequireGateHoldsWhenNoGoFileChanged(t *testing.T) {
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
	if code := reportDiffCoverage("main", 0.8, false, true); code != 1 {
		t.Errorf("no recorded gate run, no .go change, --require-gate: exit %d; want 1", code)
	}
	if code := reportDiffCoverage("main", 0.8, false, false); code != 0 {
		t.Errorf("the same without the flag: exit %d; want 0", code)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "g1", Verdict: "PASS", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"g1": profileHash(written)})
	if code := reportDiffCoverage("main", 0.8, false, true); code != 0 {
		t.Errorf("a recorded passing run, --require-gate: exit %d; want 0", code)
	}
	writeCoverVerdict(t, r.root, "g1", "FAIL", profileHash(written))
	if code := reportDiffCoverage("main", 0.8, false, false); code != 1 {
		t.Errorf("a failing run on HEAD, no .go change, no flag: exit %d; want 1", code)
	}
}
