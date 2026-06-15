---
layout: default
title: "Execution Modes and Pod Lifecycle"
parent: Reference
nav_order: 8
description: The session and service execution modes, the sessionPolicy settings matrix and presets, the residual-state and isolation table, the recycle lifecycle, and a decision guide.
---

# Execution Modes and Pod Lifecycle

A runtime runs in one of two execution modes. `session` binds a managed session to a claimed pod and is parameterized by the `sessionPolicy` block. `service` routes each message to any ready replica with no claim and no conversation continuity. The pool scaling factors derive from the policy properties (see [Scaling](../operator-guide/scaling)).

---

## The sessionPolicy settings matrix

```yaml
executionMode: session            # session | service
sessionPolicy:                    # session mode only
  maxConcurrentSessions: 1        # > 1 requires acknowledgeProcessLevelIsolation
  recycle:
    enabled: false                # true requires acknowledgeBestEffortScrub
    maxSessionsPerPod: 50         # required when enabled; counts every session served
    maxPodUptimeSeconds: 86400    # optional
    maxScrubFailures: 3
    onScrubFailure: warn          # warn | fail
    scrubProfile: standard        # standard | vm-restart | in-place
    acknowledgeMicrovmResidualState: false  # required when scrubProfile: in-place
    allowCrossTenantReuse: false  # microvm-gated; never permitted when maxConcurrentSessions > 1
  cleanupCommands: []
  cleanupTimeoutSeconds: 60
  maxSessionRetries: 1            # crash re-dispatch budget; 2 total attempts; 0 disables
  maxSessionAgeSeconds: 7200
  maxClientIdleSeconds: 7200      # defaults to the effective maxSessionAgeSeconds
  slotRetries: 1
  onPoolExhausted: reject         # reject | queue
  maxQueueWaitSeconds: 30
```

`service` mode does not use `sessionPolicy`; it keeps a per-pod slot bound through the pool-level `maxConcurrent` field and routes by readiness.

---

## Presets

| Preset | `maxConcurrentSessions` | `recycle.enabled` | Behavior |
|:--|:--|:--|:--|
| One session per pod | 1 | `false` | Each pod is exclusive to one session and terminates when the session ends (default). |
| Pod reuse | 1 | `true` | The pod is recycled across sequential sessions of the same tenant with a whole-pod scrub at the occupancy-zero boundary. |
| Concurrent | N | `true` | The pod serves up to N simultaneous sessions in per-slot workspaces and recycles when occupancy reaches zero. |
| Bounded cohort | N | `false` | The pod serves N concurrent sessions, then terminates after the cohort drains. |

The acknowledgments, tenant pinning, `residualStateWarning`, and the scaling factors derive from `sessionPolicy` properties: `acknowledgeBestEffortScrub` is required when recycling is enabled, `acknowledgeProcessLevelIsolation` is required when concurrency exceeds one, `acknowledgeMicrovmResidualState` is required for `scrubProfile: in-place`, and tenant pinning is required when `maxConcurrentSessions > 1` or `recycle.enabled: true`.

---

## Residual state and isolation

The `sessionIsolationLevel` object in the `POST /v1/sessions` response reports the assigned pool's posture. `podReuse` and `residualStateWarning` are `true` whenever the pod serves more than one session over its lifetime.

| Configuration | Scrub at session release | `conversationContinuity` | Residual state across sessions |
|:--|:--|:--|:--|
| One session per pod | Pod terminated | `platform` | None; the pod is never reused |
| Pod reuse | Best-effort whole-pod scrub at occupancy zero | `platform` | DNS cache, TCP `TIME_WAIT`, page cache, residual processes may survive a best-effort scrub |
| Concurrent (`maxConcurrentSessions > 1`) | Per-slot cleanup at each release plus a whole-pod scrub at occupancy zero | `platform` | Concurrent slots share process namespace, `/tmp`, cgroup memory, and network stack |
| Service | None | `none` | Pods serve successive requests with no scrub; process space, network stack, `/tmp`, and page cache shared across same-tenant concurrent requests |

A client that requires strict isolation should reject a session whose response carries `residualStateWarning: true`.

---

## Recycle lifecycle

When occupancy reaches zero on a recycling pod, the gateway patches the pod's `SandboxClaim` to `recycling`, the adapter runs the whole-pod scrub, and the recycle disposition decides whether to hold the pod or retire it. On a preConnect pool the SDK re-warm runs after a successful scrub before the claim enters `reserved`.

![Recycle lifecycle: claimed, recycling whole-pod scrub, sdk_connecting SDK re-warm, reserved tenant hold, then claimed again on a same-tenant rebind or idle on hold expiry.](../assets/diagrams/recycle-lifecycle.svg)

<!--
ASCII fallback for the diagram above (recycle-lifecycle):

  claimed ===(occ. zero)==> recycling ===(scrub ok)==> sdk_connecting ===(re-warm)==> reserved ===(expires)==> idle
                                                                                          |
                                                                                  (within TTL)
                                                                                          v
                                                                                       claimed (same-tenant rebind)

  On a non-preConnect pool the scrub success patches recycling directly to reserved with no SDK re-warm leg.
-->

The reserved hold extends the occupancy episode across an idle gap: a same-tenant session arriving within the deployment-level hold TTL (`gateway.claimHoldTTLSeconds`, default 10s) rebinds the pod with no acquisition round trip. A pod retires when it reaches `recycle.maxSessionsPerPod`, `recycle.maxScrubFailures`, or `recycle.maxPodUptimeSeconds`, when a session ends in failure or a crash, or when its host node is unschedulable. See the full machine in [State Machines](state-machines).

---

## Decision guide

- **Default to one session per pod.** Use it when each session must run in a clean, single-use pod. It carries no residual-state risk.
- **Choose pod reuse (`recycle.enabled: true`) for sequential throughput** when cold-start cost dominates and the workload tolerates a best-effort scrub between sessions. Set `recycle.maxSessionsPerPod` to bound reuse.
- **Choose concurrent sessions (`maxConcurrentSessions > 1`) for lightweight handlers** that share a pod's process space, accepting process-level isolation between same-tenant slots.
- **Choose service mode for stateless, high-throughput request handling** where each message is self-contained. A `multi_turn` runtime in service mode requires the client to re-inject context into each message.
- **Prefer the external connector model** over service mode when the workload is a long-lived MCP server that external clients connect to directly. Connectors carry gateway-managed OAuth and content-policy interception, which service mode does not.

For cross-tenant reuse, only the sequential-reuse path (`maxConcurrentSessions: 1`, `recycle.enabled: true`) with `isolationProfile: microvm` and `recycle.allowCrossTenantReuse: true` is permitted, and never on a `workspaceTier: T4` pool.
