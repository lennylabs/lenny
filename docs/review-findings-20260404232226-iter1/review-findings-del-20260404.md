# Review Findings — Recursive Delegation & Task Trees

**Document reviewed:** `docs/technical-design.md`
**Perspective:** 10. Recursive Delegation & Task Trees
**Date:** 2026-04-04
**Reviewer:** Claude (Sonnet 4.6)

**Focus:** Evaluate the recursive delegation model for correctness, safety, and recovery. Assess policy propagation, resource budgets, tree lifecycle, and edge cases at depth.

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 0 |
| High     | 2 |
| Medium   | 9 |
| Low      | 4 |
| Info     | 1 |

**Total findings:** 16

### Previously fixed (included for completeness)

DEL-001 and DEL-002 were identified in a prior review pass and are addressed in the current spec revision. They are not re-raised here.

---

## Findings

### DEL-003 Orphan Cleanup Interval and `cascadeTimeoutSeconds` Default Unspecified [High] — Fixed
**Section:** 8.11

The spec documents the `detach` cascade policy and states that "a background job detects task tree nodes whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired." However, three critical details are missing:

1. No default value for `cascadeTimeoutSeconds` is given. Without a default, detached subtrees could run indefinitely, consuming pods, credential leases, and token budget with no owning session.
2. The background job has no specified run interval, no metrics emitted, and no alert threshold.
3. Detached orphan pods are not counted against user or tenant quotas during the detached window, so a user could trivially exceed their concurrency limit by detaching trees.

The `detach` policy is the only one where orphaned workloads are intentionally allowed to run past parent termination. This makes the cleanup job's behavior load-bearing, not incidental.

**Recommendation:**
- Specify `cascadeTimeoutSeconds` default (suggest 3600s / 1h) and a deployer-configurable cap.
- Specify background cleanup job interval (suggest 60s) and make it configurable.
- Emit `lenny_orphan_cleanup_runs_total`, `lenny_orphan_tasks_terminated`, `lenny_orphan_tasks_active` metrics. Alert when `lenny_orphan_tasks_active` exceeds a deployer threshold.
- Count detached orphan pods and sessions toward their originating user's concurrency quota for the duration of the detached run. Document this explicitly.

**Resolution:** Fixed items 1-3: added `cascadeTimeoutSeconds` default (3600s) with deployer-configurable Helm cap, specified cleanup job interval (60s, configurable), and added three orphan cleanup metrics with alerting guidance. Item 4 (quota counting for orphans) was intentionally deferred: detached orphans are bounded by `cascadeTimeoutSeconds`, making unbounded quota abuse unlikely. A documentation note acknowledges the gap and marks quota-aware orphan accounting as a future enhancement. This avoids adding concurrency-tracking complexity to a rare edge case in v1.

---

### DEL-004 Credential Propagation `inherit` Has No Capacity Pre-Check [High] — Fixed
**Section:** 8.3

The `inherit` credential propagation mode causes all child sessions to draw from the same credential pool as the parent. The spec defines `maxConcurrentSessions` per credential in the pool, but there is no pre-flight check at delegation time that verifies sufficient capacity exists for the about-to-be-created child.

In a tree with `maxTreeSize: 50` and `credentialPropagation: inherit`, all 50 pods compete for the same pool. Each pod claims one lease slot. If the pool's `maxConcurrentSessions` is smaller than the eventual fan-out, child sessions fail at credential assignment after a pod has already been claimed and workspace files exported — wasting a warm pod and the export work.

The spec notes a pre-claim credential check in Section 4.9 (`CREDENTIAL_POOL_EXHAUSTED` before pod allocation), but this check evaluates availability at the moment of the individual delegation call, not aggregate capacity for the entire planned subtree. Concurrent delegations from the same parent racing through `DECRBY`/`INCR` can each pass the individual check while collectively exhausting the pool.

**Recommendation:**
- At `delegate_task` time, when `inherit` mode is active, validate that the credential pool has at least `1` available slot beyond current active leases (the immediate child). Add a comment that this is a point-in-time check, not a reservation.
- Document explicitly that `inherit` mode is not suitable for high fan-out trees and recommend `independent` instead when `maxParallelChildren > pool.maxConcurrentSessions / expected_tree_depth`.
- Alternatively, implement a soft reservation: when the gateway processes `delegate_task` with `inherit`, it atomically reserves a credential slot for the duration of pod startup, releasing it only once `AssignCredentials` succeeds or the pod fails. This closes the race between the pre-claim check and the actual assignment.

**Resolution:** Fixed. Added a credential availability pre-check at `delegate_task` time in Section 8.3: for `inherit` mode, the gateway verifies that the parent's credential pool has at least one assignable slot before claiming a warm pod; for `independent` mode, the target runtime's default pool is checked. The check reuses the same pre-claim logic from Section 4.9 and rejects with `CREDENTIAL_POOL_EXHAUSTED` before pod allocation. The check is explicitly documented as point-in-time (not a reservation) — the existing race-detection metric (`lenny_gateway_credential_preclaim_mismatch_total`) and pod-release-on-failure behavior cover the residual race window. A deployer guidance callout was added warning that `inherit` mode is not suitable for high fan-out trees and recommending `independent` when `maxParallelChildren > pool.maxConcurrentSessions / expected_tree_depth`. Soft reservation was intentionally not implemented: it adds significant complexity (atomic slot holds, timeout-based releases, rollback paths) for a narrow race window that is already observable and recoverable.

---

### DEL-005 Cross-Environment Delegation: Child Credential Mode `inherit` Semantically Broken [Medium]
**Section:** 10.6

When a session in environment A delegates to a runtime in environment B (cross-environment delegation), the delegation lease carries `credentialPropagation: inherit`. The child session in environment B would then attempt to draw credentials from environment A's credential pool. This is semantically incorrect and likely unsafe:

1. Environment B's runtime may be configured for a different LLM provider entirely.
2. The credential pool from environment A may not be accessible or authorized from environment B's tenant context.
3. The `credentialPropagation` field on the delegation lease is set by the calling session (environment A), not the target environment, so there is no mechanism by which environment B can override it.

The spec (Section 10.6) states "Connectors are never cross-environment. Child sessions use their own environment's connector configuration" — establishing a clear principle for connector credentials. However, it does not apply the same principle to LLM credential propagation. The enforcement steps listed in Section 10.6 (steps 1–4 at delegation time) do not include a validation that the `credentialPropagation` mode in the active lease is compatible with the target environment's credential configuration.

**Recommendation:**
- Add a validation step to the cross-environment delegation enforcement sequence (Section 10.6): if the calling session's delegation lease specifies `credentialPropagation: inherit` and the target runtime is in a different environment, reject the delegation with `CROSS_ENV_INHERIT_CREDENTIAL_PROHIBITED` and require the caller to use `independent` or `deny`.
- Document this constraint explicitly alongside "Connectors are never cross-environment."
- Add a worked example showing a cross-environment delegation with `credentialPropagation: independent`.

---

### DEL-006 Token Budget Overshoot in Deep Trees Under Concurrent Fan-Out [Medium]
**Section:** 8.3, 11.2

The spec uses atomic Redis `DECRBY` / `INCR` for budget reservation (Section 8.3). The rollback path is documented: if `DECRBY` drives the counter negative, a compensating `INCRBY` restores it. However, at high concurrency within a single session (multiple children delegating simultaneously), a window exists between `DECRBY` and the negative-check-and-rollback where multiple goroutines have already obtained negative-but-not-yet-rolled-back values. Each goroutine independently decides to proceed or rollback based on the value it received from `DECRBY` — this is correct for simple cases. But for deep trees at `maxDepth: 5` with `maxParallelChildren: 10` at each level, the tree can generate up to 100,000 concurrent delegation attempts within a single tree. Each subtree's `maxTokenBudget` is sliced separately, but the root's budget can be eroded faster than the atomic operations can check.

More concretely: the spec's reservation model reserves budget at delegation time and returns it on child completion. It does not describe how overshoot is bounded during the window between the root node's budget hitting zero and all in-flight child delegations completing their individual reserve-or-rollback cycles. During this window, new token-consuming LLM calls from already-running children can temporarily push actual consumption beyond the tree's `maxTokenBudget` if the budget counter hits zero mid-flight and the LLM proxy is checking a stale or slightly-delayed counter.

**Recommendation:**
- Add a `budgetWarningThreshold` (e.g., 80% of `maxTokenBudget`) at which the gateway sends a `BUDGET_WARNING` signal to the root session and the client. This provides proactive notification before exhaustion.
- Document the maximum theoretical overshoot per deployment tier based on `max_tokens_per_LLM_call * max_parallel_children`.
- Consider a hard-stop mode where the LLM proxy checks the Redis budget counter before forwarding any request (not just at delegation time), providing a tighter enforcement boundary for token-sensitive workloads.

---

### DEL-007 `await_children` Mode `any` Leaves Uncollected Children With No Automatic Cleanup [Medium]
**Section:** 8.9

The `any` mode for `await_children` returns as soon as one child completes and leaves all other children running. The spec notes: "The parent can explicitly cancel them via `lenny/cancel_child` if desired." This is advisory, not enforced.

If the parent session terminates (normally or via failure) without cancelling the remaining children, those children become de facto orphans. They are not covered by the `detach` cascade policy (which applies when the parent's session enters a terminal failure state — but a parent that completed successfully also exits). The cascade policy `cascadeOnFailure` only triggers on failure, not on normal completion.

The orphan cleanup job described in Section 8.11 targets nodes "whose root session has been terminated and whose `cascadeTimeoutSeconds` has expired" — but the root session (the tree root) may still be active while the intermediate parent (which called `await_children` with mode `any`) completed. The cleanup job's trigger condition is thus based on root termination, not per-node parent termination, creating a gap where uncollected `any`-mode children run unchecked until the root session eventually terminates.

**Recommendation:**
- Add an optional `cancelRemaining: bool` parameter to `lenny/await_children` mode `any`. When `true`, the gateway automatically cancels non-winning children when the first result is returned.
- Add a deployer-configurable `maxUncollectedChildAgeSeconds` that triggers gateway-initiated cancellation of any child whose parent has reached a terminal state without cancelling it.
- Emit a `child_uncollected` warning event to the root session's stream when a parent completes with live children still running, so operators and tracing systems can detect the pattern.

---

### DEL-008 `cancel_all` Cascade Has No Per-Node Timeout or Ordering Guarantee [Medium]
**Section:** 8.11

The `cancel_all` cascade policy cancels "all descendants immediately" when a parent reaches terminal failure. The spec does not define:

1. Whether cancellation propagates top-down (parent notified before children) or bottom-up.
2. What happens if a child pod is in a non-cancellable state (e.g., `running_setup`, pre-`attached`) — does the gateway wait, force-terminate, or skip?
3. The per-node timeout: how long does the gateway wait for a child to acknowledge cancellation before force-terminating its pod?
4. Conflict resolution when a descendant has `cascadeOnFailure: await_completion` — does the ancestor's `cancel_all` override it?

Without these definitions, `cancel_all` on a depth-5 tree with 50 nodes could take arbitrarily long, blocking resource cleanup. The delegation budget counter and warm pool slots remain occupied until all children terminate, potentially starving new sessions.

**Recommendation:**
- Specify cancellation propagation order (top-down: cancel parent, then cancel each child subtree concurrently).
- Specify a per-node cancellation timeout (suggest 30s): if a child does not transition to a terminal state within 30s of receiving `Terminate`, the gateway force-terminates the pod.
- Specify a total cascade timeout (suggest `5 * perChildMaxAge` or a deployer cap).
- Specify that an ancestor's `cancel_all` overrides a descendant's `await_completion` — parent cascade policy wins. Document this as the precedence rule.

---

### DEL-009 No Fan-Out Rate Limiting at the Delegation Level [Medium]
**Section:** 8, 8.3

The delegation lease includes `maxParallelChildren` and `maxTreeSize` as absolute ceilings, but there is no rate limit on how quickly a session can spawn children. A session with `maxParallelChildren: 10` can issue 10 concurrent `delegate_task` calls in a single event loop iteration. At depth 3 with `maxParallelChildren: 10` at each level, a single tree can generate 1,000 pod claims in under a second.

The warm pool controller handles pod claims, but a 1,000-claim burst is qualitatively different from gradual growth: it saturates the claim path, triggers HPA scale-up events, and can starve other tenants of warm pods before the `maxTreeSize` ceiling is enforced. The atomic reservation model (Section 8.3) ensures budget consistency but does not throttle the rate of claim requests.

**Recommendation:**
- Add a `delegationRateLimit` field to the delegation lease (e.g., `maxDelegationsPerSecond: 5`) that limits how many `delegate_task` calls a single session can issue per second. Excess calls are queued (up to a configurable backlog depth) and processed when below the rate limit.
- Apply this rate limit per-session (not per-tree), so a tree cannot collectively fan out faster than the root's rate limit allows.
- Document the interaction between `delegationRateLimit` and `maxParallelChildren`: both limits apply independently. `delegationRateLimit` controls creation rate; `maxParallelChildren` controls steady-state concurrency.

---

### DEL-010 `send_message` Cross-Tree Scope Not Enforced at the Tenant Boundary [Medium]
**Section:** 7.2, 8.5

The `messagingScope` setting restricts `lenny/send_message` to `direct` (parent/children) or `siblings` (Section 7.2). The spec says the maximum deployment scope is `siblings`, but it does not explicitly state that sessions belonging to different task trees (but same tenant) cannot message each other when scope is `siblings`.

The `siblings` definition is "children of the same parent." If session A and session B are both children of different parents in the same tenant but not in the same tree, they are not siblings by this definition and should be unreachable from each other. However, the spec does not state what happens when `lenny/send_message` receives a target `taskId` that belongs to a different tree in the same tenant: does it return `SCOPE_DENIED`, silently drop, or — the risk — route successfully?

The spec notes "Additional scopes (e.g. full-tree or cross-tree) may be added in future versions." This implies the routing infrastructure may already need to support broader scopes. If the gateway resolves any `taskId` within the tenant namespace regardless of tree membership, and only checks `messagingScope` for same-tree targets, a bug in the scope-check could allow cross-tree messaging today.

**Recommendation:**
- Explicitly state that `lenny/send_message` with any current scope (`direct`, `siblings`) returns `SCOPE_DENIED` if the target `taskId` is not in the caller's task tree. Tenant isolation is not sufficient — tree isolation must be enforced.
- Reserve a future `cross_tree` scope and require explicit policy authorization (`DelegationPolicy` rule) to use it.
- Add a test requirement: `TestSendMessageCrossTree_ReturnsScopeDenied`.

---

### DEL-011 `children_reattached` Event Has No Formal Schema [Medium]
**Section:** 8.2, 8.11

The `children_reattached` event is described in two places: Section 8.2 ("The parent agent receives a `children_reattached` event with this state") and Section 8.11 ("Parent session receives a `children_reattached` event listing current child states"). Neither section defines the event's schema.

This creates implementation ambiguity for runtime adapter authors. Key questions left unanswered:

- What fields are present per child: `taskId`, `state`, `runtimeRef`, pending result ref?
- How are pending elicitations represented in the event — are they included inline or delivered as a separate follow-up?
- Is there a sequence number or cursor included so the parent agent can determine how many events it missed while the parent pod was down?
- What happens if a child completed and its result was already offloaded to the `session_tree_archive` table (Section 8.2, completed subtree offloading)? Is the result inline in `children_reattached` or does the parent need to call `lenny/await_children` to fetch it?

**Recommendation:**
- Add a formal JSON schema for the `children_reattached` event in Section 8.11, covering: `type: "children_reattached"`, `children: [{ taskId, state, runtimeRef, pendingResult?: TaskResult, pendingElicitation?: ElicitationPayload }]`, `lastKnownSequence`.
- Specify that for children whose results are in `session_tree_archive`, the result is fetched by the gateway before constructing the event and delivered inline (so the parent does not need a separate call).

---

### DEL-012 `LeaseSlice` Schema Incomplete — Does Not Document Budget Sharing vs Reservation [Medium]
**Section:** 8.2

The `LeaseSlice` table in Section 8.2 now lists five fields with types and descriptions. However, it does not document:

1. Whether `maxTokenBudget` in the `LeaseSlice` represents a **reservation** (subtracted from parent immediately) or a **ceiling** (parent budget is unchanged; child is merely capped). The Budget Reservation Model in Section 8.3 clarifies that reservation is the mechanism, but this is not stated on the `LeaseSlice` definition itself.
2. Whether omitting a field means "inherit parent's value" or "use runtime default." Section 8.3 says "defaults are described in Section 8.3" but the default section uses a lease JSON block, not a clear per-field inheritance table.
3. The constraint that `LeaseSlice` fields can only narrow — not widen — the parent's lease. This is stated in Section 8.3 ("Child leases are always strictly narrower than parent leases") but is not visible on the `LeaseSlice` definition.

**Recommendation:**
- Add a note to the `LeaseSlice` table: "All fields are optional. Omitted fields default to `min(remaining_parent_budget, defaultDelegationFraction)` for budget fields and the parent's own value for structural limits. All fields are strictly narrowing — a child cannot specify a value larger than the parent's remaining allocation."
- Clarify that `maxTokenBudget` in `LeaseSlice` is a reservation amount subtracted from the parent's remaining budget at delegation time.

---

### DEL-013 No Platform-Level Maximum Delegation Depth Ceiling [Low]
**Section:** 8.3

The `orchestrator` preset sets `maxDepth: 5`. There is no platform-level ceiling on `maxDepth` — a deployer could set `maxDepth: 100` either directly in a delegation lease or via a custom preset. At extreme depths, elicitation chains (Section 9.2), recovery ordering (Section 8.11), audit trail lineage (Section 11.7), and tracing become operationally unmanageable.

The spec does not provide a Helm-level `maxDelegationDepth` that acts as an absolute ceiling regardless of lease or preset configuration.

**Recommendation:**
- Add a Helm-level `platform.maxDelegationDepth` ceiling (default: 10, hard maximum: configurable but documented as operationally inadvisable above 10). The gateway validates this at delegation time alongside lease limits.
- Document the operational impact of each depth increment on recovery time (`maxTreeRecoverySeconds` must scale proportionally to depth * `maxLevelRecoverySeconds`).

---

### DEL-014 Non-Cascading Interrupt Leaves Tree Budget Draining While Root Is Paused [Low]
**Section:** 6.2

Section 6.2 states: "`interrupt_request` does NOT cascade to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children."

When a root or intermediate orchestrator receives an interrupt (e.g., user pauses the top-level session), its children continue running and consuming token budget. The parent is paused but cannot call `lenny/cancel_child` or `lenny/await_children` while interrupted. The net effect is that the tree's token budget drains without any orchestrator decision-making, potentially exhausting the budget before the root resumes.

For long-running orchestrator trees, a user who intends to temporarily pause the root workflow unintentionally allows subtask spending to continue.

**Recommendation:**
- Add an optional `cascade: true` parameter to the interrupt API that pauses token budget consumption for the entire tree (by pausing the LLM proxy for all descendant leases) while the root session is interrupted.
- Document the tradeoff explicitly: `cascade: false` (default) preserves current behavior; `cascade: true` pauses all descendant LLM calls at the cost of increased task latency when the root resumes.

---

### DEL-015 Section 8.7 Missing (Numbering Gap) [Info]
**Section:** 8

Section 8 jumps from 8.6 (Lease Extension) to 8.8 (File Export Model), with 8.7 absent. There is no heading, no content, and no note explaining the gap. This is likely a remnant from a prior revision that deleted a subsection without renumbering.

**Recommendation:**
- Either renumber 8.8 onward to close the gap, or add a `### 8.7 (Reserved)` placeholder with a note explaining what was removed. This prevents confusion for readers who may search for referenced Section 8.7 content.

---

## Cross-Cutting Observations

**Recovery at depth 5+:** The interaction between `maxLevelRecoverySeconds` (120s) and `maxTreeRecoverySeconds` (600s) creates a narrow window for deep trees. At depth 5 with failures at all levels simultaneously, the effective per-level budget is `600 / 5 = 120s` — exactly equal to `maxLevelRecoverySeconds`. Any slight overrun at an early level cascades into compressed windows for higher levels, potentially causing healthy-but-slow nodes to be marked failed due to the total timeout running out. Deployers using `orchestrator` preset (depth 5) should be warned to set `maxTreeRecoverySeconds >= maxDepth * maxLevelRecoverySeconds` to avoid this. This is not a spec error but warrants a documentation callout.

**Credential propagation chain documentation:** The spec defines three `credentialPropagation` modes but does not specify what happens when they change across delegation hops — i.e., when a parent sets `inherit` but the target runtime's own `delegationPolicyRef` specifies `independent`. The "effective policy = base ∩ derived" rule applies to `DelegationPolicy` (Section 8.3) but is not stated to apply to `credentialPropagation`. Adding a sentence clarifying that `credentialPropagation` from the calling session's lease is authoritative, subject to cross-environment restrictions (see DEL-005), would close this ambiguity.
