# Summary: One runtime lifetime contract: session_start and session_end on CH-MSGSOCK and multi-session runtime SDKs

## Summary

**Problem statement.** The platform keeps one `type: agent` runtime process across sessions, both on concurrent pools and, since 0079, on recycling pools. The Go, Python, and TypeScript runtime SDKs nevertheless load one session's context when the process starts and invoke the create handler once. A kept runtime therefore serves every later session under the context it loaded at startup, which is usually empty on sidecar pods. A user-scoped credential lease can cross users inside a tenant, platform and connector MCP stop working for later sessions, and recycling and concurrent pools are unsafe with any SDK-built runtime. The spec still describes one session per process in several places, the adapter sends no session-boundary signal, and the runtime side of the `CH-MSGSOCK` and `CH-RUNTIMEOPS` nonce handshake does not exist.

**What changes.**
- Spec: §4.7.10 **Runtime process lifetime.** states the lifetime contract once; the §28.5.3 `CH-MSGSOCK` card defines `session_start`, its acknowledgement `session_started`, and `session_end`, and the table of paths that write them, with the §5.2 slot serialization and the §4.7.1 rule-8 entry-identity confirmation its open sequence rests on; the per-session manifest rows move into `session_start`; `CH-RUNTIMEOPS` session-scoped frames gain `sessionId` and are written only after the adapter reads the session's `session_started`, and `terminate` is deleted; §15.7, §15.4.6, and §29 follow (SPEC-1 to SPEC-7).
- Schemas: both intra-pod frame schemas and their fixtures and gates (SCHEMA-1).
- Adapter: one open sequence at every start and the `session_end` write at the `Shutdown` and `DemoteSDK` teardowns, on the rows of one write table, with `DemoteSDK` failing closed when it cannot take the slot serialization; `sessionId` on the session-scoped `CH-RUNTIMEOPS` frames; the developer-loop executor's minimal frame; removal of the per-session manifest fields; and the `session_started` wait with the `CH-RUNTIMEOPS` ordering it gates (CODE-1, CODE-2, CODE-3, CODE-8, CODE-9).
- SDKs and harness: all three SDKs serve sessions keyed by `sessionId`, and the compliance harness and reference runtimes play the new contract (CODE-4, CODE-5, CODE-6).
- Docs, records, and tests: runtime-author documentation, `BUILD-GAPS.md`, and new tests across tiers 1, 3, 4, 7a, and 10 (DOCS-1, RECORDS-1, TEST-1).
- Part B: the runtime connection handshake on `CH-MSGSOCK` and `CH-RUNTIMEOPS` (SPEC-8, CODE-7).

**Decisions.**
1. Minimal design: three `CH-MSGSOCK` frames, `session_start`, `session_started`, and `session_end`. The adapter writes `session_start` and `session_end` through a Server-level helper pair over `RuntimeProcess.WriteEnvelope` and reads `session_started` through `RuntimeProcess.Output`. No `RuntimeProcess` method signature changes, and CODE-9 item 6 gives `Output` on `InProcessRuntime` and `SubprocessExecutor` the fan-out semantics `SocketRuntimeProcess` already has.
2. The contract covers `type: agent` runtimes in both deployment models. `type: mcp` runtimes are excluded, and the embedded loop receives the same frames.
3. `session_start` carries `sessionId`, `startId`, `credentialsPath`, `experimentContext`, `tracingContext`, and `llm` (including `llm.headers`).
4. `taskId` and the workspace path are not carried, because both are fixed functions of `sessionId`.
5. `credentialsPath` is carried rather than derived, because `--credentials-dir` makes the credential root operator-configurable.
6. `credentialsPath` is optional: present whenever a credential file was provisioned and absent otherwise. The empty string is never written.
7. `mcpNonce`, `connectorServers`, `agentInterface`, and `minPlatformVersion` stay in the manifest. 0084 owns the nonce and the connector set.
8. The per-session manifest rows are deleted rather than duplicated, and manifest `version` stays 1.
9. `session_end` carries `type` and `sessionId` only, is not acknowledged, and has no timeout. An acknowledgement would order nothing: after `session_end` the adapter writes no frame for the session on either channel, and a `CH-RUNTIMEOPS` frame written before it is ordered by neither connection's write order whether or not the runtime answers. `session_start` is acknowledged (decision 28).
10. One table on the `CH-MSGSOCK` card, **Session frame writes.**, states every path that writes `session_start` or `session_end`.
11. The stale-`session_end` race is fenced by the open sequence that SPEC-3 **Session frame writes.** states, and every `session_end` row is decided on the rule-8 record. CODE-9 item 1 adds a second adapter-side record, the per-start acknowledgement record, which holds for each session the current start's `startId`, the state of its acknowledgement (pending, read, failed, or not awaiting), and a channel that closes when the state leaves pending. It gates the session's `CH-RUNTIMEOPS` frames and decides no `session_end` write. A runtime ignores a `session_end` for a session it does not hold and a `session_start` for a session it already holds. The first rule absorbs a duplicate `session_end` from two teardowns that race outside the guard, and the second absorbs a start that follows a `session_end` whose write failed.
12. An SDK answers a message for an unknown or failed-create session with a `response` carrying `error` (`RUNTIME_ERROR`) and keeps running. The wire rule is limited to runtimes that keep per-session context.
13. `shutdown` stays process-scoped and is never written at a session boundary. Its signal, deadline, and exit-code wording stays with 0087 (F-4.7.27).
14. `CH-RUNTIMEOPS` `terminate` is deleted. It has no production sender, and it would be a second end-of-session signal with undefined addressing.
15. The session-scoped `CH-RUNTIMEOPS` frames, `files_updated` and `llm_request_completed` included, carry a required `sessionId`. Without it a multi-session runtime cannot tell which session a checkpoint quiesces, and the adapter cannot attribute direct-mode tokens. `llm_request_started` feeds only the pod-wide in-flight counter and takes none. `files_updated` gains its first spec row.
16. The Full **deadline signal handling** category is retargeted onto `deadline_approaching` addressed by `sessionId`. It is conditional on the declared capability and requires no unsolicited `response`.
17. The developer-loop `SubprocessExecutor` writes a minimal `session_start` for a child that `Send` spawned. Without it every SDK runtime under `make run` would answer with errors.
18. The SDKs dial the platform and connector MCP sockets once per process. A running pod MCP surface validates the nonce of the start that armed it, and every later start's manifest write carries a fresh nonce (§4.7.6 `mcpNonce` row), so a per-session redial with the re-read nonce is refused while that surface runs. Per-session MCP connections belong to 0084.
19. The session reaches `OnTerminate` as a parameter, `OnTerminate(ctx, sessionID, reason)`, which mirrors `OnSessionTerminated`. The shared §15 `TerminationReason` struct does not change.
20. `CreateRequest` gains `ExperimentContext`, `TracingContext`, and `LLM`, taken from `session_start`.
21. The Python SDK stays thread-based and synchronous, and the TypeScript SDK uses a per-session promise chain.
22. Each SDK's credential-path option and the manifest fallbacks for the credential path are deleted. `session_start` is the only source.
23. Part B validates the nonce line against the `mcpNonce` the manifest carries at accept time, and leaves the nonce's lifetime to 0084.
24. Part B's nonce line is `{"_lennyNonce":"<hex>"}`, which matches the existing `_lennyChallenge` keys. The manifest-v2 `lenny_nonce` line is not adopted before v2.
25. Part B depends on no adapter-boot manifest, no launch ordering from 0087, and no nonce pinning from 0084. The SPEC-8 **Runtime connection handshake.** redial covers every change of the published nonce under a dialing runtime: the §4.7.9 step-3 placeholder replaced at step 6, a concurrent start's manifest write, a `CH-MSGSOCK` connection the accept loop installs after a timed-out first `Runtime.Start`, and a `CH-RUNTIMEOPS` connection, which the adapter accepts at any time from boot.
26. No SDK gains a `status` helper, because no SDK emits a `status` frame. The effect on 0080 is under **Impacts on other proposals**.
27. The term "runtime generation" is not used, because 0087 defines it.
28. `session_start` is acknowledged by `session_started` on `CH-MSGSOCK`, as SPEC-3 **Outbound: `session_started`** states, and the adapter orders the session-scoped `CH-RUNTIMEOPS` frames through that acknowledgement, as SPEC-4 Edit 2 states. The owner chose the acknowledgement on 2026-10-04 in place of a runtime-side hold of early frames, whose runtime-chosen wait left the cross-channel order nondeterministic.
29. `DemoteSDK` fails closed when its wait for the slot serialization outlasts its request's deadline, as the SPEC-3 **Session frame writes.** `DemoteSDK` deadline row states (owner decision of 2026-10-04).
30. The adapter's bound on the `session_started` wait is an adapter flag whose default is IMPLEMENTOR'S CHOICE (CODE-9 item 2). The spec states its other intra-pod adapter timeouts as fixed values rather than operator settings, so its convention does not require operator tuning; the code rules require a flag because the spec fixes no value.

**Watch out for.**
- CODE-9 lands after CODE-4, CODE-5, and CODE-6. Its wait fails every start that waits under SPEC-3 **Outbound: `session_started`** rule 3 until the runtime writes `session_started`; the SDKs write it from CODE-4 and CODE-5 on, and `streaming-echo` from CODE-6 on. CODE-1 already drops `session_started` in the Attach loop, so an acknowledgement written before CODE-9 lands reaches no client.
- From CODE-3 until CODE-6 the compliance harness reads a `response` to a `message` before a session-scoped `CH-RUNTIMEOPS` frame. CODE-6 replaces that interim order with the `session_started` read SPEC-6 Edit 4 states. Every SDK revision in between satisfies the interim order, and the harness's readers skip `session_started` from CODE-3 on.
- The lane order is fixed. DOCS-1 lands before SCHEMA-1, because the tier-11 frame and schema reconciliation gate reads the schema and needs the reference blocks first. CODE-3 lands immediately after SCHEMA-1, which makes `sessionId` required on the runtime-ops frames. CODE-8 lands only after CODE-4, CODE-5, and CODE-6, because removing the manifest fields earlier breaks the tier-10 credential-path cases while the SDKs still read them.
- CODE-6 deletes the SDK and `streaming-echo` `terminate` handlers in the same commit as the `checkDeadlineSignal` retarget. Splitting them leaves the Full battery red.
- The §4.7.6 `mcpNonce` row and several runtime-author pages carry phrases that `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` pins, and 0084 rewrites them. SPEC-8 appends a sentence to the row and widens its level cell, leaving the pinned description sentences unchanged. That gate's other retargets are in DOCS-1 item 8.
- The hold-timeout termination writes no `session_end` in either deployment model, so CODE-1 leaves `holdstate.go` unchanged. `StartSession` and `ConfigureWorkspace` take the per-slot guard after `Runtime.Start` returns, and never across it.
- SPEC-3 edits the §5.2 **Slot-identifier reclaim hold.** paragraph and §4.7.1 rule 8. A proposal that later edits either text keeps the slot serialization and the entry-identity confirmation. CODE-1 also reorders the `DemoteSDK` handler, which is the SIGTERM path that 0087's supervisor work scopes to `sdk_connecting`.
- The SDK-warm start is not spelled `Runtime.Start`. A helper placed only around `Runtime.Start` misses `ConfigureWorkspace`, which CODE-1 item 2 covers.
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
- An acknowledgement or a timeout for `session_end` (decision 9).
- A runtime-side hold of a `CH-RUNTIMEOPS` frame that arrives before its session's `session_start`. The `session_started` acknowledgement orders the two channels instead (decision 28).
- A `reason` field on `session_end`. No runtime behavior depends on it.
- Writing `session_end` at pod exit or at the coordinator hold timeout, in either deployment model.
- Writing `session_end` on interrupt or heartbeat escalation. An interrupted session stays resumable, and an escalated session's teardown writes the frame through its `Shutdown` row.
- Changing the `RuntimeProcess.Start` and `Close` signatures so each transport writes the frames.
- A second adapter-side record for the `session_end` decision. Every `session_end` row is decided on the rule-8 record, which marks each session whose `session_start` was written and confirmed. The per-start acknowledgement record that CODE-9 item 1 adds holds the current start's `startId` and acknowledgement state, and it gates only the session-scoped `CH-RUNTIMEOPS` frames.
- Holding the per-slot guard across `Runtime.Start` in `StartSession` and `ConfigureWorkspace`. A reclaim during a slow first accept would then run without the guard and hold the identifier for the life of the pod.
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

None.

## Defects in the shipped tree that this proposal does not stage

- `CreateRequest.RuntimeOptions` and `CreateRequest.WorkspacePlan` are never populated in any SDK. `RuntimeOptions` has no carrier from the gateway to the adapter, and `WorkspacePlan` has no carrier from the adapter to the runtime. RECORDS-1 files the finding. A fix needs a new carrier for a gap that predates this proposal.
- The Go SDK's `TerminationReason` (`{Reason, DeadlineMS}`) differs from the §15 shared `TerminationReason` (`{Code, Detail}`). RECORDS-1 files it. Reconciling it is an SDK type change outside the lifetime contract.
- No adapter code populates `llm.headers`, which §4.9 and the `CH-LLMPROXY` card advertise. SPEC-2 moves the member's statement into `session_start` and RECORDS-1 files the gap. Implementing the advertised default is outside the validated problem.
- The adapter never sends `shutdown`, and the card's signal, deadline, and exit-code wording is unimplemented (F-4.7.27). 0087's supervisor owns it.
- The first-session manifest-ordering race (the runtime reads the manifest before the adapter writes it, F-4.7.26) stays open for 0087's supervisor. This proposal removes only the per-session dependence on the manifest.
- The `CH-MSGSOCK` card's **Degradation.** bullet says the adapter synthesizes `RUNTIME_CRASH` from the process exit code, and no adapter code reads one. The kept runtime process makes that bullet's per-process reading moot for session completion, and SPEC-3 corrects only the completion sentence that the lifetime contract contradicts.
- The §15.4.6 Standard **MCP nonce handshake** row says the runtime connects "on startup". The SDKs still dial once per process, so it stays as written.
- The §28.5.3 `CH-MSGSOCK` **Error reporting via `response`.** paragraph, and its mirror in `docs/reference/adapter-contract.md`, map the task to `failed` whenever a `response` carries `error`, while the shipped Go and Python SDKs answer any per-message handler error with such a `response` and keep serving the session (`sdks/runtime/go/runtime/runtime.go:467-478`, `sdks/runtime/python/lenny_runtime/runtime.py:363-372`). Whether a per-message error ends the session predates this proposal. This proposal keeps the `failed` sentence, and the only errored answers it adds, for an unknown or failed-create session under SPEC-3 **Session errors.**, end a session that cannot proceed.
- `SocketRuntimeProcess.WriteEnvelope` writes each `CH-MSGSOCK` frame to the connection all sessions share with one `conn.Write` call, sets no write deadline, and leaves the connection open after a write error, so a write that fails partway can leave a partial frame on the shared connection (`pkg/adapter/socketruntime.go:455-466`). The hazard predates this proposal and applies to every frame. The new `session_start` and `session_end` frames use the same write path, and no staged rule assumes a failed write was delivered, so this proposal does not stage a fix.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0084 | Draft (drafted 2026-09-26) | Contradicts its decision that the first-party runtime SDKs stay single-session, because CODE-4 and CODE-5 make each SDK serve sessions keyed by `sessionId`. The SDKs still dial the platform and connector MCP sockets once per process (decision 18), so one MCP connection serves every session a kept runtime holds. SPEC-4, SCHEMA-1, and CODE-3 make `sessionId` required on `llm_request_completed` and drop the counts of a frame that names no session or an unbound one. That removes the subject of its optional `llm_request_completed` `sessionId`, its occupancy fallback for that frame in the direct-mode token sink, and the premise of its §11.2 residual-risk sentence. SPEC-8 appends a sentence to the §4.7.6 `mcpNonce` row that 0084 rewrites, widens that row's level cell, and checks the `CH-MSGSOCK` and `CH-RUNTIMEOPS` handshake against the nonce the manifest publishes at accept time. SPEC-8 and CODE-7 do not wait on its nonce pinning, because a runtime refused for a replaced nonce reads the manifest again and redials (decision 25); a pinned nonce makes that redial rarer and changes no rule. Its `runtimeSession` resolver for platform and connector MCP, its §15.4.3 **Session address.** rules, its connector-refusal tolerance (its CODE-6 and CODE-7), and its nonce lifetime are unaffected. | Serialize its spec steps after this proposal's, which edit the same §4.7, §15.4, §15.7, and §4.7.6 text and define the lifetime contract it builds on. Re-baseline its "SDKs stay single-session" decision against this proposal, and take on per-session platform and connector MCP connections in the three SDKs, which this proposal leaves once per process (decision 18). Keep the appended `mcpNonce` sentence and the widened level cell when it rewrites the row, and keep the manifest's published `mcpNonce` as the value the connection handshake compares against. Make its `llm_request_completed` `sessionId` required, drop that frame's occupancy fallback and the docs that describe the optional field and the fallback, and re-base its §11.2 residual-risk sentence on the required field. |
| 0087 | Draft | Edits §4.7, §15.4, and §15.7 sections that 0087 also edits, and leaves the `restart` sentence of §4.7.10 **Runtime process lifetime.**, the `shutdown` semantics (F-4.7.27), launch-ordering wording, and the first-session manifest ordering (F-4.7.26) to 0087. | Serialize its spec steps after this proposal's, add the `restart` sentence to **Runtime process lifetime.** when it defines the selector, and re-baseline its D-STALE and nonce text against SPEC-8. |
| 0079 | Implemented (approved 2026-10-02) | Discharges the follow-up 0079 handed over: the SDK multi-session change, the `Handler` and `types.go` comment corrections, the §15.7 `CreateRequest` comments, and the §15.4.6 deadline category. It also discharges 0079's pre-release condition. Nothing 0079 landed is contradicted. | Nothing. Implemented proposals are not edited. |
| 0080 | Draft (marked "EARLY DRAFT, NOT CONVERGED"; last commit 2026-09-06, a commit date rather than a review date) | Discharges §1.9 ("The runtime SDKs model no status frame") with no change, because no SDK emits a `status` frame and CODE-5 adds no `status` helper (decision 26). Its other §1 entries are unaffected. | Record §1.9 as discharged by this proposal. |
| 0085 | Draft (drafted 2026-09-27; last commit 2026-09-27, a commit date rather than a review date) | SPEC-3's **Session frame writes.** row for a `DemoteSDK` that outlasts its request's deadline (decision 29) states that a gateway caller fails the pod and claims a replacement, and cites the §4.7.1 `ConfigureWorkspace` row for it. 0085 SPEC-3 edit (a) deletes "and a replacement is claimed" from that row and defers the recovery to the §6.2 policy inside the §7.1 creation unit and to §7.2 and §7.3 at `POST /v1/sessions/{id}/start`. Its `grep -n "a replacement is claimed" spec/` check does not match this proposal's spelling, "claims a replacement". Its other deliverables (the SPEC-1 §6.2 **Scope:** rule, SPEC-2, SPEC-3 edit (b) to §29.2 step 23, and RECORDS-1) are unaffected. | Whichever proposal lands second re-bases the row. If 0085 lands first, this proposal rewords the row's recovery clause so that the pod transitions to `failed` and the policy governing the request applies, with no replacement claim. If this proposal lands first, 0085 extends its SPEC-3 edit and its `grep` check to the §28.5.3 **Session frame writes.** row, which spells the claim as "claims a replacement". |

## Deliverable index

- SPEC-1 — `spec/04_system-components.md`, `spec/15_external-api-surface.md` — state the runtime lifetime contract in §4.7.10 and align §4.7.9, the §4.7.1 `Shutdown` row, and the §15.4.2 `DRAINING` row.
- SPEC-2 — `spec/04_system-components.md`, `spec/06_warm-pod-model.md`, `spec/08_recursive-delegation.md`, `spec/10_gateway-internals.md`, `spec/15_external-api-surface.md`, `spec/16_observability.md`, `spec/28_communication-channels.md` — delete the per-session manifest rows and repoint every reader to `session_start`.
- SPEC-3 — `spec/28_communication-channels.md`, `spec/15_external-api-surface.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/04_system-components.md` — define `session_start`, `session_started`, `session_end`, **Session frame writes.**, and **Session errors.** on the `CH-MSGSOCK` card, with the §15.4, §15.4.1, and §15.4.3 alignments, the §5.2 slot serialization, the §4.7.1 rule-8 entry-identity confirmation, and the §5.1 and §5.2 per-session setup retarget.
- SPEC-4 — `spec/28_communication-channels.md`, `spec/15_external-api-surface.md` — add `sessionId` to the session-scoped `CH-RUNTIMEOPS` frames, order them after the session's `session_started`, add `files_updated`, and delete `terminate` from the card, §15.4.4, and §15.7.
- SPEC-5 — `spec/15_external-api-surface.md` — rewrite the §15.7 SDK contract and the `CreateRequest` and `Message` comments for per-session invocation.
- SPEC-6 — `spec/15_external-api-surface.md` — add the Basic **session lifetime** conformance category, retarget the Full **deadline signal handling** and **credential rotation handling** categories, check the `session_started` acknowledgement in the Full **CH-RUNTIMEOPS opening** category, and state the session precondition for session-scoped categories.
- SPEC-7 — `spec/29_communication-scenarios.md` — trace the session frame writes in §29.2, §29.4, and §29.6.
- SPEC-8 — `spec/04_system-components.md`, `spec/15_external-api-surface.md`, `spec/28_communication-channels.md`, `spec/29_communication-scenarios.md` — Part B: state the runtime connection handshake on `CH-MSGSOCK` and `CH-RUNTIMEOPS`.
- CODE-1 — `pkg/adapter/sessionframes.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/attach.go`, `pkg/adapter/manifest.go` — write `session_start` and `session_end` through one helper pair on the rows of the SPEC-3 **Session frame writes.** table, drop `session_started` in the Attach loop, and fail `DemoteSDK` closed.
- CODE-2 — `pkg/gateway/session/executor/subprocess.go` — write a minimal `session_start` for a `Send`-spawned child.
- CODE-3 — `pkg/adapter/runtimeops.go`, `pkg/adapter/usage.go`, the other adapter, `pkg/runtimekit`, and `cmd/lenny-compliance` files its Targets list, and their peers and tests — key session-scoped `CH-RUNTIMEOPS` frames and the direct-mode token sink by `sessionId`, and delete `Terminate`.
- CODE-4 — `sdks/runtime/go/` and the Go scaffold templates — serve sessions keyed by `sessionId` in the Go SDK.
- CODE-5 — `sdks/runtime/python/`, `sdks/runtime/typescript/`, and their scaffold templates — port CODE-4 to the Python and TypeScript SDKs.
- CODE-6 — `cmd/lenny-compliance/`, `cmd/lenny-ctl/runtimescaffold/{probe,probe_test}.go`, `cmd/runtimes/echo-concurrent/`, `cmd/runtimes/streaming-echo/`, the three SDK lifecycle files, `tests/tier3_contract/sdks/runtime_sdk_test.go` — play the contract in the compliance harness and delete the runtime-side `terminate` handlers.
- CODE-7 — `pkg/adapter/intrapodauth.go`, `pkg/adapter/peercred.go`, `pkg/adapter/mcp/challenge.go`, and every file of the CODE-7 **Client inventory.** table — Part B: enforce and answer the connection handshake.
- CODE-8 — `pkg/adapter/manifest.go` and the tests that read a removed field — stop writing the per-session manifest fields.
- CODE-9 — `pkg/adapter/sessionframes.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/attach.go`, `pkg/adapter/checkpoint.go`, `pkg/adapter/lifecycle.go`, `pkg/adapter/credentials.go`, `pkg/adapter/staging.go`, `pkg/adapter/embedded.go`, `pkg/gateway/session/executor/subprocess.go`, `cmd/lenny-adapter/main.go`, `tests/registers/deployment-boundary-defaults.yaml`, and the fakes its item 5 names — wait for `session_started` in the open sequence, write the session-scoped `CH-RUNTIMEOPS` frames only after it, give the embedded and subprocess transports fan-out `Output`, and register the flag's default.
- SCHEMA-1 — `schemas/lenny-adapter-jsonl.schema.json`, `schemas/runtime-ops-events.schema.json`, `schemas/examples/`, `schemas/lenny-adapter.proto`, `pkg/proto/adapter/v1/lenny-adapter.pb.go`, and the tier-0, tier-3, and tier-11 gates its Targets list — schematize the frames and update the fixtures and gates that pin them.
- DOCS-1 — `docs/reference/adapter-contract.md`, `docs/runtime-author-guide/`, `docs/tutorials/build-a-runtime.md`, `docs/tutorials/recursive-delegation.md`, `docs/getting-started/concepts.md`, the pages DOCS-1 item 9's sweep reaches, and the `tests/tier11_docs/` tests its Targets list — update the runtime-author documentation and the tier-11 tests that pin it.
- RECORDS-1 — `BUILD-GAPS.md` — record what this proposal closes and file the gaps it leaves.
- TEST-1 — `pkg/`, `sdks/`, `tests/` — add the new cross-component tests.
