// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// resumeOnPod restores a session onto a fresh §5 warm pod. When the
// session carries a §7.1 WorkspaceSnapshot it is restored from that
// checkpoint via the adapter Resume RPC. A session that never
// checkpointed has no snapshot to restore; it is rebuilt from the §14
// WorkspacePlan recorded at create by reusing the start path.
//
// On a successful resume the gateway publishes a §7.2 / §4.4
// `session.resumed` event with the derived `resumeMode` (full,
// partial_workspace, or conversation_only) and `workspaceLost`
// (derived from the resume mode) so clients can detect a degraded
// resume.
//
// The returned adapterReportedResumeMode is the §4.4 / §7.2 mode the
// adapter signalled (empty when the snapshot path was not taken or the
// adapter is on an older protocol). The caller passes it to
// `classifyResume` so a gateway-side eviction / partial-manifest record
// can upgrade `full` to `conversation_only` / `partial_workspace` while
// still letting the adapter signal a stronger classification when it
// has one. F-7.3.22.
//
// spec: §4.4, §7.2, §10.1 partial-manifest path.
func (s *Server) resumeOnPod(ctx context.Context, row sessionstore.Session) (string, error) {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			return "", err
		}
		// spec: §7.3 (snapshotless resume-rebuild) — the resume path holds no
		// create-time claim or credential resolution (it claims a fresh pod via
		// the whole-sequence Bind), so startOnPod resolves credentials itself.
		result, err := s.startOnPod(ctx, row, plan, nil)
		if err != nil {
			return "", err
		}
		// spec: §4.6.1 (coordinating replica holds the lease), §10.1
		// (per-session coordination lease) — the row is still `ready` here,
		// so registerBinding's at-bind acquire precedes the running-commit.
		// On ErrHeld a live foreign holder still coordinates this session
		// (handleResume applies no upstream holder gate), so publish no
		// competing binding, skip fenceResumedPod, release the fresh pod
		// claim startOnPod made, and fail the resume closed rather than
		// double-bind.
		if berr := s.registerBinding(ctx, result); berr != nil {
			s.rollbackBinding(ctx, result)
			return "", berr
		}
		// spec: §5.2 — a service-mode session is claimless, so startOnPod
		// returns a nil BindResult with no pod to fence. Service mode has no
		// Lenny-managed lifecycle and never reaches a resumable state, so this
		// guard is defensive: skip the resumed-pod fence when no pod was bound.
		if result == nil {
			return "", nil
		}
		if ferr := s.fenceResumedPod(ctx, result.Adapter, row.TenantID, row.ID); ferr != nil {
			return "", ferr
		}
		return "", nil
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool) on the resume-rebuild path; empty resolves by runtime + §5.3
	// profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return "", err
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §7.3 — surface last_checkpoint_workspace_bytes and the
	// §4.4 hard workspace size cap so the adapter can refuse a restore
	// whose archive would exceed the pod's emptyDir budget before
	// quiescing the runtime. F-7.3.26.
	var expectedBytes int64
	if row.WorkspaceSnapshot != nil {
		expectedBytes = row.WorkspaceSnapshot.Bytes
	}
	// spec: §10.1.7 — resolve the checkpoint's chunk set from the
	// manifest row the gateway owns, verify contiguity of [0, chunk_count),
	// and mint one presigned GET capability per index. The adapter fetches
	// them in ascending index order and concatenates the bodies into one
	// decompress→untar pipeline. A contiguity failure falls back to the
	// last successful full checkpoint; an unresolvable manifest (dev mode,
	// a checkpoint predating the chunked model) leaves the chunk set empty.
	chunks := s.resolveResumeChunks(ctx, row)
	result, err := s.podBinder.Resume(ctx, checkpointResumeRequest(row, match, agentInterface, minPlatformVersion, expectedBytes, chunks))
	if err != nil {
		accountResumeSlotFailure(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement,
			s.slotLeakGauge, match, err)
		return "", err
	}
	// spec: §4.6.1 (coordinating replica holds the lease), §10.1
	// (per-session coordination lease) — the checkpoint-restore branch
	// publishes the serving binding directly through podRegistry.Put below
	// without going through registerBinding, so acquire the coordination
	// lease here, ahead of that Put. On ErrHeld a live foreign holder still
	// coordinates this session (handleResume applies no upstream holder
	// gate), so skip the Put, the recovery-generation bump, and the fence,
	// release the restored pod, and fail the resume closed rather than
	// double-bind.
	if lerr := s.acquireCoordinationLease(ctx, row.TenantID, row.ID); lerr != nil {
		s.rollbackBinding(ctx, result.Result)
		return "", lerr
	}
	s.podRegistry.Put(result.Result)
	// spec: §4.2 — recovery_generation is incremented on each
	// pod recovery. Persist the new pod assignment in the same update
	// so a fresh replica picks up the recovered binding without
	// re-running resume.
	s.bumpRecoveryGeneration(ctx, row.TenantID, row.ID, result.Result.SandboxName)
	// spec: §10.1 / §4.2 — announce the session's
	// coordination_generation to the (re-)bound pod so it rejects any
	// straggler RPC from a prior coordinator. A relinquish aborts the
	// resume; a best-effort failure is logged and swallowed.
	if ferr := s.fenceResumedPod(ctx, result.Result.Adapter, row.TenantID, row.ID); ferr != nil {
		return "", ferr
	}
	return result.Mode, nil
}

// fenceResumedPod issues the §10.1 / §4.2 CoordinatorFence to the pod a
// resume just (re-)bound, announcing the session's current
// coordination_generation so the pod rejects straggler RPCs from a prior
// coordinator. A relinquish (the coordinator gave up the session after
// exhausting its §11.3 fence retries, releasing the lease) is returned so
// the caller aborts the resume; a best-effort fence failure (the
// generation could not be read) is logged and swallowed because the
// coordination lease still guards exclusive ownership.
//
// spec: §10.1, §4.2, §11.3.
func (s *Server) fenceResumedPod(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string) error {
	if s.fencer == nil || adapter == nil {
		return nil
	}
	relinquished, err := s.fencer.Fence(ctx, adapter, tenantID, sessionID)
	if relinquished {
		return err
	}
	if err != nil {
		log.Printf("sessionserver: coordinator fence for session %s best-effort failed: %v", sessionID, err)
	}
	return nil
}

// checkpointResumeRequest assembles the §7.1 ResumeRequest that restores row
// from its checkpoint onto a pod of the resolved pool match. KeepsRuntime is
// copied from the pool match so the resume's claim applies the §5.2
// process-reuse rule the session's pool configuration resolves to.
func checkpointResumeRequest(row sessionstore.Session, match podsession.PoolMatch, agentInterface []byte, minPlatformVersion string, expectedBytes int64, chunks []adapterclient.ChunkGrant) podsession.ResumeRequest {
	return podsession.ResumeRequest{
		Pool:                    match.Pool,
		SessionID:               row.ID,
		TenantID:                row.TenantID,
		KeepsRuntime:            match.KeepsRuntime,
		Runtime:                 row.RuntimeRef,
		CheckpointID:            row.WorkspaceSnapshot.Ref,
		ExperimentContext:       experimentContextToProto(row.ExperimentContext),
		TracingContext:          row.TracingContext,
		AgentInterface:          agentInterface,
		MinPlatformVersion:      minPlatformVersion,
		RecoveryGeneration:      row.RecoveryGeneration,
		ExpectedWorkspaceBytes:  expectedBytes,
		WorkspaceSizeLimitBytes: match.WorkspaceSizeLimitBytes,
		// spec: §7.3 step (d) — "Recreate same absolute `cwd`
		// path." The gateway carries the original session's adapter-
		// reported WorkspaceRoot (captured on the §15.5 handshake and
		// persisted at first bind) on every Resume. An empty value
		// (legacy row, adapter on an older protocol) disables the
		// assertion on the adapter side. F-7.3.15.
		ExpectedWorkspaceRoot: row.WorkspaceRoot,
		// spec: §5.2 — the resume reserves one counted slot on the
		// replacement pod on the pools the start path reserves one on, and
		// delivers the pool's uptime cap onto that pod. Normalize the bound
		// to a minimum of 1: Binder.Resume scopes the reservation on it.
		MaxConcurrentSessions: maxConcurrentSessions(match.MaxConcurrentSessions),
		MaxPodUptimeSeconds:   match.MaxPodUptimeSeconds,
		// spec: §5.2 — carry the pool's recycle disposition and whole-pod
		// scrub parameters onto the resume, the same way the start path
		// carries them onto a bind. The resumed session's release reads
		// them off the bind result, so a resume that dropped them would
		// retire a recycling pod instead of recycling it.
		Recycle:               match.Recycle,
		CleanupCommands:       match.CleanupCommands,
		CleanupTimeoutSeconds: match.CleanupTimeoutSeconds,
		Chunks:                chunks,
	}
}
