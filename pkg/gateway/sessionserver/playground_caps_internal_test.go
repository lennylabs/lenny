// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"testing"

	authmw "github.com/lennylabs/lenny/pkg/gateway/middleware/auth"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// fakePlaygroundCaps mirrors playground.Config's §27.6 effective-cap math
// without importing the playground package into the session server's tests.
type fakePlaygroundCaps struct {
	idleSeconds int    // §27.2 playground.maxIdleTimeSeconds
	sessionMins int    // §27.2 playground.maxSessionMinutes
	hidden      string // §27.2 playground.allowedRuntimes-excluded runtime; empty = all visible
}

func (f fakePlaygroundCaps) RuntimeVisible(name string) bool {
	return f.hidden == "" || name != f.hidden
}

func (f fakePlaygroundCaps) EffectiveIdleSeconds(maxClientIdleSeconds int) int {
	if maxClientIdleSeconds > 0 && maxClientIdleSeconds < f.idleSeconds {
		return maxClientIdleSeconds
	}
	return f.idleSeconds
}

func (f fakePlaygroundCaps) EffectiveSessionMinutes(runtimeMinutes int) int {
	if runtimeMinutes > 0 && runtimeMinutes < f.sessionMins {
		return runtimeMinutes
	}
	return f.sessionMins
}

func playgroundCtx() context.Context {
	return authmw.WithPrincipal(context.Background(),
		authmw.Principal{Origin: originPlayground, TenantID: "acme", Subject: "alice@acme.com"})
}

// TestApplyPlaygroundCaps_NonPlaygroundNoOp_spec_27_6 — a caller whose bearer
// carries no origin=playground claim is left untouched: no label, no caps, no
// counter.
func TestApplyPlaygroundCaps_NonPlaygroundNoOp_spec_27_6(t *testing.T) {
	count := 0
	s := &Server{
		playgroundCaps:              fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30},
		incPlaygroundSessionCreated: func(string) { count++ },
	}

	// no principal at all
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(context.Background(), "rt", &row)
	if row.Origin != "" || row.Timeouts != nil || count != 0 {
		t.Fatalf("no-principal: origin=%q timeouts=%v count=%d, want all zero", row.Origin, row.Timeouts, count)
	}

	// a principal whose origin is not playground
	ctx := authmw.WithPrincipal(context.Background(), authmw.Principal{TenantID: "acme", Origin: ""})
	row = sessionstore.Session{}
	s.applyPlaygroundCaps(ctx, "rt", &row)
	if row.Origin != "" || row.Timeouts != nil || count != 0 {
		t.Fatalf("non-playground: origin=%q timeouts=%v count=%d, want all zero", row.Origin, row.Timeouts, count)
	}
}

// TestApplyPlaygroundCaps_LabelAndCounterWithoutResolver_spec_27_6 — even with
// no cap resolver wired, the origin=playground label (§27.6 line 203) is
// stamped and the §27.8 counter fires.
func TestApplyPlaygroundCaps_LabelAndCounterWithoutResolver_spec_27_6(t *testing.T) {
	count := 0
	s := &Server{incPlaygroundSessionCreated: func(rt string) {
		count++
		if rt != "claude-code" {
			t.Errorf("counter runtime label = %q, want claude-code", rt)
		}
	}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "claude-code", &row)
	if row.Origin != originPlayground {
		t.Errorf("origin = %q, want %q", row.Origin, originPlayground)
	}
	if row.Timeouts != nil {
		t.Errorf("timeouts = %v, want nil (no resolver wired)", row.Timeouts)
	}
	if count != 1 {
		t.Errorf("counter fired %d times, want 1", count)
	}
}

// TestApplyPlaygroundCaps_StampsCapsNoRuntimeLimit_spec_27_6 — with no runtime
// cap declared, the §27.6 playground idle (line 201) and duration (line 200)
// caps are stamped onto the row unchanged.
func TestApplyPlaygroundCaps_StampsCapsNoRuntimeLimit_spec_27_6(t *testing.T) {
	s := &Server{playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "rt", &row)
	if row.Timeouts == nil {
		t.Fatal("timeouts not stamped")
	}
	if row.Timeouts.MaxSessionAgeSeconds != 1800 {
		t.Errorf("MaxSessionAgeSeconds = %d, want 1800 (30 min)", row.Timeouts.MaxSessionAgeSeconds)
	}
	if row.Timeouts.MaxIdleSeconds != 300 {
		t.Errorf("MaxIdleSeconds = %d, want 300", row.Timeouts.MaxIdleSeconds)
	}
}

// TestApplyPlaygroundCaps_RuntimeDurationTighter_spec_27_6 — when the runtime's
// own limits.maxSessionAge is tighter than the playground cap, the §27.6 line
// 200 min() keeps the runtime bound.
func TestApplyPlaygroundCaps_RuntimeDurationTighter_spec_27_6(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:   "short",
		Type:   runtimestore.TypeAgent,
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 600}, // 10 min
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	s := &Server{runtimes: runtimes, playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "short", &row)
	if row.Timeouts.MaxSessionAgeSeconds != 600 {
		t.Errorf("MaxSessionAgeSeconds = %d, want 600 (runtime tighter than playground)", row.Timeouts.MaxSessionAgeSeconds)
	}
}

// TestApplyPlaygroundCaps_SubMinuteRuntimeNotLoosened_spec_27_6 — a runtime cap
// below 60s rounds to zero whole minutes; the seconds re-clamp keeps the row
// at the exact runtime bound rather than loosening it to the playground
// default.
func TestApplyPlaygroundCaps_SubMinuteRuntimeNotLoosened_spec_27_6(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:   "tiny",
		Type:   runtimestore.TypeAgent,
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 30},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	s := &Server{runtimes: runtimes, playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "tiny", &row)
	if row.Timeouts.MaxSessionAgeSeconds != 30 {
		t.Errorf("MaxSessionAgeSeconds = %d, want 30 (sub-minute runtime cap preserved)", row.Timeouts.MaxSessionAgeSeconds)
	}
}

// TestApplyPlaygroundCaps_IdleResolvesAgainstClientIdleBound_spec_27_6 — the
// playground idle override resolves against the runtime's effective
// sessionPolicy.maxClientIdleSeconds, not the removed limits.maxIdleTimeSeconds
// knob. A runtime declaring a maxClientIdleSeconds tighter than the playground
// cap keeps the runtime bound: min(maxClientIdleSeconds,
// playground.maxIdleTimeSeconds). spec: §6.84, §3.1 (maxClientIdleSeconds
// supersedes maxIdleTimeSeconds).
func TestApplyPlaygroundCaps_IdleResolvesAgainstClientIdleBound_spec_27_6(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:          "tight-idle",
		Type:          runtimestore.TypeAgent,
		SessionPolicy: &runtimestore.SessionPolicy{MaxClientIdleSeconds: 120},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	s := &Server{runtimes: runtimes, playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "tight-idle", &row)
	if row.Timeouts == nil {
		t.Fatal("timeouts not stamped")
	}
	if row.Timeouts.MaxIdleSeconds != 120 {
		t.Errorf("MaxIdleSeconds = %d, want 120 (runtime maxClientIdleSeconds tighter than playground)", row.Timeouts.MaxIdleSeconds)
	}
}

// TestApplyPlaygroundCaps_IdleDefaultsToSessionAge_spec_27_6 — when the runtime
// declares no sessionPolicy.maxClientIdleSeconds, the idle override resolves
// against the runtime's effective maxSessionAgeSeconds (the §6.2 idle-clock
// default). A maxSessionAgeSeconds tighter than the playground cap keeps the
// session-age bound. spec: §6.84, §3.1 (idle defaults to effective
// maxSessionAgeSeconds).
func TestApplyPlaygroundCaps_IdleDefaultsToSessionAge_spec_27_6(t *testing.T) {
	runtimes := runtimestore.NewMemory()
	if err := runtimes.Create(context.Background(), runtimestore.Runtime{
		Name:   "age-only",
		Type:   runtimestore.TypeAgent,
		Limits: &runtimestore.Limits{MaxSessionAgeSeconds: 200},
	}); err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	s := &Server{runtimes: runtimes, playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{}
	s.applyPlaygroundCaps(playgroundCtx(), "age-only", &row)
	if row.Timeouts == nil {
		t.Fatal("timeouts not stamped")
	}
	if row.Timeouts.MaxIdleSeconds != 200 {
		t.Errorf("MaxIdleSeconds = %d, want 200 (idle defaults to effective maxSessionAgeSeconds)", row.Timeouts.MaxIdleSeconds)
	}
}

// TestApplyPlaygroundCaps_MinWinsOverClientTimeouts_spec_27_6 — a tighter §14
// client-requested timeout is preserved; the playground cap only tightens, it
// never loosens.
func TestApplyPlaygroundCaps_MinWinsOverClientTimeouts_spec_27_6(t *testing.T) {
	s := &Server{playgroundCaps: fakePlaygroundCaps{idleSeconds: 300, sessionMins: 30}}
	row := sessionstore.Session{
		Timeouts: &sessionstore.SessionTimeouts{MaxSessionAgeSeconds: 900, MaxIdleSeconds: 120},
	}
	s.applyPlaygroundCaps(playgroundCtx(), "rt", &row)
	if row.Timeouts.MaxSessionAgeSeconds != 900 {
		t.Errorf("MaxSessionAgeSeconds = %d, want 900 (tighter client value preserved)", row.Timeouts.MaxSessionAgeSeconds)
	}
	if row.Timeouts.MaxIdleSeconds != 120 {
		t.Errorf("MaxIdleSeconds = %d, want 120 (tighter client value preserved)", row.Timeouts.MaxIdleSeconds)
	}
}

// TestMinPositiveInt64 covers the unset-axis semantics the cap stamp relies on.
func TestMinPositiveInt64(t *testing.T) {
	cases := []struct{ a, b, want int64 }{
		{0, 5, 5},
		{5, 0, 5},
		{0, 0, 0},
		{3, 7, 3},
		{7, 3, 3},
		{-1, 9, 9},
	}
	for _, c := range cases {
		if got := minPositiveInt64(c.a, c.b); got != c.want {
			t.Errorf("minPositiveInt64(%d,%d) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
