// SPDX-License-Identifier: MIT

package static

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/lennylabs/lenny/tests/testinfra/schematest"
)

// The change records under changes/ are read by the board and by every agent
// that builds a step, so a step entry that names a missing step, a dependency
// cycle, or a change status its steps contradict misleads all of them at once.
// scripts/changes/changes.py holds the rules the changes README states, and
// these gates run it. The records are contributor tooling rather than a
// specified behavior, so no specification section applies.

// changesToolTimeout bounds one run of the tool. It reads a few hundred small
// markdown files, so a run that reaches this bound is hung rather than slow.
const changesToolTimeout = 120 * time.Second

func runChangesTool(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := schematest.RepoRoot(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatalf("python3 is not on PATH, so the change records cannot be checked: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), changesToolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// diagnosis: a rule of scripts/changes/changes.py no longer behaves as its
// tests state, so check, board, or archive would accept or write records the
// changes README forbids.
func TestChangeRecordToolPassesItsTests(t *testing.T) {
	t.Parallel()
	out, err := runChangesTool(t, "-m", "unittest", "discover", "-s", filepath.Join("scripts", "changes"), "-p", "test_*.py")
	if err != nil {
		t.Fatalf("scripts/changes tests failed: %v\n%s", err, out)
	}
}

// diagnosis: a step entry under changes/ breaks the format, names a step that
// does not exist, closes a dependency cycle, or sits under a change status its
// steps contradict. The output names each problem and its change.
func TestChangeStepEntriesAreValid(t *testing.T) {
	t.Parallel()
	out, err := runChangesTool(t, filepath.Join("scripts", "changes", "changes.py"), "check")
	if err != nil {
		t.Fatalf("changes.py check found problems: %v\n%s", err, out)
	}
}

// diagnosis: changes/BOARD.md no longer matches the step entries and the
// programs, because a status or a dependency changed without regenerating it.
// Run `python3 scripts/changes/changes.py board` and commit the result.
func TestChangeBoardIsCurrent(t *testing.T) {
	t.Parallel()
	out, err := runChangesTool(t, filepath.Join("scripts", "changes", "changes.py"), "board", "--check")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

// diagnosis: the generated plan section of docs/about/status.md no longer
// matches the programs under changes/programs/ and the step statuses, because
// a program or a step status changed without regenerating it. Run
// `python3 scripts/changes/changes.py status-page` and commit the result.
func TestStatusPageIsCurrent(t *testing.T) {
	t.Parallel()
	out, err := runChangesTool(t, filepath.Join("scripts", "changes", "changes.py"), "status-page", "--check")
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
}
