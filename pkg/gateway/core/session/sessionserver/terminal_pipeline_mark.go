// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/session/sessionstore"
)

// recordTerminalPipelineDone writes the pipeline mark on the session row once
// this replica's terminal pipeline for the session has run its last step. User
// erasure and tenant deletion wait while a terminal session of their scope has
// no mark and its terminal pipeline budget has not elapsed, so the billing
// event, the quota checkpoint, and the session-log close that follow the pod
// release land before the user's stores are deleted.
//
// It runs on the pipeline's own context (terminalContext), whatever the
// outcome of the steps before it. When that context has ended, the budget has
// elapsed and already ends the wait, and a write after the budget is what the
// budget arm rules out, so it writes nothing and logs the skip. A missing row
// (an erasure already deleted it) and a row that is not terminal or already
// carries a mark write nothing. Any other store error is logged at WARN and
// leaves the session pending until the budget elapses.
//
// The callers are recordSessionCompleted, after the billing event, and
// AfterTerminalWrite, the whole pipeline of the writers that call the
// post-terminal hook directly.
//
// spec: §7.1 (Terminal pipeline context)
func (s *Server) recordTerminalPipelineDone(ctx context.Context, sess sessionstore.Session) {
	if err := ctx.Err(); err != nil {
		slog.Warn("sessionserver: terminal pipeline mark not recorded; the pipeline ran past its budget",
			"session_id", sess.ID, "error", err)
		return
	}
	_, err := s.store.Update(ctx, sess.TenantID, sess.ID, sessionstore.MarkTerminalPipelineDone(s.clock()))
	if err == nil || errors.Is(err, sessionstore.ErrNotFound) || errors.Is(err, sessionstore.ErrSuperseded) {
		return
	}
	slog.Warn("sessionserver: terminal pipeline mark not recorded; the session stays pending until the budget elapses",
		"session_id", sess.ID, "error", err)
}
