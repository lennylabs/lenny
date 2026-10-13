// SPDX-License-Identifier: MIT

package sessionstore_test

import (
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// ageCountingStates is the §6.2 maxSessionAge pause table read as the states
// whose interval counts toward the cap. Every other state in AllStates is
// paused, not yet running, or stopped.
var ageCountingStates = map[session.State]bool{
	session.StateStarting:      true,
	session.StateRunning:       true,
	session.StateInputRequired: true,
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// AccrueSessionAge adds the interval since the previous write exactly when
// the row was in starting, running, or input_required at that write, for
// every state the session model defines. An accrual that counted suspended,
// resume_pending, resuming, or awaiting_client_action time fails here.
func TestAccrueSessionAgeCountsOnlyActiveStates_spec_6_2(t *testing.T) {
	prevAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := prevAt.Add(90 * time.Second)
	for _, st := range everyState() {
		t.Run(string(st), func(t *testing.T) {
			row := sessionstore.Session{State: session.StateRunning, AccumulatedSessionAgeSeconds: 10}
			sessionstore.AccrueSessionAge(st, prevAt, &row, now)
			want := 10.0
			if ageCountingStates[st] {
				want = 100
			}
			if row.AccumulatedSessionAgeSeconds != want {
				t.Fatalf("accumulated after an interval in %q = %v, want %v", st, row.AccumulatedSessionAgeSeconds, want)
			}
		})
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// A write stamped at or before the previous UpdatedAt accrues nothing, so a
// clock that does not advance never subtracts age.
func TestAccrueSessionAgeIgnoresNonAdvancingClock_spec_6_2(t *testing.T) {
	prevAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, now := range []time.Time{prevAt, prevAt.Add(-time.Minute)} {
		row := sessionstore.Session{AccumulatedSessionAgeSeconds: 5}
		sessionstore.AccrueSessionAge(session.StateRunning, prevAt, &row, now)
		if row.AccumulatedSessionAgeSeconds != 5 {
			t.Fatalf("accumulated with now %v = %v, want 5", now.Sub(prevAt), row.AccumulatedSessionAgeSeconds)
		}
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// ActiveAge is the accumulated value plus the time since the last write while
// the row is in a counting state, and the accumulated value alone otherwise.
// A sweep instant that precedes the row's UpdatedAt adds nothing.
func TestActiveAgeAddsElapsedOnlyInCountingStates_spec_6_2(t *testing.T) {
	updated := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := updated.Add(time.Hour)
	for _, st := range everyState() {
		t.Run(string(st), func(t *testing.T) {
			row := sessionstore.Session{State: st, UpdatedAt: updated, AccumulatedSessionAgeSeconds: 120}
			want := 2 * time.Minute
			if ageCountingStates[st] {
				want += time.Hour
			}
			if got := sessionstore.ActiveAge(row, now); got != want {
				t.Fatalf("ActiveAge in %q = %v, want %v", st, got, want)
			}
			if got := sessionstore.ActiveAge(row, updated.Add(-time.Minute)); got != 2*time.Minute {
				t.Fatalf("ActiveAge before UpdatedAt in %q = %v, want the accumulated 2m", st, got)
			}
		})
	}
}

// everyState is AllStates plus resuming, which the §15.1 polling envelope
// omits but the session row carries during a restore.
func everyState() []session.State {
	return append(session.AllStates(), session.StateResuming)
}
