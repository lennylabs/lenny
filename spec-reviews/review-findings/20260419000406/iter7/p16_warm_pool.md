# Perspective 16 — Warm Pool & Pod Lifecycle Management (Iteration 7)

Category: **WPL**
Iteration: **7**
Reviewer focus: Evaluate the warm pool model for correctness, efficiency, and operational complexity. Must-check items: SDK-warm mode complexity vs. latency benefit tradeoff; `sdkWarmBlockingPaths` demotion mechanism; Pool sizing formulas under burst traffic; Pod eviction during SDK-warm; Experiment variant impact on pool sizing and waste. Specific focus for iter7: NEW issues introduced by iter6 fixes (commit `8604ce9`), especially whether the circuit-breaker admin endpoints in §15.1 interact with pool management.

Severity calibration anchors: `feedback_severity_calibration_iter5.md` (carry-forward severities are NOT re-inflated; new findings are calibrated against the same rubric used by iter4/iter5/iter6).

---

## 1. Prior-Iteration Carry-Forward Audit

Iter6 closed with **0 new findings** for WPL and 4 iter4 carry-overs held at **Low/Info** pending post-implementation telemetry. Each anchor was re-verified against the current spec text.

| ID      | Original severity | Title                                                                                                          | Anchor (spec)                                                                                                                                  | Iter7 status | Notes |
|---------|-------------------|----------------------------------------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------------------------|--------------|-------|
| WPL-001 | Low               | `Claimed→Terminating` transition via in-band controller message relies on operational discipline               | `spec/06_warm-pod-model.md` §6.3 state machine; `spec/04_system-components.md` §4.6 WPC/PSC ownership                                           | HELD         | No change in iter6 fixes. Remains a post-implementation telemetry item (observe state-diagram violations in practice). |
| WPL-002 | Low               | Warm-pool labeling field-manager ownership declared only in prose, not in a field-manager table                 | `spec/04_system-components.md` §4.6.3 ownership text (host-schedulability labeling); `spec/06_warm-pod-model.md` §6.4 status layout            | HELD         | Iter6 did not add a formal field-manager table; workaround still acceptable because each field has a single declared owner. Low severity preserved. |
| WPL-003 | Low               | `lenny_prestop_cap_selection_total` per-replica grouping using `service_instance_id`                           | `spec/16_observability.md` L41 metric def; L462 `PreStopCapFallbackRateHigh` alert                                                              | HELD         | Metric + alert unchanged by iter6 fixes. Per-replica grouping preserved; monitor post-impl. |
| WPL-004 | Info              | Host-node schedulability precondition (`lenny.dev/host-schedulable`) relies on single-owner field-manager norm | `spec/06_warm-pod-model.md` §6 (line ~181) precondition; `spec/04_system-components.md` §4.6 WPC labeling contract                             | HELD         | Contract intact after iter6 fixes. Remains Info pending operational confirmation. |

No prior carry-over changed severity or was closed or escalated.

---

## 2. New Findings (Iter7)

**None.**

Focused scans performed against the current spec state (post commit `8604ce9`, iter6 fixes):

1. **Circuit-breaker cross-check (iter6 delta).** §15.1 added `GET/POST /v1/admin/circuit-breakers` and `GET/POST /v1/admin/circuit-breakers/{name}` (operator-managed admission-gate breakers, Redis-backed, `limit_tier ∈ {runtime, pool, connector, operation_type}`). These are semantically distinct from §4.6.2’s PSC-managed **SDK-warm circuit breaker** (auto-trip at 90% demotion rate, persisted in `SandboxWarmPool.status.sdkWarmCircuitBreaker`, manually overridable via `PUT /v1/admin/pools/{name}/circuit-breaker`). The two surfaces share a name but operate on different state: the operator breaker gates admission, the SDK-warm breaker gates SDK pre-connect demotion. The iter6 additions do not redefine, override, or shadow the SDK-warm breaker’s contract. No WPL regression.
   - Minor taxonomy note: §11.6 says "Two types of circuit breaker" while effectively three exist (automatic subsystem, operator admission, PSC SDK-warm). This is **pre-existing wording** and documentation-scope — not introduced by iter6, not WPL-actionable. Flagged for the API/docs perspective if not already captured there.
2. **SDK-warm mode complexity vs. latency tradeoff.** §6.2/§6.3 define `preConnect: true` gating through `capabilities.preConnect.supported`; the demotion mechanism (`sdkWarmBlockingPaths` default `["CLAUDE.md", ".claude/*"]`), the session-token flag propagation, the PSC-managed circuit breaker with leader-handoff continuity, and the observability surface (`lenny_sdk_warm_demotion_total`, `SDKConnectTimeout` alert) remain coherent. Iter6 did not touch this area. No new defect.
3. **Pool sizing formula under burst traffic.** §6.4 formula `ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_warmup_seconds) + burst_p99_claims × pod_warmup_seconds)` is unchanged. Variant-weight clamping `Σ variant_weights ∈ [0, 1)` with CRD validation is unchanged. No iter6 delta touches this; no new defect.
4. **Pod eviction during SDK-warm.** §10.1 preStop Stage 2 tiered-cap protocol (30/60/90s tiers), `service_instance_id`-grouped metric, `BarrierAck` budget CRD validation rule, and CheckpointBarrier protocol remain intact. No iter6 regression; no new defect.
5. **Experiment variant impact.** §6.4 + §21 variant weighting, the clamp at `< 1.0`, and the pool-sizing roll-up are unchanged. No new waste-related concern surfaced.
6. **WPC ↔ PSC ownership boundary.** §4.6.1/§4.6.2 Lease-based independent leader election, CRD field ownership (WPC: `spec.template`, host-schedulability label; PSC: `status.sdkWarmCircuitBreaker`, desired-replica ledger entries) unchanged by iter6.
7. **Admin-endpoint interaction with pool state.** `PUT /v1/admin/pools/{name}/circuit-breaker` (SDK-warm override, PSC-scoped) remains the sole operator-facing pool-level breaker surface; the new `/v1/admin/circuit-breakers/*` endpoints target admission-gate breakers keyed by `cb:{name}`, and §11.6 explicitly uses `limit_tier: "pool"` + `scope.pool: Y` to scope the breaker to a pool — this is additive and does not displace the PSC surface. No ambiguity at the API boundary that would cause an operator to mutate the wrong breaker accidentally (distinct paths, distinct payloads, distinct `kind` fields).
8. **State machine invariants.** `Provisioning → Initializing → Ready → Claimed → Terminating → Terminated` and the short-circuit `Initializing → Terminated` remain the only legal transitions. No iter6 fix adds an edge.

---

## 3. Convergence Assessment

- Iter4 introduced 4 Low/Info findings (WPL-001 through WPL-004). None were re-severity-inflated in iter5, iter6, or iter7.
- Iter5 added 0 new WPL findings.
- Iter6 added 0 new WPL findings.
- Iter7 adds 0 new WPL findings.

**Four consecutive iterations (iter4→iter7) with no new Critical/High/Medium defects in the WPL scope, and no broken carry-overs.** The SDK-warm complexity, pool sizing, variant weighting, preStop, and host-schedulability contracts are internally consistent and correctly decoupled from the iter6 circuit-breaker admin-endpoint additions.

**Verdict: CONVERGED** for Warm Pool & Pod Lifecycle Management scope. The 4 existing Low/Info carry-overs remain as post-implementation telemetry items (no spec change needed); they should be validated once real operational data is available.

---

## 4. Summary

- Perspective: 16 — Warm Pool & Pod Lifecycle Management
- Category: WPL
- New findings (iter7): **0**
- Carry-overs held: 4 (WPL-001 Low, WPL-002 Low, WPL-003 Low, WPL-004 Info)
- Verdict: **Converged** (4th consecutive iteration of zero new defects)
