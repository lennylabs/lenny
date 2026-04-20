# Scalability & Performance Engineering Review Findings (Iteration 2)

## Summary

PRF-001 (startup latency SLO scope) was fixed correctly — §6.3, §16.1 metric description, §16.5 SLO rows, and §6.3 budget table all now carry the same "excluding client file upload and workspace materialization" phrasing. Two new findings surfaced in iter2.

---

### PRF-002: Tier 3 "new sessions/s" Figure in §12.4 Uses Tier 4 Number [Medium]

**Files:** `12_storage-architecture.md` §12.4 (line 247), `16_observability.md` §16.5 (line 446)

**Issue:**

The authoritative capacity-tier table (§16.5 line 446) gives sustained session creation rates of 5/s, 30/s, **200/s**, **2,000/s** for Tier 1/2/3/4. §10.1 and §17.8.2 correctly use `burst_arrival_rate = 200/s` at Tier 3; §4.6.2 / §17.8.2 correctly use 30/s sustained.

§12.4 line 247 opens the "Tier 3 Redis write throughput quantification" block with "At Tier 3 scale (~10,000 concurrent sessions, **2,000 new sessions/s**)". The 2,000/s figure is the Tier 4 session creation rate, not Tier 3.

The table that follows (lines 249–256) is sized against this wrong rate:
- Quota counter `INCR` ~2,000/s and Rate-limit counter `INCR` ~2,000/s both appear 10x too high if session creation is the driver.
- "Burst at session storm may reach ~20,000/s" (line 256) is similarly inflated.

Note: Rate-limit INCRs actually scale with Gateway RPS (50,000/s at Tier 3, §16.5 line 447), not with session creation — so that row may be under-estimated for a different reason. Quota counter INCR scales with gateway→pod RPC rate (active sessions × turn rate).

**Impact:**

- The "Tier 3 Redis Cluster topology is required; Sentinel is appropriate only through Tier 2" conclusion (line 258) rests on the ~6,500/s sustained total built from the 10x-inflated rows. If corrected, the Sentinel→Cluster threshold may move, affecting when Tier 2→3 operators must plan cluster migration (lines 260–266).
- The §16.5 line 466 reconciliation paragraph ties single-tenant ~365 ops/s back to the §17.8 single-tenant estimate, but the §12.4 multi-tenant ~6,500/s aggregate does not reconcile cleanly if the underlying session-creation rate is wrong.

**Recommendation:**

Replace "2,000 new sessions/s" with "200 new sessions/s sustained" (cross-reference §16.5). Recalculate the Quota and Rate-limit rows using the correct drivers (gateway→pod RPC rate and Gateway RPS respectively, not session creation rate). Revise the 20,000/s burst and the ~6,000–6,500/s sustained total. Re-examine whether the Sentinel-appropriate-through-Tier-2 conclusion still holds.

---

### PRF-003: Warm Pool `minWarm` Table Contradicts Its "Production Value" Note [Medium]

**Files:** `17_deployment-topology.md` §17.8.2 (lines 866–876), `04_system-components.md` §4.6.2 (line 512)

**Issue:**

The §17.8.2 "Warm pool sizing" table publishes `Recommended minWarm (per hot pool)` = **20 / 175 / 1050** for Tier 1/2/3. The column header is explicitly "Recommended".

The note immediately below (line 874) mandates the opposite: "The recommended `minWarm` values above use `safety_factor = 1.0` (no safety margin)... **For production deployments**, operators MUST apply the per-tier `safety_factor`: Tier 1/2 with 1.5 yields 27 / 263; Tier 3 with 1.2 yields **1,260**. Use the safety-factor-adjusted values as the production `minWarm`."

So the "Recommended" column is a non-production value explicitly prohibited for production deployment. A Tier 3 operator reading the table sets 1,050 and has zero headroom during the 35-second failover window.

Compounding this, §4.6.2 line 512 says `safety_factor` defaults are **1.5 / 2.0** (agent-type / mcp-type). §17.8.2 line 871–872 shows Tier 3 values of **1.2 / 1.5**. Applying §4.6.2's default to Tier 3 produces `ceil(30 * 1.5 * 35) = 1,575` — a third number for the same parameter. The delegation-fan-out worked example (§17.8.2 line 898) uses `safety_factor = 1.2`, matching §17.8.2's table but not §4.6.2's stated default.

**Impact:**

Three different sizing outputs (1,050 / 1,260 / 1,575) for Tier 3 `minWarm` across adjacent sections. An operator or AI-DevOps agent reading §17.8.2's table will apply 1,050 and miss the mandated 1,260 unless they read the prose note.

**Recommendation:**

(1) Rename the table column from "Recommended minWarm" to "Raw demand estimate (no safety margin)" and add a second row "Production `minWarm` (with tier safety factor)" showing 27 / 263 / 1260. (2) Reconcile §4.6.2 line 512 with §17.8.2 lines 871–872: either §4.6.2 cites per-tier overrides, or §17.8.2 matches §4.6.2's defaults. The delegation-fan-out example (line 898) must then be kept consistent.

---

## Non-Findings

1. **Redis session coordination** — §10.1 coordinator fencing, hold state, dual-store degraded mode, orphan-session reconciler with mirror-table staleness fallback are internally consistent.
2. **HPA scaling lag** — §10.1 and §17.8.2 consistently specify 60s Prometheus Adapter vs. 20s KEDA, Tier 3 mandatory KEDA, and the `minReplicas` burst-absorption formula per path.
3. **Gateway throughput bottleneck** — §4.1 subsystem extraction thresholds, `maxSessionsPerReplica` calibration methodology, and LLM Proxy extraction prerequisite for Tier 3 are internally consistent.
4. **Checkpoint drain burst** — §10.1 line 130's 400-session parallel-checkpoint upload burst cross-references the §17.8.2 MinIO "burst, max workspace" row (line 1015: 20 GB/s at Tier 3).
5. **Startup latency SLO scope** (PRF-001 fix) — verified clean.
