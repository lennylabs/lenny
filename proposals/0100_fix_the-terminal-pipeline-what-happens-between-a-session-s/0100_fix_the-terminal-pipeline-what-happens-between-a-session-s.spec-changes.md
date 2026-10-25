# Spec changes — Terminal pipeline completeness: seal and writer context

## Design (as the spec must state it)

§7.1 gains one anchor paragraph, **Terminal pipeline context:** (SPEC-1). It names the work that follows a terminal write as the terminal pipeline, places it on a gateway-owned context, bounds it by the **terminal pipeline budget** (`maxWorkspaceSealDurationSeconds` plus the **release allowance**), names the two writers whose terminal write itself runs on a gateway-owned context, states the post-terminal window, and points the §12.8 drain at that window. Every other staged edit that needs a bound cites that paragraph rather than restating the sum.

The **Seal-and-export invariant:** gains the outcome of a seal whose binding disappears, the export rule for a session whose row no longer exists, and the slot form of "terminates the pod anyway" (SPEC-2). §7.1 **Artifact retention:** states that the TTL runs from the terminal transition (SPEC-3). Derive rule 1 states one sealed-else-checkpoint rule for every terminal state (SPEC-3).

§10.1.7 gains **Terminal pipelines at process exit.**, which states the SIGTERM wait and the bound on a pipeline that SIGKILL or a crash cuts, §29.7 traces the same wait, and the first sentence of the **Seal-and-export invariant:** points to it (SPEC-4). §12.3 gains a durability-table row for a session write whose commit acknowledgement is lost (SPEC-5). §5.2's post-terminal slot activity invariant names the terminal pipeline budget in place of the seal window, and §4.6.1's live-binding orphan predicate also spares a pod whose session ended within `claimOrphanTimeout`, and §10.1.7 gives the intent row of a seal upload and of every other upload a `checkpoint_timeout_at`, and §4.4 and §28 apply deadline-fire retention to every deadline fire (SPEC-6). §7.2 and §6.2 are aligned to §15.1 so that `POST /v1/sessions/{id}/terminate` yields `completed` from `resume_pending` and from the internal `resuming` state (SPEC-7). §15.1 is unchanged.

## Edge cases and accepted failure modes

- A message that arrives between the start of the seal and the release: SPEC-1, last-but-one sentence.
- A seal whose binding on the sealing replica is removed after the seal begins, and a seal or checkpoint for a session whose row no longer exists: SPEC-2.
- A seal that fails on a slot of a pool with `maxConcurrentSessions > 1`: SPEC-2, the "terminates the pod anyway" parenthetical.
- A component that acts on a terminal session's pod, slot, binding, or seal intent row while its terminal pipeline runs: SPEC-1 (a).
- A seal or periodic checkpoint attempt in progress when the §12.5 partial-manifest backstop runs: SPEC-6 (c).
- A terminal source derived during its seal window, including one the seal relabels `failed`: SPEC-3, derive rule 1.
- A session that terminates after its create-time retention deadline has passed: SPEC-3, **Artifact retention:**.
- A terminal pipeline cut by SIGKILL or a crash, including the case where the 240 s gateway grace period is shorter than the budget: SPEC-4, **Terminal pipelines at process exit.**
- A session write whose commit acknowledgement is lost: SPEC-5.
- `POST /v1/sessions/{id}/terminate` against a session in `resume_pending` or `resuming`: SPEC-7.

## Staged edits

### SPEC-1 · spec/07_session-lifecycle.md § 7.1 Normal Flow; spec/29_communication-scenarios.md § 29.4 Interrupt, terminate, and delete; spec/28_communication-channels.md § 28.5.1 Gateway-to-pod; spec/12_storage-architecture.md § 12.8 Compliance Interfaces

(a) Insert a new paragraph immediately after the paragraph that begins "**Seal-and-export invariant:** The workspace is always exported to durable storage before the pod is released." and ends "…while preventing a permanent MinIO outage from holding pods in `draining` indefinitely.", and before the heading `### 7.2 Interactive Session Model`.

```markdown
**Terminal pipeline context:** After a terminal write commits, the gateway runs the session's terminal pipeline: the seal-and-export step (**Seal-and-export invariant:** above), the inbox drain, the terminal events and signals, the pod or slot release, and the close of the session log. The pipeline runs on a context the gateway owns. The end of the request, stream, or background task that made the terminal write does not end the pipeline, and no shorter deadline of that caller bounds it. The **terminal pipeline budget**, counted from the session's terminal transition, bounds the whole pipeline: `maxWorkspaceSealDurationSeconds` plus the **release allowance**, a gateway setting the deployer configures (default 30 s), which bounds the release, the parent notification, and the cascade that follow the seal. The gateway gives a `CH-ATTACH` stream-failure report ([Section 28.5.1](28_communication-channels.md#2851-gateway-to-pod)) the same bound. The seal retry window runs inside the budget. While the pipeline runs, the session is terminal and its pod, its slot, its binding, and the intent row of a seal attempt in progress, with the chunk objects under that row's prefix ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling) **Intent-row-first ordering (orphan prevention).**), are still in use. A component that treats a terminal session as finished does not act on any of them before the pipeline ends. A component that cannot observe the end of the pipeline does not act on one of them until the deadline of the step that uses it has passed, and no such deadline ends later than the budget. The slot set the gateway rebuilds after a Redis loss ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Post-recovery rehydration atomicity.**) and the slot count it gates on during a Redis outage ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) are excepted, because they count only non-terminal sessions: until the release, the pod can run one session more than `maxConcurrentSessions` for each of its sessions in a terminal pipeline, and another session's release can empty the set and run the occupancy-zero disposition while the pipeline still uses the pod. A terminal write runs on the writer's own context, except for two writes that must not depend on the request that triggered them: the `finalizing → failed` write a failed finalize makes, and an administrative force-terminate together with its audit event. The gateway makes those two on a context it owns. From the start of the seal until the release, the gateway delivers no message to the session's runtime and starts no checkpoint of the session other than the seal, including a preStop or `CheckpointBarrier` checkpoint ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)). A message that arrives in that interval is routed by the session's terminal state, as for a message to a terminal session. A terminal pipeline that the gateway process's exit cuts is not resumed; [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) **Terminal pipelines at process exit.** states the shutdown wait and the bound on what remains.
```

(b) In the §7.1 normal-flow code block, replace the lines that begin "20. Gateway → Pod:       Seal workspace", "21. Gateway → Pod:       Shutdown", and "22. Gateway → Store:     Mark session completed" with the following. Steps 19, 23, and 24 keep their numbers and text, because §7.3 cites step 23.

```text
20. Gateway → Store:     Mark session completed, persist final state
21. Gateway → Pod:       Seal workspace — export final workspace snapshot to Artifact Store,
                         record artifact refs
22. Gateway → Pod:       Shutdown
```

(c) In §29.4, insert a new step 11 before the step that begins "11. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by an expiry timer: `gateway` → `adapter`", and renumber the current step 11 as 12:

```markdown
11. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by
    an expiry timer: `gateway` → `postgres`, no register entry, the Postgres `SessionStore` role
    ([§12.2](12_storage-architecture.md#122-storage-roles)). The gateway marks the session terminal and
    persists the final state ([§7.1](07_session-lifecycle.md#71-normal-flow)).
```

Delete the current step 15 (`gateway` → `postgres`). Immediately after the seal step (the current step 11, renumbered 12 above), insert a new step 13 that opens "On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or by an expiry timer: `gateway`, `internal`." and continues with the deleted step's last sentence verbatim, from "On the terminal transition the gateway drains the session's inbox" through "and emits the session's terminal event ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§7.3](07_session-lifecycle.md#73-retry-and-resume))." Renumber the current steps 12 to 14 as 14 to 16 and the current steps 16 to 18 as 17 to 19, and keep the list's four-space continuation indent. In the §29.4 intro paragraph, replace "steps 10 through 18 trace it" with "steps 10 through 19 trace it".

(d) In §28.5.1, in the `CH-ATTACH` **Timing.** bullet, replace "The gateway holds one `Attach` stream for a session from its first message delivery until it releases the session's binding." with:

```markdown
The gateway holds one `Attach` stream for a session from its first message delivery until it releases the
session's binding or, for a session whose terminal write has committed, until the seal begins
([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**).
```

(e) In §12.8, in the paragraph that begins "**Lifecycle fence and drain.**", replace "A successful drain does not wait for the session's terminal steps or the release of its pod, so a step that follows it can start before either one finishes." with:

```markdown
A successful drain does not wait for the session's terminal steps or the release of its pod, and a step that follows it treats a terminal session as finished only as [Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:** permits.
```

### SPEC-2 · spec/07_session-lifecycle.md § 7.1 Normal Flow (Seal-and-export invariant); spec/16_observability.md § 16.1 Metrics, § 16.5 Alerting Rules and SLOs; spec/29_communication-scenarios.md § 29.4 Interrupt, terminate, and delete

Items (a) to (c) edit the paragraph that begins "**Seal-and-export invariant:**"; items (d) to (f) edit restatements of its outcome.

(a) Replace the phrase "terminates the pod anyway, and fires the `WorkspaceSealStuck` alert" with:

```markdown
terminates the pod anyway (for a session on a pool with `maxConcurrentSessions > 1`, it runs the session's slot cleanup and release, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Slot failure and cleanup**), and fires the `WorkspaceSealStuck` alert
```

(b) Immediately after the sentence "The retry window bounds transient export failures; an export failure the adapter reports as permanent ends the window on its first observation with the same terminal outcome.", insert:

```markdown
A seal whose session binding on the sealing replica is removed after the seal begins has not exported the workspace. It ends the window on its first observation with the permanent outcome. The gateway drives no checkpoint or seal export for a session whose row no longer exists. A seal in progress for such a session ends with the permanent outcome, and the replica releases the session's binding.
```

(c) Replace "transitions the session to `failed` with reason `workspace_seal_timeout`" with "relabels the session's committed terminal state as `failed` with reason `workspace_seal_timeout`", and immediately after the sentence that ends "fires the `WorkspaceSealStuck` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos))." insert:

```markdown
The relabel takes no [Section 7.2](#72-interactive-session-model) edge, and the gateway emits the session's terminal events after the seal ends, so they report the relabelled state when the relabel commits.
```

(d) In `spec/16_observability.md` § 16.1, in the row that begins "| Workspace seal duration (`lenny_workspace_seal_duration_seconds`", replace "the `permanent` outcome records a seal that failed on a permanent adapter error and returned immediately without retrying" with "the `permanent` outcome records a seal that ended on a condition the **Seal-and-export invariant:** classifies as permanent and returned immediately without retrying". The row stays one physical line.

(e) In `spec/16_observability.md` § 16.5, in the `WorkspaceSealStuck` row, replace "a seal that fails on a permanent adapter error records `outcome="permanent"`" with "a seal that ends on a condition the seal-and-export invariant classifies as permanent records `outcome="permanent"`", and delete the sentence "Pods held in `draining` beyond this deadline are forcibly terminated and the session is transitioned to `failed` with reason `workspace_seal_timeout`." The row keeps its closing "See [Section 7.1](07_session-lifecycle.md#71-normal-flow) (seal-and-export invariant)." and stays one physical line.

(f) In `spec/29_communication-scenarios.md` § 29.4, in the seal step (renumbered by SPEC-1 (c)), replace "after which the session goes to `failed` with reason `workspace_seal_timeout`, the pod is terminated anyway, and the `WorkspaceSealStuck` alert fires ([§7.1](07_session-lifecycle.md#71-normal-flow))" with "after which the seal-timeout outcome the [§7.1](07_session-lifecycle.md#71-normal-flow) **Seal-and-export invariant:** states applies".

### SPEC-3 · spec/07_session-lifecycle.md § 7.1 Normal Flow (Artifact retention; Derive session semantics); spec/12_storage-architecture.md § 12.5 Artifact Store

(a) In the paragraph that begins "**Artifact retention:**", replace "are retained for a configurable TTL (default: 7 days, deployer-configurable). A background GC job deletes expired artifacts." with:

```markdown
are retained for a configurable TTL (default: 7 days, deployer-configurable) measured from the session's terminal transition. The gateway never shortens a deadline that `POST /v1/sessions/{id}/extend-retention` has set later. A background GC job deletes expired artifacts.
```

Keep the rest of the paragraph, from "Clients can extend retention on specific sessions" onward, unchanged.

(b) Under "**Derive session semantics:**", rule 1, replace the bullet "`completed` — uses the sealed final workspace. This is the recommended and most predictable source state." with the bullet below. Delete the bullet "`failed` — uses the last successful checkpoint snapshot. The response includes `workspaceSnapshotSource: "checkpoint"` and `workspaceSnapshotTimestamp` so the client knows the snapshot predates the failure." and the bullet "`cancelled`, `expired` — uses the sealed workspace if seal completed before termination, otherwise the last successful checkpoint. `workspaceSnapshotSource` indicates which was used.":

```markdown
   - `completed`, `failed`, `cancelled`, `expired` — uses the sealed final workspace once the seal has completed, otherwise the last successful checkpoint. `workspaceSnapshotSource` and `workspaceSnapshotTimestamp` indicate which was used and when it was taken. The sealed workspace of a `completed` session is the recommended and most predictable source.
```

(c) In `spec/12_storage-architecture.md` § 12.5 Artifact Store, replace the list item "Session artifact retention: configurable TTL (default 7 days), extendable per session" with:

```markdown
- Session artifact retention: configurable TTL (default 7 days) measured from the terminal transition, extendable per session
```

### SPEC-4 · spec/10_gateway-internals.md § 10.1.7 preStop Hook Drain; spec/29_communication-scenarios.md § 29.7 Gateway drain; spec/07_session-lifecycle.md § 7.1 Normal Flow (Seal-and-export invariant)

(a) In §10.1.7, insert a new paragraph immediately after the list item "4. **SIGKILL deadline.** If active streams have not drained by the grace period deadline, the process receives SIGKILL and remaining clients must reconnect. Combined with the one-pod-at-a-time scale-down policy, this ensures that at most one replica is draining at any moment." and before the heading `#### 10.1.8 Rolling Updates and the CheckpointBarrier Protocol`:

```markdown
**Terminal pipelines at process exit.** After the preStop hook returns and the process receives SIGTERM, the gateway stops accepting requests and waits until no terminal write, no terminal pipeline, and no release of a terminal session's binding is in progress on the replica, for at most the terminal pipeline budget ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**). Only then does it stop its background work and close its store connections. `terminationGracePeriodSeconds` bounds the preStop stages and this wait as a whole, so the wait has only the time stages 1 to 3 leave, and the default gateway grace period of 240 s is shorter than the default budget. A terminal pipeline that SIGKILL or a crash cuts is not resumed. The session row keeps its terminal state, and the orphaned-claim detection and the stale slot release reclaim the pod and the slot after `claimOrphanTimeout` ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) **Orphaned `SandboxClaim` detection:**).
```

(b) In §29.7, insert a new step between step 10 (which begins "`gateway`, `internal`. The hook polls `active_streams > 0` at 1-second intervals") and step 11, and renumber the current step 11 to 12:

```markdown
11. `gateway`, `internal`. After the preStop hook returns and the process receives SIGTERM, the process stops accepting requests and runs the shutdown wait of [§10.1](10_gateway-internals.md#101-horizontal-scaling) **Terminal pipelines at process exit.** before it closes its store connections.
```

(c) In the renumbered step 12, replace "When active streams have not drained by the grace-period deadline the process receives SIGKILL and the remaining clients must reconnect." with:

```markdown
When active streams or terminal pipelines have not finished by the grace-period deadline, the process receives SIGKILL, the remaining clients must reconnect, and a cut terminal pipeline is not resumed ([§10.1](10_gateway-internals.md#101-horizontal-scaling)).
```

Keep the rest of that step ("Together with the one-pod-at-a-time scale-down policy, …") unchanged.

(d) In §7.1, in the paragraph that begins "**Seal-and-export invariant:**", replace its first sentence "The workspace is always exported to durable storage before the pod is released." with:

```markdown
The workspace is exported to durable storage before the pod is released, except where this paragraph or [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) **Terminal pipelines at process exit.** states otherwise.
```

### SPEC-5 · spec/12_storage-architecture.md § 12.3 Postgres HA Requirements

In the **Write durability categories during failover:** table, insert a new row immediately after the row that begins "| In-flight (uncommitted) session state writes |". The row is one physical line.

```markdown
| Session state write whose commit acknowledgement is lost | **Unknown until checked** — the write may have committed | The write stores a value unique to it in the session row, and after the lost acknowledgement the gateway reads that row from the primary. When the row carries the write's value, the writer proceeds as for an applied write, including its post-commit steps. When it carries another value, or the row cannot be read before the read times out, the writer treats the write as not applied. A committed terminal write misclassified this way runs no terminal pipeline, with the outcome [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) **Terminal pipelines at process exit.** states for a cut pipeline. |
```

### SPEC-6 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool Configuration and Execution Modes; spec/04_system-components.md § 4.6.1 Warm Pool Controller (Pod Lifecycle), § 4.4.2 Checkpoint Atomicity and Partial Manifests, § 4.4.3 Checkpoint Quiescence, Timeout, and Abort Handling; spec/10_gateway-internals.md § 10.1.7 preStop Hook Drain; spec/28_communication-channels.md § 28.5.1 Gateway-to-pod, § 28.8 Failure and degradation matrix

(a) In the paragraph that begins "**Slot assignment atomicity (`maxConcurrentSessions > 1`).**", in the sentence that begins "`claimOrphanTimeout` exceeds, by at least one second, each window in which work of a session that has ended can still act on the session's slot:", replace "and seal-and-export, which `maxWorkspaceSealDurationSeconds` bounds ([Section 7.1](07_session-lifecycle.md#71-normal-flow))" with:

```markdown
and the terminal pipeline, which the terminal pipeline budget bounds (`maxWorkspaceSealDurationSeconds` plus the release allowance, [Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**)
```

(b) In §4.6.1, under **Orphaned `SandboxClaim` detection:**, in the list item that begins "1. **Live binding states (`bound` or `recycling`).**", and only there, because predicate 3 carries the same phrase, replace "and whose pod no active session references" with:

```markdown
and whose pod no session references that is active or whose terminal transition is younger than `claimOrphanTimeout`
```

(c) In `spec/10_gateway-internals.md` §10.1.7, in the list item that begins "1. **Intent-row write (gateway).**", replace "the Stage 2 tier cap for preStop-triggered uploads, or `checkpoint_started_at + checkpointBarrierAckTimeoutSeconds` for barrier-triggered uploads" with:

```markdown
the Stage 2 tier cap for preStop-triggered uploads, `checkpoint_started_at + checkpointBarrierAckTimeoutSeconds` for barrier-triggered uploads, for a seal upload, the end of the seal retry window as the terminal pipeline budget bounds it ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**), or, for any other upload, `checkpoint_started_at` plus the checkpoint timeout of [Section 4.4](04_system-components.md#44-event--checkpoint-store)
```

In the same section, in the list item that begins "3. **Finalisation.**", replace "a deadline fire (the Stage 2 tier cap for preStop uploads, or `checkpointBarrierAckTimeoutSeconds` for barrier-triggered uploads)" with "a deadline fire (the applicable deadline that step 1 names)".

In the same section, in the `manifest_reason` enumeration, replace "exceeded its applicable deadline (Stage 2 tier cap or `checkpointBarrierAckTimeoutSeconds`)" with "exceeded the applicable deadline that step 1 of the intent-row flow names".

(d) In `spec/04_system-components.md` §4.4.3, in the paragraph that begins "**Checkpoint abort cleanup:**", in the sentence that begins "A **deadline-fire** finalisation", delete " on a drain, preStop, or barrier path", and replace "it is reclaimed only by the resume-time cleanup ([§10.1](10_gateway-internals.md#101-horizontal-scaling)) once resume has consumed it, or by the §12.5 backstop after `maxResumeWindowSeconds`" with "it is reclaimed by the resume-time cleanup once resume has consumed it, by a later attempt's supersede step, or by the §12.5 backstop ([§10.1](10_gateway-internals.md#101-horizontal-scaling))".

In §4.4.2, in the paragraph that begins "**Partial checkpoint manifest cleanup:**", replace "(a failed periodic checkpoint on a session that completes normally)" with "(an abort arm that [Section 4.4.3](#443-checkpoint-quiescence-timeout-and-abort-handling) **Checkpoint abort cleanup:** sweeps)".

In `spec/28_communication-channels.md` §28.5.1, in the `CH-CHECKPOINT` **Degradation.** bullet, replace "A deadline fire on a drain, preStop, or barrier path retains" with "A deadline fire retains"; the source sentence wraps after "preStop, or". In §28.8, in the `CH-CHECKPOINT` row, make the same replacement, and keep the row one physical line.

### SPEC-7 · spec/07_session-lifecycle.md § 7.2 Interactive Session Model; spec/06_warm-pod-model.md § 6.2 Pod State Machine

(a) In the §7.2 state enumeration, in the line that begins "resume_pending → cancelled              (pre-attach terminal collapse:", replace "client POST /v1/sessions/{id}/terminate, DELETE /v1/sessions/{id}, parent cancel cascade, or operator cancel arrives" with:

```text
DELETE /v1/sessions/{id}, parent cancel cascade, or operator cancel arrives
```

On the same line, replace "See [§15.1](15_external-api-surface.md#151-rest-api) `POST /terminate` preconditions and pre-attach note below." with:

```text
See the pre-attach note below.
```

(b) In the line that begins "resume_pending → completed              (pre-attach terminal collapse:", replace "— e.g., rehydrated budget/lease exhaustion evaluation on re-enqueue," with:

```text
— e.g., client POST /v1/sessions/{id}/terminate, rehydrated budget/lease exhaustion evaluation on re-enqueue,
```

(c) In the line that begins "resuming → completed                   (replayed event log shows", replace "(replayed event log shows the agent already emitted session_complete before the crash —" with:

```text
(replayed event log shows the agent already emitted session_complete before the crash, or client POST /v1/sessions/{id}/terminate mid-resume —
```

(d) In the paragraph that begins "The `resuming → cancelled` edge fires when the client issues `DELETE /v1/sessions/{id}`", immediately after the sentence that ends "…and the gateway recognises this during replay.", insert:

```markdown
`POST /v1/sessions/{id}/terminate` against a session mid-resume also takes the `resuming → completed` edge, under the same close sequence.
```

(e) Replace the first three sentences of the paragraph that begins "The `resume_pending → cancelled` edge is the terminal path for", from "The `resume_pending → cancelled` edge is the terminal path for `POST /v1/sessions/{id}/terminate`," through "…(`resume_pending` is a valid precondition state).", with:

```markdown
The `resume_pending → cancelled` edge is the terminal path for `DELETE /v1/sessions/{id}`, parent-cascade cancels, and administrative cancels that arrive during the `resume_pending` window. The `resume_pending → completed` edge is the terminal path for `POST /v1/sessions/{id}/terminate` and for a terminal outcome the gateway determines on its own, such as a parent's completion cascade resolving to graceful closure.
```

Keep the remaining sentence of that paragraph, from "The `resuming` failure table in" onward, unchanged.

(f) In §6.2, append to the bullet that begins "**Terminal session outcome during snapshot replay (`resuming → completed`):**", after its last sentence "…for the full sequence.":

```markdown
A client `POST /v1/sessions/{id}/terminate` arriving mid-resume takes the same edge and sequence.
```

## Spec files touched

- `spec/07_session-lifecycle.md` (SPEC-1, SPEC-2, SPEC-3, SPEC-4, SPEC-7)
- `spec/12_storage-architecture.md` (SPEC-1, SPEC-3, SPEC-5)
- `spec/10_gateway-internals.md` (SPEC-4, SPEC-6)
- `spec/16_observability.md` (SPEC-2)
- `spec/28_communication-channels.md` (SPEC-1, SPEC-6)
- `spec/29_communication-scenarios.md` (SPEC-1, SPEC-2, SPEC-4)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-6)
- `spec/04_system-components.md` (SPEC-6)
- `spec/06_warm-pod-model.md` (SPEC-7)
