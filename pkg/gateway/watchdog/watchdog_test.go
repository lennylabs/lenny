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

func TestTickRespectsBudgetForEveryState(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		state   session.State
		budget  time.Duration
		reason  string
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
	res, err := w.Tick(context.Background(), t0.Add(48*time.Hour))
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.ForcedFailures != 0 {
		t.Errorf("post-running states must not be touched: %+v", res)
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
