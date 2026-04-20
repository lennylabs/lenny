### STR-005 `CLASSIFICATION_CONTROL_VIOLATION` referenced but not defined in §15.1 catalog [High]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (§12.5, lines 297, 301, 303), `/Users/joan/projects/lenny/spec/15_external-api-surface.md` (§15.1 error catalog, lines 541–633)

The iter1 STR-003 fix cites `CLASSIFICATION_CONTROL_VIOLATION` three times in §12.5:

1. **Runtime write rejection** (line 297): returned "if the tenant-scoped KMS key is unavailable" at first-artifact-write time.
2. **Admin-time T4 promotion probe** (line 301): "the update is rejected with `CLASSIFICATION_CONTROL_VIOLATION` and the tenant remains at its prior tier."
3. **Post-promotion write rejection** (line 303): "the gateway MUST reject the write with `CLASSIFICATION_CONTROL_VIOLATION`. The `ArtifactStore` does **not** fall back to the deployment-wide SSE key..."

This error code is **not present** in the §15.1 catalog table (lines 541–633) — a direct regression pattern matching iter1 API-001 (`TENANT_SUSPENDED` missing). It violates the §15.2.1(d) contract test (line 871: "All error responses — REST and MCP — use the error categories defined in [Section 16.3]"), which requires every wire error code be cataloged with category, HTTP status, and description so SDKs and conformance tests can discover it.

Impact: admin callers of `PUT /v1/admin/tenants/{id}` and runtime checkpoint paths receive an undocumented wire code with no discoverable category, retryability, or remediation.

**Recommendation:** Add `CLASSIFICATION_CONTROL_VIOLATION` to the §15.1 catalog. Suggested single-row entry:

| Code | Category | HTTP Status | Description |
| --- | --- | --- | --- |
| `CLASSIFICATION_CONTROL_VIOLATION` | `POLICY` | 422 (admin-time) / 503 (runtime write) | Tenant classification control could not be enforced. Returned on (a) `PUT /v1/admin/tenants/{id}` when the online KMS availability probe fails while setting `workspaceTier: T4` (tenant remains at prior tier); and (b) runtime `ArtifactStore` writes when the tenant's per-tenant KMS key (`tenant:{tenant_id}`) is unavailable and fallback to the shared SSE key is forbidden. `details.tenantId`, `details.kmsKeyRef`, and `details.probeError` are included. See [Section 12.5](12_storage-architecture.md#125-artifact-store) (T4 per-tenant KMS key lifecycle). |

Alternatively, split into `KMS_PROBE_FAILED` (422-admin) / `KMS_KEY_UNAVAILABLE` (503-runtime) to align with the existing `KMS_REGION_UNRESOLVABLE` (422) vs. `REGION_UNAVAILABLE` (503) precedent. Either choice must update the three §12.5 citations.

---

### STR-006 `CheckpointStorageHigh` and `StorageQuotaHigh` share threshold numerator — operability gap [Low]
**Files:** `/Users/joan/projects/lenny/spec/12_storage-architecture.md` (§12.5, lines 322–327), `/Users/joan/projects/lenny/spec/16_observability.md` (§16.5, lines 362–363)

The iter1 STR-002 fix replaced the proposed per-tenant checkpoint quota with a unified `storageQuotaBytes` bucket covering all artifact classes. §12.5, §16.5, and §4.4 are now internally consistent on metric names, labels, and the `maxConcurrent × 2` per-pod checkpoint count formula — so this is **not a contradiction**.

However, `CheckpointStorageHigh` (§16.5 line 362) and `StorageQuotaHigh` (line 363) both fire at 80% of the same `storageQuotaBytes` denominator, against different but closely-correlated numerators (`lenny_checkpoint_storage_bytes_total` vs. `lenny_storage_quota_bytes_used`). When legal-hold or concurrent-workspace checkpoint accumulation drives total storage growth, both alerts fire in tandem; operators cannot distinguish "checkpoint-dominated saturation" from "general artifact saturation" from alert signal alone, despite the §12.5 text claiming `CheckpointStorageHigh` "fires ahead of the artifact-wide `StorageQuotaHigh` only when checkpoints dominate the tenant's footprint."

**Recommendation:** Add one line to the §16.5 `CheckpointStorageHigh` alert description: "Because this alert and `StorageQuotaHigh` share the same `storageQuotaBytes` denominator, both fire in tandem when any artifact class dominates the tenant footprint. Operators should consult the ratio `lenny_checkpoint_storage_bytes_total / lenny_storage_quota_bytes_used` to determine whether checkpoints specifically are the driver before applying checkpoint-specific remediation (raising `periodicCheckpointIntervalSeconds`, lifting legal holds)." This is an operability note, not a correctness issue.

---

No other regressions. Verified:

- **STR-001 GC concurrency intact.** §12.5 lines 329–337 define single-writer + `WHERE deleted_at IS NULL` guard on every mutation path; Redis decrement ordering consistent with §11.2.
- **STR-002 checkpoint sizing intact.** §12.5 lines 322–327, §16.5 line 362, §4.4 lines 252–254 all agree on `maxConcurrent × 2` per-pod count, 500 MB ceiling, 100 MB / 2 s SLO. No contradictions with §16.1. See STR-006.
- **STR-003 KMS lifecycle intact (mechanism side).** §12.5 lines 299–305 define pre-provisioning with admin-time probe, fail-closed runtime behavior, operator-managed rotation. Deletion at §12.8 Phase 4a (line 836) with per-provider minimum delays (AWS 7d / GCP 24h / Vault immediate) and `KmsKeyDeletionFailed` alert. Wire-contract side broken — see STR-005.
- **CMP-042 MemoryStore erasure intact.** §12.1 line 5 mandates `DeleteByUser`/`DeleteByTenant` with compile-time enforcement. §9.4 lines 157–170, 182 define interface with idempotency and empty-arg rejection. §12.8 lines 729–744 define three-layer preflight (compile-time + startup + per-job). Metrics contracted at §9.4 line 186.
- **Redis fail-open (STR-004 scope).** §12.4 line 205 (rate limits, 60s window) and line 209 (quota, 300s cumulative) remain distinct-by-design. `/run/lenny/failopen-cumulative.json` persistence described in §12.4 line 224, closing iter1's split-with-§11.2 concern. Per-user ceiling at line 222. No regression.
- **Data-at-rest encryption.** §12.4 lines 197, 199 and §12.5 line 297 remain consistent with §13 posture.
