# Spec changes: Name who starts the next session's runtime on a recycled pod

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

## 12. Open decisions for review

1. **Is first-party-only pod reuse acceptable, or must the successor mechanism land now?** This is the decision the proposal turns on and the one it does not have standing to take. After this change, reuse across a recycle boundary works on the embedded deployment model, which is Go-only and which §4.7.10 explicitly tells third-party authors not to use. Recycling therefore serves the platform's own runtimes rather than its users'. A reviewer who judges that unacceptable should direct the §9.4 supervisor as an immediate follow-up, so the staged §5.2 text names it rather than leaving the limitation open-ended. Nothing in this proposal has to be unpicked either way: the follow-up flips the label's predicate for supervised pods and changes one test property.
2. **Boundary retire, or configuration-time refusal?** D2 and §9.3. A refusal at pool admission is louder and tells the deployer at write time. Its cost is a client-surface change across the REST payload, the OpenAPI document, the client SDKs, the registry, and a migration, plus a decision about admin-registered runtimes, which today have no deployment model and resolve to `sidecar`. A reviewer who judges configuration-time refusal to be the actual requirement should say so; the staged spec edits are compatible with that choice and most of them are needed by it too.
3. **A dedicated metric?** D6 declines one on cost grounds rather than correctness grounds. Detecting the degradation through `lenny_pod_session_reuse_count` requires an operator who already suspects it, and no alert can find it. If the reviewer weighs operator detection above surface cost, a counter is added and nothing else in the design changes.
4. **Should the retirement audit reason be surfaced to the client?** It is not today, and D7 leaves `sessionIsolationLevel.podReuse` reporting the pool's configuration. A reviewer may prefer that the session creation response report the pod's actual disposition, which would need the gateway to know the pod before it computes the field.
5. **Whether the first-session manifest-ordering defect (§9.5) is filed as its own finding.** It is pre-existing, it affects every sidecar pod rather than only recycling pools, and the §9.4 supervisor would repair it as a side effect. Recording it separately keeps the audit trail honest about what was broken before this change.
