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
	"os"
	"strconv"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/lennylabs/lenny/pkg/agentpodstate"
	lennyv1 "github.com/lennylabs/lenny/pkg/apis/lenny/v1alpha1"
	"github.com/lennylabs/lenny/pkg/blobstore"
	"github.com/lennylabs/lenny/pkg/gateway/adapterclient"
	"github.com/lennylabs/lenny/pkg/gateway/gitref"
	"github.com/lennylabs/lenny/pkg/gateway/podclaim"
	"github.com/lennylabs/lenny/pkg/gateway/sdkwarm"
	"github.com/lennylabs/lenny/pkg/gateway/storage/slotcounter"
	"github.com/lennylabs/lenny/pkg/gateway/subsystem"
	"github.com/lennylabs/lenny/pkg/gateway/vcscred"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
	"github.com/lennylabs/lenny/pkg/sandbox/isolation"
	"github.com/lennylabs/lenny/pkg/sandbox/state"
	"github.com/lennylabs/lenny/pkg/upload"
	"github.com/lennylabs/lenny/pkg/upload/archive"
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
	// spec: §4.9 lines 1340-1381.
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
	// ClaimAccepted records the §6.3 line 352 / §16.1 line 122
	// `lenny_warmpool_claims_total{pool,runtime_class}` counter
	// increment on each idle→claimed transition the §6.1 warm pool
	// observes. Bind and Resume both go through `connect()`, so the
	// counter rolls up session-mode + resume claims. Slot claims
	// (BindSlot / concurrent mode) are accounted separately under
	// §5.2; the deployer-facing demotion-rate ratio (§6.3 line 352)
	// keys off this denominator. Nil is a no-op.
	// spec: §6.3 line 352, §16.1 line 122.
	ClaimAccepted func(pool, runtimeClass string)
	// SDKDemotion records one §6.1 line 34 SDK-warm demotion: the binder
	// demoted an SDK-warm pod to pod-warm because the workspace plan
	// matched a sdkWarmBlockingPaths pattern. pool is the demoted pod's
	// pool and teardownSeconds is the §6.3 line 352 DemoteSDK teardown
	// penalty. The deployer-facing demotion rate (§6.3 line 352) is this
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
	// IntegrationLevelUnderdeclared records the §5.1 line 43
	// `runtime.integrationLevel.underdeclared` warning: the observed level
	// exceeds the declared level, so the author can raise the declared
	// level in a future release. Called at most once per runtime per
	// gateway process. Nil is a no-op. spec: §5.1 line 43.
	IntegrationLevelUnderdeclared func(runtime, declared, observed string)
	// integrationVerified gates the §5.1 observed-level probe to the first
	// session assignment per runtime: a runtime whose observed level met or
	// exceeded its declared level is recorded so later assignments skip the
	// probe. Underperforming runtimes are not recorded, so every assignment
	// keeps being rejected. spec: §5.1 line 41 ("the first session
	// assignment").
	integrationVerified sync.Map
	// UploadGate is the §4.1 Upload Handler subsystem the gateway runs
	// archive extraction inside, so a hostile archive's decompression is
	// bounded by the same goroutine pool, concurrency limiter, and circuit
	// breaker that gate the upload HTTP path and cannot starve session
	// attachment or delegation. Production wires the shared
	// `upload_handler` subsystem; nil runs extraction ungated (tests).
	// spec: §7.4 line 448 — F-7.4.1, F-13.4.1.
	UploadGate *subsystem.Subsystem
	// ExtractionAbort records one §7.4 line 462 archive-extraction abort,
	// backing `lenny_upload_extraction_aborted_total{error_type}`. errorType
	// is the §13.4 sub-code (max_decompressed_size, non_regular_entry,
	// symlink, etc.). Called only on the gateway extraction path. Nil is a
	// no-op. spec: §7.4 line 462; §16.1 — F-7.4.11.
	ExtractionAbort func(errorType string)
	// HoldCanceller cancels the holding replica's local §3.2 reserved-hold
	// expiry timer after a successful acquisition-path rebind, so the timer
	// does not issue a wasted no-op DELETE. The rebind patch already changed
	// the claim resourceVersion, so the precondition guard is the
	// authoritative race resolver and a missed cancellation is harmless. Nil
	// is a no-op (a deployment with no in-process hold coordinator, or a
	// peer-held reserved claim). spec: §3.2.
	HoldCanceller HoldCanceller
	// RecycleBoundary arms the §3.4 gateway-side missing-report timeout when
	// Release patches the per-pod claim bound → recycling on a recycling pool.
	// The adapter then runs the whole-pod scrub and reports it via
	// ReportPodScrub; the report cancels the timer. If no report arrives within
	// the pool's cleanupTimeoutSeconds plus a grace, the coordinator retires the
	// pod so a hung or silent adapter does not leave it stuck in `recycling`
	// until the much longer §4.6.1 orphan-GC window. Nil is a no-op (a
	// deployment with no in-process recycle coordinator); the orphan GC remains
	// the crash backstop. spec: §3.4 (missing-report timeout).
	RecycleBoundary RecycleBoundaryArmer
	// Now supplies the wall clock for the §3.2 reserved-hold-window check on
	// the acquisition-path rebind branch. Nil uses time.Now.
	Now func() time.Time
}

// HoldCanceller cancels a §3.2 reserved-hold expiry timer this replica holds
// for the pod's claim. *recycle.HoldCoordinator satisfies it through Cancel;
// the interface is defined at this consumer so podsession does not import the
// recycle package. spec: §3.2 (within-hold rebind cancels the local timer).
type HoldCanceller interface {
	Cancel(podID string)
}

// RecycleBoundaryArmer arms the §3.4 missing-report timeout for a pod at the
// bound → recycling patch. *recycle.RecycleBoundaryCoordinator satisfies it
// through OnRecycling; the interface is defined at this consumer so podsession
// does not import the recycle package. spec: §3.4 (gateway-side missing-report
// timeout armed at session termination).
type RecycleBoundaryArmer interface {
	OnRecycling(podID string)
}

// SDKDemotionNotSupported is returned by Bind when a §6.1 preConnect pod's
// workspace plan requires demotion but the pod's adapter does not
// implement the DemoteSDK RPC (it returns UNIMPLEMENTED). Per §6.1 line 40
// the gateway fails the session with SDK_DEMOTION_NOT_SUPPORTED rather than
// serving the session with stale SDK state.
type SDKDemotionNotSupported struct {
	Pod string
}

func (e *SDKDemotionNotSupported) Error() string {
	return fmt.Sprintf("podsession: pod %s requires SDK demotion but its adapter does not implement DemoteSDK (SDK_DEMOTION_NOT_SUPPORTED)", e.Pod)
}

// workspacePlanPaths returns the relative workspace paths the plan places,
// one per §14 WorkspaceSource, for matching against sdkWarmBlockingPaths
// (§6.1 line 34).
func workspacePlanPaths(plan *adapterv1.WorkspacePlan) []string {
	if plan == nil {
		return nil
	}
	paths := make([]string, 0, len(plan.GetSources()))
	for _, src := range plan.GetSources() {
		if p := src.GetPath(); p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// isUnimplemented reports whether err is a gRPC UNIMPLEMENTED status, the
// §6.1 line 40 signal that a preConnect pod's adapter cannot DemoteSDK.
func isUnimplemented(err error) bool {
	return status.Code(err) == codes.Unimplemented
}

// RequiresDemotion reports the §6.1 SDK-warm demotion decision for a bind
// request: whether the request's workspace plan forces a still-SDK-warm
// (preConnect) pod to be demoted to pod-warm before the workspace is
// materialized. The decision is a pure function of the plan's placed paths and
// the runtime's sdkWarmBlockingPaths glob list, so the finalize-time Prepare
// (which makes the decision) and the launch-only /start path (which needs it
// without re-running Prepare) compute the identical answer from the persisted
// plan rather than the gateway persisting the boolean. A non-preConnect request
// never demotes. spec: §6.1 lines 34-40, §4.3, §4.4 (proposal).
func RequiresDemotion(req BindRequest) bool {
	if !req.PreConnect {
		return false
	}
	_, _, requires := sdkwarm.RequiresDemotion(workspacePlanPaths(req.Plan), req.SDKWarmBlockingPaths)
	return requires
}

// §5.2 line 12 lenny_slot_failure_total error_type labels: the
// concurrent-mode slot bind stages whose failure terminates a reserved
// slot. The set is finite so the metric stays low-cardinality.
const (
	slotFailureWorkspacePrep        = "workspace_prep"
	slotFailureSetup                = "setup"
	slotFailureCredentialAssignment = "credential_assignment"
	slotFailureSessionStart         = "session_start"
	// slotFailureConnect labels a reservation-bearing failure before the
	// post-connection bind stages run (resolve, dial, version handshake).
	// It is used only on the SlotBindError for retry classification; it is
	// not a lenny_slot_failure_total error_type value.
	slotFailureConnect = "connect"
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
	// disables binding. tenantID is recorded on the lease so the §4.9
	// LLM proxy can attribute proxy-extracted usage to the right tenant
	// (spec: §4.9 line 1468).
	AssignProto(poolName, sessionID, spiffeURI, tenantID string) (*adapterv1.CredentialLease, error)
	// ReleaseSession releases every §4.9 credential lease the session
	// holds back to its pool. It is the §7.1 step 23 teardown the binder
	// runs when a session's pod is released, so a completed session's
	// pool slots are returned rather than leaking. A session with no
	// leases is a no-op. spec: §7.1 line 52.
	ReleaseSession(sessionID string)
}

// UserCredentialAssigner materializes a session's §4.9 user-source
// credential leases (the Pre-Authorized Credential Flow). The gateway's
// usercreds.Materializer satisfies it; binder tests substitute a fake.
// MintProto resolves the user's registered credential for the provider
// into a proxy-mode lease, records it in the shared credential-lease
// store, caches the upstream secret for the §4.9 LLM proxy, and returns
// the wire-form lease the adapter materializes into the runtime credential
// file. User leases share the lease store the pool assigner uses, so the
// pool assigner's ReleaseSession releases them at teardown.
//
// spec: §4.9 lines 1340-1381.
type UserCredentialAssigner interface {
	MintProto(ctx context.Context, tenantID, userID, sessionID, spiffeURI, provider string) (*adapterv1.CredentialLease, error)
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
	// DeclaredIntegrationLevel is the runtime's §5.1 author-declared
	// integrationLevel ("basic", "standard", or "full"; empty is treated as
	// the §5.1 default "basic"). On the first session assignment to the
	// runtime, Bind probes the adapter for the observed level and rejects
	// the assignment with RUNTIME_LEVEL_UNDERPERFORMS when observed <
	// declared. spec: §5.1 lines 41-44.
	DeclaredIntegrationLevel string
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
	// ArchivePolicy is the §13.4 per-Runtime archive-extraction opt-in
	// block. The Binder forwards it to the adapter on FinalizeWorkspace so
	// uploadArchive symlink entries are admitted (and target-validated)
	// only for runtimes that opt in. Nil leaves the platform default
	// (symlinks rejected). spec: §7.4 lines 458, 462; §13.4 — F-7.4.4.
	ArchivePolicy *adapterv1.ArchivePolicy
	// CredentialPools names the §4.9 credential pool to lease from for
	// each authorized provider, keyed by provider. The caller resolves it
	// from the §4.9 intersection of the Runtime's supportedProviders and
	// the tenant's credentialPolicy.providerPools. Bind mints one lease
	// per entry and pushes the set to the pod via AssignCredentials
	// before StartSession. Empty (or nil) when the session needs no
	// upstream LLM credentials; Bind then assigns nothing.
	CredentialPools map[string]string
	// UserID is the session's owning user, used to resolve the §4.9
	// user-source credentials named in UserCredentialProviders. Empty when
	// the session names no user-source providers.
	UserID string
	// UserCredentialProviders names the providers whose §4.9 credential the
	// caller resolved to the user source (the Pre-Authorized Credential
	// Flow). For each, Bind materializes a proxy-mode lease from the user's
	// registered credential through UserCredentials and pushes it alongside
	// the pool leases. Empty when no provider resolved to a user credential.
	// spec: §4.9 lines 1340-1381.
	UserCredentialProviders []string
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
	// PreConnect is the runtime's §5.1 capabilities.preConnect flag. When
	// true the pod is SDK-warm: the binder either points the pre-connected
	// SDK at the workspace (ConfigureWorkspace) or, when the plan matches a
	// blocking path, demotes it (DemoteSDK) and proceeds pod-warm.
	PreConnect bool
	// SDKWarmBlockingPaths is the runtime's §5.1 sdkWarmBlockingPaths glob
	// list. When PreConnect is true and any workspace path matches, the
	// binder demotes the SDK-warm pod before materializing the workspace
	// (§6.1 line 34). An empty list disables demotion (§6.1 line 38).
	SDKWarmBlockingPaths []string
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag, resolved
	// by ResolvePool. When true and the session ends cleanly, Release patches
	// the per-pod claim bound → recycling and signals the adapter to run the
	// whole-pod scrub (the §3.4 recycle disposition) rather than draining the
	// pod; the adapter's ReportPodScrub then drives recycle vs. retire. A
	// failed/crashed session always retires regardless of this flag. spec:
	// §3.1, §3.4 (recycle on occupancy-zero).
	Recycle bool
	// SandboxName is the pod claimed at /create, persisted on the session
	// row. The decomposed §7.1 lifecycle (§4.6) sets it so Prepare and Launch
	// reconnect to the claimed pod from the binding rather than holding the
	// claim connection across /create → /finalize → /start. Empty for Claim,
	// which produces the binding. spec: §4.6 (proposal).
	SandboxName string
	// Demoted carries the §6.1 SDK-warm demotion decision Prepare made into
	// Launch, so Launch chooses StartSession (demoted or pod-warm) vs.
	// ConfigureWorkspace (still SDK-warm) without re-running the blocking-path
	// match. Meaningful only when PreConnect is true. spec: §6.1 lines 30-40.
	Demoted bool
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
	// SlotID identifies the §5.2 concurrent-workspace slot the session was
	// placed on. It is empty for an exclusive (maxConcurrentSessions=1) bind,
	// where the pod is claimed exclusively for the session. It is non-empty
	// only for a BindSlot result.
	SlotID string
	// MaxConcurrentSessions is the §5.2 sessionPolicy.maxConcurrentSessions
	// bound of the pod's pool, carried from the bind request. It is 0 (or 1)
	// for an exclusive session-mode bind and > 1 for a BindSlot result on a
	// concurrent-session pool. The gateway's per-slot message routing keys on
	// it: a bind reporting maxConcurrentSessions > 1 with an empty SlotID is
	// the §7.2 SLOT_ID_REQUIRED routing-bug case, where per-slot dispatch is
	// reached for a concurrent pod but the gateway resolved no slot for the
	// session, so the executor fails closed rather than misdelivering to
	// another slot. spec: §7.2 (per-slot routing, SLOT_ID_REQUIRED), §5.2.
	MaxConcurrentSessions int32
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag, carried
	// from the bind request so the release path can apply the §3.4 recycle
	// disposition without re-resolving the pool. On a recycling session-mode
	// pool a clean session release patches the claim bound → recycling and
	// signals the whole-pod scrub rather than draining the pod (Release). On a
	// recycling concurrent-session pool (the §3.1 "Concurrent" preset) the same
	// disposition runs when the last slot drains cleanly (ReleaseSlot →
	// SlotClaimer.ReleaseSlot). False for a non-recycling pool, where the pod
	// terminates after the session or cohort drains. spec: §3.1, §3.4,
	// §6.30/§6.41.
	Recycle bool
	// Adapter is the live connection to the pod's adapter. The caller
	// owns it and closes it when the session ends.
	Adapter *adapterclient.Client
	// Timings reports the wall-clock duration of each §6.3 hot-path
	// phase Bind executed, for the caller to record on the §6.3
	// per-phase and end-to-end startup-latency histograms. It is the
	// zero value for a BindSlot result, where the concurrent-mode
	// startup path is timed separately.
	Timings BindTimings
	// WorkspacePlanWarnings carries the §14 non-fatal advisories the
	// adapter raised during FinalizeWorkspace materialization. The
	// caller republishes each as an SSE event so clients see the §7.4
	// line 459 `workspace_plan_strip_components_skip` per-entry notice.
	// Nil when materialization produced no warnings or when Bind
	// returned before FinalizeWorkspace ran. F-7.4.15.
	WorkspacePlanWarnings []*adapterv1.WorkspacePlanWarning
	// WorkspaceRoot is the §7.3 line 408 / §6.1 absolute cwd path the
	// pod's adapter reported on the §15.5 version handshake. The gateway
	// persists it on the session row so a subsequent Resume can pass it
	// back via ResumeRequest.expected_workspace_root for the adapter to
	// assert "same absolute cwd path" before extracting any checkpoint
	// bytes. Empty when the adapter is on an older protocol that did
	// not report the field. F-7.3.15.
	WorkspaceRoot string
	// SetupOutputs carries the §7.5 line 475 captured stdout/stderr/exit
	// for each setup command the adapter ran. The gateway persists this
	// trail on the session row so it is visible through §15.1
	// GET /v1/sessions/{id} and the §11.7 audit log. F-7.5.4 / F-7.5.11.
	SetupOutputs []*adapterv1.SetupCommandOutput
}

// SetupCommandFailure wraps a §7.5 setup-command failure with the partial
// captured output the adapter returned before the abort. The gateway
// unwraps it on the §7.3 setup_command_failed classification path to
// persist the trail on the session row and to emit the §16.1
// `lenny_warmpool_warmup_failure_total{error_type=setup_command_failed}`
// counter. spec: §7.5 line 475, §7.3 line 387 — F-7.5.4 / F-7.5.9.
type SetupCommandFailure struct {
	// Pod is the sandbox name the failure was observed on.
	Pod string
	// Cause is the adapter-side error (a gRPC FailedPrecondition).
	Cause error
	// Outputs is the per-command transcript captured up to the failure,
	// including the failing command's stdout/stderr/exit code when
	// available.
	Outputs []*adapterv1.SetupCommandOutput
}

func (e *SetupCommandFailure) Error() string {
	return fmt.Sprintf("podsession: run setup on pod %s: %v", e.Pod, e.Cause)
}

func (e *SetupCommandFailure) Unwrap() error { return e.Cause }

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
	// RecoveryGeneration is the session's §4.2 / §7.3 pod-recovery
	// counter at issue time. Echoed back from the adapter on
	// ResumeResult so the gateway can verify the adapter consumed the
	// fenced generation. F-7.3.22.
	RecoveryGeneration int64
	// ExpectedWorkspaceBytes is the session's
	// last_checkpoint_workspace_bytes from sessionstore. Passed to the
	// adapter so it can run the §4.4 / §7.3 line 397 symmetric
	// workspace size pre-check before extraction. F-7.3.26.
	ExpectedWorkspaceBytes int64
	// WorkspaceSizeLimitBytes is the §4.4 hard workspace size cap from
	// the SandboxTemplate. F-7.3.26.
	WorkspaceSizeLimitBytes int64
	// ExpectedWorkspaceRoot is the §7.3 line 408 "same absolute cwd
	// path" the original session ran against. The gateway records the
	// SandboxTemplate's WorkspaceRoot at session creation; the adapter
	// asserts on Resume that the replacement pod's WorkspaceRoot
	// matches. Empty disables the assertion. F-7.3.15.
	ExpectedWorkspaceRoot string
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

// ClaimResult reports the §7.1-step-4 pod claim made at session create,
// for the gateway to persist on the session row so a later Prepare and
// Launch can reconnect to the claimed pod from the binding alone (§4.6).
// spec: §4.1 (proposal), §7.1 step 4.
type ClaimResult struct {
	// SandboxName is the claimed Sandbox the binding is persisted against.
	SandboxName string
	// Pool is the pool the pod was claimed from, persisted so Prepare and
	// Launch (and the §4.5 created-expiry reclaim) can name the pool.
	Pool string
	// PodIP is the claimed pod's address, returned for the §15.1 create
	// response and the §6.3 pod-claim metric.
	PodIP string
	// SlotID identifies the §5.2 concurrent-workspace slot reserved at
	// create by ClaimSlot. It equals SessionID (one session per slot), so
	// the binding is reconstructable from SandboxName + Pool + the session
	// id. Empty for an exclusive (maxConcurrentSessions=1) Claim, where the
	// whole pod is claimed for the session; non-empty marks the create-time
	// disposition as a reserved concurrent slot that /start reconnects to
	// via BindReservedSlot rather than re-reserving. spec: §5.2.
	SlotID string
	// WorkspaceRoot is the §7.3 absolute cwd the pod's adapter reported on
	// the §15.5 handshake at claim. Persisted so Prepare's archive symlink
	// canonicalization and Launch's SDK-warm ConfigureWorkspace cwd both
	// use the negotiated root without re-handshaking before they need it.
	// Empty for a ClaimSlot result: the per-slot workspace root is
	// negotiated when BindReservedSlot reconnects.
	WorkspaceRoot string
	// PodClaim is the §6.3 "pod claim and routing" phase duration (claim,
	// pod-IP resolution, mTLS dial, version handshake), for the create
	// handler to record on the §6.3 / §16.1 pod_claim phase histogram.
	PodClaim time.Duration
}

// PrepareResult reports the outcome of the §4.3 finalize-time preparation
// barrier so the finalize handler can persist the workspace root, the
// captured setup outputs, and the §7.4 strip-skip advisories, and emit the
// per-phase §6.3 timings. The adapter connection Prepare opened is closed
// before Prepare returns; Launch reconnects from the binding (§4.6).
// spec: §4.3 (proposal), §6.3 lines 358, 372.
type PrepareResult struct {
	// WorkspaceRoot is the §7.3 cwd the adapter reported when Prepare
	// reconnected, carried onto the session row for a later Resume.
	WorkspaceRoot string
	// Demoted reports whether the §6.1 SDK-warm pod was demoted to pod-warm
	// during Prepare. Launch reads it to decide StartSession vs.
	// ConfigureWorkspace without re-running the blocking-path match.
	Demoted bool
	// WorkspacePlanWarnings carries the §7.4 line 459 strip-skip advisories
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
	// Prepare, and the claim timing from Claim. spec: §6.3 lines 358, 372.
	res.Timings.PodClaim = claim.PodClaim
	res.Timings.WorkspaceMaterialization = prep.Timings.WorkspaceMaterialization
	res.Timings.SetupCommands = prep.Timings.SetupCommands
	res.Timings.CredentialAssignment = prep.Timings.CredentialAssignment
	res.WorkspacePlanWarnings = prep.WorkspacePlanWarnings
	res.SetupOutputs = prep.SetupOutputs
	res.WorkspaceRoot = prep.WorkspaceRoot
	return res, nil
}

// Claim performs the §7.1 step-4 pod claim at session create: it claims an
// idle warm pod from the pool, resolves the pod's adapter address, and runs
// the §15.5 version handshake to confirm the pod is usable and to negotiate
// the workspace root. It records the §6.3 pod-claim phase duration and
// returns the claimed Sandbox name, pool, pod IP, and negotiated workspace
// root for the gateway to persist on the session row, so a later Prepare
// and Launch reconnect from the binding without a held connection (§4.6).
// The handshake connection is closed before Claim returns. A pool-exhaustion
// or handshake failure is returned so the create handler surfaces it before
// the client uploads. spec: §4.1 (proposal), §7.1 step 4; §6.3 lines 358, 372.
func (b *Binder) Claim(ctx context.Context, req BindRequest) (*ClaimResult, error) {
	phaseStart := time.Now()
	sb, cl, neg, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return nil, err
	}
	// Claim runs only the claim and handshake; the setup chain reconnects
	// at Prepare, so the connection is not held across the upload window.
	cl.Close()
	return &ClaimResult{
		SandboxName:   sb.Name,
		Pool:          req.Pool,
		PodIP:         sb.Status.PodIP,
		WorkspaceRoot: neg.WorkspaceRoot,
		PodClaim:      time.Since(phaseStart),
	}, nil
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
// spec: §4.3 (proposal), §6.1 lines 34-40, §7.4, §4.9; §6.3 lines 358, 372.
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

	var t BindTimings
	// leaseAssigned tracks whether assignCredentials issued a lease in this
	// phase, so a failure in a LATER step reclaims the lease as well as the
	// pod (Gap 2): a finalize-block credential assignment must not leak the
	// lease back to the §4.9 pool when a subsequent step aborts.
	leaseAssigned := false
	reclaim := func() {
		b.failPhase(ctx, sb, leaseAssigned, req.SessionID)
		cl.Close()
	}

	// spec: §6.1 lines 34-40 — on an SDK-warm (preConnect) pod, decide
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
				reclaim()
				if isUnimplemented(err) {
					// spec: §6.1 line 40 — the runtime declared preConnect
					// but its adapter cannot tear down the SDK; fail the
					// session rather than serve it with stale SDK state.
					return nil, &SDKDemotionNotSupported{Pod: sandboxName}
				}
				return nil, fmt.Errorf("podsession: demote SDK on pod %s: %w", sandboxName, err)
			}
			demoted = true
			if b.SDKDemotion != nil {
				// spec: §6.3 line 352 — record the SDK teardown penalty.
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
	// spec: §7.4 line 458; §13.4 line 665 — symlink targets are
	// canonicalized against the pod's actual workspace root so the
	// gateway-side extraction matches the adapter's post-promotion
	// re-validation location.
	allow := upload.RuntimeAllow{
		AllowSymlinks: req.ArchivePolicy.GetAllowSymlinks(),
		WorkspaceRoot: firstNonEmpty(req.ArchivePolicy.GetWorkspaceRoot(), neg.WorkspaceRoot, archive.DefaultWorkspaceRoot),
	}
	stagedPlan, stageWarnings, err := b.stageWorkspace(ctx, cl, req.SessionID, "", req.TenantID, req.Plan, allow)
	if err != nil {
		reclaim()
		return nil, fmt.Errorf("podsession: stage workspace on pod %s: %w", sandboxName, err)
	}
	finalizeWarnings, err := cl.FinalizeWorkspace(ctx, req.SessionID, stagedPlan, req.ArchivePolicy, false)
	if err != nil {
		reclaim()
		return nil, fmt.Errorf("podsession: finalize workspace on pod %s: %w", sandboxName, err)
	}
	// §7.4 line 459 strip-skip warnings now originate gateway-side (the
	// archive is no longer decompressed in the pod); merge them ahead of
	// any adapter-raised advisories for the §7.2 SSE republish.
	finalizeWarnings = append(stageWarnings, finalizeWarnings...)
	t.WorkspaceMaterialization = time.Since(phaseStart)

	phaseStart = time.Now()
	setupOutputs, err := cl.RunSetup(ctx, req.SessionID, stagedPlan.GetSetupCommands(), req.SetupPolicy)
	if err != nil {
		reclaim()
		// spec: §7.5 line 488 — partial outputs ride alongside the failure
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
	if err := b.assignCredentials(ctx, cl, req); err != nil {
		reclaim()
		return nil, fmt.Errorf("podsession: assign credentials on pod %s: %w", sandboxName, err)
	}
	// The lease is now held; a failure after this point must also revoke it.
	leaseAssigned = true
	t.CredentialAssignment = time.Since(phaseStart)

	cl.Close()
	return &PrepareResult{
		WorkspaceRoot:         neg.WorkspaceRoot,
		Demoted:               demoted,
		WorkspacePlanWarnings: finalizeWarnings,
		SetupOutputs:          setupOutputs,
		Timings:               t,
	}, nil
}

// Launch runs the §4.4 start-time launch against the prepared pod. It
// reconnects to req.SandboxName from the persisted binding, then either
// starts the runtime from cold (StartSession, for a pod-warm or demoted
// pod) or points the pre-connected SDK at the finalized workspace
// (ConfigureWorkspace, for a still-SDK-warm pod), and verifies the §5.1
// observed integration level. On success the caller owns the returned live
// adapter connection. A launch failure reclaims the pod (and any lease
// assigned at Prepare) via failPhase and is returned so the gateway retries
// on a fresh pod. spec: §4.4 (proposal), §6.1 lines 30-34, §5.1 lines 41-44.
func (b *Binder) Launch(ctx context.Context, req BindRequest) (*BindResult, error) {
	sb, cl, neg, err := b.reconnect(ctx, req)
	if err != nil {
		// A reconnect failure (dial/handshake against the bound pod fails
		// transiently between /finalize and /start) strands the pod claimed at
		// /create AND the §4.9 lease Prepare assigned, since by Launch the
		// finalize block has always assigned it. Reclaim from the persisted
		// binding so the monolith invariant — any post-claim failure reclaims
		// the pod, and a post-AssignCredentials failure revokes the lease
		// (Gap 2) — holds before the first launch RPC runs. ReclaimClaimed
		// revokes the lease (keyed by sessionID) and deletes the per-pod claim.
		// spec: §4.6 (proposal), §7.1 step 23 (lease release).
		if rerr := b.ReclaimClaimed(ctx, req.SandboxName, req.SessionID); rerr != nil {
			log.Printf("podsession: reclaim claimed pod %s after Launch reconnect failure for session %s: %v", req.SandboxName, req.SessionID, rerr)
		}
		return nil, err
	}
	sandboxName := sb.Name
	// A launch failure reclaims the pod and the lease assigned at Prepare:
	// by Launch the finalize block has always assigned the lease, so the
	// reclaim revokes it (Gap 2). spec: §7.1 step 23 (lease release).
	reclaim := func() {
		b.failPhase(ctx, sb, true, req.SessionID)
		cl.Close()
	}

	phaseStart := time.Now()
	// spec: §6.1 lines 30-34 — a still-SDK-warm pod (preConnect, not
	// demoted) is started by pointing the pre-connected SDK at the
	// finalized workspace (ConfigureWorkspace) rather than booting the
	// runtime from cold (StartSession). A demoted or pod-warm pod uses
	// StartSession.
	if req.PreConnect && !req.Demoted {
		if err := cl.ConfigureWorkspace(ctx, req.SessionID, neg.WorkspaceRoot, req.ExperimentContext, req.TracingContext); err != nil {
			reclaim()
			return nil, fmt.Errorf("podsession: configure SDK-warm workspace on pod %s: %w", sandboxName, err)
		}
	} else if err := cl.StartSession(ctx, adapterclient.StartSessionParams{
		SessionID:          req.SessionID,
		Runtime:            req.Runtime,
		ExperimentContext:  req.ExperimentContext,
		TracingContext:     req.TracingContext,
		AgentInterface:     req.AgentInterface,
		MinPlatformVersion: req.MinPlatformVersion,
	}); err != nil {
		reclaim()
		return nil, fmt.Errorf("podsession: start session on pod %s: %w", sandboxName, err)
	}
	// spec: §5.1 lines 41-44 — the runtime has now booted, so the adapter
	// has had its first lifecycle_capabilities/lifecycle_support exchange.
	// On the first assignment to this runtime, compare the observed level
	// against the declared integrationLevel and reject the assignment with
	// RUNTIME_LEVEL_UNDERPERFORMS when the runtime delivers less than it
	// declares. An underperforming runtime fails before the session is
	// reported running, and the pod is reclaimed by draining it.
	if err := b.verifyIntegrationLevel(ctx, cl, req.Runtime, req.DeclaredIntegrationLevel); err != nil {
		reclaim()
		return nil, err
	}
	// spec: §6.2 — the session reaching `running` is a session-model state
	// recorded on the Postgres session row; the pod stays in the coarse
	// `claimed` phase. No CRD phase write happens here.
	var t BindTimings
	t.AgentSessionStart = time.Since(phaseStart)

	return &BindResult{
		SessionID:     req.SessionID,
		TenantID:      req.TenantID,
		SandboxName:   sandboxName,
		PodIP:         sb.Status.PodIP,
		Recycle:       req.Recycle,
		Adapter:       cl,
		Timings:       t,
		WorkspaceRoot: neg.WorkspaceRoot,
	}, nil
}

// failPhase reclaims a claimed pod whose setup chain aborted before the
// session was reported running. A pre-attached setup failure is a terminal
// claim disposition: the pod cannot serve the session and is retired by
// draining it (the coarse §6.2 claimed → draining → terminated edge), so
// the warm-pool sizer provisions a replacement. When leaseAssigned is true
// the finalize block had already pushed the §4.9 credential lease to the
// pod, so the reclaim also revokes the lease back to its pool (§7.1 step 23)
// rather than leaking the credential's active-session slot; leaseAssigned is
// false before assignCredentials runs, so the revoke is a no-op then (Gap 2).
// It is best-effort — the caller already returns the underlying error, and a
// drain lost to a concurrent reclamation does not change the outcome, because
// the orphaned claim is collected and the pod recycled (§4.6.1). spec: §6.2
// (claimed → draining on a failed claim disposition); §4.6.1 orphan-claim
// collection; §7.1 step 23 (lease release).
func (b *Binder) failPhase(ctx context.Context, sb *lennyv1.Sandbox, leaseAssigned bool, sessionID string) {
	if leaseAssigned {
		// spec: §7.1 step 23 — a lease assigned earlier in this phase must be
		// returned to its §4.9 pool on the reclaim, or the credential's
		// active-session counter leaks for the abandoned session.
		b.releaseCredentials(sessionID)
	}
	if err := b.drain(ctx, sb); err != nil {
		log.Printf("podsession: drain failed sandbox %s after pre-attached setup failure: %v", sb.Name, err)
	}
}

// ReclaimClaimed releases a pod claimed at /create that no live BindResult
// covers: a `created`/`finalizing`/`ready` session retired by the §4.5
// created-expiry sweeper or by /terminate, whose pod↔session binding is the
// persisted SandboxName + pool alone (§4.6). It deletes the per-pod
// SandboxClaim (returning the pod to the pool per the §4.6.1 occupancy
// projection) and revokes any §4.9 credential lease the session holds, keyed
// by sessionID (Credentials.ReleaseSession). The lease revoke is mandatory
// rather than best-effort-skipped: a `finalizing`/`ready` session always
// holds a lease assigned at finalize (§4.3), and ReleaseSession is a no-op
// for a `created` session that never assigned one, so the unconditional
// revoke fails closed without over-releasing. spec: §4.5 (proposal), §4.6
// (proposal), §7.1 step 23 (lease release); §4.6.1 (occupancy projection on
// claim DELETE).
func (b *Binder) ReclaimClaimed(ctx context.Context, sandboxName, sessionID string) error {
	// Revoke the lease first so a DELETE error does not strand the
	// credential's active-session slot; ReleaseSession is keyed by sessionID
	// and is a no-op for a session that holds no lease.
	b.releaseCredentials(sessionID)
	if err := podclaim.DeleteClaim(ctx, b.Client, b.Namespace, sandboxName); err != nil {
		return fmt.Errorf("podsession: reclaim claimed pod %s for session %s: %w", sandboxName, sessionID, err)
	}
	return nil
}

// reconnect re-establishes the §4.7 adapter connection to a pod claimed at
// /create from its persisted binding (§4.6): it resolves the Sandbox by the
// req.SandboxName recorded on the session row, dials the adapter, and re-runs
// the §15.5 version handshake. Prepare and Launch each call it so no phase
// depends on a connection held open by the phase before it. The caller owns
// cl and closes it on completion or reclaim. spec: §4.6 (proposal),
// §15.5 (version handshake).
func (b *Binder) reconnect(ctx context.Context, req BindRequest) (*lennyv1.Sandbox, *adapterclient.Client, negotiated, error) {
	if req.SandboxName == "" {
		// Fail closed: a Prepare/Launch with no persisted binding cannot
		// reconnect to the claimed pod, so reject rather than re-claiming a
		// fresh one and orphaning the pod claimed at /create.
		return nil, nil, negotiated{}, fmt.Errorf("podsession: reconnect for session %s has no claimed sandbox binding", req.SessionID)
	}
	sb, err := b.resolveSandbox(ctx, req.SandboxName)
	if err != nil {
		return nil, nil, negotiated{}, err
	}
	addr := net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))
	cl, err := b.DialAdapter(addr)
	if err != nil {
		return nil, nil, negotiated{}, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}
	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf("podsession: negotiate version with %s: %w", req.SandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", req.SandboxName,
		)
	}
	return sb, cl, negotiated{WorkspaceRoot: resp.GetWorkspaceRoot()}, nil
}

// drain releases a session-mode pod by deleting its per-pod occupancy
// SandboxClaim. The gateway does not write Sandbox.status (§4.6.3 ownership
// decomposition): the WarmPoolController is the sole writer of the coarse
// occupancy phase and projects it from claim existence and pool policy
// (§4.6.1). On a `recycle.enabled: false` pool a claim DELETE projects
// `draining` then `terminated`, so deleting the claim is the gateway's
// reclaim action; on a recycling pool under its limits the projection
// returns the pod to `idle`. The delete is idempotent — a claim already
// gone (a double release, or one the orphan GC collected) is a no-op.
//
// spec: §4.6.1 (occupancy projection on claim DELETE); §4.6.3 (gateway is
// not a writer of Sandbox.status). The WarmPoolController-side projection
// that consumes the claim DELETE lands in the WPC occupancy-projection step.
func (b *Binder) drain(ctx context.Context, sb *lennyv1.Sandbox) error {
	return podclaim.DeleteClaim(ctx, b.Client, b.Namespace, sb.Name)
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
	}
	if hasUser {
		// spec: §4.9 lines 1347-1351 — for each provider the pre-claim
		// resolved to the user source, materialize a proxy-mode lease from
		// the user's registered credential. The lease shares the credential-
		// lease store the pool path uses, so the pod sees a single
		// AssignCredentials set and teardown releases both alike.
		for _, provider := range req.UserCredentialProviders {
			lease, err := b.UserCredentials.MintProto(ctx, req.TenantID, req.UserID, req.SessionID, req.PodSpiffeURI, provider)
			if err != nil {
				return &CredentialAssignmentError{Provider: provider, Pool: "user", Err: err}
			}
			lease.Provider = provider
			leases[provider] = lease
		}
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
// non-filesystem-native sources, ahead of FinalizeWorkspace. It extracts
// every uploadArchive source — and every gitClone source's repository
// archive — inside the gateway (§7.4 line 448; §13.4 line 652 — the pod
// never decompresses external archives), rewriting each into the
// uploadFile / mkdir / symlink sources its already-validated entries
// produce; it fetches the blob content of every (original) uploadFile
// source from the §4.5 blob store; and it streams all of it to the pod
// via PrepareWorkspace. It returns the rewritten plan (which carries no
// uploadArchive or gitClone sources) and the §7.4 line 459
// strip-components-skip warnings the gateway raised during extraction. A
// plan that carries upload sources but binds through a Binder with no
// blob store fails rather than materializing an incomplete workspace.
// slotID selects the §6.4 concurrent-workspace per-slot staging area
// (/workspace/slots/{slotId}/staging) when non-empty; session-mode Bind
// passes "" so uploads stage into the pod-global /workspace/staging.
func (b *Binder) stageWorkspace(ctx context.Context, cl *adapterclient.Client, sessionID, slotID, tenantID string, plan *adapterv1.WorkspacePlan, allow upload.RuntimeAllow) (*adapterv1.WorkspacePlan, []*adapterv1.WorkspacePlanWarning, error) {
	uploads := make(map[string][]byte)

	// §7.4 line 448 / §13.4 line 652 — extract uploadArchive and gitClone
	// sources in the gateway and rewrite them into pre-extracted
	// file/dir/symlink sources whose bytes ride the same PrepareWorkspace
	// stream.
	rewritten, warnings, err := b.rewriteExtractedSources(ctx, plan, tenantID, uploads, allow)
	if err != nil {
		return nil, nil, err
	}

	if refs := uploadFileRefs(rewritten); len(refs) > 0 {
		for _, ref := range refs {
			// Synthetic refs for archive-extracted files already carry
			// their content; only original client uploadFile refs resolve
			// through the blob store.
			if _, ok := uploads[ref]; ok {
				continue
			}
			if b.Blobs == nil {
				return nil, nil, fmt.Errorf("plan has upload source(s) but the binder has no blob store")
			}
			uri, err := blobstore.ParseURI(ref)
			if err != nil {
				return nil, nil, fmt.Errorf("parse upload ref %q: %w", ref, err)
			}
			_, rc, err := b.Blobs.Get(uri)
			if err != nil {
				return nil, nil, fmt.Errorf("fetch upload %q: %w", ref, err)
			}
			content, err := io.ReadAll(rc)
			_ = rc.Close()
			if err != nil {
				return nil, nil, fmt.Errorf("read upload %q: %w", ref, err)
			}
			uploads[ref] = content
		}
	}

	if len(uploads) > 0 {
		var err error
		if slotID != "" {
			_, err = cl.PrepareWorkspaceSlot(ctx, sessionID, slotID, uploads)
		} else {
			_, err = cl.PrepareWorkspace(ctx, sessionID, uploads)
		}
		if err != nil {
			return nil, nil, err
		}
	}
	return rewritten, warnings, nil
}

// rewriteExtractedSources rewrites every §14 uploadArchive source and
// every gitClone source into the uploadFile / mkdir / symlink sources its
// extracted entries produce. uploadArchive blobs are fetched from the
// §4.5 blob store; gitClone repositories are cloned on the gateway's
// network path (so the pod never sees VCS credentials). Both are then
// decompressed inside the gateway's §4.1 Upload Handler subsystem
// (UploadGate), so the pod never sees the compressed bytes (§7.4 line
// 448; §13.4 line 652). Extracted file content is accumulated under
// synthetic refs in uploads so it rides the same PrepareWorkspace stream;
// directory and symlink entries become source records the adapter
// materializes without parsing untrusted input. A plan with no
// uploadArchive or gitClone source is returned unchanged. spec: §7.4
// lines 448-462; §13.4 — F-7.4.1, F-13.4.1.
func (b *Binder) rewriteExtractedSources(ctx context.Context, plan *adapterv1.WorkspacePlan, tenantID string, uploads map[string][]byte, allow upload.RuntimeAllow) (*adapterv1.WorkspacePlan, []*adapterv1.WorkspacePlanWarning, error) {
	needsRewrite := false
	for _, src := range plan.GetSources() {
		if t := src.GetType(); t == "uploadArchive" || t == "gitClone" {
			needsRewrite = true
			break
		}
	}
	if !needsRewrite {
		return plan, nil, nil
	}
	if allow.WorkspaceRoot == "" {
		allow.WorkspaceRoot = archive.DefaultWorkspaceRoot
	}

	newSources := make([]*adapterv1.WorkspaceSource, 0, len(plan.GetSources()))
	var warnings []*adapterv1.WorkspacePlanWarning
	for i, src := range plan.GetSources() {
		var res *archive.Result
		var err error
		switch src.GetType() {
		case "uploadArchive":
			res, err = b.extractOneArchive(ctx, src, i, allow)
		case "gitClone":
			res, err = b.extractGitCloneSource(ctx, src, tenantID, i, allow.WorkspaceRoot)
		default:
			newSources = append(newSources, src)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		var expanded []*adapterv1.WorkspaceSource
		expanded, warnings = appendExtracted(newSources, uploads, res, i, warnings)
		newSources = expanded
	}
	return &adapterv1.WorkspacePlan{
		SchemaVersion: plan.GetSchemaVersion(),
		Sources:       newSources,
		SetupCommands: plan.GetSetupCommands(),
	}, warnings, nil
}

// appendExtracted expands one extraction Result onto the source list. It
// lays directories first (so directory modes survive any implicit parent
// creation by a later file write), then files (whose content is staged
// under a synthetic ref), then symlinks (whose targets the gateway
// already validated). Returns the grown source slice and the accumulated
// warnings. spec: §7.4 — F-7.4.1.
func appendExtracted(sources []*adapterv1.WorkspaceSource, uploads map[string][]byte, res *archive.Result, sourceIndex int, warnings []*adapterv1.WorkspacePlanWarning) ([]*adapterv1.WorkspaceSource, []*adapterv1.WorkspacePlanWarning) {
	for _, d := range res.Dirs {
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "mkdir", Path: d.Path, Mode: modeOctal(d.Mode)})
	}
	for n, f := range res.Files {
		ref := syntheticArchiveRef(sourceIndex, n)
		uploads[ref] = f.Content
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "uploadFile", Path: f.Path, UploadRef: ref, Mode: modeOctal(f.Mode)})
	}
	for _, sl := range res.Symlinks {
		sources = append(sources, &adapterv1.WorkspaceSource{Type: "symlink", Path: sl.Path, LinkTarget: sl.Target})
	}
	return sources, append(warnings, archiveWarningsToProto(res.Warnings)...)
}

// extractOneArchive fetches an uploadArchive source's blob and decodes it
// inside the §4.1 Upload Handler subsystem gate, recording a §16.1
// extraction-abort metric for any §13.4 violation. F-7.4.1, F-7.4.11.
func (b *Binder) extractOneArchive(ctx context.Context, src *adapterv1.WorkspaceSource, sourceIndex int, allow upload.RuntimeAllow) (*archive.Result, error) {
	if b.Blobs == nil {
		return nil, fmt.Errorf("plan has an uploadArchive source but the binder has no blob store")
	}
	uri, err := blobstore.ParseURI(src.GetUploadRef())
	if err != nil {
		return nil, fmt.Errorf("parse uploadArchive ref %q: %w", src.GetUploadRef(), err)
	}
	_, rc, err := b.Blobs.Get(uri)
	if err != nil {
		return nil, fmt.Errorf("fetch uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	data, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return nil, fmt.Errorf("read uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	res, err := b.gatedExtract(ctx, func() (*archive.Result, error) {
		return archive.Extract(data, src.GetFormat(), int(src.GetStripComponents()), sourceIndex, src.GetPath(), allow)
	})
	if err != nil {
		return nil, fmt.Errorf("extract uploadArchive %q: %w", src.GetUploadRef(), err)
	}
	return res, nil
}

// extractGitCloneSource clones a §14 gitClone repository on the gateway's
// network path (so the runtime never sees VCS credentials) and decodes
// the resulting gzip-tar inside the §4.1 Upload Handler subsystem gate,
// exactly as an uploadArchive. Git histories commonly carry symlinks, so
// gitClone opts in to symlinks unconditionally; every target is still
// resolved through pkg/upload.ValidateSymlinkTarget against the workspace
// root. spec: §7.4 line 448; §13.4 line 652; §14 line 95 — F-7.4.1.
func (b *Binder) extractGitCloneSource(ctx context.Context, src *adapterv1.WorkspaceSource, tenantID string, sourceIndex int, workspaceRoot string) (*archive.Result, error) {
	// §14 line 95: an authenticated clone resolves the §4.9 VCS
	// credential-lease token on the gateway and injects it into the fetch.
	// A public clone (no auth block) proceeds with a zero credential.
	var cred gitref.Credential
	if mode := src.GetAuth().GetMode(); mode != "" {
		if b.VCSCreds == nil {
			return nil, fmt.Errorf("gitClone of %q uses auth.mode=%q but no VCS credential resolver is wired", src.GetUrl(), mode)
		}
		c, err := b.VCSCreds.Resolve(ctx, tenantID, src.GetUrl(), src.GetAuth().GetLeaseScope())
		if err != nil {
			return nil, fmt.Errorf("resolve gitClone credential for %q: %w", src.GetUrl(), err)
		}
		cred = gitref.Credential{Username: c.Username, Token: c.Token}
	}
	repoArchive, err := gitref.CloneArchive(ctx, src.GetUrl(), src.GetResolvedCommitSha(),
		gitref.CloneOptions{Depth: int(src.GetDepth()), Submodules: src.GetSubmodules(), Credential: cred})
	if err != nil {
		return nil, fmt.Errorf("clone %q: %w", src.GetUrl(), err)
	}
	allow := upload.RuntimeAllow{AllowSymlinks: true, WorkspaceRoot: workspaceRoot}
	res, err := b.gatedExtract(ctx, func() (*archive.Result, error) {
		return archive.Extract(repoArchive, "tar.gz", 0, sourceIndex, src.GetPath(), allow)
	})
	if err != nil {
		return nil, fmt.Errorf("extract gitClone %q: %w", src.GetUrl(), err)
	}
	return res, nil
}

// gatedExtract runs one archive decode inside the §4.1 Upload Handler
// subsystem (UploadGate) so a hostile archive's decompression shares the
// upload path's goroutine pool, concurrency limiter, and circuit breaker
// and cannot starve session attachment or delegation. It records the
// §16.1 extraction-abort metric on any failure. A nil gate runs the
// decode directly (tests). spec: §7.4 line 448; §16.1 — F-7.4.1, F-7.4.11.
func (b *Binder) gatedExtract(ctx context.Context, fn func() (*archive.Result, error)) (*archive.Result, error) {
	var res *archive.Result
	do := func(context.Context) error {
		r, err := fn()
		if err != nil {
			return err
		}
		res = r
		return nil
	}
	var err error
	if b.UploadGate != nil {
		err = b.UploadGate.Do(ctx, do)
	} else {
		err = do(ctx)
	}
	if err != nil {
		b.recordExtractionAbort(err)
		return nil, err
	}
	return res, nil
}

// recordExtractionAbort increments lenny_upload_extraction_aborted_total
// for a §13.4 extraction failure, labeling by the typed sub-code when the
// error is a *upload.ValidationError and "format_error" otherwise. spec:
// §7.4 line 462; §16.1 — F-7.4.11.
func (b *Binder) recordExtractionAbort(err error) {
	if b.ExtractionAbort == nil {
		return
	}
	errorType := string(upload.ReasonFormatError)
	var vErr *upload.ValidationError
	if errors.As(err, &vErr) {
		errorType = string(vErr.Reason)
	}
	b.ExtractionAbort(errorType)
}

// syntheticArchiveRef is the PrepareWorkspace upload ref for one archive-
// extracted file. It is a plain token with no path separators (the
// adapter hashes it into the staging directory), unique across the plan,
// and namespaced so it never collides with a client-supplied blob ref.
func syntheticArchiveRef(sourceIndex, fileIndex int) string {
	return fmt.Sprintf("__archx_%d_%d", sourceIndex, fileIndex)
}

// modeOctal renders a file mode's permission bits as the octal string the
// adapter's mkdir / uploadFile materializer parses.
func modeOctal(mode os.FileMode) string {
	return "0" + strconv.FormatUint(uint64(mode.Perm()), 8)
}

// firstNonEmpty returns the first non-empty string in vs, or "".
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// archiveWarningsToProto transcribes the gateway extractor's strip-skip
// warnings onto the proto warning surface the binder republishes on the
// session SSE stream. F-7.4.15.
func archiveWarningsToProto(ws []archive.Warning) []*adapterv1.WorkspacePlanWarning {
	if len(ws) == 0 {
		return nil
	}
	out := make([]*adapterv1.WorkspacePlanWarning, 0, len(ws))
	for _, w := range ws {
		out = append(out, &adapterv1.WorkspacePlanWarning{
			Code:            w.Code,
			SourceIndex:     int32(w.SourceIndex),
			EntryPath:       w.EntryPath,
			SegmentCount:    int32(w.SegmentCount),
			StripComponents: int32(w.StripComponents),
			Message:         w.Message,
		})
	}
	return out
}

// uploadFileRefs collects the distinct uploadRef values of the plan's
// uploadFile sources, in first-seen order. After archive rewriting the
// plan carries no uploadArchive sources, so only uploadFile refs need
// staging (originals through the blob store, synthetics in memory).
func uploadFileRefs(plan *adapterv1.WorkspacePlan) []string {
	seen := make(map[string]bool)
	var refs []string
	for _, src := range plan.GetSources() {
		if src.GetType() != "uploadFile" {
			continue
		}
		if ref := src.GetUploadRef(); ref != "" && !seen[ref] {
			seen[ref] = true
			refs = append(refs, ref)
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
//
// The returned ResumeResult carries the standard claim-and-handshake
// BindResult plus the §4.4 / §7.2 mode the adapter reported and the
// echoed §4.2 recovery_generation. F-7.3.22.
func (b *Binder) Resume(ctx context.Context, req ResumeRequest) (ResumeResult, error) {
	sb, cl, neg, err := b.connect(ctx, req.Pool, req.SessionID, req.TenantID)
	if err != nil {
		return ResumeResult{}, err
	}
	res, err := cl.Resume(ctx, adapterclient.ResumeParams{
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
	})
	if err != nil {
		cl.Close()
		return ResumeResult{}, fmt.Errorf("podsession: resume session on pod %s: %w", sb.Name, err)
	}
	// spec: §6.2 lines 82, 172 — the resumed session's fine states
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
			WorkspaceRoot: neg.WorkspaceRoot,
		},
		Mode:               res.Mode,
		RecoveryGeneration: res.RecoveryGeneration,
	}, nil
}

// negotiated bundles the handshake-reported metadata the caller needs
// to capture from connect. It carries the §15.5 NegotiateVersion
// response fields the gateway threads onto BindResult so downstream
// users (session-row persistence, Resume-time assertion) can see them.
type negotiated struct {
	// WorkspaceRoot is the adapter's reported §7.3 line 408 cwd path.
	// Empty when the adapter is on an older protocol. F-7.3.15.
	WorkspaceRoot string
}

// connect claims an idle pod from the pool, resolves the claimed Sandbox,
// dials its adapter, and runs the §15.5 version handshake. On success the
// caller owns cl and must close it once the session ends or on any later
// failure, and owns the returned Sandbox object for chaining §6.2 phase
// transitions. The shared claim-and-handshake path of Bind and Resume.
// The negotiated return value carries the handshake-reported metadata
// (workspace root, etc.) the caller threads onto BindResult.
func (b *Binder) connect(ctx context.Context, pool, sessionID, tenantID string) (sb *lennyv1.Sandbox, cl *adapterclient.Client, neg negotiated, err error) {
	claimer := &podclaim.Claimer{
		Client:    b.Client,
		Namespace: b.Namespace,
		Now:       b.Now,
		// On a §3.2 acquisition-path rebind, cancel the holding replica's local
		// hold-TTL timer so it does not issue a wasted no-op expiry DELETE.
		OnRebind: func(podID string) {
			if b.HoldCanceller != nil {
				b.HoldCanceller.Cancel(podID)
			}
		},
	}
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
			return nil, nil, negotiated{}, err
		}
	} else if err != nil {
		return nil, nil, negotiated{}, err
	} else {
		sandboxName = claim.Spec.SandboxRef
	}

	sb, err = b.resolveSandbox(ctx, sandboxName)
	if err != nil {
		return nil, nil, negotiated{}, err
	}

	addr := net.JoinHostPort(sb.Status.PodIP, strconv.Itoa(b.AdapterPort))
	cl, err = b.DialAdapter(addr)
	if err != nil {
		return nil, nil, negotiated{}, fmt.Errorf("podsession: dial adapter at %s: %w", addr, err)
	}

	resp, err := cl.NegotiateVersion(ctx, b.AcceptedVersions)
	if err != nil {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf("podsession: negotiate version with %s: %w", sandboxName, err)
	}
	if resp.GetIncompatible() {
		cl.Close()
		return nil, nil, negotiated{}, fmt.Errorf(
			"podsession: pod %s adapter speaks no protocol version the gateway accepts", sandboxName,
		)
	}
	// spec: §6.3 line 352, §16.1 line 122 — record the warm-pool claim
	// now that the idle→claimed transition has succeeded and the
	// adapter handshake has confirmed the pod is usable. Labels are
	// {pool, runtime_class}; the runtime_class is mapped from the
	// pod's §5.3 isolation profile so the §6.3 demotion-rate
	// denominator is per runtime class. An unrecognized profile would
	// mislabel the series; skip rather than emit an empty
	// runtime_class.
	if b.ClaimAccepted != nil {
		if rc, ok := isolation.RuntimeClassName(isolation.Profile(sb.Spec.IsolationProfile)); ok {
			b.ClaimAccepted(pool, rc)
		}
	}
	neg = negotiated{WorkspaceRoot: resp.GetWorkspaceRoot()}
	return sb, cl, neg, nil
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
// still guards against a double-claim), re-reads the Sandbox to confirm
// it is still idle (the WPC may have drained the pod between the mirror
// snapshot and the CRD lookup), and flips the Sandbox CRD phase idle →
// claimed via SSA Apply under the §4.6.3 gateway field manager +
// ForceOwnership. If the live Sandbox is past idle the just-created
// SandboxClaim is deleted and ErrNoIdlePod is returned so the caller
// surfaces warm-pool exhaustion rather than binding a session to a
// draining pod.
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
	claim, err := podclaim.CreateClaim(ctx, b.Client, b.Namespace, pod.PodID, req)
	if err != nil {
		return "", err
	}

	// Re-read the Sandbox to detect a mid-fallback drain: the mirror
	// snapshot may pre-date a §6.2 idle → draining transition the WPC
	// applied while we were locking the Postgres row. If the live phase
	// has moved past idle, delete the orphan claim and surface the
	// no-idle-pod result rather than binding a session to a doomed pod.
	// spec: §4.6.1 fallback claim consistency; §6.2 line 305.
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: pod.PodID}, &sb); err != nil {
		_ = b.Client.Delete(ctx, claim)
		return "", fmt.Errorf("podsession: get sandbox %s for fallback claim: %w", pod.PodID, err)
	}
	if sb.Status.Phase != string(state.Idle) {
		_ = b.Client.Delete(ctx, claim)
		return "", podclaim.ErrNoIdlePod
	}
	// §4.6.1: the per-pod claim is CREATEd with spec only; write its first
	// `bound` binding state with a subsequent status patch, the same first
	// status the in-cluster Claimer writes. The gateway does not write
	// Sandbox.status; the WarmPoolController projects the pod's occupancy
	// phase from the claim binding state.
	if err := podclaim.WriteBoundStatus(ctx, b.Client, b.Namespace, claim.Name); err != nil {
		_ = b.Client.Delete(ctx, claim)
		return "", err
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

// dispositionFailed is the disposition string Release treats as the §6.2
// "ends in failure or a crash" terminal that always retires the pod
// regardless of recycle settings. Every other clean terminal (completed,
// cancelled, expired) recycles on a recycling pool. spec: §6.2 lines 24, 157
// (a session that ends in failure or a crash always retires its pod).
const dispositionFailed = "failed"

// Release tears down a session that Bind placed on a pod: it releases the
// session's §4.9 credential leases, then applies the §3.4 disposition by either
// recycling the pod (patch the claim bound → recycling, then signal the
// whole-pod scrub) or draining it (signal the adapter shutdown, then delete the
// claim). disposition is the session-terminal outcome the session reached
// (completed, failed, cancelled, or expired); the fine session-terminal states
// and the Terminated session-condition fact are recorded on the Postgres
// session model (§7.2 / §8.8), so Release writes no Sandbox.status field — the
// WarmPoolController is the sole writer of Sandbox.status (§4.6.3).
//
// On a recycling pool (BindResult.Recycle, maxConcurrentSessions: 1) a clean
// terminal (anything but "failed") brings occupancy to zero. The §3.4 recycle
// disposition orders the two steps patch-then-scrub: Release first patches the
// per-pod claim bound → recycling (podclaim.WriteRecyclingStatus), then signals
// the adapter so the whole-pod scrub runs while the pod projects `claimed`. The
// claim must be in `recycling` before any §4.7 ReportPodScrub can arrive,
// because the claim state machine admits only recycling → reserved/released/failed,
// not bound → reserved (§3.2); the ReportPodScrub report then drives the
// recycle-vs-retire disposition off the `recycling` binding state. A
// non-recycling pool, or a failed/crashed session, takes the drain path: the
// adapter is signaled to tear the session down and the per-pod claim is deleted,
// which the WarmPoolController projects as draining → terminated (§4.6.1). The
// recycling patch is durable and ordered first; the adapter Shutdown is
// best-effort — a coordinating-gateway crash after the patch leaves the claim in
// `recycling` and the §4.6.1 orphan GC drains the stuck pod — so on the recycle
// path Release returns only an error from the recycling patch. spec: §3.1, §3.2,
// §3.4 (recycle on occupancy-zero, patch-then-scrub ordering); §4.6.1; §4.6.3;
// §7.2 / §8.8.
func (b *Binder) Release(ctx context.Context, result *BindResult, disposition string) error {
	// spec: §7.1 line 52 (step 23) — release the session's §4.9 credential
	// leases back to the pool. Done before the disposition so the credential's
	// active-session counter is decremented on the way out; without it the
	// pool's per-credential slot count drifts up without bound and
	// select.go eventually reports exhaustion for idle credentials.
	b.releaseCredentials(result.SessionID)

	// spec: §3.2 / §3.4 / §6.2 lines 24, 105, 157 — a recycling pool recycles
	// the pod across whole sessions of the same tenant when occupancy reaches
	// zero after a clean session termination; a failed/crashed session always
	// retires the pod. On the recycle path Release patches the claim
	// bound → recycling FIRST, then signals the adapter scrub: the claim must be
	// in `recycling` before any ReportPodScrub arrives, because the claim state
	// machine admits recycling → reserved/released/failed but not bound →
	// reserved (§3.2). On the retire path it tears the session down and deletes
	// the claim so the WarmPoolController drains the pod.
	if result.Recycle && disposition != dispositionFailed {
		if err := podclaim.WriteRecyclingStatus(ctx, b.Client, b.Namespace, podclaim.ClaimName(result.SandboxName), nil); err != nil {
			return fmt.Errorf("podsession: patch claim recycling for sandbox %s: %w", result.SandboxName, err)
		}
		// §3.4: arm the gateway-side missing-report timeout now that the claim
		// is `recycling`. The adapter's ReportPodScrub cancels it; if the report
		// never arrives within cleanupTimeoutSeconds plus a grace, the
		// coordinator retires the pod so a hung adapter does not leave it stuck
		// in `recycling` until the much longer orphan-GC window. Armed before
		// the best-effort Shutdown so a Shutdown that blocks does not delay the
		// timer.
		if b.RecycleBoundary != nil {
			b.RecycleBoundary.OnRecycling(result.SandboxName)
		}
		// The claim now projects `recycling`. This Shutdown is the
		// occupancy-zero signal that runs the whole-pod scrub the adapter
		// reports via §4.7 ReportPodScrub, which drives the disposition off the
		// `recycling` binding state. spec: §3.1, §3.4 (whole-pod scrub on the
		// occupancy-zero recycle edge).
		b.shutdownAdapter(ctx, result)
		return nil
	}

	// Retire path: tear the session's processes down, then drain the pod.
	b.shutdownAdapter(ctx, result)
	var sb lennyv1.Sandbox
	if err := b.Client.Get(ctx, client.ObjectKey{Namespace: b.Namespace, Name: result.SandboxName}, &sb); err != nil {
		return fmt.Errorf("podsession: get sandbox %s: %w", result.SandboxName, err)
	}
	return b.drain(ctx, &sb)
}

// shutdownAdapter shuts the pod's runtime down through the adapter and closes
// the connection. The Shutdown call is best-effort: on the recycle path it is
// the occupancy-zero signal that triggers the whole-pod scrub, and on the retire
// path it tears the session's processes down before the pod drains. A nil
// Adapter (a BindSlot result or a re-resolved release) is a no-op.
func (b *Binder) shutdownAdapter(ctx context.Context, result *BindResult) {
	if result.Adapter == nil {
		return
	}
	_, _ = result.Adapter.Shutdown(ctx, result.SessionID)
	result.Adapter.Close()
}

// resolveSandbox reads the claimed Sandbox and verifies it carries a pod
// address. The Sandbox reconciler records status.podIP once the pod is
// running, so a pod that was idle when claimed carries an address. The full
// object is returned so the caller can read its coarse §6.2 phase and pod
// address without a re-Get.
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
