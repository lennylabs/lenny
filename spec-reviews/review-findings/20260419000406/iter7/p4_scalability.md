# Iteration 7 — Perspective 4 (Scalability & Performance Engineering)

**Category ID:** SCP (Scalability / Capacity / Performance)
**Reviewer:** Scalability & Performance Engineering
**Prior iteration baseline:** iter5 `p04_scalability.md` (iter6 deferred due to sub-agent rate-limit exhaustion; iter5 remains the most recent perspective-level baseline).
**Severity calibration:** Anchored to the iter5 rubric — Critical/High/Medium reserved for defects that break a sizing formula, invalidate a capacity target, or render a tier unreachable with the documented architecture; Low covers narrative/derivation tightness, documentation-vs-spec coherence, and bounded operator-experience issues. Per `feedback_severity_calibration_iter5.md`, new-iteration findings do not escalate prior-tier severities without a structural change.

---

## 1. Prior-iteration carry-forwards

Four Low findings from iter5 remain open as of iter7 (iter6 did not execute for this perspective; the iter5-fix commit `c941492` focused on P11–P25 perspective work, with no changes to the SCL-related scalability derivations). All four are re-filed under the SCP namespace and retained at **Low** per severity calibration.

### SCP-001 (carry-forward of iter5 PRF-013) — Low

**Title:** Stream Proxy `maxConcurrent` per-replica still lacks streams-per-session derivation.

**Location:** `spec/17_deployment-topology.md` §17.8.2 (Capacity Tier Reference, Gateway and API layer table); cross-reference `docs/reference/configuration.md` §"Streams and proxies".

**Finding.** The tier table publishes `Stream Proxy maxConcurrent = 200 / 2,000 / 20,000` for Tier 1/2/3 alongside `maxSessionsPerReplica = 50 / 200 / 400`. The relationship between sessions and concurrent streams (streams-per-session multiplier, LLM-stream vs upload-stream breakdown, and whether WebSocket/SSE keep-alives are counted) is not derived inline — operators who change `maxSessionsPerReplica` cannot recompute `maxConcurrent` from first principles. This remains a sizing-transparency gap rather than a formula error; at default values the published ratios (4:10:50 streams/session across tiers) are self-consistent but un-explained.

**Why Low.** Values are internally consistent at defaults; the gap is derivational not structural. No tier is unreachable.

**Recommendation.** Add a paragraph (~3 lines) next to the tier table clarifying: (a) the multiplier used to get from `maxSessionsPerReplica` to each subsystem's `maxConcurrent`; (b) which subsystem dominates at which tier (LLM Proxy at Tier 3 — hence the 25× uplift from session count); (c) a one-line invariant operators must preserve when tuning `maxSessionsPerReplica`. No code or tier changes required.

---

### SCP-002 (carry-forward of iter5 PRF-014) — Low

**Title:** HPA scale-up `stabilizationWindowSeconds: 0` coexists with PDB-bound ~25-minute scale-down.

**Location:** `spec/17_deployment-topology.md` §17.8.2 rows "HPA scale-up stabilization window" (0s at every tier) and "Gateway scale-down time" (up to ~25 min Tier 3, PDB-bound).

**Finding.** The asymmetry is intentional and the spec narrative defending the PDB-bound floor is thorough (the §17.8.2 multi-paragraph treatment introduced in iter5 addresses the mechanism and operator signals). However, one downstream consequence remains undocumented: with zero scale-up stabilization the HPA can aggressively add replicas during a transient request-queue-depth spike, and — because scale-down is rate-limited to `1 pod / 60s` and capped by `maxUnavailable: 1` — those replicas persist for 25+ minutes even after the spike subsides. Operators monitoring cost-per-session may see material overhead when spike patterns are frequent but short (<5 min). The spec does not mention this cost-visibility implication or suggest a cost-optimization knob (`scaleUp.stabilizationWindowSeconds: 15–30s` would trade a small burst absorption penalty for lower long-tail replica count).

**Why Low.** Availability trade-off is deliberate; this is a cost-transparency gap not a capacity defect.

**Recommendation.** Add a one-line note in §17.8.2 after the PDB-bound scale-down paragraph acknowledging the cost trade-off and pointing operators at the `scaleUp.stabilizationWindowSeconds` knob if their spike profile skews toward short bursts. No structural change.

---

### SCP-003 (carry-forward of iter5 PRF-015) — Low

**Title:** Tier 3 gateway replica-failure headroom superposition math still not shown.

**Location:** `spec/17_deployment-topology.md` §17.8.2 Tier 3 rows (`maxReplicas: 30`, `maxSessionsPerReplica: 400`, 12,000-session capacity for 10,000 target → 20% headroom).

**Finding.** At Tier 3, `30 × 400 = 12,000` capacity for a 10,000-session target leaves 20% headroom. Losing a single replica removes `1 / 30 ≈ 3.3%` of capacity (400 sessions), leaving 11,600 > 10,000; simultaneous loss of 2 replicas leaves 11,200 (still covered); 3 replicas leaves 10,800 (narrow). The spec does not present this superposition explicitly, so an operator reading the 20% headroom row cannot tell how many concurrent replica failures the architecture absorbs before 429s appear. The `Minimum healthy gateway replicas (alert)` row at `5` gives the *alert threshold* but not the *capacity floor*.

**Why Low.** Architecture is internally sound; missing only a clarifying derivation.

**Recommendation.** Add a two-line callout after the Tier 3 column: "At `maxReplicas: 30` and `maxSessionsPerReplica: 400`, Tier 3 absorbs up to 5 concurrent replica losses (25 × 400 = 10,000) at the 10,000-session target; losing a 6th replica triggers 429s. Operators tuning these two values must preserve `(maxReplicas − expected_concurrent_failures) × maxSessionsPerReplica ≥ tier_session_target`."

---

### SCP-004 (carry-forward of iter5 PRF-016) — Low

**Title:** Tier 4 (Platform) capacity targets lack a per-replica scaling derivation.

**Location:** `spec/16_observability.md` §16.5 Capacity tiers row `Per-replica session capacity budget (maxSessionsPerReplica)` — Tier 4 entry "400 (provisional — same per-replica budget as Tier 3; scale-out via replica count and infrastructure changes below)"; cross-referenced `spec/04_system-components.md` §4.1 Tier 4 entry; `spec/17_deployment-topology.md` §17.8.2 tier table (Tier 4 column absent from most rows).

**Finding.** Tier 4 targets 100,000 concurrent sessions at `maxSessionsPerReplica = 400`, implying ~250 gateway replicas. The spec commits to `maxSessionsPerReplica: 400` as "provisional — same per-replica budget as Tier 3" but:
1. The sizing table in §17.8.2 does not add a Tier 4 column (gateway replica range, HPA queue-depth target, stream-proxy `maxConcurrent`, preStop drain timeout, `terminationGracePeriodSeconds`, scale-down time). Operators cannot compute `minReplicas` from the `(burst_arrival_rate × pipeline_lag) / sessions_per_replica` formula for Tier 4 (2000/s × 20s / 400 = 100 minReplicas); the KEDA-path formula works for 2,000/s burst arrival but the supporting table entries don't exist.
2. The Tier 4 "indicative" disclaimer is correct but leaves open how the same `maxSessionsPerReplica: 400` that was "provisional — requires LLM Proxy extraction" at Tier 3 holds at 10× scale without further extraction or tuning — i.e., the assumption carry-over from Tier 3 to Tier 4 is not defended.
3. With 250 replicas and the flat `maxUnavailable: 1` PDB, a rolling update or KEDA-triggered scale-down of all replicas takes `250 × 60s ≈ 4.2 hours` (best case) or `250 × 120s ≈ 8.3 hours` (worst case) at the documented PDB floor — effectively prohibitive for routine upgrades. This is explicitly acknowledged for Tier 3 ("~25 min") but no Tier 4 row or Tier 4 PDB-tuning guidance appears.

**Why Low.** Tier 4 is labelled "post-GA scaling milestone" and "not a GA deliverable" in §16.5; the spec frames the targets as indicative and contingent on §12.6 interface swaps. The gap is thus projection/narrative rather than a GA blocker. If Tier 4 were on the GA path this would be Medium.

**Recommendation.** Either (a) add a Tier 4 advisory column to §17.8.2 with derived values plus an explicit "re-calibration required at Phase 14.5" note; or (b) tighten the §16.5 Tier 4 disclaimer to call out the specific open items (gateway replica count, PDB-relaxation for rollout time, per-subsystem `maxConcurrent` extrapolation, 400-per-replica assumption validity at 10× session rate). Option (b) is cheaper and matches the "post-GA" posture.

---

## 2. New findings (iter7)

Four Low-severity findings surfaced during this pass. Three are narrative gaps around spec additions made since iter5 (MinIO burst headroom, KEDA Tier 3 brittleness, Tier 3 single-tenant Redis topology detection). One (SCP-008) is a capacity-planning coherence issue between `§17.8.2` ResourceQuota and the SDK-warm `minWarm` upper bound. None are Critical/High/Medium under the iter5 rubric — architecture consistency holds and no tier becomes unreachable.

### SCP-005 — Low

**Title:** Tier 3 KEDA `minReplicas: 5 / maxReplicas: 30` validity is conditional on the aggressive scale-up policy but coupling is not enforced as an invariant.

**Location:** `spec/17_deployment-topology.md` §17.8.2, "Tier 3 note (KEDA path)" paragraph (after the Path A KEDA table).

**Finding.** The note states that 5/30 at Tier 3 is "valid for the KEDA path **when** the aggressive scale-up policy (`100%/15s or 8 pods/15s`) is in place" and suggests `minReplicas: 10` "to eliminate reliance on scale-up speed." Two problems:
1. The dependency between `minReplicas: 5` and `scaleUp.policies[].value: 8 pods / 15s` is documented but not mechanized — an operator changing the scale-up policy via Helm (e.g., to 4 pods / 15s to match Tier 1/2, or disabling the multi-policy `select: Max` to reduce scale-up thrash) silently invalidates the burst-absorption analysis. The math (`5 × 400 = 2,000` absorbed, remaining 2,000 covered by doubling in 15s) assumes `policies: [{value: 100%, period: 15s}, {value: 8, period: 15s}]` with `selectPolicy: Max`.
2. The gateway does not validate at startup that Tier 3 deployments configured with `minReplicas: 5` also have the aggressive scale-up policy active. A `[WARN] scale-up policy weaker than Tier 3 burst-absorption assumption` startup log (analogous to the existing `[WARN] RedisClusterRecommended` and `[WARN] capacityPlanning` patterns) would surface the coupling at deploy time rather than on the first 200/s burst.

**Why Low.** The invariant is documented; the gap is enforcement. The recommended escape hatch (`minReplicas: 10`) is also documented.

**Recommendation.** Add a gateway startup validator and log-warn emitter that reads `scaling.hpa.scaleUp.policies` and `autoscaling.minReplicas` from Helm values; if `capacityPlanning.tier: 3` and `minReplicas < 10` and the scale-up policy is not at or above the "100%/15s + 8 pods/15s" baseline, emit `[WARN] minReplicas (<n>) assumes Tier 3 aggressive scale-up policy; current policy is weaker. Either set minReplicas: 10 or restore scale-up policy per §17.8.2 Tier 3 note.` Mirror to a Prometheus gauge for ongoing visibility. No spec-level tier changes required.

---

### SCP-006 — Low

**Title:** Tier 3 MinIO burst-throughput headroom is narrow (~17%) and not gated by an operator alert.

**Location:** `spec/17_deployment-topology.md` §17.8.2 "Object storage" table (Tier 3 `Minimum MinIO aggregate throughput (burst, max workspace) ~12 GB/s` vs 8-node NVMe "~10–12 GB/s aggregate" — narrative under the table).

**Finding.** The Tier 3 MinIO Topology-vs-Capacity invariant evaluates steady-state at 2.0 GB/s against `12 GB/s × 0.7 = 8.4 GB/s` — safe. The *burst* (max-workspace) analysis puts the required aggregate at 12 GB/s, and the 8-node NVMe topology is described as "~10–12 GB/s," which means the narrative accepts a ~17% headroom band that can invert into a deficit at the low end of the hardware range. The spec directs operators to add nodes or upgrade drives on `CheckpointDurationHigh` (2.5s P95 alert), but does not publish an *ops-plane* proactive alert for "MinIO aggregate-write headroom < 20%" — i.e., an alert that fires before P95 checkpoint duration breaches 2.5s. Given that MinIO aggregate-write throughput is measurable via `minio_bucket_traffic_received_bytes` and per-drive saturation via `minio_drive_total_io_time_seconds`, a "burst headroom projection" alert is within reach.

Secondary: the §17.8.2 invariant formula `replicas × maxSessionsPerReplica × avg_workspace_bytes / periodicCheckpointIntervalSeconds ≤ minio_aggregate_write_throughput × 0.7` uses `avg_workspace_bytes` for steady-state; there is no matching *burst* invariant published (operators must derive it from the narrative "max-workspace variant" phrase).

**Why Low.** 17% headroom is within the bounds the spec defends as acceptable; the gap is proactive-alerting coverage, not a sizing error.

**Recommendation.** (a) Publish a `MinIOAggregateWriteHeadroomLow` alert at 80% sustained aggregate-write throughput (fires 2–5 min before `CheckpointDurationHigh`); (b) add the burst-variant invariant formula (`... × 0.7` with `avg_workspace_bytes = max_workspace_bytes`) to §17.8.2 alongside the steady-state formula; (c) update `docs/reference/metrics.md` alerts section accordingly.

---

### SCP-007 — Low

**Title:** Tier 3 single-tenant Redis Sentinel detection relies on a startup log warning only, not a Prometheus alert.

**Location:** `spec/17_deployment-topology.md` §17.8.2, "`[WARN] RedisClusterRecommended` gateway startup log" (paragraph at line ~1143); `spec/12_storage-architecture.md` §12.4 "When Sentinel becomes insufficient" signals.

**Finding.** The `[WARN] RedisClusterRecommended` startup log fires when Tier 3 single-tenant Sentinel topology is detected, but (a) it is emitted only at gateway startup — replicas restarting on rolling update will re-emit, but operators who missed the initial warning have no Prometheus signal for ongoing visibility; (b) unlike `QuotaFailOpenUserFractionInoperative`, no paired Prometheus gauge (`lenny_redis_topology_recommendation`) nor alert rule (`RedisClusterRecommendedForTier3`) is published; (c) the §12.4 "When Sentinel becomes insufficient" ceiling signals (CPU >70%, P99 latency >5ms, ops >80% of budget) are documented as observational thresholds but the spec does not attach a named alert to any of them — only `CheckpointDurationHigh` and generic latency/queue-depth alerts fire.

**Why Low.** The topology recommendation is non-blocking; Sentinel "works" at Tier 3 for single-tenant deployments per §12.4. The gap is monitoring-surface coherence (the `feedback_docs_sync_after_spec_changes` rubric).

**Recommendation.** Add a `lenny_redis_topology_mode` gauge + a `RedisClusterRecommendedForTier3` warning alert (fires at Tier 3 when single-tenant Sentinel is detected and any one §12.4 ceiling signal is active). This mirrors the `QuotaFailOpenUserFractionInoperative` pattern the spec already uses. No code restructure needed.

---

### SCP-008 — Low

**Title:** ResourceQuota Tier 3 `pods: 15,000` default is below the SDK-warm + delegation upper bound (~16,500) documented in the adjacent narrative.

**Location:** `spec/17_deployment-topology.md` §17.8.2 ResourceQuota table (Tier 3 `pods: 15,000`) vs. the immediately-following "Sizing formula" narrative: "`(≈2,100 delegation-adjusted warm pods for pod-warm pools, up to ≈4,000 for SDK-warm pools across all pools) + (10,000 active sessions × 1.2) + (500 concurrent delegation children) ≈ 14,600–16,500`".

**Finding.** The formula admits an upper bound of ~16,500 pods at Tier 3 when SDK-warm pools operate at the 30s `pod_warmup_seconds` baseline, yet the published default is 15,000 — below that upper bound. The narrative qualifies this with "assumes a mixed deployment with the Tier 3 `maxWarm` envelope sized for the delegation-adjusted demand" and directs SDK-warm deployments at the upper end to raise the quota manually. Two coherence issues:
1. An SDK-warm Tier 3 deployment that follows the per-tier default and also exercises the orchestrator preset will hit `ResourceQuota` admission rejections — there is no startup warning comparable to `[WARN] capacityPlanning Helm values are at defaults` that would flag "SDK-warm + Tier 3 default pods: 15,000 is below the SDK-warm upper bound; consider raising to 17,000."
2. The `PoolScaleoutBlockedByQuota` alert will fire at scale-out time, but only *after* pods are rejected. A proactive "quota headroom < 10%" alert (quota observation is cheap via `kube_resourcequota` exporter) would give operators a ~1-week heads-up during first-week monitoring — the window the spec itself names as "monitor `lenny_warmpool_idle_pods` and `lenny_pod_claim_queue_wait_seconds`."

**Why Low.** The spec discloses the gap and names the knob (`.Values.agentNamespaces[].resourceQuota.pods`); this is an operator-experience sharpening, not a sizing defect.

**Recommendation.** (a) Raise the Tier 3 SDK-warm-capable default to ~17,000 pods (covers the upper bound plus 5% headroom) and keep the current 15,000 as a pod-warm-only default selectable via `.Values.capacityPlanning.podWarmMode: true`; alternatively, keep 15,000 and add an automatic bump when `poolScaling.podWarmupSecondsBaseline ≥ 30` is configured; (b) add a `ResourceQuotaHeadroomLow` alert on `kube_resourcequota{resource="pods",type="used"} / kube_resourcequota{resource="pods",type="hard"} > 0.9`. No spec-level tier renumbering.

---

## 3. Convergence assessment

**Verdict:** **Converged** for Perspective 4. **0 Critical / 0 High / 0 Medium / 8 Low.**

**Rationale.**

- No finding escalates to Medium or higher under the iter5-anchored rubric: every issue is either a derivation/narrative tightness gap (SCP-001/003/004/005/008), a monitoring-surface coherence issue (SCP-006/007), or a cost-transparency note (SCP-002).
- iter5 closed all Critical/High/Medium items (convergence verdict was "0 C/H/M"). iter6 deferred this perspective with no spec changes in-between that would regress scalability — the `c941492` iter5-fix commit and the iter5 review's P4 output both confirm no SCL-family regressions.
- The four iter5 Lows (PRF-013 → SCP-001, PRF-014 → SCP-002, PRF-015 → SCP-003, PRF-016 → SCP-004) remain open but do not block convergence: each is purely a documentation/tightness issue, none invalidates a formula, and each is contained to a single spec region.
- The four new Lows (SCP-005/006/007/008) surface recent spec work (KEDA Tier 3 path, MinIO 8-node burst budget, Tier 3 single-tenant Redis startup log, SDK-warm-aware ResourceQuota) — they are consistent with the iter6 cross-cutting theme of "doc-sync drift and broken alert/metric cross-references" (summary.md), adapted to the scalability domain.

**Suggested triage.**

- **Next-iteration eligible (cheap, one-paragraph edits):** SCP-001, SCP-003, SCP-005, SCP-006, SCP-007. Each can be closed by adding a derivation callout, one alert rule, or one startup validator — total effort ~2 hours.
- **Operator-experience batch (combine with general alerting hardening):** SCP-006, SCP-007, SCP-008 — all surface as "add Prometheus alert + cross-reference in `docs/reference/metrics.md`." Best done together to keep `metrics.md` internally consistent.
- **Requires decision:** SCP-004 (Tier 4 narrative vs. Tier 4 column) — defer-to-post-GA is acceptable per the §16.5 disclaimer; only needed if Tier 4 gets a GA commitment.
- **Safe to defer:** SCP-002 (cost-transparency note) — the asymmetry is deliberate and documented.

No finding flagged by the iter5 rubric as convergence-blocking remains open; this perspective meets the "0 Critical/High/Medium" bar used throughout the iter5 review cycle.

---

## 4. Methodology

Sources consulted for iter7:
- Prior findings: `spec-reviews/review-findings/20260419000406/iter5/p04_scalability.md` (baseline Lows), `iter6/summary.md` (cross-cutting themes, P4 deferral), `iter6/p4_scalability.md` (deferral stub).
- Current spec state: `spec/04_system-components.md` §4.1, §4.6.1, §4.6.2; `spec/06_warm-pod-model.md` §6.3; `spec/10_gateway-internals.md` §10.1; `spec/12_storage-architecture.md` §12.4, §12.6; `spec/16_observability.md` §16.5; `spec/17_deployment-topology.md` §17.1, §17.8.2.
- Docs sync surface: `docs/reference/configuration.md` HPA defaults; `docs/reference/metrics.md` HPA roles + alerts; `docs/runbooks/pool-bootstrap-mode.md` bootstrap-mode `minWarm`.
- iter5-fix commit (`c941492`) reviewed for SCL-family touch points — none found (commit scoped to P11–P25 perspectives).

Severity calibration followed `feedback_severity_calibration_iter5.md`: findings were only elevated above Low when a sizing formula was broken, a capacity target was invalidated, or a tier was demonstrably unreachable. None met that bar this iteration.
