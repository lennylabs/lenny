# Review log: A failed session bind leaves a stale adapter slot registry entry

## Standing context

CHANGELOG (compaction pass after spec round 1 of run 0081-opt3, 2026-09-23). Read the whole ledger, from
`[non-spec.9.review-edit-sites.1]` through `[spec.1.review-single-source.1]`, and lifted its durable residue.
Lifted: the spec-round-1 decisions (the §5.2 hold scope narrowed to requests rule 1 admits; the §4.7.1 paragraph after
rule 9 as the one home of the two client envelopes), the rule-6 slot-path envelope fact, and about forty FACT, WATCHOUT
and MISTAKE lines from the non-spec rounds 9 to 16 and spec rounds 27 to 30 that the hard compaction had not carried
(credit-gate granularity, the cleanup-budget worked-number history, lock-order acyclicity, the tier-0 lint being
non-fatal, and the recurring non-findings). Closed and moved to `### Settled`: four Opens (the rule-6 envelope, the
§10.1.7 preStop drain, the concurrent-pod `credentials.json` residue, and the DOCS-1 trigger cell) and the S10 `unused`
Open (lint is non-fatal). Added to `### Open`: the `recycle_scrub_path_test.go` wrapped carrier, the `stageWorkspace`
threading gap, the CONF-1/tier-3 driver restatement, the §5.2 `SLOT_FAILED` naming gap, and the `ErrorCode` header
disagreement. Added to `### Deferred`: checklist S1's false rationale clause. Annotated two spec-changes.md Deferreds as
filed in spec round 1 with no fix recorded. Deleted: the `### Retired` subsection, whose content is in this paragraph:
the hard compaction before run 0081-opt3 archived the prior 1,438-line standing context in
`0081_fix_a-failed-session-bind-leaves-an-adapter-slot-registry.review-log-archive.md` under
`## Standing context archived 2026-09-23 before run 0081-opt3`, and retired the Opens its `CORRECTS` closed (S22 on S18,
the claim-register row step, S23 tiers, `binder_envtest_test.go`, the `deregisterSlot` callers, `manifest_fields_test.go`,
the `CredentialAssigner` fakes, the `SlotReclaim` hook arity, the untokened accessor, `Binder.shutdownAdapter`, and tier
5), the CODE-10 second-wrap Deferred, and every entry for decisions 20, 41, 45, 47, 48 and 49. Target: NOT reached. The
section is about 580 lines, because `### Settled` alone holds about 245 one-line entries and every `### Traps` entry
records a dead end; dropping either loses a claim nothing else carries.

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
- **Two adapter `ErrorCode` values are minted, 28 and 29, and they take NO §15.1 row.** §15.1 catalogs the client-facing `code`, and no adapter `ErrorCode` string reaches REST. The enum is nested, so the Go constants are `adapterv1.Error_ERROR_CODE_*`.
- **DECISION (`[spec.1.fix-G2.1]`): the staged §4.7.1 paragraph after rule 9 is the ONE home of why neither adapter code takes a §15.1 row and which envelope the client receives.** It names two owners: the §15.1 envelope of the endpoint that issued the bind sequence, and, on a pool serving concurrent sessions, §5.2's `**Client error on exhaustion**`. Other sites cite it. Rejected: a §15.1 `SLOT_FAILED` row (decides open decision 29).
- **A rule-6 `FAILED_PRECONDITION` at a non-setup bind stage on the concurrent slot path reaches the client as 422 `SLOT_FAILED`, `policy_rejection`, `retryable: false`** (`SlotBindError.Reason()` maps `FailedPrecondition` to `transient` only at `slotFailureWorkspacePrep`). That envelope's only spec definition is §5.2 `**Client error on exhaustion**`; `SLOT_FAILED` appears nowhere in `spec/` (`[spec.1.review-client-surface.1]`, `[spec.1.review-reliability.1]`).
- **`Binder.Resume` issues no `RunSetup`,** so §15.1's "resume-time setup" `SETUP_COMMAND_FAILED` route has no producer in the tree.
- **The gateway reads no adapter `adapterv1.Error` category anywhere,** so the hold refusal's unstated category is not a mechanism defect.
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
- **DECISION (`[spec.1.fix-G1.1]`): §5.2's hold scope sentence is further narrowed, by citation, to requests §4.7.1 rule 1 (the pairing rule) ADMITS.** A malformed request meets rule 1's `INVALID_ARGUMENT` before rule 2. Rule 2 and both §15.4 blocks stay unedited and inherit the scope. Rejected: a §15.4 carve-out; "requests reaching rule 2" wording (a citation loop).
- **Staged CODE-6 already orders rule 1 before the hold on every guarded RPC** (`acquireSlotGuardForResolve` runs where `validateBindFields` runs), and CONF-1's rule-2 case drives a well-formed request, so the scope fix changed no code or test text.
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
- **`lenny_adapter_leaked_slots` is registered and emitted by the GATEWAY,** from `applySlotRetryPolicy` alone, labeled `pod_id` and `pool`, while spec/06:160 and spec/05:545 attribute it to the adapter, and §6.2's claim that the adapter publishes `leaked_slots` in `/healthz` is false. The attribution is a recorded unstaged defect.
- **The gauge's label carriers (`CORRECTS` by `[spec.30.review-single-source.1]`):** spec/06:160 names `pod_id` and `pool`, and only spec/05:545 names the gauge without labels. The lists agree with SPEC-6's row. `pod_id` carries the pod name (`sbe.Pod`).
- **The gauge is per gateway replica and in memory, is zeroed at the whole-pod drain rather than at pod termination, and is never deleted,** so its series count grows with drained pods. All pre-existing; any remedy (`DeleteLabelValues`) is code-lane and was not filed.
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
- **The §10.1.7 preStop drain discharges the §7.1 reclaim obligation** (`[spec.1.review-reliability.1]`): readiness flips, in-flight requests finish, and a synchronous compensation sends during the drain. A drain that overruns ends in SIGKILL, which is the gateway-crash residue the Edge-cases bullet records.
- **A `credentials.json` left by a row-3 cleanup on a concurrent pod stays inside the deployer-acknowledged isolation** (`[spec.1.review-security.1]`): every slot's credential file is group-readable through `lenny-cred-readers` (spec/05:517), and whole-pod scrub step 0 purges it. Decision 38 stands on security grounds too.
- **DOCS-1's own-voice trigger cell for the new `receiving_uploads → slot_cleanup` row agrees with SPEC-4's pre-`running` paragraph** (`[non-spec.16.review-docs-alignment.1]`).
- **golangci-lint is NON-FATAL in tier 0 today** (`cmd/lenny-test/cmd_run.go`, "WARNING (non-fatal)"). A helper landing before its caller (the untokened accessor at S10, called at S16; `noteCompensationOutcome` at S10, called at S19) trips `unused` but fails no gate until the lint flips to hard-fail.
- **Agent pods render `RestartPolicy: Never`,** so an adapter crash ends the pod, and the registry, hold and `slotGuards` never survive into a restarted adapter. No adapter-restart recovery case is owed.
- **Leaked-slot occupancy is durable on the §7.1 reclaim path:** `SlotClaimer.ReleaseSlot(leaked=true)` skips the Redis DECR, so the slot stays counted in the §12.4-backed counter. Only `slotstate.Registry` and `slothealth.Tracker` are in-process.
- **The lock orders never cycle.** `Shutdown` is `s.mu → release → guard → s.mu`; `releaseSessionSlot`, `terminateHeldSession` and `acquireSlotGuardForResolve` are `guard → s.mu`; §10.1.4 pass 1 holds `s.mu` across every member and takes no guard.
- **The only writers still writing under an identifier after its deregistration are the guarded sections** (`PrepareWorkspace`, `FinalizeWorkspace`, `RunSetup`, `Resume`); `Checkpoint` only reads. `AssignCredentials` writes `credentials.json` inside `s.mu`.
- **`Resume` restores into two roots (`paths.Current`, `paths.Sessions`), and "the slot's workspace directory" names the whole tree-removal act,** as spec/05:545 uses it. `ExtractTree` is synchronous, so `Resume`'s own rollback runs after its writing stopped and the decision-47 sentence never marks it failed.
- **On an already-expired parent, `contextWithGraceDeadline` returns the expired context and `resolveShutdownGrace` falls back to the configured or default grace,** so an unguarded removal on an expired `Shutdown` context cannot hang `SocketRuntimeProcess.Close`.
- **§7.4 mid-session uploads buffer the whole body at the gateway (`parseUploadToSession`)** before streaming, so the guard they hold is short. The §7.4 admission guard is a live-binding check returning 409 `TARGET_NOT_READY`, which keeps `mid_session`'s exemption from being a bypass.
- **`SessionUsageMeter.Usage` and `Cumulative` ignore their context,** so `emitFinalUsage` cannot lose a report to an expired context. "No final usage report is lost" in the §10.1.4 bullet is not a finding.
- **`slotCleanupBudget` cannot divide by zero.** `slotBindRequest` is built only under `MaxConcurrentSessions > 1`, and `ResumeRequest.MaxConcurrentSessions` is floored at 1 by `maxConcurrentSessions()`.
- **`SlotBindRequest.CleanupTimeoutSeconds` IS populated on every concurrent pool** (from `match.CleanupTimeoutSeconds` via `foldPoolPolicy`), although the struct's doc comment says empty on a non-recycling pool. `poolstore` validation enforces `cleanupTimeoutSeconds >= maxConcurrentSessions*5` when non-zero, so the 5s floor never binds on a configured pool.
- **spec/05 has NO stated default for `cleanupTimeoutSeconds`,** and `maxConcurrentSessions: 1` is stated as the default only at spec/05:393. §5.1 runs to line 364 and §5.2 starts at 365, so spec/05:166 (`research-pipeline`) is a §5.1 derived-runtime example.
- **One queued attempt runs up to TWO `BindSlot` calls** (`maxSlotRetries = 1`), so the queue-head hold is up to two compensation budgets. The wait bound is tested after admission (`case <-ticket` then the deadline check).
- **The adapter process imports no client-go or `rest.Config`,** so nothing in CODE-1, CODE-2 or CODE-6 touches the §10.3 zero-RBAC posture.
- **The §10.1.4 hold-state allowlist is exactly five methods** (`CoordinatorFence`, `NegotiateVersion`, `AdapterEvents`, and the two health probes), so the "compensation refused in coordinator-hold state" accepted mode is exact.
- **The §15.4.2 drain gate moves INWARD** (from `bound` to `started`, still under `!boundRemains`); the widening from `bound` to `removed` is on the slot-tree removal only. `cancelPodMCPIfRuntimeIdle` is double-guarded, so the widened gate cannot cancel a live claimant's arming.
- **`maxScrubFailures` is fed by `ReportPodScrub`, never `ReportSessionScrub`,** so SPEC-3's report biconditional cannot relax the residual-state retirement ceiling.
- **`codes.Aborted` is produced in shipped production code only at `pkg/adapter/checkpoint.go:115`** (the checkpoint op lock), which no resume path reaches, so CODE-5's `Aborted` arm cannot fail open on an auth error.
- **The attempt-scoped release covers user-source leases too.** `UserCredentials.MintProto` writes into the pool assigner's lease store, so `Release(leaseID)` reclaims either kind.
- **The token appears in no metric label.** CODE-9's series carry `pool`, `k8s_pod_name`, `outcome` and `cause` only, so a token-leak hunt ends there; the token is 128 bits of crypto/rand and never travels in a message.
- **The tier-0 credit gate keys on the FINEST declared spec-map id.** `creditKey` stops at the first declared ancestor and `tests/spec-map.json` declares `4.7.1`, so a whole-file `4.7` credit never satisfies a case citing §4.7.1.
- **The `// spec: §4.7.1 (...)` form parses under the tier-0 inventory gate** (`specTagRE` matches anywhere in a comment line).
- **`metrics.Validate` checks name form and the §16.1.1 forbidden list only, and the leaked gauge is in no alert catalog,** so its `catalog.go` entry trips no gate. No gate parses §16.1 labels against §16.1.1.
- **§16.1.1 admits an inline-documented domain label (spec/16:310),** so SPEC-6's `cause` label needs no attribute-table row.
- **The §4.7 `Shutdown` row's pod-global signal condition has no competing spec statement;** §29.4 step 13 is the one trigger site, staged by SPEC-1.
- **§10.1's `**Hold state timeout**` bullet states no graceful window,** so the §5.2 ten-second figure has one spec carrier. The only spec sites outside §4.7/§4.1 describing `Shutdown` (spec/05:459, spec/11:263/270, spec/29:696) restate no teardown precondition.
- **Register arithmetic:** `tests/claim-map.json` holds 76 rows today (32 WIRED, 24 UNWIRED, 20 ABSENT), 78 after S9, and 79/34/24/21 after S22. Do not recompute the summary's end state.
- **Client-method production callers are `binder.go` (five sites), `slotbinder.go` (three) and `upload_to_session.go` (two).** The only test callers against a real adapter are the tier-4 and tier-8 `token_service_unavailability_guard_test.go` files, which the S13 signature change breaks at compile time.
- **The S-2 covered-file sentence lives at THREE sites** (two in summary.md, one in problem-statement.md). Grep `S-2's covered list` before changing any of them.
- **The two mechanical-sweep greps (`ShutdownRequest{` and the `adapterv1.` bind-field grep) are each written at THREE byte-identical sites** (checklist, Testing, files-touched), while CODE-10 and CODE-12 state theirs once. Editing either sweep is a three-site edit.
- **`binder.go`'s only apiserver writes are the two `podclaim.DeleteClaim` calls,** so the tier-2 case's "leaves the `SandboxClaim` present and the `Sandbox` untouched" is exact. No component writes another's status, no finalizer is added, and no reconcile sits on a request path.

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
- **WATCHOUT (`[spec.1.fix-design-G2.1]`): do not name `SLOT_FAILED` or 422 in staged spec text.** No spec section
  defines either, and naming them decides open decision 29.
- **WATCHOUT: judge the malformed-request-during-hold contradiction at §5.2's hold sentence, never at §15.4.** An
  earlier fix made §15.4 cite "a request the §5.2 hold refuses", which moved the unconditional scope into §5.2 instead
  of removing it; `[spec.1.fix-G1.1]` fixed it there. Add no malformed-request exception to §15.4.
- **MISTAKE, the per-slot cleanup budget's worked example (rounds 9 to 12).** Rounds 9 and 10 each corrected the NUMBER
  (fifteen, then thirty) and left the example standing; round 11 then filed the line anchors (:166 is §5.1, :399 states
  no default). The budget divides by `maxConcurrentSessions` and clamps at 5, so HIGHER concurrency SHRINKS it; the worst
  case is a low-concurrency pool with a large `cleanupTimeoutSeconds`. Every instantiation, "five on an unset pool"
  included, is deleted. Cite spec/05:545 and never re-add a figure; the 30s `maxQueueWaitSeconds` is unrelated.
- **MISTAKE: marking an entry "FILED" is not its remedy landing (`[non-spec.14.review-reliability.1]`).** The
  untokened-accessor Open read "OPEN, FILED" for rounds with the call still undeclared. Two spec-changes.md Deferreds
  were filed in spec round 1 with no fix entry; treat them as open until a `CORRECTS` says the text changed.
- **MISTAKE, at least six archived FACTs called "three of S-2's covered handler files" exact.** Each checked only the
  UNCOVERED files and never `credentials.go`, which is covered and opened. The set is four; name them, never count them.
- **WATCHOUT: a whole-file credit at a PARENT spec section never satisfies the tier-0 slot-address credit gate** when
  the map declares the child. Compare a new `slot*_test.go` file's `// spec:` ids against its registration id for id
  at full granularity; `4.7` against `4.7.1` is the live trap here.
- **WATCHOUT: "expired acquisition" now means CONTENDED past the context** (`[non-spec.15.fix-G1.1]`). Every staged
  cancelled-context test (destructive-expiry, admission-refusal, the `Resume` rollback row, the `Shutdown` expiry case)
  runs with the guard HELD, so they stay valid under try-send-first. Do not rewrite them or restate the step elsewhere.
  The `Resume` row passes a cancelled context because a live one would self-deadlock on `Resume`'s own guard.
- **WATCHOUT: the `Resume` rollback row cannot be driven by a concurrent `Shutdown`.** `Resume` holds its guard from
  before `claimSessionSlot` to the end of the call, so the removing arm deadlocks; the row removes through
  `ReleaseSlotForTest` from an `onStart` hook on `probeRuntime` (a fixture extension the bullet declares).
- **WATCHOUT: the refused-refusal drain at `maxConcurrentSessions: 2` via CODE-5's `resumeOnPod` accounting is open
  decision 34.** It reads like a fresh kube or mechanism finding ("drains a healthy pod on the first refusal"); filing
  it resolves an open decision.
- **Refuted, do not re-file: "CODE-8 skips `failPhase`, but the caller deletes the claim anyway."** On `/finalize`,
  `Prepare`'s error returns unchanged with no claim release; on the combined one-call path the rollback runs on an
  exclusive claim, but no typed refusal is reachable there, because the pod is fresh and the session identifier new.
- **Refuted as edit-list polish (round 12): the five in-stage `cl.Close()` calls in `materializeSlot`,** which CODE-4's
  wrapper split must move so the compensation runs on an open connection. The `stageWorkspace` threading gap is the
  same class (see `### Open`); a re-filing owes an argument that distinguishes it from that refutation.
- **WATCHOUT: CODE-4's `noteCompensationOutcome` rationale says "can" where its argument needs "cannot".** Below the
  bar; a repair must not read it as licence to make the forwarder package-level.
- **WATCHOUT: read CONF-1 before filing a tier-3 or tier-10 coverage gap.** The tier-3 block points at CONF-1's case
  list and abbreviates it. The checklist's per-step "Tiers:" lines are tiers to RUN, and a deliverable's "Tiers:" line
  names the tiers its own new gates land at; neither is a coverage claim.
- **WATCHOUT: four tier-3 bullets (rules 2, 6, 8 and 13) re-describe CONF-1's case DRIVERS near-verbatim.** They agree
  today. A fixer editing a CONF-1 case must edit the tier-3 bullet for the same rule; "reaches exactly one site" holds
  for assertion sets only.
- **WATCHOUT: do not re-add a partial list of step conventions to the checklist preamble.** It keeps "Every spec step
  leads", the per-line tier sentence, a citation of `## Testing`, and the ordering rule.
- **WATCHOUT: the two tier-4/tier-8 guard tests are edited at S13** (the Client signature change), although the adapter
  begins refusing an empty token only at S14.
- **WATCHOUT: the accepted "revoked session reaches `running` on an empty workspace" case is not a revoke bypass.** The
  attempt that recreates its own entry after an unconditional `Shutdown` covers a §11.4 full revoke; shipped
  `ensureSlotStateLocked` already recreates entries. File it only with evidence the proposal widens it.
- **WATCHOUT: the lost-compensation-on-gateway-crash residue is accepted with the token mechanism.** Only a SIGKILL
  mid-bind reaches it; do not re-file it as a reliability regression.
- **WATCHOUT: the concurrent `/finalize` `DemoteSDK` race is a pre-existing defects row staged by proposal 0082,** never
  an accepted failure mode of 0081, so its absence from the Edge-cases section is not a finding.
- **WATCHOUT: the coalesced-reconcile §4.6.1 window has a second variant** (a `reserved → bound` rebind plus a failed
  bind's claim DELETE before the controller projects `claimed`, projected `idle` while unscrubbed). Same root cause as
  the defects row; a problem statement against §4.6.1 names both. The no-claim `sdk_connecting` leg returning
  `("", false)` is also pre-existing.
- **WATCHOUT: the `ErrorCode` enum header and `schemas/embed.go`'s "closed §15.1 catalog" comment were already loose**
  before this proposal (six earlier codes have no §15.1 presence). Rounds 10 and 13 declined to file it; the `### Deferred`
  entry on the header is newer and disagrees (see `### Open`).
- **Recurring non-findings, third list:** bare-basename citations (each resolves by context); `status.Convert`'s two
  test hits under `pkg/gateway`; the summary's "125 times" (spelling-specific, do not "correct" to 139); the
  `ShutdownRequest.recycle` proto comment (pre-existing); `SLOT_FAILED` missing from `error-catalog.md`; DOCS-4's
  "beside DOCS-2" against S23; `summary.md`'s SPEC-7/SPEC-8 (other proposals'); off-by-two drift in the 0078 row;
  `runtimeHoldsLocked` (created and consumed at S17); CODE-2's header omitting `slotsession.go`; the untested
  `metricsbackfill.go:149` wiring; `Retryable: true` unasserted; the unconstructible `mid_session` rows; the unasserted
  exactly-once scrub; SPEC-4's replacement-only `claimed ──→ draining` fence (a verbatim matcher flags it falsely); SPEC-1's
  commentary naming three unconditional-teardown callers (`shutdownAdapter` is a fourth); §7.2 step 3's "connection
  that attempt still holds"; the §4.7 row tail "On the default disposition the pod is replaced."; "§6.2's
  projection-input enumeration becomes incomplete"; the §4.7 RPC tables sitting under the `#### 4.7.1` heading;
  `observability.md`'s `k8s_pod_name` against the gauge's `pod_id`; the SPEC-1 `DemoteSDK` commentary's rare-case
  "fresh entry" wording; `session.go:219-224` cited for the gateway's `deadline_ms`; the §4.6.3 `SandboxClaim` notes
  cell (pre-existing); `slotSubjectFileRE` matching "slot" anywhere against the proposal's `slot*_test.go` paraphrase;
  `session_scrub_report_addressing_doc_reconciliation_test.go:6-8,:20-21` (CODE-10's grep returns both, and the
  non-carrier arm's "two tier-11 files under checklist S4's sweep" are the two the SPEC-3 carrier table names).

### Open

- **Does rule 8's untokened arm compare equal across a replacement?** UNVERIFIED: two untokened entries under one identifier compare "" to "", so `noteRuntimeStarted`'s replaced-entry check would record. No gateway path producing it is known; start from whether a `StartSession` or `ConfigureWorkspace` can be an attempt's FIRST request on a pod.
- **Should §5.2 name the `SLOT_FAILED` code value and the 422 status of its exhaustion envelope?** OPEN, summary open decision 51 [`[spec.1.fix-design-G2.1]`]: a separate spec gap overlapping open decision 29.
- **Is the §15.1 split between the setup-command request and every other bind-sequence request wanted?** OPEN, summary open decision 29: the same refusal is non-retryable at one stage and retryable at another.
- **Does the process-group kill fall inside the "every act that cleanup owes the slot" completion predicate?** UNVERIFIED: nobody traced where the kill runs or whether its failure is observable.
- **Does CONF-1 owe a failed-cleanup arm for the reclaim-hold rule?** OPEN, summary open decision 36: a third-party harness cannot force a failed removal.
- **Neither newly recorded residue has any observability** OPEN, summary open decision 33.
- **Are "registry entry" and "bound entry" defined anywhere?** OPEN, summary open decision 32.
- **Churn from the third accounting caller** OPEN, summary open decision 34: at `maxConcurrentSessions: 2` a failed §7.3 re-attach drains a fresh pod.
- **Should SPEC-5 also correct `spec/06:290`?** OPEN, summary open decision 30. Pre-existing.
- **Should the tier-3 descriptor gate pin the `SlotReclaimOutcome` enum values?** OPEN, summary open decision 50 (`[index-reconcile.5]`).
- **Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?** OPEN, pre-existing: `slothealth.Tracker` and `slotstate.Registry` are replica-local and in-process, so a restart forgets leaked pods, and this proposal raises the leak rate.
- **Is the adapter's gateway-facing gRPC server reachable from the agent container, and does it require mTLS?** UNVERIFIED, pre-existing: if unauthenticated, an in-pod agent could send a co-tenant's unconditional `Shutdown`.
- **Does the SPEC-6 `lenny_adapter_leaked_slots` row define the series the tree emits?** OPEN, summary open decision 52, filed by `[spec.28.review-mechanism.1]` and not fixed: the row says "the per-pod count of slots in the `leaked` sub-state, which stay counted until the pod terminates", while the gauge moves only on `applySlotRetryPolicy` and is per replica. `[spec.28.review-reliability.1]` declined it as a restatement of spec/06:160.
- **CODE-1 (S16, S17, S21), CODE-4 (S13, S19) and CODE-6 (S14, S15, S21) each span several checklist steps** OPEN, summary open decision 53 [`[index-reconcile.5]`], against the rule that a deliverable appears in one step. Merging rewrites non-spec steps and their order.
- **Tier-4 fixture extension** OPEN: whether the per-case interceptor perturbs the other `recycleAdapterDialer` call sites, and whether `recycle_scrub_path_test.go` can carry the concurrent-slot compensation case, is untraced.
- **Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?** OPEN, recorded in the summary's unstaged-defects rows: a CREATE, `bound` patch and DELETE inside one reconcile window leaves `("", false)` and an idle unscrubbed pod. Correctly scoped out; do not re-file.
- **Does the gateway double-book a leak for a runtime-given slot whose close fails?** UNVERIFIED: row 2 gives both a `leaked` report and an unclean response into one tracker with no per-session dedup. Check `MarkLeaked` idempotency.
- **Can a `PrepareWorkspace` frame on an already-admitted call write into a tree `removeSlotTree` is deleting?** UNVERIFIED: turns on whether the handler honours the cancelled server context.
- **Does the rate of unanswered reclaims accelerate whole-pod replacement at `maxConcurrentSessions: 2`?** UNVERIFIED: one leaked slot retires the pod there.
- **Does `s.reclaiming` grow without bound over a long-lived recycling pod?** UNVERIFIED, code lane: one entry per failed cleanup, held for the pod's life, and the adapter process survives pod reuse.
- **Does the widened §15.1 row owe `RunSetup`'s third deterministic `FailedPrecondition` producer?** UNVERIFIED: "adapter is not configured with a workspace root" maps to 422 the same way.
- **Does `docs/reference/adapter-contract.md` owe a mirror of the §4.7.1 numbered-rule block?** UNVERIFIED: DOCS-2 stages four edits only.
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
- **Does CODE-10's line-oriented grep miss the wrapped carrier at `tests/tier4_integration/recycle_scrub_path_test.go:1081-1083`?** UNVERIFIED [`[non-spec.9.review-edit-sites.1]`]: "Each" ends one comment line and "release reports the per-slot cleanup outcome" starts the next, and the grep returned zero hits there after round 9's added alternation. No later entry addressed this file; CODE-12's joined-comment form is the known remedy shape.
- **Does `b.stageWorkspace` owe a named edit to carry the attempt token and `mid_session`?** UNVERIFIED [`[non-spec.13.review-mechanism.1]`]: it is the one production carrier of `Client.PrepareWorkspace` on both bind paths and no deliverable names it. Not filed: the client signature change makes the omission a compile error. See the `cl.Close()` refutation in `### Traps`.
- **Does CODE-9's heading owe `pkg/gateway/podlifecycle/podsession/slotfailure.go`?** UNVERIFIED [`[non-spec.10.review-edit-sites.1]`]: the files-touched list places `slotFailureWorkspaceFinalize` there; judged bookkeeping. staticcheck's `unused` marks a const BLOCK used when one member is, so the constant is safe only inside the used group at `binder.go:288-298`.
- **Does the `ErrorCode` enum header owe a repair under SCHEMA-1?** UNVERIFIED: the `### Deferred` entry (from `[index-reconcile.4]`, newer) says the header is false for codes 27 to 29; `[non-spec.10.review-client-surface.1]` and `[non-spec.13.review-client-surface.1]` (older) say it was already loose for six earlier codes, so the additions do not newly falsify it. A later reviewer settles which reading governs.
- **Does `[non-spec.5.fix-G2.1]`'s "a CONF-1 change reaches exactly one site" still hold?** UNVERIFIED [`[non-spec.13.review-single-source.1]`]: it holds for assertion sets and fails for the driver text of rules 2, 6, 8 and 13.

### Deferred

- DEFERRED [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start` failure
  branches release by session identifier alone. Carried in the summary's unstaged-defects section.
- DEFERRED [pkg/gateway/runtime/adapterclient/client.go, `Client.Shutdown` doc comment]: "A zero deadline lets the
  adapter apply its default grace period" is false. CODE-7 opens the file and stages no repair.
- DEFERRED [schemas/lenny-adapter.proto, `ErrorCode` enum header]: "The catalog below mirrors spec §15.1" is false
  for codes 27, 28 and 29, and SCHEMA-1 stages no replacement.
- DEFERRED [spec-changes.md, commentary wording]: the untokened-entry commentary calls the §10.1 hold-timeout
  termination "a request" where §5.2 says it runs under no request. Filed in spec round 1 as a contradiction against the
  §5.2 hold paragraph's "runs under no request" (`[spec.1.review-citations.1]`, `[spec.1.review-fresh.1]`); no fix entry
  is recorded, so it stays open.
- DEFERRED [spec-changes.md, `## Spec files touched`]: the spec/16 bullet says §16.1 gains "one row for each counter",
  and the docs-mirror paragraph after it says `docs/reference/metrics.md` gains "the row for each counter"; both omit the
  `lenny_adapter_leaked_slots` gauge row SPEC-6 stages and CODE-9 mirrors. Filed in spec round 1 as bookkeeping
  (`[spec.1.review-docs-alignment.1]`, `[spec.1.review-edit-sites.1]`); no fix entry is recorded. Preferred remedy is
  reduction: the closing docs paragraph restates each non-spec docs deliverable's site list and becomes a pointer at
  DOCS-1 to DOCS-4, CODE-9 and SCHEMA-1.
- CLOSED by `[index-reconcile.6]`. DEFERRED [implementation-checklist.md, step S1] (`[spec.1.fix-G2.1]`): delete the clause "because the gateway consumes
  both refusals as adapter-contract codes and renders neither into the REST error envelope", so the step reads "§15.1
  takes no new catalog row for either adapter error code;" with no rationale. The clause is false on the concurrent-slot
  path, where the gateway renders a slot-path refusal as §5.2's exhaustion envelope, and it contradicts the corrected
  SPEC-5 §4.7.1 paragraph after rule 9. The spec-round-1 fixer's grant excluded the checklist.
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

## Ledger

### [spec.2.fix-G1.1]
DECISION: removed the owner enumeration from SPEC-5's §4.7.1 paragraph after rule 9; the sentence now ends at "the bind stage that received the refusal." — BECAUSE the enumeration keyed envelopes on pool mode, and a concurrent-pool refusal at the setup-command request renders the §15.1 SETUP_COMMAND_FAILED row (or the 503 fallback), never the §5.2 Client error on exhaustion — ALTERNATIVES: a stage-keyed three-arm list (rejected: third enumeration of the same owners, restates the §15.1 row, omits the fallback); an unkeyed §15.1/§5.2 pointer list (rejected: leaves the selector unstated and goes stale).
WATCHOUT: do not re-add any owner list or §5.2 citation to that §4.7.1 paragraph. Round 1 named one owner, round 2 named two by pool mode, and both were false for some stage/mode pair. The §15.1 SETUP_COMMAND_FAILED row as SPEC-5 widens it owns the setup-command mapping, and the spec-changes Edge-cases bullet "A bind-sequence refusal reaches the client under the envelope its stage already selects." carries the rationale — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:299-304 wraps RunSetup errors in SetupCommandFailure; pkg/gateway/sessionserver/start.go:139 (setupFail) is matched before :186 (slotFailed).

### [spec.2.fix-design-G1.1]
DECISION: delete the envelope enumeration after the colon in the staged §4.7.1 paragraph after rule 9 (SPEC-5); the sentence ends at "...for the bind stage that received the refusal." — BECAUSE the per-stage envelope is already owned elsewhere (the §15.1 SETUP_COMMAND_FAILED row SPEC-5 widens for the setup-command request; existing gateway selection, with §5.2 **Client error on exhaustion** on the concurrent slot path, for every other stage), and each owner list written here has failed: round 1 named one owner, round 2 keyed two owners on pool mode — ALTERNATIVES: the reviewer's three-arm stage-keyed enumeration (a third attempt at the same enumeration; restates the §15.1 row), and naming the owners with no key (a reader still reads an either/or with no selector).
FACT: on the concurrent-workspace slot path a RunSetup refusal is wrapped in SetupCommandFailure inside SlotFailedError, and the /start handler matches setupFail before slotFailed, so a FailedPrecondition there renders 422 SETUP_COMMAND_FAILED and an Aborted there renders the 503 fallback, never SLOT_FAILED — EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:299-304; pkg/gateway/sessionserver/start.go:139, :186-193, :239-246
WATCHOUT: any sentence in §4.7.1 that says WHICH envelope a refusal reaches will be falsified by one of the stage × pool-mode combinations; the stage-keyed lead clause alone is true. Do not re-add owners. — EVIDENCE: review-log `[spec.1.fix-G2.1]` and this finding

### [spec.2.review-client-surface.1]
FACT: on the concurrent-workspace slot path a RunSetup failure is wrapped as SlotBindError{Stage: slotFailureSetup, Err: &SetupCommandFailure{...}} and the start handler's setupFail case (start.go:139) precedes the slotFailed case (start.go:186), so a FailedPrecondition refusal at the setup-command request on a concurrent pool renders 422 SETUP_COMMAND_FAILED, not the §5.2 SLOT_FAILED envelope. EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:299-304; pkg/gateway/sessionserver/start.go:139-147, 186-194, 239-243
WATCHOUT: the §4.7.1 paragraph after rule 9 keys the client envelope on pool mode ("or, on a pool serving concurrent sessions, §5.2"); the real key is the bind stage. The concurrent-path AssignCredentials adapter error is NOT wrapped in CredentialAssignmentError (only lease minting is), so the §5.2 envelope is right for non-setup stages. EVIDENCE: slotbinder.go:370-400
CORRECTS [spec.1.fix-G2.1]: the two-owner split omits the setup-command stage on concurrent pools, which reaches the widened §15.1 SETUP_COMMAND_FAILED row that SPEC-5 itself stages.

### [spec.2.review-mechanism.1]
FACT: on the concurrent-workspace slot path the typed setupFail case in writePodClaimError precedes the slotFailed case, and SlotFailedError keeps the SlotBindError (and its SetupCommandFailure) in its Unwrap chain, so a refusal at the setup-command request on a concurrent pool reaches SETUP_COMMAND_FAILED (FailedPrecondition) or the retryable 503 fallback (Aborted), never 422 SLOT_FAILED. EVIDENCE: pkg/gateway/sessionserver/start.go:139,186-194,237-249,2774-2779,2875-2880; spec/15_external-api-surface.md:1136 (row already names /start on a concurrent-workspace pool)
WATCHOUT: the round-1 rewrite of the §4.7.1 paragraph after rule 9 keys the §5.2 **Client error on exhaustion** arm on the pool type alone; the true key is pool type AND a stage other than the setup-command request. EVIDENCE: spec-changes.md:1060
WATCHOUT: two sites claim to be the home of "which envelope each stage's refusal reaches": the SPEC-5 §15.1 commentary points at the Edge-cases bullet (spec-changes.md:1185-1187), the untouched-§15.1 bullet and summary point at the §4.7.1 paragraph (spec-changes.md:1246-1249, summary.md:90). The Edge-cases bullet (spec-changes.md:206-222) has no concurrent-pool arm.
FACT: hunk 1 (§5.2 hold scope qualified by rule 1) agrees with rule 2 "Applied to a request rule 1 admits" (spec-changes.md:1051) and the §15.4 hold block (spec-changes.md:1113) cites §5.2 for scope; no stale site found.

### [spec.3.review-client-surface.1]
FACT: the round-2 to round-3 diff is one hunk: the §4.7.1 paragraph after rule 9 dropped its explicit envelope mapping (endpoint envelope / §5.2 Client error on exhaustion) and now says only that the client receives the error the gateway already returns for the bind stage. No proto, schema, OpenAPI, SDK, error-code or docs surface changed. EVIDENCE: spec-changes.md:1060
FACT: "Client error on exhaustion" no longer appears anywhere in the proposal outside the review log, so no site still sends a concurrent-pool refusal to that envelope. EVIDENCE: grep over the proposal directory
WATCHOUT: spec-changes.md:1247-1249 (the "§15.1's error catalog takes no new row" bullet) still says the reason and the envelope are "stated once" in the §4.7.1 paragraph after rule 9. The paragraph now gives the envelope only by stage, which still matches that description. The bullet is commentary, so it is not a finding.

### [spec.3.review-mechanism.1]
FACT: The only r2->r3 change is spec-changes.md:1060, where the §4.7.1 paragraph after rule 9 drops its stage-to-envelope mapping (and the §5.2 Client error on exhaustion arm) and now says only that the client receives the error the gateway already returns for the bind stage. The applied §15.1 exclusion sentence (spec-changes.md:1177, "reaches the client under the envelope that stage selects") agrees with it. No staged spec, checklist, or non-spec site still names the dropped concurrent-pool arm. EVIDENCE: grep "Client error on exhaustion" over the proposal dir finds no hit
WATCHOUT: Commentary pointers now disagree about which site states the envelope mapping. spec-changes.md:1248 says it is stated once in the §4.7.1 paragraph after rule 9, but that paragraph no longer states it. spec-changes.md:1185 points at the Edge-cases bullet (:206) instead. Both sites are unapplied commentary. The refuted-list precedent treats this as polish, so I did not file it. A fixer tidying up should reduce :1248 to point at :206. EVIDENCE: spec-changes.md:1246-1249, :1184-1187

### [spec.4.review-applicability.1]
FACT: every verbatim anchor in spec-changes.md's staged edits (SPEC-1 to SPEC-6) matches the current tree exactly once within its scoped section; the multi-hit strings ("regardless of the flag.", the §29.4 "§28.5.3)." tail) are disambiguated by the paragraph or step the instruction names. All 18 section anchors the staged text links resolve to existing headings. — EVIDENCE: spec/04_system-components.md:659 (#### 4.7.1), :848 (#### 4.7.9); spec/16_observability.md:14 (lenny_slot_failure_total row SPEC-6 sits beside), :289 (### 16.1.1); spec/05_runtime-registry-and-pool-model.md:453 (Scrub model), :488 (Session count limit), :545 (Slot cleanup), :561 (Whole-pod replacement trigger)
WATCHOUT: SPEC-4's fence edit for the §6.2 `claimed ──→ draining` Occupancy-projection entry shows only the REPLACEMENT text (no verbatim "reads" block); the tree's current entry at spec/06_warm-pod-model.md:95-97 reads "claim deleted on a pod with recycle.enabled: false". The target is unique by block, so it is appliable; do not mistake the shown block for the current text. Likewise the §4.6.1 bullet's quoted closing sentence renders "§6.2" where the tree has a link (spec/04_system-components.md:416); the whole bullet is replaced, so it is not an anchor defect.
FACT: the round-3 diff is one hunk (the §4.7.1 envelope paragraph after rule 9 lost its owner list); no staged spec block cites that paragraph for an envelope, only commentary at spec-changes.md:1185-1187 and :1246-1249 does.

### [spec.4.review-citations.1]
FACT: every verbatim anchor the staged spec edits replace or append after still resolves in the tree, re-checked in round 4: §4.1 third sentence (spec/04:157), §4.7 `Shutdown`/`DemoteSDK`/`ConfigureWorkspace`/`ReportSessionScrub` rows (spec/04:686, :674, :673, :692), §4.6.1 claim-deletion bullets (spec/04:415-416), §4.7.9 step 5 (spec/04:854), §5.2 slot-cleanup/scrub-model/whole-pod-trigger sentences (spec/05:545, :453, :561), §6.2 fence entries (spec/06:95-97, :148, :152-155) and client-visibility clause (spec/06:290), §7.1 atomicity paragraph (spec/07:23), §7.2 mid-resume block (spec/07:210-214), §7.3 list item 4 (spec/07:414), §12.6 prose and DDL (spec/12:481, :494), §15.1 `SETUP_COMMAND_FAILED` row (spec/15:1136), §15.4 SDK-warm demotion contract and 15.4.1 heading (spec/15:1469, :1471), §29.4 step 13 (spec/29:705-711). EVIDENCE: grep -F of each quoted string
WATCHOUT: the §4.6.1 closing sentence SPEC-4 quotes as "The one-session-only invariant of §6.2 is ..." reads "[Section 6.2](06_warm-pod-model.md#62-pod-state-machine)" in the tree; the block locates it by "bullet beginning", so it is not a finding. EVIDENCE: spec/04:416
WATCHOUT: the ending "([§15.4.3](...), §28.5.3)." occurs three times in spec/29 (:645, :711, :982); only :711 is §29.4 step 13, which SPEC-1 names by step number, so the anchor is unambiguous. EVIDENCE: spec/29:575, :705-711
FACT: commentary code citations verified: holdstate.go:201 10s pass-2 context; commit 3997f502b dated 2026-08-22; spec/11:264 "wait up to 10s"; socketruntime.go:470-473; mcpruntime.go:86 5s; maxSlotRetries = 1 (start.go:2720); RecordSessionScrub increments sessions_served (scrubreport_server.go:451-457); RemoveTree covers CredentialsDir (slotlayout/tree.go:58-69); PROTOCOL_VERSION_INCOMPATIBLE is ErrorCode 27 and the §15.4.2 INIT row (spec/15:1699); §28 names no `Shutdown`.
USEFUL [recurring non-findings, third list]: `session.go:219-224` for the gateway's `deadline_ms` is already listed there; saved a re-file.

### [spec.4.review-client-surface.1]
FACT: the diff against spec-r2-prefix is the same single hunk round 3 reviewed (spec-changes.md:1060, the §4.7.1 paragraph after rule 9 dropping its envelope enumeration). No text changed in round 3's fix stage. EVIDENCE: diff -ru output
FACT: no client SDK (sdks/client/*, sdks/runtime/*) and no served OpenAPI document (pkg/gateway/externalapi/openapi/openapi.json; the lens prompt's pkg/gateway/openapi path does not exist) names SETUP_COMMAND_FAILED, setup_command_failed, or any adapter ErrorCode. The widened §15.1 row therefore has no SDK or OpenAPI parallel. Its only mirror is docs/reference/error-catalog.md:129, which DOCS-3 carries. EVIDENCE: grep over sdks/ and pkg/gateway/externalapi/openapi/
FACT: the §15.1 endpoint rows (spec/15:625, :646, :647, :720) say only that a deterministic non-zero setup command surfaces as SETUP_COMMAND_FAILED. They make no exclusivity claim, so the widened row leaves them true.
FACT: the graceful-shutdown condition that SPEC-1's §4.7 Shutdown row states (the signal goes out only when no bound entry remains) is shipped behaviour (pkg/adapter/session.go:259-261, `if !boundRemains`). It is not a runtime-facing behaviour change, so no runtime SDK owes an edit.
WATCHOUT: spec/15:1462 names `examples/runtimes/echo/` as the Go reference adapter "built from the same .proto file". That directory does not exist; cmd/runtimes/echo is a JSONL runtime and does not implement the adapter. This is pre-existing and not a parallel representation that this proposal owes.

### [spec.4.review-docs-alignment.1]
FACT: the r3->r4 diff is one hunk, the §4.7.1 paragraph after rule 9, which now ends at "the bind stage that received the refusal." No docs-facing identifier, code, metric or row changed, so no docs mirror moves with it. EVIDENCE: spec-changes.md:1060
FACT: the SPEC-5 §6.2 Client-visibility rewrite drops the "any other setup-window failure stays the retryable STARTING_FAILED fallback" half; the §15.1 exclusion sentence (spec/15_external-api-surface.md:1136, as SPEC-5 replaces it) still names the STARTING_FAILED fallback, so the mapping keeps a spec home. EVIDENCE: spec/06_warm-pod-model.md:290
USEFUL [Settled: the remedy for an accepted-residue finding is ONE SENTENCE]: the gateway-crash and self-recreated-entry residues appear only in the Edge-cases section and decision 39 keeps the emptied-workspace outcome proposal-only; not re-filed.

### [spec.4.review-edit-sites.1]
FACT: every "reads, verbatim:" anchor block in spec-changes.md (SPEC-1 to SPEC-5, 26 blocks) resolves exactly once in spec/ as of spec round 4; checked mechanically by extracting each fenced block and substring-matching the concatenated spec/*.md. The inline SPEC-4 §6.2 clause anchors (spec/06:80) and the fence original (spec/06:95-97) also resolve. EVIDENCE: spec/04:157,:674,:686,:692; spec/05:453,:545,:561; spec/06:95,:148,:150-152,:234,:290; spec/07:210,:214,:388; spec/12:481,:494; spec/15:1136
FACT: the §16.1.1 `k8s.pod.name` row's "Used on" cell (spec/16:297) enumerates scrub/retirement, slot failure/replacement and the reuse histogram; the SPEC-6 superseded counter adds a k8s_pod_name user outside that list. Judged below the bar (a descriptive "used on" cell, not an exclusive predicate); do not re-chase unless a gate reads it (none under tests/ or scripts/). EVIDENCE: spec/16:297, grep of tests/ scripts/ empty
FACT: no §16.5 alert and no pkg/alerting/rules entry references leaked slots or slot failures, so SPEC-6 owes no runbook companion. EVIDENCE: grep -i leak spec/16 (only CredentialCompromised), grep pkg/alerting docs/runbooks empty
WATCHOUT: spec/28:1124 names a credential-rotation fallback (checkpoint, pod termination, replacement pod, AssignCredentials, Resume) that is a bind onto a replacement pod; whether §7.1's reclaim obligation reaches it depends on reading it as a §7.3 re-attach. Pre-existing scope question, not an edit site. EVIDENCE: spec/28_communication-channels.md:1118-1125
USEFUL [recurring non-findings, third list]: "§6.2's projection-input enumeration becomes incomplete" and the spec/07:208 sufficient-condition reading saved re-deriving both.

### [spec.4.review-fresh.1]
FACT: every "reads, verbatim" anchor in spec-changes.md (SPEC-1 through SPEC-5, 28 anchors across spec/04, 05, 06, 07, 12, 15, 29) resolves exactly once in the current tree, apart from the §29.4 step-13 tail phrase (3 hits, disambiguated by "step 13"). The SPEC-4 `claimed ──→ draining` fence edit gives only replacement text; the original is spec/06_warm-pod-model.md:95-97 and is the only entry in that group carrying "claim deleted". EVIDENCE: grep -c -F per anchor, run at round 4
FACT: the only r3->r4 change is the §4.7.1 paragraph after rule 9 (spec-changes.md:1060), which now ends at "the bind stage that received the refusal." It agrees with the §15.1 exclusion replacement at :1177. EVIDENCE: diff against scratchpad/cp-snap/0081-opt3/spec-r2-prefix
FACT: the claim register §28.4 names is `tests/claim-map.json` (spec/28_communication-channels.md:161-165), so the SPEC-5 commentary's ABSENT row is a non-spec edit and spec/28 is correctly absent from "Spec files touched".
FACT: spec/06_warm-pod-model.md:386 (§6.4) says the adapter creates the slot tree "on the first reference to that identifier". Rules 1 and 3 now make that sentence loose for a malformed request and for a mid-session first reference. It was already loose before this proposal (a re-created tree and a Shutdown to an unknown session are not first references that create), so I did not file it.
USEFUL [Traps: recurring non-findings, third list]: it already covers the §6.2 projection-input enumeration, the §7.2 step 3 connection wording, and the §4.7 row tail "On the default disposition the pod is replaced", and it saved three re-derivations.

### [spec.4.review-kubernetes.1]
DECISION: empty findings for the Kubernetes-idiom lens in spec round 4. BECAUSE the only change since round 3 is the SPEC-5 §4.7.1 envelope sentence losing its owner enumeration (spec-changes.md:1060), which touches no CRD, status, finalizer, webhook or controller surface. Every Kubernetes-facing staging (SPEC-4 §4.6.1/§6.2 occupancy re-key, ReportSessionScrub rows feeding the drain-request annotation) verified against the tree. ALTERNATIVES: re-filing the claim CREATE + bound + DELETE coalescing window against SPEC-4's "phase the pod currently projects" input was rejected, because it is recorded in the Open list as a summary unstaged-defects row ("Correctly scoped out; do not re-file").
FACT: OccupancyReconciler reads the claim from the manager cache with no finalizer on SandboxClaim, and ProjectOccupancyPhase's no-claim branch switches on o.Current (its own last-written Sandbox.status.phase). SPEC-4's staged §4.6.1 bullets transcribe that exactly. EVIDENCE: pkg/controller/warmpool/occupancy.go:113-143, :181-215, :298-313
USEFUL [Open: "Does the §4.6.1 claimed-and-released-between-two-reconciles window need a remedy?"]: this is the fourth time this lens has landed on it. Read that entry first and do not re-derive it.

### [spec.4.review-mechanism.1]
FACT: the r2-prefix -> current diff is still the single §4.7.1 hunk after rule 9 (envelope owner list dropped); no staged spec text changed in round 3. EVIDENCE: diff -ru of scratchpad/cp-snap/0081-opt3/spec-r2-prefix against the proposal dir
FACT: every tree entry creator is one of the seven RPCs §4.7.1 Admission lists: staging.go (Prepare/Finalize/RunSetup via ensureSlotPaths), slotcreds.go (AssignCredentials), session.go/resume.go (StartSession/Resume via claimSessionSlot), sdkwarm.go:217 (ConfigureWorkspace). Rule 13's "an untokened entry is one a StartSession or ConfigureWorkspace created" holds. EVIDENCE: pkg/adapter/slot.go:105, slotsession.go:75, slotcreds.go:26
FACT: the tree has no resume-time RunSetup and no mid-session AssignCredentials (rotation is RotateCredentials), so rule 1's non-empty-token demand on RunSetup/AssignCredentials/Resume breaks no shipped caller. EVIDENCE: grep of `RunSetup(` and `.AssignCredentials(` in pkg/gateway returns only binder.go:933/1256 and slotbinder.go:299/403
FACT: Binder.Prepare issues DemoteSDK before stageWorkspace, so the DemoteSDK row's registry removal never destroys the same attempt's staged workspace. EVIDENCE: pkg/gateway/podlifecycle/podsession/binder.go:878-918
WATCHOUT: §4.6.1 after SPEC-4 names no projection for a claim deleted while the pod projects sdk_connecting; the tree (occupancy.go:128-143) leaves it to the reconciler too, so it is not a new defect. Do not file it.
USEFUL [archive, the hold-timeout sessions_served entries at 60445 and 63649]: the biconditional's exclusion of the §10.1 hold-timeout release from sessions_served is shipped (holdstate.go:215-218), not new.

### [spec.4.review-performance.1]
- FACT: the spec-round-3 diff is one deletion (the §4.7.1 paragraph after rule 9 drops its two-envelope clause); it adds no write, watch, label or lock. EVIDENCE: spec-changes.md:1060
- FACT: SPEC-6's new `lenny_slot_compensation_superseded_total` labels (`pool`, `k8s_pod_name`, bounded `cause`) match the cardinality of the shipped slot metrics `lenny_slot_failure_total` and `lenny_slot_pod_replacement_total`; `k8s_pod_name` is catalogued for slot metrics. EVIDENCE: spec/16_observability.md:14-15, :297
- USEFUL [Settled "The staging adds NO per-session or per-request write to etcd, Postgres or Redis"]: re-confirmed against SPEC-3 §12.6 (increment trigger moves onto the report, no new row or column) and SPEC-4 §4.6.1 (the new projection input is the controller's own status field, already in its informer cache, so no net-new watch). Nothing for this lens.

### [spec.4.review-reliability.1]
FACT: the r3->r4 diff is again the single §4.7.1 paragraph after rule 9 (spec-changes.md:1060); the reliability surface (§7.1 reclaim, §5.2 table and hold, §7.2 step 3, §4.7.1 rules 8 and 10-15) is unchanged since round 3. EVIDENCE: diff -ru against scratchpad/cp-snap/0081-opt3/spec-r2-prefix
FACT: a reclaim the adapter does not acknowledge clean still keeps the slot counted: the shipped release takes a leaked disposition (claimer.ReleaseSlot(ctx, name, false, leaked)) and CODE-4 threads sbe.Leaked into ReleaseSlotReservation, so §7.1's "releases the slot reservation afterwards" does not contradict §6.2's "remains counted in the pod's Redis slot-counter occupancy". EVIDENCE: pkg/gateway/podlifecycle/podsession/slotbinder.go:493-503; non-spec-changes.md:1166-1190; spec/06_warm-pod-model.md:160
WATCHOUT: a create-time-reserved session whose reserved pod holds its identifier for the pod's life is refused ABORTED on every client /start retry until the pod retires (row keeps its §4.6 pod binding, start.go bindConcurrentSlot). This is decision 41 (`[operator.20-41-45]`, Settled: one transient ABORTED on both hold arms); do not re-file as a stall. EVIDENCE: pkg/gateway/sessionserver/start.go:2594-2604
WATCHOUT: §7.2 step 3 says the reclaim goes "on the connection that attempt still holds" while §7.1 makes the connection a preference only; CODE-4 sends only on the held connection. Below the bar (a dead connection yields an unanswered reclaim, which the §5.2 table accounts as leaked). EVIDENCE: spec-changes.md:385, :460; non-spec-changes.md:1009-1012
USEFUL [Settled: "the reclaim hold ends only on a cleanup that COMPLETED"] and [Settled: decision 41]: they closed the two recovery-stall candidates this lens would otherwise have filed.

### [spec.4.review-security.1]
FACT: the r3->r4 diff is the single §4.7.1 sentence after rule 9 losing its envelope enumeration; it touches no security control. EVIDENCE: diff against scratchpad/cp-snap/0081-opt3/spec-r2-prefix, spec-changes.md:1060
FACT: the spec already requires mTLS on the gateway-adapter contract, which bounds the "in-pod agent sends a co-tenant's unconditional Shutdown" worry at the spec level; whether pkg/adapter's server enforces it is a code-lane question. EVIDENCE: spec/04_system-components.md:663 "Contract (internal gRPC/HTTP+mTLS API — gateway ↔ adapter)"
FACT: SPEC-4's re-keyed §4.6.1 bullet ("deleted while the pod projects claimed ... on a pool of either recycle setting") is stricter than the deleted one-session-only sentence, and the remaining one-session-only citations (spec/06:395, :402) stay true. So §7.2 step 3's "released back to the pool" of a replacement pod that may hold a started runtime drains that pod instead of reusing it. EVIDENCE: spec-changes.md SPEC-4 §4.6.1 block; spec/04_system-components.md:416
FACT: every session the narrowed report biconditional leaves uncounted in sessions_served either never reached running or ended on a pod that retires anyway (§10.1 hold timeout: the single-session adapter exits, and a concurrent pod takes whole-pod replacement). sessions_served was already adapter-report-sourced before this proposal (spec/12:481), so the proposal moves no trust boundary. EVIDENCE: spec/10_gateway-internals.md:58, :62
USEFUL [spec.1.review-security.1]: the credentials.json-residue settlement saved re-deriving the lenny-cred-readers boundary.

### [spec.4.review-single-source.1]
DECISION: returned an empty findings list for the single-source lens in spec round 4. BECAUSE the r3-to-r4 diff is one hunk: spec-changes.md's §4.7.1 paragraph after rule 9 drops its envelope enumeration. That leaves one applied clause at §4.7.1 ("the error the gateway already returns for the bind stage") and one at the widened §15.1 exclusion ("the envelope that stage selects"). Each is a one-clause pointer at existing gateway selection, not a restated mapping. ALTERNATIVES: I considered and rejected filing the §16.1 superseded-counter row's gloss of rule 15's `superseded` meaning, because it is a metric description that cites §4.7.1. I also rejected the carriage table's cells against rule 1, which the Settled entry and archive record as sender obligation versus receiver refusal.
USEFUL [Standing context, "The single-source inventory has not moved since round 11"]: re-walking rules 1-15, the critical section, stamp-once, the disposition table, the hold paragraph, the report biconditional, §7.1, §15.4 and §15.1 against it found no new stating site.
WATCHOUT: the `## Spec sections deliberately untouched` bullet on §15.1 still says the envelope is "stated once" in the §4.7.1 paragraph after rule 9, and SPEC-5's §15.1 commentary points at the Edge-cases bullet instead. Both are unapplied commentary, and the refuted list covers this as polish. EVIDENCE: spec-changes.md:1246-1249, :1182-1187

### [index-reconcile.6]
FACT: the staged deliverables are SPEC-1 through SPEC-6, CODE-1 through CODE-12, CONF-1, SCHEMA-1 and DOCS-1 through DOCS-4, unchanged in set since `[index-reconcile.5]`; the deliverable index lists each once with its files. One index line changed: CODE-11 now names its three files (`errorclassify.go`, `start.go` and `resume_setup_demotion_internal_test.go`) in place of "the four sites CODE-11 names in `pkg/gateway`". EVIDENCE: `### ` headings in spec-changes.md and non-spec-changes.md against the summary's `## Deliverable index`.
FACT: the SPEC-lane block keeps its ids, order and dependency lines (S1 SPEC-5, S2 SPEC-1, S3 SPEC-2, S4 SPEC-3, S5 SPEC-4, S6 SPEC-6), one lane per step. Each line is now a pointer naming the sections it lands, and keeps only the ordering and tier notes: S1's forward references to S4, S5's tier deferral to S7 and S8, S3's untouched `**Max retries:**` bullet, and S4's tier-11 `sessions_served` sweep, kept verbatim because SPEC-3's carrier table cites "checklist S4's tier-11 sweep" as that work's only home.
FACT: every non-spec step's `Depends on:` already names the spec steps staging what it implements; no dependency line changed this pass.
CORRECTS [DEFERRED implementation-checklist.md, step S1, from `[spec.1.fix-G2.1]`]: S1 now reads "§15.1 takes no new catalog row for either adapter error code." with no rationale; the false clause "because the gateway consumes both refusals as adapter-contract codes and renders neither into the REST error envelope" is deleted.
OPEN [non-spec-changes.md, CODE-7, lands in pkg/gateway/runtime/adapterclient/client.go]: the `Client.Shutdown` doc comment's "A zero deadline lets the adapter apply its default grace period" is false; staging the repair is authoring a code change for the non-spec loop. Carried.
OPEN [non-spec-changes.md, SCHEMA-1, lands in schemas/lenny-adapter.proto]: the `ErrorCode` enum header's "The catalog below mirrors spec §15.1" is false for codes 27, 28 and 29, and SCHEMA-1 stages no replacement. Carried.
OPEN [non-spec-changes.md, SCHEMA-1, lands in schemas/lenny-adapter.proto]: the `LEAKED` comment's drain-ledger sentence may restate a rule whose home is §4.6.3 or §5.2; a reduction to a citation is a finding of its own. Carried.
OPEN [non-spec-changes.md, DOCS-2, lands in docs/reference/adapter-contract.md]: the page gives the §15.4 reclaim hold's `ABORTED` refusal neither a sentence nor an exclusion rationale; either is new DOCS-2 content. Carried.
OPEN [non-spec-changes.md, CODE-6 and the §10.1.4 parked-member Edge-cases bullet]: the expired-acquisition disposition's premature-removal claim is false for a pass member whose acquisition expired because another member's park spent the pass's single guard-acquisition context, and "leaves every remaining member" is too wide; the remedy is a code-lane design choice, which this pass does not make. Carried.
OPEN [spec-changes.md, commentary]: the untokened-entry commentary calls the §10.1 hold-timeout termination "a request" where §5.2 says it runs under no request. Carried; this pass may not edit spec-changes.md.
OPEN [spec-changes.md, `## Spec files touched`]: the spec/16 bullet and the docs-mirror sentence after it omit the `lenny_adapter_leaked_slots` gauge row SPEC-6 stages and CODE-9 mirrors. Carried; this pass may not edit spec-changes.md.
OPEN [pkg/adapter/session.go, resume.go, sdkwarm.go, a later proposal]: the pre-`Runtime.Start` failure branches release by session identifier alone; recorded in the summary's unstaged-defects section. Carried.
OPEN [pkg/apis/lenny/v1alpha1/sandbox_types.go]: the `Sandbox.status.phase` doc comment lacks the projection input SPEC-4 adds; record only, and any fix goes through the Go comment plus `make generate`. Carried.
OPEN [docs/api/internal.md]: the gRPC status table omits `ABORTED`; pre-existing, for a separate finding. Carried.
DECISION: routed three standing Opens to the summary as open decisions 51 (§5.2 naming the `SLOT_FAILED` code value and 422 status), 52 (whether SPEC-6's `lenny_adapter_leaked_slots` row describes the series the gateway emits) and 53 (CODE-1, CODE-4 and CODE-6 each spanning several steps), each with the ground the log gives and no recommendation, and annotated the §15.1-split Open as decision 29 — BECAUSE each is a choice for a human that no summary entry carried — ALTERNATIVES: leaving them in the log, rejected because the reviewer never reads it. Not routed: "Is the §5.2 whole-pod replacement trigger diluted across gateway replicas?", a pre-existing condition the archive records as a refuted near-filing against this proposal, and the Opens marked UNVERIFIED, FILED or "the next prune decides", which are verification questions or loop work.
