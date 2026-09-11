# Summary: A failed session bind leaves a stale adapter slot registry entry

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
  stating where a retry after an incomplete reclaim lands, beside the saturation and
  health conditions it already lists. §4.1
  and the §4.7 `Shutdown` row separate the slot release from the runtime teardown and give
  each its own precondition, §5.2's slot-cleanup action list names the per-slot credential
  directory and the §4.9 timer cancellation while its scrub model covers the cleanup of a
  bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches
  `running`, withholds that cleanup's outcome report, and gives each session release at most
  one cleanup-outcome report, and §6.2 gains the `receiving_uploads → slot_cleanup` edge.
- The specification states the bind epoch and the slot identifier's reclaim hold. §4.7.1
  states what the adapter mints, which responses report it, which request carries it, and
  which epoch a caller may name; the §4.7 `Shutdown` row states the comparison and points at
  §4.7.1 for its three outcomes; §5.2 is the single home of the reclaim hold, stating that the
  adapter holds a slot identifier from the deregistration of its registry entry until the cleanup
  that reclaims it has finished, why the hold is a property of the identifier rather than of the
  entry, that a request onto a held identifier is refused as a transient condition, that the
  refusal is the only record the adapter makes of it, and what bounds it; §4.7.1 and §6.2 point
  at that statement rather than restating it; and §15.4 publishes the observable an adapter must
  exhibit for each, the `ABORTED` refusal included, with a non-conformance statement per part.
- The adapter mints a bind epoch when it creates a registry entry for a slot identifier it
  holds none for, holds that epoch unchanged for the entry's life, releases it with the entry,
  and reports it on every bind-sequence response that resolves the entry. No bind-sequence
  request carries an epoch and none is refused on one; a bind onto an identifier whose cleanup
  is running is refused by the reclaim hold. A compensating `Shutdown` names the epoch its own
  connection observed, so a reclaim addressed to a released entry answers `superseded` and
  removes nothing (`pkg/adapter/bindepoch.go`, `schemas/lenny-adapter.proto`).
- The adapter holds the slot identifier for the duration of a reclaim, from the
  deregistration through the tree removal and the runtime close, and refuses a bind onto a
  held identifier with a transient `codes.Aborted`. Every site that deregisters an entry it
  then destroys takes the hold through one helper, so `Shutdown`, `releaseSessionSlot` and the
  §10.1.4 hold termination cannot diverge (`pkg/adapter/bindepoch.go`,
  `pkg/adapter/slotsession.go`, `pkg/adapter/holdstate.go`, `pkg/adapter/slot.go`).
- The adapter's `Shutdown` compares the epoch a reclaim names against the entry it holds and
  performs neither teardown when the entry it holds is not the one the reclaim names, releases the slot for any entry the call removed,
  runs the runtime teardown only for a session whose start it has admitted, and files a
  cleanup-outcome report only for a session the pod's shared runtime process was given
  (`pkg/adapter/session.go`).
- A start confirms its slot survived at the epoch its own claim observed before recording the
  runtime as holding the session. This confirmation is the only check that reaches the window between the
  start's claim and the record of the runtime holding the session, on both RPCs whose
  admitted start the gateway's compensation can race
  (`pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`).
- The gateway latches the most recent bind epoch a connection has reported, clears the latch when it issues the `DemoteSDK` that removes the entry that epoch names and
  when a reclaim on that connection answers `RECLAIMED`, re-latches from every later
  bind-sequence response that connection carries for the session, the §7.4 mid-session
  upload's `PrepareWorkspace` and `FinalizeWorkspace` included, and sends what it holds on the
  compensating `Shutdown`
  at each post-connection bind failure and at a failed resume, on the connection the failed
  stage still holds and never on a fresh one. It maps the reclaim's
  outcome before it reads `exited_cleanly`: a superseded or absent reclaim is a completed
  reclaim that leaves the slot unleaked, an unanswered one leaves it leaked, and a reclaim
  that ran and did not complete leaves it leaked
  (`pkg/gateway/podlifecycle/podsession/`, `pkg/gateway/runtime/adapterclient/client.go`).
- One accounting helper serves every bind path the reclaim obligation binds, so the
  create-time reserved path and the §7.3 re-attach reach the §5.2 unhealthy threshold neither
  has ever reached (`pkg/gateway/sessionserver/start.go`).
- The per-slot edge list in `pkg/sandbox/slotstate` and the per-slot table in
  `docs/reference/state-machines.md` follow the §6.2 edit.

**Decisions.**

- The remedy is the `Shutdown` RPC the gateway already has, sent on the connection it
  already holds at the failure site. No new RPC: `Shutdown` clause two remains the single
  removal entry point and the bind epoch is a precondition on it. No timer, no new persisted
  state, and no new metric. `ExcludePods` on the in-process bind request structs carries
  §5.2's placement constraint for the pods holding an incomplete reclaim into placement
  and reaches no wire format.
- `schemas/lenny-adapter.proto` is opened, additively. The edit is one enum and nine fields,
  with no field removed and none renumbered, so `buf breaking` has nothing to fire on. Rule
  S-2 of the gateway-runtime-comms remediation programme reserves the first proto window to
  step R1b and states that a later step needing a field the plan did not enumerate opens a
  second narrow window whose precondition is that every in-flight `pkg/adapter` handler edit
  has merged. R1b's end state is in the tree and the file has already been reopened once, for
  proposal 0076's comment-only edit. SCHEMA-1 is that second window. The precondition binds
  this proposal's own step ordering, because it opens three of S-2's covered handler files
  (`session.go`, `slotcreds.go`, `sdkwarm.go`): the schema step and its regenerated stubs
  land in one commit before those handler steps.
- Open decision 9 is closed by fencing the reclaim rather than by accepting the exposure or
  by reopening the create-time pod binding. Two mechanisms are needed because the ordering
  has two halves. The bind epoch makes a late reclaim refusable, which is the ordering a hold
  cannot reach: `compensateFailedSlotBind` runs on `context.WithoutCancel(ctx)` precisely
  because the caller's context has expired, so the client may already have retried and bound
  the slot before the reclaim is dispatched, at which point nothing has arrived to take a
  hold. The reclaim hold makes an admitted reclaim exclusive while it tears state down, which
  is the ordering the epoch cannot reach: the destructive steps read no registry state,
  because `removeSlotTree` deletes paths derived from the slot identifier and `SlotID ==
  SessionID` makes that identifier equal across attempts. The ABA ordering the proposal
  previously accepted, in which a successor's claim takes the entry a start confirmation
  reads, closes with the epoch where the successor's claim creates a fresh entry, because that entry
  carries a different one and the earlier attempt's confirmation refuses its start. Seven
  residues survive. Four sit under the accepted failure modes: a bind failing inside its first
  entry-creating RPC reclaims unfenced; a retry can meet the reclaim hold and spend an attempt
  on it; a compensation still on the wire when a retry adopts the surviving entry is not
  separated by the epoch, and where the teardown lands after that retry has answered its client
  it tears a serving session down; and an abandoned attempt's start whose claim runs after the
  reclaim completed re-creates the entry and its own confirmation admits it. Two sit under the
  defects this proposal does not stage: concurrent starts for one session are not serialized,
  and the post-bind session RPCs on a bound session carry no fence. The rule that a compensation
  never re-dials becomes a hard dependency, under "Watch out for".
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
  carries both inputs, and an unset `cleanupTimeoutSeconds` yields the 5s floor. That figure is
  the gateway's give-up bound on the compensating `Shutdown`, and the graceful window the call
  pins for the adapter is half of it, so the gateway does not give up on the adapter's SIGTERM
  pivot. A cleanup whose slot-tree removal outruns the remaining budget answers nothing in
  time, and that is the unanswered reclaim §7.1 accounts, which leaves the slot `leaked` on
  a pod serving concurrent sessions and retires the pod on a pod serving one. The
  specification states no number for the gateway-side deadline; the code cites §5.2.
- The proposal does not claim to fix "the pod's intra-pod MCP surface never arms again".
  `claimPodMCPStartLocked` gates on `len(s.slots) != 1`, and the claimant's own entry is
  inserted immediately above that guard, so two ordinary interleaved binds on a healthy
  concurrent pod each observe two entries and neither arms, with no failed bind anywhere.
  The residue makes that outcome permanent rather than causing it.
- File-collision discipline: the compensation's own adapter edits land in `session.go` and
  `runtimegeneration.go`, and the epoch and hold land in a new file, `pkg/adapter/bindepoch.go`,
  rather than being spread through the existing ones. `pkg/adapter/slotsession.go` and
  `pkg/adapter/slot.go` are both opened, because `ensureSlotStateLocked` is where the entry
  is created and is therefore where the epoch is minted, on that creating branch alone,
  and where the hold is refused, and
  `slotState` gains one field, `epoch`. A later position covering proposal 0080 rewrites
  both files, so this proposal keeps its edits in each to the smallest set the mechanism
  needs: the hold refusal and the epoch mint in `ensureSlotStateLocked`, and the resolve-site
  move plus the claim functions' epoch return in `slotsession.go`.

**Watch out for.**

- Sending today's `Shutdown` at the credential-assignment stage would brick the pod.
  `SocketRuntimeProcess.Close` returns early only on `!p.connected`, then calls
  `releaseActiveLocked`, which deletes an absent key and returns `len(p.active) == 0`. On a
  pod whose active set is empty it therefore closes the shared connection, kills the spawned
  child, and calls `p.listener.Close()` on a listener bound once in `NewSocketRuntimeProcess`
  and never rebound (`pkg/adapter/socketruntime.go:156-161`, `:435-467`). `InProcessRuntime.Close`
  and `MCPRuntime.Close` ignore the session identifier entirely and tear the runtime down on
  any call (`pkg/adapter/embedded.go:188-204`, `pkg/adapter/mcpruntime.go:266-291`). This is
  why the adapter gate must land before the gateway compensation.
- A compensation must never re-dial. This proposal creates a hard dependency on the rule
  that the reclaim runs on the connection the failed attempt already holds. The bind epoch is
  pod-local and is latched off the responses that connection returned, so a compensation
  written to re-dial when the connection is gone would send a zero epoch, which is the
  unconditional form, at a pod that may already hold a successor's slot. The rule is
  normative in §7.1 and in §4.7's caller rules and is stated in
  `compensateFailedSlotBind`'s doc comment, and a later change that adds a retry-with-redial
  to that function reopens the race this amendment closes.
- The ordering is fixed across all three lanes. The spec steps lead. The schema step and its
  regenerated stubs land in one commit before anything reads the generated types. CODE-6
  lands the epoch counter, the entry's epoch and the reclaim hold before CODE-1 inserts into
  that hold, so each step compiles on its own. The adapter gate lands before the gateway
  half: landing the gateway half alone would send a full teardown, a §15.4.2 drain frame, a
  `Runtime.Close`, and a `sessionsServed` increment for a session that never ran.
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
- `TestValidTransitions_spec_6_2` compares a `want` list in
  `pkg/sandbox/slotstate/slotstate_test.go` against `ValidTransitions()` and fatals on a
  length mismatch. CODE-3 changes both, so the two must move in the same step. Nothing
  compares either against `spec/06_warm-pod-model.md`, so the §6.2 edit is not what the test
  gates.
- `SlotID == SessionID` on every path, and §5.2 placement prefers a pod already hosting the
  tenant's slots. A retry therefore re-uses the identifier and can land on the pod whose
  reclaim may still be running `os.RemoveAll` outside `s.mu`. CODE-5 closes that window by
  carrying `ExcludePods` on the retry, so the attempt keeps its §5.2 retry and only the
  reclaiming pods are disqualified from it.

## Goals

- Remove the adapter slot registry entry, the per-slot tree, the per-slot credential file,
  and the armed §4.9 expiry timers that a failed bind leaves behind, in all three residue
  classes.
- Take the abandoned session back off the pod's shared runtime process in the class where
  the runtime was given it.
- Give the compensation a stated obligation in the specification, so the code traces to a
  section rather than to a convention.
- Make the compensation's own failure bounded and counted, on every bind path the reclaim
  obligation binds, through the accounting the specification already states.

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
- **A new adapter RPC (`AbortBind`, `ReleaseSlot`, `DiscardSlot`).** `Shutdown` is already
  addressed by the session identifier, which is also the slot identifier on every path, and
  its clause two already deregisters unconditionally. A second removal entry point would be a
  second lifetime to get wrong. An additive field is no longer a non-goal:
  the bind epoch travels on `ShutdownRequest`, which is the only request that carries
  it, and it is a precondition on that request rather than a second removal entry point.
- **Branching the adapter on `ShutdownRequest.reason`.** It is the obvious shortcut. It makes
  the cleanup semantics depend on a free-form string the
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
  `Resume` response, is closed by extending CODE-4 to `Binder.Resume` and CODE-2's start
  confirmation to the adapter's `Resume` instead.
- **Widening the §5.2 whole-pod scrub to enumerate the registry as its source.** Its on-disk
  enumeration is deliberate and its own comment gives the reason: the residue it must reach
  belongs to a leaked slot whose registry entry is already gone.
- **Splitting the work by residue class into two proposals.** The change touches one map, one
  entry, one `delete`, and one conditional. Two proposals would edit the same branch.
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
  different owner. CODE-1's gate changes nothing on that path, because a session that reaches
  a terminal state before it launches is reclaimed from its persisted binding and the adapter
  is sent no `Shutdown` for it (`pkg/gateway/sessionserver/usage.go:446` and `:581-605`). The
  residue that leaves behind is recorded under the defects this proposal does not stage.
- **Re-stating §28.5.3, §15.4.3, or §15.4.2 in the specification.** Their rules are correct as
  written. With the residue removed or the pod retired, none of those predicates is fed a
  residue. Naming them would state a rule change the code does not make and would invite a
  later reader to weaken a count that fails closed on purpose.

## Open decisions for human to make

The spec review loop routed these to a human. Each states the question and the ground the loop
derived, and carries a recommendation where the review derived one. Entry 9, the
create-time-reserved retry's unbounded exposure, has left this section: the bind epoch and the
reclaim hold close the released-entry ordering, and the shared-entry ordering is accepted,
recorded in the staged spec changes under `## Edge cases and accepted failure modes` together
with what closing it would cost. Entry 17 re-puts entry 9's question against the text the spec
loop converged on, which states a worse residue than the text entry 9 was answered against.
Entries 12, 13, 14 and 15 have also left it: the spec the proposal stages
already answers each, and the residue entries 13 and 14 named is recorded under
`## Defects in the shipped tree that this proposal does not stage`. Entry 15 asked whether
§7.1's exclusive-pod clause needs a mid-resume carve-out, and it does not. §6.2's `resuming`
failure-transitions subsection is the authoritative enumeration of every edge out of that state
(`spec/06_warm-pod-model.md:229`) and it already states the outcome for exactly that pod, "the
half-claimed replacement pod is released to the pool" (`spec/06_warm-pod-model.md:234`), so the
disposition is owned elsewhere and settled. §7.1 as staged asserts no competing outcome: it
closes "The pre-attached disposition governs the pod; this reclaim governs the slot state on a
pod that is released or reused rather than terminated", which names the released-or-reused case,
and SPEC-2's §7.2 step 3 states no pod outcome by design. The residue on the released pod is
disposed of in both configurations by shipped text: a claim deleted on a pod with
`recycle.enabled: false` projects `draining` and then `terminated`
(`spec/06_warm-pod-model.md:80`), and on a recycling pod "a whole-pod scrub runs whenever
occupancy reaches zero on a recycling pod before the pod is reused"
(`spec/05_runtime-registry-and-pool-model.md:453`), a trigger the release path already implements
(`pkg/gateway/podlifecycle/podsession/binder.go:498-501`). Resolved entries are deleted
and the survivors keep their original numbers, so the numbering below does not start at 1 and
skips the numbers the resolved entries held; entries 16 through 18 were carried out of the
review log by the index-and-checklist reconciliation pass.

11. **Does §7.1's trigger read "fails" or "abandoned or fails"?** §7.1's staged paragraph opens with
    a bind attempt that fails, while SPEC-3 and SPEC-4 say "abandoned or fails", and §7.2 and §6.2
    route a client-cancelled re-attach through the same obligation. Abandonment here means the
    gateway abandoned the bind by failing it; client abandonment is out of scope by the problem
    statement. The decision is whether §7.1's trigger widens to match the other three sites.

16. **Does the exclusive-pool resume budget want an upper clamp?** The compensating `Shutdown`
    budget is §5.2's per-slot cleanup timeout, `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)`
    seconds. On an exclusive pool the divisor is 1, so the budget is the whole
    `cleanupTimeoutSeconds`, which §5.2's own example puts at 60 seconds. That is the longest
    hold on the path with the least parallelism, taken on a request goroutine
    `context.WithoutCancel` has detached from the client, and on an exclusive pool nothing is
    accounted and nothing is released while it runs. The decision is whether the budget takes
    an upper clamp on an exclusive pool. Three lenses recorded the exposure and none filed it,
    because no stated budget is breached and the proposal argues the direction explicitly, so
    the loop derived no recommendation.

17. **Ship with a rare path on which a live session is torn down and reported as a clean
    reclaim, or widen this proposal to close it?** When a bind fails, the gateway sends the
    adapter a compensating teardown for the slot it reserved. If that call errors at the
    gateway while the adapter is still running it, and a second attempt at the same session
    reaches the same pod under the same slot identifier before the teardown removes the
    registry entry, the second attempt adopts the surviving entry and inherits its bind epoch.
    The epoch is what tells a stale teardown from a current one, so an adopted epoch compares
    equal and the teardown proceeds. Where the teardown's critical section runs after the
    second attempt has started its session and answered its client, it closes that live
    runtime session and removes the slot's directories, and the gateway records the reclaim as
    a clean `reclaimed`. Nothing in the staged deliverable surfaces the loss to an operator.
    The question for a human is whether that residue ships as recorded, or whether this
    proposal must close it.

    *Recommendation:* accept it as recorded, at low confidence. The staged text already prices
    the general closure and declines it: a per-attempt discriminator would have to be carried
    on every request that can create or resolve an entry
    (the staged spec changes, `## Edge cases and accepted failure modes`, the bullet "A
    compensation still on the wire when a retry adopts the surviving entry is not fenced").
    Ground: the residue reaches only a create-time-reserved slot, because a slot the §5.2
    retry policy places is kept off the reclaiming pod by the staged placement constraint,
    while a row carrying a `PodAssignment` goes through `BindReservedSlot` and never enters
    `applySlotRetryPolicy` (`pkg/gateway/sessionserver/start.go:2594-2604`). The recommendation
    is low-confidence because the harm is a silent correctness violation on a live session and
    the review found no metric, alert, or runbook that would surface it.

    *Alternatives considered.* Placing the retried attempt off the reclaiming pod without
    `applySlotRetryPolicy` lost because a create-time reservation is the §4.6 durable binding
    to one named pod: moving the attempt means abandoning that binding and re-reserving a
    different slot, which changes what `/create` promised rather than adding a constraint.
    Persisting the pod exclusion on the session row lost because it adds a stored field with
    its write path, its read path, and something that clears it once the reclaim is answered,
    which is a schema change this proposal does not otherwise make. The per-attempt
    discriminator lost on the cost the staged text states, since it touches every entry-creating
    and entry-resolving request on the adapter surface.

    *Cost of deciding otherwise.* Answering "close it" adds a placement or a persistence
    deliverable, its tests, and a further review round before this proposal can land.
    Answering "accept" ships the path described above, and closing it later is a separate
    proposal rather than an amendment to this one.

18. **Should a retry refused by a reclaim hold that never clears get a distinct client-visible
    outcome?** When a reclaim for a session whose start the adapter admitted is never answered,
    the hold on that slot identifier never reaches its stated terminal, so every further attempt
    at the same session on that pod is refused as a transient condition until the pod
    terminates. On a create-time-reserved slot §5.2's placement constraint does not move the
    retry elsewhere, so the attempts retry against the same refusal. The residue is recorded in
    the accepted failure modes and the refusal is `codes.Aborted`, which the client sees as a
    transient failure rather than as a stuck session. The decision is whether that path warrants
    a distinct client-visible outcome. The loop derived the question and recommends nothing.

## Defects in the shipped tree that this proposal does not stage

- **`claimPodMCPStartLocked` gates on a raw entry count.** `claimSessionSlotUnderLock`
  inserts the claimant's own entry and then calls a guard that returns false whenever
  `len(s.slots) != 1` (`pkg/adapter/slotsession.go:75-110`), so two ordinary interleaved binds
  on a healthy concurrent pod each observe two entries and neither arms the pod's intra-pod
  MCP surface. The arming decision is taken once, inside the claim, so neither session is
  armed later. It produces the same user-visible outcome as the residue and fires more often,
  and removing the residue restores the arming only for a session that claims while it holds
  the pod alone, leaving it refused wherever two binds overlap. The reclaim hold does not
  change it either way, because a held identifier carries no registry entry and the guard
  counts entries. Out of scope by the problem statement and its own fix.
- **Concurrent `POST /v1/sessions/{id}/start` for one session is not serialized.** `handleStart`
  validates the precondition against the row it read and writes no `starting` state before
  launching, so two concurrent calls both reach `BindReservedSlot` on the same pod with the same
  slot identifier, which equals the session identifier
  (`pkg/gateway/sessionserver/start.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`).
  That is a gateway serialization defect on the shipped path, present before this proposal and
  unchanged by it. It is recorded here because the two attempts share one registry entry and one
  epoch, so this proposal's fence does not separate them. It belongs to the session-lifecycle
  area and is raised as its own finding rather than staged here.
- **Forward-RPC fencing is not closed for the post-bind session RPCs.** The bind epoch fences `Shutdown`, which is the one request that carries it. Every
  other gateway-to-pod RPC of a session still addresses it by its identifier with no
  statement of which attempt is asking, so an `Attach`, an `Interrupt`, a `Checkpoint`, an
  `ExportPaths`, a `ReportUsage`, a `RotateCredentials`, or an `ExtendCredentialLease` from
  an abandoned attempt that the gateway stopped waiting for can still reach a successor's
  entry on the same pod. This proposal does not stage that fence: it would mean carrying the
  epoch on the request of every session RPC and refusing a mismatch on each, which changes
  the failure semantics of the whole session surface rather than of the bind sequence and
  its compensation. It is recorded here rather than as a claim-register
  deferral, because a non-`WIRED` row must name a step the remediation plan declares and no
  declared step owns this fence.
- **§10.1's per-RPC coordination-generation fence is not enforced by the shipped adapter.**
  `spec/10_gateway-internals.md:30` states that pods validate the generation on every
  gateway-to-pod RPC and reject a stale one, and `ShutdownRequest` carries the field
  (`schemas/lenny-adapter.proto:1630-1635`). The adapter reads it on two RPCs only,
  `CoordinatorFence` and `CheckpointBarrier`
  (`pkg/adapter/coordination.go:120`, `:262`), and on no other handler, so the divergence
  spans the whole gateway-to-pod surface and predates this proposal. It is recorded here
  because SPEC-5's staged §4.7.1 block states that where the generation and the bind epoch
  appear on one message, as on `Shutdown`, each is checked on its own terms, which makes the
  compensating `Shutdown` subject to the fence rather than exempt from it. Nothing this
  proposal stages depends on the fence being unenforced. A generation-stale rejection is a
  reclaim the adapter did not answer, which SPEC-2's §7.1 paragraph already dispositions:
  the slot is `leaked` on a pod serving concurrent sessions and the pod retires under §6.2's
  pre-attached failure disposition on a pod serving one, so the §7.1 obligation is to send
  the reclaim rather than to have it accepted. Carving the compensation out of the fence
  would also contradict §10.1's stale-replica rule, which tells a replica that receives a
  generation-stale rejection to cancel its in-flight RPCs for the session and not retry
  (`spec/10_gateway-internals.md:66-68`). No fix is staged because closing the divergence
  means implementing §10.1's fence across every adapter handler, which is a §10.1-scoped
  change on a rule this proposal neither wrote nor widened.
- **`BindReservedSlot` never re-reserves while `ReleaseSlotReservation` decrements.** On the
  create-time-reserved path the reservation is taken at session creation and released on a
  failed bind, and the bind is then retried against the same row without re-taking it, so the
  pod's counter is decremented once for a reservation the retry still relies on. This is a
  shipped defect and predates this proposal: the reclaim changes which failures reach the
  release and changes nothing about the accounting either side of it. Recorded so a reader of
  the accounting deliverable does not read the asymmetry as something CODE-5 introduced. No
  fix is staged; correcting it means deciding whether a create-time reservation is per
  session or per attempt, which reopens §4.6's created-state pod binding.
- **`SocketRuntimeProcess.Close`'s doc comment is false in one reachable state.** It claims a
  no-op for a session not in the active set (`pkg/adapter/socketruntime.go:427-429`). After
  `Interrupt` releases the last active session, `connected` stays true and `conn` stays
  non-nil (`:398-417`), so the next `Close` for any session finds an empty active set and
  closes the connection, kills the child, and closes the listener, which is bound once in
  `NewSocketRuntimeProcess` and never rebound (`:435-467`, `:156-161`). The staged adapter
  tests pin the behaviour: the sibling assertion in `pkg/adapter/socketruntime_test.go`, beside
  `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`, records that `Close` in that state is
  not the no-op its doc comment claims. No staged deliverable relies on the promised no-op,
  because CODE-1 gates the runtime teardown on `started` and CODE-2's rollback closes a session
  the runtime holds. Correcting the comment or the state handling is separate work.
- **A pod whose runtime process has been closed cannot serve another session.** The socket
  runtime binds its listener once, in `NewSocketRuntimeProcess` at adapter start
  (`pkg/adapter/socketruntime.go:156-161`), and closes it on any last `Runtime.Close`, the
  release that empties the active set (`:435-467`, the listener close at `:467`). `Start`
  accepts on that same listener (`:181-202`, the accept at `:202`) and has no rebind path, so
  a later session placed on the pod cannot connect. The sidecar socket transport is the
  production selection (`cmd/lenny-adapter/main.go:350-359`). The ordinary session-end path
  reaches the same state through the same call, because `Binder.ReleaseSlot` sends the adapter
  `Shutdown` for the last started session
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:528-545`, the call at `:542`) while a
  co-tenant whose bind is still in flight is absent from the runtime's active set, which records
  a session only at `Start` (`pkg/adapter/socketruntime.go:184` and `:220`). This proposal's
  compensating reclaim therefore adds a trigger for a state the shipped release path already
  produces under the same precondition, rather than a state class of its own. Whether the
  bricked pod leaves inventory depends on the release's disposition rather than on the close,
  and there are three of them. The per-pod claim is retained while a sibling slot still counts
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845-847`), and at occupancy zero on a
  non-recycling pool it is deleted, which retires the pod (`:881-885`). On a recycling pool the
  occupancy-zero edge takes neither: it patches the claim `bound → recycling`, arms the
  missing-report timeout and signals the whole-pod recycle `Shutdown` (`:850-878`), and a clean
  `ReportPodScrub` then drives the reuse disposition that returns the pod to inventory
  (`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:98-110`). That is
  the disposition in which a bricked pod is handed to the next session, and it is broken for
  socket runtimes independently of any failed bind. No fix is staged because correcting it
  reopens the `Start` and `Close` contract of the shared runtime transport for every pod that
  uses it, which carries its own §5.2 conformance question. The defect is BUILD-GAPS finding
  F-5.2.33, which is open in `BUILD-GAPS.md` and already carries a problem statement in each
  half: part (a), the pod-scoped listener teardown, is proposal 0078, and part (b), the component
  that creates the successor runtime process on the sidecar deployment model, is proposal 0079.
- **§6.2 has no terminal for a bind abandoned at the connect stage.** The per-slot fence for a
  pod of either concurrency carries a single edge out of `slot_assigned`, into
  `receiving_uploads` (`spec/06_warm-pod-model.md:150-155`, the edge at `:151`), so a slot
  abandoned before workspace materialization has no legal terminal, and
  `pkg/sandbox/slotstate` mirrors the same edge list with no exit-completeness assertion over
  it (`pkg/sandbox/slotstate/slotstate.go:70-114`). The connect stage reserves the slot before
  any workspace RPC (`pkg/gateway/podlifecycle/podsession/slotbinder.go:230-254` and the
  reservation-bearing failures at `:449-473`), so the adapter holds no entry and this
  proposal's compensation does not run there. SPEC-4 adds the `receiving_uploads →
  slot_cleanup` edge alone, because that is the state the staged reclaim acts on. The missing
  edge produces no runtime error, because the gateway never drives a slot through the
  validating path: `Registry.Assign` and `Registry.Transition` have no production caller, and
  `MarkLeaked` writes the terminal state without consulting the edge list
  (`pkg/sandbox/slotstate/registry.go:99-116`, the write at `:106`). The transition the fence
  fails to model is already produced in the shipped tree, because `applySlotRetryPolicy` marks
  a connect-stage failure's slot leaked when its reservation release errors
  (`pkg/gateway/sessionserver/start.go:2834-2848`). This proposal adds one more producer of
  the same transition, on the create-time-reserved path. `BindReservedSlot` today logs its own
  reservation-release error and returns
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224`, the swallow at `:217-220`);
  CODE-4 retires that swallow and folds the error into `sbe.Leaked`, and CODE-5's
  reserved-branch caller passes that discriminator to `accountSlotFailure`. A connect-stage
  failure whose own reservation release also fails therefore reaches `MarkLeaked` on a slot in
  `slot_assigned` rather than the windowed `RecordFailure`. The scope call stands on the
  remaining ground: the hole predates this proposal and is widened rather than created here,
  the widened transition raises no runtime error for the reason above, and closing it means an
  edge out of `slot_assigned` that SPEC-4 deliberately does not add, on a residue class this
  proposal does not act on. It wants its own problem statement rather than a widened fence
  here.
- **A lagging pre-`Runtime.Start` failure branch deletes a later attempt's slot tree.**
  `StartSession` releases the slot on each of its pre-start failure branches
  (`pkg/adapter/session.go:133`, `:147`, `:157`), and `Resume` carries the same claim,
  start, and record sequence with the same releases (`pkg/adapter/resume.go:69`, `:73`,
  `:89`, `:107`, `:126`, `:134`, `:141`). `releaseSessionSlot` is keyed on the session
  identifier alone: it deregisters whatever entry the registry holds under that key and,
  when it removed one, deletes the slot's on-disk tree
  (`pkg/adapter/slotsession.go:214-220`, the removal at `:217`), which drops
  `/workspace/slots/{sessionId}`, `/sessions/{sessionId}`, `/artifacts/{sessionId}`, and
  the credential directory (`pkg/adapter/slotlayout/tree.go:49-63`). Because the slot
  identifier is the session identifier, a branch still running after the gateway abandoned
  its RPC deletes the tree a later attempt at the same session has staged. The
  create-time-reserved path reaches it in the shipped tree, because `BindReservedSlot`
  reconnects the retry to the same pod under the same slot identifier
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:210-224`). The producer here is the
  abandoned attempt's own shipped rollback, which is a different mechanism from the
  compensating `Shutdown` the bind epoch and the reclaim hold fence. No fix is staged because
  separating the two attempts needs a per-attempt identity on the adapter's slot entry
  that the platform does not carry, which is the same gap the accepted failure modes record
  for a compensation still on the wire when a retry adopts the surviving entry. This proposal
  opens none of those branches, and CODE-2's rollback
  drops its own instance of the pattern rather than making the class identity-checked.
- **The exclusive path leaves the same residue by a different trigger.** `Binder.Prepare`
  runs the §4.7 setup chain through `assignCredentials` and closes its connection before it
  returns (`pkg/gateway/podlifecycle/podsession/binder.go:843-965`, the doc comment at `:841`
  and the close at `:957`), and `Binder.Launch` reconnects at client pace, so a session that
  prepares and never launches leaves the adapter holding an entry that
  `assignCredentialsSlot` bound, credentialed, and armed §4.9 expiry timers on without ever
  starting it (`pkg/adapter/slotcreds.go:23-52`). Nothing later reclaims that entry. A
  session that reaches a terminal state before it launches is released by the by-name claim
  reclaim rather than through the executor, and that reclaim revokes the lease and deletes
  the per-pod `SandboxClaim` without sending the adapter any RPC
  (`pkg/gateway/sessionserver/usage.go:446` and `:581-605`;
  `pkg/gateway/podlifecycle/podsession/binder.go:1097-1106`). No fix is staged here because
  the trigger is a bind that succeeded, so CODE-4's compensation has no failure site to hang
  off and no open connection to carry a reclaim, and the adapter cannot distinguish the entry
  from a live exclusive session's, which is why closing the case needs the pre-start slot
  lease this proposal rejects under Non-goals. It is named out of scope by the problem
  statement.
- **The §10.1 coordinator hold cannot arm in production.** `enterHoldState` has one non-test
  caller, `onCoordinatorChannelClosed` (`pkg/adapter/holdstate.go:89-99`), which runs only from
  the deferred close of the adapter's own `AdapterEvents` server handler
  (`pkg/adapter/adapterevents.go:100-108`), and no gateway code opens that stream. The generated
  client stub exists and the only reference to the stream under `pkg/gateway` is a doc comment
  (`pkg/gateway/runtime/adapterclient/client.go:464`). `hasStartedSession`
  (`pkg/adapter/slotsession.go:338`), the hold timeout (`pkg/adapter/holdstate.go:177-190`), and
  its `deregisterStartedSessions` pass (`pkg/adapter/slotsession.go:375`) are unreachable in a
  running deployment, so the third residue class's arming of the hold on a pod the gateway
  believes holds nothing is latent rather than live. No fix is staged here because the arming
  needs the gateway control-stream consumer and the per-slot hold state that remediation step
  R12 builds, and that step records the same fact and the same reason
  (`gateway-runtime-comms-remediation.md:1268-1274`). The hold-timeout reclaim of unstarted
  slots and its §10.1.4 statement are left to R12 under Non-goals for the same reason.
- **§5.2's Fresh workspace guarantee does not reach a retry that adopts a surviving registry
  entry.** The guarantee sits in §5.2's slot retry policy list
  (`spec/05_runtime-registry-and-pool-model.md:553`), whose first bullet fixes the domain to a
  retry the policy assigns to a new slot (`:555`), and the guarantee then promises that such a
  slot inherits nothing from the failed slot's workspace (`:556`). A same-session attempt on the
  create-time-reserved path, which that policy does not place, resolves the surviving entry
  instead: `ensureSlotStateLocked` returns the existing `*slotState` with its paths, credentials
  and on-disk tree, and `slotlayout.EnsureTree` runs only on the create branch
  (`pkg/adapter/slot.go:105-124`). With the slot identifier equal to the session identifier the
  adopted tree is the predecessor's. The behaviour is shipped and unchanged by anything staged
  here: the staged admission rules narrow which attempts reach that pod and create no adoption
  path the adapter did not already have. It is recorded rather than staged because closing it
  means deciding whether §5.2's guarantee is rewritten against a slot identifier that is the
  session identifier, which reopens the retry policy's placement wording.
- **A leaked slot's held occupancy has no durable backing, so a Redis restart frees it.**
  §6.2's **`leaked` slot semantics** makes the pod's Redis slot-counter occupancy the
  persistent count for a leaked slot, and `SlotClaimer.ReleaseSlot` implements that by
  returning before the decrement
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:830-836`). §5.2's **Post-recovery
  rehydration atomicity** paragraph rebuilds the counter from one source,
  `SessionStore.GetActiveSlotsByPod`, which counts the non-terminal session rows bound to
  the pod (`pkg/gateway/session/sessionstore/pgstore/pgstore.go:732-744`), so a leaked slot
  whose session row is terminal is not counted and a Redis restart frees occupancy the pod
  still holds. The hole is live for the class the tree already ships, on the ordinary
  session-end path and on a healthy Redis: `Binder.ReleaseSlot` computes the disposition
  from the adapter `Shutdown` on every session end
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543`), and the shipped failed-bind
  path already marks the slot leaked when the reservation release errors
  (`pkg/gateway/sessionserver/start.go:2834-2848`). CODE-4's compensating `Shutdown` adds
  instances of that existing producer rather than a new dependence. Re-deriving the
  occupancy from the adapter is unavailable for any leaked slot, because the per-slot
  registry carries a leaked count and no pod-wide occupancy count
  (`pkg/sandbox/slotstate/registry.go:8-12`), so the gap is not distinctive to a failed
  bind. No fix is staged here because the remedy is wider than one clause. The single
  rebuild source is restated at four sites: §5.2's **Post-recovery rehydration atomicity**
  paragraph, §12.4's Redis key table row for `lenny:pod:{pod_id}:active_slots`, §12.4's
  Redis-unavailable Postgres fallback for the same key, and §10.1.4's whole-pod
  connection-loss counter reset. That paragraph is also already wrong about its own index
  predicate, which it gives as `sessions(pod_assignment) WHERE state = 'active'` while
  `migrations/0080_sessions_active_by_pod_index.up.sql:19-25` cites that sentence and then
  creates the index `ON sessions (pod_assignment) WHERE pod_assignment <> '' AND state NOT
  IN ('completed', 'failed', 'cancelled', 'expired')`, matching the shipped query.
  Correcting that fidelity defect and giving leaked occupancy a durable backing across the
  four sites wants a problem statement of its own, and this proposal opens none of those
  sites.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0080 (inventory of the residues 0073 recorded and deferred) | Draft, stages no changes. Its own `Date` line reads 2026-08-31 and the last commit to the file is 2026-09-06, which records when someone touched the file rather than when it was reviewed. It heads itself an unconverged inventory rather than a design. | **§1.2 is the entry this proposal promotes, and it is discharged in its entry-removal half only.** The staged reclaim removes the registry entry for both of §1.2's classes, together with the per-slot tree, the slot's credential directory and the armed §4.9 expiry timers, so the held inbound count and the pinned drain gate lose their subject. §1.2's third clause survives with a different cause: a later session on the pod arms neither the platform nor the connector MCP server, because `claimSessionSlotUnderLock` inserts the claimant's own entry and then gates the arming on `len(s.slots) != 1` (`pkg/adapter/slotsession.go:75-110`), which two ordinary interleaved binds already miss with no failed bind anywhere. That clause is recorded under `## Defects in the shipped tree that this proposal does not stage` and is not taken here. **§1.12's denominator moves and its numerator does not.** `tests/claim-map.json` carries 76 rows today, 20 `ABSENT`, 24 `UNWIRED` and 32 `WIRED`. SCHEMA-1 adds two `WIRED` rows, so §1.12's "seventy-six" becomes seventy-eight while its twenty `ABSENT` rows, its two owned rows and its eighteen with no owner stand, and §1.18's twenty-four `UNWIRED` rows are untouched. **§1.19 keeps its class set and moves only membership.** `boundSlotState` and `checkSessionBound` are untouched. The membership of the sets they read changes in three ways. A fence for a session whose bind failed and whose reclaim completed meets the absent-entry refusal rather than the unbound-entry refusal, and so does a fence for a session whose `StartSession` or `Resume` rolled back because the reclaim landed after its claim, since the reclaim removed the entry and the rollback removes nothing further. Where a racing start's own claim re-created the entry after the reclaim, the entry is present and its `sessionID` is set, so the fence meets neither refusal and an abandoned session answers as a bound one. After a reclaim the adapter did not acknowledge on a create-time-reserved slot, a surviving entry is present-and-unbound (workspace-preparation residue) or present-and-bound (credential-assignment residue) for a session the gateway has abandoned, so a fence in that window meets the unbound-entry refusal or neither refusal, for a session 0080 would classify as gone. The placement constraint also narrows one class: with a pod appended to `ExcludePods` after an unacknowledged reclaim, a retry the §5.2 slot retry policy places issues no fence to a pod that may still hold the prior attempt's entry, which does not reach a §15.1 start onto a create-time-reserved slot. The rollbacks CODE-2 adds answer `codes.Aborted` rather than `codes.FailedPrecondition`, so they put no further meaning on the status code §1.19 is disambiguating. The ABA class narrows rather than closing. Where a successor's claim creates the entry at a fresh bind epoch, the abandoned attempt's own start confirmation refuses its start against the epoch its claim observed, so that entry does not stand for an abandoned session and the third case leaves the inventory §1.19 has to classify. Where no successor exists and the abandoned attempt's own claim re-created the entry after the reclaim, the confirmation compares equal and admits, so the case this row states three clauses above stands unchanged. The reclaim hold adds one more producer of an arm §1.19 already enumerates rather than a class of its own: a fence naming a session whose slot identifier is held carries no registry entry, and `boundSlotState` answers the absent entry and the unbound entry alike with one `codes.FailedPrecondition` (`pkg/adapter/slotsession.go:274-283`). The hold's own refusal is outside §1.19's inventory, because it is `errSlotReclaimInProgress`, a `codes.Aborted` raised at the top of `ensureSlotStateLocked`, which serves the bind-sequence and workspace RPCs and is never reached from `CoordinatorFence` (`pkg/adapter/coordination.go:116`). It carries neither the status code nor the RPC §1.19 is scoped to. The class set is therefore unchanged and only the membership of its classes moves. **§1.7 keeps its subject and grows the spec side of its divergence.** §1.7 keeps its subject and both of the closures 0073 named, and the tree side of the divergence is untouched. `slothealth.UnhealthyThreshold`'s clamp of a sub-1 denominator to 1 and `drainLedger.RecordLeak` are unmodified, and no staged code produces a `leaked` disposition on an exclusive pod: the §7.3 re-attach accounting is gated on a non-empty slot id, which an exclusive pool never reserves, and the other two accounting callers sit behind `maxConcurrentSessions > 1`. The spec side grows from the two sites §1.7 names. SPEC-2's §7.1 paragraph and SPEC-3's §5.2 scrub-model append each restate that the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy, and each states pod retirement under §6.2's pre-attached failure disposition as the exclusive-pod alternative. **§1.1, §1.3, §1.4, §1.5, §1.16 and §1.20 have no conflict of subject.** No conflict of subject. The adapter edits are placed in `session.go`, `runtimegeneration.go`, `resume.go`, `sdkwarm.go` and the new `bindepoch.go`. `slot.go` and `slotsession.go` are now opened as well, which the earlier reading of this row said they would not be: `ensureSlotStateLocked` mints the epoch and refuses the hold, `claimSessionSlot` and `claimSessionSlotUnderLock` report the epoch their critical section captured, and `slotState` gains an `epoch` field. Each edit is small and localised, so the later position's rewrite of those two files re-lands a mint, a refusal, a returned value and a struct field rather than a mechanism. | Re-derive four entries when 0080 is triaged into successor proposals. Split §1.2 so its MCP-arming clause returns to the inventory as a gap this proposal does not take, and record the rest of §1.2 as discharged here. Re-derive §1.19's membership against the remaining cases and the narrowed ABA class, its class set being unchanged. Re-derive §1.7 against four spec sites rather than the two §6.2 and §5.2 sites it names. Re-derive §1.12's denominator as seventy-eight. Re-land the epoch mint, the hold refusal, the claim functions' epoch return and the `slotState.epoch` field when the rewrite of `slot.go` and `slotsession.go` happens. No edit to 0080's staged content, because it stages none. |
| 0073 (give every session a slot) | Implemented (2026-08-31 per its own status line; spec applied 2026-08-19) | This change touches 0073 in two ways. 0073 recorded this gap in its §9 recorded limits and declined to discharge it, and this proposal discharges it. SPEC-1 also retires the third sentence of §4.1's `ShutdownRequest` paragraph, which 0073's SPEC-7 authored and which reached `spec/04_system-components.md:157` in commit `f37e867b8`. That sentence states one per-session teardown gated on a bound entry; after the split there are two teardowns with two preconditions, and the slot release is gated on the entry being present. The paragraph's first two sentences, its session-scoped classification, and its closing rule are preserved. SPEC-4 and CODE-3 extend 0073's per-slot sub-state fence (`spec/06_warm-pod-model.md:150-155`) and its `pkg/sandbox/slotstate` edge list with one new edge, which adds to that list rather than retracting from it. | Nothing. A landed proposal is not edited, so this row is the record of what this proposal takes back from 0073. |
| 0075 (derive message scope from the address type) | Implemented (2026-09-08 per its status file's `implemented-date`) | 0075's SPEC-1 replaced the §4.1 block around the `ShutdownRequest` paragraph and reserved the paragraph itself, stating that it "stands unedited" because it explains a divergence between what a request addresses and what its handler touches that 0075's D3 rests on. SPEC-1 here rewrites that paragraph's third sentence. The ground D3 rests on survives: the first two sentences carry the session-scoped classification and the single address unchanged, and the replacement keeps the whole-pod-scrub clause and the closing rule that no operation is selected by a field's presence standing in for a scope. Nothing 0075 landed is opened, including the derivation rule at `spec/04_system-components.md:151-155`, its tier-0 addressing gate, its tier-3 session-address suite, and its `tests/spec-map.json` entries. The new `ShutdownRequest.expected_bind_epoch` field does not disturb that rule: it is a bare `int64` present on every `ShutdownRequest`, as the `coordination_generation` fence on the same message already is, so no operation is selected by a field's presence. SPEC-1 therefore states nothing about the epoch in §4.1, and the §4.7 `Shutdown` row carries every precondition. | Nothing. |
| 0078 (keep the pod's runtime listener across a session teardown) | Draft for review (2026-08-25 per its own `Date` line) | No deliverable of 0078 loses its subject. CODE-1, CODE-2, TEST-1 through TEST-7 and DOCS-1 all keep theirs, because this proposal opens neither `pkg/adapter/socketruntime.go` nor `cmd/lenny-adapter/main.go`. Two effects are real. The staged co-tenancy-hazard case adds a sibling assertion in `pkg/adapter/socketruntime_test.go` beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (`:252`), inside the block 0078's TEST-1 through TEST-4 and their cleanup conversion at `:258` and `:316` rewrite. 0078's CODE-1 replaces the `return p.listener.Close()` at `pkg/adapter/socketruntime.go:467` that the assertion's listener half records, so that half stops discriminating once 0078 lands, while its connection-close and child-kill halves stand and this proposal's own CODE-1 `started` gate is still required. This proposal also extends `tests/tier4_integration/concurrent_workspace_test.go`, whose cleanup at `:126` 0078's TEST-5 converts, and adds a case to `tests/tier7a_load_local/`, where 0078's TEST-6 and TEST-7 land; it edits no file 0078's TEST-7 rewrites, so that last overlap is package co-location rather than a file collision. The tier-4 assertion that the pod's listener survives holds on the shipped tree independently of 0078, because alice is still active when bob's compensation runs and `Close` takes the sibling early return (`pkg/adapter/socketruntime.go:441-446`). The row previously stated that 0078 widens the exposure by making a pod serve more sessions. 0078's own fixed decision states the opposite, that after it lands a recycling sidecar pod still serves one session and fails at the accept timeout, so that premise is withdrawn. | Land after this one, and amend the sibling assertion's listener half when 0078's CODE-1 removes the listener close. |
| gateway-runtime-comms remediation, step R1b | Programme step | SCHEMA-1 opens `schemas/lenny-adapter.proto` under rule S-2's second window rather than against R1b's reservation. S-2 reserves the first window to R1b and states that a later step needing a field the plan did not enumerate opens a second narrow window, whose precondition is that every in-flight `pkg/adapter` handler edit has merged first. R1b's end state is in the tree, and the file has already been reopened once, for proposal 0076's comment-only edit. The edit is additive, so the baseline R1b recorded is extended rather than reopened: no field is removed, none is renumbered, and no identifier R1b renamed is touched. The precondition binds this proposal's own step ordering, because it opens three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`), so SCHEMA-1 and its regenerated stubs land in one commit before those handler steps. | Record the second window against S-2, so the steps that plan against the generated types (R12, R15, R16, R17, R22, R23) plan against the regenerated set rather than against R1b's. |
| gateway-runtime-comms remediation, step R12 | Programme step | The hold-timeout reclaim of unstarted slots and its §10.1.4 statement are left to R12, which builds the control-stream consumer that arms the hold and owns the gateway-side whole-pod-loss response. | Take both halves when it builds the consumer, together with an in-flight-upload guard. |

## Deliverable index

- SPEC-1 — spec/04_system-components.md, spec/29_communication-scenarios.md — §4.1's scope sentence and the §4.7 `Shutdown` row state the slot release and the runtime teardown as two teardowns with two preconditions, and state the no-op answer for a session the adapter holds nothing for; the §4.7 row also states the epoch comparison and points at §4.7.1, which states the three outcomes; §29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7.
- SPEC-2 — spec/07_session-lifecycle.md, spec/06_warm-pod-model.md, spec/05_runtime-registry-and-pool-model.md, spec/04_system-components.md — §7.1 gains the failed-bind pod-side reclaim obligation, the rule that the reclaim names the attempt it compensates by carrying that attempt's bind epoch and never re-dials, the meaning of the superseded and absent answers, and the `leaked` disposition, as a paragraph of its own covering the creation, start, and re-attach bind attempts; §7.2's mid-resume snapshot-close sequence runs the reclaim before the replacement pod is released and drops the premise that no runtime was started on it; §7.3's resume flow, §6.2's mid-resume cancel edge, and §4.7.9 step 5 point at it; §5.2's slot-retry `**Max retries:**` bullet states the placement constraint, naming a pod holding an incomplete reclaim among the conditions that place a retry on a different pod.
- SPEC-3 — spec/05_runtime-registry-and-pool-model.md — §5.2's slot-cleanup action list names the slot's credential directory and the §4.9 timer cancellation, and its scrub model covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, states one cleanup-outcome report per session release, and states the slot identifier's reclaim hold, from the deregistration of the registry entry until the cleanup finishes, together with the transient refusal of a bind onto a held identifier.
- SPEC-4 — spec/06_warm-pod-model.md — §6.2's per-slot sub-state machine gains the `receiving_uploads → slot_cleanup` edge and the pre-`running` cleanup paragraph, which points at §5.2's reclaim hold for what refuses a bind onto the slot's identifier while its cleanup runs.
- SPEC-5 — spec/04_system-components.md, spec/15_external-api-surface.md — §4.7.1 gains the bind-epoch block after the RPC tables, stating what the adapter mints, which bind-sequence responses report it, that no bind-sequence request carries one, that it is not a coordination generation, and which epoch a caller may name; §15.4 gains the published bind-epoch contract and the slot-identifier reclaim-hold contract, each written to be implementable without reading Go and each with its non-conformance statement.
- SCHEMA-1 — schemas/lenny-adapter.proto, scripts/seed-claim-register.py, tests/claim-map.json — the additive `SlotReclaimOutcome` enum and nine fields: `ShutdownRequest.expected_bind_epoch`, `ShutdownResponse.slot_reclaim`, and `bind_epoch` on the seven bind-sequence responses; two `WIRED` claim-register rows.
- CODE-1 — pkg/adapter/session.go, pkg/adapter/runtimegeneration.go — `Shutdown` refuses a reclaim naming an epoch the entry does not carry, takes the reclaim hold in the deregistration's own critical section with a deferred release, releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given.
- CODE-2 — pkg/adapter/runtimegeneration.go, pkg/adapter/session.go, pkg/adapter/resume.go, pkg/adapter/sdkwarm.go — `noteRuntimeStarted` takes the epoch its own claim observed and confirms the registry still holds an entry bound to this session at that epoch, which is the only check covering the window between the claim and the runtime record; `StartSession` and `Resume`, the RPCs whose admitted start the gateway's compensation can race, roll back when the reclaim landed after their claim.
- CODE-3 — pkg/sandbox/slotstate/slotstate.go — the per-slot edge list and its doc comment gain the `receiving_uploads → slot_cleanup` edge.
- CODE-4 — pkg/gateway/podlifecycle/podsession/slotbinder.go, binder.go, slotfailure.go — the gateway sends the compensating `Shutdown` at every post-connection bind failure and at a failed `Resume`, carrying the latched bind epoch and never re-dialling, maps the answer outcome-first onto the `leaked` disposition, and releases the session's credential leases on the bind paths.
- CODE-5 — pkg/gateway/sessionserver/start.go, pkg/gateway/podlifecycle/podclaim/slotclaimer.go — one account-classify-drain helper serves every bind path the §7.1 obligation binds, so the create-time reserved path and the §7.3 re-attach reach the §5.2 threshold, and the retry after an incomplete reclaim carries `ExcludePods` so `ClaimSlot` places it on a different pod, and `isTransientPodClaimError` gains a `codes.Aborted` arm so a §7.3 resume refused by the reclaim hold or rolled back by CODE-2 reverts the row to `awaiting_client_action` rather than being demoted to terminal `failed`.
- CODE-6 — pkg/adapter/bindepoch.go, pkg/adapter/server.go, pkg/adapter/slot.go, pkg/adapter/staging.go, pkg/adapter/slotcreds.go, pkg/adapter/slotsession.go, pkg/adapter/sdkwarm.go, pkg/adapter/holdstate.go, pkg/gateway/runtime/adapterclient/client.go — the epoch counter and its lazy seed, `slotState.epoch`, the reclaim-hold side table and the single `reclaimSlotLocked` helper every deregister-then-destroy site takes it through, the transient refusal sentinel, the shared slot-resolve helper the five `InvalidArgument` re-wrap sites move to, the epoch on the seven responses, and the client's per-connection epoch latch with its clear on `DemoteSDK` and on a `RECLAIMED` reclaim, `BindEpoch`, and `ShutdownReclaim`.
- CONF-1 — tests/tier10_conformance/bind_epoch_conformance_test.go — the four properties of §15.4's published bind-epoch contract.
- DOCS-1 — docs/reference/state-machines.md — the per-slot sub-state table gains the row matching the §6.2 edge.
- DOCS-2 — docs/reference/adapter-contract.md — the `Shutdown` row states the two teardowns with their two preconditions, the withheld cleanup-outcome report, the drain gate's own condition, the bind-epoch precondition and the clean-exit answer; the page gains the bind-epoch and reclaim-hold block for third-party adapter authors; and the `DemoteSDK` row states that the demotion drops the adapter's slot registry entry.

Tests are not separate deliverables, with one exception. Each implementation step carries the
tests for the tiers it reaches, specified per deliverable under `## Testing` in the non-spec
changes file. CONF-1 is listed above because a conformance battery for a contract §15.4
publishes to third-party adapter authors is the deliverable that makes that contract
checkable, rather than the test coverage of another deliverable.
