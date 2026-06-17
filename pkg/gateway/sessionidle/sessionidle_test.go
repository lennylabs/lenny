// SPDX-License-Identifier: MIT

package sessionidle_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/sessionstore/memstore"
)

// runtimeStore returns a runtime store holding one runtime named ref whose
// sessionPolicy carries maxClientIdleSeconds (0 declares no idle bound) and
// whose limits carry maxSessionAgeSeconds (0 declares no age cap).
func runtimeStore(t *testing.T, ref string, idleSeconds, ageSeconds int) runtimestore.Store {
	t.Helper()
	rs := runtimestore.NewMemory()
	rt := runtimestore.Runtime{
		Name: ref,
		Type: runtimestore.TypeAgent,
	}
	if idleSeconds > 0 {
		rt.SessionPolicy = &runtimestore.SessionPolicy{MaxClientIdleSeconds: idleSeconds}
	}
	if ageSeconds > 0 {
		rt.Limits = &runtimestore.Limits{MaxSessionAgeSeconds: ageSeconds}
	}
	if err := rs.Create(context.Background(), rt); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	return rs
}

// poolStore returns a pool store holding one session-mode pool named ref
// whose sessionPolicy carries maxClientIdleSeconds (0 declares none).
func poolStore(t *testing.T, ref string, idleSeconds int) poolstore.Store {
	t.Helper()
	ps := poolstore.NewMemory()
	p := poolstore.Pool{Name: ref, RuntimeRef: ref}
	if idleSeconds > 0 {
		p.SessionPolicy = &runtimestore.SessionPolicy{MaxClientIdleSeconds: idleSeconds}
	}
	if err := ps.Create(context.Background(), p); err != nil {
		t.Fatalf("create pool: %v", err)
	}
	return ps
}

// spec: 11.3 line 199 (max client idle row), 6.2 (maxClientIdleSeconds
// clock) — the runtime sessionPolicy.maxClientIdleSeconds is the effective
// cap when no per-session override is set. F-11.3.7.
func TestResolverReturnsRuntimePolicy_spec_11_3_199(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 300, 0), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300", got)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock default) — when no sessionPolicy
// declares maxClientIdleSeconds, the bound defaults to the pool's effective
// maxSessionAgeSeconds rather than a fixed 600s. F-11.3.7.
func TestResolverDefaultsToEffectiveMaxSessionAge_spec_6_2(t *testing.T) {
	// Runtime declares only maxSessionAgeSeconds (no idle bound); the idle
	// default takes that age cap.
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 0, 3600), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"})
	if got != 3600 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 3600 (effective maxSessionAge default)", got)
	}
}

// spec: 6.2 (maxClientIdleSeconds clock) — the pool's
// sessionPolicy.maxClientIdleSeconds participates in the most-restrictive
// resolution alongside the runtime's. F-11.3.7.
func TestResolverPoolPolicyTightensRuntime_spec_6_2(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 600, 0), poolStore(t, "rt", 120))
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		PoolRef:    "rt",
	})
	if got != 120 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 120 (pool cap wins)", got)
	}
}

// spec: 27.6 line 201 (playground idle override) — the playground idle
// override (landed on Timeouts.MaxIdleSeconds) tightens the resolved cap
// min-wins. F-11.3.7 / F-9.2.15.
func TestResolverPlaygroundOverrideTightensPolicy_spec_27_6_201(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 600, 0), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300 (playground cap wins)", got)
	}
}

// spec: 27.6 line 201 (playground idle override) — the playground override
// applies against the maxSessionAge default too: a session whose only bound
// is the age-default still gets the tighter playground cap. F-27.6.1.
func TestResolverPlaygroundOverrideTightensAgeDefault_spec_27_6_201(t *testing.T) {
	// No idle bound declared; the default is the 3600s age cap, which the
	// 300s playground override tightens.
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 0, 3600), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300 (playground cap tightens age default)", got)
	}
}

// A policy idle cap tighter than the per-session override still wins
// (min-wins on both axes). F-11.3.7.
func TestResolverPolicyTighterThanOverride(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 120, 0), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 120 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 120 (policy cap wins)", got)
	}
}

// No policy, no age cap, and no override → 0, so the watchdog applies the
// platform default. A nil runtime store also resolves to 0. F-11.3.7.
func TestResolverNoCapReturnsZero(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 0, 0), nil)
	if got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"}); got != 0 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 0", got)
	}
	if got := sessionidle.NewResolver(nil, nil).EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"}); got != 0 {
		t.Errorf("nil-store EffectiveMaxIdleSeconds = %d, want 0", got)
	}
}

// spec: 6.2 (qualifying events; ≤1/s flush) — the stamper advances
// last_agent_activity_at and coalesces writes to ≤1 per interval per
// session. F-11.3.7.
func TestStamperAdvancesAndCoalesces_spec_6_2(t *testing.T) {
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
