// SPDX-License-Identifier: MIT

package poolscaling

import (
	"fmt"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
)

// ActiveVariant describes one active variant of one active A/B
// experiment (§4.6.2, §10.7). The variant routes Weight of BasePool's
// claim traffic to VariantPool. A PoolConfigSource builds the slice of
// ActiveVariants from the experiment store, restricted to experiments
// and variants that are currently active, and passes it to
// ResolveVariantRoles together with the resolved pool definitions.
type ActiveVariant struct {
	// ExperimentID is the experiment the variant belongs to. It is
	// carried for diagnostics and error messages; ResolveVariantRoles
	// aggregates across experiments and does not key on it.
	ExperimentID string
	// BasePool is the standard pool the experiment diverts traffic from.
	BasePool string
	// VariantPool is the pool that receives the variant's traffic share.
	VariantPool string
	// Weight is the traffic fraction routed to VariantPool, in (0,1).
	Weight float64
}

// ResolveVariantRoles returns a copy of configs with each pool's
// PoolType, VariantWeight, and SumActiveVariantWeights set from the
// active experiment variants (§4.6.2 variant-pool sizing and base-pool
// adjustment). The input configs slice is not mutated.
//
// For every variant, the pool named by VariantPool becomes
// strategy.PoolVariant with VariantWeight set to the variant's Weight,
// and the pool named by BasePool accumulates Weight into its
// SumActiveVariantWeights. The base-pool sum is aggregated in a single
// pass across all variants of all experiments, so two experiments
// diverting traffic from the same base pool add their weights rather
// than last-write-wins overwriting (§4.6.2).
//
// An error wrapping strategy.ErrVariantWeightsExceedOne is returned
// when any base pool's aggregated Σ variant_weights is ≥ 1, which
// matches the INVALID_VARIANT_WEIGHTS admission rejection in §4.6.2: the
// base pool must retain a positive traffic fraction.
//
// Conservative handling of malformed input, all spec-consistent:
//
//   - A VariantPool that names a pool absent from configs is an error.
//     The variant pool's own SandboxWarmPool would never be sized, so
//     the experiment would route traffic to a pool with no warm-pod
//     floor. The configuration is rejected rather than silently dropped.
//   - A pool that is named as both a BasePool and a VariantPool is an
//     error. A pool cannot simultaneously be sized by the variant
//     formula (its own variant_weight) and the base-pool adjustment
//     (1 - Σ variant_weights); the two roles are mutually exclusive in
//     §4.6.2, so the ambiguous configuration is rejected.
//   - Two variants naming the same VariantPool is an error. A variant
//     pool belongs to exactly one experiment with one variant_weight
//     (§10.7); a second weight on the same pool is contradictory.
//   - A non-positive or ≥ 1 variant Weight is an error. variant_weight
//     is a traffic fraction in (0,1) per §4.6.2; the strategy rejects
//     these values too, but rejecting here keeps the failure attributable
//     to a named experiment.
//   - A BasePool that names a pool absent from configs is an error: the
//     base pool whose minWarm needs the (1 - Σ) adjustment is missing,
//     so the diversion cannot be applied.
func ResolveVariantRoles(configs []PoolConfig, variants []ActiveVariant) ([]PoolConfig, error) {
	out := make([]PoolConfig, len(configs))
	copy(out, configs)

	// index maps a pool name to its position in out for O(1) lookup.
	index := make(map[string]int, len(out))
	for i := range out {
		index[out[i].Name] = i
	}

	// variantOf records which experiment already claimed a pool as its
	// variant pool, and baseSum aggregates each base pool's Σ weights.
	variantOf := make(map[string]string)
	baseSum := make(map[string]float64)
	isBase := make(map[string]bool)

	for _, v := range variants {
		if v.Weight <= 0 || v.Weight >= 1 {
			return nil, fmt.Errorf("variant pool %q of experiment %q: weight must be in (0,1), got %g",
				v.VariantPool, v.ExperimentID, v.Weight)
		}
		vi, ok := index[v.VariantPool]
		if !ok {
			return nil, fmt.Errorf("variant pool %q of experiment %q: not found among pool configs",
				v.VariantPool, v.ExperimentID)
		}
		if _, ok := index[v.BasePool]; !ok {
			return nil, fmt.Errorf("base pool %q of experiment %q: not found among pool configs",
				v.BasePool, v.ExperimentID)
		}
		if prev, dup := variantOf[v.VariantPool]; dup {
			return nil, fmt.Errorf("variant pool %q is claimed by experiments %q and %q: a variant pool belongs to exactly one experiment",
				v.VariantPool, prev, v.ExperimentID)
		}
		variantOf[v.VariantPool] = v.ExperimentID

		out[vi].PoolType = strategy.PoolVariant
		out[vi].VariantWeight = v.Weight
		baseSum[v.BasePool] += v.Weight
		isBase[v.BasePool] = true
	}

	for name := range isBase {
		if variantOf[name] != "" {
			return nil, fmt.Errorf("pool %q is named as both a base pool and a variant pool: the roles are mutually exclusive",
				name)
		}
		sum := baseSum[name]
		if sum >= 1 {
			return nil, fmt.Errorf("base pool %q: Σ variant_weights = %g: %w", name, sum, strategy.ErrVariantWeightsExceedOne)
		}
		bi := index[name]
		out[bi].PoolType = strategy.PoolStandard
		out[bi].SumActiveVariantWeights = sum
	}

	return out, nil
}
