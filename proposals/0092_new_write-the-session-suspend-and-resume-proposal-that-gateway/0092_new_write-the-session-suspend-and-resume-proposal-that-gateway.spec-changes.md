# Spec changes: Session suspend, idle suspension, and resume driver

## Design (as the spec must state it)

The specification names one actor that moves a session out of `resume_pending`: the resume driver, which runs on the session's coordinating replica. SPEC-1 states it in §7.3 under **Resume driver.**, together with the snapshotless rebuild rule (step 3e) and the retry accounting. Every other route into recovery, which is a failure edge, the `resuming` watchdog, `POST /v1/sessions/{id}/resume`, and a message or `resume_session` call to a podless `suspended` session, writes `resume_pending` to the session row and leaves the restore to the driver. `retryPolicy.maxRetries` is the only retry budget, and `sessionPolicy.maxSessionRetries` is deleted.

SPEC-2 states when `REG-COORDLEASE` is kept and adopted in the recovering and suspended states. The coordinating replica keeps renewing the lease in every non-terminal state after the first bind, and a peer adopts a lapsed lease in every ever-bound non-terminal state except `starting`. The adopter runs the §10.1.2 generation compare-and-swap in every case and the fence only when a pod is bound.

SPEC-3 makes `POST /v1/sessions/{id}/resume` the REST counterpart of `resume_session`. From `awaiting_client_action` and from a podless `suspended` session, the call is a SessionStore write that any replica serves. From a `suspended` session whose pod is held, the coordinating replica performs the state write and the resume RPC, as §7.2 delivery path 6 already states for a message. `RESUME_FAILED` is deleted.

SPEC-4 corrects the §5.2 comment that calls `maxSessionAgeSeconds` a wall-clock cap. The §6.2 active-time pause table and its evaluation paragraph stay as they are.

SPEC-5 states idle suspension, the suspension reason, the release disposition and its meaning on a concurrent pod, the suspended-session lifetime, resume on any message, and the budget-key TTL rule that keeps §8.3 consistent with that lifetime.

## Edge cases and accepted failure modes

- **Pre-claim failure.** Pool exhaustion, credential exhaustion, a Token Service outage, a failed pin read, or a pool-resolution error before the claim writes nothing. The session stays in `resume_pending` until a later attempt succeeds or `maxResumeWindowSeconds` moves it to `awaiting_client_action` (§7.3 step 3c). SPEC-1 **Resume driver.** item 1 owns this.
- **Terminate or cancel racing the driver.** A terminal write that lands before the driver's `resume_pending → resuming` write takes the existing pre-attach collapse. A terminal write that lands after it takes the mid-resume snapshot-close sequence. SPEC-1 **Resume driver.** item 2 states the driver's side.
- **Setup failure during a rebuild.** A deterministic setup-command failure during a snapshotless rebuild is non-retryable under §7.3 and moves the session from `resuming` to `awaiting_client_action` with the failure reason `setup_command_failed`. No caller receives `SETUP_COMMAND_FAILED` for a resume.
- **Coordinator death in a recovering or suspended state.** A peer adopts the lapsed lease under SPEC-2. In `resume_pending`, the adopter's driver performs the restore. In `awaiting_client_action` and in podless `suspended`, adoption gives the §29.3 terminate, delete, resolution, and events rows a holder.
- **Adoption of `starting`.** SPEC-2 leaves it unstated. The summary **Defects** entry records the residual.
- **`input_required` with an absent client.** The idle clock is not evaluated in `input_required`, so such a session keeps its pod until `maxSessionAge` expires it or the `lenny/request_input` call expires. SPEC-5 states this in the idle table.
- **A runtime that hangs mid-turn.** A turn is in flight, so the idle clock never suspends it. The `CH-ATTACH` stream-failure rule that proposal 0091 lands detects the hang.
- **A root session that cycles between `awaiting_client_action` and `resume_pending` without entering `suspended`.** The budget-key TTL is re-armed only when a root leaves `suspended`, so a root that a client keeps resuming without a successful restore for longer than `delegation.budgetKeyTTLSeconds` meets `BUDGET_KEYS_EXPIRED`. This is accepted.
- **Off-holder message or `/resume` to a held-pod `suspended` session.** §7.2 path 6 requires a forward to the coordinator, and no inter-replica carrier for it exists. The summary **Defects** entry records the residual.

## Staged edits

Proposal 0091 lands before this proposal and edits several of the passages below. Where it does, the anchor quoted here is the text after 0091's edit, and the edit names the 0091 deliverable that produced it. Every table row this proposal edits or adds is one physical line. The spec/29 prose is hard-wrapped, so match its anchors with line breaks ignored and keep its wrapping style in the replacement.

### SPEC-1 · spec/07_session-lifecycle.md § 7.3; spec/06_warm-pod-model.md § 6.2; spec/05_runtime-registry-and-pool-model.md § 5.2

**SPEC-1a. §7.3 Retry and Resume, **Resume flow after pod failure:**, steps 3b and 3e.** Replace the line

```
   b. Allocate new warm pod (may wait if pool is temporarily exhausted)
```

with

```
   b. The resume driver allocates a new warm pod (may wait if pool is temporarily exhausted)
```

Replace the line

```
   e. Replay latest workspace checkpoint
```

with

```
   e. Replay latest workspace checkpoint. A session that has no checkpoint is rebuilt instead: the gateway
      materializes the workspace on the replacement pod from the session's stored workspace plan, runs the
      plan's setup commands, and starts the runtime with no carried conversation state, and steps 3f and 3g
      do not apply
```

**SPEC-1b. §7.3, after the paragraph that begins "A step in this flow that fails after the gateway has issued its first pod-side RPC".** Insert

```
**Resume driver.** The resume driver is the gateway component that moves a session out of `resume_pending`, and no other component does. It runs on the session's coordinating replica, which is the replica holding the coordination lease `REG-COORDLEASE` ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)), and it attempts the restore of every `resume_pending` session that replica coordinates while the session's `maxResumeWindowSeconds` window is open. Every route into recovery writes `resume_pending` to the session row and leaves the restore to the driver: a failure edge of this flow, the `resuming` watchdog ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), `POST /v1/sessions/{id}/resume` ([Section 15.1](15_external-api-surface.md#151-rest-api)), and a message or a `resume_session` call addressed to a `suspended` session whose pod was released ([Section 7.2](#72-interactive-session-model) delivery path 6). An attempt proceeds in this order:

1. The driver claims a replacement pod (step 3b). A claim that fails writes nothing to the session row. The session stays in `resume_pending`, the driver attempts again later, and step 3c bounds the wait.
2. Once the pod is claimed, and before the first RPC to the pod, the driver writes `resume_pending → resuming`. When the session row has left `resume_pending` by then, the driver releases the claimed pod and stops.
3. The driver restores the session onto the pod (steps 3d to 3g). A failure from this point follows the **`resuming` failure transitions** in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).
4. On success the driver writes `resuming → running` together with the new `recovery_generation`. It then delivers the messages buffered while the session was recovering, in FIFO order ([Section 7.2](#72-interactive-session-model)), before any message that arrives after the transition.

**Retry accounting.** `retryPolicy.maxRetries` is the only retry budget. `retryCount` advances by one each time a pod failure or the `resuming` watchdog moves the session into `resume_pending`: from `starting`, `running`, `input_required`, `suspended` with its pod held, or `resuming`. A successful restore does not advance it. Neither does a client-initiated entry into `resume_pending`, which is `POST /v1/sessions/{id}/resume` from `awaiting_client_action`, or a message or `resume_session` call to a `suspended` session whose pod was released.
```

**SPEC-1c. §6.2 Pod State Machine, **Pod crash during an active session.**, the **Retry policy:** and **maxSessionRetries** bullets.** Replace the **Retry policy:** bullet with

```
- **Retry policy:** A retryable failure with `retryCount < retryPolicy.maxRetries` moves the session to `resume_pending`, and the resume driver ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume driver.**) claims a fresh pod from the warm pool and restores the session onto it. Where a session checkpoint exists, the replacement pod's workspace is restored from the latest checkpoint; otherwise it is rebuilt as step 3e of the [Section 7.3](07_session-lifecycle.md#73-retry-and-resume) resume flow states. Nothing is carried over directly from the crashed pod.
```

Delete the **maxSessionRetries** bullet, which after 0091 SPEC-1c reads

```
- **maxSessionRetries** is a `sessionPolicy` field (default: `1`). Setting it to `0` disables automatic retries.
```

**SPEC-1d. §5.2 Pool Configuration and Execution Modes, the `sessionPolicy` YAML block.** Delete the line

```
  maxSessionRetries: 1                    # crash re-dispatch attempts (default: 1, giving 2 total attempts; 0 disables retries)
```

### SPEC-2 · spec/10_gateway-internals.md § 10.1.1 **Per-session coordination:** and § 10.1.2

**SPEC-2a. §10.1.1 Stateless Replicas and Per-Session Coordination, the **Primary:** bullet.** After "If that replica dies, another picks up after TTL expiry." append, in the same bullet

```
 Releasing a session's pod binding does not release its lease. The coordinating replica keeps renewing the lease in every non-terminal state after the session's first bind. When the lease lapses, another replica adopts any session in `running`, `input_required`, `suspended`, `resume_pending`, or `awaiting_client_action`. The adopter runs [§10.1.2](#1012-coordinator-handoff-protocol) steps 0 and 1. When the session has a bound pod, it also runs steps 2 and 3. A `resume_pending` session adopted this way is restored by the adopter's [§7.3](07_session-lifecycle.md#73-retry-and-resume) resume driver. A session in `created`, `finalizing`, or `ready` has never been bound and is never adopted. Adoption of a `starting` session is not stated.
```

**SPEC-2b. §10.1.2 Coordinator Handoff Protocol, step 2.** Replace

```
2. **Fence announcement (precondition):** Send a `CoordinatorFence(session_id, new_generation)` RPC
```

with

```
2. **Fence announcement (precondition):** When the session has no bound pod, the acquiring replica skips steps 2 and 3. The pod it binds next is fenced at bind. Otherwise, send a `CoordinatorFence(session_id, new_generation)` RPC
```

### SPEC-3 · spec/15_external-api-surface.md § 15.1, § 15.2; spec/07_session-lifecycle.md § 7.2; spec/04_system-components.md § 4.7.1; spec/05_runtime-registry-and-pool-model.md § 5.2; spec/29_communication-scenarios.md § 29.3, § 29.6

**SPEC-3a. §15.1 REST API, the endpoint table row for `/v1/sessions/{id}/resume`.** Replace the description cell "Explicitly resume after retry exhaustion" with

```
Resume a session in `awaiting_client_action` or `suspended`
```

**SPEC-3b. §15.1, the precondition table row `POST /v1/sessions/{id}/resume`.** Replace the row with

```
| `POST /v1/sessions/{id}/resume`    | `awaiting_client_action`, `suspended`                              | `awaiting_client_action` → `resume_pending`; `suspended` → `resume_pending` when the pod was released; `suspended` → `running` when the pod is still held | The REST counterpart of the `resume_session` tool ([Section 15.2](#152-mcp-api)). From `awaiting_client_action`, and from a `suspended` session whose pod was released, the call is a SessionStore write that any replica serves: it returns `200` with the session row in `resume_pending`, the coordinating replica's resume driver performs the restore ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)), and the restore's outcome arrives on the event stream as a `state_change` event. From a `suspended` session whose pod is still held, the transition follows the rule [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) delivery path 6 states: the coordinating replica performs the state write and the resume RPC to the pod, and the call returns `200` with the row in `running`. `resuming` is an internal-only transient state between `resume_pending` and `running`; the API reports the transition as `resume_pending` → `running`. A setup-command failure during the restore does not reach this call; the session enters `awaiting_client_action` with the failure reason `setup_command_failed`. |
```

**SPEC-3c. §15.1, the error catalog.** Delete the `RESUME_FAILED` row. In the `SETUP_COMMAND_FAILED` row, replace

```
`POST /v1/sessions/{id}/start` (concurrent-workspace pool, whose slot materializes and runs setup at start), and `POST /v1/sessions/{id}/resume` (resume-time setup).
```

with

```
and `POST /v1/sessions/{id}/start` (concurrent-workspace pool, whose slot materializes and runs setup at start). A setup command that fails during a resume is not returned to a caller: the session enters `awaiting_client_action` with the failure reason `setup_command_failed` ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)).
```

The list then reads "`POST /v1/sessions/{id}/finalize` (decomposed lifecycle), `POST /v1/sessions/start` (combined create-and-start), and `POST /v1/sessions/{id}/start` (...)". In the same row, replace

```
it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback
```

with

```
it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED` fallback
```

**SPEC-3d. §15.1, the session-state table rows `suspended` and `resume_pending`.** Replace the `suspended` description cell "Agent paused via `interrupt`; pod held, workspace preserved" with

```
Agent paused via `interrupt` or by idle suspension; workspace preserved, pod held until released ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine))
```

Replace the `resume_pending` description cell "Pod failed; gateway is retrying on a new pod" with

```
Gateway is restoring the session onto a new pod
```

**SPEC-3e. §15.2 MCP API, the `resume_session` tool row.** Replace the description cell "Resume a suspended or paused session" with

```
Resume a `suspended` session or a session in `awaiting_client_action`; REST equivalent is `POST /v1/sessions/{id}/resume`
```

**SPEC-3f. §7.2 Interactive Session Model, **Pre-attached vs. post-attached failure visibility.**.** Replace

```
at whichever of `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/start`, `POST /v1/sessions/{id}/start`, or `POST /v1/sessions/{id}/resume` ran the setup
```

with

```
at whichever of `POST /v1/sessions/{id}/finalize`, `POST /v1/sessions/start`, or `POST /v1/sessions/{id}/start` ran the setup
```

**SPEC-3g. §4.7.1 Role and Gateway RPC Contract, the paragraph that begins "Neither `SLOT_BIND_ATTEMPT_SUPERSEDED` nor `SLOT_BIND_ALREADY_STARTED`".** Replace "(`SESSION_CREATION_FAILED`, `STARTING_FAILED`, or `RESUME_FAILED`)" with "(`SESSION_CREATION_FAILED` or `STARTING_FAILED`)". After "rather than with `SETUP_COMMAND_FAILED` or any other non-retryable error." insert

```
 A resume's bind that meets either refusal answers no client request: the session takes the **`resuming` failure transitions** of [Section 6.2](06_warm-pod-model.md#62-pod-state-machine), or stays in `resume_pending` for the resume driver ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)) when the refusal arrives before the replacement pod is claimed.
```

**SPEC-3h. §5.2, the paragraph that begins "The pin persists across the recycle-to-idle edge".** Replace

```
(`SESSION_CREATION_FAILED`, `STARTING_FAILED`, or `RESUME_FAILED`, [Section 15.1](15_external-api-surface.md#151-rest-api)).
```

with

```
(`SESSION_CREATION_FAILED` or `STARTING_FAILED`, [Section 15.1](15_external-api-surface.md#151-rest-api)). On the resume path a failed pin read claims no pod, so the session stays in `resume_pending` for the resume driver ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)).
```

**SPEC-3i. §29.3 Interactive message send, **Off-holder matrix.**, the lead paragraphs.** In the sentence that begins "The rows whose outcome is not a forward are stated by the rows themselves:", replace

```
built-in adapter row addresses no pre-existing session and so has no holder to forward to, and the
store-only row requires no forwarding at all.
```

with

```
built-in adapter row addresses no pre-existing session and so has no holder to forward to, the
`POST /v1/sessions/{id}/resume` rows for `awaiting_client_action` and for a `suspended` session whose pod
was released write the session row alone, and the store-only row requires no forwarding at all.
```

The list of forwarding rows ("the interrupt, terminate, delete, resume, interaction-resolution, upload, and events JSON rows below") keeps "resume", because the held-pod `suspended` resume row forwards.

**SPEC-3j. §29.3, the matrix row `POST /v1/sessions/{id}/resume` | `awaiting_client_action`.** Replace the row with the following rows

```
| `POST /v1/sessions/{id}/resume` | `awaiting_client_action` | No forwarding is required; the serving replica writes the [§15.1](15_external-api-surface.md#151-rest-api) transition and the coordinating replica's resume driver performs the restore and the delegation-tree traversal ([§7.3](07_session-lifecycle.md#73-retry-and-resume)). The serving replica does not run the traversal, because its view of a descendant held by another replica is not evidence that the descendant is orphaned | §29 |
| `POST /v1/sessions/{id}/resume` | `suspended`, pod released | No forwarding is required; the serving replica writes the [§15.1](15_external-api-surface.md#151-rest-api) transition and the coordinating replica's resume driver performs the restore and the delegation-tree traversal ([§7.3](07_session-lifecycle.md#73-retry-and-resume)) | §29 |
| `POST /v1/sessions/{id}/resume` | `suspended`, pod still held | Forward to the coordinator, which performs the state write and the resume RPC to the pod that [§7.2](07_session-lifecycle.md#72-interactive-session-model) delivery path 6 states. The serving replica writes no state transition of its own. On an unreachable coordinator it fails closed | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
```

**SPEC-3k. §29.6 Restore and resume, the intro paragraph.** Replace

```
and a `delivery: "immediate"` message
addressed to a `suspended` session whose pod was already released drives the same transition
```

with

```
and a message, a `resume_session` call, or a
`POST /v1/sessions/{id}/resume` call addressed to a `suspended` session whose pod was already released
drives the same transition
```

**SPEC-3l. §29.6, **Preconditions.**.** Replace the paragraph with

```
**Preconditions.** The session is in `awaiting_client_action`, which a session reaches through auto-retry
exhaustion or through `maxResumeWindowSeconds` elapsing in `resume_pending` while no pod was available
([§15.1](15_external-api-surface.md#151-rest-api),
[§7.3](07_session-lifecycle.md#73-retry-and-resume)). Any replica can serve steps 1 through 3. The gateway
replica that performs steps 4 through 14 is the session's coordinating replica, which holds the session's
coordination lease `REG-COORDLEASE` and runs the §7.3 resume driver, and the pod validates the
`coordination_generation` stamp on every gateway-to-pod RPC and rejects a stale one
([§10.1](10_gateway-internals.md#101-horizontal-scaling), §28.3).
```

**SPEC-3m. §29.6, step 2.** Replace the text from "then applies the" through the end of the step with

```
then applies the
   endpoint's precondition table, which admits `awaiting_client_action` and `suspended`. A `suspended`
   session whose pod was released is admitted and takes the same step 3 write. A `suspended` session whose
   pod is still held leaves this trace for [§7.2](07_session-lifecycle.md#72-interactive-session-model)
   delivery path 6, and a call against a terminal row is rejected with `409 INVALID_STATE_TRANSITION`
   ([§15.1](15_external-api-surface.md#151-rest-api)).
```

**SPEC-3n. §29.6, step 3.** After "the API reports the whole sequence as `resume_pending → running` ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§15.1](15_external-api-surface.md#151-rest-api))." append, keeping the step's wrapping

```
The serving replica answers the call with `200` and the row in `resume_pending`.
The coordinating replica's resume driver performs every later step
([§7.3](07_session-lifecycle.md#73-retry-and-resume) **Resume driver.**).
```

**SPEC-3o. §29.6, step 4.** Replace the text from "A pool or credential exhaustion, a Token Service outage, or a transient setup-time transport failure on this path is returned to the caller as `RESUME_FAILED`" through the end of the step with

```
A pool or credential
   exhaustion, a Token Service outage, or a failed pin read before the claim writes nothing to the session
   row: the session stays in `resume_pending`, the driver attempts again later, and
   `maxResumeWindowSeconds` bounds the wait. Once the claim succeeds, and before the first RPC to the pod,
   the driver writes `resume_pending → resuming`
   ([§7.3](07_session-lifecycle.md#73-retry-and-resume),
   [§7.2](07_session-lifecycle.md#72-interactive-session-model)).
```

**SPEC-3p. §29.6, after step 13.** Append

```
14. `gateway` → `adapter`, `CH-ATTACH`, `gateway-to-pod`. The gateway delivers the messages buffered for
    the session while it was recovering, in FIFO order and before any message that arrives after the
    session reached `running` ([§7.2](07_session-lifecycle.md#72-interactive-session-model),
    [§7.3](07_session-lifecycle.md#73-retry-and-resume)).
```

### SPEC-4 · spec/05_runtime-registry-and-pool-model.md § 5.2

**SPEC-4a. §5.2, the `sessionPolicy` YAML block.** Replace the line

```
  maxSessionAgeSeconds: 7200              # wall-clock session age cap (default: 7200)
```

with

```
  maxSessionAgeSeconds: 7200              # active-time session age cap; paused in suspended and the recovery states (see Section 6.2) (default: 7200)
```

The §6.2 paragraph after the **`maxSessionAge` timer behavior across states:** table is not edited.

### SPEC-5 · spec/06_warm-pod-model.md § 6.2; spec/07_session-lifecycle.md § 7.2; spec/15_external-api-surface.md § 15.1, § 15.4; spec/29_communication-scenarios.md § 29.3; spec/05_runtime-registry-and-pool-model.md § 5.2; spec/11_policy-and-controls.md § 11.3; spec/08_recursive-delegation.md § 8.3; spec/09_mcp-integration.md § 9.2; spec/16_observability.md § 16.1; spec/14_workspace-plan-schema.md; spec/17_deployment-topology.md § 17.8.1; spec/27_web-playground.md § 27.6

**SPEC-5a. §6.2, the **`suspended` state:** transition block.** After the line `running → suspended   (interrupt_request timeout — deadlineMs elapsed without interrupt_acknowledged; adapter forces suspended, RPC returns INTERRUPT_TIMEOUT)` insert

```
running → suspended   (maxClientIdleSeconds elapsed with no turn in flight — idle suspension; pod released at once)
```

Replace the four lines

```
suspended → running   (resume_session — no new content; pod still held)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate; pod still held)
suspended → resume_pending (resume_session; pod was released by maxSuspendedPodHoldSeconds)
suspended → resume_pending (POST /v1/sessions/{id}/messages delivery:immediate; pod was released — message held in session inbox, delivered after pod acquisition and workspace restore)
```

with

```
suspended → running   (resume_session, POST /v1/sessions/{id}/resume, or any message; pod still held)
suspended → resume_pending (resume_session, POST /v1/sessions/{id}/resume, or any message; pod was released — a message is held in the session inbox and delivered after pod acquisition and workspace restore)
```

After the line `suspended → expired   (delegation lease perChildMaxAge wall-clock expiry while suspended)` insert

```
suspended → expired   (gateway.maxSuspendedSessionSeconds elapsed since entry to suspended)
```

**SPEC-5b. §6.2, **Graceful pod release during extended suspension (`maxSuspendedPodHoldSeconds`).** and its numbered steps 1 and 2.** Replace the paragraph's opening, from "When a session has been in `suspended` state for longer than" through "Behavior when the timer fires:", with

```
The gateway records on the session row why a session entered `suspended`: `InterruptAcknowledged` or `InterruptTimeout` for an interrupt, and `Idle` for an idle suspension (see "Idle suspension" below). A session suspended by an interrupt holds its pod for `maxSuspendedPodHoldSeconds` (default: 900s / 15 minutes) from its entry to `suspended`, so a client can decide without a pod reallocation. A session suspended for idleness has a hold of zero. When the hold ends, the gateway checkpoints the workspace and releases the pod. The effective hold is `min(deployment_value, tenant_value)` — the deployer sets a platform-wide ceiling via `gateway.maxSuspendedPodHoldSeconds` (Helm), and tenants may request a lower value via their tenant configuration. The most restrictive wins. Behavior when the hold ends:
```

Replace step 2 with

```
2. **If checkpoint succeeds:** the gateway clears the session's pod binding, then releases the pod, and emits a `session.pod_released_during_suspension` structured event. The release takes the pool's normal session-end disposition: a pod on a recycling pool recycles, and any other pod drains ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). On a pod with `maxConcurrentSessions > 1`, the release frees only the session's slot, and the pod's other sessions keep running. The session remains in `suspended` — no state change. The hold timer stops (it has served its purpose).
```

Step 3 is unchanged.

**SPEC-5c. §6.2, the paragraph after the release steps, which begins "Once the pod is released".** Replace the paragraph with

```
Once the pod is released, `resume_session`, `POST /v1/sessions/{id}/resume`, and any message route the session through `resume_pending` instead of going directly to `running`. The transition is a session-row write that any gateway replica performs, and the coordinating replica's resume driver ([§7.3](07_session-lifecycle.md#73-retry-and-resume)) then acquires a new pod and restores the workspace from checkpoint. The `maxResumeWindowSeconds` countdown starts only when `resume_pending` is entered, so the human is not racing against a timer while the session sits podless in `suspended`. A message that resumes a suspended-without-pod session is held in the session inbox ([§7.2](07_session-lifecycle.md#72-interactive-session-model)) and delivered after pod acquisition and workspace restore complete. The standard `resume_pending` inbox handling applies: with `durableInbox: true`, messages remain in the Redis-backed inbox; with `durableInbox: false` (default), the inbox-to-DLQ drain ([§7.2](07_session-lifecycle.md#72-interactive-session-model)) fires atomically with the state transition, moving messages to the Redis DLQ.
```

**SPEC-5d. §6.2, **Interaction with other timers during podless suspension:**.** Replace "`perChildMaxAge` (wall-clock) continues ticking — if it fires while suspended-without-pod, the session transitions directly to `expired` (no pod to release; checkpoint already happened)." with

```
`perChildMaxAge` (wall-clock) and the suspended-session lifetime `gateway.maxSuspendedSessionSeconds` continue ticking — if either fires while suspended-without-pod, the session transitions directly to `expired` (no pod to release; checkpoint already happened).
```

**SPEC-5e. §6.2, **`maxClientIdleSeconds` clock behavior across states.**, the intro, the qualifying-event bullets, and the clock table.** In the intro, replace "it terminates a session after continuous client inactivity and replaces" with "it suspends a session after continuous inactivity, releasing the session's pod or slot, and replaces". In the agent-work bullet, replace "so an autonomously working session is never idle-terminated" with "so an autonomously working session is never idle-suspended". In the direct-mode bullet, replace "so a hung or wedged pod that emits no tokens still idle-terminates" with "so a wedged pod that emits no tokens between turns is still idle-suspended". Replace the clock table rows `running`, `input_required`, `awaiting_client_action`, and `suspended` with

```
| `running`                | **Active.** Resets on every qualifying event (see list above). When elapsed time since `last_agent_activity_at` exceeds the effective `maxClientIdleSeconds` and the runtime has no turn in flight, which means no message delivered to it is still being processed, the gateway suspends the session (see "Idle suspension" below). |
| `input_required`         | **Not evaluated.** A turn is in flight while the runtime is blocked in `lenny/request_input`, so the session is not idle. Elicitation waits behave the same way ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)). Such a session keeps its pod until `maxSessionAge` expires it or the `lenny/request_input` call expires. |
| `awaiting_client_action` | **Paused.** The session holds no pod, and `maxAwaitingClientActionSeconds` reclaims it ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)). |
| `suspended`              | **Paused.** The suspended-session lifetime bounds this state (see "`suspended → expired` trigger mechanism" below). |
```

**SPEC-5f. §6.2, the paragraph after the clock table, which begins "The clock fires the `expired` transition independently of `maxSessionAge`".** Replace the text from the start of the paragraph through "The effective per-session value is resolved through the existing most-restrictive timeout resolution." with

```
When the clock fires, the gateway suspends the session. The default value is 900 seconds (15 minutes), and the effective per-session value is resolved through the existing most-restrictive timeout resolution.
```

The **Origin-scoped override:** text that follows is unchanged. After the paragraph, insert

```
**Idle suspension.** When the `maxClientIdleSeconds` clock fires for a `running` session whose runtime has no turn in flight, the gateway writes `running → suspended` with the suspension reason `Idle` and releases the session's pod or slot at once through the graceful release above, with a hold of zero. Any message, a `resume_session` call, or `POST /v1/sessions/{id}/resume` resumes the session ([§7.2](07_session-lifecycle.md#72-interactive-session-model) delivery path 6).
```

**SPEC-5g. §6.2, **`suspended → expired` trigger mechanism:**.** Replace the paragraph with

```
**`suspended → expired` trigger mechanism:** Both `maxSessionAge` and `maxClientIdleSeconds` are paused during `suspended`, and two wall-clock deadlines expire a suspended session. The suspended-session lifetime `gateway.maxSuspendedSessionSeconds` (default: 604800s / 7 days, operator-tunable) is measured from the session's entry to `suspended` and applies to every session; when it elapses, the gateway transitions the session to `expired` with the `max_idle_time` reason on the `dev.lenny.session_expired` event ([Section 14](14_workspace-plan-schema.md)). The delegation lease's `perChildMaxAge` ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) is not paused during suspension and expires a suspended delegation child when it elapses first. A podless suspended session costs the session row in Postgres and, when the session has a delegation tree, the budget keys in Redis. The budget keys' TTL exceeds the suspended-session lifetime and is re-armed when a root session leaves `suspended` ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) **Defense-in-depth TTL.**), so the TTL does not fire during a suspension.
```

**SPEC-5h. §7.2 Interactive Session Model, the **Session state machine:** block.** After the line `running → suspended        (interrupt_request timeout — deadlineMs elapsed without interrupt_acknowledged; adapter forces suspended, RPC returns INTERRUPT_TIMEOUT)` insert

```
running → suspended        (maxClientIdleSeconds elapsed with no turn in flight — idle suspension; pod released at once, see §6.2)
```

Replace the four lines

```
suspended → running        (resume_session — no new content; pod still held)
suspended → running        (POST /v1/sessions/{id}/messages delivery:immediate; pod still held)
suspended → resume_pending (resume_session; pod was released by maxSuspendedPodHoldSeconds)
suspended → resume_pending (POST /v1/sessions/{id}/messages delivery:immediate; pod was released — message held in session inbox)
```

with

```
suspended → running        (resume_session, POST /v1/sessions/{id}/resume, or any message; pod still held)
suspended → resume_pending (resume_session, POST /v1/sessions/{id}/resume, or any message; pod was released — a message is held in session inbox)
```

After the line `suspended → expired        (delegation lease perChildMaxAge wall-clock expiry while suspended)` insert

```
suspended → expired        (gateway.maxSuspendedSessionSeconds elapsed since entry to suspended — see §6.2)
```

**SPEC-5i. §7.2, **Message delivery routing**, delivery path 6.** Replace the path from "6. **Target session is `suspended`**" through the end of the paragraph that ends "its `delivery: immediate` resume rows restate the rule stated here and cite this section as its owner." with

```
6. **Target session is `suspended`** → the message resumes the session, whatever its `delivery` value. How it resumes depends on whether the session still holds a pod (see [§6.2](06_warm-pod-model.md#62-pod-state-machine) "Graceful pod release during extended suspension"):
   - **Pod still held:** The gateway atomically resumes the session (`suspended → running`) and delivers the message to the runtime's stdin pipe once the runtime reports `ready_for_input`. The delivery receipt is `delivered` on successful resume-and-deliver.
   - **Pod released (podless suspension):** The gateway transitions the session to `resume_pending` (`suspended → resume_pending`). The message is held in the session inbox; the standard `resume_pending` inbox handling applies (inbox-to-DLQ drain for `durableInbox: false`, or Redis inbox retention for `durableInbox: true` — see [§7.2](#72-interactive-session-model)). The coordinating replica's resume driver ([§7.3](#73-retry-and-resume)) acquires a new pod and restores the workspace from checkpoint, then delivers the buffered messages in FIFO order. The delivery receipt is `queued`.

   This applies uniformly to all message sources: external client (`POST /v1/sessions/{id}/messages`) and inter-session via `lenny/send_message`. A `resume_session` call and `POST /v1/sessions/{id}/resume` take the same two branches without delivering content. **Coordinator routing for a suspended-session resume:** The pod-held `suspended → running` transition requires a Postgres state write and a resume RPC to the pod, both of which must be performed by the session's coordinating gateway replica. When such a message lands on a non-coordinator replica, that replica forwards the message to the session's coordinator (identified via the coordination lease in Redis/Postgres), and the coordinator executes the atomic resume-and-deliver sequence. If the coordinator is unreachable (e.g., crashed, network partition), the forwarding replica falls back to inbox buffering with a `queued` delivery receipt status — the message is not silently dropped. The coordinator forwarding mechanism reuses the same internal gRPC `ForwardMessage` RPC used for all cross-replica message routing (see [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) per-session coordination). The podless `suspended → resume_pending` transition is a SessionStore write that any replica performs, so it needs no forward. The off-holder matrix in [Section 29.3](29_communication-scenarios.md#293-interactive-message-send) states the required outcome for the other session-scoped client routes its rows name when they are served by a replica that is not the session's coordinating replica; its `suspended` message rows restate the rule stated here and cite this section as its owner.
```

**SPEC-5j. §15.1, the precondition table row `POST /v1/sessions/{id}/messages`.** Replace the transition cell "`running` (if `suspended` with `delivery: immediate`, atomically resumes and delivers); no state change for other states" with

```
`suspended` → `running` when the pod is held, `suspended` → `resume_pending` when it was released, for any `delivery` value; no state change for other states
```

**SPEC-5k. §15.4 Runtime Adapter Specification, the **`delivery`** table.** In the `"immediate"` row, delete "If session is `suspended`, the gateway atomically resumes (`suspended → running`) then delivers. ". In the `"queued"` row, replace "Delivered in FIFO order when the runtime next enters `ready_for_input`." with "Delivered in FIFO order when the runtime next enters `ready_for_input`; a `suspended` target resumes first (see below the table)." After the table's last row and before "No other values are valid.", insert

```
A message addressed to a `suspended` session resumes the session, whatever its `delivery` value, as [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) delivery path 6 states.
```

**SPEC-5l. §29.3 Interactive message send, the step 1 prose and the **Off-holder matrix.** message rows.** In step 1, replace

```
and the message carries `delivery: "immediate"` against a `suspended`
   session, §7.2 requires the serving replica to forward the message to the coordinator
```

with

```
and the message targets a `suspended` session whose pod is still
   held, §7.2 requires the serving replica to forward the message to the coordinator
```

Replace the matrix row `POST /v1/sessions/{id}/messages` carrying `delivery: immediate` | `suspended` with

```
| `POST /v1/sessions/{id}/messages`, any `delivery` value | `suspended`, pod still held | Forward to the coordinator, which performs the atomic resume-and-deliver. On an unreachable coordinator the serving replica falls back to inbox buffering with a `queued` receipt. The serving replica writes no state transition of its own | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
| `POST /v1/sessions/{id}/messages`, any `delivery` value | `suspended`, pod released | No forwarding is required. The serving replica writes the `suspended` to `resume_pending` transition, buffers the message, and answers `queued`; the coordinating replica's resume driver restores the session and delivers the buffered messages ([§7.3](07_session-lifecycle.md#73-retry-and-resume)) | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
```

Replace the matrix row `The MCP tool surface, `lenny/send_message` carrying `delivery: immediate`` | `suspended` with

```
| The MCP tool surface, `lenny/send_message`, any `delivery` value | `suspended` | The same requirement as the `suspended` message-send rows, which §7.2 states for both of its message sources | [§7.2](07_session-lifecycle.md#72-interactive-session-model) |
```

**SPEC-5m. §5.2, the `sessionPolicy` YAML block and the **`maxClientIdleSeconds`** paragraph.** Replace the line

```
  maxClientIdleSeconds: 7200              # client-inactivity bound; defaults to the effective maxSessionAgeSeconds (see Section 6.2)
```

with

```
  maxClientIdleSeconds: 900               # inactivity bound before idle suspension (default: 900; see Section 6.2)
```

Replace the paragraph that begins "**`maxClientIdleSeconds`** terminates a session after continuous client inactivity." with

```
**`maxClientIdleSeconds`** suspends a `running` session after continuous inactivity while its runtime has no turn in flight, and the suspension releases the session's pod or slot at once. It is the single platform idle bound; the activity definition, the per-state clock table, and the idle suspension are specified in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine). The default is 900 seconds.
```

**SPEC-5n. §11.3 Timeouts and Cancellation, the timeout table.** Replace the row `Max client idle time` with

```
| Max client idle time                      | 900s    | `sessionPolicy.maxClientIdleSeconds`               | Yes (per pool)     | [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), [§6.2](06_warm-pod-model.md#62-pod-state-machine) |
```

After the row `Max suspended pod hold`, insert

```
| Max suspended session lifetime            | 604800s | `gateway.maxSuspendedSessionSeconds`               | Yes                | [§6.2](06_warm-pod-model.md#62-pod-state-machine) |
```

Replace the row `Delegation budget key TTL` with

```
| Delegation budget key TTL                 | 691200s | `delegation.budgetKeyTTLSeconds`                   | Yes (must exceed `gateway.maxSuspendedSessionSeconds`) | [§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)           |
```

**SPEC-5o. §8.3 Delegation Policy and Lease, **Defense-in-depth TTL.**.** Replace

```
(default: 259200s / 72h, configurable via Helm) at tree creation time.
```

with

```
(default: 691200s / 8 days, configurable via Helm) at tree creation time.
```

Replace the sentence "The TTL is deliberately generous — it must never fire during normal operation, including sessions that spend extended time in `suspended` state (where `maxSessionAge` is paused and the session may persist indefinitely after pod release; see [§6.2](06_warm-pod-model.md#62-pod-state-machine) `maxSuspendedPodHoldSeconds`)." with

```
The TTL must never fire during normal operation, including a suspension. `delegation.budgetKeyTTLSeconds` must therefore exceed the suspended-session lifetime `gateway.maxSuspendedSessionSeconds` ([§6.2](06_warm-pod-model.md#62-pod-state-machine)), and when a root session that has a delegation tree leaves `suspended`, the gateway re-arms the TTL of that root's tree-wide budget keys to `delegation.budgetKeyTTLSeconds`.
```

**SPEC-5p. §9.2 Elicitation Chain, the **Idle clock:** item.** Replace the item's text after "**Idle clock:**" with

```
The `maxClientIdleSeconds` clock ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), [§6.2](06_warm-pod-model.md#62-pod-state-machine)) is not evaluated while a session waits for an elicitation response, because a turn is in flight during the wait. The elicitation response is client activity and resets the clock when it arrives.
```

**SPEC-5q. §16.1 Metrics, the `lenny_session_expiry_total` row.** Replace "the `max_idle_time` series counts client-inactivity terminations under the `maxClientIdleSeconds` bound ([§6.2](06_warm-pod-model.md#62-pod-state-machine))" with

```
the `max_idle_time` series counts `suspended` sessions that reached the suspended-session lifetime `gateway.maxSuspendedSessionSeconds` ([§6.2](06_warm-pod-model.md#62-pod-state-machine))
```

**SPEC-5r. spec/14_workspace-plan-schema.md, the `dev.lenny.session_expired` line.** Replace "(maxSessionAge or maxClientIdleSeconds)" with "(maxSessionAge or the suspended-session lifetime maxSuspendedSessionSeconds)".

**SPEC-5s. §17.8.1 Operational Defaults — Quick Reference.** Replace the row `Max client idle time (`maxClientIdleSeconds`)` with

```
| Max client idle time (`maxClientIdleSeconds`) | 900 s (idle suspension)                                                  | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
| Max suspended session lifetime (`gateway.maxSuspendedSessionSeconds`) | 604800 s (7 d)                                   | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
```

**SPEC-5t. §27.6 Session lifecycle and cleanup.** In the **Idle-timeout override.** bullet, replace "This caps the reclamation window after the best-effort cancel below fails to deliver." with

```
When the bound fires, the session is suspended and its pod is released ([§6.2](06_warm-pod-model.md#62-pod-state-machine)), which caps the reclamation window after the best-effort cancel below fails to deliver.
```

In the browser-close bullet, replace "fires within the 5-minute playground default rather than after the platform `maxClientIdleSeconds` default (the pool's effective `maxSessionAgeSeconds`, 2 hours by default)." with

```
suspends the session and releases its pod within the 5-minute playground default rather than after the platform `maxClientIdleSeconds` default of 15 minutes.
```

**SPEC-5u. Bounded sweep.** After SPEC-5a to SPEC-5t, grep `spec/` for `delivery: immediate`, `delivery:immediate`, `delivery: "immediate"`, `maxIdleTimeSeconds`, `idle-terminat`, and `remain indefinitely`. Reconcile only a statement that conditions the resume of a `suspended` session on `delivery: immediate`, gives `maxClientIdleSeconds` an expiry outcome or a default other than 900 seconds, or lets a root `suspended` session persist without bound. Leave unchanged every statement about `delivery: immediate` interrupting a `running` session and the `input_required` exception.

## Spec files touched

- `spec/04_system-components.md` (SPEC-3g)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1d, SPEC-3h, SPEC-4a, SPEC-5m)
- `spec/06_warm-pod-model.md` (SPEC-1c, SPEC-5a to SPEC-5g)
- `spec/07_session-lifecycle.md` (SPEC-1a, SPEC-1b, SPEC-3f, SPEC-5h, SPEC-5i)
- `spec/08_recursive-delegation.md` (SPEC-5o)
- `spec/09_mcp-integration.md` (SPEC-5p)
- `spec/10_gateway-internals.md` (SPEC-2a, SPEC-2b)
- `spec/11_policy-and-controls.md` (SPEC-5n)
- `spec/14_workspace-plan-schema.md` (SPEC-5r)
- `spec/15_external-api-surface.md` (SPEC-3a to SPEC-3e, SPEC-5j, SPEC-5k)
- `spec/16_observability.md` (SPEC-5q)
- `spec/17_deployment-topology.md` (SPEC-5s)
- `spec/27_web-playground.md` (SPEC-5t)
- `spec/29_communication-scenarios.md` (SPEC-3i to SPEC-3p, SPEC-5l)
