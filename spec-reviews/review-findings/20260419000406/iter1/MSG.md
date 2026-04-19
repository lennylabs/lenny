### MSG-001 DLQ Overflow `message_dropped` Reason Code Unspecified [HIGH]

**Files:** `07_session-lifecycle.md` (line 295), `15_external-api-surface.md` (lines 1202, 1208)

**Description:** Section 7.2 defines two message overflow scenarios with inconsistent reason code specification:

1. **Inbox overflow (line 236):** "sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`" — explicitly specifies reason code.

2. **DLQ overflow (line 295):** "on overflow, the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt" — **does not specify reason code**.

The delivery_receipt schema at 15_external-api-surface.md (line 1202) states: `reason: "<string — populated when status is dropped, expired, or rate_limited>"`. This implies reason MUST be populated for dropped status, but DLQ overflow behavior leaves it ambiguous whether to use `"dlq_overflow"`, `"inbox_overflow"`, or something else. 

**Cross-section inconsistency:** Inbox and DLQ both use the same `message_dropped` status, but only inbox specifies the accompanying reason. Implementation will either: (a) guess at a reason value, risking client-side mismatch; (b) leave reason null/empty, violating the schema contract; or (c) use the same `"inbox_overflow"` for both, conflating two distinct overflow scenarios.

**Recommendation:** Explicitly specify the reason code for DLQ overflow, e.g., change line 295 to: "...the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`." Alternatively, clarify in the delivery_receipt schema whether reason is conditionally required vs. always populated for dropped status.

---

### MSG-002 Message Delivery Path 4 (`delivery: "immediate"`) Timeout Behavior Underspecified [MEDIUM]

**Files:** `07_session-lifecycle.md` (line 276), `15_external-api-surface.md` (line 1190)

**Description:** Path 4 documents interrupt and delivery for `delivery: "immediate"` on running sessions with in-flight tool calls, but the two specifications diverge on timeout handling detail:

**Section 7.2 (line 276):** "If the runtime does not consume the message within the delivery timeout (default: 30 seconds), the message falls through to inbox buffering (path 5 behavior) with receipt status `queued`. Otherwise receipt status: `delivered`."

**Section 15.4.1 (line 1190):** "...writes the message to stdin as soon as the runtime emits `interrupt_acknowledged` (Full-level) or immediately after the in-flight stdin write completes (Basic/Standard-level)... For all other `running` sub-states, receipt: `delivered`."

**Inconsistency:** Section 15's wording ("receipt: `delivered`") appears to skip the timeout check, implying the receipt is delivered after the write completes, not after consumption confirmation. The 30-second delivery timeout and fallthrough to path 5 are missing from the 15.4.1 description. 

**Edge case uncovered:** Section 7.2 applies the timeout only after the interrupt is acknowledged or the write completes. But if the write succeeds and the interrupt is acknowledged, does the 30-second timeout clock start immediately, or does it start counting only after the stdin write attempt? The precedence is underspecified.

**Recommendation:** Synchronize 15.4.1 with 7.2 by adding: "If the runtime does not confirm message consumption within 30 seconds, the message falls through to inbox buffering with receipt status `queued`." Clarify that the timeout starts after the interrupt is acknowledged (Full-level) or after the write completes (Basic/Standard-level).

---

### MSG-003 Path 3 (`input_required`) Does Not Inherit Precedence Under Concurrent `await_children` in Some Scenarios [MEDIUM]

**Files:** `07_session-lifecycle.md` (lines 269, 275), `08_recursive-delegation.md` (line 809)

**Description:** Section 7.2 (line 269) states: "When a runtime has multiple concurrent blocking tool calls (e.g., `lenny/await_children` and `lenny/request_input` in flight simultaneously via parallel tool execution), the session-level `input_required` state (path 3) takes precedence over the runtime-level `await_children` condition (path 5)."

However, Section 8.5 (line 809) describes `lenny/await_children` unblock behavior: "When a child enters `input_required` state, the parent's `lenny/await_children` call yields a partial result carrying the child's question and `requestId`."

**Potential edge case:** If a parent is blocked in `lenny/await_children` on its own child (child is in `input_required`), and another sibling sends a message to the parent during this time:
- The parent session itself is NOT in `input_required` state — it is in `running` state, blocked inside an `await_children` tool call.
- The parent's `input_required` state only occurs if the parent itself calls `lenny/request_input`.

**Implication:** A message sent to the parent under `siblings` scope while the parent is awaiting children is correctly routed to path 5 (inbox), not path 3 — this is correct behavior. But the spec wording at line 269 ("multiple concurrent blocking tool calls") could be misread to suggest that being blocked in `await_children` alone (without `request_input` also in flight) triggers path 3 precedence. The spec is technically correct but ambiguous about whether `await_children` alone qualifies as "multiple concurrent blocking tool calls" requiring path 3 precedence. 

**Recommendation:** Clarify line 269 to explicitly state: "...when a **single runtime** has multiple concurrent blocking tool calls (specifically, when `lenny/request_input` is in flight concurrently with other tool calls like `await_children`)..." to eliminate the misreading that merely being blocked in `await_children` alone triggers the precedence rule.

---

## Summary

**Real Issues Found:** 3

All issues are **cross-section inconsistencies** affecting message delivery semantics:

1. **MSG-001 (HIGH):** DLQ overflow reason code not specified — creates ambiguity in delivery_receipt schema compliance and implementation guidance.
2. **MSG-002 (MEDIUM):** Path 4 timeout behavior underspecified between sections 7.2 and 15.4.1 — risks inconsistent client and gateway implementations of delivery timeout semantics.
3. **MSG-003 (MEDIUM):** Path 3 precedence wording ambiguous under `await_children`-only blocking — unlikely to cause implementation errors but undermines spec clarity.

No gaps identified (only real errors reported). Message delivery paths are complete and unambiguous in the core model (three paths: inReplyTo, immediate, queued). SSE buffer overflow, DLQ TTL expiry, and coordinator failover semantics are well-specified. Multi-turn integration with session lifecycle is sound.
