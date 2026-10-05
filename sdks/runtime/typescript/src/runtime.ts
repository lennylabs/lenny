// SPDX-License-Identifier: MIT

// This module is the §15.7 entry point of the TypeScript runtime-author
// SDK. run wires up the §28.5.3 stdin/stdout framing, optionally dials
// the manifest-advertised Unix sockets (platform MCP server, connector
// MCP servers, CH-RUNTIMEOPS) once per process with the §15.4.3
// manifest-nonce handshake, and drives the frame loop. One process serves
// every session the pod holds: each session_start opens a session with its
// own context and promise chain, and each session_end releases it
// (§4.7.10).

import { readFile } from "node:fs/promises";
import type { Readable, Writable } from "node:stream";
import { Lifecycle } from "./lifecycle.js";
import type { LifecycleHooks } from "./lifecycle.js";
import { Tools } from "./mcp.js";
import {
  SessionRecord,
  SessionTable,
  decodeSessionStart,
  loadCredentialBundle,
  sessionErrorResponse,
} from "./session.js";
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

// RunOptions configures run. Every field is optional; the zero-value
// configuration covers the Basic level.
export interface RunOptions {
  // level is the §15.4.3 integration level. Defaults to "basic".
  // "standard" dials the platform and connector MCP servers; "full"
  // additionally opens the CH-RUNTIMEOPS.
  level?: IntegrationLevel;
  // lifecycle holds the Full-level lifecycle-event callbacks. Setting
  // it implies level "full".
  lifecycle?: LifecycleHooks;
  // manifestPath overrides the §4.7 adapter manifest path. Defaults to
  // the LENNY_ADAPTER_MANIFEST environment variable when set, otherwise
  // /run/lenny/adapter-manifest.json.
  manifestPath?: string;
  // socketTransport enables the §4.7 abstract-Unix-socket transport
  // fallback. When true (the default) and LENNY_ADAPTER_SOCKET is set,
  // run dials that socket instead of using stdin/stdout.
  socketTransport?: boolean;
  // dialTimeoutMs bounds each Unix-socket dial. Defaults to 5000.
  dialTimeoutMs?: number;
  // input and output override the §28.5.3 byte transport with explicit
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
// honoring the §28.5.3 producer obligation, and returns a non-empty
// array so an empty Reply still serializes as output: [].
function stampParts(parts: MessagePart[]): MessagePart[] {
  return parts.map((p) =>
    p.schemaVersion === undefined
      ? { ...p, schemaVersion: SCHEMA_VERSION }
      : p,
  );
}

// run wires up the §28.5.3 stdin/stdout framing, dials the higher-level
// channels for the configured integration level once per process, and
// drives the frame loop. Each session_start opens a session with its own
// context and promise chain, and each session loads its credentials from
// the path its session_start names. run resolves when the adapter closes
// the inbound stream or sends a shutdown frame, after it ends every
// session the process holds.
//
// run with no options covers the Basic level. Set level to "standard"
// or "full" to opt into the higher integration levels.
//
// spec: §15.7 (API surface, Run), §4.7.10 (runtime process lifetime).
export async function run(handler: Handler, opts: RunOptions = {}): Promise<void> {
  if (!handler) {
    throw new Error("runtime: run requires a handler");
  }
  return new RuntimeProcess(handler, resolveConfig(opts)).run();
}

// errorMessage returns the message of a thrown value.
function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// RuntimeProcess holds the SDK state of one run call. The manifest, the
// MCP connections, and the CH-RUNTIMEOPS are process-scoped and shared by
// every session; each session's own context lives in its SessionRecord.
class RuntimeProcess {
  private writer!: FrameWriter;
  private manifest?: AdapterManifest;
  private tools?: Tools;
  private lifecycle?: Lifecycle;
  private readonly registry = new ToolCallRegistry();
  private readonly sessions = new SessionTable();
  // unreleased holds every record whose context is not yet released,
  // including one a session_end removed, so run waits for each.
  private readonly unreleased = new Set<SessionRecord>();
  private exitReason?: TerminationReason;

  constructor(
    private readonly handler: Handler,
    private readonly cfg: ResolvedConfig,
  ) {}

  // run drives one runtime process: resolve the transport, load the
  // manifest, dial the higher-level channels for the configured level,
  // run the §28.5.3 frame loop, and end every session once the loop ends.
  async run(): Promise<void> {
    const transport = await this.openTransport();
    this.writer = new FrameWriter(transport.output);

    // §4.7 manifest. It is optional: a Basic-level runtime is exercised
    // without one.
    await this.loadManifest();
    await this.startChannels();

    let loopErr: Error | undefined;
    try {
      await this.loop(transport.input);
    } catch (err) {
      loopErr = err as Error;
    }

    // Every live session dispatches the messages it queued and then runs
    // onTerminate with the shutdown frame's reason, or stdin_closed when
    // the adapter closed the transport without one. run resolves after
    // every session's context was released, including one a session_end
    // removed whose release is still running.
    this.closeSessions(
      this.exitReason ?? { reason: "stdin_closed", deadlineMs: 0 },
    );
    while (this.unreleased.size > 0) {
      await Promise.all([...this.unreleased].map((rec) => rec.released));
    }

    this.registry.rejectAll(new Error("runtime: inbound stream closed"));
    this.closeChannels();
    transport.close();
    if (loopErr) {
      throw loopErr;
    }
  }

  // loop is the §28.5.3 frame loop. It reads newline-delimited JSON and
  // routes each frame by type without waiting on any session's work:
  // session_start, message, tool_result, and session_end go to the
  // addressed session, a heartbeat is answered inline, and a shutdown
  // frame ends the loop. Unknown frame types are ignored for forward
  // compatibility.
  //
  // spec: §28.5.3 (CH-MSGSOCK).
  private async loop(input: Readable): Promise<void> {
    const reader = new LineReader(input);
    for (;;) {
      let line: string | null;
      try {
        line = await reader.next();
      } catch (err) {
        throw new ProtocolError(`input read error: ${errorMessage(err)}`);
      }
      if (line === null) {
        return;
      }
      if (line.length === 0) {
        continue;
      }
      let frame: Record<string, unknown>;
      try {
        frame = JSON.parse(line) as Record<string, unknown>;
      } catch (err) {
        throw new ProtocolError(`malformed JSON Lines on input: ${errorMessage(err)}`);
      }
      if (await this.routeFrame(frame)) {
        return;
      }
    }
  }

  // routeFrame handles one inbound frame. It resolves true for a shutdown
  // frame, which ends the loop.
  private async routeFrame(frame: Record<string, unknown>): Promise<boolean> {
    const kind =
      frame !== null && typeof frame === "object" && typeof frame.type === "string"
        ? frame.type
        : "";
    switch (kind) {
      case "session_start":
        this.handleSessionStart(frame);
        break;
      case "session_end":
        this.handleSessionEnd(frame);
        break;
      case "message":
        this.routeMessage(frame as unknown as MessageEnvelope);
        break;
      case "heartbeat":
        await this.writer.write({ type: "heartbeat_ack" });
        break;
      case "tool_result":
        this.handleToolResult(frame as unknown as InboundToolResult);
        break;
      case "shutdown":
        this.handleShutdown(frame);
        return true;
      default:
        this.cfg.logger(`runtime: ignoring unknown frame type "${kind}"`);
    }
    return false;
  }

  // handleSessionStart opens the session a session_start names. A frame
  // for a session the runtime already holds creates nothing and is
  // answered with session_started again, carrying the held session's
  // creation error when there is one.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: session_start rules 2 and 3).
  private handleSessionStart(frame: Record<string, unknown>): void {
    const start = decodeSessionStart(frame);
    if (start.sessionId === "" || start.startId === "") {
      throw new ProtocolError("session_start carries no sessionId or no startId");
    }
    const { rec, held } = this.sessions.open(start);
    if (held) {
      this.ackDuplicateStart(rec, start.startId);
      return;
    }
    this.unreleased.add(rec);
    this.chain(rec, () => this.create(rec));
  }

  // ackDuplicateStart answers a duplicate session_start for a held
  // session. While the held session's creation is still running the answer
  // waits for it, so session_started never precedes the context it
  // reports.
  private ackDuplicateStart(rec: SessionRecord, startId: string): void {
    if (!rec.createDone) {
      rec.pendingAcks.push(startId);
      return;
    }
    void this.writeSessionStarted(rec, startId, rec.createError);
  }

  // handleSessionEnd ends the start of the session's live record. The loop
  // removes the record from the routing table, marks it ended so no later
  // response or tool_call for it is written, rejects its pending tool_call
  // waiters, and chains its release. The session's chain skips the
  // messages still queued, waits for the in-flight handler, and runs
  // onTerminate once the creation has finished. A session_end for a
  // session the runtime does not hold is ignored.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rules 2 and 3).
  private handleSessionEnd(frame: Record<string, unknown>): void {
    const sessionId = typeof frame.sessionId === "string" ? frame.sessionId : "";
    const rec = this.sessions.remove(sessionId);
    if (!rec) {
      this.cfg.logger(
        `runtime: ignoring session_end for session "${sessionId}", which this runtime does not hold`,
      );
      return;
    }
    rec.ended = true;
    rec.endRead = true;
    this.registry.cancelOwner(rec);
    this.chain(rec, () => this.release(rec));
  }

  // routeMessage chains a message frame onto its session's promise chain.
  // A message for a session whose session_start the runtime never read is
  // answered at once with a RUNTIME_ERROR response; a message for a session
  // whose creation failed is answered the same way on the session's chain,
  // in order. A message for a session the runtime read a session_end for,
  // and that no later session_start reopened, is logged and dropped: after
  // the session_end the runtime writes no response addressed to the
  // session.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Session errors; Inbound: session_end
  // rule 2).
  private routeMessage(env: MessageEnvelope): void {
    const sessionId = env.sessionId ?? "";
    const rec = this.sessions.lookup(sessionId);
    if (rec) {
      this.chain(rec, () => (rec.endRead ? undefined : this.dispatch(rec, env)));
      return;
    }
    if (this.sessions.endedSince(sessionId)) {
      this.cfg.logger(
        `runtime: dropping message "${env.id}" for session "${sessionId}", which already ended`,
      );
      return;
    }
    this.cfg.logger(
      `runtime: message "${env.id}" for session "${sessionId}", which this runtime does not hold`,
    );
    void this.safeWrite(
      sessionErrorResponse(sessionId, `no session ${sessionId} is open on this runtime`),
    );
  }

  // chain appends step to the session's promise chain. A step that throws
  // is logged, and the chain continues.
  private chain(rec: SessionRecord, step: () => Promise<void> | void): void {
    rec.tail = rec.tail.then(step).catch((err: unknown) => {
      this.cfg.logger(`runtime: session ${rec.id}: ${errorMessage(err)}`);
    });
  }

  // create loads the session's credential bundle, invokes onCreate, and
  // writes the session's session_started, with error when either failed.
  // It then answers any duplicate session_start read meanwhile. A failure
  // is the session's own: the process keeps serving its other sessions.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Outbound: session_started rules 1 and 2,
  // Session errors), §15.7 (Handler).
  private async create(rec: SessionRecord): Promise<void> {
    const err = await this.createContext(rec);
    if (err) {
      this.cfg.logger(`runtime: session ${rec.id}: ${err.message}`);
    }
    rec.createError = err;
    await this.writeSessionStarted(rec, rec.startId, err);
    rec.createDone = true;
    const acks = rec.pendingAcks;
    rec.pendingAcks = [];
    for (const startId of acks) {
      await this.writeSessionStarted(rec, startId, err);
    }
  }

  // createContext reads the credential file the session_start names and
  // invokes onCreate with the session's CreateRequest. It resolves to the
  // failure, or undefined.
  private async createContext(rec: SessionRecord): Promise<Error | undefined> {
    try {
      rec.credentials = await loadCredentialBundle(rec.start.credentialsPath);
    } catch (err) {
      return err as Error;
    }
    const req: CreateRequest = {
      sessionId: rec.id,
      taskId: rec.id,
      runtimeOptions: this.manifest?.runtimeOptions,
      credentials: rec.credentials,
      experimentContext: rec.start.experimentContext,
      tracingContext: rec.start.tracingContext,
      llm: rec.start.llm,
      manifestSnapshot: this.manifest,
    };
    try {
      await this.handler.onCreate(req);
    } catch (err) {
      return new Error(`onCreate: ${errorMessage(err)}`);
    }
    return undefined;
  }

  // dispatch handles one queued message for the session.
  private async dispatch(rec: SessionRecord, env: MessageEnvelope): Promise<void> {
    if (rec.createError) {
      await this.writeFor(
        rec,
        sessionErrorResponse(rec.id, `the context of session ${rec.id} could not be created`),
      );
      return;
    }
    await this.handleMessage(rec, env);
  }

  // release runs onTerminate for the session and drops its record. It runs
  // on the session's chain after the in-flight handler settled, so
  // onTerminate never overlaps one of the session's own handler calls. It
  // runs for every record the SDK opened, whether its creation succeeded
  // or failed, so every session ends the same way.
  //
  // spec: §15.7 (Runtime Author SDKs), §28.5.3 (CH-MSGSOCK, Inbound:
  // session_end).
  private async release(rec: SessionRecord): Promise<void> {
    try {
      await this.handler.onTerminate(rec.id, rec.terminationReason());
    } catch (err) {
      this.cfg.logger(`runtime: session ${rec.id}: onTerminate error: ${errorMessage(err)}`);
    }
    rec.markReleased();
    this.sessions.forget(rec);
    this.unreleased.delete(rec);
  }

  // closeSessions chains the release of every live session with reason, on
  // EOF or shutdown. Each session dispatches the messages it already
  // queued and then runs onTerminate with reason.
  private closeSessions(reason: TerminationReason): void {
    for (const rec of this.sessions.liveRecords()) {
      rec.closeReason ??= reason;
      this.chain(rec, () => this.release(rec));
    }
  }

  // heldSession returns the live record a session-scoped CH-RUNTIMEOPS
  // event names, or undefined when the runtime does not hold the session
  // or failed to create its context. The caller drops the event without a
  // reply.
  //
  // spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
  private heldSession(sessionId: string): SessionRecord | undefined {
    const rec = this.sessions.lookup(sessionId);
    if (!rec || rec.createError) {
      return undefined;
    }
    return rec;
  }

  // writeSessionStarted writes the session_started frame answering the
  // session_start whose startId is startId. It is written even after the
  // session's session_end, because the frame answers a session_start read
  // before it.
  private async writeSessionStarted(
    rec: SessionRecord,
    startId: string,
    err: Error | undefined,
  ): Promise<void> {
    await this.safeWrite({
      type: "session_started",
      sessionId: rec.id,
      startId,
      ...(err ? { error: { code: "RUNTIME_ERROR", message: err.message } } : {}),
    });
  }

  // writeFor writes a frame addressed to rec, dropping it when the session
  // already ended. The check and the enqueue happen in one turn of the
  // event loop, so no frame for a session follows the frame loop's read of
  // its session_end.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
  private async writeFor(rec: SessionRecord, frame: Record<string, unknown>): Promise<void> {
    if (rec.ended) {
      this.cfg.logger(`runtime: session ${rec.id} ended; dropping its ${String(frame.type)} frame`);
      return;
    }
    await this.safeWrite(frame);
  }

  // handleMessage invokes onMessage for one of the session's messages and
  // writes the resulting response frame. It runs on the session's chain,
  // one message at a time. A handler rejection is reported as a structured
  // response error so the adapter records the failure without losing
  // context (§28.5.3 error reporting via response). A response for a
  // session that already ended is dropped.
  private async handleMessage(rec: SessionRecord, env: MessageEnvelope): Promise<void> {
    rec.sequence += 1;
    const msg: Message = {
      envelope: env,
      sessionId: rec.id,
      taskId: rec.id,
      sequence: rec.sequence,
    };
    const tools: HandlerTools = {
      adapter: new AdapterToolset(this.writer, this.registry, this.cfg.dialTimeoutMs, rec),
      platform: this.tools,
      credentials: rec.credentials,
    };

    let reply: Reply;
    try {
      reply = await this.handler.onMessage(msg, tools);
    } catch (err) {
      this.cfg.logger(`runtime: session ${rec.id}: onMessage error: ${errorMessage(err)}`);
      await this.writeFor(rec, sessionErrorResponse(rec.id, errorMessage(err)));
      return;
    }

    // A turn marked streaming and not final defers the response frame.
    // The §28.5.3 contract still requires a final response frame, so
    // the SDK emits it once the runtime returns a final Reply.
    if (reply.streaming && !reply.final) {
      return;
    }
    await this.writeFor(rec, {
      type: "response",
      output: stampParts(reply.parts ?? []),
      ...(reply.error ? { error: reply.error } : {}),
      // §28.5.3: every session-scoped frame carries the session it
      // addresses.
      sessionId: rec.id,
    });
  }

  // handleToolResult routes an inbound §28.5.3 tool_result frame to the
  // pending tool_call of the session its sessionId addresses. A result
  // whose id matches no pending call of that session is dropped and
  // logged.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: tool_result).
  private handleToolResult(tr: InboundToolResult): void {
    if (!this.registry.deliver(tr)) {
      this.cfg.logger(
        `runtime: tool_result "${tr.id}" for session "${tr.sessionId ?? ""}" has no pending tool_call in that session`,
      );
    }
  }

  // handleShutdown records the termination reason of a §28.5.3 shutdown
  // frame. Shutdown is process-scoped: run hands the reason to every live
  // session after the loop ends, and each session drains its queued
  // messages before its onTerminate.
  private handleShutdown(frame: Record<string, unknown>): void {
    this.exitReason = {
      reason: typeof frame.reason === "string" ? frame.reason : "shutdown",
      deadlineMs: typeof frame.deadline_ms === "number" ? frame.deadline_ms : 0,
    };
  }

  // safeWrite writes an outbound frame, logging a write error instead
  // of propagating it: a write failure usually means the adapter has
  // closed the transport.
  private async safeWrite(frame: unknown): Promise<void> {
    try {
      await this.writer.write(frame);
    } catch (err) {
      this.cfg.logger(`runtime: write frame: ${errorMessage(err)}`);
    }
  }

  // startChannels dials the §15.4.3 platform MCP server, connector MCP
  // servers, and CH-RUNTIMEOPS for the configured integration level, once
  // per process. When a higher-level channel is configured but the
  // manifest does not advertise it, the SDK logs the gap and degrades to
  // the level the manifest supports, so a Standard- or Full-level binary
  // still runs in a Basic-only environment.
  //
  // spec: §15.7 (Run dials the sockets once per process).
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
      if (!this.manifest?.runtimeOps?.socket) {
        this.cfg.logger(
          "runtime: the manifest advertises no CH-RUNTIMEOPS socket; lifecycle features disabled",
        );
      } else {
        this.lifecycle = await Lifecycle.dial(
          this.manifest,
          this.cfg.dialTimeoutMs,
          this.cfg.lifecycle,
          {
            heldSession: (sessionId) => this.heldSession(sessionId),
            reloadCredentials: (session, path) =>
              this.reloadCredentials(session as SessionRecord, path),
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

  // reloadCredentials re-reads the credential file a credentials_rotated
  // event names for the session and resolves to the bundle the session
  // holds afterwards. The event names the file the adapter just rewrote,
  // so a failed read is reported and the session keeps the bundle it
  // holds. An event carrying no path breaks the frame's contract; the
  // session keeps its bundle rather than reading a file the event did not
  // name.
  //
  // spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11 (item 4).
  private async reloadCredentials(
    rec: SessionRecord,
    path: string,
  ): Promise<CredentialBundle | undefined> {
    if (path === "") {
      this.cfg.logger(
        `runtime: credential rotation for session ${rec.id}: event carries no credentialsPath; keeping the bundle already held`,
      );
      return rec.credentials;
    }
    try {
      rec.credentials = await loadCredentialBundle(path);
    } catch (err) {
      this.cfg.logger(`runtime: credential rotation for session ${rec.id}: ${errorMessage(err)}`);
    }
    return rec.credentials;
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
        `runtime: no adapter manifest at ${this.cfg.manifestPath} (${errorMessage(err)})`,
      );
      return;
    }
    let m: AdapterManifest;
    try {
      m = JSON.parse(data) as AdapterManifest;
    } catch (err) {
      this.cfg.logger(
        `runtime: malformed adapter manifest ${this.cfg.manifestPath}: ${errorMessage(err)}`,
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

  // openTransport resolves the §28.5.3 transport. When explicit streams
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
