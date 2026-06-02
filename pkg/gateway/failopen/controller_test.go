// SPDX-License-Identifier: MIT

package failopen

import (
	"context"
	"sync"
	"testing"
	"time"
)

type recordingEmitter struct {
	mu    sync.Mutex
	calls []string
}

func (e *recordingEmitter) EmitQuotaFailOpenStarted(_ context.Context, instance string, _ time.Time) {
	e.mu.Lock()
	e.calls = append(e.calls, instance)
	e.mu.Unlock()
}

func (e *recordingEmitter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.calls)
}

func newTestController(clk *fakeClock, emit AuditEmitter, fraction float64, hardCap int64) *Controller {
	return NewController(ControllerConfig{
		Timer:             NewCumulativeTimer(CumulativeConfig{MaxSeconds: 300 * time.Second, StatePath: "-", Now: clk.now}),
		Backstop:          NewBackstop(clk.now),
		Replicas:          NewReplicaCount(),
		UserFraction:      fraction,
		PerReplicaHardCap: hardCap,
		Audit:             emit,
		InstanceID:        "gateway-0",
		Now:               clk.now,
	})
}

// spec: §12.4 line 224 — the quota_failopen_started audit event fires
// exactly once per fail-open episode (on the leading edge).
func TestControllerEmitsAuditOncePerEpisode_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	emit := &recordingEmitter{}
	c := newTestController(clk, emit, 0.25, 0)

	c.Enter()
	c.Enter() // second call inside the same episode: no new event
	// The emit is async; give the goroutine a moment.
	waitFor(t, func() bool { return emit.count() == 1 })
	if got := emit.count(); got != 1 {
		t.Fatalf("audit emit count after one episode = %d, want 1", got)
	}

	clk.advance(10 * time.Second)
	c.Exit()
	clk.advance(10 * time.Second)
	c.Enter() // a fresh episode emits again
	waitFor(t, func() bool { return emit.count() == 2 })
	if got := emit.count(); got != 2 {
		t.Fatalf("audit emit count after a second episode = %d, want 2", got)
	}
}

// spec: §12.4 line 224 — once cumulative fail-open time exceeds the
// maximum, Evaluate fails closed for quota.
func TestControllerEvaluateCumulativeExceeded_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	c := newTestController(clk, nil, 0.25, 0)
	c.Enter()
	clk.advance(301 * time.Second)
	dec := c.Evaluate(FailOpenRequest{TenantKey: "acme", UserKey: "acme:alice", TenantLimit: 1000}, clk.now())
	if dec.Admit {
		t.Fatal("a cumulative-exceeded replica must fail closed")
	}
	if dec.Reason != ReasonCumulativeExceeded {
		t.Fatalf("reason = %q, want %q", dec.Reason, ReasonCumulativeExceeded)
	}
}

// spec: §12.4 line 222 — the per-user ceiling binds before the per-tenant
// ceiling, so a single user cannot monopolize the tenant allocation.
func TestControllerEvaluateUserCeiling_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	c := newTestController(clk, nil, 0.25, 0)
	c.Enter()
	// tenant_limit=1000, replicas=1 → effective tenant ceiling = min(1000, 500)=500.
	// per-user ceiling = 500 * 0.25 = 125.
	req := FailOpenRequest{TenantKey: "acme", UserKey: "acme:alice", TenantLimit: 1000}
	for i := 0; i < 125; i++ {
		if dec := c.Evaluate(req, clk.now()); !dec.Admit {
			t.Fatalf("request %d rejected early: %+v", i, dec)
		}
	}
	dec := c.Evaluate(req, clk.now()) // the 126th
	if dec.Admit {
		t.Fatal("the request past the per-user ceiling must be rejected")
	}
	if dec.Reason != ReasonUserCeiling {
		t.Fatalf("reason = %q, want %q", dec.Reason, ReasonUserCeiling)
	}
	if dec.Ceiling != 125 {
		t.Fatalf("ceiling = %d, want 125", dec.Ceiling)
	}
}

// spec: §12.4 line 224 — the per-tenant effective ceiling binds across all
// of a tenant's users even when each user stays under the per-user cap.
func TestControllerEvaluateTenantCeiling_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	// userFraction = 1.0 so the per-user ceiling equals the tenant ceiling
	// and never binds first; the tenant ceiling (500) is the only brake.
	c := newTestController(clk, nil, 1.0, 0)
	c.Enter()
	req := func(user string) FailOpenRequest {
		return FailOpenRequest{TenantKey: "acme", UserKey: "acme:" + user, TenantLimit: 1000}
	}
	// Spread 500 requests across two users (250 each) — under each user's
	// ceiling (500) but exactly at the tenant ceiling.
	for i := 0; i < 250; i++ {
		c.Evaluate(req("alice"), clk.now())
		c.Evaluate(req("bob"), clk.now())
	}
	dec := c.Evaluate(req("carol"), clk.now())
	if dec.Admit {
		t.Fatal("the request past the per-tenant effective ceiling must be rejected")
	}
	if dec.Reason != ReasonTenantCeiling {
		t.Fatalf("reason = %q, want %q", dec.Reason, ReasonTenantCeiling)
	}
}

// A request with no configured tenant limit is admitted (only the
// cumulative timer can block it).
func TestControllerEvaluateNoTenantLimitAdmits(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	c := newTestController(clk, nil, 0.25, 0)
	c.Enter()
	dec := c.Evaluate(FailOpenRequest{TenantKey: "acme", UserKey: "acme:alice", TenantLimit: 0}, clk.now())
	if !dec.Admit {
		t.Fatalf("unbounded tenant should admit during fail-open, got %+v", dec)
	}
}

// spec: §12.4 line 222 — Exit resets the per-user backstop counters.
func TestControllerExitResetsBackstop_spec_12_4(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC)}
	c := newTestController(clk, nil, 0.25, 0)
	c.Enter()
	req := FailOpenRequest{TenantKey: "acme", UserKey: "acme:alice", TenantLimit: 1000}
	for i := 0; i < 125; i++ {
		c.Evaluate(req, clk.now())
	}
	c.Exit() // recovery edge resets counters
	c.Enter()
	if dec := c.Evaluate(req, clk.now()); !dec.Admit {
		t.Fatalf("after Exit reset the first request should admit, got %+v", dec)
	}
}

// A nil controller is safe to call (the middleware holds an optional one).
func TestControllerNilSafe(t *testing.T) {
	var c *Controller
	c.Enter()
	c.Exit()
	if c.CumulativeExceeded() {
		t.Fatal("nil controller must report not-exceeded")
	}
	c.Sweep(time.Now())
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
