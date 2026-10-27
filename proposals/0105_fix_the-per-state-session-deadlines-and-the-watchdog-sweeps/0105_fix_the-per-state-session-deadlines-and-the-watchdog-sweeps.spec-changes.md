# Spec changes — Per-state session deadlines and watchdog sweeps are under-specified and mis-implemented

## Design (as the spec must state it)

- The session row carries `state_entered_at`, the instant the session entered its current state. Every per-state deadline in §6.2 that is measured from entry to a state reads it, apart from the two suspended deadlines, which read the instant recorded when the session moved into `suspended` (SPEC-1 Edit (a)).
- The idle clock restarts on every entry to `running` (SPEC-1 Edit (b)).
- `POST /v1/sessions/{id}/start` moves the session `ready → starting` before the runtime launch, so the `maxStartingTimeoutSeconds` watchdog bounds the launch and the ready watchdog no longer applies. A `/start` that fails after that write moves the session to `failed`, and the client retries by creating a new session (SPEC-2 Edits (a), (b), (f) to (i), and (k)).
- §7.2 **Terminal writes from any non-terminal state.** is the one home for the terminal writes that apply from every non-terminal state, and the §6.2 `resuming` enumeration excepts them (SPEC-2 Edits (c) and (d)). The `maxSessionAge` expiry is an edge per source state, from `starting`, `resume_pending`, and `resuming` (SPEC-2 Edits (a), (d), and (e)).
- Terminate is admitted from `input_required`, with the `input_required → completed` edge (SPEC-2 Edit (l)).
- `perChildMaxAge` counts from the creation of the child session whose row stores it (SPEC-3).
- §10.1.1 states which replica runs the watchdog sweeps and orphan cleanup, the locked-row rule for their writes, and per-row continuation past errors (SPEC-4).

## Edge cases and accepted failure modes

- A write that leaves the state unchanged leaves `state_entered_at` unchanged, so it does not extend a deadline (SPEC-1).
- A session that leaves a state and re-enters it gets a newer `state_entered_at`, so a watchdog write prepared from the earlier listing finds the locked row within its deadline and does not apply (SPEC-1, SPEC-4).
- An activity stamp that lands between an idle sweep's listing and its write keeps the session, because the write re-evaluates the idle deadline on the locked row (SPEC-4).
- A session returning to `running` from `input_required`, `starting`, or a recovery state starts with a fresh idle clock, so a `lenny/request_input` wait that ends by timeout or cancel is not counted as idle time (SPEC-1 Edit (b)).
- A concurrent-workspace setup at `/start` that outlasts `maxStartingTimeoutSeconds` fails with `STARTING_TIMEOUT` (SPEC-2 Edit (i)).
- A terminal write that SPEC-2 Edit (c) lists and that arrives while the session is in `resume_pending` or `resuming` ends the session from that state (SPEC-2 Edits (c) and (j)).
- A `perChildMaxAge` expiry keeps counting while the child is suspended or recovering (SPEC-3, with the existing §6.2 **`suspended → expired` trigger mechanism:**).
- One tenant whose session listing fails, or one session whose write fails, is evaluated again on the next pass, and the other tenants and sessions are processed in the same pass (SPEC-4).
- Two replicas that both find the same session due write it once, because the second write finds the locked row no longer meeting the deadline or no longer non-terminal (SPEC-4).

## Staged edits

### SPEC-1 · spec/06_warm-pod-model.md § 6.2 Pod State Machine

**Edit (a).** Insert the following paragraph directly after the paragraph that begins "The gateway persists the `accumulated_session_age_seconds` value in Postgres on every state transition that pauses or resumes the timer." and before the paragraph that begins "**`maxClientIdleSeconds` clock behavior across states.**"

```markdown
The gateway also persists `state_entered_at` on the session row, the instant the session entered its current state. The session store sets it to the creation instant when it creates the row, sets it on every write that changes the session's state, and leaves it unchanged on a write that does not change the state. Each per-state deadline in this section that is measured from the session's entry to a state is measured from `state_entered_at`, except the suspended-session lifetime and the `maxSuspendedPodHoldSeconds` pod hold, which are measured from the instant the gateway records on the row when it moves the session into `suspended`.
```

**Edit (b).** In the paragraph that begins "**`maxClientIdleSeconds` clock behavior across states.**", replace

```markdown
The `last_agent_activity_at` timestamp is updated in Postgres on each qualifying event and on each entry to `running` from a state where the clock is paused.
```

with

```markdown
The `last_agent_activity_at` timestamp is updated in Postgres on each qualifying event and on each entry to `running`.
```

### SPEC-2 · spec/07_session-lifecycle.md § 7.1 Normal Flow, § 7.2 Interactive Session Model, and § 7.3 Retry and Resume; spec/06_warm-pod-model.md § 6.2 Pod State Machine; spec/15_external-api-surface.md § 15.1 REST API; spec/29_communication-scenarios.md § 29.3 Interactive message send; spec/08_recursive-delegation.md § 8.8 TaskRecord and TaskResult Schema

**Edit (a) · §7.2 **Session state machine:** code block.** Replace the three lines

```
starting → running         (agent runtime reaches ready — session becomes active)
starting → resume_pending  (pod crash / gRPC error during agent runtime launch, retryCount < maxRetries)
starting → failed          (retries exhausted, or STARTING_TIMEOUT expired — see §6.3 maxStartingTimeoutSeconds)
```

with

```
ready → starting           (POST /v1/sessions/{id}/start accepted; the maxStartingTimeoutSeconds watchdog bounds the launch — see §6.2)
starting → running         (agent runtime reaches ready — session becomes active)
starting → failed          (POST /v1/sessions/{id}/start fails after its ready → starting write; or STARTING_TIMEOUT expired — see §6.2)
starting → expired         (maxSessionAge exceeded — see §6.2 "maxSessionAge timer behavior across states")
```

**Edit (b) · §7.2 **Pre-attached vs. post-attached failure visibility.**** In that paragraph, make two replacements.

Replace

```markdown
surfaces as the non-retryable `SETUP_COMMAND_FAILED` (HTTP 422) at whichever of `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/start`, or `POST /v1/sessions/{id}/start` ran the setup; a transient transport failure during setup stays the retryable atomic-unit fallback for the endpoint that ran it; and a runtime-launch failure surfaces at `POST /v1/sessions/{id}/start`).
```

with

```markdown
surfaces as the non-retryable `SETUP_COMMAND_FAILED` (HTTP 422) at whichever of `POST /v1/sessions/{id}/finalize` or `POST /v1/sessions/start` ran the setup; and a transient transport failure during setup stays the retryable atomic-unit fallback for the endpoint that ran it).
```

Replace

```markdown
The `starting → resume_pending` / `starting → failed` transitions above cover **post-attached** agent-runtime launch failures — specifically, failures that occur after the session has transitioned into the externally visible `starting` state and is visible to the client via `GET /v1/sessions/{id}`. These failures produce a visible `resume_pending` event so the client sees the recovery in progress.
```

with

```markdown
The `starting → failed` transition above covers **post-attached** failures: the `POST /v1/sessions/{id}/start` launch, including the setup a concurrent-workspace slot runs at start, runs after the session has entered the externally visible `starting` state, which the client sees through `GET /v1/sessions/{id}`.
```

**Edit (c) · §7.2, new paragraph.** Insert the following paragraph directly after the closing fence of the **Session state machine:** code block and before the paragraph that begins "`input_required` is a **sub-state of `running`**".

```markdown
**Terminal writes from any non-terminal state.** The following terminal writes apply from every non-terminal state, in addition to the edges listed above. An operator force-terminate ([§24.11](24_lenny-ctl-command-reference.md#2411-session-investigation)) moves the session to `failed` with reason `FORCE_TERMINATED`. A budget, credential-lease, or LLM fallback exhaustion moves the session to `expired`. Orphan cleanup ([§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)) moves an orphaned delegated session to `expired`. A delegation lease `perChildMaxAge` expiry ([§8.2](08_recursive-delegation.md#82-delegation-mechanism)) moves the delegated child to `expired`.
```

**Edit (d) · §6.2 **`resuming` failure transitions:**.** Make two replacements and one insertion.

Replace

```markdown
This subsection is the **authoritative enumeration** of every edge out of the session-model `resuming` state (per iter3 SES-013);
```

with

```markdown
This subsection is the **authoritative enumeration** of every edge out of the session-model `resuming` state (per iter3 SES-013), apart from the terminal writes that [§7.2](07_session-lifecycle.md#72-interactive-session-model) **Terminal writes from any non-terminal state.** applies from every non-terminal state;
```

Replace

```markdown
or (b) directly to the terminal `cancelled` / `completed` states via snapshot-close when the client, parent, or agent itself has already decided the session's terminal outcome mid-resume.
```

with

```markdown
or (b) directly to a terminal state via snapshot-close ([§7.2](07_session-lifecycle.md#72-interactive-session-model) "Mid-resume terminal transitions — snapshot-close semantics").
```

Insert the following bullet directly after the bullet that begins "**Terminal session outcome during snapshot replay (`resuming → completed`):**".

```markdown
- **`maxSessionAge` exceeded mid-resume (`resuming → expired`):** A session whose accumulated session age is over its `maxSessionAge` cap collapses directly to `expired` through the mid-resume terminal transition ([§7.2](07_session-lifecycle.md#72-interactive-session-model) "Mid-resume terminal transitions — snapshot-close semantics").
```

**Edit (e) · §7.2 code block, `resume_pending` and `resuming` lines.** In the `resume_pending → completed` line, replace

```
e.g., client POST /v1/sessions/{id}/terminate, rehydrated budget/lease exhaustion evaluation on re-enqueue, TTL wall-clock check, or parent cancellation cascade resolving to graceful completion.
```

with

```
e.g., client POST /v1/sessions/{id}/terminate, or parent cancellation cascade resolving to graceful completion.
```

Insert the following line directly after the `resume_pending → completed` line.

```
resume_pending → expired                (accumulated session age already over maxSessionAge — see [§6.2](06_warm-pod-model.md#62-pod-state-machine) "`maxSessionAge` timer behavior across states")
```

Insert the following line directly after the `resuming → completed` line.

```
resuming → expired                     (accumulated session age already over maxSessionAge — see snapshot-close semantics below and authoritative enumeration in [§6.2](06_warm-pod-model.md#62-pod-state-machine) "`resuming` failure transitions")
```

**Edit (f) · §15.1 error catalog, `STARTING_FAILED` row.** In the Description cell, replace "Retryable for the same reason as `SESSION_CREATION_FAILED`." with the following text. The row stays one physical line.

```markdown
Retryable by creating a new session, because the failed start leaves this session `failed` ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)).
```

**Edit (g) · §6.2 pre-attached retry policy, **Client visibility:** bullet.** Replace

```markdown
The **sole exception** is the `starting_session → resume_pending` transition documented above: once the session has entered the externally visible `starting` state (post-attach), a crash during agent runtime launch is surfaced as a client-visible `resume_pending` event and is governed by the client-facing `retryPolicy` rather than the pre-attached retry policy. In short: crashes **before** `attached` are invisible and use pre-attached retries; crashes **after** `attached` (including during `starting`) are visible and use the `retryPolicy` defined in §7.3.
```

with

```markdown
The **sole exception** is the `POST /v1/sessions/{id}/start` launch, which runs after the session has entered the externally visible `starting` state and takes the `starting` edges of [§7.2](07_session-lifecycle.md#72-interactive-session-model).
```

**Edit (h) · §7.3 **Retry accounting.**.** Replace

```markdown
from `starting`, `running`, `input_required`,
```

with

```markdown
from `running`, `input_required`,
```

**Edit (i) · §6.2 **`maxSessionAge` timer behavior across states:** table, `starting` row.** In that row, insert the following sentence directly after "If the agent runtime does not reach `running` within this window, the session is transitioned to `failed` with reason `STARTING_TIMEOUT`." The row stays one physical line.

```markdown
 On a concurrent-workspace pool, whose reserved slot runs setup at `POST /v1/sessions/{id}/start`, the window also covers that setup, so a setup that outlasts the window fails with `STARTING_TIMEOUT` even within `setupTimeoutSeconds`.
```

**Edit (j) · §7.2 **Mid-resume terminal transitions — snapshot-close semantics.** and **Pre-attach terminal collapse**.** Make the following replacements.

In the **Mid-resume terminal transitions — snapshot-close semantics.** paragraph, replace

```markdown
The `resuming → cancelled` and `resuming → completed` edges terminate the session while the gateway is still restoring it onto a replacement pod. The gateway handles both edges with the same close sequence:
```

with

```markdown
A terminal transition that finds the session in `resuming` terminates the session while the gateway is still restoring it onto a replacement pod. The gateway handles every such transition with the same close sequence:
```

In step 4 of that sequence, replace

```markdown
distinguish an intentional mid-resume cancel or complete from a lost mid-flight.
```

with

```markdown
distinguish an intentional mid-resume collapse from a lost mid-flight.
```

In step 5 of that sequence and in the **Terminal handling otherwise identical.** bullet of **Pre-attach terminal collapse**, replace each occurrence of

```markdown
and the `session_complete` / `session.cancelled` event emission all proceed exactly as for a terminal transition from `running`.
```

with

```markdown
and the event emission for the terminal state all proceed exactly as for a terminal transition from `running`.
```

Replace

```markdown
**Pre-attach terminal collapse — `resume_pending → cancelled` / `resume_pending → completed`.** The direct `resume_pending → cancelled` and `resume_pending → completed` edges in the enumeration above fire when the terminal trigger arrives while the session is still in `resume_pending`, before it transitions into the internal `resuming` state. The distinguishing property of these edges is that **no pod is attached at all**:
```

with

```markdown
**Pre-attach terminal collapse.** A terminal transition that finds the session in `resume_pending` fires before the session transitions into the internal `resuming` state. The distinguishing property of such a transition is that **no pod is attached at all**:
```

In the **No snapshot-close sequence.** bullet, replace

```markdown
Unlike the `resuming → cancelled` / `resuming → completed` edges,
```

with

```markdown
Unlike a mid-resume terminal transition,
```

In the paragraph that begins "The `resume_pending → cancelled` edge is the terminal path for", replace

```markdown
and for a terminal outcome the gateway determines on its own, such as a parent's completion cascade resolving to graceful closure.
```

with

```markdown
and for a parent's completion cascade resolving to graceful closure.
```

and replace

```markdown
the pre-attach variants listed here are authoritative for `resume_pending` directly.
```

with

```markdown
**Pre-attach terminal collapse.** above governs `resume_pending` directly.
```

**Edit (k) · §7.1 **Gateway-owned terminal writes:**.** Replace

```markdown
A terminal write runs on the writer's own context, except for two writes that must not depend on the request that triggered them: the `finalizing → failed` write a failed finalize makes, and an administrative force-terminate together with its audit event. The gateway makes those two on a context it owns.
```

with

```markdown
A terminal write runs on the writer's own context, except for the writes that must not depend on the request that triggered them: the `finalizing → failed` write a failed finalize makes, the `starting → failed` write a failed `POST /v1/sessions/{id}/start` makes, and an administrative force-terminate together with its audit event. The gateway makes those writes on a context it owns.
```

**Edit (l) · `input_required` terminate.** Make the following insertions and replacements. Every table row stays one physical line.

In the §7.2 **Session state machine:** code block, insert the following line directly after the line that begins "input_required → running   (input provided via inReplyTo or request expires/cancelled)".

```
input_required → completed (client POST /v1/sessions/{id}/terminate while awaiting input)
```

In the §6.2 **`input_required` sub-state:** code block, insert the following line directly after the line "input_required → running   (input provided via inReplyTo, or request cancelled/expired)".

```
input_required → completed (terminate while awaiting input)
```

In the §8.8 **Lenny canonical task state machine:** code block, insert the following line directly after the line "input_required → expired               (deadline reached while awaiting input)".

```
input_required → completed             (client terminate while awaiting input)
```

In the §15.1 **State-mutating endpoint preconditions.** table, in the `POST /v1/sessions/{id}/terminate` row, replace "`starting`, `running`, `suspended`" with "`starting`, `running`, `input_required`, `suspended`".

In the §29.3 off-holder matrix, in the `POST /v1/sessions/{id}/terminate` row, replace "`starting`, `running`, `resume_pending`, and `awaiting_client_action`, and `suspended` while" with "`starting`, `running`, `input_required`, `resume_pending`, and `awaiting_client_action`, and `suspended` while".

### SPEC-3 · spec/08_recursive-delegation.md § 8.2 Delegation Mechanism

In the `LeaseSlice` field table, replace the row

```markdown
| `perChildMaxAge`      | int  | Max wall-clock seconds for the child  |
```

with

```markdown
| `perChildMaxAge`      | int  | Max wall-clock seconds for the child, counted from the child session's creation |
```

### SPEC-4 · spec/10_gateway-internals.md § 10.1.1 Stateless Replicas and Per-Session Coordination

In the **Primary:** bullet, insert the following text directly after the sentence "A `resuming` session is not adopted: every replica evaluates the `resuming` watchdog ([§6.2](06_warm-pod-model.md#62-pod-state-machine)) from the session row, and the watchdog moves the session to `resume_pending` or `awaiting_client_action`, where it is adopted." and before the sentence "Adoption of a `starting` session is not stated."

```markdown
Every replica likewise runs the session watchdog's other sweeps ([§6.2](06_warm-pod-model.md#62-pod-state-machine)) and orphan cleanup ([§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)) over the session rows. A watchdog sweep's write re-evaluates the sweep's deadline on the locked session row and takes effect only when that row still meets it. An orphan cleanup write takes effect only on a non-terminal locked row. A failure to list one tenant's sessions or to write one session does not stop either sweep for the other sessions and tenants, and the next pass evaluates the failed session again.
```

## Spec files touched

- spec/06_warm-pod-model.md (SPEC-1; SPEC-2 Edits (d), (g), (i), and (l))
- spec/07_session-lifecycle.md (SPEC-2 Edits (a), (b), (c), (e), (h), (j), (k), and (l))
- spec/08_recursive-delegation.md (SPEC-2 Edit (l); SPEC-3)
- spec/10_gateway-internals.md (SPEC-4)
- spec/15_external-api-surface.md (SPEC-2 Edits (f) and (l))
- spec/29_communication-scenarios.md (SPEC-2 Edit (l))
