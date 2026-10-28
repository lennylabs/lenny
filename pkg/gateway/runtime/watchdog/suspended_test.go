// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// suspendedHooks records the suspended sweep's hook calls.
type suspendedHooks struct {
	released  []string
	resumed   []string
	suspended map[string]int
	// holdsPod answers HoldsPod; nil answers whether the row names a pod.
	holdsPod func(sessionstore.Session) bool
	// backlog answers ResumeBacklog.
	backlog bool
}

func (h *suspendedHooks) ReleaseSuspendedPod(_ context.Context, row sessionstore.Session) {
	h.released = append(h.released, row.ID)
}

func (h *suspendedHooks) HoldsPod(row sessionstore.Session) bool {
	if h.holdsPod != nil {
		return h.holdsPod(row)
	}
	return row.PodAssignment != ""
}

func (h *suspendedHooks) ResumeBacklog(_ context.Context, row sessionstore.Session) bool {
	h.resumed = append(h.resumed, row.ID)
	return h.backlog
}

func (h *suspendedHooks) SuspendIdle(_ context.Context, row sessionstore.Session, idleSeconds int) {
	if h.suspended == nil {
		h.suspended = map[string]int{}
	}
	h.suspended[row.ID] = idleSeconds
}

const (
	testHold     = 900
	testLifetime = 604800
)

// suspendedCfg isolates the suspended sweep: every other expiry is disabled.
func suspendedCfg() watchdog.Config {
	return watchdog.Config{
		MaxIdleSeconds:                 idleCapDisabled,
		MaxSessionAgeSeconds:           idleCapDisabled,
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     testHold,
		MaxSuspendedSessionSeconds:     testLifetime,
		MaxResumePendingSeconds:        idleCapDisabled,
	}
}

// seedSuspended seeds a suspended row entered at since, naming pod.
func seedSuspended(t *testing.T, store sessionstore.Store, id, pod string, since time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: session.StateSuspended, PodAssignment: pod,
		PoolRef: "pool-a", CreatedAt: since, UpdatedAt: since, SuspendedAt: since,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// TestSuspendedLifetimeExpiresAtTheBound_spec_6_2 pins the suspended-session
// lifetime: a row expires at MaxSuspendedSessionSeconds and not one second
// before, with expired:idle and the max_idle_time reason, and the terminal and
// expiry hooks run once each; a podless row is never released.
//
// spec: 6.2 (Pod State Machine), 16.1.1 (Attribute Naming)
//
// diagnosis: a failure means a suspended session outlives or undercuts its
// lifetime, or its expiry is counted under the wrong reason.
func TestSuspendedLifetimeExpiresAtTheBound_spec_6_2(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		offset  time.Duration
		expired bool
	}{
		{"one_second_before", testLifetime*time.Second - time.Second, false},
		{"at_the_bound", testLifetime * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seedSuspended(t, store, "s1", "", since)
			hooks := &suspendedHooks{}
			capture := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, suspendedCfg(), nil).
				WithTerminalHook(capture).
				WithSuspendedPodReleaser(hooks).
				WithBacklogResumer(hooks)

			res, err := w.Tick(context.Background(), since.Add(tc.offset))
			if err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			row, _ := store.Get(context.Background(), "acme", "s1")
			if !tc.expired {
				if row.State != session.StateSuspended || res.SuspendedExpirations != 0 || len(capture.terminal) != 0 {
					t.Fatalf("state %q expirations %d terminal %d, want suspended and nothing run", row.State, res.SuspendedExpirations, len(capture.terminal))
				}
				if len(hooks.released) != 0 {
					t.Errorf("released %v, want no release of a podless row", hooks.released)
				}
				return
			}
			if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredIdle) {
				t.Fatalf("row = %q/%q, want expired/expired:idle", row.State, row.FailureReason)
			}
			if res.SuspendedExpirations != 1 || len(capture.terminal) != 1 {
				t.Errorf("expirations %d terminal %d, want 1 each", res.SuspendedExpirations, len(capture.terminal))
			}
			if got := capture.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxIdleTime {
				t.Errorf("expiry reasons = %v, want [max_idle_time]", got)
			}
			if len(hooks.released) != 0 || len(hooks.resumed) != 0 {
				t.Errorf("released %v resumed %v, want nothing after the expiry", hooks.released, hooks.resumed)
			}
		})
	}
}

// supersedingStore moves the listed row before the lifetime write, so the
// write's guard reads a row that is no longer the listed suspended row.
type supersedingStore struct {
	sessionstore.Store
	mutate func(*sessionstore.Session)
	err    error
}

func (s *supersedingStore) Update(ctx context.Context, tenant, id string, fn func(*sessionstore.Session) error) (sessionstore.Session, error) {
	if s.err != nil {
		return sessionstore.Session{}, s.err
	}
	if s.mutate != nil {
		mutate := s.mutate
		s.mutate = nil
		if _, err := s.Store.Update(ctx, tenant, id, func(r *sessionstore.Session) error {
			mutate(r)
			return nil
		}); err != nil {
			return sessionstore.Session{}, err
		}
	}
	return s.Store.Update(ctx, tenant, id, fn)
}

// TestSuspendedLifetimeGuardAndStoreError_spec_6_2 pins the guarded write: a
// row that left suspended, or re-entered it with a new SuspendedAt, is not
// written and runs no hook; a store error runs no hook, no backlog resume,
// and no release, returns no Tick error, and a later tick expires the row.
//
// spec: 6.2 (Pod State Machine)
//
// diagnosis: a failure means the lifetime expiry overwrites a session that
// resumed after the listing, or a store error leaks a hook or a release.
func TestSuspendedLifetimeGuardAndStoreError_spec_6_2(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	due := since.Add(testLifetime * time.Second)
	for _, tc := range []struct {
		name      string
		mutate    func(*sessionstore.Session)
		wantState session.State
	}{
		{"resumed_after_listing", func(r *sessionstore.Session) { r.State = session.StateRunning }, session.StateRunning},
		{"resuspended_after_listing", func(r *sessionstore.Session) { r.SuspendedAt = due }, session.StateSuspended},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := memstore.New()
			seedSuspended(t, base, "s1", "pod-1", since)
			store := &supersedingStore{Store: base, mutate: tc.mutate}
			hooks := &suspendedHooks{}
			capture := &expiryCapture{}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, suspendedCfg(), nil).
				WithTerminalHook(capture).WithSuspendedPodReleaser(hooks).WithBacklogResumer(hooks)
			if _, err := w.Tick(context.Background(), due); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			w.WaitDispatched()
			row, _ := base.Get(context.Background(), "acme", "s1")
			if row.State != tc.wantState || len(capture.terminal) != 0 || len(capture.expiries) != 0 {
				t.Fatalf("state %q terminal %d expiries %d, want %q and no hook", row.State, len(capture.terminal), len(capture.expiries), tc.wantState)
			}
			if len(hooks.released) != 0 || len(hooks.resumed) != 0 {
				t.Errorf("released %v resumed %v, want nothing", hooks.released, hooks.resumed)
			}
		})
	}

	t.Run("store_error_then_retry", func(t *testing.T) {
		base := memstore.New()
		seedSuspended(t, base, "s1", "pod-1", since)
		store := &supersedingStore{Store: base, err: errors.New("postgres down")}
		hooks := &suspendedHooks{}
		capture := &expiryCapture{}
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, suspendedCfg(), nil).
			WithTerminalHook(capture).WithSuspendedPodReleaser(hooks).WithBacklogResumer(hooks)
		// spec: §10.1.1 (Primary:) — the failed write is collected and
		// returned in the pass's joined error rather than swallowed.
		if _, err := w.Tick(context.Background(), due); err == nil || !strings.Contains(err.Error(), "postgres down") {
			t.Fatalf("Tick error = %v, want the collected store error", err)
		}
		w.WaitDispatched()
		if len(capture.terminal) != 0 || len(hooks.released) != 0 || len(hooks.resumed) != 0 {
			t.Fatalf("terminal %d released %v resumed %v after a store error, want none", len(capture.terminal), hooks.released, hooks.resumed)
		}
		store.err = nil
		if _, err := w.Tick(context.Background(), due.Add(time.Second)); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		w.WaitDispatched()
		if row, _ := base.Get(context.Background(), "acme", "s1"); row.State != session.StateExpired {
			t.Fatalf("state after the retry = %q, want expired", row.State)
		}
	})
}

// TestSuspendedPodHoldReleaseAndBacklog_spec_6_2 pins the held-pod path: an
// interrupt row's pod is released only once the hold ended, a backlog resume
// suppresses the release, a podless row gets only the lifetime check, and a
// row whose pod check answers true with no pod (a gateway with no pod binder)
// reaches the backlog check.
//
// spec: 6.2 (Pod State Machine), 7.2 (Message delivery routing — seven paths,
// path 6)
//
// diagnosis: a failure means the watchdog releases a pod inside its hold,
// releases a pod a backlog resume is using, or takes a podless row away from
// the leader's re-check.
func TestSuspendedPodHoldReleaseAndBacklog_spec_6_2(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name         string
		pod          string
		offset       time.Duration
		backlog      bool
		holdsPod     func(sessionstore.Session) bool
		wantReleased bool
		wantResumed  bool
	}{
		{name: "inside_hold", pod: "pod-1", offset: testHold*time.Second - time.Second, wantResumed: true},
		{name: "hold_ended", pod: "pod-1", offset: testHold * time.Second, wantReleased: true, wantResumed: true},
		{name: "backlog_resume_suppresses_release", pod: "pod-1", offset: testHold * time.Second, backlog: true, wantResumed: true},
		{name: "podless_row", pod: "", offset: testHold * time.Second},
		{name: "binderless_gateway", pod: "", offset: testHold * time.Second, holdsPod: func(sessionstore.Session) bool { return true }, wantResumed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			seedSuspended(t, store, "s1", tc.pod, since)
			hooks := &suspendedHooks{backlog: tc.backlog, holdsPod: tc.holdsPod}
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, suspendedCfg(), nil).
				WithSuspendedPodReleaser(hooks).WithBacklogResumer(hooks)
			if _, err := w.Tick(context.Background(), since.Add(tc.offset)); err != nil {
				t.Fatalf("Tick: %v", err)
			}
			if got := len(hooks.released) == 1; got != tc.wantReleased {
				t.Errorf("released %v, want release %v", hooks.released, tc.wantReleased)
			}
			if got := len(hooks.resumed) == 1; got != tc.wantResumed {
				t.Errorf("backlog checks %v, want check %v", hooks.resumed, tc.wantResumed)
			}
		})
	}
}

// idleSuspendResolver answers the idle cap and whether a row can be
// idle-suspended.
type idleSuspendResolver struct {
	suspendable map[string]bool
	err         error
}

func (idleSuspendResolver) EffectiveMaxIdleSeconds(context.Context, sessionstore.Session) int {
	return 0
}

func (r idleSuspendResolver) IdleSuspendable(_ context.Context, sess sessionstore.Session) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	return r.suspendable[sess.ID], nil
}

// TestIdleSweepSuspendsOrExpires_spec_6_2 pins the idle firing: a suspendable
// over-idle running row reaches the suspender with the effective cap and is
// not written; a row that cannot be idle-suspended is expired with
// expired:idle and counted max_idle_time; a resolver error skips the row; and
// with no suspender wired every idle row expires.
//
// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 11.3
// (Timeouts and Cancellation)
//
// diagnosis: a failure means an idle session that should be suspended is
// destroyed, or a service-mode or MCP session is sealed.
func TestIdleSweepSuspendsOrExpires_spec_6_2(t *testing.T) {
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(watchdog.DefaultMaxIdleSeconds*time.Second + time.Second)
	newStore := func(t *testing.T) sessionstore.Store {
		store := memstore.New()
		seedRow(t, store, "agent", "acme", session.StateRunning, born)
		seedRow(t, store, "service", "acme", session.StateRunning, born)
		return store
	}
	cfg := suspendedCfg()
	cfg.MaxIdleSeconds = watchdog.DefaultMaxIdleSeconds

	t.Run("suspendable_and_not", func(t *testing.T) {
		store := newStore(t)
		hooks := &suspendedHooks{}
		capture := &expiryCapture{}
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, cfg, nil).
			WithTerminalHook(capture).
			WithIdleResolver(idleSuspendResolver{suspendable: map[string]bool{"agent": true}}).
			WithIdleSuspender(hooks)
		res, err := w.Tick(context.Background(), now)
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if hooks.suspended["agent"] != watchdog.DefaultMaxIdleSeconds || len(hooks.suspended) != 1 {
			t.Errorf("suspended = %v, want only agent with %d", hooks.suspended, watchdog.DefaultMaxIdleSeconds)
		}
		if row, _ := store.Get(context.Background(), "acme", "agent"); row.State != session.StateRunning {
			t.Errorf("agent state = %q, want running (the suspender writes)", row.State)
		}
		row, _ := store.Get(context.Background(), "acme", "service")
		if row.State != session.StateExpired || row.FailureReason != string(session.FailureExpiredIdle) {
			t.Errorf("service row = %q/%q, want expired/expired:idle", row.State, row.FailureReason)
		}
		if res.IdleSuspensions != 1 || res.IdleExpirations != 1 {
			t.Errorf("suspensions %d expirations %d, want 1 each", res.IdleSuspensions, res.IdleExpirations)
		}
		if got := capture.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxIdleTime {
			t.Errorf("reasons = %v, want [max_idle_time]", got)
		}
	})

	t.Run("resolver_error_skips", func(t *testing.T) {
		store := newStore(t)
		hooks := &suspendedHooks{}
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, cfg, nil).
			WithIdleResolver(idleSuspendResolver{err: errors.New("runtime store down")}).
			WithIdleSuspender(hooks)
		if _, err := w.Tick(context.Background(), now); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		for _, id := range []string{"agent", "service"} {
			if row, _ := store.Get(context.Background(), "acme", id); row.State != session.StateRunning {
				t.Errorf("%s state = %q, want running", id, row.State)
			}
		}
		if len(hooks.suspended) != 0 {
			t.Errorf("suspended %v, want none", hooks.suspended)
		}
	})

	t.Run("no_suspender_expires", func(t *testing.T) {
		store := newStore(t)
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, cfg, nil).
			WithIdleResolver(idleSuspendResolver{suspendable: map[string]bool{"agent": true}})
		if _, err := w.Tick(context.Background(), now); err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if row, _ := store.Get(context.Background(), "acme", "agent"); row.State != session.StateExpired {
			t.Errorf("agent state = %q, want expired with no suspender", row.State)
		}
	})
}

// TestEffectiveMaxSuspendedSessionSeconds_spec_6_2 pins the resolved
// suspended lifetime: a non-positive configured value resolves to the 7-day
// default, and a positive value is kept. Startup validation of the delegation
// budget key TTL compares against this resolved value.
//
// spec: 6.2 (Pod State Machine), 8.3 (Delegation Policy and Lease)
func TestEffectiveMaxSuspendedSessionSeconds_spec_6_2(t *testing.T) {
	for _, tc := range []struct{ configured, want int }{
		{0, watchdog.DefaultMaxSuspendedSessionSeconds},
		{-5, watchdog.DefaultMaxSuspendedSessionSeconds},
		{1, 1},
		{3600, 3600},
	} {
		if got := watchdog.EffectiveMaxSuspendedSessionSeconds(tc.configured); got != tc.want {
			t.Errorf("EffectiveMaxSuspendedSessionSeconds(%d) = %d, want %d", tc.configured, got, tc.want)
		}
	}
}
