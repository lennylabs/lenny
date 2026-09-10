# Spec changes: A failed session bind leaves a stale adapter slot registry entry

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The specification currently states the defect as the contract. §4.1's derivation paragraph
and the §4.7 `Shutdown` row both treat the runtime close and the slot release as one act
gated on the binding, §4.7.9 states the bind sequence with no failure branch, and §6.2's
per-slot sub-state machine has no edge into `slot_cleanup` from either pre-`running` state.
Four statements are needed, each on a surface that already owns the subject.

**One teardown becomes two, each with its own precondition.** A `Shutdown` for a session
does two separable things. The **slot release** is the §5.2 slot cleanup for that slot: it
reclaims the slot's per-slot tree, its credential file, its armed §4.9 expiry timers, and
the `slotId`. It runs whenever the adapter holds an entry for the named session, whether or
not `AssignCredentials` bound that entry. The **runtime teardown** flushes the session's
final usage report and closes the runtime, and it runs only for a session whose start the
adapter has admitted, taken from the moment the adapter admits the start rather than from
the moment the session reaches the runtime, so a start still in flight is torn down rather than
skipped. The slot's `running` sub-state and the cleanup-outcome report take
the later moment, when the pod's shared runtime process has been given the session, so the
teardown over-approximates toward closing while the report under-approximates toward not
counting. The §15.4.2 graceful-shutdown signal precedes the close inside the runtime
teardown and carries a second condition of its own: the signal is pod-global and names no
session, so it goes out only when the deregistration leaves the adapter holding no bound
entry. A request naming a session the adapter holds no entry for
removes nothing, runs neither teardown, and answers with a clean-exit response. That last
sentence is what makes the compensation safe to send at a stage where the adapter may hold
nothing.

**The gateway owes a pod-side reclaim on a failed bind.** §7.1 already states the atomicity
of session creation and already routes a finalize-block failure to §6.2's pre-attached
disposition, which governs the pod. It does not state what happens to the state the failed
attempt created on the pod. The obligation binds any gateway-issued bind attempt rather than
session creation alone. The creation finalize block, the §15.1 start transition, and the
§7.3 re-attach onto a replacement pod each issue the same pod-side RPCs and each leave the
same residue behind, so the rule is stated as its own §7.1 paragraph rather than inside the
atomicity paragraph. That paragraph is scoped by its own heading to steps 2 through 8 and
closes by stating that the client never receives a `session_id` for a session that failed to
fully initialize, and neither holds for a re-attach of a session whose row is already
persisted. §7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph
and restate nothing. The obligation begins with an attempt's first pod-side RPC and ends when
that attempt succeeds: the gateway sends `Shutdown` for the session on the connection the
failed stage still holds, before that connection closes, and releases the slot reservation
afterwards. It sends the reclaim even when the failing RPC's own context is already
cancelled or past its deadline, because that is the case in which the adapter may have
started the session. A reclaim the adapter does not answer, and one answered `reclaimed`
without reporting a clean exit, are the reclaims that did not complete. On a pod serving
concurrent sessions a reclaim that did not complete leaves the slot `leaked` under §6.2's
existing disposition, which holds the slot's occupancy and counts it toward the §5.2
whole-pod replacement trigger.
On a pod serving one session the failed attempt releases the pod's claim and the pod retires
under §6.2's pre-attached failure disposition, so the residue dies with the pod and the
`leaked` disposition does not arise there, which matters because §6.2 offers the `leaked`
sub-state and §5.2 the replacement trigger only under concurrent occupancy. The slot
identifier is the session identifier, and §5.2 states that the adapter holds that identifier
from the deregistration of its registry entry until the cleanup that reclaims it has
finished, so a further attempt at the same session on that pod meets that hold while the
reclaim runs and is refused as a transient condition, so an attempt that meets the hold is
one the retry policy did not place. Where a retry lands is §5.2's subject rather than §7.1's, so §5.2's
`**Max retries:**` bullet states that constraint itself: a pod holding a reclaim for this
session that did not complete is not a placement for a retry that policy places, beside
the saturation and health conditions the bullet already names. §7.1 states the
reclaim and its disposition and no placement rule, so the constraint reaches exactly the
retries §5.2 places and nothing about an attempt's retryability changes. A §15.1 start onto a
slot reserved at creation is placed by neither mechanism, and the residue that leaves is
recorded among the accepted failure modes below rather than governed by a rule no layer
enforces. No failure class becomes non-retryable, so §5.2's non-retryable categories,
§6.2's pre-attached retry policy, and §15.1's error catalog are unchanged. The specification
states no deadline for the reclaim; §5.2's per-slot cleanup timeout is the figure the gateway
reuses and the code cites.

**The cleanup-outcome report follows the `running` boundary.** §5.2's scrub model already
states that a per-slot cleanup runs on every session release and is reported via
`ReportSessionScrub`. A bind abandoned or failed after its slot enters `receiving_uploads`
and before it reaches `running` is a cleanup on a slot the pod's shared runtime process was
never given, and it reports no outcome, because `ReportSessionScrub` advances the pod's
served-session count and that count records the sessions the process was given. Reporting
for a slot that never ran would advance
`recycle.maxSessionsPerPod` retirement for a session the pod did not serve and would
double-count a session a §5.2 retry re-binds. The rule lands in §5.2's scrub-model paragraph
rather than in its `**Slot cleanup:**` bullet, because that bullet sits under a heading scoped
to `maxConcurrentSessions > 1` while the rule holds on a pod of either concurrency. One
cleanup-outcome report per session release follows, filed by the cleanup that reclaims the
slot, so a start the adapter completes after that cleanup files none.

**The state machine gains the edge the window needs.** A slot reaches `running` when the
pod's shared runtime process has been given the session with its session identifier, so
every stage of the §4.7.9 step-5 bind sequence before that point, credential assignment
included, and a start still in flight, whose session has not yet reached the runtime, leave
the slot in `receiving_uploads`. The new edge, `receiving_uploads →
slot_cleanup`, covers the residue a bind leaves before the runtime is given the session: the
workspace-preparation and setup stages, the credential-assignment stage, and a start still in
flight. A session the runtime was already given is a slot in `running`, and it takes the
`running → slot_cleanup` edge §6.2 already carries. No new state is added, so the existing
terminals and the existing `leaked` semantics paragraph stay authoritative for the new path.

**The reclaim names the attempt it compensates.** The gateway sends the compensation after it
has stopped waiting for the attempt that failed, so the reclaim can reach the adapter after
the client's own retry has already bound the slot and started its session. The slot
identifier is the session identifier, so the retry's registry entry is indistinguishable
from the abandoned attempt's on identity alone, and an unfenced reclaim tears down a session
that is serving its client. §4.7.1 states the **bind epoch** for that reason. The epoch names the registry entry the adapter holds for a slot identifier. It is a positive
integer and zero is not one, so a request whose bind epoch is zero carries none. The adapter
mints it when it creates a registry entry for a slot identifier it holds none for, and the
entry carries it unchanged until it is released, so an entry created after a reclaim was
dispatched carries a different one and the lagging reclaim compares unequal.
Every bind-sequence response reports the epoch of the entry the call resolved. No
bind-sequence request carries an epoch and none is refused on one, so a
[§7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload, which reuses
`PrepareWorkspace` and `FinalizeWorkspace` on the binding the session already holds, is
answered at the entry's epoch under the same rule as any other resolving request. A caller
holds the most recent epoch a response on its own connection reported, and holds none once
it has itself issued an RPC on that connection that removes the entry the epoch names, so a
caller that holds none sends the epochless form.

A `Shutdown` that names an epoch performs the slot release and the runtime teardown only when
the entry the adapter holds for the named session carries that epoch, and performs neither
when it does not. It is answered `superseded` when a different attempt owns the identifier
and `absent` when the adapter holds no entry for the session, and both are completed reclaims
that leave the slot unleaked. On `absent` the adapter holds nothing for the session. On
`superseded` a later attempt owns the slot identifier and whatever the naming attempt created under it, and the entry the adapter now holds was created
after the naming attempt's entry was released, so the naming attempt owns nothing the reclaim
could release. A `Shutdown` that
names no epoch is the unconditional teardown, which is what every caller other than a fenced
compensation sends, including a compensation for an attempt that observed no epoch. The epoch
is a bare scalar precondition on the request, carried as the `coordination_generation` fence
on the same message already is, so §4.1's rule about a field's presence standing in for a
scope is not engaged and §4.1 states nothing about the epoch. The refusal the reclaim hold produces is transient, so nothing about an attempt's
retryability changes.

**The epoch names the registry entry, and the adapter holds the slot identifier while its
cleanup runs.**
The bind epoch names the registry entry a reclaim is addressed to, so a reclaim addressed to
an entry that was already released performs nothing. The epoch says
nothing about a reclaim that is admitted and then races a successor while it tears state
down, because the destructive steps read no registry state: the workspace directory, the
credential directory, and the process group are each named from the slot identifier,
which every attempt at the session shares. §5.2 therefore states the reclaim hold: the
adapter holds the slot identifier from the critical section that deregisters the
registry entry until the cleanup that reclaims it has finished, the hold outlasts the
deregistration, and while the identifier is held the adapter admits no bind onto it and
refuses one as a transient condition. §6.2's `slot_cleanup` sub-state begins when the
gateway observes the failure, earlier than the deregistration the hold starts at, so
§6.2 points at the hold rather than restating its window. The epoch closes the late reclaim and the hold closes the bind that arrives mid-cleanup.
Neither closes the other's case.

## Edge cases and accepted failure modes

- **A compensation for a session the adapter holds nothing for.** `stageWorkspace` sends
  `PrepareWorkspace` only when the plan carries uploads, so a workspace-preparation failure
  on an upload-free plan leaves no adapter entry. The new no-op sentence in the §4.7
  `Shutdown` row makes this a clean-exit response, so the failure is accounted transient
  rather than leaked and adds nothing to the pod's persistent leak count. The windowed
  failure counter still records it, and at `maxConcurrentSessions: 2` a single windowed
  failure already reaches the §5.2 whole-pod replacement threshold.
- **A reclaim the adapter does not acknowledge on a pod serving concurrent sessions.** The
  slot is `leaked`. Its occupancy is held, it counts persistently toward the §5.2 threshold,
  and the pod retires through the shipped trigger. This is §6.2's own disposition applied
  consistently rather than a new rule, and it is what makes a best-effort compensation's
  failure bounded. A retry the §5.2 slot retry policy places goes to a different pod; when
  that pod is the pool's only candidate the retry meets the shipped `WARM_POOL_EXHAUSTED`
  outcome.
- **A client retry of the §15.1 start after a failed bind on a create-time-reserved slot.**
  The row keeps its §4.6 pod binding, so the retried start reconnects to the same pod under
  the same slot identifier, and the §5.2 retry policy does not place this attempt, so its
  placement constraint does not reach it. The epoch and the reclaim hold are what govern this
  path instead, and three orderings arise. A retry that arrives while the first attempt's
  cleanup is still running is refused on the reclaim hold, as a transient condition,
  and binds on a further attempt. A retry that arrives before the lagging reclaim does meets the abandoned attempt's surviving
  entry. Its workspace, setup and credential RPCs resolve that entry at the epoch the entry
  already carries, whether or not its session has started, because the adapter refuses nothing
  on an epoch. A retried `StartSession` onto an entry whose session has already started is
  refused on the started-session condition, which is not an epoch rule and is not deleted here,
  and that transient refusal consumes one of the client's own retries. A retry that arrives after the cleanup
  finished materializes the slot afresh and owns it at a fresh epoch. Where the retry created a fresh entry, the previous attempt's reclaim names an epoch the
  adapter no longer holds, so it is answered `superseded` or `absent` and performs neither
  teardown, and neither the tree that retry staged nor the session it started can be taken down
  by the previous attempt's reclaim. Where the retry adopted the surviving entry, it holds that
  entry's own epoch, so the reclaim compares equal and removes it, and what that costs turns on
  the ordering. A reclaim whose critical section runs while the retry is still mid-sequence
  makes the retry fail its stage and retry again. A reclaim whose critical section runs after
  the retry started its session and the gateway answered its client tears that session down:
  the entry compares equal, so the runtime session is closed and the slot's directories are
  removed under a session that is serving, and the gateway records the reclaim as a clean
  `reclaimed`. That ordering is the shared-entry residue the accepted failure modes record
  below, where what closing it would cost is stated.
- **A retry that meets the reclaim hold spends an attempt on it.** The refusal carries the
  `ABORTED` status §15.4 publishes as the transient classification, so the attempt keeps its
  §5.2 retryability. On the §7.3 resume the gateway classifier the non-spec changes amend is
  what holds the row in `awaiting_client_action` for the client's retry. The retry the §5.2 policy places does not meet
  the hold: the retry after an unacknowledged reclaim excludes that pod, and an acknowledged
  reclaim is answered only after its cleanup finished. What can meet it is a §7.4 mid-session
  upload still in flight when the session's own teardown opens the hold, a client-driven §7.3
  resume, which carries no pod exclusion, and, on a create-time-reserved slot, the client's own
  retry of the §15.1 start, which consumes one of the retries the client has. The wait is the
  graceful window the reclaiming request carried when it carried one, that request's own
  deadline when it carried none, plus the removal of the slot's directories. This is accepted
  rather than closed: a gateway-side wait-and-retry inside the bind path holds the client's
  request open for the same window and adds a second place where the timeout is stated.
- **A bind that fails inside its first entry-creating RPC reclaims unfenced.** An attempt
  whose first entry-touching RPC fails may have created the registry entry and still received
  no response carrying its epoch, so it holds none and sends the unconditional form. The
  window is one RPC wide and it precedes any successor, because the attempt has not yet
  released the slot identifier to anything else, but a reclaim sent in it is not fenced. The
  fence for that window would have to travel on the failing RPC's error rather than on its
  response, which is a wider change to the gateway-adapter error surface than this proposal
  stages.
- **A reclaim the adapter does not acknowledge on a pod serving one session.** The failed
  attempt releases the pod's claim and the pod retires under §6.2's pre-attached failure
  disposition, so the residue dies with the pod. §6.2 states the `leaked` sub-state and §5.2
  the whole-pod replacement trigger under concurrent occupancy alone, so the disposition is
  not stated for an exclusive pod and is not needed there.
- **A start that races the reclaim.** Three orderings arise. When the adapter admitted the
  start before the reclaim removed the entry, the start finds its entry gone at the point it
  would record the runtime as holding the session, takes the session back off the shared
  runtime process, and refuses; the slot never reached `running` and no cleanup outcome is
  reported. When the reclaim's answer precedes the start, the cleanup has finished and the
  hold is released, so the start's own claim creates a fresh entry and mints a fresh epoch.
  The confirmation compares the epoch that same claim observed against the entry that carries
  it, so it compares equal and the start is recorded: the slot reaches `running` and the pod
  is left holding an entry and a runtime session no gateway attempt owns. Nothing refuses that
  ordering, and it is recorded among the accepted failure modes rather than closed. When a
  successor has already taken the identifier, the abandoned attempt's claim resolves the
  successor's entry rather than one of its own; a successor whose session has started refuses
  it on the started-session condition, and a successor whose session has not started shares
  one entry and one epoch with it, which is the shared-entry residue the accepted failure
  modes record. The confirmation covers the window between the start's claim and the record of
  the runtime holding the session, and what it refuses there is a start whose claimed entry
  has been replaced by another attempt's.
- **A compensation still on the wire when a retry adopts the surviving entry is not fenced.**
  The epoch separates a reclaim addressed to a released entry from one addressed to the entry
  that replaced it. It does not separate two attempts sharing one surviving entry: when a
  compensation errors at the gateway while the adapter is still processing it, and a retry
  reaches the same pod and the same slot identifier before the teardown removes the entry, the
  retry resolves that entry at the epoch the entry already carries and the teardown compares
  equal. Which interleaving follows decides the cost. A teardown whose critical section runs
  while the retry is still mid-sequence makes the retry fail its stage and retry again, and
  nothing leaks. A teardown whose critical section runs after the retry completed its whole
  sequence tears down a session that has already answered its client: the start confirmation
  spans only the window between the start's claim and the runtime record, so it admits a start
  made on the shared entry, and the teardown then closes the runtime session and removes the
  slot's directories while the gateway records the reclaim as a clean `reclaimed`. Closing
  this needs a per-attempt discriminator, because the epoch does not tell two attempts apart
  while they share one entry: a retry that adopts a surviving entry inherits that entry's
  epoch. Such a discriminator would have to be carried on every request that can create or
  resolve an entry, which this proposal does not stage.
- **An abandoned attempt's start whose claim runs after the reclaim completed re-creates the
  entry.** The cleanup has finished and the hold is released, so the late `StartSession`
  creates a fresh entry, mints a fresh epoch, and starts the session, and the start
  confirmation compares the epoch that same claim observed against the entry that claim
  created, so it admits. The pod is left holding an entry and a runtime session no gateway
  attempt owns, which is residue class one reached by a second route. Closing it would need
  the adapter to hold the slot identifier until the gateway's reclaim has been answered, which
  keeps an identifier held across a network round trip on every failed bind and refuses the
  client's own retry for that whole window. The ordering needs the abandoned attempt's
  `StartSession` to reach the adapter after its own compensation completed, which the
  gateway's sequential bind stages make rare rather than impossible. The tier-1 hold suite
  pins it as a residue.
- **A bind abandoned at the connect stage.** The slot is reserved before any workspace RPC,
  so the adapter holds nothing and no reclaim is owed. §6.2 still has no terminal out of
  `slot_assigned`; that hole is pre-existing, and the summary records it, the widening this
  change brings on the create-time-reserved path, and why neither is closed here.
- **The graceful-shutdown signal on a co-tenanted pod.** A bound-but-unstarted co-tenant is a
  bind about to issue `StartSession` against the shared runtime, so the signal stays gated on
  the binding rather than on `started`. A pod that keeps a bound co-tenant sends no signal for
  the session being reclaimed. This is the shipped behaviour, restated so the two teardowns
  do not read as sharing one precondition.

## Staged edits

### SPEC-1 · spec/04_system-components.md § 4.1 (Request Message Scope)

Under `#### Request Message Scope`, in the paragraph beginning "`ShutdownRequest` is
session-scoped and carries one address.", replace the third sentence only. The sentence to
replace reads, verbatim:

```
The handler runs the per-session teardown when the adapter holds a bound entry for the named session and runs the whole-pod scrub when the recycle disposition is set, so neither operation is selected by a field's presence standing in for a scope.
```

Replace it with:

```
The handler runs the slot release and the runtime teardown under the preconditions [Section 4.7](#47-runtime-adapter) states, and runs the whole-pod scrub when the recycle disposition is set, so no operation is selected by a field's presence standing in for a scope.
```

No sentence about the bind epoch is added here. `expected_bind_epoch` is a bare `int64`
and is therefore present on every `ShutdownRequest`, exactly as the `coordination_generation`
fence the same message already carries is (`schemas/lenny-adapter.proto:1630-1635`). This
subsection's closing rule forbids an operation selected by a field's presence standing in for
a scope, and a field that has no wire presence cannot engage it, which is why the paragraph
states nothing about `coordination_generation` either. The replacement sentence therefore
delegates both teardown preconditions to §4.7, which owns them, and states no epoch rule.

The paragraph's first two sentences are untouched and this edit retires no vocabulary. The
second sentence ("The per-slot teardown and the whole-pod teardown are the same operation on
the same address, and what remains is the recycle disposition the request carries beside it.")
states where the work is addressed. That is what the subsection needs from it, because the
subsection derives a message's scope from its field set
(`spec/04_system-components.md:151`) and the paragraph's closing clause concludes that no
operation is selected by a field's presence standing in for a scope. The split names each
operation, leaves each precondition to §4.7, and leaves the address-sharing claim true. The second
sentence's two names already disagree with the shipped third sentence's "per-session
teardown" and "whole-pod scrub" before this edit applies
(`spec/04_system-components.md:157`), so that mismatch belongs to the shipped paragraph and
the implementor changes neither sentence here.

### SPEC-1 · spec/04_system-components.md § 4.7 (Gateway → Adapter RPC table, `Shutdown` row)

Replace the `Shutdown` row's first sentence and add the no-op sentence. The row currently
opens, verbatim:

```
| `Shutdown` | Graceful end-of-session teardown of the named session: the adapter closes that session's runtime and releases the session's slot. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

Replace that opening with the text below, leaving the remainder of the row (from "On the
default disposition the pod is replaced." to the end) unchanged:

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response. The request may carry the bind epoch [Section 4.7.1](#471-role-and-gateway-rpc-contract) states, which is a precondition on the whole request: when the request carries one, the adapter performs the slot release and the runtime teardown only if the entry it holds for the named session carries that epoch, and performs neither when it does not. The response reports which of the three outcomes [Section 4.7.1](#471-role-and-gateway-rpc-contract) states the reclaim took. A superseded reclaim is neither a failed reclaim nor a leaked slot: the entry the adapter now holds was created after the naming attempt's entry was released, so the naming attempt owns nothing the reclaim could release, and the reclaim correctly performed nothing. A request carrying no epoch is the unconditional teardown, which is what every caller other than a fenced compensation sends, including a compensation for an attempt that observed no epoch, and its response reports `reclaimed` for an entry it removed and `absent` for a session it holds none for. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

The last of the added sentences is what keeps every existing caller defined: the §11.4
revoke fan-out, the occupancy-zero recycle edge, and `Binder.ReleaseSlot` all send the
unfenced form, and without that sentence the row would state an outcome only for a request
that carries an epoch.

The row does not enumerate the slot release's actions; §5.2 owns that list. It does not name
`ReportSessionScrub` either, because §5.2 states both the report and the pre-`running` exception
after SPEC-3.

### SPEC-1 · spec/29_communication-scenarios.md § 29.4 (session-end step 13)

In §29.4's numbered step 13, append one sentence to the end of the step, after the sentence
ending "([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3)."
The sentence to add reads:

```
The frame is pod-global and names no session, so on a pod serving concurrent sessions it goes out only when the deregistration leaves the adapter holding no bound entry; an end that leaves a bound co-tenant writes no frame and this step does not occur ([§4.7](04_system-components.md#47-runtime-adapter)).
```

Nothing else in the step changes. The operative clause reuses §4.7's own wording verbatim so
the two statements cannot drift into two paraphrases. Step 13 already carries an inline
exception for the Basic-level and Standard-level integration levels, so a second exception in
the same body follows the step's own form.

Step 12 needs no condition of its own and is untouched. §29.4's `**Preconditions.**` paragraph
scopes the whole trace, the interrupt path and the session-end path alike, to a session that has
completed the §29.2 startup sequence, "so the runtime is running"
(`spec/29_communication-scenarios.md:586-588`), and the sentence after it introduces the interrupt
path's further requirement as an addition to that base (`:589-591`). Every session the trace
carries into step 12 therefore has a start the adapter has admitted, so the step's "the adapter
closes the session runtime" (`:697`) stays true under the narrower runtime-teardown precondition
the §4.7 row states. Step 10's clause that `POST /v1/sessions/{id}/terminate` "is valid in any
non-terminal state" is a restatement of §15.1's endpoint precondition table, cited as such
(`:669-674`), and it fixes what the endpoint admits rather than what this trace covers, so it does
not widen the trace past its own preconditions. The authority for leaving step 12 alone is §29.4's
own preconditions paragraph.

### SPEC-2 · spec/07_session-lifecycle.md § 7.1 (Normal Flow)

Two changes at the paragraph beginning "**Atomicity of session creation (steps 2–8).**": one
inside it, and one paragraph inserted after it.

First, replace the parenthetical in this clause. The text to replace reads, verbatim:

```
the gateway rolls back any partially allocated resources (releases the pod claim)
```

Replace it with:

```
the gateway rolls back any partially allocated resources (releases the pod claim, and reclaims the state the attempt created on the pod)
```

Second, insert the block below as its own paragraph immediately after the paragraph the
first change edits, which ends "...its failure mode remains roll-back-without-persist
regardless of the flag." The obligation reaches past session-creation atomicity, binding the
creation finalize block, the §15.1 start transition, and the §7.3 re-attach alike, so it
stands as a paragraph of its own with its own lead-in rather than inside a paragraph whose
heading scopes it to steps 2 through 8. The atomicity paragraph sits inside §7.1's fenced
flow listing, between the step-8 line and the line continuing it, so the new paragraph goes
in the same place, directly below it and before the line resuming `(executionMode,
isolationProfile, scrubPolicy summary)`:

```
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the creation finalize block, the [§15.1](15_external-api-surface.md#151-rest-api) start transition, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session on the connection the failed stage still holds, before that connection closes, and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. The reclaim runs on the connection the failed attempt already holds and never dials a new one, because the epoch the attempt holds is latched off the responses that connection reported to it, so a reclaim that dialled a fresh connection would hold none and would send the unconditional teardown at a pod that may already hold a successor's slot. The reclaim names the registry entry it compensates by carrying the bind epoch the attempt observed on that connection ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)), and it may name no other. An attempt that holds none, because no response carried it one or because it removed the entry the epoch named, sends the unconditional form. A reclaim naming an attempt that no longer holds the slot is answered `superseded`, and one for a session the adapter holds no entry for is answered `absent`; both are completed reclaims and leave the slot unleaked. On `absent` the adapter holds nothing for the session. On `superseded` the entry the adapter now holds was created after the failed attempt's entry was released, so the failed attempt owns nothing the reclaim could release. A reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit, are the reclaims that did not complete. On a pod serving concurrent sessions a reclaim that did not complete leaves the slot `leaked` under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) disposition, which holds the slot's occupancy and counts it toward the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) whole-pod replacement trigger. The slot identifier is the session identifier, and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states that the adapter holds that identifier from the deregistration of its registry entry until the cleanup that reclaims it has finished, so a further attempt at the same session on that pod meets that hold while the reclaim runs and is refused as a transient condition. [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) keeps the retries its slot retry policy places off that pod, so an attempt that meets the hold is one that policy did not place. Nothing about the attempt's retryability changes. On a pod serving one session the failed attempt releases the pod's claim and the pod retires under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) pre-attached failure disposition, so the reclaim's residue does not outlive the pod; the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there. The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated.
```

The paragraph states no deadline for the reclaim. §5.2's per-slot cleanup timeout is the
figure the gateway reuses, cited from the code rather than restated here.

The no-re-dial rule is stated in the paragraph rather than left to the code because the
epoch fence depends on it. An epoch is minted for a registry entry inside one adapter
process, and a caller latches it off the responses one connection reported to it, so a
compensation that dialled a fresh connection would hold none on that connection and would
send the unconditional teardown at a pod a successor may already hold.

### SPEC-2 · spec/07_session-lifecycle.md § 7.2 (Mid-resume terminal transitions — snapshot-close semantics)

Both of §6.2's `resuming` terminal bullets take their step-by-step sequence from this block,
so the obligation §7.1 states has to appear here or the edge resolves through a sequence that
denies it. The edits below cover the `resuming → cancelled` and the `resuming → completed`
edge alike, because §7.2 states one close sequence for both. They are a deletion in the
section preamble, a sentence added to step 2, and a replacement of step 3.

First, delete the section preamble's premise sentence. The text to replace reads, verbatim:

```
onto a replacement pod. Because the replacement pod has not yet reached `attached` — the agent runtime has not been started or reconnected — there is no live workspace on the pod to seal. The gateway handles
```

Replace it with:

```
onto a replacement pod. The gateway handles
```

The deleted sentence's conclusion is what SPEC-1, SPEC-3 and SPEC-4 falsify. The adapter's
re-attach claims the slot, replays the workspace and reaches the runtime start on its own,
before the gateway observes the re-attach, so a replacement pod short of `attached` may hold
a started runtime and the pod's not having reached `attached` no longer entails that it holds
nothing live. Skipping the live seal stays correct, and step 2 states why on its own terms
below: the sealed workspace is the checkpoint the aborted replay was carrying, and whatever
that replay left on the pod is reclaimed under §7.1 rather than sealed. The preamble
therefore carries no reason at all and the reason is stated once, in the step that decides
it.

Second, step 2 of the close sequence gains one sentence. Append it to the sentence that
reads, verbatim:

```
— the same artifact that was about to be replayed onto the replacement pod.
```

Giving:

```
— the same artifact that was about to be replayed onto the replacement pod. The aborted re-attach's pod-side state, including a session the adapter may already have started on the replacement pod, is reclaimed in step 3 rather than sealed.
```

Third, step 3 of the close sequence. It reads, verbatim:

```
3. **Release the replacement pod.** The half-claimed replacement pod is released back to the pool via the standard pod release path ([§6.2](06_warm-pod-model.md#62-pod-state-machine)) — no runtime was started on it, so no scrub beyond the pool's default post-session scrub is required.
```

Replace it with:

```
3. **Reclaim the pod-side state and release the replacement pod.** The aborted re-attach carries the [§7.1](#71-normal-flow) pod-side reclaim obligation for the session, which the gateway sends on the connection that attempt still holds, before the half-claimed replacement pod is released back to the pool via the standard pod release path ([§6.2](06_warm-pod-model.md#62-pod-state-machine)). The pod needs no scrub beyond the pool's default post-session scrub.
```

The step takes three things by reference rather than restating them. Its scoping is §7.1's, so
a terminal trigger arriving before the attempt has issued its first pod-side RPC for the
session owes no reclaim and the step reads as a plain release. It states the ordering of the
reclaim against the release and states no outcome for the pod, because what a pod holds after
a reclaim the adapter does not acknowledge, and after a start that races it, is §7.1's
`leaked` disposition and this proposal's record of the failure modes it accepts, which cover
the resume path together with every other bind path. The replaced sentence's "no runtime was
started on it" goes as the same overturned premise as the preamble sentence this block
deletes, and the surviving no-scrub clause is scoped to the pod, because the per-slot cleanup
the reclaim performs is the one §5.2 states and it does run on this path.

Steps 1, 4 and 5 keep their wording. Step 1 needs none, because step 3 names the connection
the reclaim runs on.

### SPEC-2 · spec/07_session-lifecycle.md § 7.3 (Resume flow after pod failure)

Append one sentence after the numbered list under `**Resume flow after pod failure:**`, whose
last item reads, verbatim:

```
4. If retries exhausted → state becomes `awaiting_client_action`
```

The appended sentence, as its own paragraph below the list:

```
A step in this flow that fails after the gateway has issued its first pod-side RPC onto the replacement pod carries the [§7.1](#71-normal-flow) pod-side reclaim obligation for the session before the replacement pod is released.
```

§7.1 stays the normative statement. This sentence points at it and states no rule of its own,
so the resume flow's steps, its retry branching, and its `awaiting_client_action` terminal are
unchanged.

### SPEC-2 · spec/06_warm-pod-model.md § 6.2 (`resuming` failure transitions)

Insert one clause into the action sequence of the `**Client / parent / admin cancel
mid-resume (`resuming → cancelled`)**` bullet. The clause to replace reads, verbatim:

```
the half-claimed replacement pod is released to the pool
```

Replace it with:

```
the [§7.1](07_session-lifecycle.md#71-normal-flow) pod-side reclaim for the session runs on the half-claimed replacement pod, which is then released to the pool
```

No edge is added to or removed from the `resuming` failure enumeration, so the subsection
keeps its authoritative-enumeration status: the reclaim is one action inside an edge the
enumeration already carries. The sibling `resuming → completed` bullet is not edited, because
both bullets take their step-by-step sequence from §7.2, which the block above corrects so
that it carries the reclaim for both edges. Neither bullet enumerates that sequence. Both
summarise it, and neither names the `coordination_generation` bump the sequence's fourth step
performs, so the sibling's "abort / skip-seal / release-replacement-pod /
run-terminal-handling" gloss is a pointer at §7.2 rather than a list that has to grow when the
sequence does.

### SPEC-2 · spec/05_runtime-registry-and-pool-model.md § 5.2 (slot retry policy, `**Max retries:**`)

Widen the pod-selection sentence in the `**Max retries:**` bullet under `**Slot retry policy
(`maxConcurrentSessions > 1`)**`. §5.2 owns where a retry lands, so the constraint the §7.1
reclaim creates is stated here rather than in §7.1, and it reaches exactly the retries this
policy places. The sentence to replace reads, verbatim:

```
The retry is always assigned to a **new slot** on the same pod (if a slot is available) or on a different pod from the pool (if the original pod is fully saturated or unhealthy).
```

Replace it with:

```
The retry is always assigned to a **new slot** on the same pod (if a slot is available and the pod is not one whose [Section 7.1](07_session-lifecycle.md#71-normal-flow) reclaim for this session did not complete) or on a different pod from the pool (if the original pod is fully saturated, unhealthy, or holds such an incomplete reclaim).
```

This lands in the same step as the §7.1 paragraph, because §7.1 states the reclaim this
constraint reads and states no placement rule of its own. Nothing else in §5.2's retry policy
changes: the retry budget and its default, the **Fresh workspace guarantee**, the
**Non-retryable failure categories** list, the whole-pod replacement trigger, and the
**Client error on exhaustion** bullet all stand as written. The constraint governs where an
attempt is retried rather than whether it is retried, so no failure class becomes
non-retryable and §5.2's `error.category` contract needs no new value. It reaches the retries
this policy places and no others, so a §15.1 start onto a slot reserved at creation, which
the policy does not place, is not governed by it. When the pod holding the incomplete
reclaim is the pool's only candidate, the retry meets the
`WARM_POOL_EXHAUSTED` outcome with `details.reason: "concurrent_slots_exhausted"` that the
shipped claim path already returns when the pool holds pods and none can take the slot. That
reason value is reused as it stands and its §5.2 definition is not edited, so the change
mints no client-visible code or reason.

### SPEC-2 · spec/04_system-components.md § 4.7.9 (Startup Sequence for `type: agent` Runtimes)

Append one sentence after step 5. Step 5 currently reads, verbatim:

```
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials(leases)` → `StartSession`
```

Replace it with:

```
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials(leases)` → `StartSession`. A stage that fails takes the failure branch stated in [Section 7.1](07_session-lifecycle.md#71-normal-flow) rather than leaving the pod holding the session's state.
```

No other change to §4.7.9. The section stays a `type: agent` orientation list and states no
failure semantics of its own.

### SPEC-3 · spec/05_runtime-registry-and-pool-model.md § 5.2 (scrub model, slot failure and cleanup)

Two anchors in §5.2.

The first anchor is the `**Slot cleanup:**` bullet's action list, which the §4.7 `Shutdown`
row defers to for the slot release's actions. It reads, verbatim:

```
On slot completion or failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, and releases the `slotId`.
```

Replace it with:

```
On slot completion or failure, the adapter removes the slot's workspace directory, kills any processes owned by the slot's process group, removes the slot's credential directory `/run/lenny/slots/{sessionId}/` and the `credentials.json` it carries, cancels the [Section 4.9](04_system-components.md#49-credential-leasing-service) direct-delivery-mode lease-expiry timers armed for that session, and releases the `slotId`.
```

Both added actions are shipped adapter behaviour rather than new obligations: `slotlayout.RemoveTree`
already removes the slot's credential directory and `deregisterSlotLocked` already cancels every
armed expiry timer on removal. §5.2's own recycle-lifecycle paragraph already assumes the credential
lease is gone by the time `cleanupCommands` run. The whole-pod credential purge at §5.2 scrub step 0
is unchanged and stays the backstop for anything a per-slot cleanup left behind, and §4.9's arming and
`AUTH_EXPIRED` firing rules are unchanged.

The second anchor is the `**Scrub model.**` paragraph, which is §5.2's
concurrency-independent statement of the rule the report exception qualifies. The paragraph
reads, verbatim:

```
**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, reported by the adapter via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)), and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`.
```

Append to that paragraph:

```
That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency. It also runs when a bind is abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), and a cleanup on that path reports no outcome, because `ReportSessionScrub` advances the pod's served-session count and that count records the sessions the pod's shared runtime process has been given. A cleanup on that path that does not complete is still accounted: the adapter's `Shutdown` response for that reclaim does not report a clean exit. On a pod serving concurrent sessions the slot is then `leaked` under [Section 6.2](06_warm-pod-model.md#62-pod-state-machine), so it holds its occupancy, counts toward the whole-pod replacement trigger stated below, and is surfaced on the `lenny_adapter_leaked_slots` gauge. On a pod serving one session the disposition is the one [Section 7.1](07_session-lifecycle.md#71-normal-flow) states: the failed attempt releases the pod's claim and the pod retires, so the reclaim's residue does not outlive the pod. A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome. A session release produces at most one cleanup-outcome report, filed by the cleanup that reclaims the slot.

**Slot-identifier reclaim hold.** The adapter holds a slot's identifier from the critical section that deregisters the slot's registry entry until the cleanup that reclaims the slot has finished. The hold outlasts the deregistration because the cleanup's remaining acts are addressed by the slot identifier rather than by the entry: the removal of the slot's workspace directory, the removal of its credential directory, the kill of its process group, and the close of the session on the pod's shared runtime process are each resolved from the identifier, and every attempt at the same session names the same identifier. The hold is a property of the identifier rather than of an entry, because the entry is what a later attempt would re-create and because the adapter's other rules read the entry set as the pod's live occupancy. While the identifier is held the adapter admits no request that would create or resolve a registry entry under it, and refuses one as a transient condition so the caller retries. A refusal is the only record the adapter makes of the hold: no report and no counter names it, and a bind refused this way is accounted by the gateway as an ordinary transient slot failure. The hold lasts as long as the cleanup it covers. The cleanup's close of the session on the pod's shared runtime process is bounded by the graceful window the reclaiming request carries when the request carries one, by that request's own deadline when it carries none, and, for a hold taken outside any request, by the termination window of the pass that runs the cleanup. The cleanup's removal of the slot's directories runs after that close under no deadline, so the hold outlasts the window by the time that removal takes. The `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` figure the **Slot cleanup:** bullet states is the deployer's budget for that cleanup and is not a second bound on the hold. A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it. [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind epoch the adapter reports for a slot's registry entry and the reclaim that names it.
```

The reclaim-hold paragraph is what makes the reclaim exclusive while it runs. The entry
deregistration and the destructive steps are not one act: the tree removal, the credential
directory removal, and the process-group kill each resolve from the slot identifier, which
every attempt at the same session shares, so a successor admitted between the two would be
torn down by a reclaim that had already refused nothing. The paragraph states the hold
as a property of the identifier rather than of the entry for that reason.

The withheld-report rule and the one-report rule land in the scrub-model paragraph rather than
in the `**Slot cleanup:**` bullet, because that bullet sits under a heading scoped to
`maxConcurrentSessions > 1` while both rules hold on a pod of either concurrency, and because
the scrub-model paragraph is where §5.2 already states the per-slot cleanup uniformly across
session-mode configurations. That same uniformity sentence is what carries the bullet's action
list across the concurrency boundary, so the appended pointer clause discharges the scoping
question for both anchors without stating a second action list. Nothing else in the bullet
changes: its trigger, the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula, the
CRD validation rule, and the leaked outcome all stand as written.

The clause on a cleanup that does not complete states §6.2's existing disposition on the pod
class that offers it rather than adding a second one. §7.1's reclaim reads the adapter's answer
to its `Shutdown`, and an answer that does not report a clean exit leaves the slot `leaked` on a
pod serving concurrent sessions, which §6.2's leaked-slot semantics and §5.2's own whole-pod
replacement trigger already account for. On a pod serving one session §6.2 offers no `leaked`
sub-state and §5.2 states no replacement trigger, so the clause points at §7.1's disposition for
that class instead of restating it, matching what §7.1 and the edge-case list already say. The
clause sits here because §5.2 is where the report is withheld, and a reader of the withholding
rule alone would otherwise read the leak accounting as withheld with it. The accounting the
clause names reaches every bind path §7.1 binds: the gateway's slot-failure accounting covers
the retry-placed bind path today, and the code lane extends the same helper to the create-time
reserved path and to the §7.3 re-attach, so the clause holds on all three.

### SPEC-4 · spec/06_warm-pod-model.md § 6.2 (per-slot sub-state fence)

In the fenced state-machine block, under the heading line `Per-slot sub-states (tracked per
session, not as pod-level phase; a pod of either concurrency):`, insert one edge immediately
after the `receiving_uploads ──→ running` entry:

```
  receiving_uploads ──→ slot_cleanup    (the bind is abandoned or fails before the runtime has
                                         been given the session, a start still in flight
                                         included)
```

No edge is added out of `slot_assigned`. The connect stage reserves the slot before any
workspace RPC and the adapter holds nothing there, so it is not one of this proposal's
residue classes. The gateway can still mark such a slot `leaked` when the reservation
release fails, on the shipped retry path and, once CODE-4 folds `BindReservedSlot`'s own
release error into the disposition, on the create-time-reserved path as well. That terminal
is a pre-existing hole this proposal widens rather than closes, recorded in the summary.

### SPEC-4 · spec/06_warm-pod-model.md § 6.2 (prose after the fence)

Insert the paragraph below immediately after the fenced block closes and before the
paragraph beginning `**`reserved` hold semantics.**`:

```
**Pre-`running` slot cleanup.** A slot reaches `running` when the pod's shared runtime process has been given the session with its session identifier. Every earlier stage of the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence, credential assignment included, and a start still in flight, whose session has not yet reached the runtime, leave the slot in `receiving_uploads`. A bind abandoned or failed at one of those stages takes the `receiving_uploads → slot_cleanup` edge; a session the runtime has already been given is a slot in `running` and takes the `running → slot_cleanup` edge the fence above carries. The cleanup either edge runs, and the report the pre-`running` reclaim withholds, are the ones [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states. [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold, from the adapter's deregistration of the slot's registry entry until the cleanup finishes, which is what refuses a bind onto the slot's identifier in that window. The `slot_cleanup` sub-state is tracked per session and carries no admission rule of its own.
```

The closing sentences add no edge and state no rule. They point a reader of the fence at
the section that fixes what holds a slot's identifier while its cleanup runs, so `slot_cleanup` is not read as a label on a
slot anything else may bind, and §6.2 does not become a third statement of a window
§5.2 and §15.4 already fix.

### SPEC-5 · spec/04_system-components.md § 4.7.1 (after the Gateway → Adapter and Adapter → Gateway RPC tables)

Insert the block below immediately after the `*Adapter → Gateway RPCs:*` table closes and
before the `#### 4.7.2 Checkpoint and Interrupt Mutual Exclusion` heading. The block states
the bind epoch once, on the section that owns the gateway-adapter RPC contract, so the §4.7
`Shutdown` row and §7.1 each point at one statement rather than carrying two. §4.1 states
nothing about the epoch, because the field has no wire presence and the rule that paragraph
closes with is about a field's presence standing in for a scope:

```
**Bind epoch.** The adapter mints a **bind epoch** when it creates a registry entry for a slot identifier it holds none for. The epoch belongs to that entry and does not change while the entry lives; it is released with the entry. The epoch is a positive integer, so zero is not an epoch: a request whose bind epoch is zero carries none, and a response reporting zero reports none. The epoch is unique and strictly increasing within one adapter process, pod-local, and never persisted: it survives neither a pod restart nor a re-attach onto a replacement pod. It is never derived from the session identifier, from the slot identifier, or from a `coordination_generation`. It is not a coordination generation and carries none of that field's semantics. The two answer different questions: the generation names the gateway replica that speaks for the session ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) and is validated on the RPCs that carry it, while the epoch names the registry entry a reclaim is addressed to and is validated on `Shutdown` alone. Where both appear on one message, as on `Shutdown`, each is checked on its own terms.

The bind-sequence RPCs are `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `ConfigureWorkspace`, and `Resume`. Each of them reports the entry's current epoch on its response, and none of them carries one on its request. A bind-sequence RPC that creates the entry reports the epoch it minted; one that resolves an entry the adapter already holds reports that entry's epoch and mints nothing. `PrepareWorkspace` is client-streaming: the adapter resolves the entry once, at the frame from which it first resolves the slot identifier, and the call's single response reports that entry's epoch. A [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload reuses `PrepareWorkspace` and `FinalizeWorkspace` on the entry the session already holds, so it reports that entry's epoch under the same rule and needs no rule of its own. `Shutdown` is the one request that carries a bind epoch, as a precondition on the whole request, and reports none on its response. Its response reports which of three outcomes the reclaim took: `reclaimed` when the adapter held the entry the request named and released the slot, `superseded` when it holds an entry for the session at a different epoch, and `absent` when it holds no entry for the session. All three are successful outcomes and are answered on a successful RPC. Every other RPC on this contract neither carries nor reports a bind epoch. No epoch precondition applies to any request other than `Shutdown`; this block states no refusal onto a slot identifier, and the refusal a cleanup in progress imposes is stated by the reclaim hold in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).

The caller rules follow from that. A caller holds the most recent epoch a response reported to it on one adapter connection, and holds none on a connection that has received no such response. It holds none once it has itself issued an RPC on that connection that removes the entry the epoch names: `DemoteSDK` returns the pod to its pod-warm state ([Section 4.7](#47-runtime-adapter)) and removes the entry, and a `Shutdown` answering `reclaimed` removes it. A `Shutdown` carries an epoch only when it compensates a bind attempt the caller abandoned; such a compensation names the epoch the caller holds for that session and names no other. Every other `Shutdown`, and a compensation by a caller that holds none, carries no epoch and is the unconditional teardown, which is what every caller other than a fenced compensation sends. A caller sends a compensation on the connection its own attempt already holds and never dials a new one for it, because the epoch it holds is latched off the responses that connection reported to it, so a caller that dialled a fresh connection would hold none on it and would send the unconditional teardown at a pod that may already hold a successor's slot. A bind attempt whose stages span two connections, as the [Section 4.7.9](#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence does when the gateway splits it across a prepare and a launch, holds one epoch per connection and each connection's compensation names what that connection observed; both connections resolve one entry, so both observe one epoch.
```

The block sits in §4.7.1 rather than in §4.7.9 because §4.7.9 is a `type: agent`
orientation list and the epoch binds every session mode the RPC tables serve. The
comparison the adapter performs and the outcomes it answers are stated in the §4.7
`Shutdown` row, which is where a reader of that RPC looks; this block states what the value
is, which responses report it, which request carries it, and who may name it.

### SPEC-5 · spec/15_external-api-surface.md § 15.4 (after the SDK-warm demotion contract)

Insert the two blocks below immediately after the paragraph beginning `**SDK-warm demotion
contract:**` and before the `#### 15.4.1 Message Format and Binary I/O Requirements`
heading. §15.4 is where the adapter contract is published to third-party adapter authors, so
the statement here is written to be implementable without reading any Go and states the
non-conformance case for each part:

```
**Bind epoch contract:** [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind epoch: what an adapter mints it for, which responses report it, which request carries it, and which epoch a caller may name. That statement is normative for a third-party adapter and is not restated here. The clauses below state what non-conformance looks like against it.

An adapter does not conform when it reuses an epoch across two entries, derives one from the session identifier, mints a fresh epoch for a bind-sequence request that resolves an entry it already holds, changes a live entry's epoch, reports zero on a bind-sequence response, mints more than one epoch for one `PrepareWorkspace` call, or resolves the slot identifier more than once within one `PrepareWorkspace` call.

An adapter does not conform when it refuses a bind-sequence request on a bind epoch, because no bind-sequence request carries one. The refusal an adapter must exhibit while a slot's cleanup is running is stated by the reclaim-hold contract below.

An adapter does not conform when, on a `Shutdown` whose bind epoch is non-zero and differs from the epoch of the entry it holds for the named session, it releases the slot, closes the runtime, or signals the shared runtime process. It does not conform when it compares a zero bind epoch against the entry it holds, or answers `superseded` for a request whose bind epoch is zero.

The `Shutdown` response reports the comparison's outcome and each outcome has one meaning. `reclaimed` means the adapter held the named entry and released the slot. `superseded` means the adapter holds an entry for the session at a different epoch, so a later entry owns the slot identifier and the reclaim removed nothing. `absent` means the adapter holds no entry for the session. All three are successful outcomes and are answered on a successful RPC. An adapter that answers a gRPC error for a superseded or an absent reclaim does not conform.
```

```
**Slot-identifier reclaim hold:** [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold: the adapter holds a slot's identifier from the deregistration of that slot's registry entry until the cleanup that reclaims the slot has finished, because the cleanup's remaining acts are addressed by the identifier rather than by the entry. This is what an adapter must exhibit on the wire. While the identifier is held, a request that would create or resolve a registry entry under it is refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on. An adapter that admits such a request while the cleanup is running does not conform. An adapter that refuses one with a permanent status, or that answers a status the caller cannot retry, does not conform. `Shutdown` is not held: a `Shutdown` naming a session whose cleanup is running removes nothing and answers as [Section 4.7](04_system-components.md#47-runtime-adapter) states for a session the adapter holds no entry for.
```

Both blocks state the contract and neither states a Go type, a field number, or a package
name. The wire form is `schemas/lenny-adapter.proto`, which §15.4 already names as the
published artifact. The epoch appears on the response side of the bind sequence and on the `Shutdown` request, so
the wire edit that lands them adds a reporting field to each of the seven bind-sequence
responses and the fence and its outcome to `Shutdown`. No bind-sequence request carries an
epoch. The [§7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload therefore needs
no wire field of its own: it resolves the entry the session already holds and is answered at
that entry's epoch.

## Spec sections deliberately untouched

Listed so a reviewer can tell scope from oversight.

- **§15.1's error catalog.** The reclaim-in-progress refusal is answered on the
  gateway-adapter surface and mints no client-visible error code.
- **§15.4.6's conformance categories.** They exercise the runtime binary over JSONL against a
  fake adapter, so they have no adapter under test and cannot observe an adapter obligation.
  CONF-1 lands in the tier-10 conformance battery instead.
- **§5.2's non-retryable failure categories.** The list is scoped to
  `maxConcurrentSessions > 1` and the reclaim hold is not, and the refusal the hold returns
  is transient, so no category is added or removed.
- **§10.1's coordinator handoff.** The bind epoch is not a coordination generation and
  changes nothing about the handoff.
- **§28's registers.** `Shutdown` appears in no §28 register row, and §28.5.1 is organised
  per channel rather than per field, so a new field on an existing message adds no row.

## Spec files touched

- `spec/04_system-components.md` — §4.1 Request Message Scope (one sentence replaced), §4.7 Gateway → Adapter RPC table `Shutdown` row (first sentence replaced, the
  no-op sentence and the epoch-and-outcomes sentences added), §4.7.1 (the bind-epoch block,
  new, after the RPC tables), §4.7.9 step 5 (one sentence).
- `spec/05_runtime-registry-and-pool-model.md` — §5.2 `**Scrub model.**` paragraph (the
  per-slot cleanup pointer, the cleanup-outcome report rules, and the slot-identifier
  reclaim-hold paragraph appended), the §5.2
  `**Slot cleanup:**` bullet's action-list sentence (replaced), and the §5.2
  slot-retry-policy `**Max retries:**` bullet's pod-selection sentence (replaced).
- `spec/06_warm-pod-model.md` — §6.2 per-slot sub-state fence (one edge), the prose after
  it (one paragraph, pointing at the §5.2 reclaim hold), and the §6.2
  `resuming` mid-resume cancel bullet (one clause).
- `spec/07_session-lifecycle.md` — §7.1 atomicity paragraph (one parenthetical) and a new
  paragraph after it carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/15_external-api-surface.md` — §15.4 (the published bind-epoch contract and the
  slot-identifier reclaim-hold contract, new, after the SDK-warm demotion contract).
- `spec/29_communication-scenarios.md` — §29.4 session-end step 13 (one sentence appended).
