# Spec changes: Name who starts the next session's runtime on a recycled pod

## 2. Decisions

**D1. The runtime process lives as long as the pod.** In the sidecar deployment model the runtime process is the one the kubelet started in the runtime container. It dials `CH-MSGSOCK` once; the adapter accepts that connection at the pod's first session start and closes it only in the pod-scope teardown (D10). The adapter does not spawn a process in the runtime container: the containers share no process namespace (§13.1), and the runtime binary exists only in the runtime container's image. In the embedded model the runtime process is the adapter process, which already lives as long as the pod, and `InProcessRuntime` keeps its per-session loop, which ends a session's execution and leaves the process running. No per-session call ends the process, and occupancy zero ends nothing.

**D2. The adapter rule is unconditional, and the gateway decides reuse.** The adapter holds no recycle state (§5.2). On the concurrent path the recycle `Shutdown` follows the last slot's `Close` (`pkg/adapter/session.go`, `answerShutdown`), so a close decided at the last slot cannot know the disposition. Every pool that must not reuse a pod retires it after occupancy zero through the recycle disposition or the session path: a non-recycling pool, `maxSessionsPerPod: 1`, `scrubProfile: vm-restart`, a pool without the process-reuse acknowledgment (D5), a pod whose runtime is reported as not live (D4), and every existing retire trigger. The kubelet ends the runtime with the pod, and no session is resident when that happens. No wire field tells the adapter the pool's acknowledgment or cross-tenant setting.

**D3. Per-session calls do not end the transport.** `SocketRuntimeProcess.Close` and `Interrupt` return nil and change nothing. The active set, `addActiveLocked`, and `releaseActiveLocked` are deleted, because no decision reads them. The sidecar transport has no signal path, so a clean `Interrupt` delivers nothing there, which is its behaviour today whenever a sibling slot is active.

**D4. A stopped runtime is reported at the boundary and refused at the next start.** When the fan-out reader's scan ends, `SocketRuntimeProcess` records a sticky `ended` state. Nothing clears it and nothing redials. After the whole-pod scrub returns, the adapter asks its runtime whether it can serve the next session and sends the answer on the `ReportPodScrub` it already sends, in a new `ReportPodScrubRequest.runtime_live` field. The sample follows the scrub because step 4 clears `/tmp` and `/dev/shm`, which the runtime container shares. Each transport answers from state it already holds: the socket transport reports `connected && !ended`, `InProcessRuntime` reports that no session is bound, `SubprocessExecutor` reports true because it spawns a child per session, and a transport that does not answer (`MCPRuntime` and the test doubles) reads as false. A request that omits the field decodes as false. A false report retires the pod with the non-counting reason `runtime_not_live`, so the pod is never held for a session its runtime cannot serve. A runtime that stops after the report, during the `reserved` hold, is found by the next start: `Start` and `Output` return an error at once when `ended` is set, `StartSession` fails, and the failed-start path drains the pod as SPEC-7(a) states (`pkg/gateway/podlifecycle/podsession/bindlaunch.go:68-79` and `reclaim.go:28-35` on a `maxConcurrentSessions: 1` pool; `pkg/gateway/sessionserver/pod_launch.go:167-169` and `slot_bind.go:183-190` on a concurrent pool).

Two alternatives are rejected. Withholding the report retires the pod through the missing-report timeout, holding it in `recycling` for `cleanupTimeoutSeconds` plus a grace period and recording `scrub_report_timeout` with the `failed` terminal. Reading the runtime container's status from the Pod object lags the kubelet, has no counterpart on an embedded pod, and cannot observe the connection the next `Start` uses.

**D5. Reusing a runtime process requires `sessionPolicy.acknowledgeProcessLevelIsolation: true`.** The field (`pkg/gateway/runtime/runtimestore/runtimestore.go:1024-1028`) names the property the deployer accepts: process-level state one session leaves is visible to another on the same pod. A kept runtime process carries into the next session the state the whole-pod scrub does not reach (D11). The gate applies in both deployment models, because the embedded runtime's process is the adapter process and is kept as well (open decision 2). `podscrub.Decide` evaluates it at the recycle boundary from the pool record the recycle policy resolver already fetches (`pkg/gateway/session/recycle/scrubreporter_seams.go:608`), so a change to the pool takes effect at the pod's next boundary. Admission is unchanged: a recycling pool without the field is admitted, and its pods retire at each occupancy-zero boundary with the non-counting reason `process_reuse_unacknowledged`. A pool with `maxConcurrentSessions > 1` always carries the field (`pkg/gateway/runtime/poolstore/poolstore.go:561-566`), so its runtime is always kept.

**D6. A kept runtime process serves one tenant.** The state is the pod's `lenny.dev/tenant-id` label. The gateway stamps it at first assignment (`pkg/gateway/podlifecycle/podclaim/claimer.go:157`, `slotclaimer.go:723`), and the `lenny-tenant-label-immutability` webhook refuses a change to another tenant (`pkg/admission/label_immutability/label_immutability.go:197-205`). No component writes the `{tenant_id} → unassigned` release (`UnassignedTenantID`, `label_immutability.go:48`, has no writer in `pkg/` or `cmd/`), and SPEC-8 forbids it for a recycled pod, so the pin holds for the pod's life. §5.2 already requires the candidate scan to filter on the pin, and the code does not: the idle scan (`claimer.go:124-159`) and the concurrent-slot idle pass (`slotclaimer.go:486-510`) read no pin, and the Postgres fallback claim (`pkg/gateway/podlifecycle/podsession/fallbackclaim.go:91-133`, over `pkg/agentpodstate/pgstore/pgstore.go:245-279`) reads none and stamps none. A kept runtime turns that gap into cross-tenant exposure of in-memory state, so one helper reads the pin at all three sites before the claim is created and refuses a pod pinned to another tenant. On an `allowCrossTenantReuse` pool the refusal also stamps the pod's drain request (`StampDrainRequest`, `slotclaimer.go:95`), which the WarmPoolController consumes in any phase (`pkg/controller/warmpool/pod_reconciler.go:609`), so the pod retires when a different tenant's session would have been assigned to it. Only an `in-place` cross-tenant pool reaches that drain, because a `vm-restart` pool retires every pod at the boundary and cross-tenant reuse is refused on concurrent pools (`poolstore.go:571`). On every other pool a refused pod stays pinned for its tenant, so the WarmPoolController planner must not count it toward the pool's warm target: today it counts every idle pod (`pkg/controller/warmpool/plan/plan.go:111-113`, `:130`), and once each warm pod on a recycling pool has served some tenant, every other tenant's claim returns `ErrNoIdlePod` while the pool reports full inventory. CODE-10 applies the SPEC-8(c) accounting, so the controller provisions unpinned pods for other tenants and holds pinned idle pods within `maxWarm`, which the namespace quota floor sums (§17).

**D7. The new retire reasons do not count.** §16.1 freezes `lenny_gateway_pod_retirement_total{reason}` to the three limit triggers. `process_reuse_unacknowledged` is a configuration-driven retire, like `vm_restart_reprovision`, and `runtime_not_live` is a state-driven retire, like `host_unschedulable` (`pkg/sandbox/podscrub/podscrub.go:186-226`). Both are recorded in the audit trail and on neither retirement counter, and `applyDisposition` drives `Retire(failed=false)` to `released` for both without change.

**D8. The terminate frame moves from occupancy zero to pod exit.** The §15.4.2 terminate frame tells the runtime to exit, so `tearDownReclaimedSlot` stops sending it and the `boundRemains` result that gated it is deleted. The adapter's SIGTERM handler sends the frame once, with the reason `eviction`, before it closes `CH-RUNTIMEOPS`, so a Full-level runtime still receives the `DRAINING` state the §15.4 integration-level matrix promises (open decision 5).

**D9. The runtime generation counts the sessions given to the process for the pod's life.** `noteRuntimeClosed` removes the session from `runtimeLive` and no longer resets the cohort. `soleSession` names the cohort session only while `runtimeLive` still holds it. This is the rule §4.7 and §28 already state ("refuses the call unless that process has been given exactly one session and that session is the caller") applied to a process that outlives occupancy zero. The pod-global surfaces (intra-pod MCP forwarding, the direct-mode token fold, and the control-event stamp) therefore fail closed on every session after a kept process's first, as they do on a concurrent pod (open decision 1).

**D10. The pod-scope teardown is proposal 0078's `CloseListener`, extended.** It sets `ended`, closes the shared connection, waits `defaultSocketShutdownGrace` for a spawned child and then kills it, and closes the listener. It keeps 0078's name and signature, `CloseListener() error`, so every 0078 caller and test cleanup compiles unchanged. It is idempotent, and `cmd/lenny-adapter` is its only production caller, at process exit.

**D11. The scrub does not reach the kept runtime process.** Step 1's `kill -9 -1` runs as the adapter's UID in the container that runs the scrub. In the sidecar model it cannot reach the runtime container, which has its own process namespace (§13.1) and its own UID (`pkg/controller/sandbox/podspec/podspec.go:56-57`). In the embedded model the runtime is the adapter process, which the kill spares. Step 1b's `ipcrm --all=shm` removes only the segments the adapter's UID owns or created. Steps 3 and 5 act on the adapter's own environment and log buffers.

**D12. The client disclosure keeps its values and widens its stated meaning.** `podReuse` and `residualStateWarning` are already `true` on every pool with `recycle.enabled: true` (`pkg/gateway/sessionserver/isolationlevel.go:205-212`), which covers every pool on which a runtime process can serve more than one session. The §5.2 client-visibility paragraph and the §7.1 `residualStateWarning` row state that a recycled session may run in a runtime process that served an earlier session of the same tenant. A pool without the acknowledgment still reports `true` and over-warns, because the gateway computes the fields from pool configuration before it binds a pod. A new field would change the external API, the OpenAPI document, and the client SDKs to draw a distinction no client has asked for.

**D13. New `Decide` inputs carry retire polarity.** `Inputs.RuntimeNotLive`, `Inputs.ProcessReuseUnacknowledged`, and `PodRecyclePolicy.ProcessReuseUnacknowledged` are true to retire, the polarity `VMRestart` has, so the zero value keeps today's disposition and every existing literal keeps its meaning (`pkg/sandbox/podscrub/podscrub_test.go:14-27`, `tests/tier4_integration/recycle_scrub_path_test.go:473-479`, `tests/tier7a_load_local/scenarios/vm_restart_recycle_disposition/scenario.go:150-156`, and the reuse fakes in `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go`). The fail-closed default sits at the single production assignment of each: `RecordPodScrub` negates the positive wire field, and `InspectForRecycle` negates the pool's acknowledgment.

## 3. Design overview

**The transport.** State and its writers in `SocketRuntimeProcess`, after proposal 0078:

| Field | Set | Cleared |
|:--|:--|:--|
| `conn`, `connected`, `subscribers` | First successful `Start` (accept) | Never inside the pod's life |
| `ended` (new) | `fanOut` when its scan ends, under `p.mu`, before it closes its subscribers; the pod-scope teardown, before it closes the connection | Never |
| `cmd` | `spawn` (tests only) | `killSpawned` on a failed `Start`; the pod-scope teardown |
| `listener` | `NewSocketRuntimeProcess` | The pod-scope teardown |

`Start` returns the ended error when `ended` is set, returns nil when `connected` is set, and otherwise accepts. `Output` returns the ended error when `ended` is set. `ServesNextSession` reports `connected && !ended`. `WriteEnvelope` is unchanged. `Close` and `Interrupt` return nil.

**The generation.** In `Server`, `runtimeLive` gains a session at `noteRuntimeStartedLocked` and loses it at `noteRuntimeClosed`. `runtimeCohort` and `cohortSession` gain at `noteRuntimeStartedLocked` and never reset. `soleSessionLocked` returns `cohortSession` only when `runtimeCohort == 1` and `runtimeLive` holds `cohortSession`.

**The boundary.** `startPodScrub` runs the scrub, computes its outcome, samples `ServesNextSession`, and sends `ReportPodScrub(podID, outcome, runtimeLive, detail)`. The gateway handler passes `runtime_live` to `RecordPodScrub`, which sets `Inputs.RuntimeNotLive` to its negation. `InspectForRecycle` sets `PodRecyclePolicy.ProcessReuseUnacknowledged` from the pool record it already fetches. `Decide` gains two branches after the uptime branch and before the host-schedulability branch:

```go
if in.ProcessReuseUnacknowledged {
    return Disposition{
        Ready: true, NextPhase: state.Draining,
        ScrubWarning: warned, Retire: true,
        Reason: ReasonProcessReuseUnacknowledged,
    }
}
if in.RuntimeNotLive {
    return Disposition{
        Ready: true, NextPhase: state.Draining,
        ScrubWarning: warned, Retire: true,
        Reason: ReasonRuntimeNotLive,
    }
}
```

**The acquisition.** `podclaim.AdmitTenantPin` reads the pin before the claim is created on the idle scan, the concurrent-slot idle pass, and the Postgres fallback claim (D6; non-spec changes §4.4 states the predicate).

**Outcome per pool.** A recycling pool on `standard` or `in-place` with `acknowledgeProcessLevelIsolation: true` and `maxSessionsPerPod > 1` reuses the pod with its runtime for the pinned tenant until a retire trigger fires. A recycling pool without the acknowledgment admits and retires each pod at its first occupancy-zero boundary. A `vm-restart` pool, a `maxSessionsPerPod: 1` pool, and a non-recycling pool retire the pod as they do today, and the kubelet ends the runtime with it.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it lands |
|:--|:--|:--|
| Acknowledged recycling pool at occupancy zero | The connection stays up; the next session's `Start` returns without an accept, and its frames reach the same runtime process | D1, D3, SPEC-2, CODE-1 |
| Recycling pool without the acknowledgment, `maxSessionsPerPod > 1` | Admitted; the pod retires at each occupancy-zero boundary with `process_reuse_unacknowledged`; a sequential pool sizes at `mode_factor = 1.0` | D5, SPEC-7 |
| `maxSessionsPerPod: 1` | The pod retires at its first boundary with the counting `session_count_limit`, as today | D5, NS §4.3 |
| Concurrent pool | Always acknowledged, so the runtime is kept across occupancy zero; the per-release `maxSessionsPerPod` drain is unchanged | D5 |
| `vm-restart` pool on any deployment model | Unchanged: the `VMRestart` branch runs first and the pod retires with `vm_restart_reprovision` | D2, TEST-15 |
| Runtime exits before the boundary report | The report carries `runtime_live: false` and the pod drains with `runtime_not_live`; a pod at a limit keeps that limit's reason | D4, SPEC-7 |
| Runtime exits during the `reserved` hold | The next `StartSession` fails at once; the pod drains (SPEC-7(a)) | D4, SPEC-7 |
| Runtime exits during a session | The session's Attach stream ends at EOF, the session fails, and §5.2 retires the pod | D4 |
| Runtime misses a heartbeat | `Interrupt` delivers nothing, and the Attach stream ends with DeadlineExceeded and the session fails. On a `maxConcurrentSessions: 1` pool §5.2 retires the pod on the failed session. On a concurrent pool the failed slot counts toward the §5.2 whole-pod replacement trigger (Slot retry policy); a hung runtime keeps its connection open, so at the occupancy-zero boundary it reports `runtime_live: true`, and the pod is reused with the hung runtime until that trigger fires | D3, D4, SPEC-2, SPEC-3(c), SPEC-14 |
| A `RuntimeProcess` without `ServesNextSession` | Reports `runtime_live: false`, and the pod retires at each boundary | D4 |
| Adapter receives SIGTERM | One terminate frame on `CH-RUNTIMEOPS`, then the connection and listener close; the kubelet signals the runtime container | D8, D10 |
| Adapter SIGKILLed | The kernel closes the sockets, the runtime observes EOF, and the pod is terminating | D10 |
| Full-level runtime at a session end | No terminate frame arrives; the runtime is told nothing about the session's end | D8; open decision 3 |
| Runtime built on the Go SDK, acknowledged pool | A later session's frames arrive under the first session's `CreateRequest` | Open decision 3 |
| Second session on a kept process | `soleSession` is empty, so intra-pod MCP calls are refused with FailedPrecondition and direct-mode tokens fold to no session, as on a concurrent pod | D9; open decision 1 |
| Window after the cohort session closes and before the next start | `soleSession` is empty, and the pod MCP surface is cancelled | D9, CODE-3 |
| `allowCrossTenantReuse` `in-place` pool, next session from a different tenant | The acquisition path refuses the pod and stamps its drain request, and the session is placed on another pod; a same-tenant session that binds before the drain lands completes while the pod drains | D6, SPEC-8 |
| Pinned pod on a pool without `allowCrossTenantReuse`, claim from a different tenant | Refused and left pinned for its tenant, or drained, as SPEC-8(c) states | D6, CODE-10 |
| Postgres fallback claim | Reads the pin before claiming the mirror row, leaves the row unchanged on a refusal, and stamps the pin on success | D6, CODE-8 |
| Acknowledgment removed while a kept-runtime pod is reserved or idle | A same-tenant session may still bind and run in the kept process; the next boundary reads the updated pool and retires the pod | D5 |
| Embedded recycling pod | The adapter process is kept and the loop runs once per session; the embedded mains wire no `ScrubOps`, so the report is withheld and the pod retires by timeout, which predates this proposal | D1, §9.1 |
| `sessionIsolationLevel` | `podReuse` and `residualStateWarning` are `true`; they over-warn on a pool without the acknowledgment | D12, SPEC-8, SPEC-10 |
| The whole-pod scrub and the kept runtime | The scrub does not reach the process; the residual-state list names it | D11, SPEC-6 |

## 8. Proposed changes

### 8.1 Proposed spec changes

**SPEC-1 — `spec/04_system-components.md` §4.7.9.** Replace `7. Adapter spawns runtime binary` with:

```
7. The runtime process becomes live for the pod's first session. In the sidecar
   deployment model ([§4.7.10](#4710-deployment-model)) the kubelet started the
   runtime container when the pod started; the runtime dials the adapter on
   `CH-MSGSOCK` ([§28.5.3](28_communication-channels.md#2853-intra-pod)), and
   the adapter accepts that connection here. The adapter does not spawn a
   process in the runtime container: the two containers share no process namespace
   ([§13.1](13_security-model.md#131-pod-security)) and the runtime binary exists
   only in the runtime container's image. A later session on the same pod skips
   this step and steps 8 and 9, and uses the connections the runtime opened on
   the pod's first session. In the embedded deployment model the adapter runs
   the runtime loop in its own process for each session.
```

**SPEC-2 — `spec/04_system-components.md` §4.7.10.** Insert before `**Health check:**`:

```
**Runtime process lifetime.** The runtime process lives as long as the pod. In
the sidecar model it is the process the kubelet started in the runtime
container, and its `CH-MSGSOCK` connection, accepted once, serves every session
the pod serves, multiplexed by `sessionId`. No session's teardown, interrupt,
or heartbeat escalation, and no occupancy-zero boundary, closes that connection
or sends the process a signal; the pod's termination ends the process. In the
embedded model the runtime process is the adapter process, which also lives as
long as the pod, and the adapter runs one runtime loop per session inside it. On a pool whose runtime declares `preConnect`, the SDK process, which
[Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) calls
the agent process and which `DemoteSDK` tears down and the SDK re-warm restarts, is
distinct from the runtime process, and neither ends the runtime process. A
runtime process that stops is not re-created inside the pod: the adapter
reports it at the next recycle boundary and refuses the next session's start,
and the pod is retired
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
Whether a later session reuses the process is a pool setting
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
"Deployer acknowledgment (runtime process kept across sessions)").
```

Add a row to the sidecar-versus-embedded trade-off table, after the `Recommended for` row:

```
| Runtime process lifetime | The pod's lifetime; one connection serves every session | The pod's lifetime (the adapter process); one loop per session |
```

**SPEC-3 — `spec/04_system-components.md` §4.7, the adapter RPC table.**

(a) In the `Shutdown` row, replace from `It flushes the session's final usage report and then closes the runtime.` through `since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session.` with this text, on one line inside the cell:

```
It flushes the session's final usage report and then ends the session's use of the pod's runtime process, which stays alive for the pod's life ([Section 4.7.10](#4710-deployment-model)). A session's teardown sends no [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal, because that signal ends the runtime process the pod keeps; the adapter sends it once, with the reason `eviction`, when the pod terminates.
```

(b) In the same row, replace `the adapter keeps the pod process alive across the recycle boundary` with `the adapter keeps its own process and the runtime process alive across the recycle boundary`.

(c) In the `ReportPodScrub` row, insert after its first sentence (ending `on a recycling pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`):

```
The request also states whether the adapter's runtime process can serve the next session, sampled after the scrub finishes; a sidecar runtime process whose connection is open is reported as able to, whether or not it answers heartbeats.
```

and insert after the sentence ending `rather than reserving or re-warming ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`:

```
A pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true` takes the same terminal retire with the non-counting reason `process_reuse_unacknowledged`, and a report that states the runtime cannot serve the next session, or omits that fact, takes it with the non-counting reason `runtime_not_live` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

**SPEC-4 — `spec/04_system-components.md` §4.6.1 and §4.6.3.** Each part extends a `vm-restart` carve-out or the pin scope and cites §5.2 for the rule rather than restating it.

(a) In §4.6.3, the `released` bullet, replace `or the `vm-restart` recycle-boundary reprovision` with `, the `vm-restart` recycle-boundary reprovision, or the recycle-boundary retire of a pod whose pool lacks the process-reuse acknowledgment or whose runtime cannot serve the next session`.

(b) In §4.6.3, the `recycling` bullet, append after the sentence ending `rather than entering the `sdk_connecting` re-warm sub-phase ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`:

```
A pod whose pool does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, or whose scrub report states that its runtime cannot serve the next session, likewise projects `claimed → draining` after its scrub report on any `scrubProfile` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

(c) In §4.6.3, the claim-status paragraph's `rewarmStartedAt` parenthetical, replace `a `vm-restart` preConnect pool retires (draining → released) after its scrub report and records no `rewarmStartedAt` stamp,` with `a `vm-restart` preConnect pool, a preConnect pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and a preConnect pod whose scrub report states that its runtime cannot serve the next session retire (draining → released) after the scrub report and record no `rewarmStartedAt` stamp,`.

(d) In §4.6.3, the **Gateway ServiceAccount RBAC grants:** paragraph, replace `and allows the WarmPoolController SA to reset `{tenant_id} → unassigned` only on a pool whose microvm-gated `recycle.allowCrossTenantReuse` permits cross-tenant reuse ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); on every other pool the pin persists for the pod's lifetime, across the recycle-to-idle edge.` with:

```
and allows the WarmPoolController SA to reset `{tenant_id} → unassigned`. No component performs that reset on a recycled pod: the pin persists for the pod's lifetime on every pool, across the recycle-to-idle edge, and on a pool with microvm-gated `recycle.allowCrossTenantReuse` a pod that a session of a different tenant would reach is drained rather than reused across tenants ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

In the same paragraph, delete ` when a pod crosses the unhealthy threshold`, keeping the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) citation that follows.

(e) In §4.6.1, the occupancy-projection `sdk_connecting` bullet, append after the sentence ending `or the non-preConnect `reserved` hold, on preConnect and non-preConnect pools alike ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`:

```
A pod whose pool does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, or whose scrub report states that its runtime cannot serve the next session, takes the same terminal retire on any `scrubProfile` ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

(f) In §4.6.1, **Reserved hold (claim retention across same-tenant sessions):**, replace `This reserved hold applies to `standard` and `in-place` pools; a `vm-restart` pool retires at the occupancy-zero boundary rather than entering `reserved`` with `This reserved hold applies to `standard` and `in-place` pools; a `vm-restart` pool, a pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and a pod whose scrub report states that its runtime cannot serve the next session retire at the occupancy-zero boundary rather than entering `reserved``, keeping the citation that follows.

(g) In the same paragraph, replace the last sentence, from `The `lenny.dev/tenant-id` pin persists across the recycle-to-idle edge` through `available to that tenant alone.`, with `[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the `lenny.dev/tenant-id` pin rule for a recycled idle pod, including its claim and inventory accounting.`

**SPEC-5 — `spec/05_runtime-registry-and-pool-model.md` §5.1 setup commands, and §5.2 recycle lifecycle and scrub procedure.**

(a) In **Recycle lifecycle (`recycle.enabled: true`)**, insert after the sentence ending `rather than running the SDK re-warm leg or entering `reserved`.`, without rewording it:

```
A pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and a pod whose scrub report states that its runtime cannot serve the next session, likewise retire at the occupancy-zero boundary with a non-counting reason after the session-count and uptime retirements (see the Pod retirement policy below), so the `reserved` hold and the SDK re-warm apply only to a pod that keeps a live runtime process under that acknowledgment.
```

In the same paragraph, replace `per-session setup belongs in the runtime's initialization.` with `per-session setup belongs in the runtime's handling of the session's first message, because a runtime process kept across sessions initializes once per pod.` In §5.1 **Setup Commands and Policy**, replace `Per-task setup belongs in the runtime's initialization.` with `Per-task setup belongs in the runtime's handling of the session's first message ([Section 5.2](#52-pool-configuration-and-execution-modes)).`

(b) Replace the **Recycling and integration levels** paragraph with:

```
**Recycling and integration levels.** Pod recycling requires no CH-RUNTIMEOPS
exchange between sessions: the per-slot cleanup and the whole-pod scrub are
adapter-executed and gateway-coordinated. The runtime process is kept across the
recycle boundary ([Section 4.7.10](04_system-components.md#4710-deployment-model)),
so on a pool that sets `sessionPolicy.acknowledgeProcessLevelIsolation: true` a
runtime serves each later session on the same connection, keyed by the frame's
`sessionId`, as a runtime on a concurrent pool does, and
`recycle.maxSessionsPerPod` bounds the number of sessions one runtime process
serves. A runtime that exits after its session makes the pod retire at the
recycle boundary rather than serve the next session. Recycling is admitted at
every integration level (Basic, Standard, and Full).
```

(c) In **Lenny scrub procedure**, replace `The adapter closes the ending session's runtime, keeps the pod process alive across the recycle boundary,` with `The adapter ends the ending session's use of the runtime, keeps its own process and the runtime process alive across the recycle boundary,`. Replace `On a `standard` or `in-place` pool the pod then keeps the process alive and reuses it for the next session;` with `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process;`.

**SPEC-6 — `spec/05_runtime-registry-and-pool-model.md` §5.2, scrub steps.**

(a) Append to step 1: `The kill runs in the container that executes the scrub and does not end the runtime process; see "What the scrub reaches" below.` In step 1b, replace `Purge all `shmget`-allocated IPC shared memory segments` with `Purge the `shmget`-allocated IPC shared memory segments the adapter's UID owns or created`, leaving the rest of step 1b unchanged.

(b) Insert after step 6 and before the best-effort paragraph:

```
**What the scrub reaches.** The scrub executes in the adapter's process and
reaches what that process can reach. In the sidecar deployment model
([§4.7.10](04_system-components.md#4710-deployment-model)) steps 0, 2, 4, and 6
operate on volumes mounted into both containers, so they clear and verify the
runtime container's view of those paths. Step 1 signals only processes in the
adapter container, because `shareProcessNamespace` is forbidden
([§13.1](13_security-model.md#131-pod-security)). Steps 3 and 5 act on the
adapter's own environment and log buffers. In the embedded model the runtime is
the adapter's process, which step 1 spares, and step 1 ends the processes the
runtime started. In both models the
runtime process and its memory persist across the recycle boundary, and in the
sidecar model so do the processes it started, because the pod keeps the runtime
process ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime
process lifetime").
```

(c) In the best-effort paragraph, replace `and named pipes or UNIX domain sockets outside managed paths.` with `named pipes or UNIX domain sockets outside managed paths, and the kept runtime process (see "What the scrub reaches" above).`

**SPEC-7 — `spec/05_runtime-registry-and-pool-model.md` §5.2, retirement and sizing.**

(a) Append to the **Pod retirement policy (recycling pools)** list, after the **Scrub failure limit** item:

```
- **Process reuse not acknowledged:** The pool does not set
  `sessionPolicy.acknowledgeProcessLevelIsolation: true`. The recycle
  disposition retires the pod at each occupancy-zero boundary with the
  non-counting reason `process_reuse_unacknowledged`, and the gateway provisions
  a fresh replacement. The scrub-failure, `vm-restart`, session-count, and uptime
  retirements are evaluated first and keep their own reasons, so a
  `maxSessionsPerPod: 1` pool still retires on the session count. The gateway
  reads the field when the whole-pod scrub reports, so a change to the pool
  takes effect at the pod's next recycle boundary.
- **Runtime not live:** The adapter's `ReportPodScrub` states that its runtime
  process cannot serve the next session, or omits that fact. The recycle
  disposition retires the pod with the non-counting reason `runtime_not_live`
  after every retirement above, and the gateway provisions a fresh replacement.
  A runtime that stops after the report is found when the next session starts,
  and the start fails. On a pool with `maxConcurrentSessions: 1` the gateway
  drains the pod. On a concurrent pool each failed slot start counts toward the
  whole-pod replacement trigger under **Slot retry policy
  (`maxConcurrentSessions > 1`)** below, and the pod drains when that trigger
  fires.
```

In the **Uptime limit** item, replace `if omitted, only `maxSessionsPerPod` and `maxScrubFailures` govern retirement` with `if omitted, the other conditions in this list govern retirement`.

(b) In the validation paragraph, append after the sentence ending `without a `vm-restart` carve-out.`: `On a pool that does not set `acknowledgeProcessLevelIsolation`, a `maxSessionsPerPod` above 1 is inert for the same reason, because the pod retires at its first occupancy-zero boundary.`

(c) Append to the `session` item of **Mode adjustment factor (`mode_factor`)**, after the concurrent `vm-restart` sentence: `A sequential recycling pool that does not set `acknowledgeProcessLevelIsolation` retires each pod at its first occupancy-zero boundary, so its steady-state `mode_factor` is `1.0` and its observed `lenny_pod_session_reuse_count` p50 is 1.`

(d) In **Caveats**, replace `This convergence toward `recycle.maxSessionsPerPod` applies to `standard` and `in-place` recycling pools.` with `This convergence toward `recycle.maxSessionsPerPod` applies to `standard` and `in-place` recycling pools that set `sessionPolicy.acknowledgeProcessLevelIsolation: true`; a sequential pool that does not set it sizes at `mode_factor = 1.0`.` In the same bullet's **Integration level consideration:**, replace `and recycling requires no runtime cooperation ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed.` with `and recycling requires no CH-RUNTIMEOPS exchange ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed.`

(e) In **`onScrubFailure` behaviors:**, the **`warn`** bullet, insert after the sentence that ends `does not serve the next session, and does not traverse the `claimed → sdk_connecting` re-warm edge.`, without rewording it:

```
A pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and a pod whose scrub report states that its runtime cannot serve the next session, take the same retire on a `warn`-policy outcome (see the Pod retirement policy below, "Process reuse not acknowledged" and "Runtime not live").
```

**SPEC-8 — `spec/05_runtime-registry-and-pool-model.md` §5.2, acknowledgment, tenant pin, and client visibility.**

(a) In the YAML block, replace `acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1 — see the concurrent-session acknowledgment below` with `acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1; on a recycling pool, also lets a runtime process serve later sessions (see the runtime-process acknowledgment below)`.

(b) Replace the bullet `- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`; concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below).` with `- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`, because concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below). On a recycling pool it is also the condition under which a runtime process serves later sessions (see the runtime-process acknowledgment below).`

(c) Replace the paragraph that begins `The pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated` and ends `The `{tenant_id} → unassigned` transition applies only where cross-tenant reuse is permitted.` with:

```
The pin persists across the recycle-to-idle edge for the pod's lifetime on every
pool, including a pool with microvm-gated `recycle.allowCrossTenantReuse`,
because a recycled pod keeps the runtime process that served its tenant
([Section 4.7.10](04_system-components.md#4710-deployment-model)): a pinned idle
pod is claimable only by its pinned tenant, the candidate scan filters on the pin
label, and inventory accounting counts pinned-idle pods as idle inventory
available to that tenant alone, toward the pool's `maxWarm` ceiling but not
toward `minWarm`; the WarmPoolController drains pinned idle pods in excess of
`maxWarm` minus `minWarm`, so on a pool whose `maxWarm` equals `minWarm` a
recycled pod serves its tenant again only within the reserved hold
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle))
and is drained when it returns to idle. A recycled pod never takes the
`{tenant_id} → unassigned` transition. The gateway reads the pin on every
idle-pod acquisition, including the Postgres-backed fallback claim
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)),
before it creates the claim and passes over a pod the pin refuses to the next
idle candidate; the fallback claim stamps the pin at first
assignment as the primary claim path does. On a pool with
`recycle.allowCrossTenantReuse: true`, a pod refused to a session of a different
tenant is drained: the gateway stamps its `lenny.dev/drain-request` annotation,
the pod retires, and the warm pool provisions an unpinned replacement.
```

(d) Insert after the **Deployer acknowledgment (concurrent sessions).** paragraph, keeping that label, which `pkg/admission/pool_config_validator/validator.go:377` cites:

```
**Deployer acknowledgment (runtime process kept across sessions).**
`sessionPolicy.acknowledgeProcessLevelIsolation` also governs whether a recycling
pool reuses a runtime process from one session to the next. The runtime process
lives as long as the pod
([Section 4.7.10](04_system-components.md#4710-deployment-model)), so a later
session on a recycled pod runs in the process that served the earlier one. That
process carries into the later session the state the whole-pod scrub does not
reach (see "What the scrub reaches" above). A recycling pool that does not set
the field is admitted, and its pods retire at each occupancy-zero boundary (Pod
retirement policy above, "Process reuse not acknowledged"). The rule holds in
both deployment models, because an embedded runtime runs in the adapter's process, which the pod also
keeps. A pool with `maxConcurrentSessions > 1` always sets the field. A kept
runtime process serves one tenant; the tenant pinning paragraphs above state how
the gateway enforces that.
```

(e) In **Client visibility of weak-isolation reuse.**, replace `may be observable from prior sessions; concurrent slots share process-level state;` with `may be observable from prior sessions, and on a pool that sets `acknowledgeProcessLevelIsolation` the session may run in a runtime process that served an earlier session of the same tenant; concurrent slots share process-level state;`

(f) In the paragraph that begins `Cross-tenant pod reuse is only permitted on the sequential-reuse path`, replace its first two sentences, from `Cross-tenant pod reuse is only permitted on the sequential-reuse path` through `but it is not equivalent to dedicated hardware isolation.`, with the text below, and keep the sentences that follow (`The pool controller rejects ...` and `Cross-tenant slot sharing ...`):

```
`recycle.allowCrossTenantReuse: true` is configurable only on the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`) with `microvm` isolation, where the VM boundary provides a VM-level isolation boundary that is significantly stronger than runc or gVisor but shares host virtio devices. The field makes a pod that a session of a different tenant would reach drain rather than serve that session: a recycled pod keeps the runtime process that served its pinned tenant, so it is never reused across tenants (see the tenant pinning paragraph above).
```

(g) In the scrub-profile paragraph under **Kata/microvm scrub variant.**, replace `such pools set `vm-restart`, or `in-place` when the deployer requires cross-tenant reuse without the warm-pod reprovision cost. In `in-place` mode the following residual state vectors are documented as persisting across tenant boundaries:` with:

```
such pools set `vm-restart` or `in-place`. Neither profile reuses a pod across tenants: a `vm-restart` pool retires the pod at every recycle boundary, and an `in-place` pod keeps its continuing guest across its pinned tenant's later sessions and is drained when a session of a different tenant would reach it (see the tenant pinning paragraph above). In `in-place` mode the following residual state vectors are documented as persisting across the pinned tenant's sessions in the continuing guest:
```

(h) In the table under `Common configurations of the block:`, replace the `Pod reuse` row's behavior cell `The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions.` with `The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions, when the pool sets `sessionPolicy.acknowledgeProcessLevelIsolation: true`; otherwise it retires at each occupancy-zero boundary.`

**SPEC-9 — `spec/06_warm-pod-model.md` §6.1 and §6.2.**

(a) In the §6.1 `session`, `maxConcurrentSessions: 1`, `recycle.enabled: true` row, replace `At the occupancy-zero recycle boundary the whole-pod scrub terminates the SDK process along with all other session processes;` with `At the occupancy-zero recycle boundary the ending session's use of the runtime ends and the runtime process is kept ([Section 4.7.10](04_system-components.md#4710-deployment-model)); a pod whose pool does not set `acknowledgeProcessLevelIsolation`, or whose runtime is reported as not live, retires instead;`

(b) In the §6.2 occupancy-projection sentence, insert immediately before `a `reserved` claim projects `reserved`` (after the clause ending `step 7);`), without rewording the surrounding text: `a `recycling` claim on a pool that does not set `acknowledgeProcessLevelIsolation`, or on a pod whose scrub report states that its runtime cannot serve the next session, takes the same `claimed → draining` projection on any `scrubProfile`;`

(c) In the §6.2 recycle-edge block, in the `claimed ──→ draining` recycle edge, replace `an unschedulable host node; or a vm-restart pool reprovisioning a fresh` / `guest at the recycle boundary)` with `an unschedulable host node; a vm-restart pool reprovisioning a fresh` / `guest at the recycle boundary; a pool without acknowledgeProcessLevelIsolation;` / `or a scrub report stating that the runtime cannot serve the next session)`, keeping the block's column alignment.

(d) In **`reserved` hold semantics**, after the sentence ending `see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) step 7).`, append: `A pod on a pool that does not set `acknowledgeProcessLevelIsolation`, or whose runtime is reported as not live, is not a recycled pod in this sense either and takes the same `draining → terminated` projection.`

(e) In the §6.2 occupancy-projection block, in the `idle ──→ draining` edge, replace `level-triggers the drain from the pod CreationTimestamp,` / `regardless of session activity)` with `level-triggers the drain from the pod CreationTimestamp,` / `regardless of session activity; or gateway-stamped` / `lenny.dev/drain-request annotation — see §5.2)`, and in the `claimed ──→ draining` edge above it replace `lenny.dev/drain-request annotation — unhealthy threshold)` with `lenny.dev/drain-request annotation — see §5.2)`, keeping the block's column alignment.

(f) In **preConnect re-warm on scrub_warning.**, insert after the sentence that ends `and the gateway provisions a fresh replacement (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) step 7).`, without rewording it:

```
A pool that does not set `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and a pod whose scrub report states that its runtime cannot serve the next session, take the same retire on both outcomes ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), Pod retirement policy).
```

(g) In §6.1 **Pod-warm (default):**, replace `The agent process is NOT started.` with `No session is bound, and no message for the next session has been delivered.`, keeping the sentences that follow.

(h) In §6.1 **One-session-per-pod default and the recycle opt-in.**, replace `with explicit deployer acknowledgment (`acknowledgeBestEffortScrub`):` with `with explicit deployer acknowledgments (`acknowledgeBestEffortScrub` and `acknowledgeProcessLevelIsolation`):`.

(i) In §6.1 **Per-session credential lease lifecycle.**, replace `and a recycling pod does not retain credentials between sessions.` with `and a recycling pod does not retain credentials between sessions outside a kept runtime process ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

**SPEC-10 — `spec/07_session-lifecycle.md` §7.1, `residualStateWarning` row.** Replace `For recycling pools, residual-state vectors include the DNS resolver cache, TCP `TIME_WAIT` state, and page cache from prior sessions.` with `For recycling pools, residual-state vectors include the DNS resolver cache, TCP `TIME_WAIT` state, and page cache from prior sessions, and on a pool that sets `sessionPolicy.acknowledgeProcessLevelIsolation: true` the session may run in a runtime process that served an earlier session of the same tenant (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) deployer acknowledgments).`

**SPEC-11 — `spec/15_external-api-surface.md` §15.4, §15.4.1, §15.4.2, and §15.4.3.**

(a) In §15.4, replace `the platform scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime cooperation and is available at every integration level.` with `recycling needs no CH-RUNTIMEOPS exchange and is available at every integration level, on the terms [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Recycling and integration levels** states.`

(b) In the §15.4.2 state table, replace the `DRAINING` description `Graceful shutdown requested. The adapter finishes the current exchange and signals the agent to stop.` with `The pod is terminating. The adapter finishes the current exchange, signals the agent to stop, and then closes the runtime's connections. A session's end does not enter this state.` In the diagram above the table, replace the return edge from `ACTIVE` to `TERMINATED` labelled `(session ends normally)` with a return edge from `ACTIVE` to `READY` labelled `(session ends; the pod is recycled)`. In the sentence after the table, replace `readiness signal, exit on completion)` with `readiness signal)`.

(c) In §15.4.3, locating each site by its quoted text (each matrix row is one physical line padded with spaces, and stays one line):

- In the **Basic** list, replace `failure to ack within 10 seconds causes SIGTERM` with `failure to ack within 10 seconds ends the session ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))`.
- In the matrix `Interrupt` row, replace the Basic cell `No clean interrupt. Gateway sends SIGTERM; runtime has no opportunity to reach a safe stop point.` with `No clean interrupt, and the runtime process receives no signal ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, and the Standard cell `No clean interrupt. Same SIGTERM-based termination as Basic.` with `No clean interrupt. Same as Basic.`
- In the matrix `Deadline / expiry warning` row's Basic cell, delete `; Basic-level receives only `shutdown` at expiry`, so the cell ends at `requires CH-RUNTIMEOPS.`
- In **Basic-level limitations**, the **Clean interrupt:** bullet, replace `The gateway issues `shutdown` on stdin and follows with SIGTERM after `deadline_ms`; the runtime cannot acknowledge an interrupt cleanly.` with `The runtime cannot acknowledge an interrupt cleanly.`
- In the same list, the **`DEADLINE_APPROACHING` warning:** bullet, replace `Basic-level runtimes receive only the `shutdown` message at expiry with no advance notice.` with `Basic-level runtimes receive no advance notice of a session's expiry.`

(d) In §15.4.1, delete ` — the runtime knows it's receiving its first message by virtue of just having started`, so the sentence reads `No `sessionState` field.`

**SPEC-12 — `spec/29_communication-scenarios.md` §29.2, §29.4, and §29.9.**

(a) In §29.2, replace step 25 (from `25. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. The adapter spawns the runtime` through its closing `([§4.7](04_system-components.md#47-runtime-adapter)).`) with:

```
25. On the first session of a pod-warm pod whose `type: agent` runtime runs in the sidecar deployment
    model: `runtime` → `adapter`, `CH-MSGSOCK`, `intra-pod`. The runtime process dials the connection
    the adapter accepts ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes)
    step 7, [§4.7.10](04_system-components.md#4710-deployment-model), §28.5.3 `CH-MSGSOCK`).
```

and replace step 29 (from `29. On a pod-warm pod with a `type: agent` runtime: `unstated`.` through its closing `§28.3).`) with:

```
29. On a pod-warm pod with a `type: agent` runtime: `adapter`, `internal`. In the sidecar deployment
    model the adapter serves this session on the pod's `CH-MSGSOCK` connection, which the runtime dialled
    on the pod's first session (step 25), and in the embedded deployment model the adapter runs the
    runtime loop in its own process
    ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 7).
```

In steps 26a, 26b, 26c, 27, and 28, replace the opening `On a pod-warm pod` with `On the first session of a pod-warm pod`, keeping each step's number and the rest of its text and rewrapping to the step's width.

(b) In §29.4, replace step 13 (from `13. On a session end against a Full-level runtime, triggered by` through `[§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row states for the graceful-shutdown signal.`) with:

```
13. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or
    by an expiry timer: `adapter`, `internal`. The adapter writes no `CH-RUNTIMEOPS` frame and sends the
    runtime process no signal ([§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row,
    [§4.7.10](04_system-components.md#4710-deployment-model)). The `terminate` frame the pod's
    termination carries is traced in §29.9 step 4b.
```

(c) In §29.4 step 12, replace `the graceful end-of-session shutdown of the pod's runtime` with `the graceful end-of-session teardown of the named session`, replace `the adapter keeps the pod process alive across the recycle boundary` with `the adapter keeps its own process and the runtime process alive across the recycle boundary`, and replace `On the default disposition the adapter closes the session runtime and the pod is replaced.` with `On the default disposition the pod is replaced.`

(d) In §29.4 step 17, replace `retires the pod when the recycle limits or the host-node schedulability check say so` with `retires the pod when the recycle limits, the process-reuse acknowledgment, the reported runtime liveness, or the host-node schedulability check say so`.

(e) In §29.4 step 6, replace `so this step does not occur and the interrupt degrades to SIGTERM-based termination with no opportunity for the runtime to reach a safe stop point` with `so this step does not occur and the interrupt degrades as the `Interrupt` row states`, keeping the citation that follows.

(f) In §29.9, replace step 4b (from `4b. `unstated`. The specification names `eviction`` through `and it does not fix the relative order of steps 4a and 4b.`) with:

```
4b. Against a Full-level runtime: `adapter` → `runtime`, `CH-RUNTIMEOPS`, `intra-pod`. The adapter writes
    the `terminate` frame once, with the reason `eviction`, at the point the
    [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row states (§28.5.3 `CH-RUNTIMEOPS`). A
    Basic-level or Standard-level runtime opens no `CH-RUNTIMEOPS` channel and receives no frame. The
    specification does not fix the relative order of steps 4a and 4b.
```

**SPEC-13 — `spec/11_policy-and-controls.md` §11.4, the full-revoke propagation mechanism, step 3.** Replace `3. The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL).` with:

```
3. The pod's runtime adapter tears down the session ([Section 4.7](04_system-components.md#47-runtime-adapter), `Shutdown`), which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
```

**SPEC-14 — `spec/28_communication-channels.md` §28.5.3 and §28.8.** Locate each site by its quoted text, because the §28.5.3 sentences wrap across lines. Each §28.8 row stays one physical line.

(a) In the `CH-MSGSOCK` **Timing.** bullet, replace `is treated as hung and is sent SIGTERM` with `is treated as hung: the adapter ends that session's stream, and the runtime process receives no signal ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime")`, keeping the citation that follows.

(b) Under **Inbound: `heartbeat`**, replace `the adapter considers the process hung and sends SIGTERM.` with `the adapter considers the process hung and escalates as the `CH-MSGSOCK` **Timing.** bullet states.`

(c) In the `CH-RUNTIMEOPS` **Degradation.** bullet, replace `interrupt degrades to SIGTERM-based termination and the deadline warning and the drain coordination signal are not delivered` with `interrupt degrades as the [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) `Interrupt` row states, and the deadline warning and the drain coordination signal are not delivered`.

(d) In the §28.8 `CH-RUNTIMEOPS` row, make the replacement (c) makes.

(e) In the §28.8 `CH-MSGSOCK` row, replace `is treated as hung and is sent SIGTERM` with `is treated as hung, as the §28.5.3 `CH-MSGSOCK` **Timing.** bullet states`.

§28's sentence "refuses the call unless that process has been given exactly one session" binds across recycle boundaries once the process is kept. The §28.5.3 sentences that say the adapter spawns the runtime binary belong to the first-session manifest-ordering defect and keep their wording (§9.1).

**SPEC-15 — `spec/13_security-model.md` §13.1, credential-read boundary.**

(a) In **`lenny-cred-readers` membership boundary.**, replace `except on pools with `sessionPolicy.maxConcurrentSessions > 1` (see the per-slot credential-read clause below).` with `except on pools with `sessionPolicy.maxConcurrentSessions > 1` (see the per-slot credential-read clause below) and on recycling pools that set `acknowledgeProcessLevelIsolation` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

(b) In the same paragraph, delete the sentence `On a pod that serves one session at a time (`maxConcurrentSessions: 1`, with or without recycling), the subprocess-inheritance surface is inside the trust boundary the session already owns.`

(c) In **Per-slot credential-read scope (`maxConcurrentSessions > 1`).**, replace `MUST set `sessionPolicy.maxConcurrentSessions: 1`: the pod then holds at most one credential lease at a time, and sequential pod reuse under `recycle.enabled: true` keeps that property because each session's lease is released before the next session begins ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).` with `MUST set `sessionPolicy.maxConcurrentSessions: 1`, and on a recycling pool leave `acknowledgeProcessLevelIsolation` unset ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)"): the pod then holds at most one credential lease at a time.`

**SPEC-16 — `spec/16_observability.md` §16.1, the `lenny_warmpool_idle_pods` row.** Replace `number of pods in `idle` state ready to be claimed;` with `number of pods in `idle` state ready to be claimed, excluding idle pods pinned to a tenant ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes));`. The row stays one physical line.

## 9. Non-goals

### 9.1 Out of scope

- **Re-creating a runtime process inside the pod.** A runtime that stops ends the pod's service through the `runtime_not_live` retire or the failed-start drain.
- **A per-session end signal to the runtime, and SDK support for sequential sessions.** Open decision 3.
- **Per-session attribution for intra-pod MCP on a multi-session process.** Open decision 1.
- **The per-release `maxSessionsPerPod` drain and its `vm-restart` exclusion** (`pkg/gateway/session/recycle/scrubreporter_seams.go:344-373`). Unchanged.
- **The `SocketRuntimeProcess` listener teardown.** Proposal 0078, which lands first.
- **Per-slot cleanup**, which runs at every session release and does not depend on the runtime's lifetime.
- **The §16.1 retirement-counter row.** The row omits the existing non-counting reasons `host_unschedulable`, `scrub_report_timeout`, and `vm_restart_reprovision`. File it separately; the new reasons are named in §5.2.
- **The first-session manifest ordering on the sidecar transport.** The runtime reads the manifest before the adapter writes it on a pod's first session. It is a pre-existing defect on every sidecar pod, filed to be fixed after proposals 0078 and 0079 land. The sentences that describe the manifest as written "before spawning" belong to it and keep their wording here: the §4.7 adapter manifest ("before the runtime binary is spawned"), the manifest tool-list field, the manifest `credentialsPath` field, §4.7.11 items 1 and 4, the `SO_PEERCRED` self-test ("before any agent process is spawned"), §15.4.3 **Authentication.**, the §15.7 `CreateRequest` comment, the §6.1 **Per-session credential lease lifecycle.** paragraph ("before that session's binary is spawned"), and the §28.5.3 sentences that place the manifest write before the adapter spawns the runtime binary (the **Preconditions.** bullets of `CH-MSGSOCK`, `CH-RUNTIMEOPS`, `CH-MCP-PLATFORM`, and `CH-MCP-CONNECTOR`, and the two `adapterLocalTools` sentences under `CH-MSGSOCK` that say "before spawning the runtime"). §29.2 step 25 is not among them: it states the runtime's `CH-MSGSOCK` dial rather than the manifest ordering, and SPEC-12 rewrites it.
- **Scrub steps 3 and 5, and the preConnect re-warm.** `scrubConfig` sets neither `ResetEnv` nor `TruncateLogs` (`pkg/adapter/podscrub.go:120-127`), so both steps record as skipped, and no component calls `PreConnect` after adapter startup. Both predate this proposal; file them separately.
- **Embedded recycling wiring.** `cmd/runtimes/echo-embedded` and `cmd/runtimes/preconnect-echo` wire no `ScrubOps` or `PodScrubReporter`, so an embedded recycling pod never reports its scrub and retires by timeout. File it separately.
- **Claim-path defects outside the pin check.** `Claimer.Claim` leaks a bound claim when `stampPodTenant` fails (`pkg/gateway/podlifecycle/podclaim/claimer.go:157-159`), and `CRDPodRegistry.ClaimPod` has no pin check and no production caller (`pkg/podregistry/crd.go:170-209`). File them separately.

## 12. Open decisions for review

1. **Pod-global surfaces after a kept process's first session.** D9 fails closed: intra-pod MCP forwarding, the direct-mode token fold, and the control-event stamp name no session once the process has been given a second session. On an acknowledged recycling pool this removes platform tools and direct-mode usage attribution for every session after the pod's first, including on embedded pods. **Default:** accept it as the concurrent-pool behaviour extended. **Alternative:** a follow-up that attributes calls per session, which needs runtime cooperation.
2. **Scope of the acknowledgment gate.** **Default:** the gate applies to every recycling pool, because the embedded runtime runs in the adapter process, which is kept. It needs no label, and the only recycling fixture pool, `task-mode-echo-pool`, gains the field (FIXTURE-1). The consequence is that an embedded recycling pool without the acknowledgment loses reuse. **Alternative A:** exempt embedded pods, which needs a pod label that the Sandbox reconciler stamps from the runtime's `deploymentModel` and the gateway reads at the boundary, with an envtest and a rollout note. **Alternative B:** refuse `recycle.enabled` with `maxSessionsPerPod > 1` and no acknowledgment at pool admission. It is louder and removes the `process_reuse_unacknowledged` branch, but every existing recycling pool definition without the field starts failing validation, and a pool edit that removes the field is refused rather than taking effect at the next boundary.
3. **The runtime contract for sequential sessions.** The Go runtime SDK binds `OnCreate`, the manifest session, and the credential path once per process (`sdks/runtime/go/runtime/runtime.go:76-84`), and no frame tells a runtime that a session ended. On an acknowledged pool such a runtime serves a later session under the first session's context. The same gap exists on concurrent pools today, and proposal 0084 declares multi-session SDKs out of scope. **Default:** stage the SDK change in a sibling proposal; this proposal stages the specification sentences, the doc comments, and the runtime-author guide statement that a runtime on an acknowledged recycling pool keys every frame by `sessionId`. **Alternative:** gate reuse on a runtime capability declaration, which adds a registry field and a client-surface change.
4. **Relationship to proposal 0078.** **Default:** land 0078 first. CODE-1 applies on top of 0078's CODE-1 through CODE-3, and CODE-4 extends 0078's `CloseListener` (0078 CODE-2) and keeps its name and signature. TEST-1 deletes 0078's TEST-1 and TEST-9 and amends its TEST-4. **Alternative:** fold 0078's CODE-1 and CODE-2 into this proposal.
5. **The terminate frame.** Removing the occupancy-zero send leaves the §15.4.2 `DRAINING` state and the §15.4 matrix's Full-level drain row with no sender, because `RuntimeOps.Terminate` has no other caller (`pkg/adapter/session.go:516`). **Default:** send the frame once at pod exit (D8, CODE-2, CODE-4, SPEC-11(b)). **Alternative:** delete `drainViaLifecycle` and `drainReason` with their tests, and amend the §15.4 matrix row and the §15.4.2 table to state that no drain coordination exists.
