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
reclaim runs and is refused as a transient condition. Where a retry lands is §5.2's subject
rather than §7.1's, so §5.2's
`**Max retries:**` bullet states the placement constraint itself: a pod holding a reclaim for this
session that did not complete is not a placement for a retry that policy places, beside
the saturation and health conditions the bullet already names. That constraint reaches a pod
whose reclaim did not complete and no further, so a hold that no reclaim opened, such as the
one a start-path rollback takes while it removes the slot's directories, stays reachable by a
retry the policy places; the accepted failure modes below state what that costs. §7.1 states the
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
that is serving its client. §4.7.1 states the **bind attempt token** for that reason. The
gateway mints the token once per bind attempt, from a cryptographically secure random source,
before that attempt issues its first pod-side RPC, and carries that one value on the requests
§4.7.1 enumerates and on the compensating `Shutdown`. The adapter treats the token as opaque:
it compares it for equality and derives no order, no age, and no identity from its bytes. The
empty string is not a token.

**The adapter stamps the token on the entry it creates and never on one it resolves.** The
adapter writes the request's token onto the registry entry at the moment it creates that entry,
and it never writes the field again, in either direction. An entry therefore names the attempt
that created it for as long as the entry lives, so the first attempt to create an entry owns it
until the entry is removed and no later request takes that ownership away. The resolve, the
stamp, and the comparison are one indivisible step under the lock that guards the registry.
Without that clause first-writer-wins is not implementable from the prose: an adapter that
resolved an entry, released its lock, and then stamped would satisfy each act separately and
still admit two attempts onto one entry.

**Admission reads the token and the stage the entry has reached.** A request whose non-empty
token differs from the non-empty token the entry carries is refused as a transient condition. A
request that is not a mid-session upload and that resolves an entry whose session has already
started is refused as a permanent condition. A mid-session upload asserts no attempt identity,
never creates an entry, and is refused on the precondition when the adapter holds no entry for
the session. §15.1 carries a row for each of the two refusals, and the token field is non-empty
exactly when the mid-session marker is false.

**The `Shutdown` says which teardown it is asking for.** A `Shutdown` either names the bind
attempt it compensates or asks for the unconditional teardown, and it carries exactly one of the
two. A request that carries neither, and one that carries both, is an invalid argument and the
adapter performs nothing. A `Shutdown` naming an attempt performs the slot release and the
runtime teardown only when the entry the adapter holds for the named session carries that
attempt's token, and performs neither when the entry carries a different token, when the entry
carries none, and when the adapter holds no entry at all. The unconditional teardown is what
every caller other than a compensating reclaim sends, and it removes whatever entry the adapter
holds for the session. Stating the two forms as two fields rather than as the presence and the
absence of one makes the destructive form the one a caller has to ask for by name, so a caller
that forgets the token is answered with an error rather than a destroyed session.

**The token names the bind attempt an entry belongs to, and the adapter holds the slot
identifier while its cleanup runs.**
An entry belongs to the attempt whose token it carries, so a reclaim naming an attempt
whose entry was already released performs nothing. The token says
nothing about a reclaim that is admitted and then races a successor while it tears state
down, because the destructive steps read no registry state: the workspace directory, the
credential directory, and the process group are each named from the slot identifier,
which every attempt at the session shares. §5.2 therefore states the reclaim hold: the
adapter holds the slot identifier from the critical section that deregisters the
registry entry until the cleanup that reclaims it has finished, the hold outlasts the
deregistration, and while the identifier is held the adapter admits no bind onto it and
refuses one as a transient condition. §6.2's `slot_cleanup` sub-state begins when the
gateway observes the failure, earlier than the deregistration the hold starts at, so
§6.2 points at the hold rather than restating its window. The token closes the late reclaim, the
admission rules close the bind that a foreign attempt would otherwise make onto a live entry,
and the hold closes the bind that arrives mid-cleanup. Each closes a case the others leave
open.

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
  placement constraint does not reach it. The attempt token and the reclaim hold are what govern
  this path instead, and the orderings differ in what they cost. A retry that arrives while the
  first attempt's cleanup is still running is refused on the reclaim hold, as a transient
  condition, and binds on a further attempt. A retry that arrives before the lagging reclaim
  does meets the abandoned attempt's surviving entry, and its first entry-resolving RPC carries
  its own freshly minted token, which differs from the token the entry carries, so the adapter
  refuses it as a transient condition before it touches workspace, setup, credential, or
  registry state. That refusal consumes one of the client's own retries. The lagging reclaim
  then names the token the entry does carry, compares equal, and removes the entry, so the
  attempt after it creates a fresh entry and owns it under its own token. A retry that arrives
  after the cleanup finished materializes the slot afresh and owns it the same way. In every
  ordering the abandoned attempt's reclaim can only remove the entry its own attempt created:
  where the retry created a fresh entry the reclaim is answered `superseded` or `absent` and
  performs neither teardown, and where the retry was refused the entry the reclaim removes is
  the one its own attempt left behind. Neither the tree a retry staged nor the session a retry
  started can be taken down by a previous attempt's reclaim, which a fence scoped to the
  registry entry could not guarantee and which is why the fence is minted per attempt by the
  caller.
- **A retry that meets the reclaim hold spends an attempt on it.** The refusal carries the
  `ABORTED` status §15.4 publishes as the transient classification, so the attempt keeps its
  §5.2 retryability. On the §7.3 resume the gateway classifier the non-spec changes amend is
  what holds the row in `awaiting_client_action` for the client's retry. The callers that reach the
  hold are a §7.4 mid-session upload still in flight when the session's own teardown opens it, a
  client-driven §7.3 resume, which carries no pod exclusion, and a retry the §5.2 policy places,
  when the hold it meets is one no reclaim opened. The §5.2 placement constraint covers a pod
  whose reclaim for this session did not complete, so it does not reach the hold a start-path
  rollback takes while it removes the slot's directories after the gateway abandoned the RPC it
  is unwinding: the compensating reclaim finds no entry, is answered `absent`, completes, and
  leaves the pod a placement the policy may pick again. The slot retry budget defaults to one
  retry, so a retry refused on that hold is the request's last attempt under the default and the
  client sees §5.2's exhaustion error. On a create-time-reserved slot the client's own retry of
  the §15.1 start is placed by neither mechanism and meets the hold the same way, consuming one
  of the retries the client has. The wait is the
  graceful window the reclaiming request carried when it carried one, that request's own
  deadline when it carried none, plus the removal of the slot's directories. This is accepted
  rather than closed: a gateway-side wait-and-retry inside the bind path holds the client's
  request open for the same window and adds a second place where the timeout is stated.
- **A bind that fails inside its first entry-creating RPC is fenced.** This was a residue while
  the fence travelled on a response: an attempt whose first entry-touching RPC failed could have
  created the registry entry and still have received no response carrying the fence, so it held
  none and had to send the unconditional form. The attempt token is minted before the attempt's
  first pod-side RPC and is held by the gateway rather than latched off a response, so an
  attempt holds its token throughout that window and its compensation names it. The window is
  closed rather than accepted, and closing it needs no change to the gateway-adapter error
  surface.
- **A reclaim the adapter does not acknowledge on a pod serving one session.** The failed
  attempt releases the pod's claim and the pod retires under §6.2's pre-attached failure
  disposition, so the residue dies with the pod. §6.2 states the `leaked` sub-state and §5.2
  the whole-pod replacement trigger under concurrent occupancy alone, so the disposition is
  not stated for an exclusive pod and is not needed there.
- **A start that races the reclaim.** The orderings differ in what they leave behind. When the
  adapter admitted the start before the reclaim removed the entry, the start finds its entry
  gone at the point it would record the runtime as holding the session, takes the session back
  off the shared runtime process, and refuses; the slot never reached `running` and no cleanup
  outcome is reported. When the reclaim's answer precedes the start, the cleanup has finished
  and the hold is released, so the start's own claim creates a fresh entry and stamps the
  attempt's token on it. The confirmation compares the token that same claim observed against
  the entry that carries it, so it compares equal and the start is recorded: the slot reaches
  `running` and the pod is left holding an entry and a runtime session no gateway attempt owns.
  Nothing refuses that ordering, and it is recorded among the accepted failure modes rather than
  closed. When a successor has already taken the identifier, the abandoned attempt's claim meets
  the successor's entry, which carries the successor's token, so the claim is refused before it
  can resolve the entry or record a start against it. That refusal is what removes the
  shared-entry residue an entry-scoped fence left open: two attempts never share one entry,
  because the entry belongs to the attempt that created it until it is removed.
- **A compensation still on the wire when a retry reaches the same slot is fenced.** An
  entry-scoped fence separated a reclaim addressed to a released entry from one addressed to the
  entry that replaced it, and did not separate two attempts sharing one surviving entry: a retry
  that resolved the surviving entry inherited that entry's fence value, so a teardown running
  after the retry had answered its client compared equal and tore down a serving session. The
  attempt token is the per-attempt discriminator that case needed. A retry never inherits the
  surviving entry's token, because the token is minted by the caller per attempt and the adapter
  writes it only on the entry it creates, so a retry that meets a surviving entry is refused and
  the lagging teardown removes only what its own attempt left. The residue is closed rather than
  accepted.
- **An abandoned attempt's start whose claim runs after the reclaim completed re-creates the
  entry.** The cleanup has finished and the hold is released, so the late `StartSession` claim
  creates a fresh entry stamped with its own attempt's token and starts the session, and the
  start confirmation compares the token that same claim observed against the entry that claim
  created, so it admits. The pod is left holding an entry and a runtime session no gateway
  attempt owns, which is residue class one reached by a second route. Closing it would need
  the adapter to hold the slot identifier until the gateway's reclaim has been answered, which
  keeps an identifier held across a network round trip on every failed bind and refuses the
  client's own retry for that whole window. The ordering needs the abandoned attempt's
  `StartSession` to reach the adapter after its own compensation completed, which the
  gateway's sequential bind stages make rare rather than impossible. The tier-1 hold suite
  pins it as a residue.
- **An attempt that recreates its own entry after an unconditional teardown removed it.** A
  token fences one attempt against another and cannot fence an attempt against itself. When an
  unconditional `Shutdown`, such as a `/terminate` or a §11.4 revoke, removes the entry while one
  of that attempt's own later requests is in flight, and that request is one that may
  legitimately be an attempt's first entry-creating RPC, the request recreates the entry under
  the same attempt's token and materializes from a staging tree the teardown emptied. The
  session can then reach `running` on an empty workspace. Closing it needs either a generation
  on the slot's on-disk tree or a bind-scoped lock spanning the whole attempt, each of which is
  a wider change than this proposal stages. It is recorded here and carried as a deferred item
  rather than closed.
- **A compensation lost to a gateway crash leaves an entry no attempt can use.** The reclaim is
  sent once, from the process that abandoned the attempt, so a gateway that dies between
  abandoning the attempt and sending the reclaim leaves a registry entry stamped with a token
  no live attempt holds. Every later attempt at that session on that pod is refused as
  superseded, so the session is unstartable on that pod until the pod is replaced. This is
  strictly worse than what an entry-scoped fence left, which let a later attempt adopt the
  entry, and it is the cost of refusing adoption. Two recoveries are named and neither is in
  this proposal: a durable record of the pending reclaim, written beside the session row before
  the compensating `Shutdown` is sent and re-driven from a startup sweep until the adapter
  answers, and a reaper for a registry entry no attempt owns, whose recovery primitive is the
  unconditional teardown this proposal adds. Each goes out as its own finding against the
  section that owns it.
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

No sentence about the bind attempt token is added here. `bind_attempt` is a bare `string`
carried beside the address on `PrepareWorkspaceRequest`, `FinalizeWorkspaceRequest`,
`RunSetupRequest`, `AssignCredentialsRequest`, `ResumeRequest`, and `ShutdownRequest`, and
`unconditional_teardown` is a bare `bool` carried beside the address on `ShutdownRequest`.
Neither is declared `optional`, so neither has wire presence, and neither selects an operation
or a scope: each is a precondition the handler reads on a request whose scope its own address
already fixes. The precedent sits on this same message. The shipped comment on
`ShutdownRequest.recycle` says the field "carries the occupancy-zero recycle disposition beside
the named session's teardown rather than selecting a scope"
(`schemas/lenny-adapter.proto:1621-1627`), and both new fields sit beside the address in that
way. The replacement sentence therefore delegates both teardown preconditions to §4.7, which
owns them, and states no rule about either field.

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
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response. Every request states which teardown it asks for. It carries either a non-empty `bind_attempt`, naming the bind attempt it compensates ([Section 4.7.1](#471-role-and-gateway-rpc-contract)), or `unconditional_teardown`, and it carries exactly one of the two. A request carrying neither, and a request carrying both, is answered `INVALID_ARGUMENT`: the adapter performs neither teardown, removes no entry, and changes nothing. A request naming a bind attempt performs both teardowns, under their own preconditions above, only when the entry the adapter holds for the named session carries that attempt's token. It performs neither, and removes nothing, when that entry carries a different token, when that entry carries no token, and when the adapter holds no entry for the session. A request asking for the unconditional teardown performs both teardowns, under their own preconditions above, on whatever entry the adapter holds, and it is what every caller other than a compensating reclaim sends. The response reports the outcome the reclaim took, which is `reclaimed` for an entry the request removed, `superseded` for an entry a different bind attempt owns, and `absent` for a session the adapter holds no entry for, as [Section 4.7.1](#471-role-and-gateway-rpc-contract) defines them. A superseded reclaim is neither a failed reclaim nor a leaked slot: the entry the adapter holds was created by a different bind attempt, so the naming attempt owns nothing the reclaim could release, and the reclaim correctly performed nothing. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

The two-field rule is what keeps every existing caller defined: the §11.4 revoke fan-out, the
occupancy-zero recycle edge, and `Binder.ReleaseSlot` all ask for the unconditional teardown by
setting `unconditional_teardown`, and only a compensating reclaim names a bind attempt. Stating
the unconditional form as its own field rather than as the absence of a token is what makes the
destructive form the one a caller has to ask for by name: a caller that omits the token is
answered `INVALID_ARGUMENT` rather than served a teardown it did not ask for, and an adapter
that refuses a `Shutdown` naming nothing stays conforming.

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
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the creation finalize block, the [§15.1](15_external-api-surface.md#151-rest-api) start transition, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session on the connection the failed stage still holds, before that connection closes, and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. The reclaim is sent on the connection the failed attempt already holds when that connection is still open, because reusing it costs nothing; the fence does not depend on the connection, and a reclaim sent on a fresh connection is fenced exactly as one sent on the original. The reclaim names the bind attempt it compensates by carrying that attempt's token ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)), and it may name no other. The gateway mints that token before the attempt's first pod-side RPC and holds it independently of any response, so every attempt holds its own token for the whole of the obligation and a compensation always names one. A reclaim naming an attempt that no longer holds the slot is answered `superseded`, and one for a session the adapter holds no entry for is answered `absent`; both are completed reclaims and leave the slot unleaked. On `absent` the adapter holds nothing for the session. On `superseded` the entry the adapter holds was created by a different bind attempt, so the failed attempt owns nothing the reclaim could release. A reclaim the adapter does not answer, and one answered `reclaimed` without reporting a clean exit, are the reclaims that did not complete. On a pod serving concurrent sessions a reclaim that did not complete leaves the slot `leaked` under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) disposition, which holds the slot's occupancy and counts it toward the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) whole-pod replacement trigger. The slot identifier is the session identifier, and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states that the adapter holds that identifier from the deregistration of its registry entry until the cleanup that reclaims it has finished, so a further attempt at the same session on that pod meets that hold while the reclaim runs and is refused as a transient condition. [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) keeps the retries its slot retry policy places off a pod whose reclaim for this session did not complete. A hold that no reclaim opened is outside that constraint, so an attempt the policy places can still meet one. Nothing about the attempt's retryability changes. On a pod serving one session the failed attempt releases the pod's claim and the pod retires under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) pre-attached failure disposition, so the reclaim's residue does not outlive the pod; the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there. The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated.
```

The paragraph states no deadline for the reclaim. §5.2's per-slot cleanup timeout is the
figure the gateway reuses, cited from the code rather than restated here.

The paragraph states the connection preference and no correctness rule about it. A caller holds
its own attempt token from before its first pod-side RPC, independently of any connection and of
any response, so a compensation sent on a fresh connection carries the same token and is fenced
the same way. Reusing the open connection is the cheaper path rather than the correct one, which
is what makes a durable re-drive of the compensation, after the connection or the process that
sent it has gone, buildable as its own change.

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
That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency. It also runs when a bind is abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), and a cleanup on that path reports no outcome, because `ReportSessionScrub` advances the pod's served-session count and that count records the sessions the pod's shared runtime process has been given. A cleanup on that path that a reclaiming `Shutdown` performs and that does not complete is accounted on that request's response, which does not report a clean exit. On a pod serving concurrent sessions the slot is then `leaked` under [Section 6.2](06_warm-pod-model.md#62-pod-state-machine), so it holds its occupancy, counts toward the whole-pod replacement trigger stated below, and is surfaced on the `lenny_adapter_leaked_slots` gauge. On a pod serving one session the disposition is the one [Section 7.1](07_session-lifecycle.md#71-normal-flow) states: the failed attempt releases the pod's claim and the pod retires, so the reclaim's residue does not outlive the pod. A cleanup the adapter runs inside the failed start's own handler reports nothing and no response carries its outcome, so its residue is accounted at the whole-pod boundary instead: on a recycling pod the occupancy-zero whole-pod scrub stated below removes every per-slot workspace tree and every per-slot credential file the pod still holds and verifies their absence, and a verification that fails is handled under the pool's `onScrubFailure` policy stated below; a pod that does not recycle retires at that boundary and the residue does not outlive it. A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome. A session release produces at most one cleanup-outcome report, filed by the cleanup that reclaims the slot.

**Slot-identifier reclaim hold.** The adapter holds a slot's identifier from the critical section that deregisters the slot's registry entry until the cleanup that reclaims the slot has finished. The hold outlasts the deregistration because the cleanup's remaining acts are addressed by the slot identifier rather than by the entry: the removal of the slot's workspace directory, the removal of its credential directory, the kill of its process group, and the close of the session on the pod's shared runtime process are each resolved from the identifier, and every attempt at the same session names the same identifier. The hold is a property of the identifier rather than of an entry, because the entry is what a later attempt would re-create and because the adapter's other rules read the entry set as the pod's live occupancy. While the identifier is held the adapter admits no request that would create or resolve a registry entry under it, and refuses one as a transient condition so the caller retries. A refusal is the only record the adapter makes of the hold: no report and no counter names it, and a bind refused this way is accounted by the gateway as an ordinary transient slot failure. The hold lasts as long as the cleanup it covers. The cleanup's close of the session on the pod's shared runtime process is bounded by the graceful window the reclaiming request carries when the request carries one, by that request's own deadline when it carries none, and, for a hold taken outside any request, by the termination window of the pass that runs the cleanup. The cleanup's removal of the slot's directories runs after that close under no deadline, so the hold outlasts the window by the time that removal takes. The `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` figure the **Slot cleanup:** bullet states is the deployer's budget for that cleanup and is not a second bound on the hold. A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it. [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token the adapter stamps on a slot's registry entry and the reclaim that names it.
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
the bind attempt token once, on the section that owns the gateway-adapter RPC contract, so the
§4.7 `Shutdown` row and §7.1 each point at one statement rather than carrying two. §4.1 states
nothing about either new field, for the reason the SPEC-1 commentary above gives:

```
**Bind attempt token.** A **bind attempt** is one gateway attempt to bind a session onto a pod, running from that attempt's first pod-side RPC for the session until the attempt succeeds or is abandoned. The gateway mints a **bind attempt token** for each attempt: one opaque string value, drawn from a cryptographically secure random source, minted once, before the attempt issues its first pod-side RPC. The adapter compares the token for equality and does nothing else with it. It parses no structure out of it, derives no order and no age from it, and never mints one of its own. The empty string is not a token, so a request whose `bind_attempt` is empty names no attempt. A token belongs to an attempt rather than to a session, so two attempts at one session hold different tokens, and it is never derived from the session identifier, from the slot identifier, or from a `coordination_generation`. It is not a coordination generation and carries none of that field's semantics. The two answer different questions: the generation names the gateway replica that speaks for the session ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) and is validated on the RPCs that carry it, while the token names the bind attempt a registry entry belongs to. Where both appear on one message, as on `Shutdown`, each is checked on its own terms.

The requests that carry `bind_attempt` are `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `Resume`, and the compensating `Shutdown`. Those are the requests through which a bind attempt creates or resolves a registry entry before its session starts, together with the reclaim that compensates the attempt. `StartSession` and `ConfigureWorkspace` carry none. A start may be issued by a later stage of the same binding than the stage that created the entry, so comparing a token on either of them would refuse a start against an entry the same binding legitimately created; the started-session rule below is what governs those two requests instead. No response reports a token. Nothing a caller must hold travels on a response, so a caller that received no response at all still holds the token it minted, and a caller on a fresh connection holds it exactly as the caller on the original connection did.

**The adapter stamps once, on the entry it creates.** When the adapter creates a registry entry for a slot identifier it holds none for, it writes the requesting attempt's token onto that entry. It never writes that field again, in either direction: an entry's token does not change while the entry lives, and a request that resolves an entry the adapter already holds writes nothing. The first attempt to create an entry therefore owns it until the entry is removed, and no later request takes that ownership away. The adapter resolves or creates the entry, stamps the token on an entry it creates, and performs the comparisons stated below as one indivisible step, under the same lock that guards the registry. Performing them as separable steps does not conform even when each step is correct on its own, because an adapter that resolved an entry, released its lock, and then stamped admits a second attempt onto that entry in the window between the two.

**Admission.** The adapter applies the rules below to every request that would create or resolve a registry entry other than `Shutdown`, whose own rule is stated further below, and it applies them inside that same step, before it creates anything, stamps anything, or returns. A request marked `mid_session` that resolves no entry is answered `FAILED_PRECONDITION` and creates nothing. A request that is not marked `mid_session` and that resolves no entry creates the entry and stamps its token on it. A request whose non-empty `bind_attempt` differs from the non-empty token the resolved entry carries is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED`, which [Section 15.1](15_external-api-surface.md#151-rest-api) classifies as transient and a caller retries on. A request that is not marked `mid_session` and that resolves an entry whose session has already started is refused with `SLOT_BIND_ALREADY_STARTED`, which [Section 15.1](15_external-api-surface.md#151-rest-api) classifies as permanent. Any other request is admitted, and an admitted request that resolved an entry leaves that entry's token as it found it, whether the request carried a token or not.

On those requests, `bind_attempt` is non-empty exactly when `mid_session` is false. A request that is not marked `mid_session` and carries an empty `bind_attempt`, and a request that is marked `mid_session` and carries a non-empty one, are each answered `INVALID_ARGUMENT`, and the adapter creates, resolves, and changes nothing. A [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload is the marked form: it reuses `PrepareWorkspace` and `FinalizeWorkspace` on the entry the session already holds, asserts no attempt identity, cannot create an entry, and is exempt from the started-session rule, because the session a mid-session upload runs against has started by definition. Because such a request asserts no attempt identity, the gateway issues one only for a session the issuing replica holds a live binding for. That admission is what keeps a request asserting no identity away from an entry its sender does not own.

`PrepareWorkspace` is client-streaming. The adapter resolves the slot identifier once, from the first frame that carries one, and reads `bind_attempt` and `mid_session` from that same frame. A later frame of the same call whose `bind_attempt` is non-empty and differs from the first frame's, or whose `mid_session` differs from the first frame's, is refused, and the call resolves nothing further. A later frame that repeats the first frame's values, and one that carries an empty `bind_attempt` where the first frame carried one, are not disagreements.

**`Shutdown` states which teardown it asks for.** A `Shutdown` carries either a non-empty `bind_attempt`, naming the bind attempt it compensates, or `unconditional_teardown`, and it carries exactly one of the two. A request carrying neither, and a request carrying both, is answered `INVALID_ARGUMENT`: the adapter performs neither teardown, removes no entry, and changes nothing. The comparison runs inside the same critical section as the deregistration and precedes it, so an entry the adapter refuses to release is never removed and restored.

A `Shutdown` asking for the unconditional teardown removes whatever entry the adapter holds for the named session, and answers `reclaimed` when it held one and `absent` when it held none. A `Shutdown` naming a bind attempt removes the entry and answers `reclaimed` only when that entry carries the named attempt's token. It removes nothing and answers `superseded` when the entry carries a different token and when the entry carries no token, and it removes nothing and answers `absent` when the adapter holds no entry for the session. The entry-carries-no-token case answers `superseded` so that the rule fails closed. Every bind attempt mints its token before its first pod-side RPC and a request marked `mid_session` cannot create an entry, so no compensated path produces an entry carrying no token, and a caller that produces one is refused rather than served.

`reclaimed`, `superseded`, and `absent` are the outcomes a `Shutdown` response reports, and each has one meaning. `reclaimed` means the adapter held the entry the request was addressed to and released the slot. `superseded` means the adapter holds an entry for the session that the request is not addressed to, so a different bind attempt owns the slot identifier and the reclaim released nothing. `absent` means the adapter holds no entry for the session. All of them are successful outcomes and are answered on a successful RPC: a `Shutdown` is never refused for naming an attempt that does not own the entry, because `superseded` is its answer for that case. Every other RPC on this contract neither carries nor reports a bind attempt token. This block states no refusal onto a slot identifier; the refusal a cleanup in progress imposes is stated by the reclaim hold in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes).
```

The block sits in §4.7.1 rather than in §4.7.9 because §4.7.9 is a `type: agent`
orientation list and the token binds every session mode the RPC tables serve. The
comparison the adapter performs on a `Shutdown` and the outcomes it answers are restated in the
§4.7 `Shutdown` row, which is where a reader of that RPC looks; this block states what the value
is, which requests carry it, when the adapter stamps it, which requests it refuses, and who may
name it.

Two things the block states are obligations this proposal takes on rather than shipped
behaviour it records. The atomicity clause makes first-writer-wins implementable from the prose,
which it is not while the resolve, the stamp and the comparison read as three acts. And the
sentence on the gateway's own admission of a mid-session upload is the whole safety argument for
a request that asserts no attempt identity, so the implementation confirms that the shipped
mid-session admission guard reads what it appears to read before the mid-session conditioning
lands, and records what it found.

### SPEC-5 · spec/15_external-api-surface.md § 15.1 (error catalog)

Add two rows to the §15.1 error catalog table, in the block that carries the adapter-originated
codes, beside `WORKSPACE_PLAN_SCHEMA_UNSUPPORTED` and `SETUP_COMMAND_FAILED`. The adapter's
`ErrorCode` enum states that the catalog mirrors this section, so a code the adapter can emit
that has no row here is a defect in the catalog. Both codes are emitted by the adapter as a
gRPC status detail; the gateway translates each to a Go typed error and surfaces it under the
category the row states.

```
| `SLOT_BIND_ATTEMPT_SUPERSEDED` | `TRANSIENT` | 503  | A bind-sequence request resolved a slot registry entry that a different bind attempt owns, or that carries no attempt token. The request performed nothing. Surfaced with `Retry-After` at whichever endpoint issued the bind sequence; the client's retry mints a new attempt. See [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract). |
| `SLOT_BIND_ALREADY_STARTED` | `PERMANENT` | 409  | A bind-sequence request that is not a mid-session upload resolved a slot registry entry whose session has already started on that pod. The request performed nothing and the running session is untouched. Surfaced at whichever endpoint issued the bind sequence. See [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract). |
```

The HTTP status for `SLOT_BIND_ALREADY_STARTED` follows the `INVALID_STATE_TRANSITION` and
`RESOURCE_ALREADY_EXISTS` precedent for a request refused because the resource is already in
the state the request would produce. The endpoint precondition tables in this section keep a
client retry of `POST /v1/sessions/{id}/start` off a `running` row, so the row is reached only
through a session row that stayed `ready` after its launch published a live binding, which the
gateway-side finding recorded in the proposal's summary names.

### SPEC-5 · spec/15_external-api-surface.md § 15.4 (after the SDK-warm demotion contract)

Insert the two blocks below immediately after the paragraph beginning `**SDK-warm demotion
contract:**` and before the `#### 15.4.1 Message Format and Binary I/O Requirements`
heading. §15.4 is where the adapter contract is published to third-party adapter authors, so
the statement here is written to be implementable without reading any Go and states the
non-conformance case for each part:

```
**Bind attempt token contract:** [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token: what it is, which requests carry it, when the adapter stamps it, which requests it refuses, and how a `Shutdown` states which teardown it asks for. That statement is normative for a third-party adapter and is not restated here. The clauses below state what non-conformance looks like against it.

An adapter does not conform when it admits a request that is not marked `mid_session` and that resolves an entry whose session has already started, instead of refusing it with `SLOT_BIND_ALREADY_STARTED`.

An adapter does not conform when it admits a request whose non-empty bind attempt token differs from the non-empty token the resolved entry carries, instead of refusing it with `SLOT_BIND_ATTEMPT_SUPERSEDED`.

An adapter does not conform when it writes a bind attempt token onto an entry it resolved rather than created, in either direction. It does not conform when it performs the resolve, the stamp and the comparison as separable steps rather than as one indivisible step under the lock that guards the registry, because two attempts can then be admitted onto one entry.

An adapter does not conform when it admits a request that is not marked `mid_session` and carries an empty bind attempt token, or a request that is marked `mid_session` and carries a non-empty one, instead of answering `INVALID_ARGUMENT`.

An adapter does not conform when it creates a registry entry for a request that is marked `mid_session` and resolves none, instead of answering `FAILED_PRECONDITION`.

An adapter does not conform when it resolves the slot identifier more than once within one `PrepareWorkspace` call, or when it admits a later frame of that call whose non-empty bind attempt token or whose mid-session marker disagrees with the first frame's.

An adapter does not conform when it creates a registry entry for a `Resume` without stamping that request's bind attempt token on the entry it creates.

An adapter does not conform when it performs the slot release or the runtime teardown for a `Shutdown` whose non-empty bind attempt token differs from the token the resolved entry carries, or for one whose resolved entry carries no token, instead of answering `superseded` and performing neither.

An adapter does not conform when it treats a `Shutdown` carrying an empty bind attempt token as a reclaim conditioned on that token. It does not conform when it performs either teardown for a `Shutdown` that carries neither a non-empty bind attempt token nor `unconditional_teardown`, or for one that carries both, instead of answering `INVALID_ARGUMENT` and performing nothing. It does not conform when it performs neither teardown for a `Shutdown` that asks for the unconditional teardown.

The `Shutdown` response reports the reclaim's outcome and each outcome has one meaning. `reclaimed` means the adapter held the entry the request was addressed to and released the slot. `superseded` means the adapter holds an entry for the session that the request is not addressed to, so a different bind attempt owns the slot identifier and the reclaim released nothing. `absent` means the adapter holds no entry for the session. Each of them is a successful outcome and is answered on a successful RPC. An adapter that answers a gRPC error for a superseded or an absent reclaim does not conform.
```

```
**Slot-identifier reclaim hold:** [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold: the adapter holds a slot's identifier from the deregistration of that slot's registry entry until the cleanup that reclaims the slot has finished, because the cleanup's remaining acts are addressed by the identifier rather than by the entry. This is what an adapter must exhibit on the wire. While the identifier is held, a request that would create or resolve a registry entry under it is refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on. An adapter that admits such a request while the cleanup is running does not conform. An adapter that refuses one with a permanent status, or that answers a status the caller cannot retry, does not conform. `Shutdown` is not held: a `Shutdown` naming a session whose cleanup is running removes nothing and answers as [Section 4.7](04_system-components.md#47-runtime-adapter) states for a session the adapter holds no entry for.
```

Both blocks state the contract and neither states a Go type, a field number, or a package
name. The wire form is `schemas/lenny-adapter.proto`, which §15.4 already names as the
published artifact. The token travels on the request side alone, so the wire edit that lands it
adds one string field to each of the six requests §4.7.1 names, the mid-session marker to
`PrepareWorkspace`, the unconditional-teardown flag and the outcome to `Shutdown`, and a value
to the error enum for each of the two refusals. No response reports a token.

The clauses are normative for a third-party adapter, and the project has no harness that can run
one: the conformance battery drives a runtime binary over JSONL against a fake adapter and speaks
no gRPC. The wire-level enforcement is therefore the tier-3 contract suite, which drives this
adapter over a real gRPC channel and can exercise every clause above, alongside a descriptor gate
pinning the field numbers and types. The in-process battery at tier 10 drives the same adapter
without the wire. The absence of a third-party harness is recorded as a §28.4 claim-register row
with status `ABSENT`, following the rows the `coordination_generation` fence already carries.

### SPEC-6 · spec/16_observability.md § 16.1 (metric catalog)

Add three rows to the §16.1 metric catalog table, beside the session-slot failure count. The
first two are gateway series emitted from the compensation's outcome mapping; the third is an
adapter series and carries the adapter metrics endpoint's existing deferral (the adapter process
exposes no scrape target today), so its row records the series for the register and names the
deferral.

```
| Slot compensation removed a started entry (`lenny_slot_compensation_removed_started_total`, labeled by `pool`, `k8s_pod_name` — a compensating `Shutdown` answered `reclaimed` for an entry whose session had started; zero on a conforming deployment, and non-zero is the identity gate's regression signal) | Counter |
| Slot compensation superseded (`lenny_slot_compensation_superseded_total`, labeled by `pool`, `k8s_pod_name` — a compensating `Shutdown` answered `superseded` because a later bind attempt owns the entry; non-zero on a system that retries, and is the count of retries the fence protected) | Counter |
| Slot shutdown met an untokened entry (`lenny_slot_shutdown_untokened_entry_total`, labeled by `k8s_pod_name` — a `Shutdown` carrying an attempt token met a registry entry that carries none; zero on a conforming adapter. Adapter-side; not scraped until the adapter metrics endpoint is wired) | Counter |
```

## Spec sections deliberately untouched

Listed so a reviewer can tell scope from oversight.

- **§15.4.6's conformance categories.** They exercise the runtime binary over JSONL against a
  fake adapter, so they have no adapter under test and cannot observe an adapter obligation.
  CONF-1 lands in the tier-10 conformance battery instead, and the wire-level clauses land in
  the tier-3 contract suite.
- **§5.2's non-retryable failure categories.** The list is scoped to
  `maxConcurrentSessions > 1` and the reclaim hold is not, and the refusal the hold returns
  is transient, so no category is added or removed.
- **§10.1's coordinator handoff.** The bind attempt token is not a coordination generation and
  changes nothing about the handoff.
- **§28's registers.** `Shutdown` appears in no §28 register row, and §28.5.1 is organised
  per channel rather than per field, so a new field on an existing message adds no row.

## Spec files touched

- `spec/04_system-components.md`: §4.1 Request Message Scope (one sentence replaced), §4.7
  Gateway → Adapter RPC table `Shutdown` row (first sentence replaced, the no-op sentence and
  the two-field rule with its outcomes added), §4.7.1 (the bind attempt token block, new, after
  the RPC tables), §4.7.9 step 5 (one sentence).
- `spec/05_runtime-registry-and-pool-model.md`: §5.2 `**Scrub model.**` paragraph (the
  per-slot cleanup pointer, the cleanup-outcome report rules, and the slot-identifier
  reclaim-hold paragraph appended, whose closing sentence points at the bind attempt token), the §5.2
  `**Slot cleanup:**` bullet's action-list sentence (replaced), and the §5.2
  slot-retry-policy `**Max retries:**` bullet's pod-selection sentence (replaced).
- `spec/06_warm-pod-model.md`: §6.2 per-slot sub-state fence (one edge), the prose after
  it (one paragraph, pointing at the §5.2 reclaim hold), and the §6.2
  `resuming` mid-resume cancel bullet (one clause).
- `spec/07_session-lifecycle.md`: §7.1 atomicity paragraph (one parenthetical) and a new
  paragraph after it carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/15_external-api-surface.md`: §15.1's error catalog (one row for
  `SLOT_BIND_ATTEMPT_SUPERSEDED` and one for `SLOT_BIND_ALREADY_STARTED`, each with its
  category, its HTTP status, and its description), and §15.4 (the published bind attempt token
  contract and the slot-identifier reclaim-hold contract, new, after the SDK-warm demotion
  contract).
- `spec/16_observability.md`: §16.1's metric catalog (one row for each counter
  the compensation's outcome mapping and the adapter's fail-closed row emit).
- `spec/29_communication-scenarios.md`: §29.4 session-end step 13 (one sentence appended).

Two files outside `spec/` move with them. `docs/reference/error-catalog.md` gains the published
row for each of the two new codes, matching the §15.1 rows, and `docs/reference/metrics.md`
gains the row for each counter, matching the §16.1 rows, so the catalogs a reader consults and
the catalogs the specification states stay one list each. The non-spec changes carry both
edits as DOCS-3 and CODE-9.