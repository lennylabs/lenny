// SPDX-License-Identifier: MIT

package sessionserver

import (
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/slothealth"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// TestNewHonorsInjectedSlotHealth verifies New wires the §5.2 fail/leak
// tracker passed in Options rather than constructing its own, so the gateway
// can share one Tracker between the slot-bind-failure path (the slot retry
// policy) and the §4.7 scrub-report drain ledger. Sharing the Tracker is what
// makes adapter-reported leaks and slot-bind failures accumulate in one
// rolling window for the combined ceil(maxConcurrentSessions/2) threshold.
// spec: 5.2 (combined failed+leaked unhealthy threshold), 6.2 (leaked-slot semantics)
func TestNewHonorsInjectedSlotHealth_spec_5_2(t *testing.T) {
	shared := slothealth.New()
	s := New(memstore.New(), Options{SlotHealth: shared})
	if s.slotHealth != shared {
		t.Error("New did not use the injected SlotHealth tracker; the slot-bind-failure path and the scrub-report ledger would observe disjoint windows")
	}
}

// TestNewDefaultsSlotHealthWhenNil verifies New defaults to a fresh per-server
// Tracker when no shared tracker is injected (the standalone path with no §4.7
// scrub-report ledger), so the slot retry policy never dereferences a nil
// tracker.
// spec: 5.2 (whole-pod replacement trigger)
func TestNewDefaultsSlotHealthWhenNil_spec_5_2(t *testing.T) {
	s := New(memstore.New(), Options{})
	if s.slotHealth == nil {
		t.Error("New left slotHealth nil with no injected tracker; the slot retry policy would panic")
	}
}
