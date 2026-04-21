# Iter6 — Perspective 16: Warm Pool & Pod Lifecycle

**Scope:** `spec/06_warm-pod-model.md`, `spec/07_session-lifecycle.md`, `spec/04_system-components.md` §4.6.1 / §4.6.2 / §4.6.3, `spec/10_gateway-internals.md` §10.1 (preStop), `spec/16_observability.md` §16.1 / §16.5, `spec/05_runtime-registry-and-pool-model.md` §5.2.

**Prior:** iter5 reported all four iter4 WPL findings HELD with cross-document consistency and no new defects — "**Converged for warm-pool & pod-lifecycle scope**". No Skipped/Deferred iter4 WPL items. Iter1, Iter2, Iter3 all "No real issues found." in the WPL scope.

**Severity rubric (anchored to iter1-iter5 WPL calibration):**
- **High:** silent data loss, cluster-wide deadlock, orphan-at-scale, cross-tenant leakage, unrecoverable state divergence.
- **Medium:** observability blind spot on a P1 failure mode, contract ambiguity that could diverge implementations, latent RBAC gap.
- **Low:** wording clarity, cross-reference completeness, cosmetic doc inconsistencies.
- **Info:** non-actionable observation.

---

## Iter5 carry-over audit

All iter5 carry-overs originate from the iter4 WPL batch (WPL-001 → WPL-004). iter5 confirmed each HELD. iter6 re-verifies at the current spec anchors.

| ID      | Iter4 severity | Iter5 status | Iter6 anchor | Iter6 verification |
|---------|----------------|--------------|--------------|--------------------|
| WPL-001 | High           | HELD         | `spec/06_warm-pod-model.md` lines 152–155 (four `task_cleanup → {draining, sdk_connecting}` preConnect edges, each guarded by host-node schedulability with explicit `scrub_warning` branches) | HELD — the state diagram still splits the four preConnect re-warm edges (plain success, `scrub_warning` success-with-warning, plain unschedulable-drain, `scrub_warning` unschedulable-drain) and explicitly forbids SDK re-warm on a cordoned node. The `scrub_warning` branches preserve the annotation through the re-warm path. No regression. |
| WPL-002 | High           | HELD         | `spec/04_system-components.md` line 481 (WPC-owned `lenny.dev/host-schedulable` label) + line 586 (WPC Nodes `get`/`list`/`watch` RBAC) + line 588 (Gateway SA has no Node verbs) + `spec/06_warm-pod-model.md` line 181 ("Host-node schedulability precondition" narrative) | HELD — labeling is WPC-owned with field manager `lenny-warm-pool-controller`; the label is explicitly carved out of the `lenny-label-immutability` webhook's immutable set so WPC can flip it on cordon/uncordon; gateway reads via existing `get` on `Pods` with **no** Node verbs on its ServiceAccount; the WPC grants `get`/`list`/`watch` on `Nodes` for the informer. Label absence is treated as unschedulable (fail-safe). Cross-doc consistency across spec/04, spec/06 preserved. No regression. |
| WPL-003 | Medium         | HELD         | `spec/16_observability.md` line 41 (metric), line 458 (alert); `spec/10_gateway-internals.md` line 114 (narrative) | HELD — `lenny_prestop_cap_selection_total` carries `service_instance_id` (OTel `service.instance.id`) alongside `pool` and `source`; `PreStopCapFallbackRateHigh` aggregates with `sum by (service_instance_id, pool)` so cold-cache replicas are not masked by fleet averaging; the narrative in spec/10 §10.1 cross-references `§16.1.1` attribute naming. All three documents remain mutually consistent. No regression. |
| WPL-004 | Medium         | HELD         | `spec/06_warm-pod-model.md` line 152 + 153 (draining branches for unschedulable node, both scrub-success and scrub-warning) + line 181 precondition narrative; reinforced by `spec/04_system-components.md` §4.6.3 ownership carve-out at line 571 | HELD — both unschedulable-drain branches remain distinct from the schedulable re-warm branches; the precondition paragraph names the WarmPoolController as sole evaluator, describes per-reconcile label maintenance, names the `lenny.dev/host-schedulable` pod label as the gateway's sole input, and covers the `label absent → treated as unschedulable` fail-safe. The carve-out row for `status.sdkWarmCircuitBreaker.*` (PSC-owned) confirms ownership boundaries are still disjoint. No regression. |

**No Skipped/Deferred iter4 WPL items.** iter5 produced no new WPL findings, so there are no iter5-origin carry-overs.

## New findings

None.

Scans performed on iter6 delta (changes since iter5 close, reviewing all sections in the WPL scope):

1. **Pod state machine integrity** (`spec/06_warm-pod-model.md` §6.2, full state diagram including non-preConnect, preConnect, concurrent-workspace, task-mode, and per-slot sub-states): all transitions remain well-formed; no dangling terminal states; `leaked` slot semantics preserved (counted toward unhealthy threshold, replacement triggered, `lenny_adapter_leaked_slots` gauge exposed). The `attached → resume_pending` retry-on-new-pod path is well-distinguished from session-mode resume.
2. **Host-node schedulability precondition** (line 181): wording intact including fail-safe default on absent label, WPC as sole evaluator, per-reconcile re-labeling on cordon/uncordon, and explicit absence of Node informer / Node verbs in the gateway.
3. **WarmPoolController §4.6.1** (lines 475–493): PDB strategy (`maxUnavailable: 1`) rationale intact; finalizer `lenny.dev/session-cleanup` with 5-min stuck-finalizer alert and runbook reference preserved; orphan claim GC explicitly assigned to the elected leader only (prevents gateway-replica fan-out on API server); go/no-go dependency criteria and fallback plan present.
4. **PoolScalingController §4.6.2** (lines 495–561): separate Lease name, same 15s/10s/2s parameters, independent failover model; variant-pool formula clamps `Σ variant_weights` to `[0, 1)` and aggregates across all active experiments in a single pass (no last-write-wins); `PoolConfigDrift` alert fires from gateway (not PSC) so it survives PSC downtime; `status.sdkWarmCircuitBreaker.*` persistence with leader-handoff continuity documented at §6.1 cross-reference.
5. **CRD ownership §4.6.3** (lines 566–594): disjoint ownership preserved; SSA conflict retry policy mandates re-read and forbids `--force-conflicts`; RBAC grants match ownership table (WPC has Node `watch` for informer; PSC has `get`/`patch` on `SandboxWarmPool` `status` subresource for the circuit-breaker carve-out; Gateway SA has no Node or `Sandbox` `spec` verbs). `SandboxClaim.status.phase` enumeration is gateway-owned and properly distinct from `Sandbox.status.phase`.
6. **preStop §10.1** (lines 97–175): four-stage drain sequence intact; tiered checkpoint cap fallback documented with the four source values; `CheckpointBarrier` protocol produces a partial manifest on timeout; intent-row-first ordering for orphan prevention preserved; `service_instance_id` tagging consistent with §16.1.
7. **§16.1 / §16.5 observability**: metric row (line 41), alert row (line 458), and narrative (spec/10 line 114) are mutually consistent; no stale reference after any fix in iter5. `PreStopCapFallbackRateHigh` PromQL uses per-replica grouping that cannot be silently degraded by a cluster-wide aggregate.
8. **§5.2 execution modes integration**: `ceil(maxConcurrent/2)` unhealthy threshold for concurrent-mode combines `failed + leaked` slots in a rolling 5-min window; stabilization delay (5s `active → idle`) prevents label oscillation; `terminationGracePeriodSeconds` validation via CRD webhook preserved.

No new Critical, High, Medium, or Low findings. No regressions introduced by iter5's cross-cutting fixes (CRD-015, CMP-054, CMP-057, CMP-058, NET-070) in the WPL surface area — those fixes do not touch the pod lifecycle state machine, schedulability labeling, preStop tier selection, or PSC/WPC ownership boundaries.

## Convergence assessment

**Converged for warm-pool & pod-lifecycle scope (iter6).**

Rationale:
- All four iter4 WPL findings remain HELD at the same spec anchors reviewed in iter5, with no drift in wording or cross-references.
- iter5 produced no new WPL findings; iter6 produces no new WPL findings.
- No regressions detected from iter5's fixes elsewhere in the spec (security, compliance, networking): those fixes are localized to their respective scopes and do not mutate any pod lifecycle, WPC/PSC ownership, preStop tier-selection, or schedulability-labeling contract.
- Iter1, Iter2, Iter3 all closed with "No real issues found" on WPL scope; iter4 introduced the four remaining findings that were fully resolved; iter5 re-verified; iter6 re-verifies once more at the same anchors.

Three consecutive iterations (iter4-fix → iter5 → iter6) with no new WPL defects and no broken carry-overs satisfies the convergence criterion for this perspective.
