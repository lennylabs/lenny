### STR-001 Artifact GC Strategy Specification Incomplete [Medium]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (lines 303–312), `/Users/joan/projects/lenny/spec/11_policy-and-controls.md` (quota reconciliation section)

The spec states clearly in Section 12.5 that **"No reference counting required in v1"** because "each MinIO artifact object is owned by exactly one session" and v1 uses session derivation with object copying. However, the spec specifies GC via TTL-based deletion but does not explicitly formalize the idempotency guarantees for concurrent GC invocations when the same artifact is being deleted by multiple leaders or when checkpoint rotation conflicts with TTL expiration. 

Specifically, the ordering guarantee (line 308) states: "The Postgres decrement MUST be issued only after the Postgres `artifact_store` row has been durably committed with `deleted_at` set." This is correct for crash recovery, but the spec does not document the concurrency protocol for:
1. Two concurrent GC cycles attempting to delete the same artifact
2. Concurrent deletion by both the "latest 2 checkpoints" rotation and TTL-based eviction on the same session

While these collisions are likely benign (MinIO delete-on-absent is idempotent, conditional Postgres updates are safe), the lack of explicit concurrency semantics creates ambiguity for implementers and makes correctness difficult to validate in tests.

**Recommendation:** Add a concurrency section to Section 12.5 explicitly documenting that the GC job is leader-elected per-tenant (or globally) and cannot run concurrently on the same artifact, and that checkpoint rotation and TTL-based deletion both use `WHERE deleted_at IS NULL` guards to prevent double-deletion. Alternatively, specify distributed locking (e.g., Postgres advisory locks scoped to `session_id`) to allow parallel GC cycles while ensuring artifact-level atomicity.

---

### STR-002 Checkpoint Scaling Metrics and Alerts Insufficient [Medium]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (lines 301–312), `/Users/joan/projects/lenny/spec/16_observability.md` (Section 5, alerts)

The spec mandates monitoring checkpoint storage via `lenny_checkpoint_storage_bytes_total` gauge (labeled by `tenant_id` and `pool`) and an alert `CheckpointStorageHigh` fires at an unspecified threshold. However, the spec does **not quantify** the alert threshold, per-tenant checkpoint quota limits, or scaling guidance for high-checkpoint-volume workloads.

This is critical because:
- Checkpoint objects are large (up to 500MB per checkpoint)
- Concurrent-workspace mode multiplies checkpoint count by `maxConcurrent` (up to 8 slots per pod, "latest 2" per slot = up to 16 checkpoints per pod)
- Legal holds accumulate all checkpoints indefinitely with no quota enforcement, creating unlimited storage growth risk

The spec acknowledges this in Section 12.8: "Storage growth for long-held sessions is accepted as a compliance cost; operators should monitor [...] and should allocate additional quota for tenants with active holds." But it provides **no operational limits** on checkpoint storage for non-held sessions, no guidance on when to trigger checkpoint pruning beyond the TTL, and no per-tenant storage quota limits for checkpoints (distinct from artifact storage quota).

**Recommendation:** Define and document:
1. A per-tenant checkpoint storage quota (separate from artifact storage quota) with a configurable limit (e.g., 10x the artifact quota for T3/T4)
2. Alert threshold for `CheckpointStorageHigh` (e.g., 80% of quota)
3. Scaling guidance for concurrent-workspace deployments (e.g., concurrent-workspace mode requires 16× the checkpoint quota of single-workspace mode)
4. Legal-hold storage limit enforcement or explicit opt-out mechanism requiring operator acknowledgment

---

### STR-003 MinIO SSE-KMS per-Tenant Key Provisioning Timing Unclear [Medium]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (line 297), `/Users/joan/projects/lenny/spec/17_deployment-topology.md` (preflight checks, Section 17.6)

Section 12.5 mandates that **T4 tenants must use SSE-KMS with a tenant-specific KMS key**, and states: "The preflight check [...] validates that MinIO SSE is enabled but **does not validate per-tenant KMS key existence** — this is validated at runtime when the first T4 artifact is written, returning `CLASSIFICATION_CONTROL_VIOLATION` if the tenant-scoped KMS key is unavailable."

This creates a **just-in-time provisioning requirement** that is not covered by the spec:
1. **Pre-provisioning:** Must deployers pre-create KMS keys for all T4 tenants before session creation? Or does the platform create keys on-demand?
2. **Timing window:** If a T4 tenant is created but their KMS key provisioning is pending in the KMS service (typical in cloud environments with eventual consistency), what happens to session checkpoints in that window? Are they rejected, deferred, or stored unencrypted?
3. **Rotation:** The spec mentions "per-tenant key rotation" but does not define the rotation API, the impact on in-flight reads/writes during rotation, or whether Lenny is responsible for scheduling rotations or if that's operator-driven.

This creates a durability and compliance gap: a session that cannot write its checkpoint due to missing KMS key is unrecoverable, but the spec does not specify whether the session is rejected at creation time or at checkpoint time.

**Recommendation:** Add a section documenting:
1. The KMS key provisioning model: require pre-provisioning, specify a pre-provisioning API, or document the on-demand creation flow and which component creates keys
2. The failure behavior when a KMS key is unavailable during checkpoint write (reject the session, defer the checkpoint with retries, or fail the checkpoint with session loss)
3. The key rotation procedure, including impact on in-flight reads and the scope of responsibility (platform-managed vs. operator-managed)

---

### STR-004 Redis Fail-Open Behavior for Rate Limits and Quota Diverges [Low]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (lines 205, 209, 220), `/Users/joan/projects/lenny/spec/11_policy-and-controls.md` (per-tenant fail-open section)

The spec defines two distinct fail-open behaviors for Redis unavailability:

1. **Rate limit counters** (line 205): "**Fail open with bounded window** — allow requests for up to `rateLimitFailOpenMaxSeconds` (default: 60s), then fail closed"
2. **Quota counters** (line 209): "**Fail open with per-replica budget ceiling** — enforce conservative per-tenant limits locally, then fail closed"

These descriptions are subtly inconsistent. The rate limit version uses a time window before failing closed. The quota version uses a per-replica ceiling for as long as the replica exists, then transitions to fail-closed only when the cumulative fail-open window exceeds `quotaFailOpenCumulativeMaxSeconds` (default: 300s). This means rate limits are more aggressive about closure (60s) while token quotas tolerate longer outages (300s cumulative), creating different risk profiles for the same resource (tokens are both rate-limited and quota-managed).

Section 11_policy-and-controls elaborates the quota behavior with a cumulative timer that persists across replicas via `failopen-cumulative.json`, but this file-based persistence mechanism is not mentioned in Section 12.4, creating a spec split.

**Recommendation:** Clarify in Section 12.4 whether rate limits and quota counters use the same fail-open window logic or distinct logic, and if distinct, justify the difference. Consolidate the cumulative-timer mechanism description into Section 12.4 to avoid duplication and divergence. Consider whether 300s cumulative quota fail-open is acceptable for T4 deployments (e.g., a 300s window × high throughput could allow significant overshoot).

