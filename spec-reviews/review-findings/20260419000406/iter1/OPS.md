# Operator & Deployer Experience Review
## Findings (Iteration 1)

### OPS-001 Bootstrap Snapshot Stale During Operational Phase [Medium]

**Files:** `17_deployment-topology.md` (§17.6), `25_agent-operability.md` (§25.10)

**Issue:**
The `bootstrap_seed_snapshot` table is updated only at two well-defined points: early in OpsRoll and at upgrade completion (Verification phase). Between upgrades, if an operator makes manual runtime or pool changes via the admin API (e.g., `PUT /v1/admin/pools/{name}` to adjust scaling parameters, or `POST /v1/admin/runtimes` to register a new runtime), the `bootstrap_seed_snapshot` row in Postgres becomes stale and no longer reflects running state.

Then when drift detection runs (`GET /v1/admin/drift`), it compares running state against a snapshot that is no longer the desired state. An operator who modified a pool's `maxWarm` parameter at 3 PM will see drift reported against a snapshot frozen at the last upgrade completion (possibly days or weeks earlier). Section 25.10 acknowledges this ("if the operator updated Helm values but the snapshot wasn't refreshed ... all subsequent drift detection runs against an out-of-date desired state") but this is a daily operational problem, not an edge case.

**Root cause:**
The spec requires operators to call `snapshot/refresh` themselves after API mutations, but provides no automation or reminder mechanism. For GitOps-driven deployments (the stated design intent), this is workable — every change goes through Helm. For hybrid deployments (some changes via Helm, some via API for emergency tweaks), the snapshot can silently diverge from reality.

**Impact:**
Drift reports during operational phases (between upgrades) can be misleading. Agents relying on drift to detect "what has drifted from desired state" will encounter false positives (changes that were intentional manual tweaks) or false negatives (if the operator forgets to refresh, future drift detection is blind to the manually-changed baseline).

**Recommendation:**
Document that snapshot staleness is an operator responsibility, or: (1) emit a warning in the drift response if the snapshot age exceeds a configurable threshold (e.g., `>7 days since refresh` with a threshold in ops config), directing operators to `POST /v1/admin/drift/snapshot/refresh`, and (2) provide a runbook step recommending `snapshot/refresh` as a post-hotfix cleanup task. Alternatively, require snapshot refresh automatically when any admin API mutation occurs on pools/runtimes/tenants (at the cost of Postgres write overhead on every API call). Status quo is acceptable if explicitly documented as an operator discipline; verify that §17.7 runbooks all include snapshot refresh in remediation paths when they modify pool/runtime state.

---

### OPS-002 Pool Configuration Reconciliation Direction Underspecified [Low]

**Files:** `17_deployment-topology.md` (§17.6), `04_system-components.md` (§4.6.2)

**Issue:**
Section 17.6 states: "The bootstrap section is the **AUTHORITATIVE source** for pool definitions — the lenny-bootstrap Job writes pools to Postgres via the admin API, and the **PoolScalingController reconciles CRDs from Postgres.**" However, the PoolScalingController's responsibilities in §4.6.2 include "Reconcile pool configuration from Postgres (admin API source of truth) into `SandboxTemplate` and `SandboxWarmPool` CRDs."

This is correct — Postgres is the source, CRDs are the derived state. But the spec does not explicitly state: "Operators must not directly edit `SandboxTemplate` or `SandboxWarmPool` CRDs; they must use the admin API or bootstrap seed." If an operator accidentally `kubectl edit sandboxwarmpool echo` and changes `spec.minWarm`, what happens?

**Root cause:**
The bootstrap comment clarifies that bootstrap is the sole source of pool *definitions* (runtime, resources, etc.), but does not clarify the constraint that **all pool mutations after bootstrap must go through the admin API**, not direct CRD edits.

**Impact:**
Low. The `PoolScalingController` will reconcile from Postgres and overwrite the manual CRD edit on the next reconciliation cycle, so data is not lost. But an operator who makes a manual CRD edit may be surprised when it is silently reverted, and they lack guidance on the correct path (admin API).

**Recommendation:**
Add a sentence to §17.6 under "Bootstrap seed mechanism": "After bootstrap, all pool configuration changes must be made through the admin API (`lenny-ctl admin pools` or `PUT /v1/admin/pools`); do not edit `SandboxTemplate` or `SandboxWarmPool` CRDs directly. The PoolScalingController automatically reconciles Postgres state into CRDs, overwriting any manual edits."

---

### OPS-003 `lenny-ops` Mandatory for All Deployments But Not Documented As Blocker [Low]

**Files:** `25_agent-operability.md` (§25.1, §25.2), `17_deployment-topology.md` (§17.1)

**Issue:**
Section 25.1 states clearly: "`lenny-ops` is mandatory in every Lenny installation regardless of tier. There is no supported topology without it — the features it hosts have no alternative path." This is correct and important.

However, this critical constraint is buried in §25.1 operational philosophy text, not highlighted in the deployment topology section (§17.1) where operators deciding "what to deploy" would first look. An operator reading §17.1's "Kubernetes Resources" table will see `lenny-ops` listed with description "Mandatory in every tier" but will not see explicit emphasis that removing it breaks drift detection, audit, runbooks, backup/restore, and platform upgrades.

**Root cause:**
The constraint is stated but not positioned as a "you cannot skip this component" blocker. A deployer skimming §17.1 might treat `lenny-ops` as optional and remove it to "save costs" or reduce complexity, then discover later that runbooks, drift detection, and backups are nonfunctional.

**Impact:**
Low operational risk but high support burden. A deployer who removes `lenny-ops` silently loses operability features without immediate failure signal.

**Recommendation:**
Add a prominent note to §17.1 after the `lenny-ops` row: "**Note:** `lenny-ops` is not optional. It is the exclusive host for operability endpoints (drift detection, runbooks, platform upgrade, audit queries, backup/restore). The gateway `health` endpoints are a fallback only; `lenny-ops` must be deployed and healthy for standard operations. Removing it disables all operations features listed in Section 25."

---

## Summary

Three findings identified: one medium (snapshot staleness during operations), two low (CRD edit guidance, `lenny-ops` deployment emphasis). No cross-section inconsistencies requiring spec correction. Bootstrap-vs-operational plane split is sound; documentation improvement would help operator adoption.

**Verified:** All runbooks in §17.7 checked for snapshot refresh: emergency revocation, credential pool exhaustion, gateway replica failure — none mention snapshot refresh post-remediation. This is acceptable for incident runbooks (caller has bigger problems); drift consistency is an inter-upgrade concern, not an immediate incident response step. OPS-001 requires documentation audit of GitOps vs. hybrid deployment guidance; no code change needed.
