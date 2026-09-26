// SPDX-License-Identifier: MIT

package adapter

// SlotRegistryView is a point-in-time, read-only answer about the registry
// state §4.7.1's admission and Shutdown rules read for one slot identifier.
// It exists so an in-process conformance case can name the internal state
// behind a failure, such as whether an entry carries the expected stamp or
// a reclaim hold stayed open, where a wire case can only report the answer a
// request received.
//
// The view never carries the bind attempt token itself. §4.7.1 has the
// adapter compare the token for equality and do nothing else with it, so the
// view answers the same comparison against a token the caller already holds
// rather than handing the entry's token back out of the adapter.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold); §15.4 (runtime adapter specification)
type SlotRegistryView struct {
	// Entry reports whether the registry holds an entry for the identifier.
	// The remaining entry fields are false when it is false.
	Entry bool
	// Tokened reports whether the entry was stamped with a bind attempt token
	// at creation.
	Tokened bool
	// CarriesBindAttempt reports whether the entry is tokened and its stamp
	// equals the token the caller named. It is false for an untokened entry
	// and for an empty named token.
	CarriesBindAttempt bool
	// Started reports whether the merged start claim has run for the
	// entry's session.
	Started bool
	// ReclaimHeld reports whether the §5.2 reclaim hold is open on the
	// identifier, meaning a release has deregistered its entry and the
	// cleanup has not yet completed.
	ReclaimHeld bool
}

// InspectSlotRegistry returns the registry state for slotID, comparing the
// entry's stamp against token, read under the registry lock in one critical
// section so the entry fields and the hold are mutually consistent. It
// changes nothing and is reachable only in process; no gateway RPC exposes
// it.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func (s *Server) InspectSlotRegistry(slotID, token string) SlotRegistryView {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.reclaiming[slotID]
	view := SlotRegistryView{ReclaimHeld: held}
	if st, ok := s.slots[slotID]; ok {
		view.Entry = true
		view.Tokened = st.bindAttempt != ""
		view.CarriesBindAttempt = view.Tokened && st.bindAttempt == token
		view.Started = st.started
	}
	return view
}
