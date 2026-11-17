---
code: SESAREA
type: index
area: sessions
summary: >-
  Lists the capabilities that govern a session from creation to its end, including suspension, resume, checkpoints, derive, and isolation between sessions on one pod.
---

# Sessions

The capabilities in this area govern one session from its creation to its end. A session is created on a claimed pod, runs turns, may suspend and resume, is checkpointed and restored, may be derived into a new session, and ends with a seal. Sessions that share a pod are isolated in slots, and a pod is scrubbed before another session reuses it.

## Reading paths

- To change what states a session takes and when, read [Session states](session-lifecycle/session-states.md), then [Timeouts](session-lifecycle/timeouts.md).
- To change how a session starts on a pod, read [Session creation](session-lifecycle/session-creation.md), then [Pod claims](../runtimes-and-pools/pod-claims.md), then [Materialization](../workspaces-and-files/materialization.md).
- To change what survives a pod failure, read [Retry and resume](retry-and-resume.md), then [Checkpoint capture](checkpoints/checkpoint-capture.md), then [Restore](checkpoints/restore.md).
- To change what one session on a shared pod can see of another, read [Session isolation](session-isolation.md), then [Slot assignment and failure](slot-assignment-and-failure.md), then [Pod reuse and scrub](pod-reuse-and-scrub.md).

## Capabilities

| Page | Code | Summary |
| :-- | :-- | :-- |
| [Session lifecycle](session-lifecycle/index.md) | SESLC | Lists the pages of the session lifecycle: states, creation, suspension and dormancy, the end of a session, and timeouts. |
| [Retry and resume](retry-and-resume.md) | RESUME | States the retry policy after a pod failure, session generations, the resume flow, and the states a session takes when retries run out. |
| [Checkpoints](checkpoints/index.md) | CKIDX | Lists the checkpoint pages: capture, storage and retention, and restore. |
| [Derive](derive.md) | SDERIVE | Starts a session from an earlier one at its start or at its latest checkpoint, with the copy and restore rules and the options a derive takes. |
| [Setup commands](setup-commands.md) | SETUP | States the setup commands a runtime and a plan declare, their policy, ordering, limits, and failure. |
| [Session isolation](session-isolation.md) | SLOT | States the isolation between sessions that share a pod: slots, slot identity, slot release, the per-slot resource and credential scope, and slot failure. |
| [Pod reuse and scrub](pod-reuse-and-scrub.md) | SCRUB | States when a pod may serve another session, the scrub that runs between sessions, the reuse prohibitions, and the acknowledgements weak reuse requires. |
| [Slot assignment and failure](slot-assignment-and-failure.md) | SLOTOPS | States how a session is assigned a slot atomically, slot liveness and release, slot failure and retry, and the rehydration of slots after recovery. |
| [Scrub procedure](scrub-procedure.md) | SCRUBPROC | States the scrub procedure step by step, the supervised scrub sequence, what the scrub reaches, the sweep, and the Kata variant. |

## Components that carry out this area

- [Gateway](../../components/gateway/index.md): The gateway as a unit: responsibilities, the subsystem list, replicas, disruption budget, probes, ServiceAccount, RBAC, NetworkPolicy, process settings, and metrics. The slice wrote a stub that lists subsystems.
- [Session coordination](../../components/gateway/session-coordination.md): How gateway replicas coordinate a session: the coordination lease, handoff, hold state, stale replicas, and behavior when both stores fail.
- [Adapter](../../components/agent-pod/adapter.md): The adapter container: startup sequence, the slot registry, gateway and runtime connections, health checks, process settings, and metrics.
- [Runtime supervisor](../../components/agent-pod/runtime-supervisor.md): The slot containers and the runtime supervisor that runs as PID 1 in each: process launch and end, the sweep, the file-descriptor broker, and the materialization helper.
- [Warm pool controller](../../components/warm-pool-controller.md): The WarmPoolController: pod creation and status, leader election, failover, API and etcd load, and metrics.

## Interfaces this area exposes

- [Sessions](../../interfaces/client-api/sessions.md): Session operations: create, start, get, list, interrupt, terminate, delete, derive, and the states and errors each returns.
- [Gateway to pod](../../interfaces/channels/gateway-to-pod.md): The gateway-to-pod link and its channels, with the adapter RPC operations and request scope rules.
- [Handshake and capabilities](../../interfaces/runtime-protocol/handshake-and-capabilities.md): The handshake frames, the capabilities a runtime declares, and their dependencies.
- [Object store layout](../../interfaces/stored-schemas/object-store-layout.md): The object key layout and the checkpoint pack format.
