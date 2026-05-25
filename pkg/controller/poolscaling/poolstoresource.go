// SPDX-License-Identifier: MIT

package poolscaling

import (
	"context"
	"fmt"

	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
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
// field the store models is copied; fields the v1 store does not model
// (taskPolicy, concurrentWorkspacePolicy) are left unset, and a pool
// whose mode requires them is rejected by the pool-config validator at
// admission, which the controller's per-tuple backoff handles.
func (s *PoolStoreSource) toConfig(p poolstore.Pool) PoolConfig {
	warm := int32(p.WarmCount)
	return PoolConfig{
		Name:      p.Name,
		Namespace: s.Namespace,
		Template: lennyv1.SandboxTemplateSpec{
			RuntimeRef:           p.RuntimeRef,
			IsolationProfile:     string(p.IsolationProfile),
			ResourceClass:        p.ResourceClass,
			ExecutionMode:        string(p.ExecutionMode),
			ConcurrencyStyle:     string(p.ConcurrencyStyle),
			MaxConcurrent:        int32(p.MaxConcurrent),
			MaxSessionAgeSeconds: int64(p.MaxSessionAgeSeconds),
		},
		MinWarm: warm,
		MaxWarm: warm,
	}
}
