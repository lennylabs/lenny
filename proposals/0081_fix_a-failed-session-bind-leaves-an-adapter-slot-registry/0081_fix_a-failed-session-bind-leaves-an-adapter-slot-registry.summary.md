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

The gateway compensates a failed bind, and a token the gateway mints fences that compensation.
At every post-connection bind-stage failure and at a failed `Resume`, the gateway sends the
adapter a `Shutdown` for the slot it reserved, so the registry entry, the per-slot tree, the
slot's credential directory and the armed §4.9 expiry timers are removed instead of surviving
for the life of the pod. Before an attempt issues its first pod-side RPC the gateway mints one
opaque bind attempt token, and it carries that token on the requests §4.7.1's carriage table
marks it carried on, the compensating `Shutdown` among them. The adapter stamps the
token on an entry it creates, compares it against the entry a later request resolves, and
answers a compensation according to whether the token it names owns the entry, so an abandoned
attempt cannot act on a successor's entry and a teardown that arrives after a retry has bound
the slot removes nothing. §4.7.1 states admission and teardown as two ordered cascades of
numbered rules, and every other carrier of this contract names a rule by its number rather than
restating it.

**Decisions.**

- The fence is a token the caller mints per bind attempt rather than an epoch the adapter mints
  per entry. An adapter-minted value reaches the caller only on a response, so an attempt holds
  it only after the response that reported it. That latch is what made the epoch self-defeating:
  a bind that fails inside its first entry-creating RPC never received one and reclaims
  unfenced, a compensation on a fresh connection holds none, and two attempts sharing one entry
  share one value and cannot be told apart. The value that has to fence the compensation is
  exactly the value a failed attempt may never have been given. A token held by the caller
  before its first RPC has none of those windows.
- First-writer-wins governs the stamp, rather than last-writer-wins: the stamp-once rule and
  rule 4 (the create-and-stamp rule) fix where the write happens, and the reason is that a
  caller-minted identifier carries no order. A design in which an admitted request can report a
  fresh value onto an entry the adapter already holds lets an earlier attempt's straggler take
  ownership back, which is the objection that withdrew the per-attempt form in an earlier round.
  Writing once makes the mechanism monotone by construction and leaves nothing for a counter to
  order.
- The comparison lives inside `ensureSlotStateLocked`, under `s.mu`, rather than at the top of
  each handler. Its production callers cover the seven requests §4.7.1's admission rules govern, the
  claim path included. A comparison placed in the workspace handlers instead leaves the claim
  path ungated, and a `Resume` landing on a leaked entry then resolves it, sets `started`, and is
  destroyed by the stale compensation that entry's own attempt sent. §4.7.1's registry
  critical-section paragraph states that atomicity.
- `unconditional_teardown` is the affirmative form of the unconditional teardown, rather than an
  empty `bind_attempt` meaning it, and rule 10 (the teardown-pairing rule) is where that choice
  lands. An empty value meaning destruction is fail-open on a destructive path, it makes an
  adapter that refuses a `Shutdown` naming nothing non-conforming when it is safer than the
  contract, and a contract test can pin that the compensating caller sends a token while it
  cannot pin that the other callers meant to destroy. Both fields are bare scalars with no wire
  presence, so §4.1's rule that no operation is selected by a field's presence standing in for a
  scope is untouched, and `ShutdownRequest.recycle` is the shipped precedent for a disposition
  carried beside the address.
- The cascade is stated once, as §4.7.1's numbered rules with one table of which request carries
  which field, and every other carrier names a rule rather than restating it. Two of the
  decisions those rules encode are this proposal's own. A mid-session upload asserts no attempt
  identity, because its binding predates the request and may have been made by another replica,
  so requiring a token there is unshippable while permitting one on a bind-sequence request is
  fail-open; rule 1 (the pairing rule) is what makes each form's field set mandatory in both
  directions. And a mid-session request never creates an entry, under rule 3 (the
  mid-session-create rule), because today a mid-session finalize for a session the pod holds no
  entry for creates the entry and a full on-disk tree before the mid-session branch is consulted
  (`pkg/adapter/staging.go:181` resolves, `:239` reads the marker), and an entry created that way
  would carry no token, admit every attempt, match no compensation, and be permanent. Reading the
  marker before the resolve is why it has to be on `PrepareWorkspaceRequest` as well as on the
  finalize, and why rule 9 (the first-frame rule) settles the client-streaming call on the frame
  that resolves the slot identifier.
- `Binder.Prepare` mints a token and sends no compensation (CODE-13), and `Binder.Launch` mints none
  (CODE-4, §4.7.1's carriage lead-in).
- The two refusals are distinct error codes, which CODE-7 translates to Go sentinels (CODE-7 gives
  why). Rule 5 (the attempt identity
  rule) and rule 6 (the started-session rule) fix the status and the category each carries. The
  gateway-side consequence of that mapping is that the identity refusal arrives on `Aborted`,
  which the `codes.Aborted` arm CODE-5 adds to `isTransientPodClaimError` already covers, so the
  superseded case reuses one arm rather than adding a third.
- Neither refusal reaches the client as a failure of the client's own request (operator decision
  29, reversed on 2026-09-24). CODE-5 answers a client request that fails because its bind met either refusal
  with the retryable 503 fallback of its endpoint (`SESSION_CREATION_FAILED`, `STARTING_FAILED`
  or `RESUME_FAILED`) and a `Retry-After` header, at every bind stage, through one typed check
  that `writePodClaimError` runs ahead of the setup-command and slot-failure envelopes, while a
  genuine non-zero setup exit keeps its 422 `SETUP_COMMAND_FAILED`. Rule 6's refusal is
  reachable only through a race between two of the gateway's own bind attempts for one session
  on one pod: attempt A times out, its compensation removes A's entry, retry attempt B creates a
  fresh entry stamped with its own token, A's delayed tokenless `StartSession` starts the session
  on B's entry, and B's next request meets rule 6. The refusal is permanent for that attempt on
  that entry, which is why the adapter keeps `FAILED_PRECONDITION` and `CATEGORY_PERMANENT`, but
  the client's setup commands never failed and a new client request almost always binds, so a 422
  `SETUP_COMMAND_FAILED`, which tells the client a retry fails identically, or a non-retryable
  422 `SLOT_FAILED` misreports it. The exception is the untokened-entry residue the spec Edge
  cases record: while that entry stands, each retry that lands on that pod meets rule 6 and
  answers the retryable fallback until the entry is torn down or the pod retires, which is the
  same unbounded retry decision 41 accepts for the reclaim hold. The superseded refusal is
  already transient and takes the same envelope. The §4.7.1 paragraph after rule 9 is the one spec statement. The §15.1
  `SETUP_COMMAND_FAILED` row and §5.2's `**Client error on exhaustion:**` bullet each gain only a
  pointer sentence at it, because each states a non-retryable envelope a refusal would otherwise
  fall under; §6.2's client-visibility clause and the published error catalog, both
  keyed on the cause, take no edit.
- The remedy is the `Shutdown` RPC the gateway already has, sent on the connection it already
  holds at the failure site. `Shutdown` clause two remains the single removal entry point and the
  token is a precondition on it. The compensation runs on a detached context
  (`context.WithoutCancel`), because the residue class that leaves a runtime running arises
  precisely when the caller's context expired during `StartSession`.
- The compensation is best-effort and its own failure is counted. A reclaim not acknowledged clean sets
  the `leaked` disposition, which `SlotClaimer.ReleaseSlot` already implements, so the pod
  retires through the shipped §5.2 threshold instead of accumulating residue.
- The leaked disposition reads the RPC error and the clean-exit flag alone (CODE-13; the reason is
  in SPEC-2's commentary).
- The credential release returns only the lease identifiers the failed attempt minted (CODE-13,
  **The lease release is scoped to the attempt.**).
- `schemas/lenny-adapter.proto` is opened once, additively, under rule S-2's second window, whose precondition is that every in-flight `pkg/adapter` handler edit has merged first.
  Rule S-2 admits no third window, which is why the phase
  gate is folded into this proposal rather than sequenced ahead of it as its own proposal with
  its own proto change. The precondition binds this proposal's own step ordering, because it
  opens `session.go`, `credentials.go`, `slotcreds.go` and `sdkwarm.go` from S-2's covered
  list: the schema step and its regenerated stubs land in one commit before those handler steps.
- No downstream predicate is re-scoped. `slotCount` keeps counting registered-but-unbound
  entries, `claimPodMCPStartLocked` keeps its raw entry-count guard, and `boundRemains` keeps
  reading the binding. Both are deliberate co-tenancy rules with their reasons in their own
  comments. The residue stops existing; the rules it was breaking stay as they are.
- The compensation's outcome is measured. The counters are added against the §16.1 catalog: a
  compensation answered `superseded` under rule 13 (the attempt-mismatch rule), which is the case
  in which the reclaim released nothing; and a `Shutdown` that met an entry carrying no token,
  as SPEC-6's §16.1 row states.
- File-collision discipline: the compensation's own adapter edits land in `session.go` and
  `runtimegeneration.go`, and the reclaim hold (under rule 2, the adapter refuses a bind onto a
  slot identifier from the deregistration of its entry until the reclaim's cleanup completes, or
  for the pod's life where it does not) and its helper land in a new file.
  `pkg/adapter/slotsession.go` and `pkg/adapter/slot.go` are both opened, because
  `ensureSlotStateLocked` is where the entry is created and is therefore where the token is
  stamped, on that creating branch alone, where the two refusals are raised, and where the hold
  is refused; `slotState` gains one field, `bindAttempt`. Five further adapter files are opened
  for one small edit each: `server.go` declares the reclaim-hold set and the per-slot guard
  table, `staging.go`, `slotcreds.go` and `credentials.go` move their resolve sites to the
  shared helper and pass what their RPCs assert, and `holdstate.go` gives
  `terminateHeldSession` a deferred hold release, a deferred slot-guard release and its own
  per-member ten-second close context. A later position
  covering proposal 0080 rewrites these files, so this proposal keeps its edits in each to the
  smallest set the mechanism needs.

**Watch out for.**

- CODE-8 lands before CODE-6's gates (S12 before S14). CODE-8's opening gives the reason.
- S13 lands before S14 and S16. The implementation checklist's preamble gives the reason.
- The phase gate must not refuse a legitimate repeat. Rule 6 exempts `ConfigureWorkspace`'s
  idempotent repeat, which resolves a started entry on purpose on an SDK-warm pod; CODE-6's
  **Production call sites that must thread the parameter.** table gives the `allowStarted` value
  at every resolve. A gate that refuses the repeat breaks the SDK-warm path; one that admits a
  foreign attempt defeats the mechanism.
- CODE-15 lands before CODE-13 (S16 before S19), because today's `Shutdown` sent at the
  credential-assignment stage would tear the shared runtime down for an unstarted session.
  CODE-15's bullet on why the runtime teardown must not run for an unstarted session gives the
  mechanics.
- The §7.1 connection sentence is a cost preference; restoring it as a correctness rule would
  foreclose the durable compensation record that position 2 of the remediation plan stages.
- The accepted failure modes of the contract are stated in the spec-changes file's
  `## Edge cases and accepted failure modes` section, which is their single carrier.
- Two further residues are priced and accepted. A retry is refused while the previous attempt's
  entry stands, and burns one attempt; the window is bounded by the compensation's latency plus
  the §5.2 reclaim hold where the cleanup completes, and by the pod's remaining life where it does
  not, and a refused retry is cheaper than a destroyed session, than a successor reaching `running`
  on an empty workspace, or than a successor materializing over the residue a failed cleanup left
  in place. And an entry that outlives its attempt costs the pod its MCP arming and its
  unaddressed-frame path for the pod's life, which both gates count deliberately and which is
  recorded under the defects this proposal does not stage.

## Goals

- Fence each bind attempt with a token the caller mints, carried on the requests that stage the
  session's workspace, its credentials and its setup and on a `Resume`, and on the compensation
  that reclaims the entry, so an abandoned attempt cannot act on a successor's entry. A start
  carries no token and confirms instead that the entry it claimed is still its own before
  recording the runtime as holding the session.
- Remove the adapter slot registry entry, the per-slot tree, the per-slot credential file, and
  the armed §4.9 expiry timers that a failed bind leaves behind, in all three residue classes,
  and, where rule 8 (the start-confirmation rule) refuses, take the abandoned session back off the pod's
  shared runtime process.
- Hold the slot identifier from the deregistration of its registry entry until the cleanup that
  reclaims it has completed, and for the life of the pod where that cleanup does not complete, so
  a bind cannot be admitted onto an identifier whose tree is still being torn down or was left
  standing by a cleanup that failed, and guard every section that writes or destroys a slot's
  tree outside the registry lock, the reclaim's own destructive section included, so the fence at the resolve is
  not outrun by work that runs after that lock is released.
- Give the compensation, the hold and the fence each a stated obligation in the specification,
  so the code traces to a section rather than to a convention, and publish the adapter's half of
  the fence to third-party adapter authors as a named rule set §15.4 publishes non-conformance
  against, with a battery that checks it one case per rule.
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
  second lifetime to get wrong. An additive field is no longer a non-goal: `bind_attempt` and
  `unconditional_teardown` travel on `ShutdownRequest`, and they are preconditions on the one
  removal entry point rather than a second one. Splitting `Shutdown` into two RPCs was
  considered for the same reason and rejected: it doubles the published surface, forces every
  caller to choose a method rather than a field, and moves the distinction from a precondition
  to a scope, which §4.1 forbids.
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
- **An `AdapterEvents` event for the compensation's outcome.** The compensation's outcome
  series is a gateway series, emitted from a `Binder` hook beside the existing slot-failure hook
  and scraped by the shipped target set, so no event carries it. The untokened-entry counter is
  an adapter series, because only the adapter observes an entry carrying no token, and its
  §16.1 row records the standing deferral that the adapter process exposes no scrape target
  until a deployer wires one, which is the deferral the channel-naming law's metric half
  already carries. `lenny_slot_failure_total` already labels the stages at these call sites and
  `lenny_adapter_leaked_slots` and the `slothealth` ledger already carry the leak.
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
  `Resume` response, is closed by extending CODE-4 and CODE-13 to `Binder.Resume` and CODE-2's start
  confirmation to the adapter's `Resume` instead.
- **Widening the §5.2 whole-pod scrub to enumerate the registry as its source.** Its on-disk
  enumeration is deliberate and its own comment gives the reason: the residue it must reach
  belongs to a leaked slot whose registry entry is already gone.
- **Splitting the work by residue class into two proposals.** The change touches one map, one
  entry, one `delete`, and one conditional. Two proposals would edit the same branch.
- **Landing the gateway compensation without the adapter's teardown gate.** It would send a full teardown, a
  §15.4.2 drain frame, a `Runtime.Close`, and a `sessionsServed` increment for a session that
  never ran, and it would reach the listener close recorded under `## Defects in the shipped tree
  that this proposal does not stage` (**A pod whose runtime process has been closed cannot serve
  another session.**).
- **Fixing `claimPodMCPStartLocked`'s raw `len(s.slots) != 1` gate.** Named out of scope by
  the problem statement and independently reachable with no failed bind anywhere. It needs
  its own proposal.
- **Retuning the `ceil(maxConcurrentSessions/2)` unhealthy threshold.** Named out of scope.
  The accounting deliverable adds accounting where there is none and changes no threshold.
- **The exclusive path's abandoned-Prepare case**, where a session prepares and never calls
  Launch. Named out of scope by the problem statement; it has a different trigger and a
  different owner. CODE-15's gate changes nothing on that path, because a session that reaches
  a terminal state before it launches is reclaimed from its persisted binding and the adapter
  is sent no `Shutdown` for it (`pkg/gateway/sessionserver/usage.go:446` and `:581-605`). The
  residue that leaves behind is recorded under the defects this proposal does not stage.
- **Re-stating §28.5.3, §15.4.3, or §15.4.2 in the specification.** Their rules are correct as
  written. With the residue removed or the pod retired, none of those predicates is fed a
  residue. Naming them would state a rule change the code does not make and would invite a
  later reader to weaken a count that fails closed on purpose.

## Open decisions for human to make

No decisions remain open for a human: the operator answered the last of them, decisions 30,
32, 33, 34, 36, 50, 56 and 57, on 2026-09-25 (`[operator.decisions-0925]`), and the review log's
`### Settled` list, its standing-context changelog and the review-log archive record how every
entry that has left this section was answered or moved to `## Defects in the shipped tree that
this proposal does not stage`, among them decision 53, whose re-cut of CODE-1, CODE-4 and CODE-6
lands CODE-14 at S15 (its `Shutdown` rows with CODE-1 at S16), CODE-13 at S19, and CODE-15 with
CODE-1 at S16.

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
  counts entries. `deliverToSession`'s rejection of unaddressed session-scoped frames once the
  slot count passes one is the same kind of rule and is deliberate for the same reason. Out of
  scope by the problem statement, the finding that owns it is whether the arming gate should
  count bound entries rather than registry entries, and the persistence of an entry nothing
  collects belongs to the reaper named in the spec-changes file's Edge-cases section.
- **Concurrent `POST /v1/sessions/{id}/start` for one session is not serialized.** `handleStart`
  validates the precondition against the row it read and writes no `starting` state before
  launching, so two concurrent calls both reach `BindReservedSlot` on the same pod with the same
  slot identifier, which equals the session identifier
  (`pkg/gateway/sessionserver/start.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`).
  That is a gateway serialization defect on the shipped path, present before this proposal and
  unchanged by it. The identity gate refuses the loser at the adapter rather than serializing the
  two at the gateway, so one caller spends an attempt on a start the gateway should not have
  admitted twice. The serialization has to be invisible to the client: `ValidTransitions()`
  declares no `{Starting, Ready}` edge, and §7.2 states that pre-attached failures are not
  exposed as session state transitions, so a `ready → starting → ready` compare-and-swap is
  unavailable. The replacement is a bounded claim on the session row, a holder identifier and an
  expiry compare-and-swapped in one short transaction and cleared on every launch-failure
  return, in the style of the transaction-scoped per-pod advisory lock the slot reservation
  already takes, with a migration following the established column-adding pattern. It needs a
  §7.1 statement first and is raised as its own finding rather than staged here.
- **A terminal trigger does not abort an in-flight resume.** `handleDelete` on a `resuming`
  row writes `cancelled` and bumps the coordination generation, and it cancels nothing
  (`pkg/gateway/sessionserver/sessionserver.go`). `handleResume` runs `resumeOnPod` on the
  `/resume` request's own context, and on success it writes `running` through
  `transitionResume` over whatever state the row then holds
  (`pkg/gateway/sessionserver/start.go`). §6.2 already requires the in-flight restoration RPCs
  to be cancelled on that edge, so the gap predates this proposal. SPEC-2's §7.2 step-3 reclaim
  runs through CODE-13's `Binder.Resume` compensation once a re-attach is aborted, so it holds
  in code only when the abort exists. No fix is staged, because the abort, which cancels a
  resume that another request owns and guards the resume commit on the row state, is a §7.2
  snapshot-close change rather than a bind-fence one.
- **Forward-RPC fencing is not closed for the post-bind session RPCs.** The attempt token
  fences the bind sequence and `Shutdown`, which are the requests that carry it. Every
  other gateway-to-pod RPC of a session still addresses it by its identifier with no
  statement of which attempt is asking, so an `Attach`, an `Interrupt`, a `Checkpoint`, an
  `ExportPaths`, a `ReportUsage`, a `RotateCredentials`, or an `ExtendCredentialLease` from
  an abandoned attempt that the gateway stopped waiting for can still reach a successor's
  entry on the same pod. This proposal does not stage that fence: it would mean carrying the
  token on the request of every session RPC and refusing a mismatch on each, which changes
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
  because SPEC-5's staged §4.7.1 block states that where the coordination generation and the
  bind-attempt fields appear on one message, as on `Shutdown`, each is checked on its own terms,
  which makes the
  compensating `Shutdown` subject to the fence rather than exempt from it. Nothing this
  proposal stages depends on the fence being unenforced. A generation-stale rejection is a
  reclaim the adapter did not answer. SPEC-2's §7.1 paragraph counts it as a reclaim not
  acknowledged clean, and SPEC-3's §5.2 disposition table states what becomes of the slot on a pod of
  either concurrency. Either way the §7.1 obligation is to send the reclaim rather than to have it
  accepted. Carving the compensation out of the fence
  would also contradict §10.1's stale-replica rule, which tells a replica that receives a
  generation-stale rejection to cancel its in-flight RPCs for the session and not retry
  (`spec/10_gateway-internals.md:66-68`). No fix is staged because closing the divergence
  means implementing §10.1's fence across every adapter handler, which is a §10.1-scoped
  change on a rule this proposal neither wrote nor widened.
- **`PrepareWorkspace` never validates a later frame's `session_id` against the one it
  resolved.** The handler checks only that each frame carries a non-empty identifier
  (`pkg/adapter/staging.go:68`) and resolves the staging directory once, under the
  `if stagingDir == ""` guard, off the first frame alone (`pkg/adapter/staging.go:77-78`).
  Every frame after that writes into the staging tree the first frame resolved, whatever
  session it names. It is a stream-validation gap on the §4.7 client-streaming upload RPC and
  predates this proposal. CODE-6's first-frame rule latches `bind_attempt` and `mid_session`
  from the resolving frame and refuses no later frame on either field, which is what the
  shipped handler does; a later frame creates and resolves no registry entry, so the attempt
  token's guarantee is unaffected, and the bytes such a frame carries land in the tree the
  resolving frame was already admitted to write rather than in one the sender was refused. No
  fix is staged, because closing it means a per-frame identity comparison on the §4.7 upload
  stream and a wire clause stating it, which is a `PrepareWorkspace` contract change rather
  than a bind-fence one. It is its own finding.
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
  because CODE-15 gates the runtime teardown on `started` and CODE-2's rollback closes a session
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
  CODE-13 retires that swallow and folds the error into `sbe.Leaked`, and CODE-5's
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
  abandoned attempt's own shipped rollback. CODE-6 routes it through `reclaimSlotLocked`, so it
  does take the reclaim hold, and a retry that has not yet staged its tree meets that hold and
  spends an attempt rather than losing a tree. The deletion survives in the ordering where the
  rollback runs after the retry staged, which neither the token nor the hold separates: the
  release compares no token, because it is a session-scoped release rather than a compensation,
  and the tree paths derive from the identifier both attempts share. No fix is staged because
  separating an attempt from its own lagging rollback needs either a generation on the tree or a
  bind-scoped lock spanning the attempt, which is the same ground the spec-changes file's
  Edge-cases section records for an attempt recreating the entry its own teardown removed. This proposal
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
  the trigger is a bind that succeeded, so CODE-13's compensation has no failure site to hang
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
  (`pkg/gateway/sessionserver/start.go:2834-2848`). CODE-13's compensating `Shutdown` adds
  instances of that existing producer rather than a new dependence. Re-deriving the
  occupancy from the adapter is unavailable for any leaked slot, because the adapter's
  only pod-wide entry count is the unexported `slotCount` that the §28.5.3 output
  demultiplexer reads (`pkg/adapter/slotsession.go:398-407`), and no gateway-to-adapter
  RPC returns a pod-wide occupancy count, so the gap is not distinctive to a failed
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

- **§7.1, §7.5. Two shipped paths return 500 after publishing a live binding.** `handleStart`
  runs `registerBinding`, then `acquireCoordinationLease`, then `publishBinding`. The
  `SetupOutput` persistence failure returns `INTERNAL_ERROR` one statement after its own comment
  says the §7.5 trail is best-effort (`pkg/gateway/sessionserver/start.go:1187-1195`), and the
  `transitionStart` update failure returns 500 as well (`:1199-1206`). In both the row stays
  `ready` with the binding published, so the client's retry passes the precondition and reaches
  the bind sequence against a session that is already live. The first site should log and
  continue, matching `applyFinalizePrepareResult`; the second wants a bounded retry with an
  operator-tunable bound, which is a §11.3 timeout-table row. `rollbackBinding` must not be
  called at either site: it releases neither the coordination lease nor the `podRegistry` entry,
  and its one shipped call site runs before `publishBinding`. No spec change is needed, and it is
  raised as its own finding.
- **§7.3, §6.2. A `Resume` refused with `codes.Unavailable` takes the row terminal.** The refusal
  is wrapped by `fmt.Errorf` (`pkg/gateway/podlifecycle/podsession/binder.go:1629`) and matches
  no arm of `isTransientPodClaimError`, so `holdOrFailOnResumeError` calls `failSession` while
  the workspace and the checkpoint are intact. `writePodClaimError`'s default arm meanwhile
  answers the client a retryable 503 `RESUME_FAILED`. §7.2's session state machine and §6.2's
  `resuming` failure transitions return a failed re-attach to `awaiting_client_action`, so the
  classifier, rather than the spec, is what disagrees. CODE-5's
  arms cover the refusals this proposal introduces, including the started-session refusal, which
  CODE-6 moves off `codes.Unavailable`; what remains is `codes.Unavailable` from the SDK-warm
  different-session arm and every other resume cause no arm recognises. The converse
  disagreement also remains: on a snapshotless concurrent resume, a reclaim hold that exhausts
  the slot retry at a stage other than the setup-command stage, or a start-confirmation rollback
  that exhausts it, answers the client 422 `SLOT_FAILED`
  through `writeSlotFailed`, while CODE-5's `Aborted` arm holds the row in
  `awaiting_client_action`. CODE-5 converges with that fix rather than replacing it.
- **§5.2, §12.4. The `active_slots` reservation is released on a failure the client will retry.**
  Retaining the create-time reservation across such a failure needs the classifier corrected
  first: `SlotBindError.Reason()` returns `SlotReasonTransient` on its `default` arm by
  deliberate design (`pkg/gateway/podlifecycle/podsession/slotfailure.go:76-102`), so a rule
  keyed on "transient means retain" retains on every unrecognized failure, which leaks
  occupancy forever. The rule has to key on an explicit allowlist and treat an unrecognized
  classification as release-and-record. It needs a §5.2 statement and depends on the slot-scoped
  release below.
- **§6.2, §5.2. The pre-running collector deletes a claim covering live siblings.**
  `terminalReclaimPreRunning` (`pkg/gateway/sessionserver/usage.go:581-605`) routes into
  `Binder.ReclaimClaimed`, which calls `podclaim.DeleteClaim` on the whole-pod claim, so on a
  concurrent pool it retires a pod hosting live sibling slots and never decrements the Redis
  counter. A concurrent-pool reclaim should route through `SlotClaimer.ReleaseSlot`.
  `isPreRunningClaimState`'s exclusion of `starting` is deliberate and its own doc comment gives
  the reason, so that exclusion is not part of the defect. It needs a narrow §6.2 statement.
- **No spec change. `SlotClaimer.ReleaseSlot`'s callers have no slot-scoped release.**
  `ReleaseSlotReservation` calls `claimer.ReleaseSlot(ctx, sandboxName, false, false)`
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:502`), so every release is a bare pod-wide
  decrement any caller can issue for any slot. `ReclaimClaimed` has no slot branch and
  `sessionstore.Session` carries no slot identifier, so the scoping has to be threaded rather
  than read. It is a prerequisite of the collector fix above.
- **§7.1, §10.1. Cross-replica `/start` serialization, and the lease hoist that must not be
  taken.** `acquireCoordinationLease` acquires under `s.replicaID`, and the sweeper renews any
  session the replica holds or can adopt, so hoisting the acquire ahead of `launchOnPod` leaves a
  failed launch holding the lease on a row still in `ready` and every retry elsewhere fails with
  `ErrHeld` until the 300-second ready watchdog drives the row terminal. The acquire is also a
  silent no-op when the lease store is nil. It needs a §7.1 or §10.1 statement, and it is the
  cross-replica half of the serialization defect recorded above.
- **§15.1. Adapter error codes have no catalog row.** `DEADLINE_EXCEEDED`,
  `DELEGATION_BUDGET_UNAVAILABLE`, `DELEGATION_DENIED`, `INVALID_WORKSPACE_PLAN` (§15.1 carries
  `WORKSPACE_PLAN_INVALID`, a different string), `MAX_DELEGATION_DEPTH_EXCEEDED`,
  `PLATFORM_DEGRADED`, `PROTOCOL_VERSION_INCOMPATIBLE`, `RUNTIME_OPTIONS_INVALID`,
  `SESSION_NOT_FOUND` and `TOKEN_BUDGET_EXHAUSTED` have no §15.1 row, more have no row in
  `docs/reference/error-catalog.md`, and `DELEGATION_DENIED`, `INVALID_WORKSPACE_PLAN` and
  `MAX_DELEGATION_DEPTH_EXCEEDED` appear nowhere in `spec/` at all. The codes this proposal
  mints are adapter-contract codes the gateway consumes, so they take no §15.1 row either,
  following the `PROTOCOL_VERSION_INCOMPATIBLE` precedent of an adapter `ErrorCode` the
  specification publishes to adapter authors with no §15.1 row; the two codes minted here are
  stated in §4.7.1. The pre-existing gap is its own finding.
- **No spec change. Nothing reconciles §15.1's error catalog against
  `docs/reference/error-catalog.md`.** The published `SETUP_COMMAND_FAILED` row sits at
  `docs/reference/error-catalog.md:129`, and no test, script, or Makefile target in the tree
  references that page: `grep -rn error-catalog tests/ scripts/ Makefile` returns nothing, while
  the tier-11 battery does reconcile other reference pages against the spec, among them
  `adapter-contract.md`, `state-machines.md`, `metrics.md`, `wire-artifacts.md` and
  `glossary.md`. The published catalog can therefore diverge from the spec catalog with no gate
  observing it, which is how the missing and misspelled rows recorded in the entry above
  survived. This proposal stages no gate and edits no row of the page. Building the
  reconciliation over the whole page belongs to a finding against §15.1.
- **No spec change. `Server.ReportSessionFailure` has no production caller.** Every reference
  outside `pkg/gateway/sessionserver/failure.go:115` is a test, so no outcome should be rested on
  it. Recorded so a later reader does not route a terminal disposition through it.
- **§4.6.1. A pod claimed and released between two reconciles loses its `claimed → draining`
  retirement.** The occupancy projection decides a released pod's fate from the phase the pod is
  observed in once the claim is already gone: `ProjectOccupancyPhase` returns `Draining` only for
  a pod observed as `Claimed`, `Idle` for one observed as `Reserved`, and no phase at all for
  every other value, which leaves the pod where it was
  (`pkg/controller/warmpool/occupancy.go:128-143`). A bind that claims and releases a pod between
  two reconciles therefore never takes the retirement edge, because `claimToSandbox` maps every
  claim event to one reconcile request keyed on the owning Sandbox, so the claim CREATE and the
  claim DELETE coalesce onto a single dequeue (`occupancy.go:281-291`), the per-pod `SandboxClaim`
  carries no production finalizer, and `podclaim.DeleteClaim` is an unconditional delete
  (`pkg/gateway/podlifecycle/podclaim/claimer.go:322-330`). The pod then stays in inventory as an
  ordinary idle candidate: `SlotClaimer.ClaimSlot`'s pass 2 filters on `Status.Phase == Idle`, the
  absence of a live claim, and `expiredByUptime` alone, with no used-pod guard
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:336-357, 470-497`). The condition is
  pre-existing, and nothing staged here reads or writes the claim release path: SPEC-4 aligns
  §4.6.1's prose with what `occupancy.go` already does and changes no mechanism. Two candidate
  fixes exist. A gateway-side hold keeps the claim in existence until the pod projects `claimed`
  before deleting it. A durable disposition recorded on the claim status through
  `podclaim.WriteDispositionStatus` (`pkg/gateway/podlifecycle/podclaim/bindingstate.go:239`)
  closes the window only where the claim outlives the projection's read of it. Both sit in
  components this proposal does not open, so the fix wants a problem statement of its own against
  §4.6.1 and is raised as its own finding rather than staged here.
- **No spec change. A per-slot cleanup whose runtime close succeeds and whose later act fails
  leaves a residue that no disposition names.** The shipped `Shutdown` handler discards the
  slot-tree removal error (`pkg/adapter/session.go:271`) and derives the cleanup outcome from the
  close error alone (`pkg/adapter/sessionscrubreporter.go:39-44`), so a failure to remove the
  slot's credential directory or its workspace tree has never produced a `leaked` report. Row 3
  of SPEC-3's disposition table records that behaviour, and SPEC-3 deletes the shipped statement
  that a failed cleanup leaks the slot, so the withdrawal takes back a control the code never
  exercised. Such a failure therefore reaches no `leaked` disposition, no
  `lenny_adapter_leaked_slots` series and no drain request, and is visible only in the adapter's
  `slot_tree_removal_failed` log line and, on the pre-`running` arm, in the clean-exit flag on the
  `Shutdown` response. The residue is reclaimed at the occupancy-zero whole-pod scrub, which
  enumerates the credential files and the workspace trees from the on-disk children of the slots
  containers and so reaches a slot whose registry entry is already gone
  (`pkg/adapter/podscrub.go:132-156`); this arm reports `released`, so the pod's occupancy still
  reaches zero and the scrub is reachable. Re-keying the report on both errors is rejected on the
  ground CODE-15's paragraph 'The report stays keyed on `closeErr`' gives. Carrying the signal
  some other way wants a mechanism nothing here stages. One companion question is unverified and
  would reverse this record if it were answered the other way: whether a `credentials.json` left
  behind by a failed cleanup on a pod serving concurrent sessions is reachable by a later session
  through the pod-global runtime process that serves every registered slot
  (`pkg/adapter/slot.go:219-225`). That is an isolation question against §13, it wants a problem
  statement of its own, and it is raised as its own finding rather than staged here.
- **A slot's §4.9 lease-expiry timers are cancelled before the credential file they protect is
  removed.** The shipped release runs the deregistration first, and the deregistration cancels
  every armed direct-mode expiry timer on the entry
  (`pkg/adapter/slotsession.go:158-161`, `pkg/adapter/slotsession.go:174-181`). The removal of the
  slot's credential directory runs afterwards and is best-effort: `Shutdown` reaches
  `_ = removeSlotTree(st)` at `pkg/adapter/session.go:271` after deregistering at
  `pkg/adapter/session.go:238` and discards its error, `releaseSessionSlot` runs the same two steps
  in succession (`pkg/adapter/slotsession.go:214-219`), and the §10.1.4 hold-timeout termination
  deregisters in its first pass (`pkg/adapter/holdstate.go:190`) and removes the tree in its second
  (`pkg/adapter/holdstate.go:254`). A removal that fails therefore leaves
  `/run/lenny/slots/{sessionId}/credentials.json` on the pod with no timer armed against it. That
  matters in direct delivery mode, where `spec/04_system-components.md:1169` makes the in-pod timer
  the enforcement point for a key that does not itself expire, and where §4.9's extension rule
  states that the adapter expiry timer is the enforced lease deadline. Proxy delivery mode is
  enforced server-side and is unaffected. The order is the shipped one on every release path and
  this proposal changes none of it: SPEC-3's §5.2 action list names both acts in one sentence
  because both are shipped adapter behaviour, and CODE-1, CODE-15 and CODE-6 keep the deregistration and the
  tree removal in the order the tree already runs them. The residue is reclaimed at the
  occupancy-zero whole-pod scrub, which removes the credential file from disk
  (`pkg/adapter/podscrub.go:139-149`). Reordering the two acts, so the file is gone before its
  timer is disarmed, is a change to the release paths' own sequencing rather than to the bind
  compensation this proposal states, and it is raised as its own finding rather than staged here.
- **The REST classification map in `errorclassify.go` carries two spec citations in the retired
  line-number form.** `"CONFIRMATION_REQUIRED": {CategoryPermanent, false}, // spec: 15:1105` and
  `"SETUP_COMMAND_FAILED":  {CategoryPermanent, false}, // spec: 15:1106` sit at
  `pkg/gateway/externalapi/errorclassify/errorclassify.go:475-476`. Naming law N8 retires the
  line-number citation form and requires a citation to name a heading. The two lines predate this
  proposal by three months (commit `985e4b231`, 2026-06-24). This proposal edits neither them nor
  the doc comment above the map, so it introduces no further instance. Their conversion belongs to the repository's
  line-citation migration, which owns the form across the tree; the bare `NN:LINE` spelling is one
  the citation grammar's reference half does not match today
  (`scripts/specshift/citation/grammar.go:120-122`), so no tier-0 gate reads these two lines. The spelling occurs 125 times across `pkg/`, `cmd/` and
  `tests/`, so converting the two lines in this file would open a tree-wide sweep that no
  deliverable here is scoped to.
- **A queued waiter's wait bound does not cover the time it spends behind the FIFO head.**
  `waitInQueue` admits only the FIFO head and blocks every other waiter on its ticket
  (`pkg/gateway/sessionserver/queue.go:185-192`), and it tests `maxQueueWaitSeconds` after the
  ticket is received rather than while the waiter is behind the head
  (`pkg/gateway/sessionserver/queue.go:195-202`, default 30 seconds at
  `pkg/gateway/sessionserver/queue.go:18`), so a waiter's wall-clock wait is its own bound plus
  the duration of every attempt ahead of it. The head's attempt is the closure `runWithQueue`
  runs (`pkg/gateway/sessionserver/start.go:2606-2608`), and on the shipped path that attempt can
  already occupy the head for an uncapped setup phase, because `SetupPolicy.timeout_seconds` of
  zero means no aggregate cap (`schemas/lenny-adapter.proto:941-943`). The increment CODE-13's
  compensating `Shutdown` adds to the head's attempt is accepted in the non-spec changes file's
  `## Edge cases and accepted failure modes` entry **A compensating `Shutdown` holds the pool's
  queue head for its own budget.** No fix is staged. Making the bound cover the whole wait is a
  change to queue admission on a path this proposal does not open.
- **A failed compensating drain leaves the pod holding the dead attempt's registry entry.**
  `Binder.failPhase` logs the error and continues when `b.drain` fails
  (`pkg/gateway/podlifecycle/podsession/binder.go:1079-1081`), and that drain is the only act on
  the path that retires the pod, so a pod whose claim DELETE failed survives carrying the slot
  registry entry the failed bind created. Under the staged design that entry is stamped with the
  dead attempt's token, so the attempt-identity refusal `SLOT_BIND_ATTEMPT_SUPERSEDED` turns away
  every later attempt at the same session on the same pod until something removes it. CODE-8
  makes both typed refusals return before `failPhase`, so the residue requires an ordinary bind
  failure whose own drain then fails as well. The window is bounded rather than permanent: the
  §4.6.1 orphan-claim collector drains a `Bound` claim aged past the orphan timeout once Postgres
  reports no live session on the pod (`pkg/controller/warmpool/gc.go:227-246`,
  `pkg/controller/warmpool/gc.go:274-289`), so the pod retires and the entry goes with it, and on
  a pod still serving a co-tenant that wait lasts as long as the co-tenant does. No fix is
  staged. Collecting the entry sooner means a reaper on the adapter's own side that removes a
  registry entry no live attempt owns, a mechanism this proposal states nowhere, whose owner is
  named in the spec-changes file's Edge-cases section.

- **§6.2 attributes `lenny_adapter_leaked_slots` to the adapter, and the gateway emits it.**
  The **`leaked` slot semantics** paragraph states that the adapter exposes a `leaked_slots`
  count in the pod's health metadata, the `/healthz` response and the gauge. The adapter's
  `/healthz` carries no such count, and the gauge is registered and set by the gateway from its own
  slot records (`adapterLeakedSlots` in
  `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go`, set from
  `applySlotRetryPolicy` in `pkg/gateway/sessionserver/start.go`). SPEC-6's §16.1 row names the
  series without naming its emitter, so it does not repeat the attribution. Correcting §6.2 is a
  separate spec change.
  Each gateway replica sets the gauge from its own in-process `slotstate.Registry`, so the series
  is per replica and does not yet carry §6.2's persistent per-pod count.

- **§7.1, §6.1. Two concurrent `/finalize` calls for one session both run, and on an SDK-warm pod
  the loser's `DemoteSDK` can end the winner's live session.** `handleFinalize` checks its
  precondition against a row it read before any lock, `transitionFinalizing` writes `finalizing`
  without checking the current state (`pkg/gateway/sessionserver/sessionserver.go`), and
  `pgstore.Update` locks the row without validating the transition, so both calls run
  `Binder.Prepare` against the session's pod, each on its own adapter connection. `DemoteSDK`
  names no session and releases whichever entry the registry holds (the `DemoteSDK` handler in
  `pkg/adapter/sdkwarm.go`). `Binder.Prepare` sends it when the request sets `PreConnect` and the
  plan touches one of the SDK-warm blocking paths, so a losing call whose demotion reaches the pod
  after the winner's
  `Prepare`, its `ready` commit and its launch closes the live session's runtime and releases its
  entry. The losing call can also overwrite the row's state, fail the session, or drain the shared
  pod. The remedy is a compare-and-swap on the transition into `finalizing`, which proposal 0082
  stages.

- **§5.2. The exhaustion error names no code value or status.** §5.2's
  `**Client error on exhaustion:**` bullet states the fields of the error a client receives when
  a slot on a pool serving concurrent sessions fails and is not retried, and names neither a code
  value nor an HTTP status. The gateway returns 422 with the code `SLOT_FAILED` on that path
  (`writeSlotFailed` in `pkg/gateway/sessionserver/start.go`), and no file under `spec/`, `docs/`
  or `schemas/` defines the code. No staged sentence depends on either value. Naming them is a
  separate spec change.

- **§6.2, §7.2, §15.1. The spec sites that name a `/finalize` failure envelope disagree about a
  workspace envelope.** §6.2's pre-attached **Client visibility** bullet says
  `POST /v1/sessions/{id}/finalize` returns "the workspace-validation, setup-command, or credential
  error per the §15.1 finalize precondition note", and the §15.1 finalize precondition row names a
  credential envelope (`CREDENTIAL_POOL_EXHAUSTED`) and a setup-command envelope
  (`SETUP_COMMAND_FAILED`) and no workspace envelope, so the citation attributes to that row
  something the row does not state. The other sites disagree about which side is wrong. §7.2's
  pre-attached failure-visibility paragraph states that a workspace-materialization failure
  surfaces as `WORKSPACE_PLAN_INVALID` at `/finalize`, which corroborates §6.2. The §15.1 error
  catalog's `WORKSPACE_PLAN_INVALID` row scopes that code to the inner `workspacePlan` payload on
  `POST /v1/sessions` failing schema validation and calls it "Reserved for inner-plan schema
  failures only". The gateway sides with the catalog: every non-test writer of the code is a
  request-validation path (`pkg/gateway/sessionserver/start.go`,
  `pkg/gateway/sessionserver/sessionserver.go`), the finalize handler emits no workspace envelope,
  and `writePodClaimError`, the mapper finalize routes bind failures through, has no workspace
  branch; the slot path's `workspace_validation` classification
  (`pkg/gateway/podlifecycle/podsession/slotfailure.go`) reaches the client as `SLOT_FAILED`. The
  condition predates this proposal and is unrelated to the bind-attempt mechanism, and this
  proposal edits none of those sites, by operator decision 30. Settling it is a spec change across
  §6.2, §7.2 and both §15.1 tables, whose correct direction is not yet established, and proposal
  0083 takes it up.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0080 (inventory of the residues 0073 recorded and deferred) | Draft, stages no changes. Its own `Date` line reads 2026-08-31 and the last commit to the file is 2026-09-06, which records when someone touched the file rather than when it was reviewed. It heads itself an unconverged inventory rather than a design. | **§1.2 is the entry this proposal promotes, and it is discharged in its entry-removal half.** The staged reclaim removes the registry entry for both of §1.2's classes, together with the per-slot tree, the slot's credential directory and the armed §4.9 expiry timers, so the held inbound count and the pinned drain gate lose their subject. §1.2's third clause survives with a different cause and moves to §1.4. **§1.4 is made worse by a leaked entry, and this proposal records that rather than fixing it.** `claimPodMCPStartLocked` returns false whenever the pod's entry count is not one (`pkg/adapter/slotsession.go:109`), and the only entry-removal site is `deregisterSlotLocked`, whose §10.1.4 sweep caller filters on `st.started` and so cannot collect an unstarted entry. `writeSessionManifest` mints a fresh nonce before the arming guard is consulted, so every later session on that pod reads a manifest advertising a socket no running server authenticates. `deliverToSession`'s rejection of unaddressed session-scoped frames once the slot count passes one has the same cause. Both gates are deliberate co-tenancy rules; the defect is that the entry never goes away. This proposal removes the entry on every path it compensates, which restores the arming wherever the failed bind caused the loss, and changes neither gate. The entry that survives an unanswered compensation is not collected here, and the reaper that would collect it is named under the accepted failure modes. **The claim-register counts §1.12 and §1.18 quote both go stale, and by how much is determinate.** `tests/claim-map.json` carries 76 rows today, 20 `ABSENT`, 24 `UNWIRED` and 32 `WIRED`. This proposal adds exactly three rows to the `EXPLICIT` list in `scripts/seed-claim-register.py`, SCHEMA-1's two `WIRED` for the new wire contract and CONF-1's one `ABSENT` for the absent third-party conformance harness, which takes the register to 79 rows, 21 `ABSENT`, 24 `UNWIRED` and 34 `WIRED`. §1.12's heading and opening sentence ("Twenty of the register's seventy-six rows") and §1.18's tally ("seventy-six rows, thirty-two are `WIRED`, twenty are `ABSENT`") are both restated from those figures, and the `UNWIRED` count alone is unchanged. §1.12's note that the adapter metric names row is naming-law N4's metric half under `deferral_id: R12` gains a member rather than losing one: CODE-9's untokened-entry counter is registered in `pkg/adapter/metrics.go` and carries the same deferral, because the adapter process still exposes no scrape target. **§1.19 keeps its class set and moves only membership.** `boundSlotState` and `checkSessionBound` are untouched. A fence for a session whose bind failed and whose reclaim's cleanup completed meets the absent-entry refusal rather than the unbound-entry refusal, and so does a fence for a session whose `StartSession` or `Resume` rolled back because the reclaim landed after its claim. After a reclaim the adapter did not acknowledge, a surviving entry is present-and-unbound or present-and-bound for a session the gateway has abandoned, so a fence in that window meets the unbound-entry refusal or neither refusal. The two new refusals are outside §1.19's inventory for the reason the hold's refusal already is: they are raised inside `ensureSlotStateLocked` with `codes.Aborted` and `codes.FailedPrecondition` on the bind-sequence and workspace RPCs, and carry neither the status code nor the RPC §1.19 is scoped to. **§1.7 keeps its subject and grows the spec side of its divergence.** `slothealth.UnhealthyThreshold`'s clamp of a sub-1 denominator to 1 is unmodified and `drainLedger.RecordLeak` gains only a slot key, and no staged code produces a `leaked` disposition on an exclusive pod: the §7.3 re-attach accounting is gated on a non-empty slot id, which an exclusive pool never reserves, and the other accounting callers sit behind `maxConcurrentSessions > 1`. SPEC-2's §7.1 paragraph and SPEC-3's §5.2 scrub-model append each restate that the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy, and each cites SPEC-4's §6.2 paragraph for the exclusive-pod case rather than stating a disposition of its own. **§1.1, §1.3, §1.5, §1.16 and §1.20 have no conflict of subject.** The adapter edits are placed in `session.go`, `runtimegeneration.go`, `resume.go`, `sdkwarm.go`, `server.go`, `staging.go`, `slotcreds.go`, `credentials.go`, `slotsession.go`, `slot.go`, `holdstate.go` and the new `bindattempt.go`. `slot.go` and `slotsession.go` are opened because `ensureSlotStateLocked` stamps the token, raises both refusals and refuses the hold, `slotState` gains a `bindAttempt` field, and `reclaimSlotLocked` sits beside `deregisterSlotLocked`. `server.go` declares the reclaim-hold set and the per-slot guard table, `staging.go`, `slotcreds.go` and `credentials.go` move their resolve sites to the shared helper and pass what their RPCs assert, and `holdstate.go` gives `terminateHeldSession` a slot-guard acquisition with two deferred releases and moves the §10.1.4 pass's ten-second budget from one shared close context to a guard-acquisition deadline plus a per-member close context. Every edit outside `holdstate.go` is small and localised, so a later position's rewrite of those files re-lands a hold set, a stamp, two refusals, the moved resolve sites, a struct field and a helper rather than a mechanism. The `holdstate.go` edit is a mechanism, and the action column states what re-landing it requires. | Re-derive four entries when 0080 is triaged into successor proposals. Split §1.2 so its MCP-arming clause moves to §1.4 as a gap this proposal does not take, and record the rest of §1.2 as discharged here. Re-derive §1.4 against the leaked-entry cause and the reaper it waits on. Re-derive §1.19's membership against the remaining cases, its class set being unchanged. Re-derive §1.7 against four spec sites rather than the two §6.2 and §5.2 sites it names. Restate §1.12's and §1.18's counts from `tests/claim-map.json` once this proposal's three rows have landed, and add the untokened-entry counter to §1.12's list of surfaces waiting on the adapter metrics endpoint. Re-land the stamp, the two refusals, the hold refusal, the `slotState.bindAttempt` field and the `reclaimSlotLocked` helper when the rewrite of `slot.go` and `slotsession.go` happens, and the reclaim-hold set, the moved resolve sites and `terminateHeldSession`'s guard acquisition, its two deferred releases and the per-member close-context split of the §10.1.4 pass's ten-second budget when the rewrite reaches `server.go`, `staging.go`, `slotcreds.go`, `credentials.go` and `holdstate.go`. No edit to 0080's staged content, because it stages none. |
| 0073 (give every session a slot) | Implemented (2026-08-31 per its own status line; spec applied 2026-08-19) | This change touches 0073 in two ways. 0073 recorded this gap in its §9 recorded limits and declined to discharge it, and this proposal discharges it. SPEC-1 also retires the third sentence of §4.1's `ShutdownRequest` paragraph, which 0073's SPEC-7 authored and which reached `spec/04_system-components.md:157` in commit `f37e867b8`. That sentence states one per-session teardown gated on a bound entry; after the split there are two teardowns with two preconditions, and the slot release is gated on the entry being present. The paragraph's first two sentences, its session-scoped classification, and its closing rule are preserved. SPEC-4 and CODE-3 extend 0073's per-slot sub-state fence (`spec/06_warm-pod-model.md:150-155`) and its `pkg/sandbox/slotstate` edge list with one new edge, which adds to that list rather than retracting from it. CODE-14 also replaces the single close context 0073's coordinator-hold timeout landed (`pkg/adapter/holdstate.go`, commit `3997f502b2`), as CODE-14's **`terminateHeldSession`'s guard and close context.** states. | Nothing. A landed proposal is not edited, so this row is the record of what this proposal takes back from 0073. |
| 0075 (derive message scope from the address type) | Implemented (2026-09-08 per its status file's `implemented-date`) | 0075's SPEC-1 replaced the §4.1 block around the `ShutdownRequest` paragraph and reserved the paragraph itself, stating that it "stands unedited" because it explains a divergence between what a request addresses and what its handler touches that 0075's D3 rests on. SPEC-1 here rewrites that paragraph's third sentence. The ground D3 rests on survives: the first two sentences carry the session-scoped classification and the single address unchanged, and the replacement keeps the whole-pod-scrub clause and the closing rule that no operation is selected by a field's presence standing in for a scope. Nothing 0075 landed is opened, including the derivation rule at `spec/04_system-components.md:151-155`, its tier-0 addressing gate, its tier-3 session-address suite, and its `tests/spec-map.json` entries. The new `ShutdownRequest.bind_attempt` and `ShutdownRequest.unconditional_teardown` fields do not disturb that rule: both are bare scalars present on every `ShutdownRequest`, as the `coordination_generation` fence on the same message already is, so no operation is selected by a field's presence. SPEC-1 restates the justification per message rather than stating a precondition in §4.1, and the §4.7 `Shutdown` row carries every precondition. | Nothing. |
| 0078 (keep the pod's runtime listener across a session teardown) | Draft for review (2026-08-25 per its own `Date` line; the file's last commit, `9589aea54`, carries the same date, which records when someone touched it rather than when it was reviewed) | No deliverable of 0078 loses its subject. CODE-1, CODE-2, TEST-1 through TEST-7 and DOCS-1 all keep theirs, because this proposal opens neither `pkg/adapter/socketruntime.go` nor `cmd/lenny-adapter/main.go`. The file collisions are real, and all of them are in test files. The staged co-tenancy-hazard case adds a sibling assertion in `pkg/adapter/socketruntime_test.go` beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (`:252`), inside the block 0078's TEST-1 through TEST-4 and their cleanup conversion at `:258` and `:316` rewrite. 0078's CODE-1 replaces the `return p.listener.Close()` at `pkg/adapter/socketruntime.go:467` that the assertion's listener half records, so that half stops discriminating once 0078 lands, while its connection-close and child-kill halves stand and this proposal's own CODE-15 `started` gate is still required. This proposal also extends `tests/tier4_integration/concurrent_workspace_test.go`, whose cleanup at `:126` 0078's TEST-5 converts. And it edits `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`, the file 0078's TEST-7 rewrites: the rule-10 row of the files-touched **The admission-rule test migration.**, which CODE-1's two-field precondition forces, adds `UnconditionalTeardown: true` to every `ShutdownRequest` literal that row requires, a recycle request included, so `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` (`:232`) and `TestShutdownDrainRacesAnIncomingSession_spec_6_4` (`:316`) take that field at their `ShutdownRequest` literals (`:269`, `:332`, `:373`, `:453`), and that table's rule-1 row, which CODE-6's rule 1 forces, adds a non-empty `BindAttempt` at the `FinalizeWorkspaceRequest` literals inside the second of those tests (`:327`, `:460`). Both tests must keep passing. The two proposals' edit regions in that file are distinct, so the collision is a merge hazard rather than a design conflict. And it edits `tests/tier4_integration/concurrent_delegation_proxy_test.go`, whose cleanup at `:168` 0078's TEST-5 converts: the `ShutdownRequest` literal at `:424` takes the same `UnconditionalTeardown: true` edit and the `AssignCredentialsRequest` call at `:194` moves ahead of the `StartSession` at `:186` under that table's rule-6 row and takes the rule-1 row's non-empty `BindAttempt`, and the edit regions are distinct, so that collision is a merge hazard as well. The tier-4 assertion that the pod's listener survives holds on the shipped tree independently of 0078, because alice is still active when bob's compensation runs and `Close` takes the sibling early return (`pkg/adapter/socketruntime.go:441-446`). The row previously stated that 0078 widens the exposure by making a pod serve more sessions. 0078's own fixed decision states the opposite, that after it lands a recycling sidecar pod still serves one session and fails at the accept timeout, so that premise is withdrawn. | Land after this one. Preserve the `unconditional_teardown` field on those `Shutdown` calls, and the `bind_attempt` field on the two `FinalizeWorkspace` calls in the same fixture, when TEST-7 rewrites the fixture's accept-timeout bound and the sequenced-leg assertion, because after this proposal lands the adapter answers a `Shutdown` carrying neither selector, and a `FinalizeWorkspace` whose bind-attempt token and mid-session flag are both unset, with `INVALID_ARGUMENT` and performs nothing. Amend the sibling assertion's listener half when 0078's CODE-1 removes the listener close. |
| 0072 (correct the inconsistencies the scenario authoring surfaced) | Draft for review (2026-08-13 per its own `Date` line; the file's last commit, `57427ee5f`, is dated 2026-08-26, which records when someone touched it rather than when it was reviewed) | **No deliverable of 0072 loses its subject.** SPEC-1, SPEC-2, SPEC-4 through SPEC-8 and CODE-1 through CODE-3 all keep theirs, because this proposal changes no checkpoint quiescence timeout, no Basic-level checkpoint statement, no eviction retry budget, no `awaiting_client_action` entry-path enumeration, no upload route, no metric label domain and no naming matcher. SPEC-5's premise in particular stands: it rests on §7.2's state-transition list, and this proposal's §7.2 edits are confined to the mid-resume snapshot-close block (a preamble sentence, a sentence appended to step 2, and step 3 replaced), leaving that list untouched. **Seven files are opened by both proposals, and every collision is a merge hazard rather than a design conflict.** Four of them collide inside the same section. In `spec/07_session-lifecycle.md` §7.3, 0072's SPEC-5 rewrites the `awaiting_client_action` **Entry paths** bullet (`spec/07_session-lifecycle.md:432`; 0072 cites `:431`, one line of drift) while SPEC-2 here appends a paragraph after the numbered list under `**Resume flow after pod failure:**` (`spec/07_session-lifecycle.md:402-414`). In `spec/15_external-api-surface.md`, 0072's SPEC-6 names the mid-session upload route in §15.1's endpoint and precondition tables, which this proposal does not edit (its one §15.1 edit is a pointer sentence appended to the error catalog's `SETUP_COMMAND_FAILED` row); in §15.4, 0072's SPEC-2 corrects the §15.4.3 integration-level text and its SPEC-8 corrects the §15.4.2 handshake sentence while SPEC-5 here adds a published bind-attempt block after the SDK-warm demotion contract. In `spec/16_observability.md` §16.1 and `docs/reference/metrics.md`, 0072's SPEC-7 adds the `reason` label to the `lenny_checkpoint_storage_failure_total` row (`spec/16_observability.md:203` and `docs/reference/metrics.md:193`; 0072 cites `:201` and `:191`, two lines of drift each) while SPEC-6 and CODE-9 here add one row per counter CODE-9 emits to those same two tables. The remaining three files are shared at distinct sections: `spec/04_system-components.md` (0072 in §4.4.3 and §4.4.5, this proposal in §4.1, §4.6.1, §4.7, §4.7.1 and §4.7.9), `spec/29_communication-scenarios.md` (0072 in §29.9, this proposal in §29.4), and `docs/reference/adapter-contract.md` (0072's SPEC-8 in the `Version Negotiation` section, now at `docs/reference/adapter-contract.md:446-452` against the `:426` it cites, and DOCS-2 here in the RPC table's `DemoteSDK`, `Shutdown` and `ReportSessionScrub` rows at `:64`, `:75` and `:81`). Nothing here falsifies 0072's §1.6: the §7.4 upload pair sets `mid_session` on its `PrepareWorkspace` (`pkg/gateway/sessionserver/upload_to_session.go`), which is the mid-session route SPEC-6 documents. | Land in either order and resolve the shared files as a merge, re-running the tier-11 documentation reconciliation after the second lands. No content of 0072 needs re-deriving. Re-anchor 0072's four drifted citations (`spec/07_session-lifecycle.md:431`, `spec/16_observability.md:201`, `docs/reference/metrics.md:191` and `docs/reference/adapter-contract.md:426`) against the tree before it is applied, whichever proposal lands first. |
| 0079 (name who starts the next session's runtime on a recycled pod) | Draft for review (2026-08-25 per its own `Date` line; the file's last commit, `a5bf9db26`, carries the same date, which records when someone touched it rather than when it was reviewed) | **No deliverable of 0079 loses its subject.** SPEC-1 through SPEC-8, CODE-1 through CODE-4, TEST-1 through TEST-8 and DOC-1 through DOC-4 all keep theirs, because this proposal names no creator of a runtime process, stamps no pod label, opens `pkg/sandbox/podscrub` nowhere, edits `pkg/adapter/socketruntime.go` not at all, and under `pkg/controller/sandbox` opens no file for a mechanism edit, while 0079's CODE-1 and TEST-3 stage `controller.go` and `controller_test.go`. A file CODE-10 or CODE-12 alone opens is opened for comment prose only and does not count toward this row's file-collision ground, which is the rule the non-spec `## Files touched on application` list states for those entries. **Five spec files are opened by both proposals, and every collision lands on a different anchor.** In §4.7.9, 0079's SPEC-1 replaces step 7 while SPEC-2 here replaces step 5 and states that no other part of §4.7.9 changes. This proposal's other `spec/04` anchors (§4.1's request-message-scope sentence, §4.6.1's projection-list opening sentence and its two claim-deletion bullets, the §4.7 `Shutdown`, `DemoteSDK` and `ReportSessionScrub` rows and the new §4.7.1 bind attempt token block) are ones 0079 opens nowhere, and 0079's §4.7.10 runtime-process-lifetime paragraph and trade-off row (its SPEC-2) are untouched here. In §5.2, 0079 appends to the scrub procedure after step 6 and adds a sentence to step 1 (SPEC-3), replaces the **Recycling and integration levels** paragraph (SPEC-4) and edits the sizing text (SPEC-5), while SPEC-3 here replaces the `**Scrub model.**` paragraph's opening sentence and appends to that paragraph, and replaces the `**Slot cleanup:**` bullet's action-list, reporting and leaked-outcome sentences, and SPEC-5 appends one sentence to the `**Client error on exhaustion:**` bullet. In §6.2 both edit the same fenced state machine at different entries: SPEC-4 here reduces the `claimed ──→ draining` trigger list in the `Occupancy projection` group and the claim-existence clauses of the projection prose to pointers at §4.6.1, leaving the `Recycle edges` group untouched, while 0079's SPEC-6 qualifies the `claimed ──→ sdk_connecting` and `claimed ──→ reserved` edges in that group, adds a new `claimed ──→ draining` edge after the vm-restart drain edge, and appends to the §6.1 preConnect row. In §15.4, SPEC-5 here inserts its two blocks after the SDK-warm demotion contract (`spec/15_external-api-surface.md:1469`) and before the `#### 15.4.1` heading (`:1471`), while 0079's SPEC-7 replaces the recycling paragraph at `:1785`, inside §15.4.3. In §16.1, SPEC-6 here adds one catalog row per counter CODE-9 emits, while 0079's SPEC-8 extends the existing `lenny_gateway_pod_retirement_total` row's parenthetical with a `no_successor_runtime` reason value, which adds a label value rather than a series. **Two Go files are opened by both, on different functions.** In `scrubreport_server.go` and `scrubreporter_seams.go`, CODE-5 here re-keys `DrainLedger.RecordLeak`, `drainLedger.RecordLeak` and `RecordSessionScrub`'s ledger call by slot, while 0079's CODE-3 adds `runtimeRecreatable` and a `PodRecyclePolicy` field. **The two statements are compatible.** 0079 conditions pod reuse on the deployment model; the per-slot reclaim staged here is adapter-executed and holds on both models. 0079 in turn narrows one of the two harm classes this proposal fixes: a sidecar recycling pod that retires at its occupancy-zero boundary cannot carry a leaked entry into a later session. The co-tenant class, where the pod still holds another session at the moment of release, is untouched by it. | Land in either order, resolving the five shared spec files and the two shared Go files as a merge. Nothing of 0079 needs re-deriving. |
| 0077 (pre-materialize a workspace on a warm pod) | Early draft (2026-08-19 per its own `Date` line; the file's last commit, `8364087f9`, carries the same date, which records when someone touched it rather than when it was reviewed). It heads itself unreviewed and unconverged, its design section leaves the mechanism to a later revision, and its files-touched section reads "Not enumerable until §7 is answered", so it names targets and stages no text. | **Nothing of 0077 loses its subject, because it stages none.** One of its named targets is newly constrained. Its per-slot derivation at slot assignment (`proposals/0077_new_pre-materialize-a-workspace-on-a-warm-pod.md:136,:171`) lands inside the adapter slot lifetime this proposal fences: after CODE-6 a registry entry may be created only inside `ensureSlotStateLocked`, under `s.mu`, as one indivisible resolve-or-create-and-stamp step, and only by a request carrying a bind attempt token, and the per-slot tree is destroyed by the failed-bind reclaim under the slot identifier's reclaim hold and the per-slot guard. A derivation that writes a slot's tree therefore runs inside that guard and creates no entry of its own. Its §7 question 5, what the recycle boundary does to the pod-wide cache, is unchanged: SPEC-3 appends to §5.2's scrub model and widens §5.2's slot-cleanup action list, and widening the §5.2 whole-pod scrub to enumerate the registry is a stated non-goal here, so the whole-pod scrub the cache has to survive is the shipped one. Its remaining targets are untouched, `spec/06`'s warm-pod checklist and `spec/05`'s pool configuration included, both of which sit outside the §6.2 fence and projection and the §5.2 anchors SPEC-3 and SPEC-4 edit. | Write 0077's detailed design against the post-reclaim slot lifetime: place the per-slot derivation inside the per-slot guard, have it create no registry entry, and establish that the pod-wide cache is not per-slot state the reclaim removes. No edit to 0077's staged content, because it stages none. |
| 0071 (route a runtime frame to one consumer instead of broadcasting it) | Draft for review (2026-08-13 per its own `Date` line; the file's last commit, `8f1083b70`, is dated 2026-08-31, which records when someone touched it rather than when it was reviewed). It is written against 0069 having landed, and its subject is intact in the tree: `demuxSessionOutput` (`pkg/adapter/attach.go:314`) and `broadcast` (`pkg/adapter/socketruntime.go:249`) both still stand. | **No deliverable of 0071 loses its subject.** CODE-1, CODE-2 and SPEC-1 all keep theirs. This proposal's `## Files touched on application (non-spec)` opens none of `pkg/adapter/socketruntime.go`, `pkg/adapter/attach.go`, `pkg/adapter/heartbeat.go` or `pkg/adapter/slotframe.go`, which appear here only as citations, and it leaves §28.5.3 unedited and adds no §28 register row, because `Shutdown` appears in none and §28.5.1 is organised per channel rather than per field. **Two files collide, both in the test lane, as merge hazards.** The staged co-tenancy-hazard case adds a sibling assertion in `pkg/adapter/socketruntime_test.go` beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (`:252`), and 0071 rewrites the same package's tier-1 cases. Both proposals also edit `tests/spec-map.json`, 0071 for the tier-1, tier-7a, tier-8 and tier-9 cases it rewrites and this proposal for the files and cases it adds together with the one tier-11 row S7 removes from section `12.1`; the entries are disjoint and the file is one. The assertion's listener half survives 0071, which leaves `p.listener.Close()` (`pkg/adapter/socketruntime.go:467`) alone; 0078's CODE-1 is what removes it. **One statement of this proposal's record goes stale in 0071's direction, and it is prose about the shipped tree rather than a staged edit.** The third residue symptom is narrated against `deliverToSession` (`pkg/adapter/attach.go:345`, `:357`), which 0071's CODE-1 deletes. It stands under `## Defects in the shipped tree that this proposal does not stage`, inside the 0080 row above, in the accepted failure modes of the non-spec changes, and in the problem statement's own account of the residue. No staged edit rests on it: re-scoping `slotCount` and `deliverToSession` is a stated non-goal, and the predicate is left exactly as it is. | Land in either order. Resolve `pkg/adapter/socketruntime_test.go` and `tests/spec-map.json` as merges, keeping both proposals' cases and entries. If 0071 lands first, restate the third residue symptom against the routing table's unknown-address arm, which is where 0071 moves the drop. No edit to 0071's staged content, and no re-derivation of any of its deliverables. |
| 0082 (serialize concurrent `/finalize` with a compare-and-swap) | Draft (2026-09-23 per its status file's `drafted-date`; its first review run that day did not converge) | **No deliverable of 0082 loses its subject.** 0082's prepare-failure path inherits this proposal's refusal-aware `Binder.Prepare` reclaim closure whichever proposal lands first. CODE-13's attempt-scoped lease release on `Binder.Prepare`'s credential-assignment failure runs before 0082 CODE-3's `revokeFinalizeLease`, which then finds no lease, so each lease is released once in either landing order. This proposal edits no §15.1 table 0082 opens (its one §15.1 edit is a pointer sentence on the error catalog's `SETUP_COMMAND_FAILED` row, and 0082 edits the endpoint precondition table), and the two proposals edit different functions in `pkg/gateway/sessionserver/start.go` and `pkg/gateway/sessionserver/sessionserver.go`; this proposal's refusal case in `writePodClaimError` also sets the envelope `handleFinalize` writes after 0082's rewritten prepare-failure branch. | Rebase textually if it lands second. If 0082 lands, this proposal names another trigger for CODE-8's `Binder.Prepare`-arm short-circuit or states that the arm is a guard with no known path (0082's own row on 0081 records the same obligation). |
| 0083 (four spec sites disagree on the finalize workspace-failure envelope) | Draft (2026-09-25 per its status file's `drafted-date`) | **No deliverable of 0083 loses its subject.** 0083 takes up the shipped-tree defect "§6.2, §7.2, §15.1. The spec sites that name a `/finalize` failure envelope disagree about a workspace envelope.", which this proposal records under `## Defects in the shipped tree that this proposal does not stage` and does not stage, by operator decision 30. The two proposals open the same spec files at different anchors. 0083's SPEC-1 edits §6.2's **Client visibility:** bullet and its SPEC-2 edits §7.2's **Pre-attached vs. post-attached failure visibility.** paragraph, while SPEC-2 here edits §6.2's `resuming` failure transitions and §7.2's mid-resume snapshot-close sequence, SPEC-4 here edits §6.2's state machine and occupancy projection, and SPEC-5 here appends a pointer sentence to the §15.1 error catalog's `SETUP_COMMAND_FAILED` row, which 0083 does not open. | Nothing is required. Whichever proposal lands second rebases textually. |
| gateway-runtime-comms remediation, step R1b | Programme step | SCHEMA-1 opens `schemas/lenny-adapter.proto` under rule S-2's second window rather than against R1b's reservation, additively and without touching any identifier R1b renamed, on the terms and with the step ordering the proto-window decision above states. | Record the second window against S-2, so the steps that plan against the generated types (R12, R15, R16, R17, R22, R23) plan against the regenerated set rather than against R1b's. |
| gateway-runtime-comms remediation, step R12 | Programme step | The hold-timeout reclaim of unstarted slots and its §10.1.4 statement are left to R12, which builds the control-stream consumer that arms the hold and owns the gateway-side whole-pod-loss response. | Take both halves when it builds the consumer, together with an in-flight-upload guard. |

## Deliverable index

- **SPEC-1** (`spec/04_system-components.md`, `spec/29_communication-scenarios.md`): §4.1's `ShutdownRequest` paragraph, the §4.7 `Shutdown` and `DemoteSDK` rows, and §29.4's session-end step 13.
- **SPEC-2** (`spec/07_session-lifecycle.md`, `spec/06_warm-pod-model.md`, `spec/04_system-components.md`): the §7.1 failed-bind reclaim obligation, §7.2's mid-resume snapshot-close sequence, §7.3's resume flow after pod failure, §6.2's `resuming` failure transitions, and §4.7.9 step 5.
- **SPEC-3** (`spec/05_runtime-registry-and-pool-model.md`, `spec/04_system-components.md`, `spec/12_storage-architecture.md`): §5.2's scrub model, slot failure and cleanup, with the disposition table and the slot-identifier reclaim hold, the §4.7 `ReportSessionScrub` row, and the §12.6 `agent_pod_state` table schema.
- **SPEC-4** (`spec/06_warm-pod-model.md`, `spec/04_system-components.md`): the §6.2 per-slot sub-state fence and the prose after it, and the occupancy projection's claim-deletion statements in §6.2 and §4.6.1.
- **SPEC-5** (`spec/04_system-components.md`, `spec/05_runtime-registry-and-pool-model.md`, `spec/15_external-api-surface.md`): the §4.7.1 bind attempt token block, one pointer sentence on the §15.1 `SETUP_COMMAND_FAILED` row, one on the §5.2 `**Client error on exhaustion:**` bullet, and the two §15.4 blocks after the SDK-warm demotion contract.
- **SPEC-6** (`spec/16_observability.md`): the §16.1 metric catalog rows.
- **SCHEMA-1** (`schemas/lenny-adapter.proto`, `pkg/proto/adapter/v1` (regenerated), `scripts/seed-claim-register.py`, `tests/claim-map.json`): the bind attempt, the mid-session conditioning, the two-field teardown precondition, the reclaim outcome, the two refusal codes, and the scrub-outcome and report-trigger comments.
- **CODE-1** (`pkg/adapter/session.go`): `Shutdown` requires exactly one of the two teardown fields and compares the bind attempt before the deregistration.
- **CODE-15** (`pkg/adapter/session.go`, `pkg/adapter/server.go`, `pkg/adapter/slotsession.go`, `pkg/adapter/runtimegeneration.go`): `Shutdown` releases the slot and its tree for any entry removed and tears the runtime down only for a started session.
- **CODE-2** (`pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go`, `pkg/adapter/slotsession.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`): a start confirms the registry still holds its own attempt's entry before recording the runtime as holding the session.
- **CODE-3** (`pkg/sandbox/slotstate/slotstate.go`, `pkg/sandbox/slotstate/registry.go`, `pkg/gateway/runtime/slothealth/slothealth.go`, `pkg/gateway/sessionserver/sessionserver.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go`): the per-slot comment surfaces follow the fence's reduction and the edge list gains the pre-running cleanup edge.
- **CODE-4** (`pkg/gateway/podlifecycle/podsession/slotbinder.go`, `binder.go`, `bindattempt.go`, `pkg/gateway/sessionserver/upload_to_session.go`, `pkg/gateway/runtime/adapterclient/client.go`): the gateway mints a bind attempt, carries it on the bind sequence, and sets `unconditional_teardown` at every non-compensating `Shutdown` caller.
- **CODE-13** (`pkg/gateway/podlifecycle/podsession/slotbinder.go`, `binder.go`, `slotfailure.go`, `pkg/gateway/sessionserver/start.go`): the gateway compensates every post-connection bind failure and every failed resume, and scopes the lease release to the attempt.
- **CODE-5** (`pkg/gateway/sessionserver/start.go`, `pkg/gateway/runtime/slothealth/slothealth.go`, `pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go`, `pkg/gateway/session/recycle/scrubreporter_seams.go`): one accounting helper serves every bind path the reclaim obligation binds, the resume classifier learns the adapter's transient code and the started-session refusal, and the client envelope mapper answers both slot-bind refusals with the retryable fallback.
- **CODE-6** (`pkg/adapter/bindattempt.go`, `pkg/adapter/slot.go`, `pkg/adapter/slotsession.go`, `pkg/adapter/server.go`, `pkg/adapter/staging.go`, `pkg/adapter/slotcreds.go`, `pkg/adapter/credentials.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/holdstate.go`): the bind-attempt token, the rule 2-through-7 predicate at the one resolve chokepoint, the reclaim hold, and the shared resolve helper.
- **CODE-14** (`pkg/adapter/bindattempt.go`, `pkg/adapter/server.go`, `pkg/adapter/staging.go`, `pkg/adapter/resume.go`, `pkg/adapter/slotsession.go`, `pkg/adapter/session.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/holdstate.go`): the per-slot guard and the removing-site table.
- **CODE-7** (`pkg/gateway/runtime/adapterclient/client.go`): the translation from the gRPC status detail to the two sentinel errors the gateway matches with `errors.Is`, and the exported predicate `IsSlotBindRefusal` over both.
- **CODE-8** (`pkg/gateway/podlifecycle/podsession/binder.go`): the reclaim closures return a typed refusal without draining the pod.
- **CODE-9** (`pkg/observability/metrics/catalog.go`, `pkg/observability/metrics/catalog_test.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go`, `pkg/adapter/metrics.go`, `docs/reference/metrics.md`, `pkg/gateway/podlifecycle/podsession/binder.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`, `cmd/lenny-gateway/metricsbackfill.go`, `tests/tier11_docs/slot_compensation_metric_reference_test.go`): the counters that make the fence observable.
- **CODE-10** (the comment carriers the CODE-10 grep returns): the comment carriers of the withdrawn reporting universal take their reduction. CODE-10 and CODE-12 are sub-blocks of the non-spec `### Comment-carrier reduction: shared invariants` block.
- **CODE-12** (the comment carriers the CODE-12 command returns): the comment carriers of the re-keyed claim-deletion projection take their reduction.
- **CONF-1** (`tests/tier3_contract/adapter_bind_attempt/`, `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`, `scripts/seed-claim-register.py`, `tests/claim-map.json`): the published contract is enforced at the wire and exercised in process.
- **DOCS-1** (`docs/reference/state-machines.md`): the per-slot sub-state table and the pod state machine paragraph mirror SPEC-4.
- **DOCS-2** (`docs/reference/adapter-contract.md`): the `Shutdown`, `DemoteSDK` and `ReportSessionScrub` rows, and one added bind-attempt paragraph.
- **DOCS-4** (`docs/reference/execution-modes.md`, `docs/operator-guide/security-principles.md`): each page's per-slot cleanup sentence loses its reporting clause.

Tests are not separate deliverables, with one exception. The landing table under `## Testing`
in the non-spec changes file assigns each test to the deliverable that owns it and the
implementation step it lands in, and each case is specified there under its owning deliverable. CONF-1 is listed above because a conformance battery for a contract §15.4
publishes to third-party adapter authors is the deliverable that makes that contract
checkable, rather than the test coverage of another deliverable.
