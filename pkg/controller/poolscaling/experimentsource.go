// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
	"github.com/lennylabs/lenny/pkg/experiment"
	"github.com/lennylabs/lenny/pkg/gateway/experimentstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// ExperimentReader yields every §10.7 experiment definition across all
// tenants. The PoolScalingController is platform-global, so it reads the
// per-tenant registry through this cross-tenant accessor (the Postgres
// implementation uses the §4.2 platform-admin path). Both
// experimentstore.Memory and the Postgres store satisfy it.
type ExperimentReader interface {
	ListAll(ctx context.Context) ([]experimentstore.Experiment, error)
}

// BasePoolResolver maps an experiment's base runtime to the name of the
// base pool the control group falls through to (§10.7 line 703). The
// §4.6.2 base-pool adjustment reduces that pool's minWarm by the active
// variants' aggregate weight. A resolver that cannot identify a unique
// base pool returns ("", false): the variant pool is still sized by the
// variant formula, but the base-pool adjustment is skipped for that
// experiment rather than guessing the wrong pool.
type BasePoolResolver interface {
	ResolveBasePool(ctx context.Context, baseRuntime string) (string, bool)
}

// ExperimentVariantSource decorates a base PoolConfigSource (typically a
// PoolStoreSource over the §5.2 pool registry) with the §10.7 experiment
// variant-pool lifecycle. It reads every experiment across tenants and
// rewrites the affected pools' PoolConfig entries by experiment status:
//
//   - active:    the variant pool is sized by the §4.6.2 variant formula
//     (PoolType=variant, VariantWeight set) and its base pool's minWarm
//     is reduced by Σ active variant weights.
//   - paused:    the variant pool's minWarm is pinned to 0 (existing warm
//     pods drain naturally; the CRD is retained for re-activation).
//   - concluded: the variant pool is marked DrainAndDelete, draining to
//     0/0 and deleting its SandboxWarmPool CRD once readyCount hits 0.
//
// Base-pool restoration on pause/conclude is automatic: a paused or
// concluded variant's weight is not summed into its base pool, so the
// base pool's (1 - Σ variant_weights) factor recovers without any extra
// bookkeeping. spec: §10.7 lines 1092, 1102-1104; §4.6.2 lines 534-547.
type ExperimentVariantSource struct {
	// Inner supplies the base set of pool definitions (every §5.2 pool
	// as a standard pool).
	Inner PoolConfigSource
	// Experiments reads the cross-tenant experiment registry. When nil,
	// the source is a transparent pass-through over Inner.
	Experiments ExperimentReader
	// BasePools resolves an experiment's base pool for the §4.6.2
	// base-pool adjustment. When nil, every base-pool adjustment is
	// skipped and only the variant pools are resized.
	BasePools BasePoolResolver
}

var _ PoolConfigSource = (*ExperimentVariantSource)(nil)

// ListPoolConfigs returns the base pool set with the §10.7 variant
// lifecycle applied. A failure to read the experiment registry is
// returned so the controller retries on the next tick; a malformed set
// of active experiments (rejected by ResolveVariantRoles) is logged and
// the un-adjusted base set is returned so one bad experiment never wedges
// reconciliation of every pool.
func (s *ExperimentVariantSource) ListPoolConfigs(ctx context.Context) ([]PoolConfig, error) {
	base, err := s.Inner.ListPoolConfigs(ctx)
	if err != nil {
		return nil, err
	}
	if s.Experiments == nil {
		return base, nil
	}
	exps, err := s.Experiments.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("list experiments: %w", err)
	}

	// index maps a pool name to its position in base for O(1) lookup.
	// ResolveVariantRoles preserves order, so the index stays valid
	// against the returned slice as well.
	index := make(map[string]int, len(base))
	for i := range base {
		index[base[i].Name] = i
	}

	// Partition the active variants into those whose base pool resolves
	// to a managed pool (eligible for ResolveVariantRoles' base-pool
	// adjustment) and the rest (variant pool sized directly, no base
	// adjustment). A variant whose own pool is not a managed pool is
	// dropped: the controller cannot size a SandboxWarmPool it does not
	// own.
	var resolvable, orphan []ActiveVariant
	for _, e := range exps {
		if e.Status != experiment.StatusActive {
			continue
		}
		basePool := ""
		if s.BasePools != nil {
			if bp, ok := s.BasePools.ResolveBasePool(ctx, e.BaseRuntime); ok {
				basePool = bp
			}
		}
		for _, v := range e.Variants {
			if v.Pool == "" {
				continue
			}
			if _, ok := index[v.Pool]; !ok {
				continue
			}
			av := ActiveVariant{
				ExperimentID:   e.ID,
				BasePool:       basePool,
				VariantPool:    v.Pool,
				Weight:         v.Weight,
				InitialMinWarm: int32(v.InitialMinWarm),
			}
			if basePool != "" {
				if _, ok := index[basePool]; ok {
					resolvable = append(resolvable, av)
					continue
				}
			}
			orphan = append(orphan, av)
		}
	}

	configs := base
	if len(resolvable) > 0 {
		adjusted, err := ResolveVariantRoles(base, resolvable)
		if err != nil {
			log.FromContext(ctx).WithName("poolscaling").Error(err,
				"active experiment variants rejected; reconciling pools without variant adjustment")
		} else {
			configs = adjusted
		}
	}

	// Apply the orphan (base-unresolved) active variants directly: size
	// the variant pool by the variant formula but skip base adjustment.
	for _, av := range orphan {
		i := index[av.VariantPool]
		configs[i].PoolType = strategy.PoolVariant
		configs[i].VariantWeight = av.Weight
		if av.InitialMinWarm > 0 {
			configs[i].MinWarm = av.InitialMinWarm
		}
	}

	// claimed tracks pools already assigned a lifecycle role so the
	// status precedence (active > paused > concluded) holds when the same
	// pool is referenced by experiments in different states.
	claimed := make(map[string]bool, len(resolvable)+len(orphan))
	for _, av := range resolvable {
		claimed[av.VariantPool] = true
	}
	for _, av := range orphan {
		claimed[av.VariantPool] = true
	}

	// spec: §10.7 line 1102 — a paused variant pool pins minWarm to 0
	// while leaving maxWarm at its current ceiling so warm pods drain.
	for _, e := range exps {
		if e.Status != experiment.StatusPaused {
			continue
		}
		for _, v := range e.Variants {
			i, ok := index[v.Pool]
			if !ok || claimed[v.Pool] {
				continue
			}
			configs[i].ForceZeroMinWarm = true
			claimed[v.Pool] = true
		}
	}

	// spec: §10.7 line 1104 — a concluded variant pool drains to 0/0 and
	// its SandboxWarmPool is deleted once readyCount reaches 0.
	for _, e := range exps {
		if e.Status != experiment.StatusConcluded {
			continue
		}
		for _, v := range e.Variants {
			i, ok := index[v.Pool]
			if !ok || claimed[v.Pool] {
				continue
			}
			configs[i].DrainAndDelete = true
			claimed[v.Pool] = true
		}
	}

	return configs, nil
}

// PoolStoreBasePoolResolver resolves the §10.7 base pool for an
// experiment's base runtime by listing the §5.2 pools that warm that
// runtime. It resolves only when exactly one pool warms the base runtime
// (the unambiguous common case); zero or multiple candidates yield no
// resolution, so the base-pool adjustment is conservatively skipped
// rather than reducing the wrong pool's minWarm. spec: §10.7 line 703 —
// the control group falls through to the base runtime's default pool.
type PoolStoreBasePoolResolver struct {
	// Store is the §5.2 pool registry.
	Store poolstore.Store
}

var _ BasePoolResolver = PoolStoreBasePoolResolver{}

// ResolveBasePool returns the name of the sole non-deleted pool warming
// baseRuntime, or ("", false) when the base pool is absent or ambiguous.
func (r PoolStoreBasePoolResolver) ResolveBasePool(ctx context.Context, baseRuntime string) (string, bool) {
	if r.Store == nil || baseRuntime == "" {
		return "", false
	}
	pools, err := r.Store.List(ctx, poolstore.ListFilter{RuntimeRef: baseRuntime, IncludeDeleted: false})
	if err != nil || len(pools) != 1 {
		return "", false
	}
	return pools[0].Name, true
}
