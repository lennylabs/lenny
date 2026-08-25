# Proposal: Name who starts the next session's runtime on a recycled pod

- **Status:** Draft for review.
- **Date:** 2026-08-25
- **Scope:** Closes BUILD-GAPS F-5.2.33 part (b). The specification promises a fresh runtime process for every session on a recycling pod and names no component that creates it under the §4.7.10 sidecar deployment model, which is the default and the model §4.7.10 tells third-party authors to use. This proposal names the creator under each deployment model, records that the sidecar model has none, converts the resulting silent single-session failure into a specified pod retirement at each occupancy-zero boundary, states what the §5.2 whole-pod scrub reaches when the process namespace is not shared, reconciles §4.7.9 step 7 with §4.7.10, and makes the tier-10 and tier-5 cases able to fail. Part (a), the pod-scoped listener teardown, is proposal 0078 and lands first.

This document stages the proposed specification, code, test, and documentation changes. It does not modify any spec, code, or doc file. Apply the changes in the Proposed changes section after sign-off.

## Summary

**What changes.**

- `spec/04` §4.7.9 step 7 and §4.7.10 state which component creates the runtime process under each deployment model and how long that process lives. The sidecar model's runtime container is started once by the kubelet and no component re-creates its process inside the pod's lifetime.
- `spec/05` §5.2 states what each whole-pod scrub step reaches across the container boundary, narrows the recycling promise from an unconditional one to one conditioned on the deployment model, and adds the `no_successor_runtime` retirement trigger with its `mode_factor` consequence. `spec/06` §6.1 and §6.2 and `spec/15` §15.4 take the matching qualifier, and `spec/16` §16.1 records the reason.
- `pkg/controller/sandbox` stamps a `lenny.dev/runtime-recreatable` pod label at render, and `pkg/sandbox/podscrub.Decide` gains a retire branch keyed on it, so a pod whose runtime cannot be re-created drains at each occupancy-zero boundary instead of being held for a session it cannot serve.
- `pkg/adapter/scrub`, `pkg/adapter/socketruntime.go`, and the Go runtime SDK lose the doc comments that claim the scrub terminates the runtime's process and that a recycling pool re-invokes the runtime after it exits.
- The tier-10 recycle-scrub conformance case is driven against a real `SocketRuntimeProcess` so its pod-survival property can fail, and gains a property pinning the second `Start`'s failure. The tier-5 case is split into a sidecar retirement case and an embedded reuse case, and its skip-register row is deleted.

**Fixed decisions.**

- The runtime process exits at occupancy zero. Nothing here keeps a runtime alive across a recycle boundary, and no implementation step may reintroduce one.
- §5.2's "keeps the process alive and reuses it" is the adapter process. Proposals 0031 and 0034 authored that clause and state the reading.
- `releaseActiveLocked` and the occupancy-zero gate (`pkg/adapter/socketruntime.go:384`) are correct and are not touched.
- The listener-teardown fix in `SocketRuntimeProcess.Close` (`pkg/adapter/socketruntime.go:467`) is proposal 0078's and lands first. Nothing here restages it.
- A kubelet container restart and an adapter-side spawn are both rejected on verified grounds (§9). The successor-runtime capability on the sidecar transport is deferred to a named follow-up whose mechanism is outlined in §9.4.
- The gate is a pod label evaluated at the recycle boundary. It is not an admission-time refusal, because the gateway runtime registry does not carry `deploymentModel` and the admin registration payload does not model it (§2, D2).
- The retire reason is non-counting. §16.1 freezes the `lenny_gateway_pod_retirement_total{reason}` vocabulary to the three limit triggers, and `no_successor_runtime` is a per-boundary structural reprovision like `vm_restart_reprovision`.

**Watch out for.**

- **Ordering against proposal 0078.** Today a second `StartSession` on a sidecar recycling pod fails at once, because `Close` closed the listener (`pkg/adapter/socketruntime.go:467`) and `Accept` on a closed listener returns immediately. Once 0078 removes that close, the same call blocks for the full 30-second accept bound (`pkg/adapter/socketruntime.go:181`, default resolved at `:193`). Landing 0078 without this proposal makes the defect slower and worse.
- **`SpawnPath` has no production caller.** The finding's dossier attributes it to the `cmd/lenny-adapter --runtime-bin` developer loop, and the field's own doc comment says the same (`pkg/adapter/socketruntime.go:38`). That flag builds a `SubprocessExecutor` instead; the only assignments to `SpawnPath` are `pkg/adapter/socketruntime_e2e_test.go:56`, `:198`, `tests/tier4_integration/concurrent_workspace_test.go:123`, and `tests/tier4_integration/concurrent_delegation_proxy_test.go:165`. A design that generalises the developer loop is generalising a test-only field.
- **`deploymentModel` is deliberately absent from the gateway registry.** `pkg/controller/runtime/controller.go:237` states that it "is a CRD-only field with no registry counterpart and is intentionally not mirrored", and the admin runtime registration payload does not model it, so every admin-registered runtime resolves to the `sidecar` default. A gate placed in `validateRecyclePolicy` (`pkg/gateway/runtime/poolstore/poolstore.go:591`) would refuse recycling on every admin-registered runtime in the tree, fixtures included.
- **The `Decide` branch order carries a reason-stability argument.** The `VMRestart` branch at `pkg/sandbox/podscrub/podscrub.go:388` documents why it sits before the session-count branch: otherwise a `maxSessionsPerPod: 1` pool retires with a different reason than an otherwise-identical `maxSessionsPerPod: 2` pool. The new branch inherits that argument verbatim and sits in the same window.
- **Scrub reach does not follow from `shareProcessNamespace: false` alone.** `buildSidecar` mounts `workspace`, the credential volume, `tmp`, `sessions`, `artifacts`, `/dev/shm`, and `shared` into both containers (`pkg/controller/sandbox/podspec/podspec.go:536-557`), so the filesystem steps do cross the boundary. Only the process-scoped and adapter-local steps do not.
- **The tier-10 double cannot fail.** `scrubConformanceRuntime.Start` returns `nil` unconditionally (`tests/tier10_conformance/recycle_scrub_conformance_test.go:61`), so Property 2 at `:231` asserts a pod survival the double could not have refuted. Substituting a real transport changes what that case asserts; read §10 before editing it.
- **A prior attempt burned four hours and six reverted commits** (`8cdd5d6d` through `ccaeb30d`, reverted by `39e08bb4`) making the runtime survive the boundary. Any edit whose effect is that a runtime process outlives occupancy zero is that attempt returning.

## Implementation checklist

- [ ] **S1 · spec** — SPEC-1, SPEC-2. §4.7.9 step 7 split by deployment model; the §4.7.10 runtime-process-lifetime paragraph and trade-off row.
      Tiers 0, 11. Depends on: —
- [ ] **S2 · spec** — SPEC-3. The §5.2 scrub-reach paragraph and the step 1 qualifier.
      Tiers 0, 11. Depends on: S1
- [ ] **S3 · spec** — SPEC-4, SPEC-5. The §5.2 recycling narrowing, the `no_successor_runtime` retirement trigger, and the `mode_factor` consequence.
      Tiers 0, 11. Depends on: S2
- [ ] **S4 · spec** — SPEC-6, SPEC-7, SPEC-8. The §6.1 recycle row and §6.2 recycle edges; the §15.4 sentence; the §16.1 retirement-counter row.
      Tiers 0, 11. Depends on: S3
- [ ] **S5 · code** — CODE-1. The `lenny.dev/runtime-recreatable` label constant and the Sandbox reconciler stamp.
      Tiers 0, 1, 2. Depends on: S4
- [ ] **S6 · code** — CODE-2. `podscrub.Inputs.RuntimeRecreatable`, `ReasonNoSuccessorRuntime`, and the `Decide` branch.
      Tiers 0, 1. Depends on: S4
- [ ] **S7 · code** — CODE-3. The recycle policy resolver reads the label and carries it into `Decide`.
      Tiers 0, 1, 2, 4. Depends on: S5, S6
- [ ] **S8 · code** — CODE-4. The scrub, socket-runtime, and runtime-SDK doc-comment corrections.
      Tiers 0, 1. Depends on: S4
- [ ] **S9 · test** — TEST-1, TEST-2, TEST-3. The tier-1 `Decide` table, the tier-1 label parse, and the tier-2 reconciler cases.
      Tiers 0, 1, 2. Depends on: S5, S6, S7
- [ ] **S10 · test** — TEST-4. The tier-10 conformance case on a real `SocketRuntimeProcess`, with the successor-`Start` property.
      Tiers 0, 10. Depends on: S8
- [ ] **S11 · test** — FIXTURE-1, TEST-5, TEST-6, TEST-7. The embedded recycling pool fixture, the tier-5 split, and the skip-register deletion.
      Tiers 0, 5. Depends on: S7
- [ ] **S12 · test** — TEST-8. The tier-7a concurrent occupancy-zero retire-once case.
      Tiers 0, 7a. Depends on: S7
- [ ] **S13 · docs** — DOC-1, DOC-2, DOC-3, DOC-4. The runtime-author guide, the execution-modes reference, the glossary and level pages, and the operator narratives.
      Tiers 0, 11. Depends on: S4

## 1. Problem

### 1.1 What the specification promises

§15.4 states, outside the integration-level matrix, that pod recycling "is not a level-sensitive capability and does not appear in the matrix: the platform scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime cooperation and is available at every integration level" (§15.4). §5.2 repeats the claim under **Recycling and integration levels** (§5.2). `recycle.maxSessionsPerPod` is required with no default, and §5.2 explains that the requirement exists to force an explicit reuse-limit choice (§5.2). §6.1's recycle row states that at the occupancy-zero boundary "the whole-pod scrub terminates the SDK process along with all other session processes", after which the adapter "re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge" (§6.1).

Every one of those statements describes a pod that serves session N+1.

### 1.2 What the sidecar transport delivers

Under §4.7.10 the default deployment model puts the runtime in its own pod container, started by the kubelet, communicating with the adapter over an abstract Unix socket. No component creates that container's process a second time.

- The kubelet does not. `basePod` renders every agent pod with `RestartPolicy: Never` (`pkg/controller/sandbox/podspec/podspec.go:980`), and `buildSidecar` renders the runtime as an ordinary container (`pkg/controller/sandbox/podspec/podspec.go:609`), so a container that exits stays exited.
- The adapter does not. `SocketRuntimeProcess`'s own doc comment records the contract: "The adapter never spawns the runtime in this model — the kubelet starts the runtime container" (`pkg/adapter/socketruntime.go:33`). The `SpawnPath` field that would exec one has no production caller; the four assignments in the tree are all in tests.
- The scrub cannot reach it. §13.1 forbids `shareProcessNamespace` on every pod template Lenny generates (§13.1), so `DefaultOps.KillUserProcesses` (`pkg/adapter/scrub/defaultops.go:28`) signals only the adapter container's own process tree.

The runtime's exit is correct and specified. The adapter closes the shared connection at occupancy zero (`pkg/adapter/socketruntime.go:450`), the runtime observes the clean-exit EOF and returns, and the runtime-author guide tells authors "On a recycling pod the runtime exits at each session end" (`docs/runtime-author-guide/lifecycle.md:330`). Nothing in this proposal disputes that. The defect is that after the runtime exits, no component creates its successor, and the specification never says which one should.

### 1.3 How the failure presents

The gateway patches the claim to `recycling`, the adapter runs the scrub against a runtime container whose process is already gone, the scrub reports success, and the disposition decider reuses the pod (`pkg/sandbox/podscrub/podscrub.go:328`). The claim reaches `reserved`, the pod is held for its pinned tenant, and the tenant's next session is dispatched onto it with no acquisition round trip. `StartSession` reaches `Runtime.Start` (`pkg/adapter/session.go:156`), `SocketRuntimeProcess.Start` finds no live connection and waits out the accept bound, and the session fails. The gateway categorises the failure as transient and retries onto another pod, so the client usually sees a slower session rather than an error and the operator sees nothing: the scrub succeeded, the pod was reused, the retry worked.

The consequence for capacity is concrete. §5.2's `mode_factor` converges toward `recycle.maxSessionsPerPod` on a `standard` or `in-place` recycling pool (§5.2), so a pool configured for twenty sessions per pod is provisioned for a fraction of the pods it actually needs.

### 1.4 The two subsidiary false statements

**The scrub does not terminate the runtime's process.** `scrub.Ops` states of step 1 that "It terminates the runtime's SDK process along with every other task process" (`pkg/adapter/scrub/scrub.go:74-77`). §6.1's recycle row makes the same claim at specification level. Both are false on the sidecar model.

The correct statement is narrower than a flat denial and is not derivable from `shareProcessNamespace: false` alone. `buildSidecar` mounts `workspace`, the credential volume, `tmp`, `sessions`, `artifacts`, `/dev/shm`, and `shared` into both containers (`pkg/controller/sandbox/podspec/podspec.go:536-557`), so steps 0, 2, 4, and 6 do cross the container boundary. Step 1b's `ipcrm --all=shm` reaches the pod's shared IPC namespace, because §13.1 forbids only `hostIPC`. What does not cross is step 1's process kill, step 3's environment restoration, and step 5's log-buffer truncation, each of which is scoped to the adapter container.

**§4.7.9 step 7 names the wrong actor.** The startup sequence reads "Adapter spawns runtime binary" (§4.7). That holds in the embedded model and in the developer loop, and it is false in the sidecar model that the following section makes the default. Nothing reconciles the two sections.

### 1.5 The two tests that should have caught it

`tests/tier10_conformance/recycle_scrub_conformance_test.go` Property 2 asserts that "the pod process stays alive across the recycle boundary — a replacement session binds, which is impossible if `Shutdown` terminated the pod" (`:231`). It is asserted against `scrubConformanceRuntime`, whose `Start` returns `nil` unconditionally (`:61`). No transport behaviour can make the property fail.

`tests/tier5_e2e_kind/execution_modes_test.go` `TestTaskModeRecycleScrubsWorkspaceBetweenSessions` would catch it against a real pod. It is skipped (`:124`) with the reason recorded at `tests/registers/skip-reasons.yaml:1120-1124`, and its pool `task-mode-echo-pool` is backed by `echo-runtime-task-mode`, a `deploymentModel: sidecar` runtime (`tests/testinfra/kind/agent-workload.yaml:209`).

### 1.6 Finding

BUILD-GAPS F-5.2.33 part (b). Part (a), the pod-scoped listener destroyed by a per-session `Close`, is proposal 0078 and is a precondition of the conformance work in S10.

## 2. Decisions

**D1. The specification states who creates the runtime process under each deployment model, and records that the sidecar model has no such component.** The embedded model's adapter is the runtime and creates one per session in its own process. The sidecar model's runtime container is started once by the kubelet and its process is not re-created inside the pod. That is the answer to the question the finding asks, and §9 states why the two mechanisms that would change it are rejected and §9.4 outlines the one that could.

**D2. The gate is evaluated at the recycle boundary rather than at pool admission.** The gateway's disposition decider already fetches the agent Pod and reads its labels at exactly this point, and already takes a non-counting retire branch on one of them. The alternative, a refusal in `validateRecyclePolicy`, needs `deploymentModel` in the gateway runtime registry, which `pkg/controller/runtime/controller.go:237` records as an explicit decision not to mirror, and which the admin runtime registration payload does not model, so every admin-registered runtime would resolve to `sidecar` and be refused. §9.3 records the refusal design as considered and §12 puts the trade to the reviewer.

**D3. The label carries the derived predicate rather than the deployment model.** The label is `lenny.dev/runtime-recreatable`, following `lenny.dev/host-schedulable` (`pkg/controller/warmpool/pod_reconciler.go:39`), which stamps the predicate the gateway consumes rather than the underlying state. A future deployment model that can create a successor process sets the label true without a second gate, and the gateway never learns what a deployment model is.

**D4. The label parse fails safe to false.** An absent or unrecognised value reads as not recreatable, matching `hostSchedulable`'s exact-`"true"` comparison (`pkg/gateway/session/recycle/scrubreporter_seams.go:725`). A pod rendered before the label existed degrades to one session per pod, which is the pre-recycle default and is never wrong. Failing open re-enters the silent failure this proposal removes.

**D5. The retire reason does not count.** §16.1 freezes `lenny_gateway_pod_retirement_total{reason}` to the three limit triggers (§16.1). `no_successor_runtime` is a per-boundary structural reprovision, exactly like `ReasonVMRestartReprovision` (`pkg/sandbox/podscrub/podscrub.go:226`), so it is recorded in the audit trail and on neither retirement counter.

**D6. No new metric and no new alert.** The degradation is visible on `lenny_pod_session_reuse_count`, whose p50 falls to 1 on an affected pool, and §5.2 already treats a p50 of 1 as the signal for a sequential `vm-restart` pool. A new counter would carry a §16 inventory row, a metrics-reference row, and an alert-and-runbook decision for a signal the existing histogram gives. This is a cost argument rather than a correctness one and §12 puts it to the reviewer.

**D7. `sessionIsolationLevel.podReuse` stays `true` on an affected pool.** The field is computed from the pool's `recycle.enabled` at session creation (`pkg/gateway/sessionserver/sessionserver.go:2365`), where the gateway has not yet bound a pod. Reporting `podReuse: true` and `residualStateWarning: true` for a pool that in practice serves one session per pod over-warns rather than under-warns, which is the safe direction for a client isolation disclosure. §8.1 stages the sentence that documents it.

## 3. Design overview

At pod render the Sandbox reconciler already resolves the runtime's `deploymentModel` (`pkg/controller/sandbox/controller.go:398`) and already stamps pod labels immediately after building the spec (`pkg/controller/sandbox/controller.go:464-479`). It gains one more label:

```
lenny.dev/runtime-recreatable: "true" | "false"
```

The value is `"true"` for `deploymentModel: embedded` and `"false"` for `sidecar` and for an empty value, which §4.7.10 defaults to sidecar.

At the occupancy-zero boundary the recycle policy resolver already fetches the agent Pod and reads `lenny.dev/host-schedulable` from it (`pkg/gateway/session/recycle/scrubreporter_seams.go:649`). It reads one more key from the same object with the same fail-safe parse and carries it on `leasecontrol.PodRecyclePolicy` into `podscrub.Decide` (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:511`). No new API call, no new RBAC verb, and no new watch.

`Decide` gains a branch immediately after the `VMRestart` branch and before the session-count branch:

```go
if !in.RuntimeRecreatable {
    return Disposition{
        Ready: true, NextPhase: state.Draining,
        ScrubWarning: warned, Retire: true,
        Reason: ReasonNoSuccessorRuntime,
    }
}
```

The pod drains, the warm pool provisions a replacement, and the tenant's next session lands on a fresh pod with a live runtime. A recycling pool on a sidecar runtime behaves as a one-session-per-pod pool with a whole-pod scrub, a named retirement reason in the audit trail, and a `lenny_pod_session_reuse_count` p50 of 1.

Pod reuse across a recycle boundary therefore remains available on the embedded deployment model and is unavailable on the sidecar model until the follow-up in §9.4 lands. The specification says so where a deployer, an operator, and a runtime author each meet it.

## 4. Detailed design

### 4.1 The label and its writer

`pkg/controller/sandbox/podspec` already exports the `DeploymentModel` type and its constants, and the reconciler already branches on `podspec.DeploymentEmbedded` (`pkg/controller/sandbox/controller.go:558`), so the predicate is resolved with no new vocabulary. The label constant lives beside `LabelHostSchedulable` in `pkg/controller/warmpool`, because the gateway-side reader already imports that package for the sibling label.

The label is stamped at pod build, in the same block as `state.LabelRuntime`, before `SetControllerReference`. It is immutable for the pod's lifetime: the deployment model is a property of the runtime image the pod was rendered from, and a Runtime edit that changed it is rendered into new pods rather than mutated onto running ones. The reconciler therefore stamps it on create and does not reconcile it on update, unlike `lenny.dev/host-schedulable`, which tracks Node state that changes underneath a running pod.

**IMPLEMENTOR'S CHOICE:** whether `podspec.Render` stamps the label from `Inputs.DeploymentModel` or the reconciler stamps it after `Render` returns. Any answer must put the label on the Pod object before `Create`, so no pod ever exists without it, and must derive the value from the same `Inputs.DeploymentModel` the container layout is derived from, so the label cannot disagree with the pod it describes.

### 4.2 The `Decide` branch and its position

The branch sits after the `VMRestart` branch and before the session-count branch. Both halves of that placement carry an argument.

After `VMRestart`: a `vm-restart` sidecar pool would retire for both reasons, and `vm_restart_reprovision` is the more specific one. It names an isolation requirement the platform is meeting, where `no_successor_runtime` names a transport limitation. An operator reading the audit trail of a microvm cross-tenant pool should see the reprovision.

Before the session-count branch: this is the argument `pkg/sandbox/podscrub/podscrub.go:379-388` already makes for `VMRestart`. A `maxSessionsPerPod: 1` pool reaches the recycle boundary with the served-session count equal to the limit, so a branch placed after the session-count branch would retire it with the counting `session_count_limit` while an identical `maxSessionsPerPod: 20` pool retired with `no_successor_runtime`, and `lenny_gateway_pod_retirement_total` would diverge between two pools whose observable behaviour is the same.

The branch is also after the `onScrubFailure: fail` and scrub-failures-exhausted branches, so a genuine scrub failure keeps its fail-closed `Failed` terminal and its counting reason. `ScrubWarning: warned` carries the `scrub_warning` annotation onto the drain, matching every other retire branch.

### 4.3 What the gateway does not do

The gateway does not refuse the pool, does not warn at session creation, and does not alter `sessionIsolationLevel`. The first two need the registry mirror D2 declines and §12 escalates; the third is D7. A deployer configuring `recycle.maxSessionsPerPod: 20` on a sidecar pool is therefore accepted and receives one session per pod, and learns it from the pool's reuse histogram, the retirement audit trail, and the documentation this proposal stages. §9.3 states plainly why that is unsatisfying.

### 4.4 Scrub reach

Nothing about the scrub's execution changes. The steps are unchanged, the ordering is unchanged, and the reported outcome is unchanged. What changes is that a reader can no longer conclude from §6.1's recycle row or from the `Ops` contract that step 1 terminated the runtime's process. §5.2 gains a paragraph stating which steps cross the container boundary in the sidecar model, and the doc comments on `scrub.Ops.KillUserProcesses` and `DefaultOps.KillUserProcesses` are corrected to state that the kill reaches the adapter container's PID namespace and that the runtime container's process is gone by that point for a different reason: the adapter closed its connection at occupancy zero and the runtime exited on the clean-exit EOF.

## 5. Edge cases and accepted failure modes

| Case | Observable outcome | Where it lands |
|:--|:--|:--|
| Sidecar recycling pool at occupancy zero | The pod drains and is replaced; the tenant's next session lands on a fresh pod. The reserved hold never opens. | SPEC-4 (§5.2 retirement trigger), SPEC-6 (§6.2 edge), DOC-2 (`docs/reference/execution-modes.md` retirement list) |
| Sidecar recycling pool sizing | `mode_factor` is `1.0` rather than converging toward `recycle.maxSessionsPerPod`, and the observed `lenny_pod_session_reuse_count` p50 is 1. | SPEC-5 (§5.2 `mode_factor`), DOC-4 (`docs/operator-guide/scaling.md`) |
| Sidecar preConnect recycling pool | The pod never traverses the `claimed → sdk_connecting` re-warm edge, because the disposition retires before the re-warm leg. The §6.1 invariant that every pod reaching `reserved` or `idle` is SDK-warm holds trivially, since no such pod reaches either. | SPEC-6 (§6.1 recycle row, §6.2 edges) |
| `sessionIsolationLevel` on an affected pool | `podReuse: true` and `residualStateWarning: true` are still reported, over-warning relative to the one-session-per-pod behaviour the pool has. | SPEC-4 (§5.2 client-visibility paragraph), DOC-2 |
| `recycle.maxSessionsPerPod` on an affected pool | Required, validated, and inert. This mirrors the required-but-inert state §5.2 already documents for a `vm-restart` pool, so pool validation keeps one rule for every recycling pool. | SPEC-4 |
| `acknowledgeBestEffortScrub` on an affected pool | Still required. The deployer acknowledges a residual-state risk the pod never incurs, because it serves one session. Accepted rather than carved out, for the reason in the row above. | SPEC-4 |
| Pod rendered before the label existed | Reads as not recreatable and retires at each boundary. An embedded recycling pool loses reuse until its pods are replaced by the rollout. | SPEC-4 (fail-safe sentence), DOC-4 (`docs/operator-guide/upgrades.md`) |
| Sidecar recycling pod whose scrub fails under `warn` | Retires with `no_successor_runtime` and carries the `scrub_warning` annotation onto the drain rather than re-entering the pool. The cumulative failure count is still recorded. | SPEC-4 |
| Sidecar recycling pod whose scrub fails under `fail` | Unchanged: the fail-policy branch runs first and keeps its `cleanup_fail_policy` reason and its `Failed` terminal. | SPEC-4 |
| Concurrent sidecar pool (`maxConcurrentSessions > 1`) | Slots multiplex over the one runtime connection while occupancy is nonzero, which works. The pod retires when occupancy reaches zero. Concurrency within one occupancy cycle is unaffected. | SPEC-4 |
| The whole-pod scrub cannot reach a sidecar runtime container's processes | Accepted and stated. The runtime's own process has already exited on the clean-exit EOF, and the pod is retired at the boundary, so nothing from the previous session is handed to a later one. | SPEC-3 (§5.2 scrub reach) |
| Deferred: a successor runtime process on the sidecar transport | Not delivered. A deployer who needs pod reuse with a third-party runtime uses one pod per session with a warm pool sized for the arrival rate until the follow-up lands. | SPEC-4 (the narrowing paragraph states the limitation and names the follow-up), DOC-1, DOC-2 |

## 6. Observability surface

No new metric and no new alert (D6). The change is observable through surfaces that already exist:

- `lenny_pod_session_reuse_count` p50 falls to 1 on an affected pool, which is the signal §5.2 already names for a sequential `vm-restart` pool.
- The pod's retirement audit record carries `reason: no_successor_runtime`. The §16.1 retirement-counter row names it beside `host_unschedulable`, `scrub_report_timeout`, and `vm_restart_reprovision`, none of which the row records today.
- `lenny_gateway_pod_retirement_total` is unchanged, deliberately, because the frozen vocabulary is the three limit triggers.
- `docs/operator-guide/troubleshooting.md` gains the narrative cause, so an operator who sees a recycling pool provisioning one pod per session finds the reason rather than filing it as a scaling defect.

## 7. CRD and RBAC changes

None. The label is written by the Sandbox reconciler, which already owns and patches the agent Pod, and is read by the gateway through the `get` verb on Pods in agent namespaces that the §6.2 host-schedulability read already requires. No CRD field is added; `Runtime.spec.deploymentModel` already exists and is unchanged.

## 8. Proposed changes

### 8.1 Proposed spec changes

**SPEC-1 — `spec/04_system-components.md` §4.7.9.**

Replace step 7 of the startup sequence:

```
7. The runtime process becomes live. In the embedded deployment model
   ([§4.7.10](#4710-deployment-model)) the adapter runs the runtime loop in
   its own process, once per session. In the sidecar deployment model the
   kubelet has already started the runtime container, and the runtime dials
   the adapter's abstract Unix socket, whose name the adapter supplies in the
   `LENNY_ADAPTER_SOCKET` environment variable; the adapter accepts the
   connection at this step. The adapter does not spawn a process in the
   runtime container under any circumstances: the two containers share no
   process namespace ([§13.1](13_security-model.md#131-pod-security)) and the
   runtime binary exists only in the runtime container's image.
```

**SPEC-2 — `spec/04_system-components.md` §4.7.10.**

Append after the sidecar-versus-embedded trade-off table and its note, before the health-check paragraph:

```
**Runtime process lifetime.** The two deployment models differ in which
component can create a runtime process and how often. In the embedded model
the adapter creates one per session, in its own process, for as many sessions
as the pod serves. In the sidecar model the kubelet starts the runtime
container once, under `RestartPolicy: Never`, and the runtime process it
contains serves one session: it exits at the clean-exit EOF the adapter sends
when occupancy reaches zero, and no component creates a successor inside the
pod. The kubelet does not re-run a container of a pod whose restart policy is
`Never`, and the adapter cannot spawn one, because the runtime binary exists
only in the runtime container's image and the two containers share no process
namespace ([§13.1](13_security-model.md#131-pod-security)). A sidecar pod
therefore serves exactly one session, and a recycling pool built on a sidecar
runtime retires each pod at its occupancy-zero boundary
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

Add a row to the trade-off table:

```
| Pod reuse across a recycle boundary | Unavailable — no component creates the next session's runtime process, so the pod retires at each occupancy-zero boundary ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) | Available — the adapter process is the runtime and creates one per session |
```

**SPEC-3 — `spec/05_runtime-registry-and-pool-model.md` §5.2, scrub procedure.**

Append after scrub step 6 and before the best-effort paragraph:

```
**What the scrub reaches across the container boundary.** In the
[§4.7.10](04_system-components.md#4710-deployment-model) sidecar deployment
model the scrub executes in the adapter container. Steps 0, 2, 4, and 6
operate on volumes mounted into both containers, so they clear the runtime
container's view of those paths as well. Step 1b reaches the runtime
container's shared-memory segments, because pod containers share an IPC
namespace and only `hostIPC` is forbidden. Step 1's process kill, step 3's
environment restoration, and step 5's log-buffer truncation are scoped to the
adapter container and do not reach the runtime container, because
`shareProcessNamespace` is forbidden
([§13.1](13_security-model.md#131-pod-security)). The runtime container's
process is nonetheless gone by the time the scrub runs: the adapter closed the
runtime's connection at occupancy zero and the runtime exited on the clean-exit
EOF. In the embedded model the runtime shares the adapter's process, so every
step reaches it.
```

Append to step 1 the sentence `The kill runs in the container that executes the scrub; see "What the scrub reaches across the container boundary" below.`

**SPEC-4 — `spec/05_runtime-registry-and-pool-model.md` §5.2.**

Replace the **Recycling and integration levels** paragraph (§5.2) with:

```
**Recycling, integration levels, and deployment models.** Pod recycling
requires no runtime cooperation: the per-slot cleanup and the whole-pod scrub
are adapter-executed and gateway-coordinated, with no CH-RUNTIMEOPS exchange
between sessions. Recycling is admitted at every integration level (Basic,
Standard, and Full), and `recycle.maxSessionsPerPod` is required and validated
at every level.

Whether a recycled pod is reused depends on the runtime's deployment model
rather than on its integration level, because reuse requires the pod to create
a runtime process for the next session. An embedded runtime
([§4.7.10](04_system-components.md#4710-deployment-model)) creates one per
session, and its pods are reused up to `recycle.maxSessionsPerPod`. A sidecar
runtime's process cannot be re-created inside the pod, so a sidecar pod is
retired at each occupancy-zero recycle boundary with the non-counting reason
`no_successor_runtime` and the gateway provisions a fresh replacement, as a
`vm-restart` pool is retired at its boundary. The whole-pod scrub still runs
and still reports before the retire, so the pod's disposition is decided on a
reported outcome.

The gateway evaluates this at the recycle boundary from the pod label
`lenny.dev/runtime-recreatable`, which the SandboxReconciler stamps at pod
render from the runtime's `deploymentModel`. An absent or unrecognised value is
treated as not recreatable, so a pod whose label is missing retires rather than
being held for a session it cannot serve.

`recycle.maxSessionsPerPod` and `acknowledgeBestEffortScrub` remain required
and validated on a sidecar recycling pool. Both are inert there: the pod serves
one session, so the reuse limit is never reached and the residual-state risk
the acknowledgment covers is never incurred. This required-but-inert state is
deliberate and mirrors the `vm-restart` case, so pool and admission validation
keep one rule for every recycling pool.

The session creation response reports `podReuse: true` and
`residualStateWarning: true` for any pool with `recycle.enabled: true`,
including a sidecar pool that in practice serves one session per pod. The
gateway computes those fields before it binds a pod, so it reports the pool's
configuration rather than the pod's disposition, and the reported value
over-warns rather than under-warns.

Creating a successor runtime process inside a sidecar pod is not specified.
Until it is, pod reuse across a recycle boundary is an embedded-deployment-model
capability, and a deployer running a sidecar runtime who wants to avoid pod cold
start uses the default one-session-per-pod configuration with a warm pool sized
for the session arrival rate.
```

**SPEC-5 — `spec/05_runtime-registry-and-pool-model.md` §5.2, sizing text.**

Append to the **Pod retirement policy (recycling pools)** list, after the scrub-failure-limit item:

```
- **No successor runtime process:** The pod's runtime cannot be re-created
  inside the pod (`lenny.dev/runtime-recreatable` is not `"true"`, which holds
  for every `deploymentModel: sidecar` runtime). The recycle disposition
  retires the pod at each occupancy-zero boundary regardless of
  `maxSessionsPerPod`, `maxPodUptimeSeconds`, and `maxScrubFailures`, with the
  non-counting reason `no_successor_runtime`, and the gateway provisions a
  fresh replacement. The `vm-restart` reprovision takes precedence when both
  apply.
```

Append to the `mode_factor` paragraph (§5.2), after the `vm-restart` sentences:

```
A recycling pool whose runtime uses the sidecar deployment model retires each
pod at its occupancy-zero boundary, so its steady-state `mode_factor` is `1.0`
and its observed `lenny_pod_session_reuse_count` p50 is 1 rather than
converging toward `recycle.maxSessionsPerPod`. The PoolScalingController's
cold-start fallback of `1.0` is therefore also its converged value on such a
pool.
```

**SPEC-6 — `spec/06_warm-pod-model.md` §6.1 and §6.2.**

Append to the `recycle.enabled: true` preConnect row (§6.1), and replace that row's claim that the whole-pod scrub terminates the SDK process with the reach-accurate statement:

```
The ending session's runtime process exits when the adapter closes its
connection at occupancy zero, and the whole-pod scrub clears the remaining
session processes in the adapter's own container and the shared writable paths
([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
On a sidecar-deployment-model runtime the pod is retired at the occupancy-zero
boundary rather than re-warmed, so it never traverses the
`claimed → sdk_connecting` re-warm edge and never reaches `reserved`.
```

In the §6.2 recycle-edge block, add `and the pod's runtime is recreatable` to the preconditions of the `claimed → sdk_connecting` and `claimed → reserved` edges, and add after the `vm-restart` drain edge:

```
  claimed ──→ draining              (the pod's runtime cannot be re-created in the pod —
                                     lenny.dev/runtime-recreatable is not "true", which holds
                                     for every deploymentModel: sidecar runtime; the scrub
                                     reports, then the pod retires and a fresh replacement is
                                     provisioned)
```

**SPEC-7 — `spec/15_external-api-surface.md` §15.4.**

Replace the paragraph at §15.4:

```
Pod recycling under `sessionPolicy.recycle`
([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes))
is not a level-sensitive capability and does not appear in the matrix: the
scrub is adapter-executed and gateway-coordinated, so recycling requires no
runtime cooperation and is admitted at every integration level. Whether a
recycled pod is reused depends on the runtime's deployment model rather than on
its integration level. A pod whose runtime runs in its own container is retired
at each occupancy-zero boundary, because no component inside the pod creates the
next session's runtime process; see
[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).
```

**SPEC-8 — `spec/16_observability.md` §16.1.**

Extend the `lenny_gateway_pod_retirement_total` inventory row (§16.1), which is the text that freezes the `reason` label. Append to the row's parenthetical:

```
The recycle disposition also retires a pod for reasons outside this label set,
which are recorded in the pod's audit trail and on neither retirement counter:
`host_unschedulable`, `scrub_report_timeout`, `vm_restart_reprovision`, and
`no_successor_runtime` (the pod's runtime cannot be re-created inside the pod,
so the pod retires at each occupancy-zero boundary — see
[Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

The first three already behave this way in code (`pkg/sandbox/podscrub/podscrub.go:186-226`) and are absent from the specification's row, so this edit records an existing gap alongside the new reason.

### 8.2 Proposed code changes

**CODE-1 — `pkg/controller/warmpool/pod_reconciler.go` and `pkg/controller/sandbox/controller.go`.**

Add `LabelRuntimeRecreatable = "lenny.dev/runtime-recreatable"` beside `LabelHostSchedulable` (`pkg/controller/warmpool/pod_reconciler.go:39`), with a doc comment citing §5.2 and stating the fail-safe reading.

In the label block at `pkg/controller/sandbox/controller.go:464-479`, stamp the label from the resolved `rt.Spec.DeploymentModel`: `"true"` for `podspec.DeploymentEmbedded` and `"false"` otherwise, including the empty value. Cite `// spec: §5.2 (no-successor-runtime retire trigger), §4.7.10 (runtime process lifetime)`.

**CODE-2 — `pkg/sandbox/podscrub/podscrub.go`.**

Add `RuntimeRecreatable bool` to `Inputs`, documenting that it reflects the label and that any value other than `"true"` is fail-safe-false. Add `ReasonNoSuccessorRuntime RetireReason = "no_successor_runtime"` with a doc comment following the `ReasonVMRestartReprovision` pattern (`:212-226`): a per-boundary structural reprovision rather than a limit trigger, so `CountsOnRetirementTotal` reports false. Add the `Decide` branch immediately after the `VMRestart` branch (`:388-396`) and before the session-count branch, returning `Draining`, `Retire: true`, `ScrubWarning: warned`, and the new reason, with the placement argument from §4.2 in its comment.

**CODE-3 — `pkg/gateway/session/recycle/scrubreporter_seams.go` and `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`.**

Add a `runtimeRecreatable(labels map[string]string) bool` helper beside `hostSchedulable` (`pkg/gateway/session/recycle/scrubreporter_seams.go:724`), reading `warmpool.LabelRuntimeRecreatable` with the same exact-`"true"` comparison. Populate a new `PodRecyclePolicy.RuntimeRecreatable` from it at the site that already populates `HostSchedulable` from the same fetched Pod (`:649`), and pass it into `podscrub.Decide` beside `HostSchedulable` (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:522`).

**CODE-4 — doc-comment corrections.**

- `pkg/adapter/scrub/scrub.go:74-77`: replace "It terminates the runtime's SDK process along with every other task process" with a statement that the kill is scoped to the container that executes the scrub, that `shareProcessNamespace` is forbidden so it does not reach the runtime container in the sidecar model, and that the runtime's process is already gone there because it exited on the clean-exit EOF before the scrub began.
- `pkg/adapter/scrub/defaultops.go:26-27`: the same qualifier on the concrete implementation.
- `pkg/adapter/socketruntime.go:22-58`: state that the runtime process is not re-created after it exits, so a `SocketRuntimeProcess` serves exactly one occupancy episode and the recycle disposition retires the pod for that reason. Correct the `SpawnPath` comment at `:37-40`, which attributes the field to `cmd/lenny-adapter --runtime-bin`; that flag builds a `SubprocessExecutor` and the field has no production caller.
- `sdks/runtime/go/runtime/runtime.go:82-83`: replace "A recycling pool serves the next session in a fresh `OnCreate` invocation after the runtime exits" with a statement that the process exits at session end and the next session runs in a new process, on a new pod when the runtime runs in its own container.

### 8.3 Proposed test and fixture changes

**FIXTURE-1 — `tests/testinfra/kind`.** Add an embedded recycling pool and its Runtime, so TEST-6 exercises reuse against a live pod. The tree already carries an embedded echo image (`echo-runtime-embedded`, `tests/testinfra/kind/agent-workload.yaml:73-80`), so the fixture is a new Runtime name over that image with `deploymentModel: embedded` and a pool with `recycle.enabled: true`.

**IMPLEMENTOR'S CHOICE:** whether the new pool reuses the embedded echo image under a new Runtime name or takes a dedicated image. Any answer must give the pool a `runtimeRef` no other pool on the cluster shares, must land in `tests/testinfra/kind/agent-workload.yaml`, the overlay `tests/testinfra/kind/install.sh` generates, and `tests/testinfra/kind/install.go`'s deployment-model map together, and must leave `task-mode-echo-pool` on its existing sidecar runtime, which TEST-5 asserts against.

The remaining test changes are specified in §10.

### 8.4 Proposed documentation changes

- **DOC-1 — `docs/runtime-author-guide/lifecycle.md` and `docs/runtime-author-guide/index.md`.** Qualify the unconditional statements at `lifecycle.md:69`, `:371`, and `:408` and at `index.md:186` with the deployment-model dependency, and extend the recycle walkthrough with the disposition a sidecar pod takes. The sentence at `lifecycle.md:330` is correct and stays.
- **DOC-2 — `docs/reference/execution-modes.md`.** Qualify the `Pod reuse` preset row (`:49`), add the retirement trigger to the retirement list (`:92`), and state the deployment-model precondition in the decision guide (`:99`) so a reader learns it before configuring the preset.
- **DOC-3 — `docs/reference/glossary.md:302`, `docs/runtime-author-guide/integration-levels.md:32`, `docs/about/why-lenny.md:104`, and `docs/about/contributing.md:194`.** Each carries the "requires no runtime cooperation and works at every integration level" claim. Each keeps the runtime-cooperation half, which is true, and qualifies the availability half.
- **DOC-4 — operator narratives.** `docs/operator-guide/scaling.md` states that a sidecar recycling pool sizes at `mode_factor = 1.0`. `docs/operator-guide/troubleshooting.md` gains the cause under the pool-capacity narrative, naming the observable signature (a `lenny_pod_session_reuse_count` p50 of 1 on a recycling pool, retirements recorded with `no_successor_runtime`) and the resolution (size for one session per pod, or move the runtime to the embedded model). `docs/operator-guide/upgrades.md` notes that pods rendered before this change carry no `lenny.dev/runtime-recreatable` label and retire at each boundary until they are replaced, so an embedded recycling pool loses reuse for the duration of the rollout.

## 9. Non-goals

### 9.1 A restartable runtime container

Rejected. A pod-level `RestartPolicy: OnFailure` does not restart a container that exits zero, which is exactly the clean-exit path §15.4 specifies. `RestartPolicy: Always` also restarts the adapter, whose in-memory slot registry and coordination generation cannot survive a restart, presenting a live pod whose sessions are all lost. The one per-container restart control that is generally available is the Kubernetes sidecar container, an `initContainers` entry carrying `restartPolicy: Always`, and it fails on three independent grounds:

- The supported Kubernetes floor is 1.27 (`charts/lenny/Chart.yaml:8`, `# §17.6: Kubernetes >= 1.27 is the supported floor`). The feature is beta from 1.29 and generally available from 1.33, so adopting it raises the deployment floor for one capability on one deployment model.
- The kubelet restarts a container on every exit and cannot be told to restart on some exits and not others. §4.7.11 item 5 states the opposite policy for this exact process, that the adapter does not restart the agent and the gateway handles retry at the session level, and §5.2 retires a pod whose session ended in a crash. A restartable runtime container would bring a crashed agent back while the gateway is retiring its pod.
- The restart fires when the process exits, which on a recycling pod is at occupancy zero, before the whole-pod scrub runs. The successor process would be live on the unscrubbed `/workspace` and `/tmp` volumes and could read the previous session's data, and no adapter-side gate closes that, because the adapter can withhold work from the process but not the filesystem the kubelet already mounted for it. The kubelet's restart backoff, which applies to a clean exit as readily as to a crash and which is node configuration rather than a pod-spec setting, then grows the successor's start latency across a pod's life in exactly the regime recycling targets.

The root of all three is that a container restart is a pod-lifecycle event while the successor process must be created at a specific point inside a session-lifecycle sequence, after the scrub and before the next claim, and Kubernetes offers no barrier that would order one inside the other.

### 9.2 An adapter-side spawn generalising `SpawnPath`

Rejected as structurally impossible on the transport in question, on a fact the finding's dossier had wrong: `SpawnPath` has no production caller. Beyond that, the adapter container and the runtime container are separate images with separate mount namespaces, so the adapter cannot exec a binary that exists only in the runtime image, and an interpreter runtime is a dependency tree rather than a file that could be copied. Making it work means putting the runtime binary in the adapter container, which runs the untrusted third-party binary at the adapter's UID beside the credential leases and the gateway identity, discards §4.7.11's separate-UID boundary and the credential file's `0440` cross-UID delivery, and gives up the any-language packaging property §4.7.10 gives as the sidecar model's reason for existing. §13.1 also drops `CAP_SETUID`, so the adapter cannot fork a child under the agent UID even if the binary were present.

### 9.3 Refusing the combination at pool admission

Not rejected on merit; rejected as the change to make now, and escalated in §12. It is the louder gate and it tells the deployer at write time. Its cost is that the signal does not exist anywhere the gateway can read it. `applyCRDFields` states that `DeploymentModel` "is a CRD-only field with no registry counterpart and is intentionally not mirrored" (`pkg/controller/runtime/controller.go:237`), and the admin runtime registration payload does not model it, so an admin-registered runtime has no deployment model and resolves to `sidecar`. Landing the gate means adding the field to the REST payload, the OpenAPI document, the client SDKs, the registry record, and a Postgres migration, reversing an explicit prior decision, and then deciding whether every admin-registered runtime in the tree, fixtures included, is refused recycling. The precedent the platform set for a deployment-model-conditional rule is the opposite one: the §4.7.11 nonce-only gate is enforced CRD-side by the RuntimeReconciler and the render-side check keys on the derived value, which is the pattern D3 follows.

### 9.4 The successor-runtime capability, deferred with its strongest candidate named

Deferred to a named follow-up rather than rejected, because the platform may decide it must exist (§12, open decision 1). The strongest candidate is a platform-supplied supervisor process as the runtime container's PID 1: an init container stages a static first-party binary into a shared volume, the pod builder sets it as the runtime container's command, and it execs the runtime's declared argv once per session on the adapter's instruction, reports the child's exit, and kills the runtime container's remaining processes for the scrub's step 1. It satisfies "a fresh runtime process for each session" literally, needs no kubelet restart, needs no cross-container exec, needs no change to the runtime SDKs whose single-shot `Run` is exactly what it drives, and it is the only candidate that makes scrub steps 1 and 3 true in the runtime container rather than merely restating them.

Its costs are why it is a proposal of its own rather than a section of this one. It needs a registration field naming the image's entrypoint, which duplicates the image's own configuration and drifts silently after a re-tag. It needs a new adapter-to-supervisor conversation, which is a §28.3 register entry with its own identifier, socket, peer authentication, and failure contract, sitting inside the boundary §4.7.11 declares untrusted. And the supervisor runs at the agent UID in the same container as the author's binary, so `SO_PEERCRED` and the manifest nonce cannot distinguish the two and the only available control is an ordering rule: the adapter accepts one connection on that channel per pod, and the supervisor dials at container start, before any author process exists. That is a security-relevant mechanism whose defence is an assumption about ordering, and it deserves the adversarial review a proposal gets.

Everything this proposal stages is a prerequisite of that follow-up rather than a competitor to it: the specification that names the creator per deployment model, the scrub-reach statement, a conformance property that can fail, and an un-skipped tier-5 case. The follow-up flips the label's predicate for supervised pods and changes one test property.

### 9.5 Also out of scope

- **Keeping the runtime alive across the recycle boundary.** Foreclosed by §15.4, §6.1's recycle row, §5.2 scrub steps 1 and 3, the published runtime-author contract, and proposals 0031 and 0034, and by an implementation attempt that reverted.
- **The `SocketRuntimeProcess.Close` listener teardown.** Proposal 0078, which lands first.
- **Any change to `releaseActiveLocked` or the occupancy-zero gate.** Correct as they stand.
- **Per-slot cleanup**, which is unaffected: it runs at every session release on a pod of any recycle setting and does not depend on a successor runtime.
- **The first-session manifest ordering on the sidecar transport.** §4.7.9 orders the final manifest write before the runtime's manifest read, and on the sidecar transport the runtime process starts at pod boot and reads the manifest the adapter writes inside `StartSession` (`pkg/adapter/session.go:124`), so the read precedes the write on a pod's first session and the adapter writes no placeholder manifest at boot. This is a pre-existing defect on every sidecar pod rather than a recycling one, and it is filed separately rather than cured here (§12, open decision 5).

## 10. Testing

Tier selection follows `.claude/rules/test-coverage.md`: the change touches pure decision logic (tier 1), a reconciler writing the apiserver (tier 2), a gateway flow across the datastore and the pod (tier 4), a cluster behaviour (tier 5), an ordering and atomicity property (tier 7a), the runtime adapter contract (tier 10), and documentation (tier 11). No wire contract changes, so tier 3 is not reached.

**TEST-1 (tier 1, `pkg/sandbox/podscrub`).** Table cases on `Decide`, each a non-happy path:

- `RuntimeRecreatable: false` with a clean scrub retires with `no_successor_runtime` and `NextPhase: Draining`.
- `RuntimeRecreatable: false` with `VMRestart: true` retires with `vm_restart_reprovision`, pinning the precedence.
- `RuntimeRecreatable: false` with `SessionsServed == MaxSessionsPerPod == 1` retires with `no_successor_runtime` rather than `session_count_limit`. This is the reason-divergence boundary §4.2 argues, and the case that fails if the branch is moved after the session-count branch.
- `RuntimeRecreatable: false` with `Scrub: ScrubFailed` and `OnCleanupFailure: fail` keeps `cleanup_fail_policy` and the `Failed` terminal, so the fail-closed path is not weakened.
- `RuntimeRecreatable: false` with `Scrub: ScrubFailed` under `warn` retires with `no_successor_runtime` and `ScrubWarning: true`.
- `RuntimeRecreatable: false` with `Scrub: ScrubPending` returns not-ready, so the branch does not pre-empt the wait.
- `RuntimeRecreatable: true` with every other input unchanged reuses, proving the branch is the only thing that changed for the embedded case.
- `CountsOnRetirementTotal(ReasonNoSuccessorRuntime)` is false.

**TEST-2 (tier 1, `pkg/gateway/session/recycle`).** `runtimeRecreatable` returns false for an absent key, an empty value, `"TRUE"`, `"1"`, and `"yes"`, and true only for exactly `"true"`. The fail-safe parse is the security-relevant half of D4 and is asserted on the deny side.

**TEST-3 (tier 2, envtest, `pkg/controller/sandbox`).** The reconciler stamps `lenny.dev/runtime-recreatable: "false"` for a `deploymentModel: sidecar` Runtime, `"true"` for `embedded`, and `"false"` for a Runtime with an empty `deploymentModel`. `// diagnosis:` a failure means the recycle disposition reads a label the reconciler did not write, so every recycling pod either retires when it should be reused or is reused when it cannot serve the next session.

**TEST-4 (tier 10, conformance).** The recycle-scrub case is driven against a real `SocketRuntimeProcess` on a real abstract socket with a one-shot dialer that connects once and exits on EOF, which is what the shipped SDKs do. Properties 1, 3, and 4 are unchanged. Property 2 becomes falsifiable: it now asserts that the adapter Server survived the boundary against a transport that could have failed it. A new Property 5 asserts that the second session's `Start` returns an error, pinning the transport fact the disposition rests on. `// diagnosis:` a Property 5 failure means the sidecar transport gained a successor-runtime mechanism and the `no_successor_runtime` retire is now over-broad; a Property 2 failure means the adapter Server did not survive the recycle boundary.

**IMPLEMENTOR'S CHOICE:** whether the one-shot dialer is a goroutine in the test file or a reused reference-runtime helper. Any answer must dial exactly once and exit on EOF without redialing, and must not depend on `SpawnPath`, which has no production caller and which this proposal does not promote to one.

**TEST-5 (tier 5, Kind).** `TestSidecarRecycleRetiresPodAtOccupancyZero` on the existing `task-mode-echo-pool`: session A binds pod A and terminates, pod A's claim reaches `draining` rather than `reserved`, and session B binds a pod other than pod A and completes. `// diagnosis:` a failure means either the disposition regressed to reuse, in which case session B fails inside `Start` against a dead runtime, or the pod was retired for a different reason, in which case the label or its reader is wrong.

**TEST-6 (tier 5, Kind).** `TestEmbeddedRecycleScrubsWorkspaceBetweenSessions` on the FIXTURE-1 embedded recycling pool, carrying the original case's assertions: session B lands on session A's pod, and session A's content under `/workspace/slots/` is gone. This is the reuse-plus-scrub contract, exercised against a live pod for the first time.

**TEST-7 (register).** Delete the skip row at `tests/registers/skip-reasons.yaml:1120-1124`. Neither successor case is skipped, so the register must not carry the reason.

**TEST-8 (tier 7a, `-race`).** On a concurrent sidecar recycling pool, several slots release simultaneously and drive occupancy to zero. Exactly one retire disposition is applied and exactly one drain is stamped, with no interleaving that produces a `reserved` patch. This is the atomicity property the new branch shares with the existing ones, and `lenny-test stress` exercises the flake budget on it.

**Tier 11.** The doc-consistency pass covers the pages in §8.4: no page states pod reuse as available on every deployment model, and the glossary entry matches §5.2.

**Coverage.** `lenny-test coverage --diff <base-ref>` on the changed lines, to the 80% floor. The changed lines are branch-dense, so the tier-1 table reaches the target on its own; the higher tiers exist for the integration behaviour rather than for the number.

## 11. Findings closed on application

- **BUILD-GAPS F-5.2.33 part (b)** — the substantive half. Part (a), the `SocketRuntimeProcess.Close` listener teardown, is proposal 0078 and is not closed here.
- The skip-register entry for `TestTaskModeRecycleScrubsWorkspaceBetweenSessions` (`tests/registers/skip-reasons.yaml:1120-1124`).
- The unfalsifiable tier-10 Property 2.
- Proposal 0073 §9's recorded and uncured property.

## 12. Open decisions for review

1. **Is first-party-only pod reuse acceptable, or must the successor mechanism land now?** This is the decision the proposal turns on and the one it does not have standing to take. After this change, reuse across a recycle boundary works on the embedded deployment model, which is Go-only and which §4.7.10 explicitly tells third-party authors not to use. Recycling therefore serves the platform's own runtimes rather than its users'. A reviewer who judges that unacceptable should direct the §9.4 supervisor as an immediate follow-up, so the staged §5.2 text names it rather than leaving the limitation open-ended. Nothing in this proposal has to be unpicked either way: the follow-up flips the label's predicate for supervised pods and changes one test property.
2. **Boundary retire, or configuration-time refusal?** D2 and §9.3. A refusal at pool admission is louder and tells the deployer at write time. Its cost is a client-surface change across the REST payload, the OpenAPI document, the client SDKs, the registry, and a migration, plus a decision about admin-registered runtimes, which today have no deployment model and resolve to `sidecar`. A reviewer who judges configuration-time refusal to be the actual requirement should say so; the staged spec edits are compatible with that choice and most of them are needed by it too.
3. **A dedicated metric?** D6 declines one on cost grounds rather than correctness grounds. Detecting the degradation through `lenny_pod_session_reuse_count` requires an operator who already suspects it, and no alert can find it. If the reviewer weighs operator detection above surface cost, a counter is added and nothing else in the design changes.
4. **Should the retirement audit reason be surfaced to the client?** It is not today, and D7 leaves `sessionIsolationLevel.podReuse` reporting the pool's configuration. A reviewer may prefer that the session creation response report the pod's actual disposition, which would need the gateway to know the pod before it computes the field.
5. **Whether the first-session manifest-ordering defect (§9.5) is filed as its own finding.** It is pre-existing, it affects every sidecar pod rather than only recycling pools, and the §9.4 supervisor would repair it as a side effect. Recording it separately keeps the audit trail honest about what was broken before this change.

## 13. Files touched on application

**Specification**

- `spec/04_system-components.md` (SPEC-1, SPEC-2)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-3, SPEC-4, SPEC-5)
- `spec/06_warm-pod-model.md` (SPEC-6)
- `spec/15_external-api-surface.md` (SPEC-7)
- `spec/16_observability.md` (SPEC-8)

**Code**

- `pkg/controller/warmpool/pod_reconciler.go` (CODE-1, label constant)
- `pkg/controller/sandbox/controller.go` (CODE-1, label stamp)
- `pkg/sandbox/podscrub/podscrub.go` (CODE-2)
- `pkg/gateway/session/recycle/scrubreporter_seams.go` (CODE-3)
- `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go` (CODE-3)
- `pkg/adapter/scrub/scrub.go`, `pkg/adapter/scrub/defaultops.go` (CODE-4)
- `pkg/adapter/socketruntime.go` (CODE-4, doc comments only)
- `sdks/runtime/go/runtime/runtime.go` (CODE-4, doc comment only)

**Tests and fixtures**

- `pkg/sandbox/podscrub/podscrub_test.go` (TEST-1)
- `pkg/gateway/session/recycle/scrubreporter_seams_test.go` (TEST-2)
- `pkg/controller/sandbox/controller_test.go` (TEST-3)
- `tests/tier10_conformance/recycle_scrub_conformance_test.go` (TEST-4)
- `tests/tier5_e2e_kind/execution_modes_test.go` (TEST-5, TEST-6)
- `tests/testinfra/kind/agent-workload.yaml`, `tests/testinfra/kind/install.sh`, `tests/testinfra/kind/install.go`, `tests/testinfra/kind/bootstrap-overlay.gen.yaml` (FIXTURE-1)
- `tests/registers/skip-reasons.yaml` (TEST-7)
- A tier-7a concurrency case under `tests/tier7a_load_local/` (TEST-8)
- `tests/spec-map.json` (annotations for the new and changed cases)

**Documentation**

- `docs/runtime-author-guide/lifecycle.md`, `docs/runtime-author-guide/index.md` (DOC-1, DOC-3)
- `docs/reference/execution-modes.md`, `docs/reference/glossary.md` (DOC-2, DOC-3)
- `docs/runtime-author-guide/integration-levels.md`, `docs/about/why-lenny.md`, `docs/about/contributing.md` (DOC-3)
- `docs/operator-guide/scaling.md`, `docs/operator-guide/troubleshooting.md`, `docs/operator-guide/upgrades.md` (DOC-4)
