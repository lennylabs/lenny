# Problem: Scope the coordination generation to the session

## 1. Problem

### 1.1 The specification scopes the generation per session

`spec/10` §10.1.1 states that each session row carries a `coordination_generation` counter and that a
replica taking over coordination increments it. §10.1.2 step 1 is a compare-and-swap of the form
`UPDATE sessions SET coordination_generation = $expected_generation + 1 WHERE id = $session_id AND
tenant_id = $tenant_id AND coordination_generation = $expected_generation`. The counter is a column on the
`sessions` table, one value per session.

The gateway sends it per session. `Client.CoordinatorFence`
(`pkg/gateway/runtime/adapterclient/coordinatorfence.go:48`) takes a session identifier and a generation
and puts both on the request.

### 1.2 The adapter stores it per pod

`Server` holds one `coord coordinationState` field (`pkg/adapter/server.go:302`). `coordinationState`
(`pkg/adapter/coordination.go:25`) carries a single `lastFenced int64` for the whole adapter process.

`CoordinatorFence` (`pkg/adapter/coordination.go:84`) reads the session identifier, verifies the pod is
running that session, takes `coord.mu`, and compares the incoming generation against that one pod-wide
value. It rejects `gen <= lastFenced` with `FailedPrecondition` and a `coordinator_handoff_stale` detail,
treats `gen > lastFenced+1` as a gap, records the new value otherwise, and calls `exitHoldState()`.

The doc comment on `CoordinatorFenceRequest.coordination_generation`
(`schemas/lenny-adapter.proto:1449-1451`) states that the generation is "Strictly monotonic on the pod
side per session". It is monotonic per pod. The comment describes the Postgres design rather than the
adapter's.

### 1.3 What breaks on a concurrent pod

A pod hosts session A at generation 5 and session B at generation 3.

1. A's coordinator hands off, mints 6, and fences the pod. `lastFenced` becomes 6.
2. B's coordinator hands off, mints 4, and fences the same pod. The handler compares 4 against 6 and
   rejects it as stale.
3. B's acquiring replica takes the §10.1.5 stale-replica path. It receives the `FailedPrecondition`
   rejection, re-reads `coordination_generation`, finds that the value has not advanced, relinquishes the
   lease, and returns `ErrRelinquished` (`pkg/gateway/coordination/coordfence/coordfence.go:164-179`,
   `:195-200`), so the sweeper's re-adoption or the client-driven resume aborts.

B then has no coordinator on that pod, and §10.1.2 step 2 bars every operational RPC until a fence
acknowledges (`spec/10_gateway-internals.md:38`), so B is stalled while that holds. The sweeper released the
lease and recorded a per-session adoption backoff, and it retakes the session on a later sweep
(`pkg/gateway/coordination/coordination/coordination.go:399-416`); on the resume path the row is held in
`awaiting_client_action` so the client's `POST /resume` retry drives the next attempt
(`pkg/gateway/sessionserver/start.go:3668-3672`). Every takeover bumps B's own generation by one
(`pkg/gateway/coordination/coordination/coordination.go:463-468`), so B climbs past the value the co-tenant
fenced after a bounded number of cycles, and each cycle increments `lenny_coordinator_handoff_stale_total`
and `lenny_coordinator_fence_relinquished_total`. Four further consequences follow from the same field:

**The drain barrier is rejected, so the drain checkpoint runs unquiesced.** `CheckpointBarrier`
(`pkg/adapter/coordination.go:211`) shares the gate and requires the supplied generation to equal
`lastFenced` exactly. A barrier for B is rejected. The checkpoint itself survives the rejection: the
gateway's barrier dispatcher opens the `Checkpoint` stream for the session concurrently with the
`CheckpointBarrier` RPC and finalises the manifest row itself
(`spec/10_gateway-internals.md:185`, `pkg/gateway/coordination/barrier/barrier.go:209-232`), and the
BarrierAck-timeout path finalises the intent row with `manifest_reason = "timeout"`
(`spec/10_gateway-internals.md:190`). What the rejection costs is the quiescence: the adapter never stops
dispatching tool calls, so the capture runs against a moving workspace. It also drops the barrier record,
because the `barrier_id`, `checkpoint_ref`, and `workspace_recovery_fraction` upsert runs only on an
acknowledged barrier (`pkg/gateway/coordination/barrier/barrier.go:237-246`), and it pushes the session into
the post-barrier per-session eviction checkpoint, which captures it a second time because the barrier did not
acknowledge (`pkg/gateway/podlifecycle/prestop/prestop.go:390-397`).

**Gap detection fires on a handoff that skipped nothing.** A session whose generation jumps past the pod's
view of another session's generation looks like a skipped generation, so the adapter logs
`coordinator_generation_gap` and reports `gap_detected` on the fence response
(`pkg/adapter/coordination.go:108-118`). The §10.1.2 cancel-and-reset the event announces is a requirement
the adapter records as unimplemented (`pkg/adapter/coordination.go:112-113`), and the gap branch records the
new value on the same path a non-gap fence takes (`:119-121`), so what the defect produces today is a false
split-brain signal rather than a discarded state.

**The coordinator-lost record attributes one session's generation to every session a hold terminates.**
`holdState` (`pkg/adapter/holdstate.go:39-44`) carries `mu`, `active`, `timer`, and `gen`, and it names no
session: its unit is the pod, and the set it terminates is read from the slot registry when the timeout
fires rather than recorded at arming (`pkg/adapter/holdstate.go:107-112`, `:192`). A pod-level exit on any
session's fence is therefore not the defect on this path. The defect is that the single generation captured
at arming from the pod-wide accessor (`pkg/adapter/holdstate.go:119`) is stamped on every session the
timeout terminates, in the `coordinator_lost` log line and in the on-disk post-mortem record
(`pkg/adapter/holdstate.go:225-229`, `:283-296`), so on a pod holding sessions at different generations one
session's generation is recorded against all of them.

**The counter that should surface this instead misleads.** `lenny_coordinator_handoff_stale_total`
increments on legitimate handoffs, so the metric the specification designates for detecting split-brain
reports healthy ones.

### 1.4 What kept this out of sight, and why it is now exposed

Until proposal 0073 landed, `CoordinatorFence` called `checkSession`, which read the pod-global
`Server.sessionID`. Only `claimSession` and `claimSessionForConfigure` wrote that field, and a concurrent
pod's `StartSession` returned early into `startSessionSlot` (`pkg/adapter/slotsession.go`), which recorded
the session on the slot-registry entry and never touched `Server.sessionID`. On a concurrent pod the guard
therefore failed first, the fence never reached the pod-wide counter, and coordinator handoff was already
broken on those pods while **failing closed**.

Proposal 0073 merged `checkSession` and `checkSlotSession` into a single `checkSessionBound`
(`pkg/adapter/slotsession.go:267`) resolving through the slot registry, and removed the `Server.sessionID`
field the old guard read. `CoordinatorFence` now calls `checkSessionBound` (`pkg/adapter/coordination.go:89`),
so the guard is repaired on every pod. 0073 §1.9 records this consequence for the five handlers that called
`checkSession` with no slot branch, of which `CoordinatorFence` and `CheckpointBarrier` are two. The
repaired guard is correct and necessary. Its effect here is to unmask this defect: the fence no longer fails
closed and instead succeeds against a pod-wide counter.

That masking is gone as of 0073's implementation, so the transition below has already happened in the tree
rather than being a consequence this proposal anticipates.

The transition is from *fails closed* to *succeeds incorrectly*. On availability that is an improvement. On
correctness it is a regression, and it is the kind that surfaces as an unexplained stuck session rather
than as an error.
