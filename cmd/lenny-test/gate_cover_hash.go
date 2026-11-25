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
)

// This file ties `lenny-test coverage --diff` to the gate run that wrote
// the profile it reads (change 0172, decision 18). A gate run with --cover
// records the SHA-256 of the profile it wrote as gate.cover_profile_sha256
// in its verdict and its line of the run history. Before it measures,
// coverage --diff on a tree without uncommitted changes to tracked files
// finds the latest gate run with --cover on the commit HEAD names and
// refuses a profile whose hash differs from that run's, a run on a dirty
// tree, and a run whose verdict records no hash. When no such run is
// recorded, it refuses a profile whose hash a gate run with --cover on
// another commit recorded, and otherwise measures the profile with a note,
// because `lenny-test run --changed` and `lenny-test --tier unit` also
// write it. On a tree with uncommitted changes the profile comes from the
// developer loop, which no gate verdict on HEAD describes, so the check is
// skipped with a note.
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
}

// historyLine is the part of a run history line the coverage check reads.
type historyLine struct {
	RunID              string `json:"run_id"`
	TriggerMode        string `json:"trigger_mode"`
	GitRevision        string `json:"git_revision"`
	Cover              bool   `json:"cover"`
	CoverProfileSHA256 string `json:"cover_profile_sha256,omitempty"`
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
}

// readCoverHistory reads the run history under root for gate runs with
// --cover. A line that does not parse, or whose run identifier has
// another form, is skipped, as the history's other readers skip a line
// they cannot read. A history that does not exist holds no run.
func readCoverHistory(root, commit string) (coverHistory, error) {
	h := coverHistory{others: map[string]string{}}
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

// readCoverVerdict reads the hash and the tree state of gate run id from
// its verdict file under root.
func readCoverVerdict(root, id string) (coverGateRun, bool, error) {
	rerun := "; run `lenny-test gate --tier static,unit,component,contract --cover` again"
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, "gate-"+id+".json"))
	if err != nil {
		return coverGateRun{}, true, fmt.Errorf("read the verdict of gate run %s: %w%s", id, err, rerun)
	}
	var v struct {
		Gate struct {
			Dirty              bool   `json:"dirty"`
			CoverProfileSHA256 string `json:"cover_profile_sha256"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return coverGateRun{}, true, fmt.Errorf("parse the verdict of gate run %s: %w%s", id, err, rerun)
	}
	return coverGateRun{runID: id, sha256: v.Gate.CoverProfileSHA256, dirty: v.Gate.Dirty}, true, nil
}

// checkProfileAgainstGate returns why coverage --diff refuses the profile
// at path, whose content is body, for the checkout at root, or nil with a
// note for the output. See the file comment for the cases.
func checkProfileAgainstGate(root, path string, body []byte) (string, error) {
	ctx := context.Background()
	commit, err := gitOutput(ctx, root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD to find the gate run that wrote the profile: %w", err)
	}
	changes, err := gitOutput(ctx, root, "status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return "", fmt.Errorf("read whether the tree has uncommitted changes: %w", err)
	}
	if changes != "" {
		return "the tree has uncommitted changes, which no gate run on " + shortCommit(commit) + " describes, so the profile is not checked against a verdict", nil
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	rerun := "run `lenny-test gate --tier static,unit,component,contract --cover` again before coverage --diff"
	h, err := readCoverHistory(root, commit)
	if err != nil {
		return "", err
	}
	if h.last == "" {
		if other, ok := h.others[got]; ok {
			return "", fmt.Errorf("the profile %s is the one a gate run on %s wrote, not on %s; %s", path, shortCommit(other), shortCommit(commit), rerun)
		}
		return fmt.Sprintf("no gate run with --cover is recorded for %s, so the profile is not checked against a verdict", shortCommit(commit)), nil
	}
	run, _, err := readCoverVerdict(root, h.last)
	if err != nil {
		return "", err
	}
	switch {
	case run.dirty:
		return "", fmt.Errorf("gate run %s, the latest with --cover on %s, ran on a tree with uncommitted changes, so its profile does not describe the commit; %s", run.runID, shortCommit(commit), rerun)
	case run.sha256 == "":
		return "", fmt.Errorf("gate run %s ran with --cover on %s, and its verdict records no hash of the profile; %s", run.runID, shortCommit(commit), rerun)
	case got != run.sha256:
		return "", fmt.Errorf("the profile %s (SHA-256 %s) is not the one gate run %s wrote for %s (SHA-256 %s): a later run rewrote it; %s", path, got[:12], run.runID, shortCommit(commit), run.sha256[:min(12, len(run.sha256))], rerun)
	}
	return fmt.Sprintf("the profile is the one gate run %s wrote for %s", run.runID, shortCommit(commit)), nil
}
