// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"fmt"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/gateway/poolstore"
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
// field the store models is copied. spec: §5.2 — the §5.2 task-mode
// taskPolicy block and §5.2 concurrent-workspace
// concurrentWorkspacePolicy block are populated from the corresponding
// store fields so the SandboxTemplate carries the full mode-specific
// configuration the pool-config validation webhook expects.
func (s *PoolStoreSource) toConfig(p poolstore.Pool) PoolConfig {
	warm := int32(p.WarmCount)
	spec := lennyv1.SandboxTemplateSpec{
		RuntimeRef:           p.RuntimeRef,
		IsolationProfile:     string(p.IsolationProfile),
		EgressProfile:        string(p.EgressProfile),
		ResourceClass:        p.ResourceClass,
		ExecutionMode:        string(p.ExecutionMode),
		ConcurrencyStyle:     string(p.ConcurrencyStyle),
		MaxConcurrent:        int32(p.MaxConcurrent),
		MaxSessionAgeSeconds: int64(p.MaxSessionAgeSeconds),
	}
	if p.TaskPolicy != nil || p.AllowCrossTenantReuse {
		spec.TaskPolicy = taskPolicyToCRD(p.TaskPolicy, p.AllowCrossTenantReuse)
	}
	if p.ConcurrencyStyle == poolstore.ConcurrencyStyleWorkspace {
		spec.ConcurrentWorkspacePolicy = &lennyv1.ConcurrentWorkspacePolicy{
			AcknowledgeProcessLevelIsolation: p.AcknowledgeProcessLevelIsolation,
			CleanupTimeoutSeconds:            int64(p.CleanupTimeoutSeconds),
		}
	}
	cfg := PoolConfig{
		Name:        p.Name,
		Namespace:   s.Namespace,
		Template:    spec,
		MinWarm:     warm,
		MaxWarm:     warm,
		Generation:  p.Generation,
		ResumeEpoch: p.ReconciliationResumeEpoch,
	}
	// spec: §17.8.2 — carry the bootstrapMinWarm override so the
	// controller can pin the pool to it (status.scalingMode: bootstrap)
	// until the convergence criteria are met. A nil column means no
	// override is in force.
	if p.BootstrapMinWarm != nil {
		v := *p.BootstrapMinWarm
		cfg.BootstrapMinWarm = &v
	}
	return cfg
}

// taskPolicyToCRD renders a store TaskPolicy into the CRD shape. The
// pool-level AllowCrossTenantReuse flag lives at the Pool top level in
// the store (legacy v1 admin contract) but at the CRD's TaskPolicy
// level in §5.2 spec yaml, so the value is folded in here. A nil store
// policy with allowCrossTenantReuse=true still produces a CRD
// TaskPolicy carrying the flag — the webhook then rejects it for
// missing acknowledgeBestEffortScrub / maxTasksPerPod, surfacing the
// configuration error to the operator. spec: §5.2 lines 398-475.
func taskPolicyToCRD(tp *poolstore.TaskPolicy, allowCrossTenantReuse bool) *lennyv1.TaskPolicy {
	out := &lennyv1.TaskPolicy{AllowCrossTenantReuse: allowCrossTenantReuse}
	if tp != nil {
		out.AcknowledgeBestEffortScrub = tp.AcknowledgeBestEffortScrub
		out.MicrovmScrubMode = string(tp.MicrovmScrubMode)
		out.AcknowledgeMicrovmResidualState = tp.AcknowledgeMicrovmResidualState
		out.CleanupCommands = append([]string(nil), tp.CleanupCommands...)
		out.CleanupTimeoutSeconds = int64(tp.CleanupTimeoutSeconds)
		out.OnCleanupFailure = string(tp.OnCleanupFailure)
		if tp.MaxScrubFailures > 0 {
			n := int32(tp.MaxScrubFailures)
			out.MaxScrubFailures = &n
		}
		out.MaxTasksPerPod = int32(tp.MaxTasksPerPod)
		if tp.MaxPodUptimeSeconds > 0 {
			n := int64(tp.MaxPodUptimeSeconds)
			out.MaxPodUptimeSeconds = &n
		}
		if tp.MaxTaskRetries != nil {
			n := int32(*tp.MaxTaskRetries)
			out.MaxTaskRetries = &n
		}
	}
	return out
}
