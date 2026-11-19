// SPDX-License-Identifier: MIT

package deadlock

import (
	"context"
	"errors"
	"log"
	"time"

	session "github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/platformtools/seamrow"
	"github.com/lennylabs/lenny/pkg/gateway/seam"
	"github.com/lennylabs/lenny/pkg/gateway/session/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/session/state"
)

// TerminalFail returns the FailFunc that drives a deepest blocked task to
// failed with reason deadlock_timeout, as a funnel terminal writer: it
// opens the session core's terminal claim before the write, writes only
// over a non-terminal row that still holds a pending request_input wait in
// waits, records the Terminated fact, and runs the core's terminal
// pipeline exactly when the write applied. With no waits no wait can be
// pending, so the write fails closed to no write.
//
// spec: §8.8 (Subtree deadlock detection), §7.2 (Terminal writes from any
// non-terminal state)
func TerminalFail(sessions sessionstore.Store, terminals seam.Terminals, waits *inputwait.Registry, now func() time.Time) FailFunc {
	if now == nil {
		now = time.Now
	}
	stillPending := func(sessionID string) bool {
		return waits != nil && len(waits.PendingDetailsForSession(sessionID)) > 0
	}
	return func(ctx context.Context, tenantID, sessionID string) bool {
		end := terminals.BeginTerminal(sessionID)
		defer end()
		var fromState session.State
		at := now()
		updated, err := sessions.Update(ctx, tenantID, sessionID, func(s *sessionstore.Session) error {
			if session.IsTerminal(s.State) || !stillPending(sessionID) {
				return sessionstore.ErrSuperseded
			}
			fromState = s.State
			s.State = session.StateFailed
			s.FailureClass = session.FailureClassRuntime
			s.FailureReason = string(session.FailureDeadlockTimeout)
			sessionstore.MarkTerminated(s, fromState, at)
			return nil
		})
		if errors.Is(err, sessionstore.ErrSuperseded) {
			return false
		}
		if err != nil {
			log.Printf("lenny-gateway: §8.8 deadlock-timeout fail %s: %v", sessionID, err)
			return false
		}
		// A deadlock-timeout fails a blocked running session, so fromState
		// routes its teardown through the executor recycle path rather than
		// the pre-running reclaim.
		terminals.OnSessionTerminal(ctx, state.State(fromState), seamrow.Record(updated))
		return true
	}
}
