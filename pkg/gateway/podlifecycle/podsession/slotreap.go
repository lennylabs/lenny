// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// errNoSlotCounter reports a reap on a binder with no slot counter wired.
var errNoSlotCounter = errors.New("podsession: no slot counter is wired")

// ReapStaleSlots releases the stale slot set members sessionIDs name on
// sandboxName, for the stale-slot reaper. It dials the pod's adapter once,
// as a slot bind does, and for each identifier calls wait, sends the
// unconditional Shutdown, and removes the member with SlotCounter.Reap only
// when that Shutdown returned no error and reported a clean exit. A Shutdown
// that reports an unclean exit means the session's runtime may still run
// (another teardown in flight, or the last one unclean), so the member is
// kept and stays counted. It writes no claim and runs no occupancy-zero
// disposition, because Reap never empties the set.
//
// Each Shutdown and each Reap is bounded by the §5.2 per-slot cleanup floor
// on a context derived from ctx, so a pass that ends (a lost Lease, a
// process stop) keeps every member it has not reached. It returns the count
// of members Reap removed and the first error; a wait error ends the call.
// spec: §5.2 (Slot assignment atomicity).
func (b *Binder) ReapStaleSlots(ctx context.Context, sandboxName string, sessionIDs []string, wait func(context.Context) error) (released int, err error) {
	if b.SlotCounter == nil {
		return 0, errNoSlotCounter
	}
	_, cl, _, err := b.dialAndNegotiate(ctx, sandboxName)
	if err != nil {
		return 0, fmt.Errorf("podsession: reap stale slots on %s: %w", sandboxName, err)
	}
	defer cl.Close()
	var firstErr error
	for _, id := range sessionIDs {
		if werr := wait(ctx); werr != nil {
			return released, firstNonNil(firstErr, fmt.Errorf("podsession: reap stale slots on %s: %w", sandboxName, werr))
		}
		removed, rerr := b.reapStaleSlot(ctx, cl, sandboxName, id)
		if rerr != nil && firstErr == nil {
			firstErr = rerr
		}
		if removed {
			released++
		}
	}
	return released, firstErr
}

// reapStaleSlot tears one stale slot down and removes its member. It reports
// whether Reap removed the member.
func (b *Binder) reapStaleSlot(ctx context.Context, cl *adapterclient.Client, sandboxName, sessionID string) (bool, error) {
	callCtx, cancel := context.WithTimeout(ctx, slotCleanupFloor)
	clean, err := cl.Shutdown(callCtx, sessionID, "", 0)
	cancel()
	if err != nil {
		return false, fmt.Errorf("podsession: reap shutdown of session %s on %s: %w", sessionID, sandboxName, err)
	}
	if !clean {
		slog.Info("stale slot kept: the teardown reported an unclean exit",
			"pod", sandboxName, "session_id", sessionID)
		return false, nil
	}
	callCtx, cancel = context.WithTimeout(ctx, slotCleanupFloor)
	defer cancel()
	removed, err := b.SlotCounter.Reap(callCtx, sandboxName, sessionID)
	if err != nil {
		return false, fmt.Errorf("podsession: reap member %s on %s: %w", sessionID, sandboxName, err)
	}
	return removed, nil
}

// firstNonNil returns a when it is non-nil, and b otherwise.
func firstNonNil(a, b error) error {
	if a != nil {
		return a
	}
	return b
}
