// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()

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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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

// TestSyncAppliesTierDefaultSafetyFactor confirms a pool that does not pin
// SafetyFactor inherits the Reconciler's tier-resolved default. With the
// Tier 3 default of 1.2 (instead of the Tier 1/2 1.5) the formula yields
// ceil(0.1 × 1.2 × 35 + 0.2 × 10) = ceil(4.2 + 2.0) = 7, one below the
// Tier 1/2 result of 8. spec: spec/17_deployment-topology.md line 1008.
func TestSyncAppliesTierDefaultSafetyFactor_spec_17_8_2_1008(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.1,
		BurstP99Claims: 0.2,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{
		Client:              c,
		Source:              src,
		Demand:              demand,
		DefaultSafetyFactor: poolscaling.DefaultSafetyFactorForTier("tier3"),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 7 {
		t.Errorf("warm pool minWarm = %d, want 7 (Tier 3 safety_factor 1.2)", got)
	}
}

// TestSyncPoolSafetyFactorOverridesTierDefault confirms a pool that pins
// its own SafetyFactor ignores the Reconciler's tier default. With the
// pool pinning 2.0 the formula yields ceil(0.1 × 2.0 × 35 + 0.2 × 10) =
// ceil(7.0 + 2.0) = 9. spec: spec/17_deployment-topology.md line 1010.
func TestSyncPoolSafetyFactorOverridesTierDefault_spec_17_8_2_1010(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.SafetyFactor = 2.0
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	demand := &fakeDemand{demand: poolscaling.Demand{
		BaseDemandP95:  0.1,
		BurstP99Claims: 0.2,
		Observed:       true,
	}}

	r := &poolscaling.Reconciler{
		Client:              c,
		Source:              src,
		Demand:              demand,
		DefaultSafetyFactor: poolscaling.DefaultSafetyFactorForTier("tier3"),
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}

	if got := getWarmPool(t, c).Spec.MinWarm; got != 9 {
		t.Errorf("warm pool minWarm = %d, want 9 (pool-pinned safety_factor 2.0)", got)
	}
}

func TestSyncStaysAtBootstrapWithoutObservedDemand(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
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

// spec: §4.6.2 item 2 (per-tuple backoff gates re-sync within the pause)
// A denied apply pauses the tuple for the backoff window; a Sync inside
// that window does not re-issue the apply, and a Sync after the window
// retries.
func TestSyncBacksOffWithinPause(t *testing.T) {
	scheme := newScheme(t)
	var creates int
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			creates++
			return apierrors.NewForbidden(
				schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"},
				obj.GetName(), errors.New("validator rejected"))
		},
	}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	now := time.Unix(1000, 0)
	r := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return now }}

	// First pass: both CRD tuples are denied once (2 Create attempts).
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if creates != 2 {
		t.Fatalf("after first sync creates=%d, want 2 (template+warmpool)", creates)
	}
	// Second pass at the same instant: both tuples are inside the 1s
	// backoff window, so neither apply is retried.
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if creates != 2 {
		t.Errorf("sync inside backoff retried the apply: creates=%d, want 2", creates)
	}
	// Advancing past the 1s window lets the tuples retry.
	now = now.Add(1 * time.Second)
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	if creates != 4 {
		t.Errorf("sync after backoff did not retry: creates=%d, want 4", creates)
	}
}

// spec: §4.6.2 item 3 (stuck-pool abort and operator resume)
// At the retry ceiling the pool is marked stuck and no longer retried;
// ResumeReconciliation clears the in-memory counter so a later Sync —
// once the underlying rejection clears — succeeds.
func TestSyncStuckPoolResumesAfterReconciliationReset(t *testing.T) {
	scheme := newScheme(t)
	deny := true
	var creates int
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
			if deny {
				creates++
				return apierrors.NewForbidden(
					schema.GroupResource{Group: "lenny.dev", Resource: "sandboxwarmpools"},
					obj.GetName(), errors.New("validator rejected"))
			}
			return cl.Create(ctx, obj, opts...)
		},
	}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	now := time.Unix(1000, 0)
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		AdmissionDeniedRetryCeiling: 1,
		Now:                         func() time.Time { return now },
	}

	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if got := r.StuckPools(); len(got) != 1 || got[0] != testNS+"/"+testPool {
		t.Fatalf("StuckPools = %v, want [%s/%s]", got, testNS, testPool)
	}
	createsBeforeStuckSync := creates
	now = now.Add(5 * time.Minute) // well past any backoff
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 2: %v", err)
	}
	if creates != createsBeforeStuckSync {
		t.Errorf("stuck pool retried apply: creates=%d, want %d", creates, createsBeforeStuckSync)
	}

	// Operator resets the denial counter for the pool.
	if cleared := r.ResumeReconciliation(testNS, testPool); cleared != 2 {
		t.Errorf("ResumeReconciliation cleared=%d, want 2 (both tuples)", cleared)
	}
	// The underlying rejection has cleared; the next Sync now succeeds.
	deny = false
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync 3: %v", err)
	}
	if got := r.StuckPools(); len(got) != 0 {
		t.Errorf("StuckPools after recovery = %v, want []", got)
	}
	getTemplate(t, c)
	getWarmPool(t, c)
}

// spec: §4.6.2 item 3 condition (c) (AdminResumer binds the agent namespace)
// The adapter the gateway admin endpoint calls forwards the pool name
// to the Reconciler under the configured namespace.
func TestAdminResumerBindsNamespace(t *testing.T) {
	scheme := newScheme(t)
	deny := true
	c := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Create: func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.CreateOption) error {
			_ = deny
			return apierrors.NewForbidden(schema.GroupResource{}, obj.GetName(), errors.New("denied"))
		},
	}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	now := time.Unix(0, 0)
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		AdmissionDeniedRetryCeiling: 1,
		Now:                         func() time.Time { return now },
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	resumer := poolscaling.AdminResumer{Reconciler: r, Namespace: testNS}
	cleared, err := resumer.ResumePoolReconciliation(context.Background(), testPool)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if cleared != 2 {
		t.Errorf("AdminResumer cleared=%d, want 2", cleared)
	}
	// A wrong namespace clears nothing.
	other := poolscaling.AdminResumer{Reconciler: r, Namespace: "wrong-ns"}
	if got, _ := other.ResumePoolReconciliation(context.Background(), testPool); got != 0 {
		t.Errorf("wrong-namespace resume cleared=%d, want 0", got)
	}
}

// spec: §4.6.1 line 400 — a pool inside its scaleToZero window targets
// minWarm 0 regardless of its bootstrap floor or observed demand.
func TestSyncScaleToZeroOverridesMinWarmToZero(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.ScalePolicy = &lennyv1.ScalePolicy{
		ScaleToZero: &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *"},
	}
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	// 23:00 UTC falls inside the 22:00→06:00 window.
	now := time.Date(2026, 5, 24, 23, 0, 0, 0, time.UTC)
	r := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return now }}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 0 {
		t.Errorf("inside scaleToZero window minWarm = %d, want 0", got)
	}
}

// TestSyncStampsConfigGenerationOnCRDs_Spec4_6_2_558 confirms the
// PoolScalingController writes lenny.dev/config-generation onto both
// SandboxTemplate and SandboxWarmPool, taking the value from the
// PoolConfig.Generation the admin store bumps on every write.
// spec: spec/04_system-components.md line 558.
func TestSyncStampsConfigGenerationOnCRDs_Spec4_6_2_558(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.Generation = 7
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	tmpl := getTemplate(t, c)
	if got := tmpl.Annotations["lenny.dev/config-generation"]; got != "7" {
		t.Errorf("template config-generation annotation = %q, want 7", got)
	}
	pool := getWarmPool(t, c)
	if got := pool.Annotations["lenny.dev/config-generation"]; got != "7" {
		t.Errorf("warm pool config-generation annotation = %q, want 7", got)
	}
}

// TestSyncWithZeroGenerationOmitsAnnotation_Spec4_6_2_558 confirms a
// PoolConfig with the zero generation does not stamp the annotation
// (the source has not advanced past create-time).
func TestSyncWithZeroGenerationOmitsAnnotation_Spec4_6_2_558(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	src := &fakeSource{configs: []poolscaling.PoolConfig{config()}}
	if err := syncOnce(t, c, src); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	tmpl := getTemplate(t, c)
	if _, set := tmpl.Annotations["lenny.dev/config-generation"]; set {
		t.Errorf("template carries config-generation annotation with zero generation: %v", tmpl.Annotations)
	}
}

// TestSyncServiceModeUsesMaxConcurrent_spec_5_2 confirms a service-mode
// pool uses MaxConcurrent for both mode_factor and burst_mode_factor.
// With MaxConcurrent=8 and demand p95 0.4, the steady-state term
// collapses by a factor of 8. The session-mode session-rate reuse
// derivation lands with the gateway-side sessionPolicy sizing knobs in
// the poolscaling step; until then session mode keeps the base factors.
// spec: §5.2 (execution mode scaling implications).
func TestSyncServiceModeUsesMaxConcurrent_spec_5_2(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.Template.ExecutionMode = "service"
	cfg.Template.MaxConcurrent = 8
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	r := &poolscaling.Reconciler{
		Client: c, Source: src,
		Demand: &fakeDemand{demand: poolscaling.Demand{
			BaseDemandP95: 0.4, BurstP99Claims: 0.4, Observed: true,
		}},
	}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	// mode_factor=burst=8: steady = 0.4·1.5·35 / 8 = 2.625; burst =
	// 0.4·10 / 8 = 0.5 → ceil(3.125) = 4.
	if got := getWarmPool(t, c).Spec.MinWarm; got != 4 {
		t.Errorf("service pool minWarm = %d, want 4 (mode_factor=8)", got)
	}
}

// Outside the window the pool keeps its bootstrap floor.
func TestSyncScaleToZeroInactiveKeepsBootstrap(t *testing.T) {
	s := newScheme(t)
	c := fake.NewClientBuilder().WithScheme(s).WithStatusSubresource(&lennyv1.SandboxWarmPool{}).Build()
	cfg := config()
	cfg.ScalePolicy = &lennyv1.ScalePolicy{
		ScaleToZero: &lennyv1.ScaleToZeroPolicy{Schedule: "0 22 * * *", ResumeAt: "0 6 * * *"},
	}
	src := &fakeSource{configs: []poolscaling.PoolConfig{cfg}}
	// 12:00 UTC is outside the window.
	now := time.Date(2026, 5, 24, 12, 0, 0, 0, time.UTC)
	r := &poolscaling.Reconciler{Client: c, Source: src, Now: func() time.Time { return now }}
	if err := r.Sync(context.Background()); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if got := getWarmPool(t, c).Spec.MinWarm; got != 3 {
		t.Errorf("outside scaleToZero window minWarm = %d, want bootstrap 3", got)
	}
}
