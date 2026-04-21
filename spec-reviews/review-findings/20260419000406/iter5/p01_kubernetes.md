# Perspective 1 — Kubernetes Infrastructure (iter5)

## Scope and method

Iter5 is a COMPACT re-run of the Kubernetes perspective anchored to the iter4 fix commit (`5c8c86a`). No spec commits have landed since that commit, so this iteration operates on the same tree iter4 declared complete.

Method:
1. Re-read the iter4 K8S findings index (`K8S-040` through `K8S-050`) and their per-finding `Fix applied:` paragraphs.
2. Spot-check the spec at each claimed fix site to verify the fix is present and does not regress invariants elsewhere.
3. Re-evaluate the iter4 Lows (K8S-046 through K8S-050) against the iter5 severity rubric (`feedback_severity_calibration_iter5.md`): forward-compat hedges / defense-in-depth gaps that are not correctness bugs stay at Low.
4. Scan for NEW correctness / reliability / security issues in the K8S surfaces touched by the iter4 fixes (CRD ownership, admission plane, WPC/PSC leader handoff, PDB, preflight inventory).

No new High/Medium/Critical Kubernetes issues were surfaced by the spot-checks. The iter4 Lows remain accurate but all fall into the "forward-compat hedge / defense-in-depth" bucket the calibration note marks as Low-only, so none block convergence.

## Verification of iter4 fixes

### KIN-015. iter4 K8S-040 — Phase 8 `lenny-drain-readiness` deployment [High] [Verified Fixed]

**Section:** `spec/18_build-sequence.md` Phase 8 row; `spec/17_deployment-topology.md` §17.2 item 11.

**Verification:** Phase 8 of `spec/18_build-sequence.md` now contains an explicit "Admission control enforcement — deploy `lenny-drain-readiness`" block that mirrors the Phase 5.8 / Phase 13 template: `replicas: 2`, `podDisruptionBudget.minAvailable: 1`, `failurePolicy: Fail`, `lenny.dev/component: admission-webhook` pod label, `DrainReadinessWebhookUnavailable` alert wiring, and inclusion in both the `lenny-preflight` inventory and `admission_webhook_inventory_test.go`. The Phase 8 milestone row also gained "pre-drain MinIO health check admission gate enforced". The deferral note in Phase 3.5 now resolves in the phase it pointed to.

**Residual risk:** None at High severity. The only adjacent cleanup (the "continuous inventory" gap from K8S-046) remains Low (see KIN-021 below).

---

### KIN-016. iter4 K8S-041 — Admission-webhook NetworkPolicy scope [Medium] [Verified Fixed]

**Section:** `spec/13_security-model.md` §13.2; `spec/17_deployment-topology.md` §17.2.

**Verification:** The §13.2 NetworkPolicy row for admission webhooks is split into (a) a base egress rule keyed on `lenny.dev/component: admission-webhook` allowing only `kube-system` CoreDNS and (b) a narrower egress rule keyed additively on `lenny.dev/webhook-name: drain-readiness` permitting TCP to the gateway internal port. The NET-047/NET-050 canonical-selector invariant is extended with "Exception 2 (NET-068)" permitting additive per-pod keys on egress only; `lenny-preflight` selector-consistency audits flag additive keys on the ingress side as failures. §17.2 documents the additive label's egress-only scope. The least-privilege gap on the seven non-drain webhooks is closed.

---

### KIN-017. iter4 K8S-042 — `lenny-sandboxclaim-guard` PATCH/PUT phase reference [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.1.

**Verification:** The webhook rule now explicitly reads `Sandbox.status.phase` via `SandboxClaim.spec.sandboxRef` rather than the non-existent `SandboxClaim.status.phase`, matching the §6.2 state-machine authority. §4.6.3 enumerates the legal `SandboxClaim.status.phase` values, with the gateway as the sole writer. The double-claim-prevention semantics are intact.

---

### KIN-018. iter4 K8S-043 — PSC circuit-breaker leader-handoff continuity [Medium] [Verified Fixed]

**Section:** `spec/06_warm-pod-model.md` §6.1; `spec/04_system-components.md` §4.6.2, §4.6.3.

**Verification:** The "Circuit-breaker state persistence across PSC leader failover" contract in §6.1 introduces three PSC-owned fields on `SandboxWarmPool.status.sdkWarmCircuitBreaker` — `openedAt`, `openedReason`, `minOpenUntil` — written in the same SSA apply that sets `spec.sdkWarmDisabled: true`. The new leader keeps the breaker open until `minOpenUntil` elapses. `sdkWarmCircuitBreakerMinOpenDuration` defaults to 30 min and is operator-configurable via `scalingPolicy.sdkWarmCircuitBreakerMinOpenSeconds`. The operator override (`circuitBreakerOverride: "enabled"`) unconditionally clears all three status fields. §4.6.3 has the ownership carve-out (PSC owns `status.sdkWarmCircuitBreaker.*`; WPC owns the rest of `status.*`); §4.6.2 adds the responsibility bullet and PSC RBAC gains `get`/`patch` on the `SandboxWarmPool` status subresource. The design accepts rolling-window cold-start after failover as the documented trade-off — the `minOpenUntil` floor ensures a crash shortly after trip cannot silently re-enable SDK-warm.

**Residual risk:** None at Medium. The observer-gap on `SandboxWarmPool.status` (K8S-050) remains Low (KIN-025).

---

### KIN-019. iter4 K8S-044 — Fallback claim path mirror staleness precondition [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.1.

**Verification:** The fallback claim path is now gated on two preconditions: (1) `lenny_agent_pod_state_mirror_lag_seconds ≤ podClaimFallbackMaxMirrorLagSeconds` (default 10 s) and (2) a lightweight API-server readiness probe (needed because `lenny-sandboxclaim-guard` is `failurePolicy: Fail`). Failure of either precondition returns `WARM_POOL_EXHAUSTED` and increments `lenny_pod_claim_fallback_skipped_total{reason=mirror_stale|apiserver_unreachable}`. Separately, the WPC now bulk-UPSERTs `agent_pod_state` from a fresh `Sandbox` list on startup and every leader-election acquisition and deletes orphan rows, establishing a post-outage convergence invariant. This closes both the stale-mirror double-claim window and the `failurePolicy: Fail` collapse window.

---

### KIN-020. iter4 K8S-045 — `lenny-pool-config-validator` PSC wedge [Medium] [Verified Fixed]

**Section:** `spec/04_system-components.md` §4.6.3; `spec/16_observability.md`; `spec/18_build-sequence.md` Phase 3.5.

**Verification:** §4.6.3 now specifies the PSC's admission-denial policy: HTTP 400/403 denials trigger exponential backoff (initial 1 s, max 60 s), increment `lenny_pool_scaling_admission_denied_total{pool, reason}`, and — after 10 consecutive denials on the same pool — emit `PoolScalingAdmissionStuck` and stop retrying until operator intervention. Phase 3.5 integration tests include the stale-Postgres scenario. The wedge loop is broken without weakening the rule-set-1 enforcement on PSC writes.

---

## Carry-forward Lows (no blocker under severity calibration)

### KIN-021. iter4 K8S-046 — `lenny-preflight` detects drift only at install/upgrade [Low] [Carry-forward]

**Status:** Not addressed in iter4 fix commit. Remains Low.

Under the iter5 calibration this is a forward-compat / defense-in-depth hedge — the documented `*Unavailable` alerts cover reachability of deployed webhooks, and `helm rollback` / `kubectl delete` are out-of-band admin actions outside the build-sequence contract. Elevation would only be warranted if a shipping feature relied on continuous-inventory detection; none does. A continuous inventory check is a reasonable hardening for a future iteration but not a correctness defect today.

### KIN-022. iter4 K8S-047 — Kata PDB scope [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

Cross-template simultaneous drain on a small Kata node pool is a capacity-planning concern already implicit in §17.8 sizing guidance. Single-template `maxUnavailable: 1` remains correct per Kubernetes PDB semantics. Elevation to Medium requires a shipping contract guaranteeing cross-template concurrent-disruption bounds, which is not promised.

### KIN-023. iter4 K8S-048 — Admission-webhook topology spread [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

Zonal-failure HA for admission webhooks is a deployment hardening lever; `replicas: 2` + `PDB minAvailable: 1` provides voluntary-disruption protection, which is what the spec contracts. Topology spread is valuable but is a forward-compat hedge per the calibration note.

### KIN-024. iter4 K8S-049 — `Sandbox` finalizer orphan-reclaim leader fence [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

The failure mode requires a narrow WPC leader-failover window between the `SandboxClaim` DELETE and the `Sandbox` `claimed → idle` transition. The existing orphan-`SandboxClaim` GC loop will recover the next cycle if the in-flight state is re-observed; the stranded `claimed`-but-no-claim case is a narrow, recoverable gap. An idempotent reclaim protocol listing both orphan claims and stranded-`claimed` Sandboxes is a reasonable hardening; not a blocker.

### KIN-025. iter4 K8S-050 — PSC observer gap on `SandboxWarmPool.status` [Low] [Carry-forward]

**Status:** Not addressed. Remains Low.

The PSC's scaling formula can tolerate the WPC's 25 s failover window because target sizing is expressed in pods-per-second-of-lag rather than instantaneous state; the formula already includes `failover_seconds + pod_startup_seconds` headroom. A staleness guard on `status.lastTransitionTime` is a legitimate hardening but the absence of one is not a correctness defect at the specified scale.

---

## New issues surfaced in iter5

None. Spot-checks of the iter4 fix sites (§4.6.1 fallback preconditions, §4.6.1 admission-guard phase reference, §4.6.2 PSC status ownership carve-out, §4.6.3 admission-denial backoff, §6.1 circuit-breaker persistence, §13.2 NetworkPolicy split, §17.2 additive label exception, §18 Phase 8 drain-readiness deployment) did not turn up regressions, contradictions with adjacent invariants, or new correctness bugs.

The iter4 baseline list of WPC/PSC leader-election hazards, admission-plane HA contracts, and CRD ownership boundaries is closed to the Medium/High bar after iter4's fixes.

---

## Convergence assessment

**Counts (new findings this iteration):**
- Critical: 0
- High: 0
- Medium: 0
- Low: 5 (all iter4 K8S-046 – K8S-050 carry-forwards; re-labelled KIN-021 – KIN-025 for continuity)
- Info: 0

**Verified fixed from iter4:** K8S-040 (High), K8S-041 (Medium), K8S-042 (Medium), K8S-043 (Medium), K8S-044 (Medium), K8S-045 (Medium) — six fixes held.

**Converged (Y/N):** **Y** — zero new Critical/High/Medium findings; all prior C/H/M findings verified fixed; remaining Lows are forward-compat hedges that do not block convergence under the iter5 severity rubric.
