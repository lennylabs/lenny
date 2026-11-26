// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
)

// TestTerminalPipelinePendingTable pins the pipeline-pending predicate over
// state, PodAssignment, PodReleasedAt, TerminalPipelineDoneAt, TerminatedAt
// (zero, inside, at, and past the budget), and the budget (zero, negative,
// and positive). A session is pending with or without a pod and whether or
// not its release is recorded, until its mark is set or the budget elapses.
//
// spec: §7.1 (Pipeline-pending session)
func TestTerminalPipelinePendingTable(t *testing.T) {
	const budget = 5 * time.Minute
	inside := windowNow.Add(-time.Minute)
	at := windowNow.Add(-budget)
	pastBudget := windowNow.Add(-budget - time.Second)
	justInside := windowNow.Add(-budget + time.Nanosecond)
	for _, tc := range []struct {
		name    string
		row     sessionstore.Session
		budget  time.Duration
		pending bool
	}{
		{"pod, no release, no mark", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside}, budget, true},
		{"pod, release recorded, no mark", sessionstore.Session{State: session.StateFailed, PodAssignment: "p", TerminatedAt: inside, PodReleasedAt: inside}, budget, true},
		{"no pod, no mark", sessionstore.Session{State: session.StateCancelled, TerminatedAt: inside}, budget, true},
		{"each terminal state counts", sessionstore.Session{State: session.StateExpired, TerminatedAt: inside}, budget, true},
		{"mark set with a pod", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside, PodReleasedAt: inside, TerminalPipelineDoneAt: inside}, budget, false},
		{"mark set without a pod", sessionstore.Session{State: session.StateCompleted, TerminatedAt: inside, TerminalPipelineDoneAt: inside}, budget, false},
		{"mark set with the binding still held", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: inside, TerminalPipelineDoneAt: inside}, budget, false},
		{"one nanosecond inside the budget", sessionstore.Session{State: session.StateCompleted, TerminatedAt: justInside}, budget, true},
		{"at the budget is not pending", sessionstore.Session{State: session.StateCompleted, TerminatedAt: at}, budget, false},
		{"past the budget is not pending", sessionstore.Session{State: session.StateCompleted, PodAssignment: "p", TerminatedAt: pastBudget}, budget, false},
		{"zero TerminatedAt is not pending", sessionstore.Session{State: session.StateCompleted}, budget, false},
		{"zero budget pends nothing", sessionstore.Session{State: session.StateCompleted, TerminatedAt: inside}, 0, false},
		{"negative budget pends nothing", sessionstore.Session{State: session.StateCompleted, TerminatedAt: inside}, -time.Minute, false},
		{"non-terminal is not pending", sessionstore.Session{State: session.StateRunning, PodAssignment: "p", TerminatedAt: inside}, budget, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sessionstore.TerminalPipelinePending(tc.row, windowNow, tc.budget); got != tc.pending {
				t.Errorf("TerminalPipelinePending = %v, want %v", got, tc.pending)
			}
		})
	}
}

// TestMarkTerminalPipelineDoneSetsTheMarkOnce pins the pipeline-mark mutator:
// it sets TerminalPipelineDoneAt on a terminal row with no mark, and returns
// ErrSuperseded, changing nothing, on a row that already carries a mark and on
// a non-terminal row. It leaves the release marker alone.
//
// spec: §7.1 (Terminal pipeline context)
func TestMarkTerminalPipelineDoneSetsTheMarkOnce(t *testing.T) {
	first := windowNow
	row := sessionstore.Session{State: session.StateCompleted, TerminatedAt: first.Add(-time.Second)}
	if err := sessionstore.MarkTerminalPipelineDone(first)(&row); err != nil || !row.TerminalPipelineDoneAt.Equal(first) {
		t.Fatalf("first mark = %v, TerminalPipelineDoneAt %v; want nil and %v", err, row.TerminalPipelineDoneAt, first)
	}
	if !row.PodReleasedAt.IsZero() {
		t.Errorf("the pipeline mark set PodReleasedAt = %v, want zero", row.PodReleasedAt)
	}
	if err := sessionstore.MarkTerminalPipelineDone(first.Add(time.Minute))(&row); !errors.Is(err, sessionstore.ErrSuperseded) || !row.TerminalPipelineDoneAt.Equal(first) {
		t.Errorf("second mark = %v, TerminalPipelineDoneAt %v; want ErrSuperseded and the first instant", err, row.TerminalPipelineDoneAt)
	}
	live := sessionstore.Session{State: session.StateRunning}
	if err := sessionstore.MarkTerminalPipelineDone(first)(&live); !errors.Is(err, sessionstore.ErrSuperseded) || !live.TerminalPipelineDoneAt.IsZero() {
		t.Errorf("mark on a running row = %v, TerminalPipelineDoneAt %v; want ErrSuperseded and zero", err, live.TerminalPipelineDoneAt)
	}
}
