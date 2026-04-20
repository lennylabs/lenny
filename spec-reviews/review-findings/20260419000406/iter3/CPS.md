# Iter3 CPS Review

**Review Date:** 2026-04-19
**Reviewer:** Claude Opus 4.7
**Perspective (CPS):** Checkpoint manifests, snapshot barriers, resume semantics, workspace-tar encoding, MinIO storage, partial-manifest recovery, CheckpointBarrier protocol, cross-region replication.
**Prior findings reviewed:** `iter1/CPS.md` (CPS-001, competitive positioning — out of scope for this category code in iter3), `iter2/CPS.md` (CPS-002, out of scope for iter3).

## Status of iter2 fixes (FLR-002, FLR-004, CMP-045)

All three regression-check targets are **present and coherent**:

- **`spec/10_gateway-internals.md` §10.1 preStop Postgres-unreachable fallback (FLR-004):** New paragraph at line 110 specifies the in-replica cache mirror of `last_checkpoint_workspace_bytes`, cache-hit uses the tiered value, cache-miss selects the 90s maximum tier, and the stream-drain clamp still applies after tier selection. The cache-maintenance invariant ("updated on every successful checkpoint, immediately after the Postgres write") is stated. Consistent with §4.4 checkpoint SLOs and §12.3 Postgres failover window.
- **`spec/12_storage-architecture.md` per-region backup pipeline (CMP-045):** Per-region KMS/MinIO/credentials structure (`backups.regions.<region>.*`), `BackupRegionUnresolvable` fail-closed, per-shard dump routing, and restore region symmetry are all internally consistent. Checkpoint residency is correctly subsumed under the per-region pipeline — a checkpoint's MinIO object at `/{tenant_id}/…` lands in the same region as the tenant's runtime shard.
- **`spec/17_deployment-topology.md` §17.1 + §17.8.2 (FLR-002):** MinIO throughput budget at line 1078 and Gateway PDB `maxUnavailable: 1` at line 7 compose correctly. With the PDB capping concurrent draining replicas to one, the per-replica 400-pod drain burst stays within the 8.5 GB/s ceiling (400 × 512 MB / 90s ≈ 2.3 GB/s); the 8 GB/s burst ceiling is dominated by the steady-state 17/s × 512 MB calculation, not drain bursts.

No regressions detected in any of the three fix areas.

## New issues

### CPS-003 `partial_recovery_threshold_bytes` configuration surface is undefined [LOW]

**Files:** `spec/10_gateway-internals.md:122`

The Partial manifest on checkpoint timeout paragraph introduces `partial_recovery_threshold_bytes` as "configurable, default: 50% of last full checkpoint size" but does not specify the configuration surface: no Helm value name, no CRD field, no admin-API endpoint. Every other adjacent tunable in the same subsection is explicitly anchored to a surface (e.g., `periodicCheckpointIntervalSeconds` → Helm value, `checkpointBarrierAckTimeoutSeconds` → pool config, `workspaceSizeLimitBytes` → pool-level configuration). Without an explicit anchor, operators cannot deterministically tune partial-recovery aggressiveness, and the CRD-validation rule for partial-recovery thresholds (if any) cannot be specified.

Additionally, "50% of last full checkpoint size" is a per-session dynamic quantity, not a static threshold — the spec does not state whether the 50% is computed at checkpoint-start time (stored in the partial manifest) or at resume time (looked up from the last full checkpoint record). If the last full checkpoint is garbage-collected between the partial write and the resume (e.g., session TTL expired during an outage), the threshold is undefined.

**Recommendation:** (1) Name the Helm value — suggest `gateway.partialRecoveryThresholdFraction` (a fraction 0.0–1.0) with default 0.5, or make it a pool-level field if per-pool tuning is warranted. (2) State explicitly that the threshold bytes value is computed at checkpoint-start time using `last_checkpoint_workspace_bytes` (the same field used by the preStop tiered-cap selection) and stored in the partial manifest row so that resume-time recovery is self-contained and unaffected by subsequent GC of the prior full checkpoint.

---

### CPS-004 Partial-manifest reassembly semantics are ambiguous (multipart vs. separate objects) [MEDIUM]

**Files:** `spec/10_gateway-internals.md:122`, `spec/04_system-components.md:234,236`

The partial-manifest description states that the gateway records `partial_object_keys` as "list of MinIO object keys for any multipart parts already uploaded" and that on resume the coordinator "attempts to reconstruct the workspace from the partial upload by listing and reassembling committed multipart parts." This conflates two distinct S3/MinIO upload models:

1. **Multipart upload (`CreateMultipartUpload` + `UploadPart` + `CompleteMultipartUpload`):** Parts uploaded via `UploadPart` are referenced by an `UploadId` + part number and are NOT independently readable via `GetObject`. They are only readable after `CompleteMultipartUpload` assembles them into a single object. `ListParts` enumerates them by part number for a given `UploadId`; you cannot "list and reassemble" them from an object-key list because they do not have independent object keys. If the gateway timed out before `CompleteMultipartUpload`, the partial manifest must record `UploadId` (not object keys), and resume must call `CompleteMultipartUpload` with the parts that uploaded successfully — which produces a truncated-but-readable object.

2. **Separate-object pseudo-parts (workspace tar chunked into N MinIO objects written with `PutObject`):** Each chunk is an independent object with its own key and is independently readable. "Reassembly" means downloading all N keys and concatenating them, then un-tarring. This matches the "list of MinIO object keys" wording but implies the adapter's upload path is chunked-PutObject, not multipart.

The spec uses language from both models ("multipart parts" + "object keys"), and §4.4 line 236 cleanup mentions both `AbortMultipartUpload` (for model 1) and `DeleteObject` (for model 2) as equivalent options — but these are NOT equivalent: the correct cleanup API depends on which upload path produced the partial data. Runtime adapters and the GC sweep will need one or the other; they cannot implement both behaviors simultaneously without the spec telling them which is in use.

This ambiguity also affects CPS-003's threshold evaluation — `workspace_bytes_uploaded` is a natural quantity in model 2 (sum of written chunk sizes) but less natural in model 1 (sum of `UploadPart` content lengths, which are parts not yet assembled into a readable tar).

**Recommendation:** Pick one model and state it explicitly in §4.4 **Checkpoint Atomicity** and §10.1 **Partial manifest**. Recommended: **model 1 (multipart with `UploadId`)** because MinIO multipart uploads are the idiomatic way to upload a large single-object tar with concurrent part uploads and the existing atomicity model ("metadata record written only after both artifacts upload") already implies a single terminal object. If model 1 is chosen: the partial manifest records `multipart_upload_id` + `completed_parts: [{partNumber, etag}, ...]`; resume calls `CompleteMultipartUpload` with the completed-parts list to produce a truncated readable tar, then un-tars and validates that the truncation boundary lies on a tar-member boundary (workspaces where the last member is partially uploaded fall back to the last full checkpoint). If model 2 is chosen: update §4.4 line 236 cleanup to remove the `AbortMultipartUpload` mention (it would be inapplicable) and remove "multipart parts" phrasing from §10.1 line 122.

---

### CPS-005 Partial-manifest truncation lies on arbitrary byte offset, not tar-member boundary [MEDIUM]

**Files:** `spec/10_gateway-internals.md:122`

Assuming either reassembly model, the reconstructed workspace is a tar archive truncated at an arbitrary byte offset (the boundary between the last completed multipart part and the next un-uploaded part). A tar archive truncated mid-member produces an error when un-tarring the final member — standard tar readers either error out on the final member or silently emit a partial file with incorrect metadata. The spec says "if reassembly succeeds, the session resumes with the partial workspace and the client receives a `session.resumed` event with `resumeMode: "partial_workspace"`" but does not address what "reassembly succeeds" means in the presence of mid-member truncation.

Without a defined truncation policy, a single corrupt-final-member tar could either: (a) fail to un-tar entirely (the session falls back to full-checkpoint resume, but `lenny_checkpoint_partial_total{recovered: true}` was already incremented based on the threshold check), (b) silently un-tar with a half-written file in the workspace (a correctness bug — the agent sees corrupt workspace state), or (c) un-tar up to the last complete member and drop the partial member (requires a custom tar reader).

**Recommendation:** Specify the truncation policy in §10.1 explicitly. Recommended: require the adapter to align multipart part boundaries with tar-member boundaries (flush each completed member as its own part). On reassembly, the truncation boundary is guaranteed to be on a tar-member boundary and the tar archive un-tars cleanly up to the last completed part. The `workspaceRecoveryFraction` reported in `session.resumed` is then defined as `bytes_recovered / last_full_checkpoint_bytes` where `bytes_recovered` counts only fully-completed tar members. Any pod that cannot guarantee member-aligned multipart parts (e.g., a pod using a non-tar workspace format) must be ineligible for partial-workspace resume and must set `partial: true` with `aligned: false`, which the coordinator interprets as "full-checkpoint fallback only."

---

### CPS-006 Partial-manifest resume path lacks coordinator_generation guard against split-brain [LOW]

**Files:** `spec/10_gateway-internals.md:122,134`

The partial manifest records `session_id` and `coordination_generation` (stated in line 122), and §10.1 line 134 says the resume-deduplication path reads `last_tool_call_id` from the checkpoint manifest as a fallback. However, the partial-manifest resume flow in line 122 does not explicitly gate the partial-reassembly decision on a `coordination_generation` match. Consider the scenario:

1. Replica A owns session S at `coordination_generation = 5`, writes a partial manifest at `generation = 5` during preStop timeout.
2. Before replica A's Postgres write commits (or during a Postgres failover that drops the write), replica B acquires the coordinator lease and increments to `generation = 6`, runs a full checkpoint at `generation = 6`, then also drains and writes a partial manifest at `generation = 6`.
3. On resume, replica C sees both partial manifests. If C selects "the latest checkpoint record" and the Postgres timestamp ordering happens to favor replica A's late-committed generation-5 partial over replica B's generation-6 partial, C reconstructs from a stale-coordinator partial upload that post-dated a full checkpoint, silently regressing workspace state.

The atomicity claim ("writes a partial checkpoint manifest to Postgres before proceeding") is necessary but not sufficient — without a `max(coordination_generation)` filter, late-committed older writes can win against timestamp-ordered selection.

**Recommendation:** Add to §10.1 line 122: "The resume coordinator selects the partial manifest with the highest `coordination_generation` for the session; partial manifests with `coordination_generation < max_observed_coordination_generation` for the session are ignored (and GC'd). If the highest-generation record is a `partial: true` row and there is a successful full checkpoint at the same or higher generation, the full checkpoint wins." This mirrors the fencing model already established in §4.4 eviction fallback (which uses `coordination_generation` for coordinator fencing on resume) and extends it to partial manifests.

---

### CPS-007 CheckpointBarrier fan-out during drain does not address recently-handed-off sessions [LOW]

**Files:** `spec/10_gateway-internals.md:130`

§10.1 line 130 specifies: "When the preStop hook flips readiness to `false`, it simultaneously sends a `CheckpointBarrier` control message to every pod currently coordinated by this replica." The phrase "currently coordinated by this replica" is read from the replica's in-memory coordinator-lease cache. A session that was handed off to replica B moments before this replica entered preStop (but for which this replica still holds a stale cache entry) will receive a `CheckpointBarrier` it should not receive — and conversely, a session that was just handed off TO this replica (fencing completed, but cache not yet updated) will be skipped.

In the second case the missed session proceeds without a checkpoint, and its next checkpoint will be delayed until the next `periodicCheckpointIntervalSeconds` on replica C (whoever picks it up post-drain). Data-durability impact is bounded by the periodic-checkpoint SLO (10 min), which matches the existing "at-most 10 minutes of workspace changes lost" bound in §4.4, so this is not a regression against the freshness SLO — but the `CheckpointBarrier` protocol's stated goal of "at-most-once tool call semantics across the update" (§10.1 line 136) does not hold for the just-handed-in session; a tool call dispatched by this replica moments before drain could be re-dispatched by replica B without the `last_tool_call_id` deduplication fence that the barrier would have produced.

**Recommendation:** Add a clarifying paragraph to §10.1 between lines 130 and 131: "The preStop barrier-target set is the snapshot of `coordination_lease` rows for this replica at the moment readiness flips to `false`, queried from Postgres (not the in-memory cache). This avoids both false-positives (sending `CheckpointBarrier` to sessions handed off moments earlier) and false-negatives (skipping sessions just handed in). If Postgres is unreachable when readiness flips, the replica falls back to its in-memory cache and emits the `lenny_prestop_barrier_target_source{source="cache"}` counter increment so operators can detect the degraded mode." This also composes with the CPS-006 generation-guard recommendation.

---

## Areas checked and clean

- **Checkpoint Atomicity (§4.4):** Single-commit-after-both-uploads rule + `partial: true` exception is explicit and non-conflicting with the normal atomicity guarantee.
- **Eviction fallback (§4.4):** `session_eviction_state` two-tier context (≤2KB inline vs. MinIO key), MinIO-unavailable truncation to 2KB, Postgres failover retry budget (60s), total-loss path (both stores unavailable), storage quota accounting for eviction context objects, and tenant RLS coverage all coherent.
- **`resumeMode` enum:** §7.2 event schema (`full | conversation_only | partial_workspace`), §4.4 eviction fallback (uses `conversation_only`), and §10.1 partial-manifest path (uses `partial_workspace`) are enum-consistent; `workspaceRecoveryFraction` is optional and only present for `partial_workspace` per §7.2 signature.
- **`last_tool_call_id` durability (§10.1 step 4):** Dual-sink storage (MinIO checkpoint manifest as primary + `session_checkpoint_meta` Postgres table as secondary) + resume-deduplication fallback path (`coordinator_resume_meta_source` label) is well-specified and survives Postgres unavailability during preStop.
- **CheckpointBarrier CRD validation (§10.1):** `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds` rejection + BarrierAck-floor rule `checkpointBarrierAckTimeoutSeconds >= max_tiered_checkpoint_cap` are consistent; admission-webhook scope explicitly covers PoolScalingController SSA applies (not subject to the userInfo-based bypass).
- **MinIO throughput budget (§17.8.2):** 10,000 concurrent sessions / 600s = 17/s steady-state; × 512 MB max workspace = 8.5 GB/s burst; 8-node NVMe MinIO at ~10–12 GB/s aggregate write = ~40% headroom. Drain-triggered burst (400 pods × 512 MB / 90s ≈ 2.3 GB/s per replica × `maxUnavailable: 1`) comfortably fits within the steady-state burst ceiling.
- **Per-region backup pipeline (§12.8):** Per-region KMS/MinIO/credentials structure, `BackupRegionUnresolvable` fail-closed, per-shard dump routing via `StorageRouter`, restore region symmetry, and post-restore GDPR erasure reconciler with receipt-replay invariants are self-consistent. Checkpoint residency is correctly a consequence of the runtime store's regional pinning (checkpoints live at `/{tenant_id}/…` in the tenant's `dataResidencyRegion` MinIO).
- **GC concurrency model (§12.5):** Single-writer leader election + `WHERE deleted_at IS NULL` guard + MinIO delete-on-absent idempotency + Redis-decrement-after-commit ordering + partial-manifest backstop sweep using the same guard composes correctly. No double-decrement or orphaned-parts possibility.
- **Legal-hold exemption:** "Latest 2 checkpoints" rotation exemption for `legal_hold = true` (§12.5 checkpoint retention policy) is consistent with §12.8 spoliation-avoidance requirement; no cap during hold is explicitly out-of-scope for v1.
- **Pre-drain MinIO health-check webhook (§12.5):** `lenny-drain-readiness` `failurePolicy: Fail` + `2-second HeadBucket` probe + `lenny.dev/drain-force` override + `lenny_drain_readiness_checks_total` instrumentation coherent.

---

## Summary

**New issues:** 5 (1 Medium × 2: CPS-004, CPS-005; 3 Low: CPS-003, CPS-006, CPS-007).
**Regressions from iter2:** None. All three iter2 fixes (FLR-004, CMP-045, FLR-002) are coherent and do not introduce new checkpoint-semantics gaps.
**Pre-existing, out-of-scope:** iter1 CPS-001 / iter2 CPS-002 are competitive-positioning findings under a different interpretation of the "CPS" code; the iter3 task scopes CPS to the checkpoint/snapshot perspective, so those findings are not relevant here.

The checkpoint semantics of the spec are in a mature state. The remaining issues are refinements of the partial-manifest recovery path (which is the newest and least-tested mechanism in the spec) and a precision improvement on the CheckpointBarrier fan-out during drain. None of the five new findings describe a critical or high-severity data-loss path; all five describe under-specified behavior where a reasonable implementation could converge on different semantics.
