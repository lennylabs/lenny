// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
	"github.com/lennylabs/lenny/pkg/gateway/watchdog"
)

// fakeDLQSweeper records which sessions the watchdog asked it to sweep and
// returns a per-session expired count (and optionally an error).
type fakeDLQSweeper struct {
	swept []string
	count map[string]int
	errOn map[string]bool
}

func (f *fakeDLQSweeper) SweepExpired(_ context.Context, _, sessionID string) (int, error) {
	f.swept = append(f.swept, sessionID)
	if f.errOn[sessionID] {
		return 0, errors.New("redis blip")
	}
	return f.count[sessionID], nil
}

// spec: §7.2 lines 294, 341 — the DLQ TTL trimmer is state-gated: the
// watchdog runs SweepExpired for every session in a recovering state
// (resume_pending, awaiting_client_action) and for no other state, and
// accumulates the expired count in Result.DLQExpired.
func TestSweepDLQExpiry_OnlyRecoveringSessions_spec_7_2_294(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	// Recent UpdatedAt so no state-timeout sweep fires; the rows stay in
	// their seeded states for the DLQ sweep to observe.
	seedRow(t, store, "sess_rp", "acme", session.StateResumePending, now)
	seedRow(t, store, "sess_aca", "acme", session.StateAwaitingClientAction, now)
	seedRow(t, store, "sess_run", "acme", session.StateRunning, now)
	seedRow(t, store, "sess_susp", "acme", session.StateSuspended, now)

	sweeper := &fakeDLQSweeper{count: map[string]int{"sess_rp": 2, "sess_aca": 3}}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithMessaging(sweeper)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.DLQExpired != 5 {
		t.Errorf("DLQExpired = %d, want 5 (2+3)", res.DLQExpired)
	}
	if got := len(sweeper.swept); got != 2 {
		t.Fatalf("swept %d sessions, want 2 (only recovering): %v", got, sweeper.swept)
	}
	got := map[string]bool{}
	for _, id := range sweeper.swept {
		got[id] = true
	}
	if !got["sess_rp"] || !got["sess_aca"] {
		t.Errorf("swept = %v, want sess_rp and sess_aca", sweeper.swept)
	}
	if got["sess_run"] || got["sess_susp"] {
		t.Errorf("non-recovering session was swept: %v", sweeper.swept)
	}
}

// A per-session sweep error must not stall the tick or the other sessions'
// sweeps: the erroring session is skipped and the rest still sweep.
func TestSweepDLQExpiry_PerSessionErrorIsolated_spec_7_2_341(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_a", "acme", session.StateResumePending, now)
	seedRow(t, store, "sess_b", "acme", session.StateResumePending, now)

	sweeper := &fakeDLQSweeper{
		count: map[string]int{"sess_b": 4},
		errOn: map[string]bool{"sess_a": true},
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil).
		WithMessaging(sweeper)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick returned error, want per-session error swallowed: %v", err)
	}
	if len(sweeper.swept) != 2 {
		t.Errorf("both sessions must be attempted; swept = %v", sweeper.swept)
	}
	if res.DLQExpired != 4 {
		t.Errorf("DLQExpired = %d, want 4 (only the non-erroring session counts)", res.DLQExpired)
	}
}

// Without WithMessaging the watchdog runs no DLQ sweep and never panics.
func TestSweepDLQExpiry_NilMessaging_NoOp_spec_7_2_294(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_rp", "acme", session.StateResumePending, now)
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, watchdog.Config{}, nil)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.DLQExpired != 0 {
		t.Errorf("DLQExpired = %d, want 0 when messaging unwired", res.DLQExpired)
	}
}
