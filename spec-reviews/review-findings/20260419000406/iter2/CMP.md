### CMP-044 GDPR Erasure Not Propagated to Backups [HIGH]
**Files:** `12_storage-architecture.md` (§12.8 erasure scope and erasure propagation), `25_agent-operability.md` (§25.11 Backup and Restore API)

Section 12.8 specifies `DeleteByUser`/`DeleteByTenant` across every runtime store and propagates an `erasure.requested` event to SIEM and billing sinks, but the erasure scope table is silent on **backups**. Section 25.11 defines daily full Postgres and MinIO backups (including `SessionStore`, `EventStore` billing, `MemoryStore`, `UserStore`, `TokenStore`, artifacts) retained up to `retainDays: 90` at Tier 3. `pg_restore` reads the archive verbatim with no per-user or per-tenant filtering.

Consequences:

1. **Latent personal data retention.** A user erased on Day 1 is still present in every backup taken before erasure until the retention window elapses (up to 90 days at Tier 3). This sits inside the window during which the ICO, CNIL, and other DPAs treat personal data in backups as still under GDPR Article 17 absent explicit controls (documented retention policy, crypto-shredding, or "brought forward at first restore").
2. **Resurrection on restore.** `POST /v1/admin/restore/execute` pulls back the entire archive. After a restore, an erased user's sessions, memories, OAuth tokens, billing events with the original `user_id` (the per-tenant `erasure_salt` was destroyed — re-identification is now theoretically impossible, but the raw `user_id` is back on the row), and audit rows are resurrected. `processing_restricted` is also reverted. There is no post-restore reconciler that replays completed erasures — the `gdpr.*` receipts survive the restore under `audit.gdprRetentionDays` (7y) and would be usable for this, but no mechanism consumes them.
3. **HIPAA §164.530(c)(1) collision.** A restore that resurrects an erased patient's PHI is an unauthorized disclosure under 45 C.F.R. §164.502.

This is a material new finding not covered by CMP-042/043.

**Recommendation:** Add a "Backups in erasure scope" subsection to §12.8. Prefer a **post-restore reconciler** that, between `restore_completed` and the gateway restart, scans `audit_log` for `gdpr.*` completion receipts with `completed_at > backupTakenAt` and replays `DeleteByUser`/`DeleteByTenant` for each affected subject against the restored databases (the receipts survive restore under the 7-year retention). As alternative fallbacks: per-tenant backup crypto-shredding (tenant-scoped wrap key destroyed on `DeleteByTenant` — tenant-level only), or documenting that `backups.retainDays` must be ≤ the GDPR erasure SLA (72h for T3, 1h for T4). The reconciler path is the only one that satisfies per-user erasure without forcing short retention, and should be the default.

---

### CMP-045 Backup Storage Ignores Data Residency [HIGH]
**Files:** `12_storage-architecture.md` (§12.8 Data residency, Multi-region reference architecture), `25_agent-operability.md` (§25.11 Backup MinIO layout and KMS configuration)

Section 12.8 enforces `dataResidencyRegion` at pod routing, storage routing (Postgres, MinIO, Redis, KMS), and session admission — failing closed with `REGION_CONSTRAINT_UNRESOLVABLE` on unresolvable regions, and prohibiting cross-region transfer of T4 data.

§25.11 defines a single MinIO backup location (`backups/{type}/{id}/{timestamp}.tar.gz.enc`) accessed via a single `lenny-backup-minio` credential. `backups.encryption.kmsKeyId` is a single scalar, not a per-region map. `backups.*` has no per-region variant analogous to `storage.regions.<region>.*`. The `pg_dump` flow is a full-shard dump via `AllSessionShards()` — in a multi-region topology this aggregates every region's rows into one archive.

Consequences:

1. In a multi-region deployment (e.g., `eu-west-1` + `us-east-1`), EU tenant data (T3, subject to `dataResidencyRegion: eu-west-1`) is dumped and written to a MinIO bucket whose endpoint is not region-constrained. If the bucket is US-hosted (likely, given `minio.endpoint` is a single scalar), this is a prohibited cross-border transfer of T3 data under §12.8 rules and a GDPR Article 44-46 transfer without a documented legal basis.
2. The KMS decrypt capability for backup archives is single-region. Per §12.8 "KMS key residency," cross-region decrypt capability IS a cross-border transfer even if the encrypted bytes stay in-region.
3. The multi-region reference architecture says "one Lenny control plane per region" but makes no statement on whether each region has its own `lenny-ops` + backup pipeline. The spec is silent on the only correct configuration (per-region backup pipeline + per-region backup bucket + per-region KMS).

**Recommendation:** Extend `backups.*` to per-region maps consistent with `storage.regions.<region>.*`: require `backups.regions.<region>.{minioEndpoint,kmsKeyId,accessCredentialSecret}` when any tenant has `dataResidencyRegion` set. `lenny-ops` must route per-shard dumps to their region's backup endpoint (per-region `pg_dump`, not a global dump) and reject configurations that would cross regions. Add a `BackupRegionUnresolvable` fail-closed path mirroring `REGION_CONSTRAINT_UNRESOLVABLE`, emit a `DataResidencyViolationAttempt` audit event when a backup write would cross regions, and document the per-region backup pipeline as part of the multi-region reference architecture.

---

No real issues found in: GDPR erasure sequence ordering (steps 1–19 remain dependency-correct), MemoryStore preflight (CMP-042 resolved), Article 20 audit-event export scope (CMP-043 resolved), audit hash-chain integrity (per-tenant genesis nonce + JCS canonical payload + per-tenant advisory lock + `rechained_post_outage`), billing immutability (INSERT-only grants + triggers + `lenny_erasure` role bypass guarded by `SET LOCAL lenny.erasure_mode` + guard-clause startup verification), operator-initiated billing correction dual-control, `erasure_salt` immediate deletion and verification, compliance profile SIEM gating (hard-fail for regulated profiles), pgaudit requirement, OCSF translation failure path preserves chain integrity, write-time tenant validation, `processing_restricted` database-level CHECK trigger for Article 18, cross-tenant admin-impersonation modeling.
