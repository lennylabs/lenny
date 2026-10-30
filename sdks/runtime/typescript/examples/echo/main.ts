// SPDX-License-Identifier: MIT

// Command echo is a Basic-level Lenny agent runtime built on the
// TypeScript runtime-author SDK (@lennylabs/runtime-sdk). It echoes
// every inbound message back, prefixing text parts with the session's
// message sequence number, the session's identifier, and the session's
// experiment variant.
//
// The handler implements the three §15.7 Handler methods. The SDK
// drives the §28.5.3 stdin/stdout protocol around it: it opens a session
// on each session_start, invokes onMessage for the session's messages,
// serializes the returned Reply into a response frame, answers
// heartbeats, and ends the session on session_end. One process serves
// every session the pod holds, so the handler keeps the context each
// session's onCreate delivered keyed by session. A runtime author
// writing a real agent replaces the body of onMessage with a model call
// and keeps the rest unchanged.
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

// EchoHandler is a Basic-level Handler. It records each live session's
// experiment variant, from that session's onCreate, so a reply names the
// context of the session it answers.
class EchoHandler implements Handler {
  private readonly variants = new Map<string, string>();

  // onCreate records the session's experiment variant.
  onCreate(req: CreateRequest): void {
    this.variants.set(req.sessionId, req.experimentContext?.variantId || NO_VARIANT);
  }

  // onMessage echoes the inbound parts. Text parts are prefixed with the
  // session's message sequence number, identifier, and experiment
  // variant; non-text parts are returned unchanged.
  onMessage(msg: Message): Reply {
    const variant = this.variants.get(msg.sessionId) ?? NO_VARIANT;
    const input = msg.envelope.input ?? [];
    const out = input.map((p) =>
      p.type === "text" && p.inline
        ? text(`[echo seq=${msg.sequence} session=${msg.sessionId} variant=${variant}] ${p.inline}`)
        : p,
    );
    return { parts: out, final: true };
  }

  // onTerminate forgets the ended session's variant.
  onTerminate(sessionId: string): void {
    this.variants.delete(sessionId);
  }
}

run(new EchoHandler()).then(
  () => process.exit(EXIT_OK),
  (err: unknown) => {
    process.stderr.write(String(err) + "\n");
    process.exit(isProtocolError(err) ? EXIT_PROTOCOL_ERROR : EXIT_RUNTIME_ERROR);
  },
);
