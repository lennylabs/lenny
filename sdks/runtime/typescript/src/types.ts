// SPDX-License-Identifier: MIT

// This file holds the wire and convenience types the runtime-author SDK
// surfaces. The wire-level types (MessagePart, MessageEnvelope, the
// inbound and outbound frame types) mirror the §28.5.3 adapter binary
// protocol. The convenience types (CreateRequest, Message, Reply,
// CredentialBundle, AdapterManifest, WorkspacePlan) are §15.7 wrappers
// the SDK materializes from each session's session_start frame, the
// credential file that frame names, the pod-scoped manifest, and the stdin
// framing before invoking Handler methods. They introduce no new
// wire types.

// SCHEMA_VERSION is the current MessagePart and MessageEnvelope schema
// revision (§28.5.3). Producers stamp it on every emitted MessagePart.
export const SCHEMA_VERSION = 1;

// MessagePart is the §28.5.3 internal content model. A part either
// carries bytes inline or references blob storage; inline and ref are
// mutually exclusive. Basic-level runtimes need only set type and
// inline; the SDK stamps schemaVersion when it is unset.
export interface MessagePart {
  // schemaVersion identifies the MessagePart schema revision. Defaults
  // to 1. The SDK sets it to 1 on any emitted part that leaves it
  // undefined.
  schemaVersion?: number;
  // id is a stable part identifier. The adapter generates one when a
  // runtime omits it.
  id?: string;
  // type is an open string from the §28.5.3 canonical type registry
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
  parts?: MessagePart[];
  // status is one of streaming, complete, or failed.
  status?: string;
}

// text builds a minimal text MessagePart with schemaVersion set.
export function text(s: string): MessagePart {
  return { schemaVersion: SCHEMA_VERSION, type: "text", inline: s };
}

// MessageFrom is the §15.4 from object. kind is one of client, agent,
// system, or external. The adapter injects both fields; runtimes never
// supply them.
export interface MessageFrom {
  kind: string;
  id: string;
}

// MessageEnvelope is the §15.4 unified inbound message format. The
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
  // sessionId names the session this frame is addressed to. The adapter
  // populates it on every session-scoped frame on every pod, and the
  // runtime echoes it on the frames it emits in response.
  // spec: §28.5.3.
  sessionId?: string;
  input?: MessagePart[];
}

// ResponseError is the optional §28.5.3 response.error object and the
// §8.8 TaskResult.error object. Both carry a code and a message.
export interface ResponseError {
  code: string;
  message?: string;
}

// CredentialBundle is the parsed runtime credential file that a
// session's session_start names in credentialsPath, the session's own
// /run/lenny/slots/{sessionId}/credentials.json. The file lists one entry
// per credential provider the session holds a lease for. The SDK reloads
// the session's bundle on a credentials_rotated lifecycle event naming
// that session.
//
// spec: §4.7.11 (item 4, runtime credential file contract), §28.5.3
// (CH-MSGSOCK, Inbound: session_start).
export interface CredentialBundle {
  // providers holds one entry per leased credential provider.
  providers: ProviderCredential[];
}

// ProviderCredential is one entry of a CredentialBundle's providers list.
// materializedConfig is left as the decoded JSON object because its fields
// depend on the provider and the delivery mode: a proxy entry carries
// proxyUrl and leaseToken, and a direct entry carries the provider's own
// credential fields.
//
// spec: §4.7.11 (item 4, runtime credential file contract), §4.9
// (materializedConfig schema by provider).
export interface ProviderCredential {
  // leaseId identifies the §4.9 credential lease.
  leaseId: string;
  // provider is the credential provider identifier.
  provider: string;
  // expiresAt is the ISO 8601 lease expiry timestamp.
  expiresAt?: string;
  // deliveryMode is direct or proxy.
  deliveryMode: string;
  // materializedConfig is the entry's materializedConfig object.
  materializedConfig?: Record<string, unknown>;
}

// ExperimentContext is the experiment enrollment a session's
// session_start carries in experimentContext. inherited is true when the
// enrollment was propagated from a parent session through delegation.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table), §10.7.
export interface ExperimentContext {
  experimentId: string;
  variantId: string;
  inherited: boolean;
}

// LLMConfig is the LLM provider configuration a session's session_start
// carries in llm. deliveryMode is direct or proxy; dialect names the
// provider dialect a proxy-mode runtime speaks to the LLM Proxy; apiKeyEnv
// names the variable a runtime's LLM client reads its key from, which the
// runtime sets for this session only and never in its own process
// environment; headers lists the headers a proxy-mode runtime sends.
//
// spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start field table), §4.9.
export interface LLMConfig {
  deliveryMode: string;
  dialect?: string;
  apiKeyEnv?: string;
  headers?: Record<string, string>;
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

// AdapterLocalTool is one §28.5.3 adapter-local tool entry: a name, a
// human-readable description, and a JSON Schema for its arguments.
export interface AdapterLocalTool {
  name: string;
  description?: string;
  inputSchema?: Record<string, unknown>;
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
export interface AdapterManifest {
  // version is the manifest schema version. Every increment is
  // breaking; the SDK rejects a version newer than it understands.
  version?: number;
  // mcpNonce is the §15.4.3 intra-pod MCP nonce (256-bit hex). The
  // SDK injects it as params._lennyNonce on every MCP initialize.
  mcpNonce?: string;
  // platformMcpServer names the platform MCP server socket.
  platformMcpServer?: MCPServerRef;
  // connectorServers names the per-connector MCP server sockets.
  connectorServers?: ConnectorServerRef[];
  // runtimeOps names the Full-level CH-RUNTIMEOPS socket.
  runtimeOps?: SocketRef;
  // adapterLocalTools enumerates the §28.5.3 adapter-local tools the
  // runtime may invoke via stdout tool_call frames.
  adapterLocalTools?: AdapterLocalTool[];
  // runtimeOptions is the effective caller options map.
  runtimeOptions?: Record<string, unknown>;
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
// SDK populates it from the session's session_end, from the §28.5.3
// shutdown frame, or from the end of the connection.
export interface TerminationReason {
  // reason is session_end when the session's session_end ended it, the
  // shutdown frame's reason (drain, deadline, etc.) when a shutdown ended
  // the process, or stdin_closed when the adapter closed the connection
  // without a shutdown frame.
  reason: string;
  // deadlineMs is the shutdown deadline in milliseconds when the
  // adapter supplied one; zero otherwise.
  deadlineMs: number;
}

// CreateRequest is the §15.7 snapshot of session context handed to
// Handler.onCreate when the session's session_start arrives and before the
// session's first Message is delivered. The SDK assembles it from the
// session_start frame, the credential file that frame names, and the
// pod-scoped adapter manifest. Handler implementations MUST treat it as
// read-only.
//
// spec: §15.7 (SDK Handler types), §28.5.3 (CH-MSGSOCK, Inbound:
// session_start).
export interface CreateRequest {
  // sessionId is the session this request opens, the session_start's
  // sessionId.
  sessionId: string;
  // taskId is the session's external-protocol task identifier. Each
  // session has exactly one execution, so it equals sessionId; the SDK
  // derives it from the session_start's sessionId.
  // spec: §15.7 (TaskID derived from sessionId), §7.2 (one execution per session)
  taskId: string;
  // runtimeOptions is the effective caller options map.
  runtimeOptions?: Record<string, unknown>;
  // workspacePlan references the §14 materialized workspace plan.
  workspacePlan?: WorkspacePlan;
  // credentials is the session's credential bundle, read from the file the
  // session_start's credentialsPath names. Undefined when the frame names
  // no credentialsPath. The SDK reloads the session's bundle on a
  // credentials_rotated event rather than re-invoking onCreate.
  credentials?: CredentialBundle;
  // experimentContext is the session_start's experimentContext; undefined
  // when the session is not enrolled in an experiment.
  experimentContext?: ExperimentContext;
  // tracingContext is the session_start's tracingContext; undefined for a
  // top-level session.
  tracingContext?: Record<string, string>;
  // llm is the session_start's llm; undefined when the session has no
  // active LLM credential lease.
  llm?: LLMConfig;
  // manifestSnapshot is the parsed pod-scoped adapter manifest.
  manifestSnapshot?: AdapterManifest;
}

// Message is the §15.7 per-turn envelope handed to Handler.onMessage
// for every §28.5.3 message frame. Fields other than envelope are
// SDK-derived conveniences.
export interface Message {
  // envelope is the canonical §15.4 MessageEnvelope. All message
  // semantics live on this field.
  envelope: MessageEnvelope;
  // sessionId is the session the message was delivered to, the frame's
  // sessionId.
  sessionId: string;
  // taskId is the external-protocol task identifier of the session the
  // message belongs to. It equals sessionId and always equals the
  // session's CreateRequest.taskId.
  // spec: §15.7 (TaskID derived from sessionId), §7.2 (one execution per session)
  taskId: string;
  // sequence is a monotonic, SDK-assigned per-session counter ordering the
  // session's messages as the SDK observed them on stdin. It is local to
  // this process and suitable for logging only.
  sequence: number;
}

// Reply is the value Handler.onMessage returns. The SDK serializes it
// into the stdout §28.5.3 response frame: parts becomes output.
export interface Reply {
  // parts is the MessagePart array the runtime emits for this turn.
  // Undefined or empty is valid when output was already emitted via
  // the lenny/output platform MCP tool.
  parts?: MessagePart[];
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

// Handler is the single interface a runtime author implements. One
// runtime process serves any number of sessions, one after another and
// side by side. The SDK invokes onCreate when a session_start opens a
// session, and writes the session's session_started once onCreate
// settles; onMessage for each of the session's messages; and onTerminate
// once when that session ends, on its session_end or at the end of the
// connection. Each session's calls are chained on that session's own
// promise chain, so calls for one session never overlap while calls for
// different sessions interleave; an implementation keeps per-session state
// keyed by session. A session whose onCreate rejects is answered as the
// CH-MSGSOCK Session errors rule states, and the process keeps serving its
// other sessions.
//
// spec: §15.7 (API surface, Handler), §4.7.10 (runtime process lifetime),
// §28.5.3 (CH-MSGSOCK, Inbound: session_start, Session errors).
export interface Handler {
  // onCreate receives the session's context snapshot before the
  // session's first Message is delivered. A rejection or throw fails the
  // session's creation: the SDK writes session_started with error and
  // answers the session's messages with a RUNTIME_ERROR response.
  onCreate(req: CreateRequest): Promise<void> | void;
  // onMessage handles one inbound message and returns the turn's
  // Reply. A rejected promise is reported to the adapter as a
  // structured response error and the session continues with its next
  // message.
  onMessage(msg: Message, tools: HandlerTools): Promise<Reply> | Reply;
  // onTerminate runs once when the session sessionId ends, after the
  // session's last handler call settled. It runs for every session the SDK
  // opened, including one whose creation failed. It SHOULD settle before
  // the shutdown deadline elapses.
  onTerminate(
    sessionId: string,
    reason: TerminationReason,
  ): Promise<void> | void;
}

// HandlerTools is the SDK surface passed to Handler.onMessage. It
// carries the §28.5.3 adapter-local tool helpers (available at every
// level), the §8.5 platform MCP tool helpers (Standard level and
// above), and the current §4.7 credential bundle.
export interface HandlerTools {
  // adapter is the §28.5.3 adapter-local tool surface. It is present
  // at every integration level.
  adapter: AdapterTools;
  // platform is the §8.5 platform MCP tool surface, present only when
  // the runtime runs at Standard level or above and the manifest
  // advertised a platform MCP server. Undefined otherwise.
  platform?: PlatformTools;
  // credentials is the session's current credential bundle, or undefined
  // when the session's session_start named no credential file.
  credentials?: CredentialBundle;
}

// AdapterTools and PlatformTools are declared in their own modules;
// re-declared here as forward interface references so types.ts has no
// import cycle. The concrete classes implement these interfaces.

// AdapterTools is the §28.5.3 adapter-local tool surface.
export interface AdapterTools {
  toolCall(name: string, args: Record<string, unknown>): Promise<ToolResult>;
  readFile(path: string): Promise<string>;
  writeFile(path: string, content: string): Promise<void>;
  listDir(path: string): Promise<MessagePart[]>;
  deleteFile(path: string): Promise<void>;
}

// ToolResult is the decoded result of an adapter-local tool call.
export interface ToolResult {
  content: MessagePart[];
  isError: boolean;
}

// PlatformTools is the §8.5 platform MCP tool surface.
export interface PlatformTools {
  delegateTask(
    target: string,
    parts: MessagePart[],
    budget?: Record<string, unknown>,
  ): Promise<TaskHandle>;
  awaitChildren(childIds: string[], mode?: string): Promise<TaskResult2[]>;
  cancelChild(childId: string): Promise<void>;
  discoverAgents(query: Record<string, unknown>): Promise<unknown>;
  output(parts: MessagePart[]): Promise<void>;
  requestInput(prompt: MessagePart[]): Promise<MessagePart[]>;
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
  parts?: MessagePart[];
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
