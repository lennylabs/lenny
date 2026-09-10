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
the runtime's acknowledgement of it, so a start still in flight is torn down rather than
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
started the session. On a pod serving concurrent sessions, a reclaim the
adapter does not acknowledge leaves the slot `leaked` under §6.2's existing disposition,
which holds the slot's occupancy and counts it toward the §5.2 whole-pod replacement trigger.
On a pod serving one session the failed attempt releases the pod's claim and the pod retires
under §6.2's pre-attached failure disposition, so the residue dies with the pod and the
`leaked` disposition does not arise there, which matters because §6.2 offers the `leaked`
sub-state and §5.2 the replacement trigger only under concurrent occupancy. The slot
identifier is the session identifier and the slot's workspace tree is derived from it, so a
further attempt at the same session on that pod would reuse a tree the lagging reclaim may
still be deleting. Where a retry lands is §5.2's subject rather than §7.1's, so §5.2's
`**Max retries:**` bullet states that constraint itself: a pod holding a reclaim for this
session that the adapter did not acknowledge is not a placement for a retry that policy
places, beside the saturation and health conditions the bullet already names. §7.1 states the
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
runtime has been given the session, which is the moment §6.2's `receiving_uploads → running`
trigger names, so every stage of the §4.7.9 step-5 bind sequence before that point, credential
assignment included, and a start still in flight, whose session the runtime has not yet
acknowledged, leave the slot in `receiving_uploads`. The new edge, `receiving_uploads →
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
that is serving its client. §4.7.1 states the **bind epoch** for that reason: the adapter
mints one per registry entry, reports it on the response of every RPC that can create or
resolve an entry, and a caller may name only an epoch it observed on its own attempt's
responses. A `Shutdown` that names an epoch releases the slot only when the entry the adapter
holds carries that epoch. It is answered `superseded` when a different attempt holds the
slot and `absent` when the adapter holds no entry for the session, and both are completed
reclaims that leave the slot unleaked, because the state the naming attempt created is
already gone in each. A `Shutdown` that names no epoch is the unconditional teardown, which
is what every caller other than a failed bind's compensation sends. The epoch is a
precondition on the slot release rather than a scope selector, so §4.1's rule that no
operation is selected by a field's presence standing in for a scope holds: both forms address
one session and both release that session's slot, and the epoch states which attempt is
asking.

**A slot identifier stays occupied until its cleanup finishes.** The epoch refuses a reclaim
that arrives after a successor took the slot. It says nothing about a reclaim that is
admitted and then races a successor while it tears state down, because the destructive steps
read no registry state: the workspace directory, the credential directory, and the process
group are each named from the slot identifier, which every attempt at the session shares.
§5.2 therefore states the occupancy: a slot identifier is occupied from the creation of its
registry entry until the cleanup that reclaims it has finished, deregistering the entry does
not end the occupancy, and while the identifier is occupied the adapter admits no bind onto
it and refuses one as a transient condition. §6.2's `slot_cleanup` sub-state is that
occupancy on either incoming edge. The two mechanisms close two different orderings and
neither closes both.

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
  placement constraint does not reach it. The epoch and the occupancy are what govern this
  path instead. A retry that arrives while the first attempt's cleanup is still running is
  refused on the occupancy, as a transient condition, and binds on a further attempt. A
  retry that arrives after the cleanup finished materializes the slot afresh and holds a new
  epoch, so a reclaim for the first attempt that reaches the adapter afterwards is answered
  `superseded` and removes nothing. Neither the tree the retry staged nor the session it
  started can be taken down by the previous attempt's reclaim.
- **A retry that meets the occupancy spends an attempt on it.** The refusal is transient, so
  the attempt keeps its §5.2 retryability, but a retry placed onto a pod whose cleanup for
  that identifier is still running consumes one of the attempts the §5.2 retry budget allows,
  and on a create-time-reserved slot it consumes one of the client's own retries. The
  occupancy is bounded by §5.2's per-slot cleanup timeout, which on an exclusive pool
  degenerates to the pool's whole `cleanupTimeoutSeconds`, so the wait is bounded but can be
  the full cleanup budget. This is accepted rather than closed: the alternative is a
  gateway-side wait-and-retry inside the bind path, which holds the client's request open for
  the same budget and adds a second place where the timeout is stated.
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
- **A start that races the reclaim.** When the adapter admitted the start before the reclaim
  removed the slot, the start finds its slot gone at the point it would record the runtime as
  holding the session, takes the session back off the shared runtime process, and refuses.
  The reverse ordering, in which the reclaim's answer precedes the start's admission and the
  start's own claim re-creates the registry entry, is refused on the epoch: the re-created
  entry carries a fresh epoch, so the start confirmation, which names the epoch its own claim
  observed, finds an entry at a different epoch and refuses. Neither ordering reports a
  cleanup outcome, because the slot never reached `running`.
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
The handler runs the slot release when the adapter holds an entry for the named session, runs the runtime teardown under the narrower precondition [Section 4.7](#47-runtime-adapter) states, and runs the whole-pod scrub when the recycle disposition is set, so no operation is selected by a field's presence standing in for a scope. The bind epoch a request may carry ([Section 4.7.1](#471-role-and-gateway-rpc-contract)) is a precondition on the slot release rather than a scope selector, in the manner of a Kubernetes `DeleteOptions` precondition: a request carrying one and a request carrying none both address the same session and both release that session's slot, and the epoch states which bind attempt is asking.
```

The second sentence is added because `expected_bind_epoch` is present on some
`ShutdownRequest` messages and absent on others, and this subsection's closing rule forbids
an operation selected by a field's presence standing in for a scope. The added sentence
states which side of that rule the field falls on rather than leaving a reader to infer it.

The paragraph's first two sentences are untouched and this edit retires no vocabulary. The
second sentence ("The per-slot teardown and the whole-pod teardown are the same operation on
the same address, and what remains is the recycle disposition the request carries beside it.")
states where the work is addressed. That is what the subsection needs from it, because the
subsection derives a message's scope from its field set
(`spec/04_system-components.md:151`) and the paragraph's closing clause concludes that no
operation is selected by a field's presence standing in for a scope. The split gives each
operation its own precondition and leaves the address-sharing claim true. The second
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
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the runtime's acknowledgement of it, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response. The request may carry the bind epoch [Section 4.7.1](#471-role-and-gateway-rpc-contract) states, and when it does the slot release runs only if the entry the adapter holds for the named session carries that epoch. The response reports which of three outcomes the reclaim took: `reclaimed` when the adapter held the named entry and released the slot, `superseded` when it holds an entry for the session at a different epoch, and `absent` when it holds no entry for the session. All three are successful outcomes and are answered on a successful RPC. A superseded reclaim is neither a failed reclaim nor a leaked slot: a later bind attempt holds the slot, so the state the naming attempt created is already gone and the reclaim correctly performed nothing. A request carrying no epoch is the unconditional teardown, which is what every caller other than a failed bind's compensation sends, and its response reports `reclaimed` for an entry it removed and `absent` for a session it holds none for. The request carries the recycle disposition beside that teardown rather than selecting a scope.
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
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the creation finalize block, the [§15.1](15_external-api-surface.md#151-rest-api) start transition, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session on the connection the failed stage still holds, before that connection closes, and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. The reclaim runs on the connection the failed attempt already holds and never dials a new one; that rule is load-bearing rather than incidental, because the bind epoch it carries is pod-local and is observed on that connection alone. The reclaim names the attempt it compensates by carrying the bind epoch ([Section 4.7](04_system-components.md#47-runtime-adapter)) that attempt observed, and it may name no other. An attempt that fails before any response carried it an epoch holds none and sends the unconditional form. A reclaim naming an attempt that no longer holds the slot is answered `superseded`, and one for a session the adapter holds no entry for is answered `absent`; both are completed reclaims and leave the slot unleaked, because in each the state the failed attempt created is already gone. Only a reclaim the adapter does not answer at all leaves the slot unreclaimed. On a pod serving concurrent sessions, a reclaim the adapter does not answer leaves the slot `leaked` under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) disposition, which holds the slot's occupancy and counts it toward the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) whole-pod replacement trigger. The slot identifier is the session identifier and the slot's workspace tree is derived from it, so a further attempt at the same session on that pod would reuse a tree the lagging reclaim may still be deleting; [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) keeps the retries its slot retry policy places off that pod. Nothing about the attempt's retryability changes. On a pod serving one session the failed attempt releases the pod's claim and the pod retires under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) pre-attached failure disposition, so the reclaim's residue does not outlive the pod; the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there. The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated.
```

The paragraph states no deadline for the reclaim. §5.2's per-slot cleanup timeout is the
figure the gateway reuses, cited from the code rather than restated here.

The no-re-dial rule is stated in the paragraph rather than left to the code because the
epoch fence depends on it. An epoch is minted per registry entry inside one adapter process
and is observed on the responses an attempt received; a compensation that dialled a fresh
connection would still hold the epoch it observed, but a compensation written to dial when
the connection is gone would be free to run for an attempt that observed nothing, which is
the unfenced form on a path where a successor may already hold the slot.

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
The retry is always assigned to a **new slot** on the same pod (if a slot is available and the pod is not one whose [Section 7.1](07_session-lifecycle.md#71-normal-flow) reclaim for this session the adapter did not acknowledge) or on a different pod from the pool (if the original pod is fully saturated, unhealthy, or holds such an unacknowledged reclaim).
```

This lands in the same step as the §7.1 paragraph, because §7.1 states the reclaim this
constraint reads and states no placement rule of its own. Nothing else in §5.2's retry policy
changes: the retry budget and its default, the **Fresh workspace guarantee**, the
**Non-retryable failure categories** list, the whole-pod replacement trigger, and the
**Client error on exhaustion** bullet all stand as written. The constraint governs where an
attempt is retried rather than whether it is retried, so no failure class becomes
non-retryable and §5.2's `error.category` contract needs no new value. It reaches the retries
this policy places and no others, so a §15.1 start onto a slot reserved at creation, which
the policy does not place, is not governed by it. When the pod holding the unacknowledged
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

A slot identifier is occupied from the moment the adapter creates the slot's registry entry until the cleanup that reclaims it has finished. Deregistering the entry does not end the occupancy, because the cleanup still holds the slot's workspace directory, its credential directory, and its process group, each of which is named from the slot identifier rather than from the entry. While the identifier is occupied the adapter admits no bind onto it and refuses one as a transient condition, which is why that refusal is absent from the **Non-retryable failure categories** list below. The per-slot cleanup timeout the **Slot cleanup:** bullet states bounds the occupancy; a cleanup that exceeds it leaves the slot `leaked`, which holds the identifier until the pod terminates.
```

The occupancy paragraph is what makes the reclaim exclusive while it runs. The entry
deregistration and the destructive steps are not one act: the tree removal, the credential
directory removal, and the process-group kill each resolve from the slot identifier, which
every attempt at the same session shares, so a successor admitted between the two would be
torn down by a reclaim that had already refused nothing. The paragraph states the occupancy
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
**Pre-`running` slot cleanup.** A slot reaches `running` when the pod's shared runtime process has been given the session, which is the moment the `receiving_uploads → running` trigger above names. Every earlier stage of the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence, credential assignment included, and a start still in flight, whose session the runtime has not yet acknowledged, leave the slot in `receiving_uploads`. A bind abandoned or failed at one of those stages takes the `receiving_uploads → slot_cleanup` edge; a session the runtime has already been given is a slot in `running` and takes the `running → slot_cleanup` edge the fence above carries. The cleanup either edge runs, and the report the pre-`running` reclaim withholds, are the ones [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states. On either incoming edge the `slot_cleanup` sub-state is exclusive occupancy of the slot identifier, lasting until the cleanup reaches `released` or `leaked`; [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states what that occupancy covers and what bounds it.
```

The exclusivity sentence adds no edge. It states what the state the fence already carries
means for the identifier, so a reader of the fence alone does not read `slot_cleanup` as a
label on a slot that anything else may bind.

### SPEC-5 · spec/04_system-components.md § 4.7.1 (after the Gateway → Adapter and Adapter → Gateway RPC tables)

Insert the block below immediately after the `*Adapter → Gateway RPCs:*` table closes and
before the `#### 4.7.2 Checkpoint and Interrupt Mutual Exclusion` heading. The block states
the bind epoch once, on the section that owns the gateway-adapter RPC contract, so the §4.1
paragraph, the §4.7 `Shutdown` row, and §7.1 each point at one statement rather than
carrying three:

```
**Bind epoch.** The adapter mints a **bind epoch** for each slot registry entry it creates. The epoch is unique and strictly increasing within one adapter process, pod-local, and never persisted: it survives neither a pod restart nor a re-attach onto a replacement pod. It is never derived from the session identifier, from the slot identifier, or from the `coordination_generation` the same requests carry. It is not a coordination generation and carries none of that field's semantics. The two travel on the same messages and answer different questions: the generation names the gateway replica that speaks for the session ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)), and the epoch names the bind attempt that holds the slot.

The adapter reports the entry's epoch on the response of every RPC in the tables above that can create or resolve a slot registry entry. A bind attempt therefore observes the epoch of the entry it is working against from its first such response onwards, and every later response of that attempt reports the same value.

The caller rules follow from that. A caller may name an epoch only on a `Shutdown` for the session, may name only an epoch it observed on a response to its own attempt, and may name no other. A caller that holds no epoch, because its attempt failed before any response carried one, sends the unconditional form. A `Shutdown` carrying no epoch is the session teardown and is never a compensation for a failed bind. A caller sends a compensation on the connection its own attempt already holds and never dials a new one for it, because the epoch it names was observed on that connection and is meaningless without it.
```

The block sits in §4.7.1 rather than in §4.7.9 because §4.7.9 is a `type: agent`
orientation list and the epoch binds every session mode the RPC tables serve. The
comparison the adapter performs and the outcomes it answers are stated in the §4.7
`Shutdown` row, which is where a reader of that RPC looks; this block states what the value
is and who may name it.

### SPEC-5 · spec/15_external-api-surface.md § 15.4 (after the SDK-warm demotion contract)

Insert the two blocks below immediately after the paragraph beginning `**SDK-warm demotion
contract:**` and before the `#### 15.4.1 Message Format and Binary I/O Requirements`
heading. §15.4 is where the adapter contract is published to third-party adapter authors, so
the statement here is written to be implementable without reading any Go and states the
non-conformance case for each part:

```
**Bind epoch contract:** An adapter mints a **bind epoch** for every slot registry entry it creates ([Section 4.7](04_system-components.md#47-runtime-adapter)). The epoch is a positive integer, unique and strictly increasing within the adapter process, held in memory and never persisted. An adapter that reuses an epoch across two entries, or that derives one from the session identifier, does not conform.

The adapter reports the entry's epoch on the response of every RPC that creates or resolves a slot registry entry: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `ConfigureWorkspace`, and `Resume`. Every response the adapter sends for one entry reports the same value. An adapter that reports zero on such a response, or that reports two different values for one entry, does not conform.

The adapter compares the epoch a `Shutdown` request carries against the epoch of the entry it holds for the named session, and it performs the slot release only when the two are equal. An adapter that releases the slot on a mismatch does not conform. A `Shutdown` carrying no epoch is the unconditional teardown and is compared against nothing.

The adapter reports the comparison's outcome on the `Shutdown` response, and each outcome has one meaning. `reclaimed` means the adapter held the named entry and released the slot. `superseded` means the adapter holds an entry for the session at a different epoch, so a later bind attempt holds the slot and the reclaim removed nothing. `absent` means the adapter holds no entry for the session. All three are successful outcomes and are answered on a successful RPC. An adapter that answers a gRPC error for a superseded or an absent reclaim does not conform.
```

```
**Slot-identifier occupancy:** A slot identifier is occupied from the moment the adapter creates its registry entry until the cleanup that reclaims it has finished. The occupancy outlasts the deregistration of the entry, because the cleanup still holds the slot's workspace directory, its credential directory, and its process group, each of which is named from the slot identifier. While the identifier is occupied the adapter refuses a bind onto it, and it refuses it as a transient condition so the caller retries rather than failing the session. An adapter that admits a bind onto an identifier whose cleanup is still running does not conform. The per-slot cleanup timeout ([Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)) bounds the occupancy.
```

Both blocks state the contract and neither states a Go type, a field number, or a package
name. The wire form is `schemas/lenny-adapter.proto`, which §15.4 already names as the
published artifact.

## Spec sections deliberately untouched

Listed so a reviewer can tell scope from oversight.

- **§15.1's error catalog.** The reclaim-in-progress refusal is answered on the
  gateway-adapter surface and mints no client-visible error code.
- **§15.4.6's conformance categories.** They exercise the runtime binary over JSONL against a
  fake adapter, so they have no adapter under test and cannot observe an adapter obligation.
  CONF-1 lands in the tier-10 conformance battery instead.
- **§5.2's non-retryable failure categories.** Named only to state that the
  reclaim-in-progress refusal is absent from the list. No category is added or removed.
- **§10.1's coordinator handoff.** The bind epoch is not a coordination generation and
  changes nothing about the handoff.
- **§28's registers.** `Shutdown` appears in no §28 register row, and §28.5.1 is organised
  per channel rather than per field, so a new field on an existing message adds no row.
- **§29.4.** The session-end trace sends the unconditional form, which the §4.7 row now
  states explicitly, so the trace stands as written.

## Spec files touched

- `spec/04_system-components.md` — §4.1 Request Message Scope (one sentence replaced, one
  added), §4.7 Gateway → Adapter RPC table `Shutdown` row (first sentence replaced, the
  no-op sentence and the epoch-and-outcomes sentences added), §4.7.1 (the bind-epoch block,
  new, after the RPC tables), §4.7.9 step 5 (one sentence).
- `spec/05_runtime-registry-and-pool-model.md` — §5.2 `**Scrub model.**` paragraph (the
  per-slot cleanup pointer, the cleanup-outcome report rules, and the slot-identifier
  occupancy paragraph appended), the §5.2
  `**Slot cleanup:**` bullet's action-list sentence (replaced), and the §5.2
  slot-retry-policy `**Max retries:**` bullet's pod-selection sentence (replaced).
- `spec/06_warm-pod-model.md` — §6.2 per-slot sub-state fence (one edge), the prose after
  it (one paragraph, carrying the `slot_cleanup` exclusivity sentence), and the §6.2
  `resuming` mid-resume cancel bullet (one clause).
- `spec/07_session-lifecycle.md` — §7.1 atomicity paragraph (one parenthetical) and a new
  paragraph after it carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/15_external-api-surface.md` — §15.4 (the published bind-epoch contract and the
  slot-identifier occupancy contract, new, after the SDK-warm demotion contract).
- `spec/29_communication-scenarios.md` — §29.4 session-end step 13 (one sentence appended).
