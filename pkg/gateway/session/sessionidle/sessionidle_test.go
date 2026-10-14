// SPDX-License-Identifier: MIT

package sessionidle_test

import (
	"context"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionidle"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore/memstore"
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

// spec: 6.2 (maxClientIdleSeconds clock behavior across states) — the age
// cap is not an idle default: with no sessionPolicy.maxClientIdleSeconds the
// resolver returns 0 and the watchdog's 900s default applies.
//
// diagnosis: a failure means the idle bound still falls back to the age cap,
// so an idle session holds its pod for up to maxSessionAge instead of 900s.
func TestResolverIgnoresTheAgeCap_spec_6_2(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 0, 3600), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{RuntimeRef: "rt"})
	if got != 0 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 0 (the age cap is not an idle default)", got)
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

// spec: 27.6 (Session lifecycle and cleanup) — a playground idle override
// under the playground cap applies on its own and ignores the runtime's
// session-age cap.
func TestResolverPlaygroundOverrideIgnoresTheAgeCap_spec_27_6(t *testing.T) {
	r := sessionidle.NewResolver(runtimeStore(t, "rt", 0, 3600), nil)
	got := r.EffectiveMaxIdleSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxIdleSeconds: 300},
	})
	if got != 300 {
		t.Errorf("EffectiveMaxIdleSeconds = %d, want 300 (the playground override alone)", got)
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

// spec: 6.2 (maxClientIdleSeconds clock behavior across states), 5.1
// (Runtime), 5.2 (Pool Configuration and Execution Modes) — a service-mode
// session and a type: mcp runtime's session cannot be idle-suspended; an
// agent runtime's session can; a resolution error is returned.
func TestIdleSuspendableExcludesServiceModeAndMCP_spec_6_2(t *testing.T) {
	ctx := context.Background()
	rs := runtimestore.NewMemory()
	for _, rt := range []runtimestore.Runtime{
		{Name: "agent", Type: runtimestore.TypeAgent},
		{Name: "tools", Type: runtimestore.TypeMCP},
	} {
		if err := rs.Create(ctx, rt); err != nil {
			t.Fatalf("create runtime: %v", err)
		}
	}
	r := sessionidle.NewResolver(rs, nil)
	cases := []struct {
		name    string
		sess    sessionstore.Session
		want    bool
		wantErr bool
	}{
		{"agent runtime", sessionstore.Session{ID: "a", RuntimeRef: "agent"}, true, false},
		{"mcp runtime", sessionstore.Session{ID: "m", RuntimeRef: "tools"}, false, false},
		{"service mode", sessionstore.Session{ID: "s", RuntimeRef: "agent", ExecutionMode: string(runtimestore.ExecutionModeService)}, false, false},
		{"unknown runtime", sessionstore.Session{ID: "u", RuntimeRef: "missing"}, false, true},
		{"no runtime reference", sessionstore.Session{ID: "n"}, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := r.IdleSuspendable(ctx, c.sess)
			if (err != nil) != c.wantErr || got != c.want {
				t.Fatalf("IdleSuspendable = %v, %v; want %v (error %v)", got, err, c.want, c.wantErr)
			}
		})
	}
}

// spec: 6.2 (maxClientIdleSeconds clock behavior across states) — StampAt
// persists the given instant without coalescing and only advances the stored
// time.
func TestStampAtPersistsWithoutCoalescingAndOnlyAdvances_spec_6_2(t *testing.T) {
	ctx := context.Background()
	store := memstore.New()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := store.Create(ctx, sessionstore.Session{
		ID: "sess", TenantID: "acme", State: session.StateRunning, CreatedAt: t0, UpdatedAt: t0,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	st := sessionidle.NewStamper(store, func() time.Time { return t0 })
	st.Stamp("acme", "sess")
	read := func() time.Time {
		deadline := time.Now().Add(2 * time.Second)
		for {
			row, _ := store.Get(ctx, "acme", "sess")
			if !row.LastAgentActivityAt.IsZero() || time.Now().After(deadline) {
				return row.LastAgentActivityAt
			}
			time.Sleep(time.Millisecond)
		}
	}
	if got := read(); !got.Equal(t0) {
		t.Fatalf("after Stamp: %v, want %v", got, t0)
	}
	later := t0.Add(400 * time.Millisecond)
	st.StampAt("acme", "sess", later)
	if row, _ := store.Get(ctx, "acme", "sess"); !row.LastAgentActivityAt.Equal(later) {
		t.Fatalf("StampAt inside the coalescing window wrote %v, want %v", row.LastAgentActivityAt, later)
	}
	st.StampAt("acme", "sess", t0.Add(-time.Hour))
	if row, _ := store.Get(ctx, "acme", "sess"); !row.LastAgentActivityAt.Equal(later) {
		t.Fatalf("StampAt moved the anchor backwards to %v", row.LastAgentActivityAt)
	}
	var nilStamper *sessionidle.Stamper
	nilStamper.StampAt("acme", "sess", later)
}
