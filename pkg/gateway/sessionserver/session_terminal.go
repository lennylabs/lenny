// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// expireSession transitions a session to the §7.3 terminal `expired`
// state and runs the same archive / terminal-lifecycle teardown as
// failSession. The §8.10 tree-recovery driver uses it for a node whose
// individual `maxResumeWindowSeconds` elapsed before recovery reached
// it (spec: §8.10 — "that node transitions to `expired`").
func (s *Server) expireSession(ctx context.Context, tenantID, sessionID string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateExpired
		return nil
	})
	if err == nil {
		s.archiveSettledChild(ctx, updated)
		s.emitTerminalLifecycle(ctx, updated)
	}
}

// failSession marks a session row failed after a start-path error. The
// update is best-effort: the start handler has already chosen the HTTP
// error it returns to the client, so a store failure here cannot change
// the reply. The write is unconditional, because its callers (the /start
// path and tree recovery) serialize through their own mechanisms; the
// finalize handler uses failFinalizing instead, which admits only a
// `finalizing` row.
func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		row.State = session.StateFailed
		return nil
	})
	if err == nil {
		s.afterFailed(ctx, updated)
	}
}

// afterFailed runs the terminal tail of a `failed` write: it archives a
// settled child to the §8.10 session_tree_archive so a resumed parent can
// replay the outcome, and emits the terminal lifecycle. A failed session is
// a terminal transition, so it emits the same status_change and
// session_complete SSE events, the session.failed audit event, and the
// retention-window roll as any other terminal path. The heavier
// seal/executor-close teardown stays in recordSessionCompleted, because a
// start-path or finalize failure never bound a workspace to seal.
// failSession and failFinalizing share it.
// spec: §7.2, §11.7, §7.1
func (s *Server) afterFailed(ctx context.Context, updated sessionstore.Session) {
	s.archiveSettledChild(ctx, updated)
	s.emitTerminalLifecycle(ctx, updated)
}

// finalizingPrecondition refuses a finalize exit write unless the locked row
// is still `finalizing`. Another writer (for example terminate, DELETE, admin
// force-terminate, the finalizing watchdog, or the orphan session reconciler)
// can move the row to a terminal state while the prepare phase runs; the exit
// write must not overwrite that state. The refusal reuses the §15.1
// precondition error so writePreconditionError renders the 409 with the
// locked state. EndpointFinalize has no capability-gated states, so passing
// nil capabilities to AllowedStates yields the complete allowed set.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states)
func finalizingPrecondition(row *sessionstore.Session) error {
	if row.State == session.StateFinalizing {
		return nil
	}
	return &session.PreconditionError{
		Endpoint:      session.EndpointFinalize,
		CurrentState:  row.State,
		AllowedStates: session.AllowedStates(session.EndpointFinalize, nil),
	}
}

// failFinalizing marks a finalizing session failed, but only while the locked
// row is still `finalizing`. A lost write returns *session.PreconditionError
// and emits nothing, so a terminal state another writer committed is kept and
// no second terminal lifecycle is emitted. Any other store error is returned
// unchanged, and the caller decides the response.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states)
func (s *Server) failFinalizing(ctx context.Context, tenantID, id string) error {
	updated, err := s.store.Update(ctx, tenantID, id, func(row *sessionstore.Session) error {
		if err := finalizingPrecondition(row); err != nil {
			return err
		}
		row.State = session.StateFailed
		return nil
	})
	if err != nil {
		return err
	}
	s.afterFailed(ctx, updated)
	return nil
}
