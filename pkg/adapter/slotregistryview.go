// SPDX-License-Identifier: MIT

package adapter

// SlotRegistryView is a point-in-time, read-only copy of the registry state
// §4.7.1's admission and Shutdown rules read for one slot identifier. It
// exists so an in-process conformance case can name the internal state
// behind a failure, such as the stamp an entry carries or a reclaim hold
// that stayed open, where a wire case can only report the answer a request
// received.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold); §15.4 (runtime adapter specification)
type SlotRegistryView struct {
	// Entry reports whether the registry holds an entry for the identifier.
	// The remaining entry fields are zero when it is false.
	Entry bool
	// BindAttempt is the bind attempt token the entry was stamped with at
	// creation, empty for an untokened entry. It is a capability over the
	// entry's teardown: no RPC returns it, and a caller must not log it.
	BindAttempt string
	// Started reports whether the merged start claim has run for the
	// entry's session.
	Started bool
	// ReclaimHeld reports whether the §5.2 reclaim hold is open on the
	// identifier, meaning a release has deregistered its entry and the
	// cleanup has not yet completed.
	ReclaimHeld bool
}

// InspectSlotRegistry returns the registry state for slotID, read under the
// registry lock in one critical section so the entry fields and the hold are
// mutually consistent. It changes nothing and is reachable only in process;
// no gateway RPC exposes it.
//
// spec: §4.7.1 (role and gateway RPC contract); §5.2 (slot-identifier
// reclaim hold)
func (s *Server) InspectSlotRegistry(slotID string) SlotRegistryView {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.reclaiming[slotID]
	view := SlotRegistryView{ReclaimHeld: held}
	if st, ok := s.slots[slotID]; ok {
		view.Entry = true
		view.BindAttempt = st.bindAttempt
		view.Started = st.started
	}
	return view
}
