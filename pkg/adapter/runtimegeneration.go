// SPDX-License-Identifier: MIT

package adapter

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

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

// noteRuntimeStarted records the pod's one shared runtime process as holding
// sessionID, the record at which the slot reaches §6.2 running, and reports
// whether the record was taken. It runs immediately after a successful
// start. It refuses in the two states a §7.1 reclaim leaves: the registry
// holds no entry bound to this session, or it holds one stamped with a
// different bind attempt. Recording in either would put a session in
// runtimeLive that the registry does not hold under this attempt's
// identity, holding runtimeIdleLocked false and soleSession empty for the
// life of the pod.
//
// The second state is the replaced-entry case. The slot identifier equals
// the session identifier, so a successor attempt at the same session holds
// an entry under the same key with the same sessionID, and a predicate
// reading only st.sessionID cannot see that the entry belongs to a later
// attempt. The bind attempt can, because the successor stamped the entry it
// created with its own token and the adapter never overwrites a token on a
// resolve.
//
// attempt is the token the entry carried when this start's own claim was
// admitted, passed in rather than re-read here: a read taken after the
// claim is as racy as the confirmation it anchors. A start that carries no
// token of its own passes the entry's token, so an entry no later attempt
// replaced compares equal to itself. The predicate reads the registry entry
// rather than st.started, because the claim sets st.started before
// Runtime.Start and it is therefore true for this very call.
//
// The resolve, the confirmation and the record run under one hold of s.mu,
// which is the start step of the §4.7.1 registry critical section: a
// confirmation that released the lock before recording would record a
// session a reclaim had already released.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract), rule 8
// (the start-confirmation rule); §15.4.3.
func (s *Server) noteRuntimeStarted(sessionID, attempt string) bool {
	if sessionID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.slots[sessionID]
	if !ok || st.sessionID != sessionID || st.bindAttempt != attempt {
		return false
	}
	s.noteRuntimeStartedLocked(sessionID)
	return true
}

// rollbackUnconfirmedStart is the arm a start takes when noteRuntimeStarted
// refuses: a §7.1 reclaim removed the slot while Runtime.Start ran, or a
// successor replaced its entry. It takes the session back off the shared
// runtime process, cancels a pod-wide MCP surface no surviving claimant
// holds, and returns the refusal the handler answers.
//
// It deregisters nothing, and that is deliberate. The confirmation refuses
// exactly when the registry holds no entry under this slot identifier or
// holds one a later attempt owns, so a release keyed on the session
// identifier alone either removes nothing or removes the successor's
// entry, its tree, its uploads and its credential directory. The MCP
// cancellation is safe against a successor because it is gated on
// mcpArmingHeldLocked. It reports no cleanup outcome either: the slot never
// reached running, and §5.2 files the one cleanup-outcome report per
// release from the cleanup that reclaims the slot.
//
// The refusal is codes.Aborted, the transient classification a caller
// retries on: a further attempt succeeds once the reclaim's residue is gone.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract), rule 8
// (the start-confirmation rule); §15.4.3.
func (s *Server) rollbackUnconfirmedStart(ctx context.Context, sessionID string) error {
	if s.Runtime != nil {
		_ = s.Runtime.Close(ctx, sessionID)
	}
	s.cancelPodMCPIfRuntimeIdle()
	return status.Errorf(codes.Aborted,
		"session %s slot was reclaimed while the start was in flight", sessionID)
}

// noteRuntimeStartedLocked adds sessionID to the shared runtime process's
// generation, with s.mu already held and the start already confirmed. It is
// a no-op when the generation's first session is already this session, so
// an idempotent repeat of a start cannot raise the cohort and drive
// soleSession empty for the life of the pod.
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

// runtimeHoldsLocked reports whether the pod's shared runtime process holds
// sessionID, which is what separates a slot that reached §6.2's running from
// one whose start is still in flight or never ran. noteRuntimeStartedLocked
// is the only writer that adds the session and noteRuntimeClosed the only
// one that removes it. Callers hold s.mu.
// spec: §5.2 (pool configuration and execution modes); §6.2.
func (s *Server) runtimeHoldsLocked(sessionID string) bool {
	_, ok := s.runtimeLive[sessionID]
	return ok
}
