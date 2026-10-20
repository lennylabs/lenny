// SPDX-License-Identifier: MIT

package adapter

import (
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// The two Shutdown teardown records answer rule 11's exit report. A
// Shutdown that finds no entry for a session removed nothing, but the
// session's runtime can still run: another Shutdown may be tearing the slot
// down right now, or the last Shutdown that removed the entry may have
// reported an unclean exit. The gateway's stale-slot reaper frees a slot set
// member only on a clean exit, so an absent answer in either case reports an
// unclean exit and the member is kept.
//
// Only a Shutdown sets either record. A release outside a Shutdown (the
// gateway-side rollbacks, DemoteSDK, the adapter's own start rollbacks)
// leaves both untouched, so the §5.2 disposition-table rows whose leaked
// cell is "Not entered" keep answering a later absent with a clean exit.
//
// spec: §4.7.1 (role and gateway RPC contract), rule 11; §5.2 (Slot-identifier
// reclaim hold).

// beginShutdownTeardownLocked records that a Shutdown removed sessionID's
// entry and has not yet answered. The caller holds s.mu and calls it in the
// critical section that deregistered the entry, so no absent answer can
// observe the gap between the removal and the record.
func (s *Server) beginShutdownTeardownLocked(sessionID string) {
	if s.shutdownInFlight == nil {
		s.shutdownInFlight = map[string]int{}
	}
	s.shutdownInFlight[sessionID]++
}

// endShutdownTeardown clears the in-flight record a removing Shutdown set and
// records its answer in one critical section. An unclean answer is kept for
// the life of the pod, as the reclaim hold after an incomplete cleanup is.
// A clean answer clears it, because rule 11 keys on the last Shutdown that
// removed the session's entry.
func (s *Server) endShutdownTeardown(sessionID string, exitedCleanly bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n := s.shutdownInFlight[sessionID]; n > 1 {
		s.shutdownInFlight[sessionID] = n - 1
	} else {
		delete(s.shutdownInFlight, sessionID)
	}
	if exitedCleanly {
		delete(s.shutdownUnclean, sessionID)
		return
	}
	if s.shutdownUnclean == nil {
		s.shutdownUnclean = map[string]struct{}{}
	}
	s.shutdownUnclean[sessionID] = struct{}{}
}

// nonRemovingExitCleanLocked is the exited_cleanly a Shutdown answer that
// removed nothing reports. superseded always reports a clean exit (rule 15);
// absent reports an unclean one while either record names the session
// (rule 11). The caller holds s.mu, in the critical section that decided
// the outcome.
func (s *Server) nonRemovingExitCleanLocked(sessionID string, outcome adapterv1.SlotReclaimOutcome) bool {
	if outcome != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT {
		return true
	}
	if s.shutdownInFlight[sessionID] > 0 {
		return false
	}
	_, unclean := s.shutdownUnclean[sessionID]
	return !unclean
}
