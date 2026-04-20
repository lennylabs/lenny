# Iter3 FLR Review

### FLR-006 Gateway PDB `minAvailable: 2` at Tier 1 blocks node drains and rolling updates [High]
**Files:** `17_deployment-topology.md:7`, `17_deployment-topology.md:860`

The iter2 fix for FLR-002 set gateway PDB to `minAvailable: 2` at Tier 1/2. But Tier 1 `autoscaling.minReplicas: 2` (17:860). A PDB of `minAvailable: 2` on a 2-replica deployment prevents **all** voluntary disruption:

1. **Node drain**: with 2 replicas across 2 zones (17:97 topology spread), draining any node would bring replicas to 1, violating the PDB. `kubectl drain` blocks indefinitely.
2. **Rolling update without scale-up headroom**: `maxSurge: 25%` of 2 = 1, so surging first creates a third pod that must become Ready before the old pod can terminate. This works in the happy path, but if the surge pod crashes or its image pull stalls, the old replica cannot drain — the PDB pins it in place. Compare Tier 2: `minReplicas: 3` with `minAvailable: 2` permits drain of 1 replica, which is the intended shape.
3. **Correlated zone failure**: not bound by PDB (involuntary), but combined with a surviving-replica maintenance event during the outage, the PDB escalates the mitigation window.

No other component uses `minAvailable: 2`: Token Service (17:8), PgBouncer (12:44), `lenny-ops` (17:15), and the admission webhook (17:42) all use `minAvailable: 1`. The gateway's PDB is an outlier that introduces a new availability foot-gun at Tier 1 to fix a Tier-3 fan-out concern.

**Recommendation:** At Tier 1 (`minReplicas: 2`), reduce PDB to `minAvailable: 1` and rely on `maxUnavailable: 1` + `maxSurge: 25%` to bound the checkpoint fan-out (which is already limited by 400-pod-per-replica at Tier 1 being under the MinIO budget — a Tier 3-scale problem, not Tier 1). At Tier 2 (`minReplicas: 3`), `minAvailable: 2` is workable. Keep `ceil(replicas/2)` for Tier 3. Alternatively: raise Tier 1 `minReplicas` to 3 so `minAvailable: 2` leaves 1-pod disruption headroom. The second option pairs PDB with the replica range rather than splitting the math.

---

### FLR-007 Gateway PDB formula `ceil(replicas/2)` is not expressible as PDB YAML [Medium]
**Files:** `17_deployment-topology.md:7`

The iter2 fix states `minAvailable: ceil(replicas/2)` for Tier 3. Kubernetes `PodDisruptionBudget` does not accept an expression referencing the Deployment's current replica count — `minAvailable` must be a fixed integer or a percentage string (e.g., `"50%"`). Kubernetes integer-percentage resolution on `minAvailable` rounds **up** (e.g., `50%` of 5 = 3, of 30 = 15), which matches `ceil(replicas/2)` exactly. But the spec's literal YAML-looking text `minAvailable: ceil(replicas/2)` will not apply as-is in a Helm chart render.

Two related gaps:

1. The text does not tell chart authors to render the value as `minAvailable: "50%"`.
2. With HPA-scaling, `replicas` varies from 5 to 30. If the chart renders a fixed integer computed from `autoscaling.minReplicas` (5 → 3), then the PDB stays at 3 even when replicas grow to 30 — allowing up to 27 concurrent disruptions. If it renders from `maxReplicas` (30 → 15), then a scaled-down deployment at 5 replicas cannot disrupt any replica (`minAvailable: 15` > 5 is infeasible). Only the percentage form self-adjusts.

**Recommendation:** Replace `ceil(replicas/2)` in 17:7 with `"50%"` and add an explicit note: "Kubernetes resolves the percentage against `Deployment.status.replicas`, rounding up. At Tier 3 this yields `ceil(replicas/2)` at any HPA-chosen replica count." Cross-reference the `maxUnavailable: 1` bound that keeps the actual rolling-update fan-out capped regardless of PDB headroom.

---

### FLR-008 preStop cache population gap on coordinator handoff [Medium]
**Files:** `10_gateway-internals.md:110`, `10_gateway-internals.md:30–37`

The iter2 FLR-004 fix introduces an in-replica cache of `last_checkpoint_workspace_bytes`, updated "on every successful checkpoint (immediately after the Postgres write)." It does not address how a replica populates the cache for sessions it acquires via **coordinator handoff** (10:30–37). When replica B takes over from crashed replica A, replica B's local cache is empty for that session. If B then enters preStop while Postgres is unreachable before any checkpoint has completed on B, the spec's cache-miss branch forces the 90s maximum tier for every newly-inherited session — even sessions whose workspaces are 30 MB and whose historical cache value (on A) was 30s.

This produces a correctness gap (conservative overshoot) and an operability gap (no way to measure it):

1. A rolling update restarts replicas one at a time. After a gateway rollout, every replica has been re-created and every inherited session falls into cache miss until its first successful checkpoint lands on the new replica. If a Postgres outage overlaps the post-rollout window, nearly every preStop will be capped at 90s, compounding drain durations at Tier 3.
2. No metric is emitted when the cache path is taken, when the cache misses, or when the 90s fallback fires. Operators cannot distinguish "Postgres healthy — cache never consulted" from "Postgres outage — 90% of sessions hitting 90s cap." The `coordinator_resume_meta_source` counter (10:134) is the closest analogue but covers the post-resume path, not preStop tier selection.

**Recommendation:** Specify in 10:110 that on coordinator handoff (10:30 step 0 "Pre-CAS session read"), the acquiring replica also reads `last_checkpoint_workspace_bytes` and populates its local cache. Add a metric `lenny_prestop_tier_source_total` (counter, labeled by `pool`, `source`: `postgres` | `cache` | `cache_miss_fallback`) so operators can quantify each path. Add a low-priority alert `PreStopCacheMissHigh` firing when the `cache_miss_fallback` rate exceeds 10% of preStop invocations in a 15-minute window, which would indicate either a correlated Postgres outage or a recent rollout without cache warming.

---

### FLR-009 `InboxDrainFailure` alert rule text is not an evaluable PromQL expression [Low]
**Files:** `16_observability.md:412`

The iter2 FLR-003 fix adds the alert with description `"lenny_inbox_drain_failure_total incremented (any non-zero increase over a 5-minute window)"`. Peer alerts in 16.5 use concrete PromQL — e.g., `LLMUpstreamEgressAnomaly` uses `incremented` as prose ([420]), but `CheckpointStorageUnavailable` and others use explicit rate expressions. The ambiguity of "any non-zero increase" permits two divergent implementations:

- `increase(lenny_inbox_drain_failure_total[5m]) > 0` — fires on any single increment across the whole fleet within 5 minutes.
- `rate(lenny_inbox_drain_failure_total[5m]) > 0` — fires as long as the rate is positive (can oscillate in and out of firing at low increment rates).

Both are defensible; the spec does not choose.

**Recommendation:** Replace the prose with an explicit expression in 16.5, e.g., `increase(lenny_inbox_drain_failure_total[5m]) > 0` with a 5-minute `for:` clause to debounce. Also clarify whether the alert is emitted per-label (per `pool`, `session_state`) or aggregated. Per-label firing is more useful operationally; aggregated firing hides the pool scope of the loss.

---

### FLR-010 PgBouncer readiness probe — FLR-005 still unresolved [Low]
**File:** `12_storage-architecture.md:45`

Iter2 closed FLR-002/003/004 but did not address FLR-005 (PgBouncer probe amplifies Postgres failover window). The probe settings remain `periodSeconds: 5`, `failureThreshold: 2`, `timeoutSeconds: 3` — a 30s Postgres failover still translates into a 40–45s gateway-visible Postgres outage through the probe's response lag, which pushes `dualStoreUnavailableMaxSeconds` (60s) closer to breach during any overlapping Redis degradation.

**Recommendation:** Same as FLR-005. Widen `failureThreshold` to 8 (≈ 40s before NotReady) so PgBouncer's own retry logic absorbs Postgres failover, or decouple readiness from backend reachability and rely on the existing `PgBouncerBackendUnreachable` alert path. If this is a deliberate accept-as-is, add a "Known limitation" note under 12.4 stating the amplification window so operators can factor it into their Tier-3 RTO budgets.

---

### FLR-011 No regression on billing MAXLEN (FLR-001) — resolved [PARTIAL]

FLR-001 (iter1) flagged the billing stream MAXLEN formula mismatch. Iter1 addressed it with footnote ⁵ at 17:1093 that now documents the 60s envelope (RTO + XAUTOCLAIM reclaim + catch-up overlap) with a 2× safety factor, reaching 72,000. The footnote also names the conservative floor (36,000 using raw 30s RTO). This is a clean close — the reviewer's original alternative reading (undocumented RTO increase) is now documented. No new finding.

---

## Regressions check summary

- **FLR-002** (gateway PDB): **partial regression** — new PDB at Tier 1 introduces FLR-006 (drain block) and FLR-007 (percentage expression gap).
- **FLR-003** (inbox drain metric/alert): resolved but FLR-009 remains for alert-expression ambiguity.
- **FLR-004** (preStop Postgres-fail): resolved at the hot path; FLR-008 remains for coordinator-handoff cache population and observability gap.
- **FLR-005** (PgBouncer probe): unresolved — re-filed as FLR-010.
- **FLR-001** (billing MAXLEN): resolved with explanatory footnote.

## Missed failure-mode issues in iter3

See FLR-006, FLR-007, FLR-008, FLR-009, FLR-010 above.

## PARTIAL / SKIPPED

- None skipped. Coverage was full; no sub-agents spawned.
