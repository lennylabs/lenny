# Non-spec changes: Sidecar runtime processes have no platform supervisor

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

Every change below is written against the tree at `f52fc9a49` (2026-10-22). Code is located by symbol and file. The behavior each change implements is stated in the spec-changes file, and each code change cites the spec home it implements by heading. The re-baseline of 2026-10-22 replaced the draft's Go `supervise` subcommand with a C supervisor in its own image (D-BINARY) and widened every change to every sidecar pod (D-SCOPE). The review run completes the per-symbol detail.

### Process model

```
init container lenny-supervisor-stage (stage UID)
  /lenny-supervisor stage DEST          copies itself into the emptyDir, mode 0555

runtime container (agent UID), PID 1 = /lenny-supervisor/lenny-supervisor run --env K=V ... -- ARGV
  clearenv; PR_SET_DUMPABLE 0; install handlers for every catchable signal
  property self-test (result reported in ready; the adapter gates)
  dial @lenny-supervise until accepted or --dial-timeout ends (then exit non-zero); SO_PEERCRED peer must be the adapter UID
  send ready{gen:0,properties}; serve requests in order until EOF, then exit

adapter (adapter UID)
  SO_PEERCRED self-test
  AwaitSupervisor: listen @lenny-supervise  <====  dial until accepted
  accept once (agent-UID check), close listener
  read ready; log each failed property    <====  ready{gen:0,properties}
  restart pod and a gating property failed: exit non-zero (no READY)
  [--warm-up-launch: write manifest, bind listeners, launch, accept CH-MSGSOCK]
  srv.Serve (READY)
  ...
  step 6 final manifest; listeners bound
  launch{gen,id}                        ====>  fork; child: setsid, exec argv with the rendered env
                                        <====  launched{gen,id}
  CH-MSGSOCK accept (0090 handshake)    <====  runtime process dials
  ping{gen,id} every interval           ====>  <==== pong{gen,id}
  ...occupancy zero, restart pod...
  scrub step 0
  end{gen,id,graceMs:0}                 ====>  kill(-1,SIGTERM); kill(-1,SIGKILL); reap; /proc check x2
                                        <====  ended{gen,id}
  sweep{gen,id}                         ====>  fork worker (no CH-SUPERVISE fd): IPC by cuid/uid,
                                               POSIX mqueues, agent-owned entries; parent kill(-1) + /proc check
                                        <====  swept{gen,id}
  cleanup{gen,id,commands,timeoutMs,prev...} ====>  fork worker (no CH-SUPERVISE fd), cwd workspace base:
                                               each argv in its own session, launch env + cleanup metadata
                                        <====  cleaned{gen,id}
  end{gen,id,graceMs:0}                 ====>  second end: kill(-1) and /proc check x2
                                        <====  ended{gen,id}
  sweep{gen,id}                         ====>  second sweep, as above
                                        <====  swept{gen,id}
  close and rebind CH-MSGSOCK and CH-RUNTIMEOPS listeners
  steps 1-6 (openat2 RESOLVE_BENEATH|RESOLVE_NO_SYMLINKS)
  ReportPodScrub(runtime_live)
  ...occupancy zero, keep pod: step 0; cleanup ====> <==== cleaned; steps 1-6; ReportPodScrub
```

### Adapter state for one pod

| State | Set | Cleared |
|:--|:--|:--|
| `gen` | Incremented by each `launch` | Never |
| `live` (a process is running) | `launched` with no error | `ended` with no error, `exited` |
| `notLive` (sticky) | Each condition of §4.7.10 **Runtime supervisor.** **Liveness.** | Never |
| `launchPermitted` | Adapter start; an attested end (`ended` with no error) | `launch` |

`ServesNextSession()` returns false when `notLive` is set or, on a `keep` pod, when the composed `SocketRuntimeProcess.ServesNextSession` does. The pod's lifetime is the `--runtime-process` flag the pod builder rendered at pod creation (D-PODLIFETIME); the adapter holds no other recycle state. `podscrub.go` reads it as today.

## Staged code changes

### CODE-1 · `command`, `env`, and `restartable`

**Targets.** `pkg/apis/lenny/v1alpha1/runtime_types.go`, the regenerated `zz_generated.deepcopy.go` and CRD manifests (`charts/lenny/crds/`, `pkg/embedded/crds/`), `pkg/controller/runtime/controller.go` (`mirror`, `applyCRDFields`), `pkg/gateway/runtime/runtimestore/runtimestore.go` (`Runtime`, merge rules), a registry migration for the `restartable` column, `charts/lenny/values.yaml` (`referenceRuntimes.catalog`), `charts/lenny/templates/reference-runtimes.yaml`, and `charts/lenny/tests/reference-runtimes_test.yaml`.

**Change.**

- `RuntimeSpec.Command []string` with `MinItems=1`, `items:MinLength=1`, and CEL rules on the spec: required when `type` is `agent` and `deploymentModel` is empty or `sidecar`, forbidden otherwise, first element absolute. `// spec: §5.1 (command)`.
- `RuntimeSpec.Env []RuntimeEnvVar` (`Name`, `Value`), optional, with `listType=map` and `listMapKey=name` so a duplicate name is rejected, `Name` matching `^[A-Za-z_][A-Za-z0-9_]*$`, and CEL rules: no name begins with `LENNY_`, and the field is forbidden wherever `command` is. `// spec: §5.1 (env)`.
- `applyCRDFields` computes `Restartable` as §5.1 **`command`** defines it and copies neither `Command` nor `Env`. The registry gains the `restartable` column. `runtimestore.Merge` takes `Restartable` from the base.
- The `PreConnect` doc comments in `runtime_types.go` and `runtimestore.go` drop the warm-pool controller as the pre-connecting actor and cite §6.1; the CRD manifests are regenerated.
- Every Kind fixture sidecar Runtime CRD gains `command` (FIXTURE-1).
- Each `referenceRuntimes.catalog` entry carries `command` as §26.1 states, the template renders it as `spec.command`, and the helm-unittest asserts every rendered Runtime has it.

### CODE-2 · The C supervisor, its image, and the binary check

**Targets.** `supervisor/` (new top-level directory: `supervisor.c`, `wire.c`, `procs.c`, `sweep.c`, `selftest.c`, `syscalls.h`, `Makefile`), `Dockerfile.supervisor`, the image build and signing in CI and the release workflow, and a CI check script under `scripts/`.

**Change.**

- A freestanding C program built with `-static -nostdlib -ffreestanding` over raw syscall wrappers. No `PT_INTERP`, no dynamic section, no libc, and no NSS. The CI check fails the build when `readelf` reports an interpreter or a dynamic section, or the symbol table contains `dlopen` or an NSS entry point.
- Modes: `stage DEST` copies `/proc/self/exe` to `DEST/lenny-supervisor` with mode `0555`. `run --socket NAME --adapter-uid UID [--dial-timeout D] --env K=V ... -- ARGV...` does, in order: `clearenv`; `prctl(PR_SET_DUMPABLE, 0)`; `sigaction` for every catchable signal with `SA_SIGINFO`; the property self-test; dial, repeating a dial refused with `ECONNREFUSED` until one connects or the `--dial-timeout` window ends, when it exits non-zero (the flag's default is at least the adapter's `AwaitSupervisor` timeout); `SO_PEERCRED` check against `--adapter-uid`; `ready` with the self-test result; serve. The supervisor gates on no property; the adapter does (§4.7.11 item 8 **Property self-test.**). `selftest` runs the self-test alone, writes one result line per property to stdout, and exits non-zero when the non-dumpable, sender-identification, or kill-reach property fails; it is the mode `lenny-ctl preflight supervisor` runs, in a pod with no runtime process, so D-LOGS does not apply to it. The launch environment is the fixed `PATH`, then each `--env` pair in order, a later pair replacing an earlier one of the same name, so the Runtime's `env` (rendered first) can replace `PATH` and the platform's variables (rendered last) cannot be replaced.
- Signal handling: a handler records the signal and `siginfo`. The main loop treats `si_pid == 0 && si_code == SI_USER` as the stop request, which forwards SIGTERM with `kill(-1)`, and ignores every other signal.
- Wire: a bounded line reader and a strict JSON subset parser for the request schema. Replies are written from fixed templates with integer and enumerated fields only.
- `end`: SIGTERM, a wait of `graceMs`, SIGKILL, reap with `wait4(-1, WNOHANG)`, and a `/proc` scan for any numeric entry other than 1. Two consecutive clean passes are required, and `pids.current` is read where `/sys/fs/cgroup/pids.current` is readable. A deadline yields `ended` with an error code.
- `sweep`: a forked worker that closes the `CH-SUPERVISE` descriptor first. It removes System V segments, queues, and semaphore sets whose `cuid` or `uid` is the agent UID (`/proc/sysvipc/*`, with `*_STAT` enumeration as fallback), POSIX message queues under the mqueue mount when present, and every entry the agent UID owns in the volumes §4.7.10 **Runtime supervisor.** **Sweep.** lists, skipping the workspace base's `slots/`, with `openat` and `O_NOFOLLOW | O_DIRECTORY`, `unlinkat` without following links, and bounded depth and entry counts. The parent repeats `kill(-1)` and the `/proc` check before it replies.
- `cleanup`: a forked worker that closes the `CH-SUPERVISE` descriptor first, changes to the workspace base with `O_NOFOLLOW | O_DIRECTORY`, and runs each argv list in order: fork, `setsid`, `execve` with the launch environment plus `LENNY_PREV_CREDENTIAL_PROVIDER` and `LENNY_PREV_LEASE_ID`, and wait. At the `timeoutMs` deadline it sends SIGKILL to the running command's session and stops. A command's stdout and stderr stay on the container's streams. The parent replies `cleaned`, with `cleanup_failed` when a command exited non-zero or could not be executed and `cleanup_timeout` at the deadline. On a `restart` pod the adapter's second `end` and `sweep` remove what the commands left. The parser's argv-list decoding is bounded in list length and string length and is fuzzed with the rest of the parser.
- `exited`: sent when the reaper collects the launched process outside an `end`, carrying the exit status.
- The supervisor writes nothing to stdout or stderr.
- The self-test forks a child that tries `ptrace(PTRACE_ATTACH)` and reads `/proc/1/mem` and `/proc/1/fd` (must fail), signals the parent to check `si_pid` and `si_code`, and double-forks a grandchild that a `kill(-1)` must reach, and it tests PID-namespace init immunity to SIGKILL and SIGSTOP. `ready` carries `properties`, an object whose members are the enumerated property names and whose values are `pass` or `fail`, written from a fixed template. `// spec: §4.7.11 (Runtime supervisor, Property self-test); §28.5.3 (CH-SUPERVISE)`.
- `Dockerfile.supervisor` builds `FROM scratch` with the single binary. The image is signed and added to the cosign verified set.

**IMPLEMENTOR'S CHOICE:** the enumerated error code set, constrained to distinguish a launch exec failure, an end deadline, a `/proc` read failure, a sweep failure, a cleanup command failure, a cleanup deadline, a malformed request, and a generation mismatch.

### CODE-3 · Adapter: the `CH-SUPERVISE` client and the supervised runtime

**Targets.** `pkg/adapter/supervise.go` and `supervise_wire.go` (new), `pkg/adapter/socketruntime.go`, `pkg/adapter/runtimegeneration.go`, `pkg/adapter/podscrub.go`, `pkg/adapter/scrub/` (`Ops`, `DefaultOps`, `Run`), `pkg/adapter/sdkwarm.go`, `pkg/adapter/heartbeat.go`, `pkg/adapter/runtimeops.go`, `pkg/adapter/manifest.go`, `cmd/lenny-adapter/main.go`, `tests/claim-map.json`, `tests/registers/exceptions-deployment-boundary.yaml`, and `tests/registers/deployment-boundary-defaults.yaml`.

**Change.**

- `AwaitSupervisor(ctx, socket, auth, lifetime, timeout)` listens through `peerCheckedListener` with the agent-UID check (none in nonce-only mode), accepts one connection, closes the listener, reads `ready` within the same timeout, logs each failed property through `slog` with its enumerated name, returns an error on a `restart` pod when the non-dumpable, sender-identification, or kill-reach property failed, and starts one reader goroutine. A missing, late, or malformed `ready`, or a first message other than `ready`, is an error. `// spec: §4.7.11 (Runtime supervisor, Property self-test)`. The reader decodes strictly with a decoder that rejects the decode failures of §28.5.3 **Degradation.**, including a second `ready`, and sets `notLive` on any failure. `// spec: §28.5.3 (CH-SUPERVISE); §4.7.9 (step 4)`.
- `SupervisedRuntime` implements `RuntimeProcess`, `ServesNextSession`, `CloseListener`, `SDKWarmRuntime`, and `RuntimeGeneration() uint64`. It composes one `SocketRuntimeProcess` per launch on a `keep` pod and a `restart` pod alike (on a `keep` pod that is one per pod unless `DemoteSDK` ends the process) and sends `launch` before the composed `Start` accepts (D-TRANSPORT). A launch mutex spans the `launchPermitted` check (§4.7.10 **Launch.**), `launch`, and the composed `Start`, so concurrent first starts send one `launch`.
- `EndProcess(ctx, grace)` and `Sweep(ctx)` wrap `end` and `sweep`. After each attested end, before the next `launch`, the composed `CloseListener` runs and the `CH-RUNTIMEOPS` listener is closed and rebound (`RuntimeOps.Rebind`, new), which discards every backlogged connection (D-STALE).
- The scrub order changes on a pod that runs the supervisor: `scrub.Run` gains an optional `EndRuntime` step between step 0 and `cleanupCommands`, which calls `EndProcess` and `Sweep` on a `restart` pod and is skipped on a `keep` pod, and an optional `EndRuntimeAgain` step between `cleanupCommands` and step 1, which calls `EndProcess` and `Sweep` on a `restart` pod, and runs whether or not the cleanup step failed, as steps 1 to 6 do. `scrub.Config` gains an optional `CleanupRunner`; when it is set, `runCleanup` sends the commands through it (`SupervisedRuntime.RunCleanup`, which splits each command as the argv mode of `workspace.RunSetup` does and sends `cleanup`) instead of `workspace.RunSetup`, and a `cleaned` error is the step's error. `podscrub.go` `scrubConfig` sets `CleanupRunner` when `--supervise-socket` is set and leaves it nil otherwise (D-CLEANUP). The lifetime comes from the `--runtime-process` flag (D-PODLIFETIME); no field is added to `RecycleScrub`. `DefaultOps.KillUserProcesses` and `PurgeIPCShm` run without exec on every pod, as `kill(-1, SIGKILL)` from the adapter process and `IPC_RMID` on each System V segment whose `uid` or `cuid` is the adapter UID, because the distroless image has no `sh` or `ipcrm`. `RemoveAll`, `ClearContents`, and `PathState` resolve through `openat2` with `RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS` (D-PATHS).
- `runtimegeneration.go`: the cohort resets when `RuntimeGeneration()` changes (D-COHORT).
- Crash: on `exited` with a bound session, the adapter ends each bound session's `Attach` stream with the `runtime_crash` failure that proposal 0091's funnel maps (D-CRASH), and sets `notLive`.
- Hang: `onHeartbeatHung` sets `notLive` on a `keep` pod (D-HUNG, F-5.2.46).
- Ping: a goroutine tied to the server's context sends `ping` every `--supervise-ping-interval` while no other request is outstanding and sets `notLive` on a missed `--supervise-ping-timeout` (D-PING).
- SDK-warm, as §4.7.10 **Runtime supervisor.** **SDK-warm.** states (D-SDKWARM): `SupervisedRuntime.PreConnect` runs once, at adapter startup and only under `--warm-up-launch`; it writes the warm-up manifest, launches under the launch mutex and permit, and accepts `CH-MSGSOCK` before `srv.Serve`, and nothing calls it again, so the re-warm edge launches nothing and the pod's unchanged Ready condition completes the gateway's re-warm poll. `ConfigureWorkspace` binds the session on the live process, and with no live process it launches through the same path as a step-7 start after writing the final manifest. Every launch on a `preConnect` pod sets `sdkConnected`. `DemoteSDK` keeps 0090's order (slot guard, `session_end` decision, teardown) and replaces the in-process teardown with `EndProcess` bounded by the request's context; on a guard timeout it sends no `end`, and an `end` error is returned and sets `notLive`. `refuseUnconfirmedSDKWarmStart` and `ShutdownDemoteSDK` reach the same `EndProcess`.
- Service mode: launch at warm-up before READY under `--warm-up-launch` (D-SERVICE), and exits non-zero when `notLive` is set (§4.7.10 **Liveness.**).
- Warm-up: under `--warm-up-launch` a failed `launch`, or no `CH-MSGSOCK` connection within `--warm-up-timeout`, exits the adapter non-zero before `srv.Serve` (§4.7.10 **Launch.**).
- `cmd/lenny-adapter/main.go`: flags `--supervise-socket` (empty on every pod that runs no supervisor, and in the developer loop), `--supervise-ping-interval`, `--supervise-ping-timeout`, `--runtime-process` (`keep` or `restart`, default `keep`), which selects the pod's lifetime (D-PODLIFETIME), and `--warm-up-launch` with `--warm-up-timeout`, which select and bound the warm-up launch (D-SDKWARM, D-SERVICE). `AwaitSupervisor` runs after `PeercredSelftest` and before `srv.Serve`, and its failure exits non-zero.
- `tests/claim-map.json`: `WIRED` rows for the launch and end, the single accept, the ping, the sweep, and the supervised cleanup, each naming its surface. `tests/registers/exceptions-deployment-boundary.yaml`: a `deferred` row with blocker CODE-4 for each new adapter flag CODE-4 renders (`--supervise-socket`, `--supervise-ping-interval`, `--supervise-ping-timeout`, `--runtime-process`, and `--warm-up-launch`), because CODE-4 lands in a later checklist step and the deployment-boundary gate fails a read flag that no rendered pod sets. `tests/registers/deployment-boundary-defaults.yaml`: a `no-spec-value` row for `lenny-adapter --warm-up-timeout`, which no pod renders and whose compiled default is the deployed value.

### CODE-4 · Pod builder, controller flag, and chart

**Targets.** `pkg/controller/sandbox/podspec/` (new `supervisor.go`, and `podspec.go` `Inputs`, `sidecar.go` `buildSidecar`, `resources.go` `applyResources`, `volumes.go`), `pkg/controller/sandbox/controller.go` (`createPod`), `cmd/lenny-controller/flags.go`, `charts/lenny/values.yaml` and the controller templates, `pkg/podsecurity/podsecurity.go`, `pkg/admission/label_immutability/label_immutability.go` with the doc comment of `pkg/admission/webhook/label_immutability.go`, and `tests/registers/exceptions-deployment-boundary.yaml`.

This change serializes with R17 under rule S-4.

**Change.**

- `Inputs` gains `RuntimeCommand []string`, `ExecutionMode string`, `RuntimeProcess string`, and `SupervisorImage string`. `createPod` sets them from the Runtime CRD, the SandboxTemplate's mirrored `runtimeProcess`, and the controller flag `--supervisor-image` (chart value `controller.supervisorImage`).
- `Build` applies `applySupervisor` when `RuntimeCommand` is set, and returns an error for a `type: agent` sidecar runtime with no `RuntimeCommand`, or for a set `RuntimeCommand` with an empty `SupervisorImage`.
- `Inputs` also gains `RuntimeEnv`, set from the Runtime CRD's `env`, and `WarmUpLaunch`, which `createPod` sets true for a service-mode runtime and for a `preConnect` runtime whose `resolveSDKWarm` result is active (`sdkWarmActive`, the pool's `spec.sdkWarmDisabled` false at creation) (§4.7.10 **Runtime supervisor.** **SDK-warm.**).
- `applySupervisor` adds an `emptyDir` volume, the init container `lenny-supervisor-stage` (image `SupervisorImage`, a dedicated stage UID that is neither reserved UID, read-write mount), the runtime container's `Command` (`/lenny-supervisor/lenny-supervisor run --socket @lenny-supervise --adapter-uid <adapter UID> --env <each RuntimeEnv pair> --env <each platform variable> -- <command>`) with a read-only mount, and the adapter's `--supervise-socket`, ping, `--runtime-process`, and, when `WarmUpLaunch` is set, `--warm-up-launch` flags. The runtime container's `Env` list is dropped, because the supervisor clears it; `LENNY_ADAPTER_SOCKET` moves into the platform `--env` pairs. `applyResources` stamps the init container.
- `tests/registers/exceptions-deployment-boundary.yaml`: the rows CODE-3 added for the flags this change renders are deleted.
- The pod carries the label `lenny.dev/runtime-process` with the rendered lifetime: `restart` when `RuntimeProcess` is `restart`, otherwise `keep`, on every agent pod including embedded ones (D-PODLIFETIME). `DecideImmutableLabels` adds `lenny.dev/runtime-process` to its strictly immutable set, so an UPDATE that changes, adds, or removes it is refused, and a CREATE carries any value. `// spec: §17.2 (item 5); §5.2 (Runtime-process lifetime)`.
- `pkg/podsecurity/podsecurity.go` refuses a read-write mount of the supervisor volume on any init, regular, or ephemeral container other than `lenny-supervisor-stage` (§13.1 **Runtime supervisor volume.**).

### CODE-5 · Pool field, admission, and the split predicates

**Targets.** `pkg/gateway/runtime/runtimestore/runtimestore.go` (`RecyclePolicy.RuntimeProcess`), `pkg/gateway/runtime/poolstore/poolstore.go` (`validateRecyclePolicy`, `KeepsRuntimeAcrossSessions`, `recycleKeepsRuntime`), `pkg/gateway/podlifecycle/podclaim/` (`tenantpin.go` `AdmitTenantPin`, `claimer.go`, `slotclaimer.go`), `pkg/gateway/podlifecycle/podsession/` (`resolve.go`, `resume.go`, `bindrequest.go`, `bindclaim.go`, `claimconnect.go`), `pkg/gateway/sessionserver/` (`pool_policy_mirror.go`, `bind_request.go`, `resume_rebind.go`), the SandboxTemplate CRD and the PoolScalingController mirror, the OpenAPI pool schema, and the pool storage migration.

**Change.**

- `recycle.runtimeProcess` (`keep` or `restart`): required with no default on a pool where `recycleKeepsRuntime` holds today, rejected elsewhere, and `restart` rejected when the resolved runtime's (`runtimestore.Resolve`) `Restartable` is false. `// spec: §5.2 (Runtime-process lifetime)`.
- `KeepsRuntimeAcrossSessions` splits into `RequiresProcessAcknowledgment` (concurrent, or recycling under `keep`) and `ReusesPods` (concurrent, or a reusing recycling pool, §5.2 **Runtime-process lifetime**). `validateRecyclePolicy` reads the first. `AdmitTenantPin`, the reserved-hold rebind, and the `KeepsRuntime` fields read the second, renamed `ReusesPods`. The error string drops "the pod keeps one runtime process across the sessions it serves" for a pool under `recycle.runtimeProcess: restart`.
- No proto change (D-PODLIFETIME). The claim request carries `RequiresProcessAcknowledgment` beside `ReusesPods`, and one helper in `tenantpin.go` refuses a pod whose `lenny.dev/runtime-process` label is not `restart` when `RequiresProcessAcknowledgment` is false. `AdmitTenantPin` applies it to a pinned pod, so the existing `DrainRefusedPod` path drains it. `Claimer.rebindReserved` reads the candidate pod and applies it before `WriteRebindStatus`: a refused pod's hold ends with the precondition-guarded `DELETE` hold expiry uses rather than being rebound, and a pod read error other than NotFound ends the acquisition as a failed pin read does. It reads the label as §5.2 **Runtime-process lifetime** states. `// spec: §5.2 (Deployer acknowledgment (runtime process kept across sessions))`.

### CODE-6 · `lenny-ctl preflight supervisor`

**Targets.** `cmd/lenny-ctl` and `pkg/ctlcli`.

**Change.** `lenny-ctl preflight supervisor --runtime-class NAME` creates a one-off pod with the supervisor image under the named RuntimeClass, runs the supervisor's `selftest` mode (CODE-2), reads its result lines from the pod log, reports each property, and deletes the pod. It refuses to report success when the non-dumpable, sender-identification, or kill-reach property fails.

## Staged schema, chart, and migration changes

- The Runtime CRD (`command` and `env`) and the SandboxTemplate CRD (`runtimeProcess`), regenerated.
- The runtime registry column `restartable`, and the pool storage for `runtimeProcess`.
- `openapi.json`: the pool `sessionPolicy.recycle.runtimeProcess`.
- No change to `schemas/lenny-adapter.proto`.
- `charts/lenny/values.yaml`: `controller.supervisorImage`, and the image in the cosign policy.

## Staged docs changes

### DOC-1 · Runtime-author, operator, and security documentation

Every new sentence uses "launch" and names the supervisor or the platform as its subject. No sentence contains a phrase that `tests/tier11_docs/runtime_process_lifetime_doc_reconciliation_test.go` bans. The pages carry no spec section numbers and no counts. The review run lists each site.

- `docs/runtime-author-guide/lifecycle.md` is the home for the runtime supervisor: the platform launches `command` after the manifest exists with a fixed environment and the Runtime's `env`, the image `ENTRYPOINT`, `CMD`, and `ENV` are unused, the process receives SIGTERM and then SIGKILL at an end, a crash fails the bound sessions and retires the pod, and a runtime that kills or freezes the supervisor retires its own pod.
- `docs/runtime-author-guide/publishing.md` and `runtime-configuration.md`: the required `command` row and the optional `env` row, with the reserved `LENNY_` prefix.
- The pool configuration reference: `cleanupCommands` run in the runtime image at the agent UID on sidecar pods of a `type: agent` runtime, and in the adapter container on every other pod.
- `docs/reference/execution-modes.md` and the pool configuration reference: `recycle.runtimeProcess`, the acknowledgment rule, and the decision guide.
- `docs/reference/adapter-contract.md`: the supervisor in the sidecar architecture and the `shutdown` and exit-code statements.
- `docs/getting-started/architecture.md`: the stored-records entry **Session logs and runtime stderr:** becomes **Session logs:**.
- `docs/operator-guide/security.md`: the supervisor's trust basis, the property self-test, `lenny-ctl preflight supervisor`, the supervisor image in the cosign policy, and a note that a pod PID limit and a node-level process limit bound a fork bomb on runc without user namespaces (security review finding 12).

## Testing

Tier selection follows `.claude/rules/test-coverage.md`. Every test carries a `// spec:` annotation, and every test at tier 2 or above carries a `// diagnosis:` comment. No test outside a container calls `kill(-1)`. Tests follow the owner's expansive-test instruction for terminal paths: table-driven over every state, randomized interleavings under `-race`, a fault injected at each step, and invariant checks for leaked processes and connections.

### FIXTURE-1 · Probe runtime and supervised Kind pools

- `cmd/runtimes/supervise-probe`: a test-only Standard-level runtime that records a per-process token (PID and start time), the manifest values it read at start, and its environment, and on request double-forks a daemon, creates System V and POSIX IPC objects, plants a symlink under the workspace base, and makes a platform MCP call. It carries subcommands the tier-6 and tier-9 tests exec: a ptrace attach to PID 1, reads of `/proc/1/mem` and `/proc/1/fd`, a write under the supervisor mount, a SIGKILL and a SIGSTOP to PID 1, and a bind of `@lenny-supervise`.
- `tests/testinfra/kind/agent-workload.yaml` and `install.sh`: `command` on every sidecar fixture Runtime CRD, the supervisor image in the build and load list, a supervised pool with `recycle.runtimeProcess: restart` and one with `keep` on the probe runtime, and a sidecar `preConnect` probe pool.

### TEST-1 · Tests across the reached tiers

- **Tier 0.** The supervisor binary check; the `tests/tier0_static` matrix, identifier, naming-lint, and claim-register gates with the new rows; the deployment-boundary render gate with every new adapter flag rendered.
- **Tier 1.** CRD validation and `mirror` (`command`, `env` with a `LENNY_` name, a duplicate name, an invalid name, and `env` on an embedded runtime, `restartable`); `Merge` taking `Restartable` from the base, and pool admission of `restart` on a derived runtime of a restartable and of a non-restartable base; `validateRecyclePolicy` and the split predicates over every pool configuration; the strict decoder (unknown key, duplicate key, unknown code, unmatched `gen` and `id`, a first message other than `ready`, a second `ready`, an unknown property name or value in `ready`, and an `exited` of the current and of an earlier generation); `AwaitSupervisor` over every property result on a `keep` pod and a `restart` pod (each gating failure exits on a `restart` pod and is logged on a `keep` pod, an init-immunity failure gates neither, and a missing `ready` exits both); `SupervisedRuntime` with a fake peer (one `launch` under concurrent starts, end and sweep failures, ping timeout, crash with bound sessions, an idle exit on a `keep` pod and on a `restart` pod, a start after a crash on a concurrent pool with no launch permitted, a service-mode exit exiting the adapter, rebind discarding a backlogged connection, `cleaned` with each error code, and no reply to `cleanup`; a warm-up launch under `--warm-up-launch` and none without it, a failed warm-up launch and a warm-up connect timeout each exiting the adapter, `ConfigureWorkspace` with no live process launching once, `DemoteSDK` on a slot-guard timeout sending no `end`, an `end` error from `DemoteSDK` setting `notLive`, and no launch on the re-warm edge); the scrub order on a `keep` pod and on a `restart` pod, including the second `end` and `sweep` after `cleanup` on a `restart` pod, whether or not `cleanup` failed and no supervised cleanup in the embedded model; `AdmitTenantPin` and `rebindReserved` over the label values (`keep`, `restart`, absent) against both values of `RequiresProcessAcknowledgment`; the pod builder's `env` and platform `--env` rendering order, the lifetime label, `DecideImmutableLabels` refusing a change, an add, and a removal of `lenny.dev/runtime-process` on UPDATE, and `--warm-up-launch` for service mode and for `preConnect` with the breaker closed and open; `openat2` refusing a symlink; the pod builder; the pod-security mount rule; and the C supervisor's parser, self-test, sweep walker (including its skip of `slots/`), and signal filter under a C unit harness, with the parser and walker fuzzed.
- **Tier 2.** envtest: a sidecar Runtime without `command` is rejected; a Runtime whose `env` names `LENNY_ADAPTER_SOCKET` is rejected; a supervised pod renders with the init container and passes the pod-security and cosign webhooks.
- **Tier 3.** The real supervisor binary against the real adapter client over a socket, asserting the exact message members, `ready` as the first message, and the reply-per-request order; a supervisor started before the adapter binds `@lenny-supervise` connects once the adapter binds.
- **Tier 4.** The recycle path on a `restart` pod: an attested end reports `runtime_live: true`; a failed end or sweep retires the pod under `warn`; a `cleaned` error fails the scrub under `onScrubFailure`; after an edit from `keep` to `restart` that drops `acknowledgeProcessLevelIsolation`, an acquisition refuses and drains a used `keep` pod and admits a `restart` pod created after the edit.
- **Tier 5.** On the supervised pools: a `cleanupCommands` command runs at the agent UID with the Runtime's `env` and the workspace base as working directory, and a symlink the probe planted toward `/run/lenny` gives it no access the agent lacks; a background process a cleanup command started on a `restart` pod is gone before the next session; two sequential sessions on a `restart` pod report different process tokens; a `keep` pod reports one; the first session's process read the final manifest (F-4.7.26); a daemon, IPC objects, and a planted symlink from session 1 are absent in session 2; a sidecar `preConnect` pod serves a session and a demoted session (F-6.1.28), and one created while the pool's SDK-warm circuit breaker is open serves its first session through a step-7 launch; the pod reports Ready only after the supervisor connects; a process a setup command detached from its process group is gone from the adapter container before the next session on a supervised `keep` pod and a `restart` pod.
- **Tier 6.** On gVisor and Kata nodes, with a skip-register entry where none exist: the self-test properties, a fork bomb, `--oci-seccomp`, and gVisor's ptrace and KVM platforms (security review finding 9).
- **Tier 7a.** Races of `launch` against `launch`, `end` against `ping`, `exited` against `end`, and `CloseListener` against an outstanding request, under `-race` with a goroutine-leak check; the launch-after-scrub ordering (finding 11).
- **Tier 8.** SIGKILL and SIGSTOP to the supervisor from the probe retire the pod; killing the adapter makes the supervisor exit with no runtime process left.
- **Tier 9.** From the agent UID: ptrace, `/proc/1` reads, and a write to the supervisor mount fail; a squatted `@lenny-supervise` never receives a connection; a container injected at the agent UID is refused by the webhook; a pod patch that relabels a used `keep` pod's `lenny.dev/runtime-process` is refused by `lenny-label-immutability`; an init container, including one with `restartPolicy: Always`, that mounts the supervisor volume read-write is refused by the webhook; a symlink planted toward `/run/lenny` is not followed by the scrub.
- **Tier 10.** The adapter conformance battery's recycle property runs against a supervised `keep` pod and a supervised `restart` pod.
- **Tier 11.** A one-home check for `**Runtime supervisor.**` in `spec/04`, and the documentation sites above.

## Files touched on application (non-spec)

The review run completes this list from the deliverables above.
