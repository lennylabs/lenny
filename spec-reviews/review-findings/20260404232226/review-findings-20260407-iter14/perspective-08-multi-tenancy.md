# Perspective 8 — Multi-Tenancy & Tenant Isolation (Iteration 14)

**Spec file:** `technical-design.md` (8,691 lines)
**Category:** TNT
**Reviewed sections:** 4.2, 4.3, 4.5, 7.2, 10.6, 12.3, 12.4, 12.5, 12.8, 15.1, 24.9

## Findings

### TNT-035 — `session_dlq_archive` Postgres table missing from erasure scope and tenant deletion (Medium)

**Location:** Section 12.8 (erasure scope table, lines 5217-5233) and tenant deletion Phase 4 (line 5264)

**Finding:** Section 7.2 (line 2630) defines a `session_dlq_archive` Postgres table keyed by `(tenant_id, session_id, message_id)` that persists DLQ messages for `awaiting_client_action` sessions beyond the Redis TTL window. This table stores inter-session message payloads that may contain user-generated content (agent messages, delegation results).

The table is absent from:
1. The **erasure scope table** (Section 12.8, lines 5219-5233) — neither `DeleteByUser` nor `DeleteByTenant` covers it.
2. The **tenant deletion Phase 4 dependency order** (line 5264) — the ordered store list does not include it.
3. The **RLS / `lenny_tenant_guard` coverage** — no explicit statement that this table has RLS policies or is covered by `TestRLSTenantGuardMissingSetLocal`.

**Impact:** User-level GDPR erasure and tenant deletion leave orphaned message data in `session_dlq_archive`. For GDPR erasure, this is a compliance gap — PII in message payloads survives a completed erasure job.

**Fix:** Add `session_dlq_archive` to the erasure scope table (after `EvictionStateStore`, before `EventStore`). Add it to the Phase 4 dependency order. Explicitly state it carries RLS policies and is covered by `TestRLSTenantGuardMissingSetLocal`.

---

### TNT-036 — Durable inbox Redis key missing from canonical key prefix table (Medium)

**Location:** Section 12.4 (lines 5048-5055)

**Finding:** The canonical Redis key prefix table states "The following table lists all canonical key prefix patterns in use" and enumerates 5 tenant-prefixed patterns plus the pod-scoped exception. However, the durable inbox key `t:{tenant_id}:session:{session_id}:inbox` (defined in Section 7.2, line 2607) is not listed.

The table is referenced as the authoritative registry by the `TestRedisTenantKeyIsolation` integration test requirement (line 5057), which explicitly requires DLQ key coverage but does not mention inbox key coverage.

**Impact:** The canonical key table is incomplete. Implementers using it as their exhaustive key inventory will miss the durable inbox key, and the mandated integration test will not cover inbox tenant isolation.

**Fix:** Add `t:{tenant_id}:session:{session_id}:inbox` to the canonical key prefix table with role "Durable session inbox" and notes "Redis list; `durableInbox: true` only; see Section 7.2". Add inbox key coverage to the `TestRedisTenantKeyIsolation` test requirement alongside the existing DLQ coverage.

---

### TNT-037 — SemanticCache Redis key pattern missing from canonical key prefix table (Medium)

**Location:** Section 12.4 (lines 5048-5055) vs. Section 4.9 Semantic Caching (line 1350)

**Finding:** The `SemanticCache` is listed in the erasure scope table (line 5231) as "Redis (or pluggable)" and Section 4.9 (line 1350) states it "inherits tenant key isolation from the Redis wrapper layer (Section 12.4)." However, no concrete Redis key prefix pattern for the SemanticCache appears in the canonical key prefix table in Section 12.4. Every other Redis-backed store has its key pattern documented there.

**Impact:** Without a defined key pattern, implementers of the default Redis-backed SemanticCache must invent their own key scheme. The cross-reference from Section 4.9 to Section 12.4 leads to a table that contains no SemanticCache entry, creating ambiguity about the expected key format.

**Fix:** Add the SemanticCache key prefix pattern to the canonical table, e.g., `t:{tenant_id}:semcache:{pool_id}:{hash}` (or whatever the intended pattern is), with appropriate notes about `cacheScope` variations (`per-user`, `per-session`, `tenant`).
