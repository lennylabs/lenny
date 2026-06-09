// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
)

// fakeIdleResolver maps a runtime ref to its effective maxIdleTimeSeconds,
// mirroring sessionidle.Resolver for the watchdog sweep tests.
type fakeIdleResolver map[string]int

func (f fakeIdleResolver) EffectiveMaxIdleSeconds(_ context.Context, sess sessionstore.Session) int {
	return f[sess.RuntimeRef]
}

// fakeIdlePause reports a fixed paused-set of session ids, modelling a
// session blocked on a pending elicitation or request_input.
type fakeIdlePause map[string]bool

func (f fakeIdlePause) IdlePaused(_ context.Context, sess sessionstore.Session) bool {
	return f[sess.ID]
}

// spec: §11.3 line 199 / §6.2 lines 273-300 — a `running` session with no
// qualifying activity for longer than the platform default maxIdleTime is
// expired with reason expired:idle. F-11.3.7.
func TestIdleSweepExpiresIdleRunningSession_spec_11_3_199(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// 10 minutes after birth with no activity: past the 600s default idle
	// cap (the anchor falls back to UpdatedAt = born). maxSessionAge (7200s)
	// has not fired, so this is purely the idle path.
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

// spec: §6.2 lines 273-278 — recent qualifying activity (LastAgentActivityAt)
// anchors the idle clock, so a session whose agent acted recently is not
// reaped even when it entered `running` long ago. F-11.3.7.
func TestIdleSweepRespectsActivityAnchor_spec_6_2_273(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(time.Hour)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		CreatedAt: born, UpdatedAt: born,
		// Stamped 30s before the tick: well within the 600s idle window.
		LastAgentActivityAt: now.Add(-30 * time.Second),
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

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

// spec: §11.3 line 199 — the per-runtime limits.maxIdleTimeSeconds tightens
// the platform default; a tighter runtime cap expires earlier. F-11.3.7.
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
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
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

// spec: §9.2 line 102 — a session blocked waiting for an elicitation
// response is "waiting_for_human, not idle"; the idle timer is paused so it
// is not reaped. F-9.2.15.
func TestIdleSweepPausesForPendingInteraction_spec_9_2_102(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_waiting", "acme", session.StateRunning, born)
	seedRow(t, store, "sess_idle", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithIdlePauseChecker(fakeIdlePause{"sess_waiting": true})

	// Both rows are 10 minutes idle. The paused one (a pending elicitation)
	// must survive; the other is reaped.
	res, err := w.Tick(context.Background(), born.Add(10*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Fatalf("IdleExpirations: got %d, want 1 (only the non-paused session)", res.IdleExpirations)
	}
	waiting, _ := store.Get(context.Background(), "acme", "sess_waiting")
	if waiting.State != session.StateRunning {
		t.Errorf("waiting_for_human session: got %q, want running (idle timer paused)", waiting.State)
	}
	idle, _ := store.Get(context.Background(), "acme", "sess_idle")
	if idle.State != session.StateExpired {
		t.Errorf("idle session: got %q, want expired", idle.State)
	}
}

// spec: §6.2 timer-behavior table — the idle timer is Active only in
// `running`; `input_required`, `suspended`, and the recovery states pause
// it, so the sweep never expires them for idleness. F-11.3.7 / F-9.2.15.
func TestIdleSweepIgnoresNonRunningStates_spec_6_2_273(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{
		session.StateInputRequired,
		session.StateSuspended,
		session.StateResumePending,
		session.StateAwaitingClientAction,
	} {
		seedRow(t, store, "sess_"+string(st), "acme", st, born)
	}
	// A large maxSuspendedPodHold / awaiting / resume cap so only the idle
	// sweep is under test here.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     idleCapDisabled,
		MaxResumePendingSeconds:        idleCapDisabled,
		MaxSessionAgeSeconds:           idleCapDisabled,
	}, nil)

	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 0 {
		t.Errorf("the idle sweep must only touch `running`: IdleExpirations=%d", res.IdleExpirations)
	}
}

// spec: §6.2 line 296 — a zero resolver result falls through to the
// platform default; a session under that default is not idle-expired
// early. F-11.3.7.
func TestIdleSweepZeroResolverFallsThroughToDefault_spec_11_3_199(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
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
