// SPDX-License-Identifier: MIT

package admin

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/session/sessionstore"
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
// runs on a context detached from ctx's cancellation and starts this
// replica's terminal pipeline for the session, which runs detached; the
// drain does not wait for that pipeline and moves to the next session.
// A caller's deadline stops the drain between sessions and never inside
// one session's transition; when ctx has ended before the next
// session, the drain publishes what it transitioned and returns the
// partial result and ctx's error, wrapped. A second primary listing then verifies the scope and records
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
//     transition writes the terminal state on the session row and starts
//     this replica's terminal pipeline, whose post-terminal hook releases
//     the bindings this replica holds after the drain has moved on. After its transitions the drain
//     publishes the identifiers of the sessions it moved to a terminal
//     state through pods, the binding-release fan-out (CH-BINDRELEASE),
//     and each peer replica's subscriber releases the bindings of those
//     sessions it holds. The publish runs on a context detached from
//     ctx's cancellation, it is not redelivered when lost, and its
//     outcome never changes the result or the returned error. A nil pods
//     publishes nothing.
//   - The verifying listing checks the creation-fence invariant. It can
//     report, in Remaining, a session legitimately created after a
//     concurrent tenant resume cleared the marker.
//
// spec: §12.8 (Lifecycle fence and drain: Drain), §12.3 (Read replicas),
// §24.11, §11.4 (User Invalidation), §28.5.7 (Gateway-to-store).
func DrainSessions(ctx context.Context, l DrainLister, t DrainTerminator, pods UserPodTerminator, tenantID, userID string) (DrainResult, error) {
	filter := sessionstore.ListFilter{UserID: userID, Primary: true}
	var res DrainResult
	rows, err := l.List(ctx, tenantID, filter)
	if err != nil {
		return res, fmt.Errorf("drain sessions of tenant %s: list: %w", tenantID, err)
	}
	failed := make(map[string]bool)
	var transitionedIDs []string
	var cerr error
	for _, row := range rows {
		if session.IsTerminal(row.State) {
			continue
		}
		if cerr = ctx.Err(); cerr != nil {
			break
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
			transitionedIDs = append(transitionedIDs, row.ID)
		}
	}
	publishBindRelease(ctx, pods, tenantID, userID, transitionedIDs)
	if cerr != nil {
		return res, fmt.Errorf("drain sessions of tenant %s: %w", tenantID, cerr)
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

// publishBindRelease hands the identifiers of the sessions the drain
// transitioned to the binding-release fan-out, on a context detached from
// ctx's cancellation so a caller's deadline that ended the loop still
// publishes what was transitioned. It does nothing for a nil pods or an
// empty ids. The fan-out's result is discarded: the drain's outcome is the
// verifying listing's, and a lost publish is not redelivered.
// spec: §12.8 (Lifecycle fence and drain: Drain), §28.5.7 (Gateway-to-store).
func publishBindRelease(ctx context.Context, pods UserPodTerminator, tenantID, userID string, ids []string) {
	if pods == nil || len(ids) == 0 {
		return
	}
	pods.TerminateUserSessions(context.WithoutCancel(ctx), tenantID, userID, ids)
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

// ErrPipelineBudgetUnset is returned by TerminalPipelinesPending when it is
// given no positive terminal pipeline budget. It lists nothing, so an unwired
// budget fails closed: the erasure job waits and tenant deletion pauses rather
// than delete a session's stores while its terminal pipeline may still write.
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Pipeline-pending session).
var ErrPipelineBudgetUnset = errors.New("terminal pipeline budget is not set")

// DefaultPipelineWaitMargin is the default of the pipeline wait margin that
// user erasure and tenant deletion add to the terminal pipeline budget, the
// gateway's --terminal-pipeline-wait-margin.
// spec: §12.8 (Lifecycle fence and drain)
const DefaultPipelineWaitMargin = 30 * time.Second

// ErrPipelineWaitMarginInvalid is returned by TerminalPipelinesPending when
// it is given a negative pipeline wait margin, or one so large that the
// budget plus the margin overflows. A negative margin would end the wait
// before the budget that bounds the pipeline's writes, and an overflowed sum
// is negative, which the predicates read as no wait at all, so the gate lists
// nothing and fails closed, as an unset budget does.
// spec: §12.8 (Lifecycle fence and drain)
var ErrPipelineWaitMarginInvalid = errors.New("terminal pipeline wait margin is negative or overflows the budget")

// PipelineWaitMarginValid reports whether margin is a pipeline wait margin
// TerminalPipelinesPending accepts with budget: not negative, and small
// enough that budget plus margin does not overflow.
// spec: §12.8 (Lifecycle fence and drain)
func PipelineWaitMarginValid(budget, margin time.Duration) bool {
	return margin >= 0 && budget+margin >= budget
}

// TerminalPipelinesPending returns the identifiers of the sessions of one
// lifecycle scope (the whole tenant when userID is empty, otherwise the
// sessions userID owns in tenantID) that user erasure and tenant deletion
// wait on at now: a session that is pipeline-pending
// (sessionstore.TerminalPipelinePending), with or without a pod, or in its
// post-terminal window (sessionstore.InPostTerminalWindow), both read under
// the terminal pipeline budget plus the pipeline wait margin.
//
// The margin covers clock skew between the replica that ran a session's
// pipeline, whose context deadline is the budget counted on its clock, and
// the replica that runs this check on its own clock, and a pipeline write
// that commits just after that deadline. It extends this wait alone: the
// session store's slot queries, the retention collector, and the pipeline's
// own context read the budget without it.
//
// It lists the scope from the Postgres primary with the filter DrainSessions
// uses, so a pipeline mark or a release recorded on the primary is seen. User
// erasure and tenant deletion call it after their drain and delete no store of
// the scope while it returns an identifier: a pipeline still running writes
// the session's billing event, quota checkpoint, and session log after the
// pod release, and a seal still running would write under a prefix the
// deletion already cleared. Both arms end within one budget plus the margin
// of the terminal transition. A listing error is returned wrapped; a
// non-positive budget returns ErrPipelineBudgetUnset and an invalid margin
// (PipelineWaitMarginValid) returns ErrPipelineWaitMarginInvalid, both
// without listing.
//
// spec: §12.8 (Lifecycle fence and drain), §7.1 (Pipeline-pending session),
// §7.1 (Post-terminal window)
func TerminalPipelinesPending(ctx context.Context, l DrainLister, tenantID, userID string, now time.Time, budget, margin time.Duration) ([]string, error) {
	if budget <= 0 {
		return nil, ErrPipelineBudgetUnset
	}
	if !PipelineWaitMarginValid(budget, margin) {
		return nil, ErrPipelineWaitMarginInvalid
	}
	rows, err := l.List(ctx, tenantID, sessionstore.ListFilter{UserID: userID, Primary: true})
	if err != nil {
		return nil, fmt.Errorf("list the terminal sessions of tenant %s: %w", tenantID, err)
	}
	wait := budget + margin
	var pending []string
	for _, row := range rows {
		if sessionstore.TerminalPipelinePending(row, now, wait) || sessionstore.InPostTerminalWindow(row, now, wait) {
			pending = append(pending, row.ID)
		}
	}
	return pending, nil
}
