// SPDX-License-Identifier: MIT

package sessionstore

import (
	"context"
	"errors"
	"fmt"
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

// InPostTerminalWindow reports whether s is in its §7.1 post-terminal window
// at now under the window cap: s is terminal, names a pod, has no release
// marker, carries a TerminatedAt, and now is before TerminatedAt plus cap.
//
// A non-positive cap puts no row in the window, so a store or a gate built
// without a cap counts no terminal row as holding its pod. A terminal row
// with a zero TerminatedAt is outside the window: every terminal mutator
// stamps it through MarkTerminated, and counting a row with no stamp would
// hold its pod with no end.
//
// A session holds pod P when its PodAssignment is P and it is non-terminal or
// InPostTerminalWindow reports true. The gateway passes the terminal pipeline
// budget as cap; the warm-pool controller passes claimOrphanTimeout, which
// §5.2 keeps above that budget.
//
// spec: §7.1 (Post-terminal window)
func InPostTerminalWindow(s Session, now time.Time, cap time.Duration) bool {
	if cap <= 0 || !session.IsTerminal(s.State) {
		return false
	}
	if s.PodAssignment == "" || !s.PodReleasedAt.IsZero() || s.TerminatedAt.IsZero() {
		return false
	}
	return now.Before(s.TerminatedAt.Add(cap))
}

// HoldsPod reports whether s holds the pod its row names at now under the
// window cap: s names a pod and is non-terminal, or it is in its post-terminal
// window. It is the in-memory form of the two-arm predicate the Postgres
// store's slot queries run.
//
// spec: §7.1 (Post-terminal window), §5.2 (Slot assignment atomicity)
func HoldsPod(s Session, now time.Time, cap time.Duration) bool {
	if s.PodAssignment == "" {
		return false
	}
	return !session.IsTerminal(s.State) || InPostTerminalWindow(s, now, cap)
}

// MarkPodReleased returns the Update mutator that records the §7.1 release
// marker: it sets PodReleasedAt to now on a terminal row that records no
// release yet. On a non-terminal row, or a row that already records a
// release, it returns ErrSuperseded and changes nothing, so a second release
// writer, or a writer racing a resume that left the row non-terminal, writes
// nothing.
//
// The replica that released the session's binding, or reclaimed its
// pre-running claim, runs it in one Update once that release has returned,
// whatever the release's error.
//
// spec: §7.1 (Post-terminal window)
func MarkPodReleased(now time.Time) func(*Session) error {
	return func(r *Session) error {
		if !session.IsTerminal(r.State) || !r.PodReleasedAt.IsZero() {
			return ErrSuperseded
		}
		r.PodReleasedAt = now
		return nil
	}
}

// RowUpdater is the one store method RecordPodRelease needs.
type RowUpdater interface {
	Update(ctx context.Context, tenantID, id string, mutate func(*Session) error) (Session, error)
}

// RecordPodRelease records the §7.1 release marker on the session row with
// MarkPodReleased(now) in one Update. The write runs on a context detached
// from ctx's cancellation and bounded by DefaultPrimaryReadTimeout, because
// the release it records already ran and the caller's context may be the one
// whose end cut it. ErrNotFound (the row was deleted) and ErrSuperseded (the
// row is not terminal, or a release is already recorded) return nil; any
// other store error is returned wrapped. A row whose marker write fails stays
// in its post-terminal window until the cap.
//
// spec: §7.1 (Post-terminal window)
func RecordPodRelease(ctx context.Context, store RowUpdater, tenantID, sessionID string, now time.Time) error {
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultPrimaryReadTimeout)
	defer cancel()
	_, err := store.Update(writeCtx, tenantID, sessionID, MarkPodReleased(now))
	if err == nil || errors.Is(err, ErrNotFound) || errors.Is(err, ErrSuperseded) {
		return nil
	}
	return fmt.Errorf("record the pod release of session %s: %w", sessionID, err)
}
