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
timeout, releases the session's gateway-side credential leases, and records the outcome on
the `*SlotBindError` it is about to return. `Binder.Resume` has the identical structure and
takes the same compensation. The outcome then reaches `SlotClaimer.ReleaseSlot`'s existing
`leaked` parameter, which already implements §6.2's disposition.

**The race the design opens, and its guard.** The compensation's trigger is a `StartSession`
whose deadline expired, so it routinely races an adapter handler still between
`claimSessionSlot` (which sets `started`) and `noteRuntimeStarted`. Without a guard the
reclaim deletes the entry, `Runtime.Start` then succeeds, and `noteRuntimeStarted` records a
session in `runtimeLive` that the registry no longer holds: the third residue class by a new
route, with `runtimeIdleLocked` false for the life of the pod. The race pre-exists this
proposal, because `claimSessionSlotUnderLock` sets `st.sessionID` and `st.started` together
before `Runtime.Start` and §11.4's full revoke already sends `Shutdown` for a non-terminal
session; the compensation turns a narrow window into the expected case. `noteRuntimeStarted`
therefore confirms the slot survived and reports whether it did, and the start rolls back
when it did not. The confirmation catches the ordering in which the reclaim lands after the
start's claim. When the reclaim's answer precedes that claim, the claim re-creates the entry
the confirmation reads, and the residue that leaves is recorded among the accepted failure
modes rather than closed here.

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
structs the bind path already carries, as `ExcludePod`, set on the retry iteration in
`applySlotRetryPolicy` and honoured as a read-only placement filter in `ClaimSlot`'s two
candidate passes. No failure class becomes non-retryable, so §5.2's non-retryable categories
and its client-error contract are untouched, and when the excluded pod is the pool's only
candidate the retry meets the `WARM_POOL_EXHAUSTED` outcome with `details.reason:
"concurrent_slots_exhausted"` that the shipped claim path already returns.

**What does not change.** No new RPC, frame, wire field, flag, metric, or operator-tunable.
One in-process error field, one in-process request field carrying §5.2's placement constraint
for a pod holding an unacknowledged reclaim, one existing parameter threaded to two more call sites, and
one predicate split.

## Staged code changes

### CODE-1 · pkg/adapter/session.go — `Shutdown` releases the slot for any entry removed and tears the runtime down only for a started session

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

Clause two becomes, with the deregistration and the drain decision still one critical
section:

```go
s.mu.Lock()
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
return &adapterv1.ShutdownResponse{ExitedCleanly: closeErr == nil && (started || treeErr == nil)}, nil
```

The two meanings of `exited_cleanly` are deliberate and must be stated in the doc comment.
The started path keeps discarding the tree-removal error, because making it fail there would
reclassify existing session-end slots as leaked under `Binder.ReleaseSlot`'s
`err != nil || !cleanly` rule, which is a §6.2 accounting change this proposal does not make.
The unstarted path surfaces it, because CODE-4 has no other signal that the reclaim did not
complete.

Doc-comment work on `Shutdown`:

- State the handler as two teardowns with two preconditions, matching the §4.7 row, and name
  `releaseSessionSlot` (`pkg/adapter/slotsession.go:214-220`) as the shipped statement of the
  unstarted branch's semantics, so the two compensating paths read as one rule.
- State the cleanup-outcome report's own gate beside them, because it is neither teardown's:
  §5.2 gives a session release at most one report and gives it to the cleanup that reclaimed
  the slot, and only a slot that reached `running` is owed one, so the report reads
  `runtimeLive` membership while the teardown reads `st.started`.
- Record why the runtime teardown must not run for an unstarted session, in the form the tree
  supports rather than the form that is easy to write. `Close` is not uniformly session-scoped
  across the implementations: `InProcessRuntime.Close` (`pkg/adapter/embedded.go:188-204`) and
  `MCPRuntime.Close` (`pkg/adapter/mcpruntime.go:266-291`) ignore the session identifier and
  tear the runtime down on any call, and `SocketRuntimeProcess.Close` tears down the shared
  connection, the spawned child, and the never-rebound listener whenever the active set empties
  (`pkg/adapter/socketruntime.go:441-467`, listener bound once at `:155-161`). The socket
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

### CODE-2 · pkg/adapter/runtimegeneration.go, pkg/adapter/session.go — a start confirms its slot survived before recording the runtime as holding the session

`noteRuntimeStarted` gains the confirmation rather than a fourth entry point beside it:

```go
// noteRuntimeStarted records that sessionID has been given to the pod's one
// shared runtime process, and reports whether the record was taken. It runs
// immediately after a successful start. It refuses when the slot registry no
// longer holds an entry bound to this session, which is the state a §7.1
// reclaim leaves when it lands after this start's claim: recording there
// would put a session in runtimeLive that the registry does not hold, holding
// runtimeIdleLocked false and soleSession empty for the life of the pod.
//
// The predicate reads the registry entry rather than st.started, because
// st.started is set before Runtime.Start and is therefore true for this very
// call.
//
// spec: §7.1 (the reclaim and the start that races it); §15.4.3.
func (s *Server) noteRuntimeStarted(sessionID string) bool {
    if sessionID == "" {
        return false
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    st, ok := s.slots[sessionID]
    if !ok || st.sessionID != sessionID {
        return false
    }
    s.noteRuntimeStartedLocked(sessionID)
    return true
}
```

`StartSession`'s call site (`pkg/adapter/session.go:163`) takes the rollback:

```go
if !s.noteRuntimeStarted(sessionID) {
    // spec: §7.1 — the reclaim removed this slot while Runtime.Start ran.
    // Take the session back off the shared runtime process and refuse the
    // start. No cleanup outcome is reported: the slot never reached §6.2's
    // `running`, and §5.2 gives a session release at most one such report,
    // filed by the cleanup that reclaimed the slot, which withheld it for
    // the same reason.
    if s.Runtime != nil {
        _ = s.Runtime.Close(ctx, sessionID)
    }
    s.releaseSessionSlot(sessionID)
    return nil, status.Errorf(codes.FailedPrecondition,
        "session %s slot was reclaimed while the start was in flight", sessionID)
}
```

`releaseSessionSlot` is already correct against an entry a concurrent reclaim removed:
`removed` is false, so it skips the tree removal the reclaim already performed, and
`cancelPodMCPIfRuntimeIdle` still runs.

Scope of the call-site change:

- `pkg/adapter/session.go:163` (`StartSession`) takes the rollback. This is the only site the
  compensation can race: `materializeSlot`'s start stage is `cl.StartSession`.
- `pkg/adapter/resume.go:144` and `pkg/adapter/sdkwarm.go:261` become `_ = s.noteRuntimeStarted(...)`
  with no rollback. `sdkwarm.go` in particular must not take a bare `Runtime.Close`: that site's
  own failure idiom is `releaseSessionSlot` with the §6.1 `DemoteSDK` fallback, and a bare close
  there leaves `s.sdkConnected` true, which only `DemoteSDK` clears.
- Test callers become `_ = s.noteRuntimeStarted(...)`: `pkg/adapter/export_test.go:45`,
  `usage_test.go:233`, `adapterevents_test.go:95,184`, `podmcp_arming_internal_test.go:84,185,230`.

### CODE-3 · pkg/sandbox/slotstate/slotstate.go — the per-slot edge list gains the pre-`running` cleanup edge

`ValidTransitions()` gains `{ReceivingUploads, SlotCleanup}`, and the doc comment's edge list
above it gains the matching line:

```go
//	receiving_uploads → slot_cleanup    (bind abandoned before the runtime is given the session)
```

The edge list is the authoritative transcription of §6.2's fence, so this lands in the same
step as the §6.2 edit. `IsValid(SlotAssigned, Running)` stays illegal.

### CODE-4 · pkg/gateway/podlifecycle/podsession — the gateway compensates every post-connection bind failure and every failed resume

Targets:

- `slotfailure.go` — `SlotBindError` gains a `Leaked bool` field.
- `slotbinder.go` — a new `slotCleanupBudget` helper and a new `compensateFailedSlotBind`
  method; `materializeSlot` splits into a stage runner and a compensating wrapper;
  `ReleaseSlotReservation` takes the disposition; `BindReservedSlot` and `ClaimSlot`'s
  connect-stage release pass it.
- `binder.go` — `Binder.Resume`'s adapter-RPC failure branch compensates before `cl.Close()`,
  and `releaseResumeSlot` takes the disposition.

The budget:

```go
// slotCleanupBudget is the §5.2 per-slot cleanup timeout,
// max(cleanupTimeoutSeconds / maxConcurrentSessions, 5) seconds. §5.2 assigns
// that figure to the adapter's own cleanup enforcement; the gateway reuses it
// as the deadline for the compensating Shutdown rather than inventing a
// constant. cleanupTimeoutSeconds is optional, so an unset pool yields the 5s
// floor. spec: §5.2 (slot cleanup); §7.1 (the reclaim obligation).
func slotCleanupBudget(cleanupTimeoutSeconds int, maxConcurrentSessions int32) time.Duration
```

The compensation:

```go
// compensateFailedSlotBind reclaims the pod-side state a failed bind created,
// on the connection the failed stage still holds, and reports whether the
// slot must be released as leaked. It also returns the session's gateway-side
// §4.9 credential leases, which assignSlotCredentials minted before the RPC.
//
// The context is detached from the caller's: the residue class this exists for
// arises when the caller's context expired during StartSession, so a reclaim
// issued on that context would fail in the one case that leaves a runtime
// running for an abandoned session.
//
// deadlineMs is zero, so the adapter falls through to the inbound RPC context
// and the budget above is the only bound.
//
// spec: §7.1 (the reclaim obligation and its leaked disposition); §4.7
// (Shutdown's two teardowns and its no-op answer); §5.2 (the budget).
func (b *Binder) compensateFailedSlotBind(ctx context.Context, cl *adapterclient.Client, req SlotBindRequest, sandboxName, slotID string) bool {
    rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx),
        slotCleanupBudget(req.CleanupTimeoutSeconds, req.MaxConcurrentSessions))
    defer cancel()
    cleanly, err := cl.Shutdown(rctx, req.SessionID, "slot_bind_failed", 0)
    b.releaseCredentials(req.SessionID)
    if err != nil || !cleanly {
        log.Printf("podsession: reclaim slot %s on pod %s after failed bind for session %s: cleanly=%v err=%v",
            slotID, sandboxName, req.SessionID, cleanly, err)
        return true
    }
    return false
}
```

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
        cl.Close()
        return nil, err
    }
    return res, nil
}
```

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
| `binder.go` `releaseResumeSlot` | the disposition its caller computed |
| `start.go` `slotBinder` interface declaration | signature only |
| `start.go` `applySlotRetryPolicy` | `sbe.Leaked` |
| `start.go` `rollbackClaim` | `false` — no workspace RPC runs at create |

`Binder.Resume` takes the same compensation. Its adapter-RPC failure branch currently calls
`cl.Close()` and then `releaseResumeSlot`; it becomes a compensation on the still-open
connection, then the close, then the release carrying the outcome. The adapter's `Resume`
claims the session's slot before it can fail, so a lost or refused `Resume` leaves the
identical residue, and this is the site the recycle-boundary sweep alternative was reaching
for. Its spec basis is the same §7.1 obligation, which names the §7.3 re-attach onto a
replacement pod as one of the bind attempts it binds; §7.3's resume flow and §6.2's
mid-resume cancel edge point at that paragraph, so the `// spec: §7.1` citation resolves on
the resume path as it does on the creation path.

### CODE-5 · pkg/gateway/sessionserver/start.go, pkg/gateway/podlifecycle/podclaim — one accounting helper serves both concurrent bind paths, and a retry skips the pod whose reclaim went unacknowledged

Targets: the classify/record/threshold tail of `applySlotRetryPolicy`, `bindConcurrentSlot`'s
`BindReservedSlot` branch, the `slotBinder` interface, and the placement exclusion the §7.1
reclaim obligation requires (`podsession.SlotBindRequest`, `podclaim.SlotRequest`, and
`SlotClaimer.ClaimSlot`'s two candidate passes).

Extract the tail only. The release stays with each caller, which already knows its own
disposition:

```go
// accountSlotFailure records one failed or leaked slot against the pod's §5.2
// health ledger and retires the pod when the combined windowed-failure plus
// persistent-leak count crosses the unhealthy threshold. Both concurrent bind
// paths call it, so the create-time reserved path reaches the threshold §5.2
// obliges it to reach.
//
// spec: §5.2 (whole-pod replacement trigger); §6.2 (`leaked` slot semantics).
func accountSlotFailure(ctx context.Context, binder slotBinder, health *slothealth.Tracker,
    slots *slotstate.Registry, replacement func(pool string),
    leakGauge func(pod, pool string, leaked int),
    req podsession.SlotBindRequest, sbe *podsession.SlotBindError, leaked bool)
```

The body is today's `MarkLeaked` plus leak gauge plus `RecordLeak` arm, today's
`RecordFailure` arm, and the unchanged `Unhealthy → DrainSandbox → replacement → Forget →
ForgetPod → zero-gauge` tail, with the discriminator taken as a parameter instead of computed
inline. `binder` is still needed, for `DrainSandbox`.

Callers:

- `applySlotRetryPolicy` keeps its own `ReleaseSlotReservation(ctx, sbe.Pod, sbe.SlotID, sbe.Leaked)`
  call and passes `sbe.Leaked || relErr != nil`. Its retry iteration then carries
  `ExcludePod = sbe.Pod` when that discriminator was true, so the retry re-claims on a
  different pod under the §7.1 rule that a pod whose reclaim went unacknowledged carries no
  further attempt at the same session. `req` is the function's own value copy, so the
  exclusion lives for the remaining iterations of this request and reaches no other request.
  The loop's exit condition is unchanged and the non-retryable set stays §5.2's three
  reasons, so a clean release retries on the same pod exactly as it does today.
- `bindConcurrentSlot`'s reserved branch calls `accountSlotFailure(..., sbe, sbe.Leaked)`
  before `classifySlotBindFailure`, using the same collaborators `bindSlotWithRetry` already
  passes. `BindReservedSlot` keeps its own release.

`applySlotRetryPolicy`'s comment that "a clean release makes the failure transient, and a
failed release leaks the slot permanently" is widened rather than corrected: it is true today
about the reservation release, and the change extends the release-outcome test from the
reservation counter to the pod-side reclaim.

The trade this deliverable ships, stated here rather than left open: a `leaked` disposition
withholds the counter decrement for the life of the pod, and `UnhealthyThreshold` is
`(maxConcurrent+1)/2`, so at `maxConcurrentSessions: 2` one unacknowledged reclaim both burns
a slot and drains the pod. This is §6.2's semantics applied consistently, and it is the
mechanism that makes a best-effort compensation's failure bounded. It changes behaviour only
at `maxConcurrentSessions >= 3`: at 2 the threshold is already 1, so a single cleanly released
bind failure drains the pod today.

The placement exclusion, stated whole because it is the deliverable's one new field:

- **The field.** `ExcludePod string` on `podsession.SlotBindRequest` and on
  `podclaim.SlotRequest`, carrying a Sandbox name. Its doc comment states that it is a
  read-only placement filter for §5.2's `**Max retries:**` constraint on a pod holding a
  reclaim the adapter did not acknowledge, in the vocabulary
  `MaxPodUptimeSeconds` already uses on both structs, and that an empty value excludes
  nothing.
- **Where it is set.** One site: `applySlotRetryPolicy`'s retry iteration, from `sbe.Pod`,
  under the same `sbe.Leaked || relErr != nil` discriminator that already chooses `RecordLeak`
  over `RecordFailure`. Nothing clears it, because each client request builds its own
  `SlotBindRequest` and every new request carries the zero value.
- **Where it is read.** `Binder.connectSlot`'s existing `podclaim.SlotRequest` mapping carries
  it through, and `ClaimSlot` skips the named pod with one `continue` in the pass-1 scan of
  claimed pods and one in the pass-2 idle-pod scan, placed beside the `expiredByUptime` skip
  and documented the same way. No interface signature changes: the field rides on the request
  struct `slotBinder.BindSlot` already takes, so every implementing type and every test fake
  compiles unchanged.
- **When it does not fire.** The retry re-claims on the pod that may still be executing the
  prior attempt's `Shutdown`. Because `SlotID == SessionID` and the tree is
  `/workspace/slots/{sessionId}/`, a lagging `os.RemoveAll` either lands between the retry's
  workspace preparation and its `StartSession`, in which case `claimSessionSlotUnderLock`
  re-creates an empty entry and tree through `ensureSlotStateLocked` and the session starts on
  an empty workspace, or lands after the retry is running and tears down a healthy session.
  CODE-2's survived-the-start confirm does not catch the first case, because the entry it
  checks is the one its own claim recreated. The tier-1 retry case, the tier-2
  placement-filter cases, and the tier-4 re-bind assertion below are what observe the
  exclusion firing.
- **Reachability.** At every concurrency the retry policy runs at. At
  `maxConcurrentSessions: 2` the unhealthy threshold is already 1, so the same iteration also
  drains the pod, but `DrainSandbox` only stamps the `lenny.dev/drain-request` annotation the
  WarmPoolController acts on asynchronously, and `ClaimSlot`'s pass-1 scan of claimed pods
  reads the per-pod claim rather than the Sandbox phase. The drain therefore does not keep the
  immediate retry off that pod and the exclusion is what does.

## Staged schema, chart, and migration changes

None. `schemas/lenny-adapter.proto` is never opened: the remediation programme's rule S-2
reserves the single proto edit to step R1b, and the remedy needs no wire change by
construction. No chart value and no migration.

## Staged docs changes

### DOCS-1 · docs/reference/state-machines.md — the per-slot sub-state table gains the new row

Under `### Per-slot sub-states`, add the row matching the §6.2 edge, immediately after the
`receiving_uploads` → `running` row:

```
| `receiving_uploads` | `slot_cleanup` | The bind is abandoned or fails before the runtime has been given the session, a start still in flight included |
```

The tier-11 documentation reconciliation test polices agreement between this table and the
§6.2 block, so this lands in the same step as SPEC-4 and CODE-3.

## Testing

Every test carries a `// spec:` annotation naming the sections it exercises. Every tier-2 and
higher test carries a `// diagnosis:` comment above its function declaration.

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
  `emitFinalUsage` moves from the `bound` branch to the `started` branch. The expiry-timer
  assertion is kept as a guard that `deregisterSlotLocked`'s unconditional cancellation has
  not moved under the `started` branch; it duplicates
  `TestShutdownCancelsTheEndingSessionsExpiryTimers_spec_4_9` on purpose.
- **Bound and started.** The fixture drives the start through `noteRuntimeStarted`, so the
  session is in `runtimeLive`, which is what the cleanup-outcome assertion now turns on.
  Today's full teardown is unchanged: the signal when no bound entry remains, the close, the
  tree removal, and the cleanup-outcome report all still run, in that order.
- **Claimed but not yet recorded.** A reclaim of a session whose `st.started` is true and
  which `runtimeLive` does not hold runs the runtime teardown, removes the entry and the tree,
  and files no cleanup-outcome report. This is the case that pins the two predicates apart,
  and it fails if the report is regated on `st.started`.
- **Co-tenancy hazard.** Drive `SocketRuntimeProcess` into `connected == true` with an empty
  active set through `Interrupt` of the last active session, then assert that `Shutdown` of a
  bound-but-unstarted entry leaves the connection, the spawned child, and the listener intact.
  Add the sibling assertion in `socketruntime_test.go` beside
  `TestSocketRuntimeProcessCloseScopedToSlot_spec_5_2`, recording that `Close` in that state is
  not the no-op its doc comment claims, so the hazard is pinned at the unit that owns it.
- **Start-versus-reclaim rollback, deterministic form.** `noteRuntimeStarted` returns false for
  a session whose entry was removed, and the `StartSession` rollback closes the runtime,
  releases the slot, and answers `FailedPrecondition`. Neither the rollback nor the reclaim
  files a `ReportSessionScrub` for that session. The concurrent form is the tier-7a case below.


Scope accounting to record in the deliverable: besides
`TestShutdownDrainsWhileARegisteredUnboundEntrySurvives_spec_5_2`, no existing adapter test
drives `Shutdown` for an unbound or unstarted entry, so CODE-1 breaks no shipped test. That
test's own path is unaffected, because its ending session is started and `boundRemains` is
still false after the deregistration. It must keep passing unchanged.

Excluded deliberately: an assertion that a not-started reclaim whose tree removal fails
reports `exited_cleanly: false`. `removeSlotTree` calls `slotlayout.RemoveTree` directly with
no injectable ops, and the repository's own precedent records that forcing that error from a
non-root unit test is not portable. Restoring it needs a removal seam on the pattern of the
whole-pod scrub's `Ops`, which no deliverable here stages.

### Gateway tests for CODE-4 and CODE-5, tier 1

Files: `pkg/gateway/podlifecycle/podsession/slotbinder_test.go`,
`pkg/gateway/sessionserver/slotretry_test.go`, `pkg/gateway/sessionserver/start_test.go`.

Fixture work is part of the deliverable rather than an assumption: `concurrentAdapter`
discards its `Shutdown` request and injects failures for `StartSession` alone, and serves
neither `PrepareWorkspace` nor `AssignCredentials`. It gains per-stage error injection
(finalize, setup, credential assignment, beside today's `startErr`), handlers for
`PrepareWorkspace` and `AssignCredentials`, and the request recording plus `uncleanExit` and
`shutdownErr` behaviour `recordingShutdownAdapter` carries today, so one fake drives the
table.

Cases, each `// spec: §7.1; §5.2; §6.2`:

- **Per-stage compensation table.** For the finalize, setup, credential-assignment, and
  session-start stages: exactly one `Shutdown` naming the session, with `deadlineMs` zero, and
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
- **Both bind paths reach the accounting.** `bindConcurrentSlot`'s reserved branch reaches
  `accountSlotFailure` with `sbe.Leaked`, and `BindReservedSlot` still performed its own
  release.
- **The retry moves pods after an unacknowledged reclaim.** At `maxConcurrentSessions: 4`,
  `applySlotRetryPolicy` still makes its second `BindSlot` call when `sbe.Leaked` is true, and
  that call carries `ExcludePod` equal to the failed attempt's pod; after a clean release the
  second call carries an empty `ExcludePod`. The non-retryable reasons still return the §5.2
  `SlotFailedError` on the first attempt in both cases.
- **The resume path.** A failed `Resume` sends the compensation on the still-open connection
  and releases the resume slot with the outcome.

### Placement-filter tests for CODE-5, tier 2

File: `pkg/gateway/podlifecycle/podclaim/slotclaimer_test.go`, beside
`TestClaimSlotSkipsOverUptimeClaimedPod_spec_6_2` and
`TestClaimSlotSkipsOverUptimeIdlePod_spec_6_2`, which pin the sibling read-only placement
filter across the same two candidate passes and already stand up the envtest API server this
package's `ClaimSlot` cases use. Each case carries `// spec: §7.1; §5.2` and the
`// diagnosis:` comment tier 2 requires.

- `ClaimSlot` skips the named pod in the pass-1 scan of claimed pods and places the slot on
  the next same-tenant pod with free capacity.
- `ClaimSlot` skips the named pod in the pass-2 idle-pod scan and acquires a different idle
  pod.
- `ClaimSlot` returns `ErrNoConcurrentSlot` when the excluded pod is the pool's only
  candidate, so the caller maps it to `WARM_POOL_EXHAUSTED` with `details.reason:
  "concurrent_slots_exhausted"` through the sentinel path it already has.
- An empty `ExcludePod` excludes nothing, which is the shipped placement behaviour and the
  case every other `ClaimSlot` test exercises.

No tier-3 item. No proto, JSONL, HTTP, or CRD schema contract changes, so tier 3's mandate is
not engaged; the adapter's no-op answer is pinned at tier 1 by the adapter cases above and
end to end at tier 4 below.

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
relays again, where it is rejected before the compensation; the shared runtime's connection
and listener survive, demonstrated by a later session carol binding and starting on the same
pod; and alice's later `Shutdown` still emits the §15.4.2 signal. No MCP-arming assertion:
`claimPodMCPStartLocked` refuses on `len(s.slots) != 1` and the claimant's own entry is
inserted above it, so carol beside a live alice arms nothing, with or without this change.
`// diagnosis:` states that a failure means the reclaim left the pod's registry, its
demultiplexer count, or its shared runtime in the state the residue produced; it must not
mention MCP arming.

**Tier 4, the datastore-crossing case.** Extend `tests/tier4_integration/recycle_scrub_path_test.go`,
which already runs envtest plus miniredis plus a real adapter. A bind whose compensating
`Shutdown` the adapter refuses is released with `leaked=true`, so the Redis slot counter does
not decrement, the per-pod claim stays `bound` rather than being patched to `recycling`, the
occupancy-zero recycle boundary does not fire, and the §5.2 threshold retires the pod instead.
The pool carries a second placeable pod, so the same case also asserts that the §5.2 retry
re-binds there rather than on the pod whose reclaim was refused. This is the consequence of
CODE-4 and CODE-5 that no fake-backed tier-1 test reaches.

**Tier 7a, one case.** The start-versus-reclaim race: a `StartSession` parked inside
`Runtime.Start` while the compensating `Shutdown` reclaims the slot, under `-race` with a
`lenny-test stress` budget. Assert that `StartSession` returns `FailedPrecondition`, that
`runtimeLive` is empty, that `runtimeIdleLocked` is true, and that the runtime was closed
exactly once. Assert the report as an invariant over the interleavings rather than as a fixed
count: the session yields at most one `ReportSessionScrub`, none when the reclaim lands before
`noteRuntimeStarted` records and one when it lands after. Run it on a pod with no co-tenant as
well as a co-tenanted one, because only the no-co-tenant pod exercises the branch where the
rollback close is the last close.

`tests/tier7a_load_local/shutdown_drain_gate_race_test.go`'s
`TestConcurrentShutdownsSendOneDrainSignal_spec_6_4` and
`TestShutdownDrainRacesAnIncomingSession_spec_6_4` already pin the one-signal and
unbound-entry properties and must keep passing unchanged. No new signal-frame case is added,
because `boundRemains` is untouched.

### Coverage and preflight

Run `lenny-test coverage --diff <merge-base>` and raise the changed lines to the 80% floor,
covering the error and boundary paths rather than the happy path. Before any tier above 1, run
the environment preflight in `.claude/rules/test-coverage.md`: confirm the loaded images match
the renderer, sweep terminal agent pods, confirm the warm pools reach Ready, reap orphaned
envtest processes, and confirm the same tier fails on a clean checkout of the merge base
before treating a failure as this change's.

## Edge cases and accepted failure modes

- **A retry after an unacknowledged reclaim loses one pod rather than the attempt.** The
  attempt keeps the §5.2 retry budget it has; only the pod whose reclaim went unacknowledged is
  disqualified from carrying it, which is what closes the cross-attempt window in which a stale
  `RemoveAll` could delete a retry's freshly materialized tree or tear down a retry that is
  already running. When that pod is the pool's only candidate the retry meets the shipped
  `WARM_POOL_EXHAUSTED` outcome with `details.reason: "concurrent_slots_exhausted"`, so the
  narrowed placement mints no new error code and no new client-visible category.
- **`exited_cleanly` carries two meanings.** The started path discards the tree-removal error,
  preserving today's classification of session-end slots; the unstarted path surfaces it,
  because it is the only signal the gateway has that the reclaim did not complete. Both are
  documented in `Shutdown`'s doc comment.
- **A pod bricked by a rolled-back start.** In the class-three interleaving on a pod holding no
  co-tenant, CODE-2's rollback `Runtime.Close` is the last close, and on the socket runtime the
  last close ends the shared connection, the spawned child, and the listener bound once at
  adapter start (`pkg/adapter/socketruntime.go:435-467`, the listener bound at `:156-161`). An
  ordinary session end reaches the same state through the same call, so this path is not
  distinguished from a normal last close and this proposal stages no retirement trigger for it.
  Whether a pod whose runtime process has been closed is reusable is a pre-existing question of
  the shipped recycle disposition, and it is not answered through the cleanup-outcome
  accounting channel.
- **Faster pod churn at `maxConcurrentSessions >= 3`.** The `leaked` disposition withholds the
  counter decrement and counts persistently, so an unacknowledged reclaim moves a pod toward
  retirement sooner than a clean release would. Accepted as §6.2's semantics applied
  consistently. At `maxConcurrentSessions: 2` nothing changes, because the threshold is already
  1.
- **A compensation sent to a pod in coordinator-hold state is refused.** The hold-state
  allowlist admits only the fence, version negotiation, the event stream, and the health probes,
  so the reclaim returns an error and the slot is correctly classified `leaked`, which §6.2 then
  routes to the §5.2 threshold. No special case is written for it.
- **The connect stage compensates nothing.** The slot is reserved before any workspace RPC, so
  the adapter holds no entry and `Leaked` stays false there. §6.2 still has no terminal out of
  `slot_assigned`; that hole is recorded in the summary rather than closed here.
- **`slotCount` still counts a registered-but-unbound entry.** The §28.5.3 count fails closed on
  purpose and its comment says so. With the residue removed the count is fed no residue, so the
  predicate is left exactly as it is.

## Files touched on application (non-spec)

- `pkg/adapter/session.go` — `Shutdown`'s clause two, its response, its doc comment, and the
  `StartSession` rollback.
- `pkg/adapter/runtimegeneration.go` — `noteRuntimeStarted`'s signature, body, and doc
  comment, and the new `runtimeHoldsLocked` accessor.
- `pkg/adapter/slotsession.go` — `deregisterSlotLocked`'s doc comment only.
- `pkg/adapter/resume.go`, `pkg/adapter/sdkwarm.go` — the `noteRuntimeStarted` call sites.
- `pkg/sandbox/slotstate/slotstate.go` — `ValidTransitions()` and its doc comment.
- `pkg/gateway/podlifecycle/podsession/slotfailure.go` — `SlotBindError.Leaked`.
- `pkg/gateway/podlifecycle/podsession/slotbinder.go` — `slotCleanupBudget`,
  `compensateFailedSlotBind`, `materializeSlot` and `materializeSlotStages`,
  `ReleaseSlotReservation`, `BindReservedSlot`, `ClaimSlot`'s connect-stage release,
  `SlotBindRequest.ExcludePod` and its pass-through in `connectSlot`'s `podclaim.SlotRequest`
  mapping.
- `pkg/gateway/podlifecycle/podsession/binder.go` — `Binder.Resume`'s failure branch and
  `releaseResumeSlot`.
- `pkg/gateway/sessionserver/start.go` — the `slotBinder` interface, `accountSlotFailure`,
  `applySlotRetryPolicy` (including the retry iteration's `ExcludePod`), `bindConcurrentSlot`,
  `rollbackClaim`.
- `pkg/gateway/podlifecycle/podclaim/slotclaimer.go` — `SlotRequest.ExcludePod` and the skip
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
  `tests/tier4_integration/concurrent_workspace_test.go`,
  `tests/tier4_integration/recycle_scrub_path_test.go`, `tests/tier7a_load_local/`.
