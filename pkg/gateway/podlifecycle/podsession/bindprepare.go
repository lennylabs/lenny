// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/sdkwarm"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
)

// PrepareResult reports the outcome of the §4.3 finalize-time preparation
// barrier so the finalize handler can persist the workspace root, the
// captured setup outputs, and the §7.4 strip-skip advisories, and emit the
// per-phase §6.3 timings. The adapter connection Prepare opened is closed
// before Prepare returns; Launch reconnects from the binding (§4.6).
// spec: §4.3 (proposal), §6.3.
type PrepareResult struct {
	// WorkspaceBase is the §6.4 workspace base the adapter reported when
	// Prepare reconnected, verbatim. The session row records the root
	// derived from it for a later Resume.
	WorkspaceBase string
	// Demoted reports whether the §6.1 SDK-warm pod was demoted to pod-warm
	// during Prepare. Launch reads it to decide StartSession vs.
	// ConfigureWorkspace without re-running the blocking-path match.
	Demoted bool
	// WorkspacePlanWarnings carries the §7.4 strip-skip advisories
	// the gateway and adapter raised during materialization, for the
	// finalize handler to republish on the §7.2 SSE stream.
	WorkspacePlanWarnings []*adapterv1.WorkspacePlanWarning
	// SetupOutputs is the §7.5 captured per-command transcript, persisted on
	// the session row and surfaced through §15.1 and the §11.7 audit log.
	SetupOutputs []*adapterv1.SetupCommandOutput
	// Timings carries the §6.3 workspace-materialization, setup-commands,
	// and credential-assignment phase durations Prepare measured.
	Timings BindTimings
}

// Prepare runs the §4.3 finalize-time preparation barrier against the pod
// claimed at create. It reconnects to req.SandboxName from the persisted
// binding (resolve the Sandbox, dial the adapter, re-run the §15.5 version
// handshake), then runs the §4.7 setup chain: the §6.1 SDK-warm demotion
// decision (now made here because it depends on the materialized plan),
// stageWorkspace (PrepareWorkspace), FinalizeWorkspace, RunSetup, and
// assignCredentials. On any step failure the pod and any assigned lease are
// reclaimed via failPhase and the corresponding error is returned. The
// adapter connection is closed before Prepare returns; Launch reconnects.
// spec: §4.3 (proposal), §6.1, §7.4, §4.9; §6.3.
func (b *Binder) Prepare(ctx context.Context, req BindRequest) (*PrepareResult, error) {
	sb, cl, neg, err := b.reconnect(ctx, req)
	if err != nil {
		// A reconnect failure (resolve/dial/handshake against the bound pod
		// fails transiently between /create and /finalize) strands the pod
		// claimed at /create with no live BindResult covering it. Reclaim it
		// from the persisted binding so the monolith invariant — any post-claim
		// failure reclaims the pod — holds for the Bind composition. No lease is
		// assigned before Prepare runs, so the ReclaimClaimed lease revoke is a
		// no-op here. spec: §4.6 (proposal), §6.2 (claimed → draining).
		if rerr := b.ReclaimClaimed(ctx, req.SandboxName, req.SessionID); rerr != nil {
			log.Printf("podsession: reclaim claimed pod %s after Prepare reconnect failure for session %s: %v", req.SandboxName, req.SessionID, rerr)
		}
		return nil, err
	}
	sandboxName := sb.Name
	// spec: §4.7.1 (role and gateway RPC contract) — Prepare is one bind
	// attempt. It mints its token before its first entry-touching RPC and
	// carries it on PrepareWorkspace, FinalizeWorkspace, RunSetup, and
	// AssignCredentials, each with mid_session false. DemoteSDK carries none.
	bindAttempt := newBindAttempt()

	var t BindTimings
	// leaseAssigned tracks whether assignCredentials issued a lease in this
	// phase, so a failure in a LATER step reclaims the lease as well as the
	// pod (Gap 2): a finalize-block credential assignment must not leak the
	// lease back to the §4.9 pool when a subsequent step aborts.
	leaseAssigned := false
	// Each call site passes the error it is about to return as cause, before
	// any wrap into *SetupCommandFailure or *SDKDemotionNotSupported, so the
	// refusal check reads the raw adapter error.
	reclaim := func(cause error) {
		b.reclaimOnFailure(ctx, sb, cl, leaseAssigned, req.SessionID, cause)
	}

	// spec: §6.1 — on an SDK-warm (preConnect) pod, decide
	// whether the workspace plan forces a demotion before the workspace is
	// materialized. A blocking-path match tears down the pre-connected SDK
	// (DemoteSDK) and the pod proceeds via the normal pod-warm StartSession
	// path; no match keeps the pod SDK-warm and the launch points the SDK at
	// the finalized workspace (ConfigureWorkspace) instead. The decision lives
	// in Prepare because it depends on the materialized plan (§4.3).
	demoted := false
	if req.PreConnect {
		if mp, pat, requires := sdkwarm.RequiresDemotion(workspacePlanPaths(req.Plan), req.SDKWarmBlockingPaths); requires {
			demoteStart := time.Now()
			if err := cl.DemoteSDK(ctx, fmt.Sprintf("workspace path %q matches sdkWarmBlockingPaths %q", mp, pat)); err != nil {
				reclaim(err)
				if isUnimplemented(err) {
					// spec: §6.1 — the runtime declared preConnect
					// but its adapter cannot tear down the SDK; fail the
					// session rather than serve it with stale SDK state.
					return nil, &SDKDemotionNotSupported{Pod: sandboxName}
				}
				return nil, fmt.Errorf("podsession: demote SDK on pod %s: %w", sandboxName, err)
			}
			demoted = true
			if b.SDKDemotion != nil {
				// spec: §6.3 — record the SDK teardown penalty.
				b.SDKDemotion(req.Pool, time.Since(demoteStart).Seconds())
			}
		}
	}

	// spec: §6.2 — the fine session-lifecycle states (receiving_uploads,
	// finalizing_workspace, running_setup, starting_session) are session-model
	// states on the Postgres session row, not coarse Sandbox.status.phase
	// occupancy values. The pod projects the coarse `claimed` phase set at
	// claim time, so Prepare runs the §4.7 setup RPCs without writing any
	// per-step CRD phase. On a pre-attached failure the pod is reclaimed by
	// draining it (the failed claim disposition: claimed → draining, §6.2).
	phaseStart := time.Now()
	// spec: §7.4; §13.4 — symlink targets are
	// canonicalized against the pod's actual workspace root so the
	// gateway-side extraction matches the adapter's post-promotion
	// re-validation location.
	allow := upload.RuntimeAllow{
		AllowSymlinks: req.ArchivePolicy.GetAllowSymlinks(),
		WorkspaceRoot: firstNonEmpty(req.ArchivePolicy.GetWorkspaceRoot(), slotlayout.SessionCurrentDir(neg.WorkspaceBase, req.SessionID)),
	}
	stagedPlan, stageWarnings, err := b.stageWorkspace(ctx, cl, req.SessionID, req.TenantID, req.Plan, allow, bindAttempt)
	if err != nil {
		reclaim(err)
		return nil, fmt.Errorf("podsession: stage workspace on pod %s: %w", sandboxName, err)
	}
	finalizeWarnings, err := cl.FinalizeWorkspace(ctx, req.SessionID, stagedPlan, req.ArchivePolicy, bindAttempt, false)
	if err != nil {
		reclaim(err)
		return nil, fmt.Errorf("podsession: finalize workspace on pod %s: %w", sandboxName, err)
	}
	// §7.4 strip-skip warnings now originate gateway-side (the
	// archive is no longer decompressed in the pod); merge them ahead of
	// any adapter-raised advisories for the §7.2 SSE republish.
	finalizeWarnings = append(stageWarnings, finalizeWarnings...)
	t.WorkspaceMaterialization = time.Since(phaseStart)

	phaseStart = time.Now()
	setupOutputs, err := cl.RunSetup(ctx, req.SessionID, stagedPlan.GetSetupCommands(), req.SetupPolicy, bindAttempt)
	if err != nil {
		reclaim(err)
		// spec: §7.5 — partial outputs ride alongside the failure
		// so the gateway can persist what was captured before the abort.
		return nil, &SetupCommandFailure{
			Pod:     sandboxName,
			Cause:   err,
			Outputs: setupOutputs,
		}
	}
	t.SetupCommands = time.Since(phaseStart)

	phaseStart = time.Now()
	// §4.7 AssignCredentials is the fourth setup RPC; it runs while the pod
	// projects the coarse `claimed` phase, before the runtime starts at Launch.
	if minted, err := b.assignCredentials(ctx, cl, req, bindAttempt); err != nil {
		// spec: §7.1 (normal flow); §4.9 (credential leasing service). The
		// attempt releases, by identifier, exactly the leases it minted before
		// the failure. failPhase's session-wide release is gated on
		// leaseAssigned, still false here, and a refusal skips failPhase
		// altogether, so this release runs unconditionally, ahead of the
		// reclaim closure and outside its refusal guard.
		b.releaseAttemptCredentials(minted)
		reclaim(err)
		return nil, fmt.Errorf("podsession: assign credentials on pod %s: %w", sandboxName, err)
	}
	// The lease is now held; a failure after this point must also revoke it.
	leaseAssigned = true
	t.CredentialAssignment = time.Since(phaseStart)

	cl.Close()
	return &PrepareResult{
		WorkspaceBase:         neg.WorkspaceBase,
		Demoted:               demoted,
		WorkspacePlanWarnings: finalizeWarnings,
		SetupOutputs:          setupOutputs,
		Timings:               t,
	}, nil
}
