// SPDX-License-Identifier: MIT

package runtime

// This file holds the wire and convenience types the runtime-author SDK
// surfaces. The wire-level structs (OutputPart, MessageEnvelope, the
// inbound and outbound frame types) mirror the §15.4.1 adapter binary
// protocol. The convenience structs (CreateRequest, Message, Reply,
// CredentialBundle, AdapterManifest, WorkspacePlan) are §15.7 wrappers
// the SDK materializes from the manifest, the credential file, and the
// stdin framing before invoking Handler methods. They introduce no new
// wire types.

// schemaVersion is the current OutputPart and MessageEnvelope schema
// revision (§15.4.1). Producers stamp it on every emitted OutputPart.
const schemaVersion = 1

// OutputPart is the §15.4.1 internal content model. A part either
// carries bytes inline or references blob storage; Inline and Ref are
// mutually exclusive. Basic-level runtimes need only set Type and
// Inline; the SDK stamps SchemaVersion when it is unset.
type OutputPart struct {
	// SchemaVersion identifies the OutputPart schema revision. Defaults
	// to 1. The SDK sets it to 1 on any emitted part that leaves it 0.
	SchemaVersion int `json:"schemaVersion,omitempty"`
	// ID is a stable part identifier. The adapter generates one when a
	// runtime omits it.
	ID string `json:"id,omitempty"`
	// Type is an open string from the §15.4.1 canonical type registry
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
	Parts []OutputPart `json:"parts,omitempty"`
	// Status is one of streaming, complete, or failed.
	Status string `json:"status,omitempty"`
}

// Text builds a minimal text OutputPart with SchemaVersion set.
func Text(s string) OutputPart {
	return OutputPart{SchemaVersion: schemaVersion, Type: "text", Inline: s}
}

// MessageEnvelope is the §15.4.1 unified inbound message format. The
// adapter populates From, and the gateway populates SchemaVersion and ID
// when omitted. Basic-level handlers typically read only Input.
type MessageEnvelope struct {
	SchemaVersion   int          `json:"schemaVersion,omitempty"`
	Type            string       `json:"type"`
	ID              string       `json:"id"`
	From            *MessageFrom `json:"from,omitempty"`
	InReplyTo       string       `json:"inReplyTo,omitempty"`
	ThreadID        string       `json:"threadId,omitempty"`
	Delivery        string       `json:"delivery,omitempty"`
	DelegationDepth int          `json:"delegationDepth,omitempty"`
	SlotID          string       `json:"slotId,omitempty"`
	Input           []OutputPart `json:"input,omitempty"`
}

// MessageFrom is the §15.4.1 from object. Kind is one of client, agent,
// system, or external. The adapter injects both fields; runtimes never
// supply them.
type MessageFrom struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// ResponseError is the optional §15.4.1 response.error object and the
// §8.8 TaskResult.error object. Both carry a code and a message.
type ResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
}

// CredentialBundle is the parsed §4.7 runtime credential file written to
// /run/lenny/credentials.json. The SDK refreshes it in place on a
// credentials_rotated lifecycle message. Fields are the union of proxy
// and direct delivery modes; an empty field is absent in the file.
type CredentialBundle struct {
	// Mode is proxy or direct (§4.7 manifest llm fields).
	Mode string `json:"mode,omitempty"`
	// Provider names the upstream LLM provider for this lease.
	Provider string `json:"provider,omitempty"`
	// LeaseID identifies the §4.9 credential lease.
	LeaseID string `json:"leaseId,omitempty"`
	// APIKey is the upstream key under direct delivery.
	APIKey string `json:"apiKey,omitempty"`
	// APIKeyEnv names the environment variable carrying the key under
	// proxy delivery.
	APIKeyEnv string `json:"apiKeyEnv,omitempty"`
	// BaseURL is the upstream or proxy endpoint base URL.
	BaseURL string `json:"baseUrl,omitempty"`
	// ExpiresAt is the RFC 3339 lease expiry timestamp.
	ExpiresAt string `json:"expiresAt,omitempty"`
}

// AdapterManifest is the parsed §4.7 adapter manifest written to
// /run/lenny/adapter-manifest.json before the runtime binary is
// spawned. Unknown fields are ignored (§4.7 forward compatibility).
type AdapterManifest struct {
	// Version is the manifest schema version. Every increment is
	// breaking; the SDK rejects a version newer than it understands.
	Version int `json:"version,omitempty"`
	// SessionID is the session this runtime instance is bound to.
	SessionID string `json:"sessionId,omitempty"`
	// TaskID is the current task identifier.
	TaskID string `json:"taskId,omitempty"`
	// MCPNonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
	// SDK injects it as params._lennyNonce on every MCP initialize.
	MCPNonce string `json:"mcpNonce,omitempty"`
	// PlatformMCPServer names the platform MCP server socket.
	PlatformMCPServer *MCPServerRef `json:"platformMcpServer,omitempty"`
	// ConnectorServers names the per-connector MCP server sockets.
	ConnectorServers []ConnectorServerRef `json:"connectorServers,omitempty"`
	// LifecycleChannel names the Full-level lifecycle channel socket.
	LifecycleChannel *SocketRef `json:"lifecycleChannel,omitempty"`
	// AdapterLocalTools enumerates the §15.4.1 adapter-local tools the
	// runtime may invoke via stdout tool_call frames.
	AdapterLocalTools []AdapterLocalTool `json:"adapterLocalTools,omitempty"`
	// RuntimeOptions is the effective caller options map.
	RuntimeOptions map[string]any `json:"runtimeOptions,omitempty"`
	// TracingContext carries §16.3 tracing identifiers.
	TracingContext map[string]any `json:"tracingContext,omitempty"`
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

// AdapterLocalTool is one §15.4.1 adapter-local tool entry: a name, a
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
// populates it from the §15.4.1 shutdown frame or the lifecycle-channel
// terminate event.
type TerminationReason struct {
	// Reason is the adapter-supplied reason string (drain, deadline,
	// etc.) or stdin_closed when the adapter closed stdin without a
	// shutdown frame.
	Reason string
	// DeadlineMS is the shutdown deadline in milliseconds when the
	// adapter supplied one; zero otherwise.
	DeadlineMS int
}

// CreateRequest is the §15.7 snapshot of task-scoped context handed to
// Handler.OnCreate before the first Message. Handler implementations
// MUST treat it as read-only.
type CreateRequest struct {
	// SessionID is the session this runtime instance is bound to.
	SessionID string `json:"sessionId"`
	// TaskID is the current task identifier.
	TaskID string `json:"taskId"`
	// RuntimeOptions is the effective caller options map.
	RuntimeOptions map[string]any `json:"runtimeOptions,omitempty"`
	// WorkspacePlan references the §14 materialized workspace plan.
	WorkspacePlan *WorkspacePlan `json:"workspacePlan,omitempty"`
	// Credentials is the current §4.7 credential bundle. The SDK
	// refreshes it in place on rotation rather than re-invoking
	// OnCreate. Nil when the runtime has no active lease.
	Credentials *CredentialBundle `json:"credentials,omitempty"`
	// ManifestSnapshot is the parsed adapter manifest.
	ManifestSnapshot *AdapterManifest `json:"manifestSnapshot,omitempty"`
}

// Message is the §15.7 per-turn envelope handed to Handler.OnMessage for
// every §15.4.1 message frame. Fields other than Envelope are SDK-derived
// conveniences.
type Message struct {
	// Envelope is the canonical §15.4.1 MessageEnvelope. All message
	// semantics live on this field.
	Envelope *MessageEnvelope `json:"envelope"`
	// SessionID is the session the message was delivered to.
	SessionID string `json:"sessionId"`
	// TaskID is the active task the message belongs to.
	TaskID string `json:"taskId"`
	// Sequence is a monotonic, SDK-assigned per-task counter ordering
	// messages as the SDK observed them on stdin. It is local to this
	// process and suitable for logging only.
	Sequence uint64 `json:"sequence"`
	// Metadata is an optional SDK-scoped pass-through map. It is not
	// forwarded on the wire.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// Reply is the value Handler.OnMessage returns. The SDK serializes it
// into the stdout §15.4.1 response frame: Parts becomes output.
type Reply struct {
	// Parts is the OutputPart array the runtime emits for this turn.
	// Nil or empty is valid when output was already emitted via the
	// lenny/output platform MCP tool.
	Parts []OutputPart `json:"parts,omitempty"`
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
	return Reply{Parts: []OutputPart{Text(s)}, Final: true}
}

// --- §15.4.1 wire frame types ------------------------------------------

// frameType peeks the type discriminator of an inbound frame.
type frameType struct {
	Type string `json:"type"`
}

// inboundMessage is the §15.4.1 inbound message frame. It is the
// MessageEnvelope with an explicit type discriminator.
type inboundMessage = MessageEnvelope

// inboundHeartbeat is the §15.4.1 heartbeat frame.
type inboundHeartbeat struct {
	Type string `json:"type"`
	TS   int64  `json:"ts"`
}

// inboundShutdown is the §15.4.1 shutdown frame.
type inboundShutdown struct {
	Type       string `json:"type"`
	Reason     string `json:"reason"`
	DeadlineMS int    `json:"deadline_ms"`
}

// inboundToolResult is the §15.4.1 tool_result frame.
type inboundToolResult struct {
	Type    string       `json:"type"`
	ID      string       `json:"id"`
	Content []OutputPart `json:"content"`
	IsError bool         `json:"isError,omitempty"`
	SlotID  string       `json:"slotId,omitempty"`
}

// outboundResponse is the §15.4.1 outbound response frame.
type outboundResponse struct {
	Type   string         `json:"type"`
	Output []OutputPart   `json:"output"`
	Error  *ResponseError `json:"error,omitempty"`
	SlotID string         `json:"slotId,omitempty"`
}

// outboundHeartbeatAck is the §15.4.1 heartbeat_ack frame.
type outboundHeartbeatAck struct {
	Type string `json:"type"`
}

// outboundToolCall is the §15.4.1 tool_call frame.
type outboundToolCall struct {
	Type      string         `json:"type"`
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
	SlotID    string         `json:"slotId,omitempty"`
}

// outboundStatus is the §15.4.1 optional status frame.
type outboundStatus struct {
	Type    string `json:"type"`
	State   string `json:"state,omitempty"`
	Message string `json:"message,omitempty"`
}

// outboundTracingContext is the §15.4.1 set_tracing_context frame.
type outboundTracingContext struct {
	Type    string         `json:"type"`
	Context map[string]any `json:"context"`
}
