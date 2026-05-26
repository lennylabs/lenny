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
	// StagingDir is the directory PrepareWorkspace streams uploaded
	// files into before FinalizeWorkspace materializes them — the pod's
	// workspace staging area. Empty leaves PrepareWorkspace returning
	// FailedPrecondition.
	StagingDir string
	// CredentialsDir is the directory the credential RPCs materialize
	// the §4.7 credential file into — the pod's /run/lenny.
	CredentialsDir string
	// ManifestDir is the directory StartSession writes the §15.4
	// adapter-manifest.json into — the pod's /run/lenny. Empty disables
	// manifest writing.
	ManifestDir string
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
	// LeaseExtender is the §8.6 adapter→gateway lease-extension client.
	// When set, HandleBudgetExhaustion uses it to request additional
	// token budget after the LLM proxy rejects a call for budget
	// exhaustion. Nil leaves the §8.6 trigger path returning
	// ErrLeaseExtenderUnset.
	LeaseExtender LeaseExtender

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

	// ops serializes the Checkpoint and Interrupt RPCs per §4.7.
	ops opLock

	// controlMu guards controlSink.
	controlMu sync.Mutex
	// controlSink is the queue feeding the active §4.7 adapter→gateway
	// LifecycleChannel stream. Non-nil only while a gateway stream is
	// attached; emitControlEvent drops events when it is nil.
	controlSink chan controlEvent

	// mu guards sessionID, mcpCancel, and the credential fields.
	mu sync.Mutex
	// sessionID is the session currently assigned to the pod, empty
	// when the pod is idle. Per §6.1 a session-mode pod is
	// one-session-only.
	sessionID string
	// mcpCancel stops the platform MCP server started for the current
	// session. Nil when no platform MCP server is running.
	mcpCancel context.CancelFunc
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
		Capabilities:   s.Capabilities,
		AdapterVersion: s.Version,
	}
	selected := highestCommonVersion(req.GetAcceptedProtocolVersions(), s.ProtocolVersions)
	if selected == "" {
		resp.Incompatible = true
		return resp, nil
	}
	resp.SelectedProtocolVersion = selected
	return resp, nil
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
