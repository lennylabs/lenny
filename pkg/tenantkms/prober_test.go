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
