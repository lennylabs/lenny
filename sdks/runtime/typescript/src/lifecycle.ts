// SPDX-License-Identifier: MIT

// This module implements the §15.4.3 Full-level CH-RUNTIMEOPS. The
// SDK answers the protocol-level handshake and the checkpoint,
// interrupt, credential-rotation, and deadline events automatically; a
// runtime that needs to react registers callbacks through
// LifecycleHooks.

import type { Duplex } from "node:stream";
import { FrameWriter, LineReader, dialAuthenticated } from "./transport.js";
import type { AdapterManifest, CredentialBundle } from "./types.js";

// LIFECYCLE_CAPABILITIES is the §15.4.3 / §15.4.6 set of Full-level
// lifecycle events the SDK handles on the runtime's behalf. It is the
// payload of the lifecycle_support handshake reply.
const LIFECYCLE_CAPABILITIES = [
  "checkpoint",
  "interrupt",
  "credential_rotation",
  "deadline_signal",
];

// SESSION_SCOPED_EVENTS are the adapter-to-runtime CH-RUNTIMEOPS frames
// that name a session. The SDK hands each to the session it names and
// drops, without a reply, one naming a session the runtime does not hold or
// whose context it failed to create. The adapter writes them only after it
// reads the session's session_started, so the SDK keeps no queue of early
// events.
//
// spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
const SESSION_SCOPED_EVENTS = new Set([
  "checkpoint_request",
  "checkpoint_complete",
  "interrupt_request",
  "credentials_rotated",
  "deadline_approaching",
  "deadline_signal",
  "files_updated",
]);

// LifecycleEvent is a decoded CH-RUNTIMEOPS frame handed to a runtime
// callback. sessionId names the session a session-scoped event concerns.
// raw carries the full frame for fields the typed callbacks do not cover.
export interface LifecycleEvent {
  type: string;
  sessionId: string;
  raw: Record<string, unknown>;
}

// LifecycleHooks holds the optional runtime callbacks for lifecycle
// events. Each callback receives the session the event names. An
// undefined hook means the SDK answers with the default behavior.
export interface LifecycleHooks {
  // onCheckpoint runs on a §15.4.3 checkpoint_request before the SDK
  // replies checkpoint_ready. The callback quiesces the named session's
  // output.
  onCheckpoint?(sessionId: string, checkpointId: string): Promise<void> | void;
  // onInterrupt runs on a §15.4.3 interrupt_request before the SDK
  // replies interrupt_acknowledged. The callback brings the named
  // session's work to a safe stop point.
  onInterrupt?(sessionId: string, interruptId: string): Promise<void> | void;
  // onCredentialsRotated runs after the SDK re-reads the credential file a
  // credentials_rotated event names for the named session, with the
  // session's refreshed bundle.
  onCredentialsRotated?(
    sessionId: string,
    creds: CredentialBundle | undefined,
  ): void;
  // onDeadline runs on a §15.4.3 deadline_approaching or deadline_signal
  // event for a session the runtime holds.
  onDeadline?(event: LifecycleEvent): void;
}

// LifecycleHost is the subset of the SDK process the CH-RUNTIMEOPS
// reaches. heldSession returns the live session a session-scoped event
// names, or undefined when the runtime does not hold it or failed to
// create its context. reloadCredentials re-reads the file an event names
// into that session's bundle and resolves to the bundle the session holds
// afterwards. No CH-RUNTIMEOPS frame ends the process: a session ends on
// the CH-MSGSOCK session_end and the process on shutdown or stdin EOF
// (spec: §4.7.10, Runtime process lifetime).
export interface LifecycleHost {
  heldSession(sessionId: string): unknown;
  reloadCredentials(
    session: unknown,
    path: string,
  ): Promise<CredentialBundle | undefined>;
  log(msg: string): void;
}

// Lifecycle is the §15.4.3 Full-level CH-RUNTIMEOPS surface. The
// channel is constructed only when the runtime runs at Full level and
// the manifest advertised a lifecycle socket.
export class Lifecycle {
  private closed = false;

  private constructor(
    private readonly conn: Duplex,
    private readonly writer: FrameWriter,
    private readonly reader: LineReader,
    private readonly hooks: LifecycleHooks,
    private readonly host: LifecycleHost,
  ) {}

  // dial opens the §15.4.3 CH-RUNTIMEOPS: it dials the
  // manifest-advertised socket, completes the lifecycle_capabilities /
  // lifecycle_support handshake, and starts the event loop.
  static async dial(
    manifest: AdapterManifest,
    manifestPath: string,
    timeoutMs: number,
    hooks: LifecycleHooks,
    host: LifecycleHost,
  ): Promise<Lifecycle> {
    if (!manifest.runtimeOps?.socket) {
      throw new Error("adapter manifest has no CH-RUNTIMEOPS socket");
    }
    // The runtime connection handshake reads the nonce from manifestPath
    // before each dial and redial. spec: §4.7.11 (Runtime connection
    // handshake).
    const conn = await dialAuthenticated(
      manifest.runtimeOps.socket,
      manifestPath,
      timeoutMs,
    );
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
    let caps: { type?: string };
    try {
      caps = JSON.parse(first) as { type?: string };
    } catch (err) {
      conn.destroy();
      throw new Error(
        `lifecycle handshake: frame not JSON: ${(err as Error).message}`,
      );
    }
    if (caps.type !== "lifecycle_capabilities") {
      conn.destroy();
      throw new Error(
        `lifecycle handshake: expected lifecycle_capabilities, got ${first}`,
      );
    }
    await writer.write({
      type: "lifecycle_support",
      capabilities: LIFECYCLE_CAPABILITIES,
    });

    const lc = new Lifecycle(conn, writer, reader, hooks, host);
    void lc.loop();
    return lc;
  }

  // loop processes inbound CH-RUNTIMEOPS frames until the
  // connection closes.
  private async loop(): Promise<void> {
    for (;;) {
      let line: string | null;
      try {
        line = await this.reader.next();
      } catch (err) {
        if (!this.closed) {
          this.host.log(`lifecycle read error: ${(err as Error).message}`);
        }
        return;
      }
      if (line === null) {
        return;
      }
      let frame: Record<string, unknown>;
      try {
        frame = JSON.parse(line) as Record<string, unknown>;
      } catch (err) {
        this.host.log(`malformed lifecycle frame: ${(err as Error).message}`);
        continue;
      }
      const kind = typeof frame.type === "string" ? frame.type : "";
      if (!SESSION_SCOPED_EVENTS.has(kind)) {
        this.host.log(`ignoring unknown lifecycle event "${kind}"`);
        continue;
      }
      await this.route(kind, frame);
    }
  }

  // route hands a session-scoped event to the session it names, or drops
  // it without a reply when the runtime does not hold that session.
  //
  // spec: §28.5.3 (CH-RUNTIMEOPS, Messages).
  private async route(
    kind: string,
    frame: Record<string, unknown>,
  ): Promise<void> {
    const sessionId =
      typeof frame.sessionId === "string" ? frame.sessionId : "";
    const session = this.host.heldSession(sessionId);
    if (session === undefined) {
      this.host.log(
        `dropping lifecycle event "${kind}" for session "${sessionId}", which this runtime does not hold`,
      );
      return;
    }
    switch (kind) {
      case "checkpoint_request":
        await this.handleCheckpoint(sessionId, frame);
        break;
      case "interrupt_request":
        await this.handleInterrupt(sessionId, frame);
        break;
      case "credentials_rotated":
        await this.handleCredentialsRotated(session, sessionId, frame);
        break;
      case "deadline_approaching":
      case "deadline_signal":
        this.handleDeadline({ type: kind, sessionId, raw: frame });
        break;
      default:
      // checkpoint_complete and files_updated need no reply.
    }
  }

  // handleCheckpoint answers a §15.4.3 checkpoint_request: it runs the
  // runtime quiesce callback for the named session and replies
  // checkpoint_ready.
  private async handleCheckpoint(
    sessionId: string,
    frame: Record<string, unknown>,
  ): Promise<void> {
    const checkpointId =
      typeof frame.checkpointId === "string" ? frame.checkpointId : "";
    if (this.hooks.onCheckpoint) {
      try {
        await this.hooks.onCheckpoint(sessionId, checkpointId);
      } catch (err) {
        this.host.log(`onCheckpoint callback error: ${(err as Error).message}`);
      }
    }
    await this.writer.write({ type: "checkpoint_ready", checkpointId });
  }

  // handleInterrupt answers a §15.4.3 interrupt_request: it runs the
  // runtime safe-stop callback for the named session and replies
  // interrupt_acknowledged.
  private async handleInterrupt(
    sessionId: string,
    frame: Record<string, unknown>,
  ): Promise<void> {
    const interruptId =
      typeof frame.interruptId === "string" ? frame.interruptId : "";
    if (this.hooks.onInterrupt) {
      try {
        await this.hooks.onInterrupt(sessionId, interruptId);
      } catch (err) {
        this.host.log(`onInterrupt callback error: ${(err as Error).message}`);
      }
    }
    await this.writer.write({ type: "interrupt_acknowledged", interruptId });
  }

  // handleCredentialsRotated answers a §15.4.3 credentials_rotated event:
  // it re-reads the credential file the event names into the named
  // session's bundle, runs the runtime rotation callback, and replies
  // credentials_acknowledged. The session comes from the frame's sessionId
  // rather than from parsing credentialsPath, whose root is
  // operator-configurable.
  //
  // spec: §28.5.3 (CH-RUNTIMEOPS, credentials_rotated), §4.7.11 (item 4).
  private async handleCredentialsRotated(
    session: unknown,
    sessionId: string,
    frame: Record<string, unknown>,
  ): Promise<void> {
    const leaseId = typeof frame.leaseId === "string" ? frame.leaseId : "";
    const provider =
      typeof frame.provider === "string" ? frame.provider : "";
    const path =
      typeof frame.credentialsPath === "string" ? frame.credentialsPath : "";
    const creds = await this.host.reloadCredentials(session, path);
    if (this.hooks.onCredentialsRotated) {
      try {
        this.hooks.onCredentialsRotated(sessionId, creds);
      } catch (err) {
        this.host.log(
          `onCredentialsRotated callback error: ${(err as Error).message}`,
        );
      }
    }
    await this.writer.write({
      type: "credentials_acknowledged",
      leaseId,
      provider,
    });
  }

  // handleDeadline runs the runtime deadline callback for a §15.4.3
  // deadline_approaching or deadline_signal event.
  private handleDeadline(event: LifecycleEvent): void {
    if (this.hooks.onDeadline) {
      this.hooks.onDeadline(event);
    } else {
      this.host.log(`lifecycle ${event.type}`);
    }
  }

  // send writes an arbitrary frame on the CH-RUNTIMEOPS. It is the escape
  // hatch for lifecycle messages the SDK does not model. It applies no
  // per-session filter, because a runtime may still report a late
  // llm_request_completed for a request that started before the session's
  // session_end.
  //
  // spec: §28.5.3 (CH-MSGSOCK, Inbound: session_end rule 2).
  send(frame: unknown): Promise<void> {
    if (this.closed) {
      return Promise.reject(new Error("CH-RUNTIMEOPS closed"));
    }
    return this.writer.write(frame);
  }

  // close releases the CH-RUNTIMEOPS connection.
  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.conn.destroy();
  }
}
