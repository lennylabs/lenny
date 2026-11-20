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

Each step edits the numbered sections it lists while the page that holds their content is a placeholder, and writes into the page once an area change has written it, as `spec/conventions.md` states. The placeholder pages under `spec/capabilities/messaging/`, `spec/capabilities/delegation/`, `spec/capabilities/workspaces-and-files/`, `spec/capabilities/billing-and-usage/`, `spec/interfaces/runtime-protocol/`, and `spec/interfaces/client-api/` that name this change as a source of their content are those pages. A requirement written into a page registers under the page's code, and MDR-n is the design's label for it.

### 0135.S1 Delivery core
- status: Planned
- aliases: [MD-A, MD-1, MD-2, MD-3, MD-4, MD-5, MD-6, MD-7, MD-9 (part), MD-11 (part), MD-12 (part), MD-14]
- lanes: [specification, code, documentation]
- closes: [F-28.5.3.4, F-7.2.47, F-7.2.48, F-5.1.38, F-7.2.49]

The delivery core: MD-1 to MD-7, MD-9, MD-12, and MD-14, and the message rows of MD-11 (the `retract` and `retract_result` frames, the `retracted` state, routing, audit, and the SDK call), with the specification changes to §5.1, §7.2, §7.3, §8.5, §11.7, §15.1, §15.2, §15.4, §15.7, §16.1, §24, §28.5.3, and §29.3. It replaces the mechanisms of MDF-1 to MDF-5 under the owner's holistic plan, which closes their findings. It adds the `session_messages` sender and delivery-state columns of MD-3 **Records** with their migration, which SD-1 of [`0144-session-derive`](../0144-session-derive/change.md) reads.

SL-A of [`0146-session-lifecycle`](../0146-session-lifecycle/steps.md#steps) lands with this step, because the delivery paths name the states `idle`, `dormant`, `suspended`, and `blocked`: 0135.S1 and 0146.S1 share one branch, and both reshape the held-delivery seam operations. Which 0146 step this step and 0135.S5 bind to if 0146.S1 is split is open decision O25.

Until the later steps land, this step builds against the tree as follows. It names the end-of-turn frame and the output frame by their specification names, `response` and `output`, until 0135.S5 (MD-E) renames them `message` and `message_part`. It routes `consumed` and `retract` under the tree's rule for session-scoped frames until 0139.S1 lands RPR-18 ("MD-4"). It reads `injection.modes` and `injection.defaultMode` from the runtime record and the existing per-tenant capability override until 0140.S1 moves them to the revision ("MD-2"). It gates the `interrupt` intent on `integrationLevel: full`, with `interrupt_request` on `CH-RUNTIMEOPS`, until 0139.S1 converts both ("MD-2"). The receipt's `seq` lands with 0138.S1, which adds R1. How this step is ordered against 0145.S1 and 0145.S3 is open decision O6.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-SendMessages`, which `Deliver` replaces (MD-6), reshapes the held-delivery operations (`GWSEAM-LockDelivery`, `GWSEAM-HoldMessage`, `GWSEAM-HoldUndeliveredForward`, `GWSEAM-HoldsUndelivered`, `GWSEAM-DeliverHeld`, `GWSEAM-ResumeSuspended`, and `GWSEAM-AdmitResume`), `GWSEAM-EnqueueInbox`, and `GWSEAM-RouteMessage`, which chooses among those paths (MD-3 and MD-5), and reshapes `GWSEAM-AuthorizeSession` and `GWSEAM-SessionAccessible`, which applies the same ownership rule to a session record the platform tools seam read, with the predicate for the root's client that the delivery paths read.

### 0135.S2 Provider surfaces and client tools
- status: Planned
- aliases: [MD-B, MD-8, MD-10, MD-11 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0120.S1, 0122.S1]

Provider surfaces and client tools: MD-8 and MD-10, the client-call rows of MD-11, and the cold-history ordering rule. MD-8's surface rules move to the provider adapters under O8 of [`0138-rest-only-client-api`](../0138-rest-only-client-api/change.md); this step's scope, its edge to 0120.S1, and its order against the 0138 step that removes the translators are open decision O17.

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
- closes: [F-28.5.3.5, F-4.8.28, F-28.5.3.6, F-28.5.3.7, F-28.5.3.8, F-5.2.57, F-15.1.84]

Runtime reports during a turn: MD-15 to MD-21, the reference-adapter translation of MD-13, and the SDK calls of MD-12, with the specification changes to §4.6.1, §4.7, §4.8, §5.1, §5.2, §6.2, §7.1, §8.3, §8.5, §9, §10.1, §10.4, §11.2, §11.3, §12, §13, §15, §15.1, §15.4.6, §16.1, §17.8.1, §28.3, §28.4, §28.5.1, §28.5.3, §28.5.5, and §28.6. It replaces the mechanisms of MDF-6 to MDF-10, which closes their findings, lands the pool field and the §5.2 rewrite of MDF-13 (F-5.2.57), and registers or removes the `artifacts/{path}` route of MDF-11 (F-15.1.84), whose choice is open decision O19.

Until the later steps land, this step names the frames `response` and `output` by their specification names until 0135.S5 renames them, routes `activity`, `output_failed_ack`, and the `request` frames of MD-19 under the tree's rule for session-scoped frames until 0139.S1 lands RPR-18, and reads `capabilities.output.maxFileBytes`, `harnessApprovals`, and `maxWorkspaceSealDurationSeconds` from the runtime record until 0140.S1 moves them to the revision. Whether this step is split is open decision O9, and which change builds the export stream and its tables is open decision O10.

It needs the steps of the incremental checkpoint design that land IC-1 to IC-7, because file exports use its capture spool and reflink probe, chunks, packs, stream, catalog, download rendering, reclamation, and spool cap.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It reshapes the tool approvals (`GWSEAM-RegisterApproval`, `GWSEAM-CancelApproval`, `GWSEAM-ResolveApproval`, and `GWSEAM-AwaitApproval`) for harness approvals (MD-19). 0142.S6 of [`0142-security-oversight`](../0142-security-oversight/steps.md#steps) depends on this step for those reshaped operations and for the approval wait off the session's frame reader.

### 0135.S5 Parents and children and the names
- status: Planned
- aliases: [MD-E, MD-22, MD-23, MD-24, MD-25, MD-26, MD-9 (part), MD-19 (part)]
- lanes: [specification, code, documentation]
- depends-on: [0135.S1, 0135.S4, 0126.S1, 0146.S1]

Parents and children and the names: MD-22 to MD-26, the rules of MD-9 and MD-19 that revisions 9 and 10 add, the removal of `lenny/request_input` and the `siblings` scope, with the specification changes to §4.7, §5.1, §7.2, §8.2, §8.3, §8.5, §8.7, §8.8, §8.9, §9.1, §9.2, §15, §15.1, §15.2, §15.4.1, §15.4.3, §15.4.6, §21, §28.3, §28.4, §28.5.1, and §28.5.3.

It needs 0135.S4 for the frame changes and the output files that MD-26 imports, DA-A of [`0126-delegation-availability`](../0126-delegation-availability/steps.md#steps) for `capabilities.childSessions`, and SL-A of [`0146-session-lifecycle`](../0146-session-lifecycle/change.md) for the states. SL-B of that design follows this step and lands the resend of `ended` and `end_of_turn` child events after a parent's restore and the `turn_lost` and `idle_timeout` child events ("MD-22", **Recovery**). Which step owns `capabilities.childSessions` is open decision O22, the order of MD-26 against 0153.S5 is open decision O18, and how a part over the 4 MiB result bound reaches a parent's runtime is open decision O24.

The session of a frame (MD-24) points to 0139.S1, which lands the `sessionId` rule with RPR-18, and to 0119.S3 (AR-C), which lands the `startId` removal; this step builds neither.

Under decision 8 of [0117](../0117-gateway-package-layout/decisions.md), this step edits `spec/interfaces/gateway-seams.md` and `pkg/gateway/seam` for the gateway seam operations it changes. It removes `GWSEAM-PendingInputWaits`, `GWSEAM-ResolveInputWait`, and `GWSEAM-ForgetInputWaits` with `lenny/request_input`, reshapes the tool approvals for the rules of MD-19 this step carries, and adds child events into the parent's inbox (MD-22), `watch_child_session` (MD-25), and `import_child_session_file` (MD-26), crossings that have no operation yet. This step alone adds the operation for child events into the parent's inbox, which 0146.S2 then uses for the resend.
