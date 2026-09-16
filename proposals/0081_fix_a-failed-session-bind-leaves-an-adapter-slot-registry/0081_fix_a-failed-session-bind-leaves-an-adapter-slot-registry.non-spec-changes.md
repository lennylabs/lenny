# Non-spec changes: A failed session bind leaves a stale adapter slot registry entry

These changes are staged. Applying them is the implementation's work, under the
implementation checklist. Every code deliverable here lands after the spec deliverables it
implements.

The deliverables are listed below in reading order. Build order differs and is the
implementation checklist's: the spec edits land first, then SCHEMA-1's single proto window and
its regenerated stubs, then CODE-7's typed-error translation, then the adapter deliverables
CODE-6, CODE-1 and CODE-2, then the gateway deliverables CODE-8, CODE-4 and CODE-5, then
CONF-1, DOCS-1 and DOCS-2.

## Design (implementation-facing)

The mechanism is one predicate at one chokepoint on the adapter, one two-field precondition on
`Shutdown`, and a per-attempt token the gateway mints and carries.

**The token.** A bind attempt mints an opaque string from `crypto/rand` before it issues its
first RPC, carries that string on every request of that attempt that can create or resolve the
pod's slot registry entry, and names it again on the compensating `Shutdown` it sends when the
attempt fails. The adapter treats the value as opaque and never parses or orders it. The
adapter writes it onto the entry it creates and never onto an entry it resolved, in either
direction, so the first attempt to create an entry owns that entry until the entry is removed.
That write rule is what makes the mechanism monotone without a counter: a compensation can only
match an entry its own attempt created.

**The chokepoint.** `ensureSlotStateLocked` (`pkg/adapter/slot.go`) is the adapter's only
resolve-or-create step and has three production callers, `ensureSlotPaths`,
`assignCredentialsSlot` and `claimSessionSlotUnderLock`. Those three cover every RPC that can
create or resolve an entry: `PrepareWorkspace`, `FinalizeWorkspace` and `RunSetup` through the
first, `AssignCredentials` through the second, and `StartSession`, `Resume` and
`ConfigureWorkspace` through the third. One predicate at that function covers all seven. A
predicate placed in the handlers instead would leave the three claim callers ungated, and a
`Resume` landing on a leaked entry would then resolve that entry without comparing and set
`started`, destroying a live resumed session.

`validateBindFields` runs at the handler's entry, before the resolve, and is not one of the
five rules below; the rules below are reached only by a well-formed request. The predicate
itself is evaluated under `s.mu`, before anything is written, as part of the same indivisible
resolve-or-create-and-stamp step:

1. The entry is absent and the caller may not create: `FailedPrecondition`. Nothing is created.
2. The entry is absent: create it, stamp the caller's token on it, and return it.
3. The entry is present, both tokens are non-empty, and they differ:
   `SLOT_BIND_ATTEMPT_SUPERSEDED` on `codes.Aborted`.
4. The entry is present, its session has already started, and the caller does not permit a
   started entry: `SLOT_BIND_ALREADY_STARTED` on `codes.FailedPrecondition`.
5. Otherwise return the entry, writing nothing.

Rule 5 covers both "the entry carries no token" and "the caller asserts no identity". Rules 3
and 4 are the identity gate and the phase gate. They share one critical section and one call
graph, which is why they land together rather than in sequence.

**The atomicity requirement.** The resolve, the create, the stamp and the comparison are one
step under the lock that guards the registry. Without that clause first-writer-wins is not
implementable from prose, and a third-party adapter that resolves, releases its lock, and then
stamps conforms to the letter while being broken in fact. SPEC-5 states the clause and CONF-1
tests it.

**The mid-session conditioning.** The §7.4 mid-session upload is issued against an entry whose
token the gateway does not hold, because the binding predates the request and may have been
made by another replica. The wire rule is therefore conditioned rather than absolute: on `PrepareWorkspace` and
`FinalizeWorkspace`, the two requests that carry both fields, `bind_attempt` is non-empty
exactly when `mid_session` is false, while `RunSetup`, `AssignCredentials` and `Resume` carry
no `mid_session` marker and always carry a non-empty token. A mid-session
request carrying a token, and a non-mid-session request carrying none, are both
`INVALID_ARGUMENT`, refused by `validateBindFields` before the handler resolves anything. A mid-session request asserts no identity under rule 5, cannot create under
rule 1, and is exempt from the phase gate. What keeps that safe is the shipped admission guard:
a mid-session upload is admitted only for a session with a live binding in the replica's
`podRegistry` (`pkg/gateway/sessionserver/upload_to_session.go`), which a foreign attempt
cannot obtain. Reading that guard is a precondition of landing CODE-6's rule 1.

Conditioning on `mid_session` is also what closes a hole the resolve ordering opens today.
`FinalizeWorkspace` calls `ensureSlotPaths` at `pkg/adapter/staging.go:181` and reads
`req.GetMidSession()` at `:239`, fifty-eight lines later, so a mid-session finalize for a
session the pod holds no entry for creates an entry and a full on-disk tree before the
mid-session branch is consulted. `RunSetup` and `PrepareWorkspace` have the same unconditional
ordering. The entry created that way would carry no token, every attempt would be admitted
against it, no compensation could collect it, and it would be permanent. Reading `mid_session`
before the resolve is what makes rule 1 reachable, and that requires the field on
`PrepareWorkspaceRequest` as well, which is why it belongs in the same proto window as the
token.

**`Shutdown` says which teardown it is asking for.** `bind_attempt` and
`unconditional_teardown` sit beside the address on `ShutdownRequest`, and exactly one of a
non-empty token and a true boolean is required. Neither or both is `INVALID_ARGUMENT` and
performs nothing. The alternative, treating an empty token as the unconditional form, is
fail-open on a destructive path, in the category `code-best-practices.md` names: fail closed on
admission and credential handling, and on doubt deny. It also inverts the conformance
obligation, because a third-party adapter that refuses a `Shutdown` naming nothing would be
safer than the specification and non-conforming. Both fields are bare scalars with no wire
presence, so §4.1's rule that no operation is selected by a field's presence standing in for a
scope is untouched; `ShutdownRequest.recycle` is the shipped precedent for a disposition
carried beside the address.

**The reclaim hold.** The token makes a late reclaim refusable. It does not make an admitted
reclaim exclusive while it tears state down. `removeSlotTree` deletes paths derived from the
slot identifier, `SlotID == SessionID` makes that identifier equal across attempts, and the
destructive steps read no registry state, so the deregistration alone does not keep a successor
out of them. The hold is taken in the same critical section as the deregistration, released on
every return path, and refuses a bind onto a held identifier as a transient condition. It is
retained from the epoch design unchanged, because it closes an ordering the token does not.

**The two residues the token closes that the epoch could not.** A bind that fails inside its
first entry-creating RPC reclaimed unfenced under the epoch, because the epoch travelled on a
response and a failing RPC returns none. The token is held by the caller before the first RPC,
so that window does not exist. And two attempts sharing one entry could not be told apart by a
per-entry counter, which is why the proposal recorded the cross-attempt case as unclosable. The
token is the per-attempt discriminator that case needed, for the attempts whose requests carry
one. `StartSession` and `ConfigureWorkspace` carry none, and SPEC-5 records the entry they can
create as a residue.

**What the caller holds, and why nothing travels on a response.** The epoch was latched off the
responses one connection reported, so a fresh connection held none and the compensation was
pinned to the connection the failed attempt already had. The token is held by the caller
independent of any connection and independent of any response, so a compensation on a fresh
connection is exactly as fenced as one on the original. The no-re-dial rule loses its
correctness load and becomes a statement of preference, which SPEC-2 restates. That is also what
makes a durable compensation record buildable later; this proposal does not stage one.

**Where the gateway mints.** `materializeSlot`, `Binder.Prepare` and `Binder.Resume` each mint
one token at the top of the attempt. `Binder.Launch` mints none: it runs in a separate HTTP
request from the `Prepare` that created the entry, it issues only `ConfigureWorkspace` or
`StartSession`, and it sends no slot compensation, so a token gate on its requests would refuse
it against the entry `Prepare` legitimately created. `Binder.Prepare` mints because the wire
rule refuses an empty token on the four non-mid-session bind-sequence requests it sends, and it
needs no compensating `Shutdown`, because every `Prepare` failure drains the pod and the entry
dies with it.

**The refusal must not drain a healthy pod.** `failPhase`
(`pkg/gateway/podlifecycle/podsession/binder.go:1072-1082`) gates `releaseCredentials` on its
`leaseAssigned` argument and calls `b.drain` outside that block, so it drains on every call.
`Binder.Launch`'s reclaim closure passes a literal true and `Binder.Prepare`'s passes a mutable
flag; either way the pod is drained. A refusal on either new code means this pod is serving the
session correctly, so draining it is the worst available response. CODE-8 makes both closures
return the refusal without calling `failPhase`, and it is a hard blocker of the gates rather
than an independent cleanup: without it the first thing the gates do in production is retire
live pods.

**How the refusal reaches the gateway.** `adapterv1.Error` declares `code`, `category`,
`message`, `retryable` and `docs_url` and has no reason field, so the distinction between a
token mismatch and a started-entry refusal has to be two `ErrorCode` values. On the gateway
side every consumer of a bind failure matches on Go types, and the only gRPC status-detail
reader in the whole gateway is `pkg/gateway/runtime/adapterclient/client.go:333-341`, which
recovers `RunSetup` partial outputs. CODE-7 adds the translation from the status detail to two
Go sentinels, following that reader's pattern. Four deliverables have nothing to match on
without it.

**What the change costs.** One additive proto window: two enum values, one enum, and ten fields,
no field removed, no field renumbered, and no RPC added, so `buf breaking` has nothing to fire on.
On the gateway side: one mint helper, one in-process error field, one existing parameter
threaded to two more call sites, two error sentinels, and one predicate split. No new frame, no
new flag, and the counters CODE-9 states.

The proto edit lands under rule S-2's second window rather than against it. S-2 reserves the
first window to R1b and states that a later step needing a field the plan did not enumerate
opens a second narrow window, whose precondition is that every in-flight `pkg/adapter` handler
edit has merged first. R1b's end state is in the tree, and the file has already been reopened
once, for proposal 0076's comment-only edit. There is no third window, which is why the phase
gate, the identity gate, the mid-session conditioning and the `Shutdown` precondition all land
in this one.

## Staged code changes

### CODE-1 · pkg/adapter/session.go · `Shutdown` requires exactly one of the two teardown fields, compares the bind attempt before the deregistration, releases the slot and its tree for any entry removed, and tears the runtime down only for a started session

W7's split of the `if bound` gate is folded into this deliverable rather than named separately:
the split and the compare sit in one handler and one pair of critical sections, and separating
them would stage two rewrites of the same forty lines.

Targets:

- `Shutdown`'s clause two: the two-field precondition, the bind-attempt comparison, the
  `bound := removed && st.sessionID != ""` gate, the `if bound { … }` block, and the response
  construction.
- `Shutdown`'s doc comment.
- `pkg/adapter/slotsession.go` · `deregisterSlotLocked`'s doc comment only. No body change: it
  already cancels every armed expiry timer on removal.
- `pkg/adapter/runtimegeneration.go` · a `runtimeHoldsLocked` accessor beside
  `runtimeIdleLocked`, reporting whether `runtimeLive` holds the named session. Callers hold
  `s.mu`. It reads state that already exists and adds none: `noteRuntimeStartedLocked` is the
  only writer that sets membership and `noteRuntimeClosed` the only one that clears it.
- `pkg/adapter/bindattempt.go` · the refusal sentinels, the reclaim hold and
  `reclaimSlotLocked` are CODE-6's, which lands first. This deliverable inserts into them and
  defines none of them.

**The two-field precondition, checked before the lock is taken.** `Shutdown` performs only the
empty-session-id check before `s.mu.Lock()` today, and the precondition joins it there:

```go
// spec: §4.7.1 (role and gateway RPC contract). A Shutdown states which
// teardown it is asking for. Exactly one of a non-empty bind_attempt and
// unconditional_teardown = true is carried. Neither or both is
// INVALID_ARGUMENT and performs nothing, so a caller that forgets the token
// gets an error rather than a destroyed session.
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
critical section. The rule table:

| Request | Entry | Answer | Removes |
|:--|:--|:--|:--|
| `unconditional_teardown` true | one is held | `reclaimed` | yes |
| `unconditional_teardown` true | none | `absent` | no |
| non-empty `bind_attempt` | none | `absent` | no |
| non-empty `bind_attempt` | token empty | `superseded` | no |
| non-empty `bind_attempt` | token differs | `superseded` | no |
| non-empty `bind_attempt` | token matches | `reclaimed` | yes |

The empty-token row fails closed and is reachable. With `materializeSlot`, `Binder.Prepare`
and `Binder.Resume` all minting, and with CODE-6's rule 1 removing the mid-session creation
route, the remaining producer of an unstamped entry is a `StartSession` or a
`ConfigureWorkspace` that resolved none and created one, because neither carries a token. The
untokened-entry counter below names the row, so that entry, and a future caller that creates one
without a token, are visible rather than silent.

```go
// spec: §4.7.1 (role and gateway RPC contract). The comparison is inside the
// same critical section as the deregistration, because deregisterSlotLocked
// deletes unconditionally once it finds an entry. A request naming a bind
// attempt performs the slot release and the runtime teardown only for an
// entry carrying that attempt, and performs neither otherwise. Both refusals
// remove nothing and are successful answers: ABSENT for a session the adapter
// holds no entry for, SUPERSEDED for one another bind attempt owns or one
// carrying no attempt at all.
s.mu.Lock()
if !unconditional {
    cur, ok := s.slotStateLocked(sessionID)
    switch {
    case !ok:
        s.mu.Unlock()
        return &adapterv1.ShutdownResponse{
            ExitedCleanly: true,
            SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT,
        }, nil
    case cur.bindAttempt == "":
        // The fail-closed row. Nothing a compensated path produces reaches
        // it; the counter exists so that stops being true loudly.
        s.noteShutdownMetUntokenedEntry(sessionID)
        s.mu.Unlock()
        return &adapterv1.ShutdownResponse{
            ExitedCleanly: true,
            SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED,
        }, nil
    case cur.bindAttempt != attempt:
        s.mu.Unlock()
        return &adapterv1.ShutdownResponse{
            ExitedCleanly: true,
            SlotReclaim:   adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED,
        }, nil
    }
}
st, removed, boundRemains, release := s.reclaimSlotLocked(sessionID)
```

**The split gates.** The remainder of the critical section captures three predicates and they
never diverge afterwards:

```go
// spec: §4.7.1 (role and gateway RPC contract). The runtime teardown runs
// for a session whose start the adapter has admitted. st.started is that
// predicate: it is set inside claimSessionSlotUnderLock's critical section,
// which every start RPC enters, before Runtime.Start, so gating on it fails
// closed on a start still in flight, which is torn down rather than skipped.
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
// and the hold are one critical section, so no bind is admitted between them,
// and the release is deferred rather than written at each return: Runtime.Close
// reaches three implementations and a child process and removeSlotTree reaches
// the filesystem, so a panic out of either would otherwise hold the identifier
// for the life of the pod. reclaimSlotLocked returns a non-nil release on every
// path, so the defer is unconditional and a call that removed no entry takes a
// no-op.
defer release()
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

// spec: §4.7.1 (role and gateway RPC contract). The slot release runs for any
// entry the call removed, bound or not. It follows the drain and the close so
// the agent process is not reading a credential file the teardown has already
// removed inside the §15.4.2 grace window. Widening the gate from bound to
// removed newly reclaims the slot tree ensureSlotStateLocked created, the
// workspace and the empty credential directory, for a registered-but-unbound
// entry. A bound entry's credentials.json was already reclaimed inside the
// shipped bound gate, and the armed §4.9 expiry timers are cancelled outside
// it, by deregisterSlotLocked, for every entry it removes.
treeErr := error(nil)
if removed {
    treeErr = removeSlotTree(st)
    s.cancelPodMCPIfRuntimeIdle()
}

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

The response becomes:

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

`slot_reclaim` is populated on the unconditional form as well as the fenced one, so the gateway
reads one field on every answer and never has to infer an outcome from the request it sent.

`exited_cleanly` carries one rule, keyed on the same §6.2 running boundary the rest of this
change installs: the response reports a clean exit when the runtime close succeeded and, for a
slot the pod's shared runtime process was never given, when the slot release also completed.
The gate is `live` rather than `st.started` because `st.started` is set inside
`claimSessionSlotUnderLock` before `Runtime.Start` runs, so a start still in flight is
`started` and pre-running, and gating on `started` would discard the tree-removal error for
exactly the reclaim §7.1 sends on a cancelled context. Reading `live` leaves the session-end
classification untouched: every path that admits a start reaches `noteRuntimeStarted`
unconditionally on success (`pkg/adapter/session.go:163`, `resume.go:144`, `sdkwarm.go:261` on
the freshness arm), and the two `noteRuntimeClosed` callers outside `Shutdown`
(`pkg/adapter/holdstate.go:251`, `sdkwarm.go:297`) each remove the registry entry in the same
pass, so a session the runtime was given and that reaches an ordinary session end is in
`runtimeLive` when its `Shutdown` arrives and answers on `closeErr` alone as it does today.

Doc-comment work on `Shutdown`:

- State the two-field precondition first, because it is the handler's new outermost branch:
  exactly one of a non-empty `bind_attempt` and `unconditional_teardown` is carried, and
  neither or both is `INVALID_ARGUMENT` and performs nothing.
- State the comparison second, with its rule table: a request naming a bind attempt performs
  the slot release and the runtime teardown only for the entry carrying that attempt and
  performs neither otherwise; the two refusals are successful answers that remove nothing; and
  an entry carrying no attempt at all is refused rather than removed, which is the fail-closed
  row. Name the two refusals' outcomes so a reader of the handler does not have to open the
  proto to learn what `SUPERSEDED` and `ABSENT` mean to the gateway.
- State the hold and why it is deferred, in the terms the code comment above carries: the
  destructive steps resolve from the slot identifier rather than from the entry, so the
  deregistration alone does not keep a successor out of them, and a panic out of
  `Runtime.Close` or `removeSlotTree` would hold the identifier for the life of the pod if the
  release were a statement at each return.
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
- Note that `cancelPodMCPIfRuntimeIdle` under `removed` is safe: it is double-guarded by
  `runtimeIdleLocked` and `mcpArmingHeldLocked` (`pkg/adapter/slotsession.go:238-260`), so a
  shutdown of an unbound entry on a pod whose armed session still holds a slot cancels nothing.

`deregisterSlotLocked`'s doc comment gains one sentence recording that its unconditional timer
cancellation is now relied on by the unbound path as well as the bound one.

### CODE-2 · pkg/adapter/runtimegeneration.go, pkg/adapter/session.go, pkg/adapter/resume.go · a start confirms the registry still holds its own attempt's entry before recording the runtime as holding the session

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
// noteRuntimeStarted records that sessionID has been given to the pod's one
// shared runtime process, and reports whether the record was taken. It runs
// immediately after a successful start. It refuses in the two states a §7.1
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
// spec: §7.1 (normal flow); §4.7.1 (role and gateway RPC contract); §15.4.3
// (runtime integration levels).
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
    // spec: §7.1 (normal flow). The reclaim removed this slot while
    // Runtime.Start ran. Take the session back off the shared runtime process
    // and refuse the start. No cleanup outcome is reported: the slot never
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
(`pkg/gateway/sessionserver/start.go:2766-2779`, `:2873-2879`). No change is staged to that
switch. `Aborted` is also the code the identity gate answers, which is deliberate: both are
"another attempt owns this", both clear on a further attempt, and CODE-5's single `Aborted` arm
on `isTransientPodClaimError` covers the rollback, the reclaim-hold refusal, and the identity
gate with one arm rather than three. It is unused on the `StartSession` and `Resume` handlers,
where `FailedPrecondition` already refuses an unconfigured runtime (`pkg/adapter/session.go:104`)
and an unconfigured workspace base or checkpoint transport (`pkg/adapter/resume.go:33,:42`).

Scope of the call-site change:

- The compensation can race any RPC that admits a start on a slot the gateway may reclaim, and
  CODE-4 stages it at two such sites. Both take the rollback. `pkg/adapter/session.go:163`
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
  (`pkg/gateway/podlifecycle/podsession/binder.go:1009`), the exclusive path CODE-4 does not
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

### CODE-3 · pkg/sandbox/slotstate/slotstate.go · the per-slot edge list gains the pre-running cleanup edge

`ValidTransitions()` gains `{ReceivingUploads, SlotCleanup}`, and the doc comment's edge list
above it gains the matching line:

```go
//	receiving_uploads → slot_cleanup    (bind abandoned before the runtime is given the session)
```

`ValidTransitions()` and `TestValidTransitions_spec_6_2`'s `want` list are one statement of the
edge set, so CODE-3 moves both in its own step. Nothing compares either against
`spec/06_warm-pod-model.md`, so SPEC-4 and CODE-3 are separate steps. `IsValid(SlotAssigned,
Running)` stays illegal.

### CODE-4 · pkg/gateway/podlifecycle/podsession · the gateway mints a bind attempt, carries it on the bind sequence, compensates every post-connection bind failure and every failed resume, and scopes the lease release to the attempt

Targets:

- `bindattempt.go` (new in `pkg/gateway/podlifecycle/podsession`) · `newBindAttempt`, and the
  counters this deliverable states.
- `slotfailure.go` · `SlotBindError` gains a `Leaked bool` field.
- `slotbinder.go` · a new `slotCleanupBudget` helper and a new `compensateFailedSlotBind`
  method; `materializeSlot` splits into a stage runner and a compensating wrapper, and mints
  the attempt's token at the top; `ReleaseSlotReservation` takes the disposition;
  `BindReservedSlot` and `ClaimSlot`'s connect-stage release pass it; `Binder.ReleaseSlot`'s
  `Shutdown` and `ShutdownRecycle` calls set `unconditional_teardown`.
- `binder.go` · `Binder.Prepare` mints and carries; `Binder.Resume` mints, carries, and
  compensates before `cl.Close()`; `releaseResumeSlot` takes the disposition and returns its
  release error; `Binder.shutdownAdapter` and the recycle path set `unconditional_teardown`.
- `pkg/gateway/runtime/adapterclient/client.go` · the six requests carry `bind_attempt`,
  `PrepareWorkspace` carries `mid_session`, and the two `Shutdown` forms become two methods.

**The mint.**

```go
// newBindAttempt mints the opaque per-attempt token §4.7.1 fences the slot
// registry entry with. It is called once per bind attempt, before that
// attempt's first RPC, and the value is carried on every request of the
// attempt that can create or resolve the entry and named again on the
// compensating Shutdown.
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
mints none. It runs in a separate HTTP request from the `Prepare` that created the entry, it
issues only `ConfigureWorkspace` or `StartSession`, and it sends no slot compensation, so a
token on its requests would be refused against the entry `Prepare` legitimately created. Its
requests carry an empty `bind_attempt` and `StartSessionRequest` and `ConfigureWorkspaceRequest`
carry no such field at all.

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
| crash takeover and the coordinator-handoff teardown | the `Shutdown` each issues on adoption |

`Client.Shutdown` and `Client.ShutdownRecycle` set the field themselves rather than taking it
as a parameter, so no caller can forget it and the compensation is the one path that reaches
the fenced form. The fenced form is a separate method, `Client.ShutdownReclaim`, which sets
`bind_attempt` and leaves `unconditional_teardown` false.

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
// The reclaim is sent on the connection the failed stage still holds because
// that connection is open and costs nothing, rather than for correctness: the
// token is held by this attempt independent of any connection, so a
// compensation on a fresh connection would be exactly as fenced.
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

**The outcome mapping, at the caller.** An unrecognized outcome is not a leak:

```go
// spec: §6.2 (pod state machine). The leaked disposition withholds the pod's
// slot-counter decrement for the life of the pod, and UnhealthyThreshold is
// int((maxConcurrent+1)/2), so at maxConcurrentSessions: 2 the threshold is 1,
// Unhealthy compares with >=, and RecordLeak increments a map that is never
// window-pruned. One unknown outcome would therefore drain the pod. An outcome
// this build does not recognize is counted and treated as not leaked.
func slotReclaimLeaked(outcome adapterv1.SlotReclaimOutcome, cleanly bool, err error) bool {
    switch {
    case err != nil:
        return true
    case outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_RECLAIMED:
        return !cleanly
    case outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_SUPERSEDED,
        outcome == adapterv1.SlotReclaimOutcome_SLOT_RECLAIM_OUTCOME_ABSENT:
        return false
    default:
        slotReclaimOutcomeUnrecognized.Inc()
        return false
    }
}
```

`SUPERSEDED` and `ABSENT` are completed reclaims that correctly performed nothing. On `ABSENT`
the adapter holds nothing for the session; on `SUPERSEDED` another attempt owns the slot
identifier and everything under it, or the entry carries no token at all, which is what a
`StartSession` or a `ConfigureWorkspace` creates. Either way this attempt owns nothing the
reclaim could release. An untokened entry survives the reclaim and is released only by an
unconditional teardown or by the pod's retirement, which SPEC-5 records as a residue. Reading `SUPERSEDED` as leaked would withhold
the pod's slot-counter decrement for the life of the pod on the common retry case.

Correcting a claim the shipped text carried: the disposition does not travel through
`ReleaseSlotReservation`'s own parameter list into `claimer.ReleaseSlot`. That function calls
`claimer.ReleaseSlot(ctx, sandboxName, false, false)` at `slotbinder.go:502` and takes no such
parameter; the only `leaked=true` producer is `Binder.ReleaseSlot` (`slotbinder.go:528`).

**The reason string.** The reclaim's reason is `"slot_bind_failed"`, and where the reclaim
reaches a session whose start the adapter admitted, the intra-pod `terminate` frame carries
`session_complete`. `drainReason` maps a §4.7 `ShutdownRequest.reason` onto the closed
four-value `terminate` enum and returns `session_complete` for every value outside it
(`pkg/adapter/session.go:309-321`), so the compensation mints no wire value. The shipped §11.4
full-revoke path already takes that same default with `"USER_REVOKED"`. The frame's schema
requires `deadlineMs` and fixes its minimum at 100, and the budget's five-second floor puts
half the budget at 2500ms or above, so the value this call sends satisfies that minimum on
every pool configuration. A reclaim of a bound-but-unstarted session sends no frame at all,
because the drain sits inside CODE-1's `started` block.

**The wrapper.** `materializeSlot` becomes a wrapper so no stage can be added later without the
compensation, and the wrapper is where the token is minted, where the refusal is read, and
where the lease release runs:

```go
func (b *Binder) materializeSlot(
    ctx context.Context, req SlotBindRequest,
    sandboxName, slotID, podIP, workspaceBase string,
    cl *adapterclient.Client,
) (*BindResult, error) {
    attempt := newBindAttempt()
    res, minted, err := b.materializeSlotStages(ctx, req, sandboxName, slotID, podIP, workspaceBase, cl, attempt)
    if err != nil {
        // spec: §4.7.1 (role and gateway RPC contract). A typed refusal means
        // this pod is serving the session correctly under another attempt's
        // identity, or the session has already started. This attempt created
        // nothing on the pod and owns nothing there, so it sends no
        // compensation: a compensation naming this attempt's token would
        // answer SUPERSEDED and remove nothing, and one naming nothing would
        // be the unconditional teardown of a live session.
        refused := errors.Is(err, adapterclient.ErrSlotBindAttemptSuperseded) ||
            errors.Is(err, adapterclient.ErrSlotBindAlreadyStarted)
        var sbe *SlotBindError
        if errors.As(err, &sbe) && !refused {
            outcome, cleanly, cerr := b.compensateFailedSlotBind(
                ctx, cl, req.SessionID, attempt,
                req.CleanupTimeoutSeconds, req.MaxConcurrentSessions, sandboxName, slotID)
            sbe.Leaked = slotReclaimLeaked(outcome, cleanly, cerr)
            noteCompensationOutcome(outcome)
        }
        // spec: §7.1 (normal flow); §4.9 (credential leasing service). Every
        // stage that mints a §4.9 lease runs inside materializeSlotStages, so
        // the failed attempt returns its own leases here rather than in the
        // compensation, which the resume path also calls and which mints none.
        // The call is unconditional and sits outside the errors.As guard:
        // assignSlotCredentials mints per provider and returns on the first
        // failure, so a failed credential-assignment stage can already hold
        // minted leases, and a future stage error that is not a *SlotBindError
        // must still return them. It is scoped to the leases this attempt
        // minted rather than to the session, because a session-wide walk
        // strips a successor's leases. On a typed refusal this attempt minted
        // none, so the call is a no-op and needs no guard of its own.
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
`b.releaseCredentials(sessionID)`, the session-wide walk, stays where it legitimately belongs,
on the session-end path. The lease store is keyed by lease identifier and each mint produces a
fresh one, so a second assignment for one session adds leases rather than replacing them, while
the adapter side replaces (`slotcreds.go:40-48`); under the identity gate a foreign
`AssignCredentials` is refused, so the double-count arises only from a retry of an attempt's
own request and is recorded among the accepted failure modes.

**Why the suppression needs `errors.Is` on a translated sentinel rather than `errors.As` on
`*SlotBindError`.** The refusal arrives as a gRPC status carrying an `adapterv1.Error` detail.
`errors.As(err, &sbe)` matches the gateway's own wrapper, which is present on a refusal and on
an ordinary stage failure alike, so it cannot tell them apart. CODE-7's translation is what
makes the two sentinels matchable, and `SlotBindError.Unwrap` returns the cause, so
`errors.Is` walks to the sentinel through the wrapper. A build in which CODE-7 has not landed
matches neither sentinel and compensates every failure, which is the shipped behaviour and is
why CODE-7 precedes this deliverable rather than accompanying it.

The two obligations sit at different levels because they reclaim different state. The
compensation is pod-side, so both bind paths and the resume path take it. The lease release is
gateway-side and belongs to the attempt that minted the lease, so it sits in this wrapper,
which is exactly the boundary of the stages that mint one.

Every stage inside `materializeSlotStages` is post-connection, so the compensation is
unconditional there except on a refusal. A stage whose failure sent no RPC (an upload-free plan
failing inside `stageWorkspace`) still sends it; the adapter answers `ABSENT` for a session it
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

**`Binder.Prepare` mints and carries, and sends no compensation.** Every `Prepare` failure runs
`failPhase`, which drains the pod (`binder.go:1072-1082`), so the entry dies with the pod and
there is nothing for a compensation to collect. What `Prepare` needs instead is CODE-8's
short-circuit: two concurrent `/finalize` calls (`start.go:2634`, `finalize.go:280`) produce two
`Prepare` attempts, and without it the loser's refusal drains the winner's pod. The abandoned
`Prepare` case on the exclusive path stays out of scope, as the problem statement records.

**`Binder.Resume` mints, carries and compensates.** `Resume` is the first and only
entry-touching RPC of its attempt and creates the entry through its own claim
(`pkg/adapter/resume.go:50`, ahead of every path-resolving step), so `ResumeRequest` carries
`bind_attempt` and the compensation names the same value. Its adapter-RPC failure branch
currently calls `cl.Close()` and then `releaseResumeSlot`; it becomes the refusal check, then
the compensation on the still-open connection, taking `req.SessionID`,
`req.CleanupTimeoutSeconds` and `req.MaxConcurrentSessions` off the `ResumeRequest`, then the
close, then the release carrying the outcome. It releases no credential lease: `Binder.Resume`
mints none, its §7.3 retry re-mints none, and the session may still hold the leases its
original bind minted, so returning them on a retryable failure would leave every later resume
of that session running with leases the gateway has already released. `releaseResumeSlot`
returns that release's error rather than only logging it, so the branch can read the release
outcome as the other two release sites do. The branch also returns its error with a
`*SlotBindError` in the chain, carrying the pod, the reserved slot id, the stage `"resume"`,
and a `Leaked` set to the same `compensation leaked || relErr != nil` discriminator
`applySlotRetryPolicy` and `BindReservedSlot` use, wrapped inside the message the branch returns
today. That is what carries the disposition to the accounting in CODE-5; `SlotBindError.Unwrap`
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

### CODE-5 · pkg/gateway/sessionserver/start.go · one accounting helper serves every bind path the reclaim obligation binds, and the resume classifier learns the adapter's transient code

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
stages and which `NonRetryable()` reports true for. That is the right classification: the
session has already started on this pod, and a retry of the same bind is not going to change
that.

**The §7.3 resume path reads a second classifier, and that one gains an arm.**
`isTransientPodClaimError` (`pkg/gateway/sessionserver/start.go:3648-3681`) is what
`holdOrFailOnResumeError` reads to decide whether a failed resume reverts the row to
`awaiting_client_action` for the client's `POST /v1/sessions/{id}/resume` or demotes it to
terminal `failed`. Every arm it carries today is a typed error or a sentinel, so a bare
`codes.Aborted` status matches none of them and falls through to `return false`, which demotes
a session the refusal says to retry. Add one arm after the existing switch and before that
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
	return false
```

The gateway reads the status code rather than CODE-6's adapter-local sentinel because that
sentinel is minted in the adapter process and no production file under `pkg/gateway` imports
`pkg/adapter`; the code is the part of the refusal that survives the wire. CODE-7's typed
sentinels are the other half and are matched where the distinction between the two new codes
matters, which is the compensation suppression and the reclaim closures; this classifier needs
only the code, so one arm covers all three producers. `status.Code` resolves through a wrapper
with `errors.As`, so the arm fires on the bare status and equally through the `*SlotBindError`
that CODE-4 makes `Binder.Resume` return. The wire envelope needs no change:
`writePodClaimError`'s default arm already answers the retryable 503 `RESUME_FAILED` with a
`Retry-After` for a cause it does not recognise, so the row-state classifier was the only half
that disagreed with it.

No in-gateway wait-and-retry is staged for the hold. The hold is bounded by the graceful window
the reclaiming request carries when it carries one and by that request's own deadline when it
carries none, plus the removal of the slot's directories, and a gateway-side wait would hold
the client's request open for that window and state the timeout in a second place. The attempt
spends a retry on the refusal instead, which is recorded among the accepted failure modes.

### CODE-6 · pkg/adapter/bindattempt.go, pkg/adapter/slot.go, pkg/adapter/slotsession.go, pkg/adapter/staging.go, pkg/adapter/slotcreds.go, pkg/adapter/holdstate.go · the bind-attempt token, the five-rule predicate at the one resolve chokepoint, the mid-session rule, the reclaim hold, and the shared resolve helper

This deliverable lands the mechanism the other adapter deliverables insert into, so it lands
before them and compiles alone. W8's per-slot guard is folded in rather than named separately,
because it is a lock-ordering rule over the same two functions and splitting it would leave an
intermediate commit in which the resolve is fenced and the destructive section is not.

What the epoch design contributed and what replaces it: the epoch counter, `slotState.epoch`,
`nextBindEpochLocked`, the seven epoch-bearing response fields, the seven handlers that
reported them, and the per-connection latch in `adapterclient` all come out. The reclaim hold,
`reclaimSlotLocked`, the refusal sentinel, and the five-site `slotResolveError` routing all
stay, unchanged in substance.

**New file `pkg/adapter/bindattempt.go`**, holding:

- The hold set's helpers, the three refusal sentinels, and the shared resolve helpers. The
  fields themselves are declared where their structs are: `Server.reclaiming
  map[string]struct{}` and `Server.slotGuards map[string]*sync.Mutex`, both guarded by `s.mu`,
  on `Server` in `pkg/adapter/server.go`, and `slotState.bindAttempt string` on `slotState` in
  `pkg/adapter/slot.go`. Go declares a struct's fields in the file that declares the struct, so
  those two files are opened by this deliverable even though they hold none of its logic.
- The `slotResolve` parameter type and its documentation:

  ```go
  // slotResolve carries what a caller asserts about the entry it is resolving.
  // Every field is a caller assertion rather than a discovered fact, and the
  // five-rule predicate in ensureSlotStateLocked is the only reader.
  //
  // spec: §4.7.1 (role and gateway RPC contract)
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
  steps that follow it. `release` is always non-nil and idempotent, so a caller defers it
  unconditionally and a path that removed nothing takes a no-op. Callers hold `s.mu`. This is
  the only site that takes a hold and the only one that ends one; the side table holds
  `struct{}` because nothing reads a value from it: no entry can stand under a held identifier,
  so there is no token to compare against.
- **Every site that deregisters an entry it then destroys goes through the helper**, rather than
  the hold being inserted at one of them. There are three in the tree and the hold does not
  distinguish them: `Shutdown` (`pkg/adapter/session.go`), `releaseSessionSlot`
  (`pkg/adapter/slotsession.go`, whose callers are the start-path rollbacks in `session.go`,
  `resume.go` and `sdkwarm.go`, `DemoteSDK` among them), and the §10.1.4 hold termination
  (`deregisterStartedSessions`, whose members are destroyed one at a time in
  `terminateHeldSession`, `pkg/adapter/holdstate.go`, up to ten seconds later). `heldSession`
  gains a `release func()` field so pass 1 carries each member's hold to the pass-2 call that
  ends it, and `terminateHeldSession` defers it. `deregisterSlotLocked` keeps its signature for
  the read-only caller that needs the deregistration without a destroy, and `deregisterSlot` is
  retired into the helper. In the same rewrite `releaseSessionSlot` stops discarding
  `removeSlotTree`'s error and logs it with `slog.Warn` naming the session identifier and the
  error, because nothing else records that cleanup's failure; `pkg/adapter/slotsession.go` gains
  a `log/slog` import for it, matching the structured adapter events `pkg/adapter/podscrub.go`
  already emits. The function still returns nothing and every caller still returns its own
  error, so no control flow changes.
- `errSlotReclaimInProgress`, a `codes.Aborted` status carrying `slot_reclaim_in_progress`, and
  `isSlotReclaimInProgress(err) bool`, both unchanged from the shipped text.
- `errSlotBindAttemptSuperseded(slotID string) error`, a `codes.Aborted` status whose detail is
  an `adapterv1.Error` with `code: ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED`, `category:
  TRANSIENT`, and a message naming the slot identifier and nothing else. The token itself is
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

**`ensureSlotStateLocked` takes the resolve and evaluates the five-rule predicate.** The hold
refusal stays first, before the map lookup, because a held identifier has no entry to return:

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
// claimSessionSlotUnderLock, cover all seven RPCs that can create or resolve an
// entry, so the predicate here is the whole of the admission rules that read
// the registry and no handler carries a second copy of them. Well-formedness of
// bind_attempt against mid_session is a property of the request alone, checked
// by validateBindFields at each handler that carries the fields, before the
// resolve, so a malformed request never reaches this function.
//
// spec: §4.7.1 (role and gateway RPC contract)
func (s *Server) ensureSlotStateLocked(slotID string, r slotResolve) (*slotState, error) {
    if _, held := s.reclaiming[slotID]; held {
        return nil, errSlotReclaimInProgress
    }
    st, ok := s.slots[slotID]
    switch {
    case !ok && !r.allowCreate:
        // Rule 1. A mid-session request resolves an entry that already exists
        // and never creates one. Creating here would mint an entry carrying no
        // token, which no attempt could ever reclaim and no sweep collects.
        return nil, status.Errorf(codes.FailedPrecondition,
            "slot %s has no registry entry for a mid-session request", slotID)
    case !ok:
        // Rule 2. The create branch, and the only writer of bindAttempt.
        st = s.newSlotStateLocked(slotID)
        st.bindAttempt = r.bindAttempt
        s.slots[slotID] = st
        return st, nil
    case r.bindAttempt != "" && st.bindAttempt != "" && st.bindAttempt != r.bindAttempt:
        // Rule 3, the identity gate.
        return nil, errSlotBindAttemptSuperseded(slotID)
    case st.started && !r.allowStarted:
        // Rule 4, the phase gate.
        return nil, errSlotBindAlreadyStarted(slotID)
    default:
        // Rule 5. The entry is returned and the token is not written, which
        // covers both "the entry carries no token" and "the caller asserts no
        // identity".
        return st, nil
    }
}
```

`ensureSlotPaths` takes the same parameter and passes it through:

```go
func (s *Server) ensureSlotPaths(slotID string, r slotResolve) (slotlayout.SlotPaths, error)
```

**Production call sites that must thread the parameter.** Ten, and each is named with the
resolve it passes:

| Site | `bindAttempt` | `allowCreate` | `allowStarted` |
|:--|:--|:--|:--|
| `staging.go` `resolvePrepareStagingDir`, from `PrepareWorkspace` | the frame's `bind_attempt` | `!midSession` | `midSession` |
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

Roughly twenty-two test call sites across the package thread the same parameter. They are not
enumerated here, because every one of them is a mechanical widening to
`slotResolve{allowCreate: true}` and the tier-1 work below names the files.

**`claimSessionSlotUnderLock`'s own refusal becomes typed.** Its shipped
`status.Errorf(codes.Unavailable, "session %s has already started on this pod")` at
`slotsession.go:84-86` is the phase gate stated in one handler's terms, and rule 4 now states it
for all seven. The `!idempotentRepeat` arm therefore returns `errSlotBindAlreadyStarted(slotID)`
rather than the untyped `Unavailable`, and the `idempotentRepeat` arm is unchanged: it still
returns `(false, false, nil, nil)` and the caller still treats it as a satisfied claim. In
practice rule 4 fires first, inside the resolve, so the arm is reached only when
`allowStarted` was true and the entry started between the resolve and the arm, which the lock
forbids; it is kept as the local statement of the same rule so a later reader of the claim does
not conclude that a started entry is admitted there.

**Rule 1 needs `mid_session` read before the resolve.** `FinalizeWorkspace` reads
`req.GetMidSession()` at `pkg/adapter/staging.go:239` today, fifty-eight lines after its
`ensureSlotPaths` call at `:181`. The read moves to the top of the handler and the value is
passed into the resolve; the branch at `:239` reads the same local. `RunSetup` and
`AssignCredentials` are never mid-session and pass `allowCreate` true and `allowStarted` false.

**The per-frame rule on `PrepareWorkspace`.** `PrepareWorkspace` is a client-streaming RPC that
resolves lazily on the first frame carrying a session identifier
(`resolvePrepareStagingDir`, `staging.go:78`). The handler therefore latches
`bind_attempt` and `mid_session` from the frame that resolves the entry and refuses any later
frame in the same call whose values disagree, with `codes.InvalidArgument`. It also resolves at
most once per call: the staged bytes are keyed by `sha256(uploadRef)` under the live session's
own staging directory, opened `O_CREATE|O_WRONLY|O_TRUNC` and named by refs the client supplies
verbatim, so a second resolve inside one call would let a later frame's assertion take effect
over bytes the first frame's assertion admitted. Both rules are conformance clauses CONF-1
tests.

**The five resolve sites keep the shared helper.** All five re-wrap any slot-resolve failure as
`codes.InvalidArgument` today, which `SlotBindError.Reason()` maps to
`SlotReasonWorkspaceValidation` and `NonRetryable()` reports true for. Routing the three
sentinels through `slotResolveError` is what keeps the identity gate's refusal transient and
the phase gate's refusal correctly permanent:

| Site | Wrap | Span category stamped at |
|:--|:--|:--|
| `pkg/adapter/staging.go` `resolvePrepareStagingDir` | `InvalidArgument` | the caller stamps `CategoryPermanent` |
| `pkg/adapter/staging.go` `FinalizeWorkspace`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/staging.go` `RunSetup`'s `ensureSlotPaths` | `InvalidArgument` | stamped beside the wrap |
| `pkg/adapter/slotsession.go` `claimSessionSlotUnderLock`'s `ensureSlotStateLocked` | `InvalidArgument` | none |
| `pkg/adapter/slotcreds.go` `assignCredentialsSlot`'s `ensureSlotStateLocked` | `InvalidArgument` | none |

No test asserts any of the five message strings, so the helper may reword none of them and the
wrap text stands as it is for every non-sentinel error.

**The per-slot guard across each handler's destructive section.** With both gates inside the
resolve's critical section the gate is a fence for the resolve itself. What remains unlocked is
the destructive work in `FinalizeWorkspace` and `RunSetup`, which runs against paths handed out
earlier: `ensureSlotPaths` takes `s.mu`, resolves, releases it, and returns a `SlotPaths` value
(`pkg/adapter/slot.go:140-148`), so two calls holding the same paths can interleave their
materialization. A retry of an attempt's own RPC reaches it, and so does a matching `Shutdown`
racing a `FinalizeWorkspace`.

`Server.slotGuards map[string]*sync.Mutex` is the remedy. The lock order is fixed and stated
once: `s.mu` is never taken while a slot guard is held, and a slot guard is acquired only after
`s.mu` has been released. The helper that hands one out takes `s.mu`, reads or creates the
mutex, releases `s.mu`, and then locks the mutex, so the two are never held at once. The guard
is dropped from the map by `reclaimSlotLocked`'s release, beside the reclaim hold, so the map
does not grow with the pod's lifetime. `FinalizeWorkspace` and `RunSetup` hold it across their
materialization; `PrepareWorkspace` holds it across each frame's write; `Shutdown` does not take
it, because the reclaim hold already excludes a concurrent bind and taking both would invert the
order.

### CODE-7 · pkg/gateway/runtime/adapterclient · the gRPC status detail becomes a Go typed error

Nothing in the gateway can match the two new refusals without this deliverable, which is why it
precedes CODE-4 and CODE-8 rather than accompanying them. Two facts fix its design.
`adapterv1.Error` declares `code`, `category`, `message`, `retryable` and `docs_url` and has no
reason field, so the distinction between the two refusals has to be carried by two `ErrorCode`
values rather than by a string. And every gateway consumer of a bind failure matches on Go
types: `isTransientPodClaimError` (`start.go:3648-3681`), CODE-4's suppression guard, and both
reclaim closures. `status.Convert` appears nowhere in `pkg/gateway`, and the only status-detail
reader in the whole gateway is `client.go:333-341`, which recovers `RunSetup` partial outputs.

```go
// ErrSlotBindAttemptSuperseded is returned when the adapter refused a
// bind-sequence RPC because the pod's slot registry entry for the session
// carries a non-empty bind attempt token different from this attempt's.
// Another attempt owns the entry and everything under it. The caller must not
// compensate: a Shutdown naming this attempt's token would answer superseded
// and remove nothing, and one naming nothing would tear down a live session.
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

Its returns are the outcome, `exited_cleanly` and the error. The
per-connection epoch latch, `BindEpoch()`, and every read of it come out: the value the
compensation names is the token the attempt holds, so nothing is latched off a response and the
`atomic.Int64` has no remaining reader.

### CODE-8 · pkg/gateway/podlifecycle/podsession/binder.go · the reclaim closures return a typed refusal without draining the pod

`failPhase` (`binder.go:1072-1082`) gates `releaseCredentials` on `leaseAssigned` and calls
`b.drain` outside that block, so it drains on every call. `Binder.Launch`'s reclaim closure
passes the literal `true` (`:998`) and `Binder.Prepare`'s passes a mutable flag (`:867`,
declared `:865`, set true at `:1053`). Either way the pod is drained.

A refusal on either new code means the opposite of a failure: this pod is serving the session
correctly, under another attempt's identity or with the session already started. Draining it
retires a healthy pod and, on the `Prepare` path, retires the winner's pod on behalf of the
loser of two concurrent `/finalize` calls (`start.go:2634`, `finalize.go:280`). This deliverable
is therefore a hard blocker of CODE-6's gates rather than an independent cleanup: without it
the first thing the gates do in production is drain live pods.

Both closures become:

```go
// spec: §4.7.1 (role and gateway RPC contract); §6.2 (pod state machine).
// A typed slot-bind refusal is not a pod failure. The pod is serving the
// session correctly, so the closure returns the refusal and calls neither
// failPhase nor drain. failPhase drains unconditionally: its leaseAssigned
// argument gates releaseCredentials alone and b.drain sits outside that block.
if errors.Is(err, adapterclient.ErrSlotBindAttemptSuperseded) ||
    errors.Is(err, adapterclient.ErrSlotBindAlreadyStarted) {
    cl.Close()
    return nil, err
}
b.failPhase(ctx, sb, leaseAssigned, req.SessionID)
cl.Close()
return nil, err
```

`Binder.Prepare`'s closure additionally releases the credentials its own attempt minted when
`leaseAssigned` is set, because that half of `failPhase` is legitimate on a refusal and the
drain is not. `Binder.Launch`'s passes the literal `true` for `leaseAssigned` today; on the
refusal arm it releases the leases the same way and skips only the drain.

The urgency claim the earlier revision carried for this path is withdrawn. `EndpointStart`
admits `StateReady` alone (`pkg/api/v1/session/session.go:289`), so a `/start` retry against a
running session is refused on the precondition before `launchOnPod` runs, and the exclusive
path's drain is reachable today only in composition with a post-publish 500. The dependency
claim replaces it and is stronger: once the gates exist, both closures meet a refusal they did
not meet before.

One residue is named rather than closed. `failPhase` logs and continues when `drain` fails
(`binder.go:1079-1081`), so a pod whose drain failed survives holding an entry stamped with a
dead attempt's token, and every later attempt at that session on that pod is refused. That is
recorded among the accepted failure modes and closing it needs a reaper this proposal does not
stage.

### CODE-9 · pkg/gateway/observability/catalog.go, docs/reference/metrics.md, pkg/gateway/podlifecycle/podsession/slotbinder.go · the counters that make the fence observable

`noteCompensationOutcome(outcome)` is the one helper the compensation's caller runs to fire the
superseded counter, so a later caller that maps the outcome without counting it is a missing
call rather than a missing branch. The unrecognized-outcome counter is incremented in the
default arm of CODE-4's `slotReclaimLeaked`, which is the single site that decides an outcome is
unrecognized.

The counters are registered in `catalog.go` beside `SlotFailure` (`slotbinder.go:358`). The
superseded series takes a `SlotReclaim` hook on `Binder` of the same form, because its call site
holds `req.Pool` and `sandboxName`. The unrecognized-outcome series takes no hook and carries no
labels, because `slotReclaimLeaked` is a free function that holds neither value, which is why
SPEC-6 stages its §16.1 row unlabelled. Both are documented in `docs/reference/metrics.md` and
in the §16.1 rows SPEC-6 stages. `catalog_test.go` and `alert_catalog_crosscheck_test.go` gain
the matching entries.

| Counter | Fires when | Expected value |
|:--|:--|:--|
| `lenny_slot_compensation_outcome_unrecognized_total` | the outcome mapping met a `SlotReclaimOutcome` value this build does not recognize, which it treats as not leaked | zero, and a non-zero value means the gateway and the adapter are running different versions of the contract |
| `lenny_slot_compensation_superseded_total` | a compensation answered `superseded`, meaning the adapter held an entry the compensation was not addressed to | no expected rate; the series records the outcome, whose meaning SPEC-6's §16.1 row states |
| `lenny_slot_shutdown_untokened_entry_total` | the adapter met an entry carrying no token, incremented from CODE-1's fail-closed row | zero on the bind paths, and non-zero when an abandoned attempt's late `StartSession` left an untokened entry behind, which SPEC-5 records as a residue |

The unrecognized-outcome and superseded series are gateway-side. The untokened-entry series is
adapter-side, and the adapter process emits nothing scrapeable today, so its catalog row carries
the same deferral the adapter metrics endpoint already owns; it is recorded in the register
rather than wired to a scrape target here. `slotFailureFinalize` does not exist: both
`stageWorkspace` and `FinalizeWorkspace` record `slotFailureWorkspacePrep`
(`slotbinder.go:287,294`), so a measurement separating them needs a new stage constant, which
this deliverable adds as `slotFailureWorkspaceFinalize`.

### CONF-1 · tests/tier3_contract/adapter_bind_attempt/, tests/tier10_conformance/slot_bind_attempt_conformance_test.go · the published contract is enforced at the wire and exercised in process

The shipped CONF-1 asserted a one-entry-one-epoch invariant. Under the amended mechanism that
invariant does not exist, and a battery asserting it would pass a defective adapter and fail a
conforming one, so the re-cut is a requirement rather than a preference.

**The non-conformance set**, which SPEC-5 states normatively and which the cases below cover one
for one. An adapter does not conform when it:

(a) admits a bind-sequence RPC whose `mid_session` is false and which resolves an entry whose
    session has already started, other than a repeat `ConfigureWorkspace` for the session that
    started on that pod, which §4.7 publishes as idempotent;
(b) admits a bind-sequence RPC whose non-empty `bind_attempt` differs from the non-empty token
    the entry carries;
(c) writes a `bind_attempt` onto an entry it resolved rather than created, in either direction;
(d) performs the slot release or the runtime teardown for a `Shutdown` whose non-empty
    `bind_attempt` differs from the entry's, instead of answering `superseded`;
(e) performs either teardown for a `Shutdown` carrying neither a non-empty `bind_attempt` nor
    `unconditional_teardown`, instead of answering `INVALID_ARGUMENT`;
(f) performs either teardown for a `Shutdown` carrying both, instead of answering
    `INVALID_ARGUMENT`;
(g) admits a bind-sequence RPC whose `mid_session` is false and whose `bind_attempt` is empty,
    or whose `mid_session` is true and whose `bind_attempt` is non-empty;
(h) creates a registry entry for a request whose `mid_session` is true and which resolves no
    entry, instead of answering `FailedPrecondition`;
(i) resolves the slot identifier more than once within one `PrepareWorkspace` call, or admits a
    later frame whose `bind_attempt` or `mid_session` disagrees with the first frame's;
(j) records the pod's shared runtime process as holding a session while it holds no entry for the
    slot identifier carrying the token the request that started the session was admitted against,
    instead of recording nothing, taking the session back off that process, and refusing that
    request on `codes.Aborted`.

The clauses are a set rather than an evaluation order. A request meeting both (a)'s and (b)'s
conditions is refused under (b), because §4.7.1 applies the attempt identity rule before the
started-session rule.

**Tier 3 is the enforcement.** The clauses are wire behaviour and the precedent is exact:
`tests/tier3_contract/adapter_generation_fence/` holds `generation_fence_wire_test.go`, a
descriptor-and-bytes gate pinning a precondition field's number and type across thirteen
messages, and `barrier_unfenced_session_wire_test.go`, a bufconn case driving `adapter.New` over
gRPC and asserting the handler's behaviour. Cases (a) through (j) land in a new
`tests/tier3_contract/adapter_bind_attempt/` following both forms.

**Tier 10 is the in-process battery**, following
`tests/tier10_conformance/recycle_scrub_conformance_test.go`, which drives the exported adapter
`Server` directly with a fake runtime. It covers the same clauses against this adapter and is
worth having for the diagnosis it gives, rather than as the gate.

**The honest limit, recorded rather than papered over.** The §15.4 obligation is published for
third-party adapter authors and the project has no harness that can run one.
`cmd/lenny-compliance` drives a runtime binary over stdin and stdout JSONL against a fake
adapter; it imports no `adapterv1` and no gRPC, so it has no adapter under test and cannot
observe an adapter obligation. SPEC-5 states that the clauses are normative for a third-party
adapter and that the project runs them against its own. The absence of a third-party harness
registers as a `tests/claim-map.json` row with status `ABSENT`, following the thirteen `R16`
rows the `coordination_generation` fence already carries, naming the clause set it cannot
enforce and the reason.

**The negative cases, named because a battery of positives would pass a defective adapter.**
Each appears at tier 3 and again at tier 10:

- **Overwrite on resolve.** A second attempt's admitted request against an entry carrying no
  token does not stamp its own token: a later `Shutdown` naming that second attempt's token
  still answers `superseded`. This is clause (c) and it is the one a naive implementation gets
  wrong, because stamping on resolve looks like an improvement.
- **A differing token is admitted.** A bind-sequence RPC carrying attempt B against an entry
  stamped A is refused `SLOT_BIND_ATTEMPT_SUPERSEDED` on `codes.Aborted`, and the entry, its
  `current` directory and its `credentials.json` all survive the call. Clause (b).
- **Teardown on `superseded`.** A `Shutdown` naming attempt B against an entry stamped A answers
  `superseded` on a successful RPC and removes nothing: the entry, the tree and the credential
  file survive. Clause (d).
- **An empty-token bind is admitted.** A non-mid-session bind-sequence RPC carrying an empty
  `bind_attempt` is `INVALID_ARGUMENT` and creates nothing. Clause (g), and its mirror, a
  mid-session request carrying a token.
- **`unconditional_teardown` mishandled.** A `Shutdown` carrying neither field, and one carrying
  both, are each `INVALID_ARGUMENT` and each leave the entry, the tree and the credential file
  intact. Clauses (e) and (f).
- **A mid-session request creates an entry.** A `FinalizeWorkspace` with `mid_session` true for a
  session the adapter holds no entry for answers `FailedPrecondition`, and the registry and the
  filesystem are both unchanged afterwards. Clause (h), and the same for `PrepareWorkspace`.
- **A second resolve inside one `PrepareWorkspace` call.** A stream whose second frame carries a
  different `bind_attempt` or a different `mid_session` is refused `INVALID_ARGUMENT`, and the
  bytes the first frame staged are not extended by the second. Clause (i).
- **The phase gate.** A bind-sequence RPC with `mid_session` false against an entry whose session
  has started is refused `SLOT_BIND_ALREADY_STARTED` on `codes.FailedPrecondition`, while the
  same RPC with `mid_session` true and no token is admitted, and so is a repeat
  `ConfigureWorkspace` for the session that started on that pod. Clause (a), with the §7.4 upload
  and the idempotent repeat as its paired positives.
- **A start recorded after its entry went.** A start whose entry is removed while `Runtime.Start`
  is still running is refused on `codes.Aborted`, the runtime is observed closed, and no cleanup
  outcome is reported. The fake runtime blocks inside `Start` until the test has sent an
  unconditional `Shutdown` on a second call, which is what makes the ordering deterministic.
  Clause (j).

## Staged schema, chart, and migration changes

### SCHEMA-1 · schemas/lenny-adapter.proto · the bind attempt, the mid-session conditioning, the two-field teardown precondition, the reclaim outcome, and the two refusal codes

One window, and the only schema step in this proposal. The edit is additive: two enum values,
one enum, and ten fields, no field removed, no field renumbered, no RPC added, no message
removed. `buf breaking` has nothing to fire on. `make generate-proto` runs in the same commit
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
    // ERROR_CODE_SLOT_BIND_ALREADY_STARTED: the resolved slot registry entry's
    // session has already started on this pod and the request is not a §7.4
    // mid-session upload, and is not a repeat ConfigureWorkspace for the
    // session that started on that pod, which §4.7 publishes as idempotent.
    // PERMANENT; FailedPrecondition.
    ERROR_CODE_SLOT_BIND_ALREADY_STARTED = 28;
    // ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED: the resolved entry carries a
    // non-empty bind attempt token different from the request's, so a
    // different bind attempt owns it. An entry carrying no token is admitted
    // rather than refused, and a Shutdown naming an attempt reports the
    // superseded outcome rather than this code. TRANSIENT; Aborted.
    ERROR_CODE_SLOT_BIND_ATTEMPT_SUPERSEDED = 29;
```

`Aborted` is chosen for the superseded case because `isTransientPodClaimError` already gains one
`Aborted` arm for the reclaim hold and CODE-2's rollback, so the refusal reuses that arm rather
than adding a third.

**The outcome enum**, kept from the shipped text beside the existing `SessionScrubOutcome`,
which is the sibling it follows. proto3 enum values share the enclosing package namespace, so
the values are fully prefixed with the enum's own name:

```proto
// SlotReclaimOutcome is the result of the §4.7 bind-attempt comparison on a
// Shutdown. All three values are successful outcomes, answered on a successful
// RPC.
enum SlotReclaimOutcome {
  SLOT_RECLAIM_OUTCOME_UNSPECIFIED = 0;
  // SLOT_RECLAIM_OUTCOME_RECLAIMED: the adapter held the named entry under
  // the named bind attempt, or the request was the unconditional teardown, and
  // the slot was released. spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_RECLAIMED = 1;
  // SLOT_RECLAIM_OUTCOME_ABSENT: the adapter holds no entry for the named
  // session, so the reclaim removed nothing. spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_ABSENT = 2;
  // SLOT_RECLAIM_OUTCOME_SUPERSEDED: the adapter holds an entry for the
  // session under a different bind attempt, or under none, so the reclaim
  // removed nothing. Not a failed reclaim and not a leaked slot.
  // spec: §4.7; §7.1.
  SLOT_RECLAIM_OUTCOME_SUPERSEDED = 3;
}
```

**The fields.** Every number below was checked free against the message it lands in, in
`schemas/lenny-adapter.proto` as the file stands:

| Message | Field | Number | Basis | Role |
|:--|:--|--:|:--|:--|
| `PrepareWorkspaceRequest` | `string bind_attempt` | 5 | 1-3 used, 4 `reserved "slot_id"` | the token, on the first entry-creating RPC |
| `PrepareWorkspaceRequest` | `bool mid_session` | 6 | same block | read before the resolve, so rule 1 is reachable |
| `FinalizeWorkspaceRequest` | `string bind_attempt` | 6 | 1-4 used (`mid_session` is 4), 5 reserved | the token |
| `RunSetupRequest` | `string bind_attempt` | 5 | 1-3 used, 4 reserved | the token |
| `AssignCredentialsRequest` | `string bind_attempt` | 4 | 1-2 used, 3 reserved | the token |
| `ResumeRequest` | `string bind_attempt` | 16 | 1-5 and 7-14 used, 6 and 15 reserved | the token, on the §7.3 re-attach's first and only entry-creating RPC |
| `ShutdownRequest` | `string bind_attempt` | 7 | 1-3 used, 4 reserved, 5 `recycle`, 6 `coordination_generation` | the fence |
| `ShutdownRequest` | `bool unconditional_teardown` | 8 | same block | the other half of the precondition |
| `ShutdownResponse` | `SlotReclaimOutcome slot_reclaim` | 3 | 1-2 used | the outcome |

`StartSessionRequest` and `ConfigureWorkspaceRequest` carry no token, because `Binder.Launch`
issues them and mints none.

Both `bind_attempt` and `unconditional_teardown` are bare scalars rather than `optional`, so
neither has explicit wire presence and §4.1's rule that a message's scope is derived from its
field set, with no operation selected by a field's presence standing in for a scope, stays
untouched. `ShutdownRequest.recycle` is the shipped precedent: its own proto comment says it
carries the recycle disposition beside the named session's teardown rather than selecting a
scope.

The field comments state the wire rules the adapter enforces, because a third-party
implementor reads the proto before the specification: that `bind_attempt` is non-empty exactly
when `mid_session` is false; that the adapter treats it as opaque and never parses or orders
it; that the adapter writes it onto an entry it creates and never onto one it resolved; and
that `ShutdownRequest` carries exactly one of a non-empty `bind_attempt` and
`unconditional_teardown = true`.

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

**Claim register.** Three rows, added to the `EXPLICIT` list in
`scripts/seed-claim-register.py` and regenerated with `python3 scripts/seed-claim-register.py
--out tests/claim-map.json` in the same commit. `tests/claim-map.json` is generator output
rather than an authoring source: the tier-0 gate `TestClaimRegisterIsReproducibleFromItsGenerator`
(`tests/tier0_static/claim_register_generator_test.go`) re-runs the generator and fails unless
the committed file is byte-identical, so a row hand-written into the file is dropped by the next
seeding run. The generator emits its rows sorted by claim, so none is placed by hand. Each row
carries a `note`, as its `EXPLICIT` siblings do, and names its production surface in
file-or-symbol form rather than as a line number. `spec_anchor` is `#2851-gateway-to-pod`, the
anchor the sibling `AttachRequest.coordination_generation` row uses.

Two rows are `WIRED`, because a production reader ships in the same change: the adapter compares
the token and the two teardown fields inside `Shutdown`, and the gateway's compensation reads
the outcome. The third is `ABSENT`, recording that the §15.4 clause set is normative for a
third-party adapter and that the project has no harness able to run one, per CONF-1.

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
  "note": "the gateway mints one token per bind attempt and carries it on every request that can create or resolve the entry; the adapter writes it on the create branch alone and compares it on every resolve"
},
{
  "claim": "third-party adapter conformance harness for the §15.4 slot-bind clauses",
  "status": "ABSENT",
  "spec_anchor": "#2851-gateway-to-pod",
  "surface": "`tests/tier3_contract/adapter_bind_attempt/`, `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`",
  "note": "the clauses are normative for any adapter implementation; cmd/lenny-compliance drives a runtime binary over JSONL against a fake adapter and imports no gRPC, so the project runs the clauses against its own adapter only"
}
```

No chart value and no migration.

## Staged docs changes

### DOCS-1 · docs/reference/state-machines.md · the per-slot sub-state table gains the new row

Under `### Per-slot sub-states`, add the row matching the §6.2 edge, immediately after the
`receiving_uploads` → `running` row:

```
| `receiving_uploads` | `slot_cleanup` | The bind is abandoned or fails before the runtime has been given the session, a start still in flight included |
```

No shipped tier-11 gate compares this table's edge rows against the §6.2 block: the tests in
`tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go` read the specification and
the reference page separately and never meet. DOCS-1 therefore carries the tier-11 work that
makes the pair reconcile, specified under `## Testing`, and it lands after SPEC-4.

### DOCS-2 · docs/reference/adapter-contract.md · the `Shutdown` row, the bind-attempt block, and the `DemoteSDK` row

`docs/reference/adapter-contract.md` states of itself that it is the complete reference for the
gateway-to-adapter gRPC protocol (`docs/reference/adapter-contract.md:10`), and its `Shutdown`
row (`:75`) is the reader-facing mirror of the §4.7 row SPEC-1 rewrites. After SPEC-1, SPEC-3,
SPEC-5 and CODE-1 that row is false on the usage flush and the runtime close, which the staged
§4.7 row gates on a session whose start the adapter has admitted while the slot release runs for
any entry the call removed; on the unconditional cleanup-outcome report, which SPEC-3 withholds
on the pre-running path; and on the drain gate, which the staged row re-derives from the
deregistration. The row also carries nothing of the two teardown preconditions, the three
reclaim outcomes, or the no-op clean-exit answer for a session the adapter holds no entry for.
The shipped gate `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts only the
substrings "end-of-session teardown", "recycle disposition", "ReportSessionScrub" and
"ReportPodScrub", all of which survive the staged edits, so nothing turns red on the drift.

The staged prose carries no specification section number, because reader-facing documentation
states the behavior and links to a documentation page instead (`.claude/rules/doc-content.md`).
The vocabulary is the vocabulary of SPEC-5's §15.4 blocks, so the two read as one contract.

Replace the `Shutdown` row under `**Gateway-to-Adapter RPCs:**` with the row below. It stays one
physical line, because the gate reads the row through `lineContaining(page, "| \`Shutdown\` |")`.

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. Every request states which teardown it is asking for: it carries either the bind attempt whose registry entry it is reclaiming or the unconditional-teardown flag, and a request carrying neither or both is rejected as invalid and performs nothing. The slot release removes the session's slot tree and runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The runtime teardown runs only for a session whose start the adapter has admitted: it flushes the session's final usage report and then closes the runtime, and the CH-RUNTIMEOPS drain signal precedes that close only when the deregistration leaves the adapter holding no other bound session. The adapter reports the per-slot cleanup outcome through `ReportSessionScrub` for a session the shared runtime process was given, and reports no outcome for a cleanup on a slot the runtime was never given. A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response. The request carries the recycle disposition beside that teardown: on the recycle disposition the adapter keeps the pod process alive, runs the whole-pod scrub the carried `RecycleScrub` parameterizes, and reports its outcome for `podId` through `ReportPodScrub`. |
```

Add the block below immediately after the `**Scrub responsibilities.**` paragraph, so the bind
attempt and the hold reach the third-party adapter author the page is written for:

```
**Bind attempt and slot-identifier reclaim hold.** Each bind attempt mints an opaque token before its first call and carries that token on the requests that stage the session's workspace, its credentials and its setup, and on a `Resume`. The adapter treats the token as opaque, writes it onto the entry it creates, and never writes it onto an entry it resolved. The resolve, the creation, the write and the comparison are one indivisible step under the lock that guards the registry. A request whose token differs from the one the entry carries is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED` on `ABORTED`, which is the transient classification a caller retries on. A request that resolves an entry whose session has already started is refused with `SLOT_BIND_ALREADY_STARTED` on `FAILED_PRECONDITION`, which is permanent, unless it is a mid-session upload or a repeat `ConfigureWorkspace` for the session that started on that pod, which this page publishes as idempotent. A request meeting both conditions is refused with `SLOT_BIND_ATTEMPT_SUPERSEDED`, because the token comparison is applied before the started-session rule. A mid-session upload carries no token: it resolves an existing entry, creates none, and is admitted against a started session. Before the adapter records the pod's shared runtime process as holding a session it confirms it still holds that slot's entry under the token the starting request was admitted against, and when it does not it records nothing, takes the session back off that process, and refuses that request with the transient `ABORTED` status. `StartSession` and `ConfigureWorkspace` carry no token either and are not mid-session uploads. They may create an entry, which then carries no token, and the started-session rule governs them rather than any token comparison. Every other RPC resolves an entry the session already holds and is outside these rules. A `Shutdown` naming a bind attempt acts only on an entry carrying it. When the entry carries a different attempt, or carries none, the response reports `superseded`, and when the adapter holds no entry for the session it reports `absent`; on either outcome the adapter performs neither teardown. The unconditional teardown removes whatever entry the adapter holds and reports `reclaimed`. Every outcome is answered on a successful call. The adapter holds the slot identifier from the deregistration of that slot's registry entry until the cleanup that reclaims the slot has finished, and while the identifier is held it refuses a request that would create or resolve an entry under it with the gRPC status code `ABORTED`, which is the transient classification a caller retries on.
```

Amend the `DemoteSDK` row (`:64`) so it states the registry effect §4.7.1's caller rule turns on:

```
| `DemoteSDK` | Tear down the pre-connected SDK process, drop the adapter's slot registry entry for the session, and return the pod to pod-warm state. The next bind sequence on the pod creates a fresh entry and stamps it with that attempt's own token. |
```

DOCS-2 lands beside DOCS-1, after SPEC-1, SPEC-3 and SPEC-5 have landed the contract it mirrors.
Its tier-11 work is specified under `## Testing`.

## Testing

Every test carries a `// spec:` annotation naming the sections it exercises, in heading form
(`// spec: §4.7.1 (role and gateway RPC contract)`) and never as a line number, per
`channel-naming.md` N8. Every tier-2 and higher test carries a `// diagnosis:` comment above its
function declaration. Every deliverable runs tier 0 and tier 1 on every package it touches; the
tiers named per section below are the additional ones. Every new file registers in
`tests/spec-map.json` under the sections its annotations cite, and every deliverable that
changes the wire or adds a normative claim adds its `tests/claim-map.json` rows by editing the
`EXPLICIT` list in `scripts/seed-claim-register.py` and regenerating.

### Adapter tests for CODE-6, tier 1

New file `pkg/adapter/bindattempt_test.go` (package `adapter`). Every case must fail against the
tree before this amendment and pass after. Each carries `// spec: §4.7.1 (role and gateway RPC
contract); §5.2 (pool configuration and execution modes); §7.1 (normal flow)`.

The five-rule predicate is the subject, table-driven over the rule it exercises:

- **Rule 1.** A resolve with `allowCreate` false against an absent entry answers
  `FailedPrecondition`, creates no registry entry, and leaves the filesystem untouched. The
  filesystem assertion is the one that matters: `ensureSlotPaths` creates the tree, so a rule
  that refuses after creating would pass a registry-only check.
- **Rule 2.** A create stamps the caller's token, and the entry carries exactly that value
  afterwards. A create with an empty token stamps the empty string, which is the state the
  fail-closed `Shutdown` row and the untokened-entry counter exist for.
- **Rule 3.** A resolve carrying B against an entry stamped A is refused, the error satisfies the
  superseded sentinel's code and detail, and the entry's token is unchanged afterwards.
- **Rule 4.** A resolve with `allowStarted` false against a started entry is refused with the
  already-started code; the same resolve with `allowStarted` true is admitted.
- **Rule 5, both arms and the write rule.** A resolve carrying B against an entry carrying no
  token is admitted and **does not stamp B**; a resolve carrying no token against an entry
  stamped A is admitted and leaves A. This is the case that fails against an implementation that
  stamps on resolve, which is the most likely wrong turn.
- **Rule precedence.** An entry that is both started and stamped A, resolved with B and
  `allowStarted` false, answers the superseded code rather than the already-started one, because
  rule 3 precedes rule 4 and the caller's correct response differs: superseded is retryable and
  already-started is not.

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
- **`PrepareWorkspace` resolves once per call and pins the first frame's assertion.** A stream
  whose second frame carries a different token, and one whose second frame flips `mid_session`,
  are each refused `InvalidArgument`, and the bytes the first frame staged are not extended.
- **The wire rule's two directions.** A non-mid-session request with an empty token and a
  mid-session request with a non-empty one are each `InvalidArgument`, across
  `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `AssignCredentials` and `Resume`. After
  each refusal the registry holds no entry for the slot identifier and no tree exists on disk
  for it, which is what pins `validateBindFields` ahead of the create rule.
- **The hold refuses admission until the teardown returns**, across every entry point that
  resolves a slot identifier. Park the reclaim inside `Runtime.Close` on one of the two sites
  CODE-6 itself routes, `releaseSessionSlot` or the §10.1.4 hold termination, because `Shutdown`
  is routed through the helper only once CODE-1 lands. Assert each entry point refuses with the
  sentinel, release the park, and assert each then admits.
- **Every deregister-then-destroy site takes the hold**, table-driven over `releaseSessionSlot`
  and the §10.1.4 hold termination, with the `Shutdown` row added when CODE-1 routes that
  handler through `reclaimSlotLocked`. This is the case that turns red if a later change adds a
  deregister-then-destroy site without routing it through `reclaimSlotLocked`.
- **The hold is cleared on every return path, including a panic.** Drive `Runtime.Close` to
  panic, recover it at the test boundary, and assert a later bind is admitted. This is the arm
  that turns red if the release is written as a statement at each return rather than as a
  `defer`.
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

### Adapter tests for CODE-1 and CODE-2, tier 1

Files: `pkg/adapter/slotsession_test.go` (package `adapter`) and
`pkg/adapter/socketruntime_test.go`. Reuse the internal fixtures that already exist: `slotPod`,
`slotTreeProbe`, `probeRuntime`, `startRuntimeOps`, `recordingSessionScrubReporter`, `assignOne`
and `fakeExpiryClock`. No `export_test.go` change and no new production seam:
`AssignCredentials` is exported and `assignCredentialsSlot` produces the bound-but-unstarted
state, and the internal package reaches `ensureSlotPaths` directly.

Cases, each `// spec: §4.7.1 (role and gateway RPC contract); §5.2 (pool configuration and
execution modes)`:

- **The two-field precondition, four rows.** A `Shutdown` carrying only a non-empty token, and
  one carrying only `unconditional_teardown`, are each admitted; one carrying neither and one
  carrying both are each `InvalidArgument` and remove nothing. The last two are the fail-closed
  rows and are written first.
- **`Shutdown`'s six-row rule table**, one case per row of CODE-1's table, asserting the outcome,
  whether the entry was removed, and on every non-removing row that the entry, its `current`
  directory and its `credentials.json` all survive the call.
- **The untokened-entry row fires its counter.** A `Shutdown` naming a token against an entry
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
- **Start-versus-reclaim rollback, deterministic form.** `noteRuntimeStarted` returns false for a
  session whose entry was removed, and the `StartSession` rollback closes the runtime, cancels a
  pod MCP surface no surviving session holds, leaves the registry untouched, and answers
  `Aborted`. Neither the rollback nor the reclaim files a `ReportSessionScrub` for that session.
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

Scope accounting to record in the deliverable: besides
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`, no existing adapter test
drives `Shutdown` for an unbound or unstarted entry, so CODE-1 breaks no shipped test. That
test's own path is unaffected and must keep passing unchanged, with one mechanical edit: its
`Shutdown` call sets `unconditional_teardown`, as every shipped caller's does. CODE-2 changes
the fixtures of the two shipped adapter tests named in its call-site scope, both in
`adapterevents_test.go`; only `TestAdapterEventsEmitsControlEvents_spec_4_7` goes red without
that change.

Excluded deliberately: an assertion that a reclaim of a slot the pod's shared runtime process
was never given, whose tree removal fails, reports `exited_cleanly: false`. `removeSlotTree`
calls `slotlayout.RemoveTree` directly with no injectable ops, and the repository's own
precedent records that forcing that error from a non-root unit test is not portable.

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
| Attempt 2 against a live started session | refused twice, by the phase gate and the identity gate; no workspace, credential, setup or staging damage and no compensation sent |
| Resume onto a replacement pod | the resume mints, its claim stamps the entry it creates, its compensation matches; a stale compensation from an earlier attempt answers `superseded` |
| §7.4 mid-session upload onto a live session | empty token and `mid_session` true, admitted by rule 5, barred from creating by rule 1, exempt from the phase gate |
| Adapter process restart between the attempt and its compensation | a fresh process holds no entry the stale compensation can match, so it answers `absent`, and any entry a later attempt creates carries that attempt's own token, so it answers `superseded` |
| A compensation lost to a gateway crash | the entry stands stamped with a dead token and every later attempt at that session on that pod is refused. This pins the residue, and its comment names the recovery that is out of scope |

The third and fourth rows are the ones a handler-placement design fails, so they are written
first and their comments say so.

### Gateway tests for CODE-4, CODE-5, CODE-7 and CODE-8, tier 1

Files: `pkg/gateway/runtime/adapterclient/client_test.go`,
`pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
`pkg/gateway/podlifecycle/podsession/binder_test.go`,
`pkg/gateway/sessionserver/slotretry_test.go`, and
`pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`.

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

CODE-4 and CODE-5's cases, each `// spec: §7.1 (normal flow); §5.2 (pool configuration and
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
- **The outcome mapping, six arms.** `RECLAIMED` + clean → not leaked; `RECLAIMED` + not clean →
  leaked; `SUPERSEDED` → not leaked; `ABSENT` → not leaked; RPC error → leaked; an outcome value
  this build does not recognize → not leaked, with
  `lenny_slot_compensation_outcome_unrecognized_total` incremented. The last arm is the one that
  fails against a fail-closed default, and it is the arm to write first: at
  `maxConcurrentSessions: 2` a fail-closed default drains the pod on one unknown value.
- **The compensation is suppressed on either typed refusal**, and so is the credential release.
  Two arms, one per sentinel: no `Shutdown` is recorded at all, and the `fakeAssigner`'s release
  list stays empty. This is the case that fails against a guard written as `errors.As` on
  `*SlotBindError` alone, because that type is present on a refusal and on an ordinary failure
  alike.
- **The credential release is scoped to the attempt.** A failed attempt that minted two leases
  releases exactly those two by identifier, and a successor's leases for the same session survive
  it. Assert the identifiers rather than the count. The session-wide `ReleaseSession` is asserted
  not to be called from this path at all, which is what the shipped implementation does and what
  strips a successor.
- **The release still runs for a non-`SlotBindError` failure**, which is the property the staged
  comment claims and the reason the call sits outside the `errors.As` guard.
- **Per-stage compensation table.** For the finalize, setup, credential-assignment and
  session-start stages: exactly one `Shutdown` naming the session, carrying a positive
  `deadlineMs` equal to half the budget the case's pool configuration produces and strictly less
  than the RPC deadline that same configuration produces.
- **The upload-free workspace branch, separately.** `stageWorkspace` with no uploads sends no
  `PrepareWorkspace`, so the pod holds no entry. Assert that the compensation is still sent, that
  the adapter answers `ABSENT`, and that `SlotBindError.Leaked` is false.
- **The cancelled-context case.** A failure whose caller context is already cancelled or past its
  deadline still sends the compensation. This is the residue class the compensation exists for
  and the case a naive implementation gets wrong.
- **The counters.** A compensation answering `superseded` increments
  `lenny_slot_compensation_superseded_total`, and one answering an outcome this build does not
  recognize increments `lenny_slot_compensation_outcome_unrecognized_total`. The second must be
  zero across every other case in this file, asserted once at the end of the table.
- **CODE-8's short-circuit, both closures.** `Binder.Prepare` and `Binder.Launch` each meet a
  typed refusal from their adapter RPC, return the refusal, and call neither `failPhase` nor
  `drain`; the same closure meeting an ordinary failure drains as it does today. Assert the
  drain's absence on the fake binder directly. Two concurrent `Binder.Prepare` attempts for one
  session, the loser refused, leave the winner's pod undrained, which is the production case.
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
  gateway-side credential lease: the `fakeAssigner` records every `ReleaseSession` call in
  `released` (`binder_test.go:301,:322`), so the assertion is that the list stays empty. At
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
- **The resume classifier holds the row for every refused resume.**
  `TestHoldOrFailOnResumeErrorSlotRefusals_spec_7_3` in
  `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`, reusing that file's
  `seedResumingRow` fixture. Three cases: a bare `status.Error(codes.Aborted,
  "slot_reclaim_in_progress")`, a superseded refusal, and either of those wrapped in a
  `*podsession.SlotBindError`. Each asserts that `holdOrFailOnResumeError` takes the
  `awaiting_client_action` branch and leaves the row a valid precondition for the explicit
  `POST /v1/sessions/{id}/resume`, rather than the terminal `failed` the classifier answered
  before CODE-5's arm.

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

**The descriptor gate**, one file. It pins each of the ten fields SCHEMA-1 adds by message, number
and type, and pins both new `ErrorCode` values by number and name. This is the gate that fails if
a later change renumbers a field or reuses a number, which no `buf breaking` run catches for an
addition.

**The behavioural cases**, one file, covering clauses (a) through (j) of CONF-1's set and its
negative cases one for one:

- Clause (a): a bind-sequence RPC with `mid_session` false against a started entry is refused
  `SLOT_BIND_ALREADY_STARTED` on `codes.FailedPrecondition`, with the detail carrying that code,
  and a repeat `ConfigureWorkspace` for the session that started on that pod is admitted.
- Clause (b): a differing token is refused `SLOT_BIND_ATTEMPT_SUPERSEDED` on `codes.Aborted`, and
  the entry, its `current` directory and its `credentials.json` survive.
- Clause (c): an admitted resolve does not overwrite the entry's token, in both directions,
  observed through a later `Shutdown`'s answer.
- Clause (d): a `Shutdown` naming a differing token answers `superseded` on a successful RPC and
  removes nothing.
- Clauses (e) and (f): a `Shutdown` carrying neither field and one carrying both are each
  `INVALID_ARGUMENT` and leave the entry, the tree and the credential file intact.
- Clause (g): a non-mid-session request with an empty token, and a mid-session request with a
  non-empty one, are each `INVALID_ARGUMENT`.
- Clause (h): a mid-session request resolving no entry answers `FailedPrecondition` and creates
  nothing on disk.
- Clause (i): a `PrepareWorkspace` stream whose later frame disagrees with the first on either
  field is refused, and the first frame's bytes are not extended.
- Clause (j): a start whose registry entry is removed by an unconditional `Shutdown` while the
  fake runtime blocks inside `Start` is refused on `codes.Aborted`, the runtime is observed
  closed, and no cleanup outcome is reported.
- **The §7.4 positive**, `TestMidSessionUploadIsAdmittedOnAStartedSession`: a mid-session upload
  on a started session, sent over the connection the successful bind published, is admitted on
  both `PrepareWorkspace` and `FinalizeWorkspace` and creates no second entry. This is the case
  whose absence let the §7.4 regression stand for three rounds, so it is written with the rest
  rather than deferred.
- **The unconditional teardown** answers `reclaimed` for an entry it removed and `absent` for a
  session the adapter holds no entry for.

### Conformance battery for CONF-1, tier 10

`tests/tier10_conformance/slot_bind_attempt_conformance_test.go`, following
`recycle_scrub_conformance_test.go`, which drives the exported adapter `Server` directly with a
fake runtime. It covers the same clause set in process, where a failure names the internal state
that produced it rather than a wire answer. `tests/spec-map.json` gains it as an entry under
sections 4.7.1 and 15.4. The `ABSENT` claim-register row SCHEMA-1 states lands with it, recording
that the clauses are normative for a third-party adapter and that no harness runs one.

### Edge-list test for CODE-3, tier 1

`TestValidTransitions_spec_6_2` asserts an exact edge set and fatals on a length mismatch, so it
must move to seven edges in the same step as CODE-3. Add `{ReceivingUploads, SlotCleanup}` to
`want`, add a positive `IsValid(ReceivingUploads, SlotCleanup)` assertion, and keep the negative
assertion that `IsValid(SlotAssigned, Running)` is illegal.

### Integration and race tests, tiers 2, 4, 7a, 8 and 9

**Tier 2, the adapter against envtest, two cases** in
`pkg/gateway/podlifecycle/podsession/binder_envtest_test.go`, each carrying `// spec: §4.7.1
(role and gateway RPC contract); §6.2 (pod state machine)` and the `// diagnosis:` comment tier 2
requires. A `Binder.Prepare` whose adapter RPC is refused leaves the Sandbox undrained and
unannotated with `lenny.dev/drain-request`; the same `Prepare` meeting an ordinary failure stamps
it. `// diagnosis:` states that a failure means a refusal is retiring a pod that is serving the
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
is released once the compensating `Shutdown` has returned, so the reclaim always lands before
`noteRuntimeStarted` runs, which is the only ordering a start parked inside `Runtime.Start`
admits. The park is the `gatedRuntime` form
`tests/tier7a_load_local/podmcp_arming_handoff_test.go:43-101` already provides, and the case is
driven over both RPCs CODE-2 gives the rollback from one rendezvous keyed on the RPC name, which
is the form `podmcp_once_per_pod_start_race_test.go:239-256` already uses. The `Resume` arm needs
a session identifier and a checkpoint identifier and no chunks, because a conversation-only
resume restores nothing and the checkpoint-transport precondition fires only for a request
carrying chunks. On both arms the RPC returns `Aborted`, `runtimeLive` does not hold the raced
session, and neither the reclaim nor the rollback files a `ReportSessionScrub` for it. Run each
arm on a pod with no co-tenant as well as a co-tenanted one, because the pod-level cohort outcome
is what the two variants hold apart.

**Tier 7a, the reverse ordering.** The same rendezvous with the park released only after the
reclaim has answered and a successor has re-created the entry under the same identifier. The
start confirmation finds an entry carrying a different token and refuses, the RPC returns
`Aborted`, and the successor's entry, tree and runtime membership survive. This fails against a
confirmation predicate that reads only `st.sessionID`.

**Tier 7a, the concurrent-resolve race**,
`TestConcurrentBindAttemptsResolveExactlyOneOwner_spec_4_7_1` in the same new file. N goroutines,
each a distinct attempt, race `ensureSlotStateLocked` for one slot identifier under `-race`.
Exactly one is admitted on the create branch, every other is refused `superseded`, the entry's
token equals the winner's afterwards, and no goroutine observes a partially initialized entry.
This is the case that fails against a resolve that releases the lock between the lookup and the
stamp, which is the atomicity clause SPEC-5 states and CONF-1 publishes.

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
`removeSlotTree`, and that every refusal carries `codes.Aborted`.

**Tier 7a, the lock-order assertion.** The same file gains a case driving `FinalizeWorkspace`,
`RunSetup`, `PrepareWorkspace` and `Shutdown` concurrently for one slot identifier under `-race`,
asserting no deadlock across a `lenny-test stress` budget. The per-slot guard and `s.mu` are two
locks and the order between them is stated once in CODE-6; this is what holds the statement true.

`tests/tier7a_load_local/shutdown_drain_gate_race_test.go`'s
`TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` and
`TestShutdownDrainRacesAnIncomingSession_spec_6_4` already pin the one-signal and unbound-entry
properties and must keep passing, with the mechanical edit that their `Shutdown` calls set
`unconditional_teardown`. Every session whose `Shutdown` they drive is started through
`startDrainSession`, which runs a full `StartSession`, so CODE-1's move of the ending session's
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
under sections 4.7, 4.9 and 5.2 and which `slotAddressCaseFiles` gains in its sorted position, in
the same step. This tier owns the cases because the token is what decides whose credential
material a reclaim reaches, and because CODE-1 widens the tree-removal gate from `bound` to
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
the adapter has admitted"`, and `"either the bind attempt"`, the last of which pins the two-field
precondition the staged row states. Assert over the whole page, beside the row assertion, that it
carries `"bind attempt"` and `"reclaim hold"`. Add one clause to the `// diagnosis:` comment
naming the two teardowns and the two teardown preconditions.

**For the counters and SPEC-6**, the shipped `alert_catalog_crosscheck_test.go` and the
metrics-catalog reconciliation it drives gain the three counter names, so a counter present in
`catalog.go` and absent from `docs/reference/metrics.md` or from the §16.1 row fails tier 11.
`catalog_test.go` gains the same three names in its own inventory.

### Coverage and preflight

Run `lenny-test coverage --diff <merge-base>` and raise the changed lines to the 80% floor,
covering the error and boundary paths rather than the happy path. Before any tier above 1, run
the environment preflight in `.claude/rules/test-coverage.md`: confirm the loaded images match
the renderer, sweep terminal agent pods, confirm the warm pools reach Ready, reap orphaned
envtest processes, and confirm the same tier fails on a clean checkout of the merge base before
treating a failure as this change's.

## Edge cases and accepted failure modes

- **A refused retry burns one attempt.** Attempt 2 is refused while attempt 1's entry stands.
  The window is bounded by the compensation's latency plus the §5.2 reclaim hold, and the retry
  is refused `Aborted`, which the classifier treats as transient, so the attempt is placed again
  rather than ended. A refused retry is a cheaper residue than a destroyed session or a
  successor reaching `running` on an empty workspace.
- **A compensation lost to a gateway crash leaves the session unstartable on that pod.** The
  entry stands stamped with a dead attempt's token, and every later attempt at that session on
  that pod is refused rather than admitted. That is worse on one axis than the epoch design,
  which would have let a retry adopt the entry, and better on the axis that matters: adoption is
  what destroys a live session. The recoveries are a durable compensation record and a reaper
  for a leaked entry, neither of which is staged here. The recovery primitive both would use
  ships with SCHEMA-1: a `Shutdown` with `unconditional_teardown = true` removes whatever entry
  the adapter holds.
- **An attempt cannot fence itself.** A non-mid-session `FinalizeWorkspace` may legitimately be
  an attempt's first RPC, on a plan with no uploads, so `allowCreate` is true. If an
  unconditional `Shutdown` removed attempt A's entry while A was still running, A's next RPC
  recreates its own entry stamped A, materializes from an empty staging tree, and can reach
  `running` on an empty workspace. The token discriminates attempts and this is one attempt
  against itself. Closing it needs either a generation on the on-disk tree or a bind-scoped lock
  spanning the whole attempt, both wider than this proposal.
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
  bind that fails after its slot entered `receiving_uploads` and before the runtime was given
  the session runs the §5.2 per-slot cleanup inside the failing handler, through
  `releaseSessionSlot`. That function discards `removeSlotTree`'s error
  (`pkg/adapter/slotsession.go:217`), and CODE-6 replaces the discard with a warning log line
  rather than with a carrier the gateway can read. The gateway's compensation then finds no
  entry, is answered `ABSENT`, and CODE-4 reads that as a completed reclaim with `sbe.Leaked`
  false, so an incomplete cleanup on this path reaches no leaked sub-state and contributes
  nothing to the `ceil(maxConcurrentSessions/2)` trigger. The residue is the slot's workspace
  tree and its credential directory, bounded at the whole-pod boundary: on a recycling pod the
  occupancy-zero whole-pod scrub removes both and verifies their absence, and a pod that does
  not recycle retires at that boundary.
- **A pod whose drain failed keeps a dead attempt's token.** `failPhase` logs and continues when
  `drain` fails (`binder.go:1079-1081`), so a surviving pod can hold an entry stamped with a
  dead attempt's token, and every later attempt at that session on that pod is refused. CODE-8
  makes the refusal path skip `failPhase` entirely, so this arises only on an ordinary failure
  whose drain then fails. It is the reaper's subject.
- **What can meet the reclaim hold, and what it costs.** Three callers can. The §7.4 mid-session
  upload resolves the session's binding with `podRegistry.Get` and sends `PrepareWorkspace` and
  `FinalizeWorkspace` on it; the teardown prunes that binding before it tears the slot down, so
  an upload that arrives after the removal is answered `TARGET_NOT_READY` and reaches no
  adapter, while one that resolved the binding before it and is still in flight when the
  `Shutdown` opens the hold reaches `ensureSlotStateLocked` inside it. That is the interleaving
  that would otherwise resurrect a registry entry after `Shutdown` removed it, and the hold is
  what refuses it. A client-driven §7.3 resume is the second and can re-place the same session
  on the same pod. A §5.2 retry is the third, and the hold it meets is one no reclaim opened:
  `StartSession`'s deadline expires, the handler keeps running and reaches a pre-`Runtime.Start`
  failure branch, and the hold `releaseSessionSlot` opens there is invisible to the gateway; the
  compensating `Shutdown` is not held, finds no entry, and answers `ABSENT`. In all three cases
  the refusal is `codes.Aborted`, which the caller can retry on, but what records it differs by
  caller. The §7.3 resume's bind attempt builds a `SlotBindError`, takes `Reason()`'s transient
  default, and costs the pod one windowed `RecordFailure`; once that retry budget is spent the
  surviving error reaches `holdOrFailOnResumeError`, and CODE-5's `codes.Aborted` arm is what
  reverts the row to `awaiting_client_action` rather than to a terminal `failed`. The
  mid-session upload builds none: `handleUploadToSession` converts the error into an HTTP 502
  `UPSTREAM_ERROR` and nothing is counted against the pod. The §5.2 retry's refused attempt
  costs one windowed `RecordFailure`, and `maxSlotRetries` is 1, so that attempt is the
  request's last. Nothing else records the hold: no metric and no report distinguishes a hold
  refusal from any other transient slot failure. Accepted rather than closed, because a
  gateway-side wait-and-retry would hold the client's request open for the same window and would
  state that timeout in a second place.
- **A retry of an attempt's own `AssignCredentials` double-counts leases.** The lease store is
  keyed by lease identifier and each mint produces a fresh one, so a second assignment for one
  session adds leases rather than replacing them, while the adapter side replaces. Under the
  identity gate a foreign `AssignCredentials` is refused, so the double-count arises only from a
  retry of an attempt's own request, and CODE-4's attempt-scoped release returns every
  identifier that attempt minted.
- **A compensation sent to a pod in coordinator-hold state is refused.** The hold-state
  allowlist admits only the fence, version negotiation, the event stream and the health probes,
  so the reclaim returns an error and the slot is correctly classified leaked, which on a pod
  serving concurrent sessions §6.2 then routes to the §5.2 threshold. The state cannot arise in
  a running deployment, because nothing arms the hold; the case becomes live when remediation
  step R12 ships the gateway control-stream consumer.
- **The connect stage compensates nothing.** The slot is reserved before any workspace RPC, so
  the adapter holds no entry and no compensation is sent. On the create-time-reserved path
  `BindReservedSlot`'s own reservation release can still fail, CODE-4 folds that failure into
  `Leaked`, and CODE-5's reserved branch accounts it persistently, which marks a slot leaked out
  of `slot_assigned`. §6.2 still has no terminal out of `slot_assigned`; the summary records
  that hole and why it is not closed here.
- **Faster pod churn at `maxConcurrentSessions >= 3`.** The leaked disposition withholds the
  counter decrement and counts persistently, so an unacknowledged reclaim moves a pod toward
  retirement sooner than a clean release would. Accepted as §6.2's semantics applied
  consistently. At `maxConcurrentSessions: 2` the disposition changes nothing on the retry path,
  because the threshold there is already 1. The reserved branch and the §7.3 re-attach change at
  every concurrency because they reach the accounting at all for the first time.
- **An unrecognized reclaim outcome is not a leak.** `UnhealthyThreshold` is
  `int((maxConcurrent + 1) / 2)`, so at `maxConcurrentSessions: 2` the threshold is 1,
  `Unhealthy` compares with `>=`, and `RecordLeak` increments a map that is never window-pruned.
  A fail-closed default on an unknown outcome would therefore drain the pod on the first one. It
  is counted instead, and the counter is what surfaces a build talking to an adapter that
  answers a value it does not know.
- **`slotCount` still counts a registered-but-unbound entry.** The §28.5.3 count fails closed on
  purpose and its comment says so. The predicate is left exactly as it is.

## Files touched on application (non-spec)

- `schemas/lenny-adapter.proto` · the two `ErrorCode` values, the `SlotReclaimOutcome` enum and
  the ten fields SCHEMA-1 states.
- `pkg/proto/adapter/v1` · regenerated by `make generate-proto` in the same commit as the proto
  edit.
- `scripts/seed-claim-register.py` · the three rows SCHEMA-1 states, two `WIRED` and one
  `ABSENT`, added to the `EXPLICIT` list that is their row source.
- `tests/claim-map.json` · regenerated from `scripts/seed-claim-register.py` in the same commit
  as the row edit.
- `tests/spec-map.json` · every section a new or edited case's own `// spec:` annotation names is
  credited here, satisfied by a whole-file or directory entry for a file the map registers as a
  whole and otherwise by a `path::TestName` entry, because `validate-maps` checks file membership
  alone while `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate` checks a file the map
  registers case by case one case at a time. Each entry lands in the step that creates the file
  or the case it maps, so no intermediate commit leaves either gate red. The new test surfaces
  take file and directory entries: `tests/tier3_contract/adapter_bind_attempt/...` under sections
  4.7, 4.7.1, 7.1 and 15.4; `tests/tier7a_load_local/slot_bind_attempt_race_test.go` under
  sections 4.7.1 and 7.1; `tests/tier7a_load_local/slot_reclaim_hold_race_test.go` under section
  5.2; `tests/tier8_chaos/compensation_loss_test.go` under sections 4.7.1 and 7.1;
  `tests/tier9_security/slot_credential_reclaim_fence_test.go` under sections 4.7, 4.9 and 5.2;
  and `tests/tier10_conformance/slot_bind_attempt_conformance_test.go` under sections 4.7.1 and
  15.4. `tests/tier4_integration/concurrent_workspace_test.go` is registered as a whole, under
  sections 5.2, 6.4 and 28.5.3, so it gains whole-file credits under sections 7.1 and 4.7.1. The
  new cases landing in files the map already registers case by case take per-case entries: in
  `tests/tier4_integration/recycle_scrub_path_test.go`, whose only whole-file credit is 15.1,
  `TestRecyclePathUnansweredReclaimLeaksTheSlot_spec_5_2` is entered as
  `tests/tier4_integration/recycle_scrub_path_test.go::<name>` under sections 5.2, 6.2 and 7.1.
  The cases added to `pkg/gateway/podlifecycle/podsession/slotbinder_test.go` and
  `binder_test.go` need no entry, because the map registers those files as a whole under 4.1,
  4.6.3, 4.7, 5.2, 6.2 and 7.1, which covers their annotations; the new 4.7.1 credit is added to
  both. Any other file the implementor lands a case in is checked the same way, against that
  file's own credits and its own registration granularity.
- `tests/tier0_static/spec_map_slot_address_registration_test.go` · the `slotAddressCaseFiles`
  inventory is the second register a test file enters, required both for a file whose name
  matches `slot*_test.go` and for any file that calls the slot claim surface. The three
  slot-named new files enter it in the step that creates them, and an edited test file that gains
  such a call enters it in the step that adds the call.
- `pkg/adapter/bindattempt.go` · new: the `slotResolve` type, the reclaim-hold side table's
  helpers, the per-slot guard helper, the three refusal sentinels and their predicates, and the
  two shared resolve helpers.
- `pkg/adapter/server.go` · the `Server.reclaiming map[string]struct{}` hold set and the
  `Server.slotGuards map[string]*sync.Mutex` guard table, both guarded by `s.mu`, declared on the
  struct that owns them.
- `pkg/adapter/slot.go` · the `slotState.bindAttempt string` field, `ensureSlotStateLocked`'s
  `slotResolve` parameter and five-rule predicate, and `ensureSlotPaths`'s widened signature.
- `pkg/adapter/staging.go` · the three resolve sites' `slotResolve` arguments,
  `resolvePrepareStagingDir`'s widened signature, `FinalizeWorkspace`'s `mid_session` read moved
  above the resolve, `PrepareWorkspace`'s per-frame latch and resolve-once rule, the wire rule's
  two directions on all three handlers, and the per-slot guard around the destructive sections.
- `pkg/adapter/slotcreds.go` · the resolve site's `slotResolve` argument.
- `pkg/adapter/credentials.go` · `AssignCredentials`'s wire-rule check and the resolve it passes.
- `pkg/gateway/runtime/adapterclient/client.go` · the two error sentinels and
  `translateSlotBindRefusal` with its call sites, `bind_attempt` on the six requests,
  `mid_session` on `PrepareWorkspace`, `unconditional_teardown` set inside `Shutdown` and
  `ShutdownRecycle`, the new `ShutdownReclaim`, and the removal of the per-connection epoch latch
  and `BindEpoch`.
- `pkg/adapter/session.go` · `Shutdown`'s two-field precondition, its bind-attempt comparison,
  its reclaim hold, its split gates, its response, its doc comment, and the `StartSession`
  rollback.
- `pkg/adapter/runtimegeneration.go` · `noteRuntimeStarted`'s signature, body and doc comment,
  and the new `runtimeHoldsLocked` accessor.
- `pkg/adapter/slotsession.go` · the `reclaimSlotLocked` helper beside `deregisterSlotLocked` and
  that function's doc comment, `releaseSessionSlot` rerouted through the helper and its discarded
  `removeSlotTree` error turned into a logged warning (the file gains a `log/slog` import),
  `deregisterSlot` retired into it, the `release` field on `heldSession`,
  `claimSessionSlotUnderLock`'s `slotResolve` parameter and typed `!idempotentRepeat` refusal,
  and the token the two claim functions report.
- `pkg/adapter/holdstate.go` · `terminateHeldSession` defers the release its `heldSession`
  carries, so the §10.1.4 hold termination holds the identifier from pass 1's deregistration
  until pass 2 has closed the runtime and removed the slot tree.
- `pkg/adapter/resume.go` · the `bind_attempt` read and its resolve, the `noteRuntimeStarted`
  call site and the token it passes, and its rollback.
- `pkg/adapter/sdkwarm.go` · `ConfigureWorkspace`'s resolve, which carries no token and sets
  `allowStarted` from `idempotentRepeat`, and the `noteRuntimeStarted` call site.
- `pkg/sandbox/slotstate/slotstate.go` · `ValidTransitions()` and its doc comment.
- `pkg/gateway/podlifecycle/podsession/bindattempt.go` · new: `newBindAttempt` and the three
  counters.
- `pkg/gateway/podlifecycle/podsession/slotfailure.go` · `SlotBindError.Leaked` and the new
  `slotFailureWorkspaceFinalize` stage constant.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` · `slotCleanupBudget`,
  `compensateFailedSlotBind`, `slotReclaimLeaked`, `materializeSlot` and
  `materializeSlotStages` with the mint and the attempt-scoped lease release,
  `releaseAttemptCredentials`, `ReleaseSlotReservation`, `BindReservedSlot`, `ClaimSlot`'s
  connect-stage release, and `Binder.ReleaseSlot`'s two teardown calls.
- `pkg/gateway/podlifecycle/podsession/binder.go` · `Binder.Prepare`'s mint, carry and reclaim
  closure; `Binder.Launch`'s reclaim closure; `Binder.Resume`'s mint, carry, refusal check,
  compensation and failure branch; `releaseResumeSlot`; and `Binder.shutdownAdapter`'s two
  teardown calls.
- `pkg/gateway/sessionserver/start.go` · the `slotBinder` interface, `accountSlotFailure`,
  `applySlotRetryPolicy`, `bindSlotWithRetry`, `bindConcurrentSlot`, `resumeOnPod`,
  `rollbackClaim`, and `isTransientPodClaimError`'s `codes.Aborted` arm.
- `pkg/gateway/sessionserver/upload_to_session.go` · the §7.4 pair sets `mid_session` true and
  carries no token.
- `cmd/lenny-gateway/user_revocation.go` and the coordinator-handoff teardown ·
  `unconditional_teardown` on their `Shutdown` calls, which `Client.Shutdown` sets for them.
- `pkg/gateway/observability/catalog.go` and `catalog_test.go` · the counters CODE-9 states.
- `docs/reference/metrics.md` · the three counter rows.
- `docs/reference/state-machines.md` · the per-slot sub-state table.
- `docs/reference/adapter-contract.md` · the `Shutdown` row, the bind-attempt block after the
  scrub-responsibilities paragraph, and the `DemoteSDK` row.
- Tests: `pkg/adapter/bindattempt_test.go`, `pkg/adapter/bindattempt_orderings_test.go`,
  `pkg/adapter/slotsession_test.go`, `pkg/adapter/socketruntime_test.go`,
  `pkg/adapter/export_test.go`, `pkg/adapter/usage_test.go`,
  `pkg/adapter/adapterevents_test.go`, `pkg/adapter/podmcp_arming_internal_test.go`,
  `pkg/adapter/exportpaths_test.go`, `pkg/adapter/one_session_only_test.go`,
  `pkg/adapter/holdstate_test.go`, `pkg/sandbox/slotstate/slotstate_test.go`,
  `pkg/gateway/runtime/adapterclient/client_test.go`,
  `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
  `pkg/gateway/podlifecycle/podsession/binder_test.go`,
  `pkg/gateway/podlifecycle/podsession/binder_envtest_test.go`,
  `pkg/gateway/sessionserver/slotretry_test.go`,
  `pkg/gateway/sessionserver/slotretry_load_test.go`,
  `pkg/gateway/sessionserver/start_test.go`,
  `pkg/gateway/sessionserver/resume_setup_demotion_internal_test.go`,
  `tests/tier3_contract/adapter_bind_attempt/`,
  `tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go`,
  `tests/tier4_integration/concurrent_workspace_test.go`,
  `tests/tier4_integration/recycle_scrub_path_test.go`,
  `tests/tier7a_load_local/slot_bind_attempt_race_test.go`,
  `tests/tier7a_load_local/slot_reclaim_hold_race_test.go`,
  `tests/tier7a_load_local/shutdown_drain_gate_race_test.go`,
  `tests/tier8_chaos/compensation_loss_test.go`,
  `tests/tier9_security/slot_credential_reclaim_fence_test.go`,
  `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`,
  `tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go`,
  `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go`,
  `tests/tier11_docs/alert_catalog_crosscheck_test.go`, and
  `tests/tier0_static/spec_map_slot_address_registration_test.go`.
