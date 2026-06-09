// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"k8s.io/client-go/tools/record"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
)

// TestSyncDisabledOverrideForcesSDKWarmOff covers the §6.1 line 63
// `circuitBreakerOverride: disabled`: SDK-warm is forced off regardless of
// the demotion rate, recorded with the operator_manual reason and no
// minOpenUntil. spec: §6.1 lines 54, 63.
func TestSyncDisabledOverrideForcesSDKWarmOff_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	cfg := config()
	cfg.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideDisabled
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	// A low live rate must not matter: disabled ignores the rolling window.
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.0, HasSample: true}},
		Now:      frozenClock(now),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	pool := getWarmPool(t, c)
	if !pool.Spec.SDKWarmDisabled {
		t.Fatal("spec.sdkWarmDisabled should be true under the disabled override")
	}
	cb := pool.Status.SDKWarmCircuitBreaker
	if cb == nil {
		t.Fatal("status.sdkWarmCircuitBreaker should be populated under the disabled override")
	}
	if cb.OpenedReason != "operator_manual" {
		t.Errorf("openedReason = %q, want operator_manual", cb.OpenedReason)
	}
	if cb.MinOpenUntil != nil {
		t.Errorf("minOpenUntil = %v, want nil (operator disable is not grace-window bounded)", cb.MinOpenUntil)
	}
	if cb.OpenedAt == nil || !cb.OpenedAt.Time.Equal(now) {
		t.Errorf("openedAt = %v, want %v", cb.OpenedAt, now)
	}
}

// TestSyncDisabledOverridePreservesOpenedAt asserts a steady-state
// reconcile does not churn the operator_manual openedAt timestamp.
func TestSyncDisabledOverridePreservesOpenedAt_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	cfg := config()
	cfg.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideDisabled
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	first := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	r1 := &poolscaling.Reconciler{Client: c, Source: src, Now: frozenClock(first)}
	if err := r1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	r2 := &poolscaling.Reconciler{Client: c, Source: src, Now: frozenClock(first.Add(time.Hour))}
	if err := r2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	cb := getWarmPool(t, c).Status.SDKWarmCircuitBreaker
	if cb == nil || cb.OpenedAt == nil || !cb.OpenedAt.Time.Equal(first) {
		t.Errorf("openedAt should be preserved as %v across reconciles, got %+v", first, cb)
	}
}

// TestSyncEnabledOverrideClearsTrippedBreakerAheadOfGraceWindow covers
// the §6.1 lines 63-65 escape hatch: an `enabled` override clears a
// persisted breaker even while its minOpenUntil grace window has not
// elapsed and no DemotionRateSource is wired (the live rate reads 0).
func TestSyncEnabledOverrideClearsTrippedBreakerAheadOfGraceWindow_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)

	// Pass 1: trip the breaker with a high rate; minOpenUntil = now+1800s.
	cfg := config()
	cfg.ScalePolicy = minOpenPolicy(1800)
	tripped := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r1 := &poolscaling.Reconciler{
		Client:   c,
		Source:   tripped,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{Rate: 0.95, HasSample: true}},
		Now:      frozenClock(now),
	}
	if err := r1.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if !getWarmPool(t, c).Spec.SDKWarmDisabled {
		t.Fatal("precondition: breaker should be tripped after pass 1")
	}

	// Pass 2, still inside the grace window (now+60s), override=enabled,
	// no demotion source. The breaker must clear.
	cfgEnabled := config()
	cfgEnabled.ScalePolicy = minOpenPolicy(1800)
	cfgEnabled.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideEnabled
	enabled := &fakeSource{configs: []poolscaling.PoolConfig{cfgEnabled}}
	r2 := &poolscaling.Reconciler{Client: c, Source: enabled, Now: frozenClock(now.Add(60 * time.Second))}
	if err := r2.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	pool := getWarmPool(t, c)
	if pool.Spec.SDKWarmDisabled {
		t.Error("spec.sdkWarmDisabled should be cleared by the enabled override")
	}
	if pool.Status.SDKWarmCircuitBreaker != nil {
		t.Error("status.sdkWarmCircuitBreaker should be cleared by the enabled override")
	}
}

// TestSyncEnabledOverrideReTripsWhenRateStillHigh covers the §6.1 "one-shot
// ... does not suppress future trips" semantics: with the override set the
// breaker re-trips immediately if the live rate is still at or above 90%.
func TestSyncEnabledOverrideReTripsWhenRateStillHigh_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	now := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	cfg := config()
	cfg.ScalePolicy = minOpenPolicy(1800)
	cfg.SDKWarmCircuitBreakerOverride = poolstore.SDKWarmOverrideEnabled
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
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
		t.Error("enabled override must still re-trip the breaker at a >=90% live rate")
	}
	if cb := pool.Status.SDKWarmCircuitBreaker; cb == nil || cb.OpenedReason != "demotion_rate_exceeded" {
		t.Errorf("re-trip should record demotion_rate_exceeded, got %+v", cb)
	}
}

// TestSyncEmitsDemotionRateHighEvent covers the §6.1 line 48
// SDKWarmDemotionRateHigh warning event: it fires when the rolling 1-hour
// rate exceeds the 60% threshold and the pool has not acknowledged it.
func TestSyncEmitsDemotionRateHighEvent_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	rec := record.NewFakeRecorder(8)
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Events:   rec,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{HourRate: 0.75, HourHasSample: true}},
		Now:      frozenClock(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !contains(ev, "SDKWarmDemotionRateHigh") {
			t.Errorf("event = %q, want SDKWarmDemotionRateHigh", ev)
		}
	default:
		t.Fatal("expected an SDKWarmDemotionRateHigh event")
	}
}

// TestSyncSuppressesDemotionRateHighWhenAcknowledged covers the §6.1
// line 48 acknowledgeHighDemotionRate flag: it suppresses the event.
func TestSyncSuppressesDemotionRateHighWhenAcknowledged_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	rec := record.NewFakeRecorder(8)
	cfg := config()
	cfg.AcknowledgeHighDemotionRate = true
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Events:   rec,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{HourRate: 0.99, HourHasSample: true}},
		Now:      frozenClock(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("no event expected when acknowledged, got %q", ev)
	default:
	}
}

// TestSyncNoDemotionRateHighWithoutHourSample asserts the event never
// fires before the 1-hour window holds a usable sample.
func TestSyncNoDemotionRateHighWithoutHourSample_spec_6_1(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	rec := record.NewFakeRecorder(8)
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Events:   rec,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{HourRate: 0.99, HourHasSample: false}},
		Now:      frozenClock(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("no event expected without a 1-hour sample, got %q", ev)
	default:
	}
}

// floatPtr is a small helper for the *float64 demotion-rate threshold.
func floatPtr(v float64) *float64 { return &v }

// TestSyncDemotionRateHighHonorsHigherThreshold covers the §6.1 line 48
// deployer-configurable demotionRateThreshold: a rate that would fire at
// the 60% default is suppressed when the pool raises the threshold above
// the observed rate.
func TestSyncDemotionRateHighHonorsHigherThreshold_spec_6_1_48(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	rec := record.NewFakeRecorder(8)
	cfg := config()
	cfg.DemotionRateThreshold = floatPtr(0.80) // above the 75% observed rate
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Events:   rec,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{HourRate: 0.75, HourHasSample: true}},
		Now:      frozenClock(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	select {
	case ev := <-rec.Events:
		t.Errorf("no event expected when rate 75%% is below the 80%% threshold, got %q", ev)
	default:
	}
}

// TestSyncDemotionRateHighHonorsLowerThreshold covers the inverse: a rate
// below the 60% default still fires when the pool lowers the
// demotionRateThreshold under the observed rate.
func TestSyncDemotionRateHighHonorsLowerThreshold_spec_6_1_48(t *testing.T) {
	s := newScheme(t)
	c := breakerClient(s)
	rec := record.NewFakeRecorder(8)
	cfg := config()
	cfg.DemotionRateThreshold = floatPtr(0.50) // below the 55% observed rate
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r := &poolscaling.Reconciler{
		Client:   c,
		Source:   src,
		Events:   rec,
		Demotion: &fakeDemotion{signal: poolscaling.DemotionSignal{HourRate: 0.55, HourHasSample: true}},
		Now:      frozenClock(time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	select {
	case ev := <-rec.Events:
		if !contains(ev, "SDKWarmDemotionRateHigh") || !contains(ev, "50%") {
			t.Errorf("event = %q, want SDKWarmDemotionRateHigh naming the 50%% threshold", ev)
		}
	default:
		t.Fatal("expected an SDKWarmDemotionRateHigh event at the lowered threshold")
	}
}

// fakeQuerier is a static PromQLQuerier returning a value per query
// substring match, for the PrometheusDemotionSource tests.
type fakeQuerier struct {
	claims5m, demotions5m float64
	claims1h, demotions1h float64
	err                   error
}

func (f *fakeQuerier) Query(_ context.Context, q string) (float64, error) {
	if f.err != nil {
		return 0, f.err
	}
	switch {
	case contains(q, "claims_total") && contains(q, "[5m]"):
		return f.claims5m, nil
	case contains(q, "sdk_demotions_total") && contains(q, "[5m]"):
		return f.demotions5m, nil
	case contains(q, "claims_total") && contains(q, "[1h]"):
		return f.claims1h, nil
	case contains(q, "sdk_demotions_total") && contains(q, "[1h]"):
		return f.demotions1h, nil
	}
	return 0, errors.New("unexpected query: " + q)
}

// TestPrometheusDemotionSourceComputesBothWindows covers F-6.2.21: the
// production source computes the 5-minute (breaker) and 1-hour
// (demotion-rate-high) demotion rates as demotions/claims.
func TestPrometheusDemotionSourceComputesBothWindows_spec_6_1(t *testing.T) {
	src := &poolscaling.PrometheusDemotionSource{Querier: &fakeQuerier{
		claims5m: 10, demotions5m: 9,
		claims1h: 100, demotions1h: 70,
	}}
	sig, err := src.PoolDemotionSignal(context.Background(), "acme")
	if err != nil {
		t.Fatalf("PoolDemotionSignal: %v", err)
	}
	if sig.Rate != 0.9 || !sig.HasSample {
		t.Errorf("5m rate = %v (sample=%v), want 0.9 true", sig.Rate, sig.HasSample)
	}
	if sig.HourRate != 0.7 || !sig.HourHasSample {
		t.Errorf("1h rate = %v (sample=%v), want 0.7 true", sig.HourRate, sig.HourHasSample)
	}
}

// TestPrometheusDemotionSourceZeroClaimsHasNoSample asserts a window with
// no claims reports HasSample=false rather than a divide-by-zero rate.
func TestPrometheusDemotionSourceZeroClaimsHasNoSample_spec_6_1(t *testing.T) {
	src := &poolscaling.PrometheusDemotionSource{Querier: &fakeQuerier{
		claims5m: 0, demotions5m: 0,
		claims1h: 0, demotions1h: 0,
	}}
	sig, err := src.PoolDemotionSignal(context.Background(), "acme")
	if err != nil {
		t.Fatalf("PoolDemotionSignal: %v", err)
	}
	if sig.HasSample || sig.HourHasSample {
		t.Errorf("zero-claim windows should report no sample, got %+v", sig)
	}
}

// TestPrometheusDemotionSourceClampsAboveOne asserts the ratio is clamped
// to [0,1] when demotions exceed claims over the window (a transient
// counter-reset artifact).
func TestPrometheusDemotionSourceClampsAboveOne_spec_6_1(t *testing.T) {
	src := &poolscaling.PrometheusDemotionSource{Querier: &fakeQuerier{
		claims5m: 5, demotions5m: 8,
		claims1h: 5, demotions1h: 8,
	}}
	sig, err := src.PoolDemotionSignal(context.Background(), "acme")
	if err != nil {
		t.Fatalf("PoolDemotionSignal: %v", err)
	}
	if sig.Rate != 1.0 || sig.HourRate != 1.0 {
		t.Errorf("rates should clamp to 1.0, got 5m=%v 1h=%v", sig.Rate, sig.HourRate)
	}
}

// contains is a tiny substring helper so the tests avoid importing strings.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
