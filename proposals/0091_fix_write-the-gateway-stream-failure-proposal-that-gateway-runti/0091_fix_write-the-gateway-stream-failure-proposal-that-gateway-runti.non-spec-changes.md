# Non-spec changes: Gateway does not survive or react to Attach stream failure

## Design (implementation-facing)

The pod executor holds one `Attach` stream per session on a session-scoped context. One reader goroutine per stream consumes every frame. A one-slot turn token serializes turns on the stream, so the runtime's reply to an abandoned turn is consumed by the reader and never delivered to the next turn. The reader also observes every end of the stream, including an end while no message is outstanding. It evicts the stream from the cache and, for the ends SPEC-2b names as stream failures, calls a handler that the gateway injects. The handler reports `runtime_crash` through `Server.ReportSessionFailure`. The release that retires the pod runs in one place, `applyFailureFromActive`, so every reporter gets it.

The parts land in this order, and a later part depends on each earlier one:

1. CODE-1 (the held stream, the reader, and the turn token) lands before any reporter is wired. Without it, every second message on a session would end the stream and would be reported as a failure.
2. CODE-2 (the release funnel) lands before CODE-3 wires the detector, so no commit reports a failure that nothing releases.
3. CODE-3 wires the detector.

The change adds no wire field, RPC, error code, session state, or `BindResult` field. The only new exported identifier is the `SetStreamFailureHandler` method.

## Staged code changes

### CODE-1 · Session-scoped `Attach` stream with one reader, a turn token, cancel-based eviction, and eviction on end

Targets: `pkg/gateway/session/executor/pod.go` (`PodExecutor.streams`, `streamFor`, `Send`, `EvictStream`, `readAttachResponse`, `maybeGateToolCall`); `pkg/gateway/session/executor/podstream.go` (new file in the existing package); `pkg/gateway/runtime/adapterclient/client.go` (`Attach` doc comment only).

Spec ties: §28.5.1 `CH-ATTACH` **Timing.** (as SPEC-2a states it), §28.5.3 `CH-MSGSOCK` **Timing.**, §7.2 tool-use approval.

1. **`attachConn`.** Add the unexported type `attachConn` in `podstream.go`. It holds the `*adapterclient.AttachStream`, the stream's context and its `cancel`, the session's `tenantID`, `sessionID`, and `sandboxName` (read from the binding at open), a `sendMu sync.Mutex`, a `closedByGateway` flag (`atomic.Bool`), the turn token (a channel of capacity 1), the registered turn (guarded by a small mutex), and a `done` channel that the reader closes when it exits. `PodExecutor.streams` becomes `map[string]*attachConn`.
2. **Single writer.** Every write to the stream goes through `conn.send(b []byte) error`, which holds `sendMu` around `AttachStream.Send`. Both the `Send` path and the reader's approval relay use it. gRPC `SendMsg` is not safe for concurrent calls, so no other code calls `AttachStream.Send` directly.
3. **Open.** On a cache miss, `streamFor` opens the stream on `context.WithCancel(context.WithoutCancel(ctx))`, stores the cancel on the conn, caches the conn, and starts the conn's reader goroutine. It keeps holding `e.mu` across the open, as today, so a session never races two streams into existence. The empty-session-identifier guard stays first.
4. **Turn.** For each message, `Send`:
   1. Acquires the turn token, selecting on the token, `ctx.Done()`, and `conn.done`.
   2. Registers the turn with a result channel of capacity 1.
   3. Writes the envelope through `conn.send`.
   4. Waits on the result channel, `ctx.Done()`, and `conn.done`.

   On `ctx.Done()`, `Send` returns the context error, wrapped, and leaves the turn registered and the token held. Only the reader completes a turn. It does so when it consumes a `response` frame, or when the stream ends. Completing a turn clears the registration and returns the token. On `conn.done`, `Send` returns a wrapped stream-ended error. The tenant for the approval gate comes from the conn rather than a fresh registry read.
5. **Reader.** The reader loops on `Recv` and handles each frame in this order:
   1. When an approval gate is wired, the reader gates a `tool_call` frame carrying `approvalRequired: true` on the conn's context with the conn's `tenantID`, whether or not a turn is registered, and relays the verdict through `conn.send`. A gate error is written to the registered turn's result channel, if one exists, and the turn stays registered until its `response` arrives. A relay write error is dropped, and the reader's next `Recv` returns the stream's end for `endConn` to classify.
   2. A `response` frame completes the registered turn by writing to its result channel. A `response` that arrives while no turn is registered is discarded.
   3. Every other frame is discarded, as `readAttachResponse` skips such frames today.

   The reader blocks only in `Recv`, in the approval gate, and in `conn.send`. It never waits on a consumer, so the adapter's per-subscriber intake never fills.
6. **End.** When `Recv` returns an error or `io.EOF`, the reader runs `endConn`:
   1. If `closedByGateway` is set, `endConn` closes `done` and returns.
   2. Otherwise it takes `e.mu` and deletes the cache entry only if the cached pointer is this conn.
   3. It cancels the conn's context and closes `done`. Eviction happens before `done` closes, so a `Send` that observes `done` and retries re-Attaches over the binding.
   4. It runs the classification CODE-3 adds.

   Lock order: `e.mu` before any conn mutex. The reader holds no conn mutex while it takes `e.mu`.
7. **`EvictStream`.** Under `e.mu`, set `closedByGateway`, call the stored cancel, and delete the entry. Do not call `CloseSend`. `Release` and the coordination Sweeper's `EvictBinding` keep calling `EvictStream` unchanged.
8. **Removal.** Delete `readAttachResponse`. Change `maybeGateToolCall` to take the conn in place of the stream, so it writes through `conn.send` and gates on the conn's context.
9. **`adapterclient.Attach` doc comment.** State that the executor holds the stream on a session-scoped context and cancels it on eviction.
10. **Implementor verification.** Before CODE-3 wires the handler, confirm that no gateway-initiated operation other than `EvictStream` or `Release` ends a session's `Attach` stream with a status CODE-3 reports. An `Interrupt` on the embedded runtime, which closes the loop (`pkg/adapter/embedded.go` `Interrupt`), is the case to check. If an operation does, its caller calls `EvictStream` before it issues the operation.

### CODE-2 · Release with the `failed` disposition and count the failed slot in `applyFailureFromActive`; release any prior binding before a resume rebinds

Targets: `pkg/gateway/sessionserver/failure.go` (`applyFailureFromActive`, `transitionToFailed`); `pkg/gateway/sessionserver/slot_bind.go` (new unexported `Server.accountFailedSlot`); `pkg/gateway/sessionserver/resume_rebind.go` (`resumeOnPod`).

Spec ties: §7.3 "Resume flow after pod failure", §6.2 "Pod crash during an active session" (as SPEC-1c states it), §5.2 **Failure isolation:** (as SPEC-4 states it) and **Whole-pod replacement trigger:**.

1. **Snapshot.** `applyFailureFromActive` reads `bind, bound := s.podRegistry.Get(row.ID)` before its switch.
2. **`failed` edge.** Thread the snapshot into `transitionToFailed`, through a parameter or a narrow pre-terminal hook. Inside `transitionToFailed`, after `store.Update` commits a real active-to-`failed` transition and before `s.recordSessionCompleted`, call `s.accountFailedSlot(ctx, row, bind)` when `bound` and `bind.SlotID != ""`. `recordSessionCompleted` already releases with `DispositionFailed`, so this edge adds no release. The accounting must run before that release: on a recycling concurrent pool, `ReleaseSlot` at occupancy zero patches the claim to recycling and starts the scrub, so a threshold-crossing `DrainSandbox` stamped afterwards can arrive after the pod has been handed on. The resuming source does not take this path, because `applyFailureFromResuming` is unchanged.
3. **`resume_pending` and `awaiting_client_action` edges.** After `transitionToResumePending` or `transitionToAwaitingClientAction` commits a real transition, call `s.accountFailedSlot(ctx, row, bind)` when the snapshot is a slot binding, and then call `releaseExecutor(ctx, s.executor, row.ID, executor.DispositionFailed)`. A conflict or no-op return releases nothing.
4. **`accountFailedSlot`.**
   1. Resolve the pool and its `maxConcurrentSessions` from the row with `podsession.ResolvePool`, as `resumeOnPod` does. If resolution fails, log the error and skip the accounting; the release still runs.
   2. Call `accountSlotFailure` unchanged, with a constructed `*podsession.SlotBindError` carrying `Pod: bind.SandboxName` and `SlotID: bind.SlotID`, and `leaked: false`.
   3. Increment `lenny_slot_failure_total` once for the slot, with `error_type` set to the report's failure reason (`runtime_crash` for a stream failure), `pool`, and `k8s_pod_name`.

   **IMPLEMENTOR'S CHOICE:** how the increment reaches the metric. The constraint is that it uses the existing series that `gwMetrics.IncSlotFailure` emits (for example through the binder's `SlotFailure` hook) and adds no new metric.
5. **`resumeOnPod`.** Its first statement calls `releaseExecutor(ctx, s.executor, row.ID, executor.DispositionFailed)`. A release error is logged and the resume proceeds; `PodExecutor.Release` evicts the stream and removes the registry entry before it calls the binder, so the later `podRegistry.Put` or `registerBinding` does not overwrite a live entry. `Release` is a no-op when nothing is bound.

### CODE-3 · Inject the stream-failure handler and report `runtime_crash` on a bounded detached context

Targets: `pkg/gateway/session/executor/pod.go` (`SetStreamFailureHandler`); `pkg/gateway/session/executor/podstream.go` (classification in `endConn`); `pkg/gateway/sessionserver/stream_failure.go` (new file: `Server.ReportAttachStreamFailure`); `cmd/lenny-gateway/session_messaging.go` (wiring); `cmd/lenny-gateway/flags.go` (report timeout).

Spec ties: §28.5.1 `CH-ATTACH` **Degradation.** (as SPEC-2b states it), §7.3.

1. **Handler.** Add `func (e *PodExecutor) SetStreamFailureHandler(h func(tenantID, sessionID, sandboxName string, cause error))` beside `SetApprovalGate`. It is set at wiring time, before the first `Send`, and is read without a lock, as `approvals` is. A nil handler reports nothing.
2. **Classification.** In `endConn`, after the eviction step, classify by `status.Code(err)`. This implements the SPEC-2b stream-failure rule:

   | Stream end | Action |
   |:--|:--|
   | `closedByGateway` set | None. CODE-1 item 6 step 1 already returned. |
   | `codes.DeadlineExceeded` or `codes.Internal` | Call the handler with the conn's tenant, session, and sandbox. |
   | `io.EOF`, `codes.Unavailable`, `codes.FailedPrecondition`, `codes.InvalidArgument`, or any other status | No handler call. |

   The handler runs on the reader goroutine after the conn has left the cache.
3. **`Server.ReportAttachStreamFailure(tenantID, sessionID, sandboxName string, cause error)`.**
   1. Derive a context from `context.Background()` bounded by the report timeout.
   2. Confirm that `s.podRegistry.Get(sessionID)` exists and names `sandboxName`. If it does not, log that the end belongs to a superseded binding and return without reporting.
   3. Call `s.ReportSessionFailure` with `TenantID`, `SessionID`, `Reason: session.FailureRuntimeCrash`, and `PodAssignment: sandboxName`.
   4. Log the outcome with the session identifier, sandbox, status code, and resulting disposition, through `log/slog`. A report error is logged and not retried.
4. **Wiring.** In `session_messaging.go`, under the existing `exec.(*executor.PodExecutor)` assertion, call `pe.SetStreamFailureHandler(srv.ReportAttachStreamFailure)` beside `pe.SetApprovalGate`.
5. **Report timeout.** A `Server` field set at construction, default 30 s, beside the `toolApprovalTimeout` wiring. Operators override it with the gateway flag `--stream-failure-report-timeout` (environment variable `LENNY_STREAM_FAILURE_REPORT_TIMEOUT`), registered in `flags.go` with the same `envDuration` pattern as `--tool-approval-timeout` and documented as operator-tunable in its flag help.

## Staged schema, chart, and migration changes

None.

## Staged docs changes

### DOCS-1 · Reader documentation follows SPEC-1 at every exhaustion-to-`failed` site

Targets: `docs/reference/state-machines.md`, `docs/client-guide/session-lifecycle.md`, `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/delegation.md`, `docs/getting-started/concepts.md`. The pages state the behavior without spec section numbers.

1. **`docs/reference/state-machines.md`, session diagram.**
   - Change `running --> failed : Unrecoverable error / retries exhausted / BUDGET_KEYS_EXPIRED` to `running --> failed : Non-retryable error / BUDGET_KEYS_EXPIRED`, and add `running --> awaiting_client_action : Retries exhausted`.
   - Change `input_required --> failed : Pod crash (retries exhausted) / BUDGET_KEYS_EXPIRED` to `input_required --> failed : Non-retryable error / BUDGET_KEYS_EXPIRED`, and add `input_required --> awaiting_client_action : Retries exhausted`.
   - Add `suspended --> awaiting_client_action : Pod failure (retries exhausted)`, and change `suspended --> failed : BUDGET_KEYS_EXPIRED` to `suspended --> failed : Non-retryable error / BUDGET_KEYS_EXPIRED`.
   - Change the labels of `running --> resume_pending`, `input_required --> resume_pending`, and `suspended --> resume_pending : Pod failure while suspended (pod still held)` so that each names a retryable failure, for example `running --> resume_pending : Retryable failure (retries remain)`.
2. **`docs/reference/state-machines.md`, state table and transition table.**
   - The `failed` row reads `Terminal. Unrecoverable or non-retryable error, or BUDGET_KEYS_EXPIRED.`
   - The `running` → `failed`, `input_required` → `failed`, and `suspended` → `failed` transition rows name a non-retryable error rather than retry exhaustion.
   - Add transition rows `running` → `awaiting_client_action`, `input_required` → `awaiting_client_action`, and `suspended` → `awaiting_client_action`, each triggered by a pod or runtime failure with retries exhausted, with the endpoint `Internal`.
   - The `running` → `resume_pending` and `input_required` → `resume_pending` rows, and the pod-failure clause of the `suspended` → `resume_pending` row, name a retryable failure.
3. **`docs/reference/state-machines.md`, pod session-phase diagram.** Change `attached --> failed : Pod crash (retries exhausted)` to `attached --> failed : Pod crash (released with the failed disposition)`.
4. **`docs/reference/state-machines.md`, task state machine.**
   - Change `running --> failed : Crash, retries exhausted, or BUDGET_KEYS_EXPIRED` to `running --> failed : Unrecoverable error or BUDGET_KEYS_EXPIRED`.
   - Change `input_required --> failed : Pod crash (retries exhausted) / BUDGET_KEYS_EXPIRED` to `input_required --> failed : Non-retryable error / BUDGET_KEYS_EXPIRED`.
   - In the paragraph after the diagram, replace "On retry exhaustion the task transitions to `failed`." with "On retry exhaustion the underlying session enters `awaiting_client_action`, which external protocol clients see as `input_required` (see the session-level mapping below)."
5. **`docs/client-guide/session-lifecycle.md`.**
   - In the `input_required` paragraph, replace "or `failed` (on pod crash with retries exhausted)" with "`awaiting_client_action` (on pod crash with retries exhausted), or `failed` (on a non-retryable error)".
   - Add `running --> awaiting_client_action : pod crash (retries exhausted)` to the diagram.
   - The diagram's `running --> failed : unrecoverable error` edge and the `failed` row already agree and stay.
6. **`docs/runtime-author-guide/lifecycle.md`.** The `failed` row reads `Unrecoverable or non-retryable error. Terminal state.`
7. **`docs/runtime-author-guide/delegation.md`, Child Failure example.** Change `"classification": "transient"` to `"classification": "permanent"` and `"retriesExhausted": true` to `"retriesExhausted": false`.
8. **`docs/getting-started/concepts.md`.** Change `input_required --> failed: Retries exhausted` to `input_required --> awaiting_client_action: Retries exhausted`, and add `running --> awaiting_client_action: Pod failure (retries exhausted)`.
9. **Sweep.** Before landing, grep `docs/` for `retries exhausted` and `retry exhaustion` and reconcile every remaining session-state statement in the same change. Hits about credential renewal, webhooks, and checkpoint uploads are unrelated and stay.

### RECORDS-1 · Claim rows, `BUILD-GAPS.md` closure, the deferred findings, and the remediation-plan item

Targets: `tests/claim-map.json`, `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md`.

1. **New claim row** in `tests/claim-map.json`:
   - `claim`: `Coordinating replica reports a CH-ATTACH stream failure as runtime_crash, releases the pod with the failed disposition, and does not redial Attach`
   - `status`: `WIRED`
   - `spec_anchor`: `#2851-gateway-to-pod`
   - `surface`: `pkg/gateway/session/executor/podstream.go` `endConn`, `pkg/gateway/sessionserver/stream_failure.go` `ReportAttachStreamFailure`, `pkg/gateway/sessionserver/failure.go` `applyFailureFromActive`
2. **Updated claim row.** The `` `Attach` content stream `` row's surface names `pkg/gateway/session/executor/pod.go` `streamFor` and `pkg/gateway/session/executor/podstream.go` `attachConn` in place of the stale `pod.go` line, and keeps the adapter handler.
3. **No `ABSENT` row** for the card's replacement-pod re-attach sentence. The tier-0 validator accepts a `deferral_id` only when it names an R step the remediation plan declares, and none exists for re-dispatch.
4. **New `BUILD-GAPS.md` finding: no driver moves `resume_pending` to `resuming` on a replacement pod.** It states that the §28.5.1 `CH-ATTACH` **Degradation.** re-attach sentence has no claim row, and it owns adding the row together with an R step when the driver is scheduled.
5. **New `BUILD-GAPS.md` finding: a release on a replica that does not hold the in-memory binding is a no-op.** It covers watchdog and terminate releases.
6. **IMPLEMENTOR'S CHOICE:** the identifiers of the two new findings. The constraint is that each takes the next free `F-<section>.<n>` identifier in the section that owns its subject, and the summary's **Defects in the shipped tree** entries are cited by that identifier once it is assigned.
7. **Closures.** Close F-7.3.28 after CODE-1 and TEST-1 land, and F-7.3.27 after CODE-3 and TEST-2 land. Each closure cites the commits and the tests.
8. **Remediation plan.** Tick the checklist item "Gateway stream-failure proposal (not yet written)" in `gateway-runtime-comms-remediation.md` and record the proposal number and implementation date, as proposal 0090 did for its item.

## Testing

Every test carries a `// spec:` annotation in the form `// spec: N.M (section heading)`. Every test at tier 2 or higher carries a `// diagnosis:` comment above the function. Run `lenny-test coverage --diff <base-ref>` after each code step and raise changed-line coverage to the 80% floor.

### TEST-1 · Step-1 tests

Spec ties: 28.5.1 (Gateway-to-pod), 7.2 (Interactive Session Model).

Tier 1, `pkg/gateway/session/executor/pod_test.go`, against a fake `AttachStream` or a bufconn adapter:

- (a) **Lifetime.** Two `Send` calls whose contexts are cancelled after each call returns both succeed on one stream. This is the F-7.3.28 regression; the assertion that discriminates is the second reply.
- (b) **Abandon.** A `Send` whose context is cancelled mid-turn returns promptly with the context error, and the stream stays cached.
- (c) **Late reply.** After (b), the abandoned turn's late `response` is not returned by the next `Send`. The next `Send` waits for the token and receives its own reply.
- (d) **Gateway end.** `EvictStream` cancels the stream's context, which the fake adapter observes, and removes the entry.
- (e) **Eviction on end.** A stream that ends is removed from the cache, and the next `Send` re-Attaches.
- (f) **Stream end mid-wait.** A `Send` waiting on a turn returns a wrapped error when the stream ends.
- (g) **Drain.** While no turn is registered, the fake adapter pushes more than 64 unsolicited frames, and the next `Send` still receives its own reply.
- (h) **Stale reader.** After `EvictStream` and a re-Attach, the old stream's end leaves the new stream cached.
- (i) **Gateway-caused end is silent.** An `EvictStream` or `Release` end does not reach the classification. Assert it through a recording handler or an internal test hook.
- (j) **Between-turn approval.** An `approvalRequired` `tool_call` that arrives while no turn is registered is gated and answered.
- (k) **Gate error.** A gate error mid-turn makes that `Send` return the wrapped gate error, and the next `Send` receives its own reply after the gated turn's `response`.

Tier 7a, `tests/tier7a_load_local`:

- `Send`, `EvictStream`, `Release`, and the reader's end run concurrently under `-race`. No goroutine leaks after `Release`.
- An approval relay races a concurrent turn `Send` under `-race`.

Tier 4, `tests/tier4_integration`, through a real `http.Server`, so each request's context is cancelled when the request returns:

- Each of these multi-turn call sites sends two or more messages on one pod-backed session: REST direct delivery, resume-and-deliver followed by a second message, MCP `send_message`, and `delegate_task` with a follow-up.

Tier 5, `tests/tier5_e2e_kind`:

- Extend `prompt_roundtrip_test.go` to send two messages on one session on the default session pod.
- One concurrent-pool case: two slot sessions on the same pod each send two messages.

Tier 2, `tests/tier2_component/translators`:

- A client disconnect mid-turn makes `Send` return the context error promptly. Once the existing detached release runs, the fake adapter observes the `Attach` stream's context end. The release itself stays covered by `TestSingleShotReleaseDrainsOnDetachedContextAfterTimeout_spec_15`.

### Tests that land with CODE-2

Spec ties: 7.3 (Retry and Resume), 6.2 (Pod State Machine), 5.2 (Pool Configuration and Execution Modes).

- Tier 1, `pkg/gateway/sessionserver/failure_test.go`:
  - A report on a running session with retries left releases the binding with `DispositionFailed`.
  - A report with retries exhausted does the same and lands in `awaiting_client_action`.
  - A non-retryable report releases exactly once.
  - A conflict or no-op report releases nothing.
  - A report from `resuming` releases nothing.
- Tier 1: on a slot binding whose failure crosses `ceil(n/2)` on the `failed` edge, `DrainSandbox` is recorded before `ReleaseSlot`. This ordering is the assertion that discriminates.
- Tier 1: `lenny_slot_failure_total` increments once per failed slot with `error_type` `runtime_crash`. A pool-resolution error skips the accounting and still releases.
- Tier 1, `pkg/gateway/sessionserver/resume_rebind_test.go`: `resumeOnPod` releases a leftover binding before it publishes the new one. That covers the fence-error-after-`Put` path, where the row stays in `awaiting_client_action` and a second `POST /resume` follows.
- Tier 2, `tests/tier2_component` (envtest): a `failed`-disposition release on a recycling session-mode pool retires the pod and does not recycle it.

### Tests that land with CODE-3

Spec ties: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume).

- Tier 1, `pod_test.go`:
  - `DeadlineExceeded` and `Internal` ends call the handler once, including a `DeadlineExceeded` end while the reader waits in the approval gate, whose relay write then fails.
  - `io.EOF`, `FailedPrecondition`, `InvalidArgument`, and `Unavailable` ends evict without a handler call.
  - A crashed runtime is reported on the next `Send`'s re-Attach when that stream ends with `Internal`.
- Tier 1, `pkg/gateway/sessionserver/stream_failure_test.go`:
  - The report uses `runtime_crash` and the sandbox as `PodAssignment`.
  - A registry entry that names a different sandbox, or no entry, produces no report.
  - The report context carries the configured deadline.

### TEST-2 · Step-2 tests

Spec ties: 28.5.1 (Gateway-to-pod), 7.3 (Retry and Resume), 6.2 (Pod State Machine), 5.2 (Pool Configuration and Execution Modes).

Tier 4, `tests/tier4_integration`, with a bufconn adapter whose `HeartbeatInterval` is set:

- (a) A heartbeat escalation between turns, on a stream that REST direct delivery opened, moves the session to `resume_pending`, releases the binding with `DispositionFailed`, and evicts the stream.
- (b) A mid-turn escalation fails the in-flight `Send` and produces the same report.

Tier 5, `tests/tier5_e2e_kind`, with a runtime fixture that stops acking heartbeats between turns:

- On an exclusive recycling pool, the pod is retired rather than recycled, and the fixture records the `session_end` frame it receives.
- The session sets a `retryPolicy.maxResumeWindowSeconds` of a few seconds and reaches `awaiting_client_action` after it.
- On a concurrent pool, each failed slot is counted, and the pod drains at `ceil(maxConcurrentSessions / 2)` failed slots.

Tier 7a, `tests/tier7a_load_local`:

- The report and release race the watchdog release and a concurrent `Send` under `-race`, with a goroutine-leak check.

Tier 8, `tests/tier8_chaos`:

- Killing the runtime connection, mid-turn or between turns, produces no report until the next message delivery, which produces one `runtime_crash` report (SPEC-2b).
- An `LNK-POD-GRPC` partition test waits past the gateway keepalive time plus timeout, asserts that the stream was evicted with `UNAVAILABLE`, and then asserts that the row has not left `running`. Asserting before the eviction would pass without exercising the `UNAVAILABLE` branch.

Tier 11, `tests/tier11_docs`, in the `concurrent_slot_lifecycle_doc_reconciliation_test.go` pattern:

- A reconciliation test pins the exhaustion outcome across the SPEC-1 sites, the SPEC-2 card, the SPEC-3 row, and the DOCS-1 pages.

Tier 9 is not reached.

## Edge cases and accepted failure modes

- **Abandoned turn that never answers.** The token stays held until the stream ends or the session's binding is released. Later `Send` calls on the session wait on the token and return their own context errors. The watchdog or a heartbeat escalation bounds the wait.
- **Approval pending after the client disconnects.** The gate runs on the session-scoped context, so the approval stays resolvable. `toolApprovalTimeout` bounds it. While the reader waits in the gate, it reads no frames; this matches today's behavior.
- **Failed report.** The stream has already been evicted, so the next message re-Attaches and re-escalates within the heartbeat interval plus the ack timeout (30 s + 10 s by default). The watchdog bounds an idle session.
- **Single-shot translator sessions.** These report through the same path. The `resume_pending` row they leave holds no resources, because the release runs on the failure edge.
- **Clean runtime exit.** An `io.EOF` end evicts without a report. Until the next message delivery reports the exited runtime under SPEC-2b, the session watchdog bounds an idle session.

## Files touched on application (non-spec)

- `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/podstream.go` (new), `pkg/gateway/session/executor/pod_test.go` (CODE-1, CODE-3, TEST-1)
- `pkg/gateway/runtime/adapterclient/client.go` (CODE-1)
- `pkg/gateway/sessionserver/failure.go`, `slot_bind.go`, `resume_rebind.go`, `failure_test.go`, `resume_rebind_test.go` (CODE-2)
- `pkg/gateway/sessionserver/stream_failure.go` (new), `stream_failure_test.go` (new) (CODE-3)
- `cmd/lenny-gateway/session_messaging.go`, `cmd/lenny-gateway/flags.go` (CODE-3)
- `tests/tier2_component/`, `tests/tier2_component/translators/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier11_docs/` (TEST-1, TEST-2, and the CODE-2 envtest)
- `docs/reference/state-machines.md`, `docs/client-guide/session-lifecycle.md`, `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/delegation.md`, `docs/getting-started/concepts.md` (DOCS-1)
- `tests/claim-map.json`, `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` (RECORDS-1)
