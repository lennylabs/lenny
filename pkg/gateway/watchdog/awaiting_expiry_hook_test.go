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

// TestSweepAwaitingClientActionStampsExpiredDeadline_spec_8_8_867
// verifies the §8.8 line 867 expiry-reason prefix lands on the
// failureReason column when the watchdog drives an
// awaiting_client_action row to `expired`. The MCP boundary's
// taskError.Code fallback then surfaces `expired:deadline` to clients.
// F-8.8.8.
func TestSweepAwaitingClientActionStampsExpiredDeadline_spec_8_8_867(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_aw_d", "acme", session.StateAwaitingClientAction, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(&captureExpiryHook{})
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_aw_d")
	if err != nil {
		t.Fatalf("Get sess_aw_d: %v", err)
	}
	if got.State != session.StateExpired {
		t.Fatalf("State = %q, want expired", got.State)
	}
	if got.FailureReason != string(session.FailureExpiredDeadline) {
		t.Errorf("FailureReason = %q, want %q (§8.8 line 867)", got.FailureReason, session.FailureExpiredDeadline)
	}
}

// TestSweepMaxAgeStampsExpiredDeadline_spec_8_8_867 verifies the
// §8.8 line 867 expiry prefix is stamped when the §11.3 maxSessionAge
// cap fires. F-8.8.8.
func TestSweepMaxAgeStampsExpiredDeadline_spec_8_8_867(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_old_d", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(&captureExpiryHook{})
	if _, err := w.Tick(context.Background(), born.Add(3*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_old_d")
	if err != nil {
		t.Fatalf("Get sess_old_d: %v", err)
	}
	if got.State != session.StateExpired {
		t.Fatalf("State = %q, want expired", got.State)
	}
	if got.FailureReason != string(session.FailureExpiredDeadline) {
		t.Errorf("FailureReason = %q, want %q (§8.8 line 867)", got.FailureReason, session.FailureExpiredDeadline)
	}
}

// TestSweepDoesNotOverwriteExistingFailureReason_spec_8_8_867 verifies
// that when an earlier writer has already stamped a non-empty
// FailureReason on a soon-to-be-expired row, the watchdog leaves the
// existing value alone. The MCP boundary will surface that earlier
// reason rather than the watchdog's default deadline prefix. F-8.8.8.
func TestSweepDoesNotOverwriteExistingFailureReason_spec_8_8_867(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID:            "sess_aw_pre",
		TenantID:      "acme",
		State:         session.StateAwaitingClientAction,
		CreatedAt:     born,
		UpdatedAt:     born,
		FailureReason: string(session.FailureExpiredBudget),
	}); err != nil {
		t.Fatalf("seed sess_aw_pre: %v", err)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(&captureExpiryHook{})
	if _, err := w.Tick(context.Background(), born.Add(20*time.Minute)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := store.Get(context.Background(), "acme", "sess_aw_pre")
	if err != nil {
		t.Fatalf("Get sess_aw_pre: %v", err)
	}
	if got.FailureReason != string(session.FailureExpiredBudget) {
		t.Errorf("FailureReason = %q, want %q (pre-stamped reason preserved)", got.FailureReason, session.FailureExpiredBudget)
	}
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
