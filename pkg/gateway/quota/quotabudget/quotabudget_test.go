// SPDX-License-Identifier: MIT

package quotabudget

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/policy"
	"github.com/lennylabs/lenny/pkg/gateway/quota/quotastore"
	"github.com/lennylabs/lenny/pkg/quota"
)

// fakeLimits is a policy.TenantLimitLookup returning a configured per-tenant
// limit and reset period.
type fakeLimits struct {
	tenantLimit int64
	period      quota.ResetPeriod
	err         error
	unknown     bool
}

func (f fakeLimits) LookupLimits(ctx context.Context, tenantID string) (policy.TenantLimits, error) {
	if f.unknown {
		return policy.TenantLimits{}, policy.ErrTenantNotFound
	}
	if f.err != nil {
		return policy.TenantLimits{}, f.err
	}
	p := f.period
	if p == "" {
		p = quota.ResetHourly
	}
	return policy.TenantLimits{Tenant: f.tenantLimit, Period: p}, nil
}

// fakeAdder is an in-memory CheckpointAdder with atomic add semantics and an
// optional injected failure. It records every call so tests can assert the
// reconcile cadence.
type fakeAdder struct {
	mu     sync.Mutex
	totals map[string]int64
	calls  int
	fail   bool
}

func newFakeAdder() *fakeAdder { return &fakeAdder{totals: map[string]int64{}} }

func (a *fakeAdder) AddTenantTotal(ctx context.Context, tenantID, period, windowLabel string, delta int64) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls++
	if a.fail {
		return 0, errors.New("postgres unreachable")
	}
	key := tenantID + "|" + period + "|" + windowLabel
	a.totals[key] += delta
	return a.totals[key], nil
}

func (a *fakeAdder) total(tenantID, period, windowLabel string) int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.totals[tenantID+"|"+period+"|"+windowLabel]
}

func (a *fakeAdder) callCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.calls
}

func (a *fakeAdder) setFail(v bool) {
	a.mu.Lock()
	a.fail = v
	a.mu.Unlock()
}

// clock is a manually advanced clock.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTracker(t *testing.T, limit int64, replicas int, adder *fakeAdder, clk *clock) *Tracker {
	t.Helper()
	return New(Options{
		Limits:            fakeLimits{tenantLimit: limit},
		Adder:             adder,
		Replicas:          StaticReplicaCount(replicas),
		ReconcileInterval: 30 * time.Second,
		Now:               clk.now,
	})
}

const tenant = "acme"

// spec: §12.4 line 268 — on the first request the replica draws a budget
// slice from Postgres (1/N of the remaining budget) and the admission read
// returns the persisted base.
func TestTracker_ColdStartDrawsSlice_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 4, adder, clk)

	used, err := tr.TenantUsed(context.Background(), tenant, quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed: %v", err)
	}
	if used != 0 {
		t.Fatalf("cold-start used = %d, want 0", used)
	}
	if adder.callCount() != 1 {
		t.Fatalf("cold start should draw exactly one slice from Postgres, got %d calls", adder.callCount())
	}
}

// spec: §12.4 line 268 — the replica decrements the slice locally per
// request without touching Postgres until a reconcile is due.
func TestTracker_AddDecrementsLocally_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	before := adder.callCount()
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 100)
	if adder.callCount() != before {
		t.Fatalf("a sub-threshold Add must not reach Postgres: calls %d → %d", before, adder.callCount())
	}
	used, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed: %v", err)
	}
	if used != 100 {
		t.Fatalf("used = %d, want 100 (base 0 + local 100)", used)
	}
}

// spec: §12.4 line 268 — reconcile when the local slice is 80% consumed,
// folding the delta into Postgres and redrawing the slice.
func TestTracker_ReconcilesAt80Percent_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk) // slice = 1000
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	label := mustLabel(t, quota.ResetHourly, clk.now())

	// 799 < 80% of 1000: no flush yet.
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 799)
	if got := adder.total(tenant, string(quota.ResetHourly), label); got != 0 {
		t.Fatalf("Postgres total after sub-threshold consumption = %d, want 0", got)
	}
	// One more token crosses 80%: reconcile folds 800 into Postgres.
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 1)
	if got := adder.total(tenant, string(quota.ResetHourly), label); got != 800 {
		t.Fatalf("Postgres total after 80%% crossing = %d, want 800", got)
	}
	used, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed: %v", err)
	}
	if used != 800 {
		t.Fatalf("used after reconcile = %d, want 800 (base 800 + local 0)", used)
	}
}

// spec: §12.4 line 268 — reconcile periodically (default every 30s) even
// when the slice is far from the 80% threshold.
func TestTracker_ReconcilesOnInterval_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 100)
	label := mustLabel(t, quota.ResetHourly, clk.now())

	clk.advance(30 * time.Second)
	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("TenantUsed after interval: %v", err)
	}
	if got := adder.total(tenant, string(quota.ResetHourly), label); got != 100 {
		t.Fatalf("Postgres total after interval reconcile = %d, want 100", got)
	}
}

// spec: §12.4 line 268 — multiple replicas each hold 1/N and concurrent
// reconciles serialize through the atomic adder without losing usage.
func TestTracker_MultiReplicaAtomicReconcile_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder() // shared Postgres across replicas
	ctx := context.Background()
	label := mustLabel(t, quota.ResetHourly, clk.now())

	// Two replicas, limit 1000, N=2 → each draws a 500 slice.
	r1 := New(Options{Limits: fakeLimits{tenantLimit: 1000}, Adder: adder, Replicas: StaticReplicaCount(2), Now: clk.now})
	r2 := New(Options{Limits: fakeLimits{tenantLimit: 1000}, Adder: adder, Replicas: StaticReplicaCount(2), Now: clk.now})
	if _, err := r1.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("r1 draw: %v", err)
	}
	if _, err := r2.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("r2 draw: %v", err)
	}
	// Each replica consumes 400 then crosses 80% of its 500 slice.
	r1.Add(ctx, tenant, quota.ResetHourly, clk.now(), 400)
	r2.Add(ctx, tenant, quota.ResetHourly, clk.now(), 400)
	if got := adder.total(tenant, string(quota.ResetHourly), label); got != 800 {
		t.Fatalf("Postgres total after both replicas reconcile = %d, want 800 (no lost update)", got)
	}
}

// spec: §12.4 line 268 — bounded overshoot: a Postgres outage with slice
// headroom keeps serving from the slice; an exhausted slice fails closed.
func TestTracker_FailClosedWhenSliceExhaustedAndPostgresDown_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 100, 1, adder, clk) // slice = 100
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	adder.setFail(true)
	// Consume the whole slice; the 80% reconcile attempt fails silently.
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 100)
	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err == nil {
		t.Fatalf("TenantUsed must fail closed when the slice is exhausted and Postgres is unreachable")
	}
}

func TestTracker_ServesFromSliceHeadroomWhenPostgresDown_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk) // slice = 1000
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 100) // headroom remains
	adder.setFail(true)
	clk.advance(30 * time.Second) // force a reconcile attempt
	used, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed with slice headroom must serve best-effort, got error: %v", err)
	}
	if used != 100 {
		t.Fatalf("used = %d, want 100 (served from local slice)", used)
	}
}

// A cold start with Postgres unreachable cannot draw a slice and must fail
// closed rather than admit an unbounded budget.
func TestTracker_FailClosedOnColdStartPostgresDown(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	adder.setFail(true)
	tr := newTracker(t, 1000, 1, adder, clk)
	if _, err := tr.TenantUsed(context.Background(), tenant, quota.ResetHourly, clk.now()); err == nil {
		t.Fatalf("cold-start TenantUsed with Postgres down must fail closed")
	}
}

// An unknown / soft-deleted tenant surfaces an error so QuotaEvaluator fails
// closed; no Postgres row is created.
func TestTracker_UnknownTenantFailsClosed(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := New(Options{Limits: fakeLimits{unknown: true}, Adder: adder, Replicas: StaticReplicaCount(1), Now: clk.now})
	if _, err := tr.TenantUsed(context.Background(), tenant, quota.ResetHourly, clk.now()); err == nil {
		t.Fatalf("unknown tenant must fail closed")
	}
	if adder.callCount() != 0 {
		t.Fatalf("unknown tenant must not write a checkpoint row, got %d calls", adder.callCount())
	}
}

// spec: §12.4 line 268 — the budget resets at the window boundary.
func TestTracker_WindowRolloverResets_spec_12_4_268(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 30, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 900)
	// Advance into the next hour: the window label changes.
	clk.advance(time.Hour)
	used, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed after rollover: %v", err)
	}
	if used != 0 {
		t.Fatalf("used after window rollover = %d, want 0 (budget reset)", used)
	}
}

// An unlimited tenant (no token budget) is admitted with no enforcement and
// no checkpoint rows.
func TestTracker_UnlimitedTenantNotEnforced(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 0, 1, adder, clk) // limit 0 = unlimited
	ctx := context.Background()

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 5000)
	if adder.callCount() != 0 {
		t.Fatalf("unlimited tenant must not write checkpoint rows, got %d calls", adder.callCount())
	}
	// Repeated reads stay cheap (no reconcile churn).
	clk.advance(time.Second)
	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("TenantUsed (unlimited): %v", err)
	}
	if adder.callCount() != 0 {
		t.Fatalf("unlimited tenant reconcile churn: %d Postgres calls", adder.callCount())
	}
}

// spec: §11.2 (rolling window); §12.4 line 268 — the in-memory budget mode is
// defined over fixed windows; a rolling-period tenant is not enforced here.
func TestTracker_RollingPeriodNotEnforced_spec_11_2(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	used, err := tr.TenantUsed(context.Background(), tenant, quota.ResetRolling, clk.now())
	if err != nil {
		t.Fatalf("TenantUsed(rolling): %v", err)
	}
	if used != 0 {
		t.Fatalf("rolling-period used = %d, want 0", used)
	}
	if adder.callCount() != 0 {
		t.Fatalf("rolling period must not touch Postgres, got %d calls", adder.callCount())
	}
}

// spec: §11.2 line 44 — Flush is the final-reconciliation hook persisting a
// replica's unflushed slice on shutdown.
func TestTracker_FlushPersistsConsumed_spec_11_2_44(t *testing.T) {
	clk := &clock{t: time.Date(2026, 6, 8, 14, 0, 0, 0, time.UTC)}
	adder := newFakeAdder()
	tr := newTracker(t, 1000, 1, adder, clk)
	ctx := context.Background()
	label := mustLabel(t, quota.ResetHourly, clk.now())

	if _, err := tr.TenantUsed(ctx, tenant, quota.ResetHourly, clk.now()); err != nil {
		t.Fatalf("draw: %v", err)
	}
	tr.Add(ctx, tenant, quota.ResetHourly, clk.now(), 250)
	if err := tr.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := adder.total(tenant, string(quota.ResetHourly), label); got != 250 {
		t.Fatalf("Postgres total after Flush = %d, want 250", got)
	}
}

// New panics on a missing required seam.
func TestNewPanicsOnMissingSeam(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatalf("New must panic when a required seam is nil")
		}
	}()
	_ = New(Options{Adder: newFakeAdder(), Replicas: StaticReplicaCount(1)})
}

func TestStaticReplicaCountFloorsAtOne(t *testing.T) {
	if StaticReplicaCount(0).Get() != 1 {
		t.Fatalf("StaticReplicaCount(0).Get() = %d, want 1", StaticReplicaCount(0).Get())
	}
	if StaticReplicaCount(-3).Get() != 1 {
		t.Fatalf("StaticReplicaCount(-3).Get() = %d, want 1", StaticReplicaCount(-3).Get())
	}
	if StaticReplicaCount(5).Get() != 5 {
		t.Fatalf("StaticReplicaCount(5).Get() = %d, want 5", StaticReplicaCount(5).Get())
	}
}

func mustLabel(t *testing.T, period quota.ResetPeriod, at time.Time) string {
	t.Helper()
	// The fixed-interval window label is derived by quotastore.WindowLabel;
	// re-derive it here to assert against the Postgres rollup row.
	lbl, err := quotastore.WindowLabel(period, at)
	if err != nil {
		t.Fatalf("window label: %v", err)
	}
	return lbl
}
