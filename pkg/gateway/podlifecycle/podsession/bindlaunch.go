// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
)

// Launch runs the §4.4 start-time launch against the prepared pod. It
// reconnects to req.SandboxName from the persisted binding, then either
// starts the runtime from cold (StartSession, for a pod-warm or demoted
// pod) or points the pre-connected SDK at the finalized workspace
// (ConfigureWorkspace, for a still-SDK-warm pod), and verifies the §5.1
// observed integration level. On success the caller owns the returned live
// adapter connection. A launch failure reclaims the pod (and any lease
// assigned at Prepare) via failPhase and is returned so the gateway retries
// on a fresh pod. spec: §4.4 (proposal), §6.1, §5.1.
//
// Launch mints no §4.7.1 bind attempt token: neither StartSession nor
// ConfigureWorkspace carries one, and the start confirms instead that the
// entry it claimed is still the one it was admitted against (§4.7.1 rule 8,
// the start-confirmation rule).
func (b *Binder) Launch(ctx context.Context, req BindRequest) (*BindResult, error) {
	sb, cl, neg, err := b.reconnect(ctx, req)
	if err != nil {
		// A reconnect failure (dial/handshake against the bound pod fails
		// transiently between /finalize and /start) strands the pod claimed at
		// /create AND the §4.9 lease Prepare assigned, since by Launch the
		// finalize block has always assigned it. Reclaim from the persisted
		// binding so the monolith invariant — any post-claim failure reclaims
		// the pod, and a post-AssignCredentials failure revokes the lease
		// (Gap 2) — holds before the first launch RPC runs. ReclaimClaimed
		// revokes the lease (keyed by sessionID) and deletes the per-pod claim.
		// spec: §4.6 (proposal), §7.1 step 23 (lease release).
		if rerr := b.ReclaimClaimed(ctx, req.SandboxName, req.SessionID); rerr != nil {
			log.Printf("podsession: reclaim claimed pod %s after Launch reconnect failure for session %s: %v", req.SandboxName, req.SessionID, rerr)
		}
		return nil, err
	}
	sandboxName := sb.Name
	// A launch failure reclaims the pod and the lease assigned at Prepare:
	// by Launch the finalize block has always assigned the lease, so the
	// reclaim revokes it (Gap 2). spec: §7.1 step 23 (lease release).
	// Launch issues ConfigureWorkspace or StartSession, neither of which
	// carries a bind attempt token, so the only refusal it can meet is the
	// already-started one. spec: §4.7.1 (role and gateway RPC contract).
	reclaim := func(cause error) {
		b.reclaimOnFailure(ctx, sb, cl, true, req.SessionID, cause)
	}

	phaseStart := time.Now()
	// spec: §6.1 — a still-SDK-warm pod (preConnect, not
	// demoted) is started by pointing the pre-connected SDK at the
	// finalized workspace (ConfigureWorkspace) rather than booting the
	// runtime from cold (StartSession). A demoted or pod-warm pod uses
	// StartSession.
	if req.PreConnect && !req.Demoted {
		if err := cl.ConfigureWorkspace(ctx, req.SessionID, slotlayout.SessionCurrentDir(neg.WorkspaceBase, req.SessionID), req.ExperimentContext, req.TracingContext); err != nil {
			reclaim(err)
			return nil, fmt.Errorf("podsession: configure SDK-warm workspace on pod %s: %w", sandboxName, err)
		}
	} else if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		reclaim(err)
		return nil, fmt.Errorf("podsession: start session on pod %s: %w", sandboxName, err)
	}
	// spec: §5.1 — the runtime has now booted, so the adapter
	// has had its first lifecycle_capabilities/lifecycle_support exchange.
	// On the first assignment to this runtime, compare the observed level
	// against the declared integrationLevel and reject the assignment with
	// RUNTIME_LEVEL_UNDERPERFORMS when the runtime delivers less than it
	// declares. An underperforming runtime fails before the session is
	// reported running, and the pod is reclaimed by draining it.
	if err := b.verifyIntegrationLevel(ctx, cl, req.Runtime, req.DeclaredIntegrationLevel); err != nil {
		reclaim(err)
		return nil, err
	}
	// spec: §6.2 — the session reaching `running` is a session-model state
	// recorded on the Postgres session row; the pod stays in the coarse
	// `claimed` phase. No CRD phase write happens here.
	var t BindTimings
	t.AgentSessionStart = time.Since(phaseStart)

	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       sb.Status.PodIP,
		Recycle:     req.Recycle,
		// spec: §5.2 (whole-pod scrub trigger) — carry the pool's scrub
		// parameters resolved at bind time so the recycle-path Shutdown
		// delivers them to the adapter without re-resolving the pool.
		CleanupCommands:       req.CleanupCommands,
		CleanupTimeoutSeconds: req.CleanupTimeoutSeconds,
		Adapter:               cl,
		Timings:               t,
		WorkspaceBase:         neg.WorkspaceBase,
	}, nil
}
