// SPDX-License-Identifier: MIT

package watchdog_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/watchdog"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessioninbox"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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

// dlqCfg keeps every state-timeout sweep from moving the seeded rows, so the
// DLQ sweep observes them in their seeded states.
func dlqCfg() watchdog.Config {
	const off = 365 * 24 * 3600
	return watchdog.Config{
		MaxResumePendingSeconds:        off,
		MaxResumingSeconds:             off,
		MaxAwaitingClientActionSeconds: off,
		MaxSessionAgeSeconds:           off,
		MaxIdleSeconds:                 off,
		MaxSuspendedSessionSeconds:     off,
		MaxSuspendedPodHoldSeconds:     off,
	}
}

// spec: §7.2 (Interactive Session Model) — the DLQ expiry sweep covers the
// recovering states (resume_pending, resuming, awaiting_client_action) and
// skips suspended, so a suspended session's held messages wait for its
// resume; it accumulates the expired count in Result.DLQExpired.
func TestSweepDLQExpiryCoversTheRecoveringStates_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-sessioninbox.DefaultDLQTTL)
	seedRow(t, store, "sess_rp", "acme", session.StateResumePending, old)
	seedRow(t, store, "sess_rs", "acme", session.StateResuming, old)
	seedRow(t, store, "sess_aca", "acme", session.StateAwaitingClientAction, old)
	seedRow(t, store, "sess_run", "acme", session.StateRunning, old)
	seedRow(t, store, "sess_susp", "acme", session.StateSuspended, old)

	sweeper := &fakeDLQSweeper{count: map[string]int{"sess_rp": 2, "sess_rs": 1, "sess_aca": 3, "sess_susp": 7}}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, dlqCfg(), nil).
		WithMessaging(sweeper)

	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if res.DLQExpired != 6 {
		t.Errorf("DLQExpired = %d, want 6 (2+1+3)", res.DLQExpired)
	}
	got := map[string]bool{}
	for _, id := range sweeper.swept {
		got[id] = true
	}
	for _, id := range []string{"sess_rp", "sess_rs", "sess_aca"} {
		if !got[id] {
			t.Errorf("%s was not swept: %v", id, sweeper.swept)
		}
	}
	if got["sess_run"] || got["sess_susp"] {
		t.Errorf("a running or suspended session was swept: %v", sweeper.swept)
	}
}

// spec: §7.2 (Interactive Session Model) — an entry expires at the later of
// its score and the row's entry to its state plus the DLQ TTL, so a
// resume_pending row whose StateEnteredAt is inside the TTL is not swept.
//
// Discriminates: an entry expired by its score while its row's StateEnteredAt
// is inside the TTL.
func TestSweepDLQExpiryWaitsForTheStateEntryTTL_spec_7_2(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	seedRow(t, store, "sess_recent", "acme", session.StateResumePending, now.Add(-sessioninbox.DefaultDLQTTL+time.Second))
	seedRow(t, store, "sess_due", "acme", session.StateResumePending, now.Add(-sessioninbox.DefaultDLQTTL))
	sweeper := &fakeDLQSweeper{count: map[string]int{"sess_recent": 1, "sess_due": 1}}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, dlqCfg(), nil).WithMessaging(sweeper)
	res, err := w.Tick(context.Background(), now)
	if err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(sweeper.swept) != 1 || sweeper.swept[0] != "sess_due" || res.DLQExpired != 1 {
		t.Fatalf("swept = %v, DLQExpired = %d; want only sess_due", sweeper.swept, res.DLQExpired)
	}
}

// A per-session sweep error must not stall the tick or the other sessions'
// sweeps: the erroring session is skipped and the rest still sweep.
func TestSweepDLQExpiry_PerSessionErrorIsolated_spec_7_2_341(t *testing.T) {
	store := memstore.New()
	now := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	old := now.Add(-sessioninbox.DefaultDLQTTL)
	seedRow(t, store, "sess_a", "acme", session.StateResumePending, old)
	seedRow(t, store, "sess_b", "acme", session.StateResumePending, old)

	sweeper := &fakeDLQSweeper{
		count: map[string]int{"sess_b": 4},
		errOn: map[string]bool{"sess_a": true},
	}
	w := watchdog.New(store, watchdog.StaticTenants{"acme"}, dlqCfg(), nil).
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
