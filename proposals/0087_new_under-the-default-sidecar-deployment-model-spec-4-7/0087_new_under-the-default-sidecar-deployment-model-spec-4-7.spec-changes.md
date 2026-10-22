# Spec changes: Sidecar runtime processes have no platform supervisor

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

Each staged edit is written against the specification at `f52fc9a49` (2026-10-22), in which proposals 0078, 0079, 0090, 0091, 0092, 0093, and 0094 have landed. Every anchor is located by heading and quoted text. None is located by line. The re-baseline of 2026-10-22 rewrote the draft's edits against the decisions in the summary. The review run converges the wording.

Each statement has one home in the specification. Every other site cites that home.

| Statement | Home |
|:--|:--|
| The `command` field and which runtimes carry it | SPEC-1, §5.1 **`command`** |
| The `env` field, its reserved prefix, and how it reaches the runtime process | SPEC-1, §5.1 **`env`** |
| What the supervisor is, when it launches and ends a runtime process, its environment and signals, liveness, crash, and pod termination | SPEC-2, §4.7.10 **Runtime supervisor.** |
| Startup order | SPEC-3, §4.7.9 steps 4 and 7 |
| Why the adapter trusts the supervisor, and what the reuse decision rests on | SPEC-4, §4.7.11 item 8 |
| `recycle.runtimeProcess`, its admission rules, the acknowledgment, the scrub order, where `cleanupCommands` run, and what the scrub reaches | SPEC-5, §5.2 |
| The supervisor volume and the stage container's identity | SPEC-6, §13.1 |
| The `CH-SUPERVISE` endpoint, messages, authentication, exclusivity, and failure behavior | SPEC-7, the §28.5.3 card |
| The `shutdown` deadline, signals, and exit codes | SPEC-8, the §28.5.3 `CH-MSGSOCK` card |
| Supervised SDK-warm | SPEC-9, §6.1 |
| `command` on the admin registration payload | SPEC-10, §15.1 |

## Edge cases and accepted failure modes

| Case | Observable outcome | Home |
|:--|:--|:--|
| The supervisor never connects | The adapter exits non-zero before READY, and the pod never becomes `idle` | SPEC-3, SPEC-7 |
| The property self-test fails on a `restart` pod | The supervisor exits before it dials, and the pod never becomes `idle` | SPEC-4 |
| `launch` fails | `launched` carries an error code, the session's start fails, and the pod is reported not live | SPEC-2, SPEC-7 |
| The runtime process exits while a session is bound | `exited` reaches the adapter, each bound session fails with `runtime_crash` into the §7.3 resume flow, and the pod retires at the boundary | SPEC-2 **Crash.** |
| A `restart` process exits after its last session and before the boundary | The boundary's `end` finds no process, and the pod does not retire | SPEC-2 |
| A heartbeat escalation on a `keep` pod | The pod is reported not live at the boundary and retires | SPEC-2 **Liveness.** |
| A process survives two `end` passes, or the sweep fails | The pod is reported not live and retires, whatever `onScrubFailure` says | SPEC-2, SPEC-5 |
| The supervisor misses a `pong` deadline, or is killed or frozen | The pod is reported not live, bound sessions fail, and the pod retires | SPEC-2, SPEC-7 |
| A reply with an unknown key, a duplicate key, or an unmatched `gen` or `id` | Link failure, and the pod retires | SPEC-7 |
| Pod termination | The supervisor forwards the kubelet's SIGTERM, and exits when `CH-SUPERVISE` closes | SPEC-2 **Pod termination.** |
| A pool sets `restart` on a runtime that is embedded, service-mode, or `preConnect` | Pool admission refuses the pool | SPEC-5 |
| A Runtime's `env` names a variable with the prefix `LENNY_`, or names one variable twice | The API server or the admin API refuses the Runtime | SPEC-1 |
| A `cleanupCommands` command exits non-zero or outlasts `cleanupTimeoutSeconds` | `cleaned` carries an error, and the scrub fails under `onScrubFailure` as a failed cleanup does today | SPEC-5, SPEC-7 |
| A pool is edited from `keep` to `restart` and no longer requires `acknowledgeProcessLevelIsolation` | Every acquisition refuses each used pod created under `keep` and requests its drain; pods created after the edit run `restart` | SPEC-5 |
| Nonce-only mode | Neither peer-UID check is available, and ordering identifies both ends | SPEC-4 |

## Staged edits

### SPEC-1 · spec/05_runtime-registry-and-pool-model.md § 5.1 Runtime

(a) In the **Standalone Runtime** YAML example, insert after the line that begins `requireSoPeercred: true # optional;`:

```yaml
command: ["/usr/local/bin/my-agent", "--serve"] # required on deploymentModel: sidecar; forbidden on embedded; see §4.7.10 Runtime supervisor
env: # optional; sidecar only; names beginning LENNY_ are refused
  - name: PYTHONUNBUFFERED
    value: "1"
```

(b) Insert after the **`requireSoPeercred`** paragraph, which ends `so a runtime registered only through the admin API runs with the default of `true`.`, and before the first `#### Derived Runtime` heading (the one directly after that paragraph, not `#### Derived Runtime Instantiation`):

```
**`command`** — the argv of the runtime process. Required on a `deploymentModel: sidecar` runtime and forbidden on a `deploymentModel: embedded` runtime. Its first element is an absolute path in the runtime image. The runtime supervisor launches this argv as the runtime process ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**); the image's `ENTRYPOINT`, `CMD`, and `ENV` are not used. The API server rejects a sidecar `Runtime` without the field or with a first element that is not absolute, and an embedded `Runtime` with it. The image's environment is replaced by `env` below. The RuntimeReconciler mirrors the field into the gateway runtime registry together with `restartable`, which is true for a sidecar runtime with `executionMode: session` that does not declare `capabilities.preConnect: true` ([Section 5.2](#52-pool-configuration-and-execution-modes), `recycle.runtimeProcess`). The admin registration payload carries `command` and requires it for `type: agent` ([Section 15.1](15_external-api-surface.md#151-rest-api)); a runtime registered through the admin API is a sidecar runtime.

**`env`** — optional variables for the runtime process, as a list of `name` and `value` pairs. Forbidden on a `deploymentModel: embedded` runtime. Each `name` is a non-empty POSIX variable name, no name appears twice, and no name begins with `LENNY_`, which the platform reserves for its own variables; the API server and the admin API reject a `Runtime` that breaks any of these rules. The runtime supervisor sets the pairs on every process it launches and on every `cleanupCommands` command it runs, after it clears its environment and before it adds the platform's variables ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**); a pair named `PATH` replaces the supervisor's fixed `PATH`. The values are readable by the runtime and are not a channel for secrets, which reach the runtime through its credential file ([Section 4.7](04_system-components.md#47-runtime-adapter)). The RuntimeReconciler mirrors the field into the gateway runtime registry, and the admin registration payload carries it ([Section 15.1](15_external-api-surface.md#151-rest-api)).
```

(c) Add `command` and `env` to the **Never overridable on derived runtime:** list, and add a `command` row and an `env` row to the table under `#### Normative Merge Algorithm` as **Inherited** with "derived may not set", matching `requireSoPeercred`, because the argv and its environment are properties of the base runtime's image.

### SPEC-2 · spec/04_system-components.md § 4.7.10 Deployment Model

(a) In the **Runtime process lifetime.** paragraph, replace its first sentence, `The runtime process lives as long as the pod.`, with:

```
On a pod whose pool keeps its runtime process (`recycle.runtimeProcess: keep`,
or a pool that does not recycle), the runtime process lives as long as the pod.
On a `restart` pod each runtime process lives from its launch to the next
occupancy-zero boundary at which the pod is reused
([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)),
and the sentences of this paragraph about one process or one `CH-MSGSOCK`
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
runtime process that stops, or whose connection the hold timeout closed, is not
re-created or reconnected inside the pod: the adapter
```

Replace with:

```
runtime process that stops while a session is bound, or whose connection the
hold timeout closed, is not launched again for that session or reconnected
inside the pod: the adapter
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
or heartbeat escalation, and, under `keep`, no occupancy-zero boundary, closes that connection
```

(b) Insert after the **Runtime process lifetime.** paragraph and before `**Health check:**`:

```
**Runtime supervisor.** On every sidecar pod the runtime container's first
process, and the init of its process namespace, is the runtime supervisor, a
platform binary from its own digest-pinned image that an init container copies
onto a read-only mount ([§13.1](13_security-model.md#131-pod-security)). The
supervisor launches the runtime's `command`
([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)) when the adapter asks
it to on `CH-SUPERVISE` ([§28.5.3](28_communication-channels.md#2853-intra-pod)),
and launches nothing otherwise. In the embedded model there is no runtime
container and no supervisor.

- **Environment.** Each launch starts from an empty environment to which the
  supervisor adds a fixed `PATH`, the runtime's `env`
  ([§5.1](05_runtime-registry-and-pool-model.md#51-runtime)), and the
  platform's runtime variables. The image's `ENTRYPOINT`, `CMD`, and `ENV` are
  not used. The supervisor reads no file from the runtime image's filesystem.
- **Launch.** The adapter asks for a launch only after the manifest the process
  reads exists and after every adapter listener the runtime dials is bound. A
  session-mode runtime that does not declare `preConnect` is launched at startup
  step 7 ([§4.7.9](#479-startup-sequence-for-type-agent-runtimes)) when no
  process is live. A `preConnect` runtime and a service-mode runtime are
  launched at pod warm-up, before READY. After an end, no launch happens before
  the whole-pod scrub has completed and verified and the end is attested, and
  never on a pod carrying a `scrub_warning`. On a concurrent pool a session that
  starts while a process is live uses that process.
- **End.** The supervisor ends a runtime process by sending SIGTERM and then
  SIGKILL to every other process in the runtime container, and attests the end
  only after two consecutive passes find no other process. On a `restart` pod
  the end runs at each occupancy-zero boundary at which the pod is reused,
  after scrub step 0 and before `cleanupCommands`, and again after
  `cleanupCommands`, with no SIGTERM grace
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
  The end is also the `DemoteSDK` teardown
  ([§6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)) and the
  deadline action of a `shutdown`
  ([§28.5.3](28_communication-channels.md#2853-intra-pod)). Between an attested
  end and the next launch the adapter closes and rebinds every runtime listener
  it has bound, so no connection the ended process opened or left pending
  reaches the next process.
- **Cleanup.** At every recycle boundary the supervisor runs the deployer's
  `cleanupCommands` at the agent UID in a worker it forks, with the workspace
  base as working directory and the launch environment plus the step-0
  cleanup metadata
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).
  The worker holds no `CH-SUPERVISE` connection and stops a command that
  outlasts `cleanupTimeoutSeconds`.
- **Generation.** Each launch starts a new runtime generation. The rule that
  counts the sessions the pod's shared runtime process has been given
  ([§28.6](28_communication-channels.md#286-exclusivity-and-concurrency-model))
  counts from the generation's launch.
- **Crash.** A runtime process that exits while a session is bound is not
  launched again for that session. Each bound session fails with
  `runtime_crash` ([§7.3](07_session-lifecycle.md#73-retry-and-resume)), and
  the pod is retired at its next boundary.
- **Liveness.** The adapter reports the runtime as able to serve the next
  session only while the supervisor answers its liveness checks, no runtime
  process has exited or been escalated as hung while a session was bound, and,
  on a `restart` pod, the last end and sweep succeeded; otherwise the pod is
  retired
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  "Runtime not live").
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
The pod's lifetime under `keep`, or one launch to the next reused occupancy-zero boundary under `restart`; launched by the runtime supervisor; one connection per process serves its sessions, each opened by `session_start` and released by `session_end` or by the end of the connection
```

(d) In §4.7.1, in the gateway RPC table's `Shutdown` row, replace `ends the session's use of the pod's runtime process, which stays alive for the pod's life` with `ends the session's use of the pod's runtime process, which stays alive until the pod terminates or, under `recycle.runtimeProcess: restart`, until the whole-pod scrub ends it`. In the same row, replace `the adapter keeps its own process and the runtime process alive across the recycle boundary, runs the §5.2 whole-pod scrub asynchronously` with `the adapter keeps its own process alive across the recycle boundary, keeps the runtime process alive under `keep`, runs the §5.2 whole-pod scrub asynchronously`. The row stays one physical line.

### SPEC-3 · spec/04_system-components.md § 4.7.9 Startup Sequence for `type: agent` Runtimes

(a) Append to step 4, `4. Adapter signals READY to gateway — pod enters warm pool`, on the same list item:

```
 In the sidecar model the adapter signals READY only after the runtime supervisor has connected on `CH-SUPERVISE`, and on a `preConnect` or service-mode runtime only after the supervisor has launched the runtime process ([§4.7.10](#4710-deployment-model), **Runtime supervisor.**).
```

(b) Replace step 7, from `7. The runtime process becomes live for the pod's first session.` through `the runtime loop in its own process for each session.`, with:

```
7. The runtime process becomes live. In the sidecar deployment model
   ([§4.7.10](#4710-deployment-model)) the adapter has the runtime supervisor
   launch the runtime's `command` here, after step 6 wrote the final manifest,
   when no runtime process is live, and accepts that process's `CH-MSGSOCK`
   connection. A session that starts while a process is live, and every
   session on a `preConnect` or service-mode runtime, whose process was launched
   at warm-up, skips this step and steps 8 and 9 and takes step 10
   ([§4.7.10](#4710-deployment-model), **Runtime supervisor.**). In the
   embedded deployment model the adapter runs the runtime loop in its own
   process for each session.
```

### SPEC-4 · spec/04_system-components.md § 4.7.11 Adapter-Agent Security Boundary

Insert after item 7, **MCP server security:**, which ends `by directly connecting to the MCP socket without manifest access.`, and before `### 4.8 Gateway Policy Engine`:

```
8. **Runtime supervisor:**
   - **Where it runs.** The runtime supervisor
     ([§4.7.10](#4710-deployment-model), **Runtime supervisor.**) runs at the
     agent UID on the untrusted side of this boundary. The adapter sends every
     request on `CH-SUPERVISE`, the supervisor sends only replies and exit
     reports, and no author-controlled byte crosses the channel, so item 2 and
     item 3 hold.
   - **Why each end trusts the other.** The adapter accepts one connection,
     from the agent UID when `SO_PEERCRED` is enforced, and the supervisor
     accepts the connection only when its peer is the adapter UID. The
     connection is made before any author code exists in the pod: the
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
   - **Property self-test.** At startup the supervisor tests that its
     non-dumpable mark resists same-UID inspection, that it identifies a
     signal's sender, and that its kill reaches a double-forked descendant. On
     a `restart` pod a failure makes the supervisor exit before it connects,
     and the pod never becomes `idle`. PID-namespace init immunity to SIGKILL
     and SIGSTOP is reported and does not gate, because its failure retires
     only the pod whose runtime exploits it. `lenny-ctl preflight` runs the
     same self-test on a named RuntimeClass, and it is re-run when the
     container runtime version changes, as item 1 requires for `SO_PEERCRED`.
```

### SPEC-5 · spec/05_runtime-registry-and-pool-model.md § 5.2 Pool Configuration and Execution Modes

(a) In the `sessionPolicy` YAML block that follows the **Slot (session mode).** paragraph (the block whose first line is `sessionPolicy:                            # session mode only`), insert after its line `    maxSessionsPerPod: 50                 # required when enabled — counts every session served; pod retired at the bound`:

```yaml
    runtimeProcess: keep                  # keep | restart — required, no default, where the acknowledgment rule below applies to recycling
```

(b) Insert after the **Recycling and integration levels.** paragraph:

```
**Runtime-process lifetime (`recycle.runtimeProcess`).** On a pool with
`recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, and a
`recycle.scrubProfile` other than `vm-restart`, `recycle.runtimeProcess` is
required and has no default; the pool controller rejects it on every other
pool. `keep` keeps the runtime process across the recycle boundary
([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime
process lifetime"). `restart` ends the runtime process at each occupancy-zero
boundary at which the pod is reused, and the next session runs in a newly
launched process ([Section 4.7.10](04_system-components.md#4710-deployment-model),
**Runtime supervisor.**). The gateway pool admission path rejects `restart` on a
pool whose runtime is not `restartable` in the runtime registry, which is an
embedded, service-mode, or `preConnect` runtime
([Section 5.1](#51-runtime)). Pod retirement at `recycle.maxSessionsPerPod` or
`recycle.maxPodUptimeSeconds` is the same under both values. A pod's lifetime
is fixed when the pod is created, from the pool's value at that moment, and an
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
adapter-executed and gateway-coordinated. Under `recycle.runtimeProcess: keep`
the runtime process is kept across the recycle boundary
([Section 4.7.10](04_system-components.md#4710-deployment-model)),
```

In the same paragraph, replace `A runtime that exits after its session makes the pod retire` with `Under `keep`, a runtime that exits after its session makes the pod retire`.

(c) In **Lenny scrub procedure**, replace `The scrub has two phases: a **pre-cleanup credential purge** (step 0) executed by the adapter before `cleanupCommands`, and a **post-cleanup scrub sequence** (steps 1-6) executed after `cleanupCommands` finish.` with:

```
The scrub runs in this order: the **pre-cleanup credential purge** (step 0); on a sidecar pod under `recycle.runtimeProcess: restart`, the **runtime end and sweep**, in which the runtime supervisor ends the runtime process and removes the agent UID's IPC objects and its entries in every agent-writable volume ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**); the deployer's `cleanupCommands`; on a sidecar pod under `restart`, a second runtime end, which ends every process the commands left; and the **post-cleanup scrub sequence** (steps 1-6). In the sidecar model the runtime supervisor runs `cleanupCommands` at the agent UID in the runtime container under both values, so they run with the runtime image's tools and with no more access than the agent has; in the embedded model the adapter runs them in its own container. A failed end or sweep makes the adapter report the runtime as not able to serve the next session, so the pod retires under "Runtime not live" below whatever `onScrubFailure` states. A failed or timed-out `cleanupCommands` run fails the scrub as before.
```

In the **Recycle lifecycle (`recycle.enabled: true`).** paragraph, replace `deployer-defined `cleanupCommands` execute in the pod's workspace base `/workspace`,` with `deployer-defined `cleanupCommands` execute in the pod's workspace base `/workspace` (in the sidecar model at the agent UID in the runtime container; see the scrub procedure below),`.

In the **Lenny scrub procedure** paragraph, replace the sentence `The adapter ends the ending session's use of the runtime, keeps its own process and the runtime process alive across the recycle boundary, runs the scrub asynchronously,` with `The adapter ends the ending session's use of the runtime, keeps its own process alive across the recycle boundary, keeps the runtime process alive under `keep`, runs the scrub asynchronously,`, and replace `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process;` with `On a `standard` or `in-place` pool whose pod is reused, the next session binds to the same runtime process under `keep` and to a newly launched one under `restart`;`.

(d) In step 1, replace `The kill runs in the container that executes the scrub and does not end the runtime process; see "What the scrub reaches" below.` with:

```
In the embedded model the kill runs in the adapter container. In the sidecar model the adapter container runs no author process, and the runtime container's processes are ended by the runtime end before `cleanupCommands` under `restart` and are kept under `keep`; see "What the scrub reaches" below.
```

In step 1b, replace `the adapter's UID owns or created` with `the agent UID owns or created, matched on the creator UID because an owner can reassign a segment's owner`, and append `In the sidecar model the runtime sweep removes them, together with the agent UID's System V message queues and semaphores and its POSIX message queues.`

In step 3, append `In the sidecar model each launched runtime process starts from the supervisor's fixed environment ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**).`

(e) Replace the **What the scrub reaches.** paragraph, from `**What the scrub reaches.** The scrub executes in the adapter's process and reaches what that process can reach.` through `because the pod keeps the runtime process ([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime").`, with:

```
**What the scrub reaches.** Steps 0 and 2 to 6 execute in the adapter's
process, and its file operations resolve beneath their target directory without
following symbolic links. In the sidecar model `cleanupCommands` run at the
agent UID, so a symbolic link the agent planted gives them no access the agent
lacks. In the sidecar deployment model
([§4.7.10](04_system-components.md#4710-deployment-model)) steps 0, 2, 4, and 6
operate on volumes mounted into both containers, so they clear and verify the
runtime container's view of those paths. Under `recycle.runtimeProcess:
restart` the runtime end and sweep reach every process in the runtime container
other than the supervisor, the agent UID's System V and POSIX IPC objects, and
its entries in the workspace base, `/sessions`, `/artifacts`, `/tmp`, and
`/dev/shm`, so no runtime process persists across the recycle boundary; the
adapter process persists. Under `keep` the runtime process, its memory, the
processes it started, and its IPC objects persist across the boundary, because
the pod keeps the runtime process
([§4.7.10](04_system-components.md#4710-deployment-model), "Runtime process
lifetime"). In the embedded model the runtime is the adapter's process, which
step 1 spares, and step 1 ends the processes the runtime started.
```

(f) In the best-effort paragraph, replace `and the kept runtime process (see "What the scrub reaches" above).` with `and, under `recycle.runtimeProcess: keep`, the kept runtime process (see "What the scrub reaches" above).`

(g) In the **Deployer acknowledgment (runtime process kept across sessions).** paragraph, replace `Deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` on a pool with `recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, and a `recycle.scrubProfile` other than `vm-restart`.` with `Deployers must set `sessionPolicy.acknowledgeProcessLevelIsolation: true` on a pool with `recycle.enabled: true`, `recycle.maxSessionsPerPod` above 1, a `recycle.scrubProfile` other than `vm-restart`, and `recycle.runtimeProcess: keep`.` Replace `The gateway admits a session to a pod that has served an earlier session only when the pool configuration it resolves for that claim requires this field, which is a pool with `maxConcurrentSessions` above 1 or the recycling configuration above;` with `The gateway admits a session to a pod that has served an earlier session only when the pool configuration it resolves for that claim reuses pods, which is a pool with `maxConcurrentSessions` above 1 or a recycling pool with `recycle.maxSessionsPerPod` above 1 and a `recycle.scrubProfile` other than `vm-restart`, under either `recycle.runtimeProcess` value;`. Replace `When the resolved configuration does not require this field, every acquisition refuses such a pod` with `When the resolved configuration does not reuse pods, every acquisition refuses such a pod`. Replace `An admitted update that takes a pool outside the rule ends reuse at the next acquisition.` with `An admitted update that takes a pool outside the rule ends reuse at the next acquisition of every pod created under `keep`.`. In the paragraph's first sentence, replace `The runtime process lives as long as the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime"), so a later session` with `Under `recycle.runtimeProcess: keep` the runtime process lives as long as the pod ([Section 4.7.10](04_system-components.md#4710-deployment-model), "Runtime process lifetime"), so a later session`. Append after `the tenant pinning paragraphs above state how the gateway enforces that.`:

```
A sequential pool under `recycle.runtimeProcess: restart` does not require this field, because no runtime process serves two sessions; the state that persists there is covered by `acknowledgeBestEffortScrub`. Each pod carries the lifetime it was created under ("Runtime-process lifetime" above). A pod created under `keep` that has served an earlier session is admitted only while the resolved configuration requires this field: after an admitted edit from `keep` to `restart` on a sequential pool, every acquisition refuses such a pod and stamps its drain request as stated above. A pod created under `restart` is admitted after an edit to `keep`, and its runtime process still ends at each reused boundary.
```

### SPEC-6 · spec/13_security-model.md § 13.1 Pod Security

Insert after the **Container identity.** paragraph, which ends `and it identifies the agent only because this rule gives the agent UID to one container.`:

```
**Runtime supervisor volume.** On a sidecar pod the init container `lenny-supervisor-stage` copies the runtime supervisor ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**) into an `emptyDir` volume. It runs at a UID that is neither the agent nor the adapter UID, as the rule above requires. The `runtime` container mounts the volume read-only, and no regular or ephemeral container mounts it read-write. The `lenny-pod-security` webhook enforces the mount rule with the identity rule above.
```

### SPEC-7 · spec/28_communication-channels.md § 28.3, § 28.5.3, § 28.6, and § 28.8

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
| `CH-SUPERVISE` | None | `intra-pod` | Control | Runtime supervisor | Pod adapter | Unix socket JSON Lines | Runtime process launch, end, sweep, and liveness | None |
```

(c) In §28.5.3, replace the intro paragraph, from `This boundary carries the channels between the runtime adapter and the runtime binary inside one agent` through `The runtime is the dialling participant on every channel here.`, with:

```
This boundary carries the channels between the runtime adapter and the runtime binary inside one agent
pod, and on a sidecar pod the channel between the runtime adapter and the runtime supervisor
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
  drawn above except CH-SUPERVISE, which the runtime supervisor dials. The
  runtime supervisor is present on every sidecar pod.
```

(d) Insert after the **`CH-MCP-CONNECTOR`** card and before `#### 28.5.4 Inter-replica`:

```
**`CH-SUPERVISE`**

- **Link.** `None` (§28.3). The channel's own register row and the endpoint below describe the
  connection.
- **Endpoint.** The abstract Unix socket `@lenny-supervise` in the Linux abstract namespace, on every
  sidecar pod ([§4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**). The
  adapter listens and the runtime supervisor dials. When `SO_PEERCRED` is enforced, the adapter accepts
  only the agent UID and the supervisor accepts the connection only when its peer is the adapter UID
  ([§4.7.11](04_system-components.md#4711-adapter-agent-security-boundary) item 8). No manifest-nonce
  handshake or challenge-response runs on this channel, because the supervisor connects before any
  manifest exists.
- **Axes.** Control plane, dialled by the runtime supervisor, message authority with the pod adapter,
  Unix socket JSON Lines transport (§28.3).
- **Messages.** Each message is a single JSON object terminated by `\n` whose `type` member carries the
  message name. Adapter to supervisor: `launch`, `end`, `sweep`, `cleanup`, and `ping`. Supervisor to
  adapter: `launched`, `ended`, `swept`, `cleaned`, and `pong`, each answering the request of the matching
  name, and
  `exited`, which reports that a runtime process exited and carries its exit status. Every message
  carries `gen`, the runtime generation the adapter assigned with `launch`, and every request and reply
  carries `id`, which a reply echoes from its request. A reply carries an optional `error` whose value is
  one of an enumerated set of codes; it is absent when the request succeeded. `end` carries `graceMs`,
  the SIGTERM grace before SIGKILL. `cleanup` carries the pool's `cleanupCommands`, each as an argument
  list, the `cleanupTimeoutSeconds` bound in milliseconds, and the two step-0 cleanup metadata values
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). No message
  carries a credential, manifest content, a session identifier, or any byte a runtime process or a
  cleanup command wrote, and no other message carries an environment value. The exit status in `exited` is
  diagnostic and decides nothing but the failure of the bound sessions.
- **Preconditions.** The adapter accepts the connection after its `SO_PEERCRED` self-test and before it
  signals READY ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4).
  It sends `launch` only at the points [§4.7.10](04_system-components.md#4710-deployment-model)
  **Runtime supervisor.** names, `end` and `sweep` at a `restart` boundary, on `DemoteSDK`, and at a
  `shutdown` deadline, `cleanup` at every recycle boundary after step 0, a second `end` after `cleaned`
  at a `restart` boundary, and `ping` periodically for the pod's life. The adapter has at most one request
  outstanding.
- **Timing.** The adapter waits a bounded time for the connection before READY. `end`, `sweep`, and
  `cleanup` are bounded by the scrub's context
  ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). A `ping`
  that receives no `pong` within its deadline is a link failure. The specification states no value for
  these bounds or for the ping interval.
- **Exclusivity.** One connection per pod. The adapter accepts the first connection before READY and
  then closes the listener, and the supervisor never dials a second time (§28.6).
- **Degradation.** When no supervisor connects within the adapter's wait, the adapter exits non-zero and
  the pod is never marked `idle`. A `launched` that carries `error` fails the session's start. A
  `cleaned` that carries `error` fails the scrub as a failed `cleanupCommands` run does. An `ended`
  or `swept` that carries `error`, a missed `pong`, a closed connection, and a message the adapter cannot
  decode strictly (an unknown or duplicate key, an unknown code, or a `gen` or `id` that matches nothing
  outstanding) each make the adapter report the runtime as not able to serve the next session, and the
  pod is retired ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes),
  "Runtime not live"). When the connection closes, the supervisor exits, which ends every process in the
  runtime container.
```

(e) In §28.6, insert after the **One operation per pod.** paragraph and before **Channels the specification states no constraint on.**:

```
**One connection per pod.** `CH-SUPERVISE` admits one connection for the pod's life: the first
connection the adapter accepts before READY. The guards are the adapter's closing of the listener after
that accept and the supervisor's exit when its connection closes (§28.5.3). The constraint bounds the
connections inside one pod; no gateway replica holds the channel.
```

(f) In the §28.8 matrix, insert after the `CH-MCP-CONNECTOR` row, as one physical line:

```
| `CH-SUPERVISE` | When no supervisor connects within the adapter's wait before READY, the adapter exits non-zero and the pod is never marked `idle` ([§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step 4, §28.5.3) | A closed connection, a missed `pong`, an undecodable message, or an `ended` or `swept` carrying an error makes the adapter report the runtime as not able to serve the next session, and the pod is retired; the supervisor exits on the close, which ends every process in the runtime container ([§4.7.10](04_system-components.md#4710-deployment-model), [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), §28.5.3) | One connection per pod; the adapter closes the listener after its one accept and the supervisor never redials, and no gateway replica holds the channel (§28.5.3, §28.6) | A pod that exits before READY, or a pod retired under "Runtime not live" ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The specification states no metric or alert for this channel (§28.5.3) |
```

The claim-register rows for this card are `WIRED` rows naming the surfaces CODE-2 and CODE-3 create, and CODE-3 lands them.

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

(b) In §28.5.3 **Exit Codes**, append after `non-zero triggers an alert but the session result (if any) is still delivered.`: `In the sidecar model the runtime supervisor reports the exit status to the adapter on `CH-SUPERVISE`.`

(c) The string `the adapter synthesizes a `RUNTIME_CRASH` error from the exit code and stderr` occurs three times in `spec/28_communication-channels.md`: in the `CH-MSGSOCK` card's **Degradation.** bullet (§28.5.3), in the **Error reporting via `response`.** paragraph (§28.5.3), and in the §28.8 `CH-MSGSOCK` row. Replace each occurrence with `the adapter synthesizes a `RUNTIME_CRASH` error from the exit status the runtime supervisor reports`, because no runtime output crosses `CH-SUPERVISE`. In the embedded model the adapter observes its own runtime loop, and the review run checks whether the sentence needs an embedded clause. §15.4 states no `RUNTIME_CRASH` construction. The review run checks the §15.4 sites F-4.7.27 names (the `Graceful drain (DRAINING state)` row of the integration-level table, which reads `Adapter sends `shutdown` with `deadline_ms`; SIGTERM on timeout.`, and the Basic-level **shutdown within `deadline_ms`** conformance row) against SPEC-8(a) and corrects any that stays untrue. Whether the adapter writes `shutdown` at pod drain belongs to R16.

### SPEC-9 · spec/06_warm-pod-model.md § 6.1 What a Pre-Warmed Pod Looks Like

Append to the **Optional: SDK-warm mode.** paragraph, after `This eliminates SDK cold-start latency from the hot path.`:

```
In the sidecar model the adapter has the runtime supervisor launch the runtime process at pod warm-up, and the SDK-warm state is that process connected on `CH-MSGSOCK`; `DemoteSDK` ends the process, and the session's start launches a new one after the final manifest exists ([Section 4.7.10](04_system-components.md#4710-deployment-model), **Runtime supervisor.**).
```

In the `preConnect` compatibility table's `recycle.enabled: true` row, replace `the adapter re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge` with `the adapter re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge, which launches a new runtime process when the session demoted the earlier one`.

### SPEC-10 · spec/15_external-api-surface.md § 15.1 REST API

In the admin runtime registration payload's field description, add `command`: required for `type: agent`, the runtime-process argv whose first element is an absolute path ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime), `command`). Add `env`: optional, the runtime-process variables as `name` and `value` pairs, with names beginning `LENNY_` refused ([Section 5.1](05_runtime-registry-and-pool-model.md#51-runtime), `env`). The review run locates the payload's description in §15.1 and the OpenAPI document.

### SPEC-11 · Launch-ordering wording that proposal 0090 left to 0087

Proposal 0090 left the sentences that place the manifest write "before" the adapter spawns the runtime to this proposal. On a sidecar pod the adapter spawns nothing, and the supervisor launches the process after the manifest exists (SPEC-2). Each replacement below keeps the ordering and drops the spawning subject.

(a) In `spec/28_communication-channels.md`, replace each of the three occurrences of `before it spawns the runtime binary` (the `CH-RUNTIMEOPS`, `CH-MCP-PLATFORM`, and `CH-MCP-CONNECTOR` cards in §28.5.3) with `before the runtime process is launched`.

(b) In `spec/04_system-components.md` §4.7.6, in the `adapterLocalTools` row, replace `Populated by the adapter before spawning the runtime;` with `Populated by the adapter before the runtime process is launched;`.

(c) In `spec/04_system-components.md` §4.7.11 item 1, replace `before spawning the agent, and the agent must present` with `before the agent process is launched, and the agent must present`.

(d) In `spec/15_external-api-surface.md` §15.4.3, replace `read-only to the agent container) before spawning the runtime.` with `read-only to the agent container) before the runtime process is launched.`.

## Spec files touched

- `spec/04_system-components.md` (SPEC-2, SPEC-3, SPEC-4, SPEC-11)
- `spec/05_runtime-registry-and-pool-model.md` (SPEC-1, SPEC-5)
- `spec/06_warm-pod-model.md` (SPEC-9)
- `spec/13_security-model.md` (SPEC-6)
- `spec/15_external-api-surface.md` (SPEC-8(c), SPEC-10, SPEC-11)
- `spec/28_communication-channels.md` (SPEC-7, SPEC-8, SPEC-11)
