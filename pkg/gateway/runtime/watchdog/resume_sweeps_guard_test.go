// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// driverHook is a terminal hook that is also the watchdog's ResumeDriver and
// records each row the watchdog hands it.
type driverHook struct {
	captureLifecycleHook
	mu     sync.Mutex
	driven []string
}

func (d *driverHook) DriveResume(_ context.Context, row sessionstore.Session) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.driven = append(d.driven, row.ID)
}

// afterListStore runs once after the first List of the given state returns,
// so a test can land a concurrent write between a sweep's listing and its
// guarded write.
type afterListStore struct {
	sessionstore.Store
	state session.State
	once  sync.Once
	after func()
}

func (a *afterListStore) List(ctx context.Context, tenant string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	rows, err := a.Store.List(ctx, tenant, f)
	if f.State == a.state {
		a.once.Do(a.after)
	}
	return rows, err
}

func seedState(t *testing.T, store sessionstore.Store, id string, st session.State, updated time.Time, mut func(*sessionstore.Session)) {
	t.Helper()
	row := sessionstore.Session{ID: id, TenantID: "acme", State: st, CreatedAt: updated, UpdatedAt: updated}
	if mut != nil {
		mut(&row)
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestSweepResumePendingDrivesInsideTheWindowAndGuardsExpiry_spec_7_3 asserts
// that the watchdog hands every resume_pending row inside its window to the
// resume driver, expires a row whose window passed, and applies the expiry
// only when the locked row is still resume_pending with StateEnteredAt past
// the window: a row that left the state, or left and re-entered it, after the
// listing keeps the concurrent write and gets no timeout count and no
// notification. A write that leaves the row in resume_pending does not
// restart the window, so that row is still expired.
//
// spec: §6.2 (Pod State Machine), §7.3 (Resume driver)
func TestSweepResumePendingDrivesInsideTheWindowAndGuardsExpiry_spec_7_3(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(901 * time.Second)

	t.Run("drive and expire", func(t *testing.T) {
		store := memstore.New()
		seedState(t, store, "inside-a", session.StateResumePending, now.Add(-10*time.Second), nil)
		seedState(t, store, "inside-b", session.StateResumePending, now.Add(-899*time.Second), nil)
		seedState(t, store, "expired", session.StateResumePending, born, nil)
		hook := &driverHook{}
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).WithTerminalHook(hook)
		res, err := w.Tick(context.Background(), now)
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if len(hook.driven) != 2 {
			t.Errorf("driven = %v, want both rows inside the window", hook.driven)
		}
		for _, id := range hook.driven {
			if id == "expired" {
				t.Error("the expired row was handed to the driver")
			}
		}
		if res.ResumePendingTimeouts != 1 || len(hook.awaitingEntry) != 1 {
			t.Errorf("timeouts %d, notifications %d; want 1 and 1", res.ResumePendingTimeouts, len(hook.awaitingEntry))
		}
	})

	for _, tc := range []struct {
		name  string
		write func(r *sessionstore.Session)
		// reenter writes resume_pending again after write, so the row
		// re-enters the state after the listing.
		reenter bool
	}{
		{"window restarted by a re-entry", func(r *sessionstore.Session) {
			r.State = session.StateResuming
			r.CoordinationGeneration++
		}, true},
		{"row moved to resuming", func(r *sessionstore.Session) { r.State = session.StateResuming; r.CoordinationGeneration++ }, false},
		{"row cancelled", func(r *sessionstore.Session) {
			r.State = session.StateCancelled
			sessionstore.MarkTerminated(r, session.StateResumePending, time.Now())
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.NewWithClock(func() time.Time { return now })
			seedState(t, inner, "sess", session.StateResumePending, born, nil)
			var written sessionstore.Session
			store := &afterListStore{Store: inner, state: session.StateResumePending, after: func() {
				written, _ = inner.Update(context.Background(), "acme", "sess", func(r *sessionstore.Session) error {
					tc.write(r)
					return nil
				})
				if tc.reenter {
					written, _ = inner.Update(context.Background(), "acme", "sess", func(r *sessionstore.Session) error {
						r.State = session.StateResumePending
						return nil
					})
				}
			}}
			hook := &driverHook{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			got, _ := inner.Get(context.Background(), "acme", "sess")
			if got.State != written.State || !got.UpdatedAt.Equal(written.UpdatedAt) {
				t.Errorf("row = %q updated %v, want the concurrent write's %q updated %v",
					got.State, got.UpdatedAt, written.State, written.UpdatedAt)
			}
			if res.ResumePendingTimeouts != 0 || len(hook.awaitingEntry) != 0 {
				t.Errorf("timeouts %d, notifications %d; want none for a superseded row",
					res.ResumePendingTimeouts, len(hook.awaitingEntry))
			}
		})
	}

	// spec: §6.2 (Pod State Machine) — the window runs from the entry to
	// resume_pending, so a same-state write after the listing does not
	// restart it and the expiry still applies.
	t.Run("same-state write does not restart the window", func(t *testing.T) {
		inner := memstore.NewWithClock(func() time.Time { return now })
		seedState(t, inner, "sess", session.StateResumePending, born, nil)
		store := &afterListStore{Store: inner, state: session.StateResumePending, after: func() {
			if _, err := inner.Update(context.Background(), "acme", "sess", func(*sessionstore.Session) error { return nil }); err != nil {
				t.Errorf("same-state write: %v", err)
			}
		}}
		hook := &driverHook{}
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).WithTerminalHook(hook)
		res, err := w.Tick(context.Background(), now)
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		got, _ := inner.Get(context.Background(), "acme", "sess")
		if got.State != session.StateAwaitingClientAction || res.ResumePendingTimeouts != 1 || len(hook.awaitingEntry) != 1 {
			t.Errorf("row %q, timeouts %d, notifications %d; want awaiting_client_action, 1, and 1",
				got.State, res.ResumePendingTimeouts, len(hook.awaitingEntry))
		}
	})
}

// TestSweepResumingExitsOnlyTheListedIncarnation_spec_6_2 asserts that the
// resuming watchdog exits only the incarnation it listed: a row that
// re-entered resuming at a higher coordination_generation, or that a failure
// report already moved to resume_pending, is left unchanged with no
// notification, and two watchdogs over one store exit one incarnation at most
// once, so RetryCount never exceeds maxRetries.
//
// spec: §6.2 (Pod State Machine), §7.3 (Retry and Resume)
func TestSweepResumingExitsOnlyTheListedIncarnation_spec_6_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(301 * time.Second)
	for _, tc := range []struct {
		name  string
		write func(r *sessionstore.Session)
	}{
		{"re-entered at a higher generation", func(r *sessionstore.Session) { r.CoordinationGeneration++ }},
		{"moved to resume_pending by a report", func(r *sessionstore.Session) {
			r.State = session.StateResumePending
			r.RetryCount++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := memstore.New()
			seedState(t, inner, "sess", session.StateResuming, born, func(r *sessionstore.Session) {
				r.CoordinationGeneration = 5
				r.RetryCount = 1
			})
			var written sessionstore.Session
			store := &afterListStore{Store: inner, state: session.StateResuming, after: func() {
				written, _ = inner.Update(context.Background(), "acme", "sess", func(r *sessionstore.Session) error {
					tc.write(r)
					return nil
				})
			}}
			hook := &driverHook{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxRetries: 2}, nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			got, _ := inner.Get(context.Background(), "acme", "sess")
			if got.State != written.State || got.RetryCount != written.RetryCount ||
				got.CoordinationGeneration != written.CoordinationGeneration || !got.UpdatedAt.Equal(written.UpdatedAt) {
				t.Errorf("row = %q retry %d gen %d, want the concurrent write's %q retry %d gen %d unchanged",
					got.State, got.RetryCount, got.CoordinationGeneration, written.State, written.RetryCount, written.CoordinationGeneration)
			}
			if res.ResumingTimeouts != 0 || len(hook.retryAttempts) != 0 || len(hook.awaitingEntry) != 0 {
				t.Errorf("timeouts %d, retry notifications %d, awaiting notifications %d; want none",
					res.ResumingTimeouts, len(hook.retryAttempts), len(hook.awaitingEntry))
			}
		})
	}

	t.Run("two watchdogs exit one incarnation once", func(t *testing.T) {
		for _, startRetry := range []int64{1, 2} {
			inner := memstore.New()
			seedState(t, inner, "sess", session.StateResuming, born, func(r *sessionstore.Session) {
				r.CoordinationGeneration = 5
				r.RetryCount = startRetry
			})
			hookA, hookB := &driverHook{}, &driverHook{}
			var b *watchdog.Watchdog
			// Watchdog B lists the same incarnation A lists, then runs
			// after A's exit landed.
			storeA := &afterListStore{Store: inner, state: session.StateResuming, after: func() {}}
			a := watchdog.New(storeA, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxRetries: 2}, nil).WithTerminalHook(hookA)
			storeB := &afterListStore{Store: inner, state: session.StateResuming, after: func() {
				if _, err := a.Tick(context.Background(), now); err != nil {
					t.Errorf("Tick A: %v", err)
				}
			}}
			b = watchdog.New(storeB, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxRetries: 2}, nil).WithTerminalHook(hookB)
			resB, err := b.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick B: %v", err)
			}
			got, _ := inner.Get(context.Background(), "acme", "sess")
			exits := len(hookA.retryAttempts) + len(hookA.awaitingEntry) + len(hookB.retryAttempts) + len(hookB.awaitingEntry)
			if exits != 1 || resB.ResumingTimeouts != 0 {
				t.Errorf("retry %d: exits = %d (B timeouts %d), want exactly one exit, by A", startRetry, exits, resB.ResumingTimeouts)
			}
			if got.RetryCount > 2 {
				t.Errorf("retry %d: RetryCount = %d, want at most maxRetries 2", startRetry, got.RetryCount)
			}
		}
	})
}

// TestIdleExpiryOfARowWithAnEarlierReasonRunsTheHook_spec_7_2 asserts that the
// idle expiry of a running row that already carries a FailureReason applies
// and runs the terminal hook exactly once: the writer decides on its Update's
// nil return, not on the reason the returned row carries.
//
// spec: §7.2 (Interactive Session Model), §12.3 (Postgres HA Requirements)
func TestIdleExpiryOfARowWithAnEarlierReasonRunsTheHook_spec_7_2(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedState(t, store, "sess", session.StateRunning, born, func(r *sessionstore.Session) {
		r.FailureReason = "runtime_crash"
		r.LastAgentActivityAt = born
	})
	hook := &driverHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxIdleSeconds: 60, MaxSessionAgeSeconds: 100000,
	}, nil).WithTerminalHook(hook)
	if _, err := w.Tick(context.Background(), born.Add(time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, _ := store.Get(context.Background(), "acme", "sess")
	if got.State != session.StateExpired || got.TerminatedAt.IsZero() {
		t.Errorf("row = %q (terminated %v), want expired with the Terminated fact", got.State, got.TerminatedAt)
	}
	if len(hook.terminalCalls) != 1 {
		t.Errorf("terminal hook calls = %d, want exactly one", len(hook.terminalCalls))
	}
}
