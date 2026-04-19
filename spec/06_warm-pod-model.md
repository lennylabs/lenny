## 6. Warm Pod Model

### 6.1 What a Pre-Warmed Pod Looks Like

A warm pod is either **pod-warm** (default) or **SDK-warm** (optional, per runtime capability):

- Pod scheduled and running
- Selected `RuntimeClass` active (runc/gVisor/Kata)
- Runtime adapter listening and health-checked
- Agent binary dependencies installed and loaded
- `/workspace/current` exists but is empty
- `/workspace/staging` exists for upload staging
- `/sessions` directory present for session files
- Projected service account token mounted (audience: deployment-specific, see [Section 10.3](10_gateway-internals.md#103-mtls-pki))
- No LLM provider credentials assigned (credential lease is assigned at claim time, not warm time)
- No user session bound
- No client files present
- Marked "idle and claimable" via readiness gate

**Pod-warm (default):** The agent process is NOT started. Because workspace contents (including `CLAUDE.md`, `.claude/*`) are unknown until request time, the session must start after workspace finalization. This is the safest and most general mode.

**SDK-warm (optional):** The agent process IS pre-connected and waiting for its first prompt. See below for constraints.

**Session mode security invariant: pods are one-session-only.** After a session completes or fails in `executionMode: session`, the pod is terminated and replaced — never recycled for a different session. This prevents cross-session data leakage through residual workspace files, session transcripts, cached DNS, or runtime memory. **Task mode** (`executionMode: task`) relaxes this invariant with explicit deployer acknowledgment — pods are reused across sequential tasks with workspace scrub between tasks (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). **Concurrent mode** allows multiple simultaneous tasks on a single pod (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

**Task-mode credential lease lifecycle.** Credentials are leased per-task, not per-pod. A fresh credential assignment (`AssignCredentials` RPC) is performed at each task dispatch — the pod does not retain credentials between tasks. The adapter manifest is regenerated per task, and `/run/lenny/credentials.json` is rewritten with the new lease before the runtime binary is spawned for each task. The previous lease is revoked when the task completes or the runtime exits. Per-task leasing aligns with the single-use-execution model: each task dispatch is semantically a fresh credential assignment regardless of pod reuse. Pool capacity implications: because each task requires a credential assignment, the `maxConcurrentSessions` limit on credential pool entries is evaluated per-task (not held for the pod's lifetime). A task-mode pod that completes a task releases its credential lease before the next task begins.

**Concurrent-mode credential lease lifecycle.** Credentials are leased per-slot, not per-pod. Each active slot holds an independent credential lease obtained via a separate `AssignCredentials` RPC at slot assignment time. This ensures `maxConcurrentSessions` on pool credentials accurately reflects slot-level concurrency and prevents a single credential rotation from disrupting all concurrent slots simultaneously. The adapter writes per-slot credential files at `/run/lenny/slots/{slotId}/credentials.json` (mode `0400`, tmpfs-backed) rather than a single global `/run/lenny/credentials.json`. Credential rotation in concurrent mode follows the standard rotation protocol ([Section 4.9](04_system-components.md#49-credential-leasing-service) Fallback Flow) independently per slot — the in-flight gate and `credentials_rotated` acknowledgment apply to the individual slot being rotated, not to the pod as a whole. Other slots' LLM requests are unaffected by a rotation on a sibling slot. When a slot completes or fails, its credential lease is revoked independently. Pool capacity implications: a concurrent-workspace pod with `maxConcurrent: 8` and all slots active holds up to 8 simultaneous credential leases, each counting against `maxConcurrentSessions`.

**Optional: SDK-warm mode.** Runtimes that declare `capabilities.preConnect: true` can pre-connect their agent process during the warm phase (before workspace finalization) without sending a prompt. The warm pool controller starts the SDK process after the pod reaches `idle` state, leaving it waiting for its first prompt. This eliminates SDK cold-start latency from the hot path.

**All pods are SDK-warm when the runtime supports it.** Pools referencing a `preConnect`-capable runtime warm **all** pods to SDK-warm state. There is no pod-warm/SDK-warm split or ratio to configure — simplicity over micro-optimization.

**Demotion on demand.** SDK-warm mode is only safe when the request does not inject project config files that must be present at session start. Each `Runtime` declares a `sdkWarmBlockingPaths` list (default: `["CLAUDE.md", ".claude/*"]`) — if the workspace plan includes files matching any of these glob patterns, the gateway sets `requiresDemotion: true` on the `ClaimOpts` and the adapter calls the `DemoteSDK` RPC ([Section 4.7](04_system-components.md#47-runtime-adapter)) to tear down the pre-connected SDK process, transitions the pod back to `idle` (pod-warm), and the normal pod-warm setup path proceeds. This incurs an SDK teardown penalty (typically 1–3s depending on runtime) but avoids the complexity of maintaining a dual-pool inventory. The metric `lenny_warmpool_sdk_demotions_total` (counter, labeled by pool) tracks demotion frequency for observability.

**`sdkWarmBlockingPaths` matching contract.** Patterns are matched against the **relative path** of each uploaded file within the workspace root (e.g., `CLAUDE.md`, `.claude/settings.json`). Matching is **case-sensitive** on all platforms. The glob dialect is Go's `path.Match` extended with `**` support: `**` matches zero or more path segments (e.g., `**/*.md` matches `foo/bar/README.md`); `*` matches within a single path segment only (no `/`); `?` matches any single non-separator character; `[...]` matches character classes. Patterns are never anchored to the root automatically — a pattern `CLAUDE.md` matches only a top-level file named `CLAUDE.md`, not `subdir/CLAUDE.md` (use `**/CLAUDE.md` for recursive matching). Symlinks are **not resolved** — only the literal path of each file in the workspace plan is checked, not its target. Files injected via `workspaceDefaults` (from the `Runtime` definition) are included in the matching check alongside client-uploaded files — if a derived runtime's `workspaceDefaults` includes a file whose path matches a blocking pattern, demotion is triggered even when the client uploads no files.

**Disabling demotion-path checking entirely.** Setting `sdkWarmBlockingPaths: []` (empty list) on the Runtime definition disables demotion-path checking entirely — no file ever triggers demotion and all pods remain SDK-warm for every request. This is the correct configuration when the SDK process is designed to tolerate all files that may appear in the workspace (e.g., the runtime initializes lazily or reads project config after receiving its first prompt). Derived runtimes that include `CLAUDE.md` or `.claude/*` files in their `workspaceDefaults` as a normal part of operation — and whose SDK process is safe to run with those files present — should set `sdkWarmBlockingPaths: []` to avoid triggering 100% demotion and hitting the circuit-breaker threshold.

**Demotion support is mandatory for `preConnect` runtimes.** Declaring `capabilities.preConnect: true` implies that the runtime's adapter supports the `DemoteSDK` RPC — the ability to cleanly tear down and restart the agent process without restarting the pod. This is not optional: since all pods in a `preConnect` pool are SDK-warm, any request that includes `sdkWarmBlockingPaths` files requires demotion. A runtime that cannot safely tear down its SDK process must not declare `preConnect: true`. The gateway validates this at runtime registration: if `preConnect: true` is set and `sdkWarmBlockingPaths` is non-empty (which it is by default), the registration response includes a warning reminding the runtime author that their adapter must implement `DemoteSDK`. If the adapter does not implement `DemoteSDK`, the RPC returns `UNIMPLEMENTED` and the gateway fails the session with a clear error (`SDK_DEMOTION_NOT_SUPPORTED`) rather than silently proceeding with stale SDK state.

**Demotion rate threshold and circuit-breaker.** The default `sdkWarmBlockingPaths` list (`["CLAUDE.md", ".claude/*"]`) matches the majority of real-world Claude Code projects, which means the SDK-warm benefit is negated for most sessions when those defaults are in use. Deployers operating SDK-warm pools must monitor `lenny_warmpool_sdk_demotions_total` against `lenny_warmpool_claims_total` to compute the **demotion rate** for each pool.

Guidance by demotion rate:

- **< 20% demotion rate:** SDK-warm is providing meaningful latency savings. No action needed.
- **20–60% demotion rate:** The pool is partially benefiting from SDK-warm. Consider narrowing `sdkWarmBlockingPaths` (e.g., remove patterns that match files your workloads rarely upload) or splitting into two pools — one for workloads known to upload blocking files, one for workloads that do not.
- **> 60% demotion rate (`demotionRateThreshold`):** SDK-warm is providing negligible benefit and the demotion teardown penalty is adding net latency over a plain pod-warm pool. The PoolScalingController emits a `SDKWarmDemotionRateHigh` warning event (rate, pool name, threshold) when the rolling 1-hour demotion rate exceeds this threshold. Operators should either: (a) narrow `sdkWarmBlockingPaths` to exclude commonly uploaded file types, (b) disable SDK-warm for this pool (`capabilities.preConnect: false`), or (c) explicitly acknowledge the rate via the pool config field `sdkWarm.acknowledgeHighDemotionRate: true` to suppress the event.

**Circuit-breaker for SDK-warm pools.** If the rolling 5-minute demotion rate exceeds 90% (hardcoded safety threshold, not operator-configurable), the PoolScalingController automatically disables SDK-warm for the pool by setting `spec.sdkWarmDisabled: true` on the `SandboxWarmPool` CRD (a spec field owned by PoolScalingController per [Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)). The WarmPoolController reads `spec.sdkWarmDisabled` and stops initiating `sdk_connecting` transitions — new pods warm to `idle` in pod-warm mode only, preventing the demotion penalty from dominating all sessions. The gateway emits an audit event (`pool.sdk_warm_circuit_breaker_open`) when this occurs. Operators must re-enable SDK-warm explicitly via `PUT /v1/admin/pools/{name}` with `{"sdkWarm": {"circuitBreakerOverride": "enabled"}}` after narrowing the blocking paths or adjusting the workload profile.

**Existing idle SDK-warm pods are NOT drained on circuit-breaker activation.** Pods already sitting in `idle` state with a live pre-connected SDK process completed SDK initialization successfully — they are known-good. Draining them would waste functional warm capacity and create a pool gap lasting 30–90s while replacements reach `idle` state, exactly when the pool is already under stress. Instead, these pods remain claimable and are served through the normal demotion path: the gateway sets `requiresDemotion: true` at claim time and the adapter calls `DemoteSDK` before workspace materialization. The demotion penalty is the same as it was before the circuit tripped. The pool transitions gradually from SDK-warm to pod-warm as existing idle pods are claimed and replaced; the mixed-state window closes within one pod cert lifetime (4h by default). This is intentional: the circuit breaker stops accumulating more SDK-warm inventory; it does not discard inventory that is already available.

**Adapter SIGTERM behavior during `sdk_connecting`.** A pod in `sdk_connecting` state may receive SIGTERM at any time — for example, due to node eviction, a voluntary node drain, or the warm pool controller scaling down surplus pods. The adapter must handle SIGTERM during `sdk_connecting` as follows: (1) call `DemoteSDK` internally with a bounded timeout (default: 5s, configurable via `LENNY_DEMOTE_TIMEOUT_SECONDS`) to cleanly tear down the in-progress SDK connection attempt; (2) if `DemoteSDK` does not complete within the timeout, force-terminate the SDK process; (3) exit the adapter process. The pod then transitions to `terminated` (not `failed`) because the SIGTERM is an expected lifecycle signal, not a defect. The `terminationGracePeriodSeconds` on the pod spec must be set to at least `LENNY_DEMOTE_TIMEOUT_SECONDS + 5s` to give the adapter time to complete this sequence before Kubernetes sends SIGKILL. This prevents the SDK process from being abandoned mid-connection and leaking credentials or holding LLM provider connections open.

**`sdk_connecting` watchdog.** A pod that hangs in `sdk_connecting` state (SDK process alive but not completing its connection establishment) holds a warm pool slot indefinitely while appearing available. The WarmPoolController applies a per-pod timeout: `sdkConnectTimeoutSeconds` (default: 60s, configurable per pool in the `scalingPolicy` block). If the SDK does not complete its connection and transition to `idle` within this timeout, the WarmPoolController transitions the pod to `failed` and increments `lenny_warmpool_sdk_connect_timeout_total` (counter, labeled by `pool`). Alert `SDKConnectTimeout` (Warning) fires when this counter rate exceeds 0.1/min for > 5 min on the same pool, indicating systematic SDK warm startup issues.

**`preConnect` compatibility with execution modes.** SDK-warm mode (`preConnect: true`) interacts differently with each execution mode. The following table defines the compatibility and behavioral semantics:

| Execution mode | `preConnect` | Behavior |
|---|---|---|
| `session` | Supported (primary target) | SDK process pre-connected once during warm phase. Demotion via `sdkWarmBlockingPaths` evaluated at claim time. Pod is exclusive to the session — no between-task re-warm needed. |
| `task` | Supported | SDK process pre-connected once during warm phase (same as session mode). Between tasks: after scrub completes and the adapter sends `task_ready`, the adapter re-establishes SDK-warm state by calling the SDK connect sequence again — the SDK process is terminated during Lenny scrub step 1 (`kill -9 -1` as sandbox user) along with all other task processes. The `sdkWarmBlockingPaths` check is evaluated **per task** at dispatch time (each task may upload different files); if any file in the new task's workspace plan matches a blocking pattern, the gateway sets `requiresDemotion: true` on the task's `ClaimOpts` and the adapter calls `DemoteSDK` before materializing that task's workspace, then proceeds via the pod-warm path for that task only. The SDK is re-warmed after the next scrub. The per-task demotion rate contributes to the same circuit-breaker threshold ([Section 6.1](#61-what-a-pre-warmed-pod-looks-like)). |
| `concurrent` (`workspace`) | Not supported | Concurrent-workspace mode multiplexes multiple simultaneous tasks onto a single pod via `slotId`. The SDK-warm model assumes a single agent process waiting for a single first prompt, which is incompatible with slot-level multiplexing where each slot requires independent workspace materialization and independent demotion decisions. The pool controller rejects pool definitions that combine `executionMode: concurrent`, `concurrencyStyle: workspace`, and `capabilities.preConnect: true` at validation time with error: `"preConnect: true is not supported with executionMode: concurrent, concurrencyStyle: workspace; concurrent-workspace mode requires independent per-slot agent initialization"`. |
| `concurrent` (`stateless`) | Not supported | Concurrent-stateless mode routes through a Kubernetes Service with no Lenny-managed workspace or session lifecycle. The warm pool controller does not manage SDK-warm state for stateless pods. The pool controller rejects pool definitions that combine `executionMode: concurrent`, `concurrencyStyle: stateless`, and `capabilities.preConnect: true` at validation time with error: `"preConnect: true is not supported with executionMode: concurrent, concurrencyStyle: stateless; stateless mode has no Lenny-managed agent lifecycle"`. |

### 6.2 Pod State Machine

```
Pod-warm path:
  warming ──→ idle ──→ claimed ──→ receiving_uploads ──→ finalizing_workspace
                                                                │
                                                                ▼
                           attached ←── starting_session ←── running_setup

SDK-warm path (preConnect: true):
  warming ──→ sdk_connecting ──→ idle ──→ claimed ──→ receiving_uploads
                                                           │
                                                           ▼
                           attached ←── finalizing_workspace ──→ running_setup

Pre-attached failure transitions:
  warming ──→ failed
  sdk_connecting ──→ failed
  sdk_connecting ──→ terminated   (SIGTERM received: DemoteSDK called with timeout, then process exits)
  receiving_uploads ──→ failed
  running_setup ──→ failed
  finalizing_workspace ──→ failed
  starting_session ──→ failed

Session state transitions (from attached):
                            attached
                            │
                    ┌───────┼───────────────┬────────────────┬──────────┐
                    ▼       ▼               ▼                ▼          ▼
               completed   failed    resume_pending     suspended   cancelled
                                         │                   │
                                    ┌────┤              ┌────┼────────┐
                                    ▼    ▼              ▼    ▼        ▼
                               resuming  awaiting    running completed resume_pending
                                  │       _client     (resume)        (pod failure
                                  ▼       _action                      while suspended)
                               attached     │
                                            ▼
                                          expired

  input_required sub-state of running (pod live, runtime active but blocked in lenny/request_input):
    running ──→ input_required   (runtime calls lenny/request_input)
    input_required ──→ running   (input provided via inReplyTo, request cancelled, or maxRequestInputWaitSeconds timeout fires — gateway delivers REQUEST_INPUT_TIMEOUT tool-call error; see §11.3)
    input_required ──→ cancelled (parent cancels while awaiting input)
    input_required ──→ expired   (session deadline reached while awaiting input)
    input_required ──→ resume_pending (pod crash / gRPC error while awaiting input, retryCount < maxRetries)
    input_required ──→ failed    (pod crash / gRPC error while awaiting input, retries exhausted)

Resuming failure transitions:
  resuming ──→ resume_pending        (pod crash / gRPC error during resume, retryCount < maxRetries)
  resuming ──→ awaiting_client_action (pod crash / gRPC error during resume, retries exhausted)
  resuming ──→ awaiting_client_action (resuming timeout: 300s, retries exhausted)
  resuming ──→ resume_pending        (resuming timeout: 300s, retryCount < maxRetries)

Task-mode state transitions (from attached):
  attached ──→ task_cleanup          (task completes — adapter sends task_complete, runtime replies task_complete_acknowledged)
  attached ──→ cancelled             (cancel signal received — current task terminated)
  attached ──→ failed                (pod crash / node failure / unrecoverable gRPC error during active task)
  attached ──→ resume_pending        (pod crash / gRPC error during active task, retryCount < maxTaskRetries)
  cancelled ──→ task_cleanup         (cancellation acknowledged — pod runs scrub, then proceeds to idle or draining per normal task_cleanup rules)
  task_cleanup ──→ idle              (scrub succeeds, maxTasksPerPod not reached, uptime limit not reached)
  task_cleanup ──→ idle [scrub_warning] (scrub fails, onCleanupFailure: warn, maxScrubFailures not reached)
  task_cleanup ──→ draining          (scrub fails, maxScrubFailures reached)
  task_cleanup ──→ draining          (scrub succeeds, maxTasksPerPod reached)
  task_cleanup ──→ draining          (scrub succeeds, maxPodUptimeSeconds exceeded)
  task_cleanup ──→ sdk_connecting    (preConnect: true, scrub succeeds, maxTasksPerPod not reached, uptime limit not reached, node not draining — re-warm SDK before returning to idle)
  task_cleanup ──→ failed            (onCleanupFailure: fail — pod terminated)
  idle ──→ draining                  (maxPodUptimeSeconds exceeded while idle, checked before next assignment)
  draining ──→ terminated            (pod replacement provisioned from warm pool)

Concurrent-workspace pod state transitions (from idle):
  idle ──→ slot_active               (first slot assigned — atomic Redis INCR succeeds, slotId allocated)
  slot_active ──→ slot_active        (additional slot assigned, active_slots < maxConcurrent)
  slot_active ──→ slot_active        (slot completes or fails — per-slot cleanup runs, active_slots decremented)
  slot_active ──→ idle               (last active slot completes/fails — active_slots reaches 0, pod fully drained of work)
  slot_active ──→ draining           (ceil(maxConcurrent/2) slots fail or leak within 5-min window — pod marked unhealthy)
  slot_active ──→ draining           (maxPodUptimeSeconds exceeded — no new slots accepted, existing slots drain)
  idle ──→ draining                  (maxPodUptimeSeconds exceeded while idle, checked before next assignment)
  draining ──→ terminated            (all remaining slots complete/fail, pod replacement provisioned from warm pool)

  Per-slot sub-states (tracked per slotId, not as pod-level phase):
    slot_assigned ──→ receiving_uploads   (workspace materialization begins for this slot)
    receiving_uploads ──→ running         (workspace ready, task dispatched to runtime with slotId)
    running ──→ slot_cleanup              (task completes or fails)
    running ──→ failed                    (non-retryable error: OOM, workspace validation, policy rejection)
    slot_cleanup ──→ released             (slot workspace removed, processes killed, slotId released)
    slot_cleanup ──→ leaked               (cleanup timeout exceeded — slot not reclaimed until pod termination)
```

**`leaked` slot semantics.** A slot in the `leaked` state remains counted in `active_slots` (preventing the gateway from over-assigning new slots that would conflict with the leaked slot's unreleased resources). A leaked slot counts toward the `ceil(maxConcurrent/2)` unhealthy threshold defined in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) ("**fail or leak**" whole-pod replacement trigger) — leaked slots are combined with `failed` slots in the rolling 5-minute count, and if `failed_slots + leaked_slots >= ceil(maxConcurrent/2)` within that window, the pod immediately transitions to `draining`. If `leaked_slots` alone reaches `ceil(maxConcurrent/2)` (independent of any `failed` slots), the pod also transitions to `draining` for the same reason. The adapter exposes a `leaked_slots` count in the pod's health metadata (`/healthz` response and `lenny_adapter_leaked_slots` gauge, labeled by `pod_id` and `pool`) for observability.

**Pod crash during active task-mode task.** A pod crash, node failure, or unrecoverable gRPC error can occur while a task-mode pod is `attached` (task in progress). Because task-mode pods run sequential tasks without a persistent session checkpoint (task-mode does not use the session checkpoint mechanism — workspace is scrubbed between tasks), the failure handling is simpler than session-mode recovery:

- **Retry policy:** If `retryCount < maxTaskRetries` (default: `1`, giving 2 total attempts), the gateway transitions the task to `resume_pending`, claims a fresh pod from the warm pool, and re-dispatches the original task from the beginning on the new pod. The retried task receives a fresh workspace — no state from the crashed pod is carried over. This mirrors the pre-attached failure retry policy (see below), extended to cover active-task crashes.
- **Retry exhaustion:** If retries are exhausted or the failure is non-retryable (e.g., workspace validation error, policy rejection), the task transitions to `failed`. The failed pod is released from the pool and terminated (consistent with `task_cleanup ──→ failed` semantics). The gateway returns a structured error to the client.
- **Non-retryable failures:** OOM kills, workspace validation errors, and policy rejections are not retried — the same input is likely to fail again on an identically-provisioned pod.
- **maxTaskRetries** is a `taskPolicy` field (default: `1`). Setting it to `0` disables retries — crashes always fail the task outright.

The `attached → resume_pending` transition is therefore a **retry-on-new-pod path** (task restarts from scratch on a fresh pod), not a session recovery path (no checkpoint replay). The distinction is important: `resume_pending` here means "waiting for a new pod to re-run the task", not "restoring session workspace from checkpoint".

**Concurrent-workspace pod lifecycle.** A concurrent-workspace pod has a two-level state model: the **pod-level** state machine tracks the pod's overall availability (above), while **per-slot sub-states** track each individual slot's progress through workspace materialization, execution, and cleanup. The pod-level phase is `slot_active` whenever at least one slot is occupied; the pod returns to `idle` only when all slots have completed or failed and their cleanup has finished. This contrasts with session mode (one session per pod, pod is exclusive) and task mode (sequential tasks, pod cycles between `idle` and `attached`).

- **Pod failure during active slots.** A pod crash, node eviction, or OOM kill while slots are active fails all active slots simultaneously. Each slot transitions to `failed`. The gateway applies the per-slot retry policy ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) independently for each failed slot — retries are dispatched to other available pods in the pool (or new slots on those pods). The failed pod is terminated and a replacement is provisioned from the warm pool.
- **Partial occupancy.** A pod with `active_slots < maxConcurrent` accepts new slot assignments concurrently with running slots. There is no queuing at the pod level — the atomic Redis `INCR` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) gates assignment. The pod label `lenny.dev/state` is `active` whenever `active_slots > 0`. When `active_slots` reaches 0, the gateway defers the label transition to `idle` by a stabilization delay of 5 seconds — if a new slot is assigned within that window, the label remains `active` and no pod `PATCH` is issued. This prevents high-frequency label oscillation on pods near capacity with rapid slot turnover (e.g., `maxConcurrent: 8` with short-lived tasks), which would otherwise generate unnecessary API server write churn at scale. The stabilization delay applies only to the `active → idle` label transition on concurrent-workspace pods; session-mode and task-mode pods transition labels immediately because their slot lifecycles do not overlap.
- **Draining behavior.** When a pod enters `draining` (unhealthy threshold or uptime limit), no new slots are assigned. Existing slots run to completion (or failure) with their normal retry policy. The pod transitions to `terminated` only after all remaining slots have completed or failed and their cleanup has finished. The `terminationGracePeriodSeconds` must accommodate the worst case of `maxConcurrent` slots all completing near the drain deadline — the CRD validation webhook enforces this ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

**`input_required` sub-state:** `input_required` is a sub-state of `running` at the pod level — the pod is live and the runtime process is active, but the agent is blocked inside a `lenny/request_input` tool call awaiting a response. The pod is NOT released or suspended. Because `input_required` is a sub-state of `running` where the pod is live, all failure transitions defined for `running` also apply — including pod crash and gRPC error transitions (`resume_pending` if `retryCount < maxRetries`, `failed` if retries exhausted). Transitions:

```
running → input_required   (runtime calls lenny/request_input)
input_required → running   (input provided via inReplyTo, or request cancelled/expired)
input_required → cancelled (parent cancels while awaiting input)
input_required → expired   (session deadline reached while awaiting input)
input_required → resume_pending (pod crash / gRPC error while awaiting input, retryCount < maxRetries)
input_required → failed    (pod crash / gRPC error while awaiting input, retries exhausted)
input_required → failed    (BUDGET_KEYS_EXPIRED detected while awaiting input — see §8.3)
```

While in `input_required`, the `maxSessionAge` timer continues running (the session is logically active). This aligns with the canonical task state machine ([Section 8.8](08_recursive-delegation.md#88-taskrecord-and-taskresult-schema)) and the interactive session model ([Section 7.2](07_session-lifecycle.md#72-interactive-session-model)).

**`suspended` state:** `interrupt_request` on the lifecycle channel produces a distinct `suspended` session state:

```
running → suspended   (interrupt_request + interrupt_acknowledged)
running → suspended   (interrupt_request timeout — deadlineMs elapsed without interrupt_acknowledged; adapter forces suspended, RPC returns INTERRUPT_TIMEOUT)
suspended → running   (resume_session — no new content; pod still held)
suspended → running   (POST /v1/sessions/{id}/messages delivery:immediate; pod still held)
suspended → resume_pending (resume_session; pod was released by maxSuspendedPodHoldSeconds)
suspended → resume_pending (POST /v1/sessions/{id}/messages delivery:immediate; pod was released — message held in session inbox, delivered after pod acquisition and workspace restore)
suspended → completed (terminate)
suspended → cancelled (client/parent cancels while suspended)
suspended → expired   (delegation lease perChildMaxAge wall-clock expiry while suspended)
suspended → failed    (BUDGET_KEYS_EXPIRED detected — see §8.3)
suspended → resume_pending (involuntary pod failure/eviction while suspended; pod still held)
```

Pod held (initially), workspace preserved, `maxSessionAge` timer paused while suspended. `interrupt_request` is a standalone lifecycle signal — pause-and-decide with decoupled timing. Distinct from `delivery: "immediate"` in a message, which atomically interrupts and delivers content.

**`interrupt_request` does NOT cascade** to children. Budget/lease expiry does cascade. Runtime decides whether to propagate a received interrupt to its children.

**Graceful pod release during extended suspension (`maxSuspendedPodHoldSeconds`).** When a session has been in `suspended` state for longer than `maxSuspendedPodHoldSeconds` (default: 900s / 15 minutes), the gateway checkpoints the workspace and releases the pod. The effective value is `min(deployment_value, tenant_value)` — the deployer sets a platform-wide ceiling via `gateway.maxSuspendedPodHoldSeconds` (Helm), and tenants may request a lower value via their tenant configuration. The most restrictive wins. Behavior when the timer fires:

1. The gateway initiates a checkpoint (same mechanism as pod-failure recovery in [§7.3](07_session-lifecycle.md#73-retry-and-resume)).
2. **If checkpoint succeeds:** the gateway releases the pod back to the pool, clears the session's pod binding, and emits a `session.pod_released_during_suspension` structured event. The session remains in `suspended` — no state change. The `maxSuspendedPodHoldSeconds` timer stops (it has served its purpose).
3. **If checkpoint fails:** the gateway does NOT release the pod. It emits a `session.suspension_checkpoint_failed` warning event and retries on the next evaluation interval (60s). The pod is held until checkpoint succeeds or the session exits `suspended` by other means (cancel, terminate, `perChildMaxAge` expiry).

Once the pod is released, `resume_session` and `delivery:immediate` transitions route through `resume_pending` instead of going directly to `running` — a new pod must be acquired and the workspace restored from checkpoint. The `maxResumeWindowSeconds` countdown starts only when `resume_pending` is entered, so the human is not racing against a timer while the session sits podless in `suspended`. Messages sent via `delivery:immediate` to a suspended-without-pod session are held in the session inbox ([§7.2](07_session-lifecycle.md#72-interactive-session-model)) and delivered after pod acquisition and workspace restore complete. The standard `resume_pending` inbox handling applies: with `durableInbox: true`, messages remain in the Redis-backed inbox; with `durableInbox: false` (default), the inbox-to-DLQ drain ([§7.2](07_session-lifecycle.md#72-interactive-session-model)) fires atomically with the state transition, moving messages to the Redis DLQ.

**Interaction with other timers during podless suspension:** `maxSessionAge` remains paused (with or without pod). `maxIdleTimeSeconds` remains paused. `perChildMaxAge` (wall-clock) continues ticking — if it fires while suspended-without-pod, the session transitions directly to `expired` (no pod to release; checkpoint already happened). The orphan session reconciler skips sessions in `suspended` state with no pod binding.

**Pod failure while `suspended` (pod still held):** A pod eviction, node drain, or runtime crash can occur while a session is in the `suspended` state and the pod is still held (i.e., before `maxSuspendedPodHoldSeconds` fires). When the gateway detects pod failure for a `suspended` session, the session transitions to `resume_pending` and follows the standard retry-and-resume path ([Section 7.3](07_session-lifecycle.md#73-retry-and-resume)). On successful recovery (`resuming → attached`), the session transitions to `running` — not back to `suspended` — because the interrupt context that caused the original suspension cannot be recovered from a checkpoint. The loss of interrupt context is an expected limitation: the session and workspace are recoverable, but the suspended-state semantics (paused agent, pending client decision) are not preserved across pod failure. The `maxSessionAge` timer, which was paused during `suspended`, resumes when the recovered session enters `running`. Pod failure while suspended-without-pod is impossible — there is no pod to fail.

**`resuming` failure transitions:** A pod crash, gRPC error, or workspace-restoration hang while the gateway is restoring a session onto a new pod must not leave the session permanently stuck in `resuming`. The gateway applies the following transitions:

- **Pod crash or unrecoverable gRPC error during `resuming`:** If `retryCount < maxRetries`, transition to `resume_pending` and begin another retry attempt. If retries are exhausted, transition directly to `awaiting_client_action`.
- **`resuming` timeout (300s):** The gateway sets a 300-second watchdog when entering `resuming` (matching the setup-command total timeout). If the watchdog fires, the same retry-count branching applies: `resume_pending` if retries remain, `awaiting_client_action` if exhausted.
- **Non-retryable errors during `resuming`** (e.g., workspace checkpoint corrupt, policy rejection): transition directly to `awaiting_client_action` regardless of retry count.

This ensures `resuming` has the same failure coverage as every pre-attached pod state and cannot become a deadlock sink.

**`maxSessionAge` timer behavior across states:** The `maxSessionAge` timer measures elapsed wall-clock time during which the session is actively making progress or available for interaction. Its behavior is explicitly defined per state:

| State                    | Timer behavior                                                                                                                                                                  |
| ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `finalizing`             | **Bounded by dedicated watchdog.** `maxSessionAge` does not run during `finalizing` — the agent has not started and no productive session time is consumed. Instead, a dedicated `maxFinalizingTimeoutSeconds` wall-clock watchdog (default: 600s) starts when the session enters `finalizing`. If the watchdog fires before the session reaches `ready`, the session is transitioned to `failed` with reason `FINALIZE_TIMEOUT`. The setup-command timeout (300s, `runtime.setupTimeoutSeconds`) provides an inner bound on the setup-command phase within `finalizing`; `maxFinalizingTimeoutSeconds` is the outer bound covering both workspace materialization and setup commands. `maxFinalizingTimeoutSeconds` must be ≥ `setupTimeoutSeconds`; the gateway rejects configurations that violate this constraint. |
| `ready`                  | **Bounded by dedicated watchdog.** `maxSessionAge` does not run during `ready` — the agent has not started. A dedicated `maxReadyTimeoutSeconds` wall-clock watchdog (default: 300s) starts when the session enters `ready`. If the client does not call `POST /v1/sessions/{id}/start` before the watchdog fires, the session transitions to `failed` with reason `READY_TIMEOUT`. This prevents sessions from accumulating in `ready` and holding pre-warmed pods indefinitely when clients abandon the setup flow. |
| `running`                | **Running.** The session is active; elapsed time counts toward `maxSessionAge`.                                                                                                 |
| `input_required`         | **Running.** Sub-state of `running` — pod is live, runtime is active but blocked in `lenny/request_input`. The session is logically active and elapsed time counts toward `maxSessionAge`. |
| `starting`               | **Bounded by dedicated watchdog.** A dedicated `maxStartingTimeoutSeconds` wall-clock watchdog (default: 120s, configurable via `gateway.maxStartingTimeoutSeconds`) starts when the session enters `starting`. If the agent runtime does not reach `running` within this window, the session is transitioned to `failed` with reason `STARTING_TIMEOUT`. `maxSessionAge` elapsed time also counts during `starting` as a secondary bound, but `maxStartingTimeoutSeconds` provides the primary, tighter watchdog. |
| `suspended`              | **Paused.** The agent is deliberately halted by `interrupt_request`. Elapsed time during suspension does not count toward `maxSessionAge`. Timer resumes on `running` entry.     |
| `resume_pending`         | **Paused.** The session is waiting for a pod to become available for recovery. Waiting time does not count; the session cannot make progress. Timer resumes when `running` is entered after successful recovery. A separate `maxResumeWindowSeconds` wall-clock timer starts on entry; if it fires before a pod is allocated, the session transitions to `awaiting_client_action`. |
| `resuming`               | **Paused.** The session is being restored onto a new pod. Restoration time does not count. Timer resumes on `running` entry.                                                     |
| `awaiting_client_action` | **Paused.** The session is blocked pending a human or automated client decision. Wait time does not count. Timer resumes on `running` entry after the client resumes the session. |
| Terminal states (`completed`, `failed`, `cancelled`, `expired`) | **Stopped.** Timer is no longer evaluated.                                                                                                                     |

The gateway persists the `accumulated_session_age_seconds` value in Postgres on every state transition that pauses or resumes the timer. On timer evaluation (checked periodically and on every state transition into `running`), the gateway computes `accumulated_session_age_seconds + elapsed_since_last_resume`. If this exceeds `maxSessionAge`, the session is terminated and transitions to `expired`. This ensures that recovery states — which may last hundreds of seconds — do not silently consume a session's age budget without the agent having done any productive work.

**`maxIdleTimeSeconds` timer behavior across states.** "Idle" is defined as no qualifying activity since `last_agent_activity_at`. The `last_agent_activity_at` timestamp is updated in Postgres on each qualifying event. Qualifying events:

- `agent_output` or `tool_use` events from the adapter (all delivery modes).
- `lenny/await_children` invocation and each partial result received from the `await_children` stream (including `input_required` events and terminal child results). This ensures that a parent session actively blocked in `await_children` is not falsely expired as idle.
- **Proxy mode (`deliveryMode: proxy`):** Each upstream SSE chunk (or equivalent partial response frame) received from the LLM provider and proxied through the LLM Proxy to the pod. This ensures that a long-running LLM call that streams tokens over an extended period is not mistaken for idle — each proxied chunk is direct evidence of active work. The gateway updates `last_agent_activity_at` in-memory on each chunk and flushes to Postgres at most once per second (coalescing rapid chunk arrivals) to avoid write amplification. The LLM Proxy already processes each chunk for token counting ([§4.9](04_system-components.md#49-credential-leasing-service)); the idle-timer reset piggybacks on this existing per-chunk code path.
- **Direct mode (`deliveryMode: direct`):** Each `ReportUsage` gRPC call received from the adapter. In direct mode the gateway has no per-chunk LLM visibility, so `ReportUsage` is the best available signal. These calls are periodic (interval determined by the adapter's reporting configuration), so there is a gap between the last LLM activity and the next `ReportUsage` — the idle timer window should account for this by being at least 2× the `ReportUsage` interval. This is a weaker signal than proxy-mode per-chunk resets, but it narrows the false-idle window from `maxIdleTimeSeconds` to the `ReportUsage` interval.

| State                    | Timer behavior                                                                                                                                                       |
| ------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `running`                | **Active.** Resets on every qualifying event (see list above). Fires `expired` if elapsed time since `last_agent_activity_at` exceeds `maxIdleTimeSeconds`.         |
| `input_required`         | **Paused.** Agent is blocked in `lenny/request_input`, not idle — timer does not advance.                                                                            |
| `suspended`              | **Paused.** Agent is deliberately halted. Timer does not advance.                                                                                                    |
| `resume_pending`         | **Paused.** No agent activity possible while waiting for a pod. `maxResumeWindowSeconds` wall-clock timer applies (see above).                                       |
| `resuming`               | **Paused.** Session being restored; no agent activity yet.                                                                                                           |
| `awaiting_client_action` | **Paused.** Agent is not running; timer does not advance.                                                                                                            |
| Terminal states          | **Stopped.** Timer no longer evaluated.                                                                                                                              |

The `maxIdleTimeSeconds` timer fires the `expired` transition independently of `maxSessionAge`. The first timer to fire wins. `maxIdleTimeSeconds` is a pool-level field in `RuntimeDefinition` `limits:` block (default: 600s, per [Section 17.8](17_deployment-topology.md#178-capacity-planning-and-defaults) operational defaults table). **Origin-scoped override:** sessions minted with the `origin: "playground"` JWT claim are subject to a tighter hard cap — the gateway enforces `min(runtime.limits.maxIdleTimeSeconds, playground.maxIdleTimeSeconds)` (default 300s) for these sessions to bound reclamation time after a best-effort browser-close cancel. See [§27.6](27_web-playground.md#276-session-lifecycle-and-cleanup).

**`resume_pending` wall-clock cap:** Although `maxSessionAge` and `maxIdleTimeSeconds` are both paused during `resume_pending` (the session cannot make progress), an indefinite wait is not acceptable — pool exhaustion, a misconfigured pool, or a scheduler bug could otherwise leave the session permanently stuck. The gateway starts a dedicated wall-clock timer when entering `resume_pending`. The timer duration is `maxResumeWindowSeconds` (same field that governs `awaiting_client_action` expiry; default: 900s). If a pod is allocated before the timer fires, the timer is cancelled and the session proceeds to `resuming`. If the timer fires first, the session transitions to `awaiting_client_action`, where the client can inspect the session, wait longer, or take another action. The transition fires `session.awaiting_action` webhook ([Section 14](14_workspace-plan-schema.md)) so clients are notified without polling. This reuses existing `awaiting_client_action` infrastructure — no additional expiry path is required.

**`suspended → expired` trigger mechanism:** Both `maxSessionAge` and `maxIdleTimeSeconds` are paused during `suspended`, so neither can trigger the `expired` transition from that state. The trigger is the delegation lease's `perChildMaxAge` ([Section 8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)), which is a **wall-clock** deadline that is **not paused** during suspension. When a delegation child session is suspended and `perChildMaxAge` elapses, the gateway transitions the session to `expired`. This transition is therefore only reachable for delegation child sessions; root sessions in `suspended` cannot reach `expired` (they must first resume or be cancelled/completed). A root session in `suspended` can remain indefinitely — after `maxSuspendedPodHoldSeconds` fires, the pod is released but the session stays in `suspended` (podless) until the client acts. The only resource cost of a podless suspended session is the session row in Postgres and (if it has a delegation tree) the budget keys in Redis — both negligible. The `BUDGET_KEYS_EXPIRED` safety mechanism ([§8.3](08_recursive-delegation.md#83-delegation-policy-and-lease)) provides the final backstop: if the budget key TTL fires while the session is still suspended, the tree is cleaned up and the session transitions to `failed`.

**Pre-attached failure retry policy:** Failures in any state before `attached` trigger automatic retry by the gateway. The pod is marked `failed` and released back to the pool (or terminated if unhealthy). The gateway re-claims a new pod and replays the setup sequence from the beginning. Policy:

- **Max retries:** 2 (3 total attempts including the original)
- **Backoff:** Exponential — 500ms, 1s between retries
- **Scope:** Retries apply per client request, not per pod. Each retry claims a fresh pod.
- **Non-retryable failures:** Upload validation errors and policy rejections (e.g., disallowed setup commands) are returned to the client immediately without retry.
- **Exhaustion:** After retry exhaustion, the gateway returns an error to the client with a correlation ID for debugging.

**State storage:** The authoritative state machine lives in the `Sandbox` CRD `.status.phase` and `.status.conditions` fields, backed by Postgres via the controller. **Pod labels are used only for coarse operational states** needed by selectors and monitoring:

| Label               | Values                       | Purpose                                                       |
| ------------------- | ---------------------------- | ------------------------------------------------------------- |
| `lenny.dev/state`   | `idle`, `active`, `draining` | Coarse state for kubectl, monitoring, NetworkPolicy selectors |
| `lenny.dev/pool`    | pool name                    | Pool membership                                               |
| `lenny.dev/runtime` | runtime name                 | Runtime type                                                  |

This avoids the 8-10 label mutations per session that would stress the API server at scale. Detailed state transitions (e.g., `receiving_uploads` → `finalizing_workspace`) are tracked in the CRD status subresource only.

### 6.3 Startup Latency Analysis

**Saved by pre-warming (removed from hot path):**

- Pod scheduling and container creation
- Image pull
- Runtime sandbox initialization
- Volume mounting
- Runtime adapter/binary boot
- Health/readiness checks

**Still on hot path (pod-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- Agent session start (depends on runtime)
- First prompt / first token

**Still on hot path (SDK-warm):**

- Pod claim and routing (~ms)
- File upload and workspace materialization (depends on payload size)
- Setup commands (depends on commands)
- First prompt / first token (session start is already done)

**Estimated latency savings (targets, not benchmarks — to be validated by startup benchmark harness, see Phase 2):**

- runc with cached image: ~1–3s
- gVisor: ~2–5s
- Kata: ~3–8s
- Cold image pulls: +5–30s avoided

**Startup latency SLO target:** P95 pod-warm session start (pod claim through agent session ready) < 2s for runc, < 5s for gVisor, **excluding client file upload and workspace materialization time**. "Client file upload" is the client→gateway transfer (not platform-controlled; depends on client network); "workspace materialization" is the gateway→pod delivery phase, excluded because its duration depends on payload size (variable per request) and is not representative of the steady-state pod-readiness path. This SLO therefore measures a narrow, payload-independent subset of the full startup sequence. The broader platform-controlled envelope — including workspace materialization — is tracked by the indicative per-phase latency budget below (Total: ≤ 6s runc / ≤ 9s gVisor); the 2s / 5s SLO is intentionally stricter than that total. See [Section 16.5](16_observability.md#165-alerting-rules-and-slos) for the SLO definition and [Section 16.1](16_observability.md#161-metrics) for the `lenny_session_startup_duration_seconds` metric boundary.

**SDK-warm savings depend on demotion rate.** The SDK-warm latency savings above (elimination of agent session start time) are only realized for sessions that are **not** demoted. A session that triggers demotion (workspace includes a `sdkWarmBlockingPaths` match) incurs pod-warm latency plus an additional SDK teardown penalty (typically 1–3s). Deployers must track `lenny_warmpool_sdk_demotions_total / lenny_warmpool_claims_total` per pool to verify that SDK-warm is delivering net benefit. See the "Demotion rate threshold and circuit-breaker" guidance in [Section 6.1](#61-what-a-pre-warmed-pod-looks-like) for operator actions when demotion rates are high.

**Per-phase latency budget (indicative targets, to be validated by Phase 2 benchmark harness):**

The TTFT SLO is P95 < 10s (from session start request to first streaming event, [Section 16.5](16_observability.md#165-alerting-rules-and-slos)). The following table allocates that budget across hot-path phases. Setup commands are deployer-controlled and are excluded from the platform-managed budget; deployers should target ≤ 3s to preserve the overall TTFT SLO.

| Phase                                          | Who controls     | Indicative P95 target                               | Notes                                                                                                                                                                                                                                                  |
| ---------------------------------------------- | ---------------- | --------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Pod claim and routing                          | Platform         | ≤ 100ms                                             | Pool lookup and pod assignment                                                                                                                                                                                                                         |
| Credential assignment                          | Platform         | ≤ 100ms                                             | Secret injection via gateway-mediated delivery                                                                                                                                                                                                         |
| Workspace materialization                      | Platform         | ≤ 1s (small, ≤ 1MB) / ≤ 3s (large, ≤ 50MB)          | File delivery over internal network; excluded from pod-warm SLO                                                                                                                                                                                        |
| Setup commands                                 | Deployer         | ≤ 3s (runc recommended) / ≤ 1s (gVisor recommended) | Excluded from platform budget; deployer must size accordingly                                                                                                                                                                                          |
| Agent session start                            | Platform         | ≤ 1.5s (runc) / ≤ 4.5s (gVisor)                     | Covered by pod-warm SLO (2s / 5s); includes any remaining setup in adapter                                                                                                                                                                             |
| First prompt dispatch + first token            | Platform + model | ≤ 1s platform overhead                              | Model TTFP is runtime-dependent and excluded from platform budget                                                                                                                                                                                      |
| **Total (platform-controlled, no setup cmds)** |                  | **≤ 6s (runc) / ≤ 9s (gVisor)**                     | Indicative total of all platform-controlled phases above, **including workspace materialization**. This is broader than the pod-warm startup latency SLO (P95 < 2s runc / < 5s gVisor), which excludes client file upload and workspace materialization — the SLO measures a narrow, payload-independent subset of this envelope. Leaves ≤ 4s (runc) / ≤ 1s (gVisor) for deployer setup commands within the 10s TTFT SLO. gVisor deployments with >1s setup commands will exceed the 10s TTFT SLO; deployers should either minimize setup time or accept a relaxed SLO for gVisor pools. |

> These are indicative planning targets, not hard requirements. Actual values depend on cluster conditions, file payload size, and runtime behaviour. All phases must be validated by the Phase 2 startup benchmark harness before targets are promoted to SLOs.

**Tier 2 promotion gate:** Promotion from Tier 1 to Tier 2 is **blocked** until the Phase 2 startup benchmark harness has produced validated P50/P95/P99 measurements for all hot-path phases across all supported runtime classes (runc, gVisor, Kata). The gate is satisfied when: (a) actual P95 pod-warm session start latency has been measured and is ≤ the targets above for runc and gVisor, and (b) the per-phase histogram metrics (`lenny_session_startup_phase_duration_seconds{phase, runtime_class}`) are fully instrumented and producing data in the benchmark environment, and (c) the benchmark results are recorded as an annotated benchmark run (benchmark run ID, cluster configuration, date) and attached to the Phase 2 exit gate ADR. Until these conditions are met, the latency budget table above is planning guidance only and MUST NOT be used as an SLO in any capacity agreement or customer-facing documentation.

**Per-phase measurement requirement:** Each hot-path phase (pod claim, credential assignment, workspace materialization, setup commands, agent session start, first prompt dispatch) must be independently instrumented with histogram metrics (`lenny_session_startup_phase_duration_seconds{phase, runtime_class}`). The startup benchmark harness (Phase 2) must measure pod-warm vs SDK-warm latency per runtime class to validate the complexity tradeoff of the SDK-warm model.

### 6.4 Pod Filesystem Layout

```
/workspace/
  current/      # Agent's actual cwd — populated during workspace finalization
  staging/      # Upload staging area — files land here first
/sessions/      # Session files (e.g., conversation logs, runtime state)        [tmpfs]
/artifacts/     # Logs, outputs, checkpoints
/tmp/           # tmpfs writable area                        [tmpfs]
```

**Concurrent-workspace per-slot layout.** In `concurrent` mode with `concurrencyStyle: workspace`, the single `/workspace/current` layout above does not apply. Instead, the adapter creates a per-slot directory tree under `/workspace/slots/`:

```
/workspace/
  slots/
    {slotId}/
      current/    # This slot's cwd — populated during per-slot workspace finalization
      staging/    # Per-slot upload staging area
  shared/         # Optional read-only shared assets (populated once at pod start, read-only mount)
/sessions/
  {slotId}/       # Per-slot session files (conversation logs, runtime state)    [tmpfs]
/artifacts/
  {slotId}/       # Per-slot logs, outputs, checkpoints
/tmp/             # tmpfs writable area (shared across slots)                    [tmpfs]
```

**Responsibility split:**

- **Adapter** — creates and removes per-slot directory trees (`/workspace/slots/{slotId}/`, `/sessions/{slotId}/`, `/artifacts/{slotId}/`). The adapter creates the slot directory on `slotId` assignment and removes it during slot cleanup ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The adapter sets each slot's `cwd` to `/workspace/slots/{slotId}/current/` when dispatching a task to the runtime.
- **Runtime** — derives `cwd` per slot from `slotId` using the pattern `/workspace/slots/{slotId}/current/`. The runtime MUST NOT assume a global `/workspace/current` path in concurrent-workspace mode. All file operations use the slot-derived `cwd` for the corresponding `slotId`.
- **Gateway** — addresses per-slot workspace finalization and checkpoint export using the `slotId`-qualified paths. `FinalizeWorkspace` materializes files from `/workspace/slots/{slotId}/staging/` to `/workspace/slots/{slotId}/current/`. Checkpoint export ([Section 4.4](04_system-components.md#44-event--checkpoint-store)) targets `/workspace/slots/{slotId}/current/` for the specific slot.

Session mode and task mode continue to use the base layout (`/workspace/current`).

**`/workspace/shared/` population and enforcement.** The `/workspace/shared/` directory in concurrent-workspace mode is populated by the gateway during pod initialization (before any slot is assigned) from the Runtime's `sharedAssets` configuration — a list of artifact references or inline file specs. Once populated, `/workspace/shared/` is mounted read-only at the container level: the pod spec uses a separate `emptyDir` volume for `/workspace/shared/` with a `readOnly: true` volumeMount on the runtime container. This enforces immutability at the kernel level — any write attempt by the runtime process returns `EROFS` (read-only filesystem). The adapter does not create or modify files under `/workspace/shared/` after initial population. If no `sharedAssets` are configured on the Runtime, the `/workspace/shared/` directory is still mounted (empty, read-only) to prevent runtimes from using it as writable scratch space.

**Data-at-rest protection:**

- `/sessions/` and `/tmp/` use `emptyDir.medium: Memory` (tmpfs) — data is guaranteed gone when the pod terminates. tmpfs usage counts against pod memory limits and must be accounted for in resource requests. Resource class definitions ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) must account for tmpfs usage in memory requests. For example, if a pod's memory limit is 2Gi and tmpfs usage can reach 500Mi, the effective memory available to the agent process is 1.5Gi. Deployers should set `emptyDir.sizeLimit` on tmpfs volumes to cap usage and provide predictable OOM boundaries rather than silent memory pressure. Recommended size limits: `/sessions/` sizeLimit of 256Mi (session transcripts), `/tmp/` sizeLimit of 256Mi (scratch space).
- `/workspace/` and `/artifacts/` use disk-backed emptyDir. Node-level disk encryption (LUKS/dm-crypt or cloud-provider encrypted volumes) is **required** for production deployments. **T4 workspace limitation:** For tenants with `workspaceTier: T4`, the data classification table ([Section 12.9](12_storage-architecture.md#129-data-classification)) requires envelope encryption via KMS. Node-level disk encryption satisfies this for data at rest on the node's physical volume, but does not provide per-tenant key isolation for in-flight workspace files on emptyDir. Application-layer encryption of individual file writes on emptyDir is impractical without a FUSE-based encrypted filesystem, which conflicts with simplicity and performance goals. The T4 envelope encryption requirement is fully enforced for durable workspace data (MinIO snapshots use SSE-KMS with tenant-scoped keys per [Section 12.5](12_storage-architecture.md#125-artifact-store)). For ephemeral in-flight workspace data, the combined controls of node-level disk encryption + pod isolation (gVisor/Kata RuntimeClass) + one-session-only pod lifecycle + emptyDir cleanup on pod termination provide defense-in-depth. Deployers handling T4 workloads should use Kata or gVisor isolation profiles, which provide per-pod encrypted scratch volumes on supported platforms. Lenny cannot programmatically verify node-level disk encryption — see [Section 17.6](17_deployment-topology.md#176-packaging-and-installation) preflight warning.

  **T4 dedicated-node requirement:** Because node-level disk encryption does not provide per-tenant key isolation, a node compromise exposes all T4 tenants co-located on that node simultaneously. To bound this blast radius, **T4 workloads MUST run on dedicated node pools** — nodes that carry no non-T4 agent pods. The following controls enforce this:

  1. **Pool `nodeSelector`:** Every pool whose associated Runtime has `workspaceTier: T4` MUST specify a `nodeSelector` entry in its `SandboxTemplate` pod spec matching a deployer-provisioned label (e.g., `lenny.dev/workspace-tier: t4`). The pool controller rejects pool creation or update requests that reference a T4 Runtime without this selector. T4 nodes MUST additionally carry the taint `lenny.dev/workspace-tier=t4:NoSchedule` so that non-T4 pods cannot land on T4 nodes even if they accidentally omit the selector.
  2. **Validating admission webhook:** A `ValidatingAdmissionWebhook` (`lenny-t4-node-isolation`) is deployed as part of the Lenny Helm chart and configured with `failurePolicy: Fail`. It intercepts `CREATE` and `UPDATE` operations on `Pod` resources in agent namespaces. For any pod whose associated Runtime is T4 (detected via the `lenny.dev/workspace-tier: t4` label injected by the pool controller), the webhook verifies: (a) the pod's `nodeSelector` or `nodeAffinity` contains a T4 node label, and (b) the pod's `tolerations` include the T4 taint. If either check fails, the webhook rejects the pod with: `"T4 pod missing required nodeSelector/toleration for dedicated T4 node pool (STR-003)"`. The webhook additionally rejects non-T4 pods that carry a T4 node selector or T4 toleration — this prevents accidental non-T4 workloads from scheduling onto T4-dedicated nodes. The preflight check ([Section 17.6](17_deployment-topology.md#176-packaging-and-installation)) verifies that the `lenny-t4-node-isolation` webhook exists and its `caBundle` is non-empty before installation completes.
- `/dev/shm` is limited to 64MB. `procfs` and `sysfs` are masked/read-only. `shareProcessNamespace: false` when using sidecar containers. `shareProcessNamespace: false` is not enforceable via Pod Security Standards; a Kyverno or Gatekeeper policy must be deployed to reject pods in agent namespaces that set `shareProcessNamespace: true`, as part of the RuntimeClass-aware admission policies described in [Section 17.2](17_deployment-topology.md#172-namespace-layout).
- Combined with the one-session-only invariant, sensitive data never persists on disk after pod termination.

