// SPDX-License-Identifier: MIT

// Package @lennylabs/runtime-sdk is the Lenny TypeScript runtime-author
// SDK. It lets a developer write a Lenny agent runtime in TypeScript or
// JavaScript by implementing the Handler interface and calling run; the
// SDK drives the §28.5.3 adapter binary protocol, the §15.4.2 RPC
// lifecycle state machine, the §8.5 platform MCP tool helpers, and the
// Full-level CH-RUNTIMEOPS.
//
// This SDK is the runtime-author counterpart of the client SDK at
// sdks/client/typescript. The client SDK wraps the gateway REST API for
// application developers; this SDK targets the agent process that runs
// inside a Lenny-managed pod and speaks the Runtime Adapter
// Specification (§15.4).
//
// Integration levels
//
// The SDK covers the §15.4.3 integration levels:
//
//   - Basic: the stdin/stdout JSON Lines protocol. run with no options
//     exercises Basic level fully: session_start and session_end per
//     session, message/response round trip, heartbeat acknowledgement,
//     shutdown within the deadline, and forward-compatible handling of
//     unknown frame types.
//   - Standard: the SDK additionally dials the manifest-advertised
//     platform MCP server and connector MCP servers with the §15.4.3
//     manifest-nonce handshake, and exposes typed §8.5 platform tool
//     helpers through the tools value passed to onMessage.
//   - Full: the SDK additionally opens the §15.4.3 CH-RUNTIMEOPS,
//     completes the lifecycle_capabilities / lifecycle_support
//     handshake, and answers checkpoint, interrupt, credential
//     rotation, and deadline events, each routed to the session the
//     event names.
//
// Sessions
//
// One runtime process serves every session the pod holds. Each
// session_start opens a session with its own context and promise chain,
// and each session_end releases it, so onCreate, onMessage, and
// onTerminate run once per session, and calls for different sessions
// interleave (§4.7.10, §15.7).
//
// Minimal runtime
//
// A Basic-level echo runtime is a Handler whose onMessage echoes the
// inbound parts:
//
//   import { run, Handler } from "@lennylabs/runtime-sdk";
//
//   const handler: Handler = {
//     onCreate() {},
//     onTerminate() {},
//     onMessage(msg) {
//       return { parts: msg.envelope.input ?? [], final: true };
//     },
//   };
//
//   run(handler).catch(() => process.exit(1));

export { run } from "./runtime.js";
export type { RunOptions, IntegrationLevel } from "./runtime.js";
export { Tools, McpClient } from "./mcp.js";
export { AdapterToolset } from "./tool.js";
export { Lifecycle } from "./lifecycle.js";
export type { LifecycleHooks, LifecycleEvent } from "./lifecycle.js";
export {
  SCHEMA_VERSION,
  ProtocolError,
  isProtocolError,
  text,
  textReply,
} from "./types.js";
export type {
  Handler,
  HandlerTools,
  CreateRequest,
  Message,
  MessageEnvelope,
  MessageFrom,
  Reply,
  MessagePart,
  ResponseError,
  TerminationReason,
  CredentialBundle,
  ProviderCredential,
  ExperimentContext,
  LLMConfig,
  AdapterManifest,
  MCPServerRef,
  ConnectorServerRef,
  SocketRef,
  AdapterLocalTool,
  WorkspacePlan,
  AdapterTools,
  PlatformTools,
  ToolResult,
  TaskHandle,
  TaskOutput,
  TaskResult2,
  MCPConnection,
} from "./types.js";
