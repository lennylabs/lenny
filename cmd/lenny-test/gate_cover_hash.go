// SPDX-License-Identifier: MIT

package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/lennylabs/lenny/cmd/lenny-test/verdictstatus"
)

// This file ties `lenny-test coverage --diff` to the gate run that wrote
// the profile it reads (change 0172, decisions 18, 23, and 24). A gate run
// with --cover records the SHA-256 of the profile it wrote as
// gate.cover_profile_sha256 in its verdict and its line of the run
// history. Before it measures, coverage --diff on a tree without
// uncommitted changes to tracked files finds the latest gate run with
// --cover on the commit HEAD names and refuses a run on a dirty tree, a
// run whose verdict is not PASS, a run whose verdict records no hash, and
// a profile whose hash differs from that run's. When no such run is
// recorded, it refuses a profile whose hash a gate run with --cover on
// another commit recorded. Otherwise, without --require-gate, it measures
// the profile with a note, because `lenny-test run --changed` and
// `lenny-test --tier unit` also write it, and with --require-gate, the
// check before a merge, it refuses. On a tree with uncommitted changes the
// profile comes from the developer loop, which no gate verdict on HEAD
// describes, so without --require-gate the check is skipped with a note,
// unless the profile's hash is the one a gate run whose verdict is not
// PASS recorded. With --require-gate the uncommitted changes do not skip
// the check: the measured lines are the committed ones, and the profile
// still has to be the one the latest gate run with --cover on HEAD wrote.
// A run of `lenny-test gate --rerun-failed` stands for the run whose
// failed tests it reran when that run is a gate run on the same commit, on
// a clean tree, with the profile hash and the tier restriction the rerun
// records (checkRerunOrigin, change 0172, decision 26).
// With --require-gate that run must also have run every tier of the
// v1 merge check, static, unit, component, and contract (decision 25): a
// run that --tier restricted records the tiers it ran as
// gate.restricted_to, and one whose list leaves out any of the four is
// refused. A run that --tier did not restrict records no list and ran
// every tier of the merge gate. The note of an accepted profile names the
// run's tiers.
//
// The check detects a stale or rewritten profile. It is no defense
// against the branch under test: the profile is written by the branch's
// test binaries, and the branch's tests can write the results directory.
// The harness is contributor tooling, so this file carries no
// specification annotation.

// fileSHA256 returns the hex SHA-256 of the file at path.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// coverGateRun is the latest gate run with --cover on one commit.
type coverGateRun struct {
	runID string
	// sha256 is the hash its verdict records, or "" when the verdict
	// records none.
	sha256 string
	// dirty reports a run on a tree with uncommitted changes.
	dirty bool
	// verdict is the run's verdict, such as PASS or FAIL.
	verdict string
	// restrictedTo lists the tiers a run that --tier restricted ran, as
	// its verdict's gate.restricted_to records them; nil for a run of
	// every tier of the merge gate.
	restrictedTo []string
	// rerunOf names the gate run a `--rerun-failed` run reran, and
	// rerunOfSHA256 the profile hash that run recorded; both are "" for
	// a run that is not a rerun.
	rerunOf, rerunOfSHA256 string
}

// requiredGateTiers are the tiers a gate run must have run for
// `coverage --diff --require-gate` to accept its profile: the tiers of the
// check before a merge in the v1 merge policy (change 0172, decisions 8
// and 25).
var requiredGateTiers = []string{tierStatic, tierUnit, tierComponent, tierContract}

// historyLine is the part of a run history line the coverage check reads.
type historyLine struct {
	RunID              string `json:"run_id"`
	Verdict            string `json:"verdict,omitempty"`
	TriggerMode        string `json:"trigger_mode"`
	GitRevision        string `json:"git_revision"`
	Cover              bool   `json:"cover"`
	CoverProfileSHA256 string `json:"cover_profile_sha256,omitempty"`
	// RerunOf names the gate run a `--rerun-failed` run reran.
	RerunOf string `json:"rerun_of,omitempty"`
}

// coverRun names a gate run with --cover that recorded a profile hash: its
// identifier, its commit, and its verdict.
type coverRun struct {
	runID, commit, verdict string
}

// runIDPattern is the form of a run identifier (newRunID), which the
// check requires before it names a verdict file after one, so a history
// line cannot point the read outside the results directory.
var runIDPattern = regexp.MustCompile(`^[0-9A-Za-z]+$`)

// coverHistory is what the run history says about gate runs with --cover.
type coverHistory struct {
	// last is the run identifier of the latest such run on the commit
	// asked for, or "".
	last string
	// others maps the profile hash of each such run on another commit to
	// that commit.
	others map[string]string
	// byHash maps the profile hash of each such run, on any commit, to
	// the latest run that recorded it.
	byHash map[string]coverRun
}

// readCoverHistory reads the run history under root for gate runs with
// --cover. A line that does not parse, or whose run identifier has
// another form, is skipped, as the history's other readers skip a line
// they cannot read. A history that does not exist holds no run.
func readCoverHistory(root, commit string) (coverHistory, error) {
	h := coverHistory{others: map[string]string{}, byHash: map[string]coverRun{}}
	f, err := os.Open(filepath.Join(root, gateResultsDir, verdictHistoryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return h, nil
	}
	if err != nil {
		return h, fmt.Errorf("read the run history: %w", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line historyLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.TriggerMode != "gate" || !line.Cover || !runIDPattern.MatchString(line.RunID) {
			continue
		}
		if line.CoverProfileSHA256 != "" {
			h.byHash[line.CoverProfileSHA256] = coverRun{runID: line.RunID, commit: line.GitRevision, verdict: line.Verdict}
		}
		if line.GitRevision == commit {
			h.last = line.RunID
		} else if line.CoverProfileSHA256 != "" {
			h.others[line.CoverProfileSHA256] = line.GitRevision
		}
	}
	if err := sc.Err(); err != nil {
		return h, fmt.Errorf("read the run history: %w", err)
	}
	return h, nil
}

// latestCoverGateRun returns the last gate run with --cover on commit,
// with the hash and the tree state its verdict file records, and whether
// one was found. The verdict file of the run it finds must exist and
// parse, because the run history alone does not say what was written.
func latestCoverGateRun(root, commit string) (coverGateRun, bool, error) {
	h, err := readCoverHistory(root, commit)
	if err != nil || h.last == "" {
		return coverGateRun{}, false, err
	}
	return readCoverVerdict(root, h.last)
}

// readCoverVerdict reads the verdict, the hash, and the tree state of gate
// run id from its verdict file under root.
func readCoverVerdict(root, id string) (coverGateRun, bool, error) {
	rerun := "; run `lenny-test gate --tier static,unit,component,contract --cover` again"
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, "gate-"+id+".json"))
	if err != nil {
		return coverGateRun{}, true, fmt.Errorf("read the verdict of gate run %s: %w%s", id, err, rerun)
	}
	var v struct {
		Verdict string `json:"verdict"`
		Gate    struct {
			Dirty              bool     `json:"dirty"`
			CoverProfileSHA256 string   `json:"cover_profile_sha256"`
			RestrictedTo       []string `json:"restricted_to"`
			RerunOf            *struct {
				RunID              string `json:"run_id"`
				CoverProfileSHA256 string `json:"cover_profile_sha256"`
			} `json:"rerun_of"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return coverGateRun{}, true, fmt.Errorf("parse the verdict of gate run %s: %w%s", id, err, rerun)
	}
	run := coverGateRun{runID: id, sha256: v.Gate.CoverProfileSHA256, dirty: v.Gate.Dirty, verdict: v.Verdict, restrictedTo: v.Gate.RestrictedTo}
	if r := v.Gate.RerunOf; r != nil {
		run.rerunOf, run.rerunOfSHA256 = r.RunID, r.CoverProfileSHA256
		if run.rerunOf == "" {
			run.rerunOf = "(none named)"
		}
	}
	return run, true, nil
}

// checkRerunOrigin returns why a `--rerun-failed` run, the latest gate run
// with --cover on commit, does not stand for that commit, or nil when it
// does (change 0172, decision 26). The run it reran must be a gate run on
// the same commit, on a clean tree, that is not itself a rerun, that
// recorded the profile hash the rerun names, and that ran the tiers the
// rerun records, so a rerun cannot carry another commit's profile or a
// wider tier restriction than the run whose tests ran.
func checkRerunOrigin(root, commit string, run coverGateRun) error {
	refuse := func(why string) error {
		return fmt.Errorf("gate run %s, the latest with --cover on %s, reran the failed tests of gate run %s, which %s; %s", run.runID, shortCommit(commit), run.rerunOf, why, coverRerun)
	}
	if !runIDPattern.MatchString(run.rerunOf) {
		return refuse("is not a run identifier")
	}
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, "gate-"+run.rerunOf+".json"))
	if err != nil {
		return refuse(fmt.Sprintf("has no readable verdict (%v)", err))
	}
	var orig verdict
	if err := json.Unmarshal(body, &orig); err != nil || orig.Gate == nil {
		return refuse("has no gate verdict that parses")
	}
	og := orig.Gate
	switch {
	case orig.Trigger.GitRevision != commit:
		return refuse("ran on " + shortCommit(orig.Trigger.GitRevision))
	case og.Dirty:
		return refuse("ran on a tree with uncommitted changes")
	case og.RerunOf != nil:
		return refuse("is itself a rerun")
	case !og.Cover || og.CoverProfileSHA256 == "" || og.CoverProfileSHA256 != run.rerunOfSHA256:
		return refuse("did not record the profile hash the rerun names")
	case !sameTiers(og.RestrictedTo, run.restrictedTo):
		return refuse("ran " + gateRunTiers(coverGateRun{restrictedTo: og.RestrictedTo}) + ", not the tiers the rerun records")
	}
	return nil
}

// sameTiers reports whether two tier restrictions name the same tiers; nil
// (every tier) equals only nil.
func sameTiers(a, b []string) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for _, t := range a {
		if !contains(b, t) {
			return false
		}
	}
	return true
}

// coverRerun is the remedy each refusal of the profile names.
const coverRerun = "run `lenny-test gate --tier static,unit,component,contract --cover` again before coverage --diff"

// checkProfileAgainstGate returns why coverage --diff refuses the profile
// at path, whose content is body, for the checkout at root, or nil with a
// note for the output. requireGate is --require-gate. See the file comment
// for the cases.
func checkProfileAgainstGate(root, path string, body []byte, requireGate bool) (string, error) {
	ctx := context.Background()
	commit, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD to find the gate run that wrote the profile: %w", err)
	}
	changes, err := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", fmt.Errorf("read whether the tree has uncommitted changes: %w", err)
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	h, err := readCoverHistory(root, commit)
	if err != nil {
		return "", err
	}
	if changes != "" && !requireGate {
		if run, ok := h.byHash[got]; ok && run.verdict != verdictstatus.VerdictPass {
			return "", notPassedError(path, run.runID, run.commit, run.verdict)
		}
		return "the tree has uncommitted changes, which no gate run on " + shortCommit(commit) + " describes, so the profile is not checked against a verdict", nil
	}
	if h.last == "" {
		if other, ok := h.others[got]; ok {
			return "", fmt.Errorf("the profile %s is the one a gate run on %s wrote, not on %s; %s", path, shortCommit(other), shortCommit(commit), coverRerun)
		}
		if requireGate {
			return "", fmt.Errorf("--require-gate: no gate run with --cover is recorded for %s in %s; %s", shortCommit(commit), filepath.Join(gateResultsDir, verdictHistoryFile), coverRerun)
		}
		return fmt.Sprintf("no gate run with --cover is recorded for %s, so the profile is not checked against a verdict", shortCommit(commit)), nil
	}
	run, _, err := readCoverVerdict(root, h.last)
	if err != nil {
		return "", err
	}
	if err := refuseGateRun(path, commit, got, run); err != nil {
		return "", err
	}
	if run.rerunOf != "" {
		if err := checkRerunOrigin(root, commit, run); err != nil {
			return "", err
		}
	}
	if requireGate {
		if err := refuseRestrictedRun(commit, run); err != nil {
			return "", err
		}
	}
	note := fmt.Sprintf("the profile is the one gate run %s wrote for %s, which ran %s", run.runID, shortCommit(commit), gateRunTiers(run))
	if run.rerunOf != "" {
		note = fmt.Sprintf("the profile is the one gate run %s recorded for %s, a rerun of the failed tests of gate run %s, which ran %s", run.runID, shortCommit(commit), run.rerunOf, gateRunTiers(run))
	}
	return note, nil
}

// refuseRestrictedRun returns why run, the latest gate run with --cover on
// commit, does not stand for the check before a merge under
// --require-gate: --tier restricted it to tiers that leave out one of
// requiredGateTiers. A run that --tier did not restrict ran every tier of
// the merge gate and is accepted.
func refuseRestrictedRun(commit string, run coverGateRun) error {
	if run.restrictedTo == nil {
		return nil
	}
	var missing []string
	for _, tier := range requiredGateTiers {
		if !contains(run.restrictedTo, tier) {
			missing = append(missing, tier)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return fmt.Errorf("--require-gate: gate run %s, the latest with --cover on %s, ran %s, which leaves out %s; the check before a merge needs a run of %s, so %s",
		run.runID, shortCommit(commit), gateRunTiers(run), strings.Join(missing, ", "), strings.Join(requiredGateTiers, ", "), coverRerun)
}

// gateRunTiers names the tiers run ran, for a note or a refusal. A run
// that --tier did not restrict ran the tiers of the merge gate its
// selection reached, and the verdict records the others as not selected.
func gateRunTiers(run coverGateRun) string {
	if run.restrictedTo == nil {
		return "every tier of the merge gate its selection reached, with no --tier restriction"
	}
	return "the tiers " + strings.Join(run.restrictedTo, ", ")
}

// refuseGateRun returns why the profile at path, whose SHA-256 is got,
// cannot stand for run, the latest gate run with --cover on commit, or nil
// when the run is clean, passed, and wrote that profile.
func refuseGateRun(path, commit, got string, run coverGateRun) error {
	switch {
	case run.dirty:
		return fmt.Errorf("gate run %s, the latest with --cover on %s, ran on a tree with uncommitted changes, so its profile does not describe the commit; %s", run.runID, shortCommit(commit), coverRerun)
	case run.verdict != verdictstatus.VerdictPass:
		return notPassedError(path, run.runID, commit, run.verdict)
	case run.sha256 == "":
		return fmt.Errorf("gate run %s ran with --cover on %s, and its verdict records no hash of the profile; %s", run.runID, shortCommit(commit), coverRerun)
	case got != run.sha256:
		return fmt.Errorf("the profile %s (SHA-256 %s) is not the one gate run %s wrote for %s (SHA-256 %s): a later run rewrote it; %s", path, got[:12], run.runID, shortCommit(commit), run.sha256[:min(12, len(run.sha256))], coverRerun)
	}
	return nil
}

// notPassedError is the refusal of the profile of a gate run whose
// verdict is not PASS (change 0172, decision 24): a run that failed, or
// that could not verify its tiers, does not stand behind its profile. The
// remedy it names depends on the verdict: an UNVERIFIED run has no failing
// test to fix, and its verdict's gate.unverified list names the reason,
// such as a tree with uncommitted changes or a run outside the gate image.
func notPassedError(path, runID, commit, verdict string) error {
	remedy := "fix the failure and " + coverRerun
	switch verdict {
	case "":
		verdict = "missing"
	case verdictstatus.VerdictUnverified:
		remedy = "its gate.unverified list names why; commit any uncommitted changes, run the gate in its container rather than with --here, and " + coverRerun
	}
	return fmt.Errorf("the profile %s comes from gate run %s on %s, whose verdict is %s rather than PASS; %s", path, runID, shortCommit(commit), verdict, remedy)
}
