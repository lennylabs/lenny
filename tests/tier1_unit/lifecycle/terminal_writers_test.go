// SPDX-License-Identifier: MIT

// Package lifecycle_test runs the terminal-writer and resume-attempt cell
// tables, and the lifecycle state machine, on the lifecycle harness over the
// in-memory session store.
package lifecycle_test

import (
	"testing"

	"github.com/lennylabs/lenny/tests/testinfra/lifecycleharness"
)

// TestTerminalWriterCells_spec_7_2 runs every terminal writer row the harness
// drives through its production caller on every pre-state it reaches, with
// the binding on the writer's replica and on a peer replica. In each cell the
// writer writes its terminal state exactly when its guard admits the
// pre-state, and otherwise writes no terminal state; the invariant checker
// then holds.
//
// spec: §7.2 (Interactive Session Model), §8.8 (TaskRecord and TaskResult
// Schema), §10.1.5 (Stale Replica Behavior)
func TestTerminalWriterCells_spec_7_2(t *testing.T) {
	lifecycleharness.RunTerminalWriterCells(t, nil)
}
