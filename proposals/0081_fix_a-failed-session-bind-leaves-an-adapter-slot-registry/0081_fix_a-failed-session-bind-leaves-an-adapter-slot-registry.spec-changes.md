# Spec changes: A failed session bind leaves a stale adapter slot registry entry

These edits are staged. Applying them is the implementation's work, under the
implementation checklist.

## Design (as the spec must state it)

The specification currently states the defect as the contract. §4.1's derivation paragraph
and the §4.7 `Shutdown` row both treat the runtime close and the slot release as one act
gated on the binding, §4.7.9 states the bind sequence with no failure branch, and §6.2's
per-slot sub-state machine has no edge into `slot_cleanup` from either pre-`running` state.
Each paragraph below records one design choice, its ground, and the staged block that owns
the rule. The staged blocks are the statement of every rule.

**One teardown becomes two.** A `Shutdown` performs a slot release and a runtime teardown, each
under its own precondition, so a session whose bind never completed can give up its slot. The
teardown over-approximates toward closing and the cleanup-outcome report under-approximates
toward not counting, which is the safe direction for each. Owner: the staged §4.7 `Shutdown`
row.

**The reclaim obligation is a §7.1 paragraph of its own.** It binds bind paths the atomicity
paragraph's heading and its closing sentence exclude, so it cannot sit inside that paragraph,
and §7.2, §7.3, §4.7.9 and §6.2's mid-resume cancel edge point at it. No failure class changes
retryability, and the specification states no reclaim deadline. Owner: SPEC-2's §7.1 paragraph.

**The cleanup-outcome report follows the `running` boundary.** A report for a slot that never
reached `running` would advance `recycle.maxSessionsPerPod` retirement for a session the pod did
not serve and would count a re-bound session twice. The rule sits in the scrub-model paragraph
because the `**Slot cleanup:**` bullet is scoped to `maxConcurrentSessions > 1` and the rule is
not. Owner: the staged §5.2 `**Scrub model.**` opening sentence.

**The `running` boundary sits where the adapter records the session on the shared runtime
process.** The record is the one event a later request can read under the registry lock, and the
start-confirmation rule takes the session back off that process when its confirmation fails, so a
slot the adapter never recorded is owed no cleanup-outcome report. No state is added, so the
existing terminals stay authoritative. Owner: the staged §6.2 pre-`running` paragraph and fence
edge.

**Every cleanup's disposition is one table.** What a cleanup reports, what its `Shutdown`
answers, whether the slot is `leaked`, and when the identifier hold ends are one matrix, and a
table makes a missing cell visible where prose does not. What a cleanup that fails or does not
run leaves on the pod, and what ends it, follows from the action list and which acts ran, so it
is stated in the paragraph after the table rather than in a column. §5.2 owns both because
§5.2 owns the cleanup and the report, and §6.2, §7.1 and the reclaim-hold paragraph cite them.
Owner: the table and the paragraph after it in the staged §5.2 `**Scrub model.**` append.

**The token is minted by the caller, once per attempt, and the first writer owns the entry.**
The compensation can reach the adapter after the client's retry has bound the same slot
identifier, and an entry-scoped fence is inherited by a retry that resolves the surviving
entry. A caller-minted token is held from before the attempt's first pod-side RPC, so an
attempt that received no response still names itself. The identity refusal is transient and
is applied before the permanent started-session refusal, so a stale attempt is retried rather
than failed. Neither refusal takes a §15.1 row, because the gateway consumes both. Owner: the
staged §4.7.1 block.

**The adapter's atomicity is stated once.** First-writer-wins, the start confirmation and the
opening of the hold are each unimplementable from prose that reads as separable acts. One
paragraph lists every step the adapter performs under the registry lock, and the rules cite
it. Owner: the staged §4.7.1 registry critical-section paragraph.

**`Shutdown` names its teardown in two fields.** Stating the unconditional form as its own
field makes the destructive form the one a caller asks for by name, so a caller that omits the
token is answered with an error rather than a destroyed session. Owner: the staged §4.7.1
teardown rules.

**The adapter holds the slot identifier while its cleanup runs.** The token says nothing about
an admitted reclaim that races a successor, because the destructive acts are addressed by the
slot identifier every attempt shares. The hold is a property of the identifier, and it carries
a terminal for a cleanup that does not complete, because a successor would otherwise bind over
the residue. Owner: the staged §5.2 reclaim-hold paragraph.

## Edge cases and accepted failure modes

- **A compensation for a session the adapter holds nothing for.** `stageWorkspace` fetches
  upload blobs and rewrites archive and `gitClone` sources before it sends
  `PrepareWorkspace`, so a failure there leaves no adapter entry, as does a bind whose
  request never reaches the pod. §4.7.1's no-entry rule answers `absent`
  here and its reclaim-outcome rule makes that a clean-exit response, so the failure is
  accounted transient rather than leaked and adds nothing to the pod's persistent leak
  count. The windowed failure counter still records it, and at `maxConcurrentSessions: 2` a
  single windowed failure already reaches the §5.2 whole-pod replacement threshold.
- **A reclaim the adapter does not acknowledge on a pod serving concurrent sessions.** SPEC-3's
  §5.2 disposition table states the slot's disposition, and the pod's retirement through the
  whole-pod replacement threshold is what bounds
  a best-effort compensation's failure. §5.2 does not steer a retry away from that pod: the
  policy places a retry on a new slot on the same pod when one is available, where it names the
  same slot identifier and is refused on the attempt token or on the reclaim hold. Each refusal
  is transient and spends one of the request's attempts. The retry is placed on another pod
  only when this pod is saturated, or when the leaked slot has carried the pod past §5.2's
  `ceil(maxConcurrentSessions / 2)` replacement threshold, which one leak does at
  `maxConcurrentSessions: 2` and does not above it. Steering the retry would need the
  per-request pod exclusion this revision withdrew, and the pod-side refusal replaces it, so
  this disposition is accepted rather than closed.
- **A client retry of the §15.1 start after a failed bind on a create-time-reserved slot.**
  The row keeps its §4.6 pod binding, so the retried start reconnects to the same pod under
  the same slot identifier. The attempt token and the reclaim hold are what govern
  this path, and the orderings differ in what they cost. A retry that arrives while the
  first attempt's cleanup is still running is refused on the reclaim hold, as a transient
  condition, and binds on a further attempt. A retry that arrives before the lagging reclaim
  does meets the abandoned attempt's surviving entry, and its first entry-resolving RPC carries
  its own freshly minted token, which differs from the token the entry carries, so the adapter
  refuses it as a transient condition before it touches workspace, setup, credential, or
  registry state. That refusal consumes one of the client's own retries. The lagging reclaim
  then names the token the entry does carry, compares equal, and removes the entry, so the
  attempt after it creates a fresh entry and owns it under its own token. A retry that arrives
  after the cleanup finished materializes the slot afresh and owns it the same way.
- **A retry that meets the reclaim hold spends an attempt on it.** The refusal carries the
  `ABORTED` status §15.4 publishes as the transient classification, so the attempt keeps its
  §5.2 retryability. On the §7.3 resume the gateway classifier the non-spec changes amend is
  what holds the row in `awaiting_client_action` for the client's retry. The callers that reach the
  hold are a §7.4 mid-session upload still in flight when the session's own teardown opens it, a
  client-driven §7.3 resume, and a retry the §5.2 policy places. An upload that arrives after
  the teardown pruned the session's pod binding reaches no adapter, so only one that resolved the
  binding earlier meets the hold. The hold a §5.2 retry meets can be one no reclaim opened: the
  first attempt's `StartSession` deadline expires at the gateway, the handler runs on to a
  failure ahead of the runtime start and opens the hold through its own cleanup, and the
  compensating `Shutdown` finds no entry and is answered `absent`. The cost differs by caller.
  The resume and the §5.2 retry each cost the pod one windowed failure, and the upload costs the
  pod nothing and reaches its client as an upstream error. The slot retry budget defaults to one
  retry (`maxSlotRetries` in `pkg/gateway/sessionserver/start.go`), so a retry refused on that
  hold is the request's last attempt under the default and the client sees §5.2's exhaustion
  error. On a create-time-reserved slot the client's own retry of
  the §15.1 start meets the hold the same way, consuming one
  of the retries the client has. How long the hold lasts is stated per cleanup in the hold
  column of SPEC-3's §5.2 disposition table. This is accepted
  rather than closed: a gateway-side wait-and-retry inside the bind path holds the client's
  request open for the same window and adds a second place where the timeout is stated.
- **A failed bind on a pod serving one session, which no reclaim reaches.** The failed
  attempt is disposed of by `failPhase`, which sends no `Shutdown` and deletes the pod's claim
  while the pod projects `claimed` (`failPhase` in
  `pkg/gateway/podlifecycle/podsession/binder.go`; `ProjectOccupancyPhase` in
  `pkg/controller/warmpool/occupancy.go`). SPEC-3's §5.2 disposition table states the
  disposition, in its row for a pre-`running` slot no cleanup reclaims, and this list does not
  restate it.
- **A start that races the reclaim.** The orderings differ in what they leave behind. When the
  adapter admitted the start before the reclaim removed the entry, the start finds its entry
  gone at the point it would record the runtime as holding the session, and the
  start-confirmation rule of §4.7.1 states what it does then. §15.4 publishes non-conformance
  against that rule, so an adapter written from the published contract performs it. When the reclaim's answer precedes the start, the
  bullet on an abandoned attempt's start re-creating the entry records what stands there. When a
  successor has already taken the identifier, the abandoned attempt's claim meets the
  successor's entry, and the started-session rule is what answers it, because the claim asserts
  no attempt identity: the claim is refused once the successor's session has started, and it
  resolves the successor's entry and starts a session on it while the successor's session has
  not. An entry a token-carrying request created belongs to the attempt that created it until
  the entry is removed, so two such attempts never share one entry. A start asserts no identity
  and is outside that guarantee.
- **An abandoned attempt's start whose claim runs after the reclaim's cleanup completed re-creates the
  entry.** The cleanup has completed and the hold is released, so the late `StartSession` claim
  creates a fresh entry and starts the session. `StartSession` carries no bind attempt token, so
  the entry it creates carries none. The pod is left holding an entry and a runtime session no
  gateway attempt owns, which is residue class three reached by a second route, and the entry is
  one no named reclaim can release: a `Shutdown` naming an attempt answers `superseded` against
  an untokened entry and removes nothing, and nothing else the failed attempt sends removes it
  either. The entry stands until a request that removes an entry without naming a bind attempt
  runs, which is an unconditional teardown, an SDK demotion, or the §10.1 hold-timeout
  termination, or until the pod retires, and a later bind attempt at that session on that pod
  meets the started-session rule while it stands. Closing it would need
  the adapter to hold the slot identifier until the gateway's reclaim has been answered, which
  keeps an identifier held across a network round trip on every failed bind and refuses the
  client's own retry for that whole window, or the narrower rule that bars a start from creating
  an entry at all. Both are staged at position 2 of the gateway-runtime-comms remediation plan
  rather than here. The ordering needs the abandoned attempt's
  `StartSession` to reach the adapter after its own compensation's cleanup completed, which the
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
  the binding rather than on `started`. The §4.7 `Shutdown` row states the condition; this case
  records why it is keyed on the binding, so the two teardowns do not read as sharing one
  precondition.
- **A bind-sequence refusal reaches the client under the envelope its stage already selects.**
  The gateway consumes both refusal codes, and this proposal leaves its envelope selection
  alone, so a workspace-stage refusal reaches the client under the transient session-start
  envelope. In the setup window the gateway renders only the setup-command request's failure
  into that envelope, and there it branches on the gRPC code rather than on the window, so an
  already-started refusal at that request, answered on `FAILED_PRECONDITION`, reaches the client
  as the non-retryable `SETUP_COMMAND_FAILED` that §15.1 defines for a deterministic
  setup-window failure, while a superseded refusal, answered on `ABORTED`, reaches it as the
  retryable session-start fallback carrying `Retry-After`. The client-visible code therefore
  names the stage the refusal arrived in rather than the refusal itself. The category and the
  retryability the client reads are correct in every case, and narrowing the code is outside
  this proposal. SPEC-5 states both of the setup-command request's deterministic
  `FAILED_PRECONDITION` causes in the §15.1 `SETUP_COMMAND_FAILED` row and qualifies that row's
  setup-output remedy to the cause that ran a command, so §15.1's catalog is true for this
  refusal. DOCS-3 mirrors those same replacements into the `SETUP_COMMAND_FAILED` row of
  `docs/reference/error-catalog.md`, so the page a reader consults states the setup-command
  refusal cause, its non-retryability, and the setup-output remedy qualified to the cause that
  ran a command.

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
The handler runs the slot release and the runtime teardown under the preconditions [Section 4.7](#47-runtime-adapter) states, and runs the whole-pod scrub when the recycle disposition is set on a request that passed the teardown-pairing rule [Section 4.7.1](#471-role-and-gateway-rpc-contract) states, whatever outcome that request answers, so no operation is selected by a field's presence standing in for a scope.
```

No sentence about the bind attempt token is added here. `bind_attempt` is a bare `string` and
`unconditional_teardown` a bare `bool`, each carried beside the address on the requests the
carriage table in [Section 4.7.1](#471-role-and-gateway-rpc-contract) names.
Neither is declared `optional`, so neither has wire presence, and neither selects an operation
or a scope: each is a precondition the handler reads on a request whose scope its own address
already fixes. The precedent sits on this same message. The shipped comment on
`ShutdownRequest.recycle` says the field "carries the occupancy-zero recycle disposition beside
the named session's teardown rather than selecting a scope"
(`schemas/lenny-adapter.proto`), and both new fields sit beside the address in that
way. The replacement sentence therefore delegates both teardown preconditions to §4.7, which
owns them, and states no rule about either field. The scrub clause restates rule 10 and
CODE-1: a request the pairing rule refuses changes nothing, and every other outcome starts the
scrub.

The paragraph's first two sentences are untouched and this edit retires no vocabulary. The
second sentence ("The per-slot teardown and the whole-pod teardown are the same operation on
the same address, and what remains is the recycle disposition the request carries beside it.")
states where the work is addressed. That is what the subsection needs from it, because the
subsection derives a message's scope from its field set and the paragraph's closing clause concludes that no
operation is selected by a field's presence standing in for a scope. The split names each
operation, leaves each precondition to §4.7, and leaves the address-sharing claim true. The second
sentence's two names already disagree with the shipped third sentence's "per-session
teardown" and "whole-pod scrub" before this edit applies, so that mismatch belongs to the shipped paragraph and
the implementor changes neither sentence here.

### SPEC-1 · spec/04_system-components.md § 4.7 (Gateway → Adapter RPC table, `Shutdown` row)

Replace the `Shutdown` row's first sentence. The row currently
opens, verbatim:

```
| `Shutdown` | Graceful end-of-session teardown of the named session: the adapter closes that session's runtime and releases the session's slot. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

Replace that opening with the text below, leaving the remainder of the row (from "On the
default disposition the pod is replaced." to the end) unchanged:

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the request removes an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. Every request states which teardown it asks for, by carrying either a non-empty `bind_attempt` ([Section 4.7.1](#471-role-and-gateway-rpc-contract)) or `unconditional_teardown`, and the response reports which entry the request was addressed to and what became of it. [Section 4.7.1](#471-role-and-gateway-rpc-contract) states the named rules that decide both, and states what each of `reclaimed`, `superseded`, and `absent` means; this row restates neither. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

The two-field rule is what keeps every existing caller defined: the §11.4 revoke fan-out, the
occupancy-zero recycle edge, and `Binder.ReleaseSlot` all ask for the unconditional teardown by
setting `unconditional_teardown`, and only a compensating reclaim names a bind attempt. The row
names the two fields and points at the named rules rather than restating them. Its neighbours in
that table are one-line RPC descriptions, and the restatement it carried had already drifted: it
stated `superseded` as the outcome for an entry a different bind attempt owns, two sentences
after correctly stating that an entry carrying no token answers `superseded` as well. Stating
the unconditional form as its own field rather than as the absence of a token is what makes the
destructive form the one a caller has to ask for by name: a caller that omits the token is
answered `INVALID_ARGUMENT` rather than served a teardown it did not ask for, and an adapter
that refuses a `Shutdown` naming nothing stays conforming.

The row does not enumerate the slot release's actions; §5.2 owns that list. It does not name
`ReportSessionScrub` either, because §5.2 states the report's condition and the disposition table
after SPEC-3.

### SPEC-1 · spec/04_system-components.md § 4.7 (Gateway → Adapter RPC table, `DemoteSDK` row)

Replace the `DemoteSDK` row's opening clause. The row currently reads, verbatim:

```
| `DemoteSDK`          | Tear down the pre-connected SDK process and return the pod to pod-warm state (see [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). Required for runtimes that declare `preConnect: true`. |
```

Replace that row with:

```
| `DemoteSDK`          | Tear down the pre-connected SDK process, remove the adapter's slot registry entry for the session the pod holds if it holds one, and return the pod to pod-warm state (see [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like)). The request names no session, and [Section 6.1](06_warm-pod-model.md#61-what-a-pre-warmed-pod-looks-like) admits `preConnect` only at `maxConcurrentSessions: 1`, so the entry it removes is the registry's single entry, and the removal is a slot release whose [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) per-slot cleanup the adapter runs inside this request, before it answers. Required for runtimes that declare `preConnect: true`. |
```

This row is the single home of the registry removal and of the timing of the cleanup that
release runs. Two statements elsewhere in this proposal rest on it: the §5.2 slot-identifier
reclaim hold's sentence on a release that runs its cleanup inside its own RPC, which ends the
hold that release opens when the cleanup completes, and §4.7.1 rule 4 (**the
create-and-stamp rule**), under which the bind that follows a demotion creates a fresh entry
stamped with its attempt's token, only because the demotion left no entry for that identifier.
Neither is derivable from the row as it stood before this replacement, which named no release
and no cleanup, so the hold a demotion's deregistration opens had nothing in the applied
specification to close it and the pod-warm bind sequence that follows the demotion met a held
identifier for the life of the pod.

The row names the release and the cleanup's timing and states neither the cleanup's actions nor
its outcome. §5.2 owns the action list and the disposition table, which carries the row for a
release outside a `Shutdown` whose cleanup fails, so the row asserts no completion guarantee.
The row also pulls in no reporting obligation: under §5.2's scrub-model paragraph as SPEC-3
replaces its opening sentence, a cleanup-outcome report is filed for a cleanup a `Shutdown`
performs, and a demotion is not a `Shutdown`. What the next attempt gets is rule 4's.

A demotion whose own teardown fails answers an error before it removes anything
(the `DemoteSDK` handler in `pkg/adapter/sdkwarm.go`), and the §5.2 disposition table carries that
row. The row is conditional because the tree is: the same handler releases nothing when
the registry holds no entry. The removal itself is shipped behaviour at those lines, so no code
deliverable changes and this edit closes a spec-surface gap.

### SPEC-1 · spec/29_communication-scenarios.md § 29.4 (session-end step 13)

In §29.4's numbered step 13, append one sentence to the end of the step, after the sentence
ending "([§15.4.3](15_external-api-surface.md#1543-runtime-integration-levels), §28.5.3)."
The sentence to add reads:

```
On a pod serving concurrent sessions this step occurs only under the condition the [§4.7](04_system-components.md#47-runtime-adapter) `Shutdown` row states for the graceful-shutdown signal.
```

Nothing else in the step changes. The condition stays in the §4.7 `Shutdown` row, which owns
the two teardowns and their preconditions, and this step states only whether it occurs, so a
later refinement of the condition lands in one place. Step 13 already carries an inline
exception for the Basic-level and Standard-level integration levels, so a second exception in
the same body follows the step's own form.

Step 12 needs no condition of its own and is untouched. §29.4's `**Preconditions.**` paragraph
scopes the whole trace, the interrupt path and the session-end path alike, to a session that has
completed the §29.2 startup sequence, "so the runtime is running", and the sentence after it
introduces the interrupt path's further requirement as an addition to that base. Every session the trace
carries into step 12 therefore has a start the adapter has admitted, so the step's "the adapter
closes the session runtime" stays true under the narrower runtime-teardown precondition
the §4.7 row states. Step 10's clause that `POST /v1/sessions/{id}/terminate` "is valid in any
non-terminal state" is a restatement of §15.1's endpoint precondition table, cited as such, and it fixes what the endpoint admits rather than what this trace covers, so it does
not widen the trace past its own preconditions. The authority for leaving step 12 alone is §29.4's
own preconditions paragraph.

### SPEC-2 · spec/07_session-lifecycle.md § 7.1 (Normal Flow)

One change at the paragraph beginning "**Atomicity of session creation (steps 2–8).**": a
paragraph inserted after it. The atomicity paragraph itself is not edited, because session
creation leaves no pod-side state for the gateway to reclaim. On a pool serving one session
per pod the finalize block's failure drains the pod, and on a pool serving concurrent
sessions no adapter RPC runs at finalize.

Insert the block below as its own paragraph immediately after that paragraph, which ends
"...its failure mode remains roll-back-without-persist
regardless of the flag." The obligation reaches past session-creation atomicity, binding the
§15.1 start transition onto a pod serving concurrent sessions and the §7.3 re-attach onto a
replacement pod, so it
stands as a paragraph of its own with its own lead-in rather than inside a paragraph whose
heading scopes it to steps 2 through 8. The atomicity paragraph sits inside §7.1's fenced
flow listing, between the step-8 line and the line continuing it, so the new paragraph goes
in the same place, directly below it and before the line resuming `(executionMode,
isolationProfile, scrubPolicy summary)`:

```
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the [§15.1](15_external-api-surface.md#151-rest-api) start transition onto a slot on a pod serving concurrent sessions, whether that slot was reserved at creation or placed by the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot retry policy, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. It sends the reclaim on the connection the failed attempt holds when that connection is still open; the fence does not depend on the connection, and a reclaim sent on a fresh connection is fenced exactly as one sent on the original. The reclaim names the bind attempt it compensates by carrying that attempt's token ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)), and it may name no other. A compensation therefore always names one. The reclaim is answered under the named rules [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states. A reclaim is **acknowledged clean** when the adapter answers it and that answer reports a clean exit, whatever outcome the answer carries, so a reclaim answered `superseded` or `absent` is acknowledged clean. A reclaim the adapter does not answer, and one whose answer does not report a clean exit, are the reclaims not acknowledged clean. The disposition table in [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states what each reclaim reports and what becomes of the slot's occupancy and of its identifier, and the paragraph after the table states what a cleanup that did not complete leaves on the pod and what ends it. Nothing about the attempt's retryability changes. Where this obligation does not reach an attempt the gateway sends no `Shutdown`, and the same paragraph states what becomes of the slot state that attempt left on the pod. This obligation governs the slot state on a pod that outlives the attempt.
```

The paragraph's acknowledged-clean predicate quantifies over every outcome rather than over
`reclaimed` alone. On a conforming adapter the two forms select the same slots, because SPEC-5's
reclaim-outcome rule ties `absent` and `superseded` to a clean exit, so only a response reporting
`reclaimed` can fail the clean-exit test. Quantifying over every outcome fails closed against an
adapter that answers otherwise, and it is what lets CODE-4's disposition read the RPC error and
the clean-exit flag alone rather than branch on the outcome value.

The paragraph states no report, no `leaked` disposition and no hold window. SPEC-3's §5.2
disposition table owns each, and the paragraph cites it.

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
a reclaim the adapter does not acknowledge, and after a start that races it, is the §5.2
disposition table's and this proposal's record of the failure modes it accepts, which cover
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

### SPEC-2 · spec/04_system-components.md § 4.7.9 (Startup Sequence for `type: agent` Runtimes)

Append one sentence after step 5. Step 5 currently reads, verbatim:

```
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials(leases)` → `StartSession`
```

Replace it with:

```
5. Gateway assigns session: `PrepareWorkspace` → `FinalizeWorkspace` → `RunSetup` → `AssignCredentials(leases)` → `StartSession`. A stage that fails takes the failure branch stated in [Section 7.1](07_session-lifecycle.md#71-normal-flow).
```

No other change to §4.7.9. The section stays a `type: agent` orientation list and states no
failure semantics of its own.

### SPEC-3 · spec/05_runtime-registry-and-pool-model.md § 5.2 (scrub model, slot failure and cleanup)

The anchors in §5.2 are the `**Slot cleanup:**` bullet's action list, the `**Scrub model.**`
paragraph, the same bullet's reporting sentence, the same bullet's leaked-outcome sentence, and
the `**Whole-pod replacement trigger:**` bullet's `leaked` parenthetical. Further anchors land in
§4.7 and in §12.6, each staged under its own heading below.

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
lease is gone by the time `cleanupCommands` run. The whole-pod credential purge at §5.2 scrub step 0 is unchanged. It is addressed from disk, so
it reaches the credential file a per-slot cleanup left behind wherever an entry for that slot
survives or not, and §4.9's arming and `AUTH_EXPIRED` firing rules are unchanged.

The second anchor is the `**Scrub model.**` paragraph, which is §5.2's
concurrency-independent statement of the per-slot cleanup and the single home of the
cleanup-outcome reporting rule. It takes a replacement of its opening sentence and an append.
The opening sentence reads, verbatim:

```
**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, reported by the adapter via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)), and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`.
```

Replace it with:

```
**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`. The adapter reports a per-slot cleanup's outcome to the gateway via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)) when, and only when, that cleanup is one a `Shutdown` performs to reclaim a slot that reached `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), because the report advances the pod's served-session count and that count records the sessions that process served.
```

The replacement is the single home of the cleanup-outcome reporting rule, stated as a
biconditional on the predicate the report's own effect fixes: a `Shutdown` that reclaims a slot
that reached `running`. The SDK
demotion the §4.7 `DemoteSDK` row states, the §10.1 hold-timeout termination, and the cleanup
the adapter runs inside a failed start's own handler are all releases outside a `Shutdown`, so
none of them files a report, and the same session is therefore never counted twice against
`recycle.maxSessionsPerPod` when a §5.2 retry or a pod-warm bind sequence re-binds it.

The withdrawn universal, that the adapter reports on every session release, is carried at the
sites in the table below, and the table is the single home of their dispositions: a carrier found
later is added here and nowhere else. A site that states only that the cleanup runs on every
release (spec/06 §6.1, spec/07 §7.1's `scrubPolicy` row, the `SessionScrubOutcome` comments in
the proto and in `pkg/adapter/gatewaycontrol/scrubreport.go`, the residual-state tables of
`execution-modes.md` and `multi-tenancy.md` and the tier-11 assertion over them) stays true and
is not a carrier.

| Carrier | Disposition |
|:--|:--|
| `spec/05_runtime-registry-and-pool-model.md`, §5.2 `**Scrub model.**` opening sentence | Staged here, the scrub-model replacement above |
| `spec/05_runtime-registry-and-pool-model.md`, §5.2 `**Slot cleanup:**` reporting sentence | Staged here, the third anchor below |
| `spec/04_system-components.md`, §4.7 `ReportSessionScrub` row | Staged here, the §4.7 block below |
| `spec/12_storage-architecture.md`, §12.6 prose write and read clauses on `sessions_served` | Staged here, the §12.6 block below |
| `spec/12_storage-architecture.md`, §12.6 DDL comment on `sessions_served` | Staged here, the §12.6 block below |
| `docs/reference/adapter-contract.md`, `ReportSessionScrub` row | Mirrored by DOCS-2 |
| `docs/reference/execution-modes.md`, the per-slot cleanup sentence after the residual-state table | Mirrored by DOCS-4 |
| `docs/operator-guide/security-principles.md`, the per-slot cleanup sentence | Mirrored by DOCS-4 |
| `schemas/lenny-adapter.proto`, `ReportSessionScrub` RPC comment | Mirrored by SCHEMA-1 |
| `schemas/lenny-adapter.proto`, `ReportSessionScrubRequest` message comment | Mirrored by SCHEMA-1 |
| `pkg/proto/adapter/v1/lenny-adapter.pb.go` and `lenny-adapter_grpc.pb.go`, the generated copies of those two comments | Mirrored by SCHEMA-1, through regeneration |
| `pkg/adapter/server.go`, `Server.SessionScrubReporter` field comment | Mirrored by CODE-1 |
| `migrations/0167_runtime_definitions_execution_mode_service.up.sql`, the `sessions_served` column comment | Deferred to the non-spec loop, code-lane comment re-key; the code lane decides whether a landed migration's comment is edited |
| `tests/tier11_docs/spec_28_register_writers_test.go`, `podStateGatewayWrittenSentence` | Deferred to the non-spec loop, code-lane tier-11 re-key in the step that applies SPEC-3 (the §12.6 block below) |
| `tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`, the spec/12 substring block | Deferred to the non-spec loop, code-lane tier-11 deletion in the step that applies SPEC-3 (the §12.6 block below) |
| `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go`, header comment and `// spec:` annotation | Deferred to the non-spec loop, code-lane comment re-key |
| `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`, the `// diagnosis:` comment on `TestPerSlotCleanupStatedOnEverySessionModeRow` | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`, the `ReportSessionScrub` handler comment, the `SessionCountRetirer` comment and the `RecordSessionScrub` inline comment on evaluating the count on every release | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server_test.go`, the three doc comments keyed on every release | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/gateway/session/recycle/scrubreporter_seams.go`, the `sessionCountRetirer` comment | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/adapter/sessionscrubreporter.go`, the `SessionScrubReporter` interface comment | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/adapter/gatewaycontrol/scrubreport.go`, the `Client.ReportSessionScrub` method comment | Deferred to the non-spec loop, code-lane comment re-key |
| `pkg/agentpodstate/agentpodstate.go`, the `SessionsServed` field comment and the `IncrementSessionsServed` doc comment | Deferred to the non-spec loop, code-lane comment re-key |
| `tests/tier4_integration/concurrent_delegation_proxy_test.go`, the two `// spec:` annotations | Deferred to the non-spec loop, code-lane comment re-key |

Then append to the same paragraph. The block carries blank lines, so the table, the paragraph
after it and the `**Slot-identifier reclaim hold.**` paragraph each land as their own block:

```
That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency. It also runs on a slot whose bind is abandoned or fails after the slot enters `receiving_uploads` and before it reaches `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), performed either by a `Shutdown`, the [Section 7.1](07_session-lifecycle.md#71-normal-flow) pod-side reclaim where that obligation reaches the attempt among them, or by a release outside a `Shutdown`, such as the adapter's own handler for a start that fails. A session release produces at most one cleanup-outcome report, filed by the cleanup that reclaims the slot. The table below states the disposition of each per-slot cleanup: what it reports under this paragraph's opening rule, what the `Shutdown` that performs it answers, whether the slot enters the `leaked` sub-state, and when the slot-identifier reclaim hold stated below ends.

| Slot and cleanup | How the cleanup ends | Cleanup-outcome report | Clean-exit flag on the `Shutdown` response | `leaked` sub-state | Slot-identifier reclaim hold |
|:--|:--|:--|:--|:--|:--|
| Slot that reached `running`, reclaimed by a `Shutdown` | Every act returns without error | `released` | Set | Not entered | Ends when the cleanup returns |
| Slot that reached `running`, reclaimed by a `Shutdown` | The runtime close fails | `leaked` | Not set | Entered | Held for the life of the pod |
| Slot that reached `running`, reclaimed by a `Shutdown` | The runtime close succeeds and any other act fails | `released` | Set | Not entered | Held for the life of the pod |
| Pre-`running` slot, reclaimed by a `Shutdown` | Every act returns without error | None | Set | Not entered | Ends when the cleanup returns |
| Pre-`running` slot, reclaimed by a `Shutdown` | An act fails | None | Not set | Entered | Held for the life of the pod |
| Slot of either kind, released outside a `Shutdown`: by the [Section 4.7](04_system-components.md#47-runtime-adapter) SDK demotion, by the [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) hold-timeout termination, or, for a pre-`running` slot, by the adapter's own handler for a start that fails | Every act returns without error | None | No `Shutdown` performs the cleanup | Not entered | Ends when the cleanup returns |
| Slot of either kind, released outside a `Shutdown` by a performer the row above names | An act fails after the deregistration | None | No `Shutdown` performs the cleanup | Not entered, because nothing carries the outcome to the gateway | Held for the life of the pod |
| Slot of either kind that the SDK demotion would release | The demotion's runtime close fails, so the demotion deregisters nothing and runs no cleanup | None | No `Shutdown` performs a cleanup | Not entered | Not opened, because the registry entry stands |
| Pre-`running` slot no cleanup reclaims | No cleanup runs | None | No `Shutdown` performs a cleanup | Not entered | Not opened, because the registry entry stands |

The `leaked` column applies on a pod serving concurrent sessions, and [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) states what a slot in that sub-state holds and counts toward. A pod serving one session has no `leaked` sub-state, and a pre-`running` slot on it counts toward no whole-pod replacement trigger. A slot enters `leaked` on the gateway's reading of the report or of the response, so a [Section 7.1](07_session-lifecycle.md#71-normal-flow) reclaim the adapter does not answer enters it, and its hold ends on the terms of the row the cleanup on the pod met. A cleanup act that fails or does not run leaves on the pod what it would have removed or ended, the acts being those the **Slot cleanup:** bullet below states and the close of the session on the pod's shared runtime process; a cleanup that did not deregister the entry also leaves the registry entry and its armed [Section 4.9](04_system-components.md#49-credential-leasing-service) lease-expiry timers. Pod termination ends all of it, and the whole-pod scrub, on a pod that reaches one under this section's scrub model, ends the slot's workspace tree and credential file. A pod serving one session whose claim the failed bind deletes retires under the [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) occupancy projection.

**Slot-identifier reclaim hold.** The adapter holds a slot's identifier from the deregistration of the slot's registry entry, which opens the hold in the same step under the registry critical section [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states, until the cleanup that reclaims the slot has **completed**, which is when every act that cleanup owes the slot has returned without error. The hold outlasts the deregistration because the cleanup's remaining acts are addressed by the slot identifier rather than by the entry, and every attempt at the same session names the same identifier. While the identifier is held the adapter admits no request that would create or resolve a registry entry under it, and refuses one as a transient condition so the caller retries. The requests that can create one are the requests [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) enumerates as governed by its admission rules. A request that resolves an entry without creating one is refused on the same terms, which is what refuses a [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload still in flight when the cleanup opens the hold. `Shutdown` is the one request outside the hold, governed by the rule stated for it rather than by this one. A refusal is the only record the adapter makes of the hold: no report and no counter names it, and a bind refused this way is accounted by the gateway as an ordinary transient slot failure. The cleanup's close of the session on the pod's shared runtime process is bounded by the graceful window the reclaiming `Shutdown` carries when it carries one, by that request's own deadline when it carries none, and, for the [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) hold-timeout termination, which runs under no request, by a graceful window of ten seconds. A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers when that cleanup completes. The table above states the hold's outcome for each cleanup, and [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token the adapter stamps on a slot's registry entry and the reclaim that names it.
```

The table is the single home of every cleanup's disposition. §6.2, §7.1, the `**Slot cleanup:**`
bullet and the reclaim-hold paragraph cite it and state no cell, so a disposition changes in one
place. Its rows are keyed on what the cleanup reclaims and who performs it, and on which act
fails, because those are what the adapter branches on: the report and the clean-exit flag are
keyed on the runtime close for a slot that reached `running` and on every act for one that did
not, and the hold is keyed on every act on every row. The `leaked` cells restate no §6.2
semantics. The cell for a slot released outside a `Shutdown` records a choice: the shipped
handlers (`releaseSessionSlot` in `pkg/adapter/slotsession.go`, `terminateHeldSession` in
`pkg/adapter/holdstate.go`) discard the cleanup's errors and file nothing, so no `leaked`
outcome reaches the gateway and the table says so. The demotion closes the runtime before it
deregisters, so a close that fails there deregisters nothing and opens no hold, which is the
table's own row; the hold-timeout pass deregisters first.

The scrub-model paragraph's retirement clause rests on SPEC-4's re-keyed projection, under which a
claim deleted while the pod projects `claimed` drains the pod on a pool of either recycle
setting.

The reclaim-hold paragraph is what makes the reclaim exclusive while it runs. The entry
deregistration and the destructive steps are not one act: the directory removals and the
process-group kill each resolve from the slot identifier, which every attempt at the same
session shares, so a successor admitted between the two would be torn down by a reclaim that
had already refused nothing. The paragraph states a terminal for a cleanup that does not
complete, because a successor would otherwise bind over the residue the cleanup left in place,
and §5.2's fresh-workspace guarantee is unconditional. The identifier hold and the `leaked`
occupancy are separate objects, which is why they are separate columns.

The ten-second graceful window the paragraph names is the figure the tree already runs, recorded
here rather than minted. `onHoldTimeout`'s pass-2 close context is
`context.WithTimeout(context.Background(), 10*time.Second)` at `pkg/adapter/holdstate.go:201`,
landed by commit `3997f502b` on 2026-08-22, and CODE-6 keeps the same figure while re-scoping it
from one context shared by the pass to a context per member. The figure bounds one of the three
cases the sentence states, the §10.1 hold-timeout termination, which runs under no request and so
inherits no caller bound; the other two take the reclaiming `Shutdown`'s carried grace or that
request's own deadline. No flag, configuration field or operator-tunable note is staged for it.
The rule in `.claude/rules/code-best-practices.md` that a hard-coded constant carry an override is
conditioned on a default the spec does not fix, and this paragraph fixes it.

The reporting rule and the one-report rule land in the scrub-model paragraph rather than
in the `**Slot cleanup:**` bullet, because that bullet sits under a heading scoped to
`maxConcurrentSessions > 1` while both rules hold on a pod of either concurrency, and because
the scrub-model paragraph is where §5.2 already states the per-slot cleanup uniformly across
session-mode configurations. That same uniformity sentence is what carries the bullet's action
list across the concurrency boundary, so the appended pointer clause discharges the scoping
question for both anchors without stating a second action list. The bullet's own reporting
sentence and its leaked-outcome sentence are the remaining anchors below. Nothing else in the
bullet changes: its trigger, the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)`
formula, and the CRD validation rule all stand as written.

The `leaked` accounting the table names reaches every bind path §7.1 binds: the gateway's
slot-failure accounting covers the retry-placed bind path today, and the code lane extends the
same helper to the create-time reserved path and to the §7.3 re-attach.

The third anchor is the `**Slot cleanup:**` bullet's own reporting sentence, which states the
reporting rule a second time and without qualification. It reads, verbatim:

```
The adapter reports each slot cleanup outcome (`released` or `leaked`) to the gateway via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)).
```

Replace it with:

```
The adapter reports the cleanup's outcome (`released` or `leaked`) on the terms the **Scrub model.** paragraph above states.
```

The sentence becomes a citation rather than a second statement. Leaving it as written would
assert the universal the scrub-model paragraph withdraws, in the same section and a few
paragraphs below it.

The fourth anchor is the same bullet's leaked-outcome sentence, which states the disposition of a
cleanup that fails without qualification. It reads, verbatim:

```
If cleanup fails, the slot is leaked — the pod continues but the slot is not reclaimed until pod termination.
```

Replace it with:

```
The disposition table under the **Scrub model.** paragraph above states which cleanups leave the slot `leaked` and which hold the slot's identifier alone, and the paragraph after the table states what a cleanup that fails or does not run leaves on the pod.
```

The sentence is replaced by a pointer at the table and states no disposition of its own. The
universal it carried is withdrawn rather than qualified: the table gives `Not entered` for a
slot that reached `running` whose runtime close succeeds and whose directory removal fails, and for every
cleanup that fails outside a `Shutdown`, so a sentence saying that a failed cleanup leaks would
contradict the table it cites in the same bullet. Qualifying it instead would leave a second
statement of the `leaked` predicate in the section that holds its home. The `leaked` semantics
§6.2 states are unchanged, and the following sentence's pointer at §6.2 stands.

A further §5.2 anchor is the `**Whole-pod replacement trigger:**` bullet, whose parenthetical
states the predicate for entering `leaked` as a cleanup timeout. The clause reads, verbatim:

```
slots that transition to `leaked` (cleanup timeout exceeded; see **`leaked` slot semantics** in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine))
```

Replace it with:

```
slots that transition to `leaked` (the disposition table above states when a cleanup enters `leaked`; for what such a slot holds, see **`leaked` slot semantics** in [Section 6.2](06_warm-pod-model.md#62-pod-state-machine))
```

The table's `leaked` column states the grounds, and the parenthetical's timeout is none of them,
so it names a cause as though it were the predicate. The clause becomes a pointer at the table
and keeps its §6.2
pointer for what a leaked slot holds. The rest of the bullet, including the threshold formula and
the persistent-count rule, is unchanged.

### SPEC-3 · spec/04_system-components.md § 4.7 (Adapter → Gateway RPC table, `ReportSessionScrub` row)

The anchor is the `ReportSessionScrub` row. The row mirrors the universal
the scrub-model paragraph withdraws, so it states the report's trigger the way §5.2 stated it
before this deliverable. Its opening clause becomes a citation of §5.2 rather than a second
statement of the rule, and the `sessionsServed` clause beside it is re-keyed onto the report, because §12.6's
re-keyed sentences cite §4.7 as the authority for that counter. The row reads, verbatim:

```
| `ReportSessionScrub` | Report the outcome of the per-slot cleanup at a session release (`released` or `leaked`, [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)). The gateway increments `sessionsServed` on the pod's `agent_pod_state` row, and `leaked` outcomes feed the unhealthy-threshold ledger behind the `lenny.dev/drain-request` annotation ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). The request is session-scoped: it is addressed by the identifier of the released session and names no slot. |
```

Replace it with:

```
| `ReportSessionScrub` | Report a per-slot cleanup's outcome (`released` or `leaked`) on the terms [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states. The gateway increments `sessionsServed` on the pod's `agent_pod_state` row on each such report, and `leaked` outcomes feed the unhealthy-threshold ledger behind the `lenny.dev/drain-request` annotation ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)). The request is session-scoped: it is addressed by the identifier of the released session and names no slot. |
```

The row stays one physical line, because the tier-11 gate
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` reads it through
`lineContaining`, and the addressing sentence is unchanged word for word, because the same gate
asserts that the specification row and its reader-facing mirror open the rule identically. The
`leaked` ledger clause and its §4.6.3 pointer are unchanged. No part of the reporting rule is
restated here.

### SPEC-3 · spec/12_storage-architecture.md § 12.6 (`agent_pod_state` table schema)

Phrase replacements on the triggers of the `sessions_served` column, in the prose sentence and
in the DDL comment. §5.2's scrub-model paragraph now conditions the report, and
`sessions_served` is incremented by the report rather than by the release, so a sentence keyed
on the release is false for every release that files none.

The prose sentence reads, verbatim:

```
they are gateway-written recycle counters, incremented at each session release (`ReportSessionScrub`) and on each failed whole-pod scrub (`ReportPodScrub`) respectively
```

Replace it with:

```
they are gateway-written recycle counters, incremented on each cleanup-outcome report (`ReportSessionScrub`) and on each failed whole-pod scrub (`ReportPodScrub`) respectively
```

The read clause of the same sentence reads, verbatim:

```
and `sessions_served` is read by the recycle disposition on a single-session pool and on each session release on a concurrent non-`vm-restart` pool
```

Replace it with:

```
and `sessions_served` is evaluated against `recycle.maxSessionsPerPod` on the terms the **Session count limit:** bullet states
```

The trailing [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)
link after that clause is unchanged and resolves the bullet the replacement names.

The DDL comment on the same column reads, verbatim:

```
    sessions_served     INTEGER,           -- gateway-written at each session release; sessions served over the pod's lifetime; on a single-session pool evaluated against recycle.maxSessionsPerPod at the recycle disposition, on a concurrent non-vm-restart pool evaluated on each session release (§5.2)
```

Replace it with:

```
    sessions_served     INTEGER,           -- gateway-written on each cleanup-outcome report; sessions served over the pod's lifetime; evaluated against recycle.maxSessionsPerPod on the terms §5.2 states
```

The write trigger moves onto the report because that is where the gateway performs the
increment: `ScrubReporter.RecordSessionScrub`
(`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`) increments
`sessions_served` on the report, so a release that files none performs no increment, and a
sentence keyed on the release is false for it. The evaluation point is a separate rule whose
home is the **Session count limit:** bullet of §5.2, so both read clauses cite that bullet in
place of restating it, and §12.6 keeps the write trigger it owns and states neither an evaluation
point nor a reporting rule of its own. The implementor runs `grep -rn sessions_served tests/`,
and the step that applies SPEC-3 re-keys or deletes every tier-11 assertion that grep finds
pinning a §12.6 `sessions_served` sentence: an assertion on the write clause is re-keyed onto the
replacement, and an assertion on the read clause is deleted, because §12.6 no longer states an
evaluation point for it to compare.

### SPEC-4 · the occupancy projection's claim-deletion statements (spec/06_warm-pod-model.md § 6.2, and spec/04_system-components.md § 4.6.1 in the sub-section below)

§4.6.1's **Occupancy projection** bullet list owns the projection and is the single statement of
its claim-existence half. §6.2 carries two further statements of that half, the trigger list of
the `claimed ──→ draining` entry in its fenced `Occupancy projection` block and the
claim-existence clauses of the projection prose above it, and both are reduced to a pointer at
§4.6.1 here rather than re-keyed alongside it. §6.2's own `sdk_connecting ──→ failed` entry is
the precedent for a section pointer inside a fence of this section, and this deliverable applies
the same reduction to the two `slot_cleanup` entries below.

The statements as they stand are keyed on the pool's recycle setting or on its retirement
limits, and the projection reads neither. `ProjectOccupancyPhase` in
`pkg/controller/warmpool/occupancy.go` switches on the pod's current phase alone: `state.Reserved` with no claim projects `Idle` and
`state.Claimed` with no claim projects `Draining`, and the function's own comment states the
rule the edits below adopt, that "the §6.2 state machine encodes the recycle-versus-one-session
distinction in the phase the pod sits in at the claim DELETE". A pod its pool reuses returns to inventory
through the `reserved → idle` edge, which it reaches only after its claim has been patched
through `recycling` and its whole-pod scrub has been reported. §4.6.1's bullets are re-keyed on
the projected phase at the claim DELETE, which is what the retirement clause of SPEC-3's §5.2
scrub-model paragraph relies on for a pod serving one session whose claim a failed bind deletes.

In the fenced `Occupancy projection` block, replace the trigger list of the `claimed ──→
draining` entry:

```
  claimed ──→ draining              (terminal claim disposition released or failed; claim
                                     deletion — see §4.6.1; or gateway-stamped
                                     lenny.dev/drain-request annotation — unhealthy threshold)
```

In the projection prose, the claim-existence clauses are removed and one pointer sentence takes
their place. The clause reading "a pod with no claim projects `idle`; " is deleted, the clause
reading "a claim deleted on a recycling pod under its limits projects `idle`; " is deleted, and
the phrase reading ", or a claim deleted on a pod with `recycle.enabled: false`," is deleted from
the final clause, which then reads "and a terminal claim disposition (`released` or `failed`)
projects `draining` and then `terminated`". Immediately after that sentence's terminating period,
insert:

```
A claim's deletion, or its absence, projects as [Section 4.6.1](04_system-components.md#461-warm-pool-controller-pod-lifecycle) states.
```

The sentence's remaining clauses, including its opening input enumeration and its `standard`,
`in-place` and `vm-restart` recycling clauses, are unchanged, and no input is added to the
enumeration: no surviving §6.2 clause reads the phase the pod currently projects. The three
clauses go together because the claim-existence half of the projection is a partition and §4.6.1
states it whole. Reducing the two claim-deletion clauses alone would leave §6.2's unqualified
no-claim clause answering `idle` for a pod whose claim is deleted while it projects `claimed`,
which is the input a failed bind produces on a `maxConcurrentSessions: 1` pool with
`recycle.enabled: true`, where §4.6.1 answers `draining`. The fence's own `reserved ──→ idle`
entry states an edge rather than the claim-deletion rule and is untouched.

The `Recycle edges` group is untouched. Its `a failed session` trigger is correct for its own
group, whose preamble requires the claim to be patched `bound → recycling` at the recycle
boundary before any edge in the group is evaluated, and the pre-attached bind failure never
reaches that boundary.

### SPEC-4 · spec/04_system-components.md § 4.6.1 (occupancy projection, the claim-deletion bullets)

§4.6.1's two claim-deletion bullets are keyed on the pool's recycle setting and on its
retirement limits, and its `draining`, then `terminated` bullet closes with a sentence
that is false against the controller: `ProjectOccupancyPhase` never returns `idle` from
`claimed` on any pool (`pkg/controller/warmpool/occupancy.go`). Both bullets are re-keyed on the projected
phase and the false sentence is deleted rather than reworded, because the replacement trigger
clause already states where a pod its pool reuses returns to inventory.

The bullet list's opening sentence enumerates the projection's inputs and does not name the
phase the pod currently projects, which the re-keyed bullets below read. In that sentence,
replace "a level-triggered projection of claim existence, claim binding state ([Section
4.6.3](#463-crd-field-ownership-and-write-boundaries)), and the pool's `sessionPolicy`" with:

```
a level-triggered projection of claim existence, claim binding state ([Section 4.6.3](#463-crd-field-ownership-and-write-boundaries)), the pool's `sessionPolicy`, and the phase the pod currently projects (the controller's own last level, which it may read back because it is the sole writer of that field)
```

The rest of that sentence is unchanged.

Replace the bullet beginning "`idle` when the claim is deleted on a recycling pod under its
limits (hold expiry)":

```
- `idle` when the claim is deleted while the pod projects `reserved` (hold expiry). The scrub and any re-warm completed before the claim entered `reserved`, so this edge is a pure claim-deletion projection with no second re-warm.
```

Replace the bullet beginning "`draining`, then `terminated`, when the claim records a terminal
disposition", whose closing sentence ("The one-session-only invariant of §6.2 is the
`recycle.enabled: false` configuration: the projection returns a pod from `claimed` to `idle`
only on a recycling pool.") is deleted with it:

```
- `draining`, then `terminated`, when the claim records a terminal disposition (`released` or `failed`) or is deleted while the pod projects `claimed`, on a pool of either recycle setting, because such a pod is unscrubbed; a pod its pool reuses returns to inventory through the `reserved → idle` edge above instead.
```

The rest of the bullet list, including the `reserved` hold bullet, the `sdk_connecting` re-warm
bullet and the `vm-restart` carve-out, is untouched.

### SPEC-4 · spec/06_warm-pod-model.md § 6.2 (per-slot sub-state fence)

In the fenced state-machine block, under the heading line `Per-slot sub-states (tracked per
session, not as pod-level phase; a pod of either concurrency):`, insert one edge immediately
after the `receiving_uploads ──→ running` entry:

```
  receiving_uploads ──→ slot_cleanup    (see the pre-`running` slot cleanup paragraph below)
```

In the same fenced block, the `receiving_uploads ──→ running` entry reads, verbatim:

```
  receiving_uploads ──→ running         (workspace ready, session dispatched to runtime with its
                                         session identifier)
```

Replace it with:

```
  receiving_uploads ──→ running         (see the pre-`running` slot cleanup paragraph below)
```

In the same fenced block, the `slot_cleanup ──→ released` entry reads, verbatim:

```
  slot_cleanup ──→ released             (slot workspace removed, processes killed, slot released)
```

Replace it with:

```
  slot_cleanup ──→ released             (see §5.2)
```

In the concurrent-occupancy block above, the `slot_cleanup ──→ leaked` entry reads, verbatim:

```
    slot_cleanup ──→ leaked               (cleanup timeout exceeded — slot not reclaimed until pod termination)
```

Replace it with:

```
    slot_cleanup ──→ leaked               (see §5.2)
```

The entry stays in the concurrent-occupancy block where it sits.

Each entry carries a pointer and states no trigger. The §5.2 disposition table gives the cleanups
that traverse either edge different reports, and the grounds it states for entering `leaked` do
not include the timeout the entry named. A trigger short enough for a fence entry therefore either
restates §5.2 or excludes one of those traversals, so §5.2 stays the single home of both
predicates and each fence entry carries its edge alone. §6.2's own warm-fill
`sdk_connecting ──→ failed`
entry is the precedent for a section pointer inside a fence of this section; it pairs its
trigger with a `see §6.1` pointer, and the trigger half is dropped here for the reason above.
The `**`leaked` slot semantics.**` paragraph below the fence is untouched: it states what a slot
in that sub-state holds and counts toward, and states no trigger.

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
**Pre-`running` slot cleanup.** A slot reaches `running` when the adapter has recorded the pod's shared runtime process as holding the session. Every earlier stage of the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence, credential assignment included, and a start still in flight leave the slot in `receiving_uploads`. A bind abandoned or failed at one of those stages takes the `receiving_uploads → slot_cleanup` edge when a cleanup runs on the slot; a slot that has reached `running` takes the `running → slot_cleanup` edge the fence above carries. [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states who performs the cleanup either edge runs, the acts it performs, the disposition of each cleanup and of a pre-`running` slot no cleanup reclaims, and the reclaim hold that refuses a bind onto the slot's identifier. The `slot_cleanup` sub-state is tracked per session and carries no admission rule of its own.
```

The paragraph states the `running` boundary and the two edges into `slot_cleanup`, and nothing
else. Its closing sentences point a reader of the fence at §5.2, so `slot_cleanup` is not read as
a label on a slot anything else may bind.

### SPEC-5 · spec/04_system-components.md § 4.7.1 (after the Gateway → Adapter and Adapter → Gateway RPC tables)

Insert the block below immediately after the `*Adapter → Gateway RPCs:*` table closes and
before the `#### 4.7.2 Checkpoint and Interrupt Mutual Exclusion` heading. The block states
the bind attempt token once, on the section that owns the gateway-adapter RPC contract, so the
§4.7 `Shutdown` row and §7.1 each point at one statement rather than carrying two. §4.1 states
nothing about either new field, for the reason the SPEC-1 commentary above gives:

```
**Bind attempt token.** A **bind attempt** is one gateway attempt to bind a session onto a pod, running from that attempt's first pod-side RPC for the session until the attempt succeeds or is abandoned. The gateway mints a **bind attempt token** for each attempt: one opaque string value, drawn from a cryptographically secure random source, minted once, before the attempt issues its first pod-side RPC. The adapter compares the token for equality and does nothing else with it. It parses no structure out of it, derives no order and no age from it, and never mints one of its own. The empty string is not a token, so a request whose `bind_attempt` is empty names no attempt. A token belongs to an attempt rather than to a session, so two attempts at one session hold different tokens, and it is never derived from the session identifier, from the slot identifier, or from a `coordination_generation`. It is not a coordination generation and carries none of that field's semantics. The two answer different questions: the generation names the gateway replica that speaks for the session ([Section 10.1](10_gateway-internals.md#101-horizontal-scaling)) and is validated on the RPCs that carry it, while the token names the bind attempt a registry entry belongs to. Where both appear on one message, as on `Shutdown`, each is checked on its own terms.

The table below states which requests carry `bind_attempt` and which carry the `mid_session` marker. Where that table leaves `StartSession` and `ConfigureWorkspace` outside the comparison, the reason is that a start may be issued by a later stage of the same binding than the stage that created the entry, so comparing a token on either of them would refuse a start against an entry the same binding legitimately created. No token comparison runs against either of them, and the rules below reach them by their own conditions. No response reports a token. Nothing a caller must hold travels on a response, so a caller that received no response at all still holds the token it minted, and a caller on a fresh connection holds it exactly as the caller on the original connection did.

Which fields each request carries, where a request on this contract that the table does not list carries neither field:

| Request | `bind_attempt` | `mid_session` |
|:--|:--|:--|
| `PrepareWorkspace` | non-empty when the request is not marked `mid_session`, empty when it is | carried |
| `FinalizeWorkspace` | non-empty when the request is not marked `mid_session`, empty when it is | carried |
| `RunSetup` | non-empty | not carried |
| `AssignCredentials` | non-empty | not carried |
| `Resume` | non-empty | not carried |
| `StartSession` | not carried | not carried |
| `ConfigureWorkspace` | not carried | not carried |
| `Shutdown` | pairs with `unconditional_teardown` under rule 10 | not carried |

A [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload is the marked form. It reuses `PrepareWorkspace` and `FinalizeWorkspace` on the entry the session already holds, asserts no attempt identity, cannot create an entry under rule 3, and is exempt from rule 6, because the session it runs against has started by definition. Because it asserts no attempt identity, the gateway issues one only for a session the issuing replica holds a live binding for, and that admission is what keeps a request asserting no identity away from an entry its sender does not own.

**The stamp-once rule.** The adapter stamps once, and rule 4 below is the only rule that writes an entry's `bind_attempt`. The adapter never writes that field again, in either direction: an entry's token does not change while the entry lives, and a request that resolves an entry the adapter already holds writes nothing. The first attempt to create an entry therefore owns it until the entry is removed, and no later request takes that ownership away.

**The registry critical section.** One lock guards the adapter's slot registry, and the adapter performs each of the following as one indivisible step under it. On a request rules 1 through 9 govern, the step is the resolve or the creation of the entry, the stamp on an entry it creates, and the application of rules 2 through 7, the token comparison included. On a request that starts a session, the step is the resolve, the confirmation and the recording of the pod's shared runtime process as holding the session that rule 8 states. On a `Shutdown`, the step is the decision of rules 11 through 14 and the deregistration that decision selects. On every release, a `Shutdown` or otherwise, the step is the deregistration of the entry and the opening of the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot-identifier reclaim hold. Performing the members of one step as separable steps does not conform even when each is correct on its own: an adapter that resolved an entry, released the lock, and then stamped admits a second attempt onto that entry, one that confirmed a start, released the lock, and then recorded it records a session a reclaim has already released, one that deregistered before it opened the hold admits a successor onto an identifier its cleanup is about to destroy, and one that decided a `Shutdown` outside the lock removes and restores an entry it refuses to release. This paragraph is the only statement of that atomicity.

**Admission.** The rules below are numbered and named, and every other statement of this contract refers to a rule by its number and name rather than restating it. Each rule states, or cites, its own condition, on the request's fields, on the registry entry it resolves, and, where the rule names an RPC, on that RPC, and it reaches every request meeting that condition. Rules 1 through 9 govern every request on this contract that can create a slot registry entry: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `Resume`, and `ConfigureWorkspace`. A `Shutdown` is governed by rules 10 through 15 in place of them. Every other RPC on this contract resolves an entry the session already holds, creates none, and is outside these rules, apart from the reclaim hold of rule 2.

The adapter applies rules 1 through 7 as an ordered cascade, stopping at the first rule whose condition the request meets, so a request meeting the condition of more than one rule is governed by the earlier rule. Rule 8 stands outside that order and is applied at the point a request records a started session. Rule 9 states which frame of a client-streaming call the rules above are decided on.

1. **The pairing rule.** Decided on the request's fields alone, before the adapter reads the registry. A request other than a `Shutdown` that carries `bind_attempt`, whose `bind_attempt` is empty and which is not marked `mid_session`, and one marked `mid_session` whose `bind_attempt` is non-empty, are each answered `INVALID_ARGUMENT`, and the adapter creates, resolves, stamps, and changes nothing, even when it holds no entry for that slot. A `Shutdown` is outside this rule, because rule 10 pairs its fields instead.
2. **The reclaim hold.** Applied to a request rule 1 admits. Its scope and the refusal it returns are the ones [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states, and the status that refusal is answered on is the one [Section 15.4](15_external-api-surface.md#154-runtime-adapter-specification) publishes for the slot-identifier reclaim hold. A request it refuses reaches no rule below.
3. **The mid-session-create rule.** A request marked `mid_session` that resolves no entry is answered `FAILED_PRECONDITION` and creates nothing.
4. **The create-and-stamp rule.** A request that is not marked `mid_session` and that resolves no entry creates the entry, stamping its `bind_attempt` on it when it carries one; a request carrying no token creates an entry carrying none. It reaches every request meeting its condition, a `Resume` among them.
5. **The attempt identity rule.** A request whose non-empty `bind_attempt` differs from the non-empty token the resolved entry carries is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED`, answered on `ABORTED` and carried on the adapter's error envelope as `CATEGORY_TRANSIENT`, which a caller retries on. Because this rule is applied before rule 6, such a request is refused here even when the resolved entry's session has already started: a bind whose attempt identity is stale is a transient condition its caller retries, and refusing it as a started session would present a transient condition to the client as a permanent one.
6. **The started-session rule.** A request that is not marked `mid_session` and that resolves an entry whose session has already started is refused with `SLOT_BIND_ALREADY_STARTED`, answered on `FAILED_PRECONDITION` and carried as `CATEGORY_PERMANENT`. A repeat `ConfigureWorkspace` for the session that started on that pod is exempt and is admitted: [Section 4.7](#47-runtime-adapter) publishes that request as idempotent, and it re-points the pre-connected runtime without restarting it.
7. **The admit rule.** Any other request is admitted.
8. **The start-confirmation rule.** A start confirms the entry is still its own. Before the adapter records the pod's shared runtime process as holding a session, it resolves the registry entry for that slot identifier again and confirms that it still holds an entry for that identifier carrying the same bind attempt token the entry carried when the request that starts the session was admitted. These acts are the start step of the registry critical section. The confirmation is required of every request that starts a session, including one that carries no token of its own, for which the token compared is the one the entry carried at admission, so an entry no later attempt replaced compares equal to itself. When the adapter holds no entry for the identifier, or holds one carrying a different token, it records nothing, removes no entry, and reports no cleanup outcome for the slot, because the slot never reached `running` and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) files at most one cleanup-outcome report per session release, from the cleanup that reclaims the slot. It takes the session back off the shared runtime process and refuses the request that started it, answered on `ABORTED`, which is the transient classification a caller retries on. The rule is conditioned on what the request does rather than on the name of the RPC that does it.
9. **The first-frame rule.** `PrepareWorkspace` is client-streaming. The adapter resolves the slot identifier once per call, from the first frame that carries one, and reads `bind_attempt` and `mid_session` from that same frame. Every rule above is decided on that frame's values, rule 1 included, and the adapter reads neither field on any later frame of the call, so a later frame states nothing about the call's admission.

Neither `SLOT_BIND_ATTEMPT_SUPERSEDED` nor `SLOT_BIND_ALREADY_STARTED` appears in the [Section 15.1](15_external-api-surface.md#151-rest-api) REST error catalog, because the gateway consumes both and the client receives the envelope that section already defines for the endpoint that issued the bind sequence.

**`Shutdown` states which teardown it asks for.** Rules 10 through 15 govern a `Shutdown` in place of the admission rules, applied as an ordered cascade on the same terms. Each of rules 11 through 14 fixes the outcome the response reports and whether the request removes an entry; the two teardowns follow from the removal rather than being decided separately. Rule 15 fixes what each reported outcome means.

10. **The teardown-pairing rule.** Decided on the request's fields alone, before the adapter reads the registry. A `Shutdown` carries either a non-empty `bind_attempt`, naming the bind attempt it compensates, or `unconditional_teardown`, and it carries exactly one of the two. A request carrying neither, and a request carrying both, is answered `INVALID_ARGUMENT`: the adapter performs neither teardown, removes no entry, and changes nothing.
11. **The no-entry rule.** A request for a session the adapter holds no entry for removes nothing, performs neither teardown, and answers `absent`. It reaches a request of either form.
12. **The unconditional-teardown rule.** A request asking for the unconditional teardown is addressed to whatever entry the adapter holds. It removes that entry, performs both teardowns under their own preconditions, and answers `reclaimed`. It is what every caller other than a compensating reclaim sends.
13. **The attempt-mismatch rule.** A request naming a bind attempt whose token differs from the token the resolved entry carries, and one whose resolved entry carries no token, removes nothing, performs neither teardown, and answers `superseded`. The entry-carries-no-token arm answers `superseded` so that the rule fails closed. An entry carrying no token is one a `StartSession` or a `ConfigureWorkspace` created, because those two carry none, and such an entry may be the one a successor's own start created for a session now running, which is why a reclaim naming an attempt removes nothing there. No `Shutdown` naming a bind attempt removes such an entry; within this cascade only rule 12 does. That is a residue this contract records rather than one it closes.
14. **The attempt-match rule.** A request naming a bind attempt whose token equals the token the resolved entry carries removes that entry, performs both teardowns under their own preconditions, and answers `reclaimed`.
15. **The reclaim-outcome rule.** `reclaimed`, `superseded`, and `absent` are the outcomes a `Shutdown` response reports, and each has one meaning, fixed by the rule that answers it. `reclaimed`, answered by rule 12 and by rule 14, means the adapter held the entry the request was addressed to and released the slot. `superseded`, answered by rule 13, means the adapter holds an entry for the session that the request is not addressed to, so the request's own bind attempt does not own that entry, either because a different attempt owns it or because the entry carries no token and no attempt owns it, and the reclaim released nothing. `absent`, answered by rule 11, means the adapter holds no entry for the session. All of them are successful outcomes and are answered on a successful RPC: a `Shutdown` is never refused for naming an attempt that does not own the entry. Rule 11 and rule 13 remove no entry and perform neither teardown, so a response reporting `absent` or `superseded` reports a clean exit; only a response reporting `reclaimed` can report an unclean exit, on the terms the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) disposition table states.

Rules 11 through 15 state no refusal, and rule 10's refusal is on the request's own fields rather than onto a slot identifier; the refusal a cleanup in progress imposes is the one rule 2 applies, stated by [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). The two teardowns a removing rule performs, and the precondition each carries, are stated in the [Section 4.7](#47-runtime-adapter) `Shutdown` row.
```

The block sits in §4.7.1 rather than in §4.7.9 because §4.7.9 is a `type: agent`
orientation list and the token binds every session mode the RPC tables serve. The numbered rules
are stated here and nowhere else, and the numbering is the reference every other site uses. The
§4.7 `Shutdown` row, §7.1's reclaim obligation, CONF-1's battery
and the conformance case lists each reach a rule by its number and name rather than restating it,
and §15.4's conformance criterion cites the section whole,
so a refinement to one rule lands once and no second site can drift from this one. §4.7
owns the two teardowns and their preconditions; this block owns what the value is, which requests
carry it, when the adapter stamps it, which entry a request is addressed to, what becomes of that
entry, and what the response reports.

Some of what the block states are obligations this proposal takes on rather than shipped
behaviour it records. The registry critical-section paragraph makes first-writer-wins
implementable from the prose, which it is not while the resolve, the stamp and the comparison
read as three acts, and it is the one place the block states atomicity, so a step missing from
the adapter's critical section is a step missing from that paragraph. The start
confirmation is the second: the shipped adapter records the session unconditionally, so the rule
is new behaviour rather than a restatement; rule 8 states the status its failure is answered on
and §15.4 publishes non-conformance against the rule, so an adapter written from the published
contract performs it. And the
sentence on the gateway's own admission of a mid-session upload is the whole safety argument for
a request that asserts no attempt identity, so the implementation confirms that the shipped
mid-session admission guard reads what it appears to read before the mid-session conditioning
lands, and records what it found.

### SPEC-5 · spec/15_external-api-surface.md § 15.4 (after the SDK-warm demotion contract)

Insert the two blocks below immediately after the paragraph beginning `**SDK-warm demotion
contract:**` and before the `#### 15.4.1 Message Format and Binary I/O Requirements`
heading. §15.4 is where the adapter contract is published to third-party adapter authors. It
states no rule of its own. It points at §4.7.1 for the rules and states what conformance against
them means, which is what a published contract adds to the rule text. The criterion quantifies
over what §4.7.1 states for each request, so neither block states what the rules govern:

```
**Bind attempt token contract:** [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token, the requests that carry it, the stamp-once rule, the registry critical section, and the numbered rules an adapter applies, and that statement is normative for a third-party adapter. An adapter conforms when, on every request, it refuses or admits, performs or withholds the acts, and answers as that section states for that request, and an adapter that behaves otherwise on any request does not conform.
```

```
**Slot-identifier reclaim hold:** [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold, its window, and the requests it refuses. This block states what an adapter must exhibit on the wire. While the identifier is held, a request the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) hold refuses is refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on. An adapter that admits a request that hold refuses does not conform. An adapter that refuses one with a permanent status, or that answers a status the caller cannot retry, does not conform. `Shutdown` is outside this hold, on the terms [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states, and is answered under the rules [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states for it.
```

Both blocks state the contract and name no Go type, field number or package. The wire form is
`schemas/lenny-adapter.proto`, which §15.4 already names as the published artifact and which
SCHEMA-1 edits; no response reports a token. The project has no harness that can run a
third-party adapter, so the enforcement is CONF-1's tier-3 suite over a real gRPC channel, with
the tier-10 battery in process, and the absent harness is recorded as a §28.4 claim-register row
with status `ABSENT`, following the rows the `coordination_generation` fence already carries.

### SPEC-5 · spec/15_external-api-surface.md § 15.1 (REST error catalog, `SETUP_COMMAND_FAILED` row)

The started-session refusal is a second producer of a deterministic `FailedPrecondition` failure
in the setup window, and the gateway's envelope selection is unchanged by this proposal, so the
refusal reaches this row only where it arrives at the setup-command request, which is the stage
whose deterministic `FailedPrecondition` failure §15.1 already maps to this code. A refusal
arriving at any other bind-sequence request reaches the client under the envelope that stage
already selects. The row's cause, retryability and setup-output sentences state a cause the
refusal does not have, and its exclusion sentence leaves the refusal's own envelope unstated. In the `SETUP_COMMAND_FAILED` row of the
§15.1 REST error catalog table, replace the opening sentence, which reads, verbatim:

```
A session setup command exited non-zero (or hit its hard timeout), which the adapter reports as a deterministic `FailedPrecondition` failure.
```

with:

```
The adapter answered the setup-command request with a deterministic `FailedPrecondition` failure: either a session setup command exited non-zero or hit its hard timeout, or the request was refused because the registry entry it resolved carried a session that had already started ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)).
```

In the same row, replace the retryability sentence, which reads, verbatim:

```
Deterministic and not retryable without changing the workspace plan or setup script, consistent with `setup_command_failed` under `retryPolicy.nonRetryableFailures` in [Section 7.3](07_session-lifecycle.md#73-retry-and-resume).
```

with:

```
Deterministic and not retryable, consistent with `setup_command_failed` under `retryPolicy.nonRetryableFailures` in [Section 7.3](07_session-lifecycle.md#73-retry-and-resume): the setup-command cause fails identically until the workspace plan or setup script changes, and the refused setup-command request means another start already holds the slot identifier.
```

In the same row, replace the closing sentence, which reads, verbatim:

```
`details.reason` is `setup_command_failed`; the per-command stdout and stderr are retrievable via `GET /v1/sessions/{id}/setup-output`.
```

with:

```
`details.reason` is `setup_command_failed`. Where a setup command ran, its per-command stdout and stderr are retrievable via `GET /v1/sessions/{id}/setup-output`; a refused setup-command request runs no setup command and produces no such output.
```

In the same row, replace the exclusion sentence, which reads, verbatim:

```
Any other setup-window failure (every gRPC code other than `FailedPrecondition`, including a crashed pod surfaced as `Unavailable` or `DeadlineExceeded` and a wrapped cause reported as `Unknown`) is not this code; it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback and is recovered with a fresh pod per [Section 6.2](06_warm-pod-model.md#62-pod-state-machine).
```

with:

```
Any other failure of the setup-command request (every failure of that request other than a deterministic `FailedPrecondition`, including a crashed pod surfaced as `Unavailable` or `DeadlineExceeded`, a wrapped cause reported as `Unknown`, and a superseded bind answered as `Aborted`) is not this code; it stays the retryable `SESSION_CREATION_FAILED`/`STARTING_FAILED`/`RESUME_FAILED` fallback and is recovered with a fresh pod per [Section 6.2](06_warm-pod-model.md#62-pod-state-machine). A deterministic `FailedPrecondition` the adapter answers to any other bind-sequence request is not this code either, and reaches the client under the envelope that stage selects.
```

The exclusion is re-keyed the same way as the cause sentence above it, on the request the
adapter answered as well as on the gRPC code, because the started-session refusal is a second
deterministic `FailedPrecondition` producer at the setup-command request. Keyed on the code
alone, the exclusion would sweep a `FailedPrecondition` the adapter answers to a bind-sequence
request other than the setup-command request into the retryable fallback, which is not the
envelope that stage selects; which envelope each stage's refusal reaches is stated in the
Edge-cases bullet `**A bind-sequence refusal reaches the client under the envelope its stage
already selects.**` `details.reason` keeps its one value, so no client contract changes.

### SPEC-5 · spec/06_warm-pod-model.md § 6.2 (pre-attached retry policy, client visibility)

The `**Client visibility:**` bullet under the pre-attached retry policy restates the
`SETUP_COMMAND_FAILED` mapping for the setup-command request at `POST /v1/sessions/{id}/start`,
which the §15.1 row above owns, and it already cites §15.1 immediately before restating it. That
half of the clause is replaced by a pointer at the row rather than re-keyed. The clause's other
half, which sends any other setup-window failure to the retryable `STARTING_FAILED` fallback, is
dropped: the bullet's own preceding clause already states that `/start` surfaces a runtime-launch
failure as `STARTING_FAILED`, and the §15.1 `STARTING_FAILED` row states the mapping. Neither
mapping is then stated twice. Replace the clause, which reads, verbatim:

```
a deterministic non-zero setup-command exit at `/start` surfaces as the non-retryable `SETUP_COMMAND_FAILED` ([§15.1](15_external-api-surface.md#151-rest-api)) while any other setup-window failure stays the retryable `STARTING_FAILED` fallback
```

with:

```
a failure of the setup-command request at `/start` takes the envelope the `SETUP_COMMAND_FAILED` row of [§15.1](15_external-api-surface.md#151-rest-api) states
```

The clause then carries the boundary condition §6.2 alone knows, that the concurrent-workspace
pool materializes its reserved slot and runs setup at start, and states no mapping of its own.
The rest of the bullet stands as it is, including its finalize, create, `resume_pending` and
retry-budget sentences.

### SPEC-6 · spec/16_observability.md § 16.1 (metric catalog)

Add a row to the §16.1 metric catalog table for each counter below, beside the session-slot
failure count. The superseded series is emitted from the compensation's caller, which holds the
pool and the pod name. The adapter series carries the adapter metrics endpoint's existing
deferral (the adapter process exposes no scrape target today), so its row records the series for
the register and names the deferral.

```
| Slot compensation superseded (`lenny_slot_compensation_superseded_total`, labeled by `pool`, `k8s_pod_name` — a compensating `Shutdown` answered `superseded`: the adapter holds an entry for the session that the compensation is not addressed to, so the reclaim released nothing and the slot is not leaked. See [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract).) | Counter |
| Slot shutdown met an untokened entry (`lenny_slot_shutdown_untokened_entry_total`, labeled by `k8s_pod_name` — a `Shutdown` carrying an attempt token met a registry entry that carries none, which counts the reclaims that met an entry no attempt owns: an entry a start created and a non-conforming adapter's entry alike. Adapter-side; not scraped until the adapter metrics endpoint is wired) | Counter |
```

## Spec sections deliberately untouched

Listed so a reviewer can tell scope from oversight.

- **§15.4.6's conformance categories.** They exercise the runtime binary over JSONL against a
  fake adapter, so they have no adapter under test and cannot observe an adapter obligation.
  CONF-1 lands in the tier-10 conformance battery instead, and the wire-level rules land in
  the tier-3 contract suite.
- **§5.2's non-retryable failure categories.** The list is scoped to
  `maxConcurrentSessions > 1` and the reclaim hold is not, and the refusal the hold returns
  is transient, so no category is added or removed.
- **§10.1's coordinator handoff.** The bind attempt token is not a coordination generation and
  changes nothing about the handoff.
- **§28's registers.** `Shutdown` appears in no §28 register row, and §28.5.1 is organised
  per channel rather than per field, so a new field on an existing message adds no row.
- **§15.1's error catalog takes no new row.** It takes none for
  `SLOT_BIND_ATTEMPT_SUPERSEDED` and none for `SLOT_BIND_ALREADY_STARTED`. §15.1 catalogs the
  `code` field of the REST error envelope, and the gateway consumes both refusals as
  adapter-contract codes rather than rendering either into that envelope, so a client receives the
  envelope §15.1 already defines for the endpoint that issued the bind sequence.
  `PROTOCOL_VERSION_INCOMPATIBLE` is the shipped precedent: it is an adapter `ErrorCode` the
  specification publishes to adapter authors with no §15.1 row
  (the `INIT` row of §15.4.2's RPC lifecycle state table, and the `ErrorCode` enum in
  `schemas/lenny-adapter.proto`). What is deliberate
  here is the absence of a new row. The
  section itself is edited under SPEC-5 rather than untouched: the started-session refusal is a
  second deterministic `FAILED_PRECONDITION` producer in the setup window and the existing
  `SETUP_COMMAND_FAILED` row states its cause as the setup command alone, so SPEC-5 widens that
  row's cause, retryability and setup-output sentences.

## Spec files touched

- `spec/04_system-components.md`: §4.1 Request Message Scope (one sentence replaced), §4.6.1's
  **Occupancy projection** bullet list (the opening sentence's input enumeration gains the phase
  the pod currently projects, the two claim-deletion bullets replaced, one false
  closing sentence deleted), §4.7
  Gateway → Adapter RPC table `Shutdown` row (first sentence replaced, the two teardowns with
  their preconditions and a pointer at §4.7.1's numbered rules added), §4.7 Gateway → Adapter RPC
  table `DemoteSDK` row (opening clause replaced, the registry removal stated as a slot release
  whose §5.2 cleanup runs inside the request), §4.7 Adapter → Gateway RPC table
  `ReportSessionScrub` row (the opening clause re-keyed to a citation of §5.2, the
  `sessionsServed` clause re-keyed onto the report), §4.7.1 (the bind attempt
  token block, new, after the RPC tables, stating the admission cascade and the teardown cascade
  as one numbered rule list, with the registry critical-section paragraph as the one statement
  of the adapter's atomicity), §4.7.9 step 5 (one sentence).
- `spec/05_runtime-registry-and-pool-model.md`: §5.2 `**Scrub model.**` paragraph (its opening
  sentence replaced so the cleanup-outcome report is stated once, as a biconditional on a
  `Shutdown` that reclaims a slot that reached `running`, and the per-slot
  cleanup pointer, the one-report rule, the cleanup disposition table and the slot-identifier
  reclaim-hold paragraph appended, whose closing sentence points at the bind attempt token), and
  the §5.2 `**Slot cleanup:**` bullet's action-list sentence, its reporting sentence (which
  becomes a citation of the scrub-model paragraph) and its leaked-outcome sentence (all
  replaced), and the §5.2 `**Whole-pod replacement trigger:**` bullet's `leaked` parenthetical
  (replaced with a pointer at the disposition table).
- `spec/12_storage-architecture.md`: §12.6's `agent_pod_state` table schema, the `sessions_served`
  column's write trigger in the prose sentence and in the DDL comment (both re-keyed on the
  cleanup-outcome report, because that is where the gateway increments), and the read clause of
  each (replaced with a citation of §5.2's **Session count limit:** bullet, which owns the
  evaluation point).
- `spec/06_warm-pod-model.md`: §6.2's occupancy projection (the `claimed ──→ draining`
  trigger list in the fence and the claim-existence clauses of the projection prose, each
  reduced to a pointer at §4.6.1, which owns the projection; no input is added to that
  sentence's enumeration), §6.2 per-slot sub-state fence (one edge added carrying a pointer
  at the pre-`running` slot cleanup paragraph, the `receiving_uploads ──→ running` annotation
  replaced with that same pointer, and the `slot_cleanup ──→ released` and
  `slot_cleanup ──→ leaked`
  annotations each replaced with a pointer at §5.2), the prose after
  it (one paragraph, stating the `running` boundary and pointing at §5.2), the §6.2
  `resuming` mid-resume cancel bullet (one clause), and the §6.2 pre-attached retry policy's
  `**Client visibility:**` bullet (one restating clause replaced with a pointer at §15.1's
  `SETUP_COMMAND_FAILED` row).
- `spec/07_session-lifecycle.md`: §7.1 gains a new paragraph after the atomicity paragraph
  carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/15_external-api-surface.md`: §15.4 (the published bind attempt token contract, new,
  after the SDK-warm demotion contract: a pointer at §4.7.1 and the conformance criterion, with
  the slot-identifier reclaim-hold contract beside it), and §15.1's `SETUP_COMMAND_FAILED` row (the cause sentence, the retryability
  sentence, the setup-output sentence and the exclusion sentence replaced).
- `spec/16_observability.md`: §16.1's metric catalog (one row for each counter
  the compensation's caller and the adapter's fail-closed row emit).
- `spec/29_communication-scenarios.md`: §29.4 session-end step 13 (one sentence appended).

Each reader-facing reference page that mirrors these sections moves with the section it mirrors.
The edits the non-spec deliverables must carry are these. `docs/reference/metrics.md` gains the
row for each counter, matching the §16.1 rows, so the catalog a reader consults and the catalog
the specification states stay one list (CODE-9). `docs/reference/state-machines.md` gains the
per-slot sub-state row matching the §6.2 edge, takes its own trigger replacement for the
`receiving_uploads` → `running` row, for the `slot_cleanup` → `released` row and for the page's
`slot_cleanup -> leaked` clause, each in the page's own voice
because the page cannot carry the pointers the §6.2 annotations now carry, and its pod
state machine paragraph states in the page's own voice what §4.6.1's re-keyed claim-deletion
bullets state, each clause keyed on the phase the pod projects at the claim DELETE, with the
same projection input added to that paragraph's own enumeration (DOCS-1). `schemas/lenny-adapter.proto` takes
the `ReportSessionScrub` comment replacements, each naming the outcome the adapter reports
and citing §5.2 for its terms and for the cleanups the adapter reports, so the wire contract a
third-party implementor reads stops asserting what `released` implies and stops asserting a
report on every session release (SCHEMA-1). `docs/reference/adapter-contract.md`
takes the rewritten `Shutdown` row, the amended `DemoteSDK` row, the re-keyed
`ReportSessionScrub` row and the added bind-attempt paragraph (DOCS-2).
`docs/reference/execution-modes.md` and `docs/operator-guide/security-principles.md` each lose
the reporting clause of their per-slot cleanup sentence (DOCS-4).
`docs/reference/error-catalog.md` takes a sentence replacement mirroring each of SPEC-5's §15.1
replacements, the retryable-fallback sentence among them, and a replaced remedy cell, all in the
`SETUP_COMMAND_FAILED` row it already carries rather than in a new row, each stated in the
page's own voice because its reader is a REST client with neither the gRPC codes nor the
specification (DOCS-3).