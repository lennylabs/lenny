// SPDX-License-Identifier: MIT

// Command lifecycle is a Full-level Lenny agent runtime built on the
// TypeScript runtime-author SDK (@lennylabs/runtime-sdk).
//
// The runtime is started with level "full", so the SDK opens the
// §15.4.3 CH-RUNTIMEOPS and completes the lifecycle_capabilities /
// lifecycle_support handshake in addition to the Standard-level MCP
// setup. The SDK answers the checkpoint, interrupt,
// credential-rotation, and deadline events automatically, each routed
// to the session it names; the lifecycle callbacks below show where a
// real Full-level runtime quiesces a session's output, reaches a safe
// interrupt point, and rebinds a session's rotated credentials.
//
// The message handler is an echo naming the session and its experiment
// variant. The Full-level behavior lives in the CH-RUNTIMEOPS hooks
// rather than the per-turn path.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.

import { isProtocolError, run, text } from "../../src/index.js";
import type { CreateRequest, Handler, Message, Reply } from "../../src/index.js";

const EXIT_OK = 0;
const EXIT_RUNTIME_ERROR = 1;
const EXIT_PROTOCOL_ERROR = 2;

// NO_VARIANT is the variant the reply names for a session that is not
// enrolled in an experiment.
const NO_VARIANT = "none";

// LifecycleHandler is a Full-level Handler. The message path is an echo;
// the Full-level behavior lives in the lifecycle hooks passed to run.
class LifecycleHandler implements Handler {
  private readonly variants = new Map<string, string>();

  // onCreate records the session's experiment variant.
  onCreate(req: CreateRequest): void {
    this.variants.set(req.sessionId, req.experimentContext?.variantId || NO_VARIANT);
  }

  // onMessage echoes the inbound parts.
  onMessage(msg: Message): Reply {
    const variant = this.variants.get(msg.sessionId) ?? NO_VARIANT;
    const input = msg.envelope.input ?? [];
    const out = input.map((p) =>
      p.type === "text" && p.inline
        ? text(`[lifecycle seq=${msg.sequence} session=${msg.sessionId} variant=${variant}] ${p.inline}`)
        : p,
    );
    return { parts: out, final: true };
  }

  // onTerminate forgets the ended session's variant.
  onTerminate(sessionId: string): void {
    this.variants.delete(sessionId);
  }
}

run(new LifecycleHandler(), {
  level: "full",
  lifecycle: {
    // A checkpoint quiesces the named session's output before the SDK
    // replies checkpoint_ready. This echo runtime holds no streaming
    // state, so it is quiescent immediately.
    onCheckpoint(_sessionId: string, _checkpointId: string) {},
    // An interrupt brings the named session's work to a safe stop point
    // before the SDK replies interrupt_acknowledged.
    onInterrupt(_sessionId: string, _interruptId: string) {},
    // On rotation the SDK has already re-read the credential file the
    // event named into the session's bundle; a real runtime rebinds the
    // session's upstream client to the refreshed bundle here.
    onCredentialsRotated(_sessionId: string) {},
  },
}).then(
  () => process.exit(EXIT_OK),
  (err: unknown) => {
    process.stderr.write(String(err) + "\n");
    process.exit(isProtocolError(err) ? EXIT_PROTOCOL_ERROR : EXIT_RUNTIME_ERROR);
  },
);
