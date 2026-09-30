// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/poolstore"
)

// poolPolicyReader adapts the §5.2 poolstore onto the
// podsession.PoolPolicyReader the CRD resolver folds the gateway-enforced
// sessionPolicy mirror through. It returns nil when no pool store is
// wired, so ResolvePool keeps its CRD-derived dispatch defaults.
func (s *Server) poolPolicyReader() podsession.PoolPolicyReader {
	if s.pools == nil {
		return nil
	}
	return poolPolicyMirror{pools: s.pools}
}

// NewPoolPolicyReader adapts a §5.2 poolstore onto the
// podsession.PoolPolicyReader the CRD resolver and the checkpoint driver
// read the gateway-enforced sessionPolicy mirror through. It returns nil
// when no pool store is wired so callers keep their CRD-derived defaults.
// The gateway wiring shares one reader between ResolvePool and the
// Checkpointer's per-pool checkpointGrantWindow lookup.
//
// spec: §5.2 (sessionPolicy block, gateway-enforced subset); §10.1
// (per-pool checkpointGrantWindow override).
func NewPoolPolicyReader(pools poolstore.Store) podsession.PoolPolicyReader {
	if pools == nil {
		return nil
	}
	return poolPolicyMirror{pools: pools}
}

// poolPolicyMirror reads a pool's gateway-enforced §5.2 sessionPolicy
// fields (maxConcurrentSessions, the service-mode maxConcurrent,
// recycle.allowCrossTenantReuse, and recycle.maxPodUptimeSeconds) from
// the poolstore so ResolvePool can fold them into the PoolMatch. The CRD
// pair does not carry these; the poolstore is the source of truth.
//
// spec: §5.2 (sessionPolicy block, gateway-enforced subset).
type poolPolicyMirror struct {
	pools poolstore.Store
}

// PoolPolicy implements podsession.PoolPolicyReader. found is false for a
// missing or soft-deleted pool, leaving the CRD-derived dispatch fields
// unchanged.
func (m poolPolicyMirror) PoolPolicy(ctx context.Context, name string) (podsession.PoolPolicyMirror, bool, error) {
	p, err := m.pools.Get(ctx, name)
	if err != nil {
		if errors.Is(err, poolstore.ErrNotFound) {
			return podsession.PoolPolicyMirror{}, false, nil
		}
		return podsession.PoolPolicyMirror{}, false, fmt.Errorf("sessionserver: get pool %s: %w", name, err)
	}
	mirror := podsession.PoolPolicyMirror{MaxConcurrent: int32(p.MaxConcurrent)}
	// spec: §10.1.7 / §5.2 — surface the per-pool
	// checkpointGrantWindow override so the checkpoint driver's per-pool
	// lookup reads the gateway-enforced value; nil leaves the driver on the
	// deployment-wide default.
	if p.CheckpointGrantWindow != nil {
		w := int32(*p.CheckpointGrantWindow)
		mirror.CheckpointGrantWindow = &w
	}
	if sp := p.SessionPolicy; sp != nil {
		mirror.MaxConcurrentSessions = int32(sp.MaxConcurrentSessions)
		// §5.2 / §4.6.1 pool-exhaustion disposition: fold the queue-vs-reject
		// choice and its wait bound from the session policy so the start
		// path's claim queue reads the gateway-enforced values.
		mirror.OnPoolExhausted = string(sp.OnPoolExhausted)
		mirror.MaxQueueWaitSeconds = sp.MaxQueueWaitSeconds
		// §5.2 whole-pod scrub trigger: the deployer cleanup commands and their
		// aggregate cap live on the poolstore sessionPolicy, not the CRD pair,
		// so surface them on the mirror for foldPoolPolicy to copy onto
		// PoolMatch. The recycle-path Shutdown delivers them to the adapter.
		mirror.CleanupCommands = sp.CleanupCommands
		mirror.CleanupTimeoutSeconds = sp.CleanupTimeoutSeconds
		if r := sp.Recycle; r != nil {
			// spec: §5.2 (Recycle lifecycle) — recycle.enabled itself is
			// gateway-enforced and poolstore-sourced for every scrub
			// profile; the CRD pair carries it only implicitly for the
			// microvm variants (a non-empty scrubProfile). Without this,
			// a standard-profile recycling pool's pod is always retired
			// at release (BindResult.Recycle never true), so §5.2
			// sequential pod reuse never triggers for the common case.
			mirror.Recycle = r.Enabled
			mirror.AllowCrossTenantReuse = r.AllowCrossTenantReuse
			mirror.MaxPodUptimeSeconds = int64(r.MaxPodUptimeSeconds)
		}
	}
	return mirror, true, nil
}
