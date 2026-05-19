// SPDX-License-Identifier: MIT
// Package @lennylabs/runtime-sdk is the Lenny TypeScript runtime-author
// SDK. It lets a developer write a Lenny agent runtime in TypeScript or
// JavaScript by implementing the Handler interface and calling run; the
// SDK drives the §15.4.1 adapter binary protocol, the §15.4.2 RPC
// lifecycle state machine, the §8.5 platform MCP tool helpers, and the
// Full-level lifecycle channel.
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
//     exercises Basic level fully — message/response round trip,
//     heartbeat acknowledgement, shutdown within the deadline, and
//     forward-compatible handling of unknown frame types.
//   - Standard: the SDK additionally dials the manifest-advertised
//     platform MCP server and connector MCP servers with the §15.4.3
//     manifest-nonce handshake, and exposes typed §8.5 platform tool
//     helpers through the tools value passed to onMessage.
//   - Full: the SDK additionally opens the §15.4.3 lifecycle channel,
//     completes the lifecycle_capabilities / lifecycle_support
//     handshake, and answers checkpoint, interrupt, credential
//     rotation, and deadline events.
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
export { Tools, McpClient } from "./mcp.js";
export { AdapterToolset } from "./tool.js";
export { Lifecycle } from "./lifecycle.js";
export { SCHEMA_VERSION, ProtocolError, isProtocolError, text, textReply, } from "./types.js";
//# sourceMappingURL=index.js.map