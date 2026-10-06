# Summary: Hold the session's CH-ATTACH stream for the life of its binding and report the stream's failure through the §7.3 classifier

## Summary

**Problem statement.** The gateway opens a pod-backed session's `Attach` stream on the context of the first request that delivers a message. The stream therefore ends when that request returns, and every later message on the session fails (F-7.3.28). When a stream does fail, including when the adapter ends it because the runtime stopped answering heartbeats, the gateway does nothing. The session keeps its slot, claim, and workspace until terminate or the watchdog, and a recycling pool can reuse the pod whose runtime hung (F-7.3.27). The spec also disagrees with itself on whether exhausted retries end in `awaiting_client_action` or `failed`, and it does not state what the gateway does after a stream failure.

**What changes.**
- Pod executor: a session-scoped stream with one reader, a turn token, cancel-based eviction, and eviction on end (CODE-1).
- Session server: the release with the `failed` disposition and the slot accounting in the failure funnel, and a release before a resume rebinds (CODE-2).
- Executor and session server: the injected stream-failure handler that reports `runtime_crash` (CODE-3).
- Spec: the exhaustion outcome reconciled to `awaiting_client_action` (SPEC-1). The stream lifetime, the stream-failure rule, and no redial are stated on the §28.5.1 `CH-ATTACH` card (SPEC-2) and mirrored in the §28.8 row (SPEC-3). The §5.2 slot carve-out (SPEC-4).
- Reader docs follow SPEC-1 (DOCS-1). Records and claim rows (RECORDS-1).
- Tests across tiers 1, 2, 4, 5, 7a, 8, and 11 (TEST-1, TEST-2).

**Decisions.**
1. The minimal design is closed. It consists of a session-scoped cached stream, one reader per stream, a one-slot turn token, reader-side classification, a handler injected the way `SetApprovalGate` is, and the release inside the failure funnel. It adds no wire field, RPC, error code, state, `BindResult` field, or helper-signature refactor.
2. The stream is held from the first delivery until the binding is released. Per-`Send` `Attach` is rejected for three reasons. The adapter fan-out still hands an abandoned turn's late reply to the next subscriber. No heartbeat runs on an idle session. On a concurrent pod, only slots that send a message fail.
3. Turns are correlated by serialization, with no wire field. An abandoned turn keeps the token until the reader consumes its own `response` or the stream ends. A frame that arrives with no turn registered is discarded, except an approval `tool_call`, which is still gated so the runtime always receives a verdict.
4. The reader always drains the stream outside an approval wait, so the adapter's per-subscriber intake never stalls sibling slots or heartbeat-ack handling.
5. Classification happens once, in the reader, and is internal to the executor. Callers still surface a wrapped error. Only `DEADLINE_EXCEEDED` (heartbeat escalation) and `INTERNAL` are reported. A clean end is not reported, because exit code 0 is normal completion under §28.5.3 Exit Codes. `FAILED_PRECONDITION` is not reported, because it can be a retryable ordering race before `session_start`. `UNAVAILABLE` is transport loss and belongs to the coordination Sweeper. The gRPC status code is the discriminator; `adapterclient.Client.Alive()` is not.
6. `EvictStream` sets `closedByGateway`, cancels, and deletes the entry. It is the one seam that `Release` and the Sweeper's `EvictBinding` already use. The reader removes the conn from the cache only when the cached pointer is still its own.
7. Exhaustion outcome, decision (a): a retryable failure with retries exhausted goes to `awaiting_client_action`, and a non-retryable failure of an active session goes to `failed`. This keeps the code and the larger set of spec statements, and leaves "Resume anyway" reachable. A heartbeat escalation is classified as the existing `runtime_crash`, which is retryable.
8. Concurrent pools, decision (b): implemented proposal 0079 is adopted unchanged. A failed slot counts toward the §5.2 whole-pod replacement trigger through the existing `accountSlotFailure`, and `ReleaseSlot` keeps taking no disposition.
9. The release lives in `applyFailureFromActive`. On the `failed` edge, the slot accounting runs inside `transitionToFailed`, before `recordSessionCompleted` releases. `applyFailureFromResuming` is unchanged, because the resume path owns its rollback.
10. There is no same-pod redial. A reported failure releases the binding. After an `UNAVAILABLE` end, a later message opens a new stream only over a binding the replica still holds.
11. Replacement-pod re-dispatch stays out of scope. The failure-edge release sends `Shutdown`, so re-dispatch is not needed to write `session_end`.
12. The report runs on a detached context with a 30 s operator-tunable deadline. A failed report is not retried, because the next message re-detects and the watchdog bounds an idle session.
13. The report is dropped when the registry no longer names the conn's sandbox, so a stale end cannot act on a newer binding.
14. The §28.5.1 card is the owning statement of the stream rules, recorded by the RECORDS-1 claim row. §28.5.3 `CH-MSGSOCK` **Timing.** is not edited.

**Watch out for.**
- Step order. CODE-1 lands before any reporter is wired, because a reporter on the request-scoped stream would move every multi-turn session to `resume_pending` on its second message. CODE-2 lands before CODE-3, so no commit reports a failure that nothing releases.
- gRPC `SendMsg` is not safe for concurrent calls. After CODE-1 the reader's approval relay and the `Send` path both write, so every write goes through `conn.send`. `EvictStream` must stop calling `CloseSend`.
- Discarding an approval `tool_call` between turns looks harmless but wedges the session. The runtime blocks on the tool call and keeps answering heartbeats, so the stream never ends.
- On the `failed` edge, `recordSessionCompleted` already releases. Accounting the slot after the switch lets `ReleaseSlot` start a recycle before `DrainSandbox` is stamped.
- Existing executor tests pass `context.Background()` and cannot catch F-7.3.28. The tier-4 tests must go through a real `http.Server`.
- The tier-8 partition test passes without exercising anything if it asserts before the keepalive bound has elapsed and the stream has been evicted.
- An operation the gateway initiates that ends the stream with a reported status would read as a failure. CODE-1 item 10 names the check.
- The draft leaves one question to the approver, which the review loop records under **Open decisions**: whether CODE-1 and TEST-1 land as a direct code change ahead of the spec lane. CODE-1 has no spec dependency beyond the lifetime sentence of SPEC-2a.

## Goals

- Every multi-turn pod-backed session delivers its second and later messages on the stream its first message opened.
- A request that ends mid-turn stops waiting without ending the stream, and its late reply never reaches a later turn.
- A runtime that hangs, whether mid-turn or between turns, is reported through the §7.3 classifier, and its binding is released.
- The spec states the exhaustion outcome once, consistently, and states the gateway's stream-failure handling on the `CH-ATTACH` card.

## Non-goals

- One `Attach` per `Send` on the request context with no cache. It is rejected under decision 2. It would also delete the `EvictStream` seam that 0060 uses.
- Turning `EvictStream` into a no-op and deleting `streamEvicter` from `coordination_seams.go`. The detached stream keeps the seam, which now cancels.
- A correlation field such as `inReplyTo` on the §28.5.3 `response` frame. It would be a new wire contract across the runtime SDKs and every Basic runtime, and the turn token gives the same guarantee.
- A skip-count of abandoned replies with auto-deny of their approvals. It breaks when an abandoned turn never answers, and it issues a deny that §7.2 does not state.
- Abandoning a turn by cancelling and reopening the stream. The late reply still reaches the next subscriber, and every abandon would read as a gateway-caused end.
- A `Send` that never abandons. The request context must be honored, and translators would hold their pod after a disconnect.
- A goroutine per turn with `context.AfterFunc`. It adds no protection over the reader-side turn handling.
- An exported `StreamEndedError` or `ErrStreamClosed` sentinel. No caller branches on it.
- Calling `ReportSessionFailure` from the message paths. That would miss ends with no `Send` in flight and would need one call per call site.
- Reporting `UNAVAILABLE` ends as `runtime_crash`. A transport blip or rolling update would push live sessions to `resume_pending` and drain pods the Sweeper is re-adopting.
- Using `adapterclient.Client.Alive()` as the transport-loss discriminator. After a drop the channel can read Idle, which `Alive()` reports live.
- Keeping a dead stream cached as a tombstone until `Release` or until the handler returns. A redial after a failed report re-escalates within about 40 s, so the redial is bounded.
- A report retry loop with backoff. The next message re-detects, and the watchdog bounds an idle session.
- Releasing at the tail of `ReportSessionFailure` for every edge. That would release the in-flight resume's binding, which the resume rollback owns.
- Releasing inside each transition helper, or in the executor reader. One funnel covers every edge and every future detector.
- Releasing the pod before writing the failure row. A crash between the two would leave a running row with no pod, which the orphan reconciler makes terminal.
- Adding `Pool` and `MaxConcurrentSessions` to `BindResult`. `podsession.ResolvePool` already resolves them from the row.
- Narrowing `accountSlotFailure`'s signature. A constructed `SlotBindError` reuses it unchanged.
- A disposition parameter on `Binder.ReleaseSlot` so one failed slot retires a concurrent pod. That would reverse 0079 and contradict "Slots fail independently".
- Moving exhausted retries to `failed` in code. It would need code, test, and spec changes, and would make "Resume anyway" unreachable after exhaustion.
- Deferring the §7.2 and §7.3 reconciliation to its own proposal. The validated problem places decision (a) here.
- A new failure label such as `heartbeat_timeout`. `runtime_crash` exists, is retryable, and is the label §28.8 `CH-MSGSOCK` gives runtime death.
- Same-pod `Attach` redial, bounded or not. The adapter keeps no hung marker, so a redial would be admitted against a hung runtime with a fresh heartbeat monitor.
- Adding a §7.3 stream-detection paragraph. The stream rules are stated once on the card.
- Editing §28.5.3 `CH-MSGSOCK` **Timing.**. It already says the adapter ends the session's stream.
- Opening `Attach` eagerly at bind, resume, or re-adopt for heartbeat coverage before the first message. The validated problem does not exhibit this case.
- Automatic replacement-pod re-dispatch from `resume_pending`. It is not needed to send `Shutdown` and is filed by RECORDS-1.
- Cross-replica release. It is a general release-path defect, filed by RECORDS-1.
- An adapter-emitted `CH-ADAPTEREVENTS` slot-failure event for a stream failure. Gateway-side detection and SPEC-4 cover it.
- Excluding single-shot translator sessions from reporting. One path is kept, and the `resume_pending` row they leave holds no resources.
- Making `full_revoke` evict the stream before `Shutdown`. On the sidecar transport a per-session `Shutdown` does not close the session's `Attach` output, so no false report arises.
- Reconciling `sessionPolicy.maxSessionRetries` (§6.2) against `retryPolicy.maxRetries` (§7.3). This problem does not exhibit it.
- Closing the in-memory inbox ordering window between a direct-delivery fallback buffer and the `resume_pending` DLQ migration. This problem does not exhibit it, and durable mode is unaffected.
- Counting failed terminal slots from other reporters beyond `applyFailureFromActive`. No other reporter exists.

## Open decisions for human to make

The spec review loop routed these questions to a human and left each one unfiled. None has a recommendation from the loop; the open-decisions-and-impact-review phase supplies one.

1. **Does 0091 add an inbox drain to the new direct edges into `awaiting_client_action`?** In §7.2, the inbox-to-DLQ migration and the durable-inbox expiry run only when a session enters `resume_pending`. SPEC-1 stages direct `running`, `input_required`, and `suspended` → `awaiting_client_action` edges for a retryable failure with retries exhausted, and no rule drains the inbox on those edges. The code (`transitionToAwaitingClientAction` in `pkg/gateway/sessionserver/failure.go`) does not drain either. Ground for leaving it out: §7.3 step 4 already sent exhaustion to `awaiting_client_action` without a drain before this proposal, and the **Defects in the shipped tree** section already scopes the related inbox ordering window out. Choose between staging the drain in 0091 and filing it as a separate finding.
2. **Is a follow-up needed for the embedded adapter's SIGSTOP checkpoint against the 10 s heartbeat ack?** §4.4 lets the embedded adapter freeze the runtime with SIGSTOP for up to 60 s during a checkpoint, and no spec text suspends the §28.5.3 heartbeat during the freeze. After 0091, a long embedded checkpoint ends the stream with `DEADLINE_EXCEEDED` and is reported as `runtime_crash`. Ground for leaving it out: the hazard predates 0091, because the current §28.5.1 **Degradation.** text already sends any gRPC error to `resume_pending`, and the SIGSTOP path is not implemented in `pkg/adapter`.
3. **Is a follow-up needed for a stale replica that keeps a long-lived `Attach` stream after a fence it never saw?** The adapter does not end a superseded generation's streams when a fence arrives, and `pkg/adapter` has no code that does. Ground for leaving it out: the question belongs to the exclusivity and security review, and the session-row compare-and-swap fences the failure report the stale replica would make.
4. **Is the emitter of `lenny_slot_failure_total` in the §5.2 Failure isolation bullet corrected, and where?** The bullet's first sentence says the adapter marks a slot `failed` and emits `lenny_slot_failure_total`. In the tree the gateway is the only emitter (`podBinder.SlotFailure` is wired to `gwMetrics.IncSlotFailure`), and CODE-2 adds stream-failure increments through the same gateway series. SPEC-4 names no emitter. Ground for leaving it out: the divergence predates 0091, and the loop judged it a separate spec or `BUILD-GAPS.md` finding.
5. **How does `perChildRetryBudget` combine with a delegated child's own `retryPolicy.maxRetries`?** A child session carries its own `retryPolicy` (default `maxRetries` 2), and §8.3 `perChildRetryBudget` (default 1) is a separate cap. The spec does not state which limit wins when they differ. SPEC-1j makes the budget the child's exhaustion predicate and leaves the combination as it was. The tree implements no `perChildRetryBudget`; children exhaust on their own `maxRetries`. Ground for leaving it out: the ambiguity predates 0091, and the loop judged it a separate finding.
6. **Is the adapter-synthesized `RUNTIME_CRASH` statement filed as a separate finding?** The §28.5.3 `CH-MSGSOCK` **Degradation.** bullet and the §28.8 `CH-MSGSOCK` row say the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr when the runtime exits non-zero without a response. `pkg/adapter` contains no such code, and on the sidecar socket transport the adapter does not see the runtime's exit code. SPEC-2d and SPEC-3 keep that clause unchanged. Ground for leaving it out: the clause predates 0091, and SPEC-2b states separately how the gateway treats a runtime exit.

## Defects in the shipped tree that this proposal does not stage

- **No driver moves `resume_pending` to `resuming` on a replacement pod.** A session this proposal moves to `resume_pending` waits out `maxResumeWindowSeconds` and then enters `awaiting_client_action`, unless the client resumes it. The card's existing re-attach sentence also has no claim row. RECORDS-1 files the finding; the driver is a separate piece of work.
- **A release on a replica that does not hold the in-memory binding is a no-op.** It affects every watchdog and terminate release, so it is not specific to stream failure. RECORDS-1 files it.
- **Two retry budgets.** `sessionPolicy.maxSessionRetries` (§6.2) and `retryPolicy.maxRetries` (§7.3) both bound session retries. Stream failure does not depend on which one governs.
- **Inbox ordering window.** In non-durable mode, a direct-delivery fallback buffer and the `resume_pending` DLQ migration can reorder messages. Stream failure does not depend on it.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0060 | Implemented | `EvictStream` now cancels the stream rather than calling `CloseSend`. The Sweeper's `EvictBinding` calls it unchanged. | Nothing. The record stands. |
| 0079 | Implemented | Adopts its missed-heartbeat edge case unchanged, and makes the "session fails" outcome it recorded reachable. | Nothing. |
| 0090 | Implemented | Supplies the stream-failure dependency that its **Watch out for** names. The failure-edge release sends `Shutdown`, which writes `session_end`. | Nothing. The record stands. |
| 0084 | Draft | Both edit §15.4.3 **Basic**, in different bullets (SPEC-1g edits the heartbeat bullet). | Whichever lands second re-anchors its edit against the landed text. |

## Deliverable index

- SPEC-1 — `spec/07_session-lifecycle.md`, `spec/06_warm-pod-model.md`, `spec/08_recursive-delegation.md`, `spec/15_external-api-surface.md` — state the mid-session failure outcome once in the §7.3 resume flow, align every other statement of it to that flow, and point the §15.4.3 heartbeat sentence at §28.5.3.
- SPEC-2 — `spec/28_communication-channels.md`, `spec/04_system-components.md` — state the stream lifetime, the stream-failure rule, and no redial on the §28.5.1 `CH-ATTACH` card, and point §28.6, the §28.5.3 `CH-MSGSOCK` card, and §4.7 at it.
- SPEC-3 — `spec/28_communication-channels.md` — mirror SPEC-2 in the §28.8 `CH-ATTACH` row and point the §28.8 `CH-MSGSOCK` row at the card.
- SPEC-4 — `spec/05_runtime-registry-and-pool-model.md` — route a slot's stream failure to §28.5.1 and §7.3 in the §5.2 **Failure isolation:** bullet.
- DOCS-1 — `docs/reference/state-machines.md`, `docs/client-guide/session-lifecycle.md`, `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/delegation.md`, `docs/getting-started/concepts.md` — follow SPEC-1 in the reader docs.
- CODE-1 — `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/podstream.go`, `pkg/gateway/runtime/adapterclient/client.go` — hold the session's stream with one reader and a turn token, and evict on end.
- CODE-2 — `pkg/gateway/sessionserver/failure.go`, `pkg/gateway/sessionserver/slot_bind.go`, `pkg/gateway/sessionserver/resume_rebind.go`, with tests in `failure_test.go`, `resume_rebind_test.go`, and `tests/tier2_component/` — release with the `failed` disposition and count the failed slot in the failure funnel, and release before a resume rebinds.
- CODE-3 — `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/podstream.go`, `pkg/gateway/sessionserver/stream_failure.go`, `cmd/lenny-gateway/session_messaging.go`, `cmd/lenny-gateway/flags.go`, with tests in `pod_test.go` and `stream_failure_test.go` — inject the stream-failure handler and report `runtime_crash`.
- TEST-1 — `pkg/gateway/session/executor/pod_test.go`, `tests/tier2_component/translators/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/` — step-1 tests at tiers 1, 2, 4, 5, and 7a.
- TEST-2 — `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier11_docs/` — step-2 tests at tiers 4, 5, 7a, 8, and 11.
- RECORDS-1 — `tests/claim-map.json`, `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` — claim rows, finding closures, the deferred findings, and the remediation-plan item.
