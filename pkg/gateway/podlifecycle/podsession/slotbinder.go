// SPDX-License-Identifier: MIT

package podsession

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strconv"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/pkg/upload/archive"
)

// SlotBindRequest describes a session to place on a §5.2 concurrent-session
// pod slot (sessionPolicy.maxConcurrentSessions > 1). It is the
// concurrent-session counterpart of BindRequest: rather than claiming a
// pod exclusively, BindSlot reserves one of the pod's up-to-
// maxConcurrentSessions slots, each in its own per-slot workspace.
type SlotBindRequest struct {
	// Pool is the SandboxWarmPool to claim a slot from.
	Pool string
	// SessionID is the §15.1 session being started. It is also the
	// slot's SlotID.
	SessionID string
	// TenantID is the tenant that owns the session. §5.2 tenant pinning
	// binds a concurrent-session pod to its first tenant.
	TenantID string
	// Runtime is the runtime name passed to the adapter's StartSession.
	Runtime string
	// MaxConcurrentSessions is the §5.2 sessionPolicy.maxConcurrentSessions
	// per-pod simultaneous-session bound.
	MaxConcurrentSessions int32
	// MaxPodUptimeSeconds is the §6.2 lines 166-167 concurrent-session
	// pod-uptime retirement cap. The slot-claim path skips a candidate pod
	// whose uptime exceeds it before assigning a slot; the skip is a
	// read-only placement filter and the gateway does not write
	// Sandbox.status. The WarmPoolController owns the resulting draining
	// transition, derived from the pod CreationTimestamp (§4.6.1). Zero
	// leaves uptime retirement off.
	MaxPodUptimeSeconds int64
	// Plan is the per-slot workspace the adapter materializes under
	// /workspace/slots/{slotId}/ before start. spec: §5.2 — concurrent
	// sessions always materialize a per-slot workspace.
	Plan *adapterv1.WorkspacePlan
	// ExperimentContext and TracingContext are delivered to the runtime
	// in the adapter manifest. Nil when unset.
	ExperimentContext *adapterv1.ExperimentContext
	TracingContext    map[string]string
	// SetupPolicy bounds the §5.1 setup phase. Nil when the runtime
	// declares no cap.
	SetupPolicy *adapterv1.SetupPolicy
	// ArchivePolicy is the §13.4 per-Runtime archive-extraction opt-in
	// block. Nil leaves the platform default (symlinks rejected).
	// spec: §7.4 lines 458, 462 — F-7.4.4.
	ArchivePolicy *adapterv1.ArchivePolicy
	// CredentialPools names the §4.9 credential pools to lease from,
	// keyed by provider. Per §6 a concurrent-session slot holds an
	// independent per-slot credential lease. Empty when the session
	// needs no upstream LLM credentials.
	CredentialPools map[string]string
	// UserID is the session's owning user, used to resolve the §4.9
	// user-source credentials named in UserCredentialProviders.
	UserID string
	// UserCredentialProviders names the providers the §4.9 pre-claim
	// resolved to the user source (the Pre-Authorized Credential Flow).
	// BindSlot materializes a proxy-mode lease for each. Empty when no
	// provider resolved to a user credential. spec: §4.9 lines 1340-1381.
	UserCredentialProviders []string
	// PodSpiffeURI is the issuing pod's SPIFFE identity recorded on each
	// minted lease. Empty disables proxy-mode SPIFFE binding.
	PodSpiffeURI string
	// AgentInterface is the runtime's §5.1 agentInterface descriptor as
	// JSON, written into the §15.4 manifest. Nil when undeclared.
	AgentInterface []byte
	// MinPlatformVersion is the runtime's §5.1 minPlatformVersion written
	// into the §15.4 manifest. Empty when none is specified.
	MinPlatformVersion string
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag, resolved
	// by ResolvePool. On a recycling concurrent-session pool (the §3.1
	// "Concurrent" preset, maxConcurrentSessions > 1 with recycle.enabled:
	// true), when the last slot drains cleanly ReleaseSlot patches the per-pod
	// claim bound → recycling and signals the whole-pod scrub (the §3.4 recycle
	// disposition) rather than deleting the claim, so the adapter's
	// ReportPodScrub drives recycle vs. retire and the occupancy-zero whole-pod
	// scrub clears the cross-cohort residue (shared /tmp, /dev/shm, surviving
	// processes). False for a non-recycling concurrent pool (the §3.1 "Bounded
	// cohort" preset), where the pod terminates after the cohort drains. spec:
	// §3.1, §3.4, §6.30/§6.41 (occupancy-zero recycle edge on a recycling pod).
	Recycle bool
}

// BindSlot places a session on a §5.2 concurrent-session pod slot.
//
// It reserves a slot via podclaim.SlotClaimer — landing on a pod that
// is already hosting slots when one has free capacity for the tenant,
// or opening a fresh idle pod otherwise — resolves the pod's adapter
// address, runs the §15.5 version handshake, and then runs the per-slot
// assignment sequence. The slot gets its own workspace: BindSlot stages
// and finalizes the workspace, runs setup, assigns credentials (a
// per-slot lease per §6), and starts the session, exactly as
// session-mode Bind does. The §6.4 per-slot directory tree under
// /workspace/slots/{slotId}/ is the adapter's responsibility, and the
// pod's /workspace/shared/ tree is shared read-only across the pod's
// slots.
//
// On success the caller owns the returned live adapter connection and
// the BindResult carries the slot's SlotID. Any failure after the slot
// reservation is returned so the caller can retry on another slot.
func (b *Binder) BindSlot(ctx context.Context, req SlotBindRequest) (*BindResult, error) {
	sandboxName, slotID, podIP, cl, err := b.connectSlot(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.materializeSlot(ctx, req, sandboxName, slotID, podIP, cl)
}

// ClaimSlot performs the §7.1 step-4 claim at session create for a §5.2
// concurrent-workspace pool (sessionPolicy.maxConcurrentSessions > 1). It
// reserves one of a pod's slots (the SandboxClaim plus the atomic
// active_slots increment) and runs the §15.5 version handshake to confirm
// the slot's pod is usable, then closes the handshake connection. It does
// not materialize the workspace, run setup, assign credentials, or start
// the runtime; those run at /finalize and /start through BindReservedSlot,
// which reconnects from the persisted binding.
//
// The slot is the concurrent-pool analog of the exclusive Claim: reserving
// it at create makes the §15.1 created-state invariant hold uniformly (a
// warm pod has been claimed) and gives the §4.5 created-expiry sweeper and
// the §4.6 /terminate path a durable binding to release the slot from.
// SlotID equals SessionID, so the binding is reconstructable from
// SandboxName + Pool + the session id, exactly like the exclusive path. A
// reservation-exhaustion sentinel (ErrNoConcurrentSlot, ErrTenantMismatch,
// ErrNoIdlePod) is returned unwrapped so the create handler maps it to the
// §7.1 atomicity envelope before the client uploads.
//
// spec: §4.1 (proposal), §7.1 step 4, line 75; §5.2 (concurrent slot
// reservation); §6.3 lines 358, 372.
func (b *Binder) ClaimSlot(ctx context.Context, req SlotBindRequest) (*ClaimResult, error) {
	phaseStart := time.Now()
	sandboxName, slotID, podIP, cl, err := b.connectSlot(ctx, req)
	if err != nil {
		// connectSlot returns a SlotBindError once the slot has been reserved
		// (a resolveSandbox/dial/handshake failure after the active_slots
		// increment); release the reservation so a failed create-time claim does
		// not leak the pod's active_slots. An exhaustion sentinel
		// (ErrNoConcurrentSlot/ErrTenantMismatch/ErrNoIdlePod) reserved no slot
		// and is returned unwrapped for the create handler's exhaustion mapping.
		var sbe *SlotBindError
		if errors.As(err, &sbe) {
			if relErr := b.ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID); relErr != nil {
				log.Printf("podsession: release reserved slot %s on pod %s after create-time claim handshake failure for session %s: %v",
					sbe.SlotID, sbe.Pod, req.SessionID, relErr)
			}
		}
		return nil, err
	}
	// ClaimSlot runs only the reservation and handshake; the setup chain
	// reconnects at BindReservedSlot, so the connection is not held across
	// the upload window.
	cl.Close()
	return &ClaimResult{
		SandboxName: sandboxName,
		Pool:        req.Pool,
		PodIP:       podIP,
		SlotID:      slotID,
		PodClaim:    time.Since(phaseStart),
	}, nil
}

// BindReservedSlot materializes the workspace, runs setup, assigns
// credentials, and starts the runtime on a slot already reserved at create
// by ClaimSlot. It reconnects to the reserved slot's pod from the persisted
// binding rather than reserving a fresh slot, so the §15.1 created-state
// pod-binding holds from create through start.
//
// On any failure after the reconnect it releases the reserved slot
// (ReleaseSlotReservation) before returning the error, the slot analog of
// the exclusive Prepare/Launch reclaim: a start that cannot reach `running`
// reclaims the slot the create-time reservation held, so the pod's
// active_slots is not leaked. The release runs exactly once here; the
// callers therefore do not release the create-time slot reservation again on
// a BindReservedSlot failure. The slot is not re-reserved and retried here,
// unlike BindSlot under the §5.2 retry policy; the start handler surfaces
// the failure to the client.
//
// On success the caller owns the returned live adapter connection. spec:
// §4.3, §4.4 (proposal), §5.2; §6.2 (pre-attached reclaim); §6.4 lines
// 401-405.
func (b *Binder) BindReservedSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID string) (*BindResult, error) {
	res, err := b.bindReservedSlot(ctx, req, sandboxName, slotID)
	if err != nil {
		// Release the reserved slot so a failed start does not leak the pod's
		// active_slots; the create-time reservation increment is rolled back by
		// the matching release. ReleaseSlotReservation is the slot-count half of
		// ReleaseSlot (the failed attempt closed its own adapter connection).
		if relErr := b.ReleaseSlotReservation(ctx, sandboxName, slotID); relErr != nil {
			log.Printf("podsession: release reserved slot %s on pod %s after start failure for session %s: %v",
				slotID, sandboxName, req.SessionID, relErr)
		}
		return nil, err
	}
	return res, nil
}

// bindReservedSlot reconnects to a slot reserved at create and runs the
// post-reservation materialize-and-launch sequence. BindReservedSlot wraps
// it and owns the reservation release on failure, so the slot-count rollback
// runs exactly once per failed start.
func (b *Binder) bindReservedSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID string) (*BindResult, error) {
	sb, err := b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, err)
	}
	podIP := sb.Status.PodIP
	addr := net.JoinHostPort(podIP, strconv.Itoa(b.AdapterPort))
	cl, err := b.DialAdapter(addr)
	if err != nil {
		return nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: dial reserved slot adapter at %s: %w", addr, err))
	}
	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err))
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		))
	}
	return b.materializeSlot(ctx, req, sandboxName, slotID, podIP, cl)
}

// materializeSlot runs the post-reservation §4.7 workspace-and-start
// sequence on a slot whose reservation and §15.5 handshake the caller
// already completed (BindSlot reserves a fresh slot, BindReservedSlot
// reconnects to one reserved at create). The slot gets its own workspace:
// it stages and finalizes the workspace, runs setup, assigns credentials (a
// per-slot lease per §6), and starts the session. Any failure closes the
// adapter connection, records the §5.2 failure counter, and returns a
// SlotBindError so the caller can release the reservation and retry.
func (b *Binder) materializeSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID, podIP string, cl *adapterclient.Client) (*BindResult, error) {
	// spec: §5.2 — a concurrent-session slot has its own per-slot workspace
	// (§6.4). Run the full §4.7 workspace-and-start sequence. Archive
	// extraction runs gateway-side (§7.4 line 448) exactly as in
	// session-mode Bind; the adapter re-validates symlinks against the
	// slot's actual /workspace/slots/{slotId}/current after promotion.
	allow := upload.RuntimeAllow{
		AllowSymlinks: req.ArchivePolicy.GetAllowSymlinks(),
		WorkspaceRoot: firstNonEmpty(req.ArchivePolicy.GetWorkspaceRoot(), archive.DefaultWorkspaceRoot),
	}
	// spec: §6.4 lines 401-405 — the slot's workspace materializes into
	// its own /workspace/slots/{slotId}/ tree. slotID is the per-slot
	// identifier the adapter keys the tree on.
	stagedPlan, stageWarnings, err := b.stageWorkspace(ctx, cl, req.SessionID, slotID, req.TenantID, req.Plan, allow)
	if err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureWorkspacePrep, req.Pool, sandboxName)
		return nil, b.slotBindError(sandboxName, slotID, slotFailureWorkspacePrep,
			fmt.Errorf("podsession: stage slot workspace on pod %s: %w", sandboxName, err))
	}
	warnings, err := cl.FinalizeWorkspaceSlot(ctx, req.SessionID, slotID, stagedPlan, req.ArchivePolicy, false)
	if err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureWorkspacePrep, req.Pool, sandboxName)
		return nil, b.slotBindError(sandboxName, slotID, slotFailureWorkspacePrep,
			fmt.Errorf("podsession: finalize slot workspace on pod %s: %w", sandboxName, err))
	}
	finalizeWarnings := append(stageWarnings, warnings...)
	setupOutputs, err := cl.RunSetupSlot(ctx, req.SessionID, slotID, stagedPlan.GetSetupCommands(), req.SetupPolicy)
	if err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureSetup, req.Pool, sandboxName)
		return nil, b.slotBindError(sandboxName, slotID, slotFailureSetup,
			&SetupCommandFailure{Pod: sandboxName, Cause: err, Outputs: setupOutputs})
	}

	if err := b.assignSlotCredentials(ctx, cl, req, slotID); err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureCredentialAssignment, req.Pool, sandboxName)
		return nil, b.slotBindError(sandboxName, slotID, slotFailureCredentialAssignment,
			fmt.Errorf("podsession: assign slot credentials on pod %s: %w", sandboxName, err))
	}
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
		// spec: §6.4 lines 385-405 — claim the slot rather than the whole pod.
		SlotID: slotID,
	}); err != nil {
		cl.Close()
		b.recordSlotFailure(slotFailureSessionStart, req.Pool, sandboxName)
		return nil, b.slotBindError(sandboxName, slotID, slotFailureSessionStart,
			fmt.Errorf("podsession: start slot session on pod %s: %w", sandboxName, err))
	}
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       podIP,
		SlotID:      slotID,
		// spec: §7.2 (per-slot routing) — carry the pool's
		// maxConcurrentSessions bound onto the binding so the executor's
		// per-slot message routing can detect the SLOT_ID_REQUIRED
		// routing-bug case (a concurrent pod with no resolved slot) and fail
		// closed rather than misdeliver. A BindSlot result is by definition a
		// maxConcurrentSessions > 1 bind.
		MaxConcurrentSessions: req.MaxConcurrentSessions,
		Adapter:               cl,
		WorkspacePlanWarnings: finalizeWarnings,
		SetupOutputs:          setupOutputs,
		// spec: §3.4 / §6.30 — carry the pool's recycle.enabled flag so
		// ReleaseSlot drives the §3.4 recycle disposition (patch the per-pod
		// claim bound → recycling, signal the whole-pod scrub) on the
		// occupancy-zero edge of a recycling concurrent pool rather than
		// deleting the claim.
		Recycle: req.Recycle,
	}, nil
}

// recordSlotFailure emits the §5.2 line 12 lenny_slot_failure_total
// counter for a slot bind stage that failed after the slot was reserved.
// It is a no-op when the binder has no SlotFailure hook.
//
// spec: §5.2 line 12.
func (b *Binder) recordSlotFailure(errorType, pool, podName string) {
	if b.SlotFailure != nil {
		b.SlotFailure(errorType, pool, podName)
	}
}

// assignSlotCredentials mints the slot's §4.9 credential leases and
// pushes them to the pod via AssignCredentials. Per §6 each
// concurrent-session slot holds an independent per-slot lease, so a
// rotation on one slot does not disrupt sibling slots. It is a no-op
// when the binder has no credential service or the request names no
// pools.
func (b *Binder) assignSlotCredentials(ctx context.Context, cl *adapterclient.Client, req SlotBindRequest, slotID string) error {
	hasPool := b.Credentials != nil && len(req.CredentialPools) > 0
	hasUser := b.UserCredentials != nil && len(req.UserCredentialProviders) > 0
	if !hasPool && !hasUser {
		return nil
	}
	leases := make(map[string]*adapterv1.CredentialLease, len(req.CredentialPools)+len(req.UserCredentialProviders))
	if hasPool {
		for provider, pool := range req.CredentialPools {
			lease, err := b.Credentials.AssignProto(pool, req.SessionID, req.PodSpiffeURI, req.TenantID)
			if err != nil {
				// §4.9 line 1220 pre-claim race: surface a typed error so the
				// caller can release the slot and emit the mismatch metric.
				return &CredentialAssignmentError{Provider: provider, Pool: pool, Err: err}
			}
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	if hasUser {
		// spec: §4.9 lines 1347-1351 — per-slot user-source leases mirror
		// the per-slot pool leases: each slot holds its own user lease.
		for _, provider := range req.UserCredentialProviders {
			lease, err := b.UserCredentials.MintProto(ctx, req.TenantID, req.UserID, req.SessionID, req.PodSpiffeURI, provider)
			if err != nil {
				return &CredentialAssignmentError{Provider: provider, Pool: "user", Err: err}
			}
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	// spec: §6.1 line 28 — the slot's lease is written to its own per-slot
	// credential file so a rotation on a sibling slot does not disrupt it.
	return cl.AssignCredentialsSlot(ctx, req.SessionID, slotID, leases)
}

// connectSlot reserves a concurrent-session slot from the pool, resolves
// the slot's pod adapter address, dials it, and runs the §15.5 version
// handshake. On success the caller owns cl and must close it once the
// session ends or on any later failure.
//
// A podclaim.ErrNoConcurrentSlot or podclaim.ErrTenantMismatch from the
// SlotClaimer is returned unwrapped so the gateway's session-creation
// handler can map it to WARM_POOL_EXHAUSTED with the §5.2
// "concurrent_slots_exhausted" reason.
func (b *Binder) connectSlot(ctx context.Context, req SlotBindRequest) (sandboxName, slotID, podIP string, cl *adapterclient.Client, err error) {
	claimer := &podclaim.SlotClaimer{
		Client:         b.Client,
		Namespace:      b.Namespace,
		Counter:        b.SlotCounter,
		OnSlotConflict: b.SlotConflict,
		OnRehydrate:    b.Rehydration,
	}
	res, err := claimer.ClaimSlot(ctx, podclaim.SlotRequest{
		Pool:                  req.Pool,
		SessionID:             req.SessionID,
		TenantID:              req.TenantID,
		MaxConcurrentSessions: req.MaxConcurrentSessions,
		MaxPodUptimeSeconds:   req.MaxPodUptimeSeconds,
	})
	if err != nil {
		// The §5.2 line 519 exhaustion sentinels are returned unwrapped
		// for the caller's errors.Is check, which maps them to
		// WARM_POOL_EXHAUSTED with the right details.reason: ErrNoIdlePod
		// → "no_idle_pods" (pool holds no pods), ErrNoConcurrentSlot and
		// ErrTenantMismatch → "concurrent_slots_exhausted".
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
			errors.Is(err, podclaim.ErrTenantMismatch) ||
			errors.Is(err, podclaim.ErrNoIdlePod) {
			return "", "", "", nil, err
		}
		return "", "", "", nil, fmt.Errorf("podsession: claim concurrent slot: %w", err)
	}
	sandboxName = res.SandboxName
	slotID = res.SlotID

	// Past this point a slot is reserved on sandboxName. Any failure is
	// wrapped in a SlotBindError so the caller can release the reservation,
	// count it toward the pod's §5.2 fail/leak window, and retry on a fresh
	// slot. The reservation-bearing failures use the slotFailureConnect
	// stage (no lenny_slot_failure_total emission — that counter labels the
	// four post-connection bind stages).
	sb, err := b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, err)
	}
	podIP = sb.Status.PodIP

	addr := net.JoinHostPort(podIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: dial slot adapter at %s: %w", addr, err))
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err))
	}
	if resp.GetIncompatible() {
		cl.Close()
		return "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		))
	}
	return sandboxName, slotID, podIP, cl, nil
}

// slotBindError wraps a post-reservation slot failure with the pod and
// slot it belongs to so the §5.2 retry policy can release and account for
// it. spec: §5.2 "Concurrent-workspace slot retry policy".
func (b *Binder) slotBindError(sandboxName, slotID, stage string, err error) *SlotBindError {
	return &SlotBindError{Pod: sandboxName, SlotID: slotID, Stage: stage, Err: err}
}

// ReleaseSlotReservation releases a §5.2 slot reservation (the SandboxClaim
// and the active_slots count) without an adapter Shutdown. The §5.2 retry
// policy calls it after a failed bind so the retry lands on a genuinely
// fresh slot and the pod's active_slots is not leaked by the failed
// attempt. It is the slot-count half of ReleaseSlot, reused here because
// the failed attempt already closed its adapter connection.
func (b *Binder) ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string) error {
	claimer := &podclaim.SlotClaimer{Client: b.Client, Namespace: b.Namespace, Counter: b.SlotCounter}
	// recycle=false: a released reservation after a failed bind is a slot-count
	// rollback, not the occupancy-zero recycle edge, so it never patches the
	// claim to `recycling` or arms the missing-report timeout. leaked=false: a
	// reservation rollback frees a slot that never held runtime resources, so
	// the counter must decrement (the slot is not leaked). spec: §5.2 (slot
	// retry releases the reservation), §3.4, §6.2 (leaked slot remains counted).
	return claimer.ReleaseSlot(ctx, sandboxName, slotID, false, false)
}

// ReleaseSlot tears down a concurrent-session slot when its session ends.
// It shuts the slot's runtime down through the adapter, closes the adapter
// connection, and decrements the pod's §5.2 Redis slot counter; when the
// last slot drains (the counter reaches zero) the per-pod SandboxClaim is
// deleted so the pod returns to the pool.
//
// Unlike session-mode Release, ReleaseSlot does not delete the claim while
// sibling slots remain: a concurrent-session pod hosts up to
// maxConcurrentSessions slots on one per-pod claim, and the claim spans the
// whole occupancy episode. The Redis-counter decrement and the
// claim-delete-on-last edge are handled by podclaim.SlotClaimer.ReleaseSlot.
// The adapter Shutdown is best-effort.
func (b *Binder) ReleaseSlot(ctx context.Context, result *BindResult) error {
	// spec: §6.2 (leaked slot remains counted) — a slot whose adapter cleanup
	// did not complete cleanly is leaked: its resources are not reclaimed until
	// pod termination, so the Redis slot counter must keep counting it and the
	// gateway must not over-assign a new slot into the leaked slot's occupancy.
	// A transport error on ShutdownSlot is treated as leaked too (fail closed:
	// on doubt the slot stays counted rather than freeing occupancy the adapter
	// may still hold).
	leaked := false
	if result.Adapter != nil {
		// spec: §6.4 lines 401-405 — tear down just this slot (its runtime
		// and per-slot tree); sibling slots on the pod keep running.
		cleanly, err := result.Adapter.ShutdownSlot(ctx, result.SessionID, result.SlotID)
		leaked = err != nil || !cleanly
		result.Adapter.Close()
	}
	// spec: §7.1 line 52 (step 23) — release the slot session's §4.9
	// credential leases back to the pool, the same teardown session-mode
	// Release runs. The pod and its sibling slots stay live.
	b.releaseCredentials(result.SessionID)
	claimer := &podclaim.SlotClaimer{
		Client:    b.Client,
		Namespace: b.Namespace,
		Counter:   b.SlotCounter,
		// §3.4: a recycling concurrent-session pool arms the missing-report
		// timeout on the occupancy-zero edge (the last slot draining), the same
		// gateway-side timeout session-mode Release arms.
		RecycleBoundary: b.RecycleBoundary,
	}
	return claimer.ReleaseSlot(ctx, result.SandboxName, result.SessionID, result.Recycle, leaked)
}

// DrainSandbox requests the whole-pod retirement of a concurrent-session
// pod that crossed the §5.2 unhealthy-slot threshold (ceil(maxConcurrent/2)
// slots failed or leaked within the rolling window) rather than releasing a
// single slot. The gateway stamps the §4.6.3 lenny.dev/drain-request
// annotation on the agent Pod; the WarmPoolController consumes it and writes
// the draining transition on Sandbox.status. The gateway never writes
// Sandbox.status itself (§4.6.3 ownership decomposition: the
// WarmPoolController is the sole writer), so the unhealthy-threshold drain
// is routed through the annotation rather than a gateway phase write.
//
// The stamp is idempotent: a re-request overwrites the annotation with a
// fresh instant, and a pod already gone is a no-op (a pod with no slots
// needs no drain).
//
// spec: §4.6.3 (gateway stamps drain-request; WarmPoolController writes the
// drain); §5.2 "whole-pod replacement trigger". The WarmPoolController-side
// projection that consumes the annotation lands in the WPC
// occupancy-projection step.
func (b *Binder) DrainSandbox(ctx context.Context, sandboxName string) error {
	return podclaim.StampDrainRequest(ctx, b.Client, b.Namespace, sandboxName, time.Now())
}
