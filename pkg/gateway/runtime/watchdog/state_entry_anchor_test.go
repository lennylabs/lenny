// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// anchorCase is one per-state deadline the watchdog measures from the
// session's entry to its state.
type anchorCase struct {
	name   string
	state  session.State
	cfg    watchdog.Config
	budget time.Duration
	// seed adjusts the seeded row, such as clearing SuspendedAt.
	seed func(*sessionstore.Session)
	// dlq routes the case through the dead-letter-queue TTL sweep, whose
	// outcome is the sweep call rather than a state change.
	dlq bool
}

func anchorCases() []anchorCase {
	sec := func(n int) time.Duration { return time.Duration(n) * time.Second }
	return []anchorCase{
		{name: "created", state: session.StateCreated, budget: sec(watchdog.DefaultMaxCreatedStateSeconds)},
		{name: "finalizing", state: session.StateFinalizing, budget: sec(watchdog.DefaultMaxFinalizingStateSeconds)},
		{name: "ready", state: session.StateReady, budget: sec(watchdog.DefaultMaxReadyStateSeconds)},
		{name: "starting", state: session.StateStarting, budget: sec(watchdog.DefaultMaxStartingStateSeconds)},
		{name: "resume_pending window", state: session.StateResumePending, budget: sec(watchdog.DefaultMaxResumePendingSeconds)},
		{name: "resuming watchdog", state: session.StateResuming, budget: sec(watchdog.DefaultMaxResumingSeconds)},
		{name: "awaiting_client_action deadline", state: session.StateAwaitingClientAction, budget: sec(watchdog.DefaultMaxAwaitingClientActionSeconds)},
		{name: "DLQ TTL", state: session.StateResumePending, cfg: dlqCfg(), budget: sessioninbox.DefaultDLQTTL, dlq: true},
		{name: "idle fallback with no recorded activity", state: session.StateRunning, cfg: idleCfg(600), budget: sec(600)},
		{
			name: "suspended lifetime with a zero SuspendedAt", state: session.StateSuspended,
			cfg: suspendedCfg(), budget: sec(testLifetime),
			seed: func(r *sessionstore.Session) { r.SuspendedAt = time.Time{} },
		},
	}
}

// runAnchorCase seeds one row whose state entry is entered and whose last
// write is updated, runs one Tick at now, and reports whether the deadline
// fired for it.
func runAnchorCase(t *testing.T, tc anchorCase, entered, updated, now time.Time) bool {
	t.Helper()
	store := memstore.NewWithClock(func() time.Time { return now })
	// Create stamps StateEnteredAt from CreatedAt and keeps the caller's
	// UpdatedAt, so the two instants are set independently here.
	row := sessionstore.Session{
		ID: "sess", TenantID: "acme", State: tc.state, CreatedAt: entered, UpdatedAt: updated,
	}
	if tc.seed != nil {
		tc.seed(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sweeper := &fakeDLQSweeper{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, tc.cfg, func() time.Time { return now }).
		WithTerminalHook(&captureLifecycleHook{})
	if tc.dlq {
		w = w.WithMessaging(sweeper)
	}
	if _, err := w.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if tc.dlq {
		return slices.Contains(sweeper.swept, "sess")
	}
	got, err := store.Get(context.Background(), "acme", "sess")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	return got.State != tc.state
}

// spec: §6.2 (Pod State Machine)
//
// Every per-state deadline the watchdog enforces runs from the persisted
// state_entered_at instant. A row that entered its state past the budget is
// timed out although a later write left its UpdatedAt recent, and a row that
// entered its state inside the budget survives although its UpdatedAt is old.
// The second half discriminates: measured from UpdatedAt, it would fire.
func TestWatchdogMeasuresEachDeadlineFromStateEntry_spec_6_2(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, tc := range anchorCases() {
		t.Run(tc.name, func(t *testing.T) {
			old := now.Add(-(tc.budget + time.Minute))
			recent := now.Add(-time.Second)
			if !runAnchorCase(t, tc, old, recent, now) {
				t.Errorf("a row that entered %s %s ago, with a write 1s ago, did not time out", tc.state, tc.budget+time.Minute)
			}
			if runAnchorCase(t, tc, recent, old, now) {
				t.Errorf("a row that entered %s 1s ago timed out on its UpdatedAt %s ago", tc.state, tc.budget+time.Minute)
			}
		})
	}
}

// spec: §10.1.1 (Primary:)
//
// A pre-running row that leaves its listed state and re-enters it between
// the sweep's listing and its write is inside its budget on the locked row,
// so the write is superseded: the row keeps its state, the pass counts no
// failure, and no terminal hook runs.
func TestPreRunningExpiryRechecksTheBudgetOnTheLockedRow_spec_10_1_1(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	inner := memstore.NewWithClock(func() time.Time { return now })
	seedState(t, inner, "sess", session.StateReady, now.Add(-time.Hour), nil)
	store := &afterListStore{Store: inner, state: session.StateReady, after: func() {
		reenter(t, inner, session.StateStarting, session.StateReady)
	}}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, func() time.Time { return now }).
		WithTerminalHook(hook)
	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	assertSuperseded(t, inner, session.StateReady, hook)
	if res.ForcedFailures != 0 {
		t.Errorf("ForcedFailures = %d, want 0 for a row that re-entered ready", res.ForcedFailures)
	}
}

// spec: §10.1.1 (Primary:)
//
// An awaiting_client_action row that leaves the state and re-enters it
// between the sweep's listing and its write has a fresh deadline on the
// locked row, so the expiry is superseded and runs no hook.
func TestAwaitingExpiryRechecksTheDeadlineOnTheLockedRow_spec_10_1_1(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	inner := memstore.NewWithClock(func() time.Time { return now })
	seedState(t, inner, "sess", session.StateAwaitingClientAction, now.Add(-time.Hour), nil)
	store := &afterListStore{Store: inner, state: session.StateAwaitingClientAction, after: func() {
		reenter(t, inner, session.StateResumePending, session.StateAwaitingClientAction)
	}}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, func() time.Time { return now }).
		WithTerminalHook(hook)
	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	assertSuperseded(t, inner, session.StateAwaitingClientAction, hook)
	if res.Expirations != 0 {
		t.Errorf("Expirations = %d, want 0 for a row that re-entered awaiting_client_action", res.Expirations)
	}
}

// spec: §10.1.1 (Primary:), §6.2 (maxClientIdleSeconds clock behavior across states)
//
// A qualifying activity stamp that lands between the idle sweep's listing
// and its write puts the locked row inside the idle cap, so the idle expiry
// is superseded and runs no hook.
func TestIdleExpiryRechecksTheIdleCapOnTheLockedRow_spec_10_1_1(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	inner := memstore.NewWithClock(func() time.Time { return now })
	seedState(t, inner, "sess", session.StateRunning, now.Add(-time.Hour), func(r *sessionstore.Session) {
		r.LastAgentActivityAt = now.Add(-time.Hour)
	})
	store := &afterListStore{Store: inner, state: session.StateRunning, after: func() {
		if _, err := inner.Update(context.Background(), "acme", "sess", func(r *sessionstore.Session) error {
			r.LastAgentActivityAt = now
			return nil
		}); err != nil {
			t.Errorf("activity stamp: %v", err)
		}
	}}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), func() time.Time { return now }).
		WithTerminalHook(hook)
	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	assertSuperseded(t, inner, session.StateRunning, hook)
	if res.IdleExpirations != 0 {
		t.Errorf("IdleExpirations = %d, want 0 for a row stamped active after the listing", res.IdleExpirations)
	}
}

// reenter moves the seeded row through via and back to back, as a recovery
// round trip landing between a sweep's listing and its write.
func reenter(t *testing.T, store sessionstore.Store, via, back session.State) {
	t.Helper()
	for _, st := range []session.State{via, back} {
		if _, err := store.Update(context.Background(), "acme", "sess", func(r *sessionstore.Session) error {
			r.State = st
			return nil
		}); err != nil {
			t.Errorf("move to %s: %v", st, err)
		}
	}
}

// assertSuperseded asserts the row kept want and the sweep ran no terminal hook.
func assertSuperseded(t *testing.T, store sessionstore.Store, want session.State, hook *captureLifecycleHook) {
	t.Helper()
	got, err := store.Get(context.Background(), "acme", "sess")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.State != want || !got.TerminatedAt.IsZero() {
		t.Errorf("row = %q terminated %v, want %q and not terminated", got.State, got.TerminatedAt, want)
	}
	if len(hook.terminalCalls) != 0 {
		t.Errorf("terminal hook ran %d times, want 0 for a superseded write", len(hook.terminalCalls))
	}
}
