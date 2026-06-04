// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// fakeDemotion is a static DemotionRateSource for the circuit-breaker
// sync tests.
type fakeDemotion struct {
	signal poolscaling.DemotionSignal
	err    error
}

func (f *fakeDemotion) PoolDemotionSignal(context.Context, string) (poolscaling.DemotionSignal, error) {
	return f.signal, f.err
}

// frozenClock returns a clock function pinned to t.
func frozenClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// breakerClient builds a fake client with the SandboxWarmPool status
// subresource registered, so the §6.1 circuit-breaker status writes
// (Status().Update) take effect the way they would against a real API
// server.
func breakerClient(s *runtime.Scheme, objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(objs...).
		WithStatusSubresource(&lennyv1.SandboxWarmPool{}).
		Build()
}

// minOpenPolicy builds a ScalePolicy carrying an explicit circuit-breaker
// grace window in seconds.
func minOpenPolicy(seconds int64) *lennyv1.ScalePolicy {
	return &lennyv1.ScalePolicy{SDKWarmCircuitBreakerMinOpenSeconds: &seconds}
}

func TestSyncTripsBreakerOnHighDemotionRate(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	cfg := config()
	cfg.ScalePolicy = minOpenPolicy(1800)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.95, HasSample: true}},
		Now:      frozenClock(now),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	pool := getWarmPool(t, c)
	if !pool.Spec.SDKWarmDisabled {
		t.Fatal("spec.sdkWarmDisabled should be true after the breaker trips")
	}
	cb := pool.Status.SDKWarmCircuitBreaker
	if cb == nil {
		t.Fatal("status.sdkWarmCircuitBreaker should be populated after a trip")
	}
	if cb.OpenedAt == nil || !cb.OpenedAt.Time.Equal(now) {
		t.Errorf("openedAt = %v, want %v", cb.OpenedAt, now)
	}
	if cb.OpenedReason != "demotion_rate_exceeded" {
		t.Errorf("openedReason = %q, want demotion_rate_exceeded", cb.OpenedReason)
	}
	wantUntil := now.Add(1800 * time.Second)
	if cb.MinOpenUntil == nil || !cb.MinOpenUntil.Time.Equal(wantUntil) {
		t.Errorf("minOpenUntil = %v, want %v", cb.MinOpenUntil, wantUntil)
	}
}

func TestSyncLeavesBreakerClosedOnLowDemotionRate(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.10, HasSample: true}},
		Now:      frozenClock(time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	pool := getWarmPool(t, c)
	if pool.Spec.SDKWarmDisabled {
		t.Error("spec.sdkWarmDisabled should stay false at a low demotion rate")
	}
	if pool.Status.SDKWarmCircuitBreaker != nil {
		t.Error("status.sdkWarmCircuitBreaker should stay nil while the breaker is closed")
	}
}

// TestSyncBreakerStateRoundTripsAndHoldsOpen drives two sync passes:
// the first trips the breaker, the second runs 10 minutes later with a
// fully recovered rate. The persisted minOpenUntil must hold the
// breaker open across the second pass — the §6.1 leader-failover
// guard, exercised here as state read back from the status subresource.
func TestSyncBreakerStateRoundTripsAndHoldsOpen(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	cfg := config()
	cfg.ScalePolicy = minOpenPolicy(1800)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	// Pass 1: high rate trips the breaker.
	r1 := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.95, HasSample: true}},
		Now:      frozenClock(opened),
	}
	if err := r1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync pass 1: %v", err)
	}
	first := getWarmPool(t, c).Status.SDKWarmCircuitBreaker
	if first == nil || first.OpenedAt == nil {
		t.Fatal("pass 1 should have persisted an open breaker")
	}

	// Pass 2: 10 minutes later the rate has recovered, but the
	// persisted minOpenUntil (opened + 30m) has not elapsed.
	r2 := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.0, HasSample: true}},
		Now:      frozenClock(opened.Add(10 * time.Minute)),
	}
	if err := r2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync pass 2: %v", err)
	}

	pool := getWarmPool(t, c)
	if !pool.Spec.SDKWarmDisabled {
		t.Fatal("breaker must stay open inside the grace window despite the recovered rate")
	}
	cb := pool.Status.SDKWarmCircuitBreaker
	if cb == nil || cb.OpenedAt == nil {
		t.Fatal("breaker status should still be persisted in pass 2")
	}
	if !cb.OpenedAt.Time.Equal(opened) {
		t.Errorf("openedAt = %v after pass 2, want the original trip time %v", cb.OpenedAt, opened)
	}
}

// TestSyncBreakerClosesAfterGraceWindow drives a third pass after the
// grace window has elapsed with a recovered rate; the breaker closes
// and the status carve-out is cleared.
func TestSyncBreakerClosesAfterGraceWindow(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	cfg := config()
	cfg.ScalePolicy = minOpenPolicy(1800)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)

	r1 := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.95, HasSample: true}},
		Now:      frozenClock(opened),
	}
	if err := r1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync pass 1: %v", err)
	}

	// 31 minutes later: grace window elapsed, rate recovered.
	r2 := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.05, HasSample: true}},
		Now:      frozenClock(opened.Add(31 * time.Minute)),
	}
	if err := r2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync pass 2: %v", err)
	}

	pool := getWarmPool(t, c)
	if pool.Spec.SDKWarmDisabled {
		t.Error("breaker should close once the grace window elapses and the rate recovers")
	}
	if pool.Status.SDKWarmCircuitBreaker != nil {
		t.Error("status.sdkWarmCircuitBreaker should be cleared when the breaker closes")
	}
}

// TestSyncHonorsPersistedBreakerWithoutDemotionSource confirms the
// §6.1 leader-failover guard: a fresh PSC leader with no
// DemotionRateSource still holds a persisted breaker open until its
// minOpenUntil elapses.
func TestSyncHonorsPersistedBreakerWithoutDemotionSource(t *testing.T) {
	s := newScheme(t)
	opened := time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)
	minOpenUntil := opened.Add(30 * time.Minute)

	// Seed a pool that already has an open breaker persisted.
	existing := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: testPool, Namespace: testNS},
		Spec: lennyv1.SandboxWarmPoolSpec{
			TemplateRef:     testPool,
			MinWarm:         3,
			MaxWarm:         10,
			SDKWarmDisabled: true,
		},
		Status: lennyv1.SandboxWarmPoolStatus{
			SDKWarmCircuitBreaker: &lennyv1.SDKWarmCircuitBreakerStatus{
				OpenedAt:     &metav1.Time{Time: opened},
				OpenedReason: "demotion_rate_exceeded",
				MinOpenUntil: &metav1.Time{Time: minOpenUntil},
			},
		},
	}
	c := breakerClient(s, existing)
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	// No Demotion source wired — simulating a fresh leader before its
	// rolling window exists. Reconcile 5 minutes after the trip.
	r := &poolscaling.Reconciler{
		Client: c,
		Source: src,
		Now:    frozenClock(opened.Add(5 * time.Minute)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	pool := getWarmPool(t, c)
	if !pool.Spec.SDKWarmDisabled {
		t.Fatal("a fresh leader must hold a persisted breaker open until its grace window elapses")
	}
	if pool.Status.SDKWarmCircuitBreaker == nil {
		t.Fatal("the persisted breaker status must survive a sync with no demotion source")
	}
}

func TestSyncPropagatesDemotionSourceError(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{err: errors.New("metrics window unavailable")},
		Now:      frozenClock(time.Date(2026, 5, 18, 12, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err == nil {
		t.Fatal("Sync should return an error when the demotion-rate source fails")
	}
}
