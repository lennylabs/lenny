// SPDX-License-Identifier: MIT

// Package adapter implements the §4.7 runtime adapter: the gRPC server
// that runs as a sidecar in every Lenny agent pod and bridges the
// gateway to the pod's runtime binary.
//
// Server implements the generated adapterv1.AdapterServer contract. It
// embeds UnimplementedAdapterServer, so RPCs that are not yet built
// return codes.Unimplemented rather than breaking the build as the
// contract grows. This file carries the version-negotiation handshake;
// the workspace, session, credential, and lifecycle RPCs are
// implemented in later increments.
package adapter

import (
	"context"
	"sync"
	"time"

	"github.com/lennylabs/lenny/pkg/adapter/scrub"
	"github.com/lennylabs/lenny/pkg/adapter/sharedassets"
	adapterv1 "github.com/lennylabs/lenny/pkg/proto/adapter/v1"
)

// ProtocolVersionV1 is the adapter↔gateway protocol version for the v1
// contract (§15.5).
const ProtocolVersionV1 = "1.0.0"

// RuntimeKind selects which §5.1 runtime type the adapter drives. It
// picks the adapter→runtime path: a type: agent runtime is driven over
// the §15.4.1 JSONL stdin/stdout protocol; a type: mcp runtime's agent
// is itself an MCP server, driven by the adapter as an MCP client
// (§9.1). The Server's Runtime field is the RuntimeProcess for the
// selected kind in both cases — only the implementation differs.
type RuntimeKind string

const (
	// RuntimeKindAgent is the §5.1 type: agent runtime: the adapter
	// drives an agent binary over the JSONL stdin/stdout protocol. It is
	// the default when RuntimeKind is unset.
	RuntimeKindAgent RuntimeKind = "agent"
	// RuntimeKindMCP is the §5.1 type: mcp runtime: the agent process is
	// an MCP server and the adapter drives it as an MCP client. Wire the
	// Runtime field with an *MCPRuntime when this kind is selected.
	RuntimeKindMCP RuntimeKind = "mcp"
)

// Server implements adapterv1.AdapterServer. It embeds the generated
// UnimplementedAdapterServer for forward compatibility.
type Server struct {
	adapterv1.UnimplementedAdapterServer

	// ProtocolVersions are the adapter↔gateway protocol versions this
	// adapter speaks. NegotiateVersion selects from this set.
	ProtocolVersions []string
	// Capabilities are the capability tokens the adapter advertises
	// during negotiation, for example preConnect or fullLifecycle.
	// They start empty and grow as the adapter implements the
	// corresponding features.
	Capabilities []string
	// Version is the adapter build version, surfaced for observability.
	Version string

	// WorkspaceRoot is the directory StartSession materializes the
	// session workspace into — the pod's /workspace/current.
	WorkspaceRoot string
	// WorkspaceBase is the §6.4 base the per-slot `slots/{slotId}` trees
	// nest under (production /workspace), the parent of the single
	// WorkspaceRoot. A slotId-bearing frame routes to the per-slot path,
	// so cmd/lenny-adapter sets this unconditionally. Empty disables
	// per-slot workspace materialization.
	WorkspaceBase string
	// ArtifactsRoot is the §6.4 base the per-slot `/artifacts/{slotId}`
	// trees nest under (production /artifacts). Empty omits the per-slot
	// artifacts directory.
	ArtifactsRoot string
	// SessionsRoot is the §6.4 line 380 /sessions tmpfs the runtime
	// writes its session file into (conversation logs, native SDK
	// session state). When set, Checkpoint bundles it into the workspace
	// archive and Resume restores it to its expected path, satisfying the
	// §7.3 line 409 step (f) "restore session file to expected path".
	// Empty (dev mode, an in-process executor with no /sessions mount)
	// leaves the session file out of the checkpoint and the resume a
	// workspace-only restore.
	SessionsRoot string
	// StagingDir is the directory PrepareWorkspace streams uploaded
	// files into before FinalizeWorkspace materializes them — the pod's
	// workspace staging area. Empty leaves PrepareWorkspace returning
	// FailedPrecondition.
	StagingDir string
	// SharedAssetsDir is the §6.4 /workspace/shared directory the adapter
	// materializes read-only shared assets into at warm time, before the
	// pod is claimed. The adapter mounts it read-write; the runtime
	// container mounts it read-only. Empty skips the populate step (the
	// volume is still mounted read-only and empty by the pod spec).
	SharedAssetsDir string
	// SharedAssets is the §6.4 line 409 inline shared-asset set
	// EnsureWarmWorkspaceLayout writes into SharedAssetsDir. Nil or empty
	// leaves /workspace/shared empty. spec: §6.4 line 409 — F-6.4.3.
	SharedAssets []sharedassets.FileSpec
	// CredentialsDir is the directory the credential RPCs materialize
	// the §4.7 credential file into — the pod's /run/lenny.
	CredentialsDir string
	// ManifestDir is the directory StartSession writes the §15.4
	// adapter-manifest.json into — the pod's /run/lenny. Empty disables
	// manifest writing.
	ManifestDir string
	// OTLPEndpoint is the §4.7 / §16.3 OTLP collector URL written into the
	// manifest's observability.otlpEndpoint so an OTel-emitting runtime can
	// configure its SDK. Empty omits the observability manifest object.
	OTLPEndpoint string
	// OTLPTLSDisabled records the §13.2 (NET-059) dev/make-run escape hatch:
	// when set, the manifest carries observability.otlpTlsEnabled: false so
	// the runtime accepts an http:// collector endpoint. Production leaves
	// it false and uses an https:// endpoint.
	OTLPTLSDisabled bool
	// MCPSocket is the Unix socket address the §4.7 platform MCP server
	// listens on for the session — the pod's abstract socket
	// @lenny-platform-mcp in production. Empty disables the platform
	// MCP server. A manifest directory must also be configured, since
	// the runtime reads the authenticating nonce from the manifest.
	MCPSocket string
	// RuntimeUID is the Unix UID the agent runtime process runs as.
	// When non-zero the platform MCP server applies the §4.7 / §13
	// SO_PEERCRED peer-credential check, rejecting any connection from
	// a process running as a different UID. Zero disables the check.
	RuntimeUID uint32
	// NonceOnlyMode reports that SO_PEERCRED is disabled
	// (--require-so-peercred=false), so the manifest nonce alone is the
	// intra-pod MCP authentication boundary. When set, the platform MCP
	// server supplements the static nonce with the §4.7 per-connection
	// challenge-response (lines 879-883), keeping forward security on each
	// new connection.
	NonceOnlyMode bool
	// PlatformForwarder forwards the §9.1 platform tool calls a type:agent
	// runtime makes against the intra-pod platform MCP server to the
	// gateway over GatewayControl. When set, startPlatformMCP wires it as
	// the MCP server's tool Provider so the server advertises the
	// gateway's platform tool catalog on tools/list and dispatches every
	// tools/call to the gateway. Nil leaves the platform MCP server
	// serving an empty catalog (the dev path with no gateway link).
	// *gatewaycontrol.Client satisfies it. spec: §9.1 lines 14-31. F-9.1.1.
	PlatformForwarder PlatformToolForwarder
	// ConnectorForwarder forwards the §9.3 per-connector tool calls a
	// type:agent runtime makes against the intra-pod @lenny-connector-<id>
	// MCP servers to the gateway over GatewayControl. When set, StartSession
	// resolves the session's permitted connectors (ListSessionConnectors),
	// lists them in the manifest connectorServers array, and opens one MCP
	// server per connector whose tools/list and tools/call forward to the
	// gateway. Nil leaves the manifest connectorServers array empty (the dev
	// path with no gateway link). *gatewaycontrol.Client satisfies it.
	// spec: §9.3 lines 142-164. F-9.1.2.
	ConnectorForwarder ConnectorToolForwarder
	// PodScrubReporter emits the §5.2 whole-pod scrub outcome to the gateway
	// on the recycle boundary via ReportPodScrub. ConnectGateway wires it to
	// the dialed *gatewaycontrol.Client alongside the platform and connector
	// forwarders; the recycle-scrub driver reports through it. Nil (the dev
	// path with no gateway link) leaves the driver unable to report, so the
	// gateway missing-report timeout retires the pod.
	// spec: §4.7 (ReportPodScrub); §5.2. F-5.2.15.
	PodScrubReporter PodScrubReporter
	// ScrubOps runs the §5.2 whole-pod scrub host operations at the recycle
	// boundary. cmd/lenny-adapter wires it to a scrub.DefaultOps built from
	// the pod's credential and workspace paths; tests inject a fake. Nil (a
	// wiring gap rather than a supported mode) makes the recycle-scrub driver
	// withhold the report fail-closed, so the gateway missing-report timeout
	// retires the pod rather than reusing it without a scrub having run.
	// spec: §5.2 (whole-pod scrub).
	ScrubOps scrub.Ops
	// VMRestarter performs §5.2 scrub step 7 (guest VM restart) for a
	// vm-restart pool. Nil until the concrete Kata client lands: the driver
	// then withholds the report on a vm-restart profile (fail-closed) so the
	// gateway missing-report timeout retires the pod rather than reusing it.
	// spec: §5.2 step 7.
	VMRestarter scrub.VMRestarter
	// scrubDone is a test-only seam the recycle-scrub driver fires once its
	// background goroutine returns, so a test can wait for the async scrub
	// deterministically without a sleep. Nil in production. spec: §5.2 (async
	// scrub report).
	scrubDone func()
	// RuntimeKind selects the §5.1 runtime type the adapter drives. The
	// zero value is RuntimeKindAgent: the adapter drives an agent binary
	// over the §15.4.1 JSONL stdin/stdout protocol. RuntimeKindMCP
	// selects the type: mcp path, where the agent is an MCP server the
	// adapter drives as an MCP client; wire Runtime with an *MCPRuntime.
	RuntimeKind RuntimeKind
	// Runtime manages the pod's runtime process. StartSession starts it
	// once the workspace is prepared.
	Runtime RuntimeProcess
	// Checkpoints stores the §4.4 workspace checkpoints the Checkpoint
	// RPC produces. Nil leaves Checkpoint Unimplemented.
	Checkpoints CheckpointSink
	// CheckpointAborter cleans up partially-uploaded MinIO objects when
	// a checkpoint is aborted per §4.4 line 248. Nil leaves the abort
	// path a no-op — the dev-mode adapter relies on `lenny-ctl` /
	// admin tooling for storage cleanup.
	CheckpointAborter CheckpointAborter
	// CheckpointMetrics receives the §4.4 lines 248 / 254 counter
	// increments (`lenny_checkpoint_orphaned_objects_total` on abort
	// cleanup failure; `lenny_checkpoint_size_exceeded_total` on
	// pre-checkpoint workspace-size probe exceed). Nil makes every
	// metric call a no-op.
	CheckpointMetrics CheckpointMetrics
	// CheckpointEvents publishes the §4.4 line 254 `checkpoint.skipped`
	// session event when the pre-checkpoint workspace-size probe
	// rejects the run. Nil makes the emission a no-op.
	CheckpointEvents CheckpointEventEmitter
	// CheckpointPoolLabel is the pool label stamped onto the §4.4
	// metric increments (e.g., `claude-code-pool`). Empty omits the
	// label per metric-emit advisory.
	CheckpointPoolLabel string
	// CheckpointLevelLabel is the runtime-integration-level label
	// stamped onto `lenny_checkpoint_size_exceeded_total` per §4.4
	// line 254 (one of `basic`, `standard`, `full`, `embedded`).
	// Empty omits the label.
	CheckpointLevelLabel string
	// CheckpointTriggerLabel is the trigger label stamped onto
	// `lenny_checkpoint_orphaned_objects_total` per §4.4 line 248
	// (one of `periodic`, `pre_scale_down`, `eviction`). Empty omits
	// the label.
	CheckpointTriggerLabel string
	// WorkspaceSize returns the on-disk byte count of the session's
	// workspace root. Nil disables the §4.4 line 254 pre-checkpoint
	// workspace-size probe — the kubelet `emptyDir.sizeLimit` is the
	// backstop. Production wires this against
	// `pkg/adapter/workspace.Size`.
	WorkspaceSize WorkspaceSizeFunc
	// WorkspaceSizeLimitBytes is the §4.4 line 254
	// `workspaceSizeLimitBytes` per-pool cap. Zero or negative
	// disables the probe; production sets this to the pool's
	// configured limit (default 512Mi).
	WorkspaceSizeLimitBytes int64
	// Restorer loads the §4.4 workspace checkpoints the Resume RPC
	// restores from. Nil leaves Resume Unimplemented.
	Restorer CheckpointSource
	// Usage reports the session's token and wall-clock accounting the
	// ReportUsage RPC returns. Nil leaves ReportUsage Unimplemented.
	Usage UsageMeter
	// Lifecycle is the §15.4.6 runtime lifecycle channel. When set, the
	// adapter advertises its Unix socket in the session manifest so a
	// Full-level runtime can connect for checkpoint, interrupt,
	// credential-rotation, and deadline signals. Nil leaves the adapter
	// Basic-level, with no lifecycle channel.
	Lifecycle *LifecycleChannel

	// RuntimeName is the §5.1 runtime name stamped onto the
	// `lenny_credential_rotation_timeout_total` metric's runtime label.
	// Empty leaves the label empty.
	RuntimeName string
	// RotationInflightCeiling overrides the §4.7 line 822 fault/revocation
	// in-flight gate ceiling (default 300s). The renewal trigger ignores
	// it and waits unbounded. Zero selects the spec default. Operator
	// override is for test and latency tuning; production keeps the spec
	// value.
	RotationInflightCeiling time.Duration
	// CredentialsAckTimeout overrides the §4.7 line 824 60s
	// credentials_acknowledged timeout. Zero selects the spec default.
	CredentialsAckTimeout time.Duration
	// RotationAudit emits the §4.7 line 822 / §4.9.2
	// credential.rotation_ceiling_hit audit event when the in-flight gate
	// hits the ceiling. Nil makes the emission a no-op (the dev-mode
	// adapter has no EventStore path).
	RotationAudit RotationAuditEmitter

	// ExpiryAfterFunc and ExpiryNow are the §4.9 line 1149 expiry-timer
	// test seams. Nil selects time.AfterFunc and time.Now; tests inject
	// fakes to fire a lease expiry deterministically.
	ExpiryAfterFunc func(time.Duration, func()) expiryTimerHandle
	ExpiryNow       func() time.Time

	// CoordinatorHoldTimeout overrides the §10.1 line 50
	// coordinatorHoldTimeoutSeconds default (120s): the window the adapter
	// holds a session after losing its coordinator before it
	// self-terminates. Zero selects the spec default.
	CoordinatorHoldTimeout time.Duration
	// HoldAfterFunc is the §10.1 hold-timer test seam. Nil selects
	// time.AfterFunc; tests inject a fake to fire the hold timeout
	// deterministically.
	HoldAfterFunc func(time.Duration, func()) expiryTimerHandle
	// PostMortemDir is the pod-local directory the §10.1 line 50 hold
	// timeout writes a coordinator_lost post-mortem record into when no
	// new coordinator returns and the gateway control stream is gone.
	// Empty skips the disk write (the dev path); the AdapterTerminating
	// control event and the gateway orphan-session reconciler remain the
	// primary terminal-notification surfaces.
	PostMortemDir string

	// HeartbeatInterval is the §15.4.1 line 1442 cadence at which the
	// Attach loop sends a `{type:heartbeat,ts}` liveness ping to the
	// runtime. Zero (the default) disables heartbeats — the in-process
	// dev executor and tests that construct a bare Server send none.
	// cmd/lenny-adapter sets it for the production sidecar so a hung
	// runtime is detected by liveness probe, not only by stream close.
	// spec: §15.4.1 lines 1442, 1826, 2061.
	HeartbeatInterval time.Duration
	// HeartbeatAckTimeout is the §15.4.1 line 1826 window the runtime has
	// to answer a heartbeat with `heartbeat_ack`. When the window elapses
	// with no ack the adapter considers the process hung and sends SIGTERM
	// (RuntimeProcess.Interrupt with hard=false). Zero selects the spec
	// default (10s) whenever HeartbeatInterval > 0.
	HeartbeatAckTimeout time.Duration

	// ops serializes the Checkpoint and Interrupt RPCs per §4.7.
	ops opLock

	// coord holds the §10.1 coordinator generation gate the
	// CoordinatorFence RPC installs and the CheckpointBarrier RPC
	// validates. See pkg/adapter/coordination.go.
	coord coordinationState

	// hold tracks the §10.1 coordinator-loss hold state the adapter
	// enters when the gateway control stream drops while a session is
	// live. See pkg/adapter/holdstate.go.
	hold holdState

	// controlMu guards controlSink.
	controlMu sync.Mutex
	// controlSink is the queue feeding the active §4.7 adapter→gateway
	// LifecycleChannel stream. Non-nil only while a gateway stream is
	// attached; emitControlEvent drops events when it is nil.
	controlSink chan controlEvent

	// mu guards sessionID, mcpCancel, sdkConnected, and the credential
	// fields.
	mu sync.Mutex
	// sdkConnected records that the §6.1 SDK-warm pre-connect has
	// completed for a preConnect runtime. It is set by PreConnect at warm
	// time and cleared by DemoteSDK; SDKWarmReady reads it so a readiness
	// probe can hold the pod un-claimable until the SDK is connected.
	sdkConnected bool
	// mcpHandshakeSeen records that the runtime completed a §15.4.3
	// nonce-authenticated initialize handshake against the platform MCP
	// server during the current session. The §5.1 observed-integration
	// -level probe reads it: a runtime that connected to MCP is at least
	// Standard. Cleared by releaseSession. F-5.1.11.
	mcpHandshakeSeen bool
	// sessionID is the session currently assigned to the pod, empty
	// when the pod is idle. Per §6.1 a session-mode pod is
	// one-session-only.
	sessionID string
	// mcpCancel stops the platform MCP server started for the current
	// session. Nil when no platform MCP server is running.
	mcpCancel context.CancelFunc
	// connectorCancels stops the §9.3 per-connector MCP servers started
	// for the current session, one cancel per connector socket. Reset to
	// nil by releaseSession after every server is stopped. F-9.1.2.
	connectorCancels []context.CancelFunc
	// credSessionID is the session the current credential leases were
	// assigned for, empty when none are assigned.
	credSessionID string
	// credLeases is the credential lease set materialized into the
	// credential file, keyed by provider.
	credLeases map[string]*adapterv1.CredentialLease
	// expiryTimers holds the §4.9 line 1149 direct-mode lease-expiry
	// timers, keyed by provider. Each fires at its lease's expiresAt to
	// delete the provider's credential-file entry and report
	// AUTH_EXPIRED unless a replacement lease arrived first.
	expiryTimers map[string]*expiryTimer

	// slots holds the §6.4 concurrent-workspace per-slot state, keyed by
	// slotId. Populated when a slot bind assigns a slot. Each slot owns its
	// own workspace tree, state, and credential lease set; one pod-global
	// runtime serves every slot, multiplexed on slotId over the single
	// runtime connection. Guarded by mu. spec: §6.4 lines 385-409.
	slots map[string]*slotState
}

// New returns a Server advertising the given build version and the v1
// protocol contract. Capabilities start empty.
func New(version string) *Server {
	return &Server{
		ProtocolVersions: []string{ProtocolVersionV1},
		Version:          version,
	}
}

// NegotiateVersion performs the §4.7 and §15.5 gateway↔adapter
// handshake. It selects the gateway's highest-preference protocol
// version that the adapter also speaks. When the two sets do not
// overlap the response is marked incompatible, and the gateway tears
// the connection down and evicts the pod.
func (s *Server) NegotiateVersion(_ context.Context, req *adapterv1.NegotiateVersionRequest) (*adapterv1.NegotiateVersionResponse, error) {
	resp := &adapterv1.NegotiateVersionResponse{
		Capabilities:   s.advertisedCapabilities(),
		AdapterVersion: s.Version,
		// spec: §7.3 line 408 — surface the absolute cwd path the adapter
		// mounts the workspace into so the gateway can persist it for the
		// "same absolute cwd path" assertion on Resume. F-7.3.15.
		WorkspaceRoot: s.WorkspaceRoot,
	}
	selected := highestCommonVersion(req.GetAcceptedProtocolVersions(), s.ProtocolVersions)
	if selected == "" {
		resp.Incompatible = true
		return resp, nil
	}
	resp.SelectedProtocolVersion = selected
	return resp, nil
}

// advertisedCapabilities returns the capability tokens the adapter
// declares during negotiation. It is the configured Capabilities set plus
// the §4.7 preConnect token when the wired runtime supports the SDK-warm
// fast path (SDKWarmRuntime), so the gateway drives the pod through
// ConfigureWorkspace rather than StartSession.
func (s *Server) advertisedCapabilities() []string {
	caps := s.Capabilities
	if _, ok := s.sdkWarmRuntime(); !ok {
		return caps
	}
	if containsCapability(caps, CapabilityPreConnect) {
		return caps
	}
	out := make([]string, 0, len(caps)+1)
	out = append(out, caps...)
	return append(out, CapabilityPreConnect)
}

// containsCapability reports whether the capability token c is in caps.
func containsCapability(caps []string, c string) bool {
	for _, v := range caps {
		if v == c {
			return true
		}
	}
	return false
}

// highestCommonVersion returns the first entry of gatewayAccepted that
// also appears in adapterSupported. The gateway orders its accepted
// list highest-preference first (§4.7), so the first match is the
// highest mutually-supported version. An empty string means the sets
// do not overlap.
func highestCommonVersion(gatewayAccepted, adapterSupported []string) string {
	supported := make(map[string]bool, len(adapterSupported))
	for _, v := range adapterSupported {
		supported[v] = true
	}
	for _, v := range gatewayAccepted {
		if supported[v] {
			return v
		}
	}
	return ""
}
