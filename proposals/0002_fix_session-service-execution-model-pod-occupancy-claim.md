# Proposal: Session/service execution model: task mode removed, one parameterized session mode, per-pod occupancy claims, and the WarmPoolController-sole-written `Sandbox.status` carried forward

- **Status:** Verified (2026-06-12). Converged after 4 adversarial review rounds (4 findings fixed); awaiting sign-off. Initial recommendation authored from the 2026-06-11 execution-mode design discussion; the first-round open decisions were resolved the same day (Section 12). Seventeen adversarial convergence passes applied 2026-06-11 through 2026-06-12 (Section 11), followed by the promotion of Section 6 to exact anchored edit blocks (Section 11, Revision).
- **Date:** 2026-06-11.
- **Scope:** Replaces the `session | task | concurrent` execution modes with two modes, `session` and `service`. Session mode is one mechanism parameterized by a `sessionPolicy` block (`maxConcurrentSessions`, `recycle.*`); the current session, task, and concurrent-workspace modes become configuration presets of it, and task mode's runtime-delimited intra-session task cycling is removed in favor of pod recycling across whole sessions. The session/task concept collapses to a 1:1 mapping: the session is the only client-facing unit of execution, and "Task" survives only as the external-protocol name (MCP and A2A Tasks, `lenny/delegate_task`). The `SandboxClaim` moves from per-session to per-pod-occupancy granularity: the deterministic CREATE guards pod acquisition, the Redis slot counter is the intra-pod capacity gate (with a §12.4-consistent Postgres fallback, Section 3.2), and the session-to-pod binding lives on the Postgres session row's `pod_assignment` column. A recycled pod's claim is held through a short `reserved` window (a deployment-level TTL, default 10 seconds) so back-to-back sessions of the same tenant reuse the claim without etcd churn. Pool exhaustion gains a bounded queueing option (`onPoolExhausted: queue`), and `sessionPolicy` gains a client-inactivity bound (`maxClientIdleSeconds`). Service mode (the current `concurrencyStyle: stateless`) becomes claimless as §5.2 already describes, keeps `multi_turn` runtimes permitted, and surfaces its no-continuity contract through a new `sessionIsolationLevel.conversationContinuity` field plus a registration-time warning. This proposal supersedes `proposals/0002_fix_pod-status-ownership-decomposition-warmpoolcontroller-sole-w.md`: it carries forward that proposal's pod-status ownership decomposition (the WarmPoolController as sole writer of `Sandbox.status`, occupancy as a claim projection, the coarse phase enum, and the gateway RBAC convergence), re-derived for per-pod claims, and discards its task-mode between-task disposition machinery (the per-task `ReportTaskCleanup` counting, the per-task Redis counter posture, and the `task_dispatch_deadline` column), which is replaced by a simpler per-session recycle disposition. The prior 0002 file is retained until this proposal is converged, after which this document becomes the canonical 0002. Re-triages F-5.2.30 as superseded.

This document stages the proposed spec edits as exact anchored edit blocks in Section 6. Each subsection carries an anchor instruction quoting the existing spec text and a fenced block with the exact replacement or inserted text, applied mechanically after sign-off. Schema, chart, code, and documentation changes are recorded for the implementer in Sections 5, 7, and 13 and land with the implementation work. It does not modify any spec file.

## 1. Problem

The execution-mode surface has accumulated the following classes of defect.

**Task mode has an incoherent client contract.** The client creates sessions and sends messages; task mode partitions that message stream into tasks at boundaries the adapter signals via `task_complete` after each task completes (`spec/15_external-api-surface.md:1461`; the runtime's role is the `task_complete_acknowledged` reply), with no client-visible marker, and the platform scrubs the pod between them. A client that sends a follow-up message therefore loses all context with no error and no signal. The mode is also unavailable to most runtimes: the between-task lifecycle requires the Full integration level, and Standard and Basic runtimes silently degrade to `maxTasksPerPod = 1` (`spec/05_runtime-registry-and-pool-model.md:417-420`), which removes the mode's only benefit. Its efficiency advantage over per-session pod reuse is one session row and one claim pair per job.

**Session and task are not clearly defined, and the internal task concept is vestigial without task mode.** The spec uses "task" for the intra-session execution unit (`TaskID`, `spec/15_external-api-surface.md:2568-2573`), for the delegation unit (`lenny/delegate_task`, §8), and for the external protocol object (MCP and A2A Tasks, §7.2). Clients and users care about sessions; tasks are internal. With task mode removed, every session has exactly one execution, and the dual concept carries definition weight with no behavior behind it.

**The modes are one mechanism with three configurations.** Session, task, and concurrent-workspace all claim a pod, materialize workspaces, run the session lifecycle, and differ only in pod multiplexing (one session, sequential reuse, or concurrent slots) and in what happens when the pod's occupancy reaches zero (terminate or return to the pool). Their acknowledgments, tenant-pinning rules, scrub procedures, and scaling factors are keyed to mode names rather than to the properties that create the risk, and the concurrent-workspace recycle path has a latent gap: a pod that returns to `idle` after its last slot never receives a whole-pod scrub, so shared `/tmp`, `/dev/shm`, and process residue persist across occupancy cohorts.

**Service-style workloads have two unresolved seams.** The implementation routes `concurrencyStyle: stateless` through the per-slot claim path (`pkg/gateway/podsession/slotbinder.go`, `pkg/gateway/podclaim/slotclaimer.go`) although §5.2 describes claimless tenant-affinity Service routing (`spec/05_runtime-registry-and-pool-model.md:500`). Nothing defines the interaction between `capabilities.interaction: multi_turn` and stateless routing: a multi-turn runtime in a stateless pool silently loses conversation context between messages, with no documentation, no warning, and no client-visible contract field.

**The per-session claim makes etcd traffic scale with session rate.** Each session pays a `SandboxClaim` CREATE, a status write, an admission-webhook round trip, and a DELETE. The claim's three duties (the double-claim guard, the controller's in-use signal for occupancy and drain, and a durable hold that survives a Postgres outage) are properties of the pod rather than of the session; the per-session granularity is an artifact of session mode's 1:1 collapse of pod and session.

The prior 0002 resolved the pod-status ownership defects (two writers of `Sandbox.status`, fine session phases on the CRD) and built a per-task disposition pipeline for task mode. The ownership half remains correct and is carried forward; the task half is mooted by this redesign.

## 2. Decisions

The following were decided in the design discussion and constrain this proposal.

- Task mode is removed. Pod amortization is provided by recycling pods across whole sessions of the same tenant, with a scrub between sessions. The client-facing unit is always the session.
- The execution modes are `session` and `service`. The names follow the unit of the client contract: in session mode the session is the managed unit (bound pod, workspace, lifecycle, and recovery); in service mode the runtime is a replicated service and the gateway routes each message to any ready replica. The mode enum is renamed from the current `session | task | concurrent` values; `concurrencyStyle` is removed.
- Session mode is parameterized by a `sessionPolicy` block rather than split into named sub-modes. The current modes become documented presets of the block.
- Acknowledgments, tenant pinning, `residualStateWarning`, and scaling factors are derived from `sessionPolicy` properties rather than keyed to mode names.
- Session and task collapse to a 1:1 mapping in two phases. Phase 1 (this proposal) defines the session as the only unit of execution, freezes `TaskID` to the root task identifier, and deletes the between-task lifecycle frames. Phase 2 (a separate cleanup proposal) performs the lexical sweep of internal "task" names.
- The `SandboxClaim` becomes a per-pod occupancy claim with the deterministic name `claim-<podName>`. The Redis slot counter is the intra-pod capacity gate, with a Postgres fallback per §12.4 (Section 3.2). The session-to-pod binding is the Postgres session row's `pod_assignment` column (migration 0050; `SessionStore.PodAssignment`).
- The pod-status ownership decomposition from the prior 0002 is carried forward: the WarmPoolController is the sole writer of `Sandbox.status` (phase and conditions), occupancy is a level-triggered projection of claim existence and pool policy, the phase enum is coarsened to occupancy values, and the gateway's `sandboxes/status` grant is removed.
- The per-session recycle counters (`sessionsServed` and `scrubFailureCount`) are gateway-written columns on the per-pod `agent_pod_state` Postgres row, updated at session end. Session-rate events already write Postgres, so the prior 0002's per-task Redis counter posture is unnecessary and is discarded. The Redis slot counter for concurrency capacity is unaffected.
- Service mode permits `multi_turn` runtimes. The contract (no platform conversation continuity; every message is self-contained) is documented, surfaced through `sessionIsolationLevel.conversationContinuity`, and flagged by a registration-time warning event.
- Safe defaults: `recycle.enabled` defaults to `false`; a session that ends in failure or a crash always retires its pod regardless of recycle settings; recycling requires `acknowledgeBestEffortScrub`; `maxConcurrentSessions > 1` requires `acknowledgeProcessLevelIsolation`; `scrubProfile: in-place` requires `acknowledgeMicrovmResidualState` (the cross-tenant guest-kernel residual-state gate carried forward from the current `microvmScrubMode: in-place` rule); and `recycle.allowCrossTenantReuse: true` is categorically rejected when `maxConcurrentSessions > 1` regardless of isolation profile (the current concurrent-workspace `spec/05:498` prohibition carried forward, distinct from the microvm gate that permits cross-tenant reuse on the sequential-reuse path).
- The recycled pod's claim is held rather than deleted: the gateway patches the claim to a `reserved` binding state when occupancy reaches zero on a recycling pod, stamps the hold deadline on the claim status (`holdExpiresAt`), and deletes it only when the deployment-level hold TTL expires (`gateway.claimHoldTTLSeconds`, default 10) or the pod retires. The hold-expiry DELETE carries Kubernetes preconditions (claim UID plus the resourceVersion observed at the `reserved` patch) so a concurrent rebind from any gateway replica wins the race (Section 3.2). Configuration validation warns when the TTL is set high, because a long hold delays the pod's return to the pinned tenant's claimable idle inventory and delays retirement-limit evaluation.
- The coarse phase enum carries no `slot_active` value; concurrent occupancy reads `claimed`, and concurrency is observable through the Redis counter and metrics.
- `sessionPolicy` includes `maxClientIdleSeconds`, which replaces the pool-level `runtime.limits.maxIdleTimeSeconds` knob and its §6.2 agent-inactivity timer as the single idle bound; one clock with one pause table survives. The default changes from the current 600s to the pool's effective `maxSessionAgeSeconds`, which raises the platform idle bound to the age cap so the bound binds before the age cap only when a deployer lowers it or when the session accrues idle time in a state where `maxSessionAge` is paused but this clock runs (`awaiting_client_action`). This relaxation is deliberate: abandoned sessions hold pods, slots, and credential leases up to the age cap (default 7200s) at the default setting, and the age cap remains the backstop for any state in which both clocks advance. The clock is paused during `suspended` (a suspended session may otherwise persist indefinitely by design, §8), during the recovery states `resume_pending` and `resuming` (carrying forward the existing `maxIdleTimeSeconds` pause during `resume_pending`, `spec/06_warm-pod-model.md:292`), and does not run during `finalizing`; it runs during `input_required`, `awaiting_client_action`, and elicitation waits, because waiting on an absent client is the condition the bound exists to reclaim. `maxSessionAge` runs during `input_required` but is paused during `awaiting_client_action`, so the clock has its own pause table rather than sharing the `maxSessionAge` pause table, and during the age-paused `awaiting_client_action` wait the default-equal bound can bind before the age cap. Agent activity counts as activity, so autonomous work is never idle-terminated. The effective value lands on the existing per-session `SessionTimeouts.MaxIdleSeconds` field through the existing most-restrictive resolution, and expiry reuses the existing `max_idle_time` reason.
- The scrub outcomes are reported on separate RPCs matching the scrub split: `ReportSessionScrub` for the per-session slot cleanup at each session release, and `ReportPodScrub` for the whole-pod scrub at the occupancy-zero recycle boundary.
- The metric renames are fixed: `lenny_pod_session_reuse_count`, `lenny_pod_scrub_failure_total`, `lenny_pod_scrub_failure_count`, `lenny_pod_retirement_total`, and the `lenny_stateless_*` family becomes `lenny_service_*`. No deprecated aliases are kept.
- `onPoolExhausted: queue` is in scope for this proposal; the batch session-creation API remains a follow-up.

## 3. Design overview

### 3.1 The mode surface

```yaml
executionMode: session            # session | service
sessionPolicy:                    # session mode only
  maxConcurrentSessions: 1        # >1 requires acknowledgeProcessLevelIsolation
  recycle:
    enabled: false                # true requires acknowledgeBestEffortScrub
    maxSessionsPerPod: 50         # required when enabled; counts every session served
    maxPodUptimeSeconds: 86400    # existing knob, relocated; optional, matches the §5.2 example
    maxScrubFailures: 3           # existing knob, relocated
    onScrubFailure: warn          # warn | fail; existing onCleanupFailure, relocated
    scrubProfile: standard        # standard | vm-restart | in-place; absorbs microvmScrubMode
    acknowledgeMicrovmResidualState: false # required when scrubProfile: in-place; cross-tenant guest-kernel residual state
    allowCrossTenantReuse: false  # microvm-gated for sequential reuse; never permitted when maxConcurrentSessions > 1
  cleanupCommands: []             # existing knob, relocated
  cleanupTimeoutSeconds: 60       # existing knob, relocated
  maxSessionRetries: 1            # existing maxTaskRetries, renamed (crash re-dispatch); default 1 (2 total attempts; 0 disables)
  maxSessionAgeSeconds: 7200      # existing template field, surfaced here (2h default)
  maxClientIdleSeconds: 7200      # replaces runtime.limits.maxIdleTimeSeconds; defaults to the effective maxSessionAgeSeconds
  slotRetries: 1                  # existing fixed value, exposed
  onPoolExhausted: reject         # reject | queue (bounded synchronous wait)
  maxQueueWaitSeconds: 30         # queue wait bound when onPoolExhausted is queue
```

The presets, for documentation and migration:

| Preset | maxConcurrentSessions | recycle.enabled | Replaces |
|:--|:--|:--|:--|
| One session per pod | 1 | false | session mode |
| Pod reuse | 1 | true | task mode |
| Concurrent | N | true | concurrent-workspace |
| Bounded cohort | N | false | new combination: serve N concurrent sessions, then terminate after the cohort drains |

Derivations replace mode-keyed rules. Tenant pinning is required when `maxConcurrentSessions > 1` or `recycle.enabled` is true. `acknowledgeBestEffortScrub` is required when recycling is enabled, `acknowledgeProcessLevelIsolation` is required when concurrency exceeds one, and `acknowledgeMicrovmResidualState` is required when `scrubProfile: in-place` (the cross-tenant microvm in-place reuse path, which leaves guest-kernel residual state, DNS resolver cache, TCP `TIME_WAIT` state, page-cache priming, and inotify/fanotify registrations, persisting across the tenant boundary). The `acknowledgeMicrovmResidualState` gate is the existing `spec/05:442` mandatory acknowledgment carried forward onto the renamed `scrubProfile` field rather than dropped with the `microvmScrubMode` knob; it is additional to `acknowledgeBestEffortScrub`, fail-closed, so the pool controller rejects `scrubProfile: in-place` without it. `residualStateWarning` and `podReuse` are true when `recycle.enabled` is true, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`; service-mode pods serve successive requests with no scrub and share process space, network stack, `/tmp`, and page cache across same-tenant concurrent requests, the rationale §7.1 records today for concurrent-stateless (`spec/07_session-lifecycle.md:71-75`). `capabilities.preConnect: true` is admitted only when `maxConcurrentSessions` is 1, and service-mode pools reject it, because the SDK-warm model assumes a single agent process awaiting a single first prompt; this re-keys the §6.1 compatibility table (`spec/06_warm-pod-model.md:71-78`) and preserves the existing code rejection's spec anchor. The §5.2 scaling factors become, in session mode, `mode_factor` = expected sessions per pod lifetime (bounded by `maxSessionsPerPod` and measured by the reuse histogram) and `burst_mode_factor` = `maxConcurrentSessions`. Service mode keeps a per-pod concurrency capacity, because its readiness-driven routing and saturation scaling are keyed to the pod's slot bound: the pool-level `maxConcurrent` field that the current `concurrencyStyle: stateless` mode already carries (`spec/05_runtime-registry-and-pool-model.md:482`) is retained as the service-mode per-pod capacity, since `sessionPolicy` and its `maxConcurrentSessions` are session-mode only. Service mode therefore derives `mode_factor = maxConcurrent` and `burst_mode_factor = maxConcurrent`, and the PoolScalingController's saturation signal stays `active_slots / (pod_count × maxConcurrent)` (`spec/05_runtime-registry-and-pool-model.md:573`). Three cross-tenant controls carry over and remain distinct, each re-keyed off the removed `taskPolicy.allowCrossTenantReuse` field and the removed `task` mode onto `sessionPolicy.recycle.allowCrossTenantReuse`, because their current spec text and enforcement code key off the field and the mode this proposal removes. The T4 cross-tenant prohibition (`spec/05:396`) is enforced at two pool-admission sites, both re-keyed to `recycle.allowCrossTenantReuse`: the mode-keyed `decideTaskMode` branch (`pkg/admission/pool_config_validator/validator.go:545`) and the mode-independent `ValidateCrossTenantReuseTier` (`pkg/gateway/poolstore/poolstore.go:667`), which reads the top-level `Pool.AllowCrossTenantReuse` field (poolstore.go:84) the proposal relocates to `sessionPolicy.recycle.allowCrossTenantReuse` and runs unconditionally at the gateway's three pool-admission call sites (`pkg/gateway/admin/pools.go:606`, `pkg/gateway/admin/pools.go:1447`, `pkg/gateway/admin/bootstrap_resources.go:62`) for any pool whose `AllowCrossTenantReuse && runtimeTier.IsT4()`, with no execution-mode gate. Re-keying the `decideTaskMode` branch alone would leave `ValidateCrossTenantReuseTier` reading a field the proposal deletes, silently disabling this fail-closed gate at the gateway pool-admission layer on the surviving sequential-reuse path (`maxConcurrentSessions: 1`, `recycle.enabled: true`, `recycle.allowCrossTenantReuse: true`) and dropping one of the two independent enforcement layers spec/05:389 requires, so both enforcers re-key onto the relocated field in the same change. The prohibition applies to the sequential-reuse successor of task mode: `recycle.allowCrossTenantReuse: true` is rejected on any pool whose Runtime is `workspaceTier: T4` regardless of isolation profile, and the gateway rejects a T4 session that would route to a sequential-reuse pod (`maxConcurrentSessions: 1`, `recycle.enabled: true`) already bound to a different tenant, the assignment-time enforcement spec/05:396 names for the "task-mode pod" today. The microvm gate on `allowCrossTenantReuse` (`spec/05:394`, enforced at `pkg/gateway/poolstore/poolstore.go:623` in `ValidateTaskPolicy` and `pkg/admission/pool_config_validator/validator.go:534` in `decideTaskMode`) is re-keyed to `recycle.allowCrossTenantReuse` on the same sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`), where a VM restart separates tenants over time: `recycle.allowCrossTenantReuse: true` requires `isolationProfile: microvm`. Both branches are reached today only for `executionMode: task` (`ValidateTaskPolicy` returns early when the mode is not `task` at poolstore.go:605; `decideTaskMode` is invoked only for `case "task"` at validator.go:431), so the mode collapse re-keys their predicates onto the `sessionPolicy.recycle` validation path rather than leaving them as dead branches with no successor. The categorical concurrent-mode prohibition also carries over and is separate from the microvm gate: cross-tenant slot sharing is never permitted when `maxConcurrentSessions > 1`, regardless of `isolationProfile` or `scrubProfile`, because simultaneous process-level cotenancy has no isolation boundary (the current `spec/05:498` rule for concurrent-workspace mode, which the proposal renames to `maxConcurrentSessions > 1`). The pool controller rejects `recycle.allowCrossTenantReuse: true` on any pool where `maxConcurrentSessions > 1`. `acknowledgeProcessLevelIsolation` (required when concurrency exceeds one) is a same-tenant acknowledgment for a tenant's own concurrent slots and does not gate cross-tenant sharing, so this categorical rejection is the only control that closes the simultaneous-cotenancy gap and the mode collapse keeps it.

The scrub model becomes uniform: per-slot cleanup on every session release (the current concurrent-workspace behavior), plus a whole-pod scrub whenever occupancy reaches zero on a recycling pod before it returns to `idle`. The whole-pod scrub on the recycle edge closes the current concurrent-workspace gap in which shared `/tmp`, `/dev/shm`, and surviving processes persist across cohorts. A pod recycles only after clean session termination and a successful scrub; a failed or crashed session retires the pod through the drain path. On a recycling preConnect pool the whole-pod scrub runs while the claim is `recycling` and the pod projects `claimed`, and the SDK re-warm runs only after the scrub reports successful, projecting `sdk_connecting` for the re-warm leg alone, before the claim enters `reserved` (Sections 3.2 and 3.3), so the `sdkConnectTimeoutSeconds` watchdog bounds only the re-warm rather than the scrub, every pod that reaches `reserved` or `idle` is SDK-warm, and the §6.1 invariant is preserved.

`maxClientIdleSeconds` terminates a session after continuous client inactivity. It replaces the pool-level `runtime.limits.maxIdleTimeSeconds` knob and the §6.2 agent-inactivity timer (`spec/06_warm-pod-model.md:273-290`, default 600s per `spec/11_policy-and-controls.md:199`); a single clock with a single pause table survives, so the existing per-state table and the §9.1 elicitation pause rule (`spec/09_mcp-integration.md:102`) are rewritten rather than duplicated. Activity is any client-originated interaction (a message, a tool approval or denial, an elicitation response, or an attach), and agent work also resets the clock, so an autonomously working session or an `await_children` block is never idle-terminated. The clock has its own pause table rather than sharing the `maxSessionAge` pause table: it is paused during `suspended` (a suspended session may otherwise persist indefinitely by design, §8) and does not run during `finalizing`. It is also paused during the recovery states `resume_pending` and `resuming`, where the session cannot make progress and is not awaiting a present client, matching the existing `maxIdleTimeSeconds` pause during `resume_pending` (`spec/06_warm-pod-model.md:292`) that the §6.2 re-key carries forward. Unlike the current timer, it runs during `input_required`, `awaiting_client_action`, and elicitation waits, because waiting on an absent client is the condition the bound exists to reclaim. The default is the pool's effective `maxSessionAgeSeconds`, which raises the platform idle bound to the age cap; this raises the platform default from 600s, a deliberate relaxation recorded in Section 2 and accepted because the age cap still bounds abandoned sessions. The bound binds before the age cap when a deployer lowers it or when the session accrues idle time in a state where `maxSessionAge` is paused but this clock runs: `maxSessionAge` is paused during `awaiting_client_action` (`spec/06_warm-pod-model.md:268`) while this clock runs there, so a session that reaches `awaiting_client_action` (for example after a `resume_pending` wall-clock cap of `maxResumeWindowSeconds`, default 900s, `spec/06_warm-pod-model.md:266,292`) accrues idle time with no age accrual and the default-equal bound can fire there before the age cap. During `input_required` both clocks run (`spec/06_warm-pod-model.md:263`), so they stay aligned there. The idle clock is the intended backstop for exactly the abandoned-client condition, so this binding is the wanted behavior. The effective value lands on the existing per-session `SessionTimeouts.MaxIdleSeconds` field through the existing most-restrictive resolution (`pkg/gateway/sessionidle`), and the §27.6 playground override (`min` with `playground.maxIdleTimeSeconds`, `spec/27_web-playground.md:201`) continues to apply. Expiry reuses the existing `max_idle_time` reason on the `dev.lenny.session_expired` event (`spec/14_workspace-plan-schema.md:146`), which is already distinguishable from `max_session_age`.

`onPoolExhausted` parameterizes the existing §4.6.1 per-pool claim queue rather than adding a second one. The current behavior is already a bounded wait: the gateway queues claim requests up to `podClaimQueueTimeout` (default 60s, `spec/04_system-components.md:404`), runs the Postgres fallback claim, and returns `WARM_POOL_EXHAUSTED` only after exhausting both paths (`spec/15_external-api-surface.md:1017`). `reject` keeps that behavior. `queue` keeps the request in the same FIFO for up to `maxQueueWaitSeconds` after the claim-path timeout and the Postgres fallback are exhausted, re-entering acquisition as pods free; a queued request holds no pod, slot, or claim, and the §7.1 atomicity contract is preserved (the client receives a `session_id` only on success). On timeout the gateway returns `WARM_POOL_EXHAUSTED` with a `Retry-After` header as today. The queue is scoped to session mode. The existing `lenny_pod_claim_queue_depth`, `lenny_pod_claim_queue_wait_seconds`, and `lenny_pod_claim_timeout_total` series and the `PodClaimQueueSaturated` alert continue to measure this single queue; no new queue series is added.

### 3.2 Per-pod occupancy claims

The `SandboxClaim` is created when the gateway acquires an idle pod and deleted when the reserved hold below expires or the WarmPoolController completes the pod's termination. Its name is the deterministic `claim-<podName>`, so the CREATE race resolves pod acquisition between gateway replicas exactly as the per-session CREATE resolves it today, and the `lenny-sandboxclaim-guard` webhook's single-claim rule simplifies to per-pod uniqueness with no slot exemption. Intra-pod capacity is gated by the atomic Redis Lua counter. The `Counter == nil` fallback paths still exist in the shipped tree (`pkg/gateway/podclaim/slotclaimer.go:553` and `:562`) because the prior 0002 was never applied; their deletion is carried forward as an edit of this proposal. The counter follows the §12.4 posture that every Redis-backed role has a durable fallback (`spec/12_storage-architecture.md:199`): during a Redis outage the gateway gates capacity on the Postgres `SessionStore.GetActiveSlotsByPod` check under a per-pod advisory lock (the same source the existing blocking rehydration reads, `spec/05_runtime-registry-and-pool-model.md:521`), failing closed only after a bounded outage window, so a Redis-only outage does not reject all session dispatch the way an unguarded mandatory increment would. The session-to-pod binding is recorded on the Postgres session row's `pod_assignment` column, which already exists (migration 0050, indexed by migration 0080; `SessionStore.PodAssignment`); per-session idempotency is anchored by the session identifier rather than by a claim name. The §5.2 prose that names the rehydration index column `sessions(pod_name)` is corrected to `pod_assignment` in the staged spec/05 edits, so the spec, the migrations, and the session store agree on one column name.

Claim traffic therefore scales with pod-occupancy episodes rather than with sessions, and the reserved hold extends episodes across idle gaps. When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`, coordinates the whole-pod scrub and, on preConnect pools, the SDK re-warm, then patches the binding state to `reserved` rather than deleting the claim; the recycle path is `claimed → sdk_connecting (preConnect pools) → reserved → idle`, and a pod that reaches `reserved` is always scrubbed and SDK-warm. At the `reserved` patch the gateway stamps `holdExpiresAt` (the reservation time plus the TTL) on the claim status, and the status records the time of each binding-state transition, so the hold deadline and the binding age are visible to the WarmPoolController without access to gateway configuration. A new session of the same tenant arriving within the hold window is dispatched onto the pod with a `reserved` to `bound` patch and no acquisition round trip; any gateway replica may rebind, which preserves the hold's benefit under multi-replica deployments. If the hold TTL expires first, the holder deletes the claim and the pod returns to `idle` with no second re-warm. Every hold-expiry DELETE (the gateway TTL holder and the WarmPoolController orphan GC alike) carries Kubernetes preconditions: the claim UID and the resourceVersion observed when the claim entered `reserved`. A rebind patch that lands first changes the resourceVersion, so the DELETE fails its precondition and the expiry aborts; the rebinding replica re-reads the claim after its patch before dispatching. The TTL is a deployment-level gateway configuration value (`gateway.claimHoldTTLSeconds`, default 10) rather than a per-pool or per-tenant setting; configuration validation emits a warning when it is set high, because a long hold delays the pod's return to the pinned tenant's claimable idle inventory and delays retirement-limit evaluation at the next disposition. The `lenny.dev/tenant-id` pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated `allowCrossTenantReuse`, matching the lifetime pinning the current spec mandates (`spec/05_runtime-registry-and-pool-model.md:389`): a pinned idle pod is claimable only by its pinned tenant, the candidate scan filters on the pin label, and inventory accounting counts pinned-idle pods as idle inventory available to that tenant alone. A reserved pod is excluded from idle inventory entirely: the candidate scan skips it and the per-pod CREATE guard blocks acquisition by another replica. The orphan GC reclaims a reserved claim whose holding gateway crashed once `holdExpiresAt` plus a grace period has passed, using the same precondition-guarded DELETE; a claim in any other live binding state (`bound` or `recycling`) is evaluated against the binding-transition time plus the orphan timeout with no active session in Postgres and is reclaimed by draining the pod (Section 3.3), which both restores the persist-grace window that the current per-session GC derives from `metadata.creationTimestamp` (`pkg/controller/warmpool/gc.go:157`) and that a per-pod claim's creation time, marking the start of the whole occupancy episode, can no longer provide, and covers a coordinating-gateway crash during the `recycling` scrub wait, which leaves the claim in `recycling` with no `holdExpiresAt` so neither the reserved predicate nor the `sdk_connecting` watchdog would reach it. Because the claim is CREATEd with spec only and the binding state is set by a subsequent status patch (a Kubernetes status subresource cannot be written by the resource Create call, `charts/lenny/crds/lenny.dev_sandboxclaims.yaml:178-180`), a gateway crash in the window between the claim CREATE and its first binding-state patch leaves the claim with empty status (no binding state, no binding-transition time, no `holdExpiresAt`), which a status-only predicate cannot select; the orphan GC therefore retains a `metadata.creationTimestamp` fallback that reclaims by draining a claim whose binding state is unset, older than `claimOrphanTimeout` from its creation time, with no active session referencing the pod, so the CREATE-but-no-status-yet crash cannot strand the pod, matching the exact crash scenario the shipped GC's creation-timestamp key plus the `SessionActive` check exists to cover (spec/04:479) and keying the active-session check on the pod through the Postgres `pod_assignment` binding rather than on a per-session claim name. Under sustained same-tenant load the per-session claim CREATE and DELETE cost approaches zero, because one claim spans many sessions; the recycle path instead adds per-session `SandboxClaim.status` binding-state PATCHes (`bound → recycling → reserved → bound`), which the Sandbox-only `statusUpdateDeduplicationWindow` (spec/04:460) does not coalesce, so the §4.6.1 etcd write-pressure budget is revised for this profile rather than reduced uniformly (spec/04:431-439, Section 6). On a one-session-per-pod pool the episode and the session coincide and the recycle path is off, so nothing changes where the current model was already minimal.

The claim's spec carries `sandboxRef` and `tenantId`. `SlotID` and the per-session `SessionID` fields are removed. The claim's status carries the binding state, the time of its last binding-state transition, a `rewarmStartedAt` stamp the gateway records on a preConnect pool when a successful `ReportPodScrub` arrives and the SDK re-warm begins (which moves the projection from `claimed` to `sdk_connecting` and arms the re-warm watchdog clock, Sections 3.3 and 3.4), `holdExpiresAt` during the hold window, and the pod-level disposition: `bound` while at least one session is bound, `recycling` while the occupancy-zero whole-pod scrub runs (projecting `claimed`) and while the preConnect re-warm runs after the scrub reports successful (projecting `sdk_connecting`), `reserved` during the hold window, and the terminal `released` (limit-reached retirement) or `failed` (scrub-failure retirement under `onScrubFailure: fail` or a crashed session), which the occupancy projection consumes.

### 3.3 Ownership decomposition, carried forward

The following is retained from the prior 0002, re-derived for per-pod claims. The gateway stops writing `Sandbox.status` entirely (phase, slot count, tenant pin, and conditions); the `Terminated` and `Suspended` session conditions move to the Postgres session model (§7.2, §8.8); the WarmPoolController is the complete sole writer of `Sandbox.status`. The chart drops the gateway's `sandboxes/status` rule and the `patch` and `watch` verbs on the `sandboxes` main resource, and adds the `sandboxclaims/status` grant for the binding-disposition write. The §5.2 application-layer tenant pin reads the gateway-stamped agent-Pod `lenny.dev/tenant-id` label. `Sandbox.status.activeSlots` is dropped; the Redis counter, with its Postgres rehydration and the §12.4 outage fallback (Section 3.2), is the slot-count authority.

The WarmPoolController watches `SandboxClaim` and computes `Sandbox.status.phase` as a level-triggered projection of claim existence, claim disposition, and `sessionPolicy`:

- No claim, warm-inventory phase: `idle`.
- Claim present with binding state `bound`: `claimed`.
- Claim present with binding state `recycling`, whole-pod scrub not yet reported: `claimed` on both preConnect and non-preConnect pools. The scrub runs while the pod projects `claimed`, so the `sdkConnectTimeoutSeconds` watchdog (which fires only for the `sdk_connecting` projection, spec/06:69) does not run during the scrub. The scrub is instead bounded by the gateway-side missing-report timeout at the recycle boundary (`cleanupTimeoutSeconds` plus a grace, Section 3.4).
- Claim present with binding state `recycling`, whole-pod scrub reported successful, on a preConnect pool with the re-warm in progress: `sdk_connecting` (the SDK re-warm alone, bounded by the `sdkConnectTimeoutSeconds` watchdog measured from the re-warm start, later in this section). On a non-preConnect pool a successfully scrubbed `recycling` claim projects directly to `reserved` (the scrub completes inside the occupancy episode and there is no re-warm leg).
- Claim present with binding state `reserved`: `reserved` (scrubbed, SDK-warm on preConnect pools, held for the tenant, excluded from idle inventory).
- Claim deleted on a recycling pod under its limits (hold expiry): `idle`. The scrub and re-warm completed before the claim entered `reserved` (Section 3.2), so this edge is a pure claim-deletion projection with no second re-warm and no `sdk_connecting` leg.
- Claim disposition `released` or `failed`, or claim deleted on a non-recycling pod: `draining`, then `terminated`.
- Uptime drains derive from the pod `CreationTimestamp` against `recycle.maxPodUptimeSeconds`; the unhealthy-threshold drain derives from the gateway-stamped `lenny.dev/drain-request` Pod annotation. Both are WarmPoolController-written, as in the prior 0002.

The current §6.2 one-session-only invariant becomes the `recycle.enabled: false` configuration; the projection emits `idle` from `claimed` only for recycling pools. The orphan GC becomes binding-state-aware rather than mode-aware, and it retains the shipped GC's phase-agnostic coverage so a coordinating-gateway crash cannot strand a pod in any live binding state. The predicate is keyed to the binding state recorded on the claim status. An orphaned claim in any non-terminal binding state other than `reserved` (`bound` or `recycling`) with no active session in Postgres once the binding-transition time plus the orphan timeout has passed (Section 3.2) drains the pod regardless of recycle settings. This covers the `recycling` window explicitly: when occupancy reaches zero on a recycling pool the gateway patches the claim to `recycling` and awaits the adapter's `ReportPodScrub` under a gateway-side timeout (Section 3.4), and if the coordinating gateway replica crashes during that window the claim is left in `recycling` with no `holdExpiresAt` (which is stamped only at the `reserved` patch, Section 3.2) and no `rewarmStartedAt` stamp (which is recorded only on a successful scrub report, Section 3.4), so the pod projects `claimed` on both pool kinds during the scrub wait (Section 3.3), and neither the `reserved` predicate nor the `sdk_connecting` watchdog (`spec/06_warm-pod-model.md:69`, which fires only for the `sdk_connecting` projection) would reclaim it; the binding-transition-time predicate over the live states reclaims it by draining. Draining rather than returning to `idle` is fail-closed by necessity: the whole-pod scrub is adapter-executed and gateway-coordinated (Section 3.4), the WarmPoolController has no GatewayControl role and no network path to agent pods (the agent-pod ingress NetworkPolicy admits only the gateway control channel, `spec/13_security-model.md:56-78`), and returning an unscrubbed pod to `idle` would break the scrub-before-idle invariant of Section 3.1. The shipped per-session GC reclaims every orphaned claim regardless of phase (`pkg/controller/warmpool/gc.go:157` creation-time cutoff, `:166` the `SessionActive` check), and this live-states predicate restores that coverage for the per-pod claim, keyed to the binding-transition time rather than to the per-occupancy-episode creation timestamp, with one fallback for the window the binding-transition-time key cannot cover. The claim is CREATEd with spec only and its first binding state (`bound`) is written by a subsequent gateway status patch, because a Kubernetes status subresource is not writable by the resource Create call (`charts/lenny/crds/lenny.dev_sandboxclaims.yaml:178-180`); a gateway crash between the CREATE and that first patch leaves the claim with empty status, which the binding-state-keyed predicate cannot match (no binding state, no binding-transition time, no `holdExpiresAt`). The orphan GC therefore reclaims by draining a claim with an unset binding state whose `metadata.creationTimestamp` is older than `claimOrphanTimeout` and that no active session references, the same creation-timestamp-keyed reclamation the shipped GC runs for exactly this CREATE-but-no-session-persisted crash (spec/04:479), so a coordinating-gateway crash in the CREATE-before-status window cannot strand the pod. An orphaned reserved claim is reclaimed by precondition-guarded deletion after `holdExpiresAt` plus the grace period; the pod was scrubbed and re-warmed before entering `reserved`, so returning it to `idle` preserves the invariant.

The coarse phase enum carries `warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, and `terminated`. With one session mode the `slot_active` value collapses into `claimed` (resolved in Section 12); concurrency is observable through the Redis counter and metrics, and `reserved` carries the hold window. The PoolScalingController counts `reserved` pods as occupied for inventory purposes; this rule lands in the §4.6.2 PoolScalingController text (Section 6). The `lenny.dev/state` pod label keeps its deliberately minimal value set (`idle`, `active`, and `draining`, `spec/06_warm-pod-model.md:309`) and is derived solely from the projected coarse phase through `CoarseState(phase)` with no input from the claim binding state (`pkg/controller/sandbox/controller.go:483,500,515`). A `reserved` pod, a `recycling` pod on a non-preConnect pool, and a `recycling` preConnect pod whose whole-pod scrub has not yet reported are labeled `active`: the first projects to the `reserved` phase and the others to `claimed` (Section 3.3), both of which map to `active`. A `recycling` pod on a preConnect pool projects to `sdk_connecting` only during the SDK re-warm leg, after the whole-pod scrub has reported successful (Section 3.3), and `CoarseState(sdk_connecting)` returns no coarse value (`pkg/sandbox/state/state.go:59-71`), so the pod carries no `lenny.dev/state` label during that re-warm window, exactly as a warm-phase `sdk_connecting` pod and the existing task-mode inter-task re-warm pod (`spec/06_warm-pod-model.md:154`) carry no label. This re-warm window is bounded by the `sdkConnectTimeoutSeconds` watchdog and is not unclaimed warm inventory; it is unlabeled because the `sdk_connecting` phase is shared with the warm-phase pre-connect state and the phase alone cannot distinguish an occupied recycling pod from unclaimed pre-idle inventory. The whole-pod scrub runs in the `claimed` projection rather than the `sdk_connecting` projection, so the `sdkConnectTimeoutSeconds` watchdog never runs during the scrub; the scrub is bounded by the gateway-side missing-report timeout at the recycle boundary (`cleanupTimeoutSeconds` plus a grace, Section 3.4), exactly as the current model runs the whole-pod scrub in a dedicated `task_cleanup` phase and transitions `task_cleanup → sdk_connecting` only after the scrub succeeds (`spec/06_warm-pod-model.md:147-154`). The watchdog must measure only the SDK-connect phase rather than the pod's total Running time, which the shipped watchdog clock does not: the shipped clock is `now − pod.Status.StartTime` (`pkg/controller/sandbox/sdkwarm.go:105,114-122`), and `TimedOut()` fires whenever the pod is `sdk_connecting`, NotReady, and that elapsed time exceeds the budget (`pkg/controller/sandbox/lifecycle/lifecycle.go:138-142,186-191`). On the recycle re-warm edge the pod has already been Running for the whole occupancy episode (up to `recycle.maxPodUptimeSeconds`), so `now − pod.Status.StartTime` is far past the 60s budget the instant the pod projects `sdk_connecting` and the readiness gate holds it NotReady during the re-warm, which would retire every recycling preConnect pool to `failed` before it could reach `reserved`. Anchoring the clock at the `bound → recycling` patch does not prevent this either, because the whole-pod scrub (`cleanupTimeoutSeconds`, up to 60s) runs inside that window before the re-warm begins, so scrub plus re-warm together can exceed the 60s `sdkConnectTimeoutSeconds` budget that was sized for the re-warm alone. The proposal therefore keeps the whole-pod scrub in the `claimed` projection and arms the watchdog clock at the start of the SDK re-warm rather than at the `bound → recycling` patch: the gateway records a second binding-state-transition stamp on `SandboxClaim.status` (the re-warm-start time) when it receives a successful `ReportPodScrub` on a preConnect pool and begins coordinating the re-warm (Section 3.4), the projection enters `sdk_connecting` at that stamp, the WarmPoolController arms the clock from it, and the clock stops when the projection reaches `reserved`, so only the SDK re-warm is measured against `sdkConnectTimeoutSeconds` and the scrub time is excluded. A preConnect recycle whose re-warm completes within the budget reaches `reserved` and is not retired to `failed`. The warm-fill path that legitimately measures from pod start (`SandboxStatus` carries no per-phase-entry timestamp, only `Conditions[].lastTransitionTime`) keeps the pod-start anchor; only the recycle re-warm edge, identified by the `recycling` claim with a recorded re-warm-start stamp, uses the re-warm-start anchor. The §6.1 watchdog paragraph (spec/06:69) is restated to admit `reserved` as a non-failure terminus alongside `idle` and to describe the re-warm-start-relative clock with this anchor, and the implementation (`pkg/controller/sandbox` and `pkg/controller/sandbox/lifecycle`) gains both the `sdk_connecting → reserved` non-failure exit and the recycle-edge clock re-anchoring to the re-warm-start stamp (Sections 6 and 13), so the watchdog's success terminus is consistent with the recycle path's `reserved` terminus and the budget measures only the SDK re-warm rather than the scrub or the prior occupancy episode. The §4.6.1 warm-pod PDB selector (`lenny.dev/state: idle`, `spec/04_system-components.md:475`) therefore matches neither a `reserved` pod, a `recycling`-on-non-preConnect pod (labeled `active`), a preConnect-recycling pod during its scrub (labeled `active`), nor a preConnect-recycling pod during its re-warm leg (unlabeled), so none has voluntary-disruption protection during the recycle and hold window. This is accepted because both the recycle scrub and the hold are optimizations: a voluntary eviction during either deletes the claim through the normal pod-termination path and the next same-tenant session falls back to normal acquisition. Widening the label vocabulary was rejected because every NetworkPolicy and monitoring selector keyed to the existing values would need auditing for a label value that duplicates the CRD phase. The `pkg/sandbox/state` CoarseState mapping gains the `reserved → active` case; `sdk_connecting` stays unmapped, so its label removal is unchanged. The fine session phases remain solely in the Postgres session model, and the spec locations the prior 0002 identified as calling them authoritative are corrected the same way.

### 3.4 The recycle disposition

The scrub reporting matches the two-scrub split, on two RPCs on the existing adapter-to-gateway `GatewayControl` service. Both RPCs are additions to the proto: the shipped service carries only `ExtendLease` and the platform-tool and connector RPCs (`schemas/lenny-adapter.proto:209-263`), and the prior 0002's per-task `ReportTaskCleanup`, which these RPCs replace at the design level, was never applied to the tree. At each session release the adapter runs the per-slot cleanup and reports its outcome (`released` or `leaked`) via `ReportSessionScrub`; the gateway increments `sessionsServed` on the pod's `agent_pod_state` Postgres row, and leaks feed the unhealthy-threshold ledger behind the `lenny.dev/drain-request` annotation. When occupancy reaches zero on a recycling pool the gateway patches the claim to `recycling`, the adapter runs the whole-pod scrub and reports its binary outcome via `ReportPodScrub`, and the gateway increments `scrubFailureCount` on failure and computes the disposition with the shipped decision function (`pkg/sandbox/taskcleanup.Decide`, inputs re-sourced) against `sessionPolicy` and the pod uptime:

- Recycle: on preConnect pools, on a successful `ReportPodScrub` record the re-warm-start binding-state-transition stamp on `SandboxClaim.status` and coordinate the SDK re-warm (the claim stays `recycling`, the pod projects `sdk_connecting` for the re-warm leg, and the WarmPoolController arms the `sdkConnectTimeoutSeconds` watchdog from that stamp so the scrub time is excluded from the budget, Section 3.3); then patch the claim to `reserved`, stamp `holdExpiresAt`, and start the hold TTL (or dispatch a queued same-tenant session directly, returning the claim to `bound`). On non-preConnect pools a successful `ReportPodScrub` patches the claim directly to `reserved` with no re-warm leg. No `Sandbox.status` write occurs; the projection observes the claim.
- Retire (`maxSessionsPerPod`, `maxScrubFailures`, or `maxPodUptimeSeconds` reached, `onScrubFailure: fail`, a failed session, or an unschedulable host node): record the disposition on `SandboxClaim.status.phase`; the projection drains and terminates the pod.

A missing scrub report is bounded by a gateway-side timeout at session end (`cleanupTimeoutSeconds` plus a grace), after which the pod is retired. Because the timeout arms at session termination, which the gateway already coordinates durably, the prior 0002's `task_dispatch_deadline` column and its coordinator-handoff re-arm machinery are unnecessary and are discarded. The gateway-side timeout does not survive a crash of the coordinating gateway replica during the `recycling` scrub wait; that case is the WarmPoolController orphan GC's responsibility, which reclaims a claim left in `recycling` with no active session by draining the pod (Sections 3.2 and 3.3), so a coordinator crash does not strand the pod even though the gateway-side timeout is lost.

### 3.5 Session and task

Phase 1, in this proposal: the spec defines in one place that a session is the client-facing unit of execution, each session has exactly one execution, and "Task" is the name external protocols and the delegation API give to that unit (an MCP or A2A Task is the protocol surface of a session; `lenny/delegate_task` creates a child session). `TaskID` is frozen equal to the root task identifier in the SDK `CreateRequest`, the adapter manifest, and checkpoint keys. The `task_complete`, `task_complete_acknowledged`, and `task_ready` lifecycle frames are deleted from §4.7, §15.4.1, `schemas/lifecycle-events.schema.json`, and `schemas/lenny-adapter-jsonl.schema.json` (which defines the same-named envelopes as the separate adapter-to-gateway control surface, `schemas/lenny-adapter-jsonl.schema.json:19-21` and `:228-263`), together with their conformance examples (`schemas/examples/jsonl.task_complete.json`, `jsonl.task_complete_acknowledged.json`, `jsonl.task_ready.json`, and `lifecycle.task_complete.json`). The tier-0 static schema-example test `tests/tier0_static/schemas_test.go` hardcodes those four example paths in two slices and reads-and-validates each (`lifecycle.task_complete.json` in the `TestLifecycleEventExamplesValidate` slice at schemas_test.go:157, and `jsonl.task_complete.json`, `jsonl.task_complete_acknowledged.json`, and `jsonl.task_ready.json` in the `TestAdapterJSONLExamplesValidate` slice at schemas_test.go:189-191), so the four entries are removed from those slices in the same change that deletes the example files and removes the frame `$defs` from the two schemas; otherwise the test fails with file-not-found for the deleted paths (and would fail the expect-valid assertion even if the files were retained, since the schema no longer defines the removed types), and the tier-0 static suite does not run green. This is the same hardcoded-mirror edit-site class Pass 15 named for `catalog_test.go`'s `spec161Metrics` transcription, applied to the schema-example slices. The §4.7 frame definitions are in the lifecycle-channel message table at `spec/04_system-components.md:678-711` (the `task_complete` row at spec/04:701, the `task_ready` row at spec/04:702, the `task_complete_acknowledged` row at spec/04:708, the diagram lines at spec/04:678-685, the `terminate`-row cross-reference to `task_complete` at spec/04:700, and the "Regenerated per task" manifest prose at spec/04:711), which the §4.7 directive (Section 6) re-anchors. The same deletion removes the `task_lifecycle` capability value that gates these frames: the `lifecycle_capabilities` row prose at spec/04:694 (which states `"task_lifecycle"` "is offered only on task-mode pods and governs the `task_complete` / `task_complete_acknowledged` / `task_ready` message exchange") drops the `task_lifecycle` value from its `capabilities` list, and the `task_lifecycle` enum value and its task-mode description are removed from both the `lifecycle_capabilities` and `lifecycle_support` capability arrays in `schemas/lifecycle-events.schema.json` (the description at `:28`, the `lifecycle_capabilities.capabilities` enum value at `:41`, and the `lifecycle_support.capabilities` enum value at `:64`), so the capability disappears together with the frames it governs. The per-task manifest and `mcpNonce` regeneration becomes per-session. `schemas/lenny-adapter.proto` defines no lifecycle frames (the lifecycle stream carries opaque JSON envelopes) and is touched only for the new `GatewayControl` RPCs and the task-mode comment and `task_id` field cleanup. The §15.4.1 `slotId` protocol references and the §15.4.5 execution-modes list still name the removed `concurrent-workspace mode` and `session and task mode`; they are re-keyed to `maxConcurrentSessions > 1` and to the `session` and `service` modes respectively (Section 6), consistent with the parallel spec/06:170-177 and spec/10:54,141 slot_id re-keys, so no §15 surface keys `slotId` to a mode name the proposal removes. The authored JSONL conformance source mirrors that §15.4.1 wire description, so its surviving `slotId` field description "Present only on pods in concurrent-workspace mode" (`schemas/lenny-adapter-jsonl.schema.json:63`) is re-keyed to `maxConcurrentSessions > 1` in the same change (Section 5), so the schema and the rewritten §15.4.1 text agree on the mode name. The integration-level matrix rows for task-mode reuse are removed; recycling requires no runtime cooperation and works at every integration level, which removes the Full-level-only restriction task mode carried.

Phase 2, out of scope here: the lexical sweep of internal "task" names in Go identifiers, file paths, and proto field names (`TaskRecord` and §8.8 naming, `pkg/gateway/taskdriver`, and `pkg/sandbox/taskcleanup`). No `lenny_task_*` Prometheus series survives Phase 1: the existing series are all renamed by this proposal (Section 4).

### 3.6 Service mode

Service mode is the current `concurrencyStyle: stateless`, renamed, with the claim divergence fixed: the gateway routes each message to a ready tenant-pinned pod through the Service's `EndpointSlice` with the in-memory tenant-affinity map, creates no `SandboxClaim`, and materializes no workspace, as §5.2 already describes. The mode keeps the pool-level `maxConcurrent` field as its per-pod slot bound, which the readiness-driven routing already depends on: a pod at slot capacity reports its readiness probe `false` and the gateway selects a new unpinned pod (`spec/05_runtime-registry-and-pool-model.md:500`). `sessionPolicy` and its session-only `maxConcurrentSessions` do not apply to service mode, so retaining `maxConcurrent` is what keeps service-mode routing and scaling (Section 3.1) operative. Service mode pins each pod to a single tenant using the same two-layer mechanism §5.2 describes (the gateway records `tenantId` on first request and rejects a mismatched `tenantId`, and the `lenny-tenant-label-immutability` webhook locks the `lenny.dev/tenant-id` label), and because the relocated `allowCrossTenantReuse` field lives under `sessionPolicy.recycle` (session mode only), service mode has no cross-tenant-reuse field at all, so cross-tenant reuse is unrepresentable and structurally prohibited rather than rejected at validation time on a removed field; the staged §5.2 edit rewrites the current "Tenant isolation (concurrent-stateless)" paragraph (spec/05:502) accordingly (Section 6). Sessions remain the API surface (`POST /v1/sessions`, `lenny/create_session`, `send_message`); a service-mode session is a connection handle. `multi_turn` runtimes are permitted: a session as a handle with self-contained messages reduces client overhead, and the failure mode is usability rather than safety. Three contract mechanisms accompany this: the spec and docs state that service mode provides no cross-message continuity and that clients of multi-turn runtimes re-inject context into each message's `input`; `sessionIsolationLevel` gains `conversationContinuity: "platform" | "none"` (`"none"` for service mode); and runtime registration emits a warning event when a `multi_turn` runtime is bound to a service-mode pool. Service-mode pools report `podReuse: true`, `residualStateWarning: true`, and `scrubPolicy: "none"`: pods serve successive requests with no scrub and share process space, network stack, `/tmp`, and page cache across same-tenant concurrent requests, the rationale §7.1 records today for concurrent-stateless (`spec/07_session-lifecycle.md:73`), so the isolation-visibility contract does not regress when the mode is renamed.

## 4. Observability surface

- `sessionIsolationLevel` (§7.1): `executionMode` takes the values `session` and `service`; `podReuse` and `residualStateWarning` are true when `recycle.enabled` is true, when `maxConcurrentSessions > 1`, or when `executionMode` is `service` (Sections 3.1 and 3.6, carrying the §7.1 concurrent-stateless rationale into the rewritten table); `scrubPolicy` values carry over with `"none"` for service mode, its presence keyed to `podReuse: true` as today; `conversationContinuity` is added.
- Metrics: the task-mode metrics are renamed to `lenny_pod_session_reuse_count`, `lenny_pod_scrub_failure_total`, `lenny_pod_scrub_failure_count`, and `lenny_pod_retirement_total`, and the `lenny_stateless_*` family to `lenny_service_*`, with no deprecated aliases. The renames are applied at every emitter and consumer in the same change: the PoolScalingController's demand-source PromQL over `lenny_stateless_requests_total` and `lenny_stateless_concurrent_active` (`pkg/controller/poolscaling/statelessdemandsource.go:46-74`) and its `mode_factor` derivation from the reuse histogram (`pkg/controller/poolscaling/controller.go:255`), the gateway emitters (`pkg/gateway/gatewaymetrics`, `pkg/gateway/tenantaffinity`, `pkg/gateway/statelessproxy`), and the §16.1 typed catalog (`pkg/observability/metrics/catalog.go`). New series: a reserved-pod gauge and an idle-termination counter keyed to the existing `max_idle_time` reason. The existing `lenny_pod_claim_queue_*` series continue to measure the single claim queue (Section 3.1); no queue series is added. The slot metrics read the Redis counter, as in the prior 0002.
- Conditions: the condition surface is the prior 0002's (pool health on `SandboxTemplate`, occupancy on `Sandbox`, both WarmPoolController-written); no new condition is added.
- The §16 inventories, the alert catalog, and `docs/reference/metrics.md` follow the renames.

## 5. CRD, schema, and RBAC changes

- `SandboxClaim`: deterministic per-pod name, spec reduced to `sandboxRef` and `tenantId`, `SlotID` removed, and status carrying the binding state and disposition (`bound`, `recycling`, `reserved`, `released`, and `failed`), the binding-state transition time, the `rewarmStartedAt` stamp (recorded on a preConnect pool when a successful `ReportPodScrub` arrives and the SDK re-warm begins, which moves the projection from `claimed` to `sdk_connecting` and arms the re-warm watchdog clock, Sections 3.3 and 3.4), and `holdExpiresAt`. The guard webhook validates per-pod uniqueness and drops the slot exemption. Its CREATE rule rejects a second non-terminal claim for the same `Sandbox`. The shipped per-session PATCH/PUT rule that reads the referenced `Sandbox.status.phase` is dropped rather than re-keyed: the per-pod claim spec is immutable after CREATE, and the binding-state transitions (initial empty `→ bound`, `bound → recycling`, `recycling → reserved`, the `reserved → bound` rebind, and the terminal `→ released` or `→ failed`) are writes to the `SandboxClaim.status` subresource, which the admission webhook does not gate. A `Sandbox.status.phase` accept-set cannot serialize those writes anyway, because the occupancy projection sets the Sandbox to `claimed` from the claim's `bound` binding state (Section 3.3), so the gateway's first `bound` status patch lands while the referenced Sandbox is still `idle` (nothing has projected `claimed` yet); an accept-set requiring a live-claim phase would reject the very write that establishes `bound`. Binding-state writes are instead serialized by the optimistic-concurrency UID and `resourceVersion` preconditions Section 3.2 specifies for the rebind-versus-hold-expiry race. The two generated `SandboxClaim` CRD manifests (`charts/lenny/crds/lenny.dev_sandboxclaims.yaml` and the `pkg/embedded/crds/` copy) regenerate in the same change via `make generate`: the schema drops the `slotId` field (charts/lenny/crds/lenny.dev_sandboxclaims.yaml:74) and the `sessionId` field with its `required: sessionId` entry (the `required` list at :86-88) and the `.spec.sessionId` printColumn (:25), the `status.phase` enum changes from `bound; active; released; failed` (:168-172) to `bound; recycling; reserved; released; failed`, and the `holdExpiresAt`, `rewarmStartedAt`, and binding-state-transition-time status fields are added, because the API server otherwise enforces the stale generated schema and rejects a per-pod claim created without `sessionId` and a `phase: reserved` or `phase: recycling` status patch.
- `Sandbox`: `spec.executionMode` enum becomes `session | service`; `status.activeSlots` is dropped; the coarse phase enum loses `slot_active` and the task states and gains `reserved`.
- `SandboxTemplate` and the pool definition: `taskPolicy` and `concurrentWorkspacePolicy` are replaced by `sessionPolicy`; validation rules re-key to derived properties (acknowledgments, including the `acknowledgeMicrovmResidualState` rejection when `scrubProfile: in-place` carried forward from `pkg/admission/pool_config_validator/validator.go:551-556` and re-anchored to the new field, the microvm cross-tenant gate (`recycle.allowCrossTenantReuse: true` requires `isolationProfile: microvm`) carried forward from the `decideTaskMode` rejection at `pkg/admission/pool_config_validator/validator.go:534` and the `ValidateTaskPolicy` rejection at `pkg/gateway/poolstore/poolstore.go:623` and re-keyed onto `sessionPolicy.recycle.allowCrossTenantReuse`, the T4 cross-tenant reuse prohibition carried forward from the `decideTaskMode` rejection at `pkg/admission/pool_config_validator/validator.go:545` (reached today only for `executionMode: task`, re-keyed onto the `sessionPolicy.recycle` path rather than being dropped with the mode) and from the mode-independent `ValidateCrossTenantReuseTier` at `pkg/gateway/poolstore/poolstore.go:667`, which the gateway runs unconditionally at its three pool-admission call sites (`pkg/gateway/admin/pools.go:606`, `pkg/gateway/admin/pools.go:1447`, `pkg/gateway/admin/bootstrap_resources.go:62`) and which reads the top-level `Pool.AllowCrossTenantReuse` field the proposal relocates, so its `p.AllowCrossTenantReuse` read re-keys to `recycle.allowCrossTenantReuse` rather than referencing a deleted field; both enforcers reject `recycle.allowCrossTenantReuse: true` on a `workspaceTier: T4` pool regardless of isolation profile, the `cleanupTimeoutSeconds / maxConcurrentSessions` floor, `maxSessionsPerPod` required when recycling, the CAP_NET_RAW rejection when `maxConcurrentSessions > 1`, the categorical rejection of `recycle.allowCrossTenantReuse: true` when `maxConcurrentSessions > 1` regardless of isolation profile, carried forward from the current concurrent-workspace prohibition at `pkg/gateway/poolstore/poolstore.go:503-506` (`ValidateConcurrentConfig`) and `pkg/admission/pool_config_validator/validator.go:579-581` (`decideConcurrentWorkspace`) and re-keyed to the derived `maxConcurrentSessions > 1` predicate, kept distinct from the sequential-reuse microvm gate, `capabilities.preConnect: true` admitted only when `maxConcurrentSessions` is 1 and rejected for service-mode pools, and the termination-grace formula). The gateway-side typed homes change with the spec: `pkg/gateway/runtimestore` renames the closed `ExecutionMode` enum from `session | task | concurrent` to `session | service` and updates `AllExecutionModes()` (runtimestore.go:1074-1084, the field at :44); `pkg/gateway/poolstore` removes the `ConcurrencyStyle` typed enum and its closed-set validators (poolstore.go:380, 387, 392, 395-403), replaces the `TaskPolicy` struct (poolstore.go:268) and the concurrent-workspace fields on `Pool` (`AcknowledgeProcessLevelIsolation`, `ConcurrentMaxPodUptimeSeconds`, `AllowCrossTenantReuse`, poolstore.go:50-84) with the `sessionPolicy` mirror, carrying the `MicrovmScrubMode` (renamed to `scrubProfile`) and the `AcknowledgeMicrovmResidualState` field (poolstore.go:279, 284) and its `in-place`-gated rejection (poolstore.go:626-631) forward onto the mirror rather than deleting them with the `TaskPolicy` struct, re-keys the `ValidateConcurrentConfig` categorical cross-tenant rejection (poolstore.go:503-506, "allowCrossTenantReuse is not permitted for concurrent-mode pools") from the `concurrencyStyle`/concurrent-mode predicate to `sessionPolicy.maxConcurrentSessions > 1` so the prohibition survives the mode collapse, retains `MaxConcurrent` (poolstore.go:52-55) as the service-mode per-pod slot bound (Sections 3.1 and 3.6; it is no longer a concurrent-workspace field, whose per-pod concurrency moves to `sessionPolicy.maxConcurrentSessions`), and re-keys the preConnect-versus-concurrency rejection in `ValidatePreConnectExecutionMode` (poolstore.go:686-696) from the `executionMode: concurrent` plus `concurrencyStyle` predicate to the derived rule (admit only when `maxConcurrentSessions == 1`; reject for service-mode pools). The CRD-side `ExecutionMode` field is a Go `string` in `pkg/apis/lenny/v1alpha1` (the `Runtime` field at runtime_types.go:39, the spec-declared primary v1 home of `executionMode` per spec/05:536, the `Sandbox` field at sandbox_types.go:47, and the `SandboxTemplate` field at sandboxtemplate_types.go:173-177), and each field carries a `+kubebuilder:validation:Enum=session;task;concurrent` marker (runtime_types.go:37, sandbox_types.go:45, and sandboxtemplate_types.go:175) that generates the closed OpenAPI enum the API server enforces at admission independently of the gateway's runtimestore validation. All three markers change from `session;task;concurrent` to `session;service`, and the six generated CRD manifests carrying that enum (`charts/lenny/crds/lenny.dev_runtimes.yaml`, `charts/lenny/crds/lenny.dev_sandboxes.yaml`, `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml`, and the `pkg/embedded/crds/` copies of all three) regenerate in the same change so the admitted value set matches the runtimestore typed enum. The Runtime CRD is a live, reconciled resource (`pkg/controller/runtime/controller.go`), and its `executionMode` enum at charts/lenny/crds/lenny.dev_runtimes.yaml:289-292 and pkg/embedded/crds/lenny.dev_runtimes.yaml:289-292 is enforced at API-server admission, so without this marker change the Runtime CRD would reject a `service`-mode Runtime while still admitting the removed `task` and `concurrent` values, leaving the runtimestore typed enum, the §5.2 mode set, and the `runtime_definitions` CHECK constraint (Pass 11) inconsistent with the Runtime CRD enum. The standalone CRD-side `ConcurrencyStyle` field on `SandboxTemplate` (sandboxtemplate_types.go:185, with its `+kubebuilder:validation:Enum=stateless;workspace` marker at sandboxtemplate_types.go:183 and doc comment) gates only the removed `concurrent` mode and is distinct from the `executionMode` marker and from both policy structs; it is removed alongside the marker change, and the generated `concurrencyStyle` schema block and its enum are dropped from the two `SandboxTemplate` manifests in the same `make generate` run, otherwise the CRD would advertise a `stateless;workspace` enum and a doc referencing a removed mode. The derived preConnect-versus-concurrency rejection itself lives in `pkg/gateway/poolstore` (`ValidatePreConnectExecutionMode`) rather than in `pkg/admission/pool_config_validator`.
- Postgres: `agent_pod_state` gains nullable `sessions_served` and `scrub_failure_count` columns (replacing the prior 0002's task-counter columns); no per-session deadline column. The sessions table is unchanged: the binding column `pod_assignment` already exists (migration 0050). A new append-only migration (the next free number after the current head 0166, e.g. `migrations/0167_runtime_definitions_execution_mode_service.up.sql`) drops the `runtime_definitions_execution_mode_check` constraint and re-adds it as `CHECK (execution_mode IN ('session', 'service'))`, because that constraint is a live, enforced enumeration of the old mode set (`migrations/0001_initial_schema.up.sql:56-57`, never altered by a later migration) and the gateway runtime store writes `execution_mode` into `runtime_definitions` with the literal mode string (`pkg/gateway/runtimestore/pgstore/pgstore.go:239` INSERT, `:311` UPSERT). Without the re-key the database rejects a `service`-mode runtime definition with a constraint violation and still permits the removed `task` and `concurrent` values. The same migration also re-keys the stale mode-enum comments on the unconstrained `execution_mode` columns (`migrations/0033_sandbox_warm_pools.up.sql:15`, `-- ... (session, task, concurrent)`, and `migrations/0084_sessions_isolation_level.up.sql:18`, `... when execution_mode is 'task' or 'concurrent'`) to the `session | service` set. The `concurrency_style` column on the pool table (`migrations/0040_warm_pool_concurrency.up.sql:13`, unconstrained `TEXT NOT NULL DEFAULT ''`) is retired by the same migration once `concurrencyStyle` is removed, leaving `max_concurrent` (the same migration) as the surviving per-pod bound consumed by service mode and the session-mode concurrency path.
- Proto and schemas: `ReportSessionScrub` and `ReportPodScrub` added to the `GatewayControl` service (additions against the shipped proto, Section 3.4); the between-task lifecycle frames removed from `schemas/lifecycle-events.schema.json` and `schemas/lenny-adapter-jsonl.schema.json` together with their `schemas/examples/` instances and the four hardcoded example-path entries that reference them in `tests/tier0_static/schemas_test.go` (the `lifecycle.task_complete.json` entry in the `TestLifecycleEventExamplesValidate` slice at schemas_test.go:157 and the `jsonl.task_complete.json`/`jsonl.task_complete_acknowledged.json`/`jsonl.task_ready.json` entries in the `TestAdapterJSONLExamplesValidate` slice at schemas_test.go:189-191), removed in the same change so the tier-0 static schema-validation suite stays green, the surviving `slotId` field description in `schemas/lenny-adapter-jsonl.schema.json` re-keyed from "Present only on pods in concurrent-workspace mode" (schemas/lenny-adapter-jsonl.schema.json:63, the `messageEnvelope` `slotId`) to `maxConcurrentSessions > 1` so the authored JSONL conformance source matches the spec/15:1818/1846/1879/1903 `slotId` re-keys (the other `slotId` fields at lines 142, 164, and 193 carry no mode-named description and are unchanged), and the `task_lifecycle` capability value removed from the `lifecycle_capabilities` and `lifecycle_support` arrays in `schemas/lifecycle-events.schema.json` (lines 28, 41, 64) so the capability does not outlive the frames it governs (Section 3.5); `schemas/lenny-adapter.proto` touched only for the new RPCs and the task-mode comment and `task_id` field cleanup (Section 3.5). The hand-authored gateway OpenAPI document `pkg/gateway/openapi/openapi.json` (embedded by `pkg/gateway/openapi/openapi.go` and served at `/openapi.json` and `/openapi.yaml`; it is not produced by `make generate`) is the single authoritative schema the MCP `create_session` tool schema and the client SDKs derive from (`spec/15_external-api-surface.md:1386`, `spec/15_external-api-surface.md:2492`), so its three `executionMode` enums (openapi.json:180, :270, :540) change from `["session","task","concurrent"]` to `["session","service"]`, the `concurrencyStyle` enum property (openapi.json:541) is removed, the `conversationContinuity` field (enum `["platform","none"]`) is added to the `sessionIsolationLevel` schema properties (openapi.json:179-184), and the `taskPolicy` object inside the `Runtime` schema (openapi.json:380-394) is replaced by the `sessionPolicy` structure so the authoritative document matches the renamed modes, the new contract field, and the collapsed policy block rather than only the downstream SDK structs. The `taskPolicy` replacement drops `maxTasksPerPod` (openapi.json:391), renames `maxTaskRetries` (openapi.json:393) to `maxSessionRetries`, renames `microvmScrubMode` (openapi.json:385, enum `["restart","in-place"]`) to `scrubProfile` with the enum `["standard","vm-restart","in-place"]` under the `recycle` sub-object, and relocates the surviving knobs (`acknowledgeBestEffortScrub`, `allowCrossTenantReuse`, `acknowledgeMicrovmResidualState`, `cleanupCommands`, `cleanupTimeoutSeconds`, `onCleanupFailure` renamed to `onScrubFailure`, `maxScrubFailures`, and `maxPodUptimeSeconds`) onto `sessionPolicy` so the served `Runtime` schema no longer advertises a `taskPolicy` object, a `maxTasksPerPod` knob, or a `microvmScrubMode` enum the proposal removes from the spec, the CRD types, the CRD manifests, and the poolstore mirror, the same authoritative-document edit-site class the Pass-9 fix caught for the `executionMode`/`concurrencyStyle` enums in this hand-authored file.
- RBAC: as carried from the prior 0002 (gateway loses `sandboxes/status` and the `sandboxes` `patch` and `watch` verbs, and gains `sandboxclaims/status`; the WarmPoolController gains `get` and `watch` on `SandboxClaim`).

## 6. Proposed spec changes

One subsection per target file and section, in spec order. Each edit carries an anchor instruction that quotes the existing spec text exactly (line numbers in anchors are location hints and drift; the quoted text is authoritative) and, where text is inserted or replaced, a fenced block with the exact new text, written to be applied mechanically after sign-off. Code citations in subsection prose are implementer rationale and never enter the staged spec text. Schema, chart, code, and documentation changes are recorded in Sections 5, 7, and 13 and land with the implementation work.

### 6.1 `spec/02_goals-and-non-goals.md` Goals (execution-modes goal line)

The goals list names the removed `task` and `concurrent` modes. Replace the goal bullet that reads:

> - Support multiple execution modes for agent runtimes: `session` (one session per pod), `task` (sequential pod reuse with workspace scrub), and `concurrent` (slot-multiplexed parallel tasks) — with mode-aware pool scaling

with:

```
- Support the `session` and `service` execution modes for agent runtimes: `session` binds a managed session to a pod and is parameterized by a `sessionPolicy` block (pod recycling across sessions and concurrent session slots), `service` routes each message to any ready replica, and pool scaling factors derive from the policy properties
```

### 6.2 `spec/04_system-components.md` §4.4 (per-slot workspace path re-key)

Re-key the "Hard workspace size limit" paragraph's per-slot path from the removed concurrent-workspace mode name to the `maxConcurrentSessions > 1` predicate, in lockstep with the §6.4 per-slot layout re-key and the §14 WorkspacePlan scope-note re-key, so the per-slot workspace path survives while the removed mode name does not.

In the paragraph beginning `**Hard workspace size limit.**`, replace the text:

> `/workspace/current` (and `/workspace/slots/{slotId}/current/` in concurrent-workspace mode) MUST carry

with:

```
`/workspace/current` (and `/workspace/slots/{slotId}/current/` when `sessionPolicy.maxConcurrentSessions > 1`, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) MUST carry
```

### 6.3 `spec/04_system-components.md` §4.6.1 (SandboxClaim CRD-mapping row)

Replace the `SandboxClaim` row of the agent-sandbox CRD-mapping table so the resource is described as the per-pod occupancy claim (deterministic name, spec reduced to `sandboxRef` and `tenantId`, Postgres `pod_assignment` as the session binding) rather than a per-session binding.

Replace the table row:

> `| `SandboxClaim`    | `AgentSession`               | Represents an active session binding. Created by the gateway only after it has successfully claimed a `Sandbox`. Links the claimed pod to session metadata (deliberately not an `ownerReference`, so the session survives pod deletion and can be reassigned). |`

with:

```
| `SandboxClaim`    | `AgentSession`               | Represents a pod-occupancy claim with the deterministic name `claim-<podName>`. Created by the gateway when it acquires an idle pod and deleted when the reserved hold expires or the pod terminates; its spec carries `sandboxRef` and `tenantId`, and its status carries the binding state ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). The session-to-pod binding lives on the Postgres session row's `pod_assignment` column. The claim deliberately omits an `ownerReference`: deletion is an explicit step of the claim lifecycle (hold expiry, orphan GC, or pod termination) rather than a cascade from pod deletion. |
```

### 6.4 `spec/04_system-components.md` §4.6.1 (sandboxclaim-guard webhook)

Re-key the `lenny-sandboxclaim-guard` paragraph: the per-session `PATCH`/`PUT` stale-write rule that reads the referenced `Sandbox.status.phase` is removed, the status-enumeration cross-reference names the pod-occupancy binding state, and the `CREATE` rule is stated as per-pod uniqueness with no concurrency exemption. The webhook's role in the per-pod model is the `CREATE` per-pod-uniqueness check only. The per-pod claim spec is immutable after `CREATE`, and binding-state transitions are writes to the `SandboxClaim.status` subresource, which the webhook does not gate. A `Sandbox.status.phase` accept-set cannot serialize those writes anyway, because the occupancy projection sets the Sandbox to `claimed` from the claim's `bound` binding state (Section 3.3), so the gateway's first `bound` status patch lands while the referenced Sandbox is still `idle`; an accept-set requiring a live-claim phase would reject the very write that establishes `bound`. Binding-state serialization is the Section 3.2 `resourceVersion`-precondition mechanism. Five edits inside the paragraph beginning `**`SandboxClaim` admission webhook (double-claim prevention):**`: the opening framing that asserts the webhook intercepts `PATCH`/`PUT` and reads `Sandbox.status.phase`, the three per-session PATCH/PUT sentences, the `(Note: ...)` parenthetical, the `For CREATE` sentence, and the closing sentence that grounds the double-claim guarantee in evaluating `Sandbox.status.phase`. The opening and closing edits restage the paragraph's intercept set and double-claim rationale so the entire paragraph describes a `CREATE`-only guard whose double-claim guarantee rests on the deterministic `claim-<podName>` name plus the API-server name-uniqueness check, rather than on reading `Sandbox.status.phase`.

Replace the opening framing that names the PATCH/PUT intercept set and the `Sandbox.status.phase` phase-check basis:

> The webhook intercepts every `CREATE`, `PATCH`, and `PUT` operation on `SandboxClaim` resources in agent namespaces. The authoritative pod state machine lives on the referenced `Sandbox` CRD's `.status.phase` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), not on `SandboxClaim.status.phase` — every phase check in this webhook reads `Sandbox.status.phase` via the claim's `.spec.sandboxRef`.

with:

```
The webhook intercepts every `CREATE` operation on `SandboxClaim` resources in agent namespaces and admits `PATCH` and `PUT` operations without inspection. The webhook reads no phase: the `CREATE` per-pod-uniqueness check queries existing `SandboxClaim` resources by `.spec.sandboxRef` and does not read `Sandbox.status.phase` or `SandboxClaim.status.phase`. The pod state machine on the referenced `Sandbox` CRD's `.status.phase` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) and the `SandboxClaim.status.phase` binding state ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)) are both outside the webhook's evaluation.
```

Replace the three sentences that state the per-session PATCH/PUT rule:

> For `PATCH`/`PUT`, the webhook queries the API server for the referenced `Sandbox` (via `.spec.sandboxRef`) and rejects the request if the `Sandbox.status.phase` is not `claimed` (i.e., the target pod no longer holds an active claim to this `SandboxClaim` — e.g., it has been released back to `idle`, or has progressed into a terminal state such as `terminated`/`failed`). The rejection returns `403 Forbidden` with message `"SandboxClaim stale: referenced Sandbox <name> is in phase <phase>, not claimed; concurrent write rejected"`. This ensures that a stale in-flight write from a failed-over gateway or controller cannot mutate a `SandboxClaim` whose underlying pod is no longer bound to it.

with:

```
The webhook does not gate `PATCH`/`PUT`: the per-pod `SandboxClaim` spec is immutable after `CREATE`, and the binding-state transitions are writes to the `SandboxClaim.status` subresource. Those writes are serialized by the Kubernetes optimistic-concurrency UID and `resourceVersion` preconditions ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle) reserved-hold paragraph), which order a `reserved → bound` rebind against a concurrent hold-expiry `DELETE` without the webhook. A `Sandbox.status.phase` accept-set cannot serialize binding-state writes in any case: the occupancy projection sets the referenced `Sandbox` to `claimed` from the claim's `bound` binding state, so the gateway's first `bound` status patch lands while the `Sandbox` is still `idle`, and a phase accept-set requiring a live claim would reject the write that establishes `bound`.
```

Replace:

> (Note: the webhook does not examine `SandboxClaim.status.phase` itself — see the `SandboxClaim` status enumeration in [Section 4.6.3](#463-crd-field-ownership-and-write-boundaries), which is owned by the gateway and tracks session-binding state rather than pod lifecycle state.)

with:

```
(Note: the webhook does not examine `SandboxClaim.status.phase` itself; the `SandboxClaim` status enumeration in [Section 4.6.3](#463-crd-field-ownership-and-write-boundaries) is owned by the gateway and tracks the pod-occupancy binding state rather than pod lifecycle state.)
```

Replace:

> For `CREATE`, the webhook queries the API server for any existing `SandboxClaim` whose `.spec.sandboxRef` matches the target `Sandbox`; if a non-terminal claim already exists, the creation is rejected with `403 Forbidden` and message `"SandboxClaim already exists for Sandbox <name>; concurrent claim rejected"`.

with:

```
For `CREATE`, the webhook queries the API server for any existing `SandboxClaim` whose `.spec.sandboxRef` matches the target `Sandbox`; if a non-terminal claim already exists, the creation is rejected with `403 Forbidden` and message `"SandboxClaim already exists for Sandbox <name>; concurrent claim rejected"`. The rule is per-pod uniqueness with no concurrency exemption: a pool with `sessionPolicy.maxConcurrentSessions > 1` multiplexes its sessions onto the single per-pod claim ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The deterministic `claim-<podName>` name means a duplicate `CREATE` under the canonical name also fails the API server's name-uniqueness check; the webhook check additionally covers a claim created under any other name.
```

Replace the closing sentence that grounds the double-claim guarantee in evaluating `Sandbox.status.phase`:

> Because the webhook evaluates `Sandbox.status.phase` as persisted in etcd at admission time, it makes double-claim impossible regardless of timing: even if two writers race with the same `resourceVersion`, the second writer's request is rejected at admission before it reaches the API server's optimistic-lock check.

with:

```
Because the `CREATE` check evaluates the set of existing `SandboxClaim` resources for the target `Sandbox` as persisted in etcd at admission time, and the deterministic `claim-<podName>` name additionally subjects a duplicate `CREATE` to the API server's name-uniqueness check, double-claim is impossible regardless of timing: even if two writers race to create a claim for the same `Sandbox`, the second writer's `CREATE` is rejected at admission, or by the name-uniqueness check, before a second binding is established.
```

### 6.5 `spec/04_system-components.md` §4.6.1 (pod claim mechanism, occupancy projection, and reserved hold)

Replace the "Pod claim mechanism" paragraph with the per-pod acquisition model and add the two carried-forward paragraphs the prior 0002 staged for this section, re-derived for per-pod claims: the claim-driven occupancy projection (Section 3.3) and the reserved hold with the precondition-fenced expiry DELETE (Section 3.2). The `Counter == nil` fallback deletion and the Redis-outage Postgres capacity gate land in the §5.2 and §12.4 directives; this section states the claim-side contract.

Replace the paragraph:

> **Pod claim mechanism:** Gateway replicas claim pods via `SandboxClaim` resources with optimistic locking — exactly one gateway wins; all others receive a conflict and retry with a different idle pod from the pool. This keeps the controller off the claim hot path entirely — pod-to-session binding is resolved at the API-server level with no single-writer bottleneck. `ClaimPod` implementations must use a `resourceVersion`-guarded compare-and-swap loop: on HTTP 409, re-read the current `Sandbox` state and retry with the updated `resourceVersion` (or select a different idle pod if the target pod is no longer available). This loop is the primary defense against both concurrent gateway races and stale in-flight writes from a failed controller replica.

with:

```
**Pod claim mechanism (per-pod occupancy claim):** Gateway replicas acquire pods by creating a `SandboxClaim` with the deterministic name `claim-<podName>`. Exactly one gateway's `CREATE` succeeds; every other replica receives an `AlreadyExists` conflict and retries with a different idle pod from the pool. This keeps the controller off the claim hot path entirely, and pod acquisition is resolved at the API-server level with no single-writer bottleneck. The claim's spec carries `sandboxRef` and `tenantId`. The claim is created with spec only, and the gateway writes the first binding state (`bound`) with a subsequent status patch, because the status subresource is not writable by the resource `Create` call. The session-to-pod binding is recorded on the Postgres session row's `pod_assignment` column, and per-session idempotency is anchored by the session identifier rather than by a claim name. Intra-pod capacity on pools with `sessionPolicy.maxConcurrentSessions > 1` is gated by the atomic Redis slot counter, with a Postgres fallback during a Redis outage per the [Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes) posture ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). On an `AlreadyExists` conflict or a webhook rejection, `ClaimPod` implementations re-read the idle inventory and retry with a different pod; this retry loop is the primary defense against both concurrent gateway races and stale in-flight writes from a failed replica. Claim traffic therefore scales with pod-occupancy episodes rather than with sessions: the claim is created when the gateway acquires an idle pod and deleted when the reserved hold below expires or the WarmPoolController completes the pod's termination.

**Occupancy projection (claim-driven `Sandbox.status.phase`):** The WarmPoolController watches `SandboxClaim` resources and computes `Sandbox.status.phase` as a level-triggered projection of claim existence, claim binding state ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), and the pool's `sessionPolicy`; the gateway does not write `Sandbox.status`. The phase enum is coarse: `warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, and `terminated`. There is no per-slot phase value: concurrent occupancy projects `claimed`, and concurrency is observable through the Redis slot counter and metrics. The projection emits:

- `idle` for a pod in a warm-inventory phase with no claim.
- `claimed` while the claim's binding state is `bound`, and also while it is `recycling` with the whole-pod scrub not yet reported (on both preConnect and non-preConnect pools), so the `sdkConnectTimeoutSeconds` watchdog ([Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)) does not run during the scrub; the scrub is bounded by the gateway-side missing-report timeout at the recycle boundary ([Section 4.7](#47-runtime-adapter)).
- `sdk_connecting` on a preConnect pool while the claim is `recycling` after a successful `ReportPodScrub` ([Section 4.7](#47-runtime-adapter)), covering the SDK re-warm leg alone; the watchdog clock is armed from the `rewarmStartedAt` stamp on the claim status, so only the re-warm is measured against `sdkConnectTimeoutSeconds`. On a non-preConnect pool a successfully scrubbed `recycling` claim projects directly to `reserved` with no re-warm leg.
- `reserved` while the binding state is `reserved` (scrubbed, SDK-warm on preConnect pools, held for the pinned tenant, and excluded from idle inventory).
- `idle` when the claim is deleted on a recycling pod under its limits (hold expiry). The scrub and any re-warm completed before the claim entered `reserved`, so this edge is a pure claim-deletion projection with no second re-warm.
- `draining`, then `terminated`, when the claim records a terminal disposition (`released` or `failed`) or is deleted on a non-recycling pod. The one-session-only invariant of [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) is the `recycle.enabled: false` configuration: the projection returns a pod from `claimed` to `idle` only on a recycling pool.

Uptime drains derive from the pod `CreationTimestamp` against `recycle.maxPodUptimeSeconds`, and the unhealthy-threshold drain derives from the gateway-stamped `lenny.dev/drain-request` Pod annotation; both transitions are WarmPoolController-written.

**Reserved hold (claim retention across same-tenant sessions):** When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`, coordinates the whole-pod scrub and, on preConnect pools, the SDK re-warm, and then patches the binding state to `reserved` rather than deleting the claim; a pod that reaches `reserved` is always scrubbed and SDK-warm. At the `reserved` patch the gateway stamps `holdExpiresAt` (the reservation time plus the hold TTL) on the claim status. The hold TTL is a deployment-level gateway configuration value (`gateway.claimHoldTTLSeconds`, default: 10 seconds); configuration validation warns when it is set high, because a long hold delays the pod's return to the pinned tenant's claimable idle inventory and delays retirement-limit evaluation at the next disposition. A new session of the same tenant arriving within the hold window is dispatched onto the pod with a `reserved → bound` status patch and no acquisition round trip; any gateway replica may rebind. If the TTL expires first, the holder deletes the claim and the pod returns to `idle` with no second re-warm. Every hold-expiry `DELETE` (the gateway TTL holder and the WarmPoolController orphan GC alike) carries Kubernetes preconditions: the claim UID and the `resourceVersion` observed when the claim entered `reserved`. A rebind patch that lands first changes the `resourceVersion`, so the `DELETE` fails its precondition and the expiry aborts; the rebinding replica re-reads the claim after its patch before dispatching. A reserved pod is excluded from idle inventory entirely: the candidate scan skips it and the per-pod `CREATE` guard blocks acquisition by another replica. The `lenny.dev/tenant-id` pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated `recycle.allowCrossTenantReuse` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)): a pinned idle pod is claimable only by its pinned tenant, and inventory accounting counts pinned-idle pods as idle inventory available to that tenant alone.
```

### 6.6 `spec/04_system-components.md` §4.6.1 (CRD validation CEL rules)

Re-key the CEL validation rule from the removed `taskPolicy` to the `sessionPolicy` rules, matching the derived-property validation re-key staged in Section 5.

In the paragraph beginning `**CRD validation:**`, replace:

> `maxSessionAge > 0`, and `taskPolicy.maxTasksPerPod > 0` when `executionMode: task`.

with:

```
`maxSessionAge > 0`, `sessionPolicy.maxConcurrentSessions >= 1`, and `sessionPolicy.recycle.maxSessionsPerPod > 0` when `sessionPolicy.recycle.enabled` is `true`.
```

### 6.7 `spec/04_system-components.md` §4.6.1 (claim queue and onPoolExhausted)

Restate the claim-queue and Postgres-fallback paragraphs for the `onPoolExhausted` composition (Section 3.1): the existing per-pool queue is parameterized rather than duplicated, `reject` keeps current behavior, and `queue` extends the wait after both acquisition paths are exhausted. Four edits.

In the paragraph beginning `**Controller failover and warm pool sizing:**`, replace:

> If no pod becomes available before the timeout, the session creation fails with a retryable error so the client can back off and retry.

with:

```
If no pod becomes available before the timeout, the gateway attempts the Postgres fallback claim below and then follows the pool's `onPoolExhausted` setting (see "Pool exhaustion behavior" below).
```

In the paragraph beginning `**Fallback claim path via Postgres:**`, replace:

> If the Postgres fallback also finds no idle pods (the warm pool is genuinely exhausted), session creation fails with `WARM_POOL_EXHAUSTED`.

with:

```
If the Postgres fallback also finds no idle pods (the warm pool is genuinely exhausted), the request follows the pool's `onPoolExhausted` setting (see "Pool exhaustion behavior" below).
```

In the paragraph beginning `**Fallback preconditions (mirror freshness and admission reachability).**`, replace:

> If either fails, the gateway skips the fallback and returns `WARM_POOL_EXHAUSTED` immediately rather than risking a stale-mirror double-claim:

with:

```
If either fails, the gateway skips the fallback rather than risking a stale-mirror double-claim and proceeds per the pool's `onPoolExhausted` setting:
```

After the fallback-preconditions list (immediately after the numbered item ending `has itself exceeded its safe operating envelope.`), insert the new paragraph:

```
**Pool exhaustion behavior (`onPoolExhausted`):** The per-pool claim queue above is parameterized by `sessionPolicy.onPoolExhausted` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). With `reject` (the default), session creation fails with `WARM_POOL_EXHAUSTED` once the claim-path timeout and the Postgres fallback are exhausted. With `queue`, the request remains in the same per-pool FIFO for up to `sessionPolicy.maxQueueWaitSeconds` (default: 30) after both paths are exhausted and re-enters acquisition as pods free. A queued request holds no pod, no slot, and no claim, and the [Section 7.1](07_session-lifecycle.md#71-normal-flow) atomicity contract is preserved: the client receives a `session_id` only on success. On queue-wait timeout the gateway returns `WARM_POOL_EXHAUSTED` with a `Retry-After` header. The queue is scoped to session mode; service-mode messages are routed to ready replicas rather than claimed ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The existing `lenny_pod_claim_queue_depth`, `lenny_pod_claim_queue_wait_seconds`, and `lenny_pod_claim_timeout_total` series and the `PodClaimQueueSaturated` alert measure this single queue; no separate queue series is added.
```

### 6.8 `spec/04_system-components.md` §4.6.1 (etcd write-pressure budget)

Replace the budget's assumption sentence, which describes the removed `claim → active → released` per-session lifecycle, with the per-pod claim profile: amortized CREATE and DELETE traffic, per-session binding-state PATCHes on the recycle path that `statusUpdateDeduplicationWindow` does not coalesce, and the accounting against the Tier 3 ceiling.

In the paragraph following the etcd write-pressure table, replace the sentence:

> The estimate assumes ~1 status write per pod per 2-minute lifetime (claim → active → released) plus warm-pool reconciliation writes.

with:

```
The estimate assumes the per-pod claim profile: one `SandboxClaim` CREATE and DELETE per pod-occupancy episode rather than per session, the `Sandbox.status` projection writes the claim transitions drive, and warm-pool reconciliation writes. On a recycling pool the per-pod claim amortizes CREATE and DELETE traffic across every session of the occupancy episode, but each recycled session adds approximately 3 `SandboxClaim.status` binding-state PATCHes (`bound → recycling → reserved → bound`) plus the projection writes each transition drives; a pod serving up to `maxSessionsPerPod` sessions over up to `maxPodUptimeSeconds` cycles these states once per session. The `statusUpdateDeduplicationWindow` does not coalesce the claim PATCHes, because that window is scoped to consecutive `UpdateStatus` writes for the same `Sandbox` resource (see the semantics note below) while the claim PATCHes land on per-pod `SandboxClaim` resources and are spaced across session arrivals. At the Tier 3 expected claim rate of ~30 claims/s ([Section 17.8.2](17_deployment-topology.md#1782-capacity-tier-reference)), a fully recycling profile adds on the order of ~90 non-deduplicated `SandboxClaim` writes/s plus the driven projection writes; this fits within the ~800 writes/s Tier 3 estimate, but only the `Sandbox` projection writes are bounded by the ~120 QPS post-dedup ceiling and the status update rate limiter. On a pool without recycling the occupancy episode and the session coincide, the recycle PATCHes do not occur, and the per-session write profile is one CREATE, the binding-state patch, the projection writes, and one DELETE.
```

### 6.9 `spec/04_system-components.md` §4.6.1 (warm-pod PDB during recycle and hold)

State that no pod inside the recycle or hold window matches the warm-pod PDB idle selector, with the label values from the Section 3.3 projection (a preConnect recycling pod is labeled `active` during the scrub and unlabeled only during the re-warm leg; the Section 6 row's coarser phrasing is superseded by Section 3.3 here).

After the sentence:

> The PDB targets only unclaimed (warm) pods via a label selector (`lenny.dev/state: idle`), so it does not interfere with the preStop-based protection on active session pods.

append:

```
A pod inside the recycle or hold window never matches this selector: a `reserved` pod carries `lenny.dev/state: active`, a `recycling` pod carries `active` while the whole-pod scrub runs (on both preConnect and non-preConnect pools, since the pod projects `claimed` during the scrub), and a `recycling` pod on a preConnect pool projects `sdk_connecting` during its SDK re-warm leg and carries no `lenny.dev/state` label at all ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). None of these pods has voluntary-disruption protection during the recycle and hold window. This is accepted because the recycle scrub and the reserved hold are optimizations: a voluntary eviction during either deletes the claim through the normal pod-termination path, and the next same-tenant session falls back to normal acquisition.
```

### 6.10 `spec/04_system-components.md` §4.6.1 (orphaned SandboxClaim detection)

Re-key the orphan GC for the per-pod claim and its binding states: live states (`bound` and `recycling`) are keyed to the binding-state-transition time and reclaimed by draining, `reserved` claims are reclaimed by the precondition-guarded `DELETE` after `holdExpiresAt` plus a grace period, and a `metadata.creationTimestamp` fallback covers the CREATE-before-status crash window the shipped GC's creation-timestamp key covers today (`pkg/controller/warmpool/gc.go` keys on creation time plus the active-session check).

Replace the full paragraph beginning:

> **Orphaned `SandboxClaim` detection:** Because `SandboxClaim` resources deliberately omit an `ownerReference` (to survive pod deletion and allow reassignment), Kubernetes will not garbage-collect them automatically.

and ending:

> A `SandboxClaimOrphanRateHigh` warning alert fires when `lenny_orphaned_claims_total` exceeds 10 in a 15-minute window, indicating potential gateway instability (see [Section 16.5](16_observability.md#165-alerting-rules-and-slos)).

with:

```
**Orphaned `SandboxClaim` detection:** Because `SandboxClaim` resources deliberately omit an `ownerReference` (claim deletion is an explicit step of the claim lifecycle rather than a cascade from pod deletion), Kubernetes will not garbage-collect them automatically. The WarmPoolController's `GarbageCollect` reconciliation loop detects orphaned claims as follows: every 60 seconds, **the leader replica** lists all `SandboxClaim` resources and evaluates three predicates keyed to the claim's binding state ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). Because the WarmPoolController runs with Lease-based leader election (see leader election paragraph above), only the current leader executes the `GarbageCollect` loop; non-leader replicas skip it. This is the deliberate reason orphan detection is owned by the WarmPoolController rather than the gateway: placing the GC loop in the gateway would cause all gateway replicas to run API server list operations against the agent namespace simultaneously at each 60-second tick, multiplying API load proportionally with the gateway replica count. By running GC exclusively in the single elected leader of the WarmPoolController, the API list load is constant regardless of how many gateway or controller replicas are deployed. The predicates are:

1. **Live binding states (`bound` or `recycling`).** A claim whose binding state is `bound` or `recycling`, whose last binding-state transition is older than `claimOrphanTimeout` (default: 5 minutes, configurable via `--claim-orphan-timeout`), and whose pod no active session references (the controller queries Postgres through the `pod_assignment` binding) is reclaimed by draining the pod, regardless of the pool's recycle settings. Draining rather than returning to `idle` is fail-closed by necessity: the whole-pod scrub is adapter-executed and gateway-coordinated ([Section 4.7](#47-runtime-adapter)), the controller has no GatewayControl path to the pod, and returning an unscrubbed pod to `idle` would break the scrub-before-idle invariant ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). This predicate covers a coordinating-gateway crash during the `recycling` scrub wait, which leaves the claim in `recycling` with no `holdExpiresAt` and no `rewarmStartedAt` stamp, where neither the reserved predicate below nor the `sdkConnectTimeoutSeconds` watchdog would reach it. The binding-state-transition time replaces `metadata.creationTimestamp` as the orphan key for any claim that has reached a binding state, because a per-pod claim's creation time marks the start of the whole occupancy episode rather than the start of the orphan window.
2. **Reserved claims.** A claim whose binding state is `reserved` is reclaimed once `holdExpiresAt` plus a grace period has passed, using the same precondition-guarded `DELETE` as the gateway's hold-expiry path (the claim UID plus the `resourceVersion` observed at the `reserved` transition), so a concurrent rebind from any gateway replica wins the race. The pod was scrubbed and re-warmed before entering `reserved`, so deletion returns it to `idle` and preserves the scrub-before-idle invariant.
3. **Unset binding state (CREATE-before-status fallback).** A claim is created with spec only, and its first binding state is written by a subsequent gateway status patch; a gateway crash between the `CREATE` and that first patch leaves the claim with empty status, which the binding-state-keyed predicates cannot select. The loop therefore also reclaims, by draining, a claim whose binding state is unset, whose `metadata.creationTimestamp` is older than `claimOrphanTimeout`, and whose pod no active session references. This handles the crash scenario where a gateway creates a `SandboxClaim` but crashes before persisting the session to Postgres: on recovery, the gateway has no record of the claim, so the controller's orphan loop reclaims it.

The metric `lenny_orphaned_claims_total` (counter, labeled by pool) is incremented for each orphaned claim reclaimed. A `SandboxClaimOrphanRateHigh` warning alert fires when `lenny_orphaned_claims_total` exceeds 10 in a 15-minute window, indicating potential gateway instability (see [Section 16.5](16_observability.md#165-alerting-rules-and-slos)).
```

### 6.11 `spec/04_system-components.md` §4.6.1 (host-node schedulability labeling)

Re-key the paragraph's dangling `task_cleanup → sdk_connecting` reference to the recycle re-warm edge; the labeling mechanism, the immutability-set carve-out, the gateway's label read through its existing `Pods` `get` access, and the no-new-Node-RBAC posture carry forward unchanged.

In the paragraph beginning `**Host-node schedulability labeling (`lenny.dev/host-schedulable`):**`, replace the final sentence fragment:

> pods whose `spec.nodeName` is not yet set — i.e., still `Pending` at the scheduler — are not eligible for `task_cleanup → sdk_connecting` transitions, so the absence of the label on an unscheduled pod is never encountered by the gateway's precondition check.

with:

```
pods whose `spec.nodeName` is not yet set — i.e., still `Pending` at the scheduler — are not eligible for the `claimed → sdk_connecting` recycle re-warm edge (the preConnect occupancy-zero whole-pod scrub and SDK re-warm under a `recycling` claim, [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), so the absence of the label on an unscheduled pod is never encountered by the gateway's precondition check.
```

### 6.12 `spec/04_system-components.md` §4.6.2 (scaling-factor derivations and reserved-pod inventory)

Restate the three mode-keyed scaling sentences as `sessionPolicy` derivations for session mode and `maxConcurrent` derivations for service mode, and add the reserved-pods-count-as-occupied inventory rule (Section 3.3).

Replace:

> This formula assumes session mode (one session per pod). For task and concurrent modes, a `mode_factor` adjustment reduces pod demand based on reuse and slot multiplexing — see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) "Execution Mode Scaling Implications" for per-mode formula variants.

with:

```
This formula assumes the default `sessionPolicy` (`maxConcurrentSessions: 1`, `recycle.enabled: false`; `mode_factor = 1.0`). When recycling is enabled, when `maxConcurrentSessions` exceeds one, or for a service-mode pool, a `mode_factor` adjustment reduces pod demand based on pod reuse and slot multiplexing: session mode derives `mode_factor` from the observed sessions per pod lifetime (bounded by `recycle.maxSessionsPerPod`, measured by the reuse histogram) and `burst_mode_factor = maxConcurrentSessions`, and service mode derives both factors from the pool-level `maxConcurrent` per-pod capacity. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) "Execution Mode Scaling Implications" for the formula variants.
```

Replace:

> This formula assumes session mode (`mode_factor = 1.0`). For task-mode or concurrent-mode variant pools, apply `/ mode_factor` to the steady-state term and `/ burst_mode_factor` to the burst term, consistent with the mode-adjusted formula in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).

with:

```
This formula assumes the default `sessionPolicy` (`mode_factor = 1.0`). For variant pools whose `sessionPolicy` enables recycling or sets `maxConcurrentSessions > 1`, and for service-mode variant pools, apply `/ mode_factor` to the steady-state term and `/ burst_mode_factor` to the burst term, consistent with the mode-adjusted formula in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).
```

Replace:

> This formula assumes session mode. For task-mode or concurrent-mode base pools, apply `/ mode_factor` to the steady-state term and `/ burst_mode_factor` to the burst term, consistent with the mode-adjusted formula in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).

with:

```
This formula assumes the default `sessionPolicy`. For base pools whose `sessionPolicy` enables recycling or sets `maxConcurrentSessions > 1`, and for service-mode base pools, apply `/ mode_factor` to the steady-state term and `/ burst_mode_factor` to the burst term, consistent with the mode-adjusted formula in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).
```

Immediately before the paragraph beginning `**Variant pool formula (experiment pools only).**`, insert:

```
**Reserved pods count as occupied.** A pod whose claim is in the `reserved` hold window ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) is excluded from claimable idle inventory and is counted as occupied for inventory and scaling purposes. A long `gateway.claimHoldTTLSeconds` therefore depresses apparent idle inventory; configuration validation warns when the hold TTL is set high.
```

### 6.13 `spec/04_system-components.md` §4.6.3 (ownership table and RBAC)

Carry the prior 0002's ownership decomposition and RBAC convergence forward for per-pod claims: the `Sandbox` status row records the projection, the `SandboxClaim` row records the new lifecycle, the WarmPoolController gains the claim watch that drives the projection, and the gateway loses every `Sandbox.status` write surface while gaining the `sandboxclaims/status` grant. The gateway no longer toggles `lenny.dev/state` (the label is a WarmPoolController projection per Section 3.3); it stamps the `lenny.dev/drain-request` annotation instead.

Replace the table row:

> `| `Sandbox`         | `status.*`                                         | WarmPoolController         | State machine transitions, health               |`

with:

```
| `Sandbox`         | `status.*`                                         | WarmPoolController         | Sole writer of phase and conditions; the occupancy phase is a level-triggered projection of `SandboxClaim` state ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) |
```

Replace the table row:

> `| `SandboxClaim`    | `spec.*`, `status.*`                               | Gateway (not a controller) | Created/deleted by gateway during claim/release |`

with:

```
| `SandboxClaim`    | `spec.*`, `status.*`                               | Gateway (not a controller) | Created by the gateway at pod acquisition; binding state written via the status subresource; deleted by the gateway at hold expiry or by the WarmPoolController at pod termination and orphan GC |
```

In the paragraph beginning `**RBAC enforcement (coarse-grained backstop):**`, replace:

> `list`/`delete` on `SandboxClaim` (required for orphan claim garbage collection — see `GarbageCollect` above)

with:

```
`get`/`list`/`watch`/`delete` on `SandboxClaim` (the `watch` drives the claim-existence occupancy projection — see [Section 4.6.1](#461-warm-pool-controller-pod-lifecycle) — and `list`/`delete` serve orphan claim garbage collection — see `GarbageCollect` above)
```

Replace the full paragraph beginning `**Gateway ServiceAccount RBAC grants:**` and ending `when a pod is returned to the pool.` with:

```
**Gateway ServiceAccount RBAC grants:** The gateway's ServiceAccount (`system:serviceaccount:lenny-system:lenny-gateway`) requires RBAC grants separate from the controller ServiceAccounts. The gateway performs pod label and annotation mutations (setting `lenny.dev/tenant-id` on first assignment and stamping the `lenny.dev/drain-request` annotation when a pod crosses the unhealthy threshold, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) and manages `SandboxClaim` resources. Required grants: `get`/`patch` on `Pods` in agent namespaces (`lenny-agents`, `lenny-agents-kata`) for the tenant-id label and the drain-request annotation, `create`/`get`/`delete` on `SandboxClaim` resources for the per-pod claim lifecycle, `get`/`patch` on the `sandboxclaims/status` subresource for the binding-state, transition-time, `rewarmStartedAt`, and `holdExpiresAt` writes, and `get`/`list` on `Sandbox` resources for pod selection during claim. The gateway holds no `sandboxes/status` grant and no `patch` or `watch` verb on the `Sandbox` main resource: `Sandbox.status` is written solely by the WarmPoolController, and the `lenny.dev/state` pod label is a WarmPoolController projection of the coarse phase ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) rather than a gateway write. The `lenny-tenant-label-immutability` webhook ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) must include the gateway ServiceAccount in its allowlist for the `unset → {tenant_id}` initial label write. The webhook allows the gateway SA to set `lenny.dev/tenant-id` on initial assignment, and allows the WarmPoolController SA to reset `{tenant_id} → unassigned` only on a pool whose microvm-gated `recycle.allowCrossTenantReuse` permits cross-tenant reuse ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); on every other pool the pin persists for the pod's lifetime, across the recycle-to-idle edge.
```

### 6.14 `spec/04_system-components.md` §4.6.3 (SandboxClaim binding-state enumeration)

Replace the per-session phase enumeration (`bound`, `active`, `released`, `failed`) with the per-pod binding states (`bound`, `recycling`, `reserved`, `released`, `failed`) and the additional status stamps the projection and the orphan GC consume.

Replace the full block beginning:

> **`SandboxClaim.status.phase` enumeration (gateway-owned binding state):** `SandboxClaim.status.phase` is distinct from the pod lifecycle state machine on `Sandbox.status.phase` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) — it tracks the **session-to-pod binding** rather than the pod's warm-pool lifecycle. The gateway is the sole writer and the legal phase values are:

and ending:

> CRD OpenAPI validation restricts `SandboxClaim.status.phase` to exactly this enumeration; any write containing an undefined phase value is rejected by the API server at validation time.

with:

```
**`SandboxClaim.status.phase` enumeration (gateway-owned binding state):** `SandboxClaim.status.phase` is distinct from the pod lifecycle state machine on `Sandbox.status.phase` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)); it tracks the **pod-occupancy binding** rather than the pod's warm-pool lifecycle. The gateway is the sole writer and the legal phase values are:
- `bound` — at least one session is bound to the pod; the pod projects `claimed`.
- `recycling` — occupancy reached zero on a recycling pool and the whole-pod scrub is running (the pod projects `claimed`), or, on a preConnect pool after a successful `ReportPodScrub` ([Section 4.7](#47-runtime-adapter)), the SDK re-warm is running (the pod projects `sdk_connecting`).
- `reserved` — the pod is scrubbed (and SDK-warm on preConnect pools) and held for its pinned tenant until `holdExpiresAt`; the pod projects `reserved` and is excluded from idle inventory.
- `released` — limit-reached retirement (`recycle.maxSessionsPerPod` or `recycle.maxPodUptimeSeconds`); the projection drains and terminates the pod. Terminal.
- `failed` — scrub-failure retirement under `onScrubFailure: fail`, or a failed or crashed session; the projection drains and terminates the pod. Terminal.

The claim status additionally carries the time of the last binding-state transition (the orphan GC's key for the live states, [Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)), the `rewarmStartedAt` stamp the gateway records on a preConnect pool when a successful `ReportPodScrub` arrives and the SDK re-warm begins (the anchor for the re-warm watchdog clock, [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)), and `holdExpiresAt` during the hold window. These phase values carry no pod-lifecycle semantics and must not be confused with `Sandbox.status.phase`; the WarmPoolController consumes them as projection input ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) but never writes them. The `lenny-sandboxclaim-guard` admission webhook ([Section 4.6.1](#461-warm-pool-controller-pod-lifecycle)) examines existing `SandboxClaim` resources by `.spec.sandboxRef`, never `SandboxClaim.status.phase`, when enforcing per-pod claim uniqueness at `CREATE`; it does not gate `SandboxClaim.status` writes, which are serialized by the Section 4.6.1 `resourceVersion`-precondition mechanism. CRD OpenAPI validation restricts `SandboxClaim.status.phase` to exactly this enumeration; any write containing an undefined phase value is rejected by the API server at validation time.
```

### 6.15 `spec/04_system-components.md` §4.7 (scrub-report RPCs)

Add `ReportSessionScrub` and `ReportPodScrub` to the Adapter → Gateway RPC table (Section 3.4); both are additions to the existing `GatewayControl` service, whose sole current row is `ExtendLease`. These are the design-level successors of the prior 0002's `ReportTaskCleanup`, which was never applied to the tree.

Immediately after the table row:

> `| `ExtendLease`    | Request a lease extension when the LLM proxy rejects a call for budget exhaustion ([Section 8.6](08_recursive-delegation.md#86-lease-extension)). Request body carries `extensions.additionalChildren`, `extensions.additionalTokenBudget`, `extensions.additionalMaxAge`, etc. Response status: `GRANTED` \| `PARTIALLY_GRANTED` \| `CEILING_REACHED` \| `REJECTED`. The adapter MUST NOT retry on `CEILING_REACHED` or `REJECTED`. |`

insert the rows:

```
| `ReportSessionScrub` | Report the outcome of the per-slot cleanup at a session release (`released` or `leaked`, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The gateway increments `sessionsServed` on the pod's `agent_pod_state` row, and `leaked` outcomes feed the unhealthy-threshold ledger behind the `lenny.dev/drain-request` annotation ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). |
| `ReportPodScrub` | Report the binary outcome of the whole-pod scrub that runs when occupancy reaches zero on a recycling pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). On failure the gateway increments `scrubFailureCount` on the pod's `agent_pod_state` row and computes the disposition against `sessionPolicy` ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). On success: on a preConnect pool the gateway records the `rewarmStartedAt` stamp on `SandboxClaim.status` and coordinates the SDK re-warm; on a non-preConnect pool it patches the claim directly to `reserved`. A missing report is bounded by a gateway-side timeout (`cleanupTimeoutSeconds` plus a grace period), after which the pod is retired. |
```

### 6.16 `spec/04_system-components.md` §4.7 (between-task lifecycle frame removal)

Delete the between-task lifecycle frames and the capability that gates them (Section 3.5): the `task_complete`, `task_ready`, and `task_complete_acknowledged` rows (the last carrying the `task_complete_ack_timeout` warning behavior, which is removed with it), the channel-diagram labels, the `terminate` row's cross-reference, and the `task_lifecycle` value in the `lifecycle_capabilities` row. The matching `lifecycle_capabilities` and `lifecycle_support` schema-array edits are staged in Section 5.

Replace the channel diagram block:

```text
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    checkpoint_complete, interrupt_request,
                    credentials_rotated, terminate,
                    task_complete, task_ready
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged,
                    llm_request_started, llm_request_completed,
                    task_complete_acknowledged
```

with:

```
Adapter → Runtime:  lifecycle_capabilities, checkpoint_request,
                    checkpoint_complete, interrupt_request,
                    credentials_rotated, terminate
Runtime → Adapter:  lifecycle_support, checkpoint_ready,
                    interrupt_acknowledged, credentials_acknowledged,
                    llm_request_started, llm_request_completed
```

Replace the table row:

> `| `lifecycle_capabilities`    | Adapter → Runtime  | `type`, `protocolVersion` (string, e.g., `"1.0"`), `capabilities` (array of strings: `"checkpoint"`, `"interrupt"`, `"credential_rotation"`, `"deadline_signal"`, `"task_lifecycle"`) | First message sent on channel open. Runtime must reply with `lifecycle_support`. `"task_lifecycle"` is offered only on task-mode pods and governs the `task_complete` / `task_complete_acknowledged` / `task_ready` message exchange ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). |`

with:

```
| `lifecycle_capabilities`    | Adapter → Runtime  | `type`, `protocolVersion` (string, e.g., `"1.0"`), `capabilities` (array of strings: `"checkpoint"`, `"interrupt"`, `"credential_rotation"`, `"deadline_signal"`) | First message sent on channel open. Runtime must reply with `lifecycle_support`. |
```

In the `terminate` table row, replace:

> Always means process exit — never used for between-task signaling (see `task_complete` below).

with:

```
Receipt always means process exit.
```

Delete the table row:

> `| `task_complete`             | Adapter → Runtime  | `type`, `taskId` (string)                                                                                                                                           | Between-task signal in task mode ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The current task is finished; the runtime must release task-specific resources (open files, temp state) and reply with `task_complete_acknowledged`. The runtime does NOT exit — it remains alive for the next task. |`

Delete the table row:

> `| `task_ready`                | Adapter → Runtime  | `type`, `taskId` (string)                                                                                                                                           | Sent after scrub completes and the new workspace is materialized. The runtime should re-read the adapter manifest (regenerated per task) and prepare for the next `{type: "message"}` on stdin. |`

Delete the table row:

> `| `task_complete_acknowledged` | Runtime → Adapter  | `type`, `taskId` (string)                                                                                                                                          | Runtime has released task-specific resources and is ready for scrub. Sent in response to `task_complete`. The adapter proceeds with deployer-defined `cleanupCommands` and Lenny scrub after receiving this acknowledgment. If not received within 30 seconds, the adapter logs a `task_complete_ack_timeout` warning and proceeds with cleanup anyway (the runtime may hold stale references but the scrub will clean the workspace). |`

### 6.17 `spec/04_system-components.md` §4.7 (adapter manifest regeneration)

Re-key the manifest and `mcpNonce` regeneration from per-task to per-session (Section 3.5): on a recycling pool the manifest is rewritten before each session's runtime start, and the runtime always reads a current manifest at startup.

Replace the paragraph:

> **Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` on the manifest volume (read-only to the agent container) **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per task execution (in task mode, re-written before each task; the runtime must re-read the manifest at the start of each task). The manifest is stable for the duration of a single task or session — it does not change while the runtime is processing.

with:

```
**Adapter manifest:** Written to `/run/lenny/adapter-manifest.json` on the manifest volume (read-only to the agent container) **before the runtime binary is spawned** — complete and authoritative when the binary starts. Regenerated per session: on a recycling pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) the adapter rewrites the manifest before each session's runtime start, so the manifest a runtime reads at startup is always current for its session. The manifest is stable for the duration of a single session and does not change while the runtime is processing.
```

In the `mcpNonce` row of the adapter manifest field reference table, replace:

> Random 256-bit hex nonce regenerated per task execution.

with:

```
Random 256-bit hex nonce regenerated per session.
```

### 6.18 `spec/04_system-components.md` §4.9 (residual-state cross-reference)

Re-key the semantic-cache timing-side-channel paragraph's analogy from the removed task-mode scrub to the recycle scrub.

In the paragraph beginning `**Tenant and user isolation:**`, replace:

> This is analogous to the residual state vectors documented for task-mode scrub ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) — a documented trade-off, not a defect.

with:

```
This is analogous to the residual state vectors documented for the pod-recycle scrub ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); it is a documented trade-off rather than a defect.
```

### 6.19 `spec/05_runtime-registry-and-pool-model.md` §5.1 (runtime.yaml examples and the sessionPolicy block)

The §5.1 runtime examples carry the removed `executionMode: task` value, a `sharedAssets` comment keyed to the removed `concurrent` mode, and the removed `taskPolicy` block. Replace each with the `session | service` mode set and the `sessionPolicy` structure from Section 3.1. The §5.1 `integrationLevel` prose (the field definition and its three consumers) names no execution mode; the task-mode integration-level prose lives in §5.2 and is removed by the §5.2 execution-modes subsection below.

Replace the line `executionMode: task` in the "Standalone Runtime" example (near line 65) with:

```yaml
executionMode: session
```

Replace the line beginning `sharedAssets:  # files populated into /workspace/shared/ (read-only) during pod init; only meaningful for concurrent execution mode` (near line 102) with:

```yaml
sharedAssets:  # files populated into /workspace/shared/ (read-only) during pod init; only meaningful when sessionPolicy.maxConcurrentSessions > 1
```

Replace the block from `taskPolicy:` through the line ending `maxPodUptimeSeconds: 86400 # optional — pod retired after this duration` in the "Derived Runtime" example (near lines 152-161) with:

```yaml
sessionPolicy:
  recycle:
    enabled: true
    acknowledgeBestEffortScrub: true # required when enabled: true
    allowCrossTenantReuse: false # only valid when isolationProfile: microvm
    maxSessionsPerPod: 50 # required when enabled — pod retired after serving this many sessions
    maxPodUptimeSeconds: 86400 # optional — pod retired after this uptime (seconds)
    maxScrubFailures: 3
    onScrubFailure: warn
  cleanupCommands:
    - rm -rf /tmp/sandbox-*
  cleanupTimeoutSeconds: 30
```

### 6.20 `spec/05_runtime-registry-and-pool-model.md` §5.1 (derived-runtime inheritance rules)

The two §5.1 inheritance references to `taskPolicy` re-key to `sessionPolicy`, and the `sharedAssets` merge-table note drops the removed `concurrent` mode name, so the inheritance prose names no removed field or mode after application.

Replace the paragraph beginning `**Independently configurable on derived runtime:**` (near line 172) with:

```
**Independently configurable on derived runtime:** Pool settings, `workspaceDefaults`, `setupCommands`, `setupPolicy.timeoutSeconds` (gateway takes maximum of base and derived), `agentInterface`, `delegationPolicyRef` (restrict only), `publishedMetadata`, `labels`, `sessionPolicy`.
```

Replace the merge-table row `| `taskPolicy` | **Override** — derived value replaces base if set | |` (near line 205) with:

```
| `sessionPolicy` | **Override** — derived value replaces base if set | |
```

In the `sharedAssets` merge-table row (near line 208), replace the sentence `Only meaningful for `concurrent` execution mode.` with:

```
Only meaningful when `sessionPolicy.maxConcurrentSessions` exceeds 1.
```

### 6.21 `spec/05_runtime-registry-and-pool-model.md` §5.2 (section overview)

The §5.2 opening sentence enumerates the removed mode set and the removed sub-variants. Replace the paragraph beginning `This section covers: pool dimensions and configuration fields, three execution modes` (near line 361) with:

```
This section covers: pool dimensions and configuration fields, the execution modes (`session` and `service`), the `sessionPolicy` block and its common configurations, tenant pinning and cross-tenant reuse rules, the Lenny scrub procedure for recycling pods, slot assignment atomicity, slot retry policy, execution mode scaling implications (mode-adjusted formulas), pool taxonomy, and bootstrap behavior.
```

### 6.22 `spec/05_runtime-registry-and-pool-model.md` §5.2 (execution modes and the sessionPolicy block)

This is the central §5.2 rewrite. It replaces the `session | task | concurrent` mode set and the `taskPolicy` YAML with the `session` and `service` modes and the Section 3.1 `sessionPolicy` block, restates tenant pinning and the cross-tenant controls as `sessionPolicy` derivations (the microvm gate and the T4 prohibition re-key from `taskPolicy.allowCrossTenantReuse` and the task-mode assignment rule onto `recycle.allowCrossTenantReuse` and the sequential-reuse path), replaces the between-task lifecycle with the recycle lifecycle and its two scrub-report RPCs, and replaces the Full-level-only task-mode reuse restriction with the statement that recycling works at every integration level. The `#### Execution Modes` heading is retained. Replace the block from the paragraph beginning `All three execution modes are implemented in v1.` through the bullet ending `warm pool sizing should account for per-task pod replacement latency.` (near lines 379-420) with:

````
The `session` and `service` execution modes are both implemented in v1. Graph mode is removed as a separate concept; graph-aware runtimes are session-mode runtimes. In v1, graph-aware runtimes may emit OpenTelemetry spans using their own OTel SDK configured against the OTLP collector endpoint injected in the adapter manifest as `observability.otlpEndpoint`. Lenny does not define a dedicated span emission tool or RPC in v1; runtimes use standard OTLP libraries directly. A dedicated `lenny/emit_span` MCP tool is deferred to post-v1.

```yaml
executionMode: session | service
```

The mode names follow the unit of the client contract. In `session` mode the session is the managed unit: the gateway binds each session to a claimed pod and manages its workspace, lifecycle, and recovery. In `service` mode the runtime is a replicated service: the gateway routes each message to any ready tenant-pinned replica, creates no `SandboxClaim`, and materializes no workspace (see the `service` definition below).

**`session`** — the session is bound to a claimed pod for the session's lifetime. Default mode. Session mode is parameterized by the `sessionPolicy` block. In the default configuration (`maxConcurrentSessions: 1`, `recycle.enabled: false`) each pod is exclusive to one session and terminates when the session ends.

```yaml
sessionPolicy:                            # session mode only
  maxConcurrentSessions: 1                # simultaneous sessions per pod; > 1 requires acknowledgeProcessLevelIsolation
  acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1 — see the concurrent-session acknowledgment below
  recycle:
    enabled: false                        # serve successive sessions on one pod with a scrub between them
    acknowledgeBestEffortScrub: false     # required when enabled: true — the workspace scrub is best-effort
    maxSessionsPerPod: 50                 # required when enabled — counts every session served; pod retired at the bound
    maxPodUptimeSeconds: 86400            # optional — pod retired after this uptime (seconds)
    maxScrubFailures: 3                   # pod retired after this many cumulative scrub failures (default: 3)
    onScrubFailure: warn                  # warn | fail — see the onScrubFailure behaviors below
    scrubProfile: standard                # standard | vm-restart | in-place — see the Kata/microvm scrub variant below
    acknowledgeMicrovmResidualState: false # required when scrubProfile: in-place
    allowCrossTenantReuse: false          # requires isolationProfile: microvm; never permitted when maxConcurrentSessions > 1
  cleanupCommands: []                     # deployer cleanup executed before the whole-pod scrub
  cleanupTimeoutSeconds: 60
  maxSessionRetries: 1                    # crash re-dispatch attempts (default: 1, giving 2 total attempts; 0 disables retries)
  maxSessionAgeSeconds: 7200              # wall-clock session age cap (default: 7200)
  maxClientIdleSeconds: 7200              # client-inactivity bound; defaults to the effective maxSessionAgeSeconds (see Section 6.2)
  slotRetries: 1                          # retries for failed slots when maxConcurrentSessions > 1 (default: 1)
  onPoolExhausted: reject                 # reject | queue — see below
  maxQueueWaitSeconds: 30                 # queue wait bound when onPoolExhausted: queue
```

Common configurations of the block:

| Configuration | `maxConcurrentSessions` | `recycle.enabled` | Behavior |
|:--|:--|:--|:--|
| One session per pod (default) | 1 | `false` | The pod is exclusive to a single session and terminates when the session ends. |
| Pod reuse | 1 | `true` | The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions. |
| Concurrent | N | `true` | The pod serves up to N simultaneous sessions in per-slot workspaces and recycles when occupancy reaches zero. |
| Bounded cohort | N | `false` | The pod serves up to N simultaneous sessions, then terminates after the cohort drains. |

Acknowledgments, tenant pinning, `residualStateWarning`, and the scaling factors are derived from `sessionPolicy` properties rather than keyed to mode names:

- `recycle.acknowledgeBestEffortScrub: true` is required when `recycle.enabled: true`; the workspace scrub is best-effort (see the deployer acknowledgment below).
- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`; concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below).
- `recycle.acknowledgeMicrovmResidualState: true` is required when `recycle.scrubProfile: in-place` (see the Kata/microvm scrub variant below).
- Tenant pinning is required when `maxConcurrentSessions > 1` or `recycle.enabled: true` (see tenant pinning below).
- `capabilities.preConnect: true` is admitted only when `maxConcurrentSessions` is 1, and service-mode pools reject it ([Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).

**`maxClientIdleSeconds`** terminates a session after continuous client inactivity. It is the single platform idle bound; the activity definition and the per-state pause table are specified in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine). The default is the pool's effective `maxSessionAgeSeconds`, so the bound binds before the age cap only when a deployer lowers it or when the session accrues idle time in a state where `maxSessionAge` is paused but the idle clock runs.

**`onPoolExhausted`** parameterizes the per-pool claim queue ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). `reject` keeps the bounded claim-path wait and the Postgres fallback before returning `WARM_POOL_EXHAUSTED`. `queue` holds the request in the same per-pool FIFO for up to `maxQueueWaitSeconds` after the claim-path timeout and the Postgres fallback are exhausted, re-entering acquisition as pods free; a queued request holds no pod, slot, or claim, and the client receives a `session_id` only on success. On timeout the gateway returns `WARM_POOL_EXHAUSTED` with a `Retry-After` header.

**Tenant pinning:** A session-mode pod whose pool sets `recycle.enabled: true` or `maxConcurrentSessions > 1` is pinned to a single tenant for its entire lifetime; service-mode pods are pinned by the tenant-affinity routing in the `service` definition below. The gateway MUST NOT assign a pinned pod to a different tenant than its first assignment. This is enforced at two independent layers:

1. **Application layer (gateway):** Each pinned pod records its `tenantId` on first use, and subsequent assignment requests verify `tenantId` match before routing. The gateway reads the pin from the `lenny.dev/tenant-id` Pod label it stamps at first assignment.
2. **Kubernetes layer (admission webhook):** Warm-pool pods are labeled `lenny.dev/tenant-id: {tenant_id}` at first assignment time by the gateway agent. A `ValidatingAdmissionWebhook` (`lenny-tenant-label-immutability`) rejects any request that mutates the `lenny.dev/tenant-id` label on an existing pod to a different non-empty value. The only permitted transitions are: unset → `{tenant_id}` (initial assignment) and `{tenant_id}` → `unassigned` (pod return to pool). Any other mutation is rejected with HTTP 403 and error `tenant_label_immutable`. This webhook provides defense-in-depth: even if the gateway's application-layer logic has a bug, no Kubernetes API call can silently re-label a pod to a different tenant. The webhook runs in **fail-closed** mode (`failurePolicy: Fail`) and is deployed with `replicas: 2` and a PodDisruptionBudget (`minAvailable: 1`), matching the HA requirements of the `lenny-label-immutability` webhook ([Section 13.2](13_security-model.md#132-network-isolation)). The `unset → {tenant_id}` transition is authorized only for the gateway ServiceAccount (`system:serviceaccount:lenny-system:lenny-gateway`); the `{tenant_id} → unassigned` transition is authorized only for the WarmPoolController ServiceAccount (`system:serviceaccount:lenny-system:lenny-controller`). The webhook is deployed as part of the Helm chart under `templates/admission-policies/` and its correctness is covered by the admission policy integration test suite (`tests/integration/admission_policy_test.go`).

The pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated `recycle.allowCrossTenantReuse`: a pinned idle pod is claimable only by its pinned tenant, the candidate scan filters on the pin label, and inventory accounting counts pinned-idle pods as idle inventory available to that tenant alone. The `{tenant_id} → unassigned` transition applies only where cross-tenant reuse is permitted.

Cross-tenant pod reuse is only permitted on the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`), with `microvm` isolation and an explicit `allowCrossTenantReuse: true` field in `sessionPolicy.recycle`, where the VM boundary provides a VM-level isolation boundary that is significantly stronger than runc or gVisor but shares host virtio devices. Kata provides isolation appropriate for cross-tenant sequential reuse where tenants have been independently vetted, but it is not equivalent to dedicated hardware isolation. The pool controller rejects `recycle.allowCrossTenantReuse: true` on any pool whose `isolationProfile` is not `microvm` at validation time with a descriptive error. Cross-tenant slot sharing when `maxConcurrentSessions > 1` is categorically prohibited regardless of isolation profile (see the cross-tenant prohibition below).

**T4 cross-tenant reuse prohibition.** The pool controller additionally rejects `recycle.allowCrossTenantReuse: true` on any pool whose associated Runtime is configured with `workspaceTier: T4` ([Section 12.9](12_storage-architecture.md#129-data-classification)). T4 workloads require dedicated node pools for per-tenant key isolation ([Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout)); cross-tenant pod reuse, even with microvm isolation, places two tenants' data within the same shared microvm host and violates the dedicated-node boundary that T4 requires. The rejection error is: `"recycle.allowCrossTenantReuse: true is not permitted for T4-tier pools (workspaceTier: T4); T4 workloads require dedicated node pools (Section 6.4)"`. The gateway also enforces this at session assignment time: if a T4 session request would route to a sequential-reuse pod (`maxConcurrentSessions: 1`, `recycle.enabled: true`) already bound to a different tenant, the assignment is rejected and the pod is retired from the cross-tenant pool. This guards against misconfigured pools that bypass the pool controller validation.

**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, reported by the adapter via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)), and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`.

**Recycle lifecycle (`recycle.enabled: true`).** When the pod's occupancy reaches zero, the gateway patches the pod's `SandboxClaim` to `recycling` and the whole-pod boundary runs: the adapter removes `/run/lenny/credentials.json` (the credential purge precedes any deployer code; see scrub step 0 below), deployer-defined `cleanupCommands` execute (with access to session state but without the previous session's credential file), the Lenny whole-pod scrub runs, and the adapter reports the binary outcome via `ReportPodScrub`. On a successful scrub the pod is held for its tenant through the claim's `reserved` state and serves the next session; on a preConnect pool the SDK re-warm runs after the scrub reports success and before the claim enters `reserved`, so every pod that reaches `reserved` or `idle` is SDK-warm ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). A session that ends in failure or a crash always retires its pod regardless of recycle settings. `setupCommands` run once per pod at start; per-session setup belongs in the runtime's initialization.

**Recycling and integration levels.** Pod recycling requires no runtime cooperation: the per-slot cleanup and the whole-pod scrub are adapter-executed and gateway-coordinated, with no lifecycle-channel exchange between sessions. Recycling works at every integration level (Basic, Standard, and Full), and `recycle.maxSessionsPerPod` applies as configured at every level.
````

### 6.23 `spec/05_runtime-registry-and-pool-model.md` §5.2 (scrub procedure)

The scrub steps survive unchanged; the framing paragraphs re-key from the between-task boundary to the occupancy-zero recycle boundary, the microvm variant re-keys onto `recycle.allowCrossTenantReuse` and the `scrubProfile` value set, and the per-session vocabulary replaces the per-task vocabulary in the step prose.

Replace the paragraph beginning `**Lenny scrub procedure.** The scrub has two phases` (near line 422) with:

```
**Lenny scrub procedure.** The whole-pod scrub runs at the recycle boundary, when occupancy reaches zero on a recycling pod and the pod's claim is `recycling` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). The scrub has two phases: a **pre-cleanup credential purge** (step 0) executed by the adapter before `cleanupCommands`, and a **post-cleanup scrub sequence** (steps 1-6) executed after `cleanupCommands` finish. The adapter reports the scrub's binary outcome to the gateway via `ReportPodScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)); a missing report is bounded by a gateway-side timeout (`cleanupTimeoutSeconds` plus a grace period), after which the pod is retired.
```

Replace the paragraph beginning `**Step 0 (pre-cleanup, before `cleanupCommands`):**` (near line 424) with:

```
**Step 0 (pre-cleanup, before `cleanupCommands`):** Remove `/run/lenny/credentials.json`. The credential file is a platform-managed security artifact rather than deployer session state, and MUST be purged before any deployer-defined code runs. This prevents `cleanupCommands` (which may be deployer-defined and potentially untrusted or buggy) from reading the previous session's credential file. If `cleanupCommands` require credential metadata for custom audit logging, the adapter exposes sanitized metadata (provider name, lease ID) in the cleanup environment variables `LENNY_PREV_CREDENTIAL_PROVIDER` and `LENNY_PREV_LEASE_ID` rather than leaving the full credential file accessible.
```

Replace the list item `3. Purge environment variables injected for the previous task (tracked by the adapter; restored to the pod baseline set recorded at first boot).` (near line 431) with:

```
3. Purge environment variables injected for the previous session (tracked by the adapter; restored to the pod baseline set recorded at first boot).
```

Replace the paragraph beginning `The scrub is **best-effort, not a security boundary**` (near line 436) with:

```
The scrub is **best-effort** and is not a security boundary: it reduces cross-session data leakage within a single tenant but does not replace isolation. This is why recycling pods are tenant-pinned (see above). Specifically, the scrub **cannot** address the following residual state vectors: kernel TCP socket `TIME_WAIT` state and connection tracking entries, DNS resolver cache in long-lived `nscd` or `systemd-resolved` processes (killed in step 1 but cache may be observable via timing during the kill window), kernel buffer/page cache priming (files read by a previous session remain in page cache, observable via timing), `inotify` and `fanotify` watch registrations (cleared only when the owning process is killed), and named pipes or UNIX domain sockets outside managed paths. (`shmget`-allocated IPC shared memory segments are addressed by step 1b above.) Deployers should evaluate whether these residual vectors are acceptable for their workload's sensitivity level.
```

Replace the paragraph beginning `**Kata/microvm scrub variant.** When `isolationProfile: microvm` and `allowCrossTenantReuse: true`` (near line 438) with:

```
**Kata/microvm scrub variant.** When `isolationProfile: microvm` and `recycle.allowCrossTenantReuse: true`, the standard Lenny scrub (steps 1-6 above) is insufficient because the guest VM itself persists across sessions: the guest kernel's DNS resolver cache, TCP connection tracking state, kernel buffer/page cache, and in-memory filesystem metadata survive the scrub. For cross-tenant sequential reuse on microvm pods, the scrub procedure includes an additional step after step 6 (the `vm-restart` scrub profile):
```

The guest VM restart step (step 7, near line 440) is unchanged. Replace the paragraph beginning `If a deployer requires cross-tenant reuse without the guest restart latency cost` (near line 442) with:

```
The scrub procedure is selected by `recycle.scrubProfile`: `standard` (the default) runs steps 0-6, `vm-restart` adds the guest restart in step 7, and `in-place` runs only steps 0-6 inside the continuing VM guest. On a pool with `recycle.allowCrossTenantReuse: true`, `scrubProfile: standard` is insufficient and the pool controller rejects it at validation time; such pools set `vm-restart`, or `in-place` when the deployer requires cross-tenant reuse without the guest restart latency cost. In `in-place` mode the following residual state vectors are documented as persisting across tenant boundaries: guest kernel DNS resolver cache, guest kernel TCP `TIME_WAIT` state, guest kernel buffer/page cache priming, and guest kernel inotify/fanotify registrations. The `in-place` profile requires an additional acknowledgment: `acknowledgeMicrovmResidualState: true` in `sessionPolicy.recycle`. The pool controller rejects `scrubProfile: in-place` without this acknowledgment.
```

### 6.24 `spec/05_runtime-registry-and-pool-model.md` §5.2 (onScrubFailure behaviors)

The `onCleanupFailure` block renames to `onScrubFailure`, the two scrub-failure metrics rename to the `lenny_pod_*` family, the `task_cleanup → sdk_connecting [scrub_warning]` cross-reference re-keys to the recycle re-warm edge `claimed → sdk_connecting`, and the "accepts the next task" language restates in recycle terms. Replace the block from the line `**`onCleanupFailure` behaviors:**` through the bullet ending `The failed pod's metadata is retained in the audit log for inspection.` (near lines 444-447) with:

```
**`onScrubFailure` behaviors:**

- **`warn`** (default) — the pod is returned to the available pool with a `scrub_warning` annotation. The gateway logs the failure, increments `lenny_pod_scrub_failure_total` (aggregate counter) and `lenny_pod_scrub_failure_count` (per-pod gauge, labeled by `k8s_pod_name`, `pool`, `runtime_class`), and the pod serves the next session. The deployer accepts residual state risk. **preConnect interaction:** for pools where the runtime declares `capabilities.preConnect: true`, the pod routes through the recycle re-warm edge `claimed → sdk_connecting` before reaching `reserved` even on a `scrub_warning` outcome (see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) and the "preConnect re-warm on scrub_warning" note). This preserves the Section 6.1 invariant that every pod reaching `reserved` or `idle` in a preConnect pool is SDK-warm; the `scrub_warning` annotation and cumulative failure count persist through the re-warm. When the pod's cumulative scrub failure count reaches `maxScrubFailures` (default: `3`), the pod is retired and terminated regardless of the `onScrubFailure` setting; the gateway provisions a replacement from the warm pool and logs the retirement reason as `scrub_failure_limit_reached`.
- **`fail`** — the pod is removed from the pool and terminated. The gateway provisions a replacement pod from the warm pool. The failed pod's metadata is retained in the audit log for inspection.
```

### 6.25 `spec/05_runtime-registry-and-pool-model.md` §5.2 (pod retirement policy)

The retirement-policy paragraph is the second spec home of the per-pod retirement counter and its `reason` vocabulary (alongside the §16.1 inventory row). The title, the trigger bullets, the metric name, and the `reason` value set restate in session and recycle terms in lockstep with the §16 re-keys. Replace the block from the paragraph beginning `**Task-mode pod retirement policy.**` through the paragraph ending `logs the retirement with the pod's lifetime task count and uptime.` (near lines 449-455) with:

```
**Pod retirement policy (recycling pools).** A recycling pod is retired (transitioned to `draining` and then terminated) when any of the following conditions is met:

- **Session count limit:** The pod's served-session count reaches `recycle.maxSessionsPerPod`. After the current session ends and the whole-pod scrub finishes, the pod transitions to `draining` instead of returning to `idle`. A replacement pod is provisioned from the warm pool. `maxSessionsPerPod` is required with no default when `recycle.enabled: true`; the deployer must make an explicit choice based on the workload's sensitivity and the residual state vectors enumerated above.
- **Uptime limit:** The pod's wall-clock uptime since first boot exceeds `recycle.maxPodUptimeSeconds`. The gateway checks uptime before dispatching the next session; if the pod has exceeded the limit, it transitions to `draining` after its current sessions end. `maxPodUptimeSeconds` is optional; if omitted, only `maxSessionsPerPod` and `maxScrubFailures` govern retirement.
- **Scrub failure limit:** The pod's cumulative scrub failure count reaches `recycle.maxScrubFailures` (see `onScrubFailure: warn` above).

A session that ends in failure or a crash also retires the pod, regardless of recycle settings. When a pod is retired, the gateway increments `lenny_pod_retirement_total` (labeled by `reason`: `session_count_limit`, `uptime_limit`, `scrub_failure_limit`) and logs the retirement with the pod's lifetime session count and uptime.
```

### 6.26 `spec/05_runtime-registry-and-pool-model.md` §5.2 (deployer acknowledgment for recycling)

The acknowledgment recap re-keys from task mode onto `recycle.enabled` and the `sessionPolicy.recycle` structure. Replace the block from the paragraph beginning `**Deployer acknowledgment.** Because workspace scrub is best-effort, deployers must set an explicit acknowledgment flag to enable task mode:` through the paragraph ending `forcing the deployer to make an explicit reuse-limit choice appropriate to their workload.` (near lines 457-473) with:

````
**Deployer acknowledgment.** Because the workspace scrub is best-effort, deployers must set an explicit acknowledgment flag to enable recycling:

```yaml
sessionPolicy:
  recycle:
    enabled: true
    acknowledgeBestEffortScrub: true # required — recycling rejected without this
    allowCrossTenantReuse: false # requires isolationProfile: microvm; sequential reuse only
    scrubProfile: standard # standard | vm-restart | in-place
    # acknowledgeMicrovmResidualState: false # required when scrubProfile: in-place
    maxScrubFailures: 3 # pod retired after this many cumulative failures (default: 3)
    maxSessionsPerPod: 50 # required — no default, forces deployer choice
    maxPodUptimeSeconds: 86400 # optional — retirement after uptime threshold
  cleanupCommands: [...]
  cleanupTimeoutSeconds: 30
```

If `acknowledgeBestEffortScrub` is absent or `false` on a pool with `recycle.enabled: true`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section. Similarly, `maxSessionsPerPod` is required with no default when recycling is enabled; the pool controller rejects recycling pool definitions that omit it, forcing the deployer to make an explicit reuse-limit choice appropriate to their workload.
````

### 6.27 `spec/05_runtime-registry-and-pool-model.md` §5.2 (client visibility of weak-isolation reuse)

The client-facing security-visibility paragraph retitles to the session and service vocabulary, replaces its task-mode references with the broadened derivation, and restates the `residualStateWarning` semantics to match the Section 4 derivation (`podReuse` and `residualStateWarning` are true when `recycle.enabled` is true, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`). Replace the paragraph beginning `**Client visibility of task-mode isolation.**` (near line 475) with:

```
**Client visibility of weak-isolation reuse.** Because `acknowledgeBestEffortScrub` and `acknowledgeProcessLevelIsolation` are deployer-level configuration, clients creating sessions against a pool that recycles pods, that runs `maxConcurrentSessions > 1`, or that runs in service mode have no independent mechanism to determine their isolation posture unless the platform surfaces it explicitly. To address this, the session creation response (`POST /v1/sessions`) includes a `sessionIsolationLevel` object containing `executionMode`, `isolationProfile`, `podReuse`, `scrubPolicy`, `residualStateWarning`, and `conversationContinuity` — see [Section 7.1](07_session-lifecycle.md#71-normal-flow) for the full field definitions. `podReuse` and `residualStateWarning` are `true` when `recycle.enabled` is `true`, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`. When `residualStateWarning: true`, the session runs on a pod that serves more than one session over its lifetime: on a recycling pod the scrub is best-effort and residual state vectors (DNS cache, TCP `TIME_WAIT`, page cache, etc.) may be observable from prior sessions; concurrent slots share process-level state; and service-mode pods serve successive requests with no scrub. Clients that require strict isolation should check `residualStateWarning` in the response and reject sessions where this field is `true` if their use case cannot tolerate residual state.
```

### 6.28 `spec/05_runtime-registry-and-pool-model.md` §5.2 (concurrent sessions on one pod)

The `concurrent` mode definition, the `concurrencyStyle` enum, the `concurrentWorkspacePolicy` block, and the concurrent-workspace tenant-pinning paragraph collapse into concurrent-session prose keyed to `sessionPolicy.maxConcurrentSessions > 1`. The categorical cross-tenant prohibition (currently keyed to concurrent-workspace mode) re-keys to `maxConcurrentSessions > 1` and `recycle.allowCrossTenantReuse`, and stays distinct from the sequential-reuse microvm gate. Replace the block from the paragraph beginning `**`concurrent`** — multiple tasks on a single pod simultaneously.` through the paragraph ending `cross-tenant slot sharing has no isolation boundary in concurrent-workspace mode"`.` (near lines 477-498) with:

```
**Concurrent sessions (`maxConcurrentSessions > 1`).** Setting `sessionPolicy.maxConcurrentSessions` above 1 multiplexes simultaneous sessions onto a single pod, each in its own slot. Each slot gets its own workspace under `/workspace/slots/{slotId}/` (see [Section 6.4](06_warm-pod-model.md#64-pod-filesystem-layout) for the full per-slot filesystem layout). The gateway tracks per-slot lifecycle. Message delivery uses `slotId` multiplexing over stdin: the adapter assigns a `slotId` per slot, creates the per-slot directory tree, and sets the slot's `cwd` to `/workspace/slots/{slotId}/current/`; the runtime implements a dispatch loop keyed on `slotId`; all binary protocol messages (inbound and outbound) carry `slotId` when `maxConcurrentSessions > 1`. Cross-slot isolation is process-level and filesystem-level, which is weaker than the one-session-per-pod configuration.

**Deployer acknowledgment (concurrent sessions).** Because concurrent sessions share a single pod's process namespace, `/tmp`, cgroup memory, and network stack across all simultaneous slots, deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` to configure `maxConcurrentSessions > 1`. If `acknowledgeProcessLevelIsolation` is absent or `false`, the pool controller rejects the pool definition at validation time with a descriptive error referencing this section and listing the specific isolation properties the deployer is accepting: shared process namespace, shared `/tmp`, shared cgroup memory, shared network stack, and **shared credential-file group-read access** (each slot's `/run/lenny/slots/{slotId}/credentials.json` is readable by every slot's agent process via the shared `lenny-cred-readers` supplementary group — see [§13.1](13_security-model.md)) between concurrent slots. The rejection message additionally enumerates network-level side-channels inherent to the shared network namespace: (a) cross-slot network traffic observation via raw sockets, (b) port binding conflicts between slots, (c) DNS resolver cache poisoning where one slot's DNS queries populate cached entries visible to other slots, and (d) network activity timing patterns observable across slots. To mitigate raw socket sniffing, the agent container's `securityContext` MUST drop `CAP_NET_RAW` (the `SandboxWarmPool` CRD validation webhook rejects pool definitions where `maxConcurrentSessions > 1` and the pod template grants `CAP_NET_RAW`). Deployers requiring network isolation or credential-lease isolation between concurrent sessions should set `maxConcurrentSessions: 1`.

**Cross-tenant prohibition (concurrent sessions).** Concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack *simultaneously*, so simultaneous process-level cotenancy has no isolation boundary. Cross-tenant slot sharing is therefore never permitted when `maxConcurrentSessions > 1`, regardless of `isolationProfile` or `recycle.scrubProfile`; the microvm gate above applies only to the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`). The pool controller rejects any pool definition where `maxConcurrentSessions > 1` and `recycle.allowCrossTenantReuse: true` at validation time with error: `"recycle.allowCrossTenantReuse: true is not permitted when maxConcurrentSessions > 1; cross-tenant slot sharing has no isolation boundary"`. Tenant pinning for concurrent-session pods follows the derivation above (pinning is required when `maxConcurrentSessions > 1`) and is enforced by the same two layers.
```

### 6.29 `spec/05_runtime-registry-and-pool-model.md` §5.2 (service mode)

The `concurrencyStyle: stateless` definition moves to the `service` mode definition with the claimless routing model, the retained pool-level `maxConcurrent` capacity (the `maxConcurrent: 8` example and the readiness-driven routing prose carry onto it, since `sessionPolicy` does not apply to service mode), the multi-turn contract from Section 3.6, the structural cross-tenant posture, and the renamed limitations block with the connector guidance kept. Replace the block from the paragraph beginning `**`concurrencyStyle: stateless`** — no workspace materialization.` through the blockquote bullet ending `Connectors are the recommended long-term target for stateless workloads.` (near lines 500-510) with:

````
**`service`** — the runtime is a replicated service with no Lenny-managed session state. There is no workspace materialization and no `SandboxClaim`. The gateway routes each message through the pool's Kubernetes Service with **tenant-affinity routing**: it maintains an in-memory mapping of `tenantId → set of pinned pod IPs` and uses Kubernetes `EndpointSlice` watches to discover pod IPs behind the Service. On first request for a tenant, the gateway selects an unpinned pod from the Service's endpoints, pins it to the tenant (applying the `lenny.dev/tenant-id` label), and records the mapping. Subsequent requests for the same tenant are routed directly to a pinned pod IP (bypassing the Service load balancer) via the gateway's HTTP client. If all pinned pods for a tenant are at slot capacity (readiness probe `false`), the gateway selects a new unpinned pod and pins it. This ensures tenant affinity is enforced despite the Service-based routing model. The pool-level `maxConcurrent` field is the service-mode per-pod request capacity (`sessionPolicy` and its `maxConcurrentSessions` apply only to session mode): the pod readiness probe reflects slot availability, and the PoolScalingController watches `active_slots / (pod_count × maxConcurrent)`.

```yaml
executionMode: service
maxConcurrent: 8 # per-pod concurrent request capacity; readiness reports false at capacity
```

**Session contract (service mode).** Sessions remain the API surface (`POST /v1/sessions`, `lenny/create_session`, `send_message`); a service-mode session is a connection handle. Service mode provides no cross-message conversation continuity: every message is self-contained, and the platform may route successive messages of one session to different replicas. `capabilities.interaction: multi_turn` runtimes are permitted; clients of multi-turn runtimes re-inject any needed context into each message's `input`. The session creation response carries `sessionIsolationLevel.conversationContinuity: "none"` for service-mode pools and `"platform"` otherwise ([Section 7.1](07_session-lifecycle.md#71-normal-flow)), and runtime registration emits a warning event when a `multi_turn` runtime is bound to a service-mode pool. Service-mode pools report `podReuse: true`, `residualStateWarning: true`, and `scrubPolicy: "none"`: pods serve successive requests with no scrub and share process space, network stack, `/tmp`, and page cache across same-tenant concurrent requests.

**Tenant isolation (service mode).** Service-mode pods are tenant-pinned using the same two-layer mechanism as pinned session-mode pods: (1) the gateway records `tenantId` on first request routed to the pod and rejects subsequent requests with a mismatched `tenantId`, and (2) the `lenny-tenant-label-immutability` `ValidatingAdmissionWebhook` prevents mutation of the `lenny.dev/tenant-id` label. Although service-mode pods have no Lenny-managed workspace or session state, they share a network namespace and process space across all concurrent requests; cross-tenant routing would expose tenant-specific network traffic patterns and process metadata. Cross-tenant reuse is structurally unrepresentable in service mode: the only cross-tenant-reuse field, `sessionPolicy.recycle.allowCrossTenantReuse`, applies to session mode alone, so a service-mode pool has no configuration that could permit serving two tenants from one pod.

> **Service-mode limitations (v1).** `executionMode: service` provides only minimal platform guarantees compared to session mode. There is no workspace delivery, no per-slot lifecycle tracking, no slot-level retry policy, no checkpoint support, and no per-slot failure isolation; a pod failure affects all concurrent requests routed to it. The gateway's role is limited to load-balanced routing via a Kubernetes Service; it does not track individual request outcomes. Deployers are responsible for all retry, idempotency, and error-handling logic in their runtime.
>
> **Preferred alternative:** Truly stateless runtimes that do not need workspace materialization or session lifecycle management are better registered as **external connectors** (see [Section 9.3](09_mcp-integration.md#93-connector-definition-and-oauthoidc)). Connectors integrate with Lenny's routing and observability without incurring pod warm-pool overhead or requiring a Lenny-managed runtime adapter. `executionMode: service` exists for runtimes that are already deployed as Lenny pods and have minimal statefulness, but where migrating to the connector model is not yet feasible. New deployments with no workspace requirements should use connectors instead.
>
> **Choosing between `service` mode and connectors:**
> - Use `service` mode if: you already have a Lenny-managed pod image and want simple horizontal scaling with Lenny's pool management and readiness-probe-driven routing.
> - Use connectors if: your runtime is independently deployed, has its own scaling, or you need richer failure semantics. Connectors are the recommended long-term target for stateless workloads.
````

### 6.30 `spec/05_runtime-registry-and-pool-model.md` §5.2 (slot failure, cleanup, checkpoint, and contention bullets)

The titled slot bullets drop the concurrent-workspace mode name and rename the slot cap from `maxConcurrent` to `sessionPolicy.maxConcurrentSessions` in every formula, example, and threshold, so the intra-pod capacity gate is capped by the session-mode bound. The per-slot cleanup additionally gains its `ReportSessionScrub` reporting linkage from Section 3.4. The `mode_factor` reference in the resource-contention bullet stays a scaling-factor derivation and is not renamed. Replace the block from the paragraph beginning `**Concurrent-workspace slot failure and cleanup.**` through the bullet ending `Future versions may introduce per-slot resource quotas via cgroup nesting.` (near lines 512-517) with:

```
**Slot failure and cleanup (`maxConcurrentSessions > 1`).** Slots fail independently — a single slot failure does not terminate the pod or affect other active slots. Per-slot behavior:

- **Failure isolation:** When a slot's session fails (runtime error, pod-level OOM kill, or unhandled exception), the adapter marks that `slotId` as `failed` and emits `lenny_slot_failure_total{error_type}`. Other slots continue unaffected. The gateway is notified via the lifecycle channel and applies the slot retry policy below.
- **Slot cleanup:** On slot completion or failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the `slotId`. The adapter reports each slot cleanup outcome (`released` or `leaked`) to the gateway via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)). Cleanup timeout is `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds (minimum 5s enforced at runtime by the adapter). **CRD validation rule:** The `SandboxWarmPool` admission webhook rejects any pool configuration where `cleanupTimeoutSeconds / maxConcurrentSessions < 5`, i.e., where `cleanupTimeoutSeconds < maxConcurrentSessions × 5`. Rejection error: `422 INVALID_POOL_CONFIGURATION` with message `"cleanupTimeoutSeconds / maxConcurrentSessions would produce a per-slot cleanup timeout below the 5s minimum; set cleanupTimeoutSeconds ≥ maxConcurrentSessions × 5"`. This CRD validation is intentionally stricter than the runtime formula requires: the runtime formula `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` would clamp sub-5s results to 5s regardless, but the CRD validation rejects such configurations at admission time to ensure the deployer's configured `cleanupTimeoutSeconds` produces a meaningful per-slot budget above the minimum floor rather than silently relying on the runtime clamp. If cleanup fails, the slot is leaked — the pod continues but the slot is not reclaimed until pod termination. See **`leaked` slot semantics** ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) for the full specification of leaked slot behavior: leaked slots remain counted in the slot counter, count toward the unhealthy threshold, and are surfaced via the `lenny_adapter_leaked_slots` gauge.
- **Checkpoint granularity:** Checkpoints are per-slot. Each slot's checkpoint includes only that slot's workspace state and conversation history. Whole-pod checkpoints are not taken when `maxConcurrentSessions > 1` because slot lifecycles are independent. Per-slot checkpoints are subject to the same tiered cap as one-session-per-pod checkpoints ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling) preStop hook, stage 2): the cap is selected based on the slot's last measured workspace size (`last_checkpoint_workspace_bytes` for the `(session_id, slot_id)` pair in Postgres). The total preStop budget for a pod with `maxConcurrentSessions > 1` is the **sum** of per-slot caps across all active slots; the `SandboxWarmPool` CRD validation webhook enforces that `maxConcurrentSessions × max_tiered_checkpoint_cap + checkpointBarrierAckTimeoutSeconds + 30 ≤ terminationGracePeriodSeconds`. Deployers must set `terminationGracePeriodSeconds` accordingly when `maxConcurrentSessions > 1` — the Helm chart provides a helper formula in `values.yaml` comments. **Node drain timeout interaction:** At high `maxConcurrentSessions` values, the required `terminationGracePeriodSeconds` can exceed typical cluster automation drain timeouts (e.g., `maxConcurrentSessions: 8` with 512 MB workspaces yields 8 × 90 + 90 + 30 = 840s, or 14 minutes). If `terminationGracePeriodSeconds` exceeds the node drain timeout (commonly 600s), the kubelet will SIGKILL the pod before checkpoints complete, causing data loss for in-flight slots. Deployers MUST ensure that the cluster's node drain timeout (`--pod-eviction-timeout` on the kube-controller-manager, or the equivalent setting in managed Kubernetes node group configurations) is at least as large as the pool's `terminationGracePeriodSeconds`. The `SandboxWarmPool` CRD validation webhook emits a warning (not a rejection) when the computed `terminationGracePeriodSeconds` floor exceeds 600s: `"terminationGracePeriodSeconds floor (<value>s) exceeds 600s; verify that cluster node drain timeout is configured to accommodate this value"`. Additionally, the pool definition supports an optional `maxTerminationGracePeriodSeconds` field (default: unset) that, when set, causes the CRD validation webhook to **reject** (not warn) any pool configuration whose computed `terminationGracePeriodSeconds` floor exceeds the configured value. This provides a hard ceiling for deployments where the cluster's drain timeout is known and non-negotiable, reconciling the warning-only default with the fail-closed philosophy applied elsewhere. The `lenny-preflight` Job ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) also checks for this condition and emits a preflight warning. **Eviction checkpoint ordering:** Eviction checkpoints for concurrent-session pods are serialized across slots (not fully parallel) to avoid MinIO write amplification. The adapter processes one slot's checkpoint upload at a time, in slot-ID order. This prevents `maxConcurrentSessions` simultaneous uploads from saturating MinIO write bandwidth during a degraded-MinIO scenario, where all uploads would individually exhaust their retry budgets and cascade to `maxConcurrentSessions` simultaneous Postgres fallback writes. The per-slot tiered cap applies to each slot's upload; the total preStop budget (the sum of per-slot caps) accommodates serialized execution. The Postgres fallback retry budget (60s per slot) also runs serially. The CRD validation formula uses `max_tiered_checkpoint_cap` per slot; for pools where `max_tiered_checkpoint_cap ≥ 60s` (workspaces > 100 MB), this subsumes the Postgres fallback budget. For pools with smaller workspaces (`max_tiered_checkpoint_cap = 30s`), the Postgres fallback path (60s per slot) exceeds the per-slot budget assumed by the formula — deployers of small-workspace, high-concurrency pools should use `max(max_tiered_checkpoint_cap, 60)` when computing `terminationGracePeriodSeconds` manually if MinIO degradation is a concern.
- **Resource contention:** CPU and memory are shared across slots (no per-slot cgroup subdivision in v1). If a single slot monopolizes resources, the adapter's health probe degrades and the PoolScalingController reduces `mode_factor` for the pool. Deployers should set `maxConcurrentSessions` conservatively relative to the resource class. Future versions may introduce per-slot resource quotas via cgroup nesting.
```

### 6.31 `spec/05_runtime-registry-and-pool-model.md` §5.2 (slot assignment atomicity and rehydration)

The slot-counter paragraphs rename the cap to `maxConcurrentSessions`, gain the per-pod-claim and §12.4 Redis-outage fallback linkage from Section 3.2, compose pool exhaustion with `onPoolExhausted`, and correct the rehydration index column from `sessions(pod_name)` to `pod_assignment` so the spec, the migrations, and the session store agree on one column name.

Replace the paragraph beginning `**Concurrent-workspace slot assignment atomicity.**` (near line 519) with:

```
**Slot assignment atomicity (`maxConcurrentSessions > 1`).** Slot availability checks and slot reservation must be atomic to prevent double-assignment. The gateway uses an atomic Redis `INCR` with a cap check: the slot counter for a given pod (`lenny:pod:{pod_id}:active_slots`) is incremented only if the resulting value does not exceed `maxConcurrentSessions`. Specifically: the gateway uses a Lua script that atomically checks `GET` and conditionally `INCR` — if `current_count >= maxConcurrentSessions`, the script returns `nil` (slot unavailable) without incrementing; if `current_count < maxConcurrentSessions`, the script increments and returns the new count. This prevents two concurrent session assignments from both reading "1 slot available" on a pod where `maxConcurrentSessions: 1` and both being assigned, which would transiently exceed the pod's slot limit. The counter is the intra-pod capacity gate; pod acquisition itself is guarded by the deterministic per-pod `SandboxClaim` CREATE ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)). During a Redis outage the gateway gates capacity on the Postgres `SessionStore.GetActiveSlotsByPod` check under a per-pod advisory lock, failing closed only after a bounded outage window ([Section 12.4](12_storage-architecture.md#124-redis-ha-and-failure-modes)). If the atomic reservation fails (all slots taken), the gateway falls through to the next available pod in the pool. If all pods in the pool have reached their `maxConcurrentSessions` slot limit, the gateway attempts to claim an additional warm pod from the pool (the standard warm pool claim path). If no warm pods are available, the request is rejected with `WARM_POOL_EXHAUSTED`, subject to the pool's `onPoolExhausted` setting (a `queue` pool first holds the request in the [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) claim queue for up to `maxQueueWaitSeconds`) — the same error code used for pod exhaustion at `maxConcurrentSessions: 1`. The `details.reason` field distinguishes the cause: `"concurrent_slots_exhausted"` indicates that pods exist but all slots are full, whereas the default reason `"no_idle_pods"` indicates no pods are available at all. The metric `lenny_slot_assignment_conflict_total` (counter, labeled by `pool`) tracks atomic reservation failures due to slot contention, enabling operators to detect pool under-sizing.
```

Replace the paragraph beginning `**Post-recovery rehydration atomicity.**` (near line 521) with:

```
**Post-recovery rehydration atomicity.** After a Redis restart, slot counters reset to zero but pods may still have active slots. The Lua script enforces a **blocking rehydration** guarantee via a per-pod `rehydrated` flag (`lenny:pod:{pod_id}:rehydrated`). On every slot allocation attempt, the Lua script first checks whether the `rehydrated` flag exists for the target pod. If the flag is absent (indicating the counter has not been rehydrated since the last Redis restart), the script does **not** proceed with the slot reservation. Instead, it returns a `REHYDRATE_REQUIRED` sentinel value. The calling gateway goroutine then acquires a per-pod rehydration mutex (in-process, with cross-replica coordination via a short-lived Redis `SET NX` lock on `lenny:pod:{pod_id}:rehydrating`), queries `SessionStore.GetActiveSlotsByPod(pod_id)` from Postgres, writes the accurate count to `lenny:pod:{pod_id}:active_slots`, sets the `rehydrated` flag (`SET lenny:pod:{pod_id}:rehydrated 1`), and retries the Lua script. Concurrent allocation attempts for the same pod that arrive while rehydration is in progress block on the `SET NX` lock (with a short spin-wait, bounded by `slotRehydrationTimeoutMs`, default 2000ms) and retry after the flag is set. This eliminates the race window where two simultaneous post-recovery requests could both observe `counter=0` and succeed before rehydration completes. **Scope of blocking:** Rehydration blocks only slot allocations targeting the specific pod being rehydrated; slot allocations for other pods (whether already rehydrated or awaiting their own rehydration) proceed independently. There is no global lock. **Postgres query burst mitigation:** After a Redis restart, the first slot allocation attempt for each concurrent-session pod triggers a `GetActiveSlotsByPod` query. At Tier 3 with hundreds of concurrent-session pods, this produces a burst of Postgres queries as traffic arrives. The queries are naturally staggered by incoming request arrival times (not all pods receive a slot allocation request in the same instant), and the per-pod `SET NX` lock ensures at most one query per pod. The `GetActiveSlotsByPod` query is indexed (`sessions(pod_assignment) WHERE state = 'active'`) and returns at most `maxConcurrentSessions` rows, so each query completes in < 5ms under normal Postgres load. The total rehydration burst at Tier 3 (e.g., 500 pods) is expected to complete within 2-5 seconds of traffic resumption. The `lenny_slot_rehydration_total` counter (labeled by `pod`, `pool`) is emitted on each rehydration event. The `rehydrated` flag has no TTL — it persists until the next Redis restart, at which point all flags are naturally cleared and rehydration is triggered again on first access.
```

### 6.32 `spec/05_runtime-registry-and-pool-model.md` §5.2 (slot retry policy)

The slot retry policy retitles off the removed mode name, exposes the retry bound as `sessionPolicy.slotRetries` (the Section 3.1 block exposes the previously fixed value), renames the replacement threshold's cap, and re-keys the per-task vocabulary. Replace the block from the paragraph beginning `**Concurrent-workspace slot retry policy.**` through the bullet ending `and `error.slotId` identifying the failed slot.` (near lines 523-532) with:

```
**Slot retry policy (`maxConcurrentSessions > 1`).** When a slot fails, the gateway applies the following retry policy (analogous to the pre-attached failure retry policy in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)):

- **Max retries:** `sessionPolicy.slotRetries` (default: 1, giving 2 total attempts including the original; 0 disables retries). The retry is always assigned to a **new slot** on the same pod (if a slot is available) or on a different pod from the pool (if the original pod is fully saturated or unhealthy).
- **Fresh workspace guarantee:** A retried slot always receives a fresh workspace — workspace staging is materialized from scratch. The retried slot never inherits any state from the failed slot's workspace, even if the failed slot's cleanup has not yet completed.
- **Non-retryable failure categories:** The following failure reasons are returned to the client immediately without retry:
  - **OOM** (`reason: oom`) — the same input is likely to OOM again on an identically-sized slot.
  - **Workspace validation error** (`reason: workspace_validation`) — the workspace plan is structurally invalid and will fail on any slot.
  - **Policy rejection** (`reason: policy_rejection`) — the session was rejected by admission policy.
- **Whole-pod replacement trigger:** When `ceil(maxConcurrentSessions / 2)` or more slots on the same pod **fail or leak** within a rolling 5-minute window, the gateway marks the pod as unhealthy, drains remaining slots gracefully, and requests a replacement pod from the warm pool. The `lenny_slot_pod_replacement_total` counter is incremented. Both categories contribute to the threshold: slots that transition to `failed` (runtime error, OOM, unhandled exception, non-retryable failure) and slots that transition to `leaked` (cleanup timeout exceeded; see **`leaked` slot semantics** in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Leaked slots count because they consume slot capacity without being reclaimable, so a pod accumulating leaks is as degraded as one accumulating outright failures. This prevents a degraded pod from consuming retries across many independent slot failures or leaks.
- **Client error on exhaustion:** When a slot fails and either no retry is attempted (non-retryable category) or the retry budget is exhausted, the gateway returns a structured error to the client with `error.category` set to the failure reason, `error.retryable: false` from the platform's perspective (the client may choose to resubmit as a new request), and `error.slotId` identifying the failed slot.
```

### 6.33 `spec/05_runtime-registry-and-pool-model.md` §5.2 (execution mode scaling implications)

The scaling subsection restates the factor derivations per Section 3.1: session mode derives `mode_factor` from expected sessions per pod lifetime (bounded by `recycle.maxSessionsPerPod` and measured by the renamed `lenny_pod_session_reuse_count` histogram) and `burst_mode_factor = maxConcurrentSessions`; service mode derives both factors from the retained `maxConcurrent` field, with the demand-signal series renamed to `lenny_service_requests_total` and `lenny_service_concurrent_active` (this PromQL prose is the sole spec home of those series). The adjusted-formula block and the `(failover_seconds + pod_warmup_seconds)` paragraph carry over unchanged. Replace the block from the paragraph beginning `The default PoolScalingController formula` (near line 540) through the final caveat bullet ending `divisors as concurrent-workspace mode.` (near line 573) with:

````
The default PoolScalingController formula ([Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)) assumes the default session-mode configuration: one session per pod with no reuse. Pod recycling, concurrent sessions, and service mode change the relationship between pod count and effective capacity, so the formula includes a per-pool adjustment factor derived from `sessionPolicy` (session mode) or from `maxConcurrent` (service mode).

**Term definitions:**
- `mode_factor`: pod reuse multiplier — in session mode, the expected number of sessions a pod serves over its lifetime; in service mode, `maxConcurrent`.
- `burst_mode_factor`: burst-term equivalent of `mode_factor`, reflecting slot availability during burst periods.

**Mode adjustment factor (`mode_factor`):**

- **`session`**: `mode_factor = expected_sessions_per_pod_lifetime`. In the default configuration (`maxConcurrentSessions: 1`, `recycle.enabled: false`) each pod serves exactly one session and `mode_factor = 1.0`. On a recycling pool a pod serves multiple sessions before replacement: if a pod typically handles 10 sessions before retirement, the pool needs ~1/10th the pods to serve the same request volume. Measured via the `lenny_pod_session_reuse_count` histogram (p50). **Formula assumption:** the `mode_factor` estimate converges toward the configured `recycle.maxSessionsPerPod` for predictable workloads where pods are not retired early by `maxPodUptimeSeconds` or `maxScrubFailures`. For variable workloads where early retirement is common, use the observed `lenny_pod_session_reuse_count` p50 rather than `recycle.maxSessionsPerPod` as the estimate.
- **`service`**: `mode_factor = maxConcurrent` — each pod serves `maxConcurrent` simultaneous requests. A pod with `maxConcurrent: 8` provides 8x the effective capacity of a one-session-per-pod session-mode pod.

**Adjusted formula (non-experiment pools):**

```
target_minWarm = ceil(base_demand_p95 × safety_factor × (failover_seconds + pod_warmup_seconds) / mode_factor
                      + burst_p99_claims × pod_warmup_seconds / burst_mode_factor)
```

For A/B experiment variant pools, apply `variant_weight` as defined in the variant pool formula in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration), combined with the `mode_factor` and `burst_mode_factor` divisors from this formula.

The steady-state term (first) is divided by `mode_factor` because pod recycling and slot multiplexing both reduce the number of pods needed to sustain a given throughput over time. The burst term (second) uses a separate `burst_mode_factor` because burst absorption depends on how many simultaneous requests a single pod can handle at the instant of arrival rather than on lifetime reuse:

- **`session`**: `burst_mode_factor = maxConcurrentSessions` — a pod absorbs as many simultaneous burst arrivals as it has free slots. With `maxConcurrentSessions: 1` each pod absorbs exactly one burst arrival regardless of how many sessions it serves over its lifetime.
- **`service`**: `burst_mode_factor = maxConcurrent` — each pod has `maxConcurrent` slots that can accept simultaneous arrivals.

The `(failover_seconds + pod_warmup_seconds)` factor in the first term converts the claim rate (claims/second) to a pod count, consistent with the base formula in [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration). `pod_warmup_seconds` is the pod creation-to-ready time for the pool (pod-warm baseline ≈ 10s, SDK-warm 30–90s; see [Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration) terminology note).

**Caveats:**

- For recycling pools, `mode_factor` is derived from observed reuse metrics and converges toward `recycle.maxSessionsPerPod` over time. During cold start (no historical data), the controller falls back to `mode_factor = 1.0` (one-session-per-pod sizing) until sufficient samples are collected (default: 100 completed sessions). Once converged, `mode_factor` is bounded above by `recycle.maxSessionsPerPod` (pods cannot serve more sessions than the configured limit). **Integration level consideration:** Because each pool references exactly one runtime (and therefore one integration level), there is no level heterogeneity within a pool, and recycling requires no runtime cooperation ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed. For recycling pools with `preConnect: true`, the between-session SDK re-warm window (up to `sdkConnectTimeoutSeconds`, default 60s) adds to the per-session cycle time (whole-pod scrub + SDK re-warm + potential demotion), reducing effective throughput per pod and lowering the observed `mode_factor` below the configured `recycle.maxSessionsPerPod`. The PoolScalingController should use the observed `lenny_pod_session_reuse_count` p50 (which naturally reflects this overhead) rather than `recycle.maxSessionsPerPod` for such pools.
- For session pools with `maxConcurrentSessions > 1`, the effective `mode_factor` may be lower than the slot-derived expectation if workspace materialization per slot is a bottleneck. The PoolScalingController uses `active_slots / (pod_count × maxConcurrentSessions)` saturation to detect this and adjusts `mode_factor` downward when slot saturation consistently exceeds 0.85.
- For service mode, routing goes through a Kubernetes Service and pod readiness reflects slot availability, so the scaling controller monitors slot saturation directly rather than using the warm pool claim model. **Demand signal source:** Because service mode bypasses the gateway claim model, the PoolScalingController derives `base_demand_p95` from the Prometheus metric `rate(lenny_service_requests_total[5m])` (requests per second arriving at the pool's Service) and `burst_p99_claims` from `max_over_time(lenny_service_concurrent_active[5m])` (peak concurrent active slots). These metrics are emitted by the gateway's tenant-affinity routing layer (see the `service` definition above). The scaling formula uses `mode_factor = maxConcurrent` and `burst_mode_factor = maxConcurrent`, and the saturation signal is `active_slots / (pod_count × maxConcurrent)`.
````
Promoted edit blocks for the Section 6 row `spec/06_warm-pod-model.md §6.1, §6.2, §6.4`. Line numbers are hints against the current file; anchors are the quoted text.

### 6.34 `spec/06_warm-pod-model.md` §6.1 (one-session default and credential lease lifecycle)

The one-session-only invariant becomes the default `sessionPolicy` configuration (`recycle.enabled: false`, Sections 2 and 3.1), and the two titled credential-lease paragraphs are re-keyed off the removed `task` and `concurrent` modes onto the session/recycle and `maxConcurrentSessions > 1` vocabulary, preserving the per-execution lease-and-revoke granularity.

Replace the paragraph beginning "**Session mode security invariant: pods are one-session-only.**" (near line 24) with:

```markdown
**One-session-per-pod default and the recycle opt-in.** With the default `sessionPolicy` (`maxConcurrentSessions: 1`, `recycle.enabled: false`), a pod serves exactly one session: after the session completes or fails, the pod is terminated and replaced. This prevents cross-session data leakage through residual workspace files, session transcripts, cached DNS, or runtime memory. Setting `recycle.enabled: true` relaxes this default with explicit deployer acknowledgment (`acknowledgeBestEffortScrub`): the pod is recycled across whole sessions of the same tenant, with a per-slot cleanup at every session release and a whole-pod scrub at the occupancy-zero recycle boundary (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). Setting `maxConcurrentSessions > 1` allows multiple simultaneous sessions on a single pod and requires `acknowledgeProcessLevelIsolation` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A session that ends in failure or a crash always retires its pod regardless of recycle settings.
```

Replace the paragraph beginning "**Task-mode credential lease lifecycle.**" (near line 26) with:

```markdown
**Per-session credential lease lifecycle on recycling pods.** Credentials are leased per session rather than per pod. A fresh credential assignment (`AssignCredentials` RPC) is performed at each session dispatch; a recycling pod does not retain credentials between sessions. The adapter manifest is regenerated per session, and `/run/lenny/credentials.json` is rewritten with the new lease before the runtime binary is spawned for each session. The previous lease is revoked when the session completes or the runtime exits. Per-session leasing aligns with the single-use-execution model: each session dispatch is semantically a fresh credential assignment regardless of pod reuse. Pool capacity implications: because each session requires a credential assignment, the `maxConcurrentSessions` limit on credential pool entries ([Section 4.9](04_system-components.md#49-credential-leasing-service)) is evaluated per session rather than held for the pod's lifetime. A recycling pod that completes a session releases its credential lease before the next session begins.
```

Replace the paragraph beginning "**Concurrent-mode credential lease lifecycle.**" (near line 28) with:

```markdown
**Per-slot credential lease lifecycle (`maxConcurrentSessions > 1`).** When `sessionPolicy.maxConcurrentSessions > 1`, credentials are leased per slot rather than per pod. Each active slot holds an independent credential lease obtained via a separate `AssignCredentials` RPC at slot assignment time. This keeps the credential pool's concurrency accounting aligned with slot-level concurrency and prevents a single credential rotation from disrupting all concurrent slots simultaneously. The adapter writes per-slot credential files at `/run/lenny/slots/{slotId}/credentials.json` (mode `0440`, tmpfs-backed, with the same adapter-owner and `lenny-cred-readers` group-ownership scheme as the single-slot file; see [§4.7](04_system-components.md#47-runtime-adapter) item 4) rather than a single global `/run/lenny/credentials.json`. Credential rotation follows the standard rotation protocol ([Section 4.9](04_system-components.md#49-credential-leasing-service) Fallback Flow) independently per slot: the in-flight gate and `credentials_rotated` acknowledgment apply to the individual slot being rotated rather than to the pod as a whole. Other slots' LLM requests are unaffected by a rotation on a sibling slot. When a slot completes or fails, its credential lease is revoked independently. Pool capacity implications: a pod with `maxConcurrentSessions: 8` and all slots active holds up to 8 simultaneous credential leases, each counting against the `maxConcurrentSessions` limit on credential pool entries ([Section 4.9](04_system-components.md#49-credential-leasing-service)).
```

### 6.35 `spec/06_warm-pod-model.md` §6.1 (`sdk_connecting` watchdog)

The watchdog gains `reserved` as a non-failure terminus and a per-edge clock anchor, so the recycle re-warm edge measures only the SDK re-warm rather than the prior occupancy episode or the whole-pod scrub (Sections 3.2, 3.3, and 3.4); the shipped watchdog clock measures from pod start, which would retire every recycling preConnect pod the instant it projected `sdk_connecting`.

Replace the paragraph beginning "**`sdk_connecting` watchdog.**" (near line 69) with:

```markdown
**`sdk_connecting` watchdog.** A pod that hangs in `sdk_connecting` state (SDK process alive but not completing its connection establishment) holds a warm pool slot indefinitely while appearing available. The WarmPoolController applies a per-pod timeout: `sdkConnectTimeoutSeconds` (default: 60s, configurable per pool in the `scalingPolicy` block). If the SDK does not complete its connection and transition out of `sdk_connecting` to `idle` or `reserved` within this timeout, the WarmPoolController transitions the pod to `failed` and increments `lenny_warmpool_sdk_connect_timeout_total` (counter, labeled by `pool`). The clock is anchored at the entry into the SDK-connect work on the edge being measured. On the warm-fill edge (`warming → sdk_connecting`) the clock measures from pod start. On the recycle re-warm edge (`claimed → sdk_connecting`, [Section 6.2](#62-pod-state-machine)) the clock measures from the re-warm start: the gateway records a re-warm-start stamp on the `SandboxClaim` status when the whole-pod scrub report arrives on a preConnect pool and the recycle disposition begins the SDK re-warm, the projection enters `sdk_connecting` at that stamp, and the WarmPoolController measures `sdkConnectTimeoutSeconds` from it. Neither the pod's prior occupancy episode nor the whole-pod scrub therefore counts against the re-warm budget, and a re-warm that completes within the budget reaches `reserved`. The whole-pod scrub itself runs while the pod projects `claimed` and is bounded by the gateway-side scrub-report timeout (`cleanupTimeoutSeconds` plus a grace period, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) rather than by `sdkConnectTimeoutSeconds`. Alert `SDKConnectTimeout` (Warning) fires when this counter rate exceeds 0.1/min for > 5 min on the same pool, indicating systematic SDK warm startup issues.
```

### 6.36 `spec/06_warm-pod-model.md` §6.1 (preConnect compatibility table)

The compatibility table is re-keyed from the removed mode names to `maxConcurrentSessions == 1` plus the service-mode rejection (Section 3.1), and the `task` row's between-task re-warm description becomes the recycle re-warm under a `recycling` claim. The validation error strings are restated because the current ones name removed modes.

Replace the paragraph beginning "**`preConnect` compatibility with execution modes.**" and the table that follows it (near lines 71-78) with:

```markdown
**`preConnect` compatibility with the execution modes.** SDK-warm mode (`preConnect: true`) is admitted only in session mode with `maxConcurrentSessions: 1`, because the SDK-warm model assumes a single agent process awaiting a single first prompt. The following table defines the compatibility and behavioral semantics:

| Configuration | `preConnect` | Behavior |
|---|---|---|
| `session`, `maxConcurrentSessions: 1`, `recycle.enabled: false` | Supported (primary target) | SDK process pre-connected once during the warm phase. Demotion via `sdkWarmBlockingPaths` is evaluated at claim time. The pod is exclusive to its session and is retired when the session ends, so no re-warm occurs. |
| `session`, `maxConcurrentSessions: 1`, `recycle.enabled: true` | Supported | SDK process pre-connected once during the warm phase. At the occupancy-zero recycle boundary the whole-pod scrub terminates the SDK process along with all other session processes; after the scrub report arrives and the disposition is recycle, the adapter re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge before the claim enters `reserved` ([Section 6.2](#62-pod-state-machine)). The `sdkWarmBlockingPaths` check is evaluated per session at dispatch time (each session may upload different files); if a file in the new session's workspace plan matches a blocking pattern, the gateway sets `requiresDemotion: true` on the session's `ClaimOpts` and the adapter calls `DemoteSDK` before materializing that session's workspace, then proceeds via the pod-warm path for that session only. The SDK is re-warmed at the next recycle boundary. The per-session demotion rate contributes to the same circuit-breaker threshold ([Section 6.1](#61-what-a-pre-warmed-pod-looks-like)). |
| `session`, `maxConcurrentSessions > 1` | Not supported | Concurrent sessions multiplex onto a single pod via `slotId`. The SDK-warm model assumes a single agent process waiting for a single first prompt, which is incompatible with slot-level multiplexing where each slot requires independent workspace materialization and independent demotion decisions. The pool controller rejects pool definitions that combine `sessionPolicy.maxConcurrentSessions > 1` with `capabilities.preConnect: true` at validation time with error: `"preConnect: true requires sessionPolicy.maxConcurrentSessions: 1; concurrent sessions require independent per-slot agent initialization"`. |
| `service` | Not supported | Service mode routes through a Kubernetes Service with no Lenny-managed workspace or session lifecycle. The warm pool controller does not manage SDK-warm state for service-mode pods. The pool controller rejects pool definitions that combine `executionMode: service` with `capabilities.preConnect: true` at validation time with error: `"preConnect: true is not supported with executionMode: service; service mode has no Lenny-managed agent lifecycle"`. |
```

### 6.37 `spec/06_warm-pod-model.md` §6.2 (coarse pod state machine and recycle edges)

The fenced state machine becomes the coarse occupancy machine the WarmPoolController writes, carrying the prior 0002's enum coarsening with `slot_active` collapsed into `claimed` and `reserved` added: the `task_cleanup` states, the between-task transitions, the fine setup chain, and the session-state sub-blocks are removed (the session states live in the Postgres session model, §7.2 and §8.8), the recycle edges and the concurrent-occupancy sub-block are added, and the per-slot sub-states block is retained for `maxConcurrentSessions > 1`. A new titled paragraph after the fence carries the reserved-hold semantics from Sections 3.2 and 3.3.

Replace the entire fenced block that opens with "Pod-warm path:" and closes with "    slot_cleanup ──→ leaked               (cleanup timeout exceeded — slot not reclaimed until pod termination)" (lines 82-177) with the following paragraph and fenced block:

````markdown
`Sandbox.status.phase` carries the coarse pod-occupancy enum: `warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, and `terminated`. The WarmPoolController is the sole writer of the phase and computes it as a level-triggered projection of per-pod `SandboxClaim` existence, the claim's binding state and disposition, and `sessionPolicy` ([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)): a pod with no claim projects `idle`; a claim with binding state `bound` projects `claimed`; a `recycling` claim projects `claimed` until its whole-pod scrub reports successful, and `sdk_connecting` during the preConnect SDK re-warm leg that follows; a `reserved` claim projects `reserved`; a claim deleted on a recycling pod under its limits projects `idle`; and a terminal claim disposition (`released` or `failed`), or a claim deleted on a pod with `recycle.enabled: false`, projects `draining` and then `terminated`. The fine session-lifecycle states live in the Postgres session model ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)) and are not projected onto the CRD.

```
Warm fill (pod-warm path):
  warming ──→ idle                  (pod scheduled, adapter healthy, readiness gate set)
  warming ──→ failed                (warm-fill failure)

Warm fill (SDK-warm path, preConnect: true):
  warming ──→ sdk_connecting        (adapter starts the SDK process before first claim)
  sdk_connecting ──→ idle           (SDK pre-connect completes — watchdog clock anchored at pod start)
  sdk_connecting ──→ failed         (sdkConnectTimeoutSeconds watchdog fires — see §6.1)
  sdk_connecting ──→ terminated     (SIGTERM received: DemoteSDK called with timeout, then process exits)

Occupancy projection (WarmPoolController-computed from the per-pod SandboxClaim):
  idle ──→ claimed                  (gateway wins the per-pod claim CREATE; binding state patched to bound)
  claimed ──→ draining              (terminal claim disposition released or failed; claim deleted on a pod
                                     with recycle.enabled: false; or gateway-stamped
                                     lenny.dev/drain-request annotation — unhealthy threshold)
  idle ──→ draining                 (recycle.maxPodUptimeSeconds exceeded while idle, checked before
                                     next assignment)
  failed ──→ draining               (failed pod reclaimed and replaced)
  draining ──→ terminated           (pod replacement provisioned from warm pool)

Recycle edges (recycle.enabled: true; occupancy reaches zero after clean session termination; the claim
is patched bound → recycling and the whole-pod scrub runs while the pod projects claimed, bounded by
the gateway-side scrub-report timeout):
  claimed ──→ sdk_connecting        (preConnect: true, scrub reported and disposition is recycle, host
                                     node schedulable — SDK re-warm begins; the projection enters
                                     sdk_connecting at the re-warm-start stamp on the claim status)
  claimed ──→ reserved              (preConnect: false, scrub reported and disposition is recycle —
                                     claim patched to reserved, no re-warm leg)
  claimed ──→ draining              (recycle disposition retires the pod: recycle.maxSessionsPerPod,
                                     maxScrubFailures, or maxPodUptimeSeconds reached; onScrubFailure: fail;
                                     a failed session; a missing scrub report at the gateway-side timeout;
                                     or an unschedulable host node)
  sdk_connecting ──→ reserved       (SDK re-warm completes within sdkConnectTimeoutSeconds measured from
                                     the re-warm-start stamp)
  sdk_connecting ──→ failed         (re-warm watchdog fires)
  reserved ──→ claimed              (same-tenant session rebinds within the hold TTL — reserved → bound
                                     claim patch, no acquisition round trip)
  reserved ──→ idle                 (hold TTL expires — precondition-guarded claim DELETE; the pod is
                                     already scrubbed and SDK-warm, no second re-warm)

Concurrent occupancy (sessionPolicy.maxConcurrentSessions > 1; the pod-level phase is the coarse
claimed whenever the Redis-counter occupancy is nonzero):
  idle ──→ claimed                  (first session assigned — per-pod claim CREATE wins and the atomic
                                     Redis counter increment succeeds, slotId allocated)
  claimed ──→ claimed               (additional session assigned — Redis-counter occupancy below
                                     maxConcurrentSessions)
  claimed ──→ claimed               (a session completes or fails — per-slot cleanup runs, Redis-counter
                                     occupancy decremented, occupancy still nonzero)
  claimed ──→ draining              (ceil(maxConcurrentSessions/2) slots fail or leak within 5-min
                                     window — pod marked unhealthy)
  claimed ──→ draining              (recycle.maxPodUptimeSeconds exceeded — no new sessions accepted,
                                     existing sessions drain)
  (occupancy reaches zero: the recycle edges above apply when recycle.enabled: true;
   otherwise the claim is deleted and the pod drains)

  Per-slot sub-states (tracked per slotId, not as pod-level phase):
    slot_assigned ──→ receiving_uploads   (workspace materialization begins for this slot)
    receiving_uploads ──→ running         (workspace ready, session dispatched to runtime with slotId)
    running ──→ slot_cleanup              (session completes or fails)
    running ──→ failed                    (non-retryable error: OOM, workspace validation, policy rejection)
    slot_cleanup ──→ released             (slot workspace removed, processes killed, slotId released)
    slot_cleanup ──→ leaked               (cleanup timeout exceeded — slot not reclaimed until pod termination)
```

**`reserved` hold semantics.** A recycled pod's claim is held rather than deleted. When occupancy reaches zero on a recycling pod, the gateway patches the claim's binding state to `recycling`, coordinates the whole-pod scrub (and, on preConnect pools, the SDK re-warm that follows it), and then patches the binding state to `reserved`, stamping `holdExpiresAt` (the reservation time plus the deployment-level hold TTL, `gateway.claimHoldTTLSeconds`, default 10 seconds) on the claim status. A pod that reaches `reserved` is always scrubbed and, on a preConnect pool, SDK-warm. During the hold the pod is held for its pinned tenant and is excluded from idle inventory: the candidate scan skips it and the per-pod claim CREATE guard blocks acquisition by another replica. A new session of the same tenant arriving within the hold window is dispatched onto the pod with a `reserved → bound` claim patch and no acquisition round trip; any gateway replica may rebind. If the hold TTL expires first, the holder deletes the claim with Kubernetes preconditions (the claim UID and the resourceVersion observed at the `reserved` patch), so a concurrent rebind from any replica wins the race, and the pod returns to `idle` with no second re-warm. The PoolScalingController counts `reserved` pods as occupied for inventory purposes ([Section 4.6.2](04_system-components.md#462-poolscalingcontroller-pool-configuration)).
````

### 6.38 `spec/06_warm-pod-model.md` §6.2 (`leaked` slot semantics)

The paragraph is re-keyed from the dropped `Sandbox.status.activeSlots` to the Redis-counter occupancy and from `maxConcurrent` to `maxConcurrentSessions`.

Replace the paragraph beginning "**`leaked` slot semantics.**" (near line 179) with:

```markdown
**`leaked` slot semantics.** A slot in the `leaked` state remains counted in the pod's Redis slot-counter occupancy (preventing the gateway from over-assigning new slots that would conflict with the leaked slot's unreleased resources). A leaked slot counts toward the `ceil(maxConcurrentSessions/2)` unhealthy threshold defined in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) (the "**fail or leak**" whole-pod replacement trigger): leaked slots are combined with `failed` slots in the rolling 5-minute count, and if `failed_slots + leaked_slots >= ceil(maxConcurrentSessions/2)` within that window, the pod immediately transitions to `draining`. If `leaked_slots` alone reaches `ceil(maxConcurrentSessions/2)` (independent of any `failed` slots), the pod also transitions to `draining` for the same reason. The adapter exposes a `leaked_slots` count in the pod's health metadata (`/healthz` response and `lenny_adapter_leaked_slots` gauge, labeled by `pod_id` and `pool`) for observability.
```

### 6.39 `spec/06_warm-pod-model.md` §6.2 (host-node schedulability and scrub-warning re-warm)

Both titled paragraphs are re-keyed from the `task_cleanup → sdk_connecting` transitions to the `claimed → sdk_connecting` recycle re-warm edge, the `maxTasksPerPod` trigger becomes `recycle.maxSessionsPerPod`, the cumulative metric is renamed per Section 4, and the schedulability gate is restated at the recycle disposition (the Section 3.4 retire trigger), keeping the gateway as the label reader with no new RBAC.

Replace the paragraph beginning "**Host-node schedulability precondition.**" (near line 181) with:

```markdown
**Host-node schedulability precondition.** The "host node is schedulable" precondition on the `claimed → sdk_connecting` recycle re-warm edge (with or without a `scrub_warning` annotation) is defined as the pod's host node (`spec.nodeName`) having `.spec.unschedulable == false` in the Kubernetes Node object (equivalently: no `node.kubernetes.io/unschedulable` taint applied when `kubectl cordon` or an autoscaler drain marks the node). The evaluation is performed by the WarmPoolController rather than the gateway, and its result is surfaced to the gateway as the pod label `lenny.dev/host-schedulable` (values: `"true"` when the host node is schedulable, `"false"` otherwise). The WarmPoolController maintains this label on every reconcile from its Node informer cache and re-labels all pods on an affected Node within a single reconcile cycle when the Node's cordon or uncordon state changes; see [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) "Host-node schedulability labeling (`lenny.dev/host-schedulable`)" for the controller-side contract (including the per-reconcile label maintenance and the cordon/uncordon re-label latency) and the rationale for keeping cluster-scoped Node reads out of the gateway's RBAC surface. The gateway reads this label via its existing `get` access on the `Pods` resource in agent namespaces when it computes the recycle disposition at the occupancy-zero boundary; it does not re-check during the SDK re-warm, holds no Node verbs on its ServiceAccount, and needs no new RBAC grants ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries) "Gateway ServiceAccount RBAC grants"). If the label reads `"false"` (or is absent, which is treated as unschedulable for fail-safe behavior), the recycle disposition retires the pod: the pod drains instead of re-warming, even though `recycle.maxSessionsPerPod`, `maxPodUptimeSeconds`, and `maxScrubFailures` have not been reached, because an SDK re-warm on a cordoned node would produce a held or idle pod whose next eviction is imminent. The rule applies identically to the scrub-success and scrub-warning re-warm edges: a pod carrying a `scrub_warning` annotation on a cordoned node must not re-enter the warm pool, because holding or idling it (even SDK-warm) would hand a soon-to-be-evicted pod to the next session. The unschedulable-host-node retire trigger applies to the recycle disposition on non-preConnect pools as well: a recycling pod on a cordoned node is retired rather than held in `reserved`, for the same imminent-eviction rationale.
```

Replace the paragraph beginning "**preConnect re-warm on scrub_warning.**" (near line 183) with:

```markdown
**preConnect re-warm on scrub_warning.** Section 6.1 establishes the invariant that all pods in a `preConnect: true` pool are SDK-warm when idle; on recycling pools the invariant extends to the hold window, so every pod that reaches `reserved` or `idle` is SDK-warm. To preserve this invariant for recycling pools that use `onScrubFailure: warn`, a pod whose whole-pod scrub fails (but has not exhausted `maxScrubFailures`, `recycle.maxSessionsPerPod`, or `maxPodUptimeSeconds`) routes through the `claimed → sdk_connecting` re-warm edge before the claim enters `reserved`, exactly as a successful-scrub pod does; the gateway records the re-warm-start stamp when the scrub report arrives and the disposition is recycle, whether the report is a success or a warn-policy failure. The `scrub_warning` annotation and the cumulative `lenny_pod_scrub_failure_count` metric ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) persist through the re-warm: the SDK re-initialization does not clear the warning, since the residual-state risk is orthogonal to SDK readiness. If the SDK re-warm itself fails during `sdk_connecting`, the pod transitions to `failed` via the standard `sdk_connecting → failed` path (the `scrub_warning` annotation is retained in the pod's audit trail). On non-preConnect pools the claim is patched directly to `reserved` after the scrub report, carrying the `scrub_warning` annotation. This ensures no mixed-state inventory: every reserved or idle pod in a preConnect pool is SDK-warm regardless of whether the prior session's scrub succeeded cleanly or produced a warning.
```

### 6.40 `spec/06_warm-pod-model.md` §6.2 (session crash recovery)

The titled task-mode crash paragraph is restated as the session crash-recovery path: `maxTaskRetries` becomes `maxSessionRetries` on `sessionPolicy` (default 1, two total attempts, 0 disables), the false no-checkpoint claim is corrected (a session uses the checkpoint mechanism), and the closing sentence becomes the session crash re-dispatch on a fresh pod.

Replace the block from the paragraph beginning "**Pod crash during active task-mode task.**" through the paragraph ending "not \"restoring session workspace from checkpoint\"." (near lines 185-192) with:

```markdown
**Pod crash during an active session.** A pod crash, node failure, or unrecoverable gRPC error can occur while a session is bound to a pod. The crashed pod is always retired through the drain path regardless of `recycle` settings: a pod that fails mid-session never re-enters the warm pool and never reaches `reserved`. The session itself follows the standard recovery path:

- **Retry policy:** If `retryCount < maxSessionRetries` (default: `1`, giving 2 total attempts), the gateway transitions the session to `resume_pending`, claims a fresh pod from the warm pool, and re-dispatches the session on the new pod. Where a session checkpoint exists, the replacement pod's workspace is restored from the latest checkpoint per the retry-and-resume path ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)); nothing is carried over directly from the crashed pod.
- **Retry exhaustion:** If retries are exhausted or the failure is non-retryable (for example a workspace validation error or a policy rejection), the session transitions to `failed`. The failed pod is released from the pool and terminated. The gateway returns a structured error to the client.
- **Non-retryable failures:** OOM kills, workspace validation errors, and policy rejections are not retried; the same input is likely to fail again on an identically provisioned pod.
- **maxSessionRetries** is a `sessionPolicy` field (default: `1`). Setting it to `0` disables retries, so crashes always fail the session outright.

The `resume_pending` transition here is the session crash re-dispatch onto a fresh pod: the crashed pod is drained, a replacement is claimed, and the session resumes from its latest checkpoint where one exists ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)).
```

### 6.41 `spec/06_warm-pod-model.md` §6.2 (concurrent pod lifecycle and partial occupancy)

The lifecycle paragraph drops the task-mode parenthetical and re-keys `slot_active` to the coarse `claimed` under `maxConcurrentSessions > 1`, and the partial-occupancy bullet derives occupancy from the Redis slot counter and the label from the projected coarse phase; the reserved hold window provides the label-stabilization behavior, because the label is derived solely from the projected phase with no gateway-side label writes (Section 3.3).

Replace the paragraph beginning "**Concurrent-workspace pod lifecycle.**" (near line 194) with:

```markdown
**Concurrent pod lifecycle (`maxConcurrentSessions > 1`).** A pod serving `sessionPolicy.maxConcurrentSessions > 1` has a two-level state model: the **pod-level** coarse phase tracks the pod's overall occupancy (above), while **per-slot sub-states** track each individual slot's progress through workspace materialization, execution, and cleanup. The pod-level phase is the coarse `claimed` whenever the Redis-counter occupancy is nonzero. When occupancy reaches zero and all slot cleanup has finished, the pod leaves `claimed` through the recycle edges when `recycle.enabled` is true, or through `draining` otherwise.
```

Replace the bullet beginning "- **Partial occupancy.**" (near line 197) with:

```markdown
- **Partial occupancy.** A pod whose Redis-counter occupancy is below `maxConcurrentSessions` accepts new session assignments concurrently with running sessions. There is no queuing at the pod level: the atomic Redis counter ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) gates assignment and is the occupancy authority. The pod label `lenny.dev/state` follows the projected coarse phase: `claimed` and `reserved` both map to `active`, so the label remains `active` through the occupancy-zero recycle boundary and the reserved hold, and transitions to `idle` only when the hold expires and the claim is deleted. The reserved hold window (see "`reserved` hold semantics" above) therefore provides label stabilization: a new session assigned within the hold window rebinds the claim and the label never leaves `active`, so rapid session turnover (for example `maxConcurrentSessions: 8` with short-lived sessions) generates no label oscillation and no unnecessary API server write churn at scale. Pods with `maxConcurrentSessions: 1` follow the same projection; their label transitions track the phase directly because session lifecycles on them do not overlap.
```

### 6.42 `spec/06_warm-pod-model.md` §6.2 (session-model scoping and the `resuming` enumeration)

Carried from the prior 0002: the prose subsections that enumerate `input_required`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`, and the session terminals are reclassified as session-model states, and the `resuming` authoritative-enumeration sentence scopes its authority to the session model.

Insert before the paragraph beginning "**`input_required` sub-state:**" (near line 200):

```markdown
The transitions enumerated in the remaining subsections of this section (`running`, `input_required`, `suspended`, `resume_pending`, `resuming`, `awaiting_client_action`, and the session terminals `completed`, `failed`, `cancelled`, and `expired`) are **session-model states** in the Postgres session model ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)) rather than coarse `Sandbox.status.phase` occupancy values. The `Sandbox.status.phase` enum carries only the coarse occupancy set above (`warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, `terminated`); a pod whose bound session is in any of these session states projects `claimed`.
```

In the paragraph beginning "**`resuming` failure transitions:**" (near line 246), replace the sentence "This subsection is the **authoritative enumeration** of every edge out of `resuming` (per iter3 SES-013); the §7.2 session-state listing cross-references back here." with:

```markdown
This subsection is the **authoritative enumeration** of every edge out of the session-model `resuming` state (per iter3 SES-013); the [§7.2](07_session-lifecycle.md#72-interactive-session-model) session-state listing cross-references back here. `resuming` is a session-model state recorded on the Postgres session row rather than a coarse `Sandbox.status.phase` occupancy value.
```

### 6.43 `spec/06_warm-pod-model.md` §6.2 (`maxClientIdleSeconds` clock)

The `maxIdleTimeSeconds` timer block and its per-state pause table are replaced by the `sessionPolicy.maxClientIdleSeconds` clock per Section 3.1: one clock with one pause table, client-originated interactions added to the qualifying events, the clock running during `input_required`, `awaiting_client_action`, and elicitation waits, paused during `suspended`, `resume_pending`, and `resuming`, and the default raised to the pool's effective `maxSessionAgeSeconds`.

Replace the block from the paragraph beginning "**`maxIdleTimeSeconds` timer behavior across states.**" through the paragraph beginning "The `maxIdleTimeSeconds` timer fires the `expired` transition independently of `maxSessionAge`." (near lines 273-290) with:

```markdown
**`maxClientIdleSeconds` clock behavior across states.** `sessionPolicy.maxClientIdleSeconds` is the platform's single idle bound: it terminates a session after continuous client inactivity and replaces the former pool-level `runtime.limits.maxIdleTimeSeconds` knob. "Idle" is defined as no qualifying activity since `last_agent_activity_at`. The `last_agent_activity_at` timestamp is updated in Postgres on each qualifying event. Qualifying events:

- Any client-originated interaction: a message, a tool approval or denial, an elicitation response, or an attach.
- `agent_output` or `tool_use` events from the adapter (all delivery modes). Agent work counts as activity, so an autonomously working session is never idle-terminated.
- `lenny/await_children` invocation and each partial result received from the `await_children` stream (including `input_required` events and terminal child results). This ensures that a parent session actively blocked in `await_children` is not falsely expired as idle.
- **Proxy mode (`deliveryMode: proxy`):** Each upstream SSE chunk (or equivalent partial response frame) received from the LLM provider and proxied through the LLM Proxy to the pod. This ensures that a long-running LLM call that streams tokens over an extended period is not mistaken for idle; each proxied chunk is direct evidence of active work. The gateway updates `last_agent_activity_at` in-memory on each chunk and flushes to Postgres at most once per second (coalescing rapid chunk arrivals) to avoid write amplification. The LLM Proxy already processes each chunk for token counting ([§4.9](04_system-components.md#49-credential-leasing-service)); the idle-clock reset piggybacks on this existing per-chunk code path.
- **Direct mode (`deliveryMode: direct`):** Each `ReportUsage` gRPC call received from the adapter. In direct mode the gateway has no per-chunk LLM visibility, so `ReportUsage` is the best available signal. These calls are periodic (interval determined by the adapter's reporting configuration), so there is a gap between the last LLM activity and the next `ReportUsage`; the idle bound should be at least 2× the `ReportUsage` interval. This is a weaker signal than proxy-mode per-chunk resets, but it narrows the false-idle window from `maxClientIdleSeconds` to the `ReportUsage` interval.

The clock has its own pause table and does not share the `maxSessionAge` pause table:

| State                    | Clock behavior                                                                                                                                                       |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `running`                | **Active.** Resets on every qualifying event (see list above). Fires `expired` when elapsed time since `last_agent_activity_at` exceeds the effective `maxClientIdleSeconds`. |
| `input_required`         | **Running.** The agent is blocked in `lenny/request_input` awaiting a client response, and waiting on an absent client is the condition this bound exists to reclaim. A client response resets the clock. Elicitation waits behave the same way ([Section 9.2](09_mcp-integration.md#92-elicitation-chain)). |
| `awaiting_client_action` | **Running.** The session is blocked pending a client decision. `maxSessionAge` is paused in this state, so this clock is the bound that reclaims an abandoned session here. |
| `suspended`              | **Paused.** The agent is deliberately halted; a suspended session may persist indefinitely by design (see "`suspended → expired` trigger mechanism" below).            |
| `resume_pending`         | **Paused.** The session cannot make progress and is not awaiting a present client. The `maxResumeWindowSeconds` wall-clock timer applies (see above).                 |
| `resuming`               | **Paused.** The session is being restored onto a new pod and cannot make progress.                                                                                    |
| `finalizing`             | **Not running.** The dedicated `maxFinalizingTimeoutSeconds` watchdog bounds this state (see the `maxSessionAge` table above).                                         |
| Terminal states          | **Stopped.** The clock is no longer evaluated.                                                                                                                        |

The clock fires the `expired` transition independently of `maxSessionAge`; the first timer to fire wins, and expiry carries the existing `max_idle_time` reason on the `dev.lenny.session_expired` event ([Section 14](14_workspace-plan-schema.md)). The default value is the pool's effective `maxSessionAgeSeconds`, so the idle bound binds before the age cap only when a deployer lowers `maxClientIdleSeconds` or when the session accrues idle time in a state where `maxSessionAge` is paused but this clock runs (`awaiting_client_action`); during `input_required` both clocks run and stay aligned. The effective per-session value is resolved through the existing most-restrictive timeout resolution. **Origin-scoped override:** sessions minted with the `origin: "playground"` JWT claim are subject to a tighter hard cap: the gateway enforces `min(maxClientIdleSeconds, playground.maxIdleTimeSeconds)` (default 300s) for these sessions to bound reclamation time after a best-effort browser-close cancel. The claim is stamped by the `/playground/*` ingress path under every `playground.authMode` value (`oidc`, `apiKey`, `dev`); see [§27.3](27_web-playground.md#273-authentication) for the per-mode mint points, so the override binds uniformly. See [§27.6](27_web-playground.md#276-session-lifecycle-and-cleanup).
```

In the paragraph beginning "**Interaction with other timers during podless suspension:**" (near line 242), replace the sentence "`maxIdleTimeSeconds` remains paused." with:

```markdown
`maxClientIdleSeconds` remains paused.
```

In the paragraph beginning "**`resume_pending` wall-clock cap:**" (near line 292), replace "Although `maxSessionAge` and `maxIdleTimeSeconds` are both paused during `resume_pending` (the session cannot make progress)" with:

```markdown
Although `maxSessionAge` and `maxClientIdleSeconds` are both paused during `resume_pending` (the session cannot make progress and is not awaiting a present client; the recovery states `resume_pending` and `resuming` are both in the idle clock's paused set)
```

In the paragraph beginning "**`suspended → expired` trigger mechanism:**" (near line 294), replace "Both `maxSessionAge` and `maxIdleTimeSeconds` are paused during `suspended`" with:

```markdown
Both `maxSessionAge` and `maxClientIdleSeconds` are paused during `suspended`
```

### 6.44 `spec/06_warm-pod-model.md` §6.2 (state storage and the `lenny.dev/state` label table)

Carried from the prior 0002 with `reserved` added and `slot_active` collapsed: the authoritative session state machine moves to the Postgres session model, the CRD carries only the coarse occupancy phase, and the label table gains the reserved- and recycling-pod label statements from Section 3.3.

Replace the sentence "The authoritative state machine lives in the `Sandbox` CRD `.status.phase` and `.status.conditions` fields, backed by Postgres via the controller." in the paragraph beginning "**State storage:**" (near line 305) with:

```markdown
The authoritative session state machine lives in the Postgres session model ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)). The `Sandbox` CRD `.status.phase` carries only the coarse pod-occupancy phase the WarmPoolController writes (`warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, `terminated`); the fine session-lifecycle states are not projected onto the CRD.
```

Append after the label table (whose `lenny.dev/state` row reads "`idle`, `active`, `draining`", near line 309) the following new paragraph:

```markdown
The `lenny.dev/state` label is derived solely from the projected coarse phase: `idle` maps to `idle`, `claimed` and `reserved` map to `active`, and `draining` maps to `draining`; the claim binding state is not a label input. A `reserved` pod, a `recycling` pod on a non-preConnect pool, and a `recycling` pod on a preConnect pool during its whole-pod scrub therefore carry `active` (the first projects the `reserved` phase and the others project `claimed`). A `recycling` pod on a preConnect pool projects `sdk_connecting` during its SDK re-warm leg and carries no `lenny.dev/state` label in that window, exactly as a warm-phase `sdk_connecting` pod carries none; that re-warm window is bounded by the `sdkConnectTimeoutSeconds` watchdog and is not unclaimed warm inventory.
```

Replace the sentence "Detailed state transitions (e.g., `receiving_uploads` → `finalizing_workspace`) are tracked in the CRD status subresource only." (near line 313) with:

```markdown
The coarse pod-occupancy transitions (for example `idle` → `claimed`) are tracked in the CRD status subresource; the fine session transitions live in the Postgres session model ([§7.2](07_session-lifecycle.md#72-interactive-session-model), [§8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)) and are not projected onto the CRD.
```

### 6.45 `spec/06_warm-pod-model.md` §6.4 (per-slot layout re-keys)

The per-slot filesystem prose is re-keyed from `concurrent` mode with `concurrencyStyle: workspace` to `sessionPolicy.maxConcurrentSessions > 1`, in lockstep with the §4.4 and §14 per-slot re-keys, and the base-layout sentence drops the removed task mode.

Replace the sentences "**Concurrent-workspace per-slot layout.** In `concurrent` mode with `concurrencyStyle: workspace`, the single `/workspace/current` layout above does not apply." (near line 385) with:

```markdown
**Per-slot layout (`maxConcurrentSessions > 1`).** When `sessionPolicy.maxConcurrentSessions > 1`, the single `/workspace/current` layout above does not apply.
```

In the Runtime responsibility-split bullet (near line 404), replace the sentence "The runtime MUST NOT assume a global `/workspace/current` path in concurrent-workspace mode." with:

```markdown
The runtime MUST NOT assume a global `/workspace/current` path when `maxConcurrentSessions > 1`.
```

Replace the sentence "Session mode and task mode continue to use the base layout (`/workspace/current`)." (near line 407) with:

```markdown
The base layout (`/workspace/current`) applies when `maxConcurrentSessions` is 1.
```

In the paragraph beginning "**`/workspace/shared/` population and enforcement.**" (near line 409), replace the first sentence "The `/workspace/shared/` directory in concurrent-workspace mode is populated by the gateway during pod initialization (before any slot is assigned) from the Runtime's `sharedAssets` configuration — a list of artifact references or inline file specs." with:

```markdown
When `maxConcurrentSessions > 1`, the `/workspace/shared/` directory is populated by the gateway during pod initialization (before any slot is assigned) from the Runtime's `sharedAssets` configuration: a list of artifact references or inline file specs.
```
Promoted from the Section 6 directive rows for `spec/07_session-lifecycle.md` §7.1 and §7.2, `spec/08_recursive-delegation.md` §8.8, `spec/09_mcp-integration.md` §9.1 (the anchored text sits in §9.2), `spec/10_gateway-internals.md` §10.1, and `spec/11_policy-and-controls.md` §11.3. Subsections are grouped per file in file order.

### 6.46 `spec/07_session-lifecycle.md` §7.1 (sessionIsolationLevel table)

Rewrite the `sessionIsolationLevel` field table for the `session` and `service` modes: `podReuse` and `residualStateWarning` become `sessionPolicy` derivations with the service-mode `true` values, `scrubPolicy` keys to `recycle.scrubProfile` and keeps its presence rule (`podReuse: true`) and its `"none"` service-mode value with the current concurrent-stateless rationale carried over, and the table gains the new `conversationContinuity` field (Sections 3.1, 3.6, and 4). The three hand-written client SDK `IsolationLevel` types and the gateway OpenAPI document mirror this object and change in the same implementation step (Section 5).

In §7.1, replace the field table that begins with the header row `| Field | Type | Description |` and whose first data row reads `| `executionMode` | `string` | `session`, `task`, or `concurrent` — the execution mode of the assigned pool |`, ending with the `residualStateWarning` row (the row ending `Clients can use this to display warnings or enforce additional input sanitization. |`), with:

```
| Field | Type | Description |
| ----- | ---- | ----------- |
| `executionMode` | `string` | `session` or `service`: the execution mode of the assigned pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) |
| `isolationProfile` | `string` | `runc`, `gvisor`, or `microvm`: the container or VM isolation level |
| `podReuse` | `boolean` | `true` when the assigned pool reuses pods across executions: when `sessionPolicy.recycle.enabled` is `true`, when `sessionPolicy.maxConcurrentSessions > 1`, or when `executionMode` is `service`. `false` otherwise. |
| `scrubPolicy` | `string` | Present only when `podReuse: true`. **Sequential recycling** (`maxConcurrentSessions: 1` with `recycle.enabled: true`): `"best-effort"` for `scrubProfile: standard`; `"vm-restart"` for microvm pools with `scrubProfile: vm-restart`; `"best-effort-in-place"` for microvm pools with `scrubProfile: in-place`. **Concurrent slots** (`maxConcurrentSessions > 1`): `"best-effort-per-slot"`; the scrub operations (workspace removal, process-group kill, and scratch directory cleanup) are applied per slot at each session release, and a whole-pod scrub runs when occupancy reaches zero on a recycling pool (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) per-slot cleanup). **Service mode:** `"none"`; no workspace exists and no scrub is performed, and pod reuse is implicit via Kubernetes Service routing. For service mode, `"none"` indicates more than the absence of cleanup: the gateway does not track per-request state or lifecycle for this mode, and no per-request scrub, checkpoint, or slot-level lifecycle management is performed by Lenny (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) service-mode limitations). |
| `residualStateWarning` | `boolean` | `true` when `recycle.enabled` is `true`, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`; signals that the pod may carry residual state from prior sessions, sibling slots, or same-tenant concurrent requests. For recycling pools, residual-state vectors include the DNS resolver cache, TCP `TIME_WAIT` state, and page cache from prior sessions. For `maxConcurrentSessions > 1`, residual state includes the shared process namespace, `/tmp`, cgroup memory, and network stack across simultaneous slots, plus a per-slot workspace reset between sessions (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) deployer acknowledgments). For service mode, residual state is the broadest case: process space, network stack, `/tmp`, and page cache are shared across same-tenant concurrent requests with no per-request scrub, and there is no workspace to reset and no slot-level lifecycle clearing `/tmp` between requests (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) service-mode limitations and tenant pinning). Clients can use this flag to display warnings or to enforce additional input sanitization. |
| `conversationContinuity` | `string` | `"platform"` or `"none"`. `"platform"` for session mode: the platform binds the session to a pod and preserves conversation context across messages for the session's lifetime. `"none"` for service mode: the gateway routes each message to any ready replica, the platform maintains no conversation context between messages, and clients of `multi_turn` runtimes re-inject any needed context into each message's `input` (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) service-mode contract). |
```

### 6.47 `spec/07_session-lifecycle.md` §7.2 (session and task definition)

Rewrite the §7.2 opening paragraph as the single defining statement of the session/task 1:1 mapping: the session is the only client-facing unit of execution, each session has exactly one execution, and "Task" is the name external protocols and the delegation API give to that unit (Section 3.5).

Replace the paragraph that begins `Once a session is attached, the client interacts via a **Lenny session** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server).` and ends `which is defined independently of any external protocol.` with:

```
Once a session is attached, the client interacts via a **Lenny session** with bidirectional streaming over Streamable HTTP (SSE for server→client, POST for client→server). All content delivery uses the `MessageEnvelope` format (see [Section 15.4.1](15_external-api-surface.md#1541-adapterbinary-protocol)). The session is the only client-facing unit of execution, and each session has exactly one execution. "Task" is the name external protocols and the delegation API give to that unit: the active `ExternalProtocolAdapter` surfaces the session as a protocol-native task object (an MCP Task for MCP clients and an A2A Task for A2A clients), and `lenny/delegate_task` ([Section 8.2](08_recursive-delegation.md#82-delegation-mechanism)) creates a child session. Internally the gateway operates against the **Lenny canonical task state machine** ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)), which is the session state machine of this section under its external-protocol name and is defined independently of any external protocol.
```

### 6.48 `spec/07_session-lifecycle.md` §7.2 (state-machine summary note)

Re-key the note above the session state machine so it no longer names the removed task-mode cycling or the removed `concurrent-workspace` mode; the per-slot sub-states survive keyed to `sessionPolicy.maxConcurrentSessions > 1` (the spec/06 §6.2 per-slot block retained by the spec/06 directive row).

Replace the note that reads `> **Note:** This is a summary of session-level state transitions as visible to external clients. For the complete pod and session state machine — including pre-attached states, task-mode cycling, and concurrent-workspace slot multiplexing — see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).` with:

```
> **Note:** This is a summary of session-level state transitions as visible to external clients. For the complete pod and session state machine, including pre-attached states and the per-slot sub-states used when `sessionPolicy.maxConcurrentSessions > 1`, see [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).
```

### 6.49 `spec/07_session-lifecycle.md` §7.2 (session conditions on the Postgres session row)

Carry the prior 0002's conditions-to-Postgres move into §7.2: the session-end and interrupt-suspension facts the gateway records (today written as the `Terminated` and `Suspended` conditions on `Sandbox.status.conditions` from `pkg/gateway/podsession/binder.go`) live on the Postgres session row, leaving the WarmPoolController as the sole writer of `Sandbox.status` (Section 3.3).

Immediately after the paragraph `Terminal states: `completed`, `failed`, `cancelled`, `expired`. These match the canonical task states defined in [Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema).`, insert:

```
**Session lifecycle state lives on the Postgres session model.** The state machine above, including the terminal disposition and the suspend transition, is recorded on the Postgres session row and is read through the session API (`GET /v1/sessions/{id}` and `GET /v1/admin/sessions/{id}`). The session-end and interrupt-suspension facts are session-row fields rather than `Sandbox.status` conditions: the gateway writes no `Sandbox.status` field, and the WarmPoolController is the sole writer of `Sandbox.status` ([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)). The `Sandbox` CRD `.status.phase` carries only the coarse pod-occupancy phases the WarmPoolController projects ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)).
```

### 6.50 `spec/07_session-lifecycle.md` §7.2 (per-slot slotId routing)

Re-key the per-slot `slotId` message-routing paragraph from the removed `concurrent-workspace mode` to `sessionPolicy.maxConcurrentSessions > 1`, retaining the `SLOT_ID_REQUIRED` rejection, in lockstep with the §6.2, §10.1, and §15.4.1 `slotId` re-keys staged by the sibling directive rows.

Replace the paragraph that begins `**Concurrent-workspace mode (`slotId`) routing:** In concurrent-workspace mode, each active slot maintains its own independent inbox on the coordinating gateway replica.` and ends `See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) for full concurrent-workspace semantics.` with:

```
**Per-slot (`slotId`) routing when `maxConcurrentSessions > 1`:** On a pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1`, each active slot maintains its own independent inbox on the coordinating gateway replica. The `slotId` field in the `MessageEnvelope` determines which slot's inbox receives the message. Path evaluation (paths 1-7 above) is performed per slot: `ready_for_input`, `input_required`, and `await_children` are tracked for each slot rather than for the pod. A message with `slotId: "slot_01"` can be delivered (path 2) to slot 01 while slot 02 is in `input_required` (path 3). Messages without a `slotId` for such a pod are rejected with `SLOT_ID_REQUIRED`. The `delivery: "immediate"` interrupt (path 4) targets the specific slot's tool-call context rather than the whole pod. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) for the full per-slot semantics.
```

### 6.51 `spec/08_recursive-delegation.md` §8.8 (canonical task state machine)

State in §8.8 that the canonical task state machine is the session state machine and that task-tree nodes are sessions, per the Phase 1 session/task 1:1 collapse (Section 3.5); the lexical renames of `TaskRecord`, `TaskResult`, and `taskId` are deferred to Phase 2 and the schemas in this section are otherwise unchanged.

Replace the block that reads `**Lenny canonical task state machine:**` followed by the paragraph `Lenny defines its own task states independent of any external protocol. External protocol adapters map to/from these states at the boundary.` with:

```
**Lenny canonical task state machine:**

The canonical task state machine is the session state machine ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)) under the name external protocols give it. A session is the only unit of execution and each session has exactly one execution, so an MCP or A2A Task is the protocol surface of a session; the nodes of the task tree ([Section 8.9](#89-task-tree)) are sessions, and `lenny/delegate_task` creates a child session. Lenny defines these states independently of any external protocol, and external protocol adapters map to and from them at the boundary. The `TaskRecord` and `TaskResult` schemas and the `taskId` field carry the external-protocol vocabulary for the session's execution.
```

### 6.52 `spec/09_mcp-integration.md` §9.2 (elicitation-wait idle clock)

Replace the elicitation-wait pause rule with the `maxClientIdleSeconds` behavior: the clock runs while a session awaits an elicitation response and resets when the response arrives (Sections 2 and 3.1). The Section 6 row cites §9.1, but the anchored text sits in the §9.2 "Elicitation Timeout Semantics" list.

Replace list item 1, which reads `1. **Timer pause:** When a session is waiting for an elicitation response, the session's `maxIdleTime` timer is paused. The session is in a "waiting_for_human" state, not idle.`, with:

```
1. **Idle clock:** The `maxClientIdleSeconds` clock ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), [§6.2](06_warm-pod-model.md#62-pod-state-machine)) continues to run while a session waits for an elicitation response, because a wait on an absent client is the condition the bound exists to reclaim. The elicitation response is client activity and resets the clock when it arrives.
```

### 6.53 `spec/10_gateway-internals.md` §10.1 (whole-pod connection loss)

Re-key the whole-pod connection-loss paragraph from the removed `concurrent-workspace` mode name to `sessionPolicy.maxConcurrentSessions > 1`, and rename the replacement-trigger threshold's `maxConcurrent` to `maxConcurrentSessions` in lockstep with the §5.2 whole-pod-replacement-trigger and §6.2 threshold renames staged by the sibling directive rows.

Replace the paragraph that begins `**Concurrent-workspace pod connection loss.** When the gateway loses connection to a concurrent-workspace pod (a pod serving multiple active slots), all active slots on that pod simultaneously enter `resume_pending` state` and ends `preventing stale counters from blocking new slot assignments on the replacement pod.` with:

```
**Whole-pod connection loss when `maxConcurrentSessions > 1`.** When the gateway loses connection to a pod whose pool sets `sessionPolicy.maxConcurrentSessions > 1` (a pod serving multiple active slots), all active slots on that pod simultaneously enter `resume_pending` state, because the connection loss affects the whole pod rather than individual slots. The whole-pod replacement trigger ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): fires when `ceil(maxConcurrentSessions / 2)` or more slots **fail or leak** within a 5-minute window) also fires immediately on total connection loss regardless of the per-slot failure or leak count; total connection loss is always a whole-pod failure. The gateway atomically resets the Redis slot counter (`lenny:pod:{pod_id}:active_slots` → 0) and rehydrates it from `SessionStore.GetActiveSlotsByPod(pod_id)` on the replacement pod's first slot allocation after recovery, preventing stale counters from blocking new slot assignments on the replacement pod.
```

### 6.54 `spec/10_gateway-internals.md` §10.1 (partial-manifest slot_id sentinel)

Re-key the partial-manifest `slot_id` sentinel parenthetical from the removed mode names to the `maxConcurrentSessions` predicate; the `'default'` sentinel survives for single-session pools so the `(session_id, slot_id)` scoping key stays well-defined.

In the partial-manifest field enumeration, replace the parenthetical after `slot_id` that reads `(the concurrent-workspace slot identifier from [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes); set to the sentinel `'default'` for session-mode and task-mode pools so the `(session_id, slot_id)` scoping key is well-defined in every execution mode)` with:

```
(the per-slot identifier from [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), carried when the pool sets `sessionPolicy.maxConcurrentSessions > 1`; set to the sentinel `'default'` for pools with `maxConcurrentSessions: 1` so the `(session_id, slot_id)` scoping key is well-defined for every session)
```

### 6.55 `spec/11_policy-and-controls.md` §11.3 (max client idle row)

Replace the max-idle timeout row with the `maxClientIdleSeconds` bound, which supersedes the pool-level `runtime.limits.maxIdleTimeSeconds` knob and changes the default from 600s to the pool's effective `maxSessionAgeSeconds` (Sections 2 and 3.1); the clock's pause table lives in the rewritten §6.2 timer block staged by the spec/06 directive row.

Replace the table row that reads `| Max idle time                             | 600s    | `runtime.maxIdleTimeSeconds`                      | Yes                | [§6.2](06_warm-pod-model.md#62-pod-state-machine)           |` with:

```
| Max client idle time                      | Effective `maxSessionAgeSeconds` (7200s default) | `sessionPolicy.maxClientIdleSeconds`     | Yes (per pool)     | [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), [§6.2](06_warm-pod-model.md#62-pod-state-machine) |
```

### 6.56 `spec/11_policy-and-controls.md` §11.3 (task_complete_acknowledged timeout row)

Delete the `task_complete_acknowledged` timeout row: the between-task lifecycle frames are removed by the §4.7 and §15.4.1 deletions (Section 3.5), and the recycle boundary's missing-report bound is the gateway-side timeout derived from the §5.2 `cleanupTimeoutSeconds` knob (Section 3.4), which needs no new row here. The deleted span:

```
| `task_complete_acknowledged` timeout       | 30s     | (hard-coded; not configurable in v1)              | No                 | [§4.7](04_system-components.md#47-runtime-adapter)           |
```

### 6.57 `spec/12_storage-architecture.md` §12.4 (slot-counter key-table row and failure-behavior row)

The Redis slot counter becomes the intra-pod capacity gate for session-mode pools, capped by `sessionPolicy.maxConcurrentSessions`, with the §12.4-consistent Postgres fallback from Section 3.2 (every Redis-backed role has a durable fallback). The key-table row currently scopes the counter to the removed concurrent-workspace mode, and the failure-behavior table has no row for the counter at all, which the shipped `Counter == nil` fallback paths in `pkg/gateway/podclaim/slotclaimer.go` filled silently; the fallback becomes an explicit spec behavior here.

In the tenant-key-isolation table, replace the row that begins:

> `| `lenny:pod:{pod_id}:active_slots` | Concurrent-workspace slot counter | **Pod-scoped** (not tenant-prefixed — intentional, as pod IDs are already globally unique within the cluster). Tracks the number of concurrently active slots on a given pod.`

with:

```
| `lenny:pod:{pod_id}:active_slots` | Session-mode slot counter | **Pod-scoped** (not tenant-prefixed — intentional, as pod IDs are already globally unique within the cluster). Tracks the number of concurrently active sessions on a given pod; the atomic Lua increment is the intra-pod capacity gate for session-mode pools and caps at `sessionPolicy.maxConcurrentSessions` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). On Redis restart, counters reset to zero; the gateway performs **blocking rehydration** from `SessionStore.GetActiveSlotsByPod(pod_id)` before accepting new slot assignments — see [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) ("Post-recovery rehydration atomicity") for the full protocol. The Lua script checks a per-pod `rehydrated` flag (`lenny:pod:{pod_id}:rehydrated`) atomically; if absent, all slot allocation attempts for that pod block until rehydration completes, preventing concurrent requests from observing a stale zero counter. During a Redis outage the counter has a Postgres fallback — see the failure-behavior table below. |
```

In the **Failure behavior per use case** table, insert a new row immediately after the row `| Routing cache | **Fall back** to Postgres lookup |`:

```
| Session slot counter (`lenny:pod:{pod_id}:active_slots`) | **Postgres fallback under a per-pod advisory lock, then fail closed** — on Redis unavailability the gateway gates intra-pod session capacity on the `SessionStore.GetActiveSlotsByPod` count (the same source the blocking-rehydration protocol reads — see [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), serialized under a per-pod Postgres advisory lock so two concurrent admissions cannot both observe the same free slot. The fallback window is bounded: after `slotCounterPostgresFallbackMaxSeconds` (default: 60s) with Redis still unavailable, or immediately when Postgres is also unavailable, slot admission fails closed and new session dispatch requiring a slot on the pod is rejected. A Redis-only outage therefore degrades slot admission to Postgres latency rather than rejecting all session dispatch. On Redis recovery, the counter is rebuilt through the standard blocking-rehydration path before fast-path enforcement resumes. |
```

### 6.58 `spec/12_storage-architecture.md` §12.5 (per-slot checkpoint retention re-keyed to `maxConcurrentSessions`)

The per-slot checkpoint surfaces key off the removed concurrent-workspace mode and the relocated `maxConcurrent` knob; each re-keys to `sessionPolicy.maxConcurrentSessions > 1` so no §12.5 surface names a removed mode after application. These edits change in lockstep with the §5.2 slot-counter paragraphs and the §16.5 `CheckpointStorageHigh` re-key staged below.

In the first checkpoint-retention bullet, replace:

> In concurrent-workspace mode ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), checkpoints are per-slot — the "latest 2" limit applies independently to each slot, meaning a pod with `maxConcurrent: 8` may retain up to 16 checkpoint objects (8 slots × 2). The GC job and retention policy operate on `(session_id, slot_id)` pairs, not on sessions alone.

with:

```
On pools with `sessionPolicy.maxConcurrentSessions > 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), checkpoints are per-slot — the "latest 2" limit applies independently to each slot, meaning a pod with `maxConcurrentSessions: 8` may retain up to 16 checkpoint objects (8 slots × 2). The GC job and retention policy operate on `(session_id, slot_id)` pairs rather than on sessions alone.
```

In the **Checkpoint-storage sizing guidance** block, replace the bullet:

> `- **Task / stateless pools:** up to 2 retained checkpoints per session × concurrent sessions assigned to the pod.`

with:

```
  - **Session pools with `maxConcurrentSessions: 1` (with or without recycling) and service pools:** up to 2 retained checkpoints per session × concurrent sessions assigned to the pod (one session at a time for a `maxConcurrentSessions: 1` session pod, and up to the pool-level `maxConcurrent` for a service pod).
```

Replace the next bullet:

> `- **Concurrent-workspace pools:** up to `maxConcurrent × 2` retained checkpoints per pod (the "latest 2" rule applies independently per slot). A pod with `maxConcurrent: 8` therefore carries up to 16 checkpoint objects steady-state.`

with:

```
  - **Session pools with `maxConcurrentSessions > 1`:** up to `maxConcurrentSessions × 2` retained checkpoints per pod (the "latest 2" rule applies independently per slot). A pod with `maxConcurrentSessions: 8` therefore carries up to 16 checkpoint objects steady-state.
```

In GC concurrency model rule 5, replace the parenthetical:

> (per-slot in concurrent-workspace mode — see the first bullet above)

with:

```
(per-slot when `maxConcurrentSessions > 1` — see the first bullet above)
```

### 6.59 `spec/12_storage-architecture.md` §12.6 (`agent_pod_state` columns and `execution_mode` comment)

The per-pod recycle counters from Section 5 land as gateway-written nullable columns on `agent_pod_state`, the `execution_mode` SQL comment re-keys to the `session | service` enum, and the `state` comment re-keys to the coarse phase enum that Section 3.3 fixes (the current comment names a `running` value the enum does not carry and omits `reserved`). The cross-cluster `PodRecord` compatibility contract later in §12.6 carries a second, normative enumeration of the same canonical pod state set, cites §4.6.1 as its authority, and is re-keyed to the same coarse phase enum so the two §12.6 enumerations and the §4.6.1 authority (Section 6.5) agree on one canonical pod state set; leaving it unedited would let it forbid the `reserved` value this proposal introduces while permitting the removed `running` value against its own cited source. The columns are written by the gateway at session-rate events (Section 3.4), so the mirror-maintenance prose gains the ownership exception.

In the `agent_pod_state` schema block, replace the line:

> `    state           TEXT        NOT NULL,  -- pod state machine value (idle, claimed, running, draining, terminated, failed)`

with:

```sql
    state           TEXT        NOT NULL,  -- coarse pod phase (warming, idle, reserved, claimed, sdk_connecting, draining, failed, terminated)
```

Replace the line:

> `    execution_mode  TEXT        NOT NULL,  -- session, task, concurrent_workspace`

with:

```sql
    execution_mode  TEXT        NOT NULL,  -- session, service
```

In the **Cross-cluster `PodRecord` compatibility contract** subsection, re-key the `State` machine-values invariant so its enumeration matches the §4.6.1 coarse phase enum it cites (Section 6.5). Replace the line:

> `1. **State machine values.** The `State` field MUST be one of the canonical pod state machine values defined in [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) (`idle`, `claimed`, `running`, `draining`, `terminated`, `failed`). No implementation-specific states are permitted.`

with:

```
1. **State machine values.** The `State` field MUST be one of the canonical pod state machine values defined in [§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) (`warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, `terminated`). No implementation-specific states are permitted.
```

Insert after the line `node_name       TEXT,                  -- Kubernetes node hosting the pod`:

```sql
    sessions_served     INTEGER,           -- gateway-written at each session release; sessions served over the pod's lifetime, evaluated against recycle.maxSessionsPerPod at the recycle disposition
    scrub_failure_count INTEGER,           -- gateway-written; failed whole-pod scrubs, evaluated against recycle.maxScrubFailures
```

In the paragraph beginning `**`agent_pod_state` table schema.**`, append after the sentence ending "see [§4.2](04_system-components.md#42-session-manager) data model table).":

```
The `sessions_served` and `scrub_failure_count` columns are the exception to the WarmPoolController-maintained mirror: they are gateway-written recycle counters, incremented at each session release (`ReportSessionScrub`) and on each failed whole-pod scrub (`ReportPodScrub`) respectively ([§4.7](04_system-components.md#47-runtime-adapter)), and read by the recycle disposition ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

### 6.60 `spec/13_security-model.md` §13.1 (credential-isolation re-key)

The trust-boundary scoping and the strict-isolation remedy key off the removed `executionMode: concurrent`, `concurrencyStyle: workspace`, and `executionMode: task` names, and the `ConcurrentWorkspaceCredentialSharing` warning trigger names a pool kind that no longer exists; both re-key to `maxConcurrentSessions` so the trigger matches a configuration that exists. The condition name itself is retained.

In the **`lenny-cred-readers` membership boundary** paragraph, replace the closing span:

> because the agent container is already a single-tenant, single-session trust boundary in all modes except `executionMode: concurrent` with `concurrencyStyle: workspace` (see concurrent-mode clause below). For single-session and task-mode pods, the subprocess-inheritance surface is inside the trust boundary the session already owns.

with:

```
because the agent container is already a single-tenant, single-session trust boundary except on pools with `sessionPolicy.maxConcurrentSessions > 1` (see the per-slot credential-read clause below). On a pod that serves one session at a time (`maxConcurrentSessions: 1`, with or without recycling), the subprocess-inheritance surface is inside the trust boundary the session already owns.
```

Replace the entire **Concurrent-workspace mode credential-read scope** paragraph with:

```
**Per-slot credential-read scope (`maxConcurrentSessions > 1`).** On session-mode pools with `sessionPolicy.maxConcurrentSessions > 1` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), multiple slots share the same pod, the same agent UID, and therefore the same `lenny-cred-readers` group membership. Each slot's per-slot credential file at `/run/lenny/slots/{slotId}/credentials.json` ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)) is mode `0440` with `lenny-cred-readers` group ownership, so **any slot's agent code can read every other slot's credential file** via filesystem access. Lenny does not mitigate this at the pod level — per-slot tmpfs mounts with distinct per-slot GIDs are not used in v1. This property is covered by the existing `acknowledgeProcessLevelIsolation` deployer flag ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) alongside shared process namespace, `/tmp`, cgroup memory, and network stack; cross-slot credential-file readability is an instance of the same process-level co-tenancy the deployer has already accepted. Deployers requiring strict credential-lease isolation between simultaneous sessions MUST set `sessionPolicy.maxConcurrentSessions: 1`: the pod then holds at most one credential lease at a time, and sequential pod reuse under `recycle.enabled: true` keeps that property because each session's lease is released before the next session begins ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). The pool validation in [§4.7](04_system-components.md#47-runtime-adapter) pool admission additionally emits a warning-class condition `ConcurrentWorkspaceCredentialSharing=True` on the `SandboxWarmPool` CRD whenever a pool with `maxConcurrentSessions > 1` is created against a Runtime with non-empty `supportedProviders` (i.e., a credential-bearing runtime), ensuring the property is visible in pool status alongside the other process-level co-tenancy tradeoffs.
```

### 6.61 `spec/14_workspace-plan-schema.md` §14 (WorkspacePlan scope note and session-expiry feed)

The shared-template scope note re-keys from `concurrencyStyle: workspace` to `maxConcurrentSessions > 1`, in lockstep with the §4.4 and §6.4 per-slot re-keys, and drops the intra-session task vocabulary per the Section 3.5 session/task collapse. The `dev.lenny.session_expired` event keeps the `max_idle_time` reason value unchanged; only the feeding knob renames.

Replace the entire **Concurrent-workspace mode scope note** paragraph with:

```
**Per-slot shared-template scope note.** On pools with `sessionPolicy.maxConcurrentSessions > 1`, the `WorkspacePlan` serves as a shared template: the same sources, setup commands, and options are materialized independently for every slot on the pod (each into its own `/workspace/slots/{slotId}/current/` directory). Per-slot workspace differentiation — different files or environment per slot — is intentionally out of scope. All slots on a given pod are assigned sessions that share the same workspace plan; the pool model relies on this uniformity to pre-warm pods with a single workspace template. Clients that require different workspace content should create separate sessions (each with its own `WorkspacePlan`) rather than using per-slot overrides.
```

In the per-event `data` schemas list, replace the line:

> `- `dev.lenny.session_expired` (maxSessionAge or maxIdleTimeSeconds): `{ "session_id", "expiryReason": "max_session_age|max_idle_time" }``

with:

```
  - `dev.lenny.session_expired` (maxSessionAge or maxClientIdleSeconds): `{ "session_id", "expiryReason": "max_session_age|max_idle_time" }`
```

### 6.62 `spec/15_external-api-surface.md` §15.1, §15.2 (execution-mode values, `sessionIsolationLevel`, and the OpenAPI source document)

The §15.1 and §15.2 prose references `executionMode` only generically (the replay-semantics `targetRuntime` bullet and the `INCOMPATIBLE_RUNTIME` catalog row require a matching `executionMode` without enumerating values) and references `sessionIsolationLevel` without enumerating its fields, so the mode rename to `session | service` and the `conversationContinuity` addition require no §15.1 or §15.2 prose edit. The binding edits for this site are to the hand-authored gateway OpenAPI document `pkg/gateway/openapi/openapi.json` that the §15.1 OpenAPI endpoint prose ("Community SDK generators should target `/openapi.yaml` as the canonical source") and §15.2.1 item 4 name as the single authoritative schema, staged in Section 5 (the three `executionMode` enums to `["session","service"]`, the `concurrencyStyle` property removed, `conversationContinuity` added to `sessionIsolationLevel`, and `taskPolicy` replaced by `sessionPolicy`), and to the three hand-written client SDK `IsolationLevel` type files staged in Section 13 (`sdks/client/go/lenny/types.go`, `sdks/client/python/lenny/types.py`, and `sdks/client/typescript/src/types.ts`), each of which adds `conversationContinuity` alongside the `executionMode` value-set change. The §15.1 OpenAPI endpoint paragraph and the §15.6 generation sentence ("SDKs are generated from the OpenAPI spec (REST) and MCP tool schemas") are correct as written and are unchanged.

### 6.63 `spec/15_external-api-surface.md` §15.1 (externally visible and internal-only session states)

Restate the two internal-only-states sentences so the state-model name reflects the session/task 1:1 collapse (Section 3.5) and the storage attribution matches the coarse-enum reduction (Section 3.3): the `Sandbox` CRD `.status.phase` carries only the coarse occupancy phases, and the fine session states live solely in the Postgres session model. This mirrors the prior 0002's §7.13 replacement.

Replace the paragraph:

> **Externally visible vs. internal-only states.** The REST API (`GET /v1/sessions/{id}`) returns session states from the **session/task state model** ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model), 8.8), not the pod state model ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Pod states are internal implementation details not exposed to API callers.

with:

```
**Externally visible vs. internal-only states.** The REST API (`GET /v1/sessions/{id}`) returns session states from the **session state model** ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model), 8.8) rather than the pod state model ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)). Pod states are internal implementation details and are never exposed to API callers.
```

Replace the paragraph:

> Internal-only states (from the pod state machine in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine)) such as `warming`, `idle`, `claimed`, `receiving_uploads`, `running_setup`, `sdk_connecting`, and `resuming` are **never** returned in external API responses. These are tracked in the `Sandbox` CRD `.status.phase` for controller reconciliation and operational monitoring only.

with:

```
Internal-only states are **never** returned in external API responses. The coarse pod phases from the pod state machine in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) (`warming`, `idle`, `reserved`, `claimed`, `sdk_connecting`, `draining`, `failed`, and `terminated`) are tracked in the `Sandbox` CRD `.status.phase` for controller reconciliation and operational monitoring only. The fine session states `receiving_uploads`, `running_setup`, and `resuming` are tracked solely in the Postgres session model ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model), 8.8) and are likewise never returned in external API responses.
```

### 6.64 `spec/15_external-api-surface.md` §15.1 (error code catalog: `WARM_POOL_EXHAUSTED`)

Update the `WARM_POOL_EXHAUSTED` catalog row for the `onPoolExhausted` composition (Section 3.1): `queue` extends the existing per-pool claim queue by a bounded wait after the claim-path timeout and the Postgres fallback are exhausted, and the response carries a `Retry-After` header.

Replace the row:

> | `WARM_POOL_EXHAUSTED`       | `TRANSIENT` | 503         | No idle pods are available in the warm pool after exhausting both the API-server claim path and the Postgres fallback. Client should retry with exponential backoff. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle).                                                                  |

with:

```
| `WARM_POOL_EXHAUSTED`       | `TRANSIENT` | 503         | No idle pod could be acquired after exhausting both the API-server claim path and the Postgres fallback. On pools with `sessionPolicy.onPoolExhausted: queue`, the request additionally waited in the per-pool claim queue for up to `maxQueueWaitSeconds` before this error was returned. The response carries a `Retry-After` header; the client should retry with exponential backoff. See [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). |
```

### 6.65 `spec/15_external-api-surface.md` §15.4.1 (between-task lifecycle frames deleted)

Delete the between-task signaling paragraph in lockstep with the frame deletions from §4.7, `schemas/lifecycle-events.schema.json`, and `schemas/lenny-adapter-jsonl.schema.json` (Section 3.5); the `task_complete`, `task_complete_acknowledged`, and `task_ready` frames no longer exist on any channel.

Delete the paragraph (the span):

> **Task mode between-task signaling:** Adapter sends `{type: "task_complete", taskId: "..."}` on the lifecycle channel after a task completes. The runtime releases task-specific resources and replies with `{type: "task_complete_acknowledged", taskId: "..."}`. After deployer-defined `cleanupCommands` and Lenny scrub complete, the adapter sends `{type: "task_ready", taskId: "..."}` with the new task's ID. The runtime re-reads the adapter manifest (regenerated per task) and the next `{type: "message"}` on stdin is the start of the new task. This is distinct from `terminate`, which always means process exit.

### 6.66 `spec/15_external-api-surface.md` §15.4.1 (`slotId` re-keyed to `maxConcurrentSessions > 1`)

Re-key every §15.4.1 `slotId` reference from the removed `concurrent-workspace mode` and `session and task mode` names to the `sessionPolicy.maxConcurrentSessions > 1` predicate, consistent with the spec/06 per-slot block, the spec/10 `slot_id` re-keys, and the `schemas/lenny-adapter-jsonl.schema.json` `slotId` description re-key staged in Section 5.

In the inbound-message table, replace the row:

> | `message`     | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` for concurrent-workspace mode. |

with:

```
| `message`     | All content delivery: initial task, mid-session injection, reply to `request_input`, sibling notification. Carries optional `slotId` on pods whose pool sets `maxConcurrentSessions > 1`. |
```

Replace the row:

> | `tool_result` | The result of a tool call requested by the agent. Carries `slotId` in concurrent-workspace mode.                                                                    |

with:

```
| `tool_result` | The result of a tool call requested by the agent. Carries `slotId` when `maxConcurrentSessions > 1`. |
```

In the outbound-message table, replace the row:

> | `response`               | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` in concurrent-workspace mode. |

with:

```
| `response`               | Streamed or complete response carrying `OutputPart[]`. Carries `slotId` when `maxConcurrentSessions > 1`. |
```

Replace the row:

> | `tool_call`              | Agent requests execution of a tool. Carries `slotId` in concurrent-workspace mode.                    |

with:

```
| `tool_call`              | Agent requests execution of a tool. Carries `slotId` when `maxConcurrentSessions > 1`. |
```

Replace the multiplexing note:

> **`slotId` for concurrent-workspace multiplexing:** Session mode and task mode messages never carry `slotId` and runtimes for those modes never see it. Concurrent-workspace runtimes implement a dispatch loop keyed on `slotId` — each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent task streams through a single stdin channel.

with:

```
**`slotId` for concurrent-session multiplexing:** Messages on a pod whose pool sets `sessionPolicy.maxConcurrentSessions: 1` never carry `slotId`, and runtimes on those pods never see it. Runtimes serving a pool with `maxConcurrentSessions > 1` implement a dispatch loop keyed on `slotId`: each concurrent slot's messages carry a distinct `slotId` assigned by the adapter. This allows multiple independent concurrent session streams through a single stdin channel.
```

In the `MessageEnvelope` field reference, replace the field definition:

> **`slotId`** — optional string; present only in concurrent-workspace mode. Identifies the concurrent slot this message is addressed to. Session-mode and task-mode messages never carry `slotId`. See [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) and the `slotId` multiplexing note in the Protocol Reference.

with:

```
**`slotId`** — optional string; present only on pods whose pool sets `sessionPolicy.maxConcurrentSessions > 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). Identifies the concurrent slot this message is addressed to. Messages on pods serving one session at a time never carry `slotId`. See the `slotId` multiplexing note in the Protocol Reference.
```

In the Protocol Reference "Inbound: `message`" subsection, replace the sentence:

> Basic-level: read `type`, `id`, `input`. Ignore all other fields. `slotId` is optional — present only in concurrent-workspace mode.

with:

```
Basic-level: read `type`, `id`, `input`. Ignore all other fields. `slotId` is optional and is present only on pods with `maxConcurrentSessions > 1`.
```

In the "Inbound: `tool_result`", "Outbound: `response`", and "Outbound: `tool_call`" schema blocks, replace each of the three identical JSON schema lines:

> `  "slotId": "<string, optional — present only in concurrent-workspace mode>"`

with:

```
  "slotId": "<string, optional — present only on pods with maxConcurrentSessions > 1>"
```

### 6.67 `spec/15_external-api-surface.md` §15.4.3 (integration-level matrix and per-session manifest regeneration)

Remove the task-mode pod-reuse row from the Level Comparison Matrix and record that recycling is level-insensitive (Section 3.5: recycling requires no runtime cooperation and works at every integration level, which removes the Full-level-only restriction task mode carried), and re-key the per-task `mcpNonce` regeneration to per-session (Section 3.5: the per-task manifest and `mcpNonce` regeneration becomes per-session).

Delete the matrix row (the span):

> | **Task mode pod reuse**                                                    | No pod reuse. Adapter sends `shutdown` on stdin after task; pod replaced from warm pool. Effectively `maxTasksPerPod: 1` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).       | No pod reuse. Same as Basic — no lifecycle channel for between-task signaling.                                                             | Full pod reuse via `task_complete` / `task_complete_acknowledged` / `task_ready` on lifecycle channel. Scrub + reuse cycle as described in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). |

Insert a new paragraph between the end of the Level Comparison Matrix table (the row beginning "| **MessageEnvelope fields**") and the blockquote beginning "> **Basic-level limitations — complete list:**":

```
Pod recycling under `sessionPolicy.recycle` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) is not a level-sensitive capability and does not appear in the matrix: the platform scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime cooperation and is available at every integration level.
```

In the Standard-Level MCP Integration "Authentication" bullet, replace the closing parenthetical:

> The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string, regenerated per task execution alongside the rest of the manifest).

with:

```
The nonce is stored in the manifest under the top-level key `mcpNonce` (a random 256-bit hex string, regenerated per session alongside the rest of the manifest).
```

### 6.68 `spec/15_external-api-surface.md` §15.4.5 (runtime author roadmap execution-modes item)

Rewrite the Full-level roadmap item that enumerates the removed modes to the `session` and `service` modes with `sessionPolicy` (Sections 2 and 3.1).

Replace the list item:

> 11. **[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)** — Pool Configuration and Execution Modes. Execution modes (session, task, concurrent-workspace), resource classes, and pool sizing.

with:

```
11. **[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)** — Pool Configuration and Execution Modes. The `session` and `service` execution modes, the `sessionPolicy` block, resource classes, and pool sizing.
```

### 6.69 `spec/15_external-api-surface.md` §15.4.6 (conformance suite between-task rows)

Verification with no staged edit: the §15.4.6 test-category table contains no between-task signaling category (its Full-level categories are lifecycle channel opening, checkpoint quiesce/resume, interrupt acknowledgement, credential rotation handling, and deadline signal handling), and its observed-level probe references the `lifecycle_capabilities` / `lifecycle_support` exchange without naming the removed `task_lifecycle` capability value, so the between-task frame deletion leaves §15.4.6 unchanged. The corresponding schema-example deletions and the hardcoded example-path slice edits in `tests/tier0_static/schemas_test.go` are staged in Section 5.

### 6.70 `spec/15_external-api-surface.md` §15.7 (Runtime Author SDKs: graceful shutdown bullet)

Re-key the protocol-codec "Graceful shutdown" bullet to drop the `task_complete` / `task_ready` lifecycle-channel handling and the Full-level task-mode clause, because those frames and that mode are removed (Section 3.5); only the SIGTERM and `terminate` / `shutdown` deadline contract survives.

Replace the bullet:

>     - **Graceful shutdown.** SIGTERM handling and the `terminate` / `shutdown` deadline contract from [§4.7](04_system-components.md#47-runtime-adapter) and [§15.4.1](#1541-adapterbinary-protocol), plus `task_complete` / `task_ready` handling on the lifecycle channel for Full-level task-mode runtimes.

with:

```
    - **Graceful shutdown.** SIGTERM handling and the `terminate` / `shutdown` deadline contract from [§4.7](04_system-components.md#47-runtime-adapter) and [§15.4.1](#1541-adapterbinary-protocol).
```

### 6.71 `spec/15_external-api-surface.md` §15.7 (SDK `CreateRequest.TaskID` and `Message.TaskID` frozen to the root task identifier; `WorkspacePlan` per-slot comment)

Freeze the SDK `TaskID` field documentation to the root task identifier per Section 3.5 (each session has exactly one execution; the between-task `task_ready` re-invocation of `OnCreate` no longer exists), and re-key the `WorkspacePlan` field comment's per-slot parenthetical from the removed mode name to `maxConcurrentSessions > 1`, consistent with the §15.4.1 `slotId` re-keys.

In the `CreateRequest` Go block, replace the `TaskID` field and its doc comment:

> ```go
>     // TaskID is the current task identifier. Matches `taskId` in the
>     // adapter manifest. In task-mode pools ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))
>     // the SDK calls OnCreate again with a new TaskID after each
>     // `task_ready` lifecycle signal; in session-mode pools TaskID equals
>     // the root task ID.
>     TaskID string `json:"taskId"`
> ```

with:

```go
    // TaskID is the root task identifier for this session. Matches `taskId`
    // in the adapter manifest. Each session has exactly one execution, and
    // external protocols surface that execution as a Task
    // ([§7.1](07_session-lifecycle.md#71-normal-flow)), so TaskID is frozen
    // for the session's lifetime and OnCreate is invoked once with this
    // value.
    TaskID string `json:"taskId"`
```

In the `Message` Go block, replace the `TaskID` field and its doc comment:

> ```go
>     // TaskID is the active task the message belongs to. Populated from
>     // `taskId` in the adapter manifest; equals CreateRequest.TaskID at the
>     // time OnMessage is invoked.
>     TaskID string `json:"taskId"`
> ```

with:

```go
    // TaskID is the root task identifier of the session the message belongs
    // to. Populated from `taskId` in the adapter manifest; always equals
    // CreateRequest.TaskID, which is frozen for the session's lifetime.
    TaskID string `json:"taskId"`
```

In the `CreateRequest.WorkspacePlan` doc comment, replace the two comment lines:

> ```go
>     // `/workspace/current` (or the per-slot path in concurrent-workspace
>     // mode) before OnCreate is invoked; runtimes typically read this value
> ```

with:

```go
    // `/workspace/current` (or the per-slot path when the pool sets
    // `maxConcurrentSessions > 1`) before OnCreate is invoked; runtimes
    // typically read this value
```

### 6.72 `spec/16_observability.md` §16.1 (metric inventory: renames, descriptor re-keys, transition example, and new series)

The §16.1 inventory rows rename to the Section 2 metric names with their descriptions re-keyed to the session/recycle vocabulary, the retirement `reason` vocabulary replaces `task_count_limit` with `session_count_limit` (matching the re-keyed emitter vocabulary in `pkg/sandbox/taskcleanup`), the pod-state-transition example drops the removed `attached` phase for the coarse enum, and the Section 4 new series (the reserved-pod gauge and the idle-termination counter) gain inventory rows. The `lenny_pod_claim_queue_*` rows are unchanged and continue to describe the single claim queue; the `lenny_stateless_*` family appears nowhere in §16.1 (its sole spec home is §5.2), so no `lenny_service_*` rename lands in this file.

Replace the row:

> `| Task-mode per-pod scrub failure count (`lenny_task_pod_scrub_failure_count`, per pod, labeled by `k8s_pod_name`, `pool`, `runtime_class`)`

with:

```
| Per-pod scrub failure count (`lenny_pod_scrub_failure_count`, per pod, labeled by `k8s_pod_name`, `pool`, `runtime_class` — failed whole-pod scrubs on a recycling session-mode pod, evaluated against `recycle.maxScrubFailures` at the recycle disposition)                                                                                  | Gauge           |
```

Replace the row:

> `| Task-mode pod retirement (`lenny_task_pod_retirement_total`, labeled by `reason`: `task_count_limit`, `uptime_limit`, `scrub_failure_limit`, `pool`, `runtime_class`)`

with:

```
| Session-pool pod retirement (`lenny_pod_retirement_total`, labeled by `reason`: `session_count_limit`, `uptime_limit`, `scrub_failure_limit`, `pool`, `runtime_class` — retirement of a recycling session-mode pod at the recycle disposition)                                                  | Counter         |
```

Replace the row:

> `| Concurrent-workspace slot failure count (`lenny_slot_failure_total`, labeled by `error_type`, `pool`, `k8s_pod_name`)`

with:

```
| Session-slot failure count (`lenny_slot_failure_total`, labeled by `error_type`, `pool`, `k8s_pod_name` — per-slot failures on session-mode pods with `maxConcurrentSessions > 1`)                                                                                                  | Counter         |
```

Replace the row:

> `| Concurrent-workspace slot pod replacement (`lenny_slot_pod_replacement_total`, labeled by `pool`, `k8s_pod_name`)`

with:

```
| Session-slot pod replacement (`lenny_slot_pod_replacement_total`, labeled by `pool`, `k8s_pod_name` — whole-pod replacement of session-mode pods with `maxConcurrentSessions > 1`)                                                                                                      | Counter         |
```

In the pod-state-transition-durations row, replace the span:

> enables identifying slow transitions such as `idle → claimed` or `claimed → attached`

with:

```
enables identifying slow transitions such as `idle → claimed` or `claimed → reserved`
```

Replace the reuse-histogram row:

> `| Task-mode pod reuse count (`lenny_task_reuse_count`, labeled by `pool`, `k8s_pod_name` — number of tasks executed on a single pod in task mode; used to track recycling efficiency and enforce `maxTasksPerPod` retirement. Observed per-pod at task completion; the PoolScalingController uses `histogram_quantile(0.50, ...)` to derive `mode_factor` for task-mode pools — see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))`

with:

```
| Pod session reuse count (`lenny_pod_session_reuse_count`, labeled by `pool`, `k8s_pod_name` — number of sessions served by a single pod under `recycle.enabled`; used to track recycling efficiency and enforce `recycle.maxSessionsPerPod` retirement. Observed per-pod at session end; the PoolScalingController uses `histogram_quantile(0.50, ...)` to derive `mode_factor` for recycling session-mode pools — see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) | Histogram       |
```

Insert a new row immediately after the "Warm pods available (`lenny_warmpool_idle_pods`, ...)" row:

```
| Reserved pods (`lenny_warmpool_reserved_pods`, gauge labeled by `pool` — number of pods whose occupancy claim is in the `reserved` hold window: scrubbed, SDK-warm on preConnect pools, held for the pinned tenant until the claim-hold TTL expires, and excluded from idle inventory; see [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)) | Gauge           |
```

Insert a new row immediately after the "Resume success/failure rate (`lenny_session_resume_attempts_total`, ...)" row:

```
| Session expiries (`lenny_session_expiry_total`, counter labeled by `pool`, `reason`: `max_session_age` \| `max_idle_time` — sessions terminated by a platform expiry clock; the `max_idle_time` series counts client-inactivity terminations under the `maxClientIdleSeconds` bound ([§6.2](06_warm-pod-model.md#62-pod-state-machine)) and shares the `expiryReason` vocabulary of the `dev.lenny.session_expired` event ([§14](14_workspace-plan-schema.md))) | Counter         |
```

### 6.73 `spec/16_observability.md` §16.1.1 (attribute-table "Used on" cell and retirement `reason` vocabulary)

The `k8s_pod_name` attribute row and the normative `reason`-label paragraph both name the removed mode vocabulary; the latter is the second spec home of the retirement-trigger value set and re-keys in lockstep with the inventory row above.

In the attribute table, replace the row:

> `| `k8s.pod.name` | `k8s_pod_name` | Name of the Kubernetes pod the signal originates from or refers to | Task-mode scrub/retirement metrics, slot failure/replacement metrics, task reuse histograms |`

with:

```
| `k8s.pod.name` | `k8s_pod_name` | Name of the Kubernetes pod the signal originates from or refers to | Session-pool scrub and retirement metrics, slot failure and replacement metrics, and the session reuse histogram |
```

In the paragraph **Distinguishing `error.type` from `reason`.**, replace the span:

> pod retirement triggers (`task_count_limit`, `uptime_limit`, `scrub_failure_limit`)

with:

```
pod retirement triggers (`session_count_limit`, `uptime_limit`, `scrub_failure_limit`)
```

### 6.74 `spec/16_observability.md` §16.5 (`SandboxClaimGuardUnavailable`, `CheckpointStorageHigh`, and `PodClaimQueueSaturated` descriptions)

The `SandboxClaimGuardUnavailable` description states that a guard outage blocks "all `PATCH` and `PUT` operations on `SandboxClaim` resources." Under the seed-fixed per-pod model the webhook intercepts only `CREATE` and admits `PATCH`/`PUT` without inspection (§6.4), so a guard outage blocks new pod acquisition (the `SandboxClaim` `CREATE`) rather than binding-state `PATCH`/`PUT` writes. This alert is single-sourced in `pkg/alerting/rules/rules.go` and mirrored into the spec/16 catalog by `make generate`, so the description re-keys in both the rendered source and the spec mirror; editing only the spec prose would diverge from the rendered `PrometheusRule`. The `CheckpointStorageHigh` description is hand-authored in spec/16 and absent from the `pkg/alerting/rules` single source, so the alert regeneration cannot reach it; it re-keys to `maxConcurrentSessions` in lockstep with the §12.5 rewrite above. The `PodClaimQueueSaturated` trailing clause asserts applicability to all execution modes, which the Section 3.1 decision (the claim queue is scoped to session mode; service routing is claimless) makes false; the alert itself is otherwise unchanged.

In the `SandboxClaimGuardUnavailable` row (and the matching `Description` string in `pkg/alerting/rules/rules.go`), replace the span:

> With `failurePolicy: Fail`, all `PATCH` and `PUT` operations on `SandboxClaim` resources are blocked — new pod claims are prevented, halting session creation.

with:

```
With `failurePolicy: Fail`, every `SandboxClaim` `CREATE` is blocked; new pod acquisition is prevented, halting session creation.
```

In the `CheckpointStorageHigh` row, replace the sentence:

> Concurrent-workspace pools can multiply per-pod checkpoint footprint by `maxConcurrent × 2` — see the [§12.5](12_storage-architecture.md#125-artifact-store) scaling guidance for sizing.

with:

```
Session pools with `sessionPolicy.maxConcurrentSessions > 1` can multiply per-pod checkpoint footprint by `maxConcurrentSessions × 2` — see the [§12.5](12_storage-architecture.md#125-artifact-store) scaling guidance for sizing.
```

In the `PodClaimQueueSaturated` row, replace the span:

> indicates claim queue is backing up even though warm pods exist — applicable to all pool execution modes

with:

```
indicates claim queue is backing up even though warm pods exist — applicable to session-mode pools (service-mode routing is claimless and does not use the claim queue)
```

### 6.75 `spec/17_deployment-topology.md` §17.2 (sandboxclaim-guard admission-policy inventory item)

The admission-policy inventory item for `lenny-sandboxclaim-guard` describes the webhook's role as "double-claim prevention and tenant-scope enforcement on `SandboxClaim` PATCH/PUT operations." Under the seed-fixed per-pod model the webhook intercepts only `CREATE` (§6.4), so the role re-keys to per-pod claim uniqueness at `CREATE` in lockstep with the §6.4 guard paragraph, the §6.14 binding-state enumeration, the §6.79 spec/18 §18.10 deliverable, and the §6.85 TESTING.md §13.7 directive.

In the admission-policy inventory list, replace the item:

> 7. **`lenny-sandboxclaim-guard`** `ValidatingAdmissionWebhook` — double-claim prevention and tenant-scope enforcement on `SandboxClaim` PATCH/PUT operations (see [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).

with:

```
7. **`lenny-sandboxclaim-guard`** `ValidatingAdmissionWebhook` — per-pod claim uniqueness at `SandboxClaim` `CREATE` (double-claim prevention); it admits `PATCH` and `PUT` without inspection (see [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).
```

### 6.76 `spec/17_deployment-topology.md` §17.8.1 (operational-defaults max-idle row)

The quick-reference row for the idle bound renames to `maxClientIdleSeconds` with the Section 3.1 default (the pool's effective `maxSessionAgeSeconds`), in lockstep with the §11.3 max-idle row replacement; the §11.3 reference is retained.

In the operational-defaults table, replace the row:

> `| Max idle time                     | 600 s                                                                    | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |`

with:

```
| Max client idle time (`maxClientIdleSeconds`) | The pool's effective `maxSessionAgeSeconds` (7200 s at the default age cap) | [§11.3](11_policy-and-controls.md#113-timeouts-and-cancellation)     |
```

### 6.77 `spec/17_deployment-topology.md` §17.8.2 (delegation child-pool sizing restated in `sessionPolicy` terms)

The delegation-adjusted `minWarm` prose and the worked-example header key the `mode_factor` divisors to the removed task and concurrent modes and the removed `maxTasksPerPod` knob; both restate as `sessionPolicy` derivations, matching the §5.2 scaling-factor rewrite (session mode derives `mode_factor` from sessions per pod lifetime bounded by `recycle.maxSessionsPerPod`, and `burst_mode_factor = maxConcurrentSessions`).

Replace the paragraph:

> This formula assumes session mode when `mode_factor = 1.0` and `burst_mode_factor = 1.0`. For task-mode or concurrent-mode delegation child pools, apply the appropriate `mode_factor` and `burst_mode_factor` values from [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). Omitting these divisors for a task-mode pool with `maxTasksPerPod: 50` would over-provision by up to 50x.

with:

```
This formula assumes a one-session-per-pod pool (`maxConcurrentSessions: 1`, `recycle.enabled: false`) when `mode_factor = 1.0` and `burst_mode_factor = 1.0`. For delegation child pools that recycle pods (`recycle.enabled: true`) or serve concurrent sessions (`maxConcurrentSessions > 1`), apply the `mode_factor` and `burst_mode_factor` derivations from [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). Omitting these divisors for a recycling pool with `recycle.maxSessionsPerPod: 50` would over-provision by up to 50x.
```

Replace the worked-example header:

> **Tier 3 worked example (orchestrator preset, session mode `mode_factor = 1.0`, default `safety_factor = 1.2`, pod-warm pool with `pod_warmup_seconds = 10`, `failover_seconds = 25`):**

with:

```
**Tier 3 worked example (orchestrator preset, one-session-per-pod pool with `mode_factor = 1.0`, default `safety_factor = 1.2`, pod-warm pool with `pod_warmup_seconds = 10`, `failover_seconds = 25`):**
```

### 6.78 `spec/18_build-sequence.md` phase overview and phase table (Phase 12c rename)

The Phase 12c vertical is renamed from "Concurrent execution modes" to the `sessionPolicy` and service-mode vertical, so the overview prose and the phase-to-TESTING.md table follow the rename, and the TESTING.md link anchor follows the renamed §13.27 heading staged below.

In the numbered phase-group overview, in the item beginning "5. **Hardening and compliance (Phase 11 through Phase 14.5).**", replace the phrase:

> `` `type: mcp` runtimes, concurrent execution modes, full audit ``

with:

```
`type: mcp` runtimes, the `sessionPolicy` presets and service mode, full audit
```

In the phase table, replace the row:

> | 12c   | Concurrent execution modes                                             | —                                       | [§13.27](../TESTING.md#1327-phase-12c--concurrent-execution-modes)                                                                                          |

with:

```
| 12c   | `sessionPolicy` presets and service mode                               | —                                       | [§13.27](../TESTING.md#1327-phase-12c--sessionpolicy-presets-and-service-mode)                                                                              |
```

### 6.79 `spec/18_build-sequence.md` §18.10 (sandboxclaim guard PATCH/PUT rule removal)

The Phase 3.5 `sandboxclaim_guard` deliverable pins the guard's PATCH/PUT precondition to the single accept value `claimed`, which the re-keyed §4.6.1 guard rule (Section 3.2) removes entirely: in the per-pod model the guard's role is the `CREATE` per-pod-uniqueness check only, and the binding-state transitions that a per-session PATCH/PUT rule would have gated are `SandboxClaim.status`-subresource writes the webhook does not gate. In the deliverable bullet that names the ADR-007 enforcement, replace the clause:

> `` ; PATCH/PUT rejection when the referenced `Sandbox.status.phase` is not `claimed` ``

with the empty string, so the bullet retains only its `CREATE` clause and reads `- `pkg/admission/sandboxclaim_guard` (ADR-007 enforcement: CREATE rejection when a non-terminal `SandboxClaim` already exists).`

The CREATE clause of the same bullet ("CREATE rejection when a non-terminal `SandboxClaim` already exists") is retained unchanged: it already states the per-pod uniqueness rule, and the slot exemption it loses is not named in this deliverable.

### 6.80 `spec/18_build-sequence.md` §18.30 (Phase 12c deliverables and exit criteria)

Replace the entire §18.30 section, beginning with the heading `### 18.30 Phase 12c — Concurrent execution modes` and ending with the exit-criteria line "**Exit criteria.** Tier 4 `concurrent_workspace_test` and `concurrent_stateless_test` pass; Tier 5 `concurrent_modes_test` confirms admission webhooks and pod-level isolation hold per [TESTING.md §13.27](../TESTING.md#1327-phase-12c--concurrent-execution-modes).", with:

```
### 18.30 Phase 12c — `sessionPolicy` presets and service mode

**Deliverables.**

- Pod recycling across whole sessions (`sessionPolicy.recycle`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): the per-session slot cleanup and whole-pod scrub split, the reserved-hold claim window, and the retirement limits (`maxSessionsPerPod`, `maxPodUptimeSeconds`, and `maxScrubFailures`).
- Concurrent session slots (`sessionPolicy.maxConcurrentSessions > 1`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): per-slot workspaces, the Redis slot-counter capacity gate, and the `acknowledgeProcessLevelIsolation` requirement.
- Service execution mode (`executionMode: service`) per [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes): claimless tenant-affinity routing over the per-pod `maxConcurrent` slot bound, the `sessionIsolationLevel.conversationContinuity` contract field, and the registration-time warning for `multi_turn` runtimes on service-mode pools.
- Pod-level isolation enforcement for multiplexed pods (`maxConcurrentSessions > 1` and service-mode pools).

**Prerequisites.** Phase 12b exit gate.

**Exit criteria.** Tier 4 `session_recycle_test`, `session_concurrency_test`, and `service_mode_test` pass; Tier 5 `execution_modes_test` confirms admission webhooks and pod-level isolation hold per [TESTING.md §13.27](../TESTING.md#1327-phase-12c--sessionpolicy-presets-and-service-mode).
```

### 6.81 `spec/23_competitive-landscape.md` §23 comparison table and §23.1 (execution modes)

The comparison table and the "Why Lenny?" differentiator item name the removed mode set. In the comparison table, replace the row:

> | **Execution modes**        | session / task / concurrent (workspace + stateless) ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) | N/A            | N/A                       | N/A                         | N/A (workflow-based)     | N/A                         | N/A (graph-based)               |

with:

```
| **Execution modes**        | session (`sessionPolicy`-parameterized) / service ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) | N/A            | N/A                       | N/A                         | N/A (workflow-based)     | N/A                         | N/A (graph-based)               |
```

In §23.1, replace the differentiator item beginning "4. **Flexible runtime types and execution modes.**" in its entirety (its second sentence opens "Three execution modes for agent runtimes — `session` (1:1 pod, strongest isolation), `task` (sequential reuse with workspace scrub, tenant-pinned), and `concurrent` (slot multiplexing with `workspace` and `stateless` sub-variants)") with the following, which keeps the runtime-types sentence and the closing sentence verbatim, removes the mode count, and restates the scaling-factor sentence as the Section 3.1 derivation:

```
4. **Flexible runtime types and execution modes.** Two runtime types — `type: agent` (full task lifecycle with sessions, delegation, elicitation, multi-turn) and `type: mcp` (managed MCP server hosting with zero code changes) — serve fundamentally different workloads on the same platform ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime)). The execution modes for agent runtimes, `session` (a managed session bound to a pod, with a `sessionPolicy` block configuring pod recycling across sessions and concurrent session slots) and `service` (the runtime as a replicated service with claimless per-message routing), give deployers control over the pod-usage-to-isolation trade-off ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The PoolScalingController derives the adjustment factors (`mode_factor`, `burst_mode_factor`) from `sessionPolicy` properties and the service-mode per-pod capacity, so operators do not manually account for reuse or multiplexing when sizing pools. This combination of runtime types and execution modes allows a single Lenny deployment to serve diverse workload patterns without forcing operators into a one-size-fits-all model.
```

### 6.82 `spec/24_lenny-ctl-command-reference.md` (mode-reference sweep; no edits)

A sweep of §24 for `task` mode, `concurrent` mode, `concurrencyStyle`, `stateless`, `maxTasksPerPod`, `taskPolicy`, and the `executionMode` value set finds no reference to the platform execution modes. The "two execution modes: **standalone** and **API-backed**" prose in the `lenny-ctl preflight` section describes the CLI's own local-versus-API operation and is unrelated to the §5.2 mode enum; it is retained unchanged. No spec/24 edit is staged.

### 6.83 `spec/26_reference-runtime-catalog.md` §26.2 (shared-assets mode re-key)

The coding-agent workspace-layout convention keys `/workspace/shared/` to the removed `concurrent` mode. Replace the bullet:

> `` - `/workspace/shared/` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime), `sharedAssets`) is used only for `concurrent` pools; coding-agent runtimes default to `executionMode: session` and do not use it. ``

with:

```
- `/workspace/shared/` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime), `sharedAssets`) is used only on pools with `sessionPolicy.maxConcurrentSessions > 1`; coding-agent runtimes default to `executionMode: session` with one session per pod and do not use it.
```

The two `executionMode: session` lines in the §26.3 and §26.7 `runtime.yaml` examples remain valid under the new enum and are unchanged. The §26.10 sentence "the runtime is stateless relative to local filesystem" uses `stateless` as a filesystem adjective rather than the removed `concurrencyStyle` value and is unchanged.

### 6.84 `spec/27_web-playground.md` §27.2 and §27.6 (playground idle override re-key)

The playground idle override is defined against the removed `runtime.limits.maxIdleTimeSeconds` knob; it re-keys to `sessionPolicy.maxClientIdleSeconds` (Section 3.1), the playground-side `playground.maxIdleTimeSeconds` value keeps its name, and the `min()` rule is retained. In the §27.2 Helm value table, replace the row:

> `` | `playground.maxIdleTimeSeconds` | `300` | Hard override of the runtime's `maxIdleTimeSeconds` for playground-initiated sessions (bounded `60 ≤ v ≤ runtime's maxIdleTimeSeconds`). See [§27.6](#276-session-lifecycle-and-cleanup). | ``

with:

```
| `playground.maxIdleTimeSeconds` | `300` | Hard override of the pool's effective `sessionPolicy.maxClientIdleSeconds` for playground-initiated sessions (bounded `60 ≤ v ≤` the pool's effective `maxClientIdleSeconds`). See [§27.6](#276-session-lifecycle-and-cleanup). |
```

In §27.6, replace the bullet beginning "- **Idle-timeout override.**" in its entirety (its current text enforces "a **hard override** of the runtime's `maxIdleTimeSeconds` ([§7.2](07_session-lifecycle.md#72-interactive-session-model))" and states "The effective idle cap is therefore `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)`") with:

```
- **Idle-timeout override.** Playground-initiated sessions MUST NOT remain idle for longer than `playground.maxIdleTimeSeconds` (default: `300` / 5 min). The gateway enforces this value as a **hard override** of the pool's effective `sessionPolicy.maxClientIdleSeconds` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) whenever the session was established through a `/playground/*` ingress path, detected via the `origin: "playground"` JWT claim, which [§27.3](#273-authentication) stamps on session-capability JWTs for the `oidc`, `apiKey`, and `dev` auth modes alike. The effective idle cap is `min(maxClientIdleSeconds, playground.maxIdleTimeSeconds)`, where `maxClientIdleSeconds` is the pool's effective value; the override never relaxes a stricter platform bound and can only tighten a looser one. This caps the reclamation window after the best-effort cancel below fails to deliver.
```

In the next bullet, the closing clause "which — because of the override — fires within 5 min (default) rather than the runtime default of 10 min" cites the removed knob's 600-second default, which Section 3.1 replaces with a default equal to the pool's effective `maxSessionAgeSeconds`. Replace that clause with:

```
which, because of the override, fires within the 5-minute playground default rather than after the platform `maxClientIdleSeconds` default (the pool's effective `maxSessionAgeSeconds`, 2 hours by default)
```

### 6.85 `TESTING.md` §13.7 (sandboxclaim guard description)

The §13.7 `pkg/admission/sandboxclaim_guard` description pins the PATCH/PUT precondition to the single accept value `claimed` and asserts verbatim spec-message matching, so it must change in lockstep with the rewritten §4.6.1 guard paragraph and the §18.10 deliverable, both of which remove the PATCH/PUT rule from the per-pod guard. Replace the bullet:

> `` - `pkg/admission/sandboxclaim_guard` is the pure-decision logic for the `lenny-sandboxclaim-guard` webhook from §4.6.1 (ADR-007). CREATE rejects when a non-terminal `SandboxClaim` already exists for the same `Sandbox`; PATCH/PUT rejects when the referenced `Sandbox.status.phase` is not `claimed`. Rejection messages match the spec verbatim. ``

with:

```
- `pkg/admission/sandboxclaim_guard` is the pure-decision logic for the `lenny-sandboxclaim-guard` webhook from §4.6.1 (ADR-007). The guard enforces per-pod claim uniqueness at CREATE only: CREATE rejects when a non-terminal `SandboxClaim` already exists for the same `Sandbox`. The webhook does not gate PATCH/PUT; the per-pod claim spec is immutable after CREATE and binding-state transitions are `SandboxClaim.status`-subresource writes serialized by the §4.6.1 `resourceVersion`-precondition mechanism. The CREATE rejection message matches the spec verbatim.
```

### 6.86 `TESTING.md` §13.27 (Phase 12c title and test inventory)

The §13.27 title and test paths name the removed concurrent modes; the heading rename also renames the markdown anchor, and the two spec/18 links to `#1327-phase-12c--concurrent-execution-modes` (the phase table row and the §18.30 exit criteria) are updated to the new anchor in the spec/18 edits above so the build sequence and TESTING.md agree on the same deliverables. Replace the section, beginning with the heading `### 13.27 Phase 12c — Concurrent execution modes` and ending with the line "- `tests/tier5_e2e_kind/concurrent_modes_test.go` confirms admission webhooks and pod-level isolation hold.", with:

```
### 13.27 Phase 12c — `sessionPolicy` presets and service mode

**Test infrastructure to land.**
- `tests/tier4_integration/session_recycle_test.go`, `session_concurrency_test.go`, and `service_mode_test.go`.
- `tests/tier5_e2e_kind/execution_modes_test.go` confirms admission webhooks and pod-level isolation hold.
```

The existing `tests/tier4_integration/concurrent_workspace_test.go` and `concurrent_stateless_test.go` files in the tree are renamed or replaced by the implementation to match the new inventory; `tests/tier5_e2e_kind/concurrent_modes_test.go` does not yet exist.

## 7. Documentation changes

| Page | Change |
|:--|:--|
| `docs/getting-started/concepts.md` | Session/task definitions (1:1, Task as protocol name); the execution modes. |
| `docs/getting-started/architecture.md` | Mode overview, claim model, and occupancy projection. |
| `docs/operator-guide/scaling.md` | `sessionPolicy` presets, derived scaling factors, and pool sizing. |
| `docs/operator-guide/configuration.md` and `docs/reference/configuration.md` | The `sessionPolicy` block, removed `taskPolicy`/`concurrentWorkspacePolicy` keys, `maxClientIdleSeconds` replacing `maxIdleTimeSeconds` with the new default, and validation rules. |
| `docs/operator-guide/web-playground.md` | The playground idle override (`docs/operator-guide/web-playground.md:25`, `:48`): the `maxIdleTimeSeconds` example value and the superseded `min(runtime.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)` rule are re-keyed to `maxClientIdleSeconds`, mirroring the spec/27 §27.2 playground-formula rewrite (§6.84). |
| `docs/reference/cloudevents-catalog.md` and `docs/client-guide/webhooks.md` | The `dev.lenny.session_expired` event reason (`docs/reference/cloudevents-catalog.md:73`, `docs/client-guide/webhooks.md:58`) names the removed `maxIdleTimeSeconds` knob; re-keyed to `maxClientIdleSeconds` so the documented expiry trigger matches the renamed knob (the `max_idle_time` expiry-reason value is unchanged). |
| `docs/operator-guide/multi-tenancy.md` and `docs/operator-guide/namespace-and-isolation.md` | Tenant pinning and cross-tenant reuse as derivations; residual-state table. The `lenny-sandboxclaim-guard` description in `namespace-and-isolation.md` ("intercepting `CREATE`, `PATCH`, and `PUT` on `SandboxClaim` resources") is re-keyed to CREATE-only: the per-pod guard intercepts `CREATE` for per-pod uniqueness and no longer gates `PATCH`/`PUT` (binding-state writes are `SandboxClaim.status` patches serialized by resourceVersion preconditions, Section 5). |
| `docs/operator-guide/security-principles.md` | Scrub model (per-slot plus whole-pod-on-recycle) and acknowledgment derivations. |
| `docs/client-guide/session-lifecycle.md` | New page content: session-mode lifecycle with recycle; service-mode contract (no continuity and the context re-injection pattern); `conversationContinuity`. Sequence diagrams per preset and for service mode (the validated Mermaid set from the design discussion seeds these). |
| `docs/client-guide/index.md`, `docs/client-guide/error-handling.md`, SDK examples | Mode references, `sessionIsolationLevel` fields, and removal of task-mode caveats. |
| `docs/runtime-author-guide/lifecycle.md` and `docs/runtime-author-guide/adapter-contract.md` | Between-task frames removed; per-session manifest and nonce; scrub responsibilities. |
| `docs/runtime-author-guide/integration-levels.md` | Task-mode rows removed; recycling available at every level. |
| `docs/runtime-author-guide/publishing.md` | The `executionMode` enum (`docs/runtime-author-guide/publishing.md:209`) becomes `session` and `service`; the `sessionPolicy` block. |
| `docs/runtime-author-guide/testing.md` | The `TestTaskLifecycle` conformance case over the deleted frames (`docs/runtime-author-guide/testing.md:115`) removed. |
| `docs/runtime-author-guide/index.md` | The Full-level capability list (`docs/runtime-author-guide/index.md:175`): the task-mode pod-reuse line removed; recycling requires no runtime cooperation. |
| `docs/api/internal.md` | The `execution_mode` wire values (`docs/api/internal.md:116`) and mode examples. |
| `docs/about/comparisons.md`, `docs/about/why-lenny.md`, and `docs/about/contributing.md` | The execution-modes comparison row, the mode table, and the Full integration-level row describing task-mode pod reuse, rewritten to `session` with `sessionPolicy` and `service`. |
| `docs/assets/diagrams/task-mode-reuse.svg` | Removed and replaced by a recycle-lifecycle diagram, with the embedding image reference and ASCII fallback in `docs/runtime-author-guide/lifecycle.md` updated. |
| `docs/reference/state-machines.md` | Coarse pod enum, recycle edges, and the session state machine as the single fine-grained machine. |
| `docs/reference/glossary.md` | Session, Task, execution mode, recycle, and occupancy episode. |
| `docs/reference/metrics.md` | Metric renames and new rows. |
| `docs/api/rest.md`, `docs/api/mcp.md`, `docs/api/openai-completions.md` | Mode values, `sessionIsolationLevel`, and the multi-turn-in-service-mode note. |
| `docs/adr/0007-sandboxclaim-optimistic-locking.md` | Superseding note: per-pod claim granularity (new ADR or amendment). |
| `docs/runbooks/double-claim-verification.md`, `docs/runbooks/sandbox-claim-race.md`, `docs/runbooks/pool-bootstrap-mode.md` | Claim-name and projection changes. |
| `docs/runbooks/sandboxclaim-guard-unavailable.md` | The frontmatter symptom "SandboxClaim PATCH and PUT operations rejected by the failure policy" (`docs/runbooks/sandboxclaim-guard-unavailable.md:11`) describes a PATCH/PUT guard role the per-pod guard no longer has; re-keyed to the CREATE-only failure mode (the guard rejects `SandboxClaim` CREATE under its failure policy, blocking new pod claims), matching the `SandboxClaimGuardUnavailable` alert and §17.2 guard-role changes. |
| `docs/tutorials/*` touching sessions | Mode references where present. |
| A new dedicated page (location at convergence): "Execution modes and pod lifecycle" | The settings matrix, presets, sequence diagrams, residual-state and isolation table, and a decision guide including connectors. |

## 8. Non-goals

- A batch session-creation API, recorded as a follow-up candidate that pairs with pod recycling and the exhaustion queue for batch workloads.
- The Phase 2 lexical removal of internal "task" names. It is mechanical, large, and staged separately so this proposal is reviewable.
- Deprecating service mode in favor of connectors. The spec's existing guidance stands; the naming chosen here ages correctly if that deprecation later happens.
- Per-slot cgroup resource quotas, amortizing credential leases across sessions (lease-per-session is the revocation granularity the security model requires), and a `recycleAfterFailure` knob (failure always retires the pod).
- The upload-skipping fast path for workspace-less sessions, recorded as a follow-up.

## 9. Testing

- Pool admission: acknowledgment derivations, including the `acknowledgeMicrovmResidualState` rejection when `scrubProfile: in-place` is set without it, `maxSessionsPerPod` required when recycling, the per-slot cleanup floor, the categorical rejection of `recycle.allowCrossTenantReuse: true` when `maxConcurrentSessions > 1` regardless of isolation profile (including a microvm pool with `maxConcurrentSessions > 1` rejected, and the same knob admitted on the sequential-reuse microvm path with `maxConcurrentSessions: 1`), the microvm cross-tenant gate (`recycle.allowCrossTenantReuse: true` rejected on a non-microvm sequential-reuse pool and admitted on a microvm one), the T4 cross-tenant reuse prohibition (`recycle.allowCrossTenantReuse: true` rejected on a `workspaceTier: T4` microvm sequential-reuse pool), and the bounded-cohort combination admitted and exercised.
- Claim model: per-pod CREATE race between replicas, guard CREATE-only uniqueness (a second non-terminal claim for the same `Sandbox` is rejected, while a `SandboxClaim.status` binding-state patch, including the first empty `→ bound` patch landed while the referenced `Sandbox` is still `idle` and a `reserved → bound` rebind, is admitted because the webhook does not gate PATCH/PUT), binding-state serialization through the UID and `resourceVersion` preconditions rather than the webhook, episode extension under back-to-back same-tenant sessions, and orphan GC at both recycle settings. The orphan-GC coverage exercises every non-terminal binding state and the empty-status CREATE-before-status window: an orphaned `bound` or `recycling` claim whose binding-transition time plus the orphan timeout has passed with no active session drains the pod (the `recycling` case standing in for a coordinating-gateway crash during the scrub wait, where the claim carries no `holdExpiresAt`), an orphaned claim with empty status (no binding state, no binding-transition time, no `holdExpiresAt`) older than `claimOrphanTimeout` from its creation time with no active session referencing the pod drains the pod through the creation-timestamp fallback (the CREATE-but-no-status crash a status-only predicate cannot reach), and an orphaned `reserved` claim reclaims to `idle` after `holdExpiresAt` plus the grace period; the test asserts that neither a non-terminal binding state nor an empty-status claim is left unreclaimed, so the per-pod GC matches the shipped GC's phase-agnostic, status-independent coverage.
- Projection: every occupancy edge per Section 3.3, including the recycle path (`claimed` during the whole-pod scrub, then `sdk_connecting → reserved → idle` on preConnect pools after a successful scrub report, and `claimed → reserved → idle` otherwise, with no `sdk_connecting` leg at hold expiry), drain on disposition, uptime drains, and the drain-request annotation. The `sdk_connecting` watchdog admits `reserved` as a non-failure terminus and measures only the SDK re-warm leg rather than the pod's total Running time or the whole-pod scrub: a preConnect recycle on a pod whose total uptime already exceeds `sdkConnectTimeoutSeconds` (the prior occupancy episode, which the pod-start-relative clock would have counted) and whose whole-pod scrub itself runs for longer than `sdkConnectTimeoutSeconds` (which the `bound → recycling` anchor would have counted) reaches `reserved` when its SDK re-warm completes within the budget measured from the `rewarmStartedAt` stamp, and is not retired to `failed`, while a re-warm that itself exceeds the budget is retired; the scrub runs in the `claimed` projection bounded by the gateway-side missing-report timeout rather than by the watchdog, and the warm-fill path keeps the pod-start anchor and is unchanged. The recycle re-warm path drains rather than re-warming when the host node reads unschedulable.
- Disposition: `Decide` against `sessionPolicy` inputs; counter persistence on `agent_pod_state`; the scrub-report timeout retiring the pod; failed sessions always retiring the pod.
- Scrub: whole-pod scrub on the recycle edge, including the concurrent cohort case the current model misses.
- Service mode: claimless routing, tenant-affinity pinning, `conversationContinuity: "none"`, `podReuse: true`, and `residualStateWarning: true` in the create response, and the registration warning for `multi_turn` runtimes.
- Conformance and SDK: `TaskID` frozen; between-task frames absent; integration levels unaffected by recycling.
- Reserved hold: hold expiry deletes the claim and the pod reads `idle`; a same-tenant session within the TTL rebinds with a `reserved` to `bound` patch and no acquisition; a hold-expiry DELETE that races a cross-replica rebind fails its precondition, aborts the expiry, and leaves the bound claim intact; a reserved pod is skipped by the candidate scan and blocked by the CREATE guard; the orphan GC reclaims a reserved claim after a holder crash via `holdExpiresAt` plus the grace period; configuration validation warns on a high TTL.
- Exhaustion queue: FIFO ordering, the wait bound and its composition with the §4.6.1 claim-path timeout and Postgres fallback, no pod or claim held while queued, §7.1 atomicity on success and timeout, the `Retry-After` response, and the existing claim-queue metrics measuring the single queue.
- Idle timeout: not binding before the age cap at the default; binding when lowered; agent activity resets the clock, `suspended` pauses it, and it runs during `input_required` and elicitation waits; expiry carries the existing `max_idle_time` reason; the playground override still resolves through the most-restrictive path.
- Scrub reports: `ReportSessionScrub` increments `sessionsServed` and feeds the leak ledger; `ReportPodScrub` failures increment `scrubFailureCount` and reach `Decide`; the missing-report timeout retires the pod.

## 10. Findings closed on application

- **F-5.2.30** (task-mode gateway driver): re-triaged as superseded; task mode and its between-task transport are removed from the spec. The finding closes with a resolution note referencing this proposal rather than an implementation.
- The findings the prior 0002 listed for the ownership decomposition carry over and are re-validated during convergence.

## 11. Resolved in adversarial review

### Pass 1 (2026-06-11, automated)

- **Binding column misnamed `sessions.pod_id`:** corrected to `sessions.pod_assignment` (migration 0050, indexed by 0080; `SessionStore.PodAssignment`) in the Scope and Sections 2, 3.2, and 5, and the spec/05 row now stages the `sessions(pod_name)` prose correction so the spec, the migrations, and the session store agree on one column name.
- **Directives anchored on the unapplied prior-0002 baseline:** the proto and spec/04 §4.7 directives now add `ReportSessionScrub` and `ReportPodScrub` against the shipped baseline (the sole §4.7 row is `ExtendLease`; `ReportTaskCleanup` exists nowhere in the tree), the spec/18 row names the Phase 12c concurrent-execution-modes deliverables and test names as the lines to rewrite, and Section 3.2 states that the `Counter == nil` deletion (`slotclaimer.go:553`, `:562`) is carried forward as an edit of this proposal.
- **Between-task frame deletion targeted the proto:** the second deletion target is now `schemas/lenny-adapter-jsonl.schema.json` with the `schemas/examples/` frame instances (Sections 3.5, 5, and 13); `schemas/lenny-adapter.proto` is scoped to the new RPCs and the task-mode comment and `task_id` field cleanup.
- **Missing spec surfaces:** Section 6 (mirrored in Section 13) gains rows for spec/02, spec/09 §9.1, spec/10 §10.1, spec/11 §11.3, spec/12 §12.4 and §12.5, spec/13 §13.1, spec/14, spec/17 §17.8, spec/23, and spec/27, and the spec/04 row extends to §4.6.2 (scaling sentences and the reserved-as-occupied rule) and §4.9, and the spec/06 row to §6.4.
- **`reserved` missing from the enum directives:** the Section 5 Sandbox bullet now gains `reserved`, and the spec/06 row's recycle edges are restated as `claimed → sdk_connecting → reserved → idle` with the rebind edge `reserved → claimed`.
- **Contradictory SDK re-warm placement:** Section 3.2's ordering is kept everywhere: the scrub and re-warm run under a `recycling` claim state before the `reserved` patch, hold expiry is a pure claim-deletion projection with no second re-warm, and the `sdk_connecting` parenthetical is removed from the hold-expiry bullet and the spec/06 edge (Sections 3.2, 3.3, 3.4, 5, 6, and 9).
- **`maxSessionRetries: 2` under a claimed pure rename:** the staged value is now 1, preserving the existing `maxTaskRetries` semantics (default 1, 2 total attempts, 0 disables), and the spec/06 row covers the rename at spec/06:145, 187, and 190.
- **`maxClientIdleSeconds` contradicted the existing 600s `maxIdleTimeSeconds`:** the knob now explicitly replaces `runtime.limits.maxIdleTimeSeconds` and its §6.2 timer as the single clock with a single pause table; the default change from 600s is recorded as a decision in Sections 2 and 12, expiry reuses the existing `max_idle_time` reason, the most-restrictive resolution and playground override are retained, and edit rows for spec/06 §6.2, spec/09 §9.1, spec/11 §11.3, spec/14, spec/17 §17.8, and spec/27 are added.
- **Documentation pages missing from Section 7:** rows added for `docs/api/internal.md`, `docs/runtime-author-guide/publishing.md`, `testing.md`, and `index.md`, `docs/about/comparisons.md`, `why-lenny.md`, and `contributing.md`, and the `task-mode-reuse.svg` diagram with its ASCII fallback.
- **Service mode fell out of the `podReuse`/`residualStateWarning` derivations:** the derivation now covers both modes (true when `recycle.enabled`, when `maxConcurrentSessions > 1`, or when `executionMode` is `service`), Sections 3.1, 3.6, 4, and the spec/07 row carry the concurrent-stateless rationale and explicit service-mode values, and the Section 9 service-mode test asserts them.
- **Metric renames missing their emitters and consumers:** Section 13 gains `pkg/controller/poolscaling`, `pkg/gateway/gatewaymetrics`, `pkg/gateway/tenantaffinity`, `pkg/gateway/statelessproxy`, and `pkg/observability/metrics`, and Section 4 states that the demand-source PromQL and the `mode_factor` derivation follow the renames in the same change.
- **`task_complete` attributed to the runtime:** the Section 1 problem statement now attributes the boundary signal to the adapter, with the runtime's role limited to the acknowledgment.
- **Tenant-pin disposition at hold expiry unspecified and a false high-TTL rationale:** Section 3.2 now states that the `lenny.dev/tenant-id` pin persists across the recycle-to-idle edge on pools without microvm-gated `allowCrossTenantReuse`, that the candidate scan and inventory accounting treat pinned-idle pods as claimable only by the pinned tenant, and the warning rationale is rewritten to the delay of the pod's return to that tenant's inventory and of retirement-limit evaluation (Sections 2 and 3.2).
- **Orphan GC asked the WarmPoolController to scrub:** the orphan GC is now binding-state-aware and fail-closed: orphaned bound claims drain the pod regardless of recycle settings (the controller has no GatewayControl path to agent pods), and orphaned reserved claims, already scrubbed and re-warmed, are reclaimed to `idle` by precondition-guarded deletion (Section 3.3).
- **Hold-expiry DELETE unfenced against cross-replica rebinds:** every hold-expiry DELETE now carries Kubernetes preconditions (claim UID plus the resourceVersion observed at the `reserved` patch), a lost race aborts the expiry, the rebinding replica re-reads the claim before dispatch, any replica may rebind, and the interleaving is added to the Section 9 tests (Sections 2, 3.2, and 9).
- **Universal Redis gate fail-closed during a Redis outage:** the counter now follows the §12.4 fallback posture, matching the lease precedent: during a Redis outage the gateway gates capacity on the Postgres `GetActiveSlotsByPod` check under a per-pod advisory lock with a bounded fail-closed window, and the spec/12 row adds the §12.4 failure-behavior row and the key-table de-scoping (Scope and Sections 2, 3.2, and 6).
- **Reserved-claim orphan-GC predicate uncomputable:** the claim status now carries `holdExpiresAt` (stamped at the `reserved` patch) and the binding-state transition time; reserved claims reclaim at `holdExpiresAt` plus a grace period, and bound claims reclaim at the binding-transition time plus the orphan timeout, restoring the persist-grace window (Sections 2, 3.2, 3.3, and 5).
- **preConnect compatibility rule lost in the re-keying:** the derived rule (`preConnect: true` only when `maxConcurrentSessions` is 1; rejected for service mode) is added to Sections 3.1 and 5, and the spec/06 row re-keys the §6.1 compatibility table.
- **`lenny_task_*` metrics both renamed and deferred:** the Phase 2 list is scoped to Go identifiers, file paths, and proto field names, and Section 3.5 states that no `lenny_task_*` Prometheus series survives Phase 1.
- **Exhaustion queue contradicted the existing §4.6.1 claim queue:** `onPoolExhausted` now parameterizes the existing claim queue (wait up to `podClaimQueueTimeout`, Postgres fallback, then either rejection or the bounded `maxQueueWaitSeconds` extension), the existing `lenny_pod_claim_queue_*` series and `PodClaimQueueSaturated` alert continue to measure the single queue with no new series, and the spec/04 and spec/15 rows cover the affected paragraphs (Sections 3.1, 4, 6, and 9).
- **`reserved` had no `lenny.dev/state` projection:** a `reserved` pod and a `recycling` pod on a non-preConnect pool carry `active`, keeping the deliberately minimal label set; the consequence (outside the warm-pod PDB, no voluntary-disruption protection during the hold) is stated and accepted, the spec/04 and spec/06 rows stage the PDB-paragraph and label-table updates, and `pkg/sandbox/state` is added to Section 13 (Section 3.3). Pass 4 corrected the preConnect-recycling subcase, which projects to `sdk_connecting` and carries no label.

### Pass 2 (2026-06-11, automated)

- **TESTING.md §13.27 missing from the edit lists:** the spec/18 row already named "their TESTING.md §13.27 references" as part of the surface being rewritten, but `TESTING.md` itself appeared in no edit directive, so applying the proposal would leave the §13.27 section titled "Phase 12c — Concurrent execution modes" with the `concurrent_workspace_test`, `concurrent_stateless_test`, and `concurrent_modes_test` paths (TESTING.md:1746,1749-1750) while the spec/18 Phase 12c block was rewritten. Section 6 gains a `TESTING.md` §13.27 row that renames the section to the `sessionPolicy` and service-mode vertical and replaces the three test paths, keeping the spec/18 §13.27 anchor, and Section 13 names `TESTING.md` so the build sequence and its test inventory stay in agreement.
- **Phase 5 build-sequence guard deliverable restated the old rule:** the proposal re-keys the `lenny-sandboxclaim-guard` PATCH/PUT precondition, but spec/18:201 still asserted "PATCH/PUT rejection when the referenced `Sandbox.status.phase` is not `claimed`," which would contradict the changed guard after application. The spec/18 row was updated to reconcile the Phase 5 deliverable's PATCH/PUT clause with the §4.6.1 guard paragraph at spec/04:384. (Superseded by Pass 18: the per-pod guard's PATCH/PUT rule is removed rather than re-keyed, because binding-state transitions are `SandboxClaim.status`-subresource writes the webhook does not gate; the spec/18 §18.10 deliverable now deletes the PATCH/PUT clause and keeps only the CREATE per-pod-uniqueness clause.)
- **CEL validation rule referencing the removed `task` enum and `taskPolicy.maxTasksPerPod` uncovered:** the §4.6.1 CRD-validation CEL paragraph (spec/04:402) still asserted `taskPolicy.maxTasksPerPod > 0 when executionMode: task`, a configuration the proposal eliminates. The spec/04 §4.6.1 directive now re-keys that paragraph to the `sessionPolicy` rules (`sessionPolicy.recycle.maxSessionsPerPod > 0 when recycle.enabled` and `sessionPolicy.maxConcurrentSessions >= 1`) and notes the kubebuilder CEL markers in `pkg/apis/lenny/v1alpha1` regenerate in the same change. The directive also re-keys the `lenny-sandboxclaim-guard` webhook paragraph at spec/04:384 (CREATE per-pod uniqueness; the PATCH/PUT rule reconciled with the per-pod claim), and Section 5 carries the same guard posture so no drift is introduced. (Superseded by Pass 18 for the PATCH/PUT half: the per-pod guard's PATCH/PUT rule is removed rather than widened, because binding-state transitions are `SandboxClaim.status`-subresource writes serialized by the Section 3.2 `resourceVersion`-precondition mechanism. The CREATE per-pod-uniqueness re-key stands.)
- **`maxClientIdleSeconds` default "never binding" contradicted the `maxSessionAge` pause table:** the proposal claimed the idle clock "follows the `maxSessionAge` pause semantics" while also running during `awaiting_client_action`, and concluded the default-equal bound was "never binding until a deployer lowers it, because a session's idle time cannot exceed its age." `maxSessionAge` is paused during `awaiting_client_action` (`spec/06_warm-pod-model.md:268`) while the idle clock runs there, so the idle clock can accrue (for example up to `maxResumeWindowSeconds`, default 900s, in `awaiting_client_action`) while age does not, and the premise is false. Sections 2, 3.1, and 12 now state that the clock has its own pause table (paused during `suspended` and `finalizing`, running during the client-wait states), that `maxSessionAge` runs during `input_required` but is paused during `awaiting_client_action` so the two diverge there, that the default raises the platform idle bound to the age cap, and that the bound binds before the age cap when a deployer lowers it or when the session accrues idle time in the age-paused `awaiting_client_action` wait, which is the abandoned-client condition the bound exists to reclaim. The deliberate decision that the clock runs during client-wait states is preserved; the spec/06 §6.2 and spec/11 §11.3 directives reference the corrected Section 3.1.

### Pass 3 (2026-06-11, automated)

- **`task_lifecycle` capability orphaned by the frame deletion:** the proposal deleted the `task_complete`, `task_complete_acknowledged`, and `task_ready` frames but never removed the `task_lifecycle` capability value that gates them, which lives on the kept `lifecycle_capabilities` and `lifecycle_support` messages rather than on the deleted frame `$defs`. After the staged edits the schema and spec would still advertise a `task_lifecycle` capability that "governs the `task_complete` / `task_complete_acknowledged` / `task_ready` message exchange" (spec/04:694) for frames that no longer exist (schemas/lifecycle-events.schema.json:28, 41, 64). Section 3.5 and the Section 6 spec/04 row now remove the `task_lifecycle` value and its task-mode governance clause from the `lifecycle_capabilities` row prose at spec/04:694 and the `task_lifecycle` enum value and description from both capability arrays in schemas/lifecycle-events.schema.json (lines 28, 41, 64), and Section 5 stages the same schema removal. The §4.7 frame deletion was also mis-anchored to the Adapter → Gateway RPC table at spec/04:640-644 (which holds only `ExtendLease`); Sections 3.5 and 6 re-anchor it to the lifecycle-channel message table at spec/04:678-711 (the `task_complete` row at :701, the `task_ready` row at :702, the `task_complete_acknowledged` row at :708, the diagram labels at :681 and :685, the `terminate`-row cross-reference at :700, and the "Regenerated per task" manifest prose at :711).
- **`runtimestore` and `poolstore` edit sites missing from the files-touched list:** the mode-enum rename and `concurrencyStyle` removal change the closed typed enums in `pkg/gateway/runtimestore` (the `ExecutionMode` enum and `AllExecutionModes()` at runtimestore.go:1074-1084) and `pkg/gateway/poolstore` (the `ConcurrencyStyle` enum and validators at poolstore.go:380-403, the `TaskPolicy` struct at :268, the concurrent-workspace `Pool` fields at :50-84, and the preConnect-versus-concurrency rejection `ValidatePreConnectExecutionMode` at :686-696), neither of which appeared in the proposal. The CRD-side `pkg/apis/lenny/v1alpha1` carries `ExecutionMode` only as a plain `string` (sandbox_types.go:47) plus the policy structs, so it does not cover the closed enum or the gateway-side rejection. Section 13's code list now names `pkg/gateway/runtimestore` and `pkg/gateway/poolstore` with their specific edits, and Section 5 line 145 attributes the preConnect rejection to `ValidatePreConnectExecutionMode` in poolstore rather than to `pkg/admission/pool_config_validator`, re-keyed from the `executionMode: concurrent` plus `concurrencyStyle: workspace` predicate to `maxConcurrentSessions == 1` plus the service-mode rejection.
- **Recycling-binding-state claim had no reclamation path on a coordinator crash:** the orphan GC enumerated only two predicates (`bound` claims by binding-transition time, `reserved` claims by `holdExpiresAt`), so a claim left in `recycling` after the coordinating gateway replica crashed during the scrub wait was reclaimed by neither: a `recycling` claim carries no `holdExpiresAt` (stamped only at the `reserved` patch) and projects as `claimed` on a non-preConnect pool, so the `sdk_connecting` watchdog (spec/06:69, which fires only for the `sdk_connecting` projection) also could not reach it. This regressed against the shipped GC, which reclaims any orphaned claim regardless of phase by creation-time cutoff plus a `SessionActive` check (pkg/controller/warmpool/gc.go:157, 166). Section 3.3, Section 3.2, the Section 6 spec/04 §4.6.1 orphaned-claim-detection directive (spec/04:479), Section 3.4, and the Section 9 orphan-GC test row now broaden the live-state predicate to cover any non-terminal binding state other than `reserved` (`bound` and `recycling`) by binding-transition time plus the orphan timeout with no active session, reclaiming by draining the pod because the whole-pod scrub may not have completed and the controller has no GatewayControl path to scrub, which restores the shipped GC's phase-agnostic coverage.

### Pass 4 (2026-06-11, automated)

- **Orphan-GC live-binding-state set listed `sdk_connecting`, which the claim binding-state enum never holds:** the predicate is keyed to the claim binding state (Section 3.3), whose enumeration is `{bound, recycling, reserved, released, failed}` (Sections 3.2 and 5). `sdk_connecting` is a `Sandbox.status.phase` value that the `recycling` binding state projects to on preConnect pools (Section 3.3) rather than a binding state, so a binding-state-keyed predicate can never match it, and the `recycling` binding state already covers the coordinator-crash-during-scrub case the Pass-3 fix targets. The orphan-GC live-binding-state set in Sections 3.2, 3.3, the Section 6 spec/04:479 directive, the Section 9 test row, and the Pass-3 resolution narrows to the non-terminal binding states other than `reserved`, namely `bound` and `recycling`. `sdk_connecting` is retained only in the coarse Sandbox phase enum (Section 3.3), where it is a `Sandbox.status.phase` value the `recycling` binding state projects to during the SDK re-warm leg. (Pass 18 note: an earlier revision of this entry also named the guard PATCH/PUT accept-set as a retention site for `sdk_connecting`; Pass 18 removed that accept-set from the per-pod guard, so the coarse phase enum is now the only retention site.)
- **`executionMode` CRD enum markers mischaracterized as a plain string:** Section 5 line 145 had stated the CRD-side `ExecutionMode` is "a plain `string`" with no closed enum, routing the mode rename to the runtimestore/poolstore typed enums only and citing the policy structs at sandboxtemplate_types.go:93, 261-265. Both `executionMode` fields carry a `+kubebuilder:validation:Enum=session;task;concurrent` marker (sandbox_types.go:45 and sandboxtemplate_types.go:175) that generates the closed OpenAPI enum the API server enforces at admission, and the generated manifests carry it (charts/lenny/crds/lenny.dev_sandboxes.yaml:80-83, lenny.dev_sandboxtemplates.yaml:164-167, mirrored in pkg/embedded/crds/). Line 145 now states the closed enum lives on the CRD as those markers (in addition to the runtimestore typed enum), that both markers change to `session;service`, and that the four generated CRD manifests regenerate in the same change; the misattributed citation is corrected to the `SandboxTemplate` `ExecutionMode` field at sandboxtemplate_types.go:173-177; Section 13 adds `SandboxTemplate`, the four CRD manifests, and the specific `+kubebuilder:validation:Enum` marker edit to the regeneration list; and line 144 (which already names the CRD enum change) and line 145 now agree. Pass 13 corrected this entry's enumeration: a third `executionMode` enum marker exists on the `Runtime` CRD type (runtime_types.go:37), spec/05:536's spec-declared primary v1 home of `executionMode`, with its two generated manifests (`charts/lenny/crds/lenny.dev_runtimes.yaml` and `pkg/embedded/crds/lenny.dev_runtimes.yaml`, enum at :289-292), so the "two markers / four manifests" count understated the surface by one marker and two manifests.
- **Recycling-on-preConnect projection contradicted the carried-forward `active` label claim:** the `lenny.dev/state` label is derived solely from `CoarseState(phase)` with no input from the claim binding state (pkg/controller/sandbox/controller.go:483,500,515), and `CoarseState(sdk_connecting)` returns no coarse value (pkg/sandbox/state/state.go:59-71), so the label is removed. A `recycling` pod on a preConnect pool projects to `sdk_connecting` (Section 3.3), so it carries no `lenny.dev/state` label, contradicting the blanket claim that recycling pods carry `active`. The `sdk_connecting` phase cannot be remapped to `active` because it is shared with the warm-phase pre-connect state (warming → sdk_connecting → idle, spec/06:90), where the pod is unclaimed inventory that must stay unlabeled. Section 3.3, the spec/04 PDB directive (line 155), the spec/06 label-table directive (line 158), and the Pass-1 resolution (line 256) now exempt the preConnect-recycling subcase: it projects to `sdk_connecting` and carries no label during the re-warm window, matching the existing task-mode inter-task re-warm behavior (spec/06:154); a `reserved` pod and a `recycling` pod on a non-preConnect pool carry `active`. Section 3.3 states the CoarseState mapping gains only the `reserved → active` case and leaves `sdk_connecting` unmapped.
- **§6.2 "Concurrent-workspace pod lifecycle" and "Partial occupancy" paragraphs left unrestaged:** spec/06:194 asserts "the pod-level phase is `slot_active` whenever at least one slot is occupied" and contrasts session mode "and task mode (sequential tasks ...)", and spec/06:197 asserts "the pod label `lenny.dev/state` is `active` whenever `active_slots > 0`" and that "session-mode and task-mode pods transition labels immediately." These paragraphs name `slot_active`, `active_slots` (which maps to the dropped `Sandbox.status.activeSlots`), and task mode, all of which this proposal removes, and the generic carry-forward language did not reach them (they are §6.2 prose rather than the enum, a `task_cleanup` state, a between-task transition, the one-session-only invariant, or the §6.4 layout). The spec/06 §6.2 directive (line 158) now restages both paragraphs: remove the task-mode parenthetical, re-key `slot_active` to the coarse `claimed` phase under `sessionPolicy.maxConcurrentSessions > 1`, and re-key the `active_slots`-derived label-toggle prose (including the immediate-transition sentence) to the Redis-counter-derived occupancy, retaining the stabilization delay for `maxConcurrentSessions > 1`.

### Pass 5 (2026-06-11, automated)

- **Service-mode per-pod concurrency field removed with no successor:** Section 5 line 145 listed `MaxConcurrent` among the concurrent-workspace `Pool` fields the poolstore replacement deletes, but service mode (the renamed `concurrencyStyle: stateless`) keys its readiness-driven routing and saturation scaling to that per-pod slot bound, and `sessionPolicy`/`maxConcurrentSessions` is session-mode only (Section 3.1 line 52). After the deletion the service-mode readiness routing (a pod at slot capacity reporting readiness `false`, spec/05:500, consumed by `pkg/gateway/tenantaffinity`), the PoolScalingController saturation `active_slots / (pod_count × maxConcurrent)` (spec/05:573), and the service-mode scaling divisors `mode_factor = maxConcurrent` and `burst_mode_factor = maxConcurrent` had no defined divisor. Per the project principle of extending an existing surface rather than inventing a parallel one, `maxConcurrent` is retained as the service-mode per-pod slot bound (the `concurrencyStyle: stateless` mode already carries `maxConcurrent: 8`, spec/05:482). Section 3.1 line 81 now states the session-mode derivation and the service-mode `maxConcurrent` derivation separately, Section 3.6 names the retained field and the readiness dependency, Section 5 line 145 removes `MaxConcurrent` from the deletion list and states it is retained (poolstore.go:52-55), and the Section 6 spec/05 §5.2 directive and the spec/04 §4.6.2 directive carry the moved stateless scaling and readiness prose onto the retained field rather than the session-only `maxConcurrentSessions`.
- **spec/06:160-168 "Concurrent-workspace pod state transitions" fenced block kept the removed `slot_active` phase:** the spec/06 §6.2 directive (line 158) restaged the §6.2 prose paragraphs (spec/06:194, 197, Pass 4) and the §6.4 layout but never the fenced sub-block at spec/06:160-168, which contains `idle ──→ slot_active`, the `slot_active ──→ slot_active`/`idle`/`draining` transitions, `active_slots`, and `maxConcurrent`. After the enum edit drops `slot_active` and the status edit drops `Sandbox.status.activeSlots`, these transitions would reference a phase value and a status field that no longer exist, and the carry-forward could not reach them because the prior 0002 retained `slot_active` in its coarse enum and reused these exact transitions. The spec/06 §6.2 directive now restages the spec/06:160-168 fenced block and the adjacent `leaked` slot semantics paragraph (spec/06:179): re-key the pod-level `slot_active` transitions to the coarse `claimed` phase under `sessionPolicy.maxConcurrentSessions > 1`, replace `active_slots` with the Redis-counter-derived occupancy, and rename `maxConcurrent` to `maxConcurrentSessions` in those transitions and in the `leaked` paragraph's `ceil(maxConcurrent/2)` thresholds, with the per-slot sub-states block at spec/06:170-177 retained for `maxConcurrentSessions > 1`.
- **§4.6.1 etcd write-pressure budget (spec/04:431-439) left stale and absent from the edit list:** the budget's estimate assumes "~1 status write per pod per 2-minute lifetime (claim → active → released)" (spec/04:439), the lifecycle this proposal removes. The recycle path patches `SandboxClaim.status` through `bound → recycling → reserved → bound` per session (~3 PATCHes), and the WarmPoolController writes `Sandbox.status` as a projection of each transition; the `statusUpdateDeduplicationWindow` is scoped to the same `Sandbox` resource (spec/04:460) and does not coalesce the per-resource, time-spaced `SandboxClaim` PATCHes. The spec/04 §4.6.1 directive (line 155) now adds spec/04:431-439 with a revision that accounts the added per-session `SandboxClaim.status` and projection writes against the Tier-3 ~800 writes/s / ~120 QPS-after-dedup ceiling (spec/04:437), quantified at ~90 non-dedup'd `SandboxClaim` writes/s at the 30 claims/s Tier-3 steady-state (spec/17:999), and Section 3.2 line 93 now states that only the CREATE and DELETE cost amortizes while the binding-state PATCH traffic is added rather than reduced.
- **TESTING.md §13.7 Phase 3.5 guard PATCH/PUT description left stale by the guard re-key:** the proposal re-keys the `lenny-sandboxclaim-guard` PATCH/PUT accept-set at spec/04:384 and spec/18:201, but its only TESTING.md directive was scoped to §13.27. TESTING.md:1555 documents the guard's PATCH/PUT rule verbatim ("PATCH/PUT rejects when the referenced `Sandbox.status.phase` is not `claimed`") and asserts "Rejection messages match the spec verbatim," which would no longer hold after application. The TESTING.md directive now covers §13.7 as well as §13.27, rewriting the §13.7 guard description to match the per-pod guard, and Section 13 names §13.7 alongside §13.27. (Superseded by Pass 18: the §13.7 description states the guard enforces per-pod uniqueness at CREATE only and does not gate PATCH/PUT, because binding-state transitions are `SandboxClaim.status`-subresource writes serialized by the Section 3.2 `resourceVersion`-precondition mechanism, rather than rejecting on a `claimed`/`sdk_connecting`/`reserved` accept-set.)
- **Client SDK `IsolationLevel` structs missing from the edit lists:** the three hand-written client SDK type files mirror the §7.1 `sessionIsolationLevel` object (`sdks/client/go/lenny/types.go` `IsolationLevel` at :71-77, `sdks/client/python/lenny/types.py` `IsolationLevel` dataclass at :112-119 and its `from_wire` decode at :122-129, and `sdks/client/typescript/src/types.ts` `IsolationLevel` at :71-77), none carries `conversationContinuity`, and none has a codegen marker. Because the structs deserialize the `POST /v1/sessions` response, an unrecognized JSON field is silently dropped, so the new client-facing contract field the proposal mandates be surfaced would be unreachable by clients. Section 13 now names the three SDK type files with the `conversationContinuity` addition (and its `conversation_continuity` decode in the Python `from_wire`), kept distinct from the Section 7 "SDK examples" documentation row.
- **`SandboxClaim` CRD manifests omitted from the regeneration list:** the proposal edits the `SandboxClaim` Go type (drop `SlotID`/`SessionID`, change the binding-state enum, add `holdExpiresAt` and the binding-state-transition time) but named only the four `executionMode`-enum manifests (Sandbox and SandboxTemplate) for regeneration; the string `lenny.dev_sandboxclaims.yaml` appeared nowhere. The current generated manifests still carry `required: sessionId` (charts/lenny/crds/lenny.dev_sandboxclaims.yaml:86-88), the `slotId` field (:74), the `.spec.sessionId` printColumn (:25), and the `status.phase` enum `bound; active; released; failed` (:168-172), mirrored in the `pkg/embedded/crds/` copy, so without regeneration the API server rejects a per-pod claim created without `sessionId` and a `phase: reserved`/`recycling` status patch. Section 5 line 143 and Section 13 line 297 now add `charts/lenny/crds/lenny.dev_sandboxclaims.yaml` and its `pkg/embedded/crds/` copy to the `make generate` regeneration list with the specific schema changes.

### Pass 6 (2026-06-11, automated)

- **§6.2 host-node schedulability and preConnect scrub-warning re-warm paragraphs and §4.6.1 host-node labeling left dangling on removed `task_cleanup` transitions:** the spec/06 §6.2 directive enumerated the §6.2 surfaces to restage (spec/06:160-168, 179, 194, 197) but omitted the titled prose paragraphs "Host-node schedulability precondition" (spec/06:181) and "preConnect re-warm on scrub_warning" (spec/06:183), and the spec/04 §4.6.1 directive listed spec/04:475 and :479 but not the WarmPoolController-side "Host-node schedulability labeling" paragraph (spec/04:481). All three reference the removed `task_cleanup → sdk_connecting` transitions; spec/06:181 and :183 also name the removed `maxTasksPerPod` knob, and spec/06:183 names `lenny_task_pod_scrub_failure_count`, which this proposal renames to `lenny_pod_scrub_failure_count` (pkg/observability/metrics/catalog.go:76). The "Remove the `task_cleanup` states and between-task transitions" language does not reach titled prose paragraphs. The spec/06 §6.2 directive (Section 6) now restages spec/06:181 and :183, re-keying their references to the recycle re-warm edge `claimed → sdk_connecting`, replacing `maxTasksPerPod` with `recycle.maxSessionsPerPod`, renaming the metric, and restating the schedulable-node gate so the recycle re-warm path drains on a cordoned node (consistent with the "unschedulable host node" retire trigger in Section 3.4); the spec/04 §4.6.1 directive now restages spec/04:481, re-keying the labeling contract and the still-`Pending` eligibility sentence to the `claimed → sdk_connecting` recycle re-warm edge while keeping the gateway's existing-`get`-on-`Pods` label read and the no-new-Node-RBAC posture.
- **§15.4.1 `slotId` and §15.4.5 execution-mode references to the removed `concurrent-workspace` and `task` modes absent from the edit directives:** the proposal re-keys the parallel spec/06:170-177 per-slot block and spec/10:54,141 `slot_id` prose to `maxConcurrentSessions > 1` but left the identical §15.4.1 `slotId` protocol references (spec/15:1440, 1441, 1451, 1452, 1459, 1713, 1818, 1846, 1879, 1903) and the §15.4.5 execution-modes list (spec/15:2372, "session, task, concurrent-workspace") keyed to mode names the proposal removes, so the applied §15 would name a `concurrent-workspace mode` and a `task mode` that no longer exist. Section 3.5 and the Section 6 spec/15 directive now re-key the §15.4.1 `slotId` references to `maxConcurrentSessions > 1` (consistent with the spec/06 and spec/10 re-keys) and rewrite the §15.4.5 list to the `session` and `service` modes with `sessionPolicy`.
- **`scrubProfile: in-place` dropped the mandatory `acknowledgeMicrovmResidualState` cross-tenant gate:** absorbing `microvmScrubMode` into `recycle.scrubProfile` (with the `in-place` value reachable) while keeping `allowCrossTenantReuse` and enumerating "exactly" two acknowledgments silently removed the spec/05:442 mandatory `acknowledgeMicrovmResidualState: true` acknowledgment, which is a fail-closed admission gate enforced at pkg/admission/pool_config_validator/validator.go:551-556 and pkg/gateway/poolstore/poolstore.go:629-631 and exists as a `TaskPolicy` CRD field (sandboxtemplate_types.go:44) because in-place scrub leaves guest-kernel residual state (DNS cache, TCP `TIME_WAIT`, page cache, inotify/fanotify registrations) persisting across the tenant boundary. Section 3.1 adds an `acknowledgeMicrovmResidualState` field to `sessionPolicy.recycle` and a derivation rule requiring it when `scrubProfile: in-place`; the §5.2, poolstore, pool_config_validator, and `pkg/apis/lenny/v1alpha1` directives (Sections 5, 6, and 13) carry the field and its rejection forward onto the new `recycle` structure and re-anchor the spec/05:442 text rather than deleting it with the `TaskPolicy` struct, and the Section 9 pool-admission test exercises the rejection.
- **`sdk_connecting` watchdog (spec/06:69) not re-keyed for the new `sdk_connecting → reserved` recycle edge:** the proposal adds the `sdk_connecting → reserved` edge and asserts the recycle re-warm is bounded by the existing `sdkConnectTimeoutSeconds` watchdog, but spec/06:69 defines `idle` as the only non-failure exit from `sdk_connecting` (implemented at pkg/controller/sandbox/lifecycle/lifecycle.go:181-191, where `PodReady → idle` and timeout `→ failed`), and the §6.1 watchdog paragraph was absent from the edit list. Because the recycle whole-pod scrub (`cleanupTimeoutSeconds` 30-60s) plus SDK re-warm (30-90s) can exceed the 60s `sdkConnectTimeoutSeconds`, a watchdog measuring elapsed time in the projected `sdk_connecting` phase would retire a legitimately recycling preConnect pod to `failed`. The spec/06 §6.1 directive (Section 6) now re-keys spec/06:69 so its non-failure terminus includes `reserved` as well as `idle` and the Section 9 projection test asserts that a preConnect recycle reaching `reserved` within the budget is not retired to `failed`. Pass 7 corrected a deeper defect in this same reconciliation: the shipped watchdog clock is `now − pod.Status.StartTime` (`pkg/controller/sandbox/sdkwarm.go:105,114-122`) rather than phase-entry-relative, and `TimedOut()` fires whenever the pod is `sdk_connecting`, NotReady, and that elapsed time exceeds the budget (`pkg/controller/sandbox/lifecycle/lifecycle.go:138-142,186-191`), so on the recycle edge the pod's total Running time (up to `maxPodUptimeSeconds`) is already past the 60s budget and the timeout branch fires before the new `sdk_connecting → reserved` exit can run. Section 3.3, the spec/06:69 directive, and Section 13 now re-anchor the watchdog clock on the recycle re-warm edge to the time the pod entered `sdk_connecting` (read from the binding-state-transition time the gateway records on `SandboxClaim.status` at the `bound → recycling` patch, Section 3.2), distinct from the warm-fill path that keeps the pod-start anchor, so the prior occupancy episode is not counted against the re-warm budget; Section 13 names both the recycle-edge clock re-anchoring in `pkg/controller/sandbox` and the `sdk_connecting → reserved` non-failure exit in `pkg/controller/sandbox/lifecycle`, and the Section 9 projection test exercises a preConnect recycle whose total uptime already exceeds `sdkConnectTimeoutSeconds` and asserts it reaches `reserved` rather than `failed`.
- **`ConcurrencyStyle` CRD field and its kubebuilder enum marker not in the edit lists:** the proposal removes the `concurrent` mode and the gateway-side poolstore `ConcurrencyStyle` enum but never listed removing the standalone `ConcurrencyStyle` field on the `SandboxTemplate` CRD type (sandboxtemplate_types.go:185), its `+kubebuilder:validation:Enum=stateless;workspace` marker (sandboxtemplate_types.go:183), or the generated `concurrencyStyle` schema block in the manifests (charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:81-90 and the `pkg/embedded/crds/` copy at :81), so the applied CRD would still advertise a `stateless;workspace` enum and a doc referencing the removed mode. The field is distinct from the `executionMode` marker (the sibling Pass 4 corrected) and from both policy structs. Section 5 line 145 and Section 13 line 306 now remove the field, its marker, and its doc comment from `pkg/apis/lenny/v1alpha1`, and the SandboxTemplate-manifest regeneration scope is broadened beyond "the manifests carrying the `executionMode` enum" to drop the `concurrencyStyle` schema block in the same `make generate` run.

### Pass 7 (2026-06-11, automated)

- **`maxIdleTimeSeconds` rename left three §6.2 titled paragraphs outside the staged 273-290 range naming the removed knob:** the spec/06 §6.2 idle-clock directive renamed `maxIdleTimeSeconds` to `maxClientIdleSeconds` only inside spec/06:273-290, but three §6.2 titled prose paragraphs name the knob outside that range and the generic replacement language does not reach them: "Interaction with other timers during podless suspension" (spec/06:242, "`maxIdleTimeSeconds` remains paused"), "`resume_pending` wall-clock cap" (spec/06:292, "Although `maxSessionAge` and `maxIdleTimeSeconds` are both paused during `resume_pending`"), and "`suspended → expired` trigger mechanism" (spec/06:294, "Both `maxSessionAge` and `maxIdleTimeSeconds` are paused during `suspended`"). Applying the proposal would leave §6.2 defining `maxClientIdleSeconds` while three sibling paragraphs referenced a removed `maxIdleTimeSeconds`, the same defect class Pass 4 and Pass 6 fixed for other §6.2 titled paragraphs. The Section 6 spec/06 §6.2 directive (line 159) now widens to restage spec/06:242, :292, and :294, renaming the knob in each, and reconciles them with the Section 3.1 pause table: the pause-during-`suspended` claims at :242 and :294 carry forward unchanged, and :292's "both paused during `resume_pending`" claim is kept accurate by making the new clock paused during the recovery states `resume_pending` and `resuming` (the session cannot make progress and is not awaiting a present client there), which matches the existing `maxIdleTimeSeconds` pause at spec/06:292. The recovery-state pause is propagated to the Section 2 decision bullet (line 41), the Section 3.1 clock description (line 86), and the Section 12 `maxClientIdleSeconds` resolved decision so the pause table is stated identically everywhere.
- **§12.6 `agent_pod_state.execution_mode` SQL comment and §12.5 concurrent-workspace checkpoint bullets named removed modes, absent from the spec/12 directive:** the spec/12 §12.6 directive was scoped to "column changes" (adding `sessions_served` and `scrub_failure_count`) and did not re-key the existing `execution_mode` column comment at spec/12:465 (`-- session, task, concurrent_workspace`), which would contradict the §5.2/CRD enum the proposal sets to `session | service`; the §12.5 directive cited only spec/12:325 and qualified it as "the task/stateless" bullets, leaving the parallel concurrent-workspace checkpoint surfaces unnamed (the "Concurrent-workspace pools" retained-checkpoint bullet at spec/12:326, the per-slot exception at spec/12:313, and the GC-guard restatement at spec/12:336). The Section 6 spec/12 directive (line 165) now re-keys the `execution_mode` comment at spec/12:465 to `-- session, service` and restates spec/12:313, :326, and :336 in `sessionPolicy.maxConcurrentSessions > 1` terms (renaming `maxConcurrent` to `maxConcurrentSessions` in the `maxConcurrent × 2` and `maxConcurrent: 8` examples), so no spec/12 surface names a removed mode after application.
- **Recycle re-warm watchdog clock was pod-Running-relative, so every recycling preConnect pool would retire to `failed`:** the proposal asserted the recycle re-warm is bounded by "the existing `sdkConnectTimeoutSeconds` watchdog" and that the clock "arms at the `claimed → sdk_connecting` patch," but the shipped watchdog clock is `now − pod.Status.StartTime` (`pkg/controller/sandbox/sdkwarm.go:105,114-122`) rather than phase-entry-relative, and `TimedOut()` fires whenever the pod is `sdk_connecting`, NotReady, and that elapsed time exceeds the budget (`pkg/controller/sandbox/lifecycle/lifecycle.go:138-142,186-191`). On the recycle edge the pod has already been Running for the whole occupancy episode (up to `recycle.maxPodUptimeSeconds`), so the elapsed time is far past the 60s budget the instant the pod projects `sdk_connecting` and the readiness gate holds it NotReady during the re-warm, so the timeout branch retires the pod to `failed` before the new `sdk_connecting → reserved` exit can run; the single staged code change (adding that exit) does not prevent it, and `SandboxStatus` carries no per-phase-entry timestamp. Section 3.3 (line 114), the spec/06:69 directive (line 159), the Pass-6 resolution (line 293), and Section 13 (line 315) now stage a `pkg/controller/sandbox` change that re-anchors the recycle-edge watchdog clock from `pod.Status.StartTime` to the phase-entry time read from the binding-state-transition stamp the gateway records on `SandboxClaim.status` at the `bound → recycling` patch (Section 3.2), distinct from the warm-fill path that keeps the pod-start anchor, and name both the clock re-anchoring and the `sdk_connecting → reserved` exit. The Section 9 projection test (line 218) now exercises a preConnect recycle on a pod whose total uptime already exceeds `sdkConnectTimeoutSeconds` and asserts it reaches `reserved` rather than `failed`.

### Pass 8 (2026-06-11, automated)

- **Recycle-edge `sdk_connecting` watchdog still covered scrub plus re-warm while sized for re-warm alone, so the watchdog would still retire recycling preConnect pods:** Pass 7 re-anchored the watchdog clock from `pod.Status.StartTime` to the `bound → recycling` patch time, which removed the prior occupancy episode from the budgeted window but left the whole-pod scrub inside it. The gateway stamps the `bound → recycling` transition when occupancy reaches zero, before the adapter runs the scrub (Section 3.4), and the `recycling` claim projected to `sdk_connecting` for the whole scrub plus re-warm window with no intermediate state, so the level-triggered watchdog (`TimedOut()` over `sdk_connecting` + NotReady + elapsed > budget, `pkg/controller/sandbox/lifecycle/lifecycle.go:138-142`) ran across the scrub too. By the proposal's own durations the scrub (`cleanupTimeoutSeconds`, up to 60s) plus re-warm could exceed the 60s `sdkConnectTimeoutSeconds` (default per spec/06:69), which is sized for the re-warm alone because the current model runs the scrub in a dedicated `task_cleanup` phase and transitions `task_cleanup → sdk_connecting` only after the scrub succeeds (spec/06:147-154). Consistent with that existing precedent (preferring an existing spec pattern over a parallel one), the proposal now keeps the whole-pod scrub in a phase distinct from `sdk_connecting`: a `recycling` claim projects to `claimed` during the scrub on both pool kinds, and to `sdk_connecting` only after a successful `ReportPodScrub` when the SDK re-warm begins. The gateway records a `rewarmStartedAt` binding-state-transition stamp on `SandboxClaim.status` at that point, the WarmPoolController arms the re-warm watchdog clock from it, and the scrub is bounded instead by the gateway-side missing-report timeout (`cleanupTimeoutSeconds` plus a grace). The `rewarmStartedAt` stamp was added to the claim status (Sections 3.2 and 5) and the two `SandboxClaim` CRD manifests (Section 13); Sections 3.1 (line 84), 3.3 (lines 106 and 115), 3.4 (line 121), the spec/06 §6.1 directive and recycle-edge enumeration and label-table directive (line 159), the Section 9 projection test (line 219), and Section 13 were updated so the re-warm-start anchor and the scrub-in-`claimed` projection are stated identically everywhere.
- **§5.2 slot-assignment atomicity and rehydration paragraphs not re-keyed off `maxConcurrent` / concurrent-workspace:** the spec/05 §5.2 directive enumerated the surfaces it restages but omitted the §5.2 titled slot-counter paragraphs "Slot cleanup" (spec/05:515), "Concurrent-workspace slot assignment atomicity" (spec/05:519), "Post-recovery rehydration atomicity" (spec/05:521), and "Whole-pod replacement trigger" (spec/05:531). After application these would still name the removed `concurrent-workspace` mode and cap the intra-pod Redis slot counter at `maxConcurrent` (the field the proposal makes service-mode-only), so the session-mode concurrency gate the proposal relies on would be capped by the wrong bound, a granularity mismatch in the concurrency gate, and the spec/12:189 key-table row the proposal already re-keys (line 165) links to these exact paragraphs. The spec/05 §5.2 directive (line 158) now restages spec/05:515, :519, :521, and :531, dropping the removed mode name and renaming the slot-counter cap and the `ceil(maxConcurrent / 2)` threshold from `maxConcurrent` to `sessionPolicy.maxConcurrentSessions` for the session-mode concurrency path. `pkg/gateway/slotcounter` (with its `Counter.Reserve(ctx, podID, maxConcurrent int32)` cap at slotcounter.go:93,244) and the cap value its `pkg/gateway/podclaim`/`pkg/gateway/podsession` callers pass were added to the Section 13 code list, so the gate reads the session-mode bound.
- **Mode collapse dropped the categorical concurrent-mode cross-tenant-reuse prohibition:** relocating `allowCrossTenantReuse` under `sessionPolicy.recycle` and carrying forward only "the microvm gate" plus the T4 prohibition (line 82) made a microvm pool with `maxConcurrentSessions > 1` and `recycle.allowCrossTenantReuse: true` admissible, the configuration the current spec/05:498 and the implementation categorically reject because simultaneous process-level cotenancy has no isolation boundary regardless of microvm. `acknowledgeProcessLevelIsolation` is a same-tenant acknowledgment and does not close the gap. The proposal now carries forward the categorical concurrent-mode rejection as a derivation distinct from the microvm gate: `recycle.allowCrossTenantReuse: true` is rejected whenever `maxConcurrentSessions > 1` regardless of isolation profile or scrub profile, while the microvm gate still permits cross-tenant reuse on the sequential-reuse path (`maxConcurrentSessions: 1`). The rule was added to the Section 2 safe-defaults decision (line 38), the Section 3.1 derivations (line 82) and the `recycle` YAML comment (line 62), the §5.2 directive re-keying spec/05:498 (line 158), the Section 5 validation re-key list and the poolstore clause (line 147), the Section 9 pool-admission test (line 217), and the Section 13 code list naming the re-keyed `ValidateConcurrentConfig` (poolstore.go:503-506) and `decideConcurrentWorkspace` (validator.go:579-581) enforcement points.
- **spec/15:643 internal-only-states sentence omitted from the spec/15 directive after the coarse-enum reduction:** the §15.1 sentence at spec/15:643 lists `receiving_uploads`, `running_setup`, and `resuming` (fine session phases the proposal removes from the enum) as tracked on `Sandbox.status.phase` "for controller reconciliation and operational monitoring only," a kubectl-visible operational-monitoring surface that would make a false storage attribution after the coarse-enum reduction moves those phases to the Postgres session model. The generic line-114 carry-forward statement is design-overview prose rather than an entry in the Section 6 edit table, and the prior 0002 staged this exact sentence as its §7.13. The spec/15 directive (line 169) now restages spec/15:643 to list only the coarse occupancy phases on `Sandbox.status.phase` and move `receiving_uploads`, `running_setup`, and `resuming` to the description as Postgres session-model states, mirroring the prior 0002's §7.13 replacement.
- **§16.5 `CheckpointStorageHigh` alert and §16.6/§16.1 prose named removed concurrent-workspace mode and the renamed `maxConcurrent` knob, omitted from the spec/16 directive:** the spec/16 directive was scoped to metric renames, coarse-enum examples, and the unchanged claim-queue rows, and did not list the §16.5 alert table. After application the `CheckpointStorageHigh` description (spec/16:468) would still say "Concurrent-workspace pools can multiply per-pod checkpoint footprint by `maxConcurrent × 2`" with a cross-reference to the §12.5 guidance the proposal rewrites to `maxConcurrentSessions`, leaving spec/16 inconsistent with the rewritten §12.5; the alert prose is hand-authored in spec/16 (no generation header) and is absent from `pkg/alerting/rules` (rules.go:893 carries no `maxConcurrent` text), so the alert single-source regeneration does not reach it. The spec/16 directive (line 170) now re-keys spec/16:468 to `maxConcurrentSessions > 1` and `maxConcurrentSessions × 2`, the §16.6 label-mapping prose at spec/16:288 ("Task-mode scrub/retirement metrics … task reuse histograms") to the session/service vocabulary, and the §16.1 `lenny_slot_failure_total` (spec/16:12) and `lenny_slot_pod_replacement_total` (spec/16:13) row descriptions from "Concurrent-workspace slot" to the `maxConcurrentSessions > 1` session-mode slot.
- **JSONL adapter schema `slotId` description named removed concurrent-workspace mode, left out of the schema edit scope:** the `schemas/lenny-adapter-jsonl.schema.json` edit scope covered only the between-task frame removal, but the schema's `slotId` field description (schemas/lenny-adapter-jsonl.schema.json:63) reads "Present only on pods in concurrent-workspace mode," the authored conformance source that the §15.4.1 wire description mirrors, which the proposal re-keys to `maxConcurrentSessions > 1`. Section 3.5 (line 128) and the Section 5 schemas bullet (line 149) now re-key the schema's `slotId` field description to `maxConcurrentSessions > 1`, matching the spec/15:1818/1846/1879/1903 re-keys (the other `slotId` fields at lines 142, 164, and 193 carry no mode-named description and are unchanged).
- **§15.7 SDK "Graceful shutdown" bullet named removed `task_complete`/`task_ready` frames and task mode, absent from the spec/15 directive:** the §15.7 Runtime Author SDKs "Graceful shutdown" bullet (spec/15:2519) instructs SDK authors to implement "`task_complete` / `task_ready` handling on the lifecycle channel for Full-level task-mode runtimes," two frames and a mode the proposal deletes, and §15.7 is distinct from every §15 surface the directive enumerated. The spec/15 directive (line 169) now re-keys spec/15:2519 to drop the `task_complete` / `task_ready` handling and the "Full-level task-mode runtimes" clause, retaining only the `terminate` / `shutdown` deadline contract, and re-keys the §15.7 SDK `WorkspacePlan` field comment "(or the per-slot path in concurrent-workspace mode)" (spec/15:2586) to `maxConcurrentSessions > 1`.

### Pass 9 (2026-06-11, automated)

- **Gateway OpenAPI document absent from every edit list, leaving the served contract on the removed `executionMode`/`concurrencyStyle` enums and omitting `conversationContinuity`:** the proposal renames the mode enum to `session | service`, removes `concurrencyStyle`, and adds the mandatory `conversationContinuity` field to `sessionIsolationLevel`, but enumerated only the CRD manifests, the JSONL and lifecycle schemas, the proto, and the three client SDK type files; `pkg/gateway/openapi/openapi.json` was named nowhere. That file is the hand-authored `//go:embed`-ed document the gateway serves at `/openapi.json` and `/openapi.yaml` (openapi.go:6, :30-31) and is not produced by `make generate` (Makefile:53). spec/15:1386 makes it the single authoritative schema the MCP `create_session` tool schema is generated from, and spec/15:2492 generates the SDKs from it, so editing only the downstream SDK structs would leave the served contract advertising `["session","task","concurrent"]` at openapi.json:180, :270, and :540, the removed `concurrencyStyle` enum at openapi.json:541, and a `sessionIsolationLevel` schema with no `conversationContinuity` (openapi.json:176-186), the same "new client-facing contract field unreachable by clients" failure Pass 5 closed one layer down, reopened upstream. The Section 5 schemas bullet (line 149), Section 13, and the Section 6 spec/15 row (citing the §15.1 OpenAPI endpoint prose at spec/15:589) now add `pkg/gateway/openapi/openapi.json`: the three `executionMode` enums change to `["session","service"]`, the `concurrencyStyle` enum property is removed, and `conversationContinuity` (enum `["platform","none"]`) is added to the `sessionIsolationLevel` schema, so the authoritative document the MCP tool schema and SDKs derive from matches the renamed modes and the new contract field.
- **T4 cross-tenant prohibition and the microvm cross-tenant gate dropped with task mode, asserted to "carry over unchanged" while their only enforcement branches are removed:** Section 3.1 line 82 asserted "The T4 cross-tenant prohibition carries over unchanged" and that "the microvm gate on `allowCrossTenantReuse` carries over," but both controls key off the removed `taskPolicy.allowCrossTenantReuse` field and the removed `task` mode. The microvm gate (spec/05:394) is enforced in `ValidateTaskPolicy` (poolstore.go:623, reached only when `executionMode` is `task` at poolstore.go:605) and in `decideTaskMode` (validator.go:534, invoked only for `case "task"` at validator.go:431); the T4 prohibition (spec/05:396, including the gateway session-assignment enforcement that names "task-mode pod") is enforced in `decideTaskMode` (validator.go:545) and, separately and without an execution-mode gate, in `ValidateCrossTenantReuseTier` (poolstore.go:667; Pass 9 re-keyed only the `decideTaskMode` branch and the parallel poolstore enforcer was added in Pass 10). The §5.2 directive enumerated re-keys for spec/05:498 and the others but neither spec/05:394 nor :396, and the code lists named `decideConcurrentWorkspace` and `ValidateConcurrentConfig` but neither `decideTaskMode` nor `ValidateTaskPolicy`, so after application both fail-closed cross-tenant controls would be silent no-ops on the sequential-reuse successor (`maxConcurrentSessions: 1`, `recycle.enabled: true`, `recycle.allowCrossTenantReuse: true`), exactly the cross-tenant microvm reuse path the T4 prohibition must block. Section 3.1 line 82 now states each control is re-keyed off `taskPolicy.allowCrossTenantReuse` and the `task` mode onto `sessionPolicy.recycle.allowCrossTenantReuse` rather than carrying over with no edit; the §5.2 directive (line 158) re-keys spec/05:394 (the microvm gate, requiring `isolationProfile: microvm` for `recycle.allowCrossTenantReuse: true` on the sequential-reuse path) and spec/05:396 (the T4 prohibition and its assignment-time enforcement, the "task-mode pod" reference re-keyed to a sequential-reuse pod); the Section 5 validation re-key list (line 147) and the Section 13 code lists name re-keying the microvm gate (validator.go:534, poolstore.go:623) and the T4 prohibition (validator.go:545) onto the `sessionPolicy.recycle` path; and the Section 9 pool-admission test (line 217) exercises the non-microvm sequential-reuse rejection and the T4 rejection.
- **Staged `maxPodUptimeSeconds: 14400` default contradicted the relocated knob:** the staged YAML labeled `maxPodUptimeSeconds: 14400` as "existing knob, relocated," but the existing knob's documented example is `86400` everywhere (spec/05:161, :411, :470, docs/operator-guide/configuration.md:308), and the knob is optional with no enforced default (spec/05:452; the CRD type is a nullable `*int64` with omitempty at sandboxtemplate_types.go:81; poolstore validates only `< 0` at poolstore.go:639-640). The value 14400 is the unrelated `maxSessionAgeSeconds` chart default. The YAML now stages `86400` to match the relocated knob's example and notes it is optional, and the "default 14400s" assertions in the Section 3.3 watchdog prose (line 115) and the Pass-7 resolution (line 301) are reduced to "up to `recycle.maxPodUptimeSeconds`," so no surface attributes an enforced 14400 default to a relocated optional knob, the same defect class the Pass-1 `maxSessionRetries: 2`-vs-1 fix corrected.
- **`lenny_stateless_*` demand-signal PromQL at spec/05:573 not renamed to `lenny_service_*`:** the proposal renames the `lenny_stateless_*` family to `lenny_service_*` at every emitter and consumer, but spec/05:573 is the sole spec home of the metric names `lenny_stateless_requests_total` and `lenny_stateless_concurrent_active` (`grep -rn lenny_stateless spec/ docs/` returns only spec/05:573; the §16.1 catalog carries no such row), and the §5.2 directive named spec/05:573 only to re-key the `maxConcurrent` field. After application the gateway would emit `lenny_service_*` while spec/05:573 still documented the demand-signal source as `rate(lenny_stateless_requests_total[5m])` and `max_over_time(lenny_stateless_concurrent_active[5m])`. The §5.2 directive (line 158) now renames both metric names in the spec/05:573 PromQL prose alongside the `maxConcurrent` re-key, so the documented demand-signal source matches the emitted metric.
- **spec/05:446 `onCleanupFailure` behaviors block left naming the renamed scrub metrics and the removed `task_cleanup` transition:** the `onCleanupFailure: warn` paragraph at spec/05:446 is the sole spec home of the aggregate counter `lenny_task_scrub_failure_total` (taskdriver.go:61 confirms it is named only in §5.2, outside the §16.1 inventory) and also names `lenny_task_pod_scrub_failure_count`, the `task_cleanup → sdk_connecting [scrub_warning]` cross-reference, and the "preConnect re-warm on scrub_warning" note. The proposal renames both metrics and removes the `task_cleanup` state, but the §5.2 directive never named spec/05:446 and the metric is absent from the §16.1 inventory, so after application spec/05:446 would document the aggregate counter as `lenny_task_scrub_failure_total` (no longer emitted) and reference a removed transition, the same titled-prose defect class the Pass-6 fix corrected for its spec/06:183 sibling. The §5.2 directive (line 158) now restages spec/05:446 (renamed to `onScrubFailure`): rename `lenny_task_scrub_failure_total` to `lenny_pod_scrub_failure_total` and `lenny_task_pod_scrub_failure_count` to `lenny_pod_scrub_failure_count`, re-key the `task_cleanup → sdk_connecting [scrub_warning]` cross-reference and the "preConnect re-warm on scrub_warning" note to the recycle re-warm edge `claimed → sdk_connecting`, and restate the task-mode "accepts the next task" language to the session/recycle model.

### Pass 10 (2026-06-11, automated)

- **T4 cross-tenant prohibition's mode-independent poolstore enforcer (`ValidateCrossTenantReuseTier`) and its gateway pool-admission call sites omitted from the re-key:** Pass 9 re-keyed the spec/05:396 T4 prohibition onto `sessionPolicy.recycle.allowCrossTenantReuse` but named only the `decideTaskMode` branch (validator.go:545), which is reached today only for `executionMode: task`. The prohibition is also enforced by a distinct, mode-independent function the proposal never named: `ValidateCrossTenantReuseTier` (poolstore.go:667), which fires for any pool whose `p.AllowCrossTenantReuse && runtimeTier.IsT4()` with no execution-mode gate (poolstore.go:595 documents it as the poolstore enforcer of the §5.2 line 396 prohibition) and runs unconditionally at three gateway pool-admission call sites (`pkg/gateway/admin/pools.go:606`, `pkg/gateway/admin/pools.go:1447`, `pkg/gateway/admin/bootstrap_resources.go:62`). It reads the top-level `Pool.AllowCrossTenantReuse` field (poolstore.go:84) the proposal relocates to `sessionPolicy.recycle.allowCrossTenantReuse`, and `pkg/gateway/admin` appeared in no files-touched list. Re-keying only `decideTaskMode` would leave this fail-closed gate reading a deleted field, silently disabling it at the gateway pool-admission layer on the surviving sequential-reuse path (`maxConcurrentSessions: 1`, `recycle.enabled: true`, `recycle.allowCrossTenantReuse: true`) and dropping one of the two independent enforcement layers spec/05:389 requires, the same defect class Pass 9 flagged for `decideTaskMode` left unfixed for the parallel poolstore enforcer. Section 3.1 line 82 now states the T4 prohibition is enforced at both `decideTaskMode` (validator.go:545) and the mode-independent `ValidateCrossTenantReuseTier` (poolstore.go:667), both re-keyed onto the relocated field; the Section 5 validation re-key list (line 147) names `ValidateCrossTenantReuseTier` and its three unconditional call sites; Section 13 adds the `ValidateCrossTenantReuseTier` re-key under `pkg/gateway/poolstore` and adds `pkg/gateway/admin` with the three call sites; and the Pass-9 resolution (line 316) is corrected to record that its `decideTaskMode`-only enforcement claim was incomplete and that the parallel poolstore enforcer was added here.
- **§5.2 "Checkpoint granularity" and "Resource contention" slot-cleanup bullets retained the removed `concurrent-workspace` mode name and the service-mode-only `maxConcurrent` cap:** the Pass-8 §5.2 slot-counter restage enumerated the "Slot cleanup" (spec/05:515), "Concurrent-workspace slot assignment atomicity" (spec/05:519), "Post-recovery rehydration atomicity" (spec/05:521), and "Whole-pod replacement trigger" (spec/05:531) bullets but skipped the two intervening titled bullets in the same §5.2 slot-cleanup list (spanning spec/05:512-531): "Checkpoint granularity" (spec/05:516), which names "concurrent-workspace mode"/"concurrent-workspace pod" three times and keys `maxConcurrent` to the session-concurrency slot path (`maxConcurrent × max_tiered_checkpoint_cap`, `maxConcurrent: 8`, `maxConcurrent > 1`, and "`maxConcurrent` simultaneous uploads"), and "Resource contention" (spec/05:517), which keys the `maxConcurrent` cap to that same slot path (its `mode_factor` reference is the session-mode scaling factor and stays named). After application these would leave §5.2 internally inconsistent: every other §5.2/§6.2/§12.5/§16 surface drops "concurrent-workspace mode" and renames the slot-path cap to `maxConcurrentSessions`, while :516/:517 continued to name the removed mode and the service-mode-only bound, and the §12.5 directive already re-keys spec/12:313 (the per-slot-checkpoint bullet that cross-links to the spec/05:516 checkpoint-granularity paragraph) without re-keying the spec/05 paragraph it points at. The §5.2 directive (line 158) now adds spec/05:516 and :517 to the slot-counter restage list, dropping the "concurrent-workspace mode"/"concurrent-workspace pod" naming and renaming the slot-path `maxConcurrent` cap (in the `maxConcurrent × max_tiered_checkpoint_cap` budget formula, the `maxConcurrent: 8` example, the `maxConcurrent > 1` threshold, the eviction-ordering prose, and the resource-contention "set `maxConcurrent` conservatively" guidance) to `maxConcurrentSessions` in lockstep with the spec/12:313 re-key, while the resource-contention `mode_factor` stays the session-mode scaling-factor derivation.

### Pass 11 (2026-06-11, automated)

- **`runtime_definitions` `execution_mode` CHECK constraint not re-keyed, rejecting the new `service` mode:** the migration edit scope was scoped to "`agent_pod_state` columns" with "The sessions table is unchanged," but the `runtime_definitions` table carries a live, enforced `CHECK (execution_mode IN ('session', 'task', 'concurrent'))` constraint (`migrations/0001_initial_schema.up.sql:56-57`) that no later migration alters (the current head is 0166), and the gateway runtime store writes `execution_mode` into `runtime_definitions` with the literal mode string (`pkg/gateway/runtimestore/pgstore/pgstore.go:239` INSERT, `:311` UPSERT). After the enum rename to `session | service` the database would reject a `service`-mode runtime definition with a constraint violation while still permitting the removed `task` and `concurrent` values, an absent migration edit site. Section 5 (line 148) and Section 13 (the migrations entry) now add a new append-only migration after 0166 that drops `runtime_definitions_execution_mode_check` and re-adds it as `CHECK (execution_mode IN ('session', 'service'))`, re-keys the stale unconstrained-column mode comments at `migrations/0033_sandbox_warm_pools.up.sql:15` and `migrations/0084_sessions_isolation_level.up.sql:18`, and retires the `concurrency_style` column from `migrations/0040_warm_pool_concurrency.up.sql:13` once `concurrencyStyle` is removed.
- **`openapi.json` `taskPolicy` object (with `maxTasksPerPod`, `maxTaskRetries`, and a `microvmScrubMode` enum) left stale, absent from the openapi.json edit directive:** the Pass-9 openapi.json directive caught the three `executionMode` enums, the `concurrencyStyle` property, and the added `conversationContinuity` field, but the same hand-authored `Runtime` schema carries a full `taskPolicy` object (`pkg/gateway/openapi/openapi.json:380-394`) with `maxTasksPerPod` (:391), `maxTaskRetries` (:393), and a `microvmScrubMode` enum `["restart","in-place"]` (:385), the structure the proposal removes elsewhere by replacing `taskPolicy` with `sessionPolicy`, dropping `maxTasksPerPod`, and renaming `microvmScrubMode` to `scrubProfile`. Because spec/15:1386 makes this document the single authoritative source the MCP `create_session` tool schema and the SDKs derive from, and it is not produced by `make generate`, leaving the object stale would emit a `taskPolicy` type and a `microvmScrubMode` field for a removed structure, the identical edit-site-miss class Pass 9 raised for the enums in this same file. Section 5 (line 149) and Section 13 (the openapi.json entry) now extend the directive to replace the `taskPolicy` object at openapi.json:380-394 with the `sessionPolicy` structure: drop `maxTasksPerPod`, rename `maxTaskRetries` to `maxSessionRetries`, rename `microvmScrubMode` to `scrubProfile` with the enum `["standard","vm-restart","in-place"]` under the `recycle` sub-object, rename `onCleanupFailure` to `onScrubFailure`, and relocate the surviving knobs onto `sessionPolicy`, so the served authoritative document matches the post-collapse spec, CRD types, CRD manifests, and poolstore mirror.
- **spec/05:502 "Tenant isolation (concurrent-stateless)" paragraph omitted from the §5.2 edit directive:** the §5.2 directive re-keyed the parallel cross-tenant paragraphs at spec/05:498, :394, and :396 but never named spec/05:502, a distinct normative service-mode tenant-isolation paragraph that states the two-layer tenant-pinning mechanism for concurrent-stateless pods and the categorical control "Concurrent-stateless pools are not permitted in multi-tenant deployments where `allowCrossTenantReuse` would be needed; the pool controller rejects `concurrencyStyle: stateless` pools that set `allowCrossTenantReuse: true` at validation time." After application this paragraph would name a removed mode (`concurrencyStyle: stateless`), a removed field (the top-level `allowCrossTenantReuse` relocated to session-mode-only `sessionPolicy.recycle`), and a removed validation rule (the current `ValidateConcurrentConfig` enforcer at `pkg/gateway/poolstore/poolstore.go:503-506` covers stateless because `concurrencyStyle: stateless` is `executionMode: concurrent`, and the Section 5 re-key to `maxConcurrentSessions > 1` drops service mode from that enforcer's scope), leaving a tenant-isolation control internally inconsistent, the same edit-site-miss class the proposal fixed for the sibling paragraphs. The §5.2 directive (line 158) now rewrites spec/05:502 as the service-mode tenant-isolation contract: it drops the `concurrencyStyle: stateless` mode name, restates the two-layer tenant-affinity pinning as the service-mode pinning contract, and replaces the cross-tenant-prohibition sentence with the structural posture that service mode has no `allowCrossTenantReuse` field, so cross-tenant reuse is unrepresentable and structurally prohibited rather than rejected on a removed field. Section 3.6 (line 134) states the same service-mode structural prohibition, and the existing Section 7 multi-tenancy/isolation doc row already covers tenant pinning and cross-tenant reuse as derivations.

### Pass 12 (2026-06-11, automated)

- **`maxIdleTimeSeconds` rename leaving three §6.2 paragraphs (spec/06:242, :292, :294) on the removed knob, re-verified:** the spec/06 §6.2 idle-clock directive (line 159) was confirmed to widen past the staged spec/06:273-290 range and restage the three titled prose paragraphs that name the knob outside it: "Interaction with other timers during podless suspension" (spec/06:242, "`maxIdleTimeSeconds` remains paused"), "`resume_pending` wall-clock cap" (spec/06:292, "Although `maxSessionAge` and `maxIdleTimeSeconds` are both paused during `resume_pending`"), and "`suspended → expired` trigger mechanism" (spec/06:294, "Both `maxSessionAge` and `maxIdleTimeSeconds` are paused during `suspended`"); `grep -n maxIdleTimeSeconds spec/06_warm-pod-model.md` returns 242, 273, 278, 282, 290, 292, and 294, so the directive's restage set (273-290 plus 242, 292, 294) now covers every occurrence. The reconciliation against the Section 3.1 pause table holds against the spec: the pause-during-`suspended` claims at :242 and :294 carry forward because the new clock is also paused during `suspended`, and :292's "both paused during `resume_pending`" claim stays accurate because the new clock is paused during `resume_pending` and `resuming` (spec/06:285 records the existing idle-timer pause during `resume_pending`, and the existing idle-timer table at spec/06:285-287 the directive replaces also pauses during `awaiting_client_action`, which the new clock deliberately changes to run). The recovery-state pause is stated identically in the Section 2 decision bullet (line 41), the Section 3.1 clock description (line 86), and the Section 12 `maxClientIdleSeconds` resolved decision (line 338), so no drift remains. The stale "(line 302)" cross-reference in the Pass 7 resolution, which pointed inside the Pass 7 block rather than at the Section 12 decision after Passes 8-11 shifted Section 12 down, is corrected to name the Section 12 `maxClientIdleSeconds` resolved decision by section rather than by a drifting line number, matching the Section-name convention the other pass entries use for Section 12 (for example line 265).
- **§12.6 `agent_pod_state.execution_mode` SQL comment and §12.5 concurrent-workspace checkpoint bullets on removed modes, re-verified:** the spec/12 §12.4/§12.5/§12.6 directive (line 166) was confirmed to re-key the `execution_mode` column comment at spec/12:465 from `-- session, task, concurrent_workspace` to `-- session, service` (verified against the `CREATE TABLE agent_pod_state` block, the comment sits at spec/12:465) and to restate the parallel §12.5 concurrent-workspace checkpoint surfaces that the prior "task/stateless" qualifier left unnamed: the per-slot exception at spec/12:313 ("In concurrent-workspace mode … checkpoints are per-slot"), the "Concurrent-workspace pools" retained-checkpoint bullet at spec/12:326 ("up to `maxConcurrent × 2` retained checkpoints per pod"), and the GC-guard restatement at spec/12:336 ("per-slot in concurrent-workspace mode"), each re-keyed to `sessionPolicy.maxConcurrentSessions > 1` with `maxConcurrent` renamed to `maxConcurrentSessions` in the `maxConcurrent × 2` and `maxConcurrent: 8` examples, so no spec/12 surface names a removed mode after application. The spec/05:516 "Checkpoint granularity" bullet that spec/12:313 cross-links to is independently re-keyed in the §5.2 directive (line 158, Pass 10), so the spec/05 and spec/12 checkpoint surfaces change in lockstep.

### Pass 13 (2026-06-11, automated)

- **Retirement `reason` label value `task_count_limit` named the removed `maxTasksPerPod` trigger and was absent from the spec/16 directive:** the spec/16 directive (line 170) re-keyed the §16.6 label-mapping prose at spec/16:288, the §16.1 `lenny_slot_failure_total`/`lenny_slot_pod_replacement_total` row descriptions, and the §16.5 alert, but left the retirement-trigger `reason` value vocabulary on the renamed `lenny_pod_retirement_total` row at spec/16:11 and in the §16.6 `error.type`-versus-`reason` normative paragraph at spec/16:299 naming `task_count_limit`, the retirement trigger bounded by `maxTasksPerPod`, the knob this proposal removes (the successor triggers are `recycle.maxSessionsPerPod`, `maxPodUptimeSeconds`, and `maxScrubFailures`). After application both spec/16 surfaces would advertise `reason: task_count_limit` for a trigger the spec no longer defines, the same removed-knob vocabulary defect class the Pass-8 fix caught at spec/16:288 and at spec/16:12/:13 that the generic metric-name-only "follow the renames" language does not reach. The spec/16 directive (line 170) now re-keys `task_count_limit` to `session_count_limit` (matching the renamed bound `recycle.maxSessionsPerPod`) at spec/16:11 and spec/16:299 while keeping `uptime_limit` and `scrub_failure_limit`, and names the emitter whose value the vocabulary must match: the `RetireReason` constant `ReasonMaxTasksReached` (value `max_tasks_reached`, taskcleanup.go:126-128), re-keyed onto the `recycle.maxSessionsPerPod` retirement bound in the Section 13 `pkg/sandbox/taskcleanup` entry in the same change, so the retirement counter's `reason` vocabulary and its emitter agree and no spec/16 surface names the removed `maxTasksPerPod` trigger.
- **Runtime CRD `executionMode` enum marker and its two generated manifests omitted from the mode rename:** Section 5 line 147 and Pass 4 (line 276) asserted exactly two `executionMode` `+kubebuilder:validation:Enum` markers (sandbox_types.go:45 and sandboxtemplate_types.go:175) and four generated manifests, but a third marker exists on the `Runtime` CRD type (runtime_types.go:37 on the `ExecutionMode` field at runtime_types.go:39), which spec/05:536 names as the spec-declared primary v1 home of `executionMode`. The Runtime CRD is a live, reconciled resource (`pkg/controller/runtime/controller.go`), and its two generated manifests carry `enum: [session, task, concurrent]` at charts/lenny/crds/lenny.dev_runtimes.yaml:289-292 and pkg/embedded/crds/lenny.dev_runtimes.yaml:289-292, enforced at API-server admission. After application the runtimestore typed enum, the §5.2 mode set, and the Pass-11 `runtime_definitions` CHECK constraint would all read `session | service` while the Runtime CRD enum still rejected a `service`-mode Runtime and admitted the removed `task` and `concurrent` values, the same edit-site-miss class the proposal caught for the Postgres constraint (Pass 11), the SandboxClaim/SandboxTemplate/Sandbox manifests (Passes 4/5/6), and the `executionMode`/`concurrencyStyle` enums in the hand-authored openapi.json (Pass 9), reopened on the spec-declared primary home of the field. Section 5 line 147 now names the `Runtime` field and marker, corrects the count to three markers and six manifests, and adds `charts/lenny/crds/lenny.dev_runtimes.yaml` and `pkg/embedded/crds/lenny.dev_runtimes.yaml` to the `make generate` regeneration list; Section 13 (line 356) adds the `Runtime` CRD type and its two manifests and names runtime_types.go:37 in the `pkg/apis/lenny/v1alpha1` marker-edit list; and the Pass-4 resolution (line 276) is corrected to record that its "two markers / four manifests" enumeration understated the surface by the Runtime marker and its two manifests.

### Pass 14 (2026-06-11, automated)

- **§16.1 reuse-histogram inventory row (spec/16:124) named the removed `task` mode and the `maxTasksPerPod` knob, absent from the spec/16 directive:** the spec/16 directive (line 170) re-keyed the §16.5 `CheckpointStorageHigh` alert (spec/16:468), the §16.6 label-mapping "Used on" cell at spec/16:288 (including "task reuse histograms"), the `lenny_slot_failure_total`/`lenny_slot_pod_replacement_total` rows (spec/16:12, :13), and the `reason`-label retirement vocabulary at spec/16:11 and :299, but never named the §16.1 inventory row at spec/16:124 that defines the reuse histogram itself: "Task-mode pod reuse count (`lenny_task_reuse_count` … number of tasks executed on a single pod in task mode; used to track recycling efficiency and enforce `maxTasksPerPod` retirement … to derive `mode_factor` for task-mode pools)." `grep -rn lenny_task_reuse_count spec/` confirms spec/16:124 is the metric's only §16.1 home, and the §16.6 re-key at spec/16:288 changes a separate cell (the label-mapping "Used on" column) rather than the inventory row's own definition. The row is hand-authored §16.1 prose (catalog.go's header states "the spec table prose remains the source of truth"), so the metric-name rename to `lenny_pod_session_reuse_count` recorded in Sections 2 and 4 does not re-key the row's description, and after application spec/16:124 would still describe the metric as counting "tasks … in task mode" and enforcing `maxTasksPerPod` retirement, naming a mode and a knob the rest of the applied spec removes, the same removed-knob/removed-mode vocabulary defect class the Pass-8 (spec/16:288, :12, :13) and Pass-13 (spec/16:11, :299 `task_count_limit`) fixes establish that the generic "follow the renames" language does not reach. The spec/16 directive (line 170) now renames spec/16:124 to `lenny_pod_session_reuse_count` and re-keys its description to the session/recycle vocabulary (sessions served per pod under `recycle.enabled`, bounded by `recycle.maxSessionsPerPod`, deriving `mode_factor` for recycling session-mode pools), re-keys the matching catalog description string at `pkg/observability/metrics/catalog.go:189` ("Tasks executed on a single pod in task mode") in the same change (covered by the Section 13 `pkg/observability/metrics` entry), and re-keys the sibling §16.1 mode-named leading descriptors "Task-mode per-pod scrub failure count" (spec/16:10) and "Task-mode pod retirement" (spec/16:11) that the row-11 `reason`-only re-key does not reach, so no §16.1 surface names the removed `task` mode or `maxTasksPerPod` after application. Pass 15 corrected this entry's scoping: spec/16:124 is the metric's only §16.1 home, but `lenny_task_reuse_count` has two additional spec homes outside §16.1 in the §5.2 scaling-factor caveats at spec/05:549 and :571, which the spec/05 §5.2 directive (line 158, Pass 15) now also renames, so the §16.1-only scoping understated the metric's spec footprint.

### Pass 15 (2026-06-11, automated)

- **§5.2 "Task-mode pod retirement policy" paragraph (spec/05:449-455) left the renamed retirement counter and the removed `task_count_limit` trigger absent from every edit directive:** spec/05:455 is a second spec home of the retirement counter `lenny_task_pod_retirement_total` and its `reason: task_count_limit` value (verified: "When a pod is retired, the gateway increments `lenny_task_pod_retirement_total` (labeled by `reason`: `task_count_limit`, `uptime_limit`, `scrub_failure_limit`) …", spec/05:455), separate from the §16.1 inventory row at spec/16:11 the Pass-13 fix re-keyed. The §5.2 directive (line 158) restated retirement generically and named only the sibling scrub-failure counter's spec/05:446 home, so after application spec/05:455 would still read `lenny_task_pod_retirement_total` (the metric this proposal renamed to `lenny_pod_retirement_total` in Sections 2 and 4) labeled by `task_count_limit` (the value Pass 13 re-keyed to `session_count_limit`, which names the removed `maxTasksPerPod` trigger), leaving §5.2 inconsistent with spec/16:11, spec/16:299, and `pkg/observability/metrics/catalog.go:77`, the same missed-edit-site class the proposal closed at spec/05:446 (Pass 9) and spec/16:11/:299 (Pass 13). The §5.2 directive (line 158) now restages spec/05:449-455: rename `lenny_task_pod_retirement_total` to `lenny_pod_retirement_total`, re-key the `reason` value `task_count_limit` to `session_count_limit` at spec/05:455 in lockstep with the spec/16:11/:299 re-key and the catalog rename, and rename the "Task count limit" bullet's `maxTasksPerPod` trigger (spec/05:451) to `recycle.maxSessionsPerPod` with the `maxPodUptimeSeconds`/`maxScrubFailures` bullets re-keyed onto the `recycle` block, so no §5.2 retirement surface names the renamed metric or the removed trigger after application.
- **§5.2 scaling-factor caveat lines naming `lenny_task_reuse_count`, the `task`/`concurrent` modes, and `maxTasksPerPod` (spec/05:543, :549, :564, :571) absent from every edit directive:** `lenny_task_reuse_count` (renamed to `lenny_pod_session_reuse_count` in Sections 2 and 4) has three spec homes rather than one: spec/05:549, spec/05:571, and spec/16:124 (verified: `grep -rn lenny_task_reuse_count spec/` returns spec/05:549, spec/05:571, and spec/16:124). The §5.2 directive enumerated the service-mode scaling rows at spec/05:550, :565, and :573 but never the task-mode rows at spec/05:543, :549, :564, and :571, so after application spec/05:549 and :571 would still emit `lenny_task_reuse_count` (a metric the gateway no longer emits after the rename) and still name the removed `task` mode and the removed `maxTasksPerPod` knob, while spec/16:124, the catalog, and the demand-signal metrics at :573 were re-keyed, the identical edit-site asymmetry the proposal caught at spec/05:573 (Pass 9) and spec/16:124 (Pass 14). The §5.2 directive (line 158) now restages spec/05:543, :549, :564, and :571: rename `lenny_task_reuse_count` to `lenny_pod_session_reuse_count`, drop the `task`/`concurrent` mode names and the `maxTasksPerPod` knob, and rewrite the `mode_factor`/`burst_mode_factor` caveats in `sessionPolicy` terms (session-mode `mode_factor` = sessions per pod lifetime bounded by `recycle.maxSessionsPerPod` and measured by the reuse histogram p50, `burst_mode_factor = maxConcurrentSessions`; service-mode `mode_factor = maxConcurrent`), consistent with Section 3.1 line 82, the spec/16:124 Pass-14 re-key, and the spec/05:573 Pass-9 re-key. The Pass-14 resolution is corrected to record that spec/16:124 is the metric's only §16.1 home but not its only spec home.
- **§16.1 metric rename left the in-code spec-mirror transcription `spec161Metrics` (`catalog_test.go`) out of every edit list, breaking two tier-1 consistency gates:** `catalog_test.go` holds a third, independent transcription of the §16.1 inventory (`spec161Metrics` at catalog_test.go:15-151, naming `lenny_task_pod_scrub_failure_count` and `lenny_task_pod_retirement_total` at :17 and `lenny_task_reuse_count` at :68), the same mirror role as `catalog.go` and `docs/reference/metrics.md`, both of which the proposal names as mandatory edit sites. Two tier-1 unit tests check the catalog against it bidirectionally — `TestMetricCatalogIsCompleteAgainstSpec161` (catalog_test.go:183) and `TestMetricCatalogHasNoUnspecifiedMetrics` (catalog_test.go:197) — so renaming the three metrics in `catalog.go` without updating `spec161Metrics` fails both (the file carries no build tag and runs in the standard tier-0/1 sweep), and the applied implementation does not build green. The Section 13 `pkg/observability/metrics` entry now names `catalog_test.go`'s `spec161Metrics` transcription alongside the `catalog.go` rename, updating the three renamed §16.1 entries, and adds the reserved-pod gauge and idle-termination counter rows from Section 4 to both `spec161Metrics` and the §16.1 inventory directive when those series are registered in `catalog.go`, so both consistency tests stay green.
- **Orphan GC could not reclaim a per-pod claim CREATEd before its first binding-state status patch, stranding the pod on a gateway crash in that window:** the proposal re-keyed the orphan-GC predicate from `metadata.creationTimestamp` to the binding state and binding-transition time recorded on `SandboxClaim.status` and claimed it restored the shipped GC's phase-agnostic coverage (line 113), but the claim is CREATEd with spec only (the shipped CREATE at `pkg/gateway/podclaim/slotclaimer.go:709-715` sets `Spec` and no status) and the first binding state is written by a subsequent status patch, because a status subresource is not writable by the resource Create call (`charts/lenny/crds/lenny.dev_sandboxclaims.yaml:178-180`). A gateway crash between the CREATE and that first patch leaves the claim with empty status (no binding state, no binding-transition time, no `holdExpiresAt`), which neither the `bound`/`recycling` branch nor the `reserved` branch of the status-only predicate can select, so the claim is unreclaimable and the pod is stranded, exactly the failure the shipped GC closes via its creation-timestamp key plus the active-session check, both independent of any status write (gc.go:157, :166; spec/04:479 documents the crash-before-Postgres-persist scenario). Section 3.2 (line 94), Section 3.3 (line 113), the Section 6 spec/04:479 directive (line 157), and the Section 9 orphan-GC test row (line 218) now retain a `metadata.creationTimestamp` fallback that reclaims by draining a claim with an unset binding state older than `claimOrphanTimeout` from its creation time with no active session referencing the pod, keying the active-session check on the pod through the Postgres `pod_assignment` binding, so the CREATE-before-status crash window cannot strand the pod while the binding-state-transition-time key continues to govern claims that have reached a binding state.

### Pass 16 (2026-06-11, automated)

- **spec/23:26 competitive-landscape comparison-table "Execution modes" row omitted from the spec/23 directive:** the spec/23 directive cited only the numbered prose item at spec/23:78, but spec/23 has a second surface naming the modes, the competitive-landscape comparison-table "Execution modes" row at spec/23:26 ("session / task / concurrent (workspace + stateless)"). After the proposal removes `task` and `concurrent` from the mode set, spec/23:26 would still name two removed modes while the rest of the applied spec deletes them, leaving spec/23 internally inconsistent, the same sibling-omission class the proposal caught at spec/16:124 (Pass 14) and spec/05:573 (Pass 9). The spec/23 directive (line 174) now adds spec/23:26: rewrite the comparison-table row to the `session` and `service` naming with `sessionPolicy` in lockstep with the spec/23:78 rewrite, so no spec/23 surface names the removed `task` or `concurrent` modes after application.
- **§7.2 paragraphs naming removed `concurrent-workspace`/`task` modes omitted from the spec/07 directive:** the spec/07 directive's §7.2 work was scoped to conditions-to-Postgres, but two normative §7.2 surfaces (heading at spec/07:114) name modes the proposal removes: the titled "Concurrent-workspace mode (`slotId`) routing" paragraph at spec/07:331, which names `concurrent-workspace mode` four times and keys per-slot `slotId` message routing and the `SLOT_ID_REQUIRED` rejection to it, and the §7.2 Note at spec/07:161 referencing "task-mode cycling, and concurrent-workspace slot multiplexing." After application §7.2 would describe message routing and a state-machine note for a `concurrent-workspace mode` and a `task-mode` that no longer exist, while the parallel §10.1 (spec/10:54,141), §6.2 (spec/06:170-177), and §15.4.1 (spec/15:1440-1903) `slotId` surfaces are re-keyed to `maxConcurrentSessions > 1`, the same missed-edit-site class the Pass-6 fix closed for the §15.4.1 `slotId` references. The spec/07 directive (line 161) now re-keys spec/07:331 from `concurrent-workspace mode` to `maxConcurrentSessions > 1` (retaining `SLOT_ID_REQUIRED`, consistent with the spec/10, spec/06, and spec/15 re-keys) and re-keys the §7.2 Note at spec/07:161 to drop the `task-mode cycling` and `concurrent-workspace slot multiplexing` mode names.
- **spec/04:254 §4.4 workspace-size-limit paragraph naming `concurrent-workspace mode` omitted from the spec/04 directive:** the spec/04 directive scoped its edits to §4.6.1, §4.6.2, §4.6.3, §4.7, and §4.9, but the "Hard workspace size limit" paragraph at spec/04:254, in §4.4 (Event/Checkpoint Store, heading at spec/04:219), names `/workspace/slots/{slotId}/current/` "in concurrent-workspace mode." The per-slot workspace path itself survives for `maxConcurrentSessions > 1` (the per-slot sub-states block at spec/06:170-177 and the §6.4 layout are retained and re-keyed), but the mode name is removed; after application this §4.4 paragraph would name a removed mode while the parallel WorkspacePlan per-slot scope note at spec/14:5 is re-keyed by the spec/14 directive, confirming :254 as a missed sibling of the same mode-name re-key class. The spec/04 directive header and body (line 157) now add §4.4 (spec/04:254), re-keying the parenthetical `in concurrent-workspace mode` to `when `maxConcurrentSessions > 1`` in lockstep with the spec/14:5 and §6.4 per-slot re-keys, so the per-slot workspace-size-limit prose no longer names the removed mode.
- **Tier-0 static schema-example test (`tests/tier0_static/schemas_test.go`) hardcoded the four deleted task-frame example files and was in no edit list:** the proposal deletes the four between-task conformance examples (`schemas/examples/jsonl.task_complete.json`, `jsonl.task_complete_acknowledged.json`, `jsonl.task_ready.json`, and `lifecycle.task_complete.json`), but the tier-0 static test enumerates all four in two hardcoded slices and reads-and-validates each (`lifecycle.task_complete.json` in `TestLifecycleEventExamplesValidate` at schemas_test.go:157, and the three JSONL files in `TestAdapterJSONLExamplesValidate` at schemas_test.go:189-191; the file carries no build tag and runs in the standard tier-0 sweep). After application the test fails with file-not-found for each deleted path, and the schemas no longer define the removed types even if the files were retained, so the tree does not run tier-0 green; the proposal named the test nowhere and the generic Section 13 closer does not reach a hardcoded path list referencing deleted files, the same hardcoded-mirror edit-site class Pass 15 named for `catalog_test.go`'s `spec161Metrics`. Section 3.5 (line 128), the Section 5 schemas bullet (line 149), and Section 13 now name `tests/tier0_static/schemas_test.go` and remove the four task-frame entries from the two slices in the same change that deletes the example files and removes the frame `$defs`, so the tier-0 static suite stays green.

### Pass 17 (2026-06-12, automated)

- **§5.1 `taskPolicy:` YAML block and inheritance-table rows omitted from the §5.1 directive:** the §5.1 directive scoped its edits to `executionMode` values and `integrationLevel` prose, but §5.1 (heading at spec/05:3, §5.2 begins at spec/05:359) contains a full `taskPolicy:` block in the `runtime.yaml` example (spec/05:152-161, with `maxTasksPerPod: 50`, `onCleanupFailure`, `maxScrubFailures`, `maxPodUptimeSeconds`) and two inheritance-table references to the `taskPolicy` field (the "Independently configurable on derived runtime" list at spec/05:172 and the "`taskPolicy` | Override" row at spec/05:205) that the §5.2 directive does not reach. After application §5.1 would still display a `taskPolicy:` block and list `taskPolicy` as Override-on-derived while §5.2, the CRD types, the CRD manifests, the openapi.json, and the poolstore mirror document only `sessionPolicy`, leaving the applied spec internally inconsistent on a removed CRD field, the same missed-edit-site class the loop closed for sibling sections. The §5.1 directive (line 159) now replaces the §5.1 `taskPolicy:` block with the `sessionPolicy` block per Section 3.1 (renaming `maxTasksPerPod` to `recycle.maxSessionsPerPod`, `onCleanupFailure` to `recycle.onScrubFailure`, and `maxTaskRetries` to `maxSessionRetries`, relocating the surviving knobs onto `sessionPolicy`/`sessionPolicy.recycle`) and re-keys the two §5.1 inheritance-table references (spec/05:172 and :205) to `sessionPolicy`, so no §5.1 surface names the removed `taskPolicy` field or its knobs after application.
- **§6.4 base-layout sentence (spec/06:407) and `/workspace/shared/` paragraph (spec/06:409) named removed modes and were outside the §6.4 directive scope:** the §6.4 directive cited spec/06:374 and :385 and the content category "per-slot filesystem layout and responsibility-split prose," but spec/06:407 ("Session mode and task mode continue to use the base layout (`/workspace/current`)") is a standalone sentence naming the removed `task mode` after the responsibility-split bullets, and the titled "`/workspace/shared/` population and enforcement" paragraph at spec/06:409 names "The `/workspace/shared/` directory in concurrent-workspace mode," and the cited :374 is the `### 6.4 Pod Filesystem Layout` heading carrying no mode name, so only :385 landed on mode-naming prose. After application both sentences would name modes the proposal removes, the same missed-titled-paragraph class the proposal treats as a finding for the §4.4 sibling (spec/04:254, Pass 16) and the §6.2 paragraphs (Passes 4/6/7). The §6.4 directive (line 160) now adds spec/06:404, :407, and :409, re-keying spec/06:407 to the base layout when `maxConcurrentSessions == 1` (dropping `task mode`) and the spec/06:409 `/workspace/shared/` paragraph's `in concurrent-workspace mode` to `when maxConcurrentSessions > 1` in lockstep with the §4.4 spec/04:254 and spec/14:5 re-keys, and corrects the imprecise :374 citation to the actual mode-naming lines :385 and :404, so no removed surface survives in §6.4 after application.
- **§5.2 "Client visibility of task-mode isolation" client-facing contract (spec/05:475) named the removed `task` mode and was absent from every edit directive:** spec/05:475 is a normative, client-facing security-visibility contract that tells clients how to detect and reject sessions with weak isolation, titled "Client visibility of task-mode isolation," and names the removed mode twice ("clients creating sessions against a task-mode pool" and "the client is running on a task-mode pod where the scrub is best-effort"). After the proposal removes `task` mode and broadens the `residualStateWarning`/`podReuse` derivation to cover recycling, `maxConcurrentSessions > 1`, and service-mode pools (line 138), this paragraph would still describe the client-visibility contract exclusively in terms of "task-mode pod," leaving §5.2 internally inconsistent and the `residualStateWarning` semantics narrower than the proposal's own derivation, the same missed-edit-site class the proposal closed for sibling titled §5.2 paragraphs at spec/05:446 (Pass 9) and spec/05:449-455 (Pass 15). The §5.2 directive (line 158) now restages spec/05:475: retitle it to the session/service vocabulary, replace the two "task-mode pool"/"task-mode pod" references with the broadened derivation (a recycling, `maxConcurrentSessions > 1`, or service-mode pod), and restate the `residualStateWarning: true` semantics so the client-facing contract matches the Section 4 derivation (line 138) and the §7.1 concurrent-stateless rationale rather than naming the removed `task` mode.
- **§6.2 "Pod crash during active task-mode task" paragraph (spec/06:185-193) not re-keyed; the directive edits lines inside it but left the task-mode framing:** the directive lands the `maxTaskRetries`→`maxSessionRetries` rename inside this titled paragraph (lines 187, 190) without restaging the paragraph itself, which is neither a `task_cleanup` state nor a between-task transition, so the generic removal language does not reach it. After application the paragraph would read `maxSessionRetries` while its heading still says "Pod crash during active task-mode task," its body still says "a task-mode pod is `attached`" and asserts the now-false "task-mode pods run sequential tasks without a persistent session checkpoint," and its closing sentence still says "not a session recovery path (no checkpoint replay)," naming the removed `task` mode, the removed `attached` phase (the coarse enum at line 115 carries no `attached`), and a claim the session model contradicts, the same titled-prose defect class Passes 4/6/7/8 closed for the sibling §6.2 paragraphs. The spec/06 §6.2 directive (line 160) now restages spec/06:185-193: re-key the heading and the `attached`-cycling framing to the session crash-recovery path, correct the false "task-mode pods run sequential tasks without a persistent session checkpoint" claim, re-key the `taskPolicy` field reference on the `maxSessionRetries` bullet to `sessionPolicy`, and reconcile the renamed `maxSessionRetries` knob with the surviving crash-re-dispatch semantics, so no §6.2 surface names the removed `task` mode or the removed `attached` phase after application.
- **§6.1 "Task-mode credential lease lifecycle" and "Concurrent-mode credential lease lifecycle" titled paragraphs (spec/06:26, :28) named removed modes and were absent from every edit directive:** the spec/06 §6.1 directive enumerated only the preConnect compatibility table, the `sdk_connecting` watchdog paragraph, and the one-session-only invariant restate, leaving the two titled §6.1 credential-lease paragraphs unaddressed. After application they would remain as titled §6.1 paragraphs naming the removed `task` and `concurrent` modes ("A task-mode pod that completes a task releases its credential lease before the next task begins"; "a concurrent-workspace pod with `maxConcurrent: 8` and all slots active holds up to 8 simultaneous credential leases"), and the :28 paragraph names the `maxConcurrent: 8` example the proposal renames to `maxConcurrentSessions` on the session concurrency path, leaving §6.1 internally inconsistent (category d), the same missed-edit-site class the loop closed for sibling §6.2 titled paragraphs. The spec/06 §6.1 directive (line 160) now adds spec/06:26 and :28: re-key "Task-mode credential lease lifecycle" to the session/recycle per-execution leasing vocabulary (preserving the per-execution lease-and-revoke granularity the security model requires per the Non-goals), and re-key "Concurrent-mode credential lease lifecycle" to per-slot leasing when `maxConcurrentSessions > 1` with the `maxConcurrent: 8` example renamed to `maxConcurrentSessions`, so no §6.1 surface names the removed `task`/`concurrent` modes or the renamed knob after application.

### Pass 18 (2026-06-12, automated)

- **Guard-webhook PATCH/PUT accept-set could not gate `SandboxClaim` binding-state writes (deadlock or incoherence on the initial `bound` patch):** earlier passes widened the `lenny-sandboxclaim-guard` PATCH/PUT precondition (which reads the referenced `Sandbox.status.phase`) from the single value `claimed` to the coarse phases `claimed`, `sdk_connecting`, and `reserved`, and framed that accept-set as admitting the `reserved → bound` rebind. The framing is broken either way the guard is wired. Binding state lives on the `SandboxClaim.status` subresource, and the occupancy projection sets the referenced `Sandbox` to `claimed` from the claim's `bound` binding state (Section 3.3), so the gateway's first `bound` status patch lands while the `Sandbox` is still `idle` (the claim is not `bound` yet, so nothing has projected `claimed`). If the guard fired on status-subresource writes, the accept-set would reject that first `bound` patch and the claim could never reach `bound`, because the `Sandbox` can never reach `claimed` first: a deadlock. If the guard fired only on the main resource, it could not read or gate binding-state writes at all, so admitting a `reserved → bound` rebind via the accept-set would be incoherent. Resolved per the human-adjudicated decision: in the per-pod model the guard's role is the `CREATE` per-pod-uniqueness check only (reject a second non-terminal `SandboxClaim` referencing the same `Sandbox`, with no slot exemption), and the shipped per-session PATCH/PUT-reads-`Sandbox`-phase rule is dropped because the per-pod claim spec is immutable after `CREATE` and binding-state writes are `SandboxClaim.status`-subresource patches the webhook does not gate. Binding-state transitions (initial empty `→ bound`, `bound → recycling`, `recycling → reserved`, the `reserved → bound` rebind, and the terminal `→ released` and `→ failed`) are serialized by the optimistic-concurrency UID and `resourceVersion` preconditions Section 3.2 already specifies for the rebind-versus-hold-expiry race. The same predicate is now stated at every site: the Section 5 `SandboxClaim` bullet, the Section 6 spec/04 §4.6.1 guard directive (which removes the PATCH/PUT rule rather than re-keying it and retains the `CREATE` per-pod-uniqueness re-key), the Section 6 spec/04 §4.6.3 binding-state-enumeration block (which now states the guard examines existing claims by `.spec.sandboxRef` and does not gate status writes, dropping the stale-write-rejection clause), the Section 6 spec/18 §18.10 deliverable (which deletes the PATCH/PUT clause and keeps only the `CREATE` clause), the TESTING.md §13.7 directive, the Section 9 Claim-model test row, the Section 13 files-touched summary, and the Pass 2, Pass 4, and Pass 5 entries that referenced the widened accept-set (each carries a Pass 18 supersession note). The rationale is recorded inline: the initial `bound` patch lands while the `Sandbox` is still `idle` because the projection sets `claimed` from the `bound` claim, so an accept-set requiring a live-claim phase would reject the write that establishes `bound`, and a status-subresource binding-state write cannot be gated by a main-resource accept-set in any case.

### Revision (2026-06-12, edit-block promotion)

Section 6 was promoted from a 21-row directive table to 85 anchored edit subsections covering every row-named edit site; the directive rationale survives in subsection prose. Concretizations made where the directives were silent, with Section 3 governing: `acknowledgeBestEffortScrub` is placed under `sessionPolicy.recycle` and `acknowledgeProcessLevelIsolation` directly under `sessionPolicy`; `scrubProfile: standard` is rejected on a `recycle.allowCrossTenantReuse: true` pool, carrying the current `restart`-default cross-tenant posture onto the renamed field; the presets table is staged without historical mode names; the re-warm start stamp is recorded on any recycle disposition (scrub success or a warn-policy failure) so the scrub-warning re-warm survives; the §6.2 five-second idle-stabilization delay is re-keyed to the reserved hold window; cordoned-node retirement applies to recycling pools regardless of preConnect; and the names fixed by promotion are the `lenny_warmpool_reserved_pods` gauge, the `lenny_session_expiry_total` counter (reason values `max_session_age` and `max_idle_time`), the `slotCounterPostgresFallbackMaxSeconds` bound (default 60 seconds), the Phase 12c retitle to "`sessionPolicy` presets and service mode", and the tier-4/tier-5 test names in TESTING.md §13.27. Section-number corrections relative to the directive rows: the elicitation-wait rule sits in §9.2, the playground idle row in §27.2, the guard deliverable in §18.10 (Phase 3.5), and the rows cited as §16.6 in §16.1.1. Known sites deliberately left to the convergence loop: the §6.4 data-at-rest prose at spec/06:414 and :421 (still factually correct), the §4.7 manifest `taskId` field row (the field survives Phase 1 frozen to the root task identifier), and the `lifecycle_support` schema-array `task_lifecycle` removal (a `schemas/` edit recorded in Section 5).

### Pass 19 (2026-06-12, automated)

- **§12.6 cross-cluster `PodRecord` compatibility-contract state enumeration not re-keyed to the coarse phase enum (second §12.6 pod-state list left contradicting the first and its cited §4.6.1 authority):** Section 6.59 re-keyed the `agent_pod_state.state` SQL comment at spec/12:461 to the coarse enum `warming, idle, reserved, claimed, sdk_connecting, draining, failed, terminated`, but spec/12:620 carries a second, normative enumeration of the same canonical pod state set in the "Cross-cluster `PodRecord` compatibility contract" subsection: the `State` machine-values invariant states the field "MUST be one of the canonical pod state machine values defined in [§4.6.1] (`idle`, `claimed`, `running`, `draining`, `terminated`, `failed`)." Section 6.5 re-keys the §4.6.1 enum this invariant cites as its authority to the coarse set, so after application the §12.6 schema comment (`reserved`, no `running`) and the §12.6 compatibility-contract invariant (`running`, no `reserved`/`warming`/`sdk_connecting`) would contradict each other and the §4.6.1 source, and the invariant would forbid the `reserved` value this proposal introduces while permitting the `running` value the new coarse enum drops, the same missed-edit-site class the loop closed for sibling cross-reference enumerations. Section 6.59 now adds an edit re-keying the spec/12:620 `State` machine-values invariant from `(idle, claimed, running, draining, terminated, failed)` to the coarse phase enum `(warming, idle, reserved, claimed, sdk_connecting, draining, failed, terminated)`, matching the §4.6.1 authority (Section 6.5) and the spec/12:461 schema comment, so the two §12.6 enumerations and their cited §4.6.1 source agree on one canonical pod state set after application. The Section 6.59 preamble now records the second enumeration and the contradiction it would otherwise leave.

### Pass 20 (2026-06-12, automated)

- **§6.4 guard directive left three sentences in the same spec/04:384 paragraph asserting the webhook reads `Sandbox.status.phase`, contradicting the seed-fixed `CREATE`-only guard:** the §6.4 directive scoped its edits to the three PATCH/PUT sentences, the `(Note: ...)` parenthetical, and the `For CREATE` sentence, but the spec/04:384 guard paragraph contains three further sentences the directive did not touch: the opening framing ("The webhook intercepts every `CREATE`, `PATCH`, and `PUT` operation on `SandboxClaim` resources in agent namespaces. The authoritative pod state machine lives on the referenced `Sandbox` CRD's `.status.phase` ... every phase check in this webhook reads `Sandbox.status.phase` via the claim's `.spec.sandboxRef`."), and the closing double-claim rationale ("Because the webhook evaluates `Sandbox.status.phase` as persisted in etcd at admission time, it makes double-claim impossible regardless of timing: even if two writers race with the same `resourceVersion`, the second writer's request is rejected at admission before it reaches the API server's optimistic-lock check."). After application the same paragraph would simultaneously say the webhook "does not gate `PATCH`/`PUT`" and that it intercepts `PATCH`/`PUT`, reads `Sandbox.status.phase` on every phase check, and grounds its double-claim guarantee in evaluating `Sandbox.status.phase`, leaving the seed-fixed guard role internally contradictory within one paragraph on a fail-closed security control. The §6.4 directive (now five edits) adds the opening-framing anchor and the closing-sentence anchor: the opening restages the intercept set to `CREATE`-only and states the webhook reads no phase, and the closing grounds the double-claim guarantee in the deterministic `claim-<podName>` name plus the API-server name-uniqueness check rather than in reading `Sandbox.status.phase`, so the entire paragraph describes one `CREATE`-only guard after application.
- **`SandboxClaimGuardUnavailable` alert (single-sourced in `pkg/alerting/rules/rules.go:486`, mirrored at spec/16:424) and the spec/17:48 admission-policy inventory item 7 still described the removed PATCH/PUT guard role and were in no edit list:** spec/16:424 asserts that during a guard outage "all `PATCH` and `PUT` operations on `SandboxClaim` resources are blocked — new pod claims are prevented," but under the seed-fixed per-pod model pod acquisition is a `CREATE` and the webhook no longer gates `PATCH`/`PUT`, and this description is single-sourced in `pkg/alerting/rules/rules.go:486` (the §16 catalog renders from that package, so editing only spec/16 prose would diverge from the rendered `PrometheusRule`); spec/17:48 item 7 likewise describes the role as "double-claim prevention and tenant-scope enforcement on `SandboxClaim` PATCH/PUT operations," a PATCH/PUT role the seed fix removes. Neither site was reached by §6.74 (which touched only `CheckpointStorageHigh` and `PodClaimQueueSaturated`), §6.76, or §6.77 (which touch §17.8). §6.74 now re-keys the `SandboxClaimGuardUnavailable` description in both the spec/16 mirror and the `pkg/alerting/rules` single source so a guard outage is described as blocking every `SandboxClaim` `CREATE` (new pod acquisition) rather than `PATCH`/`PUT`, and a new §6.75 re-keys spec/17:48 item 7 to per-pod claim uniqueness at `CREATE`, dropping the "tenant-scope enforcement on PATCH/PUT" framing the seed-fixed webhook no longer performs (the shipped guard implements no separate tenant-scope role; the §6.4 webhook reads no phase and performs only the `CREATE` uniqueness check). Section 13 now names `pkg/alerting/rules` for the `SandboxClaimGuardUnavailable` description change alongside the metric renames, and the §6.x subsections §6.75 through §6.86 were renumbered to admit the new spec/17 §17.2 subsection (the single internal cross-reference to the playground subsection, now §6.84, was updated in lockstep).

## 12. Decisions resolved in initial review and open items

### Resolved (2026-06-11)

- **Claim deletion timing on recycle:** hold the claim through a `reserved` binding state with a deployment-level TTL (`gateway.claimHoldTTLSeconds`, default 10 seconds); configuration validation warns on high values. The gateway stamps `holdExpiresAt` at the `reserved` patch, and hold-expiry deletion is precondition-guarded so a cross-replica rebind wins the race. The coarse enum gains `reserved` (Sections 3.2 and 3.3).
- **`slot_active`:** collapsed into `claimed` on the coarse enum; concurrency is observable through the Redis counter and metrics.
- **`maxClientIdleSeconds`:** included as the single idle bound, replacing `runtime.limits.maxIdleTimeSeconds` and its §6.2 timer; the platform default moves from 600s to the pool's effective `maxSessionAgeSeconds`, which raises the platform idle bound to the age cap. The clock has its own pause table: it is paused during `suspended`, `finalizing`, and the recovery states `resume_pending` and `resuming`, but runs during `input_required`, `awaiting_client_action`, and elicitation waits. `maxSessionAge` runs during `input_required` but is paused during `awaiting_client_action`, so the default-equal bound binds before the age cap when a deployer lowers it or when the session accrues idle time in the age-paused `awaiting_client_action` wait. Agent activity counts as activity, and the existing `max_idle_time` expiry reason and playground override are retained (Section 3.1).
- **Scrub report RPCs:** `ReportSessionScrub` for the per-session slot cleanup and `ReportPodScrub` for the whole-pod recycle scrub, both on `GatewayControl`.
- **Metric renames:** the `lenny_pod_*` family and `lenny_service_*`, with no deprecated aliases (Section 4).
- **Scope:** `onPoolExhausted: queue` is included in this proposal; the batch session-creation API remains a follow-up.

### Open

- **The high-TTL warning threshold** for `gateway.claimHoldTTLSeconds`, and whether `lenny-preflight` checks it in addition to configuration validation.
- **Queue fairness and depth.** The initial queue is a per-pool FIFO with a wait bound; whether it needs per-tenant fairness and an explicit depth bound, and whether service mode participates in queueing.
- **The default value of `maxQueueWaitSeconds`** relative to client HTTP timeouts.

## 13. Files touched on application

Spec: `spec/02`, `spec/04`, `spec/05`, `spec/06`, `spec/07`, `spec/08`, `spec/09`, `spec/10`, `spec/11`, `spec/12`, `spec/13`, `spec/14`, `spec/15`, `spec/16`, `spec/17`, `spec/18`, `spec/23`, `spec/24`, `spec/26`, and `spec/27` per Section 6. The build-sequence companion `TESTING.md` §13.27 is rewritten alongside the spec/18 Phase 12c block, and its §13.7 `pkg/admission/sandboxclaim_guard` description is updated alongside the spec/18:201 Phase 5 guard deliverable and the spec/04:384 guard paragraph (Section 6), all three of which remove the per-session PATCH/PUT rule and state the per-pod guard as the CREATE per-pod-uniqueness check only, so the build sequence and its test inventory describe the same `sessionPolicy` and service-mode deliverables and the same CREATE-only guard role. Schemas: `schemas/lenny-adapter.proto` (new RPCs and task-mode comment and field cleanup only), `schemas/lifecycle-events.schema.json`, `schemas/lenny-adapter-jsonl.schema.json`, the `schemas/examples/` between-task frame instances and the four hardcoded example-path entries that reference them in `tests/tier0_static/schemas_test.go` (the `TestLifecycleEventExamplesValidate` slice at schemas_test.go:157 and the `TestAdapterJSONLExamplesValidate` slice at schemas_test.go:189-191), removed in the same change so the tier-0 static suite stays green, and the `Runtime`, `Sandbox`, `SandboxTemplate`, and `SandboxClaim` CRD types with the generated CRD manifests they back, regenerated by `make generate`: the six manifests carrying the `executionMode` enum (`charts/lenny/crds/lenny.dev_runtimes.yaml`, `charts/lenny/crds/lenny.dev_sandboxes.yaml`, `charts/lenny/crds/lenny.dev_sandboxtemplates.yaml`, and the `pkg/embedded/crds/` copies of all three; the Runtime CRD is the spec-declared primary v1 home of `executionMode` per spec/05:536 and is reconciled by `pkg/controller/runtime/controller.go`, so its enum at lenny.dev_runtimes.yaml:289-292 is enforced at API-server admission and must match the runtimestore typed enum and the §5.2 mode set); the `SandboxTemplate` manifests additionally drop the `concurrencyStyle` schema block and its `enum: [stateless, workspace]` (charts/lenny/crds/lenny.dev_sandboxtemplates.yaml:81-90 and the `pkg/embedded/crds/` copy at :81) for the removed field, and re-key the `TaskPolicy`-derived `microvmScrubMode`/`acknowledgeMicrovmResidualState` schema blocks (lenny.dev_sandboxtemplates.yaml:289 and :233) onto the new `sessionPolicy.recycle` structure; and the two `SandboxClaim` manifests carrying the reduced spec and the binding-state enum (`charts/lenny/crds/lenny.dev_sandboxclaims.yaml` and its `pkg/embedded/crds/` copy, which drop the `slotId` and `sessionId` fields, the `required: sessionId` entry, and the `.spec.sessionId` printColumn, change the `status.phase` enum to `bound; recycling; reserved; released; failed`, and add the `holdExpiresAt`, `rewarmStartedAt`, and binding-state-transition-time status fields per Section 5). The hand-authored gateway OpenAPI document `pkg/gateway/openapi/openapi.json` (embedded by `pkg/gateway/openapi/openapi.go` and not regenerated by `make generate`) is edited in the same change: the three `executionMode` enums (openapi.json:180, :270, :540) change to `["session","service"]`, the `concurrencyStyle` enum property (openapi.json:541) is removed, `conversationContinuity` (enum `["platform","none"]`) is added to the `sessionIsolationLevel` schema (openapi.json:179-184), and the `taskPolicy` object inside the `Runtime` schema (openapi.json:380-394) is replaced by the `sessionPolicy` structure (drop `maxTasksPerPod` at openapi.json:391, rename `maxTaskRetries` at openapi.json:393 to `maxSessionRetries`, rename `microvmScrubMode` at openapi.json:385 to `scrubProfile` with the enum `["standard","vm-restart","in-place"]` under the `recycle` sub-object, rename `onCleanupFailure` to `onScrubFailure`, and relocate the surviving knobs onto `sessionPolicy`), because it is the single authoritative schema the MCP `create_session` tool schema and the client SDKs derive from (spec/15:1386, 2492), so editing only the downstream SDK structs would leave the served contract advertising the removed modes, a `taskPolicy` object with `maxTasksPerPod`/`maxTaskRetries`, and a `microvmScrubMode` enum the proposal removed, and omitting the new field. Code (with the implementation work): `pkg/gateway/runtimestore` (rename the closed `ExecutionMode` enum to `session | service` and update `AllExecutionModes()`, runtimestore.go:1074-1084), `pkg/gateway/poolstore` (remove the `ConcurrencyStyle` enum and its validators, replace the `TaskPolicy` struct and the concurrent-workspace `Pool` fields with the `sessionPolicy` mirror, re-key the preConnect-versus-concurrency rejection in `ValidatePreConnectExecutionMode` at poolstore.go:686-696 to `maxConcurrentSessions == 1` plus the service-mode rejection, and re-key the categorical cross-tenant rejection in `ValidateConcurrentConfig` at poolstore.go:503-506 to `sessionPolicy.maxConcurrentSessions > 1` so cross-tenant reuse stays categorically rejected for simultaneous slots, distinct from the sequential-reuse microvm gate, and carry the microvm cross-tenant gate in `ValidateTaskPolicy` at poolstore.go:623 forward onto the `sessionPolicy.recycle.allowCrossTenantReuse` validation path rather than dropping it when the `ExecutionModeTask` guard at poolstore.go:605 is removed, so `recycle.allowCrossTenantReuse: true` still requires `isolationProfile: microvm` on the sequential-reuse path, and re-key the mode-independent `ValidateCrossTenantReuseTier` at poolstore.go:667 so its `p.AllowCrossTenantReuse` read keys off `sessionPolicy.recycle.allowCrossTenantReuse` rather than the deleted top-level `Pool.AllowCrossTenantReuse` field, keeping the spec/05:396 T4 prohibition's mode-independent pool-admission enforcement alive after the field relocation), `pkg/gateway/admin` (the three pool-admission call sites that invoke `ValidateCrossTenantReuseTier` unconditionally — `pkg/gateway/admin/pools.go:606`, `pkg/gateway/admin/pools.go:1447`, and `pkg/gateway/admin/bootstrap_resources.go:62` — which pass the relocated field into the re-keyed enforcer, and the `ValidatePreConnectExecutionMode` call sites that pass `pl.ConcurrencyStyle`, `pkg/gateway/admin/pools.go:598`, `pkg/gateway/admin/pools.go:1480`, and `pkg/gateway/admin/bootstrap_resources.go:56`, which the derived preConnect rule replaces), `pkg/gateway/podclaim` (claimer and slotclaimer), `pkg/gateway/podsession` (binder and slotbinder), `pkg/gateway/slotcounter` (the atomic Redis Lua cap and its `Counter.Reserve(ctx, podID, maxConcurrent int32)` signature, slotcounter.go:93,244; the cap argument its callers in `pkg/gateway/podclaim`/`pkg/gateway/podsession` pass becomes the session-mode `sessionPolicy.maxConcurrentSessions` bound, matching the re-keyed §5.2 atomicity paragraphs, so the intra-pod gate caps at the session-mode bound rather than the service-mode `maxConcurrent`), `pkg/gateway/sessionserver`, `pkg/gateway/sessionidle` and `pkg/gateway/watchdog` (the `maxClientIdleSeconds` clock and its default), `pkg/gateway/taskdriver` and `pkg/sandbox/taskcleanup` (re-sourced inputs, and the `RetireReason` value `ReasonMaxTasksReached` (taskcleanup.go:126-128) re-keyed off the removed `maxTasksPerPod` trigger onto the `recycle.maxSessionsPerPod` bound so the emitted `lenny_pod_retirement_total{reason}` value matches the re-keyed spec/16:11 and spec/16:299 retirement-trigger vocabulary (`session_count_limit`); lexical identifier renames in Phase 2), `pkg/adapter` (scrub, lifecycle channel, and gatewaycontrol), `pkg/controller/warmpool` and `pkg/controller/sandbox` (projection, drains, orphan GC, the projection of a `recycling` claim to `claimed` during the whole-pod scrub and to `sdk_connecting` only after the re-warm-start stamp, and the recycle-edge `sdk_connecting` watchdog clock re-anchored from `pod.Status.StartTime` to the `SandboxClaim.status` re-warm-start stamp so neither the prior occupancy episode nor the whole-pod scrub is counted against the re-warm budget, `pkg/controller/sandbox/sdkwarm.go:105,114-122`), `pkg/controller/sandbox/lifecycle` (the `sdk_connecting → reserved` non-failure exit and the `TimedOut()` clock input, `lifecycle.go:138-142,186-191`), `pkg/controller/poolscaling` (demand-source PromQL and `mode_factor` renames; reserved pods counted as occupied), `pkg/gateway/gatewaymetrics`, `pkg/gateway/tenantaffinity`, `pkg/gateway/statelessproxy`, `pkg/observability/metrics` (the §16.1 typed catalog `catalog.go`, renaming the three §16.1 metric entries `lenny_task_reuse_count`/`lenny_task_pod_scrub_failure_count`/`lenny_task_pod_retirement_total` and their description strings at catalog.go:76, :77, and :189, and the in-code spec-mirror transcription `spec161Metrics` in `catalog_test.go:15-151`, which two tier-1 unit tests check the catalog against bidirectionally — `TestMetricCatalogIsCompleteAgainstSpec161` and `TestMetricCatalogHasNoUnspecifiedMetrics`, `catalog_test.go:183,197` — so the three renamed names at catalog_test.go:17,68 must change in the same edit or both tests fail and the tree does not build green; if the reserved-pod gauge and idle-termination counter from Section 4 are registered in `catalog.go`, their rows are added to both `spec161Metrics` and the §16.1 inventory directive so both consistency tests stay green), `pkg/sandbox/state` (CoarseState mapping for the new claim states), `pkg/admission/sandboxclaim_guard`, `pkg/admission/pool_config_validator` (re-key the `decideConcurrentWorkspace` cross-tenant rejection at validator.go:579-581 to the derived `maxConcurrentSessions > 1` predicate, and carry the `decideTaskMode` microvm cross-tenant gate at validator.go:534 and the `decideTaskMode` T4 cross-tenant reuse prohibition at validator.go:545 forward onto the `sessionPolicy.recycle.allowCrossTenantReuse` validation path rather than dropping them when `case "task"` at validator.go:431 is removed, so the microvm gate and the T4 prohibition stay enforced on the sequential-reuse successor of task mode, alongside the other derived-property re-keys), `pkg/apis/lenny/v1alpha1` (the three `executionMode` `+kubebuilder:validation:Enum` markers at runtime_types.go:37, sandbox_types.go:45, and sandboxtemplate_types.go:175 change from `session;task;concurrent` to `session;service`, the Runtime marker being the spec-declared primary v1 home per spec/05:536; the standalone `ConcurrencyStyle` field on `SandboxTemplate` (sandboxtemplate_types.go:185) with its `+kubebuilder:validation:Enum=stateless;workspace` marker (sandboxtemplate_types.go:183) and doc comment, which gates the removed `concurrent` mode and is neither the `executionMode` marker nor inside a policy struct, is removed; the `taskPolicy`/`concurrentWorkspacePolicy` types are replaced by the `sessionPolicy` type, carrying the `TaskPolicy.MicrovmScrubMode` field (sandboxtemplate_types.go:37, renamed to `recycle.scrubProfile` with the `+kubebuilder:validation:Enum` marker extended to `standard;vm-restart;in-place`) and the `TaskPolicy.AcknowledgeMicrovmResidualState` field (sandboxtemplate_types.go:44) forward onto the `sessionPolicy.recycle` type rather than dropping them with the struct, so the cross-tenant in-place residual-state acknowledgment remains a CRD field the API server enforces; and the CRD manifests regenerate), `pkg/podregistry`, `pkg/podlifecycle`, migrations (the `agent_pod_state` `sessions_served` and `scrub_failure_count` columns, plus a new append-only migration after the current head 0166 that drops and re-adds `runtime_definitions_execution_mode_check` as `CHECK (execution_mode IN ('session', 'service'))`, since the gateway runtime store writes `execution_mode` into `runtime_definitions` at `pkg/gateway/runtimestore/pgstore/pgstore.go:239` and `:311` and the unaltered constraint at `migrations/0001_initial_schema.up.sql:56-57` would otherwise reject the `service` value; the same migration re-keys the stale mode comments at `migrations/0033_sandbox_warm_pools.up.sql:15` and `migrations/0084_sessions_isolation_level.up.sql:18` and retires the `concurrency_style` column from `migrations/0040_warm_pool_concurrency.up.sql:13` once `concurrencyStyle` is removed), `charts/lenny` (RBAC, pool schema, and the gateway configuration for the claim-hold TTL and queue bounds), `pkg/alerting/rules` (the `SandboxClaimGuardUnavailable` `Description` at rules.go:486 re-keyed from "all `PATCH` and `PUT` operations on `SandboxClaim` resources are blocked — new pod claims are prevented" to the seed-fixed `CREATE`-only blocking text per §6.74, since this alert catalog is the single source `make generate` renders into the spec/16 mirror, so editing only spec/16:424 would diverge from the rendered `PrometheusRule`), the three hand-written client SDK type files that mirror the §7.1 `sessionIsolationLevel` object (`sdks/client/go/lenny/types.go` `IsolationLevel` struct at :71-77, `sdks/client/python/lenny/types.py` `IsolationLevel` dataclass at :112-119 and its `from_wire` decode at :122-129, and `sdks/client/typescript/src/types.ts` `IsolationLevel` interface at :71-77), each of which adds the `conversationContinuity` field (and its `conversation_continuity` decode in the Python `from_wire`) alongside the `executionMode` value-set change so the new client-facing contract field is not silently dropped on deserialization, and the test tiers these touch. Documentation per Section 7. The prior `proposals/0002_fix_pod-status-ownership-decomposition-warmpoolcontroller-sole-w.md` is marked superseded when this proposal is converged.
