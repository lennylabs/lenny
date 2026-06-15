// SPDX-License-Identifier: MIT

// This file holds the wire and convenience types the runtime-author SDK
// surfaces. The wire-level types (OutputPart, MessageEnvelope, the
// inbound and outbound frame types) mirror the §15.4.1 adapter binary
// protocol. The convenience types (CreateRequest, Message, Reply,
// CredentialBundle, AdapterManifest, WorkspacePlan) are §15.7 wrappers
// the SDK materializes from the manifest, the credential file, and the
// stdin framing before invoking Handler methods. They introduce no new
// wire types.

// SCHEMA_VERSION is the current OutputPart and MessageEnvelope schema
// revision (§15.4.1). Producers stamp it on every emitted OutputPart.
export const SCHEMA_VERSION = 1;

// OutputPart is the §15.4.1 internal content model. A part either
// carries bytes inline or references blob storage; inline and ref are
// mutually exclusive. Basic-level runtimes need only set type and
// inline; the SDK stamps schemaVersion when it is unset.
export interface OutputPart {
  // schemaVersion identifies the OutputPart schema revision. Defaults
  // to 1. The SDK sets it to 1 on any emitted part that leaves it
  // undefined.
  schemaVersion?: number;
  // id is a stable part identifier. The adapter generates one when a
  // runtime omits it.
  id?: string;
  // type is an open string from the §15.4.1 canonical type registry
  // (text, code, image, error, etc.) or an x-<vendor>/ custom type.
  type: string;
  // mimeType handles the encoding of the part content. Defaults to
  // text/plain for text parts.
  mimeType?: string;
  // inline carries the part content directly (base64 for binary).
  inline?: string;
  // ref references external blob storage via a lenny-blob:// URI.
  ref?: string;
  // annotations is an open metadata map (role, language, final, etc.).
  annotations?: Record<string, unknown>;
  // parts holds nested parts for compound outputs (execution_result).
  parts?: OutputPart[];
  // status is one of streaming, complete, or failed.
  status?: string;
}

// text builds a minimal text OutputPart with schemaVersion set.
export function text(s: string): OutputPart {
  return { schemaVersion: SCHEMA_VERSION, type: "text", inline: s };
}

// MessageFrom is the §15.4.1 from object. kind is one of client, agent,
// system, or external. The adapter injects both fields; runtimes never
// supply them.
export interface MessageFrom {
  kind: string;
  id: string;
}

// MessageEnvelope is the §15.4.1 unified inbound message format. The
// adapter populates from, and the gateway populates schemaVersion and
// id when omitted. Basic-level handlers typically read only input.
export interface MessageEnvelope {
  schemaVersion?: number;
  type: string;
  id: string;
  from?: MessageFrom;
  inReplyTo?: string;
  threadId?: string;
  delivery?: string;
  delegationDepth?: number;
  slotId?: string;
  input?: OutputPart[];
}

// ResponseError is the optional §15.4.1 response.error object and the
// §8.8 TaskResult.error object. Both carry a code and a message.
export interface ResponseError {
  code: string;
  message?: string;
}

// CredentialBundle is the parsed §4.7 runtime credential file written
// to /run/lenny/credentials.json. The SDK refreshes it in place on a
// credentials_rotated lifecycle message. Fields are the union of proxy
// and direct delivery modes; an absent field is missing in the file.
export interface CredentialBundle {
  // mode is proxy or direct (§4.7 manifest llm fields).
  mode?: string;
  // provider names the upstream LLM provider for this lease.
  provider?: string;
  // leaseId identifies the §4.9 credential lease.
  leaseId?: string;
  // apiKey is the upstream key under direct delivery.
  apiKey?: string;
  // apiKeyEnv names the environment variable carrying the key under
  // proxy delivery.
  apiKeyEnv?: string;
  // baseUrl is the upstream or proxy endpoint base URL.
  baseUrl?: string;
  // expiresAt is the RFC 3339 lease expiry timestamp.
  expiresAt?: string;
}

// MCPServerRef names a platform MCP server socket in the manifest.
export interface MCPServerRef {
  socket: string;
}

// ConnectorServerRef names one connector MCP server in the manifest.
export interface ConnectorServerRef {
  id: string;
  socket: string;
}

// SocketRef names a single Unix socket in the manifest.
export interface SocketRef {
  socket: string;
}

// AdapterLocalTool is one §15.4.1 adapter-local tool entry: a name, a
// human-readable description, and a JSON Schema for its arguments.
export interface AdapterLocalTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
}

// AdapterManifest is the parsed §4.7 adapter manifest written to
// /run/lenny/adapter-manifest.json before the runtime binary is
// spawned. Unknown fields are ignored (§4.7 forward compatibility).
export interface AdapterManifest {
  // version is the manifest schema version. Every increment is
  // breaking; the SDK rejects a version newer than it understands.
  version?: number;
  // sessionId is the session this runtime instance is bound to.
  sessionId?: string;
  // taskId is the root task identifier for this session. Each session
  // has exactly one execution, so the manifest is per-session and taskId
  // is frozen for the session's lifetime.
  // spec: §15.7 (manifest TaskID), §7.1 (one execution per session)
  taskId?: string;
  // mcpNonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
  // SDK injects it as params._lennyNonce on every MCP initialize.
  mcpNonce?: string;
  // platformMcpServer names the platform MCP server socket.
  platformMcpServer?: MCPServerRef;
  // connectorServers names the per-connector MCP server sockets.
  connectorServers?: ConnectorServerRef[];
  // lifecycleChannel names the Full-level lifecycle channel socket.
  lifecycleChannel?: SocketRef;
  // adapterLocalTools enumerates the §15.4.1 adapter-local tools the
  // runtime may invoke via stdout tool_call frames.
  adapterLocalTools?: AdapterLocalTool[];
  // runtimeOptions is the effective caller options map.
  runtimeOptions?: Record<string, unknown>;
  // tracingContext carries §16.3 tracing identifiers.
  tracingContext?: Record<string, unknown>;
}

// WorkspacePlan is a reference to the §14 materialized workspace plan.
// The SDK parses it from the manifest when present; runtimes consult it
// for source metadata rather than to drive materialization.
export interface WorkspacePlan {
  schemaVersion?: number;
  sources?: unknown[];
  setupCommands?: string[];
}

// TerminationReason is the reason passed to Handler.onTerminate. The
// SDK populates it from the §15.4.1 shutdown frame or the
// lifecycle-channel terminate event.
export interface TerminationReason {
  // reason is the adapter-supplied reason string (drain, deadline,
  // etc.) or stdin_closed when the adapter closed stdin without a
  // shutdown frame.
  reason: string;
  // deadlineMs is the shutdown deadline in milliseconds when the
  // adapter supplied one; zero otherwise.
  deadlineMs: number;
}

// CreateRequest is the §15.7 snapshot of session context handed to
// Handler.onCreate once before the first Message. Handler implementations
// MUST treat it as read-only.
export interface CreateRequest {
  // sessionId is the session this runtime instance is bound to.
  sessionId: string;
  // taskId is the root task identifier for this session. Each session
  // has exactly one execution, so taskId is frozen for the session's
  // lifetime and onCreate is invoked once with this value.
  // spec: §15.7 (TaskID frozen, OnCreate once), §7.1 (one execution per session)
  taskId: string;
  // runtimeOptions is the effective caller options map.
  runtimeOptions?: Record<string, unknown>;
  // workspacePlan references the §14 materialized workspace plan.
  workspacePlan?: WorkspacePlan;
  // credentials is the current §4.7 credential bundle. The SDK
  // refreshes it in place on rotation rather than re-invoking
  // onCreate. Undefined when the runtime has no active lease.
  credentials?: CredentialBundle;
  // manifestSnapshot is the parsed adapter manifest.
  manifestSnapshot?: AdapterManifest;
}

// Message is the §15.7 per-turn envelope handed to Handler.onMessage
// for every §15.4.1 message frame. Fields other than envelope are
// SDK-derived conveniences.
export interface Message {
  // envelope is the canonical §15.4.1 MessageEnvelope. All message
  // semantics live on this field.
  envelope: MessageEnvelope;
  // sessionId is the session the message was delivered to.
  sessionId: string;
  // taskId is the root task identifier of the session the message belongs
  // to. It always equals CreateRequest.taskId, which is frozen for the
  // session's lifetime.
  // spec: §15.7 (TaskID frozen), §7.1 (one execution per session)
  taskId: string;
  // sequence is a monotonic, SDK-assigned per-task counter ordering
  // messages as the SDK observed them on stdin. It is local to this
  // process and suitable for logging only.
  sequence: number;
}

// Reply is the value Handler.onMessage returns. The SDK serializes it
// into the stdout §15.4.1 response frame: parts becomes output.
export interface Reply {
  // parts is the OutputPart array the runtime emits for this turn.
  // Undefined or empty is valid when output was already emitted via
  // the lenny/output platform MCP tool.
  parts?: OutputPart[];
  // error reports a structured failure for this turn. When set, the
  // adapter maps the task to failed and populates TaskResult.error.
  error?: ResponseError;
  // streaming indicates more parts may still arrive out-of-band
  // before the turn is final.
  streaming?: boolean;
  // final marks this Reply as the terminal response for the turn.
  // final MUST be true for Basic-level runtimes. The SDK treats a
  // Reply that omits final and streaming as final.
  final?: boolean;
}

// textReply builds a final Reply carrying a single text part.
export function textReply(s: string): Reply {
  return { parts: [text(s)], final: true };
}

// Handler is the single interface a runtime author implements. The SDK
// invokes onCreate once before the first message of a task, onMessage
// for every inbound §15.4.1 message frame, and onTerminate once when
// the adapter closes stdin or sends a shutdown frame.
export interface Handler {
  // onCreate receives the task-scoped context snapshot before the
  // first Message is delivered. A rejected promise aborts the runtime.
  onCreate(req: CreateRequest): Promise<void> | void;
  // onMessage handles one inbound message and returns the turn's
  // Reply. A rejected promise is reported to the adapter as a
  // structured response error and the runtime continues.
  onMessage(msg: Message, tools: HandlerTools): Promise<Reply> | Reply;
  // onTerminate runs once when the session ends. It SHOULD return
  // before the shutdown deadline elapses.
  onTerminate(reason: TerminationReason): Promise<void> | void;
}

// HandlerTools is the SDK surface passed to Handler.onMessage. It
// carries the §15.4.1 adapter-local tool helpers (available at every
// level), the §8.5 platform MCP tool helpers (Standard level and
// above), and the current §4.7 credential bundle.
export interface HandlerTools {
  // adapter is the §15.4.1 adapter-local tool surface. It is present
  // at every integration level.
  adapter: AdapterTools;
  // platform is the §8.5 platform MCP tool surface, present only when
  // the runtime runs at Standard level or above and the manifest
  // advertised a platform MCP server. Undefined otherwise.
  platform?: PlatformTools;
  // credentials is the current §4.7 credential bundle, or undefined
  // when the runtime's pool has no active lease.
  credentials?: CredentialBundle;
}

// AdapterTools and PlatformTools are declared in their own modules;
// re-declared here as forward interface references so types.ts has no
// import cycle. The concrete classes implement these interfaces.

// AdapterTools is the §15.4.1 adapter-local tool surface.
export interface AdapterTools {
  toolCall(name: string, args: Record<string, unknown>): Promise<ToolResult>;
  readFile(path: string): Promise<string>;
  writeFile(path: string, content: string): Promise<void>;
  listDir(path: string): Promise<OutputPart[]>;
  deleteFile(path: string): Promise<void>;
}

// ToolResult is the decoded result of an adapter-local tool call.
export interface ToolResult {
  content: OutputPart[];
  isError: boolean;
}

// PlatformTools is the §8.5 platform MCP tool surface.
export interface PlatformTools {
  delegateTask(
    target: string,
    parts: OutputPart[],
    budget?: Record<string, unknown>,
  ): Promise<TaskHandle>;
  awaitChildren(childIds: string[], mode?: string): Promise<TaskResult2[]>;
  cancelChild(childId: string): Promise<void>;
  discoverAgents(query: Record<string, unknown>): Promise<unknown>;
  output(parts: OutputPart[]): Promise<void>;
  requestInput(prompt: OutputPart[]): Promise<OutputPart[]>;
  requestElicitation(args: Record<string, unknown>): Promise<unknown>;
  sendMessage(args: Record<string, unknown>): Promise<unknown>;
  memoryWrite(args: Record<string, unknown>): Promise<void>;
  memoryQuery(args: Record<string, unknown>): Promise<unknown>;
  getTaskTree(args: Record<string, unknown>): Promise<unknown>;
  setTracingContext(ctx: Record<string, unknown>): Promise<void>;
  connector(id: string): MCPConnection | undefined;
  call(name: string, args: Record<string, unknown>): Promise<unknown>;
}

// TaskHandle is the §8.2 lenny/delegate_task return value.
export interface TaskHandle {
  taskId: string;
}

// TaskOutput is the output object of a §8.8 TaskResult.
export interface TaskOutput {
  parts?: OutputPart[];
}

// TaskResult2 mirrors the §8.8 TaskResult schema returned by
// lenny/await_children, restricted to the fields the SDK decodes. The
// name carries a numeric suffix because ToolResult already occupies
// the natural name in this module.
export interface TaskResult2 {
  schemaVersion?: number;
  taskId: string;
  state: string;
  output: TaskOutput;
  error?: ResponseError;
}

// MCPConnection is the per-connector MCP client surface.
export interface MCPConnection {
  callTool(name: string, args: unknown): Promise<unknown>;
}

// ProtocolError signals a non-recoverable inbound-format violation.
// run rejects with it; an entrypoint maps it to the §15.4
// protocol-error exit code (2).
export class ProtocolError extends Error {
  constructor(message: string) {
    super(`protocol error: ${message}`);
    this.name = "ProtocolError";
  }
}

// isProtocolError reports whether err is a ProtocolError. An entrypoint
// uses it to select the §15.4 protocol-error exit code.
export function isProtocolError(err: unknown): err is ProtocolError {
  return err instanceof ProtocolError;
}
