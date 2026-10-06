# Spec changes: Gateway does not survive or react to Attach stream failure

## Design (as the spec must state it)

The specification states the gateway's handling of a session's `CH-ATTACH` stream in one place, the §28.5.1 `CH-ATTACH` card. SPEC-2a states the stream lifetime on the card, and SPEC-2b states the stream-failure rule, the report, the discarded ends, and the absence of a redial. The RECORDS-1 claim row records the card as the owner. The §28.8 `CH-ATTACH` row restates the card and cites it, as the §28.8 preamble requires.

SPEC-1 states the mid-session failure outcome once, in §7.3 **Resume flow after pod failure**, and every other statement of it names its edge or cites that flow. On a pod serving concurrent sessions, a slot whose stream fails is handled as SPEC-4 states.

## Edge cases and accepted failure modes

- **Abandoned turn.** A request that ends mid-turn stops waiting. The runtime's late reply to that turn is consumed by the gateway and is never delivered as a later message's reply. The spec text states only the lifetime rule; the correlation mechanism belongs to CODE-1.
- **Hung runtime between turns.** The held stream keeps the adapter's heartbeat running, so a runtime that hangs while no message is outstanding is detected and reported under SPEC-2b.
- **Hang before the first message, or after an `UNAVAILABLE` end.** No stream is open, so no heartbeat runs. The next message delivery or the session watchdog detects the hang. This is accepted.
- **Connection loss.** An `UNAVAILABLE` end is discarded without a report. The specification does not state how the gateway distinguishes a lost pod from a partitioned one on this channel, and the card records that.
- **Hung runtime on a pod serving concurrent sessions.** Every open slot stream on the pod fails. Each failed slot counts toward the §5.2 whole-pod replacement trigger. A pod with fewer open slot streams than the trigger threshold is reused until the trigger fires or the slots end. This adopts proposal 0079 unchanged.
- **Session in `resume_pending` with no re-dispatch driver.** The session waits out `maxResumeWindowSeconds` and then enters `awaiting_client_action`, unless the client resumes it. The card keeps its existing replacement-pod re-attach sentence; the missing driver is filed by RECORDS-1.
- **Launch-time retry paths.** The pre-running `starting → failed (retries exhausted, ...)` edge in §7.2 and the §6.2 pre-attached retry policy's **Exhaustion:** bullet describe launch-time retries that the §7.3 classifier does not govern. They stay unchanged.

## Staged edits

Every table row this proposal edits is one physical line. The spec/28 cards and §28.6 are hard-wrapped, so match their anchors with line breaks ignored and keep their wrapping style in the replacement.

### SPEC-1 · spec/07_session-lifecycle.md § 7.1, § 7.2, § 7.3; spec/06_warm-pod-model.md § 6.2; spec/08_recursive-delegation.md § 8.3, § 8.8, § 8.10; spec/15_external-api-surface.md § 15.4.3

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
- **Retry exhaustion:** When retries are exhausted or the failure is non-retryable, the session takes the next state that [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume flow after pod failure** selects. The failed pod is released from the pool and terminated, and the gateway returns a structured error to the client.
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

**SPEC-1e. §8.8 TaskRecord and TaskResult Schema, the **Lenny canonical task state machine:** block and the **Recovery transitions are session-level, not task-level.** paragraph.** In the block, replace

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

In the "On failure:" `TaskResult` example, replace `"category": "TRANSIENT"` with `"category": "PERMANENT"` and `"retriesExhausted": true` with `"retriesExhausted": false`.

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

### SPEC-2 · spec/28_communication-channels.md § 28.5.1 `CH-ATTACH` card, § 28.5.3 `CH-MSGSOCK` card, § 28.6; spec/04_system-components.md § 4.7

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
transition the replica releases the session's binding. A pod serving one session is released with the
`failed` disposition ([§6.2](06_warm-pod-model.md#62-pod-state-machine) "Pod crash during an active
session"), and on a pod serving concurrent sessions only the session's slot is released, which counts toward
the whole-pod replacement trigger as the
[§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Failure isolation:**
bullet states. A session the classifier moves to `resume_pending` is re-attached on a replacement pod
from its last checkpoint when a pod is allocated within `maxResumeWindowSeconds`, and reaches
`awaiting_client_action` when that window elapses
([§7.2](07_session-lifecycle.md#72-interactive-session-model),
[§7.3](07_session-lifecycle.md#73-retry-and-resume)). A stream that ends with `UNAVAILABLE` reports a loss
of the connection to the pod: the replica discards the cached stream and reports no stream failure. The
specification does not state how the gateway distinguishes a lost pod from a partitioned one on this
channel. A stream that closes when the runtime's output ends, or that ends with any other status, is
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

### SPEC-3 · spec/28_communication-channels.md § 28.8 Failure and degradation matrix, `CH-ATTACH` and `CH-MSGSOCK` rows

Land SPEC-3 in the same edit as SPEC-2. Replace the `CH-ATTACH` row with the line below. The **Holder of the exclusivity constraint changes** cell is unchanged.

```
| `CH-ATTACH` | Loss of the coordinating replica is detected by the pod's gRPC transport within 15 seconds, after which the adapter enters hold state and rejects every inbound RPC other than `CoordinatorFence` with `UNAVAILABLE` and a `coordinator_hold` error detail ([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.5.1). A stream that ends with `UNAVAILABLE` is discarded without a stream-failure report, and the specification does not state how the gateway distinguishes a lost pod from a partitioned one on this channel (§28.5.1). The gateway does not redial `Attach` after a stream failure, and a later message delivery opens a new stream only while the replica still holds the session's binding (§28.5.1) | A stream the gateway did not end that ends with `DEADLINE_EXCEEDED`, which the adapter uses when the runtime misses its heartbeat ack, or with `INTERNAL` is a stream failure (§28.5.1). The coordinating replica reports it with the failure reason `runtime_crash` whether or not a message is outstanding. The session's next state follows the [§7.3](07_session-lifecycle.md#73-retry-and-resume) resume flow. The replica releases the session's binding (§28.5.1). A stream that closes when the runtime's output ends, or that ends with any other status, is discarded without a report (§28.5.1) | The constraint is one coordinating replica per session, guarded by `REG-COORDLEASE` and the `coordination_generation` stamp (§28.5.1, §28.3). A replica that receives a generation-stale rejection cancels all in-flight RPCs for the session without retrying and discards its cached in-memory streams, and the acquiring replica may not send this RPC until its `CH-FENCE` acknowledgement returns ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) | The session state transitions `resume_pending`, `awaiting_client_action`, and `failed` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§7.3](07_session-lifecycle.md#73-retry-and-resume)), `lenny_slot_failure_total` for a failed slot on a pod serving concurrent sessions ([§16.1](16_observability.md#161-metrics)), and the `coordinator_hold` error detail on a rejected RPC ([§10.1](10_gateway-internals.md#101-horizontal-scaling)). The specification names no metric or alert scoped to this channel, and names no retirement-counter reason for a `failed`-disposition retirement of a pod serving one session |
```

In the `CH-MSGSOCK` row, replace "reports the failure to the gateway, and does not restart the agent; retry is handled by the gateway at the session level" with "and does not restart the agent; the gateway detects the failure on the session's `CH-ATTACH` stream (§28.5.1) and handles retry at the session level". The row stays one physical line.

### SPEC-4 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool configuration and execution modes, **Slot failure and cleanup (`maxConcurrentSessions > 1`).**, **Failure isolation:** bullet

Append to the end of the bullet, after "and the gateway applies the slot retry policy below.":

```
A slot whose session's `CH-ATTACH` stream fails after the session has started follows the stream-failure rule of [Section 28.5.1](28_communication-channels.md#2851-gateway-to-pod). The gateway counts that slot as `failed` toward the **Whole-pod replacement trigger:** below, and the session's retry and exhaustion follow [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) in place of the other slot retry policy bullets. A runtime process that stops answering heartbeats ends every open `CH-ATTACH` stream on the pod.
```

SPEC-4 changes no other text in §5.2 or §6.2. The §6.2 **Pod failure during active slots.** bullet covers a pod crash, node eviction, or OOM kill, and stays as it is.

## Spec files touched

- `spec/04_system-components.md` (SPEC-2)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-4)
- `spec/06_warm-pod-model.md` (SPEC-1)
- `spec/07_session-lifecycle.md` (SPEC-1)
- `spec/08_recursive-delegation.md` (SPEC-1)
- `spec/15_external-api-surface.md` (SPEC-1)
- `spec/28_communication-channels.md` (SPEC-2, SPEC-3)
