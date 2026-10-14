// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"

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
//  2. Seal and export the workspace when a Sealer is wired; a seal failure
//     relabels the row failed through failWorkspaceSeal, whose returned row
//     replaces sess. With no local binding the Sealer returns nil.
//  3. Drain the session's inbox and DLQ, sending message_expired.
//  4. On a terminal row, emit one status_change carrying the row's state and
//     one session_complete, then run signals.
//  5. Release the pod: a pre-running claim by name, otherwise the executor's
//     binding with the disposition of the row's state.
//  6. Expire the pending elicitations the session's pod raised.
//
// Every step is best-effort and none fails the terminal write. The hook
// writes no row except failWorkspaceSeal's relabel. It returns the row the
// later steps saw.
//
// spec: §7.1 (Normal Flow), §7.2 (Interactive Session Model), §7.3 (Resume
// driver), §9.2 (Elicitation Chain), §29.4 (Interrupt, terminate, and delete)
func (s *Server) AfterTerminalWrite(ctx context.Context, fromState session.State, sess sessionstore.Session,
	signals func(context.Context, sessionstore.Session),
) sessionstore.Session {
	s.CancelResumeAttempt(sess.ID)
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
