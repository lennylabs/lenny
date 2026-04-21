# Perspective 7: Operator & Deployer Experience — Iter7

## Scope

Iter6 was deferred for this perspective (sub-agent rate-limit exhaustion per `/Users/joan/projects/lenny/spec-reviews/review-findings/20260419000406/iter6/p7_operator_experience.md`). Iter5 (`iter5/p07_operator.md`) is the authoritative baseline. For iter7 I re-examined each of the six iter5 carry-forwards (OPS-016 through OPS-021) against the current spec, re-read the relevant sections of `spec/17_deployment-topology.md` (§17.4, §17.7, §17.8.1), `spec/25_agent-operability.md` (§25.4, §25.7 Path B `issueRunbooks`, §25.10, §25.11), `spec/15_external-api-surface.md` (§15.1 admin endpoints and scope taxonomy), `spec/24_lenny-ctl-command-reference.md` (§24.7), and the companion `docs/runbooks/` directory. I also verified the two iter6 docs-sync fixes that landed while P7 was deferred — CRD-021 (user-credential runbook) and API-020 (circuit-breaker admin endpoints) — for internal consistency.

Prefix: **OPS-** (matching iter2–iter5). Severities anchored to the prior-iteration rubric (all discoverability / runbook-catalog gaps remain Low per `feedback_severity_calibration_iter5`).

## Prior-iteration carry-forwards

All six iter5 findings remain **unfixed in the committed spec**. Verification by targeted Grep on the current tree:

- §25.7 `issueRunbooks` map at `25_agent-operability.md:3222-3231` still enumerates exactly the eight pre-iter3 entries (`WARM_POOL_EXHAUSTED`, `WARM_POOL_LOW`, `CREDENTIAL_POOL_EXHAUSTED`, `POSTGRES_UNREACHABLE`, `REDIS_UNREACHABLE`, `MINIO_UNREACHABLE`, `CERT_EXPIRY_IMMINENT`, `CIRCUIT_BREAKER_OPEN`). No additions for `BACKUP_RECONCILE_BLOCKED`, `MINIO_ARTIFACT_REPLICATION_{LAG,FAILED}`, `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`, or `DRIFT_SNAPSHOT_STALE`.
- §17.8.1 defaults table: `Grep "ops\.drift|backups\.erasureReconciler|minio\.artifactBackup|residencyCheckInterval|replicationLagRpoSeconds|EMBEDDED_PG_VERSION|PG_VERSION"` against `spec/17_deployment-topology.md` returns **zero matches inside the defaults table** (the single hit is the drift-runbook trigger blurb at line 815, which predates iter5).
- §17.7 runbook catalog: no entries named `post-restore-reconciler-blocked`, `artifactstore-replication-recovery`, or `artifactstore-replication-residency-violation`. The `docs/runbooks/` directory likewise lacks all three files.
- §17.4 "State and resets" still contains no `PG_VERSION` check, `EMBEDDED_PG_VERSION_MISMATCH` error code, or documented `lenny export`/`lenny import` recovery path.

The text of the six findings below is therefore a verbatim carry-forward from iter5 with severities held at Low. Recommendations are unchanged — the iter5 guidance to land all six in a single fix commit (~15 lines across three spec files) is still the right disposition.

### OPS-016 `lenny-ops` Helm values `backups.erasureReconciler.*` and `minio.artifactBackup.*` missing from §17.8.1 operational defaults table (iter5 carry-forward; iter4 OPS-010 chain) [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.1; `spec/25_agent-operability.md` §25.4 canonical values block, §25.11 ArtifactStore Backup subsection.

Unchanged since iter4. §17.8.1's header still promises *"All tunable defaults collected in one place for operator convenience"*, but the table omits `backups.erasureReconciler.enabled`, `backups.erasureReconciler.legalHoldLedgerFreshnessGate`, `minio.artifactBackup.enabled`, `minio.artifactBackup.target.*`, `minio.artifactBackup.versioning`, `minio.artifactBackup.replicationLagRpoSeconds`, and the iter4 residency preflight knobs `residencyCheckIntervalSeconds` / `residencyAuditSamplingWindowSeconds`. An operator scanning §17.8.1 for backup/erasure/artifact-replication tunables after an alert fires finds nothing and incorrectly concludes the knobs do not exist.

**Recommendation:** Apply the iter4 OPS-010 fix verbatim (four rows for the erasure reconciler, the legal-hold ledger freshness gate, `minio.artifactBackup.enabled`, and the replication-lag RPO) and add two rows for the iter4-new residency preflight (`residencyCheckIntervalSeconds`, default 300s; `residencyAuditSamplingWindowSeconds`, default 3600s). Commit alongside OPS-018 / OPS-020 so the defaults-table pass is done once.

---

### OPS-017 `issueRunbooks` lookup table omits `BACKUP_RECONCILE_BLOCKED` and `MINIO_ARTIFACT_REPLICATION_*` codes (iter5 carry-forward; iter4 OPS-011 chain) [Low]

**Section:** `spec/25_agent-operability.md` §25.7 Path B lookup at line 3222; `spec/17_deployment-topology.md` §17.7 line 723 enumeration; §25.11 ArtifactStore Backup subsection.

Unchanged since iter4. The `issueRunbooks` map in `pkg/gateway/health/runbook_links.go` (reproduced at §25.7 line 3222) still enumerates exactly the pre-iter3 eight entries. No entry for `BACKUP_RECONCILE_BLOCKED`, `MINIO_ARTIFACT_REPLICATION_LAG`, `MINIO_ARTIFACT_REPLICATION_FAILED`, or `ARTIFACT_REPLICATION_REGION_UNRESOLVABLE`. Agents receiving these alert events get no `runbook:` field on Path B and are forced to fall back to the Path C full-list scan, breaking the cheaper-path convention §17.7 line 723 advertises.

**Recommendation:** Apply the iter4 OPS-011 fix plus one row for the iter4-introduced residency-violation code:

```go
"BACKUP_RECONCILE_BLOCKED":                 "post-restore-reconciler-blocked",
"MINIO_ARTIFACT_REPLICATION_LAG":           "artifactstore-replication-recovery",
"MINIO_ARTIFACT_REPLICATION_FAILED":        "artifactstore-replication-recovery",
"ARTIFACT_REPLICATION_REGION_UNRESOLVABLE": "artifactstore-replication-residency-violation",
```

Amend §17.7 line 723 "required by §25.7 Path B" enumeration to include these codes. If `BACKUP_RECONCILE_BLOCKED` surfaces only as an alert (not a health-API issue code), also add the `runbook` annotation on the Prometheus rule so the §25.5 `alert_fired` event path carries the pointer.

---

### OPS-018 §17.7 runbook catalog missing entries for post-restore reconciler block, ArtifactStore replication recovery, and residency-violation recovery (iter5 carry-forward; iter4 OPS-012 chain, expanded) [Low]

**Section:** `spec/17_deployment-topology.md` §17.7 (lines 715–829); `spec/25_agent-operability.md` §25.11; §25.14 `lenny-ctl` table.

Unchanged since iter4. The §17.7 catalog enumerates 18+ runbooks (Postgres, Redis, MinIO-gateway, token service, credential pool, admission webhooks, etcd, stuck finalizers, schema migration, audit pipeline, token store, rate-limit storms, drift snapshot refresh, clock drift, gateway replica failure, gateway capacity, cert-manager-outage, controller-leader-election, delegation-budget-recovery, …). Missing:

1. **`post-restore-reconciler-blocked.md`** — triggered by `BackupReconcileBlocked` alert / `gdpr.backup_reconcile_blocked` audit event / `GET /v1/admin/restore/{id}/status` returning `phase: "reconciler_blocked"`. Remediation (`POST /v1/admin/restore/{id}/confirm-legal-hold-ledger` or `lenny-ctl restore confirm-legal-hold-ledger`) is scattered across §25.11 prose, §25.14, and §12.8 without a unified three-part runbook.
2. **`artifactstore-replication-recovery.md`** — triggered by `MinIOArtifactReplicationLagHigh` / `MinIOArtifactReplicationFailed`. Remediation requires assembling §25.11's "Restore procedure" prose by hand.
3. **`artifactstore-replication-residency-violation.md`** — triggered by `ArtifactReplicationResidencyViolation` critical alert / `lenny_minio_replication_residency_violation_total` increments / `GET /v1/admin/artifact-replication/{region}` returning `status: "suspended_residency_violation"`. No §17.7 entry; procedure documented only in §25.11 runtime-residency-preflight prose and §12.8.

**Recommendation:** Add three stubbed §17.7 runbook entries with the standard `<!-- access: trigger --> / diagnosis / remediation` three-part structure. Entries #1 and #2 are the iter4 OPS-012 recommendation verbatim. Entry #3 (iter4-new):

- **`artifactstore-replication-residency-violation.md`** — *Trigger:* `ArtifactReplicationResidencyViolation` critical alert; `lenny_minio_replication_residency_violation_total` increments; `GET /v1/admin/artifact-replication/{region}` returns `status: "suspended_residency_violation"`. *Diagnosis:* inspect `DataResidencyViolationAttempt` audit event for source region, destination endpoint, returned jurisdiction tag, CIDR-resolution result; compare with Helm `minio.regions.<region>.artifactBackup.target` and `backups.regions.<region>.allowedDestinationCidrs`. *Remediation:* (1) fix root cause — correct destination bucket `lenny.dev/jurisdiction-region` tag, revert DNS rebinding, or re-provision destination in the correct region; (2) re-verify with `s3:GetBucketTagging`; (3) call `POST /v1/admin/artifact-replication/{region}/resume` with `justification` (platform-admin, audited); (4) confirm alert clears and `lenny_minio_replication_lag_seconds` resumes decreasing.

Pair with OPS-017 so `issueRunbooks` map and §17.7 catalog land consistent runbook slugs in one revision. Create the companion docs/runbooks/ files in the same commit per `feedback_docs_sync_after_spec_changes`.

---

### OPS-019 Embedded Mode has no Postgres-major-version mismatch fail-safe (iter5 carry-forward; iter4 OPS-013 / iter3 OPS-007 / iter2 OPS-005 chain) [Low]

**Section:** `spec/17_deployment-topology.md` §17.4 "State and resets" (lines 169–174).

Unchanged since iter2. §17.4 documents schema-migration handling and `lenny down --purge` but says nothing about a PostgreSQL *binary major version* bump. `~/.lenny/postgres/` uses a PG-major-specific on-disk layout; a newer `lenny` binary against an on-disk directory written by an older embedded-postgres major will either fail to start or crash opaquely. No `PG_VERSION` check, no documented `lenny export` / `lenny import` path, no fail-closed `EMBEDDED_PG_VERSION_MISMATCH` error. Low severity today (PG 16 pinned, no deployments in the wild, `feedback_no_backward_compat`) but surfaces on the first PG major bump after GA.

**Recommendation:** Apply the iter4 OPS-013 fix verbatim — add one sentence to §17.4: *"`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch with the expected major, fails closed with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to <path>` then `lenny down --purge && lenny up && lenny import --from <path>`). In-place `pg_upgrade` is not supported in Embedded Mode."*

---

### OPS-020 §17.8.1 defaults table still omits `ops.drift.*` tunables (iter5 carry-forward; iter4 OPS-014 / iter3 OPS-008 / iter2 OPS-006 chain) [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.1; `spec/25_agent-operability.md` §25.4 canonical values block (lines 946–947), §25.10 configuration drift detection.

Unchanged since iter2. §25.4 defines `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60) as operator-facing Helm values; both are referenced in §25.10 and in `drift-snapshot-refresh.md`'s trigger blurb (§17.7 line 815). Neither appears in the §17.8.1 defaults table. Fold into OPS-016 so the defaults-table pass is done once.

**Recommendation:** Add two rows per the iter4 OPS-014 recommendation:

| Setting | Default | Reference |
| --- | --- | --- |
| Drift snapshot-staleness warning threshold (`ops.drift.snapshotStaleWarningDays`) | 7 days (0 disables) | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |
| Drift running-state cache TTL (`ops.drift.runningStateCacheTTLSeconds`) | 60 s | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |

---

### OPS-021 `issueRunbooks` lookup still missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` mapping (iter5 carry-forward; iter4 OPS-015 / iter3 OPS-009 chain) [Low]

**Section:** `spec/25_agent-operability.md` §25.7 Path B (line 3222); `spec/17_deployment-topology.md` §17.7 line 723 enumeration.

Unchanged since iter3. Fold into the OPS-017 edit so all five missing codes (`DRIFT_SNAPSHOT_STALE` plus the four iter3/iter4 additions) land in a single pass.

**Recommendation:** Add to the `issueRunbooks` map:

```go
"DRIFT_SNAPSHOT_STALE": "drift-snapshot-refresh",
```

Amend the §17.7 line 723 enumeration sentence accordingly. Decide in the same edit whether `snapshot_stale: true` should surface as an `alert_fired` event on the §25.5 event stream; if yes, add the alert annotation in §16.5; if no, state explicitly in §25.10 that the signal is API-response-only.

---

## New findings

### OPS-022 `issueRunbooks` map routes `CIRCUIT_BREAKER_OPEN` to `gateway-replica-failure` but a dedicated `docs/runbooks/circuit-breaker-open.md` now exists [Low]

**Section:** `spec/25_agent-operability.md` §25.7 Path B lookup at line 3230; `docs/runbooks/circuit-breaker-open.md`; `docs/runbooks/gateway-replica-failure.md`.

Discovered in this iteration. `issueRunbooks` contains:

```go
"CIRCUIT_BREAKER_OPEN":     "gateway-replica-failure",
```

But the docs/runbooks/ directory already ships a dedicated `circuit-breaker-open.md` file (front-matter `triggers: [GatewaySubsystemCircuitOpen, CircuitBreakerActive]`; components: gateway; symptoms: "gateway subsystem circuit in open state", "downstream dependency failing consistently", "upstream returns fail-fast responses"). This file is the semantically correct target — it covers the tokenService / postgres / redis / objectStore / llmUpstream subsystem breakers, not just replica failures. The map misroutes Path B health-API consumers away from the purpose-built runbook and to `gateway-replica-failure`, which is scoped to "gateway pod crash-loops or replica-count drops" and does not address circuit-breaker diagnosis.

Provenance: the iter6 fix pass that landed API-020 (`/v1/admin/circuit-breakers` endpoints in §15.1) and the docs-sync pass that landed `docs/runbooks/circuit-breaker-open.md` did not update the `issueRunbooks` Go-literal map. `feedback_docs_sync_after_spec_changes` requires the Go-literal that drives Path B discovery to be in sync with the docs/runbooks/ directory contents after a docs-sync iteration.

**Severity:** Low (matches the calibration rule for all `issueRunbooks` entries in this perspective; has a documented Path C full-list-scan workaround; no convergence-blocking). Fold into the OPS-017 / OPS-021 single-commit fix:

```go
"CIRCUIT_BREAKER_OPEN":     "circuit-breaker-open",  // was: "gateway-replica-failure"
```

If the migration should be non-disruptive for any in-flight health-API consumers that pinned `gateway-replica-failure`, `gateway-replica-failure.md` can add a `related: circuit-breaker-open` cross-link, but this is optional; the map edit alone is sufficient.

---

## Iter6 docs-sync verification (CRD-021, API-020)

Iter6 landed two fixes affecting P7-relevant surfaces while P7 was deferred. Verified for internal consistency:

- **CRD-021 (user-credential runbook coverage).** `docs/runbooks/credential-revocation.md` lines 163–222 add a "User-scoped credentials" section with remediation steps U1–U5 for `POST /v1/credentials/{credential_ref}/revoke`, `DELETE /v1/credentials/{credential_ref}`, provider-side revocation, user re-registration, and the audit-event pairing (`credential.user_revoked`, `credential.registered`). The section cites `lenny_user_credential_revoked_with_active_leases` gauge (labels `tenant_id`, `provider`) feeding the `CredentialCompromised` alert alongside its pool-scoped counterpart. Spec `§4.9` cross-reference is present. No inconsistency observed.
- **API-020 (circuit-breaker admin endpoints).** `spec/15_external-api-surface.md` lines 884–887 add `GET/POST /v1/admin/circuit-breakers[/{name}[/{close}]]` rows; line 915 adds `circuit_breaker` to the scope-taxonomy domain list; line 1030 retains the `CIRCUIT_BREAKER_OPEN` error row. `spec/24_lenny-ctl-command-reference.md` §24.7 wires `lenny-ctl` commands `circuit-breakers list / open / close` consistent with the API surface. `docs/runbooks/circuit-breaker-open.md` front-matter triggers align with §16.5 `GatewaySubsystemCircuitOpen` / `CircuitBreakerActive` alerts. The only remaining gap from this family is OPS-022 above (the `issueRunbooks` misroute) — otherwise the fix is internally coherent.

No drift found between these iter6 changes and §15.1, §24.7, §16.5, or the companion docs/runbooks/ files.

## Convergence assessment

**Not converged on ops-discoverability polish, but no convergence-blocking issues.** Perspective 7 finds zero Critical, zero High, zero Medium findings, and seven Low items: six iter5 carry-forwards (OPS-016 through OPS-021) plus one new-in-iter7 finding (OPS-022). All seven are literal discoverability / runbook-catalog / Path-B-mapping gaps with documented workarounds (Path C full-list scan for runbooks; reading §25.4 directly for defaults; purge-and-reinstall for an embedded PG major bump).

The calibration rule (anchor iter7 severities to prior iterations' rubric) holds: every carry-forward was filed at Low in iter2/iter3/iter4/iter5 and remains Low here. OPS-022 anchors to the same `issueRunbooks`-mapping rubric (OPS-017, OPS-021) — Low.

None of the seven findings was introduced by iter6. OPS-022 was *surfaced* by iter6 (the dedicated runbook file was added without a companion map update), but the misroute existed in iter5 with less-specific justification (no dedicated runbook to route to). That is, iter6 didn't regress the map; it revealed a latent mismatch by shipping a better target.

**Recommendation:** land OPS-016 through OPS-022 in a single iter7 fix commit. The edits are co-located in three spec files (`17_deployment-topology.md` §17.4/§17.7/§17.8.1 and `25_agent-operability.md` §25.7) plus three new companion runbook files in `docs/runbooks/` (`post-restore-reconciler-blocked.md`, `artifactstore-replication-recovery.md`, `artifactstore-replication-residency-violation.md`). Total ~18 lines of spec additions plus three runbook stubs. This clears the entire iter2→iter7 carry-forward tail for this perspective and aligns Path B discoverability with the now-shipped docs/runbooks/ set per `feedback_docs_sync_after_spec_changes`.
