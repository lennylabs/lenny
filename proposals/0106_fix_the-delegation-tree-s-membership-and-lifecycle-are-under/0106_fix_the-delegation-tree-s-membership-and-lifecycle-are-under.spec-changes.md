# Spec changes — Delegation tree membership and lifecycle are under-specified and mis-implemented

## Design (as the spec must state it)

- §8.10 tree recovery writes only nodes still in `resume_pending`, fails each level's unrecovered `resume_pending` nodes before continuing upward, and leaves an elapsed resume window to the §6.2 **`resume_pending` wall-clock cap:** (SPEC-1). §7.2 carries the one edge that write uses (SPEC-2).
- §8.10 **Orphan cleanup:** defines the active orphan set, which orphan cleanup ends and which `maxOrphanTasksPerTenant` and the orphan gauges count, and states each selected session's disposition (SPEC-3 Edit (a)). The §7.2 any-state clause names both terminal states orphan cleanup writes (SPEC-3 Edit (b)).
- Derive and replay membership needs no specification text. §12.6 StoreRouter, §15.1 Session Replay Semantics, §12.8 step 16, and §4.5 Workspace lineage already state it.

## Edge cases and accepted failure modes

- A session whose direct parent is live but whose higher ancestor no longer exists, and that has no terminal ancestor, is selected and counted with no expiry instant.
- A detach parent that fell back to `cancel_all` under `maxOrphanTasksPerTenant` stores `detach`. A child its interrupted cascade missed is expired after `cascadeTimeoutSeconds` rather than cancelled at once.
- A derived or replayed session is never selected by orphan cleanup, because `lenny/delegate_task` did not create it.

## Staged edits

### SPEC-1 · spec/08_recursive-delegation.md § 8.10 Delegation Tree Recovery

Locate every edit by quoted text.

**Edit (a).** Make two changes. In **Recovery ordering:**, delete the last sentence, which begins "This ensures that by the time a parent resumes", and keep the bottom-up rule before it.

Replace the paragraph that begins "If `maxLevelRecoverySeconds` is exceeded for a given depth" (directly after the recovery-budget table)

```markdown
If `maxLevelRecoverySeconds` is exceeded for a given depth, unrecovered nodes at that level are marked as terminally failed and the gateway continues upward. If `maxTreeRecoverySeconds` is exceeded, all remaining unrecovered nodes are marked as terminally failed and cascade policies apply from that point.
```

with

```markdown
If `maxLevelRecoverySeconds` is exceeded for a given depth, the gateway marks each unrecovered node at that level that is still in `resume_pending` as terminally `failed` before it continues upward. If `maxTreeRecoverySeconds` is exceeded, the gateway marks every remaining unrecovered node that is still in `resume_pending` as terminally `failed`, and cascade policies apply from that point. Tree recovery does not write a node that has left `resume_pending`. The `resuming` watchdog ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) bounds a node in `resuming`, and a node whose restore committed is recovered.
```

**Edit (b).** In **Interaction with `maxResumeWindowSeconds`:**, make two replacements and keep the sentence beginning "The effective recovery window for any node is therefore".

Replace

```markdown
If a node's `maxResumeWindowSeconds` expires before tree recovery reaches it, that node transitions to `expired` and its cascade policy is applied.
```

with

```markdown
If a node's `maxResumeWindowSeconds` elapses before tree recovery recovers it, the node transitions to `awaiting_client_action` under the [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) **`resume_pending` wall-clock cap:**, and tree recovery counts it as unrecovered without writing it.
```

Replace

```markdown
Conversely, `maxTreeRecoverySeconds` can terminate a node's recovery attempt even if its `maxResumeWindowSeconds` has not yet elapsed.
```

with

```markdown
Conversely, `maxTreeRecoverySeconds` can fail a node that is still in `resume_pending` even if its `maxResumeWindowSeconds` has not yet elapsed.
```

**Edit (c).** In **Parent pod failure with active children:** step 5, replace

```markdown
expiry (`maxResumeWindowSeconds` or `maxTreeRecoverySeconds` elapsed → `expired`)
```

with

```markdown
expiry (`expired`)
```

### SPEC-2 · spec/07_session-lifecycle.md § 7.2 Interactive Session Model

In the **Session state machine:** code block, insert the following line directly after the line beginning "resume_pending → awaiting_client_action (maxResumeWindowSeconds elapsed". Apply it after 0105 SPEC-2, which edits the same block. The line stays one physical line, and its `(` aligns with the neighbouring `resume_pending` lines.

```text
resume_pending → failed                 (tree recovery: maxLevelRecoverySeconds or maxTreeRecoverySeconds elapsed while the node was still in resume_pending — see [§8.10](08_recursive-delegation.md#810-delegation-tree-recovery))
```

### SPEC-3 · spec/08_recursive-delegation.md § 8.10 Delegation Tree Recovery; spec/07_session-lifecycle.md § 7.2 Interactive Session Model

**Edit (a) · §8.10 **Orphan cleanup:**.** Replace

```markdown
A background job runs every 60 seconds (configurable via Helm `delegation.orphanCleanupIntervalSeconds`) and detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired.
```

with

```markdown
A background job runs every 60 seconds (configurable via Helm `delegation.orphanCleanupIntervalSeconds`). It selects every non-terminal session that `lenny/delegate_task` created and that has an ancestor session that is terminal or no longer exists. These sessions are the tenant's active orphans, which `maxOrphanTasksPerTenant`, `lenny_orphan_tasks_active`, and `lenny_orphan_tasks_active_per_tenant` count. The job cancels a selected session whose parent is terminal with `cascadeOnFailure` policy `cancel_all` on the run that selects it, which completes a cascade that a store error or a gateway replica failure interrupted. It expires a selected session whose parent no longer exists on the run that selects it. It expires a selected session whose parent is terminal with any other policy once `cascadeTimeoutSeconds` has elapsed since the earliest terminal transition among its ancestors. It does not end a selected session whose parent is live, and it evaluates that session after its parent on the same run, so one `cascadeTimeoutSeconds` window bounds every session under a terminated session.
```

Keep the following sentence, "Orphaned children are terminated and their artifacts follow standard retention policy.", unchanged.

**Edit (b) · §7.2 **Terminal writes from any non-terminal state.**.** This paragraph lands with 0105 SPEC-2 Edit (c); apply after it. Replace

```markdown
Orphan cleanup ([§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)) moves an orphaned delegated session to `expired`.
```

with

```markdown
Orphan cleanup ([§8.10](08_recursive-delegation.md#810-delegation-tree-recovery)) moves an orphaned delegated session to `cancelled` or `expired`.
```

## Spec files touched

- spec/07_session-lifecycle.md
- spec/08_recursive-delegation.md
