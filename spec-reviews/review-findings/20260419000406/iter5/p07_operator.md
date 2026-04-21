# Perspective 7: Operator & Deployer Experience — Iter5

## Scope

Re-examined iter4 findings OPS-010 through OPS-015 (all six Low-severity gaps) against the current spec, with a focused re-read of `spec/17_deployment-topology.md` (§17.4, §17.7, §17.8.1), `spec/18_build-sequence.md`, and `spec/25_agent-operability.md` (§25.4, §25.7, §25.11). The iter4 summary did not mark any OPS-010..OPS-015 as Fixed; line 2206 of the iter4 summary classifies `OPS-005/006/009` (their iter3 ancestors) as carry-forwards from iter3 where fixes were skipped or never landed. Direct inspection of the current spec confirms the text is unchanged from what iter4 described.

Prefix: **OPS-** (matching iter4). Severities anchored to iter2/iter3/iter4 rubric (all doc-discoverability gaps remain Low per `feedback_severity_calibration_iter5`).

## Carry-forward findings

### OPS-016 `lenny-ops` Helm values `backups.erasureReconciler.*` and `minio.artifactBackup.*` missing from §17.8.1 operational defaults table (iter4 OPS-010 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 830–881); `25_agent-operability.md` §25.4 canonical values block, §25.11 ArtifactStore Backup subsection.

Unchanged since iter4. The §17.8.1 defaults table header at line 832 still reads *"All tunable defaults collected in one place for operator convenience"*, but the table (lines 834–879) does not mention `backups.erasureReconciler.enabled`, `backups.erasureReconciler.legalHoldLedgerFreshnessGate`, `minio.artifactBackup.enabled`, `minio.artifactBackup.target.*`, `minio.artifactBackup.versioning`, `minio.artifactBackup.replicationLagRpoSeconds`, or the iter4-added `minio.artifactBackup.residencyCheckIntervalSeconds` / `minio.artifactBackup.residencyAuditSamplingWindowSeconds` knobs. An operator scanning §17.8.1 for backup/erasure/artifact-replication tunables after an iter3-introduced alert fires finds nothing and will incorrectly conclude the knobs are non-existent.

**Recommendation:** Apply the iter4 OPS-010 fix verbatim (four rows covering the erasure reconciler, the legal-hold ledger freshness gate, `minio.artifactBackup.enabled`, and the replication-lag RPO) and add two extra rows for the iter4-new residency preflight (`residencyCheckIntervalSeconds`, default 300s; `residencyAuditSamplingWindowSeconds`, default 3600s). Commit alongside the OPS-018 / OPS-019 fixes so the defaults table is revised once.

---

### OPS-017 `issueRunbooks` lookup table omits `BACKUP_RECONCILE_BLOCKED` and `MINIO_ARTIFACT_REPLICATION_*` codes (iter4 OPS-011 carry-forward) [Low]

**Section:** `25_agent-operability.md` §25.7 Path B lookup at line 3184; `17_deployment-topology.md` §17.7 line 723 enumeration; §25.11 ArtifactStore Backup subsection (lines ~3990–4060).

Unchanged since iter4. The `issueRunbooks` map in `pkg/gateway/health/runbook_links.go` at §25.7 (line 3184) still enumerates exactly the pre-iter3 eight entries (`WARM_POOL_EXHAUSTED` through `CIRCUIT_BREAKER_OPEN`). No entry exists for `BACKUP_RECONCILE_BLOCKED`, `MINIO_ARTIFACT_REPLICATION_LAG`, `MINIO_ARTIFACT_REPLICATION_FAILED`, or the iter4-introduced `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`. Agents receiving these alert events get no `runbook:` field and must fall back to Path C (full-list scan), breaking the cheaper Path B convention §17.7 line 723 advertises.

**Recommendation:** Apply the iter4 OPS-011 fix and add one extra entry for the iter4-introduced residency-violation code:

```go
"BACKUP_RECONCILE_BLOCKED":                 "post-restore-reconciler-blocked",
"MINIO_ARTIFACT_REPLICATION_LAG":           "artifactstore-replication-recovery",
"MINIO_ARTIFACT_REPLICATION_FAILED":        "artifactstore-replication-recovery",
"ARTIFACT_REPLICATION_REGION_UNRESOLVABLE": "artifactstore-replication-residency-violation",
```

Extend the §17.7 line 723 "required by §25.7 Path B" sentence to list all additional codes alongside the OPS-020 (`DRIFT_SNAPSHOT_STALE`) fold. If `BACKUP_RECONCILE_BLOCKED` surfaces only as an alert (not a health-API issue code), also add the `runbook` annotation to the Prometheus rule so the §25.5 `alert_fired` event path carries the pointer.

---

### OPS-018 §17.7 runbook catalog missing entries for post-restore reconciler block, ArtifactStore replication recovery, and residency-violation recovery (iter4 OPS-012 carry-forward, expanded) [Low]

**Section:** `17_deployment-topology.md` §17.7 (lines 713–822); `25_agent-operability.md` §25.11 ArtifactStore Backup subsection (lines ~3990–4060) and Post-restore reconciler block (lines ~4155+); §25.14 `lenny-ctl` table line 4916.

Unchanged since iter4, with one iter4-new surface added. The §17.7 catalog still enumerates runbooks covering Postgres, Redis, MinIO-gateway, token service, credential pool, admission webhooks, etcd, stuck finalizers, schema migration, audit pipeline, token store, rate-limit storms, drift snapshot refresh, and clock drift. Missing:

1. **`post-restore-reconciler-blocked.md`** — triggered by `BackupReconcileBlocked` alert / `gdpr.backup_reconcile_blocked` audit event / `GET /v1/admin/restore/{id}/status` returning `phase: "reconciler_blocked"`. Remediation path (`POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` or `lenny-ctl restore confirm-legal-hold-ledger`) is scattered across §25.11 prose (line 3859), §25.14 (line 4916), and §12.8 with no unified three-part runbook entry.

2. **`artifactstore-replication-recovery.md`** — triggered by `MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed`. Remediation requires piecing together §25.11's "Restore procedure" prose.

3. **`artifactstore-replication-residency-violation.md`** (new in iter4 / iter3-post-fix `CMP-048`-adjacent hardening) — triggered by `ArtifactReplicationResidencyViolation` critical alert. Recovery requires correcting the destination jurisdiction tag or Helm values, then calling `POST /v1/admin/artifact-replication/{region}/resume` (platform-admin, audited). No §17.7 entry exists; the procedure is documented only in §25.11's runtime-residency-preflight prose.

**Recommendation:** Add three stubbed §17.7 runbook entries with the standard `<!-- access: trigger --> / diagnosis / remediation` three-part structure. The first two are the iter4 OPS-012 recommendation verbatim. The third:

- **`artifactstore-replication-residency-violation.md`** — *Trigger:* `ArtifactReplicationResidencyViolation` critical alert; `lenny_minio_replication_residency_violation_total` increments; `GET /v1/admin/artifact-replication/{region}` returns `status: "suspended_residency_violation"`. *Diagnosis:* inspect `DataResidencyViolationAttempt` audit event for source region, destination endpoint, returned jurisdiction tag, and CIDR-resolution result; compare to Helm `minio.regions.<region>.artifactBackup.target` and `backups.regions.<region>.allowedDestinationCidrs`. *Remediation:* (1) fix the root cause — correct the destination bucket's `lenny.dev/jurisdiction-region` tag, revert the DNS rebinding, or re-provision the destination in the correct region; (2) re-verify with `s3:GetBucketTagging` against the destination; (3) call `POST /v1/admin/artifact-replication/{region}/resume` with `justification` (platform-admin, audited); (4) confirm the alert clears and `lenny_minio_replication_lag_seconds` resumes decreasing. Cross-reference: §25.11 runtime-residency-preflight, §12.8 backup pipeline residency.

Pair this edit with OPS-017 so the `issueRunbooks` map and the §17.7 catalog land consistent runbook slugs in a single revision.

---

### OPS-019 Embedded Mode has no Postgres-major-version mismatch fail-safe (iter4 OPS-013 / iter3 OPS-007 / iter2 OPS-005 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.4 "State and resets" (lines ~162–163).

Unchanged since iter2. §17.4 still documents schema-migration handling and `lenny down --purge` but says nothing about a PostgreSQL *binary major version* bump. `~/.lenny/postgres/` uses a PG-major-version-specific on-disk layout (the spec pins PG 16 elsewhere); a newer `lenny` binary against an on-disk directory written by an older embedded-postgres major will either fail to start or crash opaquely. No `PG_VERSION` check, no documented `lenny export` / `lenny import` path, no fail-closed `EMBEDDED_PG_VERSION_MISMATCH` error. Low severity today (PG 16 pinned, no deployments in the wild, `feedback_no_backward_compat`), but surfaces on the first PG major bump after GA.

**Recommendation:** Apply the iter4 OPS-013 fix verbatim — add one sentence to §17.4: *"`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch with the expected major, fails closed with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to <path>` then `lenny down --purge && lenny up && lenny import --from <path>`). In-place `pg_upgrade` is not supported in Embedded Mode."* Two-line spec addition that prevents silent data loss on the first PG major bump.

---

### OPS-020 Operational defaults table §17.8.1 still omits `ops.drift.*` tunables (iter4 OPS-014 / iter3 OPS-008 / iter2 OPS-006 carry-forward) [Low]

**Section:** `17_deployment-topology.md` §17.8.1 (lines 830–881); `25_agent-operability.md` §25.4 canonical values block, §25.10 configuration drift detection.

Unchanged since iter2. §25.4 defines `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60) as operator-facing Helm values; both are referenced in §25.10 and in `drift-snapshot-refresh.md`'s trigger blurb (§17.7 line 813). Neither appears in the §17.8.1 defaults table. Fold into OPS-016 so the defaults table pass is done once.

**Recommendation:** Add two rows per the iter4 OPS-014 recommendation:

| Setting | Default | Reference |
| --- | --- | --- |
| Drift snapshot-staleness warning threshold (`ops.drift.snapshotStaleWarningDays`) | 7 days (0 disables) | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |
| Drift running-state cache TTL (`ops.drift.runningStateCacheTTLSeconds`) | 60 s | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |

---

### OPS-021 `issueRunbooks` lookup still missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` mapping (iter4 OPS-015 / iter3 OPS-009 carry-forward) [Low]

**Section:** `25_agent-operability.md` §25.7 Path B (line 3184); `17_deployment-topology.md` §17.7 line 723 enumeration.

Unchanged since iter3. `issueRunbooks` at line 3184 still enumerates only the original eight entries and §17.7 line 723 still does not include `DRIFT_SNAPSHOT_STALE`. Fold into the OPS-017 edit so all five missing codes (`DRIFT_SNAPSHOT_STALE` + four iter3/iter4 additions) land in one pass.

**Recommendation:** Add to the `issueRunbooks` map:

```go
"DRIFT_SNAPSHOT_STALE": "drift-snapshot-refresh",
```

Amend the §17.7 line 723 enumeration sentence accordingly. Decide in the same edit whether `snapshot_stale: true` should also surface as an `alert_fired` event on the §25.5 event stream; if yes, add the alert annotation in §16.5; if no, state explicitly in §25.10 that the signal is API-response-only.

---

## New issues

None. The spot-checks of §17.4, §17.7, §17.8.1, §18, §25.4, §25.7, and §25.11 did not surface any additional operational breakage beyond the six carry-forwards above. §18 build-sequence subsections 18.1 (build artifacts introduced by §25) are tightly cross-referenced; `lenny-ops`, `lenny-backup`, and the shared `pkg/alerting/rules` wiring are all called out. §17.7 covers 18+ runbooks with uniform three-part structure and the three new entries above plug the specific gaps iter3/iter4 introduced.

## Convergence assessment

**Not converged on ops-discoverability polish, but no convergence-blocking issues.** Perspective 7 finds zero Critical, zero High, zero Medium, and six Low carry-forwards from iter4. All six are literal discoverability/runbook-catalog gaps — they have documented workarounds (Path C full-list scan for runbooks; reading §25.4 directly for defaults; purge-and-reinstall for an embedded PG major bump). None prevents deployment, none creates an upgrade-risk surface, and none is a newly-introduced iter5 regression.

The calibration rule (anchor iter5 severities to prior iterations' rubric) holds: every finding was filed at Low in iter2/iter3/iter4 and remains Low here. Per `feedback_docs_sync_after_spec_changes`, if a future iter5 fix pass lands any subset, the companion docs/ sync is limited to these same three files (§17.7, §17.8.1, §25.7) — no broader reconciliation is needed.

Recommendation: land OPS-016 through OPS-021 in a single iter5 fix commit — the edits are co-located in three spec files, total ~15 lines added, and clear the entire iter2→iter5 carry-forward tail for this perspective.
