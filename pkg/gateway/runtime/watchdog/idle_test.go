// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
)

// fakeIdleResolver maps a runtime ref to its effective maxClientIdleSeconds,
// mirroring sessionidle.Resolver for the watchdog sweep tests.
type fakeIdleResolver map[string]int

func (f fakeIdleResolver) EffectiveMaxIdleSeconds(_ context.Context, sess sessionstore.Session) int {
	return f[sess.RuntimeRef]
}

// idleCfg returns a Config whose only meaningful idle cap is the platform
// idle default, set to capSeconds, with every other expiry sweep disabled so
// a test isolates the idle path. The maxSessionAge and pre-running caps are
// set far larger than any age these tests exercise.
func idleCfg(capSeconds int) watchdog.Config {
	return watchdog.Config{
		MaxIdleSeconds:                 capSeconds,
		MaxSessionAgeSeconds:           idleCapDisabled,
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     idleCapDisabled,
		MaxResumePendingSeconds:        idleCapDisabled,
	}
}

// spec: 11.3 (Timeouts and Cancellation: "Max client idle time" row), 6.2 (maxClientIdleSeconds
// clock) — a running session with no qualifying activity for longer than the
// platform default maxClientIdleSeconds is expired with reason
// expired:idle. F-11.3.7.
//
// diagnosis: a failure means the idle sweep no longer reclaims an abandoned
// running session at the platform idle bound, so a client that walks away
// would hold a pod and credential lease until the maxSessionAge backstop.
func TestIdleSweepExpiresIdleRunningSession_spec_11_3_199(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	// A 600s platform idle cap (the spec default is 900s; this
	// test pins the sweep behaviour against an explicit cap).
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil)

	// 10 minutes after birth with no activity: past the 600s idle cap (the
	// anchor falls back to StateEnteredAt, which Create stamps as CreatedAt =
	// born). maxSessionAge is disabled here,
	// so this is purely the idle path.
	res, err := w.Tick(context.Background(), born.Add(10*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations: got %d, want 1", res.IdleExpirations)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations should include the idle expiry: got %d, want 1", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess")
	if row.State != session.StateExpired {
		t.Errorf("state: got %q, want expired", row.State)
	}
	if row.FailureReason != string(session.FailureExpiredIdle) {
		t.Errorf("FailureReason: got %q, want %q", row.FailureReason, session.FailureExpiredIdle)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock behavior across states) — the
// platform default idle cap is 900s. A running session idle for 900s with no
// resolver survives, and one idle one second longer is reaped.
//
// diagnosis: a failure means the platform idle default is not the 900s the
// spec fixes, so an idle session holds its pod longer or shorter than the
// spec states.
func TestIdleSweepDefaultIs900Seconds_spec_6_2(t *testing.T) {
	if got := watchdog.DefaultMaxIdleSeconds; got != 900 {
		t.Fatalf("DefaultMaxIdleSeconds = %d, want 900", got)
	}
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	cfg := idleCfg(0)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, cfg, nil)
	res, err := w.Tick(context.Background(), born.Add(900*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Fatalf("at 900s the session must survive: IdleExpirations=%d", res.IdleExpirations)
	}
	res, err = w.Tick(context.Background(), born.Add(901*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("past 900s the session must be reaped: IdleExpirations=%d", res.IdleExpirations)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock) — recent qualifying activity
// (LastAgentActivityAt) anchors the idle clock, so a session whose agent
// acted recently is not reaped even when it entered `running` long ago.
// F-11.3.7.
//
// diagnosis: a failure means the idle anchor ignores agent activity, so an
// autonomously working session would be falsely idle-terminated.
func TestIdleSweepRespectsActivityAnchor_spec_6_2(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(time.Hour)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		CreatedAt: born, UpdatedAt: born,
		// Stamped 30s before the tick: well within the 600s idle window.
		LastAgentActivityAt: now.Add(-30 * time.Second),
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Errorf("a session active 30s ago must not be idle-expired: IdleExpirations=%d", res.IdleExpirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess")
	if row.State != session.StateRunning {
		t.Errorf("state: got %q, want running", row.State)
	}
}

// spec: 11.3 (Timeouts and Cancellation: "Max client idle time" row) — the resolver's
// per-runtime / per-pool maxClientIdleSeconds tightens the platform default;
// a tighter cap expires earlier. F-11.3.7.
//
// diagnosis: a failure means the watchdog ignores the resolver, so a deployer
// who tuned a tighter idle bound per pool would not see it enforced.
func TestIdleSweepHonorsResolverCap_spec_11_3_199(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_short", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "short", CreatedAt: born, UpdatedAt: born,
	})
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_default", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "default", CreatedAt: born, UpdatedAt: born,
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil).
		WithIdleResolver(fakeIdleResolver{"short": 60}) // 60s idle for "short"

	// 2 minutes in: past the 60s short cap, well under the 600s default the
	// uncapped runtime inherits.
	res, err := w.Tick(context.Background(), born.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations: got %d, want 1 (only the short-cap runtime)", res.IdleExpirations)
	}
	short, _ := store.Get(context.Background(), "acme", "sess_short")
	if short.State != session.StateExpired {
		t.Errorf("short-cap session: got %q, want expired", short.State)
	}
	def, _ := store.Get(context.Background(), "acme", "sess_default")
	if def.State != session.StateRunning {
		t.Errorf("default-cap session: got %q, want running (600s not reached)", def.State)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 9.2
// (elicitation-wait idle clock) — the clock is evaluated in `running` alone:
// it is not evaluated in `input_required`, where the runtime waits on the
// client, and is paused in `awaiting_client_action`. An elicitation wait
// keeps the session in `running`, so it is covered by the `running` entry.
//
// diagnosis: a failure means the idle sweep reclaimed a session waiting on
// its client in input_required or awaiting_client_action, which the spec
// leaves to their own deadlines, or stopped reclaiming an idle running
// session.
func TestIdleSweepRunsInRunningOnly_spec_6_2(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{
		session.StateRunning,
		session.StateInputRequired,
		session.StateAwaitingClientAction,
	} {
		seedRow(t, store, "sess_"+string(st), "acme", st, born)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil)

	res, err := w.Tick(context.Background(), born.Add(10*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations: got %d, want 1 (running only)", res.IdleExpirations)
	}
	want := map[session.State]session.State{
		session.StateRunning:              session.StateExpired,
		session.StateInputRequired:        session.StateInputRequired,
		session.StateAwaitingClientAction: session.StateAwaitingClientAction,
	}
	for st, wantState := range want {
		row, _ := store.Get(context.Background(), "acme", "sess_"+string(st))
		if row.State != wantState {
			t.Errorf("%s session: got %q, want %q", st, row.State, wantState)
		}
	}
}

// spec: 6.2 (maxClientIdleSeconds clock pause table) — the clock is paused in
// `suspended`, `resume_pending`, `resuming`, and `finalizing`, so the idle
// sweep never expires them for idleness. F-11.3.7.
//
// diagnosis: a failure means the idle clock ran in a paused state, so a
// deliberately suspended session or a session mid-recovery would be falsely
// idle-terminated before its dedicated wall-clock timer.
func TestIdleSweepPausedInPausedStates_spec_6_2(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{
		session.StateSuspended,
		session.StateResumePending,
		session.StateResuming,
		session.StateFinalizing,
	} {
		seedRow(t, store, "sess_"+string(st), "acme", st, born)
	}
	// Disable the dedicated pre-running and recovery sweeps so only the idle
	// sweep is under test; a short 600s idle cap that any paused row exceeds.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxIdleSeconds:                 600,
		MaxSessionAgeSeconds:           idleCapDisabled,
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     idleCapDisabled,
		MaxResumePendingSeconds:        idleCapDisabled,
		MaxResumingSeconds:             idleCapDisabled,
		MaxFinalizingSeconds:           idleCapDisabled,
	}, nil)

	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Errorf("the idle clock must be paused in suspended/resume_pending/resuming/finalizing: IdleExpirations=%d", res.IdleExpirations)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock default) — a zero resolver result
// falls through to the platform default; a session under that default is not
// idle-expired early. F-11.3.7.
//
// diagnosis: a failure means a zero resolver result is treated as a zero cap
// (expire immediately) rather than as "use the platform default".
func TestIdleSweepZeroResolverFallsThroughToDefault_spec_11_3_199(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil).
		WithIdleResolver(fakeIdleResolver{}) // no entry → 0 → platform default

	// 5 minutes in: under the 600s platform default. No idle expiry.
	res, err := w.Tick(context.Background(), born.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Errorf("under the 600s default the session must survive: IdleExpirations=%d", res.IdleExpirations)
	}
	// 11 minutes in: past the default. Now it expires.
	res, err = w.Tick(context.Background(), born.Add(11*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Errorf("past the 600s default the session must expire: IdleExpirations=%d", res.IdleExpirations)
	}
}
