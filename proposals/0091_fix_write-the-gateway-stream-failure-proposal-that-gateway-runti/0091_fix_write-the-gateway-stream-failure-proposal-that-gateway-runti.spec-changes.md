# Spec changes: Gateway does not survive or react to Attach stream failure

## Design (as the spec must state it)

The specification states the gateway's handling of a session's `CH-ATTACH` stream in one place, the §28.5.1 `CH-ATTACH` card. SPEC-2a states the stream lifetime on the card, and SPEC-2b states the stream-failure rule, the report, the discarded ends, and the absence of a redial. The RECORDS-1 claim row records the card as the owner. The §28.8 `CH-ATTACH` row restates the card and cites it, as the §28.8 preamble requires.

SPEC-1 states the mid-session failure outcome once, in §7.3 **Resume flow after pod failure**, and every other statement of it names its edge or cites that flow. On a pod serving concurrent sessions, a slot whose stream fails is handled as SPEC-4 states.

The coordination lease `REG-COORDLEASE` has one lifecycle statement, the §10.1.1 **Lease lifecycle:** bullet that SPEC-1m stages. SPEC-2b, the §29.3, §29.6, §7.2, and §7.3 edits of SPEC-1l, the §28.5.1 `CH-ATTACH` **Exclusivity.** bullet (SPEC-2g), and the §28.3 `REG-COORDLEASE` row (SPEC-2h) cite it rather than restate when the lease is held. SPEC-1n states the outcome of a resume whose bind meets another replica's lease.

## Edge cases and accepted failure modes

- **Abandoned turn.** A request that ends mid-turn stops waiting. The runtime's late reply to that turn is consumed by the gateway and is never delivered as a later message's reply. The spec text states only the lifetime rule; the correlation mechanism belongs to CODE-1.
- **Hung runtime between turns.** The held stream keeps the adapter's heartbeat running, so a runtime that hangs while no message is outstanding is detected and reported under SPEC-2b.
- **Hang before the first message, or after an `UNAVAILABLE` end.** No stream is open, so no heartbeat runs. The next message delivery or the session watchdog detects the hang. This is accepted.
- **Connection loss.** An `UNAVAILABLE` end is discarded without a stream-failure report, as SPEC-2b states. No gateway code implements the §10.1.4 whole-pod reaction that SPEC-2b cites, as the summary **Defects** entry **Whole-pod connection loss has no gateway reaction.** records.
- **Hung runtime on a pod serving concurrent sessions.** Every open slot stream on the pod fails. The coordinating replica counts each failed slot toward the §5.2 whole-pod replacement trigger and requests the drain in the order that CODE-2 items 4 and 6 state. Below the threshold, each failed slot is released under the §5.2 per-slot cleanup disposition: a slot whose cleanup is not acknowledged clean is `leaked` and stays counted. A pod whose last slot releases cleanly takes the pool's occupancy-zero path, where §4.7 `ReportPodScrub` reports a hung runtime whose connection is open as live, so the pod serves again and the hang is found as the §5.2 **Runtime not live:** bullet states for a runtime that stops after the report. Failures that fall outside the trigger's rolling window do not accumulate, so at a low session arrival rate the pod can keep serving. This is accepted. SPEC-4 limits the retire-on-failure statements in §4.6.3, §5.2, §6.1, and §6.2 to `maxConcurrentSessions: 1`. This adopts the outcome of proposal 0079. The count is kept per gateway replica, which the summary lists as a defect this proposal does not stage.
- **Session in `resume_pending` with no re-dispatch driver.** The outcome is the summary **Defects** entry **No driver moves `resume_pending` to `resuming` on a replacement pod.** The card keeps its existing replacement-pod re-attach sentence; the missing driver is filed by RECORDS-1.
- **Launch-time retry paths.** The pre-running `starting → failed (retries exhausted, ...)` edge in §7.2 and the §6.2 pre-attached retry policy's **Exhaustion:** bullet describe launch-time retries that the §7.3 classifier does not govern. They stay unchanged.
- **Resume racing another replica's lease.** A restore whose bind finds `REG-COORDLEASE` held by another replica answers `RESUME_FAILED`, as SPEC-1n states; CODE-2 item 8 lists where the tree reaches it. The §29.3 forward is unchanged.

## Staged edits

Every table row this proposal edits is one physical line. The spec/28 cards and §28.6 are hard-wrapped, so match their anchors with line breaks ignored and keep their wrapping style in the replacement.

### SPEC-1 · spec/10_gateway-internals.md § 10.1.1; spec/07_session-lifecycle.md § 7.1, § 7.2, § 7.3; spec/06_warm-pod-model.md § 6.2; spec/08_recursive-delegation.md § 8.3, § 8.8, § 8.10; spec/15_external-api-surface.md § 15.1, § 15.4.3; spec/29_communication-scenarios.md § 29.2, § 29.3, § 29.6; spec/12_storage-architecture.md § 12.4

**SPEC-1a. §7.2 Interactive Session Model, the **Session state machine:** block and the paragraph after it.** Replace the lines

```
running → resume_pending   (pod crash / gRPC error, retryCount < maxRetries)
running → failed           (runtime crash, unrecoverable error, retries exhausted, or BUDGET_KEYS_EXPIRED — see §8.3)
```

with

```
running → resume_pending   (retryable failure, retryCount < maxRetries — see §7.3)
running → awaiting_client_action (retryable failure, retries exhausted — see §7.3)
running → failed           (non-retryable failure, or BUDGET_KEYS_EXPIRED — see §7.3, §8.3)
```

Replace the lines

```
input_required → resume_pending (pod crash / gRPC error while awaiting input, retryCount < maxRetries)
input_required → failed    (pod crash / gRPC error while awaiting input, retries exhausted)
```

with

```
input_required → resume_pending (retryable failure while awaiting input, retryCount < maxRetries — see §7.3)
input_required → awaiting_client_action (retryable failure while awaiting input, retries exhausted — see §7.3)
input_required → failed    (non-retryable failure while awaiting input — see §7.3)
```

Replace the line `suspended → resume_pending (involuntary pod failure/eviction while suspended; pod still held)` with

```
suspended → resume_pending (retryable failure while suspended; pod still held; retryCount < maxRetries — see §7.3)
suspended → awaiting_client_action (retryable failure while suspended; pod still held; retries exhausted — see §7.3)
suspended → failed         (non-retryable failure while suspended; pod still held — see §7.3)
```

In the paragraph beginning "`input_required` is a **sub-state of `running`**", replace "— including `resume_pending` on pod crash when retries remain and `failed` when retries are exhausted (these transitions are listed explicitly in the state machine above)" with "(the state machine above lists them)".

**SPEC-1b. §7.1 Normal Flow, the **Session `failureClass` field.** table.** Replace the `runtime_failure` row with

```
| `runtime_failure`         | A runtime crash or unrecoverable error that [§7.3](#73-retry-and-resume) treats as non-retryable, from the `running`/`input_required`/`suspended` → `failed` transitions ([§7.2](#72-interactive-session-model)) |
```

**SPEC-1c. §6.2 Pod State Machine, **Pod crash during an active session.**, the **Retry exhaustion:** and **maxSessionRetries** bullets.** Replace the **Retry exhaustion:** bullet with

```
- **Retry exhaustion:** When retries are exhausted or the failure is non-retryable, the session takes the next state that [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume flow after pod failure** selects, and the gateway returns a structured error to the client.
```

In the **maxSessionRetries** bullet, replace "Setting it to `0` disables retries, so crashes always fail the session outright." with "Setting it to `0` disables automatic retries."

**SPEC-1d. §6.2, the **`input_required` sub-state:** paragraph and its transition block.** In the paragraph, replace

```
(`resume_pending` if `retryCount < maxRetries`, `failed` if retries exhausted)
```

with

```
([Section 7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume flow after pod failure**)
```

In the transition block, replace the lines `input_required → resume_pending (pod crash / gRPC error while awaiting input, retryCount < maxRetries)` and `input_required → failed    (pod crash / gRPC error while awaiting input, retries exhausted)` with the three lines SPEC-1a gives for the same edges.

**SPEC-1e. §8.8 TaskRecord and TaskResult Schema, the **Lenny canonical task state machine:** block, the **Recovery transitions are session-level, not task-level.** paragraph, and the "On failure:" `TaskResult` example.** In the block, replace

```
                    → failed            (terminal — unrecoverable error or pod-crash retries exhausted)
```

with

```
                    → failed            (terminal — unrecoverable or non-retryable failure)
```

and replace

```
input_required → failed                (pod crash / gRPC error while awaiting input, retries exhausted)
```

with

```
input_required → failed                (non-retryable failure while awaiting input)
```

In the paragraph, replace "When a pod crash or gRPC error occurs with `retryCount < maxRetries`" with "When a retryable failure occurs with `retryCount < maxRetries`", and replace the clause "on retry exhaustion the task transitions to `failed`." with

```
on retry exhaustion the underlying session enters `awaiting_client_action` ([§7.3](07_session-lifecycle.md#73-retry-and-resume)), which external protocol clients see as `input_required` per the supplementary table below, and a non-retryable failure moves the task to `failed`.
```

In the "On failure:" `TaskResult` example, replace `"retriesExhausted": true` with `"retriesExhausted": false`, and leave `"category": "TRANSIENT"` unchanged. After the example's closing fence, and before the paragraph that begins "`TaskResult.schemaVersion` follows", insert

```
The `error.category` field carries the [Section 16.3](16_observability.md#163-distributed-tracing) category of the error, which for a crash is `TRANSIENT`. It does not record whether the gateway retried the failure. `retriesExhausted` records whether the session's retry budget was spent, and the failure classification in the `child_failed` event ([Section 8.10](#810-delegation-tree-recovery)) records whether [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) classified the failure as retryable. In the example, the child's `retryPolicy` omits `runtime_crash` from `retryableFailures` and lists it under `nonRetryableFailures`, so the child failed on its first crash without a retry.
```

**SPEC-1f. §8.10 Delegation Tree Recovery, **Parent pod failure with active children:**, step 5.** Replace "failure (retry exhaustion)" with

```
failure (a non-retryable failure; a parent whose retries are exhausted enters the non-terminal `awaiting_client_action` and reaches this step when that state expires, as [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) states)
```

**SPEC-1g. §15.4.3 Runtime Integration Levels, **Basic**, the heartbeat bullet.** Replace "failure to ack within 10 seconds ends the session ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))" with

```
failure to ack within 10 seconds ends that session's stream, as the `CH-MSGSOCK` **Timing.** bullet in [Section 28.5.3](28_communication-channels.md#2853-intra-pod) states
```

**SPEC-1h. §7.3 Retry and Resume, **Resume flow after pod failure:** and the **`awaiting_client_action` semantics:** **Expiry:** bullet.** In the flow, replace `2. Classify failure (retryable vs. non-retryable)` with

```
2. Classify failure (retryable vs. non-retryable). A failure whose reason is on neither list is non-retryable.
```

and replace the step-4 line

```
4. If retries exhausted → state becomes `awaiting_client_action`
```

with

```
4. If retryable and retries exhausted → state becomes `awaiting_client_action`
5. If non-retryable and the session was `running`, `input_required`, or `suspended` → state becomes `failed`;
   a failure during `resuming` follows [§6.2](06_warm-pod-model.md#62-pod-state-machine) **`resuming` failure transitions**
```

In the **Expiry:** bullet, delete " (same behavior as terminal failure after retry exhaustion)".

**SPEC-1i. §6.2, the **`suspended` state:** block and the **Pod failure while `suspended` (pod still held):** paragraph.** In the block, replace the line `suspended → resume_pending (involuntary pod failure/eviction while suspended; pod still held)` with the three lines SPEC-1a gives for the same edge. In the paragraph, replace "the session transitions to `resume_pending` and follows the standard retry-and-resume path" with "the session follows the standard retry-and-resume path".

**SPEC-1j. §8.3, the **`perChildRetryBudget`** paragraph.** Replace " before marking the child as permanently failed" with ", after which the child's retries are exhausted and it takes the state that step 4 of [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume flow after pod failure** gives".

**SPEC-1k. Bounded sweep.** After SPEC-1a to SPEC-1j, grep `spec/` for `retries exhausted`, `retry exhaustion`, `retries are exhausted`, and `ends the session`. Reconcile only a statement that gives the outcome of a mid-session (post-`running`) session failure. Leave unchanged the §7.2 `starting → failed (retries exhausted, or STARTING_TIMEOUT expired ...)` edge, the §6.2 pre-attached retry policy's **Exhaustion:** bullet, and every hit about checkpoint uploads, `CoordinatorFence`, credentials, or webhooks.

**SPEC-1l. Coordination lease in `resume_pending` and `awaiting_client_action`: §29.6 **Preconditions.**, §29.3 **Off-holder matrix.**, the §7.2 durable-inbox **Per-message TTL** row and default-mode **Crash recovery** row, the §12.4 durable-inbox key row, and §7.3 **Children behavior:**.** The §29.6 paragraph is hard-wrapped, so match with line breaks ignored and keep its wrapping style. Replace "The gateway replica that drives the restore holds the session's coordination lease `REG-COORDLEASE`, and" with "The gateway replica that drives the restore acquires the session's coordination lease `REG-COORDLEASE` when it binds the replacement pod ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**), and".

In the §29.3 **Off-holder matrix.** paragraph, after "holding the coordination lease `REG-COORDLEASE` (§28.3, [§10.1](10_gateway-internals.md#101-horizontal-scaling))." insert "For a session in `resume_pending` or `awaiting_client_action` that has no coordinating replica because no replica holds the lease ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**), no off-holder condition arises, and the serving replica performs the effect of a row whose required outcome is a forward." The paragraph is hard-wrapped, so match with line breaks ignored and keep its wrapping style.

In the §29.3 `POST /v1/sessions/{id}/terminate` row, replace "the non-terminal states for which the specification establishes a coordinating replica holding `REG-COORDLEASE` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), §28.3)" with "the non-terminal states in which a replica can hold `REG-COORDLEASE`; in `resume_pending` and `awaiting_client_action` the row applies while a replica holds it ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**)". The matrix preamble states the outcome when no replica holds it. The `DELETE /v1/sessions/{id}` row inherits the cell and is not edited.

In the §29.3 `POST /v1/sessions/{id}/resume` row, replace "Forward to the coordinator, which performs the restore" with "While a replica holds `REG-COORDLEASE`, forward to the coordinator, which performs the restore". The matrix preamble states the outcome when no replica holds it, and §29.6 step 4, as SPEC-1n edits it, states the outcome of a bind that meets another replica's lease.

In the §29.3 row for the tool-use and elicitation resolution routes, after "holding that state is not evidence that the blocked call can be resolved locally" insert ". In `resume_pending` and `awaiting_client_action`, while no replica holds `REG-COORDLEASE`, no pod is blocked on the call, and the serving replica records the resolution in that durable state".

In the §29.3 `GET /v1/sessions/{id}/events`, `Accept: application/json` row, after "reports an event history the session does not have. On an unreachable coordinator it fails closed" insert ". In `resume_pending` and `awaiting_client_action`, while no replica holds `REG-COORDLEASE`, no replica holds the session's buffer, and the serving replica serves the envelope from the shared session-event relay `CH-EVENTRELAY`, as the streaming row does".

In the §7.2 durable-inbox **Per-message TTL** row, replace "A background goroutine on the coordinating replica evaluates expiry every 30 seconds and trims expired messages from the list head using `LRANGE` + `LTRIM`." with "A background goroutine evaluates expiry every 30 seconds and removes expired messages from the list head with one Redis script, which reads each head entry's `enqueued_at` and `per_message_ttl` and removes the entry only while it has expired.", and replace "On each expiry, the gateway emits" with "For each message the script removes, the gateway emits". In the `spec/12_storage-architecture.md` §12.4 `t:{tenant_id}:session:{session_id}:inbox` key row, replace "TTL trim via `LRANGE`+`LTRIM`" with "TTL trim via one atomic head-trim script".

In the §7.2 default-mode inbox **Crash recovery** row, replace "When a new gateway replica takes over coordination (via lease reacquisition)," with "When a new gateway replica takes over coordination (via lease reacquisition, which includes the resume bind of a session whose lease was released, [§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**),".

In the §7.3 **`awaiting_client_action` semantics:** **Children behavior:** bullet, replace "if the coordinating gateway replica crashes while the parent is in `awaiting_client_action`" with "if the gateway replica that coordinated the parent crashes, or releases the parent's coordination lease ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**), while the parent is in `awaiting_client_action`".

**SPEC-1m. §10.1.1 Stateless Replicas and Per-Session Coordination, **Per-session coordination:**, and the §29.3 and §29.2 sites that state no acquisition point.** After the **Generation counters:** bullet, insert

```
- **Lease lifecycle:** A gateway replica acquires a session's coordination lease `REG-COORDLEASE` ([Section 28.3](28_communication-channels.md#283-registers)) at three points: when it starts the session on its pod, before it commits the session to `running`; when it binds a replacement pod to the session on a resume; and when it adopts a session's lapsed lease, which is a coordinator takeover that runs the [§10.1.2](#1012-coordinator-handoff-protocol) sequence. The holder renews the lease while it holds the session's pod binding and, in a non-terminal state other than `resume_pending` and `awaiting_client_action`, while it holds the lease without a binding. The holder releases the lease when a stream failure moves the session to `resume_pending` or `awaiting_client_action` and the holder releases the session's binding ([Section 28.5.1](28_communication-channels.md#2851-gateway-to-pod) `CH-ATTACH` **Degradation.**), when it evicts a binding whose gateway-to-pod connection has died, when a bind whose running-commit fails rolls the binding back, and when [§10.1.2](#1012-coordinator-handoff-protocol) or [§10.1.5](#1015-stale-replica-behavior) requires it to relinquish or release the lease. User and tenant erasure deletes the lease ([Section 12.8](12_storage-architecture.md#128-compliance-interfaces)). A lease its holder no longer renews lapses at its expiry. A terminal session's lease is not renewed, lapses at its expiry, and is not acquired again. No replica adopts the lease of a session in `resume_pending` or `awaiting_client_action`, and no replica renews it there without a binding, so a session that a stream failure moved to either state has no holder until a resume binds a replacement pod. The session's coordinating replica is the holder of `REG-COORDLEASE`, and a session in `resume_pending` or `awaiting_client_action` that no replica holds has no coordinating replica.
```

In the same list, in the **Primary:** bullet, replace "If that replica dies, another picks up after TTL expiry." with "If that replica dies, the lease lapses after its TTL and is adopted as the **Lease lifecycle:** bullet states."

In the §29.3 `POST /v1/sessions/{id}/terminate`, `POST /v1/sessions/{id}/start`, and `POST /v1/sessions/{id}/finalize` rows, replace each occurrence of "the specification states no point at which the coordination lease `REG-COORDLEASE` is acquired for the session (§28.3, §28.5.1)" with "no replica has yet acquired the coordination lease `REG-COORDLEASE`, which a replica acquires when it starts the session ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**)". The phrase occurs in those rows and nowhere else in `spec/29_communication-scenarios.md`. In the terminate row it is in the outcome cell, which SPEC-1l's state-column replacement does not touch.

In §29.2 step 11, which is hard-wrapped, so match with line breaks ignored and keep its wrapping style, replace "The specification does not state when the replica creating a session acquires that session's coordination lease `REG-COORDLEASE`, and it does not state whether that replica announces" with "The replica that starts the session at step 21 acquires that session's coordination lease `REG-COORDLEASE` ([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**). The specification does not state whether that replica announces", and replace "state the coordinating replica as the holder of that lease, without a section stating the initial acquisition at session creation." with "state the coordinating replica as the holder of that lease." The step keeps its `unstated` tag for the fence question.

**SPEC-1n. A resume whose bind meets another replica's coordination lease: the §15.1 `RESUME_FAILED` catalog row and §29.6 step 4.** Apply after SPEC-1l. §29.6 step 4 is the single statement of the outcome for every transient resume cause, and the catalog row names the cause and cites the step.

In the §15.1 error catalog `RESUME_FAILED` row, replace "failed for a generic reason (pool or credential exhaustion, a Token Service outage, or a transient setup-time transport failure)." with "failed for a generic reason (pool or credential exhaustion, a Token Service outage, a transient setup-time transport failure, or the session's coordination lease `REG-COORDLEASE` held by another gateway replica when the gateway binds the replacement pod)." After "succeeds once the condition clears." insert " When another gateway replica's resume of the session has committed, the retry is refused with `INVALID_STATE_TRANSITION`. Step 4 of [§29.6](29_communication-scenarios.md#296-restore-and-resume) states the outcome of a bind that meets another replica's lease."

In §29.6 step 4, which is hard-wrapped, so match with line breaks ignored and keep its wrapping style, replace "A pool or credential exhaustion, a Token Service outage, or a transient setup-time transport failure on this path is returned to the caller as `RESUME_FAILED` with `Retry-After` set, and the session row returns to `awaiting_client_action` so" with "A pool or credential exhaustion, a Token Service outage, a transient setup-time transport failure on this path, or the coordination lease `REG-COORDLEASE` held by another replica when the gateway binds the replacement pod is returned to the caller as `RESUME_FAILED` with `Retry-After` set, and the session row returns to `awaiting_client_action` unless another write has moved it on, so". SPEC-1n stages no edit to §29.6 **Preconditions.**.

### SPEC-2 · spec/28_communication-channels.md § 28.3 `REG-COORDLEASE` row, § 28.5.1 `CH-ATTACH` card, § 28.5.3 `CH-MSGSOCK` card and **Exit Codes** table, § 28.6; spec/04_system-components.md § 4.7

**SPEC-2a. The **Timing.** bullet.** After the sentence "The specification states no deadline for `Attach`.", insert

```
The gateway holds one `Attach` stream for a session from its first message delivery until it releases the
session's binding. The end of the client request that delivered a message does not end the stream.
```

**SPEC-2b. The **Degradation.** bullet.** Replace the span from "A gRPC error on the stream while the coordinator is unchanged" through "before treating the pod as lost." with

```
A stream the gateway did not end is a stream failure when it ends with `DEADLINE_EXCEEDED` or `INTERNAL`.
The adapter ends a session's stream with `DEADLINE_EXCEEDED` when the runtime misses its heartbeat ack
(§28.5.3 `CH-MSGSOCK` **Timing.**). The coordinating replica reports a stream failure with the failure reason
`runtime_crash` whether or not a message is outstanding. The
[§7.3](07_session-lifecycle.md#73-retry-and-resume) classifier selects the session's next state, and on that
transition the replica releases the session's binding and, on a transition to `resume_pending` or
`awaiting_client_action`, its coordination lease `REG-COORDLEASE`
([§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**). A pod serving one session is released with the
`failed` disposition ([§6.2](06_warm-pod-model.md#62-pod-state-machine) "Pod crash during an active
session"), and on a pod serving concurrent sessions only the session's slot is released, which counts toward
the whole-pod replacement trigger as the
[§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Failure isolation:**
bullet states. A session the classifier moves to `resume_pending` is re-attached on a replacement pod
from its last checkpoint when a pod is allocated within `maxResumeWindowSeconds`, and reaches
`awaiting_client_action` when that window elapses
([§7.2](07_session-lifecycle.md#72-interactive-session-model),
[§7.3](07_session-lifecycle.md#73-retry-and-resume)). A stream that ends with `UNAVAILABLE`
produces no stream-failure report, and the replica discards the cached stream. The specification does not
state how the gateway distinguishes a lost pod from a partitioned one on this channel, so a single stream's
`UNAVAILABLE` end is not treated as a loss of the connection to the pod. On a pod serving concurrent
sessions, the gateway's reaction to a lost connection is the one that
[§10.1.4](10_gateway-internals.md#1014-coordinator-loss-detection-and-hold-state)
**Whole-pod connection loss when `maxConcurrentSessions > 1`.** states, and that reaction is not a
stream-failure report. A stream that closes when the runtime's output ends, or that ends with any other status, is
discarded without a report; a runtime that has exited is reported when the next message delivery's
`Attach` ends with `INTERNAL`. A stream the gateway ends itself, when it releases the session's binding or
discards its cached streams, is not a failure. The gateway does not redial `Attach` after a stream failure,
and a later message delivery opens a new stream only while the replica still holds the session's binding.
```

**SPEC-2c. §28.6, the **The second opener on those channels.** paragraph.** Replace "The specification states no limit on how many `CH-ATTACH` streams one holding replica opens at the same time;" with

```
The holding replica holds one `CH-ATTACH` stream per session (§28.5.1);
```

**SPEC-2d. §28.5.3 `CH-MSGSOCK` card, the **Degradation.** bullet.** Replace "reports the failure to the gateway, and does not restart the agent; retry is handled by the gateway at the session level" with

```
and does not restart the agent; the gateway detects the failure on the session's `CH-ATTACH` stream
(§28.5.1) and handles retry at the session level
```

**SPEC-2e. spec/04_system-components.md §4.7, item 5 **Agent crash isolation:**.** Replace "reports the failure to the gateway, and does not restart the agent. The gateway handles retry at the session level." with

```
and does not restart the agent. The gateway detects the failure on the session's `CH-ATTACH` stream ([Section 28.5.1](28_communication-channels.md#2851-gateway-to-pod)) and handles retry at the session level.
```

**SPEC-2f. §28.5.3, the **Exit Codes** table, row `1`.** Replace "adapter logs stderr and reports failure to gateway" with

```
adapter logs stderr; the gateway detects the failure on the session's `CH-ATTACH` stream (§28.5.1)
```

**SPEC-2g. The `CH-ATTACH` **Exclusivity.** bullet.** Replace "One coordinating replica per session. The guard is" with

```
At most one coordinating replica per session, which holds and releases the lease at the points
[§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:**
states. The guard is
```

**SPEC-2h. §28.3 **Register-entry register**, the `REG-COORDLEASE` row.** Replace "One holder per tenant and session, on a compare-and-set with a 60 second expiry" with "At most one holder per tenant and session, on a compare-and-set with a 60 second expiry, acquired and released at the points [§10.1.1](10_gateway-internals.md#1011-stateless-replicas-and-per-session-coordination) **Lease lifecycle:** states". The row stays one physical line.

### SPEC-3 · spec/28_communication-channels.md § 28.8 Failure and degradation matrix, `CH-ATTACH` and `CH-MSGSOCK` rows

Land SPEC-3 in the same edit as SPEC-2. Replace the `CH-ATTACH` row with the line below. The **Holder of the exclusivity constraint changes** cell is unchanged.

```
| `CH-ATTACH` | Loss of the coordinating replica is detected by the pod's gRPC transport within 15 seconds, after which the adapter enters hold state and rejects every inbound RPC other than `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` error detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1). A stream that ends with `UNAVAILABLE` is discarded without a stream-failure report, and the specification does not state how the gateway distinguishes a lost pod from a partitioned one on this channel (§28.5.1). On a pod serving concurrent sessions, the gateway's reaction to a lost connection is the one that [§10.1.4](10_gateway-internals.md#1014-coordinator-loss-detection-and-hold-state) **Whole-pod connection loss when `maxConcurrentSessions > 1`.** states, and that reaction is not a stream-failure report. The gateway does not redial `Attach` after a stream failure, and a later message delivery opens a new stream only while the replica still holds the session's binding (§28.5.1) | A stream the gateway did not end that ends with `DEADLINE_EXCEEDED`, which the adapter uses when the runtime misses its heartbeat ack, or with `INTERNAL` is a stream failure (§28.5.1). The coordinating replica reports it with the failure reason `runtime_crash` whether or not a message is outstanding. The session's next state follows the [§7.3](07_session-lifecycle.md#73-retry-and-resume) resume flow. The replica releases the session's binding (§28.5.1). A stream that closes when the runtime's output ends, or that ends with any other status, is discarded without a report (§28.5.1) | The constraint is one coordinating replica per session, guarded by `REG-COORDLEASE` and the `coordination_generation` stamp (§28.5.1, §28.3). A replica that receives a generation-stale rejection cancels all in-flight RPCs for the session without retrying and discards its cached in-memory streams, and the acquiring replica may not send this RPC until its `CH-FENCE` acknowledgement returns ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) | The session state transitions `resume_pending`, `awaiting_client_action`, and `failed` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§7.3](07_session-lifecycle.md#73-retry-and-resume)), `lenny_slot_failure_total` for a failed slot on a pod serving concurrent sessions ([§16.1](16_observability.md#161-metrics)), and the `coordinator_hold` error detail on a rejected RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification names no metric or alert scoped to this channel, and names no retirement-counter reason for a `failed`-disposition retirement of a pod serving one session |
```

In the `CH-MSGSOCK` row, replace "reports the failure to the gateway, and does not restart the agent; retry is handled by the gateway at the session level" with "and does not restart the agent; the gateway detects the failure on the session's `CH-ATTACH` stream (§28.5.1) and handles retry at the session level". The row stays one physical line.

### SPEC-4 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool configuration and execution modes, **Failure isolation:** bullet, **Recycle lifecycle (`recycle.enabled: true`).**, and the **Pod retirement policy (recycling pools).** paragraph that begins "A session that ends in failure or a crash also retires the pod"; spec/06_warm-pod-model.md § 6.1, **One-session-per-pod default and the recycle opt-in.**, and § 6.2, the per-slot sub-states scoped to concurrent occupancy, the recycle `claimed ──→ draining` edge, and **Pod crash during an active session.**; spec/04_system-components.md § 4.6.3, the `SandboxClaim.status.phase` `failed` bullet

**SPEC-4a. §5.2, the **Failure isolation:** bullet.** Append to the end of the bullet, after "and the gateway applies the slot retry policy below.":

```
A slot whose session's `CH-ATTACH` stream fails after the session has started follows the stream-failure rule of [Section 28.5.1](28_communication-channels.md#2851-gateway-to-pod). The gateway counts that slot as `failed` toward the **Whole-pod replacement trigger:** below, and the session's retry and exhaustion follow [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) in place of the other slot retry policy bullets. A runtime process that stops answering heartbeats ends every open `CH-ATTACH` stream on the pod.
```

**SPEC-4b. §5.2, the **Pod retirement policy (recycling pools).** paragraph that begins "A session that ends in failure or a crash also retires the pod".** Replace "A session that ends in failure or a crash also retires the pod, regardless of recycle settings." with

```
On a pool with `maxConcurrentSessions: 1`, a session that ends in failure or a crash also retires the pod, regardless of recycle settings. On a pool with `maxConcurrentSessions > 1`, a failed session's slot is handled as the **Failure isolation:** bullet below states.
```

**SPEC-4c. §5.2, **Recycle lifecycle (`recycle.enabled: true`).**.** Replace "A session that ends in failure or a crash always retires its pod regardless of recycle settings." with

```
A session that ends in failure or a crash retires its pod as the **Pod retirement policy (recycling pools).** paragraph that begins "On a pool with `maxConcurrentSessions: 1`" states.
```

**SPEC-4d. §6.1 What a Pre-Warmed Pod Looks Like, **One-session-per-pod default and the recycle opt-in.**.** Replace "A session that ends in failure or a crash always retires its pod regardless of recycle settings." with

```
A session that ends in failure or a crash retires its pod as the **Pod retirement policy (recycling pools).** paragraph that begins "On a pool with `maxConcurrentSessions: 1`" in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states.
```

**SPEC-4e. §6.2 Pod State Machine, the per-slot sub-states scoped to concurrent occupancy.** In the line `running ──→ failed                    (non-retryable error: OOM, workspace validation, policy rejection)`, replace the parenthetical with `(non-retryable error: OOM, workspace validation, policy rejection; or a CH-ATTACH stream failure, see §5.2)`.

**SPEC-4f. §4.6.3 CRD Field Ownership and Write Boundaries, the `SandboxClaim.status.phase` enumeration, the `failed` bullet.** Replace "or a failed or crashed session;" with "or a failed or crashed session on a pool with `maxConcurrentSessions: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes));".

**SPEC-4g. §6.2 Pod State Machine, the recycle `claimed ──→ draining` edge.** In the parenthetical that begins `(recycle disposition retires the pod:`, replace `a failed session;` with `a failed session on a maxConcurrentSessions: 1 pool (see §5.2);`, and rewrap the parenthetical's continuation lines at the block's existing indentation.

**SPEC-4h. §6.2 Pod State Machine, **Pod crash during an active session.**.** Replace "The crashed pod is always retired through the drain path regardless of `recycle` settings: a pod that fails mid-session never re-enters the warm pool and never reaches `reserved`." with

```
The crashed pod is retired as the **Pod retirement policy (recycling pools).** paragraph that begins "On a pool with `maxConcurrentSessions: 1`" in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states; a pod retired this way goes through the drain path, never re-enters the warm pool, and never reaches `reserved`.
```

In the paragraph after the bullets that begins "The `resume_pending` transition here", replace "the crashed pod is drained, a replacement is claimed," with "a replacement pod is claimed,".

SPEC-1c edits the **Retry exhaustion:** and **maxSessionRetries** bullets of the same paragraph, and SPEC-4h's anchor sentence is outside both, so the two edits do not overlap.

SPEC-4 changes no other text in §4.6.3, §5.2, §6.1, or §6.2. The §6.2 **Pod failure during active slots.** bullet covers a pod crash, node eviction, or OOM kill, and it stays as it is.

## Spec files touched

- `spec/04_system-components.md` (SPEC-2, SPEC-4)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-4)
- `spec/06_warm-pod-model.md` (SPEC-1, SPEC-4)
- `spec/07_session-lifecycle.md` (SPEC-1)
- `spec/08_recursive-delegation.md` (SPEC-1)
- `spec/10_gateway-internals.md` (SPEC-1)
- `spec/12_storage-architecture.md` (SPEC-1)
- `spec/15_external-api-surface.md` (SPEC-1)
- `spec/28_communication-channels.md` (SPEC-2, SPEC-3)
- `spec/29_communication-scenarios.md` (SPEC-1)
