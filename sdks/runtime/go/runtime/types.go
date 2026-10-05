// SPDX-License-Identifier: MIT

package runtime

import "encoding/json"

// This file holds the wire and convenience types the runtime-author SDK
// surfaces. The wire-level structs (MessagePart, MessageEnvelope, the
// inbound and outbound frame types) mirror the §28.5.3 adapter binary
// protocol. The convenience structs (CreateRequest, Message, Reply,
// CredentialBundle, AdapterManifest, WorkspacePlan) are §15.7 wrappers
// the SDK materializes from each session's session_start frame, the
// credential file that frame names, the pod-scoped manifest, and the
// stdin framing before invoking Handler methods. They introduce no new
// wire types.

// schemaVersion is the current MessagePart and MessageEnvelope schema
// revision (§28.5.3). Producers stamp it on every emitted MessagePart.
const schemaVersion = 1

// MessagePart is the §28.5.3 internal content model. A part either
// carries bytes inline or references blob storage; Inline and Ref are
// mutually exclusive. Basic-level runtimes need only set Type and
// Inline; the SDK stamps SchemaVersion when it is unset.
type MessagePart struct {
	// SchemaVersion identifies the MessagePart schema revision. Defaults
	// to 1. The SDK sets it to 1 on any emitted part that leaves it 0.
	SchemaVersion int `json:"schemaVersion,omitempty"`
	// ID is a stable part identifier. The adapter generates one when a
	// runtime omits it.
	ID string `json:"id,omitempty"`
	// Type is an open string from the §28.5.3 canonical type registry
	// (text, code, image, error, etc.) or an x-<vendor>/ custom type.
	Type string `json:"type"`
	// MimeType handles the encoding of the part content. Defaults to
	// text/plain for text parts.
	MimeType string `json:"mimeType,omitempty"`
	// Inline carries the part content directly (base64 for binary).
	Inline string `json:"inline,omitempty"`
	// Ref references external blob storage via a lenny-blob:// URI.
	Ref string `json:"ref,omitempty"`
	// Annotations is an open metadata map (role, language, final, etc.).
	Annotations map[string]any `json:"annotations,omitempty"`
	// Parts holds nested parts for compound outputs (execution_result).
	Parts []MessagePart `json:"parts,omitempty"`
	// Status is one of streaming, complete, or failed.
	Status string `json:"status,omitempty"`
}

// Text builds a minimal text MessagePart with SchemaVersion set.
func Text(s string) MessagePart {
	return MessagePart{SchemaVersion: schemaVersion, Type: "text", Inline: s}
}

// MessageEnvelope is the §15.4 unified inbound message format. The
// adapter populates From, and the gateway populates SchemaVersion and ID
// when omitted. Basic-level handlers typically read only Input.
//
// Annotations carries the §15.5 degradation-annotation
// catalog (`schema_version_ahead`, `durable_schema_version_ahead`,
// `mcp_protocol_version_retired`). Producers stamp them via the
// pkg/degradation helpers when forward-read or retirement defects
// occur. The field is open metadata so future annotations can land
// without a schema-version bump. F-15.5.5.
type MessageEnvelope struct {
	SchemaVersion   int          `json:"schemaVersion,omitempty"`
	Type            string       `json:"type"`
	ID              string       `json:"id"`
	From            *MessageFrom `json:"from,omitempty"`
	InReplyTo       string       `json:"inReplyTo,omitempty"`
	ThreadID        string       `json:"threadId,omitempty"`
	Delivery        string       `json:"delivery,omitempty"`
	DelegationDepth int          `json:"delegationDepth,omitempty"`
	// SessionID names the session this frame is addressed to. The adapter
	// populates it on every session-scoped frame on every pod, whatever
	// the pool's maxConcurrentSessions, and a runtime echoes it on the
	// frames it emits in response. From separately names the sending
	// session, which may be a different session. spec: §28.5.3.
	SessionID   string         `json:"sessionId,omitempty"`
	Input       []MessagePart  `json:"input,omitempty"`
	Annotations map[string]any `json:"annotations,omitempty"`
}

// MessageFrom is the §15.4 from object. Kind is one of client, agent,
// system, or external. The adapter injects both fields; runtimes never
// supply them.
type MessageFrom struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ResponseError is the optional §28.5.3 response.error object and the
// §8.8 TaskResult.error object. Both carry a code and a message.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// CredentialBundle is the parsed runtime credential file that a session's
// session_start names in credentialsPath, the session's own
// /run/lenny/slots/{sessionId}/credentials.json. The file lists one entry
// per credential provider the session holds a lease for. The SDK reloads
// the session's bundle on a credentials_rotated lifecycle event naming
// that session.
//
// spec: §4.7.11 (item 4, runtime credential file contract), §28.5.3
// (CH-MSGSOCK, Inbound: session_start).
type CredentialBundle struct {
	// Providers holds one entry per leased credential provider.
	Providers []ProviderCredential `json:"providers"`
}

// ProviderCredential is one entry of a CredentialBundle's providers list.
// MaterializedConfig is left undecoded because its fields depend on the
// provider and the delivery mode: a proxy entry carries proxyUrl and
// leaseToken, and a direct entry carries the provider's own credential
// fields.
//
// spec: §4.7.11 (item 4, runtime credential file contract), §4.9
// (materializedConfig schema by provider).
type ProviderCredential struct {
	// LeaseID identifies the §4.9 credential lease.
	LeaseID string `json:"leaseId"`
	// Provider is the credential provider identifier.
	Provider string `json:"provider"`
	// ExpiresAt is the ISO 8601 lease expiry timestamp.
	ExpiresAt string `json:"expiresAt,omitempty"`
	// DeliveryMode is direct or proxy.
	DeliveryMode string `json:"deliveryMode"`
	// MaterializedConfig is the entry's materializedConfig object, kept
	// as raw JSON.
	MaterializedConfig json.RawMessage `json:"materializedConfig,omitempty"`
}

// ExperimentContext is the experiment enrollment a session's session_start
// carries in experimentContext. Inherited is true when the enrollment was
// propagated from a parent session through delegation.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table), §10.7.
type ExperimentContext struct {
	ExperimentID string `json:"experimentId"`
	VariantID    string `json:"variantId"`
	Inherited    bool   `json:"inherited"`
}

// LLMConfig is the LLM provider configuration a session's session_start
// carries in llm. DeliveryMode is direct or proxy; Dialect names the
// provider dialect a proxy-mode runtime speaks to the LLM Proxy; APIKeyEnv
// names the variable a runtime's LLM client reads its key from, which the
// runtime sets for this session only and never in its own process
// environment; Headers lists the headers a proxy-mode runtime sends.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table), §4.9.
type LLMConfig struct {
	DeliveryMode string            `json:"deliveryMode"`
	Dialect      string            `json:"dialect,omitempty"`
	APIKeyEnv    string            `json:"apiKeyEnv,omitempty"`
	Headers      map[string]string `json:"headers,omitempty"`
}

// AdapterManifest is the parsed §4.7 adapter manifest written to
// /run/lenny/adapter-manifest.json before the runtime binary is
// spawned. It carries only pod-scoped fields: one runtime process serves
// every session the pod holds, so each session's own context arrives in
// that session's session_start frame rather than here. Unknown fields are
// ignored (§4.7 forward compatibility).
//
// spec: §4.7 (adapter manifest field reference), §4.7.10 (runtime process
// lifetime).
type AdapterManifest struct {
	// Version is the manifest schema version. Every increment is
	// breaking; the SDK rejects a version newer than it understands.
	Version int `json:"version,omitempty"`
	// MCPNonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
	// SDK injects it as params._lennyNonce on every MCP initialize.
	MCPNonce string `json:"mcpNonce,omitempty"`
	// PlatformMCPServer names the platform MCP server socket.
	PlatformMCPServer *MCPServerRef `json:"platformMcpServer,omitempty"`
	// ConnectorServers names the per-connector MCP server sockets.
	ConnectorServers []ConnectorServerRef `json:"connectorServers,omitempty"`
	// RuntimeOps names the Full-level CH-RUNTIMEOPS socket.
	RuntimeOps *SocketRef `json:"runtimeOps,omitempty"`
	// AdapterLocalTools enumerates the §28.5.3 adapter-local tools the
	// runtime may invoke via stdout tool_call frames.
	AdapterLocalTools []AdapterLocalTool `json:"adapterLocalTools,omitempty"`
	// RuntimeOptions is the effective caller options map.
	RuntimeOptions map[string]any `json:"runtimeOptions,omitempty"`
}

// MCPServerRef names a platform MCP server socket in the manifest.
type MCPServerRef struct {
	Socket string `json:"socket"`
}

// ConnectorServerRef names one connector MCP server in the manifest.
type ConnectorServerRef struct {
	ID     string `json:"id"`
	Socket string `json:"socket"`
}

// SocketRef names a single Unix socket in the manifest.
type SocketRef struct {
	Socket string `json:"socket"`
}

// AdapterLocalTool is one §28.5.3 adapter-local tool entry: a name, a
// human-readable description, and a JSON Schema for its arguments.
type AdapterLocalTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"inputSchema,omitempty"`
}

// WorkspacePlan is a reference to the §14 materialized workspace plan.
// The SDK parses it from the manifest when present; runtimes consult it
// for source metadata rather than to drive materialization.
type WorkspacePlan struct {
	SchemaVersion int            `json:"schemaVersion,omitempty"`
	Sources       []any          `json:"sources,omitempty"`
	SetupCommands []string       `json:"setupCommands,omitempty"`
	Extra         map[string]any `json:"-"`
}

// TerminationReason is the reason passed to Handler.OnTerminate. The SDK
// populates it from the session's session_end, from the §28.5.3 shutdown
// frame, or from the end of the connection.
type TerminationReason struct {
	// Reason is session_end when the session's session_end ended it, the
	// shutdown frame's reason (drain, deadline, etc.) when a shutdown
	// ended the process, or stdin_closed when the adapter closed the
	// connection without a shutdown frame.
	Reason string
	// DeadlineMS is the shutdown deadline in milliseconds when the
	// adapter supplied one; zero otherwise.
	DeadlineMS int
}

// CreateRequest is the §15.7 snapshot of session context handed to
// Handler.OnCreate when the session's session_start arrives and before the
// session's first Message is delivered. The SDK assembles it from the
// session_start frame, the credential file that frame names, and the
// pod-scoped adapter manifest. Handler implementations MUST treat it as
// read-only.
//
// spec: §15.7 (SDK Handler types), §28.5.3 (CH-MSGSOCK, Inbound:
// session_start).
type CreateRequest struct {
	// SessionID is the session this request opens, the session_start's
	// sessionId.
	SessionID string `json:"sessionId"`
	// TaskID is the session's external-protocol task identifier. Each
	// session has exactly one execution, so it equals SessionID; the SDK
	// derives it from the session_start's sessionId.
	// spec: §15.7 (TaskID derived from sessionId), §7.2 (one execution per session)
	TaskID string `json:"taskId"`
	// RuntimeOptions is the effective caller options map.
	RuntimeOptions map[string]any `json:"runtimeOptions,omitempty"`
	// WorkspacePlan references the §14 materialized workspace plan. Its
	// files are staged under /workspace/slots/{sessionId}/current before
	// OnCreate is invoked.
	// spec: §15.7 (WorkspacePlan staged before OnCreate), §6.4 (one pod filesystem layout)
	WorkspacePlan *WorkspacePlan `json:"workspacePlan,omitempty"`
	// Credentials is the session's credential bundle, read from the file
	// the session_start's credentialsPath names. Nil when the frame names
	// no credentialsPath. The SDK reloads the session's bundle on a
	// credentials_rotated event rather than re-invoking OnCreate.
	Credentials *CredentialBundle `json:"credentials,omitempty"`
	// ExperimentContext is the session_start's experimentContext; nil when
	// the session is not enrolled in an experiment.
	ExperimentContext *ExperimentContext `json:"experimentContext,omitempty"`
	// TracingContext is the session_start's tracingContext; nil for a
	// top-level session.
	TracingContext map[string]string `json:"tracingContext,omitempty"`
	// LLM is the session_start's llm; nil when the session has no active
	// LLM credential lease.
	LLM *LLMConfig `json:"llm,omitempty"`
	// ManifestSnapshot is the parsed pod-scoped adapter manifest.
	ManifestSnapshot *AdapterManifest `json:"manifestSnapshot,omitempty"`
}

// Message is the §15.7 per-turn envelope handed to Handler.OnMessage for
// every §28.5.3 message frame. Fields other than Envelope are SDK-derived
// conveniences.
type Message struct {
	// Envelope is the canonical §15.4 MessageEnvelope. All message
	// semantics live on this field.
	Envelope *MessageEnvelope `json:"envelope"`
	// SessionID is the session the message was delivered to, the frame's
	// sessionId. It equals the CreateRequest.SessionID of that session's
	// OnCreate.
	SessionID string `json:"sessionId"`
	// TaskID is the external-protocol task identifier of the session the
	// message belongs to. It equals SessionID and the CreateRequest.TaskID
	// of that session's OnCreate.
	// spec: §15.7 (Message), §7.2 (one execution per session)
	TaskID string `json:"taskId"`
	// Sequence is a monotonic, SDK-assigned per-session counter ordering
	// the session's messages as the SDK observed them on stdin. It is
	// local to this process and suitable for logging only.
	Sequence uint64 `json:"sequence"`
	// Metadata is an optional SDK-scoped pass-through map. It is not
	// forwarded on the wire.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Reply is the value Handler.OnMessage returns. The SDK serializes it
// into the stdout §28.5.3 response frame: Parts becomes output.
type Reply struct {
	// Parts is the MessagePart array the runtime emits for this turn.
	// Nil or empty is valid when output was already emitted via the
	// lenny/output platform MCP tool.
	Parts []MessagePart `json:"parts,omitempty"`
	// Error reports a structured failure for this turn. When set, the
	// adapter maps the task to failed and populates TaskResult.error.
	Error *ResponseError `json:"error,omitempty"`
	// Streaming indicates more Parts may still arrive out-of-band
	// before the turn is final.
	Streaming bool `json:"streaming,omitempty"`
	// Final marks this Reply as the terminal response for the turn.
	// Final MUST be true for Basic-level runtimes. The SDK treats a
	// zero-value Reply as Final for the common single-turn case.
	Final bool `json:"final,omitempty"`
}

// TextReply builds a Final Reply carrying a single text part.
func TextReply(s string) Reply {
	return Reply{Parts: []MessagePart{Text(s)}, Final: true}
}

// --- §28.5.3 wire frame types ------------------------------------------

// frameType peeks the type discriminator of an inbound frame.
type frameType struct {
	Type string `json:"type"`
}

// inboundMessage is the §28.5.3 inbound message frame. It is the
// MessageEnvelope with an explicit type discriminator.
type inboundMessage = MessageEnvelope

// inboundHeartbeat is the §28.5.3 heartbeat frame.
type inboundHeartbeat struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
}

// inboundSessionStart is the §28.5.3 session_start frame. An absent or
// null object member decodes to nil.
type inboundSessionStart struct {
	Type              string             `json:"type"`
	SessionID         string             `json:"sessionId"`
	StartID           string             `json:"startId"`
	CredentialsPath   string             `json:"credentialsPath,omitempty"`
	ExperimentContext *ExperimentContext `json:"experimentContext"`
	TracingContext    map[string]string  `json:"tracingContext"`
	LLM               *LLMConfig         `json:"llm"`
}

// inboundSessionEnd is the §28.5.3 session_end frame.
type inboundSessionEnd struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
}

// outboundSessionStarted is the §28.5.3 session_started frame. Error is
// set when the runtime failed to create the session's context.
type outboundSessionStarted struct {
	Type      string         `json:"type"`
	SessionID string         `json:"sessionId"`
	StartID   string         `json:"startId"`
	Error     *ResponseError `json:"error,omitempty"`
}

// inboundShutdown is the §28.5.3 shutdown frame.
type inboundShutdown struct {
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	DeadlineMS int    `json:"deadline_ms"`
}

// inboundToolResult is the §28.5.3 tool_result frame.
type inboundToolResult struct {
	Type      string        `json:"type"`
	ID        string        `json:"id"`
	Content   []MessagePart `json:"content"`
	IsError   bool          `json:"isError,omitempty"`
	SessionID string        `json:"sessionId,omitempty"`
}

// outboundResponse is the §28.5.3 outbound response frame.
type outboundResponse struct {
	Type      string         `json:"type"`
	Output    []MessagePart  `json:"output"`
	Error     *ResponseError `json:"error,omitempty"`
	SessionID string         `json:"sessionId,omitempty"`
}

// outboundHeartbeatAck is the §28.5.3 heartbeat_ack frame.
type outboundHeartbeatAck struct {
	Type string `json:"type"`
}

// outboundToolCall is the §28.5.3 tool_call frame.
type outboundToolCall struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	SessionID string         `json:"sessionId,omitempty"`
}

// outboundStatus is the §28.5.3 optional status frame. It is
// session-scoped, so it carries the session it is addressed to.
// spec: §28.5.3.
type outboundStatus struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId,omitempty"`
	State     string `json:"state,omitempty"`
	Message   string `json:"message,omitempty"`
}

// outboundTracingContext is the §28.5.3 set_tracing_context frame.
type outboundTracingContext struct {
	Type    string         `json:"type"`
	Context map[string]any `json:"context"`
}
