## 4. Scalability & Performance Engineering

### PRF-013. Stream Proxy `maxConcurrent` per replica still lacks streams-per-session derivation [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.2 (line 897), `spec/10_gateway-internals.md` §10.1, `spec/04_system-components.md` §4.1 Gateway subsystem table

**Carry-forward from iter4 PRF-011.** The Tier 3 capacity tier reference still lists `Stream Proxy maxConcurrent: 20,000` (line 897) and `Upload Handler maxConcurrent: 2,000` / `MCP Fabric maxConcurrent: 5,000` / `LLM Proxy maxConcurrent: 10,000` without derivation from the Tier 3 per-replica session budget (`maxSessionsPerReplica: 400`). 20,000 streams × 30 replicas = 600,000 aggregate concurrent streams for a 10,000-session target — a 60:1 stream-to-session ratio that may be correct but is not documented.

**Recommendation:** Add a subsection to §10.1 (or a footnote to §17.8.2) deriving each `maxConcurrent` default: `maxConcurrent = maxSessionsPerReplica × streams_per_session(component) × safety_factor`. Add `lenny_gateway_streams_per_session{p99}` observation guidance.

---

### PRF-014. HPA scale-up `stabilizationWindowSeconds: 0` paired with PDB-bound 25-min scale-down — cost/efficiency finding unchanged [Low]

**Section:** `spec/17_deployment-topology.md` §17.8.2 (lines 894, 903, 906), `spec/10_gateway-internals.md` §10.1 (line 93)

**Carry-forward from iter4 PRF-012.** The HPA combines `stabilizationWindowSeconds: 0` scale-up with 25–50 min PDB-bounded scale-down. For bursty workloads whose burst duration is shorter than the ~25-min window, the gateway never completes scale-down between bursts, so effective cost is "max-replica continuously," not the advertised auto-scaling curve.

**Recommendation:** Option A — add `stabilizationWindowSeconds: 60` to `scaleUp.behavior`. Option B — publish a brief cost-recovery worked example in §17.8.2.

---

### PRF-015. Tier 3 gateway session headroom = 20% leaves narrow room for replica failure during burst [Low]

**Section:** `spec/16_observability.md` §16.5 capacity tiers (lines 539, 545), `spec/17_deployment-topology.md` §17.8.2 (lines 891, 897, 946)

**New — surfaced by iter4 PRF-002/PRF-003 tier reconciliation.** Tier 3 sized as `maxReplicas: 30 × 400 = 12,000` against a 10,000-session target is 20% headroom. That must simultaneously cover (a) the `minReplicas` burst-absorption buffer, (b) PDB `maxUnavailable: 1` replica loss (400 sessions = 4% of target), and (c) the `GatewaySessionBudgetNearExhaustion` threshold at 90%. Under worst-case superposition, available capacity can drop below 10,000.

**Recommendation:** Add a Tier 3 superposition worked example to §17.8.2 and either widen `maxReplicas` to 35 or document the trade-off with a Helm-tunable note.

---

### PRF-016. Tier 4 (Platform) capacity targets added to §16.5 without per-replica scaling derivation [Low]

**Section:** `spec/16_observability.md` §16.5 (lines 537–547), `spec/04_system-components.md` §4.1 (line 82 Tier 4 row)

**New — introduced by iter4 Tier 4 addition.** Tier 4 at `maxSessionsPerReplica: 400` implies 250 gateway replicas for 100,000 sessions. Two undocumented second-order issues: (a) 250 replicas × `maxUnavailable: 1` × 60–120s rolling update = 4–8 hour rollout; (b) the 400-session per-replica budget hasn't been re-validated for 5,000-tenant Tier 4 aggregate workload.

**Recommendation:** Add a Tier 4 planning note under §16.5 line 547 flagging (a) linear PDB-bound rolling-update duration at Tier 4 scale, and (b) `maxSessionsPerReplica: 400` is copied from Tier 3 and requires Phase 14.5 re-calibration before Tier 4 production deployment.

---

### Convergence assessment (Perspective 4)

- Critical: 0
- High: 0
- Medium: 0
- Low: 4 (PRF-013, PRF-014 carry-forward; PRF-015, PRF-016 new)
- Info: 0

**Converged for this perspective** (0 C/H/M). No regressions introduced by iter4 fixes.
