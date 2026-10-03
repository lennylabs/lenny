# Summary: One runtime lifetime contract: session_start and session_end on CH-MSGSOCK and multi-session runtime SDKs

## Summary

**Problem statement.** The platform keeps one `type: agent` runtime process across sessions, both on concurrent pools and, since 0079, on recycling pools. The Go, Python, and TypeScript runtime SDKs nevertheless load one session's context when the process starts and invoke the create handler once. A kept runtime therefore serves every later session under the context it loaded at startup, which is usually empty on sidecar pods. A user-scoped credential lease can cross users inside a tenant, platform and connector MCP stop working for later sessions, and recycling and concurrent pools are unsafe with any SDK-built runtime. The spec still describes one session per process in several places, the adapter sends no session-boundary signal, and the runtime side of the `CH-MSGSOCK` and `CH-RUNTIMEOPS` nonce handshake does not exist.

**What changes.**
- Spec: §4.7.10 **Runtime process lifetime.** states the lifetime contract once; the §28.5.3 `CH-MSGSOCK` card defines `session_start` and `session_end`; the per-session manifest rows move into `session_start`; `CH-RUNTIMEOPS` session-scoped frames gain `sessionId` and `terminate` is deleted; §15.7, §15.4.6, and §29 follow (SPEC-1 to SPEC-7).
- Schemas: both intra-pod frame schemas and their fixtures and gates (SCHEMA-1).
- Adapter: one start helper and one end helper at every start and end site, `sessionId` on the session-scoped `CH-RUNTIMEOPS` frames, the developer-loop executor's minimal frame, and removal of the per-session manifest fields (CODE-1, CODE-2, CODE-3, CODE-8).
- SDKs and harness: all three SDKs serve sessions keyed by `sessionId`, and the compliance harness and reference runtimes play the new contract (CODE-4, CODE-5, CODE-6).
- Docs, records, and tests: runtime-author documentation, `BUILD-GAPS.md`, and new tests across tiers 1, 3, 4, 7a, and 10 (DOCS-1, RECORDS-1, TEST-1).
- Part B: the runtime connection handshake on `CH-MSGSOCK` and `CH-RUNTIMEOPS` (SPEC-8, CODE-7).

**Decisions.**
1. Minimal design: two `CH-MSGSOCK` frames written through a Server-level helper pair over `RuntimeProcess.WriteEnvelope`, with no `RuntimeProcess` interface change.
2. The contract covers `type: agent` runtimes in both deployment models. `type: mcp` runtimes are excluded, and the embedded loop receives the same frames.
3. `session_start` carries `sessionId`, `credentialsPath`, `experimentContext`, `tracingContext`, and `llm` (including `llm.headers`). Every member is a former manifest member, and only `llm.apiKeyEnv` is reworded.
4. `taskId` and the workspace path are not carried, because both are fixed functions of `sessionId`.
5. `credentialsPath` is carried rather than derived, because `--credentials-dir` makes the credential root operator-configurable.
6. `credentialsPath` is optional: present whenever a credential file was provisioned and absent otherwise. The empty string is never written.
7. `mcpNonce`, `connectorServers`, `agentInterface`, and `minPlatformVersion` stay in the manifest. 0084 owns the nonce and the connector set.
8. The per-session manifest rows are deleted rather than duplicated, and manifest `version` stays 1.
9. `session_end` carries `type` and `sessionId` only. Neither frame is acknowledged, and neither has a timeout.
10. The end of the `CH-MSGSOCK` connection ends every session. No `session_end` is written at pod exit or after the sidecar hold timeout's `CloseListener`, and an interrupt writes none.
11. A runtime ignores a `session_end` for a session it does not hold, which absorbs the rule-8 rollback race.
12. An SDK answers a message for an unknown or failed-create session with a `response` carrying `error` (`RUNTIME_ERROR`) and keeps running. The wire rule is limited to runtimes that keep per-session context.
13. `shutdown` stays process-scoped and is never written at a session boundary. Its signal, deadline, and exit-code wording stays with 0087 (F-4.7.27).
14. `CH-RUNTIMEOPS` `terminate` is deleted. It has no production sender, and it would be a second end-of-session signal with undefined addressing.
15. The session-scoped `CH-RUNTIMEOPS` frames, `files_updated` and `llm_request_completed` included, carry a required `sessionId`. Without it a multi-session runtime cannot tell which session a checkpoint quiesces, and the adapter cannot attribute direct-mode tokens. `llm_request_started` feeds only the pod-wide in-flight counter and takes none. `files_updated` gains its first spec row.
16. The Full **deadline signal handling** category is retargeted onto `deadline_approaching` addressed by `sessionId`. It is conditional on the declared capability and requires no unsolicited `response`.
17. The developer-loop `SubprocessExecutor` writes a `sessionId`-only `session_start` for a child that `Send` spawned. Without it every SDK runtime under `make run` would answer with errors.
18. The SDKs dial the platform and connector MCP sockets once per process, because the §4.7.6 `mcpNonce` row's no-re-arm rule rejects a later session's re-read nonce. Per-session MCP connections belong to 0084.
19. The session reaches `OnTerminate` as a parameter, `OnTerminate(ctx, sessionID, reason)`, which mirrors `OnSessionTerminated`. The shared §15 `TerminationReason` struct does not change.
20. `CreateRequest` gains `ExperimentContext`, `TracingContext`, and `LLM`, taken from `session_start`.
21. The Python SDK stays thread-based and synchronous, and the TypeScript SDK uses a per-session promise chain.
22. The Go SDK's `WithCredentialsPath` option and the manifest fallbacks for the credential path are deleted. `session_start` is the only source.
23. Part B validates the nonce line against the `mcpNonce` the manifest carries at accept time, and leaves the nonce's lifetime to 0084.
24. Part B's nonce line is `{"_lennyNonce":"<hex>"}`, which matches the existing `_lennyChallenge` keys. The manifest-v2 `lenny_nonce` line is not adopted before v2.
25. Part B needs no adapter-boot manifest, no launch ordering from 0087, and no nonce pinning from 0084. The SDK waits for the manifest before it dials, and the adapter accepts only inside the first `Runtime.Start`. A runtime whose nonce a later manifest write replaced is closed and redials, which is the only behavior that holds on unsupervised pods.
26. 0080 §1.9 is discharged with no change. No SDK emits a `status` frame.
27. The term "runtime generation" is not used, because 0087 defines it.

**Watch out for.**
- Lane ordering is load-bearing. DOCS-1 lands before SCHEMA-1, because the tier-11 frame and schema reconciliation gate is driven off the schema and needs the reference blocks first. CODE-3 lands immediately after SCHEMA-1, which makes `sessionId` required on the runtime-ops frames. CODE-8 lands only after CODE-4, CODE-5, and CODE-6, because removing the manifest fields earlier breaks the tier-10 credential-path cases while the SDKs still read them.
- CODE-6 deletes the SDK and `streaming-echo` `terminate` handlers in the same commit as the `checkDeadlineSignal` retarget. Splitting them leaves the Full battery red.
- The §4.7.6 `mcpNonce` row and several runtime-author pages carry phrases that `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` pins, and 0084 rewrites them. SPEC-8 appends a sentence to the row and widens its level cell, leaving the pinned description sentences unchanged. That gate's other retargets are in SPEC-2 and DOCS-1 item 8.
- On the sidecar transport, `endRuntimeForHoldTimeout` closes the connection before the per-session close runs. CODE-1 skips the `session_end` write there instead of relying on a failed write. On the embedded transport the write is real.
- The SDK-warm start is not spelled `Runtime.Start`. A helper placed only around `Runtime.Start` misses `ConfigureWorkspace`, which CODE-1 item 3 covers.
- A frozen-context runtime still answers every message. The discriminating assertion in TEST-1's tier-10 cases is that B's reply names B's `sessionId` and experiment variant.
- `sessionId` on `credentials_rotated` replaces path parsing. Do not route by parsing `credentialsPath`, because its root is operator-configurable.
- A compliance or test frame must omit `credentialsPath` when no file exists. SCHEMA-1 rejects the empty string.
- Platform and connector MCP tools for a second live session stay refused by the fail-closed `soleSession` rule until 0084 lands. A Standard-level multi-session test fails for that reason, not because of this proposal.
- Every table row this proposal edits is one physical line. The §4.7.9, §4.7.10, §28.5.3, and §29 passages are hard-wrapped, so match their anchors with line breaks ignored.

## Goals

- One statement of the runtime lifetime contract in §4.7.10, which every other site cites.
- Every session's context reaches a shared runtime process in that session's own frame, and every session's end is signalled while the connection lives.
- Every session-scoped frame on `CH-MSGSOCK` and `CH-RUNTIMEOPS` names its session.
- The Go, Python, and TypeScript SDKs serve any number of sequential and concurrent sessions with per-session context and no reliance on process exit.
- The conformance suite checks the contract directly for third-party runtimes.
- Part B: the runtime side of the manifest-nonce handshake on `CH-MSGSOCK` and `CH-RUNTIMEOPS` exists in the adapter and in every in-tree client.

## Non-goals

- Carrying `runtimeOptions` and `workspacePlan` in `session_start`. Neither is in the manifest, and `runtimeOptions` has no carrier from the gateway to the adapter. RECORDS-1 files the gap.
- Carrying `taskId` or the workspace path in `session_start`. Both are fixed functions of `sessionId`.
- Deriving `credentialsPath` in the runtime. The credential root is the operator flag `--credentials-dir`.
- Carrying `mcpNonce` or `connectorServers` in `session_start`. 0084 owns both and rejects a start frame that carries them.
- Moving `connectorServers`, `agentInterface`, or `minPlatformVersion` into `session_start`. 0084 owns the connector set, and the other two are runtime-definition descriptors.
- Keeping the per-session manifest fields beside the frame. That would be a dual carrier, and the manifest copy would be wrong on any multi-session pod.
- A per-session manifest file at `/run/lenny/slots/{sessionId}/`. The owner fixed the carrier as a `CH-MSGSOCK` frame, 0084 rejected that layout, and a session end needs a frame anyway.
- Carrying session context on the first `MessageEnvelope`. The envelope is client-facing and persisted, and a session can end with no message.
- Signalling session boundaries on `CH-RUNTIMEOPS`. The owner placed the end signal on `CH-MSGSOCK`, and `CH-RUNTIMEOPS` is Full-level only and not rendered.
- Reusing `shutdown` with a `sessionId` as the session end. One frame name would carry two meanings.
- Deleting `shutdown`. It remains a valid process-scoped signal.
- Keeping or rewording `CH-RUNTIMEOPS` `terminate`. It has no sender and would be a second end-of-session signal.
- Acknowledgement frames or timeouts for `session_start` or `session_end`.
- A `reason` field on `session_end`. No runtime behavior depends on it.
- Writing `session_end` at pod exit or after the sidecar hold-timeout `CloseListener`.
- Writing `session_end` on interrupt or heartbeat escalation. An interrupted session stays resumable, and an escalated session's teardown writes the frame through the normal close path.
- Changing the `RuntimeProcess.Start` and `Close` signatures so each transport writes the frames.
- An adapter-side open-session set for de-duplicating frames. The runtime ignores an end for a session it does not hold.
- Ending the shared connection on any write error, or adding write deadlines. The partial-write hazard predates this proposal.
- A server-first challenge in both modes. Finding 4's server mechanism awaits an owner decision across 0084 and 0087, and the order-tolerant client works unchanged if the server later challenges first.
- A new frame `type` for the handshake, or the manifest-v2 `lenny_nonce` line before manifest `version: 2`.
- Minting `mcpNonce` once per adapter process. The nonce's lifetime belongs to 0084.
- Writing a pod manifest at adapter boot. The first-session ordering half of F-4.7.26 belongs to 0087's supervisor.
- Defining "runtime generation", or renaming `pkg/adapter/runtimegeneration.go`.
- Changing the `soleSession` fail-closed MCP rule, or adding `_lennySessionId`. 0084 owns MCP attribution.
- A Standard-level tier-10 sequential or concurrent MCP case. It depends on 0084.
- A handler factory or a new per-session `Handler` interface. Per-session invocation of the existing methods, with the session parameter on `OnTerminate`, is sufficient.
- A `SessionID` field on the shared §15 `TerminationReason`. `OnSessionTerminated` already passes the session separately.
- A `status` emitter in the Python and TypeScript SDKs, or deletion of the Go SDK's unused `outboundStatus`.
- Bumping the manifest `version`.
- A runtime capability declaration for multi-session support. The owner rejected it on 2026-10-02.
- Making `restart` the universal lifetime. Concurrent pools would stay broken, and admission refuses `restart` for several runtime kinds.
- Editing pod-launch ordering wording ("before spawning the runtime" on `adapterLocalTools`, `runtimeOps`, and the MCP cards). 0087 owns it.
- Persisting session context across a crash. A crash retires the pod, and a resume on a fresh pod re-delivers the context.

## Open decisions for human to make

None. The owner answered on 2026-10-03:

- Part B (SPEC-8, CODE-7) stays in this proposal.
- `session_start` omits the task ID and the workspace path. Both are fixed derivations of `sessionId` (§15.7: the task ID equals the session ID; §6: the working directory derives from `sessionId`), so carrying them would create a second source that can disagree. A field can be added if either derivation ever changes.
- Per-session platform and connector MCP connections, and with them the symptom that MCP tools stop working for a later session on a kept runtime, belong to proposal 0084 (decision 18). This proposal does not fix that symptom.
- This proposal's spec steps land before those of proposals 0087 and 0084, which edit the same §4.7, §15.4, §15.7, and §4.7.6 text: it defines the lifetime contract both build on.

## Defects in the shipped tree that this proposal does not stage

- `CreateRequest.RuntimeOptions` and `CreateRequest.WorkspacePlan` are never populated in any SDK. `RuntimeOptions` has no carrier from the gateway to the adapter, and `WorkspacePlan` has no carrier from the adapter to the runtime. RECORDS-1 files the finding. A fix needs a new carrier for a gap that predates this proposal.
- The Go SDK's `TerminationReason` (`{Reason, DeadlineMS}`) differs from the §15 shared `TerminationReason` (`{Code, Detail}`). RECORDS-1 files it. Reconciling it is an SDK type change outside the lifetime contract.
- No adapter code populates `llm.headers`, which §4.9 and the `CH-LLMPROXY` card advertise. SPEC-2 moves the member's statement into `session_start` and RECORDS-1 files the gap. Implementing the advertised default is outside the validated problem.
- The adapter never sends `shutdown`, and the card's signal, deadline, and exit-code wording is unimplemented (F-4.7.27). 0087's supervisor owns it.
- The first-session manifest-ordering race (the runtime reads the manifest before the adapter writes it, F-4.7.26) stays open for 0087's supervisor. This proposal removes only the per-session dependence on the manifest.
- The `CH-MSGSOCK` card's **Degradation.** bullet says the adapter synthesizes `RUNTIME_CRASH` from the process exit code, and no adapter code reads one. The kept runtime process makes that bullet's per-process reading moot for session completion, and SPEC-3 corrects only the completion sentence that the lifetime contract contradicts.
- The §15.4.6 Standard **MCP nonce handshake** row says the runtime connects "on startup". The SDKs still dial once per process, so it stays as written.
- `SocketRuntimeProcess.WriteEnvelope` writes each `CH-MSGSOCK` frame to the connection all sessions share with one `conn.Write` call, sets no write deadline, and leaves the connection open after a write error, so a write that fails partway can leave a partial frame on the shared connection (`pkg/adapter/socketruntime.go:455-466`). The hazard predates this proposal and applies to every frame. The new `session_start` and `session_end` frames use the same write path, and no staged rule assumes a failed write was delivered, so this proposal does not stage a fix.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0084 | Draft (drafted 2026-09-26) | Contradicts its decision that the first-party runtime SDKs stay single-session, because CODE-4 and CODE-5 make each SDK serve sessions keyed by `sessionId`. SPEC-4, SCHEMA-1, and CODE-3 make `sessionId` required on `llm_request_completed` and drop the counts of a frame that names no session or an unbound one. That removes the subject of its optional `llm_request_completed` `sessionId`, its occupancy fallback for that frame in the direct-mode token sink, and the premise of its §11.2 residual-risk sentence. SPEC-8 appends a sentence to the §4.7.6 `mcpNonce` row that 0084 rewrites, widens that row's level cell, and validates the handshake against the currently published nonce. Its `runtimeSession` resolver for platform and connector MCP, its §15.4.3 **Session address.** rules, its connector-refusal tolerance (its CODE-6 and CODE-7), and its nonce lifetime are unaffected. | Re-baseline its "SDKs stay single-session" decision against this proposal, and keep the appended `mcpNonce` sentence and the widened level cell when it rewrites the row. Make its `llm_request_completed` `sessionId` required, drop that frame's occupancy fallback and the docs that describe the optional field and the fallback, and re-base its §11.2 residual-risk sentence on the required field. |
| 0087 | Draft | Edits §4.7, §15.4, and §15.7 sections that 0087 also edits, and leaves the `restart` sentence of §4.7.10 **Runtime process lifetime.**, the `shutdown` semantics (F-4.7.27), launch-ordering wording, and the first-session manifest ordering (F-4.7.26) to 0087. | Serialize its spec steps after this proposal's, add the `restart` sentence to **Runtime process lifetime.** when it defines the selector, and re-baseline its D-STALE and nonce text against SPEC-8. |
| 0079 | Implemented | Discharges the work 0079 handed over: the SDK change, the `Handler` and `types.go` comment corrections, the §15.7 `CreateRequest` comments, and the §15.4.6 deadline category. | Nothing. Implemented proposals are not edited. |
| 0080 | Draft | Discharges §1.9 with no change, because no SDK emits a `status` frame. | Record §1.9 as discharged by this proposal. |

## Deliverable index

- SPEC-1 — `spec/04_system-components.md`, `spec/15_external-api-surface.md` — state the runtime lifetime contract in §4.7.10 and align §4.7.9, the §4.7.1 `Shutdown` row, and §15.4.2 `DRAINING`.
- SPEC-2 — `spec/04_system-components.md`, `spec/06_warm-pod-model.md`, `spec/08_recursive-delegation.md`, `spec/10_gateway-internals.md`, `spec/15_external-api-surface.md`, `spec/16_observability.md`, `spec/28_communication-channels.md`, `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` — move the per-session manifest rows into `session_start` and repoint every reader.
- SPEC-3 — `spec/28_communication-channels.md`, `spec/15_external-api-surface.md` — define `session_start`, `session_end`, and the session-error rule on the `CH-MSGSOCK` card.
- SPEC-4 — `spec/28_communication-channels.md`, `spec/15_external-api-surface.md` — add `sessionId` to the session-scoped `CH-RUNTIMEOPS` frames, add `files_updated`, and delete `terminate`.
- SPEC-5 — `spec/15_external-api-surface.md` — rewrite the §15.7 SDK contract and the `CreateRequest` and `Message` comments for per-session invocation.
- SPEC-6 — `spec/15_external-api-surface.md` — add the Basic **session lifetime** category and retarget the Full **deadline signal handling** category, and point the Full **credential rotation handling** category at the session's credential file.
- SPEC-7 — `spec/29_communication-scenarios.md` — trace the frame writes in §29.2, §29.4, and §29.6.
- SPEC-8 — `spec/04_system-components.md`, `spec/15_external-api-surface.md`, `spec/28_communication-channels.md`, `spec/29_communication-scenarios.md` — Part B: state the runtime connection handshake wire lines.
- SCHEMA-1 — `schemas/lenny-adapter-jsonl.schema.json`, `schemas/runtime-ops-events.schema.json`, `schemas/examples/` — schematize the frames and update the fixtures and gates that pin them.
- CODE-1 — `pkg/adapter/sessionframes.go` and the start and end sites — write `session_start` and `session_end` through one helper pair.
- CODE-2 — `pkg/gateway/session/executor/subprocess.go` — write a `sessionId`-only `session_start` for a `Send`-spawned child.
- CODE-3 — `pkg/adapter/runtimeops.go` and its callers — key session-scoped `CH-RUNTIMEOPS` frames by `sessionId` and delete `Terminate`.
- CODE-4 — `sdks/runtime/go/` — serve sessions keyed by `sessionId` in the Go SDK.
- CODE-5 — `sdks/runtime/python/`, `sdks/runtime/typescript/` — port CODE-4 to the Python and TypeScript SDKs.
- CODE-6 — `cmd/lenny-compliance/`, `cmd/runtimes/`, the SDK lifecycle files — play the contract in the harness and delete the runtime-side `terminate` handlers.
- CODE-7 — `pkg/adapter/intrapodauth.go`, `pkg/runtimekit/transport.go`, the SDK transports — Part B: enforce and answer the connection handshake.
- CODE-8 — `pkg/adapter/manifest.go` — stop writing the per-session manifest fields.
- DOCS-1 — `docs/reference/adapter-contract.md`, `docs/runtime-author-guide/`, `docs/tutorials/build-a-runtime.md`, and the experiment-context pages — update the runtime-author documentation and the tier-11 tests that pin it.
- RECORDS-1 — `BUILD-GAPS.md` — record what this proposal closes and file the gaps it leaves.
- TEST-1 — `pkg/`, `sdks/`, `tests/` — add the new cross-component tests.
