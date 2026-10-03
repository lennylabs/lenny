# Non-spec changes: Runtime SDKs assume one session per process

## Design (implementation-facing)

The adapter gains one start helper and one end helper over the existing `RuntimeProcess.WriteEnvelope`, so every `type: agent` start and end site writes the SPEC-3 frames through one path and the `RuntimeProcess` interface does not change (CODE-1). The developer-loop executor writes a minimal `session_start` for a child it spawns itself (CODE-2). The session-scoped `CH-RUNTIMEOPS` senders gain a session argument and `terminate` is deleted on the adapter side (CODE-3). The three SDKs keep per-session state keyed by `sessionId` and invoke the handler per session (CODE-4, CODE-5). The compliance harness plays the adapter's new side of the contract (CODE-6). Once the SDKs read the frame, the adapter stops writing the per-session manifest fields (CODE-8). Part B adds the connection handshake on both runtime listeners and the SDK client half (CODE-7).

Each deliverable updates, in the same commit, the existing tests its change breaks. TEST-1 holds the new cross-component tests. The wire rules are stated once in the spec text staged by SPEC-3 (`session_start` rules 1 to 3, `session_end` rules 1 to 3, **Session errors.**), SPEC-4, and SPEC-8; the deliverables below cite them.

## Staged code changes

### CODE-1 · Adapter session frames: start and end helpers at every start and end site

Targets: `pkg/adapter/sessionframes.go` (new), `pkg/adapter/session.go` (`StartSession`, `Shutdown`), `pkg/adapter/resume.go` (`Resume`), `pkg/adapter/sdkwarm.go` (`ConfigureWorkspace`, `refuseUnconfirmedSDKWarmStart`), `pkg/adapter/holdstate.go` (`endRuntimeForHoldTimeout` and the hold-timeout per-session close), `pkg/adapter/runtimegeneration.go` (`rollbackUnconfirmedStart`), `pkg/adapter/manifest.go` (`ManifestLLM`, `manifestLLMFromPayload`).

1. `sessionframes.go` defines:
   - `sessionStartFrame`, with JSON members `type`, `sessionId`, `credentialsPath` (`omitempty`), `experimentContext`, `tracingContext`, and `llm`. The last three are written as `null` when the session has none. The members reuse the existing builders `manifestExperimentContext`, `s.sessionCredentialsPath`, and `s.manifestLLM`, after deleting `ManifestLLM.BaseURL` and its assignment in `manifestLLMFromPayload` (the proxy URL stays in the credential file only, §4.7.11 item 4); a `credentialsPath` the builder resolves to the empty string is omitted.
   - `sessionEndFrame`, with JSON members `type` and `sessionId`.
   - `(s *Server) writeSessionStart(sessionID string, in manifestInputs) error`, which encodes the frame and writes it through `s.Runtime.WriteEnvelope(sessionID, …)`.
   - `(s *Server) startRuntimeSession(ctx context.Context, sessionID string, in manifestInputs) error`, which calls `s.Runtime.Start` and then `writeSessionStart`.
   - `(s *Server) endRuntimeSession(ctx context.Context, sessionID string) error`, which writes `session_end` unless the runtime's connection has ended (item 4), logs a failed write with `slog.Debug("session_end_write_failed", …)` without failing the teardown, and then calls `s.Runtime.Close(ctx, sessionID)` and returns its error.
   - Both helpers and `writeSessionStart` write nothing when `s.RuntimeKind == RuntimeKindMCP`.
2. Call sites. `StartSession` and `Resume` replace `s.Runtime.Start` with `startRuntimeSession`, so the frame is written after `Runtime.Start` succeeds and before `noteRuntimeStarted` and the first message. A failed `startRuntimeSession` takes each site's existing release-and-TRANSIENT failure arm. `Shutdown` and the hold-timeout per-session close replace `s.Runtime.Close` with `endRuntimeSession`. `rollbackUnconfirmedStart` reads the slot identifier's entry with `slotStateLocked` under `s.mu`. It calls `endRuntimeSession` when no entry stands and keeps `s.Runtime.Close` otherwise (SPEC-3 **Inbound: `session_end`** rule 1), and it writes the frame after releasing `s.mu`.
3. SDK-warm start. On the fresh arm of `ConfigureWorkspace`, after `sw.ConfigureWorkspace` succeeds and before `noteRuntimeStarted`, call `writeSessionStart` with the same `manifestInputs` the RPC already assembles. A failed write releases the claimed slot through `releaseClaimedSlot` and returns `codes.Internal`, as the existing `ConfigureWorkspace` failure arm does. `refuseUnconfirmedSDKWarmStart` writes no `session_end`, because `DemoteSDK` tears down the embedded loop and the end of the connection ends the session; state this in a comment there.
4. Hold timeout. `endRuntimeForHoldTimeout` records, after `CloseListener` returns, that the runtime's connection has ended, and `endRuntimeSession` skips the `session_end` write when that record is set. On the embedded transport there is no `CloseListener`, so the write happens. **IMPLEMENTOR'S CHOICE:** how the record is stored. The record must be safe for concurrent reads, set before any hold-timeout per-session close runs, and never cleared for the life of the adapter process.
5. Interrupt paths write nothing.
6. Cite `// spec: §28.5.3 (CH-MSGSOCK session_start, session_end); §4.7.10 (Runtime process lifetime)` on the helpers and on each call site's comment.

Every `type: agent` start site, sidecar and embedded, including the SDK-warm `ConfigureWorkspace` start, writes `session_start`; `RuntimeKindMCP` writes none. Existing adapter tests whose fake `RuntimeProcess` asserts the exact sequence of frames it received gain the leading `session_start` and the trailing `session_end`.

### CODE-2 · Developer-loop `SubprocessExecutor` writes `session_start` for a child it spawns

Target: `pkg/gateway/session/executor/subprocess.go` (`session`, `Send`).

1. `session(sessionID)` reports whether this call spawned the child.
2. `Send` writes `{"type":"session_start","sessionId":"<id>"}` on the child's stdin before the first `message` only when `session` spawned the child in that call. The frame carries no other member.
3. A child spawned by `Start` (the adapter's `--runtime-bin` path) receives the adapter's full frame through `WriteEnvelope` (CODE-1), so `Start` writes nothing.
4. Cite `// spec: §28.5.3 (CH-MSGSOCK session_start); §17.4 (Local Development Mode)`.

### CODE-3 · `CH-RUNTIMEOPS` adapter side: `sessionId` on session-scoped frames and deletion of `terminate`

Targets: `pkg/adapter/runtimeops.go`, `pkg/adapter/checkpoint.go`, `pkg/adapter/lifecycle.go`, `pkg/adapter/credentials.go`, `pkg/adapter/staging.go`, `pkg/adapter/usage.go`, `pkg/adapter/adapterevents.go`, `cmd/lenny-adapter/main.go`, `pkg/runtimekit/lifecycle.go`, and the tests listed in items 4 and 5.

1. `lifecycleFrame` gains `SessionID string \`json:"sessionId,omitempty"\``. The six session-scoped senders set it; `lifecycle_capabilities` and `lifecycle_support` leave it empty.
2. `RequestCheckpoint`, `CompleteCheckpoint`, `RequestInterrupt`, `RotateCredentials`, `SignalDeadlineApproaching`, and `SignalFilesUpdated` each take `sessionID string` as their first argument after `ctx` where they take one, and before their other arguments otherwise. Each caller passes the session it already holds: the checkpoint path in `checkpoint.go`, `SignalDeadline` and `interruptViaLifecycle` in `lifecycle.go`, the rotation in `credentials.go`, and the upload promotion in `staging.go`.
3. Delete `RuntimeOps.Terminate` and `TestRuntimeOpsTerminate`, and remove `terminate` from the `RuntimeOps` type comment.
4. In the same commit, update every peer and test that builds or decodes these frames: `pkg/runtimekit/lifecycle.go` and its tests; the checkpoint, interrupt, and credential checks in `cmd/lenny-compliance/full.go` (each send carries `sessionId`, preceded by that session's `session_start` on `CH-MSGSOCK`); `tests/testinfra/rotationgate/rotationgate.go`; the rotation peers in `tests/tier8_chaos/credential_rotation_ceiling_test.go`, `tests/tier9_security/credential_rotation_cotenant_inflight_gate_test.go`, and `tests/tier10_conformance/credential_path_resolution_conformance_test.go`; and `pkg/adapter/{credentials,lifecycle,checkpoint_stream,files_updated,rotationgate,runtimeops}_test.go`.
5. `RuntimeOps.readLoop` passes the `llm_request_completed` frame's `SessionID` to the token sink, and `tokenSink.AddTokens` takes the session. `NewSessionTokenSink` (`pkg/adapter/usage.go`) takes the bound-session check `Server.checkSessionBound` uses in place of `soleSession`, and drops counts for an empty or unbound session. The in-flight decrement stays unconditional and runs first, so an unattributed frame cannot wedge credential rotation. Keep `Server.SoleSessionID`, and update its doc comment and the token-sink comment in `cmd/lenny-adapter/main.go`, which name it as the sink's session resolver. In the same commit, update `pkg/adapter/usage_test.go` and every test that writes an `llm_request_completed` frame to name the session. The `usage_test.go` cases that discriminate are one fold under the named session and one drop for an unbound session.

This step lands immediately after SCHEMA-1, which makes `sessionId` required on these frames. The SDK consumer side (routing by `sessionId`) lands in CODE-4 and CODE-5, and the SDK and reference-runtime `terminate` handlers are deleted in CODE-6.

### CODE-4 · Go runtime SDK serves sessions keyed by `sessionId`

Targets: `sdks/runtime/go/runtime/runtime.go`, `types.go`, `context.go`, `mcp.go`, `lifecycle.go`, `options.go`, and `sdks/runtime/go/example/{echo,delegate,lifecycle}`.

1. Frame loop. The loop reads every inbound frame and never blocks on one session's work. It routes `session_start`, `message`, `tool_result`, and `session_end` to the addressed session's state; it handles `heartbeat` and `shutdown` itself. Each session has its own goroutine and its own queue. **IMPLEMENTOR'S CHOICE:** the queue implementation. A full queue may block delivery only to its own session, never the loop.
2. Session state. `session_start` creates a `sessionState` holding a context derived from the process context and its cancel function, the parsed frame, the `CredentialBundle` loaded from the frame's `credentialsPath` (none when the member is absent), the session's `Tools`, a create error, and a sequence counter. A duplicate `session_start` is ignored. The SDK then invokes `OnCreate` with a `CreateRequest` whose `SessionID` and `TaskID` are the frame's `sessionId` and whose `ExperimentContext`, `TracingContext`, and `LLM` come from the frame.
3. Messages. `Message.SessionID` and `Message.TaskID` come from the inbound frame's `sessionId`, and `Message.Sequence` is the session's counter. A message for a session with no state, or whose create failed, is answered as SPEC-3 **Session errors.** states, with `error.code` `RUNTIME_ERROR`, and is not dispatched. The process keeps running.
4. `session_end`. The session's goroutine cancels the session context, discards queued messages without dispatching them, cancels the session's pending `tool_call` waiters, waits for the in-flight handler to return, calls `OnTerminate(ctx, sessionID, TerminationReason{Reason: "session_end"})`, and deletes the state. With this ordering the SDK writes no `CH-MSGSOCK` frame for the session after its `session_end`, and a `CH-MSGSOCK` write after `OnTerminate`, from a goroutine the handler leaked, is logged and dropped. `Lifecycle.Send` applies no per-session filter, because SPEC-3 **Inbound: `session_end`** rule 2 admits a late `llm_request_completed`. A `session_end` for an unknown session is ignored.
5. EOF and `shutdown` run the item 4 teardown for every live session concurrently, then `Run` returns.
6. MCP. The platform and connector MCP connections stay process-scoped: `startChannels` dials them once per process with `CH-RUNTIMEOPS` and keeps its current hard-error and degrade behavior. A failed credential-file read sets that session's create error and does not end the process.
7. Handler signature. `Handler.OnTerminate` becomes `OnTerminate(ctx context.Context, sessionID string, reason TerminationReason) error`. The `Handler` doc comment states that `OnCreate` and `OnTerminate` run once per session and that handlers for different sessions run concurrently and must be safe for concurrent use; the comment that a recycled session gets a fresh `OnCreate` after the runtime exits is removed. The `types.go` comments on `CreateRequest` and `Message` follow SPEC-5.
8. Credentials. Delete `resolvedCredentialsPath`'s fallbacks to the manifest's `credentialsPath` and to `WithCredentialsPath`, and delete the `WithCredentialsPath` option. The `session_start` `credentialsPath` is the only source. `credentials_rotated` reloads the bundle of the session its `sessionId` names.
9. Lifecycle routing (the SDK half of SPEC-4). `OnCheckpoint`, `OnInterrupt`, and `OnCredentialsRotated` callbacks gain a leading `sessionID string` parameter, and `LifecycleEvent` gains `SessionID`. A `CH-RUNTIMEOPS` event for an unknown session is logged and ignored.
10. `AdapterManifest` drops `SessionID`, `TaskID`, `CredentialsPath`, `ExperimentContext`, `TracingContext`, and `LLM`. `withSessionContext` takes the `sessionState`, which carries the session's context, credentials, tools, and the process-scoped `Lifecycle`.
11. The echo example reports the session's `SessionID` and experiment variant in its reply text, so TEST-1's tier-10 cases can observe the context. The delegate and lifecycle examples adopt the new `OnTerminate` and lifecycle callback signatures.
12. In the same commit, retarget the Go cases of `tests/tier10_conformance/credential_path_resolution_conformance_test.go` from `…FromTheManifest` to `…FromSessionStart`, including the rotation case, and update `runtime_test.go`, `lifecycle_test.go`, and `mcp_test.go` for the new behavior.

### CODE-5 · Python and TypeScript runtime SDK ports

Targets: `sdks/runtime/python/lenny_runtime/{runtime,types,mcp,lifecycle}.py` and examples; `sdks/runtime/typescript/src/{runtime,types,mcp,lifecycle}.ts` and examples.

Mirror CODE-4 in each SDK's existing concurrency model.

1. Python stays thread-based and synchronous, with no asyncio and no change to the synchronous handler API. Replace the single `_dispatch_worker` thread and its `queue.Queue` with one worker thread and one `queue.Queue` per live session, held in a lock-guarded dict keyed by `sessionId`. Each entry holds that session's `CreateRequest`, `CredentialBundle`, credentials path, `Tools`, create error, and sequence counter. Retire the per-process fields `_credentials`, `_credentials_path`, `_tools`, and `_sequence`, and the manifest-derived `session_id` and `task_id` in `_handle_message` and `_build_create_request`.
2. TypeScript replaces the single `dispatchTail` with a per-session promise chain held in a `Map` keyed by `sessionId`. The fields `credentials`, `credPath`, `tools`, and `sequence` move into the per-session record.
3. In both SDKs: `Message.session_id`/`sessionId` and `task_id`/`taskId` come from the frame's `sessionId`; a message for an unknown session, or for one whose create failed, is answered as SPEC-3 **Session errors.** states with `RUNTIME_ERROR` and is not dispatched; a duplicate `session_start` and an unknown-session `session_end` are ignored; `session_end` runs the CODE-4 item 4 ordering; EOF or `shutdown` ends every live session; MCP clients stay process-scoped as CODE-4 item 6 states.
4. `on_terminate(session_id, reason)` and `onTerminate(sessionId, reason)` take the session as a parameter, matching CODE-4 item 7. The handler docstrings state that `on_create`/`onCreate` and `on_terminate`/`onTerminate` run once per session, and that handlers for different sessions run concurrently (on different threads in Python) and must be safe for that.
5. `lifecycle.py` and `lifecycle.ts` route `CH-RUNTIMEOPS` events by `sessionId` as CODE-4 item 9 does, with session-aware callbacks and per-session credential reload.
6. The echo, delegate, and lifecycle examples surface the session's id and experiment variant for TEST-1's tier-10 cases.
7. Neither SDK gains a `status` helper.
8. In the same commit, retarget the Python and TypeScript cases of `tests/tier10_conformance/credential_path_resolution_conformance_test.go` to `…FromSessionStart` and update both SDKs' unit tests.

### CODE-6 · Compliance harness, reference runtimes, and deletion of the runtime-side `terminate` handlers

Targets: `cmd/lenny-compliance/{main,standard,full}.go` and their tests (`full_test.go`, `standard_test.go`, `deadline_test.go`), `cmd/runtimes/echo-concurrent/{dispatch,main_test}.go`, `cmd/runtimes/streaming-echo/main.go`, `sdks/runtime/go/runtime/lifecycle.go`, `sdks/runtime/python/lenny_runtime/lifecycle.py`, `sdks/runtime/typescript/src/lifecycle.ts`, `tests/tier3_contract/sdks/runtime_sdk_test.go`.

1. `main.go` and `standard.go`: one shared helper prefixes each check's input with `{"type":"session_start","sessionId":<complianceSessionID>,"experimentContext":null,"tracingContext":null,"llm":null}`, which omits `credentialsPath` as SPEC-3's field table allows. Every check that sends a session-scoped frame uses it. The binary-executes, empty-stdin, heartbeat, unknown-type, and shutdown checks stay unchanged.
2. Add the `session_lifetime` check that SPEC-6 **session lifetime** states, replacing the code-only `sequential_messages_handled` check. A runtime that exits fails it.
3. `full.go`: rewrite `checkDeadlineSignal` to drive SPEC-6's Full **deadline signal handling** row in place of the `terminate` frame. The `response` it asserts carries the session's `sessionId`, and the closing `heartbeat_ack` assertion shows the process is still alive.
4. In the same commit, delete the runtime-side `terminate` handlers: `case "terminate"` and `handleTerminate` in the Go SDK, `_handle_terminate` in the Python SDK, the `terminate` case in the TypeScript SDK, and the `terminate` case in `cmd/runtimes/streaming-echo/main.go`. Landing them together keeps the Full battery green at every step.
5. `echo-concurrent/dispatch.go`: on `session_end`, close and wait for that session's worker and delete it from the map; ignore `session_end` for an unknown session; treat `session_start` as creating or keeping the worker. Add tier-1 tests in `main_test.go`.
6. `tests/tier3_contract/sdks/runtime_sdk_test.go`: assert that the `session_lifetime` check passes for each SDK example.

### CODE-7 · Part B (separable): adapter handshake on both runtime listeners and the SDK client half

Targets: `pkg/adapter/intrapodauth.go` (new), `pkg/adapter/socketruntime.go`, `pkg/adapter/runtimeops.go`, `pkg/adapter/mcp/challenge.go`, `pkg/runtimekit/transport.go`, `sdks/runtime/go/runtime` (both dials), `sdks/runtime/python/lenny_runtime/{transport,lifecycle}.py`, `sdks/runtime/typescript/src/{transport,lifecycle}.ts`, `cmd/lenny-compliance` dials, and every in-tree test or harness that dials `CH-MSGSOCK` or `CH-RUNTIMEOPS` (`pkg/adapter/*_test.go`, the tier-4, tier-7a, tier-9, and tier-10 files that dial either socket, the tier-3 SDK harness, and `cmd/lenny-gateway/direct_usage_quota_integration_test.go`).

1. `authenticateRuntimeConn(conn net.Conn, br *bufio.Reader, nonce func() string, nonceOnly bool) error` implements SPEC-8 **Runtime connection handshake.**. It reads the first line through `br` under the 500 ms `mcp.ChallengeTimeout`, refuses an empty provider value, compares with `subtle.ConstantTimeCompare`, and in nonce-only mode issues the challenge through the existing `pkg/adapter/mcp` challenge code. Export `newChallenge` as `mcp.NewChallenge` for this adapter-side caller.
2. The nonce provider returns the `mcpNonce` of the currently published manifest. `NewSocketRuntimeProcess` and `NewRuntimeOps` take it as a constructor argument, because both listeners are built at adapter boot before any nonce exists. This deliverable does not change when or how the nonce is minted.
3. `SocketRuntimeProcess`: create one `bufio.Reader` per accepted connection, run the handshake on it after the peer check, and build the fan-out scanner in `startReaderLocked` over that same reader rather than over `p.conn`, so bytes buffered during the handshake are not lost. A failed handshake follows the existing refuse, log, and keep-accepting path of `logRefusedPeer`.
4. `RuntimeOps.Run`/`serveConn`: run the handshake on the connection's reader after the peer check and before `handshake` sends `lifecycle_capabilities`, with the same refusal path.
5. Client half: one exported helper in `pkg/runtimekit` beside `DialSocket` waits for the manifest file to exist (polling at a short interval with no overall deadline), dials, writes the `{"_lennyNonce":"<nonce>"}` line, and answers a `_lennyChallenge` that arrives before the first protocol frame, and when the connection closes before that frame it reads the manifest again and redials at the same polling interval. The Go SDK, the reference runtimes, `cmd/lenny-compliance`, the tier-3 SDK harness, and every in-tree Go test that dials either socket use it. Python and TypeScript implement the same client half in their own transport and lifecycle modules.
6. Update the header comments of `socketruntime.go` and `runtimeops.go` that say the handshake is not performed.

The adapter and every in-tree client change in one commit, because a server that requires the line refuses every client that lacks it.

### CODE-8 · Adapter stops writing the per-session manifest fields

Targets: `pkg/adapter/manifest.go`, `pkg/adapter/manifest_test.go`, `pkg/adapter/manifest_fields_test.go`, `pkg/adapter/sessionframes_test.go`, `cmd/lenny-compliance/full_test.go`, `tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go`, and every tier-2, tier-7a, tier-8, and tier-9 test that reads a removed field (find them by grepping for the removed `Manifest` field names).

1. Delete `SessionID`, `TaskID`, `CredentialsPath`, `ExperimentContext`, `TracingContext`, and `LLM` from `Manifest`, and stop setting them in `writeSessionManifest`. Keep the builders CODE-1 reuses, and keep `manifestInputs` members the frame builder reads. `ManifestVersion` stays 1.
2. Rewrite the `Manifest` doc comment to describe a pod-scoped document whose per-session context moved to `session_start`, and rewrite the `MCPNonce` comment to drop "alongside the rest of the manifest" while still stating that the current writer mints a nonce on each manifest write.
3. Move the `llm` and `credentialsPath` cases of `manifest_fields_test.go` into `sessionframes_test.go` as frame-builder tests, keeping the unsafe-path rejection case and asserting that a proxy-mode `llm` carries no `baseUrl`. Update the `manifest_test.go` assertions. Change the tier-7a race test to assert the `session_start` frame in place of the manifest `sessionId`.

## Staged schema, chart, and migration changes

### SCHEMA-1 · Frame schemas, fixtures, and the gates that pin them

Targets: `schemas/lenny-adapter-jsonl.schema.json`, `schemas/runtime-ops-events.schema.json`, `schemas/examples/`, `tests/tier0_static/schemas_test.go`, `tests/tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go`, `tests/tier11_docs/compliance_suite_artifact_enumeration_test.go`.

1. `lenny-adapter-jsonl.schema.json`:
   - Add `$defs.session_start`: `type` const `session_start`; `sessionId` `{type: string, minLength: 1}` with a description stating that the adapter populates it on every pod; `credentialsPath` `{type: string, minLength: 1}`; `experimentContext` `{experimentId, variantId, inherited}` object or `null`; `tracingContext` object of string values or `null`; `llm` object or `null` with `deliveryMode` (enum `direct`, `proxy`, required), `dialect`, `apiKeyEnv`, and `headers` (object of string values). `required` is `["type", "sessionId"]`, and the `credentialsPath` description states it is present whenever the adapter provisioned a credential file for the session and absent otherwise.
   - Add `$defs.session_end`: `type` const `session_end` and `sessionId` as above; `required` is `["type", "sessionId"]`.
   - Add both to the root `oneOf`.
   - Rewrite the `shutdown` description as process-scoped and never written at a session boundary, and remove `session_complete` from the `reason` example list.
2. `runtime-ops-events.schema.json`: add a required `sessionId` (`string`, `minLength: 1`, with a description naming the session the frame concerns) to `checkpoint_request`, `checkpoint_complete`, `interrupt_request`, `credentials_rotated`, `deadline_approaching`, `files_updated`, and `llm_request_completed`. Delete `$defs.terminate` and its `oneOf` entry.
3. Fixtures: add `schemas/examples/jsonl.session_start.json` and `jsonl.session_end.json` and validate them in `TestAdapterJSONLExamplesValidate`; add `sessionId` to the existing runtime-ops fixtures of the frames item 2 names and add `runtime-ops.files_updated.json`; delete `schemas/examples/runtime-ops.terminate.json` and its entry in `runtimeOpsEventExamples`; update the `schemas_test.go` guard that names `terminate`.
4. Add `session_start` and `session_end` to `sessionScopedFrames` and `adapterPopulatedFrames` in `session_scoped_frame_address_test.go`.
5. In `compliance_suite_artifact_enumeration_test.go`, replace `terminate` in the runtime-ops frame list and its fixture table text with a frame that still exists.

No chart or migration change is staged.

## Staged docs changes

### DOCS-1 · Runtime-author documentation and the tier-11 tests that pin it

Targets: `docs/reference/adapter-contract.md`; `docs/runtime-author-guide/{lifecycle,runtime-sdk,integration-levels,runtime-configuration,platform-tools,testing,local-development,echo-runtime}.md`; `docs/runtime-author-guide/sdk-examples/{go,python,typescript}.md`; `docs/tutorials/build-a-runtime.md`; `tests/tier11_docs/adapter_manifest_credentials_path_doc_reconciliation_test.go`, `adapter_manifest_rewrite_trigger_doc_test.go`, `adapter_manifest_session_identifier_currency_doc_test.go`, and `intra_pod_mcp_nonce_doc_reconciliation_test.go`; `docs/getting-started/concepts.md`, `docs/tutorials/evaluation-scoring.md`, `docs/api/admin.md`, `docs/api/rest.md`, `docs/about/why-lenny.md`, `docs/reference/glossary.md`, and `docs/operator-guide/configuration.md`.

1. `adapter-contract.md` is the single reference home. Define `session_start` and `session_end` in its `CH-MSGSOCK` section, each with a fenced JSON block carrying `sessionId`, and add the required `sessionId` to the session-scoped `CH-RUNTIMEOPS` rows. Delete the moved manifest rows, the `taskId` row, the per-session members of the manifest example, the `terminate` row, and `terminate` from the schema list and the **Canonical artifacts** table. Rewrite the manifest lead so it describes a pod-scoped file. Do not write a removal or history note. Describe the per-session LLM API key only in this page's `llm.apiKeyEnv` row.
2. `lifecycle.md` states the runtime process lifetime (one process serves many sessions, each bracketed by the frames, and connection end or `shutdown` ends them all), replaces the `terminate` section with `session_end` handling, and links to `adapter-contract.md` for the frames.
3. `runtime-sdk.md` states per-session `OnCreate`, `OnMessage`, and `OnTerminate`, concurrency across sessions, and the new `OnTerminate` signature in its sample.
4. `runtime-configuration.md` points per-session setup at `session_start` and `OnCreate`.
5. `integration-levels.md` retargets the deadline row to `deadline_approaching` addressed by `sessionId` and states that the runtime keeps running. If Part B stays, it and `platform-tools.md`, `testing.md`, and `local-development.md` add the `_lennyNonce` first line on `CH-MSGSOCK` and `CH-RUNTIMEOPS` and the wait for the manifest before dialing, and `lifecycle.md` also replaces its Basic-level statement that the manifest is not required for core operation with the `mcpNonce` read on a socket connection. Every phrase that `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` pins in these pages stays.
6. The SDK example pages and the tutorial drop `taskId` and the per-session fields from their manifest structs and take session context from `session_start`.
7. `echo-runtime.md` adds one sentence: the echo runtime ignores `session_start` and `session_end` under the unknown-type rule.
8. Retarget the three tier-11 `adapter_manifest_*` tests in the same commit so that each pins the frame carrier instead of the manifest rows, and, in `intra_pod_mcp_nonce_doc_reconciliation_test.go`, retarget the `adapter-contract.md` manifest-lead site's currency phrase to the pod-scoped lead that item 1 writes. That site's anchor sentence and nonce rules stay.
9. In `concepts.md`, `evaluation-scoring.md`, `admin.md`, `rest.md`, `why-lenny.md`, `glossary.md`, and `configuration.md`, every sentence or heading that names the adapter manifest as the carrier of `experimentContext` or of the chosen variant names the session's `session_start` frame instead.
10. Follow `doc-style.md` and `doc-content.md`, and cite no spec section number.

### RECORDS-1 · Gap bookkeeping

Target: `BUILD-GAPS.md`.

1. F-4.7.26: add a partial-progress note. The per-session context half is closed by the `session_start` and `session_end` frames, and the first-session manifest-ordering half stays open for 0087's supervisor.
2. F-4.7.25: if Part B stays in this proposal, add a note that CODE-7 closes the nonce half once both listeners enforce the handshake; otherwise leave the row unchanged.
3. New finding: "The runtime SDKs' `CreateRequest.RuntimeOptions` and `CreateRequest.WorkspacePlan` are never populated." Give each field its own evidence. `RuntimeOptions` has no carrier from the gateway to the adapter (no field in `schemas/lenny-adapter.proto` carries it) and none from the adapter to the runtime (the Go SDK reads `manifest.RuntimeOptions`, which the adapter's `Manifest` never writes). `WorkspacePlan` reaches the adapter through `FinalizeWorkspaceRequest.workspace_plan`, but no carrier delivers it to the runtime and `CreateRequest.WorkspacePlan` stays nil.
4. New finding: the runtime SDKs' `TerminationReason` (`{Reason, DeadlineMS}` in Go) differs from the §15 shared `TerminationReason` (`{Code, Detail}`).
5. New finding: no adapter code populates the `llm.headers` member that §4.9 and the `CH-LLMPROXY` card advertise.

## Testing

### TEST-1 · New cross-component tests

Every new test carries a `// spec:` annotation in the form `test-coverage.md` fixes, and every tier-2-and-higher test carries a `// diagnosis:` comment. The existing tests each deliverable breaks are updated inside that deliverable, as its text states.

| Behavior | Tier | Test | Non-happy path it covers | `// spec:` |
|:--|:--|:--|:--|:--|
| The adapter writes `session_start` after `Runtime.Start` and before the first message, on the pod-warm, resume, and SDK-warm fresh paths, and writes none for `RuntimeKindMCP` | 1 | `pkg/adapter/sessionframes_test.go` | A failed `session_start` write releases the slot and fails the start TRANSIENT; an absent credentials root omits `credentialsPath` | 28.5.3 (CH-MSGSOCK session_start), 4.7.10 (Runtime process lifetime) |
| The adapter writes `session_end` before `Runtime.Close` on `Shutdown`, on a rule-8 rollback that finds no entry, and on the embedded hold-timeout close, and skips it on a rule-8 rollback that finds a successor's entry and after `CloseListener` | 1 | `pkg/adapter/sessionframes_test.go` | A failed `session_end` write does not fail the teardown; `refuseUnconfirmedSDKWarmStart` writes none; an interrupt writes none | 28.5.3 (CH-MSGSOCK session_end), 4.7.1 (Shutdown) |
| The developer-loop executor writes `session_start` only for a child that `Send` spawned | 1 | `pkg/gateway/session/executor/subprocess_test.go` | A `Start`-spawned child receives no executor frame; a second `Send` writes no second frame | 28.5.3 (CH-MSGSOCK session_start), 17.4 (Local Development Mode) |
| The developer-loop frame validates against the schema | 3 | `tests/tier3_contract/adapter_jsonl/subprocess_envelope_address_test.go` | The `sessionId`-only frame with no other member is valid | 28.5.3 (CH-MSGSOCK session_start) |
| Wire order on one runtime connection: `session_start` precedes the session's first `message`, `session_end` precedes close, and a runtime that ignores both still answers `heartbeat` and `shutdown` | 3 | `tests/tier3_contract/adapter_jsonl/` (new case) | An echocore-based runtime that ignores both frame types | 28.5.3 (CH-MSGSOCK) |
| Two sequential sessions over one runtime socket are each bracketed by their own frames | 4 | Extend `TestAdapterServesTwoSequentialSessionsOverOneRuntimeSocket` in `tests/tier4_integration/runtime_socket_lifetime_test.go` with a recording socket peer that logs the `type` and `sessionId` of every frame it receives; keep `cmd/runtimes/echo` for the existing sequential-response assertion | The frames for A and B never interleave across the A teardown, and no `session_end` follows the hold-timeout `CloseListener` | 4.7.10 (Runtime process lifetime), 28.5.3 (CH-MSGSOCK) |
| The Go SDK keeps sessions independent | 1 | `sdks/runtime/go/runtime/runtime_test.go` | A `session_end` while session A's handler is blocked in `ToolCall` does not delay session B's message; no `response` for A follows A's `session_end`; a message for an unknown session gets `RUNTIME_ERROR` and the process keeps running; a duplicate `session_start` is ignored; a failed credential-file read sets only that session's create error | 15.7 (Runtime Author SDKs), 28.5.3 (CH-MSGSOCK session errors) |
| The Python and TypeScript SDKs keep sessions independent | 1 | Each SDK's unit-test suite | The Go cases above, per SDK | 15.7 (Runtime Author SDKs) |
| Interleaved start, message, and end cycles in the Go SDK are race-free | 7a | `tests/tier7a_load_local/` (new), run under `-race` | Many concurrent sessions opened and ended while messages are in flight | 15.7 (Runtime Author SDKs), 4.7.10 (Runtime process lifetime) |
| A runtime serves sequential sessions with different contexts on one process, and two concurrent sessions on a concurrent pool | 10 | New cases beside `TestConcurrentSessionDispatchConformance` in `tests/tier10_conformance/concurrent_slot_conformance_test.go`, run at Basic level against each SDK's echo example | B's reply names B's `sessionId` and experiment variant, and the process is still alive after A's `session_end`; credential paths are covered by the retargeted `credential_path_resolution` cases rather than here | 4.7.10 (Runtime process lifetime), 15.4.6 (session lifetime) |

The tier-10 sequential case and CODE-6's `session_lifetime` compliance check share one fixture sequence, which CODE-6 defines in `cmd/lenny-compliance` and the tier-10 case reuses. The Standard-level sequential and concurrent MCP cases are not staged; they wait on proposal 0084.

Part B tests (CODE-7): at tier 1 on both listeners, a missing nonce line, a wrong nonce, silence past 500 ms, a bad HMAC, and an answered challenge, each followed by acceptance of the next good connection, plus a client-helper test that answers a challenge arriving before the first protocol frame and one that redials with the re-read nonce after the adapter closes a connection presenting a replaced nonce; at tier 9 in `tests/tier9_security/runtime_socket_peercred_test.go`, refusal of a nonce-less same-UID peer in nonce-only mode. Cite `// spec: 4.7.11 (Runtime connection handshake)`.

Each step runs the tiers its checklist line names. Coverage of changed lines is checked with `lenny-test coverage --diff <base-ref>` before each code step is declared done.

## Edge cases and accepted failure modes

- A `session_end` write that fails on a live connection is logged and dropped. The partial-write hazard on the shared connection predates this proposal and applies to every frame.
- A refused SDK-warm start writes no `session_end`; `DemoteSDK` ends the embedded loop, which ends the session.
- Under the developer loop no credential file is provisioned, so the SDK loads no credential bundle for the session.
- A runtime process that crashes takes every session's context with it; the pod is retired and a resume on a fresh pod re-delivers the context in a new `session_start`.

## Files touched on application (non-spec)

- `pkg/adapter/sessionframes.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/holdstate.go`, `pkg/adapter/runtimegeneration.go`, `pkg/adapter/manifest.go` (CODE-1)
- `pkg/gateway/session/executor/subprocess.go` (CODE-2)
- `pkg/adapter/runtimeops.go`, `pkg/adapter/checkpoint.go`, `pkg/adapter/lifecycle.go`, `pkg/adapter/credentials.go`, `pkg/adapter/staging.go`, `pkg/adapter/usage.go`, `pkg/adapter/adapterevents.go`, `cmd/lenny-adapter/main.go`, `pkg/runtimekit/lifecycle.go`, `tests/testinfra/rotationgate/rotationgate.go`, and the tests CODE-3 items 4 and 5 list (CODE-3)
- `sdks/runtime/go/runtime/*.go`, `sdks/runtime/go/example/*`, `tests/tier10_conformance/credential_path_resolution_conformance_test.go` (CODE-4)
- `sdks/runtime/python/lenny_runtime/*.py`, `sdks/runtime/typescript/src/*.ts`, their examples, `tests/tier10_conformance/credential_path_resolution_conformance_test.go` (CODE-5)
- `cmd/lenny-compliance/*.go`, `cmd/runtimes/echo-concurrent/*.go`, `cmd/runtimes/streaming-echo/main.go`, the three SDK lifecycle files, `tests/tier3_contract/sdks/runtime_sdk_test.go` (CODE-6)
- `pkg/adapter/intrapodauth.go`, `pkg/adapter/socketruntime.go`, `pkg/adapter/runtimeops.go`, `pkg/adapter/mcp/challenge.go`, `pkg/runtimekit/transport.go`, the SDK transport and lifecycle files, and every in-tree dialer CODE-7 lists (CODE-7)
- `pkg/adapter/manifest.go` and the manifest-reading tests CODE-8 lists (CODE-8)
- `schemas/lenny-adapter-jsonl.schema.json`, `schemas/runtime-ops-events.schema.json`, `schemas/examples/*`, `tests/tier0_static/schemas_test.go`, `tests/tier3_contract/adapter_jsonl/session_scoped_frame_address_test.go`, `tests/tier11_docs/compliance_suite_artifact_enumeration_test.go` (SCHEMA-1)
- `docs/reference/adapter-contract.md`, the runtime-author-guide pages, `docs/tutorials/build-a-runtime.md`, the experiment-context pages DOCS-1 item 9 lists, and the three tier-11 `adapter_manifest_*` tests (DOCS-1)
- `tests/tier11_docs/intra_pod_mcp_nonce_doc_reconciliation_test.go` (SPEC-2, DOCS-1)
- `BUILD-GAPS.md` (RECORDS-1)
- New tests listed under TEST-1 (TEST-1)
