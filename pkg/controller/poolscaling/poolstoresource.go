// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"fmt"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
	"github.com/lennylabs/lenny/pkg/gateway/runtimestore"
)

// PoolStoreSource adapts the §5.2 poolstore.Store to the §4.6.2
// PoolConfigSource the PoolScalingController reconciles from. The admin
// API's Postgres pool definitions are the source of truth (§4.6.2
// "CRDs become derived state"); this source lists the active pool rows
// and maps each into the PoolConfig the controller upserts into its
// SandboxTemplate and SandboxWarmPool CRD pair.
//
// v1 has no separately-modeled per-pool minWarm/maxWarm: the store's
// warmCount is the operator-set bootstrap floor. Until the §4.6.2
// scaling formula converges on observed demand (which requires a
// DemandSource), the controller holds each pool at this bootstrap
// minWarm, so minWarm and maxWarm both map from warmCount.
type PoolStoreSource struct {
	// Store is the pool registry. List returns the active (non
	// soft-deleted) pool rows.
	Store poolstore.Store
	// Namespace is the agent namespace the derived CRD pair lives in.
	// The CRDs are derived state, so a single agent namespace per
	// deployment is the v1 assumption (§5.1 pools are platform-global;
	// their CRDs are materialized in the agent namespace).
	Namespace string
}

var _ PoolConfigSource = (*PoolStoreSource)(nil)

// ListPoolConfigs returns one PoolConfig per active pool, mapping the
// §5.2 store fields into the §5.2 SandboxTemplate spec and the §4.6.3
// PoolScalingController-owned SandboxWarmPool fields.
func (s *PoolStoreSource) ListPoolConfigs(ctx context.Context) ([]PoolConfig, error) {
	if s.Namespace == "" {
		return nil, fmt.Errorf("poolscaling: PoolStoreSource requires a target agent namespace")
	}
	pools, err := s.Store.List(ctx, poolstore.ListFilter{IncludeDeleted: false})
	if err != nil {
		return nil, fmt.Errorf("list pools: %w", err)
	}
	out := make([]PoolConfig, 0, len(pools))
	for _, p := range pools {
		out = append(out, s.toConfig(p))
	}
	return out, nil
}

// toConfig maps one store row into a PoolConfig. The whole
// SandboxTemplate spec is PoolScalingController-owned (§4.6.3), so every
// field the CRD models is copied. spec: §5.2 — the §5.2 sessionPolicy
// recycle block carries the cross-tenant microvm scrub controls
// (scrubProfile and acknowledgeMicrovmResidualState) the API server
// enforces as a fail-closed admission gate; the remaining recycle and
// concurrency knobs live in the gateway poolstore and reach the gateway
// directly rather than through the CRD.
func (s *PoolStoreSource) toConfig(p poolstore.Pool) PoolConfig {
	warm := int32(p.WarmCount)
	spec := lennyv1.SandboxTemplateSpec{
		RuntimeRef:           p.RuntimeRef,
		IsolationProfile:     string(p.IsolationProfile),
		EgressProfile:        string(p.EgressProfile),
		ResourceClass:        p.ResourceClass,
		ExecutionMode:        string(p.ExecutionMode),
		MaxConcurrent:        int32(p.MaxConcurrent),
		MaxSessionAgeSeconds: int64(p.MaxSessionAgeSeconds),
	}
	if sp := sessionPolicyToCRD(p.SessionPolicy); sp != nil {
		spec.SessionPolicy = sp
	}
	cfg := PoolConfig{
		Name:        p.Name,
		Namespace:   s.Namespace,
		Template:    spec,
		MinWarm:     warm,
		MaxWarm:     warm,
		Generation:  p.Generation,
		ResumeEpoch: p.ReconciliationResumeEpoch,
		// spec: §6.1 lines 48, 63-65 — carry the operator SDK-warm
		// circuit-breaker override, the demotion-rate-high acknowledgment,
		// and the deployer-configurable demotion-rate threshold so the
		// controller can apply them in its breaker decision and its
		// warning-event gate.
		SDKWarmCircuitBreakerOverride: p.SDKWarmCircuitBreakerOverride,
		AcknowledgeHighDemotionRate:   p.AcknowledgeHighDemotionRate,
		DemotionRateThreshold:         p.DemotionRateThreshold,
	}
	// spec: §17.8.2 — carry the bootstrapMinWarm override so the
	// controller can pin the pool to it (status.scalingMode: bootstrap)
	// until the convergence criteria are met. A nil column means no
	// override is in force.
	if p.BootstrapMinWarm != nil {
		v := *p.BootstrapMinWarm
		cfg.BootstrapMinWarm = &v
	}
	// spec: §5.2 / §6.33 — the CRD does not model the session-mode recycle
	// sizing knobs (they live in the gateway poolstore, §4.6.3 ownership), so
	// the controller's mode_factor derivation reads recycle.enabled,
	// recycle.maxSessionsPerPod, and maxConcurrentSessions directly off the
	// store row. A non-recycling pool keeps the one-session-per-pod base
	// sizing (mode_factor = 1.0); a recycling pool derives mode_factor from
	// observed reuse bounded above by maxSessionsPerPod.
	if sp := p.SessionPolicy; sp != nil {
		cfg.MaxConcurrentSessions = sp.MaxConcurrentSessions
		if r := sp.Recycle; r != nil {
			cfg.RecycleEnabled = r.Enabled
			cfg.MaxSessionsPerPod = r.MaxSessionsPerPod
		}
	}
	return cfg
}

// sessionPolicyToCRD renders the store sessionPolicy recycle block into the
// CRD SessionPolicy block. The CRD carries only the cross-tenant microvm
// scrub controls the API server enforces as a fail-closed admission gate
// (§4.6.3 ownership): the scrub profile and the in-place residual-state
// acknowledgment. The store's `MicrovmScrubMode` enum carried on
// `recycle.scrubProfile` matches the CRD `scrubProfile` enum value-for-value
// (`standard`, `vm-restart`, `in-place`), so it is passed through unchanged;
// the remaining recycle and concurrency knobs reach the gateway directly
// through the poolstore. A store row with no recycle scrub control returns
// nil, leaving the CRD on its default one-session-per-pod configuration.
// spec: §5.2 (recycle lifecycle, Kata scrub variant).
func sessionPolicyToCRD(sp *runtimestore.SessionPolicy) *lennyv1.SessionPolicy {
	if sp == nil || sp.Recycle == nil {
		return nil
	}
	r := sp.Recycle
	if r.ScrubProfile == "" && !r.AcknowledgeMicrovmResidualState {
		return nil
	}
	return &lennyv1.SessionPolicy{
		Recycle: &lennyv1.RecyclePolicy{
			ScrubProfile:                    string(r.ScrubProfile),
			AcknowledgeMicrovmResidualState: r.AcknowledgeMicrovmResidualState,
		},
	}
}
