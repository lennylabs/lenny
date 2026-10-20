// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// BeginTerminal opens a terminal-writer claim for the session on this
// replica and returns the idempotent function that ends it. It never blocks.
// Every terminal writer whose path runs the post-terminal hook calls it
// before its terminal Update and calls the returned end on every path after
// the Update returns: after the hook when the write applied, and at once on
// an error or a not-applied sentinel. While a claim is open the terminal
// binding release defers, so on the writer's replica a release triggered by
// the coordination Sweeper never precedes the hook's seal, drain, and
// terminal signals.
//
// spec: §7.2 (Interactive Session Model), §10.1.5 (Stale Replica Behavior)
func (s *Server) BeginTerminal(sessionID string) (end func()) {
	return s.terminalClaims.open(sessionID)
}

// AfterTerminalWrite is the post-terminal hook, the one entry point of the
// session-end order from the seal to the pod release. The writer whose
// terminal write applied calls it once, after its Update returns nil and
// before it ends its claim. fromState is the pre-terminal state the writer
// read on the locked row; signals is the writer's terminal-signal tail, and a
// nil signals runs none. The steps, in order:
//
//  1. Cancel a same-replica resume attempt, so the attempt's restoration RPCs
//     stop before the hook touches the pod.
//  2. Settle a stale local binding (settleStaleBinding): when a peer advanced
//     the row's generation past this replica's binding, evict the binding
//     with no binder call, so the later steps send the peer's pod nothing.
//  3. Seal and export the workspace when a Sealer is wired; a seal failure
//     relabels the row failed through failWorkspaceSeal, whose returned row
//     replaces sess. With no local binding the Sealer returns nil.
//  4. Drain the session's inbox and DLQ, sending message_expired.
//  5. On a terminal row, emit one status_change carrying the row's state and
//     one session_complete, then run signals.
//  6. Release the pod: a pre-running claim by name, otherwise the executor's
//     binding with the disposition of the row's state.
//  7. Expire the pending elicitations the session's pod raised.
//
// Every step is best-effort and none fails the terminal write. The hook
// writes no row except failWorkspaceSeal's relabel. It returns the row the
// later steps saw.
//
// spec: §7.1 (Normal Flow), §7.2 (Interactive Session Model), §7.3 (Resume
// driver), §9.2 (Elicitation Chain), §10.1.5 (Stale Replica Behavior), §29.4
// (Interrupt, terminate, and delete)
func (s *Server) AfterTerminalWrite(ctx context.Context, fromState session.State, sess sessionstore.Session,
	signals func(context.Context, sessionstore.Session),
) sessionstore.Session {
	s.CancelResumeAttempt(sess.ID)
	s.settleStaleBinding(fromState, sess)
	if s.sealer != nil {
		if err := s.sealWorkspace(ctx, sess); err != nil {
			sess = s.failWorkspaceSeal(ctx, sess, err)
		}
	}
	s.drainMessagingOnTerminal(ctx, sess)
	if session.IsTerminal(sess.State) {
		// spec: §7.2 — the terminal events, on the row the seal step
		// returned, so a seal relabel is reported failed.
		s.emitStatusChange(sess.TenantID, sess.ID, sess.State)
		s.emitSessionComplete(ctx, sess)
	}
	if signals != nil {
		signals(ctx, sess)
	}
	if !s.terminalReclaimPreRunning(ctx, fromState, sess) && s.executor != nil {
		if err := releaseExecutor(ctx, s.executor, sess.ID, dispositionForState(sess.State)); err != nil {
			// The release is best-effort, but a failed teardown leaves a
			// claim or slot counted, so it is logged for an operator.
			log.Printf("lenny-gateway: executor release session=%s: %v", sess.ID, err)
		}
	}
	// spec: §9.2 (Elicitation Chain) — last, after the pod release, so the
	// runtime on the released pod cannot raise again after the expiry.
	s.expireRaisedElicitations(ctx, sess)
	return sess
}

// settleStaleBinding is the post-terminal hook's stale-binding step. It runs
// after CancelResumeAttempt, so a same-replica resume attempt has stopped
// before it reads the registry, and before the seal. When this replica's
// binding for the session is stale against the terminal row
// (bindingStale) and fromState is one a coordinator binds a pod in, from
// running onward, it evicts the binding with no binder call: the replica
// that advanced the generation owns the pod's release. The later seal then
// finds no binding and sends the peer's pod no Checkpoint RPC, and the later
// release finds no binding to release. A pre-running fromState is left to
// the pre-running reclaim, and a binding at the row's generation is left to
// the release step.
//
// spec: §10.1.1 (Generation counters:), §10.1.5 (Stale Replica Behavior),
// §7.3 (Resume driver)
func (s *Server) settleStaleBinding(fromState session.State, sess sessionstore.Session) {
	if s.podRegistry == nil {
		return
	}
	bind, ok := s.podRegistry.Get(sess.ID)
	if !ok || !bindingStale(bind.CoordinationGeneration, sess.CoordinationGeneration) {
		return
	}
	switch fromState {
	case session.StateRunning, session.StateInputRequired, session.StateSuspended,
		session.StateResumePending, session.StateResuming, session.StateAwaitingClientAction:
		s.evictStaleBinding(sess.ID, bind)
		slog.Info("sessionserver: post-terminal hook evicted a stale binding; a peer owns the pod",
			"session_id", sess.ID, "from_state", string(fromState),
			"binding_generation", bind.CoordinationGeneration, "row_generation", sess.CoordinationGeneration)
	}
}

// AfterRevokeWrite is the §11.4 full_revoke per-session hook, which the
// admin router calls right after each write that cancelled one of the user's
// sessions: it cancels a same-replica resume attempt for the session and
// drains the session's inbox and DLQ. The full_revoke write runs no
// post-terminal hook; its fan-out sends Shutdown to each cancelled session's
// pod. spec: §7.3 (Retry and Resume), §11.4 (User Invalidation).
func (s *Server) AfterRevokeWrite(ctx context.Context, row sessionstore.Session) {
	s.CancelResumeAttempt(row.ID)
	s.drainMessagingOnTerminal(ctx, row)
}
