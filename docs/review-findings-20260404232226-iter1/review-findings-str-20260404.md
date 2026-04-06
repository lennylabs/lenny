# Technical Design Review Findings — Storage Architecture & Data Management
**Category:** STR  
**Document reviewed:** `docs/technical-design.md`  
**Review date:** 2026-04-04  
**Perspective:** 9. Storage Architecture & Data Management

---

## Findings Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 4 |
| Medium | 5 |
| Low | 3 |
| Info | 2 |

---

## Critical

### STR-001 Redis Quota Fail-Open Enables Deliberate Quota Bypass [Critical] — VALIDATED/FIXED
**Section:** 12.4, 11.2

The spec describes a 60-second fail-open window for quota counters when Redis is unavailable (§12.4). During this window, each gateway replica enforces only a per-replica ceiling (`tenant_limit / replica_count`). An adversary who can force Redis unavailability — even briefly — gains a guaranteed open window to consume up to N×(tenant_limit/N) = tenant_limit additional tokens *on every replica simultaneously*. Because the per-replica ceiling is computed from `tenant_limit / replica_count` but each replica acts independently, the aggregate worst case is the full `tenant_limit` (§11.2: "at most 1x the tenant's configured budget"). That means a sustained Redis outage lasting longer than the 60-second default can cause actual cumulative consumption of 2× the tenant limit before the cumulative timer kicks in (`quotaFailOpenCumulativeMaxSeconds` default: 300 s). The rate-limit path has an identical exposure: the "emergency hard limit" counter is per-replica and explicitly not shared (§12.4: "the effective limit is N * per_replica_limit"). For multi-tenant deployments, a single disrupted tenant could also pollute counters during reconciliation (§12.4: "reconciliation runs automatically... completes within seconds") without auditable attribution of the overshoot.

The spec acknowledges this risk (§11.2, "Maximum Overshoot Formula") but treats it as an accepted operational trade-off. For quota systems that gate cost-bearing operations (LLM tokens, pod-minutes) this is a financial security boundary, not just an availability trade-off.

**Recommendation:** Add a threat model classification distinguishing *availability degradation* fail-open (acceptable) from *financial security boundary* fail-open (requires additional controls). Specifically: (1) reduce the default fail-open window to 10 s (not 60 s) for `QuotaStore` in multi-tenant deployments, with the 60 s default reserved for single-tenant; (2) emit a `quota_failopen_started` audit event with tenant_id, replica_id, and timestamp when fail-open begins — this makes the overshoot window attributable and detectable by billing consumers; (3) document in §12.4 that `quotaFailOpenCumulativeMaxSeconds` is a *security control*, not just an operational knob, and that its default (300 s) should be reviewed per deployment's financial exposure.

**Resolution:** Section 12.4 was updated to: (1) clarify that `replica_count` for the per-replica budget ceiling is sourced from the Kubernetes Endpoints object via the API server (not Redis), with maximum staleness 30s and fallback to `replica_count = 1` when the Endpoints object is unavailable, (2) explicitly state the cumulative fail-open timer is a true sliding 1-hour rolling window (not calendar-aligned) that accumulates across all outages — five 59-second outages sum to 295 seconds, not zero, (3) clarify the timer is stored in-memory (not Redis) and resets to zero on gateway restart, (4) label `quotaFailOpenCumulativeMaxSeconds` as a financial security control with guidance to review the default per deployment, and (5) add a `quota_failopen_started` audit event emitted when fail-open begins.

---

### STR-002 Artifact GC Strategy Lacks Reference-Counting for Shared Blobs — Storage Leak Risk [Critical] — VALIDATED/FIXED
**Section:** 12.5, 12.8

Section 12.5 defines the GC lifecycle: "the GC job queries Postgres for artifacts past their TTL, deletes them from MinIO, then marks the rows as `deleted` in Postgres." Section 12.8 introduces workspace deduplication as a future optimization with this note: "if a future optimization introduces content-addressed deduplication for workspace snapshots in the `ArtifactStore`, the erasure job must use reference counting: a blob is only deleted when no other user's artifact references it."

The problem is that the current design already creates implicit shared-blob scenarios *without deduplication* through session derivation (§7.1: `POST /v1/sessions/{id}/derive`). When a derived session is created, the spec says the gateway "creates a new session pre-populated with the previous session's workspace snapshot." If the implementation reuses the same MinIO object (copying by reference rather than duplicating the bytes) as an efficiency measure — a natural implementation choice for potentially 500 MB snapshots — then the GC job will delete an object still referenced by the derived session. The spec does not specify whether `derive` copies the artifact or references it, nor does it define what `parent_workspace_ref` (§4.5) referencing semantics mean for GC eligibility. This is an underspecified path that is highly likely to produce dangling references or premature object deletion.

Additionally, the GC job is described as "idempotent" because "MinIO delete-on-absent is a no-op" — but this only handles the case where an artifact was already deleted, not the case where an artifact that *should not yet be deleted* was deleted by a prior GC cycle because its Postgres row was past TTL while another row still referenced the same object.

**Recommendation:** (1) Explicitly specify in §4.5 and §7.1 whether `derive` creates a new MinIO object or adds a reference to an existing one. (2) If any path shares MinIO objects across artifact records, implement reference counting in the `artifacts` Postgres table immediately (not deferred) — a `reference_count` integer column with FK-scoped decrement-on-delete triggers is sufficient. (3) Make the GC job's deletion condition `WHERE reference_count = 0 AND expires_at < NOW()` rather than purely TTL-based. (4) Add an integration test that creates a derived session from a parent, expires the parent's retention window, runs GC, and verifies the derived session can still retrieve its workspace artifact.

**Resolution:** Sections 4.5, 7.1, 12.5, and 12.8 were updated to explicitly specify copy-on-derive semantics. `parent_workspace_ref` is now documented as a metadata lineage pointer only — it does not create a shared MinIO object reference. `POST /v1/sessions/{id}/derive` always performs a full byte copy into the derived session's own MinIO path (`/{tenant_id}/checkpoints/{derived_session_id}/...`). No MinIO objects are shared across sessions. TTL-based GC is safe because each artifact is owned by exactly one session. Reference counting is deferred to whenever content-addressed deduplication is added (§12.8).

---

## High

### STR-003 Checkpoint Storage Fails Silently on Eviction — No Fallback Storage Path [High]
**Section:** 4.4

The spec defines two checkpoint storage failure behaviors (§4.4): for non-eviction checkpoints, the adapter resumes the agent after retry exhaustion and logs the failure. For eviction checkpoints (preStop hook), "if all retries fail, the checkpoint is lost — there is no fallback storage." The only mitigation is a `CheckpointStorageUnavailable` critical alert and the possibility of resuming from a *previous* successful checkpoint if one exists.

For sessions that have never successfully checkpointed (e.g., newly started sessions that have not yet triggered a periodic checkpoint), a MinIO outage during pod eviction causes **permanent, unrecoverable workspace loss** — the session cannot be resumed and the agent's in-flight work disappears. This is particularly acute for the 60-second retry window (§4.4: "initial 200ms, factor 2x, up to ~5 seconds total") combined with Kubernetes' `terminationGracePeriodSeconds` of 120 s. If MinIO is experiencing a multi-minute outage, the retry window (~5 s) is vastly shorter than the outage.

The spec acknowledges the checkpoint SLO as P95 < 2 s for ≤ 100 MB workspaces but does not bound the failure window. For Tier 3 deployments with many pods, a MinIO outage during a node drain could affect hundreds of sessions simultaneously.

**Recommendation:** (1) Add a "last-resort fallback" checkpoint path: on eviction retry exhaustion, the adapter should attempt to write a minimal checkpoint bundle (session metadata + conversation transcript, not full workspace) to an in-cluster ConfigMap or etcd-backed Secret (size-limited to 1 MB, sufficient for recovery metadata). This allows the gateway to at least reconstruct the conversation context even when workspace state is lost. (2) Increase the default retry total duration for eviction checkpoints to 30 s (not ~5 s), matching the typical MinIO transient outage profile. (3) Document in §4.4 that sessions with no prior checkpoint are **irrecoverable** on MinIO outage and surface this as a metric (`lenny_checkpoint_eviction_no_prior_total`, counter) so operators can size MinIO redundancy appropriately.

---

### STR-004 "No Shared RWX Storage" Non-Goal Not Validated Against Real Agent Workflows [High]
**Section:** 2 (Non-Goals), 4.5, 8.8

Section 2 lists "shared RWX storage mounts across agent pods" as a Non-Goal. Section 8.8 describes the file export model as the substitute: parents export specific file globs to children, which receive copies in their own `/workspace/`. However, the spec does not validate this against real agent workflows where parent-child coordination requires *live shared state* rather than one-time file snapshots.

Concrete gaps identified:

1. **Iterative parallel work:** Multiple child agents working on different parts of a codebase and producing incremental outputs cannot share a live workspace. The parent must explicitly collect each child's exports, merge them, and redistribute — a manual orchestration burden that the spec does not provide tooling for (§19, Decision 9: "parents use the existing export→re-upload flow via `delegate_task` file exports. Simpler; avoids a new gateway primitive").

2. **Large build artifacts:** A child that produces a 500 MB compiled artifact cannot efficiently pass it to a sibling — the gateway must export from child A's MinIO, re-import to child B's pod, consuming bandwidth and latency twice. The `fileExportLimits` (§8.3: default `maxTotalSize: 100MB`) would silently block this workflow.

3. **Concurrent-workspace mode has implicit shared state:** The `/workspace/shared/` directory (§6.4, concurrent-workspace layout) is described as "optional read-only shared assets (populated once at pod start, immutable)." This is not enforced — there is no mechanism ensuring the runtime binary doesn't write to it, and the spec doesn't describe who creates it or how it interacts with the "no RWX" non-goal.

**Recommendation:** (1) Add a validation section (or appendix) documenting 3-5 representative real-world agent workflow patterns and explicitly mapping each to the platform's capabilities — this makes the Non-Goal a reasoned design boundary rather than an unchecked assumption. (2) For the concurrent-workspace `/workspace/shared/` path, specify how it is populated (gateway-materialized before any slot starts), enforce read-only mount at the container level (not just convention), and clarify that writes silently fail or cause errors. (3) Increase the default `fileExportLimits.maxTotalSize` to 500 MB to match the stated workspace size SLO (§4.4) and explicitly document the bandwidth cost of the export-reimport pattern so operators can size MinIO I/O accordingly.

---

### STR-005 Checkpoint Scaling Under Long-Running Sessions — Unbounded Storage Growth [High]
**Section:** 4.4, 12.5

The checkpoint retention policy (§12.5) keeps "only the latest 2 checkpoints per active session." For sessions at maximum age (7200 s, §11.3) with periodic checkpoints, this correctly bounds per-session checkpoint storage. However, several cases create unbounded growth:

1. **Concurrent-workspace mode:** Section 5.2 states "checkpoints are per-slot." A pod with `maxConcurrent: 8` running for 7200 s generates up to 8 independent checkpoint streams. The "latest 2 per session" policy is defined for sessions (§12.5), not per-slot. It is unspecified whether the 2-checkpoint limit applies per slot (8 × 2 = 16 checkpoints) or per pod. If per pod, the policy doesn't account for independent slot lifecycles.

2. **Session derivation chains:** A derived session (§7.1, `POST /v1/sessions/{id}/derive`) retains `parent_workspace_ref` linking to the parent's workspace snapshot. When the parent session expires, its checkpoint retention window governs whether its artifacts are deleted (§12.5: "delete all checkpoints when session terminates and resume window expires"). But if the child is still active and its workspace was seeded from the parent's checkpoint, deleting the parent's artifact could invalidate the child's recovery path. This interaction is not addressed.

3. **Legal holds (§12.8):** Sessions under legal hold bypass the GC job entirely ("artifacts are not deleted by the GC job regardless of TTL"). For sessions with active legal holds and periodic checkpointing, storage grows unbounded at `checkpoint_size × checkpoints_per_hour × hold_duration`. This is acknowledged nowhere in the spec.

**Recommendation:** (1) Explicitly scope the "latest 2 checkpoints" policy to per-slot in concurrent-workspace mode. (2) Add a GC interaction note in §12.8 legal hold documentation: legal holds should freeze the *session record* (preventing erasure) but should still allow replacement of older checkpoints with newer ones ("retain latest N, regardless of hold, to bound storage"). (3) Add `lenny_checkpoint_storage_bytes_total` (gauge, per tenant) to the metrics list (§16.1) and include a `CheckpointStorageHigh` warning alert in §16.5 that fires when per-tenant checkpoint storage exceeds a deployer-configurable threshold.

---

### STR-006 Data-at-Rest Encryption Completeness — Disk-Backed emptyDir Unaddressed for Workspace [High]
**Section:** 6.4, 12.5, 12.9

Section 6.4 specifies that `/workspace/` and `/artifacts/` use disk-backed emptyDir (not tmpfs) and states: "Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is **required** for production deployments." This is a deployment requirement, not an architectural control — Lenny cannot enforce it, verify it, or detect its absence. 

The data classification table (§12.9) classifies workspace files as T3 — Confidential with "Required (storage-layer, SSE-KMS)" encryption, and session transcripts similarly. Yet the disk-backed emptyDir path is not encrypted at the application layer — it relies entirely on node-level encryption which:

- Is not checked by the preflight Job (§17.6) — there is no "node disk encryption" check in the preflight table
- Is not monitored or alerted on (§16.5) — no alert for unencrypted node storage
- Cannot be verified per-session — a misconfigured node pool silently exposes T3/T4 data

For regulated workloads (PHI, financial data) where tenants elevate to `workspaceTier: T4` (§12.9), the spec requires "envelope encryption via KMS" for T4 data, but the workspace files on disk-backed emptyDir receive only node-level encryption — not application-layer envelope encryption. This creates a classification control gap.

**Recommendation:** (1) Add a preflight check (§17.6) that validates node-level disk encryption is enabled on the cluster's node pools — this is detectable via cloud provider APIs (AWS: `encrypted: true` on EBS volumes, GCP: CMEK or default encryption on persistent disks). (2) For `workspaceTier: T4` tenants, mandate that workspace snapshots stored in MinIO use SSE-KMS with a tenant-specific KMS key (not just the default MinIO key), so that key rotation or revocation provides cryptographic erasure. (3) Add a `NodeDiskEncryptionUnverified` warning alert that fires when Lenny cannot confirm node-level disk encryption is active, distinct from the preflight check (which runs at install time, not continuously).

---

## Medium

### STR-007 Artifact Retention TTL Extension — No Audit Trail or Rate Limiting [Medium]
**Section:** 7.1, 12.5

Section 7.1 mentions: "Clients can extend retention on specific sessions via `extend_artifact_retention(session_id, ttl)`." This operation is not in the REST API table (§15.1) and has no formal schema defined. More importantly:

1. There is no audit event defined for retention extensions (§11.7 lists denial reasons, token usage, etc., but not data retention changes).
2. There is no rate limit or maximum TTL extension cap — a client could call this repeatedly to retain artifacts indefinitely without paying the storage cost.
3. The interaction with legal holds is undefined — can a client un-extend a TTL below the legal hold floor? Can a client extend past a pending GDPR erasure request?

**Recommendation:** (1) Add `extend_artifact_retention` to the REST API surface (§15.1) with a formal request schema, cap the `ttl` parameter at a deployer-configurable maximum (default: 30 days beyond current expiry), and limit calls to N per session per day. (2) Add `artifact.retention_extended` to the audit event taxonomy (§11.7). (3) Define interaction rules with legal holds: retention extension cannot reduce TTL below the legal hold floor, and a pending erasure request supersedes extension requests after the GDPR erasure window (72 hours, per §12.9).

---

### STR-008 MinIO Versioning Requirement Underspecified for Checkpoint Safety [Medium]
**Section:** 12.5

Section 12.5 states: "Enable bucket versioning for checkpoint objects to prevent accidental overwrites." Bucket-level versioning in MinIO (and S3) preserves overwritten objects as older versions, but the GC job operates on Postgres metadata rows that reference *current* object versions. If a checkpoint upload partially fails mid-write (e.g., a network interruption after the first multipart part), MinIO may store a partial object as the current version while the Postgres row records it as complete.

Additionally, the "keep latest 2 checkpoints" policy (§12.5) relies on the GC job deleting older checkpoint objects by their Postgres-recorded paths. With versioning enabled, deleting an object key leaves the version history intact — the GC job must explicitly request `DeleteObject` with `versionId` to remove the storage. If it issues a bare `DeleteObject` (adding a delete marker), the object's bytes are not reclaimed, and storage grows unbounded.

**Recommendation:** (1) Specify that the GC job must use versioned deletion (`DeleteObject` with `versionId`) when bucket versioning is enabled, not just bare key deletion. (2) Add a lifecycle policy on the MinIO/S3 bucket that automatically expires non-current versions after 24 hours as a backstop against GC job failures. (3) For checkpoint uploads, use MinIO's multipart upload with client-side hash verification and abort the multipart upload on failure — do not let partial uploads become the current object version.

---

### STR-009 Redis Key Prefix Enforcement — No Runtime Validation Path [Medium]
**Section:** 12.4

Section 12.4 mandates: "All Redis keys **must** use the prefix `t:{tenant_id}:` ... enforced in the Redis wrapper layer; no raw Redis command may be issued without the tenant prefix. An integration test (`TestRedisTenantKeyIsolation`) must verify..." 

The enforcement is purely convention-based in application code. There is no Redis-level enforcement mechanism (ACLs restricting key patterns are available in Redis 6.0+ via ACL rules with key patterns). An injection via a plugin, new code path, or deserialization vulnerability that bypasses the wrapper layer would silently write unprefixed keys that are (a) invisible to the GC sweep, (b) not scoped to GDPR erasure (`DeleteByUser`/`DeleteByTenant` in §12.8 both rely on prefix-scoped operations), and (c) potentially cross-tenant readable by any replica.

**Recommendation:** (1) Add Redis ACL rules (`~t:*` key pattern on the application user account) so the Redis server itself rejects any write to an unprefixed key — this makes the enforcement a server-level control, not just a code convention. (2) Add a startup check alongside the Postgres grant verification (§11.7) that verifies the Redis application user cannot write to an unprefixed key (attempt `SET test_key value`, expect `NOPERM` error). (3) Ensure the GDPR `DeleteByTenant` operation in §12.8 uses `SCAN` with the `MATCH t:{tenant_id}:*` pattern — document this explicitly since a naive key enumeration would miss all data.

---

### STR-010 Semantic Cache (Redis) Stores T3 Data Without Field-Level Encryption [Medium]
**Section:** 12.9, 4.9

The data classification table (§12.9) classifies "Semantic cache entries" as T3 — Confidential stored in "Redis (or pluggable)." The T3 controls require "Required (storage-layer, SSE-KMS)" encryption at rest, but the Redis `SemanticCache` stores LLM prompt/response pairs which may contain workspace content, user data, or session transcripts — all T3 data.

Section 12.4 specifies that "access tokens are encrypted before storage in Redis" using AES-256-GCM, but this applies to `TokenStore` credential caches specifically. There is no corresponding requirement for the `SemanticCache` to encrypt its entries before writing to Redis. Redis data is described as "ephemeral" (§12.4: "every Redis-backed role has a durable fallback or reconstruction path"), which is used to justify lighter-than-Postgres encryption requirements — but semantic cache entries may contain verbatim user prompts and agent responses, making them as sensitive as session transcripts (also T3).

**Recommendation:** Extend the Redis application-layer encryption requirement (§12.4: AES-256-GCM with key derived from the Token Service's envelope key) to cover `SemanticCache` entries. The `CachePolicy` schema (§4.9) should include a `encryptEntries: true` field (default `true` for multi-tenant deployments). For cloud-managed Redis deployments, verify that provider encryption at rest covers in-memory data or explicitly document that this is not the case and application-layer encryption is therefore essential.

---

### STR-011 EventStore Partition Maintenance — No Alert for Aging Partitions [Medium]
**Section:** 16.4, 12.3

Section 16.4 describes EventStore partition management: "A background job drops partitions beyond the retention window: 90 days for audit events, 30 days for session logs, 7 days for stream cursors." The job is described but its operational characteristics are not specified:

- Who runs it? (The GC job in §12.5 is described as a "leader-elected goroutine inside the gateway process" — but EventStore partition management is mentioned separately in §16.4 without the same leader-election guarantee.)
- What happens if it fails? (The artifact GC job has an error counter and retry logic; the EventStore partition job has neither defined.)
- At Tier 3 (§17.8: "~3 GB log volume/day"), a 90-day audit partition contains ~270 GB. Dropping a partition is a DDL operation that can cause lock contention on Postgres under load.

**Recommendation:** (1) Explicitly assign EventStore partition maintenance to the same leader-elected GC goroutine in the gateway (§12.5) or to a dedicated CronJob — do not leave the executor ambiguous. (2) Add `lenny_eventstore_partition_age_days` (gauge, per partition type) to the metrics table (§16.1) and a `EventStorePartitionStale` warning alert that fires when the oldest live partition is approaching its retention boundary without being dropped. (3) Document that partition drops (`DROP TABLE partition_name`) should be scheduled during low-traffic windows and that Postgres `pg_try_advisory_lock` should gate the drop to prevent concurrent execution.

---

## Low

### STR-012 Workspace Lineage (`parent_workspace_ref`) Not Exposed in API [Low]
**Section:** 4.5, 15.1

Section 4.5 defines workspace lineage: "The session record tracks lineage via a `parent_workspace_ref` field that links to the workspace snapshot that seeded the session." This enables "lineage queries such as 'which sessions were derived from this workspace?' and 'what was the workspace history for this session?'" However, no API endpoint exposes this data. The REST API table (§15.1) lists `GET /v1/sessions/{id}` (session status and metadata) and `GET /v1/sessions/{id}/tree` (delegation task tree) but nothing for workspace lineage.

**Recommendation:** Add `GET /v1/sessions/{id}/workspace/lineage` returning the chain of `parent_workspace_ref` records for the session, or include `parentWorkspaceRef` and `derivedFromSessionId` in the `GET /v1/sessions/{id}` response body. This is primarily a usability gap for operators tracing workspace provenance.

---

### STR-013 Local Dev Mode Artifact Storage — No Size Limit on `./lenny-data/` [Low]
**Section:** 17.4

Section 17.4 (Tier 1 `make run`) uses a "Local filesystem directory (`./lenny-data/`) replaces MinIO for artifact storage." For development use cases that exercise the full session lifecycle (including checkpointing), this directory can accumulate workspace snapshots, logs, and session transcripts without bound. There is no mention of a GC cycle equivalent for local mode, nor a size cap.

**Recommendation:** Apply the same GC logic (§12.5) in local dev mode, with a shorter cycle interval (e.g., 5 minutes) and a hard size cap on `./lenny-data/` (e.g., 5 GB) that triggers a warning log when reached. This prevents "disk full" surprises for contributors running long test sequences.

---

### STR-014 Billing Event Write-Ahead Buffer Not Persisted — Crash Recovery Gap [Low]
**Section:** 11.2.1

Section 11.2.1 describes the billing event write-ahead buffer: "If the buffer fills before Postgres recovers, the gateway rejects new session-progressing requests (returning `503`). The buffer is not persisted to disk — if a gateway replica crashes with buffered events, those events are reconstructed from pod-reported token usage during session recovery (§7.3)."

The reconstruction path relies on pods re-reporting usage on reconnection. However, if the gateway replica crashes and the pod also crashes (e.g., node failure), both sides of the reconnect are unavailable. In this case, the in-flight billing events for active sessions are permanently lost. For regulated billing environments (§12.9 T3 — Confidential), this represents an unacknowledged data loss path.

**Recommendation:** (1) Document this specific failure mode explicitly in §11.2.1 as a known gap: "gateway crash + simultaneous pod crash during Postgres unavailability results in permanent billing event loss for in-flight sessions on the affected pods." (2) Consider writing the write-ahead buffer to the Redis instance (itself HA with Sentinel) as a secondary buffer, using the existing Redis tenant-keyed pattern (`t:{tenant_id}:billing:wal:{seq}`). This adds one durable hop without requiring Postgres availability. (3) Add `lenny_billing_walbuffer_size` (gauge) to the metrics table and an alert when the buffer exceeds 50% capacity (`billingWriteAheadBufferSize / 2`).

---

## Info

### STR-015 Incremental Checkpoints Deferred — Impact on Long-Running Sessions Not Quantified [Info]
**Section:** 4.4

Section 4.4 notes: "Incremental checkpoints (diffing against the previous snapshot) are deferred but noted as the primary mitigation if the SLO cannot be met at larger workspace sizes." The checkpoint duration SLO (P95 < 2 s for ≤ 100 MB) is well-defined, but there is no guidance on what happens for sessions that naturally accumulate > 100 MB workspaces over their 7200 s lifetime.

For realistic agent workloads (e.g., a coding agent that clones a repo, runs builds, and accumulates logs), workspace sizes can reach 500 MB+ well within the session lifetime. At the spec's estimate of "~1 second per 100MB," a 500 MB workspace checkpoint takes 5-10 seconds — meaning a Full-tier session pauses for up to 10 s per checkpoint. With periodic checkpoints (frequency not defined in the spec), this could impose multi-second pauses on a recurring basis.

**Recommendation:** Define the default periodic checkpoint interval and document expected pause durations at various workspace sizes. Consider making checkpoint pause duration a SLO signal (`lenny_checkpoint_pause_seconds` histogram) with a deployer-configurable alerting threshold, so operators can detect sessions where workspace growth is causing unacceptable pause latency before users complain.

---

### STR-016 `MemoryStore` Backend Technology Deferred — No Durability Guarantees Defined [Info]
**Section:** 9.4

Section 9.4 describes the `MemoryStore` interface backed by "Postgres + pgvector" by default and notes: "Technology choice explicitly deferred — the memory layer market is not settled as of Q1 2026." The data classification table (§12.9) classifies memory store contents as T3 — Confidential. However, the `MemoryStore` interface has no defined durability contract: there is no RPO/RTO for memory data, no backup requirement, no specification of whether `Write` is synchronous to durable storage before returning, and no GC or retention policy for stale memories.

**Recommendation:** Add a durability contract to the `MemoryStore` interface documentation: minimum requirement that `Write` is acknowledged only after the memory is persisted to durable storage (not just in-memory cache), a default memory retention policy (e.g., 90 days per user, configurable), and a `DeleteByUser` compliance path consistent with the §12.8 erasure scope table (which already includes `MemoryStore`). This is preparatory work that makes the interface contract implementable by third-party backends without requiring Lenny to commit to a specific technology.
