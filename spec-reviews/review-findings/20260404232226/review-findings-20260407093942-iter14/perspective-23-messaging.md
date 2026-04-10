# Perspective 23: Messaging, Conversational Patterns & Multi-Turn Interactions — Iteration 14

**Spec file:** `technical-design.md` (8,691 lines)
**Reviewer focus:** Message delivery paths, input_required integration, agent teams, SSE buffer overflow, message routing + delegation policies
**Prior findings carried forward:** MSG-037 (still present), MSG-038 (resolved)

---

## Findings

### MSG-037 — `delivery_receipt` schema `reason` field omits `error` status (MEDIUM — still open from iter 13)

**Location:** Section 15.4, lines 6988 vs 6994

**Problem:** The canonical `delivery_receipt` JSON schema comment at line 6988 states:

> `"reason": "<string — populated when status is dropped, expired, or rate_limited>"`

But line 6994 documents that the `error` status also populates `reason` (e.g., `reason: "inbox_unavailable"`, `reason: "scope_denied"`). The schema comment omits `error` from the list of statuses that populate `reason`.

**Why it matters:** A runtime author reading only the schema (the natural starting point) would conclude that `reason` is never populated for `error` receipts and would not parse it, losing actionable diagnostic information.

**Fix:** Change line 6988 to:
```
"reason": "<string — populated when status is dropped, expired, rate_limited, or error>"
```

---

### MSG-039 — `delivery: "immediate"` behavior undefined for `input_required` sessions (MEDIUM)

**Location:** Section 15.4 line 6976 vs Section 7.2 line 2639 (path 4)

**Problem:** The `delivery` field definition (line 6976) states that `"immediate"` means: "If session is `running`, the gateway sends an interrupt signal [...] and writes the message to stdin." Since `input_required` is a sub-state of `running` (line 2546), this definition technically applies to `input_required` sessions.

However, delivery path 4 (line 2639) states that when a session is `input_required` and the message does not match an outstanding `inReplyTo`, "the runtime is not reading from stdin while blocked in a `request_input` call, so direct delivery is not possible" -- the message is unconditionally buffered regardless of the `delivery` flag.

These two rules contradict: the `immediate` delivery definition says the gateway interrupts and delivers to stdin for `running` sessions (which includes `input_required`), but path 4 says stdin delivery is impossible during `input_required`.

**Why it matters:** An agent sending `delivery: "immediate"` to a child in `input_required` state cannot predict whether the message will be delivered immediately (per the `immediate` definition) or queued (per path 4). The receipt status would differ depending on which rule the gateway follows.

**Fix:** Add an explicit clause to the `delivery: "immediate"` definition (line 6976) or to path 4 (line 2639) stating that `delivery: "immediate"` does not override path 4 buffering -- when the target is `input_required` and the message does not match an outstanding `inReplyTo`, the message is buffered regardless of the `delivery` flag, and the receipt status is `queued`. Alternatively, add a note to the `"immediate"` row: "If session is in the `input_required` sub-state of `running`, `immediate` has no effect — see path 4."

---

### MSG-040 — `delegationDepth` referenced as MessageEnvelope field but absent from canonical schema (MEDIUM)

**Location:** Section 15.4, lines 6940-6952 (schema) vs lines 7006 and 7010 (field references)

**Problem:** Line 7006 describes `delegationDepth` as a gateway-injected field on the `MessageEnvelope`:

> "the `delegationDepth` field (integer, 0-based, gateway-injected) records how many tree hops the message crossed"

Line 7010 lists it as part of the envelope's future-proof design:

> "`MessageEnvelope` with `id`, `from`, `inReplyTo`, `threadId`, `delivery`, and `delegationDepth` accommodates all future conversational patterns"

But the canonical `MessageEnvelope` JSON schema (lines 6940-6952) does not include `delegationDepth`. The field appears in prose descriptions but is missing from the normative schema definition.

**Why it matters:** Runtime authors implementing `MessageEnvelope` deserialization from the JSON schema will not know the field exists. The schema is the single source of truth for wire format -- any field not in the schema is effectively invisible to implementers.

**Fix:** Add `"delegationDepth": "<integer — 0-based; gateway-injected for cross-tree messages; absent for same-node messages>"` to the `MessageEnvelope` JSON schema block at line 6952.

---

## Summary

| # | ID | Severity | Status | Description |
|---|--------|----------|--------|-------------|
| 1 | MSG-037 | MEDIUM | Still open | `delivery_receipt` schema `reason` field omits `error` from populated-status list |
| 2 | MSG-039 | MEDIUM | New | `delivery: "immediate"` behavior undefined for `input_required` sessions |
| 3 | MSG-040 | MEDIUM | New | `delegationDepth` referenced but absent from canonical `MessageEnvelope` schema |

**Resolved from prior iteration:** MSG-038 (inbox-to-DLQ for `durableInbox: true` is now explicitly documented at line 2624).
