// SPDX-License-Identifier: MIT

// Package strategy implements the PoolScalingStrategy interface and the
// default scaling formula from spec §4.6.2 (with the mode-adjusted form
// from §5.2 "Execution Mode Scaling Implications").
//
// The strategy returns the target minWarm pod count for a pool given
// observed demand inputs. It is intentionally pure: no Kubernetes
// client, no Postgres reader, no metrics scraper — these are wired in
// by the PoolScalingController (Phase 3.x deployment artefact) which
// builds the ScalingInputs from each side and calls Compute.
//
// The spec defines three formulas:
//
//  1. Standard (non-experiment) pool, session mode (the base formula):
//     target_minWarm = ceil(
//       base_demand_p95 × safety_factor ×
//       (failover_seconds + pod_warmup_seconds)
//       + burst_p99_claims × pod_warmup_seconds
//     )
//
//  2. Mode-adjusted form (task or concurrent mode), §5.2:
//     target_minWarm = ceil(
//       base_demand_p95 × safety_factor ×
//       (failover_seconds + pod_warmup_seconds) / mode_factor
//       + burst_p99_claims × pod_warmup_seconds / burst_mode_factor
//     )
//
//  3. Variant pool (A/B experiment), §4.6.2 variant-pool formula:
//     target_minWarm = ceil(
//       base_demand_p95 × variant_weight × safety_factor ×
//       (failover_seconds + pod_warmup_seconds) / mode_factor
//       + burst_p99_claims × variant_weight × pod_warmup_seconds /
//         burst_mode_factor
//     )
//
//  4. Adjusted base pool when one or more variant pools are active,
//     §4.6.2 base-pool adjustment:
//     base_minWarm = ceil(
//       base_demand_p95 × (1 - Σ variant_weights) × safety_factor ×
//       (failover_seconds + pod_warmup_seconds) / mode_factor
//       + burst_p99_claims × (1 - Σ variant_weights) × pod_warmup_seconds /
//         burst_mode_factor
//     )
//
// When the inputs are insufficient (cold start: no historical demand
// data yet), Compute returns a Bootstrap result whose MinWarm is the
// pool's configured bootstrapMinWarm and the ScalingMode is "bootstrap"
// — see §4.6.2 "Cold-start limitation". The PoolScalingController
// exits bootstrap mode when the convergence criteria from §17.8.2 are
// met; the strategy itself only signals "input incomplete".
package strategy

import (
	"errors"
	"fmt"
	"math"
)

// ExecutionMode mirrors the pool's execution-mode enum from spec §5.2.
type ExecutionMode string

const (
	ModeSession    ExecutionMode = "session"
	ModeTask       ExecutionMode = "task"
	ModeConcurrent ExecutionMode = "concurrent"
)

// PoolType distinguishes a standard pool from a variant pool of an A/B
// experiment (§10.7). Standard pools may carry zero-or-more active
// variants; variant pools always belong to exactly one experiment.
type PoolType string

const (
	PoolStandard PoolType = "standard"
	PoolVariant  PoolType = "variant"
)

// ScalingMode is the operating mode of the returned decision.
type ScalingMode string

const (
	// ScalingNormal: the formula was applied against observed demand.
	ScalingNormal ScalingMode = "normal"
	// ScalingBootstrap: insufficient historical demand; the result is
	// the configured bootstrapMinWarm fallback.
	ScalingBootstrap ScalingMode = "bootstrap"
)

// ScalingInputs is the set of values that drive Compute. The fields are
// the inputs named directly in the §4.6.2 formula; the controller is
// responsible for populating them from Postgres / Prometheus.
type ScalingInputs struct {
	// PoolType is "standard" or "variant" (§10.7).
	PoolType PoolType

	// Mode is the pool's execution mode (§5.2).
	Mode ExecutionMode

	// BaseDemandP95 is the p95 claim arrival rate (claims/second) over
	// the operator-configured window. The controller derives this from
	// the metrics histogram.
	BaseDemandP95 float64

	// BurstP99Claims is the p99 claim arrival rate (claims/second) over
	// the short burst window (default 60s per §4.6.2).
	BurstP99Claims float64

	// SafetyFactor scales the steady-state term to provide a buffer
	// above the raw p95. Defaults per §4.6.2 / §17.8.2: 1.5 for
	// agent-type pools at Tier 1/2; 2.0 for mcp-type at Tier 1/2;
	// 1.2 / 1.5 at Tier 3. Must be > 0.
	SafetyFactor float64

	// FailoverSeconds is the worst-case controller failover time
	// (leaseDuration + renewDeadline). Default 25s per §4.6.2.
	FailoverSeconds float64

	// PodWarmupSeconds is the time from pod creation to ready state
	// for this pool. Pod-warm pools ≈ 10s; SDK-warm pools 30-90s.
	// Sourced from the pool's scalingPolicy.podWarmupSecondsBaseline.
	PodWarmupSeconds float64

	// ModeFactor is the pod-reuse multiplier (§5.2). 1.0 for session
	// mode, maxTasksPerPod (or observed reuse) for task, maxConcurrent
	// for concurrent. Zero is treated as 1.0 (defensive — the
	// controller should never pass 0).
	ModeFactor float64

	// BurstModeFactor is the burst-term reuse multiplier (§5.2). 1.0
	// for session and task; maxConcurrent for concurrent.
	BurstModeFactor float64

	// VariantWeight (variant pools only). Fraction of traffic routed to
	// the variant; 0 < w < 1.
	VariantWeight float64

	// SumActiveVariantWeights is Σ variant_weights for every active
	// variant on the base pool (standard pools only). 0 when no
	// variants are running. The controller is responsible for the
	// admission check that Σ variant_weights < 1; the strategy clamps
	// defensively in Compute.
	SumActiveVariantWeights float64

	// BootstrapMinWarm is the static fallback returned when the
	// strategy decides this pool is in cold start (no observed
	// demand). The controller seeds this from the pool's
	// bootstrapMinWarm field.
	BootstrapMinWarm int

	// HasObservedDemand is true once the metrics window has produced
	// at least one usable BaseDemandP95 sample. Until it flips to
	// true, Compute returns the bootstrap result.
	HasObservedDemand bool
}

// ScalingDecision is the strategy's output for one pool.
type ScalingDecision struct {
	// MinWarm is the target idle-pod count for this pool.
	MinWarm int

	// Mode reflects how MinWarm was produced — "normal" if the
	// formula was applied, "bootstrap" if the cold-start fallback
	// was returned.
	Mode ScalingMode

	// FormulaInputs captures the values the formula consumed so the
	// controller can emit them as observability fields. The fields
	// are the inputs verbatim plus the intermediate effectiveWeight
	// (1 - Σ variant_weights for standard pools with variants;
	// variant_weight for variant pools; 1.0 otherwise).
	FormulaInputs FormulaSnapshot
}

// FormulaSnapshot captures the inputs used in one formula application.
// It feeds the §4.6.2 reconciliation-lag metric and the structured-log
// trace for each scaling decision.
type FormulaSnapshot struct {
	BaseDemandP95          float64
	BurstP99Claims         float64
	SafetyFactor           float64
	FailoverSeconds        float64
	PodWarmupSeconds       float64
	ModeFactor             float64
	BurstModeFactor        float64
	EffectiveWeight        float64
}

// PoolScalingStrategy is the interface the §4.6.2 "Pluggable
// PoolScalingStrategy" contract names. Deployers may register a custom
// implementation; the platform ships DefaultStrategy.
type PoolScalingStrategy interface {
	Compute(inputs ScalingInputs) (ScalingDecision, error)
}

// ErrVariantWeightsExceedOne is returned by Compute when a standard
// pool's Σ variant_weights is ≥ 1, which would leave no traffic for
// the base pool (§4.6.2 says the controller rejects the experiment
// configuration with INVALID_VARIANT_WEIGHTS at admission time; the
// strategy surfaces the same condition here so unit tests can assert
// it).
var ErrVariantWeightsExceedOne = errors.New("sum of active variant_weights must be < 1")

// ErrInvalidInput covers all other validation failures (negative rates,
// zero safety factor, etc.).
var ErrInvalidInput = errors.New("invalid scaling input")

// DefaultStrategy implements the §4.6.2 default formula. Construct one
// with New().
type DefaultStrategy struct{}

// New returns a DefaultStrategy. The strategy carries no state; the
// constructor exists to match the Pluggable PoolScalingStrategy
// interface naming convention.
func New() *DefaultStrategy { return &DefaultStrategy{} }

// Compute applies the formula appropriate for inputs and returns the
// target minWarm.
func (s *DefaultStrategy) Compute(inputs ScalingInputs) (ScalingDecision, error) {
	if err := validate(inputs); err != nil {
		return ScalingDecision{}, err
	}
	if !inputs.HasObservedDemand {
		return ScalingDecision{
			MinWarm: inputs.BootstrapMinWarm,
			Mode:    ScalingBootstrap,
			FormulaInputs: FormulaSnapshot{
				SafetyFactor:     inputs.SafetyFactor,
				FailoverSeconds:  inputs.FailoverSeconds,
				PodWarmupSeconds: inputs.PodWarmupSeconds,
				ModeFactor:       defaultModeFactor(inputs.ModeFactor),
				BurstModeFactor:  defaultBurstModeFactor(inputs.BurstModeFactor),
				EffectiveWeight:  1,
			},
		}, nil
	}

	modeFactor := defaultModeFactor(inputs.ModeFactor)
	burstModeFactor := defaultBurstModeFactor(inputs.BurstModeFactor)

	weight, err := effectiveWeight(inputs)
	if err != nil {
		return ScalingDecision{}, err
	}

	steady := inputs.BaseDemandP95 * weight * inputs.SafetyFactor *
		(inputs.FailoverSeconds + inputs.PodWarmupSeconds) / modeFactor
	burst := inputs.BurstP99Claims * weight * inputs.PodWarmupSeconds / burstModeFactor

	return ScalingDecision{
		MinWarm: int(math.Ceil(steady + burst)),
		Mode:    ScalingNormal,
		FormulaInputs: FormulaSnapshot{
			BaseDemandP95:    inputs.BaseDemandP95,
			BurstP99Claims:   inputs.BurstP99Claims,
			SafetyFactor:     inputs.SafetyFactor,
			FailoverSeconds:  inputs.FailoverSeconds,
			PodWarmupSeconds: inputs.PodWarmupSeconds,
			ModeFactor:       modeFactor,
			BurstModeFactor:  burstModeFactor,
			EffectiveWeight:  weight,
		},
	}, nil
}

// effectiveWeight resolves the multiplicative weight applied to both
// terms: variant_weight for variant pools, (1 - Σ variant_weights) for
// standard pools when variants are active, otherwise 1.0.
func effectiveWeight(inputs ScalingInputs) (float64, error) {
	switch inputs.PoolType {
	case PoolVariant:
		if inputs.VariantWeight <= 0 || inputs.VariantWeight >= 1 {
			return 0, fmt.Errorf("%w: variant_weight must be in (0, 1), got %g", ErrInvalidInput, inputs.VariantWeight)
		}
		return inputs.VariantWeight, nil
	case PoolStandard:
		if inputs.SumActiveVariantWeights < 0 {
			return 0, fmt.Errorf("%w: sum_active_variant_weights must be >= 0, got %g", ErrInvalidInput, inputs.SumActiveVariantWeights)
		}
		if inputs.SumActiveVariantWeights >= 1 {
			return 0, ErrVariantWeightsExceedOne
		}
		return 1 - inputs.SumActiveVariantWeights, nil
	default:
		return 0, fmt.Errorf("%w: PoolType must be standard or variant", ErrInvalidInput)
	}
}

func validate(in ScalingInputs) error {
	if in.SafetyFactor <= 0 {
		return fmt.Errorf("%w: SafetyFactor must be > 0", ErrInvalidInput)
	}
	if in.FailoverSeconds < 0 {
		return fmt.Errorf("%w: FailoverSeconds must be >= 0", ErrInvalidInput)
	}
	if in.PodWarmupSeconds <= 0 {
		return fmt.Errorf("%w: PodWarmupSeconds must be > 0", ErrInvalidInput)
	}
	if in.HasObservedDemand {
		if in.BaseDemandP95 < 0 {
			return fmt.Errorf("%w: BaseDemandP95 must be >= 0", ErrInvalidInput)
		}
		if in.BurstP99Claims < 0 {
			return fmt.Errorf("%w: BurstP99Claims must be >= 0", ErrInvalidInput)
		}
	}
	if in.BootstrapMinWarm < 0 {
		return fmt.Errorf("%w: BootstrapMinWarm must be >= 0", ErrInvalidInput)
	}
	switch in.Mode {
	case ModeSession, ModeTask, ModeConcurrent, "":
	default:
		return fmt.Errorf("%w: Mode %q is not one of session, task, concurrent", ErrInvalidInput, in.Mode)
	}
	return nil
}

// defaultModeFactor returns 1.0 when the caller leaves ModeFactor zero.
// Session-mode pools omit the field; the formula divides by 1 there.
func defaultModeFactor(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}

func defaultBurstModeFactor(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}
