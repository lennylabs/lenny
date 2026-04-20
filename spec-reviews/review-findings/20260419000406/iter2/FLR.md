### FLR-002 Gateway Deployment Lacks Concrete PDB and RollingUpdate Strategy [High]
**Files:** `17_deployment-topology.md` (line 7, line 89), `10_gateway-internals.md` (line 130)

The gateway — the single external-facing component and coordinator of all session state — specifies neither a concrete `PodDisruptionBudget` value nor a rolling-update strategy. Other components are explicit: Token Service `minAvailable: 1` (17:8), PgBouncer `minAvailable: 1` (12:44), lenny-ops `minAvailable: 1` (17:15), admission webhook `minAvailable: 1` (17:42). lenny-ops even pins `maxUnavailable: 0, maxSurge: 1` (25:3272). For the gateway, 17:7 says only "HPA, PDB, multi-zone, topology spread" with no value. No `maxUnavailable`/`maxSurge` is stipulated, so the Kubernetes default (`25%/25%`) applies.

Consequence: at Tier 3 with ~20 replicas and default `maxUnavailable: 25%`, a rolling upgrade drains up to 5 gateway replicas concurrently. Stage 2 of the preStop hook (10:100–108) fans out CheckpointBarrier to up to 400 pods per replica (10:130), so five concurrent drains can trigger up to **2,000 simultaneous MinIO checkpoint uploads** — far beyond the "400 simultaneous uploads" budget documented in 10:130 and the MinIO throughput budget in 17.8.2. Without a PDB floor, a node drain or evict-all automation can also take every gateway replica offline at once.

**Recommendation:** Add concrete PDB at 17:7 (e.g., `minAvailable: 2` Tier 1/2; `minAvailable: ceil(replicas/2)` Tier 3). Specify `RollingUpdate` with `maxUnavailable: 1, maxSurge: 25%` so the CheckpointBarrier fan-out stays within the MinIO budget. Cross-reference 10:130.

---

### FLR-003 Inbox-Drain Failure Counter Undefined and Unalerted [Medium]
**Files:** `07_session-lifecycle.md` (line 265), `16_observability.md` (metrics table, alert table)

Section 7.2 specifies that when the session inbox-to-DLQ drain fails during a `resume_pending` transition (e.g., Redis unavailable), the transition is committed anyway and inbox messages are **permanently lost**. The spec says `lenny_inbox_drain_failure_total` is incremented and a `WARN` log is emitted. However:

1. `lenny_inbox_drain_failure_total` is **not defined** in the 16.1 metrics table. The only reference spec-wide is the inline mention in 07:265. By the catalog convention, monitoring stacks scraping against 16_observability.md will not know this counter exists.
2. No corresponding alert exists in 16.5. Compare: `SessionEvictionTotalLoss` fires on any non-zero increment of its counter; `CheckpointStorageUnavailable` fires on MinIO retry exhaustion. Inbox-drain loss is the only acknowledged silent-data-loss path without an alert.

Failure mode: a Redis outage that overlaps a pod failure (the same class of infrastructure events triggers both `resume_pending` and Redis degradation) will silently discard every in-flight inbox. Operators see only a WARN log, which may not reach aggregation if the replica is itself crashing.

**Recommendation:** Add `lenny_inbox_drain_failure_total` (counter, labels `pool`, `session_state`) to 16.1 under Session Lifecycle. Add `InboxDrainFailure` warning alert in 16.5 firing on any non-zero increment over 5 minutes.

---

### FLR-004 preStop Stage 2 Undefined When Postgres Is Unreachable [Medium]
**File:** `10_gateway-internals.md` (line 108)

Stage 2 of the preStop hook reads `last_checkpoint_workspace_bytes` from Postgres to select the tiered cap (30s / 60s / 90s). 10:108 handles only the "absent → 30s default" branch. It does **not** specify what happens when the Postgres read itself fails — a plausible state during Postgres failover (up to 30s per 12:150) or PgBouncer outage (12:46).

Two implicit interpretations diverge on safety:
- **A. Treat read failure as "absent":** Cap defaults to 30s. A 500MB workspace gets SIGKILLed at 30s, falling through to partial-manifest recovery (10:120) or to the total-loss path (4:283) if Postgres stays down.
- **B. Block on read:** preStop consumes the entire `terminationGracePeriodSeconds` budget, leaving zero time for stream drain (stage 3).

Neither is safe by default, and without spec guidance implementations will diverge.

**Recommendation:** Specify behavior explicitly in 10:108. Preferred: the gateway caches `last_checkpoint_workspace_bytes` in-replica on every successful checkpoint and uses the cached value if Postgres is unreachable during preStop. On cache miss (recent coordinator handoff), fall back to the 90s maximum tier rather than the 30s default — this trades 60s of extra preStop wait for avoiding truncated checkpoints during correlated infrastructure outages.

---

### FLR-005 PgBouncer Readiness Probe Amplifies Postgres Failover Window [Low]
**File:** `12_storage-architecture.md` (line 45)

The PgBouncer readiness probe (`periodSeconds: 5`, `failureThreshold: 2`, `timeoutSeconds: 3`) marks a pod NotReady after ~10s of failing `SELECT 1` probes. All PgBouncer replicas probe the same Postgres primary, so during a normal Postgres failover (< 30s RTO per 12:150), every replica fails the probe within ~13s and the Service has zero ready endpoints for the full failover plus a ~5–10s re-readiness interval. 12:46 treats this as "identical to a Postgres outage", but a 30s Postgres failover thus produces a ~40–45s gateway-visible Postgres outage — pushing closer to `dualStoreUnavailableMaxSeconds` (60s) in any dual-store scenario, at which point in-flight sessions begin terminating (10:44).

**Recommendation:** Widen the readiness probe failure window to exceed Postgres RTO (e.g., `failureThreshold: 8` ≈ 40s) so PgBouncer remains Ready while its own retry logic absorbs the failover, or decouple readiness from backend reachability. Low severity: current behavior fails closed, only amplifying the outage window.
