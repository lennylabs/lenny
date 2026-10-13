// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"

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
	// restored runtime in the resumed session's session_start frame on
	// CH-MSGSOCK (§28.5.3). Nil when unset.
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
	// OnClaimed is the post-claim hook. The binder calls it once per binder
	// call, after the pod claim and any slot reservation and before the first
	// RPC to the pod, so a resume writes `resuming` only once it holds a pod.
	// A hook error releases the claim and is returned unwrapped. Nil is a
	// no-op, which is the create and start paths. spec: §7.3 (Retry and
	// Resume).
	OnClaimed func(context.Context) error
	// CredentialPools, UserID, and UserCredentialProviders name the §4.9
	// leases the restore assigns on the replacement pod before its runtime
	// starts, with the meanings BindRequest gives them. Empty when the
	// session needs no upstream credentials. spec: §7.3 (Credential leases on
	// the replacement pod).
	CredentialPools         map[string]string
	UserID                  string
	UserCredentialProviders []string
	// CoordinationGeneration is the resume driver's entry generation, recorded
	// on every lease the restore mints. spec: §7.3 (Credential leases on the
	// replacement pod).
	CoordinationGeneration int64
}

// credentialBindRequest projects the request onto the BindRequest fields the
// credential assignment reads, so the restore mints its leases through the
// same assignCredentials path a bind uses.
func (req ResumeRequest) credentialBindRequest() BindRequest {
	return BindRequest{
		SessionID:               req.SessionID,
		TenantID:                req.TenantID,
		UserID:                  req.UserID,
		CredentialPools:         req.CredentialPools,
		UserCredentialProviders: req.UserCredentialProviders,
		CoordinationGeneration:  req.CoordinationGeneration,
	}
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
// The order is claim, slot reservation, post-claim hook, handshake,
// credential assignment, adapter Resume. A failure of the claim or of the
// reservation releases nothing, because neither leaves anything to release. A
// hook error, and a resolve, dial, or handshake failure after the
// reservation, release the claim through releaseResumeSlot with a clean
// disposition and send no compensation, because no entry-touching RPC has
// run. A hook error is returned as the hook returned it.
//
// The credential assignment mints the attempt's §4.7.1 token first and
// carries it on AssignCredentials and on the following Resume, so the adapter
// admits the Resume into the entry the assignment created and the session's
// credential file holds its leases at session_start. AssignCredentials must
// precede Resume: after Resume the adapter refuses it as already started.
// With no credential service, or no provider named, nothing is assigned. A
// failure from the minting onward, including a failed adapter Resume, returns
// through failResume, which releases exactly the leases this attempt minted
// and no other lease of the session.
//
// The returned ResumeResult carries the standard claim-and-handshake
// BindResult plus the §4.4 / §7.2 mode the adapter reported and the
// echoed §4.2 recovery_generation. F-7.3.22.
//
// spec: §7.3 (Retry and Resume); §5.2 (atomic slot reservation).
func (b *Binder) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	sandboxName, err := b.claimPod(ctx, podclaim.ClaimRequest{
		Pool: req.Pool, SessionID: req.SessionID, TenantID: req.TenantID, KeepsRuntime: req.KeepsRuntime,
	})
	if err != nil {
		return ResumeResult{}, err
	}
	// spec: §5.2; §6.4 — every session is bound to a slot, so a resume onto
	// a concurrent-workspace pool reserves one counted slot on the pod the
	// claim took. Without it the resumed session holds a whole-pod claim and
	// no slot, so it has no slot tree to restore into and the pod's occupancy
	// ledger under-counts it. The reservation runs on the claimed pod rather
	// than through a pool scan, which would increment a different pod's
	// counter.
	slotID, err := b.reserveResumeSlot(ctx, sandboxName, req)
	if err != nil {
		return ResumeResult{}, err
	}
	// spec: §7.3 (Retry and Resume) — the hook runs after the claim and the
	// reservation and before any RPC reaches the pod.
	// A failure from here to the adapter Resume releases with a clean
	// disposition: no entry-touching RPC has run, so the adapter holds
	// nothing for the session. releaseResumeSlot logs a release error, and
	// the §4.6.1 orphan-claim GC collects what it could not release.
	if err := runOnClaimed(ctx, req.OnClaimed); err != nil {
		_ = b.releaseResumeSlot(ctx, sandboxName, slotID, false)
		return ResumeResult{}, err
	}
	sb, cl, neg, err := b.handshake(ctx, sandboxName, req.Pool)
	if err != nil {
		_ = b.releaseResumeSlot(ctx, sandboxName, slotID, false)
		return ResumeResult{}, err
	}
	// spec: §7.3 (Credential leases on the replacement pod); §4.9
	// (credential leasing service) — mint the attempt's token, then assign the
	// restore's leases under it before the Resume, which carries the same
	// token. spec: §4.7.1 (role and gateway RPC contract).
	bindAttempt := newBindAttempt()
	minted, err := b.assignCredentials(ctx, cl, req.credentialBindRequest(), bindAttempt)
	if err != nil {
		return ResumeResult{}, b.failResume(ctx, cl, sb.Name, slotID, bindAttempt, req, minted, err)
	}
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
		return ResumeResult{}, b.failResume(ctx, cl, sb.Name, slotID, bindAttempt, req, minted, err)
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
			// spec: §7.3 (Resume driver) — an uncommitted restore reclaims
			// the entry under this attempt's token and returns exactly the
			// leases it minted.
			BindAttempt:           bindAttempt,
			Pool:                  req.Pool,
			MaxConcurrentSessions: max(req.MaxConcurrentSessions, 1),
			MintedLeaseIDs:        minted,
		},
		Mode:               res.Mode,
		RecoveryGeneration: res.RecoveryGeneration,
	}, nil
}

// reserveResumeSlot reserves the resumed session's counted slot on the pod
// the resume's claim took, on the pools the start path reserves one on
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
		// the claim CREATE and this reservation, and the caller
		// retries the resume on a fresh pod under Resume's existing
		// contract. No increment landed, so nothing is released.
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) {
			return "", err
		}
		return "", fmt.Errorf("podsession: reserve resume slot on pod %s: %w", sandboxName, err)
	}
	return res.SlotID, nil
}

// failResume compensates a failed credential assignment or adapter Resume. It
// first releases, by identifier, the §4.9 leases this attempt minted (minted),
// unconditionally, and no other lease of the session. It then sends the
// compensating Shutdown naming the resume's own bind attempt on the
// still-open connection, forwards the outcome to the compensation counter,
// closes the connection, and releases what the resume holds on the pod
// (releaseResumeSlot): the slot reservation carrying the reclaim's
// disposition on a concurrent-workspace pool, or the per-pod claim on an
// exclusive pool. A resume whose response was lost therefore tears down the
// orphan entry it created, and a stale compensation from another attempt at
// the same session is answered superseded rather than destroying the live
// resumed session.
//
// The reservation and the claim are the resume's own, so their release is
// too: leaving the increment behind would compound per retry on the
// retryable restore path, and leaving the exclusive claim would hold the pod
// until the orphan-claim GC. A failure before the adapter Resume releases
// through releaseResumeSlot with a clean disposition and sends no
// compensation.
//
// The lease release is scoped to this attempt's identifiers because the
// session's earlier leases belong to the binding that minted them, whose
// release returns them; a session-wide release would also strip a lease a
// concurrent attempt minted.
//
// The returned error carries a *SlotBindError at stage "resume" whose Leaked
// is the reclaim's disposition or a failed release, so the caller's slot
// accounting reads the disposition off the chain. SlotBindError unwraps to
// the Resume error, so the chain's gRPC classification is unchanged. On an
// exclusive pool the slot identifier is empty, the release deletes the
// claim, and the caller's slot accounting skips the error. A
// *CredentialAssignmentError from the minting stays in the chain under the
// *SlotBindError.
//
// spec: §7.1 (normal flow); §7.2 (interactive session model); §7.3 (retry and
// resume; Credential leases on the replacement pod); §4.7.1 (role and gateway
// RPC contract); §4.9 (credential leasing service); §5.2 (pool configuration
// and execution modes).
func (b *Binder) failResume(ctx context.Context, cl *adapterclient.Client, sandboxName, slotID, bindAttempt string, req ResumeRequest, minted []string, cause error) error {
	b.releaseAttemptCredentials(minted)
	_, cleanly, cerr := b.compensateAndNote(ctx, cl, req.SessionID, bindAttempt,
		req.CleanupTimeoutSeconds, req.MaxConcurrentSessions, req.Pool, sandboxName, slotID, cause)
	leaked := cerr != nil || !cleanly
	cl.Close()
	relErr := b.releaseResumeSlot(ctx, sandboxName, slotID, leaked)
	sbe := b.slotBindError(sandboxName, slotID, slotFailureResume, cause)
	sbe.Leaked = leaked || relErr != nil
	return fmt.Errorf("podsession: resume session on pod %s: %w", sandboxName, sbe)
}

// errUncommittedRestore is the cause an uncommitted restore's compensation is
// labeled with on the §16.1 compensation counter: a failure rather than a
// slot-bind refusal.
var errUncommittedRestore = errors.New("podsession: restore commit did not land")

// ErrReclaimUnconfirmed reports that ReleaseUncommitted, asked to require a
// confirmed reclaim, received a reply other than RECLAIMED or a reclaim
// error, and therefore released no claim, slot, or lease.
var ErrReclaimUnconfirmed = errors.New("podsession: reclaim of an uncommitted restore was not confirmed")

// ReleaseUncommitted tears down a restore whose commit did not land, sharing
// failResume's teardown. It sends one ShutdownReclaim naming the attempt's
// own token (result.BindAttempt) on its detached, budget-bounded context,
// closes the attempt's adapter connection, releases what the attempt holds on
// the pod (releaseResumeSlot: the slot reservation with leaked taken from the
// reclaim's reply, or the claim on an exclusive pool), and returns, by
// identifier, the leases the attempt minted (result.MintedLeaseIDs). It never
// releases leases by session, and it sends no second Shutdown.
//
// With requireReclaimed set, which the resume driver passes when a commit try
// returned a store error, a reply other than RECLAIMED, or a reclaim error,
// releases no claim, slot, or lease and returns ErrReclaimUnconfirmed: only a
// RECLAIMED reply shows that the entry this attempt created is still in
// place, so no other releaser has acted on the pod.
//
// spec: §7.3 (Resume driver; Credential leases on the replacement pod);
// §4.7.1 (role and gateway RPC contract).
func (b *Binder) ReleaseUncommitted(ctx context.Context, result *BindResult, requireReclaimed bool) error {
	if result == nil {
		return nil
	}
	outcome := adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_UNSPECIFIED
	cleanly := false
	var cerr error
	if result.Adapter != nil {
		outcome, cleanly, cerr = b.compensateAndNote(ctx, result.Adapter, result.SessionID, result.BindAttempt,
			result.CleanupTimeoutSeconds, result.MaxConcurrentSessions, result.Pool,
			result.SandboxName, result.SlotID, errUncommittedRestore)
		result.Adapter.Close()
	}
	if requireReclaimed && (cerr != nil || outcome != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED) {
		slog.Warn("podsession: reclaim of an uncommitted restore not confirmed; releasing nothing",
			"session_id", result.SessionID, "sandbox", result.SandboxName,
			"outcome", outcome.String(), "error", cerr)
		return fmt.Errorf("%w: session %s on pod %s", ErrReclaimUnconfirmed, result.SessionID, result.SandboxName)
	}
	leaked := cerr != nil || !cleanly
	relErr := b.releaseResumeSlot(ctx, result.SandboxName, result.SlotID, leaked)
	b.releaseAttemptCredentials(result.MintedLeaseIDs)
	return relErr
}

// releaseResumeSlot releases what a failed resume holds on its pod and
// returns the release error, so the caller books a failed release as a leak.
// On a concurrent-workspace pool it rolls back the resume's slot reservation
// carrying the caller's disposition; SlotClaimer.ReleaseSlot deletes the
// per-pod claim only at occupancy zero, so a sibling slot keeps the pod
// claimed. On an exclusive pool, where slotID is empty and no reservation was
// made, it deletes the per-pod claim the resume made. Both run on a detached
// context, so a release reached after the request context ended still lands.
//
// spec: §7.3 (Retry and Resume); §5.2 (slot retry releases the reservation);
// §4.6.1 (occupancy projection on claim DELETE).
func (b *Binder) releaseResumeSlot(ctx context.Context, sandboxName, slotID string, leaked bool) error {
	if slotID == "" {
		if err := b.deleteClaimDetached(ctx, sandboxName); err != nil {
			log.Printf("podsession: delete resume claim on pod %s: %v", sandboxName, err)
			return err
		}
		return nil
	}
	if err := b.ReleaseSlotReservation(ctx, sandboxName, slotID, leaked); err != nil {
		log.Printf("podsession: release resume slot reservation on pod %s: %v", sandboxName, err)
		return err
	}
	return nil
}
