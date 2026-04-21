# Perspective 23 — Messaging, Conversational Patterns & Multi-Turn (Iter6)

**Scope.** Verify iter5 MSG-018..MSG-022 resolutions in the post-iter5 spec. Iter5 was closed as "NOT YET CONVERGED" with four actionable Low findings (MSG-018 / MSG-019 / MSG-020 / MSG-021) and one deferred Info finding (MSG-022). This iteration audits the spec tree after commit `c941492` ("Fix iteration 5: applied fixes for Critical/High/Medium findings + docs sync") to determine whether those four Low findings were actually addressed during the iter5 fix pass or were again left open. Per `feedback_severity_calibration_iter5.md`, carryover findings retain their iter5 severity; no new-severity escalation is applied.

Methodology: verbatim `grep` verification against the live spec for each of the four strings iter5 flagged, plus docs/ reconciliation check per `feedback_docs_sync_after_spec_changes.md`.

---

### MSG-023. Iter5 MSG-018 carryover — `message_dropped` receipt terminology still present in §7.2 (three sites unchanged) [Low]

**Section:** spec/07_session-lifecycle.md §7.2 lines 282, 293, 341

Iter5 MSG-018 flagged three §7.2 sites using `message_dropped` terminology that contradicts the canonical `delivery_receipt` with `status: "dropped"` defined in §15.4.1. The iter5 fix pass (commit `c941492`) did not touch these sites; spec verification confirms the three strings are still present verbatim:

- Line 282 (in-memory inbox overflow row): "`sender receives a \`message_dropped\` delivery receipt with \`reason: "inbox_overflow"\``".
- Line 293 (durable inbox overflow row): "`Overflow drops the oldest entry (\`LPOP\` + drop) with a \`message_dropped\` receipt.`"
- Line 341 (DLQ overflow row): "`the oldest DLQ entry is dropped and the sender receives a \`message_dropped\` delivery receipt with \`reason: "dlq_overflow"\``".

`message_dropped` is neither an event type, nor a `status` enum value, nor a `reason` enum value, and is not referenced in §15.4.1 where the canonical `delivery_receipt` schema lives. An implementer reading §7.2 in isolation will search for a `message_dropped` message type or a `message_dropped` event — neither exists.

This is the **second consecutive iteration** (iter4 MSG-015 → iter5 MSG-018 → iter6 MSG-023) that this purely cosmetic string-substitution fix has been deferred. Convergence cannot close on this perspective until these three strings are reconciled with §15.4.1.

**Recommendation:** Replace all three `message_dropped` occurrences with canonical phrasing: "the sender receives a `delivery_receipt` with `status: "dropped"` and `reason: "inbox_overflow"`" (or `"dlq_overflow"` at line 341). Zero-semantic-impact edit.

---

### MSG-024. Iter5 MSG-019 carryover — `delivery_receipt.reason` schema comment still contradicts the `error`-status prose and the canonical enum table [Low]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1707, 1713, 1715–1724

Iter5 MSG-019 flagged a three-way contradiction in §15.4.1 between the inline schema comment, the prose sentence, and the canonical `delivery_receipt.reason` enum table. The iter5 fix pass did not touch line 1707. Verification:

- Line 1707 schema comment (**UNCHANGED**): `"reason": "<string — populated when status is dropped, expired, or rate_limited>"` — explicitly omits `error`.
- Line 1713 prose (**UNCHANGED**): "`error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` ..., or `reason: "scope_denied"` ...)" — `error`-status receipts DO carry `reason`.
- Lines 1715–1722 canonical enum table (**UNCHANGED**): two rows under `status: "error"` (`inbox_unavailable`, `scope_denied`), both populating `reason`.

The three statements cannot all be authoritative; the schema comment remains the outlier. The secondary ambiguity around `expired` (line 1707 says `reason` is populated; line 1724 says "the status alone conveys the condition — no `reason` enum values") is also still present.

An implementer deriving a receipt parser from line 1707 alone will discard `reason` on `error` receipts, losing the exact discrimination (`inbox_unavailable` vs. `scope_denied`) that MSG-013's iter4 fix added the enum table to provide.

**Recommendation:** Update line 1707 to match the canonical enum table and the closure text at line 1724:
`"reason": "<string — populated when status is \"dropped\" or \"error\" per the \`delivery_receipt.reason\` enum table below; omitted when status is \"delivered\", \"queued\", \"expired\", or \"rate_limited\">"`.
This is a single-line, zero-semantic-impact edit.

---

### MSG-025. Iter5 MSG-020 carryover — `msg_dedup` Redis key still missing from §12.4 key prefix table [Low]

**Section:** spec/12_storage-architecture.md §12.4 lines 178–193 (key prefix table); §12.4 line 195 (`TestRedisTenantKeyIsolation` coverage clause); spec/15_external-api-surface.md §15.4.1 line 1766

Iter5 MSG-020 flagged that `t:{tenant_id}:session:{session_id}:msg_dedup` — referenced at §15.4.1 line 1766 as a Redis sorted set scoring seen message IDs for the `deduplicationWindowSeconds` window — is absent from the normative §12.4 key prefix table. Verified after iter5 fixes:

- `grep msg_dedup spec/12_storage-architecture.md` returns **zero hits** (unchanged from iter5).
- The §12.4 key prefix table (lines 180–193) still enumerates eleven key patterns without a `msg_dedup` row.
- The `TestRedisTenantKeyIsolation` coverage clause at line 195 enumerates six sub-cases `(a)`–`(f)` covering DLQ, inbox, semantic cache, delegation budget, and EventBus keys. `msg_dedup` is not mentioned.

This is the **second consecutive carryover** of the same cross-section completeness defect (iter4 MSG-017 → iter5 MSG-020 → iter6 MSG-025). A test author building `TestRedisTenantKeyIsolation` from §12.4 alone will not exercise the tenant-isolation path for the deduplication sorted set; a regression that strips the `t:{tenant_id}:` prefix from a `msg_dedup` write would allow tenant A's sender-supplied message ID to collide with tenant B's, producing either a spurious `400 DUPLICATE_MESSAGE_ID` on a legitimate cross-tenant message or (depending on the exact regression) a silent dedup that drops a legitimate message. Cross-tenant test coverage is the direct mitigation.

**Recommendation:** As proposed in iter5 (unchanged): add a key prefix row immediately after the `:inbox` row at line 186:

```
| `t:{tenant_id}:session:{session_id}:msg_dedup` | Message ID deduplication set | Sorted set scored by receipt timestamp; retains seen message IDs for `messaging.deduplicationWindowSeconds` (default 3600s, see [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) `id` field); trimmed on write via `ZREMRANGEBYSCORE`; used to reject `400 DUPLICATE_MESSAGE_ID` on duplicate sender-supplied IDs within the window |
```

Extend the §12.4 line 195 `TestRedisTenantKeyIsolation` coverage clause with a seventh sub-case `(g)`: "a `msg_dedup` write for tenant A's session must not be visible to a deduplication check scoped to tenant B's session — a sender-supplied message ID used concurrently by two tenants must not cause the second tenant's write to be rejected with `DUPLICATE_MESSAGE_ID`."

Also update §15.4.1 line 1766 with an inline cross-reference to §12.4 for the canonical key registration.

---

### MSG-026. Iter5 MSG-021 carryover — `SCOPE_DENIED` error-code entry still mis-describes the `delivery_receipt` as an "event" [Low]

**Section:** spec/15_external-api-surface.md §15.1 line 992

Iter5 MSG-021 flagged that the `SCOPE_DENIED` row in the §15.1 error-code catalog ends with "Returned as the `error` reason in a `delivery_receipt` **event**" — contradicting §15.4.1's clear statement that `delivery_receipt` is the **synchronous** return value of `lenny/send_message` (not an event) and that the only messaging event is `message_expired`. Verification after iter5 fixes:

- Line 992 (**UNCHANGED**): "Returned as the `error` reason in a `delivery_receipt` event."
- Line 1707 (§15.4.1) remains authoritative: "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object."
- Line 1736 (§15.4.1) remains authoritative: "The `message_expired` event is delivered asynchronously on the sender session's event stream — it is **not** a field on the synchronous `delivery_receipt`."

This re-introduces the transport ambiguity that iter4 MSG-014 closed in §15.4.1. A client implementer wiring error-handling from the §15.1 catalog alone will add a `delivery_receipt` event-stream handler that will never fire — the receipt is returned in the JSON-RPC response body.

**Recommendation (unchanged from iter5):** Change line 992 from "Returned as the `error` reason in a `delivery_receipt` event" to "Returned as the `reason` on a synchronous `delivery_receipt` with `status: "error"` (see [§15.4.1](#1541-adapterbinary-protocol) `delivery_receipt.reason` enum)." Single-sentence edit.

---

### MSG-027. Iter5 MSG-022 carryover — `rate_limited` `reason` enum still lacks sender/target disambiguation (Info, explicitly deferred in iter5) [Info]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1715–1724; spec/07_session-lifecycle.md §7.2 line 371

Iter5 MSG-022 noted that §7.2 line 371 defines **two distinct rate-limit causes** (sender-side outbound `maxPerMinute` / `maxPerSession` and receiver-side inbound aggregate `maxInboundPerMinute`), but both surface on the wire as the same opaque `status: "rate_limited"` with no `reason`. Iter5 explicitly deferred this to a future v1.x enum extension ("outside the convergence scope").

Verification after iter5 fixes: the §15.4.1 enum table at lines 1715–1724 is unchanged. No work was expected in iter5; this entry is recorded solely for carry-forward tracking. Retains the iter5 `Info` severity — **does not block convergence.**

**Recommendation:** No action in this iteration. Deferred as a v1.x enhancement. If ever addressed, the two rows should sit under `status: "rate_limited"` with reasons `sender_rate_limit` and `target_inbound_rate_limit`, each cross-linking the §7.2 cap it corresponds to.

---

## Carry-forward vs. convergence assessment

### Resolution status of iter5 findings

| Iter5 ID | Severity | Fix attempted in `c941492`? | Status | Iter6 ID |
|---|---|---|---|---|
| MSG-018 | Low | No — three `message_dropped` strings unchanged | **Open (carryover #2)** | MSG-023 |
| MSG-019 | Low | No — line 1707 schema comment unchanged | **Open (carryover #2)** | MSG-024 |
| MSG-020 | Low | No — `msg_dedup` still missing from §12.4 | **Open (carryover #2)** | MSG-025 |
| MSG-021 | Low | No — §15.1 line 992 still says "event" | **Open (carryover)** | MSG-026 |
| MSG-022 | Info | Deferred by design | Open (deferred) | MSG-027 |

**Regression audit.** No new High / Critical / Medium messaging defects introduced after iter5 fixes. The structural messaging model — three delivery paths (§7.2), `input_required` lifecycle gate, sibling O(N²) storm caps, DLQ / durable inbox state-gating, the MSG-014 `message_expired` event schema, and the MSG-013 `delivery_receipt.reason` enum table — remains correct and internally consistent. Terminal drain, DLQ-to-`message_expired` handoff, and coordinator-failover `inbox_cleared` semantics are unchanged and still match §15.4.1.

**Docs reconciliation (per `feedback_docs_sync_after_spec_changes.md`).** Spot checks against `docs/client-guide/session-lifecycle.md`, `docs/reference/error-catalog.md`, `docs/api/mcp.md`, and `docs/runtime-author-guide/platform-tools.md` show docs use canonical `delivery_receipt` / `status: dropped` vocabulary — they do **not** mirror the §7.2 `message_dropped` string or the §15.1 "event" phrasing, so when MSG-023 / MSG-026 are fixed in the spec, no docs edits are required. MSG-025 (missing §12.4 key row) has no doc mirror; docs already reference deduplication via the §15.4.1 paragraph. No docs drift to flag in this iteration.

### Convergence verdict

**Perspective 23 — Messaging, Conversational Patterns & Multi-Turn — NOT YET CONVERGED.**

Iter6 opens five findings, zero Critical / High / Medium. Four are Low and all are re-carryovers of iter5 findings (MSG-018 / MSG-019 / MSG-020 all now on their second carryover; MSG-021 on its first). The fifth is Info-only and explicitly deferred. Each remaining fix is a single-line or single-table-row spec edit with zero semantic impact on the messaging model:

1. MSG-023 — replace three `message_dropped` strings in §7.2.
2. MSG-024 — rewrite one line of schema comment in §15.4.1 line 1707.
3. MSG-025 — insert one key-prefix row in §12.4 and one test-case clause `(g)` at line 195.
4. MSG-026 — rewrite one sentence in §15.1 line 992.

Two-of-four of these defects have now survived two consecutive review/fix cycles without being addressed. Convergence on this perspective requires the iter6 fix pass to pick up these edits; the underlying messaging design is sound and no further architectural review is needed here.
