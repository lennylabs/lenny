# Scalability & Performance Engineering Review Findings

## Summary

Reviewed the Lenny technical specification (28 files, ~17,741 lines) from a Scalability & Performance Engineering perspective, focusing on bottlenecks, performance targets, capacity assumptions, and scaling lag.

**Finding:** One real cross-section inconsistency identified regarding startup latency scope definitions.

---

### PRF-001: Startup Latency SLO Scope Ambiguity [Medium]

**Files:** `06_warm-pod-model.md` §6.3, `16_observability.md` §16.5

**Issue:**

The specification defines "startup latency SLO target" differently across sections, creating ambiguity about what is measured:

**Section 6.3 (line 318):**
> "Startup latency SLO target: P95 pod-warm session start (pod claim through agent session ready) < 2s for runc, < 5s for gVisor, excluding file upload time."

**Section 6.3 latency budget table (line 330):**
> "Workspace materialization ... File delivery over internal network; **excluded from pod-warm SLO**"

**Section 16.5 (line 482–483):**
> "Startup latency (pod-warm, runc): P95 < 2s — Pod claim through agent session ready, excluding file upload time"

**Contradiction:**

The term "file upload time" in line 318 is ambiguous. If it means "client-side file upload" (user uploading to gateway), then workspace materialization (file *delivery* from gateway to pod) should be included in the SLO. However, line 330 explicitly excludes "file delivery over internal network" from the pod-warm SLO.

The latency budget table (line 334) specifies a "Total (platform-controlled, no setup cmds)" of ≤ 6s (runc) / ≤ 9s (gVisor), which includes the workspace materialization phase (≤ 1s / ≤ 3s). If this total is truly platform-controlled, it contradicts the exclusion stated in line 330.

**Clarification needed:**

1. Does the 2s/5s SLO include or exclude workspace materialization (file delivery from gateway to pod)?
2. If excluded: the SLO should measure only pod claim + credential assignment + agent session start (~1.8s/~4.8s), and the latency budget table's "Total" should be clearly marked as covering a broader set of operations than the SLO.
3. If included: line 330's statement that workspace materialization is "excluded from pod-warm SLO" must be corrected, and the 2s/5s SLO targets should be re-evaluated against workspace size assumptions.

**Recommendation:**

Update Section 6.3 to clarify:
- Explicitly define what "excluding file upload time" means (client-side upload only, or also gateway-to-pod delivery).
- If workspace materialization is truly excluded from the SLO: add a note in the latency budget table explaining that the SLO (2s/5s) is stricter than the total indicative budget (6s/9s) and what operations fall outside the SLO.
- If workspace materialization is included: remove line 330's exclusion statement and adjust the SLO targets to account for the ≤ 3s worst-case workspace materialization latency.

---

## Non-Findings

The following do NOT constitute real errors and are correctly handled:

1. **Missing performance benchmarks:** Section 6.3 correctly marks all latency targets as "indicative planning targets" and "MUST NOT be used as an SLO in any capacity agreement" until Phase 2/14.5 validation. This is intentional design discipline, not a spec error.

2. **HPA scaling lag:** Section 10.1 and 17.8.2 properly document the HPA pipeline lag (60s Prometheus Adapter vs. ~20s KEDA) and provide sizing guidance (minReplicas burst-absorption formula) to absorb arrival during the lag. The guidance is conservative and acknowledged.

3. **Redis-backed session coordination:** Properly specified in 10_gateway-internals.md with clear failure modes and coordinator handoff protocol. No bottleneck left unexplained.

4. **Gateway as throughput bottleneck:** Correctly identified in Section 4.1. The spec properly specifies per-replica capacity budgets (provisional), extraction thresholds for subsystems, and metrics for monitoring (request_queue_depth as primary HPA trigger, not session count).

5. **Cross-section reference consistency:** All major cross-references (§4.1, §6.3, §10.1, §16.5, §17.8.2) are correctly numbered and exist in the spec.

