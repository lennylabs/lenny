// SPDX-License-Identifier: MIT
// This module is the §15.7 entry point of the TypeScript runtime-author
// SDK. run wires up the §15.4.1 stdin/stdout framing, optionally dials
// the manifest-advertised Unix sockets (platform MCP server, connector
// MCP servers, lifecycle channel) with the §15.4.3 manifest-nonce
// handshake, parses the §4.7 credential file, and drives the §15.4.2
// dispatch loop.
import { readFile } from "node:fs/promises";
import { Lifecycle } from "./lifecycle.js";
import { Tools } from "./mcp.js";
import { AdapterToolset, ToolCallRegistry } from "./tool.js";
import { FrameWriter, LineReader, dialUnixSocket } from "./transport.js";
import { ProtocolError, SCHEMA_VERSION } from "./types.js";
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
function resolveConfig(opts) {
    let level = opts.level ?? "basic";
    if (opts.lifecycle && level !== "full") {
        level = "full";
    }
    return {
        level,
        lifecycle: opts.lifecycle ?? {},
        manifestPath: opts.manifestPath ??
            process.env[MANIFEST_ENV_VAR] ??
            DEFAULT_MANIFEST_PATH,
        credentialsPath: opts.credentialsPath ?? DEFAULT_CREDENTIALS_PATH,
        socketTransport: opts.socketTransport ?? true,
        dialTimeoutMs: opts.dialTimeoutMs ?? 5000,
        input: opts.input,
        output: opts.output,
        logger: opts.logger ?? ((msg) => process.stderr.write(msg + "\n")),
    };
}
// levelRank orders the §15.4.3 integration levels for comparison.
function levelRank(level) {
    return level === "full" ? 2 : level === "standard" ? 1 : 0;
}
// stampParts sets schemaVersion on every part that left it unset,
// honoring the §15.4.1 producer obligation, and returns a non-empty
// array so an empty Reply still serializes as output: [].
function stampParts(parts) {
    return parts.map((p) => p.schemaVersion === undefined
        ? { ...p, schemaVersion: SCHEMA_VERSION }
        : p);
}
// run wires up the §15.4.1 stdin/stdout framing, dials the higher-level
// channels for the configured integration level, parses the §4.7
// credential file, and drives the §15.4.2 dispatch loop. It resolves
// when the adapter closes the inbound stream or sends a shutdown frame.
//
// run with no options covers the Basic level. Set level to "standard"
// or "full" to opt into the higher integration levels.
export async function run(handler, opts = {}) {
    if (!handler) {
        throw new Error("runtime: run requires a handler");
    }
    const session = new Session(handler, resolveConfig(opts));
    return session.run();
}
// Session holds the per-process SDK state for one run call.
class Session {
    handler;
    cfg;
    writer;
    manifest;
    credentials;
    tools;
    lifecycle;
    registry = new ToolCallRegistry();
    sequence = 0;
    terminated = false;
    exitReason;
    loopStopped = false;
    // dispatchTail serializes message handling so the §15.4.1
    // coordinator-local FIFO contract holds: handlers run one at a time
    // in the order the loop read their frames.
    dispatchTail = Promise.resolve();
    constructor(handler, cfg) {
        this.handler = handler;
        this.cfg = cfg;
    }
    // run drives one runtime lifecycle: resolve the transport, load the
    // manifest and credentials, dial the higher-level channels for the
    // configured level, then run the §15.4.1 frame loop.
    async run() {
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
        }
        catch (err) {
            this.closeChannels();
            transport.close();
            throw new Error(`runtime: onCreate: ${err.message}`);
        }
        let loopErr;
        try {
            await this.loop(transport.input);
        }
        catch (err) {
            loopErr = err;
        }
        // Wait for every queued message handler to write its response
        // frame before onTerminate.
        await this.dispatchTail;
        // onTerminate runs once on the way out. The reason is the shutdown
        // frame's reason when the loop exited on a shutdown; otherwise the
        // adapter closed the transport without one. A lifecycle terminate,
        // if it fired, already invoked onTerminate and this call is a
        // no-op.
        const reason = this.exitReason ?? {
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
    async loop(input) {
        const reader = new LineReader(input);
        for (;;) {
            if (this.loopStopped) {
                return;
            }
            let line;
            try {
                line = await reader.next();
            }
            catch (err) {
                throw new ProtocolError(`input read error: ${err.message}`);
            }
            if (line === null) {
                return;
            }
            if (line.length === 0) {
                continue;
            }
            let frame;
            try {
                frame = JSON.parse(line);
            }
            catch (err) {
                throw new ProtocolError(`malformed JSON Lines on input: ${err.message}`);
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
    enqueueMessage(line) {
        let env;
        try {
            env = JSON.parse(line);
        }
        catch (err) {
            this.cfg.logger(`runtime: malformed message envelope: ${err.message}`);
            return;
        }
        this.dispatchTail = this.dispatchTail.then(() => this.handleMessage(env));
    }
    // handleMessage invokes onMessage for one decoded §15.4.1 message and
    // writes the resulting response frame. A handler rejection is
    // reported as a structured response error so the adapter records the
    // failure without losing context (§15.4.1 error reporting via
    // response).
    async handleMessage(env) {
        this.sequence += 1;
        const msg = {
            envelope: env,
            sessionId: this.manifest?.sessionId ?? "",
            taskId: this.manifest?.taskId ?? "",
            sequence: this.sequence,
        };
        const tools = {
            adapter: new AdapterToolset(this.writer, this.registry, this.cfg.dialTimeoutMs, env.slotId),
            platform: this.tools,
            credentials: this.credentials,
        };
        let reply;
        try {
            reply = await this.handler.onMessage(msg, tools);
        }
        catch (err) {
            this.cfg.logger(`runtime: onMessage error: ${err.message}`);
            await this.safeWrite({
                type: "response",
                output: [],
                error: { code: "RUNTIME_ERROR", message: err.message },
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
    handleToolResult(line) {
        let tr;
        try {
            tr = JSON.parse(line);
        }
        catch (err) {
            this.cfg.logger(`runtime: malformed tool_result frame: ${err.message}`);
            return;
        }
        if (!this.registry.deliver(tr)) {
            this.cfg.logger(`runtime: tool_result "${tr.id}" has no pending tool_call`);
        }
    }
    // handleShutdown decodes the §15.4.1 shutdown frame and records the
    // termination reason for run to apply after draining in-flight
    // handlers.
    handleShutdown(line) {
        let sd;
        try {
            sd = JSON.parse(line);
        }
        catch {
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
    async safeWrite(frame) {
        try {
            await this.writer.write(frame);
        }
        catch (err) {
            this.cfg.logger(`runtime: write frame: ${err.message}`);
        }
    }
    // startChannels dials the §15.4.3 platform MCP server, connector MCP
    // servers, and lifecycle channel for the configured integration
    // level. When a higher-level channel is configured but the manifest
    // does not advertise it, the SDK logs the gap and degrades to the
    // level the manifest supports, so a Standard- or Full-level binary
    // still runs in a Basic-only environment.
    async startChannels() {
        if (levelRank(this.cfg.level) >= levelRank("standard")) {
            if (!this.manifest?.platformMcpServer?.socket) {
                this.cfg.logger("runtime: no platform MCP server in the manifest; degrading to Basic level");
            }
            else {
                this.tools = await Tools.dial(this.manifest, this.cfg.dialTimeoutMs);
            }
        }
        if (levelRank(this.cfg.level) >= levelRank("full")) {
            if (!this.manifest?.lifecycleChannel?.socket) {
                this.cfg.logger("runtime: no lifecycle channel in the manifest; lifecycle features disabled");
            }
            else {
                this.lifecycle = await Lifecycle.dial(this.manifest, this.cfg.dialTimeoutMs, this.cfg.lifecycle, {
                    stdoutWriter: this.writer,
                    reloadCredentials: () => {
                        void this.loadCredentials();
                        return this.credentials;
                    },
                    invokeTerminate: (reason, deadlineMs) => this.invokeTerminate(reason, deadlineMs),
                    stopFrameLoop: () => {
                        this.loopStopped = true;
                    },
                    log: (msg) => this.cfg.logger(`runtime: ${msg}`),
                });
            }
        }
    }
    // closeChannels releases the higher-level channels.
    closeChannels() {
        this.tools?.close();
        this.lifecycle?.close();
    }
    // invokeTerminate calls Handler.onTerminate at most once. The
    // terminated guard makes a lifecycle-channel terminate and the stdin
    // shutdown path idempotent.
    async invokeTerminate(reason, deadlineMs) {
        if (this.terminated) {
            return;
        }
        this.terminated = true;
        try {
            await this.handler.onTerminate({ reason, deadlineMs });
        }
        catch (err) {
            this.cfg.logger(`runtime: onTerminate error: ${err.message}`);
        }
    }
    // buildCreateRequest assembles the §15.7 onCreate snapshot from the
    // manifest and credential file.
    buildCreateRequest() {
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
    async loadManifest() {
        let data;
        try {
            data = await readFile(this.cfg.manifestPath, "utf8");
        }
        catch (err) {
            this.cfg.logger(`runtime: no adapter manifest at ${this.cfg.manifestPath} (${err.message})`);
            return;
        }
        let m;
        try {
            m = JSON.parse(data);
        }
        catch (err) {
            this.cfg.logger(`runtime: malformed adapter manifest ${this.cfg.manifestPath}: ${err.message}`);
            return;
        }
        if ((m.version ?? 0) > 1) {
            this.cfg.logger(`runtime: adapter manifest ${this.cfg.manifestPath} version ${m.version} is newer than supported (1)`);
            return;
        }
        this.manifest = m;
    }
    // loadCredentials parses the §4.7 runtime credential file. A missing
    // file is normal when the runtime's pool has no active lease.
    async loadCredentials() {
        let data;
        try {
            data = await readFile(this.cfg.credentialsPath, "utf8");
        }
        catch {
            return;
        }
        try {
            this.credentials = JSON.parse(data);
        }
        catch (err) {
            this.cfg.logger(`runtime: malformed credential file ${this.cfg.credentialsPath}: ${err.message}`);
        }
    }
    // openTransport resolves the §15.4.1 transport. When explicit streams
    // were supplied it uses them; when socket transport is enabled and
    // LENNY_ADAPTER_SOCKET names a socket it dials that socket; otherwise
    // it returns process.stdin / process.stdout.
    async openTransport() {
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
//# sourceMappingURL=runtime.js.map