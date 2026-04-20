# Iter3 PRF Review

## Summary

Regression-checked the PRF-002 (Tier 3 session rate in §12.4) and PRF-003 (warm pool minWarm table + safety_factor reconciliation) iter2 fixes, and re-walked tier sizing math. PRF-002 is clean. PRF-003's table update was applied but **two in-file prose references to the pre-fix "1,050" baseline were missed**, creating a fresh contradiction with the same table the fix revised. One new issue: the iter2 gateway PDB addition is justified against rolling-update behavior, but its `minAvailable: ceil(replicas/2)` at Tier 3 permits a voluntary-disruption pattern (node drain / cluster upgrade) whose MinIO checkpoint-burst exceeds the §17.8.2 "burst, max workspace" budget.

---

### PRF-004: `minWarm: 1,050` Baseline Re-Surfaces in Two Prose Locations After PRF-003 Table Fix [Medium]

**Files:** `17_deployment-topology.md` §17.8.2 (line 970), `17_deployment-topology.md` §17.8.3 Step 3 (line 1182)

The PRF-003 fix (commit 2a46fb6) updated the §17.8.2 warm pool sizing table to show both a "Raw demand estimate (no safety margin) = 1,050" row and a "Production `minWarm` (with tier safety factor) = 1,260" row, with the note text explicitly requiring the 1,260 value for production. However, two adjacent prose sections still reference the pre-fix 1,050 value as the baseline:

**Line 970 (§17.8.2 delegation fan-out prose):**
> "The recommended baseline of `minWarm: 1050` covers session-creation demand only. Deployments where a significant fraction of sessions use the `orchestrator` preset or other high-fan-out leases should increase `minWarm` using the formula above [...] If `orchestrator`-preset sessions are rare (< 10% of sessions), the baseline 1,050 remains adequate."

This contradicts the newly revised table: the very sentence that was a PRF-003 target output says the "recommended baseline" is 1,050, which is now labeled "Raw demand estimate (no safety margin)" and expressly prohibited for production in the note immediately above.

**Line 1182 (§17.8.3 "Apply Tier 3 Helm Values" promotion step):**
> "3. **Warm pools:** Update `minWarm` to Tier 3 baseline (1,050 per hot pool) or delegation-adjusted value from [§17.8.2]..."

This is a normative operator checklist for Tier 2→3 promotion that directs the operator to set the 1,050 raw-demand value at the exact moment they are most exposed to traffic growth — the opposite of what the PRF-003 fix intended.

**Impact:** An operator running the §17.8.3 Tier 3 promotion procedure reads line 1182 first, sets `minWarm: 1050`, and has zero safety headroom during the 35-second controller-failover window (exactly the failure mode the PRF-003 fix was written to prevent). A reader studying line 970's delegation-sizing prose is given the same wrong baseline. Both instances are in the same file, sections apart, and contradict the table that sits between them.

**Recommendation:**

1. Edit line 970: change both references from "baseline 1,050" / "baseline of `minWarm: 1050`" to "production baseline of `minWarm: 1,260`" (or "Tier 3 production `minWarm` of 1,260" for clarity). Keep the "+ delegation burst term = ~3,400" arithmetic valid — the formula already uses the safety-factor-adjusted `30 × 1.2 × 35 = 1,596` as its first term, so the underlying calculation is unchanged; only the labeled baseline needs updating.

2. Edit line 1182: change "Tier 3 baseline (1,050 per hot pool)" to "Tier 3 production baseline (1,260 per hot pool, per [§17.8.2](#1782-capacity-tier-reference) 'Production `minWarm`' row)" so the promotion checklist points operators at the safety-factor-adjusted row, not the raw-demand row.

---

### PRF-005: Gateway PDB Allows Voluntary-Disruption Burst That Exceeds MinIO Budget at Tier 3 [Medium]

**Files:** `17_deployment-topology.md` §17.1 Gateway row (line 7), `17_deployment-topology.md` §17.8.2 MinIO throughput table (lines 1074–1076), `10_gateway-internals.md` §10.1 CheckpointBarrier (line 132)

The iter2 fix (commit 2a46fb6) added a PodDisruptionBudget and a `maxUnavailable: 1` rolling-update strategy to the Gateway row in §17.1. The rationale text bounds the checkpoint fan-out at Tier 3 to "one replica's 400-pod quota at a time" — which is accurate **for rolling updates** (rolling-update `maxUnavailable` governs that path), but **not for other voluntary disruptions** (node drain, cluster upgrade, manual `kubectl drain`, autoscaler node replacement). Voluntary disruptions are governed by the PDB, not the rolling-update strategy, and the PDB added in the fix is:

> `minAvailable: 2` at Tier 1/2, `minAvailable: ceil(replicas/2)` at Tier 3.

At Tier 3 with `autoscaling.maxReplicas: 30` (§17.8.2 line 860), `minAvailable: ceil(30/2) = 15` means the PDB **permits up to 15 gateway replicas to be simultaneously unavailable** during a voluntary disruption. Each replica's preStop CheckpointBarrier fans out to up to 400 pods (§10.1 line 132), so 15 × 400 = **6,000 simultaneous MinIO checkpoint uploads** — three times the 2,000-upload cap the rationale claims to enforce.

**Burst math against the MinIO budget (§17.8.2 line 1076):** the budget states 20 GB/s "burst, max workspace" at Tier 3. With 6,000 concurrent uploads of 512 MB over the 90s tier cap (§10.1 line 106), the peak rate is `6,000 × 512 MB / 90s ≈ 34 GB/s` — exceeds the 20 GB/s burst budget by 70%. Even at average 100 MB workspaces over 30s, `6,000 × 100 MB / 30s = 20 GB/s` sits exactly at the budget ceiling with zero margin. The 20 GB/s burst row was sized (§17.8.2 line 1078) against the rolling-update case, not the node-drain case.

In practice, topology spread, zone affinity, and cordon-then-drain ordering usually prevent 15 replicas from evicting simultaneously — but the PDB as written authorizes it. A cluster-wide control-plane upgrade (e.g., EKS version bump draining one managed node group at a time where several gateway pods are co-located) can realistically drive concurrent eviction well above 1 replica, and the stated rationale is the only thing preventing the Helm chart from being tuned up to the PDB ceiling.

**Impact:** A cluster admin running a node-pool upgrade at Tier 3 without reading §10.1 and §17.8.2 side-by-side can saturate MinIO mid-upgrade, producing partial-manifest checkpoint timeouts (§10.1 line 122 `lenny_checkpoint_partial_total`), degraded-workspace session resumes, and potential `CheckpointDurationHigh` alert storms — all in a window where gateway replicas are already in their most fragile state.

**Recommendation:**

Tighten the §17.1 Gateway PDB so the worst-case voluntary-disruption fan-out matches the MinIO budget, not only the rolling-update path. Two options:

(a) **Replace `minAvailable: ceil(replicas/2)` with `maxUnavailable: 1`** (PDB supports `maxUnavailable` directly). This aligns voluntary disruptions with rolling updates — at most one gateway replica may be evicted at a time, bounding the MinIO burst to the 400-pod quota the rationale already argues for. Downside: a node failure during an upgrade could temporarily block further voluntary disruptions until the replacement replica becomes Ready; the MinIO budget justifies this trade.

(b) **Raise the MinIO "burst, max workspace" budget row** (§17.8.2 line 1076) to cover `ceil(maxReplicas/2) × 400 × 512 MB / 90s ≈ 34 GB/s` at Tier 3, and document the node-drain scenario explicitly in the line 1078 budget-narrative paragraph. Downside: requires at least 14-node MinIO erasure-coded cluster at Tier 3, not the 8-node recommendation in line 1069.

Option (a) is strongly preferred — it converges the PDB rationale with the actual bound. Additionally, cross-reference the §10.1 CheckpointBarrier line 132 paragraph to the §17.1 PDB choice so operators who tune the PDB (e.g., raising `minAvailable` for availability reasons) understand they are opening the MinIO-burst door.

---

## Non-Findings

1. **PRF-002 (Tier 3 Redis §12.4) regression check — clean.** §12.4 line 247 now reads "~10,000 concurrent sessions, 200 new sessions/s sustained" with an explicit cross-reference to §16.5 line 464. The row annotations correctly re-attribute the `INCR` rates to gateway→pod RPC rate (quota) and Gateway RPS (rate-limit) rather than session-creation rate, and the concluding paragraph (line 258) explicitly notes that the sustained total is unchanged. No regression.
2. **PRF-003 (warm pool table) regression check — table itself clean; prose is not — see PRF-004.** The §17.8.2 table (lines 924–931) now has both "Raw demand estimate" and "Production `minWarm`" rows with the correct values (27 / 263 / 1,260), and §4.6.2 line 512 correctly defers to §17.8.2 as the normative per-tier `safety_factor` source. The delegation-fan-out example at line 959–968 uses `safety_factor = 1.2` consistently. The remaining prose drift (lines 970 and 1182) is covered by PRF-004.
3. **Tier 3 delegation-adjusted `minWarm` math (line 965).** `30 + 500/60 ≈ 38/s` × 1.2 × 35 = 1,596; `(0 + 50) × 35 = 1,750`; total 3,346 (rounded ~3,400). Consistent with the table's agent-type `safety_factor = 1.2` and the per-pool division at line 972 (`ceil(3,346 / 10) = 335`).
4. **HPA burst-absorption formula (§17.8.2 lines 895–918).** Tier 3 KEDA path math (200/s × 20s / 400 = 10 raw, relaxed to 5 by scale-up policy) and Prometheus-Adapter path math (200/s × 60s / 400 = 30 = maxReplicas → KEDA-mandatory) are internally consistent. The §16.5 "sustained 200/s" label is used here as a burst rate; that naming inconsistency has existed since iter1 and was not flagged then — out of scope for iter3.
5. **Gateway scale-down policy (§17.8.2 line 865 "3 pods/60s at Tier 3").** 3 × 400 = 1,200 sessions evicted per minute via scale-down, bounded by the same preStop CheckpointBarrier path; within MinIO budget even at max workspace.
6. **TTFT budget (§6.3) and phase allocation.** PRF-001 iter2 fix remains intact — the 2s/5s pod-warm SLO vs. 6s/9s indicative total is explicitly reconciled in §6.3 line 339. No regression.
7. **Controller tuning rate limits (§17.8.2 line 1011 — 80 QPS / 200 burst at Tier 3).** Supports the 200/s Tier 3 burst session arrival rate one-to-one with the pod creation rate limiter, with 10% headroom. Adequate.
