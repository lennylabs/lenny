// SPDX-License-Identifier: MIT
import { FrameWriter, LineReader, dialUnixSocket } from "./transport.js";
// LIFECYCLE_CAPABILITIES is the §15.4.3 / §15.4.6 set of Full-level
// lifecycle events the SDK handles on the runtime's behalf. It is the
// payload of the lifecycle_support handshake reply.
const LIFECYCLE_CAPABILITIES = [
    "checkpoint",
    "interrupt",
    "credential_rotation",
    "deadline_signal",
];
// Lifecycle is the §15.4.3 Full-level lifecycle channel surface. The
// channel is constructed only when the runtime runs at Full level and
// the manifest advertised a lifecycle socket.
export class Lifecycle {
    conn;
    writer;
    reader;
    hooks;
    host;
    closed = false;
    constructor(conn, writer, reader, hooks, host) {
        this.conn = conn;
        this.writer = writer;
        this.reader = reader;
        this.hooks = hooks;
        this.host = host;
    }
    // dial opens the §15.4.3 lifecycle channel: it dials the
    // manifest-advertised socket, completes the lifecycle_capabilities /
    // lifecycle_support handshake, and starts the event loop.
    static async dial(manifest, timeoutMs, hooks, host) {
        if (!manifest.lifecycleChannel?.socket) {
            throw new Error("adapter manifest has no lifecycle channel socket");
        }
        const conn = await dialUnixSocket(manifest.lifecycleChannel.socket, timeoutMs);
        const writer = new FrameWriter(conn);
        const reader = new LineReader(conn);
        // §15.4.3 handshake: the adapter sends lifecycle_capabilities; the
        // runtime replies with lifecycle_support naming the events it
        // implements. Anything else on the first frame is a handshake
        // failure.
        const first = await reader.next();
        if (first === null) {
            conn.destroy();
            throw new Error("lifecycle handshake: connection closed before frame");
        }
        let caps;
        try {
            caps = JSON.parse(first);
        }
        catch (err) {
            conn.destroy();
            throw new Error(`lifecycle handshake: frame not JSON: ${err.message}`);
        }
        if (caps.type !== "lifecycle_capabilities") {
            conn.destroy();
            throw new Error(`lifecycle handshake: expected lifecycle_capabilities, got ${first}`);
        }
        await writer.write({
            type: "lifecycle_support",
            capabilities: LIFECYCLE_CAPABILITIES,
        });
        const lc = new Lifecycle(conn, writer, reader, hooks, host);
        void lc.loop();
        return lc;
    }
    // loop processes inbound lifecycle-channel frames until the
    // connection closes or the adapter sends terminate.
    async loop() {
        for (;;) {
            let line;
            try {
                line = await this.reader.next();
            }
            catch (err) {
                if (!this.closed) {
                    this.host.log(`lifecycle read error: ${err.message}`);
                }
                return;
            }
            if (line === null) {
                return;
            }
            let frame;
            try {
                frame = JSON.parse(line);
            }
            catch (err) {
                this.host.log(`malformed lifecycle frame: ${err.message}`);
                continue;
            }
            const kind = typeof frame.type === "string" ? frame.type : "";
            switch (kind) {
                case "checkpoint_request":
                    await this.handleCheckpoint(frame);
                    break;
                case "interrupt_request":
                    await this.handleInterrupt(frame);
                    break;
                case "credentials_rotated":
                    await this.handleCredentialsRotated(frame);
                    break;
                case "deadline_approaching":
                case "deadline_signal":
                    this.handleDeadline({ type: kind, raw: frame });
                    break;
                case "terminate":
                    await this.handleTerminate(frame);
                    return;
                default:
                    this.host.log(`ignoring unknown lifecycle event "${kind}"`);
            }
        }
    }
    // handleCheckpoint answers a §15.4.3 checkpoint_request: it runs the
    // runtime quiesce callback and replies checkpoint_ready.
    async handleCheckpoint(frame) {
        const checkpointId = typeof frame.checkpointId === "string" ? frame.checkpointId : "";
        if (this.hooks.onCheckpoint) {
            try {
                await this.hooks.onCheckpoint(checkpointId);
            }
            catch (err) {
                this.host.log(`onCheckpoint callback error: ${err.message}`);
            }
        }
        await this.writer.write({ type: "checkpoint_ready", checkpointId });
    }
    // handleInterrupt answers a §15.4.3 interrupt_request: it runs the
    // runtime safe-stop callback and replies interrupt_acknowledged.
    async handleInterrupt(frame) {
        const interruptId = typeof frame.interruptId === "string" ? frame.interruptId : "";
        if (this.hooks.onInterrupt) {
            try {
                await this.hooks.onInterrupt(interruptId);
            }
            catch (err) {
                this.host.log(`onInterrupt callback error: ${err.message}`);
            }
        }
        await this.writer.write({ type: "interrupt_acknowledged", interruptId });
    }
    // handleCredentialsRotated answers a §15.4.3 credentials_rotated
    // event: it re-reads the §4.7 credential file in place, runs the
    // runtime rotation callback, and replies credentials_acknowledged.
    async handleCredentialsRotated(frame) {
        const leaseId = typeof frame.leaseId === "string" ? frame.leaseId : "";
        const provider = typeof frame.provider === "string" ? frame.provider : "";
        const creds = this.host.reloadCredentials();
        if (this.hooks.onCredentialsRotated) {
            this.hooks.onCredentialsRotated(creds);
        }
        await this.writer.write({
            type: "credentials_acknowledged",
            leaseId,
            provider,
        });
    }
    // handleDeadline runs the runtime deadline callback for a §15.4.3
    // deadline_approaching or deadline_signal event.
    handleDeadline(event) {
        if (this.hooks.onDeadline) {
            this.hooks.onDeadline(event);
        }
        else {
            this.host.log(`lifecycle ${event.type}`);
        }
    }
    // handleTerminate answers a §15.4.3 terminate event: it emits a final
    // §15.4.1 response frame on stdout (carrying a DEADLINE_EXCEEDED
    // error, per the §15.4.6 deadline-signal expectation), invokes
    // onTerminate, and stops the frame loop so the runtime exits.
    async handleTerminate(frame) {
        const reasonRaw = typeof frame.reason === "string" ? frame.reason : "";
        const reason = reasonRaw === "" ? "lifecycle_terminate" : reasonRaw;
        const deadlineMs = typeof frame.deadlineMs === "number" ? frame.deadlineMs : 0;
        // §15.4.6 deadline-signal handling: the runtime writes a final
        // response on the stdout protocol channel before it exits.
        try {
            await this.host.stdoutWriter.write({
                type: "response",
                output: [],
                error: { code: "DEADLINE_EXCEEDED", message: reason },
            });
        }
        catch (err) {
            this.host.log(`write terminate response: ${err.message}`);
        }
        await this.host.invokeTerminate(reason, deadlineMs);
        this.host.stopFrameLoop();
    }
    // send writes an arbitrary frame on the lifecycle channel. It is the
    // escape hatch for lifecycle messages the SDK does not model.
    send(frame) {
        if (this.closed) {
            return Promise.reject(new Error("lifecycle channel closed"));
        }
        return this.writer.write(frame);
    }
    // close releases the lifecycle-channel connection.
    close() {
        if (this.closed) {
            return;
        }
        this.closed = true;
        this.conn.destroy();
    }
}
//# sourceMappingURL=lifecycle.js.map