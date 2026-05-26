// SPDX-License-Identifier: MIT

// Package podsession is the gateway-side path that places a session on
// a warm pod. Bind claims an idle Sandbox (§4.6.1), resolves the bound
// pod's adapter address, performs the §15.5 version handshake, and
// starts the session on the pod's §4.7 adapter. It joins the pod-claim
// path, the adapter client, and the recorded pod address into the
// single operation the gateway's session-creation handler invokes.
package podsession

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/adapter/workspace"
	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/slotcounter"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
)

// Binder places sessions on warm pods.
type Binder struct {
	// Client addresses the cluster: it backs the pod claim and the
	// Sandbox lookup that resolves the pod address.
	Client client.Client
	// Namespace is the agent namespace the pools and Sandboxes live in.
	Namespace string
	// AdapterPort is the TCP port a pod's §4.7 adapter listens on.
	AdapterPort int
	// AcceptedVersions are the adapter protocol versions the gateway
	// speaks, highest preference first (§15.5).
	AcceptedVersions []string
	// DialAdapter opens an adapter client for the pod reachable at addr.
	// Production dials over mTLS; tests substitute an in-memory link.
	DialAdapter func(addr string) (*adapterclient.Client, error)
	// Blobs resolves §4.5 lenny-blob:// upload refs to their content so
	// Bind can stage a plan's uploadFile and uploadArchive sources via
	// the adapter's PrepareWorkspace RPC. Nil when the deployment has no
	// blob store configured; a plan carrying upload sources then fails
	// to bind.
	Blobs blobstore.Store
	// Fallback is the §4.6.1 Postgres-backed agent_pod_state mirror. When
	// the Kubernetes-API claim returns podclaim.ErrNoIdlePod and Fallback
	// is non-nil, connect attempts a fallback claim against the mirror
	// before surfacing the no-idle-pod error. Nil when the deployment has
	// no Postgres configured; connect then returns ErrNoIdlePod directly.
	Fallback agentpodstate.Store
	// FallbackMaxMirrorLagSeconds is the §4.6.1
	// podClaimFallbackMaxMirrorLagSeconds freshness precondition: the
	// fallback runs only when the target pool's mirror lag is at or below
	// this many seconds. Above it the mirror may still show pods already
	// claimed in etcd but not yet mirrored, so connect skips the fallback
	// and returns ErrNoIdlePod. A zero value selects the default of
	// DefaultFallbackMaxMirrorLagSeconds.
	FallbackMaxMirrorLagSeconds float64
	// Credentials is the §4.9 credential-assignment service. When a
	// BindRequest names credential pools, Bind mints a lease from each
	// pool and pushes the set to the pod via the adapter's
	// AssignCredentials RPC before StartSession (§4.7 item 4). Nil when
	// the deployment configures no credential pools; a BindRequest then
	// names no pools and Bind assigns nothing.
	Credentials CredentialAssigner
	// SlotCounter is the §5.2 atomic slot counter. Wired in production
	// installs that expose --redis-url; the SlotClaimer constructed
	// per BindSlot call carries it through so the Redis Lua
	// GET-compare-INCR sequence enforces maxConcurrent atomically
	// across gateway replicas. Nil when no Redis is wired; the
	// SlotClaimer then falls back to its race-prone SSA-only path.
	SlotCounter *slotcounter.Counter
	// APIServerReachable is the §4.6.1 admission-reachability precondition
	// (precondition 2): before initiating the Postgres-backed fallback,
	// the gateway probes API server reachability (a lightweight GET
	// /readyz). When it returns an error the fallback is skipped, because
	// the lenny-sandboxclaim-guard CREATE check the fallback relies on
	// traverses the API server. Nil disables the probe and the fallback
	// proceeds; production wires it from the cluster rest config.
	APIServerReachable func(ctx context.Context) error
	// FallbackSkipped records a §4.6.1 fallback skip event by reason
	// (FallbackSkipReasonMirrorStale or
	// FallbackSkipReasonAPIServerUnreachable), backing the
	// lenny_pod_claim_fallback_skipped_total counter. Nil is a no-op.
	FallbackSkipped func(reason string)
	// SlotConflict records a §5.2 line 519 concurrent-mode slot
	// reservation failure due to slot contention, backing the
	// lenny_slot_assignment_conflict_total counter (labeled by pool).
	// It is threaded into the per-BindSlot SlotClaimer. Nil is a no-op.
	SlotConflict func(pool string)
	// SlotFailure records a §5.2 line 12 concurrent-workspace slot bind
	// failure after a slot was reserved, backing the
	// lenny_slot_failure_total counter (labeled by error_type, pool, and
	// k8s_pod_name). errorType names the bind stage that failed. Nil is a
	// no-op.
	SlotFailure func(errorType, pool, podName string)
	// Rehydration records a §5.2 line 521 post-recovery slot-counter
	// rehydration event, backing the lenny_slot_rehydration_total counter
	// (labeled by pod and pool). It is threaded into the per-BindSlot
	// SlotClaimer. Nil is a no-op.
	Rehydration func(podID, pool string)
}

// §5.2 line 12 lenny_slot_failure_total error_type labels: the
// concurrent-mode slot bind stages whose failure terminates a reserved
// slot. The set is finite so the metric stays low-cardinality.
const (
	slotFailureWorkspacePrep        = "workspace_prep"
	slotFailureSetup                = "setup"
	slotFailureCredentialAssignment = "credential_assignment"
	slotFailureSessionStart         = "session_start"
)

// §4.6.1 lenny_pod_claim_fallback_skipped_total reason labels: the two
// fallback preconditions whose failure skips the Postgres-backed claim.
const (
	// FallbackSkipReasonMirrorStale is recorded when the agent_pod_state
	// mirror lag exceeds the freshness precondition (precondition 1).
	FallbackSkipReasonMirrorStale = "mirror_stale"
	// FallbackSkipReasonAPIServerUnreachable is recorded when the API
	// server readiness probe fails (precondition 2).
	FallbackSkipReasonAPIServerUnreachable = "apiserver_unreachable"
)

// CredentialAssigner mints a session's §4.9 credential leases. The
// gateway's credassign.Service satisfies it; binder tests substitute a
// fake. AssignProto leases a credential from the named pool to the
// session, records the lease in the gateway's credential-lease store,
// caches the upstream credential for the §4.9 LLM proxy, and returns
// the wire-form lease the adapter materializes into the runtime
// credential file.
type CredentialAssigner interface {
	// AssignProto leases a credential from poolName to sessionID and
	// returns the wire-form CredentialLease. spiffeURI is the issuing
	// pod's SPIFFE identity for proxy-mode SPIFFE-binding; an empty value
	// disables binding.
	AssignProto(poolName, sessionID, spiffeURI string) (*adapterv1.CredentialLease, error)
	// ReleaseSession releases every §4.9 credential lease the session
	// holds back to its pool. It is the §7.1 step 23 teardown the binder
	// runs when a session's pod is released, so a completed session's
	// pool slots are returned rather than leaking. A session with no
	// leases is a no-op. spec: §7.1 line 52.
	ReleaseSession(sessionID string)
}

// CredentialAssignmentError reports that a §4.9 credential lease
// assignment failed during Bind for a specific provider/pool, after the
// §4.9 pre-claim availability check had already passed. The gateway
// maps it to the §4.9 line 1220 race: it releases the claimed pod,
// increments lenny_credential_preclaim_mismatch_total{pool,provider},
// and returns CREDENTIAL_POOL_EXHAUSTED to the client.
type CredentialAssignmentError struct {
	Provider string
	Pool     string
	Err      error
}

func (e *CredentialAssignmentError) Error() string {
	return fmt.Sprintf("podsession: lease %s credential from pool %s: %v", e.Provider, e.Pool, e.Err)
}

func (e *CredentialAssignmentError) Unwrap() error { return e.Err }

// DefaultFallbackMaxMirrorLagSeconds is the §4.6.1
// podClaimFallbackMaxMirrorLagSeconds default: the fallback claim runs
// only when the mirror is no more than this many seconds stale.
const DefaultFallbackMaxMirrorLagSeconds = 10

// BindRequest describes a session to place on a warm pod.
type BindRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §15.1 session being started.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// Runtime is the runtime name passed to the adapter's StartSession.
	Runtime string
	// Plan is the workspace the adapter materializes before start.
	Plan *adapterv1.WorkspacePlan
	// ExperimentContext is the §8.3 / §10.7 experiment enrollment
	// delivered to the runtime in the adapter manifest. Nil for an
	// unenrolled session.
	ExperimentContext *adapterv1.ExperimentContext
	// TracingContext is the §8.3 opaque tracing-identifier map delivered
	// to the runtime in the adapter manifest. Nil when none is set.
	TracingContext map[string]string
	// SetupPolicy is the §5.1 runtime setupPolicy bounding the setup
	// phase. Nil when the runtime declares no aggregate cap.
	SetupPolicy *adapterv1.SetupPolicy
	// CredentialPools names the §4.9 credential pool to lease from for
	// each authorized provider, keyed by provider. The caller resolves it
	// from the §4.9 intersection of the Runtime's supportedProviders and
	// the tenant's credentialPolicy.providerPools. Bind mints one lease
	// per entry and pushes the set to the pod via AssignCredentials
	// before StartSession. Empty (or nil) when the session needs no
	// upstream LLM credentials; Bind then assigns nothing.
	CredentialPools map[string]string
	// PodSpiffeURI is the issuing pod's SPIFFE identity, recorded on each
	// minted lease for the §4.9 proxy-mode SPIFFE-binding check. Empty
	// disables binding, which §4.9 permits only in single-tenant and
	// development deployments.
	PodSpiffeURI string
	// AgentInterface is the runtime's §5.1 agentInterface descriptor as
	// JSON, written verbatim into the §15.4 manifest's agentInterface
	// field. Nil when the runtime declares none.
	AgentInterface []byte
	// MinPlatformVersion is the runtime's §5.1 minPlatformVersion written
	// into the §15.4 manifest. Empty when the runtime specifies no minimum.
	MinPlatformVersion string
}

// BindResult reports the pod a session was bound to.
type BindResult struct {
	// SessionID is the session the pod was claimed for.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
	// SandboxName is the claimed Sandbox.
	SandboxName string
	// PodIP is the bound pod's address.
	PodIP string
	// SlotID identifies the concurrent-mode (§5.2) slot the session was
	// placed on. It is empty for a session-mode or task-mode bind, where
	// the pod is claimed exclusively for the session. It is non-empty
	// only for a BindSlot result.
	SlotID string
	// Adapter is the live connection to the pod's adapter. The caller
	// owns it and closes it when the session ends.
	Adapter *adapterclient.Client
	// Timings reports the wall-clock duration of each §6.3 hot-path
	// phase Bind executed, for the caller to record on the §6.3
	// per-phase and end-to-end startup-latency histograms. It is the
	// zero value for a BindSlot result, where the concurrent-mode
	// startup path is timed separately.
	Timings BindTimings
}

// BindTimings carries the per-phase wall-clock durations a successful
// Bind measured, so the caller can attribute the §6.3 line 358 latency
// budget. The end-to-end pod-warm SLO (§6.3 line 348,
// lenny_session_startup_duration_seconds) is the sum of PodClaim,
// CredentialAssignment, and AgentSessionStart; it excludes
// WorkspaceMaterialization (payload-dependent) and SetupCommands
// (deployer-controlled), both of which §6.3 keeps out of the
// platform-controlled pod-warm budget. spec: §6.3 lines 348, 372.
type BindTimings struct {
	// PodClaim is the §6.3 "pod claim and routing" phase: the warm-pod
	// claim, pod-IP resolution, mTLS dial, and version handshake.
	PodClaim time.Duration
	// WorkspaceMaterialization is the staging plus FinalizeWorkspace
	// phase. Excluded from the pod-warm SLO total per §6.3 line 348.
	WorkspaceMaterialization time.Duration
	// SetupCommands is the RunSetup phase. Deployer-controlled and
	// excluded from the platform pod-warm budget (§6.3 line 363), but
	// instrumented per the §6.3 line 372 per-phase requirement.
	SetupCommands time.Duration
	// CredentialAssignment is the §4.9 lease mint plus AssignCredentials
	// RPC phase.
	CredentialAssignment time.Duration
	// AgentSessionStart is the StartSession RPC phase, after which the
	// session is ready.
	AgentSessionStart time.Duration
}

// ResumeRequest describes a session to restore onto a fresh warm pod.
type ResumeRequest struct {
	// Pool is the SandboxWarmPool to claim a pod from.
	Pool string
	// SessionID is the §7.1 session being resumed.
	SessionID string
	// TenantID is the tenant that owns the session.
	TenantID string
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
}

// Bind claims an idle pod for the request's session, resolves the
// pod's adapter address, performs the §15.5 version handshake, and runs
// the §4.7 session-assignment sequence on the pod's adapter:
// PrepareWorkspace stages uploaded files and cloned repositories,
// FinalizeWorkspace materializes the workspace, RunSetup runs the
// plan's setup commands, AssignCredentials delivers the session's §4.9
// credential leases, and StartSession starts the runtime. On success
// the caller owns the returned live adapter connection. Any failure
// after the claim is returned so the gateway can retry on a fresh pod.
func (b *Binder) Bind(ctx context.Context, req BindRequest) (*BindResult, error) {
	// spec: §6.3 lines 358, 372 — time each hot-path phase so the caller
	// can attribute the per-phase latency budget and the end-to-end
	// pod-warm startup SLO. The clock starts at the claim and segments
	// at each phase boundary; on any failure the partial timings are
	// discarded (the SLO measures successful starts only).
	var t BindTimings
	phaseStart := time.Now()
	sb, cl, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	sandboxName := sb.Name
	t.PodClaim = time.Since(phaseStart)

	// spec: §6.2 lines 83-94, 305-313 — advance Sandbox.status.phase through
	// the setup chain (claimed → receiving_uploads → finalizing_workspace →
	// running_setup → starting_session → attached) so the authoritative state
	// machine reflects each phase. The detailed transitions live on the CRD
	// status subresource only (line 313). On a pre-attached RPC failure the
	// phase is best-effort moved to `failed` per §6.2 lines 99-102.
	phaseStart = time.Now()
	if err := b.setPhase(ctx, sb, state.ReceivingUploads); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: phase receiving_uploads on pod %s: %w", sandboxName, err)
	}
	if err := b.stageWorkspace(ctx, cl, req.SessionID, req.Plan); err != nil {
		b.failPhase(ctx, sb)
		cl.Close()
		return nil, fmt.Errorf("podsession: stage workspace on pod %s: %w", sandboxName, err)
	}
	if err := b.setPhase(ctx, sb, state.FinalizingWorkspace); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: phase finalizing_workspace on pod %s: %w", sandboxName, err)
	}
	if err := cl.FinalizeWorkspace(ctx, req.SessionID, req.Plan); err != nil {
		b.failPhase(ctx, sb)
		cl.Close()
		return nil, fmt.Errorf("podsession: finalize workspace on pod %s: %w", sandboxName, err)
	}
	t.WorkspaceMaterialization = time.Since(phaseStart)

	phaseStart = time.Now()
	if err := b.setPhase(ctx, sb, state.RunningSetup); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: phase running_setup on pod %s: %w", sandboxName, err)
	}
	if err := cl.RunSetup(ctx, req.SessionID, req.Plan.GetSetupCommands(), req.SetupPolicy); err != nil {
		b.failPhase(ctx, sb)
		cl.Close()
		return nil, fmt.Errorf("podsession: run setup on pod %s: %w", sandboxName, err)
	}
	t.SetupCommands = time.Since(phaseStart)

	phaseStart = time.Now()
	// §4.7 AssignCredentials is the fourth setup RPC; it runs while the pod
	// is in running_setup (§6.2 has no distinct credential phase), before the
	// starting_session transition below.
	if err := b.assignCredentials(ctx, cl, req); err != nil {
		b.failPhase(ctx, sb)
		cl.Close()
		return nil, fmt.Errorf("podsession: assign credentials on pod %s: %w", sandboxName, err)
	}
	t.CredentialAssignment = time.Since(phaseStart)

	phaseStart = time.Now()
	if err := b.setPhase(ctx, sb, state.StartingSession); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: phase starting_session on pod %s: %w", sandboxName, err)
	}
	if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		b.failPhase(ctx, sb)
		cl.Close()
		return nil, fmt.Errorf("podsession: start session on pod %s: %w", sandboxName, err)
	}
	if err := b.setPhase(ctx, sb, state.Attached); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: phase attached on pod %s: %w", sandboxName, err)
	}
	t.AgentSessionStart = time.Since(phaseStart)

	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sandboxName,
		PodIP:       sb.Status.PodIP,
		Adapter:     cl,
		Timings:     t,
	}, nil
}

// setPhase advances the claimed Sandbox's §6.2 status.phase to `to`,
// validating the edge against the state machine and retrying once per
// optimistic-locking conflict. The gateway is the sole writer of the claim
// and session phases (idle → claimed → ... → attached → terminal): the
// WarmPoolController's lifecycle planner leaves these phases untouched and
// drives only the warm-path (warming → idle) and reclamation
// (draining → terminated) edges, so a conflict means a concurrent gateway
// write — which cannot happen for the pod this replica holds the exclusive
// claim on. The retry therefore re-reads and re-applies rather than
// tolerating a lost update. sb is updated in place so a chain of setup
// transitions needs no re-Get on the common no-conflict path; an invalid edge
// fails loudly rather than corrupting the phase. spec: §6.2 lines 83-94,
// 305-313.
func (b *Binder) setPhase(ctx context.Context, sb *lennyv1.Sandbox, to state.State) error {
	const maxAttempts = 4
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		from := state.State(sb.Status.Phase)
		if from == to {
			return nil // idempotent: a retry or concurrent writer already landed it
		}
		if err := state.IsValid(from, to); err != nil {
			return err
		}
		sb.Status.Phase = string(to)
		err := b.Client.Status().Update(ctx, sb)
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return fmt.Errorf("podsession: set sandbox %s phase %s: %w", sb.Name, to, err)
		}
		lastErr = err
		if gerr := b.Client.Get(ctx, client.ObjectKeyFromObject(sb), sb); gerr != nil {
			return fmt.Errorf("podsession: re-read sandbox %s after phase conflict: %w", sb.Name, gerr)
		}
	}
	return fmt.Errorf("podsession: set sandbox %s phase %s exhausted retries: %w", sb.Name, to, lastErr)
}

// failPhase best-effort records the §6.2 pre-attached failure phase
// (receiving_uploads / finalizing_workspace / running_setup /
// starting_session → failed, spec lines 99-102) on a Sandbox whose setup
// chain aborted. It is best-effort: the caller already returns the underlying
// error, and a failed-phase write lost to reclamation does not change the
// outcome — the orphaned claim is collected and the pod recycled (§4.6.1).
func (b *Binder) failPhase(ctx context.Context, sb *lennyv1.Sandbox) {
	if err := b.setPhase(ctx, sb, state.Failed); err != nil {
		log.Printf("podsession: record failed phase on sandbox %s: %v", sb.Name, err)
	}
}

// drain transitions the Sandbox to draining so the reconciler reclaims the
// pod (§6.2 → draining → terminated), retrying once per optimistic-locking
// conflict. Unlike setPhase it does not validate the edge: the drain must
// reclaim the pod from whatever phase it holds (a terminal disposition,
// attached, or an early claimed/idle release), so reclamation is never blocked
// by a state-machine check. A Sandbox already draining or terminated is a
// no-op.
func (b *Binder) drain(ctx context.Context, sb *lennyv1.Sandbox) error {
	const maxAttempts = 4
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if sb.Status.Phase == string(state.Draining) || sb.Status.Phase == string(state.Terminated) {
			return nil
		}
		sb.Status.Phase = string(state.Draining)
		err := b.Client.Status().Update(ctx, sb)
		if err == nil {
			return nil
		}
		if !apierrors.IsConflict(err) {
			return fmt.Errorf("podsession: drain sandbox %s: %w", sb.Name, err)
		}
		lastErr = err
		if gerr := b.Client.Get(ctx, client.ObjectKeyFromObject(sb), sb); gerr != nil {
			return fmt.Errorf("podsession: re-read sandbox %s after drain conflict: %w", sb.Name, gerr)
		}
	}
	return fmt.Errorf("podsession: drain sandbox %s exhausted retries: %w", sb.Name, lastErr)
}

// assignCredentials mints the session's §4.9 credential leases and
// pushes them to the pod via the adapter's AssignCredentials RPC, the
// fourth §4.7 session-assignment RPC (after RunSetup, before
// StartSession). It mints one lease per provider named in
// req.CredentialPools, leasing from the pool the caller resolved for
// that provider. It is a no-op when the binder has no credential
// service or the request names no pools, so a session that needs no
// upstream LLM credentials, or a deployment with no credential pools,
// assigns nothing.
//
// The minted leases carry credential material; per §4.7 item 6 the
// payload is excluded from access logs and telemetry.
func (b *Binder) assignCredentials(ctx context.Context, cl *adapterclient.Client, req BindRequest) error {
	if b.Credentials == nil || len(req.CredentialPools) == 0 {
		return nil
	}
	leases := make(map[string]*adapterv1.CredentialLease, len(req.CredentialPools))
	for provider, pool := range req.CredentialPools {
		lease, err := b.Credentials.AssignProto(pool, req.SessionID, req.PodSpiffeURI)
		if err != nil {
			// The §4.9 pre-claim check (CredentialRouter) passed for this
			// provider, yet the assignment failed — the race at §4.9 line
			// 1220. Surface a typed error so the caller can release the pod,
			// increment lenny_credential_preclaim_mismatch_total, and return
			// CREDENTIAL_POOL_EXHAUSTED.
			return &CredentialAssignmentError{Provider: provider, Pool: pool, Err: err}
		}
		// The §4.7 AssignCredentials leases map is keyed by provider, and
		// the adapter writes each runtime credential-file entry under the
		// lease's own Provider field. Stamp it from the resolved provider
		// so both agree on the provider the binder leased for.
		lease.Provider = provider
		leases[provider] = lease
	}
	return cl.AssignCredentials(ctx, req.SessionID, leases)
}

// releaseCredentials returns the session's §4.9 credential leases to
// their pool at teardown (§7.1 step 23). It is a no-op when the binder
// has no credential service, mirroring assignCredentials so a deployment
// without credential pools tears down cleanly.
func (b *Binder) releaseCredentials(sessionID string) {
	if b.Credentials == nil {
		return
	}
	b.Credentials.ReleaseSession(sessionID)
}

// stageWorkspace prepares the pod's staging area for the plan's
// non-filesystem-native sources, ahead of FinalizeWorkspace. It fetches
// the blob content of every uploadFile and uploadArchive source from
// the §4.5 blob store, clones every gitClone source on the gateway's
// network path and archives the tree, and streams all of it to the pod
// via PrepareWorkspace. It is a no-op when the plan has no such
// sources. A plan that carries upload sources but binds through a
// Binder with no blob store fails rather than materializing an
// incomplete workspace.
func (b *Binder) stageWorkspace(ctx context.Context, cl *adapterclient.Client, sessionID string, plan *adapterv1.WorkspacePlan) error {
	uploads := make(map[string][]byte)

	if refs := uploadRefs(plan); len(refs) > 0 {
		if b.Blobs == nil {
			return fmt.Errorf("plan has %d upload source(s) but the binder has no blob store", len(refs))
		}
		for _, ref := range refs {
			uri, err := blobstore.ParseURI(ref)
			if err != nil {
				return fmt.Errorf("parse upload ref %q: %w", ref, err)
			}
			_, rc, err := b.Blobs.Get(uri)
			if err != nil {
				return fmt.Errorf("fetch upload %q: %w", ref, err)
			}
			content, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return fmt.Errorf("read upload %q: %w", ref, err)
			}
			uploads[ref] = content
		}
	}

	for _, src := range plan.GetSources() {
		if src.GetType() != "gitClone" {
			continue
		}
		// §14: an authenticated clone needs the §4.9 VCS credential-lease
		// token, which is not yet wired. Public clones proceed.
		if mode := src.GetAuth().GetMode(); mode != "" {
			return fmt.Errorf("gitClone of %q uses auth.mode=%q; the §4.9 VCS credential-lease path is not yet wired",
				src.GetUrl(), mode)
		}
		archive, err := gitref.CloneArchive(ctx, src.GetUrl(), src.GetResolvedCommitSha(),
			gitref.CloneOptions{Depth: int(src.GetDepth()), Submodules: src.GetSubmodules()})
		if err != nil {
			return fmt.Errorf("clone %q: %w", src.GetUrl(), err)
		}
		uploads[workspace.GitCloneStagingRef(src)] = archive
	}

	if len(uploads) == 0 {
		return nil
	}
	_, err := cl.PrepareWorkspace(ctx, sessionID, uploads)
	return err
}

// uploadRefs collects the distinct uploadRef values of the plan's
// uploadFile and uploadArchive sources, in first-seen order.
func uploadRefs(plan *adapterv1.WorkspacePlan) []string {
	seen := make(map[string]bool)
	var refs []string
	for _, src := range plan.GetSources() {
		switch src.GetType() {
		case "uploadFile", "uploadArchive":
			if ref := src.GetUploadRef(); ref != "" && !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
	}
	return refs
}

// Resume claims an idle pod for the request's session and restores the
// session's workspace onto it from the named §4.4 checkpoint via the
// adapter's Resume RPC. It is the §7.1 resume counterpart of Bind: used
// when a suspended session's original pod was released and the session
// must be rebuilt on a replacement pod. Any failure after the claim is
// returned so the gateway can retry on a fresh pod.
func (b *Binder) Resume(ctx context.Context, req ResumeRequest) (*BindResult, error) {
	sb, cl, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	if _, err := cl.Resume(ctx, adapterclient.ResumeParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		CheckpointID:       req.CheckpointID,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		cl.Close()
		return nil, fmt.Errorf("podsession: resume session on pod %s: %w", sb.Name, err)
	}
	// The resumed pod's §6.2 phase chain (resume_pending → resuming →
	// attached) is driven by the resume watchdog path, tracked separately; the
	// fresh pod stays in `claimed` here. Release's IsValid guard handles that
	// gracefully (it skips the terminal-disposition write when no edge exists
	// from claimed) and the drain reclaims the pod regardless.
	return &BindResult{
		SessionID:   req.SessionID,
		TenantID:    req.TenantID,
		SandboxName: sb.Name,
		PodIP:       sb.Status.PodIP,
		Adapter:     cl,
	}, nil
}

// connect claims an idle pod from the pool, resolves the claimed Sandbox,
// dials its adapter, and runs the §15.5 version handshake. On success the
// caller owns cl and must close it once the session ends or on any later
// failure, and owns the returned Sandbox object for chaining §6.2 phase
// transitions. The shared claim-and-handshake path of Bind and Resume.
func (b *Binder) connect(ctx context.Context, pool, sessionID, tenantID string) (sb *lennyv1.Sandbox, cl *adapterclient.Client, err error) {
	claimer := &podclaim.Claimer{Client: b.Client, Namespace: b.Namespace}
	req := podclaim.ClaimRequest{
		Pool:      pool,
		SessionID: sessionID,
		TenantID:  tenantID,
	}
	var sandboxName string
	claim, err := claimer.Claim(ctx, req)
	if errors.Is(err, podclaim.ErrNoIdlePod) {
		// The Kubernetes-API claim found no idle pod. Attempt the §4.6.1
		// Postgres-backed fallback claim before surfacing the error.
		sandboxName, err = b.fallbackClaim(ctx, req)
		if err != nil {
			return nil, nil, err
		}
	} else if err != nil {
		return nil, nil, err
	} else {
		sandboxName = claim.Spec.SandboxRef
	}

	sb, err = b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return nil, nil, err
	}

	addr := net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return nil, nil, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, nil, fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, nil, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		)
	}
	return sb, cl, nil
}

// fallbackClaim runs the §4.6.1 Postgres-backed fallback claim after
// the Kubernetes-API claim returned podclaim.ErrNoIdlePod. It returns
// the claimed Sandbox name, or podclaim.ErrNoIdlePod when the fallback
// is disabled, the mirror is too stale to trust, or the mirror also
// has no idle pod (the warm pool is genuinely exhausted, which the
// caller surfaces as WARM_POOL_EXHAUSTED).
//
// The fallback claims an agent_pod_state row, then reproduces the
// authoritative side of a claim: it creates the binding SandboxClaim
// CRD (so the lenny-sandboxclaim-guard webhook's CREATE-time check
// still guards against a double-claim) and best-effort flips the
// Sandbox CRD phase idle → claimed, tolerating a conflict the same way
// podclaim.Claimer.Claim does.
func (b *Binder) fallbackClaim(ctx context.Context, req podclaim.ClaimRequest) (string, error) {
	if b.Fallback == nil {
		// No Postgres mirror is configured; the no-idle-pod result stands.
		// This is the absence of a fallback path, not a skip of a
		// configured one, so it is not counted.
		return "", podclaim.ErrNoIdlePod
	}

	// Freshness precondition: above podClaimFallbackMaxMirrorLagSeconds
	// the mirror may still show pods already claimed in etcd but not yet
	// mirrored, so a fallback claim would race the Kubernetes-API claim.
	maxLag := b.FallbackMaxMirrorLagSeconds
	if maxLag == 0 {
		maxLag = DefaultFallbackMaxMirrorLagSeconds
	}
	lag, err := b.Fallback.MirrorLagSeconds(ctx, req.Pool)
	if err != nil {
		return "", fmt.Errorf("podsession: read mirror lag for pool %s: %w", req.Pool, err)
	}
	if lag > maxLag {
		// Precondition 1 (mirror freshness): the mirror is too stale to
		// trust; defer to the no-idle-pod result rather than risk claiming
		// an already-claimed pod.
		b.recordFallbackSkip(FallbackSkipReasonMirrorStale)
		return "", podclaim.ErrNoIdlePod
	}

	// Precondition 2 (admission reachability): the fallback's
	// lenny-sandboxclaim-guard CREATE check traverses the API server, so
	// probe reachability before locking a mirror row. A failed probe
	// means full API-server unavailability (distinct from watch-stream
	// degradation, which the fallback is designed for), so skip rather
	// than waste a SELECT ... FOR UPDATE SKIP LOCKED that would fail on
	// the subsequent CRD CREATE anyway.
	if b.APIServerReachable != nil {
		if err := b.APIServerReachable(ctx); err != nil {
			b.recordFallbackSkip(FallbackSkipReasonAPIServerUnreachable)
			return "", podclaim.ErrNoIdlePod
		}
	}

	pod, claimed, err := b.Fallback.ClaimIdle(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return "", fmt.Errorf("podsession: fallback claim from pool %s: %w", req.Pool, err)
	}
	if !claimed {
		// The mirror also has no idle pod: the warm pool is exhausted.
		return "", podclaim.ErrNoIdlePod
	}

	// Reproduce the authoritative side of a claim. Create the binding
	// SandboxClaim first: the lenny-sandboxclaim-guard webhook rejects
	// the CREATE if the pod is already claimed, which backstops the
	// §4.6.1 single-claim invariant for the fallback path.
	if _, err := podclaim.CreateClaim(ctx, b.Client, b.Namespace, pod.PodID, req); err != nil {
		return "", err
	}

	// Best-effort flip the Sandbox CRD phase idle → claimed, the
	// authoritative state. A conflict means a competing writer already
	// advanced the pod; the SandboxClaim above still binds this session,
	// so the claim holds and the conflict is tolerated.
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: pod.PodID}, &sb); err != nil {
		return "", fmt.Errorf("podsession: get sandbox %s for fallback claim: %w", pod.PodID, err)
	}
	if sb.Status.Phase == string(state.Idle) {
		sb.Status.Phase = string(state.Claimed)
		if err := b.Client.Status().Update(ctx, &sb); err != nil && !apierrors.IsConflict(err) {
			return "", fmt.Errorf("podsession: claim sandbox %s in fallback: %w", pod.PodID, err)
		}
	}
	return pod.PodID, nil
}

// recordFallbackSkip increments the §4.6.1
// lenny_pod_claim_fallback_skipped_total counter for reason when a
// counter hook is wired. A nil hook is a no-op.
func (b *Binder) recordFallbackSkip(reason string) {
	if b.FallbackSkipped != nil {
		b.FallbackSkipped(reason)
	}
}

// Release tears down a session that Bind placed on a pod: it shuts the
// pod's runtime down through the adapter, closes the adapter connection,
// records the session's terminal disposition on the Sandbox, and drains
// the pod so the Sandbox reconciler reclaims it (§6.2 → draining →
// terminated). terminal is the §6.2 terminal phase the session reached
// (completed, failed, cancelled, or expired); the gateway maps the session
// state to it. When terminal names a valid edge from the Sandbox's current
// phase, Release records it (attached → completed/failed/cancelled, §6.2
// lines 105-117) before draining so the authoritative state machine reflects
// the outcome; a non-terminal or non-applicable value skips straight to the
// drain. The adapter Shutdown and the disposition write are best-effort —
// the drain reclaims the pod regardless — so Release returns only an error
// from the drain transition. spec: §6.2 lines 105-117, 305.
func (b *Binder) Release(ctx context.Context, result *BindResult, terminal state.State) error {
	if result.Adapter != nil {
		_, _ = result.Adapter.Shutdown(ctx, result.SessionID)
		result.Adapter.Close()
	}

	// spec: §7.1 line 52 (step 23) — release the session's §4.9 credential
	// leases back to the pool. Done before the drain so the credential's
	// active-session counter is decremented on the way out; without it the
	// pool's per-credential slot count drifts up without bound and
	// select.go eventually reports exhaustion for idle credentials.
	b.releaseCredentials(result.SessionID)

	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: result.SandboxName}, &sb); err != nil {
		return fmt.Errorf("podsession: get sandbox %s: %w", result.SandboxName, err)
	}

	// §6.2 lines 105-117: record the terminal disposition on the Sandbox
	// before draining the exclusive pod. Guarded by IsValid so a disposition
	// with no edge from the current phase (e.g. attached → expired, which the
	// state machine does not model, or a resumed pod still in claimed) is
	// skipped gracefully — the disposition is still on the session row and the
	// §11.7 audit log — rather than failing the drain.
	if terminal != "" && terminal != state.Terminated {
		if from := state.State(sb.Status.Phase); state.IsValid(from, terminal) == nil {
			if err := b.setPhase(ctx, &sb, terminal); err != nil {
				log.Printf("podsession: record terminal phase %s on sandbox %s: %v", terminal, result.SandboxName, err)
			}
		}
	}

	return b.drain(ctx, &sb)
}

// resolveSandbox reads the claimed Sandbox and verifies it carries a pod
// address. The Sandbox reconciler records status.podIP once the pod is
// running, so a pod that was idle when claimed carries an address. The full
// object is returned so the caller can chain §6.2 phase transitions against
// it without a re-Get.
func (b *Binder) resolveSandbox(ctx context.Context, sandboxName string) (*lennyv1.Sandbox, error) {
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: sandboxName}, &sb); err != nil {
		return nil, fmt.Errorf("podsession: get sandbox %s: %w", sandboxName, err)
	}
	if sb.Status.PodIP == "" {
		return nil, fmt.Errorf("podsession: sandbox %s has no pod IP", sandboxName)
	}
	return &sb, nil
}
