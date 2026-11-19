// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// ReleaseTerminalBinding is the coordination Sweeper's terminal release hook
// and the resume tail's fast path. It releases this replica's binding for a
// session whose row is terminal, or evicts it when a peer took the session
// over. The post-terminal hook releases only the writing replica's binding,
// so it misses a binding published after the terminal write and every
// binding held on another replica; the Sweeper calls this method on every
// listing of a bound terminal row to close that gap.
//
// It returns at once. It does nothing for a non-terminal row, and nothing
// while this replica holds an open terminal claim for the session or already
// runs a release for it, so on the writer's replica a release never precedes
// the post-terminal hook's seal, drain, and terminal signals. A deferred call
// is not queued: the Sweeper calls again on its next listing. Otherwise it
// starts one goroutine on the server-lifetime context, marked in the
// terminal claim record, which takes the session's slot-accounting lock, so a
// release follows a failure funnel still accounting the slot, and then reads
// the current binding:
//
//   - no binding: nothing;
//   - a binding stamped 0, or below the row's generation: evict it with no
//     binder call, because the replica that advanced the generation owns the
//     pod's release, and an unstamped binding fails closed;
//   - otherwise: release exactly that binding with the disposition of the
//     row's state, which returns the binding's credential leases.
//
// It writes no session state; after a release it ran, it records the release
// on the row (§7.1 Post-terminal window). Each reclaim is logged at INFO with
// the session ID, state, and action.
//
// spec: §7.2 (Interactive Session Model), §10.1.1 (Stateless Replicas and
// Per-Session Coordination), §10.1.5 (Stale Replica Behavior)
func (s *Server) ReleaseTerminalBinding(row sessionstore.Session) {
	if !session.IsTerminal(row.State) {
		return
	}
	if !s.terminalClaims.startRelease(row.ID) {
		return
	}
	go func() {
		defer s.terminalClaims.endRelease(row.ID)
		if _, err := s.releaseTerminalBinding(s.lifetime(), row); err != nil {
			slog.Warn("sessionserver: terminal binding release failed",
				"session_id", row.ID, "state", string(row.State), "error", err)
		}
	}()
}

// ReleaseRevokedBinding is the §11.4 full_revoke binding release on the
// replica that holds the binding. The full_revoke fan-out calls it for each
// registry entry of a session the revoke cancelled, on this replica and on
// each peer that applies the published request. It reads the row from the
// primary and runs the terminal binding release body synchronously on ctx,
// under the same terminal claim record the coordination Sweeper's release
// uses, so a session's binding is released once whichever path reaches it
// first.
//
// It returns (true, nil) when the executor release returned nil. It returns
// (false, nil), and releases nothing, for a non-terminal row, when a
// terminal binding release already runs for the session or a terminal
// writer's claim is open on this replica (the coordination Sweeper releases
// the binding on a later listing), when this replica holds no binding, for a
// stale binding it evicts, and for a binding replaced after the read. It
// returns (false, err) for a primary-read error, a lock-wait error on ctx,
// and an executor release error. An executor error leaves no binding to
// retry, because the executor unbinds before its binder runs.
//
// spec: §11.4 step 2 (User Invalidation), §10.1.5 (Stale Replica Behavior),
// §7.2 (Interactive Session Model)
func (s *Server) ReleaseRevokedBinding(ctx context.Context, tenantID, sessionID string) (released bool, err error) {
	row, err := s.readPrimary(ctx, tenantID, sessionID)
	if err != nil {
		return false, fmt.Errorf("read revoked session %s: %w", sessionID, err)
	}
	if !session.IsTerminal(row.State) {
		return false, nil
	}
	if !s.terminalClaims.startRelease(sessionID) {
		return false, nil
	}
	defer s.terminalClaims.endRelease(sessionID)
	return s.releaseTerminalBinding(ctx, row)
}

// releaseTerminalBinding is the body of the terminal binding release. It
// waits on the session's slot-accounting lock on ctx, so a release follows a
// failure funnel still accounting the slot, then settles the current
// binding: none releases nothing, a stale one is evicted with no binder
// call, and a current one is released with the disposition of the row's
// state. It reports whether it released a binding and the error that left
// one unreleased.
func (s *Server) releaseTerminalBinding(ctx context.Context, row sessionstore.Session) (bool, error) {
	if s.podRegistry == nil {
		return false, nil
	}
	unlock, err := s.slotAccountLocks.Lock(ctx, row.ID)
	if err != nil {
		return false, fmt.Errorf("wait for the slot-accounting lock of session %s: %w", row.ID, err)
	}
	defer unlock()
	bind, ok := s.podRegistry.Get(row.ID)
	if !ok {
		return false, nil
	}
	log := slog.With("session_id", row.ID, "state", string(row.State))
	if bindingStale(bind.CoordinationGeneration, row.CoordinationGeneration) {
		s.evictStaleBinding(row.ID, bind)
		log.Info("sessionserver: terminal binding evicted; a peer owns the pod",
			"binding_generation", bind.CoordinationGeneration, "row_generation", row.CoordinationGeneration)
		return false, nil
	}
	released, err := s.releaseCurrentBinding(ctx, row.ID, bind, dispositionForState(row.State))
	// spec: §7.1 (Post-terminal window) — the release ran on the current
	// binding; record it whatever its error. A binding replaced after the
	// read released nothing and records nothing.
	if released || err != nil {
		s.recordPodRelease(ctx, row)
	}
	if err != nil {
		return false, fmt.Errorf("release the binding of session %s: %w", row.ID, err)
	}
	log.Info("sessionserver: terminal binding released", "released", released)
	return released, nil
}

// bindingStale reports whether a local binding stamped at bindGen is stale
// against a session row at rowGen. RecordHandoff raises the row's
// coordination_generation on every takeover, and the resume driver raises it
// on every resume_pending → resuming write, so a row generation above the
// binding's means another replica coordinates the session or a later
// incarnation owns it. A binding stamped 0 counts as stale, so a publish site
// that never stamped its binding fails closed. Every stale-binding check in
// this package uses this one predicate, so the terminal binding release, the
// stream-failure report, and the post-terminal hook cannot disagree on which
// bindings a peer owns.
//
// spec: §10.1.1 (Generation counters:), §10.1.5 (Stale Replica Behavior)
func bindingStale(bindGen, rowGen int64) bool {
	return bindGen == 0 || rowGen > bindGen
}
