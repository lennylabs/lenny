// SPDX-License-Identifier: MIT

package sessionserver

import (
	"context"
	"time"

	"github.com/lennylabs/lenny/pkg/api/v1/session"
	"github.com/lennylabs/lenny/pkg/gateway/podlifecycle/podsession"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/runtimestore"
	"github.com/lennylabs/lenny/pkg/gateway/session/sessionstore"
	"github.com/lennylabs/lenny/pkg/workspaceplan"
)

// startContext carries the create-time resolution claimAtCreate produced
// into a same-call startOnPod, so the combined one-call POST
// /v1/sessions/start path neither re-runs the §7.1-step-3 credential
// pre-check nor drops the create-time pod-claim duration from the
// end-to-end startup envelope. It is nil for the two-step POST
// /v1/sessions/{id}/start and the resume-rebuild paths, where startOnPod
// resolves credentials itself and the claim ran in a separate request.
type startContext struct {
	// CredPools and UserCredProviders are the already-resolved §4.9
	// pre-claim credential map and user-source providers; startOnPod reuses
	// them instead of calling resolveCredentialPools again.
	CredPools         map[string]string
	UserCredProviders []string
	// PoolDeliveryModes maps each resolved provider pool's name to its
	// §4.9 effective deliveryMode; startOnPod reuses it so the session-start
	// credential-delivery gate does not re-resolve the CredentialPool
	// records on the combined path. spec: §4.9.
	PoolDeliveryModes map[string]string
	// PodClaim is the create-time §6.3 pod_claim phase duration the same
	// call measured at claimAtCreate. prepareAndLaunch threads it into the
	// end-to-end lenny_session_startup_duration_seconds so the single
	// combined-call observation spans pod claim through ready (§6.3).
	PodClaim time.Duration
}

// startOnPod places a started session on a Kubernetes warm pod. It
// resolves the warm pool serving the session's runtime and §5.3
// isolation profile, then dispatches by the pool's sessionPolicy and by
// whether a pod was already claimed at /create:
//
//   - An exclusive pool (maxConcurrentSessions=1) whose pre-running row
//     carries a live §4.6 durable binding (PodAssignment set by the
//     create-time claim, the session not in a recovery state) reconnects
//     to the bound pod and runs the §4.3 prepare barrier plus the §4.4
//     launch (prepareAndLaunch); it does not re-claim.
//   - An exclusive pool with no live create-time claim runs the whole
//     Claim → Prepare → Launch sequence through podBinder.Bind. This is
//     the §7.3 snapshotless resume-rebuild path (resumeOnPod invokes
//     startOnPod on a recovery-state row that still carries the dead pod's
//     stale PodAssignment).
//   - A concurrent-workspace pool (maxConcurrentSessions>1) whose row
//     carries a live §4.6 binding reconnects to the slot reserved at
//     create through podBinder.BindReservedSlot (§5.2), the slot analog of
//     prepareAndLaunch; it does not re-reserve. A recovery-state row with a
//     stale binding re-reserves a fresh slot through podBinder.BindSlot.
//
// The pod's §4.7 adapter runs the per-mode assignment sequence; the
// BindResult is returned for the caller to register and persist after its
// own atomicity gates pass.
//
// startOnPod no longer registers the binding or persists the
// SandboxName field itself: the §7.1 atomicity contract
// requires the gateway to skip the persist write entirely when an
// earlier step in steps 2-8 fails, so the caller decides when to
// publish the binding. The session-mode and concurrent-slot paths
// both surface their result the same way; the caller distinguishes
// them by inspecting BindResult.SlotID when it has to roll back.
//
// claimed is the create-time resolution from a same-call claimAtCreate,
// passed only on the combined one-call POST /v1/sessions/start path. When
// it is non-nil, startOnPod reuses its §7.1-step-3 credential resolution
// rather than calling resolveCredentialPools again (so the combined path
// runs the pre-check exactly once before the step-4 claim, per the
// proposal's "pre-check runs once, before the claim" placement) and threads
// its create-time pod_claim duration into the end-to-end startup envelope.
// It is nil on the resume-rebuild path, where startOnPod resolves credentials
// itself because the claim ran in a separate request.
//
// The two-step `POST /v1/sessions/{id}/start` exclusive-pool launch does NOT
// flow through startOnPod: per the proposal §4.4, /start is launch-only and
// does no credential work, so handleStart calls launchOnPod, which resolves
// the pool and dispatches to launchPrepared without running the §4.9
// pre-claim credential resolution. The credential lease was assigned at
// /finalize. A concurrent-pool two-step /start still flows through
// launchOnPod, which resolves credentials only on the BindReservedSlot
// reconnect (a concurrent pool materializes and launches together at /start
// per §5.2, so it needs the assignment inputs there).
//
// startOnPod, after this split, resolves credentials because both its
// remaining callers consume them: the combined create-and-start path runs
// the prepare barrier (which assigns the lease), and the resume-rebuild path
// runs the whole Claim → Prepare → Launch sequence.
// spec: §4.1, §4.4, §5 (proposal); §4.2 — "Pod-to-session binding";
// §7.1 — atomic-creation rollback.
func (s *Server) startOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan, claimed *startContext) (*podsession.BindResult, error) {
	// spec: §4.9 — run the pre-claim credential
	// availability check and resolve the per-provider pool map BEFORE a
	// pod is claimed, so a session that would fail at credential
	// assignment is rejected without wasting a warm pod. On the combined
	// one-call path the same-call claimAtCreate already ran this step-3
	// pre-check, so reuse its resolution rather than re-running it (proposal
	// §4.1: the pre-check runs once, before the claim).
	var (
		credPools         map[string]string
		poolDeliveryModes map[string]string
		userCredProviders []string
	)
	if claimed != nil {
		credPools, poolDeliveryModes, userCredProviders = claimed.CredPools, claimed.PoolDeliveryModes, claimed.UserCredProviders
	} else {
		var err error
		credPools, poolDeliveryModes, userCredProviders, err = s.resolveCredentialPools(ctx, row)
		if err != nil {
			return nil, err
		}
	}
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool); empty resolves by runtime + §5.3 profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 — a session targeting a pool in the
	// PoolWarmingUp bootstrap state returns 503 RUNTIME_UNAVAILABLE
	// before a claim is attempted, so the client receives a retry hint
	// rather than burning a claim attempt that would surface as a less
	// informative SESSION_CREATION_FAILED.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §5.2 — service mode is claimless: there is no workspace
	// materialization and no SandboxClaim. A service-mode session is a
	// connection handle; the gateway routes each message through the
	// pool's Kubernetes Service / EndpointSlice with tenant-affinity
	// routing (statelessproxy + tenantaffinity), pinning each pod to one
	// tenant. The start path therefore returns a nil BindResult without
	// touching the claim or slot acquisition paths, so no Sandbox is
	// claimed and no pod is bound to the session. The session row persists
	// with execution_mode=service; message routing reads that mode and
	// dispatches to the stateless data plane rather than a bound pod.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return nil, nil
	}
	// spec: §4.9 — the session-start credential-delivery gate. Evaluate each
	// resolved CredentialPool's effective deliveryMode against the bound
	// pod's isolationProfile/spiffeBinding before any lease is minted, so a
	// combination the pool-definition copy hid (the divergence §4.9 names) is
	// rejected here in multi-tenant mode. This covers the combined one-call
	// POST /v1/sessions/start path (claimed != nil, prepareAndLaunch below)
	// and the resume / tree-recovery rebuild path (claimed == nil, the
	// whole-sequence Bind below), plus the concurrent-slot mint. On rejection
	// startOnPod's callers roll back the pre-bind claim exactly as they do for
	// a ResolvePool failure.
	if err := s.checkCredentialDeliveryIsolation(match, poolDeliveryModes); err != nil {
		return nil, err
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §5.2 — a session-mode pool with maxConcurrentSessions > 1 routes
	// the bind through the slot-claim path; the one-session-per-pod default
	// uses the session-claim path.
	if match.MaxConcurrentSessions > 1 {
		slotReq := s.slotBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
		return s.bindConcurrentSlot(ctx, row, match, slotReq)
	}
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
	// spec: §4.7, §7.1 steps 4-13 (proposal) — the combined one-call
	// `POST /v1/sessions/start` path (claimed is non-nil) claimed the pod in
	// the same call but never went through /finalize, so it reconnects to the
	// bound pod and runs the §4.3 prepare barrier plus the §4.4 launch together
	// (prepareAndLaunch) without re-claiming. The two-step
	// `POST /v1/sessions/{id}/start` launch-only path does not reach startOnPod
	// (handleStart calls launchOnPod), so a live create-time binding with
	// claimed == nil here is only the resume-rebuild path, which falls through
	// to the whole-sequence Bind below.
	//
	// The reconnect is gated on a live create-time claim, signalled by a
	// non-empty PodAssignment on a non-recovery row, rather than by a non-empty
	// PodAssignment alone. A session that lost its pod and entered a recovery
	// state (resume_pending / resuming / awaiting_client_action) retains the
	// dead pod's name in PodAssignment (failure.go), and no resume or
	// tree-recovery path clears it. The §7.3 snapshotless resume-rebuild path
	// (resumeOnPod → startOnPod with a recovery-state row) must claim a fresh
	// pod through the whole Claim → Prepare → Launch sequence below rather than
	// reconnect to the dead binding; reconnecting would fail Prepare against the
	// no-longer-existing pod and fail the resume instead of recovering it.
	if claimed != nil && row.PodAssignment != "" && !session.IsRecovery(row.State) {
		// spec: §5 / §6.3 — the same call measured the
		// create-time pod_claim duration; thread it into the launch-boundary
		// end-to-end envelope so the single
		// lenny_session_startup_duration_seconds observation spans pod claim
		// through ready.
		return s.prepareAndLaunch(ctx, row, match, bindReq, claimed.PodClaim)
	}
	// spec: §4.6.1 / §5.2 — on a `queue` pool, hold an exhausted session-claim
	// acquisition in the per-pool claim FIFO and re-enter the claim path as
	// pods free; a `reject` pool returns WARM_POOL_EXHAUSTED on the first
	// exhaustion (ErrNoIdlePod after both the claim-path timeout and the
	// Postgres fallback). A queued request holds no pod or claim between
	// attempts, so the §7.1 atomicity contract is preserved.
	result, err := runWithQueue(ctx, s.claimQueue, match.Pool, match.OnPoolExhausted, match.MaxQueueWaitSeconds,
		func(ctx context.Context) (*podsession.BindResult, error) {
			return s.podBinder.Bind(ctx, bindReq)
		})
	if err != nil {
		return nil, err
	}
	// The whole-sequence Bind (resume-rebuild) claims and launches in one
	// call, so result.Timings carries every §6.3 phase including PodClaim;
	// record the phases and the end-to-end envelope once at this boundary.
	s.recordStartupPhases(match, result.Timings)
	s.recordStartupDuration(match, result.Timings)
	return result, nil
}

// launchOnPod runs the §4.4 launch-only path for the two-step
// `POST /v1/sessions/{id}/start` against the pod prepared at /finalize. Per
// the proposal §4.4, /start performs only the §6.3 agent_session_start phase:
// it transitions ready → starting (the caller writes the row), reconnects to
// the prepared pod from the row's persisted §4.6 binding, invokes
// StartSession (pod-warm) or ConfigureWorkspace (SDK-warm), and lets the
// caller transition the row to running. The heavy preparation — staging,
// materialization, setup commands, and credential-lease assignment — already
// ran at /finalize, so launchOnPod runs NO §4.9 pre-claim credential
// resolution: the launch RPCs consume no credential inputs (the lease was
// pushed to the pod at finalize), so resolving them here would be wasted work
// the proposal moves off the /start path.
//
// It returns (nil, nil) for the dispositions that launch nothing at /start:
//
//   - the binder is not wired (the minimal gateway, which starts by a plain
//     ready → running transition);
//   - a service-mode pool, which is claimless and is routed through the
//     pool's Service/EndpointSlice rather than a bound pod (§5.2).
//
// A concurrent-workspace pool (maxConcurrentSessions>1) reserves a slot at
// /create and materializes + launches it together at /start via
// BindReservedSlot (§5.2), so its launch DOES need the §4.9 assignment
// inputs; launchOnPod resolves credentials on that branch alone. The
// exclusive launch-only path needs none.
//
// spec: §4.4, §4.6 (proposal); §6.1;
// §15.1 (/start precondition); §5.2; §6.3.
func (s *Server) launchOnPod(ctx context.Context, row sessionstore.Session, plan workspaceplan.Plan) (*podsession.BindResult, error) {
	// spec: §7.1 / §14.1 — constrain resolution to the client-pinned pool
	// (row.Pool); empty resolves by runtime + §5.3 profile. F-CS2 (0018).
	match, err := podsession.ResolvePool(ctx, s.podBinder.Client, s.poolPolicyReader(), s.agentNamespace,
		row.RuntimeRef, string(row.IsolationProfile), row.Pool)
	if err != nil {
		return nil, err
	}
	// spec: §5.2 — a session targeting a pool in the
	// PoolWarmingUp bootstrap state returns 503 RUNTIME_UNAVAILABLE rather than
	// reconnecting to a pod that may not be ready. A two-step /start should not
	// hit this in practice (the pod was claimed at /create and prepared at
	// /finalize), but the gate is kept fail-closed against a pool that warmed
	// down across the window.
	if match.PoolWarmingUp {
		return nil, &podsession.PoolWarmingError{Pool: match.Pool, PodsWarming: match.PodsWarming}
	}
	// spec: §5.2 — service mode is claimless: no SandboxClaim, no workspace,
	// and no bound pod. The session row persists with execution_mode=service
	// and message routing dispatches through the stateless data plane, so
	// /start returns a nil BindResult with no launch RPC.
	if match.ExecutionMode == string(runtimestore.ExecutionModeService) {
		return nil, nil
	}
	agentInterface, minPlatformVersion := s.runtimeManifestFields(ctx, row.RuntimeRef)
	// spec: §4.1, §5.2 (proposal) — a concurrent-workspace pool reserved a slot
	// at /create and materializes + launches it together at /start. That launch
	// runs the §4.9 AssignCredentials, so it needs the per-provider pool map;
	// resolve it here, on this branch alone. A live create-time reservation
	// (non-empty PodAssignment on a non-recovery row) reconnects to the reserved
	// slot via BindReservedSlot; a row without one is a misuse (a two-step
	// /start with no claimed pod), surfaced as the queue's exhaustion path.
	if match.MaxConcurrentSessions > 1 {
		credPools, poolDeliveryModes, userCredProviders, cerr := s.resolveCredentialPools(ctx, row)
		if cerr != nil {
			return nil, cerr
		}
		// spec: §4.9 — the concurrent-workspace two-step /start mints per-provider
		// leases through BindReservedSlot, so run the credential-delivery gate on
		// the resolved CredentialPool deliveryMode against the bound pod's
		// isolationProfile/spiffeBinding before the slot bind. prepareAtFinalize
		// skips concurrent pools, so this is the seam that covers them.
		if derr := s.checkCredentialDeliveryIsolation(match, poolDeliveryModes); derr != nil {
			return nil, derr
		}
		slotReq := s.slotBindRequest(ctx, row, match, plan, credPools, userCredProviders, agentInterface, minPlatformVersion)
		return s.bindConcurrentSlot(ctx, row, match, slotReq)
	}
	// spec: §4.4, §4.6 (proposal) — the exclusive-pool launch-only path. The
	// pod was claimed at /create and prepared at /finalize, so reconnect to the
	// bound pod and run only the launch (StartSession or ConfigureWorkspace).
	// No credential inputs are needed: the lease was assigned at /finalize.
	// launchPrepared recomputes the §6.1 SDK-warm demotion decision from the
	// persisted plan rather than re-running the prepare phase to learn it.
	bindReq := s.exclusiveBindRequest(ctx, row, match, plan, nil, nil, agentInterface, minPlatformVersion)
	return s.launchPrepared(ctx, row, match, bindReq)
}

// prepareAndLaunch runs the §4.3 prepare barrier and the §4.4 launch
// against the pod a session claimed at /create, reconnecting from the
// row's persisted §4.6 binding (PodAssignment + PoolRef) rather than
// re-claiming. It reassembles the monolithic BindResult the caller
// registers and persists, mirroring Binder.Bind's claim → prepare →
// launch composition without re-running the claim. A prepare or launch
// failure is surfaced for writePodClaimError to map; the binder already
// reclaimed the bound pod (and any lease) on the failure path.
//
// createPodClaim is the create-time §6.3 pod_claim phase duration the
// same call measured at claimAtCreate, or zero when the claim ran in a
// separate request (the two-step `POST /v1/sessions/{id}/start` path).
// It is threaded into the end-to-end lenny_session_startup_duration_seconds
// so a single combined-call observation includes the pod-claim component
// per §6.3, while the per-phase pod_claim histogram is still
// emitted exactly once (at /create, not re-emitted here).
// spec: §4.3, §4.4, §4.6, §5 (proposal); §6.3; §7.1 steps 11-13.
func (s *Server) prepareAndLaunch(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, bindReq podsession.BindRequest, createPodClaim time.Duration) (*podsession.BindResult, error) {
	bindReq.SandboxName = row.PodAssignment
	if row.PoolRef != "" {
		bindReq.Pool = row.PoolRef
	}
	prep, err := s.podBinder.Prepare(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	bindReq.Demoted = prep.Demoted
	result, err := s.podBinder.Launch(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	// Reassemble the BindResult the whole-sequence Bind would have returned:
	// the live adapter and launch timing come from Launch, the prepared
	// workspace and setup trail from Prepare. Launch never claims, so its
	// PodClaim is zero; the create-time claim duration is carried separately
	// (createPodClaim) and attributed to the end-to-end envelope below rather
	// than to the per-phase pod_claim histogram, which was recorded once at
	// /create.
	result.Timings.WorkspaceMaterialization = prep.Timings.WorkspaceMaterialization
	result.Timings.SetupCommands = prep.Timings.SetupCommands
	result.Timings.CredentialAssignment = prep.Timings.CredentialAssignment
	result.WorkspacePlanWarnings = prep.WorkspacePlanWarnings
	result.SetupOutputs = prep.SetupOutputs
	result.WorkspaceBase = prep.WorkspaceBase
	// spec: §6.3 / §5 (0007 proposal) — record the prepare/launch phases
	// measured here (workspace_materialization, setup_commands,
	// credential_assignment, agent_session_start). result.Timings.PodClaim is
	// zero from Launch, so recordStartupPhases skips it rather than re-emitting
	// a spurious 0s pod_claim sample; the per-phase pod_claim histogram was
	// recorded once at /create.
	s.recordStartupPhases(match, result.Timings)
	// spec: §6.3 / §5 (proposal) — the end-to-end
	// lenny_session_startup_duration_seconds spans pod claim through ready, so
	// attribute the create-time pod_claim duration to the end-to-end total
	// (without re-emitting the per-phase sample). On the combined one-call path
	// createPodClaim is the same call's claim duration so the single
	// observation covers the whole envelope; on the two-step /start path it is
	// zero by construction and the create-time pod_claim was already
	// observed by the separate /create request.
	end := result.Timings
	end.PodClaim = createPodClaim
	s.recordStartupDuration(match, end)
	return result, nil
}

// launchPrepared runs only the §4.4 launch against the pod a two-step
// session prepared at /finalize, reconnecting from the row's persisted §4.6
// binding (PodAssignment + PoolRef). The §4.3 prepare barrier
// (PrepareWorkspace, FinalizeWorkspace, RunSetup, AssignCredentials) already
// ran at /finalize, so /start neither re-stages, re-materializes, re-runs
// setup, nor re-assigns the credential lease; it only starts the runtime
// (StartSession for a pod-warm or demoted pod, ConfigureWorkspace for a
// still-SDK-warm pod). The §6.1 SDK-warm demotion decision the prepare phase
// made is a pure function of the persisted plan and the runtime's
// sdkWarmBlockingPaths, so launchPrepared recomputes it through
// podsession.RequiresDemotion rather than re-running the prepare phase to
// learn it. A launch failure is surfaced for writePodClaimError; the binder
// already reclaimed the bound pod (and the lease assigned at finalize) on the
// failure path.
//
// The /start end-to-end span is launch-only by construction: the create-time
// pod_claim phase was recorded at /create and the materialization / setup /
// credential phases at /finalize, each once, so launchPrepared records only
// the agent_session_start phase and the end-to-end envelope here.
// spec: §4.4, §4.6 (proposal); §6.1; §6.3.
func (s *Server) launchPrepared(ctx context.Context, row sessionstore.Session, match podsession.PoolMatch, bindReq podsession.BindRequest) (*podsession.BindResult, error) {
	bindReq.SandboxName = row.PodAssignment
	if row.PoolRef != "" {
		bindReq.Pool = row.PoolRef
	}
	bindReq.Demoted = podsession.RequiresDemotion(bindReq)
	result, err := s.podBinder.Launch(ctx, bindReq)
	if err != nil {
		return nil, err
	}
	// spec: §6.3 — Launch measured only the agent_session_start phase; record
	// it and the launch-only end-to-end envelope. The pod_claim and
	// workspace_materialization / setup_commands / credential_assignment phases
	// were recorded at /create and /finalize, so they are not re-emitted here.
	s.recordStartupPhases(match, result.Timings)
	s.recordStartupDuration(match, result.Timings)
	return result, nil
}
