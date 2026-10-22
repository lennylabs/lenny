# Spec changes: Sidecar runtime processes have no platform supervisor

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

Each staged edit is written against the specification at `f52fc9a49` (2026-10-22), in which proposals 0078, 0079, 0090, 0091, 0092, 0093, and 0094 have landed. Every anchor is located by heading and quoted text. None is located by line. The re-baseline of 2026-10-22 rewrote the draft's edits against the decisions in the summary. The review run converges the wording.

Each statement has one home in the specification. Every other site cites that home.

| Statement | Home |
|:--|:--|
| The `command` field and which runtimes carry it | SPEC-1, §5.1 **`command`** |
| The `env` field and its reserved prefix | SPEC-1, §5.1 **`env`** |
| What the supervisor is, when it launches and ends a runtime process, how it runs `cleanupCommands`, its environment and signals, liveness, crash, and pod termination | SPEC-2, §4.7.10 **Runtime supervisor.** |
| The SDK-warm lifecycle on a sidecar pod: the warm-up launch and the circuit breaker, `ConfigureWorkspace`, `DemoteSDK`, and the re-warm edge | SPEC-2, §4.7.10 **Runtime supervisor.** **SDK-warm.** |
| What the runtime sweep removes | SPEC-2, §4.7.10 **Runtime supervisor.** **Sweep.** |
| Startup order | SPEC-3, §4.7.9 steps 4 and 7 |
| Why the adapter trusts the supervisor, and what the reuse decision rests on | SPEC-4, §4.7.11 item 8 |
| The property self-test, how its result reaches the adapter, and which pods it gates | SPEC-4, §4.7.11 item 8 **Property self-test.** |
| What a `keep` pod and a `restart` pod are, and where `recycle.runtimeProcess` is read | SPEC-5, §5.2 **Runtime-process lifetime (`recycle.runtimeProcess`).** |
| `recycle.runtimeProcess`, its admission rules, the acknowledgment, the scrub order, which component runs `cleanupCommands` on each pod, and what the scrub reaches | SPEC-5, §5.2 |
| The supervisor volume and the stage container's identity | SPEC-6, §13.1 |
| The `CH-SUPERVISE` endpoint, messages, authentication, exclusivity, and failure behavior | SPEC-7, the §28.5.3 card |
| The `shutdown` deadline, signals, and exit codes | SPEC-8, the §28.5.3 `CH-MSGSOCK` card |

## Edge cases and accepted failure modes

| Case | Observable outcome | Home |
|:--|:--|:--|
| The supervisor never connects, or sends no `ready` | The adapter exits non-zero before READY, and the pod never becomes `idle` | SPEC-3, SPEC-7 |
| A warm-up launch fails, or its process does not connect on `CH-MSGSOCK` within the adapter's wait | The adapter exits non-zero before READY, and the pod never becomes `idle` | SPEC-2 **Launch.** |
| A `preConnect` pod is created while the pool's SDK-warm circuit breaker is open | No warm-up launch; the pod warms pod-warm, and its first session's start launches the process at step 7 | SPEC-2 **SDK-warm.** |
| The property self-test fails on a gating property | On a `restart` pod the adapter logs the failed properties from `ready` and exits non-zero before READY, and the pod never becomes `idle`; on a `keep` pod the adapter logs them and the pod serves | SPEC-4, SPEC-7 |
| `launch` fails | `launched` carries an error code, the session's start fails, and the pod is reported not live | SPEC-2, SPEC-7 |
| The runtime process exits while a session is bound | `exited` reaches the adapter, each bound session fails with `runtime_crash` into the §7.3 resume flow, and the pod retires at the boundary | SPEC-2 **Crash.**, **Liveness.** |
| The runtime process of a `restart` pod exits after its last session and before the boundary | The boundary's `end` finds no process, and the pod does not retire | SPEC-2 |
| The runtime process of a `keep` pod exits with no session bound | The pod is reported not live and retires; a session that starts first fails its start | SPEC-2 **Liveness.**, **Launch.** |
| A service-mode runtime process exits, or `CH-SUPERVISE` fails on a service-mode pod | The adapter exits non-zero, and the pod is replaced | SPEC-2 **Liveness.** |
| A heartbeat escalation on a `keep` pod | The pod is reported not live at the boundary and retires | SPEC-2 **Liveness.** |
| A process survives two `end` passes, or the sweep fails | The pod is reported not live and retires, whatever `onScrubFailure` says | SPEC-2, SPEC-5 |
| The supervisor misses a `pong` deadline, or is killed or frozen | The pod is reported not live and retires | SPEC-2, SPEC-7 |
| A reply with an unknown key, a duplicate key, or an unmatched `gen` or `id` | Link failure, and the pod retires | SPEC-7 |
| Pod termination | The supervisor forwards the kubelet's SIGTERM, and exits when `CH-SUPERVISE` closes | SPEC-2 **Pod termination.** |
| A pool sets `restart` on a runtime that is not `restartable` | Pool admission refuses the pool | SPEC-5 |
| A Runtime's `env` names a variable with the prefix `LENNY_`, or names one variable twice | The API server refuses the Runtime | SPEC-1 |
| A `cleanupCommands` command exits non-zero, or the commands together outlast `cleanupTimeoutSeconds` | `cleaned` carries an error, and the scrub fails under `onScrubFailure` as a failed cleanup does today | SPEC-5, SPEC-7 |
| A pool's `recycle.runtimeProcess` is edited from `keep` to `restart`, and the pool no longer requires `acknowledgeProcessLevelIsolation` | Every acquisition refuses each used `keep` pod and requests its drain; the pods created after the edit are `restart` pods | SPEC-5 |
| Nonce-only mode | Neither peer-UID check is available, and ordering identifies both ends | SPEC-4 |

## Staged edits

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.1 Runtime

(a) In the **Standalone Runtime** YAML example, insert after the line that begins `requireSoPeercred: true # optional;`:

```yaml
command: ["/usr/local/bin/my-agent", "--serve"] # see command below
env: # optional; see env below
  - name: PYTHONUNBUFFERED
    value: "1"
```

(b) Insert after the **`requireSoPeercred`** paragraph, which ends `so a runtime registered only through the admin API runs with the default of `true`.`, and before the first `#### Derived Runtime` heading (the one directly after that paragraph, not `#### Derived Runtime Instantiation`):

```
**`command`** — the argv of the runtime process. Required on a `type: agent` runtime whose `deploymentModel` is `sidecar`, and forbidden on every other runtime. Its first element is an absolute path in the runtime image. The runtime supervisor launches this argv as the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**). The API server rejects a `Runtime` that breaks either rule or whose first element is not absolute. The RuntimeReconciler mirrors the field into the gateway runtime registry together with `restartable`, which is true for a runtime that carries `command`, has `executionMode: session`, and does not declare `capabilities.preConnect: true` ([Section 5.2](#52-pool-configuration-and-execution-modes), `recycle.runtimeProcess`).

**`env`** — optional variables for the runtime process, as a list of `name` and `value` pairs. Forbidden wherever `command` is. Each `name` is a non-empty POSIX variable name, no name appears twice, and no name begins with `LENNY_`, which the platform reserves for its own variables; the API server rejects a `Runtime` that breaks any of these rules. The runtime supervisor applies the pairs as [Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, states. The values are readable by the runtime and are not a channel for secrets, which reach the runtime through its credential file ([Section 4.7](04_system-components.md#47-runtime-adapter)). The RuntimeReconciler mirrors the field into the gateway runtime registry. The admin registration payload models neither `command` nor `env`.
```

(c) Add `command` and `env` to the **Never overridable on derived runtime:** list, and add a `command` row and an `env` row to the table under `#### Normative Merge Algorithm` as **Inherited** with "derived may not set", matching `requireSoPeercred`, because the argv and its environment are properties of the base runtime's image.

(d) In `spec/17_deployment-topology.md` §17.4, in the `runtime-crds.yaml` heredoc, insert `  command: ["/usr/local/bin/my-agent"]` after the line `  deploymentModel: sidecar`.

(e) In `spec/26_reference-runtime-catalog.md` §26.1, after the paragraph that begins `All reference runtimes are **`type: agent`**`, insert:

```
Each reference runtime is a sidecar runtime whose `command` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)) is `["/usr/local/bin/lenny-<name>-adapter"]`, where `<name>` is the runtime name.
```

In §26.3, in the claude-code **`Runtime` definition** YAML, insert `command: ["/usr/local/bin/lenny-claude-code-adapter"]` after the line `type: agent` that follows `image: ghcr.io/lennylabs/runtime-claude-code:1.0.0`.

### SPEC-2 · spec/04_system-components.md § 4.7.10 Deployment Model and § 4.7.1, spec/11_policy-and-controls.md § 11.4, and spec/10_gateway-internals.md § 10.1

(a) In the **Runtime process lifetime.** paragraph, replace its first sentence, `The runtime process lives as long as the pod.`, with:

```
On a `keep` pod
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
"Runtime-process lifetime"), the runtime process lives as long as the pod
unless `DemoteSDK` ends it (**Runtime supervisor.**, **SDK-warm.** below). On a
`restart` pod each runtime process lives from its launch to the next
whole-pod scrub, which ends it
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), **Lenny scrub procedure.**).
The sentences of this paragraph about one process or one `CH-MSGSOCK`
connection for the pod's life apply to each process.
```

In the same paragraph, make these replacements. The paragraph is hard-wrapped, so each find string below quotes its line breaks.

Find:

```
the sidecar model it is the process the kubelet started in the runtime
container, and its
```

Replace with:

```
the sidecar model it is the process the runtime supervisor launched in the
runtime container (**Runtime supervisor.** below), and its
```

Find:

```
the agent process and which `DemoteSDK` tears down and the SDK re-warm restarts, is
distinct from the runtime process, and neither ends the runtime process.
```

Replace with:

```
the agent process, is in the sidecar model the runtime process, which `DemoteSDK`
ends (**Runtime supervisor.**, **SDK-warm.** below); in the embedded model it is distinct from
the runtime process, and neither `DemoteSDK` nor the SDK re-warm ends the runtime process.
```

Delete this whole sentence, including the `A` that ends the line before it, so the paragraph continues with `A recycling pool whose pods serve`:

```
A
runtime process that stops, or whose connection the hold timeout closed, is not
re-created or reconnected inside the pod: the adapter
reports it at the next recycle boundary and refuses the next session's start,
and the pod is retired
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
```

Find:

```
A recycling pool whose pods serve more than one session in the kept process
requires a deployer acknowledgment
```

Replace with:

```
A recycling pool whose pods serve more than one session in a kept process
requires a deployer acknowledgment
```

Find, on one line of the paragraph:

```
or heartbeat escalation, and no occupancy-zero boundary, closes that connection
```

Replace with:

```
or heartbeat escalation, and, on a `keep` pod, no occupancy-zero boundary, closes that connection
```

(b) Insert after the **Runtime process lifetime.** paragraph and before `**Health check:**`:

```
**Runtime supervisor.** On every sidecar pod of a `type: agent` runtime the
runtime container's first process, and the init of its process namespace, is
the runtime supervisor, a platform binary from its own digest-pinned image that
an init container copies onto a read-only mount
([§13.1](13_security-model.md#131-pod-security)). The
supervisor launches the runtime's `command`
([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)) when the adapter asks
it to on `CH-SUPERVISE` ([§28.5.3](28_communication-channels.md#2853-intra-pod)),
and launches nothing otherwise. In the embedded model there is no runtime
container and no supervisor.

- **Environment.** Each launch starts from an empty environment to which the
  supervisor adds a fixed `PATH`, the runtime's `env`
  ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)), whose `PATH`
  pair replaces the fixed one, and the platform's runtime variables. The image's `ENTRYPOINT`, `CMD`, and `ENV` are
  not used. The supervisor reads no file from the runtime image's filesystem.
- **Launch.** The adapter asks for a launch only when the pod has launched no
  runtime process since its creation or its last attested end, after the
  manifest the process reads exists, and after every adapter listener the
  runtime dials is bound; a session that starts while a process is live, or
  while a launch is in flight, uses that process. A service-mode runtime, and a `preConnect` runtime when
  **SDK-warm.** says so, is launched at pod warm-up, and the adapter signals
  READY ([§4.7.9](#479-startup-sequence-for-type-agent-runtimes) step 4) only
  after that process has connected on `CH-MSGSOCK`; a warm-up launch that
  fails, or whose process does not connect within the adapter's bounded wait,
  makes the adapter exit non-zero, and the pod never becomes `idle`. Every
  other launch is at startup step 7
  ([§4.7.9](#479-startup-sequence-for-type-agent-runtimes)). On a
  `restart` pod no launch follows an occupancy-zero boundary before that
  boundary's whole-pod scrub has run
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  **Lenny scrub procedure.**). A session that finds no live process when this
  bullet permits no launch fails its start
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  "Runtime not live").
- **End.** The supervisor ends a runtime process by sending SIGTERM and then
  SIGKILL to every other process in the runtime container, and attests the end
  only after two consecutive passes find no other process. On a `restart` pod
  the end runs, with no SIGTERM grace, at the points the
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)
  **Lenny scrub procedure.** names.
  The end is also the `DemoteSDK` teardown (**SDK-warm.**) and the
  deadline action of a `shutdown`
  ([§28.5.3](28_communication-channels.md#2853-intra-pod)). Between an attested
  end and the next launch the adapter closes and rebinds its `CH-MSGSOCK`
  listener and, when bound, its `CH-RUNTIMEOPS` listener, so no connection the
  ended process opened or left pending reaches the next process.
- **Sweep.** On a `restart` pod, after each attested end at the points the
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)
  **Lenny scrub procedure.** names, a worker the supervisor forks, which holds
  no `CH-SUPERVISE` descriptor, removes the agent UID's System V shared memory
  segments, message queues, and semaphore sets, matched on the creating or the
  owning UID, the agent UID's POSIX message queues, and every entry the agent
  UID owns, symbolic links included, in the runtime container's writable
  volumes, which are `/sessions`, `/artifacts`, `/tmp`, `/dev/shm`, and the
  workspace base other than its `slots/` directory. The sweep leaves `slots/` to scrub
  step 2. The worker follows no symbolic link and bounds its walk. The supervisor then
  ends every other process again as **End.** states before it answers.
- **Cleanup.** At every recycle boundary the supervisor runs the deployer's
  `cleanupCommands` at the agent UID in a worker it forks, with the workspace
  base as working directory and the launch environment plus the step-0
  cleanup metadata
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
  The worker stops the running command when the commands together outlast
  `cleanupTimeoutSeconds`.
- **SDK-warm.** On a `preConnect` runtime
  ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)) the SDK
  process is the runtime process. Whether a pod launches it at warm-up is fixed
  when the WarmPoolController creates the pod: the pod launches it when the
  pool's SDK-warm circuit breaker is closed at that moment
  ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like),
  **Circuit-breaker for SDK-warm pools.**), and otherwise warms pod-warm with no
  process. `ConfigureWorkspace` gives the session to the live process, and when
  no process is live it launches one, as **Launch.** permits, at startup step 7
  ([§4.7.9](#479-startup-sequence-for-type-agent-runtimes)) after it writes the
  final manifest. `DemoteSDK` ends the live process (**End.**) with a SIGTERM
  grace that ends within the request's deadline, and the session's start then
  launches a new process at step 7. A `DemoteSDK` whose wait for the slot
  serialization outlasts its deadline sends no `end` and tears nothing down
  ([§28.5.3](28_communication-channels.md#2853-intra-pod), **Session frame
  writes.**). A `DemoteSDK` whose `end` fails returns an error, and the runtime
  is reported not live (**Liveness.**). A `preConnect` runtime is never
  `restartable` ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime),
  **`command`**), so each of its pods is a `keep` pod, which keeps its live
  process across each recycle boundary, and the adapter launches nothing on the `claimed → sdk_connecting`
  re-warm edge ([§6.2](06_warm-pod-model.md#62-pod-state-machine)): the kept
  process, whichever launch started it, is the pod's SDK-warm process.
- **Generation.** Each launch starts a new runtime generation. The rule that
  counts the sessions the pod's shared runtime process has been given
  ([§28.6](28_communication-channels.md#286-exclusivity-and-concurrency-model))
  counts from the generation's launch.
- **Crash.** A runtime process that exits while a session is bound, other than
  by an `end`, has crashed, and each bound session fails with `runtime_crash`
  ([§7.3](07_session-lifecycle.md#73-retry-and-resume)); **Liveness.** states
  the pod's outcome.
- **Liveness.** The adapter reports the runtime as not able to serve the next
  session once any of these has happened on the pod: a `launch`, `end`, or
  `sweep` failed or `CH-SUPERVISE` failed
  ([§28.5.3](28_communication-channels.md#2853-intra-pod)); a runtime process
  exited other than by an `end` while a session was bound (**Crash.**); or, on
  a `keep` pod, the process exited, or its
  `CH-MSGSOCK` connection ended, other than by an `end`, or a heartbeat
  escalated a session as hung. The pod is then retired
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  "Runtime not live"). A service-mode pod has no recycle boundary at which to
  report, so its adapter exits non-zero instead, and the pod is replaced.
- **Signals.** The supervisor handles every catchable signal. It treats a
  catchable signal sent from outside its process namespace as the stop request
  and ignores every other. A runtime process can still end or freeze the
  supervisor with SIGKILL or SIGSTOP on a runtime class where the namespace
  init is not immune to them, which retires the pod.
- **Pod termination.** On the stop request the supervisor forwards SIGTERM to
  every other process in the container and keeps its `CH-SUPERVISE`
  connection. When the connection closes, the supervisor exits, and its exit
  as the namespace init ends every remaining process in the runtime container.
```

(c) In the sidecar-versus-embedded trade-off table, in the `Runtime process lifetime` row, replace the sidecar cell, which begins `The pod's lifetime; one connection serves every session`, with the text below. The row stays one physical line.

```
From its launch by the runtime supervisor to its end (**Runtime process lifetime.** below); one connection per process serves its sessions, each opened by `session_start` and released by `session_end` or by the end of the connection
```

(d) In §4.7.1, in the gateway RPC table's `Shutdown` row, delete `, which stays alive for the pod's life` from `ends the session's use of the pod's runtime process, which stays alive for the pod's life ([Section 4.7.10](#4710-deployment-model))`. In the same row, replace `the adapter keeps its own process and the runtime process alive across the recycle boundary, runs the §5.2 whole-pod scrub asynchronously` with `the adapter keeps its own process alive across the recycle boundary, keeps the runtime process alive on a `keep` pod, runs the §5.2 whole-pod scrub asynchronously`. The row stays one physical line.

In the same table's `ReportPodScrub` row, replace `whether the adapter's runtime process can serve the next session, sampled after the scrub finishes; a sidecar runtime process whose connection is open is reported as able to, whether or not it answers heartbeats` with `whether the adapter's runtime can serve the next session, sampled after the scrub finishes, as [§4.7.10](#4710-deployment-model) **Runtime supervisor.** **Liveness.** states in the sidecar model`. The row stays one physical line.

In `spec/11_policy-and-controls.md` §11.4 **Full revoke propagation mechanism:** step 3, delete `, which does not end the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime")`.

(e) In `spec/10_gateway-internals.md` §10.1, in the **Hold state timeout:** bullet, replace `; that pod serves no later session and is retired ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime process lifetime**)` with `; [Section 4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.** **Liveness.** states the pod's outcome`.

### SPEC-3 · spec/04_system-components.md § 4.7.9 Startup Sequence for `type: agent` Runtimes

(a) Append to step 4, `4. Adapter signals READY to gateway — pod enters warm pool`, on the same list item:

```
 In the sidecar model the adapter signals READY only after the runtime supervisor has connected on `CH-SUPERVISE` and sent `ready`, after the property self-test gate of [§4.7.11](#4711-adapter-agent-security-boundary) item 8, and after the warm-up launch, on a pod that has one, that [§4.7.10](#4710-deployment-model) **Runtime supervisor.** **Launch.** states.
```

(b) Replace step 7, from `7. The runtime process becomes live for the pod's first session.` through `the runtime loop in its own process for each session.`, with:

```
7. The runtime process becomes live. In the sidecar deployment model
   ([§4.7.10](#4710-deployment-model)) the adapter has the runtime supervisor
   launch the runtime's `command` here, after step 6 wrote the final manifest,
   when **Runtime supervisor.** **Launch.** permits a launch, and accepts that
   process's `CH-MSGSOCK` connection. A session that uses an existing process under
   **Runtime supervisor.** **Launch.** skips this step and steps 8 and 9 and takes step 10.
   In the embedded deployment model the
   adapter runs the runtime loop in its own process for each session.
```

### SPEC-4 · spec/04_system-components.md § 4.7.11 Adapter-Agent Security Boundary

Insert after item 7, **MCP server security:**, which ends `by directly connecting to the MCP socket without manifest access.`, and before `### 4.8 Gateway Policy Engine`:

```
8. **Runtime supervisor:**
   - **Where it runs.** The runtime supervisor
     ([§4.7.10](#4710-deployment-model), **Runtime supervisor.**) runs at the
     agent UID on the untrusted side of this boundary. The adapter sends every
     request on `CH-SUPERVISE`, the supervisor sends only its `ready` report,
     replies, and exit reports, and no author-controlled byte crosses the
     channel, so item 2 and item 3 hold.
   - **Why each end trusts the other.** Each end checks the other's UID as
     the [§28.5.3](28_communication-channels.md#2853-intra-pod) `CH-SUPERVISE`
     card states. The connection is made before any author code exists in the pod: the
     supervisor is the runtime container's first process, it launches nothing
     before `launch`, and [§13.1](13_security-model.md#131-pod-security)
     **Container identity.** gives the agent and adapter UIDs to one container
     each. In nonce-only mode that ordering alone identifies both ends.
   - **Protection from author code.** The supervisor is a static binary with no
     program interpreter that loads no library, clears its environment before
     anything else, and opens only `/proc` and platform volumes without
     following symbolic links. It marks itself non-dumpable, so code at the
     agent UID with every capability dropped cannot attach to it or read its
     memory or descriptors. Work that reads author-controlled data, and every
     `cleanupCommands` command, runs in a forked worker that holds no
     `CH-SUPERVISE` descriptor. Its binary is on a read-only mount.
   - **What rests on it.** The adapter's decision to give a later session a
     new runtime process on a `restart` pod rests on the supervisor's attested
     end and sweep. Tenant isolation does not: the tenant pin applies on every
     pool ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
     A killed, frozen, or failed supervisor retires the pod.
   - **Property self-test.** At startup, before it dials, the supervisor
     tests that its non-dumpable mark resists same-UID inspection, that it
     identifies a signal's sender, and that its kill reaches a double-forked
     descendant, and it tests whether it is immune as PID-namespace init to
     SIGKILL and SIGSTOP. It reports each result in its `ready` message on
     `CH-SUPERVISE` ([§28.5.3](28_communication-channels.md#2853-intra-pod)),
     and the adapter logs each failed property. The non-dumpable mark, sender
     identification, and kill reach gate a `restart` pod
     ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
     "Runtime-process lifetime"): when one of them failed, the adapter exits
     non-zero before it signals READY, and the pod never becomes `idle`. On a
     `keep` pod no property gates. PID-namespace init immunity gates no pod,
     because its failure retires only the pod whose runtime exploits it.
     `lenny-ctl preflight supervisor`
     ([§24.2](24_lenny-ctl-command-reference.md#242-preflight)) runs the
     same self-test on a named RuntimeClass, and it is re-run when the
     container runtime version changes, as item 1 requires for `SO_PEERCRED`.
```

### SPEC-5 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool Configuration and Execution Modes

(a) In the `sessionPolicy` YAML block that follows the **Slot (session mode).** paragraph (the block whose first line is `sessionPolicy:                            # session mode only`), insert after its line `    maxSessionsPerPod: 50                 # required when enabled — counts every session served; pod retired at the bound`:

```yaml
    # runtimeProcess: keep                # keep | restart — see "Runtime-process lifetime" below
```

In the §5.1 derived-runtime example, insert after the line `    maxSessionsPerPod: 50 # required when enabled — pod retired after serving this many sessions`, and in the §5.2 **Deployer acknowledgment.** example, insert after the line `    maxSessionsPerPod: 50 # required — no default, forces deployer choice`, respectively:

```yaml
    runtimeProcess: keep # required on this pool (see "Runtime-process lifetime" in Section 5.2)
```

```yaml
    runtimeProcess: keep # required on this pool (see "Runtime-process lifetime" above)
```

(b) Insert after the **Recycling and integration levels.** paragraph:

```
**Runtime-process lifetime (`recycle.runtimeProcess`).** Every agent pod has a
runtime-process lifetime, `keep` or `restart`, which the WarmPoolController
fixes when it creates the pod, from the pool's `recycle.runtimeProcess` at that
moment, or `keep` on a pool without the field, and records in the immutable pod
label `lenny.dev/runtime-process`
([Section 17.2](17_deployment-topology.md#172-namespace-layout), item 5). A
**`keep` pod** is a pod whose lifetime is `keep`, and a **`restart` pod** is a
pod whose lifetime is `restart`. A `keep` pod keeps its runtime process across
the recycle boundary
([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime
process lifetime"). A `restart` pod ends its runtime process in each whole-pod
scrub (**Lenny scrub procedure** below), and the next session runs in a
newly launched process
([Section 4.7.10](04_system-components.md#4710-deployment-model),
**Runtime supervisor.**). Pod retirement at `recycle.maxSessionsPerPod` or
`recycle.maxPodUptimeSeconds` is the same on a `keep` pod and a `restart` pod.
An acquisition reads an absent or unknown value of the label as `keep`. Every
other statement of a pod's runtime-process lifetime names a `keep` pod or a
`restart` pod. Only the WarmPoolController at pod creation, the pool admission
rules below, and the runtime-process acknowledgment below read
`recycle.runtimeProcess`. A reusing recycling pool is a pool with
`recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, and a
`recycle.scrubProfile` other than `vm-restart`. On a reusing recycling pool
`recycle.runtimeProcess` is required and has no default; the pool controller
rejects it on every other pool. The gateway pool admission path rejects
`restart` on a pool whose runtime is not `restartable`
([Section 5.1](#51-runtime)). The WarmPoolController renders `restart` only for
a runtime that is `restartable`, and otherwise creates no pod for the pool. An
admitted edit of the field applies to the pods created after it. The
runtime-process acknowledgment below states which pods created before the edit
an acquisition refuses.
```

In the paragraph before it, **Recycling and integration levels.**, which is hard-wrapped, find:

```
adapter-executed and gateway-coordinated. The runtime process is kept across the
recycle boundary ([Section 4.7.10](04_system-components.md#4710-deployment-model)),
```

and replace it with:

```
adapter-executed and gateway-coordinated. On a `keep` pod ("Runtime-process
lifetime" below) the runtime process is kept across the recycle boundary
([Section 4.7.10](04_system-components.md#4710-deployment-model)),
```

In the same paragraph, replace `A runtime that exits after its session makes the pod retire` with `On a `keep` pod, a runtime that exits after its session makes the pod retire`.

(c) In **Lenny scrub procedure**, replace `The scrub has two phases: a **pre-cleanup credential purge** (step 0) executed by the adapter before `cleanupCommands`, and a **post-cleanup scrub sequence** (steps 1-6) executed after `cleanupCommands` finish.` with:

```
The scrub runs in this order: the **pre-cleanup credential purge** (step 0); on a `restart` pod ("Runtime-process lifetime" above), the **runtime end and sweep**, in which the runtime supervisor ends the runtime process and sweeps what the agent UID left ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **End.** and **Sweep.**); the deployer's `cleanupCommands`; on a `restart` pod, a second runtime end and sweep, which remove every process and agent-UID object the commands left; and the **post-cleanup scrub sequence** (steps 1-6). On a pod that runs the runtime supervisor ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**) the supervisor runs `cleanupCommands`; on every other pod the adapter runs them in its own container. A failed end or sweep retires the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**) whatever `onScrubFailure` states. A failed or timed-out `cleanupCommands` run fails the scrub as before.
```

In the **Lenny scrub procedure** paragraph, replace the sentence `The adapter ends the ending session's use of the runtime, keeps its own process and the runtime process alive across the recycle boundary, runs the scrub asynchronously,` with `The adapter ends the ending session's use of the runtime, keeps its own process alive across the recycle boundary, keeps the runtime process alive on a `keep` pod, runs the scrub asynchronously,`, and replace `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process;` with `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process on a `keep` pod and to a newly launched one on a `restart` pod;`, and replace `the adapter holds no recycle state of its own` with `the adapter holds no recycle state of its own other than the pod's runtime-process lifetime ("Runtime-process lifetime" above)`.

(d) In step 1, replace `The kill runs in the container that executes the scrub and does not end the runtime process; see "What the scrub reaches" below.` with:

```
On a pod that runs the runtime supervisor ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**) the adapter container runs no author process, and the runtime container's processes are ended on a `restart` pod by the runtime ends the **Lenny scrub procedure** paragraph names and are kept on a `keep` pod. On every other pod the kill runs in the adapter container. See "What the scrub reaches" below.
```

In step 1b, append `On a pod that runs the runtime supervisor the adapter does not run this step; on a `restart` pod the runtime sweep removes the agent UID's IPC objects ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **Sweep.**), and on a `keep` pod they persist.`

In step 3, append `On a pod that runs the runtime supervisor each launched runtime process starts from the supervisor's fixed environment ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**).`

(e) Replace the **What the scrub reaches.** paragraph, from `**What the scrub reaches.** The scrub executes in the adapter's process and reaches what that process can reach.` through `because the pod keeps the runtime process ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, with:

```
**What the scrub reaches.** Steps 0 and 2 to 6 execute in the adapter's
process, and its file operations resolve beneath their target directory without
following symbolic links. On a pod that runs the runtime supervisor
`cleanupCommands` run as
[§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.**
states, so a symbolic link the agent planted gives them no access the agent
lacks. In the sidecar deployment model
([§4.7.10](04_system-components.md#4710-deployment-model)) steps 0, 2, 4, and 6
operate on volumes mounted into both containers, so they clear and verify the
runtime container's view of those paths. On a `restart` pod ("Runtime-process
lifetime" above) the runtime end reaches every process in the runtime container other
than the supervisor, and the sweep reaches what
[§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.**
**Sweep.** lists, so no runtime process persists across the recycle boundary;
the adapter process persists. On a `keep` pod the runtime process and its memory persist across the
boundary, because
the pod keeps the runtime process
([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process
lifetime"). In the embedded model the runtime is the adapter's process, which
step 1 spares, and step 1 ends the processes the runtime started.
```

(f) In the best-effort paragraph, replace `and the kept runtime process (see "What the scrub reaches" above).` with `and, on a `keep` pod, the kept runtime process (see "What the scrub reaches" above).`

(g) In the **Deployer acknowledgment (runtime process kept across sessions).** paragraph, replace `Deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` on a pool with `recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, and a `recycle.scrubProfile` other than `vm-restart`.` with `Deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` on a reusing recycling pool ("Runtime-process lifetime" above) under `recycle.runtimeProcess: keep`.` Replace `The gateway admits a session to a pod that has served an earlier session only when the pool configuration it resolves for that claim requires this field, which is a pool with `maxConcurrentSessions` above 1 or the recycling configuration above;` with `The gateway admits a session to a pod that has served an earlier session only when the pool configuration it resolves for that claim reuses pods, which is a pool with `maxConcurrentSessions` above 1 or a reusing recycling pool;`. Replace `The recycling configurations outside the rule keep no runtime process across sessions:` with `A recycling pool that is not a reusing recycling pool reuses no pod:`. Replace `When the resolved configuration does not require this field, every acquisition refuses such a pod` with `When the resolved configuration does not reuse pods, every acquisition refuses such a pod`. Replace `An admitted update that takes a pool outside the rule ends reuse at the next acquisition.` with `An admitted update that takes a pool outside the rule ends reuse at the next acquisition of every `keep` pod ("Runtime-process lifetime" above).`. In the paragraph's first sentence, replace `The runtime process lives as long as the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime"), so a later session on a recycled pod runs in the process that served the earlier one` with `On a `keep` pod ("Runtime-process lifetime" above), a later session on a recycled pod can run in the process that served the earlier one ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime")`. Append after `the tenant pinning paragraphs above state how the gateway enforces that.`:

```
A sequential pool under `recycle.runtimeProcess: restart` does not require this field, because no runtime process serves two sessions; the state that persists there is covered by `acknowledgeBestEffortScrub`. Each pod carries the lifetime it was created under ("Runtime-process lifetime" above). A `keep` pod that has served an earlier session is admitted only while the resolved configuration requires this field: after an admitted edit of `recycle.runtimeProcess` from `keep` to `restart` on a sequential pool, every acquisition refuses such a pod and stamps its drain request as stated above. A `restart` pod is admitted after an edit of the field to `keep`.
```

(h) Outside the acknowledgment paragraph, each site that ends a reserved hold or drains a pod names a pool predicate. Replace each predicate with the acknowledgment's per-pod refusal:

- §4.6.1, the `SandboxClaim` table row: replace `when the acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule` with `when an acquisition refuses the reserved pod under the runtime-process acknowledgment`.
- §4.6.1 **Reserved hold (claim retention across same-tenant sessions):** and §6.2 **`reserved` hold semantics.**: replace `On a pool whose configuration keeps no runtime process across sessions, the acquisition path ends the hold` with `When an acquisition refuses the pod under the runtime-process acknowledgment, it ends the hold`.
- §4.6.3, the `SandboxClaim` ownership row: replace `when its acquisition path ends the hold on a pool outside the runtime-process acknowledgment rule` with `when an acquisition refuses the reserved pod under the runtime-process acknowledgment`.
- §5.2 **Pinned idle inventory.**: replace `when an acquisition drains it on a pool outside the runtime-process acknowledgment rule (see the runtime-process acknowledgment below)` with `when an acquisition refuses and drains it under the runtime-process acknowledgment below`.
- §6.2, the state diagram's `reserved ──→ idle` edge: replace the lines `(hold TTL expires, or the acquisition path ends the hold on a pool` and `that keeps no runtime process across sessions — see §5.2;` with `(hold TTL expires, or an acquisition refuses the pod under the` and `§5.2 runtime-process acknowledgment;`, keeping the column alignment.
- §29.2, the hard-wrapped step text `On a pool whose` / `configuration keeps no runtime process across sessions, the gateway ends that hold`: replace it with `When an acquisition refuses the pod under the runtime-process` / `acknowledgment, the gateway ends that hold`, keeping the citation that follows.

In §5.2:

- **Tenant pinning:**, the paragraph beginning `The pin persists across the recycle-to-idle edge`: replace `because a recycled pod keeps the runtime process that served its tenant ([Section 4.7.10](04_system-components.md#4710-deployment-model))` with `because a recycled pod keeps state the scrub does not reach (see "What the scrub reaches" below)`, and replace `on every pool whose pods keep their runtime process, including` with `on every pool, including`.
- The `recycle.allowCrossTenantReuse: true` paragraph: replace `because a recycled pod keeps the runtime process that served its pinned tenant` with `because the tenant pin applies on every pool`, and replace `requires restarting the runtime process and resetting the tenant pin` with `requires resetting the tenant pin`.
- The configurations table, `Pod reuse` row: delete ` in one runtime process`.

Each site below names the acknowledgment requirement or the kept process by a pool. Replace each phrase and keep the citation that follows it:

- §5.2, the `acknowledgeProcessLevelIsolation` YAML comment: replace `and on a recycling pool that keeps its runtime process across sessions (see the acknowledgments below)` with `and where the runtime-process acknowledgment requires it (see the acknowledgments below)`.
- §5.2, the `acknowledgeProcessLevelIsolation: true` bullet: replace `It is also required on a recycling pool that keeps its runtime process across sessions (see the runtime-process acknowledgment below).` with `It is also required where the runtime-process acknowledgment below requires it.`
- §5.2 **Client visibility of weak-isolation reuse.**: replace `on a recycling pool that keeps its runtime process across sessions the session may run` with `on a `keep` pod ("Runtime-process lifetime" above) the session may run`.
- §6.1 **One-session-per-pod default and the recycle opt-in.**: replace `` `acknowledgeProcessLevelIsolation` on a pool that keeps its runtime process across sessions`` with `` `acknowledgeProcessLevelIsolation` where the runtime-process acknowledgment requires it``.
- §6.1 **Per-session credential lease lifecycle.**: replace `outside a kept runtime process` with `outside the runtime process of a `keep` pod`.
- §7.1, the `residualStateWarning` row of the `sessionIsolationLevel` table: replace `on a recycling pool that keeps its runtime process across sessions the session may run` with `on a `keep` pod the session may run`. The row stays one physical line.
- §13.1 **`lenny-cred-readers` membership boundary.**: replace `and on recycling pools that keep a runtime process across sessions` with `and where the runtime-process acknowledgment requires it`.

In §4.6.1, replace `` `lenny.dev/egress-profile`, and `lenny.dev/tenant-id` only`` with `` `lenny.dev/egress-profile`, `lenny.dev/runtime-process`, and `lenny.dev/tenant-id` only``. In the §13.2 **Label immutability note**, replace `` `lenny.dev/egress-profile`, and `lenny.dev/dns-policy` labels are`` with `` `lenny.dev/egress-profile`, `lenny.dev/dns-policy`, and `lenny.dev/runtime-process` labels are``.

In §17.2 item 5 (`lenny-label-immutability`), replace `` `lenny.dev/delivery-mode`, and `lenny.dev/egress-profile` labels on agent pods`` with `` `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, and `lenny.dev/runtime-process` labels on agent pods``. In the §16.5 `LabelImmutabilityWebhookUnavailable` row, replace ``the immutable `lenny.dev/managed`, `lenny.dev/delivery-mode`, `lenny.dev/egress-profile`, or `lenny.dev/tenant-id` labels on agent pods`` with ``the immutable agent-pod labels ([Section 17.2](17_deployment-topology.md#172-namespace-layout) item 5)``.

### SPEC-6 · spec/13_security-model.md § 13.1 Pod Security

Insert after the **Container identity.** paragraph, which ends `and it identifies the agent only because this rule gives the agent UID to one container.`:

```
**Runtime supervisor volume.** On a pod that runs the runtime supervisor the init container `lenny-supervisor-stage` copies the runtime supervisor ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**) into an `emptyDir` volume. It runs at a UID that is neither the agent nor the adapter UID, as the rule above requires. The `runtime` container mounts the volume read-only, and no container other than the init container `lenny-supervisor-stage` mounts it read-write. The `lenny-pod-security` webhook enforces the mount rule with the identity rule above.
```

### SPEC-7 · spec/28_communication-channels.md § 28.3, § 28.5.3, § 28.6, and § 28.8, spec/04_system-components.md § 4.7.11, and spec/10_gateway-internals.md § 10.3

(a) In §28.3, the opening paragraph is hard-wrapped. Find:

```
derivation of every column without the retired prose being reproduced here. The naming table below the
```

and replace it with:

```
derivation of every column without the retired prose being reproduced here. A channel added after that
inventory carries `None` in the column. The naming table below the
```

(b) In the §28.3 **Channel register**, insert after the `CH-MCP-CONNECTOR` row, as one physical line:

```
| `CH-SUPERVISE` | None | `intra-pod` | Control | Runtime supervisor | Both | Unix socket JSON Lines | Runtime process launch, end, sweep, and liveness, and the supervisor's self-test report | None |
```

(c) In §28.5.3, replace the intro paragraph, from `This boundary carries the channels between the runtime adapter and the runtime binary inside one agent` through `The runtime is the dialling participant on every channel here.`, with:

```
This boundary carries the channels between the runtime adapter and the runtime binary inside one agent
pod, and on a pod that runs the runtime supervisor the channel between the runtime adapter and the runtime supervisor
([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**). The §28.3 channel
register places these channels on this boundary, and each carries `None` in its Link column, so each
channel's own register row together with the endpoint stated in its card describes the connection it runs
on. The runtime binary is the dialling participant on every channel here except `CH-SUPERVISE`, which the
runtime supervisor dials.
```

Replace the diagram block that follows with:

```
  agent pod
  +----------------------------------------------------------------------+
  |                                                                      |
  |  +------------------+                          +------------------+  |
  |  |                  |   CH-MSGSOCK             |                  |  |
  |  |                  |   CH-RUNTIMEOPS          |                  |  |
  |  | runtime adapter  |                  <=====  |  runtime binary  |  |
  |  |                  |   CH-MCP-PLATFORM        |                  |  |
  |  |                  |   CH-MCP-CONNECTOR       |                  |  |
  |  |                  |                          +------------------+  |
  |  |                  |                          +------------------+  |
  |  |                  |   CH-SUPERVISE   <=====  | runtime          |  |
  |  |                  |                          | supervisor       |  |
  |  +------------------+                          +------------------+  |
  |                                                                      |
  +----------------------------------------------------------------------+

  The runtime-ops, MCP, and supervisor channels run on abstract Unix sockets
  in the Linux abstract namespace. The runtime binary dials every channel
  drawn above except CH-SUPERVISE, which the runtime supervisor dials.
```

(d) Insert after the **`CH-MCP-CONNECTOR`** card and before `#### 28.5.4 Inter-replica`:

```
**`CH-SUPERVISE`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** The abstract Unix socket `@lenny-supervise` in the Linux abstract namespace, on every
  pod that runs the runtime supervisor ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**). The
  adapter listens and the runtime supervisor dials. When `SO_PEERCRED` is enforced, the adapter accepts
  only the agent UID and the supervisor accepts the connection only when its peer is the adapter UID
  ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8). No manifest-nonce
  handshake or challenge-response runs on this channel, because the supervisor connects before any
  manifest exists.
- **Axes.** Control plane, dialled by the runtime supervisor, message authority on both sides,
  Unix socket JSON Lines transport (§28.3).
- **Messages.** Each message is a single JSON object terminated by `\n` whose `type` member carries the
  message name. Adapter to supervisor: `launch`, `end`, `sweep`, `cleanup`, and `ping`. Supervisor to
  adapter: `ready`, which the supervisor sends once, as its first message on the connection, and which
  carries `properties`, the result of each property of its startup self-test
  ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8) as `pass` or `fail`;
  `launched`, `ended`, `swept`, `cleaned`, and `pong`, each answering the request of the matching
  name; and
  `exited`, which reports that a runtime process exited other than by an `end` and carries its exit
  status. Every message carries `gen`, the runtime generation the adapter assigned with its latest
  `launch`, or 0 before the first, and every request and reply
  carries `id`, which a reply echoes from its request. A reply carries an optional `error` whose value is
  one of an enumerated set of codes; it is absent when the request succeeded. `end` carries `graceMs`,
  the SIGTERM grace before SIGKILL. `cleanup` carries the pool's `cleanupCommands`, each as an argument
  list, the `cleanupTimeoutSeconds` bound in milliseconds, and the two step-0 cleanup metadata values
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). No message
  carries a credential, manifest content, a session identifier, or any byte a runtime process or a
  cleanup command wrote, and no other message carries an environment value. The exit status in `exited` is
  diagnostic and decides nothing but the failure of the bound sessions.
- **Preconditions.** The adapter accepts the connection after its `SO_PEERCRED` self-test, and reads
  `ready` before it signals READY ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4).
  It sends no request before `ready`, and sends `launch`, `end`, `sweep`, and `cleanup` only at the points
  [§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.** and the
  [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) **Lenny scrub
  procedure.** name, and `ping` periodically while no other request is outstanding. The adapter has at
  most one request outstanding.
- **Timing.** The adapter waits a bounded time for the connection and its `ready` before READY. The supervisor repeats a
  refused dial within a bounded window, and exits when the window ends with no connection. Every request is bounded by the deadline of the operation that
  sends it, and `ping` by its own deadline; a request with no reply within its bound is a link failure.
  The specification states no value for these bounds, the ping interval, or the dial retry interval or window.
- **Exclusivity.** One connection per pod. The guards are the agent pod's `restartPolicy: Never`, under
  which no container restarts in place, the adapter's closing of its listener after the
  first connection it accepts before READY, and the supervisor's exit when that connection closes
  (**Degradation.**, §28.6).
- **Degradation.** When no supervisor connects and sends `ready` within the adapter's wait, the adapter
  exits non-zero and the pod is never marked `idle`. A `ready` that reports a failed property has the
  outcome [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8 states. A
  `launched` that carries `error` fails the session's start. A
  `cleaned` that carries `error` fails the scrub as a failed `cleanupCommands` run does. A closed
  connection, a request unanswered within its bound, and a message the adapter cannot decode strictly (an
  unknown or duplicate key, an unknown code, a first message other than `ready`, a second `ready`, a
  reply whose `gen` or `id` matches no outstanding request, or an `exited` whose `gen` is not the current
  generation) are link failures. A link failure, and a
  `launched`, `ended`, or `swept` that carries `error`, take the outcome
  [§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.** **Liveness.** states. When the connection closes, the supervisor exits, which ends every process in the
  runtime container.
```

(e) In §28.6, insert after the **One operation per pod.** paragraph and before **Channels the specification states no constraint on.**:

```
**One connection per pod.** `CH-SUPERVISE` admits one connection for the pod's life, and its unit is the
pod. Its card states the guards (§28.5.3). No gateway replica holds the channel.
```

(f) In the §28.8 matrix, insert after the `CH-MCP-CONNECTOR` row, as one physical line:

```
| `CH-SUPERVISE` | When no supervisor connects and sends `ready` within the adapter's wait before READY, or `ready` reports a failed property that gates a `restart` pod, the adapter exits non-zero and the pod is never marked `idle` ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4, [§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8, §28.5.3) | A link failure, or an `error` on `launched`, `ended`, or `swept`, takes the outcome [§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.** **Liveness.** states; the supervisor exits on the close, which ends every process in the runtime container ([§4.7.10](04_system-components.md#4710-deployment-model), [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), §28.5.3) | One connection per pod, and no gateway replica holds the channel (§28.5.3, §28.6) | A pod that exits before READY, or a pod retired under "Runtime not live" ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The specification states no metric or alert for this channel (§28.5.3) |
```

The claim-register rows for this card are `WIRED` rows naming the surfaces CODE-2 and CODE-3 create, and CODE-3 lands them.

(g) In the §28.5.3 session-frame write-order table, in the row for a start whose second confirmation is refused after its `session_start` was written, delete `, because `DemoteSDK` does not end the runtime process ([§4.7.10](04_system-components.md#4710-deployment-model))`. The row stays one physical line.

(h) In `spec/04_system-components.md` §4.7.11 item 1 **Mandatory `SO_PEERCRED` startup self-test (adapter prerequisite).** step 4, replace `The pod enters `CrashLoopBackOff`, preventing session assignment.` with `The pod is never marked `idle`.` In `spec/10_gateway-internals.md` §10.3, in the **Pod (adapter) validates gateway identity on every outbound handshake** bullet, delete ` (the pod enters `CrashLoopBackOff`)`.

### SPEC-8 · The `shutdown`, signal, and exit-code statements (F-4.7.27)

(a) In §28.5.3 **Inbound: `shutdown`**, replace `No acknowledgment required — the adapter watches for process exit. If the process does not exit by the deadline, the adapter sends SIGTERM, then SIGKILL after 10 seconds.` with `No acknowledgment required. In the sidecar model the adapter learns of the process's exit from the runtime supervisor, and when the process has not exited by the deadline the adapter has the supervisor end it with SIGTERM and then SIGKILL 10 seconds later ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**).` In the `CH-MSGSOCK` card's **Timing.** bullet, which wraps across lines, find:

```
  is sent SIGTERM and then SIGKILL 10 seconds later
```

and replace it with:

```
  is, in the sidecar model, ended by the runtime supervisor with SIGTERM and then SIGKILL 10 seconds later
  ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**)
```

The citation of §15.4 on the next line stays.

(b) In the §28.5.3 **Exit Codes** table, delete `adapter logs stderr; ` from the row for code 1, and delete ` — adapter treats as crash, pod is not reused` from the row for code 137. Each row stays one physical line. Append after `non-zero triggers an alert but the session result (if any) is still delivered.`: `In the sidecar model the runtime supervisor reports the exit status to the adapter on `CH-SUPERVISE`.`

(c) The string `the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr` occurs three times in `spec/28_communication-channels.md`: in the `CH-MSGSOCK` card's **Degradation.** bullet (§28.5.3), in the **Error reporting via `response`.** paragraph (§28.5.3), and in the §28.8 `CH-MSGSOCK` row. Replace each occurrence with `the adapter synthesizes a `RUNTIME_CRASH` error from the exit status the runtime supervisor reports`, because no runtime output crosses `CH-SUPERVISE`. Whether the adapter writes `shutdown` at pod drain belongs to R16.

(d) In `spec/15_external-api-surface.md` §15.4, make these replacements:

1. In the **SDK-warm demotion contract:** paragraph, replace `the adapter sends SIGKILL` with `the process is sent SIGKILL ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **SDK-warm.**)`.
2. In §15.4.1, replace `**stderr** is captured by the adapter for logging and diagnostics but is **not** parsed as protocol messages.` with `**stderr** goes to its container's log and is **not** parsed as protocol messages.`
3. In the §15.4.6 Basic-level **shutdown within `deadline_ms`** row, replace `the adapter will SIGKILL the process in production` with `the process is sent SIGKILL in production`.

(e) In `spec/04_system-components.md` §4.4.1 **Stores:**, replace the list item `Session logs and runtime stderr` with `Session logs`.

### SPEC-9 · spec/06_warm-pod-model.md § 6.1, § 6.2, and § 6.3, and spec/05_runtime-registry-and-pool-model.md § 5.1

(a) In the §6.1 **Optional: SDK-warm mode.** paragraph, replace `The warm pool controller starts the SDK process after the pod reaches `idle` state, leaving it waiting for its first prompt.` with:

```
The adapter starts the SDK process during warm-up, before the pod reaches `idle` state, in the sidecar model through the runtime supervisor ([Section 6.2](#62-pod-state-machine); [Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **SDK-warm.**), leaving it waiting for its first prompt.
```

(b) In §5.1 **`capabilities.preConnect`**, replace `the warm pool controller pre-connects the agent SDK process during the warm phase.` with `the agent SDK process is pre-connected during the warm phase ([Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)).`

(c) In the §6.1 **`preConnect` compatibility with the execution modes.** table, in the row for `session`, `maxConcurrentSessions: 1`, `recycle.enabled: true`, replace `the adapter re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge before the claim enters `reserved` ([Section 6.2](#62-pod-state-machine))` with `the pod takes the `claimed → sdk_connecting` re-warm edge before the claim enters `reserved` ([Section 6.2](#62-pod-state-machine)), on which a sidecar pod's adapter launches nothing ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **SDK-warm.**)`. The row stays one physical line.

(d) In §6.2 **preConnect re-warm on scrub_warning.**, replace `the SDK re-initialization does not clear the warning` with `the re-warm does not clear the warning`, because a sidecar pod's re-warm initializes nothing ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **SDK-warm.**).

(e) In §6.3 **Saved by pre-warming (removed from hot path):**, replace `- Runtime adapter/binary boot` with `- Runtime adapter boot, and the runtime binary boot on a pod whose runtime process launches at warm-up ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**, **Launch.**)`. In the §6.3 **Note — `claim-to-ready` SLO vs. `pod_warmup_seconds`.**, replace `container running, runtime booted, and` with `container running, adapter booted, and`.

### SPEC-11 · Launch-ordering wording that proposal 0090 left to 0087

Proposal 0090 left the sentences that place the manifest write "before" the adapter spawns the runtime to this proposal. On a sidecar pod the adapter spawns nothing, and the supervisor launches the process after the manifest exists (SPEC-2). Each replacement below keeps the ordering and drops the spawning subject.

(a) In `spec/28_communication-channels.md`, replace each of the three occurrences of `before it spawns the runtime binary` (the `CH-RUNTIMEOPS`, `CH-MCP-PLATFORM`, and `CH-MCP-CONNECTOR` cards in §28.5.3) with `before the runtime process is launched`. In the same file (§28.5.3 adapter-local tools), replace `Adapters MUST populate this array before spawning the runtime;` with `Adapters MUST populate this array before the runtime process is launched;`, and `in `adapterLocalTools` before spawning the runtime.` with `in `adapterLocalTools` before the runtime process is launched.`

(b) In `spec/04_system-components.md` §4.7.6, in the `adapterLocalTools` row, replace `Populated by the adapter before spawning the runtime;` with `Populated by the adapter before the runtime process is launched;`. In the **Adapter manifest:** paragraph of §4.7, replace `**before the runtime binary is spawned**` with `**before the runtime process is launched**`.

(c) In `spec/04_system-components.md` §4.7.11 item 1, replace `before spawning the agent, and the agent must present` with `before the agent process is launched, and the agent must present`.

(d) In `spec/15_external-api-surface.md` §15.4.3, replace `read-only to the agent container) before spawning the runtime.` with `read-only to the agent container) before the runtime process is launched.`.

### SPEC-12 · spec/29_communication-scenarios.md § 29.1, § 29.2, § 29.4, and spec/28_communication-channels.md § 28.5.3 (per-process connection wording)

(a) In the §29.1 **Participants.** table, insert after the `runtime` row, as one physical line:

```
| `runtime supervisor` | The platform process that launches and ends the runtime process | [§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.**, §28.3 |
```

(b) In §29.2, insert after step 24 a new step 25:

```
25. On a session whose start launches the runtime process in the sidecar deployment model
    ([§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.**, **Launch.**):
    `adapter` → `runtime supervisor`, `CH-SUPERVISE`, `intra-pod`. The adapter sends `launch`, and the
    supervisor launches the runtime's `command` and answers `launched`
    ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 7, §28.5.3
    `CH-SUPERVISE`).
```

Renumber the steps after it: 25 becomes 26, 26a, 26b, and 26c become 27a, 27b, and 27c, and 27 through 34 become 28 through 35. Update the citations inside §29.2 to match: `Steps 24 through 31` and `steps 24 through 31` (in the steps that become 23, 33, and 35) become `24 through 32`, `26a, 26b, and 26c` in the step that becomes 27c becomes `27a, 27b, and 27c`, and `as step 31 states` in the step that becomes 35 becomes `as step 32 states`. The citation of §29.2 steps 15 through 20 in §29.3 is unaffected.

(c) In the steps that become 26, 27a, 27b, 27c, 28, and 29, replace `On the first session of a pod-warm pod` with `On the first session of the runtime process of a pod-warm pod`.

(d) In the step that becomes 30, replace `serves this session on the pod's `CH-MSGSOCK` connection, which the runtime dialled on the pod's first session (step 25)` with `serves this session on the `CH-MSGSOCK` connection of the live runtime process, which that process dialled on its first session (step 26)`. The text wraps across lines in the tree.

(e) In §29.4 step 12, delete `keeps its own process and the runtime process alive across the recycle boundary and ` (wrapped across lines), so the sentence reads `the adapter runs the scrub asynchronously`.

(f) In §29.4 step 14, delete the sentence `The runtime process stays alive for the pod's life`, and attach its citation parenthetical to the preceding sentence, so that sentence ends `sends the runtime process no signal ([§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row, [§4.7.10](04_system-components.md#4710-deployment-model), §28.5.3 `CH-MSGSOCK`).`

(g) In `spec/28_communication-channels.md` §28.5.3:

1. In **Inbound: `session_end`** rule 1, replace `on the pod's one connection in the sidecar model` with `on the connection of the runtime process serving it in the sidecar model`.
2. In the **Annotated Protocol Trace — Basic-Level Session** block, replace `the runtime dialled it on the pod's first session.` with `the runtime process dialled it on its first session.`

### SPEC-13 · spec/24_lenny-ctl-command-reference.md § 24.2 Preflight

In the §24.2 command table, insert after the `lenny-ctl preflight --config <values.yaml>` row, as one physical line:

```
| `lenny-ctl preflight supervisor --runtime-class <name>` | Create a one-off pod from the runtime supervisor image under the named RuntimeClass, run the supervisor's property self-test ([Section 4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8), report each property, and delete the pod. Exits non-zero when a property that gates a `restart` pod fails. | Standalone mode only, through the current `kubeconfig` context; creates a pod | Kubernetes permission to create, get, and delete pods and to get `pods/log` in the agent namespace |
```

### SPEC-14 · spec/18_build-sequence.md § 18.6 and § 18.30

(a) In §18.6 Phase 2 **Deliverables.**, insert after the `Adapter `SO_PEERCRED` startup self-test` bullet:

```
- Runtime supervisor per [§4.7.10](04_system-components.md#4710-deployment-model) **Runtime supervisor.**: the static binary in its digest-pinned image, the `lenny-supervisor-stage` init container and its volume per [§13.1](13_security-model.md#131-pod-security), and the adapter's `CH-SUPERVISE` listener per [§28.5.3](28_communication-channels.md#2853-intra-pod).
```

(b) In §18.30 Phase 12c **Deliverables.**, in the first bullet (pod recycling), replace `and `maxScrubFailures`).` with `and `maxScrubFailures`), and the runtime-process lifetime `recycle.runtimeProcess` with the supervised recycle boundary and `lenny-ctl preflight supervisor` ([§24.2](24_lenny-ctl-command-reference.md#242-preflight)).`

## Spec files touched

- `spec/04_system-components.md` (SPEC-2, SPEC-3, SPEC-4, SPEC-5, SPEC-7, SPEC-8(e), SPEC-11)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1, SPEC-5, SPEC-9)
- `spec/06_warm-pod-model.md` (SPEC-5, SPEC-9)
- `spec/07_session-lifecycle.md` (SPEC-5)
- `spec/10_gateway-internals.md` (SPEC-2, SPEC-7)
- `spec/11_policy-and-controls.md` (SPEC-2)
- `spec/13_security-model.md` (SPEC-5, SPEC-6)
- `spec/15_external-api-surface.md` (SPEC-8(d), SPEC-11)
- `spec/16_observability.md` (SPEC-5)
- `spec/17_deployment-topology.md` (SPEC-1, SPEC-5)
- `spec/18_build-sequence.md` (SPEC-14)
- `spec/24_lenny-ctl-command-reference.md` (SPEC-13)
- `spec/26_reference-runtime-catalog.md` (SPEC-1)
- `spec/28_communication-channels.md` (SPEC-7, SPEC-8, SPEC-11, SPEC-12)
- `spec/29_communication-scenarios.md` (SPEC-5, SPEC-12)
