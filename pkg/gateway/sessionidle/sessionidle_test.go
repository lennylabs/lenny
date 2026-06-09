// SPDX-License-Identifier: MIT

package sessionidle_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/inputwait"
	"github.com/lennylabs/lenny/pkg/gateway/interactionstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

func runtimeStoreWithIdle(t *testing.T, ref string, idleSeconds int) runtimestore.Store {
	t.Helper()
	rs := runtimestore.NewMemory()
	if err := rs.Create(context.Background(), runtimestore.Runtime{
		Name:   ref,
		Type:   runtimestore.TypeAgent,
		Limits: &runtimestore.Limits{MaxIdleTimeSeconds: idleSeconds},
	}); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	return rs
}

// spec: §11.3 line 199 — the per-runtime limits.maxIdleTimeSeconds is the
// effective cap when no per-session override is set. F-11.3.7.
func TestResolverReturnsRuntimeLimit_spec_11_3_199(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStoreWithIdle(t, "rt", 300))
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300", got)
	}
}

// spec: §27.6 line 201 — the playground idle override (landed on
// Timeouts.MaxIdleSeconds) tightens the runtime cap min-wins. F-11.3.7 / F-9.2.15.
func TestResolverPlaygroundOverrideTightensRuntime_spec_27_6_201(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStoreWithIdle(t, "rt", 600))
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300 (playground cap wins)", got)
	}
}

// A runtime idle cap tighter than the per-session override still wins
// (min-wins on both axes). F-11.3.7.
func TestResolverRuntimeTighterThanOverride(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStoreWithIdle(t, "rt", 120))
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 120 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 120 (runtime cap wins)", got)
	}
}

// No runtime limit and no override → 0, so the watchdog applies the
// platform default. F-11.3.7.
func TestResolverNoCapReturnsZero(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStoreWithIdle(t, "rt", 0))
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"})
	if got != 0 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 0", got)
	}
	// A nil runtime store also resolves to 0.
	if got := sessionidle.NewResolver(nil).EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"}); got != 0 {
		t.Errorf("nil-store EffectiveMaxIdleSeconds = %d, want 0", got)
	}
}

// spec: §9.2 line 102 — a pending elicitation pauses the idle timer. F-9.2.15.
func TestPauseCheckerPausesOnPendingElicitation_spec_9_2_102(t *testing.T) {
	interactions := interactionstore.NewMemory()
	_ = interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "el1", TenantID: "acme", SessionID: "sess", UserID: "alice",
		Kind: interactionstore.KindElicitation, Phase: interactionstore.PhasePending,
	})
	pc := sessionidle.NewPauseChecker(interactions, inputwait.NewRegistry())
	if !pc.IdlePaused(context.Background(), sessionstore.Session{TenantID: "acme", ID: "sess"}) {
		t.Error("a session with a pending elicitation must be paused")
	}
	// A different session has no pending interaction → not paused.
	if pc.IdlePaused(context.Background(), sessionstore.Session{TenantID: "acme", ID: "other"}) {
		t.Error("a session with no pending interaction must not be paused")
	}
}

// A pending tool-use interaction (not an elicitation) does not pause the
// idle timer — only the §9.2 elicitation and the §6.2 request_input pause.
func TestPauseCheckerToolUseDoesNotPause(t *testing.T) {
	interactions := interactionstore.NewMemory()
	_ = interactions.Put(context.Background(), interactionstore.Interaction{
		ID: "tu1", TenantID: "acme", SessionID: "sess", UserID: "alice",
		Kind: interactionstore.KindToolUse, Phase: interactionstore.PhasePending,
	})
	pc := sessionidle.NewPauseChecker(interactions, inputwait.NewRegistry())
	if pc.IdlePaused(context.Background(), sessionstore.Session{TenantID: "acme", ID: "sess"}) {
		t.Error("a pending tool-use approval must not pause the idle timer")
	}
}

// spec: §6.2 `input_required` row — an outstanding lenny/request_input
// pauses the idle timer. F-9.2.15.
func TestPauseCheckerPausesOnPendingInputWait_spec_6_2(t *testing.T) {
	reg := inputwait.NewRegistry()
	if _, err := reg.Register("sess", "req1", nil); err != nil {
		t.Fatalf("register: %v", err)
	}
	pc := sessionidle.NewPauseChecker(interactionstore.NewMemory(), reg)
	if !pc.IdlePaused(context.Background(), sessionstore.Session{TenantID: "acme", ID: "sess"}) {
		t.Error("a session blocked on request_input must be paused")
	}
}

// spec: §6.2 line 277 — the stamper advances last_agent_activity_at and
// coalesces writes to ≤1 per interval per session. F-11.3.7.
func TestStamperAdvancesAndCoalesces_spec_6_2_277(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := t0
	st := sessionidle.NewStamper(store, func() time.Time { return now })

	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning,
		CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	// First stamp persists.
	st.Stamp("acme", "sess")
	waitForActivity(t, store, "acme", "sess", t0)

	// A second stamp within the 1s window is coalesced (no new write); the
	// stored anchor stays at t0.
	now = t0.Add(500 * time.Millisecond)
	st.Stamp("acme", "sess")
	row, _ := store.Get(context.Background(), "acme", "sess")
	if !row.LastAgentActivityAt.Equal(t0) {
		t.Errorf("coalesced stamp must not advance the anchor: got %v, want %v", row.LastAgentActivityAt, t0)
	}

	// Past the window, a stamp advances the anchor.
	now = t0.Add(2 * time.Second)
	st.Stamp("acme", "sess")
	waitForActivity(t, store, "acme", "sess", now)
}

// A stamp for a terminal session is a no-op (the guard in persist). F-11.3.7.
func TestStamperSkipsTerminalSession(t *testing.T) {
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := sessionidle.NewStamper(store, func() time.Time { return t0 })
	if err := store.Create(context.Background(), sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateCompleted,
		CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	st.Stamp("acme", "sess")
	// Give the background persist goroutine a moment; the anchor stays zero.
	time.Sleep(50 * time.Millisecond)
	row, _ := store.Get(context.Background(), "acme", "sess")
	if !row.LastAgentActivityAt.IsZero() {
		t.Errorf("terminal session must not be stamped: got %v", row.LastAgentActivityAt)
	}
}

// A nil *Stamper is a safe no-op so callers can wire it unconditionally.
func TestNilStamperIsNoOp(t *testing.T) {
	var st *sessionidle.Stamper
	st.Stamp("acme", "sess") // must not panic
	st.Forget("sess")
}

// waitForActivity polls until the session's LastAgentActivityAt reaches at
// least want, accommodating the stamper's background persist goroutine.
func waitForActivity(t *testing.T, store sessionstore.Store, tenant, id string, want time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		row, err := store.Get(context.Background(), tenant, id)
		if err == nil && !row.LastAgentActivityAt.Before(want) && !row.LastAgentActivityAt.IsZero() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	row, _ := store.Get(context.Background(), tenant, id)
	t.Fatalf("LastAgentActivityAt did not reach %v: got %v", want, row.LastAgentActivityAt)
}
