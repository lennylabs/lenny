// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"context"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
)

type fakeExpReader struct {
	exps []experimentstore.Experiment
	err  error
}

func (f fakeExpReader) ListAll(context.Context) ([]experimentstore.Experiment, error) {
	return f.exps, f.err
}

type fakeBaseResolver map[string]string

func (m fakeBaseResolver) ResolveBasePool(_ context.Context, runtime string) (string, bool) {
	p, ok := m[runtime]
	return p, ok
}

// baseConfigs returns three standard pools the experiment source rewrites.
func baseConfigs() []poolscaling.PoolConfig {
	mk := func(name string) poolscaling.PoolConfig {
		return poolscaling.PoolConfig{Name: name, Namespace: testNS, MinWarm: 5, MaxWarm: 20}
	}
	return []poolscaling.PoolConfig{mk("base-pool"), mk("variant-pool"), mk("variant-pool-2")}
}

func experimentDef(id, baseRuntime string, status experiment.Status, vs ...experimentstore.Variant) experimentstore.Experiment {
	return experimentstore.Experiment{
		ID: id, TenantID: "acme", Status: status, BaseRuntime: baseRuntime, Variants: vs,
	}
}

func byName(configs []poolscaling.PoolConfig, name string) poolscaling.PoolConfig {
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	return poolscaling.PoolConfig{}
}

func listConfigs(t *testing.T, src poolscaling.PoolConfigSource) []poolscaling.PoolConfig {
	t.Helper()
	out, err := src.ListPoolConfigs(context.Background())
	if err != nil {
		t.Fatalf("ListPoolConfigs: %v", err)
	}
	return out
}

// A nil experiment reader makes the source a transparent pass-through.
func TestExperimentSourceNilReaderPassesThrough(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{Inner: &fakeSource{configs: baseConfigs()}}
	out := listConfigs(t, src)
	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	if byName(out, "variant-pool").PoolType != "" {
		t.Errorf("pass-through must not rewrite PoolType")
	}
}

// spec: §10.7 line 1092 / §4.6.2 line 534 — an active experiment marks
// its variant pool as a variant and reduces the base pool by Σ weights.
func TestExperimentSourceActiveVariantAndBaseAdjustment(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-1", "claude-base", experiment.StatusActive,
				experimentstore.Variant{ID: "treatment", Pool: "variant-pool", Weight: 0.25}),
		}},
		BasePools: fakeBaseResolver{"claude-base": "base-pool"},
	}
	out := listConfigs(t, src)

	v := byName(out, "variant-pool")
	if v.PoolType != strategy.PoolVariant {
		t.Errorf("variant pool PoolType = %q, want variant", v.PoolType)
	}
	if v.VariantWeight != 0.25 {
		t.Errorf("variant weight = %g, want 0.25", v.VariantWeight)
	}
	b := byName(out, "base-pool")
	if b.SumActiveVariantWeights != 0.25 {
		t.Errorf("base SumActiveVariantWeights = %g, want 0.25", b.SumActiveVariantWeights)
	}
}

// spec: §10.7 line 1102 — a paused experiment's variant pool is pinned
// to minWarm 0, and its weight is removed from the base pool's sum.
func TestExperimentSourcePausedForcesZeroMinWarm(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-1", "claude-base", experiment.StatusPaused,
				experimentstore.Variant{ID: "treatment", Pool: "variant-pool", Weight: 0.25}),
		}},
		BasePools: fakeBaseResolver{"claude-base": "base-pool"},
	}
	out := listConfigs(t, src)

	v := byName(out, "variant-pool")
	if !v.ForceZeroMinWarm {
		t.Error("paused variant pool must set ForceZeroMinWarm")
	}
	if v.PoolType == strategy.PoolVariant {
		t.Error("paused variant pool must not be sized as an active variant")
	}
	if got := byName(out, "base-pool").SumActiveVariantWeights; got != 0 {
		t.Errorf("base sum = %g, want 0 (paused weight removed)", got)
	}
}

// spec: §10.7 line 1104 — a concluded experiment's variant pool is
// flagged for drain-and-delete.
func TestExperimentSourceConcludedMarksDrainAndDelete(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-1", "claude-base", experiment.StatusConcluded,
				experimentstore.Variant{ID: "treatment", Pool: "variant-pool", Weight: 0.25}),
		}},
		BasePools: fakeBaseResolver{"claude-base": "base-pool"},
	}
	out := listConfigs(t, src)

	if !byName(out, "variant-pool").DrainAndDelete {
		t.Error("concluded variant pool must set DrainAndDelete")
	}
}

// status precedence: active wins over concluded when both reference the
// same pool, so the pool stays sized as a live variant.
func TestExperimentSourceActiveWinsOverConcluded(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-active", "claude-base", experiment.StatusActive,
				experimentstore.Variant{ID: "t", Pool: "variant-pool", Weight: 0.3}),
			experimentDef("exp-old", "claude-base", experiment.StatusConcluded,
				experimentstore.Variant{ID: "t", Pool: "variant-pool", Weight: 0.3}),
		}},
		BasePools: fakeBaseResolver{"claude-base": "base-pool"},
	}
	out := listConfigs(t, src)

	v := byName(out, "variant-pool")
	if v.PoolType != strategy.PoolVariant {
		t.Errorf("PoolType = %q, want variant (active precedence)", v.PoolType)
	}
	if v.DrainAndDelete {
		t.Error("active variant must not be marked DrainAndDelete")
	}
}

// An active variant whose base pool cannot be resolved is still sized by
// the variant formula, but the base-pool adjustment is skipped.
func TestExperimentSourceOrphanVariantSizedWithoutBaseAdjustment(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-1", "unknown-runtime", experiment.StatusActive,
				experimentstore.Variant{ID: "t", Pool: "variant-pool", Weight: 0.4, InitialMinWarm: 7}),
		}},
		BasePools: fakeBaseResolver{}, // no resolution
	}
	out := listConfigs(t, src)

	v := byName(out, "variant-pool")
	if v.PoolType != strategy.PoolVariant || v.VariantWeight != 0.4 {
		t.Errorf("orphan variant not sized: %+v", v)
	}
	if v.MinWarm != 7 {
		t.Errorf("orphan variant MinWarm = %d, want 7 (initialMinWarm)", v.MinWarm)
	}
	if got := byName(out, "base-pool").SumActiveVariantWeights; got != 0 {
		t.Errorf("base sum = %g, want 0 (no base resolution)", got)
	}
}

// A malformed set of active experiments (aggregate weights >= 1) is
// rejected by ResolveVariantRoles; the source logs and returns the
// un-adjusted base set rather than wedging every pool.
func TestExperimentSourceMalformedActiveFallsBackToBase(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner: &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{exps: []experimentstore.Experiment{
			experimentDef("exp-1", "claude-base", experiment.StatusActive,
				experimentstore.Variant{ID: "t", Pool: "variant-pool", Weight: 0.6}),
			experimentDef("exp-2", "claude-base", experiment.StatusActive,
				experimentstore.Variant{ID: "t", Pool: "variant-pool-2", Weight: 0.6}),
		}},
		BasePools: fakeBaseResolver{"claude-base": "base-pool"},
	}
	out := listConfigs(t, src)

	if len(out) != 3 {
		t.Fatalf("len = %d, want 3", len(out))
	}
	for _, c := range out {
		if c.PoolType == strategy.PoolVariant || c.SumActiveVariantWeights != 0 {
			t.Errorf("pool %q adjusted despite malformed experiments: %+v", c.Name, c)
		}
	}
}

// A read error from the experiment registry is returned so the
// controller retries on the next tick.
func TestExperimentSourceReadErrorPropagates(t *testing.T) {
	src := &poolscaling.ExperimentVariantSource{
		Inner:       &fakeSource{configs: baseConfigs()},
		Experiments: fakeExpReader{err: context.DeadlineExceeded},
	}
	if _, err := src.ListPoolConfigs(context.Background()); err == nil {
		t.Fatal("want error from experiment read, got nil")
	}
}

func TestPoolStoreBasePoolResolver(t *testing.T) {
	store := poolstore.NewMemory()
	mk := func(name, runtime string) poolstore.Pool {
		return poolstore.Pool{Name: name, RuntimeRef: runtime, IsolationProfile: isolation.ProfileSandboxed}
	}
	for _, p := range []poolstore.Pool{
		mk("sole-pool", "lonely-runtime"),
		mk("dup-a", "shared-runtime"),
		mk("dup-b", "shared-runtime"),
	} {
		if err := store.Create(context.Background(), p); err != nil {
			t.Fatalf("seed pool %q: %v", p.Name, err)
		}
	}
	r := poolscaling.PoolStoreBasePoolResolver{Store: store}

	if got, ok := r.ResolveBasePool(context.Background(), "lonely-runtime"); !ok || got != "sole-pool" {
		t.Errorf("sole match = (%q,%v), want (sole-pool,true)", got, ok)
	}
	if _, ok := r.ResolveBasePool(context.Background(), "shared-runtime"); ok {
		t.Error("ambiguous (2 pools) must not resolve")
	}
	if _, ok := r.ResolveBasePool(context.Background(), "absent-runtime"); ok {
		t.Error("absent runtime must not resolve")
	}
	if _, ok := r.ResolveBasePool(context.Background(), ""); ok {
		t.Error("empty runtime must not resolve")
	}
	var nilResolver poolscaling.PoolStoreBasePoolResolver
	if _, ok := nilResolver.ResolveBasePool(context.Background(), "x"); ok {
		t.Error("nil store must not resolve")
	}
}
