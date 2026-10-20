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
// The Sandbox read, the adapter dial, and the version negotiation share one
// bound of the §5.2 per-slot cleanup floor, and each Shutdown and each Reap
// has its own, all on contexts derived from ctx. An adapter that accepts the
// connection and never answers therefore keeps that pod's members for the
// next pass rather than holding the pass open, and a pass that ends (a lost
// Lease, a process stop) keeps every member it has not reached.
//
// It returns the count of members Reap removed and an error joining every
// failure of the call: a member's failure is a *StaleSlotError naming that
// member's session, so the caller can log each one against the session that
// produced it. A wait error ends the call.
// spec: §5.2 (Slot assignment atomicity).
func (b *Binder) ReapStaleSlots(ctx context.Context, sandboxName string, sessionIDs []string, wait func(context.Context) error) (released int, err error) {
	if b.SlotCounter == nil {
		return 0, errNoSlotCounter
	}
	cl, err := b.dialForReap(ctx, sandboxName)
	if err != nil {
		return 0, fmt.Errorf("podsession: reap stale slots on %s: %w", sandboxName, err)
	}
	defer cl.Close()
	var errs []error
	for _, id := range sessionIDs {
		if werr := wait(ctx); werr != nil {
			errs = append(errs, fmt.Errorf("podsession: reap stale slots on %s: %w", sandboxName, werr))
			return released, errors.Join(errs...)
		}
		removed, rerr := b.reapStaleSlot(ctx, cl, sandboxName, id)
		if rerr != nil {
			errs = append(errs, &StaleSlotError{sessionID: id, err: rerr})
		}
		if removed {
			released++
		}
	}
	return released, errors.Join(errs...)
}

// dialForReap resolves the Sandbox, dials its adapter, and negotiates the
// protocol version under one §5.2 per-slot cleanup floor. The returned
// client outlives the bound: each later RPC carries its own deadline.
// spec: §5.2 (Slot assignment atomicity).
func (b *Binder) dialForReap(ctx context.Context, sandboxName string) (*adapterclient.Client, error) {
	dialCtx, cancel := context.WithTimeout(ctx, slotCleanupFloor)
	defer cancel()
	_, cl, _, err := b.dialAndNegotiate(dialCtx, sandboxName)
	return cl, err
}

// StaleSlotError is one stale member's release failure: its Shutdown or its
// Reap failed, and the member stays in the set for the next pass.
type StaleSlotError struct {
	sessionID string
	err       error
}

// Error returns the wrapped failure's text, which names the session and pod.
func (e *StaleSlotError) Error() string { return e.err.Error() }

// Unwrap returns the wrapped failure.
func (e *StaleSlotError) Unwrap() error { return e.err }

// SessionID names the session whose member the failure kept.
func (e *StaleSlotError) SessionID() string { return e.sessionID }

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
