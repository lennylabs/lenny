# Non-spec changes: A failed session bind leaves a stale adapter slot registry entry

These changes are staged. Applying them is the implementation's work, under the
implementation checklist. Every code deliverable here lands after the spec deliverables it
implements.

## Design (implementation-facing)

The mechanism is one conditional on each side and one RPC between them.

**Adapter.** `Shutdown`'s clause two already deregisters unconditionally and gates the whole
teardown on `bound := removed && st.sessionID != ""`. That single predicate is wrong in both
directions. It excludes a registered-but-unbound entry, so the on-disk tree and the
credential directory survive the registry removal. It includes a bound-but-unstarted entry,
so a reclaim at the credential-assignment stage would run `Runtime.Close` for a session the
runtime was never given. The predicate splits three ways: the slot release runs on `removed`,
the runtime teardown runs on `started`, and the cleanup-outcome report runs on `runtimeLive`
membership, which is §6.2's `running` boundary and the moment §5.2 gives the report.

**Gateway.** Every residue-producing stage lives inside `materializeSlot`, which both entry
paths reach, and the adapter connection is live at each failure site and closed before the
error returns. One wrapper around `materializeSlot` therefore covers both paths and leaves no
stage a later edit can forget. The wrapper sends `Shutdown` for the session on that
connection, on a context detached from the caller's, budgeted by §5.2's per-slot cleanup
timeout, records the outcome on the `*SlotBindError` it is about to return, and releases the
session's gateway-side §4.9 credential leases, which `assignSlotCredentials` minted inside
the stages it wraps. `Binder.Resume` has the identical structure and takes the same
compensating `Shutdown`. It releases no credential lease, because that attempt mints none and
its §7.3 retry re-mints none. The outcome then reaches `SlotClaimer.ReleaseSlot`'s existing
`leaked` parameter, which already implements §6.2's disposition.

**The race the design opens, and its guard.** The compensation fires on a `StartSession` or a
`Resume` whose deadline expired, so it routinely races an adapter handler still between
`claimSessionSlot` (which sets `started`) and `noteRuntimeStarted`. Without a guard the
reclaim deletes the entry, `Runtime.Start` then succeeds, and `noteRuntimeStarted` records a
session in `runtimeLive` that the registry no longer holds: the third residue class by a new
route, with `runtimeIdleLocked` false for the life of the pod. The race pre-exists this
proposal, because `claimSessionSlotUnderLock` sets `st.sessionID` and `st.started` together
before `Runtime.Start` and §11.4's full revoke already sends `Shutdown` for a non-terminal
session; the compensation turns a narrow window into the expected case. `noteRuntimeStarted`
therefore confirms the slot survived at the epoch its own claim observed, and the start
rolls back when it did not. The confirmation catches both orderings. When the reclaim lands
after the start's claim the entry is gone. When the reclaim's answer precedes that claim and
the claim re-creates the entry, the re-created entry carries a fresh epoch, so a predicate
reading only `st.sessionID` would see a bound entry and pass while an epoch predicate
refuses. That second ordering is the ABA case, and it is reachable only because `SlotID ==
SessionID` makes both attempts present the same string.

**The bind epoch and the reclaim hold.** Two orderings have to close and one mechanism
closes neither alone. The **bind epoch** makes a late reclaim refusable: the adapter mints
one per registry entry, reports it on the responses of the RPCs that create or resolve an
entry, and refuses a `Shutdown` that names a different one. That covers the reclaim
dispatched after the client's context expired, after the client retried, and after the retry
took the slot cleanly, at which point the reclaim has not yet reached the adapter so no
exclusion can have been taken. The **reclaim hold** makes an admitted reclaim exclusive while
it tears state down: `removeSlotTree` deletes paths derived from the slot identifier and
`SlotID == SessionID` makes that identifier equal across attempts, so the destructive steps
read no registry state and the deregistration alone does not keep a successor out of them.
The hold is taken in the same critical section as the deregistration and released on every
return path, and while it is held the adapter refuses a bind onto the identifier as a
transient condition. The epoch reaches the compensation because
`compensateFailedSlotBind` runs on `context.WithoutCancel(ctx)`: the case it exists for is a
caller whose context already expired, so the client may already be retrying before the
reclaim is dispatched, and a hold alone cannot close that ordering.

**The cross-attempt window, and why the retry moves pods.** `SlotID == SessionID` on every
path and §5.2 placement prefers a pod already hosting the tenant's slots, so a retry re-uses
the identifier and can land on the same pod, while the adapter runs `removeSlotTree` outside
`s.mu`. A compensation whose deadline expires mid-`RemoveAll` would let the retry's freshly
created tree be deleted under it, or would tear the retry's session down once it is running.
The remedy is the narrower of the two available: the attempt keeps the §5.2 retry, and the
pod whose reclaim the adapter did not acknowledge is excluded from carrying it. SPEC-2 states
that constraint in §5.2's `**Max retries:**` bullet alone; §7.1 states the reclaim and its
`leaked` disposition and no placement rule. The constraint therefore reaches the retries
`applySlotRetryPolicy` places and not the create-time-reserved `/start` path, which
`bindConcurrentSlot` routes through `BindReservedSlot` against the row's own
`PodAssignment`. SPEC-2's edge-case list records that path as an accepted failure mode. The
exclusion rides on the request
structs the bind path already carries, as `ExcludePods`, which `applySlotRetryPolicy`'s retry
iteration appends to and `ClaimSlot`'s two candidate passes honour as a read-only placement
filter. No failure class becomes non-retryable, so §5.2's non-retryable categories
and its client-error contract are untouched, and when the excluded pod is the pool's only
candidate the retry meets the `WARM_POOL_EXHAUSTED` outcome with `details.reason:
"concurrent_slots_exhausted"` that the shipped claim path already returns.

**What the change costs.** One additive proto edit: an enum and nine fields, no field
removed, no field renumbered, no RPC added, so `buf breaking` has nothing to fire on. `Shutdown`
clause two remains the single removal entry point and the epoch is a precondition on it. No
new frame, flag, metric, or operator-tunable. On the gateway side: one in-process error
field, one in-process request field carrying §5.2's placement constraint for the pods holding
an unacknowledged reclaim, one existing parameter threaded to two more call sites, a
per-connection epoch latch, and one predicate split.

The proto edit lands under rule S-2's second window rather than against it. S-2 reserves the
first window to R1b and states that a later step needing a field the plan did not enumerate
opens a second narrow window, whose precondition is that every in-flight `pkg/adapter`
handler edit has merged first. R1b's end state is in the tree, and the file has already been
reopened once, for proposal 0076's comment-only edit. The precondition binds this proposal's
own ordering rather than blocking it, because this proposal opens three of S-2's covered
handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`): SCHEMA-1 and its regenerated
stubs land in one commit before those handler steps.

## Staged code changes

### CODE-1 · pkg/adapter/session.go — `Shutdown` refuses a reclaim naming another attempt's epoch, holds the slot identifier while it tears state down, releases the slot for any entry removed, and tears the runtime down only for a started session

Targets:

- `Shutdown`'s clause two: the `bound := removed && st.sessionID != ""` gate, the `if bound {
  … }` block, and the response construction.
- `Shutdown`'s doc comment.
- `pkg/adapter/slotsession.go` — `deregisterSlotLocked`'s doc comment only. No body change:
  it already cancels every armed expiry timer on removal.
- `pkg/adapter/runtimegeneration.go` — a `runtimeHoldsLocked` accessor beside
  `runtimeIdleLocked`, reporting whether `runtimeLive` holds the named session. Callers hold
  `s.mu`. It reads state that already exists and adds none: `noteRuntimeStartedLocked` is the
  only writer that sets membership and `noteRuntimeClosed` the only one that clears it.
- `pkg/adapter/bindepoch.go` — the epoch comparison and the reclaim hold this handler uses
  are CODE-6's, which lands first. This deliverable inserts into them and defines none of
  them.

Clause two becomes, with the epoch comparison, the deregistration, the drain decision, and
the hold insertion all inside the same two critical sections `s.mu` already carries. No I/O
runs under the lock on either: the drain, the close, the tree removal, and the scrub report
all run with `s.mu` released, as they do today. The hold is a map entry rather than a held
mutex, and §5.2's per-slot cleanup budget bounds how long it stands:

```go
// spec: §4.7 — a reclaim naming a bind epoch releases the slot only when
// the entry the adapter holds carries that epoch. Both refusals remove
// nothing and are successful answers: ABSENT for a session the adapter
// holds no entry for, SUPERSEDED for one a later bind attempt holds. A
// zero epoch is the unconditional teardown, which is what every caller
// other than a failed bind's compensation sends.
epoch := req.GetExpectedBindEpoch()

s.mu.Lock()
if epoch != 0 {
    cur, ok := s.slotStateLocked(sessionID)
    switch {
    case !ok:
        s.mu.Unlock()
        return &adapterv1.ShutdownResponse{
            ExitedCleanly: true,
            SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT,
        }, nil
    case cur.epoch != epoch:
        s.mu.Unlock()
        return &adapterv1.ShutdownResponse{
            ExitedCleanly: true,
            SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED,
        }, nil
    }
}
st, removed, boundRemains := s.deregisterSlotLocked(sessionID)
// spec: §4.7 — the runtime teardown runs for a session whose start the
// adapter has admitted. st.started is that predicate: it is set inside
// claimSessionSlotUnderLock's critical section, which every start RPC
// enters, before Runtime.Start, so gating on it fails closed on a start
// still in flight, which is torn down rather than skipped.
started := removed && st.started
// spec: §5.2 — the cleanup-outcome report is owed only by a slot that
// reached §6.2's `running`, which is runtimeLive membership, because
// noteRuntimeStarted records it after Runtime.Start returns. Gating the
// report on st.started would report for a session this very reclaim is
// about to take back off the shared runtime process, advancing the pod's
// served-session count for a session it never ran.
live := removed && s.runtimeHoldsLocked(sessionID)
// spec: §5.2 — the slot identifier stays occupied until this cleanup
// finishes, because removeSlotTree and Runtime.Close resolve from the
// identifier rather than from the entry this call just removed. The hold
// is taken in the same critical section as the deregistration, so no bind
// can be admitted between the two, and the release is deferred rather
// than written at each return: Runtime.Close reaches three
// implementations and a child process and removeSlotTree reaches the
// filesystem, so a panic out of either would otherwise hold the
// identifier for the life of the pod. It registers inside the `if
// removed` block, so it is armed exactly when a hold was inserted.
if removed {
    s.holdReclaimLocked(sessionID)
    defer s.releaseReclaim(sessionID)
}
s.mu.Unlock()

closeErr := error(nil)
if started {
    s.emitFinalUsage(ctx, sessionID)
    if !boundRemains {
        s.drainViaLifecycle(req.GetDeadlineMs(), req.GetReason())
    }
    if s.Runtime != nil {
        closeCtx, cancel := contextWithGraceDeadline(ctx, time.Duration(req.GetDeadlineMs())*time.Millisecond)
        closeErr = s.Runtime.Close(closeCtx, sessionID)
        cancel()
    }
    s.noteRuntimeClosed(sessionID)
}

// spec: §4.7 — the slot release runs for any entry the call removed,
// bound or not. It follows the drain and the close so the agent process is
// not reading a credential file the teardown has already removed inside the
// §15.4.2 grace window. removeSlotTree reaches the slot's credential
// directory, so widening it to `removed` is what reclaims
// /run/lenny/slots/{sessionId}/credentials.json for a registered-but-unbound
// entry; deregisterSlotLocked already cancelled the §4.9 expiry timers.
treeErr := error(nil)
if removed {
    treeErr = removeSlotTree(st)
    s.cancelPodMCPIfRuntimeIdle()
}

if live {
    s.reportSessionScrub(ctx, sessionID, closeErr)
}
```

The teardown gate and the report gate are captured in one critical section and never
diverge afterwards. `started`
over-approximates toward closing and `live` under-approximates toward not counting, so every
interleaving of a reclaim with a start yields zero or one cleanup-outcome report for the
session: a reclaim before the claim removes nothing and reports nothing; a reclaim while the
start is in flight closes the runtime and reports nothing, and CODE-2's `noteRuntimeStarted`
then refuses the record; a reclaim after `noteRuntimeStarted` recorded reports exactly once,
for a session the runtime genuinely held. `RecordSessionScrub` increments the pod's
served-session count before it branches on the leak flag and holds no per-session dedup
(`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481`), so the
one-report rule is an adapter-side invariant rather than something the gateway can absorb.

and the response becomes:

```go
outcome := adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT
if removed {
    outcome = adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED
}
return &adapterv1.ShutdownResponse{
    ExitedCleanly: closeErr == nil && (live || treeErr == nil),
    SlotReclaim:   outcome,
}, nil
```

`slot_reclaim` is populated on the unfenced form as well as the fenced one, so the gateway
reads one field on every answer and never has to infer an outcome from the request it sent.

`exited_cleanly` carries one rule, keyed on the same §6.2 `running` boundary the rest of this
change installs: the response reports a clean exit when the runtime close succeeded and, for a
slot the pod's shared runtime process was never given, when the slot release also completed.
The gate is `live` rather than `st.started` because `st.started` is set inside
`claimSessionSlotUnderLock` before `Runtime.Start` runs, so a start still in flight is
`started` and pre-`running`, and gating on `started` would discard the tree-removal error for
exactly the reclaim §7.1 sends on a cancelled context. Reading `live` leaves the session-end
classification untouched: every path that admits a start reaches `noteRuntimeStarted`
unconditionally on success (`pkg/adapter/session.go:163`, `resume.go:144`, `sdkwarm.go:261` on
the freshness arm), and the two `noteRuntimeClosed` callers outside `Shutdown`
(`pkg/adapter/holdstate.go:251`, `sdkwarm.go:297`) each remove the registry entry in the same
pass, so a session the runtime was given and that reaches an ordinary session end is in
`runtimeLive` when its `Shutdown` arrives and answers on `closeErr` alone as it does today. A
reclaim of a slot the runtime was never given, whether the start was never admitted or is still
in flight, surfaces the tree-removal error, because CODE-4 has no other signal that the reclaim
did not complete.

Doc-comment work on `Shutdown`:

- State the epoch comparison first, because it is the handler's new outermost branch: a
  request naming an epoch releases the slot only for the entry carrying it, the two refusals
  are successful answers that remove nothing, and a request naming no epoch is the
  unconditional teardown. Name the two refusals' outcomes so a reader of the handler does not
  have to open the proto to learn what `SUPERSEDED` and `ABSENT` mean to the gateway.
- State the hold and why it is `defer`red, in the terms the code comment above carries: the
  destructive steps resolve from the slot identifier rather than from the entry, so the
  deregistration alone does not keep a successor out of them, and a panic out of
  `Runtime.Close` or `removeSlotTree` would hold the identifier for the life of the pod if
  the release were a statement at each return.
- State the handler as two teardowns with two preconditions, matching the §4.7 row, and name
  `releaseSessionSlot` (`pkg/adapter/slotsession.go:214-220`) as the shipped statement of the
  unstarted branch's semantics, so the two compensating paths read as one rule.
- State the cleanup-outcome report's own gate beside them, because it is neither teardown's:
  §5.2 gives a session release at most one report and gives it to the cleanup that reclaimed
  the slot, and only a slot that reached `running` is owed one, so the report reads
  `runtimeLive` membership while the teardown reads `st.started`. State the response's own
  predicate in the same place: it is `runtimeLive` membership too, so the handler holds three
  predicates and the report and the response share one of them. Say why the response cannot
  share the teardown's: `st.started` is true for a start still in flight, which is the reclaim
  whose incomplete slot release the gateway has no other way to observe.
- Record why the runtime teardown must not run for an unstarted session, in the form the tree
  supports rather than the form that is easy to write. `Close` is not uniformly session-scoped
  across the implementations: `InProcessRuntime.Close` (`pkg/adapter/embedded.go:188-204`) and
  `MCPRuntime.Close` (`pkg/adapter/mcpruntime.go:266-291`) ignore the session identifier and
  tear the runtime down on any call, and `SocketRuntimeProcess.Close` tears down the shared
  connection, the spawned child, and the never-rebound listener whenever the active set empties
  (`pkg/adapter/socketruntime.go:435-467`, listener bound once at `:156-161`). The socket
  implementation's early return at `:441-446` makes it harmless while a co-tenant is genuinely
  active; the state it is not harmless in is produced by `Interrupt` of the last active
  session, which clears neither `connected` nor `conn` (`:398-417`).
- Lead with the harm that fires without any of that. For a bound-but-unstarted entry today's
  handler sends the §15.4.2 signal whenever no other bound entry remains (`session.go:259-261`),
  which a registered-but-unbound co-tenant about to call `StartSession` does not hold off, and
  emits a `ReportSessionScrub` that advances `sessionsServed` for a session the pod never ran.
- Note that `cancelPodMCPIfRuntimeIdle` under `removed` is safe: it is double-guarded by
  `runtimeIdleLocked` and `mcpArmingHeldLocked` (`pkg/adapter/slotsession.go:238-260`), so a
  shutdown of an unbound entry on a pod whose armed session still holds a slot cancels nothing.

`deregisterSlotLocked`'s doc comment gains one sentence recording that its unconditional timer
cancellation is now relied on by the unbound path as well as the bound one.

### CODE-2 · pkg/adapter/runtimegeneration.go, pkg/adapter/session.go, pkg/adapter/resume.go — a start confirms its slot survived at its own bind epoch before recording the runtime as holding the session

`noteRuntimeStarted` gains the confirmation rather than a fourth entry point beside it:

```go
// noteRuntimeStarted records that sessionID has been given to the pod's one
// shared runtime process, and reports whether the record was taken. It runs
// immediately after a successful start. It refuses in the two states a §7.1
// reclaim leaves: the registry holds no entry bound to this session, or it
// holds one at a different bind epoch. Recording in either would put a
// session in runtimeLive that the registry does not hold under this
// attempt's identity, holding runtimeIdleLocked false and soleSession empty
// for the life of the pod.
//
// The second state is the ABA case. SlotID == SessionID, so a successor
// attempt at the same session re-creates an entry under the same key with
// the same sessionID, and a predicate reading only st.sessionID cannot see
// that the entry belongs to a later attempt. The epoch can, because
// ensureSlotStateLocked mints a fresh one per entry.
//
// epoch is the value this start's own claim observed, passed in rather than
// re-read here: a read taken after the claim is as racy as the confirmation
// it anchors. The predicate reads the registry entry rather than st.started,
// because st.started is set before Runtime.Start and is therefore true for
// this very call.
//
// spec: §7.1 (the reclaim and the start that races it); §4.7 (the bind
// epoch); §15.4.3.
func (s *Server) noteRuntimeStarted(sessionID string, epoch int64) bool {
    if sessionID == "" {
        return false
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    st, ok := s.slots[sessionID]
    if !ok || st.sessionID != sessionID || st.epoch != epoch {
        return false
    }
    s.noteRuntimeStartedLocked(sessionID)
    return true
}
```

`claimSessionSlot` and `claimSessionSlotUnderLock` report the epoch they captured inside the
critical section that claimed the slot, so the value the confirmation compares is the one the
claim itself observed. Their callers hold it across `Runtime.Start` and pass it here.

`StartSession`'s call site (`pkg/adapter/session.go:163`) takes the rollback:

```go
if !s.noteRuntimeStarted(sessionID, claimedEpoch) {
    // spec: §7.1 — the reclaim removed this slot while Runtime.Start ran.
    // Take the session back off the shared runtime process and refuse the
    // start. No cleanup outcome is reported: the slot never reached §6.2's
    // `running`, and §5.2 gives a session release at most one such report,
    // filed by the cleanup that reclaimed the slot, which withheld it for
    // the same reason. The registry is left alone: the reclaim already
    // removed this session's entry and its tree, and any entry standing
    // under this slot identifier now belongs to a later attempt at the
    // same session, whose workspace and credentials this rollback must
    // not delete. The pod-wide MCP surface is the one thing the rollback
    // still reclaims, and that call declines to cancel a surface a
    // surviving claimant holds.
    if s.Runtime != nil {
        _ = s.Runtime.Close(ctx, sessionID)
    }
    s.cancelPodMCPIfRuntimeIdle()
    // §16.3: a lost race is the TRANSIENT category. A further attempt
    // succeeds once the reclaim's residue is gone.
    rollbackErr := status.Errorf(codes.Aborted,
        "session %s slot was reclaimed while the start was in flight", sessionID)
    spanErr = tracing.CategorizeError(rollbackErr, tracing.CategoryTransient)
    return nil, rollbackErr
}
```

The rollback deregisters nothing, and that is deliberate. `noteRuntimeStarted` refuses exactly
when the registry holds no entry under this slot identifier, holds one whose `sessionID`
names a different session, or holds one at a different bind epoch, because `st.sessionID` is set to the map key by both of its
production writers (`pkg/adapter/slotcreds.go:34`, `pkg/adapter/slotsession.go:87`) and
cleared by neither. A release keyed on the session identifier alone therefore either removes
nothing, which is the interleaving where the reclaim already took the entry and the tree, or
removes a later attempt's entry. The second case is reachable rather than theoretical:
`ensureSlotPaths` (`pkg/adapter/slot.go:140-148`) creates an entry with an empty `sessionID`
for every workspace-preparation RPC, so a §5.2 retry staging its workspace on the same pod
holds precisely that entry, and `releaseSessionSlot` there would delete it and `RemoveAll`
the tree, the uploads, and the credential directory the retry had just staged
(`pkg/adapter/slotsession.go:214-220`, `slot.go:210-212`). The rollback therefore runs
`cancelPodMCPIfRuntimeIdle` alone, which is the half it wants and the half that is already
safe against a successor: the cancellation is gated on `mcpArmingHeldLocked`, which reads
`s.slots[s.mcpSession]`, so a surface a surviving claimant holds is not cancelled
(`pkg/adapter/slotsession.go:238-260`).

The rollback answers `codes.Aborted`, and the choice of code is deliberate. The
gateway derives the §5.2 failure category from the gRPC code the failing stage returned:
`SlotBindError.Reason()` maps a `FailedPrecondition` outside the workspace stages to
`policy_rejection` (`pkg/gateway/podlifecycle/podsession/slotfailure.go:91-99`), which
`NonRetryable()` reports true for (`:41-48`), and the start stage this rollback fails is minted
as `slotFailureSessionStart` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:322-324`,
`binder.go:293`). Answering `FailedPrecondition` here would therefore end the attempt at a 422
`SLOT_FAILED` carrying `retryable: false` and no `Retry-After`, with the §5.2 retry budget
unconsumed, for a failure a further attempt clears. `Aborted` takes the classifier's
transient default (`slotfailure.go:100-101`), so `applySlotRetryPolicy` still places its retry
and `classifySlotBindFailure` passes the error through with the retryable `STARTING_FAILED`
envelope intact on the create-time-reserved path (`pkg/gateway/sessionserver/start.go:2766-2779`,
`:2873-2879`). No change is staged to that switch. `Aborted` is also the adapter's established
word for a concurrency abort the caller retries at a higher level, which is what this failure
is: `Server.Checkpoint` answers it for a coalesced or busy operation lock
(`pkg/adapter/checkpoint.go:115`, `pkg/adapter/oplock.go:36-40,:82`). It is unused on the
`StartSession` and `Resume` handlers, where `FailedPrecondition` already refuses an
unconfigured runtime (`pkg/adapter/session.go:104`) and an unconfigured workspace base or
checkpoint transport (`pkg/adapter/resume.go:33,:42`), and it is unused across the wider
adapter session surface, where the same code refuses an unbound session
(`pkg/adapter/slotsession.go:274-283`) and a stale coordination generation
(`pkg/adapter/coordination.go:133,:281`). The rollback therefore adds no further meaning to a
code the adapter has already overloaded.

Scope of the call-site change:

- The compensation can race any RPC that admits a start on a slot the gateway may reclaim, and
  CODE-4 stages it at two such sites. Both take the rollback. `pkg/adapter/session.go:163`
  (`StartSession`) is the site `materializeSlot`'s start stage reaches, because that stage is
  `cl.StartSession`. `pkg/adapter/resume.go:144` (`Resume`) is the site `Binder.Resume`'s
  compensating failure branch reaches, and the adapter's `Resume` runs the same claim, start,
  record sequence: it claims the slot at `pkg/adapter/resume.go:50`, calls `Runtime.Start` at
  `:140`, and records at `:144`. The resume rollback is the same three steps in the same order,
  on the inbound `ctx` as the `StartSession` one is: close the runtime for that session, run
  `cancelPodMCPIfRuntimeIdle`, and answer `codes.Aborted`, for the classification reason the
  `StartSession` rollback records above. It takes no span-error categorization, because
  `Server.Resume` opens no span at all (`pkg/adapter/resume.go:25-35`) and so has no `spanErr`
  to set. It leaves the registry alone and reports no cleanup outcome either, for the reasons
  the `StartSession` rollback gives.
- `pkg/adapter/sdkwarm.go:261` becomes `_ = s.noteRuntimeStarted(sessionID, claimedEpoch)` with
  no rollback, because
  no compensation races it: its only gateway caller is `Binder.Launch`
  (`pkg/gateway/podlifecycle/podsession/binder.go:1009`), the exclusive path CODE-4 does not
  compensate and whose `failPhase` retires the pod. That site in particular must not take a bare
  `Runtime.Close`: its own failure idiom is `releaseSessionSlot` with the §6.1 `DemoteSDK`
  fallback, and a bare close there leaves `s.sdkConnected` true, which only `DemoteSDK` clears.
- Every test caller becomes `_ = s.noteRuntimeStarted(sessionID, epoch)`, taking the epoch from
  the claim it already makes, and every test caller must hold a bound registry entry when it
  records, because the guard now keys the record on that entry and its epoch.
  `pkg/adapter/export_test.go:45` (inside `ClaimSessionForTest`, after `claimSessionSlot`),
  `usage_test.go:233` (after `bindSessionForTest`), and
  `podmcp_arming_internal_test.go:84,185,230` (each after `claimSessionSlot("alice", …)`)
  already satisfy that and need only the discard. `adapterevents_test.go:95` and `:184` do not,
  because both build a bare `New("served")` with nothing in `s.slots`, so both gain a
  `bindSessionForTest(t, s, …)` call before the record; that helper is in the same package and
  sets the workspace base itself when it is empty (`pkg/adapter/usage_test.go:350-363`). The
  first of the two is the one that goes red without it:
  `TestAdapterEventsEmitsControlEvents_spec_4_7` asserts the `soleSession` stamp on its control
  events (`pkg/adapter/adapterevents_test.go:104-106`), `emitControlEvent` fills an empty stamp
  from `soleSession` (`pkg/adapter/adapterevents.go:153-155`), and `soleSessionLocked` answers
  the empty string unless `runtimeCohort` is exactly one
  (`pkg/adapter/runtimegeneration.go:83-88`), so a refused record empties the stamp the test
  asserts.

### CODE-3 · pkg/sandbox/slotstate/slotstate.go — the per-slot edge list gains the pre-`running` cleanup edge

`ValidTransitions()` gains `{ReceivingUploads, SlotCleanup}`, and the doc comment's edge list
above it gains the matching line:

```go
//	receiving_uploads → slot_cleanup    (bind abandoned before the runtime is given the session)
```

The edge list is the authoritative transcription of §6.2's fence, so this lands in the same
step as the §6.2 edit. `IsValid(SlotAssigned, Running)` stays illegal.

### CODE-4 · pkg/gateway/podlifecycle/podsession — the gateway compensates every post-connection bind failure and every failed resume, carrying the bind epoch and mapping the outcome first

Targets:

- `slotfailure.go` — `SlotBindError` gains a `Leaked bool` field.
- `slotbinder.go` — a new `slotCleanupBudget` helper and a new `compensateFailedSlotBind`
  method; `materializeSlot` splits into a stage runner and a compensating wrapper;
  `ReleaseSlotReservation` takes the disposition; `BindReservedSlot` and `ClaimSlot`'s
  connect-stage release pass it.
- `binder.go` — `Binder.Resume`'s adapter-RPC failure branch compensates before `cl.Close()`,
  `releaseResumeSlot` takes the disposition and returns its release error, and the branch
  returns its error with a `SlotBindError` in the chain so the §7.3 re-attach's disposition
  reaches CODE-5's accounting.

The budget:

```go
// slotCleanupBudget is the §5.2 per-slot cleanup timeout,
// max(cleanupTimeoutSeconds / maxConcurrentSessions, 5) seconds. §5.2 assigns
// that figure to the adapter's own cleanup enforcement; the gateway reuses it
// as its own give-up bound on the compensating Shutdown, and pins half of it
// as the adapter's graceful window, rather than inventing a constant.
// cleanupTimeoutSeconds is optional, so an unset pool yields the 5s floor.
// spec: §5.2 (slot cleanup); §7.1 (the reclaim obligation).
func slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32) time.Duration
```

The compensation:

```go
// compensateFailedSlotBind reclaims the pod-side state a failed bind created,
// on the connection the failed stage still holds, and reports whether the
// slot must be released as leaked. It touches pod-side state only. The
// gateway-side §4.9 credential leases belong to the caller, because the two
// bind entry paths mint them inside materializeSlotStages and the resume path
// mints none.
//
// The context is detached from the caller's: the residue class this exists for
// arises when the caller's context expired during StartSession, so a reclaim
// issued on that context would fail in the one case that leaves a runtime
// running for an abandoned session.
//
// The call carries two bounds and they differ deliberately. The fourth
// argument is the graceful window the adapter spends on the runtime close,
// and the RPC deadline is the budget above, which outlasts it so the gateway
// observes the reclaim's real outcome instead of recording a completed
// cleanup as leaked. The shipped §11.4 revoke fan-out holds the same
// relation, with userTerminateRPCTimeout bounding the call and the shorter
// userTerminateDeadline sent as the graceful window.
//
// The reclaim is sent on the connection the failed stage still holds and
// never dials a new one. That rule is load-bearing rather than
// incidental: the bind epoch the call carries is pod-local and is latched
// off the responses this connection returned, so a compensation that
// re-dialled would be free to run unfenced against a pod that may already
// hold a successor's slot.
//
// spec: §7.1 (the reclaim obligation, its leaked disposition, and the
// no-re-dial rule); §4.7 (Shutdown's two teardowns, the bind epoch, and
// the three reclaim outcomes); §5.2 (the budget).
func (b *Binder) compensateFailedSlotBind(ctx context.Context, cl *adapterclient.Client, req SlotBindRequest, sandboxName, slotID string) bool {
    budget := slotCleanupBudget(req.CleanupTimeoutSeconds, req.MaxConcurrentSessions)
    rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
    defer cancel()
    // The reclaim names the bind epoch this attempt observed. BindEpoch()
    // answers zero for an attempt that received no epoch-bearing response,
    // which is the unconditional form §4.7 states.
    cleanly, outcome, err := cl.ShutdownReclaim(rctx, req.SessionID, "slot_bind_failed", budget/2, cl.BindEpoch())
    switch {
    case err != nil:
        // No answer at all. The slot is leaked under §6.2's disposition.
        log.Printf("podsession: reclaim slot %s on pod %s after failed bind for session %s: err=%v",
            slotID, sandboxName, req.SessionID, err)
        return true
    case outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED,
        outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT:
        // A completed reclaim that correctly performed nothing. The state
        // this attempt created is already gone, so the slot is not leaked,
        // whatever exited_cleanly reports.
        return false
    case outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED && !cleanly:
        log.Printf("podsession: reclaim slot %s on pod %s after failed bind for session %s did not complete",
            slotID, sandboxName, req.SessionID)
        return true
    default:
        return false
    }
}
```

The mapping is keyed on the outcome before it is keyed on `exited_cleanly`, and that order is
the deliverable's correctness condition rather than a stylistic choice. A `SUPERSEDED`
refusal is the expected answer to the interleaving this compensation exists for: the client's
context expired, the client retried, and the retry took the slot. Reading that as leaked
would withhold the pod's slot-counter decrement for the life of the pod and push the pod
toward the §5.2 whole-pod replacement threshold on the common case, immediately at
`maxConcurrentSessions: 2` where the threshold is 1. An `ABSENT` answer is the upload-free
branch and the branch where the adapter never created an entry, which was already accounted
transient before this amendment.

The reclaim's reason string is `"slot_bind_failed"`, and where the reclaim reaches a session
whose start the adapter admitted, the intra-pod `terminate` frame carries `session_complete`.
`drainReason` maps a §4.7 `ShutdownRequest.reason` onto the closed four-value `terminate` enum and
returns `session_complete` for every value outside it (`pkg/adapter/session.go:309-321`), so the
compensation mints no wire value and the enum stands as `spec/28_communication-channels.md:1082`
and `schemas/runtime-ops-events.schema.json:181` state it. The shipped §11.4 full-revoke path
already takes that same default with `"USER_REVOKED"` (`cmd/lenny-gateway/user_revocation.go:45`,
`:129`). The frame's stated obligation is that the runtime exits within `deadlineMs`, and the
adapter passes the §4.7 `ShutdownRequest.deadline_ms` straight into the frame
(`pkg/adapter/session.go:260`), so the graceful window this call pins is what the frame carries.
The frame's schema requires `deadlineMs` and fixes its minimum at 100
(`schemas/runtime-ops-events.schema.json:180-183`), and the budget's five-second floor puts half
the budget at 2500ms or above, so the value this call sends satisfies that minimum on every pool
configuration. No code under `pkg/`, `cmd/`, or `sdks/` reads the reason value, so the value
carries no precondition this reclaim would have to signal differently. The authority is the
shipped normaliser and §28.5.3's frame row; neither the enum nor its schema is opened here. A
reclaim of a bound-but-unstarted session sends no frame at all, because the drain sits inside
CODE-1's `started` block.

The split takes the adapter's graceful window out of the budget rather than widening the RPC
deadline, so the gateway's worst-case wait on this path is exactly what it was before the split.
That direction matters because on an exclusive pool reached through `Binder.Resume` the budget
already degenerates to the whole pool `cleanupTimeoutSeconds`, and doubling that inside a path
that holds the client's request would cost more than the misreported disposition it fixes.
`ShutdownRequest.deadline_ms` is a caller-pinned window, and the shipped §11.4 revoke fan-out
pins ten seconds irrespective of the pool's configuration, so pinning less than the pool's own
cleanup figure on a compensating reclaim contradicts nothing. The reclaim is tearing down a
session with no user work to quiesce.

`materializeSlot` becomes a wrapper so no stage can be added later without the compensation.
The current body moves to an unexported `materializeSlotStages` with its five `cl.Close()`
calls removed, and the wrapper owns the close:

```go
func (b *Binder) materializeSlot(ctx context.Context, req SlotBindRequest, sandboxName, slotID, podIP, workspaceBase string, cl *adapterclient.Client) (*BindResult, error) {
    res, err := b.materializeSlotStages(ctx, req, sandboxName, slotID, podIP, workspaceBase, cl)
    if err != nil {
        var sbe *SlotBindError
        if errors.As(err, &sbe) {
            sbe.Leaked = b.compensateFailedSlotBind(ctx, cl, req, sandboxName, slotID)
        }
        // spec: §7.1 step 23 — every stage that mints a §4.9 lease runs
        // inside materializeSlotStages, so the failed attempt returns its own
        // leases here rather than in the compensation, which the resume path
        // also calls and which mints nothing. The call is unconditional and
        // sits outside the errors.As guard: assignSlotCredentials mints per
        // provider and returns on the first failure, so a failed
        // credential-assignment stage can already hold minted leases, and a
        // future stage error that is not a *SlotBindError must still return
        // them. ReleaseSession is keyed by session and is a no-op for an
        // attempt that failed before assignSlotCredentials ran.
        b.releaseCredentials(req.SessionID)
        cl.Close()
        return nil, err
    }
    return res, nil
}
```

The two obligations sit at different levels because they reclaim different state. The
compensation is pod-side, so both bind paths and the resume path take it. The lease release is
gateway-side and belongs to the attempt that minted the lease, so it sits in this wrapper,
which is exactly the boundary of the stages that mint one.

Every stage inside `materializeSlotStages` is post-connection, so the compensation is
unconditional there. A stage whose failure sent no RPC (an upload-free plan failing inside
`stageWorkspace`) still sends it; the adapter answers cleanly for a session it holds nothing
for, and `Leaked` is false, so a blob-store outage adds nothing to the pod's persistent leak
count. The windowed failure counter still records it, and at `maxConcurrentSessions: 2` a
single windowed failure already reaches the drain threshold.

`ReleaseSlotReservation` takes the disposition and threads it to the parameter
`SlotClaimer.ReleaseSlot` already implements:

```go
func (b *Binder) ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string, leaked bool) error
```

Its doc comment's current justification for sending nothing, "the failed attempt already
closed its adapter connection", is replaced: the reclaim now runs at the failure site while
the connection is open, and this function releases the reservation afterwards. The
`leaked=false` comment is corrected too: it is true for the connect stage, where the slot was
reserved before any workspace RPC, and false for every stage the reclaim covers.

Call sites for the new parameter:

| Site | Value |
|:--|:--|
| `slotbinder.go` `ClaimSlot`'s connect-stage release | `false` — reserved before any workspace RPC, so the adapter holds nothing |
| `slotbinder.go` `BindReservedSlot` | `sbe.Leaked`, and the method sets `sbe.Leaked = true` on the error it is about to return when its own release fails |
| `binder.go` `releaseResumeSlot` | the disposition its caller computed, and the function returns its `ReleaseSlotReservation` error to that caller rather than only logging it |
| `start.go` `slotBinder` interface declaration | signature only |
| `start.go` `applySlotRetryPolicy` | `sbe.Leaked` |
| `start.go` `rollbackClaim` | `false` — no workspace RPC runs at create |

`Binder.Resume`'s compensation is unfenced, and that is a property of the path rather than
an omission. `Resume` is the first and only entry-touching RPC on the §7.3 re-attach, so a
failure yields a status and no response, the connection's epoch latch is still zero, and
`BindEpoch()` answers zero. The unconditional form is the correct one there: no earlier RPC
of that attempt created an entry, so no successor can be standing behind the one the reclaim
removes.

`Binder.Resume` takes the same compensating `Shutdown`. Its adapter-RPC failure branch
currently calls `cl.Close()` and then `releaseResumeSlot`; it becomes a compensation on the
still-open connection, then the close, then the release carrying the outcome. It releases no
credential lease. `Binder.Resume` mints none, its §7.3 retry re-mints none, and the session
may still hold the leases its original bind minted, so returning them on a retryable failure
would leave every later resume of that session running with leases the gateway has already
released. `releaseResumeSlot` returns
that release's error rather than only logging it, so the branch can read the release outcome
as the other two release sites do. The branch also returns its error with a `*SlotBindError`
in the chain, carrying the pod, the reserved slot id, the stage `"resume"`, and a `Leaked` set
to the same `compensation leaked || relErr != nil` discriminator `applySlotRetryPolicy` and
`BindReservedSlot` use, wrapped inside the message the branch
returns today (`fmt.Errorf("podsession: resume session on pod %s: %w", sb.Name, …)`). That is
what carries the disposition to the accounting in CODE-5; `SlotBindError.Unwrap` returns the
cause, so `isTransientPodClaimError`'s `errors.As` and `errors.Is` classification of a resume
failure is unchanged (`pkg/gateway/podlifecycle/podsession/slotfailure.go:74`;
`pkg/gateway/sessionserver/start.go:3648-3682`). On an exclusive pool `reserveResumeSlot`
returns an empty slot id, so the error carries one too and the accounting does not run. The
adapter's `Resume`
claims the session's slot before it can fail, so a lost or refused `Resume` leaves the
identical residue, and this is the site the recycle-boundary sweep alternative was reaching
for. Its spec basis is the same §7.1 obligation, which names the §7.3 re-attach onto a
replacement pod as one of the bind attempts it binds; §7.3's resume flow and §6.2's
mid-resume cancel edge point at that paragraph, so the `// spec: §7.1` citation resolves on
the resume path as it does on the creation path.

### CODE-5 · pkg/gateway/sessionserver/start.go, pkg/gateway/podlifecycle/podclaim — one accounting helper serves every bind path the reclaim obligation binds, and a retry skips the pod whose reclaim went unacknowledged

Targets: the classify/record/threshold tail of `applySlotRetryPolicy`, `bindConcurrentSlot`'s
`BindReservedSlot` branch, `resumeOnPod`'s `podBinder.Resume` failure branch, the `slotBinder`
interface, and the placement exclusion the §7.1 reclaim obligation requires
(`podsession.SlotBindRequest`, `podclaim.SlotRequest`, and `SlotClaimer.ClaimSlot`'s two
candidate passes).

Extract the tail only. The release stays with each caller, which already knows its own
disposition:

```go
// accountSlotFailure records one failed or leaked slot against the pod's §5.2
// health ledger and retires the pod when the combined windowed-failure plus
// persistent-leak count crosses the unhealthy threshold. Every bind path §7.1
// binds calls it — both concurrent bind paths and the §7.3 re-attach — so the
// create-time reserved path and the checkpoint restore reach the threshold
// §5.2 obliges them to reach, which the trigger states with no carve-out by
// code path.
//
// spec: §5.2 (whole-pod replacement trigger); §6.2 (`leaked` slot semantics).
func accountSlotFailure(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
    slots *slotstate.Registry, replacement func(pool string),
    leakGauge func(pod, pool string, leaked int),
    pool string, maxConcurrentSessions int32,
    sbe *podsession.SlotBindError, leaked bool)
```

The two request-derived parameters are the pool name and the concurrency bound rather than a
whole `podsession.SlotBindRequest`, because the body reads only `req.Pool` and
`req.MaxConcurrentSessions` off one today, and the resume caller below holds a
`podsession.ResumeRequest` rather than a bind request. Both existing callers pass `req.Pool`
and `req.MaxConcurrentSessions`.

The body is today's `MarkLeaked` plus leak gauge plus `RecordLeak` arm, today's
`RecordFailure` arm, and the unchanged `Unhealthy → DrainSandbox → replacement → Forget →
ForgetPod → zero-gauge` tail, with the discriminator taken as a parameter instead of computed
inline. `binder` is still needed, for `DrainSandbox`.

Callers:

- `applySlotRetryPolicy` keeps its own `ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID, sbe.Leaked)`
  call and passes `req.Pool`, `req.MaxConcurrentSessions` and
  `sbe.Leaked || relErr != nil`. Its retry iteration then appends `sbe.Pod` to
  `req.ExcludePods` when that discriminator was true, so the retry re-claims on a
  different pod under the §7.1 rule that a pod whose reclaim went unacknowledged carries no
  further attempt at the same session. `req` is the request value `bindConcurrentSlot` owns
  for this client request, so every pod appended stays excluded for every later bind attempt of
  that request, including one a `queue` pool re-enters, and reaches no other request.
  The loop's exit condition is unchanged and the non-retryable set stays §5.2's three
  reasons, so a clean release retries on the same pod exactly as it does today.
- `bindConcurrentSlot`'s reserved branch calls `accountSlotFailure(..., slotReq.Pool,
  slotReq.MaxConcurrentSessions, sbe, sbe.Leaked)` before `classifySlotBindFailure`, using the
  same collaborators `bindSlotWithRetry` already passes. `BindReservedSlot` keeps its own
  release.
- `resumeOnPod`'s `podBinder.Resume` failure branch (`pkg/gateway/sessionserver/start.go:4041`)
  accounts the §7.3 re-attach's disposition. It runs `errors.As(err, &sbe)` on the error
  `Binder.Resume` now returns and, when the error carries a `*podsession.SlotBindError` whose
  `SlotID` is non-empty, calls `accountSlotFailure(ctx, s.podBinder, s.slotHealth,
  s.slotStates, s.slotReplacement, s.slotLeakGauge, match.Pool,
  maxConcurrentSessions(match.MaxConcurrentSessions), sbe, sbe.Leaked)` before returning the
  error unchanged. The non-empty-slot-id guard is exactly the concurrent pool:
  `reserveResumeSlot` returns an empty slot id when `MaxConcurrentSessions <= 1`
  (`pkg/gateway/podlifecycle/podsession/binder.go`, `releaseResumeSlot`'s own no-op guard), so
  an exclusive pool reserved nothing and has nothing to account. The `Leaked` that
  `Binder.Resume` sets already folds that branch's release outcome in under CODE-4, so this
  caller reads the same discriminator as the other two: a resume whose compensation was
  acknowledged but whose reservation release failed holds the replacement pod's occupancy with
  nothing holding it, and it is accounted persistently rather than in the five-minute window.
  Without this caller the re-attach is the one bind path §7.1 binds whose `leaked` slot holds occupancy permanently
  while the pod is never counted unhealthy, never drained through the
  `ceil(maxConcurrentSessions / 2)` trigger, and never surfaced on the gauge.

`applySlotRetryPolicy`'s comment that "a clean release makes the failure transient, and a
failed release leaks the slot permanently" is widened rather than corrected: it is true today
about the reservation release, and the change extends the release-outcome test from the
reservation counter to the pod-side reclaim.

The trade this deliverable ships, stated here rather than left open: a `leaked` disposition
withholds the counter decrement for the life of the pod, and `UnhealthyThreshold` is
`(maxConcurrent+1)/2`, so at `maxConcurrentSessions: 2` one unacknowledged reclaim both burns
a slot and drains the pod. This is §6.2's semantics applied consistently, and it is the
mechanism that makes a best-effort compensation's failure bounded. On the retry path the
disposition changes behaviour only at `maxConcurrentSessions >= 3`, because at 2 the threshold
is already 1 and a single cleanly released bind failure drains the pod today. The reserved
branch is the other case, and it changes at every concurrency: it reaches no tracker today
(`pkg/gateway/sessionserver/start.go:2594-2605`), so at `maxConcurrentSessions: 2` its first
accounted bind failure of either kind drains a pod that nothing drains today. That is the
conformance gap closing rather than a threshold change, and the threshold itself is untouched.

The placement exclusion, stated whole because it is the deliverable's one new field:

- **The field.** `ExcludePods []string` on `podsession.SlotBindRequest` and on
  `podclaim.SlotRequest`, carrying Sandbox names. Its doc comment states that it is a
  read-only placement filter for §5.2's `**Max retries:**` constraint on the pods holding a
  reclaim the adapter did not acknowledge, in the vocabulary
  `MaxPodUptimeSeconds` already uses on both structs, and that an empty slice excludes
  nothing.
- **Where it is set.** One site: `applySlotRetryPolicy`'s retry iteration, which appends
  `sbe.Pod` under the same `sbe.Leaked || relErr != nil` discriminator that already chooses
  `RecordLeak` over `RecordFailure`. The append must outlive one invocation of that function, so
  `bindSlotWithRetry` and `applySlotRetryPolicy` take `req *podsession.SlotBindRequest` and
  `bindConcurrentSlot` passes `&slotReq`, which is the value its `runWithQueue` closure
  captures (`pkg/gateway/sessionserver/start.go:2606-2609`). Without the pointer an
  `onPoolExhausted: "queue"` pool loses the exclusion: `ClaimSlot` surfaces
  `podclaim.ErrNoConcurrentSlot` unwrapped when every remaining candidate is excluded,
  `runWithQueue` classifies that sentinel as exhaustion, and `waitInQueue` re-enters the same
  closure (`pkg/gateway/sessionserver/queue.go:103-107,:143-146,:205`), which would re-place
  the attempt on the pod holding the unacknowledged reclaim. Nothing clears the list, and each
  client request builds its own `SlotBindRequest`, so the exclusion reaches every later bind
  attempt of this request and no other request. The field is a list rather than one name
  because one queued request can reach more than one unacknowledged reclaim: the retry budget
  starts over on each re-entry (`maxSlotRetries` is 1 and the loop opens at attempt 0,
  `pkg/gateway/sessionserver/start.go:2720`, `:2809`), so a second leaked failure on a second
  pod would overwrite a single-valued field and free the retry to be placed back on the first
  pod, whose reclaim the adapter never acknowledged. Appending keeps every such pod excluded,
  which is what §5.2's constraint states. The copy `BindSlot` receives names the same backing
  array, and every reader of the field only reads it.
- **Where it is read.** `Binder.connectSlot`'s existing `podclaim.SlotRequest` mapping carries
  it through, and `ClaimSlot` skips every pod the list names with one `continue` in the pass-1
  scan of claimed pods and one in the pass-2 idle-pod scan, placed beside the `expiredByUptime` skip
  and documented the same way. The `slotBinder` interface is unchanged and `BindSlot` still
  takes the request by value (`binder.BindSlot(ctx, *req)`), so every implementing type and
  both test fakes compile as they stand. The pointer stops at those two internal helpers, and
  rewiring their test call sites is part of this deliverable rather than an assumption:
  `slotretry_test.go`'s `req(pool, maxConcurrentSessions)` helper keeps its by-value return
  (`pkg/gateway/sessionserver/slotretry_test.go:69`), and every `applySlotRetryPolicy` call
  site in `pkg/gateway/sessionserver/slotretry_test.go` and
  `pkg/gateway/sessionserver/slotretry_load_test.go` binds its result to a local and passes the
  address (`r := req("pool-x", 4); applySlotRetryPolicy(ctx, binder, health, …, &r)`), taken
  inside the load test's goroutine body so each goroutine still owns its own request
  (`pkg/gateway/sessionserver/slotretry_load_test.go:84-87`). `classifySlotBindFailure` keeps
  its by-value signature (`pkg/gateway/sessionserver/start.go:2761`), so the
  `classifySlotBindFailure(in, req(…))` sites in `slotretry_test.go` are untouched, as are its
  two production callers: `claimAtCreate`'s create-time reservation arm
  (`pkg/gateway/sessionserver/start.go:2172`), which passes that function's own `slotReq` local
  built at `:2140`, and `bindConcurrentSlot`'s reserved-bind arm (`:2602`), which passes its
  by-value `slotReq` parameter. `bindSlotWithRetry` has no test caller.
- **When it does not fire.** The discriminator is false when the adapter acknowledged the
  reclaim and the reservation release succeeded, and the retry then re-claims on the same pod.
  The reclaim itself is finished there: the `Shutdown` handler completes `removeSlotTree`
  before it builds the response (`pkg/adapter/session.go:238-282`), so its `os.RemoveAll` is
  not the lagging producer on this branch. What can still lag is the abandoned attempt's own
  handler, which the gateway stopped waiting for. Because `SlotID == SessionID` and the tree is
  `/workspace/slots/{sessionId}/`, the retry re-uses that identifier and that tree, and the
  abandoned attempt's own rollbacks reach both. The shipped pre-`Runtime.Start` failure
  branches release the slot by session identifier (`pkg/adapter/session.go:133`, `:147`,
  `:157`), so a lagging one
  deletes the retry's staged tree; that is a pre-existing hazard on those branches and this
  proposal does not open them. CODE-2's rollback closes the runtime for the identifier
  (`Runtime.Close(ctx, sessionID)`), which takes the retry's session off the shared runtime's
  active set; it is recorded among the accepted failure modes below. The tier-1 retry case, the
  tier-2 placement-filter cases, and the tier-4 re-bind assertion below are what observe the
  exclusion firing.
- **Reachability.** At every concurrency the retry policy runs at. At
  `maxConcurrentSessions: 2` the unhealthy threshold is already 1, so the same iteration also
  drains the pod, but `DrainSandbox` only stamps the `lenny.dev/drain-request` annotation the
  WarmPoolController acts on asynchronously, and `ClaimSlot`'s pass-1 scan of claimed pods
  reads the per-pod claim rather than the Sandbox phase. The drain therefore does not keep the
  immediate retry off that pod and the exclusion is what does.

**The reclaim-in-progress refusal stays retryable, and no production change here is what
makes it so.** The adapter answers a bind that meets the reclaim hold with `codes.Aborted`
carrying `slot_reclaim_in_progress`. `SlotBindError.Reason()` has no `Aborted` case, so it
takes the transient default (`pkg/gateway/podlifecycle/podsession/slotfailure.go`), and
`classifySlotBindFailure` passes a transient failure through with the retryable §15.1
`STARTING_FAILED` envelope intact. Nothing in the gateway is edited for this and nothing may
be: opening a case for `Aborted` in that switch would break both this refusal and CODE-2's
rollback, which relies on the same default. What makes the retryability hold is entirely
CODE-6's `slotResolveError` helper on the adapter side, because five adapter sites re-wrap
any slot-resolve failure as `codes.InvalidArgument`, which the same switch maps to
`SlotReasonWorkspaceValidation` and `NonRetryable()` reports true for.

No in-gateway wait-and-retry is staged for the hold. The hold is bounded by §5.2's per-slot
cleanup budget, which on an exclusive pool degenerates to the pool's whole
`cleanupTimeoutSeconds`, and a gateway-side wait would hold the client's request open for
that budget and state the timeout in a second place. The attempt spends a retry on the
refusal instead, which is recorded among the accepted failure modes.

`ExcludePods` is unchanged in substance and stays whole: the pointer threading through
`bindSlotWithRetry` and `applySlotRetryPolicy`, and both `ClaimSlot` candidate-pass skips.
The epoch refuses a reclaim that arrives after a successor took the slot; the exclusion keeps
a retry off a pod whose reclaim went unanswered at all, where no outcome was observed and no
epoch comparison happened. They govern different states.

### CODE-6 · pkg/adapter/bindepoch.go, pkg/gateway/runtime/adapterclient/client.go — the epoch counter, the reclaim hold, and the shared resolve helper

This deliverable lands the mechanism the other adapter deliverables insert into, so it lands
before them and compiles alone.

**New file `pkg/adapter/bindepoch.go`**, holding:

- `Server.bindEpoch int64` and `Server.reclaiming map[string]struct{}`, both guarded by
  `s.mu`, and `slotState.epoch int64`.
- `nextBindEpochLocked`, which seeds the counter lazily from `time.Now().UnixNano()` on its
  first call and increments thereafter. The seed is lazy rather than set in a constructor
  because the adapter tests build `Server` by struct literal, where a constructor-only seed
  leaves the field zero, and zero is the wire's "unfenced" value. A test that never called
  the constructor would then mint entries at epoch zero and every fenced reclaim would read
  as unconditional.
- `holdReclaimLocked(slotID)` and `releaseReclaim(slotID)`. The side table holds `struct{}`
  rather than the epoch, because nothing reads a value from it: the hold and the
  deregistration are one critical section, so no entry can stand under a held identifier and
  there is no epoch to compare against.
- `errSlotReclaimInProgress`, a `codes.Aborted` status carrying `slot_reclaim_in_progress`,
  and `isSlotReclaimInProgress(err) bool`. `Aborted` is chosen because
  `SlotBindError.Reason()` has no case for it and therefore takes the transient default, so
  the refusal is retryable without opening that switch.
- `slotResolveError(err) error` and `slotResolveCategory(err) tracing.Category`.
  `slotResolveError` returns the sentinel unchanged and wraps everything else as
  `codes.InvalidArgument` exactly as today. `slotResolveCategory` answers `CategoryTransient`
  for the sentinel and `CategoryPermanent` otherwise.

**`ensureSlotStateLocked` gains the hold refusal at the top and mints the epoch in the slot
literal.** The refusal is first, before the map lookup, because a held identifier has no
entry to return:

```go
if _, held := s.reclaiming[slotID]; held {
    return nil, errSlotReclaimInProgress
}
```

`ensureSlotPaths` widens to return the epoch beside the paths, because its callers are the
workspace RPCs that must report one.

**The five resolve sites move to the shared helper.** All five re-wrap any slot-resolve
failure as `codes.InvalidArgument` today, which `SlotBindError.Reason()` maps to
`SlotReasonWorkspaceValidation` and `NonRetryable()` reports true for. Adding the refusal
without routing these through one helper turns a transient race into a permanently dead
session, and the credential path is the load-bearing one:

| Site | Wrap | Span category stamped at |
|:--|:--|:--|
| `pkg/adapter/staging.go` `resolvePrepareStagingDir` | `InvalidArgument` | the caller stamps `CategoryPermanent` |
| `pkg/adapter/staging.go` `FinalizeWorkspace`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/staging.go` `RunSetup`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/slotsession.go` `claimSessionSlotUnderLock`'s `ensureSlotStateLocked` | `InvalidArgument` | none |
| `pkg/adapter/slotcreds.go` `assignCredentialsSlot`'s `ensureSlotStateLocked` | `InvalidArgument` | none |

No test asserts any of the five message strings, so the helper may reword none of them and
the wrap text stands as it is for every non-sentinel error.

**Seven handlers report the epoch on their responses**, per the SCHEMA-1 table.
`ConfigureWorkspace`'s idempotent-repeat arm reads the entry's epoch under `s.mu` rather than
reporting zero, because a repeat is still a response of the same attempt and the caller's
latch would otherwise see a zero it must ignore and a non-zero it must not.

**`pkg/gateway/runtime/adapterclient/client.go`** gains a per-connection `atomic.Int64`
latch, set by `CompareAndSwap` from zero on the first non-zero epoch a response reports and
never overwritten afterwards; `BindEpoch() int64` reading it; and `ShutdownReclaim(ctx,
sessionID, reason string, deadline time.Duration, epoch int64) (bool, adapterv1.SlotReclaimOutcome, error)`.
`Shutdown` and `ShutdownRecycle` keep their signatures and pass a zero epoch, so every
existing caller sends the unconditional form and no call site outside the compensation
changes.

The connection is the natural carrier for the latch because it is dialled once per bind
attempt and closed with that attempt, so the latch's lifetime is the attempt's lifetime and
no stage signature has to grow an epoch parameter. It is `atomic` rather than a plain field
because tier 7a drives a compensation on a detached context while the failing stage may
still be unwinding on the same connection.

### CONF-1 · tests/tier10_conformance/bind_epoch_conformance_test.go — the published contract has a battery

New file, following `tests/tier10_conformance/recycle_scrub_conformance_test.go` as its
precedent, which is the sibling battery for the other adapter obligation §15.4 publishes.
Four properties, one per part of the §15.4 contract:

- The epoch is minted per registry entry and strictly increases within the process.
- One entry reports one epoch on every response of the bind sequence.
- A superseded reclaim performs nothing: the successor's registry entry, its workspace tree,
  and its credential file all survive.
- A zero epoch is the unconditional teardown.

`cmd/lenny-compliance/full.go` is not a home for any of this. That battery drives a runtime
binary over JSONL against a fake adapter, so it has no adapter under test and cannot observe
an adapter obligation.

## Staged schema, chart, and migration changes

### SCHEMA-1 · schemas/lenny-adapter.proto — the bind epoch, the reclaim fence, and the reclaim outcome

The edit is additive: one enum and nine fields, no field removed, no field renumbered, no
RPC added, no message removed. `buf breaking` has nothing to fire on. `make generate-proto`
runs in the same commit and the regenerated `pkg/gen/adapter/v1` package lands with it, so no
step compiles against a half-generated tree.

**The enum**, beside the existing `SessionScrubOutcome`, which is the sibling this follows.
proto3 enum values share the enclosing package namespace, so the values are fully prefixed
with the enum's own name, exactly as `SESSION_SCRUB_OUTCOME_*` is:

```proto
// SlotReclaimOutcome is the result of the §4.7 epoch comparison on a
// Shutdown that carries expected_bind_epoch. All three values are
// successful outcomes, answered on a successful RPC.
enum SlotReclaimOutcome {
  SLOT_RECLAIM_OUTCOME_UNSPECIFIED = 0;
  // SLOT_RECLAIM_OUTCOME_RECLAIMED — the adapter held the named entry at
  // the named epoch and released the slot. spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_RECLAIMED = 1;
  // SLOT_RECLAIM_OUTCOME_ABSENT — the adapter holds no entry for the
  // named session, so the reclaim removed nothing. spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_ABSENT = 2;
  // SLOT_RECLAIM_OUTCOME_SUPERSEDED — the adapter holds an entry for the
  // session at a different bind epoch, so a later bind attempt holds the
  // slot and the reclaim removed nothing. Not a failed reclaim and not a
  // leaked slot. spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_SUPERSEDED = 3;
}
```

**The fields.** Every number below was checked free against the message it lands in.

| Message | Field | Number | Role |
|:--|:--|--:|:--|
| `ShutdownRequest` | `int64 expected_bind_epoch` | 7 | the fence |
| `ShutdownResponse` | `SlotReclaimOutcome slot_reclaim` | 3 | the outcome |
| `PrepareWorkspaceResponse` | `int64 bind_epoch` | 3 | required: the first entry-touching RPC when the plan carries uploads |
| `FinalizeWorkspaceResponse` | `int64 bind_epoch` | 2 | required: the first when the plan carries none |
| `ResumeResponse` | `int64 bind_epoch` | 4 | required: the first and only on the §7.3 re-attach |
| `ConfigureWorkspaceResponse` | `int64 bind_epoch` | 2 | required: the SDK-warm claim |
| `RunSetupResponse` | `int64 bind_epoch` | 2 | verification |
| `AssignCredentialsResponse` | `int64 bind_epoch` | 1 | verification |
| `StartSessionResponse` | `int64 bind_epoch` | 2 | verification |

`ShutdownRequest` holds 1, 2, 3, 5 and 6 with 4 reserved, so 7 is the next free number;
`AssignCredentialsResponse` is an empty message today, so its first field is 1.

The required set is a set rather than one message because `stageWorkspace` sends
`PrepareWorkspace` only when the plan carries uploads, so on an upload-free plan
`FinalizeWorkspace` is the first response the attempt receives. The three verification
fields are reported and compared by the same latch as the required ones; they exist so a
conformance battery and a tier-3 case can assert one entry reports one epoch across the whole
bind sequence rather than on its first response alone.

**Claim register.** Two rows in `tests/claim-map.json`, both `WIRED`, because a production
reader ships in the same change. The precedent points the other way and is worth stating: the
existing `coordination_generation` rows sit `UNWIRED` under `deferral_id: R16` precisely
because no production reader compares them. Here `pkg/adapter/session.go`'s `Shutdown`
compares the epoch and `pkg/gateway/podlifecycle/podsession`'s `compensateFailedSlotBind`
reads the outcome, so both rows are `WIRED` from the commit that lands them. Each row names
its production surface in file-or-symbol form rather than as a line number. `spec_anchor` is
`#2851-gateway-to-pod`, the anchor the sibling `AttachRequest.coordination_generation` row
uses.

```json
{
  "claim": "ShutdownRequest.expected_bind_epoch slot reclaim fence",
  "status": "WIRED",
  "spec_anchor": "#2851-gateway-to-pod",
  "surface": "`pkg/adapter/session.go` `Server.Shutdown` epoch comparison, `pkg/gateway/podlifecycle/podsession/slotbinder.go` `Binder.compensateFailedSlotBind`"
},
{
  "claim": "Bind epoch reported on the slot-entry RPC responses",
  "status": "WIRED",
  "spec_anchor": "#2851-gateway-to-pod",
  "surface": "`pkg/adapter/bindepoch.go` `Server.nextBindEpoch`, `pkg/gateway/runtime/adapterclient/client.go` `Client.BindEpoch`"
}
```

The three verification-set fields are covered by the second row rather than by rows of their
own, because the gateway does compare them: the client's latch takes the first non-zero
epoch and every later response of that attempt reports the same value, so a divergent
verification field is observed. A field the gateway did not compare would need a
`deferral_id` naming a declared remediation step, and no declared step owns this fence, which
is the other reason the reader ships with the field rather than after it.

No chart value and no migration.

## Staged docs changes

### DOCS-1 · docs/reference/state-machines.md — the per-slot sub-state table gains the new row

Under `### Per-slot sub-states`, add the row matching the §6.2 edge, immediately after the
`receiving_uploads` → `running` row:

```
| `receiving_uploads` | `slot_cleanup` | The bind is abandoned or fails before the runtime has been given the session, a start still in flight included |
```

No shipped tier-11 gate compares this table's edge rows against the §6.2 block: the tests in
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` read the specification
and the reference page separately and never meet. DOCS-1 therefore carries the
tier-11 work that makes the pair reconcile, specified under `## Testing`, and it lands in S6
after SPEC-4.

## Testing

Every test carries a `// spec:` annotation naming the sections it exercises. Every tier-2 and
higher test carries a `// diagnosis:` comment above its function declaration.

### Adapter tests for CODE-6, tier 1

New file `pkg/adapter/bindepoch_test.go` (package `adapter`), plus cases in the two existing
files below. Every case must fail against the tree before this amendment and pass after.
Each carries `// spec: §4.7; §5.2; §7.1`:

- **The epoch is minted per entry and strictly increases.** Two entries on one `Server`
  carry two different epochs, and the second is greater than the first. A `Server` built by
  struct literal mints a non-zero epoch on its first entry, which is the lazy-seed
  regression guard: a constructor-only seed leaves the field zero and every fenced reclaim
  then reads as unconditional.
- **The three outcomes.** A matching epoch answers `RECLAIMED` and removes the entry. A
  missing entry answers `ABSENT`. A different epoch answers `SUPERSEDED`, and that arm
  asserts the successor's entry, its `current` directory, and its `credentials.json` all
  survive the call.
- **A zero epoch is still the unconditional teardown.** This is the regression guard on the
  escape hatch every existing caller uses, and it must stay green throughout the
  implementation rather than being written last.
- **The hold refuses admission until the teardown returns**, across all three entry points
  that resolve a slot: `ensureSlotPaths`, `claimSessionSlot`, and `assignCredentialsSlot`.
  Park the reclaim inside `Runtime.Close`, assert each of the three refuses with the
  sentinel, release the park, and assert each then admits.
- **The hold is cleared on every return path, including a panic.** Drive `Runtime.Close` to
  panic, recover it at the test boundary, and assert a later bind is admitted. This is the
  arm that turns red if the release is written as a statement at each return rather than as
  a `defer`.
- **The refusal stays retryable through all five resolve sites**, table-driven over
  `resolvePrepareStagingDir`, `FinalizeWorkspace`, `RunSetup`, `claimSessionSlotUnderLock`,
  and `assignCredentialsSlot`. Each asserts the sentinel passes through as `codes.Aborted`
  rather than being re-wrapped as `codes.InvalidArgument`, and that a non-sentinel resolve
  failure still wraps as `codes.InvalidArgument` with its span category unchanged. This is
  the case that pins the `InvalidArgument` trap, and the credential site is the load-bearing
  row.
- **The start confirmation refuses a different epoch.** `noteRuntimeStarted` called with the
  epoch of a claim whose entry a reclaim removed and a successor re-created returns false.
  This is the ABA case, and it passes against a predicate reading only `st.sessionID`.
- **The bind sequence reports one epoch**, with an upload-free arm asserting
  `FinalizeWorkspace` is the first response to carry it.

### Adapter tests for CODE-1 and CODE-2, tier 1

Files: `pkg/adapter/slotsession_test.go` (package `adapter`), `pkg/adapter/socketruntime_test.go`.
Reuse the internal fixtures that already exist: `slotPod`, `slotTreeProbe`, `probeRuntime`,
`startRuntimeOps`, `recordingSessionScrubReporter`, `assignOne`, and `fakeExpiryClock`. No
`export_test.go` change and no new production seam: `AssignCredentials` is exported and
`assignCredentialsSlot` produces the bound-but-unstarted state, and the internal package
reaches `ensureSlotPaths` directly.

Cases, each `// spec: §4.7; §5.2`:

- **Registered but unbound.** `Shutdown` for an entry created by a workspace RPC and never
  bound removes the entry, and removes the slot tree and `/run/lenny/slots/{sessionId}`,
  which `slotlayout.RemoveTree` reaches. The tree removal is the new property. The
  no-`Close`, no-signal, and no-`ReportSessionScrub` assertions are regression guards on
  behaviour the old `bound` gate already gave.
- **Bound but unstarted.** The entry, the credential file, and the tree are all gone; no
  `Runtime.Close`; no `ReportSessionScrub`; **no FINAL_USAGE_REPORT**, which is new, because
  `emitFinalUsage` moves from the `bound` branch to the `started` branch; and **no `terminate`
  frame on CH-RUNTIMEOPS**, which is new for the same reason, because `drainViaLifecycle`
  moves with it and today's handler sends that §15.4.2 signal for any bound entry
  (`pkg/adapter/session.go:243,259-261`). Attach `startRuntimeOps` and leave no other bound
  entry on the pod. That second condition is load-bearing: with a bound co-tenant remaining,
  `boundRemains` withholds the frame today as well, so the assertion would not discriminate.
  Read the absence with the bounded read `pkg/adapter/slotsession_test.go:210` already uses.
  The expiry-timer assertion is kept as a guard that `deregisterSlotLocked`'s unconditional
  cancellation has not moved under the `started` branch; it duplicates
  `TestShutdownCancelsTheEndingSessionsExpiryTimers_spec_4_9` on purpose.
- **Bound and started.** The fixture drives the start through `noteRuntimeStarted`, so the
  session is in `runtimeLive`, which is what the cleanup-outcome assertion now turns on.
  Today's full teardown is unchanged: the signal when no bound entry remains, the close, the
  tree removal, and the cleanup-outcome report all still run, in that order.
- **Claimed but not yet recorded.** A reclaim of a session whose `st.started` is true and
  which `runtimeLive` does not hold runs the runtime teardown, removes the entry and the tree,
  and files no cleanup-outcome report. This is the start still in flight, it is the case that
  pins the two predicates apart, and it fails if the report is regated on `st.started`. It
  takes the same response arm as the never-admitted start, so its `exited_cleanly` answer is
  the one that also carries the slot release's outcome; the failing half of that arm is the
  assertion excluded below.
- **Co-tenancy hazard.** Drive `SocketRuntimeProcess` into `connected == true` with an empty
  active set through `Interrupt` of the last active session, then assert that `Shutdown` of a
  bound-but-unstarted entry leaves the connection, the spawned child, and the listener intact.
  Add the sibling assertion in `socketruntime_test.go` beside
  `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`, recording that `Close` in that state is
  not the no-op its doc comment claims, so the hazard is pinned at the unit that owns it.
- **Start-versus-reclaim rollback, deterministic form.** `noteRuntimeStarted` returns false for
  a session whose entry was removed, and the `StartSession` rollback closes the runtime,
  cancels a pod MCP surface no surviving session holds, leaves the registry untouched, and
  answers `Aborted`. Neither the rollback nor the reclaim
  files a `ReportSessionScrub` for that session. The concurrent form is the tier-7a case below.
- **The rollback destroys no successor.** Drive the reclaim so the entry and its tree are gone,
  then put a successor under the same slot identifier before releasing the parked start, in two
  sub-cases. The unbound sub-case creates the successor through `ensureSlotPaths` alone, which
  is the workspace-preparation state a §5.2 retry leaves; the confirmation still refuses, the
  rollback runs, and the assertion is that the successor's entry and its per-slot cwd survive
  it. The bound sub-case runs `AssignCredentials` and then `claimSessionSlot` for the successor,
  giving a bound entry and a per-slot credential file; the confirmation is satisfied by that
  entry, no rollback runs, which is the reverse ordering already recorded among the accepted
  failure modes, and the assertion is that the entry, the cwd and the credential file all
  survive. The unbound arm is the one that turns red if a registry release is added back to the
  rollback; the bound arm records the reverse ordering rather than guarding it, because the
  confirmation succeeds there and the rollback body never runs. The case
  uses this section's `slotPod`, `slotTreeProbe` and `probeRuntime` fixtures and the internal
  package's direct reach into `ensureSlotPaths`.


Scope accounting to record in the deliverable: besides
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`, no existing adapter test
drives `Shutdown` for an unbound or unstarted entry, so CODE-1 breaks no shipped test. That
test's own path is unaffected, because its ending session is started and `boundRemains` is
still false after the deregistration. It must keep passing unchanged. CODE-2 changes the
fixtures of the two shipped adapter tests named in its call-site scope above, both in
`adapterevents_test.go`. Only `TestAdapterEventsEmitsControlEvents_spec_4_7` goes red without
that change; `TestEmitFinalUsageOnShutdownPath_spec_4_7` stays green either way, because it
passes its session identifier to `emitFinalUsage` explicitly and never reads `soleSession`.
Every other `noteRuntimeStarted` caller already holds a bound entry when it records.

Excluded deliberately: an assertion that a reclaim of a slot the pod's shared runtime process
was never given, whose tree removal fails, reports `exited_cleanly: false`. The exclusion
covers the never-admitted start and the start still in flight alike, because both take that
arm of the response. `removeSlotTree` calls `slotlayout.RemoveTree` directly with
no injectable ops, and the repository's own precedent records that forcing that error from a
non-root unit test is not portable. Restoring it needs a removal seam on the pattern of the
whole-pod scrub's `Ops`, which no deliverable here stages.

### Gateway tests for CODE-4 and CODE-5, tier 1

Files: `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
`pkg/gateway/sessionserver/slotretry_test.go`, `pkg/gateway/sessionserver/start_test.go`.

Fixture work is part of the deliverable rather than an assumption: `concurrentAdapter`
discards its `Shutdown` request, injects failures only at `StartSession` and at `Shutdown`
(`startErr`, `shutdownErr`, `shutdownExitedCleanly`), and serves neither `PrepareWorkspace`
nor `AssignCredentials`. It gains per-stage error injection for the finalize, setup, and
credential-assignment stages beside today's `startErr`, handlers for `PrepareWorkspace` and
`AssignCredentials`, and the per-request recording `recordingShutdownAdapter` carries today
(`pkg/gateway/podlifecycle/podsession/binder_test.go:1156-1173`), so one fake drives the
table.

Cases, each `// spec: §7.1; §5.2; §6.2`:

- **The client latches the first reported epoch.** The `adapterclient.Client` records the
  first non-zero `bind_epoch` a response carries and ignores every later one, including a
  later response reporting a different value, which no conforming adapter sends and which
  the latch must not follow. `BindEpoch()` answers zero on a connection that received none.
- **The compensation carries the latched epoch and arrives through `ShutdownReclaim`.**
  Assert the value on the recorded request rather than only that a request was recorded.
- **The outcome mapping, six arms.** `RECLAIMED` + clean → not leaked; `RECLAIMED` + not
  clean → leaked; `SUPERSEDED` + `exited_cleanly: true` → not leaked; `SUPERSEDED` +
  `exited_cleanly: false` → not leaked; `ABSENT` → not leaked; RPC error → leaked. The
  fourth arm is the one that fails against the pre-amendment `if err != nil || !cleanly`
  form, and it is the arm to write first.
- **Per-stage compensation table.** For the finalize, setup, credential-assignment, and
  session-start stages: exactly one `Shutdown` naming the session, carrying a positive
  `deadlineMs` equal to half the budget the case's pool configuration produces and strictly less
  than the RPC deadline that same configuration produces, and
  the gateway-side credential leases released. The "received before the connection closes"
  clause is dropped: with the compensation inside `materializeSlot` ahead of `cl.Close()`, a
  recorded `Shutdown` already implies it.
- **The upload-free workspace branch, separately.** `stageWorkspace` with no uploads sends no
  `PrepareWorkspace`, so the pod holds no entry. Assert that the compensation is still sent,
  that the adapter answers cleanly for a session it holds nothing for, and that the resulting
  `SlotBindError.Leaked` is false so the failure is accounted transient.
- **The cancelled-context case.** A failure whose caller context is already cancelled or past
  its deadline still sends the compensation. This is the class-three trigger and the case a
  naive implementation gets wrong.
- **Accounting, at `maxConcurrentSessions: 4` (threshold 2).** An unacknowledged compensation
  takes `MarkLeaked`, the leak gauge, and `RecordLeak`, and calls `ReleaseSlotReservation` with
  `leaked=true`; a compensated failure takes the windowed `RecordFailure` and `leaked=false`;
  one leak does not drain at threshold 2 while two do; a windowed failure ages out of the
  five-minute window while a leak persists. Assert the discriminator directly rather than
  through the drain.
- **One `maxConcurrentSessions: 2` case**, pinning that a single failure of either kind drains,
  labelled as the shipped threshold's behaviour rather than as evidence of the new disposition.
- **The reserved bind path reaches the accounting, at `maxConcurrentSessions: 4` (threshold
  2).** The arms below, with `BindReservedSlot` still performing its own release in each.
  A post-connect failure whose compensation was acknowledged and whose
  `ReleaseSlotReservation` succeeded reaches `accountSlotFailure` with `sbe.Leaked` false and
  takes the windowed `RecordFailure`. A post-connect failure whose compensation was
  acknowledged but whose own `ReleaseSlotReservation` returns an error has `BindReservedSlot`
  set `sbe.Leaked` true, and the reserved branch takes `MarkLeaked`, the leak gauge and
  `RecordLeak` rather than `RecordFailure`. A connect-stage failure sends no compensation, and
  when its own reservation release errors it takes the same leaked arm, which is the producer
  the summary names as marking a slot leaked out of `slot_assigned`. Assert the discriminator
  directly rather than through the drain, matching the sibling accounting case above. The
  re-attach path's own case is the resume one below.
- **The retry moves pods after an unacknowledged reclaim.** At `maxConcurrentSessions: 4`,
  `applySlotRetryPolicy` still makes its second `BindSlot` call when `sbe.Leaked` is true, and
  that call carries `ExcludePods` holding the failed attempt's pod; after a clean release the
  second call carries an empty `ExcludePods`. The non-retryable reasons still return the §5.2
  `SlotFailedError` on the first attempt in both cases.
- **The exclusion survives a `queue`-pool re-entry.** Compose `runWithQueue` with
  `onPoolExhausted: "queue"` around `applySlotRetryPolicy` over the shipped `fakeSlotBinder`,
  using the internal queue fixtures `queue_internal_test.go` already builds
  (`newPodClaimQueue` with an injected poll cadence and clock). Drive the first attempt to an
  `sbe.Leaked` failure on `pod-a` and the second to `podclaim.ErrNoConcurrentSlot`, and assert
  that the `BindSlot` call the queue's re-entry makes still carries `pod-a` in `ExcludePods`.
  This is the case that fails when the request is passed by value rather than by pointer.
- **A second unacknowledged reclaim across a re-entry excludes both pods.** The same fixture,
  with the re-entry's first attempt driven to an `sbe.Leaked` failure on `pod-b`: assert that
  the `BindSlot` call its retry iteration makes carries both `pod-a` and `pod-b` in
  `ExcludePods`. This is the case that fails when the field holds one name and the second
  failure replaces the first.
- **The resume path.** A failed `Resume` sends the compensation on the still-open connection,
  releases the resume slot with the outcome, and releases no gateway-side credential lease:
  the `fakeAssigner` this package already uses records every `ReleaseSession` call in
  `released` (`pkg/gateway/podlifecycle/podsession/binder_test.go:301,:322`), so the assertion
  is that the list stays empty. At `maxConcurrentSessions: 4`, a failed
  `Resume` whose reclaim went unacknowledged reaches `MarkLeaked`, the leak gauge and
  `RecordLeak` through `resumeOnPod`'s accounting call and releases the resume slot with
  `leaked=true`; a cleanly reclaimed one takes the windowed `RecordFailure` with
  `leaked=false`; and a third case where the compensation was acknowledged but
  `ReleaseSlotReservation` returns an error takes `MarkLeaked`, the leak gauge and
  `RecordLeak` as well, pinning the release arm of the discriminator on this path.
  On an exclusive pool the resume reserved no slot, so neither arm runs.
  Assert the discriminator directly rather than through the drain, matching the sibling
  accounting case above.
- **The rollback's status code classifies transient.** Rows added to the tables
  `pkg/gateway/sessionserver/slotretry_test.go` already carries. In
  `TestSlotBindErrorReason_spec_5_2` (`:383-407`), `{"session_start", codes.Aborted,
  podsession.SlotReasonTransient}`, placed beside the existing `{"session_start",
  codes.PermissionDenied, podsession.SlotReasonPolicyRejection}` row so the two classifications
  sit side by side. In `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` (`:468-492`),
  a `session_start`-stage `codes.Aborted` passing through unclassified, which keeps the
  retryable `STARTING_FAILED` envelope on the create-time-reserved path. The same two rows
  cover the reclaim-in-progress refusal, which answers the same code from the workspace and
  credential stages, so add a workspace-stage `codes.Aborted` row beside the start-stage one
  in each table. The subject is the
  coupling between CODE-2's rollback code, CODE-6's refusal sentinel, and the shipped
  classifier: a later change to either
  side turns one of these rows red. No new fixture and no new file.

### Placement-filter tests for CODE-5, tier 2

File: `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go`, beside
`TestClaimSlotSkipsOverUptimeClaimedPod_spec_6_2` and
`TestClaimSlotSkipsOverUptimeIdlePod_spec_6_2`, which pin the sibling read-only placement
filter across the same two candidate passes and already stand up the envtest API server this
package's `ClaimSlot` cases use. Each case carries `// spec: §7.1; §5.2` and the
`// diagnosis:` comment tier 2 requires.

- `ClaimSlot` skips a named pod in the pass-1 scan of claimed pods and places the slot on
  the next same-tenant pod with free capacity.
- `ClaimSlot` skips a named pod in the pass-2 idle-pod scan and acquires a different idle
  pod.
- A two-entry `ExcludePods` skips both pods it names in the pass-1 scan, which is the state a
  queued request that reached two unacknowledged reclaims leaves.
- `ClaimSlot` returns `ErrNoConcurrentSlot` when every remaining candidate is excluded, so the
  caller maps it to `WARM_POOL_EXHAUSTED` with `details.reason:
  "concurrent_slots_exhausted"` through the sentinel path it already has.
- An empty `ExcludePods` excludes nothing, which is the shipped placement behaviour and the
  case every other `ClaimSlot` test exercises.

### Wire-contract tests for SCHEMA-1, CODE-1 and CODE-6, tier 3

New directory `tests/tier3_contract/adapter_bind_epoch/`, beside the existing
`tests/tier3_contract/adapter_generation_fence/`, which is the sibling suite for the other
per-session stamp `ShutdownRequest` carries. SCHEMA-1 opens the proto, so tier 3's mandate is
engaged. Each case carries `// spec: §4.7; §7.1; §15.4` and the `// diagnosis:` comment tier
3 requires, and each runs over the real gRPC transport rather than against a fake:

- Every response in the required set carries a non-zero `bind_epoch`: `PrepareWorkspace` on a
  plan with uploads, `FinalizeWorkspace` on a plan with none, `ConfigureWorkspace` on the
  SDK-warm claim, and `Resume` on the re-attach.
- One bind sequence reports one value across every response that carries the field,
  including the three verification-set responses.
- A `Shutdown` carrying a mismatched `expected_bind_epoch` answers
  `SLOT_RECLAIM_OUTCOME_SUPERSEDED` on a successful RPC and removes nothing: the entry, the
  tree, and the credential file all survive.
- A `Shutdown` with the field unset is the unconditional teardown and answers
  `SLOT_RECLAIM_OUTCOME_RECLAIMED` for an entry it removed.
- Caller rule 1 is checkable at this tier and is checked: the epoch the compensation sends
  equals one the same connection received.

### Edge-list test for CODE-3, tier 1

`TestValidTransitions_spec_6_2` asserts an exact edge set and fatals on a length mismatch, so
it must move to seven edges in the same step as CODE-3. Add `{ReceivingUploads, SlotCleanup}`
to `want`, add a positive `IsValid(ReceivingUploads, SlotCleanup)` assertion, and keep the
negative assertion that `IsValid(SlotAssigned, Running)` is illegal.

### Integration and race tests for CODE-1, CODE-2, CODE-4 and CODE-5, tiers 4 and 7a

**Tier 4, one case.** Extend `tests/tier4_integration/concurrent_workspace_test.go`, which
already stands up a real `adapter.Server`, a real `SocketRuntimeProcess`, and the
`echo-concurrent` runtime on a `maxConcurrentSessions: 2` pool. Alice runs; bob's bind is
abandoned at the session-start stage, the stage that produces the third residue class and the
one no lower tier reaches against a real shared runtime. After the compensation: the pod's
registry holds only alice; an unaddressed session-scoped frame on alice's Attach stream
relays again, where it is rejected before the compensation; and the shared runtime's
connection and listener survive, demonstrated by a later session carol binding and starting on
the same pod. No §15.4.2 assertion is made here: this fixture wires no CH-RUNTIMEOPS, and it
needs none. The residue's drain-suppression harm is the residue itself, which the registry
assertion above states directly, and `boundRemains`, the gate that turns registry contents into
a drain decision, is untouched by this change and is pinned on both arms by the shipped
`TestConcurrentShutdownsSendOneDrainSignal_spec_6_4`
(`tests/tier7a_load_local/shutdown_drain_gate_race_test.go:232-304`), whose two bound co-tenants
end at once and produce exactly one frame. No MCP-arming assertion:
`claimPodMCPStartLocked` refuses on `len(s.slots) != 1` and the claimant's own entry is
inserted above it, so carol beside a live alice arms nothing, with or without this change.
`// diagnosis:` states that a failure means the reclaim left the pod's registry, its
demultiplexer count, or its shared runtime in the state the residue produced; it must not
mention MCP arming.

**Tier 4, the late-reclaim arm.** Added to the same concurrent-workspace case. Bob's bind is
abandoned at the session-start stage with the caller's context already expired; bob's retry
binds and starts cleanly on the same pod; the compensation for the first attempt then
arrives, is answered `SUPERSEDED`, leaves the running session's entry, tree, credentials and
runtime membership untouched, and releases the slot with `leaked=false`. This is the ordering
the hold alone cannot close, and it is the reason the epoch exists. Assert bob's retried
session still serves after the compensation returns, rather than asserting only on the
reclaim's answer.

**Tier 4, the datastore-crossing case.** Extend `tests/tier4_integration/recycle_scrub_path_test.go`,
which already runs envtest plus miniredis plus a real adapter. A bind whose compensating
`Shutdown` the adapter refuses is released with `leaked=true`, so the Redis slot counter does
not decrement and the per-pod `SandboxClaim` survives at `bound` rather than being deleted,
which is what a `leaked=false` release of the pod's last slot does; and the leak is counted
persistently through `RecordLeak` and the `lenny_adapter_leaked_slots` gauge rather than aging
out of the windowed counter. The threshold is left to the tier-1 accounting cases, because
this fixture's pool carries `maxConcurrentSessions: 4` and one leak does not reach
`ceil(4/2)`. The pool carries a second placeable pod, so the same case also asserts that the
§5.2 retry re-binds there rather than on the pod whose reclaim was refused. This is the consequence of
CODE-4 and CODE-5 that no fake-backed tier-1 test reaches.

**Tier 7a, the start-versus-reclaim race.** An RPC that admits a start parked inside
`Runtime.Start` while the compensating `Shutdown` reclaims the slot, under `-race` with a
`lenny-test stress` budget. The park is released once the compensating `Shutdown` has
returned, so the reclaim always lands before `noteRuntimeStarted` runs. That is the only
ordering a start parked inside `Runtime.Start` admits, because `noteRuntimeStarted` runs after
`Runtime.Start` returns (`pkg/adapter/session.go:156,:163`; `pkg/adapter/resume.go:140,:144`),
and it is the ordering CODE-2's guard exists for. The park is the `gatedRuntime` form
`tests/tier7a_load_local/podmcp_arming_handoff_test.go:43-101` already provides.
The case is driven over both RPCs CODE-2 gives the rollback,
`StartSession` and `Resume`, from one rendezvous keyed on the RPC name, which is the form
`tests/tier7a_load_local/podmcp_once_per_pod_start_race_test.go:239-256` already uses to drive
that same pair from one body. The `Resume` arm needs a session identifier and a checkpoint
identifier and no chunks, because a conversation-only resume restores nothing
(`pkg/adapter/resume.go:101-104`) and the checkpoint-transport precondition fires only for a
request that carries chunks (`pkg/adapter/resume.go:42`), so the arm needs no
checkpoint-transport fixture. Under that ordering the outcomes are the tier-1
"Start-versus-reclaim rollback, deterministic form" case's outcomes reached from two
goroutines: on both arms and both variants the RPC returns `Aborted`, `runtimeLive`
does not hold the raced session, and neither the reclaim nor the rollback files a
`ReportSessionScrub` for it. The report count is fixed rather than interleaving-dependent,
because the rendezvous admits one interleaving. The at-most-one rule across every interleaving
is CODE-1's, stated above with the gate that carries it, and its one-report interleaving is an
ordinary started session's teardown, pinned by the tier-1 "Bound and started" case. What this
case adds over the deterministic one is the concurrency: the reclaim's registry write and the
start's record are checked against each other under `-race`, and the compensating `Shutdown`'s
`Runtime.Close` runs while `Runtime.Start` has not returned. Run each arm on a pod with no
co-tenant as well as a co-tenanted one, because the pod-level cohort outcome is what the two
variants hold apart: `runtimeLive` is the pod's cohort rather than a per-session flag, and
`noteRuntimeClosed` removes the named session alone
(`pkg/adapter/runtimegeneration.go:58-69`). On the no-co-tenant variant the cohort ends empty
and `runtimeIdleLocked` is true, which is the pod-level residue the guard exists to prevent,
and on the co-tenanted variant the cohort ends holding exactly the started co-tenant and
`runtimeIdleLocked` stays false. The co-tenanted variant keeps its co-tenant started because
`noteRuntimeStarted` is the only writer of `runtimeLive`
(`pkg/adapter/runtimegeneration.go:26-49`): a co-tenant registered and not yet started is
absent from the cohort, and the two variants would then assert the same thing. The case asserts
nothing about what the rollback close did inside the runtime process, because the park is the
`gatedRuntime` fake, whose `Close` holds no connection, no spawned child, and no listener. That
effect is a property of `SocketRuntimeProcess`'s active set
(`pkg/adapter/socketruntime.go:435-446`) and is pinned at tier 1 instead: the shipped
`TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`
(`pkg/adapter/socketruntime_test.go:252-300`) covers both the sibling-active early return and
the last close, and the "Co-tenancy hazard" case above adds the connected-with-empty-active-set
state.

**Tier 7a, the reverse ordering.** The same rendezvous with the park released only after the
reclaim has answered and a successor has re-created the entry under the same identifier. The
start confirmation then finds an entry at a different epoch and refuses, the RPC returns
`Aborted`, and the successor's entry, tree and runtime membership survive. This is the
ordering the pre-amendment design accepted, and the case fails against a confirmation
predicate that reads only `st.sessionID`.

**Tier 7a, the hold serialises the destructive steps.** Under `-race` and a `lenny-test
stress` budget, drive a reclaim and a concurrent bind onto the same slot identifier
repeatedly and assert that no bind is admitted while the reclaim's `Runtime.Close` or
`removeSlotTree` is running, and that every refused bind carries the transient sentinel
rather than a permanent code.

`tests/tier7a_load_local/shutdown_drain_gate_race_test.go`'s
`TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` and
`TestShutdownDrainRacesAnIncomingSession_spec_6_4` already pin the one-signal and
unbound-entry properties and must keep passing unchanged. Every session whose `Shutdown` they
drive is started through `startDrainSession`, which runs a full `StartSession`
(`tests/tier7a_load_local/shutdown_drain_gate_race_test.go:209-218`), so CODE-1's move of the
ending session's gate from `bound` to `started` cannot reach them, and `boundRemains`, the
co-tenant gate those two tests pin, is untouched. No new signal-frame case is added here: the
signal CODE-1 newly withholds, the one owed to a reclaim of a bound-but-unstarted entry, is
deterministic and single-threaded, so it is pinned at tier 1 in the "Bound but unstarted" case
above.

### Credential-fence tests for CODE-1 and CODE-6, tier 9

New file `tests/tier9_security/slot_credential_reclaim_fence_test.go`. This tier owns the
cases because CODE-1 widens the tree-removal gate from `bound` to `removed`, which brings
`/run/lenny/slots/{sessionId}/credentials.json` into scope for a registered-but-unbound
entry, and because the epoch is what decides whose credential material a reclaim reaches.
Two arms that fail in opposite directions, each carrying `// spec: §4.7; §4.9; §5.2` and the
`// diagnosis:` comment tier 9 requires:

- A superseded reclaim leaves the successor's `credentials.json`, its credential directory,
  and its armed §4.9 expiry timers intact. A regression here lets a teardown belonging to an
  abandoned attempt reach a live session's credential material.
- A matching reclaim leaves no credential material behind, for a registered-but-unbound
  entry as well as for a bound one. A regression here leaves a written credential file and
  armed timers on a pod that will serve a later session.

### Conformance battery for CONF-1, tier 10

`tests/tier10_conformance/bind_epoch_conformance_test.go`, the four properties CONF-1
states.

### Documentation reconciliation test for SPEC-4 and DOCS-1, tier 11

File: `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`. No shipped gate
compares the reader-facing per-slot table's rows against the §6.2 block:
`TestPerSlotSubStatesAreStatedForAPodOfEitherConcurrency` reads `spec/06` and `spec/07` and
matches the fixed `generalSlotEdges` list (`:32-36`), and
`TestStateMachinesDocMirrorsThePerSlotSubStateScope` reads the reference page and asserts
section placement and the per-slot state names (`:102-108`). Extend both in place rather than
adding a test function; each already carries the `// spec:` annotation and the `// diagnosis:`
comment tier 11 requires, and each gains one clause in its `// diagnosis:` naming the new edge.

- Add `"receiving_uploads ──→ slot_cleanup"` to `generalSlotEdges`. One entry gates both sides
  of §6.2's split, because the slice feeds a positive loop over the either-concurrency block
  (`:55`) and a negative loop over the concurrent-occupancy block (`:70`): the general block
  must carry the new edge and the scoped block must not, which is the placement SPEC-4 stages.
- Add the paired substring `` `receiving_uploads` | `slot_cleanup` `` to the `requireAllContain`
  list over the page's `### Per-slot sub-states` section, so the table is required to carry
  DOCS-1's row. Assert the pair rather than the trigger text, so a later reword of the trigger
  does not break the gate.

Both edits land at S6, after SPEC-4 has landed the edge and DOCS-1 the row. Between S4 and S6
the unedited file still passes, because its edge list neither requires nor forbids the new
edge.

### Coverage and preflight

Run `lenny-test coverage --diff <merge-base>` and raise the changed lines to the 80% floor,
covering the error and boundary paths rather than the happy path. Before any tier above 1, run
the environment preflight in `.claude/rules/test-coverage.md`: confirm the loaded images match
the renderer, sweep terminal agent pods, confirm the warm pools reach Ready, reap orphaned
envtest processes, and confirm the same tier fails on a clean checkout of the merge base
before treating a failure as this change's.

## Edge cases and accepted failure modes

- **A retry after an unacknowledged reclaim loses a pod rather than the attempt.** The
  attempt keeps the §5.2 retry budget it has; only the pods whose reclaim went unacknowledged
  are disqualified from carrying it, which is what closes the cross-attempt window in which a
  stale `RemoveAll` could delete a retry's freshly materialized tree or tear down a retry that
  is already running. When no unexcluded candidate remains the retry meets the shipped
  `WARM_POOL_EXHAUSTED` outcome with `details.reason: "concurrent_slots_exhausted"`, so the
  narrowed placement mints no new error code and no new client-visible category.
- **A rolled-back start can close a successor's runtime session.** The rollback leaves the
  slot registry and the on-disk tree alone, so it destroys no successor's workspace, but
  `Runtime.Close(ctx, sessionID)` is keyed on the slot identifier alone and `SlotID ==
  SessionID` makes the abandoned attempt and any later attempt at the same session the same
  identifier. Between `noteRuntimeStarted` returning false and the rollback's close, a later
  attempt can claim that identifier on the same pod, and the close then releases the
  successor from the shared runtime's active set and, when that empties the set, ends the
  shared connection, the spawned child, and the listener
  (`pkg/adapter/socketruntime.go:435-467`). The bind epoch closes the confirmation and not
  this close: the confirmation refuses on the epoch, and the rollback that follows it still
  addresses the runtime by the identifier both attempts share. The reclaim hold does not
  reach it either, because the rollback is the start handler's own unwinding rather than a
  `Shutdown`. Closing it would need a per-session identity on the shared runtime's active
  set, which no `Runtime` implementation carries, so it is recorded rather than staged.
- **A retry that meets the reclaim hold spends an attempt on it.** The refusal is
  `codes.Aborted` and classifies transient, so the attempt keeps its §5.2 retryability, but a
  retry placed onto a pod whose cleanup for that identifier is still running consumes one of
  the attempts the retry budget allows, and on a create-time-reserved slot it consumes one of
  the client's own retries. The hold is bounded by §5.2's per-slot cleanup budget, which on
  an exclusive pool degenerates to the pool's whole `cleanupTimeoutSeconds`. Accepted rather
  than closed: a gateway-side wait-and-retry would hold the client's request open for the
  same budget and would state that timeout in a second place.
- **A bind that fails inside its first entry-creating RPC reclaims unfenced.** The
  connection's epoch latch is still zero, so `BindEpoch()` answers zero and the compensation
  sends the unconditional form. The window is one RPC wide and precedes any successor,
  because the attempt has not yet released the identifier to anything else, but the reclaim
  in it is not fenced. Carrying the epoch on the failing RPC's error rather than on its
  response would close it, and that is a wider change to the gateway-adapter error surface
  than this proposal stages.
- **The compensation must never re-dial.** This proposal creates a hard dependency on the
  rule that a compensation runs on the connection the failed attempt already holds. The epoch
  is pod-local and is latched off the responses that connection returned, so a compensation
  written to re-dial when the connection is gone would send a zero epoch, which is the
  unconditional form, against a pod that may already hold a successor's slot. The rule is
  normative in §7.1 and in §4.7's caller rules, it is stated in
  `compensateFailedSlotBind`'s doc comment, and it is recorded under `**Watch out for.**` in
  the summary as a constraint on future change rather than as a property of today's code
  alone.
- **A pod bricked by a rolled-back start.** In the class-three interleaving on a pod holding no
  co-tenant, CODE-2's rollback `Runtime.Close` is the last close, and on the socket runtime the
  last close ends the shared connection, the spawned child, and the listener bound once at
  adapter start (`pkg/adapter/socketruntime.go:435-467`, the listener bound at `:156-161`). The
  close is the last close on a co-tenanted pod as well, whenever the reclaimed session is the
  only one the shared runtime holds: the active set records a session at `Start` and at no
  earlier point (`:184`, `:220`, `:373-378`), so a co-tenant that is registered but still
  mid-bind is not in it. There the co-tenant's slot keeps the pod's occupancy above zero, so
  the per-pod claim is not deleted and the bricked pod stays in inventory
  (`pkg/gateway/podlifecycle/podclaim/slotclaimer.go:845-847`). An ordinary session end reaches
  the same state through the same call in both cases, because `Binder.ReleaseSlot` sends the
  adapter `Shutdown` for the last started session while a co-tenant's bind is in flight
  (`pkg/gateway/podlifecycle/podsession/slotbinder.go:542`), so this path is not distinguished
  from a normal last close and this proposal stages no retirement trigger for it. Whether a pod
  whose runtime process has been closed is reusable is a pre-existing question of the shipped
  recycle disposition, and it is not answered through the cleanup-outcome accounting channel.
- **Faster pod churn at `maxConcurrentSessions >= 3`.** The `leaked` disposition withholds the
  counter decrement and counts persistently, so an unacknowledged reclaim moves a pod toward
  retirement sooner than a clean release would. Accepted as §6.2's semantics applied
  consistently. At `maxConcurrentSessions: 2` the disposition changes nothing on the retry path,
  because the threshold there is already 1. The reserved branch's own change at that
  concurrency comes from reaching the accounting at all rather than from the disposition, and
  is stated with CODE-5's caller list. The §7.3 re-attach is the third such path and changes at
  every concurrency for the same reason: it reaches the accounting at all for the first time,
  so at `maxConcurrentSessions: 2` its first accounted failure of either kind drains a
  replacement pod that nothing drains today.
- **A compensation sent to a pod in coordinator-hold state is refused.** The hold-state
  allowlist admits only the fence, version negotiation, the event stream, and the health probes,
  so the reclaim returns an error and the slot is correctly classified `leaked`, which on a pod
  serving concurrent sessions §6.2 then routes to the §5.2 threshold. No special case is
  written for it. The state cannot arise in a running deployment, because nothing arms the
  hold; the summary records that defect and why this proposal leaves it standing, and the case
  becomes live when remediation step R12 ships the gateway control-stream consumer.
- **The connect stage compensates nothing.** The slot is reserved before any workspace RPC, so
  the adapter holds no entry, no compensation is sent, and the compensation's own outcome never
  sets `Leaked` there. On the create-time-reserved path `BindReservedSlot`'s own reservation
  release can still fail, CODE-4 folds that failure into `Leaked`, and CODE-5's reserved branch
  accounts it persistently, which marks a slot leaked out of `slot_assigned`. Accepted as
  §6.2's `leaked` semantics applied consistently. §6.2 still has no terminal out of
  `slot_assigned`; the summary records that hole, the widening, and why neither is closed
  here.
- **`slotCount` still counts a registered-but-unbound entry.** The §28.5.3 count fails closed on
  purpose and its comment says so. With the residue removed the count is fed no residue, so the
  predicate is left exactly as it is.

## Files touched on application (non-spec)

- `schemas/lenny-adapter.proto` — the `SlotReclaimOutcome` enum and the nine fields SCHEMA-1
  states.
- `pkg/gen/adapter/v1` — regenerated by `make generate-proto` in the same commit as the proto
  edit.
- `tests/claim-map.json` — the two `WIRED` claim-register rows SCHEMA-1 states.
- `pkg/adapter/bindepoch.go` — new: the epoch counter and its lazy seed, `slotState.epoch`,
  the reclaim-hold side table and its two accessors, the refusal sentinel and its predicate,
  and the two shared resolve helpers.
- `pkg/adapter/slot.go` — `ensureSlotStateLocked`'s hold refusal and epoch mint, and
  `ensureSlotPaths`'s widened return.
- `pkg/adapter/staging.go` — the three resolve sites moved to the shared helper, and the
  epoch on the `FinalizeWorkspace` and `RunSetup` responses.
- `pkg/adapter/slotcreds.go` — the resolve site moved to the shared helper, and the epoch on
  the `AssignCredentials` response.
- `pkg/gateway/runtime/adapterclient/client.go` — the per-connection epoch latch,
  `BindEpoch`, and `ShutdownReclaim`.
- `pkg/adapter/session.go` — `Shutdown`'s clause two, its epoch comparison, its reclaim hold,
  its response, its doc comment, and the `StartSession` rollback.
- `pkg/adapter/runtimegeneration.go` — `noteRuntimeStarted`'s signature, body, and doc
  comment, and the new `runtimeHoldsLocked` accessor.
- `pkg/adapter/slotsession.go` — `deregisterSlotLocked`'s doc comment, the resolve site in
  `claimSessionSlotUnderLock` moved to the shared helper, and the epoch the two claim
  functions report.
- `pkg/adapter/resume.go` — the `noteRuntimeStarted` call site, the epoch it passes, its
  rollback, and the epoch on the `Resume` response.
- `pkg/adapter/sdkwarm.go` — the `noteRuntimeStarted` call site and the epoch on the
  `ConfigureWorkspace` response, including its idempotent-repeat arm.
- `pkg/sandbox/slotstate/slotstate.go` — `ValidTransitions()` and its doc comment.
- `pkg/gateway/podlifecycle/podsession/slotfailure.go` — `SlotBindError.Leaked`.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` — `slotCleanupBudget`,
  `compensateFailedSlotBind`, `materializeSlot` and `materializeSlotStages`,
  `ReleaseSlotReservation`, `BindReservedSlot`, `ClaimSlot`'s connect-stage release,
  `SlotBindRequest.ExcludePods` and its pass-through in `connectSlot`'s `podclaim.SlotRequest`
  mapping.
- `pkg/gateway/podlifecycle/podsession/binder.go` — `Binder.Resume`'s failure branch and
  `releaseResumeSlot`.
- `pkg/gateway/sessionserver/start.go` — the `slotBinder` interface, `accountSlotFailure`,
  `applySlotRetryPolicy` (including the retry iteration's `ExcludePods` append), `bindSlotWithRetry`,
  `bindConcurrentSlot`, `resumeOnPod`, `rollbackClaim`.
- `pkg/gateway/podlifecycle/podclaim/slotclaimer.go` — `SlotRequest.ExcludePods` and the skip
  in `ClaimSlot`'s two candidate passes.
- `docs/reference/state-machines.md` — the per-slot sub-state table.
- Tests: `pkg/adapter/slotsession_test.go`, `pkg/adapter/socketruntime_test.go`,
  `pkg/adapter/export_test.go`, `pkg/adapter/usage_test.go`,
  `pkg/adapter/adapterevents_test.go`, `pkg/adapter/podmcp_arming_internal_test.go`,
  `pkg/sandbox/slotstate/slotstate_test.go`,
  `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
  `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go`,
  `pkg/gateway/sessionserver/slotretry_test.go`,
  `pkg/gateway/sessionserver/slotretry_load_test.go`,
  `pkg/gateway/sessionserver/start_test.go`,
  `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`,
  `tests/tier4_integration/concurrent_workspace_test.go`,
  `tests/tier4_integration/recycle_scrub_path_test.go`, `tests/tier7a_load_local/`,
  `pkg/adapter/bindepoch_test.go`, `tests/tier3_contract/adapter_bind_epoch/`,
  `tests/tier9_security/slot_credential_reclaim_fence_test.go`,
  `tests/tier10_conformance/bind_epoch_conformance_test.go`.
