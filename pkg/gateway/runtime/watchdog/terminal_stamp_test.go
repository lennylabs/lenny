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

// hookDuration is how long a dispatched post-terminal hook takes on the fake
// clock, standing in for a seal-and-export that runs while the pass moves on.
const hookDuration = 10 * time.Minute

// steppingClock is a fake clock the stepping store advances.
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

// steppingStore advances the clock by hookDuration before each Update, so
// every terminal write lands after an earlier row's dispatched hook has run
// for hookDuration past the pass's now.
type steppingStore struct {
	sessionstore.Store
	clock *steppingClock
}

func (s *steppingStore) Update(ctx context.Context, tenantID, id string, mut func(*sessionstore.Session) error) (sessionstore.Session, error) {
	s.clock.advance(hookDuration)
	return s.Store.Update(ctx, tenantID, id, mut)
}

// spec: §5.2 (Slot assignment atomicity)
//
// Each watchdog sweep that ends rows stamps terminated_at with the clock read
// inside each row's Update. The hooks of an earlier row, seal included, run on
// a dispatched goroutine while the pass goes on, so the pass's now can be
// earlier than a later row's terminal write by a hook's duration. Stamping the
// pass's now would date that write too early, and the stale-slot reaper could
// free the row's slot while its seal still runs.
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
			mem := memstore.New()
			for _, id := range []string{"s1", "s2"} {
				row := sessionstore.Session{
					ID: id, TenantID: "acme", State: tc.state, CreatedAt: born, UpdatedAt: born,
				}
				if tc.state == session.StateSuspended {
					row.SuspendedAt = born
				}
				if err := mem.Create(context.Background(), row); err != nil {
					t.Fatal(err)
				}
			}
			clk := &steppingClock{now: tc.at}
			hooks := &suspendedHooks{}
			w := watchdog.New(&steppingStore{Store: mem, clock: clk}, watchdog.StaticTenants{"acme"}, tc.cfg, clk.Now).
				WithTerminalHook(&captureLifecycleHook{}).
				WithSuspendedPodReleaser(hooks).
				WithBacklogResumer(hooks)
			if _, err := w.Tick(context.Background(), tc.at); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			assertStampedAtEachWrite(t, mem, tc.at)
		})
	}
}

// assertStampedAtEachWrite reads both rows and asserts each terminal write is
// dated by the clock at its own Update rather than by the pass's now.
func assertStampedAtEachWrite(t *testing.T, store sessionstore.Store, passStart time.Time) {
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
	for i, stamp := range stamps {
		if want := passStart.Add(time.Duration(i+1) * hookDuration); !stamp.Equal(want) {
			t.Errorf("terminated_at #%d = %s, want %s (the clock at its own Update)", i+1, stamp, want)
		}
	}
}

// spec: §6.2 (Pod State Machine), §7.2 (Session State Machine), §8.2
// (LeaseSlice perChildMaxAge)
//
// A watchdog expiry writer stamps its own reason on the row, replacing any
// reason an earlier recovery left. A row that carries a funnel reason from
// an earlier resume_pending or awaiting_client_action episode is expired by
// the awaiting_client_action deadline with `expired:deadline`, by the idle
// expiry with `expired:idle`, by the maxSessionAge cap with `expired:deadline`,
// and by the perChildMaxAge lease leg with `expired:lease`. Before the fix each
// writer kept the stale reason, so the client saw the recovery's cause instead
// of the expiry's.
func TestWatchdogExpiryWritersStampTheirOwnReason_spec_7_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	const funnelReason = "runtime_crash"
	for _, tc := range []struct {
		name  string
		cfg   watchdog.Config
		state session.State
		at    time.Time
		want  session.FailureReason
		// child makes the row a delegated child of a running root with a
		// 600-second perChildMaxAge slice.
		child bool
	}{
		{"the awaiting_client_action deadline", watchdog.Config{}, session.StateAwaitingClientAction, born.Add(20 * time.Minute), session.FailureExpiredDeadline, false},
		{"the idle expiry", idleCfg(600), session.StateRunning, born.Add(601 * time.Second), session.FailureExpiredIdle, false},
		{"the maxSessionAge cap", ageCfg(600), session.StateRunning, born.Add(601 * time.Second), session.FailureExpiredDeadline, false},
		{"the perChildMaxAge lease leg", leaseCfg(idleCapDisabled), session.StateAwaitingClientAction, born.Add(601 * time.Second), session.FailureExpiredLease, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.NewWithClock(func() time.Time { return tc.at })
			row := sessionstore.Session{
				ID: "sess", TenantID: "acme", State: tc.state, CreatedAt: born, UpdatedAt: born,
				LastAgentActivityAt: born, FailureReason: funnelReason,
			}
			if tc.child {
				mustCreate(t, store, sessionstore.Session{
					ID: "root", TenantID: "acme", State: session.StateRunning,
					CreatedAt: tc.at, UpdatedAt: tc.at, LastAgentActivityAt: tc.at,
				})
				row.ParentSessionID, row.DelegationDepth = "root", 1
				row.DelegationLease = &sessionstore.DelegationLease{PerChildMaxAge: 600}
			}
			if err := store.Create(context.Background(), row); err != nil {
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
