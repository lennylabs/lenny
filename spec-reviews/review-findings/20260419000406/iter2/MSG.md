### MSG-004 Path 5 Delivery Receipt Status Not Explicitly Stated [MEDIUM]

**Files:** `07_session-lifecycle.md` (§7.2 line 277)

**Description:** Path 5 is the canonical "buffered-in-inbox" path and is referenced by paths 2 and 4 as the fall-through target (with explicit `queued` status). However, path 5's own bullet does not state the resulting delivery-receipt status:

> 5. **No matching pending request, runtime busy (...)** → buffered in the session inbox (see inbox definition above); delivered in FIFO order when the runtime next enters `ready_for_input`.

Every other path (1, 2, 3, 4, 6) ends its bullet with an explicit `Delivery receipt status: <value>` sentence. Path 5 is the only path that omits this — even though it is textually the "base case" that paths 2 and 4 reference when falling through. A reader scanning the seven paths top-down will find the status missing; the only indirect signal is the generic summary at line 299 ("`queued` (buffered in inbox or DLQ)").

This is a cross-path consistency gap introduced by the stylistic convention set by the other six bullets. It does not create functional ambiguity (the summary at line 299 and the fall-through clauses in paths 2 and 4 make the status inferable), but it does break the uniform pattern and invites an implementer to mis-infer, e.g., `delivered` if they treat the ACK-on-dequeue model as eventually delivered, or `error` if they stop reading before line 299.

**Recommendation:** Append to line 277: "Delivery receipt status: `queued`." — matching the style used in paths 1, 3, and 6.

---

### MSG-005 Path 7 / Recovering-State DLQ Row Omits Explicit Receipt Status Value [LOW]

**Files:** `07_session-lifecycle.md` (§7.2 dead-letter table line 295)

**Description:** The Recovering (`resume_pending`, `awaiting_client_action`) row of the dead-letter handling table describes enqueue-to-DLQ behaviour:

> Message is enqueued in a **dead-letter queue** (DLQ) stored in Redis (...). (...) If the target resumes before TTL expiry, queued messages are delivered in FIFO order. On TTL expiry, undelivered messages are discarded and the sender receives a `message_expired` notification via the `delivery_receipt` mechanism (...).

Two adjacent rows in the same table state the receipt status explicitly:
- Pre-running row (line 293): "Delivery receipt status: `queued`." (on the inter-session branch).
- Overflow clause within the Recovering row itself specifies `message_dropped` receipt with `reason: "dlq_overflow"` for the overflow case (post MSG-001 fix).

But the Recovering row never states the **success-path** receipt status for the initial DLQ enqueue, only its TTL-expiry and overflow outcomes. The word "queued" appears ("queued messages are delivered in FIFO order") but refers colloquially to items in the queue, not to the `status: queued` receipt value defined at line 1213 of §15.4.1. A reader working path-by-path down the seven paths into the dead-letter table finds the status implicit only.

**Recommendation:** Add to the Recovering row: "On successful DLQ enqueue, delivery receipt status: `queued` (queueDepth populated)." — symmetric with the Pre-running row's explicit statement and consistent with the cross-row style.

---

### MSG-006 Terminal-Target Row Uses Synchronous Error Rather Than Delivery Receipt — Contract Mixed [LOW]

**Files:** `07_session-lifecycle.md` (§7.2 line 294), `15_external-api-surface.md` (line 1220 `delivery_receipt` status enum, line 1208 "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object"), `08_recursive-delegation.md` (line 442)

**Description:** The Terminal row (line 294) specifies: "Gateway returns an error to the sender immediately: `{ "code": "TARGET_TERMINAL", ... }`. The message is not enqueued." §8.5 line 442 reinforces: "Returns error for terminal targets; queues with TTL for recovering targets."

However, §15.4.1 line 1208 states: "**Every `lenny/send_message` call returns a synchronous `delivery_receipt` object.**" And the `status` enum (line 1213) includes `error` ("delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` when Redis is unreachable for durable inbox, or `reason: "scope_denied"` when messaging scope denies the target").

There is a contract inconsistency here. The Terminal case is arguably a rejection (not a delivery outcome), so a tool-call error is defensible — but §15.4.1 declares `delivery_receipt` is returned on **every** call, without exception. Two plausible implementations will diverge:
(a) An MCP server returning a tool-call error `TARGET_TERMINAL` (no receipt).
(b) An MCP server returning a normal response with `delivery_receipt{status: "error", reason: "target_terminal"}`.

Compare with `SCOPE_DENIED` (line 564 of §15.1), which is explicitly carved out as: "Returned as the `error` reason in a `delivery_receipt` event." `TARGET_TERMINAL` gets no analogous carve-out — so a careful reader cannot decide whether it is receipt-wrapped or not.

**Recommendation:** Reconcile §15.4.1's "Every call returns a receipt" claim with the §7.2 Terminal-row "returns an error" phrasing. Two acceptable fixes: (1) Extend §15.4.1 to list `TARGET_TERMINAL` (and `TARGET_NOT_READY`) as pre-receipt rejections (i.e., the `delivery_receipt` guarantee applies only to messages that reach the routing pipeline), or (2) Map `TARGET_TERMINAL` to `delivery_receipt{status: "error", reason: "target_terminal"}` symmetrically with `SCOPE_DENIED`. Either way, update both §7.2 line 294 and the `delivery_receipt` status narrative at line 1220 for symmetric treatment.

---

## Summary

**Real Issues Found:** 3

Prior findings MSG-001, MSG-002, MSG-003 all verified fixed:
- MSG-001: `reason: "dlq_overflow"` present at line 295 of §7.2 and at line 1220 of §15.4.1.
- MSG-002: Path 4 timeout-fall-through-to-path-5 is now identical wording across §7.2 line 276 and §15.4.1 line 1202 (the `delivery: immediate` row).
- MSG-003: §7.2 line 269 precedence clarification is tight; §8.5 `await_children` description (line 848) still consistent with path 3 precedence because a parent blocked in `await_children` alone is not in `input_required` state.

New findings in iter2 are cross-section receipt-status consistency gaps of decreasing severity:
1. **MSG-004 (MEDIUM):** Path 5 is the only one of seven paths missing an explicit receipt status bullet — breaks symmetry with paths 2 and 4 that reference "path 5 behavior with receipt status `queued`".
2. **MSG-005 (LOW):** Recovering-state DLQ row omits explicit success-path receipt status, asymmetric with the Pre-running row and with the overflow sub-clause's explicit `message_dropped`.
3. **MSG-006 (LOW):** Terminal-state error surfacing is unreconciled with §15.4.1's "every call returns a delivery_receipt" claim; `SCOPE_DENIED` has a receipt carve-out but `TARGET_TERMINAL` and `TARGET_NOT_READY` do not.

Other areas checked: agent-teams pattern (§7.2 sibling coordination), SSE back-pressure (`OutboundChannel` bounded-error policy, §15 lines 122–155), routing–delegation interaction (path 3 vs. `await_children`, §8.5 multi-child `input_required`), concurrent-workspace mode per-slot routing (§7.2 line 285), coordinator-failover inbox semantics (`inbox_cleared` event, `durableInbox` mode). All unambiguous and complete.
