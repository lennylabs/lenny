# Steps: 0135 Message delivery

## Validation owed

- Whether Claude Code run with `--input-format stream-json` honours `priority` on each `SDKUserMessage` written to stdin, and how it places a folded message after `tool_result` blocks. The documentation states the placement for the interactive mode.
- Whether the Python Agent SDK documents `priority`, which the Python pages did not state.
- A steering interface for `cursor-agent` run headless.
- Whether Gemini CLI's model steering leaves its experimental status, and how a hint travels with `functionResponse` parts in a headless run.
- Mid-run input in Mastra, the OpenAI Assistants API, and CrewAI.
- Whether OpenAI's Responses API states an adjacency rule for function call outputs, which the investigation found only for Chat Completions through a third-party error page.
- Export throughput of a single multi-gigabyte file on runc, gVisor, and Kata, which the checkpoint benchmarks measured for workspaces of many files and not for one large file.
- The name of the task status-message field under MCP `2026-07-28` with Tasks.
- The Claude Code flags `--include-partial-messages`, `--include-hook-events`, and `--forward-subagent-text` in the version the reference runtime pins.
- How AG-UI carries a file or binary content part, which the pages read did not state.
- How Claude Code shows its model a sub-agent's notice and the sub-agent's id, which revision 9 describes from the agent system it runs on and not from a documentation page.
- Whether each reference harness can show its model a message's sender when the adapter writes a child event to it, which decides whether its runtime declares `capabilities.childSessions`.
- Settled on 2026-11-12: the freeze of "MD-18" opens files through the slot's file-descriptor broker, whose `openat2` resolution with `RESOLVE_BENEATH`, `RESOLVE_NO_XDEV`, `RESOLVE_NO_SYMLINKS`, and `RESOLVE_NO_MAGICLINKS` worked under runc and gVisor in the session isolation spikes and under Kata in the AWS runs of that day ([`0145-session-isolation`, "Validation record"](../0145-session-isolation/steps.md#validation-record), [`0131-incremental-checkpoint`](../0131-incremental-checkpoint/change.md)).
- Whether the identifier `CH-FILEIMPORT` meets the naming law's rule that no two channels on one boundary share a stem, beside `CH-OUTPUTFILE`.
- Whether each reference harness can take a watched child's stream and show it without passing it to its model.
- How Claude Code returns a sub-agent's last message as its result, which the delivery rules cite, and whether its harness receives the sub-agent's whole stream.

## Steps

### 0135.S1 Delivery core
- status: Planned
- aliases: [MD-A, MD-1, MD-2, MD-3, MD-4, MD-5, MD-6, MD-7, MD-9 (part), MD-11 (part), MD-12 (part), MD-14]
- lanes: [specification, code, documentation]

The delivery core: MD-1 to MD-7, MD-9, MD-12, and MD-14, and the message rows of MD-11 (the `retract` and `retract_result` frames, the `retracted` state, routing, audit, and the SDK call), with the specification changes to §5.1, §7.2, §7.3, §8.5, §11.7, §15.1, §15.2, §15.4, §15.7, §16.1, §24, §28.5.3, and §29.3. It replaces the mechanisms of MDF-1 to MDF-5 under the owner's holistic plan.

SL-A of [`0146-session-lifecycle`](../0146-session-lifecycle/steps.md#steps) lands with this step, because the delivery paths name the states `idle`, `dormant`, `suspended`, and `blocked`.

### 0135.S2 Provider surfaces and client tools
- status: Planned
- aliases: [MD-B, MD-8, MD-10, MD-11 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0120.S1, 0122.S1]

Provider surfaces and client tools: MD-8 and MD-10, the client-call rows of MD-11, and the cold-history ordering rule.

### 0135.S3 Reference runtimes
- status: Planned
- aliases: [MD-C, MD-13 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0135.S4]

Reference runtimes: MD-13, with the adapter changes in each runtime's repository. It needs 0135.S4 for the translation of activity, output, and approvals.

### 0135.S4 Runtime reports during a turn
- status: Planned
- aliases: [MD-D, MD-15, MD-16, MD-17, MD-18, MD-19 (part), MD-20, MD-21, MD-12 (part), MD-13 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0131.S1, 0131.S2, 0131.S3, 0131.S4, 0131.S5, 0131.S6, 0131.S7]

Runtime reports during a turn: MD-15 to MD-21, the reference-adapter translation of MD-13, and the SDK calls of MD-12, with the specification changes to §4.6.1, §4.7, §4.8, §5.1, §5.2, §6.2, §7.1, §8.3, §8.5, §9, §10.1, §10.4, §11.2, §11.3, §12, §13, §15, §15.1, §15.4.6, §16.1, §17.8.1, §28.3, §28.4, §28.5.1, §28.5.3, §28.5.5, and §28.6. It replaces the mechanisms of MDF-6 to MDF-10.

It needs the steps of the incremental checkpoint design that land IC-1 to IC-7, because file exports use its capture spool and reflink probe, chunks, packs, stream, catalog, download rendering, reclamation, and spool cap.

### 0135.S5 Parents and children and the names
- status: Planned
- aliases: [MD-E, MD-22, MD-23, MD-24, MD-25, MD-26, MD-9 (part), MD-19 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0135.S4, 0126.S1, 0146.S1]

Parents and children and the names: MD-22 to MD-26, the rules of MD-9 and MD-19 that revisions 9 and 10 add, the removal of `lenny/request_input` and the `siblings` scope, with the specification changes to §4.7, §5.1, §7.2, §8.2, §8.3, §8.5, §8.7, §8.8, §8.9, §9.1, §9.2, §15, §15.1, §15.2, §15.4.1, §15.4.3, §15.4.6, §21, §28.3, §28.4, §28.5.1, and §28.5.3.

It needs 0135.S4 for the frame changes and the output files that MD-26 imports, DA-A of [`0126-delegation-availability`](../0126-delegation-availability/steps.md#steps) for `capabilities.childSessions`, and SL-A of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) for the states. SL-B of that design follows this step.
