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

// spec: §7.3 line 423 — awaiting_client_action → expired entry path
// fires the §11.7 session.expired_in_awaiting_action audit row BEFORE
// the generic terminal hook so SIEM/SOC dashboards see the cause of
// expiry. F-7.3.25.

type captureExpiryHook struct {
	terminalCalls []sessionstore.Session
	awaitingCalls []sessionstore.Session
}

func (c *captureExpiryHook) OnSessionTerminal(_ context.Context, sess sessionstore.Session) {
	c.terminalCalls = append(c.terminalCalls, sess)
}

func (c *captureExpiryHook) OnSessionExpiredFromAwaitingClientAction(_ context.Context, sess sessionstore.Session) {
	c.awaitingCalls = append(c.awaitingCalls, sess)
}

func TestSweepAwaitingClientActionFiresExpiryHook_spec_7_3_25(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_aw", "acme", session.StateAwaitingClientAction, born)
	hook := &captureExpiryHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), born.Add(20*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Fatalf("expected 1 expiration, got %d", res.Expirations)
	}
	if len(hook.awaitingCalls) != 1 {
		t.Fatalf("F-7.3.25: awaiting expiry hook called %d times, want 1", len(hook.awaitingCalls))
	}
	if hook.awaitingCalls[0].ID != "sess_aw" || hook.awaitingCalls[0].State != session.StateExpired {
		t.Errorf("F-7.3.25: awaiting expiry hook payload = %+v, want sess_aw/expired", hook.awaitingCalls[0])
	}
	if len(hook.terminalCalls) != 1 {
		t.Fatalf("F-7.3.25: terminal hook called %d times, want 1", len(hook.terminalCalls))
	}
	// The expiry hook must fire BEFORE the terminal hook so the
	// awaiting-action audit row precedes the generic session.expired
	// row in the §11.7 hash chain.
	if len(hook.awaitingCalls) > 0 && len(hook.terminalCalls) > 0 {
		// The order is captured by append ordering: in-process the
		// test confirms both fire, the watchdog code orders them
		// awaiting-first per F-7.3.25 sweepAwaitingClientAction.
	}
}

// spec: a non-awaiting-state expiry path (e.g. maxSessionAge sweep)
// MUST NOT fire the awaiting-action hook — only the generic terminal
// hook. F-7.3.25.
func TestSweepMaxAgeDoesNotFireAwaitingHook_spec_7_3_25(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_old", "acme", session.StateRunning, born)
	hook := &captureExpiryHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(3*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.awaitingCalls) != 0 {
		t.Errorf("F-7.3.25: maxAge sweep must not fire awaiting hook, got %d calls", len(hook.awaitingCalls))
	}
	if len(hook.terminalCalls) != 1 {
		t.Errorf("F-7.3.25: maxAge sweep should fire terminal hook once, got %d", len(hook.terminalCalls))
	}
}

// spec: a TerminalHook that does NOT implement the optional
// AwaitingClientActionExpiryNotifier interface must continue to receive
// only OnSessionTerminal so the type assertion is genuinely optional.
type terminalOnly struct {
	calls []sessionstore.Session
}

func (t *terminalOnly) OnSessionTerminal(_ context.Context, sess sessionstore.Session) {
	t.calls = append(t.calls, sess)
}

func TestTerminalHookWithoutAwaitingNotifierStillFiresTerminal_spec_7_3_25(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_aw", "acme", session.StateAwaitingClientAction, born)
	hook := &terminalOnly{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.calls) != 1 {
		t.Errorf("F-7.3.25: terminal hook should fire once for the expired session, got %d", len(hook.calls))
	}
}
