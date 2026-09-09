# Summary: A failed session bind leaves a stale adapter slot registry entry

This proposal stages its changes. It does not apply them. Nothing under `spec/`,
`docs/`, `pkg/`, `charts/`, or `schemas/` is modified by this document.

## Summary

**Problem statement.** The adapter creates a slot registry entry during the first
workspace-preparation RPC, well before `StartSession`, and binds it to the session at
`AssignCredentials`. When one of the five post-connection bind stages fails, the gateway
closes the adapter connection and calls `ReleaseSlotReservation`, which touches the
SandboxClaim and the Redis slot counter and issues no adapter RPC. Closing a connection
removes nothing from the registry, and no other adapter path removes an entry on a
gateway-side bind failure. On a pod that still holds a co-tenant at the moment of release,
or on a recycling pool, the entry survives for the life of the pod. It holds the §28.5.3
slot count above one, so the incumbent session's unaddressed runtime output is rejected
from that moment on; it holds `boundRemains` true, so the §15.4.2 drain is suppressed on
every later teardown; and in its third class it leaves the shared runtime running for a
session the gateway has abandoned. The specification does not oblige the gateway to
compensate: the §4.7.9 bind sequence has no failure branch, §6.2's per-slot sub-state
machine has no edge into `slot_cleanup` from either pre-`running` state, and §4.7 states
the current defect as the contract by gating the whole teardown on the binding.

**What changes.**

- The specification states the reclaim obligation. §7.1 gains the failed-bind pod-side
  reclaim duty and its `leaked` disposition as a paragraph of its own, binding the creation,
  start, and re-attach bind attempts alike, with §7.3's resume flow, §6.2's mid-resume cancel
  edge, and §4.7.9 step 5 pointing at it, §7.2's mid-resume snapshot-close sequence running
  the reclaim before it releases the replacement pod, and §5.2's `**Max retries:**` bullet
  stating where a retry after an unacknowledged reclaim lands, beside the saturation and
  health conditions it already lists. §4.1
  and the §4.7 `Shutdown` row separate the slot release from the runtime teardown and give
  each its own precondition, §5.2's slot-cleanup action list names the per-slot credential
  directory and the §4.9 timer cancellation while its scrub model covers the cleanup of a
  bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches
  `running`, withholds that cleanup's outcome report, and gives each session release at most
  one cleanup-outcome report, and §6.2 gains the `receiving_uploads → slot_cleanup` edge.
- The adapter's `Shutdown` releases the slot for any entry the call removed, runs the
  runtime teardown only for a session whose start it has admitted, and files a
  cleanup-outcome report only for a session the pod's shared runtime process was given
  (`pkg/adapter/session.go`).
- A start confirms its slot survived before recording the runtime as holding the session, so
  a reclaim that lands after the start's claim is caught and the start rolls back
  (`pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go`).
- The gateway sends the compensating `Shutdown` at each post-connection bind failure and at
  a failed resume, on the connection the failed stage still holds, and carries the outcome
  as a `leaked` disposition into the reservation release
  (`pkg/gateway/podlifecycle/podsession/`).
- One accounting helper serves both concurrent bind paths, so the create-time reserved path
  reaches the §5.2 unhealthy threshold it has never reached
  (`pkg/gateway/sessionserver/start.go`).
- The per-slot edge list in `pkg/sandbox/slotstate` and the per-slot table in
  `docs/reference/state-machines.md` follow the §6.2 edit.

**Decisions.**

- The remedy is the `Shutdown` RPC the gateway already has, sent on the connection it
  already holds at the failure site. No new RPC, no proto edit, no timer, no new persisted or
  pod-side state, and no new metric. The one new field is `ExcludePod` on the in-process bind
  request structs, which carries §5.2's placement constraint for a pod holding an
  unacknowledged reclaim into placement and reaches no wire format.
- `schemas/lenny-adapter.proto` is never opened. The gateway-runtime-comms remediation
  programme's rule S-2 reserves the single proto edit to step R1b.
- The adapter's teardown gate moves from the binding to `started`, and the slot release runs
  for any entry the call removed. This is forced rather than preferred: see "Watch out for".
- The two gates read two predicates, because the specification states two moments. The
  runtime teardown gates on `started`, whose moment is the admitted start:
  `claimSessionSlotUnderLock` sets `started` inside its critical section before
  `Runtime.Start`, so the gate fails closed on the start-in-flight race. The cleanup-outcome
  report gates on `runtimeLive` membership, whose moment is the later one §6.2's `running`
  and the pod's served-session count take, and `noteRuntimeStarted` records it after
  `Runtime.Start` returns.
- The compensation runs on a detached context (`context.WithoutCancel`). The third residue
  class arises precisely when the caller's context expired during `StartSession`, so a
  compensation on that context would fail in the one case that leaves a runtime running.
- The compensation is best-effort, and its own failure is counted. An unacknowledged reclaim
  sets the `leaked` disposition, which `SlotClaimer.ReleaseSlot` already implements, so the
  pod retires through the shipped §5.2 threshold instead of accumulating residue.
- No downstream predicate is re-scoped. `slotCount` keeps counting registered-but-unbound
  entries, `claimPodMCPStartLocked` keeps its raw entry-count guard, and `boundRemains` keeps
  reading the binding. The residue stops existing; the rules it was breaking stay as they are.
- The compensation's budget is §5.2's per-slot cleanup timeout,
  `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds. `SlotBindRequest` already
  carries both inputs, and an unset `cleanupTimeoutSeconds` yields the 5s floor. The
  specification states no number for the gateway-side deadline; the code cites §5.2.
- The proposal does not claim to fix "the pod's intra-pod MCP surface never arms again".
  `claimPodMCPStartLocked` gates on `len(s.slots) != 1`, and the claimant's own entry is
  inserted immediately above that guard, so two ordinary interleaved binds on a healthy
  concurrent pod each observe two entries and neither arms, with no failed bind anywhere.
  The residue makes that outcome permanent rather than causing it.
- File-collision discipline: the adapter edits land in `session.go` and
  `runtimegeneration.go`. `pkg/adapter/slotsession.go` is touched only for doc comments and
  `pkg/adapter/slot.go` not at all, because a later position covering proposal 0080 rewrites
  both. No field is added to `slotState`.

**Watch out for.**

- Sending today's `Shutdown` at the credential-assignment stage would brick the pod.
  `SocketRuntimeProcess.Close` returns early only on `!p.connected`, then calls
  `releaseActiveLocked`, which deletes an absent key and returns `len(p.active) == 0`. On a
  pod whose active set is empty it therefore closes the shared connection, kills the spawned
  child, and calls `p.listener.Close()` on a listener bound once in `NewSocketRuntimeProcess`
  and never rebound (`pkg/adapter/socketruntime.go:155-161`, `:441-467`). `InProcessRuntime.Close`
  and `MCPRuntime.Close` ignore the session identifier entirely and tear the runtime down on
  any call (`pkg/adapter/embedded.go:188-204`, `pkg/adapter/mcpruntime.go:266-291`). This is
  why the adapter gate must land before the gateway compensation.
- The ordering is fixed. The adapter gate lands first, after the spec steps. Landing the
  gateway half alone would send a full teardown, a §15.4.2 drain frame, a `Runtime.Close`,
  and a `sessionsServed` increment for a session that never ran.
- `Close`'s doc comment claims it is a no-op for a session not in the active set. That is
  false in one reachable state: `Interrupt` of the last active session closes the connection
  while leaving `connected` true and `conn` non-nil (`pkg/adapter/socketruntime.go:398-417`),
  after which any `Close` takes the last-close teardown. The adapter test set pins this; the comment is not
  corrected here.
- `stageWorkspace` issues `PrepareWorkspace` only when the plan carries uploads, so a
  workspace-preparation failure on an upload-free plan sends no RPC and leaves no adapter
  entry. The compensation is still sent, the adapter answers cleanly for a session it holds
  nothing for, and the failure is accounted transient. A test asserting "exactly one
  `Shutdown`" on that branch would pin over-sending as contract.
- `UnhealthyThreshold(2)` is 1, and `countsLocked` sums windowed failures with persistent
  leaks. At `maxConcurrentSessions: 2` a single cleanly released bind failure already drains
  the pod today, so a test that uses that concurrency cannot distinguish the leaked arm from
  the failed arm. The gateway accounting cases use `maxConcurrentSessions: 4`.
- `TestValidTransitions_spec_6_2` asserts an exact edge count and fatals on a length
  mismatch. The §6.2 edit and the `slotstate` edit must land together.
- `SlotID == SessionID` on every path, and §5.2 placement prefers a pod already hosting the
  tenant's slots. A retry therefore re-uses the identifier and can land on the pod whose
  reclaim may still be running `os.RemoveAll` outside `s.mu`. CODE-5 closes that window by
  carrying `ExcludePod` on the retry, so the attempt keeps its §5.2 retry and only the
  reclaiming pod is disqualified from it.

## Goals

- Remove the adapter slot registry entry, the per-slot tree, the per-slot credential file,
  and the armed §4.9 expiry timers that a failed bind leaves behind, in all three residue
  classes.
- Take the abandoned session back off the pod's shared runtime process in the class where
  the runtime was given it.
- Give the compensation a stated obligation in the specification, so the code traces to a
  section rather than to a convention.
- Make the compensation's own failure bounded and counted, on both concurrent bind paths,
  through the accounting the specification already states.

## Non-goals

- **An adapter-side pre-start slot lease.** A per-entry idle timer that reclaims a slot which
  has not reached `started` within a window. It is the only trigger that survives a gateway
  replica dying mid-bind with no gateway participation. It is rejected on a verified fact:
  `Binder.Prepare` calls `cl.Close()` on success and `Binder.Launch` reconnects later at
  client pace, so a prepared-but-unlaunched exclusive session sits bound, unstarted,
  credentialed, timer-armed, and connectionless, which is byte-for-byte the second residue
  class with no state-based discriminator from an abandoned concurrent bind. The lease would
  have to sit above §7.1's `maxCreatedStateTimeoutSeconds`, at which point the gateway's own
  created-state sweeper normally wins the race. It also needs an operator-tunable with no
  defensible value and a tombstone set on top, because a reclaim that deletes the workspace
  while a late `StartSession` can recreate an empty tree converts a leak into silent
  corruption.
- **Reclaiming a slot when the gRPC connection that created it dies.** Refuted by the same
  fact: connection close is the normal case rather than abandonment, and `ClaimSlot`
  deliberately closes between the handshake and `BindReservedSlot`. Connection-close reclaim
  would destroy live exclusive sessions.
- **A new adapter RPC (`AbortBind`, `ReleaseSlot`, `DiscardSlot`) or a new field on
  `ShutdownRequest`.** `Shutdown` is already addressed by the session identifier, which is
  also the slot identifier on every path, and its clause two already deregisters
  unconditionally. A second removal entry point would be a second lifetime to get wrong, and
  rule S-2 reserves the single proto edit to step R1b.
- **Branching the adapter on `ShutdownRequest.reason`.** It needs no proto edit and is the
  obvious shortcut. It makes the cleanup semantics depend on a free-form string the
  specification treats as an opaque cause, so a caller that omits or misspells it silently
  gets the wrong teardown, and the adapter would hold two sources of truth about whether a
  session ran. `st.started` is state the adapter already owns.
- **Re-scoping the downstream predicates instead of removing the entry.** Narrowing
  `slotCount`, `claimPodMCPStartLocked`, or `boundRemains` to exclude unbound or unstarted
  entries. `slotCount`'s inclusion of registered-but-unbound entries is deliberate §28.5.3
  fail-closed behaviour with its reason in its own comment,
  `TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2` pins the drain-gate half,
  and no predicate change reclaims the written credential file, the armed expiry timers, or
  the third class's running runtime.
- **Moving `boundRemains` from the binding to `started`.** It looks free once the residue is
  gone. A bound-but-unstarted co-tenant is a bind about to issue `StartSession` against the
  shared runtime, and draining there hands it a runtime that has been told to terminate. The
  gate fails closed on purpose.
- **Putting the compensating `Shutdown` inside `ReleaseSlotReservation`.** Every failure site
  closes the connection before returning, so this function would have to re-run
  `resolveSandbox`, `DialAdapter`, and `NegotiateVersion` on every failed bind, at the moment
  the pod is least trustworthy, including the connect-stage releases where no adapter entry
  was ever created.
- **A `ctx.Err()` rollback inside the adapter's `StartSession` after `Runtime.Start`
  succeeds.** The compensation is already the answer to the third class, and a response can
  be in flight and delivered after the check, so this would tear down a start whose success
  the gateway did observe.
- **A metric or an adapter event for the compensation's outcome.**
  `lenny_slot_failure_total` already labels the stages at exactly these call sites,
  `lenny_adapter_leaked_slots` and the `slothealth` ledger already carry the leak, and the
  compensation's failure is a log line on the same path as every other best-effort adapter
  teardown.
- **A hold-timeout reclaim of unstarted slots, and the §10.1.4 spec text for it.** The
  §10.1.4 hold-timeout bullet already delegates the slot disposition to the whole-pod
  connection-loss paragraph, which makes total connection loss a whole-pod failure and
  retires the pod, and §6.2 states that a pod that fails mid-session never re-enters the warm
  pool. The residue therefore sits on a pod with no future occupant. The path also cannot
  execute: `enterHoldState`'s only production caller is the `AdapterEvents` stream close, and
  no gateway code opens that stream. Both halves belong to remediation step R12, which builds
  the control-stream consumer and owns the gateway-side whole-pod-loss response.
- **A recycle-boundary sweep of the slot registry before the whole-pod scrub.** An
  unacknowledged reclaim is a `leaked` slot, and `SlotClaimer.ReleaseSlot` skips the counter
  decrement and returns no recycle signal for a leaked slot, so the residue class the sweep
  guards can never reach the recycle boundary through the gateway. §5.2 states the same
  invariant: retirement on `maxSessionsPerPod` is decoupled from the whole-pod scrub because
  a persistently leaked slot can hold occupancy above zero indefinitely. The sweep would also
  deregister a live bind's entry, because a pod in `recycling` at counter zero remains a
  placement candidate, and it would never call `noteRuntimeClosed`, leaving `runtimeLive`
  permanently non-empty for the started residue it swept. The one real hole it named, a lost
  `Resume` response, is closed by extending CODE-4 to `Binder.Resume` instead.
- **Widening the §5.2 whole-pod scrub to enumerate the registry as its source.** Its on-disk
  enumeration is deliberate and its own comment gives the reason: the residue it must reach
  belongs to a leaked slot whose registry entry is already gone.
- **Splitting the work by residue class into two proposals.** One map, one entry, one
  `delete`, one conditional. Two proposals would edit the same branch.
- **Landing the gateway half without the adapter half.** It would send a full teardown, a
  §15.4.2 drain frame, a `Runtime.Close`, and a `sessionsServed` increment for a session that
  never ran, and it would reach the listener-close hazard.
- **Fixing `claimPodMCPStartLocked`'s raw `len(s.slots) != 1` gate.** Named out of scope by
  the problem statement and independently reachable with no failed bind anywhere. It needs
  its own proposal.
- **Retuning the `ceil(maxConcurrentSessions/2)` unhealthy threshold.** Named out of scope.
  The accounting deliverable adds accounting where there is none and changes no threshold.
- **The exclusive path's abandoned-Prepare case**, where a session prepares and never calls
  Launch. Named out of scope by the problem statement; it has a different trigger and a
  different owner. The incidental consequence is worth recording rather than claiming: the
  adapter gate changes what happens when that path's `Shutdown` eventually arrives, giving it
  no full teardown and no cleanup-outcome report for a session that never ran.
- **Re-stating §28.5.3, §15.4.3, or §15.4.2 in the specification.** Their rules are correct as
  written. With the residue removed or the pod retired, none of those predicates is fed a
  residue. Naming them would state a rule change the code does not make and would invite a
  later reader to weaken a count that fails closed on purpose.

## Open decisions for human to make

The spec review loop routed these to a human. Each states the question, the ground the loop
derived, and whether the loop reached a recommendation.

1. **Should a pre-`running` cleanup that fails on the pod report a leaked outcome?** SPEC-3
   withholds the cleanup-outcome report for every cleanup that reclaims a slot before the pod's
   shared runtime process was given the session. A cleanup on that path that fails therefore
   never reports `leaked`, never counts toward §5.2's whole-pod replacement threshold, and never
   moves the leaked-slot gauge. The adapter discards that error today, so the rule suppresses no
   signal that currently exists. The alternative is to withhold only the `released` outcome and
   still report a failed pre-`running` cleanup as leaked. The loop derived no recommendation.
2. **Is `session_complete` the intended `terminate` reason for a reclaim?** The gateway maps its
   `slot_bind_failed` drain reason onto the §15.4.2 `terminate` frame's `session_complete` value,
   so a reclaim of a session whose start was admitted signals the runtime with the same reason as
   an ordinary session end. The wire stays valid and no enum value is missing. A bound-but-unstarted
   reclaim sends no frame at all under the staged design, so the question is scoped to a reclaim of
   a started session. The loop derived no recommendation, and states that the reason enum should not
   be reopened until this is answered.
3. **Does §29.4's session-end step 12 need a condition of its own?** Step 12 states unconditionally
   that the adapter closes the session runtime, and SPEC-1 gates the runtime teardown on a session
   whose start the adapter has admitted. Two readings survive the evidence. Under the first, §29.4's
   preconditions paragraph scopes the whole trace to a session whose runtime is running, step 12
   stays true, and nothing is owed. Under the second, step 10 and §15.1's precondition table admit a
   terminate at `ready`, which is bound-but-unstarted, so SPEC-1 falsifies step 12 and this proposal
   creates the site. Two pairs of round-7 lenses split on it, and answering it decides whether SPEC-1
   gains a fifth edit site.
4. **Does an incomplete §29 enumeration count as disagreement with the spec?** §29's preamble
   subordinates a trace only where it disagrees, and §29 item 12's `Shutdown` trigger enumeration
   gains no fourth trigger from SPEC-2. Whether an enumeration that is incomplete rather than wrong
   counts as disagreement decides whether item 12 is edited here. The question returned in several
   rounds; the answer wants recording once rather than re-deriving.
5. **Does §4.1 retire its two-operation sentence?** SPEC-1's replacement sentence names three
   operations, the slot release, the runtime teardown, and the whole-pod scrub, while the retained
   second sentence still reads "The per-slot teardown and the whole-pod teardown are the same
   operation on the same address" and names two the new sentence does not define. The staged
   rationale is that the retained sentence is about addressing rather than the teardown's internal
   structure and stays true under the split. The decision is whether §4.1 also retires the older
   vocabulary.
6. **Does the graceful-shutdown gate stay keyed to the binding for an unbound co-tenant?** After the
   split, a registered-but-unbound co-tenant still does not hold off the §15.4.2 signal, because the
   gate reads the binding, so a `Shutdown` of the last started session can signal the shared runtime
   to terminate while another session is mid-bind. This is shipped behaviour and the proposal stages
   nothing for it. Whether it stays out of scope once the residue is removed has not been decided.
7. **Where does the Redis rehydration hole get fixed?** After a Redis restart the pod's slot counter
   is rehydrated from active session rows, and a failed bind never has one, so a leaked slot's held
   occupancy is lost and the gateway can over-assign into unreclaimed resources. §6.2 already names
   the durable substrate it wants, so the inconsistency holds for the shipped leaked class
   independent of this proposal. The loop derived the remedy: widen §5.2's "Post-recovery rehydration
   atomicity" paragraph, and do not widen §7.1. The decision is whether that widening lands in this
   proposal or its own. One thing is distinctive to a failed-bind leak: the adapter never learns the
   slot is leaked, so no adapter-side count can re-derive it.
8. **Is a permanently occupied pod after a failed concurrent resume accepted?** On a concurrent pool
   a failed resume whose reclaim the adapter does not acknowledge leaves a freshly claimed pod
   holding occupancy 1 with no live session. The resume path claims the pod whole rather than through
   `ClaimSlot`, and the loop did not verify whether the slot claimer's first candidate pass treats
   such a pod as a candidate, so the §5.2 threshold may never reach it. Whether the residue is
   accepted, or the resume path owes a pod release, is undecided.
9. **Is the create-time-reserved retry's unbounded exposure accepted?** A §15.1 start onto a slot
   reserved at session creation is pinned to the row's pod assignment, so §5.2's placement constraint
   does not reach it. A reclaim still running when the client retries can delete the tree the retry
   materialized, or end a session the retry had already started, with no bound on the window.
   Retries the §5.2 policy places are bounded by the placement constraint and this path is not.
   Closing it requires placing the retried attempt off the reclaiming pod without the §5.2 retry
   policy, or persisting the exclusion on the session row, and this proposal stages neither. The loop
   records that the residue is worse than the version of this question asked at an earlier round, and
   asks for it to be answered against the text as it now stands.
10. **Does §7.1's exclusive-pod clause need a mid-resume carve-out?** §7.2 now routes the aborted
    mid-resume close through §7.1's reclaim, and §7.1 states that on a pod serving one session the
    failed attempt releases the pod's claim and the pod retires. Whether that reading holds for a
    half-claimed replacement pod that §7.2 step 3 releases back to the pool, or whether the clause
    needs a carve-out, has two readings the evidence does not separate. The loop states that only a
    human can adjudicate it.
11. **Does §7.1's trigger read "fails" or "abandoned or fails"?** §7.1's staged paragraph opens with
    a bind attempt that fails, while SPEC-3 and SPEC-4 say "abandoned or fails", and §7.2 and §6.2
    route a client-cancelled re-attach through the same obligation. Abandonment here means the
    gateway abandoned the bind by failing it; client abandonment is out of scope by the problem
    statement. The decision is whether §7.1's trigger widens to match the other three sites.
12. **Should SPEC-4's prose state once that a reclaim can find a slot in `running`?** The proposition
    follows from §7.1's "the adapter may have started the session", SPEC-4's `running → slot_cleanup`
    sentence, and §5.2's complement clause, and no section states it directly. The loop's position is
    that a reader who wants it said once should put it in SPEC-4's prose and never in §7.1.

## Defects in the shipped tree that this proposal does not stage

- **`claimPodMCPStartLocked` gates on a raw entry count.** `claimSessionSlotUnderLock`
  inserts the claimant's own entry and then calls a guard that returns false whenever
  `len(s.slots) != 1` (`pkg/adapter/slotsession.go:75-110`), so two ordinary interleaved binds
  on a healthy concurrent pod each observe two entries and neither arms the pod's intra-pod
  MCP surface, and nothing re-arms it afterwards. It produces the same user-visible outcome as
  the residue and fires more often. Out of scope by the problem statement and its own fix.
- **`SocketRuntimeProcess.Close`'s doc comment is false in one reachable state.** It claims a
  no-op for a session not in the active set (`pkg/adapter/socketruntime.go:427-429`). After
  `Interrupt` releases the last active session, `connected` stays true and `conn` stays
  non-nil (`:398-417`), so the next `Close` for any session finds an empty active set and
  closes the connection, kills the child, and closes the never-rebound listener (`:441-467`).
  The adapter test set pins the behaviour. Correcting the comment or the state handling is separate work.
- **§6.2 has no terminal for a bind abandoned at the connect stage.** `slot_assigned` has no
  edge into `slot_cleanup`. The connect stage reserves the slot before any workspace RPC, so
  the adapter holds nothing and this proposal's compensation does not run there, but the
  machine still has no edge out of the state. It is a pre-existing hole and wants its own
  problem statement.
- **The exclusive path leaves the same residue by a different trigger.** `Binder.Prepare`
  closes its connection on success and `Binder.Launch` reconnects later, so a
  prepared-but-unlaunched exclusive session sits bound, unstarted, credentialed, and
  timer-armed with no connection. It is named out of scope by the problem statement.
- **`bindConcurrentSlot`'s reserved branch reaches no slot-health accounting.** A
  `BindReservedSlot` error goes straight to `classifySlotBindFailure` and touches no tracker,
  no leak gauge, and no threshold (`pkg/gateway/sessionserver/start.go:2594-2605`). This is a
  conformance gap against §5.2's shipped whole-pod replacement trigger, which obliges the
  gateway to count every failed or leaked slot on a pod with no carve-out by code path. CODE-5
  closes it against the existing text, so no spec edit is staged for it.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0080 (inventory) §1.2 | Draft, stages no changes | This proposal is the promotion of §1.2 to its own proposal and discharges it. | Record §1.2 as discharged here. No edit to 0080's staged content, because it stages none. |
| 0080 §1.19 (three fence-refusal classes, one status code) | Draft | `boundSlotState` and `checkSessionBound` are untouched. The membership of the sets they read changes in three ways. A fence for a session whose bind failed and whose reclaim completed meets the absent-entry refusal rather than the unbound-entry refusal. Where a racing start's own claim re-created the entry after the reclaim, the entry is present and its `sessionID` is set, so the fence meets neither refusal and an abandoned session answers as a bound one. After a reclaim the adapter did not acknowledge on a create-time-reserved slot, a surviving entry is present-and-unbound (workspace-preparation residue) or present-and-bound (credential-assignment residue) for a session the gateway has abandoned, so a fence in that window meets the unbound-entry refusal or neither refusal, for a session 0080 would classify as gone. The placement constraint also narrows one class: with `ExcludePod` set after an unacknowledged reclaim, a retry the §5.2 slot retry policy places issues no fence to a pod that may still hold the prior attempt's entry, which does not reach a §15.1 start onto a create-time-reserved slot. | Re-derive §1.19's class inventory against all three cases and the narrowed class. |
| 0080 §1.1, §1.3, §1.4, §1.5, §1.16, §1.20 | Draft | No conflict. The adapter edits are placed in `session.go` and `runtimegeneration.go`; `slotsession.go` takes doc comments only and `slot.go` is untouched, so the later position's rewrite of those two files is not forced to re-land this work. | Nothing. |
| 0073 (give every session a slot) | Implemented | 0073 recorded this gap in its recorded limits and declined to discharge it. This proposal discharges it. | Nothing. A landed proposal is not edited. |
| 0078, 0079 (pods surviving across sessions) | Draft for review | Both widen the exposure, because a pod that serves more sessions carries the residue to more of them. This proposal is sequenced ahead of them and depends on neither. | Land after this one. |
| gateway-runtime-comms remediation, step R1b | Programme step | No impact. This proposal opens no schema file, so rule S-2's reservation of the single proto edit is preserved. | Nothing. |
| gateway-runtime-comms remediation, step R12 | Programme step | The hold-timeout reclaim of unstarted slots and its §10.1.4 statement are left to R12, which builds the control-stream consumer that arms the hold and owns the gateway-side whole-pod-loss response. | Take both halves when it builds the consumer, together with an in-flight-upload guard. |

## Deliverable index

- SPEC-1 — spec/04_system-components.md, spec/29_communication-scenarios.md — §4.1's scope sentence and the §4.7 `Shutdown` row state the slot release and the runtime teardown as two teardowns with two preconditions, and state the no-op answer for a session the adapter holds nothing for; §29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7.
- SPEC-2 — spec/07_session-lifecycle.md, spec/06_warm-pod-model.md, spec/05_runtime-registry-and-pool-model.md, spec/04_system-components.md — §7.1 gains the failed-bind pod-side reclaim obligation and its `leaked` disposition as a paragraph of its own covering the creation, start, and re-attach bind attempts; §7.2's mid-resume snapshot-close sequence runs the reclaim before the replacement pod is released and drops the premise that no runtime was started on it; §7.3's resume flow, §6.2's mid-resume cancel edge, and §4.7.9 step 5 point at it; §5.2's slot-retry `**Max retries:**` bullet states the placement constraint, naming a pod holding an unacknowledged reclaim among the conditions that place a retry on a different pod.
- SPEC-3 — spec/05_runtime-registry-and-pool-model.md — §5.2's slot-cleanup action list names the slot's credential directory and the §4.9 timer cancellation, and its scrub model covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release.
- SPEC-4 — spec/06_warm-pod-model.md — §6.2's per-slot sub-state machine gains the `receiving_uploads → slot_cleanup` edge and the pre-`running` cleanup paragraph.
- CODE-1 — pkg/adapter/session.go, pkg/adapter/runtimegeneration.go — `Shutdown` releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given.
- CODE-2 — pkg/adapter/runtimegeneration.go, pkg/adapter/session.go — `noteRuntimeStarted` confirms the slot survived and reports whether it did; `StartSession` rolls back when the reclaim landed after its claim.
- CODE-3 — pkg/sandbox/slotstate/slotstate.go — the per-slot edge list and its doc comment gain the `receiving_uploads → slot_cleanup` edge.
- CODE-4 — pkg/gateway/podlifecycle/podsession/slotbinder.go, binder.go, slotfailure.go — the gateway sends the compensating `Shutdown` at every post-connection bind failure and at a failed `Resume`, releases the session's credential leases, and carries the outcome as the `leaked` disposition.
- CODE-5 — pkg/gateway/sessionserver/start.go, pkg/gateway/podlifecycle/podclaim/slotclaimer.go — one account-classify-drain helper serves both concurrent bind paths, so the create-time reserved path reaches the §5.2 threshold, and the retry after an unacknowledged reclaim carries `ExcludePod` so `ClaimSlot` places it on a different pod.
- DOCS-1 — docs/reference/state-machines.md — the per-slot sub-state table gains the row matching the §6.2 edge.

Tests are not separate deliverables. Each code step carries the tests for the tiers it
reaches, specified per deliverable under `## Testing` in the non-spec changes file.
