// SPDX-License-Identifier: MIT

// Command delegate is a Standard-level Lenny agent runtime built on the
// TypeScript runtime-author SDK (@lennylabs/runtime-sdk). It shows the
// §8.5 delegation flow expressed through the SDK's typed platform MCP
// tool helpers.
//
// The runtime is started with level "standard", so the SDK reads the
// adapter manifest, dials the platform MCP server and every connector
// MCP server with the §15.4.3 manifest-nonce handshake, and exposes the
// platform tool surface through the tools argument of onMessage. On
// each inbound message the handler delegates a sub-task, awaits the
// child, confirms the result via request_input, emits the child output
// via lenny/output, and returns the child parts as the turn response.
//
// When no adapter manifest is present the SDK cannot reach Standard
// level; tools.platform is undefined and the handler degrades to a
// plain echo so the binary still runs in Basic-only test paths.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.

import { isProtocolError, run, text } from "../../src/index.js";
import type {
  Handler,
  HandlerTools,
  Message,
  OutputPart,
  Reply,
} from "../../src/index.js";

const EXIT_OK = 0;
const EXIT_RUNTIME_ERROR = 1;
const EXIT_PROTOCOL_ERROR = 2;

// echoParts prefixes text parts with the per-session sequence number.
function echoParts(input: OutputPart[], seq: number): OutputPart[] {
  return input.map((p) =>
    p.type === "text" && p.inline
      ? text(`[delegate seq=${seq}] ${p.inline}`)
      : p,
  );
}

// delegationError builds a final Reply carrying a structured error so
// the adapter records the failure without losing context (§15.4.1).
function delegationError(err: unknown): Reply {
  return {
    error: {
      code: "DELEGATION_FAILED",
      message: err instanceof Error ? err.message : String(err),
    },
    final: true,
  };
}

// delegateHandler is a Standard-level Handler.
class DelegateHandler implements Handler {
  private seq = 0;

  // onCreate has no task-scoped setup. The SDK has already dialed the
  // platform MCP server and connector MCP servers by the time onCreate
  // runs.
  onCreate(): void {}

  // onMessage runs the §8.5 delegation flow through the SDK platform
  // tool helpers. Without a platform MCP server it echoes the input.
  async onMessage(msg: Message, tools: HandlerTools): Promise<Reply> {
    this.seq += 1;
    const input = msg.envelope.input ?? [];
    const platform = tools.platform;
    if (!platform) {
      // Basic-level fallback: no platform MCP server in the manifest.
      return { parts: echoParts(input, this.seq), final: true };
    }

    try {
      // 1. lenny/delegate_task — spawn a child whose input is this
      //    message's input parts.
      const handle = await platform.delegateTask(
        "delegate-child",
        echoParts(input, this.seq),
      );

      // 2. lenny/await_children — wait for the child to settle.
      const results = await platform.awaitChildren([handle.taskId], "all");
      if (results.length === 0 || results[0].state !== "completed") {
        return delegationError(new Error("child did not complete"));
      }
      const childParts = results[0].output.parts ?? [];

      // 3. lenny/request_input — confirm the echoed result.
      await platform.requestInput([
        text(`confirm echo of child ${handle.taskId}?`),
      ]);

      // 4. lenny/output — emit the child output to the parent or
      //    client. The response below still signals turn completion
      //    (§15.4.1).
      await platform.output(childParts);
      return { parts: childParts, final: true };
    } catch (err) {
      return delegationError(err);
    }
  }

  // onTerminate has no teardown.
  onTerminate(): void {}
}

run(new DelegateHandler(), { level: "standard" }).then(
  () => process.exit(EXIT_OK),
  (err: unknown) => {
    process.stderr.write(String(err) + "\n");
    process.exit(isProtocolError(err) ? EXIT_PROTOCOL_ERROR : EXIT_RUNTIME_ERROR);
  },
);
