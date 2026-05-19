// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
)

const (
	testNS   = "lenny-agents"
	testPool = "claude-worker-small"
)

type fakeSource struct {
	configs []poolscaling.PoolConfig
	err     error
}

func (f *fakeSource) ListPoolConfigs(context.Context) ([]poolscaling.PoolConfig, error) {
	return f.configs, f.err
}

func newScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := lennyv1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func config() poolscaling.PoolConfig {
	return poolscaling.PoolConfig{
		Name:      testPool,
		Namespace: testNS,
		Template: lennyv1.SandboxTemplateSpec{
			RuntimeRef:       "claude-code",
			IsolationProfile: "sandboxed",
			ResourceClass:    "medium",
			ExecutionMode:    "session",
		},
		MinWarm: 3,
		MaxWarm: 10,
	}
}

func syncOnce(t *testing.T, c client.Client, src *fakeSource) error {
	t.Helper()
	r := &poolscaling.Reconciler{Client: c, Source: src}
	return r.Sync(context.Background())
}

func getTemplate(t *testing.T, c client.Client) lennyv1.SandboxTemplate {
	t.Helper()
	var tm lennyv1.SandboxTemplate
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &tm); err != nil {
		t.Fatalf("get template: %v", err)
	}
	return tm
}

func getWarmPool(t *testing.T, c client.Client) lennyv1.SandboxWarmPool {
	t.Helper()
	var p lennyv1.SandboxWarmPool
	if err := c.Get(context.Background(),
		client.ObjectKey{Namespace: testNS, Name: testPool}, &p); err != nil {
		t.Fatalf("get warm pool: %v", err)
	}
	return p
}

func TestSyncCreatesTheCRDPair(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	tmpl := getTemplate(t, c)
	if tmpl.Spec.RuntimeRef != "claude-code" || tmpl.Spec.IsolationProfile != "sandboxed" {
		t.Errorf("template spec = %+v, want runtimeRef claude-code / isolation sandboxed", tmpl.Spec)
	}

	pool := getWarmPool(t, c)
	if pool.Spec.TemplateRef != testPool {
		t.Errorf("warm pool templateRef = %q, want %q", pool.Spec.TemplateRef, testPool)
	}
	if pool.Spec.MinWarm != 3 || pool.Spec.MaxWarm != 10 {
		t.Errorf("warm pool min/max = %d/%d, want 3/10", pool.Spec.MinWarm, pool.Spec.MaxWarm)
	}
}

func TestSyncUpdatesAnExistingPool(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// The admin API raised minWarm and switched the runtime image line.
	changed := config()
	changed.MinWarm = 7
	changed.Template.RuntimeRef = "claude-code-next"
	src.configs = []poolscaling.PoolConfig{changed}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("second Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 7 {
		t.Errorf("warm pool minWarm = %d after re-sync, want 7", got)
	}
	if got := getTemplate(t, c).Spec.RuntimeRef; got != "claude-code-next" {
		t.Errorf("template runtimeRef = %q after re-sync, want claude-code-next", got)
	}
}

func TestSyncCorrectsManualSpecDrift(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("first Sync: %v", err)
	}

	// Simulate an operator hand-editing the derived CRD.
	pool := getWarmPool(t, c)
	pool.Spec.MinWarm = 999
	if err := c.Update(context.Background(), &pool); err != nil {
		t.Fatalf("manual update: %v", err)
	}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("re-sync: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 3 {
		t.Errorf("warm pool minWarm = %d, want 3 (drift not corrected)", got)
	}
}

func TestSyncHandlesMultiplePools(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()

	a := config()
	a.Name = "pool-a"
	b := config()
	b.Name = "pool-b"
	b.MinWarm = 5
	src := &fakeSource{configs: []poolscaling.PoolConfig{a, b}}

	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	var pools lennyv1.SandboxWarmPoolList
	if err := c.List(context.Background(), &pools, client.InNamespace(testNS)); err != nil {
		t.Fatalf("list warm pools: %v", err)
	}
	if len(pools.Items) != 2 {
		t.Fatalf("synced %d warm pools, want 2", len(pools.Items))
	}
}

func TestSyncPropagatesSourceError(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{err: errors.New("postgres unreachable")}

	if err := syncOnce(t, c, src); err == nil {
		t.Fatal("Sync should return an error when the pool config source fails")
	}
}

type fakeDemand struct {
	demand poolscaling.Demand
	err    error
}

func (f *fakeDemand) PoolDemand(context.Context, string) (poolscaling.Demand, error) {
	return f.demand, f.err
}

// TestSyncUsesObservedDemandForMinWarm confirms that once a pool has
// observed demand, the warm-pool minWarm is the §4.6.2 formula result
// rather than the static bootstrap value. For the session-mode pool
// with safetyFactor 1.5, failover 25s, podWarmup 10s, and demand
// p95 0.1 / p99 0.2, the formula yields ceil(5.25 + 2.0) = 8.
func TestSyncUsesObservedDemandForMinWarm(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.1,
		BurstP99Claims: 0.2,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 8 {
		t.Errorf("warm pool minWarm = %d, want 8 (formula-derived)", got)
	}
}

func TestSyncStaysAtBootstrapWithoutObservedDemand(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  99,
		BurstP99Claims: 99,
		Observed:       false,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	// config()'s MinWarm is the bootstrap value; unconverged demand
	// must not let the high observed rate above leak into the result.
	if got := getWarmPool(t, c).Spec.MinWarm; got != 3 {
		t.Errorf("warm pool minWarm = %d, want 3 (bootstrap, demand not observed)", got)
	}
}

func TestSyncStaysAtBootstrapWithNoDemandSource(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}

	// No Demand wired: the controller operates the pool in bootstrap.
	r := &poolscaling.Reconciler{Client: c, Source: src}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 3 {
		t.Errorf("warm pool minWarm = %d, want 3 (bootstrap, no demand source)", got)
	}
}

func TestSyncPropagatesDemandSourceError(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	demand := &fakeDemand{err: errors.New("prometheus unreachable")}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err == nil {
		t.Fatal("Sync should return an error when the demand source fails")
	}
}

// spec: 4.6.2
// diagnosis: targetMinWarm hardcoded strategy.PoolStandard and never
// read PoolConfig.VariantWeight, so an experiment variant pool was
// sized with the full base-demand formula instead of the
// variant-weight-scaled §4.6.2 variant-pool formula.
//
// Session mode, safetyFactor 1.5, failover 25s, podWarmup 10s, demand
// p95 0.4 / p99 0.5, variant_weight 0.25. The variant-pool formula
// yields ceil(0.4·0.25·1.5·35 + 0.5·0.25·10) = ceil(5.25 + 1.25) = 7.
func TestSyncSizesVariantPoolByVariantWeight(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	cfg := config()
	cfg.PoolType = strategy.PoolVariant
	cfg.VariantWeight = 0.25
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.4,
		BurstP99Claims: 0.5,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 7 {
		t.Errorf("variant pool minWarm = %d, want 7 (variant-weight-scaled)", got)
	}
}

// spec: 4.6.2
// diagnosis: targetMinWarm never read PoolConfig.SumActiveVariantWeights,
// so a standard base pool with active experiment variants kept its full
// minWarm instead of the §4.6.2 base-pool adjustment that scales demand
// by (1 - Σ variant_weights), over-provisioning warm pods.
//
// Session mode, safetyFactor 1.5, failover 25s, podWarmup 10s, demand
// p95 0.4 / p99 0.5, Σ variant_weights 0.3. The adjusted base-pool
// formula yields ceil(0.4·0.7·1.5·35 + 0.5·0.7·10) = ceil(14.7 + 3.5) = 19.
func TestSyncAdjustsBasePoolBySumVariantWeights(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	cfg := config()
	cfg.PoolType = strategy.PoolStandard
	cfg.SumActiveVariantWeights = 0.3
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.4,
		BurstP99Claims: 0.5,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 19 {
		t.Errorf("base pool minWarm = %d, want 19 (adjusted by 1-Σ)", got)
	}
}

// spec: 4.6.2
// diagnosis: an unset PoolConfig.PoolType must default to a standard
// pool. The strategy rejects an empty PoolType, so targetMinWarm must
// resolve the default before calling Compute; without the default the
// Sync pass would fail on every pool a PoolConfigSource leaves
// un-annotated.
//
// config() leaves PoolType empty. With observed demand p95 0.1 / p99
// 0.2 the standard formula yields ceil(5.25 + 2.0) = 8, identical to
// an explicit standard pool.
func TestSyncTreatsEmptyPoolTypeAsStandard(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.1,
		BurstP99Claims: 0.2,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync with empty PoolType: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 8 {
		t.Errorf("minWarm = %d, want 8 (empty PoolType sized as standard)", got)
	}
}

// spec: 4.6.2
// diagnosis: a standard base pool whose Σ variant_weights reaches 1
// leaves no traffic for the base pool. The strategy returns
// ErrVariantWeightsExceedOne; targetMinWarm must surface that as a Sync
// failure so the bad experiment configuration is not silently sized.
func TestSyncRejectsBasePoolWithSumVariantWeightsAtOne(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).Build()
	cfg := config()
	cfg.PoolType = strategy.PoolStandard
	cfg.SumActiveVariantWeights = 1.0
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.4,
		BurstP99Claims: 0.5,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{Client: c, Source: src, Demand: demand}
	if err := r.Sync(context.Background()); err == nil {
		t.Fatal("Sync should fail when Σ variant_weights ≥ 1")
	}
}
