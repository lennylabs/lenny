// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// ageCfg isolates the maxSessionAge sweep: the age cap is capSeconds and
// every other expiry clock is set far past anything these tests reach.
func ageCfg(capSeconds int) watchdog.Config {
	return watchdog.Config{
		MaxSessionAgeSeconds:           capSeconds,
		MaxIdleSeconds:                 idleCapDisabled,
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     idleCapDisabled,
		MaxResumePendingSeconds:        idleCapDisabled,
		MaxResumingSeconds:             idleCapDisabled,
	}
}

// staleListStore serves List from a fixed snapshot, the way a read-pool
// listing can lag the primary, while Get and Update reach the live rows.
type staleListStore struct {
	sessionstore.Store
	listed []sessionstore.Session
}

func (s *staleListStore) List(_ context.Context, _ string, f sessionstore.ListFilter) ([]sessionstore.Session, error) {
	out := make([]sessionstore.Session, 0, len(s.listed))
	for _, r := range s.listed {
		if f.State == "" || r.State == f.State {
			out = append(out, r)
		}
	}
	return out, nil
}

// failingUpdateStore fails every Update with err.
type failingUpdateStore struct {
	sessionstore.Store
	err error
}

func (s *failingUpdateStore) Update(context.Context, string, string, func(*sessionstore.Session) error) (sessionstore.Session, error) {
	return sessionstore.Session{}, s.err
}

func mustGet(t *testing.T, store sessionstore.Store, id string) sessionstore.Session {
	t.Helper()
	row, err := store.Get(context.Background(), "acme", id)
	if err != nil {
		t.Fatalf("Get %s: %v", id, err)
	}
	return row
}

// spec: 6.2 (maxSessionAge timer behavior across states), 7.2 (Session State
// Machine)
//
// The watchdog expires by active age. A session older than the cap by wall
// clock and under it by active time survives; a session whose active age is
// over the cap expires. A suspended row over the cap is not expired, and
// starting, resume_pending, resuming, and awaiting_client_action rows over the
// cap are expired with `expired:deadline` on their §7.2 maxSessionAge expiry
// edges. A paused row older than the cap by wall clock with no active age
// survives.
func TestSweepMaxAgeUsesActiveAge_spec_6_2(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const capSeconds = 7200
	cases := []struct {
		name        string
		state       session.State
		createdAgo  time.Duration
		updatedAgo  time.Duration
		accumulated time.Duration
		wantExpired bool
	}{
		{"running old by wall clock, young by active time", session.StateRunning, 3 * time.Hour, 30 * time.Minute, 10 * time.Minute, false},
		{"running over the cap by active time", session.StateRunning, 3 * time.Hour, time.Hour, 90 * time.Minute, true},
		{"input_required over the cap by active time", session.StateInputRequired, 3 * time.Hour, 2 * time.Hour, time.Minute, true},
		{"starting counts toward the cap", session.StateStarting, time.Minute, 30 * time.Second, 2 * time.Hour, true},
		{"running at the cap is not over it", session.StateRunning, 4 * time.Hour, time.Hour, time.Hour, false},
		{"suspended over the cap is never expired", session.StateSuspended, 48 * time.Hour, 24 * time.Hour, 3 * time.Hour, false},
		{"resume_pending over the cap is expired", session.StateResumePending, 4 * time.Hour, time.Minute, 3 * time.Hour, true},
		{"awaiting_client_action over the cap is expired", session.StateAwaitingClientAction, 4 * time.Hour, time.Minute, 3 * time.Hour, true},
		{"resume_pending old by wall clock with no active age", session.StateResumePending, 10 * time.Hour, time.Minute, 0, false},
		{"resuming paused for hours", session.StateResuming, 10 * time.Hour, time.Minute, time.Hour, false},
		{"resuming over the cap is expired", session.StateResuming, 4 * time.Hour, time.Minute, 3 * time.Hour, true},
		{"awaiting_client_action under the cap", session.StateAwaitingClientAction, 10 * time.Hour, time.Minute, time.Hour, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			mustCreate(t, store, sessionstore.Session{
				ID: "sess", TenantID: "acme", State: tc.state,
				CreatedAt:                    now.Add(-tc.createdAgo),
				UpdatedAt:                    now.Add(-tc.updatedAgo),
				LastAgentActivityAt:          now.Add(-time.Second),
				AccumulatedSessionAgeSeconds: tc.accumulated.Seconds(),
			})
			hook := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(capSeconds), nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			row := mustGet(t, store, "sess")
			if got := row.State == session.StateExpired; got != tc.wantExpired {
				t.Fatalf("expired = %v (state %q, Expirations %d), want %v", got, row.State, res.Expirations, tc.wantExpired)
			}
			if !tc.wantExpired {
				if len(hook.terminal) != 0 || len(hook.expiries) != 0 {
					t.Fatalf("a surviving row ran terminal side effects: terminal %d, expiries %d", len(hook.terminal), len(hook.expiries))
				}
				return
			}
			if row.FailureReason != string(session.FailureExpiredDeadline) {
				t.Fatalf("FailureReason = %q, want %q", row.FailureReason, session.FailureExpiredDeadline)
			}
			if got := hook.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxSessionAge {
				t.Fatalf("expiry reasons = %v, want exactly [%s]", got, watchdog.ExpiryReasonMaxSessionAge)
			}
		})
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// A listed row that no longer qualifies on the locked row gets no expiry side
// effect: no terminal hook, no expiry notification, and no UpdatedAt advance.
// The cases are a stored row that has since been suspended, one that has
// reached a terminal state, and one whose recomputed active age is at or
// under the cap. The sweep then goes on to the next listed row and expires it.
func TestSweepMaxAgeRechecksLockedRow_spec_6_2(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	staleRunning := func(id string) sessionstore.Session {
		return sessionstore.Session{
			ID: id, TenantID: "acme", State: session.StateRunning,
			CreatedAt: now.Add(-5 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour),
			LastAgentActivityAt: now.Add(-time.Second),
		}
	}
	cases := []struct {
		name   string
		stored func(sessionstore.Session) sessionstore.Session
	}{
		{"locked row suspended", func(r sessionstore.Session) sessionstore.Session {
			r.State = session.StateSuspended
			r.AccumulatedSessionAgeSeconds = (3 * time.Hour).Seconds()
			r.UpdatedAt = now.Add(-time.Minute)
			return r
		}},
		{"locked row terminal", func(r sessionstore.Session) sessionstore.Session {
			r.State = session.StateCompleted
			r.UpdatedAt = now.Add(-time.Minute)
			return r
		}},
		{"locked row under the cap", func(r sessionstore.Session) sessionstore.Session {
			r.UpdatedAt = now.Add(-time.Minute)
			r.AccumulatedSessionAgeSeconds = (10 * time.Minute).Seconds()
			return r
		}},
		{"locked row exactly at the cap", func(r sessionstore.Session) sessionstore.Session {
			r.UpdatedAt = now.Add(-time.Hour)
			r.AccumulatedSessionAgeSeconds = time.Hour.Seconds()
			return r
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := memstore.New()
			stale := staleRunning("sess_stale")
			stored := tc.stored(stale)
			mustCreate(t, mem, stored)
			over := staleRunning("sess_over")
			mustCreate(t, mem, over)
			store := &staleListStore{Store: mem, listed: []sessionstore.Session{stale, over}}
			hook := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), nil).WithTerminalHook(hook)

			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			got := mustGet(t, mem, "sess_stale")
			if got.State != stored.State || !got.UpdatedAt.Equal(stored.UpdatedAt) {
				t.Fatalf("superseded row was written: state %q UpdatedAt %v, want %q %v", got.State, got.UpdatedAt, stored.State, stored.UpdatedAt)
			}
			for _, sess := range hook.terminal {
				if sess.ID == "sess_stale" {
					t.Fatal("the terminal hook ran for the superseded row")
				}
			}
			if res.Expirations != 1 || len(hook.terminal) != 1 || len(hook.expiries) != 1 {
				t.Fatalf("Expirations %d, terminal %d, expiries %d; want only the next row expired", res.Expirations, len(hook.terminal), len(hook.expiries))
			}
			if row := mustGet(t, mem, "sess_over"); row.State != session.StateExpired {
				t.Fatalf("next listed row state = %q, want expired", row.State)
			}
		})
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// An Update error other than the superseded sentinel ends the tick with that
// error rather than being read as a superseded row.
func TestSweepMaxAgeReturnsUpdateError_spec_6_2(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	mem := memstore.New()
	mustCreate(t, mem, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now.Add(-5 * time.Hour), UpdatedAt: now.Add(-3 * time.Hour),
		LastAgentActivityAt: now.Add(-time.Second),
	})
	boom := errors.New("primary unavailable")
	w := watchdog.New(&failingUpdateStore{Store: mem, err: boom}, watchdog.StaticTenants{"acme"}, ageCfg(7200), nil)
	if _, err := w.Tick(context.Background(), now); !errors.Is(err, boom) {
		t.Fatalf("Tick error = %v, want %v", err, boom)
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states), 11.3 (session
// expiry warning)
//
// The expiry warning is measured on active age: a session young by active
// time and old by wall clock is not warned, and a session whose active age is
// inside the warning window is warned with the remaining active time.
func TestExpiryWarningUsesActiveAge_spec_6_2(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	store := memstore.New()
	// Wall-clock age inside the window, active age far from it.
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_young", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now.Add(-(2*time.Hour - 2*time.Minute)), UpdatedAt: now.Add(-10 * time.Minute),
		LastAgentActivityAt: now.Add(-time.Second),
	})
	// Active age two minutes short of the cap.
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_near", TenantID: "acme", State: session.StateRunning,
		CreatedAt: now.Add(-10 * time.Hour), UpdatedAt: now.Add(-time.Hour),
		LastAgentActivityAt:          now.Add(-time.Second),
		AccumulatedSessionAgeSeconds: (58 * time.Minute).Seconds(),
	})
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), nil).WithTerminalHook(hook)
	if _, err := w.Tick(context.Background(), now); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.warnings) != 1 || hook.warnings[0].sessionID != "sess_near" {
		t.Fatalf("warnings = %+v, want only sess_near", hook.warnings)
	}
	if got := hook.warnings[0].remainingSeconds; got != 120 {
		t.Fatalf("remaining = %ds, want 120", got)
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states), 11.3 (session
// expiry warning)
//
// The expiry warning fires only for a row whose age timer is running. A row
// in a state where the timer is paused or not yet started, whose frozen
// active age sits inside the warning band, is not warned: no age cap expires
// a suspended row, and a paused row's remaining time does not count down.
func TestExpiryWarningSkipsPausedAgeTimer_spec_6_2(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	paused := []session.State{
		session.StateSuspended,
		session.StateResumePending,
		session.StateResuming,
		session.StateAwaitingClientAction,
		session.StateCreated,
		session.StateReady,
		session.StateFinalizing,
	}
	for _, st := range paused {
		t.Run(string(st), func(t *testing.T) {
			store := memstore.New()
			// Frozen active age 6900s under a 7200s cap: inside the band.
			mustCreate(t, store, sessionstore.Session{
				ID: "sess_paused", TenantID: "acme", State: st,
				CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: now.Add(-time.Minute),
				LastAgentActivityAt:          now.Add(-time.Minute),
				AccumulatedSessionAgeSeconds: 6900,
			})
			hook := &captureExpiryWarning{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), nil).WithTerminalHook(hook)
			res, err := w.Tick(context.Background(), now)
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if res.ExpiryWarnings != 0 || len(hook.warnings) != 0 {
				t.Fatalf("paused row in %s warned: ExpiryWarnings=%d warnings=%+v", st, res.ExpiryWarnings, hook.warnings)
			}
		})
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states), 11.3 (session
// expiry warning)
//
// A session suspended with its active age inside the warning band is not
// warned while suspended, so the dedup entry is not spent. After it returns
// to running and its active age approaches the cap, the warning fires exactly
// once, ahead of the maxSessionAge expiry.
func TestExpiryWarningFiresAfterResumeNotWhileSuspended_spec_6_2(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	store := memstore.NewWithClock(now)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_s", TenantID: "acme", State: session.StateSuspended,
		CreatedAt: clock.Add(-2 * time.Hour), UpdatedAt: clock,
		LastAgentActivityAt:          clock,
		AccumulatedSessionAgeSeconds: 6900,
	})
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), now).WithTerminalHook(hook)
	tick := func() {
		t.Helper()
		if _, err := w.Tick(ctx, clock); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}

	tick()
	clock = clock.Add(72 * time.Hour)
	tick()
	if len(hook.warnings) != 0 {
		t.Fatalf("suspended session warned: %+v", hook.warnings)
	}

	if _, err := store.Update(ctx, "acme", "sess_s", func(r *sessionstore.Session) error {
		r.State = session.StateRunning
		r.LastAgentActivityAt = clock
		return nil
	}); err != nil {
		t.Fatalf("Update to running: %v", err)
	}
	clock = clock.Add(30 * time.Second)
	tick()
	if len(hook.warnings) != 1 || hook.warnings[0].sessionID != "sess_s" {
		t.Fatalf("after resume: warnings = %+v, want one for sess_s", hook.warnings)
	}
	if got := hook.warnings[0].remainingSeconds; got != 270 {
		t.Fatalf("remaining = %ds, want 270", got)
	}
	clock = clock.Add(60 * time.Second)
	tick()
	if len(hook.warnings) != 1 {
		t.Fatalf("re-warned: warnings = %+v, want exactly one", hook.warnings)
	}
	clock = clock.Add(250 * time.Second)
	tick()
	if row := mustGet(t, store, "sess_s"); row.State != session.StateExpired {
		t.Fatalf("past the cap: state %q, want expired", row.State)
	}
}

// spec: 27.6 (Session lifecycle and cleanup), 6.2 (maxSessionAge timer
// behavior across states)
//
// A playground session's duration cap, carried as the per-session
// maxSessionAge override, counts active time: hours in suspended do not
// expire it, and it expires once its active time passes the cap after it
// returns to running.
func TestPlaygroundCapCountsActiveTime_spec_27_6(t *testing.T) {
	ctx := context.Background()
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	store := memstore.NewWithClock(now)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_pg", TenantID: "acme", State: session.StateRunning,
		CreatedAt: clock, UpdatedAt: clock,
		// The playground create path stamps its duration cap as the
		// per-session maxSessionAge override.
		Timeouts: &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 600},
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, ageCfg(7200), now).
		WithSessionAgeResolver(sessionage.New(nil, nil))
	setState := func(st session.State) {
		t.Helper()
		if _, err := store.Update(ctx, "acme", "sess_pg", func(r *sessionstore.Session) error {
			r.State = st
			r.LastAgentActivityAt = clock
			return nil
		}); err != nil {
			t.Fatalf("Update to %s: %v", st, err)
		}
	}
	tick := func() {
		t.Helper()
		if _, err := w.Tick(ctx, clock); err != nil {
			t.Fatalf("Tick: %v", err)
		}
	}

	clock = clock.Add(400 * time.Second)
	setState(session.StateSuspended)
	clock = clock.Add(5 * time.Hour)
	tick()
	if row := mustGet(t, store, "sess_pg"); row.State != session.StateSuspended {
		t.Fatalf("after 5h suspended: state %q, want suspended", row.State)
	}
	setState(session.StateRunning)
	clock = clock.Add(150 * time.Second)
	tick()
	if row := mustGet(t, store, "sess_pg"); row.State != session.StateRunning {
		t.Fatalf("at 550s active: state %q, want running", row.State)
	}
	clock = clock.Add(100 * time.Second)
	tick()
	if row := mustGet(t, store, "sess_pg"); row.State != session.StateExpired {
		t.Fatalf("at 650s active: state %q, want expired", row.State)
	}
}

// spec: 6.2 (maxSessionAge timer behavior across states)
//
// EffectiveAgeCap is the single min-wins resolver: the platform cap, the
// runtime or pool cap from the resolver, and the per-session retryPolicy
// value, the smallest positive one winning.
func TestEffectiveAgeCapMinWins_spec_6_2(t *testing.T) {
	ctx := context.Background()
	platform := 2 * time.Hour
	cases := []struct {
		name     string
		resolver watchdog.SessionAgeResolver
		policy   *session.RetryPolicy
		want     time.Duration
	}{
		{"platform only", nil, nil, platform},
		{"resolver tighter", fixedAgeResolver(1800), nil, 30 * time.Minute},
		{"resolver looser is ignored", fixedAgeResolver(86400), nil, platform},
		{"policy tighter than resolver", fixedAgeResolver(1800), &session.RetryPolicy{MaxSessionAgeSeconds: 600}, 10 * time.Minute},
		{"policy looser than resolver", fixedAgeResolver(1800), &session.RetryPolicy{MaxSessionAgeSeconds: 3600}, 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			row := sessionstore.Session{RetryPolicy: tc.policy}
			if got := watchdog.EffectiveAgeCap(ctx, tc.resolver, platform, row); got != tc.want {
				t.Fatalf("EffectiveAgeCap = %v, want %v", got, tc.want)
			}
		})
	}
}

type fixedAgeResolver int

func (f fixedAgeResolver) EffectiveMaxSessionAgeSeconds(context.Context, sessionstore.Session) int {
	return int(f)
}
