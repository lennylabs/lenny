// SPDX-License-Identifier: MIT

// Package poolscaling holds the §4.6.2 PoolScalingController. The
// controller treats the admin API's Postgres pool definitions as the
// source of truth and reconciles them into the SandboxTemplate and
// SandboxWarmPool CRD pair the WarmPoolController consumes.
//
// This file holds the configuration sync: the PoolConfigSource
// abstraction over the pool definitions and the Sync pass that upserts
// each definition's CRD pair. The warm-pod floor is derived per pool
// by feeding the observed demand from a DemandSource and the pool's
// tuning inputs through the §4.6.2 scaling formula in the strategy
// subpackage; a pool with no observed demand stays at its bootstrap
// minWarm. Because the CRDs are derived state, Sync overwrites the
// full SandboxTemplate spec and the PoolScalingController-owned
// SandboxWarmPool spec fields (§4.6.3) on every pass, which also
// corrects any manual drift.
package poolscaling

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/controller/poolscaling/strategy"
)

// failoverSeconds is the §4.6.2 worst-case controller failover window
// (leaseDuration 15s + renewDeadline 10s).
const failoverSeconds = 25.0

// defaultSafetyFactor and defaultPodWarmupSeconds are applied when a
// PoolConfig leaves the corresponding input unset. The safety factor
// matches the §4.6.2 agent-type Tier 1/2 default; the warmup baseline
// matches the pod-warm container-pull-plus-startup figure.
const (
	defaultSafetyFactor     = 1.5
	defaultPodWarmupSeconds = 10.0
)

// PoolConfig is the desired configuration of one pool, resolved from
// the admin API's Postgres source of truth (§4.6.2). The
// PoolScalingController reconciles it into a SandboxTemplate and a
// SandboxWarmPool that share the pool name.
type PoolConfig struct {
	// Name is the pool name. The SandboxTemplate and SandboxWarmPool
	// are both created under it.
	Name string
	// Namespace is the agent namespace the pool's CRDs live in.
	Namespace string
	// Template is the desired SandboxTemplate spec. Per §4.6.3 the
	// PoolScalingController owns the whole SandboxTemplate spec.
	Template lennyv1.SandboxTemplateSpec
	// MinWarm is the bootstrap warm-pod floor: the operator-set value a
	// pool uses until the §4.6.2 scaling formula has converged on
	// observed demand. MaxWarm is the warm-pod ceiling. Both are owned
	// by the PoolScalingController per §4.6.3.
	MinWarm int32
	MaxWarm int32
	// SafetyFactor scales the steady-state term of the §4.6.2 formula.
	// A non-positive value is treated as the agent-type Tier 1/2
	// default.
	SafetyFactor float64
	// ScalePolicy carries the scaling-formula tuning inputs and
	// time-of-day overrides (§4.6.3 PoolScalingController-owned).
	ScalePolicy *lennyv1.ScalePolicy
	// SDKWarmDisabled is the SDK-warm circuit-breaker flag (§4.6.3
	// PoolScalingController-owned).
	SDKWarmDisabled bool
}

// Demand is the observed claim-rate signal for one pool — the input
// the §4.6.2 scaling formula consumes alongside the pool's static
// tuning.
type Demand struct {
	// BaseDemandP95 is the p95 claim arrival rate in claims per second.
	BaseDemandP95 float64
	// BurstP99Claims is the p99 claim arrival rate over the short burst
	// window, in claims per second.
	BurstP99Claims float64
	// Observed is true once the metrics window holds a usable sample.
	// While it is false the pool stays at its bootstrap minWarm.
	Observed bool
}

// DemandSource yields the observed demand signal for a pool. The
// production implementation reads the §16.1 claim-rate metrics from
// Prometheus. A PoolScalingController constructed without a
// DemandSource operates every pool in bootstrap mode.
type DemandSource interface {
	PoolDemand(ctx context.Context, poolName string) (Demand, error)
}

// PoolConfigSource yields the current set of pool definitions. The
// production implementation reads the admin API's Postgres tables; the
// controller treats whatever it returns as the desired state.
type PoolConfigSource interface {
	ListPoolConfigs(ctx context.Context) ([]PoolConfig, error)
}

// Reconciler is the §4.6.2 PoolScalingController. It syncs pool
// definitions from the PoolConfigSource into their CRD pair, deriving
// each pool's warm-pod floor from observed demand.
type Reconciler struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Source supplies the desired pool definitions.
	Source PoolConfigSource
	// Demand supplies the observed per-pool demand that drives the
	// scaling formula. When nil, every pool stays at its bootstrap
	// minWarm.
	Demand DemandSource
	// Strategy computes the warm-pod floor. When nil, the default
	// §4.6.2 formula is used.
	Strategy strategy.PoolScalingStrategy
}

// Sync performs one full reconciliation pass: every pool definition
// from the source is upserted into its SandboxTemplate and
// SandboxWarmPool CRD pair. A failure on one pool aborts the pass so
// the next tick retries; pools synced before the failure keep their
// applied state.
func (r *Reconciler) Sync(ctx context.Context) error {
	configs, err := r.Source.ListPoolConfigs(ctx)
	if err != nil {
		return fmt.Errorf("list pool configs: %w", err)
	}
	for i := range configs {
		cfg := configs[i]
		if err := r.syncTemplate(ctx, cfg); err != nil {
			return fmt.Errorf("sync template %s/%s: %w", cfg.Namespace, cfg.Name, err)
		}
		if err := r.syncWarmPool(ctx, cfg); err != nil {
			return fmt.Errorf("sync warm pool %s/%s: %w", cfg.Namespace, cfg.Name, err)
		}
	}
	return nil
}

// syncTemplate upserts the pool's SandboxTemplate. The whole spec is
// PoolScalingController-owned, so it is replaced wholesale.
func (r *Reconciler) syncTemplate(ctx context.Context, cfg PoolConfig) error {
	tmpl := &lennyv1.SandboxTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, tmpl, func() error {
		tmpl.Spec = cfg.Template
		return nil
	})
	return err
}

// syncWarmPool upserts the pool's SandboxWarmPool, writing only the
// spec fields §4.6.3 assigns to the PoolScalingController. minWarm is
// the formula-derived floor; the status subresource, including the
// WarmPoolController-owned counts, is not touched.
func (r *Reconciler) syncWarmPool(ctx context.Context, cfg PoolConfig) error {
	minWarm, err := r.targetMinWarm(ctx, cfg)
	if err != nil {
		return err
	}
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}
	_, err = controllerutil.CreateOrUpdate(ctx, r.Client, pool, func() error {
		pool.Spec.TemplateRef = cfg.Name
		pool.Spec.MinWarm = minWarm
		pool.Spec.MaxWarm = cfg.MaxWarm
		pool.Spec.ScalePolicy = cfg.ScalePolicy
		pool.Spec.SDKWarmDisabled = cfg.SDKWarmDisabled
		return nil
	})
	return err
}

// targetMinWarm derives the warm-pod floor for one pool by running the
// §4.6.2 scaling formula. A pool with no DemandSource, or one whose
// demand has not yet converged, stays at its bootstrap minWarm.
func (r *Reconciler) targetMinWarm(ctx context.Context, cfg PoolConfig) (int32, error) {
	in := strategy.ScalingInputs{
		PoolType:         strategy.PoolStandard,
		Mode:             strategy.ExecutionMode(cfg.Template.ExecutionMode),
		SafetyFactor:     cfg.SafetyFactor,
		FailoverSeconds:  failoverSeconds,
		PodWarmupSeconds: podWarmupSeconds(cfg),
		BootstrapMinWarm: int(cfg.MinWarm),
	}
	if in.SafetyFactor <= 0 {
		in.SafetyFactor = defaultSafetyFactor
	}
	if r.Demand != nil {
		d, err := r.Demand.PoolDemand(ctx, cfg.Name)
		if err != nil {
			return 0, fmt.Errorf("read demand: %w", err)
		}
		in.BaseDemandP95 = d.BaseDemandP95
		in.BurstP99Claims = d.BurstP99Claims
		in.HasObservedDemand = d.Observed
	}

	strat := r.Strategy
	if strat == nil {
		strat = strategy.New()
	}
	decision, err := strat.Compute(in)
	if err != nil {
		return 0, fmt.Errorf("compute minWarm: %w", err)
	}
	return int32(decision.MinWarm), nil
}

// podWarmupSeconds resolves the pod creation-to-ready time the scaling
// formula uses, from the pool's scalePolicy, defaulting to the
// pod-warm baseline.
func podWarmupSeconds(cfg PoolConfig) float64 {
	if cfg.ScalePolicy != nil && cfg.ScalePolicy.PodWarmupSecondsBaseline > 0 {
		return float64(cfg.ScalePolicy.PodWarmupSecondsBaseline)
	}
	return defaultPodWarmupSeconds
}
