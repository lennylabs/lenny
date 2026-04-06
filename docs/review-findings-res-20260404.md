# Technical Design Review Findings — Failure Modes & Resilience Engineering
**Perspective:** 20. Failure Modes & Resilience Engineering
**Category code:** RES
**Document reviewed:** `docs/technical-design.md`
**Date:** 2026-04-04

---

## Summary

| Severity | Count |
|----------|-------|
| Critical | 2 |
| High | 5 |
| Medium | 6 |
| Low | 4 |
| Info | 2 |

---

## Findings

### RES-001 Redis fail-open quota bypass window is too long and unbounded across repeated outages [Critical]
**Section:** 12.4

The spec permits quota enforcement to fail open for up to 60 seconds per outage (`rateLimitFailOpenMaxSeconds`). During this window, every gateway replica applies a conservative per-replica ceiling (`tenant_limit / replica_count`), but the effective cluster-wide enforcement degrades to full tenant budget. A cumulative fail-open timer (`quotaFailOpenCumulativeMaxSeconds`, default 300s) exists but only limits repeated drift across a 1-hour window — meaning up to 5 minutes of fail-open behaviour per hour is permitted. At Tier 3 with 30+ gateway replicas and hundreds of sessions per tenant, a sustained Redis outage can result in significant token overshoot before fail-closed triggers. The tradeoff is documented but the 60-second default for Tier 3 and the 300-second cumulative cap are too permissive for tenants billed per token. Section 17.8 notes Tier 3 should use a 30-second window, but the systemic risk of per-tenant drift accumulation across repeated short Redis outages (each under 60s, so each resets the cumulative timer independently) is not addressed.

**Recommendation:** (1) Reduce `rateLimitFailOpenMaxSeconds` default to 30s for Tier 2+ and 15s for Tier 3; enforce these as tier-locked maximums, not recommendations. (2) Fix the cumulative timer logic so it measures total fail-open time within a rolling 1-hour window regardless of outage count — currently five separate 60-second outages each reset the per-outage clock without accumulating toward the 300-second cap. (3) Add a per-session hard ceiling enforced locally without Redis: once a session has consumed 110% of its `maxTokenBudget` (as last recorded in Postgres), terminate it with `QUOTA_EXCEEDED` regardless of Redis state. (4) Document the acceptable overshoot budget explicitly in the SLO table (Section 16.5).

---

### RES-002 Cascading failure: MinIO outage during eviction checkpoint causes unrecoverable session loss with no fallback store [Critical]
**Section:** 4.4, 12.5

When MinIO is unavailable during a pod eviction (node drain, voluntary disruption), the preStop hook attempts checkpoint upload with exponential backoff (up to ~5 seconds), then gives up. The session record is marked `checkpoint_failed` and a `CheckpointStorageUnavailable` alert fires. If no prior successful checkpoint exists, the session's workspace state is permanently lost. There is no fallback storage path (the spec explicitly states "there is no fallback storage"). This creates a hard dependency: MinIO unavailability during any node drain causes irrecoverable session loss.

The cascading scenario is concrete: a node drain during a MinIO rolling upgrade or network partition triggers simultaneous preStop hooks across all pods on that node, all competing for an unavailable MinIO, all failing. The fact that sessions can resume from the *previous* checkpoint is cold comfort when sessions may not have been checkpointed recently (periodic checkpoints have no guaranteed freshness window specified).

**Recommendation:** (1) Define a checkpoint freshness SLO: the maximum time between successful checkpoints for active sessions (e.g., "at least one successful checkpoint every 10 minutes"). Alert when any active session has no checkpoint within this window. (2) For eviction checkpoints, implement a two-phase fallback: attempt MinIO with the current backoff, then fall back to writing a minimal session state record (cursor position, conversation ID, generation) directly to Postgres if MinIO remains unavailable. This cannot store the workspace tar, but it allows the session to be resumed from last-known conversation state on a fresh pod rather than losing the session entirely. (3) Stagger node drains by default to limit the number of simultaneous eviction checkpoints competing for MinIO. (4) Add a `lenny_session_last_checkpoint_age_seconds` gauge to the alert inventory in Section 16.5.

---

### RES-003 Postgres failover window (up to 30s) creates inconsistency risk for in-flight session state writes [High]
**Section:** 12.3, 10.1

The spec specifies an RTO of < 30s for Postgres automatic failover and states that "during the Postgres failover window, Redis remains the primary coordination mechanism and all Redis-backed roles continue normally." However, the spec does not address what happens to Postgres writes in flight at the moment of failover. Specifically:

- Billing events use an in-memory write-ahead buffer (max 10,000 events per replica). If a replica crashes during a Postgres outage while its buffer is non-empty, those events are "reconstructed from pod-reported token usage during session recovery" — but this is best-effort and not guaranteed lossless for sessions whose pods also failed simultaneously.
- Session state updates (`UPDATE sessions SET state = ...`) issued mid-failover may or may not be committed on the new primary depending on synchronous replication lag.
- The spec states RPO = 0 (synchronous replication, no committed transaction lost) but does not address transactions that were in flight (not yet committed) at failover time — those are lost by definition, and no compensating logic is specified.

The interaction between the 30-second failover window, the 30-second quota sync interval, and in-flight gRPC operations from pods to the gateway is unspecified. A pod reporting token usage via `ReportUsage` RPC during the failover window may receive an error, increment a retry counter, and re-report on reconnection — but the gateway's "take the maximum of Postgres checkpoint and pod-reported cumulative total" reconciliation relies on pod-side cumulative totals being accurate, which requires the pod to have survived.

**Recommendation:** (1) Explicitly document which write categories are durable through failover (committed sync replica writes) versus at-risk (in-flight transactions, buffered billing events on crashed replicas). (2) Add a `session_generation` increment to every session state transition write; detect and reject stale writes using the generation counter that already exists for coordination. (3) For billing events, persist the write-ahead buffer to a local SQLite file on the gateway node (not in-memory) so it survives gateway process restarts — only pod loss simultaneously with a Postgres outage should cause billing gaps. (4) Specify the maximum expected billing gap under simultaneous Postgres outage + gateway crash.

---

### RES-004 Gateway preStop drain hook does not guarantee zero-downtime for long-lived streams [High]
**Section:** 10.1, 17.8

The preStop hook polls `active_streams > 0` at 1-second intervals up to `terminationGracePeriodSeconds` (60s for Tier 1/2, 120s for Tier 3). If active streams have not drained by the grace period deadline, the pod receives SIGKILL and clients must reconnect. This is documented as acceptable. However, several issues make the "drain then reconnect" story incomplete:

1. **Load balancer lag:** After the pod sends a `GOAWAY` (gRPC) or closes the connection, the upstream load balancer may still route new requests to it for several seconds (health check interval + connection drain timeout). The spec does not specify that the gateway pod should stop accepting new connections before the preStop hook starts, or what the expected load balancer deregistration latency is.

2. **In-flight checkpoint interaction:** A session mid-checkpoint (preStop hook triggered the checkpoint on the pod) may have the gateway replica draining while the checkpoint is in progress. If the gateway replica is SIGKILL'd before the checkpoint completes, the session's checkpoint state is inconsistent. The spec does not specify whether the preStop hook should wait for all in-flight checkpoints to complete before declaring "draining."

3. **SSE stream vs gRPC stream:** The spec mentions both SSE and gRPC bidi streams for session interaction. The 60/120s grace period may be insufficient for sessions with `maxSessionAge` up to 7200s — long-running sessions will always be interrupted by a gateway rolling update unless sticky routing is used. The spec explicitly says sticky routing is an optimization, not a correctness requirement, but the operational impact of always interrupting long-running sessions during deploys is unaddressed.

4. **Scale-down race:** The "one pod at a time" scale-down policy combined with the preStop drain timeout means scale-down of 10 replicas takes at minimum 10 × 60s = 10 minutes. This is a very slow scale-down for cost-conscious deployments but is not acknowledged in the capacity planning section.

**Recommendation:** (1) Specify that the gateway pod must set its readiness probe to `false` at the start of preStop to ensure no new requests are routed to it before draining. (2) Add a `graceful_drain_timeout` stage breakdown: (a) stop accepting new sessions (readiness=false), (b) wait for in-flight checkpoints to complete (capped at checkpoint timeout), (c) wait for active streams to drain (capped at remaining grace period). (3) Acknowledge that long-running sessions (> grace period) will be interrupted by rolling updates; recommend sticky routing for deployments with session lifetimes approaching `maxSessionAge`. (4) Document the scale-down time for each tier in Section 17.8.

---

### RES-005 Warm Pool Controller crash blast radius: 15-second leader election gap freezes all pod creation [High]
**Section:** 4.6.1, 17.8

During a WarmPoolController leader failover (~15s via Kubernetes Lease), the spec states that "existing sessions continue unaffected; only new pod creation and pool scaling pause." The gateway queues incoming pod claim requests for up to `podClaimQueueTimeout` (default 30s). If the failover takes longer than 30s (e.g., the Lease renewal fails because the API server is under load, or a network partition delays leader detection), claim requests begin failing with retryable errors. In high-throughput deployments, a 15-30s pause in pod creation can exhaust the warm pool if sessions are being created faster than the `minWarm` buffer absorbs.

The spec provides a formula for `minWarm` sizing that accounts for the failover window, but the formula assumes failover is exactly 15s. The Kubernetes Lease `leaseDuration: 15s, renewDeadline: 10s, retryPeriod: 2s` parameters mean the actual failover could take up to `leaseDuration + renewDeadline = 25s` if the outgoing leader fails to renew before its deadline. This is not the "~15s" stated in the spec.

Additionally, the WarmPoolController and PoolScalingController use separate leader leases and may failover independently — if both failover simultaneously, both pool replenishment and scaling intelligence are down. The blast radius of simultaneous controller failures is not analyzed.

**Recommendation:** (1) Correct the documented failover window: use `leaseDuration + renewDeadline = 25s` (worst case) as the basis for `minWarm` calculations, not 15s. Update the formula examples in Sections 4.6.1 and 17.8. (2) Add a `ControllerLeaderElectionFailed` alert that fires when the Lease has not been renewed within `leaseDuration - 5s`, giving operators early warning before failover. (3) Specify behavior when both controllers failover simultaneously (pool creation paused + scaling intelligence paused) and confirm the `podClaimQueueTimeout` default covers this scenario. (4) Add a minimum `minWarm` recommendation in Section 17.8 that accounts for the corrected failover window.

---

### RES-006 PoolScalingController failure leaves pools at stale desired state indefinitely [High]
**Section:** 4.6.2

The PoolScalingController reconciles pool configuration from Postgres into CRDs on a watch-driven loop. If the PoolScalingController crashes (or is stuck in leader election) while an admin API call has updated the pool desired state in Postgres, the CRD state and the Postgres desired state diverge. The WarmPoolController continues operating against the stale CRD spec — it maintains `minWarm` pods at the old value indefinitely.

This divergence has no detection mechanism specified. An admin calling `PUT /v1/admin/pools/{name}` may believe the pool has been updated when in fact the CRD has not been reconciled. The spec mentions that "CRDs become derived state reconciled from Postgres" but does not specify a reconciliation lag SLO, a staleness alert, or how operators can detect that CRDs are out of sync.

**Recommendation:** (1) Add a `PoolConfigDrift` warning alert that fires when any pool's Postgres desired state differs from its CRD spec for more than 60 seconds (detectable via the PoolScalingController's `observedGeneration` tracking). (2) Expose a `GET /v1/admin/pools/{name}/sync-status` endpoint that reports whether the pool's CRD is in sync with its Postgres desired state. (3) The admin API response to `PUT /v1/admin/pools` should include an async `syncStatus` field indicating that CRD reconciliation is pending, so operators are not misled into believing changes are instantly effective. (4) Add a `lenny_pool_config_reconciliation_lag_seconds` gauge to the metrics inventory.

---

### RES-007 Delegation tree budget operations use Redis atomics but have no durability guarantee across Redis restart [High]
**Section:** 8.3, 12.4

The delegation budget system uses Redis atomic operations (`DECRBY`, `INCR`) as the primary enforcement layer, with returns credited back to parent budgets on child completion. The spec states these counters "follow the same durability model as token usage counters: Postgres checkpoint every `quotaSyncIntervalSeconds` (default: 30s)." However, during a Redis failure that triggers the fail-open path, delegation budget counters fall back to Postgres lookups which may be up to 30 seconds stale.

More critically: if Redis is restarted (not just briefly unavailable) between Postgres checkpoints, in-flight delegation tree counters are lost. The reconciliation logic described in Section 11.2 ("take the maximum of Postgres checkpoint and pod-reported cumulative total") applies to token usage, but it is not specified whether delegation tree counters (`maxTreeSize` active pod count, `maxTokenBudget` consumed) are also reconstructed from pod-reported state on recovery. A tree-size counter reconstructed incorrectly could allow a delegation tree to spawn more children than `maxTreeSize` permits.

**Recommendation:** (1) Explicitly specify that delegation budget counters (`maxTreeSize`, `maxTokenBudget` reservation) are included in the Postgres checkpoint cycle with the same 30-second interval as token usage. (2) On Redis recovery, the gateway must re-validate every active delegation tree's budget state against Postgres before accepting new delegation requests. Add this to the Redis recovery runbook in Section 17.7. (3) If a delegation tree's budget state cannot be reconstructed confidently (e.g., Postgres checkpoint is stale and the coordinating gateway replica crashed), the tree should be moved to `awaiting_client_action` rather than allowing potentially over-budget delegations. (4) Add `lenny_delegation_budget_reconstruction_total` as a counter metric to detect and alert on reconstruction events.

---

### RES-008 Checkpoint quiescence timeout recovery path leaves runtime in indeterminate state if adapter crashes mid-checkpoint [Medium]
**Section:** 4.4

The spec states that for the Full-tier lifecycle channel checkpoint path, "if the runtime sends `checkpoint_ready` but does not receive `checkpoint_complete` within 60 seconds, it MUST autonomously resume normal operation." This protects against adapter crashes or network partitions during the snapshot phase. However, the recovery scenario where the adapter crashes *after* the runtime has sent `checkpoint_ready` but before it receives `checkpoint_complete` leaves the runtime autonomously resuming in a state where:

1. A partial snapshot may have been written to MinIO (some objects uploaded, tar incomplete).
2. The session record in Postgres may not reflect that a checkpoint attempt occurred.
3. The gateway, having lost the adapter connection, will attempt to resume the session on a new pod — which will replay the last *successful* checkpoint, potentially replaying work the runtime had already completed.

The 60-second autonomous resume timeout is not coordinated with the gateway's pod failure detection timeout (`heartbeat_timeout`). If the gateway detects pod failure before the 60-second runtime timeout fires, the gateway may attempt to resume the session on a new pod while the original runtime is still running (autonomously resumed). This creates a split-brain: two "instances" of the same session are active briefly — one on the original pod (recovered runtime) and one being set up on the new pod.

**Recommendation:** (1) Specify that the runtime, upon autonomous resume (checkpoint timeout), must send a `checkpoint_timeout` event on the lifecycle channel. If the lifecycle channel is broken (adapter crashed), the runtime should treat this as a termination signal and exit, yielding to the gateway's recovery flow. (2) Define the coordination between `heartbeat_timeout` and the 60-second checkpoint timeout: the gateway's heartbeat interval must be shorter than 60s so that pod failure is detected and the new pod provisioned *after* the original runtime's autonomous resume window expires. (3) Add "adapter crash during checkpoint" to the runbook in Section 17.7 with explicit recovery steps.

---

### RES-009 Session coordination lease expiry during gateway replica crash creates a recovery gap [Medium]
**Section:** 10.1

The spec uses a Redis `SET NX` with TTL for per-session coordination leases and falls back to Postgres `SELECT ... FOR UPDATE SKIP LOCKED` when Redis is unavailable. The TTL on the Redis lease is not specified. If the TTL is shorter than the time required for: (a) the gateway replica crash to be detected, (b) a new replica to acquire the lease, and (c) the new replica to issue a `CoordinatorFence` RPC to the pod — then the pod may experience a gap where no coordinator holds the lease, causing the pod to enter its `heartbeat_timeout` hold state prematurely.

The spec states "if that replica dies, another picks up after TTL expiry" but does not specify the TTL value or confirm it is sized to accommodate the total handoff latency. At Tier 3 with many replicas, Kubernetes service discovery for the pod endpoint must also complete before the new coordinator can issue the `CoordinatorFence` RPC — this adds latency not accounted for in the spec.

**Recommendation:** (1) Specify the Redis lease TTL explicitly (recommend: `max(heartbeat_interval × 3, 30s)`). Document the TTL in the operational defaults table (Section 17.9). (2) Specify the pod's `heartbeat_timeout` value explicitly (it is referenced in Section 10.1 but never defined) and confirm `TTL > heartbeat_timeout` to prevent premature hold state. (3) Add the Redis lease TTL to the preflight validation checks (Section 17.6) so a misconfigured TTL is caught at install time. (4) Document the expected total coordinator handoff latency (crash detection + lease expiry + new replica acquisition + fence RPC) in Section 10.4 Gateway Reliability.

---

### RES-010 Certificate denial list propagation via Redis pub/sub has no defined fallback if Redis is unavailable [Medium]
**Section:** 10.3

The spec describes an in-memory certificate deny list propagated across gateway replicas via "Redis pub/sub (with Postgres `LISTEN/NOTIFY` as fallback)." This is used for immediate certificate revocation when a pod is terminated for security reasons. However:

1. The spec does not specify the TTL of deny list entries in Redis or how they are persisted. If Redis restarts, the deny list is lost from the pub/sub channel history. New gateway replicas that come up after the Redis restart have an empty deny list until a re-notification occurs.
2. The Postgres `LISTEN/NOTIFY` fallback is mentioned but not specified: is there a background job that re-publishes the deny list on recovery? How do new gateway replicas that missed the pub/sub event discover the current deny list?
3. The deny list is described as "ephemeral — each entry expires when the certificate's natural TTL lapses (at most 4h)." If a new gateway replica starts and Redis is unavailable, the replica has no deny list for up to 4 hours — during which revoked certificates could be accepted.

**Recommendation:** (1) Persist the deny list entries in Postgres (a `certificate_deny_list` table with `spiffe_uri`, `serial_number`, `expires_at`) as the source of truth, using Redis pub/sub only for low-latency propagation. (2) On gateway startup, each replica must load the full current deny list from Postgres before accepting connections. (3) Define the reconciliation interval: each replica should re-read the deny list from Postgres every N minutes (recommend: every 5 minutes) as a fallback for missed pub/sub events. (4) Add a `lenny_cert_deny_list_size` gauge and alert if the deny list grows unexpectedly large (indicating a revocation storm or list leak).

---

### RES-011 Concurrent-workspace slot failure recovery has an unspecified interaction with the pod's liveness probe [Medium]
**Section:** 5.2

For `concurrent` execution mode with `concurrencyStyle: workspace`, slot failures are described as isolated — a single slot failure does not terminate the pod or affect other active slots. However, the spec does not specify:

1. Whether a slot failure affects the pod's liveness probe. If the adapter's health probe degrades when a slot fails (the spec mentions "the adapter's health probe degrades" for resource contention but not for slot failures), Kubernetes may restart the pod, terminating all surviving slots.
2. When the adapter reports a slot failure to the gateway via the lifecycle channel, what the gateway does with other active slots on the same pod. The spec says "the gateway is notified via the lifecycle channel and may retry or report the failure to the client" — but the fate of other slots during this notification is unspecified.
3. If a slot's cleanup timeout expires ("if cleanup fails, the slot is leaked"), the leaked slot's workspace directory and processes persist. The spec says "the pod continues but the slot is not reclaimed until pod termination" — but does not specify whether the leaked slot's resource consumption (CPU, memory, disk) is tracked, or whether the pod's effective `maxConcurrent` is reduced.

**Recommendation:** (1) Specify that slot failures do NOT affect the pod's liveness probe. The liveness probe should report healthy as long as at least one slot is capable of accepting work (or the pod is not overloaded). (2) Add a `SlotLeak` warning metric and alert when the number of leaked slots on a pod exceeds a threshold (e.g., 2+ leaked slots on one pod should trigger pod termination and replacement). (3) Define the gateway's behavior when it receives a slot failure notification: the gateway should immediately mark that `slotId` as unavailable and reduce the pod's reported available slot count, preventing new tasks from being routed to a slot that is cleaning up.

---

### RES-012 GC job runs as a leader-elected goroutine inside the gateway — gateway crash during GC leaves artifacts in a half-cleaned state [Medium]
**Section:** 12.5

The artifact GC job "runs as a leader-elected goroutine inside the gateway process (not a separate CronJob). Only one gateway instance runs GC at a time via the existing leader-election lease." If the gateway replica running GC crashes mid-cycle:

1. MinIO deletes that have been issued but not yet confirmed may or may not have completed.
2. Postgres rows for those artifacts may or may not have been marked `deleted`.
3. The spec says "individual artifact deletion failures are retried independently" and "the GC job is idempotent" — but idempotency only holds if the Postgres state accurately reflects what has been deleted from MinIO. If MinIO deletes succeeded but Postgres was not updated (gateway crash between the two), the next GC cycle will attempt to re-delete already-deleted MinIO objects (idempotent) and then update Postgres — so this case is safe.
4. The reverse (Postgres marked `deleted` but MinIO delete not issued) is safe because the GC job checks MinIO delete-on-absent as a no-op.

However, the spec also states that the GC job "queries Postgres for artifacts past their TTL, deletes them from MinIO, then marks the rows as `deleted` in Postgres." If the Postgres mark-as-deleted step fails (transient Postgres error), the artifact is deleted from MinIO but remains in Postgres as pending deletion — the next cycle will attempt MinIO delete again (safe no-op) and then retry the Postgres update. This is correctly handled by the idempotency claim.

The real gap is: at Tier 3, a 15-minute GC cycle running inside a gateway process that may also be handling 20,000+ active streams introduces resource contention. The spec does not specify GC job resource limits (goroutine count, MinIO delete concurrency, Postgres query plan).

**Recommendation:** (1) Add explicit concurrency limits for the GC job: maximum parallel MinIO delete operations (recommend: 50) and a configurable Postgres batch size (recommend: 100 rows per query). (2) Specify that GC jobs run during low-traffic windows or yield to high-priority gateway work via a weighted goroutine scheduler. (3) Add a GC job circuit breaker: if GC errors exceed 10% of artifacts in a cycle, pause GC and fire a `GCHighErrorRate` warning alert before the next cycle. (4) Consider extracting GC to a Kubernetes CronJob at Tier 3 to isolate its resource usage from gateway traffic handling — note this as a scaling trigger alongside the subsystem extraction triggers in Section 4.1.

---

### RES-013 Agent pod heartbeat_timeout value is never specified — undefined behavior if the pod enters hold state [Low]
**Section:** 10.1

The spec references `heartbeat_timeout` in multiple places ("the pod will enter a hold state awaiting a new coordinator," "the pod's heartbeat_timeout will fire") but never defines its value. This creates operational uncertainty: operators cannot size `podClaimQueueTimeout` (30s), coordinator lease TTL, or checkpoint timeout relative to the heartbeat timeout. The interaction between these timeouts is load-bearing for the coordinator handoff protocol (Section 10.1) and the checkpoint recovery path (Section 4.4).

**Recommendation:** Define `heartbeat_timeout` explicitly in the protocol spec and the operational defaults table (Section 17.9). Recommend a value (e.g., 30s: 3× the adapter's heartbeat interval of 10s per Section 15.4.1). Ensure `heartbeat_timeout < checkpoint_timeout (60s) < coordinatorLeaseTTL`. Document the full timeout ordering constraint.

---

### RES-014 Controller work queue overflow drops reconciliation events silently [Low]
**Section:** 4.6.1

The spec states: "if the queue exceeds [max depth], new reconciliation events are dropped and a `lenny_controller_queue_overflow_total` metric is incremented." Queue overflow means pool scaling, pod lifecycle transitions, and warm pool replenishment events are silently dropped until the queue drains. The controller's next watch event will retrigger reconciliation, but the delay is unbounded — it depends on when the next CRD mutation event arrives. For pools with no churn (all pods warm and idle), no watch events arrive and the dropped reconciliation is never retried.

**Recommendation:** (1) Replace event dropping with backpressure: when the queue is full, new events should block (with a configurable timeout) rather than drop. (2) If dropping is retained for performance reasons, add a reconciliation timer: schedule a forced re-reconciliation for every pool every N seconds (e.g., 60s) regardless of watch events, ensuring dropped events are recovered within a bounded time. (3) Add a `ControllerQueueOverflow` warning alert that fires when `lenny_controller_queue_overflow_total` increases, not just when it is non-zero.

---

### RES-015 No explicit behavior defined for partial MinIO cluster failure (fewer than erasure quorum nodes) [Low]
**Section:** 12.5

The spec requires MinIO with erasure coding (minimum 4 nodes) for production, noting "data durability" as the goal. However, it does not specify the platform's behavior when MinIO is degraded but not fully unavailable — specifically when fewer nodes fail than the erasure quorum requires for writes (typically n/2+1 for 4-node deployments). In this state, MinIO may serve reads (existing objects) but reject writes (new checkpoints, workspace snapshots).

The checkpoint failure path (Section 4.4) treats this as a full MinIO failure: retries, then marks the session `checkpoint_failed`. But the artifact retrieval path (Section 12.5) does not specify whether it can serve reads from a degraded MinIO cluster. If artifact downloads fail during a MinIO quorum loss event, users cannot download their session outputs.

**Recommendation:** (1) Add a `MinIODegraded` warning alert that fires when MinIO reports quorum loss (detectable via MinIO's `/minio/health/cluster` endpoint — responds 503 when cluster is degraded). (2) Specify that artifact reads (workspace downloads, transcript downloads, log fetches) should be served from available MinIO nodes even during write quorum loss. (3) Document MinIO erasure coding parameters in Section 12.5 (e.g., data shards and parity shards for each recommended topology) so operators understand the exact failure thresholds.

---

### RES-016 Dual-store unavailability graceful termination after 60s does not specify how in-flight sessions are drained [Low]
**Section:** 10.1

The spec states that if dual-store unavailability (Redis + Postgres both down) exceeds `dualStoreUnavailableMaxSeconds` (default: 60s), "replicas begin gracefully terminating sessions that have had no successful store interaction, emitting `session.terminated` with reason `store_unavailable` when Postgres recovers." Two issues:

1. The termination event is emitted "when Postgres recovers" — but if Postgres recovery takes longer than `dualStoreUnavailableMaxSeconds`, the sessions have already been terminated locally (gateway discards state) but the clients have not yet received the `session.terminated` event. Clients will see a broken connection and attempt reconnect, which may or may not succeed depending on whether Postgres has recovered by reconnect time.
2. The phrase "sessions that have had no successful store interaction" is ambiguous: does this mean sessions that had no Postgres write succeed during the outage window, or sessions that have had no store interaction since their last checkpoint? A long-running session with a successful last checkpoint but no Postgres writes during the 60s outage window would be terminated — even though it could be fully recovered from checkpoint.

**Recommendation:** (1) Clarify that only sessions whose state cannot be reconstructed from checkpoint should be terminated. Sessions with a valid checkpoint should be kept in hold state and recovered once Postgres comes back. (2) Specify that `session.terminated` events are emitted immediately to the client over any still-open stream, not deferred until Postgres recovery. If no stream is open, the event is written to Postgres on recovery. (3) Define a maximum hold period for sessions in dual-store-unavailable state that is longer than `dualStoreUnavailableMaxSeconds` (e.g., hold for `maxResumeWindowSeconds` = 900s) to give Postgres time to recover before abandoning sessions.

---

### RES-017 No chaos engineering or fault injection test suite specified [Info]
**Section:** 17.3, 18

The spec includes a comprehensive load testing plan (Phase 13.5) and a restore testing CronJob (`lenny-restore-test`). However, it does not specify any fault injection or chaos engineering tests. Given the complexity of the failure mode interactions documented in this review (cascading checkpoint failures, dual-store unavailability, coordinator split-brain, leader election gaps), the correctness of the resilience mechanisms cannot be validated by load tests alone.

**Recommendation:** Add a fault injection test suite (Phase 13.5 or Phase 14) using a tool like Chaos Mesh or LitmusChaos. Minimum required scenarios: (1) Redis primary failure during active sessions, (2) MinIO unavailability during eviction checkpoint, (3) Postgres failover during high write throughput, (4) WarmPoolController leader election during peak session creation, (5) network partition between gateway and pods, (6) simultaneous failure of 1 gateway replica during rolling update. Record the recovery behavior for each scenario and compare against the spec's stated guarantees.

---

### RES-018 Retry exhaustion after pod failure during session setup has no client-visible progress state [Info]
**Section:** 6.2

When a pod fails during setup (states `receiving_uploads`, `running_setup`, `finalizing_workspace`, `starting_session`), the gateway retries up to 2 times (3 total attempts) with 500ms/1s backoff. The client receives a session ID at step 8 of the normal flow but then waits while retries happen. The spec does not specify what events the client receives during the retry loop. A client waiting for a response during 3 failed pod setup attempts (potentially 3 × 30s workspace materialization attempts = 90+ seconds) with no progress events would appear hung.

**Recommendation:** (1) Specify that the gateway emits a `status_change(state: "retrying", attempt: N, reason: "pod_setup_failed")` event to the client's event stream during pre-attach retries. (2) Add a `lenny_session_setup_retry_total` counter labeled by failure reason so operators can detect systemic setup failures (e.g., a bad runtime image causing all pods to fail setup). (3) Consider whether setup retries should be visible to the client or transparent — if transparent, set a tighter total retry timeout to avoid silent hangs.
