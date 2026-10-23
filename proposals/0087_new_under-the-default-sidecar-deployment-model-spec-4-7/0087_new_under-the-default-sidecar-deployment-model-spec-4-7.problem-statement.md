# Problem: Sidecar runtime processes have no platform supervisor

## Statement

Under the default sidecar deployment model (§4.7.10), nothing the platform controls starts, observes, or ends the runtime process. The pod builder sets `RestartPolicy: Never` and gives the runtime container no `Command` or `Args`, so the kubelet runs the image entrypoint once when the pod starts. The adapter drops every capability, shares no process namespace with the runtime container (§13.1 forbids `shareProcessNamespace`), and cannot exec a binary that exists only in the runtime image. Proposal 0079 (implemented 2026-10-03) made that the specified lifetime: §4.7.10 **Runtime process lifetime.** states that the runtime process lives as long as the pod, one `CH-MSGSOCK` connection serves every session, and a recycling pool that keeps the process requires `acknowledgeProcessLevelIsolation`.

That baseline leaves these defects and gaps on every sidecar pod:

- The runtime starts before the adapter writes the manifest, so the first session's runtime reads no manifest and dials no platform MCP server or `CH-RUNTIMEOPS` (BUILD-GAPS F-4.7.26, first-session half).
- The specification promises adapter-sent SIGTERM and SIGKILL, a `shutdown` deadline, and a `RUNTIME_CRASH` built from the exit code, and no production transport performs any of them (F-4.7.27).
- No sidecar runtime can be SDK-warm, so every session on a sidecar `preConnect` pool fails (F-6.1.28).
- Scrub step 1 (`kill -9 -1`) and step 1b (`ipcrm`) reach only the adapter container, and `scrub.DefaultOps` execs `sh` and `ipcrm`, which the distroless adapter image does not contain. The scrub leaves System V message queues and semaphores, POSIX message queues, and agent-owned files outside the slot trees (F-5.2.37, cleanup half). Step 3 never resets the runtime's environment (F-5.2.39, step-3 half).
- Deployer `cleanupCommands` run at the adapter UID with the workspace base as working directory, through symlinks the agent planted (F-5.2.36).
- A hung sidecar runtime on a concurrent recycling pool passes the occupancy-zero scrub, because the sidecar `Interrupt` that heartbeat escalation calls ends nothing and the runtime's open connection reads as live (F-5.2.46).
- A recycling pool can only keep its runtime process. A fresh runtime process per session is available only through `scrubProfile: vm-restart`, which exists only on `microvm` pools.

The owner directed a platform supervisor as the first process of the runtime container on every sidecar pod (plan §10.3, supervisor scope, 2026-10-02). An init container stages a static first-party binary into a shared volume, and the pod builder makes it the runtime container's command. On the adapter's instruction, over a new intra-pod channel `CH-SUPERVISE`, the supervisor launches each runtime process after the manifest exists, ends it at the boundaries the pool's lifetime defines, proves that no runtime process remains, and performs the agent-UID halves of the scrub. A pool-level lifetime, `recycle.runtimeProcess: keep | restart`, selects between 0079's kept process and a fresh process at each occupancy-zero boundary that is reused.

This proposal is part 1 of that work. Part 2, cross-tenant reuse on `in-place` pools by resetting the tenant pin, is proposal 0096.

## Owner decisions this proposal implements

All are recorded in `gateway-runtime-comms-remediation.md` §10.3, and the Phase 2 entry of §10.2 lists the items part 1 carries.

- Every sidecar pod runs the supervisor, whatever its lifetime. Every sidecar Runtime declares `command`. Plan §10.3 also directed that the admin registration API carry `command`; the owner reversed that on 2026-10-22, and `command` is settable only on the Runtime CRD (summary D-COMMAND). `preConnect` and service-mode runtimes get supervised designs rather than exclusions.
- The lifetime lives on the pool as `recycle.runtimeProcess: keep | restart`, required with no default on new recycling pools. `restart` does not require `acknowledgeProcessLevelIsolation`.
- `restart` is refused for embedded, service-mode, and `preConnect` runtimes at gateway pool admission, through a flag the RuntimeReconciler mirrors into the runtime registry. `deploymentModel` stays CRD-only.
- At the recycle boundary the supervisor ends the runtime before the deployer's `cleanupCommands` run.
- A supervised pod keeps retiring on a runtime crash. Restart after a crash stays deferred.
- The supervisor is a single-threaded binary with no language runtime, from its own digest-pinned image. It handles every catchable signal and acts on a stop only when `si_pid == 0` and `si_code == SI_USER`. A runtime can kill or freeze its own pod's supervisor, which retires the pod.
- The supervisor is a boundary for the reuse decision. On gVisor the signal-immunity property fails and fails closed, so `restart` is offered there. A conformance or preflight check runs the properties on the deployer's RuntimeClasses and refuses `restart` where they fail.
- `CH-SUPERVISE` is JSON Lines and adds no proto field.
- Every finding of the adversarial security review of 2026-10-02 that no other step owns (plan §10.2 Phase 2 entry).

## Evidence

Tree, verified against `proposal-b` at `f52fc9a49` on 2026-10-22:

- `pkg/controller/sandbox/podspec/base_pod.go` `basePod` sets `RestartPolicy: Never`. `sidecar.go` `buildSidecar` renders the runtime container with no `Command` or `Args`. No production code renders `InitContainers`. `security_context.go` `containerSecurityContext` sets container-level `RunAsUser` and `RunAsGroup`, drops every capability, and sets `ReadOnlyRootFilesystem`. The runtime container mounts `/workspace`, `/tmp`, `/sessions`, `/artifacts`, and `/dev/shm` read-write and `/run/lenny` and `/workspace/shared` read-only (`sidecar.go`, `volumes.go`).
- `egress_capture.go` runs the test-only egress-capture container at UID 65531, off the agent UID.
- `pkg/controller/sandbox/controller.go` `createPod` reads the Runtime CRD live and passes no execution mode to `podspec.Inputs`. `pkg/apis/lenny/v1alpha1/runtime_types.go` has no command field, and `image` is digest-pinned.
- `pkg/controller/runtime/controller.go` `applyCRDFields` leaves `DeploymentModel` CRD-only. `pkg/gateway/externalapi/admin/runtimes.go` `RuntimePayload` models neither `deploymentModel` nor `requireSoPeercred`, and an admin-registered runtime has no pod path unless a Runtime CRD of the same name exists.
- `pkg/adapter/socketruntime.go` `SocketRuntimeProcess` binds the `CH-MSGSOCK` listener at construction, installs one connection for the pod's life, records a sticky `ended`, and never redials. Its doc comment states that the adapter never spawns the runtime.
- `pkg/adapter/runtimegeneration.go` counts every session the process has been given for the pod's life and never resets. The tree has no epoch.
- `pkg/adapter/intrapodauth.go` authenticates every `CH-MSGSOCK` and `CH-RUNTIMEOPS` connection by a runtime-first nonce line against the `mcpNonce` of the manifest published at accept time, and refuses a connection while no manifest is published. `writeSessionManifest` (`pkg/adapter/manifest.go`) mints a fresh nonce at each session start. No manifest is written at adapter boot (F-4.7.25 is closed by proposal 0090).
- `pkg/adapter/runtimeops.go` serves one connection at a time and accepts the next after a disconnect.
- `pkg/adapter/scrub/scrub.go` runs step 0, then `cleanupCommands`, then steps 1 to 6. `podscrub.go` `scrubConfig` sets no `ResetEnv` or `TruncateLogs`. `defaultops.go` runs `sh -c "kill -9 -1"` and `ipcrm --all=shm` through `os/exec`. `podscrub.go` reports `runtime_live` from `ServesNextSession`.
- `pkg/adapter/heartbeat.go` `onHeartbeatHung` calls `Runtime.Interrupt`, which the sidecar transport answers with nil.
- `pkg/adapter/sdkwarm.go`: on a sidecar pod `PreConnect` is a no-op, and `ConfigureWorkspace` and `DemoteSDK` return `Unimplemented`.
- `pkg/adapter/holdstate.go` `onHoldTimeout` calls `CloseListener` on the runtime.
- `cmd/lenny-adapter/main.go` dispatches the `prestop` subcommand and runs the `SO_PEERCRED` self-test before `srv.Serve`. The root `Dockerfile` builds the adapter on `gcr.io/distroless/static:nonroot` with the single binary `/usr/local/bin/lenny`.
- `pkg/controller/sandbox/podspec/termination.go` renders the adapter's preStop hook as `/usr/local/bin/lenny prestop` (F-4.6.37, closed 2026-10-22). The hook SIGTERMs PID 1 of its own container (F-4.6.38, open, owned by R16).
- `pkg/gateway/runtime/poolstore/poolstore.go` `KeepsRuntimeAcrossSessions` and `validateRecyclePolicy` carry 0079's predicate and acknowledgment rule. `pkg/gateway/podlifecycle/podclaim/tenantpin.go` `AdmitTenantPin` refuses every pinned pod when `KeepsRuntime` is false. No `runtimeProcess` field exists in `pkg/`, `schemas/`, or `spec/`.
- `pkg/podsecurity/podsecurity.go` `identityViolations` reserves the agent UID for the regular container `runtime` and the adapter UID for the regular container `adapter`, on every init, regular, and ephemeral container. `pkg/admission/webhook/cosign_verify.go` verifies init-container images.
- `tests/registers/exceptions-deployment-boundary.yaml` defers `lenny-adapter --runtime-ops-socket` to R17.

Specification:

- §4.7.9 steps 3, 4, 6, and 7; §4.7.10 **Runtime process lifetime.** and the trade-off row; §4.7.11 item 1 and its **Runtime connection handshake.** paragraph.
- §5.2 **Recycle lifecycle**, **Lenny scrub procedure** (step 0 before `cleanupCommands`, steps 1 to 6 after), **What the scrub reaches.**, the best-effort paragraph, **Deployer acknowledgment (runtime process kept across sessions).**, and **Runtime not live:**.
- §6.1 SDK-warm mode and the `preConnect` compatibility table; §6.2 the re-warm edge.
- §13.1 **Container identity.**
- §28.5.3 the `CH-MSGSOCK` card's `shutdown` timing, **Inbound: `shutdown`**, and **Exit Codes**; §28.8 the `CH-MSGSOCK` row; §15.4 the graceful-drain row and the Basic-level shutdown conformance row.

Plan and records:

- `gateway-runtime-comms-remediation.md` §10.2 Phase 2 (the 0087 part 1 entry), Phase 3 (the 0087 part 2 entry), §10.3, R16, R17, and R23.
- The adversarial security review of 2026-10-02 (`scratchpad/supervisor-security-review/review.md` on the machine that ran it) and the gVisor spike of the same day (plan §10.3).
- BUILD-GAPS F-4.7.26, F-4.7.27, F-5.2.36, F-5.2.37, F-5.2.39, F-5.2.46, and F-6.1.28.
- Proposals 0090 and 0091 hand items to 0087 (0090 summary decisions 13, 25, and 27 and its impacts row; 0091 impacts row).

## Who observes it

The platform is pre-deployment. Once deployed:

- Every sidecar pod's first session runs without the manifest's capabilities (F-4.7.26).
- Every session on a sidecar `preConnect` pool fails (F-6.1.28).
- A recycling sidecar pool must accept runtime-process state carrying between sessions, because keeping the process is its only reuse path.
- A runtime author reads signal, deadline, and exit-code promises that nothing keeps (F-4.7.27).
- A deployer's `cleanupCommands` can be turned into a confused deputy by agent-planted symlinks (F-5.2.36).

## Prerequisites

The plan's ordering (§10.2) places this proposal after these, all landed: the Phase 0 pod identity hardening (proposals 0088 and 0089, the `SO_PEERCRED` half of F-4.7.25, F-4.7.30) and R6; proposals 0078 and 0079; proposals 0090, 0091, and 0092; R4 and the credential fail-open fix (F-4.9.36); and proposals P1 and P5 of the terminal-transition follow-ups, which are proposals 0093 and 0094. Proposals 0084, 0071, and 0096 and the pinned-idle and recycling follow-ups come after it.

## Validated premises

The draft of 2026-10-01 validated its premises against a tree in which 0079 was still a draft. The re-baseline of 2026-10-22 re-verified each premise this file states against the tree at `f52fc9a49` (the Evidence list above). The review run re-runs the premise lenses.
