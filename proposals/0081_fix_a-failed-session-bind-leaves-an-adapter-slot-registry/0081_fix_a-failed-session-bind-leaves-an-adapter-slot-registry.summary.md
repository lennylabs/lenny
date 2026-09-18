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

- The gateway compensates a failed bind. At every post-connection bind-stage failure and at a
  failed `Resume`, the gateway sends the adapter a `Shutdown` for the slot it reserved, so the
  registry entry, the per-slot tree, the slot's credential directory and the armed §4.9 expiry
  timers are removed instead of surviving for the life of the pod
  (`pkg/gateway/podlifecycle/podsession/`).
- A caller-minted per-attempt token fences that compensation. The gateway mints an opaque token
  once per bind attempt, before that attempt's first RPC, and carries it on every bind-sequence
  request that can create or resolve a registry entry. The adapter stamps it on the entry it
  creates and never on an entry it resolved, so the attempt that creates an entry owns it until
  the entry is removed. A compensation can therefore match only an entry its own attempt
  created, and a teardown that arrives after a retry has bound the slot answers `superseded` and
  removes nothing (`schemas/lenny-adapter.proto`, `pkg/adapter/slot.go`).
- Adoption of a foreign attempt's entry is refused where the entry is resolved.
  `ensureSlotStateLocked` compares the request's token against the entry's under the registry
  lock, in the same indivisible step that resolves or creates and stamps. Its callers cover
  every RPC that can reach an entry, the claim path included, so a `Resume` landing on a leaked
  entry another attempt stamped is refused before it can record the session as started
  (`pkg/adapter/slot.go`, `pkg/adapter/slotsession.go`).
- The same predicate refuses a bind-sequence request against an entry whose session has already
  started, and bars a mid-session request from creating an entry at all. A mid-session request
  carries no token and asserts no identity, and its safety rests on the gateway admitting a
  §7.4 upload only for a session with a live binding in the replica's `podRegistry`. A
  bind-sequence request that carries no token is refused (`pkg/adapter/staging.go`,
  `pkg/adapter/slotcreds.go`).
- Destruction requires an affirmative flag. `ShutdownRequest` carries `bind_attempt` and
  `unconditional_teardown`, exactly one of which must be set, and a request carrying neither or
  both is `INVALID_ARGUMENT` and performs nothing. A caller that forgets the token gets an error
  rather than a destroyed session, and an operator tool that must remove a leaked entry sets one
  named boolean (`pkg/adapter/session.go`).
- The refusal reaches the gateway as a Go typed error, and the reclaim closures read it.
  `pkg/gateway/runtime/adapterclient` translates the two new error codes from the gRPC status
  detail into sentinels, and `Binder.Prepare`'s and `Binder.Launch`'s reclaim closures take the
  failing error as a parameter and skip `failPhase` on such a refusal, so the call site returns
  the refusal and the pod is not drained. `failPhase` drains on every call, so without that
  short-circuit the first thing the gates do in production is retire pods that are serving their
  sessions correctly (`pkg/gateway/runtime/adapterclient/client.go`,
  `pkg/gateway/podlifecycle/podsession/binder.go`).
- The adapter holds the slot identifier for the duration of a reclaim, from the deregistration
  through the tree removal and the runtime close, and refuses a bind onto a held identifier with
  a transient `codes.Aborted`. Every site that deregisters an entry it then destroys takes the
  hold through one helper, so `Shutdown`, `releaseSessionSlot` and the §10.1.4 hold termination
  cannot diverge (`pkg/adapter/slotsession.go`, `pkg/adapter/holdstate.go`,
  `pkg/adapter/slot.go`).
- The adapter's `Shutdown` answers the compensation with the outcome the comparison reached,
  releases the slot for any entry the call removed, runs the runtime teardown only for a session
  whose start it has admitted, and files a cleanup-outcome report only for a session the pod's
  shared runtime process was given (`pkg/adapter/session.go`).
- A start confirms its entry still carries its own attempt's token before it records the runtime
  as holding the session, on both RPCs whose admitted start the gateway's compensation can race
  (`pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`).
- The gateway computes the leaked disposition from the RPC error and the clean-exit flag and
  reads the reclaim outcome only for the superseded counter, suppresses the compensation
  entirely when the failing stage was refused on identity, and releases the lease identifiers
  its own attempt minted rather than every lease the session holds
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go`, `binder.go`, `slotfailure.go`).
- One accounting helper serves every bind path the reclaim obligation binds, so the create-time
  reserved path and the §7.3 re-attach reach the §5.2 unhealthy threshold neither has ever
  reached, and `isTransientPodClaimError` gains the `codes.Aborted` arm that serves the reclaim
  hold's refusal, the superseded refusal and the start rollback alike
  (`pkg/gateway/sessionserver/start.go`).
- The specification states the obligation and the contract. §7.1 gains the failed-bind pod-side
  reclaim duty and its `leaked` disposition as a paragraph of its own, binding the creation,
  start, and re-attach bind attempts alike, with §7.3's resume flow, §6.2's mid-resume cancel
  edge and §4.7.9 step 5 pointing at it, and §7.2's mid-resume snapshot-close sequence running
  the reclaim before it releases the replacement pod. §4.1 and the §4.7 `Shutdown` row separate
  the slot release from the runtime teardown and give each its own precondition. §5.2's
  slot-cleanup action list names the per-slot credential directory and the §4.9 timer
  cancellation, its scrub model covers the cleanup of a bind abandoned or failed after its slot
  enters `receiving_uploads` and before it reaches `running`, and it states the slot
  identifier's reclaim hold. §6.2 gains the `receiving_uploads → slot_cleanup` edge.
- §4.7.1 states the mechanism once: what the caller mints and when, that the adapter stamps only
  the entry it creates, the atomicity the comparison is performed under, and then admission and
  teardown as two named, ordered cascades with one table of which request carries which field.
  §15.4 names that rule set, states what conformance against it means for a third-party adapter,
  and restates no rule's condition. §15.1's REST error catalog takes no row for either code,
  because the gateway consumes both and the client receives the envelope §15.1 already defines
  for the endpoint that issued the bind sequence; its `SETUP_COMMAND_FAILED` row is widened to
  state both deterministic `FAILED_PRECONDITION` causes, and §6.2's matching client-visibility
  clause is re-keyed on the setup-command request together with the gRPC code.
- The per-slot edge list in `pkg/sandbox/slotstate`, the per-slot table and the pod state
  machine paragraph in `docs/reference/state-machines.md`, and the adapter-contract reference
  follow the spec edits,
  and §16.1 gains the rows for the counters the compensation emits.

**Decisions.**

- The fence is a token the caller mints per bind attempt rather than an epoch the adapter mints
  per entry. An adapter-minted value reaches the caller only on a response, so an attempt holds
  it only after the response that reported it. That latch is what made the epoch self-defeating:
  a bind that fails inside its first entry-creating RPC never received one and reclaims
  unfenced, a compensation on a fresh connection holds none, and two attempts sharing one entry
  share one value and cannot be told apart. The value that has to fence the compensation is
  exactly the value a failed attempt may never have been given. A token held by the caller
  before its first RPC has none of those windows.
- The token is written once, on the branch that creates the entry, and is never overwritten on a
  resolve, in either direction. This is what answers the objection that withdrew the per-attempt
  form in an earlier round: a caller-minted identifier carries no order, so a design in which an
  admitted request can report a fresh value onto an entry the adapter already holds lets an
  earlier attempt's straggler take ownership back. First-writer-wins makes the mechanism monotone
  by construction, and leaves nothing for a counter to order.
- The comparison lives inside `ensureSlotStateLocked`, under `s.mu`, as one indivisible
  resolve-or-create-and-stamp step. That function has three production callers and they cover
  every RPC that can create or resolve an entry, the claim path included. A comparison placed in
  the workspace handlers instead leaves the claim path ungated, and a `Resume` landing on a
  leaked entry then resolves it, sets `started`, and is destroyed by the stale compensation that
  entry's own attempt sent. The specification states the atomicity, because without it
  first-writer-wins is not implementable from prose and an adapter that resolves, releases its
  lock, and then stamps conforms to the letter while being broken in fact.
- `unconditional_teardown` is the affirmative form of the unconditional teardown, rather than an
  empty `bind_attempt` meaning it. An empty value meaning destruction is fail-open on a
  destructive path, it makes an adapter that refuses a `Shutdown` naming nothing non-conforming
  when it is safer than the contract, and a contract test can pin that the compensating caller
  sends a token while it cannot pin that the other callers meant to destroy. Both fields are
  bare scalars with no wire presence, so §4.1's rule that no operation is selected by a field's
  presence standing in for a scope is untouched, and `ShutdownRequest.recycle` is the shipped
  precedent for a disposition carried beside the address.
- The admission cascade is stated once, as §4.7.1's named rules with one table of which request
  carries which field, and every other carrier names a rule rather than restating it. The two
  decisions that cascade encodes: a mid-session upload asserts no attempt identity, because its
  binding predates the request and may have been made by another replica, so requiring a token
  there is unshippable while permitting one on a bind-sequence request is fail-open; and a
  mid-session request never creates an entry, because today a mid-session finalize for a session
  the pod holds no entry for creates the entry and a full on-disk tree before the mid-session
  branch is consulted (`pkg/adapter/staging.go:181` resolves, `:239` reads the marker), and an
  entry created that way would carry no token, admit every attempt, match no compensation, and
  be permanent. Reading the marker before the resolve is why it has to be on
  `PrepareWorkspaceRequest` as well as on the finalize. On the client-streaming
  `PrepareWorkspace` both fields are read from the frame that resolves the slot identifier,
  under the first-frame rule, and no later frame is read.
- `Binder.Prepare` mints a token and gets no compensating `Shutdown`. It sends four
  non-mid-session bind-sequence requests, so it cannot pass an empty token. It needs no
  compensation because every `Prepare` failure runs `failPhase`, which drains the pod, and the
  entry dies with it. What it does need is the reclaim short-circuit: two concurrent finalize
  calls produce two `Prepare` attempts, and without it the loser's refusal drains the winner's
  pod.
- `StartSessionRequest` and `ConfigureWorkspaceRequest` carry no token. `Binder.Launch` runs in
  a separate HTTP request from the `Prepare` that created the entry, issues only those two RPCs,
  and sends no slot compensation, so a token gate on them would refuse `Launch` against the entry
  `Prepare` legitimately created. Both are governed by the phase gate and by the reclaim
  short-circuit instead, with `ConfigureWorkspace`'s published idempotent repeat exempt from the
  phase gate. An entry either of them creates carries no token, which the accepted failure modes
  below record.
- The two refusals are distinct error codes, because `adapterv1.Error` carries no reason field
  and every gateway consumer of a bind failure matches on Go types. The started-entry refusal is
  `PERMANENT` and maps to `FailedPrecondition`; the identity refusal is `TRANSIENT` and maps to
  `Aborted`, which the accounting helper's new `Aborted` arm already covers, so the superseded
  case reuses one arm rather than adding a third.
- The remedy is the `Shutdown` RPC the gateway already has, sent on the connection it already
  holds at the failure site. `Shutdown` clause two remains the single removal entry point and the
  token is a precondition on it. The compensation runs on a detached context
  (`context.WithoutCancel`), because the residue class that leaves a runtime running arises
  precisely when the caller's context expired during `StartSession`.
- The compensation is best-effort and its own failure is counted. An unacknowledged reclaim sets
  the `leaked` disposition, which `SlotClaimer.ReleaseSlot` already implements, so the pod
  retires through the shipped §5.2 threshold instead of accumulating residue. Its budget is
  §5.2's per-slot cleanup timeout, `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)`
  seconds, both inputs of which `SlotBindRequest` already carries, and the graceful window the
  call pins for the adapter is half of it so the gateway does not give up on the adapter's
  SIGTERM pivot.
- The leaked disposition reads the RPC error and the clean-exit flag and does not read the
  outcome. Every teardown rule that removes no entry answers a clean exit, so `superseded` and
  `absent` are unleaked without a branch, and an outcome this build does not recognize is judged
  on the same two fields. A fail-closed default keyed on the outcome would drain a healthy pod:
  `UnhealthyThreshold` is `int((maxConcurrent + 1) / 2)`, so the threshold at
  `maxConcurrentSessions: 2` is 1, `Unhealthy` compares with `>=`, and `RecordLeak` increments a
  map that is never window-pruned. An outcome arm would add nothing but that default.
- The credential release is scoped to the attempt rather than moved inside the `errors.As`
  guard. `credassign.Service.ReleaseSession` walks every lease the session holds, and the slot
  identifier is the session identifier, so a release running after a successor has assigned
  strips the successor's leases. The binder records the lease identifiers its own
  `assignCredentials` minted and releases those, which leaves the session-wide walk to the
  session-end path that owns it. The call stays unconditional and outside the guard, because a
  stage error that is not a `*SlotBindError` must still return minted leases.
- `schemas/lenny-adapter.proto` is opened once, additively, under rule S-2's second window.
  The edit adds fields and enum values only, removes nothing that ships today, and renumbers nothing, so
  `buf breaking` has nothing to fire on. Rule S-2 admits no third window, which is why the phase
  gate is folded into this proposal rather than sequenced ahead of it as its own proposal with
  its own proto change. The precondition binds this proposal's own step ordering, because it
  opens three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`): the
  schema step and its regenerated stubs land in one commit before those handler steps.
- No downstream predicate is re-scoped. `slotCount` keeps counting registered-but-unbound
  entries, `claimPodMCPStartLocked` keeps its raw entry-count guard, and `boundRemains` keeps
  reading the binding. Both are deliberate co-tenancy rules with their reasons in their own
  comments. The residue stops existing; the rules it was breaking stay as they are.
- The compensation's outcome is measured. The counters are added against the §16.1 catalog: a
  compensation answered `superseded`, meaning the adapter held an entry the compensation was not
  addressed to, so the reclaim released nothing; and a `Shutdown` that met an entry carrying no
  token, which is zero on the bind paths and non-zero when an abandoned attempt's late
  `StartSession` left an untokened entry behind.
- File-collision discipline: the compensation's own adapter edits land in `session.go` and
  `runtimegeneration.go`, and the reclaim hold and its helper land in a new file.
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

- The reclaim closures must learn to read a refusal before the gates go live. `failPhase` gates
  only `releaseCredentials` on `leaseAssigned`; `b.drain` sits outside that block and runs on
  every call, and both `Binder.Prepare`'s and `Binder.Launch`'s closures reach it. A refusal
  means this pod is serving the session correctly, so draining it is the worst available
  response. This is a hard blocker of the identity and phase gates rather than an independent
  cleanup.
- The gateway must learn to send the new fields before the adapter begins requiring them. The
  mint-and-carry step and the `unconditional_teardown` step land ahead of the adapter's
  precondition checks, because the reverse order refuses every bind and every teardown in the
  window between the two steps.
- The mid-session exemption rests on one shipped guard. A mid-session request asserts no
  identity, cannot create, and is exempt from the phase gate, and what keeps that safe is the
  gateway admitting a §7.4 upload only for a session with a live binding in the replica's
  `podRegistry`. Confirm that guard reads what it appears to read before the mid-session rules
  land; it is the whole safety argument and it has not been independently re-derived.
- The phase gate must not refuse a legitimate repeat. `ConfigureWorkspace`'s idempotent repeat
  on an SDK-warm pod resolves a started entry on purpose, so `allowStarted` is true there and
  for a mid-session request and false everywhere else. A gate that refuses the repeat breaks the
  SDK-warm path; one that admits a foreign attempt defeats the mechanism.
- Sending today's `Shutdown` at the credential-assignment stage would brick the pod.
  `SocketRuntimeProcess.Close` returns early only on `!p.connected`, then calls
  `releaseActiveLocked`, which deletes an absent key and returns `len(p.active) == 0`. On a pod
  whose active set is empty it therefore closes the shared connection, kills the spawned child,
  and calls `p.listener.Close()` on a listener bound once in `NewSocketRuntimeProcess` and never
  rebound (`pkg/adapter/socketruntime.go:156-161`, `:435-467`). `InProcessRuntime.Close` and
  `MCPRuntime.Close` ignore the session identifier entirely and tear the runtime down on any
  call (`pkg/adapter/embedded.go:188-204`, `pkg/adapter/mcpruntime.go:266-291`). This is why the
  adapter gate must land before the gateway compensation.
- The §7.1 connection sentence carries no correctness load, and the staged text states it as a
  cost preference: the reclaim reuses the connection the failed attempt already holds when that
  connection is still open, because reusing it costs nothing, and the fence does not depend on
  the connection. The token is held by the caller independent of any connection, so a
  compensation on a fresh connection is fenced exactly as one on the original. Restoring it as a
  correctness rule would foreclose the durable compensation record that position 2 of the
  remediation plan stages.
- `Close`'s doc comment claims it is a no-op for a session not in the active set. That is false
  in one reachable state: `Interrupt` of the last active session closes the connection while
  leaving `connected` true and `conn` non-nil (`pkg/adapter/socketruntime.go:398-417`), after
  which any `Close` takes the last-close teardown. The adapter test set pins this; the comment is
  not corrected here.
- `stageWorkspace` issues `PrepareWorkspace` only when the plan carries uploads, so a
  workspace-preparation failure on an upload-free plan sends no RPC and leaves no adapter entry.
  The compensation is still sent, the adapter answers `absent` for a session it holds nothing
  for, and the failure is accounted transient. A test asserting "exactly one `Shutdown`" on that
  branch would pin over-sending as contract.
- `UnhealthyThreshold(2)` is 1, and `countsLocked` sums windowed failures with persistent leaks.
  At `maxConcurrentSessions: 2` a single cleanly released bind failure already drains the pod
  today, so a test that uses that concurrency cannot distinguish the leaked arm from the failed
  arm. The gateway accounting cases use `maxConcurrentSessions: 4`.
- `TestValidTransitions_spec_6_2` compares a `want` list in
  `pkg/sandbox/slotstate/slotstate_test.go` against `ValidTransitions()` and fatals on a length
  mismatch. CODE-3 changes both, so the two must move in the same step. Nothing compares either
  against `spec/06_warm-pod-model.md`, so the §6.2 edit is not what the test gates.
- `SlotID == SessionID` on every path, and §5.2 placement prefers a pod already hosting the
  tenant's slots, so a retry re-uses the identifier and can land on the pod whose reclaim may
  still be running `os.RemoveAll` outside `s.mu`. The reclaim hold refuses the retry for the
  duration, which spends one attempt rather than losing a tree. The pod-exclusion mechanism that
  previously carried this constraint is withdrawn: its single write site was the within-request
  retry loop, a retry of a failed bind is a new client request that builds a fresh bind request,
  and full exclusion on a small pool returns `ErrNoConcurrentSlot` and a spurious
  `WARM_POOL_EXHAUSTED`.

## Goals

- Fence each bind attempt with a token the caller mints, carried on the requests that stage the
  session's workspace, its credentials and its setup and on a `Resume`, and on the compensation
  that reclaims the entry, so an abandoned attempt cannot act on a successor's entry. A start
  carries no token and confirms instead that the entry it claimed is still its own before
  recording the runtime as holding the session.
- Remove the adapter slot registry entry, the per-slot tree, the per-slot credential file, and
  the armed §4.9 expiry timers that a failed bind leaves behind, in all three residue classes,
  and take the abandoned session back off the pod's shared runtime process in the class where
  the runtime was given it.
- Hold the slot identifier from the deregistration of its registry entry until the cleanup that
  reclaims it has finished, so a bind cannot be admitted onto an identifier whose tree is still
  being torn down, and guard every section that writes or destroys a slot's tree outside the
  registry lock, the reclaim's own destructive section included, so the fence at the resolve is
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
  `Resume` response, is closed by extending CODE-4 to `Binder.Resume` and CODE-2's start
  confirmation to the adapter's `Resume` instead.
- **Widening the §5.2 whole-pod scrub to enumerate the registry as its source.** Its on-disk
  enumeration is deliberate and its own comment gives the reason: the residue it must reach
  belongs to a leaked slot whose registry entry is already gone.
- **Splitting the work by residue class into two proposals.** The change touches one map, one
  entry, one `delete`, and one conditional. Two proposals would edit the same branch.
- **Landing the gateway compensation without the adapter's teardown gate.** It would send a full teardown, a
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

**Accepted failure modes.**

The token closes two residues the earlier design recorded as unclosable. A bind that fails
inside its first entry-creating RPC is fenced, because the caller holds its token before that
RPC rather than latching it off a response. Two attempts that share one entry are told apart whenever both
carry a token, because the entry carries the token of the attempt that created it and the later
attempt is refused rather than admitted.

Further residues stand, and each is accepted as recorded rather than closed here.

- An attempt's own later RPC can recreate the entry an unconditional teardown removed
  mid-sequence. A non-mid-session finalize may legitimately be an attempt's first RPC, on a plan
  with no uploads, so creation is permitted, and the attempt recreates its own entry under its
  own token and can materialize from an empty staging tree. A token cannot fence an attempt
  against itself. Closing it needs either a generation on the tree or a bind-scoped lock
  spanning the attempt.
- An abandoned attempt's late `StartSession` creates an entry carrying no token, because neither
  `StartSession` nor `ConfigureWorkspace` carries one. A `Shutdown` naming an attempt answers
  `superseded` against that entry and removes nothing, so it is released only by an unconditional
  teardown or by the pod's retirement, and every later bind attempt at that session on that pod
  is refused while it stands.
- A compensation lost to a gateway crash leaves an entry stamped with a dead attempt's token,
  and every later attempt at that session on that pod is refused, so the session is unstartable
  there until the pod is replaced. This is the direction the token trades for: the earlier design
  would have let a retry adopt that entry, which is how a live session got destroyed.
- A bind-sequence refusal reaches the client under the envelope its stage already selects. The
  gateway consumes both refusal codes and this proposal leaves its envelope selection alone, so
  a workspace-stage refusal reaches the client under the transient session-start envelope. In
  the setup window the gateway renders only the setup-command request's failure into that
  envelope, and there it branches on the gRPC code rather than on the window, so an
  already-started refusal at that request, answered on `FAILED_PRECONDITION`, reaches the client
  as the non-retryable `SETUP_COMMAND_FAILED` that §15.1 defines for a deterministic
  setup-window failure, while a superseded refusal, answered on `ABORTED`, reaches it as the
  retryable session-start fallback carrying `Retry-After`. The client-visible code names the stage the
  refusal arrived in rather than the refusal itself. The category and the retryability the
  client reads are correct in every case, and narrowing the code is outside this proposal.

Recovery for the self-recreated entry, the entry a tokenless start created, and the entry a
lost compensation stranded is routed to position 2 of the gateway-runtime-comms remediation
plan, which is where the durable compensation record that survives a gateway crash and is
re-driven from a startup sweep, the reaper for a registry entry nothing collects, and the rule
that narrows which RPC may create an entry at all are staged. None is in this proposal. The
envelope-naming residue has no position-2 work and is left as recorded, because the category and
the retryability the client reads are already correct.

Two further residues are priced and accepted. A retry is refused while the previous attempt's
entry stands, and burns one attempt; the window is bounded by the compensation's latency plus
the §5.2 reclaim hold, and a refused retry is cheaper than a destroyed session or a successor
reaching `running` on an empty workspace. And an entry that outlives its attempt costs the pod
its MCP arming and its unaddressed-frame path for the pod's life, which both gates count
deliberately and which is recorded under the defects this proposal does not stage.

## Open decisions for human to make

The decisions below are open for a human. Each keeps the identifier it was stamped with, so the
numbering does not start at 1: the entries that held the earlier numbers were resolved, their
answers are staged in the change files, and their record is in the review log. Entries 21 and 27
carry a recommendation with its ground, its alternatives and a confidence. Entries 20, 22 and 26
carry the question and its ground alone, because the review loop derived no recommendation for
them.

20. **Do the two new error codes take the next two values in the `ErrorCode` enum, or the
    Phase-2 range?** The proto comment reserves 1000 through 1999 in prose and declares no
    `reserved` statement, so both ranges are available and nothing in the mechanism depends on
    the choice. The decision belongs to whoever owns the adapter's `ErrorCode` enum, and it is
    recorded here rather than taken in the deliverable because the enum's numbering convention
    is not stated anywhere this proposal can cite.

21. **Should a bind that ran the setup commands but never reached `running` count toward
    `recycle.maxSessionsPerPod`?** §4.7.9 step 5 runs the deployer and client setup commands
    (`RunSetup`) before `AssignCredentials`, so a bind abandoned at `ready` has already executed
    code on the pod, priming the residual-state vectors §5.2 says the scrub cannot address
    (TCP `TIME_WAIT` and conntrack entries, DNS resolver cache, page-cache priming,
    `inotify`/`fanotify` registrations, and pipes or sockets outside managed paths). Neither the
    shipped tree nor the spec this proposal stages counts such a bind toward the pod's session
    limit, and the condition is pre-existing rather than introduced here.

    **Recommendation (moderate confidence): leave the counter as it is, and raise the
    residual-state argument as its own finding against §5.2's retirement predicate.** The ground
    is that the counter is defined as a record of sessions served: §5.2 keys the retirement
    trigger on "the pod's served-session count", the field comment calls it "counts every session
    served", and §12 records `sessions_served` as gateway-written at each session release. A bind
    abandoned before `running` is never served and never released, so the shipped definition
    already excludes it, and the staged §5.2 append withholds the cleanup-outcome report on that
    path for the same reason.

    The alternatives and why each lost. **Count it**, treating a pod that has executed setup
    commands as having consumed one of its reuses: this loses on accounting, because the
    cleanup-outcome report carries no per-session dedup, so a §5.2 slot retry that re-binds the
    same session onto the same pod would be counted twice, retiring the pod early against a
    number that no longer measures sessions served and skewing the `lenny_pod_session_reuse_count`
    p50 the PoolScalingController derives `mode_factor` from (§16 observability, §5.2 scaling).
    **Withdraw the question as already answered by the staged text**: this loses because the
    staged text answers only what the counter counts, while §5.2 gives the field a second role
    when it requires the deployer to choose `maxSessionsPerPod` "based on the workload's
    sensitivity and the residual state vectors enumerated above". On that role the question is a
    policy question about how much unscrubbable residue one pod may accumulate, and neither the
    spec nor the code settles it.

    What deciding otherwise costs. Deciding that it should count commissions work this proposal
    has not scoped: a §5.2 edit that restates the retirement predicate in terms other than
    sessions served, a gateway accounting change with a per-session dedup so a retry is not
    double-counted, and an answer for the leak signal that rides the same report. None of that is
    staged here, so an affirmative answer is a new proposal rather than an edit to this one.

22. **Does an incomplete reclaim for a §7.3 re-attach onto a pod serving one session need a
    stated disposition?** The §7.1 obligation SPEC-2 stages reaches that attempt, because its
    parenthetical scopes only the §15.1 start by concurrency. §7.1 then states the `leaked`
    outcome only for a pod serving concurrent sessions, §5.2's staged append states that neither
    the `leaked` sub-state nor the whole-pod replacement trigger applies on a pod serving one
    session, and §6.2's staged paragraph covers only a failure at the workspace-preparation,
    setup or credential-assignment stage there, which a re-attach has none of: `Binder.Resume`
    sends only `Resume`. So the residue of an unacknowledged reclaim on that path lands in no
    spec text. The loop declined to file it, because three close variants of "the residue lands
    in no spec text" had already been refuted as landing-text completeness, and recorded that
    filing it would have to rest on §5.2 delegating into §6.2 text that does not cover the case.
    The decision is whether to state the disposition or to accept the gap.

26. **Is a hand-maintained `docs/reference/error-catalog.md` acceptable, or does it need a
    gate?** No test, script, or Makefile target holds that page to §15.1. After this proposal
    the `SETUP_COMMAND_FAILED` statement is a hand-maintained pair in three places: the §15.1
    row, the published docs row, and the enumeration sentence that lists the causes. DOCS-3
    records the absence of a gate and files it as its own finding rather than closing it. The
    decision is whether this proposal adds the reconciliation gate or leaves the pages paired by
    hand.

27. **Should the lost claim-DELETE retirement be opened as its own finding against §4.6.1, and
    if so does the fix belong on the gateway side or the controller side?** The occupancy
    projection decides a released pod's fate from the phase the pod is observed in at the moment
    the claim is gone: `pkg/controller/warmpool/occupancy.go:128-140` returns `Draining` for a
    pod observed as `Claimed`, `Idle` for one observed as `Reserved`, and no phase at all for any
    other value, which leaves the pod where it was. A bind that claims and releases a pod between
    two reconciles therefore never takes the `claimed → draining` retirement edge, and the pod
    stays in inventory as an ordinary idle candidate. Nothing in the spec records this, and it is
    a pre-existing platform property rather than something this proposal introduces.

    **Recommendation (moderate confidence): open it as a finding against §4.6.1, and leave the
    choice of side to whoever owns that finding.** The ground is that the race is mechanically
    reachable in the shipped tree rather than hypothetical. `observeClaim` is a level read of
    current state, mapping a NotFound to "no claim" with no event history
    (`pkg/controller/warmpool/occupancy.go:217-227`). Nothing holds the claim in existence long
    enough to be observed: `claimToSandbox` maps every claim event to a reconcile request keyed
    on the one owning Sandbox (`occupancy.go:281-291`), so the workqueue coalesces a create and a
    delete into a single dequeue; the per-pod `SandboxClaim` carries no production finalizer, the
    only claim finalizers in the tree being test holds (`gc_reclaim_internal_test.go:69`); and
    `podclaim.DeleteClaim` is an unconditional delete (`claimer.go:322-330`). The loop that first
    raised this recorded no recommendation because it could not establish whether the
    intermediate phase can be missed at all. That fact is now established, and it points toward
    filing.

    The alternatives and why each lost. **Do not file, and record it as a shipped-tree defect
    only**: this loses because a pod whose retirement was lost is not inert. It carries no
    used-pod guard beyond `expiredByUptime`
    (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:336-347`), so it is re-offered to the next
    claimant as a fresh idle pod, which is the outcome the `claimed → draining` edge exists to
    prevent. **Stage the fix here**: this loses because both candidate fixes sit in components
    this proposal does not touch, and neither is derivable from the projection alone.
    **Treat SPEC-4 as closing it**: this loses because SPEC-4 re-keys the spec's claim-deletion
    prose onto the projected phase at the claim DELETE
    (`spec-changes.md` SPEC-4, §4.6.1 and §6.2), which aligns the description with what
    `occupancy.go` already does. Aligning the description with the mechanism does not change the
    mechanism.

    The second half of the question stays open on its own terms. A **gateway-side** fix holds the
    claim until the pod projects `claimed` before deleting it, which puts an ordering constraint
    on the release path and costs latency on every release. A **controller-side** fix records the
    retirement on the claim durably, so the projection reads a marker rather than an observed
    phase, which costs a schema field and a migration and changes what the projection is allowed
    to conclude. Choosing between them commits work in a component this proposal does not touch,
    and nothing in the tree ranks the two.

    What deciding otherwise costs. This proposal stages nothing on this and is unaffected by the
    answer either way. Deciding not to file loses a latent retirement leak on the recycling path;
    if that is the answer, move this entry into the defects section below rather than deleting
    it, so the property stays recorded. Deciding to file spends a problem statement and a review
    cycle on a race whose frequency nobody has measured.

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
  collects belongs to the reaper named under the accepted failure modes.
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
  reclaim the adapter did not answer. SPEC-2's §7.1 paragraph dispositions that on a pod
  serving concurrent sessions, where the slot is `leaked`, and cites §6.2 for a pod serving
  one, where SPEC-4's paragraph states what becomes of the slot's state on each of the pod's
  two exits. Either way the §7.1 obligation is to send the reclaim rather than to have it
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
  abandoned attempt's own shipped rollback. CODE-6 routes it through `reclaimSlotLocked`, so it
  does take the reclaim hold, and a retry that has not yet staged its tree meets that hold and
  spends an attempt rather than losing a tree. The deletion survives in the ordering where the
  rollback runs after the retry staged, which neither the token nor the hold separates: the
  release compares no token, because it is a session-scoped release rather than a compensation,
  and the tree paths derive from the identifier both attempts share. No fix is staged because
  separating an attempt from its own lagging rollback needs either a generation on the tree or a
  bind-scoped lock spanning the attempt, which is the same ground the accepted failure modes
  record for an attempt recreating the entry its own teardown removed. This proposal
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
  the workspace and the checkpoint are intact. Adding the arm needs a §7.3 statement. CODE-5's
  `Aborted` arm covers the refusals this proposal introduces and converges with that fix rather
  than replacing it.
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
  following the `PROTOCOL_VERSION_INCOMPATIBLE` precedent of an adapter `ErrorCode` published
  through §15.4 alone. The pre-existing gap is its own finding.
- **No spec change. `Server.ReportSessionFailure` has no production caller.** Every reference
  outside `pkg/gateway/sessionserver/failure.go:115` is a test, so no outcome should be rested on
  it. Recorded so a later reader does not route a terminal disposition through it.

## Impacts on other proposals

| Proposal | Status | What this change does to it | What it must do |
|:--|:--|:--|:--|
| 0080 (inventory of the residues 0073 recorded and deferred) | Draft, stages no changes. Its own `Date` line reads 2026-08-31 and the last commit to the file is 2026-09-06, which records when someone touched the file rather than when it was reviewed. It heads itself an unconverged inventory rather than a design. | **§1.2 is the entry this proposal promotes, and it is discharged in its entry-removal half.** The staged reclaim removes the registry entry for both of §1.2's classes, together with the per-slot tree, the slot's credential directory and the armed §4.9 expiry timers, so the held inbound count and the pinned drain gate lose their subject. §1.2's third clause survives with a different cause and moves to §1.4. **§1.4 is made worse by a leaked entry, and this proposal records that rather than fixing it.** `claimPodMCPStartLocked` returns false whenever the pod's entry count is not one (`pkg/adapter/slotsession.go:109`), and the only entry-removal site is `deregisterSlotLocked`, whose §10.1.4 sweep caller filters on `st.started` and so cannot collect an unstarted entry. `writeSessionManifest` mints a fresh nonce before the arming guard is consulted, so every later session on that pod reads a manifest advertising a socket no running server authenticates. `deliverToSession`'s rejection of unaddressed session-scoped frames once the slot count passes one has the same cause. Both gates are deliberate co-tenancy rules; the defect is that the entry never goes away. This proposal removes the entry on every path it compensates, which restores the arming wherever the failed bind caused the loss, and changes neither gate. The entry that survives an unanswered compensation is not collected here, and the reaper that would collect it is named under the accepted failure modes. **The claim-register counts §1.12 and §1.18 quote both go stale, and by how much is determinate.** `tests/claim-map.json` carries 76 rows today, 20 `ABSENT`, 24 `UNWIRED` and 32 `WIRED`. SCHEMA-1 adds exactly three rows to the `EXPLICIT` list in `scripts/seed-claim-register.py`, two `WIRED` for the new wire contract and one `ABSENT` for the absent third-party conformance harness, which takes the register to 79 rows, 21 `ABSENT`, 24 `UNWIRED` and 34 `WIRED`. §1.12's heading and opening sentence ("Twenty of the register's seventy-six rows") and §1.18's tally ("seventy-six rows, thirty-two are `WIRED`, twenty are `ABSENT`") are both restated from those figures, and the `UNWIRED` count alone is unchanged. §1.12's note that the adapter metric names row is naming-law N4's metric half under `deferral_id: R12` gains a member rather than losing one: CODE-9's untokened-entry counter is registered in `pkg/adapter/metrics.go` and carries the same deferral, because the adapter process still exposes no scrape target. **§1.19 keeps its class set and moves only membership.** `boundSlotState` and `checkSessionBound` are untouched. A fence for a session whose bind failed and whose reclaim completed meets the absent-entry refusal rather than the unbound-entry refusal, and so does a fence for a session whose `StartSession` or `Resume` rolled back because the reclaim landed after its claim. After a reclaim the adapter did not acknowledge, a surviving entry is present-and-unbound or present-and-bound for a session the gateway has abandoned, so a fence in that window meets the unbound-entry refusal or neither refusal. The two new refusals are outside §1.19's inventory for the reason the hold's refusal already is: they are raised inside `ensureSlotStateLocked` with `codes.Aborted` and `codes.FailedPrecondition` on the bind-sequence and workspace RPCs, and carry neither the status code nor the RPC §1.19 is scoped to. **§1.7 keeps its subject and grows the spec side of its divergence.** `slothealth.UnhealthyThreshold`'s clamp of a sub-1 denominator to 1 and `drainLedger.RecordLeak` are unmodified, and no staged code produces a `leaked` disposition on an exclusive pod: the §7.3 re-attach accounting is gated on a non-empty slot id, which an exclusive pool never reserves, and the other accounting callers sit behind `maxConcurrentSessions > 1`. SPEC-2's §7.1 paragraph and SPEC-3's §5.2 scrub-model append each restate that the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy, and each cites SPEC-4's §6.2 paragraph for the exclusive-pod case rather than stating a disposition of its own. **§1.1, §1.3, §1.5, §1.16 and §1.20 have no conflict of subject.** The adapter edits are placed in `session.go`, `runtimegeneration.go`, `resume.go`, `sdkwarm.go`, `server.go`, `staging.go`, `slotcreds.go`, `credentials.go`, `slotsession.go`, `slot.go`, `holdstate.go` and the new `bindattempt.go`. `slot.go` and `slotsession.go` are opened because `ensureSlotStateLocked` stamps the token, raises both refusals and refuses the hold, `slotState` gains a `bindAttempt` field, and `reclaimSlotLocked` sits beside `deregisterSlotLocked`. `server.go` declares the reclaim-hold set and the per-slot guard table, `staging.go`, `slotcreds.go` and `credentials.go` move their resolve sites to the shared helper and pass what their RPCs assert, and `holdstate.go` gives `terminateHeldSession` a slot-guard acquisition with two deferred releases and moves the §10.1.4 pass's ten-second budget from one shared close context to a guard-acquisition deadline plus a per-member close context. Every edit outside `holdstate.go` is small and localised, so a later position's rewrite of those files re-lands a hold set, a stamp, two refusals, the moved resolve sites, a struct field and a helper rather than a mechanism. The `holdstate.go` edit is a mechanism, and the action column states what re-landing it requires. | Re-derive four entries when 0080 is triaged into successor proposals. Split §1.2 so its MCP-arming clause moves to §1.4 as a gap this proposal does not take, and record the rest of §1.2 as discharged here. Re-derive §1.4 against the leaked-entry cause and the reaper it waits on. Re-derive §1.19's membership against the remaining cases, its class set being unchanged. Re-derive §1.7 against four spec sites rather than the two §6.2 and §5.2 sites it names. Restate §1.12's and §1.18's counts from `tests/claim-map.json` once the schema step has landed its three rows, and add the untokened-entry counter to §1.12's list of surfaces waiting on the adapter metrics endpoint. Re-land the stamp, the two refusals, the hold refusal, the `slotState.bindAttempt` field and the `reclaimSlotLocked` helper when the rewrite of `slot.go` and `slotsession.go` happens, and the reclaim-hold set, the moved resolve sites and `terminateHeldSession`'s guard acquisition, its two deferred releases and the per-member close-context split of the §10.1.4 pass's ten-second budget when the rewrite reaches `server.go`, `staging.go`, `slotcreds.go`, `credentials.go` and `holdstate.go`. No edit to 0080's staged content, because it stages none. |
| 0073 (give every session a slot) | Implemented (2026-08-31 per its own status line; spec applied 2026-08-19) | This change touches 0073 in two ways. 0073 recorded this gap in its §9 recorded limits and declined to discharge it, and this proposal discharges it. SPEC-1 also retires the third sentence of §4.1's `ShutdownRequest` paragraph, which 0073's SPEC-7 authored and which reached `spec/04_system-components.md:157` in commit `f37e867b8`. That sentence states one per-session teardown gated on a bound entry; after the split there are two teardowns with two preconditions, and the slot release is gated on the entry being present. The paragraph's first two sentences, its session-scoped classification, and its closing rule are preserved. SPEC-4 and CODE-3 extend 0073's per-slot sub-state fence (`spec/06_warm-pod-model.md:150-155`) and its `pkg/sandbox/slotstate` edge list with one new edge, which adds to that list rather than retracting from it. | Nothing. A landed proposal is not edited, so this row is the record of what this proposal takes back from 0073. |
| 0075 (derive message scope from the address type) | Implemented (2026-09-08 per its status file's `implemented-date`) | 0075's SPEC-1 replaced the §4.1 block around the `ShutdownRequest` paragraph and reserved the paragraph itself, stating that it "stands unedited" because it explains a divergence between what a request addresses and what its handler touches that 0075's D3 rests on. SPEC-1 here rewrites that paragraph's third sentence. The ground D3 rests on survives: the first two sentences carry the session-scoped classification and the single address unchanged, and the replacement keeps the whole-pod-scrub clause and the closing rule that no operation is selected by a field's presence standing in for a scope. Nothing 0075 landed is opened, including the derivation rule at `spec/04_system-components.md:151-155`, its tier-0 addressing gate, its tier-3 session-address suite, and its `tests/spec-map.json` entries. The new `ShutdownRequest.bind_attempt` and `ShutdownRequest.unconditional_teardown` fields do not disturb that rule: both are bare scalars present on every `ShutdownRequest`, as the `coordination_generation` fence on the same message already is, so no operation is selected by a field's presence. SPEC-1 restates the justification per message rather than stating a precondition in §4.1, and the §4.7 `Shutdown` row carries every precondition. | Nothing. |
| 0078 (keep the pod's runtime listener across a session teardown) | Draft for review (2026-08-25 per its own `Date` line; the file's last commit, `9589aea54`, carries the same date, which records when someone touched it rather than when it was reviewed) | No deliverable of 0078 loses its subject. CODE-1, CODE-2, TEST-1 through TEST-7 and DOCS-1 all keep theirs, because this proposal opens neither `pkg/adapter/socketruntime.go` nor `cmd/lenny-adapter/main.go`. The file collisions are real, and all of them are in test files. The staged co-tenancy-hazard case adds a sibling assertion in `pkg/adapter/socketruntime_test.go` beside `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2` (`:252`), inside the block 0078's TEST-1 through TEST-4 and their cleanup conversion at `:258` and `:316` rewrite. 0078's CODE-1 replaces the `return p.listener.Close()` at `pkg/adapter/socketruntime.go:467` that the assertion's listener half records, so that half stops discriminating once 0078 lands, while its connection-close and child-kill halves stand and this proposal's own CODE-1 `started` gate is still required. This proposal also extends `tests/tier4_integration/concurrent_workspace_test.go`, whose cleanup at `:126` 0078's TEST-5 converts. And it edits `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`, the file 0078's TEST-7 rewrites: CODE-4 sets `unconditional_teardown` at every non-compensating `Shutdown` caller, so `TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` (`:232`) and `TestShutdownDrainRacesAnIncomingSession_spec_6_4` (`:316`) take that one-field edit at their four `ShutdownRequest` literals (`:269`, `:332`, `:373`, `:453`) and must keep passing. The two proposals' edit regions in that file are distinct, so the collision is a merge hazard rather than a design conflict. And it edits `tests/tier4_integration/concurrent_delegation_proxy_test.go`, whose cleanup at `:168` 0078's TEST-5 converts: the `ShutdownRequest` literal at `:424` takes the same one-field `UnconditionalTeardown: true` edit, and the two edit regions are distinct, so that collision is a merge hazard as well. The tier-4 assertion that the pod's listener survives holds on the shipped tree independently of 0078, because alice is still active when bob's compensation runs and `Close` takes the sibling early return (`pkg/adapter/socketruntime.go:441-446`). The row previously stated that 0078 widens the exposure by making a pod serve more sessions. 0078's own fixed decision states the opposite, that after it lands a recycling sidecar pod still serves one session and fails at the accept timeout, so that premise is withdrawn. | Land after this one. Preserve the `unconditional_teardown` field on those four `Shutdown` calls when TEST-7 rewrites the fixture's accept-timeout bound and the sequenced-leg assertion, because after this proposal lands the adapter answers a `Shutdown` carrying neither selector with `INVALID_ARGUMENT` and performs nothing. Amend the sibling assertion's listener half when 0078's CODE-1 removes the listener close. |
| 0072 (correct the inconsistencies the scenario authoring surfaced) | Draft for review (2026-08-13 per its own `Date` line; the file's last commit, `57427ee5f`, is dated 2026-08-26, which records when someone touched it rather than when it was reviewed) | **No deliverable of 0072 loses its subject.** SPEC-1, SPEC-2, SPEC-4 through SPEC-8 and CODE-1 through CODE-3 all keep theirs, because this proposal changes no checkpoint quiescence timeout, no Basic-level checkpoint statement, no eviction retry budget, no `awaiting_client_action` entry-path enumeration, no upload route, no metric label domain and no naming matcher. SPEC-5's premise in particular stands: it rests on §7.2's state-transition list, and this proposal's §7.2 edits are confined to the mid-resume snapshot-close block (a preamble sentence, a sentence appended to step 2, and step 3 replaced), leaving that list untouched. **Seven files are opened by both proposals, and every collision is a merge hazard rather than a design conflict.** Four of them collide inside the same section. In `spec/07_session-lifecycle.md` §7.3, 0072's SPEC-5 rewrites the `awaiting_client_action` **Entry paths** bullet (`spec/07_session-lifecycle.md:432`; 0072 cites `:431`, one line of drift) while SPEC-2 here appends a paragraph after the numbered list under `**Resume flow after pod failure:**` (`spec/07_session-lifecycle.md:402-414`). In `spec/15_external-api-surface.md` §15.1, 0072's SPEC-6 names the mid-session upload route in the endpoint and precondition tables while SPEC-5 here replaces four sentences of the `SETUP_COMMAND_FAILED` error-catalog row; in §15.4, 0072's SPEC-2 corrects the §15.4.3 integration-level text and its SPEC-8 corrects the §15.4.2 handshake sentence while SPEC-5 here adds a published bind-attempt block after the SDK-warm demotion contract. In `spec/16_observability.md` §16.1 and `docs/reference/metrics.md`, 0072's SPEC-7 adds the `reason` label to the `lenny_checkpoint_storage_failure_total` row (`spec/16_observability.md:203` and `docs/reference/metrics.md:193`; 0072 cites `:201` and `:191`, two lines of drift each) while SPEC-6 and CODE-9 here add one row per counter CODE-9 emits to those same two tables. The remaining three files are shared at distinct sections: `spec/04_system-components.md` (0072 in §4.4.3 and §4.4.5, this proposal in §4.1, §4.6.1, §4.7, §4.7.1 and §4.7.9), `spec/29_communication-scenarios.md` (0072 in §29.9, this proposal in §29.4), and `docs/reference/adapter-contract.md` (0072's SPEC-8 in the `Version Negotiation` section, now at `docs/reference/adapter-contract.md:446-452` against the `:426` it cites, and DOCS-2 here in the RPC table's `DemoteSDK` and `Shutdown` rows at `:64` and `:75`). Nothing here falsifies 0072's §1.6: the §7.4 upload pair sets `mid_session` on its `PrepareWorkspace` (`pkg/gateway/sessionserver/upload_to_session.go`), which is the mid-session route SPEC-6 documents. | Land in either order and resolve the shared files as a merge, re-running the tier-11 documentation reconciliation after the second lands. No content of 0072 needs re-deriving. Re-anchor 0072's four drifted citations (`spec/07_session-lifecycle.md:431`, `spec/16_observability.md:201`, `docs/reference/metrics.md:191` and `docs/reference/adapter-contract.md:426`) against the tree before it is applied, whichever proposal lands first. |
| 0079 (name who starts the next session's runtime on a recycled pod) | Draft for review (2026-08-25 per its own `Date` line; the file's last commit, `a5bf9db26`, carries the same date, which records when someone touched it rather than when it was reviewed) | **No deliverable of 0079 loses its subject.** SPEC-1 through SPEC-8, CODE-1 through CODE-4, TEST-1 through TEST-8 and DOC-1 through DOC-4 all keep theirs, because this proposal names no creator of a runtime process, stamps no pod label, opens neither `pkg/sandbox/podscrub` nor `pkg/controller/sandbox`, and edits `pkg/adapter/socketruntime.go` not at all. **Five spec files are opened by both proposals, and every collision lands on a different anchor.** In §4.7.9, 0079's SPEC-1 replaces step 7 while SPEC-2 here replaces step 5 and states that no other part of §4.7.9 changes. This proposal's other `spec/04` anchors (§4.1's request-message-scope sentence, §4.6.1's two claim-deletion bullets, the §4.7 `Shutdown` row and the new §4.7.1 bind attempt token block) are ones 0079 opens nowhere, and 0079's §4.7.10 runtime-process-lifetime paragraph and trade-off row (its SPEC-2) are untouched here. In §5.2, 0079 appends to the scrub procedure after step 6 and adds a sentence to step 1 (SPEC-3), replaces the **Recycling and integration levels** paragraph (SPEC-4) and edits the sizing text (SPEC-5), while SPEC-3 here appends to the `**Scrub model.**` paragraph and replaces the `**Slot cleanup:**` bullet's action-list sentence. In §6.2 both edit the same fenced state machine at different entries: SPEC-4 here replaces the `claimed ──→ draining` trigger list in the `Occupancy projection` group and rewrites three clauses of the projection prose, leaving the `Recycle edges` group untouched, while 0079's SPEC-6 qualifies the `claimed ──→ sdk_connecting` and `claimed ──→ reserved` edges in that group, adds a new `claimed ──→ draining` edge after the vm-restart drain edge, and appends to the §6.1 preConnect row. In §15.4, SPEC-5 here inserts its two blocks after the SDK-warm demotion contract (`spec/15_external-api-surface.md:1469`) and before the `#### 15.4.1` heading (`:1471`), while 0079's SPEC-7 replaces the recycling paragraph at `:1785`, inside §15.4.3. In §16.1, SPEC-6 here adds one catalog row per counter CODE-9 emits, while 0079's SPEC-8 extends the existing `lenny_gateway_pod_retirement_total` row's parenthetical with a `no_successor_runtime` reason value, which adds a label value rather than a series. **The two statements are compatible.** 0079 conditions pod reuse on the deployment model; the per-slot reclaim staged here is adapter-executed and holds on both models. 0079 in turn narrows one of the two harm classes this proposal fixes: a sidecar recycling pod that retires at its occupancy-zero boundary cannot carry a leaked entry into a later session. The co-tenant class, where the pod still holds another session at the moment of release, is untouched by it. | Land in either order, resolving the five shared spec files as a merge. Nothing of 0079 needs re-deriving. |
| gateway-runtime-comms remediation, step R1b | Programme step | SCHEMA-1 opens `schemas/lenny-adapter.proto` under rule S-2's second window rather than against R1b's reservation. S-2 reserves the first window to R1b and states that a later step needing a field the plan did not enumerate opens a second narrow window, whose precondition is that every in-flight `pkg/adapter` handler edit has merged first. R1b's end state is in the tree, and the file has already been reopened once, for proposal 0076's comment-only edit. The edit is additive, so the baseline R1b recorded is extended rather than reopened: no field is removed, none is renumbered, and no identifier R1b renamed is touched. The precondition binds this proposal's own step ordering, because it opens three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`), so SCHEMA-1 and its regenerated stubs land in one commit before those handler steps. | Record the second window against S-2, so the steps that plan against the generated types (R12, R15, R16, R17, R22, R23) plan against the regenerated set rather than against R1b's. |
| gateway-runtime-comms remediation, step R12 | Programme step | The hold-timeout reclaim of unstarted slots and its §10.1.4 statement are left to R12, which builds the control-stream consumer that arms the hold and owns the gateway-side whole-pod-loss response. | Take both halves when it builds the consumer, together with an in-flight-upload guard. |

## Deliverable index

- **SPEC-1** (`spec/04_system-components.md`, `spec/29_communication-scenarios.md`): §4.1's `ShutdownRequest` justification is restated per message, naming the requests that carry the attempt token and the one that carries the unconditional-teardown flag and stating that both are bare scalars with no wire presence; the §4.7 `Shutdown` row states the slot release and the runtime teardown as two teardowns with two preconditions, names the outcomes §4.7.1 defines, states the two-field precondition and its `INVALID_ARGUMENT` answer, and points at §4.7.1 for the named rules that decide the two teardowns and for the no-entry answer; §29.4's session-end step 13 restates the graceful-shutdown signal's co-tenancy condition and cites §4.7.
- **SPEC-2** (`spec/07_session-lifecycle.md`, `spec/06_warm-pod-model.md`, `spec/04_system-components.md`): §7.1 gains the failed-bind pod-side reclaim obligation as a paragraph of its own, covering the §15.1 start transition onto a pod serving concurrent sessions and the §7.3 re-attach onto a replacement pod, leaving the atomicity paragraph unedited, with the rule that the compensation carries the token its own attempt minted, the meaning of the superseded and absent answers, and the `leaked` disposition; the connection sentence becomes a statement of preference and carries no correctness load; §7.2's mid-resume snapshot-close sequence runs the reclaim before the replacement pod is released and drops the premise that no runtime was started on it; §7.3's resume flow, §6.2's mid-resume cancel edge and §4.7.9 step 5 point at it.
- **SPEC-3** (`spec/05_runtime-registry-and-pool-model.md`): §5.2's slot-cleanup action list names the slot's credential directory and the §4.9 timer cancellation; its scrub model covers the cleanup of a bind abandoned or failed after its slot enters `receiving_uploads` and before it reaches `running`, states that the cleanup reports no outcome, and states one cleanup-outcome report per session release; and it states the slot identifier's reclaim hold, from the deregistration of the registry entry until the cleanup finishes, together with the requests the hold refuses as a transient condition, the entry-creating ones named beside the predicate, and the windows that bound the cleanup's runtime close.
- **SPEC-4** (`spec/06_warm-pod-model.md`, `spec/04_system-components.md`): §6.2's per-slot sub-state machine gains the `receiving_uploads → slot_cleanup` edge and the pre-`running` cleanup paragraph, which points at §5.2's reclaim hold for what refuses a bind onto the slot's identifier while its cleanup runs, and every claim-deletion statement of the occupancy projection is re-keyed on the phase the pod projects at the claim DELETE and carries no recycle-setting qualifier, in §6.2's fence, in both claim-deletion clauses of §6.2's projection prose and in both bullets of §4.6.1's projection list, whose false closing sentence is deleted, with §6.2's no-claim clause qualified to a warm-inventory phase so that the widened claim-deletion clause and it do not both answer for a pod whose claim is deleted while it projects `claimed`, so the retirement the pre-`running` paragraph cites is the one the projection computes.
- **SPEC-5** (`spec/04_system-components.md`, `spec/15_external-api-surface.md`, `spec/06_warm-pod-model.md`): §4.7.1 gains the bind-attempt block after the RPC tables, stating what the caller mints and when, that the adapter stamps only the entry it creates and never an entry it resolved, the atomicity the resolve, the stamp and the comparison are performed under, the identity refusal, the started-entry refusal and its exemptions, the rules conditioning both on `mid_session`, the confirmation a start performs before it records the pod's shared runtime process as holding the session, and `Shutdown`'s two-field precondition; §15.4 is re-cut to name the §4.7.1 rule set, state what conformance against it means, and state the three non-conformances that are not any single rule's condition, with the slot-identifier reclaim-hold block carried forward; §15.1's `SETUP_COMMAND_FAILED` row states both of the setup-command request's deterministic `FAILED_PRECONDITION` causes and qualifies its setup-output remedy, and §6.2's pre-attached client-visibility clause is re-keyed on the setup-command request together with the gRPC code. §15.1's error catalog takes no new row for either code.
- **SPEC-6** (`spec/16_observability.md`): §16.1's metric catalog gains a row for each counter CODE-9 emits.
- **SCHEMA-1** (`schemas/lenny-adapter.proto`, `scripts/seed-claim-register.py`, `tests/claim-map.json`): two `ErrorCode` values, `bind_attempt` on the bind-sequence requests and on `ShutdownRequest`, `mid_session` on `PrepareWorkspaceRequest`, `unconditional_teardown` on `ShutdownRequest`, and the `SlotReclaimOutcome` enum with `ShutdownResponse.slot_reclaim`; no response reports a bind attempt; the claim-register rows for the new wire contract.
- **CODE-1** (`pkg/adapter/session.go`, `pkg/adapter/runtimegeneration.go`, `pkg/adapter/server.go`, `pkg/adapter/slot.go`): `Shutdown` enforces the two-field precondition, compares the named attempt against the entry under the registry lock before the deregistration deletes, answers `reclaimed`, `superseded` or `absent`, takes the reclaim hold with a deferred release, releases the slot for any entry the call removed, runs the runtime teardown only for a session whose start the adapter has admitted, and files a cleanup-outcome report only for a session the shared runtime process was given. Its `exited_cleanly` answer reports the slot-release failure for a reclaim the shared runtime process was never given, driven in test through the nil-defaulted `Server.removeSlotTreeFn` seam the reclaim path reads via `Server.removeSlotTreeVia`. The handler gains one exit after the two-field precondition, and the whole-pod recycle scrub the shipped handler runs as a trailing statement moves into it, so the scrub runs on every outcome, which is what both recycle senders need: the concurrent release's recycle request answers `absent` and the session-mode release's answers `reclaimed`. The shipped trailing copy is deleted in the same edit; leaving it in place while the exit helper also calls it starts two scrubs over one pod.
- **CODE-2** (`pkg/adapter/runtimegeneration.go`, `pkg/adapter/session.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`): `noteRuntimeStarted` takes the token its own claim observed and confirms the registry still holds an entry bound to this session carrying it. The confirmation binds every request that starts a session: `StartSession` (`pkg/adapter/session.go:163`) and `Resume` (`pkg/adapter/resume.go:144`) take the session back off the runtime and refuse the start when it does not, and the SDK-warm `ConfigureWorkspace` (`pkg/adapter/sdkwarm.go:261`) takes the runtime half of the §6.1 demotion, removes no registry entry, and refuses on `codes.Aborted`.
- **CODE-3** (`pkg/sandbox/slotstate/slotstate.go`): the per-slot edge list and its doc comment gain the `receiving_uploads → slot_cleanup` edge.
- **CODE-4** (`pkg/gateway/podlifecycle/podsession/slotbinder.go`, `binder.go`, `slotfailure.go`): the gateway mints an opaque token per bind attempt at `materializeSlot`, `Binder.Prepare` and `Binder.Resume`, carries it on every request that takes one, sets `unconditional_teardown` at every non-compensating `Shutdown` caller, sends the compensating `Shutdown` at every post-connection bind failure and at a failed `Resume`, suppresses it on either typed refusal, computes the leaked disposition from the RPC error and the clean-exit flag, which the teardown rules make sufficient, and releases the lease identifiers its own attempt minted.
- **CODE-5** (`pkg/gateway/sessionserver/start.go`, `pkg/gateway/podlifecycle/podclaim/slotclaimer.go`): one accounting helper serves every bind path the §7.1 obligation binds, so the create-time reserved path and the §7.3 re-attach reach the §5.2 threshold, and `isTransientPodClaimError` gains the `codes.Aborted` arm that serves the reclaim hold's refusal, the identity refusal and the start rollback alike.
- **CODE-6** (`pkg/adapter/slot.go`, `pkg/adapter/slotsession.go`, `pkg/adapter/server.go`, `pkg/adapter/staging.go`, `pkg/adapter/slotcreds.go`, `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go`, `pkg/adapter/holdstate.go`, and one new file): `slotState.bindAttempt`, the resolve descriptor the three callers pass, the `validateBindFields` check each handler that carries the fields runs at its entry before the resolve, the predicate that creates and stamps or refuses on identity or on an already-started session under `s.mu`, the rule that a mid-session request never creates, the per-slot guard over every section that writes or destroys a slot's tree outside `s.mu`, derived section by section rather than enumerated, and the reclaim hold with the `reclaimSlotLocked` helper every deregister-then-destroy site routes through. In `holdstate.go` the §10.1.4 pass's single ten-second context becomes its guard-acquisition deadline alone and each member mints its own ten-second close context, so one member's guard wait cannot spend a later member's final usage report or runtime close. The hold is tested at two sites: at the guard acquisition, so a guarded admission RPC is refused rather than left to block for the whole cleanup, and inside the resolve, which is where the unguarded RPCs meet it.
- **CODE-7** (`pkg/gateway/runtime/adapterclient/client.go`): the translation from the gRPC status detail to the two sentinel errors the gateway matches with `errors.Is`.
- **CODE-8** (`pkg/gateway/podlifecycle/podsession/binder.go`): `Binder.Prepare`'s and `Binder.Launch`'s reclaim closures take the failing error as a parameter and skip `failPhase` on either typed refusal, so the call site returns the refusal and the pod is not drained. `failPhase` drains on every call.
- **CODE-9** (`pkg/observability/metrics/catalog.go`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go`, `pkg/adapter/metrics.go`, `docs/reference/metrics.md`, `pkg/gateway/podlifecycle/podsession/binder.go`, `pkg/gateway/podlifecycle/podsession/slotbinder.go`, `cmd/lenny-gateway/metricsbackfill.go`, `tests/tier11_docs/slot_compensation_metric_reference_test.go`): the counters for a compensation answered `superseded` and a `Shutdown` that met an entry carrying no token. The superseded series is emitted through a `SlotReclaim` hook on `Binder` beside `SlotFailure`, forwarded by `Binder.noteCompensationOutcome` and wired in the gateway's metrics backfill, and it alone takes a `catalog.go` row and a `spec161Metrics` entry; the adapter series is registered in `pkg/adapter/metrics.go` and gated by the shipped adapter metric-catalog sweep.
- **CONF-1** (`tests/tier3_contract/adapter_bind_attempt/`, `tests/tier10_conformance/`): a case per named §4.7.1 rule, as a descriptor gate and bufconn handler cases at tier 3 and an in-process battery at tier 10.
- **DOCS-1** (`docs/reference/state-machines.md`): the per-slot sub-state table gains the row matching the §6.2 edge, and the pod state machine paragraph takes the same three projection clause replacements SPEC-4 makes in §6.2, each re-keyed on the phase the pod projects at the claim DELETE.
- **DOCS-2** (`docs/reference/adapter-contract.md`): the `Shutdown` row states the two teardowns with their two preconditions, the withheld cleanup-outcome report, the drain gate's own condition, the two-field precondition and the clean-exit answer; and the `DemoteSDK` row states that the demotion drops the adapter's slot registry entry.
- **DOCS-3** (`docs/reference/error-catalog.md`): the published `SETUP_COMMAND_FAILED` row takes the four replacements that match SPEC-5's §15.1 edits, stating the refusal of the setup-command request beside the setup-command failure cause, its non-retryability, the setup-output remedy qualified to the cause that ran a command, and the retryable-fallback sentence re-keyed from the cause class onto an enumerated set so the superseded refusal is inside it, and its remedy cell is replaced whole. The page takes no new row for either adapter error code, and no gate holds it to §15.1.

Tests are not separate deliverables, with one exception. Each implementation step carries the
tests for the tiers it reaches, specified per deliverable under `## Testing` in the non-spec
changes file. CONF-1 is listed above because a conformance battery for a contract §15.4
publishes to third-party adapter authors is the deliverable that makes that contract
checkable, rather than the test coverage of another deliverable.
