# Iter3 OPS Review

## Regression check — terminology rename (iter2 OPS-004)

Verified commit 2a46fb6 and 4e42e07 rename. Result: **clean pass on all four collision sites flagged in iter2**:

1. **§17.4 (line 117)** — introduces "three local-dev modes" with explicit disambiguation note distinguishing Embedded/Source/Compose Mode from the capacity tiers.
2. **§17.4 body (lines 119–253)** — all three mode headers and inline references (`lenny up`, `make run`, `docker compose up`) consistently labeled Embedded / Source / Compose Mode; no remaining "Tier 0/1/2" tokens for local dev.
3. **§17.6 install wizard (line 648)** — explicit "Note — independent axes" paragraph covers Target environment vs Capacity tier vs Embedded/Source/Compose Mode.
4. **§17.6 Day-0 walkthrough (line 499)** — now reads "production-style **capacity-Tier 2** installs" as recommended.
5. **§17.9.2 answer-file table (line 1253)** — now reads "Layered with `values-tier1.yaml` (capacity) for Compose-Mode dev installs".

Cross-file grep (`Tier 0`, `Tier-0`, `tier0`) returns no matches in the spec corpus. §23.2, §24 (§24.19, §24.1 framing), §27 (§27.3 `playground.authMode=dev`) all use "Embedded Mode" correctly. No regressions.

## Prior-finding status

- **OPS-001, OPS-003** — fixed and verified in iter2; no regressions.
- **OPS-002** — unchanged (iter2 classified doc-only Low; same status).
- **OPS-004** — **fixed** (this iteration's regression-check passed).
- **OPS-005** — **still open** (see below, re-filed as OPS-007 for visibility).
- **OPS-006** — **still open** (see below, re-filed as OPS-008 for visibility).

iter2 OPS-005 and OPS-006 were not touched in commit 2a46fb6 (confirmed by git log: only OPS-004 appears in the fix roster). Both remain genuinely useful to address and are re-filed below.

---

### OPS-007 Embedded Mode has no Postgres-major-version mismatch fail-safe (iter2 OPS-005 carry-forward) [Low]

**Files:** `17_deployment-topology.md` §17.4 "State and resets" (line 159–160).

**Issue.** §17.4 covers schema migrations against embedded Postgres but says nothing about the case where the Postgres *binary major version* itself is bumped in a Lenny release. `~/.lenny/postgres/` uses a Postgres-major-version-specific on-disk layout (§17.4 currently pins PostgreSQL 16); a newer `lenny` binary against an on-disk directory written by an older `embedded-postgres` major will either fail to start or crash opaquely. No `PG_VERSION` check, no documented `lenny export`/`import` path, no fail-closed error is specified.

The "Upgrades" bullet on line 160 reads: *"Upgrades: `lenny up` on a newer binary runs the standard schema migration path against the embedded Postgres. Rollback is not supported in Embedded Mode — the user is expected to `lenny down --purge` and start fresh if they need to revert."* This covers schema migrations but is silent on Postgres binary-major upgrades.

**Impact.** Low in v1 (PG 16 pinned today) but surfaces on the first Postgres major bump after GA. Operators relying on `lenny up` as a durable local platform — an explicit §17.4 use case for runtime authors and evaluating deployers — lose state silently with no warning.

**Recommendation.** Add one sentence to §17.4 "State and resets": *"`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch with the expected major, fails closed with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to <path>` then `lenny down --purge && lenny up && lenny import --from <path>`). In-place `pg_upgrade` is not supported in Embedded Mode."* This is a two-line spec addition that costs nothing today and prevents silent data loss on the first PG major bump.

---

### OPS-008 Operational defaults table §17.8.1 still omits `ops.drift.*` tunables (iter2 OPS-006 carry-forward) [Low]

**Files:** `17_deployment-topology.md` §17.8.1 (lines 804–849), `25_agent-operability.md` §25.4 (lines 916–920), §25.10 (lines 3581, 3604).

**Issue.** §17.8.1 header line 806 reads: *"All tunable defaults collected in one place for operator convenience."* §25.4 defines `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60) as operator-facing Helm values; both are referenced in §25.10 and in the `drift-snapshot-refresh.md` runbook trigger (§17.7 line 787). Neither knob appears in §17.8.1. An operator scanning §17.8.1 for drift-detection tunables finds nothing and may conclude there are none.

This was filed as OPS-006 in iter2 with an explicit two-row recommendation; commit 2a46fb6 did not touch it.

**Impact.** Low. Operator discoverability only; no runtime or security consequence.

**Recommendation.** Add two rows to the §17.8.1 defaults table pointing to §25.10:

| Setting                                                              | Default                                                                      | Reference                                                                          |
| -------------------------------------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| Drift snapshot-staleness warning threshold (`ops.drift.snapshotStaleWarningDays`) | 7 days (0 disables)                                                        | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |
| Drift running-state cache TTL (`ops.drift.runningStateCacheTTLSeconds`)           | 60 s                                                                        | [§25.10](25_agent-operability.md#2510-configuration-drift-detection) |

---

### OPS-009 `issueRunbooks` lookup table missing `DRIFT_SNAPSHOT_STALE` → `drift-snapshot-refresh` mapping [Low]

**Files:** `25_agent-operability.md` §25.7 lookup table (lines 3015–3024), `17_deployment-topology.md` §17.7 (line 697 Path-B preamble).

**Issue.** §25.7 "Path B" defines a canonical mapping from health-API issue codes to runbook slugs, version-controlled in `pkg/gateway/health/runbook_links.go`. The table enumerates 8 codes (WARM_POOL_*, CREDENTIAL_POOL_EXHAUSTED, POSTGRES/REDIS/MINIO_UNREACHABLE, CERT_EXPIRY_IMMINENT, CIRCUIT_BREAKER_OPEN). The iter2 fix added the `drift-snapshot-refresh` runbook in §17.7, but the corresponding issue code / lookup entry was not added to §25.7's table.

The drift-response field `snapshot_stale: true` is, in agent-operability terms, the programmatic trigger for that runbook — it is the precise analogue of `WARM_POOL_LOW`, etc. Omitting it from the lookup table means that an agent reading `GET /v1/admin/drift` and receiving `snapshot_stale: true` does not get a `runbook: drift-snapshot-refresh` pointer via the same convention as every other condition, breaking the agent-operability symmetry the section is built on.

§17.7 line 697 states: *"the entries for `WARM_POOL_EXHAUSTED`, `WARM_POOL_LOW`, `CREDENTIAL_POOL_EXHAUSTED`, `POSTGRES_UNREACHABLE`, `REDIS_UNREACHABLE`, `MINIO_UNREACHABLE`, `CERT_EXPIRY_IMMINENT`, and `CIRCUIT_BREAKER_OPEN` are required by §25.7 Path B."* — the sentence enumerates required entries and does not include the drift one either.

**Impact.** Low. The drift response already carries a human-readable `snapshot_stale_warning` string that tells operators what to do, so agents are not stranded. But agents parsing runbook pointers by convention will miss this one.

**Recommendation.** Add one row to the `issueRunbooks` map literal in §25.7:

```go
"DRIFT_SNAPSHOT_STALE": "drift-snapshot-refresh",
```

And amend the §17.7 line-697 enumeration to include `DRIFT_SNAPSHOT_STALE`. Decide in the same edit whether `snapshot_stale: true` should also surface as an `alert_fired` event on the §25.5 event stream — if yes, also add an alert annotation in §16.5; if no, state explicitly in §25.10 that the signal is API-response-only.

---

## Summary

No regressions. The iter2 tier-label collision (OPS-004) is fully fixed — every flagged site now uses either the new Embedded/Source/Compose Mode labels or an explicit "capacity-Tier N" qualifier, and §17.4 plus §17.6 both carry disambiguation notes covering the three orthogonal axes (local-dev mode × target environment × capacity tier). No remaining "Tier 0" references anywhere in the corpus.

Three low-severity findings re-filed: OPS-007 and OPS-008 are literal carry-forwards of iter2 OPS-005 and OPS-006 that the iter2 fix pass did not touch (iter2 fix commit explicitly lists only OPS-004). OPS-009 is new and reflects a small gap introduced by the iter2 fix: adding the `drift-snapshot-refresh` runbook without also adding its code-to-runbook lookup entry breaks the agent-operability pattern that every other runbook follows. All three are doc-only, two-to-three-line fixes.

The ops management plane — lenny-ops, backup/restore APIs, diagnostic endpoints and doctor/fix, drift detection, install wizard, and §17.7 runbook catalog — remains coherent, with strong cross-section linkage between §17 (packaging/deployment) and §25 (agent operability). Runbook catalog of 16 entries covers the main failure modes; doctor/fix has a narrow, explicit allowlist gated by `admin.doctor.allowedFixes`; bootstrap/operational plane split is still sound.
