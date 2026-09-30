# Non-spec changes: Name who starts the next session's runtime on a recycled pod

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

## 6. Observability surface

No new metric and no new alert (D6). The change is observable through surfaces that already exist:

- `lenny_pod_session_reuse_count` p50 falls to 1 on an affected pool, which is the signal §5.2 already names for a sequential `vm-restart` pool.
- The pod's retirement audit record carries `reason: no_successor_runtime`. The §16.1 retirement-counter row names it beside `host_unschedulable`, `scrub_report_timeout`, and `vm_restart_reprovision`, none of which the row records today.
- `lenny_gateway_pod_retirement_total` is unchanged, deliberately, because the frozen vocabulary is the three limit triggers.
- `docs/operator-guide/troubleshooting.md` gains the narrative cause, so an operator who sees a recycling pool provisioning one pod per session finds the reason rather than filing it as a scaling defect.

## 7. CRD and RBAC changes

None. The label is written by the Sandbox reconciler, which already owns and patches the agent Pod, and is read by the gateway through the `get` verb on Pods in agent namespaces that the §6.2 host-schedulability read already requires. No CRD field is added; `Runtime.spec.deploymentModel` already exists and is unchanged.

## 8. Proposed changes (non-spec)

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
