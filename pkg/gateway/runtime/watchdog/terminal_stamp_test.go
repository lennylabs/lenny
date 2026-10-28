// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// hookDuration is how long the fake post-terminal hook takes on the fake
// clock, standing in for a seal-and-export.
const hookDuration = 10 * time.Minute

// steppingClock is a fake clock the post-terminal hook advances.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *steppingClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// slowHook is a post-terminal hook that takes hookDuration on the clock.
type slowHook struct{ clock *steppingClock }

func (*slowHook) BeginTerminal(string) func() { return func() {} }

func (h *slowHook) OnSessionTerminal(context.Context, session.State, sessionstore.Session) {
	h.clock.advance(hookDuration)
}

// spec: §5.2 (Slot assignment atomicity)
//
// Each watchdog sweep that ends rows stamps terminated_at with the clock read
// inside each row's Update. A pass runs one row's post-terminal hook, seal
// included, before the next row's write, so stamping the pass's now would
// date the second row's terminal write by the first hook's duration too
// early, and the stale-slot reaper could free the second row's slot while
// its seal still runs.
func TestWatchdogStampsEachTerminalWriteWithItsOwnClockRead_spec_5_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name  string
		cfg   watchdog.Config
		state session.State
		at    time.Time
	}{
		{"the pre-running sweep", watchdog.Config{}, session.StateCreated, born.Add(310 * time.Second)},
		{"the idle expiry", idleCfg(600), session.StateRunning, born.Add(601 * time.Second)},
		{"the maxSessionAge sweep", watchdog.Config{
			MaxIdleSeconds: idleCapDisabled, MaxSessionAgeSeconds: 600,
			MaxAwaitingClientActionSeconds: idleCapDisabled, MaxSuspendedPodHoldSeconds: idleCapDisabled,
			MaxResumePendingSeconds: idleCapDisabled,
		}, session.StateRunning, born.Add(601 * time.Second)},
		{"the awaiting_client_action sweep", watchdog.Config{}, session.StateAwaitingClientAction, born.Add(20 * time.Minute)},
		{"the suspended lifetime", suspendedCfg(), session.StateSuspended, born.Add(testLifetime * time.Second)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			for _, id := range []string{"s1", "s2"} {
				row := sessionstore.Session{
					ID: id, TenantID: "acme", State: tc.state, CreatedAt: born, UpdatedAt: born,
				}
				if tc.state == session.StateSuspended {
					row.SuspendedAt = born
				}
				if err := store.Create(context.Background(), row); err != nil {
					t.Fatal(err)
				}
			}
			clk := &steppingClock{now: tc.at}
			hooks := &suspendedHooks{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, tc.cfg, clk.Now).
				WithTerminalHook(&slowHook{clock: clk}).
				WithSuspendedPodReleaser(hooks).
				WithBacklogResumer(hooks)
			if _, err := w.Tick(context.Background(), tc.at); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			assertStampedAfterTheEarlierHook(t, store, tc.at)
		})
	}
}

// assertStampedAfterTheEarlierHook reads both rows and asserts the second
// terminal write is dated after the first row's hook returned.
func assertStampedAfterTheEarlierHook(t *testing.T, store sessionstore.Store, passStart time.Time) {
	t.Helper()
	var stamps []time.Time
	for _, id := range []string{"s1", "s2"} {
		row, err := store.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatal(err)
		}
		if !session.IsTerminal(row.State) {
			t.Fatalf("row %s state = %q, want terminal", id, row.State)
		}
		stamps = append(stamps, row.TerminatedAt)
	}
	sort.Slice(stamps, func(i, j int) bool { return stamps[i].Before(stamps[j]) })
	if !stamps[0].Equal(passStart) {
		t.Errorf("first terminated_at = %s, want the pass start %s", stamps[0], passStart)
	}
	if want := passStart.Add(hookDuration); !stamps[1].Equal(want) {
		t.Errorf("second terminated_at = %s, want %s (after the first row's hook returned)", stamps[1], want)
	}
}

// spec: §6.2 (Pod State Machine), §7.2 (Session State Machine)
//
// A watchdog expiry writer stamps its own reason on the row, replacing any
// reason an earlier recovery left. A row that carries a funnel reason from
// an earlier resume_pending or awaiting_client_action episode is expired by
// the awaiting_client_action deadline with `expired:deadline` and by the idle
// expiry with `expired:idle`. Before the fix each writer kept the stale
// reason, so the client saw the recovery's cause instead of the expiry's.
func TestWatchdogExpiryWritersStampTheirOwnReason_spec_7_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const funnelReason = "runtime_crash"
	for _, tc := range []struct {
		name  string
		cfg   watchdog.Config
		state session.State
		at    time.Time
		want  session.FailureReason
	}{
		{"the awaiting_client_action deadline", watchdog.Config{}, session.StateAwaitingClientAction, born.Add(20 * time.Minute), session.FailureExpiredDeadline},
		{"the idle expiry", idleCfg(600), session.StateRunning, born.Add(601 * time.Second), session.FailureExpiredIdle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.NewWithClock(func() time.Time { return tc.at })
			if err := store.Create(context.Background(), sessionstore.Session{
				ID: "sess", TenantID: "acme", State: tc.state, CreatedAt: born, UpdatedAt: born,
				FailureReason: funnelReason,
			}); err != nil {
				t.Fatal(err)
			}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, tc.cfg, nil).
				WithTerminalHook(&captureLifecycleHook{})
			if _, err := w.Tick(context.Background(), tc.at); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			got, err := store.Get(context.Background(), "acme", "sess")
			if err != nil {
				t.Fatal(err)
			}
			if got.State != session.StateExpired || got.FailureReason != string(tc.want) {
				t.Errorf("row = %q reason %q, want expired with %q over the earlier %q",
					got.State, got.FailureReason, tc.want, funnelReason)
			}
		})
	}
}
