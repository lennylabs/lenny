# Perspective 23 — Messaging, Conversational Patterns & Multi-Turn (Iter5)

**Scope.** Verify iter4 MSG-011..MSG-017 resolutions and re-examine items left unresolved. Iter4 marked MSG-011, MSG-012, MSG-013, MSG-014 Fixed; MSG-015, MSG-016, MSG-017 were opened but not explicitly closed (no Resolution / Status marker in the iter4 summary). Compact severity rubric — following `feedback_severity_calibration_iter5.md`, carryover findings retain their iter4 severity; new findings are capped to the same bar.

---

### MSG-018. Iter4 MSG-015 carryover — `message_dropped` receipt terminology still present in §7.2 [Low]

**Section:** spec/07_session-lifecycle.md §7.2 lines 282, 293, 341

Iter4 MSG-015 flagged three §7.2 sites that use `message_dropped` terminology contradicting the canonical `delivery_receipt` with `status: "dropped"` defined in §15.4.1 line 1706. The iter4 summary provided a recommendation but no Resolution / Status marker. Spec verification confirms all three sites retain the non-canonical phrasing:

- Line 282 (in-memory inbox overflow): "sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`".
- Line 293 (durable inbox overflow): "Overflow drops the oldest entry (`LPOP` + drop) with a `message_dropped` receipt".
- Line 341 (DLQ overflow): "the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`".

`message_dropped` is not an event type, not a `status` enum value, and not referenced anywhere in §15.4.1. An implementer building a receipt handler from §7.2 alone will search for a `message_dropped` type that does not exist.

**Recommendation:** Replace all three `message_dropped` occurrences with the canonical phrasing used elsewhere: "the sender receives a `delivery_receipt` with `status: "dropped"` and `reason: "inbox_overflow"`" (or `"dlq_overflow"`). This is a pure string substitution with no semantic impact; it closes the MSG-015 gap that iter4 left open.

---

### MSG-019. Iter4 MSG-016 carryover — `delivery_receipt.reason` schema comment still contradicts the `error`-status prose [Low]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1707, 1713

The iter4 MSG-013 fix added a canonical `delivery_receipt.reason` enum table (lines 1715–1724) that includes two `error`-status reasons (`inbox_unavailable`, `scope_denied`). The MSG-013 Resolution note explicitly states MSG-016's schema-comment fix was out of scope and "will be addressed separately" — but iter4 did not address it, so the contradiction is still present:

- Line 1707 schema comment: `"reason": "<string — populated when status is dropped, expired, or rate_limited>"` — explicitly omits `error`.
- Line 1713 prose: "`error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` ..., or `reason: "scope_denied"` ...)" — `error`-status receipts DO carry `reason`.
- Line 1715–1722 canonical enum table: two `error`-status rows, both populating `reason`.

An implementer reading the inline schema block alone will omit `reason` on `error` receipts; a consumer parsing §15.4.1 end-to-end will see the contradiction and have to guess which form is authoritative.

A secondary ambiguity: line 1707's comment says `reason` is populated for `expired`, but line 1724 says `expired` "v1 does not define additional `reason` enum values — the status alone conveys the condition." These two sentences, five lines apart, disagree on whether `expired` receipts carry `reason`.

**Recommendation:** Update the line 1707 schema comment to match the canonical table and the line 1724 closure text:
`"reason": "<string — populated when status is dropped or error (per the canonical delivery_receipt.reason enum table below); omitted when status is delivered, queued, expired, or rate_limited>"`.
This is a single-line edit that removes the inline-comment vs. table contradiction without changing any semantics.

---

### MSG-020. Iter4 MSG-017 carryover — `msg_dedup` Redis key still missing from §12.4 key prefix table [Low]

**Section:** spec/12_storage-architecture.md §12.4 lines 180–193; spec/15_external-api-surface.md §15.4.1 line 1760

Iter4 MSG-017 flagged that `t:{tenant_id}:session:{session_id}:msg_dedup` — referenced at §15.4.1 line 1760 as a Redis sorted set for message-ID deduplication — is absent from the normative §12.4 key prefix table (rows 180–193). Verified in the current spec: `msg_dedup` is still referenced only in §15.4.1 line 1760, and `grep msg_dedup spec/12_storage-architecture.md` returns zero hits.

This is the same cross-section completeness defect that iter3 MSG-007 fixed for the durable inbox key (`:inbox`); it now reappears for `:msg_dedup`. The §12.4 text at line 195 explicitly names which keys the `TestRedisTenantKeyIsolation` integration test must cover (DLQ, inbox, semantic cache, delegation budget) — `msg_dedup` is omitted, so a test author building the suite from §12.4 alone will miss it and a cross-tenant deduplication-collision scenario will go undetected.

Concrete cross-tenant risk: sender-supplied message IDs are validated for uniqueness "within the tenant" (§15.4.1 line 1760), but if the Redis wrapper's tenant-scoping enforcement is not exercised by a `msg_dedup` test case, a regression that strips the tenant prefix from the dedup key would let tenant A's sender-supplied ID collide with tenant B's — causing a `400 DUPLICATE_MESSAGE_ID` rejection (or, worse, a silent dedup that drops a legitimate message) across tenant boundaries.

**Recommendation:** Add a row to §12.4 immediately after the existing `:inbox` row (line 186):

```
| `t:{tenant_id}:session:{session_id}:msg_dedup` | Message ID deduplication set | Sorted set scored by receipt timestamp; retains seen message IDs for `messaging.deduplicationWindowSeconds` (default 3600s); trimmed on write via `ZREMRANGEBYSCORE`; used to reject `400 DUPLICATE_MESSAGE_ID` (see [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) `id` field) |
```

Also extend the §12.4 line 195 `TestRedisTenantKeyIsolation` coverage sentence with a new clause: "(g) a `msg_dedup` write for tenant A's session must not be visible to a deduplication check scoped to tenant B's session, and a sender-supplied message ID duplicated across tenants must not produce a `DUPLICATE_MESSAGE_ID` rejection on the second tenant's write."

Also update the §15.4.1 line 1760 prose to cross-reference §12.4 for the canonical key registration.

---

### MSG-021. `SCOPE_DENIED` error-code entry mis-describes the `delivery_receipt` as an "event" [Low]

**Section:** spec/15_external-api-surface.md §15.1 line 992; §15.4.1 lines 1701, 1736

The `SCOPE_DENIED` row in the §15.1 error-code catalog (line 992) ends with "Returned as the `error` reason in a `delivery_receipt` **event**." But §15.4.1 is unambiguous elsewhere that `delivery_receipt` is the **synchronous** return value of `lenny/send_message` (line 1701: "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object") and explicitly **not** an event — the only messaging event is `message_expired` (line 1736: "delivered asynchronously on the sender session's event stream ... it is **not** a field on the synchronous `delivery_receipt`").

Calling the receipt an "event" contradicts the canonical MSG-014 schema block that iter4 added and re-opens the transport ambiguity that MSG-014 closed. An implementer parsing §15.1 in isolation will wire a `delivery_receipt` event-stream handler that will never fire.

**Recommendation:** Change line 992 ending from "Returned as the `error` reason in a `delivery_receipt` event" to "Returned as the `reason` on a synchronous `delivery_receipt` with `status: "error"` (see [§15.4.1](#1541-adapterbinary-protocol) `delivery_receipt.reason` enum)." This single-sentence edit aligns the error-catalog entry with the canonical receipt/event distinction the iter4 MSG-014 fix established.

---

### MSG-022. `delivery_receipt.reason` enum table is missing the `rate_limited` inbound-cap reason already implied by §7.2 [Low/Info]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1715–1724; spec/07_session-lifecycle.md §7.2 line 371

Iter4 MSG-013's fix (lines 1715–1724) deliberately left `rate_limited` with no `reason` enum rows, stating on line 1724 "for `status: "rate_limited"`, v1 does not define additional `reason` enum values — the status alone conveys the condition." But §7.2 line 371 defines **two distinct rate-limit causes** for inter-session messaging:

- **Sender-side outbound cap** — `messagingRateLimit.maxPerMinute` (default 30) / `maxPerSession`, enforced on the sending session.
- **Receiver-side inbound aggregate cap** — `messagingRateLimit.maxInboundPerMinute` (default 60), enforced on the target session to prevent O(N²) sibling storms.

These two rejections have different operational meanings for the sender: outbound-cap exceedance means "slow down"; inbound-cap exceedance means "the target is being flooded by multiple senders — coordinate with peers or adopt a hub pattern." A sender that cannot distinguish them cannot choose the correct back-off strategy. §7.2 line 371 states: "Messages exceeding the inbound limit are rejected with a `RATE_LIMITED` delivery receipt" — but on the wire this is the same opaque `status: "rate_limited"` with no `reason` that §15.4.1 mandates for the outbound case.

This is Info-level because §7.2 correctly describes the two caps and no message is silently lost; it is flagged to note that a future v1.x enum extension (`reason: "sender_rate_limit"` vs. `"target_inbound_rate_limit"`) would be a straightforward diagnostic win without breaking the closed-enum discipline.

**Recommendation:** Defer to a future spec iteration (outside the convergence scope). If added later, mirror the `delivery_receipt.reason` enum table pattern iter4 MSG-013 established: two rows under `status: "rate_limited"`, each citing the §7.2 cap it corresponds to.

---

## Convergence assessment

**Perspective 23 — Messaging, Conversational Patterns & Multi-Turn — NOT YET CONVERGED.**

Iter5 opens five findings, all Low/Info: three (MSG-018 / MSG-019 / MSG-020) are direct carryovers of iter4 MSG-015 / MSG-016 / MSG-017 that iter4 left unresolved (no Resolution / Status marker on those items in the iter4 summary; spec verification confirms the defects are still present verbatim). MSG-021 is a newly identified cross-reference inconsistency in §15.1 that re-introduces the `delivery_receipt` "event vs. synchronous response" ambiguity iter4 MSG-014 fixed elsewhere. MSG-022 is Info-only and explicitly deferred.

No High / Critical / Medium message-loss or mis-routing bugs are open. The structural messaging model (MSG-011 inbox TTL state-gating, MSG-013 receipt-reason enum, MSG-014 `message_expired` event schema, MSG-012 `LTRIM` allowlist) remains correctly fixed after iter4. Closure of the four Low findings above (all single-line or single-table-row edits) converges this perspective.
