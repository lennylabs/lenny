// SPDX-License-Identifier: MIT

package sessionage_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionage"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

func newPool(t *testing.T, s poolstore.Store, name string, maxAgeSeconds int) {
	t.Helper()
	if err := s.Create(context.Background(), poolstore.Pool{
		Name:                 name,
		RuntimeRef:           "rt",
		IsolationProfile:     isolation.ProfileSandboxed,
		ExecutionMode:        runtimestore.ExecutionModeSession,
		ResourceClass:        "small",
		WarmCount:            1,
		MaxSessionAgeSeconds: maxAgeSeconds,
	}); err != nil {
		t.Fatalf("create pool %s: %v", name, err)
	}
}

// spec: §5.1 limits.maxSessionAgeSeconds — the per-runtime cap is returned
// when only the runtime declares one. F-11.3.3.
func TestRuntimeLimitOnly_spec_5_1(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, Image: "lenny/rt@sha256:abc",
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 1800},
	}); err != nil {
		t.Fatal(err)
	}
	r := sessionage.New(rts, poolstore.NewMemory())
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(),
		sessionstore.Session{RuntimeRef: "rt"})
	if got != 1800 {
		t.Errorf("got %d, want 1800 (runtime limit)", got)
	}
}

// spec: §5.2 pool maxSessionAgeSeconds — the per-pool cap is returned when
// only the pool declares one. F-11.3.3.
func TestPoolLimitOnly_spec_5_2(t *testing.T) {
	pools := poolstore.NewMemory()
	newPool(t, pools, "p", 2400)
	r := sessionage.New(runtimestore.NewMemory(), pools)
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(),
		sessionstore.Session{PoolRef: "p"})
	if got != 2400 {
		t.Errorf("got %d, want 2400 (pool limit)", got)
	}
}

// spec: §11.3 line 198 — when both the runtime and the pool declare a cap,
// the most-restrictive (smaller positive) value wins. F-11.3.3.
func TestRuntimeAndPoolMostRestrictiveWins_spec_11_3_198(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, Image: "lenny/rt@sha256:abc",
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 3600},
	}); err != nil {
		t.Fatal(err)
	}
	pools := poolstore.NewMemory()
	newPool(t, pools, "p", 900)
	r := sessionage.New(rts, pools)
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(),
		sessionstore.Session{RuntimeRef: "rt", PoolRef: "p"})
	if got != 900 {
		t.Errorf("got %d, want 900 (pool tighter than runtime)", got)
	}
}

// spec: §5.1 — a derived runtime's merged Override limits block resolves
// through the derived-runtime merge. F-11.3.3.
func TestDerivedRuntimeLimitResolves_spec_5_1(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "base", Type: runtimestore.TypeAgent, Image: "lenny/base@sha256:abc",
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 1200},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "derived", BaseRuntime: "base",
	}); err != nil {
		t.Fatal(err)
	}
	r := sessionage.New(rts, poolstore.NewMemory())
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(),
		sessionstore.Session{RuntimeRef: "derived"})
	if got != 1200 {
		t.Errorf("got %d, want 1200 (inherited from base via merge)", got)
	}
}

// spec: §11.3 line 198 — neither surface declares a cap, a missing
// runtime/pool reference, and empty refs all yield 0 so the watchdog falls
// back to the platform default. F-11.3.3.
func TestNoCapAndMissingRefsYieldZero_spec_11_3_198(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, Image: "lenny/rt@sha256:abc",
	}); err != nil {
		t.Fatal(err)
	}
	r := sessionage.New(rts, poolstore.NewMemory())
	for _, tc := range []struct {
		name string
		sess sessionstore.Session
	}{
		{"runtime exists no limit", sessionstore.Session{RuntimeRef: "rt"}},
		{"missing runtime", sessionstore.Session{RuntimeRef: "ghost"}},
		{"missing pool", sessionstore.Session{PoolRef: "ghost"}},
		{"empty refs", sessionstore.Session{}},
	} {
		if got := r.EffectiveMaxSessionAgeSeconds(context.Background(), tc.sess); got != 0 {
			t.Errorf("%s: got %d, want 0", tc.name, got)
		}
	}
}

// spec: §14 line 154 / §27.6 line 200 — a per-session timeout override (also
// the carrier for the playground duration cap) tightens the runtime/pool cap,
// and an unset (zero) override leaves the resolved cap unchanged. F-27.6.2.
func TestPerSessionTimeoutTightensCap_spec_27_6(t *testing.T) {
	rts := runtimestore.NewMemory()
	if err := rts.Create(context.Background(), runtimestore.Runtime{
		Name: "rt", Type: runtimestore.TypeAgent, Image: "lenny/rt@sha256:abc",
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 3600},
	}); err != nil {
		t.Fatal(err)
	}
	r := sessionage.New(rts, poolstore.NewMemory())

	// A playground-stamped 1800s session timeout tightens the 3600s runtime cap.
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 1800},
	})
	if got != 1800 {
		t.Errorf("with timeout override: got %d, want 1800 (session timeout tighter)", got)
	}

	// A zero override never loosens or tightens the runtime cap.
	got = r.EffectiveMaxSessionAgeSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 0, MaxIdleSeconds: 120},
	})
	if got != 3600 {
		t.Errorf("with zero override: got %d, want 3600 (runtime cap unchanged)", got)
	}

	// A timeout looser than the runtime cap cannot loosen it (most-restrictive wins).
	got = r.EffectiveMaxSessionAgeSeconds(context.Background(), sessionstore.Session{
		RuntimeRef: "rt",
		Timeouts:   &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 99999},
	})
	if got != 3600 {
		t.Errorf("with looser override: got %d, want 3600 (runtime cap still binds)", got)
	}
}

// A resolver constructed with nil stores never panics and returns 0.
func TestNilStoresYieldZero(t *testing.T) {
	r := sessionage.New(nil, nil)
	got := r.EffectiveMaxSessionAgeSeconds(context.Background(),
		sessionstore.Session{RuntimeRef: "rt", PoolRef: "p"})
	if got != 0 {
		t.Errorf("nil stores: got %d, want 0", got)
	}
}
