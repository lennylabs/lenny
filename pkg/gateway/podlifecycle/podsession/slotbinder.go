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

	"github.com/lennylabs/lenny/pkg/adapter/slotlayout"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/upload"
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
	// MaxPodUptimeSeconds is the §6.2 concurrent-session
	// pod-uptime retirement cap. The slot-claim path skips a candidate pod
	// whose uptime exceeds it before assigning a slot; the skip is a
	// read-only placement filter and the gateway does not write
	// Sandbox.status. The WarmPoolController owns the resulting draining
	// transition, derived from the pod CreationTimestamp (§4.6.1). Zero
	// leaves uptime retirement off.
	MaxPodUptimeSeconds int64
	// Plan is the per-slot workspace the adapter materializes under
	// /workspace/slots/{sessionId}/ before start. spec: §5.2 — concurrent
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
	// spec: §7.4 — F-7.4.4.
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
	// provider resolved to a user credential. spec: §4.9.
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
	// by ResolvePool. On a recycling concurrent-session pool (the
	// "Concurrent" preset, maxConcurrentSessions > 1 with recycle.enabled:
	// true), when the last slot drains cleanly ReleaseSlot patches the per-pod
	// claim bound → recycling and signals the whole-pod scrub (the §4.7 recycle
	// disposition) rather than deleting the claim, so the adapter's
	// ReportPodScrub drives recycle vs. retire and the occupancy-zero whole-pod
	// scrub clears the cross-cohort residue (shared /tmp, /dev/shm, surviving
	// processes). False for a non-recycling concurrent pool (the "Bounded
	// cohort" preset), where the pod terminates after the cohort drains. spec:
	// §6.2 (occupancy-zero recycle edge on a recycling pod).
	Recycle bool
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2 whole-pod scrub
	// parameters (folded onto PoolMatch from the sessionPolicy mirror) carried
	// so the occupancy-zero recycle Shutdown delivers them to the adapter
	// without re-resolving the pool at the release boundary. materializeSlot
	// copies them onto the BindResult so ReleaseSlot builds them into the §4.7
	// RecycleScrub sub-message (with PodId == SandboxName) on the last-slot-drain
	// recycle edge. Empty/zero on a non-recycling concurrent pool. The scrub
	// profile is not carried on the wire; the gateway routes the §5.2 step-7
	// vm-restart retire on the recycle policy in its own runtime store (C4).
	// spec: §5.2 (whole-pod scrub trigger), §4.6.3.
	CleanupCommands       []string
	CleanupTimeoutSeconds int
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
// /workspace/slots/{sessionId}/ is the adapter's responsibility, and the
// pod's /workspace/shared/ tree is shared read-only across the pod's
// slots.
//
// On success the caller owns the returned live adapter connection and
// the BindResult carries the slot's SlotID. Any failure after the slot
// reservation is returned so the caller can retry on another slot.
func (b *Binder) BindSlot(ctx context.Context, req SlotBindRequest) (*BindResult, error) {
	sandboxName, slotID, podIP, workspaceBase, cl, err := b.connectSlot(ctx, req)
	if err != nil {
		return nil, err
	}
	return b.materializeSlot(ctx, req, sandboxName, slotID, podIP, workspaceBase, cl)
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
// spec: §4.1 (proposal), §7.1 step 4; §5.2 (concurrent slot
// reservation); §6.3.
func (b *Binder) ClaimSlot(ctx context.Context, req SlotBindRequest) (*ClaimResult, error) {
	phaseStart := time.Now()
	// The reported workspace base is discarded here: the create-time claim
	// persists no workspace column, and BindReservedSlot re-reports the
	// base on its own handshake when the bind reconnects. spec: §6.4.
	sandboxName, slotID, podIP, _, cl, err := b.connectSlot(ctx, req)
	if err != nil {
		// connectSlot returns a SlotBindError once the slot has been reserved
		// (a resolveSandbox/dial/handshake failure after the active_slots
		// increment); release the reservation so a failed create-time claim does
		// not leak the pod's active_slots. An exhaustion sentinel
		// (ErrNoConcurrentSlot/ErrTenantMismatch/ErrNoIdlePod) reserved no slot
		// and is returned unwrapped for the create handler's exhaustion mapping.
		// The slot was reserved before any workspace RPC, so the adapter holds
		// nothing for it and the release is not leaked.
		var sbe *SlotBindError
		if errors.As(err, &sbe) {
			if relErr := b.ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID, false); relErr != nil {
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
// §4.3, §4.4 (proposal), §5.2; §6.2 (pre-attached reclaim); §6.4.
func (b *Binder) BindReservedSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID string) (*BindResult, error) {
	res, err := b.bindReservedSlot(ctx, req, sandboxName, slotID)
	if err != nil {
		// Release the reserved slot so a failed start does not leak the pod's
		// active_slots; the create-time reservation increment is rolled back by
		// the matching release. The pod-side reclaim already ran at the failure
		// site (materializeSlot), so the release carries the disposition it
		// produced: a reclaim not acknowledged clean keeps the slot counted.
		//
		// spec: §7.1 (normal flow); §6.2 (pod state machine).
		var sbe *SlotBindError
		leaked := errors.As(err, &sbe) && sbe.Leaked
		if relErr := b.ReleaseSlotReservation(ctx, sandboxName, slotID, leaked); relErr != nil {
			log.Printf("podsession: release reserved slot %s on pod %s after start failure for session %s: %v",
				slotID, sandboxName, req.SessionID, relErr)
			// A reservation that could not be released stays counted, so the
			// failure is booked as a leak by the caller's accounting.
			if sbe != nil {
				sbe.Leaked = true
			}
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
	return b.materializeSlot(ctx, req, sandboxName, slotID, podIP, resp.GetWorkspaceBase(), cl)
}

// materializeSlot runs the post-reservation §4.7 workspace-and-start
// sequence on a slot whose reservation and §15.5 handshake the caller
// already completed (BindSlot reserves a fresh slot, BindReservedSlot
// reconnects to one reserved at create). It is the compensating wrapper
// around materializeSlotStages: every post-connection stage runs inside it,
// so no stage can be added later without the reclaim.
//
// Each run is one §4.7.1 bind attempt: it mints its token before its first
// pod-side RPC, and the stages carry it on PrepareWorkspace,
// FinalizeWorkspace, RunSetup, and AssignCredentials, each with mid_session
// false. StartSession carries no token (§4.7.1 carriage table).
//
// On a failure it sends the compensating Shutdown naming this attempt's
// token on the still-open connection, records the slot's disposition on the
// returned SlotBindError, releases the §4.9 leases this attempt minted, and
// only then closes the connection. The stages close nothing: a stage that
// closed the connection would hand the compensation a closed one, the
// reclaim would fail, and every failed bind would be booked leaked. A
// successful bind returns the connection open in BindResult.Adapter.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract); §5.2
// (pool configuration and execution modes); §6.2 (pod state machine).
func (b *Binder) materializeSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID, podIP, workspaceBase string, cl *adapterclient.Client) (*BindResult, error) {
	// spec: §4.7.1 (role and gateway RPC contract) — one token per attempt,
	// minted before the attempt's first pod-side RPC.
	bindAttempt := newBindAttempt()
	res, minted, err := b.materializeSlotStages(ctx, req, sandboxName, slotID, podIP, workspaceBase, cl, bindAttempt)
	if err == nil {
		return res, nil
	}
	// spec: §7.1 (normal flow). Every stage is post-connection, so the reclaim
	// is unconditional. A typed refusal is compensated too; §4.7.1 answers it
	// superseded when the entry belongs to another attempt.
	var sbe *SlotBindError
	if errors.As(err, &sbe) {
		_, cleanly, cerr := b.compensateAndNote(ctx, cl, req.SessionID, bindAttempt,
			req.CleanupTimeoutSeconds, req.MaxConcurrentSessions, req.Pool, sandboxName, slotID, err)
		// spec: §6.2 (pod state machine); §7.1 (normal flow). Leaked is exactly
		// "not acknowledged clean": the RPC error and the clean-exit flag decide
		// it for every outcome, and the outcome itself never enters it.
		sbe.Leaked = cerr != nil || !cleanly
	}
	// spec: §7.1 (normal flow); §4.9 (credential leasing service). Releases the
	// leases this attempt minted, never the session's, on every failure. It
	// must not move inside the errors.As guard: the credential-assignment stage
	// can fail, or be refused, after minting.
	b.releaseAttemptCredentials(minted)
	cl.Close()
	return nil, err
}

// materializeSlotStages is the stage runner materializeSlot wraps. The slot
// gets its own workspace: it stages and finalizes the workspace, runs setup,
// assigns credentials (a per-slot lease per §6), and starts the session. Any
// failure records the §5.2 failure counter and returns a SlotBindError so the
// wrapper can compensate and the caller can release the reservation and
// retry. It returns the identifiers of the §4.9 leases it minted, on success
// and on failure alike, so the wrapper can release exactly those. It never
// closes cl; the wrapper owns the connection on every path.
func (b *Binder) materializeSlotStages(ctx context.Context, req SlotBindRequest, sandboxName, slotID, podIP, workspaceBase string, cl *adapterclient.Client, bindAttempt string) (*BindResult, []string, error) {
	// spec: §5.2 — a concurrent-session slot has its own per-slot workspace
	// (§6.4). Run the full §4.7 workspace-and-start sequence. Archive
	// extraction runs gateway-side (§7.4) exactly as in
	// session-mode Bind; the adapter re-validates symlinks against the
	// slot's actual /workspace/slots/{sessionId}/current after promotion.
	allow := upload.RuntimeAllow{
		AllowSymlinks: req.ArchivePolicy.GetAllowSymlinks(),
		// spec: §6.4; §13.4 — the containment root symlink targets are
		// canonicalized against is this bind's own slot cwd, derived from
		// the base the adapter reported and the session identifier, so the
		// gateway-side extraction matches where the adapter re-validates
		// after promotion.
		WorkspaceRoot: firstNonEmpty(req.ArchivePolicy.GetWorkspaceRoot(),
			slotlayout.SessionCurrentDir(workspaceBase, req.SessionID)),
	}
	// spec: §6.4 — the slot's workspace materializes into
	// its own /workspace/slots/{sessionId}/ tree, which the adapter keys on
	// the session identifier the request already names.
	stagedPlan, stageWarnings, err := b.stageWorkspace(ctx, cl, req.SessionID, req.TenantID, req.Plan, allow, bindAttempt)
	if err != nil {
		b.recordSlotFailure(slotFailureWorkspacePrep, req.Pool, sandboxName)
		return nil, nil, b.slotBindError(sandboxName, slotID, slotFailureWorkspacePrep,
			fmt.Errorf("podsession: stage slot workspace on pod %s: %w", sandboxName, err))
	}
	warnings, err := cl.FinalizeWorkspace(ctx, req.SessionID, stagedPlan, req.ArchivePolicy, bindAttempt, false)
	if err != nil {
		b.recordSlotFailure(slotFailureWorkspaceFinalize, req.Pool, sandboxName)
		return nil, nil, b.slotBindError(sandboxName, slotID, slotFailureWorkspacePrep,
			fmt.Errorf("podsession: finalize slot workspace on pod %s: %w", sandboxName, err))
	}
	finalizeWarnings := append(stageWarnings, warnings...)
	setupOutputs, err := cl.RunSetup(ctx, req.SessionID, stagedPlan.GetSetupCommands(), req.SetupPolicy, bindAttempt)
	if err != nil {
		b.recordSlotFailure(slotFailureSetup, req.Pool, sandboxName)
		return nil, nil, b.slotBindError(sandboxName, slotID, slotFailureSetup,
			&SetupCommandFailure{Pod: sandboxName, Cause: err, Outputs: setupOutputs})
	}

	minted, err := b.assignSlotCredentials(ctx, cl, req, bindAttempt)
	if err != nil {
		b.recordSlotFailure(slotFailureCredentialAssignment, req.Pool, sandboxName)
		return nil, minted, b.slotBindError(sandboxName, slotID, slotFailureCredentialAssignment,
			fmt.Errorf("podsession: assign slot credentials on pod %s: %w", sandboxName, err))
	}
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		b.recordSlotFailure(slotFailureSessionStart, req.Pool, sandboxName)
		return nil, minted, b.slotBindError(sandboxName, slotID, slotFailureSessionStart,
			fmt.Errorf("podsession: start slot session on pod %s: %w", sandboxName, err))
	}
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       podIP,
		SlotID:      slotID,
		Adapter:     cl,
		// spec: §6.4; §7.3 — the slot bind reports the same workspace base
		// the base-mode bind reports, verbatim, so the one persist site
		// derives `<base>/slots/{sessionId}/current` once for both paths.
		WorkspaceBase:         workspaceBase,
		WorkspacePlanWarnings: finalizeWarnings,
		SetupOutputs:          setupOutputs,
		// spec: §5.2 — carry the pool's recycle.enabled flag so
		// ReleaseSlot drives the §4.7 recycle disposition (patch the per-pod
		// claim bound → recycling, signal the whole-pod scrub) on the
		// occupancy-zero edge of a recycling concurrent pool rather than
		// deleting the claim.
		Recycle: req.Recycle,
		// spec: §5.2 (whole-pod scrub trigger) — carry the pool's cleanup
		// parameters so the occupancy-zero recycle Shutdown delivers them to the
		// adapter's whole-pod scrub without re-resolving the pool at release.
		CleanupCommands:       req.CleanupCommands,
		CleanupTimeoutSeconds: req.CleanupTimeoutSeconds,
	}, minted, nil
}

// slotCleanupFloor is the §5.2 per-slot cleanup floor: the per-slot
// cleanup timeout never drops below it.
const slotCleanupFloor = 5 * time.Second

// slotCleanupBudget is the §5.2 per-slot cleanup timeout,
// max(cleanupTimeoutSeconds / maxConcurrentSessions, 5) seconds. §5.2
// assigns that figure to the adapter's own cleanup enforcement; the gateway
// reuses it as its own give-up bound on the compensating Shutdown rather than
// inventing a constant. cleanupTimeoutSeconds is optional, so an unset pool
// yields slotCleanupFloor, and a non-positive maxConcurrentSessions (an
// exclusive pool, which keeps no per-slot bound) divides by one.
//
// spec: §5.2 (pool configuration and execution modes); §7.1 (normal flow).
func slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32) time.Duration {
	n := time.Duration(maxConcurrentSessions)
	if n < 1 {
		n = 1
	}
	perSlot := time.Duration(cleanupTimeoutSeconds) * time.Second / n
	return max(perSlot, slotCleanupFloor)
}

// Values of the error_type label on the §16.1
// lenny_slot_compensation_superseded_total series.
const (
	// compensationCauseRefusal labels a compensation whose attempt the adapter
	// refused under §4.7.1 (superseded or already started).
	compensationCauseRefusal = "refusal"
	// compensationCauseFailure labels a compensation whose attempt failed for
	// any other reason.
	compensationCauseFailure = "failure"
)

// compensationCause returns the error_type label value for a compensation
// whose attempt failed with err: `refusal` when the adapter refused the
// attempt with a §4.7.1 slot-bind refusal, `failure` otherwise. It is the one
// statement of that value; both compensating call sites pass it the attempt's
// error. spec: §16.1 (metrics); §4.7.1 (role and gateway RPC contract).
func compensationCause(err error) string {
	if adapterclient.IsSlotBindRefusal(err) {
		return compensationCauseRefusal
	}
	return compensationCauseFailure
}

// compensateFailedSlotBind reclaims the pod-side state a failed bind created,
// naming the bind attempt that created it, and returns the outcome the adapter
// answered. It touches pod-side state only. The gateway-side §4.9 credential
// leases belong to the caller, because the two bind entry paths mint them
// inside materializeSlotStages and the resume path mints none.
//
// It returns the outcome rather than a leaked boolean so the caller maps a
// value it recognizes and counts one it does not, instead of a false answer
// travelling as a disposition. An RPC error is reported as such.
//
// The context is detached from the caller's: the residue class this exists
// for arises when the caller's context expired during StartSession, so a
// reclaim issued on that context would fail in the one case that leaves a
// runtime running for an abandoned session.
//
// The call carries two bounds and they differ deliberately. The fourth
// argument of ShutdownReclaim is the graceful window the adapter spends on
// the runtime close, and the RPC deadline is the budget, which outlasts that
// window so the gateway does not give up on the adapter's SIGTERM pivot. A
// cleanup whose tree removal outruns the remaining budget answers nothing in
// time, and that is the unanswered reclaim §7.1 accounts. The §11.4 revoke
// fan-out holds the same relation between its RPC timeout and the shorter
// graceful window it sends.
//
// The reclaim reuses the connection the failed stage holds for cost; §7.1
// states that the fence does not depend on it. The reason string maps onto
// the intra-pod terminate frame's session_complete default at the adapter,
// so the compensation mints no new wire value.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract); §5.2
// (pool configuration and execution modes).
func (b *Binder) compensateFailedSlotBind(
	ctx context.Context, cl *adapterclient.Client,
	sessionID, bindAttempt string,
	cleanupTimeoutSeconds int, maxConcurrentSessions int32,
	sandboxName, slotID string,
) (outcome adapterv1.SlotReclaimOutcome, exitedCleanly bool, err error) {
	budget := slotCleanupBudget(cleanupTimeoutSeconds, maxConcurrentSessions)
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()
	outcome, exitedCleanly, err = cl.ShutdownReclaim(rctx, sessionID, "slot_bind_failed", budget/2, bindAttempt)
	if err != nil {
		log.Printf("podsession: compensating Shutdown for slot %s on pod %s (session %s) failed: %v",
			slotID, sandboxName, sessionID, err)
	}
	return outcome, exitedCleanly, err
}

// compensateAndNote runs compensateFailedSlotBind for an attempt that failed
// with cause and forwards the outcome to noteCompensationOutcome labeled by
// compensationCause(cause). Both compensating call sites, the slot bind
// wrapper and the resume failure branch, go through it, so a compensation is
// never sent without its outcome being counted.
//
// spec: §7.1 (normal flow); §16.1 (metrics).
func (b *Binder) compensateAndNote(
	ctx context.Context, cl *adapterclient.Client,
	sessionID, bindAttempt string,
	cleanupTimeoutSeconds int, maxConcurrentSessions int32,
	pool, sandboxName, slotID string, cause error,
) (adapterv1.SlotReclaimOutcome, bool, error) {
	outcome, cleanly, err := b.compensateFailedSlotBind(ctx, cl, sessionID, bindAttempt,
		cleanupTimeoutSeconds, maxConcurrentSessions, sandboxName, slotID)
	b.noteCompensationOutcome(outcome, compensationCause(cause), pool, sandboxName)
	return outcome, cleanly, err
}

// reclaimOutcomeSuperseded is the outcome value the SlotReclaim hook receives
// for a compensation the adapter answered superseded.
const reclaimOutcomeSuperseded = "superseded"

// noteCompensationOutcome forwards a compensation's outcome to the
// SlotReclaim hook backing the §16.1 lenny_slot_compensation_superseded_total
// series. It fires only on SLOT_RECLAIM_OUTCOME_SUPERSEDED, the case in which
// the reclaim released nothing because the adapter held an entry the
// compensation was not addressed to, so the branch exists once. It is a no-op
// when the binder has no SlotReclaim hook. cause is the error_type label
// value compensationCause supplies.
//
// spec: §16.1 (metrics); §4.7.1 (role and gateway RPC contract).
func (b *Binder) noteCompensationOutcome(outcome adapterv1.SlotReclaimOutcome, cause, pool, podName string) {
	if b.SlotReclaim == nil || outcome != adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED {
		return
	}
	b.SlotReclaim(reclaimOutcomeSuperseded, cause, pool, podName)
}

// releaseAttemptCredentials releases, by identifier, the §4.9 leases one
// failed bind attempt minted. It never walks the session's leases: the slot
// identifier is the session identifier, so a session-wide release after a
// successor attempt has assigned would strip the successor's leases. It is a
// no-op when the binder has no credential service or the attempt minted
// nothing. The session-wide release stays on the session-end path.
//
// spec: §7.1 (normal flow); §4.9 (credential leasing service).
func (b *Binder) releaseAttemptCredentials(leaseIDs []string) {
	if b.Credentials == nil {
		return
	}
	for _, id := range leaseIDs {
		if id != "" {
			b.Credentials.Release(id)
		}
	}
}

// recordSlotFailure emits the §5.2 lenny_slot_failure_total
// counter for a slot bind stage that failed after the slot was reserved.
// It is a no-op when the binder has no SlotFailure hook.
//
// spec: §5.2.
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
// pools. bindAttempt is the calling attempt's §4.7.1 token, carried on the
// AssignCredentials request.
//
// It returns the identifiers of the leases it minted, on failure as well as
// on success, so a failed attempt releases exactly its own leases rather
// than every lease its session holds. spec: §7.1 (normal flow); §4.9
// (credential leasing service).
func (b *Binder) assignSlotCredentials(ctx context.Context, cl *adapterclient.Client, req SlotBindRequest, bindAttempt string) ([]string, error) {
	hasPool := b.Credentials != nil && len(req.CredentialPools) > 0
	hasUser := b.UserCredentials != nil && len(req.UserCredentialProviders) > 0
	if !hasPool && !hasUser {
		return nil, nil
	}
	leases := make(map[string]*adapterv1.CredentialLease, len(req.CredentialPools)+len(req.UserCredentialProviders))
	var minted []string
	if hasPool {
		for provider, pool := range req.CredentialPools {
			lease, err := b.Credentials.AssignProto(pool, req.SessionID, req.PodSpiffeURI, req.TenantID)
			if err != nil {
				// §4.9 pre-claim race: surface a typed error so the
				// caller can release the slot and emit the mismatch metric.
				return minted, &CredentialAssignmentError{Provider: provider, Pool: pool, Err: err}
			}
			minted = append(minted, lease.GetLeaseId())
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	if hasUser {
		// spec: §4.9 — per-slot user-source leases mirror
		// the per-slot pool leases: each slot holds its own user lease.
		for _, provider := range req.UserCredentialProviders {
			lease, err := b.UserCredentials.MintProto(ctx, req.TenantID, req.UserID, req.SessionID, req.PodSpiffeURI, provider)
			if err != nil {
				return minted, &CredentialAssignmentError{Provider: provider, Pool: "user", Err: err}
			}
			minted = append(minted, lease.GetLeaseId())
			lease.Provider = provider
			leases[provider] = lease
		}
	}
	// spec: §6.1 — the lease is written to the session's own per-slot
	// credential file so a rotation on a co-tenant does not disrupt it.
	return minted, cl.AssignCredentials(ctx, req.SessionID, leases, bindAttempt)
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
func (b *Binder) connectSlot(ctx context.Context, req SlotBindRequest) (sandboxName, slotID, podIP, workspaceBase string, cl *adapterclient.Client, err error) {
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
		// The §5.2 exhaustion sentinels are returned unwrapped
		// for the caller's errors.Is check, which maps them to
		// WARM_POOL_EXHAUSTED with the right details.reason: ErrNoIdlePod
		// → "no_idle_pods" (pool holds no pods), ErrNoConcurrentSlot and
		// ErrTenantMismatch → "concurrent_slots_exhausted".
		if errors.Is(err, podclaim.ErrNoConcurrentSlot) ||
			errors.Is(err, podclaim.ErrTenantMismatch) ||
			errors.Is(err, podclaim.ErrNoIdlePod) {
			return "", "", "", "", nil, err
		}
		return "", "", "", "", nil, fmt.Errorf("podsession: claim concurrent slot: %w", err)
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
		return "", "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, err)
	}
	podIP = sb.Status.PodIP

	addr := net.JoinHostPort(podIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return "", "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: dial slot adapter at %s: %w", addr, err))
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return "", "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect,
			fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err))
	}
	if resp.GetIncompatible() {
		cl.Close()
		return "", "", "", "", nil, b.slotBindError(sandboxName, slotID, slotFailureConnect, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		))
	}
	return sandboxName, slotID, podIP, resp.GetWorkspaceBase(), cl, nil
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
// attempt. It is the slot-count half of ReleaseSlot. It sends nothing to the
// adapter because the pod-side reclaim already ran at the failure site, on
// the connection the failed attempt held (materializeSlot, Binder.Resume);
// this function releases the reservation afterwards.
//
// leaked is the disposition that reclaim produced. It is false for the
// connect stage, where the slot was reserved before any workspace RPC and
// the adapter holds nothing, and for a reclaim acknowledged clean; it is true
// for a reclaim not acknowledged clean, which keeps the slot counted so the
// gateway does not over-assign into occupancy the adapter may still hold.
//
// The release runs on a context detached from the caller's and bounded by
// the §5.2 per-slot cleanup floor, because §7.1 requires it even when the
// failing RPC's context is already cancelled or past its deadline.
//
// spec: §7.1 (normal flow); §5.2 (slot retry releases the reservation);
// §4.7 (recycle disposition); §6.2 (leaked slot remains counted).
func (b *Binder) ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string, leaked bool) error {
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), slotCleanupFloor)
	defer cancel()
	claimer := &podclaim.SlotClaimer{Client: b.Client, Namespace: b.Namespace, Counter: b.SlotCounter}
	// recycle=false: a released reservation after a failed bind is a slot-count
	// rollback, not the occupancy-zero recycle edge, so it never patches the
	// claim to `recycling` or arms the missing-report timeout. The recycled
	// signal is discarded: recycle=false never returns it.
	_, err := claimer.ReleaseSlot(rctx, sandboxName, false, leaked)
	return err
}

// ReleaseSlot tears down a concurrent-session slot when its session ends.
// It shuts the slot's runtime down through the adapter and decrements the
// pod's §5.2 Redis slot counter; when the last slot drains cleanly (the counter
// reaches zero) on a recycling pool the per-pod SandboxClaim is patched
// bound → recycling and the adapter runs the §5.2 whole-pod scrub, and on a
// non-recycling pool the claim is deleted so the pod returns to the pool.
//
// Unlike session-mode Release, ReleaseSlot does not delete the claim while
// sibling slots remain: a concurrent-session pod hosts up to
// maxConcurrentSessions slots on one per-pod claim, and the claim spans the
// whole occupancy episode. The Redis-counter decrement and the
// claim-delete-on-last edge are handled by podclaim.SlotClaimer.ReleaseSlot.
// The adapter Shutdown is best-effort.
//
// On the occupancy-zero recycle edge (every slot released cleanly and the pool
// recycles) SlotClaimer.ReleaseSlot returns recycled=true; ReleaseSlot then
// sends the adapter the whole-pod recycle Shutdown over the still-open per-slot
// connection so the adapter keeps the pod process alive, runs the §5.2 scrub,
// and reports its outcome via ReportPodScrub. The adapter connection is closed
// only after that recycle Shutdown, so the whole-pod scrub trigger travels the
// same live connection the ending session's Shutdown used. spec: §5.2 (whole-pod
// scrub trigger); §4.7 (Shutdown recycle disposition).
func (b *Binder) ReleaseSlot(ctx context.Context, result *BindResult) error {
	// spec: §6.2 (leaked slot remains counted) — a slot whose adapter cleanup
	// did not complete cleanly is leaked: its resources are not reclaimed until
	// pod termination, so the Redis slot counter must keep counting it and the
	// gateway must not over-assign a new slot into the leaked slot's occupancy.
	// A transport error on that Shutdown is treated as leaked too (fail closed:
	// on doubt the slot stays counted rather than freeing occupancy the adapter
	// may still hold).
	leaked := false
	if result.Adapter != nil {
		// spec: §6.4 — tear down the named session (its runtime and its
		// per-slot tree); a co-tenant on the pod keeps running. The
		// connection is held open past this call: on the occupancy-zero recycle
		// edge below the whole-pod recycle Shutdown reuses it before it is closed.
		cleanly, err := result.Adapter.Shutdown(ctx, result.SessionID, "", 0)
		leaked = err != nil || !cleanly
		defer result.Adapter.Close()
	}
	// spec: §7.1 — release the slot session's §4.9
	// credential leases back to the pool, the same teardown session-mode
	// Release runs. The pod and its sibling slots stay live.
	b.releaseCredentials(result.SessionID)
	claimer := &podclaim.SlotClaimer{
		Client:    b.Client,
		Namespace: b.Namespace,
		Counter:   b.SlotCounter,
		// §4.7: a recycling concurrent-session pool arms the missing-report
		// timeout on the occupancy-zero edge (the last slot draining), the same
		// gateway-side timeout session-mode Release arms.
		RecycleBoundary: b.RecycleBoundary,
	}
	recycled, err := claimer.ReleaseSlot(ctx, result.SandboxName, result.Recycle, leaked)
	if err != nil {
		return err
	}
	if recycled && result.Adapter != nil {
		// spec: §5.2 (whole-pod scrub trigger); §4.7 (Shutdown recycle
		// disposition). The last slot released cleanly, the claim is now
		// `recycling`, and the missing-report timeout is armed. Send the
		// whole-pod recycle Shutdown over the still-open connection: it carries
		// the last-released slot's session id (the adapter's non-empty session_id
		// guard admits it) and the RecycleScrub disposition (PodID plus the
		// pool's cleanup parameters), so the adapter runs the §5.2 scrub
		// and reports ReportPodScrub. Best-effort: a failure here leaves the
		// armed missing-report timeout to retire the pod. The response's
		// ExitedCleanly is ignored — the scrub outcome arrives asynchronously.
		_, _ = result.Adapter.ShutdownRecycle(ctx, result.SessionID, adapterclient.RecycleScrub{
			PodID:                 result.SandboxName,
			CleanupCommands:       result.CleanupCommands,
			CleanupTimeoutSeconds: int32(result.CleanupTimeoutSeconds),
		})
	}
	return nil
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
