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

## Edge cases and accepted failure modes

- **A compensation for a session the adapter holds nothing for.** `stageWorkspace` sends
  `PrepareWorkspace` only when the plan carries uploads, so a workspace-preparation failure
  on an upload-free plan leaves no adapter entry. The new no-op sentence in the §4.7
  `Shutdown` row makes this a clean-exit response, so the failure is accounted transient and
  a blob-store outage does not retire healthy pods.
- **A reclaim the adapter does not acknowledge on a pod serving concurrent sessions.** The
  slot is `leaked`. Its occupancy is held, it counts persistently toward the §5.2 threshold,
  and the pod retires through the shipped trigger. This is §6.2's own disposition applied
  consistently rather than a new rule, and it is what makes a best-effort compensation's
  failure bounded. A retry the §5.2 slot retry policy places goes to a different pod; when
  that pod is the pool's only candidate the retry meets the shipped `WARM_POOL_EXHAUSTED`
  outcome.
- **A client retry of the §15.1 start after a failed bind on a create-time-reserved slot.**
  The row keeps its §4.6 pod binding, so the retried start reconnects to the same pod under
  the same slot identifier. The §5.2 retry policy does not place this attempt, and its
  constraint therefore does not reach it. When the adapter acknowledges the reclaim it holds
  no entry for the slot and the retried start materializes it afresh; what is not bounded
  there is a reclaim whose tree removal is still running when the retry materializes the same
  tree. When the adapter does not acknowledge the reclaim the slot is `leaked`, holds its
  occupancy, and counts toward the §5.2 whole-pod replacement trigger, and the adapter's entry
  survives in whatever state the failed stage left it. The adapter refuses the retried start
  only when the first attempt had already admitted a start on that slot, so an entry left by
  the workspace-preparation stage or the credential-assignment stage admits the retry, which
  then reuses the slot tree the first attempt created. A reclaim that reaches the adapter
  after that point removes the entry, deletes the slot tree, takes the session off the shared
  runtime process, and ends the retried session the adapter had already started. §5.2's
  placement constraint is what keeps a lagging reclaim away from the retries that policy
  places, and a start pinned to its create-time pod binding has no equivalent. Closing that
  would require the failed start to drop the session's create-time pod binding so the retry
  re-reserves through the §5.2 retry policy, which this proposal does not stage.
- **A reclaim the adapter does not acknowledge on a pod serving one session.** The failed
  attempt releases the pod's claim and the pod retires under §6.2's pre-attached failure
  disposition, so the residue dies with the pod. §6.2 states the `leaked` sub-state and §5.2
  the whole-pod replacement trigger under concurrent occupancy alone, so the disposition is
  not stated for an exclusive pod and is not needed there.
- **A start that races the reclaim.** When the adapter admitted the start before the reclaim
  removed the slot, the start finds its slot gone at the point it would record the runtime as
  holding the session, takes the session back off the shared runtime process, and refuses.
  When the reclaim's answer precedes the start's admission, the start's own claim re-creates
  the registry entry and the slot tree, and the adapter cannot tell it from a fresh bind: the
  pod then holds a started session for a bind the gateway abandoned, until the pod retires.
  That retirement is bounded on a recycling pool by §5.2's `recycle.maxSessionsPerPod` and,
  where it is configured, `maxPodUptimeSeconds`. Closing that ordering needs a record of an answered
  reclaim that outlives the entry, which this proposal does not add. Neither ordering reports
  a cleanup outcome, because the slot never reached `running`.
- **A bind abandoned at the connect stage.** The slot is reserved before any workspace RPC,
  so the adapter holds nothing and no reclaim is owed. §6.2 still has no terminal out of
  `slot_assigned`; that hole is pre-existing and is recorded in the summary rather than
  closed here.
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
The handler runs the slot release when the adapter holds an entry for the named session, runs the runtime teardown under the narrower precondition [Section 4.7](#47-runtime-adapter) states, and runs the whole-pod scrub when the recycle disposition is set, so no operation is selected by a field's presence standing in for a scope.
```

The paragraph's first two sentences are untouched. The second sentence ("The per-slot
teardown and the whole-pod teardown are the same operation on the same address, and what
remains is the recycle disposition the request carries beside it.") stays true under the
split, because it is about addressing rather than about the teardown's internal structure.

### SPEC-1 · spec/04_system-components.md § 4.7 (Gateway → Adapter RPC table, `Shutdown` row)

Replace the `Shutdown` row's first sentence and add the no-op sentence. The row currently
opens, verbatim:

```
| `Shutdown` | Graceful end-of-session teardown of the named session: the adapter closes that session's runtime and releases the session's slot. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

Replace that opening with the text below, leaving the remainder of the row (from "On the
default disposition the pod is replaced." to the end) unchanged:

```
| `Shutdown` | Graceful end-of-session teardown of the named session, stated as two teardowns with two preconditions. The **slot release** is the [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) slot cleanup for that slot, and it runs whenever the adapter holds an entry for the named session, whether or not `AssignCredentials` has bound that entry. The **runtime teardown** runs only for a session whose start the adapter has admitted. Which RPC in this table starts a session depends on the pod's session mode and on whether the session is new or resumed; the precondition is the adapter's admission of that RPC, taken at the moment of admission rather than at the runtime's acknowledgement of it, so a start still in flight is torn down rather than skipped. It flushes the session's final usage report and then closes the runtime. The [Section 15.4.2](15_external-api-surface.md#1542-rpc-lifecycle-state-machine) graceful-shutdown signal precedes that close and carries a condition of its own, because the signal is pod-global and names no session: it goes out only when the deregistration leaves the adapter holding no bound entry, since sending it while a co-tenant is still bound would signal the shared runtime to terminate while it is serving that session. A request naming a session the adapter holds no entry for removes nothing, runs neither teardown, and answers with a clean-exit response. The request carries the recycle disposition beside that teardown rather than selecting a scope.
```

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
the same body follows the step's own form. Step 12 is untouched: it states that the adapter
closes the session runtime, which runs for every bound entry the call removed and carries no
co-tenancy condition.

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
**Pod-side reclaim on a failed bind.** A gateway bind attempt that fails after the gateway has issued its first pod-side RPC for the session (the creation finalize block, the [§15.1](15_external-api-surface.md#151-rest-api) start transition, or a [§7.3](#73-retry-and-resume) re-attach onto a replacement pod) also reclaims the state that attempt created on the pod: the gateway sends `Shutdown` for the session on the connection the failed stage still holds, before that connection closes, and releases the slot reservation afterwards. The obligation begins with an attempt's first such RPC and ends when that attempt succeeds. The gateway sends the reclaim even when the failing RPC's own context is already cancelled or past its deadline, because that is the case in which the adapter may have started the session. On a pod serving concurrent sessions, a reclaim the adapter does not acknowledge leaves the slot `leaked` under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) disposition, which holds the slot's occupancy and counts it toward the [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) whole-pod replacement trigger. The slot identifier is the session identifier and the slot's workspace tree is derived from it, so a further attempt at the same session on that pod would reuse a tree the lagging reclaim may still be deleting; [Section 5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) keeps the retries its slot retry policy places off that pod. Nothing about the attempt's retryability changes. On a pod serving one session the failed attempt releases the pod's claim and the pod retires under the [§6.2](06_warm-pod-model.md#62-pod-state-machine) pre-attached failure disposition, so the reclaim's residue does not outlive the pod; the `leaked` sub-state and the whole-pod replacement trigger are stated for concurrent occupancy and do not apply there. The pre-attached disposition governs the pod; this reclaim governs the slot state on a pod that is released or reused rather than terminated.
```

The paragraph states no deadline for the reclaim. §5.2's per-slot cleanup timeout is the
figure the gateway reuses, cited from the code rather than restated here.

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
That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency. It also runs when a bind is abandoned or fails after its slot enters `receiving_uploads` and before it reaches `running` ([Section 6.2](06_warm-pod-model.md#62-pod-state-machine)), and a cleanup on that path reports no outcome, because `ReportSessionScrub` advances the pod's served-session count and that count records the sessions the pod's shared runtime process has been given. A cleanup that reclaims a slot the pod's shared runtime process was given is a session release like any other and reports its outcome. A session release produces at most one cleanup-outcome report, filed by the cleanup that reclaims the slot.
```

The withheld-report rule and the one-report rule land in the scrub-model paragraph rather than
in the `**Slot cleanup:**` bullet, because that bullet sits under a heading scoped to
`maxConcurrentSessions > 1` while both rules hold on a pod of either concurrency, and because
the scrub-model paragraph is where §5.2 already states the per-slot cleanup uniformly across
session-mode configurations. That same uniformity sentence is what carries the bullet's action
list across the concurrency boundary, so the appended pointer clause discharges the scoping
question for both anchors without stating a second action list. Nothing else in the bullet
changes: its trigger, the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula, the
CRD validation rule, and the leaked outcome all stand as written.

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
residue classes; that terminal is a pre-existing hole recorded in the summary.

### SPEC-4 · spec/06_warm-pod-model.md § 6.2 (prose after the fence)

Insert the paragraph below immediately after the fenced block closes and before the
paragraph beginning `**`reserved` hold semantics.**`:

```
**Pre-`running` slot cleanup.** A slot reaches `running` when the pod's shared runtime process has been given the session, which is the moment the `receiving_uploads → running` trigger above names. Every earlier stage of the [§4.7.9](04_system-components.md#479-startup-sequence-for-type-agent-runtimes) step-5 bind sequence, credential assignment included, and a start still in flight, whose session the runtime has not yet acknowledged, leave the slot in `receiving_uploads`. A bind abandoned or failed at one of those stages takes the `receiving_uploads → slot_cleanup` edge; a session the runtime has already been given is a slot in `running` and takes the `running → slot_cleanup` edge the fence above carries. The cleanup either edge runs, and the report the pre-`running` reclaim withholds, are the ones [§5.2](05_runtime-registry-and-pool-model.md#52-pool-configuration-and-execution-modes) states.
```

## Spec files touched

- `spec/04_system-components.md` — §4.1 Request Message Scope (one sentence), §4.7 Gateway →
  Adapter RPC table `Shutdown` row (first sentence plus one sentence), §4.7.9 step 5 (one
  sentence).
- `spec/05_runtime-registry-and-pool-model.md` — §5.2 `**Scrub model.**` paragraph (the
  per-slot cleanup pointer and the cleanup-outcome report rules appended), the §5.2
  `**Slot cleanup:**` bullet's action-list sentence (replaced), and the §5.2
  slot-retry-policy `**Max retries:**` bullet's pod-selection sentence (replaced).
- `spec/06_warm-pod-model.md` — §6.2 per-slot sub-state fence (one edge), the prose after
  it (one paragraph), and the §6.2 `resuming` mid-resume cancel bullet (one clause).
- `spec/07_session-lifecycle.md` — §7.1 atomicity paragraph (one parenthetical) and a new
  paragraph after it carrying the reclaim obligation, §7.2's mid-resume snapshot-close
  sequence (the section preamble's premise sentence deleted, a sentence added to step 2, and
  step 3 replaced), and §7.3's resume flow (one sentence appended after the numbered list).
- `spec/29_communication-scenarios.md` — §29.4 session-end step 13 (one sentence appended).
