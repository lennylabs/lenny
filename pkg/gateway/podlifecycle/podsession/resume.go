// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ResumeRequest describes a session to restore onto a fresh warm pod.
type ResumeRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §7.1 session being resumed.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// KeepsRuntime is true when the pool the session server resolved lets a
	// pod serve a later session in its kept runtime process
	// (poolstore.KeepsRuntimeAcrossSessions). The claim copies it into
	// podclaim.ClaimRequest; the zero value refuses every pod that has served
	// a session. spec: §5.2 (Deployer acknowledgment (runtime process kept
	// across sessions)).
	KeepsRuntime bool
	// Runtime is the runtime name passed to the adapter's Resume.
	Runtime string
	// CheckpointID is the §4.4 checkpoint the workspace is restored
	// from.
	CheckpointID string
	// ExperimentContext and TracingContext are re-delivered to the
	// restored runtime in the adapter manifest. Nil when unset.
	ExperimentContext *adapterv1.ExperimentContext
	TracingContext    map[string]string
	// AgentInterface and MinPlatformVersion re-deliver the §15.4 manifest
	// fields to the restored runtime, matching BindRequest.
	AgentInterface     []byte
	MinPlatformVersion string
	// RecoveryGeneration is the session's §4.2 / §7.3 pod-recovery
	// counter at issue time. Echoed back from the adapter on
	// ResumeResult so the gateway can verify the adapter consumed the
	// fenced generation. F-7.3.22.
	RecoveryGeneration int64
	// ExpectedWorkspaceBytes is the session's
	// last_checkpoint_workspace_bytes from sessionstore. Passed to the
	// adapter so it can run the §4.4 / §7.3 symmetric
	// workspace size pre-check before extraction. F-7.3.26.
	ExpectedWorkspaceBytes int64
	// WorkspaceSizeLimitBytes is the §4.4 hard workspace size cap from
	// the SandboxTemplate. F-7.3.26.
	WorkspaceSizeLimitBytes int64
	// ExpectedWorkspaceRoot is the §7.3 the original session ran against. The gateway records the
	// SandboxTemplate's WorkspaceRoot at session creation; the adapter
	// asserts on Resume that the replacement pod's WorkspaceRoot
	// matches. Empty disables the assertion. F-7.3.15.
	ExpectedWorkspaceRoot string
	// MaxConcurrentSessions is the resumed session's pool bound
	// (sessionPolicy.maxConcurrentSessions), normalized to a minimum of 1
	// by the caller. Resume reserves one counted slot on the replacement
	// pod when it is greater than one, the same pools the start path
	// reserves a slot on. spec: §5.2.
	MaxConcurrentSessions int32
	// MaxPodUptimeSeconds is the pool's recycle.maxPodUptimeSeconds cap.
	// The reservation delivers it onto the replacement pod as the
	// lenny.dev/max-pod-uptime-seconds annotation, which is the only
	// channel by which the WarmPoolController learns the cap. Without it
	// the replacement pod carries no annotation and the §5.2 uptime drain
	// never fires on it.
	MaxPodUptimeSeconds int64
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag,
	// resolved by the caller from the same pool match it resolves the
	// concurrency bound from. Resume carries it onto the BindResult so the
	// resumed session's release sees the pool's recycle disposition: the
	// occupancy-zero edge of Binder.ReleaseSlot patches the per-pod claim
	// bound → recycling instead of deleting it, and Binder.Release takes
	// the recycle path instead of retiring the pod.
	Recycle bool
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2 whole-pod
	// scrub parameters, resolved by the caller from the same pool match.
	// Resume carries them onto the BindResult so the recycle-path Shutdown
	// delivers them in the §4.7 RecycleScrub sub-message without
	// re-resolving the pool at release.
	CleanupCommands       []string
	CleanupTimeoutSeconds int
	// Chunks carries one presigned GET capability per chunk of the
	// checkpoint being restored, in ascending index order. The gateway
	// resolves the chunk set from the manifest row it owns and mints one
	// capability per index in [0, chunk_count); the adapter fetches each
	// chunk directly from object storage and concatenates them into one
	// decompress→untar pipeline. Empty for the conversation-only and
	// coordinator-handoff resume paths that restore no workspace.
	//
	// spec: §10.1.7 — reassembly on resume.
	Chunks []adapterclient.ChunkGrant
}

// ResumeResult is what Binder.Resume returns alongside the BindResult:
// the §4.4 / §7.2 mode the adapter reported and the recovery generation
// it echoed back. F-7.3.22.
type ResumeResult struct {
	// Result is the standard claim-and-handshake outcome.
	Result *BindResult
	// Mode is the adapter-reported §4.4 / §7.2 ResumeMode. Empty when
	// the adapter is on an older protocol and did not report one; the
	// gateway falls back to its own classification.
	Mode string
	// RecoveryGeneration is the value the adapter echoed back. Equal
	// to ResumeRequest.RecoveryGeneration on a healthy round-trip.
	RecoveryGeneration int64
}

// Resume claims an idle pod for the request's session and restores the
// session's workspace onto it from the named §4.4 checkpoint via the
// adapter's Resume RPC. It is the §7.1 resume counterpart of Bind: used
// when a suspended session's original pod was released and the session
// must be rebuilt on a replacement pod. Any failure after the claim is
// returned so the gateway can retry on a fresh pod.
//
// The returned ResumeResult carries the standard claim-and-handshake
// BindResult plus the §4.4 / §7.2 mode the adapter reported and the
// echoed §4.2 recovery_generation. F-7.3.22.
func (b *Binder) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	sb, cl, neg, err := b.connect(ctx, podclaim.ClaimRequest{
		Pool: req.Pool, SessionID: req.SessionID, TenantID: req.TenantID, KeepsRuntime: req.KeepsRuntime,
	})
	if err != nil {
		return ResumeResult{}, err
	}
	// spec: §5.2; §6.4 — every session is bound to a slot, so a resume onto
	// a concurrent-workspace pool reserves one counted slot on the pod
	// connect already claimed. Without it the resumed session holds a
	// whole-pod claim and no slot, so it has no slot tree to restore into
	// and the pod's occupancy ledger under-counts it. The reservation runs
	// on the pod connect resolved rather than through a pool scan, which
	// would increment a different pod's counter.
	slotID, err := b.reserveResumeSlot(ctx, sb.Name, req)
	if err != nil {
		cl.Close()
		return ResumeResult{}, err
	}
	// spec: §4.7.1 (role and gateway RPC contract) — the Resume is the first
	// and only entry-touching RPC of this attempt and creates the slot
	// registry entry through its own claim, so it carries the attempt's
	// freshly minted token.
	bindAttempt := newBindAttempt()
	res, err := cl.Resume(ctx, adapterclient.ResumeParams{
		BindAttempt:             bindAttempt,
		SessionID:               req.SessionID,
		Runtime:                 req.Runtime,
		CheckpointID:            req.CheckpointID,
		ExperimentContext:       req.ExperimentContext,
		TracingContext:          req.TracingContext,
		AgentInterface:          req.AgentInterface,
		MinPlatformVersion:      req.MinPlatformVersion,
		RecoveryGeneration:      req.RecoveryGeneration,
		ExpectedWorkspaceBytes:  req.ExpectedWorkspaceBytes,
		WorkspaceSizeLimitBytes: req.WorkspaceSizeLimitBytes,
		ExpectedWorkspaceRoot:   req.ExpectedWorkspaceRoot,
		Chunks:                  req.Chunks,
	})
	if err != nil {
		return ResumeResult{}, b.failResume(ctx, cl, sb.Name, slotID, bindAttempt, req, err)
	}
	// spec: §6.2 — the resumed session's fine states
	// (resume_pending, resuming, running) are session-model states on the
	// Postgres session row, not coarse Sandbox.status.phase values; the fresh
	// pod stays in the coarse `claimed` phase. Release drains the pod when the
	// session settles.
	return ResumeResult{
		Result: &BindResult{
			SessionID:     req.SessionID,
			TenantID:      req.TenantID,
			SandboxName:   sb.Name,
			PodIP:         sb.Status.PodIP,
			Adapter:       cl,
			WorkspaceBase: neg.WorkspaceBase,
			// spec: §5.2 — SlotID is the resumed session's statement that
			// the pod keeps a slot ledger for it, and it is the key both
			// release paths dispatch on. It is empty on an exclusive pool,
			// where no slot was reserved and the release runs through
			// Binder.Release.
			SlotID: slotID,
			// spec: §5.2 — the resumed session's release reads the pool's
			// recycle disposition off the bind result, on both the
			// exclusive Binder.Release path and the concurrent
			// Binder.ReleaseSlot occupancy-zero edge. Without these three
			// fields a session that resumed from a checkpoint stops
			// recycling the pod it ran on.
			Recycle:               req.Recycle,
			CleanupCommands:       req.CleanupCommands,
			CleanupTimeoutSeconds: req.CleanupTimeoutSeconds,
		},
		Mode:               res.Mode,
		RecoveryGeneration: res.RecoveryGeneration,
	}, nil
}

// reserveResumeSlot reserves the resumed session's counted slot on the pod
// connect already claimed, on the pools the start path reserves one on
// (sessionPolicy.maxConcurrentSessions greater than one). It returns the
// reserved slot's identifier, which is the session's own identifier, and
// the empty string on an exclusive pool, which keeps no per-pod ledger.
//
// spec: §5.2 (atomic slot reservation), §7.1 (resume onto a replacement
// pod).
func (b *Binder) reserveResumeSlot(ctx context.Context, sandboxName string, req ResumeRequest) (string, error) {
	if req.MaxConcurrentSessions <= 1 {
		return "", nil
	}
	claimer := &podclaim.SlotClaimer{
		Client:         b.Client,
		Namespace:      b.Namespace,
		Counter:        b.SlotCounter,
		OnSlotConflict: b.SlotConflict,
		OnRehydrate:    b.Rehydration,
	}
	res, err := claimer.ReserveSlotOnPod(ctx, sandboxName, podclaim.SlotRequest{
		Pool:                  req.Pool,
		SessionID:             req.SessionID,
		TenantID:              req.TenantID,
		MaxConcurrentSessions: req.MaxConcurrentSessions,
		MaxPodUptimeSeconds:   req.MaxPodUptimeSeconds,
	})
	if err != nil {
		// ErrNoConcurrentSlot is returned unwrapped: a concurrent start for
		// the same tenant can fill the pod's bound in the window between
		// connect's claim CREATE and this reservation, and the caller
		// retries the resume on a fresh pod under Resume's existing
		// contract. No increment landed, so nothing is released.
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) {
			return "", err
		}
		return "", fmt.Errorf("podsession: reserve resume slot on pod %s: %w", sandboxName, err)
	}
	return res.SlotID, nil
}

// failResume compensates a failed adapter Resume. It sends the compensating
// Shutdown naming the resume's own bind attempt on the still-open connection,
// forwards the outcome to the compensation counter, closes the connection,
// and then releases the resume's slot reservation carrying the reclaim's
// disposition. A resume whose response was lost therefore tears down the
// orphan entry it created, and a stale compensation from another attempt at
// the same session is answered superseded rather than destroying the live
// resumed session.
//
// The reservation is the resume's own, so its release is too: the adapter
// Resume RPC is the one failure that follows a completed reservation, and
// leaving the increment behind would compound per retry on the retryable
// restore path. Every earlier failure returns out of connect or out of the
// reservation itself with no increment landed, so no other error path
// releases.
//
// It releases no §4.9 credential lease. Binder.Resume mints none and its §7.3
// retry re-mints none, while the session may still hold the leases its
// original bind minted; returning them on a retryable failure would leave
// every later resume running with leases the gateway has already released.
//
// The returned error carries a *SlotBindError at stage "resume" whose Leaked
// is the reclaim's disposition or a failed release, so the caller's slot
// accounting reads the disposition off the chain. SlotBindError unwraps to
// the Resume error, so the chain's gRPC classification is unchanged. On an
// exclusive pool the slot identifier is empty and no accounting runs.
//
// spec: §7.1 (normal flow); §7.2 (interactive session model); §7.3 (retry and
// resume); §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration
// and execution modes).
func (b *Binder) failResume(ctx context.Context, cl *adapterclient.Client, sandboxName, slotID, bindAttempt string, req ResumeRequest, cause error) error {
	_, cleanly, cerr := b.compensateAndNote(ctx, cl, req.SessionID, bindAttempt,
		req.CleanupTimeoutSeconds, req.MaxConcurrentSessions, req.Pool, sandboxName, slotID, cause)
	leaked := cerr != nil || !cleanly
	cl.Close()
	relErr := b.releaseResumeSlot(ctx, sandboxName, slotID, leaked)
	sbe := b.slotBindError(sandboxName, slotID, slotFailureResume, cause)
	sbe.Leaked = leaked || relErr != nil
	return fmt.Errorf("podsession: resume session on pod %s: %w", sandboxName, sbe)
}

// releaseResumeSlot rolls back the resume's slot reservation after the
// adapter Resume RPC failed, carrying the compensating reclaim's disposition,
// and returns the release error so the caller books a failed release as a
// leak. It is a no-op on an exclusive pool, which reserved nothing:
// ReleaseSlotReservation decrements unconditionally, so a release for an
// increment that never landed would under-count the pod.
func (b *Binder) releaseResumeSlot(ctx context.Context, sandboxName, slotID string, leaked bool) error {
	if slotID == "" {
		return nil
	}
	if err := b.ReleaseSlotReservation(ctx, sandboxName, slotID, leaked); err != nil {
		log.Printf("podsession: release resume slot reservation on pod %s: %v", sandboxName, err)
		return err
	}
	return nil
}
