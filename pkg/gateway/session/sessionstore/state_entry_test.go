// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// spec: §6.2 (Pod State Machine)
//
// StampStateEntry sets the state-entry instant to the write's instant on
// every pair of distinct states, and restores the stored value on every
// same-state write, discarding a value the mutator wrote in either case.
func TestStampStateEntryMovesOnlyOnStateChange_spec_6_2(t *testing.T) {
	prevEntered := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := prevEntered.Add(10 * time.Minute)
	forged := prevEntered.Add(time.Hour)
	for _, prev := range session.AllStates() {
		for _, next := range session.AllStates() {
			row := sessionstore.Session{State: next, StateEnteredAt: forged}
			sessionstore.StampStateEntry(prev, prevEntered, &row, now)
			want := prevEntered
			if prev != next {
				want = now
			}
			if !row.StateEnteredAt.Equal(want) {
				t.Fatalf("%s -> %s: StateEnteredAt = %v, want %v", prev, next, row.StateEnteredAt, want)
			}
		}
	}
}
