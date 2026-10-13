// SPDX-License-Identifier: MIT

package sessionstore

import (
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
)

// ErrSuperseded is the not-applied sentinel a terminal writer's Update mutator
// returns when the locked row fails the writer's guard, for every writer that
// has no package sentinel of its own. The mutator changes nothing when it
// returns it, and the caller treats errors.Is(err, ErrSuperseded) as no change:
// it counts nothing, notifies nothing, runs no terminal hook, and continues.
// spec: §7.2 (Interactive Session Model), §12.3 (Postgres HA Requirements)
var ErrSuperseded = errors.New("sessionstore: write superseded on the locked row")

// MarkTerminated records the Terminated fact in the same write that sets a
// terminal state. Every terminal mutator calls it after it sets the terminal
// state; a Create of a terminal row calls it on the row before the Create with
// from set to the empty state.
//
// On a terminal r it sets TerminatedAt to now when TerminatedAt is zero, and
// sets TerminatedReason from r.State (Completed, Failed, Cancelled, or
// Expired), so a relabel to failed keeps the first TerminatedAt and rewrites
// the reason. On any other r it changes nothing.
//
// from is the locked row's state before the mutator set the terminal state.
// When it is resuming, whatever the terminal state, MarkTerminated increments
// r.CoordinationGeneration by one and leaves RecoveryGeneration unchanged, so
// the snapshot-close bump is part of the terminal write's own transaction and a
// stale coordinator's later RPC fails its fence. No other source state bumps.
//
// spec: §7.2 (Interactive Session Model), §8.8 (TaskRecord and TaskResult Schema)
func MarkTerminated(r *Session, from session.State, now time.Time) {
	reason := terminatedReason(r.State)
	if reason == "" {
		return
	}
	if r.TerminatedAt.IsZero() {
		r.TerminatedAt = now
	}
	r.TerminatedReason = reason
	if from == session.StateResuming {
		r.CoordinationGeneration++
	}
}

// terminatedReason maps a terminal state to the coded TerminatedReason, and a
// non-terminal state to "".
func terminatedReason(st session.State) string {
	switch st {
	case session.StateCompleted:
		return "Completed"
	case session.StateFailed:
		return "Failed"
	case session.StateCancelled:
		return "Cancelled"
	case session.StateExpired:
		return "Expired"
	default:
		return ""
	}
}
