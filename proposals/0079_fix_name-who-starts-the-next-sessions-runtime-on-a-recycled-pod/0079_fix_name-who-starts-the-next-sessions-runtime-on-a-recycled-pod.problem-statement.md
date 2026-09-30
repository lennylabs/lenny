# Problem: Name who starts the next session's runtime on a recycled pod

## 1. Problem

### 1.1 What the specification promises

§15.4 states, outside the integration-level matrix, that pod recycling "is not a level-sensitive capability and does not appear in the matrix: the platform scrubs the pod and starts a fresh runtime process for each session, so recycling requires no runtime cooperation and is available at every integration level" (§15.4). §5.2 repeats the claim under **Recycling and integration levels** (§5.2). `recycle.maxSessionsPerPod` is required with no default, and §5.2 explains that the requirement exists to force an explicit reuse-limit choice (§5.2). §6.1's recycle row states that at the occupancy-zero boundary "the whole-pod scrub terminates the SDK process along with all other session processes", after which the adapter "re-establishes SDK-warm state on the `claimed → sdk_connecting` re-warm edge" (§6.1).

Every one of those statements describes a pod that serves session N+1.

### 1.2 What the sidecar transport delivers

Under §4.7.10 the default deployment model puts the runtime in its own pod container, started by the kubelet, communicating with the adapter over an abstract Unix socket. No component creates that container's process a second time.

- The kubelet does not. `basePod` renders every agent pod with `RestartPolicy: Never` (`pkg/controller/sandbox/podspec/podspec.go:980`), and `buildSidecar` renders the runtime as an ordinary container (`pkg/controller/sandbox/podspec/podspec.go:609`), so a container that exits stays exited.
- The adapter does not. `SocketRuntimeProcess`'s own doc comment records the contract: "The adapter never spawns the runtime in this model — the kubelet starts the runtime container" (`pkg/adapter/socketruntime.go:33`). The `SpawnPath` field that would exec one has no production caller; the four assignments in the tree are all in tests.
- The scrub cannot reach it. §13.1 forbids `shareProcessNamespace` on every pod template Lenny generates (§13.1), so `DefaultOps.KillUserProcesses` (`pkg/adapter/scrub/defaultops.go:28`) signals only the adapter container's own process tree.

The adapter ends the runtime at occupancy zero. `SocketRuntimeProcess.Close` closes the shared connection, reaps a spawned child, and closes the listener when its active set empties (`pkg/adapter/socketruntime.go:435-467`), and `Interrupt` of the last active session closes the connection (`:398-417`). The runtime observes the clean-exit EOF and returns. Ending the runtime at occupancy zero discards a process no component can create again, on a pod the recycle disposition then holds for its tenant.

### 1.3 How the failure presents

The gateway patches the claim to `recycling`, the adapter runs the scrub against a runtime container whose process is already gone, the scrub reports success, and the disposition decider reuses the pod (`pkg/sandbox/podscrub/podscrub.go:328`). The claim reaches `reserved`, the pod is held for its pinned tenant, and the tenant's next session is dispatched onto it with no acquisition round trip. `StartSession` reaches `Runtime.Start` (`pkg/adapter/session.go:156`), `SocketRuntimeProcess.Start` finds no live connection and waits out the accept bound, and the session fails. The gateway categorises the failure as transient and retries onto another pod, so the client usually sees a slower session rather than an error and the operator sees nothing: the scrub succeeded, the pod was reused, the retry worked.

The consequence for capacity is concrete. §5.2's `mode_factor` converges toward `recycle.maxSessionsPerPod` on a `standard` or `in-place` recycling pool (§5.2), so a pool configured for twenty sessions per pod is provisioned for a fraction of the pods it actually needs.

### 1.4 The two subsidiary false statements

**The scrub does not terminate the runtime's process.** `scrub.Ops` states of step 1 that "It terminates the runtime's SDK process along with every other task process" (`pkg/adapter/scrub/scrub.go:74-77`). §6.1's recycle row makes the same claim at specification level. Both are false on the sidecar model.

The correct statement is narrower than a flat denial and is not derivable from `shareProcessNamespace: false` alone. `buildSidecar` mounts `workspace`, the credential volume, `tmp`, `sessions`, `artifacts`, `/dev/shm`, and `shared` into both containers (`pkg/controller/sandbox/podspec/podspec.go:536-557`), so steps 0, 2, 4, and 6 do cross the container boundary. Step 1b's `ipcrm --all=shm` runs in the pod's shared IPC namespace, because §13.1 forbids only `hostIPC`, but it removes only the segments the adapter's UID owns or created, and the runtime runs under a different UID (`pkg/controller/sandbox/podspec/podspec.go:56-57`). What does not cross is step 1's process kill, step 3's environment restoration, and step 5's log-buffer truncation, each of which is scoped to the adapter container.

**§4.7.9 step 7 names the wrong actor.** The startup sequence reads "Adapter spawns runtime binary" (§4.7). That holds in the embedded model and in the developer loop, and it is false in the sidecar model that the following section makes the default. Nothing reconciles the two sections.

### 1.5 The two tests that should have caught it

`tests/tier10_conformance/recycle_scrub_conformance_test.go` Property 2 asserts that "the pod process stays alive across the recycle boundary — a replacement session binds, which is impossible if `Shutdown` terminated the pod" (`:231`). It is asserted against `scrubConformanceRuntime`, whose `Start` returns `nil` unconditionally (`:61`). No transport behaviour can make the property fail.

`tests/tier5_e2e_kind/execution_modes_test.go` `TestTaskModeRecycleScrubsWorkspaceBetweenSessions` would catch it against a real pod. It is skipped (`:124`) with the reason recorded at `tests/registers/skip-reasons.yaml:1120-1124`, and its pool `task-mode-echo-pool` is backed by `echo-runtime-task-mode`, a `deploymentModel: sidecar` runtime (`tests/testinfra/kind/agent-workload.yaml:209`).

### 1.6 Finding

BUILD-GAPS F-5.2.33 part (b). Part (a), the pod-scoped listener destroyed by a per-session `Close`, is proposal 0078 and is a precondition of the conformance work in TEST-10.

### 1.7 Decision of 2026-09-30

The human decided the direction of the fix on 2026-09-30. On a recycling pool with `recycle.maxSessionsPerPod > 1`, the runtime process is kept across sessions, including across occupancy-zero boundaries, and serves the tenant's sessions until the pod reaches `recycle.maxSessionsPerPod` or another retire trigger fires. Every session is already a slot (proposal 0073), and a concurrent pool already keeps one runtime process serving many sessions while occupancy stays above zero, so the fix extends that model across occupancy zero. The decision carries four conditions: a `scrubProfile: vm-restart` pool still restarts after every session, keeping the process requires the existing `sessionPolicy.acknowledgeProcessLevelIsolation: true`, a pod on an `allowCrossTenantReuse` pool is retired when the tenant changes, and a pod whose runtime has exited at a boundary is retired rather than dispatched into.
