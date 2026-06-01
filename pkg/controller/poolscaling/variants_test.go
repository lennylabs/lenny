// SPDX-License-Identifier: MIT

package poolscaling_test

import (
	"errors"
	"testing"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
)

// poolNamed returns a minimal PoolConfig with the given name, reusing
// config()'s template so ResolveVariantRoles operates on a realistic
// desired-state struct.
func poolNamed(name string) poolscaling.PoolConfig {
	c := config()
	c.Name = name
	return c
}

// findPool returns the resolved PoolConfig with the given name.
func findPool(t *testing.T, configs []poolscaling.PoolConfig, name string) poolscaling.PoolConfig {
	t.Helper()
	for _, c := range configs {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("pool %q not found in resolved configs", name)
	return poolscaling.PoolConfig{}
}

// spec: 4.6.2
// diagnosis: a PoolConfigSource needs ResolveVariantRoles to translate
// active experiment variants into per-pool PoolType / VariantWeight /
// SumActiveVariantWeights. One experiment on one base pool must mark the
// variant pool PoolVariant with its weight and set the base pool's
// SumActiveVariantWeights to that same weight.
func TestResolveVariantRolesAnnotatesBaseAndVariant(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("variant-a")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0.2},
	}

	out, err := poolscaling.ResolveVariantRoles(configs, variants)
	if err != nil {
		t.Fatalf("ResolveVariantRoles: %v", err)
	}

	v := findPool(t, out, "variant-a")
	if v.PoolType != strategy.PoolVariant || v.VariantWeight != 0.2 {
		t.Errorf("variant pool = {type %q, weight %g}, want {variant, 0.2}", v.PoolType, v.VariantWeight)
	}
	b := findPool(t, out, "base")
	if b.PoolType != strategy.PoolStandard || b.SumActiveVariantWeights != 0.2 {
		t.Errorf("base pool = {type %q, Σ %g}, want {standard, 0.2}", b.PoolType, b.SumActiveVariantWeights)
	}
}

// spec: 4.6.2
// diagnosis: §4.6.2 requires single-pass aggregation across all active
// experiments on a base pool. Two experiments diverting traffic from
// the same base pool must add their variant weights; a per-experiment
// last-write-wins would leave Σ at the second experiment's weight only.
func TestResolveVariantRolesAggregatesAcrossExperiments(t *testing.T) {
	configs := []poolscaling.PoolConfig{
		poolNamed("base"), poolNamed("variant-a"), poolNamed("variant-b"),
	}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0.2},
		{ExperimentID: "exp-2", BasePool: "base", VariantPool: "variant-b", Weight: 0.3},
	}

	out, err := poolscaling.ResolveVariantRoles(configs, variants)
	if err != nil {
		t.Fatalf("ResolveVariantRoles: %v", err)
	}

	b := findPool(t, out, "base")
	if b.SumActiveVariantWeights != 0.5 {
		t.Errorf("base pool Σ variant_weights = %g, want 0.5 (0.2 + 0.3 aggregated)", b.SumActiveVariantWeights)
	}
	if got := findPool(t, out, "variant-a").VariantWeight; got != 0.2 {
		t.Errorf("variant-a weight = %g, want 0.2", got)
	}
	if got := findPool(t, out, "variant-b").VariantWeight; got != 0.3 {
		t.Errorf("variant-b weight = %g, want 0.3", got)
	}
}

// spec: 4.6.2
// diagnosis: §4.6.2 clamps Σ variant_weights to [0,1) and rejects an
// experiment configuration whose aggregate reaches 1 with
// INVALID_VARIANT_WEIGHTS. ResolveVariantRoles must return an error
// wrapping ErrVariantWeightsExceedOne when two experiments' weights sum
// to ≥ 1 on one base pool.
func TestResolveVariantRolesRejectsSumAtOrAboveOne(t *testing.T) {
	configs := []poolscaling.PoolConfig{
		poolNamed("base"), poolNamed("variant-a"), poolNamed("variant-b"),
	}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0.6},
		{ExperimentID: "exp-2", BasePool: "base", VariantPool: "variant-b", Weight: 0.5},
	}

	_, err := poolscaling.ResolveVariantRoles(configs, variants)
	if !errors.Is(err, strategy.ErrVariantWeightsExceedOne) {
		t.Fatalf("error = %v, want one wrapping ErrVariantWeightsExceedOne", err)
	}
}

// spec: 4.6.2
// diagnosis: a variant pool's own SandboxWarmPool is sized by the
// variant formula, so a variant naming a VariantPool absent from the
// pool configs would route traffic to a pool with no warm-pod floor.
// ResolveVariantRoles must reject the configuration rather than drop it.
func TestResolveVariantRolesRejectsUnknownVariantPool(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "ghost", Weight: 0.2},
	}

	if _, err := poolscaling.ResolveVariantRoles(configs, variants); err == nil {
		t.Fatal("ResolveVariantRoles should reject a variant pool absent from the configs")
	}
}

// spec: 4.6.2
// diagnosis: the base pool whose minWarm needs the (1 - Σ) adjustment
// must exist among the pool configs; a BasePool naming an absent pool
// means the diversion cannot be applied, so the configuration is
// rejected.
func TestResolveVariantRolesRejectsUnknownBasePool(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("variant-a")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "ghost", VariantPool: "variant-a", Weight: 0.2},
	}

	if _, err := poolscaling.ResolveVariantRoles(configs, variants); err == nil {
		t.Fatal("ResolveVariantRoles should reject a base pool absent from the configs")
	}
}

// spec: 4.6.2
// diagnosis: a pool cannot be sized by both the variant formula and the
// base-pool adjustment at once; §4.6.2 treats the variant and base
// roles as mutually exclusive. A pool named as both a BasePool and a
// VariantPool is an ambiguous configuration and must be rejected.
func TestResolveVariantRolesRejectsPoolThatIsBothBaseAndVariant(t *testing.T) {
	configs := []poolscaling.PoolConfig{
		poolNamed("base"), poolNamed("middle"), poolNamed("leaf"),
	}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "middle", Weight: 0.2},
		{ExperimentID: "exp-2", BasePool: "middle", VariantPool: "leaf", Weight: 0.2},
	}

	if _, err := poolscaling.ResolveVariantRoles(configs, variants); err == nil {
		t.Fatal("ResolveVariantRoles should reject a pool that is both a base and a variant pool")
	}
}

// spec: 4.6.2
// diagnosis: a variant pool belongs to exactly one experiment with one
// variant_weight (§10.7); two variants naming the same VariantPool
// carry contradictory weights, so ResolveVariantRoles must reject the
// configuration.
func TestResolveVariantRolesRejectsDuplicateVariantPool(t *testing.T) {
	configs := []poolscaling.PoolConfig{
		poolNamed("base-a"), poolNamed("base-b"), poolNamed("variant"),
	}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base-a", VariantPool: "variant", Weight: 0.2},
		{ExperimentID: "exp-2", BasePool: "base-b", VariantPool: "variant", Weight: 0.3},
	}

	if _, err := poolscaling.ResolveVariantRoles(configs, variants); err == nil {
		t.Fatal("ResolveVariantRoles should reject the same variant pool claimed twice")
	}
}

// spec: 4.6.2
// diagnosis: variant_weight is a traffic fraction in (0,1) per §4.6.2;
// a non-positive or ≥ 1 weight is invalid and must be rejected with the
// failure attributable to the named experiment.
func TestResolveVariantRolesRejectsOutOfRangeWeight(t *testing.T) {
	// spec: §10.7 line 694 / line 743 — weight in [0.0, 1.0); 1.0 and
	// negative values are rejected. 0.0 is accepted (a staged variant
	// with no traffic) — see TestResolveVariantRolesAcceptsZeroWeight.
	// F-10.7.16.
	for _, w := range []float64{-0.1, 1, 1.5} {
		configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("variant-a")}
		variants := []poolscaling.ActiveVariant{
			{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: w},
		}
		if _, err := poolscaling.ResolveVariantRoles(configs, variants); err == nil {
			t.Errorf("weight %g: ResolveVariantRoles should reject a weight outside [0,1)", w)
		}
	}
}

// spec: §10.7 line 694 / line 743 — weight=0.0 admits a staged variant
// before traffic is turned on. F-10.7.16.
func TestResolveVariantRolesAcceptsZeroWeight(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("variant-a")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0},
	}
	out, err := poolscaling.ResolveVariantRoles(configs, variants)
	if err != nil {
		t.Fatalf("zero-weight variant should be admitted: %v", err)
	}
	if got := findPool(t, out, "variant-a").VariantWeight; got != 0 {
		t.Errorf("variant-a VariantWeight = %g, want 0", got)
	}
}

// spec: §10.7 lines 695, 705-710
// diagnosis: a variant's initialMinWarm is the bootstrap-mode static
// minWarm floor. ResolveVariantRoles must stamp it onto the variant
// pool's PoolConfig.MinWarm (which the controller feeds the strategy as
// BootstrapMinWarm) so a freshly created variant pool warms to the
// deployer-supplied floor before demand history exists. The base pool's
// MinWarm is left untouched.
func TestResolveVariantRolesAppliesInitialMinWarm_spec_10_7(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("variant-a")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0.1, InitialMinWarm: 5},
	}
	out, err := poolscaling.ResolveVariantRoles(configs, variants)
	if err != nil {
		t.Fatalf("ResolveVariantRoles: %v", err)
	}
	if got := findPool(t, out, "variant-a").MinWarm; got != 5 {
		t.Errorf("variant-a MinWarm = %d, want 5 (initialMinWarm floor)", got)
	}
	// The base pool keeps its own configured floor; initialMinWarm is a
	// per-variant-pool knob only.
	if got := findPool(t, out, "base").MinWarm; got != config().MinWarm {
		t.Errorf("base MinWarm = %d, want %d (untouched)", got, config().MinWarm)
	}
}

// spec: §10.7 line 709
// diagnosis: an omitted initialMinWarm defaults to 0 and must leave the
// variant pool's MinWarm untouched rather than zeroing it, so the
// no-history formula result governs the floor.
func TestResolveVariantRolesOmittedInitialMinWarmLeavesMinWarm_spec_10_7(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("variant-a")}
	variants := []poolscaling.ActiveVariant{
		{ExperimentID: "exp-1", BasePool: "base", VariantPool: "variant-a", Weight: 0.1},
	}
	out, err := poolscaling.ResolveVariantRoles(configs, variants)
	if err != nil {
		t.Fatalf("ResolveVariantRoles: %v", err)
	}
	if got := findPool(t, out, "variant-a").MinWarm; got != config().MinWarm {
		t.Errorf("variant-a MinWarm = %d, want %d (omitted initialMinWarm leaves it untouched)", got, config().MinWarm)
	}
}

// spec: 4.6.2
// diagnosis: with no active experiments every pool stays a standard
// pool with Σ variant_weights 0, and ResolveVariantRoles must not
// mutate the caller's input slice — it returns a copy the
// PoolConfigSource owns.
func TestResolveVariantRolesNoVariantsLeavesPoolsStandard(t *testing.T) {
	configs := []poolscaling.PoolConfig{poolNamed("base"), poolNamed("other")}
	out, err := poolscaling.ResolveVariantRoles(configs, nil)
	if err != nil {
		t.Fatalf("ResolveVariantRoles: %v", err)
	}
	for _, c := range out {
		if c.PoolType != "" || c.VariantWeight != 0 || c.SumActiveVariantWeights != 0 {
			t.Errorf("pool %q = %+v, want untouched standard fields", c.Name, c)
		}
	}

	// Mutating the returned copy must not reach the caller's slice.
	out[0].SumActiveVariantWeights = 0.9
	if configs[0].SumActiveVariantWeights != 0 {
		t.Error("ResolveVariantRoles mutated the caller's input slice")
	}
}
