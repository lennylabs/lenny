# Non-spec changes: Sidecar pods have no platform-controlled runtime restart

These changes are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (implementation-facing)

Every change below is written against the tree as it stands once 0078, 0079, and 0084 have landed. Code is located by symbol and file. The behavior each change implements is stated in the spec-changes file, and each code change cites the spec home it implements.

### Process model

On a supervised pod, the runtime container's first process is the staged adapter binary, run as `lenny supervise run`. It does the following, in order:

1. Marks itself non-dumpable.
2. Starts its reaper.
3. Dials `@lenny-supervise` until the adapter accepts the connection.
4. Serves requests on that connection until it closes.

The adapter's own process does the following:

1. Accepts the supervisor's connection after the `SO_PEERCRED` self-test and before `srv.Serve`.
2. Closes the listener.
3. From then on, drives the supervisor from two call sites: the first `Start` after each scrub, and scrub step 1.

```
adapter (adapter UID)                              supervisor (agent UID, PID 1 of runtime container)
  SO_PEERCRED self-test
  AwaitSupervisor: listen @lenny-supervise  <====  dial with backoff
  accept once, close listener
  srv.Serve (READY)
  ...
  StartSession, step 6 final manifest
  Start: launch mutex held
    NewSocketRuntimeProcess(addr), bind
    launch                                  ====>  LookPath, ForkExec argv
                                            <====  launched{}
    composed Start accepts CH-MSGSOCK       <====  runtime process dials
  ...occupancy cycle...
  scrub step 1: inner KillUserProcesses
    end                                     ====>  kill(-1, SIGKILL), wait for reaper, /proc check, repeat until clean or deadline
                                                   remove agent-UID shm
                                            <====  ended{}
    composed CloseListener (discards backlog)
  ReportPodScrub(runtime_live = ServesNextSession())
```

### `SupervisedRuntimeProcess` state

All fields below are guarded by `launchMu`, except `link`, which the reader goroutine owns. That goroutine sets `failed` and `linkClosed` through the same mutex.

| Field | Set | Cleared |
|:--|:--|:--|
| `gen` (`*SocketRuntimeProcess`) | `Start` when `gen` is nil, after a successful bind | `EndProcess` after an `ended` with no `error`, after `gen.CloseListener()` returns |
| `epoch` (`uint64`) | Incremented in `Start` each time `gen` is created | Never |
| `attested` | `EndProcess` after an `ended` with no `error` | `Start` when it creates `gen` |
| `failed` | Any of the following: a bind failure, a `launched{error}`, a composed `Start` error, an `ended{error}`, an `EndProcess` context expiry, a request on a closed link, a reply that answers no outstanding request, and link EOF | Never |
| `linkClosed` | Link EOF, or `CloseListener` | Never |

`ServesNextSession()` returns `!failed && !linkClosed && gen == nil && attested`. SPEC-2 **Liveness.** is the rule, and this is its code form.

### Supervisor state

The supervisor holds one flag, `live`. `launch` sets it on success, and `end` clears it whatever the outcome. The supervisor tracks no PIDs. The reaper goroutine is the only caller of `wait4`. It reaps with `WNOHANG` in a loop on each SIGCHLD and signals a condition variable that `end` waits on.

## Staged code changes

### CODE-1 · Runtime CRD field and registration refusals

**Targets.**

- `pkg/apis/lenny/v1alpha1/runtime_types.go` (`RuntimeSpec.Command`)
- `pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go` (regenerated)
- `charts/lenny/crds/lenny.dev_runtimes.yaml` and `pkg/embedded/crds/lenny.dev_runtimes.yaml` (regenerated)
- `pkg/controller/runtime/controller.go` (`mirror`, and the `applyCRDFields` doc comment)

**Change.**

- Add `Command []string `json:"command,omitempty"`` to `RuntimeSpec`, with these markers:
  - `+optional`
  - `+kubebuilder:validation:MinItems=1`
  - `+kubebuilder:validation:items:MinLength=1`
  - `+kubebuilder:validation:XValidation:rule="size(self) == 0 || self[0].startsWith('/')",message="command[0] must be an absolute path"`

  The doc comment states three things: the field is the runtime-process argv and selects the supervised lifetime; it is valid only on a sidecar session-mode runtime without `preConnect`; and it is CRD-only and not mirrored. `// spec: §5.1 (command); §4.7.10 (Supervised runtime process)`.
- Regenerate the deepcopy file and both CRD manifests. Do not hand-edit them.
- In `mirror`, beside the nonce-only refusal and before `r.Store.Get`, return a `permanentError` when `len(rt.Spec.Command) > 0` and one of these holds:
  - `rt.Spec.DeploymentModel` is non-empty and not `deploymentModelSidecar`, which returns `errCommandEmbedded`;
  - `rt.Spec.ExecutionMode == "service"`, which returns `errCommandService`;
  - `rt.Spec.Capabilities.PreConnect` is true, which returns `errCommandPreConnect`.

  Each message names the field and the condition, for example `command is only valid on deploymentModel: sidecar runtimes`. The existing `Reconcile` path turns each error into `Registered=False` with reason `InvalidRuntime`.
- In the `applyCRDFields` doc comment, name `Command` beside `DeploymentModel` as a CRD-only field with no registry counterpart. `applyCRDFields` copies nothing new.

### CODE-2 · The supervisor package and the `supervise` subcommand

**Targets.**

- `pkg/supervise/wire.go`
- `pkg/supervise/stage.go`
- `pkg/supervise/supervisor.go`
- `pkg/supervise/procs_linux.go` and `pkg/supervise/procs_other.go`
- `pkg/supervise/main.go`
- `cmd/lenny-adapter/main.go`

**Change.**

- `wire.go`, which both ends import:
  - `type Request struct{ Type string `json:"type"` }` and `type Reply struct{ Type string `json:"type"`; Error string `json:"error,omitempty"` }`.
  - The constants `TypeLaunch = "launch"`, `TypeLaunched = "launched"`, `TypeEnd = "end"`, and `TypeEnded = "ended"`.
  - `WriteRequest`, `WriteReply`, `ReadRequest`, and `ReadReply`. Each reads or writes one `\n`-terminated JSON object. A read returns an error for an unknown `type` and for a request type in a reply position, or a reply type in a request position.

  `// spec: §28.5.3 (CH-SUPERVISE)`.
- `stage.go`: `Stage(dest string) error` copies `os.Executable()` to `dest/lenny` with mode `0555`. A failure returns a wrapped error.
- `supervisor.go`: `Supervisor` takes a consumer interface `procOps`, and tier-1 and tier-3 tests substitute a fake for it. The interface's methods are:
  - `SetNonDumpable() error`
  - `Launch(argv, env []string, dir string) error`
  - `KillAll(sig syscall.Signal) error`, which calls `kill(-1, sig)`
  - `OtherProcesses() (bool, error)`, which reports whether `/proc` holds a numeric entry other than `1`
  - `RemoveAgentShm(uid int) error`
  - `Reap() (reaped bool, err error)`, which makes one non-blocking `wait4(-1, WNOHANG)` pass

  `Run(ctx, conn)` serves requests in sequence on one connection:
  - **`launch` while `live` is set:** reply `launched{error}`.
  - **`launch` otherwise:**
    1. Resolve `argv[0]` with `exec.LookPath`. On failure, reply `launched{error}`.
    2. Call `Launch` with the supervisor's inherited environment, working directory, and stdio.
    3. On success, set `live` and reply `launched{}`.
  - **`end`:**
    1. Loop: `KillAll(SIGKILL)`, wait on the reaper's condition variable or a 50 ms tick, then `OtherProcesses`. The loop ends when no other process remains or the end deadline passes.
    2. Remove the agent-UID shared memory with `RemoveAgentShm(os.Getuid())`.
    3. Clear `live`.
    4. Reply `ended{}`, or reply `ended{error}` naming the deadline, the `/proc` read failure, or the shm failure.
  - The end deadline is the `--end-timeout` flag, default 10 seconds. It must stay below the adapter's scrub context, which `cleanupTimeoutSeconds` bounds at 30 seconds by default.
  - The reaper goroutine is started before the dial and exits with `ctx`. `procOps` exposes no blocking reap.
- `procs_linux.go`:
  - `SetNonDumpable` calls `unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)` (`golang.org/x/sys`, already in `go.mod`).
  - `Launch` uses `syscall.ForkExec` with `Files` set to the supervisor's stdin, stdout, and stderr.
  - `RemoveAgentShm`:
    1. Parse `/proc/sysvipc/shm`.
    2. Remove each segment whose `uid` **or** `cuid` equals `uid` with `shmctl(IPC_RMID)`.
    3. When the file is absent, enumerate with `shmctl(SHM_INFO)` and `SHM_STAT` and apply the same match.
- `procs_other.go`: every method returns an unsupported-platform error.
- `main.go`: `Main(args []string) int` dispatches two subcommands.
  - `stage --dest DIR`
  - `run --socket NAME [--end-timeout D] -- ARGV...`. It does the following, in order:
    1. Calls `SetNonDumpable`. On failure it exits non-zero.
    2. Installs a SIGTERM handler that calls `KillAll(SIGTERM)` and keeps serving.
    3. Dials `NAME` with backoff, capped at 500 ms between attempts, until it connects or receives SIGTERM.
    4. Calls `Run`.
    5. Returns when the connection reaches EOF.

  **IMPLEMENTOR'S CHOICE:** the backoff schedule below the cap. Constraint: the first connection lands within the adapter's 30-second accept wait whenever the adapter is listening.
- `cmd/lenny-adapter/main.go`: when `os.Args[1] == "supervise"`, call `os.Exit(supervise.Main(os.Args[2:]))`. Dispatch it before `logging.Setup(os.Stderr, "adapter")`, and have `supervise.Main` call `logging.Setup(os.Stderr, "supervisor")`. This keeps the runtime container's logs from carrying `component=adapter`. `// spec: §4.7.10 (Supervised runtime process)`.

### CODE-3 · Adapter supervised transport, READY gate, scrub decorator, and cohort reset

**Targets.**

- `pkg/adapter/supervise.go` (new)
- `pkg/adapter/runtimegeneration.go` (`noteRuntimeStartedLocked`)
- `pkg/adapter/server.go` (the `runtimeEpoch` field)
- `cmd/lenny-adapter/main.go`
- `tests/claim-map.json`
- The R6 G3a flag register, if R6 has landed

**Change.**

- `AwaitSupervisor(ctx context.Context, socket string, peer func(net.Conn) error, timeout time.Duration) (*supervisorLink, error)`:
  1. Listens on `socket` through `peerCheckedListener`. The check is the agent-UID check when `SO_PEERCRED` is enforced, and nil in nonce-only mode.
  2. Accepts one connection within `timeout`.
  3. Closes the listener whether or not the accept succeeded.
  4. Starts the link's single reader goroutine.

  The reader delivers each `launched` or `ended` to the one outstanding request through a channel. It sets `failed` on a reply with no outstanding request, and it sets `linkClosed` on EOF. It exits on EOF or on `ctx` cancellation. `// spec: §4.7.9 (step 4); §28.5.3 (CH-SUPERVISE); §28.6 (One connection per pod)`.
- `SupervisedRuntimeProcess` implements `RuntimeProcess`, the 0079 `ServesNextSession`, the 0079 `CloseListener() error`, and `RuntimeEpoch() uint64`. It follows the Design section's state table. It holds the `CH-MSGSOCK` socket address and `AcceptTimeout`, and does not hold a listener.
  - **`Start(ctx, sessionID)`.** `launchMu` is held from the `gen == nil` check through `launched` and the composed `gen.Start`.
    - A concurrent `Start` waits on the mutex and then joins `gen`, so a second `launch` is never sent.
    - When `gen` is nil, the sequence is:
      1. `NewSocketRuntimeProcess(addr)`. A bind error sets `failed`.
      2. Set `AcceptTimeout`.
      3. Send `launch`.
      4. Increment `epoch`.
      5. Call `gen.Start`.
    - A `launched{error}` or a `gen.Start` error sets `failed`. It is followed by one best-effort `end` and a `gen.CloseListener()`, and `Start` then returns the error.
    - When `failed` is set, `Start` returns an error at once.
  - **`Output` and `WriteEnvelope`** delegate to `gen`, and return an error when `gen` is nil.
  - **`Close` and `Interrupt`** delegate to `gen` when it is non-nil, which gives the 0079 behavior.
  - **`EndProcess(ctx)`** sends `end` whether or not `gen` is set.
    - On `ended{}`, it calls `gen.CloseListener()`, which discards every backlogged `CH-MSGSOCK` connection and frees the abstract name. It then sets `gen` to nil and `attested` to true.
    - On `ended{error}`, on expiry of `ctx`, or on a closed link, it sets `failed`.
  - **`CloseListener()`** is the 0079 pod-scope teardown. It calls `gen.CloseListener()` when `gen` is set and closes the link, and closing the link makes the supervisor exit. It is idempotent.

  `// spec: §4.7.10 (Supervised runtime process); §5.2 (What the scrub reaches)`.
- `superviseScrubOps` embeds `scrub.Ops` and holds a consumer interface `processEnder{ EndProcess(context.Context) error }`. `KillUserProcesses(ctx)` runs the inner call first, then `EndProcess(ctx)`, and returns `errors.Join` of both errors. Every other method is the embedded one. No scrub interface changes. `// spec: §5.2 (scrub steps 1 and 1b)`.
- `CH-RUNTIMEOPS` needs no change. The adapter writes `lifecycle_capabilities` before it reads, so the handshake send fails with EPIPE on a backlogged connection whose peer `end` killed, and `RuntimeOps.Run` resets and accepts the next connection. A connection the ended process held reaches EOF and resets the same way. TEST-1(b) pins this behavior.
- `runtimegeneration.go`: declare at the consumer `type processEpoch interface{ RuntimeEpoch() uint64 }`. In `noteRuntimeStartedLocked`, when `s.Runtime` implements it and its epoch differs from `s.runtimeEpoch`, the method:
  1. resets `runtimeCohort` to 0;
  2. resets `cohortSession` to empty;
  3. records the epoch;
  4. counts the session.

  A runtime without the method keeps the 0079 behavior. `server.go` adds `runtimeEpoch uint64` beside the generation fields, under the same lock. `// spec: §4.7.10 (Supervised runtime process, Generation); §15.4.3 (Session address)`.
- `cmd/lenny-adapter/main.go`:
  - Add the `--supervise-socket` flag beside `--runtime-socket`. Its default is empty, and an empty value means the pod is not supervised.
  - When the flag is set, do the following:
    1. Construct `SupervisedRuntimeProcess` in place of `SocketRuntimeProcess`.
    2. Wrap the `ScrubOps` value in `superviseScrubOps`.
    3. Call `AwaitSupervisor` after the `SO_PEERCRED` self-test and before `srv.Serve`. The timeout is the transport's `AcceptTimeout` default.
    4. When `AwaitSupervisor` fails, log the error and exit non-zero.
- `tests/claim-map.json`: add two `WIRED` rows with `spec_anchor` `#2853-intra-pod`:
  - `Runtime supervisor launch and end`, with the surface `pkg/supervise` `Supervisor.Run` and `pkg/adapter/supervise.go` `SupervisedRuntimeProcess.Start` and `EndProcess`;
  - `Supervisor socket single accept`, with the surface `pkg/adapter/supervise.go` `AwaitSupervisor`.

  No row is `ABSENT`, and `gateway-runtime-comms-remediation.md` is not edited.
- When R6's G3a flag-coverage register is in the tree, add a deferral row for the adapter's `--supervise-socket` flag and the supervisor's `--socket` flag. The row names the checklist step that lands CODE-4, and CODE-4 removes it.

### CODE-4 · Pod builder and sandbox reconciler

**Targets.**

- `pkg/controller/sandbox/podspec/supervise.go` (new)
- `pkg/controller/sandbox/podspec/podspec.go` (`Inputs`, `Build`, `buildSidecar`, `applyResources`, constants)
- `pkg/controller/sandbox/controller.go` (`createPod`)

This change serializes after remediation step R6 under rule S-4.

**Change.**

- `Inputs` gains `RuntimeCommand []string` and `ExecutionMode string`. `createPod` sets both from `rt.Spec.Command` and `rt.Spec.ExecutionMode`.
- Add the constants `SuperviseSocketName = "@lenny-supervise"`, `SupervisorVolumeName = "lenny-supervisor"`, `SupervisorMountPath = "/lenny-supervisor"`, `SupervisorBinaryPath = "/lenny-supervisor/lenny"`, and `SupervisorStageContainerName = "lenny-supervisor-stage"`. The mount path lies outside `/run/lenny`.
- `Build` returns an error when `len(in.RuntimeCommand) > 0` and any of these holds:
  - the deployment model is embedded;
  - `in.EgressCapture != nil`;
  - `in.PreConnect` is true;
  - `in.ExecutionMode == "service"`.

  The RuntimeReconciler's refusal leaves the earlier registry row in place, and `createPod` reads the live CRD, so this check is the enforcement for an edited Runtime. It follows the `resolveNonceOnly` precedent. `// spec: §5.1 (command); §13.1 (Agent UID on a supervised pod)`.
- `buildSidecar` calls `applySupervisor(in, pod)` when `len(in.RuntimeCommand) > 0`. That function does the following:
  - It adds an `emptyDir` volume named `SupervisorVolumeName`.
  - It adds one init container, `SupervisorStageContainerName`, with these settings:
    - image `in.AdapterImage`;
    - `Args: ["supervise", "stage", "--dest", SupervisorMountPath]`;
    - `containerSecurityContext` of the adapter UID;
    - the volume mounted read-write.
  - It sets the runtime container's `Command` to `[SupervisorBinaryPath, "supervise", "run", "--socket", SuperviseSocketName, "--"]` followed by `in.RuntimeCommand`, leaves `Args` empty, and mounts the volume read-only at `SupervisorMountPath`.
  - It appends `--supervise-socket=@lenny-supervise` to the adapter container's args.

  `// spec: §4.7.10 (Supervised runtime process); §13.1 (Agent UID on a supervised pod)`.
- `applyResources` also stamps `in.Resources` on the init container.
- When R6's G3a gate is in the tree, add a supervised `Inputs` case to the gate's render set, and remove the deferral row CODE-3 added.

### CODE-5 · Scoping of 0079's process-reuse comments and admission text

**Targets.**

- `pkg/gateway/runtime/poolstore/poolstore.go`: the `KeepsRuntimeAcrossSessions` doc comment, the CODE-11 error string, and the comment lines CODE-11 adds to `validateRecyclePolicy` and `ValidateSessionPolicy`.
- `pkg/gateway/podlifecycle/podclaim/claimer.go`: the `ClaimRequest.KeepsRuntime` doc comment.
- `pkg/gateway/podlifecycle/podsession/resolve.go`: the `PoolMatch.KeepsRuntime` and `PoolPolicyMirror.KeepsRuntime` doc comments.
- `pkg/gateway/runtime/runtimestore/runtimestore.go`: the `AcknowledgeProcessLevelIsolation` doc comment as 0079's CODE-9 rewrites it.

Behavior does not change.

**Change.** Add the same sentence at each comment site:

```
The predicate reads pool configuration only. On a pod the pod builder rendered with a runtime supervisor (the Runtime declared `command` when the pod was created), a runtime process serves only the sessions of one occupancy cycle and the next cycle runs in a newly launched process. The acknowledgment is required regardless, because pool configuration does not determine which lifetime a given pod carries. spec: §4.7.10 (Supervised runtime process); §5.2 (Deployer acknowledgment (runtime process kept across sessions)).
```

In the CODE-11 error string, replace the clause `the pod keeps one runtime process across the sessions it serves, and the whole-pod scrub does not reach that process (§5.2)` with `the pod may keep one runtime process across the sessions it serves, and other per-pod state persists across the whole-pod scrub (§5.2)`. Every tier-1 assertion that 0079's TEST-12, TEST-13, or TEST-19 makes on the exact string changes with it.

## Staged schema, chart, and migration changes

None. The CRD manifests CODE-1 regenerates are codegen output. No proto, OpenAPI document, chart value, or migration changes.

## Staged docs changes

### DOC-1 · Runtime-author and operator documentation

Every site below is located by the text it has after 0079's DOC-1 through DOC-6 land. Every new sentence uses "launch" and names the supervisor or the platform as its subject. No sentence contains a phrase 0079's TEST-16 lists. The pages carry no spec section numbers and no counts.

(a) **`docs/runtime-author-guide/lifecycle.md`** is the single home. Add a section titled "Supervised runtime process (`command` declared)" after the Recycle Lifecycle section.

1. Lead with the default. Without `command`, the kubelet starts the binary through the image entrypoint, and the process lives as long as the pod.
2. Then state the alternative, in this order:
   - Declaring `command` on the Runtime makes a platform supervisor the first process of the agent container.
   - The supervisor launches the `command` argv at the first session after the pod becomes ready, and again at the first session after each whole-pod scrub. The image entrypoint is not used.
   - The launch happens after the adapter writes the final manifest, so the binary reads that session's manifest at start.
   - A launched process serves one session on a pool with `maxConcurrentSessions: 1`, and the sessions of one occupancy cycle on a concurrent pool.
   - At the whole-pod scrub, the process and every process it started receive SIGKILL with no preceding frame.
   - A process that stops mid-session is not launched again in that pod, and the pod retires.
   - At pod termination, the process receives the SIGTERM the kubelet sends.
   - Embedded runtimes, service-mode runtimes, and runtimes that declare `capabilities.preConnect: true` cannot declare `command`.
   - The runtime's start time moves from pod warm-up onto session start.

(b) Scope each author-facing site that 0079 rewrites and that states the keep lifetime or the entrypoint start. Add a qualifier such as "on a runtime that declares no `command`", linked to the (a) section, and do not restate the mechanism. The sites are:

- `lifecycle.md`: the `starting_session` row, Agent start step 4, the Recycling paragraph, the Terminate Signal paragraph, the Recycle Lifecycle introduction and steps, the Session Mode bullets, and Pod Termination;
- `runtime-author-guide/index.md`: the recycle sentence;
- `integration-levels.md`: the reuse row;
- `echo-runtime.md`: the step that says who starts the runtime;
- `getting-started/concepts.md`: the `starting` state;
- `reference/adapter-contract.md`: the Shutdown row, the Scrub responsibilities paragraph, and step 1 of the Basic-level trace.

The client and API pages that say a session "may" run in an earlier process remain true and are not edited.

(c) **`docs/runtime-author-guide/publishing.md`**, the `ENTRYPOINT` row: on a runtime that declares `command`, the entrypoint is unused and `command` carries the binary's full argv, whose first element is an absolute path.

(d) **`docs/runtime-author-guide/runtime-configuration.md`**, Execution and isolation table: add a `command` row, linked to (a). Its columns are:

- **Controls:** the runtime-process argv, which selects the supervised lifetime.
- **Values:** an optional list of strings whose first element is an absolute path. It is valid only on `deploymentModel: sidecar`, `executionMode: session` runtimes without `preConnect`, and a runtime that violates this is marked `Registered=False`.
- **Set on:** the author, on the `Runtime` CRD only. The admin registration payload does not carry it.
- **Derived:** not settable on a derived runtime.

(e) **`docs/reference/adapter-contract.md`**, Sidecar Architecture: add one sentence. On a runtime that declares `command`, a platform supervisor is the first process of the agent container and launches the binary. Link the sentence to (a). Add no `CH-SUPERVISE` wire summary, because no author implements that channel.

(f) **Acknowledgment homes.** The sites are `docs/reference/execution-modes.md`, at the acknowledgment derivation and in the decision guide, and the acknowledgment paragraph of `runtime-configuration.md`.

- Add one sentence at each site. `acknowledgeProcessLevelIsolation` and the tenant pin apply whatever the runtime declares. On a supervised runtime the following still persist: the adapter process, System V semaphores and message queues, and the best-effort residual state.
- The decision guide also adds `command` as the choice for a new runtime process per session on `sandboxed` and `standard` pools, where `vm-restart` is unavailable, and notes the session-start latency cost.

(g) **`docs/operator-guide/security.md`**, Adapter-Agent Boundary: add one paragraph that states the following.

- On such pods the supervisor runs at the agent UID.
- The adapter accepts the supervisor's connection once, before the pod reports ready.
- The supervisor is not a session or tenant isolation boundary.
- Its non-dumpable mark and the reach of its kill on gVisor and Kata are checked by the security integration tests that check `SO_PEERCRED`.
- Unlike `SO_PEERCRED`, it has no startup self-test.

## Testing

Tier selection follows `.claude/rules/test-coverage.md`. Every test carries a `// spec:` annotation, and every test at tier 2 or above carries a `// diagnosis:` comment. No test outside a container calls `kill(-1)`. Tier 1 and tier 3 use a fake `procOps`.

### FIXTURE-1 · Probe runtime and supervised Kind pool

**Targets.**

- `cmd/runtimes/supervise-probe/main.go` and `cmd/runtimes/supervise-probe/Dockerfile`
- `tests/testinfra/kind/agent-workload.yaml`
- `tests/testinfra/kind/install.sh`

`bootstrap-overlay.gen.yaml` is generated at install time and is not a target.

**Change.**

- `supervise-probe` is a test-only Standard-level runtime. It is built on the Alpine base `cmd/runtimes/cred-shell-echo` uses and added to `install.sh`'s image build and load list.
  - **At process start** it does the following:
    1. Reads the adapter manifest.
    2. Records the manifest's session identifier and `mcpNonce`.
    3. Connects to the platform MCP server with the nonce and calls `tools/list`.
    4. Records a per-process token: its PID and the start time from `/proc/self/stat`, so that a reused PID does not read as the same process.
  - **On request** it does the following:
    - reports the token and the manifest values it recorded;
    - double-forks a daemon;
    - creates an agent-UID System V shared-memory segment;
    - makes a platform MCP `tools/call`.
  - **IMPLEMENTOR'S CHOICE:** the request vocabulary. Constraint: requests arrive as `message` input text, and answers return in `response` output, so a test drives the probe through the public session API.
  - **Subcommands.** The binary also carries subcommands that the tier-6 and tier-9 tests exec in the runtime container: a ptrace attach to PID 1, reads of `/proc/1/mem` and `/proc/1/fd`, a write under `/lenny-supervisor`, a signal to PID 1, and a bind of `@lenny-supervise`.
- `agent-workload.yaml`: add a Runtime CR `supervise-probe-runtime` with these fields:
  - `type: agent`
  - `image: __SUPERVISE_PROBE_IMAGE__`
  - `integrationLevel: standard`
  - `executionMode: session`
  - `isolationProfile: standard`
  - `deploymentModel: sidecar`
  - `command: ["/usr/local/bin/supervise-probe"]`

  It declares no `credentialPoolRefs`. `install.sh` substitutes the placeholder in the step that substitutes `__CRED_SHELL_ECHO_IMAGE__`. The CR carries a comment citing `spec: §4.7.10 (Supervised runtime process); §5.1 (command)`.
- In the `install.sh` bootstrap-overlay heredoc, add a matching `bootstrap.runtimes` row named `supervise-probe-runtime`, which does not model `command`. Also add a pool `supervised-probe-pool` with these settings:
  - `runtimeRef: supervise-probe-runtime`
  - `isolationProfile: standard`
  - `executionMode: session`
  - `warmCount: 2`
  - `allowStandardIsolation: true`
  - `dnsPolicy: cluster-default`
  - `sessionPolicy`:
    - `recycle: {enabled: true, acknowledgeBestEffortScrub: true, maxSessionsPerPod: 3}`
    - `acknowledgeProcessLevelIsolation: true`
    - `cleanupTimeoutSeconds: 30`

  The pool's comment states two things: the pool has its own `runtimeRef` so that `ResolvePool` does not return `ErrAmbiguousPool`, and `task-mode-echo-pool` remains the unsupervised keep pool for 0079's TEST-11.
- These are static install-time objects, so the test-coverage check-6 start-time sweep does not apply.

### TEST-1 · Tests across the reached tiers

**TEST-1(a) — CODE-1 (tiers 1 and 2).**

- Tier 1, in `pkg/controller/runtime/controller_test.go`, next to `TestMirror_RejectsEmbeddedNonceOnly_spec_4_7`. `// spec: 5.1 (Runtime)`.
  - `TestMirror_RejectsCommandOnEmbedded_spec_5_1`, `TestMirror_RejectsCommandOnServiceMode_spec_5_1`, and `TestMirror_RejectsCommandWithPreConnect_spec_5_1` each assert a `permanentError` and no registry write.
  - `TestMirror_CommandIsNotMirrored_spec_5_1` asserts that a sidecar session runtime with `command` produces a registry row equal to one without it.
- Tier 2, in `tests/tier2_component/controllers/runtime_test.go` (envtest). `// spec: 5.1 (Runtime)`.
  - The API server rejects `command: ["python", "-m", "agent"]` and `command: [""]`, and it admits `command: ["/usr/local/bin/agent"]`.
  - An embedded runtime, a service-mode runtime, and a `preConnect` runtime, each with `command`, reach `Registered=False` with reason `InvalidRuntime`.
  - `// diagnosis:` the field's CRD validation or the reconciler refusal drifted from §5.1.

**TEST-1(b) — CODE-2 and CODE-3 (tiers 1, 3, and 7a).**

- Tier 1, in `pkg/supervise/*_test.go`, using a fake `procOps`. `// spec: 4.7.10 (Deployment Model), 28.5.3 (Intra-pod)`.
  - Wire: requests and replies round-trip, `error` is omitted when empty, and an unknown `type` or a misplaced type is an error.
  - `launch` while live replies `launched{error}`. An unresolvable `argv[0]` replies `launched{error}` and does not call `Launch`.
  - `end`, clean case: the reply is `ended{}`, shm removal is called with the supervisor's UID, and `live` is cleared.
  - `end`, survivor case: `OtherProcesses` stays true, so the reply is `ended{error}` once the deadline passes. The test also asserts that `end` returns rather than blocking.
  - SIGTERM calls `KillAll(SIGTERM)`, and the loop keeps serving. EOF makes `Run` return.
  - `Main` exits non-zero when `SetNonDumpable` fails.
  - `RemoveAgentShm` parses fixture text for `/proc/sysvipc/shm`. A segment whose `uid` was changed but whose `cuid` matches is removed, and a segment owned by another UID is kept. A missing file takes the `SHM_STAT` path through a seam.
- Tier 1, in `pkg/adapter/supervise_test.go`, with a fake supervisor peer on a unique abstract name per test. `// spec: 4.7.10 (Deployment Model), 4.7.9 (Startup Sequence), 28.6 (Exclusivity and concurrency model)`.
  - `AwaitSupervisor`:
    - accepts one connection, after which a second dial is refused;
    - returns an error on timeout, and the listener is closed;
    - rejects a peer whose UID does not match when the check is enforced.
  - `Start` sends one `launch`, and the next `Start` joins without a second one.
  - `launched{error}` sets `failed`, sends exactly one best-effort `end`, and `ServesNextSession` then reports false.
  - Stale connection: after an attested `EndProcess`, a `CH-MSGSOCK` connection dialled before the end reads EOF, and the next `Start` accepts a new connection. **This is the assertion that discriminates D-STALE.**
  - `ended{error}`, a reply with no outstanding request, and peer EOF each make `ServesNextSession` false.
  - `superviseScrubOps.KillUserProcesses` calls the inner operation before `EndProcess` and joins both errors.
  - `CloseListener` closes the link, and it is idempotent.
- Tier 1, in `pkg/adapter/runtimeops_test.go`. `// spec: 28.5.3 (Intra-pod)`.
  - A backlogged connection whose peer pre-wrote `lifecycle_support` and closed is rejected, and the next real connection completes the handshake.
- Tier 1, in `pkg/adapter/runtimegeneration_test.go`. `// spec: 15.4.3 (Runtime Integration Levels), 4.7.10 (Deployment Model)`.
  - A session on epoch 1, then an end, then a session on epoch 2 makes the second session `soleSession`.
  - Two sessions on one epoch are refused, which is the 0079 D9 rule applied per process.
  - A runtime without `RuntimeEpoch` keeps the 0079 behavior.
- Tier 3, in `tests/tier3_contract/supervise/supervise_wire_test.go`. `// spec: 28.5.3 (Intra-pod)`.
  - The real `pkg/supervise` `Run` loop, with a fake `procOps`, runs against the real `SupervisedRuntimeProcess` over a socket.
  - The test asserts the exact member names `type` and `error`, the reply-per-request order, and that the supervisor never sends an unsolicited frame.
  - `// diagnosis:` the two ends of `CH-SUPERVISE` disagree on the frame format.
- Tier 7a, in `tests/tier7a_load_local/supervise_process_race_test.go`, under `-race` with a goroutine-leak check. `// spec: 4.7.10 (Deployment Model)`.
  - The test races `Start` against `Start` and asserts exactly one `launch`. It also races `Start` against `EndProcess`, `EndProcess` against `CloseListener`, and the reader goroutine against an outstanding request.
  - `// diagnosis:` a launch or an end can be issued twice, or the link's goroutine leaks.

**TEST-1(c) — CODE-4 (tiers 1 and 2).**

- Tier 1, in `pkg/controller/sandbox/podspec/supervise_test.go`. `// spec: 4.7.10 (Deployment Model), 13.1 (Pod Security)`.
  - Rendering:
    - one init container with the adapter image, the adapter UID, the stage args, and resources stamped;
    - the volume;
    - the exact runtime-container `Command`, with empty `Args`;
    - a read-only mount outside `/run/lenny`;
    - the adapter `--supervise-socket` arg.
  - No init container and no flag are rendered when `RuntimeCommand` is empty.
  - `Build` refuses `RuntimeCommand` combined with each of the embedded model, `EgressCapture`, `PreConnect`, and `ExecutionMode: service`.
  - No container other than the runtime container runs at the agent UID.
- Tier 1, in the `pkg/admission/webhook` pod-security and cosign tests. `// spec: 13.1 (Pod Security)`.
  - A rendered supervised pod passes the credential-group and mount checks.
  - The init container's image is verified.
- Tier 2, in the `pkg/controller/sandbox` envtest. `// spec: 5.1 (Runtime), 4.7.10 (Deployment Model)`.
  - A Runtime with `command` yields a supervised Pod.
  - A mirrored `preConnect` Runtime that is edited to add `command` yields no Pod.
  - A Pod created before a Runtime edit keeps its spec.
  - `// diagnosis:` the render-time refusal or the live-CRD read regressed.

**TEST-1(d) — FIXTURE-1 and the cluster tiers (tiers 4, 5, 6, 8, and 9).**

- Tier 4, a supervised case in `tests/tier4_integration/recycle_scrub_path_test.go`. `// spec: 5.2 (Pool Configuration and Execution Modes)`.
  - An attested end reports `runtime_live: true`, and the pod reaches `reserved`.
  - `ended{error}` reports false, and the pod retires.
  - `// diagnosis:` the end result no longer reaches the scrub report.
- Tier 5, in `tests/tier5_e2e_kind/supervised_runtime_test.go` on `supervised-probe-pool`. `// spec: 4.7.10 (Deployment Model), 4.7.9 (Startup Sequence), 5.2 (Pool Configuration and Execution Modes)`.
  - Two sequential same-tenant sessions on one pod report different process tokens.
  - Each process's recorded manifest session identifier equals its own session.
  - A platform MCP call in session 2 succeeds without addressing a session.
  - A double-forked daemon from session 1 is absent in session 2, and so is an agent-UID shm segment from session 1.
  - The pod reports Ready only after the supervisor is connected.
  - `// diagnosis:` a supervised pod reused a runtime process or its children across the boundary, or launched before the final manifest.
- Tier 6, in `tests/tier6_e2e_cloud/supervisor_kernel_properties_test.go` on gVisor and Kata, with a `tests/registers/skip-reasons.yaml` entry for environments without those nodes. `// spec: 4.7.11 (Adapter-Agent Security Boundary)`.
  - The test checks the following:
    - `PR_SET_DUMPABLE` blocks ptrace and `/proc/1` reads;
    - `kill(-1)` reaches double-forked and nested descendants;
    - orphans reparent to the supervisor;
    - the agent-UID shm enumeration works through either path.
  - `// diagnosis:` an isolation runtime diverges from the kernel properties the supervisor relies on for freshness.
- Tier 8, in `tests/tier8_chaos/supervisor_loss_test.go`. `// spec: 4.7.10 (Deployment Model)`.
  - A probe subcommand signals PID 1 with SIGQUIT, and the pod serves no later session and retires.
  - Killing the adapter container's process makes the supervisor exit, and no runtime process remains.
  - `// diagnosis:` loss of the supervisor or the adapter leaves a pod serving with an unsupervised or orphaned runtime.
- Tier 9, in `tests/tier9_security/supervisor_boundary_test.go`. `// spec: 4.7.11 (Adapter-Agent Security Boundary), 13.1 (Pod Security)`.
  - From the agent UID, the following fail:
    - a ptrace attach to PID 1;
    - reads of `/proc/1/mem` and `/proc/1/fd`;
    - a write under `/lenny-supervisor`, which fails with EROFS.
  - A listener that the agent UID binds on `@lenny-supervise` after the adapter's accept never receives a connection, which pins that the supervisor does not redial.
  - A pool that combines egress capture with a supervised runtime renders no pod.
  - `// diagnosis:` author code can reach the supervisor's memory, binary, or channel.

**TEST-1(e) — documentation and §28 (tiers 0 and 11).**

- The existing tier-0 gates in `tests/tier0_static` pass with the `CH-SUPERVISE` row, card, matrix row, and claim rows: `matrix_completeness_test.go`, `identifier_resolution_test.go`, `naming_lint_test.go`, and `claim_register_test.go`. The tier-11 `spec_28` register-writer and index-row tests pass as well.
- In `tests/tier11_docs/runtime_process_lifetime_doc_reconciliation_test.go`, add `TestSupervisedRuntimeProcessHasOneSpecHome`. `// spec: 4.7.10 (Deployment Model), 4.7.11 (Adapter-Agent Security Boundary), 5.2 (Pool Configuration and Execution Modes)`.
  - `spec/04` contains `**Supervised runtime process.**` exactly once.
  - Item 8 of `spec/04` §4.7.11 links `28_communication-channels.md#2853-intra-pod` and does not contain "closes the listener".
  - `spec/05` **What the scrub reaches.** contains `On a pod whose runtime declares `command`, step 1 also ends`.
  - `docs/runtime-author-guide/lifecycle.md` contains the DOC-1(a) heading, and `docs/runtime-author-guide/runtime-configuration.md` contains the `command` row.
  - `// diagnosis:` the supervised lifetime is restated outside its home, or its reader-facing pages are missing.
- In the same file, update the `// diagnosis:` of 0079's `TestRuntimeProcessLifetimeStatementsAgreeAcrossSpecAndDocs` so it refers only to runtimes that declare no `command`. Its banned-phrase list is unchanged.

## Edge cases and accepted failure modes

| Case | Outcome | Where it lands |
|:--|:--|:--|
| Two first `Start` calls on a concurrent pool | The second waits on `launchMu` and joins `gen`, and one `launch` is sent | CODE-3, TEST-1(b) tier 7a |
| A `CH-MSGSOCK` connection is backlogged when the end arrives | `gen.CloseListener()` discards it, and the next process binds a new listener | CODE-3, TEST-1(b) |
| A `CH-RUNTIMEOPS` connection is backlogged when the end arrives | The handshake send fails with EPIPE, and `Run` accepts the next connection | CODE-3, TEST-1(b) |
| The abstract name is still bound when the next launch starts | The bind fails, `failed` is set, and the pod retires at the next report | CODE-3 |
| The scrub context expires during `end` | `failed` is set, step 1 returns the joined error, and the report carries `runtime_live: false` | CODE-3 |
| A process survives repeated SIGKILL, for example in D state | The supervisor replies `ended{error}` at its deadline rather than blocking | CODE-2, TEST-1(b) |
| `/proc/sysvipc/shm` is absent in the guest | The `SHM_STAT` enumeration runs. If it also fails, `ended{error}` is sent | CODE-2, TEST-1(d) tier 6 |
| An agent changes a segment's owner with `IPC_SET` | The segment matches on `cuid` and is removed | CODE-2, TEST-1(b) |
| SIGINT, SIGHUP, or SIGQUIT is delivered to the supervisor from author code | The supervisor exits under Go's default handler, the container ends, and the pod retires | CODE-2, TEST-1(d) tier 8 |
| A Runtime with `preConnect` is edited to add `command` | The reconciler refuses it and keeps the earlier registry row, and `Build` renders no pod | CODE-1, CODE-4, TEST-1(c) |
| The adapter image predates `supervise` | The runtime container exits with a flag-package error. Test-coverage preflight check 1 applies | FIXTURE-1 |

## Files touched on application (non-spec)

- `pkg/apis/lenny/v1alpha1/runtime_types.go`, `pkg/apis/lenny/v1alpha1/zz_generated.deepcopy.go`
- `charts/lenny/crds/lenny.dev_runtimes.yaml`, `pkg/embedded/crds/lenny.dev_runtimes.yaml`
- `pkg/controller/runtime/controller.go`, `pkg/controller/runtime/controller_test.go`
- `pkg/supervise/wire.go`, `stage.go`, `supervisor.go`, `procs_linux.go`, `procs_other.go`, `main.go`, and their `_test.go` files
- `cmd/lenny-adapter/main.go`
- `pkg/adapter/supervise.go`, `pkg/adapter/supervise_test.go`, `pkg/adapter/runtimegeneration.go`, `pkg/adapter/runtimegeneration_test.go`, `pkg/adapter/server.go`, `pkg/adapter/runtimeops_test.go`
- `tests/claim-map.json`, and the R6 G3a register when present
- `pkg/controller/sandbox/podspec/supervise.go`, `pkg/controller/sandbox/podspec/supervise_test.go`, `pkg/controller/sandbox/podspec/podspec.go`, `pkg/controller/sandbox/controller.go`
- `pkg/admission/webhook` pod-security and cosign tests
- `pkg/gateway/runtime/poolstore/poolstore.go`, `pkg/gateway/podlifecycle/podclaim/claimer.go`, `pkg/gateway/podlifecycle/podsession/resolve.go`, `pkg/gateway/runtime/runtimestore/runtimestore.go`
- `cmd/runtimes/supervise-probe/main.go`, `cmd/runtimes/supervise-probe/Dockerfile`, `tests/testinfra/kind/agent-workload.yaml`, `tests/testinfra/kind/install.sh`
- `tests/tier2_component/controllers/runtime_test.go`, `tests/tier3_contract/supervise/supervise_wire_test.go`, `tests/tier4_integration/recycle_scrub_path_test.go`, `tests/tier5_e2e_kind/supervised_runtime_test.go`, `tests/tier6_e2e_cloud/supervisor_kernel_properties_test.go`, `tests/registers/skip-reasons.yaml`, `tests/tier7a_load_local/supervise_process_race_test.go`, `tests/tier8_chaos/supervisor_loss_test.go`, `tests/tier9_security/supervisor_boundary_test.go`, `tests/tier11_docs/runtime_process_lifetime_doc_reconciliation_test.go`
- `docs/runtime-author-guide/lifecycle.md`, `index.md`, `integration-levels.md`, `echo-runtime.md`, `publishing.md`, `runtime-configuration.md`; `docs/getting-started/concepts.md`; `docs/reference/adapter-contract.md`, `docs/reference/execution-modes.md`; `docs/operator-guide/security.md`
