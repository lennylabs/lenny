// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// expireSession transitions a session to the §7.3 terminal `expired`
// state. The §8.10 tree-recovery driver uses it for a node whose individual
// `maxResumeWindowSeconds` elapsed before recovery reached it (spec: §8.10 —
// "that node transitions to `expired`"). It is a hook-path terminal writer:
// it opens its terminal claim before its Update, writes only over a
// non-terminal locked row, records the Terminated fact in the write, and on a
// write that applied runs the post-terminal hook with fromState from the
// locked row. A row already terminal is not applied (sessionstore.
// ErrSuperseded) and nothing more runs; a store error is ignored, because its
// one caller, FailNode, returns nothing.
//
// spec: §7.2 (Interactive Session Model), §8.10 (Delegation Tree Recovery)
func (s *Server) expireSession(ctx context.Context, tenantID, sessionID string) {
	end := s.BeginTerminal(sessionID)
	defer end()
	from, updated, err := s.writeTerminal(ctx, tenantID, sessionID, session.StateExpired, nonTerminalGuard)
	if err != nil {
		return
	}
	s.AfterTerminalWrite(ctx, from, updated, s.settledChildSignals)
}

// failSession marks a session row failed. Its caller is §8.10 tree recovery,
// which fails a node whose level or tree budget elapsed, including an
// awaiting_client_action node. It is a hook-path terminal writer with the
// guard expireSession applies: it writes only over a non-terminal locked
// row, records the Terminated fact in the write, and on a write that applied
// runs the post-terminal hook through afterFailed with fromState from the
// locked row. A row already terminal is not applied and nothing more runs; a
// store error is ignored.
//
// spec: §7.2 (Interactive Session Model), §8.10 (Delegation Tree Recovery)
func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	end := s.BeginTerminal(sessionID)
	defer end()
	from, updated, err := s.writeTerminal(ctx, tenantID, sessionID, session.StateFailed, nonTerminalGuard)
	if err != nil {
		return
	}
	s.afterFailed(ctx, from, updated)
}

// nonTerminalGuard is the hook writers' locked-row guard that admits any
// non-terminal state. A row another writer already made terminal is refused,
// so no session runs a second post-terminal hook.
func nonTerminalGuard(r *sessionstore.Session) error {
	if session.IsTerminal(r.State) {
		return sessionstore.ErrSuperseded
	}
	return nil
}

// writeTerminal is the one Update a hook-path terminal writer makes: it
// applies guard to the locked row, captures the locked row's state as the
// pre-terminal state, writes to, and records the Terminated fact in the same
// write. It returns the pre-terminal state and the written row, or the
// guard's sentinel or the store error.
func (s *Server) writeTerminal(ctx context.Context, tenantID, sessionID string, to session.State,
	guard func(*sessionstore.Session) error,
) (session.State, sessionstore.Session, error) {
	var from session.State
	now := s.clock()
	updated, err := s.store.Update(ctx, tenantID, sessionID, func(row *sessionstore.Session) error {
		if err := guard(row); err != nil {
			return err
		}
		from = row.State
		row.State = to
		sessionstore.MarkTerminated(row, from, now)
		return nil
	})
	return from, updated, err
}

// afterFailed runs the post-terminal hook of a `failed` write made by
// failSession or failFinalizing, with the signal tail of a settled child: the
// §8.10 archive and the terminal lifecycle signals. The hook seals, drains,
// emits the terminal status_change and session_complete, and releases the
// pod: a pre-running fromState reclaims a claimed pod by name, any other
// state releases the executor's binding. fromState is the pre-terminal state
// the writer read on the locked row.
// spec: §7.2, §11.7, §7.1
func (s *Server) afterFailed(ctx context.Context, fromState session.State, updated sessionstore.Session) {
	s.AfterTerminalWrite(ctx, fromState, updated, s.settledChildSignals)
}

// settledChildSignals is the signal tail of expireSession and afterFailed:
// the §8.10 archive of a settled child, then the terminal lifecycle signals.
func (s *Server) settledChildSignals(ctx context.Context, sess sessionstore.Session) {
	s.archiveSettledChild(ctx, sess)
	s.emitTerminalLifecycle(ctx, sess)
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
// and runs nothing, so a terminal state another writer committed is kept and
// no second post-terminal hook runs. Any other store error is returned
// unchanged, and the caller decides the response; no hook ran.
//
// It is a hook-path terminal writer. On a write that applied, the
// post-terminal hook runs with the locked row's fromState, finalizing, which
// is pre-running, so the hook's reclaim releases the pod claimed at /create by
// the row's pool kind: the session's slot member on a concurrent-workspace
// pool, deleting the shared claim only when that release empties the set, and
// the claim by name on an exclusive pool. Every finalize failure exit routes
// its pod and slot release through this hook; a by-name release that a
// through-Prepare failure's binder already performed is repeated on an
// exclusive pool, and a duplicate slot release is a keyed no-op.
//
// The gateway decides this write, so it runs detached from the request's
// cancellation: a client that disconnects before the write would otherwise
// leave the row in finalizing with no later writer. The post-terminal hook
// then bounds its own context.
// spec: §15.1 (finalize row), §6.2 (finalize timeout), §7.2 (terminal states),
// §6.1 (Per-session credential lease lifecycle), §7.1 (Gateway-owned terminal
// writes)
func (s *Server) failFinalizing(ctx context.Context, tenantID, id string) error {
	ctx = context.WithoutCancel(ctx)
	end := s.BeginTerminal(id)
	defer end()
	from, updated, err := s.writeTerminal(ctx, tenantID, id, session.StateFailed, finalizingPrecondition)
	if err != nil {
		return err
	}
	s.afterFailed(ctx, from, updated)
	return nil
}
