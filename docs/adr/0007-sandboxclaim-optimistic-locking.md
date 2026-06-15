---
layout: default
title: "ADR-0007: SandboxClaim optimistic locking and failover fencing"
parent: "Architecture Decisions"
nav_order: 7
status: Accepted
date: 2026-05-17
deciders: "@maintainer"
tags:
  - controller
  - gateway
---

# ADR-0007: SandboxClaim optimistic locking and failover fencing

## Status

**Accepted** (amended 2026-06-15 for per-pod claim granularity).

## Amendment: per-pod claim granularity

The session-service-execution-model proposal changes the `SandboxClaim` from a per-session binding to a per-pod occupancy claim with the deterministic name `claim-<podName>`. The core decision of this ADR is unchanged: at-most-one-active-claim fencing still rests on Kubernetes optimistic concurrency. The mechanics shift as follows:

- The claim's spec is immutable after `CREATE`. Pod acquisition still races on `CREATE`, and the deterministic per-pod name resolves the race between gateway replicas exactly as the per-session `CREATE` did.
- The `lenny-sandboxclaim-guard` webhook now intercepts `CREATE` only and rejects a second non-terminal claim for the same pod. It no longer gates `PATCH` or `PUT`: a `Sandbox.status.phase` accept-set cannot serialize the binding-state writes, because the occupancy projection sets the Sandbox to `claimed` from the claim's own `bound` binding state, so the first `bound` status patch lands while the referenced Sandbox is still `idle`.
- Binding-state transitions (`bound`, `recycling`, `reserved`, and the terminal `released`/`failed`) are writes to the `SandboxClaim.status` subresource, serialized by the optimistic-concurrency UID and `resourceVersion` preconditions this ADR specifies. The reserved-hold expiry `DELETE` carries the UID and the `resourceVersion` observed at the `reserved` patch, so a same-tenant rebind that lands first wins the race and the expiry aborts on a precondition failure.

The fencing rationale below is therefore retained; the references to `PATCH`/`PUT` admission gating describe the superseded per-session model.

## Context and problem statement

The gateway binds a warm pod to a session by creating and updating a `SandboxClaim` resource that references a `Sandbox` (the agent pod). Several gateway replicas can attempt to claim the same idle `Sandbox` at the same time, and exactly one must win. The WarmPoolController runs as a Deployment with Kubernetes Lease-based leader election. On a clean shutdown the outgoing leader releases its lease immediately, but on a crash the new leader cannot acquire the lease until the old one expires, which gives a worst-case failover window of `leaseDuration + renewDeadline = 25s`. During that window an in-flight write from the dead leader can still reach the API server.

The platform needs a fencing mechanism that guarantees at most one active claim per `Sandbox`, including across the 25s failover window, without routing pod-to-session binding through the controller. Phase 1 cannot implement the `PodLifecycleManager.ClaimPod` contract until this is settled, so the decision is a Phase 1 blocking prerequisite per [§4.6.1](https://github.com/lennylabs/lenny/blob/main/spec/04_system-components.md).

## Decision drivers

- At most one gateway replica may bind a given `Sandbox` to a session, with no double-claim under any timing.
- Claim resolution must stay off the controller hot path so there is no single-writer bottleneck; binding is resolved at the API-server level.
- Fencing must hold across the 25s crash-case leader-election failover window.
- A Kubernetes-native primitive is preferred over a custom generation field or an external fencing token when it is sufficient.

## Considered options

- Kubernetes optimistic locking — `resourceVersion`-guarded compare-and-swap on the claim resources.
- A custom generation or epoch field stamped by the current controller leader and checked on every write.
- A lease-serialized single-writer claim path that routes every claim through the controller leader.

## Decision outcome

**Chosen: Kubernetes optimistic locking.**

`ClaimPod` implementations claim a pod with a `resourceVersion`-guarded compare-and-swap loop. The gateway reads the current `Sandbox`, attempts the bound write, and on HTTP 409 Conflict re-reads the resource and retries against a different idle pod. An old leader's in-flight `PATCH` or `PUT` carries the `resourceVersion` it observed before it died; if any other writer has since touched the resource, the API server rejects the stale write with HTTP 409. The stale write cannot silently succeed: it either completes before any other writer touches the resource, in which case it is not a double-claim, or it arrives afterward and is rejected. Optimistic locking therefore acts as the fencing mechanism across the failover window, and no separate generation field or fencing token is required.

The `lenny-sandboxclaim-guard` `ValidatingAdmissionWebhook` backs this as defense in depth. It intercepts every `CREATE`, `PATCH`, and `PUT` on `SandboxClaim` resources in agent namespaces and reads the authoritative state from the referenced `Sandbox.status.phase`. A `CREATE` is rejected when a non-terminal claim already exists for the target `Sandbox`; a `PATCH` or `PUT` is rejected when the referenced `Sandbox` is not in phase `claimed`. The webhook is deployed with `failurePolicy: Fail`, so a webhook outage blocks new claims rather than admitting them unchecked. Because admission evaluates `Sandbox.status.phase` as persisted in etcd, a double-claim is rejected even when two writers race with the same `resourceVersion`. The `CREATE`-time check is also what makes the Postgres fallback claim path safe, since that path creates a `SandboxClaim` outside the normal API-server claim flow.

### Consequences

- **Positive.** Claims resolve at the API-server level with no controller involvement and no single-writer bottleneck. The fencing guarantee reuses a Kubernetes primitive that every cluster already provides, so there is no custom epoch bookkeeping to maintain. The admission webhook closes the residual race in the narrow window before a new leader first touches a resource.
- **Negative.** Correctness depends on the API server enforcing `resourceVersion` conflict semantics, so the behavior is verified by tests rather than assumed. Under high contention the compare-and-swap loop retries, which the gateway must bound and instrument.
- **Neutral.** The `lenny_sandboxclaim_guard_rejections_total` counter exposes admission rejections; a non-zero rate during normal operation indicates a claim-path bug rather than an attack.

### Confirmation

Two tests verify the hypothesis that optimistic locking is sufficient fencing. They are stubbed in Phase 0 and become executable as their backing components land.

1. **Concurrent-claim integration test.** Race two concurrent claim attempts against the same `Sandbox` with `resourceVersion`-guarded updates and confirm that exactly one succeeds while the loser receives HTTP 409 Conflict.
2. **Leader-kill chaos test.** Kill the WarmPoolController leader mid-claim at high concurrency (at least 50 concurrent claim goroutines against a pool of 10 pods) and verify zero double-claims in the resulting `SandboxClaim` set. The chaos test runs against the WarmPoolController binary that ships in Phase 3.

If either test shows that optimistic locking does not fence correctly, a superseding ADR specifies the compensating design (for example, a generation-stamped claim record) before the claim path is considered complete.

## Pros and cons of the options

### Kubernetes optimistic locking

- Good because the fencing guarantee reuses the API server's `resourceVersion` conflict check, which every cluster already provides.
- Good because claims resolve without the controller, so there is no single-writer bottleneck on the hot path.
- Good because the same 409 path handles both gateway-versus-gateway races and stale writes from a failed leader.
- Bad because correctness depends on API-server semantics that must be verified by test rather than assumed.

### Custom generation or epoch field

- Good because an explicit epoch makes the fencing intent visible in the resource.
- Bad because it duplicates a guarantee that `resourceVersion` already provides.
- Bad because the controller must stamp and rotate the epoch, which adds controller logic and a failure mode of its own.

### Lease-serialized single-writer claim path

- Good because a single writer removes claim races by construction.
- Bad because it puts the controller on the claim hot path and reintroduces the single-writer bottleneck the design avoids.
- Bad because claim latency then depends on controller availability, including the 25s failover window.

## More information

- Spec references: [§4.6.1](https://github.com/lennylabs/lenny/blob/main/spec/04_system-components.md), [§6.2](https://github.com/lennylabs/lenny/blob/main/spec/06_warm-pod-model.md), [§10.1](https://github.com/lennylabs/lenny/blob/main/spec/10_gateway-internals.md)
- Related ADRs: ADR-0004 (`kubernetes-sigs/agent-sandbox` for pod lifecycle CRDs)
- [Architecture Decisions index](./)
