# Perspective 23 — Messaging, Conversational Patterns & Multi-Turn (Iter7)

**Scope.** Verify iter6 MSG-023..MSG-027 resolutions in the post-iter6 spec (commit `8604ce9` — "Fix iteration 6"). Iter6 closed as "NOT YET CONVERGED" with four actionable Low findings (MSG-023 / MSG-024 / MSG-025 / MSG-026 — all carryovers from iter4/iter5 that have now survived multiple review/fix cycles) and one deferred Info finding (MSG-027). This iteration audits the spec tree after the iter6 fix commit to determine whether those four Low findings were addressed.

**Method.**
1. Re-run the exact `grep` verifications iter6 MSG-023/024/025/026 used, against the post-`8604ce9` spec.
2. Inspect `git show 8604ce9 --stat` to determine which files the iter6 fix pass touched.
3. Walk the five focus areas listed for iter7 (3 delivery paths, `input_required` × session lifecycle, "agent teams" pattern support, SSE buffer overflow, message routing × delegation policies) looking for NEW issues not raised in any prior iteration.
4. Apply `feedback_severity_calibration_iter5.md`: carryover findings retain their iter-of-origin severity; new findings are capped to the same rubric.

Focus-area audit summary (before findings):

- **3 delivery paths.** §7.2 defines **seven** numbered paths (1 `inReplyTo` resolution, 2 direct stdin, 3 `input_required` buffered, 4 `delivery: "immediate"` interrupt, 5 generic busy-buffered, 6 `suspended` resume-or-buffer, 7 DLQ). The iter7 task prompt mentions "3 message delivery paths" — this is the three high-level categories (resolve-pending / direct / buffered) that paths 1–7 collapse into, and the spec is internally consistent on the seven-path enumeration. No finding.
- **`input_required` × lifecycle.** Transitions at §7.2 lines 170–181 (running → input_required → {running, cancelled, expired, resume_pending, failed}) plus path-3 routing override (lines 315, 321) and path-3-wins-over-path-5 concurrency rule (line 315) are internally consistent. No absent `input_required → completed` edge because a runtime blocked in `request_input` cannot call `session_complete` — absence is correct. No finding.
- **"Agent teams" pattern.** §7.2 lines 365–373 (Sibling coordination patterns) document `messagingScope: siblings`, `treeVisibility: full` requirement, coordinator-local FIFO ordering, no broadcast primitive, O(N²) storm caps via `maxInboundPerMinute`, and parent-communication asymmetry (no `lenny/send_to_parent`). §8.3 lines 259–274 document the `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` delegation-time compatibility check. These pieces cover the "agent teams" pattern sufficiently for v1. No new finding.
- **SSE buffer overflow.** §15 lines 125–173 (`OutboundChannel` normative back-pressure policy) and §7.2 line 363 (SSE back-pressure) define (a) `MaxOutboundBufferDepth` = 256 events default, (b) buffered-drop vs. bounded-error policies with mandatory surface semantics (`gap_detected` frame on drop, channel-close on bounded-error), (c) `lenny_outbound_channel_buffer_drop_total` metric. Policy and metric both exist. No new finding.
- **Message routing × delegation policies.** §8.3 lines 259–274 define `treeVisibility` ↔ `messagingScope` compatibility and `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`. §7.2 line 371 defines `maxInboundPerMinute` as a sibling-storm cap. §15.1 lines 998, 999 catalog `SCOPE_DENIED` and `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE`. One catalog omission found — see MSG-032 below.

---

## Prior-iteration carry-forwards

### MSG-028. Iter6 MSG-023 carryover — three `message_dropped` strings still unchanged in §7.2 (third consecutive carryover) [Low]

**Section:** spec/07_session-lifecycle.md §7.2 lines 282, 293, 341

Iter6 MSG-023 flagged three §7.2 sites that still use `message_dropped` terminology contradicting the canonical `delivery_receipt` with `status: "dropped"` defined in §15.4.1. The iter6 fix commit `8604ce9` did not touch `spec/07_session-lifecycle.md`. Verification in the current tree:

- Line 282 (in-memory inbox overflow row, **UNCHANGED**): "sender receives a `message_dropped` delivery receipt with `reason: "inbox_overflow"`".
- Line 293 (durable inbox overflow row, **UNCHANGED**): "Overflow drops the oldest entry (`LPOP` + drop) with a `message_dropped` receipt."
- Line 341 (DLQ overflow row, **UNCHANGED**): "the oldest DLQ entry is dropped and the sender receives a `message_dropped` delivery receipt with `reason: "dlq_overflow"`".

This is now the **third consecutive iteration** (iter4 MSG-015 → iter5 MSG-018 → iter6 MSG-023 → iter7 MSG-028) that this purely cosmetic string-substitution edit has been deferred in the fix pass. `message_dropped` remains neither an event type, nor a `status` enum value, nor a `reason` enum value, and is not referenced in §15.4.1 where the canonical `delivery_receipt` schema lives.

`grep -c 'message_dropped' spec/07_session-lifecycle.md` returns `3` (unchanged from iter5 and iter6). `grep 'message_dropped' spec/15_external-api-surface.md` returns zero hits — the §15.4.1 schema block does not define `message_dropped` anywhere, confirming the §7.2 strings are orphans.

Severity unchanged (Low) per `feedback_severity_calibration_iter5.md`. A downstream implementer whose receipt handler is derived from §7.2 alone will search for a `message_dropped` type that does not exist — documentation-only defect, no wire-protocol impact.

**Recommendation (unchanged from iter5 and iter6):** Replace all three occurrences with canonical phrasing: "the sender receives a `delivery_receipt` with `status: "dropped"` and `reason: "inbox_overflow"`" (or `"dlq_overflow"` at line 341). Zero-semantic-impact edit; a single `sed` pass across the three lines.

---

### MSG-029. Iter6 MSG-024 carryover — `delivery_receipt.reason` schema comment still contradicts the `error`-status prose and the canonical enum table (third consecutive carryover) [Low]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1719, 1725, 1727–1736

Iter6 MSG-024 flagged a three-way contradiction in §15.4.1 between the inline schema comment, the prose sentence, and the canonical `delivery_receipt.reason` enum table. The iter6 fix commit `8604ce9` touched §15 in a different location (circuit-breaker endpoints and scope taxonomy — API-020/021); it did not touch lines 1707–1735.

Current line numbers (shifted by iter6 fixes elsewhere in §15):

- Line 1719 schema comment (**UNCHANGED** content): `"reason": "<string — populated when status is dropped, expired, or rate_limited>"` — explicitly omits `error`.
- Line 1725 prose (**UNCHANGED**): "`error` (delivery failed due to infrastructure error, e.g., `reason: "inbox_unavailable"` ..., or `reason: "scope_denied"` ...)" — `error`-status receipts DO carry `reason`.
- Lines 1727–1734 canonical enum table (**UNCHANGED**): two rows under `status: "error"` (`inbox_unavailable`, `scope_denied`), both populating `reason`.

The same three-way contradiction iter5 MSG-019 and iter6 MSG-024 flagged is still present: the schema comment says `reason` is populated for `{dropped, expired, rate_limited}` but the table and prose say `reason` is populated for `{dropped, error}` (and omitted for `{expired, rate_limited}` per line 1736).

Secondary ambiguity (also unchanged): line 1719's comment says `reason` is populated for `expired`, but line 1736 says "for `status: "rate_limited"`, v1 does not define additional `reason` enum values — the status alone conveys the condition" and `expired` is also enum-less. These two sentences contradict each other on whether `expired` receipts carry `reason`.

An implementer deriving a receipt parser from line 1719 alone discards `reason` on `error` receipts, losing the `inbox_unavailable` vs. `scope_denied` discrimination that MSG-013's iter4 fix deliberately added.

**Recommendation (unchanged from iter5 and iter6):** Update line 1719 to match the canonical enum table and the line 1736 closure text:
`"reason": "<string — populated when status is \"dropped\" or \"error\" per the \`delivery_receipt.reason\` enum table below; omitted when status is \"delivered\", \"queued\", \"expired\", or \"rate_limited\">"`.
Single-line, zero-semantic-impact edit.

---

### MSG-030. Iter6 MSG-025 carryover — `msg_dedup` Redis key still missing from §12.4 key-prefix table (third consecutive carryover) [Low]

**Section:** spec/12_storage-architecture.md §12.4 lines 179–193 (key prefix table); §12.4 line 195 (`TestRedisTenantKeyIsolation` coverage clause); spec/15_external-api-surface.md §15.4.1 line 1772

Iter6 MSG-025 flagged that `t:{tenant_id}:session:{session_id}:msg_dedup` — referenced at §15.4.1 line 1772 as the Redis sorted set used for sender-supplied message-ID deduplication — is absent from the normative §12.4 key-prefix table. Verification after the iter6 fix commit:

- `grep msg_dedup spec/12_storage-architecture.md` returns **zero hits** (unchanged from iter5 and iter6).
- The §12.4 key-prefix table (lines 181–193) still enumerates 11 key patterns (`lease:session`, `quota:tokens`, `session:dlq`, `billing:stream`, `exp:sticky`, `session:inbox`, `scache`, `evt`, three `lenny:pod:*` rows, `cb:{name}`, delegation-budget rows). No `:msg_dedup` row.
- §12.4 line 195 `TestRedisTenantKeyIsolation` coverage clause still enumerates six sub-cases `(a)`–`(f)` covering DLQ, inbox, semantic cache, delegation budget, and EventBus keys. `msg_dedup` is not mentioned.
- §15.4.1 line 1772 still says: "seen IDs are stored in a Redis sorted set (`t:{tenant_id}:session:{session_id}:msg_dedup`, scored by receipt timestamp) and retained for `deduplicationWindowSeconds`" — a single-site reference without cross-registration in §12.4.

This is the **third consecutive carryover** (iter4 MSG-017 → iter5 MSG-020 → iter6 MSG-025 → iter7 MSG-030) of the same cross-section completeness defect. The iter6 carry-forward text already spelled out the concrete cross-tenant collision risk that tenant-isolation test coverage mitigates; that reasoning is unchanged.

Concrete risk (unchanged from iter5 and iter6): a regression that strips the `t:{tenant_id}:` prefix from a `msg_dedup` write would allow tenant A's sender-supplied message ID to collide with tenant B's, producing either a spurious `400 DUPLICATE_MESSAGE_ID` on a legitimate cross-tenant message or — depending on the regression shape — a silent dedup that drops a legitimate message. `TestRedisTenantKeyIsolation` is the direct tenant-isolation mitigation, but its coverage sentence does not enumerate `msg_dedup`, so an implementer authoring the test suite from §12.4 alone will not exercise this path.

**Recommendation (unchanged from iter5 and iter6):** Add a row to §12.4 immediately after the `:inbox` row (line 186):

```
| `t:{tenant_id}:session:{session_id}:msg_dedup` | Message ID deduplication set | Sorted set scored by receipt timestamp; retains seen message IDs for `messaging.deduplicationWindowSeconds` (default 3600s, see [§15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol) `id` field); trimmed on write via `ZREMRANGEBYSCORE`; used to reject `400 DUPLICATE_MESSAGE_ID` on duplicate sender-supplied IDs within the window |
```

Extend the line 195 `TestRedisTenantKeyIsolation` coverage clause with a seventh sub-case `(g)`: "a `msg_dedup` write for tenant A's session must not be visible to a deduplication check scoped to tenant B's session — a sender-supplied message ID duplicated across tenants must not cause the second tenant's write to be rejected with `DUPLICATE_MESSAGE_ID`."

Also add an inline cross-reference from §15.4.1 line 1772 back to §12.4 for the canonical key registration.

---

### MSG-031. Iter6 MSG-026 carryover — `SCOPE_DENIED` error-code entry still mis-describes the `delivery_receipt` as an "event" (second consecutive carryover) [Low]

**Section:** spec/15_external-api-surface.md §15.1 line 998

Iter6 MSG-026 flagged that the `SCOPE_DENIED` row in the §15.1 error-code catalog still says "Returned as the `error` reason in a `delivery_receipt` **event**" — contradicting §15.4.1's statement that `delivery_receipt` is the **synchronous** return value of `lenny/send_message` (not an event). The iter6 fix commit did not touch line 998 (the only §15.1 changes in `8604ce9` were in the error-categorization family — `PLATFORM_AUDIT_REGION_UNRESOLVABLE`, `GIT_CLONE_REF_*`, and circuit-breaker endpoint rows). Verification:

- Line 998 (SCOPE_DENIED row, **UNCHANGED**): "Returned as the `error` reason in a `delivery_receipt` event. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model)."
- Line 1713 §15.4.1 (authoritative, unchanged): "Every `lenny/send_message` call returns a synchronous `delivery_receipt` object."
- Line 1748 §15.4.1 (authoritative, unchanged): "The `message_expired` event is delivered asynchronously on the sender session's event stream — it is **not** a field on the synchronous `delivery_receipt`."
- Line 1734 (canonical `delivery_receipt.reason` enum, unchanged): describes `scope_denied` as a `reason` under `status: "error"`.

A client implementer wiring error-handling from the §15.1 catalog alone will wire a `delivery_receipt` event-stream handler that never fires — the receipt is returned in the JSON-RPC response body, not on the event stream. This re-introduces the transport-kind ambiguity that iter4 MSG-014 closed in §15.4.1.

**Recommendation (unchanged from iter5 and iter6):** Change line 998 from "Returned as the `error` reason in a `delivery_receipt` event" to "Returned as the `reason` on a synchronous `delivery_receipt` with `status: "error"` (see [§15.4.1](#1541-adapterbinary-protocol) `delivery_receipt.reason` enum)." Single-sentence edit.

---

### MSG-033. Iter6 MSG-027 carryover — `rate_limited` `reason` enum still lacks sender/target disambiguation (Info, explicitly deferred) [Info]

**Section:** spec/15_external-api-surface.md §15.4.1 lines 1727–1736; spec/07_session-lifecycle.md §7.2 line 371

Iter6 MSG-027 carried forward iter5 MSG-022's observation that §7.2 line 371 defines two distinct rate-limit causes (sender-side outbound `maxPerMinute` / `maxPerSession` and receiver-side inbound aggregate `maxInboundPerMinute`) that both surface on the wire as `status: "rate_limited"` with no `reason`, and iter5 explicitly deferred it to a future v1.x enum extension.

Verification after iter6 fixes: the §15.4.1 enum table (lines 1727–1734) is unchanged — no `rate_limited` rows were added. Line 1736 still closes with "for `status: "rate_limited"`, v1 does not define additional `reason` enum values — the status alone conveys the condition." No work was expected in iter6; this entry is recorded solely for carry-forward tracking. Retains the Info severity — **does not block convergence.**

**Recommendation:** No action in this iteration. Deferred as a v1.x enhancement per the iter5/iter6 carry-forward note. If ever addressed, the two rows should sit under `status: "rate_limited"` with reasons `sender_rate_limit` and `target_inbound_rate_limit`, each cross-linking the §7.2 cap it corresponds to.

---

## New findings

### MSG-032. `SLOT_ID_REQUIRED` is referenced in §7.2 but missing from the §15.1 error-code catalog and the docs error catalog [Low]

**Section:** spec/07_session-lifecycle.md §7.2 line 331 (the only reference in the spec tree); spec/15_external-api-surface.md §15.1 error-code catalog (lines 970–1100 region); docs/reference/error-catalog.md

`SLOT_ID_REQUIRED` is introduced by §7.2 line 331 as the wire-level rejection when a `MessageEnvelope` targeting a concurrent-workspace session omits `slotId`:

> "Messages without a `slotId` in concurrent-workspace mode are rejected with `SLOT_ID_REQUIRED`."

Verification:

- `grep -c SLOT_ID_REQUIRED spec/15_external-api-surface.md` → **0** (not in the error-code catalog).
- `grep -c SLOT_ID_REQUIRED docs/reference/error-catalog.md` → **0** (not in the docs error catalog either).
- `grep -rn SLOT_ID_REQUIRED spec/` → only the one §7.2 reference.

This is the same catalog-gap pattern iter6 API-020/021/024 and iter6 CNT-024 flagged for `circuit_breaker` endpoints and `WORKSPACE_PLAN_INVALID` respectively: the error code is referenced by one section but absent from the single-source-of-truth catalog that SDK authors and error-code-gate CI jobs consume. Unlike those iter6 findings, the referencing section (§7.2) is a runtime path used by every concurrent-workspace deployment, so the catalog gap is a real developer-experience defect, not purely cosmetic.

Severity rubric anchor: iter6 API-020 and CNT-024 were **Medium** — but those were dangling error-code references in multiple sections (6× and 4× respectively) and carried direct endpoint/endpoint-category implications. `SLOT_ID_REQUIRED` is referenced once in §7.2 and is a `PERMANENT`/400 client-supplied-envelope validation error, closer in kind to `INVALID_DELIVERY_VALUE` (already catalogued at line 1076). Closest iter6 analogue by failure-class was **Low** (`SES-020` kind — a single-site enumeration miss). Per the severity rubric, Low.

No runtime impact: the rejection itself is well-defined (the gateway emits the code string, clients can parse it). The defect is purely in the discovery surface — a client implementer skimming the error catalog will not find `SLOT_ID_REQUIRED` and will not know to handle it, leading to opaque failure modes when mis-built clients omit `slotId` under concurrent-workspace.

**Recommendation:** Add a row to the §15.1 error-code catalog (under the `PERMANENT` block, alphabetically adjacent to `SESSION_NOT_FOUND` / `SLOT_*` codes):

```
| `SLOT_ID_REQUIRED`          | `PERMANENT` | 400         | `MessageEnvelope` targets a concurrent-workspace session but omits `slotId`. The `slotId` field is mandatory for concurrent-workspace mode; session-mode and task-mode messages never carry it. See [Section 7.2](07_session-lifecycle.md#72-interactive-session-model) (Concurrent-workspace mode `slotId` routing) and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). |
```

Also add a corresponding row to `docs/reference/error-catalog.md` (client-guide parity per `feedback_docs_sync_after_spec_changes.md`):

```
| `SLOT_ID_REQUIRED` | 400 | Message envelope targets a concurrent-workspace session but omits `slotId`. | Include `slotId` in the envelope for concurrent-workspace sessions. |
```

---

## Carry-forward vs. convergence assessment

### Resolution status of iter6 findings

| Iter6 ID | Severity | Fix attempted in `8604ce9`? | Status | Iter7 ID |
|---|---|---|---|---|
| MSG-023 | Low | No — three `message_dropped` strings unchanged (§7.2 untouched by iter6 fix commit) | **Open (carryover #3 — iter4→iter5→iter6→iter7)** | MSG-028 |
| MSG-024 | Low | No — line 1719 schema comment unchanged (§15.4.1 not touched by iter6 fix commit for this line) | **Open (carryover #3 — iter4→iter5→iter6→iter7)** | MSG-029 |
| MSG-025 | Low | No — `msg_dedup` still missing from §12.4 (§12.4 touched by iter6 fix commit for a different finding — per-user fail-open text — not the MSG-025 row) | **Open (carryover #3 — iter4→iter5→iter6→iter7)** | MSG-030 |
| MSG-026 | Low | No — §15.1 line 998 still says "event" (§15.1 touched by iter6 fix commit for API-020/021/022/023/024; not for the SCOPE_DENIED row) | **Open (carryover #2 — iter5→iter6→iter7)** | MSG-031 |
| MSG-027 | Info | Deferred by design | Open (deferred) | MSG-033 |

**New findings this iteration:** one (MSG-032 — `SLOT_ID_REQUIRED` catalog omission).

**Regression audit.** No new High / Critical / Medium messaging defects introduced after iter6 fixes. The structural messaging model remains correct:
- Three conceptual delivery paths (direct / buffered / DLQ) → seven enumerated paths in §7.2 — internally consistent.
- `input_required` lifecycle gate and path-3 routing priority — internally consistent across §7.1 state machine and §7.2 path table.
- Sibling coordination (§7.2 lines 365–373) — O(N²) storm caps, coordinator-local FIFO, parent asymmetry, no broadcast primitive — all unchanged and correct.
- SSE buffer-overflow (§15 `OutboundChannel` back-pressure, §7.2 SSE back-pressure note) — bounded-error + buffered-drop both documented with gap-detected surface semantics and a dedicated counter.
- Message routing × delegation policies — `TREE_VISIBILITY_INSUFFICIENT_FOR_MESSAGING_SCOPE` and `SCOPE_DENIED` routing checks, sibling-storm caps via `maxInboundPerMinute`, all unchanged and correct.
- MSG-014 `message_expired` event schema, MSG-013 `delivery_receipt.reason` enum, MSG-011 inbox TTL state-gating, MSG-012 `LTRIM` allowlist, all unchanged and correct.

**Docs reconciliation (per `feedback_docs_sync_after_spec_changes.md`).** Spot checks:

- `docs/reference/error-catalog.md` already catalogues `SCOPE_DENIED`, `DUPLICATE_MESSAGE_ID`, `INVALID_DELIVERY_VALUE`, `TARGET_NOT_READY`, and does **not** mirror the §15.1 `SCOPE_DENIED` "event" language — so the MSG-031 spec fix requires no docs edit. `SLOT_ID_REQUIRED` is absent from both spec and docs, so MSG-032 requires a parallel docs edit (called out in the MSG-032 recommendation).
- `docs/client-guide/session-lifecycle.md`, `docs/api/mcp.md`, `docs/runtime-author-guide/platform-tools.md` — no `message_dropped` strings and no `delivery_receipt event` phrasing found; docs side of MSG-028 / MSG-031 is clean.
- No docs references to `msg_dedup`; MSG-030 has no docs mirror.
- No other new docs drift surfaced in this iteration.

### Convergence verdict

**Perspective 23 — Messaging, Conversational Patterns & Multi-Turn — NOT YET CONVERGED.**

Iter7 opens six findings: zero Critical / High / Medium; five Low; one Info (deferred). Four of the five Low findings are re-carryovers of iter6 findings:

- **MSG-028 (third consecutive carryover of iter4 MSG-015 / iter5 MSG-018 / iter6 MSG-023)** — three `message_dropped` strings in §7.2.
- **MSG-029 (third consecutive carryover of iter4 MSG-016 / iter5 MSG-019 / iter6 MSG-024)** — `delivery_receipt.reason` schema comment vs. table contradiction.
- **MSG-030 (third consecutive carryover of iter4 MSG-017 / iter5 MSG-020 / iter6 MSG-025)** — `msg_dedup` missing from §12.4 key-prefix table.
- **MSG-031 (second consecutive carryover of iter5 MSG-021 / iter6 MSG-026)** — §15.1 line 998 still says "event".

The fifth Low is MSG-032 (`SLOT_ID_REQUIRED` catalog omission), which is genuinely new and follows the familiar iter6-surface-addition-missing-catalog-entry pattern.

All five Low findings are single-line or single-table-row spec edits with zero semantic impact on the messaging model. **Three of these defects have now survived three consecutive review/fix cycles without being addressed.** This is an unusually persistent pattern for Low findings — the iter5 and iter6 fix-pass commit messages do not enumerate any MSG items, suggesting the fix subagent dispatch is consistently filtering them out below the severity threshold. Given that each is genuinely a single-edit touch-up with no architectural dependency, it is worth either (a) explicitly instructing the fix subagent to pick up all Low findings with a "pure string-substitution" / "single-table-row" tag on a given iteration, or (b) accepting that these will remain as carry-forwards until a final cosmetic cleanup pass is scheduled before v1 cut.

The underlying messaging design is structurally sound. No further architectural review of Perspective 23 is needed until a new wave of messaging features lands (e.g., multi-thread sessions, `allowedExternalEndpoints` activation, broadcast primitives).

---

**Perspective 23 findings (iter7): 6 total — 0 Critical, 0 High, 0 Medium, 5 Low, 1 Info.**
