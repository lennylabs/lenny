// SPDX-License-Identifier: MIT

package strategy

import (
	"errors"
	"math"
	"testing"
)

// Worked example: standard session-mode pool, agent type, Tier 1 defaults.
//
//	base_demand_p95 = 2 claims/s
//	burst_p99_claims = 5 claims/s
//	safety_factor = 1.5
//	failover_seconds = 25
//	pod_warmup_seconds = 10
//	mode_factor = 1, burst_mode_factor = 1
//	weight = 1 (no variants)
//
// steady = 2 × 1.5 × (25+10) / 1 = 2 × 1.5 × 35 = 105
// burst  = 5 × 10 / 1 = 50
// target = ceil(105 + 50) = 155
func TestComputeStandardSession(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:          PoolStandard,
		Mode:              ModeSession,
		BaseDemandP95:     2,
		BurstP99Claims:    5,
		SafetyFactor:      1.5,
		FailoverSeconds:   25,
		PodWarmupSeconds:  10,
		HasObservedDemand: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 155 {
		t.Errorf("MinWarm: want 155, got %d", got.MinWarm)
	}
	if got.Mode != ScalingNormal {
		t.Errorf("Mode: want normal, got %q", got.Mode)
	}
	if got.FormulaInputs.EffectiveWeight != 1 {
		t.Errorf("EffectiveWeight: want 1, got %g", got.FormulaInputs.EffectiveWeight)
	}
}

// Task-mode reduces the steady-state pod count by mode_factor.
// Using the same inputs above with mode_factor=10 (i.e., each pod
// serves ~10 tasks before recycling), burst_mode_factor=1 (task pods
// process sequentially):
//
//	steady = 105 / 10 = 10.5
//	burst  = 50 / 1   = 50
//	target = ceil(60.5) = 61
func TestComputeTaskModeReducesSteady(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:          PoolStandard,
		Mode:              ModeTask,
		BaseDemandP95:     2,
		BurstP99Claims:    5,
		SafetyFactor:      1.5,
		FailoverSeconds:   25,
		PodWarmupSeconds:  10,
		ModeFactor:        10,
		BurstModeFactor:   1,
		HasObservedDemand: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 61 {
		t.Errorf("task-mode MinWarm: want 61, got %d", got.MinWarm)
	}
}

// Concurrent mode reduces both terms. Using maxConcurrent=8:
//
//	steady = 105 / 8 = 13.125
//	burst  = 50 / 8  = 6.25
//	target = ceil(19.375) = 20
func TestComputeConcurrentModeReducesBothTerms(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:          PoolStandard,
		Mode:              ModeConcurrent,
		BaseDemandP95:     2,
		BurstP99Claims:    5,
		SafetyFactor:      1.5,
		FailoverSeconds:   25,
		PodWarmupSeconds:  10,
		ModeFactor:        8,
		BurstModeFactor:   8,
		HasObservedDemand: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 20 {
		t.Errorf("concurrent MinWarm: want 20, got %d", got.MinWarm)
	}
}

// Variant pool: variant_weight=0.25 (exactly representable in IEEE 754
// so the worked example is rounding-stable) scales the formula
// proportionally.
//
//	steady = 2 × 0.25 × 1.5 × 35 = 26.25
//	burst  = 5 × 0.25 × 10 = 12.5
//	target = ceil(38.75) = 39
func TestComputeVariantPool(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:          PoolVariant,
		Mode:              ModeSession,
		BaseDemandP95:     2,
		BurstP99Claims:    5,
		SafetyFactor:      1.5,
		FailoverSeconds:   25,
		PodWarmupSeconds:  10,
		VariantWeight:     0.25,
		HasObservedDemand: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 39 {
		t.Errorf("variant MinWarm: want 39, got %d", got.MinWarm)
	}
}

// Adjusted base pool: standard pool with active variants. Σ
// variant_weights = 0.25 (exactly representable) leaves the base pool
// serving 0.75 of demand.
//
//	steady = 2 × 0.75 × 1.5 × 35 = 78.75
//	burst  = 5 × 0.75 × 10 = 37.5
//	target = ceil(116.25) = 117
func TestComputeAdjustedBasePool(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:                PoolStandard,
		Mode:                    ModeSession,
		BaseDemandP95:           2,
		BurstP99Claims:          5,
		SafetyFactor:            1.5,
		FailoverSeconds:         25,
		PodWarmupSeconds:        10,
		SumActiveVariantWeights: 0.25,
		HasObservedDemand:       true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 117 {
		t.Errorf("adjusted base MinWarm: want 117, got %d", got.MinWarm)
	}
	if math.Abs(got.FormulaInputs.EffectiveWeight-0.75) > 1e-9 {
		t.Errorf("EffectiveWeight: want 0.75, got %g", got.FormulaInputs.EffectiveWeight)
	}
}

// Bootstrap mode: when HasObservedDemand is false the strategy returns
// the configured fallback regardless of the rest of the inputs.
func TestComputeBootstrapMode(t *testing.T) {
	got, err := New().Compute(ScalingInputs{
		PoolType:          PoolStandard,
		Mode:              ModeSession,
		SafetyFactor:      1.5,
		FailoverSeconds:   25,
		PodWarmupSeconds:  10,
		BootstrapMinWarm:  7,
		HasObservedDemand: false,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.MinWarm != 7 {
		t.Errorf("bootstrap MinWarm: want 7, got %d", got.MinWarm)
	}
	if got.Mode != ScalingBootstrap {
		t.Errorf("Mode: want bootstrap, got %q", got.Mode)
	}
}

// Σ variant_weights ≥ 1 is invalid.
func TestComputeRejectsVariantWeightsExceedingOne(t *testing.T) {
	_, err := New().Compute(ScalingInputs{
		PoolType:                PoolStandard,
		Mode:                    ModeSession,
		BaseDemandP95:           2,
		BurstP99Claims:          5,
		SafetyFactor:            1.5,
		FailoverSeconds:         25,
		PodWarmupSeconds:        10,
		SumActiveVariantWeights: 1.0,
		HasObservedDemand:       true,
	})
	if !errors.Is(err, ErrVariantWeightsExceedOne) {
		t.Fatalf("expected ErrVariantWeightsExceedOne, got %v", err)
	}
}

// Variant pool with weight outside (0, 1) is rejected.
func TestComputeRejectsBadVariantWeight(t *testing.T) {
	cases := []float64{-0.1, 0, 1, 1.5}
	for _, w := range cases {
		_, err := New().Compute(ScalingInputs{
			PoolType:          PoolVariant,
			Mode:              ModeSession,
			BaseDemandP95:     2,
			BurstP99Claims:    5,
			SafetyFactor:      1.5,
			FailoverSeconds:   25,
			PodWarmupSeconds:  10,
			VariantWeight:     w,
			HasObservedDemand: true,
		})
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("variant_weight=%g: want ErrInvalidInput, got %v", w, err)
		}
	}
}

// Required positive inputs are validated.
func TestComputeRejectsBadInputs(t *testing.T) {
	cases := []struct {
		name  string
		input ScalingInputs
	}{
		{"zero safety", ScalingInputs{PoolType: PoolStandard, SafetyFactor: 0, PodWarmupSeconds: 10}},
		{"negative pod warmup", ScalingInputs{PoolType: PoolStandard, SafetyFactor: 1.5, PodWarmupSeconds: 0}},
		{"unknown mode", ScalingInputs{PoolType: PoolStandard, Mode: "bogus", SafetyFactor: 1.5, PodWarmupSeconds: 10}},
		{"negative bootstrap", ScalingInputs{PoolType: PoolStandard, SafetyFactor: 1.5, PodWarmupSeconds: 10, BootstrapMinWarm: -1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New().Compute(c.input); !errors.Is(err, ErrInvalidInput) {
				t.Errorf("expected ErrInvalidInput, got %v", err)
			}
		})
	}
}

// Strategy implements the PoolScalingStrategy interface.
func TestDefaultStrategyImplementsInterface(t *testing.T) {
	var _ PoolScalingStrategy = New()
}
