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

// coverHistory writes the run history and the gate verdicts under root:
// one line per run, and a verdict file for each gate run whose hash is
// given (an empty hash writes a verdict without one, and "-" writes none).
func coverHistory(t *testing.T, root string, lines []historyLine, hashes map[string]string) {
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
	coverHistory(t, root, []historyLine{
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

	coverHistory(t, r.root, []historyLine{{RunID: "r1", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r1": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, written); err != nil || !strings.Contains(note, "r1") {
		t.Errorf("the gate's own profile: %q, %v; want it accepted", note, err)
	}
	rewritten := append(append([]byte{}, written...), "example.com/m/a.go:3.1,4.2 1 0\n"...)
	if _, err := checkProfileAgainstGate(r.root, profile, rewritten); err == nil || !strings.Contains(err.Error(), "gate run r1") {
		t.Errorf("a rewritten profile: %v; want a refusal naming gate run r1", err)
	}

	coverHistory(t, r.root, []historyLine{{RunID: "r2", TriggerMode: "gate", GitRevision: head, Cover: true}}, map[string]string{"r2": ""})
	if _, err := checkProfileAgainstGate(r.root, profile, written); err == nil || !strings.Contains(err.Error(), "no hash") {
		t.Errorf("a verdict without a hash: %v; want a refusal", err)
	}

	coverHistory(t, r.root, []historyLine{{RunID: "r3", TriggerMode: "gate", GitRevision: "0000000", Cover: true}}, map[string]string{"r3": profileHash(written)})
	if note, err := checkProfileAgainstGate(r.root, profile, rewritten); err != nil || !strings.Contains(note, "not checked") {
		t.Errorf("a gate run on another commit only: %q, %v; want the profile measured", note, err)
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
}
