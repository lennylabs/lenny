// SPDX-License-Identifier: MIT

// This module is the §15.7 entry point of the TypeScript runtime-author
// SDK. run wires up the §15.4.1 stdin/stdout framing, optionally dials
// the manifest-advertised Unix sockets (platform MCP server, connector
// MCP servers, lifecycle channel) with the §15.4.3 manifest-nonce
// handshake, parses the §4.7 credential file, and drives the §15.4.2
// dispatch loop.

import { readFile } from "node:fs/promises";
import type { Readable, Writable } from "node:stream";
import { Lifecycle } from "./lifecycle.js";
import type { LifecycleHooks } from "./lifecycle.js";
import { Tools } from "./mcp.js";
import { AdapterToolset, ToolCallRegistry } from "./tool.js";
import type { InboundToolResult } from "./tool.js";
import { FrameWriter, LineReader, dialUnixSocket } from "./transport.js";
import type {
  AdapterManifest,
  CredentialBundle,
  CreateRequest,
  Handler,
  HandlerTools,
  Message,
  MessageEnvelope,
  MessagePart,
  Reply,
  TerminationReason,
} from "./types.js";
import { ProtocolError, SCHEMA_VERSION } from "./types.js";

// IntegrationLevel is the §15.4.3 integration level the SDK runs at.
export type IntegrationLevel = "basic" | "standard" | "full";

// SOCKET_ENV_VAR is the §4.7 environment variable the adapter sets on
// the runtime container in the sidecar deployment model. Its value is
// the adapter's abstract Unix socket name.
const SOCKET_ENV_VAR = "LENNY_ADAPTER_SOCKET";

// MANIFEST_ENV_VAR overrides the §4.7 adapter manifest path. The
// default path is /run/lenny/adapter-manifest.json.
const MANIFEST_ENV_VAR = "LENNY_ADAPTER_MANIFEST";

// DEFAULT_MANIFEST_PATH is the §4.7 adapter manifest path.
const DEFAULT_MANIFEST_PATH = "/run/lenny/adapter-manifest.json";

// DEFAULT_CREDENTIALS_PATH is the §4.7 runtime credential file path.
const DEFAULT_CREDENTIALS_PATH = "/run/lenny/credentials.json";

// RunOptions configures run. Every field is optional; the zero-value
// configuration covers the Basic level.
export interface RunOptions {
  // level is the §15.4.3 integration level. Defaults to "basic".
  // "standard" dials the platform and connector MCP servers; "full"
  // additionally opens the lifecycle channel.
  level?: IntegrationLevel;
  // lifecycle holds the Full-level lifecycle-event callbacks. Setting
  // it implies level "full".
  lifecycle?: LifecycleHooks;
  // manifestPath overrides the §4.7 adapter manifest path. Defaults to
  // the LENNY_ADAPTER_MANIFEST environment variable when set, otherwise
  // /run/lenny/adapter-manifest.json.
  manifestPath?: string;
  // credentialsPath overrides the §4.7 runtime credential file path.
  // Defaults to /run/lenny/credentials.json.
  credentialsPath?: string;
  // socketTransport enables the §4.7 abstract-Unix-socket transport
  // fallback. When true (the default) and LENNY_ADAPTER_SOCKET is set,
  // run dials that socket instead of using stdin/stdout.
  socketTransport?: boolean;
  // dialTimeoutMs bounds each Unix-socket dial. Defaults to 5000.
  dialTimeoutMs?: number;
  // input and output override the §15.4.1 byte transport with explicit
  // streams. They are intended for in-process testing; production
  // runtimes use the default stdin/stdout or socket transport.
  input?: Readable;
  output?: Writable;
  // logger is the diagnostic sink for SDK-internal messages (unknown
  // frame types, handler errors). Defaults to a stderr writer. Pass a
  // no-op to silence diagnostics.
  logger?(msg: string): void;
}

// resolveConfig fills RunOptions with the Basic-level defaults.
interface ResolvedConfig {
  level: IntegrationLevel;
  lifecycle: LifecycleHooks;
  manifestPath: string;
  credentialsPath: string;
  socketTransport: boolean;
  dialTimeoutMs: number;
  input?: Readable;
  output?: Writable;
  logger(msg: string): void;
}

function resolveConfig(opts: RunOptions): ResolvedConfig {
  let level: IntegrationLevel = opts.level ?? "basic";
  if (opts.lifecycle && level !== "full") {
    level = "full";
  }
  return {
    level,
    lifecycle: opts.lifecycle ?? {},
    manifestPath:
      opts.manifestPath ??
      process.env[MANIFEST_ENV_VAR] ??
      DEFAULT_MANIFEST_PATH,
    credentialsPath: opts.credentialsPath ?? DEFAULT_CREDENTIALS_PATH,
    socketTransport: opts.socketTransport ?? true,
    dialTimeoutMs: opts.dialTimeoutMs ?? 5000,
    input: opts.input,
    output: opts.output,
    logger: opts.logger ?? ((msg: string) => process.stderr.write(msg + "\n")),
  };
}

// levelRank orders the §15.4.3 integration levels for comparison.
function levelRank(level: IntegrationLevel): number {
  return level === "full" ? 2 : level === "standard" ? 1 : 0;
}

// stampParts sets schemaVersion on every part that left it unset,
// honoring the §15.4.1 producer obligation, and returns a non-empty
// array so an empty Reply still serializes as output: [].
function stampParts(parts: MessagePart[]): MessagePart[] {
  return parts.map((p) =>
    p.schemaVersion === undefined
      ? { ...p, schemaVersion: SCHEMA_VERSION }
      : p,
  );
}

// run wires up the §15.4.1 stdin/stdout framing, dials the higher-level
// channels for the configured integration level, parses the §4.7
// credential file, and drives the §15.4.2 dispatch loop. It resolves
// when the adapter closes the inbound stream or sends a shutdown frame.
//
// run with no options covers the Basic level. Set level to "standard"
// or "full" to opt into the higher integration levels.
export async function run(handler: Handler, opts: RunOptions = {}): Promise<void> {
  if (!handler) {
    throw new Error("runtime: run requires a handler");
  }
  const session = new Session(handler, resolveConfig(opts));
  return session.run();
}

// Session holds the per-process SDK state for one run call.
class Session {
  private writer!: FrameWriter;
  private manifest?: AdapterManifest;
  private credentials?: CredentialBundle;
  private tools?: Tools;
  private lifecycle?: Lifecycle;
  private readonly registry = new ToolCallRegistry();
  private sequence = 0;
  private terminated = false;
  private exitReason?: TerminationReason;
  private loopStopped = false;

  // dispatchTail serializes message handling so the §15.4.1
  // coordinator-local FIFO contract holds: handlers run one at a time
  // in the order the loop read their frames.
  private dispatchTail: Promise<void> = Promise.resolve();

  constructor(
    private readonly handler: Handler,
    private readonly cfg: ResolvedConfig,
  ) {}

  // run drives one runtime lifecycle: resolve the transport, load the
  // manifest and credentials, dial the higher-level channels for the
  // configured level, then run the §15.4.1 frame loop.
  async run(): Promise<void> {
    const transport = await this.openTransport();
    this.writer = new FrameWriter(transport.output);

    // §4.7 manifest and credential file. Both are optional: a
    // Basic-level runtime is exercised without a manifest, and a
    // runtime whose pool has no active lease has no credential file.
    await this.loadManifest();
    await this.loadCredentials();

    await this.startChannels();

    // onCreate runs once before the first message with the task-scoped
    // snapshot the SDK assembled from the manifest and credential file.
    try {
      await this.handler.onCreate(this.buildCreateRequest());
    } catch (err) {
      this.closeChannels();
      transport.close();
      throw new Error(`runtime: onCreate: ${(err as Error).message}`);
    }

    let loopErr: Error | undefined;
    try {
      await this.loop(transport.input);
    } catch (err) {
      loopErr = err as Error;
    }

    // Wait for every queued message handler to write its response
    // frame before onTerminate.
    await this.dispatchTail;

    // onTerminate runs once on the way out. The reason is the shutdown
    // frame's reason when the loop exited on a shutdown; otherwise the
    // adapter closed the transport without one. A lifecycle terminate,
    // if it fired, already invoked onTerminate and this call is a
    // no-op.
    const reason: TerminationReason = this.exitReason ?? {
      reason: "stdin_closed",
      deadlineMs: 0,
    };
    await this.invokeTerminate(reason.reason, reason.deadlineMs);

    this.registry.rejectAll(new Error("runtime: inbound stream closed"));
    this.closeChannels();
    transport.close();
    if (loopErr) {
      throw loopErr;
    }
  }

  // loop is the §15.4.1 frame loop. It reads newline-delimited JSON and
  // routes each frame by type: message frames are dispatched in FIFO
  // order, heartbeat frames are answered immediately, tool_result
  // frames are correlated, and a shutdown frame ends the loop. Unknown
  // frame types are ignored for forward compatibility (§15.4.1).
  private async loop(input: Readable): Promise<void> {
    const reader = new LineReader(input);
    for (;;) {
      if (this.loopStopped) {
        return;
      }
      let line: string | null;
      try {
        line = await reader.next();
      } catch (err) {
        throw new ProtocolError(
          `input read error: ${(err as Error).message}`,
        );
      }
      if (line === null) {
        return;
      }
      if (line.length === 0) {
        continue;
      }
      let frame: { type?: unknown };
      try {
        frame = JSON.parse(line) as { type?: unknown };
      } catch (err) {
        throw new ProtocolError(
          `malformed JSON Lines on input: ${(err as Error).message}`,
        );
      }
      const kind = typeof frame.type === "string" ? frame.type : "";
      switch (kind) {
        case "message":
          this.enqueueMessage(line);
          break;
        case "heartbeat":
          await this.writer.write({ type: "heartbeat_ack" });
          break;
        case "tool_result":
          this.handleToolResult(line);
          break;
        case "shutdown":
          this.handleShutdown(line);
          return;
        default:
          this.cfg.logger(`runtime: ignoring unknown frame type "${kind}"`);
      }
    }
  }

  // enqueueMessage decodes one §15.4.1 message frame and chains its
  // handler onto the dispatch tail so messages are processed one at a
  // time in arrival order.
  private enqueueMessage(line: string): void {
    let env: MessageEnvelope;
    try {
      env = JSON.parse(line) as MessageEnvelope;
    } catch (err) {
      this.cfg.logger(
        `runtime: malformed message envelope: ${(err as Error).message}`,
      );
      return;
    }
    this.dispatchTail = this.dispatchTail.then(() => this.handleMessage(env));
  }

  // handleMessage invokes onMessage for one decoded §15.4.1 message and
  // writes the resulting response frame. A handler rejection is
  // reported as a structured response error so the adapter records the
  // failure without losing context (§15.4.1 error reporting via
  // response).
  private async handleMessage(env: MessageEnvelope): Promise<void> {
    this.sequence += 1;
    const msg: Message = {
      envelope: env,
      sessionId: this.manifest?.sessionId ?? "",
      taskId: this.manifest?.taskId ?? "",
      sequence: this.sequence,
    };
    const tools: HandlerTools = {
      adapter: new AdapterToolset(
        this.writer,
        this.registry,
        this.cfg.dialTimeoutMs,
        env.slotId,
      ),
      platform: this.tools,
      credentials: this.credentials,
    };

    let reply: Reply;
    try {
      reply = await this.handler.onMessage(msg, tools);
    } catch (err) {
      this.cfg.logger(`runtime: onMessage error: ${(err as Error).message}`);
      await this.safeWrite({
        type: "response",
        output: [],
        error: { code: "RUNTIME_ERROR", message: (err as Error).message },
        ...(env.slotId ? { slotId: env.slotId } : {}),
      });
      return;
    }

    // A turn marked streaming and not final defers the response frame.
    // The §15.4.1 contract still requires a final response frame, so
    // the SDK emits it once the runtime returns a final Reply.
    if (reply.streaming && !reply.final) {
      return;
    }
    await this.safeWrite({
      type: "response",
      output: stampParts(reply.parts ?? []),
      ...(reply.error ? { error: reply.error } : {}),
      ...(env.slotId ? { slotId: env.slotId } : {}),
    });
  }

  // handleToolResult routes an inbound §15.4.1 tool_result frame to the
  // pending tool_call that emitted the matching id.
  private handleToolResult(line: string): void {
    let tr: InboundToolResult;
    try {
      tr = JSON.parse(line) as InboundToolResult;
    } catch (err) {
      this.cfg.logger(
        `runtime: malformed tool_result frame: ${(err as Error).message}`,
      );
      return;
    }
    if (!this.registry.deliver(tr)) {
      this.cfg.logger(
        `runtime: tool_result "${tr.id}" has no pending tool_call`,
      );
    }
  }

  // handleShutdown decodes the §15.4.1 shutdown frame and records the
  // termination reason for run to apply after draining in-flight
  // handlers.
  private handleShutdown(line: string): void {
    let sd: { reason?: string; deadline_ms?: number };
    try {
      sd = JSON.parse(line) as { reason?: string; deadline_ms?: number };
    } catch {
      sd = {};
    }
    this.exitReason = {
      reason: sd.reason ?? "shutdown",
      deadlineMs: sd.deadline_ms ?? 0,
    };
  }

  // safeWrite writes an outbound frame, logging a write error instead
  // of propagating it: a write failure usually means the adapter has
  // closed the transport.
  private async safeWrite(frame: unknown): Promise<void> {
    try {
      await this.writer.write(frame);
    } catch (err) {
      this.cfg.logger(`runtime: write frame: ${(err as Error).message}`);
    }
  }

  // startChannels dials the §15.4.3 platform MCP server, connector MCP
  // servers, and lifecycle channel for the configured integration
  // level. When a higher-level channel is configured but the manifest
  // does not advertise it, the SDK logs the gap and degrades to the
  // level the manifest supports, so a Standard- or Full-level binary
  // still runs in a Basic-only environment.
  private async startChannels(): Promise<void> {
    if (levelRank(this.cfg.level) >= levelRank("standard")) {
      if (!this.manifest?.platformMcpServer?.socket) {
        this.cfg.logger(
          "runtime: no platform MCP server in the manifest; degrading to Basic level",
        );
      } else {
        this.tools = await Tools.dial(this.manifest, this.cfg.dialTimeoutMs);
      }
    }
    if (levelRank(this.cfg.level) >= levelRank("full")) {
      if (!this.manifest?.lifecycleChannel?.socket) {
        this.cfg.logger(
          "runtime: no lifecycle channel in the manifest; lifecycle features disabled",
        );
      } else {
        this.lifecycle = await Lifecycle.dial(
          this.manifest,
          this.cfg.dialTimeoutMs,
          this.cfg.lifecycle,
          {
            stdoutWriter: this.writer,
            reloadCredentials: () => {
              void this.loadCredentials();
              return this.credentials;
            },
            invokeTerminate: (reason, deadlineMs) =>
              this.invokeTerminate(reason, deadlineMs),
            stopFrameLoop: () => {
              this.loopStopped = true;
            },
            log: (msg) => this.cfg.logger(`runtime: ${msg}`),
          },
        );
      }
    }
  }

  // closeChannels releases the higher-level channels.
  private closeChannels(): void {
    this.tools?.close();
    this.lifecycle?.close();
  }

  // invokeTerminate calls Handler.onTerminate at most once. The
  // terminated guard makes a lifecycle-channel terminate and the stdin
  // shutdown path idempotent.
  private async invokeTerminate(
    reason: string,
    deadlineMs: number,
  ): Promise<void> {
    if (this.terminated) {
      return;
    }
    this.terminated = true;
    try {
      await this.handler.onTerminate({ reason, deadlineMs });
    } catch (err) {
      this.cfg.logger(
        `runtime: onTerminate error: ${(err as Error).message}`,
      );
    }
  }

  // buildCreateRequest assembles the §15.7 onCreate snapshot from the
  // manifest and credential file.
  private buildCreateRequest(): CreateRequest {
    return {
      sessionId: this.manifest?.sessionId ?? "",
      taskId: this.manifest?.taskId ?? "",
      runtimeOptions: this.manifest?.runtimeOptions,
      credentials: this.credentials,
      manifestSnapshot: this.manifest,
    };
  }

  // loadManifest parses the §4.7 adapter manifest. A missing file
  // leaves the manifest undefined; a malformed file is logged and
  // ignored. A manifest version newer than the SDK understands is
  // rejected (§4.7 forward-compatibility rule).
  private async loadManifest(): Promise<void> {
    let data: string;
    try {
      data = await readFile(this.cfg.manifestPath, "utf8");
    } catch (err) {
      this.cfg.logger(
        `runtime: no adapter manifest at ${this.cfg.manifestPath} (${(err as Error).message})`,
      );
      return;
    }
    let m: AdapterManifest;
    try {
      m = JSON.parse(data) as AdapterManifest;
    } catch (err) {
      this.cfg.logger(
        `runtime: malformed adapter manifest ${this.cfg.manifestPath}: ${(err as Error).message}`,
      );
      return;
    }
    if ((m.version ?? 0) > 1) {
      this.cfg.logger(
        `runtime: adapter manifest ${this.cfg.manifestPath} version ${m.version} is newer than supported (1)`,
      );
      return;
    }
    this.manifest = m;
  }

  // loadCredentials parses the §4.7 runtime credential file. A missing
  // file is normal when the runtime's pool has no active lease.
  private async loadCredentials(): Promise<void> {
    let data: string;
    try {
      data = await readFile(this.cfg.credentialsPath, "utf8");
    } catch {
      return;
    }
    try {
      this.credentials = JSON.parse(data) as CredentialBundle;
    } catch (err) {
      this.cfg.logger(
        `runtime: malformed credential file ${this.cfg.credentialsPath}: ${(err as Error).message}`,
      );
    }
  }

  // openTransport resolves the §15.4.1 transport. When explicit streams
  // were supplied it uses them; when socket transport is enabled and
  // LENNY_ADAPTER_SOCKET names a socket it dials that socket; otherwise
  // it returns process.stdin / process.stdout.
  private async openTransport(): Promise<{
    input: Readable;
    output: Writable;
    close(): void;
  }> {
    if (this.cfg.input || this.cfg.output) {
      return {
        input: this.cfg.input ?? process.stdin,
        output: this.cfg.output ?? process.stdout,
        close: () => undefined,
      };
    }
    if (this.cfg.socketTransport) {
      const name = (process.env[SOCKET_ENV_VAR] ?? "").trim();
      if (name !== "") {
        const conn = await dialUnixSocket(name, this.cfg.dialTimeoutMs);
        return {
          input: conn,
          output: conn,
          close: () => conn.destroy(),
        };
      }
    }
    return {
      input: process.stdin,
      output: process.stdout,
      close: () => undefined,
    };
  }
}
