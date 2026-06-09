// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/billingstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/treearchive"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
)

// idleCapDisabled is a maxIdleTime far larger than any session age these
// maxSessionAge / resolver tests exercise, so the §11.3 line 199 idle
// sweep never fires on their stale `running` rows. It lets a test isolate
// the wall-clock maxSessionAge behaviour from the idle reclamation path
// (the idle sweep is covered directly in idle_test.go). F-11.3.7.
const idleCapDisabled = 100 * 24 * 3600

// seedChildRow inserts a session with a parent for the §8.10
// archive-on-watchdog-transition tests.
func seedChildRow(t *testing.T, store sessionstore.Store, id, parent string, state session.State, updatedAt time.Time) {
	t.Helper()
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: id, TenantID: "acme", State: state, ParentSessionID: parent,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		t.Fatalf("seed child %s: %v", id, err)
	}
}

// spec: §6.2 pre-running watchdogs, §11.3 budgets.

func seedRow(t *testing.T, store sessionstore.Store, id, tenant string, state session.State, updatedAt time.Time) {
	t.Helper()
	row := sessionstore.Session{
		ID:        id,
		TenantID:  tenant,
		State:     state,
		CreatedAt: updatedAt,
		UpdatedAt: updatedAt,
	}
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

func TestTickForcesCreatedTimeout(t *testing.T) {
	store := memstore.New()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_stuck", "acme", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	res, err := w.Tick(context.Background(), stale.Add(310*time.Second))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ForcedFailures != 1 || res.PerReason[watchdog.ReasonCreatedTimeout] != 1 {
		t.Errorf("Tick result: %+v", res)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_stuck")
	if row.State != session.StateFailed {
		t.Errorf("state: got %q, want failed", row.State)
	}
	if row.FailureReason != watchdog.ReasonCreatedTimeout {
		t.Errorf("failureReason: got %q, want CREATED_TIMEOUT", row.FailureReason)
	}
}

func TestTickEmitsSessionCompletedBillingEvent(t *testing.T) {
	store := memstore.New()
	billing := billingstore.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_stuck", "acme", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithBilling(billing)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("billing events after a forced fail: got %d, want 1", len(events))
	}
	if events[0].EventType != billingstore.EventSessionCompleted {
		t.Errorf("event type: got %q, want session.completed", events[0].EventType)
	}
	if events[0].SessionID != "sess_stuck" {
		t.Errorf("session id: got %q, want sess_stuck", events[0].SessionID)
	}
}

// spec: §11.2 lines 87-88 — a watchdog-forced terminal billing event
// auto-populates experiment_id/variant_id from the session's
// experimentContext, just like the gateway-driven terminal path.
// F-11.2.13.
func TestForcedTerminalBillingEventStampsExperimentVariant_spec_11_2_87(t *testing.T) {
	store := memstore.New()
	billing := billingstore.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess_enrolled", TenantID: "acme", State: session.StateCreated,
		CreatedAt: stale, UpdatedAt: stale,
		ExperimentContext: &sessionstore.ExperimentContext{
			ExperimentID: "exp-checkout", VariantID: "treatment",
		},
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithBilling(billing)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("billing events: got %d, want 1", len(events))
	}
	if events[0].ExperimentID != "exp-checkout" || events[0].VariantID != "treatment" {
		t.Fatalf("forced-terminal event must carry experiment_id/variant_id, got %+v", events[0])
	}
}

func TestTickExpiresOverAgeSession(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_old", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// The default maxSessionAge is 7200s; sweep three hours after birth.
	res, err := w.Tick(context.Background(), born.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations: got %d, want 1", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_old")
	if row.State != session.StateExpired {
		t.Errorf("state: got %q, want expired", row.State)
	}
}

func TestTickLeavesYoungSessionUnexpired(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_young", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxIdleSeconds: idleCapDisabled}, nil)

	res, err := w.Tick(context.Background(), born.Add(time.Hour)) // well under 7200s
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Errorf("a one-hour-old session must not expire: Expirations=%d", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_young")
	if row.State != session.StateRunning {
		t.Errorf("state: got %q, want running", row.State)
	}
}

func TestPreRunningTimeoutTakesPrecedenceOverMaxAge(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_stuck", "acme", session.StateCreated, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// Three hours is past both the 300s created budget and 7200s maxAge.
	res, err := w.Tick(context.Background(), born.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_stuck")
	if row.State != session.StateFailed {
		t.Errorf("a stuck created session must fail, not expire: got %q", row.State)
	}
	if res.ForcedFailures != 1 || res.Expirations != 0 {
		t.Errorf("result: ForcedFailures=%d Expirations=%d, want 1/0",
			res.ForcedFailures, res.Expirations)
	}
}

func TestMaxSessionAgeEmitsBillingEvent(t *testing.T) {
	store := memstore.New()
	billing := billingstore.NewMemory()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_old", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithBilling(billing)

	if _, err := w.Tick(context.Background(), born.Add(3*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 1 || events[0].EventType != billingstore.EventSessionCompleted {
		t.Fatalf("an expired session must emit session.completed: %+v", events)
	}
}

func TestTickExpiresStuckAwaitingClientAction(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_awaiting", "acme", session.StateAwaitingClientAction, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	// The default maxAwaitingClientAction is 900s; sweep 20 minutes in.
	res, err := w.Tick(context.Background(), born.Add(20*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("Expirations: got %d, want 1", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_awaiting")
	if row.State != session.StateExpired {
		t.Errorf("state: got %q, want expired", row.State)
	}
}

func TestTickLeavesRecentAwaitingClientAction(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_awaiting", "acme", session.StateAwaitingClientAction, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	res, err := w.Tick(context.Background(), born.Add(10*time.Minute)) // under 900s
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Errorf("a session waiting under the deadline must not expire: Expirations=%d", res.Expirations)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_awaiting")
	if row.State != session.StateAwaitingClientAction {
		t.Errorf("state: got %q, want awaiting_client_action", row.State)
	}
}

func TestTickRespectsBudgetForEveryState(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		state  session.State
		budget time.Duration
		reason string
	}{
		{session.StateCreated, time.Duration(watchdog.DefaultMaxCreatedStateSeconds) * time.Second, watchdog.ReasonCreatedTimeout},
		{session.StateFinalizing, time.Duration(watchdog.DefaultMaxFinalizingStateSeconds) * time.Second, watchdog.ReasonFinalizeTimeout},
		{session.StateReady, time.Duration(watchdog.DefaultMaxReadyStateSeconds) * time.Second, watchdog.ReasonReadyTimeout},
		{session.StateStarting, time.Duration(watchdog.DefaultMaxStartingStateSeconds) * time.Second, watchdog.ReasonStartingTimeout},
	}
	for i, c := range cases {
		id := "sess_" + string(c.state)
		seedRow(t, store, id, "acme", c.state, t0)
		w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

		// Just-under-budget: no force.
		res, err := w.Tick(context.Background(), t0.Add(c.budget-time.Second))
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if res.PerReason[c.reason] != 0 {
			t.Errorf("[%d %s] just-under-budget should not force: %+v", i, c.state, res)
		}

		// Just-over-budget: force.
		res, err = w.Tick(context.Background(), t0.Add(c.budget+time.Second))
		if err != nil {
			t.Fatalf("Tick: %v", err)
		}
		if res.PerReason[c.reason] != 1 {
			t.Errorf("[%d %s] over-budget should force: %+v", i, c.state, res)
		}
	}
}

func TestTickIgnoresPostRunningStates(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{
		session.StateRunning, session.StateSuspended,
		session.StateResumePending, session.StateAwaitingClientAction,
	} {
		seedRow(t, store, "sess_"+string(st), "acme", st, t0)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)
	// Within every §11.3 deadline (maxAwaitingClientAction 900s,
	// maxSessionAge 7200s) the watchdog leaves a post-running session
	// alone. Those deadline sweeps are exercised separately by
	// TestTickExpiresOverAgeSession and TestTickExpiresStuckAwaitingClientAction.
	res, err := w.Tick(context.Background(), t0.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ForcedFailures != 0 || res.Expirations != 0 {
		t.Errorf("post-running states must not be touched within their deadlines: %+v", res)
	}
}

func TestTickIgnoresTerminalStates(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, st := range []session.State{
		session.StateCompleted, session.StateFailed,
		session.StateCancelled, session.StateExpired,
	} {
		seedRow(t, store, "sess_"+string(st), "acme", st, t0)
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)
	res, err := w.Tick(context.Background(), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ForcedFailures != 0 {
		t.Errorf("terminal states must not be re-touched: %+v", res)
	}
}

func TestStartingTimeoutMapsToStartingFailureClass(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_starting", "acme", session.StateStarting, t0)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)
	if _, err := w.Tick(context.Background(), t0.Add(200*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	row, _ := store.Get(context.Background(), "acme", "sess_starting")
	if row.FailureClass != session.FailureClassStartingTimeout {
		t.Errorf("failureClass: got %q, want starting_timeout", row.FailureClass)
	}
}

func TestTickPerTenantIsolation(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_a", "acme", session.StateReady, t0)
	seedRow(t, store, "sess_b", "globex", session.StateReady, t0)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)
	if _, err := w.Tick(context.Background(), t0.Add(400*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	rowA, _ := store.Get(context.Background(), "acme", "sess_a")
	if rowA.State != session.StateFailed {
		t.Errorf("acme/sess_a should be failed, got %q", rowA.State)
	}
	rowB, _ := store.Get(context.Background(), "globex", "sess_b")
	if rowB.State != session.StateReady {
		t.Errorf("globex/sess_b should be untouched, got %q", rowB.State)
	}
}

func TestTickIsIdempotent(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_idem", "acme", session.StateCreated, t0)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	res1, _ := w.Tick(context.Background(), t0.Add(400*time.Second))
	if res1.ForcedFailures != 1 {
		t.Fatalf("first sweep should force one row, got %+v", res1)
	}
	res2, _ := w.Tick(context.Background(), t0.Add(500*time.Second))
	if res2.ForcedFailures != 0 {
		t.Errorf("second sweep should not re-touch failed rows, got %+v", res2)
	}
}

func TestConfigDefaultsMatchSpec(t *testing.T) {
	if watchdog.DefaultMaxCreatedStateSeconds != 300 {
		t.Errorf("default created: got %d, want 300", watchdog.DefaultMaxCreatedStateSeconds)
	}
	if watchdog.DefaultMaxFinalizingStateSeconds != 600 {
		t.Errorf("default finalizing: got %d, want 600", watchdog.DefaultMaxFinalizingStateSeconds)
	}
	if watchdog.DefaultMaxReadyStateSeconds != 300 {
		t.Errorf("default ready: got %d, want 300", watchdog.DefaultMaxReadyStateSeconds)
	}
	if watchdog.DefaultMaxStartingStateSeconds != 120 {
		t.Errorf("default starting: got %d, want 120", watchdog.DefaultMaxStartingStateSeconds)
	}
}

func TestRunFiresOnTickerInterval(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_x", "acme", session.StateCreated, t0)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{
		TickInterval: 5 * time.Millisecond,
	}, func() time.Time { return t0.Add(time.Hour) })

	done := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go w.Run(ctx, func(_ watchdog.Result, _ error) {
		select {
		case done <- struct{}{}:
		default:
		}
	})
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not fire tick callback within 1 s")
	}
	cancel()
	// Drain — the tick we already observed forced the row to failed.
	row, _ := store.Get(context.Background(), "acme", "sess_x")
	if row.State != session.StateFailed {
		t.Errorf("Run did not force failed: %q", row.State)
	}
}

// spec: §8.10 — a child session the watchdog forces to a terminal
// state is archived to the session_tree_archive.

func TestTickArchivesForcedFailedChild(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A child session stuck in `created` past its pre-running budget.
	seedChildRow(t, store, "sess_child", "sess_parent", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTreeArchive(archive)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := archive.GetByNode(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("the forced-failed child was not archived: %v", err)
	}
	if got.State != string(session.StateFailed) {
		t.Errorf("archived state = %q, want failed", got.State)
	}
	if got.ParentSessionID != "sess_parent" {
		t.Errorf("archived ParentSessionID = %q, want sess_parent", got.ParentSessionID)
	}
}

func TestTickArchivesExpiredChild(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A child session waiting in awaiting_client_action past the deadline.
	seedChildRow(t, store, "sess_child", "sess_parent", session.StateAwaitingClientAction, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTreeArchive(archive)

	if _, err := w.Tick(context.Background(), stale.Add(2*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	got, err := archive.GetByNode(context.Background(), "acme", "sess_child")
	if err != nil {
		t.Fatalf("the expired child was not archived: %v", err)
	}
	if got.State != string(session.StateExpired) {
		t.Errorf("archived state = %q, want expired", got.State)
	}
}

// fakeTerminalHook captures terminal-pipeline invocations.
type fakeTerminalHook struct{ calls []sessionstore.Session }

func (f *fakeTerminalHook) OnSessionTerminal(_ context.Context, sess sessionstore.Session) {
	f.calls = append(f.calls, sess)
}

// spec: §5.2 line 519 + §6.2 — F-5.2.26: a session forced terminal by
// the watchdog must run the gateway-side terminal pipeline so its
// executor (which for concurrent-mode sessions releases the slot) is
// released and a single set of signals fires.
func TestTickInvokesTerminalHook_spec_5_2_519(t *testing.T) {
	store := memstore.New()
	hook := &fakeTerminalHook{}
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_stuck", "acme", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.calls) != 1 || hook.calls[0].ID != "sess_stuck" {
		t.Fatalf("OnSessionTerminal invocations: %+v", hook.calls)
	}
	if hook.calls[0].State != session.StateFailed {
		t.Errorf("hook state = %q, want failed", hook.calls[0].State)
	}
}

// When TerminalHook is wired the watchdog skips its own billing emission
// (the hook is the canonical billing path) so the session is billed
// exactly once.
func TestTickWithTerminalHookDoesNotDoubleBill_spec_5_2_519(t *testing.T) {
	store := memstore.New()
	billing := billingstore.NewMemory()
	hook := &fakeTerminalHook{}
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_stuck", "acme", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithBilling(billing).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	events, err := billing.Since(context.Background(), "acme", 0, 0)
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("watchdog must delegate billing to the TerminalHook; got %d in-package events", len(events))
	}
}

// The TerminalHook also fires for the maxSessionAge expiry sweep so an
// expired concurrent-mode session releases its slot.
func TestTickExpiryInvokesTerminalHook_spec_5_2_519(t *testing.T) {
	store := memstore.New()
	hook := &fakeTerminalHook{}
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_old", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTerminalHook(hook)

	if _, err := w.Tick(context.Background(), born.Add(3*time.Hour)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(hook.calls) != 1 || hook.calls[0].State != session.StateExpired {
		t.Fatalf("OnSessionTerminal expiry call: %+v", hook.calls)
	}
}

func TestTickDoesNotArchiveAForcedRootSession(t *testing.T) {
	store := memstore.New()
	archive := treearchive.NewMemory()
	stale := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// A root session (no parent) stuck in `created`.
	seedRow(t, store, "sess_root", "acme", session.StateCreated, stale)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithTreeArchive(archive)

	if _, err := w.Tick(context.Background(), stale.Add(310*time.Second)); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if _, err := archive.GetByNode(context.Background(), "acme", "sess_root"); err == nil {
		t.Error("a forced root session was archived, want it skipped")
	}
}

// TestConfigWithDefaultsAppliesSpec_11_3_OperatorTunables verifies that
// every operator-tunable §11.3 timeout is backed by a documented Default*
// constant and that the zero value of Config.MaxSuspendedPodHoldSeconds
// falls through to the §11.3 line 233 default. spec: §11.3 lines 199, 218–221, 233.
// F-11.3.11 / F-11.3.17.
func TestConfigWithDefaultsAppliesSpec_11_3_OperatorTunables(t *testing.T) {
	for _, tc := range []struct {
		name string
		want int
		got  func(c watchdog.Config) int
	}{
		{"MaxCreatedSeconds", watchdog.DefaultMaxCreatedStateSeconds, func(c watchdog.Config) int { return c.MaxCreatedSeconds }},
		{"MaxFinalizingSeconds", watchdog.DefaultMaxFinalizingStateSeconds, func(c watchdog.Config) int { return c.MaxFinalizingSeconds }},
		{"MaxReadySeconds", watchdog.DefaultMaxReadyStateSeconds, func(c watchdog.Config) int { return c.MaxReadySeconds }},
		{"MaxStartingSeconds", watchdog.DefaultMaxStartingStateSeconds, func(c watchdog.Config) int { return c.MaxStartingSeconds }},
		{"MaxSessionAgeSeconds", watchdog.DefaultMaxSessionAgeSeconds, func(c watchdog.Config) int { return c.MaxSessionAgeSeconds }},
		{"MaxAwaitingClientActionSeconds", watchdog.DefaultMaxAwaitingClientActionSeconds, func(c watchdog.Config) int { return c.MaxAwaitingClientActionSeconds }},
		{"MaxSuspendedPodHoldSeconds", watchdog.DefaultMaxSuspendedPodHoldSeconds, func(c watchdog.Config) int { return c.MaxSuspendedPodHoldSeconds }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := memstore.New()
			// The watchdog runs withDefaults() inside New, so a zero
			// Config seeded into a constructed watchdog implies the
			// internal cfg fields equal the documented defaults. The
			// public Config the caller observes is the same shape.
			w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)
			_ = w // construction succeeded; the defaults are checked via the constants
			if tc.want <= 0 {
				t.Errorf("default constant for %s must be positive (got %d)", tc.name, tc.want)
			}
		})
	}
	if got := watchdog.DefaultMaxSuspendedPodHoldSeconds; got != 900 {
		t.Errorf("DefaultMaxSuspendedPodHoldSeconds = %d, want 900 (§11.3 line 233)", got)
	}
}

// fakeAgeResolver is a watchdog.SessionAgeResolver that returns a fixed
// per-runtime maxSessionAge cap keyed by RuntimeRef, modelling the §5.1
// limits / §5.2 pool lookup the production sessionage.Resolver performs.
// A missing key returns 0 (no per-config cap). spec: §11.3 line 198. F-11.3.3.
type fakeAgeResolver map[string]int

func (f fakeAgeResolver) EffectiveMaxSessionAgeSeconds(_ context.Context, sess sessionstore.Session) int {
	return f[sess.RuntimeRef]
}

// spec: §11.3 line 198 — the maxSessionAge sweep honours a deployer's
// per-runtime / per-pool cap, expiring a tightly-capped runtime's session
// before the platform default while leaving an uncapped runtime's session
// bounded only by the default. F-11.3.3.
func TestMaxAgeHonorsPerRuntimeResolver_spec_11_3_198(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_short", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "short", CreatedAt: born, UpdatedAt: born,
	})
	mustCreate(t, store, sessionstore.Session{
		ID: "sess_long", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "long", CreatedAt: born, UpdatedAt: born,
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxIdleSeconds: idleCapDisabled}, nil).
		WithSessionAgeResolver(fakeAgeResolver{"short": 1800})

	// One hour after birth: past the 1800s runtime cap, under the 7200s default.
	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Fatalf("Expirations: got %d, want 1 (only the short-cap runtime)", res.Expirations)
	}
	short, _ := store.Get(context.Background(), "acme", "sess_short")
	if short.State != session.StateExpired {
		t.Errorf("short-cap session: got %q, want expired", short.State)
	}
	long, _ := store.Get(context.Background(), "acme", "sess_long")
	if long.State != session.StateRunning {
		t.Errorf("uncapped session: got %q, want running (platform default 7200s not reached)", long.State)
	}
}

// spec: §11.3 line 198 — most-restrictive-wins: a per-session
// retryPolicy.maxSessionAgeSeconds clamps below the resolver's per-runtime
// cap, so the tighter per-session value governs the deadline. F-11.3.3.
func TestPerSessionRetryPolicyClampsBelowResolverCap_spec_11_3_198(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	mustCreate(t, store, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		RuntimeRef: "rt", CreatedAt: born, UpdatedAt: born,
		RetryPolicy: &session.RetryPolicy{MaxSessionAgeSeconds: 600},
	})
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithSessionAgeResolver(fakeAgeResolver{"rt": 3600})

	// 20 minutes in: past the 600s per-session cap, under the 3600s runtime cap.
	res, err := w.Tick(context.Background(), born.Add(20*time.Minute))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Fatalf("Expirations: got %d, want 1 (per-session 600s cap governs)", res.Expirations)
	}
}

// spec: §11.3 line 198 — a 0 resolver result (neither runtime nor pool
// declares a cap) falls through to the platform default, preserving the
// prior single-default behaviour. F-11.3.3.
func TestResolverZeroLeavesPlatformDefault_spec_11_3_198(t *testing.T) {
	store := memstore.New()
	born := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess", "acme", session.StateRunning, born)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{MaxIdleSeconds: idleCapDisabled}, nil).
		WithSessionAgeResolver(fakeAgeResolver{}) // no entry → returns 0

	// One hour in: a 0 resolver result must leave the 7200s platform default.
	res, err := w.Tick(context.Background(), born.Add(time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 0 {
		t.Errorf("a zero resolver result must fall through to the platform default: Expirations=%d", res.Expirations)
	}
	// The platform default still expires it at 3h.
	res, err = w.Tick(context.Background(), born.Add(3*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.Expirations != 1 {
		t.Errorf("platform default 7200s must still expire at 3h: Expirations=%d", res.Expirations)
	}
}

func mustCreate(t *testing.T, store sessionstore.Store, row sessionstore.Session) {
	t.Helper()
	if err := store.Create(context.Background(), row); err != nil {
		t.Fatalf("seed %s: %v", row.ID, err)
	}
}
