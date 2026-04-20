# Operator & Deployer Experience Review
## Findings (Iteration 2)

### Verification of prior findings

- **OPS-001 (bootstrap snapshot staleness) — fixed.** §17.7 ships the `drift-snapshot-refresh.md` runbook triggered on `snapshot_stale: true`; §25.10 returns `snapshot_written_at`, `snapshot_age_seconds`, `snapshot_stale`, and a `snapshot_stale_warning` string (default `ops.drift.snapshotStaleWarningDays = 7`). The runbook's Remediation step 4 makes `POST /v1/admin/drift/snapshot/refresh` a permanent tail of every hotfix runbook.
- **OPS-002 (pool CRD reconciliation direction) — partially addressed.** §17.6 clarifies bootstrap is authoritative and top-level `pools` is ignored, but does not explicitly tell operators to avoid `kubectl edit sandboxwarmpool/sandboxtemplate`. Not re-filed — iter1 rated this Low, recommendation was doc-only, risk unchanged.
- **OPS-003 (`lenny-ops` mandatory emphasis) — fixed.** New §17.8.5 ("Mandatory `lenny-ops` Deployment") states the service is mandatory and chart validation rejects attempts to disable it.

No regressions observed.

---

### OPS-004 "Tier 0/1/2" local-dev labels collide with "Tier 1/2/3" capacity-tier labels inside §17 [Medium]

**Files:** `17_deployment-topology.md` (§17.4, §17.6, §17.7 Day-0 walkthrough line 443, §17.8.2, §17.9).

**Issue.** §17.4 renames local-dev modes to **Tier 0** (`lenny up`), **Tier 1** (`make run`), **Tier 2** (`docker compose up`). §17.8.2 and §17.9 continue using **Tier 1 / Tier 2 / Tier 3** as capacity-planning labels. The tokens "Tier 1" and "Tier 2" now mean two different things inside one section. Concrete collisions:

1. Line 433: "validated in Tier 2; Tier 1 (`make run`) skips preflight" — local-dev sense.
2. Line 443 Day-0 walkthrough: "this walkthrough covers production-style **Tier 2** installs." Reader cannot tell from context whether this means *docker-compose local dev* (Tier-2 local from §17.4) or *capacity Tier 2* (from §17.8.2); line 439 immediately above points to `make run` for local dev, which is local-dev "Tier 1".
3. §17.6 wizard presents "Capacity tier: `tier1|tier2|tier3`" and "Target environment: `local|dev|prod`" without explaining they are independent axes.
4. §17.9.2 line 1192: "Layered with `values-tier1.yaml` for Tier 2 dev" — one sentence requires both tier namespaces.

**Root cause.** §17.4's three-tier rename landed after capacity-tier labels were established; no disambiguating prefix was introduced.

**Impact.** Medium. A deployer reading line 443 ("Tier 2 installs") may plausibly reach for `values-tier2.yaml` (capacity) or for `docker-compose up` (local-dev Tier 2). The install wizard preserves the ambiguity in its question surface.

**Recommendation.** Rename local-dev modes to a non-"Tier" noun and reserve "Tier 1/2/3" for capacity. Suggested:
- Tier 0 (`lenny up`) → **"Embedded Mode"**
- Tier 1 (`make run`) → **"Source Mode"**
- Tier 2 (`docker compose up`) → **"Compose Mode"**

At minimum, rewrite the four collision sites above with explicit "local-dev" vs "capacity" prefixes; line 443 should read "production-style **capacity-Tier 2** installs."

---

### OPS-005 `lenny up` embedded mode lacks a documented upgrade path when embedded-Postgres major bumps [Low]

**Files:** `17_deployment-topology.md` (§17.4 "State and resets"), `10_gateway-internals.md` (§10.5).

**Issue.** §17.4 Tier 0 covers schema migrations against embedded Postgres but says nothing about the case where the Postgres *binary major version* itself is bumped in a Lenny release. `~/.lenny/postgres/` uses a Postgres-major-version-specific on-disk layout; a newer `lenny` binary against a data directory written by an older `embedded-postgres` major will either fail to start or crash with directory incompatibility. No `pg_upgrade` path, no documented `lenny export`/`import`, and no fail-closed check are specified.

**Impact.** Low in v1 (Postgres 16 pinned today) but surfaces on the first Postgres major bump. Operators relying on `lenny up` as a durable local platform — an explicit §17.4 use case — lose state with no warning.

**Recommendation.** Add to §17.4 "State and resets": "`lenny up` reads `~/.lenny/postgres/PG_VERSION` on start; on mismatch, fails with `EMBEDDED_PG_VERSION_MISMATCH` and prints the recovery procedure (`lenny export --to <path>` then `lenny down --purge && lenny up && lenny import`). In-place `pg_upgrade` is explicitly not supported in Tier 0."

---

### OPS-006 Operational defaults table (§17.8.1) omits `ops.drift.*` tunables [Low]

**Files:** `17_deployment-topology.md` (§17.8.1), `25_agent-operability.md` (§25.10).

**Issue.** §17.8.1 advertises itself as "All tunable defaults collected in one place." §25.10 introduces `ops.drift.snapshotStaleWarningDays` (default 7) and `ops.drift.runningStateCacheTTLSeconds` (default 60), both operator-facing knobs. Neither appears in §17.8.1.

**Impact.** Low. An operator scanning §17.8.1 for drift-detection tunables finds nothing and may conclude there are none.

**Recommendation.** Add two rows to §17.8.1 pointing to §25.10 — `ops.drift.snapshotStaleWarningDays` (7 days; 0 disables) and `ops.drift.runningStateCacheTTLSeconds` (60 s).

---

## Summary

Three new findings: one medium (Tier-label collision), two low (embedded Postgres major-version gap, defaults-table completeness). Prior OPS-001 and OPS-003 fixes verified; OPS-002 unchanged but acceptable as previously classified. No regressions. Bootstrap-vs-operational plane split remains sound; runbook coverage is strong (schema-migration-failure, drift-snapshot-refresh, cert-manager-outage, admission-webhook-outage, etcd-operations, stuck-finalizer, gateway-rate-limit-storm all well-structured).
