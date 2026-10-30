// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
)

// fakeWarmPoolLow is a §17.8.2 step-4 WarmPoolLowSource that reports a
// fixed recent-low verdict for every pool.
type fakeWarmPoolLow struct {
	fired bool
	err   error
}

func (f *fakeWarmPoolLow) WarmPoolLowFiredSince(context.Context, string, time.Time) (bool, error) {
	return f.fired, f.err
}

func ip(v int) *int { return &v }

// spec: §17.8.2 steps 1-2 — a pool with a bootstrapMinWarm override and
// no observed demand stays pinned to the override (status.scalingMode:
// bootstrap), and spec.minWarm equals the override.
func TestSyncBootstrapOverridePinsMinWarm_spec_17_8_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.BootstrapMinWarm = ip(2096)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r := &poolscaling.Reconciler{Client: c, Source: src} // no demand source → bootstrap
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	pool := getWarmPool(t, c)
	if pool.Spec.MinWarm != 2096 {
		t.Errorf("spec.minWarm = %d, want 2096 (override)", pool.Spec.MinWarm)
	}
	if pool.Status.ScalingMode != "bootstrap" {
		t.Errorf("status.scalingMode = %q, want bootstrap", pool.Status.ScalingMode)
	}
}

// spec: §17.8.2 — a pool without an override is formula-driven and
// carries no bootstrap-mode signal (status.scalingMode: formula).
func TestSyncNoOverrideReportsFormulaMode_spec_17_8_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		Demand: &fakeDemand{demand: poolscaling.Demand{BaseDemandP95: 0.1, Observed: true, HoursOfData: 100}},
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := getWarmPool(t, c).Status.ScalingMode; got != "formula" {
		t.Errorf("status.scalingMode = %q, want formula", got)
	}
}

// spec: §17.8.2 step 4 — with all criteria met (≥48h data, stable
// target, no WarmPoolLow, target ≤ 3× override) the controller converges
// to the formula target and reports status.scalingMode: formula.
func TestSyncBootstrapConvergesToFormula_spec_17_8_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.BootstrapMinWarm = ip(10) // formula target 6 is <= 3*10, so not underprovisioned
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Unix(1_000_000, 0).UTC()
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		Demand:                   &fakeDemand{demand: poolscaling.Demand{BaseDemandP95: 0.1, Observed: true, HoursOfData: 72}},
		WarmPoolLow:              &fakeWarmPoolLow{fired: false},
		BootstrapStabilityWindow: time.Hour,
		Now:                      func() time.Time { return now },
	}
	// First reconcile: stability run just started, so the pool is still
	// pinned to the override.
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	if got := getWarmPool(t, c).Status.ScalingMode; got != "bootstrap" {
		t.Fatalf("after 1 reconcile scalingMode = %q, want bootstrap (window not yet elapsed)", got)
	}
	// Advance past the stability window with the same steady target.
	now = now.Add(90 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	pool := getWarmPool(t, c)
	if pool.Status.ScalingMode != "formula" {
		t.Fatalf("after window elapsed scalingMode = %q, want formula", pool.Status.ScalingMode)
	}
	// steady = 0.1 · 1.5 · 35 = 5.25 → ceil = 6.
	if pool.Spec.MinWarm != 6 {
		t.Errorf("converged spec.minWarm = %d, want 6 (formula target)", pool.Spec.MinWarm)
	}
}

// spec: §17.8.2 step 4 criterion 3 — a recent WarmPoolLow keeps the pool
// in bootstrap mode even when the data and stability criteria are met.
func TestSyncBootstrapHeldByWarmPoolLow_spec_17_8_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.BootstrapMinWarm = ip(10)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Unix(1_000_000, 0).UTC()
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		Demand:                   &fakeDemand{demand: poolscaling.Demand{BaseDemandP95: 0.1, Observed: true, HoursOfData: 72}},
		WarmPoolLow:              &fakeWarmPoolLow{fired: true}, // a low fired in the last 6h
		BootstrapStabilityWindow: time.Hour,
		Now:                      func() time.Time { return now },
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	now = now.Add(90 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if got := getWarmPool(t, c).Status.ScalingMode; got != "bootstrap" {
		t.Errorf("scalingMode = %q, want bootstrap (recent WarmPoolLow blocks convergence)", got)
	}
}

// spec: §17.8.2 step 4 criterion 1 — fewer than 48h of data keeps the
// pool in bootstrap mode regardless of stability.
func TestSyncBootstrapHeldByInsufficientData_spec_17_8_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.BootstrapMinWarm = ip(10)
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	now := time.Unix(1_000_000, 0).UTC()
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		Demand:                   &fakeDemand{demand: poolscaling.Demand{BaseDemandP95: 0.1, Observed: true, HoursOfData: 12}},
		BootstrapStabilityWindow: time.Hour,
		Now:                      func() time.Time { return now },
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 1: %v", err)
	}
	now = now.Add(90 * time.Minute)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync 2: %v", err)
	}
	if got := getWarmPool(t, c).Status.ScalingMode; got != "bootstrap" {
		t.Errorf("scalingMode = %q, want bootstrap (only 12h of data)", got)
	}
}
