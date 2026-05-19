// SPDX-License-Identifier: MIT
// Command echo is a Basic-level Lenny agent runtime built on the
// TypeScript runtime-author SDK (@lennylabs/runtime-sdk). It echoes
// every inbound message back, prefixing text parts with a per-session
// sequence number.
//
// The handler implements the three §15.7 Handler methods. The SDK
// drives the §15.4.1 stdin/stdout protocol around it: it reads the
// inbound message frames, invokes onMessage, serializes the returned
// Reply into a response frame, answers heartbeats, and exits on
// shutdown. A runtime author writing a real agent replaces the body of
// onMessage with a model call and keeps the rest unchanged.
//
// Exit codes (spec §15.4): 0 success, 1 runtime error, 2 protocol
// error.
import { isProtocolError, run, text } from "../../src/index.js";
const EXIT_OK = 0;
const EXIT_RUNTIME_ERROR = 1;
const EXIT_PROTOCOL_ERROR = 2;
// echoHandler is a Basic-level Handler. It holds a per-process sequence
// counter so multi-message sessions can be correlated.
class EchoHandler {
    seq = 0;
    // onCreate has no task-scoped setup for an echo runtime.
    onCreate() { }
    // onMessage echoes the inbound parts. Text parts are prefixed with
    // the per-session sequence number; non-text parts are returned
    // unchanged.
    onMessage(msg) {
        this.seq += 1;
        const input = msg.envelope.input ?? [];
        const out = input.map((p) => p.type === "text" && p.inline
            ? text(`[echo seq=${this.seq}] ${p.inline}`)
            : p);
        return { parts: out, final: true };
    }
    // onTerminate has no teardown for an echo runtime.
    onTerminate() { }
}
run(new EchoHandler()).then(() => process.exit(EXIT_OK), (err) => {
    process.stderr.write(String(err) + "\n");
    process.exit(isProtocolError(err) ? EXIT_PROTOCOL_ERROR : EXIT_RUNTIME_ERROR);
});
//# sourceMappingURL=main.js.map