# Review Findings — Iteration 8, Perspective 9: Storage Architecture & Data Management

**Document reviewed:** `docs/technical-design.md`
**Iteration:** 8
**Perspective:** Storage Architecture & Data Management (STR)
**Category prefix:** STR (starting at STR-028 per instructions)
**Sections reviewed:** §11.2, §11.2.1, §11.7, §12.1–12.9
**Date:** 2026-04-07

---

## Summary

| ID      | Severity | Finding                                                                 | Section     |
|---------|----------|-------------------------------------------------------------------------|-------------|
| STR-028 | High     | Billing stream flusher has no replica coordination — concurrent flushers produce duplicate billing events | §11.2.1 |
| STR-029 | Medium   | Billing `sequence_number` "no gaps allowed" guarantee is false — Postgres sequences produce gaps on transaction rollback | §11.2.1 |
| STR-030 | Medium   | `erasure_salt` rotation procedure is internally contradictory — "deleted immediately" conflicts with "before deletion" | §12.8 |
| STR-031 | Medium   | `ArtifactStore.DeleteByUser` does not decrement the `storage_bytes_used` Redis quota counter — leaving stale quota state after user erasure | §11.2, §12.8 |

---

## STR-028 Billing Stream Flusher Has No Replica Coordination — Concurrent Flushers Produce Duplicate Billing Events [High]

**Section:** §11.2.1

**Problem:**

Section 11.2.1 describes a Redis stream failover path for billing events during Postgres unavailability. The design states:

> "Multiple gateway replicas write to the same tenant stream concurrently; the stream provides durable ordering across replicas. A background flusher goroutine per tenant polls the Redis stream and re-attempts Postgres INSERTs in `stream_seq` order..."

The critical gap: "a background flusher goroutine per tenant" does not specify which replica runs the flusher. If all `N` gateway replicas simultaneously have this goroutine active (the natural result of the description — every replica runs "a flusher goroutine per tenant"), then all `N` replicas will concurrently read from the same Redis stream and attempt to `INSERT` the same billing events into Postgres.

There is no described deduplication or mutual exclusion mechanism. The sequence number is assigned at INSERT time via `nextval('billing_seq_{tenant_id}')`, so two replicas inserting the same event would each receive a distinct sequence number and both rows would be committed — resulting in duplicate billing records for the same underlying billable event.

**Why `XDEL` does not solve this:** The flusher `XDEL`s the entry "after successful INSERT." With two replicas racing, both can read the entry before either deletes it. Both call `nextval()` and INSERT — the second `XDEL` is a no-op (entry already deleted), so no error surfaces. The duplicates exist silently in Postgres.

**Impact:** Tenants are billed for the same event multiple times. This is a hard billing correctness failure. It occurs exactly in the failure scenario (Postgres unavailability with Redis available) where billing accuracy is already under stress. It cannot be detected by the gap-detection mechanism because no sequence gap occurs — both duplicate events receive valid, consecutive sequence numbers.

**Recommendation:** Specify that the billing stream flusher is leader-elected — only one gateway replica per tenant runs the flusher goroutine at a time, using the existing leader-election lease mechanism (already used for the GC job in §12.5). Alternatively, use a Redis consumer group (`XGROUP CREATE` / `XREADGROUP`) so each stream entry is delivered to exactly one consumer across all replicas. The chosen approach must be specified explicitly. The `XDEL` must be atomic with confirmation that the INSERT was the first (e.g., using `INSERT ... ON CONFLICT DO NOTHING` with a unique constraint on the event's identity fields, or a Redis-based distributed lock around the flush-and-delete step).

---

## STR-029 Billing `sequence_number` "No Gaps Allowed" Guarantee Is False — Postgres Sequences Produce Gaps on Transaction Rollback [Medium]

**Section:** §11.2.1

**Problem:**

Section 11.2.1 states:

> `sequence_number` — "Monotonically increasing, per-tenant sequence number (**no gaps allowed**)"

And later:

> "Gateway replicas call `nextval('billing_seq_{tenant_id}')` inside the `EventStore` INSERT transaction; this **guarantees monotonicity and no-gap semantics** across replicas."

This is factually incorrect. Postgres sequences do not guarantee gapless incrementing. Postgres sequences intentionally trade gap-freedom for performance and correctness:

1. **Transaction rollback:** When a transaction calls `nextval()` and then rolls back (e.g., due to a constraint violation, deadlock, or application error in the same transaction as the billing INSERT), the sequence value is permanently consumed — no row is inserted, but the sequence has advanced. The next INSERT uses the next value, leaving a gap.

2. **Batching interactions:** The spec also describes billing event batching (`billingFlushBatchSize`, default: 50). If a batch transaction is partially successful or retried, gaps can appear.

3. **Flusher re-numbering:** The spec describes buffered events being "renumbered by the flusher on the actual Postgres INSERT." If a flusher transaction fails after calling `nextval()` and is then retried, the first consumed value is lost.

**The spec simultaneously:**
- Promises consumers "no gaps allowed" (field definition)
- Uses gap detection as the mechanism for consumers to detect missing events: "Consumers detecting a gap in the `sequence_number` stream may request a replay via `GET /v1/metering/events?since_sequence={N}`"

These two properties are contradictory: if gaps are truly forbidden, the gap-detection replay mechanism is vestigial; if gaps can occur (the technical reality), the "no gaps allowed" promise to consumers is false and may cause consumers to never request replay for legitimately missing events (believing gaps are impossible) or to continuously request replays for normal operational gaps.

**Impact:** External consumers (billing integrations, invoice generators, audit tools) that take "no gaps allowed" at face value may fail silently on operational gaps, or may treat every gap as a data integrity violation requiring escalation, creating alert fatigue. The semantic contract is wrong.

**Recommendation:** Change the field description from "no gaps allowed" to "monotonically increasing; gaps indicate lost events and should trigger replay." Separately, document the known Postgres sequence gap scenarios (rollback, flusher retry) and clarify that the gap-detection replay mechanism is the correct response to any observed gap. If true gapless sequencing is required for a specific consumer, use an application-level sequence counter maintained in a serialized transaction rather than a Postgres sequence.

---

## STR-030 `erasure_salt` Rotation Procedure Is Internally Contradictory [Medium]

**Section:** §12.8

**Problem:**

The `erasure_salt` rotation description in Section 12.8 contains a direct internal contradiction. The prose states:

> "On rotation, the old salt is **not** retained in `previous_erasure_salts` — it is **deleted immediately**. Re-hashing historical billing records with the new salt is required if the tenant needs continued internal consistency across the billing event timeline; a one-time re-hash migration job **re-pseudonymizes all billing events under the new salt before the old salt is deleted**."

The same paragraph both (a) deletes the old salt immediately and (b) requires the old salt to exist until the re-hash migration job completes. The re-hash migration job must read the old pseudonymized billing records and re-hash them: to do this, it needs the old salt (to verify/track which records were already pseudonymized with it) **and** the new salt (to re-pseudonymize). If the old salt is deleted immediately on rotation, the migration job cannot determine which hash values correspond to which user IDs — making the re-pseudonymization impossible.

The Section 15.1 API table repeats the contradiction:

> "The old salt is **deleted immediately** upon rotation (not retained); a one-time re-hash migration job re-pseudonymizes historical billing records under the new salt **before deletion**."

"Deleted immediately" and "before deletion" describe mutually exclusive states at the same point in time.

**Impact:** This is a specification error that will lead to one of two incorrect implementations:
1. The old salt is truly deleted before re-hashing, making the migration job fail (it cannot re-pseudonymize records without the old salt).
2. The old salt is retained during re-hashing, but the spec's "GDPR compliance" argument (salt deletion renders records anonymous) is violated for the duration of the migration — which may be hours or days for large tenants.

**Recommendation:** Define an explicit ordered procedure:
1. Generate and store the new salt.
2. Run the re-hash migration job (atomically re-pseudonymize all billing events from old-salt hash to new-salt hash, within a single transaction or idempotent batched transactions).
3. Delete the old salt only after migration completes and is verified.
4. Record the migration completion and old salt deletion as separate audit events.

The re-hash migration window (step 2 to step 3) carries compliance risk (both salts exist simultaneously); specify an SLA for migration completion (analogous to the erasure SLAs in §12.8) and require the old salt to be stored in the same access-controlled, KMS-encrypted form as the new salt during this window.

---

## STR-031 `ArtifactStore.DeleteByUser` Does Not Decrement the `storage_bytes_used` Redis Quota Counter [Medium]

**Section:** §11.2 (Storage quota enforcement mechanism), §12.8 (Data erasure)

**Problem:**

Section 11.2 defines the three-step lifecycle for the per-tenant `storage_bytes_used` Redis counter:

1. Pre-upload size check (reads counter)
2. Post-upload increment (increments counter after successful MinIO write)
3. **GC-triggered decrement** (decrements counter when GC job deletes an artifact from MinIO)

The storage quota enforcement mechanism defines GC as the **only path** for decrementing the counter.

Section 12.8 defines GDPR user-level erasure. The `ArtifactStore` is in the erasure scope:

> `ArtifactStore` | MinIO | "Workspace snapshots, checkpoints, uploaded files, session transcripts"

When the erasure job calls `ArtifactStore.DeleteByUser(user_id)`, it deletes user-owned MinIO objects and marks the corresponding `artifact_store` Postgres rows as `deleted`. This deletion path is not GC — it bypasses the GC-triggered decrement step entirely. Nowhere in the erasure procedure, the erasure scope table, or the `QuotaStore` erasure entry ("Per-user rate-limit counters and budget tracking") is a corresponding decrement to `storage_bytes_used` specified.

**Impact:** After a user's artifacts are deleted by the erasure job, the tenant's `storage_bytes_used` Redis counter continues to include the deleted bytes. The storage quota for the tenant is overstated until the next Redis restart (at which point the gateway rehydrates from the sum of `artifact_size_bytes` across active Postgres rows, which correctly excludes deleted rows). In the interim, new upload requests from other users in the same tenant may be incorrectly rejected with `STORAGE_QUOTA_EXCEEDED`, even though actual storage consumption is below the quota.

For large tenants where a single user had significant artifact storage (e.g., 50GB of checkpoints), this can cause significant disruption to the entire tenant's upload capacity until Redis is restarted — a restart that is not triggered automatically by the erasure job.

**Recommendation:** Add an explicit step to `ArtifactStore.DeleteByUser` (and `DeleteByTenant`) to atomically decrement the `storage_bytes_used` Redis counter by the sum of `artifact_size_bytes` for the deleted artifacts. This can be implemented as a single `DECRBY` (or Lua script for atomicity) using the sum queried from `artifact_store` before the rows are marked deleted. Add this to the erasure scope table as a note: "`ArtifactStore` deletion MUST atomically decrement `storage_bytes_used` in `QuotaStore` by the sum of deleted artifact sizes." Add a corresponding integration test.
