// SPDX-License-Identifier: MIT

package watchdog

import (
	"context"
	"errors"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// errSuspendedSuperseded is the sentinel the suspended-lifetime mutator
// returns when the locked row is no longer the listed suspended row: it left
// `suspended`, or it re-entered `suspended` with a new SuspendedAt. The write
// changes nothing and the sweep runs no expiry side effect.
// spec: §6.2 (Pod State Machine).
var errSuspendedSuperseded = errors.New("watchdog: suspended-session expiry superseded on the locked row")

// sweepSuspended lists the tenant's `suspended` rows and, for each row in
// order:
//
//  1. Lifetime. When the row has been suspended for
//     MaxSuspendedSessionSeconds, a guarded write expires it with
//     `expired:idle` and the `max_idle_time` expiry reason, and the sweep does
//     nothing more for the row.
//  2. Podless row. When a BacklogResumer is wired and the row holds no pod,
//     the sweep does nothing more: the gateway leader's backlog re-check owns
//     a podless row's held messages.
//  3. Held pod. The backlog check runs first; when it started a resume, or
//     could not read the backlog, the release waits. Otherwise a row that
//     names a pod and whose hold window ended is handed to the suspended-pod
//     releaser. An idle suspension names no pod, so step 2 takes it.
//
// Every hook returns at once, so a tick never waits on a delivery, a
// checkpoint, or a release. A listing error and a failed lifetime write are
// collected, and the sweep goes on to the next row.
//
// spec: §6.2 (Pod State Machine), §7.2 (Message delivery routing — seven
// paths, path 6)
func (w *Watchdog) sweepSuspended(ctx context.Context, tenant string, now time.Time, res *Result) error {
	rows, err := w.store.List(ctx, tenant, sessionstore.ListFilter{State: session.StateSuspended})
	if err != nil {
		return listError(tenant, session.StateSuspended, err)
	}
	hold := time.Duration(w.cfg.MaxSuspendedPodHoldSeconds) * time.Second
	var errs []error
	for _, row := range rows {
		due, err := w.expireSuspendedIfDue(ctx, tenant, row, now, res)
		if err != nil {
			errs = append(errs, err)
		}
		if due {
			continue
		}
		if w.backlog != nil && !w.backlog.HoldsPod(row) {
			continue
		}
		if w.backlog != nil && w.backlog.ResumeBacklog(ctx, row) {
			continue
		}
		if row.PodAssignment != "" && now.Sub(suspendedSince(row)) >= hold && w.releaser != nil {
			w.releaser.ReleaseSuspendedPod(ctx, row)
		}
	}
	return errors.Join(errs...)
}

// suspendedSince is the instant the row entered `suspended`: SuspendedAt, or
// StateEnteredAt for a row that carries none.
// spec: §6.2 (Pod State Machine).
func suspendedSince(row sessionstore.Session) time.Time {
	if row.SuspendedAt.IsZero() {
		return row.StateEnteredAt
	}
	return row.SuspendedAt
}

// expireSuspendedIfDue applies the §6.2 suspended-session lifetime to one
// listed row and reports whether the lifetime was due, in which case the
// sweep does nothing more for the row this tick, whatever the write's
// outcome. The write is a terminal writer on the funnel path: it takes the
// terminal claim, guards on the locked row still being `suspended` with the
// listed SuspendedAt, and on success notifies the `max_idle_time` expiry and
// hands the claim to dispatchTerminal, whose goroutine runs the terminal hook
// and then ends the claim. A guard miss or a missing row changes nothing and
// ends the claim at once. A store error ends the claim at once, is logged
// with the tenant and session IDs, and is returned for the pass to collect;
// the next tick retries a write that did not apply.
//
// spec: §6.2 (Pod State Machine), §16.1.1 (Attribute Naming)
func (w *Watchdog) expireSuspendedIfDue(ctx context.Context, tenant string, row sessionstore.Session, now time.Time, res *Result) (bool, error) {
	lifetime := time.Duration(w.cfg.MaxSuspendedSessionSeconds) * time.Second
	if now.Sub(suspendedSince(row)) < lifetime {
		return false, nil
	}
	end := w.beginTerminal(row.ID)
	updated, err := w.store.Update(ctx, tenant, row.ID, func(r *sessionstore.Session) error {
		if r.State != session.StateSuspended || !r.SuspendedAt.Equal(row.SuspendedAt) {
			return errSuspendedSuperseded
		}
		r.State = session.StateExpired
		r.FailureReason = string(session.FailureExpiredIdle)
		// spec: §5.2 (Slot assignment atomicity) — the stamp reads the clock
		// inside this row's Update, because the dispatched hooks of earlier
		// rows, seal included, may have run past the pass's now.
		sessionstore.MarkTerminated(r, session.StateSuspended, w.clock())
		return nil
	})
	if superseded(err, errSuspendedSuperseded) {
		end()
		return true, nil
	}
	if err != nil {
		end()
		return true, rowError(tenant, row.ID, "expire a suspended session at its lifetime", err)
	}
	res.Expirations++
	res.SuspendedExpirations++
	w.notifySessionExpiry(ctx, updated, ExpiryReasonMaxIdleTime)
	w.dispatchTerminal(ctx, end, func(hctx context.Context) {
		w.recordCompleted(hctx, session.StateSuspended, updated)
	})
	return true, nil
}
