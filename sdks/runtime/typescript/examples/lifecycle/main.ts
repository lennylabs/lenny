// SPDX-License-Identifier: MIT

// Command lifecycle is a Full-level Lenny agent runtime built on the
// TypeScript runtime-author SDK (@lennylabs/runtime-sdk).
//
// The runtime is started with level "full", so the SDK opens the
// §15.4.3 lifecycle channel and completes the lifecycle_capabilities /
// lifecycle_support handshake in addition to the Standard-level MCP
// setup. The SDK answers the checkpoint, interrupt,
// credential-rotation, and deadline events automatically; the
// lifecycle callbacks below show where a real Full-level runtime
// quiesces output, reaches a safe interrupt point, and rebinds rotated
// credentials.
//
// The message handler is a plain echo. The Full-level behavior lives
// in the lifecycle channel rather than the per-turn path.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.

import { isProtocolError, run, text } from "../../src/index.js";
import type { Handler, Message, Reply } from "../../src/index.js";

const EXIT_OK = 0;
const EXIT_RUNTIME_ERROR = 1;
const EXIT_PROTOCOL_ERROR = 2;

// lifecycleHandler is a Full-level Handler. The message path is a plain
// echo; the Full-level behavior lives in the lifecycle hooks passed to
// run.
class LifecycleHandler implements Handler {
  private seq = 0;

  // onCreate has no task-scoped setup.
  onCreate(): void {}

  // onMessage echoes the inbound parts.
  onMessage(msg: Message): Reply {
    this.seq += 1;
    const input = msg.envelope.input ?? [];
    const out = input.map((p) =>
      p.type === "text" && p.inline
        ? text(`[lifecycle seq=${this.seq}] ${p.inline}`)
        : p,
    );
    return { parts: out, final: true };
  }

  // onTerminate has no teardown.
  onTerminate(): void {}
}

run(new LifecycleHandler(), {
  level: "full",
  lifecycle: {
    // A checkpoint quiesces output before the SDK replies
    // checkpoint_ready. This echo runtime holds no streaming state, so
    // it is quiescent immediately.
    onCheckpoint() {},
    // An interrupt brings the runtime to a safe stop point before the
    // SDK replies interrupt_acknowledged.
    onInterrupt() {},
    // On rotation the SDK has already re-read the credential file; a
    // real runtime rebinds its upstream client to the refreshed bundle
    // here.
    onCredentialsRotated() {},
  },
}).then(
  () => process.exit(EXIT_OK),
  (err: unknown) => {
    process.stderr.write(String(err) + "\n");
    process.exit(isProtocolError(err) ? EXIT_PROTOCOL_ERROR : EXIT_RUNTIME_ERROR);
  },
);
