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

// spec: §6.2 lines 246-254, 292; §7.3 line 404 — F-6.2.14
//
// The watchdog must:
//   - transition resume_pending → awaiting_client_action when the §6.2
//     line 292 wall-clock cap expires;
//   - transition resuming → resume_pending on the §6.2 line 249 300s
//     timeout when retries remain (and bump retryCount);
//   - transition resuming → awaiting_client_action when retries are
//     exhausted;
//   - honour the per-session retryPolicy.maxResumeWindowSeconds tightener
//     and retryPolicy.maxRetries override;
//   - fire the awaiting-action entry notifier on every entry into the
//     state so the §7.3 line 427 webhook + §11.7 audit row land;
//   - fire the retry-attempt notifier on the resuming → resume_pending
//     retry path so the §16.1 metric + §11.7 audit row see the
//     watchdog-initiated retry.

type captureLifecycleHook struct {
	terminalCalls []sessionstore.Session
	awaitingEntry []sessionstore.Session
	retryAttempts []sessionstore.Session
}

func (c *captureLifecycleHook) OnSessionTerminal(_ context.Context, sess sessionstore.Session) {
	c.terminalCalls = append(c.terminalCalls, sess)
}

func (c *captureLifecycleHook) OnSessionEnteredAwaitingClientAction(_ context.Context, sess sessionstore.Session) {
	c.awaitingEntry = append(c.awaitingEntry, sess)
}

func (c *captureLifecycleHook) OnSessionRetryAttempt(_ context.Context, sess sessionstore.Session) {
	c.retryAttempts = append(c.retryAttempts, sess)
}

func TestSweepResumePendingExpiresPastCap_spec_6_2_292(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_rp", "acme", session.StateResumePending, born)
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	// 901s into resume_pending — just past the 900s platform default.
	res, err := w.Tick(context.Background(), born.Add(901*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumePendingTimeouts != 1 {
		t.Errorf("ResumePendingTimeouts = %d, want 1", res.ResumePendingTimeouts)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_rp")
	if row.State != session.StateAwaitingClientAction {
		t.Errorf("state = %q, want awaiting_client_action", row.State)
	}
	if len(hook.awaitingEntry) != 1 || hook.awaitingEntry[0].ID != "sess_rp" {
		t.Errorf("F-6.2.14: awaiting-entry hook calls = %d, want 1 for sess_rp", len(hook.awaitingEntry))
	}
	if len(hook.terminalCalls) != 0 {
		t.Errorf("F-6.2.14: resume_pending → awaiting is non-terminal; terminal hook calls = %d, want 0", len(hook.terminalCalls))
	}
}

func TestSweepResumePendingLeavesYoungRowAlone_spec_6_2_292(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_young", "acme", session.StateResumePending, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// 600s — well under the 900s platform default.
	res, err := w.Tick(context.Background(), born.Add(600*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumePendingTimeouts != 0 {
		t.Errorf("a young resume_pending row must not be transitioned: ResumePendingTimeouts=%d", res.ResumePendingTimeouts)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_young")
	if row.State != session.StateResumePending {
		t.Errorf("state = %q, want resume_pending", row.State)
	}
}

func seedResumePendingWithRetryPolicy(t *testing.T, store sessionstore.Store, id, tenant string, born time.Time, policy *session.RetryPolicy) {
	t.Helper()
	row := sessionstore.Session{
		ID:          id,
		TenantID:    tenant,
		State:       session.StateResumePending,
		CreatedAt:   born,
		UpdatedAt:   born,
		RetryPolicy: policy,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestSweepResumePendingHonoursPerSessionTightener_spec_6_2_292(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedResumePendingWithRetryPolicy(t, store, "sess_tight", "acme", born, &session.RetryPolicy{
		MaxResumeWindowSeconds: 120, // tighter than 900s default
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// 150s — past the per-session 120s cap but well under the 900s default.
	res, err := w.Tick(context.Background(), born.Add(150*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumePendingTimeouts != 1 {
		t.Errorf("per-session 120s cap must fire: ResumePendingTimeouts=%d", res.ResumePendingTimeouts)
	}
}

func TestSweepResumePendingTightenerCannotExceedPlatformCap_spec_6_2_292(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedResumePendingWithRetryPolicy(t, store, "sess_huge", "acme", born, &session.RetryPolicy{
		MaxResumeWindowSeconds: 86400, // 24h, well past platform cap
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// 901s — past the 900s platform cap; per-session "override" is wider
	// but must be clamped to the platform cap.
	res, err := w.Tick(context.Background(), born.Add(901*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumePendingTimeouts != 1 {
		t.Errorf("platform cap must still bound: ResumePendingTimeouts=%d", res.ResumePendingTimeouts)
	}
}

func TestSweepResumingRetriesRemain_spec_6_2_249(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_rs", TenantID: "acme",
		State: session.StateResuming, CreatedAt: born, UpdatedAt: born,
		RetryCount: 0, // < default maxRetries 2
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	// 301s — just past the 300s resuming budget.
	res, err := w.Tick(context.Background(), born.Add(301*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumingTimeouts != 1 || res.PerResumingOutcome[watchdog.ResumingRetryOutcome] != 1 {
		t.Errorf("F-6.2.14 resuming retry: %+v", res)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_rs")
	if row.State != session.StateResumePending {
		t.Errorf("state = %q, want resume_pending", row.State)
	}
	if row.RetryCount != 1 {
		t.Errorf("RetryCount = %d, want 1", row.RetryCount)
	}
	if len(hook.retryAttempts) != 1 {
		t.Errorf("F-6.2.14: retry hook calls = %d, want 1", len(hook.retryAttempts))
	}
	if len(hook.awaitingEntry) != 0 {
		t.Errorf("retry branch must not fire awaiting-entry hook; got %d", len(hook.awaitingEntry))
	}
}

func TestSweepResumingExhausted_spec_6_2_249(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_rs", TenantID: "acme",
		State: session.StateResuming, CreatedAt: born, UpdatedAt: born,
		RetryCount: 2, // == default maxRetries 2 -> exhausted
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), born.Add(301*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumingTimeouts != 1 || res.PerResumingOutcome[watchdog.ResumingExhaustedOutcome] != 1 {
		t.Errorf("F-6.2.14 resuming exhausted: %+v", res)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_rs")
	if row.State != session.StateAwaitingClientAction {
		t.Errorf("state = %q, want awaiting_client_action", row.State)
	}
	if row.RetryCount != 2 {
		t.Errorf("RetryCount must not advance on exhausted branch; got %d", row.RetryCount)
	}
	if len(hook.awaitingEntry) != 1 {
		t.Errorf("F-6.2.14: awaiting-entry hook calls = %d, want 1", len(hook.awaitingEntry))
	}
	if len(hook.retryAttempts) != 0 {
		t.Errorf("exhausted branch must not fire retry hook; got %d", len(hook.retryAttempts))
	}
}

func TestSweepResumingHonoursPerSessionMaxRetries_spec_6_2_249(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Per-session maxRetries=5, retryCount=3 — still within budget.
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_rs", TenantID: "acme",
		State: session.StateResuming, CreatedAt: born, UpdatedAt: born,
		RetryCount:  3,
		RetryPolicy: &session.RetryPolicy{MaxRetries: 5},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	hook := &captureLifecycleHook{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	res, err := w.Tick(context.Background(), born.Add(301*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.PerResumingOutcome[watchdog.ResumingRetryOutcome] != 1 {
		t.Errorf("per-session maxRetries=5 must permit retry: %+v", res)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_rs")
	if row.State != session.StateResumePending || row.RetryCount != 4 {
		t.Errorf("state=%q retry=%d, want resume_pending/4", row.State, row.RetryCount)
	}
}

func TestSweepResumingDoesNotTouchYoung_spec_6_2_249(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_young", TenantID: "acme",
		State: session.StateResuming, CreatedAt: born, UpdatedAt: born,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	res, err := w.Tick(context.Background(), born.Add(200*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ResumingTimeouts != 0 {
		t.Errorf("a young resuming row must not be transitioned: %+v", res)
	}
}

func TestConfigDefaultsForResumeAndResumingMatchSpec_spec_6_2(t *testing.T) {
	if watchdog.DefaultMaxResumePendingSeconds != 900 {
		t.Errorf("DefaultMaxResumePendingSeconds = %d, want 900", watchdog.DefaultMaxResumePendingSeconds)
	}
	if watchdog.DefaultMaxResumingSeconds != 300 {
		t.Errorf("DefaultMaxResumingSeconds = %d, want 300", watchdog.DefaultMaxResumingSeconds)
	}
	if watchdog.DefaultMaxRetries != 2 {
		t.Errorf("DefaultMaxRetries = %d, want 2", watchdog.DefaultMaxRetries)
	}
}

// A TerminalHook that does not implement the optional entry notifier
// must continue to receive only OnSessionTerminal (which the
// resume_pending sweep does NOT fire — the transition is non-terminal).
// The sweep must remain silent on a partial hook implementation.
type terminalOnlyForResume struct {
	terminalCalls []sessionstore.Session
}

func (t *terminalOnlyForResume) OnSessionTerminal(_ context.Context, sess sessionstore.Session) {
	t.terminalCalls = append(t.terminalCalls, sess)
}

func TestSweepResumePendingWithoutEntryNotifierSilent_spec_6_2_292(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_rp", "acme", session.StateResumePending, born)
	hook := &terminalOnlyForResume{}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(901*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_rp")
	if row.State != session.StateAwaitingClientAction {
		t.Errorf("state = %q, want awaiting_client_action (transition must happen even without optional notifier)", row.State)
	}
	if len(hook.terminalCalls) != 0 {
		t.Errorf("resume_pending → awaiting is non-terminal: terminal hook calls = %d, want 0", len(hook.terminalCalls))
	}
}
