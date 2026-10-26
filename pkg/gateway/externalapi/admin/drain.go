// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// DrainLister is the drain's listing seam. sessionstore.Store satisfies
// it; the drain always passes a filter with Primary set.
type DrainLister interface {
	List(ctx context.Context, tenantID string, f sessionstore.ListFilter) ([]sessionstore.Session, error)
}

// DrainTerminator is the drain's forced-transition seam. SessionAdmin
// satisfies it: ForceTerminate applies the §24.11 forced terminal
// transition to one session of the named tenant.
type DrainTerminator interface {
	ForceTerminate(ctx context.Context, tenantID, id string) (sessionstore.Session, session.State, bool, error)
}

// DrainResult reports one drain.
type DrainResult struct {
	// Terminated counts the sessions this drain transitioned.
	Terminated int
	// Failed lists the ids whose forced transition returned an error.
	Failed []string
	// Remaining lists the ids the verifying listing found non-terminal,
	// excluding the ids already in Failed.
	Remaining []string
}

// ErrDrainIncomplete is returned, wrapped, when a drain's Failed or
// Remaining list is non-empty. The lifecycle operation that ran the
// drain leaves its marker set and runs the drain again.
var ErrDrainIncomplete = errors.New("session drain incomplete")

// DrainSessions drains the live sessions of one lifecycle scope: the
// whole tenant when userID is empty, otherwise the sessions userID owns
// in tenantID. It is the single drain that tenant suspension, tenant
// deletion, and user erasure run once their marker is committed.
//
// The drain lists the scope from the Postgres primary, applies the
// forced terminal transition to each non-terminal session, and continues
// past a session whose transition fails, recording its id in Failed. A
// session that no longer exists has nothing to drain. Each transition
// runs on a context detached from ctx's cancellation, so a caller's
// deadline stops the drain between sessions and never inside one
// session's terminal pipeline; when ctx has ended before the next
// session, the drain returns the partial result and ctx's error,
// wrapped. A second primary listing then verifies the scope and records
// every non-terminal id not already in Failed in Remaining. A listing
// error returns the wrapped listing error and the partial result. When
// Failed or Remaining is non-empty the drain returns ErrDrainIncomplete,
// wrapped with both counts.
//
// What a nil error means, and what it does not:
//
//   - A nil error means every row of the scope is terminal on the
//     primary when the verifying listing ran.
//   - A nil error does not mean the scope's pods are released. The
//     transition writes the terminal state on the session row and runs
//     this replica's terminal pipeline. The pod of a session bound on
//     another replica is released by that replica's coordination sweeper
//     (ReleaseTerminalBinding) once it lists the terminal row, within the
//     sweeper interval plus maxWorkspaceSealDurationSeconds. When user
//     erasure deletes the rows before that listing, the replica never
//     releases the pod, and orphan GC drains it after claimOrphanTimeout,
//     counted in lenny_orphaned_claims_total, unless another active
//     session references the pod.
//   - The verifying listing checks the creation-fence invariant. It can
//     report, in Remaining, a session legitimately created after a
//     concurrent tenant resume cleared the marker.
//
// spec: §12.8 (Lifecycle fence and drain: Drain), §12.3 (Read replicas),
// §24.11.
func DrainSessions(ctx context.Context, l DrainLister, t DrainTerminator, tenantID, userID string) (DrainResult, error) {
	filter := sessionstore.ListFilter{UserID: userID, Primary: true}
	var res DrainResult
	rows, err := l.List(ctx, tenantID, filter)
	if err != nil {
		return res, fmt.Errorf("drain sessions of tenant %s: list: %w", tenantID, err)
	}
	failed := make(map[string]bool)
	for _, row := range rows {
		if session.IsTerminal(row.State) {
			continue
		}
		if cerr := ctx.Err(); cerr != nil {
			return res, fmt.Errorf("drain sessions of tenant %s: %w", tenantID, cerr)
		}
		_, _, transitioned, terr := t.ForceTerminate(context.WithoutCancel(ctx), tenantID, row.ID)
		switch {
		case errors.Is(terr, sessionstore.ErrNotFound):
			// The row was deleted after the listing: nothing to drain.
		case terr != nil:
			res.Failed = append(res.Failed, row.ID)
			failed[row.ID] = true
		case transitioned:
			res.Terminated++
		}
	}
	if err := verifyDrained(ctx, l, tenantID, filter, failed, &res); err != nil {
		return res, err
	}
	if len(res.Failed) > 0 || len(res.Remaining) > 0 {
		return res, fmt.Errorf("drain sessions of tenant %s: %w: %d failed, %d remaining",
			tenantID, ErrDrainIncomplete, len(res.Failed), len(res.Remaining))
	}
	return res, nil
}

// verifyDrained runs the drain's verifying primary listing and records
// in res.Remaining every non-terminal id not already in failed.
// spec: §12.8 (Lifecycle fence and drain: Drain).
func verifyDrained(ctx context.Context, l DrainLister, tenantID string, filter sessionstore.ListFilter, failed map[string]bool, res *DrainResult) error {
	rows, err := l.List(ctx, tenantID, filter)
	if err != nil {
		return fmt.Errorf("drain sessions of tenant %s: verify: %w", tenantID, err)
	}
	for _, row := range rows {
		if !session.IsTerminal(row.State) && !failed[row.ID] {
			res.Remaining = append(res.Remaining, row.ID)
		}
	}
	return nil
}

// ErrReleaseCapUnset is returned by ReleasePending when it is given no
// positive post-terminal cap. It lists nothing, so an unwired cap fails
// closed: the erasure job waits and tenant deletion pauses rather than
// delete a session's stores while its terminal pipeline may still write.
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Post-terminal window).
var ErrReleaseCapUnset = errors.New("post-terminal window cap is not set")

// ReleasePending returns the identifiers of the sessions of one lifecycle
// scope (the whole tenant when userID is empty, otherwise the sessions
// userID owns in tenantID) that are in their §7.1 post-terminal window at
// now under cap: terminal, naming a pod, with no release recorded, and
// terminated less than cap ago. It lists the scope from the Postgres
// primary with the filter DrainSessions uses, so a release recorded on the
// primary is seen. User erasure and tenant deletion call it after their
// drain and delete no store of the scope while it returns an identifier,
// because a seal still running would write under a prefix the deletion
// already cleared. A listing error is returned wrapped; a non-positive cap
// returns ErrReleaseCapUnset without listing.
//
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Post-terminal window)
func ReleasePending(ctx context.Context, l DrainLister, tenantID, userID string, now time.Time, cap time.Duration) ([]string, error) {
	if cap <= 0 {
		return nil, ErrReleaseCapUnset
	}
	rows, err := l.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
	if err != nil {
		return nil, fmt.Errorf("list the post-terminal sessions of tenant %s: %w", tenantID, err)
	}
	var pending []string
	for _, row := range rows {
		if sessionstore.InPostTerminalWindow(row, now, cap) {
			pending = append(pending, row.ID)
		}
	}
	return pending, nil
}
