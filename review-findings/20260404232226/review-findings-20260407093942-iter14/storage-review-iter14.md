# Technical Design Review Findings — 2026-04-07 (Iteration 14)

**Document reviewed:** `technical-design.md` (8,691 lines)
**Perspective:** 9 — Storage Architecture & Data Management
**Iteration:** 14
**Prior finding status:** STR-039 (XAUTOCLAIM billing duplicate race) — RESOLVED. Line 4675 now includes `INSERT ... ON CONFLICT (tenant_id, stream_entry_id) DO NOTHING` with a unique constraint, exactly as recommended.
**New findings:** 3

## Medium

| # | ID | Finding | Section | Lines |
|---|-----|---------|---------|-------|
| 1 | STR-040 | `session_dlq_archive` Postgres table missing from erasure scope and tenant deletion | 7.2, 12.8 | 2630, 5219-5233, 5264 |
| 2 | STR-041 | Durable inbox Redis key `t:{tenant_id}:session:{session_id}:inbox` missing from canonical key prefix table | 7.2, 12.4 | 2607, 5046-5055 |
| 3 | STR-042 | Delete-marker lifecycle rule scoped only to `/checkpoints/` prefix; other versioned prefixes accumulate delete markers | 12.5 | 5137 |

### STR-040 — `session_dlq_archive` Postgres table missing from erasure scope and tenant deletion (MEDIUM)

**Location:** Line 2630 (definition), Lines 5219-5233 (erasure scope table), Line 5264 (tenant deletion Phase 4)

**Problem:** Line 2630 defines a `session_dlq_archive` Postgres table that stores DLQ messages for `awaiting_client_action` sessions: "the gateway flushes the Redis DLQ to the `session_dlq_archive` Postgres table (keyed by `(tenant_id, session_id, message_id)`) to ensure DLQ durability beyond the Redis TTL window." This table contains inter-session message content, which may include T3-Confidential data (user-generated content, task context).

However, `session_dlq_archive` is not listed in the erasure scope table (Section 12.8, lines 5219-5233), not assigned to any storage role in Section 12.2, and not included in the tenant deletion Phase 4 dependency order (line 5264). A `DeleteByUser` or `DeleteByTenant` erasure job would leave message records in this table, violating the GDPR erasure guarantee.

**Fix:** Add `session_dlq_archive` to the erasure scope table under `SessionStore` (it is keyed by `session_id` and `tenant_id`, matching `SessionStore`'s scope). Add it to the Phase 4 deletion order before `SessionStore` (since it references `session_id`). Alternatively, define it as part of a new `MessageStore` role and add that role to both the erasure scope and deletion order.

### STR-041 — Durable inbox Redis key missing from canonical key prefix table (MEDIUM)

**Location:** Line 2607 (key definition), Lines 5046-5055 (canonical key prefix table)

**Problem:** When `durableInbox: true`, the inbox is backed by a Redis list keyed `t:{tenant_id}:session:{session_id}:inbox` (line 2607). This key follows the tenant prefix convention but is absent from the canonical key prefix table in Section 12.4 (lines 5046-5055), which is described as the authoritative list of all Redis key patterns. The table lists the DLQ key (`t:{tenant_id}:session:{session_id}:dlq`) but omits the inbox key.

This creates two issues: (1) the canonical table is incomplete, which undermines its purpose as the single reference for tenant key isolation auditing and Redis Cluster hash-slot planning; (2) the `TestRedisTenantKeyIsolation` integration test (line 5057) explicitly calls out DLQ key coverage but does not mention inbox key coverage, so tenant isolation for durable inbox keys may not be tested.

**Fix:** Add a row to the canonical key prefix table: `t:{tenant_id}:session:{session_id}:inbox | Durable session inbox (when durableInbox: true) | Redis list; FIFO delivery; recovered on coordinator lease acquisition; see Section 7.2`. Add inbox key coverage to the `TestRedisTenantKeyIsolation` integration test description.

### STR-042 — Delete-marker lifecycle rule scoped only to `/checkpoints/` prefix (MEDIUM)

**Location:** Line 5137

**Problem:** The delete-marker lifecycle rule is specified only for the checkpoints prefix: "A lifecycle rule MUST be configured on the checkpoints prefix (`/{tenant_id}/checkpoints/`) to expire delete markers after 24 hours and to expire noncurrent versions after 1 day." However, the `ArtifactStore` uses the path format `/{tenant_id}/{object_type}/{session_id}/{filename}` (line 5152), and bucket versioning is enabled for the entire bucket (line 5134: "Enable bucket versioning for checkpoint objects to prevent accidental overwrites"). The GC job (Section 12.5) deletes artifacts across all object types (workspace snapshots, uploaded files, session transcripts), not just checkpoints.

With versioning enabled bucket-wide but delete-marker lifecycle rules scoped only to `/checkpoints/`, delete markers from GC-deleted artifacts under other prefixes (e.g., `/{tenant_id}/uploads/`, `/{tenant_id}/transcripts/`, `/{tenant_id}/eviction/`) accumulate indefinitely. At scale, this degrades `ListObjects` performance for non-checkpoint prefixes -- the same problem the checkpoints rule was designed to prevent.

**Fix:** Either (a) extend the lifecycle rule to cover all artifact prefixes (simplest: apply the `NoncurrentVersionExpiration` and `ExpiredObjectDeleteMarker` rules at the bucket level rather than prefix-scoped), or (b) add explicit lifecycle rules for each `object_type` prefix used by the `ArtifactStore`. Option (a) is recommended since the GC job is the sole deletion path for all artifact types and the same rationale applies uniformly.

## Verification notes

Checked the following areas for issues; all were internally consistent:

- **Redis fail-open security model** (Section 12.4, lines 5067-5083): Bounded fail-open with cumulative timer, per-user and per-tenant ceilings, cached_replica_count (not hard-coded 1), fail-closed after cumulative threshold. No security gap found.
- **Artifact GC strategy** (Section 12.5, lines 5158-5167): Leader-elected, idempotent, per-artifact retry independence, legal-hold-aware checkpoint rotation, concurrent-workspace slot-aware retention (per-slot "latest 2"). All consistent.
- **"No shared RWX storage" non-goal** (Section 2, line 46): Consistently enforced — no cross-pod mount references anywhere, session derivation always copies (line 5164/5276), gateway-mediated file delivery only.
- **Checkpoint storage scaling** (Section 12.5): Concurrent-workspace mode correctly multiplies retention by `maxConcurrent` slots. Legal hold accumulation documented with storage growth acceptance and monitoring (`CheckpointStorageHigh` alert). MinIO erasure coding and versioning lifecycle specified.
- **Data-at-rest encryption completeness** (Section 12.9): All T3/T4 stores have encryption specified. Postgres volume-level encryption (line 5038), MinIO SSE-S3/SSE-KMS (line 5154), Redis app-layer encryption for tokens/credential leases (line 5059). Semantic cache entries (T3 in Redis) rely on Redis TLS for in-transit and volume-level encryption for at-rest, which is consistent with the "Redis data is treated as ephemeral" principle (line 5061) -- semantic cache entries are reconstructable and have short TTLs. MemoryStore (T3) uses Postgres with standard storage-layer encryption -- consistent with T3 controls table (line 5346).
- **EvictionStateStore encryption** (Section 4.4, line 316): Classified T3, stored in Postgres with RLS. T3 controls require storage-layer encryption (not envelope), which is satisfied by Postgres volume-level encryption. Consistent.
- **Billing event stream durability** (Section 11.2.1): Two-tier failover (Redis stream -> in-memory buffer), XAUTOCLAIM with ON CONFLICT deduplication (STR-039 fix confirmed), consumer group per-replica, stream TTL, and back-pressure. Internally consistent.
- **Storage quota counter rehydration** (Section 11.2, line 4602): On Redis restart, storage counters rehydrated from `SUM(artifact_size_bytes)` in Postgres. Consistent with token quota MAX-rule reconciliation.
- **Tenant deletion Phase 4 dependency order** (line 5264): Mostly correct -- stores deleted in reverse-dependency order. `TokenStore` appears in both Phase 3 (revoke) and Phase 4 (delete) -- idempotent, not an error. `billing_seq_{tenant_id}` sequence explicitly dropped.
- **Data residency enforcement** (Section 12.8, lines 5278-5298): StorageRouter fails closed on unresolvable region, admission webhook fail-closed, KMS key residency validation at tenant creation. Internally consistent.
