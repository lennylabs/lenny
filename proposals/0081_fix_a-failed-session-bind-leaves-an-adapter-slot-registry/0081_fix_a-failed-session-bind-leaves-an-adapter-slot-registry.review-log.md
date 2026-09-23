# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (hard compaction, 2026-09-23, before run 0081-opt3). The prior standing context, 1,438 lines through
compaction pass 34, is archived verbatim in `0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md`
under `## Standing context archived 2026-09-23 before run 0081-opt3`. This section was rewritten against the current
staging and the ledger through `[operator.post-r16]`, applying every `CORRECTS` and operator `DECISION` in it. Recover an
older entry from the archive by its bold subject; a claim absent from this section is one this log no longer carries.

How to use this section. Cite entries by their bold subject. The current staging is: a caller-minted per-attempt
`bind_attempt` token stamped once in `ensureSlotStateLocked` under `s.mu`; `unconditional_teardown`;
`SLOT_BIND_ATTEMPT_SUPERSEDED` (29) and `SLOT_BIND_ALREADY_STARTED` (28); §4.7.1 rules 1-9 (admission) and 10-15
(`Shutdown`), stated once; the §4.7.1 registry critical-section paragraph as the one atomicity home; the §5.2
disposition table as the one home of every cleanup's report, `leaked` and hold disposition, with residue stated once
after the table; the §5.2 reclaim-hold paragraph carrying the decision-47 premature-removal sentence; CODE-6's
expired-acquisition disposition with a `guarded` conjunct on every `completed` predicate; compensation of every failed
bind, typed refusals included, with a `cause` label on the superseded counter; CONF-1 with one case per numbered rule;
and the comment-carrier reduction as one shared block with CODE-10, CODE-11 and CODE-12 as sub-blocks. An archive entry
naming a bind epoch, `expected_bind_epoch`, `ExcludePod(s)`, a per-rule §15.4 table, lettered CONF-1 clauses, a residue
column or a compensation suppression on typed refusals describes a withdrawn design. Line numbers into the proposal
files are not recorded here.

### Settled

- **DECISION: the disposition of every per-slot cleanup is ONE TABLE, in the staged §5.2 `**Scrub model.**` append.** Rows key on what is reclaimed, who performs it and which act fails; other sites cite it. Rejected: a §6.2 two-exit home.
- **DECISION: where the old sites disagreed, the table takes these answers.** A failed cleanup outside a `Shutdown` is not `leaked`; rows key on the failing act, never "completed"; the whole-pod scrub ends directories only; §15.4 cites §5.2.
- **DECISION: the table's two pre-`running` rows are keyed "reclaimed by a `Shutdown`", not "reclaimed by the pod-side reclaim".** The §7.1 reclaim is a `Shutdown`, and rules 12 and 14 both answer `reclaimed`. Rejected: a third row pair.
- **DECISION: the adapter's atomicity is ONE paragraph, `**The registry critical section.**`, in the staged §4.7.1 block.** Membership reads "rules 2 through 7" because rule 1 (`validateBindFields`) runs before `s.mu`. Rejected: a sixteenth rule; per-rule atomicity clauses.
- **DECISION: `## Design (as the spec must state it)` is a choice record,** one paragraph per choice naming ground and owning block. Deleting it was rejected: the over/under-approximation grounds and the two-field rationale live nowhere else.
- **DECISION: §15.4's fifteen-row per-rule wire table is DELETED.** §15.4 is a pointer at §4.7.1 plus the conformance criterion; the hold block still publishes `ABORTED`. Rejected: §15.4 owning status and `ErrorCode`.
- **DECISION: §15.4's conformance criterion quantifies over REQUESTS and states no rule-selection predicate.** §4.7.1's cascades are total and first-match. Rejected: "earliest-numbered rule" wording; deleting the criterion; a rule 5/6 exception clause.
- **§4.7.1's two cascades are TOTAL, and that totality is what the §15.4 criterion rests on.** Rule 7 admits any other request; rules 11-14 exhaust `Shutdown`; rules 8 and 9 sit outside. A partial cascade voids the criterion.
- **DECISION: the §4.7.1 "Which fields each request carries" table is the single home of `bind_attempt`/`mid_session` carriage.** The closure over unlisted requests sits on the table's lead-in; prose keeps rationale only.
- **DECISION: the stamp contract is PARTITIONED between rule 4 and the stamp-once paragraph, and neither proposition has two homes.** Rule 4 owns the write and its condition; stamp-once owns exclusivity, immutability and ownership.
- **Rule 4 produces a FRESH ENTRY stamped with the attempt's EXISTING token; nothing on the demotion path produces a token.** Demotion and the following pod-warm bind are one bind attempt; "a fresh token" wording is a defect.
- **DECISION: the closed entry-creating-RPC list lives in §4.7.1's `**Admission.**` preamble and §5.2 cites it.** Rule 2 delegates the hold's scope to §5.2 while §5.2 takes the request set from §4.7.1; that is two facts crossing rather than a citation loop.
- **The seven entry-creating RPCs §4.7.1 enumerates are exactly the tree's set, with no eighth.** `ensureSlotStateLocked` has three callers (`ensureSlotPaths`, `assignCredentialsSlot`, `claimSessionSlotUnderLock`); `RotateCredentials` and `ExportPaths` create nothing. Do not re-derive.
- **DECISION: the clean-exit gap is closed by ONE clause on §4.7.1's reclaim-outcome rule.** `absent` and `superseded` report a clean exit; only `reclaimed` can report unclean. It is the corpus's first definition of `exited_cleanly`.
- **DECISION (open decision 23, RESOLVED): §7.1's leak predicate keeps the WIDE form,** "whatever outcome that answer carries". It fails closed against a non-conforming adapter and lets CODE-4 read only the RPC error and the clean-exit flag.
- **DECISION: §4.7.1's attempt-mismatch rule is narrowed to "No `Shutdown` naming a bind attempt removes such an entry; within this cascade only the unconditional-teardown rule does."** `DemoteSDK`, `ConfigureWorkspace` rollbacks and the hold-timeout pass also remove untokened entries.
- **The refusal a later bind meets at an UNTOKENED entry is the STARTED-SESSION rule, never the attempt identity rule.** Rule 5 requires the resolved entry to carry a non-empty token.
- **DECISION: the §4.7 `Shutdown` row's slot-release predicate is "whenever the request REMOVES an entry", not "whenever the adapter HOLDS an entry".** Rule 13 holds an entry and performs neither teardown.
- **DECISION: SPEC-1 gains a §4.7 `DemoteSDK` row, and it is the single spec home of the demotion's registry removal.** Since `[operator.post-r16]` it reads "the registry's single entry, whichever session holds it", so it guarantees nothing about whose entry it is. DOCS-2 mirrors it. Rejected: stating it in rules 1-15, §6.1, §15.4 or a SPEC-7.
- **`Server.DemoteSDK` returns `codes.Internal` from `sw.DemoteSDK` BEFORE it deregisters anything** (`pkg/adapter/sdkwarm.go`), so a failed demotion close opens no hold and the entry stands; the table has its own row.
- **A pre-`running` slot can be released by the demotion or the hold-timeout pass, not only by a `Shutdown`.** `deregisterStartedSessions` selects on `st.started`, set before `Runtime.Start`; `DemoteSDK` releases `anyRegisteredSession()` unfiltered.
- **The hold-timeout pass deregisters in PASS 1** (`deregisterStartedSessions`, `pkg/adapter/slotsession.go`, called from `onHoldTimeout`) under one `s.mu` hold sorted by identifier, and closes in `terminateHeldSession`. It does not terminate the pod.
- **DECISION: the reclaim hold ends only on a cleanup that COMPLETED, and otherwise holds for the life of the pod.** A `completed` flag gates the deferred `release()`. Rejected: an "unreclaimed" marker, a permanent status, a gauge.
- **The completion predicate differs by site, deliberately, and all three are stated.** `Shutdown` and `terminateHeldSession`: `guarded && closeErr == nil && treeErr == nil`; guard-acquiring `releaseSessionSlot`: `guarded && treeErr == nil`, because it closes no runtime (`CORRECTS` by `[redesign.8.fix.1]`).
- **DECISION (`[operator.47]`, option A): SPEC-3's reclaim-hold paragraph states that a workspace removal performed before every request still writing under the identifier has stopped writing is an act that did not return without error.** It names the ordering and never the guard. Rejected: (B) dropping `guarded`, which frees an identifier over a tree a parked `Resume` extraction is re-creating.
- **DECISION: CODE-6's `**Disposition of an expired acquisition at a removing site.**` is the ONE home of what a removing site does when its guard acquisition expires.** It keeps the unguarded removal as shipped, routes the case onto the decision-47 sentence and the table's failed-act rows, and defines `guarded`; its site table carries the removal column only (`[non-spec.16.fix-G2.1]`). Rejected: releasing the hold.
- **DECISION (`[non-spec.15.fix-G1.1]`): the guard's acquisition step is "non-blocking send first, then select against `ctx.Done()` only when the channel is full",** stated once in CODE-6's `slotGuards` preamble and cited by `lockSlotGuard` and `acquireSlotGuardForResolve`. A bare two-case select picks at random when both are ready.
- **DECISION: CODE-1's cleanup-outcome report stays keyed on `closeErr` ALONE.** The `errors.Join(closeErr, treeErr)` re-key retires a pod on one failed `os.RemoveAll`. A tree failure logs `slot_tree_removal_failed`; report `released`, hold held.
- **"Completed" and "reached `released`" are INDEPENDENT predicates across the three arms that traverse `slot_cleanup ──→ released`.** Any trigger or test equating them is wrong.
- **DECISION (`[redesign.5.fix.1]`): the cleanup has TWO completion terms, each defined once.** `completed` is pod-side (SPEC-3's hold paragraph) and decides the hold; `acknowledged clean` is gateway-side (SPEC-2's §7.1 paragraph) and decides `leaked`. The gateway-side predicate is never written as "completed".
- **DECISION: the `slot_cleanup ──→ released` fence entry carries a BARE POINTER and no trigger,** `(see §5.2)`, and the `leaked` entry likewise. No short trigger is true of every traversal.
- **`slotlayout.RemoveTree` is best-effort across four directories** and returns the first error, so spec text distinguishing them is unimplementable. `EnsureTree` is idempotent, so a successor materializes INTO a residue.
- **`deregisterSlotLocked` cannot fail,** so deregistration opens the hold and is not an owed act in the completion predicate. It is the sole `delete(s.slots, …)`.
- **The §5.2 "graceful window of ten seconds" for the §10.1 hold-timeout termination is the shipped constant** in `onHoldTimeout` (`holdstate.go`, one shared pass-2 context, commit `3997f502b`). CODE-6 re-scopes it to a guard-acquisition deadline plus a per-member close context.
- **DECISION (open decision 40, RESOLVED as staged): the ten-second graceful close window is NOT operator-tunable.** SPEC-3 fixes it, so the override rule's antecedent fails; ten seconds is the platform's existing SIGTERM-to-SIGKILL pivot and no runtime configures a longer grace.
- **`releaseSessionSlot` reports nothing, calls NO `Runtime.Close`, and `reportSessionScrub` has exactly ONE caller,** the `Shutdown` handler. SPEC-3's report biconditional records shipped behaviour.
- **DECISION: the §4.7 `ReportSessionScrub` row's trigger clause is re-keyed into a citation of §5.2 and its `sessionsServed` clause onto the report,** under SPEC-3. The addressing sentence stays verbatim on one physical line for a tier-11 gate.
- **The production adapter-`Shutdown` caller set is FIVE sites behind ONE builder** (`Client.shutdown`, adapterclient/client.go), which sets `unconditional_teardown` for `Shutdown` and `ShutdownRecycle`. `Binder.shutdownAdapter` and its recycle arm are among its callers and the staging names them, so the §11.4 revoke and the retire path cannot hit rule 10. There is exactly one non-test `adapterv1.ShutdownRequest{` literal.
- **DECISION: CODE-1's handler has ONE exit, `answerShutdown(outcome, exitedCleanly, untokened)`,** which runs the recycle scrub; the shipped trailing scrub clause is deleted or `ReportPodScrub` double-sends. Two returns bypass it: the empty-session-id rejection and the two-field precondition's `INVALID_ARGUMENT`.
- **The `Shutdown` two-field precondition is a wire-visible break on every in-tree test literal, and the compiler cannot see it.** Sweep by `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/`; it belongs to the CODE-1 step.
- **There are TWO mandatory-field wire breaks, not one.** Rule 10 on `Shutdown` and rule 1 on the five bind-sequence RPCs. The second sweep's grep MUST carry the `adapterv1.` qualifier; exactly one `MidSession: true` literal exists and rule 1 admits it unchanged. Stage the grep, never a count.
- **DECISION: the per-slot guard's scope is a PREDICATE plus a derivation table, never an RPC enumeration.** Path work outside `s.mu` takes the slot guard before the resolve.
- **DECISION: `Shutdown` compares under `s.mu` FIRST, acquires the guard only on the arm that destroys, and RE-DECIDES under `s.mu` afterwards.** Non-removing arms never wait on guard holders; the re-decision catches hold-timeout pass-1 removal.
- **DECISION: `releaseSessionSlot` splits into a guard-acquiring form and `releaseSessionSlotUnderGuard`,** because `Resume` holds the non-re-entrant guard across its rollback sites. It gains a `ctx` parameter, as does `ReleaseSlotForTest`.
- **DECISION (`[non-spec.16.fix-G3.1]`): `deregisterSlot` is retired, and its two test callers in `podmcp_arming_internal_test.go` become an inline `s.mu` lock plus `deregisterSlotLocked`,** stated once in CODE-6's retirement sentence. Routing them through `reclaimSlotLocked` opens a hold nothing releases.
- **DECISION: CODE-8 is a closure SIGNATURE change, `reclaim := func(cause error)`, plus an enumerated call-site sweep** in `Prepare` and `Launch`. The closures skip `failPhase` on either typed refusal so the pod is not drained; no credential release sits on that arm.
- **DECISION (`[operator.post-r16]`, from `[non-spec.16.fix-G1.1]`): every failed bind attempt is compensated, typed refusals included,** at `materializeSlot` and at `Binder.Resume`'s failure branch, matching §7.1 with no carve-out. The compensation cannot reclaim a session the gateway treats as live. Rejected: restoring the suppression and carving refusals out of §7.1.
- **DECISION (`[operator.post-r16]`): `lenny_slot_compensation_superseded_total` keeps every compensation and gains a `cause` label, `refusal` or `failure`,** derived by `compensationCause` in CODE-4 from CODE-7's sentinels. Rejected: excluding refusal compensations from the series.
- **DECISION: CODE-4's attempt-scoped credential release is extended to `Binder.Prepare`'s credential-assignment failure arm.** It releases through `CredentialAssigner.Release(leaseID string)`, which widens the interface (`[non-spec.13.fix-G1.1]`) with a grep-closed sweep row for the test fakes. Rejected: session-wide `ReleaseSession`.
- **DECISION (`[non-spec.16.fix-G3.1]`): the adapterclient bind-sequence method signatures have one home, CODE-4's `client.go` Targets bullet.** `AssignCredentials` and `RunSetup` take a trailing `bindAttempt`, `PrepareWorkspace` a trailing `bindAttempt, midSession`, `FinalizeWorkspace` a `bindAttempt` before its shipped `midSession`, and `ResumeParams` a `BindAttempt`.
- **The no-entry compensation case is grounded on a `stageWorkspace` FAILURE PATH, never on an upload-free plan.** `stageWorkspace` has five error returns before the `PrepareWorkspace` send, which is guarded by `len(uploads) > 0`.
- **`mid_session` is SHIPPED on `FinalizeWorkspaceRequest` (field 4) and ABSENT from `PrepareWorkspaceRequest`.** SCHEMA-1 adds it to `PrepareWorkspace` alone; no production code sets it today, and the proposal wires it first in `upload_to_session.go`.
- **SCHEMA-1's field numbers are fixed and re-verified.** `bind_attempt` on PrepareWorkspace 5, FinalizeWorkspace 6, RunSetup 5, AssignCredentials 4, Resume 16, Shutdown 7; `PrepareWorkspaceRequest.mid_session = 6`; `ShutdownResponse.slot_reclaim = 3`; `ErrorCode` 28 and 29. Do not re-derive.
- **DECISION (`[operator.20-41-45]`, decision 20): the two new `ErrorCode` values take 28 and 29.** The enum has run contiguously 1 to 27. Rejected: the 1000-1999 range, a scheme nobody defined.
- **Two adapter `ErrorCode` values are minted, 28 and 29, and they take NO §15.1 row.** §15.1 catalogs the client-facing `code`; the gateway renders no adapter `ErrorCode` into REST. The enum is nested, so the Go constants are `adapterv1.Error_ERROR_CODE_*`.
- **Field well-formedness is `validateBindFields(bindAttempt string, midSession bool) error` in `pkg/adapter/slot.go`,** returning bare `codes.InvalidArgument`. It must NOT route through `slotResolveError`.
- **`StartSession` and `ConfigureWorkspace` both CREATE the entry when none exists,** so either can create an untokened entry: rule 13 answers `superseded`, and only rule 12, demotion, hold-timeout or pod retirement removes it.
- **`FinalizeWorkspace` is UNCONDITIONAL on both bind paths and precedes the start,** so on a correct ordering a token-carrying RPC always creates the entry. `PrepareWorkspace` is conditional on uploads.
- **`ConfigureWorkspace` is the LAST step of the SDK-warm bind, issued from `Launch` after the tokened sequence,** so it never creates the attempt's entry and the ordinary SDK-warm bind never runs on an untokened entry.
- **`SLOT_BIND_ALREADY_STARTED` on `RunSetup` is reachable:** B's entry, abandoned A's tokenless late `StartSession` starts on it, and B's `RunSetup` matches the token and meets rule 6. This is the own-token rule-6 case, and it occurs on the concurrent-bind path only (`[operator.post-r16]` `CORRECTS`).
- **`writeSetupCommandError` branches on the gRPC code ALONE.** `FailedPrecondition` gives 422 `SETUP_COMMAND_FAILED` and anything else the retryable 503 fallback. It is a `*Server` method (`start.go`).
- **DECISION: §15.1's `SETUP_COMMAND_FAILED` row is WIDENED rather than the gateway's envelope selection narrowed.** SPEC-5 also replaces the row's exclusion sentence, re-keyed on the setup-command REQUEST. DOCS-3 mirrors the replacements in `error-catalog.md`, adds no row and never names the adapter codes.
- **The §15.1 row is the single home of the mapping FOR THE SETUP-COMMAND REQUEST ONLY.** Every other `/start` setup-window failure falls to §15.1's `STARTING_FAILED` row. The re-key holds on the slot path too, because start.go's `SetupCommandFailure` case precedes the `slotFailed` case.
- **DECISION (`[prune.4.fix.1]`): the SPEC-5 §15.1 rationale names NO client-facing code for any request other than the setup-command request.** The stage-by-stage mapping's one home is the Edge-cases envelope bullet.
- **Widening `SETUP_COMMAND_FAILED`'s cause falsifies none of the five other spec sites that mention it.** spec/07:208 and the §15.1 endpoint rows state sufficient conditions; the discriminator for a future candidate is an "any other …" complement clause, which §6.2:290 carries and SPEC-5 edits.
- **§5.2's retry placement prefers the SAME pod.** `applySlotRetryPolicy` re-submits the identical request with `maxSlotRetries == 1`, so a same-pod refusal is the request's last attempt. `**Max retries:**` stands untouched.
- **DECISION: §7.1's reclaim obligation is scoped by whether the pod survives the failure, rather than by a list of call sites.** `Binder.Prepare` and `Binder.Launch` drain through `failPhase` and send no compensation.
- **DECISION: SPEC-2's §7.1 atomicity-parenthetical edit is DELETED and the shipped parenthetical stands.** Session creation leaves no pod-side state on service mode or `MaxConcurrentSessions > 1`, and exclusive `Prepare` failure drains the pod.
- **`Binder.Prepare` runs at `/finalize` and `Binder.Launch` at `/start`, two independent HTTP requests.** `Launch` re-dials and cannot hold `Prepare`'s token, which is why the two starts carry none.
- **`Binder.failPhase` is `releaseCredentials` plus `drain` and issues no adapter RPC;** `drain` is a bare `podclaim.DeleteClaim`. `leaseAssigned` gates `releaseCredentials` alone, and `Binder.Prepare`'s only apiserver write is that drain.
- **The occupancy projection reads the pod's CURRENT PHASE, never the recycle setting.** `ProjectOccupancyPhase`'s no-claim branch is `Reserved→Idle`, `Claimed→Draining`, `("", false)` otherwise, so SPEC-4's re-keyed bullets are total over what it owns and `draining` is a fixed point.
- **SPEC-4 TIGHTENS the one-session-only control rather than removing it.** A claim deleted while the pod projects `claimed` sends it to `draining, then terminated` on either recycle setting, leaving `reserved → idle` as the only reuse edge.
- **`OccupancyReconciler` WATCHES ITS OWN OBJECT (`For(&Sandbox{})`), which is what answers convergence findings against SPEC-4's read-back,** and §4.6.3 makes the WarmPoolController the sole `Sandbox.status.*` writer. The watch answers convergence findings and sole-writer ownership answers field-manager findings; the gateway holds no `sandboxes/status` grant.
- **Both gateway leak ledgers share ONE `slothealth.Tracker` instance** (`cmd/lenny-gateway/sessiondeps.go`). Only the gauge differs.
- **The adapter today refuses a double start with `codes.Unavailable`,** so rule 6's `FAILED_PRECONDITION`/`CATEGORY_PERMANENT` answer is a deliberate retryability change on that path.
- **A grace-expired close is reported as CLEAN.** `SocketRuntimeProcess.Close` and `MCPRuntime.Close` return nil on the kill arm, so a shorter window cannot manufacture `leaked`.
- **`Runtime.Close` does NOT abort on a cancelled context in any of the three implementations,** and `SocketRuntimeProcess.Close` calls `releaseActiveLocked` before it consults the context, so CODE-2's rollback close on a cancelled context still performs the close.
- **The adapter enforces NO per-slot cleanup timeout of its own.** `removeSlotTree` takes NO `context.Context`, §5.2's "minimum 5s enforced by the adapter" has no implementation (pre-existing), and nothing bounds the directory removal.
- **The reclaim gate has exactly THREE production entry points.** `Binder.BindSlot` (through `applySlotRetryPolicy`), `Binder.BindReservedSlot` and `Binder.Resume`; both bind paths funnel into `materializeSlot`.
- **Adapter predicate nesting.** Entry present ⊃ bound (`st.sessionID`, set by `assignCredentialsSlot` and `claimSessionSlotUnderLock`) ⊃ started (`st.started`, set only in `claimSessionSlotUnderLock`) ⊃ `runtimeLive`. `live && !started` is unreachable.
- **`st.started` precedes `Runtime.Start`, and it has THREE setters.** `claimSessionSlotUnderLock` serves `StartSession`, `Resume` and SDK-warm `ConfigureWorkspace`; never name one RPC in a start predicate.
- **`deregisterSlotLocked` returns a NIL `*slotState` when `removed` is false.** CODE-1's `started := removed && st.started` and `live := removed && s.runtimeHoldsLocked(sessionID)` are safe only through `&&` short-circuit.
- **CODE-1's staged clause two preserves the shipped ORDER exactly for a started session.** emitFinalUsage, drain, Close, noteRuntimeClosed, removeSlotTree, cancelPodMCPIfRuntimeIdle, reportSessionScrub.
- **DECISION: two predicates, not one.** Runtime teardown gates on `st.started` (fails closed toward closing); the cleanup-outcome report gates on `runtimeLive` (fails closed toward not counting). Rejected: one shared predicate.
- **CODE-1's predicates agree with the §5.2 disposition table ROW FOR ROW, checked in both directions by at least four shards.** `exited_cleanly = closeErr == nil && (live || (guarded && treeErr == nil))`, the `live`-gated report keyed on `closeErr`, and `completed` for the hold column. Do not re-derive.
- **`leaked` and `acknowledged clean` agree ROW FOR ROW with the disposition table, checked at round 21.** `leaked` ⟺ not acknowledged clean over every `Shutdown`-performed cleanup, which CODE-4's `sbe.Leaked = cerr != nil || !cleanly` implements.
- **Defer order in CODE-1's removing arm is correct as staged.** `defer unlockSlot()` registers before the `completed`-gated `defer release()`, so the hold releases first and the guard unlocks last; `answerShutdown` runs before both.
- **`shutdownReclaimOutcome`'s ARM ORDER means an unconditional teardown against an untokened entry does not increment the untokened counter.** The coverage is scoped to the superseded arm on purpose.
- **The hold is keyed on the slot identifier, which IS the session identifier.** `SlotID == SessionID` on every path, so N concurrent cleanups refuse N distinct identifiers.
- **DECISION: the reclaim hold's scope is NARROWED to the seven requests §4.7.1's admission rules govern,** create or resolve, the §7.4 mid-session upload among them. Both halves have landed: §5.2's hold paragraph and the non-spec and summary sites all use the narrow scope.
- **DECISION (`[operator.20-41-45]`, decision 41): one transient `ABORTED` answer stands on both arms of the reclaim hold.** A life-of-the-pod hold is permanent for that pod rather than for the session. Rejected: a permanent status, which needs gateway pod exclusion outside this proposal.
- **The reclaim hold cannot refuse a mandatory credential control.** Revoke, rotate, extend and `CoordinatorFence` resolve through read-only `slotStateLocked`/`boundSlotState` (eleven call sites against `ensureSlotStateLocked`'s three), and during a hold no entry exists for them to resolve.
- **The whole-pod scrub is a RECYCLING-pod mechanism.** `leaked` pins occupancy and the scrub runs only at occupancy zero. On a one-session recycling pod the ending session's own `Shutdown` runs the scrub and no `leaked` sub-state exists.
- **§7.2 orders the step-3 reclaim BEFORE step 4's `coordination_generation` bump.** `Shutdown` is unfenced: the adapter validates the generation only in `CoordinatorFence` and `CheckpointBarrier`.
- **DECISION: the §7.3 resume classification is closed by TWO arms in `isTransientPodClaimError`, owned by CODE-5** (`CORRECTS` by `[operator.post-r16]`). `codes.Aborted` returns true; `errors.Is(err, ErrSlotBindAlreadyStarted)` holds the row in `awaiting_client_action` for the client's retry. Other resume causes keep the shipped classification.
- **`resumeOnPod`'s two branches are mutually exclusive.** The checkpoint-restore branch calls `podBinder.Resume` with no retry loop, so CODE-5's `accountSlotFailure` caller there cannot double-account.
- **The compensation is synchronous inside `materializeSlot`,** so the gateway's own retry never races its own reclaim; every racing retry is a fresh client request or a second replica.
- **`applySlotRetryPolicy`'s exhausted return is `SlotFailedError`, never an exhaustion sentinel,** so a refusal ends the request on every pool mode, `queue` included.
- **The shipped tier-3 CLOSED field-set gate is `TestShutdownMessagePostRemovalDescriptor_spec_4_1`,** the only `assertFieldSet`. `TestShutdownRequestUnsetRecycleWireIdentical_spec_4_7` is a golden-bytes pin enumerated by name so the mechanical sweep skips it.
- **The `running` boundary has ONE home and ONE vocabulary (`[redesign.6.fix.1]`).** The home is the record step of the §4.7.1 registry critical-section paragraph; "given the session" and its variants are grep-banned across the proposal files.
- **`runtimeLive` is a pod-level cohort.** Its only writers are `noteRuntimeStartedLocked` and `noteRuntimeClosed`, and `deregisterSlotLocked` does not touch it.
- **DECISION: the teardown precondition names no RPC and is stated in one place.** The home is the §4.7 `Shutdown` row, which states carriage only; rule 10 is the one home of the `INVALID_ARGUMENT` answer.
- **`RecordSessionScrub` has no per-session dedup.** It is the one production writer of `sessions_served`. The adapter emits `ReportSessionScrub` from one site, and `Client.ReportSessionScrub` is at-most-once on the wire.
- **DECISION: the refused `StartSession` reports no outcome.** CODE-2's rollback drops `releaseSessionSlot` and calls `cancelPodMCPIfRuntimeIdle()` directly on both the `StartSession` and `Resume` arms. The entry found may be a successor's, so releasing it would delete that entry.
- **DECISION (`[non-spec.10.fix-design-G1.1]`): the start-versus-reclaim rollback test is ONE two-row table-driven case, rows `StartSession` and `Resume`,** with the assertion set stated once. The no-span fact's one home is CODE-2's call-site scope.
- **§5.2's `**Slot cleanup:**` bullet is concurrency-scoped** under a `maxConcurrentSessions > 1` heading, so a rule holding at either concurrency belongs in the `**Scrub model.**` paragraph.
- **§6.2's per-slot fence is two blocks, and the split is load-bearing.** A tier-11 gate pins it by edge presence and block membership only, so `(see §5.2)` pointers and the new `receiving_uploads ──→ slot_cleanup` edge in the general block are safe.
- **The per-slot sub-state machine is a contract model.** Production calls only `MarkLeaked`, `MarkReleased` and `ForgetPod`, so CODE-3 adds one edge.
- **`SlotClaimer.ReleaseSlot(leaked=true)` returns early,** and gateway-side occupancy is the gateway's own `active_slots` counter, decremented on every bind-failure path, so a standing adapter entry does not pin occupancy.
- **The leak disposition is keyed on the gateway's acknowledgement.** `leaked = err != nil || !cleanly` on every bind path; the ledger is replica-local.
- **`UnhealthyThreshold` is `(maxConcurrent+1)/2`, which is 1 at concurrency 1 AND at 2.** It is clamped at 1, the leak count is persistent, and the failure count ages out of a five-minute window. One windowed failure drains the pod at concurrency 2.
- **The occupancy-zero concurrent release sends TWO RPCs,** so the whole-pod scrub must run on every outcome, `absent` included (`[prune.4.fix.1]` closes this with one clause in SPEC-1's §4.1 sentence).
- **DECISION: `accountSlotFailure` gains a THIRD caller** (`resumeOnPod`'s failure branch), the pre-`running` leak's only route to the §5.2 trigger. Tier 5 is not owed: `DrainSandbox` is an existing path covered at tier 2 (`[non-spec.14.review-applicability.1]`).
- **DECISION: §7.1's obligation window ends when the attempt SUCCEEDS.** §7.1 states no report rule and no deadline, and keeps only the consequence "A compensation therefore always names one."
- **DECISION: the disposition table's runtime-given `Shutdown` rows are QUANTIFIED over the act set, never keyed on a named act.** Row 3 reads "The runtime close succeeds and any other act fails". Rejected: a fourth row; re-keying on `closeErr`/`treeErr`.
- **The table's outside-`Shutdown` rows are keyed "an act fails AFTER the deregistration", and that clause is load-bearing.** Only the SDK demotion closes first, which is why it keeps its own row.
- **DECISION (`[prune.3.fix.1]`): the residue column is DELETED and the residue is ONE post-table sentence,** which also names the runtime close as an act. Round 20 deleted "as a reclaim whose act fails does" from it.
- **DECISION: DOCS-2 makes FOUR edits.** The `Shutdown` row, the `DemoteSDK` row, the `ReportSessionScrub` row and one bind-attempt paragraph. No count is written into the proposal.
- **DECISION: §15.4's `**Bind attempt token contract:**` block is TWO sentences, a pointer at §4.7.1 and a request-quantified conformance criterion.** §15.4 carries two blocks. The hold block states only rule 2's `ABORTED` status; the token block is a pointer at §4.7.1 plus a request-quantified criterion with no domain sentence (`[redesign.2.fix.1]`). Round 12 deleted the narrowing "to a request that carries it".
- **DECISION: rule 7 is one sentence, "Any other request is admitted."** The no-overwrite clause is carried by the stamp-once paragraph.
- **DECISION: §6.2's projection prose surrenders THREE clauses to one pointer at §4.6.1,** and §4.6.1's untouched first bullet absorbs the no-claim clause. Reducing only two leaves §6.2 answering `idle` where §4.6.1 answers `draining`.
- **DECISION: "The whole-pod scrub ends state only on a pod that reaches one." is DELETED from the post-table paragraph.** The replacement trigger and the whole-pod scrub are different mechanisms.
- **DECISION: SPEC-3 re-keys §12.6's WRITE trigger onto the cleanup-outcome report and reduces its READ clause to a pointer at §5.2's `**Session count limit:**` bullet,** in both carriers. The write clause stays unreduced because `podStateGatewayWrittenSentence` pins it for REG-PODSTATE.
- **Two shipped tier-11 gates go red on SPEC-3's §12.6 edits, and they are the only two.** `TestPerReleaseSessionCountDrainAgrees_F5231` (delete its spec/12 block; S4 also removes the `12.1` spec-map row and the orphaned §12 credits) and `podStateGatewayWrittenSentence` (re-key; keep the trailing `ReportPodScrub` clause).
- **DECISION: SCHEMA-1 absorbs the two proto report-trigger sentences,** `schemas/lenny-adapter.proto:308-310` and `:451-452`. The `:310` sentence boundary falls mid-line, so SCHEMA-1 quotes whole physical lines.
- **CORRECTION: `st.sessionID` has TWO setters,** so the shipped `Shutdown` takes `bound == true` for an `AssignCredentials`-bound slot that never started; the staged re-gating removes that arm.
- **§28.4's claim register is the file `tests/claim-map.json`.** Generator output under a tier-0 byte-identity gate; the validator does not check that `surface` paths exist.
- **DECISION (`[non-spec.14.fix-G2.1]`): SCHEMA-1's two `WIRED` claim-register rows land at S9 and the `ABSENT` row with CONF-1 at S22,** because its `surface` names files S22 creates. SCHEMA-1's `**Claim register.**` paragraph states it once.
- **DECISION (`[non-spec.16.fix-G4.1]`): the `## Testing` preamble in non-spec-changes.md is the single home of the step conventions** (tiers 0 and 1, spec-map registration, `// spec:` and `// diagnosis:`, claim-map seeding through `EXPLICIT` regenerated in the same commit). The checklist preamble cites it.
- **The only spec site naming `ShutdownRequest` or `ShutdownResponse` is the §4.1 paragraph SPEC-1 edits,** and no spec file enumerates adapter `ErrorCode` values, so the new fields and codes have one spec carrier.
- **DECISION (prune 1): contract-level accepted failure modes live in `spec-changes.md`'s Edge-cases section only.** The non-spec Edge-cases section carries code cases; the spec file's section carries accepted failure modes only (`[prune.4.fix.1]`).
- **DECISION (`[f7.cleanup]`): the summary's pointer at the Edge-cases section and its two priced residues are the last two bullets of `**Watch out for.**`.** No separate `**Accepted failure modes.**` part exists, and the two residues appear nowhere else.
- **DECISION (`[operator.48-49]`, decision 49): proposal files are outside rule N8,** as `scripts/specshift/scope/scope.go` already treats them. Staged blocks still locate targets by heading and quoted text.
- **DECISION (prune 4): the "does a `Shutdown` that removes nothing still run the whole-pod scrub" family is CLOSED** by the §4.1 clause: the scrub runs on a request that passed the teardown-pairing rule, whatever it answers.
- **DECISION (`[non-spec.3.fix-G1.1]`): the refused-`Shutdown` no-scrub rule is pinned by EXTENDING the four-row two-field-precondition tier-1 case** with a `RecycleScrub` disposition and one no-scrub assertion. `TestShutdownTerminatePathRunsNoScrub_spec_4_7` is the precedent.
- **A `Shutdown` answering `superseded` WHILE carrying a recycle disposition is UNREACHABLE,** because both recycle senders go through `Client.ShutdownRecycle`. Both senders arm the §5.2 missing-report timeout first, so a refused recycle `Shutdown` retires the pod.
- **§16.1's house style is a mechanism description plus a `see §X` pointer,** so the `superseded` gloss in the SPEC-6 row is not a single-source finding (precedent `lenny_warmpool_reserved_pods`).
- **DECISION: every carrier of the withdrawn "the adapter reports on every session release" universal has ONE home, the carrier table in SPEC-3's §5.2 commentary.** Built from one grep over `spec/`, `docs/`, `schemas/`, `pkg/`, `migrations/` and `tests/`. `adapter-contract.md:81` is a table row; `:75` is DOCS-2's `Shutdown` row and is not.
- **DECISION: DOCS-4 exists and is exactly two page edits,** `execution-modes.md:68` and `security-principles.md:33`. Its tiers are 0 and 11 (`[non-spec.13.fix-G3.1]`).
- **The withdrawn "reports its outcome to the gateway" universal has exactly FOUR reader-facing carriers tree-wide and all four are staged.** `lifecycle.md:390` is the whole-pod scrub, and `multi-tenancy.md:72` states the cleanup without the reporting clause.
- **DECISION (round 16): the §5.2 hold paragraph loses its SDK-demotion tail;** the demotion instance lives in the DOCS-2 `DemoteSDK` row, conditioned "Where that cleanup runs and completes".
- **`ReportSessionScrub` is AT-MOST-ONCE on the wire, and the compensating `Shutdown` is IDEMPOTENT under replay** (a second copy meets no entry and rule 11 answers `absent`).
- **`sessions_served` feeds two capacity controls and both survive the biconditional,** and `lenny_pod_session_reuse_count` has NO production caller, so conditioning the report cannot perturb `mode_factor`.
- **`lenny_adapter_leaked_slots` is registered and emitted by the GATEWAY,** from `applySlotRetryPolicy` alone, labeled `pod_id` and `pool`, while spec/06:160 and spec/05:545 attribute it to the adapter. The attribution is a recorded unstaged defect.
- **DECISION (`[operator.20-41-45]`, decision 45): `lenny_adapter_leaked_slots` takes a §16.1 row under SPEC-6 and a `catalog.go` entry, a `spec161Metrics` entry and a `docs/reference/metrics.md` row under CODE-9.** This proposal widens which failures reach the gauge. Rejected: recording the gap as unstaged.
- **DECISION (`[spec.28.fix-G1.1]`): the gauge's `pod_id` is documented in-row as a local-only extension of §16.1.1's `k8s.pod.name`.** Rejected: renaming to `k8s_pod_name` (decision 45 and the shipped registration fix `pod_id`); a §16.1.1 note.
- **DECISION (`[operator.48-49]`, decision 48): `lenny_slot_shutdown_untokened_entry_total` carries no label.** CODE-9 registers it with `mustCounter`; the accessor is the package-level `incSlotShutdownUntokenedEntry()` beside `incSetTracingContextDropped`, and CODE-1 calls it with no argument (`CORRECTS` the `(podID string)` form).
- **The §16.1 counters' labels (`CORRECTS` by `[operator.post-r16]`).** The superseded counter carries `pool`, `k8s_pod_name` and the two-valued `cause`, per-pod cardinality; the untokened counter carries none.
- **The `SlotReclaim` hook and `IncSlotCompensationSuperseded` take `(outcome, cause, pool, podName)`,** and the tier-1 gatewaymetrics case passes all four (`CORRECTS` the three-argument reading).
- **`compensationCause` lives in CODE-4 (S19) rather than CODE-9 (S10),** because it reads CODE-7's sentinels and S10 lands before S11; CODE-9's forwarder takes the cause as a string.
- **The adapter metric gate runs CODE → catalogs, never catalogs → code.** `adapter_metric_catalog_test.go` admits an adapter metric only when it reaches both `metrics.md` and §16.1; `spec161Metrics` is a hand-maintained list. SPEC-6 must land before CODE-9.
- **`docs/reference/metrics.md` is AUTHORED, and its `## Adapter metrics` table has five columns** (`Metric | Type | Labels | Description | Used by`). `observability.md` is a curated operator subset and owes no rows (decision 44).
- **The new-identifier sweep is ONE command and returns nothing:** `bind_attempt`, `unconditional_teardown`, `slot_reclaim`, both `SLOT_BIND_*` codes and both new counters have zero pre-existing carriers in `spec/`, `docs/`, `schemas/`, `charts/` and `migrations/`.
- **No non-proto wire schema, SDK or OpenAPI surface carries anything this proposal adds or retires.** `openapi.json` enumerates no REST error code, and `schemaassert.go` builds the conformance catalog by regex with no count pin.
- **The §7.4 mid-session upload path funnels every adapter error into `502 UPSTREAM_ERROR`,** and its admission guard is tenant-scoped, so the hold and rule-3 refusals owe no catalog or OpenAPI change.
- **The exclusive-path `SandboxClaim` strand CANNOT last,** because the WarmPoolController's orphan-claim GC drains the pod after `--claim-orphan-timeout`. A typed refusal means another attempt or a live session owns the pod.
- **The reclaim-hold refusal cannot drain a healthy pod from `Binder.Prepare` or `Binder.Launch` on the exclusive path** (`[prune.5.fix.1]`). It reaches `failPhase` only on a pod that is already draining or that carries a failed cleanup's residue.
- **DECISION (`[non-spec.10.fix-G2.1]`): CODE-8's closing paragraph is a pointer at the accepted failure mode "A pod whose drain failed keeps a dead attempt's token".** The residue is time-bounded by the orphan-claim GC; the shipped `failPhase` log-and-continue is a defects row (`[f6.apply.drain-failure-residue]`).
- **DECISION (`[non-spec.11.fix-G1.1]`): the per-slot cleanup budget `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` is CITED and never instantiated in the proposal.** Worked numbers were wrong twice.
- **DECISION (`[f6.apply.compensation-queue-hold]`): the compensation holding a `queue`-pool's FIFO head is accepted, and the shipped wait bound that ignores pre-admission time is a defects row.** Open decision 46 is answered.
- **The §4.9-timer-before-tree-removal ordering is UNIFORM across every release path and predates this proposal** (decision 42, a defects row). `RotateCredentials` and `ExtendCredentialLease` cannot re-create a removed credential file.
- **DECISION (open decision 38, RESOLVED as staged): a cleanup whose runtime close succeeds and whose later act fails owes NO `leaked` signal.** Recorded as an unstaged-defects row; the credential-reachability companion stands in `### Open`.
- **DECISION (open decisions 35, 39 and 43, RESOLVED as staged).** 35: no scrub-ordering statement is owed, since `answerShutdown` starts the scrub after the removing arm's acts return. 39: the emptied-workspace outcome stays proposal-only. 43: coordinator preemption needs no second unsent-reclaim trigger.
- **The open decisions left with the human are 29, 30, 32, 33, 34, 36 and 50.** 20, 41, 45, 47, 48 and 49 are answered by operator blocks; 28, 31, 40 and 46 by the staging.
- **DECISION: CODE-3 is the one home of the retired `leaked` trigger's retirement across every Go carrier.** The command is `grep -rn "cleanup tim" pkg/ --include=*.go | grep -v _test.go`; no `tests/` gate pins the rewritten strings.
- **CODE-1 contains NO markdown table.** The cascade is `shutdownReclaimOutcome`, whose five arms map onto rules 11-14, with rule 10 the preceding four-row precondition.
- **The staged handler has TWO returns that bypass `answerShutdown`.** The enumeration has one home, the `answerShutdown` rationale paragraph.
- **DECISION (`[non-spec.1.fix-G5.1]`): the `removeSlotTreeFn` / `removeSlotTreeVia` tree-removal seam is homed in CODE-6 and lands at S14.** `removeSlotTree` has exactly three production call sites and all use the seam; homing it in CODE-1 forces an S14↔S17 cycle.
- **`releaseSessionSlot` closes NO runtime on any production call site,** and its call sites are seven production, seven in `resume.go` moving to the under-guard form, and two internal test sites.
- **DECISION (`[non-spec.1.fix-G2.1]`): DOCS-2's `DemoteSDK` row is conditional** ("the pod holds if it holds one, whichever session holds it") and opens its second sentence "Where that cleanup runs and completes,". No tier-11 gate reads the row.
- **DECISION (`[non-spec.1.fix-G4.1]`): CODE-9's gateway collector and accessor are pinned by extending `TestCredentialAndLLMProxyAndSlotMetricsEmit` and `TestNewMetricsEmittersNilSafe` in place.** The case already carries its `// spec:` annotation.
- **DECISION (`[redesign.7.fix.1]`): the comment-carrier reduction is ONE mechanism, under `### Comment-carrier reduction: shared invariants`, placed directly before CODE-10.** The block states the invariants, the general non-carrier arm, the routing, the closure rule and the one-time sweep record; CODE-10, CODE-11 and CODE-12 carry only a definition, a command or site list, and an arm rule. Rejected: merging the ids.
- **DECISION (`[prune.5.fix.1]`): CODE-10 and CODE-12 are PREDICATE-DEFINED and enumerate no carrier files; CODE-11 names four sites by design.** A finding shaped "file X is missing from CODE-10" is filed against a list that no longer exists.
- **CODE-10's grep has SEVEN alternations, the seventh `reports its outcome to the gateway` (`[non-spec.8.fix-G1.1]`).** It surfaces the one wrapped carrier in `basic_level_echo_stamp_doc_reconciliation_test.go`, whose `// diagnosis:` tail is the same carrier, so no second unsurfaced wrap exists (`CORRECTS` by `[non-spec.10.review-mechanism.1]`).
- **DECISION (`[non-spec.1.fix-G1.1]`): CODE-10's arm 1 is an OUTCOME TEST rather than a syntactic deletion,** and the report predicate is quoted from SPEC-3's §5.2 replacement rather than pointed at, because the no-pointer invariant is the shared block's.
- **The general NON-CARRIER arm, stated once in the shared block, is what disposes of every adapter-side and `reserved → idle` hit.** Only a hit that states the retired proposition and fits no arm goes to `deviations.md`.
- **DECISION (`[non-spec.7.fix-G2.1]`): CODE-12 is predicate-defined at checklist S26, covering the §4.6.1 claim-deletion comment carriers** through a joined-comment perl command. Tiers 0 and 11. `Binder.drain` and `podclaim.DeleteClaim` carry the same restatement and move together.
- **CODE-11 is exactly four sites, each cut to a citation of the §15.1 row,** at checklist S25; `errorclassify.go`'s retired-form `// spec: 15:1105` citations are a defects row and CODE-11 neither converts nor adds one.
- **DECISION (`[non-spec.7.fix-G3.1]`): the token-carriage sites all CITE §4.7.1's carriage table and none enumerates or counts the requests.**
- **DECISION (`[non-spec.5.fix-G2.1]`): CONF-1's rule 6 case asserts "the status, the `ErrorCode` and the category that rule states",** matching rule 5; `CATEGORY_UNSPECIFIED` is what makes an unasserted category silently non-conforming.
- **The whole KUBERNETES surface of the non-spec staging is FOUR sites.** CODE-8's refusal arm leaving the `SandboxClaim`, CODE-5's `Unhealthy → DrainSandbox` tail, the tier-2 envtest pair in `binder_envtest_test.go`, and DOCS-1's pod-state paragraph. `binder_test.go` already starts envtest in that package.
- **The derived slot-inventory rules are three,** `slotSubjectFileRE`, `slotSurfaceCallRE` over file content, and a `TEST-GAPS.md` rule for `tests/tier11_docs/`. A blanket files-touched rule enters every new matching file; `bindattempt_test.go` matches neither regex.
- **`slotFailureWorkspaceFinalize` is safe because the STAGE argument stays `slotFailureWorkspacePrep`,** and no surface enumerates `lenny_slot_failure_total`'s `error_type` values.
- **`Server.slotGuards` is the ONE net-new never-pruned adapter structure, deliberately,** because deleting an entry destroys mutual exclusion for a holder. An entry is on the order of 200 bytes.
- **`recycle.maxSessionsPerPod` exists only when `recycle.enabled: true`,** so it is not a bound on a `maxConcurrentSessions > 1` non-recycling pool; that is what makes the `slotGuards` bound claim false there.
- **The `ensureSlotStateLocked` switch implements rules 3 through 7 with no predicate drift,** and the two pairing predicates are equivalent to rules 1 and 10 by truth table although spelled differently.
- **Every admission RPC meets the hold at exactly one of two test points.** `PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup` and `Resume` take `acquireSlotGuardForResolve`; `AssignCredentials`, `StartSession` and `ConfigureWorkspace` meet it inside `ensureSlotStateLocked`.
- **The S-2 covered handler files the proposal opens are `session.go`, `credentials.go`, `slotcreds.go` and `sdkwarm.go`** (`[non-spec.13.fix-G4.1]`), named without a count at every site.
- **Checklist coverage map, unchanged:** S1=SPEC-5, S2=SPEC-1, S3=SPEC-2, S4=SPEC-3, S5=SPEC-4, S6=SPEC-6, S7=DOCS-1/2/3, S8=CODE-3, S9=SCHEMA-1, S10=CODE-9, S11=CODE-7, S12=CODE-8, S13/S19=CODE-4, S14/S15=CODE-6, S16/S17=CODE-1, S18=CODE-2, S20=CODE-5, S21=CODE-6+CODE-1, S22=CONF-1, S23=DOCS-4, S24-S26=CODE-10..12.
- **DECISION (`[index-reconcile.4]`): each code step's `Depends on:` names the spec steps staging what it implements,** and S22 names S18. The deliverable index lists SPEC-1..6, CODE-1..12, CONF-1, SCHEMA-1 and DOCS-1..4 once each.
- **DECISION (`[non-spec.16.fix-G2.1]`): S15 lands the guard and the disposition at `releaseSessionSlot` and `terminateHeldSession`; S16 lands `Shutdown`'s removing-arm guard and its tier-7a cases.**
- **The single-source inventory has not moved since round 11:** rules 1-15, the critical section, stamp-once, the carriage table, the disposition table and residue sentence, the reclaim-hold paragraph, the report biconditional, §7.1's obligation, §15.4's two blocks, the §15.1 mapping, and §4.6.1's projection. Do not rebuild it.
- **The whole citation sweep is THREE cheap commands.** Extract fenced blocks and count against `glob('spec/*.md')`; `[ -e ]` over backticked paths; slug `^#` headings against link fragments. Resolve bare basenames under `pkg/adapter/` first.
- **The sentence-repeat sweeps return only benign repeats:** verbatim/replacement pairs, spec-row/DOCS-2-mirror pairs, the two grep commands, and summary-index/checklist-step pairs. Scopes differ between runs, so counts differ.
- **This log has NO top-level `## Retired`.** `## Ledger` is the file's last section, so an entry appended at the end of the file lands in the ledger.
- **`summary.md`'s section offsets move with every prune, so locate its sections by heading text.** The listed sections and their order are stable; line numbers drift with every prune.
- **The Design's stated direction is that the report UNDER-approximates toward not counting while the gateway-side `leaked` reading OVER-approximates.** A finding that one side books the wrong disposition meets a recorded choice.
- **The staging adds NO per-session or per-request write to etcd, Postgres or Redis and net-REDUCES Postgres writes.** It puts no slow act under the registry lock.
- **The `**Slot-identifier reclaim hold.**` paragraph's close-bounding sentence is EXHAUSTIVE, which is not obvious.** The demotion closes before it deregisters and the failed-start handler calls no `Runtime.Close`.
- **`ShutdownRequest` carries `coordination_generation` but `Server.Shutdown` performs NO generation check.** The staged §4.7.1 "validated on the RPCs that carry it" clause restates spec/10 and the tree gap predates 0081.
- **The five docs-mirroring sweeps and their exact commands, so no docs lens re-derives them.** Each is one command (reporting universal, projection clauses, `SETUP_COMMAND_FAILED`, `sessions_served` write trigger, gRPC `Shutdown`), and each returns only staged or non-carrier sites.
- **`docs/about/style-guide.md` REQUIRES absolute GitHub spec URLs carrying the section anchor,** so DOCS-2's two links are sanctioned; `markdown-link-check` does not validate them.
- **DOCS-3 fully mirrors SPEC-5's four `SETUP_COMMAND_FAILED` replacements, and its fallback names the complete retryable-code set** (`SESSION_CREATION_FAILED`, `STARTING_FAILED`, `RESUME_FAILED`).

### Traps

- **A residue-disposition cell is changed in the §5.2 table and NOWHERE ELSE, and atomicity in `**The
  registry critical section.**` and nowhere else.** An uncovered case is ONE TABLE ROW. A finding about a
  cleanup's residue is answered by the one post-table sentence or not at all.
- **MISTAKE, FIVE edit passes in four rounds on ONE disposition-table column, the residue column, and the
  answer that ended it was deletion (`[prune.3.fix.1]`).** Any future text that decides PER ROW what a
  cleanup leaves or what ends it is the sixth occurrence.
- **The §5.2 disposition table's row keys are performer-scoped, so read the KEY COLUMN before concluding a
  case is covered.** Never answer a key-column gap with prose at a citing site.
- **The staged §4.7.1 block governs itself.** Its "refers to a rule by its number and name" clause is the
  cheapest handle on a duplication finding. Never take a "stated once here" sentence at face value: grep
  its distinctive clause across the whole directory.
- **The pre-`running` boundary was rewritten WHOLE in `[redesign.6.fix.1]` after seven facet-at-a-time
  rounds; do not reopen it a facet at a time.** A finding on this boundary is answered by the home sentence.
- **MISTAKE, THREE rounds choosing an outcome predicate for the `slot_cleanup ──→ released` fence
  annotation.** Both `slot_cleanup` fence entries are bare `(see §5.2)` pointers; put no trigger back.
- **MISTAKE, FOUR attempts at ONE §15.4 paragraph, each answering the last one's defect.** Fix §15.4 only
  as a whole, from the settled two-sentence form. §15.4 carries no rule number, condition, selection
  predicate or domain, and a hand edit that shortens a pointer there can narrow its domain silently.
- **Do NOT enumerate the cleanup's acts anywhere but §5.2's `**Slot cleanup:**` bullet.** A second list
  goes stale. The failed-cleanup residue is not all on disk.
- **Do NOT unify the two pre-`running` cleanup PERFORMERS, and write no completion guarantee into the
  `DemoteSDK` row or the reporting rule.** The failed-start handler reaches no `leaked` state by design.
- **MISTAKE, the most expensive of its window: re-keying CODE-1's cleanup-outcome report onto
  `errors.Join(closeErr, treeErr)`.** One failed `os.RemoveAll` at an ordinary session end books a
  never-pruned leak and retires the pod. Do not re-key table row 3 so that any failing act enters `leaked`.
- **`leaked` is never a free adjective for a cleanup outcome in this codebase.** It means retained
  occupancy. A surviving `credentials.json` made `leaked` pins occupancy and blocks the scrub that collects it.
- **The hold and `leaked` occupancy stay separate objects, and the hold never ends at the cleanup
  TIMEOUT.** Ending it there readmits a successor onto a tree `os.RemoveAll` is deleting.
- **Do NOT flip the `Shutdown` compare so a named reclaim removes an untokened entry.** It may be a live
  successor's `StartSession` entry. Every spelling of the `superseded` gloss (rules 13 and 15, the §16.1
  row, the docs mirror) moves in one edit.
- **The remedy for an accepted-residue finding is ONE SENTENCE, never a request to build remediation
  position 2** (durable pending-reclaim record, reaper, tree generation). The gateway-crash and
  self-recreated-entry residues sit in the spec file's Edge-cases section and have been refuted four times.
- **MISTAKE: widening one clause of the two-clause claim-deletion partition gave one input two answers;
  both clauses move together.** "SPEC-4 introduces pod churn" and "SPEC-4 removes the one-session-only
  control" are both refuted.
- **The zero-value hazard of `mid_session` has two faces, and rule 9 (first frame) exists for both.** A
  per-frame predicate refuses every multi-chunk §7.4 upload. Only `PrepareWorkspaceRequest` gains the field.
- **"Carries `bind_attempt`" means the message TYPE declares the field, never that the value is non-empty.**
  That reading excludes `StartSession` and `ConfigureWorkspace` from rule 1 with no carve-out.
- **WATCHOUT: `Server.reclaiming` and `Server.slotGuards` are never seeded, and `New` initialises no map.**
  A nil-map write panics and ends the pod; seed both in `New` AND lazily, since tests build `Server` by
  literal. The `release` closure `reclaimSlotLocked` returns must take `s.mu` itself.
- **MISTAKE, three rounds on the per-slot guard's scope written as an RPC enumeration.** It is a predicate
  plus a derivation table. On expiry a destructive section proceeds unguarded and an admission RPC is refused.
- **WATCHOUT: the expired-acquisition disposition in CODE-6 is the ONLY home of what a removing site does
  on expiry.** A site that describes the fall-through in its own words is restored to a citation. Do not
  re-add hold or report columns or a three-predicate sentence to that block, and keep the sentence that
  defines `guarded`, which three site predicates cite.
- **WATCHOUT: CODE-1's removing arm proceeds destructively when `lockSlotGuard` fails, and that is not a
  bypassable gate.** Removing unguarded is shipped behaviour; the decision is re-made under `s.mu`, and
  since decision 47 the hold is RETAINED and the pre-`running` clean exit is false.
- **WATCHOUT: `DemoteSDK` names two different things.** `SDKWarmRuntime.DemoteSDK` is what CODE-2's
  refusal arm calls; `Server.DemoteSDK` deregisters through `releaseSessionSlot` and must not be used there.
- **WATCHOUT: `ShutdownReclaim` must NOT route through `Client.shutdown`.** That builder sets
  `unconditional_teardown`, so a reclaim built through it would tear down the successor.
- **Do NOT reintroduce a compensation suppression on either typed refusal (`[operator.post-r16]`).** The
  "refusal guard" phrases in CODE-4's `Binder.Prepare` paragraph and in CODE-8 are CODE-8's reclaim-closure
  guard and stay. CODE-7's sentinels are read by CODE-8, `compensationCause` and CODE-5's classifier arm.
- **Do NOT put the credential-lease release back inside `compensateFailedSlotBind`,** and do not condition
  it on `assignSlotCredentials` succeeding: it mints per provider and returns on the first error.
- **MISTAKE: CODE-2's rollback answering `codes.FailedPrecondition`.** The answer is `ABORTED`. Do not make
  the superseded refusal permanent, and do not open an `Aborted` case in `SlotBindError.Reason()`.
- **Do NOT "simplify" the compensation's split bound to a single one.** `contextWithGraceDeadline` returns
  the parent for a non-positive grace, so `deadlineMs: 0` makes a completed reclaim unreportable as clean.
- **Do NOT simplify CODE-1's predicates into fewer, and do not re-gate the report on `st.started`.** That
  reintroduces the double report through CODE-2's rollback; the tier-1 "Claimed but not yet recorded" case
  exists to fail on it.
- **MISTAKE, four occurrences: the bound/started drift.** `bound ⊋ started`. Check every sentence naming
  the runtime teardown against "a session whose start the adapter has admitted", and any sentence naming
  `StartSession` alone against `Resume` and SDK-warm `ConfigureWorkspace`.
- **MISTAKE, four occurrences: the unscoped `leaked` terminal.** The `**Scrub model.**` paragraph holds on
  a pod of either concurrency, so any appended sentence naming `leaked` needs its own concurrency scope.
- **TRIED AND WITHDRAWN: the no-retry rule, and five alternatives to the per-attempt epoch.** Never add
  adoption, re-stamping or a response-carried fence; stamp-once first-writer-wins replaces order.
- **The §5.2 append can silently redirect a tier-11 gate.** `lineContaining` returns the FIRST match; do
  not recase "whole-pod replacement trigger". Three gates resolve §4.7 rows by first match, so the §4.7.1
  block stays below the RPC tables and every §4.7 row stays on one physical line.
- **Do NOT delete the `ReportSessionScrub` mention from the `adapter-contract.md` `Shutdown` row.** A gate
  needs four substrings on one line. DOCS-2 cites no spec section numbers, and `:393` is no licence.
- **MISTAKE, twice: a design reversal leaves behind every sentence a grep on the new vocabulary misses.**
  Judge a fix by grepping the staged blocks for the claim, never only the commentary, and grep the
  checklist and summary too. After deleting an artifact, grep for every noun that named it.
- **The glob `*spec-changes.md` matches BOTH `.spec-changes.md` and `.non-spec-changes.md`.** Use full
  filenames. A one-line grep misses a phrase wrapped across comment lines. Correct a ledger claim with
  `CORRECTS`, never in place.
- **MISTAKE nearly filed many times: the "on a session release" family outside §12.6.** spec/05:488,
  spec/06:139, spec/16:12 and `state-machines.md:248` are existential; only §12.6's universal needed the re-key.
- **MISTAKE nearly filed four times: a disposition-table row for a RUNTIME-GIVEN slot no cleanup reclaims.**
  The running orphan is disposed of by rule 13 and its own Edge-cases bullet.
- **Do NOT move the §7.2 reclaim after step 4, and do NOT re-word §7.1 over step 3's summary clause.**
  Reordered, every mid-resume reclaim fails the generation check.
- **The DOCS-2 `DemoteSDK` row is the one LICENSED DOCS RESTATEMENT, and `state-machines.md:235` is the
  licensed restatement of the `running` boundary.** Re-check the row whenever a §5.2 hold sentence or rule
  4 changes; no gate reads it. Do not turn either into a pointer.
- **Do NOT "fix" the §7.1 paragraph's unrendered links by moving it out of spec/07's code fence.**
- **Recurring non-findings, each refuted at least twice:** §4.1's commentary on the carriage table's
  `unconditional_teardown` cell; the SPEC-4 fence edit's replacement-only anchor form; `spec/05:555`'s "new
  slot"; §12.6's "Interface Design" heading; the §15.1 endpoint rows and `spec/07:208`; the §15.1 row's
  "Surfaced wherever" sentence; `configuration.md:91`; `troubleshooting.md:41` (a warm-up `error_type`).
- **More recurring non-findings:** `adapter-contract.md:84`; `spec/28:140`; the `terminate`/`shutdown`
  frame names; `usage_test.go:233`; `exclusiveBindRequest`'s stale comment; the tier-2 heading label;
  two unsuffixed test-name anchors; `SessionScrubOutcome`'s enum header (`:436-437`, the cleanup universal).
- **WATCHOUT: `completed` is POD-SIDE ONLY.** A site meaning the adapter answered clean writes "acknowledged
  clean"; "unacknowledged" alone means unanswered.
- **WATCHOUT: "teardown" carries two referents in the §4.7 `Shutdown` row and DOCS-2's mirror.** "Which
  teardown it asks for" names the REQUEST FORM. Below the bar unless a rule goes wrong.
- **WATCHOUT: rule 12's "whatever entry the adapter holds" scans as pod-wide and is session-scoped.**
- **WATCHOUT: the permanent hold after a failed cleanup outside a `Shutdown` is a standing accepted
  residue,** routed to remediation position 2's reaper. Do not spend two verifiers on it.
- **The summary's two priced residues (last bullets of `**Watch out for.**`) read like accepted failure
  modes missing from the Edge-cases section.** They are there by prune 4 and `[f7.cleanup]`; do not re-home them.
- **MISTAKE: a fix pass rewrote a block WITHOUT adding the item the finding named, and a DEFERRED naming
  two sites was closed on its first half.** Diff the block against the finding's own words, and land a
  DEFERRED whose site is in a file the current loop may edit rather than re-defer it.
- **WATCHOUT: the bind-field sweep's grep MUST carry the `adapterv1.` qualifier,** and
  `shutdown_drain_gate_race_test.go` and `concurrent_delegation_proxy_test.go` are hit by both sweeps.
- **Do NOT put the reclaim-hold test inside `slotStateLocked`.** The staged `Shutdown` handler resolves
  through it, so the test would refuse the reclaim its own pass opened.
- **TRIED AND WITHDRAWN: a PER-FILE carrier enumeration for CODE-10, and a per-deliverable copy of the
  comment-carrier invariants.** A new comment-reduction deliverable is a fourth sub-block in the same form;
  when a prune replaces an enumeration with a rule, run the rule against every site the enumeration named.
- **WATCHOUT: line-oriented greps MISS wrapped comment carriers silently.** CODE-12's command joins each
  comment block; check an edited command by reading the hits it DROPS. `gatewaylink.go:70-77` carries the
  universal in two fragments, and `podspec.go` has four comments of which three are carriers.
- **WATCHOUT: CODE-10's and CODE-12's hit counts and pattern counts are NOT load-bearing.** Do not cite one.
- **The over-broad "every rule that removes no entry answers a clean exit" is FALSE and must never come
  back.** Rule 10 removes no entry and answers `INVALID_ARGUMENT`. One landing site takes one staged block.
- **WATCHOUT: adding a deliverable is a FIVE-PLACE edit plus the checklist:** the block, the `## Testing`
  line, the files-touched bullet, the summary index bullet, the SPEC-3 carrier table, and a new last step.
- **WATCHOUT: when deleting a restating sentence, read the sentence AFTER it for a dangling demonstrative.**
- **WATCHOUT: "the six requests" is NOT a safe narrowing of the token-carriage sentences,** and "can create
  or resolve the entry" is false as a statement of which requests carry `bind_attempt`.
- **WATCHOUT: the refused-`Shutdown` scrub assertion must kill a `defer` or top-of-handler placement of the
  scrub.** CODE-1's "runs on every outcome, refusals included" scopes to the superseded arms only.
- **Security and capacity families are exhausted.** The bar is that the gateway does something new; every
  in-pod-attacker dress collapses into the Open on the adapter gRPC server's reachability, and the capacity
  dresses are accepted edge cases or the shipped baseline.
- **Do NOT file the premature-removal case on the compensation path as unobservable.** The acquisition
  expires only with the compensation's own deadline, so the gateway has already recorded the error.
- **Gauge labels: `lenny_adapter_leaked_slots` keeps `pod_id` and `pool`; do not "align" it to
  `k8s_pod_name`, and stage no §16.1.1 or §6.2 edit for it.** Do not reintroduce a label on the untokened
  counter, and do not file a spec line citation in a proposal file as an N8 violation.
- **WATCHOUT: adding the same `claim` string to `EXPLICIT` at both S9 and S22 turns tier 0 red** with
  "appears more than once".

### Open

- **Does rule 8's untokened arm compare equal across a replacement?** UNVERIFIED: two untokened entries under one identifier compare "" to "", so `noteRuntimeStarted`'s replaced-entry check would record. No gateway path producing it is known; start from whether a `StartSession` or `ConfigureWorkspace` can be an attempt's FIRST request on a pod.
- **Which client envelope does a rule-6 `FAILED_PRECONDITION` at a non-setup-command bind request reach?** UNVERIFIED: `SlotBindError.Reason()` may render a non-retryable 422 `SLOT_FAILED`.
- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** OPEN, for a human: the same refusal is non-retryable at one stage and retryable at another.
- **Does the process-group kill fall inside the "every act that cleanup owes the slot" completion predicate?** UNVERIFIED: nobody traced where the kill runs or whether its failure is observable.
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** OPEN, summary open decision 36: a third-party harness cannot force a failed removal.
- **Neither newly recorded residue has any observability** OPEN, summary open decision 33.
- **Are "registry entry" and "bound entry" defined anywhere?** OPEN, summary open decision 32.
- **Churn from the third accounting caller** OPEN, summary open decision 34: at `maxConcurrentSessions: 2` a failed §7.3 re-attach drains a fresh pod.
- **Should SPEC-5 also correct `spec/06:290`?** OPEN, summary open decision 30. Pre-existing.
- **Should the tier-3 descriptor gate pin the `SlotReclaimOutcome` enum values?** OPEN, summary open decision 50 (`[index-reconcile.5]`).
- **Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?** OPEN, pre-existing: `slothealth.Tracker` and `slotstate.Registry` are replica-local and in-process, so a restart forgets leaked pods, and this proposal raises the leak rate.
- **Is the adapter's gateway-facing gRPC server reachable from the agent container, and does it require mTLS?** UNVERIFIED, pre-existing: if unauthenticated, an in-pod agent could send a co-tenant's unconditional `Shutdown`.
- **Does the SPEC-6 `lenny_adapter_leaked_slots` row define the series the tree emits?** OPEN, filed by `[spec.28.review-mechanism.1]` and not fixed: the row says "the per-pod count of slots in the `leaked` sub-state, which stay counted until the pod terminates", while the gauge moves only on `applySlotRetryPolicy` and is per replica. `[spec.28.review-reliability.1]` declined it as a restatement of spec/06:160.
- **Does S10 fail tier 0 on `unused` before S16 calls the untokened counter?** OPEN [`[non-spec.14.fix-design-G1.1]`]: `.golangci.yml` enables `unused` with `run.tests: true`, and the registration var and accessor have no reference between S10 and S16. The cheap remedy is an adapter tier-1 collector case at S10.
- **CODE-1 (S16, S17, S21), CODE-4 (S13, S19) and CODE-6 (S14, S15, S21) each span several checklist steps** OPEN [`[index-reconcile.5]`], against the rule that a deliverable appears in one step. Merging rewrites non-spec steps and their order.
- **Tier-4 fixture extension** OPEN: whether the per-case interceptor perturbs the other `recycleAdapterDialer` call sites, and whether `recycle_scrub_path_test.go` can carry the concurrent-slot compensation case, is untraced.
- **Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?** OPEN, recorded in the summary's unstaged-defects rows: a CREATE, `bound` patch and DELETE inside one reconcile window leaves `("", false)` and an idle unscrubbed pod. Correctly scoped out; do not re-file.
- **Does a gateway replica's §10.1.7 preStop drain discharge the §7.1 reclaim obligation, or hand a mid-bind session to another replica?** UNVERIFIED: the Edge-cases bullet covers a crash only.
- **Does the gateway double-book a leak for a runtime-given slot whose close fails?** UNVERIFIED: row 2 gives both a `leaked` report and an unclean response into one tracker with no per-session dedup. Check `MarkLeaked` idempotency.
- **Can a `PrepareWorkspace` frame on an already-admitted call write into a tree `removeSlotTree` is deleting?** UNVERIFIED: turns on whether the handler honours the cancelled server context.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** UNVERIFIED: one leaked slot retires the pod there.
- **Does `s.reclaiming` grow without bound over a long-lived recycling pod?** UNVERIFIED, code lane: one entry per failed cleanup, held for the pod's life, and the adapter process survives pod reuse.
- **Does the widened §15.1 row owe `RunSetup`'s third deterministic `FailedPrecondition` producer?** UNVERIFIED: "adapter is not configured with a workspace root" maps to 422 the same way.
- **Does `docs/reference/adapter-contract.md` owe a mirror of the §4.7.1 numbered-rule block?** UNVERIFIED: DOCS-2 stages four edits only.
- **Is a `credentials.json` left by a failed cleanup on a concurrent pod reachable by a later session through the shared runtime process?** UNVERIFIED, the companion of resolved decision 38; answering it the other way would reverse 38.
- **Does DOCS-1's own-voice trigger cell for the new `receiving_uploads → slot_cleanup` row still agree with the paragraph?** UNVERIFIED.
- **Would any gate newly fail because of text the staging ADDS, rather than text it replaces?** UNVERIFIED: gate breakage was checked only for replaced lines.
- **Can a recycle-carrying `Shutdown` answering `absent` start the whole-pod scrub while a DIFFERENT slot's cleanup is still inside `removeSlotTree`?** UNVERIFIED: the scrub now runs inside `answerShutdown` and takes no slot guard. Outside the arm decision 35 describes.
- **Does §5.2's "still in flight" mean "not yet admitted"?** UNVERIFIED: CODE-6 says an upload already past its resolve is not being admitted. Pre-existing.
- **Does the ten-second provenance paragraph belong in SPEC-3's commentary at all?** OPEN: decision 40 appended sentences to it; the next prune decides.
- **Does rule 8's take-back, stated outside the critical section, let a lagging attempt close a successor's session?** OPEN, below the bar: every reachable ordering has the reclaiming `Shutdown` tear the runtime down first.
- **Do `scrubreporter_seams.go:389-391` and `scrubreporter_seams_test.go:726-727` fall inside CODE-10's predicate?** UNVERIFIED: the application-time grep decides.
- **Is `pkg/adapter/sessionscrub_emit_test.go:16-19` stale against its own terminate case?** UNVERIFIED, pre-existing.
- **Is the POSITIVE arm of the reclaim-hold release at `releaseSessionSlot` pinned by any listed case?** UNVERIFIED: only the retained arm is pinned; the `removeSlotTreeFn` seam is a usable park point.
- **Does an implementor's reflow of the podspec and gatewaylink comments shift two `tests/claim-map.json` line surfaces?** UNVERIFIED: the implementing step re-checks both rows.
- **What retires a continuously occupied `maxConcurrentSessions > 1`, `recycle.enabled: false` pod?** OPEN, FILED: nothing does, which makes the `slotGuards` never-pruned bound claim false there.
- **Does the Testing section owe a disposition for DOCS-2's `DemoteSDK` row?** OPEN, FILED: the DOCS-2 block dispositions three of four edits and no gate reads the row.

### Deferred

- DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start` failure
  branches release by session identifier alone. Carried in the summary's unstaged-defects section.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline lets the
  adapter apply its default grace period" is false. CODE-7 opens the file and stages no repair.
- DEFERRED [schemas/lenny-adapter.proto, `ErrorCode` enum header]: "The catalog below mirrors spec §15.1" is false
  for codes 27, 28 and 29, and SCHEMA-1 stages no replacement.
- DEFERRED [spec-changes.md, commentary wording]: the untokened-entry commentary calls the §10.1 hold-timeout
  termination "a request" where §5.2 says it runs under no request.
- DEFERRED [spec-changes.md, `## Spec files touched`]: the spec/16 bullet says §16.1 gains "one row for each counter",
  which omits the gauge row SPEC-6 now stages. Bookkeeping.
- DEFERRED [pkg/apis/lenny/v1alpha1/sandbox_types.go:113-118]: the `Sandbox.status.phase` doc comment lacks the input
  SPEC-4 adds. Record only; any fix goes through the Go comment plus `make generate`.
- DEFERRED [docs/reference/adapter-contract.md, DOCS-2]: the §15.4 reclaim hold's `ABORTED` refusal has neither a
  sentence nor an exclusion rationale on the page. Either is new DOCS-2 content.
- DEFERRED [docs/api/internal.md]: the gRPC status table omits `ABORTED`. Pre-existing, for a separate finding.
- DEFERRED [schemas/lenny-adapter.proto, the LEAKED comment's drain-ledger sentence]: it may restate a rule whose home
  is §4.6.3 or §5.2. SPEC-3 does not falsify it; a reduction is a finding of its own.
- DEFERRED [non-spec-changes.md, CODE-6 and the §10.1.4 parked-member Edge-cases bullet]: the disposition's claim that an
  expired acquisition is the premature-removal case is false for a pass member whose acquisition expired because another
  member's park spent the pass's single guard-acquisition context, and the bullet's "leaves every remaining member" is too
  wide, since a free guard still acquires. The remedy is a code-lane design choice.

### Retired

Bodies are in the archive file. This hard compaction retired, among others: the Opens closed by `CORRECTS` (S22 on S18,
the claim-register row step, S23 tiers, `binder_envtest_test.go`, `deregisterSlot` callers, `manifest_fields_test.go`, the
`CredentialAssigner` fakes, the `SlotReclaim` hook arity, the untokened accessor, `Binder.shutdownAdapter`, tier 5); the
CODE-10 second-wrap Deferred; and every entry for decisions 20, 41, 45, 47, 48 and 49.

## Ledger


### [non-spec.9.review-edit-sites.1]
DECISION: filed one finding (CODE-10 grep still misses a comment-line-wrapped carrier) — BECAUSE the round-9 diff patched only the one literal string ('reports its outcome to the gateway') that closed the previously-reported wrap-across-break case in tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458, but CODE-10's command is still a plain line-by-line grep (unlike CODE-12's line-joining perl form), so any other carrier whose sentence also wraps a comment-line break stays invisible to it — ALTERNATIVES: considered filing this as "not a finding" (arm-rule/closure text says a carrier found later is closed by re-running the command later, at application time) but that clause presumes the shipped command actually returns every present-day carrier; it does not, so a carrier is silently skipped rather than closed.
FACT: tests/tier4_integration/recycle_scrub_path_test.go:1081-1083 reads "... never at zero. Each\n// release reports the per-slot cleanup outcome through the real handler,\n// which advances sessions_served and drives the per-release retirement." — "Each" ends line 1081 and "release reports ..." starts line 1082, so no single physical line contains "each ... release" or "release reports", and the current CODE-10 grep (verified by running it against the file directly) returns zero hits there. — EVIDENCE: tests/tier4_integration/recycle_scrub_path_test.go:1081-1083
FACT: verified the same one-line grep run against the whole tree still misses this hit even with round 9's added `-e 'reports its outcome to the gateway'` alternative, confirming the round-9 fix closed only the one specific carrier it was written for, not the general wrap-across-line-break class the same finding class (round 8) was about. — EVIDENCE: command run from repo root reproduced in this shard's finding
CORRECTS: none named this round.
USEFUL: none named this round.

### [non-spec.10.fix.1]
DECISION: corrected two defects the round-10 fixer's own rewrite of the **Start-versus-reclaim rollback, deterministic form.** bullet introduced, with the smallest edits that make the parallel statements agree. No round-10 pass subsection existed to append to, so the corrections are recorded here as one ledger entry.
- The rewritten bullet stages two new `ReleaseSlotForTest` call sites in `pkg/adapter/slotsession_test.go`, which falsified the completeness claim in the files-touched test list ("hold every `ReleaseSlotForTest` call site") and left CODE-6's caller enumeration short. Both statements are narrowed rather than re-listed: the five named files now hold every call site the tree holds today, and each carrier names the new rollback case as a caller written against the new signature whose `Resume` row supplies an already-cancelled context rather than `t.Context()`. — EVIDENCE: `pkg/adapter/export_test.go:32-35` is the seam being re-signatured; the five existing call sites are unchanged.
- The bullet introduced a nil-safe `onStart func(sessionID string)` hook on `probeRuntime`, contradicting the fixture paragraph's statement that `Server.removeSlotTreeFn` is the section's one new seam. The extension is now declared in the fixture paragraph in the form its sibling already uses (the SDK-warm double's `onConfigure` hook), and the bullet references it. — EVIDENCE: `pkg/adapter/slotsession_test.go:33-38` carries `onClose` and a hookless `Start`, so the hook is a genuine fixture extension.
CORRECTS: `[non-spec.10]`'s rollback-bullet rewrite, on both counts above. The rewrite itself stands; only its unstated parallels were wrong.

### [non-spec.10.fix-G1.1]

- DECISION: the tier-1 bullet **Start-versus-reclaim rollback, deterministic form.** became ONE table-driven case with a row per non-SDK confirmation site (`StartSession`, `Resume`), assertion set stated once — BECAUSE CODE-2 stages the same rollback body at both sites, so one home for the property list — ALTERNATIVES: a second **The Resume confirmation refuses and rolls back** bullet, rejected because it restates the five properties in a second vocabulary and a later round then has to merge them.
- WATCHOUT: the `Resume` row cannot be driven by a concurrent `Shutdown`. CODE-6's derivation table holds `Resume`'s per-slot guard from ahead of its `claimSessionSlot` to the end of the call, so the `Shutdown`'s removing arm blocks on that guard and the case deadlocks. The row removes through `ReleaseSlotForTest` with an ALREADY-CANCELLED context, which falls through CODE-6's unguarded-removal escape. — EVIDENCE: non-spec-changes.md:1805-1830 (derivation table and the `slot_guard_not_acquired` fall-through); tier-7a section records the same deadlock at :3590-3600.
- FACT: `probeRuntime` (pkg/adapter/slotsession_test.go:33-38) has an `onClose` hook and NO `onStart` hook; `Start` is a bare `return nil`. Any tier-1 case that must mutate the registry between the claim and `noteRuntimeStarted` needs that fixture extension, which the rewritten bullet now names. — EVIDENCE: pkg/adapter/slotsession_test.go:33-55.
- FACT: `releaseSessionSlot` files no `ReportSessionScrub`; it deregisters, removes the tree, and calls `cancelPodMCPIfRuntimeIdle`. The scrub report is the `Shutdown` path's. So "neither the rollback nor the removal files a `ReportSessionScrub`" is assertable in a tier-1 driver that removes through `ReleaseSlotForTest`. — EVIDENCE: pkg/adapter/slotsession.go:214-220.
- FACT: the adapter's `Resume` reaches `Runtime.Start` with no checkpoint transport when the request carries no chunks; `restoreChunks` returns on its empty-set guard. A conversation-only resume is therefore drivable from the `slotPod` fixture as it stands. — EVIDENCE: pkg/adapter/resume.go:41-45,:169-172.
- USEFUL [review-log-archive:5614]: that entry predicted exactly this finding ("a later round may prefer the deterministic case to cover the `Resume` arm as well"). It is now closed by the two-row case.
- DECISION: the tier-7a sentence pointing the `Resume` refusal's deterministic coverage at **The confirmation refuses a replaced entry** was re-pointed at the new `Resume` row — BECAUSE that case asserts only a boolean return of `noteRuntimeStarted` and nothing about the handler arm, so leaving it claimed coverage the row actually provides.
- DECISION: no "no span is recorded" assertion was added to the `Resume` row — BECAUSE the no-span fact's home is CODE-2's call-site scope (non-spec-changes.md:738-740, verified against pkg/adapter/resume.go:25-35), and a row asserting it would restate that statement and need a tracing-exporter fixture to pin a fact no code path can violate.

### [non-spec.10.fix-G2.1]

DECISION: CODE-8's closing paragraph is now a pointer at the accepted failure mode **A pod whose drain failed keeps a dead attempt's token**, carrying no mechanism, no `binder.go:1079-1081` citation, no consequence clause and no reaper sentence — BECAUSE `## Edge cases and accepted failure modes` declares itself the home of code-only cases and three sibling deliverables already point into it in that form — ALTERNATIVES: re-synchronising CODE-8 with the bullet's scoping clause (keeps two stating sites, the state prune-1 was made against); moving the home into CODE-8 (would force three sibling edits and break the section's inventory).
FACT: the §5.2 "Derived Runtime" example sets `cleanupTimeoutSeconds: 30` and sets NO `maxConcurrentSessions`; the field's documented default is 1, so the per-slot cleanup budget for that example is `max(30/1, 5) = 30` seconds — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:166 (example), :399 (default), :545 (formula).
WATCHOUT: the formula divides by `maxConcurrentSessions` and clamps at 5, so raising concurrency SHRINKS the per-slot budget. A worst-case illustration for any hold bounded by this budget is a low-concurrency pool with a large `cleanupTimeoutSeconds`, and picking a higher concurrency understates the bound — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545.
WATCHOUT: the corrected thirty-second cleanup budget in the queue-head bullet now shares a number with the queue's default `maxQueueWaitSeconds` of 30 seconds cited later in the same bullet. They are unrelated quantities and no derivation links them; do not merge them — EVIDENCE: pkg/gateway/sessionserver/queue.go:18.
DEFERRED [spec-changes.md and the proposal corpus convention]: the queue-head bullet cites spec/05 by line number (`:166`, `:399`, `:545`), as four sibling sites in the same file do. Whether channel-naming N8's ban on line citations covers proposal prose is unresolved. Converting one site alone would make it inconsistent with its siblings, so nothing was converted; the question should be decided once for the whole corpus.

### [non-spec.10.fix-design-G1.1]

DECISION: close the missing `Resume` rollback test by turning the existing bullet **Start-versus-reclaim rollback, deterministic form.** (non-spec-changes.md:3152-3155) into ONE two-row table-driven case, rows `StartSession` and `Resume`, with the assertion set stated once — BECAUSE the two arms are the same rollback body (`Runtime.Close`, `cancelPodMCPIfRuntimeIdle`, `Aborted`, deregister nothing, no `ReportSessionScrub`) and a second bullet would restate the rule a second time — ALTERNATIVES: a new standalone `Resume` bullet (rejected: restates the same five properties in a second home, which is the hair this loop is told to avoid); leaving the tier-7a `Resume` arm as the coverage (rejected: that arm asserts the OPPOSITE disposition, a resume that recorded runtime-live membership before the `Shutdown` proceeded, :3606-3613).

DECISION: do NOT add a "no span is recorded" assertion to the `Resume` row, though the finding's suggested fix asks for one — BECAUSE the no-span fact's single home is CODE-2's call-site scope (:738-740, verified: `Server.Resume` opens no span, pkg/adapter/resume.go:25-35), and a test row asserting the absence of a span would need a tracing-exporter fixture to pin a fact no code path can violate.

FACT: the `Resume` confirmation refusal cannot be driven by a concurrent exported `Shutdown`, because CODE-6's derivation table gives `Resume` the per-slot guard "from ahead of its `claimSessionSlot` to the end of the call" (non-spec-changes.md:1714+ table row; the same fact drives the tier-7a `Resume` arm's inverted ordering at :3590-3600). The production-reachable route to a removal during a parked `Resume` is the unguarded escape: a destructive section whose `lockSlotGuard` acquisition outlives its context "performs its removal unguarded and logs a `slot_guard_not_acquired` warning". — EVIDENCE: proposals/0081_*/0081_*.non-spec-changes.md:1740-1775

DECISION: the `Resume` row drives its removal that way, on the calling goroutine, from a hook inside the fake `Runtime.Start`, calling `ReleaseSlotForTest` (pkg/adapter/export_test.go:32, which CODE-6 gives a `context.Context` first parameter) with an ALREADY-CANCELLED context, so the guard acquisition returns immediately and the removal happens unguarded — BECAUSE it is deterministic with no second goroutine and no new seam, and it is the one interleaving production admits. This mirrors the SDK-warm case's determinism argument at :3095-3100. ALTERNATIVES: parking the start on a channel and issuing a `Shutdown` from a driver goroutine (rejected: deadlocks on the guard `Resume` holds, which is exactly what the tier-7a section already records).

WATCHOUT: the tier-7a sentence at :3604-3605 currently points the `Resume` confirmation's deterministic coverage at **The confirmation refuses a replaced entry** (:3156-3158), which asserts a boolean return of `noteRuntimeStarted` and nothing about the handler arm. Any fix that adds the `Resume` row must re-point that sentence at the row, or the file keeps two answers to "where is the `Resume` refusal covered".

UNVERIFIED: whether `pkg/adapter/resume_test.go` (package `adapter_test`) already has a runtime double with a `Start` hook, or whether the two-row case has to live in `session_test.go` / a shared fixture file. The fixer should place the case in the file that already carries the `StartSession` row's fixture and drive `Resume` through the exported RPC from there; the proposal names no file for the rollback bullet today, and it does not need to.

### [non-spec.10.fix-design-G2.1]

DECISION: G2 finding 0 is closed by REDUCTION, not by re-synchronising the two tellings — cut CODE-8's closing paragraph (non-spec-changes.md:2035-2039) to the pointer form its three siblings already use (:1127, :1214, :1379) and leave the `Edge cases and accepted failure modes` bullet (:3867-3871) as the single stating site — BECAUSE that bullet is the declared home for code-level cases (:3817) and already carries the CODE-8 scoping ("this arises only on an ordinary failure whose drain then fails") that CODE-8's own telling lacks. ALTERNATIVES: (a) copy the scoping clause into CODE-8 so the two agree — rejected, that is two homes kept in sync by hand, the exact state prune-1 was made against; (b) delete the Edge-cases bullet and keep CODE-8 as the home — rejected, the section is the declared home and three siblings already point into it.

DECISION: G2 finding 1 is an arithmetic/citation correction inside one sentence (:3894-3896): the §5.2 "Derived Runtime" example sets no `maxConcurrentSessions`, so the per-slot budget is `max(30/1, 5) = 30`, not fifteen. ALTERNATIVES: rewriting the bullet's bound argument — rejected, the bound and its mechanism are right; only the illustration is wrong.

FACT: the §5.2 `#### Derived Runtime` YAML runs spec/05_runtime-registry-and-pool-model.md:120-168; its `sessionPolicy` block ends at `cleanupTimeoutSeconds: 30` (:166) and contains no `maxConcurrentSessions` key. The documented default is `maxConcurrentSessions: 1` (spec/05:399, restated at :393), and the budget formula is at spec/05:545. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:166, :399, :545

WATCHOUT: the budget formula divides by `maxConcurrentSessions` and clamps at 5, so raising concurrency SHRINKS the per-slot budget. A future lens reaching for "the worst case is a high-concurrency pool" has the direction backwards; the worst case for this hold is a low-concurrency pool with a large `cleanupTimeoutSeconds`. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545

WATCHOUT: after the correction the bullet carries two thirty-second figures with different subjects — the compensation budget (`max(30/1,5)`) and the queue's default `maxQueueWaitSeconds` of 30s (`pkg/gateway/sessionserver/queue.go:18`). Keep each figure attached to its own noun; do not let a later round collapse them or read a coincidence as a derivation.

FACT: the corrected figure is the one the archive's original filing used in a different dress ("60/4 = 15s budget"); the fifteen in the current text is that arithmetic's residue applied to a pool that does not carry those values. EVIDENCE: review-log-archive.md:26893

DEFERRED [non-spec-changes.md]: this file carries 5 spec LINE citations (`spec/05_...md:166`, `:440`, `:545`, etc.). channel-naming.md N8 retires spec line citations in favour of heading citations, and its stated domain does not exclude `proposals/` the way N3 does. This round keeps the existing line-citation style so the corrected sentence matches its five neighbours; whether the proposal corpus is inside N8's domain is unresolved. Someone should decide it once for the whole file rather than per sentence.

### [non-spec.10.review-applicability.1]

FACT: the round-9 fix to CODE-10's grep is the ONLY delta since the r8 snapshot (one added
alternative, `reports its outcome to the gateway`). I ran the post-fix command from the repo
root: it adds exactly one hit, `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458`,
the `// diagnosis:` carrier the earlier DEFERRED entry named. That carrier is the
coordinated-clause case CODE-10's arm rule already covers explicitly, so the closure holds.
EVIDENCE: proposals/.../non-spec-changes.md:2160; tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:453-461

FACT: CODE-10's grep also returns `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6,:21`,
which are NOT among "the two tier-11 files under checklist S4's sweep". Those two are
`spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`
(spec-changes.md:608-609), and `grep -rn sessions_served tests/` confirms it. Line 6 is a
genuine CODE-10 carrier under the coordinated-clause arm; line 21 is the `// spec:` gloss on the
cleanup (not the report) and takes the non-carrier arm. No gap.
EVIDENCE: spec-changes.md:608-609; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-7,:20-21

FACT: CODE-12's perl command RUNS and returns 20 files; the proto/enum verbatim anchors
SCHEMA-1 quotes (`schemas/lenny-adapter.proto:311-313,:440-442,:444-447,:308-310,:451-452`)
all match byte-for-byte; the CODE-9 anchors (`binder.go:137`, `slotbinder.go:353-361`,
`metricsbackfill.go:149`, `gatewaymetrics_credential.go:185-193`, `gatewaymetrics.go:1170-1175`)
all resolve; `slotSubjectFileRE` matches the new metric test's name; and S4's §12.1 claim is
exact (one row, and `tests/spec-map-exceptions.yaml:85` covers the empty list, which
`cmd/lenny-test/cmd_validate.go:817` honours). Do not re-derive these.

FACT (the one live defect this pass found): staticcheck's `unused` rule 10.1 marks a whole const
BLOCK used when one member is used
(/home/ec2-user/go/pkg/mod/honnef.co/go/tools@v0.6.1/unused/unused.go:1072-1081). That is the
hinge for the `slotFailureWorkspaceFinalize` finding: the constant is safe if it joins the used
group at `pkg/gateway/podlifecycle/podsession/binder.go:288-298`, and is reported if it lands
alone in `slotfailure.go` as the files-touched list says, with no consumer until the CODE-4 step.

WATCHOUT: the deliverable "Tiers:" lines in non-spec-changes.md mean "tiers this deliverable's
own NEW gates land at", not "tiers the step runs". DOCS-3 says so in words ("adds no gate and
declares no tier above 0", :2883-2887) while sitting in checklist S7, whose line reads
"Tiers 0, 11". DOCS-4 has the same mismatch against S23 on its own. I judged it below the bar
rather than a contradiction; a later lens tempted to file it should read DOCS-3 first.
EVIDENCE: non-spec-changes.md:2883-2887,:2898; implementation-checklist.md:29-30,:61-62

WATCHOUT: CODE-7's block (non-spec-changes.md:1929-1931) narrates the client's request-side
halves (`bind_attempt` on six requests, `mid_session`, `unconditional_teardown` inside
`Shutdown`/`ShutdownRecycle`) that checklist S13 owns, and specifies only `ShutdownReclaim`
itself. It reads as duplicate staging but is not: S11 landing any of it early breaks nothing,
because the refusals (S14, S16) come later. I refuted this before filing it.

### [non-spec.10.review-citations.1]

DECISION: returned an EMPTY findings list — BECAUSE a mechanical audit of every `file:line`
citation in the three non-review-log proposal files resolved clean — ALTERNATIVES: filing the one
false absence claim I found (see the declined item below), rejected as immaterial under the
"absence does not make the applied spec or implementation wrong" bar.

FACT: the citation surface of this proposal is now 182 unique `file:line` citations in
`.non-spec-changes.md`, ~95 in `.summary.md`, 5 in `.spec-changes.md`, and 0 in
`.implementation-checklist.md` (which cites by heading, per N8). A python sweep that resolves each
basename, opens the target and prints the cited lines beside the proposal's own sentence is the
cheap way to audit the whole set in one pass; it took ~5 minutes to build and caught nothing,
which is the useful result. The script shape: regex `([A-Za-z0-9_./-]+\.(go|proto|sql|md|json|yaml|yml)):(\d+)([,-](\d+))?`
over each line, `glob('**/'+basename)` for the bare-basename citations, then print target lines.
EVIDENCE: /tmp/cit.txt, /tmp/cit2.txt, /tmp/cit3.txt built this round (not preserved).

FACT: every bare-basename citation in the proposal (`slot.go:105`, `sdkwarm.go:261`,
`catalog.go:271`, `client.go:333-341`, `session.go:259-261`, …) is ambiguous across two to four
files in the tree, and every one of them resolves correctly from its paragraph's context. Do not
file a bare basename as an unresolvable anchor; three of them (`sdkwarm.go`, `slot.go`,
`server.go`) have a same-named sibling under `pkg/controller/sandbox/` or `cmd/`.
EVIDENCE: pkg/controller/sandbox/sdkwarm.go vs pkg/adapter/sdkwarm.go.

FACT: the round-9 fix (the added `-e 'reports its outcome to the gateway'` alternative in CODE-10's
grep) works and adds exactly ONE new hit tree-wide:
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458`, the `// diagnosis:`
comment whose sentence wraps "at each session / release" across a comment break at :455-456. That
site falls squarely in CODE-10's coordinated-clause arm, whose own worked example ("a per-slot
cleanup runs at every session release, and the adapter reports its outcome") is almost this
sentence verbatim. The other two hits in that file (:18 header comment, :473 a `t.Errorf` FORMAT
STRING, not a comment) carry the CLEANUP universal, which survives, so both take the non-carrier
arm. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:455-458,:473.

USEFUL [Traps, "the two tier-11 files under checklist S4's sweep"]: I nearly filed CODE-10's
non-carrier-arm parenthetical as a wrong count. It is correct: the SPEC-3 carrier table names
exactly two tier-11 files as "Staged by checklist S4's tier-11 sweep"
(`spec_28_register_writers_test.go` and `concurrent_slot_lifecycle_doc_reconciliation_test.go`),
at spec-changes.md:608-609. `grep -rln sessions_served tests/` reaching three tier-11 files is not
the referent. EVIDENCE: spec-changes.md:608-609.

WATCHOUT, checked and DECLINED this round, do not re-derive: the non-spec file's
"`status.Convert` appears nowhere in `pkg/gateway`" is literally FALSE — it appears twice, at
`pkg/gateway/mcpfabric/delegationtree/leasecontrol/connectortools_test.go:188-189`, reading a
status MESSAGE in a test. It is immaterial: the sentence's work is that no production gateway path
reads a gRPC status detail or code off a bind failure, and the companion claim in the same
sentence ("the only status-detail reader in the whole gateway is `client.go:333-341`") IS true —
the only other `.Details()` hits under `pkg/gateway` are `ferr.Details()` on the field-error type,
not `status.Status.Details()`. A later lens that wants this owes an argument that the falsehood
changes what an implementor builds. EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/connectortools_test.go:188,
pkg/gateway/runtime/adapterclient/client.go:336.

FACT: line drift into the TREE (not into the proposal files) is small but real and is not a
finding. Two measured instances this round: `adapterevents_test.go:184` is `s := New("served")`
where the cited `noteRuntimeStarted` call is at :185; the summary's 0078 row cites
`tests/tier4_integration/concurrent_delegation_proxy_test.go:168` for "the cleanup", which sits at
:170. Both are off-by-one-or-two on otherwise exact claims. EVIDENCE:
pkg/adapter/adapterevents_test.go:184-185, tests/tier4_integration/concurrent_delegation_proxy_test.go:168-170.

FACT, verified so a later round need not: all seven verbatim-quote anchors resolve EXACTLY.
SCHEMA-1's four proto quotes (schemas/lenny-adapter.proto:308-310, :311-314, :437-441, :442-447,
:451-453), DOCS-1's three docs quotes (docs/reference/state-machines.md:235, :237, :251), DOCS-2's
two row anchors (docs/reference/adapter-contract.md:64, :81 plus the `Shutdown` row at :75),
DOCS-3's four sentence quotes and remedy cell (docs/reference/error-catalog.md:129), and DOCS-4's
two page sentences (docs/reference/execution-modes.md:68,
docs/operator-guide/security-principles.md:33) are all byte-exact against the tree.

FACT: the two SCHEMA-1 edits that both touch the `ReportSessionScrub` RPC comment COMPOSE
correctly, which is the thing the archive's "`The outcome is` proto orphan" trap warns about. Edit
A consumes the trailing "The outcome is" on line 310; edit B's replacement is a complete sentence
opening with "RELEASED", and the remainder of line 314 ("The gateway resolves the pod from
pod_id, …") is a whole sentence that stands. No orphan. EVIDENCE: schemas/lenny-adapter.proto:308-317
against non-spec-changes.md "the text that reads, verbatim" blocks.

FACT: every absence claim I could mechanize holds. No file under `tests/`, `scripts/`, `cmd/` and
no `Makefile` target names `docs/reference/error-catalog.md` (DOCS-3). No gate reads either DOCS-4
sentence. `per_slot_substate_scope_doc_reconciliation_test.go` really does read §6.2 and
state-machines.md in two separate test functions that never compare them (DOCS-1), and DOCS-1's
three table/clause replacements keep every substring `requireAllContain` demands
(`` `slot_cleanup` ``, `` `released` ``, `` `slot_cleanup -> leaked` ``). Nothing named
`bindEpoch`, `BindEpoch`, `reclaimSlotLocked` or `slotResolveError` exists under `pkg/`, `cmd/` or
`tests/`. The four §16.1 adapter-deferral metrics are absent from both `catalog.go` and
`spec161Metrics`, and the two that carry catalog rows are present in both. EVIDENCE:
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:95-121,
pkg/observability/metrics/catalog.go:146,:271, spec/16_observability.md:186-189.

FACT: all thirteen commit hashes cited across summary.md and the caller directives resolve, with
the dates and subjects the proposal states, including `f37e867b8` (2026-08-19) which `git blame`
confirms is the commit that put the §4.1 `ShutdownRequest` sentence at
`spec/04_system-components.md:157`. The summary's "125 times across `pkg/`, `cmd/` and `tests/`"
count for the retired `spec: NN:LINE` spelling is exact today
(`grep -rnoE 'spec: *[0-9]{1,2}:[0-9]+' pkg/ cmd/ tests/ | wc -l` → 125). A wider pattern that
also matches `§`-prefixed forms returns 139, so the count is spelling-specific; do not "correct"
it with the wider number.

OPEN, for whoever certifies: the citation lens has now swept the whole citation surface
mechanically and found nothing above the bar. If a later round wants to re-run it, re-run the
script rather than re-reading paragraphs; the marginal value of a second human pass over these
182 citations is near zero.

### [non-spec.10.review-client-surface.1]

Round 10 of the non-spec loop, client-facing surface integrity lens. EMPTY verdict. Fifth
consecutive empty on the non-spec staging (rounds 3, 5, 7, 8, 10).

USEFUL [round 8 standing-context entry "No non-proto wire schema, SDK or OpenAPI surface carries
anything this proposal adds or retires"] — re-verified in full this round and it was still true;
it saved re-deriving the whole SDK/OpenAPI sweep. EVIDENCE: `grep -rln "ERROR_CODE_|ErrorCode"
sdks/ schemas/*.json docs/` returns nothing; `pkg/gateway/externalapi/openapi/openapi.json`
carries only `slot`, `slots`, `slotRetries` (`:407`) and no REST error-code enumeration.

FACT: the round-9→10 delta is ONE line — CODE-10's carrier-set grep gains
`-e 'reports its outcome to the gateway'` (non-spec-changes.md:2163). Verified
client-surface-neutral: that alternative's only new hit outside `docs/` is
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458`, and the two `docs/`
hits (`execution-modes.md:68`, `security-principles.md:33`) are DOCS-4's own two carriers. No
proto, schema, CRD, SDK, OpenAPI, MCP or docs-row text moved. EVIDENCE: `diff -ru -x
'*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-r8-prefix proposals/0081_*/` is one hunk.

FACT: every SCHEMA-1 field number re-checked free against the live proto, message by message:
PrepareWorkspaceRequest 1-3 + reserved 4 (`schemas/lenny-adapter.proto` message block),
FinalizeWorkspaceRequest 1-4 (`mid_session` is 4) + reserved 5, RunSetupRequest 1-3 + reserved 4,
AssignCredentialsRequest 1-2 + reserved 3, ResumeRequest 1-5,7-14 + reserved 6,15,
ShutdownRequest 1-3,5,6 + reserved 4, ShutdownResponse 1-2. `ErrorCode` ends at 27
(`:585`) and the 1000-1999 range is prose with no `reserved` statement (`:587-588`). All nine
SCHEMA-1 numbers are free. Every verbatim proto comment SCHEMA-1 quotes matches byte for byte
(`:308-313`, `:440-442`, `:444-447`, `:451-452`).

FACT: the closed-field-set claim holds. The only `assertFieldSet` in `tests/tier3_contract/` is
`shutdown_recycle_wire_test.go:218,233`; `checkpoint_stream_wire_test.go:151` pins
`CheckpointStart` at six fields, a message SCHEMA-1 does not open. No descriptor or enum-length
pin anywhere in `pkg/` or `tests/` covers the six bind-sequence requests or `ErrorCode`.

FACT: every DOCS-1/DOCS-2/DOCS-3 line citation resolves exactly.
`docs/reference/adapter-contract.md` :10, :53, :64 (DemoteSDK), :75 (Shutdown), :81
(ReportSessionScrub); `docs/reference/state-machines.md` :138 (pod paragraph), :235, :237, :251;
`docs/reference/error-catalog.md` :129 (SETUP_COMMAND_FAILED), and all four quoted sentences plus
the remedy cell match verbatim. The two tier-11 gates the proposal relies on do what it says:
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName`
(`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294`) checks exactly the
four substrings named, all of which survive the staged row;
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
(`tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43`) requires the
literal opener `"The request is session-scoped: it is "` + the addressing rule + `"."`, which the
staged ReportSessionScrub row preserves word for word.

FACT: the DOCS-2 spec links resolve. `#471-role-and-gateway-rpc-contract` matches
`spec/04_system-components.md:659` and `#154-runtime-adapter-specification` matches
`spec/15_external-api-surface.md:1458`, and absolute GitHub spec URLs are the established docs
convention (`docs/about/style-guide.md:31,:100`).

FACT: SCHEMA-1's claim-register citations all check out. `#2851-gateway-to-pod` is the anchor the
sibling `coordination_generation` rows use (`scripts/seed-claim-register.py:86,:381-383`),
`EXPLICIT` is at `:169`, an existing `R8` deferral row is at `:192`, and `R8` is declared at
`gateway-runtime-comms-remediation.md:980`. `cmd/lenny-compliance/` imports no gRPC (no `grpc`
hit in any of its `.go` files), as CONF-1 states.

WATCHOUT: the `ErrorCode` enum header comment reads "The catalog below mirrors spec §15.1"
(`schemas/lenny-adapter.proto:556`), and SPEC-5 deliberately adds NO §15.1 row for either new
code, so a future round may want to file it as a newly-falsified proto comment. I did NOT file
it, and the next agent should not either without new evidence: the comment is already loose
today. `SESSION_NOT_FOUND`, `INVALID_WORKSPACE_PLAN`, `DELEGATION_BUDGET_UNAVAILABLE`,
`DELEGATION_DENIED`, `MAX_DELEGATION_DEPTH_EXCEEDED` and `PLATFORM_DEGRADED` each appear zero
times anywhere in `spec/15_external-api-surface.md`. The two additions do not make it newly
wrong. EVIDENCE: `schemas/lenny-adapter.proto:556-558`; per-value grep over
`spec/15_external-api-surface.md`.

WATCHOUT: `ShutdownRequest.recycle`'s proto comment says "the adapter tears the named session
down, keeps the pod process alive, runs the §5.2 whole-pod scrub"
(`schemas/lenny-adapter.proto:1621-1628`), and the proposal's own `answerShutdown` analysis says
`Binder.ReleaseSlot`'s recycle Shutdown answers `ABSENT` and tears nothing down. This looks like
a missed proto edit site but is NOT one: the same is already true in the shipped tree, because
`slotbinder.go:542` sends an unconditional Shutdown that removes the entry before
`slotbinder.go:574` sends the recycle Shutdown on the same connection. Pre-existing, not
introduced here. EVIDENCE: `pkg/gateway/podlifecycle/podsession/slotbinder.go:537-578`.

FACT: `SLOT_FAILED` is a live 422 REST code the gateway writes
(`pkg/gateway/sessionserver/start.go:313`) and it has NO row in
`docs/reference/error-catalog.md`. The started-session refusal (`FailedPrecondition` →
`policy_rejection` → non-retryable) lands in that envelope on the non-setup bind stages, so a
future lens will notice it. It is not a finding for 0081: the row is absent today, so no
published row becomes wrong. The per-endpoint "Key error codes" lists in `docs/api/rest.md`
(`:100`, `:115`, `:133`) likewise never carried `SETUP_COMMAND_FAILED`, so DOCS-3 leaves them
untouched correctly.

DECISION: returned EMPTY — BECAUSE every client-facing representation this proposal opens (the
adapter proto and its regenerated package, the tier-3 descriptor gate, `adapter-contract.md`,
`error-catalog.md`, `state-machines.md`, the claim register) is edited, and every parallel that
is not edited (OpenAPI, MCP tool schemas, the six SDK trees, CRDs, `charts/`, the JSONL and
runtime-ops-events schemas, `docs/client-guide/`, `docs/api/`, `docs/runtime-author-guide/`)
carries nothing the change touches, verified by reading those trees rather than the proposal's
text. ALTERNATIVES: the two WATCHOUTs above and the previously-recorded `SlotReclaimOutcome`
descriptor-pin gap were each considered and each falls under the bar.

OPEN [carried, not re-filed]: "Should the tier-3 descriptor gate pin the `SlotReclaimOutcome`
enum values?" — still open from `[non-spec.5.review-client-surface.1]`. The new gate pins the
nine fields and both `ErrorCode` values but no value of the new enum, although the gateway
branches on `slot_reclaim` and a third-party adapter must emit it. Nice-to-have hardening; the
symmetric-regeneration argument that would excuse it would equally excuse the `ErrorCode` pin the
proposal does make. A human, not another lens round, should close this.

### [non-spec.10.review-docs-alignment.1]

DECISION: returned an EMPTY findings list — BECAUSE every docs surface this proposal touches is
staged and verified, and each candidate I built either resolves to a standing-context entry that
already declined it on recorded grounds or fails the bar on its own. ALTERNATIVES considered and
rejected, each with the ground: (a) `docs/reference/adapter-contract.md` owing a §15.4
reclaim-hold mirror beside DOCS-2's token paragraph — rejected, the page describes the reclaim
hold nowhere today, so the proposal leaves no sentence false and this is an omission of new
material rather than a missed edit site (the standing DEFERRED at review-log.md:1326 says
"mirror it or record why", which is an improvement, not a defect); (b) `docs/api/internal.md`'s
gRPC status table omitting `ABORTED` — rejected on new evidence, the shipped adapter ALREADY
answers `codes.Aborted` from the checkpoint op lock (`pkg/adapter/checkpoint.go:115`), so the
omission predates this proposal exactly as the standing DEFERRED at review-log.md:1331 says;
(c) the queue-head-hold bullet as a new cause of `WARM_POOL_EXHAUSTED` missing from
`docs/operator-guide/troubleshooting.md`'s "Common causes and resolution" table — rejected twice
over, that table's causes are all warm-pod SUPPLY reasons keyed on
`lenny_warmpool_warmup_failure_total` plus `minWarm` and carry no queue-wait cause at all today,
and the proposal's own bullet establishes the hold class is pre-existing
(`SetupPolicy.timeout_seconds` of zero); (d) the "rolled-back start can close a successor's
runtime session" accepted mode — rejected, already adjudicated and declined at
review-log-archive.md:64592 and :65542 on grounds that still hold.

FACT: the docs-carrier sweep for the withdrawn reporting universal is COMPLETE and I re-ran it
wider than the standing context's form. `grep -rniE "ReportSessionScrub|reports? (its|the|that)
outcome|per-slot cleanup|slot cleanup|cleanup outcome|cleanup-outcome" docs/` (excluding
assets/diagrams) returns exactly 15 hits: the two DOCS-4 carriers
(`docs/reference/execution-modes.md:68`, `docs/operator-guide/security-principles.md:33`), the
two DOCS-2 rows (`docs/reference/adapter-contract.md:75`, `:81`), the standing-Open
`adapter-contract.md:84` `**Scrub responsibilities.**`, `runtime-author-guide/lifecycle.md:390`
(the WHOLE-POD scrub, correctly outside), and eight residual-state table cells and
`multi-tenancy.md:72` / `runtime-author-guide/index.md:186` / `client-guide/session-lifecycle.md:416`
that state the cleanup alone and stay true. No sixteenth carrier exists. — EVIDENCE:
docs/reference/execution-modes.md:63-68; docs/operator-guide/multi-tenancy.md:67-72;
docs/reference/adapter-contract.md:75,:81,:84; docs/runtime-author-guide/lifecycle.md:390

FACT: all four DOCS deliverables' anchor line numbers are live at HEAD and their quoted "currently
reads" text is verbatim. `state-machines.md:138` (projection paragraph), `:235`
(`receiving_uploads`→`running`), `:237` (`slot_cleanup`→`released`), `:251` (leaked clause);
`adapter-contract.md:64` (`DemoteSDK`), `:75` (`Shutdown`), `:81` (`ReportSessionScrub`);
`error-catalog.md:129`; `execution-modes.md:68`; `security-principles.md:33`. Do not re-derive.
— EVIDENCE: docs/reference/state-machines.md:138,235,237,251; docs/reference/adapter-contract.md:64,75,81

FACT: DOCS-1's rewritten `slot_cleanup -> leaked` clause is a faithful partition of the staged §5.2
disposition table's two `Entered` rows plus the table's follow-on prose. `leaked` report (row 2),
clean-exit-not-set on a pre-`running` `Shutdown` whose act fails (row 5), and an unanswered §7.1
reclaim ("A slot enters `leaked` on the gateway's reading of the report or of the response") map
one-to-one onto the clause's three disjuncts. Checked so no later docs lens re-walks it.
— EVIDENCE: spec-changes.md:618-631; non-spec-changes.md:2691-2697

FACT: the two tier-11 gates DOCS-1/DOCS-2 extend behave as the deliverables claim, re-verified.
`TestAdapterContractNamesTheShutdownRPCUnderItsWireName` asserts exactly the four substrings named
and all four survive the staged row; `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc`
requires the literal `"The request is session-scoped: it is addressed by the identifier of the
released session and names no slot."` on BOTH carriers, and DOCS-2's staged row reproduces it
character for character. Adding `"receiving_uploads ──→ slot_cleanup"` to `generalSlotEdges` is
safe on the NEGATIVE loop too: the §6.2 concurrent-occupancy block contains no such substring.
— EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311;
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:31,66-74;
tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:31-37,63-73

WATCHOUT: `tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8` and
`:20-21` are TWO more comment carriers of the withdrawn universal ("at every session release",
"per-slot cleanup at each session release"), in a file no deliverable names. Both are single-line
and both ARE returned by CODE-10's grep (`every (session|slot|clean) release` and
`each (session|slot|clean) release`), so CODE-10 closes them at application time and this is not
a finding. Recorded so the next edit-sites or docs lens does not file it as an unstaged carrier.
— EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:6-8,20-21;
non-spec-changes.md:2160-2165

OPEN, below the bar, do not file without a new argument: DOCS-4's body says "DOCS-4 lands beside
DOCS-2, after SPEC-3" while the checklist places it at S23 and explicitly says it is NOT beside S7
("It carries the step id S23 rather than a position beside S7 because the steps above were
numbered before this deliverable was staged"). The consequence is a docs window from S4 to S23 in
which `execution-modes.md:68` and `security-principles.md:33` still assert a universal the landed
§5.2 has withdrawn. I did not file it: no gate reads either sentence, the ordering constraint
("after SPEC-3") is satisfied at S23, and the checklist states the divergence in its own text
rather than hiding it. — EVIDENCE: non-spec-changes.md:2898; implementation-checklist.md:61

USEFUL [standing context, "The five docs-mirroring sweeps and their exact commands" and
"`docs/operator-guide/observability.md` is a curated operator subset rather than a §16.1 mirror"]:
between them these two saved rebuilding the whole reporting-universal sweep and a false filing on
a missing observability row for the two new counters.

### [non-spec.10.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens on round 10 — BECAUSE every
identifier the proposal adds, changes or removes was grepped across spec/, docs/, schemas/,
charts/, pkg/, cmd/, tests/ and migrations/, and every surface that would become wrong is
already in an edit list — ALTERNATIVES: filing the CODE-9 / slotfailure.go constant-placement
mismatch (see MISTAKE-guard below), rejected as bookkeeping that makes nothing wrong.

FACT: the `ShutdownRequest{` sweep grep in the files-touched list is complete. The only
production constructor outside `pkg/proto/` is `pkg/gateway/runtime/adapterclient/client.go:814`,
and `Client.Shutdown`/`ShutdownRecycle` keep their signatures (the flag is set inside), so
`cmd/lenny-gateway/user_revocation.go:129` and every other caller compile unchanged —
EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:806-822, cmd/lenny-gateway/user_revocation.go:129

FACT: the five adapterv1 bind-sequence request literals are all built in one file, so the
`adapterv1\.(Finalize|RunSetup|AssignCredentials|Resume|PrepareWorkspace)Request{` sweep has no
production blind spot. The near-miss hits are `tokensv1.AssignCredentialsRequest` (a different
service) and `podsession.ResumeRequest` (a gateway-internal struct) — EVIDENCE:
pkg/gateway/runtime/adapterclient/client.go:134,153,181,275,307,328,617

FACT: `ReleaseSlotForTest` has exactly five call sites and one definition, matching the
files-touched parenthetical — EVIDENCE: pkg/adapter/export_test.go:32,
integrationlevel_test.go:84, checkpoint_stream_test.go:1012, credexpiry_test.go:320,
tracingcontext_addressing_test.go:425, one_session_only_test.go:77

FACT: CODE-9's argument for entering only the superseded series in `spec161Metrics` checks out
mechanically. All four §16.1 rows carrying the adapter scrape deferral are absent from both
`catalog.go` and `catalog_test.go`, while `lenny_adapter_coordinator_hold` and
`lenny_credential_rotation_inflight_ceiling_hit_total` (no deferral) are in both. Entering the
adapter series in `spec161Metrics` would fail `TestMetricCatalogIsCompleteAgainstSpec161`, and
entering it in `catalog.go` alone would fail `TestMetricCatalogHasNoUnspecifiedMetrics` —
EVIDENCE: spec/16_observability.md:186-189, pkg/observability/metrics/catalog_test.go:188-211

FACT: the new adapter metric is still gated. `TestAdapterMetricsReachTheDocumentationCatalogs`
requires every name in `pkg/adapter/metrics.go` to reach BOTH `docs/reference/metrics.md` and
the §16.1 catalog, or to carry a `specCatalogPending` entry; SPEC-6 plus CODE-9's metrics.md row
discharge it with no exception entry — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:80-115

FACT: the adapter DOES hold a pod identity, so SPEC-6's `k8s_pod_name` label on
`lenny_slot_shutdown_untokened_entry_total` is reachable. `Server.podID` is read once from the
Downward API `POD_NAME` env at construction — EVIDENCE: pkg/adapter/server.go:36,372-380. Do not
re-file this as an actor-cannot-perform finding; I checked it and it holds.

WATCHOUT: `slotSubjectFileRE` in the slotAddressCaseFiles completeness gate is
`(slot|one_session_only|sole_session)[^/]*_test\.go$`, which matches a basename CONTAINING
"slot" anywhere, not the `slot*_test.go` prefix the files-touched list states. Every new file
this proposal creates happens to start with `slot`, so the mis-statement costs nothing here, but
a later file named e.g. `per_slot_foo_test.go` would be caught by the gate and missed by the
proposal's rule — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1173,1108-1131

FACT: there is no enumeration anywhere of the `error_type` label values of
`lenny_slot_failure_total`, so the new `slotFailureWorkspaceFinalize` stage constant opens no
docs or spec edit site. The const block's own doc comment describes the set but lists no values
outside the block — EVIDENCE: spec/16_observability.md:14, docs/reference/metrics.md:166,
pkg/gateway/podlifecycle/podsession/binder.go:286-299

UNVERIFIED: CODE-9's deliverable heading enumerates its files and omits
`pkg/gateway/podlifecycle/podsession/slotfailure.go`, while the files-touched list places
CODE-9's new `slotFailureWorkspaceFinalize` constant there and CODE-4's own target line for that
file stages only `SlotBindError.Leaked`. I judged this bookkeeping rather than a defect, because
the files-touched list is the edit list and it names the file. A later reviewer who disagrees
should read non-spec-changes.md:2041, :873, :2098-2100 and :4102-4103 together before filing.

USEFUL [the round-9 refutation of the wrapped-carrier finding]: it saved me from re-walking the
CODE-10 carrier set by hand; I confirmed the round-9 fix added `-e 'reports its outcome to the
gateway'` and that the one Go carrier of that phrase,
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458, is now returned and is
in the files-touched list.

### [non-spec.10.review-fresh.1]

DECISION: returned an EMPTY findings list — BECAUSE a fresh holistic pass spot-checked, and
verified line by line against the tree, roughly forty distinct citations spread across the
Design, CODE-1, CODE-4, CODE-6, CODE-9, SCHEMA-1, DOCS-1, DOCS-2, DOCS-3, the tier-11 test
subsection and the Edge-cases bullets, and every one resolved. ALTERNATIVES: filing the
`maxConcurrentSessions: 2` attribution below (rejected as wording, see WATCHOUT).

FACT: the round-9 CODE-10 grep edit (the sole diff since the r8 snapshot, one added
alternation `-e 'reports its outcome to the gateway'`) returns exactly ONE new hit,
`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458`, and that hit IS the
`// diagnosis:` tail of `TestPerSlotCleanupStatedOnEverySessionModeRow` that the `### Deferred`
entry describes as still open. The whole command now returns 47 lines.
— EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:453-461

CORRECTS [### Deferred, "non-spec-changes.md, CODE-10's grep"]: that entry says a second wrapped
carrier "survives" in that file "in the `// diagnosis:` tail of
`TestPerSlotCleanupStatedOnEverySessionModeRow` at `:453`", and that the seven-pattern grep
still misses it. It no longer does: the wrap it names is the SAME comment block whose line 458
the round-9 alternation now returns, so CODE-10's command reaches the block and the carrier is
dispositioned. The Deferred entry is discharged and the compaction pass should retire it rather
than carry it forward; the joined-comment pass it prescribed is not owed.

FACT: three tier-11 files appear in CODE-10's output, but the shared block's non-carrier arm
excuses only "the two tier-11 files under checklist S4's sweep". Those two are named in the
SPEC-3 carrier table (`spec_28_register_writers_test.go` and
`concurrent_slot_lifecycle_doc_reconciliation_test.go`), and the third
(`session_scrub_report_addressing_doc_reconciliation_test.go:6-8`) is a genuine CODE-10 carrier
that the arm rule's own worked example covers verbatim ("a per-slot cleanup runs at every
session release, and the adapter reports its outcome"). Not a gap; do not re-derive it.
— EVIDENCE: spec-changes.md carrier table rows; non-spec-changes.md shared block, non-carrier arm

FACT: the two-part SCHEMA-1 replacement over the `ReportSessionScrub` RPC comment DOES close the
"`The outcome is`" proto orphan the Traps section records. The second replacement explicitly
quotes "the opening sentence ... together with the words that open the sentence after it on the
same physical line" and consumes `The outcome is` at the end of `schemas/lenny-adapter.proto:310`.
Applying both replacements literally leaves a grammatical comment. Stop re-checking this.
— EVIDENCE: schemas/lenny-adapter.proto:308-318; non-spec-changes.md SCHEMA-1 report-trigger block

FACT: a normalized repeated-sentence sweep (>=100 chars, over spec-changes, non-spec-changes,
summary and checklist, INCLUDING fenced blocks) returns five repeats and every one is licensed:
the S-2 proto-window rationale (Design + summary, rationale not rule), the `SESSION_SCRUB_OUTCOME_LEAKED`
drain-ledger sentence (SCHEMA-1's verbatim-before and verbatim-after), the `ReportSessionScrub`
addressing sentence (three sites, pinned to be identical by
`TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` at
tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:65-75), and DOCS-3's
"envelope that stage already selects" rationale against SPEC-5's preamble. No rule (g) finding.

WATCHOUT: the Edge-cases bullet on the queue head says the compensation budget is "fifteen on the
§5.2 example pool's `cleanupTimeoutSeconds: 30` at `maxConcurrentSessions: 2`
(`spec/05_runtime-registry-and-pool-model.md:166`)". Line 166 does carry `cleanupTimeoutSeconds: 30`,
but the YAML it sits in is the DERIVED RUNTIME example `research-pipeline` (block opens at :121,
heading `#### Derived Runtime` at :117) and it declares no `maxConcurrentSessions` at all; the
file's only `maxConcurrentSessions:` literal is `1`, at :399. So "fifteen" is the formula
evaluated at a concurrency that example does not state (for that pool it would be thirty). Judged
BELOW the bar and not filed: the sentence names both parameters explicitly, the citation backs
only the `30`, and nothing in the applied spec or the staged code depends on the figure. A later
round that wants it owes an argument that the magnitude is load-bearing.
— EVIDENCE: spec/05_runtime-registry-and-pool-model.md:117,121,166,399

FACT: `summary.md` mentions `SPEC-7` and `SPEC-8` while this proposal stages only SPEC-1 through
SPEC-6. Both are OTHER proposals' deliverables inside the cross-proposal impact table (0073's
SPEC-7, 0072's and 0079's SPEC-1 through SPEC-8). Not an index gap; three lenses could burn a
check here. — EVIDENCE: summary.md:1116, :1119, :1120

USEFUL [Traps, "`schemas/lenny-adapter.proto:437` ... `:468-473` ... Only :308-310 and :451-452
assert the withdrawn report trigger"]: saved a full re-derivation of the proto carrier set; the
line numbers have drifted by one (309 and 452 at HEAD) but the dispositions are exact.
USEFUL [Traps, "the bind-field sweep's grep MUST carry the `adapterv1.` qualifier"]: confirmed
present in the files-touched sweep bullet, so that entry is discharged by the current text.

### [non-spec.10.review-kubernetes.1]

EMPTY VERDICT. Round 10 of the non-spec loop, Kubernetes-idiom lens. No finding met the bar.

USEFUL [standing context, "The whole KUBERNETES surface of the non-spec staging is FOUR sites"]:
the one grep really is the whole opening move. Re-ran
`grep -n "controller\|finalizer\|Sandbox\b\|SandboxClaim\|admission\|webhook\|reconcil\|etcd\|apiserver\|CRD\|field manager\|OwnerReference\|podclaim"` plus a second pass for
`RBAC|NetworkPolicy|charts/|Helm|namespace|ServiceAccount|envtest|DrainSandbox|drain-request`
over non-spec-changes.md and summary.md: 12 and 10 hits respectively, all inside the four known
sites (CODE-5's `Unhealthy → DrainSandbox` tail, CODE-8's refusal arm, the tier-2 envtest pair,
DOCS-1's pod-state-machine paragraph) plus the unstaged-defect entry at summary.md:1024-1044.
Everything else in the staging is in-process adapter state and gRPC.

FACT: re-verified this round, so the next kubernetes lens need not: `binder.go`'s ONLY apiserver
writes are the two `podclaim.DeleteClaim` calls at
`pkg/gateway/podlifecycle/podsession/binder.go:1102` and `:1201` (`grep -n "Status()\.\|\.Patch(\|\.Update("`
returns nothing in that file). That is what makes the tier-2 case's "leaves the per-pod
`SandboxClaim` present and the `Sandbox` untouched" (non-spec-changes.md:3530-3537) exactly true
for both arms, with no field-manager or SSA reasoning needed.

FACT: the `claimToSandbox` coalescing claim in the unstaged-defects row (summary.md:1030-1031,
cited `occupancy.go:281-291`) is accurate as cited: `pkg/controller/warmpool/occupancy.go:281-292`
maps every `SandboxClaim` event to one request keyed on `cl.Spec.SandboxRef`.

FACT: the four verbatim proto comment quotes SCHEMA-1 replaces exist byte-for-byte at
`schemas/lenny-adapter.proto:308-319` and `:438-449`. The `lenny.dev/drain-request` sentence
SCHEMA-1 deliberately leaves standing is the §4.6.3-owned half and is correct to leave.

FACT: DOCS-1's four replacement strings for the pod state machine paragraph all exist verbatim at
`docs/reference/state-machines.md:138`, and the replaced text lands on the same partition the
staged SPEC-4 §4.6.1 bullets state (spec/04_system-components.md:409-417 for the shipped bullets
the staging replaces). `idle for a pod in a warm-inventory phase with no claim` is already the
shipped §4.6.1 wording, so the docs edit is a mirror rather than a new rule.

WATCHOUT (for a future kubernetes or mechanism lens): the tempting finding here is "CODE-5 accounts
a bind REFUSAL against the pod's §5.2 health ledger from `resumeOnPod`, which at
`maxConcurrentSessions: 2` drains a healthy pod on the first refusal — the exact harm CODE-8 exists
to prevent, reached by another route." Do NOT file it. It is the substance of OPEN DECISION 34
(summary.md:605-634), which names the refusal case explicitly ("A cleanly reclaimed re-attach
refusal is therefore neither `failed` nor `leaked` on the documented state machine, so §5.2 does
not oblige counting it") and puts the both-arms-versus-leaked-arm choice to the human. Filing it is
resolving an open decision. I walked it to the end before deciding not to file; the ground is
`slothealth.go:215-220` (threshold `int((maxConcurrent+1)/2)`), non-spec-changes.md:1288-1300
(the `resumeOnPod` caller and its non-empty-slot-id guard) and :1951-1966 (CODE-8's rationale).

FACT: the k8s-idiom checks this lens owns all pass on the staging and are recorded here so they are
not re-derived: no component writes another's status subresource (the gateway writes only
`SandboxClaim.spec/status`, which §4.6.3 at spec/04_system-components.md:619 assigns it, and never
`Sandbox.status`); no status field is used as an RPC inbox (every compensation is a direct gRPC
`Shutdown` to the adapter, no etcd hop); no finalizer is added or removed anywhere in the staging;
no admission webhook is touched (the proposal's "admission" is the adapter's in-process RPC
cascade, a different mechanism from the `lenny-sandboxclaim-guard` `ValidatingAdmissionWebhook` at
spec/04_system-components.md:405, and the two never meet); no controller reconcile, work-queue or
leader election is placed on a synchronous request path (`DrainSandbox`'s annotation merge patch on
the resume path is the shipped pattern `applySlotRetryPolicy` already runs); and the one genuine
level-triggered-projection hazard in the neighbourhood is the pre-existing coalesced-reconcile
window already recorded as an unstaged defect.

### [non-spec.10.review-mechanism.1]

FACT: the shipped `materializeSlot` closes the adapter connection on EVERY stage-failure return — `cl.Close()` at `pkg/gateway/podlifecycle/podsession/slotbinder.go:286, :293, :301, :308, :321` (grep `cl.Close()` over that file returns 182, 244, 249, 286, 293, 301, 308, 321, 467, 472; the five in the middle are `materializeSlot`'s). CODE-4 splits that body into `materializeSlotStages` plus a wrapper whose error branch compensates on `cl` and then closes it once. Nothing in the proposal says the five in-stage closes move to the wrapper; three sentences merely assert the connection is open at compensation time (non-spec-changes.md:990-993, :1146-1152, :1161-1165). A mechanical split keeps them and every compensation runs on a closed `grpc.ClientConn`. EVIDENCE: proposals/.../non-spec-changes.md:1056-1110 (wrapper), :4104-4109 (files-touched, names only the two functions). Filed this round.

FACT: the CODE-10 seven-pattern grep as it stands at HEAD returns 47 lines and DOES return `tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458`, which is inside the `// diagnosis:` comment of `TestPerSlotCleanupStatedOnEverySessionModeRow` (the comment spans :454-462). EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:453-462.
CORRECTS [the `### Deferred` entry "a second wrapped carrier survives ... in the `// diagnosis:` tail ... at `:453`"] and the Trap "A SECOND wrap in the same file's `// diagnosis:` tail (`:453`) is STILL OPEN": that diagnosis comment IS surfaced by the seventh alternation `reports its outcome to the gateway`, which matches its :458 line. The comment is one carrier and one hit; there is no second unsurfaced wrap in that file. Whoever runs the next compaction can close the Deferred on this evidence rather than re-deriving it.

WATCHOUT: `Binder.noteCompensationOutcome`'s rationale at non-spec-changes.md:1024-1029 reads "is a method on `Binder` rather than a free function, because the series ... is labeled by `pool` and `k8s_pod_name` and is emitted through a `Binder` hook, both of which a package-level function can reach." The ground contradicts the conclusion ("can" where the argument needs "cannot"). Judged below the bar this round — the staged artifact is unambiguous and applying it breaks nothing — but a later reader who repairs the sentence should not read it as licence to make the forwarder package-level.

FACT re-verified (cheap, do not redo): the CODE-4 non-compensating-`Shutdown` caller table is complete and exact against the tree. `Client.Shutdown` at `pkg/gateway/runtime/adapterclient/client.go:807`, the single builder `Client.shutdown` at `:813`, `ShutdownRecycle` at `:860`; `Binder.ReleaseSlot`'s two sends at `slotbinder.go:542` and `:574`; `Binder.Release`'s recycle branch at `binder.go:1994` reaching `shutdownAdapter`'s `:2037`/`:2043`. The standing Open "Does `Binder.shutdownAdapter`'s retire arm set `unconditional_teardown`?" is answered by that table's `binder.go:2043` row plus the set-inside-the-builder rule; it can be retired.

### [non-spec.10.review-performance.1]

FACT: `applySlotRetryPolicy` wraps `binder.BindSlot` with `maxSlotRetries = 1`, so ONE queued
attempt runs up to TWO `BindSlot` calls, and `BindSlot` reaches `materializeSlot`
(`pkg/gateway/podlifecycle/podsession/slotbinder.go:128-133`), which CODE-4 makes the
compensating wrapper. Both calls sit inside the single closure `runWithQueue` runs
(`pkg/gateway/sessionserver/start.go:2606-2609`, `:2809-2810`), so the queue-head hold the
accepted-failure bullet bounds is up to TWO compensation budgets, not one.
EVIDENCE: pkg/gateway/sessionserver/start.go:2720, :2809; pkg/gateway/podlifecycle/podsession/slotbinder.go:133

FACT: the §5.2 "example pool" the queue-head bullet cites at
`spec/05_runtime-registry-and-pool-model.md:166` is the **Derived Runtime** YAML, whose
`sessionPolicy` sets `cleanupTimeoutSeconds: 30` and NO `maxConcurrentSessions`. `grep -n
"maxConcurrentSessions" spec/05_runtime-registry-and-pool-model.md` returns nothing between
lines 100 and 175; the default is 1 (spec/05:393, :399), so that example's per-slot cleanup
budget is `max(30/1, 5) = 30s`, not the 15s the bullet states. Worst case for the budget formula
is LOW concurrency with a large `cleanupTimeoutSeconds`, not high concurrency: at
`maxConcurrentSessions: 8` the formula clamps to the 5s floor. A capacity lens that reaches for
"the top tier" here should reach for concurrency 1, which is the opposite of the usual reflex.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:121-171, :399, :545

FACT: `DefaultMaxQueueWaitSeconds = 30` (`pkg/gateway/sessionserver/queue.go:18`), so on that
same example pool one head's two compensations (60s) can exceed the whole default queue-wait
bound. That is what makes the bullet's understatement material rather than cosmetic.
EVIDENCE: pkg/gateway/sessionserver/queue.go:18, :194-202

WATCHOUT: every other quantified site in CODE-4 and CODE-9 re-verified clean this round —
`slotCleanupBudget`'s `budget/2` against the `deadlineMs` minimum of 100, the metric hook chain
(`binder.go:137`, `slotbinder.go:353-361`, `metricsbackfill.go:149`, `gatewaymetrics.go:1170-1175`,
`gatewaymetrics_credential.go:185-193`), and the `k8s_pod_name` label precedent
(`spec/16_observability.md:14`, :297). Do not re-walk them.

FACT: `spec/16_observability.md:297` is the single-source attribute table. Its normative clause
binds attribute NAMES ("must not appear anywhere in the spec unless it is in this table"); the
"Used on" column is descriptive, so SPEC-6's new `k8s_pod_name`-labeled series owes it no edit.
Checked and deliberately NOT filed.
EVIDENCE: spec/16_observability.md:295-297

USEFUL [review-log Standing context line 166]: "`maxSlotRetries` is a hard-coded const of 1 ...
so one invocation is two attempts" is what turned the queue-head bullet from clean to a
factor-of-two understatement. It was recorded for a different question and paid off here.

### [non-spec.10.review-reliability.1]

DECISION: returned an EMPTY findings list — BECAUSE the round-9→10 delta is a single grep
alternative and nothing in it is reliability-bearing, and every recovery path I re-traced
(compensating `Shutdown` budget, reclaim hold, per-slot guard, §10.1.4 hold termination,
queue-head hold, tier-8 crash case) is either correct as staged or already disclosed as an
accepted residue with its cost and its reaper named — ALTERNATIVES: I considered filing the
`releaseSessionSlot` "removes unguarded when the caller's context is already cancelled" arm
(non-spec-changes.md:1836-1839), and dropped it: it is explicitly stated, is on the same terms
as `Shutdown`'s removing arm, and the alternative (a fresh context for the acquisition) would
be an unbounded wait on a dying handler.

FACT: the whole round-9→10 delta is ONE added alternative in CODE-10's carrier-set command,
`-e 'reports its outcome to the gateway'` (non-spec-changes.md:2163). `diff -rq` against
`scratchpad/cp-snap/0081-opt2/non-spec-r8-prefix` reports only `non-spec-changes.md` differing,
and its diff is that one hunk. A reliability lens on this round is a no-op delta check; budget
the round on a spot re-verification instead. EVIDENCE: diff -ru output; non-spec-changes.md:2163.

FACT (re-verified, no finding): the queue-head accepted-failure bullet's four tree cites all
resolve. `runWithQueue` wraps the retrying bind at pkg/gateway/sessionserver/start.go:2606-2609
and `binder.BindSlot` is at :2810; head-only admission is the ticket select at
pkg/gateway/sessionserver/queue.go:183-191; the exhaustion sentinel and its `onTimeout` are at
queue.go:193-202; `DefaultMaxQueueWaitSeconds = 30` is at queue.go:18. The bullet's "tested
after admission rather than while the waiter is behind the head" reading is right: the deadline
comparison sits after the `<-ticket` case, not before it. EVIDENCE: queue.go:183-202.

FACT (re-verified, no finding): `failPhase`'s drain failure really does log-and-continue
(pkg/gateway/podlifecycle/podsession/binder.go:1079-1081), which is the premise of the
"pod whose drain failed keeps a dead attempt's token" accepted residue.

USEFUL [non-spec.9.review-reliability.1]: its finding 1 (the `Resume` guard-scope omission)
HAS LANDED — the derivation table now carries a `Resume` row spanning `workspace.ExtractTree`
(non-spec-changes.md:1800) and the compensation's wait on that guard is an accepted-failure
bullet (:3872-3886). Do not re-derive the hand-out-paths pattern; it is closed.

WATCHOUT: the reliability candidate space on this staging is exhausted to the point where the
plausible-looking leads are all already Settled, Open or in the accepted-failure list at
non-spec-changes.md:3826-3935. Read that list BEFORE working a candidate up; four of my five
candidates this round were already bullets in it.

### [non-spec.10.review-security.1]

Round 10, security lens, non-spec loop. Empty findings list. What I verified, so a later
security pass need not redo it:

FACT: the adapter process imports NO client-go / rest.Config anywhere outside tests
(`grep -rn "client-go\|kubernetes.Interface\|rest.Config" --include=*.go pkg/adapter/` returns
nothing), so nothing in CODE-1, CODE-2 or CODE-6 can touch the §10.3 zero-RBAC posture. The
proposal's whole adapter lane is registry map + filesystem + Runtime. Do not re-derive.

FACT: the §10.1.4 hold-state allowlist is exactly five methods — CoordinatorFence,
NegotiateVersion, AdapterEvents, and the two grpc.health.v1 probes — EVIDENCE:
pkg/adapter/holdstate.go:52-58. The accepted failure mode "a compensation sent to a pod in
coordinator-hold state is refused" (non-spec-changes.md ~:3931) is exact, not approximate.

FACT: the §15.4.2 drain gate MOVES INWARD, not outward. Shipped is
`bound := removed && st.sessionID != ""` then `if bound { … if !boundRemains { drainViaLifecycle } }`
(pkg/adapter/session.go:238-261). CODE-1 nests the same `!boundRemains` test inside
`started := removed && st.started` (non-spec-changes.md:369,409-410). That NARROWS the drain
signal (a bound-but-unstarted teardown no longer signals a shared runtime a co-tenant is about
to start on). A security lens that reads "the gate widens from bound to removed" and assumes the
drain widened has misread: the widening is on the SLOT-TREE removal (`if removed { removeSlotTreeVia }`,
:424-432), which is strictly more credential material reclaimed, not less.

FACT: maxScrubFailures is fed by ReportPodScrub, NOT ReportSessionScrub — EVIDENCE:
pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:44 ("ReportSessionScrub
increments sessionsServed; leaked feeds the drain") vs :61 ("ReportPodScrub increments
scrubFailureCount"). So SPEC-3's report biconditional (withholding the per-slot report on the
pre-`running` path) cannot relax the residual-state retirement ceiling. I chased this as the
lens's check-(2) candidate and it dies here. Do not re-open.

FACT: the §7.4 mid-session admission guard the proposal leans on at non-spec-changes.md:58-62
is real and is a live-binding check on the coordinating replica, returning 409 TARGET_NOT_READY
otherwise — EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:111-121. That is what keeps
`mid_session`'s exemption from the identity and phase gates from being a bypass.

FACT: the §11.4 revoke fan-out site CODE-4 tables is exact — EVIDENCE:
cmd/lenny-gateway/user_revocation.go:129 `bind.Adapter.Shutdown(callCtx, bind.SessionID, reason,
userTerminateDeadline)`. It reaches `Client.Shutdown`, which CODE-4 makes set
`unconditional_teardown` itself, so the mandatory revoke cannot be refused by rule 10.

FACT: CODE-8's credential reasoning checks out against the tree. `failPhase` gates
`releaseCredentials` on `leaseAssigned` and drains outside that block
(pkg/gateway/podlifecycle/podsession/binder.go:1072-1082); `leaseAssigned` is still false at the
`assignCredentials` call site (:949-950, set :954), and `assignCredentials` mints into a local
map at :1225 / :1247 before the RPC at :1256. So the attempt-scoped `releaseAttemptCredentials`
CODE-8 puts on that one arm closes a lease leak that ships TODAY, and putting a session-wide
release on the refusal arm would strip a live session's leases
(credassign session-keyed walk). The asymmetry is correct, not an oversight.

WATCHOUT: the token is a capability over a live session's teardown, and the proposal already
pins "the token is never in a message" as a tier-1 assertion (non-spec-changes.md:3036-3038) and
mints from crypto/rand, 16 bytes hex (:893-906, go.mod:3 declares go 1.25.0 so the
no-error-branch comment is correct for crypto/rand on this toolchain). A future lens looking for
a token-leak finding should check the METRIC LABEL SETS instead: CODE-9's two series carry
`pool`, `k8s_pod_name` and `outcome` only, no token, which I checked and which is the only place
a leak could still hide.

DECISION: returned an empty findings list — BECAUSE every candidate I developed (report
biconditional vs maxScrubFailures; drain-gate direction; refusal-arm credential release;
hold-state allowlist; mid_session exemption; adapter-side self-reported reuse counter) either
resolved in the proposal's favour against the tree, or is pre-existing shipped behaviour the
proposal does not make worse, which the bar excludes. ALTERNATIVES: filing the
adapter-self-reported `sessionsServed` as a check-(2) trust-boundary defect — rejected, because
ReportSessionScrub has been the sole incrementer since before this proposal and the staging
changes neither its source nor its authority, so it is not a regression of an established
control and "merely less strict than it could be is NOT a finding".

### [non-spec.10.review-single-source.1]

FACT: the cheap cross-file sentence-repeat sweep (split on `(?<=[.;])\s+`, >=9 words, over the five non-log proposal files) now returns 15 repeat groups, and 13 are the known-benign classes: spec-changes verbatim-anchor/replacement pairs, spec-row/docs-mirror pairs into non-spec-changes.md, summary/checklist one-line deliverable descriptions, and the two status.md boilerplate lines. — EVIDENCE: run in-session; the two survivors were non-spec-changes.md:2037 vs :3869 and :4184 vs :4192.

DECISION: filed ONE finding, the drain-failure residue stated in full at CODE-8 (non-spec-changes.md:2035-2039) and again at the `## Edge cases and accepted failure modes` bullet (:3867-3871), same `binder.go:1079-1081` citation and same "every later attempt at that session on that pod is refused" clause — BECAUSE three sibling deliverables (:1127, :1214, :1379) already use the pointer-only form ("is recorded among the accepted failure modes"), so CODE-8 is the outlier and the reduction target is unambiguous; and the two tellings already differ, CODE-8's lacking the "only on an ordinary failure whose drain then fails" scoping its own deliverable creates — ALTERNATIVES: rejected filing the Design `**The refusal must not drain a healthy pod.**` paragraph (non-spec-changes.md:132-142) against CODE-8's opening two paragraphs (:1953-1963), which state the same `failPhase`-drains-unconditionally fact; the Design section is an overview giving the deliverable's reason and falls under the rule-and-rationale exclusion.

FACT: `failPhase` at pkg/gateway/podlifecycle/podsession/binder.go:1072-1082 does log-and-continue on a failed `b.drain` (:1079-1081), so the cited fact under both tellings is true. The finding is duplication only, not a false citation.

USEFUL [standing context, prune 1 decision]: "contract-level accepted failure modes live in `spec-changes.md`'s Edge-cases section only ... The two tellings of the refused retry had drifted" — that precedent is what establishes a failure-mode double-telling as a reduction target in this proposal rather than as benign prose, and it is the ground the one filing stands on.

FACT: CODE-10's grep in the current text carries the round-9 addition `-e 'reports its outcome to the gateway'`; that is the ONLY delta from the r8 snapshot across all non-log files. A grep alternative list is not a rule statement, so it is outside this lens. — EVIDENCE: `diff -ru -x '*.review-log*.md' scratchpad/cp-snap/0081-opt2/non-spec-r8-prefix proposals/0081_*` returns one hunk, non-spec-changes.md:2163.

FACT: the two remaining sweep survivors at non-spec-changes.md:4184 and :4192 are the two "swept file enters no row in `slotAddressCaseFiles` and takes no `tests/spec-map.json` entry" bullets, which is the standing OPEN `[non-spec.7.review-applicability.1]` (per-file registration-rule restatements). Declined again here on the same ground: each reads as an application of the Testing preamble's blanket rule to its own grep-defined set rather than as a second normative home.

### [non-spec.10.review-test-coverage.1]

FACT: CODE-2 changes THREE confirmation call sites (`pkg/adapter/session.go:163`, `pkg/adapter/resume.go:144`, `pkg/adapter/sdkwarm.go:261`) and the Testing section names a deterministic case for exactly two of them — EVIDENCE: non-spec-changes.md:733-742 (the three sites), :3152-3155 (`StartSession` rollback case), :3267-3285 (SDK-warm case, which says in so many words "This case is the site's only coverage"). No listed case drives `Server.Resume` to a refused confirmation. Filed as this round's only finding.
WATCHOUT: non-spec-changes.md:3604 ("The `Resume` confirmation's own refusal predicate keeps its deterministic coverage in **The confirmation refuses a replaced entry** above") reads like a disposition for the Resume site, but that case (:3156-3158) exercises `noteRuntimeStarted` alone — the predicate, not the handler arm (`Runtime.Close`, `Aborted`, registry left alone, no `ReportSessionScrub`, no span categorisation). EVIDENCE: non-spec-changes.md:3156-3158 vs :738-740.
FACT: the shipped tree confirms the Resume arm is new code: `pkg/adapter/resume.go:144` is a bare `s.noteRuntimeStarted(sessionID)` with no result check today, exactly as `pkg/adapter/session.go:163` is.
FACT (checked and NOT filed, so nobody re-derives it): CODE-9's production wiring of the new `SlotReclaim` hook at `cmd/lenny-gateway/metricsbackfill.go:149` has no listed test and the proposal says so itself (non-spec-changes.md:2066-2070). Declined because the shipped sibling `w.podBinder.SlotFailure` at that same line has no wiring test either (`grep -rn podBinder cmd/lenny-gateway/*_test.go` is empty), so a gate here is a new standard rather than coverage the change requires.
FACT (checked and NOT filed): the tier-3 descriptor gate pins "the nine fields ... and both new `ErrorCode` values by number and name" (non-spec-changes.md:3468-3471) and does not pin the four values of the NEW `SlotReclaimOutcome` enum by number. `grep -n SlotReclaimOutcome schemas/lenny-adapter.proto` is empty today, so the enum is new. Declined as hardening: the tier-3 behavioural cases pin each outcome's semantics over the real transport, and `buf breaking` catches a later renumber once the values are landed.
FACT (checked and NOT filed): DOCS-4's clause deletion breaks no shipped gate. `TestPerSlotCleanupStatedOnEverySessionModeRow` asserts only the substring "Per-slot cleanup" inside the residual-state table (`tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:465-475`), and the edited sentences sit outside that table (`docs/reference/execution-modes.md:68`, `docs/operator-guide/security-principles.md:33`).
FACT: the `// spec: §4.7.1 (...)` form the Testing section mandates parses under the tier-0 inventory gate: `specTagRE` matches the tag anywhere in a comment line and the gate's own comment gives `// rejected. spec: §7.4` as a valid citation (`tests/tier0_static/spec_map_slot_address_registration_test.go:565-569`). A lens tempted to file the §-vs-bare-number form against `.claude/rules/test-coverage.md` stops here.
USEFUL [non-spec.7.review-test-coverage.1]: the Open it filed (DOCS-2's `DemoteSDK` row owes no tier-11 disposition) is still open in the live text — the tier-11 DOCS-2 paragraph (non-spec-changes.md:3738-3745) dispositions the `Shutdown` row, the bind-attempt paragraph and the `ReportSessionScrub` row and stays silent on the `DemoteSDK` row. NOT re-filed this round: the record stands and the open-decisions phase carries it.

### [non-spec.11.fix-G1.1]

DECISION: the per-slot cleanup budget is now CITED and never instantiated in non-spec-changes.md — BECAUSE its one home is spec/05 section 5.2 "Slot failure and cleanup", `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` at spec/05_runtime-registry-and-pool-model.md:545, and every worked number the two "Accepted failure modes" bullets carried was derived from yaml example lines in a spec file this proposal does not edit — ALTERNATIVES: re-anchoring the finding's suggested :508/:393 pair (third rewrite of the same restatement, same liability); moving the worked example into staged spec text (spec lane locked, and :545 already states it); hedging the figure ("roughly thirty seconds").

MISTAKE: rounds 9 and 10 each corrected the NUMBER inside the queue-head bullet's worked example and left the example standing; round 11 then filed the citations that grounded it. Two rounds and their verification agents were spent on a sentence whose correct form is a citation with no instantiation.

WATCHOUT: `spec/05_runtime-registry-and-pool-model.md:166` is NOT in section 5.2. Section 5.2 begins at :365; :166 sits inside section 5.1's `#### Derived Runtime` example (block 117-171, `name: research-pipeline`). :399 states no default; it is a yaml field line whose comment reads only "simultaneous sessions per pod; > 1 requires acknowledgeProcessLevelIsolation", and the same block sets `cleanupTimeoutSeconds: 60` at :412. The explicit default statement is :393; section 5.2's own `cleanupTimeoutSeconds: 30` is at :508, in the deployer-acknowledgment example. Earlier log FACTs at review-log.md:1477 and :1502 call :166 a section 5.2 example and call :399 the default statement; both are wrong on the section and on which line carries the default. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:365, :117, :166, :393, :399, :412, :508

CORRECTS [review-log.md:1477, review-log.md:1502]: see the WATCHOUT above. The arithmetic those entries record (30/1 = 30) is right; the section attribution and the default's line are not.

FACT: "cleanupTimeoutSeconds is optional, so an unset pool yields the 5s floor" survives ONLY in the staged CODE comment on `slotCleanupBudget` (non-spec-changes.md, "**The budget:**" block). It is true there as a statement about the Go function's zero input, verified in the tree by archive entry at review-log-archive.md:42137 (`CleanupTimeoutSeconds` is a bare `int` with no defaulting layer). The same phrase as a claim about a CONFIGURED pool is not supported: spec/05 states no default for `cleanupTimeoutSeconds`. Both prose carriers of it in the Accepted failure modes bullets are now deleted. Do not re-derive the phrase into prose, and do not file the code comment against the deleted prose.

FACT: no checklist, conformance, docs or code deliverable restates the per-slot cleanup budget, so deleting the two instantiations moved nothing else. Verified by grep for the :166/:399 anchors and for "unset pool" across the proposal directory.

### [non-spec.11.fix-design-G1.1]

DECISION: delete the worked numeric instantiation of the per-slot cleanup budget from BOTH accepted-failure-mode bullets rather than re-anchor its spec citations — BECAUSE the budget `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` has one home, spec/05 §5.2 `**Slot failure and cleanup**` (spec/05_runtime-registry-and-pool-model.md:545), and an instantiation of it in non-spec-changes.md is a restatement whose only content is a pool example the proposal does not own. Round 10 re-anchored it once and round 11 filed the re-anchor; a third re-anchor is the same answer one step smaller. ALTERNATIVES: (a) re-anchor to :508 and :393 as the finding suggests — rejected, it keeps a restatement alive whose every future spec-05 edit is a new finding; (b) inline the whole worked example with more qualifiers — rejected as hair.

FACT: `cleanupTimeoutSeconds` has NO stated default in spec/05. The only values are three yaml example lines (:166 in a §5.1 derived runtime, :412 in the §5.2 sessionPolicy reference block, :508 in the §5.2 deployer-acknowledgment block). EVIDENCE: `grep -n cleanupTimeoutSeconds spec/05_runtime-registry-and-pool-model.md`.

WATCHOUT: the phrase "five on an unset pool" appears in TWO bullets (the guarded-`Resume` bullet and the queue-head bullet) and is very likely wrong in both. If the reference block's `cleanupTimeoutSeconds: 60` (spec/05:412) is the default, the unset-pool budget is 60s, not 5s; 5s is only the floor reached when the quotient is under 5, which §5.2's CRD rule rejects at admission (spec/05:545). Nobody has filed this. The G1 design deletes the clause from both bullets, which removes the question rather than answering it. EVIDENCE: proposals/0081_*/....non-spec-changes.md near "five on an unset pool" (two occurrences); spec/05_runtime-registry-and-pool-model.md:412,545.

FACT: line citations into `spec/` are LEGAL inside `proposals/`. The line-citation ratchet excludes the directory: `const readExcludedPrefix = "proposals/"` at scripts/specshift/scope/scope.go:99. So channel-naming N8 is not the ground for removing them here; single-source is.

MISTAKE: round 10's fix corrected the `maxConcurrentSessions` figure in the illustration but carried the two bad line anchors (:166, :399) through unchecked. :166 is inside §5.1's `research-pipeline` derived-runtime example (block starts spec/05:117) and §5.2 starts at spec/05:365; :399 is a yaml field line with no default statement. Correcting a number inside a citation nobody re-resolved is how this location produced two consecutive rounds of findings.

### [non-spec.11.review-performance.1]

FACT: the round-10 fix to the queue-head budget figure checks out arithmetically and by citation — the §5.2 derived-runtime example sets `cleanupTimeoutSeconds: 30` (spec/05_runtime-registry-and-pool-model.md:166) and no `maxConcurrentSessions`, and the default is 1 (spec/05_runtime-registry-and-pool-model.md:399), so `max(30/1,5) = 30` seconds is right and the earlier "fifteen at maxConcurrentSessions: 2" was the defect — EVIDENCE: proposals/.../non-spec-changes.md:3910-3917
FACT: the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` bullet at spec/05:545 sits under the heading `**Slot failure and cleanup (maxConcurrentSessions > 1)**`, so applying it to a `maxConcurrentSessions: 1` pool looks out of scope at first read. It is NOT a defect: the staged §5.2 scrub-model append explicitly carries the bullet's content across the concurrency boundary ("That per-slot cleanup is the one the **Slot cleanup:** bullet below states, on a pod of either concurrency") and the staging says the formula "stands as written" — EVIDENCE: spec-changes.md:616, spec-changes.md:684-690. Do not file this; I checked it and it is licensed.
WATCHOUT: the bullet calls spec/05:166 "the §5.2 example pool". Line 166 is inside §5.1 (`### 5.1 Runtime`, spec/05:3; §5.2 starts at spec/05:365) and the example is a *derived runtime* manifest, not a pool. I did not file it: it is a section/object label on an illustrative figure in an accepted-failure-mode bullet, the number it supports is correct, and §5.2 carries equivalent examples (`cleanupTimeoutSeconds: 60` at spec/05:412, `: 30` at spec/05:508) so no conclusion moves. A round-12 fixer touching this sentence for any other reason should re-point it at spec/05:508 while there; a reviewer should not spend a finding on it.
FACT: no stale "fifteen" or `maxConcurrentSessions: 2` survives from the pre-fix wording of that bullet; the other `maxConcurrentSessions: 2` sites (spec-changes.md:91, summary.md:185,:271, non-spec-changes.md:3413) are the replacement-threshold and test-case discussions and are untouched by this figure — EVIDENCE: grep over proposals/0081_*/ for `fifteen|thirty|maxConcurrentSessions: 2`
FACT: the round-10 test-text citations all resolve: `pkg/adapter/session.go:163` and `pkg/adapter/resume.go:144` are both the `s.noteRuntimeStarted(sessionID)` call, `pkg/adapter/resume.go:169-172` is `restoreChunks`'s empty-set guard, and `probeRuntime` is declared at `pkg/adapter/slotsession_test.go:33-38`.
FACT: the round-10 hunks add no new control-plane or data-plane write. The Start-versus-reclaim rollback rewrite is test text, CODE-8's residue paragraph became a pointer at an existing accepted-failure-mode bullet, and the `onStart` fixture hook is test-only. Nothing this round changes the etcd, Postgres or Redis write rate, adds a watch, or adds a serialization point, so the performance lens returned empty.

### [non-spec.11.review-single-source.1]

FACT: spec/05 section boundaries — §5.1 Runtime runs lines 3-364, §5.2 Pool Configuration and Execution Modes starts at line 365, §5.3 at 666. The `cleanupTimeoutSeconds: 30` at spec/05_runtime-registry-and-pool-model.md:166 is inside §5.1's `#### Derived Runtime` example (`name: research-pipeline`, block 120-171), NOT a §5.2 pool. §5.2's own `cleanupTimeoutSeconds: 30` example is the deployer-acknowledgment block at :508 (no `maxConcurrentSessions`, so default 1 → a 30s per-slot budget, which is the figure the queue-head bullet wants). The §5.2 sessionPolicy reference block (:397-419) pairs `maxConcurrentSessions: 1` (:399) with `cleanupTimeoutSeconds: 60` (:412) → 60s, so citing :399 for the example is a different pool from the one at :166. — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:365, :166, :508, :399, :412
FACT: the explicit statement that `maxConcurrentSessions: 1` is the DEFAULT is spec/05_runtime-registry-and-pool-model.md:393 ("In the default configuration (`maxConcurrentSessions: 1`, `recycle.enabled: false`)"). Line 399 is a yaml field line whose comment says only "simultaneous sessions per pod; > 1 requires acknowledgeProcessLevelIsolation" and never says "default". — EVIDENCE: spec/05_runtime-registry-and-pool-model.md:393
FACT: round-10 hunk citations all check out — `probeRuntime` at pkg/adapter/slotsession_test.go:33 (struct) through :38 (`Start`), `noteRuntimeStarted` at pkg/adapter/session.go:163 and pkg/adapter/resume.go:144, the `restoreChunks` empty-set guard at pkg/adapter/resume.go:169-172, the `drain`-failure log at pkg/gateway/podlifecycle/podsession/binder.go:1079-1081, and exactly five `ReleaseSlotForTest` call sites in the tree (integrationlevel_test.go:84, credexpiry_test.go:320, checkpoint_stream_test.go:1012, one_session_only_test.go:77, tracingcontext_addressing_test.go:425).
DECISION: did NOT file the twice-stated conversation-only-resume construction (non-spec-changes.md:3170-3173 tier-1 row and :3617-3621 tier-7a arm, both writing out "session id + checkpoint id + no chunks → `restoreChunks` empty-set guard `resume.go:169-172` → no extraction") — BECAUSE they are two distinct test cases each stating its own fixture setup rather than one rule with a home, and the two agree today — ALTERNATIVES: filing it under (g); rejected as below the bar, but if a later round touches either sentence, reducing the tier-1 row to a citation of the tier-7a statement (or vice versa) is the cheap fix, since the duplicated `resume.go:169-172` anchor is the only drift surface.
DECISION: did NOT file the CODE-1 paragraph (:1828-1831) / files-touched (:4170-4176) restatement of the `Resume` row's already-cancelled context — BECAUSE both defer the reason to the test case ("for the reason that case states"), which is a naming-and-citing site, not a stating site under this lens.
FACT: the CODE-8 residue reduction landed clean — CODE-8 (:2039-2040) now cites the accepted-failure-mode bullet by its exact bold subject **A pod whose drain failed keeps a dead attempt's token**, which exists at :3885, and the bullet carries everything the deleted copy stated including the reaper pointer.

### [non-spec.11.review-test-coverage.1]

DECISION: returned no findings for round 11 — BECAUSE the whole diff is six hunks in non-spec-changes.md and every one of them either adds test coverage or adjusts rationale prose that carries no test obligation — ALTERNATIVES: filing on the reduced CODE-8 residue pointer (rejected: the accepted-failure-mode bullet at :3885-3889 is the intact home and the residue needs no test), and filing on the queue-head latency figure (rejected: a derived rationale number, no behaviour, no test obligation)

FACT: the round-10 fixers closed the previously-filed CODE-2 Resume-rollback test gap by turning the **Start-versus-reclaim rollback, deterministic form.** bullet into a two-row table (StartSession `pkg/adapter/session.go:163`, Resume `pkg/adapter/resume.go:144`); both cited `noteRuntimeStarted` call sites are exactly those lines — EVIDENCE: proposals/.../non-spec-changes.md:3155-3173, pkg/adapter/session.go:163, pkg/adapter/resume.go:144

FACT: `releaseSessionSlot` files no `ReportSessionScrub` — it is `deregisterSlot` + `removeSlotTree` + `cancelPodMCPIfRuntimeIdle` and nothing else, so the rollback case's "neither the rollback nor the removal files a `ReportSessionScrub`" assertion is satisfiable through `ReleaseSlotForTest` — EVIDENCE: pkg/adapter/slotsession.go:213-219

FACT: the `probeRuntime` fixture the new `onStart` hook extends has only an `onClose` hook today and its `Start` is a bare `return nil`, so the cited insertion point is right — EVIDENCE: pkg/adapter/slotsession_test.go:33-38

FACT: `restoreChunks`'s empty-set guard is at pkg/adapter/resume.go:169-172, as the Resume row and the tier-7a paragraph both cite; a conversation-only Resume therefore reaches `Runtime.Start` with no extraction — EVIDENCE: pkg/adapter/resume.go:169-172

FACT: the five `ReleaseSlotForTest` call sites the CODE-6 paragraph enumerates are exactly the five the tree holds (integrationlevel_test.go:84, credexpiry_test.go:320, checkpoint_stream_test.go:1012, tracingcontext_addressing_test.go:425, one_session_only_test.go:77); the round-10 hunk correctly scopes that claim to "the tree holds today" and names the new slotsession_test.go caller separately — EVIDENCE: pkg/adapter/export_test.go:32, non-spec-changes.md:1826-1830 and :4170-4176

WATCHOUT: the Resume row's cancelled-context device is not a test-coverage question but it is the first thing a reader trips on. It is consistent with the staged guard contract: `Resume` holds its own slot guard for the whole call (derivation table, non-spec-changes.md:1801), so a same-goroutine `ReleaseSlotForTest` must not block, and the guard-acquiring form's documented fall-through removes unguarded on an expired acquisition (non-spec-changes.md:1832-1834), which the destructive-expiry case at :3040-3051 already pins. Do not re-file it as an untested path.

### [non-spec.12.review-single-source.1]

FACT: round 12's diff against the r11 snapshot is two hunks only, both in the accepted-failure-modes bullets of non-spec-changes.md (~:3890-3915), both DELETIONS of derived latency figures ("five on an unset pool", the thirty-second §5.2-example derivation). No identifier, rule number, field, section name or citation was added, renamed or re-scoped, so step 2 of the round protocol had nothing to chase. — EVIDENCE: proposals/0081_.../0081_....non-spec-changes.md:3890-3915
FACT: the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula appears at five proposal sites and this is NOT a (g) copy. Its normative home is the shipped §5.2 `**Slot cleanup:**` bullet, which the staging leaves untouched and says so (spec-changes.md:688-690, "its trigger, the ... formula, and the CRD validation rule all stand as written"). The five sites are: the `slotCleanupBudget` doc comment, which is the code home and cites §5.2 (non-spec-changes.md:952-959); summary.md:176, a deliverable description; spec-changes.md:689, a no-change note on existing spec text; and non-spec-changes.md:3893 and :3911, rationale clauses inside accepted-failure-mode analysis. None of them states a rule a reader would implement from that site alone. Do not re-file this. — EVIDENCE: proposals/0081_.../0081_....spec-changes.md:689, .non-spec-changes.md:952-959
FACT: the deletions left no stale derived figure anywhere. `grep -rn "unset pool\|five-second\|thirty\|30 second"` across the proposal returns only the code comment's own 5s-floor note (non-spec-changes.md:957), the floor clauses at :1046 and :3913, and the queue bound at :3919, which is a different number with its own true citation (`DefaultMaxQueueWaitSeconds = 30`, pkg/gateway/sessionserver/queue.go:18). — EVIDENCE: pkg/gateway/sessionserver/queue.go:14-18

### [non-spec.13.fix-G1.1]

DECISION: closed the attempt-scoped release's missing surface by widening `podsession.CredentialAssigner` with `Release(leaseID string)`, stated once in CODE-4's `**The lease release is scoped to the attempt.**` paragraph — BECAUSE both production implementations already declare it (`credassign.Service.Release` credassign.go:380, `credassign.Client.Release` client.go:299) and `AssignProto` already returns the identifier on the wire lease, so the widening costs no production code and threads no new value — ALTERNATIVES: a `leaseReleaser` type assertion inside `releaseAttemptCredentials` (rejected: compiles against every fake by doing nothing, making the tier-1 released-identifier assertion vacuous on exactly the fakes the tests use); a second `Binder.CredentialReleaser` field (rejected: parallel wiring surface for one method on the same service); release closures from `assignSlotCredentials` (rejected: still needs a by-identifier surface).

DECISION: closed the fake blast radius with a grep-defined sweep row in `## Files touched on application` rather than enumerating the files in the Testing list — BECAUSE the proposal already closes two mechanical edit sets that way (the `ShutdownRequest{` and the `BindAttempt` literal sweeps) and an enumeration is a second home that goes stale as fakes are added.

FACT: the fake set for `podsession.Binder.Credentials` is defined by `grep -rn "ReleaseSession(" --include=*_test.go pkg/ cmd/ tests/` intersected with types assigned to that field. `fakeExtendAssigner` (cmd/lenny-gateway/cred_renewal_extend_wiring_test.go:128) already declares `Release` and takes no edit; `credassign_test.go`'s hits are calls on the real service, not fakes. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:91, :319-332.

WATCHOUT: a whole-file credit at a PARENT spec section never satisfies the tier-0 slot-address credit gate when the map declares the child. `creditKey` walks up from the cited id and stops at the FIRST declared id, and `tests/spec-map.json` declares `4.7.1`, so a case citing §4.7.1 needs a `4.7.1` credit and a `4.7` credit does not match. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:911-924 and :841-846.



### [non-spec.13.fix-G2.1]

DECISION: closed the guarded-`Resume` accepted-failure bullet by reduction rather than by a code change — the bullet now states one fact of its own (the compensation's budget is the reclaim RPC's deadline, so the handler's guard acquisition expires with it) and cites three homes for everything else: CODE-6's **No guard acquisition outlives its caller's context** for the expired-acquisition disposition, CODE-6's *Lock order.* paragraph for the unordered `removeSlotTree`-against-`ExtractTree` residue, and CODE-1's completion predicate for the identifier release — BECAUSE the deleted clause was an acceptance rationale that rejected an alternative for producing exactly the interleaving the staged path produces, so no narrowing of it could be true — ALTERNATIVES: an early return in CODE-1's removing arm when `guarded` is false (reverses CODE-6's settled disposition and turns the staged tier-7a table-driven case red); gating `completed` on `guarded` (splits the one-per-site completion predicate and holds the identifier for the pod's life on every expired acquisition); deleting the bullet (loses the only record that the compensation can burn its whole budget behind a restore).

FACT: line numbers in the G2 design's citations had already drifted under G1's edits in the same round. CODE-4's `rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)` is at non-spec-changes.md:1004 (design said :1022-1025), CODE-1's `completed = closeErr == nil && treeErr == nil` at :443 (design said :437), CODE-6's lock-order residue at :1846-1849 (design said :1762-1764). Cite CODE-6 and CODE-1 by their bold or italic heading rather than by line, which is what the edit does.

WATCHOUT: do not write that this path admits a successor bind onto a partially restored foreign workspace. A successor's `PrepareWorkspace`, `FinalizeWorkspace` and `RunSetup` take `acquireSlotGuardForResolve` and serialize behind the `Resume` that still holds the guard to the end of its call; `AssignCredentials`, `StartSession` and `ConfigureWorkspace` take no guard and do no path work outside `s.mu`. The narrower truth the bullet now carries is that the identifier is freed while the extraction is still writing under it — EVIDENCE: non-spec-changes.md CODE-6 guard-placement paragraph, non-spec-changes.md:443.

WATCHOUT: the surviving rejected alternative in that bullet (a second adapter-side deadline over the restore states the gateway's own timeout in a second place) is deliberate and was kept verbatim; a later round that "completes" the bullet by re-adding a guard-release alternative re-introduces the contradiction this round removed — EVIDENCE: non-spec-changes.md, the bullet "A compensating `Shutdown` can spend its budget waiting on a guarded `Resume`."

### [non-spec.13.fix-G3.1]

DECISION: DOCS-4's closing tier line now reads `Tiers: 0, 11.`, matching checklist S23 — BECAUSE the deliverable edits two pages under `docs/` and `.claude/rules/test-coverage.md` maps a documentation change to tier 11; the neighbouring docs-touching step S24 also declares `Tiers 0, 11.` — ALTERNATIVES: editing S23 down to tier 0, rejected as the wrong side of the disagreement.
FACT: tier 11 does read `docs/reference/execution-modes.md`, but only its residual-state table rows, so DOCS-4's "No gate reads either sentence" clause stays true alongside the tier-11 declaration — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:443-445 (`perSlotCleanupTables` entry) and :462 (`TestPerSlotCleanupStatedOnEverySessionModeRow`).
FACT: no site other than the DOCS-4 block and checklist S23 states DOCS-4's tier list; the summary's DOCS-4 line and the spec-changes carrier-table rows name the deliverable without tiers, so this correction has no cascade — EVIDENCE: grep for `DOCS-4` across the proposal directory.
WATCHOUT: line numbers in this round's designs were quoted before G1 and G2 landed, and every anchor past non-spec-changes.md:1000 has drifted; the DOCS-4 closing line moved from 2899 to 2904 — EVIDENCE: non-spec-changes.md:2903-2904.

### [non-spec.13.fix-G4.1]

CORRECTS [review-log-archive.md:18616, :22023, :26363, :27970, :28094, :28122, :28430, :50254]: every one of those FACT entries declares "three of S-2's covered handler files (`session.go`, `slotcreds.go`, `sdkwarm.go`)" exact. It is an undercount. Each entry checked only that the UNCOVERED adapter files (staging.go, slot.go, slotsession.go, resume.go, holdstate.go, server.go, runtimegeneration.go) sit outside S-2's list, and none checked `credentials.go`, which is both on S-2's covered list (gateway-runtime-comms-remediation.md:1886) and opened by CODE-6. What is true: the proposal opens `session.go`, `credentials.go`, `slotcreds.go` and `sdkwarm.go` from that list, and that enumeration is exhaustive.

USEFUL [review-log-archive.md:40154]: the DEFERRED entry there is the only archived reading that got this right, and it named both the covered-list line and the files-touched site, which is what made the verification cheap.

FACT: the exhaustiveness check is a grep for the remaining S-2 covered names against `## Files touched on application (non-spec)`. `lifecycle.go`, `checkpoint.go`, `coordination.go`, `attach.go` and the two renamed channel files (`adapterevents.go`, `runtimeops.go`) are absent from that section; `adapterevents.go` appears only as a prose citation. — EVIDENCE: non-spec-changes.md:772 (the lone citation), gateway-runtime-comms-remediation.md:1885-1888

DECISION: dropped the count word and named the four files instead of writing "four". — BECAUSE the enumeration is exhaustive, so it carries itself, and a count is what went stale here in the first place. — ALTERNATIVES: writing "four of S-2's covered handler files", rejected because the next deliverable that opens `lifecycle.go` or `attach.go` restages exactly this finding.

WATCHOUT: the sentence lives at three sites, word for word identical, in summary.md (the S-2 second-window bullet and the gateway-runtime-comms remediation R1b impact row) and in problem-statement.md (the S-2 window paragraph). Grep `S-2's covered list` before changing any one of them; correcting fewer than all three is the drift this round was filed for. — EVIDENCE: summary.md and problem-statement.md, one hit each per site

### [non-spec.13.fix-design-G1.1]

DECISION: close the `Binder.Credentials` gap by widening `CredentialAssigner` with `Release(leaseID string)` stated ONCE in CODE-4's `**The lease release is scoped to the attempt.**` paragraph, plus ONE grep-closed sweep row in `## Files touched on application` for the test fakes — BECAUSE both production implementors already have the method (`credassign.Service.Release` credassign.go:380, `credassign.Client.Release` client.go:299), so the widening costs no production code, and the proposal's own convention for mechanical test-edit sets is a grep-defined row (it already has two). ALTERNATIVES: (a) a `leaseReleaser` type assertion in `releaseAttemptCredentials` — rejected, optional-capability hair that no-ops silently on every fake and makes the tier-1 identifier assertion vacuous; (b) a second `Binder.CredentialReleaser` field — rejected, parallel wiring surface for one method; (c) enumerating the twelve fake files in the Testing test-file list — rejected, second home for a set the grep already closes.

FACT: twelve types are assigned to a `podsession.Binder.Credentials` field and none declares `Release`; the only test fake in the tree that already has `Release(string)` is `fakeExtendAssigner` (cmd/lenny-gateway/cred_renewal_extend_wiring_test.go:127), which satisfies the renewal interface, so the sweep row must exempt a type that already declares one. EVIDENCE: `grep -rn "ReleaseSession(" --include=*_test.go pkg/ cmd/ tests/` — pkg/gateway/podlifecycle/podsession/binder_test.go:322, pkg/gateway/sessionserver/start_pod_test.go:1160, pkg/gateway/sessionserver/delegated_child_materialize_test.go:132,:143, pkg/gateway/sessionserver/terminal_reclaim_internal_test.go:33, tests/tier9_security/credential_delivery_gate_test.go:80, tests/tier9_security/delegation_child_materialization_cred_test.go:121, tests/tier4_integration/eager_claim_lifecycle_test.go:121, cross_environment_delegation_test.go:523, delegation_child_materialization_test.go:126.

CORRECTS [review-log.md:434 and :1295, and review-log-archive.md:41257]: the standing WATCHOUT says an earlier round "deliberately declined to file" the `CredentialAssigner` widening because "both files that need the edit are already in Files-touched". That ground is false: eleven further types outside `binder_test.go` are assigned to `Binder.Credentials`, and the widening itself appears in no edit list. The archive entry's instruction "do not re-derive it and do not file it" should not be honoured by a later round; round 13 filed it and it is being fixed.

WATCHOUT: checklist step ordering. CODE-8's `Binder.Prepare` credential-assignment arm calls `b.releaseAttemptCredentials(minted)`, but the checklist puts CODE-8 at S12 and CODE-4's release at S19, and S12's text does not mention the arm. With the interface widening now explicit, whichever step lands the helper must also land the widening and the fake sweep or tier 0 goes red mid-step. The design puts all of it in S19 with one clause; a fixer that leaves the clause out leaves the sweep with no step. EVIDENCE: implementation-checklist.md:39 (S12), :53 (S19).

FACT: the tier-0 credit gate keys on the exact cited id when the map declares it. `creditKey` walks up from the cited id and stops at the first declared id, and `tests/spec-map.json` declares `4.7.1`, so a whole-file credit under `4.7` never satisfies a case citing §4.7.1. EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:911-924, :841-846; `tests/spec-map.json` declares 4.7, 4.7.1, 4.9 and 5.2.

### [non-spec.13.fix-design-G2.1]

DECISION: the guarded-`Resume` bullet's tail is fixed by turning two restatements into citations and DELETING a false justification, not by re-describing the path — the bullet keeps its gateway-side sentence (nothing answered in time, RPC error, leaked disposition, held slot-counter occupancy, all true and gateway-side), gains one fact only this bullet knows (CODE-4 sends the reclaim under `rctx` with the budget as the deadline, so the adapter handler's own context and therefore `lockSlotGuard`'s acquisition expire at the same instant the gateway gives up), and then CITES CODE-6's expired-acquisition disposition and CODE-6's lock-order residue and CODE-1's `completed` predicate rather than restating any of them — BECAUSE the defect is an acceptance rationale that rejects an alternative ("releasing the guard before the restore's remainder re-opens the interleaving of `removeSlotTree` against `ExtractTree`") for producing exactly the interleaving the staged path itself produces on this path — ALTERNATIVES: (a) an early return on `!guarded` in CODE-1's removing arm, rejected: it reverses CODE-6's settled disposition and turns red the staged tier-7a case at non-spec-changes.md:3040-3049, and leaves a tree with no entry; (b) an adapter-side deadline over the restore, rejected and already rejected in the bullet's surviving clause; (c) deleting the bullet, rejected: the wait is a real consequence of CODE-6's `Resume` guard span and this section (`:3833`) declares itself the home of code-level accepted failure modes.

FACT: the compensating `Shutdown`'s budget IS the adapter handler's deadline. CODE-4 builds `rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)` and sends `deadline_ms = budget/2` — EVIDENCE: non-spec-changes.md:1022-1025. So on this path the guard acquisition never survives the gateway's own give-up instant; there is no window in which the handler waits on longer than the gateway does.

FACT: during a parked `Resume`, `st.started` is ALREADY TRUE (set in `claimSessionSlotUnderLock`, and `Resume` claims at `pkg/adapter/resume.go:50` while `workspace.ExtractTree` runs at `:206`, well before `s.Runtime.Start` at `:140`), so the reclaim's `started` gate is taken and `Runtime.Close` runs on an already-expired context via `contextWithGraceDeadline` (`pkg/adapter/session.go:327-332`). Whether `completed` is reached therefore depends on that close's error, and `SocketRuntimeProcess.Close` returns nil on several arms (`pkg/adapter/socketruntime.go:435-447`) — EVIDENCE: pkg/adapter/resume.go:50,140,206. Write the hold-release residue conditionally ("when the close and the removal both return nil"), never as unconditional.

WATCHOUT: do not write that this path admits a successor onto a partially restored foreign workspace. A successor's tree-touching RPCs (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`) take `acquireSlotGuardForResolve` and the parked `Resume` holds that same guard to the end of its call (guard table, non-spec-changes.md:1800), so they serialize behind it; `AssignCredentials`, `StartSession` and `ConfigureWorkspace` take no guard but do no path work outside `s.mu`. The accurate residue is narrower: the slot identifier is freed while the extraction is still writing under it — EVIDENCE: non-spec-changes.md:1800-1807.

FACT: "the pod holds that slot's counter occupancy" in this bullet is TRUE and is gateway-side, following from the leaked disposition the RPC error sets, independently of what the adapter did (`slotbinder.go:543` expression, non-spec-changes.md:465-469; summary.md:919-922). The finding's framing lumps it with the false clause; keep the sentence.

OPEN: the residue this bullet will now record (an unordered `removeSlotTree` against a live `ExtractTree`, plus an identifier released while the extraction writes) is materially worse than the residue it recorded before. Whether it is still ACCEPTABLE, or whether the extraction should be made interruptible so the parked `Resume` returns inside the budget, is a human question. Not filed as an open decision by this shard, because the remedy is a mechanism change rather than staged text being wrong on its own terms.

### [non-spec.13.fix-design-G3.1]
DECISION: fix DOCS-4's closing tier list to `Tiers: 0, 11.` and leave checklist S23 as written — BECAUSE the deliverable edits two `docs/` pages and `.claude/rules/test-coverage.md` maps a documentation change to tier 11; S23 already says `Tiers 0, 11.` and S24 (the neighbouring docs-touching step) says the same — ALTERNATIVES: editing S23 down to `Tiers 0.` (rejected: it would make the docs step the only doc-editing step in the checklist claiming no tier 11, and the tier-11 suite does read `docs/reference/execution-modes.md`).
FACT: tier 11 does read the page DOCS-4 edits, but not the sentence it edits: `perSlotCleanupTables` in tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:444-445 registers `docs/reference/execution-modes.md` and matches only residual-state table rows. DOCS-4's own sentence "No gate reads either sentence" therefore stays true and must be kept alongside the corrected tier list — the two statements are not in conflict, so do not delete one to "resolve" the other.
WATCHOUT: the `// diagnosis:` comment of `TestPerSlotCleanupStatedOnEverySessionModeRow` (tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:457-462) still carries the clause "and the adapter reports its outcome to the gateway" that SPEC-3 withdraws. That is a separate finding against the staged CODE/test set (or an unstaged tree site), not part of G3, and G3's edit does not make it wrong.


### [non-spec.13.fix-design-G4.1]
DECISION: the S-2 sentence becomes "it opens four of S-2's covered handler files (`session.go`, `credentials.go`, `slotcreds.go`, `sdkwarm.go`)" — actually: drop the count word and enumerate the four — at all three live sites (summary.md:199, summary.md:1123, problem-statement.md:229-230). BECAUSE `pkg/adapter/credentials.go` is on S-2's covered list (gateway-runtime-comms-remediation.md:1884-1885) and CODE-6 opens it (non-spec-changes.md:1381 heading, :4062 files-touched entry). ALTERNATIVES: fixing only the two summary sites (leaves problem-statement.md contradicting the summary); restating S-2's whole nine-file list at each site (restatement, not a citation).
FACT: the exhaustive set of S-2 covered files this proposal opens is exactly four — session.go, credentials.go, slotcreds.go, sdkwarm.go. Checked every S-2 covered name (lifecycle.go, checkpoint.go, coordination.go, attach.go, adapterevents.go, runtimeops.go) against `## Files touched on application (non-spec)` (non-spec-changes.md:3959-4140): none of the other six appears; adapterevents.go is cited in prose at non-spec-changes.md:772 but not edited. — EVIDENCE: non-spec-changes.md:4086-4110
MISTAKE: at least six archived FACT entries (review-log-archive.md:18616, :22023, :26363, :27970, :28094, :28122, :28430, :50254) assert the "three" count is exact. Each verified only that the *uncovered* adapter files (staging.go, slot.go, slotsession.go, resume.go, holdstate.go, server.go, runtimegeneration.go) are outside S-2's list, and none checked credentials.go, which is both covered and opened. Entry :40154 caught it correctly and filed it as DEFERRED. Do not read the older entries as settling this.
WATCHOUT: the count sentence lives in three files, one of which (problem-statement.md) no other finding opens; a partial fix makes the summary contradict the problem statement. — EVIDENCE: problem-statement.md:229
FACT: the root planning doc /home/ec2-user/lenny/gateway-runtime-comms-remediation.md only enumerates S-2's covered set and makes no claim about how many 0081 opens, so it needs no edit. — EVIDENCE: gateway-runtime-comms-remediation.md:1883-1890

### [non-spec.13.review-applicability.1]

FACT: The checklist simulates cleanly end to end. 26 steps, 0 checked boxes, every `Depends-on`
names an earlier step only, every staged deliverable (SPEC-1..6, DOCS-1..4, CODE-1..12, CONF-1,
SCHEMA-1) is claimed by at least one step, each step carries one lane, and the spec block S1..S6
leads. — EVIDENCE: implementation-checklist.md:17-68

FACT: I ran both comment-carrier commands from the repo root and both execute and return a set.
CODE-10's grep returns 47 hits across 26 files; CODE-12's perl one-liner returns 20 hits.
— EVIDENCE: non-spec-changes.md:2161-2163, :2226-2228

FACT: CODE-10's tier-11 hits are three files, not two: `basic_level_echo_stamp_...:18,458,473`,
`concurrent_slot_lifecycle_...:170,173,174` and `session_scrub_report_addressing_...:6,21`. The
non-carrier arm's exclusion "the two tier-11 files under checklist S4's sweep" resolves against
the SPEC-3 carrier table, which names `spec_28_register_writers_test.go` and
`concurrent_slot_lifecycle_doc_reconciliation_test.go` — neither of the other two. So
`basic_level_echo_stamp_...:458` ("and the adapter reports its outcome to the gateway") and
`session_scrub_report_addressing_...:6` stay inside CODE-10 and are dispositioned by its arm
rule. I chased this as a candidate survival-of-a-live-carrier finding and it does NOT hold.
— EVIDENCE: spec-changes.md:609,610; non-spec-changes.md:2126-2132;
tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:458

FACT: `tests/tier0_static/claim_register_test.go` does not check that a row's `surface` names an
existing file; it checks only that a WIRED row names a non-bare-line surface, that a non-WIRED
row names a plan step, and that the anchor resolves. So the SCHEMA-1 WIRED row naming
`pkg/gateway/podlifecycle/podsession/bindattempt.go` (a file S13 creates, after S9) breaks no
gate and is not a forward-reference finding. — EVIDENCE: tests/tier0_static/claim_register_test.go:275-282

FACT: `spec161Metrics` is a hand list inside `pkg/observability/metrics/catalog_test.go`,
reconciled only against `MetricCatalog()`, never read from spec/16. Landing S6's §16.1 rows ahead
of S10's catalog entry therefore leaves no window where a gate is red. Likewise
`runtime_page_metric_names_resolve_test.go` is one-way (page → §16.1) and says so in its own
header. — EVIDENCE: pkg/observability/metrics/catalog_test.go:186-211;
tests/tier11_docs/runtime_page_metric_names_resolve_test.go:11-17

WATCHOUT: `runtimeHoldsLocked` looks like a forward reference (S17 consumes it; the files-touched
entry for `runtimegeneration.go` lists it alongside CODE-2's `noteRuntimeStarted` work, which is
S18). It is not: the accessor is an explicit CODE-1 target, and S17 is a CODE-1 step, so it is
created and consumed in the same step. — EVIDENCE: non-spec-changes.md:182-186 vs :4075

FACT: S-2's covered-file list in the remediation plan has nine entries; this proposal opens FOUR
of them (`session.go`, `credentials.go`, `slotcreds.go`, `sdkwarm.go`), not the three the summary
enumerates. `resume.go`, `staging.go`, `slot.go`, `slotsession.go`, `holdstate.go` and `server.go`
are outside S-2's list. — EVIDENCE: gateway-runtime-comms-remediation.md:1885-1887

OPEN: SCHEMA-1's "**Claim register.** Three rows ... in the same commit" and the files-touched
entry both put all three rows in the schema step, while checklist S9 names two and S22 names the
third. Nothing breaks either way (the generator is re-run per commit), but the two statements
cannot both be followed. Filed this round.

### [non-spec.13.review-citations.1]

FACT: Round 13's citation lens swept EVERY concrete `path:line` citation in all four proposal files
mechanically (script extracting `(dir/)+file.(go|md|proto|json|yaml|sql|py):N(-M)`), resolved each
against the tree, and printed the cited text beside the proposal line that cites it. 107 distinct
cites in non-spec-changes.md, 107 in summary.md, 5 in spec-changes.md, 0 in the checklist. Zero false
citations found. Empty findings list is the result of a completed sweep, not an abandoned one.
EVIDENCE: method reproducible from /home/ec2-user/lenny (see the script form in the round-13 transcript).

FACT: The four spec-line citations in non-spec-changes.md all resolve exactly:
spec/05_runtime-registry-and-pool-model.md:440 is the `**onPoolExhausted**` paragraph carrying
"`WARM_POOL_EXHAUSTED` with a `Retry-After` header"; :545 is the `**Slot cleanup:**` bullet carrying
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)`; spec/16_observability.md:186-189 are the four
§16.1 rows that carry the adapter scrape deferral; spec/28_communication-channels.md:163 is §28.4's
"Every normative statement this section makes ... carries a row in the claim register".
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:440,545; spec/16_observability.md:186-189;
spec/28_communication-channels.md:163.

FACT: Every verbatim quote the proposal stages a replacement over is byte-accurate in the tree. Checked:
the four `slotstate.go` constant docs and the six edge-list glosses (pkg/sandbox/slotstate/slotstate.go:34-47,
:99-104); `registry.go` MarkLeaked (pkg/sandbox/slotstate/registry.go:93-95); slothealth `event` and
`RecordLeak` (pkg/gateway/runtime/slothealth/slothealth.go:33-34, :110-112); the two sessionserver gauge
comments (pkg/gateway/sessionserver/sessionserver.go:419, :1667); the two gatewaymetrics_credential
comments (:72, :220); the DOCS-4 clause ", and the adapter reports its outcome to the gateway" on
docs/reference/execution-modes.md:68 and docs/operator-guide/security-principles.md:33 and its absence
from docs/operator-guide/multi-tenancy.md; the four DOCS-3 sentences and the remedy cell at
docs/reference/error-catalog.md:129; the three DOCS-1 table rows and the four pod-state-machine spans at
docs/reference/state-machines.md:138,:235,:237,:251.

FACT: SCHEMA-1's field-number table is exact against schemas/lenny-adapter.proto as the file stands.
Verified by parsing each message body: PrepareWorkspaceRequest 1-3 used + `reserved 4`/`"slot_id"`;
FinalizeWorkspaceRequest 1-4 used (mid_session is 4) + reserved 5; RunSetupRequest 1-3 + reserved 4;
AssignCredentialsRequest 1-2 + reserved 3; ResumeRequest 1-5 and 7-14 + reserved 6 and 15;
ShutdownRequest 1-3, 5 recycle, 6 coordination_generation + reserved 4; ShutdownResponse 1-2.
`ErrorCode` runs to 27 (ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE = 27, schemas/lenny-adapter.proto:585),
so 28 and 29 are free as claimed.

FACT: The summary's claim-register arithmetic is exact. tests/claim-map.json holds 76 rows today:
32 WIRED, 24 UNWIRED, 20 ABSENT. The stated post-SCHEMA-1 figures (79 / 34 / 24 / 21) follow.
EVIDENCE: tests/claim-map.json, counted by status.

FACT: Every commit hash the summary's impact table cites resolves and its subject matches the claim:
f37e867b8 (message scope, 0073 SPEC-7), 3997f502b2 (coordinator-hold termination, 0073), 8f1083b70,
9589aea54, 57427ee5f, a5bf9db26, 8364087f9.

WATCHOUT: `pkg/adapter/export_test.go`'s `ReleaseSlotForTest` has exactly five callers today —
integrationlevel_test.go:84, credexpiry_test.go:320, checkpoint_stream_test.go:1012,
tracingcontext_addressing_test.go:425, one_session_only_test.go:77 — and none in slotsession_test.go.
CODE-6's claim "Those five are every call site the tree holds today" is correct as it now stands; an
earlier round filed a finding against a version of that list that wrongly included slotsession_test.go
and it was fixed. Do not re-derive.
EVIDENCE: pkg/adapter/export_test.go:32-35 and `grep -rn ReleaseSlotForTest pkg/adapter`.

FACT (minor, below the bar, recorded so nobody re-files it): summary.md's 0078 impact row cites
`tests/tier4_integration/concurrent_delegation_proxy_test.go:168` as "whose cleanup ... 0078's TEST-5
converts". The file's only `t.Cleanup` is at :170; :168 is `rt.AcceptTimeout = 15 * time.Second`. Two
lines of drift on an otherwise accurate claim — the cleanup exists, is the one 0078 converts, and the
meaning is unchanged. Excluded by the rubric's off-by-a-few clause. Do not file it.
EVIDENCE: tests/tier4_integration/concurrent_delegation_proxy_test.go:168-170.

DECISION: Returned an empty findings list — BECAUSE every citation class this lens owns (file:line,
spec section attribution, quoted spec/code/doc text, attributed behaviour, proto numbering, register
counts, commit hashes, data-flow direction read out of the reconciler code) checks out against the tree.
ALTERNATIVES: filing the two-line drift above, rejected under the off-by-a-few clause; filing
`client.go:860-867` for ShutdownRecycle whose body ends at :866, rejected for the same reason.

### [non-spec.13.review-client-surface.1]

FACT: SCHEMA-1's nine field numbers all check out free against the tree as it stands — PrepareWorkspaceRequest 1-3 used + 4 reserved, FinalizeWorkspaceRequest 1-4 + 5 reserved, RunSetupRequest 1-3 + 4 reserved, AssignCredentialsRequest 1-2 + 3 reserved, ResumeRequest 1-5/7-14 with 6 and 15 reserved, ShutdownRequest 1-3/5/6 with 4 reserved, ShutdownResponse 1-2. `ErrorCode` does end at 27. EVIDENCE: schemas/lenny-adapter.proto:682-697, :706-731, :846-857, :1021-1031, :1333-1412, :1609-1636, :1665-1668, :555-585

FACT: the only closed proto field set over a message SCHEMA-1 opens is the one SCHEMA-1 already names. I swept every descriptor pin under tests/tier3_contract/: adapter_generation_fence pins only `coordination_generation` by number/kind (ResumeRequest 14, ShutdownRequest 6, both untouched), adapter_session_address asserts only reserved number+name and the absence of `slot_id`/`SlotId`, checkpoint_stream's closed sets cover other messages. EVIDENCE: tests/tier3_contract/gatewaycontrol_scrub/shutdown_recycle_wire_test.go:208-237,259-267; tests/tier3_contract/adapter_generation_fence/generation_fence_wire_test.go:70-84; tests/tier3_contract/adapter_session_address/session_address_wire_test.go:80-215

FACT: production constructs the six touched request messages in exactly one place, so the two mechanical sweeps in the files-touched list (both scoped `--include=*_test.go pkg/ tests/`) leave no production caller behind. `grep -rn 'ShutdownRequest{' --include='*.go'` outside tests and pkg/proto returns one hit. EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:814 (the unexported `shutdown` builder both exported forms delegate to, :807 and :860), :181, :275, :307, :328, :617

FACT: adapter-side `adapterv1.Error` status details are shipped behaviour, so CODE-7's detail-reading translation has a real producer and a real precedent. EVIDENCE: pkg/adapter/staging.go:209 (`st.WithDetails(&adapterv1.Error{`); pkg/gateway/runtime/adapterclient/client.go:333-341 is genuinely a `status.FromError` + `st.Details()` reader, as CODE-7 claims, not a trailer-metadata reader

FACT: the two new `ErrorCode` values reach exactly one machine consumer outside the gateway, and it is permissive. `cmd/lenny-compliance` parses the enum out of the embedded proto and asserts membership of a runtime-emitted `error.code`, so two added values only widen what it accepts. No gate anywhere pins the enum as a closed set or reconciles it against §15.1 or docs/reference/error-catalog.md. EVIDENCE: cmd/lenny-compliance/schemaassert.go:56-95; schemas/embed.go (the `Error.ErrorCode` enum is "the closed §15.1 error-code catalog the JSONL schema leaves as an open string"); schemas/lenny-adapter-jsonl.schema.json:194 (`"code": { "type": "string" }`)

FACT: the docs mirrors of the retired reporting universal are exactly the three the SPEC-3 carrier table assigns. Running CODE-10's grep alternatives over `docs/` returns security-principles.md:33, execution-modes.md:68 (both DOCS-4) and adapter-contract.md:81 (DOCS-2); multi-tenancy.md:72 already carries no reporting clause, which is what SPEC-3's non-carrier sentence says. `ReportSessionScrub` appears in no other page. EVIDENCE: docs/operator-guide/security-principles.md:33, docs/reference/execution-modes.md:68, docs/operator-guide/multi-tenancy.md:72, docs/reference/adapter-contract.md:81

FACT: every DOCS-1/2/3/4 verbatim quote and line citation resolves. adapter-contract.md :10, :53, :64, :75, :81; state-machines.md :138, :235, :237, :251; error-catalog.md :129. The DOCS-2 replacement rows keep both tier-11 substring gates green: `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` wants "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" (all present), and `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` wants the exact string "The request is session-scoped: it is addressed by the identifier of the released session and names no slot." in both carriers, which the staged row reproduces word for word. EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-310; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:30-74

FACT: §15.1's `SETUP_COMMAND_FAILED` has exactly two published carriers, spec/15 and docs/reference/error-catalog.md:129, and SPEC-5 plus DOCS-3 cover both. It appears in no OpenAPI document (pkg/gateway/externalapi/openapi/openapi.json carries no error-code enumeration), no client SDK, no docs/client-guide page and no docs/api page. EVIDENCE: `grep -rn SETUP_COMMAND_FAILED docs/ schemas/ sdks/ pkg/ --include='*.json' --include='*.ts' --include='*.py'` returns only Go files under pkg/gateway/sessionserver

FACT: the claim-register citations hold. `#2851-gateway-to-pod` is the anchor the shipped `coordination_generation` rows use (scripts/seed-claim-register.py:86, :182, :383), `R8` is declared as a plan heading at gateway-runtime-comms-remediation.md:980 and is already the deferral of the shipped external-adapter compliance-suite row (scripts/seed-claim-register.py:187-196), and the tier-0 validator's schema rules for a non-credential row are status/surface/deferral/anchor only, all of which the three staged rows satisfy. EVIDENCE: tests/tier0_static/claim_register_test.go:30-58

DECISION: returned an empty findings list — BECAUSE every client-facing representation the change touches (schemas/lenny-adapter.proto and its generated copies, the tier-3 descriptor pins, docs/reference/adapter-contract.md, state-machines.md, error-catalog.md, execution-modes.md, security-principles.md, the claim register) is staged, and every parallel surface my lens owns that the change does NOT touch (openapi.json and the derived MCP create_session schema, the lenny/* tool names, the A2A and OpenAI-completions surfaces, sdks/runtime/* and sdks/client/*, the JSONL and runtime-ops-events schemas, the CRDs under charts/lenny/crds and pkg/embedded/crds, docs/api/*, docs/client-guide/*, docs/runtime-author-guide/*) stays correct after the edits — ALTERNATIVES: I considered filing the `SessionScrubOutcome` enum comment at schemas/lenny-adapter.proto:437 ("the §5.2 per-slot cleanup the adapter runs on every session release") as an unstaged proto carrier, and refuted it: SPEC-3's carrier table names that comment explicitly as a site that "states only that the cleanup runs on every release" and "stays true", and the scrub-model replacement keeps "a per-slot cleanup runs on every session release" verbatim, so it is dispositioned rather than missed.

WATCHOUT: schemas/embed.go's package comment asserts the proto's `Error.ErrorCode` enum "is the closed §15.1 error-code catalog", and the enum's own comment says "The catalog below mirrors spec §15.1". Both are already imprecise before this proposal — `ERROR_CODE_PROTOCOL_VERSION_INCOMPATIBLE` has no §15.1 row, which is the precedent the spec staging itself cites — so adding two more gateway-to-adapter-only codes does not newly falsify either, and neither is a finding. A future round tempted to file it should check the precedent first. EVIDENCE: schemas/embed.go:19-22, schemas/lenny-adapter.proto:556-558, :585

### [non-spec.13.review-docs-alignment.1]

DECISION: returned an EMPTY findings list for the documentation-alignment lens on round 13 — BECAUSE every staged docs anchor is verbatim-live at HEAD, every behavior this proposal changes that docs/ describes has a staged mirror, and every candidate gap I could construct is already recorded (Open/Deferred) or already refuted on grounds that still hold — ALTERNATIVES: rejected filing (a) `docs/api/internal.md`'s gRPC status table lacking `ABORTED` (pre-existing: `pkg/adapter/checkpoint.go:115` already answers `codes.Aborted`, and the page's proto excerpt is wholesale drift — `StopSessionRequest` at `docs/api/internal.md:146` against a proto with no `StopSession`); (b) `docs/reference/adapter-contract.md:84` `**Scrub responsibilities.**` as a surviving carrier of the withdrawn reporting universal (its reader's session ran, so the slot reached `running` and the report is owed — the standing Open's reasoning holds); (c) DOCS-2 mirroring only the §15.4 token block and not the §15.4 reclaim-hold block (adding new material, no existing sentence made false).

FACT: I independently re-verified all ten DOCS anchor lines and their "currently reads" quotes at HEAD; all verbatim. — EVIDENCE: docs/reference/state-machines.md:138,235,237,251; docs/reference/adapter-contract.md:64,75,81; docs/reference/error-catalog.md:129; docs/reference/execution-modes.md:68; docs/operator-guide/security-principles.md:33

FACT: every gate DOCS-1..4 and the Testing section name exists where claimed and asserts what is claimed. `TestAdapterContractNamesTheShutdownRPCUnderItsWireName` reads the row through `lineContaining(page, "| `Shutdown` |")` and requires exactly "end-of-session teardown", "recycle disposition", "ReportSessionScrub", "ReportPodScrub" — all four survive DOCS-2's staged row. `TestPerSlotCleanupStatedOnEverySessionModeRow` only scans the residual-state table for "Per-slot cleanup" via `residualStateTable`, so DOCS-4's clause deletion moves nothing. — EVIDENCE: tests/tier11_docs/basic_level_echo_stamp_doc_reconciliation_test.go:294-311,462-477; tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:43; tests/tier11_docs/per_slot_substate_scope_doc_reconciliation_test.go:30-118

FACT: DOCS-3's "nothing holds `docs/reference/error-catalog.md` to §15.1" is true at HEAD. `grep -rn 'error-catalog' tests/ scripts/ cmd/ Makefile` returns nothing. — EVIDENCE: (empty grep over tests/, scripts/, cmd/, Makefile)

FACT: no alert is added by this proposal (SPEC-6 stages two §16.1 metric-catalog rows and nothing else), so no `docs/runbooks/` companion is owed, and no runbook enumerates slot leak or slot-cleanup failure today (`grep -rln slot docs/runbooks/` returns only redis-failure.md, token-store-unavailable.md, ephemeral-container-cred-guard-unavailable.md). — EVIDENCE: spec-changes.md:1212-1224; docs/runbooks/

FACT: `docs/reference/metrics.md` placement for the gateway series is discoverable rather than blank — `lenny_slot_failure_total` sits at `docs/reference/metrics.md:166` under `### Session recycle and slot metrics`, and the `## Adapter metrics` preamble at `:171-173` already carries the "outside the default scrape set" phrasing CODE-9 tells the implementor to reuse. — EVIDENCE: docs/reference/metrics.md:158,166,171-173

USEFUL [the round-12 docs sweep recorded at review-log.md:1776-1793]: its four rejections (internal.md `ABORTED`, troubleshooting.md `WARM_POOL_EXHAUSTED` cause, the successor-close accepted mode, the complete 15-hit docs-carrier sweep) saved me from re-deriving all four; I re-ran the `ABORTED` and carrier-sweep halves and got the same result.

UNVERIFIED: whether `docs/reference/adapter-contract.md:84`'s `**Scrub responsibilities.**` sentence ("the adapter runs the credential purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") would read as false to a runtime author on a pool where a bind fails pre-`running`. I judged no (that reader's session ran), but a fourth docs lens disagreeing should note the remedy is one clause, never a rule restatement.

### [non-spec.13.review-edit-sites.1]

DECISION: returned an empty findings list for the edit-site lens on non-spec round 13 — BECAUSE every identifier the staging adds, changes or retires was greped across spec/, docs/, schemas/, charts/, pkg/, cmd/, tests/ and migrations/ and every surface that becomes wrong is already in an edit list — ALTERNATIVES: filing the `stageWorkspace` / `cl.Close()` function-level omissions in binder.go and slotbinder.go, rejected because the file is listed and a prior round's identical finding was refuted as edit-list polish.

FACT: no docs/, schemas/ or charts/ surface names `bind_attempt`, `unconditional_teardown`, `slot_reclaim`, `SlotReclaimOutcome` or either new `ErrorCode` today, so every one of those identifiers is greenfield and only the pages the proposal already names mirror the behaviour they change — EVIDENCE: `grep -rn "unconditional_teardown\|bind_attempt\|SlotReclaimOutcome\|slot_reclaim" docs/ schemas/ charts/ spec/` returns nothing.

FACT: the withdrawn reporting universal has exactly four docs carriers and they are all dispositioned. `docs/reference/adapter-contract.md:75` and `:81` (DOCS-2), `docs/reference/execution-modes.md:68` and `docs/operator-guide/security-principles.md:33` (DOCS-4). `docs/operator-guide/multi-tenancy.md:72` and `docs/runtime-author-guide/{lifecycle.md:69,index.md:186}` state the cleanup without the report and are correctly non-carriers — EVIDENCE: docs/operator-guide/multi-tenancy.md:72.

WATCHOUT: `docs/reference/adapter-contract.md:84` ("the adapter runs the credential purge, deployer `cleanupCommands`, and the scrub, then reports through these RPCs") is the nearest thing to an unlisted reporting carrier. I did not file it: its scope is a session that ran to its end on a recycling pod, which is exactly the case SPEC-3 keeps reporting for, so it stays true. A later lens reaching for it needs an argument that the sentence quantifies over pre-`running` releases — EVIDENCE: docs/reference/adapter-contract.md:84.

FACT: the claim-deletion projection has exactly one docs carrier, `docs/reference/state-machines.md:138`, and DOCS-1 replaces all four of its clauses. `docs/reference/glossary.md:258` describes claim CREATE/DELETE traffic and states no projected phase — EVIDENCE: docs/reference/state-machines.md:138.

FACT: the `error_type` label domain of `lenny_slot_failure_total` is enumerated in neither spec/16 nor docs/reference/metrics.md, so CODE-9's new `slotFailureWorkspaceFinalize` stage value owes no catalog edit. Contrast `lenny_upload_extraction_aborted_total` and `lenny_warmpool_warmup_failure_total`, whose §16.1 rows do enumerate their domains — EVIDENCE: spec/16_observability.md:14 versus :22 and :123.

FACT: `spec161Metrics` is reconciled only against `MetricCatalog()`, never against the §16.1 document text, so SPEC-6's adapter row can land without a `spec161Metrics` entry — EVIDENCE: pkg/observability/metrics/catalog_test.go:188-211; no test file outside `pkg/observability/metrics` names `spec161Metrics`.

FACT: verified counts and sets a future round need not re-derive. `ShutdownRequest{` in test literals is exactly 64 sites in 26 files. `ReleaseSlotForTest` call sites are exactly five (checkpoint_stream, credexpiry, integrationlevel, one_session_only, tracingcontext_addressing), with the definition in export_test.go. Production `Shutdown`/`ShutdownRecycle` callers are exactly the five the CODE-4 table names. Production constructors of the five token-carrying requests are all in `pkg/gateway/runtime/adapterclient/client.go` — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:181,275,307,328,617,814.

FACT: proto field numbers in SCHEMA-1's table are all genuinely free, including `ResumeRequest` 16 (1-5 and 7-14 used, 6 and 15 reserved) and `ErrorCode` 28/29 after `PROTOCOL_VERSION_INCOMPATIBLE = 27` — EVIDENCE: schemas/lenny-adapter.proto:585, and the `ResumeRequest` block from :1333.

FACT: the tier-0 inventory rule the proposal paraphrases as "a file whose name matches `slot*_test.go`" is actually `(slot|one_session_only|sole_session)[^/]*_test\.go$`, i.e. "slot" anywhere in the basename. Every new file this change adds has it as a prefix, so the paraphrase is harmless here; a future change adding, say, `bind_slot_test.go` must not read the proposal's wording as exhaustive — EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:1173.

USEFUL [round-12 refuted list]: the recorded refutations of the CODE-10 wrapped-carrier finding and the CODE-4 `cl.Close()` finding both saved me from re-filing near-variants; I reached the same two sites independently and stopped at the recorded reasoning.

### [non-spec.13.review-fresh.1]

DECISION: returned an empty findings list for the fresh-holistic lens on non-spec round 13 — BECAUSE a
mechanical re-verification of ~70 distinct file:line citations across the whole non-spec document turned up
zero discrepancies, and the two mechanism areas I probed independently (defer ordering in CODE-1's
`Shutdown`, lock order between the per-slot guard and `s.mu`) both check out — ALTERNATIVES: filing the
`noteShutdownMetUntokenedEntry` helper as an unstated symbol and the CODE-4/CODE-7 overlap on who adds
`Client.ShutdownReclaim`; both are proposal-completeness polish that makes no applied text wrong, which the
rubric directs me to refute.

FACT: the CODE-1 defer ordering is correct as written and is the kind of thing a reader mis-reads. `defer
unlockSlot()` is registered immediately after `lockSlotGuard` and the hold-release closure is registered
later, inside the second critical section, so Go's LIFO order runs the hold release first and the guard
unlock last — which is exactly the "the guard outlives the hold" property the staged comment claims.
EVIDENCE: non-spec-changes.md:365-400 (the staged handler body).

FACT: the whole citation surface I sampled is exact, including counts. `ensureSlotPaths` at
staging.go:181 and `req.GetMidSession()` at staging.go:239 really are 58 lines apart; the seven
`releaseSessionSlot` call sites on `Resume`'s rollback paths are exactly resume.go:69,73,89,107,126,134,141;
the five `reclaim()` sites in `Binder.Prepare` are exactly binder.go:883,918,923,935,950 and the three in
`Binder.Launch` exactly :1010,1021,1032; `ErrorCode` really does run to 27 (schemas/lenny-adapter.proto:585);
every field number in SCHEMA-1's table is free with the stated basis. A future lens should not re-verify
these one at a time — spot-check two or three and move on to mechanism.
EVIDENCE: pkg/adapter/staging.go:181,239; pkg/adapter/resume.go:69-141; pkg/gateway/podlifecycle/podsession/binder.go:883-1032.

FACT: `spec/05_runtime-registry-and-pool-model.md:545` is the `**Slot cleanup:**` bullet (the
`max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula) and `:440` is the `**onPoolExhausted**`
paragraph (`WARM_POOL_EXHAUSTED` with `Retry-After`). Both queue-head-bullet citations are right. I briefly
mis-read them as swapped because `sed -n '543,547p;438,442p'` prints in file order, not argument order.
EVIDENCE: spec/05_runtime-registry-and-pool-model.md:440,545.

FACT: the `SessionScrubOutcome` enum comment at schemas/lenny-adapter.proto:436-437 does contain "on every
session release", and SCHEMA-1 explicitly leaves it unedited. That is correct rather than a missed carrier:
its subject is the cleanup the adapter *runs* on every release, which SPEC-3 keeps true; only the
*reporting* universal is withdrawn (DOCS-4 states the same split). Do not file this.
EVIDENCE: schemas/lenny-adapter.proto:436-437; non-spec-changes.md:2519-2521, :2892-2896.

FACT: the tier-11 gate `TestSessionScrubReportAddressingAgreesBetweenSpecAndContractDoc` requires the exact
string `"The request is session-scoped: it is addressed by the identifier of the released session and names
no slot."` in BOTH the staged spec §4.7 row and the staged DOCS-2 row. Both staged rows preserve it verbatim,
so the gate holds. A future edit to either row must keep that sentence byte-for-byte.
EVIDENCE: tests/tier11_docs/session_scrub_report_addressing_doc_reconciliation_test.go:66-72;
spec-changes.md:768; non-spec-changes.md:2798.

### [non-spec.13.review-kubernetes.1]

FACT: the staged self-referential projection input is idiom-legal, and the tree says so in three
places. SPEC-4 adds "the phase the pod currently projects (the controller's own last level, which
it may read back because it is the sole writer of that field)" to §4.6.1's input enumeration; the
sole-writer premise holds because both WPC arms write `Sandbox.status.phase` under the single
`lenny-warm-pool-controller` field manager and the gateway holds no `sandboxes/status` grant.
EVIDENCE: pkg/controller/warmpool/occupancy.go:145-159 ("Both write Sandbox.status.phase under the
single lenny-warm-pool-controller field manager, so the §4.6.3 sole-writer invariant holds across
the two arms"); spec/04_system-components.md:632 ("The gateway holds no `sandboxes/status` grant").
A future kube lens does not need to re-derive this.

FACT: `ProjectOccupancyPhase`'s no-claim arm is total over three cases only — `Reserved`→`Idle`,
`Claimed`→`Draining`, everything else `("", false)` (the phase is left to the Sandbox-to-Pod
reconciler). EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143. DOCS-1's replacement clauses
and the staged §4.6.1 bullets both agree with this arm by arm; I checked each against the page text
at docs/reference/state-machines.md:138.

FACT: the summary's unstaged-defect entry "§4.6.1. A pod claimed and released between two
reconciles loses its `claimed → draining` retirement" verifies whole. Each of its six citations
says what it claims: occupancy.go:128-143 (the three-case switch), occupancy.go:281-291
(`claimToSandbox` maps every claim event to one request keyed on the owning Sandbox, so
controller-runtime's key dedup coalesces CREATE and DELETE), claimer.go:322-330 (`DeleteClaim` is
an unconditional idempotent delete), slotclaimer.go:470-497 (pass 2 filters on
`Status.Phase == Idle`, no live claim, `expiredByUptime`, with no used-pod guard),
bindingstate.go:239 (`WriteDispositionStatus`). Its finalizer claim also holds: the only production
finalizers in the tree are on `Runtime` (pkg/controller/runtime/controller.go:55) and `Sandbox`
(pkg/controller/sandbox/finalizer.go), never on `SandboxClaim`. Do not re-derive.

WATCHOUT: the obvious kube-lens finding on this proposal — "CODE-8 skips `failPhase`, but the
caller deletes the per-pod claim anyway and retires the healthy pod" — does NOT hold, and I spent
real time on it. On the /finalize path `Prepare`'s error is returned unchanged with no claim
release (pkg/gateway/sessionserver/finalize.go:279-282), which is CODE-8's own named scenario. The
combined one-call path does roll back unconditionally on an exclusive claim
(`createClaimNeedsRollback` returns true whenever `claim.SlotID == ""`,
pkg/gateway/sessionserver/start.go:3211-3225, calling `ReclaimClaimed` at :3252), but no typed
refusal is reachable there: the pod is freshly claimed and the session identifier is new, so no
entry with a foreign token can exist at that slot identifier, and `Binder.Launch`'s only refusal
needs a live started session for the same identifier. EVIDENCE: start.go:2352-2378 (startOnPod's
combined-path reconnect), start.go:826-838 (the rollback site).

FACT: the tier-2 envtest paragraph's apiserver-write claim checks out. `Binder.Prepare`'s body
(binder.go:843-970) issues no apiserver write of its own; the only ones on the two arms are
`failPhase`'s `drain` → `podclaim.DeleteClaim` (binder.go:1079, :1200-1202) and the reconnect
arm's `ReclaimClaimed`, which the refusal arms do not reach. No pod label or annotation is written
on either arm.

DECISION: returned an empty findings list for the Kubernetes-idiom lens — BECAUSE the proposal
touches exactly one CRD surface (the §4.6.1 / §6.2 occupancy projection, and only to align prose
with `occupancy.go`), writes no status from a non-owner, uses no status field as an inbox, adds no
finalizer, puts no reconcile on a synchronous request path, and leaves the admission webhook
untouched — ALTERNATIVES: filing the caller-rollback bypass above (refuted on reachability) and
filing DOCS-1's "A pod in a warm-inventory phase with no claim projects `idle`" as loose against
the `warming` case (it is wording, narrower than the text it replaces, and below the bar).

### [non-spec.13.review-mechanism.1]

DECISION: returned an EMPTY findings list — BECAUSE every end-to-end flow I traced (mint → carry →
cascade → confirmation → compensation → accounting → hold release → whole-pod scrub) closes against
the tree, and the two candidates I worked up both fall under recorded refutation precedent —
ALTERNATIVES: filing the `stageWorkspace` threading gap (see UNVERIFIED below), rejected as the same
class as the already-refuted CODE-4 `cl.Close()` edit-list finding.

FACT: `slotCleanupBudget`'s `cleanupTimeoutSeconds / maxConcurrentSessions` cannot divide by zero on
either caller, and I checked this because `PoolMatch.MaxConcurrentSessions` really can be 0.
`slotBindRequest` is built only under `match.MaxConcurrentSessions > 1`
(pkg/gateway/sessionserver/start.go:2351, :2465, :2539-2546), and `ResumeRequest.MaxConcurrentSessions`
is normalized by its caller through `maxConcurrentSessions(bound)` which floors at 1
(start.go:3353-3359, call site start.go:4029; field doc "normalized to a minimum of 1 by the caller" at
pkg/gateway/podlifecycle/podsession/binder.go:640-642). Do not spend another round on this.
— EVIDENCE: pkg/gateway/sessionserver/start.go:3353-3359, :4029; pkg/gateway/podlifecycle/podsession/binder.go:640-642

FACT: every line citation in CODE-1's `answerShutdown` recycle paragraph and CODE-4's
unconditional-teardown table resolves exactly as written on the current tree.
`slotbinder.go:542` is `cleanly, err := result.Adapter.Shutdown(ctx, result.SessionID, "", 0)`, `:543`
is `leaked = err != nil || !cleanly`, `:574` is the `ShutdownRecycle`; `binder.go:1994` is
`b.shutdownAdapter(ctx, result, true)` inside `Binder.Release`'s recycle branch, `:2037` is
`ShutdownRecycle` and `:2043` the plain `Shutdown` inside `shutdownAdapter`;
`cmd/lenny-gateway/user_revocation.go:129` is the §11.4 fan-out's `Shutdown`. A whole-tree grep for
`ShutdownRecycle(` and `.Shutdown(ctx` under pkg/ and cmd/ returns no sixth non-compensating caller,
so the table is complete. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1994,:2037,:2043;
pkg/gateway/podlifecycle/podsession/slotbinder.go:542,:543,:574; cmd/lenny-gateway/user_revocation.go:129

FACT: CODE-4's `slotbinder.go:379 and :393, both before the RPC at :403` is exact. `:379` is the pool
`AssignProto` mint, `:393` the user `MintProto` mint, `:403` the `cl.AssignCredentials` RPC. The
"a typed refusal at the credential-assignment stage arrives after this attempt's leases are minted"
argument therefore holds on the tree. — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:370-403

UNVERIFIED: `b.stageWorkspace` is the one production carrier of `Client.PrepareWorkspace` for BOTH
bind paths (called at pkg/gateway/podlifecycle/podsession/binder.go:1327, from `materializeSlot` via
slotbinder.go:285 and from `Binder.Prepare` at binder.go:918), and no deliverable names it as taking
the attempt token or the `mid_session` value, although CODE-4 stages `Client.PrepareWorkspace` to carry
both and asserts "both carry the token on four requests". I did NOT file it: the omission cannot ship
silently (the client signature change makes it a compile error, and an empty token on a
non-mid-session request is refused loudly by rule 1), the files-touched bullets for binder.go
("`Binder.Prepare`'s mint, carry") and slotbinder.go (`materializeSlotStages`) cover the function's
two enclosing scopes, and the remedy is one added clause — the same class the material skeptic refuted
for the five in-stage `cl.Close()` calls in round 12. A later round that wants it owes an argument that
distinguishes it from that refutation. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1322-1330,
:918; pkg/gateway/podlifecycle/podsession/slotbinder.go:285

USEFUL [the standing-context Traps list]: the `bound`/`started` drift trap and the "read the KEY COLUMN
of the §5.2 table" trap each killed a candidate of mine before I spent a verifier on it. The
"`ShutdownReclaim` must NOT route through `Client.shutdown`" WATCHOUT is exact: `Client.Shutdown`
(client.go:807-809) and `Client.ShutdownRecycle` (:860-866) both delegate to the builder at :813-824,
which is where `unconditional_teardown` lands, and the builder returns `(bool, error)` alone so
`ShutdownReclaim`'s three-value return could not go through it anyway.

### [non-spec.13.review-performance.1]

DECISION: returned no findings for round 13 — BECAUSE the whole delta against the r11 snapshot is
two DELETION hunks in the accepted-failure-mode bullets (non-spec-changes.md ~:3890-3915), both
removing derived latency instantiations of the per-slot cleanup budget, and a deletion of a derived
number adds no write, no serialization point and no test obligation — ALTERNATIVES: filing on the
surviving clause "raising `maxConcurrentSessions` shrinks the per-slot budget toward the five-second
floor" (rejected: directionally true per spec/05_runtime-registry-and-pool-model.md:545, and the §5.2
CRD rule rejecting `cleanupTimeoutSeconds / maxConcurrentSessions < 5` at admission means the clamp
is never reached, which is why the text says "toward" rather than "to"; rationale prose, no behaviour).

FACT: after the round-12 deletions every citation the queue-head bullet still makes checks out
verbatim: `DefaultMaxQueueWaitSeconds = 30` at pkg/gateway/sessionserver/queue.go:18; head-only FIFO
admission comment and `case <-ticket:` at queue.go:184-191; the exhaustion sentinel branch
`if q.now().After(deadline)` … `return zero, firstErr` at queue.go:194-202; `runWithQueue(...)` with
the `bindSlotWithRetry` closure at start.go:2605-2608; `result, err := binder.BindSlot(ctx, req)` at
start.go:2810; the `max(cleanupTimeoutSeconds / maxConcurrentSessions, 5)` formula and the CRD rule at
spec/05_runtime-registry-and-pool-model.md:545; `WARM_POOL_EXHAUSTED` with a `Retry-After` header at
spec/05_runtime-registry-and-pool-model.md:440.

FACT: the bullet's ordering claim is right in the code — the wait bound is tested AFTER admission
(`case <-ticket:` at queue.go:190, then `q.now().After(deadline)` at :194), so a displaced waiter's
wall-clock wait really is its own bound plus the head's attempt duration. Do not re-derive this.

FACT: the staging's metric cardinality is bounded and carries no per-attempt label. Both new counters
take `pool` and `k8s_pod_name` only (non-spec-changes.md:1026, :3383-3384); `bind_attempt` appears in
no label set anywhere in the proposal (grep for `WithLabelValues|label` over non-spec-changes.md
returns 14 sites, none of them token-keyed). A future performance lens reaching for "the per-attempt
token becomes a metric label" has nothing to file.

FACT: CODE-4's `releaseAttemptCredentials(minted)` narrows what the existing session-wide
`credassign.Service.ReleaseSession` walk releases (non-spec-changes.md:1092-1112,
`credassign.go:400-409`); it is one release per failed bind, the same count the shipped path already
issues, so it is a scope change rather than a write-rate change. No new Redis key, Postgres table or
etcd object is introduced by the non-spec staging: the only store-touching sites it names are the
pre-existing `active_slots` counter and the `ReleaseSlot(leaked)` parameter (non-spec-changes.md:467,
:3593).

USEFUL [non-spec.12.review-single-source.1]: the round-12 accounting that the diff is two hunks and
both are deletions was exact, and checking it cost one `diff -ru`. It let this round spend its whole
budget re-verifying the seven surviving citations in the two touched bullets instead of re-reading the
staging.

### [non-spec.13.review-reliability.1]

FACT: `SessionUsageMeter.Usage` and `Cumulative` both take `_ context.Context` and ignore it
(`pkg/adapter/usage.go:144`, `:157`), so `emitFinalUsage` cannot lose a report to an expired
context with the in-tree meter. EVIDENCE: pkg/adapter/usage.go:144-161. I nearly filed the
§10.1.4 bullet's "no final usage report is lost" clause as a false causal claim (emitFinalUsage
runs on the pass ctx, not on the per-member close ctx); the files-touched entry at
non-spec-changes.md:4093-4096 already moves `emitFinalUsage` onto the per-member context anyway,
and the meter ignores ctx regardless. Both halves refute it. Do not re-file.

FACT: `SlotBindRequest.CleanupTimeoutSeconds` IS populated on every concurrent pool, from
`match.CleanupTimeoutSeconds` (`pkg/gateway/sessionserver/start.go:2566-2567`), which
`foldPoolPolicy` copies off the poolstore sessionPolicy mirror unconditionally
(`pkg/gateway/podlifecycle/podsession/resolve.go:384`). The struct's own doc comment
("Empty/zero on a non-recycling concurrent pool", `slotbinder.go:97-108`) is stale and misled me
into thinking `slotCleanupBudget` would always collapse to its 5s floor on a non-recycling pool.
It does not. `ResumeRequest.CleanupTimeoutSeconds` is populated the same way
(`start.go:4038`). EVIDENCE: pkg/gateway/sessionserver/start.go:2566-2567, 4038;
pkg/gateway/podlifecycle/podsession/resolve.go:384.

FACT: `poolstore` validation already enforces `cleanupTimeoutSeconds >= maxConcurrentSessions*5`
when non-zero (`pkg/gateway/runtime/poolstore/poolstore.go:567`), so the budget's 5s floor is
never the binding constraint on a configured pool. EVIDENCE:
pkg/gateway/runtime/poolstore/poolstore.go:567.

FACT: `SocketRuntimeProcess.Close` never selects on `ctx.Done()`; it only derives the SIGTERM
grace from the context (`pkg/adapter/socketruntime.go:435-467`, `resolveShutdownGrace` at :457).
So CODE-2's rollback close on an already-cancelled inbound ctx still kills the child rather than
leaking it. Do not file the rollback's use of the inbound ctx as a leak.
EVIDENCE: pkg/adapter/socketruntime.go:435-467.

WATCHOUT: CODE-1's removing arm does NOT abandon its work when the slot-guard acquisition
expires; it logs `slot_guard_not_acquired` and removes unguarded
(non-spec-changes.md:352-356, licensed by CODE-6 at :1756-1759). Any reasoning about a
compensating `Shutdown` that "answers nothing in time" must account for the destructive work the
handler still performs on that path. EVIDENCE:
proposals/.../non-spec-changes.md:352-356, :1756-1759. This is the basis of the one finding I
filed this round (the guarded-`Resume` accepted-failure bullet at :3890-3904).

OPEN: if that bullet is corrected, check whether the hold release is still correct on that path:
`completed = closeErr == nil && treeErr == nil` (non-spec-changes.md:~430) can be true for a
`removeSlotTreeVia` that removed the tree while `ExtractTree` was still writing into it, which
releases the identifier over a live extraction.

### [non-spec.13.review-security.1]

FACT: `podsession.CredentialAssigner` (pkg/gateway/podlifecycle/podsession/binder.go:319-332) exposes
only `AssignProto` and `ReleaseSession`. The richer `credassign.Assigner` interface
(pkg/gateway/credentials/credassign/credassign.go:45-62) DOES carry `Release(leaseID string)`, and both
production implementations have it (`Service.Release` credassign.go:380, `Client.Release` client.go:299).
So `b.Credentials.Release(id)` is unavailable at the consumer interface without widening it.
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:319-332

FACT: seven test fakes are wired to `podsession.Binder.Credentials` and none declares `Release`:
`fakeAssigner` (pkg/gateway/podlifecycle/podsession/binder_test.go:309-324),
`recordingLeaseAssigner` (pkg/gateway/sessionserver/start_pod_test.go:1150-1162),
`failingAssigner` and `recordingAssigner` (pkg/gateway/sessionserver/delegated_child_materialize_test.go:129-145),
`reclaimRecordingAssigner` (pkg/gateway/sessionserver/terminal_reclaim_internal_test.go:29-35),
`leaseCountingAssigner` (tests/tier9_security/credential_delivery_gate_test.go:66-80),
`materializeLeaseCounter` (tests/tier9_security/delegation_child_materialization_cred_test.go:108, wired :172),
plus the tier4 ones (eager_claim_lifecycle_test.go:109, cross_environment_delegation_test.go:511,
delegation_child_materialization_test.go:123). Only `fakeAssigner` is named in the proposal.
EVIDENCE: grep -rn "func (.*) AssignProto(" --include=*.go .

FACT: the tier-0 credit gate resolves a cited section to the FINEST declared spec-map key, so a
whole-file credit under `4.7` does NOT satisfy a case annotating `§4.7.1`: `creditKey`
(tests/tier0_static/spec_map_slot_address_registration_test.go:911-925) walks up from the cited id and
stops at the first id present in `declared`, and `declared` is every key in tests/spec-map.json
(specMapCredits :841-846), which includes `4.7.1`. `TestSlotAddressCasesAreCreditedToEverySectionTheyAnnotate`
(:970-989) applies to whole-file registrations too, via the `len(perCase)==0` branch.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:911-989

WATCHOUT: when checking any new `slot*_test.go` file's stated tests/spec-map.json registration, compare it
against the file's stated `// spec:` annotation id-for-id at full granularity. A parent-section credit is
not a credit for a subsection that the map itself declares. `4.7` vs `4.7.1` is the live trap on this
proposal, because the whole document cites §4.7.1 while several older map rows sit at §4.7.
EVIDENCE: tests/tier0_static/spec_map_slot_address_registration_test.go:955-961

FACT: `releaseCredentials` (binder.go:1263-1268) releases through `b.Credentials` alone, and user-source
leases minted by `UserCredentials.MintProto` share the same lease store, released by the pool assigner's
`ReleaseSession` (documented at binder.go:342-343). So an attempt-scoped release by lease identifier does
cover both mint sites (slotbinder.go:379 and :393) once the interface carries `Release`; there is no
second service to reach. A future reviewer should not file "user leases are unreachable".
EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:335-348

FACT (checked, clean, do not re-file): DOCS-4's two target sentences exist verbatim
(docs/reference/execution-modes.md:68, docs/operator-guide/security-principles.md:33) and
docs/operator-guide/multi-tenancy.md:72 already carries the clause-free form, as DOCS-4 claims.
The §11.4 revoke fan-out reaches `Client.Shutdown` (cmd/lenny-gateway/user_revocation.go:129), which the
staging makes set `unconditional_teardown` inside the client, so revoke cannot be fenced out by a token.
`newBindAttempt` is 128 bits of crypto/rand and go.mod:3 declares `go 1.25.0`, so the "Read cannot return
an error" comment holds.

### [non-spec.13.review-single-source.1]

DECISION: returned NO findings for round 13 — BECAUSE the round-12→13 diff against the r11 snapshot is the same two deletion hunks `[non-spec.12.review-single-source.1]` already cleared (the "five on an unset pool" clause and the thirty-second §5.2-example derivation, non-spec-changes.md ~:3890-3915), and an independent re-run of the cross-file sentence-repeat sweep over the five non-log files returned only the known-benign classes — ALTERNATIVES: filing the CONF-1-vs-tier-3 case-list overlap and the three-site sweep-grep restatements; both rejected, see below.

FACT: the cheap sweep re-run (split on `(?<=[.;])\s+`, >=9 words, five non-log proposal files) returns 16 repeat groups, every one in a known-benign class: spec-changes verbatim-anchor/replacement pairs (:271/:278, :439/:445, :762/:768, :1047/:1061, :1168/:1174), summary/checklist one-line deliverable descriptions (summary:1128-1130,:1149 vs checklist:19,:23,:29), spec-row/docs-mirror pairs (non-spec:2780 vs spec-changes:278; non-spec:2798 vs spec-changes:762/768; non-spec:2852 vs spec-changes:1162), status.md boilerplate, the already-declined Design/CODE-8 `failPhase` pair (non-spec:124 vs :1958), and the sweep-grep restatements below. — EVIDENCE: run in-session over proposals/0081_*/*.md minus review logs.

DECISION: did NOT file the two mechanical-sweep grep commands written out at THREE sites each — `grep -rn "ShutdownRequest{" --include=*_test.go pkg/ tests/` at checklist:47, non-spec-changes.md:3242 and :4200, and the `adapterv1\.\(...\)` bind-field grep at checklist:43, non-spec-changes.md:3254-3256 and :4206-4208 — BECAUSE all three copies of each are byte-identical today, `[non-spec.10.review-single-source.1]` already ruled that a grep is not a rule statement under this lens, and the checklist restatement is the excluded one-line-deliverable class — ALTERNATIVES: filing under (g) with the Testing-section site as the home; rejected as below the bar, but note the asymmetry for anyone who touches these: CODE-10 and CODE-12 state their carrier-set commands exactly ONCE each (non-spec-changes.md:2164, :2223) by design, while these two sweeps do not. If a future round edits either command, it must edit three sites.

FACT: the comment-carrier redesign (commit 8fb9afe48) reads clean under this lens. `### Comment-carrier reduction: shared invariants` (non-spec-changes.md:2103-2151) is the single home of the invariants, the non-carrier arm, the closure rule and the SPEC-1/2/6 sweep result; CODE-10, CODE-11 and CODE-12 each carry only a carrier definition, a set (grep or site list) and an arm rule; checklist S24/S25/S26 (:63,:65,:67), the tier-11 note (:3793-3794), the files-touched entry (:4155-4157) and summary:1144-1146 all reference the block by heading rather than restating it. No invariant is written out twice. — EVIDENCE: non-spec-changes.md:2103-2233.

DECISION: did NOT file the CONF-1 case list (non-spec-changes.md:2277-2361) against the tier-3 behavioural-case list (:3492-3520) — BECAUSE the tier-3 list opens with an explicit citation ("carrying CONF-1's case list one for one") and is a per-case title list, and the tier-10 paragraph (:3527-3531) is pure citation — but WATCHOUT: four tier-3 bullets go past a title and re-describe the case's driver, and rule 6's is near-verbatim. CONF-1:2318-2321 "The mid-session arm is `TestMidSessionUploadIsAdmittedOnAStartedSession`, driven over the connection the successful bind published, and it is written with the rest rather than deferred"; tier-3 :3500-3503 "The upload arm is `TestMidSessionUploadIsAdmittedOnAStartedSession`, sent over the connection the successful bind published, and it is written with the rest rather than deferred." Rule 8 (:2323-2327 vs :3506-3507), rule 13 (:2341-2346 vs :3513-3514) and rule 2 (:2288-2294 vs :3494) are the same pattern at lower volume. They AGREE today. `[non-spec.5.fix-G2.1]` (review-log.md:453) recorded that "the tier-3 and tier-10 lists name rules by name only and carry no assertion set, so the change reaches exactly one site" — that is true of the ASSERTION sets and no longer quite true of the drivers. A fixer editing a CONF-1 case description must check the tier-3 bullet for the same rule.

UNVERIFIED: whether `[non-spec.5.fix-G2.1]`'s "reaches exactly one site" rule still holds for every future CONF-1 edit. It holds for assertion sets; it does not hold for the driver text of rules 2, 6, 8 and 13. Whoever next edits a CONF-1 rule case should re-derive rather than trust that log line.

### [non-spec.13.review-test-coverage.1]

DECISION: returned an empty findings list — BECAUSE every candidate I built either resolved
against the tree or fell below the bar; the four I chased are written out here so round 14+
does not re-chase them — ALTERNATIVES: filing the category/retryable gap (refuted by my own
evidence, see below), the unconstructible mid-session rows, and the double-scrub arm.

FACT (checked and NOT filed): the two new refusal envelopes' `category` IS pinned. The Testing
section's tier-3 summary says only "Rule 5 ... with the detail asserted to carry the code"
(non-spec-changes.md:3499) and names no detail assertion for rule 6, but that list is explicitly
"CONF-1's case list one for one" (:3491), and CONF-1's own rule 5 and rule 6 cases assert "the
status, the `ErrorCode` and the category that rule states" (:2288-2294). So the staged §4.7.1
rule 5 `CATEGORY_TRANSIENT` / rule 6 `CATEGORY_PERMANENT` clauses (spec-changes.md:1051-1052) are
covered. Only `Retryable: true`, which CODE-6 sets explicitly at non-spec-changes.md:1514-1516
because "the proto3 default of false contradicts the declared category on the wire", is asserted
nowhere; no staged spec rule states it, so it is not owed coverage.
— EVIDENCE: non-spec-changes.md:2288-2294, :3491, :3499; spec-changes.md:1051-1052

WATCHOUT: read CONF-1 (non-spec-changes.md:2269-2361) before filing any tier-3/tier-10 coverage
gap. The `### Wire-contract tests ... tier 3` block is a POINTER to CONF-1's case list, not the
list itself, and it abbreviates several cases. A gap that looks real in the Testing section is
usually written out in CONF-1. — EVIDENCE: non-spec-changes.md:3491

FACT (checked and NOT filed, below the bar): the tier-1 "wire rule's two directions" bullet
(non-spec-changes.md:2977-2981) asks for both directions "across `PrepareWorkspace`,
`FinalizeWorkspace`, `RunSetup`, `AssignCredentials` and `Resume`", but only
`FinalizeWorkspaceRequest` (schemas/lenny-adapter.proto:724) and, under SCHEMA-1,
`PrepareWorkspaceRequest` carry `mid_session` at all. On the other three the "mid-session request
with a non-empty token" row cannot be constructed at the RPC level, which CODE-6 itself implies
("`RunSetup` and `AssignCredentials` are never mid-session", implementation-checklist.md:43).
Declined: the constructible subset is obvious to an implementor, no assertion is lost, and the
remedy adds scoping text rather than reducing anything.

FACT (checked and NOT filed, below the bar): nothing asserts the whole-pod recycle scrub starts
EXACTLY ONCE on `Shutdown`'s removing arm, although CODE-1 names the double-send as the failure
mode of a partially applied edit ("leaving it in place while the helper also calls it starts the
whole-pod scrub twice", non-spec-changes.md:172-174, and `startPodScrub` files `ReportPodScrub`
at pkg/adapter/podscrub.go:78). The removing-arm and absent-arm cases assert the scrub-done
signal but not a count. Declined as implementation-error hardening rather than a spec path.

USEFUL [round 8's metricsbackfill FACT, review-log.md:2219]: saved me re-deriving the
`cmd/lenny-gateway/metricsbackfill.go:149` wiring gap; the shipped `SlotFailure` wiring has no
test either (`grep -rn podBinder cmd/lenny-gateway/*_test.go` is still empty), so the same
disposition stands.

FACT: the "is tier 5 reached?" question open at review-log.md:1281 reads settled to this lens.
The change adds no cluster primitive; the drain it can newly trigger is the
`lenny.dev/drain-request` merge patch and the `SandboxClaim` delete, both apiserver writes, and
those are covered by the tier-2 `binder_envtest_test.go` pair (non-spec-changes.md:3545-3556) and
the tier-4 datastore-crossing case (:3584-3595), with the threshold decision itself pinned at
tier 1 ("one leak does not drain at threshold 2 while two do", :3408-3412). No tier-5 omission
filed. — EVIDENCE: pkg/gateway/runtime/slothealth/slothealth.go:110-125

FACT: the per-step "Tiers:" lines in the implementation checklist are tiers to RUN, not tests to
WRITE. S11 (CODE-7) names tier 3 while the Testing section attributes no tier-3 case to CODE-7,
and S13/S16/S17/S19/S20 name tier 2 while the only tier-2 cases belong to CODE-8. Both read as
regression-run scope, so neither is a coverage finding.
— EVIDENCE: implementation-checklist.md:38, :42, :48, :50, :54, :56; non-spec-changes.md:3545

### [f6.apply.compensation-queue-hold]
DECISION: item `marker:code-4` for the non-spec bullet "A compensating Shutdown holds the pool's queue head for its own budget" stands as `out-of-scope-stands`. Wrote one entry in the summary's `## Defects in the shipped tree that this proposal does not stage` (summary.md, last entry of that section, immediately before `## Impacts on other proposals`) naming the shipped defect the hold extends: a queued waiter's `maxQueueWaitSeconds` is tested only after the ticket is received, so the bound does not cover the time the waiter spends behind the FIFO head. No decision opened and no fix staged; the non-spec bullet at non-spec-changes.md:3917-3941 is unchanged.
FACT: every citation in the new entry re-verified against the tree — head-only admission and the blocking ticket select at pkg/gateway/sessionserver/queue.go:185-192; the post-admission deadline test and the exhaustion sentinel at :195-202; the 30-second default at :18; the queued closure at pkg/gateway/sessionserver/start.go:2606-2608; the per-slot cleanup formula at spec/05_runtime-registry-and-pool-model.md:545; the `WARM_POOL_EXHAUSTED` plus `Retry-After` envelope at spec/05_runtime-registry-and-pool-model.md:440; "Zero means no aggregate cap" at schemas/lenny-adapter.proto:941-943.
WATCHOUT: the hold itself is created by CODE-4 and is absent from the shipped tree, so the entry is framed on the shipped mechanism it extends (the wait bound that does not cover pre-admission time) rather than on the compensation. A later pass that reads the entry as claiming a shipped compensating `Shutdown` exists will misread it.
FACT: reconciled the open-decisions prose for entry 46 (summary.md:452-464), which said only that the non-spec changes record the hold; it now also points at the unstaged-defects entry.

### [f6.apply.drain-failure-residue]
DECISION: item `marker:code-8` for the accepted failure mode "A pod whose drain failed keeps a dead attempt's token" stands as `out-of-scope-stands`. Wrote one entry in the summary's `## Defects in the shipped tree that this proposal does not stage` (summary.md, last entry of that section, immediately before `## Impacts on other proposals`) naming the shipped defect: `Binder.failPhase` logs and continues when its compensating `b.drain` fails, so the pod survives holding the failed bind's registry entry and, under the staged design, that entry's dead token refuses every later attempt at the session on that pod. No decision opened, no fix staged, and the non-spec bullet at non-spec-changes.md:3890-3894 and CODE-8's pointer at :2044-2045 are unchanged.
FACT: citations re-verified against the tree — the drain call and its log-and-continue handling are at pkg/gateway/podlifecycle/podsession/binder.go:1079-1081 (the bullet's cited range is exact); `releaseCredentials` is gated on `leaseAssigned` above it and `b.drain` is not, so the drain is unconditional on the path.
FACT: the residue is time-bounded rather than permanent, and the summary entry says so. The §4.6.1 orphan-claim collector classifies a `Bound` claim aged past the orphan timeout as `reclaimDrain` (pkg/controller/warmpool/gc.go:274-289) and reclaims it only after Postgres reports no live session on the pod (pkg/controller/warmpool/gc.go:227-246), so the pod retires and the entry goes with it; on a pod still serving a co-tenant the wait lasts as long as the co-tenant. `failPhase`'s own doc comment already states this collection (binder.go:1067-1071).
WATCHOUT: the entry states a bound the non-spec accepted-failure-mode bullet does not state. The bullet claims nothing that contradicts it (it names the reaper as the remedy owner and asserts no permanence), so the two were left as they stand; a later pass that reads "It is the reaper's subject" as a claim of an unbounded residue should reconcile it against the orphan-GC fact above rather than against the summary entry alone.

### [f6.cleanup]
FACT: the summary already carried exactly the required section list, in order, so the cleanup pass rewrote nothing. Headings as they stand: `# Summary: ...`; `## Summary` (no prose of its own, holding `**Problem statement.**`, `**What changes.**`, `**Decisions.**`, `**Watch out for.**` in that order); `## Goals`; `## Non-goals`; `## Open decisions for human to make`; `## Defects in the shipped tree that this proposal does not stage`; `## Impacts on other proposals`; `## Deliverable index` last.
FACT: no `### Retired` or equivalent block exists inside `## Open decisions for human to make`, and no meta-list of staged items with dispositions survives anywhere in the file. Nothing was relocated and nothing was dropped.
FACT: the nine entries under `## Open decisions for human to make` are 20, 29, 30, 32, 33, 34, 36, 41 and 45, which is exactly the set firing 6 left with the human (the two `marker:code-*` items stood as out-of-scope and their entries were written by the apply stage into the unstaged-defects section). Every identifier is verbatim as it stood; none was renumbered.
FACT: the section preamble at summary.md:431-465 was read against the entries the section now carries and is true as it stands, so it was not edited. Its claims that entry 20 and entries 32, 33 and 36 carry question and ground alone, that entries 41 and 45 carry no recommendation, and that the open-decisions phase supplied the recommendation for 29 and 30 and left 34 without one, each match the entry text; firing 6 changed no entry, so the preamble was not falsified.
WATCHOUT: `**Accepted failure modes.**` (summary.md:415-427) is a standalone labelled block at the tail of `## Non-goals`, formatted like the four `## Summary` parts. It was read as content of the listed `## Non-goals` section, whose subject (what this proposal deliberately declines and prices) covers it, rather than as a stray Summary part, and was left in place. A later pass that reads it as a fifth Summary part should relocate it deliberately rather than delete it.

### [redesign.8.fix.1]

DECISION: the expired-acquisition disposition at a removing site is stated ONCE, as `**Disposition of an expired acquisition at a removing site.**` in CODE-6 immediately after the derivation table (non-spec-changes.md, `### CODE-6`), as one rule in three parts (removal unguarded as shipped; hold RETAINED, the expired acquisition counting as a failed act for every §5.2 table column keyed on acts; report per the row a failed act selects) plus a three-row site table (`Shutdown`'s removing arm, guard-acquiring `releaseSessionSlot`, `terminateHeldSession`) — BECAUSE the Settled DECISION at review-log.md `### Settled` ("the reclaim hold ends only on a cleanup that COMPLETED") and the summary watch-out ("spends one attempt rather than losing a tree") both require it, and the locked §5.2 disposition table keys the hold and the pre-`running` clean-exit flag on "an act fails", so a retained hold and a set clean-exit flag cannot share a row: retention forces `exited_cleanly` false on the pre-`running` `Shutdown` arm, and the `running` arm's report stays on the runtime close alone (table row 3) — ALTERNATIVES: (a) hold RELEASED, residue accepted, as the round-13 bullet recorded: rejected because a released identifier admits a successor onto a tree a parked `Resume`'s `workspace.ExtractTree` is still re-creating, on the pod §5.2 placement prefers for the retry, which is the "successor materializes INTO a residue" hazard (`slotlayout.EnsureTree` is idempotent, Settled) the hold was staged to close, and it silently erodes the Settled DECISION; its only saving was leaving CODE-1's `completed` and `exited_cleanly` predicates unchanged. (b) retain the hold but leave `exited_cleanly` true on a nil-returning unguarded removal: rejected because the locked table has no row for "hold held, clean-exit set" on a pre-`running` slot. Cost of the chosen option: the identifier is withheld for the pod's remaining life, identical to the failed-removal row's cost; on the compensation path it adds nothing observable, since the acquisition expires only when the compensation's deadline has, and the gateway has already recorded the RPC error and `leaked`. Buildability checked against the staged text: `lockSlotGuard` already returns `(func(), bool)`, so each site's predicate gains the boolean as one conjunct (`guarded && closeErr == nil && treeErr == nil` at `Shutdown` and `terminateHeldSession`; `guarded && treeErr == nil` at the guard-acquiring `releaseSessionSlot`), and the response becomes `closeErr == nil && (live || (guarded && treeErr == nil))`. No new mechanism; no staged rule contradicted.

FACT: the tree ships every removing site unguarded and none of them observes any acquisition. `Server.Shutdown` (`pkg/adapter/session.go`) discards `removeSlotTree(st)`'s result on its bound arm and bounds only the runtime close, through `contextWithGraceDeadline` on the request context; `Server.releaseSessionSlot` (`pkg/adapter/slotsession.go`) takes no context at all and discards `removeSlotTree(st)`; `Server.onHoldTimeout` (`pkg/adapter/holdstate.go`) mints one ten-second `context.WithTimeout(context.Background(), …)` shared across pass 2 and `Server.terminateHeldSession` discards both `s.Runtime.Close` and `removeSlotTree(m.state)` on it. There is no guard in the tree, so "the shipped unguarded removal" is the whole of today's behaviour, and the statement's removal column reproduces it exactly. The guard, `lockSlotGuard`, `Server.slotGuards` and `removeSlotTreeVia` are all staged by CODE-6 and exist nowhere in `pkg/adapter` today (`grep -rn "slotGuard\|lockSlot\|removeSlotTreeVia" pkg/adapter` is empty).

FACT: sites reduced to citations of the statement, each located by heading: CODE-4's `Binder.Resume` paragraph ("so its removal follows the checkpoint extraction rather than interleaving with it" deleted); CODE-6's `**No guard acquisition outlives its caller's context.**` paragraph (its destructive-section sentence replaced by a pointer; the admission-RPC sentence kept); CODE-6's paragraph after the derivation table ("rather than interleaving its removal with the extraction" deleted) and its guard-acquiring-form tail ("on the same terms as `Shutdown`'s removing arm" deleted); CODE-1's handler comment, `completed` comment and code, `exited_cleanly` prose and code, and its `Shutdown` doc-comment bullet; CODE-6's `terminateHeldSession` and `releaseSessionSlot` bullets under `reclaimSlotLocked`; the tier-1 destructive-expiry case (now keyed on the disposition's three rows, with the hold assertion and the pre-`running` `exited_cleanly` false assertion added); the start-versus-reclaim rollback case's `Resume` row; the guarded-`Resume` accepted-failure bullet (every worked number deleted, including the budget formula); the §10.1.4 per-member accepted-failure bullet; the three `## Files touched` entries for `session.go`, `slotsession.go` and `holdstate.go`; and checklist S15's closing sentence. One tier-1 bullet was ADDED to CODE-1's `Shutdown` list ("An expired guard acquisition keeps the hold and fails the clean exit"), test text only.

WATCHOUT: the CODE-6 `**Disposition of an expired acquisition at a removing site.**` statement is the ONLY home for what a removing site does on an expired acquisition; every other site names it and states no cell. A future fix that finds a site describing the fall-through in its own words ("removes unguarded", "follows rather than interleaving", "frees the identifier") restores the citation rather than conforming the words. The guarded-`Resume` accepted-failure bullet carries no worked number and must not regain one; the budget has one home in shipped §5.2 (`[non-spec.11.fix-design-G1.1]`).

CORRECTS [review-log.md `### Settled`, "The completion predicate differs by site, deliberately, and all three are stated."]: each of the three predicates now carries a leading `guarded &&` conjunct; the site-by-site difference (`Shutdown` and `terminateHeldSession` on both errors, `releaseSessionSlot` on the tree removal alone) stands.
CORRECTS [`[non-spec.13.fix-design-G2.1]`, its OPEN entry]: the residue that entry called materially worse ("plus an identifier released while the extraction writes") no longer exists; the identifier is retained under the disposition, and the remaining residue is the unordered `removeSlotTree` against a live `ExtractTree`, which the bullet still accepts. The human question it declined to file is answered by this DECISION rather than left open.
CORRECTS [`[non-spec.11.review-test-coverage.1]`, its WATCHOUT on the `Resume` row's cancelled-context device]: the guard-acquiring form's fall-through is now "takes CODE-6's disposition", and the destructive-expiry case pins the retained hold and the `Shutdown` row's `exited_cleanly` false beside the removal and the warning, so "already pins" now covers more than the entry says.
CORRECTS [`[non-spec.13.fix-design-G2.1]`, its FACT that `Runtime.Close` runs on an already-expired context on this path]: unchanged as a fact, and it is why the `Shutdown` cell reads the runtime close alone on the `running` arm; a close that fails on the expired context reports `leaked` under table row 2, which the disposition does not alter.

DEFERRED [spec-changes.md, SPEC-3's §5.2 `**Slot-identifier reclaim hold.**` paragraph]: §5.2 ends the hold when "every act that cleanup owes the slot has returned without error" and names no ordering, so on its own text a nil-returning unguarded removal is a completed act; the disposition reads it as a failed act. One clause is owed, stating that a removal performed without the ordering the adapter's guard provides against a section still writing under the identifier is an act that did not return without error. Not staged, because the spec lane is locked; recorded in the summary's `## Defects in the shipped tree that this proposal does not stage` as a spec follow-up.

### [f7.apply]

DECISION: the §5.2 ordering clause CODE-6's expired-acquisition disposition rests on is no longer carried as a spec follow-up owed by a later proposal. Its bullet was REMOVED from the summary's `## Defects in the shipped tree that this proposal does not stage` and its subject re-homed as open decision 47 under `## Open decisions for human to make`, because the gap is a disagreement between this proposal's own two lanes rather than a defect of the shipped tree this proposal may leave standing. Entry 47 states the disagreement, its observable consequence (the adapter refuses every governed request under a held identifier, so a caller meets refusals for the pod's remaining life on a cleanup whose acts §5.2 reads as completed), the exact clause that would close it, and the two answers open to the operator: reopen the spec lane long enough to take the clause beside CODE-6, or drop the `guarded` conjunct and let a nil-returning unguarded removal end the hold. The section preamble gained one sentence recording the stamping and the move.

FACT: the disagreement is reachable rather than vacuous, which is what rules out "an expired acquisition implies its context is done, so another act fails anyway and §5.2's failed-act row already selects hold-for-life". `slotlayout.RemoveTree` takes no context and returns nil for a successful or already-absent tree (`pkg/adapter/slotlayout/tree.go:58-69`), and `SocketRuntimeProcess.Close` returns nil immediately while a sibling slot is active (`pkg/adapter/socketruntime.go:441-446`), so on a co-tenanted pod both acts return nil whatever the caller's deadline did. Entry 47 carries both citations.

FACT: CODE-6's closing sentence was corrected in place rather than left pointing at the log. It now states that the clause is owed by SPEC-3's own `**Slot-identifier reclaim hold.**` paragraph rather than by a later proposal, that it is unstaged because the spec lane is locked by the operator, and that open decision 47 decides whether it lands beside the deliverable or the `guarded` conjunct is dropped. The disposition itself, its three-row site table and the three completion predicates are untouched.

OPEN: entry 47, `## Open decisions for human to make`. Until it is answered, CODE-6's `guarded` conjunct has no sentence in the staged spec lane an implementor could cite for it, and a tier-3 or tier-10 case derived from the §5.2 disposition table would read a nil-returning unguarded removal as ending the hold.

DEFERRED [spec-changes.md, SPEC-3's §5.2 `**Slot-identifier reclaim hold.**` paragraph]: the one-clause addition stands owed and unstaged, on the ground the earlier `[redesign.8.fix.1]` DEFERRED line states. This firing could not land it: the spec lane is locked by the operator for this run. What changed is where the owing is recorded (open decision 47, as this proposal's own inconsistency) rather than what is owed.

### [f7.cleanup]

FACT: the summary carried the listed sections in the listed order already, with one exception. The labelled part `**Accepted failure modes.**` sat between the end of `## Non-goals` and `## Open decisions for human to make`, outside `## Summary` and outside every listed section.

DECISION: that block was relocated into `**Watch out for.**` rather than deleted, as two bullets appended at the end of the part, carrying its pointer sentence and its two priced residues verbatim. `**Watch out for.**` is the listed part whose subject covers an accepted cost an implementor has to know about, and no listed section is closer. No other content moved, and no sentence of the relocated text was rewritten.

WATCHOUT: the standing context records that accepted failure modes live in the spec-changes file's `## Edge cases and accepted failure modes` section only. The summary's pointer at that section and the two residues it prices now sit as the last two bullets of `**Watch out for.**`. A later pass that meets them there should not re-home them under a fresh `**Accepted failure modes.**` part, because that part is not on the section list this phase holds the file to.

FACT: nothing else was owed. `## Open decisions for human to make` carried no `### Retired` or equivalent block, the file carried no meta-list of staged items with dispositions, and no block of corrections owed to files outside this lane. Every item this firing reported is accounted for in the file as it now stands: entries 20, 29, 30, 32, 33, 34, 36, 41, 45 and 47 stand as open entries under `## Open decisions for human to make` with their identifiers unchanged, and the two code markers (`code-4`, the queued waiter's wait bound, and `code-8`, the failed compensating drain) stand as bullets under `## Defects in the shipped tree that this proposal does not stage`, as written.

FACT: the preamble of `## Open decisions for human to make` was read against the entries the section now carries and needed no correction. It states which entries left and by what route, names entries 20, 32, 33, 36, 41 and 45 as carrying question and ground with no recommendation, and states that entries 29 and 30 carry a recommendation and entry 34 carries ground, alternatives, cost and confidence without one, all of which the entries bear out. `## Deliverable index` was preserved line for line in last position.

### [non-spec.14.fix-G2.1]

- FACT: CONF-1's deliverable heading now reads `### CONF-1 · tests/tier3_contract/adapter_bind_attempt/, tests/tier10_conformance/slot_bind_attempt_conformance_test.go, scripts/seed-claim-register.py, tests/claim-map.json · …`, so the deliverable that lands the `ABSENT` claim-register row names the two files that row is written and regenerated in. The omission was drift from this round's own move of that row's owner from SCHEMA-1 to CONF-1: CONF-1's Testing block states "The `ABSENT` claim-register row SCHEMA-1 stages lands with it" and the files-touched list states "the two `WIRED` rows with SCHEMA-1, the `ABSENT` row with CONF-1", while the heading still listed the two test files alone. Every heading in the file enumerates the files its deliverable touches, as SCHEMA-1's does with the same two files.
- FACT: SCHEMA-1's heading was left as it stands. It stages all three rows and lands the two `WIRED` ones, so `scripts/seed-claim-register.py` and `tests/claim-map.json` belong in its file list as well as in CONF-1's. Nothing else in either deliverable was touched, and implementation-checklist S22 already carries the row under the seeding convention its preamble states.

### [non-spec.14.fix-G1.1]

DECISION: declared the untokened-entry counter's accessor once, in CODE-9's registration clause, as the package-level `func incSlotShutdownUntokenedEntry(podID string)` in `pkg/adapter/metrics.go`, and changed CODE-1's call site to `incSlotShutdownUntokenedEntry(s.podID)` — BECAUSE CODE-9 already owns the series and the checklist already orders S10 before S16, so the cross-deliverable call compiles, and the shipped file holds no method on `*Server` — ALTERNATIVES: moving `pkg/adapter/metrics.go` into CODE-1/S16 (splits the adapter metric surface from the deliverable that reasons about its catalog gating); keeping `sessionID` as a second label (contradicts SPEC-6's locked §16.1 row); spending `sessionID` on a `slog` line (text growth on a declaration gap); exporting the accessor (breaks the file's convention).

FACT: every `func` in `pkg/adapter/metrics.go` is package-level; the file declares no method on `*Server`. The in-package accessor form is `func incX(labels ...string)`. — EVIDENCE: pkg/adapter/metrics.go:117, :160, :166, :190, :215, :222

FACT: the adapter's `k8s_pod_name` value is `Server.podID`, set once in `New` from the `POD_NAME` env and never re-set, so it needs no lock at the call site. It is empty when the env is absent, which the gateway rejects on the scrub-report path. — EVIDENCE: pkg/adapter/server.go:196, :380

WATCHOUT: the accessor is declared by S10 and first called by S16, so between those two steps the registration var and the accessor have no caller inside `pkg/adapter`. `staticcheck`'s `unused` may flag the accessor on S10's own tier-0 run. The fix is a case in S10, not an exported symbol: exporting it would break the file's convention and hide the ordering. Nobody has staged that case. — EVIDENCE: implementation-checklist.md:35 (S10), :47-48 (S16, `Depends on: … S10 …`)

DEFERRED [spec-changes.md]: SPEC-6's §16.1 row labels this series "by `k8s_pod_name`", while every other adapter-emitted series registers label-free and lets the Kubernetes scrape target supply the pod label. The row is not wrong, and the staged code now matches it, but the row is the reason this series carries a label its siblings do not. A later round or the human decides whether the row should drop `labeled by k8s_pod_name`; the spec staging is locked for this run.

CORRECTS [the archived WATCHOUT reading `s.noteShutdownMetUntokenedEntry` as "a method, so it can read `s.podID`"]: the receiver was never needed. A package-level accessor taking the pod identity as an argument reads the same value and matches the file.

### [non-spec.14.fix-G2.1]

DECISION: the `ABSENT` claim-register row lands at S22 with CONF-1's tier-10 file, and SCHEMA-1's `**Claim register.**` opening sentence now says so once — BECAUSE the row's own staged `surface` names `tests/tier3_contract/adapter_bind_attempt/` and `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`, which S22 creates, and CONF-1's Testing block plus checklist S22 already said S22 — ALTERNATIVES: keying everything to S9 (rejected: lands a row whose surface names files that do not exist for eight more steps, and edits two sites to fix one); moving the JSON row object out of SCHEMA-1's block into CONF-1 (rejected: splits one generator list across two deliverables and invites CONF-1 to restate the seeding convention).
WATCHOUT: adding the same `claim` string to the `EXPLICIT` list at both S9 and S22 turns tier 0 red with "appears more than once" — EVIDENCE: tests/tier0_static/claim_register_test.go:409-415
FACT: the tier-0 claim-register validator checks a row's status, anchor, `deferral_id` and surface FORM, and never that a surface names an existing file, so a forward-looking surface passes and a misplaced row is not caught by any gate — EVIDENCE: tests/tier0_static/claim_register_test.go:274-281
FACT: the seeding convention (edit the `EXPLICIT` list in `scripts/seed-claim-register.py`, regenerate in the same commit) has one home in the checklist preamble — EVIDENCE: 0081...implementation-checklist.md:7-9. SCHEMA-1's paragraph now cites it instead of restating it.
FACT: summary.md:1175's register end state (79 rows, 21 `ABSENT`, 24 `UNWIRED`, 34 `WIRED`) is the state after BOTH steps and stays true; only the attribution of all three rows to SCHEMA-1 was wrong. Do not recompute those figures to 78/20/24/34.

### [non-spec.14.fix-design-G1.1]

DECISION: `noteShutdownMetUntokenedEntry` gets ONE home in CODE-9's clause that already names `pkg/adapter/metrics.go`, as the package-level `func incSlotShutdownUntokenedEntry(podID string)` beside `incUnaddressedFrameRejected`, with `slotShutdownUntokenedEntry` registered as a `mustCounterVec` over the single label `k8s_pod_name`; CODE-1's call site at non-spec-changes.md:334 becomes `incSlotShutdownUntokenedEntry(s.podID)` and carries no `sessionID` — BECAUSE CODE-9 already owns the series, the shipped file holds no `*Server` method (`pkg/adapter/metrics.go:117-226`, every `func` package-level), the checklist already makes S16 (CODE-1) depend on S10 (CODE-9), and SPEC-6's locked §16.1 row labels the series by `k8s_pod_name` alone, which the adapter holds as `s.podID` (`pkg/adapter/server.go:196`, set from `POD_NAME` at `:380`) — ALTERNATIVES: declaring it in CODE-1's Targets (splits the adapter metric surface off from the deliverable that reasons about its catalog and docs gating, and makes CODE-9's header, S10's site list and the files-touched row all wrong); keeping `sessionID` as a second label (mints a session-cardinality series the §16.1 row does not describe, and the spec staging is locked); keeping `sessionID` for a `slog` line beside the counter (real precedent at `pkg/adapter/sessionscrubreporter.go:78-84`, rejected as text growth on a fix whose defect is an undeclared symbol, and the residue's identity is recoverable from the slot directory); exporting the accessor to dodge `unused` (breaks the file's convention that in-package accessors are unexported).

FACT: the adapter's own §16.1 rows for self-emitted metrics (`lenny_adapter_sopeercred_*`, `lenny_adapter_coordinator_hold`) carry NO explicit pod label and their registrations are label-free (`spec/16_observability.md:185-189`, `pkg/adapter/metrics.go:15-112`). The staged untokened-entry row is the first adapter-emitted series to name `k8s_pod_name`, so it is the first adapter registration that needs an explicit label vector rather than a scrape-target label — EVIDENCE: `…spec-changes.md:1222`, `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:187-192` (the gateway pattern, where the label is explicit because the series names ANOTHER pod).

DEFERRED [spec-changes.md]: an explicitly-emitted `k8s_pod_name` on a metric the adapter emits about ITSELF duplicates the label a Kubernetes-SD scrape target already attaches, and Prometheus renames the emitted one to `exported_k8s_pod_name` unless `honor_labels` is set. Nothing is false in the staged non-spec text once the accessor takes `podID`, so this is not filed; the claim that would change is SPEC-6's §16.1 row (`…spec-changes.md:1222`), which could drop `labeled by k8s_pod_name` and let the scrape target supply it, as every other adapter-emitted row already does. The spec lane is locked for this run.

WATCHOUT: S10 (CODE-9) registers `slotShutdownUntokenedEntry` in `pkg/adapter/metrics.go` while its only caller lands at S16 (CODE-1), and `.golangci.yml` enables `unused` with `run.tests: true`. An unexported package-level var and func with no reference anywhere between S10 and S16 is a tier-0 failure AT S10. This exposure exists in the staging today for the registration var alone, so the accessor does not create it and this round does not fix it. Whoever files it: the cheap remedy is an adapter-package tier-1 collector case at S10 (a test reference counts as a use under `run.tests: true`), which is the same form S10 already owns for the gateway collector — EVIDENCE: `.golangci.yml:11-20`, implementation-checklist.md S10 ("this step's tier-1 work is the catalog transcription and the collectors"; "the assertions that each counter fires belong to S16 and S19").

USEFUL [Standing context]: "The adapter DOES hold its own pod identity, `POD_NAME` … cached as `s.podID`, so the adapter-side `k8s_pod_name` label is emittable" and "SPEC-6 must land before CODE-9" together fixed both the label source and the owning deliverable without re-deriving either.


### [non-spec.14.fix-design-G2.1]
DECISION: the `ABSENT` claim-register row lands at S22 with CONF-1, not at S9 with SCHEMA-1 — BECAUSE its own staged `surface` field names `tests/tier3_contract/adapter_bind_attempt/` and `tests/tier10_conformance/slot_bind_attempt_conformance_test.go`, both created by S22 (non-spec-changes.md:2691; implementation-checklist.md:59) — ALTERNATIVES: land all three at S9 (rejected: a row would point at files that do not exist for eight steps, and the row's whole subject is CONF-1's battery); move the staged JSON row block out of SCHEMA-1 into CONF-1 (rejected: it would split one generator-input list across two deliverables and invite CONF-1 to restate the seeding convention; one sentence naming the landing step is the smaller fix).
FACT: the seeding convention ("a step that changes the wire or adds a normative claim adds its rows by editing the `EXPLICIT` list and regenerating in the same commit") is already stated once in the checklist preamble at implementation-checklist.md:7-9, so SCHEMA-1's own `**Claim register.**` sentence restating it is a duplicate, not a needed statement. — EVIDENCE: 0081...implementation-checklist.md:7-9
FACT: the register arithmetic is unchanged by this fix. Today 76 rows / 20 ABSENT / 24 UNWIRED / 32 WIRED; after S9 78/20/24/34; after S22 79/21/24/34. Only the ATTRIBUTION of the three rows to a single step is wrong at summary.md:1175 (both the impacts cell's count sentence and its "must do" sentence "once the schema step has landed its three rows"). — EVIDENCE: summary.md:1175
WATCHOUT: `tests/tier0_static/claim_register_test.go` never checks that a `surface` names an existing file (:274-281), so the wrong placement is silent at tier 0; only the duplicate-claim case (:409-415) fires, and only if a fixer lands the row twice. The gates cannot settle this question — the design has to.
USEFUL [review-log.md:1273]: the standing UNVERIFIED entry framed the question exactly as the three sites disagree, which made the triage cheap.

### [non-spec.14.review-applicability.1]

FACT: the checklist is clean on lanes, order and Depends-on at round 14. All 26 steps carry one
lane; S1-S6 are the leading spec block; every `Depends on:` names only earlier steps; every
staged deliverable (SPEC-1..6, CODE-1..12, CONF-1, SCHEMA-1, DOCS-1..4) appears in at least one
step; every box is unchecked. Do not re-run this enumeration without a reason.
 — EVIDENCE: implementation-checklist.md:17-68

USEFUL [non-spec.3.review-applicability.1]: the `s.noteShutdownMetUntokenedEntry` declaration gap
is still open at round 14 and is filed again this round. CODE-1 calls it
(non-spec-changes.md:334), CODE-9 states only that the SERIES is registered in
`pkg/adapter/metrics.go` (:2132), and the files-touched row says only "the untokened-entry
series" (:4221). No deliverable states the helper's receiver, signature or declaring file, and
`pkg/adapter/metrics.go` holds only package-level funcs today (verified: `grep -n '^func'` returns
`mustCounter`, `incRotationCeilingHit`, `setCoordinatorHold`, … and no `*Server` method).
 — EVIDENCE: pkg/adapter/metrics.go:117-222

FACT: the claim-register ABSENT row has two landing steps, and the tier-0 gate rejects a
duplicate claim string ("appears more than once"), so following both sites hard-fails tier 0.
SCHEMA-1 says all three rows land with the proto regeneration (non-spec-changes.md:2636-2638),
CONF-1's tier-10 Testing block says the ABSENT row "lands with it" (:3608), checklist S9 names
only the two `WIRED` rows and checklist S22 names the ABSENT row.
 — EVIDENCE: tests/tier0_static/claim_register_test.go:409-415

DECISION: did NOT file the `Open` entry "Is tier 5 reached by CODE-5's third accounting caller?"
BECAUSE the drain mechanism itself is untouched: `resumeOnPod` becomes a new caller of an existing
`DrainSandbox`/`lenny.dev/drain-request` path whose patch is covered at tier 2, and
`test-coverage.md` reaches tier 5 for a NEW cluster behaviour rather than a new call path into an
old one. ALTERNATIVES: filing a missing-tier-5 finding, rejected as hardening.
 — EVIDENCE: non-spec-changes.md:1305-1316

DECISION: did NOT file CODE-2's file list omitting `pkg/adapter/slotsession.go`. CODE-2's header
names four files (non-spec-changes.md:608) while its own text requires widening
`claimSessionSlot`/`claimSessionSlotUnderLock` to report the token, and both live in
`pkg/adapter/slotsession.go` (`:52`, `:64`). BECAUSE the files-touched list does carry the edit
("and the token the two claim functions report", :4164), so no implementor is forced to invent
anything; it fails the lens's own "forced to guess" test. Recorded so the next round does not
re-derive it.

FACT (closed, do not re-check): the `CredentialAssigner` widening now has an edit list. The
interface member is named at non-spec-changes.md:4210 and the fake sweep is defined by a grep
over `ReleaseSession(` call sites assigned to `podsession.Binder.Credentials` (:4294-4301), which
covers `reclaimRecordingAssigner`, `recordingLeaseAssigner`, `failingAssigner`,
`recordingAssigner` and `poolRecordingAssigner`. The Open entry
"Does the `CredentialAssigner` widening reach four unnamed test fakes?" is stale.

FACT (closed): "Does S23's tier list agree with DOCS-4's own?" — both now read 0, 11.
 — EVIDENCE: non-spec-changes.md:2958, implementation-checklist.md:62

FACT: every other new symbol the staged Go code calls IS declared by some deliverable:
`validateBindFields` (:1552), `slotResolveError`/`slotResolveCategory` (:1545), `newBindAttempt`
(:882), `translateSlotBindRefusal` (:1949), `ShutdownReclaim` (:1994-1997),
`releaseSessionSlotUnderGuard` (:4162), `accountSlotFailure` (:1275), `errSlotReclaimInProgress`
(:1529), `runtimeHoldsLocked` (:182), `slotFailureWorkspaceFinalize` (:2160),
`removeSlotTreeVia`/`removeSlotTreeFn` (:4130-4134), `reclaiming`/`slotGuards` (:4127-4128).
`SDKWarmRuntime.DemoteSDK` exists in the tree (pkg/adapter/sdkwarm.go:133). The only gap is the
untokened counter helper.

FACT: DOCS-1 and DOCS-3 anchors all resolve verbatim against the current pages
(docs/reference/state-machines.md:138,235,237,251 and docs/reference/error-catalog.md:129); the
five `ReleaseSlotReservation` call sites in the tree are all covered by CODE-4's table
(non-spec-changes.md:1184-1192).

### [non-spec.14.review-reliability.1]

FACT: the reliability core of the staging traced clean this round. Verified by reading, not by
trusting the text: the two lock orders never cycle (`Shutdown` is s.mu -> release -> guard ->
s.mu, `releaseSessionSlot`/`terminateHeldSession` are guard -> s.mu, `acquireSlotGuardForResolve`
is guard -> s.mu), §10.1.4 pass 1 holds s.mu across every member and takes no guard
(pkg/adapter/holdstate.go:189-205), so pass 2's per-member guard acquisition cannot deadlock
against it. — EVIDENCE: non-spec-changes.md:1888-1897, pkg/adapter/holdstate.go:177-205

FACT: the defer order in CODE-1's handler is correct as written. `defer unlockSlot()` is
registered before the `if completed { release() }` defer, so LIFO runs the hold release first and
the guard release last, which is what the text claims ("the guard outlives the hold"). Do not
"fix" it by swapping them. — EVIDENCE: non-spec-changes.md:358-406

FACT: `slotCleanupBudget(cleanupTimeoutSeconds, maxConcurrentSessions)` cannot divide by zero at
either caller. `slotBindRequest` is built only under `match.MaxConcurrentSessions > 1`
(pkg/gateway/sessionserver/start.go:2536) and `ResumeRequest.MaxConcurrentSessions` is normalized
through `maxConcurrentSessions()` at start.go:4029 (helper at :3353-3359). I spent a round on
this; nobody needs to spend another. — EVIDENCE: pkg/gateway/sessionserver/start.go:3353,:4029

FACT: `exited_cleanly = closeErr == nil && (live || (guarded && treeErr == nil))` agrees term for
term with the expired-acquisition disposition table's `Shutdown` row, on both the `live` and the
pre-`running` arms. The two were written by different hands and do not drift.
— EVIDENCE: non-spec-changes.md:451, non-spec-changes.md:1846

FACT: the `CredentialAssigner` interface widening the caller's directive asks about IS staged.
`Release(leaseID string)` is named as a member "the interface (`binder.go:319-332`) gains"
(non-spec-changes.md:1132-1135) and appears in the files-touched list (:4210); both production
implementations already carry it (`credassign.Service.Release` at credassign.go:380,
`credassign.Client.Release` at client.go:299). The only interface-method gap left in the staged Go
is the one filed below, and it is a `*Server` method rather than an interface.
— EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:319-332

MISTAKE: the standing Open entry **Which deliverable DECLARES `s.noteShutdownMetUntokenedEntry`?**
is marked "OPEN, FILED" and was NOT closed by any later fix round. The call still stands at
non-spec-changes.md:334 with no declaration anywhere. Marking an entry FILED is not the same as
its remedy landing; the next compaction should not read FILED as closed.

CORRECTS [Does S23's tier list "0, 11" agree with DOCS-4's own "Tiers: 0"?]: this one IS closed.
DOCS-4 now reads "Tiers: 0, 11." (non-spec-changes.md:2958). Drop the entry.

UNVERIFIED: the whole-pod recycle scrub `s.startPodScrub(rc)` now runs inside `answerShutdown`,
i.e. on every outcome, and takes no slot guard while deleting on-disk slot directories. I did not
file it: the shipped trailing clause already ran on every non-error path, so the staging widens
reachability only over the two INVALID_ARGUMENT returns, which perform nothing. The standing Open
entry about the scrub racing another slot's `removeSlotTree` is the same area and is still open.
— EVIDENCE: non-spec-changes.md:330-343, pkg/adapter/session.go:283-291

### [non-spec.14.review-security.1]

DECISION: filed two findings, both drawn from the caller's standing instruction to close the
`### Open` entries that name an undeclared symbol or a step disagreement — BECAUSE both are
textual facts in the staging that I re-verified against the tree this round, not leads —
ALTERNATIVES: rejected filing the pod-self-report leak disposition, the in-memory reclaim hold
and the §12.4 leaked-ledger durability question, all of which are pre-existing or explicitly
out of scope.

FACT: the security surface of this staging verifies clean on every control my lens owns.
`cancelPodMCPIfRuntimeIdle` is genuinely double-guarded (`pkg/adapter/slotsession.go:238-260`),
so widening the `Shutdown` gate from `bound` to `removed` cannot cancel a live claimant's
arming. The §15.4.2 drain still gates on `!boundRemains` and narrows from `bound` to `started`,
which only reduces spurious drains. The token is never logged, never put in a refusal message,
and never travels on a response. `codes.Aborted` is produced nowhere in shipped production code
except `pkg/adapter/checkpoint.go:115` (the checkpoint op lock), which no resume path reaches,
so CODE-5's new `isTransientPodClaimError` Aborted arm cannot fail open on an auth error.
EVIDENCE: pkg/adapter/slotsession.go:238-260, pkg/adapter/session.go:259-261,
pkg/adapter/checkpoint.go:115.

FACT: CODE-4's attempt-scoped credential release is sound across BOTH lease sources, and a later
lens should not file it. `assignCredentials` mints pool leases through
`b.Credentials.AssignProto` (`binder.go:1225`) and user-source leases through
`b.UserCredentials.MintProto` (`binder.go:1248`), and the in-tree comment at `binder.go:1244-1246`
states that the user path writes into the same credential-lease store the pool path uses, so
`credassign.Service.Release(leaseID)` (`credassign.go:380`) reclaims either kind by identifier
exactly as `ReleaseSession` (`credassign.go:400-409`) would. Every line number CODE-4 cites for
this path (`:1225`, `:1248`, `:1256`, `credassign.go:380`, `client.go:299`, `credassign.go:400-409`)
is exact. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1216-1256,
pkg/gateway/credentials/credassign/credassign.go:375-409.

USEFUL [the `### Open` entry "Does the `CredentialAssigner` widening reach four unnamed test
fakes?", raised by non-spec.7.review-security.1]: it is now CLOSED by the staging and should be
retired rather than re-filed. The widening is stated at non-spec-changes.md:1132-1137 and the
fake sweep is defined by a grep in the files-touched list (non-spec-changes.md:4294-4301), which
covers `reclaimRecordingAssigner` (`terminal_reclaim_internal_test.go:33`), `failingAssigner` and
`recordingAssigner` (`delegated_child_materialize_test.go:132,:143`), `recordingLeaseAssigner`
(`start_pod_test.go:1160`) and `fakeAssigner` (`binder_test.go:322`) — the whole set
`grep -rn "ReleaseSession(" --include=*_test.go` returns.

WATCHOUT: `pkg/adapter/metrics.go` declares NO `*Server` methods — every helper there is
package-level (`:160-222`). Any future deliverable that stages an `s.note…` call and points at
that file as the home has not given the symbol a home. EVIDENCE: pkg/adapter/metrics.go:117-222.

OPEN: the `### Open` entry "Does S23's tier list '0, 11' agree with DOCS-4's own 'Tiers: 0'?" is
STALE and can be retired: DOCS-4 now reads "Tiers: 0, 11" (non-spec-changes.md:2958).

### [operator.47]

DECISION: open decision 47 answered by the operator on 2026-09-22 with option A. SPEC-3's `**Slot-identifier reclaim hold.**` paragraph now carries one sentence after the definition of **completed**: a removal of the slot's workspace directory performed before every request still writing under the slot's identifier (a `Resume` restoring a checkpoint among them) has stopped writing is an act that did not return without error, whatever the removal returns, and the disposition table's failed-act rows apply to it. The sentence names the ordering rather than the per-slot guard, because the guard is CODE-6's device and the spec states the requirement it discharges. ALTERNATIVES: (B) drop the `guarded` conjunct from the three completion predicates, rejected because a released identifier admits a successor onto a directory a parked `Resume` extraction is still re-creating, which is the residue the hold was staged to close.

CORRECTS [`[f7.apply]`, its OPEN on entry 47]: answered; entry 47 left `## Open decisions for human to make`, and the section preamble records the answer.
CORRECTS [`[f7.apply]` and `[redesign.8.fix.1]`, their DEFERRED on SPEC-3's reclaim-hold paragraph]: applied; the clause is staged, and CODE-6's closing paragraph after the expired-acquisition site table now cites it instead of recording it as owed.
WATCHOUT: the spec lane was unlocked for this edit. A fix that rewrites the reclaim-hold paragraph keeps this sentence, and the disposition table stays the single home of each row's outcome; the sentence adds no row and no cell.

### [spec.27.review-applicability.1]

FACT: the decision-47 sentence (commit 5acaf0b44) is wholly inside SPEC-3's appended code block, after the disposition table, so every artifact it references (the table's failed-act rows, `Resume`) exists at the point it lands. `Resume` is in the rules-1-to-9 admission set (SPEC-5 rule block) and in the shipped §4.7 RPC table. EVIDENCE: spec-changes.md SPEC-3 reclaim-hold paragraph; spec/04_system-components.md:684; schemas/lenny-adapter.proto:151.
FACT: "the rows keyed on a failed act" resolves on every performer: running-slot `Shutdown` maps to "runtime close succeeds and any other act fails", pre-`running` `Shutdown` to "An act fails", outside-a-`Shutdown` to "An act fails after the deregistration". No new row or cell is needed, and no Design or Edge-cases bullet mentions a restore racing a removal, so nothing there contradicts it.
DECISION: no findings this round — BECAUSE the one changed sentence adds no anchor, identifier or heading and replaces no text outside a block already certified by rounds 22 to 26 — ALTERNATIVES: filing a missing tier-1 case for the unordered removal, rejected as a non-spec (CODE-6) remedy out of this loop's scope.

### [spec.27.review-citations.1]

FACT: every "reads, verbatim" anchor in spec-changes.md resolves in spec/ at HEAD; checked mechanically (python substring match of each quoted block against the concatenated spec/*.md). The one apparent miss, the §29.4 step-13 block at spec-changes.md:339, is the ADDED sentence, not a quote; its insertion anchor "([§15.4.3](...), §28.5.3)." is at spec/29_communication-scenarios.md:711 inside §29.4 step 13. EVIDENCE: spec/29_communication-scenarios.md:703-711
FACT: the decision-47 sentence's example is true of the tree: `Resume` extracts the checkpoint into the slot tree (`restoreChunks` -> `workspace.ExtractTree`), so "a `Resume` restoring a checkpoint into that directory" names a real writer under the slot identifier, and rule 6 refuses a `Resume` on a started entry, so the sentence only ever reaches the pre-`running` failed-act rows. EVIDENCE: pkg/adapter/resume.go:97-108,169-206
FACT: SPEC-3 commentary line citations all hold at HEAD: holdstate.go:201 (10s pass-2 close ctx), commit 3997f502b (2026-08-22), spec/11:264 (step 3, 10s), session.go:219-224, socketruntime.go:470-473, mcpruntime.go:86 (5s), and `ShutdownGrace` is set nowhere in non-test code.
WATCHOUT: SPEC-4's `claimed ──→ draining` fence block (spec-changes.md ~855) shows only the REPLACEMENT, not the current text; the current entry is spec/06_warm-pod-model.md:95-97. It is unique within the `Occupancy projection` sub-block (the other four `claimed ──→ draining` entries sit under Recycle edges and concurrent occupancy), so the anchor resolves; a verbatim-matcher will flag it falsely.
USEFUL [operator.47]: its statement of what the sentence adds (no row, no cell) let this pass check the sentence against the table in one read.

### [spec.27.review-client-surface.1]

DECISION: no client-surface finding on the decision-47 sentence in SPEC-3's `**Slot-identifier reclaim hold.**` paragraph — BECAUSE it adds no field, code, status or enum; its only wire effect (a premature workspace removal routes to the disposition table's failed-act rows, so a pre-`running` Shutdown reports an unclean exit and a `running` one keeps the hold) is carried by table cells §4.7.1 rule 15 and the §15.4 reclaim-hold block already cite rather than restate — ALTERNATIVES: filing the §15.4 block as owing a mention of the ordering, rejected because it cites §5.2 for "the reclaim hold, its window", which now includes the sentence.
FACT: the clean-exit flag's per-case values live only in the §5.2 disposition table; §4.7.1 rule 15 defers to it — EVIDENCE: spec-changes.md:618-628, :1066

### [spec.27.review-docs-alignment.1]

DECISION: returned an empty findings list — BECAUSE the decision-47 sentence in SPEC-3's reclaim-hold paragraph lands its observable outcome in staged spec text (it routes an unordered removal to the disposition table's failed-act rows, so the hold is held for the life of the pod and a pre-`running` `Shutdown` reclaim does not set the clean-exit flag), and the matching accepted failure mode (a failed `Resume` whose restore outlasts the compensation budget) is recorded in the non-spec Edge-cases section; no docs/ page describes the reclaim hold (DOCS-2 excludes it, per the standing Deferred entry), so the sentence falsifies no docs surface — ALTERNATIVES: filing the SPEC-3 carrier table for omitting `docs/reference/adapter-contract.md:75` (the `Shutdown` row says the adapter "reports the per-slot cleanup outcome through `ReportSessionScrub`" unconditionally); rejected as bookkeeping, because DOCS-2 already replaces that row, so nothing is left wrong after application.
FACT: `docs/reference/adapter-contract.md:75` (`Shutdown` row) is a second docs carrier of the withdrawn reporting universal, beside `:81`, and SPEC-3's carrier table lists only `:81`. DOCS-2 replaces both rows, so the table's "single home of their dispositions" claim is incomplete but no surface stays wrong. EVIDENCE: docs/reference/adapter-contract.md:75, spec-changes.md SPEC-3 carrier table
CORRECTS [review-log Settled line 302, "No seventh carrier exists"]: its own grep found `adapter-contract.md:75` and `:81` both under DOCS-2, but only `:81` is a row of the carrier table; the table and DOCS-2's scope differ by the `Shutdown` row.

### [spec.27.review-edit-sites.1]

DECISION: no edit-site finding against the decision-47 sentence in SPEC-3's reclaim-hold paragraph (spec-changes.md:632) — BECAUSE the hold, its completion predicate and the "premature removal is a failed act" rule are all new text with no pre-existing spec/ carrier (grep of spec/04, 05, 06, 07 for "still writing", "stopped writing", "removes the slot's workspace" finds only the §5.2 Slot cleanup action list SPEC-3 already replaces); the sentence adds no table row or cell, and every staged site that states hold duration (Edge-cases bullet at :124, §7.1 paragraph at :382, §15.4 hold block at :1110, SPEC-1 DemoteSDK commentary at :311-316) cites the table or §5.2 rather than restating the completion predicate, so none is falsified. ALTERNATIVES: filing the "Spec files touched" §5.2 entry (:1270-1278) for not naming the new sentence — rejected, it describes the paragraph as appended whole and is bookkeeping, not an applied-spec error.
FACT: the §7.2 step-3 / §6.2 mid-resume cancel reclaim is the path where a `Resume` is most plausibly still writing when the reclaiming `Shutdown` arrives; under the new sentence an unguarded removal there lands on table row 3 (reached `running`) or row 5 (pre-`running`: no report, clean-exit not set, `leaked` entered on a concurrent pod, life-of-pod hold). No staged spec text claims that reclaim is clean. EVIDENCE: spec-changes.md:457, :623-624, :632.

### [spec.27.review-fresh.1]

DECISION: no finding on the decision-47 sentence in SPEC-3's `**Slot-identifier reclaim hold.**` paragraph — BECAUSE it adds no row or cell, and the failed-act rows it points at (running + Shutdown "any other act fails", pre-running + Shutdown "An act fails", outside-Shutdown "An act fails after the deregistration") each already carry an outcome for it; the table commentary's "the hold is keyed on every act on every row" stays true — ALTERNATIVES: filing the §7.2 step-3 mid-resume cancel path (compensating Shutdown racing an in-flight `Resume` extraction lands on the pre-`running` failed-act row, leaked + held for the life of the pod) as a contradiction, rejected because that is the consequence the operator chose with option A, and CODE-6's guard bounds how often it happens.
FACT: every "reads, verbatim" anchor in spec-changes.md is still present byte-for-byte in spec/ at HEAD 5acaf0b44; no spec/ commit since 911d93b13. The two non-matching blocks a naive script flags (spec-changes §29.4 step-13 sentence, §6.2 projection pointer sentence) are inserted text, not anchors — EVIDENCE: spec/29_communication-scenarios.md:645, spec/06_warm-pod-model.md:80
FACT: `Resume` claims the slot through `claimSessionSlot` and then writes via `restoreChunks` outside the registry lock, so a `Resume` is a writer that can still be running when a reclaim's removal runs, which the decision-47 sentence depends on — EVIDENCE: pkg/adapter/resume.go:49,103
WATCHOUT: SPEC-1's `Shutdown`-row commentary lists three unconditional-teardown callers (§11.4 revoke fan-out, occupancy-zero recycle edge, `Binder.ReleaseSlot`). `Binder.shutdownAdapter`'s non-recycle arm is a fourth plain-`Shutdown` caller. That is commentary and does not make the applied spec wrong, and the non-spec staging already names it — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:2043; non-spec-changes.md table row "`Binder.shutdownAdapter`, the exclusive-path teardown"

### [spec.27.review-kubernetes.1]

- FACT: the decision-47 sentence added to SPEC-3's `**Slot-identifier reclaim hold.**` paragraph (commit 5acaf0b44) is adapter-internal (a directory removal racing an in-flight `Resume` write counts as a failed act) and touches no CRD, status subresource, finalizer, webhook or controller; the Kubernetes lens has nothing on it. EVIDENCE: `git diff 6f0e6fb8a HEAD -- proposals/0081_*/*.spec-changes.md`
- FACT: the only Kubernetes-bearing staging is SPEC-4's §4.6.1 re-key onto "the phase the pod currently projects". Reading back its own `Sandbox.status.phase` is sound under §4.6.3 (WarmPoolController is sole `Sandbox.status.*` writer; the gateway holds no `sandboxes/status` grant) and matches `ProjectOccupancyPhase` (`pkg/controller/warmpool/occupancy.go:84-144`). EVIDENCE: spec/04_system-components.md:617 (ownership row), :633 (gateway RBAC)
- WATCHOUT: `ProjectOccupancyPhase` returns `("", false)` for a no-claim pod projecting `sdk_connecting` (recycle re-warm leg), and §4.6.1's re-keyed bullets name no edge for it. This predates the proposal (the old §6.2 "no claim projects idle" clause was equally wrong for it) and the staged edits do not make it newly wrong, so it is below the bar; do not re-file it as a SPEC-4 partition defect. EVIDENCE: pkg/controller/warmpool/occupancy.go:128-143
- USEFUL [Open: "Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?"]: it correctly scopes out the one real idiom hazard (no finalizer on the per-pod claim); this round did not re-file it.

### [spec.27.review-mechanism.1]

FACT: the decision-47 sentence in SPEC-3's reclaim-hold paragraph agrees with every table row and with CODE-1/CODE-6's predicates. Pre-`running` Shutdown: `exited_cleanly = closeErr == nil && (live || (guarded && treeErr == nil))` gives row 5 (flag not set, `leaked`, hold held); `running` Shutdown reads the close alone, which is row 3 (`released`, flag set, hold held); the two outside-`Shutdown` sites take the "act fails after the deregistration" row. EVIDENCE: spec-changes.md:618-626 (table), :632 (sentence); non-spec-changes.md:449, :507, :1847-1856.
FACT: Resume restores into TWO roots, `paths.Current` and `paths.Sessions` (pkg/adapter/slot.go:197-204), while the new sentence names only "the slot's workspace directory". That is not drift: the spec/05 `**Slot cleanup:**` bullet (spec/05:545) uses "removes the slot's workspace directory" as the name of the whole tree-removal act, and `slotlayout.RemoveTree` is one act over every directory. Do not file it. EVIDENCE: spec/05_runtime-registry-and-pool-model.md:545; spec/06_warm-pod-model.md:386.
FACT: ExtractTree is synchronous, and the chunk-fetch goroutine writes only to the pipe (pkg/adapter/resume.go:188-208). Resume's own rollback under `releaseSessionSlotUnderGuard` therefore runs after its writing stopped, so the new sentence never marks Resume's self-rollback as failed.
WATCHOUT: Open entries 45, 51 and 66 (claim-register step placement, S22 depends-on, `CredentialAssigner.Release` fakes) meet the caller's "promote an Open" rule, but each one's remedy is non-spec or checklist. They are out of scope in the spec loop, and the non-spec loop owns them.

### [spec.27.review-performance.1]

DECISION: no spec-lane finding on the decision-47 sentence under the performance lens — BECAUSE the sentence adds no row, no write, no store dependency and no new watch; it only routes an already-counted failed-act case into the existing table rows, so no control-plane or datastore write rate changes and Postgres failover, Redis reset and coordinator handoff paths are untouched — ALTERNATIVES: filing the leak-rate/whole-pod-replacement acceleration, rejected because it is the standing Open entry on unanswered reclaims at maxConcurrentSessions 2 and decision 41, not new.

DEFERRED [non-spec-changes.md, CODE-6 "Disposition of an expired acquisition at a removing site" and the accepted failure mode "A member parked under its own guard can cost the §10.1.4 pass its whole guard-acquisition deadline"]: the staged spec sentence makes a removal a failed act only when it runs "before every request still writing under the slot's identifier ... has stopped writing". In the §10.1.4 pass, one member parked in a Resume extraction exhausts the pass's single ten-second guard-acquisition context, and the accepted-failure bullet says every REMAINING member then takes the expired disposition (hold retained for the life of the pod). Those remaining members have no writer under their own identifiers, so under the spec sentence their cleanup completed when close and tree removal returned nil and the hold should end. CODE-6's closing claim "an expired acquisition is the case that sentence names" is therefore false for those members: the code over-holds relative to the spec. Remedy is code-lane (per-member acquisition context, or narrowing the accepted-failure text), not spec. Out of scope for the spec loop.

FACT: the decision-47 sentence is at spec-changes.md in the `**Slot-identifier reclaim hold.**` paragraph (second sentence); CODE-6's disposition cites it and the `guarded` conjunct implements it. EVIDENCE: spec-changes.md:632, non-spec-changes.md:1831-1869, :4016-4024.

### [spec.27.review-reliability.1]

DECISION: no finding on the decision-47 sentence in SPEC-3's `**Slot-identifier reclaim hold.**` paragraph — BECAUSE it adds no row or cell, routes a premature removal onto the existing failed-act rows (Shutdown running arm: `released`/set/held; pre-`running` arm: none/not set/`leaked`/held; outside-Shutdown arm: held), and the residue it leaves (a tree a late `Resume` extraction re-creates) is already covered by the paragraph after the table (pod termination or whole-pod scrub ends it) — ALTERNATIVES: filing the credential-directory half of the same removal as unordered; rejected because `AssignCredentials` writes `credentials.json` inside `s.mu` (CODE-6 guard table), so no credential writer can outlive the deregistration.
FACT: the only writers that can still be writing under an identifier after its deregistration are the guarded sections (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `Resume`); `Checkpoint` only reads and takes no guard — EVIDENCE: non-spec-changes.md CODE-6 `*Scope.*` derivation table.
WATCHOUT: on the compensation path the premature-removal case is unobservable to the gateway beyond what it already records, because the guard acquisition expires only with the compensation's own deadline — EVIDENCE: non-spec-changes.md, paragraph after the expired-acquisition site table. Do not file "no report distinguishes an unordered removal" as a reliability gap.

### [spec.27.review-security.1]
- DECISION: no security finding against the decision-47 sentence in SPEC-3's `**Slot-identifier reclaim hold.**` paragraph — BECAUSE classifying an unordered workspace removal as a failed act routes it to the table's fail-closed rows (hold retained for the pod's life; `leaked` entered on a concurrent pod's pre-`running` Shutdown row), which tightens rather than relaxes the residue control — ALTERNATIVES: filing the whole-pod scrub (staged §4.7 text: runs "whatever outcome that request answers") as a second unordered removal racing the same `Resume`; rejected because a `leaked` slot stays counted in Redis occupancy (spec/06_warm-pod-model.md:160), so the occupancy-zero recycle edge cannot fire while that slot's `Resume` is still extracting, and a one-session pod whose bind fails drains rather than recycles.
- FACT: `Resume` restores into both `paths.Current` and `paths.Sessions` (pkg/adapter/slot.go:183-206, resume.go:206), so the sentence's "into that directory" is best read as the slot's workspace tree. Not a finding: the hold covers both, and the non-spec per-site table keys on `removeSlotTreeVia` as a whole — EVIDENCE: pkg/adapter/slot.go:197-205
- USEFUL [spec.24.review-security.1]: the `binder.go:2043` plain-`Shutdown` question is closed in the staging: non-spec-changes.md:899 and :948 have `Binder.shutdownAdapter` set `unconditional_teardown`. That settles the Open entry "Does `Binder.shutdownAdapter`'s retire arm set `unconditional_teardown`?"; compaction can retire it.
- WATCHOUT: the non-spec disposition paragraph ("an acquisition that expired is ... an act that did not return without error, whatever the unguarded removal itself returns, because ...") now repeats the rule the decision-47 sentence states in SPEC-3, and then cites that sentence. The spec side is the home, so any reduction is a non-spec edit. It is out of scope for the spec loop and was not filed — EVIDENCE: non-spec-changes.md, the `**Disposition of an expired acquisition at a removing site.**` paragraph

### [spec.27.review-single-source.1]
FACT: the decision-47 sentence (commit 5acaf0b44) has exactly one stating site in spec-changes.md, SPEC-3's `**Slot-identifier reclaim hold.**` paragraph, and it reaches dispositions only by citing the §5.2 table ("the table above applies the rows keyed on a failed act to it"). No §4.7.1 rule, §15.4 block, §4.7 row, Design paragraph or Edge-cases bullet restates it. — EVIDENCE: spec-changes.md SPEC-3 append (grep "stopped writing" hits only that paragraph in spec-changes.md; spec/ has no hit)
FACT: the non-spec CODE-6 "Disposition of an expired acquisition" paragraph and summary entry 47 name the sentence and attribute it to SPEC-3 rather than stating a second home; they read as citations under the (g) bar. — EVIDENCE: non-spec-changes.md "SPEC-3's `**Slot-identifier reclaim hold.**` paragraph states that ..."; summary.md entry-47 paragraph
DECISION: returned no findings — BECAUSE every rule inventoried (completion predicate, hold scope, ABORTED status, table dispositions, rules 1-15, critical section) has one stating site with citations elsewhere — ALTERNATIVES: filing the §16.1 superseded-metric row's gloss of rule 15 was rejected as a one-clause description, not an implementable restatement.

### [index-reconcile.4]

FACT: the staged spec deliverables are SPEC-1 through SPEC-6 and the staged non-spec deliverables are CODE-1 through CODE-12, CONF-1, SCHEMA-1 and DOCS-1 through DOCS-4; the deliverable index lists each once. Index lines corrected against the staging: SPEC-1's §4.1 clause (one replaced sentence delegating to §4.7, with the scrub clause citing the teardown-pairing rule, in place of a per-message restatement citing the carriage table), SPEC-3's one-report clause ("at most one") and its reclaim-hold clause (gains the decision-47 sentence), SPEC-5's §15.4 clause (two new blocks rather than a re-cut of an existing one), and CONF-1's file list (gains the two claim-register files its heading names). EVIDENCE: spec-changes.md `## Staged edits`; non-spec-changes.md deliverable headings.
FACT: the SPEC-lane block keeps its ids and order (S1 SPEC-5, S2 SPEC-1, S3 SPEC-2, S4 SPEC-3, S5 SPEC-4, S6 SPEC-6). Step lines corrected against the staging: S1 (§15.4 gains two blocks; the §15.1 row takes no remedy cell, which is DOCS-3's), S2 (the §4.1 scrub clause), S3 (the acknowledged-clean definition and the §4.7.1 citation, in place of "a reclaim that did not complete"), and S4 (at most one report per release; the decision-47 sentence in the reclaim-hold paragraph).
FACT: `Depends on:` reconciled so each code step names the spec steps staging what it implements: S10 gains S1 (the `superseded` outcome), S15 gains S4 (the reclaim hold the guard orders against), S17 gains S1 (the admitted-start precondition), S19 gains S4 (the disposition table), S20 gains S1 (the `ABORTED` answers), and S22 gains S4 (the reclaim-hold cases).
CORRECTS [### Open, "Does checklist S22 (CONF-1) owe S18 in its `Depends on:`?"]: already satisfied; S22 names S18.
CORRECTS [### Open, "In which step do SCHEMA-1's three claim-register rows land?"]: already settled in the staging; SCHEMA-1's **Claim register.** paragraph lands the two `WIRED` rows at S9 and the `ABSENT` row with CONF-1 at S22, and the index's CONF-1 line now says so.
CORRECTS [### Deferred, "non-spec-changes.md, CODE-10's grep"]: no edit; already closed by the `CORRECTS` at `[non-spec.10.review-fresh.1]` and `[non-spec.10.review-mechanism.1]` (the diagnosis tail is one carrier, surfaced by the seventh alternation). Compaction can retire the entry.
CORRECTS [`[redesign.8.fix.1]` and `[f7.apply]`, their DEFERRED on SPEC-3's reclaim-hold paragraph]: no edit; already closed by `[operator.47]`.
OPEN [pkg/adapter/session.go, resume.go, sdkwarm.go]: the pre-`Runtime.Start` failure branches release by session identifier alone; carried in the summary's unstaged-defects section for a later proposal.
OPEN [non-spec-changes.md, CODE-7]: the `Client.Shutdown` doc comment in `pkg/gateway/runtime/adapterclient/client.go` says a zero deadline lets the adapter apply its default grace, which `resolveShutdownGrace` contradicts; CODE-7 opens the file and stages no repair. Staging the one-sentence repair is authoring a code change, so it is left for the non-spec loop.
OPEN [non-spec-changes.md, SCHEMA-1]: the `ErrorCode` enum header in `schemas/lenny-adapter.proto` says the catalog mirrors §15.1, which is false for codes 27, 28 and 29; SCHEMA-1 stages no replacement for it.
OPEN [spec-changes.md, commentary]: the untokened-entry commentary calls the §10.1 hold-timeout termination "a request" where §5.2 says it runs under no request.
OPEN [pkg/apis/lenny/v1alpha1/sandbox_types.go]: the `Sandbox.status.phase` doc comment lacks the projection input SPEC-4 adds; record only, and any fix goes through the Go comment plus `make generate`.
OPEN [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md` gives the §15.4 reclaim hold's `ABORTED` refusal neither a sentence nor an exclusion rationale of its own; DOCS-2's rationale names the cascade and its wire observables only. Mirroring it, or stating why it is excluded, is new DOCS-2 content.
OPEN [docs/api/internal.md]: the gRPC status table omits `ABORTED`; pre-existing (`pkg/adapter/checkpoint.go:115` already answers it), for a separate finding.
OPEN [non-spec-changes.md, SCHEMA-1]: the proto `LEAKED` comment's drain-ledger sentence may restate a rule whose home is §4.6.3 or §5.2; SPEC-3 does not falsify it, and a reduction to a citation is a finding of its own.
OPEN [non-spec-changes.md, CODE-6]: the closing claim of **Disposition of an expired acquisition at a removing site**, "an expired acquisition is the case that sentence names", is false for a §10.1.4 pass member whose acquisition expired because another member's park spent the pass's single guard-acquisition context: that member has no writer under its own identifier, so under SPEC-3's reclaim-hold sentence its cleanup completed, while CODE-6 retains its hold for the life of the pod. The remedy is a design choice in the code lane (a per-member acquisition context, or restating the accepted failure mode and the disposition for that member), so it is authoring rather than a repair and is left for the non-spec loop.
OPEN [spec-changes.md, SPEC-6]: whether the untokened-entry row drops `labeled by k8s_pod_name` (the two `[non-spec.14.*]` DEFERRED entries). Routed to the summary as open decision 48.
OPEN [non-spec-changes.md and spec-changes.md]: whether channel-naming N8's ban on specification line-number citations reaches proposal prose (the `[non-spec.10.fix-G2.1]` and `[non-spec.10.fix-design-G2.1]` DEFERRED entries). Routed to the summary as open decision 49.
OPEN [implementation-checklist.md]: CODE-1 (S16, S17, S21), CODE-4 (S13, S19) and CODE-6 (S14, S15, S21) each appear in more than one step, against the checklist rule that every staged deliverable appears in exactly one step. This pass rewrites no non-spec step, so the split stands for the next loop to settle.

### [operator.20-41-45]

DECISION: open decisions 20, 41 and 45 answered by the operator on 2026-09-22. (20) The two new `ErrorCode` values take 28 and 29, as SCHEMA-1 stages them: the enum has run contiguously 1 to 27 with each addition taking the next value, and the 1000-1999 comment reserves nothing in the proto and exists for a wire-compat audit a pre-deployment platform does not run. (41) One transient `ABORTED` answer stands on both arms of the reclaim hold: a life-of-the-pod hold is permanent for that pod and not for the session, so a permanent status would tell the client not to retry a request another pod would admit; the entry reaper at remediation position 2 carries the remedy for a stranded hold. (45) `lenny_adapter_leaked_slots` takes a §16.1 row under SPEC-6 and a `catalog.go` entry, a `spec161Metrics` entry and a `docs/reference/metrics.md` row under CODE-9, because this proposal widens which failures reach the gauge (the `accountSlotFailure` callers, and `leaked` keyed on the §7.1 acknowledged-clean predicate) and already rewrites its comments. ALTERNATIVES: 20, the Phase-2 range, rejected as a numbering scheme nobody defined; 41, a distinct permanent status, rejected because the right signal is "retry elsewhere", which needs gateway pod exclusion outside this proposal; 45, recording the gap as unstaged, rejected because the series changes meaning with no published definition.

FACT: the §6.2 **`leaked` slot semantics** claim that the adapter exposes `leaked_slots` in `/healthz` is false in the tree: no adapter handler publishes it, and the gauge is registered in `pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go` and set from `applySlotRetryPolicy` in `pkg/gateway/sessionserver/start.go`. SPEC-6's row names no emitter, and the attribution is recorded in the summary's unstaged-defects list rather than corrected here.

CORRECTS [`### Open`, the entries for decisions 20, 41 and 45, the `/healthz` UNVERIFIED and "Do open decisions 41 and 45 get a recommendation?"]: answered and removed.
WATCHOUT: the gauge's labels are `pod_id` and `pool`, unlike the `k8s_pod_name` the slot counters carry; `gatewaymetrics_elicitation_test.go` pins them. Do not "align" the SPEC-6 row to `k8s_pod_name`.

### [operator.48-49]

DECISION: open decisions 48 and 49 answered by the operator on 2026-09-22. (48) `lenny_slot_shutdown_untokened_entry_total` carries no label: SPEC-6's row says so, CODE-9 registers it with `mustCounter` and its accessor `incSlotShutdownUntokenedEntry()` takes no argument, because every series the adapter emits takes the pod label its scrape target attaches and an emitted `k8s_pod_name` is renamed `exported_k8s_pod_name`. (49) Proposal files are outside rule N8, as `scripts/specshift/scope/scope.go` already treats them (`readExcludedPrefix = "proposals/"`); this proposal's specification line citations stand, and staged edits keep locating their targets by heading and quoted text. A separate proposal states the exclusion in §28.1 and moves the general citation rule out of the naming law.
CORRECTS [the DEFERRED on the untokened counter's `k8s_pod_name` label, and the DEFERRED on specification line citations in the proposal files]: both closed by this entry.
WATCHOUT: do not file a specification line citation in a proposal file as an N8 violation, and do not reintroduce a pod label on the untokened-entry counter.

### [spec.28.fix-G1.1]
DECISION: SPEC-6's `lenny_adapter_leaked_slots` row now documents `pod_id` in-row as a local-only extension of the §16.1.1 `k8s.pod.name` attribute, anchor `#1611-attribute-naming` (heading at spec/16_observability.md:289) — BECAUSE §16.1.1 (spec/16_observability.md:293) admits a label only when it is in the table or documented as a local-only extension, and the row is the only site owning the label — ALTERNATIVES: rename to `k8s_pod_name` (rejected: decision 45 settled `pod_id`, code pins it at pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:225); a §16.1.1 table note (rejected: a second staged site stating the same extension).
WATCHOUT: do not also stage a §16.1.1 edit for `pod_id`; the extension is recorded once, in the SPEC-6 row. No other spec section uses the phrase "local-only extension" today, so there is no in-tree precedent format to match — EVIDENCE: grep -rn "local-only extension" spec/ returns only spec/16_observability.md:293.

### [spec.28.fix-design-G1.1]
DECISION: document `pod_id` as a local-only extension of §16.1.1's `k8s.pod.name` inside the SPEC-6 `Leaked session slots` row (one clause), not in a §16.1.1 note — BECAUSE §16.1.1 (spec/16_observability.md:293) admits "documented as a local-only extension of one of these entries" without saying where, and the row is the only site that owns this label; a §16.1.1 edit would add a second staged site in a table no other SPEC-6 row touches — ALTERNATIVES: relabel to `k8s_pod_name` (rejected, decision 45 and the shipped registration at pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-226 fix `pod_id`); doing both (rejected, restates the rule twice).
WATCHOUT: spec/06_warm-pod-model.md:160 already names the gauge "labeled by `pod_id` and `pool`" in shipped spec; that is pre-existing and needs no edit once §16.1 documents the extension. Do not stage an edit to §6.2 for this.
CORRECTS [archive 42924]: "`lenny_adapter_leaked_slots` already ships with a `pod_id` label that appears in no §16.1.1 row" was true of spec/16 while the gauge had no §16.1 row; once SPEC-6 adds the row, §16.1.1 (spec/16:293) requires the extension to be documented.

### [spec.28.review-applicability.1]

FACT: the only spec-changes.md deltas since baseline 362f75e7c are the decision-47 sentence in SPEC-3's reclaim-hold paragraph (swept by round 27) and SPEC-6's untokened-row relabel plus the new `lenny_adapter_leaked_slots` Gauge row. SPEC-6's anchor "beside the session-slot failure count" resolves uniquely to spec/16_observability.md:14 inside `### 16.1 Metrics`; the new rows keep the two-column `| desc | Type |` layout. EVIDENCE: git diff 362f75e7c HEAD -- proposals/0081_*/*.spec-changes.md
FACT: no existing gate hard-fails on the new §16.1 rows. tests/tier11_docs/adapter_metric_catalog_test.go reconciles only registered adapter metrics toward the catalogs; catalog_test.go's spec161Metrics is a hand list the non-spec CODE-9 staging already extends with the gauge; no gate parses §16.1 labels against §16.1.1. EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:84-125, pkg/observability/metrics/catalog_test.go:188-211
FACT: the SPEC-3 whole-pod trigger replacement keeps both strings TestLeakedSlotCountingLifetimeAgrees_F5231 requires ("counted within a rolling 5-minute window", "counted persistently for as long as the slots remain leaked"), because only the `leaked` parenthetical is replaced. EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:91-96; spec/05_runtime-registry-and-pool-model.md:561
MISTAKE: the operator's decision-45 edit added a Gauge row to SPEC-6 but left the `## Spec files touched` spec/16 bullet and the docs paragraph after it saying "one row for each counter", which now excludes the gauge. Filed as bookkeeping this round.
WATCHOUT: `pod_id` on the gauge is outside the §16.1.1 table, but the operator settled the labels ([operator.20-41-45] WATCHOUT); do not file it.

### [spec.28.review-citations.1]

FACT: the SPEC-6 `lenny_adapter_leaked_slots` row (commit 6272cdfea) is true of the tree: the gauge is a GaugeVec with labels `pod_id`, `pool`, registered in the gateway (not the adapter) and set from `applySlotRetryPolicy` after `slots.MarkLeaked`; §16.1 carries no row for it today, and the `#62-pod-state-machine` anchor resolves (spec/06_warm-pod-model.md:78). EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-225; pkg/gateway/sessionserver/start.go:2836-2845; spec/16_observability.md (no `leaked_slots` hit).
FACT: no spec/ commit since 911d93b13, so round 27's mechanical verbatim-anchor check still holds; the only spec-changes diff since 5acaf0b44 is the SPEC-6 hunk (gauge sentence and row, untokened row unlabeled). EVIDENCE: `git diff 5acaf0b44 HEAD -- proposals/0081_*/*.spec-changes.md`.
MISTAKE: the operator edit adding the gauge row left the `## Spec files touched` §16.1 entry ("one row for each counter the compensation's caller and the adapter's fail-closed arm emit") and the docs-mirror paragraph ("`docs/reference/metrics.md` gains the row for each counter") enumerating counters only; filed as bookkeeping this round.
USEFUL [operator.20-41-45]: its WATCHOUT on `pod_id`/`pool` versus `k8s_pod_name` saved a false label finding.

### [spec.28.review-client-surface.1]
FACT: the three SPEC-6 §16.1 rows agree with the tree and with CODE-9 on name and labels: `lenny_adapter_leaked_slots{pod_id,pool}` is registered by the gateway (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-226), `lenny_slot_compensation_superseded_total{pool,k8s_pod_name}` follows `lenny_slot_failure_total` (spec/16_observability.md:14), and the untokened counter is unlabeled in both SPEC-6 and CODE-9's `incSlotShutdownUntokenedEntry()`. EVIDENCE: non-spec-changes.md CODE-9 block.
FACT: the spec names no adapter `ErrorCode` enumeration anywhere (grep for PROTOCOL_VERSION_INCOMPATIBLE and siblings returns no spec table), so SCHEMA-1's two new codes owe no spec list row. §15.4 names the proto as the published artifact at spec/15_external-api-surface.md:1462.
FACT: `GET /v1/sessions/{id}/setup-output` has no spec statement a refused setup-command request would falsify (spec/15:690, :703 only).
USEFUL [Settled, "The six spec SETUP_COMMAND_FAILED sites are one-directional implications"]: saved re-deriving spec/07:208 and the §15.1 endpoint rows under the widened cause.
USEFUL [Settled, "Only FIVE spec sites name Shutdown"]: spec/05:459's field enumeration for the recycle Shutdown was already dispositioned.
WATCHOUT: the `## Spec files touched` spec/16 bullet still says "one row for each counter" while SPEC-6 now stages a gauge row too; bookkeeping only, since the staged block carries all three rows. EVIDENCE: spec-changes.md `## Spec files touched`, spec/16 bullet.

### [spec.28.review-docs-alignment.1]

FACT: spec/16 §16.1.1 declares its table the single source of truth for metric label names ("A label/attribute must not appear anywhere in the spec unless it is in this table or documented as a local-only extension of one of these entries"), and `pod_id` is not in it; `k8s.pod.name`/`k8s_pod_name` is the table's pod attribute. No §16.1 row carries `pod_id` today; only spec/06 `leaked` slot semantics does. EVIDENCE: spec/16_observability.md:293, :297
DECISION: filed SPEC-6's `lenny_adapter_leaked_slots` row as adding `pod_id` to §16.1 with no §16.1.1 entry or local-only-extension clause — BECAUSE the operator fixed the label as `pod_id` (review-log `[operator.20-41-45]` WATCHOUT), so the remedy is a clause documenting it as a local-only extension, never a rename — ALTERNATIVES: renaming to `k8s_pod_name`, rejected as reversing the operator's answer and the shipped registration.
FACT: the shipped gauge is zeroed at the whole-pod drain (`slots.ForgetPod` then a zero write), not at pod termination; the SPEC-6 row mirrors spec/06's "persists until pod termination". Code-side looseness, not filed. EVIDENCE: pkg/gateway/sessionserver/start.go:2855-2870

### [spec.28.review-edit-sites.1]
FACT: `pod_id` appears nowhere in spec/16 before SPEC-6, and §16.1.1's single-source label table (spec/16_observability.md:293-306) lists `k8s_pod_name` but not `pod_id`; the table's own rule is "A label/attribute must not appear anywhere in the spec unless it is in this table or documented as a local-only extension". The only prior spec use of the label is §6.2's `leaked` slot semantics paragraph (spec/06_warm-pod-model.md:160). EVIDENCE: grep -n pod_id spec/16_observability.md (empty).
WATCHOUT: the remedy for the gauge row's `pod_id` is a local-only-extension statement, never relabeling to `k8s_pod_name`; the shipped gauge's labels are pinned by pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:930. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-225
USEFUL [spec.15.review-edit-sites.1]: its "label NAME must appear in §16.1.1" reading is what makes `pod_id` a finding while `k8s_pod_name` and `pool` were not.
FACT: §6.2's attribution of `lenny_adapter_leaked_slots` to the adapter is recorded as an unstaged defect by operator decision 45 (summary "§6.2 attributes `lenny_adapter_leaked_slots` to the adapter"); not an edit-site finding.

### [spec.28.review-fresh.1]

FACT: `lenny_adapter_leaked_slots` is gateway-registered (`pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-226`, labels `pod_id`, `pool`), appears in no `tests/` gate, no `docs/` page and no §16.1 row today, so SPEC-6's new gauge row trips no pending/exemption list (`tests/tier11_docs/adapter_metric_catalog_test.go` reads `pkg/adapter/metrics.go` only). — EVIDENCE: grep -rln lenny_adapter_leaked_slots tests/ docs/ returns nothing
WATCHOUT: `*spec-changes.md` as a shell glob also matches `*non-spec-changes.md`; name the file in full or you read the wrong staging. — EVIDENCE: proposals/0081_*/
WATCHOUT: §6.2 (`spec/06_warm-pod-model.md:160`) and §5.2 (`spec/05:545`) attribute the leaked-slots gauge to the adapter while the gateway emits it. Pre-existing; SPEC-6's row names no emitter, so it neither fixes nor worsens that. Not filed.
DECISION: filed one bookkeeping finding — BECAUSE SPEC-6's lead-in ("a row ... for each counter below") and the `## Spec files touched` §16 entry ("one row for each counter the compensation's caller and the adapter's fail-closed arm emit") and the metrics.md mirror sentence ("the row for each counter") all predate the operator's decision-48/49 gauge row and now under-enumerate the fenced block — ALTERNATIVES: filing the §6.2 emitter attribution (pre-existing, not this proposal's edit).

### [spec.28.review-kubernetes.1]

- FACT: the SPEC-6 rows added after round 27 (decisions 20/41/45/48/49) touch no CRD, status subresource, finalizer, webhook or controller. The `lenny_adapter_leaked_slots` gauge is gateway-side and shipped with labels `pod_id`, `pool`, as the staged row states. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:221-225
- USEFUL [spec.27.review-kubernetes.1]: its FACT on SPEC-4's read-back of `Sandbox.status.phase` (sole writer per the §4.6.3 row; both reconcilers share the `lenny-warm-pool-controller` field manager, pkg/controller/warmpool/occupancy.go:146-156) and its WATCHOUT on the no-claim `sdk_connecting` leg saved a re-derivation. Neither is a finding this round.
- WATCHOUT: SPEC-3's "a pod serving one session whose claim the failed bind deletes retires under the §6.2 occupancy projection" depends on the controller having projected `claimed` before the DELETE. The coalesced-reconcile window is the standing Open entry that says not to re-file it. Gateway deletion of a claim on a non-recycling release is already stated in spec/06:80, so the §4.6.3 SandboxClaim notes cell omitting that deleter predates this proposal. EVIDENCE: spec/04_system-components.md:618; spec/06_warm-pod-model.md:80

### [spec.28.review-mechanism.1]

FACT: `lenny_adapter_leaked_slots` moves on exactly one path: `applySlotRetryPolicy` (to become CODE-5's `accountSlotFailure`) calls `slots.MarkLeaked` then the gauge, and zeroes the series when it drains the pod (`ForgetPod` then `leakGauge(pod, pool, 0)`). `Binder.ReleaseSlot` (a session-end leak, `leaked = err != nil || !cleanly`) and the `ReportSessionScrub` LEAKED handler (it feeds `RecordLeak` only) never touch it. `slotstate.Registry` is in-process, so the series is per gateway replica. — EVIDENCE: pkg/gateway/sessionserver/start.go:2844-2847, :2866-2872; pkg/gateway/podlifecycle/podsession/slotbinder.go:542-543; pkg/gateway/mcpfabric/delegationtree/leasecontrol/scrubreport_server.go:88-94; pkg/sandbox/slotstate/registry.go:7-12
WATCHOUT: the SPEC-6 gauge row defines the series as "the per-pod count of slots in the `leaked` sub-state, which stay counted until the pod terminates". The tree contradicts both halves (see the FACT above). Filed this round. The spec-side remedy narrows the row to what the series counts. Do not realign the labels; `[operator.20-41-45]` WATCHOUT still stands.
WATCHOUT: §16.1.1 (spec/16_observability.md:293) bars any label that is neither in the attribute table nor documented as a local-only extension of an entry in it. `pod_id` is in neither, and `k8s.pod.name` owns the pod-name attribute. The SPEC-6 gauge row is the first `pod_id` label in spec/16. Filed this round. The remedy documents the label as an extension and does not rename it.
FACT: no gate enforces the §16.1.1 label allowlist; `pkg/observability/metrics` checks only the forbidden high-cardinality list. — EVIDENCE: pkg/observability/metrics/metrics_test.go:129

### [spec.28.review-performance.1]

DECISION: no spec-lane finding under the performance lens on the SPEC-6 rows (decisions 45, 48) or the decision-47 sentence — BECAUSE the three §16.1 rows add no store write, watch or reconcile: the superseded counter is one increment per compensating `Shutdown` answered `superseded` (bounded by the bind-failure rate, labels `pool`, `k8s_pod_name` as on `lenny_slot_failure_total`), the untokened counter is unlabeled, and the leaked gauge is shipped and only catalogued — ALTERNATIVES: filing `pod_id` cardinality on the leaked gauge, rejected because spec/06:160 already fixes those labels and a series exists only for a pod that leaked; filing the gauge's stale series after drain, rejected because the drain path zeroes it (pkg/gateway/sessionserver/start.go applySlotRetryPolicy, after `slots.ForgetPod`) and any series-deletion remedy is code.
FACT: `lenny_adapter_leaked_slots` is set per gateway replica from the replica-local `slotstate.Registry` via `SlotLeakGauge` (cmd/lenny-gateway/sessionsrv.go:380-383, pkg/gateway/sessionserver/start.go `slots.MarkLeaked`), so two replicas can publish different values for one `pod_id`; this is the same replica-local dilution the standing Open entry on the §5.2 trigger records, pre-existing, not a SPEC-6 defect. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go:1213-1217
USEFUL [operator.20-41-45]: its WATCHOUT on `pod_id` versus `k8s_pod_name` saved re-deriving why the gauge row does not use the §16.1.1 canonical pod label.

### [spec.28.review-reliability.1]
- DECISION: no finding on SPEC-6's new `lenny_adapter_leaked_slots` row — BECAUSE its "stay counted until the pod terminates" restates spec/06:160 (`leaked` slot semantics), which already names the gauge as the persistent count; the row adds no new claim — ALTERNATIVES: filing the gauge's loss on gateway restart was rejected as pre-existing (same as the Open entry on the persistent `leaked` ledger's §12.4 fallback).
- FACT: the gauge is a gateway-replica, in-memory value. `cmd/lenny-gateway/sessionsrv.go:381-383` sets it from `slotstate.Registry.MarkLeaked` (`pkg/sandbox/slotstate/registry.go:99-116`, a process-local map), so a gateway restart or a different replica reports 0 for a pod whose slots are still leaked. The durability claim at spec/06:160 does not hold under restart, and that gap predates 0081 — EVIDENCE: pkg/sandbox/slotstate/registry.go:20-23
- WATCHOUT: spec/06:160 says "The adapter exposes ... `lenny_adapter_leaked_slots` gauge", but the tree emits it from the gateway (`gatewaymetrics_credential.go:219-226`). SPEC-6 files the row among the gateway rows with no adapter-scrape deferral, and CODE-9 puts the docs row in the gateway table. The staged row names no emitter, so it does not contradict §6.2 outright. The edit-sites or citations lens owns any §6.2 attribution fix, and the reliability lens does not — EVIDENCE: spec/06_warm-pod-model.md:160
- FACT: the decision-47 sentence (a removal performed before writers stop counts as a failed act) keeps the hold until pod termination through the failed-act rows, and pod termination is the reclaimer. The sentence adds no recovery path without a reclaimer.

### [spec.28.review-security.1]

FACT: the SPEC-6 `lenny_adapter_leaked_slots` row (labels `pod_id`, `pool`; persistent until pod termination) matches the shipped gateway collector and §6.2's `leaked` slot semantics. No security surface changes with it. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-223, gatewaymetrics.go:1217 (`WithLabelValues(podID, pool)`), spec/06_warm-pod-model.md:160
FACT: every gateway-side adapter `Shutdown` goes through the one builder `Client.shutdown` (pkg/gateway/runtime/adapterclient/client.go:812-823). CODE-7 sets `unconditional_teardown` in both exported forms (non-spec-changes.md "Every non-compensating `Shutdown` caller sets `unconditional_teardown = true`"), so the fourth caller `binder.go:2043` that Open entry 77 names is covered by construction. The §11.4 revoke (cmd/lenny-gateway/user_revocation.go:129) takes the same route, so the revoke is not reduced to a no-op under rule 10. EVIDENCE: binder.go:2043, slotbinder.go:542, user_revocation.go:129
USEFUL [Settled DECISION open decision 38, and the `maxSessionsPerPod` counting a bind that reached `RunSetup` (decision 21) line in Retired]: both relaxations a security lens would otherwise file were already settled: row 3 has no `leaked` on a failed non-close act, and a pre-`running` bind that ran setup commands is not counted. Do not re-file either one.
WATCHOUT: §7.2 step 3's retained "released back to the pool" is safe on a one-session pool only because SPEC-4 re-keys the projection so that a claim deleted while the pod projects `claimed` drains the pod on either recycle setting. The Sandbox.status sole-writer claim SPEC-4 relies on is true (spec/04 §4.6.3 table, `Sandbox` `status.*` row: "Sole writer of phase and conditions"). EVIDENCE: spec/04_system-components.md:618

### [spec.28.review-single-source.1]

- FACT: the only spec-changes.md delta since the round-27 sweep is commit 6272cdfea's SPEC-6 edit (untokened row loses `k8s_pod_name`; new `lenny_adapter_leaked_slots` gauge row). The gauge's name and `pod_id`/`pool` labels are already stated at spec/06:160 (`**`leaked` slot semantics.**`); the §16.1 row is the catalog entry citing §6.2 and summarises the persistence in one clause, so it is not a second stating site. — EVIDENCE: spec-changes.md SPEC-6 block; spec/06_warm-pod-model.md:160
- FACT: decision 47's sentence (removal before every writer has stopped counts as a failed act) has one stating site, SPEC-3's reclaim-hold paragraph; CODE-6 and the summary cite it by name. — EVIDENCE: `grep -n "still writing" proposals/0081_*/*.md`
- USEFUL [Settled: DECISION (round 11) §12.6 WRITE clause not reduced]: the §4.7 `ReportSessionScrub` row and §12.6 both state the per-report `sessions_served` increment; that asymmetry is a recorded decision (register gate `podStateGatewayWrittenSentence`), not a (g) finding.
- WATCHOUT: "Spec files touched" still says §16.1 gains "one row for each counter"; SPEC-6 now also adds a gauge row. Bookkeeping only, below the bar for this lens; the between-loops pass may want to widen the phrase.

### [spec.29.review-docs-alignment.1]
FACT: The round-28 fix is a single hunk in SPEC-6's leaked-slots gauge row (spec-changes.md:1225): it documents `pod_id` as a local-only extension of `k8s.pod.name`, which spec/16_observability.md:293 ("documented as a local-only extension of one of these entries") admits. Nothing else in the proposal changed. EVIDENCE: spec-changes.md:1225, spec/16_observability.md:293
FACT: The shipped gauge's label is the pod name. SetAdapterLeakedSlots(podID, pool) is fed `sbe.Pod`. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-225, pkg/gateway/sessionserver/start.go:2870
FACT: docs/ has no `lenny_adapter_leaked_slots` row today. CODE-9 stages it with `pod_id` and `pool` (non-spec-changes.md:3906), which matches the new spec row, so the spec lane has no docs-alignment finding.

### [spec.29.review-edit-sites.1]
FACT: The round-28 fix to SPEC-6's `lenny_adapter_leaked_slots` row (pod_id documented as a local-only extension of `k8s.pod.name`) uses the escape §16.1.1 itself states ("documented as a local-only extension of one of these entries"); no §16.1.1 table edit is owed. The emitter's label set and values match: `pod_id` carries the pod name. — EVIDENCE: spec/16_observability.md:293; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-226; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_elicitation_test.go:930
FACT: No other spec site names the gauge's labels. §6.2 (spec/06_warm-pod-model.md:160) and §5 (spec/05_runtime-registry-and-pool-model.md:545) name the gauge without labels, so the row's label clause cannot drift against them.

### [spec.29.review-mechanism.1]
FACT: The round-28 fix to SPEC-6's leaked-slots gauge row declares `pod_id` a local-only extension of `k8s.pod.name`. That wording matches the escape hatch in §16.1.1, which says "documented as a local-only extension of one of these entries". The tree uses the same labels: the gauge registers `pod_id` and `pool`, and the only setter passes the pod name (sbe.Pod). The same-file anchor `#1611-attribute-naming` resolves because the fence lands in spec/16. EVIDENCE: spec/16_observability.md:293; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:219-225; pkg/gateway/sessionserver/start.go:2870

### [spec.30.review-applicability.1]
FACT: the only spec-changes.md change since the r28 snapshot is SPEC-6's leaked-slots gauge row, now declaring `pod_id` a local-only extension of `k8s.pod.name` citing `#1611-attribute-naming`. The anchor resolves within spec/16 (heading `### 16.1.1 Attribute Naming`, spec/16_observability.md:289), and :293 admits a label "documented as a local-only extension of one of these entries". The row's parentheses balance. EVIDENCE: spec/16_observability.md:289,:293
FACT: SPEC-6's placement anchor "beside the session-slot failure count" matches exactly one row (spec/16_observability.md:14). No tier-0 or tier-11 gate checks `pod_id` against §16.1.1, so the new clause trips none. EVIDENCE: grep of tests/tier11_docs for pod_id and 16.1.1
WATCHOUT: the glob `*spec-changes.md` also matches `non-spec-changes.md`. Pass the exact filename to sed or Read, or line numbers get mixed across the two files.

### [spec.30.review-citations.1]
- FACT: the only spec-changes delta since r28 is SPEC-6's leaked-slots gauge row clause declaring `pod_id` a local-only extension of `k8s.pod.name`; §16.1.1 (spec/16_observability.md:293) explicitly admits a label "documented as a local-only extension of one of these entries", and `k8s.pod.name` is a table row (:297). The relative anchor `#1611-attribute-naming` resolves inside spec/16. — EVIDENCE: spec-changes.md:1225
- FACT: every cross-file anchor in spec-changes.md resolves against current spec/ headings, and every relative anchor sits in a staged block for the file that owns the heading (§4.1/§4.6.1 -> spec/04, §7.2/§7.3 -> spec/07, SPEC-6 -> spec/16). — EVIDENCE: spec-changes.md:237,242,457,486,906,909,1225
- FACT: SPEC-3 commentary's five file:line citations hold: holdstate.go:201 (10s pass-2 close ctx), spec/11:264 (§11.4 step 3, 10s), session.go:219-224 (deadline_ms comment), socketruntime.go:470-473 (10s fallback), mcpruntime.go:86 (5s default); `ShutdownGrace` is assigned nowhere outside its declaration. Commit 3997f502b dated 2026-08-22 matches. — EVIDENCE: spec-changes.md:663-676

### [spec.30.review-client-surface.1]
- FACT: the only delta since spec-r28-prefix is SPEC-6's leaked-slots gauge row, which now documents `pod_id` as a local-only extension of `k8s.pod.name`; §16.1.1 explicitly admits local-only extensions (spec/16_observability.md:293) and the shipped gauge registers `pod_id`,`pool` (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-225), matching spec/06_warm-pod-model.md:160. Clean under the client-surface lens. — EVIDENCE: spec/16_observability.md:293
- WATCHOUT: the shell glob `*spec-changes.md` also matches `*non-spec-changes.md`, which sorts first; use the full file name or `grep`/`sed` land in the non-spec staging. — EVIDENCE: proposal directory listing
- FACT: `SETUP_COMMAND_FAILED` appears on no client surface other than spec/15 and docs/reference/error-catalog.md (no openapi.json, sdks/ or schemas/ hit), so SPEC-5's §15.1 row re-key has no SDK or OpenAPI parallel to mirror; `details.reason` is unchanged. — EVIDENCE: grep over pkg/gateway/openapi, sdks, schemas
- FACT: SPEC-5's §4.7.1 carriage table agrees field-for-field with SCHEMA-1's field table (bind_attempt on Prepare/Finalize/RunSetup/AssignCredentials/Resume/Shutdown; mid_session on Prepare/Finalize; unconditional_teardown on Shutdown). The existing FinalizeWorkspaceRequest.mid_session = 4 is shipped (schemas/lenny-adapter.proto:724).

### [spec.30.review-docs-alignment.1]
FACT: the only round-28-to-30 spec delta is SPEC-6's gauge row now calling `pod_id` a local-only extension of `k8s.pod.name`; §16.1.1's single-source paragraph admits labels "documented as a local-only extension of one of these entries", and §6.2's `leaked` slot semantics paragraph already labels the gauge by `pod_id` and `pool`, so the row is consistent — EVIDENCE: spec/16_observability.md:293, spec/06_warm-pod-model.md:160
FACT: no alert in spec/16 or pkg/alerting/rules references `lenny_adapter_leaked_slots`, so the gauge row owes no runbook companion; its docs/reference/metrics.md row is CODE-9's (non-spec-changes.md:4241) — EVIDENCE: grep of spec/16*.md, pkg/alerting/rules, docs/

### [spec.30.review-edit-sites.1]
FACT: the only spec-changes.md delta against the spec-r28-prefix snapshot is the round-28 clause in SPEC-6's `lenny_adapter_leaked_slots` row documenting `pod_id` as a local-only extension of `k8s.pod.name`; it uses the escape spec/16_observability.md:293 states, and no other spec/, schemas/ or charts/ surface names the gauge's labels differently. EVIDENCE: diff -ru snapshot vs proposal (one hunk); grep -rn lenny_adapter_leaked_slots spec/ docs/ charts/ schemas/
CORRECTS [spec.29.review-edit-sites.1]: its second FACT says §6.2 names the gauge "without labels"; spec/06_warm-pod-model.md:160 in fact says "`lenny_adapter_leaked_slots` gauge, labeled by `pod_id` and `pool`". Only spec/05:545 is label-free. The labels agree with the SPEC-6 row, so no finding follows, but the "cannot drift" reasoning rests on agreement, not absence.
USEFUL [spec.28.fix-design-G1.1]: its WATCHOUT (no §6.2 or §16.1.1 edit owed for `pod_id`) saved a re-derivation.

### [spec.30.review-fresh.1]
FACT: the only spec-changes.md edit since r28 is SPEC-6's leaked-slots gauge row, which now documents `pod_id` as a local-only extension of `k8s.pod.name`; §16.1.1 admits exactly that escape ("documented as a local-only extension of one of these entries") and the anchor `#1611-attribute-naming` resolves. EVIDENCE: spec/16_observability.md:289,293; spec-changes.md:1225
FACT: the gauge's `pod_id` value is the Sandbox name (`SlotBindError.Pod`), so "carries the pod name" holds. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotfailure.go:59-60; pkg/gateway/sessionserver/start.go:2846; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-225
WATCHOUT: `sed -n X,Yp *spec-changes.md` in the proposal dir matches BOTH non-spec-changes.md and spec-changes.md and concatenates them; name the file exactly. EVIDENCE: directory listing

### [spec.30.review-kubernetes.1]
- FACT: The only spec-changes delta since spec-r28-prefix is SPEC-6's leaked-slots gauge row, which now declares `pod_id` as a local-only extension of `k8s.pod.name`. It is a metric-label edit and touches no CRD, status, finalizer, webhook or controller surface. The §16.1.1 rule admitting local-only extensions is at spec/16_observability.md:293. — EVIDENCE: spec-changes.md:1225
- USEFUL [Standing context §4.6.3 sole-writer / OccupancyReconciler For(&Sandbox{}) entries]: these answer the status-ownership and read-back convergence questions for SPEC-4, so this lens did not need to re-derive them.

### [spec.30.review-mechanism.1]
FACT: spec-changes.md is unchanged since round 29 apart from nothing; the only delta against the spec-r28-prefix snapshot is the round-28 fix to SPEC-6's `lenny_adapter_leaked_slots` row (pod_id as a local-only extension of `k8s.pod.name`). No mechanism finding on it. EVIDENCE: diff vs scratchpad/cp-snap/0081-opt2/spec-r28-prefix; spec/16_observability.md:293
FACT: no test or script enforces §16.1.1 label membership against §16.1 rows (grep for `1611-attribute-naming` and `local-only extension` in tests/ scripts/ pkg/ cmd/ is empty), so the local-only-extension wording trips no gate. EVIDENCE: grep -rln over tests scripts pkg cmd
USEFUL [spec.29.review-mechanism.1]: its trace of the sole gauge setter (start.go, sbe.Pod) confirmed pod_id carries the pod name without re-derivation.

### [spec.30.review-performance.1]

DECISION: no spec-lane finding under the performance lens — BECAUSE the only delta since the r28 snapshot is one clause in SPEC-6's `lenny_adapter_leaked_slots` row documenting `pod_id` as a local-only extension of `k8s.pod.name` (spec/16_observability.md:293 admits that escape). It adds no store write, watch, reconcile or store dependency, and `pod_id` has the same pod-count cardinality bound as `k8s_pod_name` on `lenny_slot_failure_total` — ALTERNATIVES: filing zeroed-but-never-deleted gauge series (one stale series per drained pod per gateway replica) was rejected because the row mandates no deletion semantics, the behaviour is shipped, and any remedy (`DeleteLabelValues` at drain) is code-lane.
FACT: the drain path writes 0 through `SetAdapterLeakedSlots` (`WithLabelValues(...).Set(0)`) and never deletes the series, so the gauge's series count grows with the number of pods ever drained for leaks, per replica. EVIDENCE: pkg/gateway/sessionserver/start.go:2866-2871; pkg/gateway/metrics/gatewaymetrics/gatewaymetrics.go:1213-1217
USEFUL [spec.28.review-performance.1]: its write-rate and replica-local accounting of the three SPEC-6 rows still holds and saved a re-derivation.

### [spec.30.review-reliability.1]
- DECISION: no reliability finding this round — BECAUSE the only delta since spec-r28-prefix is the SPEC-6 `lenny_adapter_leaked_slots` row's label note (`pod_id` as a local-only extension of `k8s.pod.name`), which touches no recovery, retry, fencing or reclaim mechanism, and spec/16_observability.md:293 (§16.1.1) admits a label "documented as a local-only extension of one of these entries" — ALTERNATIVES: re-sweeping the whole staging under this lens; rejected as the rest of the spec staging is byte-identical to the text five clean sweeps certified.
- FACT: `pod_id` on the leaked-slots gauge predates this proposal at spec/06_warm-pod-model.md:160 ("labeled by `pod_id` and `pool`"), undocumented as an extension there; the SPEC-6 row's note is now the one place the extension is documented. EVIDENCE: spec/06_warm-pod-model.md:160, spec/16_observability.md:293

### [spec.30.review-security.1]
FACT: `lenny_adapter_leaked_slots` is registered and set gateway-side (pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:223, set from pkg/gateway/sessionserver/start.go), so the leaked count that feeds whole-pod replacement comes from a trusted component and not from an in-pod report. The round-29 `pod_id` "local-only extension of `k8s.pod.name`" clause meets the §16.1.1 admission rule (spec/16_observability.md:293) and has no security surface. — EVIDENCE: spec-changes.md SPEC-6 gauge row
DECISION: no security findings in round 30 — BECAUSE the only delta is a label-provenance clause, and the one security-flavoured Open entry (whether the adapter gRPC server is reachable from the agent container) is pre-existing and in no spec fence — ALTERNATIVES: filing that entry was rejected because its remedy is not in the staged spec edits

### [spec.30.review-single-source.1]
FACT: the only spec-changes.md delta since snapshot spec-r28-prefix is the round-28 clause in SPEC-6's `lenny_adapter_leaked_slots` row documenting `pod_id` as a local-only extension of `k8s.pod.name`. It has one stating site: no §16.1.1 edit is staged, and §16.1.1 itself states only the general admission rule, which the row applies rather than restates. EVIDENCE: spec-changes.md SPEC-6 fence; spec/16_observability.md:293
FACT: spec/06_warm-pod-model.md:160 does name the gauge's labels ("labeled by `pod_id` and `pool`"), so [spec.29.review-edit-sites.1]'s "§6.2 names the gauge without labels" is wrong for §6.2 (true only of spec/05:545). The two label lists agree today, and a catalog row naming labels is not a restated rule, so this is not a (g) finding.
CORRECTS [spec.29.review-edit-sites.1]: spec/06:160 carries the `pod_id`/`pool` labels; only spec/05:545 names the gauge without them.
USEFUL [spec.28.review-single-source.1]: its disposition of the gauge row as a catalog entry citing §6.2 held for the round-28 clause too.

### [index-reconcile.5]

FACT: the staged deliverables are unchanged since `[index-reconcile.4]`: SPEC-1 through SPEC-6, and CODE-1 through CODE-12, CONF-1, SCHEMA-1 and DOCS-1 through DOCS-4; the deliverable index lists each once with its files. The staging moved only in SPEC-6 (operator decision 48 made the untokened-entry row label-free; `[spec.28.fix-G1.1]` documents the gauge's `pod_id` as a local-only extension of `k8s.pod.name`) and in CODE-9 (operator decision 45 gave the leaked-slots gauge a `catalog.go` entry, a `spec161Metrics` entry and a `docs/reference/metrics.md` row). Index lines corrected: SPEC-6 (the label-free untokened row and the local-only-extension clause) and CODE-9 (its "it alone takes a `catalog.go` row and a `spec161Metrics` entry" was false once the gauge took both; the line now names the gauge's three entries and the label-free adapter series). EVIDENCE: spec-changes.md SPEC-6 fence; non-spec-changes.md CODE-9 registration and gauge paragraphs.
FACT: the SPEC-lane block keeps its ids and order (S1 SPEC-5, S2 SPEC-1, S3 SPEC-2, S4 SPEC-3, S5 SPEC-4, S6 SPEC-6), one lane per step. S6's line now states the label-free untokened row and the `pod_id` local-only-extension clause. No other spec step line is stale against the staging.
FACT: every code, schema and docs step's `Depends on:` already names the spec steps staging what it implements (re-checked against `[index-reconcile.4]`'s additions); no dependency line changed this pass.
FACT: no `DEFERRED` line was filed after `[index-reconcile.4]`. Every earlier `DEFERRED` is closed by a `CORRECTS` or carried as an `OPEN` line; none of the carried ones is a repair in a file this pass may edit, so no `CORRECTS` closes a deferred entry this pass.
OPEN [non-spec-changes.md, CODE-7]: the `Client.Shutdown` doc comment in `pkg/gateway/runtime/adapterclient/client.go` says a zero deadline lets the adapter apply its default grace, which `resolveShutdownGrace` contradicts; staging the one-sentence repair is authoring a code change for the non-spec loop. Carried from `[index-reconcile.4]`.
OPEN [non-spec-changes.md, SCHEMA-1]: the `ErrorCode` enum header in `schemas/lenny-adapter.proto` says the catalog mirrors §15.1, which is false for codes 27, 28 and 29; SCHEMA-1 stages no replacement. Carried.
OPEN [non-spec-changes.md, SCHEMA-1]: the proto `LEAKED` comment's drain-ledger sentence may restate a rule whose home is §4.6.3 or §5.2; a reduction to a citation is a finding of its own. Carried.
OPEN [non-spec-changes.md, DOCS-2]: `docs/reference/adapter-contract.md` gives the §15.4 reclaim hold's `ABORTED` refusal neither a sentence nor an exclusion rationale; either is new DOCS-2 content. Carried.
OPEN [non-spec-changes.md, CODE-6]: the closing claim of **Disposition of an expired acquisition at a removing site** is false for a §10.1.4 pass member whose acquisition expired because another member's park spent the pass's single guard-acquisition context; the remedy is a code-lane design choice. Carried.
OPEN [spec-changes.md, commentary]: the untokened-entry commentary calls the §10.1 hold-timeout termination "a request" where §5.2 says it runs under no request. Carried; this pass may not edit spec-changes.md.
OPEN [spec-changes.md, `## Spec files touched`]: the spec/16 bullet says §16.1 gains "one row for each counter", and the docs-mirror sentence after it says `docs/reference/metrics.md` gains "the row for each counter"; both predate the gauge row SPEC-6 now stages (`[spec.28.review-applicability.1]` MISTAKE, `[spec.28.review-single-source.1]` WATCHOUT). Bookkeeping in a file this pass may not edit.
OPEN [pkg/apis/lenny/v1alpha1/sandbox_types.go]: the `Sandbox.status.phase` doc comment lacks the projection input SPEC-4 adds; record only. Carried.
OPEN [docs/api/internal.md]: the gRPC status table omits `ABORTED`; pre-existing, for a separate finding. Carried.
OPEN [implementation-checklist.md]: CODE-1 (S16, S17, S21), CODE-4 (S13, S19) and CODE-6 (S14, S15, S21) each appear in more than one step, against the rule that every staged deliverable appears in exactly one step. Merging them rewrites non-spec steps and their ordering, which this pass does not do. Carried.
DECISION: routed the standing Open "Should the tier-3 descriptor gate pin the `SlotReclaimOutcome` enum values?" to the summary as open decision 50, with the ground the log gives and no recommendation — BECAUSE `[non-spec.10.review-client-surface.1]` says a human rather than another lens round should close it, and no summary entry carried it — ALTERNATIVES: leaving it in the log, rejected because the reviewer never reads the log. Every other standing Open routed to a human is already in the summary (29, 30, 32, 33, 34, 36) or answered (28, 31, 40, 46).

### [non-spec.15.fix-G1.1]
DECISION: CODE-6's slotGuards preamble now carries a named **acquisition step** (non-blocking send first; select against ctx.Done() only when the channel is full); lockSlotGuard and acquireSlotGuardForResolve cite it — BECAUSE a two-case select with both cases ready picks at random, and the StartSession/SDK-warm rollback calls releaseSessionSlot on the already-expired request ctx (pkg/adapter/session.go:156-157), so an uncontended rollback retained the reclaim hold about half the time — ALTERNATIVES: detached ctx with timeout at rollback sites (adds a non-spec tunable, seven call sites, leaves the random select elsewhere); try-send-first on lockSlotGuard only (two acquisition semantics on one channel).
WATCHOUT: "the acquisition outlives ctx" / "expired acquisition" now means contended past ctx; the existing cancelled-context tests (destructive-expiry, admission-refusal, Resume row, Shutdown expired acquisition) all hold the guard, so they stay valid. Do not rewrite them or restate the step in the Disposition section. — EVIDENCE: non-spec-changes.md CODE-6 "The acquisition step." paragraph; Testing tier-1 "An uncontended acquisition on a cancelled context holds the guard."

### [non-spec.15.fix-design-G1.1]
DECISION: CODE-6's guard acquisition becomes "non-blocking send first, then select the send against ctx.Done() only when the channel is full", stated ONCE in the slotGuards preamble paragraph and cited by both hand-out bullets (lockSlotGuard and acquireSlotGuardForResolve); one tier-1 case pins it (free guard + already-cancelled ctx acquires, repeated) — BECAUSE a bare select with both cases ready is a uniform random choice in Go, so an uncontended acquisition on a done context (the StartSession/ConfigureWorkspace/SDK-warm rollback after Runtime.Start failed on the request ctx) retained the reclaim hold ~half the time — ALTERNATIVES: detached bounded context at the rollback call sites (rejected: a new non-spec timeout needing an operator override, seven call sites of hair, and the ctx parameter the split threads becomes meaningless); applying the fix to lockSlotGuard only (rejected: two acquisition semantics for one channel).
WATCHOUT: every staged cancelled-context test (non-spec-changes.md ~3116, ~3130, ~3246 Resume row, ~3290) runs with the guard HELD by another goroutine or by Resume itself, so they stay green under the try-send-first rule; do not "fix" them. — EVIDENCE: non-spec-changes.md:3117-3118, 3131, 3246, 3291-3292
FACT: the staging carries no function body for lockSlotGuard; its semantics live only in the CODE-6 bullets at non-spec-changes.md:1756-1768. — EVIDENCE: grep lockSlotGuard non-spec-changes.md

### [non-spec.15.review-applicability.1]
FACT: "local-only extension" is a §16.1.1 term of art, so SPEC-6's gauge-row wording is anchored in the tree, and `#1611-attribute-naming` resolves (heading `### 16.1.1 Attribute Naming`). No gate parses §16.1 labels against §16.1.1. EVIDENCE: spec/16_observability.md:289, :293
FACT: CODE-9's untokened accessor is now `incSlotShutdownUntokenedEntry()` with no parameter at all three sites (registration clause, CODE-1 call site, files-touched), matching `mustCounter` (label-free, WithLabelValues()). The CLOSED Open entry that quotes `(podID string)` / `s.podID` is stale. EVIDENCE: non-spec-changes.md:337, :2138; pkg/adapter/metrics.go:117
CORRECTS [Open "Which deliverable DECLARES the untokened-entry counter's accessor"]: the signature recorded there, `(podID string)` beside `incUnaddressedFrameRejected`, was superseded by decision 48. The staged form is `func incSlotShutdownUntokenedEntry()` beside `incSetTracingContextDropped`.
FACT: Two Open entries are already resolved in the staging and can be retired. S22 now depends on S18. The claim-register split is stated once: the WIRED rows land at S9 and the ABSENT row lands at S22. EVIDENCE: implementation-checklist.md S22 line; non-spec-changes.md:2653, :3625
FACT: The CredentialAssigner `Release` widening breaks about ten test fakes across tiers 1, 4 and 9, including `tests/tier4_integration` and `tests/tier9_security`. The fake sweep in the files-touched list, defined by grep on `ReleaseSession(` plus the field assignment, covers every one of them, and S19 lands the sweep with the widening. EVIDENCE: non-spec-changes.md:4312-4318

### [non-spec.15.review-reliability.1]

- FACT: the only proposal edits since the snapshot are SPEC-6's gauge-row `pod_id` wording, the checklist S6 and summary SPEC-6/CODE-9 bullets, and summary decision 50. None of them touches a recovery path. — EVIDENCE: diff against scratchpad/cp-snap/0081-opt2/spec-r28-prefix
- FACT: the `CredentialAssigner.Release(leaseID)` widening is staged (non-spec files-touched, binder.go bullet) and the production value's type `credassign.Assigner` already declares `Release(leaseID string)`. The interface-method check passes for that call. — EVIDENCE: pkg/gateway/credentials/credassign/credassign.go:53, cmd/lenny-gateway/stores.go:1797,2103
- FACT: the Open entries "S22 owes S18" and "which step lands SCHEMA-1's claim rows" are both settled in the current text. S22 lists S18, and the SCHEMA-1 staging puts the two WIRED rows at S9 and the ABSENT row with CONF-1. — EVIDENCE: checklist S22; non-spec-changes.md:2653
- WATCHOUT: `lockSlotGuard` as staged is a bare `select` between the send and `ctx.Done()`. When the context is already done and the guard is free, Go picks one of the two cases at random. On the StartSession and SDK-warm ConfigureWorkspace rollback sites, which thread the request context, a rollback caused by an expired caller context therefore retains the reclaim hold for the life of the pod about half the time, with no contention at all. Filed this round. — EVIDENCE: non-spec-changes.md:1755-1760,1882-1886,1853-1856; pkg/adapter/session.go:157

### [non-spec.15.review-security.1]
DECISION: empty findings list for round 15 — BECAUSE the only delta since the last snapshot is the SPEC-6/CODE-9 label bookkeeping (untokened-entry counter unlabeled, leaked-slots gauge `pod_id` documented as a local-only extension of `k8s.pod.name`, open decision 50 added to the summary), none of which touches a security control or a trust boundary — ALTERNATIVES: re-filing the pre-existing Open entry on adapter gRPC reachability from the agent container, rejected because a plain `Shutdown` already tears down unconditionally in the shipped tree, so `unconditional_teardown` adds no new capability to an in-pod caller.
FACT: the untokened-entry accessor is consistently argument-free across the staging (`incSlotShutdownUntokenedEntry()` at non-spec-changes.md CODE-1 body and CODE-9 registration clause), matching the unlabeled SPEC-6 row; the Open entry "Which deliverable DECLARES the untokened-entry counter's accessor" records a `(podID string)` signature that is now stale. EVIDENCE: grep -n incSlotShutdownUntokenedEntry proposals/0081_*/0081_*.non-spec-changes.md
CORRECTS [Open "Which deliverable DECLARES the untokened-entry counter's accessor"]: the signature is now `incSlotShutdownUntokenedEntry()` with no parameter, not `(podID string)`.
USEFUL [spec.30.review-security.1]: the gauge is gateway-set, so the leaked count feeding whole-pod replacement is trusted-sourced; saved re-tracing the setter.

### [non-spec.16.fix-G2.2]
- DECISION: CODE-1's `Shutdown` doc comment now credits CODE-6's expired-acquisition disposition with the removal alone and sends the hold and the response to the §5.2 failed-act rows — BECAUSE the G2 fix reduced that block to a single Removal-on-expiry column, and the parallel pointers already say so. — EVIDENCE: non-spec-changes.md CODE-1 removing-arm comment; the CODE-6 disposition table (`Removing site | Removal on an expired acquisition`).
- DECISION: the CODE-6 tier-1 expired-acquisition case now splits its assertions. The removal assertions come from the row's cell, and the hold and report assertions come from the §5.2 failed-act rows, matching the Testing row for an expired guard acquisition. — EVIDENCE: non-spec-changes.md, Adapter tests for CODE-6 tier 1, and the Testing bullet "An expired guard acquisition keeps the hold and fails the clean exit".
- DECISION: checklist S15 lands the tier-1 expired-acquisition case with its `releaseSessionSlot` and `terminateHeldSession` rows. S16 lands the case's `Shutdown` row beside the tier-7a `Shutdown` arms — BECAUSE the `Shutdown` row asserts `slot_reclaim: reclaimed` through CODE-1's removing arm, which lands at S16. S16 already runs tier 1 and depends on S15. — EVIDENCE: implementation-checklist.md S15, S16.

### [non-spec.16.fix-G1.1]
DECISION: CODE-4's materializeSlot wrapper and Binder.Resume's failure branch compensate every post-connection failure, a typed refusal included; the `refused` guard, its rationale paragraph and the CODE-4-on-CODE-7 dependency are removed — BECAUSE staged §7.1 "Pod-side reclaim on a failed bind" states the obligation without exception, and §4.7.1 rule 13 already makes a refusal's compensation answer `superseded`, remove nothing and report a clean exit, so Leaked stays false with no branch — ALTERNATIVES: a §7.1 exemption clause for rule-5/rule-6 refusals (reopens converged spec text to protect a guard with no effect) and a superseded-only suppression (a carve-out §7.1 does not state, for zero behavioral difference).
WATCHOUT: do not reintroduce a compensation suppression on SLOT_BIND_ATTEMPT_SUPERSEDED or SLOT_BIND_ALREADY_STARTED. Its premise ("a refusal means another attempt owns the entry") is false for rule 6, which is reached on an entry carrying this attempt's token or none, and the suppression contradicts staged §7.1. The tier-1 bullet "A typed refusal is compensated" now fails against it. — EVIDENCE: spec-changes.md §4.7.1 rules 5, 6 and 13; non-spec-changes.md CODE-4 wrapper.
MISTAKE: earlier rounds staged the suppression (with an errors.Is-on-sentinel rationale paragraph) and let it propagate to the summary, S19, S11, CODE-5, CODE-7 and the test-disposition table; the removal touched each of those sites. A refusal's compensation now increments lenny_slot_compensation_superseded_total, which matches its §16.1 meaning.
FACT: CODE-7's remaining consumers are isTransientPodClaimError and the two CODE-8 reclaim closures; "outside the refusal guard" in CODE-4's Binder.Prepare paragraph and in CODE-8 names CODE-8's reclaim-closure guard, which still exists.

### [non-spec.16.fix-G2.1]
DECISION: Reduced CODE-6's **Disposition of an expired acquisition at a removing site** to the unguarded removal, one sentence routing an expired acquisition onto SPEC-3's reclaim-hold failed-act sentence and the §5.2 failed-act rows, the definition of `guarded`, and the rejected-release rationale; the per-site table keeps only Removing site and Removal-on-expiry — BECAUSE §5.2's table is the single home of every disposition cell (spec-changes.md:635) and each site states its own `completed` predicate (non-spec-changes.md:449, :1458, :1486) — ALTERNATIVES: labelling the hold/report columns as informative (still a second statement); new §5.2 rows (SPEC-3 already covers the case).
WATCHOUT: Do not re-add Reclaim hold or Report columns, a retained-hold sentence, or a three-predicate sentence to that block; round 16 removed them as copies of §5.2 cells and of the site predicates. — EVIDENCE: spec-changes.md:632 (SPEC-3 failed-act sentence), non-spec-changes.md:449
DECISION: Checklist S15 lands the guard and the expiry disposition at `releaseSessionSlot` (guard-acquiring form) and `terminateHeldSession` only; S16 lands `Shutdown`'s removing-arm guard, its disposition, the tier-7a lock-order assertion and the `Shutdown` arms of the reclaim-against-an-admitted-section case — BECAUSE the shipped `Shutdown` has no arm split and CODE-1's removing arm lands at S16, which depends on S15 — ALTERNATIVES: reordering S16 before S15 (the removing arm takes S15's guard).

### [non-spec.16.fix-G3.1]
DECISION: CODE-4's `client.go` Targets bullet is the one home for the adapterclient bind-sequence signatures (AssignCredentials/RunSetup trailing `bindAttempt string`; FinalizeWorkspace `bindAttempt string` before shipped `midSession bool`; PrepareWorkspace trailing `bindAttempt string, midSession bool`; `ResumeParams.BindAttempt`) — BECAUSE the proposal never stated them and two real-adapter tests call the method, not a literal — ALTERNATIVES: a third grep sweep (redundant, compile break already catches callers); client-side mint (contradicts settled mechanism); deriving mid_session from empty token (restates the pairing rule in client code).
FACT: the only test callers of token-carrying adapterclient.Client methods are client_test.go and the two token_service_unavailability_guard_test.go files (tier4, tier8); resume_slot_reservation_test.go and binder_test.go go through Binder, not Client methods. EVIDENCE: grep of `.AssignCredentials(|.RunSetup(|.PrepareWorkspace(|.FinalizeWorkspace(|.Resume(` over adapterclient-importing tests.
DECISION: `deregisterSlot`'s test callers (podmcp_arming_internal_test.go:88,:245) become inline `s.mu` lock plus `deregisterSlotLocked`; stated once in CODE-6's retirement sentence — BECAUSE reclaimSlotLocked opens a hold nothing releases. Closes the review-log Open entry on this.
DECISION: manifest_fields_test.go added to the files-touched Tests list (setSessionLeasesForTest :220 widening). Closes the review-log Open entry on it.

### [non-spec.16.fix-G4.1]
DECISION: the `## Testing` preamble in non-spec-changes.md is the single home of the step conventions (tier 0 and tier 1 runs, spec-map registration, `// spec:` and `// diagnosis:` annotations, claim-map seeding through the `EXPLICIT` list, regenerated in the same commit) — BECAUSE it carries the stronger tier clause that matches test-coverage.md and sits in the same file as SCHEMA-1 — ALTERNATIVES: making the checklist preamble the home (weaker tier clause, cross-file citation from Testing); aligning two copies (still a restatement that drifts).
CORRECTS [earlier FACT entries naming the implementation checklist preamble as the home of the claim-register seeding convention]: the checklist preamble now cites `## Testing` in a single clause, and both SCHEMA-1 `**Claim register.**` paragraphs cite the `## Testing` preamble.
WATCHOUT: do not re-add a partial list of conventions to the checklist preamble. It keeps only "Every spec step leads", the per-line tier sentence, a citation of `## Testing`, and the ordering-rule paragraph. EVIDENCE: implementation-checklist.md preamble.

### [non-spec.16.fix-design-G1.1]
DECISION: remove CODE-4's typed-refusal compensation suppression (the `refused` variable and `&& !refused`), so every `*SlotBindError` in `materializeSlot` and every failed `Binder.Resume` sends the compensation — BECAUSE staged §7.1 "Pod-side reclaim on a failed bind" states the obligation with no carve-out, and stamp-once makes the compensation remove only an entry carrying this attempt's own token (§4.7.1 rule 14); after a rule-5 refusal the entry carries another token, and after a rule-6 refusal it carries this attempt's token or none, so rule 13 answers `superseded` and removes nothing — ALTERNATIVES: adding a §7.1 clause exempting attempts refused under rules 5/6 (rejected: it adds an exception clause to converged spec text to protect a guard that buys nothing, and the guard's premise "a refusal means another attempt owns the entry" is false for rule 6, which is reached only when the entry carries this attempt's token or none).
FACT: on the concurrent path StartSession is the last stage of materializeSlot and is sent once, with no retry — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:313. StartSession carries no token, so rule 5 cannot refuse it, and a rule-6 refusal of it meets an entry that carries this attempt's token or none.
WATCHOUT: the "refusal guard" in CODE-4's `Binder.Prepare` paragraph (non-spec-changes.md ~:1202) and in CODE-8 (~:2082) is CODE-8's reclaim-closure guard. CODE-4's suppression is a different guard, so do not delete those phrases with this fix. — EVIDENCE: non-spec-changes.md CODE-8 "`Binder.Prepare` therefore takes CODE-4's attempt-scoped release".
WATCHOUT: after the removal, CODE-7's sentinels are consumed only by CODE-8's reclaim closures. CODE-4 no longer needs CODE-7, so "precedes CODE-4 and CODE-8" (~:1925) and checklist S11's "the compensation" become stale. The step order does not change.

### [non-spec.16.fix-design-G2.1]
DECISION: CODE-6 `Disposition of an expired acquisition at a removing site` is reduced to what it alone owns: the unguarded removal after the `slot_guard_not_acquired` warning, the definition of `guarded` (the boolean `lockSlotGuard` returns) as a conjunct of each site's own `completed` predicate, and the rejected-release rationale. Hold and report become one citation of SPEC-3's reclaim-hold failed-act sentence and the §5.2 failed-act rows. The per-site table keeps only the "Removal on an expired acquisition" column. BECAUSE spec-changes.md §5.2 table is declared the single home of every disposition cell, and each site already writes its own predicate (non-spec-changes.md:449, :1475, :1503). ALTERNATIVES: keep the table and mark its columns "restated for convenience", which is still a second statement.
WATCHOUT: the site predicates at non-spec-changes.md:444-449, :1475-1477 and :1503-1505 cite the block for the `guarded` term. Keep the sentence that defines `guarded`, or those three citations dangle. EVIDENCE: non-spec-changes.md:1475-1477
DECISION: S15 lands the guard, both hand-out forms, and the expired-acquisition disposition at `releaseSessionSlot`'s guard-acquiring form and at `terminateHeldSession`. S16 lands `Shutdown`'s removing-arm guard, its disposition, and the tier-7a cases that drive a removing `Shutdown`: the lock-order assertion at :3772 and the Shutdown arms of the reclaim-against-admitted-section case at :3785. BECAUSE the arm split and `reclaimSlotLocked` routing are CODE-1's (S16), and the shipped Shutdown has no arm (pkg/adapter/session.go:237-239).

### [non-spec.16.fix-design-G3.1]
DECISION: podmcp_arming_internal_test.go:88,:245 replace `s.deregisterSlot("alice")` with an inline `s.mu.Lock()` / `deregisterSlotLocked` / `s.mu.Unlock()`; the rule is stated once in CODE-6's retirement sentence and the Tests-list entry only names the file — BECAUSE reclaimSlotLocked opens a reclaim hold on "alice" that neither arming test releases — ALTERNATIVES: routing through reclaimSlotLocked (rejected, the hold leaks); keeping deregisterSlot as a test-only helper in export_test.go (rejected, it re-creates the retired function)
DECISION: Client method signatures have one home, the CODE-4 client.go Targets bullet: AssignCredentials/RunSetup take a trailing `bindAttempt string`, PrepareWorkspace takes a trailing `bindAttempt string, midSession bool` (mirroring the shipped FinalizeWorkspace midSession), FinalizeWorkspace takes a `bindAttempt string` before its shipped midSession, and ResumeParams gains BindAttempt. The Client never derives mid_session from an empty token — BECAUSE derivation would restate the §4.7.1 pairing rule in client code
FACT: the only *_test.go files outside client_test.go that call a token-carrying adapterclient.Client method against a real adapter server are tests/tier4_integration/token_service_unavailability_guard_test.go:253 and tests/tier8_chaos/token_service_unavailability_guard_test.go:322, and both call AssignCredentials. adapterclient.ResumeParams{ appears in tests only in client_test.go. The signature change makes every such call a compile error, so no grep sweep is needed for them — EVIDENCE: pkg/gateway/runtime/adapterclient/client.go:180,247,306,327,616
FACT: the Client-method production callers are binder.go:921,933,1256,1327,1607; slotbinder.go:291,299,403; upload_to_session.go:128,134. pkg/embedded/localcli/session.go:396 calls a different client's FinalizeWorkspace (3 args) and is not a site
WATCHOUT: the Client signature change lands at S13 (CODE-4), so the two guard tests are edited at S13 even though the adapter begins refusing an empty token only at S14

### [non-spec.16.fix-design-G4.1]
DECISION: the step conventions (heading-form `// spec:`, `// diagnosis:` at tier 2+, tier 0 and tier 1 on every touched package, spec-map registration, claim-map seeding via the `EXPLICIT` list in `scripts/seed-claim-register.py` regenerated in the same commit) have ONE home: the `## Testing` preamble in non-spec-changes.md. The checklist preamble keeps "Every spec step leads", the per-line tier sentence and the ordering rule, and cites `## Testing` for the rest. SCHEMA-1's `**Claim register.**` cites `## Testing` in both paragraphs — BECAUSE the checklist preamble already defers to `## Testing` for tiers, `## Testing`'s tier clause (tier 0 and tier 1) matches test-coverage.md where the checklist's (tier 0) is weaker, and SCHEMA-1 is in the same file — ALTERNATIVES: home in the checklist preamble (rejected: carries the weaker tier clause and would make `## Testing` cite across files for its own conventions).
CORRECTS [review-log FACT at review-log.md:2992 and :3010, "the seeding convention has one home in the checklist preamble"]: after this round the home is the `## Testing` preamble in non-spec-changes.md; the checklist preamble only cites it.
WATCHOUT: the "in the same commit" clause lives only in the checklist copy today; it must move into `## Testing` or it is lost when the checklist copy is cut — EVIDENCE: implementation-checklist.md:7-9 vs non-spec-changes.md:2992

### [non-spec.16.review-applicability.1]
FACT: golangci-lint is NON-FATAL in tier 0 today: `lenny-test` runs it and returns its output as a "WARNING (non-fatal)" on a non-zero exit (bd lenny-vgl). So a helper landing before its caller (CODE-9's `noteCompensationOutcome` at S10, called only at S19; `incSlotShutdownUntokenedEntry` at S10, called only at S16) trips `unused` but hard-fails no gate. Do not file it as gate breakage until the lint flips to hard-fail. — EVIDENCE: cmd/lenny-test/cmd_run.go:598-618, .golangci.yml:17-21
FINDING-FILED: checklist S15 says it "lands that disposition at all three sites" and lists `Shutdown` on the removing arm, but that arm, its reclaimSlotLocked routing, its `slot_reclaim` answer and its raw guard acquisition are CODE-1's and land at S16 (depends on S15). Shipped `Shutdown` deregisters in one critical section with no arm split. — EVIDENCE: implementation-checklist.md:45,:47; non-spec-changes.md:166-168,:1514,:1853-1857; pkg/adapter/session.go:237-239
FINDING-FILED: two tier-4/tier-8 tests drive a real in-process adapter through `adapterclient.Client.AssignCredentials` with no bind token; neither proto-literal sweep reaches them and they are in no list, and the Client signature change carrying `bind_attempt` is never stated. After S14's `validateBindFields` they are refused INVALID_ARGUMENT. — EVIDENCE: tests/tier4_integration/token_service_unavailability_guard_test.go:253, tests/tier8_chaos/token_service_unavailability_guard_test.go:322, pkg/gateway/runtime/adapterclient/client.go:180
FINDING-FILED (from the standing Open entries): `deregisterSlot`'s two test callers (podmcp_arming_internal_test.go:88,:245) and `manifest_fields_test.go:220`'s `ensureSlotStateLocked` call are undispositioned; the second file is absent from the Tests list CODE-6 says names the files.
FACT: the complete set of pkg/ and tests/ test files calling the adapter slot-resolve/release surface (`ensureSlotStateLocked(`, `ensureSlotPaths(`, `claimSessionSlot(`, `claimSessionSlotUnderLock(`, `ReleaseSlotForTest(`, `releaseSessionSlot(`, `deregisterSlot(`) is twelve files, all in pkg/adapter; manifest_fields_test.go is the one missing from the Tests list. — EVIDENCE: grep over --include=*_test.go pkg tests
FACT: the only test files that call `adapterclient.Client` bind-sequence methods (not proto literals) are client_test.go (listed) and the two token_service_unavailability_guard tests (unlisted). — EVIDENCE: grep of `.AssignCredentials(|.FinalizeWorkspace(|.RunSetup(|.PrepareWorkspace(|ResumeParams{` over adapterclient-importing test files
CORRECTS [### Open, "Does S23's tier list '0, 11' agree with DOCS-4's own 'Tiers: 0'?"]: settled; DOCS-4 now reads "Tiers: 0, 11" (non-spec-changes.md:2981). Retire it.
CORRECTS [### Open, "Does `binder_envtest_test.go` have a reachable envtest home?"]: yes; binder_test.go in the same package already starts `tests/testinfra/envtest` (binder_test.go:41,:180).
USEFUL [non-spec.15.review-applicability.1]: its closures of the S22/S18 and claim-row-placement Open entries held; not re-checked beyond a glance.

### [non-spec.16.review-citations.1]

FACT: No commit has touched pkg/, cmd/, schemas/, spec/, docs/ or tests/ since 2026-09-21, so round 13's mechanical citation sweep still holds for all unchanged proposal text. This round re-checked only the citations added since the non-spec-r14 snapshot, and every one resolves: pkg/adapter/session.go:156-157, pkg/adapter/checkpointtransport.go:99-116 (GetChunk builds on the handler ctx, via resume.go:105,:191), queue.go:18/:185-192/:194-202, start.go:2606-2608 and :2810, schemas/lenny-adapter.proto:941-943, binder.go:1079-1081 and :1200-1202 (drain = DeleteClaim), gc.go:227-246 and :274-289, slotsession.go:109, binder.go:1994/:2037. CODE-9's gateway cites also resolve: gatewaymetrics_credential.go:185-193, gatewaymetrics.go:1170-1175, binder.go:137, slotbinder.go:353-361, metricsbackfill.go:149, pkg/adapter/metrics.go:50,:108 and catalog.go:146,:271, catalog_test.go:188-211, spec/16:186-189, and adapter_metric_catalog_test.go:26,:34,:49,:56,:100,:110. — EVIDENCE: `git log --since=2026-09-21 -- pkg cmd schemas spec docs tests` is empty; diff of scratchpad/cp-snap/0081-opt2/non-spec-r14 against the live files.
FACT: The interface-method check passes. `cl` is a concrete `*adapterclient.Client` at every staged call site (slotbinder.go:265, binder.go:1216). The start.go `slotBinder` interface widening for `ReleaseSlotReservation(..., leaked bool)` is staged in CODE-4's call-site table. `CredentialAssigner.Release(leaseID string)` is staged in the files-touched list, and both production types declare it (credassign.go:380, client.go:299). `Runtime.Close(ctx, sessionID)` exists at session.go:62. — EVIDENCE: as cited.
WATCHOUT: `sed -n` or `grep` over `*spec-changes.md` also matches `*non-spec-changes.md`, which sorts first, so you get non-spec lines labelled as SPEC-n. Use the full file name. — EVIDENCE: proposal directory listing.
DECISION: Filed the Open entry "What replaces `deregisterSlot` at its two test callers?" as a finding. BECAUSE CODE-6 retires `deregisterSlot` (non-spec-changes.md:1492, :4189), yet podmcp_arming_internal_test.go:88 and :245 still call it. The staging dispositions every other test caller in that file (:84/:185/:230 at :775, :144/:195 at :1889) and is silent on these two. This caller's directive says such an entry is filed rather than carried. ALTERNATIVES: carrying it again was rejected under the directive.
DECISION: Filed `manifest_fields_test.go:220` as a false "the tier-1 work below names the files" claim (non-spec-changes.md:1685-1687). No proposal file names it, and it calls the `ensureSlotStateLocked(slotID)` whose signature CODE-6 widens. Every other in-package caller (holdstate_test, usage_test, one_session_only_test, exportpaths_test, export_test) is listed. ALTERNATIVES: judged immaterial once before at the materiality gate. It is re-filed because the directive requires it, and the defect is a tier-0 compile break in a file the list claims to cover.
USEFUL [non-spec.13.review-citations.1]: the sweep record let this round confine re-verification to the r14→now delta.
CORRECTS [Open "Which deliverable DECLARES the untokened-entry counter's accessor"]: this confirms the round-15 correction. The staged accessor has no parameter at all three sites (non-spec-changes.md:337, :2144, :4251).

### [non-spec.16.review-client-surface.1]

DECISION: returned EMPTY — BECAUSE the round-15-to-16 delta (commits 5acaf0b44, 6272cdfea: the decision-47 reclaim-hold sentence, CODE-6's non-blocking acquisition step and its tier-1 case, the unlabeled untokened-entry counter, and the `lenny_adapter_leaked_slots` §16.1/catalog/metrics.md rows) touches no client-facing representation: no proto field, `ErrorCode`, enum value, docs/api, client-guide, runtime-author-guide, OpenAPI, MCP schema, SDK or CRD text moved. `git diff 6f0e6fb8a HEAD -- proposals/0081_*/` shows no DOCS-1..4 or SCHEMA-1 hunk — ALTERNATIVES: none considered filing-worthy.
FACT: the staged gauge row's labels match the shipped registration exactly (`[]string{"pod_id", "pool"}`), so SPEC-6's row, CODE-9's metrics.md row and the tier-11 assertion agree with the emitter. EVIDENCE: pkg/gateway/metrics/gatewaymetrics/gatewaymetrics_credential.go:222-225, gatewaymetrics.go:1217
FACT: the untokened counter's new form is well-founded in the tree: `mustCounter` is the label-free constructor and `incSetTracingContextDropped` is the zero-arg precedent the staging names. EVIDENCE: pkg/adapter/metrics.go:89, :114-117, :209
FACT: every interface method the staged gateway code calls exists or is staged: `CredentialAssigner.Release` is staged (non-spec-changes.md:1135, files-touched :4240) and both implementations carry it (credassign.go:380, client.go:299); `SlotClaimer.ReleaseSlot` already takes the leaked bool (slotbinder.go:559); `Client.shutdown` builder at client.go:813. No adapter-proto copy exists outside schemas/ and pkg/proto/adapter/v1 (grep over sdks/, docs/, charts/).
USEFUL [non-spec.10.review-client-surface.1, non-spec.13.review-client-surface.1]: their SDK/OpenAPI/proto-number sweeps were still true and let this round confine itself to the delta.

### [non-spec.16.review-docs-alignment.1]

DECISION: filed ONE bookkeeping finding. spec-changes.md `## Spec files touched` (the spec/16 bullet at ~:1307) and the docs-mirror paragraph after it (~:1312) still say §16.1 and docs/reference/metrics.md gain "one row for each counter". The SPEC-6 gauge row (`lenny_adapter_leaked_slots`) added by the recent hand edit is missing from both. CODE-9, the checklist S6/S10, the summary index and the non-spec files-touched list (":187 the two counter rows and the leaked-slots gauge row") all carry it — BECAUSE this is the only docs-facing text the round-14→16 staging delta left stale — ALTERNATIVES: none of the settled docs residues re-filed (gateway-crash entry, emptied-workspace, adapter-contract.md:84, internal.md ABORTED, §15.4 hold mirror).

FACT: the new CODE-9 docs rows are sound against the tree. `tests/tier11_docs/adapter_metric_catalog_test.go:26` regex is `lenny_[a-z0-9_]+`, so it catches `lenny_slot_shutdown_untokened_entry_total` despite the missing `lenny_adapter_` prefix; `specCatalogPending` is empty (:49), so the §16.1 row has to land before or with S10, and S10 depends on S6. The gauge has no docs row today (`grep -rn leaked_slots docs/` is empty), and the only other gauge carriers are spec/05:545 and spec/06:160. — EVIDENCE: tests/tier11_docs/adapter_metric_catalog_test.go:26,49,100,110; docs/reference/metrics.md:158-167

FACT: `slotFailureWorkspaceFinalize` adds a new `error_type` value to `lenny_slot_failure_total`. No docs page enumerates that label's values (metrics.md:166 and spec/16:14 name the label only), so no docs edit is owed. — EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:290-298; docs/reference/metrics.md:166

FACT: the Open "DOCS-1 own-voice trigger cell for receiving_uploads → slot_cleanup" is CLOSED. The cell ("A cleanup runs on the slot after its bind is abandoned or fails before the slot reaches `running`, a start still in flight included") agrees with SPEC-4's pre-`running` paragraph, which names the edge on "a bind abandoned or failed", with "a start still in flight" left in `receiving_uploads`. — EVIDENCE: non-spec-changes.md DOCS-1 new row; spec-changes.md SPEC-4 prose-after-fence block

WATCHOUT: docs/operator-guide/observability.md:18-30 states that pod labels follow OTel `k8s_pod_name`. The gauge's `pod_id` departs from that, but the departure is PRE-EXISTING (shipped gauge, spec/06:160 already names `pod_id`), and the SPEC-6 row only documents it as a §16.1.1 local-only extension. The docs page is not newly false. Do not file it.

CORRECTS [review-log Open "Which deliverable DECLARES the untokened-entry counter's accessor"]: the CLOSED text names `incSlotShutdownUntokenedEntry(podID string)` beside `incUnaddressedFrameRejected`, called with `(s.podID)`. The current staging is the no-argument `incSlotShutdownUntokenedEntry()` beside `incSetTracingContextDropped`. CODE-9 at ~:2144 and the CODE-1 call site at ~:337 agree on it. The proposal is consistent and only the log entry is stale. — EVIDENCE: non-spec-changes.md:337,2144; pkg/adapter/metrics.go:207-215

USEFUL [Traps: "The docs-alignment lens's two accepted residues ... filed and refuted in rounds 12, 15, 18 and 20"]: this saved me from re-filing the gateway-crash and emptied-workspace residues.

### [non-spec.16.review-edit-sites.1]
FACT: the round-15 delta (SPEC-6 gauge/counter rows, CODE-9 catalog/docs rows) is edit-site complete. `lenny_adapter_leaked_slots` is in neither `AlertSupportCatalog` nor any alert expr, so adding it to `MetricCatalog()` trips no disjointness or alert-crosscheck gate; `metrics.Validate` forbids only the §16.1.1 high-cardinality list, so `pod_id` passes; no gate parses §16.1 labels or `error_type` values, and no spec/docs surface enumerates `lenny_slot_failure_total` error_type values, so `slotFailureWorkspaceFinalize` owes no docs row. — EVIDENCE: pkg/observability/metrics/alert_catalog_crosscheck_test.go:98-118; pkg/observability/metrics/metrics.go:75-96; pkg/gateway/podlifecycle/podsession/binder.go:287-299
FACT: `TestLeakedSlotCountingLifetimeAgrees_F5231` requires the §5.2 whole-pod trigger line to keep "counted persistently for as long as the slots remain leaked"; SPEC-3 replaces only the parenthetical, so it survives. — EVIDENCE: tests/tier11_docs/concurrent_slot_lifecycle_doc_reconciliation_test.go:90-95; spec/05_runtime-registry-and-pool-model.md:561
FACT: `spec/06:160` still says "The adapter exposes ... `lenny_adapter_leaked_slots`" while the gateway emits it; already carried in the summary's unstaged-defects rows, not newly falsified. — EVIDENCE: spec/06_warm-pod-model.md:160; summary.md:1155
WATCHOUT: CODE-6 retires `deregisterSlot`, but pkg/adapter/podmcp_arming_internal_test.go:88 and :245 call it and no staged text dispositions them; routing them through `reclaimSlotLocked` opens a hold. Filed. — EVIDENCE: non-spec-changes.md CODE-6 "`deregisterSlot` is retired into the helper"
WATCHOUT: the "test call sites are not enumerated ... the tier-1 work below names the files" claim is false for pkg/adapter/manifest_fields_test.go:220 (`setSessionLeasesForTest` calls `ensureSlotStateLocked`). Filed. Every other test caller file (holdstate, exportpaths, slotsession, export, usage, one_session_only, podmcp_arming) IS listed.
USEFUL [non-spec.15.review-applicability.1]: settled S22/S18, the SCHEMA-1 claim-row step split and the CredentialAssigner fake sweep; did not re-check them.

### [non-spec.16.review-fresh.1]

CORRECTS [Open: "Which deliverable DECLARES the untokened-entry counter's accessor"]: the entry says CLOSED with `incSlotShutdownUntokenedEntry(podID string)` and a call `incSlotShutdownUntokenedEntry(s.podID)`. The current staging is the label-free form: CODE-9 declares `func incSlotShutdownUntokenedEntry()` beside `incSetTracingContextDropped`, CODE-1's `answerShutdown` calls it with no argument, and SPEC-6's row reads "unlabeled". All three agree today; the Open entry's text is stale, not the proposal.
FACT: `deregisterSlot` has exactly two callers outside `releaseSessionSlot`, both tests: `pkg/adapter/podmcp_arming_internal_test.go:88` and `:245`. CODE-6 retires the symbol and no edit list disposes of those two sites — EVIDENCE: pkg/adapter/slotsession.go:192, non-spec-changes.md:1492
FACT: SPEC-2's staged §7.1 paragraph has no refusal carve-out: the obligation "begins with an attempt's first such RPC and ends when that attempt succeeds", while CODE-4/materializeSlot and Binder.Resume suppress the compensation on either typed refusal — EVIDENCE: spec-changes.md:380 vs non-spec-changes.md:1085-1095
FACT: every `func (...) AssignProto(` fake under *_test.go also matches the `ReleaseSession(` grep CODE-4's fake sweep uses, so the sweep's set is complete — EVIDENCE: grep over pkg/ tests/ cmd/
FACT: the podsession package already runs envtest (`binder_test.go:41,:180`), so the tier-2 `binder_envtest_test.go` has a reachable home; the Open entry disputing that can close.
USEFUL [Settled trap on `podmcp_arming_internal_test.go`]: named the deregisterSlot break directly; it was never carried into the staging.

### [non-spec.16.review-kubernetes.1]

EMPTY VERDICT. Round 16 of the non-spec loop, Kubernetes-idiom lens. No finding met the bar.

USEFUL [non-spec.10.review-kubernetes.1]: the one-grep opening move still covers the whole kube surface. The staging changed since round 14 (SPEC-6 gauge/counter rows, CODE-9 catalog and docs rows, CODE-6's acquisition step), and none of those edits touches the apiserver: CODE-6's `slotGuards` acquisition is an in-process capacity-one channel under `s.mu`, and the CODE-9 rows are metric-catalog and docs rows only. The kube sites are still CODE-5's `Unhealthy -> DrainSandbox` tail, the tier-2 envtest pair, DOCS-1's pod-state-machine paragraph, and the unstaged coalesced-reconcile defect.

FACT: re-verified this round. `binder.go`'s only apiserver writes are still the two `podclaim.DeleteClaim` calls. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:1102, :1201. `failPhase`'s drain is at :1079. The tier-2 case text at non-spec-changes.md:3648-3660 is still accurate.

FACT: the Open question "Does binder_envtest_test.go have a reachable envtest home?" has an answer. The podsession package already imports `tests/testinfra/envtest` and calls `envtest.Start(t)` in binder_test.go, so a new `_test.go` file in that package can host the tier-2 cases. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder_test.go:41, :180. Whether it owes a `slotAddressCaseFiles` row is still untraced.

FACT: the Open question on `CredentialAssigner.Release` is NOT a finding for a missing declaration. The staged text at non-spec-changes.md:1135-1139 declares the interface widening, and files-touched at :4240 lists it. The interface is at pkg/gateway/podlifecycle/podsession/binder.go:319. Whether the four unnamed test fakes are listed is a question for the edit-sites lens.

### [non-spec.16.review-mechanism.1]

FACT: the literal-construction sweeps (`ShutdownRequest{` and the `adapterv1.(FinalizeWorkspace|RunSetup|AssignCredentials|Resume|PrepareWorkspace)Request{` greps over `*_test.go`) cannot see a test that reaches a REAL adapter through an `adapterclient.Client` method, because the literal lives in `client.go`. Two such callers exist: `tests/tier4_integration/token_service_unavailability_guard_test.go:253` and `tests/tier8_chaos/token_service_unavailability_guard_test.go:322`, both `adapterCli.AssignCredentials(ctx, id, leases)` against `adapter.New` over bufconn (`serveGuardAdapter`, :162 and :243). Filed this round. — EVIDENCE: grep -rn "AssignCredentials(" tests/
FACT: `deregisterSlot` has exactly two callers outside `releaseSessionSlot`: `pkg/adapter/podmcp_arming_internal_test.go:88` and `:245`. CODE-6 retires the function and no deliverable dispositions those two lines; the standing Open "What replaces `deregisterSlot` at its two test callers?" is therefore filed this round rather than carried. — EVIDENCE: non-spec-changes.md CODE-6 "`deregisterSlot` is retired into the helper"; pkg/adapter/slotsession.go:192
FACT: the round-15 acquisition step (non-blocking send first, then select against ctx.Done() only when full) leaves every staged cancelled-context case valid: the `Resume` rollback row's removal runs inside `Resume`'s own held guard, so the channel is full and the acquisition still expires at once. — EVIDENCE: non-spec-changes.md Testing "Start-versus-reclaim rollback, deterministic form"; CODE-6 derivation table `Resume` row
FACT: every interface method the staged Go code calls exists: `RuntimeProcess.Close(ctx, sessionID)` (pkg/adapter/session.go:62, `Server.Runtime RuntimeProcess` at server.go:210), `SDKWarmRuntime.DemoteSDK(ctx)` (sdkwarm.go:134); `cl` in the compensation is the concrete `*adapterclient.Client` (slotbinder.go:265), so `ShutdownReclaim` is a staged method rather than an interface call.
USEFUL [non-spec.15.review-applicability.1]: its closure of the S22/S18 and SCHEMA-1 claim-row Open entries held; I did not re-derive them.
WATCHOUT: the refusal-accounting drain at `maxConcurrentSessions: 2` via `accountSlotFailure` is open decision 34 territory; `[non-spec.10.review-kubernetes.1]` already records why it is not filed. Do not re-file. — EVIDENCE: review-log.md WATCHOUT near "tempting finding here is CODE-5 accounts a bind REFUSAL"

### [non-spec.16.review-performance.1]

DECISION: returned no findings for round 16 — BECAUSE the deltas since round 14 (CODE-6's acquisition step, SPEC-6/CODE-9's gauge catalog row and unlabeled untokened counter) add no store write, watch, reconcile, hot key or serialization point. The try-send-first acquisition removes a random-select branch and adds no wait. The gauge is already emitted (`gatewaymetrics.go:1213-1217`, labels `pod_id,pool`), so a catalog row and a docs row change nothing at runtime — ALTERNATIVES: re-filing the queue-head compensation hold, slotGuards/s.reclaiming growth, or replica-local leak dilution, rejected because each is already an accepted-failure bullet or a standing Open entry, and nothing in the delta changes it.
FACT: the queue-head bullet's citations still hold verbatim at this snapshot: `DefaultMaxQueueWaitSeconds = 30` at queue.go:18; admission select at queue.go:184-192; the exhaustion branch at :194-202; `runWithQueue` at start.go:2606; `binder.BindSlot(ctx, req)` at start.go:2810. — EVIDENCE: pkg/gateway/sessionserver/queue.go:18, :184-202; pkg/gateway/sessionserver/start.go:2606, :2810
FACT: two Open entries the caller asked to be converted into findings are already settled in the text. `Binder.shutdownAdapter` is listed with `unconditional_teardown` (non-spec-changes.md:899, :948-949), and DOCS-4 states "Tiers: 0, 11", which matches checklist S23. Retire both. — EVIDENCE: non-spec-changes.md:2981; implementation-checklist.md:62
USEFUL [non-spec.13.review-performance.1]: its store-surface inventory (no new Redis key, Postgres table or etcd object) still holds, and a grep for postgres/redis/etcd over non-spec-changes.md confirmed it in one command.

### [non-spec.16.review-reliability.1]

DECISION: one finding this round, the two `deregisterSlot` test callers (`pkg/adapter/podmcp_arming_internal_test.go:88`, `:245`) that CODE-6 retires the function out from under with no stated replacement. It is filed under the caller's Open-entry directive, not on reliability grounds — BECAUSE the Open entry **What replaces `deregisterSlot` at its two test callers?** names a symbol the staging deletes, and no deliverable says what those two calls become — ALTERNATIVES: carrying it forward as UNVERIFIED, which the directive does not allow.
FACT: CODE-6's acquisition step (try-send first, then select against ctx.Done() only when the channel is full) is consistent with every staged cancelled-context test. The Resume row of the start-versus-reclaim rollback case calls `ReleaseSlotForTest` from the onStart hook on Resume's own goroutine, so Resume already holds the guard, the try-send fails, and the cancelled ctx expires the acquisition as the row asserts. If that row passed a live ctx it would self-deadlock, which is why it passes a cancelled one. — EVIDENCE: non-spec-changes.md CODE-6 "The acquisition step."; Testing "Start-versus-reclaim rollback, deterministic form" Resume row
WATCHOUT: the Edge-cases bullet **A member parked under its own guard can cost the §10.1.4 pass its whole guard-acquisition deadline** still says that a park outlasting the pass context "leaves every remaining member" taking the expired-acquisition disposition. Under the acquisition step, a remaining member whose guard is free acquires it even on the expired pass context, so only members whose guards are contended take the disposition. The bullet is commentary, so I did not file it; a fixer who opens that bullet should narrow it. — EVIDENCE: non-spec-changes.md Edge cases bullet vs CODE-6 "The acquisition step."
FACT: on an already-expired parent, `contextWithGraceDeadline` returns the expired ctx, and `resolveShutdownGrace` then falls back to the configured or default grace. An unguarded removal on an expired `Shutdown` ctx therefore cannot hang `SocketRuntimeProcess.Close`. — EVIDENCE: pkg/adapter/session.go:327-332, pkg/adapter/mcpruntime.go:312-324, pkg/adapter/socketruntime.go:455-466
FACT: §7.4 mid-session uploads buffer the whole body at the gateway (`parseUploadToSession`) before streaming it to the adapter, so the guard they hold is short, and an ordinary session-end `Shutdown` waiting behind one is not a realistic expiry path. — EVIDENCE: pkg/gateway/sessionserver/upload_to_session.go:101-138
CORRECTS [Open "Does the `outcome` argument of CODE-9's `SlotReclaim` hook have a label to land in?"]: this is answered in the staging. The tier-1 gatewaymetrics case calls `IncSlotCompensationSuperseded` "in the parameter order of the `SlotReclaim` hook", which is the three-argument `IncSlotFailure` form, so direct assignment at metricsbackfill.go compiles. Retire the entry.
USEFUL [non-spec.15.fix-design-G1.1]: its WATCHOUT that every cancelled-context test holds the guard saved re-tracing all four.

### [non-spec.16.review-security.1]
DECISION: empty findings list for round 16 — BECAUSE the delta since the round-14 stop (6f0e6fb8a..HEAD) is CODE-6's acquisition step (non-blocking send first, then select against ctx.Done() only when full), SPEC-3's decision-47 sentence, the label-free untokened counter, and the leaked-slots gauge catalog/docs rows; none relaxes a control or moves a security bound to an in-pod source — ALTERNATIVES: re-filing Open entries on adapter gRPC reachability from the agent container and §12.4 durability of the leaked ledger, rejected as pre-existing and outside the staging.
FACT: the acquisition step only widens when a guard is HELD (uncontended acquisition on a done ctx now succeeds); on a destructive site that means the removal runs guarded instead of unguarded, and on an admission site the handler then fails on its own dead ctx. Neither path admits a bind the hold would refuse, because acquireSlotGuardForResolve checks s.reclaiming before the acquisition. EVIDENCE: non-spec-changes.md CODE-6 "The acquisition step." and the acquireSlotGuardForResolve bullet; pkg/adapter/session.go:156-157
FACT: the only Go interface call in the staged Shutdown body is s.Runtime.Close(ctx, sessionID), declared on RuntimeProcess. EVIDENCE: pkg/adapter/session.go:46-62, pkg/adapter/server.go:210
USEFUL [non-spec.14.review-security.1, spec.30.review-security.1]: CredentialAssigner.Release widening and the gateway-set leaked gauge were already traced; saved re-deriving both.

### [non-spec.16.review-single-source.1]

CORRECTS [non-spec.14.fix-G1.1] and the review-log `### Open` CLOSED line plus Settled line ~411: the untokened-entry accessor is now the no-argument `incSlotShutdownUntokenedEntry()` and the series is an unlabeled `mustCounter` (non-spec-changes.md:337, :2139-2146; spec-changes.md:1224). The `(podID string)` / `s.podID` form those log lines record is gone. Compaction should rewrite them.
FACT: three Open entries are now closed by the tree of the proposal: S22 depends on S18 (checklist:60); S23 and DOCS-4 both say Tiers 0, 11 (checklist:62, non-spec-changes.md:2982); the claim-register rows have one placement (SCHEMA-1 non-spec-changes.md:2657-2659, CONF-1 :3637, files-touched :4074-4076, checklist S9/S22).
DECISION: filed three (g) findings: the CODE-6 expired-acquisition disposition re-states SPEC-3's unordered-removal sentence and the §5.2 table's failed-act cells plus every site's `completed` predicate; the Shutdown pre-running expired-acquisition cell is asserted by two test bullets (CODE-6 destructive-expiry and the CODE-1 pair); the step conventions (spec/diagnosis annotations, spec-map, claim-map seeding) are written in full in both the checklist preamble and the `## Testing` preamble, and SCHEMA-1 names each as the home in consecutive paragraphs — BECAUSE each is a rule a reader could implement from either site — ALTERNATIVES: rejected the SPEC-6 leaked-slots row restating §6.2's `pod_id`/`pool` labels (a §16.1 catalog row is the natural home; spec lane converged) and the summary's "two further residues" bullet (pricing rationale).
WATCHOUT: the new acquisition step (non-blocking send first) has exactly one home, non-spec-changes.md:1750-1758; the two hand-out bullets cite it. Keep it that way. — EVIDENCE: grep "acquisition step" returns :1763, :1773, :3137 only as citations.

### [non-spec.16.review-test-coverage.1]

DECISION: returned an empty findings list for round 16 — BECAUSE each delta since round 14 has a named test at the tier it reaches. CODE-6's acquisition step is covered by the tier-1 "An uncontended acquisition on a cancelled context holds the guard" case, which loops at least 64 times over both hand-out forms. The leaked-slots gauge's `catalog.go` entry is held by the two-way `spec161Metrics` reconciliation (`TestMetricCatalogIsCompleteAgainstSpec161` and `TestMetricCatalogHasNoUnspecifiedMetrics`, pkg/observability/metrics/catalog_test.go:188-211). Its docs row is held by the new `tests/tier11_docs/slot_compensation_metric_reference_test.go`, which asserts the `pod_id` and `pool` labels. The label-free untokened counter is held by the shipped `adapter_metric_catalog_test.go` sweep and by the tier-1 "untokened-entry arm fires its counter" case — ALTERNATIVES: (1) filing a missing test that the gauge's catalog Type equals the §16.1 "Gauge". Rejected as nice-to-have: no shipped gate checks any catalog entry's type against §16.1. (2) Filing the uncovered `deregisterSlot` and `ensureSlotStateLocked` test callers (podmcp_arming_internal_test.go:88,:245; manifest_fields_test.go:220). Rejected because they are compile and edit-site questions rather than test-listing adequacy, and tier 0 catches them.
FACT: `metrics.Validate` checks only the name form, snake_case and the §16.1.1 forbidden list (session_id, operation_id, and so on). It never compares labels against the catalog, so giving `lenny_adapter_leaked_slots` a catalog entry cannot break the gauge's `pod_id` registration at runtime. — EVIDENCE: pkg/observability/metrics/metrics.go:75-98, :108-113
FACT: the podsession package already runs envtest in `binder_test.go`, so the tier-2 `binder_envtest_test.go` has a reachable home. The standing Open entry "Does binder_envtest_test.go have a reachable envtest home?" can be closed. — EVIDENCE: grep -l controller-runtime/pkg/envtest pkg/gateway/podlifecycle/podsession/binder_test.go
FACT: under the acquisition step, the start-versus-reclaim `Resume` row still expires its `ReleaseSlotForTest` acquisition. The onStart hook runs inside `Resume`, which holds the slot's guard, so the channel is full and the select meets only `ctx.Done()`. Round 15's WATCHOUT holds. — EVIDENCE: non-spec-changes.md "Start-versus-reclaim rollback, deterministic form"
USEFUL [non-spec.15.fix-design-G1.1]: its WATCHOUT that every staged cancelled-context case runs with the guard held saved me re-walking four cases against the try-send-first rule.

### [operator.post-r16]

DECISION: the operator confirmed on 2026-09-23 that every failed bind attempt is compensated, typed refusals included, as round 16's `[non-spec.16.fix-G1.1]` staged to match §7.1. ALTERNATIVES: restoring the suppression and carving typed refusals out of §7.1, rejected because it reopens the spec lane for a case whose only removal is an orphan. Two read-only reviews checked whether the compensation can reclaim a session the gateway still treats as live, and found it cannot. On the SDK-warm path, `Binder.Prepare` sends no compensation and `Binder.Launch` carries no token. On the §7.3 resume path, `Binder.Resume` sends one token-carrying RPC and its entry is started at creation, so rule 14 matches only an entry whose own `Resume` the adapter completed and the gateway reported failed, a pod the gateway never publishes.

DECISION: the superseded counter keeps every compensation and gains a `cause` label, `refusal` or `failure`, whose value `compensationCause` in CODE-4 derives from CODE-7's sentinels. SPEC-6's §16.1 row states it. BECAUSE a compensation after a typed refusal answers `superseded` routinely, and without the label it would bury the race between attempts the series exists to surface. ALTERNATIVES: excluding refusal compensations from the series, rejected by the operator because it hides them.

DECISION: CODE-5's resume classifier gains an arm for CODE-7's `ErrSlotBindAlreadyStarted`, so a §7.3 resume refused under rule 6 holds the row in `awaiting_client_action` for the client's retry instead of failing it. BECAUSE on the resume path each retry makes a new claim, which may land on another pod, and the refusal belongs to the pod holding a residue entry, which survives a recycle because the whole-pod scrub releases no registry entry. The rule-6 reach and the classifier arms stand at CODE-5, and the defects row on `codes.Unavailable` resumes is rewritten to name what the arms leave.

DECISION: two concurrent `/finalize` calls for one session both run `Binder.Prepare`, and on an SDK-warm pod the loser's unfenced `DemoteSDK` can end the winner's live session. It is pre-existing, recorded among the defects this proposal does not stage, and staged by a separate proposal. SPEC-1's `DemoteSDK` row now reads "the registry's single entry, whichever session holds it", so it no longer reads as a guarantee that the entry is the demoting attempt's.

CORRECTS [`[non-spec.16.fix-G1.1]`, the WATCHOUT that rule 6 "is reached on an entry carrying this attempt's token or none"]: the own-token case is reachable only on the concurrent-bind path, where an abandoned attempt's late tokenless `StartSession` starts a retry's fresh entry, and there the compensation's rule-14 reclaim removes an orphan. The SDK-warm and resume paths never reach it. The instruction not to reintroduce the suppression stands.
CORRECTS [CODE-7's `ErrSlotBindAttemptSuperseded` doc comment]: its "The caller must not compensate" sentences contradicted CODE-4 after round 16 and are deleted.
CORRECTS [`### Settled`, "the §7.3 resume classification gap is closed by ONE arm in `isTransientPodClaimError`"]: the classifier now carries two arms. The `codes.Aborted` arm stands as stated; the second matches CODE-7's `ErrSlotBindAlreadyStarted` with `errors.Is` rather than the `FailedPrecondition` code, because this proposal reclassifies only the refusals it introduces; other resume causes keep the shipped classification, which §6.2's `resuming` failure transitions already contradict and the summary records as a defect this proposal does not stage.
CORRECTS [`### Settled`, "The §16.1 counters add no label dimension"]: `lenny_slot_compensation_superseded_total` now also carries `cause`, a two-valued label that leaves its cardinality per pod, and `lenny_slot_shutdown_untokened_entry_total` carries no label since `[operator.48-49]`.
WATCHOUT: `compensationCause` lives in CODE-4 (S19), not CODE-9 (S10), because it reads CODE-7's sentinels and S10 lands before S11. CODE-9's forwarder takes the cause as a string.
CORRECTS [the CORRECTS on the Open "Does the `outcome` argument of CODE-9's `SlotReclaim` hook have a label to land in?", which calls the hook's form the three-argument `IncSlotFailure` form]: the hook and `IncSlotCompensationSuperseded` now take `(outcome, cause, pool, podName)`, and the tier-1 case passes all four.
