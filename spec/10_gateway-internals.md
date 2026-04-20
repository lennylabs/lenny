## 10. Gateway Internals

### 10.1 Horizontal Scaling

Gateway replicas are stateless proxies over externalized state:

```
                     ┌──────────────────┐
                     │  Load Balancer   │
                     └────┬────┬────┬───┘
                          │    │    │
                     ┌────▼┐ ┌─▼──┐ ┌▼────┐
                     │ GW  │ │ GW │ │ GW  │
                     │ #1  │ │ #2 │ │ #3  │
                     └──┬──┘ └─┬──┘ └──┬──┘
                        │      │       │
                     ┌──▼──────▼───────▼──┐
                     │   Postgres + Redis  │
                     └────────────────────┘
```

**Correctness rule:** Sticky routing is an optimization. A client can reconnect to any replica and resume.

**Per-session coordination:**

- **Primary:** Redis-based distributed lease (`SET NX` with TTL) ensures only one replica actively coordinates a given session at a time. If that replica dies, another picks up after TTL expiry.
- **Fallback:** If Redis is unavailable, replicas acquire coordination rights using `SELECT ... FOR UPDATE SKIP LOCKED` on the session row in Postgres. This is transaction-scoped (not connection-scoped like advisory locks), so it survives PgBouncer connection recycling without risk of silent lock release. Shard routing uses `StoreRouter.SessionShard(session_id)` — the shard routing prefix is embedded in every session ID (see [§12.6](12_storage-architecture.md#126-interface-design) session ID format), so no external state is needed to determine the correct shard.
- **Generation counters:** Each session row carries a `coordination_generation` counter. When a replica takes over coordination (via either mechanism), it increments the generation. Pods validate the generation on every gateway→pod RPC — if the generation is stale, the pod rejects the request and the stale replica discovers it is no longer the coordinator. This prevents split-brain even under lease/lock race conditions.

**Coordinator handoff protocol:** When a replica acquires the coordination lease (Redis or Postgres), it must execute the following sequence before sending any RPCs to the pod:

0. **Pre-CAS session read:** Before executing the CAS UPDATE, the acquiring replica MUST read the session row to obtain `$tenant_id`, `$expected_generation`, and `last_checkpoint_workspace_bytes`. The replica uses `StoreRouter.SessionShard(session_id)` to locate the correct Postgres shard (the shard routing prefix is embedded in the session ID — see [§12.6](12_storage-architecture.md#126-interface-design) session ID format), then executes `SELECT tenant_id, coordination_generation, last_checkpoint_workspace_bytes FROM sessions WHERE id = $session_id` under RLS. If the SELECT returns no row (e.g., session creation was never committed), the replica relinquishes its lease and emits a `session_not_found_on_handoff` structured event — this is a non-retryable failure. In the Postgres fallback path (`SELECT ... FOR UPDATE SKIP LOCKED`), `tenant_id`, `coordination_generation`, and `last_checkpoint_workspace_bytes` are all available from the locked row. The acquiring replica MUST **prime its in-replica `last_checkpoint_workspace_bytes` cache** with the value returned here (if non-null) as part of assume-leader — this guarantees the cache is populated before the replica starts coordinating the session, so a subsequent preStop drain can select the correct tiered cap without a Postgres round-trip even when the drain overlaps a Postgres outage. Without this priming step, every handoff (including those triggered by rolling updates, replica crashes, or rescheduling) would start with a cold cache and force preStop Stage 2 into the 90s conservative fallback whenever Postgres is unreachable during drain. The priming write is idempotent — subsequent checkpoint completions overwrite the cache entry with the latest value. Cache priming failures (e.g., the SELECT returns a row but the local in-memory cache write fails under memory pressure) do not abort the handoff; the replica proceeds and the session falls through to the cold-start behaviour described in the preStop Stage 2 paragraph.
1. **Increment generation (CAS):** Atomically increment `coordination_generation` on the session row in Postgres using a compare-and-swap: `UPDATE sessions SET coordination_generation = $expected_generation + 1 WHERE id = $session_id AND tenant_id = $tenant_id AND coordination_generation = $expected_generation RETURNING coordination_generation`. The `tenant_id` predicate is retained for RLS enforcement and defense-in-depth — shard routing is already handled by `StoreRouter.SessionShard(session_id)` before the query executes, so `tenant_id` is not needed for shard targeting. Including it prevents matching a session row that belongs to a different tenant on the same shard, guarding against application-layer bugs independently of RLS. The `$tenant_id` and `$expected_generation` values are obtained from the pre-CAS session read (step 0 above). If the row has already been incremented (0 rows updated), the replica must re-read `coordination_generation` from Postgres, discard its lease claim, and restart the handoff from lease acquisition. This prevents double-increments if the replica crashes after writing to Postgres but before completing the fence. Note: `tenant_id` is immutable on session rows (set at creation, never updated), so the `tenant_id` predicate cannot independently cause a 0-row result under correct operation — the only expected cause is a stale `coordination_generation`. As a safeguard, if the re-read in the retry path returns a row whose `tenant_id` differs from the value used in the failed CAS, the replica MUST log a `coordinator_handoff_tenant_mismatch` critical structured event, abort without retry, and relinquish the lease — this indicates an application-layer bug, not a normal race condition. The returned value becomes this replica's **local generation stamp**.
2. **Fence announcement (precondition):** Send a `CoordinatorFence(session_id, new_generation)` RPC to the pod with a 5-second deadline. The pod records the new generation and from this point rejects any RPC carrying an older generation. **This step is a hard precondition for step 3: the new coordinator MUST NOT send any operational RPC to the pod until `CoordinatorFence` returns a successful acknowledgement.** Until the pod acknowledges the fence, the pod still accepts RPCs carrying the previous generation, which means the prior coordinator can continue sending RPCs — the generation increment in Postgres alone does not close this window. The fence acknowledgement is what closes it.
   - **If `CoordinatorFence` fails or times out:** The new coordinator must retry the fence RPC with the same generation value (up to 3 attempts with 1-second backoff). If all retries are exhausted, the new coordinator must relinquish the lease — it stops extending the Redis TTL and releases the Postgres `FOR UPDATE` lock — and backs off with jittered delay (initial 2s, max 16s) before reconsidering coordination. The generation increment remains in Postgres; the next coordinator to acquire the lease will increment it again, issuing a higher generation value that supersedes both.
   - **Gap detection on the pod:** When the adapter receives a `CoordinatorFence(new_generation)` RPC, it compares `new_generation` with `last_fenced_generation` (the generation from the last successfully acknowledged fence). If `new_generation > last_fenced_generation + 1`, the gap indicates one or more prior coordinators incremented the generation but never completed fencing. In this case the adapter must: (a) immediately cancel and discard all in-flight RPCs received after `last_fenced_generation` (these originated from unfenced coordinators whose changes must not be applied), (b) reset any transient tool-call or lifecycle state accumulated since the last fenced coordinator, (c) log a `coordinator_generation_gap` structured event recording `last_fenced_generation` and `new_generation`, and (d) acknowledge the fence normally so the new coordinator can proceed. A gap of exactly 1 (`new_generation == last_fenced_generation + 1`) is the normal case and requires no special handling.
3. **Begin coordination:** All subsequent gateway→pod RPCs include the local generation stamp. The pod accepts only RPCs whose generation matches the fenced value. Because fence confirmation is required before this step is reached, there is no window in which both the old and new coordinator can simultaneously issue accepted RPCs to the pod.

**Dual-store unavailability (Redis + Postgres both down):** If both Redis and Postgres are simultaneously unreachable, replicas cannot acquire or verify coordination leases through either mechanism. In this state:

1. **Existing sessions continue:** Replicas that already hold an active coordination lease (cached locally with a known generation) continue serving their existing sessions using in-memory state. Gateway→pod RPCs proceed normally — the pod validates the generation stamp, which remains valid because no new coordinator can increment it while Postgres is down.
2. **New sessions are rejected:** Session creation requires a Postgres INSERT, so new `session.create` requests are rejected with `503 Service Unavailable` and a `Retry-After` header (recommended: 10s). Clients are expected to retry with backoff.
3. **Coordination handoffs are frozen:** No replica can increment `coordination_generation` while Postgres is unreachable, so no handoffs occur. If a coordinating replica crashes during this window, its sessions become uncoordinated until at least one store recovers. The pod detects coordinator loss via gRPC transport failure (see **Coordinator-loss detection** below) and enters a hold state awaiting a new coordinator.
4. **Duration bound:** This degraded mode is bounded by the Postgres RTO (< 30s for managed HA deployments). If dual unavailability exceeds `dualStoreUnavailableMaxSeconds` (default: 60s), replicas begin gracefully terminating sessions that have had no successful store interaction, emitting `session.terminated` with reason `store_unavailable` when Postgres recovers. **Timer anchoring:** The `dualStoreUnavailableMaxSeconds` countdown is per-replica, started at the moment that replica first detects dual-store unavailability (i.e., anchored to outage detection time on that replica — not reset by coordinator crashes). Because no coordination handoff can occur while Postgres is unreachable (see item 3), no replacement coordinator can acquire a session and restart the countdown. Sessions whose coordinator crashes during the outage become uncoordinated and are governed by `coordinatorHoldTimeoutSeconds` (default: 120s) on the pod, not by a new coordinator's dual-store timer. As a result, the total degraded window for any session is bounded by `max(dualStoreUnavailableMaxSeconds, coordinatorHoldTimeoutSeconds)` — not by the number of coordinator crashes.
5. **Observability and client notification:** Replicas emit a `dual_store_unavailable` metric (gauge, 1 while both stores are unreachable) and fire alert `DualStoreUnavailable` immediately on detection. In addition, all active client SSE streams receive a `PLATFORM_DEGRADED` server-sent event carrying `{"reason": "dual_store_unavailable", "retry_after": 10}` within 1 second of both stores being declared unreachable. This event signals clients that new session creation is suspended; in-progress sessions are unaffected while the degraded mode holds.

**Coordinator-loss detection:** The gateway→pod gRPC channel uses standard gRPC keepalive probes (`GRPC_ARG_KEEPALIVE_TIME_MS: 10000`, `GRPC_ARG_KEEPALIVE_TIMEOUT_MS: 5000`). If the coordinating gateway replica crashes or becomes network-partitioned, the pod's gRPC transport layer detects the broken connection within 15 seconds (one keepalive interval plus one timeout). Upon detecting connection loss with no active coordinator, the adapter enters **hold state**:

- **Hold state semantics:** The adapter pauses all runtime activity (no new tool results are delivered, no new lifecycle signals are sent). The runtime process continues running but receives no new instructions. The adapter accepts inbound gRPC connections — specifically, it accepts `CoordinatorFence` RPCs from a new coordinator, which is the only way to exit hold state. All other inbound RPCs are rejected with `UNAVAILABLE` status and a `coordinator_hold` error detail until a new coordinator successfully fences.
- **Hold state timeout:** If no new coordinator issues a successful `CoordinatorFence` within `coordinatorHoldTimeoutSeconds` (default: 120s), the adapter initiates graceful session termination — it sends `terminate` on the lifecycle channel and emits a `session.terminated` event with reason `coordinator_lost` once a coordinator reconnects (or writes it to local disk for post-mortem if no coordinator ever returns). **Before exiting, the adapter SHOULD send a final `AdapterTerminating(session_id, reason: coordinator_lost)` gRPC message to the gateway** on the existing pod-to-gateway control channel (port 50051). This message is the adapter's primary mechanism for notifying the platform of self-termination, since agent pods have zero RBAC bindings and no network path to the kube-apiserver under the security model (Sections 10.3, 13.2) — a direct `Sandbox.status.phase = failed` CRD write is not possible. If the gateway receives `AdapterTerminating`, it immediately transitions the session to the appropriate state (`resuming → resume_pending` or `resuming → awaiting_client_action`) without waiting for the 300-second `resuming` watchdog. If the gRPC channel is also unavailable (e.g., the gateway replica that coordinated this session has itself crashed), the adapter logs the delivery failure and proceeds with local disk post-mortem. In this case, the **orphan session reconciler** (see below) detects the terminated pod within one reconcile interval (60s) and forcibly transitions the session to `failed`. This introduces a worst-case 60-second detection delay compared to a direct CRD write, but is consistent with the zero-RBAC, NetworkPolicy-enforced isolation model for agent pods.
- **Orphan session reconciliation:** If no coordinator ever reconnects and the pod terminates without writing a terminal event to Postgres, the session row remains in a non-terminal state indefinitely. The gateway runs a periodic **orphan session reconciler** (every 60 seconds, same leader-only pattern as orphan claim detection) that cross-references the `agent_pod_state` mirror table: any session in a non-terminal state with an active pod binding — `running`, `attached`, `starting`, `suspended` (with pod), `finalizing`, `input_required` — in Postgres whose corresponding pod has `Terminated` phase in the `agent_pod_state` mirror is forcibly transitioned to `failed` with reason `orphan_pod_terminated`. Sessions in `suspended` state with no pod binding (podless suspension after `maxSuspendedPodHoldSeconds`; see [§6.2](06_warm-pod-model.md#62-pod-state-machine)) are excluded — there is no pod to cross-reference. This bounds the window during which an unrecoverable session holds quota. The reconciler emits `lenny_orphan_session_reconciliations_total` (counter) on each such transition. **Mirror table staleness detection:** The `agent_pod_state` mirror table is updated by the WarmPoolController on every pod state transition; during a controller crash (up to 25s failover), the mirror becomes stale. To detect sustained staleness, the gateway emits a `lenny_agent_pod_state_mirror_lag_seconds` gauge per pool, measuring the time since the last successful mirror update. A `PodStateMirrorStale` warning alert fires when lag exceeds 60s for any pool. When mirror staleness exceeds 60s, the orphan reconciler falls back to direct Kubernetes API queries (via the gateway's `PodLifecycleManager.GetPodStatus`) for sessions in non-terminal states, ensuring orphan detection is not silently blocked by stale mirror data.
- **Observability:** The adapter emits a `lenny_adapter_coordinator_hold` gauge (1 while in hold state) and logs a structured `coordinator_connection_lost` event with the last known generation.

**Concurrent-workspace pod connection loss.** When the gateway loses connection to a concurrent-workspace pod (a pod serving multiple active slots), all active slots on that pod simultaneously enter `resume_pending` state — the connection loss affects the entire pod, not individual slots. The whole-pod replacement trigger ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): fires when `ceil(maxConcurrent / 2)` or more slots **fail or leak** within a 5-minute window) is also triggered immediately on total connection loss, regardless of the per-slot failure or leak count — total connection loss is always a whole-pod failure. The gateway atomically resets the Redis slot counter (`lenny:pod:{pod_id}:active_slots` → 0) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the replacement pod's first slot allocation after recovery, preventing stale counters from blocking new slot assignments on the replacement pod.

**Stale replica behavior:** When a replica receives a generation-stale rejection (from a pod or from a failed Postgres CAS on the session row), it must:

1. **Stop RPCs immediately:** Cancel all in-flight RPCs for that session. Do not retry — the session now belongs to a different coordinator.
2. **Clear local state:** Discard all cached session state (in-memory streams, pending tool calls, buffered events) for that session.
3. **Exponential backoff:** If the replica believes it should re-acquire coordination (e.g., it still holds a Redis lease that has not yet expired), it must back off with jittered exponential delay (initial 500ms, max 8s) before re-checking the generation in Postgres. If the generation has advanced beyond its own, it must release the lease and stop contending.
4. **Log and metric:** Emit a `coordinator_preempted` structured log and increment the `lenny_coordinator_handoff_stale_total` counter for observability.

**Custom metrics pipeline:** Each gateway replica exposes `lenny_gateway_active_streams` (a per-replica gauge of in-flight streaming connections) on its `/metrics` endpoint. Prometheus scrapes these endpoints, and the **Prometheus Adapter** (`k8s-prometheus-adapter`) is configured to surface this metric to the Kubernetes custom metrics API (`custom.metrics.k8s.io/v1beta1`), making it available to the HPA. As an alternative, **KEDA** can be used with a Prometheus scaler trigger targeting the same metric, which simplifies HPA manifest authoring for teams already running KEDA.

**HPA metric role summary (cross-reference [§4.1](04_system-components.md#41-edge-gateway-replicas)):** `lenny_gateway_request_queue_depth` is the **primary HPA scale-out trigger**; `lenny_gateway_active_streams` is a **secondary HPA metric** surfaced alongside `request_queue_depth` and CPU; `lenny_gateway_active_sessions / gateway.maxSessionsPerReplica` is a **capacity ceiling alert** (fires `GatewaySessionBudgetNearExhaustion`, [§16.5](16_observability.md#165-alerting-rules-and-slos)) and is NOT used as an HPA trigger. See [§4.1](04_system-components.md#41-edge-gateway-replicas) for the full metric role table.

**Custom metric pipeline end-to-end latency (Prometheus Adapter path):** The full pipeline from metric change to HPA scale-out decision has the following stages and latency budget:

| Stage                                          | Latency                          | Notes                                                                                                 |
| ---------------------------------------------- | -------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Prometheus scrape interval                     | 15s (default)                    | Configurable; reduce to 10s for Tier 2/3 deployments                                                 |
| Prometheus Adapter cache TTL                   | 30s (default)                    | The adapter caches aggregated metric values; stale cache extends pipeline lag                         |
| HPA evaluation interval                        | 15s (Kubernetes default)         | Not configurable without feature gates                                                                |
| **Worst-case total pipeline lag**              | **~60s** (15s + 30s + 15s)       | Metric change is invisible to HPA for up to 60s in the worst case                                    |
| **Typical total pipeline lag**                 | **~30–45s**                      | Depends on where in the scrape/cache/eval cycle the load spike occurs                                 |

At Tier 3 with a burst session arrival rate of 200/s, a 60s lag window means up to **12,000 session attempts** arrive before the HPA reacts. This exposure window is mitigated by three mechanisms already specified in this section: (a) `minReplicas` sized to absorb expected burst peaks without scale-out, (b) leading-indicator metrics (`lenny_gateway_request_queue_depth`, `lenny_gateway_rejection_rate`) that detect saturation before CPU rises and trigger earlier scale-out, and (c) the `GatewaySessionBudgetNearExhaustion` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)).

**Reducing pipeline lag — KEDA (mandatory for Tier 3, optional for Tier 1/2):** KEDA with a Prometheus scaler trigger can reduce the scrape-to-scale latency by bypassing the Prometheus Adapter cache: KEDA polls Prometheus directly on a configurable `pollingInterval` (default 30s, recommended 10s for Tier 2/3). With `pollingInterval: 10s` and KEDA's own evaluation cycle, worst-case pipeline lag drops to approximately 20s.

**KEDA and standalone HPA are mutually exclusive (SCL-024).** KEDA creates and manages the HPA resource automatically via its `ScaledObject` controller. Deploying a standalone `HorizontalPodAutoscaler` targeting the same Deployment as a KEDA `ScaledObject` creates a conflict: both controllers attempt to set `spec.replicas` on the Deployment, producing oscillation and unpredictable scaling behavior. **When KEDA is used, do NOT deploy a standalone HPA for the same Deployment.** All `behavior.*` settings (scale-up/scale-down policies, stabilization windows) must be specified in the ScaledObject's `advanced.horizontalPodAutoscalerConfig` field, not in a separate HPA manifest. Operators migrating from Prometheus Adapter + standalone HPA to KEDA must delete the standalone HPA before or at the same time as creating the ScaledObject; leaving both in place will cause the KEDA controller to conflict with the existing HPA. The Helm chart enforces this: when `autoscaling.provider: keda`, the chart does not render a standalone `HorizontalPodAutoscaler` resource; when `autoscaling.provider: hpa`, no `ScaledObject` is rendered.

**At Tier 3, KEDA is a mandatory platform requirement, not an optional enhancement.** The rationale: with `maxParallelChildren` at the `orchestrator` preset and 10,000 concurrent sessions, burst session arrival at 200/s means up to 12,000 session attempts arrive during the 60s worst-case Prometheus Adapter pipeline lag — a quantity that will exhaust any reasonable `minReplicas` buffer. The 20s worst-case KEDA pipeline lag reduces that exposure to ~4,000 session attempts, which is within the `minReplicas` burst absorption sizing defined in [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults). The Tier 3 production gate (Phase 14.5) validates that KEDA is deployed and correctly configured as a precondition for GA sign-off. Deployers targeting Tier 3 who cannot deploy KEDA must increase `minReplicas` sufficiently to absorb the full 60s pipeline lag burst — [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults) provides the `minReplicas` formula for both paths.

For Tier 1/2 deployments with generous `minReplicas`, the Prometheus Adapter path is sufficient.

**Recommended configuration to minimize lag (both paths):**
- Prometheus scrape interval: reduce to 10s for gateway pods (`scrapeInterval: 10s` in PodMonitor/ServiceMonitor)
- Prometheus Adapter: set `metricsRelistInterval: 15s` and `cacheMetricResolutionPeriod: 15s` in the adapter config
- KEDA (if used): set `pollingInterval: 10s` on the ScaledObject
- Use `lenny_gateway_request_queue_depth` (averageValue target: 10) as the primary scale-out trigger — this metric reflects current queuing pressure and does not depend on the Prometheus Adapter cache TTL when surfaced via KEDA

**HPA scale-up policy:** Gateway workloads are inherently bursty — a spike in session creation or streaming connections can exhaust existing replicas within seconds, while default HPA behavior introduces 30–60s of lag before new replicas appear. To absorb bursts before scale-up completes, set `minReplicas` high enough that idle replicas can handle expected burst peaks (per-tier values in [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)). Configure aggressive scale-up behavior: `behavior.scaleUp.stabilizationWindowSeconds: 0` (react immediately) with `behavior.scaleUp.policies` using `type: Percent, value: 100, periodSeconds: 15` (double replica count every 15s) and a parallel `type: Pods, value: 4, periodSeconds: 15` (add at least 4 pods per period), combined via `selectPolicy: Max`. In addition to CPU utilization, configure leading-indicator metrics that detect load before CPU saturates: `lenny_gateway_request_queue_depth` (pending requests awaiting a handler goroutine) and `lenny_gateway_rejection_rate` (requests rejected with 429/503 per second). Surface both through the Prometheus Adapter or KEDA alongside `lenny_gateway_active_streams`. An HPA target on queue depth (e.g., `averageValue: 10`) triggers scale-up before CPU rises, closing the lag window. Per-tier scale-up thresholds and `minReplicas` burst-absorption guidance are in [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults).

**HPA scale-down protection:** Use `behavior.scaleDown.stabilizationWindowSeconds: 300` and `behavior.scaleDown.policies` with `type: Pods, value: 1, periodSeconds: 60` to scale down one pod at a time, preventing mass eviction of gateway replicas during traffic dips. Per-tier scale-down policy adjustments are in [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults).

**preStop hook drain:** The gateway pod spec includes a `preStop` hook that executes a staged graceful drain sequence within `terminationGracePeriodSeconds` (per-tier values in [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)). The stages are:

1. **Stop accepting new work (readiness=false).** The preStop hook immediately sets the pod's readiness probe to `false`. This causes the Kubernetes Endpoints controller to remove the pod from the Service's endpoints list, which in turn causes the load balancer to stop routing new requests to this pod. The readiness flip must happen _before_ any drain logic begins, to close the window where the load balancer continues routing new requests during health check propagation lag (typically 1–5 seconds depending on load balancer and kube-proxy configuration). No new sessions or streams are accepted after this point.
2. **Wait for in-flight checkpoints (tiered cap).** If any sessions coordinated by this replica have checkpoints in progress (triggered by periodic scheduling or pre-drain), the hook waits for those checkpoints to complete before proceeding. This prevents SIGKILL from interrupting a checkpoint upload, which would leave checkpoint state inconsistent. The wait uses a **tiered cap** based on the session's last measured workspace size, rather than a fixed 30-second cap (which is insufficient for sessions with hundreds of MB workspaces):

   | Last measured workspace size | Checkpoint cap                  |
   | ---------------------------- | ------------------------------- |
   | ≤ 100 MB                     | 30s (matches P95 SLO from [§4.4](04_system-components.md#44-event--checkpoint-store)) |
   | 101 MB – 300 MB              | 60s                             |
   | 301 MB – 512 MB (hard limit) | 90s                             |

   The gateway reads `last_checkpoint_workspace_bytes` from the session record in Postgres (updated after each successful checkpoint) to select the cap tier. If the field is absent (no prior checkpoint), the 30s default applies. The tiered cap must remain below `terminationGracePeriodSeconds - 30s` to leave at least 30 seconds for stream drain (stage 3); if the calculated cap would violate this constraint, it is clamped to `terminationGracePeriodSeconds - 30s`.

   **Postgres-read failure during preStop Stage 2.** When the Postgres read for `last_checkpoint_workspace_bytes` fails during preStop Stage 2 — a plausible state during Postgres failover (up to 30s per [§12.3](12_storage-architecture.md#123-postgres-ha-requirements)) or a PgBouncer outage ([§12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)) — the gateway MUST NOT block on the read (which would consume the entire `terminationGracePeriodSeconds` budget and leave zero time for stream drain in stage 3) and MUST NOT silently fall back to the 30s default (which would SIGKILL mid-upload for sessions with large workspaces). Instead, the gateway MUST fall back to an **in-replica cache** that mirrors `last_checkpoint_workspace_bytes` for each session this replica currently coordinates. Two write paths populate the cache: (a) every successful checkpoint writes the cache immediately after the Postgres write, making it authoritative whenever checkpoints have been observed on this replica, and (b) the **coordinator-assume-leader priming step** (see step 0 of the Coordinator handoff protocol above) populates the cache from the pre-CAS session read, guaranteeing that the cache is warm for every session this replica coordinates even immediately after rolling updates, replica crashes, or other handoff-triggering events. On cache hit, the tier is selected from the cached value exactly as it would be from the Postgres read. On cache miss — rare under the priming design above, but still possible when a session's most recent checkpoint completed on a prior coordinator **before** this replica's session row read (so the cache was primed with `NULL`) or when cache priming failed due to local memory pressure — the gateway MUST select the **90s maximum tier** (not the 30s default) to avoid truncated checkpoints during correlated infrastructure outages (Postgres failover window plus pod drain). This trades up to 60s of extra preStop wait for preventing mid-snapshot SIGKILL on sessions whose workspace size is unknown to this replica. The cap-vs-stream-drain clamp in the paragraph above still applies after tier selection.

   **Cold-start behaviour after rolling updates.** A gateway rolling update recreates every replica; every inherited session triggers a coordinator handoff on its new replica. The handoff's step 0 priming (above) writes `last_checkpoint_workspace_bytes` into the new replica's cache before the replica begins coordinating the session, so a subsequent preStop drain on the new replica can select the correct tier even if the drain overlaps a Postgres outage. The only cold-start window remaining is the narrow interval between lease acquisition and completion of the step 0 read for each session — if a second disruption (e.g., the new replica is itself evicted) fires during this interval on a sparse set of sessions, those sessions fall through to the 90s fallback. The cold-start window is bounded per-session by the step 0 latency (single SELECT, typically < 10 ms at P99); operators monitoring the `lenny_prestop_cap_selection_total` metric with `source: cache_miss_max_tier` can detect when this path is exercised.

   **Observability — preStop tier selection source.** The gateway emits `lenny_prestop_cap_selection_total` (counter, labeled by `pool` and `source`) once per preStop Stage 2 tier selection. The `source` label distinguishes how the `last_checkpoint_workspace_bytes` value was obtained: `postgres` (Postgres read succeeded — the steady-state healthy path), `cache_hit` (Postgres unreachable or slow, value taken from the in-replica cache populated by a prior checkpoint or by handoff priming), and `cache_miss_max_tier` (neither Postgres nor cache had a value; 90s conservative fallback used). Operators use the ratio `cache_miss_max_tier / (postgres + cache_hit + cache_miss_max_tier)` over a rolling window to distinguish "Postgres healthy — cache never consulted" from "Postgres outage with fully-primed caches" from "Postgres outage overlapping handoff window — sessions hitting 90s conservative cap". Alert `PreStopCapFallbackRateHigh` (warning) fires when the `cache_miss_max_tier` share exceeds 5% of preStop tier selections over any 15-minute window on a given replica, indicating that cold-start priming is not effectively covering the replica's session set (e.g., due to step 0 read failures during dual-store unavailability). The metric is sibling to `lenny_checkpoint_barrier_ack_total` ([Section 16.1](16_observability.md#161-metrics)) and uses the same `pool` label dimension.

   **CRD validation rule — tiered cap + BarrierAck budget:** The `SandboxWarmPool` CRD admission webhook (`lenny-pool-config-validator` — see [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)) enforces that the worst-case preStop budget does not exceed `terminationGracePeriodSeconds`. **Scope of enforcement:** the semantic rules in this subsection (both the tiered-cap + BarrierAck budget rule below and the BarrierAck floor rule that follows) apply to EVERY admission request that mutates `SandboxTemplate.spec` or `SandboxWarmPool.spec` — including PoolScalingController SSA applies. They are NOT subject to the `userInfo`-based bypass described for the authorization-denial rule in [§4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries); a PoolScalingController write that violates the preStop budget is rejected at admission just as a manual `kubectl apply` would be, because admitting such a configuration would guarantee SIGKILL during drain regardless of the writer's identity. Specifically, the webhook rejects any pool configuration where:

   ```
   max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 > terminationGracePeriodSeconds
   ```

   where `max_tiered_checkpoint_cap` is the largest cap tier applicable to the pool's `workspaceSizeLimitBytes` (e.g., 90s for pools allowing up to 512 MB workspaces), `checkpointBarrierAckTimeoutSeconds` is the configured BarrierAck wait (default: 90s), and 30s is the minimum stream-drain budget (stage 3). With defaults (90s + 90s + 30s = 210s), `terminationGracePeriodSeconds` must be set to at least 210s on gateway pods coordinating pools that allow large workspaces. The Helm chart sets `terminationGracePeriodSeconds: 240` by default to provide a 30s safety margin. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 exceeds terminationGracePeriodSeconds; increase terminationGracePeriodSeconds or reduce checkpointBarrierAckTimeoutSeconds / workspaceSizeLimitBytes"`.  Metric: `lenny_pool_termination_budget_exceeded_total` (counter, labeled by `pool`) incremented when the webhook rejects a configuration.

   **CRD validation rule — BarrierAck floor:** The webhook additionally rejects any pool configuration where `checkpointBarrierAckTimeoutSeconds < max_tiered_checkpoint_cap`. The BarrierAck timeout must be at least as long as the longest checkpoint tier cap for the pool: if a pod legitimately needs 90s to upload a 512 MB workspace within its tier cap, a BarrierAck timeout shorter than 90s would declare it unresponsive while it is still making progress. The default `checkpointBarrierAckTimeoutSeconds` (90s) equals the highest tier cap (90s for 512 MB workspaces), satisfying this rule. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"checkpointBarrierAckTimeoutSeconds (<value>s) must be >= max_tiered_checkpoint_cap (<cap>s) for the configured workspaceSizeLimitBytes; increase checkpointBarrierAckTimeoutSeconds"`. Observability: BarrierAck timeouts are counted by `lenny_checkpoint_barrier_ack_total{outcome="timeout"}` ([Section 16.1](16_observability.md#161-metrics)), enabling operators to distinguish legitimate slow uploads from unresponsive pods via the ratio against `lenny_checkpoint_duration_seconds`.

   **Partial manifest on checkpoint timeout:** If a checkpoint upload does not complete within the applicable tier cap, the gateway does not simply abandon the session — it writes a **partial checkpoint manifest** to Postgres before proceeding.

   **Chunked-object storage model (not S3 multipart upload).** Partial checkpoints are stored as a **sequence of separate, independently committed MinIO objects** — not as parts of a single S3 multipart upload. The adapter uploads the workspace tar stream in fixed-size chunks (`partialChunkSizeBytes`, default: 16 MiB, see [§17.8.1](#1781-operational-defaults--quick-reference)), naming each chunk deterministically as `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/partial-{n}.tar.gz` where `{n}` is a zero-padded 5-digit monotonic index starting at `00000`. Each chunk is committed via a single-part `PutObject` (not `UploadPart`). The chunks carry the same wire encoding as the full-checkpoint snapshot path (`tar` or `tar.gz`, matching the workspace snapshot format in [§15.1](15_external-api-surface.md#151-rest-api) `GET /v1/sessions/{id}/workspace`); the manifest's `chunk_encoding` field records which encoding was used for a given partial set. On concatenation in index order the chunks reproduce the original tar (or `tar.gz`) byte stream as if the full upload had completed. This model is chosen because (a) it gives each chunk a stable, individually readable object key (S3 multipart upload parts are only addressable via the parent `UploadId` and cannot be read until `CompleteMultipartUpload`, which is precisely the operation that did not happen in the timeout path), (b) reassembly is a deterministic concat of keys listed by prefix rather than a multipart listing under a (possibly expired) `UploadId`, and (c) orphan cleanup uses plain `DeleteObject` per key with no dependency on S3 multipart lifecycle rules.

   The partial manifest records: `session_id`, `coordination_generation`, `checkpoint_id` (UUID), `checkpoint_started_at`, `checkpoint_timeout_at`, `workspace_bytes_uploaded` (sum of chunk sizes successfully committed to MinIO before timeout), `chunk_count` (number of chunks committed — equal to the highest `{n}` + 1), `chunk_size_bytes` (the `partialChunkSizeBytes` value in effect for this manifest, captured for forward-compatibility if the default changes), `chunk_encoding` (`tar` or `tar.gz`), `partial_object_key_prefix` (the `/{tenant_id}/checkpoints/{session_id}/partial/{checkpoint_id}/` prefix under which chunks are listed), and `partial: true`. The manifest intentionally omits a full `partial_object_keys` array — the keys are derivable from `partial_object_key_prefix` + `partial-{n}.tar.gz` for `n` in `[0, chunk_count)`, and this derivation is cheaper and more space-efficient than materialising the list per manifest row.

   **Chunk boundaries do not align with tar members.** The adapter writes a single continuous `tar` (or `tar.gz`) byte stream during the checkpoint upload and slices that stream into fixed-size chunks at arbitrary byte offsets — the `partialChunkSizeBytes` boundary. Tar member headers, member payloads, and (for `tar.gz`) gzip deflate blocks **will** straddle chunk boundaries; no attempt is made to align chunk cuts with member starts or deflate-block starts, and the manifest does not record per-chunk member offsets. Reassembly is designed around this: per-chunk alignment is unnecessary because individual chunks are never decompressed or parsed as tar in isolation. Only the concatenation of all chunks in index order is consumed as a tar (or `tar.gz`) stream; at that level, the concatenated bytes are byte-for-byte identical to what the full upload would have produced had the tier cap not fired, so the extractor sees a single well-formed (but end-truncated) archive rather than a sequence of independently valid archives.

   **Reassembly on resume.** On session resume, the new coordinator detects `partial: true` in the latest checkpoint record. If `workspace_bytes_uploaded >= partial_recovery_threshold_bytes` (configurable, default: 50% of last full checkpoint size), the coordinator attempts to reconstruct the workspace as follows: (1) list objects under `partial_object_key_prefix` via `ListObjectsV2` and verify contiguity — every index `n` in `[0, chunk_count)` must be present; a gap (a missing intermediate index), an out-of-order index, or an unexpected extra index outside `[0, chunk_count)` fails reassembly atomically before any chunk body is fetched, because splicing non-adjacent regions would corrupt both the gzip deflate stream and the tar framing. (2) Stream the chunks to the adapter in ascending index order (`partial-00000.tar.gz`, `partial-00001.tar.gz`, …) and concatenate the chunk bodies into a **single byte stream** that is fed end-to-end into **one** decompress→untar pipeline inside the agent pod (for `chunk_encoding: tar.gz` this is `gzip -dc | tar -x`; for `chunk_encoding: tar` it is `tar -x` directly). The pipeline reads across chunk boundaries transparently — neither the gzip decompressor nor the tar extractor is reset between chunks, and no intermediate buffering at chunk granularity is performed. This means mid-member and mid-deflate-block chunk cuts are tolerated automatically: as long as the concatenated byte stream is a valid (possibly end-truncated) `tar.gz` or `tar`, the pipeline decodes it correctly. (3) Because the upload itself was truncated at an arbitrary byte offset when the tier cap fired, the tail of the concatenated stream almost certainly terminates mid-member (and, for `tar.gz`, mid-deflate-block); the extractor relies on standard end-of-archive handling so that any trailing bytes after the last successfully read tar member are silently discarded once `tar` encounters an incomplete header or an unexpected EOF within a member (and `gzip -dc` surfaces the truncated-deflate EOF to `tar` as a short read at the final member). The reassembly loop records the byte offset of the last successfully extracted tar member and treats everything after it as lost. (4) **Atomicity of extraction failure.** The decompress→untar pipeline writes into a staging directory (`/workspace/current.partial`) rather than directly into `/workspace/current`; the staging directory is atomically renamed onto `/workspace/current` only after the pipeline has completed with end-truncation being the sole observed error. Any **non-terminal** failure — `ListObjectsV2` contiguity check failure (step 1), a `GetObject` error on a non-final chunk, a gzip CRC or format error at a position that is not the final few bytes of the final chunk, or a tar header parse error mid-stream — aborts reassembly, deletes `/workspace/current.partial` in its entirety, and falls back to the last successful full checkpoint. No partially-extracted files ever become visible under `/workspace/current` on the failure path. The session then resumes with the partial workspace and the client receives a `session.resumed` event with `resumeMode: "partial_workspace"` and `workspaceRecoveryFraction` (bytes recovered / last full checkpoint bytes, computed from the post-extraction on-disk total, not from `workspace_bytes_uploaded`). If reassembly fails (any of the non-terminal failure modes in step 4, or no tar members successfully extracted from the concatenated stream) or `workspace_bytes_uploaded` is below the threshold, the session falls back to the last successful full checkpoint.

   **Cleanup.** Regardless of reassembly outcome, the coordinator deletes every chunk under `partial_object_key_prefix` via per-key `DeleteObject` calls (no `AbortMultipartUpload` is involved because no multipart upload was ever opened), then deletes the Postgres manifest row — see [§4.4](04_system-components.md#44-event--checkpoint-store) partial-checkpoint cleanup and [§12.5](12_storage-architecture.md#125-artifact-store) GC backstop. The `lenny_checkpoint_partial_total` counter (labeled by `pool` and `recovered: true|false`) tracks partial checkpoint events.
3. **Drain active streams (remaining grace period).** The hook polls `active_streams > 0` at 1-second intervals for the remainder of `terminationGracePeriodSeconds` after stages 1 and 2. This gives in-flight streams time to complete naturally or allows clients to detect the closing connection (via gRPC `GOAWAY` or SSE stream close) and reconnect to another replica via the load balancer.
4. **SIGKILL deadline.** If active streams have not drained by the grace period deadline, the process receives SIGKILL and remaining clients must reconnect. Combined with the one-pod-at-a-time scale-down policy, this ensures that at most one replica is draining at any moment.

**Long-running sessions and rolling updates:** Sessions with lifetimes approaching `maxSessionAge` will always exceed the gateway's `terminationGracePeriodSeconds` (240–300s per tier — see [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults)) and will therefore be interrupted by any gateway rolling update. Sticky routing (see above) mitigates this by preferring the same replica for a session's lifetime, but sticky routing is an optimization — it is not guaranteed across all load balancer implementations. Deployments where long-running sessions are common (e.g., interactive coding sessions with `maxSessionAge` > 1800s) should: (a) configure sticky routing at the load balancer layer (e.g., Envoy session affinity, AWS ALB stickiness), (b) schedule rolling updates during low-traffic windows, and (c) monitor the `lenny_gateway_sigkill_streams_total` metric (streams terminated by SIGKILL at grace period deadline) to quantify the impact of forced disconnections.

**CheckpointBarrier protocol for rolling updates:** To prevent in-flight tool calls from being abandoned or silently re-executed at resume time, the gateway uses a `CheckpointBarrier` protocol when a graceful drain is initiated (preStop stage 1):

1. **Barrier signal:** When the preStop hook flips readiness to `false`, it simultaneously sends a `CheckpointBarrier` control message to every pod currently coordinated by this replica. The `CheckpointBarrier` message carries the current `coordination_generation` and a `barrier_id` (monotonically increasing per session).
2. **Pod quiescence:** On receipt of `CheckpointBarrier`, the adapter finishes the current tool call execution (if any), then stops accepting new tool call dispatches. The adapter records the `barrier_id` and the last completed tool call's `tool_call_id` in the session's checkpoint metadata. No new tool calls are started after the barrier.
3. **Checkpoint flush:** After quiescing, the adapter triggers a best-effort checkpoint (full workspace snapshot) and sends `CheckpointBarrierAck(barrier_id, last_tool_call_id, checkpoint_ref)` to the gateway. **All coordinated pods checkpoint in parallel** -- the gateway issues the `CheckpointBarrier` to all pods simultaneously (step 1) and waits for `CheckpointBarrierAck` from all of them under a single wall-clock deadline of `checkpointBarrierAckTimeoutSeconds` (default: 90s), not per-pod. This is why the CRD validation formula (below) adds `max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30` rather than multiplying by session count. At Tier 3 a single gateway replica may coordinate up to 400 sessions (`maxSessionsPerReplica`); during drain, all coordinated pods upload checkpoints concurrently, producing a MinIO I/O burst of up to 400 simultaneous uploads. The MinIO throughput budget in [Section 17.8.2](17_deployment-topology.md#1782-capacity-tier-reference) accounts for drain-triggered bursts via the "burst, max workspace" row. Pods that do not ack within the timeout are treated as unresponsive; the gateway falls back to the last successful periodic checkpoint for those sessions.
4. **Tool call idempotency key:** Each tool call dispatch carries a `tool_call_idempotency_key` — a `(session_id, coordination_generation, tool_call_sequence_number)` tuple — written to the checkpoint metadata alongside `last_tool_call_id`. On resume, the new coordinator compares the pod's `last_tool_call_id` against its own last-dispatched `tool_call_sequence_number` before re-dispatching. If they match, the tool call is not re-sent. If they differ (the pod's `last_tool_call_id` is lower), only the delta calls are re-dispatched, preventing silent duplicate execution of idempotent-unsafe operations. The `tool_call_idempotency_key` and `last_tool_call_id` are stored in two places to survive Postgres unavailability during preStop: (a) embedded in the MinIO checkpoint manifest as `barrier_meta.last_tool_call_id` (written by the adapter as part of the checkpoint object — **primary durable source**), and (b) written to the `session_checkpoint_meta` Postgres table by the gateway after receiving the `CheckpointBarrierAck` (secondary, for fast lookup). The adapter MUST include `last_tool_call_id` in the checkpoint manifest before emitting `CheckpointBarrierAck`; the manifest write to MinIO is part of the checkpoint flush and is covered by the same retry budget.
5. **Resume deduplication:** On coordinator handoff (new gateway replica acquires the coordination lease), it reads `last_tool_call_id` from Postgres (`session_checkpoint_meta`) before sending any tool call RPCs. If the Postgres record is absent (e.g., the previous gateway could not write it before SIGKILL, or Postgres was unavailable during preStop), the new coordinator falls back to reading `barrier_meta.last_tool_call_id` from the MinIO checkpoint manifest for the session's latest checkpoint. Any tool call with `tool_call_sequence_number <= last_tool_call_id` is skipped as already completed. A `coordinator_resume_deduplicated_total` counter tracks skipped duplicates for observability. A `coordinator_resume_meta_source` label (`postgres` or `checkpoint_manifest`) on this counter enables operators to detect how often the MinIO fallback is exercised.

This protocol bounds the rolling-update interruption window to at most one in-flight tool call per session (the one executing when the barrier fires), and provides a deterministic resume path that avoids duplicate execution. Sessions that cannot checkpoint within `checkpointBarrierAckTimeoutSeconds` are interrupted as before; all other sessions are preserved with at-most-once tool call semantics across the update.

### 10.2 Authentication

| Boundary          | Mechanism                                                                  |
| ----------------- | -------------------------------------------------------------------------- |
| Client → Gateway  | OIDC/OAuth 2.1 (MCP-standard protected resource server)                    |
| Automated clients | Service-to-service auth (client credentials grant)                         |
| Gateway ↔ Pod     | mTLS + projected service account token (audience-bound, short TTL)         |
| Pod → Gateway     | Projected service account token (audience: deployment-specific, short TTL) |

**Session capability context:** After authentication, the gateway mints a **signed JWT** via a pluggable `JWTSigner` interface. Two backends are supported:

- **Production:** KMS-backed signing (AWS KMS, GCP Cloud KMS, HashiCorp Vault Transit). The signing key never exists in gateway memory — the gateway sends the JWT claims to the KMS service and receives a signature. This eliminates the risk of key extraction from a compromised gateway process.
- **Dev mode:** Local HMAC-SHA256 key, enabled only when `LENNY_DEV_MODE=true` (see [Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)). This backend must never be used in production deployments.

**JWT claims:**

- `session_id`, `user_id`, `tenant_id`
- `delegation_depth`, `allowed_operations`
- `expiry` (short-lived, refreshed by gateway on each interaction)

**`tenant_id` format validation.** All `tenant_id` values — whether supplied via the admin API (`POST /v1/admin/tenants`), extracted from OIDC claims, or set as the built-in `default` — **must** match the pattern `^[a-zA-Z0-9_-]{1,128}$`. This constraint is enforced at two boundaries: (1) at tenant creation time, the admin API rejects any `tenant_id` that does not match the pattern with `400 INVALID_TENANT_ID`; (2) at OIDC claim extraction time, the gateway rejects tokens whose `tenant_id` claim value does not match the pattern with `401 TENANT_CLAIM_INVALID_FORMAT` (before the tenant-registered lookup). This format constraint ensures `tenant_id` values are safe for use in DDL identifiers (e.g., `billing_seq_{tenant_id}`), MinIO path prefixes, Redis key prefixes, and log fields without risk of injection or path traversal.

**`tenant_id` extraction from OIDC tokens:** The OIDC claim used to derive the tenant identifier is configurable via the `auth.tenantIdClaim` Helm value (default: `tenant_id`). The gateway reads this claim from the validated OIDC ID token immediately after signature verification and before any further request processing. Claim extraction behavior:

| Condition | Gateway behavior |
| --------- | ---------------- |
| Single-tenant deployment (`auth.multiTenant: false`) | Claim is ignored; all requests use the built-in `default` tenant. |
| Claim present and non-empty; tenant registered | Request proceeds with the extracted `tenant_id`. |
| Claim absent or empty string | Request rejected: `401 Unauthorized`, error code `TENANT_CLAIM_MISSING`. |
| Claim present but tenant not registered | Request rejected: `403 Forbidden`, error code `TENANT_NOT_FOUND`. |

Both rejection cases are logged at INFO level (including `user_id` and JWT `jti` for traceability) and emitted as `auth_failure` audit events. There is no silent fallback to the `default` tenant in multi-tenant mode — an absent or unrecognized claim is always a hard rejection. See [Section 4.2](04_system-components.md#42-session-manager) for how `tenant_id` is propagated through the session and database layers.

**Key rotation:** KMS backends support automatic key rotation natively. The JWT `kid` (key ID) header identifies which key version signed the token. During verification, the gateway tries the current and previous key versions, with a configurable overlap window (default 24h). This allows seamless rotation with zero downtime — tokens signed with the old key remain valid until the overlap window closes.

**KMS signing failure mode:** If the KMS service is unavailable when the gateway attempts to mint a new session JWT, the session creation request fails with a retryable `KMS_SIGNING_UNAVAILABLE` error (HTTP 503, `retryable: true`). Existing sessions are **unaffected** — their JWTs are already signed and verification uses the locally cached KMS public key, not a live KMS call. The gateway wraps KMS signing calls in an automatic, in-memory per-subsystem circuit breaker (distinct from the operator-managed circuit breakers in [Section 11.6](11_policy-and-controls.md#116-circuit-breakers)). When the `JWTSigner` circuit breaker trips to open state (> 3 consecutive signing failures within 30s), all new session creation is rejected with `KMS_SIGNING_UNAVAILABLE` until the circuit resets. The `KMSSigningUnavailable` alert ([Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires when the error rate exceeds threshold. Dev-mode (`LENNY_DEV_MODE=true`) uses a local HMAC-SHA256 key and is unaffected by KMS outages, but must never be used in production.

Pods cannot forge or extend this token. The gateway validates the signature on every pod→gateway request.

**Playground auth paths and `origin: "playground"` JWT claim.** The bundled web playground ([§27](27_web-playground.md)) supports three `playground.authMode` values (`oidc`, `apiKey`, `dev`), and the `/playground/*` ingress handler stamps the `origin: "playground"` claim on **every** session-capability JWT it mints, regardless of mode. The minted JWTs are standard gateway session-capability tokens produced by the same `JWTSigner` described above — they inherit KMS signing (production) or HMAC signing (dev mode, `LENNY_DEV_MODE=true`), key rotation, claim structure, and revocation semantics — and carry the added `origin: "playground"` claim that drives the tighter idle-timeout override ([§27.6](27_web-playground.md#276-session-lifecycle-and-cleanup)) and the duration cap. Per-mode mint points:

- **`oidc`:** OIDC authorization-code flow establishes an opaque, HttpOnly, `/playground/`-scoped session cookie; `POST /v1/playground/token` exchanges the cookie for a short-lived MCP bearer JWT with the claim attached. Full flow (login, exchange, refresh, revocation, failure modes) specified in [§27.3.1](27_web-playground.md#2731-oidc-cookie-to-mcp-bearer-exchange).
- **`apiKey`:** the `/playground/*` handler treats the user-supplied "API key" as a **standard gateway bearer token** — the same credential a `lenny-ctl`, agent, or service-to-service caller would present in the `Authorization: Bearer …` header. Accepted credentials are an OIDC ID token (human or service-account OIDC client, per the Client→Gateway and Automated-clients rows of the boundary table above) or a previously-minted gateway session-capability JWT. No dedicated "API-key" primitive exists in v1; "apiKey" is a UI label for "paste a bearer token". The handler runs the standard auth chain (signature, `iss`, `aud`, `exp`, `nbf`), applies the same `tenant_id` extraction and rejection semantics documented for OIDC tokens above (`TENANT_CLAIM_MISSING` / `TENANT_NOT_FOUND` — no silent fallback to `default` in multi-tenant mode), then invokes the session-JWT mint with the `origin: "playground"` claim attached. The claim is stamped by the ingress route, not by the credential material — the same bearer presented on a non-playground route does not produce the claim. Operators who want human-user playground access without distributing service-account tokens should deploy with `playground.authMode=oidc` instead; `apiKey` mode is the right choice when the playground is being driven by an operator-owned service-account token (e.g., during smoke tests or headless runtime-author workflows).
- **`dev`:** the `/playground/*` handler issues a dev HMAC-signed session JWT (dev-mode `JWTSigner`) with the `origin: "playground"` claim attached. Non-playground dev tokens do not carry the claim.

No playground-specific codepath exists on the MCP WebSocket or admin APIs — only the cookie-auth endpoints under `/playground/auth/*` and `/v1/playground/token` are playground-specific.

#### Authorization and RBAC

Authentication alone is not sufficient for multi-tenant deployments. The platform defines a role-based access control model with five built-in roles:

- **`platform-admin`**: Full access to all endpoints across all tenants. Can manage runtimes, pools, global configuration, and platform-wide settings.
- **`tenant-admin`**: Full access scoped to their own tenant. Can manage users, quotas, credential pools, view usage, set legal holds, and configure callback URLs. Can update runtimes and pools that a `platform-admin` has granted to their tenant (via `runtime_tenant_access` / `pool_tenant_access`), but cannot create new global runtime/pool definitions or grant access to other tenants. Cannot access other tenants' data or platform-wide settings.
- **`tenant-viewer`**: Read-only access scoped to their own tenant. Can view sessions, runtimes, pools, usage, environments, and configuration. Cannot create, modify, or delete any resources.
- **`billing-viewer`**: Can view usage and metering data for their own tenant. Cannot view session content or manage any resources.
- **`user`**: Can create and manage their own sessions. Cannot access other users' sessions (even within the same tenant) unless explicitly granted by a tenant-admin.

**Permission matrix:**

| Operation                                      | `platform-admin`  |  `tenant-admin`  |     `tenant-viewer`      |  `billing-viewer`  |          `user`          |
| ---------------------------------------------- | :---------------: | :--------------: | :----------------------: | :----------------: | :----------------------: |
| Create / cancel own sessions                   |        Yes        |       Yes        |            No            |         No         |           Yes            |
| Read own session history                       |        Yes        |       Yes        |    Yes (read-only)       |         No         |           Yes            |
| Manage own credentials (`/v1/credentials`)     |        Yes        |       Yes        |            No            |         No         |           Yes            |
| Read other users' sessions (same tenant)       |        Yes        |       Yes        |    Yes (read-only)       |         No         | Only with explicit grant |
| Manage runtimes                                | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage pools / scaling policies                | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage credential pools                        | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| View usage / metering                          | Yes (all tenants) | Yes (own tenant) |    Yes (own tenant)      | Yes (own tenant)   |            No            |
| Manage quotas                                  | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage users / role assignments                | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Set / release legal holds                      | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Configure callback URLs / webhooks             | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Configure tenant RBAC config                   | Yes (all tenants) | Yes (own tenant) |            No            |         No         |            No            |
| Manage environments                            | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage delegation policies                     | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Manage egress profiles                         | Yes (all tenants) | Yes (own tenant) | Read-only (own tenant)   |         No         |            No            |
| Issue billing corrections (operator-initiated) |        Yes        |        No        |            No            |         No         |            No            |
| Manage platform-wide settings                  |        Yes        |        No        |            No            |         No         |            No            |
| Access other tenants' data                     |        Yes        |        No        |            No            |         No         |            No            |

**Platform roles vs. environment-level roles:** Platform RBAC roles (`platform-admin`, `tenant-admin`, `tenant-viewer`, `billing-viewer`, `user`) govern access to platform API operations. Environment-level member roles (`viewer`, `creator`, `operator`, `admin` — see [Section 10.6](#106-environment-resource-and-rbac-model)) are orthogonal and govern which runtimes and connectors a user can access within an environment context. A `user` with environment `admin` role gains runtime/connector management within that environment but does not acquire `tenant-admin` privileges (quota management, legal holds, user management, etc.). The two role systems compose: platform RBAC gates the API operation, environment RBAC gates the resource scope within that operation.

**Custom roles (tenant-scoped).** Deployers can define custom roles per tenant via the admin API (`POST /v1/admin/tenants/{id}/roles`). A custom role is a named set of permissions drawn from the same operation categories in the permission matrix above. Custom roles cannot exceed the permissions of `tenant-admin` — they can only restrict, not expand. This allows tenant-admins to create purpose-specific roles (e.g., `session-manager` that can create and view sessions but not manage quotas, or `connector-admin` that can manage connectors only). Custom roles are stored in the tenant RBAC configuration and conveyed via the same OIDC claim / platform-managed mapping as built-in roles.

**Role assignment:** Roles — both built-in and custom — are conveyed via OIDC claims (e.g., a `lenny_role` claim in the ID token, which can carry any built-in or custom role name) or via a platform-managed mapping (`user_id` → role stored in Postgres). When both sources are present, the platform-managed mapping takes precedence, allowing tenant-admins to override OIDC-derived roles within their tenant. Custom roles are assignable through the same mechanisms as built-in roles.

**Tenant-scoped admin API:** Admin endpoints (`GET /v1/usage`, `GET /v1/pools`, `GET /v1/metering/events`) are tenant-scoped for `tenant-admin`, `tenant-viewer`, and `billing-viewer` callers — they only return data belonging to the caller's tenant (with `billing-viewer` restricted to usage/metering endpoints only). `platform-admin` callers see data across all tenants, with an optional `?tenant_id=` filter.

**Runtime and pool scoping clarification:** In the permission matrix above, "own tenant" for "Manage runtimes" and "Manage pools / scaling policies" means the `tenant-admin` can only manage runtimes and pools that are in their tenant's access tables (`runtime_tenant_access`, `pool_tenant_access`). These records are platform-global and carry no `tenant_id` field — they are not isolated by RLS. Tenant-scoped visibility and write authorization are enforced by application-layer filtering against the access tables (see [Section 4.2](04_system-components.md#42-session-manager)). A `platform-admin` creates runtimes/pools and grants tenant access; a `tenant-admin` may then update configurations within the scope of already-granted records but cannot create new global runtime/pool definitions or grant access to other tenants.

**Future: self-service portal:** A web UI for tenant-admins to manage their configuration (users, quotas, callback URLs, legal holds) is a future goal, built on top of these tenant-scoped APIs.

### 10.3 mTLS PKI

**Certificate authority:** cert-manager with a cluster-internal CA (self-signed issuer or Vault-backed for production). This is the default; a service mesh (Istio/Linkerd) is an optional alternative. **cert-manager minimum version: v1.12.0** (required for `CertificateRequest` approval controller and stable `Certificate` webhook behavior). The preflight Job verifies that cert-manager CRDs are present and the configured `ClusterIssuer` is `Ready` (see [§17.6](17_deployment-topology.md#176-packaging-and-installation)); it also checks the cert-manager version via `kubectl get deployment cert-manager -n cert-manager -o jsonpath='{.spec.template.spec.containers[0].image}'` and fails if the version is below `v1.12.0`. cert-manager is declared as an optional Helm dependency (`condition: certmanager.enabled`, default `true`); deployments using a service mesh for mTLS may set `certmanager.enabled: false`.

**Certificate lifecycle:**

| Component               | Certificate TTL | SAN Format                                           | Rotation                                                |
| ----------------------- | --------------- | ---------------------------------------------------- | ------------------------------------------------------- |
| Gateway replicas        | 24h             | DNS: `lenny-gateway.lenny-system.svc`                | cert-manager auto-renewal at 2/3 lifetime               |
| Agent pods              | 4h              | SPIFFE URI: `spiffe://<trust-domain>/agent/{pool}/{pod-name}` (trust domain from `global.spiffeTrustDomain`) | cert-manager auto-renewal; pod restart if renewal fails |
| Controller              | 24h             | DNS: `lenny-controller.lenny-system.svc`             | cert-manager auto-renewal                               |
| Token Service | 24h             | DNS: `lenny-token-service.lenny-system.svc`          | cert-manager auto-renewal                               |

**Pod identity:** Agent pods use SPIFFE-compatible URIs as SANs, formatted as `spiffe://<trust-domain>/agent/{pool}/{pod-name}`. The trust domain is configurable via Helm value `global.spiffeTrustDomain` (default: `lenny`; deployers **must** override this to a deployment-specific value such as `lenny-<cluster-name>-<namespace>` in any environment where multiple Lenny instances share the same Kubernetes cluster and CA). Two deployments sharing a trust domain and CA would have overlapping SPIFFE URIs, enabling cross-deployment pod impersonation — a `global.spiffeTrustDomain` override eliminates this risk. The `lenny-preflight` Job warns when `global.spiffeTrustDomain` is set to the default value and more than one Lenny Deployment is detected in the cluster, prompting the deployer to set a distinct trust domain. The gateway validates the SPIFFE URI against the expected pool/pod on each connection. Each gateway replica gets a distinct certificate so compromise of one replica can be detected and revoked independently.

**Token Service identity:** The gateway validates the Token Service's DNS SAN (`lenny-token-service.lenny-system.svc`) on every mTLS connection — it must not accept any certificate from the cluster CA. The Token Service validates that incoming connections present a gateway replica certificate (matching the gateway's DNS SAN `lenny-gateway.lenny-system.svc`), rejecting connections from any other component. This mutual SAN validation ensures that compromise of an unrelated component's certificate cannot be used to impersonate either the gateway or the Token Service.

**Pod ↔ Gateway mTLS peer validation (NET-060).** The adapter→gateway gRPC link ([§4.7](04_system-components.md#47-runtime-adapter) Startup Sequence step 2) is authenticated by mTLS with symmetric SAN validation on both sides — the same posture as the Token Service ↔ Gateway link above. The exact rules:
- **Gateway validates pod identity on every inbound handshake** against the SPIFFE URI format `spiffe://<trust-domain>/agent/{pool}/{pod-name}`, matched against the expected `{pool}` and `{pod-name}` for the connecting pod (resolved from the `SandboxClaim` and `Sandbox` records the gateway has for that pod). A certificate whose SPIFFE URI parses correctly but belongs to a different pool or pod — or whose trust domain does not equal `global.spiffeTrustDomain` — is rejected at handshake with no gRPC response, and the attempt is logged as `pod_identity_mismatch`.
- **Pod (adapter) validates gateway identity on every outbound handshake** against the gateway's DNS SAN `lenny-gateway.lenny-system.svc` (the Service DNS under which all gateway replicas are reachable). The adapter configures its gRPC client with an explicit `tls.Config.ServerName` equal to this DNS name so that Go's standard `crypto/tls` verification chain rejects any cluster-CA-signed certificate whose SAN does not cover this name — in particular, certificates issued to the Token Service, controller, or any other `lenny-system` workload. A handshake failure causes the adapter to exit non-zero (the pod enters `CrashLoopBackOff`) rather than retry against a potentially impersonated peer.

Both sides reject on SAN mismatch at TLS handshake time — before any gRPC frame is sent — so an attacker holding a valid cluster-CA-signed cert for an unrelated workload cannot impersonate either endpoint. `global.spiffeTrustDomain` is the single trust-domain anchor for the pod SPIFFE URI check; `lenny-gateway.lenny-system.svc` is the single DNS-SAN anchor for the gateway check. Neither side falls back to CA-only trust: possession of a valid cluster-CA certificate is necessary but never sufficient.

**Projected SA token:** Configured with `expirationSeconds: 900` (15 minutes). Kubelet auto-refreshes the token before expiry. The gateway validates the audience claim on every pod→gateway request. The audience value **must be deployment-specific** — formatted as `lenny-gateway-<cluster-name>` — to prevent token replay across Lenny deployments sharing the same Kubernetes cluster. This is configurable via Helm value `global.saTokenAudience` (default: `lenny-gateway-default`; deployers **must** override this to a cluster-specific value such as `lenny-gateway-<cluster-name>` in any environment where multiple Lenny instances share the same Kubernetes cluster). The `lenny-preflight` Job warns when `global.saTokenAudience` is set to the default value and more than one Lenny Deployment is detected in the cluster, prompting the deployer to set a distinct audience. The ServiceAccount bound to agent pods has **zero RBAC bindings** — no Kubernetes API access. The projected SA token is one layer in a defense-in-depth strategy. It must be used alongside mTLS (the gateway validates the pod's SPIFFE certificate) and NetworkPolicy (only gateway pods can reach agent pods). None of these controls is sufficient alone — the SA token prevents token replay across audiences, mTLS prevents impersonation, and NetworkPolicy prevents unauthorized network access.

**Admission-time RBAC enforcement:** A Kyverno or Gatekeeper policy must validate that ServiceAccounts used by agent pods in the `lenny-agents` and `lenny-agents-kata` namespaces have no RoleBindings or ClusterRoleBindings. This prevents accidental RBAC escalation — if a deployer or automation adds a binding to an agent SA, the policy blocks pod admission until the binding is removed. This complements the zero-RBAC-bindings design stated above by shifting enforcement left to admission time rather than relying solely on convention.

**For long-running sessions:** SA token refresh is handled transparently by kubelet (projected token `expirationSeconds: 900`, auto-refreshed before expiry). Agent pod certificates (4h TTL) may expire during sessions where `maxSessionAge` exceeds the certificate lifetime. Both the gateway and the runtime adapter **must** use filesystem-watching TLS configuration (`tls.Config` with `GetCertificate`/`GetClientCertificate` callbacks that re-read from the cert-manager-managed projected volume) so that renewed certificates are picked up transparently without restarting the pod or dropping the gRPC connection. This decouples session duration from certificate lifetime — `maxSessionAge` can be set to any value without affecting the certificate security posture.

**cert-manager Failure Modes and CA Rotation:**

1. **Warm pool cert awareness:** The warm pool controller should verify that newly created pods have valid certificates before marking them as `idle`. If cert-manager fails to issue a certificate within 60s of pod creation, the pod is marked as unhealthy and replaced. Additionally, the controller continuously tracks certificate expiry on idle pods and proactively drains any idle pod whose certificate will expire within 30 minutes, replacing it with a fresh pod. This prevents the scenario where a pod idle for most of its 4h cert TTL is claimed with only minutes of validity remaining — insufficient even for short sessions, and cert renewal during the first minutes of a session is an unnecessary risk (see [Section 4.6](04_system-components.md#46-pod-lifecycle-controllers)).
2. **Alerting:** `CertExpiryImminent` alert (referenced in [Section 16.5](16_observability.md#165-alerting-rules-and-slos)) fires if any certificate is within 1h of expiry — since auto-renewal should happen at 2/3 lifetime, this indicates a cert-manager failure.
3. **cert-manager HA:** cert-manager should run with 2+ replicas and leader election in production. A single cert-manager failure should not prevent certificate renewal across the cluster.
4. **CA rotation procedure:** When rotating the cluster-internal CA (e.g., annually or on compromise):
   - Deploy a new CA certificate alongside the old one (both trusted during overlap period)
   - Issue new certificates signed by the new CA via cert-manager
   - Pods pick up new certificates on next rotation cycle (within 2/3 of their TTL)
   - After all certificates have rotated, remove the old CA from trust bundles
   - Gateway, controller, and Token Service trust bundles must include both CAs during the overlap window

**Certificate revocation:** Short-lived certificates (4h TTL) are the primary mitigation against compromised pods — stolen certs expire quickly. For immediate revocation, the gateway maintains an in-memory **certificate deny list** keyed by SPIFFE URI or certificate serial number. When a pod is terminated for security reasons (e.g., anomalous behavior detected by the controller), the gateway adds its certificate to the deny list and rejects any subsequent mTLS connection presenting that cert. The deny list is propagated across gateway replicas via Redis pub/sub (with Postgres `LISTEN/NOTIFY` as fallback). Entries are ephemeral — each entry expires when the certificate's natural TTL lapses (at most 4h), keeping the list small.

**Redis and PgBouncer TLS enforcement (NET-004):** NetworkPolicy is L3/L4 only — it can restrict which pods reach Redis and PgBouncer but cannot enforce that connections are TLS-encrypted. A misconfigured gateway client library or environment variable override could result in a plaintext connection even when NetworkPolicy allows the traffic. Two server-side controls close this gap:

- **Redis:** Redis **must** be configured with `tls-auth-clients yes` (requires client certificates) and `tls-port` set to the TLS listener port (default `6380`) with the plaintext `port` set to `0` (disabled). This makes plaintext connections structurally impossible — Redis will not accept them regardless of client configuration.
- **PgBouncer:** PgBouncer **must** be configured with `client_tls_sslmode = require` (or `verify-full` for production) so that plaintext client connections are rejected at the PgBouncer listener. The upstream Postgres connection from PgBouncer uses `server_tls_sslmode = verify-full`.

**Gateway startup TLS probe:** Each gateway replica **must** run a startup probe that verifies TLS connectivity to Redis and PgBouncer before the replica is marked ready. The probe attempts a TLS handshake to both endpoints and fails startup if either handshake fails or if a plaintext connection is accepted. This converts configuration errors (wrong port, missing cert) from silent runtime failures into deployment failures caught before traffic is served.

**Gateway startup configuration validation (platform defaults).** In addition to the TLS probe above, each gateway replica **MUST** validate that required platform-level configuration keys are present before marking itself ready. Missing any required key is a hard startup failure — the gateway MUST refuse to become ready (readiness probe returns `false`, preventing the replica from receiving traffic) and MUST emit a `LENNY_CONFIG_MISSING` structured log entry at `FATAL` level identifying the missing key(s). The required-key list is:

| Key | Acceptable values | Rationale |
| --- | --- | --- |
| `auth.oidc.issuerUrl` | Non-empty URL (outside dev mode) | OIDC token validation requires a configured issuer. |
| `auth.oidc.clientId` | Non-empty string (outside dev mode) | OIDC client registration is required for token audience checks. |
| `defaultMaxSessionDuration` | Positive duration | Bounds session lifetime; unbounded defaults are unsafe. |
| `noEnvironmentPolicy` | `deny-all` or `allow-all` | Governs runtime access for users with no environment membership (see [Section 10.6](#106-environment-resource-and-rbac-model) and [Section 11](11_policy-and-controls.md#11-policy-and-controls)). Operators set this via the Helm value `global.noEnvironmentPolicy` (see [Section 17.6](17_deployment-topology.md#176-packaging-and-installation)). An omitted value MUST be treated as `deny-all` *only when explicitly defaulted by the Helm chart*; the gateway does not infer a default at runtime — the value must reach the gateway as an explicit setting so that a misconfigured chart (with the default stripped) fails closed at startup rather than silently running with undefined semantics. |

Each missing key produces a separate `LENNY_CONFIG_MISSING` log entry with structured fields `config_key` (e.g., `noEnvironmentPolicy`), `scope` (`platform` or `tenant`), and `remediation` (a short pointer to the relevant Helm value or admin API path). The gateway exits non-zero after emitting the log entries so Kubernetes surfaces the failure as `CrashLoopBackOff`.

**Integration test requirement:** The test suite **must** include tests (`TestRedisTLSEnforcement`, `TestPgBouncerTLSEnforcement`) that assert plaintext connection attempts to Redis and PgBouncer are rejected with a connection error. These tests run in the CI environment where Redis and PgBouncer are deployed with the production TLS configuration. The test suite **must** also include a `TestGatewayConfigValidation` test that asserts each required key from the configuration validation table above causes a startup failure when absent, that the corresponding `LENNY_CONFIG_MISSING` log entry is emitted with the correct `config_key` value, and that the OIDC keys (`auth.oidc.issuerUrl`, `auth.oidc.clientId`) are exempt from the startup gate when `LENNY_DEV_MODE=true` (mirroring the dev-mode symmetry of the TLS probe — see [Section 17.4](17_deployment-topology.md#174-local-development-mode-lenny-dev)).

### 10.4 Gateway Reliability

**Design principle:** Gateway pod failure causes a broken stream and reconnect, never session loss.

**Mechanisms:**

- All session truth externalized (Postgres)
- Distributed session lease for active coordination
- App-level reconnect: client reconnects with session_id, gateway looks up state, reattaches
- Multiple replicas across zones/nodes
- PodDisruptionBudget limits voluntary disruptions
- Readiness probes remove unhealthy replicas from traffic
- Rolling updates preserve capacity

**Event replay buffer.** The coordinating gateway replica maintains a per-session ring buffer of the most recent `SessionEvent` envelopes (each with its `SeqNum`, see [§15](15_external-api-surface.md#15-external-api-surface) `SessionEvent` schema) so that a client that reconnects via `attach_session` with `resumeFromSeq` ([§15.2](15_external-api-surface.md#152-mcp-api)) receives every event with `SeqNum > resumeFromSeq` that is still retained, followed by live delivery. Buffer depth is set by `gateway.sessionEventReplayBufferDepth` (Helm value, default 512 events, range 64–4096); at the default event rate for an interactive session this comfortably covers a typical 60-second reconnect window. When a requested `resumeFromSeq` points to an event that has been evicted, the adapter emits a single protocol-level `gap_detected` frame (`{"lastSeenSeq": N, "nextSeq": M}`) before resuming at the oldest retained event, so the client can surface a gap warning rather than silently losing events. The `gap_detected` frame is a stream-control signal — it is not itself a `SessionEvent`, carries no `SeqNum`, and is not part of the `SessionEventKind` closed enum. The buffer is coordinator-local and is deliberately discarded on coordinator handoff (see "Stale replica behavior" in [§10.1](#101-horizontal-scaling)); a client reattaching to a new coordinator after handoff observes the gap-detected path for events that predate the handoff.

### 10.5 Upgrade and Rollback Strategy

**Gateway:** Rolling Deployment updates. Schema migrations use expand-contract pattern (add new columns/tables first, deploy code that writes both old and new, then migrate reads, then drop old). Mixed-version replicas must coexist during rollout.

**Schema Migration Tooling and Runbook:**

- **Tooling:** Use `golang-migrate` (or Atlas) for versioned, file-based migrations. Each migration is a pair of up/down SQL files stored in the repository under `migrations/`.
- **Execution:** Migrations run as a Kubernetes Job (init container or pre-deploy hook) before the gateway Deployment rolls out. The Job acquires a Postgres advisory lock to prevent concurrent migration runs.
- **Expand-contract discipline:**
  - Phase 1 (expand): Add new columns/tables, deploy code that writes to both old and new.
  - Phase 2 (migrate reads): Switch reads to new schema.
  - Phase 3 (contract): Drop old columns/tables in a subsequent release.
  - Each phase is a separate migration file and a separate deployment.
- **Expand-contract operational rules:**
  - **Nullable columns during coexistence:** All new columns added in Phase 1 **must** be `NULL`-able (or have a server-side `DEFAULT`) until Phase 3 removes the old columns. During a rolling deploy, old-version replicas that do not know about the new column will issue `INSERT` statements that omit it — a `NOT NULL` constraint without a default causes those inserts to fail. The `NOT NULL` constraint may only be added in Phase 3, after all replicas run the new code.
  - **Minimum inter-phase wait:** Phase 2 must not be deployed until all replicas are running Phase 1 code (i.e., the Phase 1 rolling deploy is fully complete). Phase 3 must not be deployed until every record that could have been written under the old schema has either been migrated or expired. The minimum wait before Phase 3 is `max(maxSessionAge, longest_record_TTL)` for the affected table — for the session store this is `maxSessionAge` (default 7200s / 2h); for audit records it is the audit retention window.
  - **Phase 3 enforcement gate (required):** Every Phase 3 migration file **must** begin with a preflight verification block that the migration runner executes before applying any DDL. The runner issues the count query `SELECT COUNT(*) FROM <table> WHERE <old_column> IS NOT NULL` (or an equivalent expression capturing un-migrated rows for the affected table and column) and **aborts the migration with a non-zero exit code** if the result is nonzero, emitting: `"Phase 3 gate failed: <N> un-migrated rows remain in <table>.<old_column>. Resolve data migration before retrying."` The advisory lock is held during this check so no concurrent Phase 3 run can bypass it. The preflight block is encoded as a PL/pgSQL `DO` block at the top of the up-migration SQL file, ensuring the check runs inside the same transaction as the DDL and is not skippable by the operator. **Phase 3 gate query performance:** The `COUNT(*)` WHERE query used in the gate check must execute against an indexed column. Each Phase 3 migration file comment must declare the index expected to satisfy the check (e.g., `-- gate-index: idx_sessions_legacy_token_partial`). If the column to be dropped does not have a covering partial index (`WHERE <old_column> IS NOT NULL`), the migration runner will warn: `"Phase 3 gate: no partial index found for <table>.<old_column> IS NOT NULL — count query may scan full table (<estimated_rows> rows). Consider adding index before proceeding."` For tables exceeding 1 million rows the operator must confirm the gate query plan (`EXPLAIN (ANALYZE, BUFFERS)`) shows an index scan before applying Phase 3 DDL. `lenny-ctl migrate status` (see [Section 24.13](24_lenny-ctl-command-reference.md#2413-migration-management)) surfaces the current migration phase and gate-check result without requiring direct DB access. Example pattern:
    ```sql
    DO $$
    DECLARE remaining bigint;
    BEGIN
      SELECT COUNT(*) INTO remaining FROM sessions WHERE legacy_token IS NOT NULL;
      IF remaining > 0 THEN
        RAISE EXCEPTION 'Phase 3 gate failed: % un-migrated rows remain in sessions.legacy_token', remaining;
      END IF;
    END $$;
    -- DROP COLUMN follows only if the DO block succeeds
    ALTER TABLE sessions DROP COLUMN IF EXISTS legacy_token;
    ```
  - **Idempotency requirements:** Phase 1 migrations must use idempotent DDL (`ALTER TABLE ... ADD COLUMN IF NOT EXISTS`, `CREATE TABLE IF NOT EXISTS`). Phase 2 migrations (switching read paths) are application-code deploys and are inherently re-deployable. Phase 3 migrations (`ALTER TABLE ... DROP COLUMN`) are **not** idempotent — `DROP COLUMN` fails if the column does not exist. Phase 3 migrations must use `DROP COLUMN IF EXISTS` or guard with a pre-check. If any migration step fails mid-transaction, the advisory lock is released and the migration can be re-run safely because each step is wrapped in a transaction.
- **Rollback:** Down migrations are always provided but only used as a last resort. The expand-contract pattern means the previous code version is compatible with the current schema, since old columns are not removed until the code no longer reads them.
- **Locking:** `golang-migrate` uses Postgres advisory locks to prevent concurrent migrations. The lock is released on completion or failure.
- **Partial completion:** If a migration fails mid-way, the advisory lock is released and the migration can be re-run. Each migration step should be idempotent where possible (see idempotency requirements above for per-phase guidance).

**Warm Pool Controller:** Rolling update with leader election. During leader failover (near-zero on clean shutdown via voluntary lease release; up to 25s on crash), existing sessions are unaffected; only new pod creation and scaling pause.

**CRD schema versioning during rolling deploys:** CRDs follow Kubernetes API versioning conventions (shipping at `v1alpha1` initially and graduating to `v1beta1` → `v1`; see [Section 15.5](15_external-api-surface.md#155-api-versioning-and-stability) for the graduation criteria and conversion webhook deployment procedure). During a rolling deploy, the gateway and controller may briefly run different versions that expect different CRD schemas. Conversion webhooks translate between CRD versions so both components operate correctly during the transition. CRD specs use `x-kubernetes-preserve-unknown-fields` on extensible sub-objects so that a controller running an older version does not crash on fields introduced by a newer gateway (or vice versa). CRD schema changes follow the same expand-contract discipline as database migrations (above): new fields are added first, both versions write them, then old fields are removed in a subsequent release.

**Helm CRD upgrade limitation:** Helm does not update CRDs on `helm upgrade` — this is a known Helm limitation. Stale CRDs after an upgrade can cause silent failures (e.g., new fields are stripped by the API server, controllers observe unexpected defaults). CRDs must be applied separately before running `helm upgrade`. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) for the full packaging and recovery details. To detect stale CRDs at runtime, each controller validates on startup that the installed CRD schema version (read from the `lenny.dev/schema-version` annotation on the CRD object) matches the version the controller binary expects. If there is a mismatch, the controller logs a `FATAL` error — `"CRD schema version mismatch: installed=<installed>, expected=<expected>. Apply updated CRDs before upgrading. See docs/runbooks/crd-upgrade.md"` — and exits with a non-zero code, preventing the Deployment rollout from completing. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) for the full recovery procedure when this occurs.

**CRD upgrade procedure.** Every Lenny upgrade that includes CRD schema changes must follow this sequence. A `lenny-upgrade` script (located at `scripts/lenny-upgrade.sh` in the repository, also available as `make upgrade RELEASE=<version>`) automates the required steps:

1. **Preflight — assert CRD version currency.** Run `lenny-ctl preflight --config <values.yaml>` (or the equivalent `lenny-preflight` Helm pre-upgrade Job). The preflight check compares the `lenny.dev/schema-version` annotation on each installed CRD against the version expected by the target chart release. If any CRD is stale, preflight fails immediately with: `"CRD '<name>' schema version is '<installed>'; expected '<expected>'. Apply updated CRDs before running helm upgrade."` This step catches stale CRDs before any workload change, preventing silent field-stripping.

2. **Diff CRDs.** The `lenny-upgrade` script diffs the CRDs in `charts/lenny/crds/` (from the target release) against the currently installed CRDs using `kubectl diff -f charts/lenny/crds/`. The diff output is printed and the operator must confirm (or use `--non-interactive` in CI) before proceeding.

3. **Apply CRDs.** Run `kubectl apply -f charts/lenny/crds/`. This updates all CRD schemas in the cluster before any controller binary changes are deployed.

4. **Wait for CRD establishment.** The script waits for each updated CRD to reach `Established=True` condition: `kubectl wait --for=condition=Established crd/<name> --timeout=60s`. This ensures the API server has ingested the new schema before controllers start using it.

5. **Run `helm upgrade`.** Proceed with the normal Helm upgrade. Controllers validate the CRD schema version on startup and will refuse to start (non-zero exit, `CrashLoopBackOff`) if any CRD is still stale at this point, providing a hard stop against partial upgrades.

**Script usage:**
```
# Interactive (prompts for diff confirmation)
scripts/lenny-upgrade.sh --release <version> --namespace <ns> --values <values.yaml>

# Non-interactive (CI / GitOps)
make upgrade RELEASE=<version> NAMESPACE=<ns> VALUES=<values.yaml> NON_INTERACTIVE=true
```

**GitOps note:** When using ArgoCD or Flux, configure CRD manifests in a dedicated sync wave (e.g., `argocd.argoproj.io/sync-wave: "-5"`) that applies before main chart resources. Without a separate sync wave, controllers and CRDs may be applied in arbitrary order, reproducing the same stale-CRD failure. See [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) for GitOps-specific configuration.

**Runtime adapters and agent binaries:** Versioned pool rotation uses the `RuntimeUpgrade` state machine defined below.

#### `RuntimeUpgrade` State Machine

Runtime image upgrades (new adapter version, OS patch, dependency update) follow a tracked, pauseable state machine. The state machine is surfaced via `GET /v1/admin/pools/{name}/upgrade-status` and managed by `lenny-ctl admin pools upgrade`.

**States:**

```
Pending → Expanding → Draining → Contracting → Complete
                                ↘ Paused (from any state except Complete)
                                ↗
```

| State | Description | Entry condition | Exit condition |
|---|---|---|---|
| `Pending` | Upgrade is registered but not yet started. Both old and new `SandboxTemplate` CRDs exist. New pool has `minWarm: 0`. | Operator runs `lenny-ctl admin pools upgrade start --pool <name> --new-image <digest>` | Operator runs `lenny-ctl admin pools upgrade proceed` or auto-proceeds if `autoAdvance: true` |
| `Expanding` | New pool is ramping up. `minWarm` for the new pool is set to the target value. New sessions are routed to the new pool (or canary-split if `canaryPercent` is set). | Proceed from `Pending` | New pool `idlePodCount >= newPool.minWarm` for at least `stabilizationWindowSeconds` (default: 120s). New pool health check passes. |
| `Draining` | Old pool is accepting no new sessions (`minWarm = 0`, new session routing disabled). Existing sessions on old pods run to completion. | Proceed from `Expanding` | Old pool `activePodCount == 0` (all sessions completed) or `drainTimeoutSeconds` (default: `maxSessionAge`; configurable) expires. On timeout, remaining sessions are force-terminated with checkpoint. |
| `Contracting` | Old `SandboxTemplate` CRD and pool record are being deleted. Old pods are all terminated. | Proceed from `Draining` | Old pool deletion confirmed (`kubectl get sandboxtemplate <old-name>` returns `NotFound`). |
| `Complete` | Upgrade is finished. Only the new pool remains. | Proceed from `Contracting` | Terminal — no further state transitions. |
| `Paused` | Upgrade is paused. All state machine activity halts. New pool may continue serving sessions; old pool retains its current state. | Operator runs `lenny-ctl admin pools upgrade pause` from any non-terminal state | Operator runs `lenny-ctl admin pools upgrade resume` |

**Pause and resume.** Any operator can pause the state machine at any point before `Complete`:

```
lenny-ctl admin pools upgrade pause --pool claude-worker
lenny-ctl admin pools upgrade resume --pool claude-worker
```

While paused, the old pool remains in whatever state it was in when `pause` was issued. If paused during `Draining`, old pods continue serving their current sessions but no new sessions are routed to the old pool. Pausing during `Expanding` halts new pod creation at the current count. The pause reason and timestamp are stored in the `RuntimeUpgrade` record.

**Schema migration interaction.** If the new runtime image requires a schema migration (e.g., new workspace format, new adapter manifest fields), the operator must:

1. Complete the schema migration Phase 1 (expand) and fully roll out gateway code that writes to both old and new schema before starting the `RuntimeUpgrade`.
2. Set `drainFirst: true` on the `RuntimeUpgrade` — this forces the state machine to complete `Draining` (old pool reaches `activePodCount == 0`) before proceeding to `Contracting`. This ensures no old-schema pod is alive when Phase 3 (contract) runs.
3. Run Phase 3 migration only after the `RuntimeUpgrade` reaches `Complete`.

The `RuntimeUpgrade` record includes a `schemaVersion` field. If `schemaVersion` is set, the gateway blocks Phase 3 migration attempts while `upgradeState != Complete` for the referenced pool.

**Rollback procedure.** If the new pool version is broken at any state:

- **From `Expanding`:** `lenny-ctl admin pools upgrade rollback` — sets new pool's `minWarm` to 0, restores full routing to old pool, transitions to `Paused`. Operator resolves the issue and re-runs `upgrade start` with a corrected image.
- **From `Draining` or `Contracting`:** Rollback is possible if the old pool's `SandboxTemplate` CRD has not yet been deleted. `lenny-ctl admin pools upgrade rollback --restore-old-pool` recreates the old pool configuration from the stored `RuntimeUpgrade.previousPoolSpec` field and restores routing. **If `SandboxTemplate` has already been deleted (late `Contracting`), rollback requires the operator to manually recreate the old `SandboxTemplate` CRD** from version control or Helm values. For this reason, the old pool's spec is always preserved in `RuntimeUpgrade.previousPoolSpec` until the upgrade reaches `Complete`.
- **Key safety invariant:** The old `SandboxTemplate` CRD is never deleted until the `RuntimeUpgrade` state machine explicitly reaches `Contracting` → `Complete`. No operator command outside the state machine can delete the old CRD while a `RuntimeUpgrade` record is active — the WarmPoolController blocks `SandboxTemplate` deletion when an active `RuntimeUpgrade` references it.

**Operational example:**

```bash
# 1. Register upgrade (Pending)
lenny-ctl admin pools upgrade start \
  --pool claude-worker-sandboxed-medium \
  --new-image registry.example.com/claude-worker@sha256:abc123 \
  --canary-percent 10

# 2. Monitor (Expanding — 10% of new sessions go to new pool)
lenny-ctl admin pools upgrade status --pool claude-worker-sandboxed-medium

# 3. Promote to full traffic after validation
lenny-ctl admin pools upgrade proceed

# 4. Drain old pool (Draining — old pool accepts no new sessions)
# ... wait for activePodCount == 0 or force-proceed after drainTimeoutSeconds ...

# 5. Remove old pool (Contracting → Complete)
lenny-ctl admin pools upgrade proceed

# OR: pause at any point
lenny-ctl admin pools upgrade pause --pool claude-worker-sandboxed-medium
# ... investigate ...
lenny-ctl admin pools upgrade resume --pool claude-worker-sandboxed-medium
```

**Rollback and safe rotation rules:**

- **Never delete the old `SandboxTemplate` CRD until the new pool is verified and the old pool is fully drained.** This is the key safety rule — enforced by the state machine.
- **The `RuntimeUpgrade` state machine is the required mechanism for production runtime upgrades.** The manual four-step procedure below is provided for reference and for environments where the CLI is unavailable, but the state machine provides pause, rollback, canary, and schema migration integration that the manual procedure lacks.
- **Manual rotation sequence (reference only):** (1) Deploy new pool, (2) verify new pool pods pass health checks, (3) route a canary percentage of new sessions to the new pool, (4) only after validation, set old pool's `minWarm` to 0, (5) only after old pool fully drains, delete old `SandboxTemplate` CRD.
- **Rollback (manual):** If the new pool version is broken, recreate the old `SandboxTemplate` CRD (same config, old image digest). Since pool rotation is additive (new pool created before old pool is drained), the old pool's config should be retained in version control or Helm values until the new pool is verified.

**Token Service:** Rolling Deployment update. Stateless — reads from Postgres/Redis, so no special migration needed for the service itself.

**KMS Key Rotation:** See [Section 4.9.1](04_system-components.md#491-kms-key-rotation-procedure) for the full KMS envelope key rotation procedure (steps, re-encryption job, Redis cache invalidation, frequency, and monitoring).

**Rollback:** All components support rollback by deploying the previous version. Schema migrations are always backward-compatible (expand-contract). Pool rotation is reversed by creating a new pool with the old image.

### 10.6 Environment Resource and RBAC Model

`Environment` is a first-class admin API resource. A named, RBAC-governed project context grouping runtimes and connectors for a team.

**Two access paths:**

- **Transparent filtering** (default) — user connects to standard endpoint; gateway computes the union of authorized runtimes across all environments where the user's groups have a role and returns that filtered view.
- **Explicit environment endpoint** (opt-in) — dedicated paths across all external interfaces: `/mcp/environments/{name}`, `/v1/environments/{name}/sessions`, scoped model namespace on `/v1/responses` and `/v1/chat/completions`.

**Runtime and connector selection is tag-based** using Kubernetes-style label expression syntax with `include`/`exclude` overrides:

```yaml
environment:
  name: security-team
  tenantId: acme-corp
  description: "Security engineering workspace"

  members:
    - identity:
        type: oidc-group
        value: security-engineers
      role: creator
    - identity:
        type: oidc-group
        value: security-leads
      role: admin

  runtimeSelector:
    matchLabels:
      team: security
    matchExpressions:
      - key: approved
        operator: In
        values: ["true"]
    types: [agent, mcp]
    include: [legacy-code-auditor]
    exclude: [unstable-scanner]

  mcpRuntimeFilters:
    - runtimeSelector:
        matchLabels:
          category: execution
      allowedCapabilities: [read, execute]
      deniedCapabilities: [write, delete, admin]

  connectorSelector:
    matchLabels:
      team: security
    allowedCapabilities: [read, search, network]
    deniedCapabilities: [write, delete, execute, admin]

  defaultDelegationPolicy: security-policy
  crossEnvironmentDelegation: false
```

**Member roles:** `viewer`, `creator`, `operator`, `admin`.

**`mcpRuntimeFilters`** — capability-based tool filtering for `type: mcp` runtimes. Capabilities inferred from MCP `ToolAnnotations` (see [Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)). Name collisions resolved by `runtime:tool` qualified reference in `overrides`.

**`connectorSelector`** — internal only. Controls what connectors agents can use via the platform MCP server. External clients do not call connectors through environment policy.

**Cross-environment delegation** — structured bilateral declaration model:

```yaml
crossEnvironmentDelegation:
  outbound:
    - targetEnvironment: platform-services
      runtimes:
        matchLabels:
          shared: "true"
  inbound:
    - sourceEnvironment: "*"
      runtimes:
        matchLabels:
          shared: "true"
```

Effective cross-environment access requires both sides to permit it. Neither side can unilaterally grant the other access.

**Effective delegation scope:** `(environment definition ∪ cross-environment permitted runtimes) ∩ delegation policy`

**Gateway enforcement at delegation time:**

1. Resolve target runtime to its home environment
2. Verify calling environment has an outbound declaration permitting target → `target_not_in_scope` if absent
3. Verify target environment has an inbound declaration permitting calling environment → `target_not_in_scope` if absent
3.5. **Isolation monotonicity check:** Verify the target pool's isolation profile is at least as restrictive as the calling session's `minIsolationProfile` (see [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) isolation monotonicity). Cross-environment delegation does not relax the monotonicity invariant — a `sandboxed` session in environment A cannot cross-environment-delegate to a `standard` pool in environment B. Violation is rejected with `ISOLATION_MONOTONICITY_VIOLATED` and a `delegation.isolation_violation` audit event is emitted ([Section 11.7](11_policy-and-controls.md#117-audit-logging)) with `cross_environment: true`.
4. Apply DelegationPolicy as normal → `target_not_authorized` if policy doesn't permit

**Cross-environment check evaluation semantics.** Steps 2 and 3 (outbound and inbound bilateral declaration checks) are **always live** — re-evaluated against the current `Environment` resource definitions at each `delegate_task` call, including grandchild and deeper delegations. If an administrator modifies or removes an outbound or inbound declaration while a delegation tree is in flight, subsequent `delegate_task` calls in that tree will be evaluated against the updated declarations; an in-flight grandchild that was already delegated is not retroactively revoked, but any further delegation attempted by that grandchild will reflect the current declarations. The `snapshotPolicyAtLease` flag ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) applies only to `DelegationPolicy` pool-label matching (step 4) and does not affect the bilateral declaration checks in steps 2 and 3.

**Connectors are never cross-environment.** Child sessions use their own environment's connector configuration.

**`noEnvironmentPolicy`:** `deny-all` or `allow-all`. **The platform default is `deny-all`.** Omission of `noEnvironmentPolicy` is handled differently at the two scopes — the two branches are not symmetric:

- **Platform-level omission** (Helm: `global.noEnvironmentPolicy` unset) is a **fatal startup error**. The gateway refuses to become Ready and emits `LENNY_CONFIG_MISSING{config_key=noEnvironmentPolicy, scope=platform}` at `FATAL` level — see the startup-configuration validation table in [Section 10.3](#103-mtls-pki). The platform default (`deny-all`) reaches the gateway only as an explicit Helm setting; the gateway never infers it at runtime so that a misconfigured chart (with the default stripped) fails closed at startup rather than silently running with undefined semantics.
- **Tenant-level omission** (admin API `PUT /v1/admin/tenants/{id}/rbac-config` without `noEnvironmentPolicy`) MUST be treated as `deny-all` by the gateway. This preserves backward-compatible tenant creation — admin API callers need not specify the field to receive the safe default.

Configurable per tenant via the admin API; overridable platform-wide at Helm install time via `global.noEnvironmentPolicy` ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)). No other values are valid; the gateway rejects unrecognised values at tenant RBAC config validation time.

**Audit interceptor (`lenny-noenvironmentpolicy-audit`):** A gateway-internal interceptor on `PUT /v1/admin/tenants/{id}/rbac-config` emits a non-blocking **audit warning** (HTTP 200 with `Warning:` response header per RFC 9110 §11.5) whenever `noEnvironmentPolicy` is explicitly set to `allow-all`. The warning text is: `"noEnvironmentPolicy: allow-all grants unrestricted runtime access to all authenticated users with no environment membership in this tenant. Verify this matches the intended security posture."` The interceptor does not block the request — it is an advisory control, not an enforcement control. A `lenny_noenvironmentpolicy_allowall_total` counter (labeled by `tenant_id`) is incremented on each such write, enabling operators to audit which tenants have opted into `allow-all` and when.

**`noEnvironmentPolicy` semantics:**

- **`deny-all`** (platform default): An authenticated user who is not a member of any environment cannot access any runtime. All runtime access requires explicit environment membership.
- **`allow-all`**: An authenticated user who is not a member of any environment can access any runtime **owned by their own tenant** with no capability restrictions. Specifically:
  - "All" means all runtimes tagged to the user's tenant (`tenantId` matches the authenticated user's tenant). Runtimes owned by other tenants are never exposed regardless of this setting. Cross-tenant runtime access is architecturally impossible — the gateway resolves tenant identity before evaluating `noEnvironmentPolicy`.
  - `allow-all` does **not** bypass environment-level RBAC for users who **are** members of an environment. It only governs the fallback behavior for users with no environment membership.
  - `allow-all` applies at the tenant level, not at individual environments. There is no inheritance model — environments define their own membership and selectors independently.
  - **Security warning:** Do not set `noEnvironmentPolicy: allow-all` as the platform-wide Helm default in multi-tenant deployments. In that configuration, every authenticated user in every tenant would have unrestricted access to all runtimes within their tenant, bypassing the environment-based access control model. Restrict `allow-all` to individual tenants that have reviewed the access implications and confirmed this matches their intended security posture.

**Identity:** OIDC. Groups from LDAP/AD carried as JWT claims. `introspectionEnabled: true` adds real-time group checks at latency cost.

**Environment-level billing rollup (v1):** `environmentId` populated on all billing events for sessions created in an environment context.

**Tenant RBAC Config:** Managed via `PUT /v1/admin/tenants/{id}/rbac-config`. Includes `noEnvironmentPolicy`, `identityProvider` (OIDC), `tokenPolicy`, `capabilities` taxonomy (platform defaults + tenant custom), `mcpAnnotationMapping` overrides.

**V1 data model accommodations:**

- `Runtime` resources need `labels` map from v1
- `Connector` registrations need `labels` map from v1
- `type:mcp` runtime tool schemas cached by gateway from v1
- `GET /v1/runtimes` and `list_runtimes` accept optional `?environmentId=` stub
- Session creation accepts optional `environmentId` parameter as no-op stub
- `environmentId` as nullable field on billing event schema from Phase 1
- `crossEnvironmentDelegation` structured form schema from Phase 1

### 10.7 Experiment Primitives

Lenny provides built-in A/B traffic routing for runtime version rollouts. The platform handles **session routing and variant pool management** — it decides which pool a session lands in, not how to analyze the results. Deployers who already use a feature-flagging or experimentation platform (LaunchDarkly, Statsig, Unleash, Flagd, GO Feature Flag, ConfigCat) can delegate variant assignment through the OpenFeature Go SDK — either via [OFREP](https://openfeature.dev/specification/appendix-c/) (recommended) or a built-in provider adapter — instead of using Lenny's built-in bucketing. See "Tenant-level experiment targeting configuration" below.

**Experiment context delivery to runtimes.** When a session is enrolled in an experiment, its `experimentContext` (containing `experimentId`, `variantId`, and `inherited` flag) is delivered to the runtime in the adapter manifest ([Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification)). Runtimes can use this context to tag their traces with variant metadata (e.g., as metadata on LangSmith runs or Braintrust logs) for filtering and grouping in their eval platform. Note that eval platforms do not provide native A/B comparison features — variant comparison works via metadata filtering in those platforms' UIs. For statistical rigor (significance testing, confidence intervals), use a dedicated experimentation platform — see "Full A/B testing with external platforms" below.

`ExperimentDefinition` as first-class admin API resource. `ExperimentRouter` as built-in `RequestInterceptor` (see [Section 4.8](04_system-components.md#48-gateway-policy-engine)).

```yaml
experiments:
  - id: claude-v2-rollout
    status: active # active | paused | concluded
    baseRuntime: claude-worker
    variants:
      - id: treatment
        runtime: claude-worker-v2
        pool: claude-worker-v2-sandboxed-medium
        weight: 0.10 # fraction (0.0–1.0); 0.10 = 10% of traffic
        initialMinWarm: 5 # optional; static minWarm override during bootstrap (see below)
    targeting:
      mode: percentage # percentage | external
      sticky: user # user | session | none
    propagation:
      childSessions: inherit # inherit | control | independent
```

**Control group identifier.** Sessions not assigned to any named variant run the `baseRuntime` and are assigned `variant_id: "control"` automatically by the gateway. `"control"` is a reserved variant identifier — deployers cannot define a variant with `id: "control"` in the `variants` list. This constraint is enforced at experiment creation and update time via `POST /v1/admin/experiments` and `PUT /v1/admin/experiments/{name}` validation; attempts to use a reserved identifier return `422` with error code `RESERVED_IDENTIFIER`. The control group's warm pool capacity is not managed by experiment machinery — it falls through to the base runtime's existing pool.

**Variant `initialMinWarm` — cold-start guidance.** The PoolScalingController derives `minWarm` for a variant pool from the standard formula (`base_demand_p95 × variant_weight × safety_factor × …`, see [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)). At experiment creation time, no demand history exists for the variant pool: `base_demand_p95` and `burst_p99_claims` are zero, so the formula yields `minWarm = 0`. This means the first real sessions routed to the variant find an empty pool and incur the full pod startup latency (cold-start penalty).

The optional `initialMinWarm` field on each variant entry provides a deployer-supplied static floor that the PoolScalingController uses **during bootstrap mode only** — from the moment the variant pool is first created until the pool exits bootstrap mode (convergence criteria per [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) and 17.8.2). Once bootstrap mode exits, the controller discards `initialMinWarm` and uses the formula-derived value exclusively. `initialMinWarm` has no effect on already-bootstrapped pools or on `paused → active` re-activations (the formula value is used on re-activation).

**Sizing guidance for `initialMinWarm`:** A conservative starting point is `ceil(expected_peak_rps × weight_fraction × (failover_seconds + pod_startup_seconds))` where `weight_fraction` is the variant's `weight` value (already a fraction in [0.0, 1.0]) and `expected_peak_rps` is the deployer's estimate of base pool peak arrival rate. Example: weight 0.10 (10%), estimated 20 req/s peak, 25s failover + 10s startup → `ceil(20 × 0.10 × 35) = 70`. For low-weight ramp experiments (≤ 5%), a flat value of 3–5 warm pods is typically sufficient to absorb the initial traffic burst without significant cold-start exposure. Setting `initialMinWarm` higher than necessary wastes resources during bootstrap; setting it to 0 (or omitting it) risks cold-start latency spikes on the variant's first sessions. If omitted, `initialMinWarm` defaults to `0` — the controller produces a zero-warm pool at creation, consistent with the no-history formula result.

**Targeting modes:** `percentage` (deterministic hash) and `external` (delegates variant assignment to an OpenFeature provider — OFREP or a built-in SDK adapter).

**Targeting schema.** The `targeting` block in the `ExperimentDefinition` varies by mode. Each mode's additional fields are defined below:

```yaml
# Mode: percentage (default)
targeting:
  mode: percentage
  sticky: user               # user | session | none
  # No additional fields. Assignment uses deterministic hash with
  # cumulative weight partitioning. See "Bucketing algorithm" below.
  # Null/anonymous user_id: see "Anonymous session handling" below.

# Mode: external (OpenFeature provider — OFREP or built-in SDK adapter)
targeting:
  mode: external
  sticky: user               # user | session | none
  # Assignment is delegated to the tenant's experimentTargeting OpenFeature
  # provider (configured at tenant scope — see "Tenant-level experiment targeting
  # configuration" below). The gateway calls the provider's evaluation API once
  # per session creation with user context; the provider returns a variant
  # assignment for this experiment.
```

**Bucketing algorithm (percentage mode).** The `ExperimentRouter` uses the following deterministic cumulative-weight partitioning algorithm to assign a session to a variant (or control) when `targeting.mode: percentage`:

```
// Inputs:
//   assignment_key — string: user_id (sticky: user), session_id (sticky: session),
//                    or a fresh random UUIDv4 (sticky: none, generated per request)
//   experiment_id  — string: the experiment's unique identifier
//   variants       — ordered list of { id: string, weight: float64 } from the ExperimentDefinition
//                    (each weight is a fraction in [0.0, 1.0]; Σ weights < 1.0 — remainder is control)
//
// Returns: variant_id string ("control" if no variant matched)

func assignVariant(assignment_key, experiment_id string, variants []Variant) string {
    // 1. Derive a stable bucket value in [0.0, 1.0).
    //    HMAC-SHA256 provides uniform distribution and prevents crafted keys
    //    from gaming the assignment boundary.
    raw := HMAC_SHA256(key=experiment_id, message=assignment_key)
    bucket := float64(binary.BigEndian.Uint64(raw[:8])) / (1 << 64)  // [0.0, 1.0)

    // 2. Walk variants in definition order, accumulating cumulative weight.
    //    The first variant whose cumulative upper boundary exceeds the bucket wins.
    cumulative := 0.0
    for _, v := range variants {
        cumulative += v.weight
        if bucket < cumulative {
            return v.id
        }
    }

    // 3. Bucket falls in the remainder (> Σ variant_weights) → control group.
    return "control"
}
```

**Properties of this algorithm:**

- **Determinism:** identical `(assignment_key, experiment_id)` always yields the same variant, making `sticky: user` and `sticky: session` semantically correct without a persistent lookup on every session creation (the cache is a performance optimisation, not a correctness requirement).
- **Ordering sensitivity:** variants are evaluated in definition order. Deployers who add a new variant to an experiment mid-flight will shift bucket boundaries for all variants that follow the insertion point, re-assigning some users. To avoid mid-experiment re-assignment, append new variants at the end of the `variants` list.
- **Independence across experiments:** the `experiment_id` is part of the HMAC key, so the same user does not land in the same relative bucket across different experiments. Assignment correlation between experiments is negligible.
- **Multi-experiment evaluation order:** When multiple active experiments are defined for a tenant, the `ExperimentRouter` evaluates them in ascending order of `created_at` (experiment creation timestamp). For each experiment in that order, `assignVariant` is called independently. The router stops at the **first experiment** where the result is a non-control variant and uses that experiment's variant pool. Experiments where the session hashes to control are skipped and the router continues to the next. If all active experiments produce a control assignment, the session runs the base runtime with no experiment context. This first-match rule ensures a session is enrolled in at most one experiment at a time, keeping the single `experimentContext` field unambiguous and preventing pool-routing conflicts.
- **Two-variant example:** one variant with `weight: 0.10` — bucket in [0.0, 0.10) → treatment; [0.10, 1.0) → control. Equivalent to the prior `hash mod 100 < 10` description.
- **Three-variant example:** variants A (weight 0.10), B (weight 0.20), C (weight 0.15) — bucket in [0.0, 0.10) → A; [0.10, 0.30) → B; [0.30, 0.45) → C; [0.45, 1.0) → control (55%).
- **Anonymous sessions:** when `assignment_key` cannot be derived (null `user_id` under `sticky: user`), the algorithm is not invoked — the session is assigned `"control"` unconditionally. See "Anonymous session handling" below.

**Tenant-level experiment targeting configuration.** The `experimentTargeting` block on the tenant configuration defines how `mode: external` experiment assignment is resolved. Lenny uses the [OpenFeature](https://openfeature.dev/) Go SDK as the integration point: every external-targeting provider is an OpenFeature provider, invoked once per session creation for tenants that have any active experiments with `mode: external`. Two integration paths are supported and share the same configuration surface:

- **OFREP (Remote Evaluation Protocol) — recommended.** The OpenFeature working group's [Remote Evaluation Protocol](https://openfeature.dev/specification/appendix-c/) is a standard REST evaluation API. Any flag service that exposes OFREP is usable without a provider-specific adapter. Flagd, GO Feature Flag, ConfigCat, and LaunchDarkly (via its Relay Proxy) all expose OFREP.
- **OpenFeature SDK providers.** For services that do not yet expose OFREP, the gateway ships built-in OpenFeature SDK providers for LaunchDarkly, Statsig, and Unleash, linked into the gateway binary.

Percentage-mode bucketing is unaffected by this configuration — it is Lenny's built-in HMAC-SHA256 algorithm and has no external dependency. Percentage-mode experiments on the same tenant continue to work regardless of whether `experimentTargeting` is configured.

```yaml
# Tenant-level configuration — OFREP (recommended)
experimentTargeting:
  provider: ofrep
  timeoutMs: 200                     # session-creation hot path timeout
  ofrep:
    endpoint: https://flags.internal/ofrep    # OFREP-compliant evaluation endpoint
    headers:                                  # optional static headers
      Authorization: "Bearer ${OFREP_TOKEN}"
```

Built-in OpenFeature SDK provider examples:

```yaml
# LaunchDarkly (via built-in OpenFeature SDK provider)
experimentTargeting:
  provider: launchdarkly
  timeoutMs: 200
  launchdarkly:
    sdkKey: "${LD_SDK_KEY}"
    baseUrl: https://app.launchdarkly.com   # optional, for private instances

# Statsig (via built-in OpenFeature SDK provider)
experimentTargeting:
  provider: statsig
  timeoutMs: 200
  statsig:
    serverSecret: "${STATSIG_SERVER_SECRET}"

# Unleash (via built-in OpenFeature SDK provider)
experimentTargeting:
  provider: unleash
  timeoutMs: 200
  unleash:
    apiUrl: https://unleash.internal/api
    apiToken: "${UNLEASH_API_TOKEN}"
```

**Evaluation semantics.** For each active `mode: external` experiment the gateway calls `client.ObjectValue(ctx, experimentId, defaultVariant, evaluationContext)` on the configured OpenFeature client, where `evaluationContext` carries the session's `user_id`, `tenant_id`, and session metadata (`runtime`, labels). The variant ID is resolved from the provider's response as follows:

- **OFREP providers:** OFREP's evaluation response carries a top-level `variant` string field per the [OFREP specification](https://openfeature.dev/specification/appendix-c/). Lenny reads `variant` directly.
- **OpenFeature SDK providers (LaunchDarkly, Statsig, Unleash, custom):** the SDK returns an `EvaluationDetails` with `Variant` (string, optional) and `Value` (the flag value). Lenny reads `Variant` if present; otherwise it falls back to `Value` if `Value` is a string, or to `Value["variant_id"]` if `Value` is an object containing that key. A `Value` shape that matches none of these yields `"control"` with an `experiment.unknown_variant_from_provider` warning event.

In both cases the gateway matches the resolved variant ID against the variant list on the `ExperimentDefinition`. Unknown variant IDs are treated as `"control"` with the same warning event. The provider's own experiment/flag catalog is authoritative for which experiments a user is enrolled in; Lenny's list of `mode: external` experiments is the set of experiments the platform knows how to route and pool-size for. Experiments present in Lenny but not returned by the provider are skipped (the session runs control for that experiment); experiments returned by the provider but not registered in Lenny are logged with an `experiment.unknown_external_id` info event and otherwise ignored.

The `sticky` field on the experiment definition governs caching: `sticky: user` means the evaluation result is cached per `user_id` across sessions (same as percentage mode). The OpenFeature client is not called again for subsequent sessions if a cached assignment exists.

**OpenFeature evaluation failure behavior.** On OpenFeature client timeout or error (whether caused by an OFREP call failure, SDK-provider transport error, or provider-returned `ErrorResolutionDetails`): no experiment assignment is made for any `mode: external` experiment. The session proceeds with the base runtime, no experiment tracking, no eval attribution for external experiments. `mode: percentage` experiments on the same tenant are unaffected — they are evaluated independently by the gateway's built-in hash. The `lenny_experiment_targeting_error_total` metric is incremented (labeled by `provider`, `error_type`), and an `experiment.targeting_failed` warning event is emitted (fields: `tenant_id`, `user_id`, `provider`, `error`). No `fallbackOnError` configuration — on failure, exclusion is the only valid behavior since the gateway has no assignment information.

**Targeting circuit breaker (SCL-023).** The OpenFeature evaluation is on the session creation hot path with a 200ms timeout. Under sustained failures, repeated 200ms waits at each session creation (even after fast-fail) reduce throughput significantly. A per-tenant circuit breaker prevents this cascade:

- **Open condition:** 5 consecutive failures (timeout or `ErrorResolutionDetails`) within any 10-second window opens the circuit for that tenant.
- **Open behavior:** While open, the gateway skips the OpenFeature call entirely and returns empty assignment — same as a provider error, but with zero wait. The `lenny_experiment_targeting_circuit_open` gauge (labeled `tenant_id`, `provider`) is set to `1`.
- **Half-open / recovery:** After 30 seconds in open state, the circuit transitions to half-open and allows one probe request. If the probe succeeds, the circuit closes; if it fails, the 30s open window resets.
- **Configuration fields** (on the `experimentTargeting` block alongside `timeoutMs`):
  - `circuitBreaker.failureThreshold` (int, default `5`) — consecutive failures required to open.
  - `circuitBreaker.windowSeconds` (int, default `10`) — rolling window for failure counting.
  - `circuitBreaker.openDurationSeconds` (int, default `30`) — how long to stay open before half-open probe.
- **Alert:** `ExperimentTargetingCircuitOpen` (Warning) fires when `lenny_experiment_targeting_circuit_open > 0` for more than 60 seconds, indicating a sustained targeting service outage affecting experiment assignment.

**Anonymous session handling under external targeting.** OpenFeature evaluations receive an `evaluationContext` with `user_id` set to the anonymous pseudo-ID `"anon:<session_id>"` (derived deterministically from the session ID). Providers that support anonymous targeting (via `device_id`, `session_id`, or similar) can use this value. Percentage-mode anonymous handling (below) is unchanged.

**Anonymous session handling (null `user_id`).** For sessions with `user_id: null` (anonymous sessions), percentage-mode experiments always route to the **control variant** — no variant assignment is made. `sticky: user` caching is not applied (no cache key can be derived from a null `user_id`). Anonymous sessions are excluded from variant pools entirely; this prevents the hash-collision problem that would arise if `hash(null + experiment_id)` returned a constant bucket and concentrated all anonymous traffic into a single variant. Deployers who need to include anonymous sessions in experiments should use `mode: external` with a flag service that handles anonymous assignment (e.g., using device ID or session ID as the assignment key).

**Multi-experiment assignment rule (first-match).** When a tenant has multiple active experiments, the `ExperimentRouter` evaluates them in ascending `created_at` order. For each experiment, it runs `assignVariant` independently. The first experiment that returns a non-control variant wins: the session is enrolled in that experiment, its `experimentContext` is set, and evaluation stops. Experiments that return `"control"` for this session are skipped. If all active experiments produce a control result, the session proceeds with no experiment context. This rule guarantees that `experimentContext` is always singular and unambiguous — a session can be enrolled in at most one experiment at a time. Deployers who need two independent tests to run concurrently without mutual exclusion should run them on different base runtimes, ensuring the two experiments' evaluation paths are disjoint. An `experiment.multi_eligible_skipped` informational event is emitted (fields: `enrolled_experiment_id`, `skipped_experiment_ids[]`) whenever one or more experiments are skipped due to this rule, enabling deployers to audit enrollment overlap.

**ExperimentRouter isolation monotonicity check.** Before routing a session to a variant pool, the gateway verifies that the variant pool's isolation profile satisfies the session's `minIsolationProfile` (the same check as delegation isolation monotonicity, [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)). If the variant pool's isolation is weaker than the session's minimum, the router **fails closed**: the session creation is rejected with `VARIANT_ISOLATION_UNAVAILABLE` (HTTP 422, see [Section 15.1](15_external-api-surface.md#151-rest-api) error catalog) and an `experiment.isolation_mismatch` warning event is emitted (fields: `experiment_id`, `variant_id`, `sessionMinIsolation`, `variantPoolIsolation`). Silent fallthrough to the base runtime is explicitly **not** permitted — routing an eligible-for-treatment session into the control bucket would contaminate control-group eval aggregates with a non-randomly-sampled subset (the isolation-incompatible population), breaking the statistical independence the experiment relies on. Callers whose session requires a stricter isolation profile than the active experiment's variant pools offer must either relax the session's `minIsolationProfile`, or the operator must re-provision the variant pool at a compatible isolation profile before the experiment will accept that traffic class.

**Propagation modes for `childSessions`:** Controls how experiment assignment flows through delegation (see [Section 8](08_recursive-delegation.md) for delegation mechanics):

| Mode          | Behavior                                                                                                                                                            |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `inherit`     | Child session receives the parent's experiment context verbatim. The child runs the same variant and its eval results attribute to the same experiment.             |
| `control`     | Child session is forced into the base runtime (control group) regardless of the parent's variant. Eval results still attribute to the parent's experiment.          |
| `independent` | Child session is evaluated for experiment eligibility independently — it may land in a different experiment or no experiment. No context is copied from the parent. |

**Cross-experiment conflict resolution (innermost wins).** When a child session is eligible for multiple experiments (e.g., parent propagates experiment A via `inherit` while the child independently qualifies for experiment B), the **innermost assignment wins**: the child's own independent assignment takes precedence over any inherited context. This can only occur when `propagation.childSessions` is `independent`; under `inherit` or `control` the parent's experiment context is authoritative and no independent evaluation occurs.

**Eval result attribution.** Eval results submitted against a child session are attributed to the child's effective experiment context (the `experiment_id` and `variant_id` on the child's session record). Under `inherit` and `control` modes this is the root experiment. Under `independent` mode it is whatever experiment the child was independently assigned to (or `null` if none). The Results API (below) aggregates scores per experiment; delegation depth is not a default grouping dimension. Each `EvalResult` record includes a `delegation_depth` field (uint32, 0 for root sessions) and an `inherited` boolean (mirroring the session's `experimentContext.inherited` flag). These fields enable operators to distinguish direct eval results (depth 0, `inherited: false`) from propagated child results (depth > 0, `inherited: true`) when querying the Results API.

**Sample contamination warning for `control` propagation mode.** Under the `control` propagation mode, child sessions run the base runtime but attribute eval results to the parent's experiment (variant `"control"`). This creates a sample contamination risk: the control group's eval aggregates may include results from children whose parent was in the treatment group. Because treatment-variant parents may generate systematically different delegation tasks than control-group parents, the child eval scores are not independently sampled and carry selection bias from the parent's variant assignment. Operators analyzing experiment results with `control`-mode delegation should use the Results API filter query parameters (`?delegation_depth=0` or `?inherited=false`) documented below under "Results API response" to obtain uncontaminated per-variant aggregates, or use `?breakdown_by=delegation_depth` to analyze the effect at each level separately.

**Experiment context propagates through delegation leases:**

```json
{
  "experimentContext": {
    "experimentId": "claude-v2-rollout",
    "variantId": "treatment",
    "inherited": true
  }
}
```

The `inherited` field is `true` when the context was propagated from a parent (`inherit` or `control` mode) and `false` when the child was assigned independently.

**Evaluation: two-tier model.** Evaluation is a standalone capability, independent of experimentation. Any session can be evaluated whether or not it is part of an experiment. Lenny supports two complementary approaches:

1. **Runtime-native tracing and scoring (primary).** Most production deployments will use runtime-native eval platforms (LangSmith, Braintrust, Humanloop, etc.) for detailed, trace-level scoring, observability, and prompt iteration. Lenny supports this by propagating `tracingContext` through delegation so child runtimes can stitch their traces into the parent's trace tree (see [Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease) and 16.3). When experiments are active, `experimentContext` is also delivered in the adapter manifest — runtimes can use it to tag traces with variant metadata for filtering and grouping in their eval platform.

2. **Built-in eval endpoint (basic alternative).** For deployers without dedicated eval tooling, Lenny provides a lightweight score ingestion endpoint (`POST /v1/sessions/{id}/eval`) and a results aggregation API (`GET /v1/admin/experiments/{name}/results`). This is a basic scoring mechanism — it stores scores and provides aggregation. When a session is enrolled in an experiment, the gateway auto-populates experiment attribution on eval results. It does not compete with the depth of runtime-native eval platforms.

**Eval result schema.** Scores submitted via `POST /v1/sessions/{id}/eval` are stored as `EvalResult` records in Postgres:

| Field           | Type     | Description                                                                                                                                                                                                                                    |
| --------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`            | uuid     | Auto-generated primary key                                                                                                                                                                                                                     |
| `tenant_id`     | string   | Tenant that owns the session (non-null, indexed). Required for RLS — matches the tenant-scoping classification in [Section 4.2](04_system-components.md#42-session-manager). The `lenny_tenant_guard` trigger validates this field on every INSERT.                                           |
| `session_id`    | uuid     | Session the score pertains to (foreign key)                                                                                                                                                                                                    |
| `experiment_id` | TEXT     | Auto-populated by gateway from session's experiment context                                                                                                                                                                                    |
| `variant_id`    | string   | Auto-populated by gateway from session's experiment context                                                                                                                                                                                    |
| `scorer`        | string   | Identifier for the scoring method (e.g., `llm-judge`, `exact-match`)                                                                                                                                                                           |
| `score`         | float64  | Normalized score value (0.0–1.0)                                                                                                                                                                                                               |
| `scores`        | jsonb    | Optional. Multi-dimensional scores as key-value pairs (e.g., `{"coherence": 0.9, "relevance": 0.7, "safety": 1.0}`). When present, `score` should be the aggregate/summary score. Keys are scorer-defined dimension names; values are float64. |
| `metadata`      | jsonb    | Arbitrary key-value pairs (model version, prompt hash, etc.)                                                                                                                                                                                   |
| `delegation_depth` | uint32 | Delegation depth of the session that produced this eval (0 for root sessions). Auto-populated by the gateway from the session's delegation lineage.                                                                                         |
| `inherited`     | boolean  | `true` when the session's experiment context was propagated from a parent (`inherit` or `control` mode), `false` when independently assigned. Mirrors `experimentContext.inherited`. `null` when no experiment context.                          |
| `submitted_after_conclusion` | boolean | `true` when the eval was submitted after the experiment transitioned to `concluded` status. `false` otherwise. Enables operators to filter post-conclusion submissions in analysis.                                               |
| `created_at`    | RFC 3339 | Server-generated UTC timestamp                                                                                                                                                                                                                 |

**Gateway auto-association.** When the gateway receives an eval submission, it looks up the session's `experimentContext` (populated at assignment time per the experiment context block above). If the session is enrolled in an active experiment, the gateway sets `experiment_id` and `variant_id` on the `EvalResult` automatically. If the session has no experiment context, those fields are `null` and the result is still stored for non-experiment use cases.

**Eval submission request body (`POST /v1/sessions/{id}/eval`):**

```json
{
  "scorer": "llm-judge",
  "score": 0.82,
  "scores": {
    "coherence": 0.9,
    "relevance": 0.78,
    "safety": 1.0
  },
  "metadata": {
    "judge_model": "gpt-4o",
    "rubric_version": "v3"
  }
}
```

Both `score` and `scores` are optional individually, but at least one must be provided. When both are present, `score` is the aggregate and `scores` contains the per-dimension breakdown. When only `scores` is provided, the gateway does not auto-compute `score` — the caller is responsible for providing the aggregate if needed.

**Eval Submission Contract.** The following rules govern all calls to `POST /v1/sessions/{id}/eval`:

| Dimension | Specification |
| --------- | ------------- |
| **Caller** | Any authenticated principal with the `session:eval:write` permission for the tenant. This includes: the agent runtime itself (via its session credential), the session owner (user or service account), or a deployer-operated external scorer pipeline. The gateway does not restrict eval submissions to a specific caller role — eval is intentionally open to post-hoc scoring from external pipelines. |
| **Accepted session states** | `active`, `completed`, `failed`. Eval submissions against `cancelled` or `expired` sessions are rejected with `422 UNPROCESSABLE_ENTITY` and error code `SESSION_NOT_EVAL_ELIGIBLE`. Submissions against `concluded` experiments' sessions are accepted (the session state, not the experiment state, governs eligibility). The eval is stored with the session's original `experiment_id` and `variant_id` regardless of the experiment's current status — the session's `experimentContext` (populated at assignment time) is the source of truth for attribution. The experiment's `concluded` status prevents new sessions from being enrolled but does not discard attribution for already-enrolled sessions. The Results API includes a `submitted_after_conclusion` boolean on each `EvalResult` record (set to `true` when the eval was submitted after the experiment transitioned to `concluded`), allowing operators to filter post-conclusion submissions in analysis without losing the attribution data. |
| **Rate limit** | Configurable per-session and per-tenant rate limits, enforced by the gateway via Redis sliding-window counters. Per-session limit is keyed by `session_id` (default: 100 eval submissions per minute, configurable via tenant config `evalRateLimit.perSessionPerMinute`). Per-tenant limit applies across all sessions (default: 10,000 eval submissions per minute, configurable via tenant config `evalRateLimit.perTenantPerMinute`). Excess requests receive `429 Too Many Requests` with a `Retry-After` header. |
| **Idempotency** | Callers may supply an optional `idempotency_key` string field in the request body (max 128 bytes). If a submission with the same `idempotency_key` and `session_id` was successfully stored within the last 24 hours, the gateway returns `200 OK` with the original `EvalResult` record without inserting a duplicate. Submissions without an `idempotency_key` are always inserted as new records. The idempotency window is 24 hours (TTL on the Redis dedup key, keyed by `session_id + idempotency_key`). |
| **Storage bounds** | Maximum 10,000 `EvalResult` records per session. On breach, the gateway returns `429` with error code `EVAL_QUOTA_EXCEEDED`. The per-session cap is configurable via tenant config (`maxEvalsPerSession`, default: 10,000, max: 100,000). There is no global per-experiment storage cap — storage is bounded indirectly by the per-session cap and the number of enrolled sessions. |
| **Trigger model** | Pull-only. The built-in eval endpoint accepts scores but does not push eval requests to agents or external systems. Callers are responsible for invoking `POST /v1/sessions/{id}/eval` at the appropriate time. For most deployments, runtime-native eval platforms (LangSmith, Braintrust, etc.) handle scoring and tracing directly — see the two-tier evaluation model above. The built-in endpoint is a basic alternative for deployers without dedicated eval tooling. |

**Results API response (`GET /v1/admin/experiments/{name}/results`).** Returns aggregated eval scores for a single experiment, grouped by variant. This endpoint is **not paginated** — the response is a single aggregated object (not a list of items), so the standard cursor-based pagination envelope ([Section 15.1](15_external-api-surface.md#151-rest-api)) does not apply. The number of variants per experiment is bounded by operator configuration (typically 2–5) and the aggregation is pre-computed, so the response size is inherently bounded.

**Results API query parameters (EXP-002).** The endpoint accepts the following optional query parameters so operators can reach the uncontaminated per-variant aggregates discussed under "Sample contamination warning" above:

| Parameter | Type | Description |
| --------- | ---- | ----------- |
| `delegation_depth` | `uint32` | Filter to `EvalResult` records whose `delegation_depth` equals the supplied value. Pass `0` to obtain direct-session results only (no delegation descendants). |
| `inherited` | `bool` | Filter to records whose `inherited` flag matches the supplied value. `inherited=false` obtains independently-assigned or root-session results. |
| `exclude_post_conclusion` | `bool` | When `true`, filters out records where `submitted_after_conclusion == true`, restricting aggregation to evals submitted while the experiment was still `active` or `paused`. |
| `breakdown_by` | `delegation_depth \| inherited \| submitted_after_conclusion` | Optional. When supplied, each variant bucket is additionally split by the named field; the response emits one sub-aggregate per unique value (e.g., `delegation_depth=0`, `delegation_depth=1`, `delegation_depth=2`). Not combinable with the equality filter for the same field (i.e., `?delegation_depth=0&breakdown_by=delegation_depth` is rejected with `400 INVALID_QUERY_PARAMS`). Response shape: see `BreakdownResponse` schema immediately below the default response example. |

When any of `delegation_depth`, `inherited`, `exclude_post_conclusion`, or `breakdown_by` is present, aggregation is recomputed on the filtered subset directly from the `eval_results` base table. The `lenny_eval_aggregates` materialized view is bypassed for filtered or broken-down requests because the materialized view pre-aggregates across all rows; it is used only when the request carries no filter or breakdown parameters. Filtered queries have higher latency than the materialized-view path — typical P95 < 2s for 1M eval rows at Tier 3, versus < 200ms for the default unfiltered path. Operators running frequent filtered analyses should submit them during off-peak windows or export the underlying `eval_results` to an external analytics store.

```json
{
  "experiment_id": "claude-v2-rollout",
  "status": "active",
  "variants": [
    {
      "variant_id": "control",
      "sample_count": 412,
      "scorers": {
        "llm-judge": {
          "mean": 0.74,
          "p50": 0.76,
          "p95": 0.91,
          "count": 412,
          "dimensions": {
            "coherence": {
              "mean": 0.8,
              "p50": 0.82,
              "p95": 0.95,
              "count": 412
            },
            "relevance": {
              "mean": 0.71,
              "p50": 0.73,
              "p95": 0.89,
              "count": 412
            },
            "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 412 }
          }
        },
        "exact-match": { "mean": 0.68, "p50": 0.7, "p95": 0.88, "count": 390 }
      }
    },
    {
      "variant_id": "treatment",
      "sample_count": 45,
      "scorers": {
        "llm-judge": {
          "mean": 0.81,
          "p50": 0.83,
          "p95": 0.94,
          "count": 45,
          "dimensions": {
            "coherence": { "mean": 0.88, "p50": 0.9, "p95": 0.97, "count": 45 },
            "relevance": { "mean": 0.78, "p50": 0.8, "p95": 0.92, "count": 45 },
            "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 45 }
          }
        },
        "exact-match": { "mean": 0.72, "p50": 0.74, "p95": 0.9, "count": 42 }
      }
    }
  ]
}
```

**Broken-down response shape (`BreakdownResponse`).** When the request carries `breakdown_by=<field>`, the response replaces each variant's flat `scorers` object with a `breakdowns` array. Each element of `breakdowns` is a self-contained sub-aggregate over the `EvalResult` rows that share a single value of the breakdown field; the sub-aggregate's `scorers` and `dimensions` structures are identical to those in the default response. Top-level fields (`experiment_id`, `status`, `variants[].variant_id`) are unchanged. The `breakdown_by` field on each variant echoes the query parameter so consumers can confirm the dimension being split. Variant-level `sample_count` equals the sum of `sample_count` across that variant's `breakdowns` entries. Buckets with zero eligible rows are omitted; `breakdowns` is always present (possibly empty `[]`) whenever `breakdown_by` is supplied. Bucket ordering is ascending by `bucket_value` (numeric ascending for `delegation_depth`; `false` before `true` for boolean fields).
- `bucket_value` is typed to match the breakdown field: `uint32` for `delegation_depth`, `bool` for `inherited`, `bool` for `submitted_after_conclusion`.
- The same blinding rules from "Results API blinding" below apply per-bucket: per-bucket temporal fields (e.g., earliest/last eval timestamps) MUST NOT be added.
- Per-dimension aggregation semantics (documented below) apply independently within each bucket; a dimension's `count` within a bucket counts only rows in that bucket where the dimension is non-null.

```json
{
  "experiment_id": "claude-v2-rollout",
  "status": "active",
  "breakdown_by": "delegation_depth",
  "variants": [
    {
      "variant_id": "control",
      "breakdown_by": "delegation_depth",
      "sample_count": 412,
      "breakdowns": [
        {
          "bucket_value": 0,
          "sample_count": 380,
          "scorers": {
            "llm-judge": {
              "mean": 0.75,
              "p50": 0.77,
              "p95": 0.91,
              "count": 380,
              "dimensions": {
                "coherence": { "mean": 0.81, "p50": 0.83, "p95": 0.95, "count": 380 },
                "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 380 }
              }
            }
          }
        },
        {
          "bucket_value": 1,
          "sample_count": 32,
          "scorers": {
            "llm-judge": {
              "mean": 0.68,
              "p50": 0.7,
              "p95": 0.88,
              "count": 32,
              "dimensions": {
                "coherence": { "mean": 0.72, "p50": 0.74, "p95": 0.9, "count": 32 },
                "safety": { "mean": 0.99, "p50": 1.0, "p95": 1.0, "count": 32 }
              }
            }
          }
        }
      ]
    },
    {
      "variant_id": "treatment",
      "breakdown_by": "delegation_depth",
      "sample_count": 45,
      "breakdowns": [
        {
          "bucket_value": 0,
          "sample_count": 40,
          "scorers": {
            "llm-judge": { "mean": 0.82, "p50": 0.84, "p95": 0.94, "count": 40 }
          }
        },
        {
          "bucket_value": 1,
          "sample_count": 5,
          "scorers": {
            "llm-judge": { "mean": 0.74, "p50": 0.75, "p95": 0.88, "count": 5 }
          }
        }
      ]
    }
  ]
}
```

For `breakdown_by=inherited` or `breakdown_by=submitted_after_conclusion`, `bucket_value` is a boolean (two possible buckets: `false`, `true`); the same per-bucket structure applies. The `breakdowns` field and the top-level/per-variant `breakdown_by` echo are present if and only if the request carried `breakdown_by`; the default (flat) response above does not include either.

Aggregation is computed on read by default. The `dimensions` object is present only when at least one `EvalResult` for that scorer has a non-null `scores` field; dimension keys are the union of all keys found across results for that scorer/variant. **Per-dimension aggregation semantics:** for each dimension `d`, `count` equals the number of `EvalResult` records for that scorer/variant where `scores[d]` is non-null (i.e., only results that actually submitted a value for `d`); `mean`, `p50`, and `p95` are computed only over those non-null values. A dimension's `count` may therefore be lower than the enclosing scorer's `count` when some results omitted that dimension. **Selection-bias caveat:** because per-dimension counts can differ, direct cross-dimension comparisons (e.g., "coherence mean vs. relevance mean") may reflect different underlying sample populations rather than a true quality difference; consumers performing such comparisons should inspect per-dimension `count` values and filter to the intersection of submitting results if unbiased comparison is required. For experiments with high eval volume, deployers can opt into a pre-defined Postgres materialized view (`lenny_eval_aggregates`) to trade freshness for query performance at scale. The materialized view is defined in the schema migration system (alongside all other DDL) and is created during database migration — never at runtime by the gateway. The Helm parameter `evalAggregationRefreshSeconds` (default: `0`, meaning disabled — aggregation computed on read via the base tables) controls only whether the gateway schedules periodic `REFRESH MATERIALIZED VIEW CONCURRENTLY lenny_eval_aggregates` calls at the configured interval. When `evalAggregationRefreshSeconds` is `0`, the materialized view still exists in the schema but is never queried; the gateway reads directly from `eval_results`. When set to a positive value (e.g., `60`), the gateway routes results queries to the materialized view and refreshes it at the configured interval.

**Results API blinding.** The Results API response deliberately omits information that could reveal experiment assignment ordering or enrollment rates beyond what the `sample_count` fields disclose. Implementers MUST NOT add fields (e.g., `first_enrolled_at`, `last_eval_at`) that expose per-variant temporal ordering, as this can reveal blinding information during an active experiment.

PoolScalingController manages variant pool lifecycle automatically — variant warm count derived from base pool demand signals × variant weight × safety factor. When a variant pool is created or activated, the controller simultaneously reduces the base pool's `minWarm` to prevent over-provisioning — see "Variant pool sizing and base pool adjustment" in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) for the recomputation formula.

**Experiment status transitions.** Experiment status (`active`, `paused`, `concluded`) is managed exclusively via the admin API. There is no automatic health-based rollback or promotion — an administrator must explicitly transition an experiment between states using `PATCH /v1/admin/experiments/{name}` with `{ "status": "<new_status>" }`. Valid transitions: `active → paused`, `paused → active`, `active → concluded`, `paused → concluded`. Concluded experiments are immutable. The gateway emits an audit event (`experiment.status_changed`) on each transition, including the acting admin identity and the previous/new status.

**Sticky assignment cache invalidation.** `sticky: user` caches variant assignments in Redis keyed by `(user_id, experiment_id)` to avoid re-evaluating assignment on every session creation. When an experiment transitions to `paused` or `concluded`, the gateway flushes all cached `sticky: user` assignments for that experiment (Redis `DEL` on all keys matching `t:{tenant_id}:exp:{experiment_id}:sticky:*`). This prevents stale assignments from routing sessions to a non-existent or paused variant pool after the transition. On `paused → active` re-activation, no flush is required — the existing cached assignment remains valid. The `lenny_experiment_sticky_cache_invalidations_total` counter (labeled by `experiment_id`, `transition`) is incremented on each flush.

**PoolScalingController behavior on experiment status transitions.** When an experiment transitions between states, the PoolScalingController adjusts the variant pool accordingly:

| Transition                                  | PoolScalingController Action                                                                                                                                                                                                                                                                                                                                                                 |
| ------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `active → paused`                           | Sets variant pool `minWarm` to 0. `maxWarm` is **intentionally left unchanged** — existing warm pods are not pre-terminated. They remain available for in-flight sessions already assigned the variant (preventing disruption mid-session), drain naturally as sessions complete, and are reclaimed at cert expiry (bounded at 4 hours). No new warm pods are created. Contrast with `concluded`, where `maxWarm` is also set to 0 to trigger an immediate full drain since no future re-activation is possible. The `SandboxWarmPool` CRD is retained so the pool can be restored on re-activation. **Base pool adjustment:** recomputes the base pool's `minWarm` using the full `(1 - Σ variant_weights)` factor (i.e., with this variant's weight removed from the sum), restoring base pool capacity for the traffic that is no longer being diverted. See [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) "Variant pool sizing and base pool adjustment". |
| `paused → active`                           | Restores variant pool `minWarm` from the experiment definition (recomputed using the standard formula: `base_demand_p95 × variant_weight × safety_factor × (failover_seconds + pod_startup_seconds) + burst_p99_claims × variant_weight × pod_warmup_seconds`). **Base pool adjustment:** simultaneously recomputes the base pool's `minWarm` using the updated `(1 - Σ variant_weights)` factor to account for the re-activated variant's traffic diversion. Normal demand-based scaling resumes for both pools. |
| `active → concluded` / `paused → concluded` | Sets variant pool `minWarm` to 0 and `maxWarm` to 0, triggering full drain. Once `status.readyCount == 0`, the PoolScalingController deletes the variant's `SandboxWarmPool` CRD. The `SandboxTemplate` is **not** deleted — it may be referenced by other experiments or retained for audit purposes. Deletion follows the same controller-owned field boundaries defined in [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries). **Base pool adjustment:** recomputes the base pool's `minWarm` with this variant's weight removed from `Σ variant_weights`, restoring full base pool capacity. |

The PoolScalingController detects experiment status changes by watching the experiment records in Postgres during its normal reconciliation loop (the same loop that reconciles pool configuration into CRDs). No additional watch mechanism is required. If the PoolScalingController is down during an experiment transition, the variant pool retains its pre-transition scaling parameters until the controller recovers and reconciles — the `PoolConfigDrift` alert ([Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)) fires in this scenario.

**Future extension — `ExperimentHealthEvaluator`.** The admin-only transition model is the v1 design. A future version may introduce a pluggable `ExperimentHealthEvaluator` interface that watches eval scores, error rates, or custom metrics and recommends (or auto-executes) status transitions. The interface is reserved but not defined:

```go
// ExperimentHealthEvaluator is a future extension point (not implemented in v1).
// Implementations would evaluate experiment health and recommend status transitions.
type ExperimentHealthEvaluator interface {
    Evaluate(ctx context.Context, experimentID string) (Recommendation, error)
}
```

Until this interface is implemented, all experiment lifecycle decisions are manual.

**Manual Rollback Triggers.** Because rollback is always operator-initiated, deployers should define monitoring thresholds that trigger a human review and, if warranted, a `PATCH /v1/admin/experiments/{name}` with `{ "status": "paused" }`. The following platform-native signals and example thresholds are recommended starting points — adjust to the experiment's sensitivity and baseline:

| Signal | Metric / Source | Example Threshold | Action |
| ------ | --------------- | ----------------- | ------ |
| Elevated variant error rate | `lenny_session_error_total{variant_id="treatment"}` / `lenny_session_total{variant_id="treatment"}` | > 5% over a 5-minute window, sustained for 2 consecutive windows | Pause experiment |
| Variant p95 session latency spike | `lenny_session_duration_seconds{quantile="0.95", variant_id="treatment"}` | > 2× the control group's p95 for 10 consecutive minutes | Pause experiment |
| Mean eval score degradation ① | `GET /v1/admin/experiments/{name}/results` — `variants[treatment].scorers[*].mean` | Treatment mean drops > 0.10 below control mean with ≥ 50 samples per group | Pause experiment |
| Warm pool exhaustion on variant | `lenny_warmpool_idle_pods{pool="<variant_pool>"}` | Reaches 0 and `lenny_pod_claim_queue_depth` > 0 for > 60s | Pause experiment and investigate pool sizing |
| Safety score regression ① | `rate(lenny_eval_score_sum{scorer="safety", variant_id="treatment"}[10m]) / rate(lenny_eval_score_count{scorer="safety", variant_id="treatment"}[10m])` | Mean safety score drops below 0.95 for the variant, regardless of other metrics | Pause experiment immediately |

① **Built-in eval endpoint only.** The eval-based signals (mean eval score degradation, safety score regression) rely on scores submitted via `POST /v1/sessions/{id}/eval`. Deployers whose runtimes use runtime-native eval platforms will not have data in these metrics. Those deployers have two alternatives: (a) configure equivalent score-regression alerts in their eval platform (LangSmith, Braintrust, etc.) and wire them to the `PATCH .../experiments/{name}` pause action, or (b) if using an external experimentation platform (LaunchDarkly, Statsig), report eval scores as custom metrics to that platform and use its guardrail metrics to detect regressions — see "Full A/B testing with external platforms" below.

These thresholds are examples, not platform defaults. Lenny does not enforce them automatically. Deployers should encode them as Prometheus alerting rules (or equivalent) that page on-call and trigger the admin API pause call, either manually or via a runbook-automation script. The `experiment.status_changed` audit event confirms the transition.

**What Lenny explicitly will not build:** Statistical significance testing, automatic experiment lifecycle management (winner declaration, auto-rollback), multi-armed bandits, segment analysis. Those belong in dedicated experimentation platforms.

**Full A/B testing with external experimentation and eval platforms.** Most deployers will use eval platforms (LangSmith, Braintrust, etc.) for session scoring and observability without running A/B experiments. For deployers who want full-featured A/B testing with statistical analysis, Lenny is designed to work as one layer in a three-platform stack:

| Concern | Platform | Mechanism |
| ------- | -------- | --------- |
| Traffic splitting and variant assignment | Lenny (built-in bucketing or external targeting via LaunchDarkly, Statsig, Unleash) | `ExperimentRouter` assigns variant; gateway routes to variant pool |
| Experiment context delivery | Lenny | `experimentContext` in adapter manifest; runtime reads `experimentId` and `variantId` |
| Trace-level scoring and observability | Runtime-native eval platform (LangSmith, Braintrust, W&B, etc.) | Runtime tags traces with variant metadata from adapter manifest for filtering and grouping |
| Statistical analysis and guardrail metrics | External experimentation platform (LaunchDarkly, Statsig) | Runtime reports eval scores as custom metrics via the platform's SDK or events API (e.g., `client.track()` for LaunchDarkly, `logEvent()` for Statsig) |
| Experiment rollback | Operator or automation script → Lenny admin API | `PATCH /v1/admin/experiments/{name}` with `{"status": "paused"}` |

In this pattern, the runtime sends eval scores to **two destinations**: (1) its eval platform for detailed trace analysis, and (2) the experimentation platform as custom numeric metrics for statistical experiment analysis. LaunchDarkly provides Bayesian analysis with credible intervals and probability-to-be-best. Statsig provides frequentist analysis with CUPED variance reduction and sequential testing. Both support guardrail metrics that flag degradation — though as of mid-2025, neither auto-pauses experiments; guardrail breaches surface alerts that an operator or automation script acts on.

Eval platforms (LangSmith, Braintrust, W&B) do not provide native A/B comparison features. When runtimes tag traces with variant metadata, deployers use the eval platform's filtering and grouping capabilities to compare variant results — not a dedicated A/B analysis view. For statistical rigor (significance testing, confidence intervals, winner recommendation), the experimentation platform is the right tool.

The automation gap — no platform in this stack auto-pauses on guardrail breach as of mid-2025 — is bridged by a deployer-operated script or CronJob that either polls the experimentation platform's API for guardrail status or receives a webhook/alert notification and calls Lenny's admin API to pause the experiment.

This three-platform integration is entirely optional. Lenny's built-in experiment primitives and `/eval` endpoint are sufficient for basic A/B comparisons without external platforms. The external integration is for deployers who need statistical rigor, guardrail automation, and the depth of a dedicated experimentation platform.

