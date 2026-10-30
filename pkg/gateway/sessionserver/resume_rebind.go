// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/session/executor"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
)

// resumeBind carries the resume driver's post-claim hook and the generation
// its entry write commits, G, into a restore. onClaimed writes the session's
// resuming incarnation once the replacement pod is claimed; generation is
// recorded on every lease the restore mints. The zero value runs no hook and
// mints at generation 0.
type resumeBind struct {
	onClaimed  func(context.Context) error
	generation int64
}

// resumeOutcome is a restore that reached its replacement pod and has not yet
// committed. result is the unpublished binding, nil for a claimless
// service-mode rebuild; mode is the adapter-reported resume mode, empty on the
// rebuild branch; rebuild reports the snapshotless rebuild branch.
type resumeOutcome struct {
	result  *podsession.BindResult
	mode    string
	rebuild bool
}

// resumeOnPod restores a session onto a fresh §5 warm pod. When the session
// carries a §7.1 WorkspaceSnapshot it is restored from that checkpoint via the
// adapter Resume RPC, with the session's credential leases assigned on the
// replacement pod before its runtime starts. A session that never
// checkpointed has no snapshot to restore; it is rebuilt from the §14
// WorkspacePlan recorded at create by reusing the start path.
//
// resumeOnPod publishes nothing. Until the commit, no session-row field and no
// published binding names the replacement pod: both branches return the
// unpublished BindResult and the adapter-reported mode, and the caller,
// completeResume, commits, fences, and publishes. It acquires no coordination
// lease; the resume driver acquires it once before the restore. rb carries
// the driver's post-claim hook, which runs after the claim and before any RPC
// reaches the pod on both branches, and its entry generation.
//
// The checkpoint branch resolves the session's credential pools and user
// before ResolvePool and runs the credential-delivery isolation check after
// it, as the start path does, and returns either error before the binder's
// Resume, so both are pre-claim failures.
//
// spec: §7.3 (Resume driver; Credential leases on the replacement pod); §4.9
// (Pre-Claim Credential Availability Check); §4.4, §10.1.7 (partial-manifest
// path).
func (s *Server) resumeOnPod(ctx context.Context, row sessionstore.Session, rb resumeBind) (resumeOutcome, error) {
	if row.WorkspaceSnapshot == nil || row.WorkspaceSnapshot.Ref == "" {
		plan, err := storedWorkspacePlan(row)
		if err != nil {
			return resumeOutcome{}, err
		}
		// spec: §7.3 (snapshotless resume-rebuild) — the resume path holds no
		// create-time claim or credential resolution (it claims a fresh pod via
		// the whole-sequence Bind), so startOnPod resolves credentials itself.
		result, err := s.startOnPod(ctx, row, plan, nil, rb)
		if err != nil {
			return resumeOutcome{}, err
		}
		return resumeOutcome{result: result, rebuild: true}, nil
	}
	credPools, poolDeliveryModes, userProviders, err := s.resolveCredentialPools(ctx, row)
	if err != nil {
		return resumeOutcome{}, err
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool) on the resume-rebuild path; empty resolves by runtime + §5.3
	// profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return resumeOutcome{}, err
	}
	if err := s.checkCredentialDeliveryIsolation(match, poolDeliveryModes); err != nil {
		return resumeOutcome{}, err
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
	req := checkpointResumeRequest(row, match, agentInterface, minPlatformVersion, expectedBytes, chunks, rb)
	req.CredentialPools = credPools
	req.UserCredentialProviders = userProviders
	req.UserID = row.UserID
	result, err := s.podBinder.Resume(ctx, req)
	if err != nil {
		accountResumeSlotFailure(ctx, s.podBinder, s.slotHealth, s.slotStates, s.slotReplacement,
			s.slotLeakGauge, match, err)
		return resumeOutcome{}, err
	}
	return resumeOutcome{result: result.Result, mode: result.Mode}, nil
}

// runClaimlessEntry runs the resume driver's post-claim hook for a restore
// that claims no pod, a service-mode session. Such a restore has no claim for
// the binder to follow with the hook, so the hook runs at the point the claim
// would have been, before the restore returns, and the resume_pending to
// resuming entry write stays the hook's write on every path. The hook's error
// is returned unwrapped, as the binder returns it, so the driver reads a
// superseded or failed entry write the same way for both kinds of restore. A
// zero resumeBind, the create and start paths, runs nothing.
//
// spec: §7.3 (Resume driver), §5.2 (Pool Configuration and Execution Modes)
func runClaimlessEntry(ctx context.Context, rb resumeBind) error {
	if rb.onClaimed == nil {
		return nil
	}
	return rb.onClaimed(ctx)
}

// releaseThenResumeOnPod releases any earlier binding of the session and then
// restores it onto a fresh pod. The resume driver calls it once per attempt,
// holding the coordination lease it acquired before the call, with its
// post-claim hook and entry generation. A context error while it waits for
// the session's slot-accounting lock is returned before any pod is claimed,
// as a pre-claim failure.
//
// spec: §7.3 (Resume driver).
func (s *Server) releaseThenResumeOnPod(ctx context.Context, row sessionstore.Session, rb resumeBind) (resumeOutcome, error) {
	if err := s.releaseEarlierBinding(ctx, row); err != nil {
		return resumeOutcome{}, err
	}
	return s.resumeOnPod(ctx, row, rb)
}

// releaseEarlierBinding releases, with the failed disposition, any binding
// this replica still holds for the session before the resume driver rebinds
// it. Such a binding belongs to an earlier bind of the session, such as one
// whose fence relinquished after its publish, or one that
// MarkBudgetUnrecoverable left on an awaiting_client_action root, and a
// resume that published a new binding over it would leak its pod or slot. It
// waits for the session's slot-accounting lock on ctx, so a binding the
// failure funnel is accounting and releasing is released only after the
// funnel has finished.
//
// Before the release it clears the row's pod_assignment with a guarded write
// that applies only while the row is in a recovery state and names the
// binding's sandbox, so the row stops naming a pod whose slot this call
// releases. A store error on that write returns before any claim with the
// binding kept, so the next attempt repeats it; a guard miss writes nothing
// and the release still runs. The release runs on the server-lifetime
// context, so the attempt bound or a CancelResumeAttempt call cannot cut it
// short after the executor's unbind. A release error is logged and the
// resume proceeds: the executor evicts the stream and removes the registry
// entry before it calls the binder, so the new bind does not overwrite a live
// entry. It writes no coordination lease.
//
// spec: §10.1.1 (one coordinating replica per session); §7.3 (Resume driver;
// Credential leases on the replacement pod; Resume flow after pod failure);
// §5.2 (whole-pod replacement trigger; Post-recovery rehydration atomicity).
func (s *Server) releaseEarlierBinding(ctx context.Context, row sessionstore.Session) error {
	unlock, err := s.slotAccountLocks.Lock(ctx, row.ID)
	if err != nil {
		return fmt.Errorf("sessionserver: wait for slot accounting of session %s: %w", row.ID, err)
	}
	defer unlock()
	if bind := s.snapshotBinding(row.ID); bind != nil {
		if err := s.clearRecoveryPod(ctx, row, bind.SandboxName); err != nil {
			return err
		}
	}
	if rerr := releaseExecutor(s.lifetime(), s.executor, row.ID, executor.DispositionFailed); rerr != nil {
		log.Printf("sessionserver: release earlier binding of session %s before resume: %v", row.ID, rerr)
	}
	return nil
}

// errRecoveryPodGuardMiss reports that clearRecoveryPod's guard did not
// match the locked row, so the write changed nothing.
var errRecoveryPodGuardMiss = errors.New("sessionserver: recovery row does not name the binding's pod")

// clearRecoveryPod clears pod_assignment on row when the locked row is in a
// recovery state and names sandboxName. A guard miss is not an error.
//
// spec: §5.2 (Post-recovery rehydration atomicity), §7.3 (Resume flow after
// pod failure).
func (s *Server) clearRecoveryPod(ctx context.Context, row sessionstore.Session, sandboxName string) error {
	_, err := s.store.Update(ctx, row.TenantID, row.ID, func(r *sessionstore.Session) error {
		if !session.IsRecovery(r.State) || sandboxName == "" || r.PodAssignment != sandboxName {
			return errRecoveryPodGuardMiss
		}
		r.PodAssignment = ""
		return nil
	})
	if err != nil && !errors.Is(err, errRecoveryPodGuardMiss) {
		return fmt.Errorf("sessionserver: clear the pod of recovering session %s: %w", row.ID, err)
	}
	return nil
}

// fenceResumedPod issues the §10.1 / §4.2 CoordinatorFence to the pod a
// resume just committed onto, at generation, the resume's entry generation G.
// A relinquish (the pod accepted a higher generation, so a takeover raced the
// commit; or the fence retries were exhausted and the lease released) is
// returned so the caller takes the Relinquished exit; a best-effort fence
// failure is logged and swallowed because the coordination lease still
// guards exclusive ownership.
//
// spec: §10.1.2 (Coordinator Handoff Protocol), §10.1.5 (Stale Replica
// Behavior), §11.3.
func (s *Server) fenceResumedPod(ctx context.Context, adapter *adapterclient.Client, tenantID, sessionID string, generation int64) error {
	if s.fencer == nil || adapter == nil {
		return nil
	}
	relinquished, err := s.fencer.FenceAt(ctx, adapter, tenantID, sessionID, generation)
	if relinquished {
		return err
	}
	if err != nil {
		log.Printf("sessionserver: coordinator fence for session %s best-effort failed: %v", sessionID, err)
	}
	return nil
}

// checkpointResumeRequest assembles the §7.1 ResumeRequest that restores row
// from its checkpoint onto a pod of the resolved pool match. ReusesPods and
// RequiresProcessAcknowledgment are copied from the pool match so the resume's claim applies the §5.2
// process-reuse rule the session's pool configuration resolves to. rb
// carries the resume driver's post-claim hook and its entry generation, which
// the restore records on every lease it mints (§7.3 (Resume driver;
// Credential leases on the replacement pod)).
func checkpointResumeRequest(row sessionstore.Session, match podsession.PoolMatch, agentInterface []byte, minPlatformVersion string, expectedBytes int64, chunks []adapterclient.ChunkGrant, rb resumeBind) podsession.ResumeRequest {
	return podsession.ResumeRequest{
		Pool:                          match.Pool,
		SessionID:                     row.ID,
		TenantID:                      row.TenantID,
		ReusesPods:                    match.ReusesPods,
		RequiresProcessAcknowledgment: match.RequiresProcessAcknowledgment,
		Runtime:                       row.RuntimeRef,
		CheckpointID:                  row.WorkspaceSnapshot.Ref,
		ExperimentContext:             experimentContextToProto(row.ExperimentContext),
		TracingContext:                row.TracingContext,
		AgentInterface:                agentInterface,
		MinPlatformVersion:            minPlatformVersion,
		RecoveryGeneration:            row.RecoveryGeneration,
		ExpectedWorkspaceBytes:        expectedBytes,
		WorkspaceSizeLimitBytes:       match.WorkspaceSizeLimitBytes,
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
		Recycle:                match.Recycle,
		CleanupCommands:        match.CleanupCommands,
		CleanupTimeoutSeconds:  match.CleanupTimeoutSeconds,
		Chunks:                 chunks,
		OnClaimed:              rb.onClaimed,
		CoordinationGeneration: rb.generation,
	}
}
