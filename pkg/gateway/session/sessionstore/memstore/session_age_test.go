// SPDX-License-Identifier: MIT

package memstore_test

import (
	"context"
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// steppedClock is a test clock the store reads for its UpdatedAt stamp.
type steppedClock struct{ now time.Time }

func (c *steppedClock) read() time.Time             { return c.now }
func (c *steppedClock) advance(d time.Duration)     { c.now = c.now.Add(d) }
func newSteppedClock(start time.Time) *steppedClock { return &steppedClock{now: start} }

// ageStep is one write in a session's history: the clock advances by after,
// then the write moves the row to state.
type ageStep struct {
	after time.Duration
	state session.State
}

func countsAge(st session.State) bool {
	return st == session.StateStarting || st == session.StateRunning || st == session.StateInputRequired
}

// replayAgeHistory creates a row in its first state, applies each step
// through Update on a stepped clock, and returns the stored row.
func replayAgeHistory(t *testing.T, first session.State, steps []ageStep) sessionstore.Session {
	t.Helper()
	ctx := context.Background()
	clock := newSteppedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := memstore.NewWithClock(clock.read)
	if err := st.Create(ctx, sessionstore.Session{
		ID: "sess_age", TenantID: "acme", State: first,
		CreatedAt: clock.now, UpdatedAt: clock.now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var row sessionstore.Session
	for i, s := range steps {
		clock.advance(s.after)
		next := s.state
		var err error
		row, err = st.Update(ctx, "acme", "sess_age", func(r *sessionstore.Session) error {
			r.State = next
			return nil
		})
		if err != nil {
			t.Fatalf("step %d Update: %v", i, err)
		}
	}
	return row
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// The store funnel accrues the starting, running, and input_required
// intervals of a row's history and no others. A write that keeps the row in
// running accrues the interval since the previous write. An accrual that
// counted time in suspended, resume_pending, resuming, or
// awaiting_client_action fails here.
func TestUpdateAccruesOnlyActiveIntervals_spec_6_2(t *testing.T) {
	cases := []struct {
		name  string
		first session.State
		steps []ageStep
		want  time.Duration
	}{
		{
			name:  "pre-running states accrue nothing",
			first: session.StateCreated,
			steps: []ageStep{
				{time.Minute, session.StateFinalizing},
				{2 * time.Minute, session.StateReady},
				{3 * time.Minute, session.StateStarting},
			},
			want: 0,
		},
		{
			name:  "starting and running both count",
			first: session.StateStarting,
			steps: []ageStep{
				{10 * time.Second, session.StateRunning},
				{30 * time.Second, session.StateInputRequired},
				{5 * time.Second, session.StateRunning},
			},
			want: 45 * time.Second,
		},
		{
			name:  "a write that stays running accrues the interval",
			first: session.StateRunning,
			steps: []ageStep{
				{20 * time.Second, session.StateRunning},
				{40 * time.Second, session.StateRunning},
			},
			want: time.Minute,
		},
		{
			name:  "suspended pauses the timer",
			first: session.StateRunning,
			steps: []ageStep{
				{time.Minute, session.StateSuspended},
				{24 * time.Hour, session.StateRunning},
				{time.Minute, session.StateSuspended},
			},
			want: 2 * time.Minute,
		},
		{
			name:  "recovery states pause the timer",
			first: session.StateRunning,
			steps: []ageStep{
				{time.Minute, session.StateResumePending},
				{time.Hour, session.StateResuming},
				{5 * time.Minute, session.StateAwaitingClientAction},
				{3 * time.Hour, session.StateResumePending},
				{time.Minute, session.StateResuming},
				{time.Minute, session.StateRunning},
				{7 * time.Second, session.StateCompleted},
			},
			want: time.Minute + 7*time.Second,
		},
		{
			name:  "terminal stops the timer",
			first: session.StateRunning,
			steps: []ageStep{
				{time.Minute, session.StateExpired},
				{time.Hour, session.StateExpired},
			},
			want: time.Minute,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := replayAgeHistory(t, tc.first, tc.steps)
			if got := time.Duration(row.AccumulatedSessionAgeSeconds * float64(time.Second)); got != tc.want {
				t.Fatalf("accumulated active age = %v, want %v", got, tc.want)
			}
		})
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// Over randomized histories through every session state, the accumulated
// value equals the sum of the intervals the row spent in a counting state,
// computed independently of the store. The seed is logged so a failing
// history replays exactly.
func TestUpdateAccrualMatchesModelOverRandomHistories_spec_6_2(t *testing.T) {
	// resuming is absent from AllStates (the §15.1 polling envelope omits
	// it) but the row carries it during a restore.
	states := append(session.AllStates(), session.StateResuming)
	for run := 0; run < 200; run++ {
		seed := int64(run) + 1
		rng := rand.New(rand.NewSource(seed))
		first := states[rng.Intn(len(states))]
		steps := make([]ageStep, 1+rng.Intn(20))
		var want time.Duration
		cur := first
		for i := range steps {
			steps[i] = ageStep{
				after: time.Duration(rng.Intn(3600)) * time.Second,
				state: states[rng.Intn(len(states))],
			}
			if countsAge(cur) {
				want += steps[i].after
			}
			cur = steps[i].state
		}
		row := replayAgeHistory(t, first, steps)
		got := row.AccumulatedSessionAgeSeconds
		// A zero interval still advances UpdatedAt by the store's 1ns
		// monotonic clamp, so allow that much per step.
		if math.Abs(got-want.Seconds()) > float64(len(steps))*1e-9+1e-9 {
			t.Fatalf("seed %d: accumulated %vs, want %vs (history from %q: %+v)", seed, got, want.Seconds(), first, steps)
		}
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// The store is the only writer of the accumulated age: a mutator that zeroes
// or inflates it is overridden by the funnel's own accrual, so a caller can
// neither reset a session's age budget nor spend it.
func TestUpdateIgnoresMutatorWritesToAccumulatedAge_spec_6_2(t *testing.T) {
	ctx := context.Background()
	clock := newSteppedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := memstore.NewWithClock(clock.read)
	if err := st.Create(ctx, sessionstore.Session{
		ID: "sess_age", TenantID: "acme", State: session.StateRunning,
		CreatedAt: clock.now, UpdatedAt: clock.now, AccumulatedSessionAgeSeconds: 30,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, forced := range []float64{0, 1e9} {
		clock.advance(10 * time.Second)
		before, _ := st.Get(ctx, "acme", "sess_age")
		row, err := st.Update(ctx, "acme", "sess_age", func(r *sessionstore.Session) error {
			r.AccumulatedSessionAgeSeconds = forced
			return nil
		})
		if err != nil {
			t.Fatalf("Update: %v", err)
		}
		if want := before.AccumulatedSessionAgeSeconds + 10; row.AccumulatedSessionAgeSeconds != want {
			t.Fatalf("mutator wrote %v; accumulated = %v, want %v", forced, row.AccumulatedSessionAgeSeconds, want)
		}
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// A failed mutator writes nothing, so it neither accrues age nor advances
// UpdatedAt; the interval stays unaccrued until the next successful write.
func TestFailedMutatorAccruesNothing_spec_6_2(t *testing.T) {
	ctx := context.Background()
	clock := newSteppedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	st := memstore.NewWithClock(clock.read)
	if err := st.Create(ctx, sessionstore.Session{
		ID: "sess_age", TenantID: "acme", State: session.StateRunning,
		CreatedAt: clock.now, UpdatedAt: clock.now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	clock.advance(time.Minute)
	if _, err := st.Update(ctx, "acme", "sess_age", func(*sessionstore.Session) error {
		return sessionstore.ErrNotFound
	}); err == nil {
		t.Fatal("Update with a failing mutator returned nil")
	}
	row, _ := st.Get(ctx, "acme", "sess_age")
	if row.AccumulatedSessionAgeSeconds != 0 || !row.UpdatedAt.Equal(clock.now.Add(-time.Minute)) {
		t.Fatalf("after a failed mutator: accumulated %v, UpdatedAt %v; want 0 and the create instant", row.AccumulatedSessionAgeSeconds, row.UpdatedAt)
	}
	clock.advance(time.Minute)
	row, err := st.Update(ctx, "acme", "sess_age", func(*sessionstore.Session) error { return nil })
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if row.AccumulatedSessionAgeSeconds != 120 {
		t.Fatalf("accumulated after the next write = %v, want 120", row.AccumulatedSessionAgeSeconds)
	}
}
