// SPDX-License-Identifier: MIT

// Package poolscaling holds the §4.6.2 PoolScalingController. The
// controller treats the admin API's Postgres pool definitions as the
// source of truth and reconciles them into the SandboxTemplate and
// SandboxWarmPool CRD pair the WarmPoolController consumes.
//
// The demand-driven scaling formula that derives minWarm lives in the
// strategy subpackage. This file holds the configuration sync: the
// PoolConfigSource abstraction over the pool definitions and the Sync
// pass that upserts each definition's CRD pair. Because the CRDs are
// derived state, Sync overwrites the full SandboxTemplate spec and the
// PoolScalingController-owned SandboxWarmPool spec fields (§4.6.3) on
// every pass, which also corrects any manual drift.
package poolscaling

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
	// MinWarm and MaxWarm are the warm-pod bounds, owned by the
	// PoolScalingController per §4.6.3.
	MinWarm int32
	MaxWarm int32
	// ScalePolicy carries the scaling-formula tuning inputs and
	// time-of-day overrides (§4.6.3 PoolScalingController-owned).
	ScalePolicy *lennyv1.ScalePolicy
	// SDKWarmDisabled is the SDK-warm circuit-breaker flag (§4.6.3
	// PoolScalingController-owned).
	SDKWarmDisabled bool
}

// PoolConfigSource yields the current set of pool definitions. The
// production implementation reads the admin API's Postgres tables; the
// controller treats whatever it returns as the desired state.
type PoolConfigSource interface {
	ListPoolConfigs(ctx context.Context) ([]PoolConfig, error)
}

// Reconciler is the §4.6.2 PoolScalingController. It syncs pool
// definitions from the PoolConfigSource into their CRD pair.
type Reconciler struct {
	// Client is the controller-runtime client.
	Client client.Client
	// Source supplies the desired pool definitions.
	Source PoolConfigSource
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
// spec fields §4.6.3 assigns to the PoolScalingController. The status
// subresource, including the WarmPoolController-owned counts, is not
// touched.
func (r *Reconciler) syncWarmPool(ctx context.Context, cfg PoolConfig) error {
	pool := &lennyv1.SandboxWarmPool{
		ObjectMeta: metav1.ObjectMeta{Name: cfg.Name, Namespace: cfg.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pool, func() error {
		pool.Spec.TemplateRef = cfg.Name
		pool.Spec.MinWarm = cfg.MinWarm
		pool.Spec.MaxWarm = cfg.MaxWarm
		pool.Spec.ScalePolicy = cfg.ScalePolicy
		pool.Spec.SDKWarmDisabled = cfg.SDKWarmDisabled
		return nil
	})
	return err
}
