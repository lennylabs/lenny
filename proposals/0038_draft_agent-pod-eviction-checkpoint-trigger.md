# Proposal: Agent-pod eviction-checkpoint trigger and its prerequisites

- **Status:** **Early draft. Not ready for review.** Blocked on prerequisites (§3) and on the checkpoint data path settling in the successor to `0036_withdrawn_gateway-mediated-checkpoint-relay.md`.
- **Date:** 2026-07-14.
- **Scope:** The §4.4 eviction checkpoint is never driven when an individual agent pod is terminated by a node drain, the kubelet Eviction API, a cluster upgrade, or a direct pod delete. This draft records the design space and the prerequisites that must land before the trigger can be built. It carries no staged changes yet.

This document records analysis. It stages no spec, code, or doc change.

## 1. Problem

`spec/04_system-components.md:489` states that "the primary protection against voluntary disruption (node drains, cluster upgrades) is a **preStop hook** on every agent pod that triggers a checkpoint via the runtime adapter's `Checkpoint` RPC before allowing termination." No code path implements it.

`pkg/checkpoint/checkpoint.go` defines `checkpoint.TriggerEviction` with its own retry budget (`{500ms, 5s, 30s}` from `RetryBudgetFor`), and no gateway code path selects it. `pkg/gateway/checkpoint/checkpointer/checkpointer.go:343` (`triggerForSource`) returns `checkpoint.TriggerPeriodic` on every arm. The only live checkpoint drivers are the periodic loop and the gateway replica's own preStop barrier fan-out, and neither fires when an individual agent pod is evicted.

The consequence is bounded rather than catastrophic: a session on an evicted agent pod falls back to its last periodic checkpoint, so it loses at most `periodicCheckpointIntervalSeconds` (default 600s) of workspace changes. It is nonetheless the §4.6.1 voluntary-disruption guarantee going unmet.

## 2. Why this is not a small change

An attempt to fold this trigger into the checkpoint data-path proposal was made and abandoned. Adversarial review surfaced five independent blockers, each of which is a product defect in its own right and none of which is about the eviction trigger as such. They are recorded here because they are the actual work.

### (A) The adapter cannot checkpoint a slot

`CheckpointRequest` (`schemas/lenny-adapter.proto:1021`) carries `session_id` and `deadline_ms` and no `slot_id`. `Server.checkpointRoots()` (`pkg/adapter/checkpoint.go:313`) bundles the pod-global `WorkspaceRoot` and `SessionsRoot`, not a slot-scoped path. On a pool with `sessionPolicy.maxConcurrentSessions > 1`, where each slot owns `/workspace/slots/{slotId}/current/` (`spec/05_runtime-registry-and-pool-model.md` §5.2), the RPC has no way to name the slot to checkpoint, and its session gate cannot pass.

**Concurrent-session pods therefore cannot be checkpointed at all today, on any trigger.** This is not specific to eviction and is arguably the highest-value item in this draft.

### (B) The preStop hook cannot run on the runtime container

`preStopDrainHook` (`pkg/controller/sandbox/podspec/podspec.go:1229`) execs `["lenny-adapter", "prestop", ...]` and is attached to **both** the adapter container and the runtime container (`podspec.go:560`, `:645`). The runtime container runs a third-party image that does not contain the `lenny-adapter` binary, so the hook cannot execute there. It would also signal PID 1 of its own container, which in the runtime container is the agent process the hook exists to keep alive.

### (C) A Full-level checkpoint cannot quiesce during eviction

In the default sidecar model the kubelet SIGTERMs every container at once. The runtime container is terminating from t=0, so there is nobody left to answer the `checkpoint_request` / `checkpoint_ready` handshake that `spec/04_system-components.md` §4.4 makes "the only mechanism that produces consistent checkpoints under all isolation profiles." An eviction checkpoint on a Full-level runtime is therefore best-effort at best, and the spec's consistency tagging needs to say so.

### (D) The terminating pod's coordinator may be a different gateway replica

Any pod-to-gateway termination signal lands on an arbitrary gateway replica, but only the replica holding the session's coordinator lease can drive the checkpoint. `leasestore.Acquire` returns `ErrHeld` against a live holder and there is no compare-and-steal primitive, so a signal arriving at a non-coordinating replica has no way to either forward to the coordinator or take the lease. Without a resolution step, most agent-pod evictions degrade to the minimal-state fallback.

### (E) The termination-grace budget does not close

The agent pod's `terminationGracePeriodSeconds` is 120s. `spec/10_gateway-internals.md:108` requires the 90s Stage-2 tier cap whenever there is no prior checkpoint to size against. 90s (tier cap) + 60s (the `spec/04:278` Postgres fallback write) + 10s (preStop drain margin) = 160s > 120s. The 240s and 300s figures that §4.4 uses to justify those budgets are the **gateway** pod's, not the agent pod's.

## 3. Prerequisites

The trigger is buildable once these land. Each is separable and each is worth landing on its own merits.

1. **Slot-aware checkpointing.** `slot_id` on `CheckpointRequest`, slot-scoped `checkpointRoots()`, and a slot-scoped session gate. Unblocks (A). Independent of eviction.
2. **Coordinator resolution for pod-originated signals.** Either a forward-to-coordinator hop or a fenced lease-steal primitive on `LeaseStore` (noting the interface has multiple implementations and production wires a `*Failover`). Unblocks (D).
3. **Agent-pod grace-period arithmetic.** A CRD/webhook rule relating `maxConcurrentSessions`, the Stage-2 tier cap, the Postgres fallback budget, and the preStop drain margin to `terminationGracePeriodSeconds`, plus a defensible default. Unblocks (E).
4. **A container-termination story for the runtime container.** Either a preStop hook the runtime image can actually run, or a design that does not place one there. Unblocks (B), and constrains (C).

## 4. Design space for the trigger itself

Two seams, to be decided once the prerequisites land.

**Pod-to-gateway termination signal.** The agent pod's existing preStop hook notifies the gateway, which drives the `Checkpoint` RPC back into the pod's adapter with `TriggerEviction`. Covers node drain, kubelet eviction, and direct delete uniformly, and needs no pod RBAC. Costs a new RPC, a pod-local query surface the adapter does not have (`preStopDeps` is `{signal, alive, sleep, logf}`, `cmd/lenny-adapter/prestop.go:31-36`), and the coordinator-resolution hop from (D).

**Gateway-side pod informer.** The gateway watches agent pods for `deletionTimestamp` and drives the checkpoint. Note that the "this needs new RBAC" objection is **false**: the gateway ClusterRole already grants `pods get/list/watch` cluster-wide. The real costs are per-replica ownership and dedup across a watch that every replica receives, and a race against the kubelet's grace period, since the informer learns of the deletion asynchronously.

Whichever wins, `triggerForSource` must be fixed so `TriggerEviction` reaches the `lenny_checkpoint_duration_seconds` trigger label and the eviction retry budget applies, and the phantom `snapshotWithTrigger` doc comment at `checkpointer.go:337-342` must go.

## 5. Findings this would close

`T-4.4.14` (the gateway never drives the eviction trigger), `T-4.6.4` (node-drain checkpoint-before-eviction), and the tier-5 half of `T-JRN.9` (the checkpoint-then-resume journey on Kind).
