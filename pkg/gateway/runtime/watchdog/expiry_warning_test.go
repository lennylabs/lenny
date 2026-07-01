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

// captureExpiryWarning is a TerminalHook that also implements
// watchdog.ExpiryWarningNotifier so the sweep's §11.3 line 240 warning is
// observable in tests.
type captureExpiryWarning struct {
	warnings []expiryWarningCall
}

type expiryWarningCall struct {
	sessionID        string
	maxSessionAge    int
	remainingSeconds int
}

func (c *captureExpiryWarning) OnSessionTerminal(_ context.Context, _ session.State, _ sessionstore.Session) {
}

func (c *captureExpiryWarning) OnSessionExpiringSoon(_ context.Context, sess sessionstore.Session, maxAge, remaining int) {
	c.warnings = append(c.warnings, expiryWarningCall{sess.ID, maxAge, remaining})
}

// activeRunning seeds a `running` session with a recent activity stamp so the
// idle sweep does not reap it, isolating the maxSessionAge expiry-warning path.
func activeRunning(t *testing.T, store sessionstore.Store, id, tenant string, born, now time.Time) {
	t.Helper()
	mustCreate(t, store, sessionstore.Session{
		ID: id, TenantID: tenant, State: session.StateRunning,
		CreatedAt: born, UpdatedAt: born,
		LastAgentActivityAt: now.Add(-30 * time.Second),
	})
}

// spec: §11.3 line 240 — the gateway warns 5 minutes before maxSessionAge. The
// session stays non-terminal at warning time (sweepMaxAge expires it later).
// F-11.3.5.
func TestExpiryWarningFiresWithinWindow_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(6901 * time.Second) // 299s before the 7200s platform cap.
	activeRunning(t, store, "sess", "acme", born, now)
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ExpiryWarnings != 1 {
		t.Fatalf("ExpiryWarnings: got %d, want 1", res.ExpiryWarnings)
	}
	if len(hook.warnings) != 1 {
		t.Fatalf("warnings: got %d, want 1", len(hook.warnings))
	}
	wn := hook.warnings[0]
	if wn.sessionID != "sess" || wn.maxSessionAge != 7200 || wn.remainingSeconds != 299 {
		t.Errorf("warning = %+v, want {sess 7200 299}", wn)
	}
	row, _ := store.Get(context.Background(), "acme", "sess")
	if row.State != session.StateRunning {
		t.Errorf("state at warning time: got %q, want running (not yet expired)", row.State)
	}
}

// A session further than the window from its deadline is not yet warned.
func TestExpiryWarningNotBeforeWindow_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(6000 * time.Second) // 1200s before the cap, outside the 300s window.
	activeRunning(t, store, "sess", "acme", born, now)
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ExpiryWarnings != 0 || len(hook.warnings) != 0 {
		t.Errorf("ExpiryWarnings=%d warnings=%d, want 0/0 outside the window", res.ExpiryWarnings, len(hook.warnings))
	}
}

// The warning fires at most once per session across repeated ticks inside the
// window.
func TestExpiryWarningDedupsAcrossTicks_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	first := born.Add(6901 * time.Second)
	activeRunning(t, store, "sess", "acme", born, first)
	if _, err := w.Tick(context.Background(), first); err != nil {
		t.Fatalf("Tick 1: %v", err)
	}
	// Keep the session active so the idle sweep never reaps it before the
	// second tick, and tick again still inside the window.
	mustUpdateActivity(t, store, "acme", "sess", born.Add(6902*time.Second).Add(-30*time.Second))
	res, err := w.Tick(context.Background(), born.Add(6902*time.Second))
	if err != nil {
		t.Fatalf("Tick 2: %v", err)
	}
	if res.ExpiryWarnings != 0 {
		t.Errorf("second tick re-warned: ExpiryWarnings=%d, want 0", res.ExpiryWarnings)
	}
	if len(hook.warnings) != 1 {
		t.Errorf("total warnings across two ticks: got %d, want 1", len(hook.warnings))
	}
}

// The warning tracks the effective (per-runtime) maxSessionAge cap, not the
// platform default: a tighter resolver cap warns earlier.
func TestExpiryWarningRespectsResolverCap_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(701 * time.Second) // 299s before a 1000s runtime cap.
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "tight", CreatedAt: born, UpdatedAt: born,
		LastAgentActivityAt: now.Add(-30 * time.Second),
	})
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook).
		WithSessionAgeResolver(fakeAgeResolver{"tight": 1000})

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ExpiryWarnings != 1 {
		t.Fatalf("ExpiryWarnings: got %d, want 1 (tighter cap warns earlier)", res.ExpiryWarnings)
	}
	if wn := hook.warnings[0]; wn.maxSessionAge != 1000 || wn.remainingSeconds != 299 {
		t.Errorf("warning = %+v, want maxSessionAge 1000 remaining 299", wn)
	}
}

// A terminal session is never warned (and is pruned from the dedup set).
func TestExpiryWarningSkipsTerminalSessions_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(6901 * time.Second)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: born, UpdatedAt: born,
	})
	hook := &captureExpiryWarning{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ExpiryWarnings != 0 || len(hook.warnings) != 0 {
		t.Errorf("terminal session warned: ExpiryWarnings=%d warnings=%d", res.ExpiryWarnings, len(hook.warnings))
	}
}

// A TerminalHook that does not implement ExpiryWarningNotifier makes the sweep
// a no-op (the type assertion is genuinely optional).
func TestExpiryWarningNoopWithoutNotifier_spec_11_3_240(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := born.Add(6901 * time.Second)
	activeRunning(t, store, "sess", "acme", born, now)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(&terminalOnly{})

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ExpiryWarnings != 0 {
		t.Errorf("ExpiryWarnings without a notifier: got %d, want 0", res.ExpiryWarnings)
	}
}

func mustUpdateActivity(t *testing.T, store sessionstore.Store, tenant, id string, at time.Time) {
	t.Helper()
	if _, err := store.Update(context.Background(), tenant, id, func(r *sessionstore.Session) error {
		r.LastAgentActivityAt = at
		return nil
	}); err != nil {
		t.Fatalf("update activity: %v", err)
	}
}
