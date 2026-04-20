# Iter3 MSG Review

## Regression check on iter2 fixes

- **MSG-004** (path 5 explicit `queued` status): **Verified fixed.** §7.2 line 284 now ends with "Delivery receipt status: `queued`."
- **FLR-003** (`lenny_inbox_drain_failure_total` metric): **Verified fixed.** §16.1 line 24 defines the metric with `pool` + `session_state` labels and §16.5 line 412 registers the `InboxDrainFailure` warning alert against it.

## Outstanding iter2 findings (not addressed)

- **MSG-005** (Recovering-row success-path receipt status): still implicit — §7.2 line 302 does not state the success-path receipt value for a Redis DLQ enqueue.
- **MSG-006** (TARGET_TERMINAL mixed contract): still open. §7.2 line 301 has the Terminal row returning a synchronous tool-call error while §15.4.1 line 1404 still states "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object" — these two contracts are not reconciled.

Both carry forward as-is; no new severity change recommended.

## New findings (iter3)

### MSG-007 Durable-inbox Redis-command set contradicted between §7.2 and §12.4 [HIGH]

**Files:** `spec/07_session-lifecycle.md` §7.2 lines 253–258, `spec/12_storage-architecture.md` §12.4 line 186

**Description:** Two spec sections describe the same Redis-list durable-inbox key `t:{tenant_id}:session:{session_id}:inbox` but disagree on the operation set:

- §7.2 line 253: "Backing store: Redis list (`RPUSH` / `LRANGE` / `LPOP`)" — with the row at line 254 adding "checks `LLEN` before `RPUSH`. Overflow drops the oldest entry (`LPOP` + drop)", line 255 "using `LRANGE` + `LTRIM` every 30 seconds", and line 257 "`LREM key 1 <serialised_message>`".
- §12.4 line 186: "Created when `messaging.durableInbox: true`; enqueue/dequeue/recovery via `LPUSH`/`LREM`/`LRANGE`".

Two distinct defects arise:

1. **Enqueue direction contradiction.** §7.2 enqueues with `RPUSH` (append to tail) and overflow-drops from head with `LPOP` → FIFO where the head is the oldest message. §12.4 says enqueue with `LPUSH` (prepend to head), which would make the head the newest message and break the `LRANGE ... 0 -1 returns FIFO order` recovery guarantee at §7.2 line 258. The crash-recovery guarantee and the overflow-drop-oldest behaviour both rely on the §7.2 convention; the §12.4 `LPUSH` wording is incorrect.
2. **Per-message TTL trim command omitted from §12.4.** §7.2 line 255 names `LTRIM` explicitly for expired-message trimming; §12.4 does not list `LTRIM` at all. A reader building the Redis-command allowlist for the gateway's Redis client wrapper (or for a Redis ACL) from §12.4 alone will miss `LTRIM` and the background trimmer will fail silently.

**Recommendation:** Make §12.4 line 186 the authoritative one-line summary that exactly mirrors §7.2. Change to: "Created when `messaging.durableInbox: true`; enqueue via `RPUSH`, dequeue/ack via `LREM`, recovery via `LRANGE`, overflow drop via `LPOP`, TTL trim via `LTRIM`; see [§7.2](07_session-lifecycle.md#72-interactive-session-model)".

---

### MSG-008 `message_expired` reason codes diverge across three co-located drain paths [MEDIUM]

**Files:** `spec/07_session-lifecycle.md` §7.2 lines 302, 304, 308, 384

**Description:** The `message_expired` event is emitted to sender event streams with three different `reason` string values for three closely related scenarios, with no glossary tying them together:

| Scenario | Reason | Spec site |
| --- | --- | --- |
| DLQ TTL elapsed while target is in a recovering state | `target_ttl_exceeded` | §7.2 line 308 ("For queued messages that later expire…") |
| In-memory / durable inbox drained when target transitions to a terminal state | `target_terminated` | §7.2 line 304 ("Inbox drain on terminal transition") |
| DLQ drained when a `resume_pending` / `awaiting_client_action` session transitions to terminal | `session_terminal` | §7.2 line 384 (`awaiting_client_action` semantics bullet) |

The three scenarios are not orthogonal:
- A message sitting in the DLQ at the moment a `resume_pending` session transitions to `cancelled` is within the intersection of rows 2 and 3 — the spec does not say which reason wins.
- From the sender's observable perspective, all three are "I sent a message to a session that didn't receive it before it ended / expired" — yet senders have to match three distinct enum values to trigger a unified handler.
- §15 does not publish a canonical enum for the `reason` field of `message_expired` events (only the `delivery_receipt.reason` enum is cataloged at §15.4.1 line 1416).

This is a cross-section consistency gap that will surface as either (a) senders handling only a subset of the three values and missing expiry notifications, or (b) implementers accidentally emitting a fourth synonym.

**Recommendation:** Define a canonical `message_expired.reason` enum in §15 (alongside the `delivery_receipt` schema at §15.4.1 line 1416) with exactly three values — e.g., `"dlq_ttl_expired"`, `"target_terminated"`, `"dlq_drained_on_terminal"` — and update the three §7.2 sites to reference the same enum. Alternatively, collapse rows 1 and 3 to a single `"target_terminated"` emission (since both end in "the sender waited for a target that no longer exists") and reserve `"dlq_ttl_expired"` only for the pre-terminal TTL-elapsed case.

---

### MSG-009 Metrics referenced by inbox normative prose are not declared in §16.1 [MEDIUM]

**Files:** `spec/07_session-lifecycle.md` §7.2 lines 257, 260; `spec/16_observability.md` §16.1 metrics table

**Description:** §7.2 defines normative behaviour that references two named Prometheus counters:

1. Line 257 (durable-inbox duplicate-delivery section): "emitting a `lenny_inbox_duplicate_suppressed_total` counter."
2. Line 260 (durable-inbox prerequisites): "The gateway emits the `lenny_inbox_redis_unavailable_total` counter."

Neither metric appears in the §16.1 metrics table. Only `lenny_inbox_drain_failure_total` is declared (added in iter2 to address FLR-003). Iter2 review (OBS-007/008/009) specifically enforced the rule that every metric mentioned in normative prose must be declared in §16.1 with its label set, type, and purpose — the two inbox metrics above break that rule.

Concrete consequences:
- Dashboard and alert authors have no canonical source for label schema (e.g., is `lenny_inbox_duplicate_suppressed_total` labeled by `pool` and `session_id` — which would violate §16.1.1 high-cardinality rules — or only by `pool` and `tenant_id`?).
- A PR author enforcing the §16.1 completeness rule at CI time will fail these two names.
- The `lenny_inbox_redis_unavailable_total` counter in particular is load-bearing for the durable-mode operational story (it is the only signal of the "Redis became unreachable mid-enqueue" failure mode) — leaving it undeclared leaves the SLO/alerting story incomplete.

**Recommendation:** Add two rows to the §16.1 metrics table:
- `lenny_inbox_duplicate_suppressed_total` (counter, labels `pool` + `runtime_class`, increments when the adapter suppresses a duplicate inbox redelivery against its `delivered_message_ids` set; no `session_id` label per §16.1.1).
- `lenny_inbox_redis_unavailable_total` (counter, labels `pool` + `tenant_id`, increments on every durable-inbox enqueue that fails because Redis is unreachable; drives an optional `DurableInboxRedisUnavailable` warning alert in §16.5).

Add a `DurableInboxRedisUnavailable` warning alert to §16.5 paired with the new counter, or document explicitly why no alert is required (i.e., operators are expected to rely on a Redis-wide availability alert instead).

---

### MSG-010 Pre-receipt rejections enumerated only in §15 error catalog, not reconciled in §15.4.1 "every call returns a receipt" clause [LOW]

**Files:** `spec/15_external-api-surface.md` §15.4.1 line 1404; error catalog rows at lines 753, 755, 791, 824, 825

**Description:** Building on open finding MSG-006 (TARGET_TERMINAL mismatch), a broader pattern exists: the "every `lenny/send_message` call returns a synchronous `delivery_receipt` object" statement at §15.4.1 line 1404 is contradicted by at least five error codes in the §15.1 catalog that are returned as synchronous tool-call errors with no receipt:

- `TARGET_TERMINAL` (line 753) — open per MSG-006.
- `TARGET_NOT_READY` (line 824) — external client pre-running target, returned as a 409 error per §7.2 line 300.
- `CROSS_TENANT_MESSAGE_DENIED` (line 825) — 403 rejection.
- `DUPLICATE_MESSAGE_ID` (line 791) — 400 rejection for idempotency-key collisions.
- `INVALID_DELIVERY_VALUE` (line 816) — 400 rejection for unknown `delivery` enum values.

Only `SCOPE_DENIED` (line 755) carries an explicit "Returned as the `error` reason in a `delivery_receipt` event" carve-out. The other five do not, which means the §15.4.1 "every call" guarantee is already false for at least five documented codes. This is the same contract-boundary defect flagged in MSG-006, but broader.

**Recommendation:** Replace the §15.4.1 line 1404 sentence with: "Every `lenny/send_message` call that reaches the routing pipeline returns a synchronous `delivery_receipt` object. Pre-pipeline rejections (validation failures such as `DUPLICATE_MESSAGE_ID`, `INVALID_DELIVERY_VALUE`; policy rejections such as `CROSS_TENANT_MESSAGE_DENIED`, `TARGET_NOT_READY`, `TARGET_TERMINAL`) are returned as MCP tool-call errors and do **not** emit a receipt. `SCOPE_DENIED` is the one policy rejection that is wrapped as `delivery_receipt{status: "error", reason: "scope_denied"}` (see §7.2)." Update the §15.1 error-catalog row for each listed code to state whether it is receipt-wrapped or a pre-receipt error, so the receipt boundary is unambiguous from either side.

---

## Summary

**Regressions:** None. Both iter2 fixes (MSG-004, FLR-003) verified present and correctly worded.

**Carry-over:** MSG-005 and MSG-006 remain open; no iter2 fix.

**New findings:** 4 (MSG-007 HIGH, MSG-008 MEDIUM, MSG-009 MEDIUM, MSG-010 LOW).

MSG-007 is the most impactful — the §7.2/§12.4 Redis-command contradiction will cause an implementation that reads §12.4 first (as storage authors naturally do) to build a broken durable inbox (LPUSH breaks FIFO; missing LTRIM breaks TTL trim). MSG-008 and MSG-009 are cross-section catalog completeness gaps; MSG-010 broadens the MSG-006 receipt-boundary defect beyond the Terminal case.

Other areas checked: path precedence rules (7 paths including §7.2 line 276), concurrent-workspace slot routing (§7.2 line 292), SSE back-pressure bounded-error policy (§7.2 line 324), coordinator-failover `inbox_cleared` schema (§7.2 line 245), `session_messages` Postgres DAG persistence (§15.4.1 line 1426, §12.3 line 136), message-ID deduplication window (§15.4.1 line 1418), rate-limit inbound aggregate cap (§7.2 line 332), sibling ordering and broadcast semantics (§7.2 lines 328–330). All remain sound.
