# Review Findings — 24. Policy Engine & Admission Control

**Document reviewed:** `docs/technical-design.md`
**Perspective:** Policy Engine & Admission Control
**Category code:** POL
**Date:** 2026-04-04
**Reviewer:** Claude (Sonnet 4.6)

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 1 |
| High | 4 |
| Medium | 5 |
| Low | 4 |
| Info | 2 |

---

## Findings

---

### POL-001 Quota Fail-Open Window Allows Full Tenant Budget Overshoot With No Cross-Replica Coordination [Critical]

**Section:** 11.2, 12.4

The spec describes a per-replica ceiling formula for the Redis fail-open window: `per_replica_limit = tenant_limit / replica_count`, sourced from the gateway's peer discovery mechanism (Section 10.1). The Maximum Overshoot Formula in Section 11.2 then states: `fail_open_overshoot = (tenant_limit / replica_count) × replica_count = tenant_limit` — confirming that during a Redis outage all replicas together can overshoot by exactly one full tenant budget before fail-closed triggers.

This arithmetic is internally consistent but the implementation requirement it imposes is not addressed: the formula is only correct if `replica_count` used by each replica matches the **live** replica count at the moment Redis fails. If a replica uses a stale count (cached from a prior peer-discovery sweep), or if replicas join or leave during the fail-open window, the product `(tenant_limit / stale_count) × actual_count` can exceed the tenant budget by an unbounded amount. The spec neither specifies how frequently the peer count is refreshed, what happens when peer discovery itself uses Redis (making it unavailable at the exact moment it is needed), nor what the fallback value is when the count cannot be determined.

Additionally, the `quotaFailOpenCumulativeMaxSeconds` circuit-breaker (default 300s over a 1-hour rolling window) is the only mechanism preventing repeated drift accumulation. The spec does not define when the 1-hour window resets (calendar-hour boundary or rolling), nor whether the sliding window counter itself is stored in Redis (unavailable during the outage it is meant to bound) or in-memory (lost on replica restart).

**Recommendation:**
1. Specify that `replica_count` is read from an in-memory Kubernetes-native peer list (e.g., Endpoints object for the gateway Service, polled via the API server) rather than from Redis, with a maximum staleness bound (e.g., 30s).
2. Define a safe fallback value for `replica_count` when peer discovery is unavailable (recommended: `1`, which makes the per-replica ceiling equal to the full tenant limit — conservative but safe).
3. Store the cumulative fail-open timer in a small in-memory structure with Postgres persistence on each increment; document its behavior on replica restart (reset to zero or re-read from Postgres).
4. Clarify whether the 1-hour rolling window is calendar-aligned or a true sliding window, and which store backs it.

---

### POL-002 Budget Rollback on Partial Delegation Failure Is Not Atomic — TOCTOU Between Token and Tree-Size Operations [High]

**Section:** 8.3 (Budget Reservation Model)

Section 8.3 step 4 ("Concurrency safety") describes two separate Redis atomic operations for a delegation attempt: `DECRBY` for token budget and `INCR` for tree size. If the tree-size `INCR` succeeds but the subsequent token `DECRBY` fails (budget exhausted), the spec says the gateway "rolls back all preceding atomic operations from that delegation attempt (token reservation and tree-size increment)." This rollback is itself two separate operations (`INCR`/`DECRBY` compensation), not a single atomic one.

Between the successful `INCR` on tree size and the compensating `DECR` rollback, concurrent delegation requests from sibling sessions see an inflated tree-size counter and may be incorrectly rejected with `BUDGET_EXHAUSTED` for tree-size reasons even though the reservation ultimately did not succeed. The reverse is also true: if the compensating `DECR` for tree size fails (e.g., Redis blip between the two operations), the tree-size counter is permanently inflated until session completion or manual reconciliation. Neither race is explicitly acknowledged or bounded.

**Recommendation:**
Use a single Lua script (evaluated atomically by Redis) to perform both the token `DECRBY` and tree-size `INCR` checks within one operation. The Lua script should check both values against their limits before committing either change, and return a structured result indicating which limit (if any) was exceeded. This makes the two-counter reservation a single atomic unit, eliminating the TOCTOU window and removing the need for compensating rollback.

---

### POL-003 `PostAgentOutput` and `PreToolResult` Interceptor Phases — Content Payload Contract Unspecified [High]

**Section:** 4.8

Seven interceptor phases are defined: `PreAuth`, `PostAuth`, `PreRoute`, `PreDelegation`, `PostRoute`, `PreToolResult`, `PostAgentOutput`. The spec specifies the content payload for exactly one phase: `PreDelegation` carries `TaskSpec.input`. For all six other phases the `content` field of `InterceptRequest` (the `bytes content = 4` field) is described only as "phase-dependent payload" with no definition of what is included.

This omission is particularly significant for `PostAgentOutput` and `PreToolResult`, which are content-level hooks. Without knowing what `content` contains for these phases, deployers cannot implement useful interceptors: a guardrail that should inspect agent output before delivery to the client cannot be built, and a tool-call filter cannot be reasoned about. The `MODIFY` action implies that `modified_content` replaces the original — but without knowing the payload schema, the semantics of a `MODIFY` response are undefined (e.g., can a `PostAgentOutput` interceptor truncate output? Remove sensitive fields? Rewrite tool arguments in `PreToolResult`?).

**Recommendation:**
Add a payload specification table for each of the seven phases, describing the `content` field type (e.g., serialized `OutputPart[]`, `ToolCallRequest`, raw prompt text) and what `MODIFY` is permitted to change. Define whether `MODIFY` on `PreAuth` or `PostAuth` is legal (these are unusual cases). At minimum, document `PostAgentOutput` (likely `OutputPart[]`) and `PreToolResult` (likely `ToolCallRequest` and its result) as these are the highest-value hooks for content safety.

---

### POL-004 `fail-open` Interceptors Can Be Ordered Before Security-Critical Interceptors — No Priority Floor Enforced [High]

**Section:** 4.8

Built-in interceptors `AuthEvaluator` (priority 100) and `QuotaEvaluator` (priority 200) run first by default. However, the priority field for external interceptors defaults to 500 and has no documented floor. The spec states only that built-in interceptors with equal priority run before external ones; it does not forbid external interceptors from being registered at priorities below 100.

An external interceptor registered at priority 50 with `failPolicy: fail-open` would run **before** `AuthEvaluator`. If it returns `REJECT` (valid behavior), authentication never runs and the rejection reason logged is from an unauthenticated context — an audit gap. If it times out and fails open, the chain continues without that interceptor's check, which is expected. But if such an interceptor is registered at priority 50 with `MODIFY`, it could alter the request payload before authentication evaluates it, potentially smuggling content that `AuthEvaluator` would otherwise have rejected. The spec does not address whether `MODIFY` responses from phases that precede `AuthEvaluator` are legal or what authentication state is available to interceptors that fire before auth completes.

**Recommendation:**
Define a minimum priority floor for external interceptors (e.g., external interceptors may not register at priorities ≤ 100, reserving the 1–100 range for built-in security-critical interceptors). Document what session/auth context is available in `InterceptRequest.metadata` at each phase. Consider making priorities below the `AuthEvaluator` threshold an admin-only capability requiring explicit opt-in.

---

### POL-005 Lease Extension "Success" Semantics Under Budget Ceiling Misrepresent Actual Grants — Silent Underfunding [High]

**Section:** 8.6

The `elicitation` approval mode states (step 3, sub-point): "All requests tied to a single elicitation + cool-off period return success, even if their grant was capped or reduced to zero because the ceiling was reached." A `success` response when the actual grant is **zero tokens** means the requesting session receives no additional budget but is told its extension was approved. That session will immediately hit a budget-exhausted error on the next LLM call — the error that the extension was supposed to prevent.

This design creates a class of "ghost approvals": sessions that believe they have been authorized to continue but will fail immediately. Depending on how the adapter handles the situation, this could result in infinite extension request loops (each request succeeds with zero grant, each LLM call fails, adapter requests extension again), consuming extension quota without making progress. The spec notes the adapter triggers extension on `RATE_LIMITED` from the LLM proxy, but does not address what happens when the grant is zero.

**Recommendation:**
Distinguish between "your request was processed" (the elicitation ran) and "you received budget" (the actual grant). Return the granted amount in the extension response. If the grant is zero (ceiling already reached), return a specific status code (e.g., `CEILING_REACHED`) so the adapter can fail the session cleanly rather than looping. Define the adapter behavior when `additionalTokenBudget = 0` in the extension response: it should propagate a `BUDGET_EXHAUSTED` error to the runtime, not retry the extension.

---

### POL-006 Token Budget Reconciliation Uses `max(Postgres, pod-reported)` — Pod Can Over-Report Consumption to Exhaust Sibling Budgets [Medium]

**Section:** 11.2 (Crash Recovery)

The crash recovery formula takes `max(postgres_checkpoint, pod_reported_cumulative)` for each session's token usage. The pod-reported value comes from the runtime adapter's `ReportUsage` RPC on reconnection to a new gateway replica.

The pod is untrusted (Section 4.7, "Adapter-Agent Security Boundary" establishes the agent binary as untrusted, and the adapter itself is a third-party binary). A misbehaving or compromised adapter could reconnect to a new gateway replica (e.g., after a gateway crash) and report an inflated `cumulative` usage. The `max()` formula would then accept this inflated value, permanently reducing the token budget available to the session tree — effectively a denial-of-service against the parent session's remaining budget. In a delegation tree, a rogue child adapter could deplete the parent's entire `maxTokenBudget` by reporting fabricated usage at recovery time.

**Recommendation:**
Cap the pod-reported cumulative usage at the session's allocated `maxTokenBudget` (the child's lease slice). Values exceeding the allocation should be rejected with an alert/audit event. This does not prevent honest over-count from being applied (if a session legitimately consumed more than its allocation, it is already over-budget and should be terminated), but it bounds the blast radius of a rogue report. Additionally, note that `ReportUsage` RPC payloads should be subjected to the same untrusted-input validation that the adapter applies to all agent-originated data.

---

### POL-007 Admission and Fairness Table Is Incomplete — Per-Team Concurrency Limit Has No Enforcement Specification [Medium]

**Section:** 11.1

The admission table lists "Concurrency limits (active sessions): Global, per-user, per-team, per-runtime" as a supported granularity. However, the spec never defines what constitutes a "team," where team membership is stored, how it is conveyed in the authentication context (e.g., as an OIDC claim), or how the gateway resolves team identity from the `user_id`/`tenant_id` already in the session context.

The `QuotaStore.increment_token_usage(user_id, window, tokens_used)` example in Section 12.6 is scoped by `user_id`, and the quota hierarchy described in Section 11.2 operates on `global → tenant → user`. There is no mention of a team layer anywhere in the quota enforcement path, making the per-team concurrency limit listed in 11.1 unimplementable as specified.

**Recommendation:**
Either define the team concept (data model, membership lookup, claim mapping) and specify how it slots into the quota hierarchy (e.g., `global → tenant → team → user`), or remove "per-team" from the admission table and note it as a post-v1 item. If kept, specify the `QuotaStore` interface method signature for team-scoped operations.

---

### POL-008 `DelegationPolicy` Tag Evaluation Is Dynamic, But Staleness Window for Cache-Based Evaluation Is Undefined [Medium]

**Section:** 8.3

Section 8.3 states: "Dynamic tag evaluation at delegation time. Tags can change without redeploying — policy re-evaluated on each delegation." This implies that tag-based `DelegationPolicy` rules are re-evaluated on every `delegate_task` call. However, the `DelegationPolicyEvaluator` backs onto the `RuntimeRegistry` (Section 4.8), and there is no specification of whether the evaluator reads tags directly from Postgres on each call or uses a cached view.

If the `RuntimeRegistry` is cached (which is typical for performance — a live Postgres read on every delegation hop in a deep tree would be prohibitively expensive), a tag change between evaluation and pod assignment could allow or deny delegations that should have been handled differently. In security terms, a deployer who revokes a tag from a runtime to block delegations to it may see in-flight delegations bypass the revocation for the duration of the cache TTL.

**Recommendation:**
Specify the caching model for the `RuntimeRegistry` as used by `DelegationPolicyEvaluator`: cache TTL (or cache invalidation mechanism), whether tag changes propagate synchronously to all replicas (pub/sub invalidation) or are eventually consistent, and what the maximum staleness bound is. For security-sensitive tag changes (e.g., revoking `team: platform`), deployers should be aware of the propagation delay.

---

### POL-009 Timeout Table Missing: External Interceptor Chain Total Deadline, Elicitation Chain Per-Hop vs. End-to-End Timeouts, and Messaging DLQ TTL [Medium]

**Section:** 11.3

The timeout table in Section 11.3 covers request, upload, setup command, session age, idle, resume window, elicitation wait, and elicitations-per-session limits. Several timeout values referenced elsewhere in the spec are absent:

1. **External interceptor chain total deadline.** Each interceptor has a per-interceptor timeout (default 500ms, Section 4.8), but there is no cap on total chain execution time. With 10 external interceptors each allowed 500ms, the chain could delay a request by up to 5 seconds before any interceptor fires. No total-chain timeout is defined.

2. **Elicitation per-hop forwarding timeout (30s) vs. the global `maxElicitationWait` (600s).** Section 9.2 specifies a per-hop forwarding timeout of 30s. Section 11.3 has `Max elicitation wait: 600s`. The relationship between these two values (are they additive? does per-hop timeout count against the global limit?) is not stated. In a deep delegation tree, 30s per hop × N hops could consume the entire 600s budget before the user even sees the prompt.

3. **Dead-letter queue message TTL.** Section 7.2 specifies that DLQ messages for recovering sessions expire after `maxResumeWindowSeconds` (default 900s). This value is not in the timeout table and has operational significance for deployers tuning message delivery guarantees.

**Recommendation:**
Add a "Total interceptor chain deadline" row (recommended: 2s maximum, regardless of per-interceptor timeout and chain length) with a note that the chain short-circuits if the deadline is reached before all interceptors complete. Add the elicitation per-hop timeout (30s) and clarify its relationship with the global `maxElicitationWait`. Add the DLQ message TTL (`maxResumeWindowSeconds` by default, with a note that it is configurable per session).

---

### POL-010 `contentPolicy` Inheritance — "Stricter" Is Defined for `maxInputSize` but Not for `interceptorRef` [Medium]

**Section:** 8.3

The spec states: "`contentPolicy` is inherited by child leases and can only be made stricter (smaller `maxInputSize`, same or more restrictive `interceptorRef`)." The rule for `maxInputSize` is clear (numeric comparison). The rule for `interceptorRef` is not: what does "more restrictive" mean for a reference to a `RequestInterceptor`? Two named interceptors have no inherent ordering — the spec provides no mechanism to determine whether interceptor B is "more restrictive" than interceptor A.

This ambiguity means the system cannot programmatically enforce the "can only be made stricter" rule for `interceptorRef`. A child lease could specify a completely different interceptor (or `null`, removing content scanning entirely) without any enforcement mechanism blocking it. The practical risk is that a child delegation lease operator could remove the content safety interceptor that the parent lease requires.

**Recommendation:**
Change the rule for `interceptorRef` to: "Children must use the same `interceptorRef` as the parent, or an additional interceptor in a chain. Children may not remove or replace the parent's `interceptorRef`." Implement this as a check at delegation time: if the parent's effective policy has a non-null `interceptorRef`, the child's policy must also reference it (it may add more, but not remove). If null inheritance ("no interceptor") is explicitly desired, require the parent to set a special `interceptorRef: allow-null-in-children` flag.

---

### POL-011 `ExperimentRouter` Interceptor at Priority 300 Can Be Blocked by `fail-closed` External Interceptors at Priorities 201–299 [Low]

**Section:** 4.8, 10.7

The `ExperimentRouter` built-in interceptor runs at priority 300. An external interceptor registered at priority 250 with `failPolicy: fail-closed` that times out will short-circuit the chain before `ExperimentRouter` runs. This means experiment assignment silently fails for sessions where the external interceptor times out, and those sessions fall to the default runtime rather than the variant — silently corrupting experiment data with no observable signal other than an unexpected dip in variant traffic.

The spec makes no mention of this interaction, and the `ExperimentRouter` is listed as a built-in interceptor without any protection against pre-emption by external interceptors.

**Recommendation:**
Document the interaction explicitly. Consider giving all built-in interceptors a protected execution path that runs regardless of external interceptor rejections — or run built-in interceptors in a fixed inner chain that precedes the external chain entirely. At minimum, add an alert `ExperimentAssignmentDropped` that fires when `ExperimentRouter` is not reached on a request that would have been eligible for experiment assignment.

---

### POL-012 Rate Limit Fail-Open Timer per Replica vs. Cumulative Timer — Two Separate Mechanisms with Conflicting Documentation [Low]

**Section:** 12.4

Section 12.4 describes two related but distinct timers:

1. **Per-outage fail-open window:** `rateLimitFailOpenMaxSeconds` (default 60s) — after this duration, rate limiting fails **closed** until Redis recovers.
2. **Cumulative fail-open timer:** `quotaFailOpenCumulativeMaxSeconds` (default 300s over 1-hour window) — triggers fail-closed for **quota enforcement** (not rate limiting) when cumulative fail-open time exceeds threshold.

These two timers govern different things (rate limits vs. quota counters) but are described in close proximity, making the failure mode matrix difficult to reason about. Specifically: after the per-outage 60s window expires and rate limiting fails closed, does quota enforcement continue in fail-open mode (bounded by the 300s cumulative timer)? The spec implies yes (they are independent mechanisms), but the operational consequence is that a long Redis outage (>60s) will simultaneously block new requests via rate limit rejection AND allow existing sessions to consume quota in fail-open mode — a combination that is not explicitly acknowledged.

**Recommendation:**
Add a unified failure mode table showing the combined state of rate limiting, quota enforcement, and session creation for each time window during a Redis outage: [0, 60s] (rate limit fail-open, quota fail-open), [60s+] (rate limit fail-closed, quota still fail-open up to 300s cumulative), [300s+ cumulative] (both fail-closed). Clarify whether the 60s rate-limit timer and 300s cumulative quota timer are independent or whether one resets/affects the other.

---

### POL-013 No Admission Control for `lenny/send_message` Against Sessions in Different Tenant Trees [Low]

**Section:** 7.2, 11.1

The `messagingScope` setting controls which sessions can message each other (`direct` or `siblings`), but this scope is enforced based on the task tree topology (parent/child relationships). There is no explicit specification that `lenny/send_message` validates that the target `taskId` belongs to the same tenant as the calling session.

The admission table (Section 11.1) does not list inter-session messaging as a controlled resource at any granularity. Given that `taskId` values are UUIDs (potentially guessable via enumeration), a session in one tenant could attempt to message a session in another tenant by guessing or leaking a `taskId`. The spec's RLS-based tenant isolation in Postgres would prevent data leakage from SessionStore reads, but the gateway's routing layer would need to validate tenant ownership of the target session before delivering the message.

**Recommendation:**
Explicitly state that `lenny/send_message` and `lenny/send_to_child` validate that the target `task_id` belongs to the same tenant as the calling session (enforced at the gateway layer before any SessionStore lookup). Add this to the admission control table as "Inter-session message delivery: per-tenant." Add a test case to the integration test suite verifying that cross-tenant message delivery is rejected.

---

### POL-014 `RetryPolicyEvaluator` Is Listed as a Policy Module but Has No Enforcement Specification [Info]

**Section:** 4.8, 7.3

The `RetryPolicyEvaluator` is listed as one of the five policy engine modules in Section 4.8. Section 7.3 describes retry eligibility rules in detail (failure classification, `maxRetries`, window bounds, deployer caps). However, there is no description of what the `RetryPolicyEvaluator` evaluates at request time vs. what is evaluated asynchronously by the recovery path in Section 7.3. It is unclear whether `RetryPolicyEvaluator` runs on the `RequestInterceptor` chain (blocking the request path) or on the session recovery path (asynchronous, after failure detection).

**Recommendation:**
Clarify the execution context of `RetryPolicyEvaluator`: does it run synchronously on the `RequestInterceptor` chain (and if so, at which phase), or is it invoked asynchronously by the session recovery subsystem? If the former, specify which phases it is active in. If the latter, clarify why it appears in the policy engine evaluator table rather than the recovery subsystem description in Section 7.3.

---

### POL-015 `AdmissionController` Circuit Breaker State Changes Are Not Audited [Info]

**Section:** 4.8, 11.6, 11.7

Section 11.6 describes circuit breaker state declarations ("Runtime X degraded," "Delegation depth > N disabled during incident"), and Section 11.7 specifies that policy decisions are audit-logged. However, the audit logging specification lists session/task/delegation events but does not explicitly include circuit breaker state changes as auditable events.

A circuit breaker that disables an entire runtime or delegation depth produces significant operational impact and should have an audit trail (who triggered it, when, from which gateway replica). Without this, a mistakenly or maliciously triggered circuit breaker has no forensic trail.

**Recommendation:**
Add `circuit_breaker.state_changed` to the audit event types, capturing: component (runtime name, pool name, or policy dimension), new state (`degraded`, `offline`, `disabled`), actor (gateway replica ID or admin API caller identity), and timestamp. Verify that the 11.7 audit log schema accommodates this event type.

---
