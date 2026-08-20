// SPDX-License-Identifier: MIT

package adapter

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// claimSessionSlot binds sessionID's slot entry and marks it started. It
// is the one claim every start path takes: StartSession, Resume, and the
// SDK-warm ConfigureWorkspace.
//
// The claim discriminates on whether the session has started rather than
// on whether its entry is bound, because the §4.7 bind sequence assigns
// credentials before the start and assignCredentialsSlot binds the entry
// when it runs first. A rule keyed on the binding would refuse the first
// start of every session on every pool that configures a credential pool
// or a user credential provider. It therefore admits the three states an
// entry can be in ahead of a start — absent, registered, and
// bound-not-started — and refuses a started one.
//
// sdkWarm gates the different-session refusal. A pre-connected pod holds
// one runtime process with one working directory the handler re-points in
// place, and §6.4 admits preConnect only at maxConcurrentSessions: 1, so
// admitting a second session there would rewrite the §15.4.3 nonce the
// incumbent runtime already authenticated with. On a pod-warm pod a
// different session arrives on its own slot and is admitted.
//
// idempotentRepeat reports a repeat for an already-started session as
// fresh=false rather than refusing it, which is the §4.7
// ConfigureWorkspace idempotency.
//
// startMCP reports that this claim took the once-per-pod intra-pod MCP
// start. The decision is taken inside this critical section rather than
// as a read followed by a bind, because the platform server binds the one
// socket the controller renders for the whole pod and two concurrent
// claims that both observed it free would hand the loser EADDRINUSE.
//
// spec: §4.7; §5.2; §15.4.3.
func (s *Server) claimSessionSlot(sessionID string, sdkWarm, idempotentRepeat bool) (fresh, startMCP bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sdkWarm {
		for id, other := range s.slots {
			if id != sessionID && other.sessionID != "" {
				return false, false, status.Errorf(codes.Unavailable,
					"pod is not idle: session %s is already assigned", other.sessionID)
			}
		}
	}
	st, err := s.ensureSlotStateLocked(sessionID)
	if err != nil {
		return false, false, status.Errorf(codes.InvalidArgument,
			"resolve slot for session %s: %v", sessionID, err)
	}
	if st.started {
		if idempotentRepeat {
			return false, false, nil
		}
		return false, false, status.Errorf(codes.Unavailable,
			"session %s has already started on this pod", sessionID)
	}
	st.sessionID = sessionID
	st.started = true
	return true, s.claimPodMCPStartLocked(), nil
}

// claimPodMCPStartLocked reports whether the caller must arm the pod's
// platform and per-connector MCP servers, and records the claim so a
// concurrent claim does not also take it. The servers are pod-wide, so
// they are armed only while the registry holds no entry but the
// claimant's; a claim on a co-tenanted pod finds the surface already
// accounted for. Callers hold s.mu. spec: §15.4.3.
func (s *Server) claimPodMCPStartLocked() bool {
	if s.mcpArmed || s.mcpCancel != nil || len(s.slots) != 1 {
		return false
	}
	s.mcpArmed = true
	return true
}

// deregisterSlotLocked is the first of the two release steps: under s.mu
// it cancels every direct-mode lease-expiry timer armed on the session's
// entry, deletes the entry, and reports whether any bound entry remains.
// It returns the deregistered state so the caller can run the second step
// after the lock is released.
//
// The cancellation belongs here because an armed timer left behind fires
// AUTH_EXPIRED against a session that has already ended, and both teardown
// paths this step replaces cancelled before the runtime close.
//
// The bound-entry answer is the outcome of the same critical section that
// removed the entry rather than a read taken before it, so two co-tenants
// ending at once cannot each observe the other and both decline the
// pod-wide action the answer gates. Callers hold s.mu.
//
// spec: §4.9; §15.4.2.
func (s *Server) deregisterSlotLocked(sessionID string) (st *slotState, removed, boundRemains bool) {
	st, removed = s.slots[sessionID]
	if removed {
		for provider := range st.timers {
			s.cancelSlotExpiryTimerLocked(st, provider)
		}
		delete(s.slots, sessionID)
	}
	for _, other := range s.slots {
		if other.sessionID != "" {
			boundRemains = true
			break
		}
	}
	return st, removed, boundRemains
}

// deregisterSlot takes s.mu and runs deregisterSlotLocked.
func (s *Server) deregisterSlot(sessionID string) (st *slotState, removed, boundRemains bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.deregisterSlotLocked(sessionID)
}

// releaseSessionSlot runs both release steps in immediate succession and
// then the pod-surface cancellation. It is the compensating action every
// failed start path takes: it undoes the claim, removes the per-slot tree
// the claim created, and returns the pod's occupancy to what it was
// before the call.
//
// The pod-wide MCP teardown is gated on the release leaving the pod's
// shared runtime process serving no session, so a rollback on a
// co-tenanted pod cancels nothing and the co-tenant keeps the surface it
// is using. A rollback that armed the servers and never reached a start
// finds that process idle and cancels them, which is how a pod recovers
// from a failed StartSession, Resume, or ConfigureWorkspace.
//
// spec: §4.7; §15.4.3.
func (s *Server) releaseSessionSlot(sessionID string) {
	st, removed, _ := s.deregisterSlot(sessionID)
	if removed {
		_ = removeSlotTree(st)
	}
	s.cancelPodMCPIfRuntimeIdle()
}

// cancelPodMCPIfRuntimeIdle stops the pod's platform and per-connector
// MCP servers and clears the §15.4.3 handshake signal when the pod's
// shared runtime process is serving no session. No arming identifier is
// stored: a release that ends the generation cancels a surface no
// surviving session can use, and a release that leaves the process
// serving a session cancels nothing. spec: §15.4.3; §5.1 — F-5.1.11.
func (s *Server) cancelPodMCPIfRuntimeIdle() {
	s.mu.Lock()
	if !s.runtimeIdleLocked() {
		s.mu.Unlock()
		return
	}
	cancel := s.mcpCancel
	s.mcpCancel = nil
	s.mcpArmed = false
	s.mcpHandshakeSeen = false
	connectorCancels := s.connectorCancels
	s.connectorCancels = nil
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	for _, c := range connectorCancels {
		c()
	}
}

// checkSessionBound validates an inbound session-scoped RPC against the
// slot registry. Every session is bound to a slot on every pod, so this
// is the one session check: it admits an entry bound to the named session
// (started or not) and refuses one that is absent or registered but not
// yet bound. spec: §4.2; §5.2.
func (s *Server) checkSessionBound(sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slotStateLocked(sessionID)
	if !ok || st.sessionID == "" {
		return status.Errorf(codes.FailedPrecondition,
			"session %s is not assigned to this pod", sessionID)
	}
	return nil
}

// anyStartedSession returns the identifier of one session the adapter has
// started on this pod, empty when none has started. The §10.1 coordinator
// hold arms on it: a closed gateway control stream is a coordinator loss
// only while the pod is serving a session. spec: §10.1.
func (s *Server) anyStartedSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, st := range s.slots {
		if st.started {
			return id
		}
	}
	return ""
}
