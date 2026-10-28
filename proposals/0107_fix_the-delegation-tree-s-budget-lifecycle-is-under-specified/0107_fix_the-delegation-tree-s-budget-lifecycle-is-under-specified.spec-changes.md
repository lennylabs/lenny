# Spec changes — Delegation tree budget lifecycle is under-specified

## Design (as the spec must state it)

- The delegation budget return is a step of a delegated child's §7.1 terminal pipeline (SPEC-1, SPEC-2).
- §8.3 names the first reservation's witness and the scripts' creation and TTL rules, makes the `BUDGET_KEYS_EXPIRED` cleanup idempotent per root, and lets only a reservation detect expiry (SPEC-3, SPEC-8).
- §8.6 states per-request resolution from the `SessionStore`, the `delegation_tree_budget` row as the record of grants, replica-local consent state with its bound, reclamation, and the terminal-session rule, and rewrites the coordinator-handoff wording for any-replica service (SPEC-4). §8.6 step 2 and §9.2 item 6 state the driver-end dismissal and redrive (SPEC-5, SPEC-7).
- §12.8 user erasure and tenant deletion name the `delegation_tree_budget` row (SPEC-6).

## Edge cases and accepted failure modes

- A tree whose keys are gone is detected only at its next reservation (SPEC-3 Edit (b), Edit (c)).
- A reservation detection leaves the state of a root that has no `BUDGET_KEYS_EXPIRED` transition to `failed` unchanged (SPEC-3 Edit (d)).
- Two replicas can each present an elicitation for one tree (SPEC-4 Edit (c)).
- A queued request whose own session is terminal receives no grant when its driver ends (SPEC-5).

## Staged edits

Every edit locates its target by heading and quoted text.

### SPEC-1 · spec/07_session-lifecycle.md § 7.1 Normal Flow

In the paragraph **Terminal pipeline context:**, replace

```markdown
the seal-and-export step (**Seal-and-export invariant:** above), the inbox drain, the terminal events and signals, the pod or slot release, and the close of the session log.
```

with

```markdown
the seal-and-export step (**Seal-and-export invariant:** above), the inbox drain, the terminal events and signals, the delegation budget return of a delegated child ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)), the pod or slot release, and the close of the session log.
```

### SPEC-2 · spec/08_recursive-delegation.md § 8.3 Delegation Policy and Lease

Append to numbered item 3 **Return on completion:**, after the sentence ending "returns only restore up to the original allocation.", in the same paragraph:

```markdown
The return is a step of the child's terminal pipeline ([Section 7.1](07_session-lifecycle.md#71-normal-flow) **Terminal pipeline context:**), so it runs once for the child's terminal transition, after whichever writer committed it. A relabel of an already-terminal child runs no second return. The return addresses the budget keys of the child's recorded `root_session_id`. It precedes the pod or slot release, and the usage quiescence timeout of **Usage quiescence before budget return** below is counted from the child's terminal transition. The gateway refuses to start, and pool admission refuses a pool, when a pool's `maxWorkspaceSealDurationSeconds` is not greater than `delegation.usageQuiescenceTimeoutSeconds`, so the quiescence wait ends before the release allowance of the terminal pipeline budget begins.
```

### SPEC-3 · spec/08_recursive-delegation.md § 8.3 Delegation Policy and Lease

**Edit (a).** In the paragraph **Defense-in-depth TTL.**, replace

```markdown
`budget_reserve.lua` SHOULD set a TTL on all delegation budget keys equal to `delegation.budgetKeyTTLSeconds` (default: 691200s / 8 days, configurable via Helm) at tree creation time. The gateway passes this value as an ARGV parameter on the first invocation for a given root session (i.e., when the root tree-wide keys are created).
```

with

```markdown
`budget_reserve.lua` sets a TTL equal to `delegation.budgetKeyTTLSeconds` (default: 691200s / 8 days, configurable via Helm) on every delegation budget key it creates, including a per-parent key that a later reservation creates. The gateway passes this value as an ARGV parameter on every invocation of `budget_reserve.lua`. `budget_return.lua` keeps the remaining TTL of every key it writes.
```

In the same paragraph, replace "(see tenant deletion Phase 4 and `DeleteByUser` step 15)" with "(see tenant deletion Phase 4 and `DeleteByUser` step 16)".

**Edit (b).** In the paragraph **`BUDGET_KEYS_EXPIRED` detection and tree cleanup.**, replace the sentence

```markdown
If the sentinel key does not exist (i.e., `EXISTS {root_session_id}:dlg:tokens` returns 0), the script MUST return a `BUDGET_KEYS_EXPIRED` error status without modifying any keys.
```

with

```markdown
If the sentinel key does not exist (i.e., `EXISTS {root_session_id}:dlg:tokens` returns 0), the script MUST return a `BUDGET_KEYS_EXPIRED` error status without modifying any keys, except on a `budget_reserve.lua` run that the gateway marks as the tree's first reservation. When `budget_reserve.lua` returns `BUDGET_KEYS_EXPIRED`, the gateway reads the root's sessions from the `SessionStore`. If it holds no delegated session under the root, the reservation is the tree's first: the gateway reruns the script marked as the first reservation, and the script creates each tree-wide key it does not find, including the sentinel when the reservation carries no token slice. Otherwise the gateway runs the script once more, and only a second `BUDGET_KEYS_EXPIRED` is a reservation detection. On a reservation detection, `delegate_task` fails with the permanent `INVALID_STATE_TRANSITION` ([Section 15.1](15_external-api-surface.md#151-rest-api)) and makes no reservation. `budget_return.lua` creates no key: it skips a counter key that does not exist. When the gateway cannot read the `SessionStore` or the `delegation_tree_budget` row ([Section 8.6](#86-lease-extension)) for a reservation, `delegate_task` fails closed with the retryable `DELEGATION_BUDGET_UNAVAILABLE` and makes no reservation.
```

In the line that follows the paragraph and introduces the cleanup list, replace "When the gateway receives `BUDGET_KEYS_EXPIRED` from either Lua script, it initiates an immediate tree cleanup:" with "On a reservation detection (above), the gateway initiates an immediate tree cleanup:".

**Edit (c).** Replace cleanup item 2

```markdown
   2. **Skip budget returns** for cancelled children. Since the budget keys are gone, `budget_return.lua` would also return `BUDGET_KEYS_EXPIRED`. The gateway marks these children as `cancelled` without attempting budget return.
```

with

```markdown
   2. **Budget returns modify no key** for cancelled children. Each child the cleanup cancels runs its terminal pipeline, and its budget return (step 3 **Return on completion:**) finds the sentinel absent, so `budget_return.lua` returns `BUDGET_KEYS_EXPIRED` without modifying any key. A `BUDGET_KEYS_EXPIRED` from `budget_return.lua` is not a detection, so the gateway starts no cleanup for it.
```

In cleanup item 1, replace "the entire delegation tree — same mechanism as root session cancellation ([§8.10](#810-delegation-tree-recovery) cascade cancellation)" with "every non-terminal descendant of the root, whatever its `cascadeOnFailure` policy".

In cleanup item 4, replace "recording `root_session_id`, `tenant_id`, and the operation that triggered detection (`reserve` or `return`)." with "recording `root_session_id` and `tenant_id`."

**Edit (d).** Replace the paragraph that begins "The `BUDGET_KEYS_EXPIRED` detection can occur in any state where delegation operations execute:" with a paragraph indented to the list's level:

```markdown
   Only the writer whose transition of the root to `failed` applies emits `delegation.budget_keys_expired`, so repeated detections fail the root once and emit the event once. A detection whose root is in a state from which the session state machine in [§7.2](07_session-lifecycle.md#72-interactive-session-model) has no `BUDGET_KEYS_EXPIRED` transition to `failed`, a terminal state included, performs neither step 3 nor step 4.
```

### SPEC-4 · spec/08_recursive-delegation.md § 8.6 Lease Extension

**Edit (a).** Under **Lease Extension Approval Modes**, in the paragraph **`elicitation` mode (default):**, replace

```markdown
Requests are serialized per task tree. The gateway presents at most one elicitation to the user at a time, with concurrent request batching and a cool-off window after approval.
```

with

```markdown
Each gateway replica serializes the requests it serves per task tree. A replica presents at most one elicitation for a tree to the user at a time, with concurrent request batching and a cool-off window after approval. **Extension state:** below states what each replica holds.
```

**Edit (b).** In step 4 **User rejects:**, sub-bullet **In-flight requests (atomic check):**, replace

```markdown
**inside the same Postgres transaction** that increments the tree budget counters. The read of the flag and the write of the counter increment are serialized under the row lock
```

with

```markdown
**inside the same Postgres transaction** that adds the grant to the row's cumulative extension grants. The read of the flag and the write of the grant are serialized under the row lock
```

and in the same sub-bullet replace "the gateway rolls back the budget increment and returns" with "the gateway rolls back the grant and returns".

**Edit (c).** Replace the items of the **Scope:** list, from "Extensions apply to the requesting session only" through "Only new children spawned after the extension benefit from the expanded parent budget", with:

```markdown
- A grant raises the session limits of the requesting session only. Its token amount also raises the reservation token ceiling of every parent in the tree, which is the parent's `maxTokenBudget` plus the `delegation_tree_budget` row's cumulative token grant (**Extension state:** below).
- Existing children's leases remain as originally granted.

**Extension state:** On each extension request, the gateway reads from the `SessionStore`, under the tenant the request carries, the requesting session's `root_session_id` and granted lease, the root's granted lease, and the parent's granted lease, so any replica serves a request for any session without prior registration. The `delegation_tree_budget` row's cumulative extension grants are the authoritative record of what extensions have raised, and they form one pool for the tree: the effective max bounds the root's granted lease plus the row's cumulative grant on each dimension, and the gateway applies that bound inside the transaction that adds the grant (**In-flight requests (atomic check):** above), so grants served by different replicas never exceed it together. A grant writes none of the [Section 8.3](#83-delegation-policy-and-lease) Redis counters. As an exception to the durable-session-state invariant of [Section 4.1](04_system-components.md#41-edge-gateway-replicas), the open elicitation, the requests batched onto it, and the success cool-off window are held in memory by the replica that serves the requests, so a request served by another replica opens its own elicitation. The row's denial and cool-off bind every replica, and losing this in-memory state never produces a grant without the approval the mode requires. A replica discards a tree's consent state once no elicitation is pending and the success cool-off has elapsed. The gateway applies no grant to, and presents no elicitation for, a terminal session.
```

**Edit (d).** In the sub-bullet **Durability:**, delete the sentence

```markdown
On coordinator handoff, the new gateway replica reads these fields from Postgres before accepting any new extension requests for the tree.
```

In the same sub-bullet, replace "as part of the same periodic checkpoint transaction that records token and delegation budget counters (see [Section 11.1](11_policy-and-controls.md#111-admission-and-fairness))" with "before the gateway returns `REJECTED`, and a failed write fails the request with no grant instead of returning `REJECTED`".

In the sub-bullet **Query semantics (handoff-safe):**, replace "stale across a coordinator handoff" with "stale across replicas", and replace

```markdown
The newly elected replica therefore observes the denial on the very first extension request it processes, without needing to replay in-flight state from the predecessor.
```

with

```markdown
Any replica therefore observes the denial on the first extension request it processes, without needing to replay in-flight state from the predecessor.
```

In the sub-bullet **Clock reference (UTC only):**, replace "regardless of which replica is elected" with "regardless of which replica serves the request".

### SPEC-5 · spec/08_recursive-delegation.md § 8.6 Lease Extension

Under **Elicitation mode flow:**, append to item 2 (the item that begins "2. **Concurrent requests** arriving while the elicitation is pending are queued silently."), in the same paragraph:

```markdown
When the session whose request opened a pending elicitation reaches a terminal state before the user decides, the gateway dismisses that elicitation with the reason `LEASE_EXTENSION_REQUESTER_TERMINAL` and applies no outcome of it to the queued requests ([Section 9.2](09_mcp-integration.md#92-elicitation-chain) **Expiry when the session leaves its pod:** does not apply to a lease-extension elicitation). Each queued request from a session that is still non-terminal re-enters step 1, so the first to re-enter opens a new elicitation and the others are queued on it. A queued request from a terminal session receives no grant.
```

### SPEC-6 · spec/12_storage-architecture.md § 12.8 Compliance Interfaces

**Edit (a).** In the `DeleteByUser` sequence, replace the lead of step 16

```markdown
16. Delegation budget keys (Redis) — enumerate
```

with

```markdown
16. Delegation budget keys (Redis) and the tree's `delegation_tree_budget` row (Postgres) — enumerate
```

In the same step, after the sentence ending "using slot-local `SCAN` (all keys share the same `{root_session_id}` hash tag, so a single-slot scan covers them).", insert:

```markdown
For each `root_session_id`, also delete the root's `delegation_tree_budget` row under the tenant's RLS context.
```

**Edit (b).** In the tenant deletion phase table, Phase 4 actions, replace

```markdown
then purge `{root_session_id}:dlg:*` per root via `RedisShard(tenantID, RedisConcernDelegation)` — must precede `SessionStore` deletion)
```

with

```markdown
then purge `{root_session_id}:dlg:*` per root via `RedisShard(tenantID, RedisConcernDelegation)`, and delete the tenant's `delegation_tree_budget` rows — must precede `SessionStore` deletion)
```

The table row stays one physical line.

### SPEC-7 · spec/09_mcp-integration.md § 9.2 Elicitation Chain

In item 6 **Expiry when the session leaves its pod:**, replace

```markdown
A lease-extension elicitation ([Section 8.6](08_recursive-delegation.md#86-lease-extension)) is not raised through `lenny/request_elicitation` and does not expire.
```

with

```markdown
A lease-extension elicitation ([Section 8.6](08_recursive-delegation.md#86-lease-extension)) is not raised through `lenny/request_elicitation` and this expiry does not apply to it. The gateway dismisses it when its requesting session is terminal, as [Section 8.6](08_recursive-delegation.md#86-lease-extension) **Elicitation mode flow:** states.
```

### SPEC-8 · spec/16_observability.md § 16.1 Metrics and § 16.5 Alerting Rules and SLOs

**Edit (a).** In the §16.1 row for `lenny_delegation_budget_keys_expired_total`, replace "increments when `budget_reserve.lua` or `budget_return.lua` returns `BUDGET_KEYS_EXPIRED`" with "increments on each `BUDGET_KEYS_EXPIRED` detection".

**Edit (b).** In the §16.5 row for `DelegationBudgetKeysExpired`, replace "incremented — a Lua script (`budget_reserve.lua` or `budget_return.lua`) returned `BUDGET_KEYS_EXPIRED`, indicating" with "incremented — a `BUDGET_KEYS_EXPIRED` detection, indicating", and delete "The gateway initiates tree cleanup (cascade cancel + root → failed). ".

Each table row stays one physical line.

### SPEC-9 · spec/19_resolved-decisions.md § 19 Resolved Decisions

In table row 13 (Lease extension), replace

```markdown
serialized per tree, one elicitation at a time, concurrent requests batched
```

with

```markdown
serialized per tree on each gateway replica, concurrent requests batched
```

The table row stays one physical line.

## Spec files touched

- spec/07_session-lifecycle.md (SPEC-1)
- spec/08_recursive-delegation.md (SPEC-2, SPEC-3, SPEC-4, SPEC-5)
- spec/09_mcp-integration.md (SPEC-7)
- spec/12_storage-architecture.md (SPEC-6)
- spec/16_observability.md (SPEC-8)
- spec/19_resolved-decisions.md (SPEC-9)
