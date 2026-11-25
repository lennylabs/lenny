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
)

// This file ties `lenny-test coverage --diff` to the gate run that wrote
// the profile it reads (change 0172, decision 18). A gate run with --cover
// records the SHA-256 of the profile it wrote as gate.cover_profile_sha256
// in its verdict and its line of the run history. Before it measures,
// coverage --diff finds the latest gate run with --cover on the commit
// HEAD names and refuses a profile whose hash differs from that run's, so
// the check before a merge of decision 8 cannot read a profile that a
// later run, or another commit's run, wrote. When no gate run with --cover
// on the commit is recorded, coverage --diff measures the profile as it
// did before, because `lenny-test run --changed` and `lenny-test --tier
// unit` also write it. The harness is contributor tooling, so this file
// carries no specification annotation.

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
}

// historyLine is the part of a run history line latestCoverGateRun reads.
type historyLine struct {
	RunID       string `json:"run_id"`
	TriggerMode string `json:"trigger_mode"`
	GitRevision string `json:"git_revision"`
	Cover       bool   `json:"cover"`
}

// latestCoverGateRun reads the run history under root and returns the
// last gate run with --cover on commit, with the hash its verdict file
// records, and whether one was found. A line that does not parse is
// skipped, as the history's other readers skip it. A history that does
// not exist holds no run. The verdict file of the run it finds must exist
// and parse, because the run history alone does not say what was written.
func latestCoverGateRun(root, commit string) (coverGateRun, bool, error) {
	f, err := os.Open(filepath.Join(root, gateResultsDir, verdictHistoryFile))
	if errors.Is(err, fs.ErrNotExist) {
		return coverGateRun{}, false, nil
	}
	if err != nil {
		return coverGateRun{}, false, fmt.Errorf("read the run history: %w", err)
	}
	defer f.Close()
	var last string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line historyLine
		if json.Unmarshal(sc.Bytes(), &line) != nil {
			continue
		}
		if line.TriggerMode == "gate" && line.Cover && line.GitRevision == commit && line.RunID != "" {
			last = line.RunID
		}
	}
	if err := sc.Err(); err != nil {
		return coverGateRun{}, false, fmt.Errorf("read the run history: %w", err)
	}
	if last == "" {
		return coverGateRun{}, false, nil
	}
	body, err := os.ReadFile(filepath.Join(root, gateResultsDir, "gate-"+last+".json"))
	if err != nil {
		return coverGateRun{}, true, fmt.Errorf("read the verdict of gate run %s: %w", last, err)
	}
	var v struct {
		Gate struct {
			CoverProfileSHA256 string `json:"cover_profile_sha256"`
		} `json:"gate"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		return coverGateRun{}, true, fmt.Errorf("parse the verdict of gate run %s: %w", last, err)
	}
	return coverGateRun{runID: last, sha256: v.Gate.CoverProfileSHA256}, true, nil
}

// checkProfileAgainstGate returns why coverage --diff refuses the profile
// at path, whose content is body, for the checkout at root, or nil. It
// refuses a profile whose SHA-256 differs from the one the latest gate run
// with --cover on HEAD recorded, and a run whose verdict records no hash.
// It returns a note for the output when no such run is recorded.
func checkProfileAgainstGate(root, path string, body []byte) (string, error) {
	commit, err := gitOutput(context.Background(), root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve HEAD to find the gate run that wrote the profile: %w", err)
	}
	run, found, err := latestCoverGateRun(root, commit)
	if err != nil {
		return "", err
	}
	if !found {
		return fmt.Sprintf("no gate run with --cover is recorded for %s, so the profile is not checked against a verdict", shortCommit(commit)), nil
	}
	sum := sha256.Sum256(body)
	got := hex.EncodeToString(sum[:])
	if run.sha256 == "" {
		return "", fmt.Errorf("gate run %s ran with --cover on %s and its verdict records no hash of the profile; run `lenny-test gate --tier static,unit,component,contract --cover` again", run.runID, shortCommit(commit))
	}
	if got != run.sha256 {
		return "", fmt.Errorf("the profile %s (SHA-256 %s) is not the one gate run %s wrote for %s (SHA-256 %s); a later run rewrote it, so run `lenny-test gate --tier static,unit,component,contract --cover` again before coverage --diff", path, got[:12], run.runID, shortCommit(commit), run.sha256[:min(12, len(run.sha256))])
	}
	return fmt.Sprintf("the profile is the one gate run %s wrote for %s", run.runID, shortCommit(commit)), nil
}
