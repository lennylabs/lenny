# Spec changes: Name who starts the next session's runtime on a recycled pod

## 2. Decisions

**D1. The runtime process lives as long as the pod.** In the sidecar deployment model the runtime process is the one the kubelet started in the runtime container. It dials `CH-MSGSOCK` once; the adapter accepts that connection at the pod's first session start and closes it only in the pod-scope teardown (D10). The adapter does not spawn a process in the runtime container: the containers share no process namespace (§13.1), and the runtime binary exists only in the runtime container's image. In the embedded model the runtime process is the adapter process, which already lives as long as the pod, and `InProcessRuntime` keeps its per-session loop, which ends a session's execution and leaves the process running. No per-session call ends the process, and occupancy zero ends nothing. The pod-scope teardown (D10) is the only adapter act that closes the sidecar runtime's connection, and it runs at adapter exit and when the coordinator hold times out.

**D2. The adapter rule is unconditional, and the gateway decides reuse.** The adapter holds no recycle state (§5.2). On the concurrent path the recycle `Shutdown` follows the last slot's `Close` (`pkg/adapter/session.go`, `answerShutdown`), so a close decided at the last slot cannot know the disposition. Every pool that must not reuse a pod retires it after occupancy zero through the recycle disposition or the session path: a non-recycling pool, `maxSessionsPerPod: 1`, `scrubProfile: vm-restart`, a pod whose runtime is reported as not live (D4), and every existing retire trigger. Pool admission refuses every other recycling pool that lacks the process-reuse acknowledgment (D5). The kubelet ends the runtime with the pod, and no session is resident when that happens. No wire field tells the adapter the pool's acknowledgment or cross-tenant setting.

**D3. Per-session calls do not end the transport.** `SocketRuntimeProcess.Close` and `Interrupt` return nil and change nothing. The active set, `addActiveLocked`, and `releaseActiveLocked` are deleted, because no decision reads them. The sidecar transport has no signal path, so a clean `Interrupt` delivers nothing there, which is its behaviour today whenever a sibling slot is active.

**D4. A stopped runtime is reported at the boundary and refused at the next start.** When the fan-out reader's scan ends, `SocketRuntimeProcess` records a sticky `ended` state. Nothing clears it and nothing redials. After the whole-pod scrub returns, the adapter asks its runtime whether it can serve the next session and sends the answer on the `ReportPodScrub` it already sends, in a new `ReportPodScrubRequest.runtime_live` field. The sample follows the scrub because step 4 clears `/tmp` and `/dev/shm`, which the runtime container shares. Each transport answers from state it already holds: the socket transport reports `connected && !ended`, `InProcessRuntime` reports that no session is bound, `SubprocessExecutor` reports true because it spawns a child per session, and a transport that does not answer (`MCPRuntime` and the test doubles) reads as false. A request that omits the field decodes as false. A false report retires the pod with the non-counting reason `runtime_not_live`, so the pod is never held for a session its runtime cannot serve. A runtime that stops after the report, during the `reserved` hold, is found by the next start: `Start` and `Output` return an error at once when `ended` is set, `StartSession` fails, and the failed-start path drains the pod as SPEC-7(a) states (`pkg/gateway/podlifecycle/podsession/bindlaunch.go:68-79` and `reclaim.go:28-35` on a `maxConcurrentSessions: 1` pool; `pkg/gateway/sessionserver/pod_launch.go:167-169` and `slot_bind.go:183-190` on a concurrent pool).

Two alternatives are rejected. Withholding the report retires the pod through the missing-report timeout, holding it in `recycling` for `cleanupTimeoutSeconds` plus a grace period and recording `scrub_report_timeout` with the `failed` terminal. Reading the runtime container's status from the Pod object lags the kubelet, has no counterpart on an embedded pod, and cannot observe the connection the next `Start` uses.

**D5. Keeping a runtime process across sessions requires `sessionPolicy.acknowledgeProcessLevelIsolation: true`, and pool admission enforces it.** The field (`pkg/gateway/runtime/runtimestore/runtimestore.go:1024-1028`) names the property the deployer accepts: process-level state that one session leaves is visible to another on the same pod, and a kept runtime process carries into the next session the state the whole-pod scrub does not reach (D11). By the human decision of 2026-09-30 (§12, decision 2, Alternative B), pool admission refuses every recycling pool that keeps its runtime process across sessions without the field. SPEC-8(d) states the rule, and CODE-11 enforces it at every pool write. Existing pool definitions get no compatibility path, because the platform is pre-deployment.

**D6. A kept runtime process serves one tenant.** The state is the pod's `lenny.dev/tenant-id` label. The gateway stamps it at first assignment (`pkg/gateway/podlifecycle/podclaim/claimer.go:157`, `slotclaimer.go:723`), and the `lenny-tenant-label-immutability` webhook refuses a change to another tenant (`pkg/admission/label_immutability/label_immutability.go:197-205`). No component writes the `{tenant_id} → unassigned` release (`UnassignedTenantID`, `label_immutability.go:48`, has no writer in `pkg/` or `cmd/`), and SPEC-8 forbids it for a recycled pod, so the pin holds for the pod's life. §5.2 already requires the candidate scan to filter on the pin, and the code does not: the idle scan (`claimer.go:124-159`) and the concurrent-slot idle pass (`slotclaimer.go:486-510`) read no pin, and the Postgres fallback claim (`pkg/gateway/podlifecycle/podsession/fallbackclaim.go:91-133`, over `pkg/agentpodstate/pgstore/pgstore.go:245-279`) reads none and stamps none. A kept runtime turns that gap into cross-tenant exposure of in-memory state, so one helper reads the pin at all three sites before the claim is created and refuses a pod pinned to another tenant. A refused pod stays pinned for its tenant on every pool that keeps its runtime process, including a pool with `recycle.allowCrossTenantReuse: true`, and the acquisition writes nothing to it (SPEC-8(c), §12 decision 11; D16 states the drain on a pool outside the rule). On an `allowCrossTenantReuse` pool the field changes no acquisition decision (SPEC-8(f)). The field keeps its admission constraints (`pkg/gateway/runtime/poolstore/poolstore.go:571-572`, `:602-618`, `:649-665`), and in the shipped tree it also selects the reported `scrubPolicy` (`pkg/gateway/sessionserver/isolationlevel.go:240`). Because a refused pod stays pinned, the WarmPoolController planner must not count it toward the pool's warm target: today it counts every idle pod (`pkg/controller/warmpool/plan/plan.go:111-113`, `:130`), and once each warm pod on a recycling pool has served some tenant, every other tenant's claim returns `ErrNoIdlePod` while the pool reports full inventory. SPEC-8(c) states the pinned idle inventory rule, CODE-10 implements it, and D14 records why it is the interim rule.

**D7. The new retire reason does not count.** §16.1 freezes `lenny_gateway_pod_retirement_total{reason}` to the three limit triggers. `runtime_not_live` is a state-driven retire, like `host_unschedulable` (`pkg/sandbox/podscrub/podscrub.go:186-226`), and no retirement counter counts it. `applyDisposition` drives `Retire(failed=false)` to `released` for it without change, and the retire path logs the reason (CODE-7). Today `claimDispositionDriver.Retire` logs a reason only for a `failed` retire (`pkg/gateway/session/recycle/scrubreporter_seams.go:993-999`), so no `released` retire's reason is recorded.

**D8. No path sends the terminate frame.** By the human decision of 2026-09-30 (§12, decision 5). The `CH-RUNTIMEOPS` `terminate` frame tells the runtime to exit (§28.5.3: "Receipt always means process exit"), so `tearDownReclaimedSlot` stops sending it and the `boundRemains` result that gated it is deleted. That call is the frame's only production sender (`pkg/adapter/session.go:431-433`, through `drainViaLifecycle` at `:512-520`), so `drainViaLifecycle` and `drainReason` are deleted with it. The pod-exit path sends no frame: the adapter's SIGTERM goroutine (`cmd/lenny-adapter/main.go:413-426`) closes `CH-RUNTIMEOPS` without writing to it, as it does today, and the §10.1.4 hold-timeout termination already sends none (`pkg/adapter/holdstate.go:228-232`; SPEC-18), which TEST-3 pins (§12, decision 10). §15.4.2 and §15.4.3 state that no drain coordination exists at pod exit at any integration level (SPEC-11(b), (c)). A Full-level runtime observes the pod's exit as the close of `CH-RUNTIMEOPS` and the kubelet's SIGTERM to its container. A pod-exit drain is out of scope (§9.1). `RuntimeOps.Terminate` stays as the frame's encoder, with no production caller.

**D9. The runtime generation counts the sessions given to the process for the pod's life.** `noteRuntimeClosed` removes the session from `runtimeLive` and no longer resets the cohort. `soleSession` names the cohort session only while `runtimeLive` still holds it. This is the rule §4.7 and §28 already state ("refuses the call unless that process has been given exactly one session and that session is the caller") applied to a process that outlives occupancy zero. The pod-global surfaces (intra-pod MCP forwarding, the direct-mode token fold, and the control-event stamp) therefore fail closed on every session after a kept process's first, as they do on a concurrent pod (§12, decision 1; proposal 0084 carries per-session attribution).

**D10. The pod-scope teardown is proposal 0078's `CloseListener`, extended.** It sets `ended`, closes the shared connection, waits `defaultSocketShutdownGrace` for a spawned child and then kills it, and closes the listener. It keeps 0078's name and signature, `CloseListener() error`, so every 0078 caller and test cleanup compiles unchanged. It is idempotent, and it has two production callers. `cmd/lenny-adapter` calls it at process exit. `onHoldTimeout` calls it when the coordinator hold times out, because the per-session `Close` that ended the sidecar runtime there no longer does (D3), and `onHoldTimeout` exists so that no runtime keeps live credentials without a coordinator (`pkg/adapter/holdstate.go:159-162`). The adapter keeps running after the timeout, so 0078's table row stating that every path reaching a `Start` after `CloseListener` is an exiting adapter no longer holds; 0078 keeps its words, and the `ended` check returns the error at once (CODE-1).

**D11. The scrub does not reach the kept runtime process.** Step 1's `kill -9 -1` runs as the adapter's UID in the container that runs the scrub. In the sidecar model it cannot reach the runtime container, which has its own process namespace (§13.1) and its own UID (`pkg/controller/sandbox/podspec/podspec.go:56-57`). In the embedded model the runtime is the adapter process, which the kill spares. Step 1b's `ipcrm --all=shm` removes only the segments the adapter's UID owns or created. Steps 3 and 5 act on the adapter's own environment and log buffers.

**D12. The client disclosure keeps its values and widens its stated meaning.** `podReuse` and `residualStateWarning` are already `true` on every pool with `recycle.enabled: true` or `maxConcurrentSessions > 1` (`pkg/gateway/sessionserver/isolationlevel.go:208-212`). SPEC-10 edits both §7.1 rows. The `podReuse` row drops its lead-in `when the assigned pool reuses pods across executions: `, so the row states the configuration conditions under which the field is `true` without claiming that every such pool reuses a pod. The `residualStateWarning` row, with the §5.2 client-visibility paragraph (SPEC-8), states that a recycled session may run in a runtime process that served an earlier session of the same tenant. A recycling pool on which no runtime process serves a second session, which is a pool with `recycle.maxSessionsPerPod: 1` or `scrubProfile: vm-restart`, still reports `true` and over-warns, because the gateway computes the fields from pool configuration before it binds a pod. The fields never understate a kept process for the claim made at session creation, because that claim uses the pool resolution that computes them, and the gateway admits a session to a pod that has served an earlier session only when that resolution keeps runtime processes across sessions (SPEC-8(d), D16). A later claim for the same session re-resolves the pool (§9.1, **A recovery claim after an update that brings a pool into pod reuse**). A new field would change the external API, the OpenAPI document, and the client SDKs to draw a distinction no client has asked for.

**D13. The new `Decide` input carries retire polarity.** `Inputs.RuntimeNotLive` is true to retire, the polarity `VMRestart` has, so the zero value keeps today's disposition and every existing literal keeps its meaning (`pkg/sandbox/podscrub/podscrub_test.go:14-27`, `tests/tier4_integration/recycle_scrub_path_test.go:473-479`, `tests/tier7a_load_local/scenarios/vm_restart_recycle_disposition/scenario.go:150-156`, the reuse fakes in `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go`, and `tests/tier9_security/pool_admission_isolation_test.go:492` and `:567`). The fail-closed default sits at its single production assignment: `RecordPodScrub` negates the positive wire field.

**D14. Pinned idle inventory is the interim rule.** By the human decision of 2026-09-30 (option D), this proposal ships the interim accounting, and a follow-up proposal adds the rest (§9.1, **Pinned idle bound and idle timeout**). The pool store writes `minWarm` and `maxWarm` from one `warmCount` (`pkg/controller/poolscaling/poolstoresource.go:107-108`). Once demand is observed, the PoolScalingController writes the formula target to `minWarm` and leaves `maxWarm` at `warmCount` (`pkg/controller/poolscaling/controller.go:729-730`, `:1090-1094`). SPEC-8(c)'s pinned-idle bound is therefore zero on an admin-API pool until demand is observed, and `warmCount` minus the formula target afterwards. Where the bound is zero, a kept runtime is reused only within the `gateway.claimHoldTTLSeconds` hold. SPEC-8(c) is the rule's one home. D6, SPEC-4(g), SPEC-7(c) and (d), SPEC-16, SPEC-17, and CODE-10 point to it. The §10.7 `status.readyCount == 0` deletion predicate is unchanged: at `minWarm = maxWarm = 0` the bound is zero, so every pinned idle pod drains in the same pass as the unpinned ones.

**D15. The new retire condition has one statement.** The §5.2 Pod retirement policy item "Runtime not live" (SPEC-7(a)) is the only statement of the condition's predicate, reason, precedence, reading of an omitted field, and terminal retire. Every other site names the item by its label and cites §5.2, and no other line of spec/04, spec/05, spec/06, or spec/29 names the reason or restates the predicate (TEST-16). "Runtime not live" is a per-pod condition, like the limit and host-unschedulable retires, so it is named only where a site lists per-pod retire causes: the §4.7 `ReportPodScrub` row, which carries the fact (SPEC-3(c)), the §4.6.3 `released` bullet (SPEC-4(a)), the §5.2 **Recycling and integration levels** paragraph (SPEC-5(b)), the §6.2 recycle `claimed ──→ draining` edge (SPEC-9(c)), and the §6.2 re-warm-on-`scrub_warning` paragraph (SPEC-9(f)). §29.4 step 17 names the Pod retirement policy as a whole (SPEC-12(d)). A site that scopes the `reserved` hold, the SDK re-warm, or the `rewarmStartedAt` stamp to a pool class keeps its `vm-restart` wording and gains no clause, because every admitted recycling pool with `maxSessionsPerPod` above 1 and a profile other than `vm-restart` keeps its runtime (D5). SPEC-5(a) edits the §5.2 recycle-lifecycle line without rewording a tier-11 pinned phrase, and no staged edit touches the §6.2 projection sentence.

**D16. An admitted edit that takes a pool outside the process-reuse rule ends reuse at the next acquisition.** By the human decision of 2026-10-01 (§12, decision 6). SPEC-8(d) is the rule's one home; D12, SPEC-4(h), SPEC-8(c), SPEC-9(j), SPEC-10, SPEC-12(g), DOC-4, and CODE-9 point to it, and CODE-8 is its code site. The pool-side input is `poolstore.KeepsRuntimeAcrossSessions` (CODE-8 defines it), evaluated on the pool record the session server resolves for the claim. It is carried as `KeepsRuntime` on `PoolMatch`, `BindRequest`, `ResumeRequest`, and `ClaimRequest`, and a pool with no pool-store row resolves false. The pod-side state is what the claim scan already reads: a Sandbox projecting `reserved`, and the Pod's `lenny.dev/tenant-id` label (D6). When `KeepsRuntime` is false, `Claimer.Claim` ends each `reserved` hold it reads instead of rebinding it, and the acquisition refuses every pinned pod through `AdmitTenantPin` and stamps its drain request through `DrainRefusedPod` only when no claim holds it (CODE-8, non-spec §4.4). `SlotClaimer.ClaimSlot` needs no check, because the session server routes a claim there only when the resolved `maxConcurrentSessions` is above 1 (`pkg/gateway/sessionserver/pod_claim.go:159`, `pod_launch.go:167`, `:281`). The recycle disposition is unchanged, because it re-reads the pool at every report (`pkg/gateway/session/recycle/scrubreporter_seams.go:574-642`). Ending a hold stamps no drain request. The `DELETE` returns the pod to `idle` through the occupancy projection's existing `reserved → idle` edge (`pkg/controller/warmpool/occupancy.go:128-133`), and the next acquisition that reads the pod `idle` refuses and stamps it, which takes the existing `idle → draining` edge. A stamp written with the `DELETE` would not wait for that projection, because the WarmPoolController writes `draining` from whatever phase the Sandbox holds (`pkg/controller/warmpool/pod_reconciler.go:609-618`, `:666-672`), and §6.2 and `state.ValidTransitions` have no `reserved → draining` edge (`pkg/sandbox/state/state.go:179-184`). The mechanism adds no binding state, Sandbox edge, metric, or wire field.

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

**The boundary.** `startPodScrub` runs the scrub, computes its outcome, samples `ServesNextSession`, and sends `ReportPodScrub(podID, outcome, runtimeLive, detail)`. The gateway handler passes `runtime_live` to `RecordPodScrub`, which sets `Inputs.RuntimeNotLive` to its negation. `Decide` gains one branch after the uptime branch and before the host-schedulability branch:

```go
if in.RuntimeNotLive {
    return Disposition{
        Ready: true, NextPhase: state.Draining,
        ScrubWarning: warned, Retire: true,
        Reason: ReasonRuntimeNotLive,
    }
}
```

**The acquisition.** `podclaim.AdmitTenantPin` reads the pin before the claim is created on the idle scan, the concurrent-slot idle pass, and the Postgres fallback claim (D6; non-spec changes §4.4 states the predicate). `Claimer.Claim` reads `ClaimRequest.KeepsRuntime`: when it is false, the rebind branch ends each `reserved` hold it reads instead of rebinding, and `AdmitTenantPin` refuses every pinned pod (D16, SPEC-8(d)). `AdmitTenantPin` writes nothing. `podclaim.DrainRefusedPod` writes the drain stamp, and only on a pool outside the process-reuse rule (D16). No acquisition stamps a pod it refused because the pod is pinned to another tenant (SPEC-8(c), §12 decision 11).

**Outcome per pool.** A recycling pool on `standard` or `in-place` with `maxSessionsPerPod > 1` carries `acknowledgeProcessLevelIsolation: true`, because admission refuses it otherwise (D5, CODE-11). It reuses the pod with its runtime for the pinned tenant while the pod is held for that tenant (SPEC-8(c)), until a retire trigger fires. A `vm-restart` pool, a `maxSessionsPerPod: 1` pool, and a non-recycling pool retire the pod as they do today, and the kubelet ends the runtime with it.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it lands |
|:--|:--|:--|
| Acknowledged recycling pool at occupancy zero | The connection stays up; the next session's `Start` returns without an accept, and its frames reach the same runtime process | D1, D3, SPEC-2, CODE-1 |
| Recycling pool the SPEC-8(d) acknowledgment rule covers, without the acknowledgment | Create, update, and dry-run are refused with `400 VALIDATION_ERROR` naming `acknowledgeProcessLevelIsolation`, and a bootstrap seed entry fails with `SEED_STORE_ERROR`; the stored pool is unchanged | D5, CODE-11, TEST-19 |
| `maxSessionsPerPod: 1` | The pod retires at its first boundary with the counting `session_count_limit`, as today | D5 |
| Concurrent pool | Always acknowledged, so the runtime is kept across occupancy zero; the per-release `maxSessionsPerPod` drain is unchanged | D5 |
| `vm-restart` pool on any deployment model | Unchanged: the `VMRestart` branch runs first and the pod retires with `vm_restart_reprovision` | D2, TEST-15 |
| Runtime exits before the boundary report | The report carries `runtime_live: false` and the pod drains with `runtime_not_live`; a pod at a limit keeps that limit's reason | D4, SPEC-7 |
| Runtime exits during the `reserved` hold | The next `StartSession` fails at once; the pod drains (SPEC-7(a)) | D4, SPEC-7 |
| Runtime exits during a session | The session's Attach stream ends at EOF, the session fails, and §5.2 retires the pod | D4 |
| Runtime misses a heartbeat | `Interrupt` delivers nothing, and the Attach stream ends with DeadlineExceeded and the session fails. On a `maxConcurrentSessions: 1` pool §5.2 retires the pod on the failed session. On a concurrent pool the failed slot counts toward the §5.2 whole-pod replacement trigger (Slot retry policy); a hung runtime keeps its connection open, so at the occupancy-zero boundary it reports `runtime_live: true`, and the pod is reused with the hung runtime until that trigger fires | D3, D4, SPEC-2, SPEC-3(c), SPEC-14 |
| A `RuntimeProcess` without `ServesNextSession` | Reports `runtime_live: false`, and the pod retires at each boundary | D4 |
| Adapter receives SIGTERM | No `CH-RUNTIMEOPS` frame; the adapter closes `CH-RUNTIMEOPS`, the connection, and the listener, and the kubelet signals the runtime container | D8, D10 |
| Adapter SIGKILLed | The kernel closes the sockets, the runtime observes EOF, and the pod is terminating | D10 |
| Coordinator hold times out (sidecar pod) | Once the hold is cleared, and before the deregistration pass, the adapter runs the pod-scope teardown: the runtime reads EOF with no `terminate` frame, as on an adapter SIGKILL, `ended` is set, and a `Start` that runs after the teardown fails at once; the next report carries `runtime_live: false`. A fence that lands first leaves the runtime kept | D10, SPEC-2, SPEC-18, CODE-4, TEST-20 |
| Runtime at a session end | No frame arrives; the runtime is told nothing about the session's end | D3; §12, decisions 3 and 5 |
| Runtime built on the Go, Python, or TypeScript runtime SDK, acknowledged pool | Until the follow-up runtime-SDK proposal lands, a later session's messages reach the handler under the first session's `CreateRequest` and credential bundle, and its replies carry the frame's own `sessionId`; no checklist step waits for the follow-up, which lands before any release | §12, decision 3 |
| Second session on a kept process | `soleSession` is empty, so intra-pod MCP calls are refused with FailedPrecondition and direct-mode tokens fold to no session, as on a concurrent pod | D9; §12, decision 1 (proposal 0084) |
| Window after the cohort session closes and before the next start | `soleSession` is empty, and the pod MCP surface is cancelled | D9, CODE-3 |
| Pinned pod, claim from a different tenant, on a pool that keeps its runtime process, including an `allowCrossTenantReuse` `in-place` pool | Refused, never stamped, and left pinned for its tenant; the scan binds an admissible pod, or the acquisition ends as one that found no idle pod (the Postgres fallback, then the claim queue or the pool-exhaustion error); the pod leaves inventory as SPEC-8(c) states | D6, SPEC-8(c), CODE-8, CODE-10, TEST-12 |
| Pool whose only idle pods are pinned | `status.readyCount` is 0, the controller creates unpinned replacements, and `PoolWarmingUp` is True while they warm, so a create for the pinned tenant receives the 503 for that window | SPEC-8(c), CODE-10 |
| Pinned idle pod on a pool whose `maxWarm` equals its warm target | Drained on the reconcile after the hold expires; the tenant's next session reuses the runtime only inside the hold | SPEC-8(c), D14 |
| Scale-to-zero window or paused experiment (`minWarm` 0, `maxWarm` kept) | Up to `maxWarm` pinned idle pods stay until claimed, certificate-replaced, or retired; they accrue idle pod-minutes | SPEC-8(c) |
| Idle pod mid-acquisition (pin stamped, claim present, projection not landed) | Counted as unpinned idle inventory, as today, and never named for the pinned drain | CODE-10 |
| Service-mode pool | The planner reads no pin; router-labelled service pods count as today | CODE-10 |
| Postgres fallback claim | Reads the pin inside the row-locked transaction before claiming the mirror row, leaves the row unchanged on a refusal, stamps the drain request on a pod outside the process-reuse rule after `ClaimIdle` returns, and stamps the pin on success | D6, D16, CODE-8, TEST-13 |
| Pool edit that removes the acknowledgment | Refused while the pool stays under the SPEC-8(d) rule, and admitted when the same edit takes the pool outside it | D5, CODE-11, SPEC-8(d) |
| Admitted edit takes a pool outside the SPEC-8(d) rule while a pod is `reserved` | No acquisition rebinds it; the next claim on the pool ends the hold with the precondition-guarded `DELETE` and places the session on another pod, and the pod returns to `idle` pinned, where the next row applies | SPEC-8(d), D16, CODE-8, TEST-21 |
| Admitted edit takes a pool outside the rule while a pod is pinned idle | Every acquisition that reads it refuses it and stamps its drain request when no `SandboxClaim` holds it; the Postgres fallback leaves its row `idle` | SPEC-8(d), D16, CODE-8, TEST-21 |
| Session bound when that edit lands | Completes in its pod; no acquisition admits the pod afterwards, and it retires at its boundary (§9.1) | SPEC-8(d), §9.1 |
| Edit lands between a session's pool resolution and its claim | The claim and the session's `sessionIsolationLevel` both follow the configuration resolved before the edit | SPEC-8(d), D16 |
| Embedded recycling pod | The adapter process is kept and the loop runs once per session; the embedded mains wire no `ScrubOps`, so the report is withheld and the pod retires by timeout, which predates this proposal | D1, §9.1 |
| `sessionIsolationLevel` | As D12 and SPEC-8(d) state | D12, SPEC-8(d), SPEC-10 |
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
or sends the process a signal. The adapter closes the connection when the pod
terminates, and when the coordinator hold times out with no new coordinator and
the adapter terminates every session it started on the pod
([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)). The pod's
termination ends the process. In the
embedded model the runtime process is the adapter process, which also lives as
long as the pod, and the adapter runs one runtime loop per session inside it. On a pool whose runtime declares `preConnect`, the SDK process, which
[Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) calls
the agent process and which `DemoteSDK` tears down and the SDK re-warm restarts, is
distinct from the runtime process, and neither ends the runtime process. A
runtime process that stops, or whose connection the hold timeout closed, is not
re-created or reconnected inside the pod: the adapter
reports it at the next recycle boundary and refuses the next session's start,
and the pod is retired
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
A recycling pool whose pods serve more than one session in the kept process
requires a deployer acknowledgment
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
It flushes the session's final usage report and then ends the session's use of the pod's runtime process, which stays alive for the pod's life ([Section 4.7.10](#4710-deployment-model)). The teardown writes no `CH-RUNTIMEOPS` frame ([Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine)).
```

(b) In the same row, replace `the adapter keeps the pod process alive across the recycle boundary` with `the adapter keeps its own process and the runtime process alive across the recycle boundary`.

(c) In the `ReportPodScrub` row, insert after its first sentence (ending `on a recycling pool ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`):

```
The request also states whether the adapter's runtime process can serve the next session, sampled after the scrub finishes; a sidecar runtime process whose connection is open is reported as able to, whether or not it answers heartbeats.
```

and insert after the sentence ending `rather than reserving or re-warming ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).`:

```
A pod retired under the Pod retirement policy's "Runtime not live" condition takes the same terminal retire ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

**SPEC-4 — `spec/04_system-components.md` §4.6.1 and §4.6.3.** Each part names a §5.2 Pod retirement policy condition by its label under D15, states the pin scope, or states that the acquisition path ends a reserved hold on a pool outside the process-reuse rule, and cites §5.2 for the rule.

(a) In §4.6.3, the `released` bullet, replace `or the `vm-restart` recycle-boundary reprovision` with `, the `vm-restart` recycle-boundary reprovision, or a retire under the Pod retirement policy's "Runtime not live" condition`.

(d) In §4.6.3, the **Gateway ServiceAccount RBAC grants:** paragraph, replace `and allows the WarmPoolController SA to reset `{tenant_id} → unassigned` only on a pool whose microvm-gated `recycle.allowCrossTenantReuse` permits cross-tenant reuse ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)); on every other pool the pin persists for the pod's lifetime, across the recycle-to-idle edge.` with:

```
and allows the WarmPoolController SA to reset `{tenant_id} → unassigned`. No component performs that reset on a recycled pod: the pin persists for the pod's lifetime on every pool, across the recycle-to-idle edge, and a pod on a pool with microvm-gated `recycle.allowCrossTenantReuse` is never reused across tenants ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

In the same paragraph, delete ` when a pod crosses the unhealthy threshold`, keeping the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) citation that follows.

(g) In §4.6.1, **Reserved hold (claim retention across same-tenant sessions):**, replace the last sentence, from `The `lenny.dev/tenant-id` pin persists across the recycle-to-idle edge` through `available to that tenant alone.`, with `[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the `lenny.dev/tenant-id` pin rule for a recycled idle pod, including its claim and inventory accounting.`

(h) In §4.6.1, **Reserved hold (claim retention across same-tenant sessions):**, after `If the TTL expires first, the holder deletes the claim and the pod returns to `idle` with no second re-warm.` insert `On a pool whose configuration keeps no runtime process across sessions, the acquisition path ends the hold with the same precondition-guarded `DELETE` instead of rebinding it ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").` In the occupancy-projection list, replace `` `idle` when the claim is deleted while the pod projects `reserved` (hold expiry). `` with `` `idle` when the claim is deleted while the pod projects `reserved` (hold expiry, or a hold the acquisition path ends under [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). `` (h) and (g) edit different sentences of the same paragraph; apply (g) after (h). In §4.6.1, the CRD table's `SandboxClaim` row, replace `and deleted when the reserved hold expires or the pod terminates;` with `and deleted when the reserved hold expires, when the acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), or when the pod terminates;`, and in the same row replace `(hold expiry, orphan GC, or pod termination)` with `(hold expiry, a hold the acquisition path ends, orphan GC, or pod termination)`. In §4.6.3, the CRD field-ownership table's `SandboxClaim` row, replace `deleted by the gateway at hold expiry or by the WarmPoolController at pod termination and orphan GC` with `deleted by the gateway at hold expiry or when its acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), or by the WarmPoolController at pod termination and orphan GC`. In §4.6.3, the `reserved` binding-state bullet, replace `held for its pinned tenant until `holdExpiresAt`;` with `held for its pinned tenant until `holdExpiresAt` or until an acquisition ends the hold ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes));`.

**SPEC-5 — `spec/05_runtime-registry-and-pool-model.md` §5.1 setup commands and Runtime definition example, and §5.2 recycle lifecycle and scrub procedure.**

(a) In **Recycle lifecycle (`recycle.enabled: true`)**, replace `per-session setup belongs in the runtime's initialization.` with `per-session setup belongs in the runtime's handling of the session's first message, because a runtime process kept across sessions initializes once per pod.` In §5.1 **Setup Commands and Policy**, replace `Per-task setup belongs in the runtime's initialization.` with `Per-task setup belongs in the runtime's handling of the session's first message ([Section 5.2](#52-pool-configuration-and-execution-modes)).`

(b) Replace the **Recycling and integration levels** paragraph with:

```
**Recycling and integration levels.** Pod recycling requires no CH-RUNTIMEOPS
exchange between sessions: the per-slot cleanup and the whole-pod scrub are
adapter-executed and gateway-coordinated. The runtime process is kept across the
recycle boundary ([Section 4.7.10](04_system-components.md#4710-deployment-model)),
so a runtime serves each later session on the same connection, keyed by the frame's
`sessionId`, as a runtime on a concurrent pool does, and
`recycle.maxSessionsPerPod` bounds the number of sessions one runtime process
serves. A runtime that exits after its session makes the pod retire at the
recycle boundary rather than serve the next session (see the Pod retirement
policy below, "Runtime not live"). Recycling is admitted at
every integration level (Basic, Standard, and Full).
```

(c) In **Lenny scrub procedure**, replace `The adapter closes the ending session's runtime, keeps the pod process alive across the recycle boundary,` with `The adapter ends the ending session's use of the runtime, keeps its own process and the runtime process alive across the recycle boundary,`. Replace `On a `standard` or `in-place` pool the pod then keeps the process alive and reuses it for the next session;` with `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process;`.

(d) In the §5.1 Runtime definition example, insert `  acknowledgeProcessLevelIsolation: true # required on this pool (see the runtime-process acknowledgment in Section 5.2)` between `sessionPolicy:` and `  recycle:`, so the example stays admissible.

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

(a) Append to the **Pod retirement policy (recycling pools)** list, after the **Scrub failure limit** item, the item below, and follow it with the paragraph below. The item and the paragraph each land as one physical line, as the existing items do:

```
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

The condition is evaluated before the host-node schedulability retire
([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), and it takes the
terminal retire the `vm-restart` reprovision takes: after the scrub report the
pod projects `claimed → draining` on preConnect and non-preConnect pools alike
rather than entering the SDK re-warm or the `reserved` hold, the claim's binding
state becomes `released`
([Section 4.6.3](04_system-components.md#463-crd-field-ownership-and-write-boundaries)),
no `rewarmStartedAt` stamp is recorded, and on a `warn`-policy scrub failure the
`scrub_warning` annotation is recorded on the retired pod.
```

In the **Uptime limit** item, replace `if omitted, only `maxSessionsPerPod` and `maxScrubFailures` govern retirement` with `if omitted, the other conditions in this list govern retirement`.

(c) Append to the `session` item of **Mode adjustment factor (`mode_factor`)**, after the concurrent `vm-restart` sentence: `On a sequential recycling pool a pod serves only its pinned tenant, and across an occupancy-zero boundary only while the pod is held for that tenant (see the tenant pinning paragraph above), so the estimate converges toward `recycle.maxSessionsPerPod` only for a workload whose tenants return to their pods within that window, and the observed p50 governs otherwise.`

(d) In **Caveats**, replace `This convergence toward `recycle.maxSessionsPerPod` applies to `standard` and `in-place` recycling pools.` with `This convergence toward `recycle.maxSessionsPerPod` applies to `standard` and `in-place` recycling pools, on the tenant-return condition the `session` item above states.` In the same bullet's **Integration level consideration:**, replace `and recycling requires no runtime cooperation ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed.` with `and recycling requires no CH-RUNTIMEOPS exchange ([Execution Modes](#execution-modes)), so no cross-level adjustment is needed.`

**SPEC-8 — `spec/05_runtime-registry-and-pool-model.md` §5.2, acknowledgment, tenant pin, and client visibility.**

(a) In the YAML block, replace `acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1 — see the concurrent-session acknowledgment below` with `acknowledgeProcessLevelIsolation: false # required when maxConcurrentSessions > 1, and on a recycling pool that keeps its runtime process across sessions (see the acknowledgments below)`.

(b) Replace the bullet `- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`; concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below).` with `- `acknowledgeProcessLevelIsolation: true` is required when `maxConcurrentSessions > 1`, because concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack (see the concurrent-session acknowledgment below). It is also required on a recycling pool that keeps its runtime process across sessions (see the runtime-process acknowledgment below).`

(c) In item 2 of **Tenant pinning:**, delete ` (pod return to pool)`. Replace the paragraph that begins `The pin persists across the recycle-to-idle edge for the pod's lifetime on pools without microvm-gated` and ends `The `{tenant_id} → unassigned` transition applies only where cross-tenant reuse is permitted.` with:

```
The pin persists across the recycle-to-idle edge for the pod's lifetime on every
pool, including a pool with microvm-gated `recycle.allowCrossTenantReuse`,
because a recycled pod keeps the runtime process that served its tenant
([Section 4.7.10](04_system-components.md#4710-deployment-model)). A recycled pod
never takes the `{tenant_id} → unassigned` transition. The gateway reads the pin
on every idle-pod acquisition, including the Postgres-backed fallback claim
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)),
before it creates the claim and passes over a pod the pin refuses to the next
idle candidate; the fallback claim stamps the pin at first assignment as the
primary claim path does. A pod the pin refuses stays pinned for its tenant on
every pool whose pods keep their runtime process, including a pool with
`recycle.allowCrossTenantReuse: true`, and the acquisition writes nothing to it.
The runtime-process acknowledgment below states when an acquisition drains a
refused pod.

**Pinned idle inventory.** A session-mode pod in `idle` whose
`lenny.dev/tenant-id` label is non-empty and for which no SandboxClaim exists is a
pinned idle pod. It is claimable only by its pinned tenant, and the
WarmPoolController accounts for it apart from the pool's unpinned inventory:

- It does not count toward `minWarm`. `status.warmCount` and `status.readyCount`
  count unpinned pods only, as do the `idlePodCount` and the `PoolWarmingUp`
  condition derived from them and the `lenny_warmpool_idle_pods` gauge
  ([Section 16.1](16_observability.md#161-metrics)). The controller therefore
  provisions unpinned pods for other tenants while pinned pods sit idle, and a
  pool whose only idle pods are pinned reports `PoolWarmingUp` while those
  replacements warm.
- It counts toward `maxWarm`. The controller keeps at most `maxWarm` minus the
  pool's warm target (`minWarm`, capped at `maxWarm`) pinned idle pods and drains
  the rest. The bound follows the current `minWarm`, so it widens while a
  scale-to-zero window or a paused experiment holds `minWarm` at 0, and it is
  zero when `minWarm` and `maxWarm` are both 0.
- It accrues `lenny_warmpool_idle_pod_minutes`, which counts every idle pod.

A recycled pod is therefore held for its pinned tenant while it is `reserved`
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle),
`gateway.claimHoldTTLSeconds`) and afterwards while it fits under that bound. On
a pool whose `maxWarm` equals its warm target the bound is zero, so a recycled
pod serves its tenant again only within the reserved hold and is drained when it
returns to `idle`. A deployer widens the window by setting `maxWarm` above
`minWarm` or by raising `gateway.claimHoldTTLSeconds`. A pinned idle pod has no
idle timeout of its own. It leaves inventory when its tenant claims it, when the
bound drains it, when the idle-pod certificate replacement drains it
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)),
when the Pod retirement policy below retires it, or when an acquisition drains
it on a pool outside the runtime-process acknowledgment rule (see the
runtime-process acknowledgment below). A per-pool bound on pinned idle pods
beyond `maxWarm` and an idle timeout for pinned pods are future work.
```

(d) Insert after the **Deployer acknowledgment (concurrent sessions).** paragraph, keeping that label, which `pkg/admission/pool_config_validator/validator.go:377` cites:

```
**Deployer acknowledgment (runtime process kept across sessions).** The runtime
process lives as long as the pod
([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime
process lifetime"), so a later session on a recycled pod runs in the process
that served the earlier one, and that process carries into the later session
the state the whole-pod scrub does not reach (see "What the scrub reaches"
above). Deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true`
on a pool with `recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, and
a `recycle.scrubProfile` other than `vm-restart`. If the field is absent or
`false` on such a pool, the pool controller rejects the pool definition at
validation time with a descriptive error referencing this section, and it
rejects an update that would leave the pool in that state, including one that
removes the field. An admitted update that takes a pool outside the rule ends
reuse at the next acquisition. The gateway admits a session to a pod that has
served an earlier session only when the pool configuration it resolves for that
claim requires this field, which is a pool with `maxConcurrentSessions` above 1
or the recycling configuration above; the claim made at session creation uses
the resolution that computes the session's `sessionIsolationLevel`
([Section 7.1](07_session-lifecycle.md#71-normal-flow)). A pod has served an
earlier session when it is `reserved` or its `lenny.dev/tenant-id` label is
set. When the resolved configuration does not require this field, every
acquisition refuses such a pod, including the `reserved → bound` rebind and the
Postgres-backed fallback claim. The gateway ends a `reserved` pod's hold with
the precondition-guarded claim `DELETE` that hold expiry uses
([Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)),
and the pod returns to `idle` pinned to its tenant. Every acquisition that
refuses a pinned `idle` pod under this rule stamps its `lenny.dev/drain-request`
annotation when no SandboxClaim holds it, whether or not the acquisition then
binds another pod, and the pod retires. A session already bound when
the update lands completes in its pod, and no acquisition admits that pod
afterwards. The recycling configurations outside the rule keep no runtime
process across sessions: with `recycle.maxSessionsPerPod: 1` the pod retires
after its first session on the session count limit, and with
`recycle.scrubProfile: vm-restart` the pod retires at every occupancy-zero
recycle boundary (see the Pod retirement policy above). The rule holds in both
deployment models, because an embedded runtime runs in the adapter's process,
which the pod also keeps. A pool with `maxConcurrentSessions > 1` already
requires the field (see the concurrent-session acknowledgment above). A kept
runtime process serves one tenant; the tenant pinning paragraphs above state how
the gateway enforces that.
```

(e) In **Client visibility of weak-isolation reuse.**, replace `may be observable from prior sessions; concurrent slots share process-level state;` with `may be observable from prior sessions, and on a recycling pool that keeps its runtime process across sessions the session may run in a runtime process that served an earlier session of the same tenant (see the runtime-process acknowledgment below); concurrent slots share process-level state;`, and replace `the session runs on a pod that serves more than one session over its lifetime` with `the session runs on a pool configured to reuse pods`

(f) In the paragraph that begins `Cross-tenant pod reuse is only permitted on the sequential-reuse path`, replace its first two sentences, from `Cross-tenant pod reuse is only permitted on the sequential-reuse path` through `but it is not equivalent to dedicated hardware isolation.`, with the text below, and keep the sentences that follow (`The pool controller rejects ...` and `Cross-tenant slot sharing ...`):

```
`recycle.allowCrossTenantReuse: true` applies only to the sequential-reuse path (`maxConcurrentSessions: 1` with `recycle.enabled: true`) with `microvm` isolation, where the VM boundary provides a VM-level isolation boundary that is significantly stronger than runc or gVisor but shares host virtio devices. A pod on a pool that sets the field is never reused across tenants, because a recycled pod keeps the runtime process that served its pinned tenant. The field therefore does not change which tenant a pod serves: an acquisition refuses a pod pinned to another tenant and leaves it pinned (see the tenant pinning paragraph above). Serving a second tenant on such a pool requires restarting the runtime process and resetting the tenant pin, which the platform does not do.
```

(g) In the scrub-profile paragraph under **Kata/microvm scrub variant.**, replace `such pools set `vm-restart`, or `in-place` when the deployer requires cross-tenant reuse without the warm-pod reprovision cost. In `in-place` mode the following residual state vectors are documented as persisting across tenant boundaries:` with:

```
such pools set `vm-restart` or `in-place`. Neither profile reuses a pod across tenants: a `vm-restart` pool retires the pod at every recycle boundary, and an `in-place` pod keeps its continuing guest across its pinned tenant's later sessions and serves no other tenant (see the tenant pinning paragraph above). In `in-place` mode the following residual state vectors are documented as persisting across the pinned tenant's sessions in the continuing guest:
```

(h) In the table under `Common configurations of the block:`, replace the `Pod reuse` row's behavior cell `The pod serves sequential sessions of one tenant, with a whole-pod scrub between sessions.` with `The pod serves sequential sessions of one tenant in one runtime process, with a whole-pod scrub between sessions (see the runtime-process acknowledgment below).`

(i) In the **Deployer acknowledgment.** YAML example, insert `  acknowledgeProcessLevelIsolation: true # required on this pool (see the runtime-process acknowledgment below)` between `sessionPolicy:` and `  recycle:`, because the example sets `maxSessionsPerPod: 50` on a `standard` pool.

(j) In the **T4 cross-tenant reuse prohibition.** paragraph, replace `the assignment is rejected and the pod is retired from the cross-tenant pool` with `the assignment is rejected`. SPEC-8(c) states that a refused pod stays pinned for its tenant, and SPEC-8(d) states when an acquisition drains a refused pod.

**SPEC-9 — `spec/06_warm-pod-model.md` §6.1 and §6.2.**

(a) In the §6.1 `session`, `maxConcurrentSessions: 1`, `recycle.enabled: true` row, replace `At the occupancy-zero recycle boundary the whole-pod scrub terminates the SDK process along with all other session processes;` with `At the occupancy-zero recycle boundary the ending session's use of the runtime ends and the runtime process is kept ([Section 4.7.10](04_system-components.md#4710-deployment-model));`

(c) In the §6.2 recycle-edge block, in the `claimed ──→ draining` recycle edge, replace `an unschedulable host node; or a vm-restart pool reprovisioning a fresh` / `guest at the recycle boundary)` with `an unschedulable host node; a vm-restart pool reprovisioning a fresh` / `guest at the recycle boundary; or the §5.2 Pod retirement policy retire` / `"Runtime not live")`, keeping the block's column alignment.

(e) In the §6.2 occupancy-projection block, in the `idle ──→ draining` edge, replace `level-triggers the drain from the pod CreationTimestamp,` / `regardless of session activity)` with `level-triggers the drain from the pod CreationTimestamp,` / `regardless of session activity; or gateway-stamped` / `lenny.dev/drain-request annotation — see §5.2)`, and in the `claimed ──→ draining` edge above it replace `lenny.dev/drain-request annotation — unhealthy threshold)` with `lenny.dev/drain-request annotation — see §5.2)`, keeping the block's column alignment.

(f) In **preConnect re-warm on scrub_warning.**, insert after the sentence that ends `and the gateway provisions a fresh replacement (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) step 7).`, without rewording it:

```
A pod retired under the Pod retirement policy's "Runtime not live" condition takes the same retire on both outcomes ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

(g) In §6.1 **Pod-warm (default):**, replace `The agent process is NOT started.` with `No session is bound, and no message for the next session has been delivered.`, keeping the sentences that follow.

(h) In §6.1 **One-session-per-pod default and the recycle opt-in.**, replace `with explicit deployer acknowledgment (`acknowledgeBestEffortScrub`):` with `with explicit deployer acknowledgments (`acknowledgeBestEffortScrub`, and `acknowledgeProcessLevelIsolation` on a pool that keeps its runtime process across sessions):`.

(i) In §6.1 **Per-session credential lease lifecycle.**, replace `and a recycling pod does not retain credentials between sessions.` with `and a recycling pod does not retain credentials between sessions outside a kept runtime process ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

(j) In the §6.2 recycle-edge block, replace the `reserved ──→ idle` edge's label lines `(hold TTL expires — precondition-guarded claim DELETE; the pod is` / `already scrubbed and SDK-warm, no second re-warm)` with `(hold TTL expires, or the acquisition path ends the hold on a pool` / `that keeps no runtime process across sessions — see §5.2;` / `precondition-guarded claim DELETE; the pod is already scrubbed and` / `SDK-warm, no second re-warm)`, keeping the block's column alignment. In **`reserved` hold semantics.**, after `and the pod returns to `idle` with no second re-warm.` insert `On a pool whose configuration keeps no runtime process across sessions, the acquisition path ends the hold with the same precondition-guarded `DELETE` instead of rebinding it ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

(k) In §6.2 **Concurrent pod lifecycle**, in the **Partial occupancy.** bullet, replace `transitions to `idle` only when the hold expires and the claim is deleted` with `transitions to `idle` only when the claim is deleted at hold expiry or when an acquisition ends the hold`.

**SPEC-10 — `spec/07_session-lifecycle.md` §7.1, `podReuse` and `residualStateWarning` rows.** In the `podReuse` row, delete `when the assigned pool reuses pods across executions: `. In the `residualStateWarning` row, replace `For recycling pools, residual-state vectors include the DNS resolver cache, TCP `TIME_WAIT` state, and page cache from prior sessions.` with `For recycling pools, residual-state vectors include the DNS resolver cache, TCP `TIME_WAIT` state, and page cache from prior sessions, and on a recycling pool that keeps its runtime process across sessions the session may run in a runtime process that served an earlier session of the same tenant (see [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

**SPEC-11 — `spec/15_external-api-surface.md` §15.4, §15.4.1, §15.4.2, and §15.4.3.**

(a) In §15.4, replace `the platform scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime cooperation and is available at every integration level.` with `recycling needs no CH-RUNTIMEOPS exchange and is available at every integration level, on the terms [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Recycling and integration levels** states.`

(b) In the §15.4.2 state table, in the `DRAINING` description, replace ` and signals the agent to stop.` with `. No drain coordination exists at pod exit at any integration level: the adapter writes no `CH-RUNTIMEOPS` `terminate` frame, and the runtime process ends with the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, so the description begins `Graceful shutdown requested. The adapter finishes the current exchange. No drain coordination exists`. In the diagram above the table, replace the return edge from `ACTIVE` to `TERMINATED` labelled `(session ends normally)` with a return edge from `ACTIVE` to `READY` labelled `(session ends; the pod is recycled)`. In the sentence after the table, replace `readiness signal, exit on completion)` with `readiness signal)`.

(c) In §15.4.3, locating each site by its quoted text (each matrix row is one physical line padded with spaces, and stays one line):

- In the **Basic** list, replace `failure to ack within 10 seconds causes SIGTERM` with `failure to ack within 10 seconds ends the session ([Section 28.5.3](28_communication-channels.md#2853-intra-pod))`.
- In the matrix `Interrupt` row, replace the Basic cell `No clean interrupt. Gateway sends SIGTERM; runtime has no opportunity to reach a safe stop point.` with `No clean interrupt, and the runtime process receives no signal ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, and the Standard cell `No clean interrupt. Same SIGTERM-based termination as Basic.` with `No clean interrupt. Same as Basic.`
- In the matrix `Deadline / expiry warning` row's Basic cell, delete `; Basic-level receives only `shutdown` at expiry`, so the cell ends at `requires CH-RUNTIMEOPS.`
- In **Basic-level limitations**, the **Clean interrupt:** bullet, replace `The gateway issues `shutdown` on stdin and follows with SIGTERM after `deadline_ms`; the runtime cannot acknowledge an interrupt cleanly.` with `The runtime cannot acknowledge an interrupt cleanly.`
- In the same list, the **`DEADLINE_APPROACHING` warning:** bullet, replace `Basic-level runtimes receive only the `shutdown` message at expiry with no advance notice.` with `Basic-level runtimes receive no advance notice of a session's expiry.`
- In the **Full** list, delete the bullet `- `DRAINING` state with graceful shutdown coordination`.
- In the matrix `Graceful drain (`DRAINING` state)` row, replace the Full cell `` `DRAINING` state via CH-RUNTIMEOPS enables graceful shutdown coordination before `shutdown`. `` with `No drain coordination at pod exit ([Section 15.4.2](#1542-rpc-lifecycle-state-machine)).` The Basic and Standard cells keep their wording.
- In **Basic-level limitations**, delete the **Graceful drain (`DRAINING` state):** bullet. The list's closing sentence sends a runtime author who needs a listed capability to Standard or Full level, and no level coordinates a drain.

(d) In §15.4.1, delete ` — the runtime knows it's receiving its first message by virtue of just having started`, so the sentence reads `No `sessionState` field.`

**SPEC-12 — `spec/29_communication-scenarios.md` §29.2, §29.4, §29.9, and §29.10.**

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

In steps 26a, 26b, 26c, 27, and 28, replace the opening `On a pod-warm pod` with `On the first session of a pod-warm pod`, keeping each step's number and the rest of its text, breaking only the lengthened opening line and leaving every later line as it is, because a tier-11 test (`TestIntraPodMCPNonceStatementsAgreeAcrossSpecAndDocs`) finds step 26a's `The runtime reads the manifest and connects to the platform MCP` on one physical line.

(b) In §29.4, replace step 13 (from `13. On a session end against a Full-level runtime, triggered by` through `[§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row states for the graceful-shutdown signal.`) with:

```
13. On a session end triggered by `POST /v1/sessions/{id}/terminate`, by `DELETE /v1/sessions/{id}`, or
    by an expiry timer: `adapter`, `internal`. The adapter writes no `CH-RUNTIMEOPS` frame and sends the
    runtime process no signal ([§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row,
    [§4.7.10](04_system-components.md#4710-deployment-model)).
```

(c) In §29.4 step 12, replace `the graceful end-of-session shutdown of the pod's runtime` with `the graceful end-of-session teardown of the named session`, replace `the adapter keeps the pod process alive across the recycle boundary` with `the adapter keeps its own process and the runtime process alive across the recycle boundary`, and replace `On the default disposition the adapter closes the session runtime and the pod is replaced.` with `On the default disposition the pod is replaced.`

(d) In §29.4 step 17, replace `retires the pod when the recycle limits or the host-node schedulability check say so` with `retires the pod when the Pod retirement policy or the host-node schedulability check says so`, keeping the citations that follow and the step's wrap width.

(e) In §29.4 step 6, replace `so this step does not occur and the interrupt degrades to SIGTERM-based termination with no opportunity for the runtime to reach a safe stop point` with `so this step does not occur and the interrupt degrades as the `Interrupt` row states`, keeping the citation that follows.

(f) In §29.9, delete step 4b (from `4b. `unstated`. The specification names `eviction`` through `and it does not fix the relative order of steps 4a and 4b.`) and the blank line before it. Renumber `4a.` as `4.` and re-indent its continuation lines from four spaces to three, as step 5's are. With 4b gone no step is unordered against step 4, so the §29 **Step numbering.** rule gives it no letter suffix. The path sends the runtime no `CH-RUNTIMEOPS` frame, as §15.4.2 states.

(g) In §29.2 step 7, after the sentence that begins `When the pod is instead` / `held for the same tenant under a `reserved` claim within its hold window` and ends with its closing citation `([§4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle)).`, insert `On a pool whose configuration keeps no runtime process across sessions, the gateway ends that hold with the same precondition-guarded claim `DELETE` instead and acquires another pod through steps 6 and 7 ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`, wrapped to the step's width with its three-space continuation indent.

(h) In §29.10 **The condition.**, delete the sentence `Nothing in this subsection applies to a pod serving one session at a time, because the co-tenancy it analyses requires two sessions sharing one pod.`, leaving the text around it unchanged.

**SPEC-13 — `spec/11_policy-and-controls.md` §11.4, the full-revoke propagation mechanism, step 3.** Replace `3. The pod's runtime adapter initiates graceful shutdown (SIGTERM to agent, wait up to 10s, then SIGKILL).` with:

```
3. The pod's runtime adapter tears down the session ([Section 4.7](04_system-components.md#47-runtime-adapter), `Shutdown`), which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").
```

**SPEC-14 — `spec/28_communication-channels.md` §28.3, §28.5.3, and §28.8.** Locate each site by its quoted text, because the §28.5.3 sentences wrap across lines. Each §28.3 and §28.8 row stays one physical line.

(a) In the `CH-MSGSOCK` **Timing.** bullet, replace `is treated as hung and is sent SIGTERM` with `is treated as hung: the adapter ends that session's stream, and the runtime process receives no signal ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime")`, keeping the citation that follows.

(b) Under **Inbound: `heartbeat`**, replace `the adapter considers the process hung and sends SIGTERM.` with `the adapter considers the process hung and escalates as the `CH-MSGSOCK` **Timing.** bullet states.`

(c) In the `CH-RUNTIMEOPS` **Degradation.** bullet, replace `interrupt degrades to SIGTERM-based termination and the deadline warning and the drain coordination signal are not delivered` with `interrupt degrades as the [§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels) `Interrupt` row states, and the deadline warning is not delivered`.

(d) In the §28.8 `CH-RUNTIMEOPS` row, make the replacement (c) makes.

(e) In the §28.8 `CH-MSGSOCK` row, replace `is treated as hung and is sent SIGTERM` with `is treated as hung, as the §28.5.3 `CH-MSGSOCK` **Timing.** bullet states`.

(f) In the **Annotated Protocol Trace — Basic-Level Session** block, replace step 1, `1. Adapter starts agent binary, stdin/stdout pipes open.`, with `1. The runtime's connection to the adapter is open; the runtime dialled it on the pod's first session.` Steps 8 through 10 keep their wording (§9.1).

(g) In the §28.3 register-entry register `REG-CLAIM` row's writer cell, replace `and the hold-expiry delete,` with `and the deletes at hold expiry and when an acquisition ends the hold,`.

§28's sentence "refuses the call unless that process has been given exactly one session" binds across recycle boundaries once the process is kept. The §28.5.3 sentences that say the adapter spawns the runtime binary before a manifest-dependent step, and the §28.5.3 and §28.8 `shutdown`, SIGTERM-on-timeout, and exit-code sentences, keep their wording (§9.1).

**SPEC-15 — `spec/13_security-model.md` §13.1, credential-read boundary.**

(a) In **`lenny-cred-readers` membership boundary.**, replace `except on pools with `sessionPolicy.maxConcurrentSessions > 1` (see the per-slot credential-read clause below).` with `except on pools with `sessionPolicy.maxConcurrentSessions > 1` (see the per-slot credential-read clause below) and on recycling pools that keep a runtime process across sessions ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)").`

(b) In the same paragraph, delete the sentence `On a pod that serves one session at a time (`maxConcurrentSessions: 1`, with or without recycling), the subprocess-inheritance surface is inside the trust boundary the session already owns.`

(c) In **Per-slot credential-read scope (`maxConcurrentSessions > 1`).**, replace `MUST set `sessionPolicy.maxConcurrentSessions: 1`: the pod then holds at most one credential lease at a time, and sequential pod reuse under `recycle.enabled: true` keeps that property because each session's lease is released before the next session begins ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).` with `MUST set `sessionPolicy.maxConcurrentSessions: 1`, and on a recycling pool set `recycle.maxSessionsPerPod: 1` or `recycle.scrubProfile: vm-restart` ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), "Deployer acknowledgment (runtime process kept across sessions)"): the pod then holds at most one credential lease at a time.`

**SPEC-16 — `spec/16_observability.md` §16.1, the `lenny_warmpool_idle_pods` and `lenny_warmpool_reserved_pods` rows.** Replace `number of pods in `idle` state ready to be claimed;` with `number of pods in `idle` state ready to be claimed, excluding idle pods pinned to a tenant ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes));`. In the `lenny_warmpool_reserved_pods` row, replace `until the claim-hold TTL expires,` with `until the claim-hold TTL expires or an acquisition ends the hold,`. Each row stays one physical line.

**SPEC-17 — `spec/17_deployment-topology.md` §17.8.2, **First-week monitoring workflow.**** In the `lenny_warmpool_idle_pod_minutes` bullet, replace `` `minWarm` is oversized; reduce by 25%.`` with `` `minWarm` is oversized; reduce by 25%. On a recycling pool whose `maxWarm` exceeds `minWarm`, the counter also accrues idle pods pinned to a tenant ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), tenant pinning), so compare against `maxWarm × 30` there.``

**SPEC-18 — `spec/10_gateway-internals.md` §10.1.4, the **Hold state timeout:** bullet.** (a) Delete `it sends `terminate` on the CH-RUNTIMEOPS and `, so the clause reads `the adapter initiates graceful session termination — it emits a `session.terminated` event with reason `coordinator_lost``. (b) Insert after the sentence ending `(or writes it to local disk for post-mortem if no coordinator ever returns).`: `When the hold times out in the sidecar deployment model, the adapter also closes the runtime's `CH-MSGSOCK` connection, so a runtime process the pod keeps across sessions does not go on holding a terminated session's credentials with no coordinator; that pod serves no later session and is retired ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime process lifetime**).` The sentence names no retire reason, so D15's single statement in §5.2 stands.

## 9. Non-goals

### 9.1 Out of scope

- **Re-creating a runtime process inside the pod.** A runtime that stops ends the pod's service through the `runtime_not_live` retire or the failed-start drain.
- **SDK support for sequential sessions, and a per-session end signal to the runtime.** A follow-up runtime-SDK proposal changes the Go, Python, and TypeScript runtime SDKs to serve sequential sessions keyed by `sessionId`, with any end-of-session signal that change needs, and it lands before any release; no checklist step of this proposal waits for it (§12, decision 3). The follow-up also corrects the SDK doc comments that state one create-handler call per runtime process and a runtime exit between recycled sessions (`sdks/runtime/go/runtime/runtime.go:80-84`, `sdks/runtime/go/runtime/types.go:205-214`, and their Python and TypeScript counterparts), because those comments describe the SDK behaviour that proposal changes. The follow-up also owns the §15.7 Go `CreateRequest` snippet's `SessionID` comment ("the session this runtime instance is bound to") and `TaskID` comment ("OnCreate is invoked once with this value"), which those code comments mirror, and this proposal keeps their wording. This proposal edits no file under `sdks/` (§12, decision 7). The §15.4.6 conformance category **deadline signal handling**, whose runtime exits on a session's deadline signal (its fixture in `cmd/lenny-compliance/full.go` sends a `terminate` frame), belongs to the same contract and keeps its wording.
- **Per-session attribution for intra-pod MCP and the direct-mode token fold on a multi-session process.** Proposal 0084 carries it (§12, decision 1).
- **The per-release `maxSessionsPerPod` drain and its `vm-restart` exclusion** (`pkg/gateway/session/recycle/scrubreporter_seams.go:344-373`). This proposal leaves the drain and its exclusion unchanged.
- **The `SocketRuntimeProcess` listener teardown.** Proposal 0078 carries it and lands first.
- **Per-slot cleanup**, which runs at every session release and does not depend on the runtime's lifetime.
- **The §16.1 retirement-counter row.** The row omits the existing non-counting reasons `host_unschedulable`, `scrub_report_timeout`, and `vm_restart_reprovision`. File it separately; the new reason is named in §5.2.
- **The first-session manifest ordering on the sidecar transport.** The runtime reads the manifest before the adapter writes it on a pod's first session. It is a pre-existing defect on every sidecar pod, filed to be fixed after proposals 0078 and 0079 land. The sentences that describe the manifest as written "before spawning" belong to it and keep their wording here: the §4.7 **Adapter manifest:** paragraph ("before the runtime binary is spawned" and "before each session's runtime start"), the manifest tool-list field, the manifest `credentialsPath` field, §4.7.11 items 1 and 4, the `SO_PEERCRED` self-test ("before any agent process is spawned"), §15.4.3 **Authentication.**, the §15.7 `CreateRequest` comment, the §6.1 **Per-session credential lease lifecycle.** paragraph ("before that session's binary is spawned"), and the §28.5.3 sentences that place the manifest write before the adapter spawns the runtime binary (the **Preconditions.** bullets of `CH-MSGSOCK`, `CH-RUNTIMEOPS`, `CH-MCP-PLATFORM`, and `CH-MCP-CONNECTOR`, and the two `adapterLocalTools` sentences under `CH-MSGSOCK` that say "before spawning the runtime"). The review log's blast-radius table lists every site of this class (P3), with its mirrors under `docs/`, `schemas/`, and the code comments, and those sites keep their wording as well. §29.2 step 25 is not among them: it states the runtime's `CH-MSGSOCK` dial rather than the manifest ordering, and SPEC-12 rewrites it.
- **Adapter signals, the `shutdown` frame, and exit observation that the sidecar model never delivered.** Before this proposal and after it, the adapter neither signals the sidecar runtime container, nor writes a `CH-MSGSOCK` `shutdown` frame, nor reads the runtime container's exit code or stderr (summary.md, Defects in the shipped tree). The sentences that state those acts, and their mirrors under `docs/` and `schemas/`, keep their wording here, and no staged text restates them. The review log's blast-radius table lists every site (class P1, P2). After this proposal no adapter path writes `terminate` (D8), so none of its reason values has an emitter (`budget_exhausted` and `operator` had none before); the enum stays a value set. File the class separately.
- **A drain signal at pod exit.** No adapter path sends the `CH-RUNTIMEOPS` `terminate` frame (D8), and a pod-exit drain belongs to remediation-plan steps R16 (eviction, the holder path) and R17 (enabling `CH-RUNTIMEOPS` at the deployment boundary) in `gateway-runtime-comms-remediation.md`.
- **Scrub steps 3 and 5, and the preConnect re-warm.** `scrubConfig` sets neither `ResetEnv` nor `TruncateLogs` (`pkg/adapter/podscrub.go:120-127`), so both steps record as skipped, and no component calls `PreConnect` after adapter startup. Both predate this proposal; file them separately.
- **SDK-warm mode on a sidecar runtime.** No sidecar transport implements `SDKWarmRuntime`, so a sidecar pool whose runtime declares `capabilities.preConnect: true` fails its sessions at `ConfigureWorkspace` or `DemoteSDK`, which return Unimplemented. The gap predates this proposal, which adds neither a §6.1 rule restricting `preConnect` to the embedded model nor a sidecar SDK-warm path; file it separately.
- **Embedded recycling wiring.** `cmd/runtimes/echo-embedded` and `cmd/runtimes/preconnect-echo` wire no `ScrubOps`, so an embedded recycling pod never reports its scrub and retires by timeout. File it separately.
- **Claim-path defects outside the pin check.** `Claimer.Claim` leaks a bound claim when `stampPodTenant` fails (`pkg/gateway/podlifecycle/podclaim/claimer.go:157-159`), and `CRDPodRegistry.ClaimPod` has no pin check and no production caller (`pkg/podregistry/crd.go:170-209`). File them separately.
- **Runtime-level and CRD-level enforcement of the process-reuse acknowledgment.** The runtime registration validator (`pkg/gateway/externalapi/admin/runtimes.go:407-439`) enforces no §5.2 acknowledgment, and the recycle disposition reads only the pool record (`pkg/gateway/session/recycle/scrubreporter_seams.go:681-686`). The SandboxTemplate CRD carries neither `recycle.enabled`, `maxSessionsPerPod`, nor `acknowledgeProcessLevelIsolation` (`pkg/apis/lenny/v1alpha1/sandboxtemplate_types.go:28-67`). The gateway pool store is therefore the single enforcement site, as it is for the concurrent-session acknowledgment.
- **A recovery claim after an update that brings a pool into pod reuse.** `sessionIsolationLevel` is computed at creation and persisted (`pkg/gateway/sessionserver/isolationlevel.go:156-184`), and a later claim for the same session (the resume-rebuild `Bind`, `pod_launch.go:208`; `resumeOnPod`, `resume_rebind.go:75-97`; the concurrent `bindSlotWithRetry`, `slot_bind.go:98`) re-resolves the pool. An update in between that brings the pool into pod reuse can place a session whose persisted level reports `podReuse: false` on a reused pod or a shared process. The case predates this proposal for pod reuse and concurrent slot sharing. The fix, which admits such a pod only to a session whose persisted `scrubPolicy` is non-empty on every claim path including `SlotClaimer.ClaimSlot`, is filed separately.
- **The occupancy-zero report on a pool that stopped recycling.** A session bound before an update to `recycle.enabled: false` still takes the recycle path, because `BindResult.Recycle` is resolved at bind time. Its `ReportPodScrub` is a no-op (`pkg/gateway/session/recycle/scrubreporter_seams.go:618-626`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:504-510`), and only the disposition driver cancels the missing-report timer (`scrubreporter_seams.go:945`), so the pod retires through that timer with `scrub_report_timeout` and the `failed` terminal (`pkg/gateway/session/recycle/recycleboundary.go:367-409`). The misleading reason predates this proposal; file it separately.
- **Pinned idle bound and idle timeout.** A per-pool bound on pinned idle pods beyond `maxWarm`, summed by the §17 namespace quota floor, and an idle TTL for pinned pods belong to a follow-up proposal (D14). This proposal adds neither and does not change how the pool store writes `maxWarm`.
- **The §6.2 `idle ──→ draining` trigger list.** The edge, and the `idle` → `draining` row of `docs/reference/state-machines.md`, name neither the scale-down excess drain nor the idle-pod certificate replacement today. SPEC-8(c)'s pinned-idle drain joins them. File it separately.

## 12. Adjudicated decisions

The human adjudicated decisions 1 to 5 and A to C on 2026-09-30, and decisions 6 to 11 and the step ordering in decision 3 on 2026-10-01. No decision is open. Each entry states the question, the choice, and what the choice fixes in the staging, and a later review does not reopen it.

1. **Pod-global surfaces after a kept process's first session.** Adjudicated at the default. D9 fails closed: once a kept runtime process has been given a second session, intra-pod MCP forwarding, the direct-mode token fold, and the control-event stamp name no session for any later session on the pod, in both deployment models, as they do on a concurrent pod. The shipped runtime-author guide already states the rule that covers this case (`docs/runtime-author-guide/platform-tools.md:23`, `docs/runtime-author-guide/integration-levels.md:99`). Per-session attribution of calls a runtime addresses by session is draft proposal 0084, whose addressed arm reads the slot registry rather than the generation. This proposal stages no attribution change (summary, Impacts on other proposals).

2. **Scope of the acknowledgment rule.** Adjudicated at Alternative B. Pool admission refuses a recycling pool that keeps its runtime process across sessions without the field, and no recycle-boundary retire re-checks the rule. D5 states the rule, and CODE-11 enforces it. Every pool definition and test literal the rule refuses gains the field (FIXTURE-1, TEST-19).

3. **The runtime contract for sequential sessions.** Adjudicated at the default. This proposal stages the specification sentences (SPEC-5(b)) and the runtime-author guide statement (DOC-1) that a runtime on an acknowledged recycling pool keys every frame by `sessionId`, and it edits no file under `sdks/` (decision 7). The Go, Python, and TypeScript runtime SDKs each invoke the create handler once per process with the first session's context (`sdks/runtime/go/runtime/runtime.go:76-84`, `sdks/runtime/python/lenny_runtime/runtime.py:217-227`, `sdks/runtime/typescript/src/runtime.ts:192-199`). Changing them to serve sequential sessions keyed by `sessionId`, with any end-of-session signal that change needs, is a separate follow-up proposal (§9.1). By the human decision of 2026-10-01, no checklist step waits for the follow-up, on the condition that it lands before any release. CODE-1 is the deliverable that keeps the runtime process across occupancy zero, and the checklist lands CODE-8 and CODE-11 before it, so that reuse is admitted only on a pool that carries `acknowledgeProcessLevelIsolation: true` and only for the pinned tenant. From the landing of CODE-1 until the follow-up lands, a kept process built on an unchanged SDK serves a later session under the first session's `CreateRequest`, including its credential bundle (`sdks/runtime/go/runtime/runtime.go:269`, `:452`, and `:530-541`), while its replies carry each frame's own `sessionId`. Before CODE-1 lands, the same pool fails that session's start. No test or fixture this proposal stages runs an SDK-built runtime through the adapter (`cmd/runtimes/*` imports no SDK, and `tests/tier3_contract/sdks/runtime_sdk_test.go` drives the SDKs through `cmd/lenny-compliance`), so the window turns no checklist step red, and the release condition is the only control on it. The summary's watch-out records the condition. The follow-up owns the test that pins the multi-session behaviour, at tier 3 in `tests/tier3_contract/sdks/`. The capability-declaration alternative is not staged.

4. **Relationship to proposal 0078.** Adjudicated at the default. Proposal 0078 lands first and unchanged. CODE-1 applies on top of 0078's CODE-1 through CODE-3, CODE-4 extends 0078's `CloseListener` and keeps its name and signature, and TEST-1 deletes 0078's TEST-1 and TEST-9 and amends its TEST-4.

5. **The terminate frame.** Adjudicated at the alternative: no adapter path sends the `CH-RUNTIMEOPS` `terminate` frame. D8 states the result, and CODE-2, SPEC-11(b), (c), and SPEC-18 carry it.

6. **An admitted pool edit that takes a pool outside the process-reuse rule.** Adjudicated on 2026-10-01: fail closed. When an admitted edit leaves a pool whose configuration no longer requires `acknowledgeProcessLevelIsolation` (for example `recycle.enabled: false`, `maxSessionsPerPod: 1`, or the acknowledgment removed together with such a change), every acquisition, including the reserved-hold rebind and the Postgres fallback claim, refuses a pod that has served a session (D16). SPEC-8(d) states the rule, D16 the design, CODE-8 the code, and TEST-21 the tests.

7. **Where the Go runtime SDK's doc-comment correction lands.** Adjudicated on 2026-10-01. The correction to the Go SDK's `Handler` doc comment (`sdks/runtime/go/runtime/runtime.go:80-84`) moves to the follow-up runtime-SDK proposal, which changes the behaviour the comment describes. CODE-9 names no file under `sdks/`, and this proposal edits none. No checklist step waits for the follow-up (decision 3). From the landing of CODE-1 until the follow-up lands, the `Handler` comment states that a recycling pool serves the next session in a fresh `OnCreate` invocation after the runtime exits, and the `CreateRequest` field comments (`sdks/runtime/go/runtime/types.go:208-213`) and the §15.7 snippet comments that §9.1 leaves to the follow-up state that the runtime instance is bound to one session and that `OnCreate` is invoked once. CODE-1 makes each statement false for a kept process, and the release condition in decision 3 bounds that window.

8. **Drains per acquisition.** Adjudicated on 2026-10-01: the drain of a refused pinned pod on a pool outside the process-reuse rule (decision 6, D16) is not capped.

9. **The Postgres fallback's pin read inside the row lock.** Adjudicated on 2026-10-01: the pin read stays inside `ClaimIdle`'s row-locked transaction, and nothing is written while the lock is held. Filtering on the mirror row's `tenant_id` in SQL is not staged, because the mirror writes `tenant_id` empty for idle rows (`pkg/controller/warmpool/controller.go:453-455`). Non-spec §4.4 states the design, CODE-8 the code, and TEST-13 the tests.

10. **A hold-timeout no-frame assertion.** Adjudicated on 2026-10-01: added. TEST-3 gains the tier-1 case `TestCoordinatorHoldTimeoutSendsNoTerminateFrame_spec_10_1_4`, which asserts that the coordinator hold timeout writes no `CH-RUNTIMEOPS` frame when CODE-4's `endRuntimeForHoldTimeout` runs the pod-scope teardown. It pins decision 5 and SPEC-18(a) on that path. The case needs no code change: `onHoldTimeout` and `terminateHeldSession` make no `RuntimeOps` call (`pkg/adapter/holdstate.go:177-322`), and after CODE-2 `RuntimeOps.Terminate` has no production caller.

11. **A refused pod pinned to another tenant.** Adjudicated on 2026-10-01: no acquisition drains a pod it refuses because the pod is pinned to another tenant, on any pool. `recycle.allowCrossTenantReuse` has no effect on which tenant a pod serves until draft proposal 0087, part 2, resets the tenant pin after a proven runtime restart. SPEC-8(c), (d), (f), and (j) state the rule, D6 the design, non-spec §4.4 and CODE-8 the code, and TEST-12, TEST-13, TEST-14, and TEST-21 the tests.

A. **How a `released` retire's reason is recorded.** Adjudicated at the default. CODE-7 logs every retire whose `failed` is false at `Info`, one record per retire, with the pod, the reason, the lifetime session count, and the uptime, and DOC-4's troubleshooting entry reads that record. A `vm-restart` pool logs one record per boundary.

B. **Whether SPEC-5(b) names "Runtime not live".** Adjudicated at the default. The **Recycling and integration levels** paragraph names the Pod retirement policy item "Runtime not live" by its label, as D15 permits for a site that lists per-pod retire causes.

C. **DOC-6 as its own deliverable.** Adjudicated at the default. DOC-6 stays a separate deliverable, and DOC-1, DOC-3, and DOC-5 keep their scope.
