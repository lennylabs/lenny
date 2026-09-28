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
	"sync"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/core/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/provisioning/vcscred"
	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
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
	// VCSCreds materializes the §14 gitClone VCS token so Bind can clone
	// a private repository on the gateway's network path. Nil when no VCS
	// resolver is wired; a plan with an authenticated gitClone source
	// then fails to stage rather than cloning unauthenticated.
	VCSCreds vcscred.Resolver
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
	// UserCredentials is the §4.9 Pre-Authorized Credential Flow's
	// user-source delivery service. When a BindRequest names user-credential
	// providers, Bind materializes a proxy-mode lease for each from the
	// user's registered credential and pushes it to the pod alongside the
	// pool leases. Nil when user-source credentials are not configured; a
	// BindRequest then names no user providers and Bind delivers none.
	// spec: §4.9.
	UserCredentials UserCredentialAssigner
	// SlotCounter is the §5.2 atomic slot counter and the only intra-pod
	// capacity gate for the maxConcurrentSessions > 1 slot path. Wired in
	// production installs that expose --redis-url; the SlotClaimer
	// constructed per BindSlot call carries it through so the Redis Lua
	// GET-compare-INCR sequence enforces maxConcurrentSessions atomically
	// across gateway replicas. It is required: a nil counter makes both
	// ClaimSlot and ReleaseSlot fail closed (each returns a configuration
	// error rather than degrading to an SSA-only path, which no longer
	// exists), so slot release cannot over-release a pod that still hosts
	// live slots. A Redis outage does not
	// disable the gate. It routes to the §12.4 Postgres-fallback capacity
	// gate under a per-pod advisory lock, which fails closed after a bounded
	// outage window. spec: §5.2, §12.4.
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
	// SlotConflict records a §5.2 concurrent-mode slot
	// reservation failure due to slot contention, backing the
	// lenny_slot_assignment_conflict_total counter (labeled by pool).
	// It is threaded into the per-BindSlot SlotClaimer. Nil is a no-op.
	SlotConflict func(pool string)
	// SlotFailure records a §5.2 concurrent-workspace slot bind
	// failure after a slot was reserved, backing the
	// lenny_slot_failure_total counter (labeled by error_type, pool, and
	// k8s_pod_name). errorType names the bind stage that failed. Nil is a
	// no-op.
	SlotFailure func(errorType, pool, podName string)
	// SlotReclaim records the outcome of a compensating Shutdown that
	// reclaims a slot after a failed bind, backing the §16.1
	// lenny_slot_compensation_superseded_total counter (labeled by
	// error_type, pool, and k8s_pod_name). outcome is the adapter's reclaim
	// outcome and cause is the error_type value (`refusal` or `failure`).
	// Nil is the no-op default; the field is never cleared once set.
	// spec: §16.1, §4.7.1.
	SlotReclaim func(outcome, cause, pool, podName string)
	// Rehydration records a §5.2 post-recovery slot-counter
	// rehydration event, backing the lenny_slot_rehydration_total counter
	// (labeled by pod and pool). It is threaded into the per-BindSlot
	// SlotClaimer. Nil is a no-op.
	Rehydration func(podID, pool string)
	// ClaimAccepted records the §6.3 / §16.1
	// `lenny_warmpool_claims_total{pool,runtime_class}` counter
	// increment on each idle→claimed transition the §6.1 warm pool
	// observes. Bind and Resume both go through `connect()`, so the
	// counter rolls up session-mode + resume claims. Slot claims
	// (BindSlot / concurrent mode) are accounted separately under
	// §5.2; the deployer-facing demotion-rate ratio (§6.3)
	// keys off this denominator. Nil is a no-op.
	// spec: §6.3, §16.1.
	ClaimAccepted func(pool, runtimeClass string)
	// SDKDemotion records one §6.1 SDK-warm demotion: the binder
	// demoted an SDK-warm pod to pod-warm because the workspace plan
	// matched a sdkWarmBlockingPaths pattern. pool is the demoted pod's
	// pool and teardownSeconds is the §6.3 DemoteSDK teardown
	// penalty. The deployer-facing demotion rate (§6.3) is this
	// numerator over the ClaimAccepted denominator. Nil is a no-op.
	SDKDemotion func(pool string, teardownSeconds float64)
	// IntegrationLevelProbeWaitMs bounds how long the §5.1 first-assignment
	// observed-integration-level probe waits for the runtime's first §4.7
	// lifecycle handshake before the adapter classifies the runtime. Zero
	// selects DefaultIntegrationLevelProbeWaitMs. A Full runtime dials the
	// channel shortly after boot, so the window is fully consumed only when
	// a runtime never opens the channel (the underperformance case the
	// probe catches). spec: §5.1.
	IntegrationLevelProbeWaitMs int32
	// IntegrationLevelUnderdeclared records the §5.1
	// `runtime.integrationLevel.underdeclared` warning: the observed level
	// exceeds the declared level, so the author can raise the declared
	// level in a future release. Called at most once per runtime per
	// gateway process. Nil is a no-op. spec: §5.1.
	IntegrationLevelUnderdeclared func(runtime, declared, observed string)
	// integrationVerified gates the §5.1 observed-level probe to the first
	// session assignment per runtime: a runtime whose observed level met or
	// exceeded its declared level is recorded so later assignments skip the
	// probe. Underperforming runtimes are not recorded, so every assignment
	// keeps being rejected. spec: §5.1.
	integrationVerified sync.Map
	// UploadGate is the §4.1 Upload Handler subsystem the gateway runs
	// archive extraction inside, so a hostile archive's decompression is
	// bounded by the same goroutine pool, concurrency limiter, and circuit
	// breaker that gate the upload HTTP path and cannot starve session
	// attachment or delegation. Production wires the shared
	// `upload_handler` subsystem; nil runs extraction ungated (tests).
	// spec: §7.4 — F-7.4.1, F-13.4.1.
	UploadGate *subsystem.Subsystem
	// ExtractionAbort records one §7.4 archive-extraction abort,
	// backing `lenny_upload_extraction_aborted_total{error_type}`. errorType
	// is the §13.4 sub-code (max_decompressed_size, non_regular_entry,
	// symlink, etc.). Called only on the gateway extraction path. Nil is a
	// no-op. spec: §7.4; §16.1 — F-7.4.11.
	ExtractionAbort func(errorType string)
	// HoldCanceller cancels the holding replica's local §6.2 reserved-hold
	// expiry timer after a successful acquisition-path rebind, so the timer
	// does not issue a wasted no-op DELETE. The rebind patch already changed
	// the claim resourceVersion, so the precondition guard is the
	// authoritative race resolver and a missed cancellation is harmless. Nil
	// is a no-op (a deployment with no in-process hold coordinator, or a
	// peer-held reserved claim). spec: §6.2.
	HoldCanceller HoldCanceller
	// RecycleBoundary arms the §5.2 gateway-side missing-report timeout when
	// Release patches the per-pod claim bound → recycling on a recycling pool.
	// The adapter then runs the whole-pod scrub and reports it via
	// ReportPodScrub; the report cancels the timer. If no report arrives within
	// the pool's cleanupTimeoutSeconds plus a grace, the coordinator retires the
	// pod so a hung or silent adapter does not leave it stuck in `recycling`
	// until the much longer §4.6.1 orphan-GC window. Nil is a no-op (a
	// deployment with no in-process recycle coordinator); the orphan GC remains
	// the crash backstop. spec: §5.2 (missing-report timeout).
	RecycleBoundary RecycleBoundaryArmer
	// Now supplies the wall clock for the §6.2 reserved-hold-window check on
	// the acquisition-path rebind branch. Nil uses time.Now.
	Now func() time.Time
}

// Bind claims an idle pod for the request's session and runs the whole
// §4.7 session-assignment sequence: PrepareWorkspace stages uploaded files
// and cloned repositories, FinalizeWorkspace materializes the workspace,
// RunSetup runs the plan's setup commands, AssignCredentials delivers the
// session's §4.9 credential leases, and StartSession starts the runtime. On
// success the caller owns the returned live adapter connection. Any failure
// after the claim is returned so the gateway can retry on a fresh pod.
//
// Bind is the thin claim → prepare → launch composition for the callers
// that run the whole sequence in one call (the §4.7 combined create-and-
// start path and the test harness). The decomposed §7.1 lifecycle invokes
// Claim, Prepare, and Launch independently across /create, /finalize, and
// /start; each reconnects to the claimed pod from the persisted binding
// (§4.6) rather than holding one connection across the whole window.
func (b *Binder) Bind(ctx context.Context, req BindRequest) (*BindResult, error) {
	claim, err := b.Claim(ctx, req)
	if err != nil {
		return nil, err
	}
	// Thread the claim binding onto the request the way the persisted row
	// would for the decomposed path, so Prepare and Launch reconnect from
	// the binding rather than a held connection.
	req.SandboxName = claim.SandboxName
	prep, err := b.Prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	req.Demoted = prep.Demoted
	res, err := b.Launch(ctx, req)
	if err != nil {
		// Launch already reclaimed the pod and any assigned lease on every
		// failure path: a launch-step failure via its failPhase reclaim, and a
		// reconnect failure before the first launch step via ReclaimClaimed.
		// Surface the error so the caller retries on a fresh pod.
		return nil, err
	}
	// Reassemble the monolithic BindResult: the live adapter and launch
	// timing come from Launch, the prepared workspace and setup trail from
	// Prepare, and the claim timing from Claim. spec: §6.3.
	res.Timings.PodClaim = claim.PodClaim
	res.Timings.WorkspaceMaterialization = prep.Timings.WorkspaceMaterialization
	res.Timings.SetupCommands = prep.Timings.SetupCommands
	res.Timings.CredentialAssignment = prep.Timings.CredentialAssignment
	res.WorkspacePlanWarnings = prep.WorkspacePlanWarnings
	res.SetupOutputs = prep.SetupOutputs
	res.WorkspaceBase = prep.WorkspaceBase
	return res, nil
}
