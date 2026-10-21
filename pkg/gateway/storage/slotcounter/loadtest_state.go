// SPDX-License-Identifier: MIT

//go:build load_local

package slotcounter

import "time"

// This file is compiled only into the tier-7a build (the load_local build
// tag), where the randomized interleaving tests drive two Counters from
// concurrent goroutines and must read the recovery state the add-only
// rebuild maintains. It adds nothing to the production binary.

// OutageState is one consistent read of a Counter's recovery state.
type OutageState struct {
	// Since is the outage clock; zero when no outage is being recovered.
	Since time.Time
	// Entries is the number of outage-ledger entries.
	Entries int
	// Seq is the ledger sequence number, raised by every record.
	Seq uint64
	// OldestSeq is the lowest sequence number among the ledger's entries,
	// zero when the ledger is empty. An entry whose sequence number is at or
	// below an earlier read's Seq was in the ledger at that read and has
	// not been recorded again since.
	OldestSeq uint64
}

// OutageStateForLoadTest returns the outage clock and the ledger's size and
// sequence numbers, read under the lock that guards them.
// spec: §12.4 (Redis failure behavior).
func (c *Counter) OutageStateForLoadTest() OutageState {
	c.outageMu.Lock()
	defer c.outageMu.Unlock()
	st := OutageState{Since: c.outageSince, Entries: len(c.ledger), Seq: c.ledgerSeq}
	for _, seq := range c.ledger {
		if st.OldestSeq == 0 || seq < st.OldestSeq {
			st.OldestSeq = seq
		}
	}
	return st
}
