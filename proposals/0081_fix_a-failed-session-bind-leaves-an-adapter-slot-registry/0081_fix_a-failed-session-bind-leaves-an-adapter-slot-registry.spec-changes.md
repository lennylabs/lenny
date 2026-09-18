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
entry. §4.7.1's no-entry rule answers `absent` for a session the adapter holds no entry
for, removing nothing and running neither teardown, and its reclaim-outcome rule makes that
answer a clean-exit response. Those two rules are what make the compensation safe to send at
a stage where the adapter may hold nothing.

**The gateway owes a pod-side reclaim on a failed bind.** §7.1 already states the atomicity of
session creation and already routes a finalize-block failure to §6.2's pre-attached disposition,
which governs the pod. It does not state what happens to the state the failed attempt created on
the pod. The obligation binds a failed bind attempt on a pod that is released or reused rather
than terminated: the bind sequence run at the §15.1 start transition onto a slot on a pod
serving concurrent sessions, whether that slot was reserved at creation or placed by §5.2's slot
retry policy, and the §7.3 re-attach onto a replacement pod. Each issues the same pod-side RPCs
and each leaves the same residue behind, so the rule is stated as its own §7.1 paragraph rather
than inside the atomicity paragraph. That paragraph is scoped by its own heading to steps 2
through 8 and closes by stating that the client never receives a `session_id` for a session that
failed to fully initialize, and neither holds for a re-attach of a session whose row is already
persisted. §7.3's resume flow and §6.2's mid-resume cancel edge point at the new paragraph and
restate nothing. The obligation begins with an attempt's first pod-side RPC and ends when that
attempt succeeds: the gateway sends `Shutdown` for the session on the connection the failed
stage still holds, before that connection closes, and releases the slot reservation afterwards.
It sends the reclaim even when the failing RPC's own context is already cancelled or past its
deadline, because that is the case in which the adapter may have started the session. A reclaim
the adapter does not answer, and one whose answer does not report a clean exit, whatever outcome
that answer carries, are the reclaims that did not complete. On a pod serving concurrent
sessions a reclaim that did not complete leaves the slot `leaked` under §6.2's existing
disposition, which holds the slot's occupancy and counts it toward the §5.2 whole-pod
replacement trigger. A bind attempt outside the obligation fails on a pod serving one session.
The paragraph sends no `Shutdown` there and states no disposition of its own, because §6.2
already decides it: the pre-attached failure deletes the pod's claim while the pod projects
`claimed`, and §6.2 retires a pod that projects `claimed` when its claim is deleted, on a
pool of either recycle setting, because such a pod is unscrubbed and the pod's
return-to-inventory path is the `reserved → idle` edge. SPEC-4 states that once, in §6.2, and this paragraph and
SPEC-3's §5.2 append each cite it. The `leaked` sub-state and the whole-pod replacement trigger
are how a reclaim that did not complete is accounted on a pod serving concurrent sessions. They
are not why a one-session pod's residue ends, and stating them as the reason is what made three
sections answer the same question in three voices. The slot identifier is the session
identifier, and §5.2 states that the adapter holds that identifier from the deregistration of
its registry entry until the cleanup that reclaims it has finished, so a further attempt at the
same session on that pod meets that hold while the reclaim runs and is refused as a transient
condition. §5.2's slot retry policy is unedited: a retry that meets a surviving entry is refused
by the attempt token, and one that meets a running cleanup is refused by the reclaim hold, and
both refusals are transient. No failure class becomes non-retryable, so §5.2's non-retryable
categories are unchanged, and neither §6.2's pre-attached retry policy nor §15.1's error catalog
changes any failure's retryability; SPEC-5 edits both, only to name the second deterministic
producer in the setup window and to re-key the fallback clause on the gRPC code. The
specification states no deadline for the reclaim; §5.2's per-slot cleanup timeout is the figure
the gateway reuses and the code cites.

**The cleanup-outcome report follows the `running` boundary.** §5.2's scrub model already
states that a per-slot cleanup runs on every session release and is reported via
`ReportSessionScrub`. A cleanup that reclaims a slot whose bind was abandoned or failed after the slot
entered `receiving_uploads` and before it reached `running` is a cleanup on a slot the pod's
shared runtime process was never given, and it reports no outcome, because `ReportSessionScrub` advances the pod's
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
flight. The edge is driven by whichever actor performs that cleanup, the §7.1 reclaim or the
adapter's own failed-start handler, so a bind that fails at the workspace-preparation, setup or
credential-assignment stage on a pod serving one session, which neither §7.1's obligation nor an
adapter handler reclaims, drives neither edge; §6.2 states the disposition of a residue neither
actor reclaims. A session the runtime was already given is a slot in `running`, and it takes the
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
staged §4.7.1 block states this as the stamp-once rule, together with the atomicity the resolve,
the creation, the stamp and the comparisons are performed under, and this prose restates neither
condition. The design choice it encodes is first-writer-wins, and the atomicity is stated with
the rule rather than beside it because without it first-writer-wins is not implementable from
the prose: an adapter that resolved an entry, released its lock, and then stamped would satisfy
each act separately and still admit two attempts onto one entry.

**Admission reads the token and the stage the entry has reached.** The staged §4.7.1 block
states admission as a named, ordered cascade with one table of which request carries which
field. The design choices it encodes are three. The identity refusal is transient and the
started-session refusal is permanent, which is why the attempt identity rule is applied first: a
bind whose attempt identity is stale is retried, and refusing it as a started session would
present a transient condition to the client as a permanent one. A mid-session upload asserts no
attempt identity and never creates an entry, because an entry it created would carry no token,
admit every attempt, match no compensation, and be permanent. Neither refusal takes a §15.1
error catalog row, because the gateway consumes both codes and the client receives the envelope
§15.1 already defines for the endpoint that issued the bind sequence. Nothing here restates a
rule's condition; the staged block is the statement.

**The `Shutdown` says which teardown it is asking for.** The staged §4.7.1 block states teardown
as a named, ordered cascade, from the teardown-pairing rule through the attempt-match rule, and
this prose restates none of their conditions. The one design choice the cascade encodes is the
two-field form: stating the two teardowns as two fields rather than as the presence and the
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
  on an upload-free plan leaves no adapter entry. §4.7.1's no-entry rule answers `absent`
  here and its reclaim-outcome rule makes that a clean-exit response, so the failure is
  accounted transient rather than leaked and adds nothing to the pod's persistent leak
  count. The windowed failure counter still records it, and at `maxConcurrentSessions: 2` a
  single windowed failure already reaches the §5.2 whole-pod replacement threshold.
- **A reclaim the adapter does not acknowledge on a pod serving concurrent sessions.** The
  slot is `leaked`. Its occupancy is held, it counts persistently toward the §5.2 threshold,
  and the pod retires through the shipped trigger. This is §6.2's own disposition applied
  consistently rather than a new rule, and the pod's retirement through that threshold is
  what bounds a best-effort compensation's failure. §5.2 does not steer a retry away from
  the pod holding the leaked slot: the policy places a retry on a new slot on the same pod
  when one is available, where it names the held slot identifier and is refused, on the
  reclaim hold while the cleanup runs and on the attempt token afterwards, and each refusal
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
  client-driven §7.3 resume, and a retry the §5.2 policy places. The slot retry budget defaults
  to one
  retry, so a retry refused on that hold is the request's last attempt under the default and the
  client sees §5.2's exhaustion error. On a create-time-reserved slot the client's own retry of
  the §15.1 start meets the hold the same way, consuming one
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
  attempt is disposed of by `failPhase`, which sends no `Shutdown` and deletes the pod's claim
  while the pod projects `claimed` (`pkg/gateway/podlifecycle/podsession/binder.go:867`, `:998`,
  `:1072-1082`, `:1200-1202`), so §6.2 retires the pod on that deletion, and the residue ends with it
  (`pkg/controller/warmpool/occupancy.go:132-140`). The `leaked` sub-state and the whole-pod
  replacement trigger account for an incomplete reclaim under concurrent occupancy and are not
  the disposition here; SPEC-4 states the disposition in §6.2 and this list does not restate it.
- **A start that races the reclaim.** The orderings differ in what they leave behind. When the
  adapter admitted the start before the reclaim removed the entry, the start finds its entry
  gone at the point it would record the runtime as holding the session, takes the session back
  off the shared runtime process, and refuses; the slot never reached `running` and no cleanup
  outcome is reported. That undo is an obligation SPEC-5 states in §4.7.1 as the
  start-confirmation rule, against which §15.4 publishes non-conformance, so an adapter written
  from the published contract performs it. When the reclaim's answer precedes the start, the
  cleanup has finished and the hold is released, so the start's own claim creates a fresh entry.
  `StartSession` carries no bind attempt token, so the entry it creates carries none and no
  token comparison runs against it: the start is recorded, the slot reaches `running`, and the
  pod is left holding an entry and a runtime session no gateway attempt owns. Nothing refuses
  that ordering, and it is recorded among the accepted failure modes rather than closed. When a
  successor has already taken the identifier, the abandoned attempt's claim meets the
  successor's entry, and the started-session rule is what answers it, because the claim asserts
  no attempt identity: the claim is refused once the successor's session has started, and it
  resolves the successor's entry and starts a session on it while the successor's session has
  not. An entry a token-carrying request created belongs to the attempt that created it until
  the entry is removed, so two such attempts never share one entry. A start asserts no identity
  and is outside that guarantee.
- **A compensation still on the wire when a retry reaches the same slot is fenced.** An
  entry-scoped fence separated a reclaim addressed to a released entry from one addressed to the
  entry that replaced it, and did not separate two attempts sharing one surviving entry: a retry
  that resolved the surviving entry inherited that entry's fence value, so a teardown running
  after the retry had answered its client compared equal and tore down a serving session. The
  attempt token is the per-attempt discriminator that case needed. A retry never inherits the
  surviving entry's token, because the token is minted by the caller per attempt and the adapter
  writes it only on the entry it creates, so a retry that meets a surviving entry is refused and
  the lagging teardown removes only what its own attempt left. The residue is closed for the
  requests that carry a token. An entry a `StartSession` or a `ConfigureWorkspace` created
  carries none, and the bullet below records what stands there.
- **An abandoned attempt's start whose claim runs after the reclaim completed re-creates the
  entry.** The cleanup has finished and the hold is released, so the late `StartSession` claim
  creates a fresh entry and starts the session. `StartSession` carries no bind attempt token, so
  the entry it creates carries none. The pod is left holding an entry and a runtime session no
  gateway attempt owns, which is residue class one reached by a second route, and the entry is
  one no named reclaim can release: a `Shutdown` naming an attempt answers `superseded` against
  an untokened entry and removes nothing, so the entry survives until an unconditional teardown
  or the pod's retirement removes it, and every later bind attempt at that session on that pod
  is refused while it stands. Closing it would need
  the adapter to hold the slot identifier until the gateway's reclaim has been answered, which
  keeps an identifier held across a network round trip on every failed bind and refuses the
  client's own retry for that whole window, or the narrower rule that bars a start from creating
  an entry at all. Both are staged at position 2 of the gateway-runtime-comms remediation plan
  rather than here. The ordering needs the abandoned attempt's
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
  ran a command. It makes one replacement §15.1 does not need,
  because the page states the retryable-fallback exclusion keyed on the cause class where §15.1
  keys it on the gRPC code.

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

Replace the `Shutdown` row's first sentence. The row currently
opens, verbatim:

```
| `Shutdown` | Graceful end-of-session teardown of the named session: the adapter closes that session's runtime and releases the session's slot. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

Replace that opening with the text below, leaving the remainder of the row (from "On the
default disposition the pod is replaced." to the end) unchanged:

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the moment the session reaches the runtime, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. Every request states which teardown it asks for, by carrying either a non-empty `bind_attempt` ([Section 4.7.1](#471-role-and-gateway-rpc-contract)) or `unconditional_teardown`, and the response reports which entry the request was addressed to and what became of it. [Section 4.7.1](#471-role-and-gateway-rpc-contract) states the named rules that decide both, and states what each of `reclaimed`, `superseded`, and `absent` means; they are stated only there. A superseded reclaim is neither a failed reclaim nor a leaked slot. The request carries the recycle disposition beside that teardown rather than selecting a scope.
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
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the [§15.1](15_external-api-surface.md#151-rest-api) start transition onto a slot on a pod serving concurrent sessions, whether that slot was reserved at creation or placed by the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot retry policy, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session on the connection the failed stage still holds, before that connection closes, and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. The reclaim is sent on the connection the failed attempt already holds when that connection is still open, because reusing it costs nothing; the fence does not depend on the connection, and a reclaim sent on a fresh connection is fenced exactly as one sent on the original. The reclaim names the bind attempt it compensates by carrying that attempt's token ([Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract)), and it may name no other. The gateway mints that token before the attempt's first pod-side RPC and holds it independently of any response, so every attempt holds its own token for the whole of the obligation and a compensation always names one. The reclaim is answered under the named rules [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states. A `superseded` reclaim released nothing, because a different bind attempt owns the entry or because the entry carries no token, and an `absent` reclaim released nothing, because the adapter holds no entry for the session. Both are completed reclaims, and a reclaim that reports a clean exit leaves the slot unleaked whatever outcome it carries. An untokened entry an abandoned attempt's late start left behind survives that reclaim and is released only by an unconditional teardown or by the pod's retirement, which [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) records as a residue of the token contract. A reclaim the adapter does not answer, and one whose answer does not report a clean exit, whatever outcome that answer carries, are the reclaims that did not complete. On a pod serving concurrent sessions a reclaim that did not complete leaves the slot `leaked` under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) disposition, which holds the slot's occupancy and counts it toward the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) whole-pod replacement trigger. The slot identifier is the session identifier, and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states that the adapter holds that identifier from the deregistration of its registry entry until the cleanup that reclaims it has finished, so a further attempt at the same session on that pod meets that hold while the reclaim runs and is refused as a transient condition. Nothing about the attempt's retryability changes. A bind attempt outside this obligation fails on a pod serving one session. It reclaims nothing on the pod and sends no `Shutdown`: the pre-attached failure disposition deletes the pod's claim, and [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) states what becomes of the slot state the attempt left there. This obligation governs the slot state on a pod that outlives the attempt.
```

The paragraph's incompleteness predicate quantifies over every outcome rather than over
`reclaimed` alone. On a conforming adapter the two forms select the same slots, because SPEC-5's
reclaim-outcome rule ties `absent` and `superseded` to a clean exit, so only a response reporting
`reclaimed` can fail the clean-exit test. Quantifying over every outcome fails closed against an
adapter that answers otherwise, and it is what lets CODE-4's disposition read the RPC error and
the clean-exit flag alone rather than branch on the outcome value.

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
lease is gone by the time `cleanupCommands` run. The whole-pod credential purge at §5.2 scrub step 0 is unchanged. It is addressed from disk, so
it reaches the credential file a per-slot cleanup left behind wherever an entry for that slot
survives or not, and §4.9's arming and `AUTH_EXPIRED` firing rules are unchanged.

The second anchor is the `**Scrub model.**` paragraph, which is §5.2's
concurrency-independent statement of the rule the report exception qualifies. The paragraph
reads, verbatim:

```
**Scrub model.** The scrub is uniform across session-mode configurations: a per-slot cleanup runs on every session release, reported by the adapter via `ReportSessionScrub` ([Section 4.7](04_system-components.md#47-runtime-adapter)), and a whole-pod scrub runs whenever occupancy reaches zero on a recycling pod before the pod is reused, reported via `ReportPodScrub`.
```

Append to that paragraph:

```
That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency. It also runs on a slot whose bind is abandoned or fails after the slot enters `receiving_uploads` and before it reaches `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), performed either by the [Section 7.1](07_session-lifecycle.md#71-normal-flow) pod-side reclaim where that obligation reaches the attempt or by the adapter's own handler for a start that fails, and a cleanup on that path reports no outcome, because `ReportSessionScrub` advances the pod's served-session count and that count records the sessions the pod's shared runtime process has been given. A cleanup on that path that a reclaiming `Shutdown` performs and that does not complete is accounted on that request's response, which does not report a clean exit. On a pod serving concurrent sessions the slot is then `leaked` under [Section 6.2](06_warm-pod-model.md#62-pod-state-machine), so it holds its occupancy, counts toward the whole-pod replacement trigger stated below, and is surfaced on the `lenny_adapter_leaked_slots` gauge. On a pod serving one session neither the `leaked` sub-state nor the whole-pod replacement trigger applies, and no per-slot cleanup runs at all for a bind that fails at the workspace-preparation, setup or credential-assignment stage there, because neither the [Section 7.1](07_session-lifecycle.md#71-normal-flow) obligation nor an adapter handler reaches it; nothing is reported, and [Section 6.2](06_warm-pod-model.md#62-pod-state-machine) states what becomes of that slot's state. A cleanup the adapter runs inside the failed start's own handler reports nothing and no response carries its outcome, so its residue is accounted at the whole-pod boundary instead: on a recycling pod the occupancy-zero whole-pod scrub stated below removes every per-slot workspace tree and every per-slot credential file the pod still holds and verifies their absence, and a verification that fails is handled under the pool's `onScrubFailure` policy stated below; a pod that does not recycle retires at that boundary and the residue does not outlive it. A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome. A session release produces at most one cleanup-outcome report, filed by the cleanup that reclaims the slot.

**Slot-identifier reclaim hold.** The adapter holds a slot's identifier from the critical section that deregisters the slot's registry entry until the cleanup that reclaims the slot has finished. The hold outlasts the deregistration because the cleanup's remaining acts are addressed by the slot identifier rather than by the entry: the removal of the slot's workspace directory, the removal of its credential directory, the kill of its process group, and the close of the session on the pod's shared runtime process are each resolved from the identifier, and every attempt at the same session names the same identifier. The hold is a property of the identifier rather than of an entry, because the entry is what a later attempt would re-create and because the adapter's other rules read the entry set as the pod's live occupancy. While the identifier is held the adapter admits no request that would create or resolve a registry entry under it, and refuses one as a transient condition so the caller retries. The requests that can create one are `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `Resume` and `ConfigureWorkspace`. A request that resolves an entry without creating one is refused on the same terms, which is what refuses a [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload still in flight when the cleanup opens the hold. `Shutdown` is the one request outside the hold, governed by the rule stated for it rather than by this one. A refusal is the only record the adapter makes of the hold: no report and no counter names it, and a bind refused this way is accounted by the gateway as an ordinary transient slot failure. The hold lasts as long as the cleanup it covers. The cleanup's close of the session on the pod's shared runtime process is bounded by the graceful window the reclaiming `Shutdown` carries when it carries one, by that request's own deadline when it carries none, and, for the [Section 10.1](10_gateway-internals.md#101-horizontal-scaling) hold-timeout termination, which runs under no request, by a graceful window of ten seconds. A cleanup that closes no runtime, which is what a release compensating a bind whose start the adapter never admitted performs, is bounded by the request it runs inside. A release that runs its cleanup inside the RPC that requested it ends the hold before that RPC answers, so the pod-warm bind sequence that follows an SDK demotion does not meet it. [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token the adapter stamps on a slot's registry entry and the reclaim that names it.
```

The reclaim-hold paragraph is what makes the reclaim exclusive while it runs. The entry
deregistration and the destructive steps are not one act: the tree removal, the credential
directory removal, and the process-group kill each resolve from the slot identifier, which
every attempt at the same session shares, so a successor admitted between the two would be
torn down by a reclaim that had already refused nothing. The paragraph states the hold
as a property of the identifier rather than of the entry for that reason. The paragraph states
no terminal for a failed cleanup, because the hold's bound is the cleanup's return on every arm,
success or failure, and the disposition of a cleanup that does not complete is the scrub-model
paragraph's `leaked` clause, which §7.1 reads off the response's clean-exit answer. The
identifier hold and the `leaked` occupancy are separate objects, and this paragraph states only
the former.

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
replacement trigger already account for. On a pod serving one session neither the `leaked` sub-state nor the whole-pod replacement
trigger applies, so the clause points at §6.2, which states what becomes of that slot's state
on each of the pod's two exits, instead of restating it. §7.1's paragraph and the edge-case
list cite the same section and restate nothing. The
clause sits here because §5.2 is where the report is withheld, and a reader of the withholding
rule alone would otherwise read the leak accounting as withheld with it. The accounting the
clause names reaches every bind path §7.1 binds: the gateway's slot-failure accounting covers
the retry-placed bind path today, and the code lane extends the same helper to the create-time
reserved path and to the §7.3 re-attach, so the clause holds on all three.

### SPEC-4 · the occupancy projection's claim-deletion statements (spec/06_warm-pod-model.md § 6.2, and spec/04_system-components.md § 4.6.1 in the sub-section below)

The claim-deletion projection is stated in §6.2's fenced `Occupancy projection` block, in the
§6.2 projection prose above it, which states it in two clauses, and in §4.6.1's **Occupancy
projection** bullet list, which states it in two bullets. §4.6.1 owns the projection and §6.2
cites it as the authority, so both sections are edited here. Every one of those statements is
keyed on the pool's recycle setting or on its retirement limits, and the projection reads
neither. `pkg/controller/warmpool/occupancy.go:128-140` switches on the pod's current phase
alone: `state.Reserved` with no claim projects `Idle` and `state.Claimed` with no claim projects
`Draining`, and the function's own comment states the rule the edits below adopt, that "the §6.2
state machine encodes the recycle-versus-one-session distinction in the phase the pod sits in at
the claim DELETE" (`pkg/controller/warmpool/occupancy.go:57-59`). A pod its pool reuses returns
to inventory through the `reserved → idle` edge, which it reaches only after its claim has been
patched through `recycling` and its whole-pod scrub has been reported. The replacements below
re-key every one of those statements on the projected phase at the claim DELETE, which is what
the pre-`running` cleanup paragraph staged after the fence relies on for a pod serving one
session on a pool that reuses it.

In the fenced `Occupancy projection` block, replace the trigger list of the `claimed ──→
draining` entry:

```
  claimed ──→ draining              (terminal claim disposition released or failed; claim deleted
                                     while the pod projects claimed, on a pool of either recycle
                                     setting, because such a pod is unscrubbed and a pod its pool
                                     reuses returns to inventory through the reserved → idle edge
                                     instead; or gateway-stamped lenny.dev/drain-request
                                     annotation — unhealthy threshold)
```

In the projection prose, three clauses are replaced. The clause reading "a pod with no claim
projects `idle`" is replaced by "a pod in a warm-inventory phase with no claim projects
`idle`". The clause reading "a claim deleted on a recycling pod under its limits projects
`idle`" is replaced by "a claim deleted while the pod projects `reserved`, its scrub and any
re-warm already complete, projects `idle`". The clause reading "or a claim deleted on a pod
with `recycle.enabled: false`" is replaced by "or a claim deleted while the pod projects
`claimed`, on a pool of either recycle setting". The rest of the sentence is unchanged.
Re-keying every claim-deletion clause on the projected phase is what keeps the sentence a
partition: widening one clause to either recycle setting while the other stayed keyed on the
recycle setting would have given two answers for one projection input, a claim deleted on a
recycling pod under its limits whose pod projects `claimed` from a `bound` claim, which is the
input a failed bind produces on a `maxConcurrentSessions: 1` pool with `recycle.enabled: true`.
The no-claim clause is qualified for the same reason: a pod whose claim is deleted while it
projects `claimed` has no claim, so an unqualified no-claim clause answers `idle` for the same
input the widened last clause answers `draining` for. §4.6.1's corresponding bullet already
carries the warm-inventory qualifier and is untouched. The retirement limits drop out of the
`idle` clause because they are evaluated at the recycle disposition, before the claim is patched
to `reserved`, so a pod that reached `reserved` has already passed them. The fence's own
`reserved ──→ idle` entry is already keyed this way and is untouched.

The `Recycle edges` group is untouched. Its `a failed session` trigger is correct for its own
group, whose preamble requires the claim to be patched `bound → recycling` at the recycle
boundary before any edge in the group is evaluated, and the pre-attached bind failure never
reaches that boundary.

### SPEC-4 · spec/04_system-components.md § 4.6.1 (occupancy projection, the claim-deletion bullets)

§4.6.1's **Occupancy projection** bullet list states the same two claim-deletion projections and
keys them the same way, and its `draining`, then `terminated` bullet closes with a sentence
that is false against the controller: `ProjectOccupancyPhase` never returns `idle` from
`claimed` on any pool (`pkg/controller/warmpool/occupancy.go:134-140`). Both bullets are re-keyed on the projected
phase and the false sentence is deleted rather than reworded, because the replacement trigger
clause already states where a pod its pool reuses returns to inventory.

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
  receiving_uploads ──→ slot_cleanup    (a cleanup reclaims the slot after its bind is
                                         abandoned or fails before the runtime has been given
                                         the session, a start still in flight included)
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
**Pre-`running` slot cleanup.** A slot reaches `running` when the pod's shared runtime process has been given the session with its session identifier. Every earlier stage of the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence, credential assignment included, and a start still in flight, whose session has not yet reached the runtime, leave the slot in `receiving_uploads`. What each stage leaves on the pod differs: the workspace-preparation and setup stages leave the slot's registry entry and its per-slot workspace tree; credential assignment additionally binds that entry to the session, writes the slot's credential file and arms the slot's [§4.9](04_system-components.md#49-credential-leasing-service) direct-delivery-mode lease-expiry timers; a start additionally records the entry as started. A bind abandoned or failed at one of those stages takes the `receiving_uploads → slot_cleanup` edge when a cleanup reclaims the slot, which the [§7.1](07_session-lifecycle.md#71-normal-flow) pod-side reclaim performs where that obligation reaches the attempt and the adapter performs in its own handler for a start that fails; a session the runtime has already been given is a slot in `running` and takes the `running → slot_cleanup` edge the fence above carries. A cleanup either edge runs removes all four: the registry entry, the per-slot workspace tree, the slot's credential directory and its armed lease-expiry timers ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)).

A bind that fails at the workspace-preparation, setup or credential-assignment stage on a pod serving one session drives neither edge, because neither the [§7.1](07_session-lifecycle.md#71-normal-flow) obligation nor an adapter handler reclaims it. The pod's two exits reach that residue on different terms. On the retiring exit, which the pre-attached failure disposition takes, the pod's claim is deleted while the pod projects `claimed`, the fence above retires a pod that projects `claimed` when its claim is deleted, on a pool of either recycle setting, because such a pod is unscrubbed and the pod's return-to-inventory path is the `reserved → idle` edge, and the entry, the tree, the credential file and the timers all end with the pod. The pod's one return-to-pool edge is `reserved → idle`, which a pod on a pool that reuses it reaches through the `recycling` binding state once its whole-pod scrub has been reported; a `vm-restart` pool's pod retires at that boundary instead. That scrub removes every per-slot workspace tree and every per-slot credential file the pod still holds and verifies their absence ([§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes)), addressed from disk and independently of what the adapter's registry holds, and a verification that fails is disposed of under the pool's `onScrubFailure` policy, whose default returns a pod its pool reuses with a `scrub_warning` annotation and the residual-state risk that section states, and whose retirement cases, the `maxScrubFailures` limit and the `vm-restart` pool's retirement at the same boundary, that section states as well. The slot's registry entry and its armed [§4.9](04_system-components.md#49-credential-leasing-service) lease-expiry timers are not reached by that scrub. This paragraph is where that disposition is stated; [§7.1](07_session-lifecycle.md#71-normal-flow) and [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) cite it.

The cleanup either edge runs, and the report the pre-`running` reclaim withholds, are the ones [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states. [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold, from the adapter's deregistration of the slot's registry entry until the cleanup finishes, which is what refuses a bind onto the slot's identifier in that window. The `slot_cleanup` sub-state is tracked per session and carries no admission rule of its own.
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

The requests that carry `bind_attempt` are `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `Resume`, and the compensating `Shutdown`. Those are the requests through which a bind attempt creates or resolves a registry entry before its session starts, together with the reclaim that compensates the attempt. `StartSession` and `ConfigureWorkspace` carry none, and no other request on this contract carries one. A start may be issued by a later stage of the same binding than the stage that created the entry, so comparing a token on either of them would refuse a start against an entry the same binding legitimately created. No token comparison runs against either of them, and the rules below reach them by their own conditions. No response reports a token. Nothing a caller must hold travels on a response, so a caller that received no response at all still holds the token it minted, and a caller on a fresh connection holds it exactly as the caller on the original connection did.

**The stamp-once rule.** The adapter stamps once, on the entry it creates. When the adapter creates a registry entry for a slot identifier it holds none for, it writes the requesting request's `bind_attempt` onto that entry. A request that carries no token creates an entry that carries none, which is what `StartSession` and `ConfigureWorkspace` create when they resolve none. It never writes that field again, in either direction: an entry's token does not change while the entry lives, and a request that resolves an entry the adapter already holds writes nothing. The first attempt to create an entry therefore owns it until the entry is removed, and no later request takes that ownership away. The adapter resolves or creates the entry, stamps the token on an entry it creates, and performs the comparisons stated below as one indivisible step, under the same lock that guards the registry. Performing them as separable steps does not conform even when each step is correct on its own, because an adapter that resolved an entry, released its lock, and then stamped admits a second attempt onto that entry in the window between the two.

**The start-confirmation rule.** A start confirms the entry is still its own. Before the adapter records the pod's shared runtime process as holding a session, it resolves the registry entry for that slot identifier again, inside the lock that guards the registry, and confirms that it still holds an entry for that identifier carrying the same bind attempt token the entry carried when the request that starts the session was admitted. The confirmation is required of every request that starts a session, including one that carries no token of its own, for which the token compared is the one the entry carried at admission, so an entry no later attempt replaced compares equal to itself. When the adapter holds no entry for the identifier, or holds one carrying a different token, it records nothing, removes no entry, and reports no cleanup outcome for the slot, because the slot never reached `running` and [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) files at most one cleanup-outcome report per session release, from the cleanup that reclaims the slot. It takes the session back off the shared runtime process and refuses the request that started it, answered on `ABORTED`, which is the transient classification a caller retries on. The rule is conditioned on what the request does rather than on the name of the RPC that does it, as the rules stated here are.

**Admission.** The rules stated here and below are named, and every other statement of this contract refers to them by name rather than restating them. Each rule states its own condition, on the request's fields and on the registry entry it resolves, and it reaches every request meeting that condition. They govern every request on this contract that can create a slot registry entry: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials`, `StartSession`, `Resume`, and `ConfigureWorkspace`. A `Shutdown` is governed by the teardown rules stated further below in place of these. Every other RPC on this contract resolves an entry the session already holds, creates none, and is outside these rules, apart from the reclaim hold, whose scope [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states and which reaches every request that would create or resolve a registry entry under the held identifier. No rule is scoped by the name of an RPC.

Two checks precede the cascade and are applied in the order stated here. **The pairing rule** is decided first, on the request's fields alone and before the adapter reads the registry: a request other than a `Shutdown` that carries `bind_attempt`, whose `bind_attempt` is empty and which is not marked `mid_session`, and one marked `mid_session` whose `bind_attempt` is non-empty, are each answered `INVALID_ARGUMENT`, and the adapter creates, resolves, stamps, and changes nothing, even when it holds no entry for that slot. A `Shutdown` is outside the pairing rule, because its `bind_attempt` pairs with `unconditional_teardown` under the teardown-pairing rule stated further below. **The reclaim hold** [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states is applied next, to a request the pairing rule admits, and its scope is the one that section states: it reaches every request that would create or resolve a registry entry under the held identifier, and a request it refuses is refused as that section states and reaches nothing further. Only a request that both admit reaches the cascade below.

The adapter applies the cascade as an ordered evaluation, stopping at the first rule whose condition the request meets, so a request meeting the condition of more than one rule is governed by the earlier rule. It applies the whole cascade inside one indivisible step under the lock that guards the registry, together with the resolve, the creation and the stamp, before it creates anything, stamps anything, or returns. Performing the cascade as separable steps does not conform even when each step is correct on its own, because an adapter that resolved an entry, released its lock, and then stamped admits a second attempt onto that entry in the window between the two.

**The mid-session-create rule** comes first. A request marked `mid_session` that resolves no entry is answered `FAILED_PRECONDITION` and creates nothing.

**The create-and-stamp rule** comes next. A request that is not marked `mid_session` and that resolves no entry creates the entry, stamping its `bind_attempt` on it when it carries one; a request carrying no token creates an entry carrying none. It is the only rule that writes that field, so the first attempt to create an entry owns it until the entry is removed, and it reaches every request meeting its condition, a `Resume` among them.

**The attempt identity rule** comes next. A request whose non-empty `bind_attempt` differs from the non-empty token the resolved entry carries is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED`, answered on `ABORTED` and carried on the adapter's error envelope as `CATEGORY_TRANSIENT`, which a caller retries on.

**The started-session rule** comes last. A request that is not marked `mid_session` and that resolves an entry whose session has already started is refused with `SLOT_BIND_ALREADY_STARTED`, answered on `FAILED_PRECONDITION` and carried as `CATEGORY_PERMANENT`, unless it is a repeat `ConfigureWorkspace` for the session that started on that pod, which [Section 4.7](#47-runtime-adapter) publishes as idempotent and which re-points the pre-connected runtime without restarting it. Because the attempt identity rule is applied before the started-session rule, a request whose non-empty `bind_attempt` differs from the resolved entry's non-empty token is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED` even when that entry's session has already started: a bind whose attempt identity is stale is a transient condition its caller retries, and refusing it as a started session would present a transient condition to the client as a permanent one.

**The admit rule** ends the cascade. Any other request is admitted, and an admitted request that resolved an entry leaves that entry's token as it found it, whether the request carried a token or not.

Neither `SLOT_BIND_ATTEMPT_SUPERSEDED` nor `SLOT_BIND_ALREADY_STARTED` appears in the [Section 15.1](15_external-api-surface.md#151-rest-api) REST error catalog, because the gateway consumes both and the client receives the envelope that section already defines for the endpoint that issued the bind sequence.

Which fields each request carries:

| Request | `bind_attempt` | `mid_session` |
|:--|:--|:--|
| `PrepareWorkspace` | non-empty when the request is not marked `mid_session`, empty when it is | carried |
| `FinalizeWorkspace` | non-empty when the request is not marked `mid_session`, empty when it is | carried |
| `RunSetup` | non-empty | not carried |
| `AssignCredentials` | non-empty | not carried |
| `Resume` | non-empty | not carried |
| `StartSession` | not carried | not carried |
| `ConfigureWorkspace` | not carried | not carried |
| `Shutdown` | pairs with `unconditional_teardown` under the teardown-pairing rule | not carried |

A [Section 7.4](07_session-lifecycle.md#74-upload-safety) mid-session upload is the marked form. It reuses `PrepareWorkspace` and `FinalizeWorkspace` on the entry the session already holds, asserts no attempt identity, cannot create an entry under the mid-session-create rule, and is exempt from the started-session rule, because the session it runs against has started by definition. Because it asserts no attempt identity, the gateway issues one only for a session the issuing replica holds a live binding for, and that admission is what keeps a request asserting no identity away from an entry its sender does not own.

**The first-frame rule.** `PrepareWorkspace` is client-streaming. The adapter resolves the slot identifier once per call, from the first frame that carries one, and reads `bind_attempt` and `mid_session` from that same frame. Every rule of this block, the pairing rule included, is decided on that frame's values, and the adapter reads neither field on any later frame of the call, so a later frame states nothing about the call's admission.

**`Shutdown` states which teardown it asks for.** The named rules below govern a `Shutdown` in place of the admission rules, applied as an ordered cascade: the adapter stops at the first rule whose condition the request meets, so a request meeting the condition of more than one rule is governed by the earlier rule. The teardown-pairing rule is decided on the request's fields alone, before the adapter reads the registry. The four that follow are decided inside the same critical section as the deregistration and precede it, so an entry the adapter refuses to release is never removed and restored. Each rule fixes the outcome the response reports and whether the request removes an entry; the two teardowns follow from the removal rather than being decided separately.

**The teardown-pairing rule.** A `Shutdown` carries either a non-empty `bind_attempt`, naming the bind attempt it compensates, or `unconditional_teardown`, and it carries exactly one of the two. A request carrying neither, and a request carrying both, is answered `INVALID_ARGUMENT`: the adapter performs neither teardown, removes no entry, and changes nothing.

**The no-entry rule.** A request for a session the adapter holds no entry for removes nothing, performs neither teardown, and answers `absent`. It reaches a request of either form.

**The unconditional-teardown rule.** A request asking for the unconditional teardown is addressed to whatever entry the adapter holds. It removes that entry, performs both teardowns under their own preconditions, and answers `reclaimed`. It is what every caller other than a compensating reclaim sends.

**The attempt-mismatch rule.** A request naming a bind attempt whose token differs from the token the resolved entry carries, and one whose resolved entry carries no token, removes nothing, performs neither teardown, and answers `superseded`. The entry-carries-no-token arm answers `superseded` so that the rule fails closed. An entry carrying no token is one a `StartSession` or a `ConfigureWorkspace` created, because those two carry none, and such an entry may be the one a successor's own start created for a session now running, which is why a reclaim naming an attempt removes nothing there. An entry carrying no token is therefore released only under the unconditional-teardown rule or by the pod's retirement, and every later bind attempt at that session on that pod is refused while it stands. That is a residue this contract records rather than one it closes.

**The attempt-match rule.** A request naming a bind attempt whose token equals the token the resolved entry carries removes that entry, performs both teardowns under their own preconditions, and answers `reclaimed`.

**The reclaim-outcome rule.** `reclaimed`, `superseded`, and `absent` are the outcomes a `Shutdown` response reports, and each has one meaning, fixed by the rule that answers it. `reclaimed`, answered by the unconditional-teardown rule and the attempt-match rule, means the adapter held the entry the request was addressed to and released the slot. `superseded`, answered by the attempt-mismatch rule, means the adapter holds an entry for the session that the request is not addressed to, so a different bind attempt owns the slot identifier and the reclaim released nothing. `absent`, answered by the no-entry rule, means the adapter holds no entry for the session. All of them are successful outcomes and are answered on a successful RPC: a `Shutdown` is never refused for naming an attempt that does not own the entry. The no-entry rule and the attempt-mismatch rule remove no entry and perform neither teardown, so a response reporting `absent` or `superseded` reports a clean exit; only a response reporting `reclaimed` can report an unclean exit, which it takes from the runtime close and the cleanup the removing rule performed. This block states no refusal onto a slot identifier; the refusal a cleanup in progress imposes is stated by the reclaim hold in [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes). The two teardowns a removing rule performs, and the precondition each carries, are stated in the [Section 4.7](#47-runtime-adapter) `Shutdown` row.
```

The block sits in §4.7.1 rather than in §4.7.9 because §4.7.9 is a `type: agent`
orientation list and the token binds every session mode the RPC tables serve. The named rules are stated here and nowhere else. The §4.7 `Shutdown` row, §7.1's reclaim
obligation, §15.4's non-conformance statement, CONF-1's battery and the conformance case lists
each reach a rule by name rather than restating it, so a refinement to one rule lands once. §4.7
owns the two teardowns and their preconditions; this block owns what the value is, which requests
carry it, when the adapter stamps it, which entry a request is addressed to, what becomes of that
entry, and what the response reports.

Some of what the block states are obligations this proposal takes on rather than shipped
behaviour it records. The atomicity clause makes first-writer-wins implementable from the prose,
which it is not while the resolve, the stamp and the comparison read as three acts. The start
confirmation is the second: the shipped adapter records the session unconditionally, so the rule
is new behaviour rather than a restatement, and §15.4 names it in the rule set it publishes
non-conformance against, so an adapter written from the published contract performs it. And the
sentence on the gateway's own admission of a mid-session upload is the whole safety argument for
a request that asserts no attempt identity, so the implementation confirms that the shipped
mid-session admission guard reads what it appears to read before the mid-session conditioning
lands, and records what it found.

### SPEC-5 · spec/15_external-api-surface.md § 15.4 (after the SDK-warm demotion contract)

Insert the two blocks below immediately after the paragraph beginning `**SDK-warm demotion
contract:**` and before the `#### 15.4.1 Message Format and Binary I/O Requirements`
heading. §15.4 is where the adapter contract is published to third-party adapter authors, so
the statement here is written to be implementable without reading any Go and states the
non-conformance case for each part:

```
**Bind attempt token contract:** [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states the bind attempt token: what it is, which requests carry it, when the adapter stamps it, which requests it refuses, what a start confirms before it records the pod's shared runtime process as holding the session, and how a `Shutdown` states which teardown it asks for. That statement is normative for a third-party adapter and is not restated here. It names the rules this section publishes non-conformance against: the pairing rule, the reclaim hold, the mid-session-create rule, the create-and-stamp rule, the attempt identity rule, the started-session rule, the admit rule, the stamp-once rule, the start-confirmation rule, the first-frame rule, the teardown-pairing rule, the no-entry rule, the unconditional-teardown rule, the attempt-mismatch rule, the attempt-match rule, and the reclaim-outcome rule. The condition each rule reaches, the order in which the adapter applies them, the answer each gives, and the status code and error category that answer is carried on are each stated once, by that section, with one exception: the reclaim hold's scope, its refusal and its answer are stated by [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes), which [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) names and cites rather than restating, and whose wire form the reclaim-hold block below publishes.

An adapter conforms on a rule when, for every request meeting that rule's condition, it gives that rule's answer with the code and the status that rule names, performs the acts that rule states and no others, and admits each exempt form that rule names. It writes the bind attempt token only where the create-and-stamp rule writes it. Three failures are not any single rule's condition and are stated here. An adapter does not conform when it applies the rules in any order other than the one [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states, because that order is what makes a stale attempt identity a transient refusal rather than a permanent one. It does not conform when it performs the resolve, the creation, the stamp and the comparisons as separable steps rather than as one indivisible step under the lock that guards the registry, because two attempts can then be admitted onto one entry. It does not conform when it answers a gRPC error for an outcome the reclaim-outcome rule reports, because `reclaimed`, `superseded` and `absent` are all successful outcomes answered on a successful RPC.
```

```
**Slot-identifier reclaim hold:** [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states the reclaim hold: the adapter holds a slot's identifier from the deregistration of that slot's registry entry until the cleanup that reclaims the slot has finished, because the cleanup's remaining acts are addressed by the identifier rather than by the entry. This is what an adapter must exhibit on the wire. While the identifier is held, a request that would create or resolve a registry entry under it is refused with the gRPC status code `ABORTED`, which is the transient classification a caller retries on. An adapter that admits such a request while the cleanup is running does not conform. An adapter that refuses one with a permanent status, or that answers a status the caller cannot retry, does not conform. `Shutdown` is not held: a `Shutdown` naming a session whose cleanup is running removes nothing and answers `absent` under the no-entry rule [Section 4.7.1](04_system-components.md#471-role-and-gateway-rpc-contract) states.
```

Both blocks state the contract and neither states a Go type, a field number, or a package
name. The wire form is `schemas/lenny-adapter.proto`, which §15.4 already names as the
published artifact. The token travels on the request side alone, so the wire edit that lands it
adds one string field to each of the six requests §4.7.1 names, the mid-session marker to
`PrepareWorkspace`, the unconditional-teardown flag and the outcome to `Shutdown`, and a value
to the error enum for each of the two refusals. No response reports a token.

The rules are normative for a third-party adapter, and the project has no harness that can run
one: the conformance battery drives a runtime binary over JSONL against a fake adapter and speaks
no gRPC. The wire-level enforcement is therefore the tier-3 contract suite, which drives this
adapter over a real gRPC channel and can exercise every rule above, alongside a descriptor gate
pinning the field numbers and types. The in-process battery at tier 10 drives the same adapter
without the wire. The absence of a third-party harness is recorded as a §28.4 claim-register row
with status `ABSENT`, following the rows the `coordination_generation` fence already carries.

### SPEC-5 · spec/15_external-api-surface.md § 15.1 (REST error catalog, `SETUP_COMMAND_FAILED` row)

The started-session refusal is a second producer of a deterministic `FailedPrecondition` failure
in the setup window, and the gateway's envelope selection is unchanged by this proposal, so the
refusal reaches this row only where it arrives at the setup-command request, which is the stage
whose deterministic `FailedPrecondition` failure §15.1 already maps to this code. A refusal
arriving at any other bind-sequence request reaches the client under the envelope that stage
already selects. Three sentences of that row state a cause the refusal does not have. In the `SETUP_COMMAND_FAILED` row of the
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

The row's exclusion sentence, which states that every gRPC code other than `FailedPrecondition`
in that window is not this code, is left exactly as it stands. It is keyed on the gRPC code, the
refusal is answered on `FAILED_PRECONDITION`, and the superseded refusal is answered on `ABORTED`
and falls under the exclusion unchanged. `details.reason` keeps its one value, so no client
contract changes.

### SPEC-5 · spec/06_warm-pod-model.md § 6.2 (pre-attached retry policy, client visibility)

The `**Client visibility:**` bullet under the pre-attached retry policy restates the same
setup-window mapping for `POST /v1/sessions/{id}/start`, and its second clause is quantified over
every setup-window failure, so a started-session refusal the adapter answers to the setup-command
request falsifies it. Replace the clause, which reads, verbatim:

```
while any other setup-window failure stays the retryable `STARTING_FAILED` fallback
```

with:

```
while any setup-window failure other than a deterministic `FAILED_PRECONDITION` answer to the setup-command request stays the retryable `STARTING_FAILED` fallback
```

This re-keys the clause on the request the adapter answered as well as on the gRPC code, matching
the §15.1 row above: a `FAILED_PRECONDITION` the adapter answers to any other bind-sequence
request keeps the envelope that stage already selects, which for the workspace stages at `/start`
is the retryable fallback the clause names. The rest of the bullet stands as it is.

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
  `PROTOCOL_VERSION_INCOMPATIBLE` is the shipped precedent: it is an adapter `ErrorCode` published
  through §15.4 with no §15.1 row. What is deliberate here is the absence of a new row. The
  section itself is edited under SPEC-5 rather than untouched: the started-session refusal is a
  second deterministic `FAILED_PRECONDITION` producer in the setup window and the existing
  `SETUP_COMMAND_FAILED` row states its cause as the setup command alone, so SPEC-5 widens that
  row's cause, retryability and setup-output sentences.

## Spec files touched

- `spec/04_system-components.md`: §4.1 Request Message Scope (one sentence replaced), §4.6.1's
  **Occupancy projection** bullet list (the two claim-deletion bullets replaced, one false
  closing sentence deleted), §4.7
  Gateway → Adapter RPC table `Shutdown` row (first sentence replaced, the two teardowns with
  their preconditions and a pointer at §4.7.1's named rules added), §4.7.1 (the bind attempt
  token block, new, after the RPC tables), §4.7.9 step 5 (one sentence).
- `spec/05_runtime-registry-and-pool-model.md`: §5.2 `**Scrub model.**` paragraph (the
  per-slot cleanup pointer, the cleanup-outcome report rules, and the slot-identifier
  reclaim-hold paragraph appended, whose closing sentence points at the bind attempt token), and
  the §5.2 `**Slot cleanup:**` bullet's action-list sentence (replaced).
- `spec/06_warm-pod-model.md`: §6.2's occupancy projection (the `claimed ──→ draining`
  trigger list in the fence, and three clauses in the projection prose, each re-keyed on the
  phase the pod projects at the claim DELETE), §6.2 per-slot sub-state fence (one edge), the prose after
  it (one paragraph, pointing at the §5.2 reclaim hold), the §6.2
  `resuming` mid-resume cancel bullet (one clause), and the §6.2 pre-attached retry policy's
  `**Client visibility:**` bullet (one clause re-keyed on the gRPC code).
- `spec/07_session-lifecycle.md`: §7.1 gains a new paragraph after the atomicity paragraph
  carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/15_external-api-surface.md`: §15.4 (the published bind attempt token
  contract and the slot-identifier reclaim-hold contract, new, after the SDK-warm demotion
  contract), and §15.1's `SETUP_COMMAND_FAILED` row (the cause sentence, the retryability
  sentence and the setup-output sentence replaced).
- `spec/16_observability.md`: §16.1's metric catalog (one row for each counter
  the compensation's caller and the adapter's fail-closed row emit).
- `spec/29_communication-scenarios.md`: §29.4 session-end step 13 (one sentence appended).

Each reader-facing reference page that mirrors these sections moves with the section it mirrors.
The edits the non-spec deliverables must carry are these. `docs/reference/metrics.md` gains the
row for each counter, matching the §16.1 rows, so the catalog a reader consults and the catalog
the specification states stay one list (CODE-9). `docs/reference/state-machines.md` gains the
per-slot sub-state row matching the §6.2 edge, and its pod state machine paragraph takes the
same projection clause replacements SPEC-4 makes in §6.2's projection prose, each re-keyed on
the phase the pod projects at the claim DELETE (DOCS-1). `docs/reference/adapter-contract.md`
takes the rewritten `Shutdown` row and the amended `DemoteSDK` row (DOCS-2).
`docs/reference/error-catalog.md` takes four sentence replacements and a replaced remedy cell in
the `SETUP_COMMAND_FAILED` row it already carries rather than a new row, three of them mirroring
SPEC-5's §15.1 replacements and the fourth re-keying the page's retryable-fallback sentence off
the cause class, which §15.1 states on the gRPC code and does not need (DOCS-3).