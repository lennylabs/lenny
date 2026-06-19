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

// spec: §16.1 (lenny_session_expiry_total{reason}); §6.2 (maxClientIdleSeconds
// clock); §11.3 line 199 (max client idle row). F-11.3.7.
//
// The watchdog must fire the SessionExpiryNotifier on every platform
// expiry-clock transition it drives, with the §16.1.1 reason it resolved:
//   - max_idle_time on the §6.2 maxClientIdleSeconds idle-clock expiry,
//   - max_session_age on the §11.3 maxSessionAge age-cap expiry,
//   - max_session_age on the §7.3 awaiting_client_action wall-clock deadline.

// expiryCapture records the SessionExpiryNotifier calls (and the terminal
// calls, so a test can confirm the expiry notification rides the same
// transition as the terminal pipeline without double-firing it).
type expiryCapture struct {
	expiries []expiryCall
	terminal []sessionstore.Session
}

type expiryCall struct {
	pool   string
	reason string
}

func (c *expiryCapture) OnSessionTerminal(_ context.Context, _ session.State, sess sessionstore.Session) {
	c.terminal = append(c.terminal, sess)
}

func (c *expiryCapture) OnSessionExpired(_ context.Context, sess sessionstore.Session, reason string) {
	c.expiries = append(c.expiries, expiryCall{pool: sess.PoolRef, reason: reason})
}

// expiryReasonsFor returns the reason values captured for the given pool.
func (c *expiryCapture) reasons() []string {
	out := make([]string, 0, len(c.expiries))
	for _, e := range c.expiries {
		out = append(out, e.reason)
	}
	return out
}

// spec: §16.1.1 (reason vocabulary) / §6.2 (maxClientIdleSeconds clock) — the
// idle-clock expiry fires the notifier with reason max_idle_time and the
// session's pool. F-11.3.7.
//
// diagnosis: a failure means the maxClientIdleSeconds idle reclamation no
// longer emits lenny_session_expiry_total{reason=max_idle_time}, so an operator
// cannot see client-inactivity terminations on the dashboards.
func TestExpiryCounterIdleReasonMaxIdleTime_spec_16_1_1(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		PoolRef: "pool-a", CreatedAt: born, UpdatedAt: born,
	})
	hook := &expiryCapture{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(10*time.Minute+time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.expiries) != 1 {
		t.Fatalf("expiry notifications: got %d, want 1", len(hook.expiries))
	}
	if got := hook.expiries[0]; got.reason != watchdog.ExpiryReasonMaxIdleTime || got.pool != "pool-a" {
		t.Errorf("idle expiry: got pool=%q reason=%q, want pool=pool-a reason=%q",
			got.pool, got.reason, watchdog.ExpiryReasonMaxIdleTime)
	}
	// The expiry notification rides the same transition as the terminal
	// pipeline; both fire exactly once.
	if len(hook.terminal) != 1 {
		t.Errorf("terminal calls: got %d, want 1", len(hook.terminal))
	}
}

// spec: §16.1.1 (reason vocabulary) / §11.3 line 198 (maxSessionAge cap) — the
// age-cap expiry fires the notifier with reason max_session_age. F-11.3.7.
//
// diagnosis: a failure means the maxSessionAge expiry no longer emits
// lenny_session_expiry_total{reason=max_session_age}, so age-cap terminations
// are invisible to the burn-rate alerts.
func TestExpiryCounterMaxAgeReason_spec_16_1_1(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		PoolRef: "pool-b", CreatedAt: born, UpdatedAt: born,
	})
	hook := &expiryCapture{}
	// A 600s age cap; the idle sweep is disabled so only the age-cap edge fires.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxSessionAgeSeconds:           600,
		MaxIdleSeconds:                 idleCapDisabled,
		MaxAwaitingClientActionSeconds: idleCapDisabled,
		MaxSuspendedPodHoldSeconds:     idleCapDisabled,
		MaxResumePendingSeconds:        idleCapDisabled,
	}, nil).WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(601*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := hook.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxSessionAge {
		t.Errorf("age-cap expiry reasons: got %v, want [%s]", got, watchdog.ExpiryReasonMaxSessionAge)
	}
	if hook.expiries[0].pool != "pool-b" {
		t.Errorf("age-cap expiry pool: got %q, want pool-b", hook.expiries[0].pool)
	}
}

// spec: §16.1.1 (reason vocabulary) / §7.3 line 423 (awaiting_client_action
// wall-clock deadline) — the awaiting-action deadline shares the age-cap series,
// so the notifier fires with reason max_session_age. F-7.3.25 / F-11.3.7.
//
// diagnosis: a failure means an abandoned awaiting_client_action session
// reclaimed at its maxResumeWindowSeconds deadline is not counted, so the
// expiry counter undercounts platform-clock terminations.
func TestExpiryCounterAwaitingActionReasonMaxAge_spec_16_1_1(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateAwaitingClientAction,
		PoolRef: "pool-c", CreatedAt: born, UpdatedAt: born,
	})
	hook := &expiryCapture{}
	// Disable the idle clock (which also runs in awaiting_client_action) and
	// the age cap so the §7.3 awaiting-action deadline is the only edge that
	// fires. The 900s default awaiting-action deadline applies.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		MaxIdleSeconds:       idleCapDisabled,
		MaxSessionAgeSeconds: idleCapDisabled,
	}, nil).WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(901*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if got := hook.reasons(); len(got) != 1 || got[0] != watchdog.ExpiryReasonMaxSessionAge {
		t.Errorf("awaiting-action expiry reasons: got %v, want [%s]", got, watchdog.ExpiryReasonMaxSessionAge)
	}
	row, _ := store.Get(context.Background(), "acme", "sess")
	if row.State != session.StateExpired {
		t.Errorf("state: got %q, want expired", row.State)
	}
}

// spec: §16.1 — a hook that does not implement SessionExpiryNotifier (and a
// session that is expired with no terminal hook at all) never panics; the
// expiry counter emission is best-effort. F-11.3.7.
//
// diagnosis: a failure means wiring a terminal hook without the expiry notifier
// (or running with no hook) aborts the sweep, blocking every other tenant's
// expiry reclamation.
func TestExpiryCounterNoNotifierIsSafe_spec_16_1(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	// No terminal hook wired at all: the expiry transition still happens and
	// the sweep does not panic on the nil-hook notify path.
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, idleCfg(600), nil)

	res, err := w.Tick(context.Background(), born.Add(10*time.Minute+time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.IdleExpirations != 1 {
		t.Errorf("IdleExpirations: got %d, want 1", res.IdleExpirations)
	}
}
