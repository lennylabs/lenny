// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// failSession marks a delegation node failed. Its caller is §8.10 tree
// recovery, which fails a node whose recovery ended without a committed
// restore: a reattach error or an elapsed level or tree budget. It writes
// `failed` only over a locked row still in resume_pending, through
// resumePendingGuard. A node that left resume_pending is not written: the
// resume driver and the §6.2 resuming watchdog own a node in resuming, a
// node whose restore committed is recovered, the §6.2 resume_pending
// wall-clock cap owns the move to awaiting_client_action, and a terminal row
// already ran its hook.
//
// It is a detached hook-path terminal writer. It opens its terminal claim
// before its Update and records the Terminated fact in the write. On a
// write that applied it runs the post-terminal hook through afterFailed on a
// goroutine over a context detached from ctx's cancellation, with fromState
// from the locked row, hands the claim to that goroutine, which ends it when
// the hook returns, and returns without waiting, so recovery moves to its
// next node while the node's hook, its budget return included, runs. A
// refused write (sessionstore.ErrSuperseded) or a store error runs no hook
// and ends the claim before failSession returns; the store error is
// ignored, because its one caller, FailNode, returns nothing.
//
// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model),
// §6.2 (resume_pending wall-clock cap), §7.1 (Terminal pipeline context),
// §10.1.7 (Terminal pipelines at process exit.)
func (s *Server) failSession(ctx context.Context, tenantID, sessionID string) {
	end := s.BeginTerminal(sessionID)
	from, updated, err := s.writeTerminal(ctx, tenantID, sessionID, session.StateFailed, resumePendingGuard)
	if err != nil {
		end()
		return
	}
	hctx := context.WithoutCancel(ctx)
	go func() {
		defer end()
		s.afterFailed(hctx, from, updated)
	}()
}

// resumePendingGuard is tree recovery's locked-row guard: it admits only a
// row in resume_pending, the one state the §7.2 resume_pending → failed edge
// leaves from, and refuses every other state with sessionstore.ErrSuperseded.
// spec: §8.10 (Delegation Tree Recovery), §7.2 (Interactive Session Model)
func resumePendingGuard(r *sessionstore.Session) error {
	if r.State != session.StateResumePending {
		return sessionstore.ErrSuperseded
	}
	return nil
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
// failSession, failFinalizing, or failStarting, with the signal tail of a settled child: the
// §8.10 archive and the terminal lifecycle signals. The hook seals, drains,
// emits the terminal status_change and session_complete, and releases the
// pod: a pre-running fromState reclaims a claimed pod by name, any other
// state releases the executor's binding. fromState is the pre-terminal state
// the writer read on the locked row.
// spec: §7.2, §11.7, §7.1
func (s *Server) afterFailed(ctx context.Context, fromState session.State, updated sessionstore.Session) {
	s.AfterTerminalWrite(ctx, fromState, updated, s.settledChildSignals)
}

// settledChildSignals is the signal tail of afterFailed: the §8.10 archive
// of a settled child, then the terminal lifecycle signals. Its writers are
// failSession, failFinalizing, and failStarting. It returns no budget; the
// post-terminal hook's return step does.
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

// reasonStartingFailed is the FailureReason failStarting stamps on a
// `starting → failed` write, the §15.1 code a failed /start answers with.
const reasonStartingFailed = "STARTING_FAILED"

// startingPrecondition refuses a /start exit write unless the locked row is
// still `starting`. Another writer (the starting watchdog, terminate, DELETE,
// admin force-terminate, full_revoke, or a §7.2 any-state terminal writer)
// can make the row terminal while the launch runs, and the exit write must
// not overwrite that state. The refusal reuses the §15.1 precondition error
// for the start endpoint, carrying the locked row's state, so
// writePreconditionError renders the 409 with it.
// spec: §15.1 (start row), §6.2 (starting timeout), §7.2 (Session state
// machine: starting → running, starting → failed)
func startingPrecondition(row *sessionstore.Session) error {
	if row.State == session.StateStarting {
		return nil
	}
	return &session.PreconditionError{
		Endpoint:      session.EndpointStart,
		CurrentState:  row.State,
		AllowedStates: session.AllowedStates(session.EndpointStart, nil),
	}
}

// failStartingGuard is failStarting's locked-row guard: it applies
// startingPrecondition and, on a `starting` row, stamps STARTING_FAILED. The
// reason is fixed rather than taken from the exit, because the response is
// chosen after the write. No FailureClass is set, as failFinalizing sets
// none.
func failStartingGuard(row *sessionstore.Session) error {
	if err := startingPrecondition(row); err != nil {
		return err
	}
	row.FailureReason = reasonStartingFailed
	return nil
}

// failStarting marks a starting session failed, but only while the locked
// row is still `starting`. Every /start exit that follows the
// ready → starting write calls it. A lost write returns
// *session.PreconditionError and runs nothing, so a terminal state another
// writer committed, or a `running` row a committed start left, is kept and
// no second post-terminal hook runs. Any other store error is returned
// unchanged, no hook ran, and the starting watchdog later fails the row.
//
// It is a hook-path terminal writer. On a write that applied, the
// post-terminal hook runs through afterFailed with fromState `starting`: a
// replica that holds no binding for the session reclaims the claimed pod by
// name, and a replica that published one releases it through the executor.
//
// The gateway decides this write, so it runs detached from the request's
// cancellation: a client that disconnects before the write would otherwise
// leave the row in starting until the watchdog fires.
// spec: §7.2 (Session state machine: starting → failed), §7.1
// (Gateway-owned terminal writes), §15.1 (start row)
func (s *Server) failStarting(ctx context.Context, tenantID, id string) error {
	ctx = context.WithoutCancel(ctx)
	end := s.BeginTerminal(id)
	defer end()
	from, updated, err := s.writeTerminal(ctx, tenantID, id, session.StateFailed, failStartingGuard)
	if err != nil {
		return err
	}
	s.afterFailed(ctx, from, updated)
	return nil
}
