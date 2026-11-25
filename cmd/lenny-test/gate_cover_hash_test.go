// SPDX-License-Identifier: MIT

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The coverage profile's hash and the container messages are contributor
// tooling (change 0172, decision 18), so these cases carry no
// specification annotation.

// profileHash returns the hex SHA-256 of b.
func profileHash(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// TestGateCoverRunRecordsTheProfileHash checks that a gate run with
// --cover records the SHA-256 of the profile it wrote in its verdict and
// its line of the run history, and that a run without --cover records
// none.
func TestGateCoverRunRecordsTheProfileHash(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, coverProfileFile)
	g := stubGateRunner()
	v := g.run(gateOptions{root: root, base: "main", rdepsDepth: defaultRdepsDepth, environment: "lenny-gate:test", cover: true, coverProfile: profile, tiers: []string{tierStatic, tierUnit}})
	body, err := os.ReadFile(profile)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "mode: atomic\n" {
		t.Fatalf("the stub's unit tier wrote no profile, so the run should have written the empty one; got %q", body)
	}
	if v.Gate.CoverProfileSHA256 != profileHash(body) {
		t.Errorf("the verdict records %q; want the SHA-256 of the profile, %s", v.Gate.CoverProfileSHA256, profileHash(body))
	}
	if got, err := fileSHA256(profile); err != nil || got != profileHash(body) {
		t.Errorf("fileSHA256 = %q, %v", got, err)
	}
	hist := filepath.Join(t.TempDir(), verdictHistoryFile)
	if err := appendHistory(hist, v); err != nil {
		t.Fatal(err)
	}
	line, err := os.ReadFile(hist)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(line, &rec); err != nil || rec["cover_profile_sha256"] != profileHash(body) {
		t.Errorf("the history line %s lacks the hash (%v)", line, err)
	}

	var summary strings.Builder
	printGateSummary(&summary, v, "x")
	if !strings.Contains(summary.String(), "(SHA-256 "+profileHash(body)[:12]+")") {
		t.Errorf("the summary does not name the profile's hash:\n%s", summary.String())
	}

	v = runStubGate(stubGateRunner())
	if v.Gate.CoverProfileSHA256 != "" {
		t.Errorf("a run without --cover records the hash %q", v.Gate.CoverProfileSHA256)
	}
}

// writeCoverHistory writes the run history and the gate verdicts under root:
// one line per run, and a verdict file for each gate run whose hash is
// given (an empty hash writes a verdict without one, and "-" writes none).
func writeCoverHistory(t *testing.T, root string, lines []historyLine, hashes map[string]string) {
	t.Helper()
	dir := filepath.Join(root, gateResultsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString("not json\n")
	for _, l := range lines {
		j, err := json.Marshal(l)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(j)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, verdictHistoryFile), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	for run, hash := range hashes {
		if hash == "-" {
			continue
		}
		body := `{"gate":{"cover":true}}`
		if hash != "" {
			body = `{"gate":{"cover":true,"cover_profile_sha256":"` + hash + `"}}`
		}
		if err := os.WriteFile(filepath.Join(dir, "gate-"+run+".json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLatestCoverGateRunPicksTheLastCoverRunOnTheCommit checks that the
// lookup takes the last gate run with --cover on the commit, passing over
// a `run` line, a gate run without --cover, a gate run on another commit,
// and a line that does not parse, and that it reports no run for an
// absent history and an error for a run whose verdict file is gone.
func TestLatestCoverGateRunPicksTheLastCoverRunOnTheCommit(t *testing.T) {
	root := t.TempDir()
	if _, found, err := latestCoverGateRun(root, "c1"); found || err != nil {
		t.Errorf("an absent history: found %v, %v", found, err)
	}
	writeCoverHistory(t, root, []historyLine{
		{RunID: "a", TriggerMode: "gate", GitRevision: "c1", Cover: true},
		{RunID: "b", TriggerMode: "gate", GitRevision: "c1", Cover: true},
		{RunID: "c", TriggerMode: "run", GitRevision: "c1", Cover: true},
		{RunID: "d", TriggerMode: "gate", GitRevision: "c1"},
		{RunID: "e", TriggerMode: "gate", GitRevision: "c2", Cover: true},
	}, map[string]string{"a": "aaa", "b": "bbb", "e": "-"})
	run, found, err := latestCoverGateRun(root, "c1")
	if err != nil || !found || run.runID != "b" || run.sha256 != "bbb" {
		t.Errorf("c1: %+v, %v, %v; want run b with hash bbb", run, found, err)
	}
	if _, found, err := latestCoverGateRun(root, "c2"); !found || err == nil {
		t.Errorf("c2, whose verdict is gone: found %v, %v; want an error", found, err)
	}
	if _, found, err := latestCoverGateRun(root, "c3"); found || err != nil {
		t.Errorf("c3: found %v, %v; want no run", found, err)
	}
}

// TestCoverageDiffRefusesAProfileTheGateDidNotWrite checks the check
// coverage --diff runs before it measures: a profile whose hash matches
// the latest gate run with --cover on HEAD passes, a profile a later run
// rewrote is refused with the run named, a verdict without a hash is
// refused, and with no such run the profile is measured with a note.
func TestCoverageDiffRefusesAProfileTheGateDidNotWrite(t *testing.T) {
	r := newLintRepo(t, "main")
	head := r.git("rev-parse", "HEAD")
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\nexample.com/m/a.go:1.1,2.2 1 1\n")

	note, err := checkProfileAgainstGate(r.root, profile, written)
	if err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("no gate run: %q, %v; want a note and no refusal", note, err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "r1", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r1": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, written); err != nil || !strings.Contains(note, "r1") {
		t.Errorf("the gate's own profile: %q, %v; want it accepted", note, err)
	}
	rewritten := append(append([]byte{}, written...), "example.com/m/a.go:3.1,4.2 1 0\n"...)
	if _, err := checkProfileAgainstGate(r.root, profile, rewritten); err == nil || !strings.Contains(err.Error(), "gate run r1") {
		t.Errorf("a rewritten profile: %v; want a refusal naming gate run r1", err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "r2", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r2": ""})
	if _, err := checkProfileAgainstGate(r.root, profile, written); err == nil || !strings.Contains(err.Error(), "no hash") {
		t.Errorf("a verdict without a hash: %v; want a refusal", err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "r3", TriggerMode: "gate", GitRevision: "0000000", Cover: true}}, map[string]string{"r3": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, rewritten); err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("a gate run on another commit only: %q, %v; want the profile measured", note, err)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "r4", TriggerMode: "gate", GitRevision: "0000000", Cover: true, CoverProfileSHA256: profileHash(written)}}, map[string]string{"r4": profileHash(written)})
	if _, err := checkProfileAgainstGate(r.root, profile, written); err == nil || !strings.Contains(err.Error(), "a gate run on 0000000") {
		t.Errorf("the profile of a gate run on another commit: %v; want a refusal naming that commit", err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "r5", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r5": profileHash(written)})
	dirtyVerdict := `{"gate":{"cover":true,"dirty":true,"cover_profile_sha256":"` + profileHash(written) + `"}}`
	if err := os.WriteFile(filepath.Join(r.root, gateResultsDir, "gate-r5.json"), []byte(dirtyVerdict), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := checkProfileAgainstGate(r.root, profile, written); err == nil || !strings.Contains(err.Error(), "uncommitted changes") {
		t.Errorf("a gate run on a dirty tree: %v; want a refusal", err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "../../x", TriggerMode: "gate", GitRevision: head, Cover: true}}, nil)
	if note, err := checkProfileAgainstGate(r.root, profile, written); err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("a run identifier with a path in it: %q, %v; want the line skipped", note, err)
	}

	writeCoverHistory(t, r.root, []historyLine{{RunID: "r6", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r6": profileHash(written)})
	r.write("a.go", "package m\n\nvar edited = 1\n")
	if note, err := checkProfileAgainstGate(r.root, profile, rewritten); err != nil || !strings.Contains(note, "uncommitted changes") {
		t.Errorf("a tree with uncommitted changes: %q, %v; want the check skipped with a note", note, err)
	}

	if _, err := checkProfileAgainstGate(t.TempDir(), profile, written); err == nil {
		t.Error("a directory outside a repository: HEAD resolved")
	}
}

// TestContainerMessagesNameTheCommandThatRunsThem checks that the
// image-build line and the build cache lines open with the command that
// runs the container, so `lenny-test run --container` does not print
// `lenny-test gate`.
func TestContainerMessagesNameTheCommandThatRunsThem(t *testing.T) {
	if got := gateImageBuildMessage("lenny-test run", "lenny-gate:x", "no image has the tag"); !strings.HasPrefix(got, "lenny-test run: building the gate image lenny-gate:x") {
		t.Errorf("image build line %q", got)
	}
	for _, f := range []*fakeGateCommands{
		{duOut: duLine(kb96_8GB)},
		{duErr: errors.New("daemon down")},
	} {
		var log strings.Builder
		capGateGoCache(context.Background(), f.run, "img", 30, "lenny-test run", &log)
		if log.Len() == 0 || strings.Contains(log.String(), "lenny-test gate") || !strings.HasPrefix(log.String(), "lenny-test run: ") {
			t.Errorf("build cache line %q; want it to open with lenny-test run", log.String())
		}
	}
	for label, knob := range map[string]string{"lenny-test gate": "--gocache-cap-gb", "lenny-test run": gateEnvGoCacheCap} {
		var log strings.Builder
		capGateGoCache(context.Background(), (&fakeGateCommands{duOut: duLine(kb96_8GB)}).run, "img", 30, label, &log)
		if !strings.Contains(log.String(), "("+knob+")") {
			t.Errorf("%s: the emptied line %q does not name %s", label, log.String(), knob)
		}
	}
}

// TestCoverageDiffRunsTheGateCheckBeforeItMeasures checks the wiring of
// the check into coverage --diff: a profile that the latest gate run with
// --cover on HEAD did not write fails the command, and the gate's own
// profile passes it.
func TestCoverageDiffRunsTheGateCheckBeforeItMeasures(t *testing.T) {
	r := newLintRepo(t, "main")
	r.git("checkout", "-q", "-b", "feature")
	r.commit("b.go", "package m\n\nfunc B() int { return 1 }\n", "feature")
	head := r.git("rev-parse", "HEAD")
	t.Setenv("LENNY_COVER_PROFILE", "")
	t.Chdir(r.root)
	profile := filepath.Join(r.root, coverProfileFile)
	written := []byte("mode: atomic\n")
	if err := os.MkdirAll(filepath.Dir(profile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile, []byte("mode: atomic\nexample.com/m/b.go:3.1,3.30 1 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeCoverHistory(t, r.root, []historyLine{{RunID: "w1", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"w1": profileHash(written)})
	if code := reportDiffCoverage("main", 0, true); code != 1 {
		t.Errorf("a profile the gate did not write: exit %d; want 1", code)
	}
	if err := os.WriteFile(profile, written, 0o644); err != nil {
		t.Fatal(err)
	}
	if code := reportDiffCoverage("main", 0, true); code != 0 {
		t.Errorf("the gate's own profile: exit %d; want 0", code)
	}
}
