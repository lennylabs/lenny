# Summary: Hold the session's CH-ATTACH stream for the life of its binding and report the stream's failure through the §7.3 classifier

## Summary

**Problem statement.** The gateway opens a pod-backed session's `Attach` stream on the context of the first request that delivers a message. The stream therefore ends when that request returns, and every later message on the session fails (F-7.3.28). When a stream does fail, including when the adapter ends it because the runtime stopped answering heartbeats, the gateway does nothing. The session keeps its slot, claim, and workspace until terminate or the watchdog, and a recycling pool can reuse the pod whose runtime hung (F-7.3.27). The spec also disagrees with itself on whether exhausted retries end in `awaiting_client_action` or `failed`, and it does not state what the gateway does after a stream failure.

**What changes.**
- Pod executor: a session-scoped stream with one reader, a turn token, cancel-based eviction, and eviction on end (CODE-1).
- Session server: the release with the `failed` disposition and the slot accounting in the failure funnel, and a release before a resume rebinds (CODE-2).
- Executor and session server: the injected stream-failure handler that reports `runtime_crash` (CODE-3).
- MCP tools: a failed child's TaskResult category follows SPEC-1e's §8.8 example (CODE-4).
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
12. The report runs on a detached, bounded context (CODE-3 item 3.1). A failed report is not retried, because the next message re-detects and the watchdog bounds an idle session.
13. The report is dropped when the registry no longer names the conn's sandbox, so a stale end cannot act on a newer binding.
14. The §28.5.1 card is the owning statement of the stream rules, recorded by the RECORDS-1 claim row. §28.5.3 `CH-MSGSOCK` **Timing.** is not edited.

**Watch out for.**
- Step order: CODE-1, then CODE-2, then CODE-3, as the non-spec Design list states.
- Discarding an approval `tool_call` between turns looks harmless but wedges the session. The runtime blocks on the tool call and keeps answering heartbeats, so the stream never ends.
- Existing executor tests pass `context.Background()` and cannot catch F-7.3.28. The tier-4 tests must go through a real `http.Server`.
- An operation the gateway initiates that ends the stream with a reported status would read as a failure. CODE-1 item 10 names the check.

## Goals

- Every multi-turn pod-backed session delivers its second and later messages on the stream its first message opened.
- A request that ends mid-turn stops waiting without ending the stream, and its late reply never reaches a later turn.
- A runtime that hangs, whether mid-turn or between turns, is reported through the §7.3 classifier, and its binding is released.
- The spec states the exhaustion outcome once, consistently, and states the gateway's stream-failure handling on the `CH-ATTACH` card.

## Non-goals

- One `Attach` per `Send` on the request context with no cache. It is rejected under decision 2. It would also delete the `EvictStream` seam that 0060 uses.
- Turning `EvictStream` into a no-op and deleting `streamEvicter` from `coordination_seams.go`. The detached stream keeps the seam, which now cancels.
- A correlation field such as `inReplyTo` on the §28.5.3 `response` frame. It would be a new wire contract across the runtime SDKs and every Basic runtime, and the turn token gives the same guarantee.
- A skip-count of abandoned replies with auto-deny of their approvals. It breaks when an abandoned turn never answers, and it denies approvals a user could still answer.
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
- Narrowing `accountSlotFailure`'s signature. A constructed `SlotBindError` reuses its signature.
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

The spec review loop routed these questions to a human and left each one unfiled. The open-decisions-and-impact-review phase supplied the recommendations that OD-1 and OD-3 carry.

1. **OD-1. Does 0091 drain the session inbox when a retryable failure with retries exhausted moves a session directly to `awaiting_client_action`?** SPEC-1 replaces the §7.2 edges `running → failed` and `input_required → failed` for retries exhausted, and adds a `suspended` edge, so that all three go to `awaiting_client_action` as §7.3 step 4 already states. Entry into `failed` runs the §7.2 **Inbox drain on terminal transition:**, which moves undelivered inbox messages to the DLQ. Entry into `awaiting_client_action` runs no drain, and the inbox-to-DLQ migration (with the durable-mode `EXPIRE`) runs only on entry to `resume_pending`. The question is whether 0091 adds that drain to the new edges or leaves it out and files it separately.
   - **Recommendation:** leave the drain out of 0091 and file it as a separate finding, which is what the staged changes already do. Confidence: moderate.
   - **Ground:** In default mode the in-memory inbox has no durability guarantee across a coordinator crash, and §7.2 calls that a documented loss window (`spec/07_session-lifecycle.md:288`), signaled by `inbox_cleared` on takeover (`:289`). The stated default-mode guarantee holds only while the coordinating replica is alive (`:308`), and while it is alive every exit from `awaiting_client_action` already drains: resume goes to `resume_pending` (`:202`), which migrates the inbox (`:310`), and the terminal exits run the terminal drain (`:348`). In durable mode the per-message trimmer is active in `awaiting_client_action` (`:299`). §7.3 step 4 (`:416`) and settled decision 7 already chose `awaiting_client_action` for exhaustion, and the shipped code matches: the exhausted path calls `transitionToAwaitingClientAction` (`pkg/gateway/sessionserver/failure.go:180`), which has no drain, while the drain runs only on the `resume_pending` path (`failure.go:262`). The stream-failure deliverable does not depend on the choice.
   - **Alternative, stage the drain in 0091:** add a resume_pending-style inbox-to-DLQ migration on direct entry to `awaiting_client_action` in SPEC-1 and CODE-2. It lost because it hardens the inbox beyond the guarantee §7.2 states, widens 0091 into the inbox subsystem, and adds a §7.2 edit and tier-4 tests that stream failure does not need.
   - **Cost of deciding otherwise:** staging the drain adds a §7.2 rule, a change to `transitionToAwaitingClientAction`, and its tests to 0091. Leaving it out keeps a residual: in default mode, a coordinator crash while a session waits in `awaiting_client_action` loses its in-memory inbox, where the replaced `→ failed` edges would have drained it. Closing that residual later takes its own spec change.
3. **OD-3. Does 0091 stop a gateway replica that has lost coordination of a session from answering approvals on that session's `Attach` stream, or does it record the gap as a shipped-tree defect and accept the residual?** Under 0091 the gateway holds a session's `Attach` stream from the first message until the pod binding is released (SPEC-2a, CODE-1), and the stream's reader gates an approval `tool_call` and relays the verdict to the pod even when no turn is registered (CODE-1 item 5). The adapter delivers the runtime's output to every `Attach` stream open on the pod. A replica that lost the coordination lease but never received a generation-stale rejection therefore keeps a live stream, sees the new coordinator's output, and can answer approvals for the new coordinator's turns. §10.1 and the `AttachRequest` schema say the pod validates `coordination_generation` on every frame and refuses a superseded generation once the new coordinator's fence is acknowledged, which would refuse that relay. The tree implements neither half: the gateway does not stamp the generation on `Attach` frames, and the adapter does not check it on them or end a superseded generation's streams at a fence.
   - **Recommendation:** keep the fix out of 0091. List the missing per-frame generation check on `CH-ATTACH` under **Defects in the shipped tree that this proposal does not stage**, have RECORDS-1 file it as a finding for the exclusivity review, and accept the residual until that work lands. Confidence: low. Nothing found in the tree shows whether a replica that loses its lease while its connection to the pod stays alive can keep the binding for long, and evidence that it cannot would shrink the residual to the pre-existing defect.
   - **Ground:** The contract is settled, and the code is what diverges. §10.1 says pods validate the generation on every gateway→pod RPC (`spec/10_gateway-internals.md:30`), and §10.1.2 says a fenced pod refuses a superseded generation (`:38`, `:41`). The `AttachRequest` field comment says the generation is validated on every frame of the stream (`schemas/lenny-adapter.proto:1042-1049`), and the §28.5.1 `CH-ATTACH` card names that stamp as part of its exclusivity guard (`spec/28_communication-channels.md:245-248`). The adapter reads the generation only in `CoordinatorFence` and `CheckpointBarrier` (`pkg/adapter/coordination.go:120`, `:262`), and `pkg/gateway/session/executor` never sets it. The divergence predates 0091, and closing it is generation stamping and checking on both sides of `CH-ATTACH`, which belongs to the exclusivity and security work.
   - **Alternative, close the gap in 0091 by stamping and checking the generation on every `Attach` frame:** the gateway stamps its local generation stamp, and the adapter refuses a frame whose generation is not the one it holds for the session. It lost because it adds code and tests in `pkg/adapter` and the gateway's stamping path, which 0091 does not otherwise touch, and turns 0091 into the `CH-ATTACH` exclusivity contract.
   - **Alternative, close the gap in 0091 by ending the stream when the lease is lost:** the coordination Sweeper calls `EvictBinding` when its `Acquire` for a bound session returns `ErrHeld`. Today it skips such a session and evicts only on a dead connection (`pkg/gateway/coordination/coordination/coordination.go:315`, `:341`). It lost because it covers only a lease loss the stale replica's own Sweeper observes, and it still leaves the pod side unguarded.
   - **Cost of deciding otherwise:** Closing the gap in 0091 adds code and tests in packages 0091 does not otherwise touch, and delays the F-7.3.28 fix that the rest of 0091 delivers. Keeping it out leaves a residual that 0091 enlarges, because the cached stream today ends when the first request returns (`BUILD-GAPS.md` F-7.3.28). Under 0091 the stream lives until release and relays approval verdicts on its own, so until the generation check lands a stale replica can answer approvals for the new coordinator's turns. The session-row compare-and-swap fences only the failure report the stale replica makes and does not cover that relay.

## Defects in the shipped tree that this proposal does not stage

- **No driver moves `resume_pending` to `resuming` on a replacement pod.** A session this proposal moves to `resume_pending` waits out `maxResumeWindowSeconds` and then enters `awaiting_client_action`, unless the client resumes it. The card's existing re-attach sentence also has no claim row. RECORDS-1 files the finding; the driver is a separate piece of work.
- **A release on a replica that does not hold the in-memory binding is a no-op.** It affects every watchdog and terminate release, so it is not specific to stream failure. RECORDS-1 files it.
- **Two retry budgets.** `sessionPolicy.maxSessionRetries` (§6.2) and `retryPolicy.maxRetries` (§7.3) both bound session retries. Stream failure does not depend on which one governs.
- **`perChildRetryBudget` against a child's own `retryPolicy.maxRetries`.** §8.3 contrasts the budget only with the parent's `maxRetries` and does not state which limit wins for a child. The tree does not enforce the budget (`BUILD-GAPS.md` 8.3-M7). SPEC-1j changes only the state a child takes when its retries are exhausted.
- **Inbox ordering window.** In non-durable mode, a direct-delivery fallback buffer and the `resume_pending` DLQ migration can reorder messages. Stream failure does not depend on it.
- **Embedded SIGSTOP checkpoint against the heartbeat ack.** §4.4 lets the embedded adapter hold SIGSTOP for up to 60 s, and the §28.5.3 `CH-MSGSOCK` **Timing.** bullet ends the stream after a 10 s missed heartbeat ack. The conflict predates 0091. `pkg/adapter/embeddedcheckpoint` implements the freeze, but no adapter path imports it.
- **Embedded `Interrupt` ends the session's runtime loop.** `InProcessRuntime.Interrupt` closes the loop's input and `Start` does not relaunch it, so a delivery that resumes an interrupted embedded session on its held pod fails. Under CODE-3 that delivery's re-`Attach` ends `INTERNAL` and is reported as `runtime_crash`. The defect predates 0091.
- **Adapter-synthesized `RUNTIME_CRASH`.** The §28.5.3 `CH-MSGSOCK` **Degradation.** bullet and the §28.8 `CH-MSGSOCK` row say the adapter synthesizes `RUNTIME_CRASH` from the exit code and stderr. `pkg/adapter` has no such code, and on the sidecar transport the adapter does not see the exit code. The clause predates 0091, which keeps it unchanged, and `BUILD-GAPS.md` F-4.7.27 already records it.
- **Emitter of `lenny_slot_failure_total`.** The §5.2 **Failure isolation:** bullet says the adapter marks a failed slot and emits `lenny_slot_failure_total`. The gateway is the only emitter in the tree (`podBinder.SlotFailure` is wired to `gwMetrics.IncSlotFailure`), as it has been since `BUILD-GAPS.md` F-5.2.13 closed. The mismatch predates 0091, which leaves that sentence unchanged, and CODE-2 increments the existing gateway series without adding a metric.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0060 | Implemented | `EvictStream` now cancels the stream rather than calling `CloseSend`. The Sweeper's `EvictBinding` calls it unchanged. | Nothing. The record stands. |
| 0079 | Implemented | Adopts its missed-heartbeat edge case unchanged, and makes the "session fails" outcome it recorded reachable. | Nothing. |
| 0090 | Implemented | Supplies the stream-failure dependency that its **Watch out for** names. The failure-edge release sends `Shutdown`, which writes `session_end`. | Nothing. The record stands. |
| 0084 | Draft (drafted 2026-09-26, not yet reviewed) | Only 0091 edits the §15.4.3 **Basic** list: SPEC-1g replaces its heartbeat-ack sentence. 0084 SPEC-1a, SPEC-1b, and SPEC-1c edit the §15.4.3 **Standard** list's **Authentication.** bullet, the **Nonce wire format** paragraph, and the **Strict MCP client libraries** note. Its §28.8 edit targets the `CH-MCP-PLATFORM` row, and its §5.2 edits target the **Concurrent sessions** and **Deployer acknowledgment** paragraphs. 0091 changes none of those passages, so every 0084 deliverable keeps its subject and its quoted anchors. | Nothing. Neither proposal rebases, in either landing order. |

## Deliverable index

- SPEC-1 — `spec/07_session-lifecycle.md`, `spec/06_warm-pod-model.md`, `spec/08_recursive-delegation.md`, `spec/15_external-api-surface.md` — state the mid-session failure outcome once in the §7.3 resume flow, align every other statement of it to that flow, and point the §15.4.3 heartbeat sentence at §28.5.3.
- SPEC-2 — `spec/28_communication-channels.md`, `spec/04_system-components.md` — state the stream lifetime, the stream-failure rule, and no redial on the §28.5.1 `CH-ATTACH` card, and point §28.6, the §28.5.3 `CH-MSGSOCK` card, and §4.7 at it.
- SPEC-3 — `spec/28_communication-channels.md` — mirror SPEC-2 in the §28.8 `CH-ATTACH` row and point the §28.8 `CH-MSGSOCK` row at the card.
- SPEC-4 — `spec/05_runtime-registry-and-pool-model.md` — route a slot's stream failure to §28.5.1 and §7.3 in the §5.2 **Failure isolation:** bullet.
- DOCS-1 — `docs/reference/state-machines.md`, `docs/client-guide/session-lifecycle.md`, `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/delegation.md`, `docs/getting-started/concepts.md` — follow SPEC-1 in the reader docs.
- CODE-1 — `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/podstream.go`, `pkg/gateway/runtime/adapterclient/client.go` — hold the session's stream with one reader and a turn token, and evict on end.
- CODE-2 — `pkg/gateway/sessionserver/failure.go`, `pkg/gateway/sessionserver/slot_bind.go`, `pkg/gateway/sessionserver/resume_rebind.go`, `pkg/gateway/runtime/slothealth/slothealth.go`, with tests in `failure_test.go`, `failure_internal_test.go`, `resume_rebind_test.go`, `slothealth_test.go`, and `tests/tier2_component/` — release with the `failed` disposition and count the failed slot in the failure funnel, and release before a resume rebinds.
- CODE-3 — `pkg/gateway/session/executor/pod.go`, `pkg/gateway/session/executor/podstream.go`, `pkg/gateway/sessionserver/stream_failure.go`, `pkg/gateway/sessionserver/seal.go`, `pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/sessionserver/messages_delivery.go`, `cmd/lenny-gateway/main.go`, `cmd/lenny-gateway/sessionsrv.go`, `cmd/lenny-gateway/flags.go`, with tests in `pod_test.go`, `stream_failure_test.go`, and `messages_resume_deliver_test.go` — inject the stream-failure handler and report `runtime_crash`.
- CODE-4 — `pkg/gateway/mcpfabric/mcptools/mcptools.go`, `pkg/sessionrecord/record.go`, with tests in `tasktree_tools_test.go` and `record_test.go` — derive a failed child's TaskResult category from the §7.3 classification.
- TEST-1 — `pkg/gateway/session/executor/pod_test.go`, `tests/tier2_component/translators/`, `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/` — step-1 tests at tiers 1, 2, 4, 5, and 7a.
- TEST-2 — `tests/tier4_integration/`, `tests/tier5_e2e_kind/`, `tests/tier7a_load_local/`, `tests/tier8_chaos/`, `tests/tier11_docs/` — step-2 tests at tiers 4, 5, 7a, 8, and 11.
- RECORDS-1 — `scripts/seed-claim-register.py`, `tests/claim-map.json`, `BUILD-GAPS.md`, `gateway-runtime-comms-remediation.md` — claim rows, finding closures, the deferred findings, and the remediation-plan item.
