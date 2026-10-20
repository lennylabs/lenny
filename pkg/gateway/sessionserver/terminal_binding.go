// SPDX-License-Identifier: MIT

package sessionserver

import (
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
// It writes no session row. Each reclaim is logged at INFO with the session
// ID, state, and action.
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
		s.releaseTerminalBinding(row)
	}()
}

// releaseTerminalBinding is the body of the terminal binding release.
func (s *Server) releaseTerminalBinding(row sessionstore.Session) {
	if s.podRegistry == nil {
		return
	}
	ctx := s.lifetime()
	unlock, err := s.slotAccountLocks.Lock(ctx, row.ID)
	if err != nil {
		return
	}
	defer unlock()
	bind, ok := s.podRegistry.Get(row.ID)
	if !ok {
		return
	}
	log := slog.With("session_id", row.ID, "state", string(row.State))
	if bindingStale(bind.CoordinationGeneration, row.CoordinationGeneration) {
		s.evictStaleBinding(row.ID, bind)
		log.Info("sessionserver: terminal binding evicted; a peer owns the pod",
			"binding_generation", bind.CoordinationGeneration, "row_generation", row.CoordinationGeneration)
		return
	}
	released := s.releaseReportedBinding(ctx, row.ID, bind, dispositionForState(row.State))
	log.Info("sessionserver: terminal binding released", "released", released)
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
