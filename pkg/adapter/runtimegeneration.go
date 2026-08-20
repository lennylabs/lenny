// SPDX-License-Identifier: MIT

package adapter

// This file holds the pod-level runtime-generation state and the one
// accessor that names the session a pod-global surface may act as.
//
// One runtime process serves every slot on the pod, and the frames it
// writes carry no session of their own. A pod-global surface (the
// intra-pod MCP providers, the direct-mode token fold, the control-event
// session stamp) therefore has to decide whether the session it would
// name can be the only one whose code is resident in that process. The
// slot registry cannot answer that: a per-session Runtime.Close deletes a
// bookkeeping entry and returns without ending that session's code inside
// the shared process, so an entry count is a proxy for the process's
// occupancy rather than a statement about it. The state below counts the
// sessions given to the process instead.
//
// spec: §15.4.3; §9.1; §11.2.

// noteRuntimeStarted records that sessionID has been given to the pod's
// one shared runtime process. It runs immediately after a successful
// start. It is a no-op when the generation's first session is already
// this session, so an idempotent repeat of a start cannot raise the
// cohort and drive soleSession empty for the life of the pod.
func (s *Server) noteRuntimeStarted(sessionID string) {
	if sessionID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.noteRuntimeStartedLocked(sessionID)
}

// noteRuntimeStartedLocked is noteRuntimeStarted with s.mu already held.
func (s *Server) noteRuntimeStartedLocked(sessionID string) {
	if s.runtimeLive == nil {
		s.runtimeLive = map[string]struct{}{}
	}
	if s.cohortSession == sessionID {
		s.runtimeLive[sessionID] = struct{}{}
		return
	}
	s.runtimeLive[sessionID] = struct{}{}
	s.runtimeCohort++
	if s.runtimeCohort == 1 {
		s.cohortSession = sessionID
	}
}

// noteRuntimeClosed records that sessionID's Runtime.Close has returned.
// When the removal leaves the generation empty it resets the generation,
// so the next session admitted to a process serving nobody is sole again.
// A close for a session the generation never held moves nothing: keying
// the removal on membership rather than on a bare count is what keeps a
// close of a bound-not-started session from emptying a generation a
// co-tenant is still resident in.
func (s *Server) noteRuntimeClosed(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runtimeLive[sessionID]; !ok {
		return
	}
	delete(s.runtimeLive, sessionID)
	if len(s.runtimeLive) == 0 {
		s.runtimeCohort = 0
		s.cohortSession = ""
	}
}

// soleSession returns the session the pod's one shared runtime process
// has been given, and nothing else, since it was last serving none. It is
// the empty string whenever another session's code may still be resident
// in that process, so a pod-global surface fails closed rather than
// acting under a session other than the caller's.
func (s *Server) soleSession() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.soleSessionLocked()
}

// soleSessionLocked is soleSession with s.mu already held.
func (s *Server) soleSessionLocked() string {
	if s.runtimeCohort != 1 {
		return ""
	}
	return s.cohortSession
}

// runtimeIdleLocked reports that the pod's shared runtime process is
// serving no session. Callers hold s.mu.
func (s *Server) runtimeIdleLocked() bool {
	return len(s.runtimeLive) == 0
}
