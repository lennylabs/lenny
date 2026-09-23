# Non-spec changes: A failed session bind leaves a stale adapter slot registry entry

These changes are staged. Applying them is the implementation's work, under the
implementation checklist. Every code deliverable here lands after the spec deliverables it
implements.

The deliverables are listed below in reading order. Build order differs from that reading
order and is stated once, as the step sequence in the implementation checklist.

## Design (implementation-facing)

The mechanism is one predicate at one chokepoint on the adapter, one two-field precondition on
`Shutdown`, and a per-attempt token the gateway mints and carries.

**The token.** A bind attempt mints an opaque string from `crypto/rand` before it issues its
first RPC, carries that string on the requests §4.7.1's carriage table marks it carried on, and
names it again on the compensating `Shutdown` it sends when the attempt fails. How the adapter
treats the value is §4.7.1's, under **Bind attempt token** and
**the stamp-once rule**. That rule is what makes the mechanism monotone without a counter: a
compensation can only match an entry its own attempt created.

**The chokepoint.** `ensureSlotStateLocked` (`pkg/adapter/slot.go`) is the adapter's only
resolve-or-create step and has three production callers, `ensureSlotPaths`,
`assignCredentialsSlot` and `claimSessionSlotUnderLock`. Those three cover the seven requests
§4.7.1's admission rules govern: `PrepareWorkspace`, `FinalizeWorkspace` and `RunSetup` through the
first, `AssignCredentials` through the second, and `StartSession`, `Resume` and
`ConfigureWorkspace` through the third. One predicate at that function covers all seven.

`validateBindFields` takes rule 1 (**the pairing rule**) at the handler's entry, before the
resolve and ahead of the cascade; the rules below it are reached only by a request it admits. The
cascade itself is evaluated under `s.mu`, before anything is written, as part of the same
indivisible resolve-or-create-and-stamp step. `ensureSlotStateLocked`'s switch arms carry rule 3
(**the mid-session-create rule**), rule 4 (**the create-and-stamp rule**), rule 5 (**the attempt
identity rule**), rule 6 (**the started-session rule**) and rule 7 (**the admit rule**) in that
order, and that function is the whole of them; rule 2 (**the reclaim hold**) is tested ahead of
the map lookup, because a held identifier has no entry to return. §4.7.1 states each rule's
condition and its answer, and this file cites them by number and name rather than restating
them.

Rule 5 and rule 6 are the identity gate and the phase gate, which are the short names the
sections below use for them. They share one critical section and one call graph, which is why
they land together rather than in sequence.

**The atomicity requirement.** §4.7.1's registry critical-section paragraph states it, and CONF-1
tests it.

**The mid-session conditioning.** A mid-session request asserts no identity under rule 1 (the
pairing rule), cannot create under the mid-session-create rule, and is exempt from the phase gate,
for the reasons the summary's cascade decision gives. What keeps that safe is the shipped admission
guard: a mid-session upload is admitted only for a session with a live binding in the replica's
`podRegistry` (`pkg/gateway/sessionserver/upload_to_session.go`). Confirm that guard reads what it
appears to read before CODE-6's mid-session-create rule lands. CODE-6 reads `mid_session` before
the resolve in `FinalizeWorkspace` and `PrepareWorkspace`.

**`Shutdown` says which teardown it is asking for.** `bind_attempt` and
`unconditional_teardown` sit beside the address on `ShutdownRequest`, paired by rule 10 (**the
teardown-pairing rule**), for the reason the summary's `unconditional_teardown` decision gives.

**The reclaim hold.** The token makes a late reclaim refusable. It does not make an admitted
reclaim exclusive while it tears state down. `removeSlotTree` deletes paths derived from the
slot identifier, `SlotID == SessionID` makes that identifier equal across attempts, and the
destructive steps read no registry state, so the deregistration alone does not keep a successor
out of them. The hold opens as §4.7.1's registry critical-section paragraph states, ends as §5.2's
reclaim-hold paragraph and disposition table state, and refuses a bind onto a held identifier as
rule 2 (**the reclaim hold**) states. It is retained
from the epoch design, with its release arm keyed on the cleanup's completion, because it closes
an ordering the token does not.

**What the change costs.** One additive proto window, SCHEMA-1, under the summary's proto-window
decision. On the gateway side: one mint helper, one in-process error field, one existing parameter
threaded to two more call sites, two error sentinels, and one predicate split. No new frame, no
new flag, and the counters CODE-9 states.

## Staged code changes

### CODE-1 · pkg/adapter/session.go · `Shutdown` requires exactly one of the two teardown fields and compares the bind attempt before the deregistration

W7's split of the `if bound` gate is CODE-15, which edits the critical section this deliverable
opens.

Targets:

- `Shutdown`'s clause two: the two-field precondition, the bind-attempt comparison under `s.mu`,
  and the slot-guard acquisition on the removing arm alone.
- `Shutdown`'s clause three, the whole-pod recycle scrub `if rc := req.GetRecycle(); rc != nil {
  s.startPodScrub(rc) }` (`pkg/adapter/session.go:283-291`). It stops being a trailing statement
  and moves inside the handler's single exit helper, where it is written once and runs on every
  outcome. The shipped copy at the tail of the handler is deleted in the same edit: leaving it
  in place while the helper also calls it starts the whole-pod scrub twice on the removing
  path.
- `pkg/adapter/session.go` · `shutdownReclaimOutcome`, the package-level function that decides the
  comparison, beside `Shutdown`.
- `pkg/adapter/bindattempt.go` · the refusal sentinels, the reclaim hold and `reclaimSlotLocked`
  are CODE-6's, and the two per-slot guard hand-out helpers are CODE-14's; both land first. This
  deliverable inserts into them and defines none of them. It takes the raw `lockSlotGuard`,
  because a `Shutdown` is never refused by a hold.
- `pkg/adapter/metrics.go` is not edited here. The untokened-entry counter and its
  `incSlotShutdownUntokenedEntry` accessor are CODE-9's, which lands first (S10 precedes S16).
  This deliverable calls the accessor and declares nothing.

**The two-field precondition, checked before the lock is taken.** `Shutdown` performs only the
empty-session-id check before `s.mu.Lock()` today, and the precondition joins it there:

```go
// spec: §4.7.1 (role and gateway RPC contract), rule 10 (the
// teardown-pairing rule). Decided on the request's fields alone, so it sits
// above s.mu and a malformed request never reaches the registry.
attempt := req.GetBindAttempt()
unconditional := req.GetUnconditionalTeardown()
if (attempt == "") == !unconditional {
    return nil, status.Errorf(codes.InvalidArgument,
        "shutdown for session %s must carry exactly one of bind_attempt and unconditional_teardown",
        sessionID)
}
```

**The comparison, inside the critical section the handler already opens.**
`deregisterSlotLocked` deletes the entry unconditionally when it finds one
(`pkg/adapter/slotsession.go:180`), so the comparison precedes the deletion in the same
critical section. The comparison is the §4.7.1 `Shutdown` cascade and states none of it again.
Its switch arms carry the rules in the order the cascade fixes: the `!ok` arm is rule 11 (**the
no-entry rule**), the `unconditional` arm is rule 12 (**the unconditional-teardown rule**), the
two `superseded` arms are rule 13 (**the attempt-mismatch rule**) in its entry-carries-no-token
form and its differing-token form, and the default arm is rule 14 (**the attempt-match rule**).
Rule 10 (**the teardown-pairing rule**) is the two-field precondition above, checked before the
lock is taken. Rule 15 (**the reclaim-outcome rule**) is what the response reports and is stated
with the response below.

Rule 13's entry-carries-no-token arm is reachable in this tree. With `materializeSlot`,
`Binder.Prepare` and `Binder.Resume` all minting, and with CODE-6's mid-session-create rule
removing the mid-session creation route, the remaining producer of an unstamped entry is a
`StartSession` or a `ConfigureWorkspace` that resolved none and created one, because neither
carries a token. The untokened-entry counter below names that arm, so such an entry, and a
future caller that creates one without a token, are visible rather than silent.

The comparison is one function, evaluated wherever the handler needs it, so the fast path and the
re-decision under the guard cannot drift apart:

```go
// shutdownReclaimOutcome decides what a Shutdown does with the entry it
// resolved, as a pure function of the request and the entry. It is the whole
// of §4.7.1's teardown comparison, and the handler evaluates it twice on the
// removing path: once under s.mu as the fast path that answers the outcomes
// removing nothing, and once under s.mu after the slot guard is held, which is
// the evaluation the deregistration is atomic with. Two calls of one function
// cannot diverge, where the same comparison written out twice can.
//
// untokened reports the fail-closed arm, an entry carrying no attempt token at
// all, so the caller counts it on the arm that answers and a call counts it at
// most once.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 11 through 14
func shutdownReclaimOutcome(cur *slotState, ok, unconditional bool, attempt string) (adapterv1.SlotReclaimOutcome, bool, bool) {
    switch {
    case !ok:
        return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT, false, false
    case unconditional:
        return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false
    case cur.bindAttempt == "":
        return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, true
    case cur.bindAttempt != attempt:
        return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED, false, false
    default:
        return adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED, true, false
    }
}
```

The handler body:

```go
// spec: §4.7.1 (role and gateway RPC contract), rules 11 through 15. The
// comparison that authorizes the deregistration is inside the same critical
// section as the deregistration, because deregisterSlotLocked deletes
// unconditionally once it finds an entry.
//
// The comparison runs first under s.mu alone, with no slot guard taken. An arm
// that removes nothing touches no path, and CODE-14's guard brackets the
// sections that do path work, which run as long as a materialization, a setup
// command or a checkpoint restore. A reclaim answering ABSENT or SUPERSEDED
// from behind one of those would spend its whole cleanup budget waiting to
// report that it removed nothing, and the gateway would record an RPC error
// instead, which sets the bind's leaked disposition and holds the pod's
// slot-counter occupancy for the life of the pod.
//
// The removing arm releases s.mu, takes the guard through the raw hand-out
// form, which no reclaim hold refuses and which reports a failure to acquire
// inside the request's own context rather than blocking past it; the arm
// proceeds either way. CODE-14's disposition of an expired acquisition at
// a removing site states what an acquisition that did not complete does
// to the removal: the removal runs unguarded after a
// slot_guard_not_acquired warning. The acquisition counts as a failed
// act, so the §5.2 disposition table's failed-act rows give its hold and
// its response, and the guarded term in the predicates below carries the
// acquisition result. It then re-takes s.mu and decides again. The
// re-decision is required rather than defensive: s.mu is not held while the
// guard is being acquired, so the entry can be removed or replaced in that
// interval, and a handler that deregistered on the first decision would
// deregister on a comparison the registry no longer supports. The first
// decision is a fast path and the second is the one the deregistration is
// atomic with.
//
// The guard's unlock is deferred ahead of the reclaim hold's release below, so
// it runs last and the guard outlives the hold. A FinalizeWorkspace, a RunSetup
// or a Resume admitted before this reclaim opened its hold is excluded by the
// guard rather than by the hold, which reaches no section that is already past
// its resolve.
// answerShutdown is the handler's only exit after the two-field
// precondition, so every outcome is built in one place. It counts the
// fail-closed arm, runs clause three, and builds the response.
//
// The whole-pod recycle scrub is outside both teardowns and outside the arms
// that remove nothing, and it runs on every outcome this helper builds. Two
// gateway callers carry the recycle disposition, and they reach different
// arms. Binder.ReleaseSlot sends it
// after a separate unconditional Shutdown has already torn the last slot
// down (pkg/gateway/podlifecycle/podsession/slotbinder.go:542, then :574),
// so that request answers ABSENT. Binder.Release's recycle branch sends it
// as the session's only teardown (pkg/gateway/podlifecycle/podsession/
// binder.go:1994, :2037), so the entry is still present and that request
// answers RECLAIMED on the removing arm. Clause three must therefore run on
// every outcome answerShutdown builds, rather than on any one arm. A request
// the two-field precondition refuses returns above this helper and performs
// nothing, the scrub included. An arm that returned before the scrub
// would drop the §5.2 scrub on a path that reaches it,
// and the pod would be handed to the next tenant unscrubbed with no
// ReportPodScrub for the gateway's armed missing-report timeout to receive.
//
// Placing the scrub in this helper also fixes its order against the per-slot
// cleanup. The helper is the handler's only exit, so on the removing arm
// Runtime.Close and removeSlotTreeVia have both returned before the scrub
// goroutine starts, which is the order §5.2 states for the whole-pod
// boundary: cleanupCommands and the Lenny whole-pod scrub run after every
// ended session's per-slot tree and credential lease have been removed. The
// one act still outstanding at that point is the reclaim hold's deferred
// release, and the hold refuses binds onto the slot identifier while it is
// held, so the overlap can only withhold the identifier from a successor
// rather than admit one onto a slot the scrub is about to touch.
//
// spec: §5.2 recycle lifecycle; §4.7 Shutdown recycle disposition.
answerShutdown := func(outcome adapterv1.SlotReclaimOutcome, exitedCleanly, untokened bool) (*adapterv1.ShutdownResponse, error) {
    if untokened {
        // The fail-closed arm. Nothing a compensated path produces reaches
        // it; the counter exists so that stops being true loudly.
        incSlotShutdownUntokenedEntry()
    }
    if rc := req.GetRecycle(); rc != nil {
        s.startPodScrub(rc)
    }
    return &adapterv1.ShutdownResponse{
        ExitedCleanly: exitedCleanly,
        SlotReclaim:   outcome,
    }, nil
}

s.mu.Lock()
cur, ok := s.slotStateLocked(sessionID)
outcome, remove, untokened := shutdownReclaimOutcome(cur, ok, unconditional, attempt)
if !remove {
    s.mu.Unlock()
    return answerShutdown(outcome, true, untokened)
}
s.mu.Unlock()

unlockSlot, guarded := s.lockSlotGuard(ctx, sessionID)
if !guarded {
    slog.Warn("slot_guard_not_acquired", "slot_id", sessionID, "caller", "Shutdown")
}
defer unlockSlot()

s.mu.Lock()
cur, ok = s.slotStateLocked(sessionID)
outcome, remove, untokened = shutdownReclaimOutcome(cur, ok, unconditional, attempt)
if !remove {
    s.mu.Unlock()
    return answerShutdown(outcome, true, untokened)
}
st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)
```

### CODE-15 · pkg/adapter/session.go, pkg/adapter/runtimegeneration.go, pkg/adapter/server.go, pkg/adapter/slotsession.go · `Shutdown` releases the slot and its tree for any entry removed and tears the runtime down only for a started session

Targets:

- `Shutdown`'s `bound := removed && st.sessionID != ""` gate, the `if bound { … }` block, and the
  response construction on the removing arm, inside the critical section CODE-1 opens.
- `Shutdown`'s doc comment.
- `pkg/adapter/server.go` · the `SessionScrubReporter` field comment, re-keyed from "on every
  session release" onto the cleanups §5.2 states the adapter reports.
- `pkg/adapter/slotsession.go` · `deregisterSlotLocked`'s doc comment only. No body change: it
  already cancels every armed expiry timer on removal.
- `pkg/adapter/runtimegeneration.go` · a `runtimeHoldsLocked` accessor beside
  `runtimeIdleLocked`, reporting whether `runtimeLive` holds the named session. Callers hold
  `s.mu`. It reads state that already exists and adds none: `noteRuntimeStartedLocked` is the
  only writer that sets membership and `noteRuntimeClosed` the only one that clears it.

**The split gates.** The remainder of the critical section captures three predicates and they
never diverge afterwards:

```go
// spec: §4.7.1 (role and gateway RPC contract), rules 12 and 14, which
// perform both teardowns under their own preconditions. st.started is the
// runtime teardown's: it is set inside claimSessionSlotUnderLock's critical
// section, which every start RPC enters, before Runtime.Start, so gating on it
// fails closed on a start still in flight, which is torn down rather than
// skipped.
started := removed && st.started
// spec: §5.2 (pool configuration and execution modes). The cleanup-outcome
// report is owed only by a slot that reached §6.2's running, which is
// runtimeLive membership, because noteRuntimeStarted records it after
// Runtime.Start returns. Gating the report on st.started would report for a
// session this very reclaim is about to take back off the shared runtime
// process, advancing the pod's served-session count for a session it never
// ran.
live := removed && s.runtimeHoldsLocked(sessionID)
// spec: §5.2 (pool configuration and execution modes). The deregistration
// and the hold are one critical section, so no bind is admitted between them.
// The release is deferred rather than written at each return so that a panic
// out of Runtime.Close or removeSlotTreeVia is treated as a cleanup that did
// not complete rather than as a silent release: Runtime.Close reaches three
// implementations and a child process and removeSlotTree reaches the
// filesystem. The deferred closure takes the release only on the arm where the
// cleanup completed, because §5.2 ends the hold when the cleanup completes and
// keeps the identifier held for the life of the pod when it does not.
// reclaimSlotLocked returns a non-nil release on every path, so a call that
// removed no entry takes a no-op.
completed := false
defer func() {
    if completed {
        release()
    }
}()
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

// spec: §4.7.1 (role and gateway RPC contract), rules 12 and 14. The slot
// release runs for any entry the call removed, bound or not. It follows the
// drain and the close so
// the agent process is not reading a credential file the teardown has already
// removed inside the §15.4.2 grace window. Widening the gate from bound to
// removed newly reclaims the slot tree ensureSlotStateLocked created, the
// workspace and the empty credential directory, for a registered-but-unbound
// entry. A bound entry's credentials.json was already reclaimed inside the
// shipped bound gate, and the armed §4.9 expiry timers are cancelled outside
// it, by deregisterSlotLocked, for every entry it removes.
treeErr := error(nil)
if removed {
    treeErr = s.removeSlotTreeVia(st)
    s.cancelPodMCPIfRuntimeIdle()
    if treeErr != nil {
        slog.Warn("slot_tree_removal_failed", "slot_id", sessionID, "error", treeErr)
    }
}
// spec: §5.2 (pool configuration and execution modes). The cleanup completed
// when every act it owed the slot returned without error, which is what ends
// the reclaim hold. An acquisition that expired counts as a failed act, per
// CODE-14's disposition of an expired acquisition at a removing site. The
// cleanup-outcome report below is keyed separately, because it is §6.2
// occupancy accounting rather than a statement about the slot identifier.
completed = guarded && closeErr == nil && treeErr == nil

if live {
    s.reportSessionScrub(ctx, sessionID, closeErr)
}
```

The asymmetry is the point. The on-disk tree belongs to the slot identifier, which the
workspace-preparation RPCs create without binding; the runtime and the pod-global drain belong
to a session that actually started. `started` over-approximates toward closing and `live`
under-approximates toward not counting, so every interleaving of a reclaim with a start yields
zero or one cleanup-outcome report for the session: a reclaim before the claim removes nothing
and reports nothing; a reclaim while the start is in flight closes the runtime and reports
nothing, and CODE-2's `noteRuntimeStarted` then refuses the record; a reclaim after
`noteRuntimeStarted` recorded reports exactly once, for a session the runtime genuinely held.
`RecordSessionScrub` increments the pod's served-session count before it branches on the leak
flag and holds no per-session dedup
(`pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:451-481`), so the
one-report rule is an adapter-side invariant rather than something the gateway can absorb.

The report stays keyed on `closeErr`, which is the key the shipped handler already uses
(`pkg/adapter/session.go:279`). The report's outcome value and the response's `exited_cleanly`
are the two inputs to one §6.2 quantity rather than two independent statements. On the `running`
arm the gateway reads the clean exit, computes `leaked = err != nil || !cleanly` as false
(`pkg/gateway/podlifecycle/podsession/slotbinder.go:543`) and frees the slot's Redis
slot-counter occupancy through `SlotClaimer.ReleaseSlot`, so a report of `leaked` filed against
that same answer books a leak against occupancy that is already free: the slot never reaches
`lenny_adapter_leaked_slots` and never counts toward `ceil(maxConcurrentSessions/2)`, while the
gateway's leak count is persistent and never pruned
(`pkg/gateway/runtime/slothealth/slothealth.go:121-125`, stated at `:127-134`) and stamps
`lenny.dev/drain-request` as soon as it reaches the unhealthy threshold, which is 1 for
`maxConcurrentSessions` 1 and 2 (`pkg/gateway/session/recycle/scrubreporter_seams.go:184-197`).
The `live` arm is the ordinary end-of-session teardown on every pod rather than this proposal's
reclaim, so keying the report on both errors would retire a pod and issue its drain-request
merge patch on the first `os.RemoveAll` failure at an ordinary session end, a class of event the
shipped design absorbs inside the occupancy-zero whole-pod scrub and its budgeted
`onScrubFailure` and `maxScrubFailures` policy (§5.2).

The reclaim hold and the cleanup-outcome report answer different questions and therefore differ
on this arm. `completed` is keyed on both errors and on the guard acquisition because the hold is
a property of the slot identifier and makes no occupancy claim, while the report is §6.2 occupancy
accounting; what the `guarded` term carries is CODE-14's disposition of an expired acquisition at
a removing site, which this handler cites rather than restates. The
failed tree removal is accounted where it distorts neither: on the pre-`running` arm it is
carried on the response's `exited_cleanly`, which the unchanged disjunct below already does; on
the `running` arm it is recorded by the `slog.Warn` above, whose event name and fields are the
ones CODE-6 fixes for this same failure so that every site reads as one convention, and its
residue is reclaimed at the occupancy-zero whole-pod scrub. The warn
uses the `log/slog` import this handler's `slot_guard_not_acquired` warning already adds to
`pkg/adapter/session.go`. The `exited_cleanly` predicate below stays keyed on the runtime close
and, for a slot that did not reach `running`, on the slot release, because it answers the
reclaiming request's own question rather than the cleanup-outcome question, and the reason it
reads `live` is stated with it; the slot-release half carries the `guarded` term for the reason
the `completed` predicate does.

The response becomes:

```go
return answerShutdown(outcome, closeErr == nil && (live || (guarded && treeErr == nil)), false)
```

`outcome` is the value the second decision returned, which is `RECLAIMED` on every path that
reaches here, because the handler answered and returned on every arm that removes nothing.
Deriving it a second time from `removed` would put the outcome rule in two places, and
`reclaimSlotLocked` removes the entry the decision it was taken under resolved.

`slot_reclaim` is populated on the unconditional form as well as the fenced one, so the gateway
reads one field on every answer and never has to infer an outcome from the request it sent.

The same helper answers the two refusal arms, so `slot_reclaim` and the whole-pod recycle scrub
are each stated once and cannot diverge between the arms. The two returns that do not go
through it are the empty-session-identifier rejection and the two-field precondition's
`INVALID_ARGUMENT`, each of which performs nothing, the scrub included, because a malformed
request performs nothing.

`treeErr` is the result of `s.removeSlotTreeVia(st)`, the tree-removal seam CODE-6 states, rather
than of `removeSlotTree(st)` directly. If the reclaim path is left calling
`removeSlotTree` directly the seam is unreachable, and the tier-1 case that asserts
`exited_cleanly` false observes true and fails at its assertion, so the miss is loud.

`exited_cleanly` carries one rule, keyed on the same §4.7.1 `running` boundary the rest of this
change installs: the response reports a clean exit when the runtime close succeeded and, for a
slot that did not reach `running`, when the slot release also completed, an expired guard
acquisition counting as a failed act under CODE-14's disposition of an expired acquisition at a
removing site.
The gate is `live` rather than `st.started` because `st.started` is set inside
`claimSessionSlotUnderLock` before `Runtime.Start` runs, so a start still in flight is
`started` and pre-running, and gating on `started` would discard the tree-removal error for
exactly the reclaim §7.1 sends on a cancelled context. Reading `live` leaves the session-end
classification untouched: every path that admits a start reaches `noteRuntimeStarted`
unconditionally on success (`pkg/adapter/session.go:163`, `resume.go:144`, `sdkwarm.go:261` on
the freshness arm), and the two `noteRuntimeClosed` callers outside `Shutdown`
(`pkg/adapter/holdstate.go:251`, `sdkwarm.go:297`) each remove the registry entry in the same
pass, so a session whose slot reached `running` and that reaches an ordinary session end is in
`runtimeLive` when its `Shutdown` arrives and answers on `closeErr` alone as it does today.

Doc-comment work on `Shutdown`:

- Cite rule 10 (**the teardown-pairing rule**) first, because it is the handler's new outermost
  branch, and say that it is decided on the request's fields alone, which is why it sits above
  `s.mu`.
- Cite rules 11 through 15 second, and map each switch arm of `shutdownReclaimOutcome` to its
  rule, in the commentary's own order. Name the outcome each arm returns so a reader of the
  handler does not have to open the proto to learn what `SUPERSEDED` and `ABSENT` mean to the
  gateway. Restate none of the rules' conditions.
- State where the slot guard is taken and why it is taken there: the comparison runs under
  `s.mu` alone, the arms that remove nothing answer without a guard because they touch no path,
  and the removing arm takes the guard before the deregistration and re-decides under `s.mu`
  afterwards, because the registry can change while the guard is being acquired. Say that the
  guard covers the runtime close and the tree removal, which is the exclusion the reclaim hold
  cannot provide against a section admitted before the hold opened.
- State the hold and why it is deferred, in the terms the code comment above carries: the
  destructive steps resolve from the slot identifier rather than from the entry, so the
  deregistration alone does not keep a successor out of them, and the release is deferred so
  that a panic out of `Runtime.Close` or `removeSlotTree` reads as a cleanup that did not
  complete rather than as a release written at a return the panic skipped. State the arm the
  release is taken on: §5.2 ends the hold when the cleanup completes, so the deferred closure
  releases only when the guard was acquired and the runtime close and the tree removal both
  returned without error, and an identifier a failed cleanup did not release stays held for the
  life of the pod. Cite CODE-14's disposition of an expired acquisition at a removing site for
  the guard term rather than restating it.
- State the handler as two teardowns with two preconditions, matching the §4.7 row, and name
  `releaseSessionSlot` (`pkg/adapter/slotsession.go:214-220`) as the shipped statement of the
  unstarted branch's semantics, so the two compensating paths read as one rule.
- State the cleanup-outcome report's own gate beside them, because it is neither teardown's:
  §5.2 gives a session release at most one report and gives it to the cleanup that reclaimed
  the slot, and only a slot that reached running is owed one, so the report reads `runtimeLive`
  membership while the teardown reads `st.started`. State the response's own predicate in the
  same place: it is `runtimeLive` membership too, so the handler holds three predicates and the
  report and the response share one of them. Say why the response cannot share the teardown's:
  `st.started` is true for a start still in flight, which is the reclaim whose incomplete slot
  release the gateway has no other way to observe.
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
  handler sends the §15.4.2 signal whenever no other bound entry remains
  (`session.go:259-261`), which a registered-but-unbound co-tenant about to call `StartSession`
  does not hold off, and emits a `ReportSessionScrub` that advances `sessionsServed` for a
  session the pod never ran.
- State the whole-pod recycle scrub as outside both teardowns and outside the arms that remove
  nothing. It runs whenever the request carries a recycle disposition, on every outcome
  `answerShutdown` builds, because both gateway
  senders of that disposition reach it on different arms: the concurrent release answers
  `absent`, its separate unconditional `Shutdown` having already torn the last slot down, and
  the session-mode release answers `reclaimed` on the removing arm, its recycle request being
  the session's only teardown. Name the returns that bypass it, the ones the `answerShutdown`
  paragraph above enumerates, each of which performs nothing, the scrub included.
- Note that `cancelPodMCPIfRuntimeIdle` under `removed` is safe: it is double-guarded by
  `runtimeIdleLocked` and `mcpArmingHeldLocked` (`pkg/adapter/slotsession.go:238-260`), so a
  shutdown of an unbound entry on a pod whose armed session still holds a slot cancels nothing.

`deregisterSlotLocked`'s doc comment gains one sentence recording that its unconditional timer
cancellation is now relied on by the unbound path as well as the bound one.

### CODE-2 · pkg/adapter/runtimegeneration.go, pkg/adapter/session.go, pkg/adapter/resume.go, pkg/adapter/sdkwarm.go · a start confirms the registry still holds its own attempt's entry before recording the runtime as holding the session

The shipped deliverable rested its confirmation on the bind epoch. The epoch is gone and the
confirmation survives in re-cut form: a `StartSession` or a `Resume` whose claim was admitted
against token T takes the session back off the runtime and refuses when the registry no longer
holds an entry carrying T.

The guard does not collapse into the resolve-time gates. Those gates run inside the claim; this
runs after `Runtime.Start` returns, and the window between them is exactly the window a §7.1
reclaim lands in. What the amended placement does remove is the ABA arm's reasoning. Under the
epoch the second refusal state was "the counter was reset or wrapped and a successor's entry
compares equal"; a `crypto/rand` token cannot ABA, so the state reduces to "the entry was
replaced", which the token compare states directly.

`noteRuntimeStarted` gains the confirmation rather than a fourth entry point beside it:

```go
// noteRuntimeStarted records the pod's one shared runtime process as holding
// sessionID, the record at which the slot reaches running, and reports whether
// the record was taken. It runs immediately after a successful start. It refuses in the two states a §7.1
// reclaim leaves: the registry holds no entry bound to this session, or it
// holds one stamped with a different bind attempt. Recording in either would
// put a session in runtimeLive that the registry does not hold under this
// attempt's identity, holding runtimeIdleLocked false and soleSession empty
// for the life of the pod.
//
// The second state is the replaced-entry case. SlotID == SessionID, so a
// successor attempt at the same session holds an entry under the same key
// with the same sessionID, and a predicate reading only st.sessionID cannot
// see that the entry belongs to a later attempt. The bind attempt can,
// because the successor stamped the entry it created with its own token and
// the adapter never overwrites a token on a resolve.
//
// attempt is the token this start's own claim was admitted against, passed in
// rather than re-read here: a read taken after the claim is as racy as the
// confirmation it anchors. The predicate reads the registry entry rather than
// st.started, because st.started is set before Runtime.Start and is therefore
// true for this very call.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract), rule 8
// (the start-confirmation rule); §15.4.3 (runtime integration levels).
func (s *Server) noteRuntimeStarted(sessionID, attempt string) bool {
    if sessionID == "" {
        return false
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    st, ok := s.slots[sessionID]
    if !ok || st.sessionID != sessionID || st.bindAttempt != attempt {
        return false
    }
    s.noteRuntimeStartedLocked(sessionID)
    return true
}
```

`claimSessionSlot` and `claimSessionSlotUnderLock` report the token the entry carried inside
the critical section that claimed the slot, so the value the confirmation compares is the one
the claim itself was admitted against. On a `ConfigureWorkspace` claim, which carries no token,
the reported value is the entry's own, which is whatever `Binder.Prepare` stamped; the
confirmation then compares the entry against itself and refuses only when the entry was
replaced, which is the property it exists for. Their callers hold the value across
`Runtime.Start` and pass it here.

`StartSession`'s call site (`pkg/adapter/session.go:163`) takes the rollback:

```go
if !s.noteRuntimeStarted(sessionID, claimedAttempt) {
    // spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract),
    // rule 8. The reclaim removed this slot while Runtime.Start ran. Take the
    // session back off the shared runtime process and refuse the start. No
    // cleanup outcome is reported: the slot never
    // reached §6.2's running, and §5.2 gives a session release at most one
    // such report, filed by the cleanup that reclaimed the slot, which
    // withheld it for the same reason. The registry is left alone: the
    // reclaim already removed this session's entry and its tree, and any
    // entry standing under this slot identifier now belongs to a later
    // attempt at the same session, whose workspace and credentials this
    // rollback must not delete. The pod-wide MCP surface is the one thing the
    // rollback still reclaims, and that call declines to cancel a surface a
    // surviving claimant holds.
    if s.Runtime != nil {
        _ = s.Runtime.Close(ctx, sessionID)
    }
    s.cancelPodMCPIfRuntimeIdle()
    // §16.3 (distributed tracing): a lost race is the TRANSIENT category. A
    // further attempt succeeds once the reclaim's residue is gone.
    rollbackErr := status.Errorf(codes.Aborted,
        "session %s slot was reclaimed while the start was in flight", sessionID)
    spanErr = tracing.CategorizeError(rollbackErr, tracing.CategoryTransient)
    return nil, rollbackErr
}
```

The rollback deregisters nothing, and that is deliberate. `noteRuntimeStarted` refuses exactly
when the registry holds no entry under this slot identifier, holds one whose `sessionID` names
a different session, or holds one stamped with a different bind attempt, because `st.sessionID`
is set to the map key by both of its production writers (`pkg/adapter/slotcreds.go:34`,
`pkg/adapter/slotsession.go:87`) and cleared by neither. A release keyed on the session
identifier alone therefore either removes nothing, which is the interleaving where the reclaim
already took the entry and the tree, or removes a later attempt's entry. The second case is
reachable rather than theoretical: `ensureSlotPaths` creates an entry for every
workspace-preparation RPC, so a §5.2 retry staging its workspace on the same pod holds
precisely that entry, and `releaseSessionSlot` there would delete it and `RemoveAll` the tree,
the uploads, and the credential directory the retry had just staged
(`pkg/adapter/slotsession.go:214-220`, `slot.go:210-212`). The rollback therefore runs
`cancelPodMCPIfRuntimeIdle` alone, which is the half it wants and the half that is already safe
against a successor: the cancellation is gated on `mcpArmingHeldLocked`, which reads
`s.slots[s.mcpSession]`, so a surface a surviving claimant holds is not cancelled
(`pkg/adapter/slotsession.go:238-260`).

The rollback answers `codes.Aborted`, and the choice of code is deliberate. The gateway derives
the §5.2 failure category from the gRPC code the failing stage returned: `SlotBindError.Reason()`
maps a `FailedPrecondition` outside the workspace stages to `policy_rejection`
(`pkg/gateway/podlifecycle/podsession/slotfailure.go:91-99`), which `NonRetryable()` reports
true for (`:41-48`), and the start stage this rollback fails is minted as
`slotFailureSessionStart` (`pkg/gateway/podlifecycle/podsession/slotbinder.go:322-324`,
`binder.go:293`). Answering `FailedPrecondition` here would therefore end the attempt at a 422
`SLOT_FAILED` carrying `retryable: false` and no `Retry-After`, with the §5.2 retry budget
unconsumed, for a failure a further attempt clears. `Aborted` takes the classifier's transient
default (`slotfailure.go:100-101`), so `applySlotRetryPolicy` still places its retry and
`classifySlotBindFailure` passes the error through with the retryable `STARTING_FAILED`
envelope intact on the create-time-reserved path
(`pkg/gateway/sessionserver/start.go:2766-2779`). No change is staged to that
switch. `Aborted` is also the code the identity gate answers, which is deliberate: both are
"another attempt owns this", both clear on a further attempt, and CODE-5's single `Aborted` arm
on `isTransientPodClaimError` covers the rollback, the reclaim-hold refusal, and the identity
gate with one arm rather than three. It is unused on the `StartSession` and `Resume` handlers,
where `FailedPrecondition` already refuses an unconfigured runtime (`pkg/adapter/session.go:104`)
and an unconfigured workspace base or checkpoint transport (`pkg/adapter/resume.go:33,:42`).

Scope of the call-site change:

- The compensation can race any RPC that admits a start on a slot the gateway may reclaim, and
  CODE-13 stages it at two such sites. Both take the rollback. `pkg/adapter/session.go:163`
  (`StartSession`) is the site `materializeSlot`'s start stage reaches. `pkg/adapter/resume.go:144`
  (`Resume`) is the site `Binder.Resume`'s compensating failure branch reaches, and the
  adapter's `Resume` runs the same claim, start, record sequence: it claims the slot at
  `pkg/adapter/resume.go:50`, calls `Runtime.Start` at `:140`, and records at `:144`. The resume
  rollback is the same three steps in the same order, on the inbound `ctx` as the `StartSession`
  one is. It takes no span-error categorization, because `Server.Resume` opens no span at all
  (`pkg/adapter/resume.go:25-35`) and so has no `spanErr` to set. It leaves the registry alone
  and reports no cleanup outcome either, for the reasons the `StartSession` rollback gives.
- `pkg/adapter/sdkwarm.go:261` takes the refusal as well. SPEC-5 states the confirmation as an
  obligation on every request that starts a session, so the site cannot discard the result even
  though no gateway compensation races it: its only gateway caller is `Binder.Launch`
  (`pkg/gateway/podlifecycle/podsession/binder.go:1009`), the exclusive path CODE-13 does not
  compensate and whose `failPhase` retires the pod. The refusal arm takes the runtime half of the
  §6.1 demotion and nothing else. It calls the runtime's own `SDKWarmRuntime.DemoteSDK`
  (`pkg/adapter/sdkwarm.go:134`), which is the in-process runtime's close and is what clears the
  pre-connected SDK, and clears `s.sdkConnected` under `s.mu`; a bare `Runtime.Close` leaves that
  flag true. It removes no registry entry and no slot tree, so it routes neither through
  `releaseSessionSlot`, the site's own failure idiom for its other arms
  (`pkg/adapter/sdkwarm.go:249-252`), nor through the `DemoteSDK` RPC handler
  (`pkg/adapter/sdkwarm.go:274`): both deregister the entry and remove the tree
  (`pkg/adapter/slotsession.go:214-220`), which is the act SPEC-5's confirmation rule bars on a
  refused confirmation, and a release keyed on the session identifier alone would take a later
  attempt's entry for the reasons the `StartSession` rollback gives. The arm answers
  `codes.Aborted`, which SPEC-5 and CONF-1 require of a refused confirmation, rather than the
  `codes.Internal` the site's other failure arms answer.
- Every test caller becomes `_ = s.noteRuntimeStarted(sessionID, attempt)`, taking the token
  from the claim it already makes, and every test caller must hold a bound registry entry when
  it records, because the guard now keys the record on that entry and its token.
  `pkg/adapter/export_test.go:45` (inside `ClaimSessionForTest`, after `claimSessionSlot`),
  `podmcp_arming_internal_test.go:84,185,230` (each after `claimSessionSlot("alice", …)`)
  already satisfy that and need only the discard. `usage_test.go:233` reaches its entry through
  `bindSessionForTest` (`usage_test.go:350-363`), which calls `ensureSlotStateLocked` and returns
  nothing, so that helper is widened to hand the token back and the caller takes it from there. `adapterevents_test.go:95` and `:184` do not,
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

### CODE-3 · pkg/sandbox/slotstate/slotstate.go, pkg/sandbox/slotstate/registry.go, pkg/gateway/runtime/slothealth/slothealth.go, pkg/gateway/sessionserver/sessionserver.go, pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go · the per-slot comment surfaces follow the fence's reduction and the edge list gains the pre-running cleanup edge

`ValidTransitions()` gains `{ReceivingUploads, SlotCleanup}`, and the doc comment's edge list
above it gains the matching line:

```go
//	receiving_uploads → slot_cleanup    (see §6.2 "Pre-running slot cleanup")
```

The glosses in that edge list are transcriptions of the §6.2 fence entries, and SPEC-4 reduces
three of the entries they transcribe to pointers, so the transcriptions become pointers to the
same homes. In `pkg/sandbox/slotstate/slotstate.go`:

- `receiving_uploads → running             (workspace ready, task dispatched)` becomes
  `receiving_uploads → running             (see §6.2 "Pre-running slot cleanup")`.
- `slot_cleanup      → released            (slot reclaimed)` becomes
  `slot_cleanup      → released            (see §5.2)`.
- `slot_cleanup      → leaked              (cleanup timeout exceeded)` becomes
  `slot_cleanup      → leaked              (see §5.2)`.

The three glosses SPEC-4 leaves alone, `slot_assigned → receiving_uploads`, `running →
slot_cleanup` and `running → failed`, stay verbatim, so the comment remains a faithful
transcription of the fence after SPEC-4 lands.

Four constant doc comments above that list carry the same retired statements, and each keeps its
existing `spec: §6.2` citation:

- `Running` reads `Running is the dispatched sub-state: the slot's workspace is ready and the task
  has been dispatched to the runtime with the slotId.` and becomes `Running is the sub-state a
  slot enters when the adapter records the pod's shared runtime process as holding the session.`
- `SlotCleanup` reads `SlotCleanup is the post-execution cleanup sub-state (task completed or
  failed, per-slot cleanup runs).` and becomes `SlotCleanup is the cleanup sub-state for one
  slot.`, because the new edge reaches it before the slot has run.
- `Released` reads `Released is the terminal sub-state for a slot whose workspace was removed,
  processes killed, and slotId released.` and becomes `Released is the terminal sub-state for a
  reclaimed slot.`
- `Leaked` reads `Leaked is the terminal sub-state for a slot whose cleanup timed out: the slot is
  not reclaimed until pod termination and remains counted in active_slots.` and becomes `Leaked is
  the terminal sub-state for a slot that is not reclaimed until pod termination and remains counted
  in active_slots.`

`pkg/gateway/runtime/slothealth/slothealth.go` states the same withdrawn trigger twice, and each
sentence keeps its consequence clause. In the `event` doc comment, `a §6.2 leaked slot (cleanup
timeout exceeded) persists until pod termination` becomes `a §6.2 leaked slot persists until pod
termination`. In the `RecordLeak` doc comment, `RecordLeak records that a slot on pod transitioned
to leaked (the §6.2 cleanup timeout was exceeded so the slot is not reclaimed until pod
termination).` becomes `RecordLeak records that a slot on pod transitioned to leaked, so the slot
is not reclaimed until pod termination.` That file's statements about persistence, in the package
doc, on `DefaultWindow` and on `Tracker`, are untouched, and so is `OccupiesSlot`'s quoted §6.2
sentence in `slotstate.go`.

Three further files state the same withdrawn trigger, and each statement takes the same
reduction the two above take: the retired ground is deleted, the consequence clause stands, and
the existing §6.2 citation is kept verbatim. No §5.2 pointer is added, because the §5.2 pointers
this deliverable writes belong to the edge list that transcribes the §6.2 fence, and §6.2 still
states what a leaked slot holds.

- `pkg/sandbox/slotstate/registry.go` · `MarkLeaked`'s doc comment opens `MarkLeaked records a
  slot whose cleanup timed out, so it remains counted in the pod's active_slots and leaked_slots
  until the pod terminates (spec §6.2).` and becomes `MarkLeaked records a leaked slot, so it
  remains counted in the pod's active_slots and leaked_slots until the pod terminates (spec
  §6.2).` The rest of that comment, which states the seeding behaviour and the returned count,
  is unchanged.
- `pkg/gateway/sessionserver/sessionserver.go` · the unexported `slotLeakGauge` field comment
  reads `the count of the pod's slots whose cleanup timed out and remain counted in active_slots
  until the pod terminates` and becomes `the count of the pod's leaked slots, which remain
  counted in active_slots until the pod terminates`. The exported `SlotLeakGauge` field comment
  reads `a pod's concurrent-workspace slots whose cleanup timed out and remain counted in
  active_slots until the pod terminates` and becomes `a pod's leaked concurrent-workspace slots,
  which remain counted in active_slots until the pod terminates`, keeping its trailing `spec:
  §6.2.`
- `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go` · the `adapterLeakedSlots`
  field comment reads `adapterLeakedSlots is the §6.2 per-pod count of concurrent-workspace
  slots whose cleanup timed out and are leaked (not reclaimed until pod termination).` and
  becomes `adapterLeakedSlots is the §6.2 per-pod count of leaked concurrent-workspace slots
  (not reclaimed until pod termination).` The comment above the gauge's construction reads
  `§6.2 — lenny_adapter_leaked_slots is the per-pod count of concurrent-workspace slots whose
  cleanup timed out and remain counted in active_slots until the pod terminates.` and becomes
  `§6.2 — lenny_adapter_leaked_slots is the per-pod count of leaked concurrent-workspace slots,
  which remain counted in active_slots until the pod terminates.` Each keeps its `Labels`
  sentence, and the gauge's `Help` string names no trigger and is unchanged.

Every site in this list is a doc comment that no gate reads, so the step's tiers are unchanged.
CODE-9 opens `gatewaymetrics_credential.go` for the superseded collector, which sits elsewhere
in the file, so the two deliverables do not collide.

`ValidTransitions()` and `TestValidTransitions_spec_6_2`'s `want` list are one statement of the
edge set, so CODE-3 moves both in its own step. Nothing compares either against
`spec/06_warm-pod-model.md`, so SPEC-4 and CODE-3 are separate steps. `IsValid(SlotAssigned,
Running)` stays illegal.

### CODE-4 · pkg/gateway/podlifecycle/podsession · the gateway mints a bind attempt, carries it on the bind sequence, and sets `unconditional_teardown` at every non-compensating `Shutdown` caller

Targets:

- `bindattempt.go` (new in `pkg/gateway/podlifecycle/podsession`) · `newBindAttempt`.
- `pkg/gateway/sessionserver/upload_to_session.go` (outside this package) · the §7.4
  mid-session pair sets `mid_session` true on `PrepareWorkspace` and `FinalizeWorkspace` and
  carries an empty `bind_attempt`.
- `slotbinder.go` · a new `slotCleanupBudget` helper; `materializeSlot` mints the attempt's
  token at the top; `Binder.ReleaseSlot`'s `Shutdown` and `ShutdownRecycle` calls set
  `unconditional_teardown`.
- `binder.go` · `Binder.Prepare` and `Binder.Resume` mint and carry; `Binder.shutdownAdapter`
  and the recycle path set `unconditional_teardown`.
- `pkg/gateway/runtime/adapterclient/client.go` · the two `Shutdown` forms become two methods,
  and the bind-sequence methods take the token and the mid-session flag as explicit arguments:
  `AssignCredentials` and `RunSetup` each gain a trailing `bindAttempt string` parameter,
  `FinalizeWorkspace` gains `bindAttempt string` before its shipped `midSession bool`,
  `PrepareWorkspace` gains a trailing `bindAttempt string, midSession bool`, and `ResumeParams`
  gains a `BindAttempt string` field. The `Client` sets each argument on its request as given
  and derives neither field from the other.

**The mint.**

```go
// newBindAttempt mints the opaque per-attempt token §4.7.1 fences the slot
// registry entry with. It is called once per bind attempt, before that
// attempt's first RPC, and the value is carried on the requests §4.7.1's
// carriage table marks it carried on and named again on the compensating
// Shutdown.
//
// crypto/rand.Read cannot return an error on the Go version this module pins
// (go.mod declares go 1.25.0); it panics if the system source fails. There is
// deliberately no error branch here, and a later reader should not add one.
//
// spec: §4.7.1 (role and gateway RPC contract)
func newBindAttempt() string {
    var b [16]byte
    _, _ = rand.Read(b[:])
    return hex.EncodeToString(b[:])
}
```

Mint sites, one each: `materializeSlot` (`slotbinder.go:265`), `Binder.Prepare`
(`binder.go:843`), and `Binder.Resume` (`binder.go:1590`). `Binder.Launch` (`binder.go:976`)
mints none, because neither request it issues carries `bind_attempt` (§4.7.1 **Bind attempt
token**, the lead-in to the carriage table).

**What each attempt carries.** `materializeSlot` and `Binder.Prepare` both run
`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup` and `AssignCredentials`, so both carry the
token on four requests; `materializeSlot` additionally names it on its compensating `Shutdown`.
`Binder.Resume` carries it on `ResumeRequest` and names it on its compensation. Every one of
those requests sets `mid_session` false, which is what the wire rule requires of a request
carrying a token. The §7.4 mid-session pair `pkg/gateway/sessionserver/upload_to_session.go`
sends is the mirror: `mid_session` true on both `PrepareWorkspace` and `FinalizeWorkspace`, and
an empty `bind_attempt`.

**Every non-compensating `Shutdown` caller sets `unconditional_teardown = true`.** The full
list, each named because the precondition refuses a request that sets neither field:

| Site | Call |
|:--|:--|
| `Binder.ReleaseSlot`, the session-end teardown | `slotbinder.go:542` |
| `Binder.ReleaseSlot`, the recycle disposition | `slotbinder.go:574` `ShutdownRecycle` |
| `Binder.shutdownAdapter`, the exclusive-path teardown | `binder.go:2043` |
| `Binder.shutdownAdapter`, its recycle disposition | `binder.go:2037` `ShutdownRecycle` |
| the §11.4 full-revoke fan-out | `cmd/lenny-gateway/user_revocation.go:129` |

`Client.Shutdown` and `Client.ShutdownRecycle` set the field themselves rather than taking it
as a parameter, so no caller can forget it and the compensation is the one path that reaches
the fenced form. The fenced form is a separate method, `Client.ShutdownReclaim`, which sets
`bind_attempt` and leaves `unconditional_teardown` false. Both exported forms build the request
through one unexported builder, `Client.shutdown`
(`pkg/gateway/runtime/adapterclient/client.go:807-824` and `:860-867`), so the field is set in a
single place and no call site above is edited for it. A teardown caller added later, an adoption
or handoff teardown among them, reaches one of those two methods and is carried by the same
statement, so it needs no row in the table above.

**The budget:**

```go
// slotCleanupBudget is the §5.2 per-slot cleanup timeout,
// max(cleanupTimeoutSeconds / maxConcurrentSessions, 5) seconds. §5.2 assigns
// that figure to the adapter's own cleanup enforcement; the gateway reuses it
// as its own give-up bound on the compensating Shutdown, and pins half of it
// as the adapter's graceful window, rather than inventing a constant.
// cleanupTimeoutSeconds is optional, so an unset pool yields the 5s floor.
// spec: §5.2 (pool configuration and execution modes); §7.1 (normal flow).
func slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32) time.Duration
```

**`Binder.Prepare` and `Binder.Resume` mint and carry.** `Resume` is the first and only
entry-touching RPC of its attempt and creates the entry through its own claim
(`pkg/adapter/resume.go:50`, ahead of every path-resolving step), so `ResumeRequest` carries
`bind_attempt`.

### CODE-13 · pkg/gateway/podlifecycle/podsession · the gateway compensates every post-connection bind failure and every failed resume, and scopes the lease release to the attempt

Targets:

- `slotfailure.go` · `SlotBindError` gains a `Leaked bool` field.
- `slotbinder.go` · a new `compensationCause` helper and a new `compensateFailedSlotBind`
  method; `materializeSlot` splits into a stage runner and a compensating wrapper;
  `ReleaseSlotReservation` takes the disposition; `BindReservedSlot` and `ClaimSlot`'s
  connect-stage release pass it.
- `binder.go` · `assignCredentials` returns the lease identifiers it minted, and
  `Binder.Prepare`'s credential-assignment failure arm releases those by identifier;
  `Binder.Resume` compensates before `cl.Close()`; `releaseResumeSlot` takes the disposition
  and returns its release error.

**The compensation.** It carries the attempt's own token, returns the outcome rather than a
boolean, and leaves the mapping to its caller:

```go
// compensateFailedSlotBind reclaims the pod-side state a failed bind created,
// naming the bind attempt that created it, and returns the outcome the adapter
// answered. It touches pod-side state only. The gateway-side §4.9 credential
// leases belong to the caller, because the two bind entry paths mint them
// inside materializeSlotStages and the resume path mints none.
//
// It returns the outcome rather than a leaked boolean so the caller maps a
// value it recognizes and counts one it does not, instead of a false answer
// travelling as a disposition. An RPC error is reported as such.
//
// The context is detached from the caller's: the residue class this exists for
// arises when the caller's context expired during StartSession, so a reclaim
// issued on that context would fail in the one case that leaves a runtime
// running for an abandoned session.
//
// The call carries two bounds and they differ deliberately. The fourth
// argument is the graceful window the adapter spends on the runtime close, and
// the RPC deadline is the budget above, which outlasts that window so the
// gateway does not give up on the adapter's SIGTERM pivot. A cleanup whose
// tree removal outruns the remaining budget answers nothing in time, and that
// is the unanswered reclaim §7.1 accounts. The shipped §11.4 revoke fan-out
// holds the same relation, with userTerminateRPCTimeout bounding the call and
// the shorter userTerminateDeadline sent as the graceful window.
//
// The reclaim reuses the connection the failed stage holds for cost; §7.1
// states that the fence does not depend on it.
//
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract); §5.2 (pool
// configuration and execution modes).
func (b *Binder) compensateFailedSlotBind(
    ctx context.Context, cl *adapterclient.Client,
    sessionID, bindAttempt string,
    cleanupTimeoutSeconds int, maxConcurrentSessions int32,
    sandboxName, slotID string,
) (outcome adapterv1.SlotReclaimOutcome, exitedCleanly bool, err error) {
    budget := slotCleanupBudget(cleanupTimeoutSeconds, maxConcurrentSessions)
    rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
    defer cancel()
    return cl.ShutdownReclaim(rctx, sessionID, "slot_bind_failed", budget/2, bindAttempt)
}
```

`ShutdownReclaim` returns the outcome, `exited_cleanly` and the error, in that order.

**The leaked disposition, at the caller.** The outcome does not enter it. Under §4.7.1 rule 15
the disposition is the one `Binder.ReleaseSlot`'s session-end teardown at `slotbinder.go:543` already
computes, and SPEC-2's commentary gives why it reads no outcome.
The staged `materializeSlot` body below carries the expression and its comment.

The expression is written at the compensation's call site rather than as a free function, which
is why the staged `materializeSlot` body changes with it.

The outcome is read for observability alone. The label's value comes from
`compensationCause(err error) string`, declared in `slotbinder.go` beside the wrapper, which
returns `refusal` when `err` matches CODE-7's `ErrSlotBindAttemptSuperseded` or
`ErrSlotBindAlreadyStarted` under `errors.Is` and `failure` otherwise. It is the one statement of
that value, and both compensating call sites pass it the attempt's error.
`Binder.noteCompensationOutcome(outcome, cause, pool, podName)` is a method on `Binder` rather
than a free function, because the series SPEC-6's
§16.1 row states is labeled by `pool`, `k8s_pod_name` and `cause` and is emitted through a
`Binder` hook, both of which a package-level function can reach. It fires only on
`SLOT_RECLAIM_OUTCOME_SUPERSEDED`, so the branch lives in one place, and nothing else consumes
the value. CODE-9 states the hook, its forwarder and its wiring.

`SUPERSEDED` and `ABSENT` are reclaims acknowledged clean that correctly performed nothing. On `ABSENT`
the adapter holds nothing for the session; on `SUPERSEDED` another attempt owns the slot
identifier and everything under it, or the entry carries no token at all, which is what a
`StartSession` or a `ConfigureWorkspace` creates. Either way this attempt owns nothing the
reclaim could release. An untokened entry survives the reclaim, which SPEC-5 records as a
residue. Reading
`SUPERSEDED` as leaked would withhold the pod's slot-counter decrement for the life of the pod
on the common retry case.

**The reason string.** The reclaim's reason is `"slot_bind_failed"`, and where the reclaim
reaches a session whose start the adapter admitted, the intra-pod `terminate` frame carries
`session_complete`. `drainReason` maps a §4.7 `ShutdownRequest.reason` onto the closed
four-value `terminate` enum and returns `session_complete` for every value outside it
(`pkg/adapter/session.go:309-321`), so the compensation mints no wire value. The shipped §11.4
full-revoke path already takes that same default with `"USER_REVOKED"`. The frame's schema
requires `deadlineMs` and fixes its minimum at 100, and the budget's five-second floor puts
half the budget at 2500ms or above, so the value this call sends satisfies that minimum on
every pool configuration. A reclaim of a bound-but-unstarted session sends no frame at all,
because the drain sits inside CODE-15's `started` block.

**The wrapper.** `materializeSlot` becomes a wrapper so no stage can be added later without the
compensation, and the wrapper is where the token is minted and where the lease release runs:

```go
func (b *Binder) materializeSlot(
    ctx context.Context, req SlotBindRequest,
    sandboxName, slotID, podIP, workspaceBase string,
    cl *adapterclient.Client,
) (*BindResult, error) {
    attempt := newBindAttempt()
    res, minted, err := b.materializeSlotStages(ctx, req, sandboxName, slotID, podIP, workspaceBase, cl, attempt)
    if err != nil {
        // spec: §7.1 (normal flow). A typed refusal is compensated too; §4.7.1
        // rule 13 answers it superseded.
        var sbe *SlotBindError
        if errors.As(err, &sbe) {
            outcome, cleanly, cerr := b.compensateFailedSlotBind(
                ctx, cl, req.SessionID, attempt,
                req.CleanupTimeoutSeconds, req.MaxConcurrentSessions, sandboxName, slotID)
            // spec: §6.2 (pod state machine); §7.1 (normal flow). Leaked is exactly
            // "not acknowledged clean"; under §4.7.1 rule 15 the error and the
            // clean-exit flag decide it for every outcome.
            sbe.Leaked = cerr != nil || !cleanly
            b.noteCompensationOutcome(outcome, compensationCause(err), req.Pool, sandboxName)
        }
        // spec: §7.1 (normal flow); §4.9 (credential leasing service). Releases
        // the leases this attempt minted, never the session's, and runs on every
        // failure. It must not move inside the errors.As guard: the credential-
        // assignment stage can fail, or be refused, after minting.
        b.releaseAttemptCredentials(minted)
        cl.Close()
        return nil, err
    }
    return res, nil
}
```

**The lease release is scoped to the attempt.** `credassign.Service.ReleaseSession` walks
`s.leases.LeasesBySession([]string{sessionID})` and releases every lease it finds
(`credassign.go:400-409`), and the slot identifier is the session identifier, so a release after
a successor has assigned strips the successor's leases. Moving the call inside the `errors.As`
guard is the wrong fix: it trades lease-stripping for lease-leaking, because an attempt that
minted leases, owns the entry, and failed with an error that is not a `*SlotBindError` would
never return them. The fix is to narrow what the call releases.
`materializeSlotStages` therefore returns the lease identifiers `assignSlotCredentials` minted
for this attempt, and `releaseAttemptCredentials` releases those by identifier.
The release calls `CredentialAssigner.Release(leaseID string)`, a member the interface
(`binder.go:319-332`) gains and that both production implementations already satisfy
(`credassign.Service.Release` at `credassign.go:380` and `credassign.Client.Release` at
`client.go:299`); `AssignProto` already returns the identifier on the wire lease, so no other
value is threaded to the call site.
`b.releaseCredentials(sessionID)`, the session-wide walk, stays where it legitimately belongs,
on the session-end path.

The two obligations sit at different levels because they reclaim different state. The
compensation is pod-side, so both bind paths and the resume path take it. The lease release is
gateway-side and belongs to the attempt that minted the lease, so each bind path takes it at its
own minting boundary. On the concurrent path that boundary is this wrapper, which spans exactly
the stages that mint one. On the exclusive path it is `Binder.Prepare`'s credential-assignment
failure arm, because `assignCredentials` mints outside any such wrapper (`binder.go:1225`,
`:1248`) and `failPhase`'s session-wide release is gated on `leaseAssigned`, still false at that
arm (`binder.go:948-954`). The arm releases unconditionally, outside CODE-8's refusal guard.

Every stage inside `materializeSlotStages` is post-connection, so the compensation is
unconditional there. A stage whose failure sent no RPC (a `stageWorkspace`
failure, through a source-rewrite error or a blob-store failure, which returns before
`PrepareWorkspace` is sent) still sends it; the
adapter answers `ABSENT` for a session it
holds nothing for, `Leaked` is false, and a blob-store outage adds nothing to the pod's
persistent leak count.

**`ReleaseSlotReservation` takes the disposition** and threads it to the parameter
`SlotClaimer.ReleaseSlot` already implements:

```go
func (b *Binder) ReleaseSlotReservation(ctx context.Context, sandboxName, slotID string, leaked bool) error
```

Its doc comment's current justification for sending nothing, "the failed attempt already closed
its adapter connection", is replaced: the reclaim now runs at the failure site while the
connection is open, and this function releases the reservation afterwards. The `leaked=false`
comment is corrected too: it is true for the connect stage, where the slot was reserved before
any workspace RPC, and false for every stage the reclaim covers.

Call sites for the new parameter:

| Site | Value |
|:--|:--|
| `slotbinder.go` `ClaimSlot`'s connect-stage release | `false`, reserved before any workspace RPC, so the adapter holds nothing |
| `slotbinder.go` `BindReservedSlot` | `sbe.Leaked`, and the method sets `sbe.Leaked = true` on the error it is about to return when its own release fails |
| `binder.go` `releaseResumeSlot` | the disposition its caller computed, and the function returns its `ReleaseSlotReservation` error to that caller rather than only logging it |
| `start.go` `slotBinder` interface declaration | signature only |
| `slotretry_test.go` `fakeSlotBinder` and `slotretry_load_test.go` `concurrentSlotBinder` | signature only, plus `fakeSlotBinder.released` widening from `[][2]string` to a record carrying the pod, the slot identifier and the disposition |
| `start.go` `applySlotRetryPolicy` | `sbe.Leaked` |
| `start.go` `rollbackClaim` | `false`, no workspace RPC runs at create |

**`Binder.Prepare` sends no compensation.** Every `Prepare` failure runs
`failPhase`, which drains the pod (`binder.go:1072-1082`), so the entry dies with the pod and
there is nothing for a compensation to collect. Its credential-assignment failure arm takes
the attempt-scoped lease release above, and its reclaim closure takes CODE-8's short-circuit. The abandoned
`Prepare` case on the exclusive path stays out of scope, as the problem statement records.

**`Binder.Resume` compensates.** The compensation names the token `ResumeRequest` carried. Its
adapter-RPC failure branch
currently calls `cl.Close()` and then `releaseResumeSlot`; it becomes the compensation on the
still-open connection, taking `req.SessionID`,
`req.CleanupTimeoutSeconds` and `req.MaxConcurrentSessions` off the `ResumeRequest`, then
`b.noteCompensationOutcome(outcome, compensationCause(err), req.Pool, sb.Name)`, then the close, then the release
carrying the outcome. It releases no credential lease: `Binder.Resume`
mints none, its §7.3 retry re-mints none, and the session may still hold the leases its
original bind minted, so returning them on a retryable failure would leave every later resume
of that session running with leases the gateway has already released. `releaseResumeSlot`
returns that release's error rather than only logging it, so the branch can read the release
outcome as the other two release sites do. The branch also returns its error with a
`*SlotBindError` in the chain, carrying the pod, the reserved slot id, the stage `"resume"`,
and a `Leaked` set to the same `compensation leaked || relErr != nil` discriminator
`applySlotRetryPolicy` and `BindReservedSlot` use, wrapped inside the message the branch returns
today. When the failure is the client's own deadline and the adapter's `Resume` handler is still
running, that compensation waits on the slot guard CODE-14 gives `Resume`, charged against the
compensation's own budget, and what bounds the wait is the `Resume` handler's context: the chunk
fetches run on it (`checkpointtransport.go:99-116`), so the deadline that triggered this
compensation has already cancelled them, the pipe closes with that error and
`workspace.ExtractTree` returns. When the budget expires before that, the removing arm takes the
disposition CODE-14 states under **Disposition of an expired acquisition at a removing site**, and
the residue is recorded among the accepted failure modes. That is what carries the disposition to the accounting in CODE-5; `SlotBindError.Unwrap`
returns the cause, so the wrapping leaves `isTransientPodClaimError`'s classification as it is
and resolves the `codes.Aborted` arm CODE-5 adds the same way, because `status.Code` walks the
chain with `errors.As`. On an exclusive pool `reserveResumeSlot` returns an empty slot id, so
the error carries one too and the accounting does not run.

Under the amended mechanism the resume path gains a property the shipped text could not give
it. A `Resume` whose response is lost carries its own token, so its compensation matches the
entry that resume created and tears the orphan down; and a stale compensation from an earlier
attempt at the same session carries a different token and is refused `superseded`, so it cannot
destroy the live resumed session. Both were open under a compensation that could only send the
unconditional form.

The counters this compensation feeds are CODE-9's.

### CODE-5 · pkg/gateway/sessionserver/start.go · one accounting helper serves every bind path the reclaim obligation binds, and the resume classifier learns the adapter's transient code and the started-session refusal

The placement constraint the earlier revision carried is withdrawn whole, and with it the
per-request pod-exclusion field on `podsession.SlotBindRequest` and `podclaim.SlotRequest`,
the pointer threading through `bindSlotWithRetry` and `applySlotRetryPolicy`, the two
candidate-pass skips in `ClaimSlot`, and the tier-2 placement-filter cases. Three grounds, each
sufficient. Its only write site was the within-request retry loop, so it never reached defect
17's retry, which is a new client request building a fresh `SlotBindRequest`. Full exclusion on a small pool returns
`ErrNoConcurrentSlot` and a spurious `WARM_POOL_EXHAUSTED`. And the state it was reaching for is
now covered at the pod: a retry that lands on a pod holding a leaked entry is refused by the
identity gate rather than admitted onto it. SPEC-2's `Max retries` edit, which existed for
nothing else, comes out with it.

Targets: the classify, record and threshold tail of `applySlotRetryPolicy`,
`bindConcurrentSlot`'s `BindReservedSlot` branch, `resumeOnPod`'s `podBinder.Resume` failure
branch, the `slotBinder` interface, and `isTransientPodClaimError`.

Extract the tail only. The release stays with each caller, which already knows its own
disposition:

```go
// accountSlotFailure records one failed or leaked slot against the pod's §5.2
// health ledger and retires the pod when the combined windowed-failure plus
// persistent-leak count crosses the unhealthy threshold. Every bind path §7.1
// binds calls it, both concurrent bind paths and the §7.3 re-attach, so the
// create-time reserved path and the checkpoint restore reach the threshold
// §5.2 obliges them to reach, which the trigger states with no carve-out by
// code path.
//
// spec: §5.2 (pool configuration and execution modes); §6.2 (pod state machine).
func accountSlotFailure(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
    slots *slotstate.Registry, replacement func(pool string),
    leakGauge func(pod, pool string, leaked int),
    pool string, maxConcurrentSessions int32,
    sbe *podsession.SlotBindError, leaked bool)
```

The two request-derived parameters are the pool name and the concurrency bound rather than a
whole `podsession.SlotBindRequest`, because the body reads only `req.Pool` and
`req.MaxConcurrentSessions` off one today, and the resume caller below holds a
`podsession.ResumeRequest` rather than a bind request. Both existing callers pass `req.Pool` and
`req.MaxConcurrentSessions`.

The body is today's `MarkLeaked` plus leak gauge plus `RecordLeak` arm, today's `RecordFailure`
arm, and the unchanged `Unhealthy → DrainSandbox → replacement → Forget → ForgetPod →
zero-gauge` tail, with the discriminator taken as a parameter instead of computed inline.
`binder` is still needed, for `DrainSandbox`.

Callers:

- `applySlotRetryPolicy` keeps its own `ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID,
  sbe.Leaked)` call and passes `req.Pool`, `req.MaxConcurrentSessions` and `sbe.Leaked ||
  relErr != nil`. The loop's exit condition is unchanged and the non-retryable set stays §5.2's
  three reasons, so a clean release retries on the same pod exactly as it does today. It keeps
  the request by value, because nothing in this revision needs the append the pointer existed
  for.
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
  `reserveResumeSlot` returns an empty slot id when `MaxConcurrentSessions <= 1`, so an
  exclusive pool reserved nothing and has nothing to account. Without this caller the re-attach
  is the one bind path §7.1 binds whose leaked slot holds occupancy permanently while the pod is
  never counted unhealthy, never drained through the `ceil(maxConcurrentSessions / 2)` trigger,
  and never surfaced on the gauge.

`applySlotRetryPolicy`'s comment that "a clean release makes the failure transient, and a
failed release leaks the slot permanently" is widened rather than corrected: it is true today
about the reservation release, and the change extends the release-outcome test from the
reservation counter to the pod-side reclaim.

The trade this deliverable ships, stated here rather than left open: a leaked disposition
withholds the counter decrement for the life of the pod, and `UnhealthyThreshold` is
`int((maxConcurrent+1)/2)`, so at `maxConcurrentSessions: 2` one unacknowledged reclaim both
burns a slot and drains the pod. This is §6.2's semantics applied consistently, and it is the
mechanism that makes a best-effort compensation's failure bounded. On the retry path the
disposition changes behaviour only at `maxConcurrentSessions >= 3`, because at 2 the threshold
is already 1 and a single cleanly released bind failure drains the pod today. The reserved
branch is the other case, and it changes at every concurrency: it reaches no tracker today
(`pkg/gateway/sessionserver/start.go:2594-2605`), so at `maxConcurrentSessions: 2` its first
accounted bind failure of either kind drains a pod that nothing drains today. That is the
conformance gap closing rather than a threshold change, and the threshold itself is untouched.

**The refusals stay retryable on the concurrent-bind path, and no production change on that
path is what makes them so.** The reclaim hold answers `codes.Aborted` carrying
`slot_reclaim_in_progress`, and the identity gate answers `codes.Aborted` carrying
`SLOT_BIND_ATTEMPT_SUPERSEDED`. `SlotBindError.Reason()` has no `Aborted` case, so both take
the transient default (`pkg/gateway/podlifecycle/podsession/slotfailure.go`), and
`classifySlotBindFailure` passes a transient failure through with the retryable §15.1
`STARTING_FAILED` envelope intact. That switch is not edited and may not be: opening a case for
`Aborted` in it would break both refusals and CODE-2's rollback, which rely on the same default.
What makes the retryability hold is CODE-6's `slotResolveError` helper on the adapter side,
because five adapter sites re-wrap any slot-resolve failure as `codes.InvalidArgument`, which the
same switch maps to `SlotReasonWorkspaceValidation` and `NonRetryable()` reports true for.

The phase gate is the exception and is correctly signed. `SLOT_BIND_ALREADY_STARTED` answers
`codes.FailedPrecondition`, which `Reason()` maps to `policy_rejection` outside the workspace
stages and which `NonRetryable()` reports true for. That is the right classification on this
path: the session has already started on this pod, and a retry of the same bind onto the same
slot is not going to change that. It is not the right classification on the §7.3 resume path,
where each retry makes a new claim, which may land on another pod, and the refusal is a fact
about the pod holding the stale entry rather than about the session, so the resume classifier
below treats it as transient. The spec basis is §7.2's session state machine and §6.2's
`resuming` failure transitions, which return a `resuming` session whose re-attach fails, a
non-retryable failure included, to `awaiting_client_action`, and §15.1's `RESUME_FAILED` row, under which the row stays there for
the client's explicit resume. Rule 6's `CATEGORY_PERMANENT` classifies the refused request on that
pod rather than the session's state.

**The §7.3 resume path reads a second classifier, and that one is extended.**
`isTransientPodClaimError` (`pkg/gateway/sessionserver/start.go:3648-3681`) is what
`holdOrFailOnResumeError` reads to decide whether a failed resume reverts the row to
`awaiting_client_action` for the client's `POST /v1/sessions/{id}/resume` or demotes it to
terminal `failed`. Every arm it carries today is a typed error or a sentinel, so a bare
`codes.Aborted` status matches none of them and falls through to `return false`, which demotes
a session the refusal says to retry. Add these arms after the existing switch and before that
final `return false`:

```go
	}
	if status.Code(err) == codes.Aborted {
		// spec: §15.4 (runtime adapter specification); §5.2 (pool configuration
		// and execution modes). ABORTED is the adapter's transient wire
		// classification for a bind it refused rather than failed. It covers
		// CODE-6's reclaim-hold refusal, the identity gate's
		// SLOT_BIND_ATTEMPT_SUPERSEDED, and the rollback CODE-2 answers on a
		// failed start or resume. All three succeed on a fresh attempt, so the
		// row holds in awaiting_client_action for the client's explicit resume
		// retry rather than going terminal.
		return true
	}
	if errors.Is(err, adapterclient.ErrSlotBindAlreadyStarted) {
		// spec: §4.7.1 (role and gateway RPC contract), rule 6; §7.3 (retry and
		// resume). The started-session refusal is a refusal of this pod rather
		// than of the session: a resume retry makes a new claim, which may land
		// on another pod, and the entry that refused it is a residue on this
		// one. Other resume causes keep the shipped classification, which the
		// summary records as a defect this proposal does not stage.
		return true
	}
	return false
```

The first arm reads the status code rather than CODE-6's adapter-local sentinel because that
sentinel is minted in the adapter process and no production file under `pkg/gateway` imports
`pkg/adapter`; the code is the part of the refusal that survives the wire. The second arm reads
CODE-7's gateway-side sentinel rather than the `FailedPrecondition` code, because this proposal
reclassifies only the refusals it introduces, and a `FailedPrecondition` workspace-root mismatch
is not one of them. CODE-7's typed
sentinels are the other half and are matched where the distinction between the two new codes
matters, which is the reclaim closures and this second arm; the first arm needs
only the code, so it covers all three `Aborted` producers. `status.Code` resolves through a wrapper
with `errors.As`, so the arm fires on the bare status and equally through the `*SlotBindError`
that CODE-13 makes `Binder.Resume` return. The wire envelope needs no change:
`writePodClaimError`'s default arm already answers the retryable 503 `RESUME_FAILED` with a
`Retry-After` for a cause it does not recognise, so for these refusals the row-state classifier
was the half that disagreed with it. Every other resume cause no arm recognises keeps that
disagreement, which the summary records as a defect this proposal does not stage.

No in-gateway wait-and-retry is staged for the hold. SPEC-3's §5.2
`**Slot-identifier reclaim hold.**` paragraph and the disposition table above it state when the
hold ends, and where the cleanup does not complete it does not end before the pod does. A
gateway-side wait would hold the client's request open for an interval nothing bounds and would
state a timeout in a second place. The attempt spends a retry on the refusal instead, which the
spec-changes file records among its accepted failure modes.

### CODE-6 · pkg/adapter/bindattempt.go, pkg/adapter/slot.go, pkg/adapter/slotsession.go, pkg/adapter/staging.go, pkg/adapter/slotcreds.go, pkg/adapter/credentials.go, pkg/adapter/resume.go, pkg/adapter/sdkwarm.go, pkg/adapter/holdstate.go, pkg/adapter/server.go · the bind-attempt token, the rule 2-through-7 predicate at the one resolve chokepoint, the reclaim hold, and the shared resolve helper

This deliverable lands the mechanism the other adapter deliverables insert into, so it lands
before them and compiles alone. W8's per-slot guard is CODE-14.

Every name in this deliverable is new to the tree. `slotState` carries no token field today
(`pkg/adapter/slot.go`), `ensureSlotStateLocked` (`slot.go:105`) and `ensureSlotPaths`
(`slot.go:140`) take a slot identifier and nothing else, and `slotsession.go:174` declares
`deregisterSlotLocked` with no reclaim helper beside it. Nothing named `bindEpoch`,
`BindEpoch`, `reclaimSlotLocked` or `slotResolveError` exists anywhere under `pkg/adapter` or
`pkg/gateway/runtime/adapterclient`, so this deliverable removes nothing and every element
below is an addition.

**New file `pkg/adapter/bindattempt.go`**, holding:

- The hold set's helpers, the three refusal sentinels, and the shared resolve helpers. The
  fields themselves are declared where their structs are: `Server.reclaiming
  map[string]struct{}`, guarded by `s.mu`, on `Server` in `pkg/adapter/server.go`, and `slotState.bindAttempt string` on `slotState` in
  `pkg/adapter/slot.go`. Go declares a struct's fields in the file that declares the struct, so
  those two files are opened by this deliverable even though they hold none of its logic.
- The `slotResolve` parameter type and its documentation:

  ```go
  // slotResolve carries what a caller asserts about the entry it is resolving.
  // Every field is a caller assertion rather than a discovered fact, and the
  // rule 2-through-7 predicate in ensureSlotStateLocked is the only reader.
  //
  // spec: §4.7.1 (role and gateway RPC contract), rules 2 through 7
  type slotResolve struct {
      // bindAttempt is the caller's per-attempt token. The empty string means
      // the caller asserts no attempt identity, which a §7.4 mid-session
      // request and ConfigureWorkspace both do.
      bindAttempt string
      // allowCreate is false for a mid-session RPC, which must resolve an
      // entry that already exists and must never create one.
      allowCreate bool
      // allowStarted is true for a mid-session RPC and for
      // ConfigureWorkspace's idempotent repeat, and false everywhere else.
      allowStarted bool
  }
  ```

- `reclaimSlotLocked(sessionID) (st *slotState, removed, boundRemains bool, release func())`,
  in `pkg/adapter/slotsession.go` beside `deregisterSlotLocked`, which it wraps. It runs the
  deregistration and, when that removed an entry, inserts the identifier into the hold set in
  the same critical section, so nothing can be admitted between the removal and the destructive
  steps that follow it. `release` is always non-nil and idempotent. A caller defers it and takes it
  only on the arm where the cleanup it then ran completed, which is when every destructive act
  that cleanup owed the slot returned without error, because §5.2 ends the hold on a completed
  cleanup and keeps the identifier held for the life of the pod on one that did not complete. A
  path that removed nothing ran no cleanup and takes the no-op. Callers hold `s.mu`. This is
  the only site that takes a hold and the only one that ends one; the side table holds
  `struct{}` because nothing reads a value from it: no entry can stand under a held identifier,
  so there is no token to compare against.
- **Every site that deregisters an entry it then destroys goes through the helper**, rather than
  the hold being inserted at one of them. There are three in the tree and the hold does not
  distinguish them: `Shutdown` (`pkg/adapter/session.go`), `releaseSessionSlot`
  (`pkg/adapter/slotsession.go`, whose callers are the start-path rollbacks in `session.go`,
  `resume.go` and `sdkwarm.go`, `DemoteSDK` among them), and the §10.1.4 hold termination
  (`deregisterStartedSessions`, whose members are destroyed one at a time in
  `terminateHeldSession`, `pkg/adapter/holdstate.go`, one member at a time and seconds later). `heldSession`
  gains a `release func()` field so pass 1 carries each member's hold to the pass-2 call that
  ends it, and `terminateHeldSession` defers it behind the same completion predicate. That site's
  cleanup owes the slot both a runtime close and a tree removal, so it captures both results
  rather than discarding either: it binds `closeErr := error(nil)` and assigns
  `closeErr = s.Runtime.Close(ctx, m.sessionID)` under the existing `s.Runtime != nil`
  guard, on the pass's `ctx` (CODE-14's **`terminateHeldSession`'s guard and close context.**
  re-scopes it), replacing the `_ =` discard at `pkg/adapter/holdstate.go:249`,
  and binds `treeErr := s.removeSlotTreeVia(m.state)`, replacing the discard at `:254`. It takes the release
  only on `closeErr == nil && treeErr == nil`; CODE-14 adds the `guarded` conjunct.
  A non-nil `closeErr` is logged as `slog.Warn("runtime_close_failed", "slot_id", m.sessionID,
  "error", closeErr)`, the sibling record of the tree-removal warning stated below, because
  nothing else records that failure. The close stays best-effort in control flow: neither error
  aborts the termination, so the final usage flush, `noteRuntimeClosed`, the tree removal,
  `cancelPodMCPIfRuntimeIdle` and `EmitAdapterTerminating` all still run on either arm, and only
  which arm takes the hold release changes. That is what §5.2 requires of this site, because the
  paragraph names the close of the session on the pod's shared runtime process among the acts the
  cleanup owes the slot and bounds that close for this very termination by a graceful window of
  ten seconds; a release taken on the tree removal alone would readmit binds onto a slot whose
  agent process the failed close may have left running.
  `deregisterSlotLocked` keeps its signature because
  `reclaimSlotLocked` calls it; after CODE-6 every production caller goes through that helper and
  no caller takes the deregistration without a destroy. `deregisterSlot` is retired into the
  helper. Its two test callers, `pkg/adapter/podmcp_arming_internal_test.go:88` and `:245`,
  become `s.mu.Lock(); _, removed, _ := s.deregisterSlotLocked("alice"); s.mu.Unlock()`, and
  each keeps its `!removed` assertion; they do not go through `reclaimSlotLocked`, because the
  reclaim hold it opens on `"alice"` is released nowhere in either test. In the same rewrite `releaseSessionSlot` calls `s.removeSlotTreeVia(st)` in place of
  the discarded `removeSlotTree(st)` at `pkg/adapter/slotsession.go:217`, reads its result, and
  logs a non-nil error as
  `slog.Warn("slot_tree_removal_failed", "slot_id", sessionID, "error", err)`, because nothing
  else records that cleanup's failure. That event and those field names are the form every site
  this deliverable records a tree-removal error at uses, and `runtime_close_failed`
  carries the identical fields for a runtime close whose error this deliverable stops
  discarding, so the records read as one
  convention rather than as one per site. That same error is what decides the
  hold: `releaseSessionSlot` closes no runtime, so the tree removal is the whole cleanup, and
  it takes the release on `treeErr == nil` and leaves the identifier held otherwise; CODE-14 adds
  the `guarded` conjunct.
  `pkg/adapter/slotsession.go` gains
  a `log/slog` import for it, matching the structured adapter events `pkg/adapter/podscrub.go`
  already emits. The function still returns nothing and every caller still returns its own
  error, so no control flow changes.
- **The tree-removal seam.** Every site whose hold release reads a tree-removal result calls
  `s.removeSlotTreeVia(st)` rather than `removeSlotTree(st)` directly: `releaseSessionSlot` and
  `terminateHeldSession` above, and `Shutdown` once CODE-1 routes it through `reclaimSlotLocked`.
  `removeSlotTreeVia` is a new method on `Server` in `pkg/adapter/slot.go` that returns
  `removeSlotTree(st)` unless the new nil-defaulted unexported field
  `Server.removeSlotTreeFn func(*slotState) error`, declared in `pkg/adapter/server.go` beside
  `scrubDone`, is set, in which case it returns that function's result. `removeSlotTree` keeps
  its signature (`pkg/adapter/slot.go:210-212`). The field exists because `removeSlotTree` calls
  `slotlayout.RemoveTree` directly, and `os.RemoveAll` cannot be made to fail portably from a
  unit test: it answers nil for an absent path (`pkg/adapter/slotlayout/tree.go:54-55`), so the
  file-where-a-directory-belongs trick that drives the create arm's `EnsureTree` failure does not
  reach it, and a permission refusal turns on the UID the suite runs as, which is the reason
  `pkg/adapter/warmlayout_test.go:162-167` records for injecting the chmod result instead. The
  retained-hold arm of each site's release therefore has no other way to be driven. The package
  already carries this form for a test-only seam in `Server.scrubDone`
  (`pkg/adapter/server.go:197-201`) and in the `HoldAfterFunc` and `ExpiryAfterFunc` hooks
  (`pkg/adapter/holdstate.go:63`, `pkg/adapter/credexpiry.go:49`). Nothing in production assigns
  the field and there is no setter, so a deployed adapter always takes the `removeSlotTree` path.
  A site left calling `removeSlotTree` directly makes the seam unreachable at that site, and the
  tier-1 tree-failure case below observes the hold released and fails at its assertion.
- `errSlotReclaimInProgress`, a `codes.Aborted` status carrying `slot_reclaim_in_progress`, and
  `isSlotReclaimInProgress(err) bool`. Neither symbol exists in the tree today; the reclaim hold
  is new behaviour this deliverable introduces rather than shipped behaviour it records.
- `errSlotBindAttemptSuperseded(slotID string) error`, a `codes.Aborted` status whose detail is
  an `adapterv1.Error` with `code: ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED`, `category:
  TRANSIENT`, `Retryable: true`, and a message naming the slot identifier and nothing else.
  `Retryable` is set explicitly, as the one shipped adapter-side `adapterv1.Error` sets it
  (`pkg/adapter/staging.go:209-214`), because the proto3 default of false contradicts the
  declared category on the wire. The token itself is
  never put in the message: it is a capability over a live session's teardown, and
  `code-best-practices.md` forbids logging or returning credential-like material.
- `errSlotBindAlreadyStarted(slotID string) error`, a `codes.FailedPrecondition` status whose
  detail is an `adapterv1.Error` with `code: ERROR_CODE_SLOT_BIND_ALREADY_STARTED`, `category:
  PERMANENT`. Both are built with `status.New(...).WithDetails(...)`, and both fall back to the
  bare status when `WithDetails` errors, so a marshalling failure degrades to an untyped refusal
  with the right code rather than to no refusal at all.
- `slotResolveError(err) error` and `slotResolveCategory(err) tracing.ErrorCategory`.
  `slotResolveError` returns any of the three sentinels unchanged and wraps everything else as
  `codes.InvalidArgument` exactly as today. `slotResolveCategory` answers `CategoryTransient`
  for `errSlotReclaimInProgress` and the superseded refusal, and `CategoryPermanent` for the
  already-started refusal and for every other error. Routing the two new refusals through this
  helper is what keeps them from being re-wrapped as `codes.InvalidArgument` by the five resolve
  sites, which would make a transient race a permanently dead session.
- `validateBindFields(bindAttempt string, midSession bool) error` in `pkg/adapter/slot.go`. It
  returns a `codes.InvalidArgument` status naming which pairing the request violated when
  `(bindAttempt == "") != midSession`, and nil otherwise. It reads no registry state and takes
  no lock, because well-formedness is a property of the request alone, which is why it can sit
  above the indivisible resolve-create-stamp-compare step without weakening it. It is returned
  unwrapped and does not route through `slotResolveError`: the code is already the right one,
  and wrapping would present the refusal as a slot-resolve failure. Its call sites are the
  handlers whose messages carry the fields, each calling it at the handler's entry before any
  resolve: `PrepareWorkspace` (on the frame that resolves the slot identifier, beside the
  first-frame latch of `bind_attempt` and `mid_session`), `FinalizeWorkspace`, `RunSetup`,
  `AssignCredentials` and `Resume`. `StartSession` and `ConfigureWorkspace` do not call it,
  because their messages carry neither field, and `Shutdown` does not call it either, because
  its `bind_attempt` pairs with `unconditional_teardown` rather than with a mid-session marker
  and is checked by the two-field rule in the `Shutdown` handler.

**`ensureSlotStateLocked` takes the resolve and evaluates the cascade §4.7.1 names**, in that
section's order and under its names, so the function is the whole of the cascade and no handler
carries a second copy of it. The hold refusal stays first, before the map lookup, because a held
identifier has no entry to return:

```go
// ensureSlotStateLocked resolves the slot's registry entry or creates it, and
// admits or refuses the caller in the same indivisible step. Callers hold s.mu
// for the whole of it: the resolve, the create, the stamp and the comparison
// are one step under the lock that guards the registry, which is what makes
// first-writer-wins implementable. The attempt token is written on the create
// branch and nowhere else, in either direction, so the first attempt to create
// an entry owns it until the entry is removed.
//
// This function is the adapter's only resolve-or-create step. Its three
// production callers, ensureSlotPaths, assignCredentialsSlot and
// claimSessionSlotUnderLock, cover the seven requests §4.7.1's admission rules
// govern, so the predicate here is the whole of the admission rules that read
// the registry and no handler carries a second copy of them. Well-formedness of
// bind_attempt against mid_session is a property of the request alone, checked
// by validateBindFields at each handler that carries the fields, before the
// resolve, so a malformed request never reaches this function.
//
// spec: §4.7.1 (role and gateway RPC contract), rules 2 through 7
func (s *Server) ensureSlotStateLocked(slotID string, r slotResolve) (*slotState, error) {
    // The reclaim hold, applied before the map lookup: a held identifier has no
    // entry to return.
    if _, held := s.reclaiming[slotID]; held {
        return nil, errSlotReclaimInProgress
    }
    st, ok := s.slots[slotID]
    switch {
    case !ok && !r.allowCreate:
        // The mid-session-create rule. A mid-session request resolves an entry
        // that already exists
        // and never creates one. Creating here would mint an entry carrying no
        // token, which no attempt could ever reclaim and no sweep collects.
        return nil, status.Errorf(codes.FailedPrecondition,
            "slot %s has no registry entry for a mid-session request", slotID)
    case !ok:
        // The create-and-stamp rule. The create branch; §4.7.1's stamp-once
        // rule is what makes it the only writer of bindAttempt. The shipped create body stands whole and the stamp is
        // appended to it, so the resolve descriptor adds a predicate ahead of
        // the create rather than replacing the create.
        if s.slots == nil {
            s.slots = map[string]*slotState{}
        }
        paths, err := s.resolveSlotPaths(slotID)
        if err != nil {
            return nil, err
        }
        if err := slotlayout.EnsureTree(paths); err != nil {
            return nil, err
        }
        st = &slotState{
            paths:  paths,
            creds:  map[string]*adapterv1.CredentialLease{},
            timers: map[string]*expiryTimer{},
        }
        st.bindAttempt = r.bindAttempt
        s.slots[slotID] = st
        return st, nil
    case r.bindAttempt != "" && st.bindAttempt != "" && st.bindAttempt != r.bindAttempt:
        // The attempt identity rule.
        return nil, errSlotBindAttemptSuperseded(slotID)
    case st.started && !r.allowStarted:
        // The started-session rule, the phase gate.
        return nil, errSlotBindAlreadyStarted(slotID)
    default:
        // The admit rule. The entry is returned and the token is not written,
        // which
        // covers both "the entry carries no token" and "the caller asserts no
        // identity".
        return st, nil
    }
}
```

The create arm keeps the error returns the shipped create branch has, and they are the only
errors this function returns that are not refusals: `resolveSlotPaths` runs
`slotlayout.ValidateSlotID` (`pkg/adapter/slotlayout/slotlayout.go:115-131`, called by
`Resolve` at `:136-139`) and so refuses a
malformed slot identifier before any entry is inserted, and `slotlayout.EnsureTree` returns the
failure to create the slot's directories, after which no entry is inserted either. It also keeps
the lazy seed of `s.slots`, because `New` never initialises that map
(`pkg/adapter/server.go:367,:376`) and the package's own tests build a `Server` by struct literal.

`ensureSlotPaths` takes the same parameter and passes it through:

```go
func (s *Server) ensureSlotPaths(slotID string, r slotResolve) (slotlayout.SlotPaths, error)
```

**Production call sites that must thread the parameter.** Ten, and each is named with the
resolve it passes:

| Site | `bindAttempt` | `allowCreate` | `allowStarted` |
|:--|:--|:--|:--|
| `staging.go` `resolvePrepareStagingDir`, from `PrepareWorkspace` | the first frame's `bind_attempt` | `!midSession` | `midSession`, both from the first frame's latch |
| `staging.go` `FinalizeWorkspace`'s `ensureSlotPaths` | `req.GetBindAttempt()` | `!req.GetMidSession()` | `req.GetMidSession()` |
| `staging.go` `RunSetup`'s `ensureSlotPaths` | `req.GetBindAttempt()` | true | false |
| `slotcreds.go` `assignCredentialsSlot`, from `AssignCredentials` | `req.GetBindAttempt()` | true | false |
| `slotsession.go` `claimSessionSlotUnderLock`, from `StartSession` | empty | true | false |
| `slotsession.go` `claimSessionSlotUnderLock`, from `Resume` | `req.GetBindAttempt()` | true | false |
| `slotsession.go` `claimSessionSlotUnderLock`, from `ConfigureWorkspace` (`sdkwarm.go:217`) | empty | true | `idempotentRepeat` |
| `slot.go` `ensureSlotPaths` | pass-through | pass-through | pass-through |
| `slot.go` `ensureSlotStateLocked` | the parameter itself | | |
| `credentials.go:74` `AssignCredentials`'s call into `assignCredentialsSlot` | `req.GetBindAttempt()` | true | false |

`claimSessionSlot` and `claimSessionSlotUnderLock` grow the parameter rather than three
booleans, because all three of their callers differ on all three fields. Their existing
`sdkWarm` and `idempotentRepeat` parameters stay: `sdkWarm` gates the pod-idle scan above the
resolve, and `idempotentRepeat` is what `ConfigureWorkspace` sets `allowStarted` from, so the
two are related rather than duplicated and the call site sets both from one value.

The test call sites across the package thread the same parameter. They are not
enumerated here, because every one of them is a mechanical widening to
`slotResolve{allowCreate: true}` and the tier-1 work below names the files.

**`claimSessionSlotUnderLock`'s own refusal becomes typed.** Its shipped
`status.Errorf(codes.Unavailable, "session %s has already started on this pod")` at
`slotsession.go:84-86` is the phase gate stated in one handler's terms, and rule 6 (**the
started-session rule**) now states it for every RPC that resolves an entry. The `!idempotentRepeat` arm therefore returns
`errSlotBindAlreadyStarted(slotID)` rather than the untyped `Unavailable`, and the
`idempotentRepeat` arm is unchanged: it still returns `(false, false, nil, nil)` and the caller
still treats it as a satisfied claim. In practice the started-session rule fires first, inside
the resolve, so the arm is reached only when `allowStarted` was true and the entry started
between the resolve and the arm, which the lock forbids; it is kept as the local statement of
the same rule so a later reader of the claim does not conclude that a started entry is admitted
there.

**The mid-session-create rule needs `mid_session` read before the resolve.** `FinalizeWorkspace`
reads `req.GetMidSession()` at `pkg/adapter/staging.go:239` today, fifty-eight lines after its
`ensureSlotPaths` call at `:181`. The read moves to the top of the handler and the value is
passed into the resolve; the branch at `:239` reads the same local. `RunSetup` and
`AssignCredentials` are never mid-session and pass `allowCreate` true and `allowStarted` false.

**The first-frame latch on `PrepareWorkspace`.** `PrepareWorkspace` is a client-streaming RPC
that resolves lazily on the first frame carrying a session identifier
(`resolvePrepareStagingDir`, `staging.go:78`), and the shipped handler already resolves at most
once per call: the `if stagingDir == ""` guard at `staging.go:77` runs the resolve on the first
frame alone, and no later frame's `session_id` is compared against it. The handler therefore
latches `bind_attempt` and `mid_session` from that frame, evaluates the cascade on the latched
values, and reads neither field again for the rest of the call. It refuses no later frame on
either field. With the resolve taken once, a later frame's assertion cannot take effect over
bytes the resolving frame's assertion admitted, so a refusal there would add a handler
comparison, a wire clause and two conformance cases that protect nothing, while the larger
shipped hole on that stream, an unchecked later-frame `session_id`, would stay open. That hole
is out of scope here. This implements rule 9 (**the first-frame rule**).

**The five resolve sites keep the shared helper.** All five re-wrap any slot-resolve failure as
`codes.InvalidArgument` today, which `SlotBindError.Reason()` maps to
`SlotReasonWorkspaceValidation` and `NonRetryable()` reports true for. Routing the three
sentinels through `slotResolveError` is what keeps rule 5's refusal transient and rule 6's
refusal correctly permanent, on the categories rule 5 and rule 6 state:

| Site | Wrap | Span category stamped at |
|:--|:--|:--|
| `pkg/adapter/staging.go` `resolvePrepareStagingDir` | `InvalidArgument` | the caller stamps `CategoryPermanent` |
| `pkg/adapter/staging.go` `FinalizeWorkspace`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/staging.go` `RunSetup`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/slotsession.go` `claimSessionSlotUnderLock`'s `ensureSlotStateLocked` | `InvalidArgument` | none |
| `pkg/adapter/slotcreds.go` `assignCredentialsSlot`'s `ensureSlotStateLocked` | `InvalidArgument` | none |

No test asserts any of the five message strings, so the helper may reword none of them and the
wrap text stands as it is for every non-sentinel error.

**The reclaim hold has one predicate and two test points.** The predicate is the one §5.2 states,
and the sites that test it are `acquireSlotGuardForResolve` and `ensureSlotStateLocked`. A caller
is refused where it would otherwise wait, and refused again where it would otherwise be admitted,
and neither test point makes the other redundant. A guarded admission RPC that met the hold only
inside `ensureSlotStateLocked` would first block on the guard for the whole destructive section
and would then resolve after the hold had already been released, so the transient refusal §5.2
promises for `PrepareWorkspace`, `FinalizeWorkspace` and `RunSetup` would arrive as a permanent
`FailedPrecondition` or not at all. The test inside `ensureSlotStateLocked` stays because
§10.1.4's first pass opens a hold for every member while holding `s.mu` and takes no guard
(`pkg/adapter/holdstate.go:189-205`), so a caller can acquire a guard cleanly and only then find a
hold open, and because `AssignCredentials`, `StartSession` and `ConfigureWorkspace` take no guard
at all and meet the hold there alone. A caller that passes the hold test and then waits on the
guard waits for the remainder of the section it is being excluded from, which is the exclusion the
guard exists to provide.

A refusal either test point raises is the same sentinel and is returned through
`slotResolveError`, which passes it through unchanged, with the span category stamped by
`slotResolveCategory` at the site that stamps it for a refusal raised inside the resolve. A
guard-acquisition refusal is therefore indistinguishable to the caller from one the resolve
raised, which is what lets §4.7.1 state one hold rule rather than two.

### CODE-14 · pkg/adapter/bindattempt.go, pkg/adapter/server.go, pkg/adapter/staging.go, pkg/adapter/resume.go, pkg/adapter/slotsession.go, pkg/adapter/session.go, pkg/adapter/sdkwarm.go, pkg/adapter/holdstate.go · the per-slot guard

Targets: `lockSlotGuard` and `acquireSlotGuardForResolve` in `pkg/adapter/bindattempt.go`, and
`Server.slotGuards map[string]chan struct{}`, guarded by `s.mu`, on `Server` in
`pkg/adapter/server.go`, with every acquisition site the derivation table below names.

**The per-slot guard over every section that writes or destroys the slot's tree outside `s.mu`.** With both gates inside
the resolve's critical section the gate is a fence for the resolve itself. What remains unlocked
is the work that runs against paths handed out earlier: `ensureSlotPaths` takes `s.mu`, resolves,
releases it, and returns a `SlotPaths` value (`pkg/adapter/slot.go:139-147`), and
`checkpointRootsForSession` does the same for the resume path, copying the entry's `current` and
`sessions` roots under `s.mu` and returning them with the lock released
(`pkg/adapter/slot.go:183-203`), so two calls holding the same paths can interleave their work
against one tree. A retry of an attempt's own RPC reaches it, and so does a matching `Shutdown`
racing a `FinalizeWorkspace`. The reclaim hold does not reach that second case. The hold is
inserted by `reclaimSlotLocked` inside the reclaiming handler's own critical section, and a
`FinalizeWorkspace` that already returned from `ensureSlotPaths` and is inside
`workspace.MaterializeWithPolicy` is not being admitted and re-enters no resolve.

`Server.slotGuards map[string]chan struct{}` is the remedy. Each entry is a capacity-one channel
used as a semaphore: an acquisition sends into it and the release receives from it, which is what
lets an acquisition wait on the caller's context rather than without bound. **The acquisition
step.** An acquisition first attempts the send without waiting, and it holds the guard whenever
the channel is empty, whatever state the caller's context is in. Only when the channel is full does
it select the send against `ctx.Done()`, so an acquisition expires only when the guard is contended
past the caller's context. The non-blocking attempt comes first because a `select` whose cases are
both ready chooses between them at random, and the `StartSession` and SDK-warm rollback sites call
the guard-acquiring `releaseSessionSlot` on the very context whose expiry failed `Runtime.Start`
(`pkg/adapter/session.go:156-157`). The guard is handed out in two forms, both declared in `pkg/adapter/bindattempt.go`, and what separates them is whether the caller
is subject to the reclaim hold:

- `lockSlotGuard(ctx context.Context, slotID string) (func(), bool)` takes `s.mu`, reads or creates
  the slot's channel, releases `s.mu`, and acquires it by the acquisition step above. On a send it
  returns the release and `true`; when the acquisition outlives `ctx` it sends nothing and returns
  a no-op release and `false`. It never refuses. The destructive sections take this form, because
  a reclaim may not be refused by a hold: a `Shutdown` naming a session whose cleanup is
  running is answered under rule 11 (**the no-entry rule**), and neither
  `releaseSessionSlot` nor the §10.1.4 `terminateHeldSession` may be refused by the hold its own
  pass opened.
- `acquireSlotGuardForResolve(ctx context.Context, slotID string) (func(), error)` takes `s.mu`; when
  `s.reclaiming[slotID]` is set it releases `s.mu` and returns `errSlotReclaimInProgress` with no
  guard and no wait; otherwise it reads or creates the slot's channel, releases `s.mu`, and acquires
  it by the acquisition step above, returning the release on a send and `ctx.Err()` when the
  acquisition outlives `ctx`. Every admission RPC that takes a guard takes this form, at the
  point that RPC runs `validateBindFields` and before its resolve, which for `PrepareWorkspace`
  is the frame that resolves the slot identifier and for the rest is the handler's entry, so the
  order §4.7.1 publishes, rule 1 then rule 2 then rules 3 through 7, is the order the code
  runs.

**No guard acquisition outlives its caller's context.** Both forms take the caller's context and
neither waits past it, so no section waits on this guard longer than the work it belongs to is
itself allowed to run. This rule states nothing about any caller's budget; each caller's budget
is stated where that caller is described.
What a destructive section does when its acquisition expires is stated once, under **Disposition of an expired acquisition at a removing
site** below the derivation table. An admission RPC whose acquisition expires is refused with the
context's own error, because proceeding unguarded is how a materialization re-creates the tree a
reclaim has just removed, which is the race the guard exists to close. Neither clause reaches the
wire: the destructive side is observed through the `slot_guard_not_acquired` warning that
disposition states, and the admission side through the handler's own context error, which the
gateway already classifies.

*Scope.* The guard's scope is a predicate rather than a list of RPCs: **a section that creates,
writes or destroys the slot's tree outside `s.mu` acquires that slot's guard before the resolve
that hands out its paths, and holds it until that path work has ended; a section whose only path
work runs inside `s.mu`, and a section that only reads the tree, acquires none.** A section that destroys the slot acquires the guard before its
destructive work, and `Shutdown` acquires it on the arm that destroys alone, because the arms that
remove nothing touch no path. The table below derives that predicate against the tree rather than
stating a second rule, and it is the one statement of which sections are guarded, so a later
section that grows unlocked path work is caught by reading the predicate against its own code:

| Section | Path work outside `s.mu` | Guard |
|:--|:--|:--|
| `PrepareWorkspace` | staging writes across frames, after `resolvePrepareStagingDir` (`staging.go:78`) | from the frame that resolves the slot identifier to the end of the call, once per call rather than once per frame, because the staging directory is resolved once and the tree can be removed between two frames |
| `FinalizeWorkspace` | `workspace.MaterializeWithPolicy` or `MaterializeOverlayWithPolicy` after `ensureSlotPaths` (`staging.go:181,:246-249`) | across the resolve and the materialization |
| `RunSetup` | `workspace.RunSetup` after `ensureSlotPaths` (`staging.go:337,:353`) | across the resolve and the setup commands |
| `Resume` | `workspace.ExtractTree` after `checkpointRootsForSession` hands out the roots and releases `s.mu` (`slot.go:183-203`, `resume.go:169,:206`) | from ahead of its `claimSessionSlot` to the end of the call |
| `Checkpoint` | reads alone, after `checkpointRootsForSession` hands out the roots and releases `s.mu` (`slot.go:183-206`): `probeWorkspaceBytes` and `workspace.ArchiveTree` (`checkpoint.go:140,:205`) | none: the read creates nothing and destroys nothing, so a reclaim running beside it removes a tree the checkpoint is reading rather than corrupting one it is writing, and the stream fails with the error the archive raises, which is the disposition a checkpoint of a session being torn down already carries |
| `AssignCredentials` | none: `writeSlotCredentialFile` runs inside `s.mu` (`slotcreds.go:24-44`) | none |
| `StartSession` | none against the slot tree | none |
| `ConfigureWorkspace` | none against the slot tree: `writeSessionManifest` writes under `ManifestDir` (`manifest.go:280-283`, `server.go:111-114`), which `slotlayout.RemoveTree` does not touch (`slotlayout/tree.go:58-69`) | none |
| `releaseSessionSlot` and the §10.1.4 `terminateHeldSession` | `removeSlotTree` | acquired ahead of `s.mu` and held across the destructive section |
| `Shutdown` | `Runtime.Close` and `removeSlotTree`, on the removing arm | on the removing arm alone; the arms answering `absent` and `superseded` acquire none |
| §10.1.4 pass 1, `deregisterStartedSessions` | none | none: it holds `s.mu` across every member and does no path work, and each member's guard is taken by the pass-2 call that destroys that member |

**`terminateHeldSession`'s guard and close context.** `terminateHeldSession` defers the guard's
release separately from the reclaim-hold release CODE-6 gives it. `onHoldTimeout`'s single
ten-second context becomes the pass's guard-acquisition deadline alone, and `terminateHeldSession`
mints each member's own ten-second close context after its acquisition returns; `emitFinalUsage`,
`Runtime.Close` and every later call run on it, so no member's close is spent on another member's
guard wait. The comment above the shared context in `onHoldTimeout` is rewritten to state the split
and keeps its observation that a non-last close on a shared runtime process returns without
touching the child.

**Disposition of an expired acquisition at a removing site.** The three removing sites in the
table take the raw `lockSlotGuard` form on their caller's context, and the removal on an expired
acquisition is stated here; CODE-1, CODE-13, CODE-15, the Testing section and the accepted failure modes
cite this statement rather than restating it. The removal runs unguarded: the site logs the
`slot_guard_not_acquired` warning naming the slot identifier and the caller and then performs
every act it would have performed under the guard, because that is what the shipped code does
today and abandoning the removal would leave a worse residue than an unordered one. An expired
acquisition is the removal performed before every request still writing under the identifier has
stopped writing that SPEC-3's `**Slot-identifier reclaim hold.**` paragraph names, so the §5.2
disposition table's rows keyed on a failed act give its reclaim hold and its report, and
`guarded`, the boolean `lockSlotGuard` returns, is the conjunct each site's own `completed`
predicate carries for it. Per site:

| Removing site | Removal on an expired acquisition |
|:--|:--|
| `Shutdown`'s removing arm | the deregistration, the runtime close and `removeSlotTreeVia` run unguarded, on the request's own context |
| `releaseSessionSlot`, guard-acquiring form | the deregistration and `removeSlotTreeVia` run unguarded |
| `terminateHeldSession` | the final usage report, the runtime close and `removeSlotTreeVia` run unguarded, on the member's own close context |

Releasing the hold instead was rejected: an unguarded removal can run beside a `Resume` whose
`workspace.ExtractTree` is still writing under the identifier, so a released identifier admits a
successor onto a tree the extraction is re-creating, on the pod §5.2 placement prefers for the
retry, which is the residue the hold was staged to close. The cost of retention is the
failed-removal row's cost, an identifier withheld for the pod's remaining life, and on the
compensation path it adds nothing observable: the acquisition expires only when the
compensation's own deadline has, so the gateway has already recorded the RPC error and the
`leaked` disposition before the answer is built.

`Resume`'s guard covers a network-bound extraction, so the compensating `Shutdown` CODE-13 sends on
a failed `Binder.Resume` waits on that guard while the handler is still running, and takes the
disposition stated above when that wait outlasts the compensation's context. Every `releaseSessionSlot` call on `Resume`'s own
rollback paths (`resume.go:69`, `:73`, `:89`, `:107`, `:126`, `:134`, `:141`) is inside that
guard, and a second acquisition of the same guard blocks on the capacity-one channel rather
than re-entering it, so `releaseSessionSlot` splits in two:
`releaseSessionSlotUnderGuard(sessionID)` carries the body this deliverable gives it, and
`releaseSessionSlot(ctx context.Context, sessionID string)` acquires the raw guard on the caller's
context and calls it. `Resume` calls the under-guard form at each of those sites. The calls in
`session.go` (`:133`, `:147`, `:157`) and in `sdkwarm.go` (`:236`, `:241`, `:251`, `:298`) keep
calling `releaseSessionSlot`, because `StartSession` and `ConfigureWorkspace` hold no guard, and
each of those call sites is inside a handler that already has its request context in scope, so
threading the parameter is mechanical. Three callers outside the handlers have no request context
to thread: `pkg/adapter/podmcp_arming_internal_test.go:144` and `:195` pass `t.Context()`, and the
exported test seam `Server.ReleaseSlotForTest` (`pkg/adapter/export_test.go:32-35`) gains a
`context.Context` first parameter and passes it through, which its callers
(`integrationlevel_test.go:84`, `credexpiry_test.go:320`, `checkpoint_stream_test.go:1012`,
`tracingcontext_addressing_test.go:425` and `one_session_only_test.go:77`) supply as `t.Context()`.
Those five are every call site the tree holds today. The start-versus-reclaim rollback case CODE-1
and CODE-2 add to `pkg/adapter/slotsession_test.go` calls the seam through the new signature as
well, and it is the one caller that does not supply `t.Context()` uniformly: its `Resume` row
supplies an already-cancelled context, for the reason that case states.
When the guard-acquiring form's acquisition does not complete it takes the disposition stated
above, in its own row.

*Lock order.* Two locks with one order between them: `s.mu` is never held at the moment a slot
guard is acquired, which both hand-out helpers' own order guarantees, and no path holds two slot
guards at once. Neither helper's acquisition outlives its caller's context, so the order between
the two locks carries no unbounded wait with it. A section that acquires a guard after releasing `s.mu` re-reads the state its
decision depends on, because that state can change while the guard is being acquired; CODE-1's
`Shutdown` is the section that does this, and it re-evaluates its whole comparison. Bracketing the
resolve is what the race needs: a guard taken only around the destructive work still admits the
ordering in which the reclaim removes the tree first and a materialization admitted earlier then
re-creates it, leaving an on-disk workspace and credential directory behind with no registry
entry.

*Lifetime.* A `slotGuards` entry is created on the first reference to a slot identifier and is
never removed for the life of the pod. `reclaimSlotLocked`'s `release` ends the reclaim hold and
touches this table not at all. Deleting an entry cannot block a goroutine that holds the guard it
named: the holder keeps the old channel, and the next acquirer finds no entry, mints a second
channel and sends into it immediately, so mutual exclusion would be gone at the moment it is
needed. The path that reaches it is the ordinary one, because `SlotID == SessionID` and a
reclaim whose cleanup completed releases the identifier, so a §5.2 retry re-binds the same
session onto the same pod under the same identifier. The table's bound is one channel per distinct session
the pod has served, and `recycle.maxSessionsPerPod` retires the pod at that count.

### CODE-7 · pkg/gateway/runtime/adapterclient · the gRPC status detail becomes a Go typed error

Nothing in the gateway can match the two new refusals without this deliverable, which is why it
precedes CODE-8 rather than accompanying it. Two facts fix its design.
`adapterv1.Error` declares `code`, `category`, `message`, `retryable` and `docs_url` and has no
reason field, so the distinction between the two refusals has to be carried by two `ErrorCode`
values rather than by a string. And every gateway consumer of a bind failure matches on Go
types: `isTransientPodClaimError` (`start.go:3648-3681`) and both
reclaim closures. `status.Convert` appears nowhere in `pkg/gateway`, and the only status-detail
reader in the whole gateway is `client.go:333-341`, which recovers `RunSetup` partial outputs.

```go
// ErrSlotBindAttemptSuperseded is returned when the adapter refused a
// bind-sequence RPC because the pod's slot registry entry for the session
// carries a non-empty bind attempt token different from this attempt's.
// Another attempt owns the entry and everything under it.
//
// spec: §4.7.1 (role and gateway RPC contract)
var ErrSlotBindAttemptSuperseded = errors.New("adapterclient: slot bind attempt superseded")

// ErrSlotBindAlreadyStarted is returned when the adapter refused a
// bind-sequence RPC because the resolved entry's session has already started
// on the pod and the request is not a §7.4 mid-session upload.
//
// spec: §4.7.1 (role and gateway RPC contract)
var ErrSlotBindAlreadyStarted = errors.New("adapterclient: slot bind already started")
```

The translation is one unexported helper every RPC method's error path runs, following the
`RunSetup` reader's pattern:

```go
// translateSlotBindRefusal recovers the two §4.7.1 slot-bind refusals from the
// gRPC status detail and returns them as sentinels the gateway can match with
// errors.Is, wrapping the original status so the code and the message survive.
// Any other error is returned unchanged. The only detail reader in this package
// before this one is RunSetup's partial-output recovery, and this follows its
// form: status.FromError, then a type switch over st.Details().
//
// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
// specification)
func translateSlotBindRefusal(err error) error {
    if err == nil {
        return nil
    }
    st, ok := status.FromError(err)
    if !ok {
        return err
    }
    for _, d := range st.Details() {
        e, isErr := d.(*adapterv1.Error)
        if !isErr {
            continue
        }
        switch e.GetCode() {
        case adapterv1.Error_ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED:
            return fmt.Errorf("%w: %w", ErrSlotBindAttemptSuperseded, err)
        case adapterv1.Error_ERROR_CODE_SLOT_BIND_ALREADY_STARTED:
            return fmt.Errorf("%w: %w", ErrSlotBindAlreadyStarted, err)
        }
    }
    return err
}
```

The double-`%w` wrap is deliberate. `errors.Is(err, ErrSlotBindAttemptSuperseded)` matches the
sentinel and `status.Code(err)` still walks to the original status, so CODE-5's single
`codes.Aborted` arm and CODE-4's sentinel match both fire on the same value and neither needs
the other's form.

Call sites: the error path of `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`,
`AssignCredentials`, `Resume`, `StartSession` and `ConfigureWorkspace` on `Client`. `Shutdown`,
`ShutdownReclaim` and `ShutdownRecycle` do not translate, because the adapter answers the
refusals on those as outcomes rather than as errors.

`Client` also gains the request-side halves the mechanism needs: `bind_attempt` on the six
bind-sequence requests, `mid_session` on `PrepareWorkspace`, `unconditional_teardown = true`
set inside `Shutdown` and `ShutdownRecycle`, and the new `ShutdownReclaim`:

```go
// ShutdownReclaim sends the fenced form of §4.7's Shutdown, naming the bind
// attempt whose entry it is reclaiming. It is the only caller of the fenced
// form; every other teardown goes through Shutdown or ShutdownRecycle, which
// set unconditional_teardown themselves so no caller can forget it.
//
// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)
func (c *Client) ShutdownReclaim(
    ctx context.Context, sessionID, reason string,
    deadline time.Duration, bindAttempt string,
) (adapterv1.SlotReclaimOutcome, bool, error)
```

Its returns are the outcome, `exited_cleanly` and the error. The value the compensation names
is the token the attempt already holds, so nothing is latched off a response and the client
needs no per-connection state: `pkg/gateway/runtime/adapterclient` holds none today, and this
deliverable introduces none.

### CODE-8 · pkg/gateway/podlifecycle/podsession/binder.go · the reclaim closures return a typed refusal without draining the pod

`failPhase` (`binder.go:1072-1082`) gates `releaseCredentials` on `leaseAssigned` and calls
`b.drain` outside that block, so it drains on every call. `Binder.Launch`'s reclaim closure
passes the literal `true` (`:998`) and `Binder.Prepare`'s passes a mutable flag (`:867`,
declared `:865`, set true at `:954`). Either way the pod is drained.

A refusal on either new code means the opposite of a failure: this pod is serving the session
correctly, under another attempt's identity or with the session already started. Draining it
retires a healthy pod and, on the `Prepare` path, retires the winner's pod on behalf of the
loser of two concurrent `/finalize` calls (`start.go:2634`, `finalize.go:280`). This deliverable
is therefore a hard blocker of CODE-6's gates rather than an independent cleanup: without it
the first thing the gates do in production is drain live pods.

Both closures take the failing error as a parameter, and every call site passes the error it is
about to return. The closures are `func()` today (`binder.go:866-869`, `:997-1000`), and every
call site declares its own error inside its own `if` statement, so a guard written into the
existing bodies has nothing to read: in `Binder.Launch` the only error a body could close over
is the reconnect error at `:977`, which is nil on every path that reaches a `reclaim()`. The
parameter is named `cause` because `Binder.Prepare` already carries a function-scope `err`.
`Binder.Prepare`'s closure becomes:

```go
// spec: §4.7.1 (role and gateway RPC contract); §6.2 (pod state machine).
// A typed slot-bind refusal is not a pod failure, so the closure calls
// neither failPhase nor drain and the call site returns the refusal.
reclaim := func(cause error) {
    if errors.Is(cause, adapterclient.ErrSlotBindAttemptSuperseded) ||
        errors.Is(cause, adapterclient.ErrSlotBindAlreadyStarted) {
        cl.Close()
        return
    }
    b.failPhase(ctx, sb, leaseAssigned, req.SessionID)
    cl.Close()
}
```

`Binder.Launch`'s closure differs only in passing the literal `true` where `Prepare` passes
`leaseAssigned`, as it does today.

The call-site sweep covers every `reclaim()` in both functions, each of which becomes
`reclaim(err)` passing the raw adapter error before any wrap into `*SetupCommandFailure` or
`*SDKDemotionNotSupported`, since `errors.Is` reads through a wrapper only where one is present:
`binder.go:883` (`DemoteSDK`), `:918` (`stageWorkspace`), `:923` (`FinalizeWorkspace`), `:935`
(`RunSetup`) and `:950` (`assignCredentials`) in `Binder.Prepare`, and `:1010`
(`ConfigureWorkspace`), `:1021` (`StartSession`) and `:1032` (`verifyIntegrationLevel`) in
`Binder.Launch`. Each call site keeps the `return nil, <its own error>` it has today. The two
reconnect-failure early returns (`:844-857`, `:977-992`) call `ReclaimClaimed` rather than
`reclaim` and are outside the sweep.

`Binder.Prepare`'s credential-assignment failure arm also takes CODE-13's attempt-scoped lease
release, which lands in S19.
`failPhase`'s `leaseAssigned`-gated session-wide release is unchanged on every other arm.

`Binder.Launch` passes the literal `true`, and it issues
`ConfigureWorkspace` or `StartSession`, neither of which carries a token, so the only refusal it
can meet is `SLOT_BIND_ALREADY_STARTED`, which reports that a live session already owns the
entry. `failPhase`'s release is `credassign.Service.ReleaseSession`'s session-keyed walk over
`LeasesBySession` (`pkg/gateway/credentials/credassign/credassign.go:400-408`), so running it on
that arm would strip the §4.9 leases the live session authenticates with, decrement the
credential's `active` counter under it, and hide those leases from the §11.4 revoke fan-out,
which enumerates by session identifier. The ordinary-failure arm keeps the session-wide release
it has today, unchanged by this deliverable.

The urgency claim the earlier revision carried for this path is withdrawn. `EndpointStart`
admits `StateReady` alone (`pkg/api/v1/session/session.go:289`), so a `/start` retry against a
running session is refused on the precondition before `launchOnPod` runs, and the exclusive
path's drain is reachable today only in composition with a post-publish 500. The dependency
claim replaces it and is stronger: once the gates exist, both closures meet a refusal they did
not meet before.

One residue is named rather than closed: it is recorded among the accepted failure modes as
**A pod whose drain failed keeps a dead attempt's token**.

### CODE-9 · pkg/observability/metrics/catalog.go, pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go, pkg/adapter/metrics.go, docs/reference/metrics.md, pkg/gateway/podlifecycle/podsession/binder.go, pkg/gateway/podlifecycle/podsession/slotbinder.go, cmd/lenny-gateway/metricsbackfill.go, tests/tier11_docs/slot_compensation_metric_reference_test.go · the counters that make the fence observable

`Binder.noteCompensationOutcome` is the one helper the compensation's caller runs to fire the
superseded counter, so a later caller that maps the outcome without counting it is a missing
call rather than a missing branch.

The superseded series is registered in `pkg/observability/metrics/catalog.go`, and its
Prometheus collector sits with the other gateway slot series in
`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go` beside the
`lenny_slot_failure_total` collector at `gatewaymetrics_credential.go:185-193`, exported as
`Metrics.IncSlotCompensationSuperseded(outcome, cause, pool, podName string)`, the signature of
the `SlotReclaim` hook below, so the hook is assigned directly the way `IncSlotFailure`
(`gatewaymetrics.go:1170-1175`) is assigned to `SlotFailure`.

The collector reaches the binder through a hook of the same form as `SlotFailure`, because the
series carries `pool`, `k8s_pod_name` and `cause` and only the call site holds those values:

- `pkg/gateway/podlifecycle/podsession/binder.go` declares `SlotReclaim func(outcome, cause, pool,
  podName string)` beside `SlotFailure` (`binder.go:137`), with a doc comment naming the §16.1
  series. A nil field is the no-op default and the field is never cleared.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` carries the forwarder `func (b *Binder)
  noteCompensationOutcome(outcome adapterv1.SlotReclaimOutcome, cause, pool, podName string)`
  beside `recordSlotFailure` (`slotbinder.go:353-361`), which it copies. It returns without
  calling the hook when `b.SlotReclaim` is nil and when the outcome is anything other than
  `SLOT_RECLAIM_OUTCOME_SUPERSEDED`, so the branch exists once. It takes the `cause` label's
  value as a string, so this deliverable does not depend on CODE-7; CODE-13's
  `compensationCause` supplies it.
- `cmd/lenny-gateway/metricsbackfill.go` sets the hook on the line below `w.podBinder.SlotFailure
  = gwMetrics.IncSlotFailure` (`metricsbackfill.go:149`). That is the hook's only production
  wiring site, and without it the forwarder no-ops while the §16.1 row and the
  `docs/reference/metrics.md` row both describe a series the gateway never exports. The tier-1
  case below sets the hook directly and so cannot observe the omission, which is why the wiring
  site is named here and in the files-touched list.

The untokened-entry series is registered in `pkg/adapter/metrics.go` as
`slotShutdownUntokenedEntry`, a `mustCounter` with no label, as SPEC-6's §16.1 row states, and
this deliverable is the only one that declares it. It carries no pod label because every series
the adapter emits takes the pod label its scrape target attaches, and an emitted `k8s_pod_name`
would be renamed `exported_k8s_pod_name` at scrape. Its accessor is the package-level `func
incSlotShutdownUntokenedEntry()`, declared beside `incSetTracingContextDropped`
(`pkg/adapter/metrics.go`), which is the form every in-package accessor in that file already
takes; the file declares no method on `*Server`. That file is the surface the tier-11 adapter sweep reads. The series takes no `catalog.go`
row and no `spec161Metrics` entry, because
the §16.1 rows that carry the adapter scrape deferral (`spec/16_observability.md:186-189`) are
absent from both, while the adapter-emitted metrics that do carry `catalog.go` rows,
`lenny_adapter_coordinator_hold` (`pkg/adapter/metrics.go:108`, `catalog.go:271`) and
`lenny_credential_rotation_inflight_ceiling_hit_total` (`pkg/adapter/metrics.go:50`,
`catalog.go:146`), carry no deferral. SPEC-6's row for this series records the same scrape
deferral, so it follows the §16.1 rows that already carry one. `spec161Metrics` in
`pkg/observability/metrics/catalog_test.go` therefore gains `lenny_slot_compensation_superseded_total`
and `lenny_adapter_leaked_slots` alone: that list is reconciled both ways against `MetricCatalog()`
(`catalog_test.go:188-211`), so entering the adapter series there fails
`TestMetricCatalogIsCompleteAgainstSpec161` on this deliverable's own step.

Both series take a `docs/reference/metrics.md` row matching the §16.1 row SPEC-6 stages, the
untokened-entry row under that page's `## Adapter metrics` table with the same "outside the
default scrape set" phrasing its siblings there already use.

The leaked-slots gauge `lenny_adapter_leaked_slots` is already registered and set by the gateway
(the `adapterLeakedSlots` collector in `gatewaymetrics_credential.go`), and this deliverable
changes no code that emits it. Because SPEC-6 gives it a §16.1 row, it takes a `catalog.go` entry
of type `TypeGauge` beside `lenny_slot_failure_total` and an entry in `spec161Metrics`, whose
reconciliation is two-way, and a `docs/reference/metrics.md` row beside the
`lenny_slot_failure_total` row naming the `pod_id` and `pool` labels and the SPEC-6 meaning. It
belongs in the gateway rows rather than under `## Adapter metrics`, because the gateway emits it.

| Counter | Fires when | Expected value |
|:--|:--|:--|
| `lenny_slot_compensation_superseded_total` | a compensation answered `superseded`, meaning the adapter held an entry the compensation was not addressed to, labeled by `cause` as SPEC-6's §16.1 row states | no expected rate; the series records the outcome, whose meaning SPEC-6's §16.1 row states |
| `lenny_slot_shutdown_untokened_entry_total` | the adapter met an entry carrying no token, incremented from CODE-1's fail-closed arm | zero on the bind paths, and non-zero when an abandoned attempt's late `StartSession` left an untokened entry behind, which SPEC-5 records as a residue |

The superseded series is gateway-side. The untokened-entry series is adapter-side, and the
adapter process emits nothing scrapeable today, so its catalog row carries the same deferral the
adapter metrics endpoint already owns; it is recorded in the register rather than wired to a
scrape target here. `slotFailureFinalize` does not exist: both `stageWorkspace` and
`FinalizeWorkspace` record `slotFailureWorkspacePrep` (`slotbinder.go:287,294`), so a
measurement separating them needs a new stage constant, which this deliverable adds as
`slotFailureWorkspaceFinalize`.

### Comment-carrier reduction: shared invariants

Each statement the spec lane retires has carriers outside `spec/`. Those in `docs/`, the proto
and the generated stubs are dispositioned by the deliverables the spec-lane carrier tables
name. The Go, SQL and test comment carriers are dispositioned by the three sub-blocks below
(CODE-10, CODE-11 and CODE-12), one per retired statement, each carrying only its carrier
definition, its grep command or site list, and its arm rule. Everything the three share is
stated here, once.

**Invariants.** Every arm of every sub-block holds these.

- No `// spec:` annotation loses a section number.
- No assertion moves, and no sub-block creates a test case or adds an assertion.
- A tier-11 file a sub-block touches keeps every substring and every check it holds today.
- Neither `tests/spec-map.json` nor `tests/claim-map.json` takes an edit.
- No pointer to a spec section is added to a comment that carries none, and no rationale
  sentence is added. Where an arm replaces a restatement with a citation, the citation stands
  in the span the restatement occupied.
- A landed migration's comment is edited in place and no new migration is written, because
  `migrations/` carries no checksum gate and a comment-only edit changes no applied DDL.
- The two claim-map line surfaces into `pkg/controller/sandbox/podspec/podspec.go` and
  `pkg/adapter/gatewaylink.go` are re-checked after any reflow of those comments.

**Non-carrier arm.** A hit that neither states the retired proposition nor restates it in
other words stays true, is not a carrier, and takes no edit. A hit that another deliverable
already dispositions (for SPEC-3, the proto comments and their generated copies under
SCHEMA-1, `pkg/adapter/server.go`'s field comment under CODE-15, and the two tier-11 files under
the tier-11 `**For SPEC-3**` sweep) is dispositioned there. Only a hit that states the retired proposition
and fits no arm of its sub-block is recorded in `deviations.md` rather than left in place.

**Closure.** A sub-block's step is done when every remaining hit of its command, or every site
on its list, is either edited under its arm rule or admitted by the non-carrier arm. A carrier
found later is closed by re-running the command and takes no row anywhere in this proposal.
A file a sub-block alone opens is opened for comment prose only, so it counts toward no impact
row's file-collision ground in the summary; a file another entry of the files-touched list
also opens is listed under that entry for its own edit.

**Sweep across the other retired statements.** SPEC-1, SPEC-2 and SPEC-6 were swept once,
from the repository root over `pkg/ cmd/ tests/ migrations/ schemas/ docs/`, with a grep for
each retired statement's distinctive phrasing:

- SPEC-1 (§4.1's rule that a bound entry's presence selects the per-session teardown, and the
  §4.7 `Shutdown` row's restated `superseded` outcome): no comment carriers.
- SPEC-2 (§7.2's premise that a replacement pod short of `attached` holds no started runtime
  and nothing to seal): no comment carriers.
- SPEC-6 adds catalog rows and retires no statement, so it has no carrier class.

A later spec-lane edit that retires a statement adds a sub-block in the same three-part form
under this heading rather than a fourth copy of the invariants.

### CODE-10 · the Go, SQL and test comments its grep returns · the comment carriers of the withdrawn reporting universal take their reduction

**Carrier.** SPEC-3 withdraws the universal that the adapter reports a cleanup outcome on every
session release. A carrier is a comment whose subject is the report itself, stating that the
adapter reports on every session release or on every per-slot cleanup, or whose subject is the
served-session count advancing or being evaluated across releases.

**IMPLEMENTOR'S CHOICE:** the carrier set. The constraint is that the set is the output of this
one command, run at application time from the repository root:

```
grep -rniE -e 'every (session|slot|clean) release' -e 'each (session|slot|clean) release' -e 'on every release' -e 'per-session-release' -e 'each per-slot cleanup' -e 'evaluated per release' -e 'reports its outcome to the gateway' -e '^\s*(//|--).*((sessions_served|SessionsServed).*\b(each|every)\b|\b(each|every)\b.*(sessions_served|SessionsServed))' pkg/ cmd/ tests/ migrations/ schemas/
```

**Arm rule.** The sentence's subject chooses the arm. Where the subject is the report itself,
the edited sentence must neither state nor imply that a report follows every release or every
per-slot cleanup. Deleting the trigger clause meets that test when the trigger is a separable
phrase and nothing left in the sentence carries the universal. When it does not, because the
quantifier modifies what is reported (such as "reports each per-slot cleanup outcome") or
because the report clause is coordinated with a clause that stays true (such as "a per-slot
cleanup runs at every session release, and the adapter reports its outcome"), replace the
report's own scoping words with the predicate SPEC-3's `**Scrub model.**` replacement states,
written as "the outcome of a cleanup a `Shutdown` performs to reclaim a slot that reached
`running`", and leave the clause that stays true standing. Where the subject is the
served-session count advancing or being evaluated across releases, re-key the trigger onto the
cleanup-outcome report, in the words SPEC-3's §12.6 replacement uses, which are "on each
cleanup-outcome report"; a `// spec:` gloss that attributes the per-release evaluation of
`sessions_served` to §12 is re-keyed with the rest, onto the write §12.6 keeps, and its
section number stays.

CODE-10 lands after SPEC-3. Tiers: 0, 11.

### CODE-11 · pkg/gateway/externalapi/errorclassify/errorclassify.go, pkg/gateway/sessionserver/start.go, pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go · the comment carriers of the narrowed SETUP_COMMAND_FAILED cause take their reduction

**Carrier.** SPEC-5 replaces the §15.1 `SETUP_COMMAND_FAILED` row's cause and retryability
sentences, and that row is the single home of what the code covers and whether it is
retryable. A carrier is a comment that restates the row's cause or its retryability ground.

**Sites.** The comment block above the `CONFIRMATION_REQUIRED` and `SETUP_COMMAND_FAILED` pair
in `pkg/gateway/externalapi/errorclassify/errorclassify.go`, the `writeSetupCommandError` and
`isTransientPodClaimError` doc comments in `pkg/gateway/sessionserver/start.go`, and the
`// diagnosis:` comment on `TestHoldOrFailOnResumeErrorSetupCommand_spec_7_3` in
`pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`.

**Arm rule.** Each site is cut back to a citation of §15.1's row. **IMPLEMENTOR'S CHOICE:** the
replacement text at each site. The constraint is that the replacement at each site is one
sentence, cites §15.1 by heading, keeps §7.3 where the replaced span cited it, and names no
gRPC code as the cause. The classification data and the branch on
`status.Code(setupFail.Cause)` are untouched. The retired-form line citations on
`errorclassify.go`'s map entries are neither converted nor added to. CODE-5's `codes.Aborted`
arm in `isTransientPodClaimError` carries its own inline comment, so the two deliverables take
different hunks of that function.

CODE-11 lands after SPEC-5. Tiers: 0.

### CODE-12 · the Go, SQL and test comments its grep returns · the comment carriers of the re-keyed claim-deletion projection take their reduction

**Carrier.** SPEC-4 re-keys §4.6.1's two claim-deletion bullets off the pool's recycle setting
and its retirement limits and onto the phase the pod projects at the DELETE, and those bullets
are the single home of what the WarmPoolController projects when a claim is deleted. A carrier
is a comment that states what the WarmPoolController projects at a claim DELETE and keys that
outcome on the pool's recycle setting or on its retirement limits, or that asserts without
qualification that the deletion returns the pod to `idle`.

**IMPLEMENTOR'S CHOICE:** the carrier set. The constraint is that the set is the output of this
one command, run at application time from the repository root. Some carriers wrap their sentence
across a comment break, so the command joins each comment block into one line before matching,
the way the §28.1 N3 matcher joins two consecutive comment lines:

```
perl -0777 -ne 'while (/((?:^[ \t]*(?:\/\/|--)[^\n]*\n)+)/gm){$b=$1;$p=$`;$ln=1+($p=~tr/\n//);$j=$b;$j=~s/\n[ \t]*(\/\/|--) ?/ /g;$j=~s/\s+/ /g; print "$ARGV:$ln\n" if $j=~/(returns?|returned|projects?) (the pod|a pod|it) (back )?to `?idle|(is|are|was|were) returned (back )?to `?idle|projects `?idle|claim (DELETE|deleted) on a `?recycle\.enabled/i}' $(git ls-files '*.go' '*.sql' | grep -E '^(pkg|cmd|tests|migrations)/')
```

**Arm rule.** A carrier loses its statement of the projected outcome and keeps the rest of its
sentence, which leaves the outcome to §4.6.1, where the WarmPoolController is the sole writer of
the coarse occupancy phase and projects it from the claim and from the phase the pod projects
at the DELETE. Where the projection statement is the sentence's only content, the sentence
instead states what its own subject does, which is that the gateway deletes the claim or that
the adapter releases the slot.

CODE-12 lands after SPEC-4. Tiers: 0, 11.

### CONF-1 · tests/tier3_contract/adapter_bind_attempt/, tests/tier10_conformance/slot_bind_attempt_conformance_test.go, scripts/seed-claim-register.py, tests/claim-map.json · the published contract is enforced at the wire and exercised in process

The shipped CONF-1 asserted a one-entry-one-epoch invariant. Under the amended mechanism that
invariant does not exist, and a battery asserting it would pass a defective adapter and fail a
conforming one, so the re-cut is a requirement rather than a preference.

**The rule set under test.** §4.7.1 numbers and names the rules and states, per numbered rule,
whatever answer an adapter gives, and §15.4
states what conformance against them means. Neither is restated here. The battery is derived
from those two by reference: every case below names the rule it drives and reads its assertion
off that rule. A rule that acquires no case shows up as a missing number in the list below.

**Tier 3 is the enforcement.** The rules are wire behaviour and the precedent is exact:
`tests/tier3_contract/adapter_generation_fence/` holds `generation_fence_wire_test.go`, a
descriptor-and-bytes gate pinning a precondition field's number and type across thirteen
messages, and `barrier_unfenced_session_wire_test.go`, a bufconn case driving `adapter.New` over
gRPC and asserting the handler's behaviour. A case per numbered rule lands in a new
`tests/tier3_contract/adapter_bind_attempt/` following both forms.

**Tier 10 is the in-process battery**, following
`tests/tier10_conformance/recycle_scrub_conformance_test.go`, which drives the exported adapter
`Server` directly with a fake runtime. It covers the same rules against this adapter and is
worth having for the diagnosis it gives, rather than as the gate.

**The honest limit, recorded rather than papered over.** The §15.4 obligation is published for
third-party adapter authors and the project has no harness that can run one.
`cmd/lenny-compliance` drives a runtime binary over stdin and stdout JSONL against a fake
adapter; it imports no `adapterv1` and no gRPC, so it has no adapter under test and cannot
observe an adapter obligation. SPEC-5 states that the rules are normative for a third-party
adapter and that the project runs them against its own. The absence of a third-party harness
registers as a `tests/claim-map.json` row with status `ABSENT`, following the thirteen `R16`
rows the `coordination_generation` fence already carries, naming the rule set it cannot
enforce and the reason.

**The cases, one per numbered rule.** Each case is titled for its rule and appears at tier 3 over
bufconn and again at tier 10 in process:

- Rule 1, **the pairing rule**: drives a non-mid-session bind-sequence request carrying an empty
  `bind_attempt`, and a mid-session request carrying a non-empty one; asserts the status that
  rule states and that the adapter created, resolved and stamped nothing.
- Rule 2, **the reclaim hold**: drives a bind-sequence request for a slot whose identifier the
  adapter holds through a running cleanup; asserts the status §15.4's
  reclaim-hold block gives, and that nothing was created and nothing resolved. Both code sites
  that test the hold take their own rows: `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`
  and `Resume` are refused at the guard acquisition, and `AssignCredentials`, `StartSession` and
  `ConfigureWorkspace` inside the resolve. Every row asserts the same answer, so the two sites
  are one answer to the caller.
- Rule 3, **the mid-session-create rule**: drives a mid-session `FinalizeWorkspace` and a
  mid-session `PrepareWorkspace` for a session the adapter holds no entry for; asserts the status
  that rule states and that the registry and the filesystem are both unchanged.
- Rule 4, **the create-and-stamp rule**: drives a `Resume` for a slot the adapter holds no entry
  for; asserts that the entry it creates carries that request's token, read back through a later
  `Shutdown` naming that token and one naming another.
- Rule 5, **the attempt identity rule**: drives a bind-sequence RPC carrying attempt B against an
  entry stamped A; asserts the status, the `ErrorCode` and the category that rule states,
  and that the entry, its `current` directory and its `credentials.json` survive the call.
- Rule 6, **the started-session rule**: drives a non-mid-session bind-sequence RPC against an
  entry whose session has started, a §7.4 mid-session upload against that same entry, and a
  repeat `ConfigureWorkspace` for the session that started on that pod; asserts the status, the
  `ErrorCode` and the category that rule states for the first, and admission for the two the rule
  exempts. The mid-session arm is `TestMidSessionUploadIsAdmittedOnAStartedSession`, driven over
  the connection the successful bind published, and it is written with the rest rather than
  deferred: its absence is what let the §7.4 regression stand for three rounds.
- Rule 7, **the admit rule**: drives a bind-sequence RPC carrying the entry's own token against
  an entry whose session has not started, and a mid-session request carrying no token against an
  entry whose session has started; asserts each is admitted and returns the entry with its token
  exactly as found.
- Rule 8, **the start-confirmation rule**: drives a start whose entry an unconditional `Shutdown`
  removes while the fake runtime blocks inside `Start`; asserts the status that rule states,
  that the session is taken back off the shared runtime process, and that no cleanup outcome is reported. The fake
  runtime's block inside `Start` is what makes the ordering deterministic.
- Rule 9, **the first-frame rule**: drives a `PrepareWorkspace` stream whose second frame carries
  a non-empty `bind_attempt` differing from the first frame's, and one whose second frame carries
  `mid_session` true where the first frame's was false; asserts each is admitted, extends the
  first frame's staged bytes, leaves the entry's stamp as the first frame set it, and creates no
  second entry.
- Rule 10, **the teardown-pairing rule**: drives a `Shutdown` carrying neither field and one
  carrying both; asserts the status that rule states and that the entry, the tree and the
  credential file are each intact.
- Rule 11, **the no-entry rule**: drives a `Shutdown` of each form for a session the adapter
  holds no entry for; asserts the outcome that rule states, the successful status rule 15 fixes
  for every reclaim outcome, that nothing is removed, and that neither teardown runs.
- Rule 12, **the unconditional-teardown rule**: drives a `Shutdown` asking for the unconditional
  teardown against an entry the adapter holds; asserts the outcome that rule states, that
  the slot release ran, and that the runtime teardown ran for an entry whose session has started.
  This is the arm a battery of refusal cases alone never reaches.
- Rule 13, **the attempt-mismatch rule**: drives a `Shutdown` naming attempt B against an entry
  stamped A, and a `Shutdown` naming an attempt against an entry a `StartSession` or a
  `ConfigureWorkspace` created and which therefore carries no token; asserts the outcome that
  rule states on each arm, and that the entry, the tree and the credential file survive
  both. The second arm is the fail-closed one, and without it a reclaim naming any attempt would
  collect a successor's live session.
- Rule 14, **the attempt-match rule**: drives a `Shutdown` naming the token the entry carries;
  asserts the outcome that rule states, that the slot release ran, and that the runtime
  teardown ran for an entry whose session has started. This is the positive the rule 11 and rule
  13 cases are read against.
- Rule 15, **the reclaim-outcome rule**: drives one `Shutdown` reaching each of rule 11, rule 12,
  rule 13 and rule 14; asserts every outcome arrives on a successful call carrying the matching
  `slot_reclaim` value and no gRPC error, and that the response reports a clean exit on the arms
  that remove nothing.

Every numbered rule carries a case above, so the battery names no rule as unobservable at the
wire.

**The cases no single numbered rule states.** Each is titled for what it exercises rather than
for a rule, and each appears at both tiers:

- **The stamp-once rule.** §4.7.1 states it above the numbered cascade, so it takes its own case.
  A `StartSession` creates an entry carrying no token; a non-mid-session request carrying attempt
  B against that entry is refused under rule 6 and does not write B, observed through a later
  `Shutdown` naming B answering `superseded` rather than `reclaimed`. This is the one a naive implementation gets wrong, because stamping on resolve
  looks like an improvement.
- **The indivisibility of the resolve, the create and the stamp.** Two concurrent bind attempts
  at one slot identifier over separate connections, under `-race`, with exactly one admitted and
  the other refused `SLOT_BIND_ATTEMPT_SUPERSEDED`. No numbered rule states it on its own; it is
  the first step of §4.7.1's registry critical-section paragraph, and an adapter that resolves, releases its lock and
  then stamps admits both.
- **Rule 5 evaluated ahead of rule 6.** A bind-sequence RPC carrying attempt B, with
  `mid_session` false, against an entry stamped A whose session has already started, answered as
  rule 5 states rather than as rule 6 states, with the entry, its `current` directory and
  its `credentials.json` surviving. The request meets both rules' conditions, and §15.4 publishes
  applying them in the other order as non-conformance.
- **The reclaim hold against the `Shutdown` cascade.** A `Shutdown` for a session whose cleanup
  is running is admitted and answers under rule 11, while a bind-sequence request for the same
  identifier is refused under rule 2. The interaction is stated by §5.2's reclaim-hold paragraph
  rather than by either rule, and an adapter that applies the hold to a reclaim blocks its own
  cleanup behind itself.

## Staged schema, chart, and migration changes

### SCHEMA-1 · schemas/lenny-adapter.proto, scripts/seed-claim-register.py, tests/claim-map.json · the bind attempt, the mid-session conditioning, the two-field teardown precondition, the reclaim outcome, the two refusal codes, and the scrub-outcome and report-trigger comments

One window, and the only schema step in this proposal. Every declaration the edit makes is
additive: two enum values, one enum, and nine fields, no field removed, no field renumbered, no
RPC added, no message removed. Two comment sentences are replaced beside them, which declares
nothing. `buf breaking` has nothing to fire on. `make generate-proto` runs in the same commit
and the regenerated `pkg/proto/adapter/v1` package lands with it, so no step compiles against a
half-generated tree.

The epoch design's seven `bind_epoch` response fields are not added, and no field on this list
reports anything back to the caller about the attempt. Nothing the caller must hold travels on
a response, which is the property the epoch could not achieve. `ShutdownResponse.slot_reclaim`
is kept: it is the outcome the whole mechanism answers with, and it is the one response field
the compensation reads.

**The two error codes.** `ErrorCode` runs to 27 today, so the new values take 28 and 29. The
proto comment near the enum's tail gestures at a Phase-2 reserved range of 1000-1999 in prose
without declaring a `reserved` statement, so either range is available; the low numbers are
chosen because the codes are Phase-1 adapter behaviour and sit beside the Phase-1 codes they
neighbour on this contract.

```proto
    // ERROR_CODE_SLOT_BIND_ALREADY_STARTED: spec §4.7.1 rule 6, the
    // started-session rule. PERMANENT; FailedPrecondition.
    ERROR_CODE_SLOT_BIND_ALREADY_STARTED = 28;
    // ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED: spec §4.7.1 rule 5, the attempt
    // identity rule. TRANSIENT; Aborted.
    ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED = 29;
```

`Aborted` is chosen for the superseded case because `isTransientPodClaimError` already gains one
`Aborted` arm for the reclaim hold and CODE-2's rollback, so the refusal reuses that arm rather
than adding a third.

**The outcome enum**, kept from the shipped text beside the existing `SessionScrubOutcome`,
which is the sibling it follows. proto3 enum values share the enclosing package namespace, so
the values are fully prefixed with the enum's own name:

```proto
// SlotReclaimOutcome is the outcome a Shutdown reports under spec §4.7.1
// rule 15.
enum SlotReclaimOutcome {
  SLOT_RECLAIM_OUTCOME_UNSPECIFIED = 0;
  // SLOT_RECLAIM_OUTCOME_RECLAIMED: spec: §4.7.1 rules 12 and 14; §7.1.
  SLOT_RECLAIM_OUTCOME_RECLAIMED = 1;
  // SLOT_RECLAIM_OUTCOME_ABSENT: spec: §4.7.1 rule 11; §7.1.
  SLOT_RECLAIM_OUTCOME_ABSENT = 2;
  // SLOT_RECLAIM_OUTCOME_SUPERSEDED: spec: §4.7.1 rule 13; §7.1.
  SLOT_RECLAIM_OUTCOME_SUPERSEDED = 3;
}
```

**The scrub-outcome and report-trigger comments.** Comments on the shipped `ReportSessionScrub`
surface state what a `RELEASED` outcome and a `LEAKED` outcome imply about the cleanup's acts, and SPEC-3's §5.2 disposition table
reports `released` for a cleanup that closes the session cleanly and fails only in a directory
removal, so each of those comments becomes false when that lands. Each one drops
the effect list and cites §5.2 for the terms. These comments document the outcome values the RPC
reports, where naming the report is the right thing to state, so each names the outcome and
leaves what it implies to the section. In the `ReportSessionScrub` RPC comment, the text that reads, verbatim:

```
  // RELEASED when the slot's runtime, credential timers, and per-slot
  // directory tree were torn down cleanly, or LEAKED when a resource could
  // not be reclaimed.
```

becomes:

```
  // RELEASED and LEAKED are the outcomes §5.2 states for the cleanup.
```

In the `SessionScrubOutcome` enum, the `SESSION_SCRUB_OUTCOME_RELEASED` comment, which reads,
verbatim:

```
  // SESSION_SCRUB_OUTCOME_RELEASED — the slot's runtime, credential
  // timers, and per-slot directory tree were torn down cleanly and the
  // slot was released. spec: §5.2 (slot_cleanup → released).
```

becomes:

```
  // SESSION_SCRUB_OUTCOME_RELEASED — the cleanup reported released, on the
  // terms §5.2 states. spec: §5.2 (slot_cleanup → released).
```

In the same enum, the `SESSION_SCRUB_OUTCOME_LEAKED` comment, which reads, verbatim:

```
  // SESSION_SCRUB_OUTCOME_LEAKED — a resource could not be reclaimed at
  // the session release. The gateway feeds the outcome into the
  // unhealthy-threshold ledger behind the lenny.dev/drain-request
  // annotation. spec: §5.2 (leaked slot semantics); §4.6.3.
```

becomes:

```
  // SESSION_SCRUB_OUTCOME_LEAKED — the cleanup reported leaked, on the
  // terms §5.2 states. The gateway feeds the outcome into the
  // unhealthy-threshold ledger behind the lenny.dev/drain-request
  // annotation. spec: §5.2 (leaked slot semantics); §4.6.3.
```

The drain-ledger sentence states what the gateway does with the outcome, on §4.6.3's terms, which
nothing in this change touches, so it stands as it is.

Two further comments on the same surface carry the universal SPEC-3 withdraws; each loses it
and cites §5.2 for the cleanups the adapter reports. The opening
sentence of the `ReportSessionScrub` RPC comment, together with the words that open the sentence
after it on the same physical line, reads, verbatim:

```
  // ReportSessionScrub reports the outcome of the per-slot cleanup the
  // adapter runs on every session release (§5.2), across the
  // `maxConcurrentSessions > 1` and recycling cases alike. The outcome is
```

becomes:

```
  // ReportSessionScrub reports a per-slot cleanup's outcome for the
  // cleanups §5.2 states the adapter reports, and for no other release.
```

The opening sentence of the `ReportSessionScrubRequest` message comment reads, verbatim:

```
// ReportSessionScrubRequest carries the §5.2 per-slot cleanup outcome the
// adapter reports on every session release.
```

becomes:

```
// ReportSessionScrubRequest carries a per-slot cleanup's outcome, for the
// cleanups §5.2 states the adapter reports and for no other release.
```

The rest of that comment (the `pod_id` and `session_id` sentences), the `SessionScrubOutcome`
enum comment and the `ReportSessionScrubResponse` comment are unedited: they describe the row
key, the cleanup and the increment, and none states a trigger for the report.

No enum value, field number or RPC signature moves, so these replacements change the regenerated
package only in the comments it copies from the proto.

**The fields.** Every number below was checked free against the message it lands in, in
`schemas/lenny-adapter.proto` as the file stands:

| Message | Field | Number | Basis | Role |
|:--|:--|--:|:--|:--|
| `PrepareWorkspaceRequest` | `string bind_attempt` | 5 | 1-3 used, 4 `reserved "slot_id"` | the token, on the first entry-creating RPC |
| `PrepareWorkspaceRequest` | `bool mid_session` | 6 | same block | read before the resolve, so the mid-session-create rule is reachable |
| `FinalizeWorkspaceRequest` | `string bind_attempt` | 6 | 1-4 used (`mid_session` is 4), 5 reserved | the token |
| `RunSetupRequest` | `string bind_attempt` | 5 | 1-3 used, 4 reserved | the token |
| `AssignCredentialsRequest` | `string bind_attempt` | 4 | 1-2 used, 3 reserved | the token |
| `ResumeRequest` | `string bind_attempt` | 16 | 1-5 and 7-14 used, 6 and 15 reserved | the token, on the §7.3 re-attach's first and only entry-creating RPC |
| `ShutdownRequest` | `string bind_attempt` | 7 | 1-3 used, 4 reserved, 5 `recycle`, 6 `coordination_generation` | the fence |
| `ShutdownRequest` | `bool unconditional_teardown` | 8 | same block | the other half of the precondition |
| `ShutdownResponse` | `SlotReclaimOutcome slot_reclaim` | 3 | 1-2 used | the outcome |

`StartSessionRequest` and `ConfigureWorkspaceRequest` carry no token, because `Binder.Launch`
issues them and mints none.

Both `bind_attempt` and `unconditional_teardown` are bare scalars rather than `optional`, for the
reason the summary's `unconditional_teardown` decision gives.

Each field comment cites the §4.7.1 rule or paragraph that governs the field, by number or name,
and restates none of it.

**The tier-3 closed-field-set gate moves in the same commit.** A shipped test pins the shutdown
messages to a closed field set, and that gate is deliberate: it forces a proto addition on the
teardown path to be reviewed rather than tolerated.
`TestShutdownMessagePostRemovalDescriptor_spec_4_1`
(`tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208`) drives
`assertFieldSet` (`:259`), which errors on any declared field number absent from its `want` map
(`:264-267`). SCHEMA-1 therefore also targets that file: `wantReq` gains `7: "bind_attempt"` and
`8: "unconditional_teardown"`, the `ShutdownResponse` `assertFieldSet` call gains `3:
"slot_reclaim"`, and the test's doc comment and its `// diagnosis:`
comment gain a clause naming the two teardown preconditions and the reclaim outcome as what the
post-removal contract now also declares. Nothing else in that file
moves: field number 4 and the name `slot_id` stay reserved, the retired-wrapper scan stays, the
`// spec:` annotation stays at 4.1, 4.7 and 5.2 because the added fields extend the
same contract, and the byte-identical round-trip case in the same file is unaffected because a
zero-valued additive field serialises to nothing. This is the only closed field set over the
messages SCHEMA-1 opens. The other tier-3 descriptor pin,
`tests/tier3_contract/checkpoint_stream/checkpoint_stream_wire_test.go`, carries closed sets of
its own over messages SCHEMA-1 does not open.

**Claim register.** Three rows are staged below, under the seeding convention the `## Testing`
preamble below states. The two `WIRED` rows land with this step. The `ABSENT` row lands
with CONF-1's tier-10 file at S22, because the surfaces it names do not exist until then.
`tests/claim-map.json` is generator output
rather than an authoring source: the tier-0 gate `TestClaimRegisterIsReproducibleFromItsGenerator`
(`tests/tier0_static/claim_register_generator_test.go`) re-runs the generator and fails unless
the committed file is byte-identical, so a row hand-written into the file is dropped by the next
seeding run. The generator emits its rows sorted by claim, so none is placed by hand. Each row
carries a `note`, as its `EXPLICIT` siblings do, and names its production surface in
file-or-symbol form rather than as a line number. `spec_anchor` is `#2851-gateway-to-pod`, the
anchor the sibling `AttachRequest.coordination_generation` row uses.

Two rows are `WIRED`, because a production reader ships in the same change: the adapter compares
the token and the two teardown fields inside `Shutdown`, and the gateway's compensation reads
the outcome. Both are carried under that same seeding convention. §28.4 does not oblige them:
its obligation at `spec/28_communication-channels.md:163` runs from a normative §28 statement to
a register row, and `Shutdown` carries no §28 statement to run it from. The obligation is
one-way, so §28.4 leaves a row it does not require lawful, and the tier-0 validator applies the
schema rules alone to a row outside the credential set
(`tests/tier0_static/claim_register_test.go:46`), which both rows satisfy. The third is `ABSENT`, recording that the §15.4 rule set is normative for a
third-party adapter and that the project has no harness able to run one, per CONF-1. That row
names `R8`, the remediation plan's reciprocal host-conformance battery, as the step that closes
it, because a row whose status is not `WIRED` must name a step the plan declares: the tier-0
gate `TestClaimRegisterSaysWhatTheSpecificationRequires`
(`tests/tier0_static/claim_register_test.go`) fails a non-`WIRED` row with an empty
`deferral_id`, and fails a `deferral_id` naming a step the plan does not declare. `R8` is
declared at `gateway-runtime-comms-remediation.md:980` and is already the deferral the shipped
external-adapter compliance-suite row names (`scripts/seed-claim-register.py`).

```json
{
  "claim": "ShutdownRequest bind_attempt and unconditional_teardown teardown precondition",
  "status": "WIRED",
  "spec_anchor": "#2851-gateway-to-pod",
  "surface": "`pkg/adapter/session.go` `Server.Shutdown` two-field precondition and bind-attempt comparison, `pkg/gateway/podlifecycle/podsession/slotbinder.go` `Binder.compensateFailedSlotBind`",
  "note": "the adapter requires exactly one of the two fields, compares the carried token against the entry it holds, and performs neither teardown on a mismatch; the gateway's compensation names the token its own attempt minted and reads the outcome"
},
{
  "claim": "bind_attempt carried on the six slot-entry requests and stamped once on create",
  "status": "WIRED",
  "spec_anchor": "#2851-gateway-to-pod",
  "surface": "`pkg/adapter/slot.go` `Server.ensureSlotStateLocked`, `pkg/gateway/podlifecycle/podsession/bindattempt.go` `newBindAttempt`",
  "note": "the adapter writes the token on the create branch alone and compares it on every resolve"
},
{
  "claim": "third-party adapter conformance harness for the §15.4 slot-bind rules",
  "status": "ABSENT",
  "spec_anchor": "#2851-gateway-to-pod",
  "deferral_id": "R8",
  "surface": "`tests/tier3_contract/adapter_bind_attempt/`, `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`",
  "note": "the rules are normative for any adapter implementation; cmd/lenny-compliance drives a runtime binary over JSONL against a fake adapter and imports no gRPC, so the project runs the clauses against its own adapter only"
}
```

No chart value and no migration.

## Staged docs changes

### DOCS-1 · docs/reference/state-machines.md · the per-slot sub-state table gains the new row, the running row's trigger, the released row's trigger and the leaked clause take their replacements, and the pod state machine paragraph takes the projection clause replacements

DOCS-1 edits the per-slot sub-state table, the concurrent-occupancy prose that follows it, and
the pod state machine paragraph on one page. Every
edit lands after SPEC-4.

**The per-slot sub-state table.** Under `### Per-slot sub-states`, add the row matching the §6.2
edge, immediately after the `receiving_uploads` → `running` row:

```
| `receiving_uploads` | `slot_cleanup` | A cleanup runs on the slot after its bind is abandoned or fails before the slot reaches `running`, a start still in flight included |
```

The same table's `receiving_uploads` → `running` row (`docs/reference/state-machines.md:235`)
states the boundary in the terms the staged §4.7.1 record step retires, and it takes its own trigger-cell replacement. It
currently reads:

```
| `receiving_uploads` | `running` | Workspace ready; the session is dispatched to the runtime with its session identifier |
```

Replace it with:

```
| `receiving_uploads` | `running` | Workspace ready; the adapter records the pod's shared runtime process as holding the session |
```

The same table's `slot_cleanup` → `released` row (`docs/reference/state-machines.md:237`) states
the acts SPEC-4 stops asserting in the fence, and it takes its own trigger-cell replacement. It
currently reads:

```
| `slot_cleanup` | `released` | Slot workspace removed, processes killed, slot released |
```

Replace it with:

```
| `slot_cleanup` | `released` | The cleanup ends and the slot stops counting toward the pod's occupancy |
```

The fence entry SPEC-4 leaves behind carries a section pointer in place of a trigger, and this
page carries no specification citation, so the cell states in the page's own voice the one
property every traversal of this edge shares. Completion is not that property: a cleanup that
closes the session cleanly and fails only in removing the slot's slot tree reaches `released`
without having completed. Every traversal does end with the slot no longer counting toward the
pod's occupancy, and the leaked disposition the page states below already distinguishes a
leaked slot by the occupancy it retains, so the two outcomes of a cleanup read apart in the
page's own terms. The cell leaves the cleanup's acts and the terms of each outcome to the
pages that document them.

The page's `slot_cleanup -> leaked` clause (`docs/reference/state-machines.md:251`) states the
trigger SPEC-4 withdraws from the sibling fence annotation, and it takes its own replacement on
the same terms. It currently reads:

```
`slot_cleanup -> leaked` when the cleanup timeout is exceeded and the slot is not reclaimed until the pod terminates
```

Replace it with:

```
`slot_cleanup -> leaked` when the gateway reads a `leaked` cleanup-outcome report or a `Shutdown` response that reports no clean exit, or a reclaim it sent is never answered, and the slot is not reclaimed until the pod terminates
```

A timeout is one cause of a failed cleanup rather than the condition, and a cleanup whose
failure the gateway never learns of leaves the slot's identifier held without entering the
sub-state, so the page states the condition and leaves the causes to the pages that document
them. The following
sentence, on the occupancy a leaked slot retains and the `claimed -> draining` threshold it
counts toward, is unchanged.

**The pod state machine paragraph.** The paragraph under `## Pod state machine`
(`docs/reference/state-machines.md:138`) is the published restatement of the occupancy
projection §4.6.1 owns and SPEC-4 re-keys there, and it carries an input enumeration and
claim-deletion clauses of its own. Left as it
stands it answers `idle` where the specification answers `draining`, on the input SPEC-4's own
rationale names: a claim deleted while a `maxConcurrentSessions: 1`, `recycle.enabled: true` pod
projects `claimed`, which is what a failed bind produces. Apply the replacements below in
the page's own voice, carrying no specification section numbers:

- "a level-triggered projection of the per-pod `SandboxClaim`: claim existence, the claim's
  binding state and disposition, and `sessionPolicy`" becomes "a level-triggered projection of
  per-pod `SandboxClaim` existence, the claim's binding state and disposition, `sessionPolicy`,
  and the phase the pod currently projects". The head of the sentence is replaced with the
  enumeration, because the page reads the list as the claim's own contents and the phase the pod
  currently projects is the controller's own prior write to `Sandbox.status.phase`.
- "A pod with no claim projects `idle`" becomes "A pod in a warm-inventory phase with no claim
  projects `idle`".
- "a claim deleted on a recycling pod under its limits projects `idle`" becomes "a claim deleted
  while the pod projects `reserved`, its scrub and any re-warm already complete, projects
  `idle`".
- "or a claim deleted on a pod with `recycle.enabled: false`" becomes "or a claim deleted while
  the pod projects `claimed`, on a pool of either recycle setting".

The remaining clauses of the sentence are unchanged. Each claim-deletion clause is re-keyed on the phase the
pod projects at the DELETE, which is what keeps the sentence a partition over the projection
input: exactly one clause answers any claim deletion. The replacement wording states what
SPEC-4's re-keyed §4.6.1 bullets state, so the published page and the specification state one
rule.

No shipped tier-11 gate compares this table's edge rows against the §6.2 block: the tests in
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` read the specification and
the reference page separately and never meet. DOCS-1 therefore carries the tier-11 work that
makes the pair reconcile, specified under `## Testing`. That work covers the per-slot edge
pairing alone. The pod state machine paragraph takes no gate, because §4.6.1's bullet list
carries a vm-restart carve-out the page deliberately omits and the two are not comparable by
substring; the published error catalog DOCS-3 edits is held by no gate for the same reason.

### DOCS-2 · docs/reference/adapter-contract.md · the `Shutdown` row, the `DemoteSDK` row, the `ReportSessionScrub` row, and one bind-attempt paragraph

`docs/reference/adapter-contract.md` states of itself that it is the reference for the protocol
between the adapter sidecar and the runtime binary, covering the gateway-to-adapter gRPC
protocol among other sections (`docs/reference/adapter-contract.md:10`), and its `Shutdown` row
(`:75`) is the reader-facing mirror of the §4.7 row SPEC-1 rewrites. After SPEC-1, SPEC-3,
SPEC-5 and CODE-15 that row is false on the usage flush and the runtime close, which the staged
§4.7 row gates on a session whose start the adapter has admitted while the slot release runs for
any entry the call removed; on the unconditional cleanup-outcome report, which SPEC-3 withholds
on the pre-running path; and on the drain gate, which the staged row re-derives from the
deregistration. The row also carries nothing of the two teardown preconditions or the reclaim
outcomes. The shipped gate `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only
the substrings "end-of-session teardown", "recycle disposition", "ReportSessionScrub" and
"ReportPodScrub", all of which survive the staged edits, so nothing turns red on the drift.

DOCS-2 republishes no rule. The `Shutdown` row states the two-field
precondition and the outcomes, which is the part of the contract a reader of this page can
observe. The `DemoteSDK` row mirrors the registry removal the §4.7 row SPEC-1 amends, together
with the slot cleanup that row states the demotion runs inside the call, and carries
the fresh-entry consequence rule 4 (**the create-and-stamp rule**) gives, because this page cannot
cite the rule. The `ReportSessionScrub` row (`:81`) carries the universal SPEC-3 withdraws, a row in SPEC-3's
carrier table; it now points at this page's own `Shutdown` row for which cleanups are reported
and adds the one clause that row lacks, which excludes every other release. One added paragraph says what the token is for and where the rules are stated. The
cascade and its wire observables stay in §4.7.1: republishing them here would drop a
normative cascade into a page whose gRPC section is a one-line orientation table
(`.claude/rules/doc-content.md`, "Match technical depth to the page"), and a runtime author can
neither issue a request those rules govern nor observe a refusal they produce
(`docs/reference/adapter-contract.md:53`).

The staged prose carries no specification section number, because reader-facing documentation
states the behavior and links rather than citing a number (`.claude/rules/doc-content.md`).

Replace the `Shutdown` row under `**Gateway-to-Adapter RPCs:**` with the row below. It stays one
physical line, because the gate reads the row through `lineContaining(page, "| \`Shutdown\` |")`.

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. Every request states which teardown it is asking for, by carrying either the bind attempt whose registry entry it is reclaiming or the unconditional-teardown flag, and a request carrying neither or both is rejected as invalid and performs nothing. The response reports what became of the entry the request was addressed to: `reclaimed` when the adapter held that entry and released the slot, `superseded` when the adapter holds an entry the request is not addressed to, so nothing was released, and `absent` when the adapter holds no entry for the session. Every outcome is answered on a successful call, and the two outcomes that remove nothing run neither teardown. The slot release removes the session's slot tree and runs whenever the request removes an entry, whether or not `AssignCredentials` has bound that entry. The runtime teardown runs only for a session whose start the adapter has admitted: it flushes the session's final usage report and then closes the runtime, and the CH-RUNTIMEOPS drain signal precedes that close only when the deregistration leaves the adapter holding no other bound session. The adapter reports the per-slot cleanup outcome through `ReportSessionScrub` for a slot that reached `running`, and reports no outcome for a cleanup on a slot that did not. The request carries the recycle disposition beside that teardown: on the recycle disposition the adapter keeps the pod process alive, runs the whole-pod scrub the carried `RecycleScrub` parameterizes, and reports its outcome for `podId` through `ReportPodScrub`. |
```

Amend the `DemoteSDK` row (`:64`) so it states the registry effect rule 4 turns on and the slot
cleanup the demotion runs inside the call:

```
| `DemoteSDK` | Tear down the pre-connected SDK process, drop the adapter's slot registry entry the pod holds if it holds one, whichever session holds it, running that slot's cleanup inside the call before it answers, and return the pod to pod-warm state. Where that cleanup runs and completes, the next bind sequence on the pod creates a fresh entry and stamps it with that attempt's own token. |
```

Replace the `ReportSessionScrub` row (`:81`) under `**Adapter-to-Gateway RPCs:**` with the row
below. It stays one physical line and its addressing sentence is unchanged word for word, because
the tier-11 gate `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` reads the row
through `lineContaining(page, "| \`ReportSessionScrub\` |")` and asserts that this page and the
§4.7 row open the addressing rule identically. The served-session and leak-ledger sentence is
unchanged:

```
| `ReportSessionScrub` | Report a per-slot cleanup's outcome (`released` or `leaked`) for the cleanups the `Shutdown` row states the adapter reports, and for no other release. The request is session-scoped: it is addressed by the identifier of the released session and names no slot. The gateway increments the pod's served-session count and feeds the leak ledger. |
```

Add the paragraph below immediately after the `**Gateway-to-Adapter RPCs:**` table and before
the `**Adapter-to-Gateway RPCs:**` heading. It is the whole of what this page says about the
token:

```
**Bind attempt token.** The gateway may attempt to bind one session onto a pod more than once, and each attempt mints its own opaque token, carried on the bind-sequence requests the linked contract's carriage table lists. The adapter stamps the token onto the entry it creates and afterwards compares it for equality, which is what lets a teardown that compensates an abandoned attempt name the entry it is entitled to destroy: a reclaim naming an attempt that no longer owns the slot answers `superseded` and removes nothing, so it cannot destroy a session a later attempt has started. The rules the adapter applies to the token are numbered and named in [Role and Gateway RPC Contract](https://github.com/lennylabs/lenny/blob/main/spec/04_system-components.md#471-role-and-gateway-rpc-contract), which states for each rule whatever answer it fixes; [Runtime Adapter Specification](https://github.com/lennylabs/lenny/blob/main/spec/15_external-api-surface.md#154-runtime-adapter-specification) states what conformance against those rules means. An adapter author reads both. A runtime binary issues none of the requests those rules govern, which is why this page states the teardown behaviour and leaves the rules where they are stated.
```

DOCS-2 lands beside DOCS-1, after SPEC-1, SPEC-3 and SPEC-5 have landed the contract it mirrors.
Its tier-11 work is specified under `## Testing`.

### DOCS-3 · docs/reference/error-catalog.md · the `SETUP_COMMAND_FAILED` row takes four sentence replacements and a replaced remedy cell

A started-session refusal reaches the client under the `SETUP_COMMAND_FAILED` envelope only
where it arrives at the request that runs the session setup commands, which is the stage whose
deterministic `FailedPrecondition` failure §15.1 already maps to this code. A refusal arriving at
any other bind-sequence request reaches the client under the envelope that stage already selects.
SPEC-5 replaces the cause, retryability, setup-output and exclusion sentences of the §15.1 row
so they no longer state a cause the refusal does not have and no longer leave the refusal's own
envelope unstated. The published catalog at `docs/reference/error-catalog.md:129` states the same
row for readers who do not have the specification. Nothing holds the two to one text: no file
under `tests/`, `scripts/` or `cmd/`, and no `Makefile` target, names
`docs/reference/error-catalog.md`, so the page drifts from §15.1 silently and this deliverable
is the only thing that moves it.

This deliverable makes one sentence replacement for each of SPEC-5's §15.1 replacements, plus
one remedy-cell replacement, in the reference page's own column set. The page's prose names the
pod slot rather than the registry entry, states its exclusion by naming its causes rather than
by naming gRPC codes, and carries no specification section number, because the reader is a REST
client who has none of those terms.

The description cell's opening sentence, which reads "A session setup command exited non-zero
(or hit its hard timeout), which the runtime adapter reports as a deterministic failure.",
becomes:

```
The runtime adapter answered the request that runs the session setup commands with a deterministic failure: either a setup command exited non-zero or hit its hard timeout, or the request was refused because the pod slot it reached already carried a started session for the same session identifier.
```

The description cell's retryability sentence, which reads "Not retryable: the command fails
identically until the workspace plan or setup script changes.", becomes:

```
Not retryable: a setup command fails identically until the workspace plan or setup script changes, and a refusal of that request means another start of the same session already holds the pod slot.
```

The description cell's `details.reason` sentence, which reads "`details.reason` is
`setup_command_failed`; the per-command stdout and stderr are retrievable via
`GET /v1/sessions/{id}/setup-output`.", becomes:

```
`details.reason` is `setup_command_failed`. Where a setup command ran, its per-command stdout and stderr are retrievable via `GET /v1/sessions/{id}/setup-output`; a refused request runs no setup command and produces no such output.
```

The description cell's closing sentence, which reads "A non-deterministic setup-window failure
(a crashed pod or a transport timeout) instead surfaces as the retryable
`SESSION_CREATION_FAILED`, `STARTING_FAILED`, or `RESUME_FAILED`.", becomes:

```
A non-deterministic setup-window failure (a crashed pod or a transport timeout), and a request refused because the adapter's entry for the session belongs to a different bind attempt, surface instead as the retryable `SESSION_CREATION_FAILED`, `STARTING_FAILED`, or `RESUME_FAILED`.
```

The sentence enumerates the causes a REST client can see under this code rather than
quantifying over the bind sequence, because the superseded refusal is a deterministic refusal
that is retryable, and because a started-session refusal that arrives at a request other than
the one running the setup commands reaches a different envelope. SPEC-5's re-keyed §15.1
exclusion sentence also sends that refusal to the envelope its own stage selects. The page
omits that class because a client reaches this row only through the request that runs the setup
commands, so the refusal at another bind-sequence request is never visible under this code and
the reader has no name for the requests it would have to be keyed on.

The remedy cell, which reads "Inspect the setup-command output, correct the workspace plan or
setup script, and create a new session.", is replaced whole, because the clause has to land
inside the cell's terminating period rather than after it:

```
Inspect the setup-command output, correct the workspace plan or setup script, and create a new session. Where the setup-command request was refused, no setup command ran: read the session's state with `GET /v1/sessions/{id}` rather than retrying the start, because another start of the same session already holds the pod slot.
```

No new row is added. The two adapter error codes SCHEMA-1 adds are gateway-to-adapter codes
that the gateway maps into this existing envelope and into the retryable slot-failure envelope,
so neither appears in the client-facing catalog.

DOCS-3 adds no gate and declares no tier above 0. There is no shipped reconciliation between
§15.1's catalog and this page to extend, and a gate built for the one row this deliverable
touches would leave every other row of the page ungated. The absent reconciliation is a defect
of the page rather than of this change, and it goes out as its own finding against §15.1.
Tiers: 0.

### DOCS-4 · docs/reference/execution-modes.md, docs/operator-guide/security-principles.md · the per-slot cleanup sentence on each page loses its reporting clause

Each page carries one sentence stating that the per-slot cleanup runs at each session release
in session mode, on a pod of any concurrency and any recycle setting, ending ", and the adapter
reports its outcome to the gateway". That clause is the universal SPEC-3 withdraws, whose carrier
table assigns both sites here. On each page, delete the clause so the sentence ends at
"any recycle setting". The residual-state tables and `docs/operator-guide/multi-tenancy.md` state
the cleanup alone and are unedited. No gate reads either sentence:
`TestPerSlotCleanupStatedOnEverySessionModeRow` pins the residual-state table rows. DOCS-4 lands
beside DOCS-2, after SPEC-3. Tiers: 0, 11.

## Testing

Every test carries a `// spec:` annotation naming the sections it exercises, in heading form
(`// spec: §4.7.1 (role and gateway RPC contract)`) and never as a line number, per
`channel-naming.md` N8. Every tier-2 and higher test carries a `// diagnosis:` comment above its
function declaration. Every deliverable runs tier 0 and tier 1 on every package it touches; the
tiers named per section below are the additional ones. Every new file registers in
`tests/spec-map.json` under the sections its annotations cite, and every deliverable that
changes the wire or adds a normative claim adds its `tests/claim-map.json` rows by editing the
`EXPLICIT` list in `scripts/seed-claim-register.py` and regenerating from it in the same commit.

### Adapter tests for CODE-6, tier 1

New file `pkg/adapter/bindattempt_test.go` (package `adapter`). Every case must fail against the
tree before this amendment and pass after. Each carries `// spec: §4.7.1 (role and gateway RPC
contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)`.

The rule 2-through-7 predicate is the subject, table-driven over the rule it exercises:

- **The mid-session-create rule.** A resolve with `allowCreate` false against an absent entry
  answers `FailedPrecondition`, creates no registry entry, and leaves the filesystem untouched.
  The filesystem assertion is the one that matters: `ensureSlotPaths` creates the tree, so a
  rule that refuses after creating would pass a registry-only check.
- **The create-and-stamp rule.** A create stamps the caller's token, and the entry carries
  exactly that value afterwards. A create with an empty token stamps the empty string, which is
  the state the fail-closed `Shutdown` row and the untokened-entry counter exist for.
- **The create arm refuses a malformed slot identifier before it inserts anything.** A resolve
  with `allowCreate` true for each of `.`, `..`, `a/b`, `a\b`, `a\x00b`, `./a` and `a/` is
  refused, the registry holds no entry for it afterwards, and no directory exists on disk for it.
  The values are the ones `malformedSessionAddresses` (`pkg/adapter/session_test.go:284`) already
  covers, restated here rather than referenced, because that variable is declared in package
  `adapter_test` and this file is in package `adapter`. This is the guard
  `TestStartSessionRejectsAMalformedSessionAddress_spec_5_2` pins today, and it fails against a
  create arm that drops `resolveSlotPaths`.
- **A tree-creation failure inserts nothing.** With a regular file planted at the slot's own
  root under the workspace `slots` directory so `slotlayout.EnsureTree`'s first `os.MkdirAll`
  answers `ENOTDIR`, a resolve with `allowCreate` true returns that error and `s.slots` is
  unchanged, so no entry stands for a slot no directory backs. The failure is forced by planting
  a file where a directory is required rather than by a mode change, so the case answers the same
  way for every UID the suite runs as; `pkg/adapter/podscrub_test.go:747-749` drives a read
  failure the same way.
- **The attempt identity rule.** A resolve carrying B against an entry stamped A is refused, the
  error satisfies the superseded sentinel's code and detail, and the entry's token is unchanged
  afterwards.
- **The started-session rule.** A resolve with `allowStarted` false against a started entry is
  refused with the already-started code; the same resolve with `allowStarted` true is admitted.
- **The admit rule, both arms, and the stamp-once rule.** A resolve carrying B against an entry
  carrying no token is admitted and **does not stamp B**; a resolve carrying no token against an
  entry stamped A is admitted and leaves A. This is the case that fails against an
  implementation that stamps on resolve, which is the most likely wrong turn.
- **Rule 5 evaluated ahead of rule 6.** An entry that is both started and stamped A, resolved with B and
  `allowStarted` false, answers the superseded code rather than the already-started one, because
  the attempt identity rule precedes the started-session rule and the caller's correct response
  differs: superseded is retryable and already-started is not.

Then the thread and the surrounding mechanism:

- **All three production callers are gated.** Table-driven over `ensureSlotPaths`,
  `assignCredentialsSlot` and `claimSessionSlotUnderLock`, each with a differing token against a
  stamped entry, asserting the refusal at each. The `claimSessionSlotUnderLock` row is the one
  the handler-placement design failed, so it is written first.
- **The claim's `!idempotentRepeat` refusal is typed.** `claimSessionSlotUnderLock` against a
  started entry with `idempotentRepeat` false answers the already-started code rather than the
  shipped untyped `Unavailable`; with `idempotentRepeat` true it answers a satisfied claim and
  no error, unchanged.
- **`FinalizeWorkspace` reads `mid_session` before it resolves.** A mid-session finalize for a
  session the pod holds no entry for answers `FailedPrecondition` and leaves the registry and the
  filesystem unchanged. Against the tree today it creates an entry and a full tree, so the case
  is a regression guard on the read ordering rather than a property test.
- **`PrepareWorkspace` latches the first frame's assertion and reads no later frame.** A stream
  whose second frame carries a different non-empty token, and one whose second frame raises
  `mid_session` to true where the first frame's was false, are each admitted, extend the bytes
  the first frame staged, leave the entry's stamp as the first frame set it, and create no
  second entry. A multi-chunk mid-session upload whose later frames carry the proto3 default for
  both fields is admitted and staged in full, which is the regression guard for the ordinary
  §7.4 upload larger than one chunk.
- **The wire rule's two directions.** A non-mid-session request with an empty token and a
  mid-session request with a non-empty one are each `InvalidArgument`, across
  `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials` and `Resume`. After
  each refusal the registry holds no entry for the slot identifier and no tree exists on disk
  for it, which is what pins `validateBindFields` ahead of the create rule.
- **The hold refuses admission until the teardown returns having completed**, across the seven
  requests §4.7.1's admission rules govern, and it refuses each at the site the guard's scope
  puts it at. Park the reclaim inside `Runtime.Close` on the §10.1.4 hold termination, which is
  the one site
  CODE-6 routes that closes a runtime: `releaseSessionSlot` closes none
  (`pkg/adapter/slotsession.go:214-220`), and `Shutdown` is routed through the helper only once
  CODE-1 lands. Drive every admission entry point. `PrepareWorkspace`, `FinalizeWorkspace`,
  `RunSetup` and `Resume` are refused at `acquireSlotGuardForResolve`, holding no guard;
  `AssignCredentials`, `StartSession` and `ConfigureWorkspace` take no guard and are refused
  inside `ensureSlotStateLocked`. Assert each refusal carries the sentinel and that every
  guarded entry point returns while the park still holds, which an implementation that
  blocks the caller on the guard and refuses it only after the cleanup returns cannot do.
  Release the park on a cleanup that completes, and assert each entry point then admits. That is
  the arm on which the release fires; the two cases below are the arms on which it does not.
- **Every deregister-then-destroy site takes the hold**, table-driven over `releaseSessionSlot`
  and the §10.1.4 hold termination, with the `Shutdown` row added when CODE-1 routes that
  handler through `reclaimSlotLocked`. This is the case that turns red if a later change adds a
  deregister-then-destroy site without routing it through `reclaimSlotLocked`.
- **A panic out of the destructive section is a cleanup that did not complete.** Drive
  `Runtime.Close` to panic, recover it at the test boundary, and assert a later bind naming the
  same session is refused with the reclaim-hold sentinel, because §5.2 keeps the identifier held
  for the life of the pod when the cleanup does not complete. This is the arm that turns red if
  the deferred release ignores the cleanup's outcome, and the `defer` is what puts the panic arm
  under the same single release site as the ordinary returns.
- **A cleanup whose tree removal fails keeps the hold**, table-driven over `releaseSessionSlot`
  and the §10.1.4 `terminateHeldSession` with `Runtime.Close` returning nil, with the `Shutdown`
  row added when CODE-1 routes that handler through `reclaimSlotLocked`. Drive
  `Server.removeSlotTreeFn`, the seam CODE-6's tree-removal bullet states, to return an error,
  and assert that a later bind naming the same session is refused with the reclaim-hold
  sentinel. This is the arm that turns red if the release is taken unconditionally at
  `releaseSessionSlot`, or is keyed on `closeErr` alone at `terminateHeldSession`.
- **A cleanup whose runtime close fails keeps the hold.** Drive `Runtime.Close` to return an
  error at the §10.1.4 hold termination, through the same seam the park case above uses, and
  assert that a later bind naming that session is refused with the reclaim-hold sentinel. Assert
  also that the termination still emitted the member's final usage report, removed the slot tree
  and sent `AdapterTerminating`, because the close is best-effort in control flow and only the
  hold release is keyed on its error. This is the arm that turns red if that site's release is
  keyed on the tree removal alone.
- **The refusals stay correctly classified through all five resolve sites**, table-driven over
  `resolvePrepareStagingDir`, `FinalizeWorkspace`, `RunSetup`, `claimSessionSlotUnderLock` and
  `assignCredentialsSlot`. Each asserts that the reclaim-hold sentinel and the superseded refusal
  pass through as `codes.Aborted`, that the already-started refusal passes through as
  `codes.FailedPrecondition`, and that a non-sentinel resolve failure still wraps as
  `codes.InvalidArgument`. The three rows whose caller stamps a span category additionally assert
  the recorded span's `error.category` attribute: `TRANSIENT` on the two transient arms,
  `PERMANENT` on the already-started arm and the non-sentinel arm. The span is read through the
  `installInternalSpanRecorder` and `endedSpanNamed` helpers the package already ships
  (`pkg/adapter/tracing_internal_test.go:23,:33`). This case carries `// spec: §16.3 (distributed
  tracing)` beside the section's own annotation, and it is what turns red when an implementor
  routes the errors through `slotResolveError` and leaves the literal
  `tracing.CategorizeError(err, tracing.CategoryPermanent)` stamps in place
  (`pkg/adapter/staging.go:81,:183,:339`).
- **The per-slot guard serializes the destructive section.** Two `FinalizeWorkspace` calls for one
  slot identifier, admitted by the same attempt's token, do not interleave their materialization,
  and neither observes a partially built tree. The lock-order assertion is the tier-7a case below.
- **The token is never in a message.** Every refusal's message and every log line the resolve
  path emits is asserted not to contain the token value, because the token is a capability over a
  live session's teardown.
- **A destructive section whose guard acquisition expires takes CODE-14's disposition of an
  expired acquisition at a removing site.** With a second goroutine holding the slot's guard and
  the caller's context already cancelled, `lockSlotGuard` returns a no-op release and `false`.
  Table-driven over the three rows of that disposition's table, `Shutdown`'s removing arm, the
  guard-acquiring `releaseSessionSlot` and `terminateHeldSession`. Each row asserts the removal
  its cell states: that the entry and its tree are removed, that the call returns without waiting
  for the parked holder, and that the `slot_guard_not_acquired` warning names the slot identifier
  and that caller through the structured-log seam, asserted on the event name and its fields
  rather than the message text. Each row also asserts the hold and report that the §5.2
  failed-act rows give it: that a later bind naming the same session is refused with the reclaim-hold
  sentinel after the parked holder releases, because the hold is retained, and, on the `Shutdown`
  row against a slot that did not reach `running`, that the response carries `slot_reclaim:
  reclaimed` with `exited_cleanly` false. This turns red against an implementation that abandons
  the removal on an expired acquisition, against one that waits for the holder regardless of the
  context, and against one that releases the hold on an unguarded removal that returned nil.
- **An uncontended acquisition on a cancelled context holds the guard.** This case pins CODE-14's
  acquisition step. With the slot's guard free and the caller's context already cancelled, in a
  loop of at least 64 iterations, `lockSlotGuard` returns `true` and `acquireSlotGuardForResolve`
  returns a nil error. The guard-acquiring `releaseSessionSlot` on that context emits no
  `slot_guard_not_acquired` warning, and a later bind naming the same session is admitted because
  the hold was released. This turns red against an acquisition that is a bare two-case `select`.
- **An admission RPC whose guard acquisition expires is refused.** With the guard held and the
  caller's context already cancelled, `acquireSlotGuardForResolve` returns the context's own error
  for `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup` and `Resume`; no registry entry is
  created, no directory exists on disk for the slot afterwards, and nothing is resolved. This is the
  branch that stops a materialization re-creating the tree a reclaim has just removed, which is the
  residue CODE-14's guard exists to close.

**Tier 1, CODE-14's §10.1.4 per-member close budget**, in `pkg/adapter/holdstate_test.go` (package
`adapter`). Two started members are on the pod and `onHoldTimeout` fires. A fake `Runtime` records,
for each member, the `Err()` and the deadline of the context its `Close` is handed. Every member's
close context has a nil `Err()`, and each member's deadline is strictly later than the previous
member's and strictly later than the deadline of the context the pass acquires guards against, which
is what a context minted per member after the acquisition returns produces and what a single context
shared across the acquisition and every close cannot. The case therefore discriminates the
per-member design from the shared one without waiting on any clock and without parking a guard
holder; the unguarded fall-through on an expired acquisition, including its
`terminateHeldSession` row, is pinned by the destructive-expiry case above. It carries
`// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and execution modes);
§10.1.4 (coordinator-loss detection and hold state)`. The file is already a `slotAddressCaseFiles`
member and `tests/spec-map.json` already credits it under sections 5.2 and 10.1.4, so it adds no
inventory row and gains a whole-file credit under 4.7.1, recorded in the spec-map listing below.

### Adapter tests for CODE-1 and CODE-2, tier 1

Files: `pkg/adapter/slotsession_test.go` (package `adapter`),
`pkg/adapter/socketruntime_test.go`, and `pkg/adapter/sdkwarm_test.go` (package `adapter_test`)
for the SDK-warm confirmation case alone. Reuse the internal fixtures that already exist:
`slotPod`, `slotTreeProbe`, `probeRuntime`, `startRuntimeOps`, `recordingSessionScrubReporter`,
`assignOne` and `fakeExpiryClock`. No `export_test.go` change for these cases (CODE-14's
`releaseSessionSlot` signature change reaches that file separately, through
`ReleaseSlotForTest`):
`AssignCredentials` is exported and `assignCredentialsSlot` produces the bound-but-unstarted
state, and the internal package reaches `ensureSlotPaths` directly. The one new seam is
`Server.removeSlotTreeFn`, which CODE-6 states and these cases reuse; the cases sit in package `adapter`, so they set
the field on the `Server` they construct and need no exported accessor. The internal cases' one
fixture extension is a nil-safe `onStart func(sessionID string)` hook on `probeRuntime`, run inside
that double's own `Start` (`pkg/adapter/slotsession_test.go:33-38`).

The SDK-warm confirmation case sits in the external file because the SDK-warm double and its
request builder are declared there (`pkg/adapter/sdkwarm_test.go:37,:71,:78`, package
`adapter_test`, as is every other SDK-warm double in the package,
`pkg/adapter/shutdown_demote_test.go:3`), and because every property that case asserts is
reachable through exported surface: `Server.SDKWarmReady` (`pkg/adapter/sdkwarm.go:177`), the
exported `Server.SessionScrubReporter` field (`pkg/adapter/server.go:177`),
`Server.AssignCredentials` and `Server.Shutdown`. Its one fixture extension is a nil-safe
`onConfigure func()` hook on `fakeSDKWarmRuntime`, run inside that double's own
`ConfigureWorkspace` (`pkg/adapter/sdkwarm_test.go:55-61`). The hook is a deterministic seam
rather than a race: `claimSessionSlot` releases `s.mu` before it returns
(`pkg/adapter/slotsession.go:52-59`) and the handler calls `sw.ConfigureWorkspace` outside the
lock with the confirmation after it (`pkg/adapter/sdkwarm.go:249-262`), so a re-entrant exported
RPC issued from the hook lands between the claim and the confirmation on the calling goroutine,
with no parking and no second goroutine. It stays deterministic under CODE-14's guard: the
derivation table leaves `ConfigureWorkspace` unguarded, and the hook's unconditional `Shutdown`
takes the slot guard on its removing arm with nothing holding it.

Cases, each `// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and
execution modes)`:

- **The two-field precondition, four rows.** A `Shutdown` carrying only a non-empty token, and
  one carrying only `unconditional_teardown`, are each admitted; one carrying neither and one
  carrying both are each `InvalidArgument` and remove nothing. The last two are the fail-closed
  rows and are written first. Those two rows carry a `RecycleScrub` disposition as well, and
  each asserts that no whole-pod scrub is started and no `ReportPodScrub` is filed, so the
  scrub-done signal is never raised. Drive them through the same `startRuntimeOps` and scrub
  fixtures the two recycle-scrub cases below use. What the assertion pins is what the staged
  §4.1 scrub sentence and §4.7.1 rule 10 give, and it turns red against a handler that starts
  the scrub from a `defer` or from above the precondition.
- **`Shutdown`'s teardown cascade, one case per arm of `shutdownReclaimOutcome`** (rule 11's
  no-entry arm on each request form, rule 12, rule 13's entry-carries-no-token and
  differing-token arms, and rule 14), asserting the outcome, whether the entry was removed, and
  on every non-removing arm that the entry, its `current` directory and its `credentials.json`
  all survive the call.
- **The untokened-entry arm fires its counter.** A `Shutdown` naming a token against an entry
  carrying none answers `superseded`, removes nothing, and increments
  `lenny_slot_shutdown_untokened_entry_total`.
- **The unconditional teardown is still the unconditional teardown.** This is the regression guard
  on the form every non-compensating caller uses, and it must stay green throughout the
  implementation rather than being written last.
- **Registered but unbound.** `Shutdown` for an entry created by a workspace RPC and never bound
  removes the entry, and removes the slot tree and `/run/lenny/slots/{sessionId}`, which
  `slotlayout.RemoveTree` reaches. The tree removal is the new property. The no-`Close`,
  no-signal and no-`ReportSessionScrub` assertions are regression guards on behaviour the old
  `bound` gate already gave.
- **Bound but unstarted.** The entry, the credential file and the tree are all gone; no
  `Runtime.Close`; no `ReportSessionScrub`; **no FINAL_USAGE_REPORT**, which is new, because
  `emitFinalUsage` moves from the `bound` branch to the `started` branch; and **no `terminate`
  frame on CH-RUNTIMEOPS**, which is new for the same reason. Attach `startRuntimeOps` and leave
  no other bound entry on the pod, because with a bound co-tenant remaining `boundRemains`
  withholds the frame today as well and the assertion would not discriminate. Read the absence
  with the bounded read `pkg/adapter/slotsession_test.go:210` already uses. The expiry-timer
  assertion is kept as a guard that `deregisterSlotLocked`'s unconditional cancellation has not
  moved under the `started` branch.
- **Bound and started.** The fixture drives the start through `noteRuntimeStarted`, so the
  session is in `runtimeLive`, which is what the cleanup-outcome assertion turns on. Today's full
  teardown is unchanged: the signal when no bound entry remains, the close, the tree removal and
  the cleanup-outcome report all still run, in that order.
- **Claimed but not yet recorded.** A reclaim of a session whose `st.started` is true and which
  `runtimeLive` does not hold runs the runtime teardown, removes the entry and the tree, and
  files no cleanup-outcome report. This is the start still in flight, and it is the case that
  pins the two predicates apart.
- **Co-tenancy hazard.** Drive `SocketRuntimeProcess` into `connected == true` with an empty
  active set through `Interrupt` of the last active session, then assert that `Shutdown` of a
  bound-but-unstarted entry leaves the connection, the spawned child and the listener intact. Add
  the sibling assertion in `socketruntime_test.go` beside
  `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`.
- **Start-versus-reclaim rollback, deterministic form.** One table-driven case with a row per
  non-SDK confirmation site, named for the handler it drives: `StartSession`
  (`pkg/adapter/session.go:163`) and `Resume` (`pkg/adapter/resume.go:144`). Every row asserts
  the same set, because CODE-2 stages the same rollback body at both sites:
  `noteRuntimeStarted` returns false for a session whose entry was removed, and the rollback
  closes the runtime, cancels a pod MCP surface no surviving session holds, leaves the registry
  untouched, and answers `Aborted`. Neither the rollback nor the removal files a
  `ReportSessionScrub` for that session. Each row removes the claimed entry from the
  `probeRuntime` `onStart` hook the fixture paragraph above states, run on the calling goroutine,
  through `ReleaseSlotForTest`. The rows differ only in the context that removal carries. The
  `StartSession` row passes `t.Context()`, because `StartSession` holds no per-slot guard and the
  acquisition completes. The `Resume` row passes an already-cancelled context, so the acquisition
  expires at once and the removal takes CODE-14's disposition of an expired acquisition at a
  removing site, which is the only ordering production admits: CODE-14's derivation table holds
  `Resume`'s guard from ahead of its `claimSessionSlot` to the end of the call, so a `Shutdown`
  issued from a second goroutine blocks on that guard rather than removing. Under that
  disposition the row's removal retains the hold; the row asserts the rollback alone and creates
  no successor. The `Resume` row is driven as a conversation-only
  resume, carrying a session identifier and a checkpoint identifier and no chunks, so
  `restoreChunks` returns on its empty-set guard (`pkg/adapter/resume.go:169-172`) and the row
  reaches `Runtime.Start` with no extraction.
- **The confirmation refuses a replaced entry.** `noteRuntimeStarted` called with the token of a
  claim whose entry a reclaim removed and a successor re-created returns false. This passes
  against a predicate reading only `st.sessionID`, which is why it is written explicitly.
- **The rollback destroys no successor.** Drive the reclaim so the entry and its tree are gone,
  then put a successor under the same slot identifier before releasing the parked start, in two
  sub-cases. The unbound sub-case creates the successor through `ensureSlotPaths` alone; the
  confirmation still refuses, the rollback runs, and the assertion is that the successor's entry
  and its per-slot cwd survive it. The bound sub-case runs `AssignCredentials` and then
  `claimSessionSlot` for the successor; the confirmation is satisfied by that entry, no rollback
  runs, and the assertion is that the entry, the cwd and the credential file all survive.
- **The SDK-warm confirmation refuses without releasing.** In `pkg/adapter/sdkwarm_test.go`,
  carrying `// spec: §4.7.1 (role and gateway RPC contract); §6.1 (SDK-warm pre-connect); §7.1
  (normal flow)` rather than the subsection's own annotation, because the arm it pins is the
  §6.1 demotion half. Drive a fresh `ConfigureWorkspace` to a successful claim and remove the
  claimed entry from inside the double's `onConfigure` hook with an unconditional `Shutdown`, so
  the confirmation runs against a registry the entry has left. Assert that the RPC answers
  `codes.Aborted` rather than the `codes.Internal` the handler's other failure arms answer
  (`pkg/adapter/sdkwarm.go:236,:241,:251`); that the double's `demoted` counter records the
  `SDKWarmRuntime.DemoteSDK` call and `Server.SDKWarmReady` is false afterwards, which is what
  discriminates a refusal written as a bare `Runtime.Close`; and that a recorder installed on
  `Server.SessionScrubReporter` records no call for that session. The successor sub-case has the
  hook re-create an entry under the same slot identifier with `AssignCredentials` after the
  `Shutdown`, and asserts that a later `Shutdown` naming the successor answers `reclaimed` rather
  than `absent` and that the successor's `current` directory and its `credentials.json` are still
  on disk. The successor sub-case is what fails against a refusal arm routed through
  `releaseSessionSlot`, which deregisters the entry and `RemoveAll`s the tree
  (`pkg/adapter/slotsession.go:214-220`); the code assertion is what fails against an arm
  answering `codes.Internal`. This case is the site's only coverage: its only gateway caller is
  `Binder.Launch`, which no compensation races, so the tier-7a rendezvous carries no arm for it.
- **`exited_cleanly` on a reclaim the runtime never held.** A `Shutdown` naming the entry's own
  token against a bound-but-unstarted entry whose `Server.removeSlotTreeFn` returns an error
  answers `slot_reclaim: reclaimed` with `exited_cleanly` false. This is the one arm CODE-15's
  disjunct newly makes false, and the staged §5.2 and §7.1 text keys the `leaked` disposition on
  it.
- **An expired guard acquisition keeps the hold and fails the clean exit.** A `Shutdown` naming
  the entry's own token against a bound-but-unstarted entry, sent on an already-cancelled context
  while a second goroutine holds the slot's guard, answers `slot_reclaim: reclaimed` with
  `exited_cleanly` false, removes the entry and its tree, and leaves a later bind naming the
  session refused with the reclaim-hold sentinel after the holder releases. The same request
  against a session in `runtimeLive` answers `exited_cleanly` on the runtime close alone and
  files `released`, and still leaves the later bind refused. Both rows drive CODE-14's disposition of an
  expired acquisition at `Shutdown`'s removing arm, and assert the hold and report that the §5.2
  failed-act rows give it; this pair pins the response and the hold apart on the `running`
  boundary.
- **The runtime's own answer still decides for a started session.** The same injected tree-removal
  error, against a session the fixture has driven through `noteRuntimeStarted` so it is in
  `runtimeLive`, answers `exited_cleanly` true. This is what pins the `live ||` half of the
  disjunct, so a predicate written as `closeErr == nil && treeErr == nil` fails here. Assert also
  that a later bind naming the session is refused with the reclaim-hold sentinel and that the
  cleanup-outcome report carries `released`, which pins the hold and the report apart: the case
  turns red equally if the report is keyed on both errors, which would file a leak against
  occupancy the same response frees.
- **The recycle scrub runs on the absent arm.** A `Shutdown` carrying `unconditional_teardown`
  and a `RecycleScrub` for a session the adapter holds no entry for answers `absent`, removes
  nothing, and still starts the whole-pod scrub. Drive it through the existing `startRuntimeOps`
  and scrub fixtures and assert the scrub-done signal. This is the arm `Binder.ReleaseSlot`
  reaches, its separate unconditional `Shutdown` having already torn the last slot down.
- **The recycle scrub runs on the removing arm.** A `Shutdown` carrying `unconditional_teardown`
  and a `RecycleScrub` against an entry the adapter still holds answers `reclaimed`, removes the
  entry, and still starts the whole-pod scrub. Drive it through the same `startRuntimeOps` and
  scrub fixtures and assert the scrub-done signal alongside the removal. This is the arm every
  session-mode recycling pool reaches, `Binder.Release`'s recycle request being the session's
  only teardown, and it is the arm on which the scrub overlaps the slot guard and the reclaim
  hold. `tests/tier4_integration/recycle_scrub_path_test.go`'s
  `TestRecyclePathScrubReportedReuses_spec_5_2` (`:404`, driving `binder.Release` at `:429`) is
  the integration-level witness that pins this arm end to end, so this case is the tier-1 half
  of a property the suite otherwise only observes through a full pod recycle. The absent arm, the
  removing arm and the precondition rows above together pin clause three across the outcomes a
  `Shutdown` answers and across the requests it refuses, rather than on one arm.
- **The clean arms answer true.** A bound-but-unstarted reclaim whose tree removal succeeds, a
  `superseded` answer and an `absent` answer each report `exited_cleanly` true, the last two
  because a teardown rule that removes no entry reports a clean exit.

Scope accounting to record in the deliverable: CODE-1's two-field precondition and CODE-6's
rule 1 are mandatory-field changes on shipped RPCs, and each carries the test-literal sweep that
its `Every other file that grep ... names` entry under `## Files touched on application
(non-spec)` defines. A skipped sweep fails silently: the added fields are proto scalars with
zero values, so nothing fails to compile, and the swept tiers go red together with no build
error to point at. The `ShutdownRequest` sweep reaches tiers 1, 2, 3, 4, 7a, 9 and 10, and the
bind-field sweep reaches tiers 1, 3, 4, 7a, 8, 9 and 10. A test that reaches these RPCs through
an `adapterclient.Client` method builds no literal the grep sees. The signature change that
CODE-4's `client.go` target states carries it, because the test fails to compile until it is
edited, and the Tests list under `## Files touched on application (non-spec)` names those files.
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2` is one member of the
`ShutdownRequest` set whose own path is otherwise unaffected and must keep passing
unchanged. CODE-2 changes
the fixtures of the two shipped adapter tests named in its call-site scope, both in
`adapterevents_test.go`; only `TestAdapterEventsEmitsControlEvents_spec_4_7` goes red without
that change.


### Tier-1 adoption-ordering tests, every case walked

New file `pkg/adapter/bindattempt_orderings_test.go` (package `adapter`). `Shutdown` performs
only the empty-session-id check before `s.mu.Lock()`, so an internal adapter test holding `s.mu`
parks a compensation at the barrier with no production seam. The existing examples are
`pkg/adapter/holdstate_test.go:350,438,781` and `pkg/adapter/one_session_only_test.go:57,116,150`.
Each case carries `// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow)`, and the
set covers one adoption ordering each:

| Case | Asserted outcome |
|:--|:--|
| Defect 17: attempt 1 fails pre-start, its entry survives, attempt 2 retries | attempt 2's first RPC is refused on the identity gate before it touches anything; attempt 1's compensation matches and removes the entry; attempt 3 creates a fresh one |
| Attempt 1's `StartSession` admitted, response lost | the entry holds attempt 1's token, the compensation matches, the orphan is torn down |
| **A resume onto a leaked entry stamped by an earlier attempt** | the claim resolves through `ensureSlotStateLocked`, which compares before `st.started` is written, so the resume is refused `Aborted`; the earlier attempt's stale compensation matches and removes the leaked entry alone |
| **That resume's own lost-response compensation** | the resume created and started nothing, so its compensation answers `superseded` and removes nothing; no orphan runtime and `Leaked` correctly false |
| The symmetric `StartSession` race | attempt 1 creates the entry, attempt 2's first RPC is refused and never reaches `StartSession` |
| Mid-session finalize after an unconditional `Shutdown` removed the entry | `allowCreate` false, absent entry is `FailedPrecondition`, no unstamped entry created |
| Attempt A's own `FinalizeWorkspace` after an unconditional `Shutdown` removed A's entry | A recreates its own entry stamped A and materializes from an empty staging tree. This pins an accepted residue rather than a refusal, and its comment names the accepted-failure-mode bullet it records |
| Attempt 2 against a live started session | refused on the identity gate, which the cascade evaluates ahead of the phase gate; no workspace, credential, setup or staging damage, and its compensation, naming attempt 2's token, answers `superseded` under §4.7.1 rule 13 and removes nothing |
| Resume onto a replacement pod | the resume mints, its claim stamps the entry it creates, its compensation matches; a stale compensation from an earlier attempt answers `superseded` |
| §7.4 mid-session upload onto a live session | empty token and `mid_session` true, admitted by the admit rule, barred from creating by the mid-session-create rule, exempt from the phase gate |
| Adapter process restart between the attempt and its compensation | a fresh process holds no entry the stale compensation can match, so it answers `absent`, and any entry a later attempt creates carries that attempt's own token, so it answers `superseded` |
| A compensation lost to a gateway crash | the entry stands stamped with a dead token and every later attempt at that session on that pod is refused. This pins the residue, and its comment names the recovery that is out of scope |

The third and fourth rows are the ones a handler-placement design fails, so they are written
first and their comments say so.

### Gateway tests for CODE-4, CODE-5, CODE-7, CODE-8, CODE-9 and CODE-13, tier 1

Files: `pkg/gateway/runtime/adapterclient/client_test.go`,
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
`pkg/gateway/podlifecycle/podsession/binder_test.go`,
`pkg/gateway/sessionserver/slotretry_test.go`,
`pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`, and
`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go`.

Fixture work is part of the deliverable rather than an assumption: `concurrentAdapter` discards
its `Shutdown` request, injects failures only at `StartSession` and at `Shutdown`, and serves
neither `PrepareWorkspace` nor `AssignCredentials`. It gains per-stage error injection for the
finalize, setup and credential-assignment stages, handlers for `PrepareWorkspace` and
`AssignCredentials`, the ability to answer a stage with a typed refusal detail, and the
per-request recording `recordingShutdownAdapter` carries today
(`pkg/gateway/podlifecycle/podsession/binder_test.go:1156-1173`), so one fake drives the table.

CODE-7's cases, each `// spec: §4.7.1 (role and gateway RPC contract); §15.4 (runtime adapter
specification)`:

- **Each refusal code translates to its sentinel.** A stage answering a status whose detail
  carries `ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED` returns an error satisfying
  `errors.Is(err, ErrSlotBindAttemptSuperseded)`, and the already-started code its own sentinel.
- **The original status survives the wrap.** `status.Code(err)` on the translated superseded
  error is `codes.Aborted` and on the already-started error `codes.FailedPrecondition`, so
  CODE-5's arm and CODE-4's guard fire on one value.
- **Everything else passes through unchanged**, including a status carrying an
  `adapterv1.Error` detail with any other code, a status with no detail, and a non-status error.
- **Every translating call site translates.** Table-driven over the seven `Client` methods, each
  asserting the sentinel surfaces; and the three `Shutdown` forms asserting they do not
  translate, because the adapter answers the refusals there as outcomes.

CODE-4, CODE-5, CODE-8, CODE-9 and CODE-13's cases, each `// spec: §7.1 (normal flow); §5.2 (pool configuration and
execution modes); §6.2 (pod state machine)`:

- **The mint is per attempt and reaches every request.** One `materializeSlot` run carries one
  value on `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup` and `AssignCredentials`, and two
  runs carry two different values. `Binder.Prepare` and `Binder.Resume` each mint their own, and
  `Binder.Launch` sends an empty `bind_attempt`. Assert the value on the recorded requests rather
  than only that a request was recorded.
- **`mid_session` is false on every minted request** and true on both requests of the §7.4 pair
  `upload_to_session.go` sends, which carries an empty token. This is the pairing the wire rule
  fails on if either half is set independently.
- **The compensation names the attempt's own token** and arrives through `ShutdownReclaim` with
  `unconditional_teardown` false, while every other teardown caller sends
  `unconditional_teardown` true with an empty token. Table-driven over `Binder.ReleaseSlot`, its
  recycle arm, `Binder.shutdownAdapter` and its recycle arm.
- **The leaked disposition, two arms.** A clean answer is not leaked and an unclean one is,
  whatever outcome the answer carries; an RPC error is leaked. Table-driven across `reclaimed`,
  `superseded`, `absent` and a value this build does not recognize, each paired with a clean and
  an unclean exit, asserting that the outcome never changes the disposition.
- **A typed refusal is compensated.** Two arms, one per sentinel. Each asserts that a
  `Shutdown` carrying this attempt's token is recorded, that the fake answers `superseded`, that
  `Leaked` is false, and that the session-wide `ReleaseSession` is not called. The case fails
  against any reintroduced suppression of the compensation on a refusal.
- **The credential release is scoped to the attempt**, at `materializeSlot` and at
  `Binder.Prepare`, each with a two-provider request. A failed attempt releases, by identifier,
  exactly the leases it minted before the failure, and a successor's leases for the same session
  survive it. Assert the identifiers rather than the count, and assert that the session-wide
  `ReleaseSession` is not called from this path. The arms are: the second `AssignProto` call
  fails, so exactly the first identifier is released; the adapter's `AssignCredentials` fails
  with an ordinary error; and it is refused with a typed refusal, the one stage where a refusal
  and minted leases coexist. The fake fails from its Nth call rather than on a named pool,
  because `CredentialPools` is a map with random iteration order, and it mints a distinct
  identifier per call. The `fakeAssigner` records its `Release(leaseID)` calls in their own
  field, because `released` records `ReleaseSession` alone (`binder_test.go:301` and `:322-324`)
  and an assertion on it discriminates nothing.
- **The release still runs for a non-`SlotBindError` failure**, which is the property the staged
  comment claims and the reason the call sits outside the `errors.As` guard.
- **Per-stage compensation table.** For the finalize, setup, credential-assignment and
  session-start stages: exactly one `Shutdown` naming the session, carrying a positive
  `deadlineMs` equal to half the budget the case's pool configuration produces and strictly less
  than the RPC deadline that same configuration produces.
- **The pre-`PrepareWorkspace` workspace failure, separately.** A `stageWorkspace` failure on a
  plan carrying an original `uploadFile` source, injected by leaving `Binder.Blobs` nil, returns
  before `PrepareWorkspace` is sent, so the pod holds no entry. Assert that the compensation is
  still sent, that the adapter answers `ABSENT`, and that `SlotBindError.Leaked` is false.
- **The cancelled-context case.** A failure whose caller context is already cancelled or past its
  deadline still sends the compensation. This is the residue class the compensation exists for
  and the case a naive implementation gets wrong.
- **The counters.** A compensation answering `superseded` reaches `b.SlotReclaim` with the
  outcome, the cause, `req.Pool` and the sandbox name, and the case asserts the recorded values
  rather than the increment alone, so a forwarder that drops a label value fails here. It runs
  after an attempt refused with CODE-7's `ErrSlotBindAttemptSuperseded`, recording cause
  `refusal`, and after an ordinary stage failure, recording cause `failure`. A compensation
  answering `absent` reaches the hook not at all. CODE-9's collector and its accessor are held
  by two shipped cases in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go`,
  extended in place beside their `IncSlotFailure` lines.
  `TestCredentialAndLLMProxyAndSlotMetricsEmit` gains one `IncSlotCompensationSuperseded` call
  with outcome `superseded`, cause `failure`, pool `pool-a` and pod name `sbx-1`, passed in the parameter order of the
  `SlotReclaim` hook CODE-9 declares, and asserts the exposition line
  `lenny_slot_compensation_superseded_total{cause="failure",k8s_pod_name="sbx-1",pool="pool-a"} 1`, so a
  collector registered under another name or another label set fails here.
  `TestNewMetricsEmittersNilSafe` gains the same call on a nil `*Metrics`.
- **The workspace stages are separated at the metric and nowhere else.** A bind whose
  `FinalizeWorkspace` fails records exactly one `SlotFailure` call whose `errorType` is CODE-9's
  `slotFailureWorkspaceFinalize` value, and a bind whose `stageWorkspace` fails still records
  `slotFailureWorkspacePrep`. Assert against the constants rather than string literals. The same
  case asserts that the `slotBindError` stage argument at the finalize site stays
  `slotFailureWorkspacePrep`, so `SlotBindError.Reason()` keeps classifying a finalize-stage
  `FailedPrecondition` as transient and no failure class becomes non-retryable. The driver is the
  finalize-stage error injection `concurrentAdapter` gains above.
- **CODE-8's short-circuit, both closures.** Each closure reads the refusal through its `cause`
  parameter, which the call site passes as the error it is about to return. `Binder.Prepare`
  meets a typed refusal from `FinalizeWorkspace`, `RunSetup` or `AssignCredentials`, and
  `Binder.Launch` meets a `StartSession` refused with `SLOT_BIND_ALREADY_STARTED`, which is the
  only refusal reachable there because `StartSession` resolves with `allowStarted` false and
  carries no token. Both return the refusal and call neither `failPhase` nor `drain`; the same
  closure meeting an ordinary failure drains as it does today. Assert the drain's absence on the
  fake binder directly, and assert that `ReleaseSession` is called on neither refusal arm, so a
  started session's §4.9 leases and its credential `active` count survive the refusal. Two concurrent
  `Binder.Prepare` attempts for one session, the loser refused, leave the winner's pod undrained,
  which is the production case.
- **Accounting, at `maxConcurrentSessions: 4` (threshold 2).** An unacknowledged compensation
  takes `MarkLeaked`, the leak gauge and `RecordLeak`, and calls `ReleaseSlotReservation` with
  `leaked=true`; a compensated failure takes the windowed `RecordFailure` and `leaked=false`; one
  leak does not drain at threshold 2 while two do; a windowed failure ages out of the five-minute
  window while a leak persists. Assert the discriminator directly rather than through the drain.
- **One `maxConcurrentSessions: 2` case**, pinning that a single failure of either kind drains,
  labelled as the shipped threshold's behaviour rather than as evidence of the new disposition.
- **The reserved bind path reaches the accounting, at `maxConcurrentSessions: 4`.** A
  post-connect failure whose compensation was acknowledged and whose `ReleaseSlotReservation`
  succeeded reaches `accountSlotFailure` with `sbe.Leaked` false and takes the windowed
  `RecordFailure`. One whose own release returns an error has `BindReservedSlot` set `sbe.Leaked`
  true and takes `MarkLeaked`, the leak gauge and `RecordLeak`. A connect-stage failure sends no
  compensation, and when its own reservation release errors it takes the same leaked arm.
- **The resume path.** A failed `Resume` sends the compensation naming its own minted token on
  the still-open connection, releases the resume slot with the outcome, and releases no
  gateway-side credential lease: the assertion is on the `fakeAssigner` field recording the
  attempt-scoped `Release(leaseID)` calls this deliverable adds, which stays empty, because
  `released` records `ReleaseSession` alone (`binder_test.go:301,:322`) and `Binder.Resume`
  calls that on no path, so an assertion on it discriminates nothing. At
  `maxConcurrentSessions: 4`, an unacknowledged reclaim reaches `MarkLeaked`, the leak gauge and
  `RecordLeak` through `resumeOnPod`'s accounting call and releases with `leaked=true`; a cleanly
  reclaimed one takes the windowed `RecordFailure`; and one whose compensation was acknowledged
  but whose `ReleaseSlotReservation` returns an error takes the leaked arm. On an exclusive pool
  the resume reserved no slot, so neither arm runs.
- **The refusals' classifications.** Rows added to the tables
  `pkg/gateway/sessionserver/slotretry_test.go` already carries. In
  `TestSlotBindErrorReason_spec_5_2` (`:383-407`), `{"session_start", codes.Aborted,
  podsession.SlotReasonTransient}` and a workspace-stage `codes.Aborted` row, placed beside the
  existing `{"session_start", codes.PermissionDenied, podsession.SlotReasonPolicyRejection}` row;
  and a `{"session_start", codes.FailedPrecondition, podsession.SlotReasonPolicyRejection}` row
  pinning that the phase gate is correctly non-retryable. In
  `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2` (`:468-492`), a `session_start`
  and a workspace-stage `codes.Aborted` passing through unclassified. The subject is the coupling
  between CODE-2's rollback code, CODE-6's three refusals and the shipped classifier.
- **The refusals' client envelopes.** Two cases written beside the shipped
  `TestClassifiedSlotFailureKeepsSetupCommandEnvelope_spec_7_3` in
  `pkg/gateway/sessionserver/slotretry_test.go` (`:519-548`), which is the shipped harness for
  driving `writePodClaimError` from a wrapped `*podsession.SlotBindError`. Each carries
  `// spec: §15.1 (REST error catalog); §6.2 (pod state machine); §4.7.1 (role and gateway RPC
  contract)`. Both fixtures wrap the refusal as the production path wraps it, in a
  `*podsession.SetupCommandFailure` inside a `*podsession.SlotBindError` whose `Stage` is the
  setup stage's label `"setup"` rather than the precedent's `"session_start"`, because
  `slotbinder.go:303-304` is where a refused `RunSetup` is wrapped. In the first case the
  `Cause` is a `codes.FailedPrecondition` status carrying `SLOT_BIND_ALREADY_STARTED`, and the
  response is 422 with body `code` `SETUP_COMMAND_FAILED` and no `Retry-After` header. In the
  second the `Cause` is a `codes.Aborted` status carrying `SLOT_BIND_ATTEMPT_SUPERSEDED`, and
  the response is the retryable 503 fallback carrying `Retry-After`. The assertions are the
  response status code, the body's `code`, and the presence or absence of `Retry-After`, because
  those are the three things the two staged sentences differ on. `details.reason` is
  `setup_command_failed` on both arms and so discriminates nothing. These two cases are what pin
  SPEC-5's widened §15.1 row and the §6.2 clause it re-keys: the envelope is chosen by
  `writeSetupCommandError`'s branch on `status.Code(setupFail.Cause)` alone
  (`pkg/gateway/sessionserver/start.go:239-249`), reached through the typed
  `*SetupCommandFailure` handler in `writePodClaimError` (`start.go:87-90`), which is a
  different decision from the `Reason()` and `classifySlotBindFailure` rows above.
- **The resume classifier holds the row for every slot-bind refusal of a resume.**
  `TestHoldOrFailOnResumeErrorSlotRefusals_spec_7_3` in
  `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`, reusing that file's
  `seedResumingRow` fixture. The held cases are a bare `status.Error(codes.Aborted,
  "slot_reclaim_in_progress")`, a superseded refusal, either of those wrapped in a
  `*podsession.SlotBindError`, and a started-session refusal carrying CODE-7's
  `ErrSlotBindAlreadyStarted` wrapped the same way. Each asserts that `holdOrFailOnResumeError`
  takes the `awaiting_client_action` branch and leaves the row a valid precondition for the
  explicit `POST /v1/sessions/{id}/resume`, rather than the terminal `failed` the classifier
  answered before CODE-5's arms. A separate case asserts that `isTransientPodClaimError` does not
  match a bare `status.Error(codes.FailedPrecondition, ...)` that is not that sentinel, which pins
  the second arm to the sentinel rather than to the code and keeps the shipped classification of
  other causes out of this deliverable.

### Wire-contract tests for SCHEMA-1, CODE-1, CODE-6 and CONF-1, tier 3

New directory `tests/tier3_contract/adapter_bind_attempt/`, beside the existing
`tests/tier3_contract/adapter_generation_fence/`, which is the sibling suite for the other
per-session stamp `ShutdownRequest` carries and which supplies both forms this directory uses: a
descriptor-and-bytes gate and a bufconn case driving `adapter.New` over gRPC. Each case carries
`// spec: §4.7.1 (role and gateway RPC contract); §7.1 (normal flow); §15.4 (runtime adapter
specification)` and the `// diagnosis:` comment tier 3 requires, and each runs over the real gRPC
transport rather than against a fake. `tests/spec-map.json` gains the directory entry
`tests/tier3_contract/adapter_bind_attempt/...` under sections 4.7, 4.7.1, 7.1 and 15.4, in the
step that creates the first file under it.

**The descriptor gate**, one file. It pins each of the nine fields SCHEMA-1 adds by message, number
and type, and pins both new `ErrorCode` values by number and name. This is the gate that fails if
a later change renumbers a field or reuses a number, which no `buf breaking` run catches for an
addition.

**The behavioural cases**, one file, carrying CONF-1's case list over bufconn and the real gRPC
transport.

### Conformance battery for CONF-1, tier 10

`tests/tier10_conformance/slot_bind_attempt_conformance_test.go`, following
`recycle_scrub_conformance_test.go`, which drives the exported adapter `Server` directly with a
fake runtime. It carries CONF-1's case list in process, one case per numbered rule and each
titled for its rule, plus the cases CONF-1 names as stated by no single rule, where a failure
names the internal state that produced it rather than a wire answer.
`tests/spec-map.json` gains it as an entry under sections 4.7.1 and 15.4, and
`slotAddressCaseFiles` in `tests/tier0_static/spec_map_slot_address_registration_test.go` gains
it in its sorted position, in the same step, because that gate derives inventory membership from
a `slot*_test.go` file name over a walk that includes `tests/` and fails tier 0 for any such file
the inventory omits. The `ABSENT` claim-register row SCHEMA-1 stages lands with it.

### Edge-list test for CODE-3, tier 1

`TestValidTransitions_spec_6_2` asserts an exact edge set and fatals on a length mismatch, so it
must move to seven edges in the same step as CODE-3. Add `{ReceivingUploads, SlotCleanup}` to
`want`, add a positive `IsValid(ReceivingUploads, SlotCleanup)` assertion, and keep the negative
assertion that `IsValid(SlotAssigned, Running)` is illegal.

### Integration and race tests, tiers 2, 4, 7a, 8 and 9

**Tier 2, the adapter against envtest, two cases** in
`pkg/gateway/podlifecycle/podsession/binder_envtest_test.go`, each carrying `// spec: §4.7.1
(role and gateway RPC contract); §6.2 (pod state machine)` and the `// diagnosis:` comment tier 2
requires. A `Binder.Prepare` whose adapter RPC answers a typed refusal leaves the pod's
per-pod `SandboxClaim` present at the apiserver and the `Sandbox` untouched; the same `Prepare`
meeting an ordinary failure deletes that `SandboxClaim`, which is what `failPhase`'s `drain`
performs (`pkg/gateway/podlifecycle/podsession/binder.go:1079`, `:1200-1202`). The claim is the
object the two arms differ on, and it is the only apiserver write either arm makes: the gateway
is not a writer of `Sandbox.status`, and the `lenny.dev/drain-request` annotation belongs to
`Binder.DrainSandbox`, which the §5.2 unhealthy-slot threshold reaches and neither `Prepare` path
does. `// diagnosis:` states that a failure means a refusal is retiring a pod that is serving the
session correctly.

**Tier 4, one case.** Extend `tests/tier4_integration/concurrent_workspace_test.go` with
`TestAbandonedBindLeavesNoSlotResidueOnTheSharedPod_spec_7_1`, carrying `// spec: §7.1 (normal
flow); §5.2 (pool configuration and execution modes); §4.7.1 (role and gateway RPC contract)` and
the `// diagnosis:` comment tier 4 requires. That file already stands up a real `adapter.Server`,
a real `SocketRuntimeProcess`, and the `echo-concurrent` runtime on a `maxConcurrentSessions: 2`
pool. Alice runs; bob's bind is abandoned at the session-start stage. After the compensation: the
pod's registry holds only alice; an unaddressed session-scoped frame on alice's Attach stream
relays again; and the shared runtime's connection and listener survive, demonstrated by a later
session carol binding and starting on the same pod. No §15.4.2 assertion is made here: this
fixture wires no CH-RUNTIMEOPS and needs none. No MCP-arming assertion:
`claimPodMCPStartLocked` refuses on `len(s.slots) != 1` and the claimant's own entry is inserted
above it, so carol beside a live alice arms nothing with or without this change.

**Tier 4, the late-reclaim arm.** A subtest arm inside that same named case. Bob's bind is
abandoned at the session-start stage with the caller's context already expired; bob's retry binds
and starts cleanly on the same pod; the compensation for the first attempt then arrives, is
answered `superseded`, leaves the running session's entry, tree, credentials and runtime
membership untouched, and releases the slot with `leaked=false`. Assert bob's retried session
still serves after the compensation returns, rather than asserting only on the reclaim's answer.

**Tier 4, the refused-retry arm.** A second subtest arm: bob's first attempt fails pre-start and
its entry survives; bob's retry lands on the same pod and is refused on the identity gate before
it touches anything; the first attempt's compensation then removes the entry; a third attempt
creates a fresh one and runs. This is defect 17 closing end to end, and it is the arm that fails
against a design whose compare sits in the handlers.

**Tier 4, the datastore-crossing case.** Extend
`tests/tier4_integration/recycle_scrub_path_test.go` with
`TestRecyclePathUnansweredReclaimLeaksTheSlot_spec_5_2`, carrying `// spec: §7.1 (normal flow);
§5.2 (pool configuration and execution modes); §6.2 (pod state machine)`. That file already runs
envtest plus miniredis plus a real adapter. The unanswered reclaim is driven from the fixture's
own dialer: `recycleAdapterDialer` builds the client through `adapterclient.Dial`, which is
variadic over `grpc.DialOption`, so the case adds a unary client interceptor that fails the
`Shutdown` RPC carrying the `slot_bind_failed` reason and passes every other RPC through
untouched. A bind whose compensating `Shutdown` the adapter does not answer is released with
`leaked=true`, so the Redis slot counter does not decrement and the per-pod `SandboxClaim`
survives at `bound`; and the leak is counted persistently through `RecordLeak` and the
`lenny_adapter_leaked_slots` gauge rather than aging out of the windowed counter.

**Tier 7a, the start-versus-reclaim race.** An RPC that admits a start parked inside
`Runtime.Start` while the compensating `Shutdown` reclaims the slot, under `-race` with a
`lenny-test stress` budget, in `tests/tier7a_load_local/slot_bind_attempt_race_test.go`. The park
is the `gatedRuntime` form `tests/tier7a_load_local/podmcp_arming_handoff_test.go:43-101` already
provides, and the case is driven from one rendezvous keyed on the RPC name, which is the form
`podmcp_once_per_pod_start_race_test.go:239-256` already uses. The rendezvous carries the two
RPCs a gateway compensation can race, `StartSession` and `Resume`, and carries no arm for the
third confirmation site CODE-2 names: `ConfigureWorkspace`'s only gateway caller is
`Binder.Launch` (`pkg/gateway/podlifecycle/podsession/binder.go:1009`), which CODE-13 does not
compensate, so that site's refusal is pinned deterministically at tier 1 instead. The two arms
release their park differently, because CODE-14's derivation table guards `Resume` and leaves
`StartSession` unguarded. On the `StartSession` arm the park is released once the compensating
`Shutdown` has returned, so the reclaim always lands before `noteRuntimeStarted` runs, which is
the only ordering a start parked inside `Runtime.Start` admits. On the `Resume` arm the
compensating `Shutdown` blocks on the attempt's own per-slot guard, which `Resume` holds from
ahead of its claim to the end of the call, so the park cannot be released on that `Shutdown`'s
return without deadlocking: the ordering that arm asserts is that the `Shutdown` is issued while
`Resume` is parked and is observed blocked on the guard, the driver then releases the park,
`Resume` returns, and only then does the `Shutdown` reclaim the started session. The property
this arm pins is that serialization alone. The extraction-versus-removal property belongs to the
third arm of **a reclaim against a section admitted before the hold opened** below, which parks a
chunk-carrying `Resume` inside `workspace.ExtractTree`; this arm's `Resume` carries a session
identifier and a checkpoint identifier and no chunks, so `restoreChunks` returns on its empty-set
guard (`resume.go:169-172`) and reaches no extraction at all. A conversation-only resume restores
nothing, and the checkpoint-transport precondition fires only for a request carrying chunks. The
`Resume` confirmation's own refusal and the rollback it takes keep their deterministic coverage
in the `Resume` row of **Start-versus-reclaim rollback, deterministic form.** above, so nothing
is lost by this arm no longer asserting a pre-confirmation reclaim. On the `StartSession` arm the RPC returns `Aborted`, `runtimeLive`
does not hold the raced session, and neither the reclaim nor the rollback files a
`ReportSessionScrub` for it, because the reclaim lands before `noteRuntimeStarted` runs. On the
`Resume` arm the reclaim removes an entry whose slot reached `running`, because `Resume`
returned and recorded runtime-live membership before the `Shutdown` proceeded, so that arm asserts
the opposite disposition: the reclaim closes the runtime and files exactly one `ReportSessionScrub`
for the raced session, which is what CODE-15's `live := removed && s.runtimeHoldsLocked(sessionID)`
stages it to do. Run each arm on a pod with no co-tenant as well as a
co-tenanted one, because the pod-level cohort outcome is what the two variants hold apart.

**Tier 7a, the reverse ordering.** The same rendezvous on its `StartSession` arm alone, with the
park released only after the reclaim has answered and a successor has re-created the entry under
the same identifier. The reverse ordering is reachable only on the unguarded start path: a
successor cannot be created under the identifier while a parked `Resume` holds that slot's guard,
because the `Shutdown` that would clear the way is still blocked on it. The start confirmation
finds an entry carrying a different token and refuses, the RPC returns `Aborted`, and the
successor's entry, tree and runtime membership survive. This fails against a confirmation
predicate that reads only `st.sessionID`.

**Tier 7a, the concurrent-resolve race**,
`TestConcurrentBindAttemptsResolveExactlyOneOwner_spec_4_7_1` in the same new file. N goroutines,
each a distinct attempt, race `ensureSlotStateLocked` for one slot identifier under `-race`.
Exactly one is admitted on the create branch, every other is refused `superseded`, the entry's
token equals the winner's afterwards, and no goroutine observes a partially initialized entry.
This is the case that fails against a resolve that releases the lock between the lookup and the
stamp, which is the registry critical-section step SPEC-5 states and CONF-1 publishes.

**Tier 7a, `TestSlotIdentifierReclaimHoldRefusesABindUntilTheCleanupReturns_spec_5_2`**, in a new
`tests/tier7a_load_local/slot_reclaim_hold_race_test.go`. `tests/spec-map.json` gains the file
under section 5.2 in the same step, and `slotAddressCaseFiles` in
`tests/tier0_static/spec_map_slot_address_registration_test.go` gains it in its sorted position,
because that gate derives inventory membership from a `slot*_test.go` file name and fails tier 0
for any such file the inventory omits. Under `-race` with a `lenny-test stress` budget, park a
reclaim inside `Runtime.Close` and drive a concurrent request onto the same slot identifier
repeatedly. Two arms, both driven through the production caller: the §7.4 mid-session upload arm,
whose `PrepareWorkspace` arriving while the session's own `Shutdown` is inside the parked close
is refused with the transient sentinel and whose gateway-side observable is the handler's own
HTTP 502 `UPSTREAM_ERROR`; and the retry arm, where a second bind attempt at the same session on
the same pod is refused for as long as the park holds and admitted once the reclaim returns.
Both arms assert that no admitted bind overlaps the reclaim's `Runtime.Close` or its
`removeSlotTree`, and that every refusal carries `codes.Aborted`. Both arms also bound each
refused call's wall time well below the parked cleanup's duration, which is what turns the case
red against an implementation that blocks the caller on the slot guard and refuses it only once
the cleanup has returned.

**Tier 7a, the lock-order assertion.** The same file gains a case driving `FinalizeWorkspace`,
`RunSetup`, `PrepareWorkspace`, `Resume` and a removing `Shutdown` concurrently for one slot
identifier under `-race`, which is the guarded set CODE-14's derivation table names, asserting no
deadlock across a `lenny-test stress` budget and asserting that the guarded sections do not
overlap: each records the instant it enters and the instant it leaves its path work, and no
recorded interval intersects another. The `Shutdown` this case drives is the removing one, because
a `Shutdown` answering `absent` or `superseded` takes no guard and is covered by the arm below. A second arm covers the guard's lifetime, which is the
property a table that dropped its entries would lose: one goroutine holds the guard across a
materialization, a reclaim of the same identifier runs to completion beside it, a third goroutine
then acquires the same identifier's guard, and the third's path work begins only after the first's
has ended. The per-slot guard and `s.mu` are two locks and the order between them is stated once
in CODE-14; this is what holds the statement true.

**Tier 7a, a reclaim against a section admitted before the hold opened.** In the same file, a
`FinalizeWorkspace`, on a second arm a multi-frame `PrepareWorkspace`, and on a third arm a
`Resume` parked inside `workspace.ExtractTree` are each admitted and parked inside their path work
before the reclaim opens its hold, and the matching `Shutdown` then runs. The third arm runs a
second time against a §10.1.4 hold termination in place of the `Shutdown`, because that pass
destroys the same tree from a goroutine no request drives. That second run drives two started
members on the pod, the first of them parked inside `workspace.ExtractTree` under its own guard for
a span shorter than the pass's guard-acquisition deadline so that the guard is genuinely acquired
for that member, and the hold termination then fires. Under `-race`, the reclaim's `removeSlotTree`
does not interleave with the first member's writes, and once both have returned no slot tree and no
partially restored workspace survives for the first member's identifier. The close-budget property
is pinned deterministically at tier 1 in `pkg/adapter/holdstate_test.go` rather than asserted here.
This is the case the
reclaim hold cannot cover, because the hold is tested at admission, and it fails against a
`Shutdown` that takes no slot guard and against a `Resume` that takes none.

A fourth arm drives the opposite disposition. While a materialization is parked under the guard, a
`Shutdown` naming a token the entry does not carry answers `superseded` within a bound far below
the parked section's duration, having acquired no guard and removed nothing. This is what turns
red against a handler that takes the guard ahead of its comparison, where a reclaim that removes
nothing waits out an unbounded materialization, spends the gateway's cleanup budget and is charged
a leak.

A fifth arm covers the interval between the two decisions CODE-1's removing arm makes, which is the
interval in which the registry can change under a reclaim that has released `s.mu` and not yet
acquired the guard. It reuses the third arm's parked `Resume`: a compensating `Shutdown` naming the
parked attempt's own token decides `RECLAIMED` on its fast path and then waits for the guard the
parked `Resume` holds. While it waits, and still under that guard, the parked `Resume` fails and
calls `releaseSessionSlotUnderGuard`, which removes the entry and its tree, and an
`AssignCredentials` carrying a successor attempt's token then creates a fresh entry, its `current`
directory and its `credentials.json` under the same slot identifier, which it can do because the
derivation table leaves `AssignCredentials` unguarded. The holder releases the guard only after
that successor entry exists, and the whole rendezvous completes well inside the blocked
`Shutdown`'s own request context, so the case is deterministic rather than timing-dependent under
the context-bounded acquisition. The waiting `Shutdown` then wakes, decides again, answers
`superseded`, deregisters nothing, and the successor's entry, its `current` directory and its
`credentials.json` all survive the call. This turns red against a handler that acts on its first
decision, which would deregister an entry a successor attempt owns and delete the workspace and
credential file that attempt is using.

`tests/tier7a_load_local/shutdown_drain_gate_race_test.go`'s
`TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` and
`TestShutdownDrainRacesAnIncomingSession_spec_6_4` already pin the one-signal and unbound-entry
properties and must keep passing, with the mechanical edit that their `Shutdown` calls set
`unconditional_teardown`. Every session whose `Shutdown` they drive is started through
`startDrainSession`, which runs a full `StartSession`, so CODE-15's move of the ending session's
gate from `bound` to `started` cannot reach them.

**Tier 8, one case.** `tests/tier8_chaos/` gains
`TestCompensationLostToAGatewayCrashLeavesTheEntryRefusable_spec_7_1`, carrying `// spec: §7.1
(normal flow); §4.7.1 (role and gateway RPC contract)` and its `// diagnosis:`. A bind attempt
fails and its compensation never leaves the gateway. The entry stands stamped with a dead
token, every later attempt at that session on that pod is refused rather than admitted onto it,
and a placement onto a different pod succeeds. This pins the residue as a refusal rather than a
silent adoption, which is the property the token buys and the one a durable compensation record
would later relieve.

**Tier 9, the credential fence.** New file
`tests/tier9_security/slot_credential_reclaim_fence_test.go`, which `tests/spec-map.json` gains
under sections 4.7.1, 4.9 and 5.2 and which `slotAddressCaseFiles` gains in its sorted position, in
the same step. This tier owns the cases because the token is what decides whose credential
material a reclaim reaches, and because CODE-15 widens the tree-removal gate from `bound` to
`removed`. Three arms, each carrying `// spec: §4.7.1 (role and gateway RPC contract); §4.9
(credential leasing service); §5.2 (pool configuration and execution modes)` and the
`// diagnosis:` comment tier 9 requires:

- A superseded reclaim leaves the successor's `credentials.json`, its credential directory and
  its armed §4.9 expiry timers intact. A regression here lets a teardown belonging to an
  abandoned attempt reach a live session's credential material.
- A matching reclaim leaves nothing of its attempt behind on either class: for a
  bound-but-unstarted entry, no `credentials.json` and no armed §4.9 expiry timer; for a
  registered-but-unbound entry, no slot directory and no credential directory.
- A failed attempt's lease release strips none of a successor's leases. This is the
  attempt-scoped release rule at the tier that owns credential isolation: the failed attempt
  releases the identifiers it minted, the
  successor's leases for the same session survive, and the successor's session continues to
  authenticate afterwards.

### Documentation reconciliation tests, tier 11

**For SPEC-3**, files `tests/tier11_docs/spec_28_register_writers_test.go` and
`tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go`. The implementor runs
`grep -rn sessions_served tests/`, and this sweep re-keys or deletes every tier-11 assertion that
grep finds pinning a §12.6 `sessions_served` sentence: an assertion on the write clause is
re-keyed onto the replacement, and an assertion on the read clause is deleted, because §12.6 no
longer states an evaluation point for it to compare. The read-clause block the sweep deletes from
`tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go` is that file's only read
of `spec/12_storage-architecture.md`, so the sweep also drops the `12.1` term from
`TestPerReleaseSessionCountDrainAgrees_F5231`'s `// spec:` annotation, drops the `§12` term from
that case's `// diagnosis:` comment, drops the `§12 (the sessions_served schema prose and column
comment)` landing site from item 2 of the file header and the `12.1 (sessions_served schema)`
credit from the file-level `// spec:` annotation, and removes that case's row from section `12.1`
of `tests/spec-map.json`. That row is section `12.1`'s only row and the section stays with an
empty test list, which its existing non-normative entry in `tests/spec-map-exceptions.yaml`
already covers. Landing the annotation edit and the map edit in this sweep keeps the harness from
crediting §12.1 to a case that reads nothing there.

**For SPEC-4 and DOCS-1**, file
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`. No shipped gate compares
the reader-facing per-slot table's rows against the §6.2 block. Extend both shipped tests in
place rather than adding a test function; each already carries the `// spec:` annotation and the
`// diagnosis:` comment tier 11 requires, and each gains one clause in its `// diagnosis:` naming
the new edge. Add `"receiving_uploads ──→ slot_cleanup"` to `generalSlotEdges`, which gates both
sides of §6.2's split because the slice feeds a positive loop over the either-concurrency block
and a negative loop over the concurrent-occupancy block. Add the paired substring
`` `receiving_uploads` | `slot_cleanup` `` to the `requireAllContain` list over the page's
`### Per-slot sub-states` section.

**For DOCS-2**, file `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`.
Extend the shipped `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` in place. Add to the
row's `requireAllContain` list the substrings `"no other bound session"`, `"a session whose start
the adapter has admitted"`, `"either the bind attempt"`, which pins the two-field precondition
the staged row states, and `"reclaimed"` and `"absent"`, which gate the outcome sentence the
staged row adds. Add a page-level assertion over the substring `"**Bind attempt token.**"`, which
gates the added paragraph. Add one clause to the `// diagnosis:` comment naming the two teardowns
and the two teardown preconditions. The `ReportSessionScrub` row edit takes no new assertion: the
shipped `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` already holds that row's
addressing sentence and its agreement with the specification row, and the reporting condition
itself cannot be gated across the two carriers, because §4.7 states it by deferring to §5.2 by
link while this page may carry no section number.

**For DOCS-3**, no file. No shipped gate compares `docs/reference/error-catalog.md` against
§15.1: no file under `tests/`, `scripts/` or `cmd/` and no `Makefile` target names the page.
This deliverable adds none, because a gate built for the one row it touches would leave every
other row of a sixty-row page ungated and would read as coverage the page does not have.

**For DOCS-4**, no file, for the reason its deliverable states.

**For CODE-10, CODE-11 and CODE-12**, no file: every edit is a comment and no assertion moves,
under the invariants `### Comment-carrier reduction: shared invariants` states.

**For the counters and SPEC-6**, each series is held by its own gate, and each is stated
separately below.

The adapter-side `lenny_slot_shutdown_untokened_entry_total` is held to both
`docs/reference/metrics.md` and the §16.1 catalog by the shipped
`tests/tier11_docs/adapter_metric_catalog_test.go`, which needs no edit: its source of truth is a
regex over the registrations in `pkg/adapter/metrics.go`
(`adapter_metric_catalog_test.go:26,:56`), and it fails when a registered name is absent from the
reference page (`:100`) or from the §16.1 catalog (`:110`). The counter is therefore not added to
that file's `undocumentedAdapterMetrics` or `specCatalogPending` maps (`:34`, `:49`), either of
which would exempt it from the sweep this deliverable relies on.

The gateway-side `lenny_slot_compensation_superseded_total` is held to §16.1 by the two-way
`spec161Metrics` reconciliation in `pkg/observability/metrics/catalog_test.go:188-211`, which is
a package test in the unit tier rather than a tier-11 gate. Its `docs/reference/metrics.md` row is
held by a new file, `tests/tier11_docs/slot_compensation_metric_reference_test.go`, following the
shipped one-metric-per-file precedent `tests/tier11_docs/crd_ssa_conflict_metric_reference_test.go`:
it reads the reference page, locates the single table row naming the counter, and asserts that the
row names the `pool`, `k8s_pod_name` and `cause` labels and the superseded semantics SPEC-6's §16.1
row states, and it asserts the same of the `lenny_adapter_leaked_slots` row, with the `pod_id` and
`pool` labels. It carries the `// spec:` annotation and the `// diagnosis:` comment tier 11 requires.

That file's name states the slot as its subject, so the tier-0 inventory gate derives it into the
inventory through `slotSubjectFileRE`
(`tests/tier0_static/spec_map_slot_address_registration_test.go:1173`) over a walk that includes
`tests/` (`:1191`). It therefore enters `slotAddressCaseFiles` in sorted position and takes a
`tests/spec-map.json` credit for §16.1, the section its own annotation names, in the same step that
creates it; the credit check is at `spec_map_slot_address_registration_test.go:970-988`.

### Coverage and preflight

Run `lenny-test coverage --diff <merge-base>` and raise the changed lines to the 80% floor,
covering the error and boundary paths rather than the happy path. Before any tier above 1, run
the environment preflight in `.claude/rules/test-coverage.md`: confirm the loaded images match
the renderer, sweep terminal agent pods, confirm the warm pools reach Ready, reap orphaned
envtest processes, and confirm the same tier fails on a clean checkout of the merge base before
treating a failure as this change's.

## Edge cases and accepted failure modes

This section carries the cases that concern code alone. The accepted failure modes of the
contract are in the section of the same name in the spec-changes file, which is the single home
of these cases:

- A retry refused while the first attempt's entry or hold stands, the callers that can meet the
  hold, and what each refusal costs: **A retry that meets the reclaim hold spends an attempt on
  it**.
- A compensation lost to a gateway crash: **A compensation lost to a gateway crash leaves an
  entry no attempt can use**.
- A bind abandoned at the connect stage: **A bind abandoned at the connect stage**. CODE-13's
  `BindReservedSlot` row and CODE-5's reserved branch state how a failed reservation release on
  the create-time-reserved path is folded into `Leaked` and accounted.
- An attempt that recreates the entry its own unconditional teardown removed: **An attempt that
  recreates its own entry after an unconditional teardown removed it**.
- **A leaked entry costs the pod its MCP arming and its unaddressed-frame path for the pod's
  life.** `claimPodMCPStartLocked` returns `startMCP` false whenever `len(s.slots) != 1`, and
  `deliverToSession` rejects unaddressed session-scoped frames once `slotCount() > 1`. Both are
  deliberate co-tenancy rules and both are correct to count a registered-but-unbound entry; the
  defect is that the entry never goes away, which is the reaper's subject rather than this
  proposal's. The problem statement puts both out of scope.
- **A rolled-back start can close a successor's runtime session.** The rollback leaves the slot
  registry and the on-disk tree alone, so it destroys no successor's workspace, but
  `Runtime.Close(ctx, sessionID)` is keyed on the slot identifier alone and `SlotID ==
  SessionID` makes the abandoned attempt and any later attempt at the same session the same
  identifier. Between `noteRuntimeStarted` returning false and the rollback's close, a later
  attempt can claim that identifier on the same pod, and the close then releases the successor
  from the shared runtime's active set and, when that empties the set, ends the shared
  connection, the spawned child and the listener (`pkg/adapter/socketruntime.go:435-467`). The
  token closes the confirmation and not this close: the confirmation refuses on the token, and
  the rollback that follows still addresses the runtime by the identifier both attempts share.
  Closing it would need a per-session identity on the shared runtime's active set, which no
  `Runtime` implementation carries.
- **A cleanup the adapter runs inside its own start handler reports its failure nowhere.** A
  bind that fails inside a start handler (`StartSession`, `Resume`, or the SDK-warm start) after
  its slot entered `receiving_uploads` and before the slot reached `running` runs the
  §5.2 per-slot cleanup inside that handler, through `releaseSessionSlot`. A bind that fails at
  the workspace-preparation, setup or credential-assignment stage runs no adapter-side cleanup,
  so its residue is reclaimed by the §7.1 compensation or, where that obligation does not reach,
  at the pod boundary. That function discards `removeSlotTree`'s error
  (`pkg/adapter/slotsession.go:217`), and CODE-6 replaces the discard with a warning log line
  rather than with a carrier the gateway can read. The gateway's compensation then finds no
  entry, is answered `ABSENT`, and CODE-13 reads that as a reclaim acknowledged clean with `sbe.Leaked`
  false, so an incomplete cleanup on this path reaches no leaked sub-state and contributes
  nothing to the `ceil(maxConcurrentSessions/2)` trigger. The residue is what the §5.2
  disposition table states in its row for a slot released outside a `Shutdown` whose act fails
  after the deregistration. The residue that goes unaccounted is the leak accounting
  rather than the identifier: `releaseSessionSlot` runs under `reclaimSlotLocked`, so a
  `removeSlotTree` that fails there is a cleanup that did not complete and the identifier stays
  held for the life of the pod, which is what keeps a later bind off that tree. What the gateway
  never learns is that the cleanup failed, and that is the accepted part.
- **A pod whose drain failed keeps a dead attempt's token.** See the summary's defects entry
  **A failed compensating drain leaves the pod holding the dead attempt's registry entry.**
- **A compensating `Shutdown` can spend its budget waiting on a guarded `Resume`.** CODE-14's
  guard spans `Resume` from ahead of its claim to the end of the call, so the reclaim CODE-13 sends
  on a failed `Binder.Resume` blocks until the handler returns, inside the compensation's own
  budget. The wait is bounded rather than open: `CheckpointTransport.GetChunk` builds each fetch
  on the handler's own context, so the client deadline that triggered the compensation cancels the
  fetch, the pipe closes with that error and `ExtractTree` returns. What is not bounded by that
  cancellation is the copy of a chunk body already in flight and the extraction of bytes already
  in the pipe, so a restore whose remaining chunk is large enough can outlast the budget. The
  reclaim then answers nothing in time, the RPC error sets the bind's leaked disposition, and the
  pod holds that slot's counter occupancy. The compensation's deadline is the handler's own
  deadline, so the removing arm's guard acquisition expires with it and the arm takes CODE-14's
  **Disposition of an expired acquisition at a removing site**. Accepted rather than closed: the
  residue is the unordered `removeSlotTree` against `ExtractTree` that CODE-14's *Lock order.*
  paragraph names, with the identifier held for the pod's remaining life under that disposition
  rather than released over a tree the extraction is still writing into. Abandoning the removal
  instead is the option that disposition rejects, and a second adapter-side deadline over the
  restore would state the gateway's own timeout in a second place. The unanswered reclaim is the
  reaper's subject, as it is for the spec-changes file's gateway-crash case.
- **A compensating `Shutdown` holds the pool's queue head for its own budget.** CODE-13 sends the
  reclaim inside the bind attempt that failed, and that attempt is the closure `runWithQueue`
  runs (`pkg/gateway/sessionserver/start.go:2606-2608`, reaching `binder.BindSlot` at `:2810`),
  so on a `queue`-mode pool the next waiter is admitted only after the compensation returns:
  admission is head-only and a non-head waiter blocks on its ticket until the waiter ahead of it
  leaves (`pkg/gateway/sessionserver/queue.go:185-191`). The added hold is bounded by the
  compensation's own deadline, `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` seconds
  (`spec/05_runtime-registry-and-pool-model.md:545`); raising `maxConcurrentSessions` shrinks the
  per-slot budget toward the five-second floor, so a low-concurrency pool with a large
  `cleanupTimeoutSeconds` is the worst case for this hold. A waiter the hold displaces past its wait
  bound leaves through the envelope §5.2 already publishes for a saturated pool,
  `WARM_POOL_EXHAUSTED` with a `Retry-After` header
  (`spec/05_runtime-registry-and-pool-model.md:440`), which is the exhaustion sentinel
  `waitInQueue` returns at `pkg/gateway/sessionserver/queue.go:194-202` against a default bound
  of 30 seconds (`pkg/gateway/sessionserver/queue.go:18`). The bound is tested after admission
  rather than while the waiter is behind the head, so a displaced waiter's wall-clock wait is its
  own bound plus the head's attempt duration; the compensation lengthens that wait and leaves it
  bounded. Accepted rather than moved off the attempt's own path. The hold class is not new:
  `SetupPolicy.timeout_seconds` of zero means no aggregate cap on the setup phase
  (`schemas/lenny-adapter.proto:941-943`), so a queued attempt can already hold the head for an
  uncapped setup, and the compensation adds an increment that carries an explicit deadline. An
  off-path compensation would need a worker, a retry policy, its own residue when the process
  dies mid-compensation and a home for the attempt token, against a residue that withholds one
  slot counter on the pod until pod termination. Whether the transport keepalive shortens the
  compensation in practice is unverified, and it can only shorten the hold.
- **A member parked under its own guard can cost the §10.1.4 pass its whole guard-acquisition
  deadline.** A `Resume` inside `workspace.ExtractTree` on a large checkpoint holds that member's
  guard, and `terminateHeldSession` waits for it against the pass's single ten-second
  guard-acquisition context. A park that outlasts that context leaves every remaining member
  taking CODE-14's **Disposition of an expired acquisition at a removing site**, in its
  `terminateHeldSession` row. Accepted rather than closed, because that disposition is the
  shipped removal with the hold retained, and each member closes on its own live ten-second
  context, so no final usage report is lost and §8.3 `budget_return.lua` still runs on complete
  token totals.
- **A retry of an attempt's own `AssignCredentials` double-counts leases.** The lease store is
  keyed by lease identifier and each mint produces a fresh one, so a second assignment for one
  session adds leases rather than replacing them, while the adapter side replaces. Under the
  identity gate a foreign `AssignCredentials` is refused, so the double-count arises only from a
  retry of an attempt's own request, and CODE-13's attempt-scoped release returns every
  identifier that attempt minted.
- **A compensation sent to a pod in coordinator-hold state is refused.** The hold-state
  allowlist admits only the fence, version negotiation, the event stream and the health probes,
  so the reclaim returns an error and the slot is correctly classified leaked, which on a pod
  serving concurrent sessions §6.2 then routes to the §5.2 threshold. The state cannot arise in
  a running deployment, because nothing arms the hold; the case becomes live when remediation
  step R12 ships the gateway control-stream consumer.
- **Faster pod churn at `maxConcurrentSessions >= 3`.** The leaked disposition withholds the
  counter decrement and counts persistently, so an unacknowledged reclaim moves a pod toward
  retirement sooner than a clean release would. Accepted as §6.2's semantics applied
  consistently. At `maxConcurrentSessions: 2` the disposition changes nothing on the retry path,
  because the threshold there is already 1. The reserved branch and the §7.3 re-attach change at
  every concurrency because they reach the accounting at all for the first time.
- **`slotCount` still counts a registered-but-unbound entry.** The §28.5.3 count fails closed on
  purpose and its comment says so. The predicate is left exactly as it is.

## Files touched on application (non-spec)

- `schemas/lenny-adapter.proto` · the two `ErrorCode` values, the `SlotReclaimOutcome` enum,
  the nine fields SCHEMA-1 states, and the scrub-outcome and report-trigger comment
  replacements SCHEMA-1 states.
- `pkg/proto/adapter/v1` · regenerated by `make generate-proto` in the same commit as the proto
  edit.
- `scripts/seed-claim-register.py` · the three rows SCHEMA-1 stages, added to the `EXPLICIT`
  list that is their row source: the two `WIRED` rows with SCHEMA-1, the `ABSENT` row with
  CONF-1.
- `tests/claim-map.json` · regenerated from `scripts/seed-claim-register.py` in the same commit
  as the row edit.
- `tests/spec-map.json` · every section a new or edited case's own `// spec:` annotation names is
  credited here, satisfied by a whole-file or directory entry for a file the map registers as a
  whole and otherwise by a `path::TestName` entry, because `validate-maps` checks file membership
  alone while `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` checks a file the map
  registers case by case one case at a time. Each entry lands in the step that creates the file
  or the case it maps, so no intermediate commit leaves either gate red. The file also takes one
  removal, the section `12.1` row the tier-11 `**For SPEC-3**` sweep names. The new test
  surfaces
  take file and directory entries: `tests/tier3_contract/adapter_bind_attempt/...` under sections
  4.7, 4.7.1, 7.1 and 15.4; `tests/tier7a_load_local/slot_bind_attempt_race_test.go` under
  sections 4.7.1 and 7.1; `tests/tier7a_load_local/slot_reclaim_hold_race_test.go` under section
  5.2; `tests/tier8_chaos/compensation_loss_test.go` under sections 4.7.1 and 7.1;
  `tests/tier9_security/slot_credential_reclaim_fence_test.go` under sections 4.7.1, 4.9 and 5.2;
  and `tests/tier10_conformance/slot_bind_attempt_conformance_test.go` under sections 4.7.1 and
  15.4. `tests/tier4_integration/concurrent_workspace_test.go` is registered as a whole, under
  sections 5.2, 6.4, 15.1 and 28.5.3, the 15.1 credit through the directory entry, so it gains
  whole-file credits under sections 7.1 and 4.7.1. The
  new cases landing in files the map already registers case by case take per-case entries: in
  `tests/tier4_integration/recycle_scrub_path_test.go`, whose only whole-file credit is 15.1,
  `TestRecyclePathUnansweredReclaimLeaksTheSlot_spec_5_2` is entered as
  `tests/tier4_integration/recycle_scrub_path_test.go::<name>` under sections 5.2, 6.2 and 7.1.
  The tier-1 files this change lands cases in are registered at different granularities, so each
  takes the treatment its own registration requires, read off the map rather than assumed:

  - `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` is registered as a whole, under
    sections 4.6.3, 4.7, 5.2, 6.2 and 7.1, with section 4.1 through the `pkg/gateway/...`
    directory entry and no per-case entries. The cases this change lands there
    annotate sections 7.1, 5.2 and 6.2, all of which it already holds, so it takes no new entry.
  - `pkg/gateway/podlifecycle/podsession/binder_test.go` holds no whole-file credit outside
    section 4.1 through that same directory entry, and the map registers it case by case. The
    gate therefore checks each of its cases individually, and every case this change
    lands there is entered as
    `pkg/gateway/podlifecycle/podsession/binder_test.go::<TestName>` under every section its own
    `// spec:` annotation names, which for those cases are 5.2, 6.2 and 7.1.
  - `pkg/gateway/runtime/adapterclient/client_test.go` is registered as a whole, under sections
    4.4, 4.7, 4.9, 5.2, 7.2, 7.3, 7.5, 11.2, 11.3, 11.4, 13.2 and 28.5.3, with section 4.1
    through the directory entry and no per-case entries. CODE-7's cases annotate sections 4.7.1
    and 15.4, neither of which it holds, so it gains a whole-file credit under each.
  - `pkg/gateway/sessionserver/slotretry_test.go` is registered case by case, so the gate checks
    each case against its own function's entries. CODE-5's rows are added to
    `TestSlotBindErrorReason_spec_5_2` and
    `TestClassifySlotBindFailurePassesTransientThrough_spec_5_2`, both already entered under
    section 5.2, and neither function's annotation changes, so no new entry falls due there. A
    new function added to that file takes a `path::TestName` entry under every section its own
    annotation names.
  - `pkg/adapter/slotsession_test.go` is registered as a whole, under sections 4.9, 5.2, 6.1,
    6.4 and 15.4, with section 4.7 through the `pkg/adapter/...` directory entry and no per-case
    entries. CODE-1 and CODE-2's cases annotate section 4.7.1, which it does not hold, so it
    gains that whole-file credit.
  - `pkg/adapter/sdkwarm_test.go` is registered as a whole, under sections 4.7.9 and 6.1.
    CODE-2's SDK-warm confirmation case annotates sections 4.7.1, 6.1 and 7.1, so the file gains
    whole-file credits under 4.7.1 and 7.1 and keeps the one it holds under 6.1.
  - `pkg/adapter/holdstate_test.go` is registered as a whole, under sections 5.2, 6.4, 9.1, 10.1,
    10.1.4, 11.2, 15.4, 15.4.3 and 28.5.3, with section 4.7 through the `pkg/adapter/...`
    directory entry and no per-case entries. CODE-14's per-member close-budget case annotates
    sections 4.7.1, 5.2 and 10.1.4, so the file gains a whole-file credit under 4.7.1 and keeps
    the credits it holds under 5.2 and 10.1.4.

  Any other file the implementor lands a case in is checked the same way, against that file's own
  credits and its own registration granularity: a file the map registers case by case takes a
  per-case entry for every section the new case's own annotation names, and a file it registers as
  a whole takes a whole-file credit for every section its new cases annotate that it does not
  already hold.
- `tests/tier0_static/spec_map_slot_address_registration_test.go` · the `slotAddressCaseFiles`
  inventory is the second register a test file enters, required both for a file whose name
  matches `slot*_test.go` and for any file that calls the slot claim surface. Every new file
  whose name matches that pattern enters the inventory in its sorted position, in the step that
  creates it, and a new or edited test file that gains a call into the slot claim surface
  (`slotstate.`, `ClaimSlot(`, `ReleaseSlot(`, `ReserveSlotOnPod(`, `claimAtCreate(` or
  `BindReservedSlot(`) enters it in the step that adds the call.
- `pkg/adapter/bindattempt.go` · new: the `slotResolve` type, the reclaim-hold side table's
  helpers, the per-slot guard table's two hand-out forms, `lockSlotGuard` and the hold-checking
  `acquireSlotGuardForResolve`, the three refusal sentinels and their predicates, and the
  two shared resolve helpers.
- `pkg/adapter/server.go` · the `Server.reclaiming map[string]struct{}` hold set and the
  `Server.slotGuards map[string]chan struct{}` guard table, both guarded by `s.mu`, declared on the
  struct that owns them, and the nil-defaulted test-only seam field
  `Server.removeSlotTreeFn func(*slotState) error`, declared beside `scrubDone`.
- `pkg/adapter/slot.go` · the `slotState.bindAttempt string` field, `ensureSlotStateLocked`'s
  `slotResolve` parameter and rule 2-through-7 predicate, and `ensureSlotPaths`'s widened signature, and the
  new `Server.removeSlotTreeVia` method that reads `removeSlotTreeFn` and otherwise calls
  `removeSlotTree`.
- `pkg/adapter/staging.go` · the three resolve sites' `slotResolve` arguments,
  `resolvePrepareStagingDir`'s widened signature, `FinalizeWorkspace`'s `mid_session` read moved
  above the resolve, `PrepareWorkspace`'s first-frame latch, the wire rule's
  two directions on all three handlers, and the per-slot guard bracketing each handler's resolve
  and the path work that follows it.
- `pkg/adapter/slotcreds.go` · the resolve site's `slotResolve` argument.
- `pkg/adapter/credentials.go` · `AssignCredentials`'s wire-rule check and the resolve it passes.
- `pkg/gateway/runtime/adapterclient/client.go` · the two error sentinels and
  `translateSlotBindRefusal` with its call sites, the bind-sequence method signatures CODE-4's
  `client.go` target states, `unconditional_teardown` set inside `Shutdown` and
  `ShutdownRecycle`, and the new `ShutdownReclaim`.
- `pkg/adapter/session.go` · `Shutdown`'s two-field precondition, its bind-attempt comparison
  under `s.mu` and the `shutdownReclaimOutcome` function that decides it, its context-bounded
  slot-guard acquisition, which on an acquisition that did not complete takes CODE-14's disposition
  of an expired acquisition at a removing site (the file gains a `log/slog` import for its
  warning) and its re-decision on the removing arm, its reclaim
  hold, its `slot_tree_removal_failed` warning on a failed tree removal, its split gates, its single exit helper (which carries the response construction and the
  whole-pod recycle scrub the shipped handler runs as a trailing statement, that trailing copy
  being deleted), its doc comment, and the `StartSession` rollback.
- `pkg/adapter/runtimegeneration.go` · `noteRuntimeStarted`'s signature, body and doc comment,
  and the new `runtimeHoldsLocked` accessor.
- `pkg/adapter/slotsession.go` · the `reclaimSlotLocked` helper beside `deregisterSlotLocked` and
  that function's doc comment, `releaseSessionSlot` rerouted through the helper and its discarded
  `removeSlotTree` error turned into a logged warning (the file gains a `log/slog` import),
  `deregisterSlot` retired into it, the split of `releaseSessionSlot` into the guard-acquiring
  form, which gains a `context.Context` first parameter and takes CODE-14's disposition of an
  expired acquisition at a removing site when its acquisition does not complete, and
  `releaseSessionSlotUnderGuard`, the `release` field on `heldSession`,
  `claimSessionSlotUnderLock`'s `slotResolve` parameter and typed `!idempotentRepeat` refusal,
  and the token the two claim functions report.
- `pkg/adapter/holdstate.go` · `terminateHeldSession`'s guard acquisition and its two deferred
  releases, per CODE-14's **`terminateHeldSession`'s guard and close context.**, and its hold
  release, completion predicate, and captured `Runtime.Close` and tree-removal errors, per CODE-6.
  `onHoldTimeout` and its comment, per CODE-14's **`terminateHeldSession`'s guard and close
  context.** On an
  acquisition that expires it takes CODE-14's disposition of an expired acquisition at a removing
  site, in its own row.
  `deregisterStartedSessions`, pass 1, acquires no guard.
- `pkg/adapter/resume.go` · the `bind_attempt` read and its resolve, the per-slot guard acquired
  ahead of the claim and held across the checkpoint restore, its rollback calls moved to
  `releaseSessionSlotUnderGuard`, the `noteRuntimeStarted` call site and the token it passes.
- `pkg/adapter/sdkwarm.go` · `ConfigureWorkspace`'s resolve, which carries no token and sets
  `allowStarted` from `idempotentRepeat`, and the `noteRuntimeStarted` call site.
- `pkg/sandbox/slotstate/slotstate.go` · `ValidTransitions()`, its edge-list doc comment, and the
  `Running`, `SlotCleanup`, `Released` and `Leaked` constant docs.
- `pkg/sandbox/slotstate/registry.go` · the retired cleanup-timeout trigger in `MarkLeaked`'s
  doc comment.
- `pkg/gateway/runtime/slothealth/slothealth.go` · the retired cleanup-timeout trigger in the
  `event` and `RecordLeak` doc comments.
- `pkg/gateway/sessionserver/sessionserver.go` · the retired cleanup-timeout trigger in the
  `slotLeakGauge` and `SlotLeakGauge` doc comments.
- `pkg/gateway/podlifecycle/podsession/bindattempt.go` · new: `newBindAttempt`.
- `pkg/gateway/podlifecycle/podsession/slotfailure.go` · `SlotBindError.Leaked` and the new
  `slotFailureWorkspaceFinalize` stage constant.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` · `slotCleanupBudget`,
  `compensationCause`, `compensateFailedSlotBind`, `materializeSlot` and
  `materializeSlotStages` with the mint and the attempt-scoped lease release,
  `releaseAttemptCredentials`, `ReleaseSlotReservation`, `BindReservedSlot`, `ClaimSlot`'s
  connect-stage release, `Binder.ReleaseSlot`'s two teardown calls, and the
  `noteCompensationOutcome` forwarder beside `recordSlotFailure`.
- `pkg/gateway/podlifecycle/podsession/binder.go` · the `SlotReclaim` hook field beside
  `SlotFailure`; `Binder.Prepare`'s mint, carry and reclaim
  closure; `assignCredentials`'s return of the lease identifiers it minted, the
  `CredentialAssigner` interface's new `Release(leaseID string)` member, and the
  attempt-scoped release on `Binder.Prepare`'s credential-assignment failure arm; `Binder.Launch`'s reclaim closure; `Binder.Resume`'s mint, carry,
  compensation and failure branch; `releaseResumeSlot`; and `Binder.shutdownAdapter`'s two
  teardown calls.
- `pkg/gateway/sessionserver/start.go` · the `slotBinder` interface, `accountSlotFailure`,
  `applySlotRetryPolicy`, `bindSlotWithRetry`, `bindConcurrentSlot`, `resumeOnPod`,
  `rollbackClaim`, and `isTransientPodClaimError`'s `codes.Aborted` and `ErrSlotBindAlreadyStarted` arms.
- `pkg/gateway/sessionserver/upload_to_session.go` · the §7.4 pair sets `mid_session` true and
  carries no token.
- `pkg/observability/metrics/catalog.go` and the `spec161Metrics` list in its `catalog_test.go` · the superseded series and the leaked-slots gauge.
- `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go` and `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go` · the superseded collector and its `IncSlotCompensationSuperseded` accessor, and in `gatewaymetrics_credential.go` alone the retired cleanup-timeout trigger in the `adapterLeakedSlots` field and construction-site comments, which CODE-3 reduces.
- `pkg/adapter/metrics.go` · the untokened-entry series and its `incSlotShutdownUntokenedEntry` accessor.
- `cmd/lenny-gateway/metricsbackfill.go` · the `SlotReclaim` hook wiring beside the `SlotFailure` wiring.
- `docs/reference/metrics.md` · the two counter rows and the leaked-slots gauge row.
- `docs/reference/state-machines.md` · the per-slot sub-state table's new row, its
  `receiving_uploads` → `running` trigger cell, its
  `slot_cleanup` → `released` trigger cell and the page's `slot_cleanup -> leaked` clause, and
  the pod state machine paragraph's projection
  clauses and the projection input added to that paragraph's opening enumeration.
- `docs/reference/adapter-contract.md` · the `Shutdown` row, the `DemoteSDK` row, the
  `ReportSessionScrub` row, and the added bind-attempt paragraph.
- `docs/reference/error-catalog.md` · the `SETUP_COMMAND_FAILED` row's four replaced
  sentences and its replaced remedy cell.
- `docs/reference/execution-modes.md` and `docs/operator-guide/security-principles.md` · the
  reporting clause deleted from each page's per-slot cleanup sentence.
- The comment carriers CODE-10's grep returns, the four sites CODE-11 names, and the comment
  carriers CODE-12's command returns · the reduction each sub-block states under
  `### Comment-carrier reduction: shared invariants`. The CODE-10 and CODE-12 sets are closed by
  their commands at application time and are not enumerated here. A file one of these entries
  alone opens is opened for comment prose only, so it counts toward no impact row's
  file-collision ground in the summary; a file another entry also opens is listed under that
  entry for its own edit.
- Tests: `pkg/adapter/bindattempt_test.go`, `pkg/adapter/bindattempt_orderings_test.go`,
  `pkg/adapter/slotsession_test.go`, `pkg/adapter/socketruntime_test.go`,
  `pkg/adapter/sdkwarm_test.go`,
  `pkg/adapter/export_test.go`, `pkg/adapter/usage_test.go`,
  `pkg/adapter/adapterevents_test.go`, `pkg/adapter/podmcp_arming_internal_test.go`,
  `pkg/adapter/exportpaths_test.go`, `pkg/adapter/one_session_only_test.go`,
  `pkg/adapter/manifest_fields_test.go` (`setSessionLeasesForTest` at `:220` takes the mechanical
  `slotResolve{allowCreate: true}` widening),
  `pkg/adapter/integrationlevel_test.go`, `pkg/adapter/credexpiry_test.go`,
  `pkg/adapter/checkpoint_stream_test.go`, `pkg/adapter/tracingcontext_addressing_test.go`
  (these four and `one_session_only_test.go` hold every `ReleaseSlotForTest` call site the tree
  holds today, and each gains the context argument; the new start-versus-reclaim rollback case in
  `slotsession_test.go` is written against the new signature, with its `Resume` row supplying an
  already-cancelled context),
  `pkg/adapter/holdstate_test.go`, `pkg/sandbox/slotstate/slotstate_test.go`,
  `pkg/gateway/runtime/adapterclient/client_test.go`,
  `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
  `pkg/gateway/podlifecycle/podsession/binder_test.go`,
  `pkg/gateway/podlifecycle/podsession/binder_envtest_test.go`,
  `pkg/gateway/sessionserver/slotretry_test.go`,
  `pkg/gateway/sessionserver/slotretry_load_test.go`,
  `pkg/gateway/sessionserver/start_test.go`,
  `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`,
  `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go`,
  `tests/tier3_contract/adapter_bind_attempt/`,
  `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go`,
  `tests/tier4_integration/concurrent_workspace_test.go`,
  `tests/tier4_integration/recycle_scrub_path_test.go`,
  `tests/tier4_integration/token_service_unavailability_guard_test.go` and
  `tests/tier8_chaos/token_service_unavailability_guard_test.go` (each passes a non-empty bind
  attempt to `AssignCredentials`, landing at S13 with the signature change),
  `tests/tier7a_load_local/slot_bind_attempt_race_test.go`,
  `tests/tier7a_load_local/slot_reclaim_hold_race_test.go`,
  `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`,
  `tests/tier8_chaos/compensation_loss_test.go`,
  `tests/tier9_security/slot_credential_reclaim_fence_test.go`,
  `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`,
  `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`,
  `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`,
  `tests/tier11_docs/slot_compensation_metric_reference_test.go`, which is new and takes a
  `slotAddressCaseFiles` row and a `tests/spec-map.json` §16.1 credit in the step that creates it,
  and
  `tests/tier0_static/spec_map_slot_address_registration_test.go`.
- Every other file that `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/` names ·
  the mechanical `UnconditionalTeardown: true` edit alone. They are not enumerated here, because
  that grep defines the set and each edit is one field on an ordinary teardown call. The edit
  adds a field to an existing literal, creates no case and adds no call into the slot claim
  surface, so a swept file enters no row in `slotAddressCaseFiles` and takes no
  `tests/spec-map.json` entry.
- Every other file that `grep -rn
  "adapterv1\.\(FinalizeWorkspaceRequest\|RunSetupRequest\|AssignCredentialsRequest\|ResumeRequest\|PrepareWorkspaceRequest\){"
  --include=*_test.go pkg/ tests/` names · the mechanical non-empty `BindAttempt` edit alone,
  or `MidSession: true` and no token where the literal stands for a §7.4 mid-session call.
  The `adapterv1.` qualifier is load-bearing: without it the pattern also matches
  `tokensv1.AssignCredentialsRequest` and `podsession.ResumeRequest`, which belong to other
  services and carry no such field. The `FinalizeWorkspaceRequest` in
  `TestFinalizeWorkspaceMidSessionOverlaysAndSignals_spec_7_4_433`
  (`pkg/adapter/files_updated_test.go`) already sets `mid_session` and carries no token, so it
  stays unchanged.
  They are not enumerated here, because that grep defines the set and each edit is one field
  on an existing literal. The edit creates no case and adds no call into the slot claim
  surface, so a swept file enters no row in `slotAddressCaseFiles` and takes no
  `tests/spec-map.json` entry.
- Every other file that `grep -rn "ReleaseSession(" --include=*_test.go pkg/ cmd/ tests/` names,
  whose type is assigned to a `podsession.Binder.Credentials` field · one added
  `Release(leaseID string)` method alone, a no-op, except in
  `pkg/gateway/podlifecycle/podsession/binder_test.go`, where `fakeAssigner` records its calls.
  A type that already declares the method takes no edit. They are not enumerated here, because
  the grep and the field assignment define the set and each edit is one method on an existing
  fake. The edit creates no case and adds no call into the slot claim surface, so a swept file
  enters no row in `slotAddressCaseFiles` and takes no `tests/spec-map.json` entry.
