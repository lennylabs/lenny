// SPDX-License-Identifier: MIT

package podsession

import (
	"fmt"
	"time"

	"github.com/lennylabs/lenny/pkg/gateway/runtime/adapterclient"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

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
	// declared. spec: §5.1.
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
	// (symlinks rejected). spec: §7.4; §13.4 — F-7.4.4.
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
	// spec: §4.9.
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
	// (§6.1). An empty list disables demotion (§6.1).
	SDKWarmBlockingPaths []string
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag, resolved
	// by ResolvePool. When true and the session ends cleanly, Release patches
	// the per-pod claim bound → recycling and signals the adapter to run the
	// whole-pod scrub (the §5.2 recycle disposition) rather than draining the
	// pod; the adapter's ReportPodScrub then drives recycle vs. retire. A
	// failed/crashed session always retires regardless of this flag. spec:
	// §5.2 (recycle on occupancy-zero).
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
	// match. Meaningful only when PreConnect is true. spec: §6.1.
	Demoted bool
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2 whole-pod scrub
	// parameters resolved from the pool's sessionPolicy at bind time. Bind
	// carries them onto BindResult so the recycle-path Shutdown delivers them
	// to the adapter (the §4.7 recycle disposition) without re-resolving the
	// pool at the release boundary, matching how the Recycle flag is captured
	// once at bind. Empty/zero on a non-recycling pool. The scrub profile is
	// not carried on the wire; the gateway routes the §5.2 step-7 vm-restart
	// retire on the recycle policy in its own runtime store (C4). spec: §5.2
	// (whole-pod scrub trigger), §4.6.3.
	CleanupCommands       []string
	CleanupTimeoutSeconds int
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
	// Recycle is the pool's §5.2 sessionPolicy.recycle.enabled flag, carried
	// from the bind request so the release path can apply the §5.2 recycle
	// disposition without re-resolving the pool. On a recycling session-mode
	// pool a clean session release patches the claim bound → recycling and
	// signals the whole-pod scrub rather than draining the pod (Release). On a
	// recycling concurrent-session pool (the §5.2 "Concurrent" preset) the same
	// disposition runs when the last slot drains cleanly (ReleaseSlot →
	// SlotClaimer.ReleaseSlot). False for a non-recycling pool, where the pod
	// terminates after the session or cohort drains. spec: §5.2,
	// §6.2.
	Recycle bool
	// CleanupCommands and CleanupTimeoutSeconds are the §5.2 whole-pod scrub
	// parameters carried from the bind request so the recycle-path Shutdown
	// delivers them to the adapter without re-resolving the pool.
	// shutdownAdapter builds them into the §4.7 RecycleScrub sub-message (with
	// PodId == SandboxName) only on the recycle branch; the retire path sends a
	// plain Shutdown and ignores them. They are populated only on the
	// session-mode (exclusive) bind result; the concurrent-session bind result
	// leaves them empty because its occupancy-zero recycle trigger is a
	// follow-on. Empty/zero on a non-recycling pool. The scrub profile is not
	// carried on the wire; the gateway routes the §5.2 step-7 vm-restart retire
	// on the recycle policy in its own runtime store (C4). spec: §5.2
	// (whole-pod scrub trigger), §4.6.3.
	CleanupCommands       []string
	CleanupTimeoutSeconds int
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
	// caller republishes each as an SSE event so clients see the §7.4 per-entry notice.
	// Nil when materialization produced no warnings or when Bind
	// returned before FinalizeWorkspace ran. F-7.4.15.
	WorkspacePlanWarnings []*adapterv1.WorkspacePlanWarning
	// WorkspaceBase is the §6.4 workspace base the pod's adapter reported
	// on the §15.5 version handshake, verbatim. The session's §7.3 cwd is
	// `<base>/slots/{sessionId}/current`, derived once where the value is
	// persisted on the session row, so a subsequent Resume can pass that
	// root back via ResumeRequest.expected_workspace_root for the adapter
	// to assert "same absolute cwd path" before extracting any checkpoint
	// bytes. Empty when the adapter reported no base. F-7.3.15.
	WorkspaceBase string
	// SetupOutputs carries the §7.5 captured stdout/stderr/exit
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
// counter. spec: §7.5, §7.3 — F-7.5.4 / F-7.5.9.
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
// Bind measured, so the caller can attribute the §6.3 latency
// budget. The end-to-end pod-warm SLO (§6.3,
// lenny_session_startup_duration_seconds) is the sum of PodClaim,
// CredentialAssignment, and AgentSessionStart; it excludes
// WorkspaceMaterialization (payload-dependent) and SetupCommands
// (deployer-controlled), both of which §6.3 keeps out of the
// platform-controlled pod-warm budget. spec: §6.3.
type BindTimings struct {
	// PodClaim is the §6.3 "pod claim and routing" phase: the warm-pod
	// claim, pod-IP resolution, mTLS dial, and version handshake.
	PodClaim time.Duration
	// WorkspaceMaterialization is the staging plus FinalizeWorkspace
	// phase. Excluded from the pod-warm SLO total per §6.3.
	WorkspaceMaterialization time.Duration
	// SetupCommands is the RunSetup phase. Deployer-controlled and
	// excluded from the platform pod-warm budget (§6.3), but
	// instrumented per the §6.3 per-phase requirement.
	SetupCommands time.Duration
	// CredentialAssignment is the §4.9 lease mint plus AssignCredentials
	// RPC phase.
	CredentialAssignment time.Duration
	// AgentSessionStart is the StartSession RPC phase, after which the
	// session is ready.
	AgentSessionStart time.Duration
}
