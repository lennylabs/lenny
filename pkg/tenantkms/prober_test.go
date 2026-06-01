// SPDX-License-Identifier: MIT

package tenantkms_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/lennylabs/lenny/pkg/kms"
	"github.com/lennylabs/lenny/pkg/tenantkms"
)

func newProber(t *testing.T, tenants []string, clock func() time.Time) (*tenantkms.Prober, *tenantkms.LocalManager, *tenantkms.ProbeMetrics) {
	t.Helper()
	seed := bytes.Repeat([]byte{0x33}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	mgr := tenantkms.NewLocalManager(local)
	lc := tenantkms.NewWithClock(mgr, clock)
	pm, err := tenantkms.NewProbeMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewProbeMetrics: %v", err)
	}
	p := &tenantkms.Prober{
		Lifecycle: lc,
		Tenants:   tenantkms.StaticTenantSource{Tenants: tenants},
		Metrics:   pm,
		Now:       clock,
	}
	return p, mgr, pm
}

func gaugeValue(t *testing.T, gv *prometheus.GaugeVec, tenant string) float64 {
	t.Helper()
	m, err := gv.GetMetricWithLabelValues(tenant)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%q): %v", tenant, err)
	}
	var dst dto.Metric
	if err := m.Write(&dst); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return dst.GetGauge().GetValue()
}

func counterValue(t *testing.T, cv *prometheus.CounterVec, tenant, result string) float64 {
	t.Helper()
	m, err := cv.GetMetricWithLabelValues(tenant, result)
	if err != nil {
		t.Fatalf("GetMetricWithLabelValues(%q,%q): %v", tenant, result, err)
	}
	var dst dto.Metric
	if err := m.Write(&dst); err != nil {
		t.Fatalf("Write metric: %v", err)
	}
	return dst.GetCounter().GetValue()
}

func TestProberRunCycleSuccessStampsGauge(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	p, mgr, pm := newProber(t, []string{"acme"}, func() time.Time { return now })
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := p.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	want := float64(now.Unix())
	if got := gaugeValue(t, pm.LastSuccessTimestamp, "acme"); got != want {
		t.Errorf("last-success gauge = %f, want %f", got, want)
	}
	if got := counterValue(t, pm.ResultTotal, "acme", "success"); got != 1 {
		t.Errorf("success counter = %f, want 1", got)
	}
}

func TestProberRunCycleKeyDisabledIncrementsCounter(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	p, mgr, pm := newProber(t, []string{"acme"}, func() time.Time { return now })
	ctx := context.Background()
	alias := tenantkms.AliasFor("acme")
	if _, err := mgr.ProvisionKey(ctx, alias); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if _, err := mgr.DisableKey(ctx, alias); err != nil {
		t.Fatalf("DisableKey: %v", err)
	}
	if err := p.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := counterValue(t, pm.ResultTotal, "acme", "key_unavailable"); got != 1 {
		t.Errorf("key_unavailable counter = %f, want 1", got)
	}
	// The gauge must not advance for a failed probe.
	if got := counterValue(t, pm.ResultTotal, "acme", "success"); got != 0 {
		t.Errorf("success counter on failed probe = %f, want 0", got)
	}
}

func TestProberRunCycleKeyMissingIncrementsCounter(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	p, _, pm := newProber(t, []string{"bob"}, func() time.Time { return now })
	if err := p.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := counterValue(t, pm.ResultTotal, "bob", "key_not_found"); got != 1 {
		t.Errorf("key_not_found counter = %f, want 1", got)
	}
}

func TestProberRunCycleMultipleTenantsIndependent(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	p, mgr, pm := newProber(t, []string{"acme", "globex"}, func() time.Time { return now })
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey acme: %v", err)
	}
	// "globex" intentionally has no key — its probe must fail without
	// affecting the success path of "acme".
	if err := p.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if got := gaugeValue(t, pm.LastSuccessTimestamp, "acme"); got != float64(now.Unix()) {
		t.Errorf("acme gauge = %f, want %f", got, float64(now.Unix()))
	}
	if got := counterValue(t, pm.ResultTotal, "globex", "key_not_found"); got != 1 {
		t.Errorf("globex key_not_found counter = %f, want 1", got)
	}
}

func TestProberLifecycleLastSuccessSurvivesCycle(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	p, mgr, _ := newProber(t, []string{"acme"}, func() time.Time { return now })
	ctx := context.Background()
	if _, err := mgr.ProvisionKey(ctx, tenantkms.AliasFor("acme")); err != nil {
		t.Fatalf("ProvisionKey: %v", err)
	}
	if err := p.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	got, ok := p.Lifecycle.LastProbeSuccess("acme")
	if !ok || !got.Equal(now) {
		t.Errorf("Lifecycle.LastProbeSuccess after cycle = %s ok=%v, want %s", got, ok, now)
	}
}

// failingSource simulates an admin tenant store that cannot enumerate
// tenants — the probe loop logs and skips the cycle without panicking.
type failingSource struct{}

func (failingSource) T4Tenants(_ context.Context) ([]string, error) {
	return nil, errors.New("simulated tenant store outage")
}

// newRateLimitedProber builds a Prober over a LocalManager whose key
// for every tenant is pre-provisioned, with a configurable RateLimit so
// the §12.5 line 307 token-bucket path is exercised.
func newRateLimitedProber(t *testing.T, tenants []string, rate float64) *tenantkms.Prober {
	t.Helper()
	seed := bytes.Repeat([]byte{0x55}, kms.DEKSize)
	local, err := kms.NewLocal(seed)
	if err != nil {
		t.Fatalf("kms.NewLocal: %v", err)
	}
	mgr := tenantkms.NewLocalManager(local)
	for _, tn := range tenants {
		if _, err := mgr.ProvisionKey(context.Background(), tenantkms.AliasFor(tn)); err != nil {
			t.Fatalf("ProvisionKey %q: %v", tn, err)
		}
	}
	pm, err := tenantkms.NewProbeMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewProbeMetrics: %v", err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	return &tenantkms.Prober{
		Lifecycle: tenantkms.NewWithClock(mgr, func() time.Time { return now }),
		Tenants:   tenantkms.StaticTenantSource{Tenants: tenants},
		Metrics:   pm,
		RateLimit: rate,
		Now:       func() time.Time { return now },
	}
}

// spec: §12.5 line 307 — the continuous probe rate-limits enumeration
// via a token bucket so a large T4 fleet does not burst the KMS backend.

func TestProberRateLimitProbesAllTenants_spec_12_5_307(t *testing.T) {
	tenants := []string{"acme", "globex", "initech", "umbrella"}
	p := newRateLimitedProber(t, tenants, 1000) // high ceiling: no perceptible delay
	if err := p.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	for _, tn := range tenants {
		if got := counterValue(t, p.Metrics.ResultTotal, tn, "success"); got != 1 {
			t.Errorf("tenant %q success counter = %f, want 1 (rate limiting must not drop tenants)", tn, got)
		}
	}
}

func TestProberRateLimitThrottlesIssuance_spec_12_5_307(t *testing.T) {
	if testing.Short() {
		t.Skip("timing-sensitive; skipped in -short")
	}
	// Burst = int(RateLimit) = 5; the 6th and 7th probes each wait ~1/5s,
	// so a 7-tenant sweep takes at least ~0.4s of token-bucket delay.
	tenants := []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"}
	p := newRateLimitedProber(t, tenants, 5)
	start := time.Now()
	if err := p.RunCycle(context.Background()); err != nil {
		t.Fatalf("RunCycle: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 300*time.Millisecond {
		t.Errorf("rate-limited sweep of %d tenants took %s, want >= 300ms (token bucket should throttle past the burst)", len(tenants), elapsed)
	}
}

func TestProberRateLimitContextCancelStopsSweep_spec_12_5_307(t *testing.T) {
	p := newRateLimitedProber(t, []string{"t1", "t2", "t3"}, 1) // burst 1
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled: the first limiter.Wait returns an error
	if err := p.RunCycle(ctx); err != nil {
		t.Fatalf("RunCycle after cancel = %v, want nil (clean stop)", err)
	}
	// No probe should have completed once the bucket Wait observes the
	// cancelled context.
	if got := counterValue(t, p.Metrics.ResultTotal, "t1", "success"); got != 0 {
		t.Errorf("success counter after cancelled sweep = %f, want 0", got)
	}
}

func TestProberMinIntervalConstant_spec_12_5_307(t *testing.T) {
	// §12.5 line 307 pins the cadence floor at 60s and the default rate
	// ceiling at 10 probes/sec.
	if tenantkms.MinProbeInterval != 60*time.Second {
		t.Errorf("MinProbeInterval = %s, want 60s", tenantkms.MinProbeInterval)
	}
	if tenantkms.DefaultProbeRateLimit != 10 {
		t.Errorf("DefaultProbeRateLimit = %v, want 10", tenantkms.DefaultProbeRateLimit)
	}
	if tenantkms.DefaultProbeInterval != 5*time.Minute {
		t.Errorf("DefaultProbeInterval = %s, want 5m", tenantkms.DefaultProbeInterval)
	}
}

func TestProberRunCycleSourceErrorReturned(t *testing.T) {
	now := time.Date(2026, 5, 20, 12, 0, 0, 0, time.UTC)
	pm, err := tenantkms.NewProbeMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatalf("NewProbeMetrics: %v", err)
	}
	seed := bytes.Repeat([]byte{0x44}, kms.DEKSize)
	local, _ := kms.NewLocal(seed)
	mgr := tenantkms.NewLocalManager(local)
	lc := tenantkms.NewWithClock(mgr, func() time.Time { return now })
	p := &tenantkms.Prober{
		Lifecycle: lc,
		Tenants:   failingSource{},
		Metrics:   pm,
		Now:       func() time.Time { return now },
	}
	err = p.RunCycle(context.Background())
	if err == nil || !strings.Contains(err.Error(), "tenant store outage") {
		t.Errorf("RunCycle source error = %v, want simulated tenant store outage", err)
	}
}
